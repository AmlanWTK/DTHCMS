package main

import (
	"context"
	"log/slog"

	"github.com/AmlanWTK/DTHCMS/backend/internal/realtime"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// boardBridge carries queue changes to the realtime gateway (CP40 criterion 1: the board
// updates within two seconds of a station event).
//
// `visit` may not import `realtime` and `realtime` may not import `visit`
// (architecture.json), so the translation between "a patient joined a queue" and "a message
// on the queue topic" lives here, in the one place allowed to know both. The same shape as
// `auditBridge` above it, and for the same reason.
//
// # What travels
//
// The message says a station changed and how deep its queue now is. It does not say who.
// `visit.QueueChange` has no patient id by design — the queue topic is what the wall
// display subscribes to, and a patient id on that channel is a join key handed to a machine
// standing in a public waiting area. A board that wants detail refetches `/v1/board` under
// `board.read`, which resolves the identification convention server-side.
//
// # Why nothing is returned
//
// A failed publish is not a failed write. CP26's design says it plainly: the socket is a
// nicety, the pull is the truth — a client that misses a message reconciles by reading,
// which it does on every reconnect anyway. Returning an error here would invite the queue
// to fail a call-next because Redis blipped, and the patient is standing at the desk either
// way. So the failure is logged and dropped.
type boardBridge struct {
	publisher realtime.Publisher
	logger    *slog.Logger
}

var _ visit.Notifier = (*boardBridge)(nil)

func (b *boardBridge) QueueChanged(ctx context.Context, change visit.QueueChange) {
	message := realtime.Message{
		Topic:      realtime.QueueTopic(change.FacilityID),
		Kind:       change.Kind,
		VisitID:    change.VisitID.String(),
		FacilityID: change.FacilityID.String(),
		// The board's own permission, so the wall display can hold exactly this one.
		Requires: visit.PermBoardRead,
		// A queue position is not a diagnosis. Marking it sensitive would hide the board
		// from every blinded role, which includes most of the people standing in front of
		// it.
		Sensitive: false,
		At:        change.At,
		Summary: map[string]any{
			"station":  change.StationCode,
			"status":   string(change.Status),
			"entry_id": change.EntryID.String(),
			"waiting":  change.Waiting,
		},
	}
	if err := b.publisher.Publish(ctx, message); err != nil {
		b.logger.Warn("the traffic board was not told about a queue change",
			"kind", change.Kind, "station", change.StationCode, "error", err.Error())
	}
}
