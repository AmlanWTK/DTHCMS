package main

import (
	"context"
	"log/slog"

	"github.com/AmlanWTK/DTHCMS/backend/internal/counseling"
	"github.com/AmlanWTK/DTHCMS/backend/internal/realtime"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// counselingProgressBridge carries counselling progress to the screens waiting on it
// (CP56 criterion 4: the traffic board shows progress within two seconds).
//
// `counseling` may not import `realtime` and `realtime` may not import `counseling`
// (architecture.json), so the translation between "five of seven items are covered" and "a
// message on two topics" lives here, in the one place allowed to know both. The same shape as
// `boardBridge`, and for the same reasons.
//
// # Why two topics and two permissions
//
// The board is a wall display in a public waiting area. It receives the queue message, which
// says a session moved and how far along it is — and nothing else. The patient's own topic
// carries the same counts to the physician's panel, which is watching one patient and holds
// `counseling.session.read`.
//
// Neither message carries an item code. "Covered 6 of 7" on a screen in a waiting room is a
// progress bar; "insulin technique — not yet covered" is a clinical detail about the person
// standing in front of it.
//
// # Why nothing is returned
//
// A failed publish is not a failed tick. CP26's design says it plainly: the socket is a nicety,
// the pull is the truth. Returning an error here would let Redis blinking refuse a counsellor's
// tick with the patient still in the chair.
type counselingProgressBridge struct {
	publisher realtime.Publisher
	logger    *slog.Logger
}

var _ counseling.Notifier = (*counselingProgressBridge)(nil)

func (b *counselingProgressBridge) CounselingProgressed(ctx context.Context, p counseling.Progress) {
	summary := map[string]any{
		"session_id": p.SessionID.String(),
		"covered":    p.Covered,
		"mandatory":  p.Mandatory,
		"complete":   p.Complete,
	}

	board := realtime.Message{
		Topic:      realtime.QueueTopic(p.FacilityID),
		Kind:       p.Kind,
		VisitID:    p.VisitID.String(),
		FacilityID: p.FacilityID.String(),
		// The board's own permission, so a wall display can hold exactly this one.
		Requires: visit.PermBoardRead,
		// Two numbers are not a diagnosis. Marking this sensitive would hide the board from
		// most of the people standing in front of it.
		Sensitive: false,
		At:        p.At,
		Summary:   summary,
	}
	if err := b.publisher.Publish(ctx, board); err != nil {
		b.logger.Warn("the traffic board was not told about counselling progress",
			"kind", p.Kind, "error", err.Error())
	}

	panel := realtime.Message{
		Topic:      realtime.PatientTopic(p.PatientID),
		Kind:       p.Kind,
		PatientID:  p.PatientID.String(),
		VisitID:    p.VisitID.String(),
		FacilityID: p.FacilityID.String(),
		Requires:   counseling.PermSessionRead,
		Sensitive:  false,
		At:         p.At,
		Summary:    summary,
	}
	if err := b.publisher.Publish(ctx, panel); err != nil {
		b.logger.Warn("the physician's panel was not told about counselling progress",
			"kind", p.Kind, "error", err.Error())
	}
}
