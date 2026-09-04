package projection

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/textmatch"
)

// Patient is the patient as a screen shows them (CP29).
//
// **Synchronous.** A patient is registered and walks straight to Anthropometry, whose
// operator searches for them. A read model a second stale there stops the queue at the
// second station while somebody retypes a name — the same failure §4.1 names for vitals,
// and worse here because it happens to every patient rather than to some.
//
// The derivation is `read.apply_patient_registered(jsonb)` (migration 00017). This type is
// the Go side: which events matter, and what the function is given. The payload is passed
// through **unmodified** with the envelope's attribution merged in — no field is renamed,
// defaulted or computed on the way — which is what lets a test compare the ledger row and
// the read row field by field and what leaves nowhere for a value to be quietly dropped.
type Patient struct{}

var _ Projection = Patient{}

func (Patient) Name() string { return "patient" }

// Version 4 from CP36: consent is derived here too. A projection whose registered version
// differs from the stored one is rebuilt before it is trusted (§7.8), which is exactly right
// here — rows built under version 3 have no consent state at all, and a cohort flag that
// defaults to false is the safe thing to rebuild from.
//
// Consent lives on the patient projection rather than in one of its own because it is
// applied in the same transaction as the event and read in the same breath as the record: a
// separate projection would be a second checkpoint that can lag, and a consent that lags is
// a consent that is briefly wrong in the direction of doing the thing.
func (Patient) Version() int { return 4 }
func (Patient) Mode() Mode   { return Synchronous }

func (Patient) Handles(eventType string) bool {
	switch eventType {
	case "PATIENT_REGISTERED", "PATIENT_MERGED", "PATIENT_DEMOGRAPHICS_CORRECTED",
		"CONSENT_GRANTED", "CONSENT_REVOKED":
		return true
	}
	return false
}

func (p Patient) Apply(ctx context.Context, tx pgx.Tx, e eventstore.Event) error {
	switch e.EventType {
	case "PATIENT_REGISTERED":
		return p.registered(ctx, tx, e)
	case "PATIENT_MERGED":
		return p.merged(ctx, tx, e)
	case "PATIENT_DEMOGRAPHICS_CORRECTED":
		return p.corrected(ctx, tx, e)
	case "CONSENT_GRANTED":
		return p.consent(ctx, tx, e, `SELECT read.apply_consent_granted($1::jsonb)`)
	case "CONSENT_REVOKED":
		return p.consent(ctx, tx, e, `SELECT read.apply_consent_revoked($1::jsonb)`)
	}
	return nil
}

// consent applies a grant or a revocation (CP36).
//
// The two share this because they differ only in the derivation they call: both carry the
// same attribution, both are applied in the transaction that wrote the event, and both have
// to reach `research.research_subject` through `identity_link`, which only a SECURITY
// DEFINER function may cross. Revocation in particular has to be *immediate* — a revocation
// that propagates on a schedule has a window in which the clinic is still doing the thing.
func (Patient) consent(ctx context.Context, tx pgx.Tx, e eventstore.Event, call string) error {
	var row map[string]any
	if err := json.Unmarshal(e.Payload, &row); err != nil {
		return fmt.Errorf("decoding %s: %w", e.EventType, err)
	}
	row["occurred_at"] = e.OccurredAt
	row["actor_id"] = e.Actor.UserID().String()
	row["actor_code"] = e.Actor.Code()
	row["recorded_at"] = e.RecordedAt
	row["event_id"] = e.EventID.String()
	row["global_seq"] = e.GlobalSeq
	encoded, err := json.Marshal(row)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, call, encoded); err != nil {
		return fmt.Errorf("%s: %w", e.EventType, err)
	}
	return nil
}

// corrected applies a demographic correction: the history row, the read model, and the
// anonymised cohort row, in one function so a replay produces all three or none (CP35).
func (Patient) corrected(ctx context.Context, tx pgx.Tx, e eventstore.Event) error {
	var row map[string]any
	if err := json.Unmarshal(e.Payload, &row); err != nil {
		return fmt.Errorf("decoding %s: %w", e.EventType, err)
	}
	// The search key follows the name. Computed here for the same reason it is on
	// registration: the rules are Bengali transliteration habits that will be tuned, and a
	// plpgsql copy would drift.
	if name, ok := row["name_en"].(string); ok {
		row["name_key_en"] = textmatch.Key(name)
	}
	row["event_id"] = e.EventID.String()
	row["global_seq"] = e.GlobalSeq
	row["recorded_at"] = e.RecordedAt
	encoded, err := json.Marshal(row)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT read.apply_patient_corrected($1::jsonb)`, encoded); err != nil {
		return fmt.Errorf("read.apply_patient_corrected: %w", err)
	}
	return nil
}

// merged marks the losing record and points it at the survivor. A redirect, never a
// delete: every event ever written against the losing record still names it, and deleting
// the row would orphan a decade of somebody's history (CP30).
func (Patient) merged(ctx context.Context, tx pgx.Tx, e eventstore.Event) error {
	var row map[string]any
	if err := json.Unmarshal(e.Payload, &row); err != nil {
		return fmt.Errorf("decoding %s: %w", e.EventType, err)
	}
	row["event_id"] = e.EventID.String()
	row["global_seq"] = e.GlobalSeq
	row["recorded_at"] = e.RecordedAt
	encoded, err := json.Marshal(row)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT read.apply_patient_merged($1::jsonb)`, encoded); err != nil {
		return fmt.Errorf("read.apply_patient_merged: %w", err)
	}
	return nil
}

func (Patient) registered(ctx context.Context, tx pgx.Tx, e eventstore.Event) error {
	if e.PatientID == nil {
		return fmt.Errorf("%s has no patient_id", e.EventType)
	}

	// The payload as it was written, then the attribution the envelope carries. Decoding
	// into a struct and re-encoding would introduce a mapping layer whose only job is to
	// be identical to the payload, which is one more place for a field to go missing.
	var row map[string]any
	if err := json.Unmarshal(e.Payload, &row); err != nil {
		return fmt.Errorf("decoding %s: %w", e.EventType, err)
	}
	row["patient_id"] = e.PatientID.String()
	// The phonetic search key, computed here rather than in the derivation: the rules are
	// Bengali transliteration habits that will be tuned against real spellings during the
	// pilot, and a plpgsql copy of them would drift from the Go one within a month
	// (CP30, internal/platform/textmatch).
	if name, ok := row["name_en"].(string); ok {
		row["name_key_en"] = textmatch.Key(name)
	}
	row["registered_at"] = e.OccurredAt
	row["registered_by"] = e.Actor.UserID().String()
	row["registered_role"] = e.Actor.Role()
	row["registered_station"] = e.Actor.Station()
	row["recorded_at"] = e.RecordedAt
	row["event_id"] = e.EventID.String()
	row["global_seq"] = e.GlobalSeq

	encoded, err := json.Marshal(row)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT read.apply_patient_registered($1::jsonb)`, encoded); err != nil {
		return fmt.Errorf("read.apply_patient_registered: %w", err)
	}
	return nil
}

func (Patient) Reset(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT read.reset_patient()`)
	return err
}
