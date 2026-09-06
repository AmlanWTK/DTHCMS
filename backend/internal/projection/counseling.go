package projection

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// Counseling is what a counsellor covered, and what is still missing (CP56, §5.3).
//
// **Synchronous**, for two reasons that are both about the next thirty seconds. CP57's gate
// reads these rows to decide whether a patient may go to the physician, and a gate reading a
// lagging projection would refuse a patient whose last item was ticked a moment ago — on the
// floor, in front of them. And the traffic board's two-second criterion is measured from the
// tick, so the row has to be there when the realtime message arrives.
//
// Four event types, two tables, and each event answers a different question:
//
//   - **started** — this patient is being walked through *that version* of the checklist.
//     The version is in the payload, which is what makes CP55's criterion 2 survive a
//     projection rebuild.
//   - **ticked** — one item, one person, one time. Never a list: criterion 1 is a property
//     of the event, and a projection that accepted a batch would be the place it stopped
//     being one.
//   - **unticked** — the tick is taken back, with a reason, and the row stays. A delete
//     would leave nothing to review, which is the opposite of what criterion 3 is for.
//   - **completed** — the counsellor says they are finished. It ticks nothing.
//
// Replay lands on the same rows in any order: the two updates guard on `global_seq`, and the
// insert is idempotent on its primary key.
type Counseling struct{}

var _ Projection = Counseling{}

func (Counseling) Name() string { return "counseling_session" }
func (Counseling) Version() int { return 1 }
func (Counseling) Mode() Mode   { return Synchronous }

func (Counseling) Handles(eventType string) bool {
	switch eventType {
	case "COUNSELING_SESSION_STARTED",
		"COUNSELING_ITEM_TICKED",
		"COUNSELING_ITEM_UNTICKED",
		"COUNSELING_SESSION_COMPLETED",
		"COUNSELING_GATE_OVERRIDDEN":
		return true
	}
	return false
}

func (Counseling) Apply(ctx context.Context, tx pgx.Tx, e eventstore.Event) error {
	switch e.EventType {
	case "COUNSELING_SESSION_STARTED":
		var started eventstore.CounselingSessionStarted
		if err := json.Unmarshal(e.Payload, &started); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_counseling_session_started", map[string]any{
			"session_id": started.SessionID,
			// From the envelope, like every other projection: the facility a record belongs
			// to is the one the writer was signed in to, not one a body can name.
			"facility_id":      e.Actor.FacilityID().String(),
			"patient_id":       started.PatientID,
			"visit_id":         started.VisitID,
			"template_id":      started.TemplateID,
			"template_version": started.TemplateVersion,
			"started_at":       started.StartedAt,
			"started_by":       e.Actor.UserID().String(),
			"started_role":     e.Actor.Role(),
			"event_id":         e.EventID.String(),
			"global_seq":       e.GlobalSeq,
		})

	case "COUNSELING_ITEM_TICKED":
		var ticked eventstore.CounselingItemTicked
		if err := json.Unmarshal(e.Payload, &ticked); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_counseling_item_ticked", map[string]any{
			"session_id":  ticked.SessionID,
			"item_code":   ticked.ItemCode,
			"facility_id": e.Actor.FacilityID().String(),
			"patient_id":  ticked.PatientID,
			"ticked_at":   ticked.TickedAt,
			// Criterion 1's attribution, and it comes from the envelope for the reason
			// every attribution does.
			"ticked_by":   e.Actor.UserID().String(),
			"ticked_role": e.Actor.Role(),
			// CP61. Which phone, and which room's queue it was standing in.
			"device_id":    deviceOf(e),
			"station_code": e.Actor.Station(),
			"note":         ticked.Note,
			"event_id":     e.EventID.String(),
			"global_seq":   e.GlobalSeq,
		})

	case "COUNSELING_ITEM_UNTICKED":
		var undone eventstore.CounselingItemUnticked
		if err := json.Unmarshal(e.Payload, &undone); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_counseling_item_unticked", map[string]any{
			"session_id": undone.SessionID,
			"item_code":  undone.ItemCode,
			"undone_at":  undone.UntickedAt,
			"undone_by":  e.Actor.UserID().String(),
			"reason":     undone.Reason,
			"global_seq": e.GlobalSeq,
		})

	case "COUNSELING_GATE_OVERRIDDEN":
		var override eventstore.CounselingGateOverridden
		if err := json.Unmarshal(e.Payload, &override); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_counseling_gate_overridden", map[string]any{
			"override_id": override.OverrideID,
			"facility_id": e.Actor.FacilityID().String(),
			"patient_id":  override.PatientID,
			"visit_id":    override.VisitID,
			"granted_at":  override.GrantedAt,
			// The person answerable for the override, from the envelope. A client that could
			// name the granting user could put a colleague's name against a decision they
			// never made — and this is the one decision here somebody has to answer for.
			"granted_by":   e.Actor.UserID().String(),
			"granted_role": e.Actor.Role(),
			"reason":       override.Reason,
			"missing":      override.Missing,
			"event_id":     e.EventID.String(),
			"global_seq":   e.GlobalSeq,
		})

	case "COUNSELING_SESSION_COMPLETED":
		var done eventstore.CounselingSessionCompleted
		if err := json.Unmarshal(e.Payload, &done); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_counseling_session_completed", map[string]any{
			"session_id":   done.SessionID,
			"completed_at": done.CompletedAt,
			"completed_by": e.Actor.UserID().String(),
			"global_seq":   e.GlobalSeq,
		})
	}
	return nil
}

// Reset empties both tables for a rebuild. The ledger keeps every tick ever made; these are
// only their current shape.
//
// Ticks go first, because they reference the session. A rebuild runs as `dthcms_projector`
// against a database nobody is counselling into — which matters more here than it looks: for
// the length of a replay, every session is unfinished and every mandatory item is outstanding,
// and CP57's gate reads exactly those rows.
func (Counseling) Reset(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `DELETE FROM read.counseling_gate_override`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM read.counseling_tick`); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM read.counseling_session`)
	return err
}
