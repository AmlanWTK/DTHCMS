package visit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Service opens, closes and walks visits (CP38).
//
// Every transition is an event and a row change in **one transaction**, exactly as
// registration is: a visit row with no event behind it is a fact with no history, and an
// event with no row is a queue board that does not know the patient arrived.
type Service struct {
	store  *Store
	events *eventstore.Store
	clock  interface{ Now() time.Time }
	// notifier tells the traffic board that the floor changed (CP40). Never nil: a service
	// built without a gateway gets the no-op, so the five call sites do not each carry a
	// nil check for a dependency that is optional by design.
	notifier Notifier
}

func NewService(store *Store, events *eventstore.Store, clk interface{ Now() time.Time }) *Service {
	return &Service{store: store, events: events, clock: clk, notifier: nopNotifier{}}
}

// uniqueViolation is the Postgres code for a partial unique index doing its job. The two
// that matter here are "one open visit per patient" and "one open encounter per station",
// and both are races two tablets can lose in the same millisecond — which is exactly why
// they are indexes and not checks in this file.
const uniqueViolation = "23505"

func isUnique(err error, constraint string) bool {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != uniqueViolation {
		return false
	}
	return constraint == "" || pg.ConstraintName == constraint
}

// Open starts a visit.
func (s *Service) Open(ctx context.Context, in Opening) (Visit, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Visit{}, err
	}
	if err := in.validate(); err != nil {
		return Visit{}, err
	}

	// The retry check comes **first**, before "does this patient already have a visit".
	// Order matters and getting it wrong is subtle: a tablet that opened a visit, lost the
	// reply and pressed again would otherwise be told the patient already has one — which is
	// true, is the visit it just opened, and reads at the desk as somebody else's mistake.
	// The ledger's uniqueness on event_id is what makes a retry a retry.
	if existing, err := s.events.ByID(ctx, in.EventID); err == nil {
		if existing.EventType != "VISIT_OPENED" {
			return Visit{}, fmt.Errorf("%w: that event id was used for a %s",
				ErrIllegalTransition, existing.EventType)
		}
		return s.store.ByID(ctx, existing.AggregateID, actor.FacilityID())
	}

	// A *different* attempt for a patient who is already in the building. The cheap check
	// here is for a useful message; the partial unique index below is what actually holds.
	if existing, ok, err := s.store.OpenFor(ctx, in.PatientID, actor.FacilityID()); err != nil {
		return Visit{}, err
	} else if ok {
		return existing, fmt.Errorf("%w: %s", ErrAlreadyOpen, existing.VisitCode)
	}

	now := s.clock.Now().UTC()
	day := ClinicDayOf(now)
	visitID := uuid.New()

	var opened Visit
	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries) error {
		code, err := q.NextVisitCode(ctx, dbgen.NextVisitCodeParams{
			PFacility: actor.FacilityID(), PDay: day,
		})
		if err != nil {
			return err
		}
		row, err := q.OpenVisit(ctx, dbgen.OpenVisitParams{
			FacilityID: actor.FacilityID(), PatientID: in.PatientID,
			VisitCode: code, VisitType: string(in.VisitType),
			ChiefComplaint: strings.TrimSpace(in.ChiefComplaint),
			ClinicDay:      day, OpenedAt: now, OpenedBy: actor.UserID(),
		})
		if err != nil {
			if isUnique(err, "visit_one_open_per_patient") {
				return ErrAlreadyOpen
			}
			return err
		}
		visitID = row.ID
		opened = visitOf(row)

		payload, err := json.Marshal(eventstore.VisitOpened{
			FacilityID: actor.FacilityID().String(), PatientID: in.PatientID.String(),
			VisitCode: code, VisitType: string(in.VisitType),
			ChiefComplaint: opened.ChiefComplaint,
			ClinicDay:      day.Format(time.DateOnly),
		})
		if err != nil {
			return err
		}
		return s.append(ctx, tx, in.EventID, visitID, in.PatientID, "VISIT_OPENED", payload, in.Source, now)
	})
	if err != nil {
		return Visit{}, err
	}
	return opened, nil
}

// Close finishes a visit with §11.1's summary.
func (s *Service) Close(ctx context.Context, visitID uuid.UUID, in Closing) (Visit, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Visit{}, err
	}
	if err := in.validate(); err != nil {
		return Visit{}, err
	}
	current, err := s.store.ByID(ctx, visitID, actor.FacilityID())
	if err != nil {
		return Visit{}, err
	}
	if current.Status != Open {
		return Visit{}, fmt.Errorf("%w: it is %s", ErrNotOpen, current.Status)
	}

	complaint := strings.TrimSpace(in.ChiefComplaint)
	if complaint == "" {
		complaint = current.ChiefComplaint
	}
	if complaint == "" {
		// §11.1 requires it, and the database invariant will refuse the row anyway. Refusing
		// here says which field, which is what an operator can act on.
		return Visit{}, fmt.Errorf("%w: the chief complaint was never recorded", ErrSummaryIncomplete)
	}

	now := s.clock.Now().UTC()
	review := ClinicDayOf(now).AddDate(0, 0, in.NextReviewDays)

	var closed Visit
	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries) error {
		days := int32(in.NextReviewDays) //nolint:gosec // bounded by validate
		row, err := q.CloseVisit(ctx, dbgen.CloseVisitParams{
			ID: visitID, FacilityID: actor.FacilityID(),
			ClosedAt: &now, ClosedBy: uuid.NullUUID{UUID: actor.UserID(), Valid: true},
			ChiefComplaint: complaint, Diagnoses: strings.TrimSpace(in.Diagnoses),
			Plan: strings.TrimSpace(in.Plan), NextReviewDays: &days,
			NextReviewOn: pgtype.Date{Time: review, Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// Somebody closed it between the read and the write.
			return fmt.Errorf("%w: it was closed by somebody else", ErrNotOpen)
		}
		if err != nil {
			return err
		}
		closed = visitOf(row)

		stations, err := q.EncountersForVisit(ctx, dbgen.EncountersForVisitParams{
			VisitID: visitID, FacilityID: actor.FacilityID(),
		})
		if err != nil {
			return err
		}
		journey := make([]string, 0, len(stations))
		for _, e := range stations {
			journey = append(journey, e.StationCode)
		}

		payload, err := json.Marshal(eventstore.VisitClosed{
			FacilityID: actor.FacilityID().String(), PatientID: current.PatientID.String(),
			VisitCode: current.VisitCode, ChiefComplaint: complaint,
			Diagnoses: closed.Diagnoses, Plan: closed.Plan,
			NextReviewDays: in.NextReviewDays,
			NextReviewOn:   review.Format(time.DateOnly),
			Stations:       journey,
		})
		if err != nil {
			return err
		}
		return s.append(ctx, tx, in.EventID, visitID, current.PatientID, "VISIT_CLOSED", payload, in.Source, now)
	})
	if err != nil {
		return Visit{}, err
	}
	return closed, nil
}

// Abandon records a visit that ended without the patient being seen.
func (s *Service) Abandon(ctx context.Context, visitID uuid.UUID, eventID uuid.UUID,
	reason, note string, source eventstore.Source) (Visit, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Visit{}, err
	}
	known := false
	for _, candidate := range eventstore.VisitAbandonReasons {
		if candidate == reason {
			known = true
		}
	}
	if !known {
		return Visit{}, fmt.Errorf("%w: %q is not a reason a visit ends unseen", ErrReasonRequired, reason)
	}
	current, err := s.store.ByID(ctx, visitID, actor.FacilityID())
	if err != nil {
		return Visit{}, err
	}
	if current.Status != Open {
		return Visit{}, fmt.Errorf("%w: it is %s", ErrNotOpen, current.Status)
	}

	now := s.clock.Now().UTC()
	var out Visit
	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries) error {
		row, err := q.AbandonVisit(ctx, dbgen.AbandonVisitParams{
			ID: visitID, FacilityID: actor.FacilityID(), StatusReason: reason,
			ClosedAt: &now, ClosedBy: uuid.NullUUID{UUID: actor.UserID(), Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: it changed underneath", ErrNotOpen)
		}
		if err != nil {
			return err
		}
		out = visitOf(row)
		payload, err := json.Marshal(eventstore.VisitAbandoned{
			FacilityID: actor.FacilityID().String(), PatientID: current.PatientID.String(),
			VisitCode: current.VisitCode, Reason: reason, Note: strings.TrimSpace(note),
		})
		if err != nil {
			return err
		}
		return s.append(ctx, tx, eventID, visitID, current.PatientID, "VISIT_ABANDONED", payload, source, now)
	})
	if err != nil {
		return Visit{}, err
	}
	return out, nil
}

// Reopen opens a closed or abandoned visit again.
//
// Recorded, never silent. §4.3's correction principle applies to a visit as much as to a
// value: a closed record that changes without saying so is what it forbids. The *policy* for
// when this is allowed is an operational confirmation Dr. Nahid owes; until then the reason
// is mandatory and the count is on the record, so a visit reopened three times is visible.
func (s *Service) Reopen(ctx context.Context, visitID, eventID uuid.UUID,
	reason string, source eventstore.Source) (Visit, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Visit{}, err
	}
	if len(strings.TrimSpace(reason)) < 10 {
		return Visit{}, ErrReasonRequired
	}
	current, err := s.store.ByID(ctx, visitID, actor.FacilityID())
	if err != nil {
		return Visit{}, err
	}
	if !CanTransition(current.Status, Open) {
		return Visit{}, fmt.Errorf("%w: %s → open", ErrIllegalTransition, current.Status)
	}
	// A patient with a live visit cannot have a second one, so reopening an old visit for
	// somebody already in the building is refused with the reason rather than by an index.
	if _, ok, err := s.store.OpenFor(ctx, current.PatientID, actor.FacilityID()); err != nil {
		return Visit{}, err
	} else if ok {
		return Visit{}, ErrAlreadyOpen
	}

	now := s.clock.Now().UTC()
	var out Visit
	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries) error {
		row, err := q.ReopenVisit(ctx, dbgen.ReopenVisitParams{
			ID: visitID, FacilityID: actor.FacilityID(), StatusReason: strings.TrimSpace(reason),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: it changed underneath", ErrIllegalTransition)
		}
		if err != nil {
			if isUnique(err, "visit_one_open_per_patient") {
				return ErrAlreadyOpen
			}
			return err
		}
		out = visitOf(row)
		payload, err := json.Marshal(eventstore.VisitReopened{
			FacilityID: actor.FacilityID().String(), PatientID: current.PatientID.String(),
			VisitCode: current.VisitCode, Reason: strings.TrimSpace(reason),
			Attempt: out.ReopenedCount,
		})
		if err != nil {
			return err
		}
		return s.append(ctx, tx, eventID, visitID, current.PatientID, "VISIT_REOPENED", payload, source, now)
	})
	if err != nil {
		return Visit{}, err
	}
	return out, nil
}

// Arrive starts an encounter: the patient is at a station.
func (s *Service) Arrive(ctx context.Context, visitID uuid.UUID, in Arrival) (Encounter, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Encounter{}, err
	}
	current, err := s.store.ByID(ctx, visitID, actor.FacilityID())
	if err != nil {
		return Encounter{}, err
	}
	if current.Status != Open {
		return Encounter{}, fmt.Errorf("%w: it is %s", ErrNotOpen, current.Status)
	}
	stations, err := s.store.Stations(ctx, actor.FacilityID())
	if err != nil {
		return Encounter{}, err
	}
	if !contains(stations, in.StationCode) {
		return Encounter{}, fmt.Errorf("%w: %s", ErrUnknownStation, in.StationCode)
	}

	now := s.clock.Now().UTC()
	encounterID := uuid.New()
	var out Encounter
	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries) error {
		device := actor.DeviceID()
		row, err := q.StartEncounter(ctx, dbgen.StartEncounterParams{
			ID: encounterID, FacilityID: actor.FacilityID(), VisitID: visitID,
			PatientID: current.PatientID, StationCode: in.StationCode,
			StartedAt: now, StartedBy: actor.UserID(), StartedRole: actor.Role(),
			DeviceID: uuid.NullUUID{UUID: device, Valid: device != uuid.Nil},
		})
		if err != nil {
			// The concurrency rule, held by a partial unique index rather than by care: two
			// tablets pressing "start" in the same second is a race no handler wins.
			if isUnique(err, "encounter_one_open_per_station") {
				return ErrAlreadyAtStation
			}
			return err
		}
		out = encounterOf(row)
		payload, err := json.Marshal(eventstore.EncounterStarted{
			FacilityID: actor.FacilityID().String(), PatientID: current.PatientID.String(),
			VisitID: visitID.String(), EncounterID: encounterID.String(),
			StationCode: in.StationCode,
		})
		if err != nil {
			return err
		}
		return s.append(ctx, tx, in.EventID, visitID, current.PatientID, "ENCOUNTER_STARTED", payload, in.Source, now)
	})
	if err != nil {
		return Encounter{}, err
	}
	return out, nil
}

// Depart finishes an encounter.
func (s *Service) Depart(ctx context.Context, encounterID uuid.UUID, in Departure) (Encounter, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Encounter{}, err
	}
	if err := in.validate(); err != nil {
		return Encounter{}, err
	}
	row, err := s.store.q.EncounterByID(ctx, dbgen.EncounterByIDParams{
		ID: encounterID, FacilityID: actor.FacilityID(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Encounter{}, ErrNotFound
	}
	if err != nil {
		return Encounter{}, err
	}
	if row.Status != string(InProgress) {
		return Encounter{}, fmt.Errorf("%w: it is %s", ErrEncounterFinished, row.Status)
	}

	now := s.clock.Now().UTC()
	status := Finished
	if in.Outcome == "bounced" {
		status = Bounced
	}
	seconds := int(now.Sub(row.StartedAt).Seconds())

	var out Encounter
	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries) error {
		updated, err := q.FinishEncounter(ctx, dbgen.FinishEncounterParams{
			ID: encounterID, FacilityID: actor.FacilityID(), Status: string(status),
			EndedAt: &now, EndedBy: uuid.NullUUID{UUID: actor.UserID(), Valid: true},
			Outcome: in.Outcome, Notes: strings.TrimSpace(in.Note),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrEncounterFinished
		}
		if err != nil {
			return err
		}
		out = encounterOf(updated)
		payload, err := json.Marshal(eventstore.EncounterFinished{
			FacilityID: actor.FacilityID().String(), PatientID: row.PatientID.String(),
			VisitID: row.VisitID.String(), EncounterID: encounterID.String(),
			StationCode: row.StationCode, Outcome: in.Outcome,
			Note: strings.TrimSpace(in.Note), SecondsAtStation: seconds,
		})
		if err != nil {
			return err
		}
		return s.append(ctx, tx, in.EventID, row.VisitID, row.PatientID, "ENCOUNTER_FINISHED", payload, in.Source, now)
	})
	if err != nil {
		return Encounter{}, err
	}
	return out, nil
}

// append writes one event on the VISIT aggregate.
func (s *Service) append(ctx context.Context, tx pgx.Tx, eventID, visitID, patientID uuid.UUID,
	eventType string, payload []byte, source eventstore.Source, now time.Time) error {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return err
	}
	visit := visitID
	patient := patientID
	_, err = s.events.AppendInTx(ctx, tx, eventstore.Envelope{
		EventID: eventID, AggregateType: "VISIT", AggregateID: visitID,
		PatientID: &patient, VisitID: &visit,
		EventType: eventType, EventVersion: 1,
		OccurredAt: now, Actor: actor, Source: source, Payload: payload,
	})
	return err
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
