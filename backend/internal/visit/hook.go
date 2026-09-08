package visit

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Telling another module that a station touch ended (CP71, §7.1).
//
// # Why the interface is declared here
//
// `visit` may not import `synthesis` — architecture.json says so, and the reason is the same one
// that made `Notifier` and `Gate` interfaces: a module that knew what a synthesis was would
// eventually acquire an opinion about when one is warranted, and that opinion belongs in the module
// that owns the summary. So the dependency is inverted. This module says what happened; the
// composition root wires up whoever cares.
//
// # Why it runs *inside* the transaction, unlike Notifier
//
// [Notifier] deliberately fires after the commit, because a notification describing a write that
// then rolled back cannot be unpublished. This is the opposite case and needs the opposite rule.
// What a hook does here is enqueue work, and CP69's whole design rests on `EnqueueTx` taking the
// caller's transaction: a station touch that rolls back must not leave a queued job behind that
// briefs a physician about a patient who is not there. So the hook is handed the transaction, and
// a hook that does not need it can ignore it.
//
// # And why an error from it does not fail the departure
//
// A station touch is a clinical fact. A summary is a convenience. D-15's rule is that the clinic
// never stops because AI stopped, so a hook that fails must not stop an operator recording that a
// patient left the room.
//
// That is harder than it sounds, and the naive version is silently broken: a failed statement
// aborts the whole PostgreSQL transaction, so *swallowing* the error would leave the departure's
// own event append failing a moment later with "current transaction is aborted" — the clinical
// write would be lost anyway, and the log would blame the wrong thing. So the hook runs in a
// **savepoint**: pgx's nested `Begin` on a transaction opens one, and rolling back to it discards
// whatever the hook did while leaving the departure intact.
//
// The cost is one round trip per station touch. That is the price of the rule being true rather
// than intended.

// StationHook is something that wants to know, inside the transaction, that a station touch ended.
type StationHook interface {
	// EncounterFinished is called after the encounter row and its event are written and before
	// the transaction commits. An error rolls back only what the hook did.
	EncounterFinished(ctx context.Context, tx pgx.Tx, touch StationTouch) error
}

// StationTouch is one station finishing with one patient.
//
// It carries ids, codes and a status — never a value, a name or a note. A hook that needed the
// clinical content would read it from the module that owns it, under the ids here; a struct that
// carried it would be a second copy of the record travelling between modules, and the first thing
// somebody would do with it is store it.
type StationTouch struct {
	VisitID     uuid.UUID
	PatientID   uuid.UUID
	EncounterID uuid.UUID
	StationCode string
	Status      EncounterStatus
	// Outcome is the station's own word for how the touch ended: what the queue and §14.2 count.
	Outcome string
	At      time.Time
}

// OnStationFinished attaches a hook.
//
// A copy rather than a mutation, like [Service.WithGate]: a service assembled once at startup
// should not be reachable in two states.
func (s *Service) OnStationFinished(h StationHook) *Service {
	copied := *s
	copied.hook = h
	return &copied
}

// runHook calls the hook in a savepoint, and never lets it fail the caller's write.
func (s *Service) runHook(ctx context.Context, tx pgx.Tx, logger *slog.Logger, touch StationTouch) {
	if s.hook == nil {
		return
	}
	nested, err := tx.Begin(ctx)
	if err != nil {
		logHookFailure(ctx, logger, touch, err)
		return
	}
	if err := s.hook.EncounterFinished(ctx, nested, touch); err != nil {
		_ = nested.Rollback(ctx)
		logHookFailure(ctx, logger, touch, err)
		return
	}
	if err := nested.Commit(ctx); err != nil {
		logHookFailure(ctx, logger, touch, err)
	}
}

// logHookFailure is at warn rather than error, and says what the consequence is.
//
// The consequence is small and specific: the physician's screen will report that no summary has
// been prepared, with a button. That is a degraded state somebody can act on, not an incident — and
// a line that said only "hook failed" would be a line nobody could triage.
func logHookFailure(ctx context.Context, logger *slog.Logger, touch StationTouch, err error) {
	if logger == nil {
		return
	}
	logger.WarnContext(ctx,
		"a station-finished hook failed; the station touch is recorded and any AI summary it would have triggered was not requested",
		"visit_id", touch.VisitID.String(), "station_code", touch.StationCode,
		"error", err.Error())
}
