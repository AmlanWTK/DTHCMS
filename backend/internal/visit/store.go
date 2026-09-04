package visit

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Store is the visit and encounter tables.
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: dbgen.New(pool)}
}

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

// ByID is one visit, scoped to the facility.
func (s *Store) ByID(ctx context.Context, id, facility uuid.UUID) (Visit, error) {
	row, err := s.q.VisitByID(ctx, dbgen.VisitByIDParams{ID: id, FacilityID: facility})
	if errors.Is(err, pgx.ErrNoRows) {
		return Visit{}, ErrNotFound
	}
	if err != nil {
		return Visit{}, err
	}
	return visitOf(row), nil
}

// OpenFor is the patient's open visit, if they have one.
func (s *Store) OpenFor(ctx context.Context, patientID, facility uuid.UUID) (Visit, bool, error) {
	row, err := s.q.OpenVisitForPatient(ctx, dbgen.OpenVisitForPatientParams{
		PatientID: patientID, FacilityID: facility,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Visit{}, false, nil
	}
	if err != nil {
		return Visit{}, false, err
	}
	return visitOf(row), true, nil
}

// ForPatient is a patient's visits, newest first.
func (s *Store) ForPatient(ctx context.Context, patientID, facility uuid.UUID, limit int) ([]Visit, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.q.VisitsForPatient(ctx, dbgen.VisitsForPatientParams{
		PatientID: patientID, FacilityID: facility, Limit: int32(limit), //nolint:gosec // bounded above
	})
	if err != nil {
		return nil, err
	}
	out := make([]Visit, 0, len(rows))
	for _, row := range rows {
		out = append(out, visitOf(row))
	}
	return out, nil
}

// OnDay is every visit of one clinic day, in arrival order. What a queue board reads.
func (s *Store) OnDay(ctx context.Context, facility uuid.UUID, day time.Time) ([]Visit, error) {
	rows, err := s.q.VisitsOnDay(ctx, dbgen.VisitsOnDayParams{FacilityID: facility, ClinicDay: day})
	if err != nil {
		return nil, err
	}
	out := make([]Visit, 0, len(rows))
	for _, row := range rows {
		out = append(out, visitOf(row))
	}
	return out, nil
}

// Encounters is one visit's journey, in order.
func (s *Store) Encounters(ctx context.Context, visitID, facility uuid.UUID) ([]Encounter, error) {
	rows, err := s.q.EncountersForVisit(ctx, dbgen.EncountersForVisitParams{
		VisitID: visitID, FacilityID: facility,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Encounter, 0, len(rows))
	for _, row := range rows {
		out = append(out, encounterOf(row))
	}
	return out, nil
}

// Stations are this facility's station codes, in journey order.
func (s *Store) Stations(ctx context.Context, facility uuid.UUID) ([]string, error) {
	return s.q.StationCodes(ctx, facility)
}

// Summarise is a visit with its journey and its timings.
//
// The two numbers are computed here rather than stored, because both change while the visit
// is open and a stored total is a total that is wrong between updates.
func (s *Store) Summarise(ctx context.Context, id, facility uuid.UUID, now time.Time) (Summary, error) {
	found, err := s.ByID(ctx, id, facility)
	if err != nil {
		return Summary{}, err
	}
	encounters, err := s.Encounters(ctx, id, facility)
	if err != nil {
		return Summary{}, err
	}

	ended := found.OpenedAt
	if found.ClosedAt != nil {
		ended = *found.ClosedAt
	} else if now.After(ended) {
		ended = now
	}
	summary := Summary{
		Visit: found, Encounters: encounters,
		TotalSeconds: int(ended.Sub(found.OpenedAt).Seconds()),
	}

	// Waiting is the whole visit minus the time actually spent at a station. Overlapping
	// encounters — a patient at two stations at once should be impossible, but a repair
	// script could produce one — are counted once, so waiting can never come out negative.
	var atStations int
	for _, e := range encounters {
		if e.Seconds != nil {
			atStations += *e.Seconds
		} else {
			atStations += int(ended.Sub(e.StartedAt).Seconds())
		}
	}
	summary.WaitingSeconds = max(summary.TotalSeconds-atStations, 0)
	return summary, nil
}

// --- mapping ---

func visitOf(row dbgen.CoreVisit) Visit {
	out := Visit{
		ID: row.ID, FacilityID: row.FacilityID, PatientID: row.PatientID,
		VisitCode: row.VisitCode, VisitType: Type(row.VisitType),
		ChiefComplaint: row.ChiefComplaint, Status: Status(row.Status),
		StatusReason: row.StatusReason, ClinicDay: row.ClinicDay,
		OpenedAt: row.OpenedAt.UTC(), OpenedBy: row.OpenedBy,
		Diagnoses: row.Diagnoses, Plan: row.Plan,
		ReopenedCount: int(row.ReopenedCount),
	}
	if row.ClosedAt != nil {
		at := row.ClosedAt.UTC()
		out.ClosedAt = &at
	}
	if row.ClosedBy.Valid {
		by := row.ClosedBy.UUID
		out.ClosedBy = &by
	}
	if row.NextReviewDays != nil {
		days := int(*row.NextReviewDays)
		out.NextReviewDays = &days
	}
	if row.NextReviewOn.Valid {
		on := row.NextReviewOn.Time
		out.NextReviewOn = &on
	}
	return out
}

func encounterOf(row dbgen.CoreEncounter) Encounter {
	out := Encounter{
		ID: row.ID, VisitID: row.VisitID, PatientID: row.PatientID,
		StationCode: row.StationCode, Status: EncounterStatus(row.Status),
		StartedAt: row.StartedAt.UTC(), StartedBy: row.StartedBy,
		StartedRole: row.StartedRole, Outcome: row.Outcome, Notes: row.Notes,
	}
	if row.EndedAt != nil {
		at := row.EndedAt.UTC()
		out.EndedAt = &at
		seconds := int(at.Sub(out.StartedAt).Seconds())
		out.Seconds = &seconds
	}
	return out
}
