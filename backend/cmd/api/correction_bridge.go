package main

import (
	"context"
	"log/slog"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/realtime"
)

// correctionBridge carries a flagged value to the person who typed it (CP62 criterion 4).
//
// `clinical` may not import `realtime`, so the translation between "somebody says this height is
// wrong" and "a message on one person's topic" lives here, in the only place allowed to know
// both. The same shape as `alertBridge` and `boardBridge`, and for the same reason.
//
// # Why the user's own topic
//
// This is not a clinical emergency shouted at whoever can act on it; it is one colleague being
// asked to look at one number again. It goes to `user:{id}` — the operator's own channel — and to
// nobody else, because a correction request broadcast to a station is a mistake announced to
// whoever happens to be standing there.
//
// # What travels
//
// The request id, the observation id, the code and the patient id: enough to fetch and no more.
// **Not the value.** A message saying "your 150 should be 140" would put a clinical value on a
// channel whose access rules are per-message rather than per-value, and the operator's own screen
// can read both numbers under the permission it already holds.
//
// # Why nothing is returned
//
// A failed publish is not a failed flag. The socket is a nicety and the pull is the truth: the
// operator finds the request on their screen the next time they read, and refusing the flag
// because Redis blinked would leave a physician unable to say a value is wrong.
type correctionBridge struct {
	publisher realtime.Publisher
	logger    *slog.Logger
}

var _ clinical.CorrectionNotifier = (*correctionBridge)(nil)

func (b *correctionBridge) CorrectionRequested(ctx context.Context, request clinical.CorrectionRequest) {
	message := realtime.Message{
		Topic:      realtime.UserTopic(request.AssignedTo),
		Kind:       "correction.requested",
		PatientID:  request.PatientID.String(),
		FacilityID: "",
		// The operator holds this already — it is what let them record the value in the first
		// place. A request to look at your own work again should not need a permission you would
		// have to be granted for the occasion.
		Requires:  clinical.PermObservationRead,
		Sensitive: false,
		At:        request.RequestedAt,
		Summary: map[string]any{
			"request_id":     request.ID.String(),
			"observation_id": request.ObservationID.String(),
			"code":           request.Code,
			"reason_code":    request.ReasonCode,
		},
	}
	if request.VisitID != nil {
		message.VisitID = request.VisitID.String()
	}
	if err := b.publisher.Publish(ctx, message); err != nil {
		b.logger.Warn("the operator was not told about a correction request",
			"code", request.Code, "error", err.Error())
	}
}
