package clinical

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical/calc"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Deriving clinical values (CP43).
//
// # Why the server computes rather than accepting a number
//
// P-4 wants a BMI on screen the instant an operator types a height and a weight, so the
// client computes one — `@dthcms/clinical-calc`, the same equations. But a client-computed
// number posted to the server would make the client authoritative about a clinical value,
// and no amount of "it uses the same library" fixes that: an old app version, a modified
// build, or a request assembled by hand would all be accepted.
//
// So the derived value the *record* holds is always computed here, from values already in
// the record. The client's copy is for the operator's eyes while they type. The parity
// fixtures are what make the two agree; this endpoint is what makes the server's the one
// that counts.
//
// # Why the inputs are stored
//
// A BMI is the output of an equation applied to two measurements. Six months later somebody
// asks why it says 34.2, and "recompute it from the patient's current height and weight" is
// the wrong answer — the weight may have been corrected since. What the record needs is what
// the formula *actually saw*, so that is what is stored beside the result.

// Derivable is a value the server can compute. The map is the closed list: a formula name a
// client can invent is a formula whose inputs nobody validated.
type Derivable string

const (
	DeriveBMI       Derivable = "BMI"
	DeriveWHR       Derivable = "WHR"
	DeriveBSA       Derivable = "BSA"
	DeriveBMR       Derivable = "BMR"
	DeriveEGFR      Derivable = "EGFR"
	DerivePackYears Derivable = "PACK_YEARS"
	// DeriveIBW is Devine's ideal body weight (CP45). A *dosing* weight by origin, which is
	// why it is derived rather than typed and why CP60's nutrition plan will not compute
	// from it.
	DeriveIBW Derivable = "IBW"
)

// Derivables is the closed list, in the order a station screen would offer them.
var Derivables = []Derivable{
	DeriveBMI, DeriveWHR, DeriveBSA, DeriveBMR, DeriveIBW, DeriveEGFR, DerivePackYears,
}

// ErrInputsMissing is a derivation whose inputs are not in the record yet. Distinct from a
// refusal by the formula: "we have not measured their height" and "that height cannot be
// right" are different sentences, and only the first tells an operator what to go and do.
var ErrInputsMissing = errors.New("clinical: the values this is computed from have not been recorded")

// ErrCannotCompute wraps the calculation library's own refusal, so a handler can tell an
// operator which input the equation would not accept.
var ErrCannotCompute = errors.New("clinical: that value cannot be computed from these inputs")

// Derivation is a request to compute one value for one patient.
type Derivation struct {
	EventID   uuid.UUID
	PatientID uuid.UUID
	VisitID   *uuid.UUID
	What      Derivable
	// AsianScale picks the obesity classification cut-offs when the derivation produces one.
	// True for this clinic; a parameter because the library serves both and a constant here
	// would make the international scale unreachable.
	AsianScale   bool
	LedgerSource eventstore.Source
}

// Patient facts a derivation needs that are not observations.
type patientFacts struct {
	sex      calc.Sex
	ageYears float64
}

// Derive computes one value from what is already in the record and stores it.
func (s *Service) Derive(ctx context.Context, in Derivation) (Observation, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Observation{}, err
	}
	var observationID uuid.UUID
	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries) error {
		var appendErr error
		observationID, appendErr = s.appendDerivation(ctx, tx, q, actor, in)
		return appendErr
	})
	if err != nil {
		return Observation{}, err
	}
	return s.store.ByID(ctx, observationID, actor.FacilityID())
}

// appendDerivation is Derive inside a caller's transaction, so that a batch (CP45) can
// derive from measurements it wrote a few statements ago.
func (s *Service) appendDerivation(ctx context.Context, tx pgx.Tx, q *dbgen.Queries,
	actor eventstore.Actor, in Derivation) (uuid.UUID, error) {

	current, err := s.currentValuesTx(ctx, q, in.PatientID, actor.FacilityID())
	if err != nil {
		return uuid.Nil, err
	}
	facts, err := s.patientFacts(ctx, in.PatientID, actor.FacilityID())
	if err != nil {
		return uuid.Nil, err
	}

	code, result, inputs, err := s.compute(in, current, facts)
	if err != nil {
		return uuid.Nil, err
	}

	recording := Recording{
		EventID: in.EventID, PatientID: in.PatientID, VisitID: in.VisitID,
		Code: code, Value: &result.Value, Unit: result.Unit,
		EffectiveAt: s.clock.Now().UTC(),
		// DEVICE would be wrong and STATION would be a small lie: nobody measured this. The
		// honest source for a value the server computed is the station the inputs came from,
		// which is what the operator will read it as — so STATION, and the formula fields
		// carry the fact that it was derived.
		Source:       Station,
		LedgerSource: in.LedgerSource,
		Formula:      result.Formula,
		Version:      result.Version,
		Inputs:       inputs,
	}
	return s.appendRecording(ctx, tx, q, actor, recording)
}

// compute is the dispatch. One switch, so that adding a derivable value is one place.
func (s *Service) compute(in Derivation, current map[string]float64, facts patientFacts) (
	string, calc.Result, map[string]float64, error) {

	need := func(codes ...string) (map[string]float64, bool) {
		out := map[string]float64{}
		for _, code := range codes {
			value, ok := current[code]
			if !ok {
				return nil, false
			}
			out[code] = value
		}
		return out, true
	}

	switch in.What {
	case DeriveBMI:
		got, ok := need("BODY_WEIGHT", "BODY_HEIGHT")
		if !ok {
			return "", calc.Result{}, nil, fmt.Errorf("%w: height and weight", ErrInputsMissing)
		}
		result, err := calc.BMI(got["BODY_WEIGHT"], got["BODY_HEIGHT"])
		return "BMI", result, inputsOf(got), wrapCalc(err)

	case DeriveWHR:
		got, ok := need("WAIST_CIRC", "HIP_CIRC")
		if !ok {
			return "", calc.Result{}, nil, fmt.Errorf("%w: waist and hip", ErrInputsMissing)
		}
		result, err := calc.WHR(got["WAIST_CIRC"], got["HIP_CIRC"])
		return "WHR", result, inputsOf(got), wrapCalc(err)

	case DeriveBSA:
		got, ok := need("BODY_WEIGHT", "BODY_HEIGHT")
		if !ok {
			return "", calc.Result{}, nil, fmt.Errorf("%w: height and weight", ErrInputsMissing)
		}
		// Du Bois rather than Mosteller: it is the one the clinic's protocols quote, and the
		// two agree to within about 2% anyway. Both are in the library so a clinician who
		// needs the other can have it.
		result, err := calc.BSADuBois(got["BODY_WEIGHT"], got["BODY_HEIGHT"])
		return "BSA", result, inputsOf(got), wrapCalc(err)

	case DeriveBMR:
		got, ok := need("BODY_WEIGHT", "BODY_HEIGHT")
		if !ok {
			return "", calc.Result{}, nil, fmt.Errorf("%w: height and weight", ErrInputsMissing)
		}
		result, err := calc.BMRMifflin(got["BODY_WEIGHT"], got["BODY_HEIGHT"], facts.ageYears, facts.sex)
		inputs := inputsOf(got)
		inputs["age_years"] = facts.ageYears
		return "BMR", result, inputs, wrapCalc(err)

	case DeriveIBW:
		got, ok := need("BODY_HEIGHT")
		if !ok {
			return "", calc.Result{}, nil, fmt.Errorf("%w: height", ErrInputsMissing)
		}
		result, err := calc.IdealBodyWeight(got["BODY_HEIGHT"], facts.sex)
		return "IBW", result, inputsOf(got), wrapCalc(err)

	case DeriveEGFR:
		got, ok := need("CREATININE")
		if !ok {
			return "", calc.Result{}, nil, fmt.Errorf("%w: serum creatinine", ErrInputsMissing)
		}
		// The equation is published in mg/dL; CP42 stores creatinine canonically in µmol/L.
		// The conversion is the database's — `core.from_canonical` — and not a second copy of
		// 88.42 here. Two copies of a constant is one copy that drifts.
		mgdl, err := s.fromCanonical(got["CREATININE"], "mg/dL#cr")
		if err != nil {
			return "", calc.Result{}, nil, err
		}
		result, calcErr := calc.EGFRCKDEPI2021(mgdl, facts.ageYears, facts.sex)
		inputs := map[string]float64{"creatinine_mg_dl": mgdl, "age_years": facts.ageYears}
		return "EGFR", result, inputs, wrapCalc(calcErr)

	case DerivePackYears:
		// The inputs are a smoking history, which CP53 records. Until then this is honest
		// about being unreachable rather than quietly computing zero.
		return "", calc.Result{}, nil, fmt.Errorf("%w: a smoking history", ErrInputsMissing)

	default:
		return "", calc.Result{}, nil, fmt.Errorf("%w: %s", ErrUnknownCode, in.What)
	}
}

// wrapCalc turns the library's refusal into this module's, keeping the original so a handler
// can say which input the equation would not accept.
func wrapCalc(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrCannotCompute, err)
}

// inputsOf renames the observation codes into the names the formula's own paper uses, so an
// `inputs` object is readable by somebody holding the paper.
func inputsOf(got map[string]float64) map[string]float64 {
	names := map[string]string{
		"BODY_WEIGHT": "weight_kg", "BODY_HEIGHT": "height_cm",
		"WAIST_CIRC": "waist_cm", "HIP_CIRC": "hip_cm",
		"CREATININE": "creatinine_umol_l",
	}
	out := map[string]float64{}
	for code, value := range got {
		if name, ok := names[code]; ok {
			out[name] = value
			continue
		}
		out[code] = value
	}
	return out
}

// currentValuesTx is the patient's active numeric observations, keyed by code, in canonical
// units, read through the caller's transaction. One read rather than one per input: a
// derivation needing three values would otherwise be three round trips at the moment an
// operator is waiting — and through the transaction, so a batch derives from what it has
// just written rather than from the previous visit.
func (s *Service) currentValuesTx(ctx context.Context, q *dbgen.Queries,
	patientID, facility uuid.UUID) (map[string]float64, error) {

	rows, err := s.store.forPatientTx(ctx, q, patientID, facility, 500)
	if err != nil {
		return nil, err
	}
	out := map[string]float64{}
	for _, row := range rows {
		if row.Value == nil {
			continue
		}
		// Newest first from the query, so the first occurrence of a code is the current one.
		if _, seen := out[row.Code]; !seen {
			out[row.Code] = *row.Value
		}
	}
	return out, nil
}

// patientFacts reads the sex and age the equations need.
//
// Read here rather than passed in, for the same reason the values are: a client that could
// name the age used in a CKD-EPI calculation could move a patient across a referral
// threshold by editing a number in a request body.
func (s *Service) patientFacts(ctx context.Context, patientID, facility uuid.UUID) (patientFacts, error) {
	var sex string
	var birth time.Time
	err := s.store.pool.QueryRow(ctx,
		`SELECT sex, birth_date FROM core.patient WHERE id = $1 AND facility_id = $2`,
		patientID, facility).Scan(&sex, &birth)
	if err != nil {
		return patientFacts{}, ErrNotFound
	}
	now := s.clock.Now().UTC()
	years := now.Sub(birth).Hours() / 24 / 365.2425
	return patientFacts{sex: calc.Sex(sex), ageYears: years}, nil
}

// fromCanonical converts out of the stored unit, using the database's own conversion so that
// there is exactly one copy of every factor in the system.
func (s *Service) fromCanonical(value float64, unit string) (float64, error) {
	var out float64
	err := s.store.pool.QueryRow(context.Background(),
		`SELECT core.from_canonical($1::numeric, $2::text)`, numericOf(value), unit).Scan(&out)
	return out, err
}
