package projection

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// StationActivity counts what each station did on each clinic day — the substrate the
// traffic board (CP40) reads.
//
// **Asynchronous**: a board that is a second behind is a board. It is also the projection
// that makes the framework's harder promises concrete, because a counter is the shape that
// breaks under replay. `events` is guarded by `last_seq`, so re-applying a batch after a
// crash, or replaying the whole ledger, produces the same number rather than twice it.
// Distinct visits are rows in a second table rather than a count kept in a column, for the
// same reason.
type StationActivity struct{}

var _ Projection = StationActivity{}

func (StationActivity) Name() string { return "station_activity" }
func (StationActivity) Version() int { return 1 }
func (StationActivity) Mode() Mode   { return Asynchronous }

// Every event counts: the board is about how busy a station is, not about what kind of
// work it did.
func (StationActivity) Handles(string) bool { return true }

// dhaka is the clinic's day boundary. A visit that starts at 23:50 and a measurement taken
// at 00:10 belong to different clinic days, and the day that matters is the one the clinic
// was working, not UTC's.
var dhaka = time.FixedZone("Asia/Dhaka", 6*60*60)

func (StationActivity) Apply(ctx context.Context, tx pgx.Tx, e eventstore.Event) error {
	station := e.Actor.Station()
	if station == "" {
		// A role that is not a station's — an administrator, a physician at a desk. The
		// board has no column for them.
		return nil
	}
	day := e.RecordedAt.In(dhaka).Format("2006-01-02")

	if _, err := tx.Exec(ctx, `
		INSERT INTO read.station_activity (facility_id, clinic_day, station, events, last_seq, updated_at)
		VALUES ($1, $2::date, $3, 1, $4, now())
		ON CONFLICT (facility_id, clinic_day, station) DO UPDATE
		   SET events = read.station_activity.events + 1,
		       last_seq = excluded.last_seq,
		       updated_at = now()
		 WHERE read.station_activity.last_seq < excluded.last_seq`,
		e.Actor.FacilityID(), day, station, e.GlobalSeq); err != nil {
		return err
	}

	if e.VisitID != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO read.station_activity_visit (facility_id, clinic_day, station, visit_id)
			VALUES ($1, $2::date, $3, $4)
			ON CONFLICT DO NOTHING`,
			e.Actor.FacilityID(), day, station, *e.VisitID); err != nil {
			return err
		}
	}
	return nil
}

func (StationActivity) Reset(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `DELETE FROM read.station_activity_visit`); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM read.station_activity`)
	return err
}
