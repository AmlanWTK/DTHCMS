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

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// The station queue (CP39, §5.2, §5.5, §14.2).
//
// Know where every patient is, and what is next.
//
// The whole checkpoint turns on one sentence: *no patient is ever assigned to two operators at
// the same station*. Two operators pressing "call next" in the same second is the ordinary
// case, not the edge case — a station with two chairs does it all morning — and it is a race
// no amount of care in this file wins.
//
// So the claim happens in `core.call_next_at_station`, which selects one waiting row under
// `FOR UPDATE SKIP LOCKED`. The first caller locks the head of the queue and takes it; the
// second does not block behind it and does not get the same row — it skips to the next waiting
// patient, or finds none. That is right at a desk as well as in the database: the second
// operator wants *a* patient, not *that* patient.

// QueueStatus is where one queue entry stands.
type QueueStatus string

const (
	Waiting   QueueStatus = "waiting"
	Called    QueueStatus = "called"
	InService QueueStatus = "in_service"
	Done      QueueStatus = "done"
	Skipped   QueueStatus = "skipped"
	Rerouted  QueueStatus = "rerouted"
)

// MaxPriority is the highest a patient may be raised to. Nine rather than one, because
// "urgent" and "this one now" are different and a clinic will discover it needs both.
const MaxPriority = 9

// QueueEntry is one patient's place in one station's queue.
type QueueEntry struct {
	ID          uuid.UUID   `json:"id"`
	VisitID     uuid.UUID   `json:"visit_id"`
	PatientID   uuid.UUID   `json:"patient_id"`
	StationCode string      `json:"station_code"`
	Position    int         `json:"position"`
	Status      QueueStatus `json:"status"`

	Priority       int    `json:"priority"`
	PriorityReason string `json:"priority_reason,omitempty"`

	EnteredAt time.Time  `json:"entered_at"`
	CalledAt  *time.Time `json:"called_at,omitempty"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`

	Outcome       string `json:"outcome,omitempty"`
	OutcomeReason string `json:"outcome_reason,omitempty"`
	ReroutedTo    string `json:"rerouted_to,omitempty"`

	// WaitedSeconds is how long they have been in *this* queue. Measured from `entered_at`,
	// not from when the visit opened: a patient who waited two minutes for anthropometry
	// after an hour in the building waited two minutes for anthropometry.
	WaitedSeconds int `json:"waited_seconds"`
}

// StationLoad is one line of the traffic board.
type StationLoad struct {
	StationCode string `json:"station_code"`
	Waiting     int    `json:"waiting"`
	Called      int    `json:"called"`
	InService   int    `json:"in_service"`
	// LongestWaitSeconds is the number a supervisor acts on. An average hides the person who
	// has been sitting there since nine.
	LongestWaitSeconds int `json:"longest_wait_seconds"`
	AverageWaitSeconds int `json:"average_wait_seconds"`
}

// PlannedStation is one step of a visit type's journey.
type PlannedStation struct {
	Position    int    `json:"position"`
	StationCode string `json:"station_code"`
	Required    bool   `json:"required"`
}

// The refusals the queue raises.
var (
	// ErrQueueEmpty is a call-next with nobody waiting. Not an error to a caller who is
	// simply free, which is why the handler answers 204 rather than 404.
	ErrQueueEmpty = errors.New("visit: nobody is waiting at that station")
	// ErrAlreadyQueued is a second live entry for one visit at one station.
	ErrAlreadyQueued = errors.New("visit: this patient is already in that queue")
	// ErrNotCalled is starting service on somebody nobody called.
	ErrNotCalled = errors.New("visit: that patient has not been called")
	// ErrQueueEntryClosed is acting on an entry that has already left the queue.
	ErrQueueEntryClosed = errors.New("visit: that queue entry has already been resolved")
	// ErrRerouteIncomplete is a reroute that does not say where or why.
	ErrRerouteIncomplete = errors.New("visit: a reroute says where the patient went and why")
)

// --- reads ---

// Queue is one station's live queue, in the order it will be called.
func (s *Store) Queue(ctx context.Context, facility uuid.UUID, station string, now time.Time) ([]QueueEntry, error) {
	rows, err := s.q.StationQueue(ctx, dbgen.StationQueueParams{
		FacilityID: facility, StationCode: station,
	})
	if err != nil {
		return nil, err
	}
	out := make([]QueueEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, queueEntryOf(row, now))
	}
	return out, nil
}

// QueueForVisit is where one patient stands everywhere.
func (s *Store) QueueForVisit(ctx context.Context, visitID, facility uuid.UUID, now time.Time) ([]QueueEntry, error) {
	rows, err := s.q.QueueForVisit(ctx, dbgen.QueueForVisitParams{
		VisitID: visitID, FacilityID: facility,
	})
	if err != nil {
		return nil, err
	}
	out := make([]QueueEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, queueEntryOf(row, now))
	}
	return out, nil
}

// Board is every station's load for one clinic day.
func (s *Store) Board(ctx context.Context, facility uuid.UUID, day, now time.Time) ([]StationLoad, error) {
	rows, err := s.q.StationBoard(ctx, dbgen.StationBoardParams{
		FacilityID: facility, ClinicDay: day, Column3: now,
	})
	if err != nil {
		return nil, err
	}
	out := make([]StationLoad, 0, len(rows))
	for _, row := range rows {
		out = append(out, StationLoad{
			StationCode: row.StationCode,
			Waiting:     int(row.Waiting), Called: int(row.Called), InService: int(row.InService),
			LongestWaitSeconds: int(row.LongestWaitSeconds),
			AverageWaitSeconds: int(row.AverageWaitSeconds),
		})
	}
	return out, nil
}

// Planned is the station sequence for a visit type.
//
// Read from the database rather than a constant because the sequences are an operational
// decision still owed by the clinic, and a sequence in Go is a sequence that needs a
// deployment to change.
func (s *Store) Planned(ctx context.Context, facility uuid.UUID, visitType Type) ([]PlannedStation, error) {
	rows, err := s.q.StationSequence(ctx, dbgen.StationSequenceParams{
		FacilityID: facility, VisitType: string(visitType),
	})
	if err != nil {
		return nil, err
	}
	out := make([]PlannedStation, 0, len(rows))
	for _, row := range rows {
		out = append(out, PlannedStation{
			Position: int(row.Position), StationCode: row.StationCode, Required: row.Required,
		})
	}
	return out, nil
}

// --- writes ---

// Joining is a patient being put in a station's queue.
type Joining struct {
	EventID        uuid.UUID
	StationCode    string
	Priority       int
	PriorityReason string
	Source         eventstore.Source
}

// Enqueue puts a patient in one station's queue.
func (s *Service) Enqueue(ctx context.Context, visitID uuid.UUID, in Joining) (QueueEntry, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return QueueEntry{}, err
	}
	if in.Priority < 0 || in.Priority > MaxPriority {
		return QueueEntry{}, fmt.Errorf("visit: priority %d is outside 0..%d", in.Priority, MaxPriority)
	}
	if in.Priority > 0 && strings.TrimSpace(in.PriorityReason) == "" {
		// §4.4's critical findings jump the queue, and so does a physician's judgement.
		// Neither may be anonymous: jumping a queue without a reason is the thing a queue
		// exists to prevent.
		return QueueEntry{}, fmt.Errorf("%w: a patient jumping the queue needs a reason", ErrReasonRequired)
	}

	current, err := s.store.ByID(ctx, visitID, actor.FacilityID())
	if err != nil {
		return QueueEntry{}, err
	}
	if current.Status != Open {
		return QueueEntry{}, fmt.Errorf("%w: it is %s", ErrNotOpen, current.Status)
	}
	stations, err := s.store.Stations(ctx, actor.FacilityID())
	if err != nil {
		return QueueEntry{}, err
	}
	if !contains(stations, in.StationCode) {
		return QueueEntry{}, fmt.Errorf("%w: %s", ErrUnknownStation, in.StationCode)
	}

	// The planned position, for the board's ordering. Not enforcement: a patient sent back
	// from QA has a position behind them, and refusing that would be refusing the clinic's
	// actual flow.
	position := 0
	if planned, err := s.store.Planned(ctx, actor.FacilityID(), current.VisitType); err == nil {
		for _, step := range planned {
			if step.StationCode == in.StationCode {
				position = step.Position
			}
		}
	}

	now := s.clock.Now().UTC()
	entryID := uuid.New()
	var out QueueEntry
	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries) error {
		row, err := q.EnterQueue(ctx, dbgen.EnterQueueParams{
			ID: entryID, FacilityID: actor.FacilityID(), VisitID: visitID,
			PatientID: current.PatientID, StationCode: in.StationCode,
			Position:       int32(position),    //nolint:gosec // a sequence position, single digits
			Priority:       int32(in.Priority), //nolint:gosec // bounded above
			PriorityReason: strings.TrimSpace(in.PriorityReason),
			EnteredAt:      now, ClinicDay: current.ClinicDay,
		})
		if err != nil {
			if isUnique(err, "queue_one_live_per_station") {
				return ErrAlreadyQueued
			}
			return err
		}
		out = queueEntryOf(row, now)
		payload, err := json.Marshal(eventstore.QueueEntered{
			FacilityID: actor.FacilityID().String(), PatientID: current.PatientID.String(),
			VisitID: visitID.String(), EntryID: entryID.String(),
			StationCode: in.StationCode, Position: position,
			Priority: in.Priority, PriorityReason: strings.TrimSpace(in.PriorityReason),
		})
		if err != nil {
			return err
		}
		return s.append(ctx, tx, in.EventID, visitID, current.PatientID, "QUEUE_ENTERED", payload, in.Source, now)
	})
	if err != nil {
		return QueueEntry{}, err
	}
	s.announce(ctx, actor.FacilityID(), out, KindQueueEntered)
	return out, nil
}

// CallNext claims the next patient at a station.
//
// Exactly one caller gets any given patient. The claim is `FOR UPDATE SKIP LOCKED` inside
// `core.call_next_at_station`, so a second operator calling in the same millisecond skips the
// locked row and gets the next one — which is what they wanted.
func (s *Service) CallNext(ctx context.Context, station string, eventID uuid.UUID,
	source eventstore.Source) (QueueEntry, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return QueueEntry{}, err
	}
	now := s.clock.Now().UTC()

	var out QueueEntry
	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries) error {
		row, err := q.CallNextAtStation(ctx, dbgen.CallNextAtStationParams{
			PFacility: actor.FacilityID(), PStation: station,
			PUser: actor.UserID(), PAt: now,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrQueueEmpty
		}
		if err != nil {
			return err
		}
		out = queueEntryOf(row, now)
		payload, err := json.Marshal(eventstore.QueueCalled{
			FacilityID: actor.FacilityID().String(), PatientID: row.PatientID.String(),
			VisitID: row.VisitID.String(), EntryID: row.ID.String(),
			StationCode:   row.StationCode,
			WaitedSeconds: out.WaitedSeconds,
		})
		if err != nil {
			return err
		}
		return s.append(ctx, tx, eventID, row.VisitID, row.PatientID, "QUEUE_CALLED", payload, source, now)
	})
	if err != nil {
		return QueueEntry{}, err
	}
	s.announce(ctx, actor.FacilityID(), out, KindQueueCalled)
	return out, nil
}

// BeginService marks a called patient as being seen, and links the encounter.
func (s *Service) BeginService(ctx context.Context, entryID, encounterID uuid.UUID) (QueueEntry, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return QueueEntry{}, err
	}
	now := s.clock.Now().UTC()
	row, err := s.store.q.StartQueueService(ctx, dbgen.StartQueueServiceParams{
		ID: entryID, FacilityID: actor.FacilityID(), StartedAt: &now,
		EncounterID: uuid.NullUUID{UUID: encounterID, Valid: encounterID != uuid.Nil},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return QueueEntry{}, ErrNotCalled
	}
	if err != nil {
		return QueueEntry{}, err
	}
	out := queueEntryOf(row, now)
	s.announce(ctx, actor.FacilityID(), out, KindQueueStarted)
	return out, nil
}

// Leaving is a patient going out of a queue.
type Leaving struct {
	EventID uuid.UUID
	// Outcome is served, skipped, rerouted or left.
	Outcome string
	Reason  string
	// ReroutedTo is required for a reroute: "sent elsewhere" with no elsewhere is a patient
	// nobody can find.
	ReroutedTo string
	Source     eventstore.Source
}

// Leave takes a patient out of a station's queue.
func (s *Service) Leave(ctx context.Context, entryID uuid.UUID, in Leaving) (QueueEntry, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return QueueEntry{}, err
	}
	if !contains(eventstore.QueueOutcomes, in.Outcome) {
		return QueueEntry{}, fmt.Errorf("visit: %q is not how a patient leaves a queue", in.Outcome)
	}
	if in.Outcome == "rerouted" {
		if strings.TrimSpace(in.ReroutedTo) == "" || len(strings.TrimSpace(in.Reason)) < 5 {
			return QueueEntry{}, ErrRerouteIncomplete
		}
		stations, err := s.store.Stations(ctx, actor.FacilityID())
		if err != nil {
			return QueueEntry{}, err
		}
		if !contains(stations, in.ReroutedTo) {
			return QueueEntry{}, fmt.Errorf("%w: %s", ErrUnknownStation, in.ReroutedTo)
		}
	}

	existing, err := s.store.q.QueueEntryByID(ctx, dbgen.QueueEntryByIDParams{
		ID: entryID, FacilityID: actor.FacilityID(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return QueueEntry{}, ErrNotFound
	}
	if err != nil {
		return QueueEntry{}, err
	}

	status := statusForOutcome(in.Outcome)
	now := s.clock.Now().UTC()
	waited := int(now.Sub(existing.EnteredAt).Seconds())

	var out QueueEntry
	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries) error {
		row, err := q.LeaveQueue(ctx, dbgen.LeaveQueueParams{
			ID: entryID, FacilityID: actor.FacilityID(), Status: string(status),
			EndedAt: &now, Outcome: in.Outcome,
			OutcomeReason: strings.TrimSpace(in.Reason), ReroutedTo: in.ReroutedTo,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrQueueEntryClosed
		}
		if err != nil {
			return err
		}
		out = queueEntryOf(row, now)
		payload, err := json.Marshal(eventstore.QueueLeft{
			FacilityID: actor.FacilityID().String(), PatientID: row.PatientID.String(),
			VisitID: row.VisitID.String(), EntryID: row.ID.String(),
			StationCode: row.StationCode, Outcome: in.Outcome,
			Reason: strings.TrimSpace(in.Reason), ReroutedTo: in.ReroutedTo,
			WaitedSeconds: waited,
		})
		if err != nil {
			return err
		}
		return s.append(ctx, tx, in.EventID, row.VisitID, row.PatientID, "QUEUE_LEFT", payload, in.Source, now)
	})
	if err != nil {
		return QueueEntry{}, err
	}
	s.announce(ctx, actor.FacilityID(), out, KindQueueLeft)
	return out, nil
}

func statusForOutcome(outcome string) QueueStatus {
	switch outcome {
	case "skipped":
		return Skipped
	case "rerouted":
		return Rerouted
	case "left":
		// The patient went home. Recorded as skipped with the outcome saying which, because
		// the queue's state machine has no "gone" and the *outcome* is where the difference
		// belongs — a station that skipped somebody and a patient who left are the same
		// thing to the queue and different things to a report.
		return Skipped
	default:
		return Done
	}
}

func queueEntryOf(row dbgen.CoreQueueEntry, now time.Time) QueueEntry {
	out := QueueEntry{
		ID: row.ID, VisitID: row.VisitID, PatientID: row.PatientID,
		StationCode: row.StationCode, Position: int(row.Position),
		Status: QueueStatus(row.Status), Priority: int(row.Priority),
		PriorityReason: row.PriorityReason, EnteredAt: row.EnteredAt.UTC(),
		Outcome: row.Outcome, OutcomeReason: row.OutcomeReason, ReroutedTo: row.ReroutedTo,
	}
	if row.CalledAt != nil {
		at := row.CalledAt.UTC()
		out.CalledAt = &at
	}
	if row.StartedAt != nil {
		at := row.StartedAt.UTC()
		out.StartedAt = &at
	}
	if row.EndedAt != nil {
		at := row.EndedAt.UTC()
		out.EndedAt = &at
	}

	// Accurate to the second, which is criterion 3. Measured to whichever came first: the
	// call, the end, or now — a patient still waiting has a waiting time that grows.
	until := now
	if out.CalledAt != nil {
		until = *out.CalledAt
	} else if out.EndedAt != nil {
		until = *out.EndedAt
	}
	if until.After(out.EnteredAt) {
		out.WaitedSeconds = int(until.Sub(out.EnteredAt).Seconds())
	}
	return out
}
