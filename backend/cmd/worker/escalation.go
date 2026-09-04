package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/realtime"
)

// Acknowledge-or-escalate (CP50 criterion 3).
//
// # Why this is a sweep rather than a timer per alert
//
// A timer per alert lives in one process's memory. It is lost when that process restarts,
// which is the moment an alert is *most* likely to go unanswered — the deploy that dropped
// the socket is the same deploy that dropped the timer. A sweep reads state that is actually
// durable, so a worker started thirty seconds late escalates everything that fell due while
// it was gone, in its first pass.
//
// It also makes two workers running at once harmless rather than a race: the escalation
// event's id is derived from the alert and the step, so the ledger absorbs the second one.
//
// # What escalating actually does
//
// It records that the chain advanced, and it tells people again — louder. In a clinic where
// every station is within thirty metres of the consultation room, step 2 is not a different
// set of people: it is the same people, on a screen that has stopped being a badge in the
// corner and become something they have to dismiss. The role in the message is what a client
// reads to decide which of those it is.
//
// The last step is the one that is genuinely different. It names no role, and its message
// goes to the operator who entered the value: nobody has answered, go and find somebody. In a
// building whose Wi-Fi has just failed, that is the only escalation path that still works.
const (
	// escalationInterval is how often the sweep runs. Fifteen seconds against escalation
	// windows measured in minutes: fine enough that the delay is a rounding error, coarse
	// enough that an idle clinic costs four queries a minute.
	escalationInterval = 15 * time.Second
	// escalationBatch bounds one pass. A clinic with more unanswered alerts than this has a
	// problem no worker can solve, and the next pass is fifteen seconds away.
	escalationBatch = 200
)

type escalator struct {
	alerts    *clinical.Store
	service   *clinical.Service
	publisher realtime.Publisher
	logger    *slog.Logger
	now       func() time.Time
}

// run sweeps until the process is asked to stop. A failed pass is logged and the loop
// continues: an escalation that could not be written is an operational failure, and stopping
// the worker over one would turn it into a clinical one.
func (e *escalator) run(ctx context.Context) {
	ticker := time.NewTicker(escalationInterval)
	defer ticker.Stop()

	e.sweep(ctx) // once at start, so a worker restarted after an outage catches up now
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.sweep(ctx)
		}
	}
}

func (e *escalator) sweep(ctx context.Context) {
	pass, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	due, err := e.alerts.DueForEscalation(pass, e.now().UTC(), escalationBatch)
	if err != nil {
		e.logger.ErrorContext(ctx, "the escalation sweep could not read its work", "error", err.Error())
		return
	}
	for _, item := range due {
		e.advance(pass, item)
	}
}

func (e *escalator) advance(ctx context.Context, item clinical.Due) {
	actor := eventstore.ActorForService(item.Alert.FacilityID, "ESCALATION")

	if err := e.service.Escalate(ctx, actor, item.Alert, item.NextStep, item.NotifyRole); err != nil {
		e.logger.ErrorContext(ctx, "an alert could not be escalated",
			"alert_id", item.Alert.ID.String(), "step", item.NextStep, "error", err.Error())
		return
	}
	// A warning, not an info line. "Nobody has answered a critical value for two minutes" is
	// the sentence somebody reading logs at the end of the week most needs to find.
	e.logger.WarnContext(ctx, "a critical value has not been acknowledged",
		"alert_id", item.Alert.ID.String(), "code", item.Alert.Code,
		"step", item.NextStep, "notify_role", item.NotifyRole,
		"waited_seconds", int(e.now().UTC().Sub(item.Alert.RaisedAt).Seconds()))

	e.tell(ctx, item)
}

// tell publishes the escalation.
//
// Failing to publish does not undo it: the step is recorded, the next sweep will not repeat
// it, and the board a consultant refreshes still shows the alert as escalated. What is lost
// is the interruption — which is exactly what the last step exists to cover.
func (e *escalator) tell(ctx context.Context, item clinical.Due) {
	alert := item.Alert

	if item.NotifyRole != "" {
		// On the patient's topic, where every screen that may act on it is listening. The
		// RBAC filter still decides per message: a topic is not a permission.
		message := realtime.Message{
			Topic:      realtime.PatientTopic(alert.PatientID),
			Kind:       "critical_value.escalated",
			PatientID:  alert.PatientID.String(),
			VisitID:    alert.VisitID,
			FacilityID: alert.FacilityID.String(),
			Requires:   clinical.PermAlertRead,
			Sensitive:  true,
			At:         e.now().UTC(),
			Summary: map[string]any{
				"alert_id":    alert.ID.String(),
				"code":        alert.Code,
				"breached":    alert.Breached,
				"step":        item.NextStep,
				"notify_role": item.NotifyRole,
			},
		}
		if err := e.publisher.Publish(ctx, message); err != nil {
			e.logger.ErrorContext(ctx, "an escalation was not published",
				"alert_id", alert.ID.String(), "error", err.Error())
		}
		return
	}

	// The last step. To the operator who entered the value, on their own topic, and
	// deliberately neither sensitive nor requiring `alert.read`: they cannot read the alert
	// board and do not need to. What they are being told is that a value **they themselves
	// entered** has not been answered and they should go and find somebody, which discloses
	// nothing to them that they did not type in the first place.
	message := realtime.Message{
		Topic:      realtime.UserTopic(alert.RaisedBy),
		Kind:       "critical_value.unanswered",
		FacilityID: alert.FacilityID.String(),
		Requires:   clinical.PermObservationRead,
		Sensitive:  false,
		At:         e.now().UTC(),
		Summary: map[string]any{
			"alert_id": alert.ID.String(),
			"code":     alert.Code,
			"step":     item.NextStep,
			"note_en":  item.NoteEN,
			"note_bn":  item.NoteBN,
		},
	}
	if err := e.publisher.Publish(ctx, message); err != nil {
		e.logger.ErrorContext(ctx, "the verbal-escalation instruction was not delivered",
			"alert_id", alert.ID.String(), "error", err.Error())
	}
}
