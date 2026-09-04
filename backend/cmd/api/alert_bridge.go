package main

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/realtime"
)

// alertBridge carries a critical value to the consultant's screen (CP50 criteria 2 and 4).
//
// `clinical` may not import `realtime` and `realtime` may not import `clinical`
// (architecture.json), so the translation between "this value is dangerous" and "a message on
// somebody's topic" lives here, in the one place allowed to know both. The same shape as
// `boardBridge` above it, and for the same reason.
//
// # What is different from every other bridge
//
// The board's bridge returns nothing: a failed publish is not a failed write, and a client
// that missed a queue update reconciles on its next read. That reasoning does not survive
// contact with a saturation of 88%. Nobody is going to reconcile; the patient is in the
// corridor now.
//
// So this one **counts**. It asks the presence registry how many people with `alert.read`
// have a live screen in this facility, publishes, and returns the count. Zero is not an error
// — the write succeeded and the alert is in the ledger — it is the fact that turns into an
// instruction on the operator's phone: go and find somebody.
//
// # Why it publishes to people rather than to the patient
//
// A critical value bypasses the queue (§4.4), which means it has to reach a consultant who is
// not looking at that patient's chart and may not be looking at a patient at all. The patient
// topic is where a screen already open on that record hears about it; the user topics are how
// it reaches the person. Both, every time.
type alertBridge struct {
	publisher realtime.Publisher
	presence  *realtime.Presence
	logger    *slog.Logger
}

var _ clinical.Notifier = (*alertBridge)(nil)

func (b *alertBridge) CriticalValueRaised(ctx context.Context, alert clinical.Alert) (int, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return 0, err
	}
	facility := actor.FacilityID()

	// Counted before publishing, not after. The question is "will anybody see this", and the
	// only moment at which that can be answered for the operator standing at the station is
	// now — a subscriber who connects two seconds later is a different, later fact.
	recipients, err := b.presence.Count(ctx, realtime.CapabilityAlerts, facility)
	if err != nil {
		b.logger.WarnContext(ctx, "could not count who is watching for alerts",
			"error", err.Error())
		// A presence lookup that failed is not evidence that nobody is there. It is
		// evidence that we do not know — and "we do not know" must read as "nobody", because
		// the cost of wrongly telling an operator to walk down the corridor is a wasted
		// minute and the cost of wrongly telling them not to is a patient.
		recipients = 0
	}

	watchers, err := b.presence.Present(ctx, realtime.CapabilityAlerts, facility)
	if err != nil {
		watchers = nil
	}

	// The patient topic: for a screen already open on this record.
	message := b.message(alert, facility, realtime.PatientTopic(alert.PatientID))
	if err := b.publisher.Publish(ctx, message); err != nil {
		b.logger.ErrorContext(ctx, "a critical value was not published",
			"alert_id", alert.ID.String(), "error", err.Error())
		return 0, err
	}

	// The user topics: for the people who have to act, wherever they are looking.
	for _, watcher := range watchers {
		addressed := b.message(alert, facility, realtime.UserTopic(watcher))
		if err := b.publisher.Publish(ctx, addressed); err != nil {
			b.logger.ErrorContext(ctx, "a critical value did not reach a recipient",
				"alert_id", alert.ID.String(), "user_id", watcher.String(), "error", err.Error())
			return 0, err
		}
	}
	return recipients, nil
}

// message is the notification, and it is deliberately not the alert.
//
// The summary carries what a screen needs to draw a red bar and make a noise — the code, which
// end was breached, the station it came from — and no value, no name and no patient detail.
// The receiving client fetches the alert under `alert.read`, where the permission is checked
// and the identification convention is resolved server-side. A message carrying the number
// would be a clinical value travelling on a channel with its own access rules.
func (b *alertBridge) message(alert clinical.Alert, facility uuid.UUID, topic realtime.Topic) realtime.Message {
	return realtime.Message{
		Topic:      topic,
		Kind:       "critical_value.raised",
		PatientID:  alert.PatientID.String(),
		VisitID:    alert.VisitID,
		FacilityID: facility.String(),
		Requires:   clinical.PermAlertRead,
		// A critical value is a clinical interpretation of a measurement: this number means
		// somebody is in danger. A blinded role does not receive it, whatever else they hold.
		Sensitive: true,
		// Deliberately empty. The board's messages carry a station so a station-scoped role
		// receives what happened at their own station; an alert must reach the consultant
		// wherever it was raised, and naming the station here would scope it away from them.
		Station: "",
		At:      alert.RaisedAt,
		Summary: map[string]any{
			"alert_id":     alert.ID.String(),
			"code":         alert.Code,
			"breached":     alert.Breached,
			"station_code": alert.StationCode,
		},
	}
}
