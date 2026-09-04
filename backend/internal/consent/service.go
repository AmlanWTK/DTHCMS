package consent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Service takes and withdraws consent (CP36).
//
// Both are events on the PATIENT aggregate, appended in the same transaction as the
// projection that derives from them. Revocation in particular has to be immediate: §15.1's
// criterion is one minute, and a revocation that propagates on a schedule has a window in
// which the clinic is still sending the messages.
type Service struct {
	store  *Store
	events *eventstore.Store
	clock  interface{ Now() time.Time }
	// gate is the one the rest of the process holds. Invalidated on every write, so a
	// revocation takes effect on the next question rather than up to CacheTTL later —
	// which is what keeps §15.1's one-minute criterion a property of the design instead of
	// a number somebody tuned.
	gate *Gate
}

func NewService(store *Store, events *eventstore.Store, clk interface{ Now() time.Time }) *Service {
	return &Service{store: store, events: events, clock: clk}
}

// Watching tells the service which gate to invalidate. Separate from the constructor
// because the gate reads through the store the service writes to, and one of the two has to
// be built first.
func (s *Service) Watching(gate *Gate) *Service {
	s.gate = gate
	return s
}

// Grant records a consent against the wording in force.
//
// The template is looked up **here**, not sent by the client. A client that could name the
// version it consented against is a client that could record a consent to words the patient
// never saw — and the version, the language and the digest of the text are the only things
// that make the record mean anything years later.
func (s *Service) Grant(ctx context.Context, patientID uuid.UUID, in Grant) (Record, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Record{}, err
	}
	if err := in.validate(); err != nil {
		return Record{}, err
	}
	if in.Language == "" {
		in.Language = "bn"
	}
	tpl, err := s.store.ActiveTemplate(ctx, in.ConsentType, in.Language)
	if err != nil {
		return Record{}, err
	}

	payload, err := json.Marshal(eventstore.ConsentGranted{
		FacilityID: actor.FacilityID().String(), PatientID: patientID.String(),
		ConsentType: string(in.ConsentType), TemplateVersion: tpl.Version,
		Language: tpl.Language, TemplateDigest: tpl.Digest,
		CaptureMethod: string(in.CaptureMethod),
		EvidenceKey:   in.EvidenceKey, EvidenceSHA256: in.EvidenceSHA256,
		PaperReference:     in.PaperReference,
		WitnessedBy:        optionalID(in.WitnessedBy),
		GrantedForRelation: in.GrantedForRelation, GrantedForName: in.GrantedForName,
	})
	if err != nil {
		return Record{}, err
	}

	if err := s.append(ctx, patientID, in.EventID, "CONSENT_GRANTED", payload, in.Source); err != nil {
		return Record{}, err
	}
	s.invalidate(patientID)
	return s.store.One(ctx, patientID, actor.FacilityID(), in.ConsentType)
}

// Revoke withdraws a consent.
//
// Refused when there is nothing to revoke, because a revocation of a consent nobody took
// records a withdrawal that never happened — and a patient asking "did you stop" deserves a
// truthful answer rather than a reassuring row.
//
// Revoking an already-revoked consent is *not* refused: it is a retry, and the second one
// changes nothing.
func (s *Service) Revoke(ctx context.Context, patientID uuid.UUID, in Revocation) (Record, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Record{}, err
	}
	if !Known(in.ConsentType) {
		return Record{}, fmt.Errorf("%w: %q", ErrUnknownType, in.ConsentType)
	}
	if strings.TrimSpace(in.RequestedBy) == "" {
		in.RequestedBy = "patient"
	}

	current, err := s.store.One(ctx, patientID, actor.FacilityID(), in.ConsentType)
	if err != nil {
		return Record{}, err
	}
	if current.Status == Absent {
		return Record{}, fmt.Errorf("%w: %s", ErrNotGranted, in.ConsentType)
	}

	payload, err := json.Marshal(eventstore.ConsentRevoked{
		FacilityID: actor.FacilityID().String(), PatientID: patientID.String(),
		ConsentType: string(in.ConsentType),
		Reason:      strings.TrimSpace(in.Reason), RequestedBy: in.RequestedBy,
	})
	if err != nil {
		return Record{}, err
	}

	if err := s.append(ctx, patientID, in.EventID, "CONSENT_REVOKED", payload, in.Source); err != nil {
		return Record{}, err
	}
	s.invalidate(patientID)
	return s.store.One(ctx, patientID, actor.FacilityID(), in.ConsentType)
}

// append writes the event and applies the derivation in one transaction.
//
// One transaction, not two, and this is the whole reason revocation meets its one-minute
// budget without any machinery: the row that says "do not send" is written by the same
// COMMIT that writes the event saying so. There is no interval in which the ledger and the
// gate disagree.
func (s *Service) append(ctx context.Context, patientID, eventID uuid.UUID, eventType string,
	payload []byte, source eventstore.Source) error {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return err
	}
	// A retried request carries the same event id, and the ledger's uniqueness is the
	// idempotency check: the consent is already recorded, and the caller reads back the
	// same state it would have read the first time.
	if existing, err := s.events.ByID(ctx, eventID); err == nil {
		if existing.EventType != eventType {
			// The same id used for something else entirely. Refused rather than answered:
			// the client has a bug, and a plausible-looking consent would hide it.
			return fmt.Errorf("%w: that event id was used for a %s", ErrReplayed, existing.EventType)
		}
		return nil
	}

	now := s.clock.Now().UTC()
	id := patientID
	return s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbgen.Queries) error {
		written, err := s.events.AppendInTx(ctx, tx, eventstore.Envelope{
			EventID: eventID, AggregateType: "PATIENT", AggregateID: patientID, PatientID: &id,
			EventType: eventType, EventVersion: 1,
			OccurredAt: now, Actor: actor, Source: source, Payload: payload,
		})
		if err != nil {
			return err
		}
		return applyConsent(ctx, tx, written, actor)
	})
}

// applyConsent calls the derivation for the event just written.
//
// Deliberately in this package rather than reached for through the projection register: the
// projection is what a *rebuild* runs, and this is what the write path runs, and they call
// the same SQL function so there is one derivation and not two.
func applyConsent(ctx context.Context, tx pgx.Tx, e eventstore.Event, actor eventstore.Actor) error {
	var row map[string]any
	if err := json.Unmarshal(e.Payload, &row); err != nil {
		return err
	}
	row["occurred_at"] = e.OccurredAt
	row["actor_id"] = actor.UserID().String()
	row["actor_code"] = actor.Code()
	row["recorded_at"] = e.RecordedAt
	row["event_id"] = e.EventID.String()
	row["global_seq"] = e.GlobalSeq
	encoded, err := json.Marshal(row)
	if err != nil {
		return err
	}
	call := `SELECT read.apply_consent_granted($1::jsonb)`
	if e.EventType == "CONSENT_REVOKED" {
		call = `SELECT read.apply_consent_revoked($1::jsonb)`
	}
	if _, err := tx.Exec(ctx, call, encoded); err != nil {
		return fmt.Errorf("%s: %w", e.EventType, err)
	}
	return nil
}

func (s *Service) invalidate(patientID uuid.UUID) {
	if s.gate != nil {
		s.gate.Forget(patientID)
	}
}

func optionalID(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}
