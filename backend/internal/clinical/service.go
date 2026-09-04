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
}

func NewService(store *Store, events *eventstore.Store, clk interface{ Now() time.Time }) *Service {
	return &Service{store: store, events: events, clock: clk}
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
func (s *Service) Record(ctx context.Context, in Recording) (Observation, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Observation{}, err
	}
	if err := in.validate(); err != nil {
		return Observation{}, err
	}

	spec, live, err := s.store.CodeByCode(ctx, in.Code)
	if err != nil {
		return Observation{}, err
	}
	if spec.Code == "" {
		return Observation{}, fmt.Errorf("%w: %s", ErrUnknownCode, in.Code)
	}
	if !live {
		return Observation{}, fmt.Errorf("%w: %s", ErrRetiredCode, in.Code)
	}
	if in.shapeOf() != spec.ValueType {
		return Observation{}, fmt.Errorf("%w: %s is %s, not %s",
			ErrWrongShape, spec.Code, spec.ValueType, in.shapeOf())
	}

	if err := s.checkUnits(ctx, spec, in); err != nil {
		return Observation{}, err
	}

	// Correcting or superseding: the earlier row must exist, be this facility's, and still
	// be the value. Correcting a row that has already been replaced is a fork in the record.
	if in.Replaces != nil {
		earlier, err := s.store.ByID(ctx, *in.Replaces, actor.FacilityID())
		if err != nil {
			return Observation{}, err
		}
		if earlier.Status != Active {
			return Observation{}, ErrAlreadyReplaced
		}
		if earlier.Code != in.Code {
			return Observation{}, fmt.Errorf("%w: %s does not replace a %s",
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
		return Observation{}, err
	}

	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbgen.Queries) error {
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
		_, err := s.events.AppendInTx(ctx, tx, envelope)
		return err
	})
	if err != nil {
		return Observation{}, err
	}

	// Read back rather than assembling the answer in Go. The canonical value, the unit and
	// the category are all the database's — computed by the trigger from the registry — and
	// an answer built here would be a second implementation of the conversion, which is
	// exactly the class of bug this checkpoint exists to prevent.
	return s.store.ByID(ctx, observationID, actor.FacilityID())
}

// checkUnits is criterion 1 as the operator experiences it.
func (s *Service) checkUnits(ctx context.Context, spec Code, in Recording) error {
	if spec.Unitless() {
		if strings.TrimSpace(in.Unit) != "" {
			return fmt.Errorf("%w: %s", ErrUnitNotAllowed, spec.Code)
		}
		return nil
	}
	if in.Value == nil || strings.TrimSpace(in.Unit) == "" {
		return fmt.Errorf("%w: %s is measured in %s", ErrUnitRequired, spec.Code, spec.CanonicalUnit)
	}
	unit, known, err := s.store.UnitByCode(ctx, in.Unit)
	if err != nil {
		return err
	}
	if !known {
		return fmt.Errorf("%w: %s", ErrWrongDimension, in.Unit)
	}
	if unit.Dimension != spec.Dimension {
		// A weight in centimetres. Not a conversion problem — a different measurement.
		return fmt.Errorf("%w: %s measures %s, and %s is a %s",
			ErrWrongDimension, in.Unit, unit.Dimension, spec.Code, spec.Dimension)
	}

	// Plausibility, against the canonical value. Computed by the database rather than in Go,
	// so that the number checked here is bit-for-bit the number the trigger will check.
	var canonical float64
	row := s.store.pool.QueryRow(ctx, `SELECT core.to_canonical($1::numeric, $2::text)`,
		numericOf(*in.Value), in.Unit)
	if err := row.Scan(&canonical); err != nil {
		return err
	}
	if spec.MinCanonical != nil && canonical < *spec.MinCanonical {
		return fmt.Errorf("%w: %s is below %g %s",
			ErrImplausible, spec.Code, *spec.MinCanonical, spec.CanonicalUnit)
	}
	if spec.MaxCanonical != nil && canonical > *spec.MaxCanonical {
		return fmt.Errorf("%w: %s is above %g %s",
			ErrImplausible, spec.Code, *spec.MaxCanonical, spec.CanonicalUnit)
	}
	return nil
}
