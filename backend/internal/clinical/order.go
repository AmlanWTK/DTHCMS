package clinical

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Ordering an investigation (CP83).
//
// # Why this exists, and why it is here rather than in `qa`
//
// `docs/qa-rules.md` rule 4 is *"no HbA1c recorded **or ordered** in the last six months"*, and
// the sentence that follows it is the argument: *"a consultant who has ordered the test has done
// the right thing, and blocking him for the lab's turnaround would be blocking the wrong
// person."* Nothing in this system recorded that a test had been asked for. No checkpoint in
// `docs/implementation-plan.md` owns lab ordering — the permission `lab.order` has been in CP15's
// catalogue since the beginning, held by the prescribers, with nothing behind it.
//
// So CP83 had to create the concept to satisfy its own acceptance criterion 2, and it is here
// rather than in `qa` because QA asks a question about orders and does not own them. An order is
// a statement about a measurement, which is this module's subject. A lab checkpoint arriving
// later inherits this table rather than migrating away from a second one.
//
// # What it is not
//
// Not a laboratory workflow. There is no specimen, no accession number, no analyser, no result
// entry, no turnaround clock, and no panel. Those are real and they belong to a checkpoint that
// has thought about them. This is the smallest thing that can answer "did somebody ask for this,
// and when" — which is the whole of what rule 4 needs and is a fact worth recording on its own.
//
// # The result is not linked to the order
//
// Deliberately, and it is the decision most likely to be revisited. An HbA1c ordered in March and
// an HbA1c resulted in April are the same code on the same patient, and "recorded or ordered"
// reads them as two independent satisfactions of one rule. Linking them would need a fulfilment
// step somebody has to perform, and an order nobody closes is worse than no link: it would make
// every result look unordered. When a lab checkpoint brings a result-entry screen, the link
// becomes a column here and the QA rule does not change.

// Ordering is one investigation being asked for.
type Ordering struct {
	EventID   uuid.UUID
	PatientID uuid.UUID
	VisitID   *uuid.UUID

	// Code is a `core.observation_code`. The order and the result therefore name the same thing,
	// which is what lets "recorded or ordered" be one question rather than two.
	Code string
	Note string

	Source eventstore.Source
}

var (
	// ErrOrderCodeUnknown is an order for a measurement the registry does not have. Refused
	// rather than stored as free text: an order nothing can match is an order no rule will ever
	// see satisfied, and the consultant would have no way to tell.
	ErrOrderCodeUnknown = errors.New("clinical: that is not a measurement this clinic records")
	// ErrOrderNotOrderable is an order for something nobody can order — a derived value like
	// eGFR or BMI, which is computed from other measurements rather than asked for.
	ErrOrderNotOrderable = errors.New("clinical: that measurement is derived, not ordered")
)

// Order records that somebody asked for an investigation.
//
// Idempotent on the event id like every other write here: a tablet that retries an order on a
// flaky clinic link produces one order, not three.
func (s *Service) Order(ctx context.Context, in Ordering) (Order, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Order{}, err
	}
	code := strings.ToUpper(strings.TrimSpace(in.Code))
	spec, found, err := s.store.CodeByCode(ctx, code)
	if err != nil {
		return Order{}, err
	}
	if !found {
		return Order{}, ErrOrderCodeUnknown
	}
	// A derived value is computed, not requested. Ordering an eGFR would produce a row that can
	// never be satisfied by anything a laboratory does, and the rule reading it would then treat
	// a permanently outstanding order as evidence that somebody had acted.
	if spec.Category == Derived {
		return Order{}, ErrOrderNotOrderable
	}

	orderID := uuid.New()
	now := s.clock.Now().UTC()
	payload := eventstore.InvestigationOrdered{
		OrderID: orderID.String(), PatientID: in.PatientID.String(),
		Code: code, Note: strings.TrimSpace(in.Note), OrderedAt: now,
	}
	if in.VisitID != nil {
		payload.VisitID = in.VisitID.String()
	}

	eventID := in.EventID
	if eventID == uuid.Nil {
		eventID = uuid.New()
	}
	source := in.Source
	if source == "" {
		source = eventstore.SourceWeb
	}

	// Encoded before the transaction, because a marshal failure inside one would roll back a
	// write that had nothing wrong with it. It cannot fail for this struct; the branch is here
	// so that the next field added to the payload does not turn that into a silent assumption.
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Order{}, err
	}

	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbgen.Queries) error {
		_, appendErr := s.events.AppendInTx(ctx, tx, eventstore.Envelope{
			EventID:       eventID,
			AggregateType: "PATIENT",
			AggregateID:   in.PatientID,
			PatientID:     &in.PatientID,
			VisitID:       in.VisitID,
			EventType:     "INVESTIGATION_ORDERED",
			EventVersion:  1,
			OccurredAt:    now,
			Actor:         actor,
			Source:        source,
			Payload:       encoded,
		})
		return appendErr
	})
	if err != nil {
		return Order{}, err
	}

	return Order{
		ID: orderID, PatientID: in.PatientID, VisitID: in.VisitID,
		Code: code, DisplayEN: spec.DisplayEN, DisplayBN: spec.DisplayBN,
		OrderedAt: now, OrderedBy: actor.UserID(), OrderedRole: actor.Role(),
		Note: payload.Note,
	}, nil
}

