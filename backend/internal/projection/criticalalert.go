package projection

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// CriticalAlert is the alert board: what was raised, whether the clinic was told, who
// answered and how far the chain got (CP50, §3 step 5, §4.4).
//
// **Synchronous**, and of everything in this system it is the projection least able to be
// anything else. §4.4 says a critical finding bypasses the queue; criterion 2 says the
// consultant sees it within five seconds. A read model that catches up later is a consultant
// refreshing a screen that does not yet know a patient is in trouble.
//
// Four event types, one row. The alert creates it; the other three amend it, and each of them
// amends a different question:
//
//   - **delivery** — was anybody actually told? Written by its own event because the answer
//     is not knowable inside the transaction that raised the alert.
//   - **acknowledgement** — did a clinician take it? This is what stops the escalation.
//   - **escalation** — how far down the chain has it travelled with nobody answering?
//
// None of them can un-happen. An acknowledgement applies only to an OPEN alert and an
// escalation only moves forward, so a replay in any order lands on the same row.
type CriticalAlert struct{}

var _ Projection = CriticalAlert{}

func (CriticalAlert) Name() string { return "critical_alert" }
func (CriticalAlert) Version() int { return 1 }
func (CriticalAlert) Mode() Mode   { return Synchronous }

func (CriticalAlert) Handles(eventType string) bool {
	switch eventType {
	case "CRITICAL_VALUE_ALERTED",
		"CRITICAL_VALUE_DELIVERY_ATTEMPTED",
		"CRITICAL_VALUE_ACKNOWLEDGED",
		"CRITICAL_VALUE_ESCALATED":
		return true
	}
	return false
}

func (CriticalAlert) Apply(ctx context.Context, tx pgx.Tx, e eventstore.Event) error {
	switch e.EventType {
	case "CRITICAL_VALUE_ALERTED":
		var raised eventstore.CriticalValueAlerted
		if err := json.Unmarshal(e.Payload, &raised); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		row := map[string]any{
			"alert_id":       raised.AlertID,
			"facility_id":    e.Actor.FacilityID().String(),
			"patient_id":     raised.PatientID,
			"visit_id":       raised.VisitID,
			"observation_id": raised.ObservationID,
			"code":           raised.Code,
			"value_num":      raised.ValueNum,
			"unit":           raised.Unit,
			"rule_id":        raised.RuleID,
			"breached":       raised.Breached,
			"threshold":      raised.Threshold,
			"action_en":      raised.ActionEN,
			"action_bn":      raised.ActionBN,
			"raised_at":      raised.RaisedAt,
			// Attribution from the envelope, never the payload — the same rule as every
			// other projection. Who raised an alert is who the ledger says wrote the value.
			"raised_by":    e.Actor.UserID().String(),
			"raised_role":  e.Actor.Role(),
			"station_code": e.Actor.Station(),
			"event_id":     e.EventID.String(),
			"global_seq":   e.GlobalSeq,
		}
		return call(ctx, tx, "read.apply_critical_value_alerted", row)

	case "CRITICAL_VALUE_DELIVERY_ATTEMPTED":
		var attempt eventstore.CriticalValueDeliveryAttempted
		if err := json.Unmarshal(e.Payload, &attempt); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_critical_value_delivery_attempted", map[string]any{
			"alert_id":     attempt.AlertID,
			"recipients":   attempt.Recipients,
			"error":        attempt.Error,
			"attempted_at": attempt.AttemptedAt,
		})

	case "CRITICAL_VALUE_ACKNOWLEDGED":
		var ack eventstore.CriticalValueAcknowledged
		if err := json.Unmarshal(e.Payload, &ack); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_critical_value_acknowledged", map[string]any{
			"alert_id": ack.AlertID,
			// From the envelope: a client that could name the acknowledging user could
			// close an alert in somebody else's name, which is the one attribution in this
			// module that a review would actually turn on.
			"acknowledged_by": e.Actor.UserID().String(),
			"acknowledged_at": ack.AcknowledgedAt,
			"note":            ack.Note,
		})

	case "CRITICAL_VALUE_ESCALATED":
		var step eventstore.CriticalValueEscalated
		if err := json.Unmarshal(e.Payload, &step); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_critical_value_escalated", map[string]any{
			"alert_id":     step.AlertID,
			"step":         step.Step,
			"escalated_at": step.EscalatedAt,
		})
	}
	return nil
}

// Reset empties the read model for a rebuild. The ledger keeps every alert ever raised; this
// table is only the current shape of them.
func (CriticalAlert) Reset(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `DELETE FROM read.critical_alert`)
	return err
}

// call hands one row to a projection function. Each of the four is a SECURITY DEFINER
// function in the database (ADR-0017), so a rebuild and a live write go through exactly the
// same code — including the "only if still OPEN" and "only forwards" guards, which is what
// makes replaying these four in any order land on the same row.
func call(ctx context.Context, tx pgx.Tx, fn string, row map[string]any) error {
	encoded, err := json.Marshal(row)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT `+fn+`($1::jsonb)`, encoded); err != nil {
		return fmt.Errorf("%s: %w", fn, err)
	}
	return nil
}
