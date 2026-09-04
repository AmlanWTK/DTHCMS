package clinical

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Store reads the registry and the observation read model.
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: dbgen.New(pool)}
}

// InTransaction runs fn against one connection, in one transaction.
func (s *Store) InTransaction(ctx context.Context, fn func(context.Context, pgx.Tx, *dbgen.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, tx, s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// --- the registry ---

// Registry is every live code with the units it may be entered in.
//
// Read whole rather than one code at a time because that is how it is used: a station app
// fetches it once on start-up and validates every entry against it for the rest of the
// clinic session, offline. A per-code endpoint would be a round trip per keystroke on a
// connection that may not be there.
func (s *Store) Registry(ctx context.Context) ([]Code, error) {
	rows, err := s.q.ObservationCodes(ctx)
	if err != nil {
		return nil, err
	}
	units, err := s.Units(ctx)
	if err != nil {
		return nil, err
	}
	byDimension := map[string][]Unit{}
	for _, unit := range units {
		byDimension[unit.Dimension] = append(byDimension[unit.Dimension], unit)
	}

	out := make([]Code, 0, len(rows))
	for _, row := range rows {
		code := Code{
			Code: row.Code, Category: Category(row.Category), ValueType: ValueType(row.ValueType),
			LOINC: row.Loinc, DisplayEN: row.DisplayEn, DisplayBN: row.DisplayBn,
			WritePermission: row.WritePermission,
		}
		if row.Dimension != nil {
			code.Dimension = *row.Dimension
			code.CanonicalUnit = row.CanonicalUnit
			code.Units = byDimension[*row.Dimension]
		}
		if v, ok := numericValue(row.MinCanonical); ok {
			code.MinCanonical = &v
		}
		if v, ok := numericValue(row.MaxCanonical); ok {
			code.MaxCanonical = &v
		}
		out = append(out, code)
	}
	return out, nil
}

// CodeByCode is one registry entry, retired ones included: a value recorded under a code
// that was later withdrawn still has to be readable.
func (s *Store) CodeByCode(ctx context.Context, code string) (Code, bool, error) {
	row, err := s.q.ObservationCodeByCode(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return Code{}, false, nil
	}
	if err != nil {
		return Code{}, false, err
	}
	out := Code{
		Code: row.Code, Category: Category(row.Category), ValueType: ValueType(row.ValueType),
		LOINC: row.Loinc, DisplayEN: row.DisplayEn, DisplayBN: row.DisplayBn,
		WritePermission: row.WritePermission,
	}
	if row.Dimension != nil {
		out.Dimension = *row.Dimension
		out.CanonicalUnit = row.CanonicalUnit
	}
	if v, ok := numericValue(row.MinCanonical); ok {
		out.MinCanonical = &v
	}
	if v, ok := numericValue(row.MaxCanonical); ok {
		out.MaxCanonical = &v
	}
	return out, row.RetiredAt == nil, nil
}

// Units is every unit, in dimension order with the canonical one first.
func (s *Store) Units(ctx context.Context) ([]Unit, error) {
	rows, err := s.q.Units(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Unit, 0, len(rows))
	for _, row := range rows {
		unit := Unit{
			Code: row.Code, Dimension: row.Dimension, IsCanonical: row.IsCanonical,
			DisplayEN: row.DisplayEn, DisplayBN: row.DisplayBn, Decimals: int(row.Decimals),
		}
		if v, ok := numericValue(row.Factor); ok {
			unit.Factor = v
		}
		if v, ok := numericValue(row.Offset); ok {
			unit.Offset = v
		}
		out = append(out, unit)
	}
	return out, nil
}

// UnitByCode is one unit, or false.
func (s *Store) UnitByCode(ctx context.Context, code string) (Unit, bool, error) {
	row, err := s.q.UnitByCode(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return Unit{}, false, nil
	}
	if err != nil {
		return Unit{}, false, err
	}
	unit := Unit{
		Code: row.Code, Dimension: row.Dimension, IsCanonical: row.IsCanonical,
		DisplayEN: row.DisplayEn, DisplayBN: row.DisplayBn, Decimals: int(row.Decimals),
	}
	if v, ok := numericValue(row.Factor); ok {
		unit.Factor = v
	}
	if v, ok := numericValue(row.Offset); ok {
		unit.Offset = v
	}
	return unit, true, nil
}

// --- observations ---

// ByID is one observation, or ErrNotFound. The same error for "another facility's" and
// "does not exist": a 403 must not reveal whether a resource exists.
func (s *Store) ByID(ctx context.Context, id, facility uuid.UUID) (Observation, error) {
	row, err := s.q.ObservationByID(ctx, dbgen.ObservationByIDParams{ID: id, FacilityID: facility})
	if errors.Is(err, pgx.ErrNoRows) {
		return Observation{}, ErrNotFound
	}
	if err != nil {
		return Observation{}, err
	}
	return observationOf(row), nil
}

// byIDTx is ByID through an open transaction, so that a write which replaces a row can see
// a row this same transaction wrote a few statements ago. A batch (CP45) corrects a value it
// has just recorded often enough for the pool's snapshot to be the wrong answer.
func (s *Store) byIDTx(ctx context.Context, q *dbgen.Queries, id, facility uuid.UUID) (Observation, error) {
	row, err := q.ObservationByID(ctx, dbgen.ObservationByIDParams{ID: id, FacilityID: facility})
	if errors.Is(err, pgx.ErrNoRows) {
		return Observation{}, ErrNotFound
	}
	if err != nil {
		return Observation{}, err
	}
	return observationOf(row), nil
}

// forPatientTx is ForPatient through an open transaction. A derivation in a batch computes
// from measurements written moments earlier in that same transaction; reading them on the
// pool would compute a BMI from the height the patient had at their last visit.
func (s *Store) forPatientTx(ctx context.Context, q *dbgen.Queries,
	patientID, facility uuid.UUID, limit int) ([]Observation, error) {

	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := q.ObservationsForPatient(ctx, dbgen.ObservationsForPatientParams{
		PatientID: patientID, FacilityID: facility, Column3: "",
		Limit: int32(limit), //nolint:gosec // bounded above
	})
	if err != nil {
		return nil, err
	}
	return observationsOf(rows), nil
}

// ForPatient is the current value of everything, optionally narrowed to one category.
func (s *Store) ForPatient(ctx context.Context, patientID, facility uuid.UUID,
	category string, limit int) ([]Observation, error) {

	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.q.ObservationsForPatient(ctx, dbgen.ObservationsForPatientParams{
		PatientID: patientID, FacilityID: facility, Column3: category,
		Limit: int32(limit), //nolint:gosec // bounded above
	})
	if err != nil {
		return nil, err
	}
	return observationsOf(rows), nil
}

// ForVisit is everything recorded on one visit.
func (s *Store) ForVisit(ctx context.Context, visitID, facility uuid.UUID) ([]Observation, error) {
	rows, err := s.q.ObservationsForVisit(ctx, dbgen.ObservationsForVisitParams{
		VisitID: uuid.NullUUID{UUID: visitID, Valid: true}, FacilityID: facility,
	})
	if err != nil {
		return nil, err
	}
	return observationsOf(rows), nil
}

// History is every value ever recorded for one code on one patient, replaced ones included.
func (s *Store) History(ctx context.Context, patientID, facility uuid.UUID,
	code string, limit int) ([]Observation, error) {

	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.q.ObservationHistoryForCode(ctx, dbgen.ObservationHistoryForCodeParams{
		PatientID: patientID, FacilityID: facility, Code: code,
		Limit: int32(limit), //nolint:gosec // bounded above
	})
	if err != nil {
		return nil, err
	}
	return observationsOf(rows), nil
}

// --- mapping ---

func observationsOf(rows []dbgen.ReadObservation) []Observation {
	out := make([]Observation, 0, len(rows))
	for _, row := range rows {
		out = append(out, observationOf(row))
	}
	return out
}

func observationOf(row dbgen.ReadObservation) Observation {
	out := Observation{
		ID: row.ID, PatientID: row.PatientID, Code: row.Code,
		Category: Category(row.Category), ValueType: ValueType(row.ValueType),
		ValueText: row.ValueText, ValueBool: row.ValueBool, ValueCode: row.ValueCode,
		EffectiveAt: row.EffectiveAt.UTC(), RecordedAt: row.RecordedAt.UTC(),
		Source: Source(row.Source), Status: Status(row.Status),
		RecordedBy: row.RecordedBy, RecordedRole: row.RecordedRole,
		StationCode: row.StationCode, Note: row.Note,
		Formula: row.Formula, Version: row.FormulaVersion,
		ImplausibleConfirmed: row.ImplausibleConfirmed,
		ImplausibleReason:    row.ImplausibleReason,
	}
	if len(row.Inputs) > 0 {
		inputs := map[string]float64{}
		if err := json.Unmarshal(row.Inputs, &inputs); err == nil {
			out.Inputs = inputs
		}
	}
	if row.VisitID.Valid {
		id := row.VisitID.UUID
		out.VisitID = &id
	}
	if row.EncounterID.Valid {
		id := row.EncounterID.UUID
		out.EncounterID = &id
	}
	if row.ReplacedBy.Valid {
		id := row.ReplacedBy.UUID
		out.ReplacedBy = &id
	}
	if v, ok := numericValue(row.ValueNum); ok {
		out.Value = &v
	}
	if row.Unit != nil {
		out.Unit = *row.Unit
	}
	if v, ok := numericValue(row.EnteredNum); ok {
		out.EnteredValue = &v
	}
	if row.EnteredUnit != nil {
		out.EnteredUnit = *row.EnteredUnit
	}
	if len(row.ValueJson) > 0 {
		out.ValueJSON = json.RawMessage(row.ValueJson)
	}
	return out
}

// numericValue turns a pgtype.Numeric into a float64.
//
// Deliberately at the edge and nowhere else. The database keeps `numeric`, which is exact
// decimal arithmetic, and every conversion and every plausibility check happens there. This
// function exists because JSON has one number type and it is a float — so the lossy step is
// serialisation, which is where it belongs, rather than storage or arithmetic.
func numericValue(n pgtype.Numeric) (float64, bool) {
	if !n.Valid || n.NaN || n.Int == nil {
		return 0, false
	}
	value := new(big.Float).SetInt(n.Int)
	if n.Exp != 0 {
		scale := new(big.Float).SetFloat64(1)
		ten := big.NewFloat(10)
		for i := int32(0); i < absExp(n.Exp); i++ {
			scale.Mul(scale, ten)
		}
		if n.Exp > 0 {
			value.Mul(value, scale)
		} else {
			value.Quo(value, scale)
		}
	}
	out, _ := value.Float64()
	return out, true
}

func absExp(e int32) int32 {
	if e < 0 {
		return -e
	}
	return e
}

// numericOf is the other direction, for the few places a Go value has to reach a numeric
// column. Kept beside its inverse so a change to one is a change to both.
func numericOf(v float64) pgtype.Numeric {
	var out pgtype.Numeric
	if err := out.Scan(formatFloat(v)); err != nil {
		return pgtype.Numeric{}
	}
	return out
}

func formatFloat(v float64) string {
	return new(big.Float).SetFloat64(v).Text('f', 10)
}

var _ = time.Time{}
