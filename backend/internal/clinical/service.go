package clinical

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

// Service writes observations.
//
// One method — Record — writes every kind of value at every station, which is the whole
// point of the checkpoint. Correcting and superseding are the same write with `Replaces`
// set, because from the ledger's point of view they are: a new fact that says an older one
// is no longer the value.
type Service struct {
	store  *Store
	events *eventstore.Store
	clock  interface{ Now() time.Time }
	// notifier carries a critical value out of this package to whatever can reach a screen
	// (CP50). Optional: a service without one still raises, stores and escalates alerts —
	// it simply cannot tell anybody, which is exactly what the delivery record then says.
	notifier Notifier
}

// Notifier is how a raised alert leaves this package.
//
// It returns how many live screens it reached, which is the whole of criterion 4: zero means
// the operator who typed the value is told to go and find somebody, because in a building
// with no Wi-Fi that is the escalation path that still works.
//
// An interface rather than a realtime client because `clinical` may not import `realtime` —
// the architecture forbids it, and the reason is worth restating: a clinical module that knew
// about sockets would grow a second, differently-permissioned way of reaching a screen.
type Notifier interface {
	CriticalValueRaised(ctx context.Context, alert Alert) (recipients int, err error)
}

func NewService(store *Store, events *eventstore.Store, clk interface{ Now() time.Time }) *Service {
	return &Service{store: store, events: events, clock: clk}
}

// WithNotifier attaches the thing that can reach a screen. Separate from the constructor
// because the bridge is assembled in cmd/api, where realtime and clinical are both in scope.
func (s *Service) WithNotifier(n Notifier) *Service {
	s.notifier = n
	return s
}

// Record writes one measured value.
//
// # The order of the checks
//
// Shape first (decidable without the database), then the registry (is this a code, is it
// live, does its shape match), then units, then plausibility. That order is not arbitrary:
// each check's error message is only useful if the ones before it passed. Telling somebody
// their weight is implausible before noticing they sent it under a code that does not exist
// would send them looking in the wrong place.
//
// # What is checked here and what is checked in the database
//
// Everything here is checked again by `core.observation_is_well_formed()`, on the way into
// the read model, and that is not redundancy to be tidied away. This layer exists so the
// operator sees a sentence in their own language instead of a 500; the trigger exists so the
// record is safe from every path that is not this one — a projection rebuild, a migration, a
// hand-written UPDATE at three in the morning.
func (s *Service) Record(ctx context.Context, in Recording) (Observation, []Alert, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Observation{}, nil, err
	}
	var observationID uuid.UUID
	var alerts []Alert
	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries) error {
		id, raised, appendErr := s.appendRecording(ctx, tx, q, actor, in)
		observationID = id
		alerts = alerts[:0]
		if raised != nil {
			alerts = append(alerts, *raised)
		}
		return appendErr
	})
	if err != nil {
		return Observation{}, nil, err
	}
	// After the commit, never before it. A message published from inside a transaction is a
	// message about a write that may still roll back, and there is no un-ringing a phone.
	s.notify(ctx, alerts)
	// Read back rather than assembling the answer in Go. The canonical value, the unit and
	// the category are all the database's — computed by the trigger from the registry — and
	// an answer built here would be a second implementation of the conversion, which is
	// exactly the class of bug this checkpoint exists to prevent.
	observation, err := s.store.ByID(ctx, observationID, actor.FacilityID())
	return observation, alerts, err
}

// notify attempts delivery and records what happened, in that order, for each alert.
//
// Nothing here can fail the write: the value is already stored and the alert is already in
// the ledger. What a failure changes is what the operator is told — and being told to walk is
// a worse outcome than a working socket, not a worse outcome than silence.
func (s *Service) notify(ctx context.Context, alerts []Alert) {
	for i := range alerts {
		recipients, err := 0, error(nil)
		if s.notifier != nil {
			recipients, err = s.notifier.CriticalValueRaised(ctx, alerts[i])
		}
		alerts[i].Recipients = recipients
		alerts[i].Delivered = recipients > 0 && err == nil
		if err != nil {
			alerts[i].DeliveryError = err.Error()
		}
		// Recorded even when it worked: "how often did an alert reach nobody" is a question
		// the clinic should be able to answer from its own record rather than from memory.
		_ = s.RecordDelivery(ctx, alerts[i], recipients, err)
	}
}

// appendRecording is everything Record does inside one transaction, factored out so that a
// batch (CP45) can do it N times against the same transaction. It returns the new
// observation's id; the caller reads the row back after the commit.
//
// The registry reads below go through the pool rather than the transaction on purpose. Codes
// and units are reference data no transaction writes, so reading them on another connection
// gives the same answer. The two reads that must see this transaction's own writes — the row
// being replaced, and the values a derivation computes from — go through q.
func (s *Service) appendRecording(ctx context.Context, tx pgx.Tx, q *dbgen.Queries,
	actor eventstore.Actor, in Recording) (uuid.UUID, *Alert, error) {

	if err := in.validate(); err != nil {
		return uuid.Nil, nil, err
	}
	spec, live, err := s.store.CodeByCode(ctx, in.Code)
	if err != nil {
		return uuid.Nil, nil, err
	}
	if spec.Code == "" {
		return uuid.Nil, nil, fmt.Errorf("%w: %s", ErrUnknownCode, in.Code)
	}
	if !live {
		return uuid.Nil, nil, fmt.Errorf("%w: %s", ErrRetiredCode, in.Code)
	}
	if in.shapeOf() != spec.ValueType {
		return uuid.Nil, nil, fmt.Errorf("%w: %s is %s, not %s",
			ErrWrongShape, spec.Code, spec.ValueType, in.shapeOf())
	}

	canonical, err := s.checkUnits(ctx, spec, in)
	if err != nil {
		return uuid.Nil, nil, err
	}

	// The shape of a structured finding (CP51). The registry says a value is `structured`;
	// what structure is a property of the code, and there is exactly one today. Checked here
	// rather than by a JSON Schema in the database because the check has a clinical meaning —
	// ten sites, all present — and "nine sites" is an examiner who was interrupted, which
	// must not be recorded as a foot with sensation at the tenth.
	if err := checkStructured(spec.Code, in.ValueJSON); err != nil {
		return uuid.Nil, nil, err
	}

	// Plausibility (CP46). After the units, because a value's band is a band in the
	// canonical unit — checking 154 against a kilogram band would refuse a perfectly good
	// weight in pounds. Before the ledger, because the point of the whole checkpoint is that
	// an impossible value never becomes a fact.
	if in.Value != nil {
		facts, err := s.patientFacts(ctx, in.PatientID, actor.FacilityID())
		if err != nil {
			return uuid.Nil, nil, err
		}
		if err := s.checkPlausible(ctx, q, spec, in, canonical, facts,
			in.PatientID, actor.FacilityID()); err != nil {
			return uuid.Nil, nil, err
		}
	}

	// Correcting or superseding: the earlier row must exist, be this facility's, and still
	// be the value. Correcting a row that has already been replaced is a fork in the record.
	if in.Replaces != nil {
		earlier, err := s.store.byIDTx(ctx, q, *in.Replaces, actor.FacilityID())
		if err != nil {
			return uuid.Nil, nil, err
		}
		if earlier.Status != Active {
			return uuid.Nil, nil, ErrAlreadyReplaced
		}
		if earlier.Code != in.Code {
			return uuid.Nil, nil, fmt.Errorf("%w: %s does not replace a %s",
				ErrWrongShape, in.Code, earlier.Code)
		}
	}

	observationID := uuid.New()
	now := s.clock.Now().UTC()
	effective := in.EffectiveAt.UTC()

	payload := eventstore.ObservationRecorded{
		ObservationID:  observationID.String(),
		FacilityID:     actor.FacilityID().String(),
		PatientID:      in.PatientID.String(),
		Code:           in.Code,
		Value:          in.Value,
		Unit:           in.Unit,
		ValueText:      strings.TrimSpace(in.ValueText),
		ValueBool:      in.ValueBool,
		ValueCode:      strings.TrimSpace(in.ValueCode),
		ValueJSON:      in.ValueJSON,
		EffectiveAt:    effective,
		Source:         string(in.Source),
		Note:           strings.TrimSpace(in.Note),
		Formula:        in.Formula,
		FormulaVersion: in.Version,
		Inputs:         in.Inputs,
	}
	if in.Confirmed {
		payload.ImplausibleConfirmed = true
		payload.ImplausibleReason = strings.TrimSpace(in.ConfirmedReason)
	}
	if in.VisitID != nil {
		payload.VisitID = in.VisitID.String()
	}
	if in.EncounterID != nil {
		payload.EncounterID = in.EncounterID.String()
	}
	if in.Replaces != nil {
		payload.Replaces = in.Replaces.String()
		payload.ReplacedStatus = string(in.ReplacedStatus)
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return uuid.Nil, nil, err
	}

	patient := in.PatientID
	envelope := eventstore.Envelope{
		EventID: in.EventID, AggregateType: "PATIENT", AggregateID: in.PatientID,
		PatientID: &patient,
		EventType: "OBSERVATION_RECORDED", EventVersion: 1,
		OccurredAt: now, Actor: actor, Source: in.LedgerSource, Payload: encoded,
	}
	if in.VisitID != nil {
		visit := *in.VisitID
		envelope.VisitID = &visit
	}
	appended, err := s.events.AppendInTx(ctx, tx, envelope)
	if err != nil {
		return uuid.Nil, nil, err
	}
	if appended.Duplicate {
		// A retry of an event that already landed. The ledger returned the original rather
		// than writing again, so the observation the caller is about to read back is the
		// *first* one — not the id minted a few lines above, which was never written.
		//
		// Without this a tablet that lost a reply and pressed save again would get a 404
		// for a value that is sitting in the record, and would try a third time.
		var earlier eventstore.ObservationRecorded
		if err := json.Unmarshal(appended.Payload, &earlier); err != nil {
			return uuid.Nil, nil, err
		}
		existing, parseErr := uuid.Parse(earlier.ObservationID)
		if parseErr != nil {
			return uuid.Nil, nil, parseErr
		}
		// No alert on a retry. The alert's own event id is derived from this one, so the
		// ledger would refuse the duplicate anyway — but returning nil here is what stops the
		// consultant's phone going off a second time for a value that was already sent.
		return existing, nil, nil
	}

	// Critical values (CP50), inside this transaction and after the ledger append, so an
	// alert can never exist without the value that raised it or the value without the alert.
	//
	// Measured values only. A derived one is computed from measurements that were each
	// checked on the way in, and a critical eGFR is a clinical rule for CP71 rather than an
	// alarm at the point of entry — the operator holding the phone did not measure it and
	// cannot act on it.
	var alert *Alert
	if in.Value != nil {
		facts, factsErr := s.patientFacts(ctx, in.PatientID, actor.FacilityID())
		if factsErr != nil {
			return uuid.Nil, nil, factsErr
		}
		alert, err = s.raise(ctx, tx, q, actor, in, spec, canonical, facts, observationID)
		if err != nil {
			return uuid.Nil, nil, err
		}
	}
	return observationID, alert, nil
}

// checkUnits is criterion 1 as the operator experiences it, and returns the canonical value
// so the plausibility rules (CP46) are applied to the same number the trigger will see.
func (s *Service) checkUnits(ctx context.Context, spec Code, in Recording) (float64, error) {
	if spec.Unitless() {
		if strings.TrimSpace(in.Unit) != "" {
			return 0, fmt.Errorf("%w: %s", ErrUnitNotAllowed, spec.Code)
		}
		return 0, nil
	}
	if in.Value == nil || strings.TrimSpace(in.Unit) == "" {
		return 0, fmt.Errorf("%w: %s is measured in %s", ErrUnitRequired, spec.Code, spec.CanonicalUnit)
	}
	unit, known, err := s.store.UnitByCode(ctx, in.Unit)
	if err != nil {
		return 0, err
	}
	if !known {
		return 0, fmt.Errorf("%w: %s", ErrWrongDimension, in.Unit)
	}
	if unit.Dimension != spec.Dimension {
		// A weight in centimetres. Not a conversion problem — a different measurement.
		return 0, fmt.Errorf("%w: %s measures %s, and %s is a %s",
			ErrWrongDimension, in.Unit, unit.Dimension, spec.Code, spec.Dimension)
	}

	// The registry's own band, against the canonical value. Computed by the database rather
	// than in Go, so that the number checked here is bit-for-bit the number the trigger will
	// check. CP46's rules are narrower and per patient; this one is the code's outer edge.
	var canonical float64
	row := s.store.pool.QueryRow(ctx, `SELECT core.to_canonical($1::numeric, $2::text)`,
		numericOf(*in.Value), in.Unit)
	if err := row.Scan(&canonical); err != nil {
		return 0, err
	}
	if spec.MinCanonical != nil && canonical < *spec.MinCanonical {
		return canonical, fmt.Errorf("%w: %s is below %g %s",
			ErrImplausible, spec.Code, *spec.MinCanonical, spec.CanonicalUnit)
	}
	if spec.MaxCanonical != nil && canonical > *spec.MaxCanonical {
		return canonical, fmt.Errorf("%w: %s is above %g %s",
			ErrImplausible, spec.Code, *spec.MaxCanonical, spec.CanonicalUnit)
	}
	return canonical, nil
}
