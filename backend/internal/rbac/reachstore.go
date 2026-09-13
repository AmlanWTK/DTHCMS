package rbac

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The reach query, against the clinic's own records.
//
// # Why it is one statement and not three
//
// The obvious shape is: find the open visit, then ask the queue, then ask the encounters.
// Three round trips on a decision that sits in front of every station-scoped request. This
// is one, with the visit as a CTE the two EXISTS clauses join to, so the cost is one index
// lookup on `visit_one_open_per_patient` (a partial unique index on patient_id WHERE status
// = 'open', already there since CP38) plus at most two on the reach indexes added by
// migration 00066.
//
// # Why it returns three booleans and not one
//
// `visit_open` is not part of the decision — a patient with no open visit is out of reach
// either way — but it is the whole of the *explanation*, and ADR-0036 says the desk must be
// told which of the two things went wrong or it will think the system is broken. Returning
// it costs nothing (the CTE has already been evaluated) and asking for it separately would
// cost a second query on exactly the path that is already refusing.
//
// # No PHI leaves here
//
// Not in a log line, not in a span, not in an error. The patient id is a bind parameter and
// never appears in a message; what comes back is three booleans and the station code the
// caller already gave. That is a house rule and it is also the only way this can be safe to
// log at all — the refusals are the rows a security dashboard most wants to count.

// PostgresReach is the shipped Reacher.
type PostgresReach struct{ pool *pgxpool.Pool }

func NewPostgresReach(pool *pgxpool.Pool) *PostgresReach { return &PostgresReach{pool: pool} }

var _ Reacher = (*PostgresReach)(nil)

// writeReachSQL: does this station have this patient *now*.
//
// The status list is the narrow one on purpose. `called` means an operator has claimed them
// and is fetching them; `in_service` means the encounter is running. `waiting` is not in it
// — a patient sitting in your queue is not yet yours to record against, and the act of
// claiming them is a recorded one (queue_entry.called_by) which is exactly the audit trail
// that makes this defensible. `done` and `skipped` are not in it because amending a patient
// you have finished with is the correction workflow's job.
//
// The encounter clause is an OR rather than a refinement: a station whose encounter is open
// holds the patient whatever the queue row says, and the queue row is the thing most likely
// to be stale if a tablet dropped a call.
const writeReachSQL = `
WITH current_visit AS (
  SELECT id FROM core.visit
   WHERE facility_id = $1 AND patient_id = $2 AND status = 'open'
)
SELECT
  EXISTS (SELECT 1 FROM current_visit),
  EXISTS (
    SELECT 1 FROM core.queue_entry q JOIN current_visit cv ON cv.id = q.visit_id
     WHERE q.station_code = $3 AND q.status IN ('called', 'in_service')
  ) OR EXISTS (
    SELECT 1 FROM core.encounter e JOIN current_visit cv ON cv.id = e.visit_id
     WHERE e.station_code = $3 AND e.status = 'in_progress'
  )`

// readReachSQL: has this station had this patient at any point in this visit.
//
// Every queue status, `done`, `skipped` and `rerouted` included. `rerouted` is in the list
// deliberately: the patient was in this station's queue and somebody sent them elsewhere,
// and the operator who has to explain that decision has to be able to open the record it
// was made about. Every encounter status too, for the same reason one step later.
const readReachSQL = `
WITH current_visit AS (
  SELECT id FROM core.visit
   WHERE facility_id = $1 AND patient_id = $2 AND status = 'open'
)
SELECT
  EXISTS (SELECT 1 FROM current_visit),
  EXISTS (
    SELECT 1 FROM core.queue_entry q JOIN current_visit cv ON cv.id = q.visit_id
     WHERE q.station_code = $3
  ) OR EXISTS (
    SELECT 1 FROM core.encounter e JOIN current_visit cv ON cv.id = e.visit_id
     WHERE e.station_code = $3
  )`

func (p *PostgresReach) WriteReachAt(ctx context.Context, facilityID uuid.UUID, station string, patientID uuid.UUID) (WriteReach, error) {
	visitOpen, held, err := p.ask(ctx, writeReachSQL, facilityID, station, patientID)
	if err != nil {
		return WriteReach{}, err
	}
	return WriteReach{station: station, held: held, visitOpen: visitOpen}, nil
}

func (p *PostgresReach) ReadReachAt(ctx context.Context, facilityID uuid.UUID, station string, patientID uuid.UUID) (ReadReach, error) {
	visitOpen, held, err := p.ask(ctx, readReachSQL, facilityID, station, patientID)
	if err != nil {
		return ReadReach{}, err
	}
	return ReadReach{station: station, held: held, visitOpen: visitOpen}, nil
}

// ask runs one of the two statements. The error it returns names the station and nothing
// else — never the patient, and never the facility's contents.
func (p *PostgresReach) ask(ctx context.Context, sql string, facilityID uuid.UUID, station string, patientID uuid.UUID) (visitOpen, held bool, err error) {
	if p == nil || p.pool == nil {
		return false, false, fmt.Errorf("rbac: the reach store has no database pool")
	}
	row := p.pool.QueryRow(ctx, sql, facilityID, patientID, station)
	if err := row.Scan(&visitOpen, &held); err != nil {
		return false, false, fmt.Errorf("evaluating the station reach for %s: %w", station, err)
	}
	return visitOpen, held, nil
}
