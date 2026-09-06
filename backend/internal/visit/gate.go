package visit

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// The checkpoints a visit has to pass (CP57, and CP83 after it).
//
// # Why the gate is asked here as well as enforced in the database
//
// The enforcement is a trigger on `core.queue_entry`, because "enforced server-side" has to mean
// the paths nobody remembers — the support script, the second client, the integration written
// after everyone who read the plan has left. But a trigger's message is not a screen: criterion
// 2 asks that the blocked message name *exactly which items are missing*, and a database
// exception cannot be rendered in two languages beside a button that takes the operator back to
// the right room.
//
// So the queue write asks first. The gate answers with the missing items, this module refuses
// with them, and the trigger is what catches everything that did not come through here. Neither
// is redundant: they protect different things.
//
// # Why the interface is declared here
//
// `visit` may not import `counseling` — the same rule that makes `Notifier` an interface, and
// the same reason. A queue that knew about checklists would grow a second opinion about what
// counselling means; what it needs to know is "may this patient go to that station, and if not,
// what do I tell the operator".

// Gate is a checkpoint on the way to a station.
//
// Nil means no gate, which is what the contract test's handler and every test that is not about
// gating carries. A deployment with no gate is a deployment where the database is the only
// enforcement — still correct, and the message is worse.
type Gate interface {
	// Check answers whether this visit may join that station's queue. `Name` says which
	// checkpoint refused, so a message can name it; `Missing` is what has to happen first.
	Check(ctx context.Context, visit uuid.UUID, station string) (GateDecision, error)
}

// GateDecision is what a checkpoint says about one visit at one station.
type GateDecision struct {
	// Applies is false when this gate has nothing to say about that station — most queue
	// entries, most of the time. It is distinct from "allowed" so the events below are written
	// only where a checkpoint actually looked.
	Applies bool

	Allowed bool

	// Overridden says the patient is allowed because somebody recorded a reason, not because
	// the work was done. Both are "allowed" to the queue and they are not the same clinical
	// fact, so the event records which.
	Overridden bool

	// Name is the checkpoint: "COUNSELING" today, "QA" at CP83.
	Name string

	// Missing is what is outstanding, by code, in the order somebody should do them.
	Missing []string

	// MissingText is the same list as a sentence a person can read, in both languages. Built
	// by the gate rather than here, because the words belong to the checklist's version.
	MissingEN string
	MissingBN string
}

// ErrGateBlocked is a patient stopped at a checkpoint.
//
// Its own error rather than a validation failure, because the caller has to be able to tell it
// apart: a blocked queue entry is not a bad request, and the remedy is in another room rather
// than in the body of this one.
var ErrGateBlocked = errors.New("visit: a checkpoint is holding this patient")

// GateRefusal carries what the operator has to be told.
type GateRefusal struct {
	Name      string
	Station   string
	Missing   []string
	MessageEN string
	MessageBN string
}

func (g GateRefusal) Error() string {
	return "visit: " + g.Name + " is holding this patient at " + g.Station
}

func (g GateRefusal) Unwrap() error { return ErrGateBlocked }

// checkGate asks the gate, records what it said, and returns a refusal if it held.
//
// The events are written whether the gate held or not, and only when a gate actually applied.
// "Held 14 times, passed 300" is a working checkpoint; "held 14 times, passed 14" is a checkpoint
// nobody can get through — and neither number exists unless both are recorded.
func (s *Service) checkGate(ctx context.Context, visit, patient uuid.UUID,
	station string, actor eventstore.Actor, source eventstore.Source) error {

	if s.gate == nil {
		return nil
	}
	decision, err := s.gate.Check(ctx, visit, station)
	if err != nil {
		return err
	}
	if !decision.Applies {
		return nil
	}

	now := s.clock.Now().UTC()
	if decision.Allowed {
		payload, err := json.Marshal(eventstore.VisitGateSatisfied{
			FacilityID: actor.FacilityID().String(), PatientID: patient.String(),
			VisitID: visit.String(), Gate: decision.Name, Station: station,
			Overridden: decision.Overridden, SatisfiedAt: now,
		})
		if err != nil {
			return err
		}
		// Outside the queue transaction on purpose: this event describes the checkpoint, and a
		// queue insert that then fails for its own reasons should not erase the fact that the
		// gate was asked and answered.
		return s.appendOwn(ctx, uuid.New(), visit, patient, "VISIT_GATE_SATISFIED", payload, source, now)
	}

	payload, err := json.Marshal(eventstore.VisitGateBlocked{
		FacilityID: actor.FacilityID().String(), PatientID: patient.String(),
		VisitID: visit.String(), Gate: decision.Name, Station: station,
		Missing: decision.Missing, BlockedAt: now,
	})
	if err != nil {
		return err
	}
	if err := s.appendOwn(ctx, uuid.New(), visit, patient, "VISIT_GATE_BLOCKED", payload, source, now); err != nil {
		return err
	}
	return GateRefusal{
		Name: decision.Name, Station: station, Missing: decision.Missing,
		MessageEN: decision.MissingEN, MessageBN: decision.MissingBN,
	}
}

// appendOwn writes one event in its own transaction.
func (s *Service) appendOwn(ctx context.Context, eventID, visit, patient uuid.UUID,
	eventType string, payload []byte, source eventstore.Source, now time.Time) error {

	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return err
	}
	if source == "" {
		source = eventstore.SourceWeb
	}
	_, err = s.events.Append(ctx, eventstore.Envelope{
		EventID:       eventID,
		AggregateType: "VISIT",
		AggregateID:   visit,
		PatientID:     &patient,
		VisitID:       &visit,
		EventType:     eventType,
		EventVersion:  1,
		OccurredAt:    now,
		Actor:         actor,
		Source:        source,
		Payload:       payload,
	})
	return err
}
