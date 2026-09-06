package projection

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// Correction is the flagging workflow's read model (CP62, §4.3).
//
// **Synchronous.** A physician flags a value and the operator's phone has to show the request
// while the patient is still in the room — §4.3's scenario is two people at two stations in the
// same few minutes, and a request that appeared a second late would be a request the operator
// walks away from.
//
// Four event types, one table, and the fourth is the checkpoint: a supervisor correcting somebody
// else's value writes `SUPERVISOR_OVERRIDE_APPLIED` rather than `CORRECTION_APPLIED`, because the
// operator's quality record (CP63) must not read a supervisor's fix as though the operator had
// put it right themselves.
//
// Nothing here touches the value. Correcting is writing a new observation that replaces the old
// one, which the observation projection already does; this records who asked, who was asked, why,
// and what happened.
type Correction struct{}

var _ Projection = Correction{}

func (Correction) Name() string { return "correction_request" }
func (Correction) Version() int { return 1 }
func (Correction) Mode() Mode   { return Synchronous }

func (Correction) Handles(eventType string) bool {
	switch eventType {
	case "CORRECTION_REQUESTED",
		"CORRECTION_APPLIED",
		"SUPERVISOR_OVERRIDE_APPLIED",
		"CORRECTION_REJECTED":
		return true
	}
	return false
}

func (Correction) Apply(ctx context.Context, tx pgx.Tx, e eventstore.Event) error {
	switch e.EventType {
	case "CORRECTION_REQUESTED":
		var requested eventstore.CorrectionRequested
		if err := json.Unmarshal(e.Payload, &requested); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_correction_requested", map[string]any{
			"request_id": requested.RequestID,
			// From the envelope, like every other attribution: the facility a record belongs to
			// is the one the writer was signed in to, not one a body can name.
			"facility_id":    e.Actor.FacilityID().String(),
			"patient_id":     requested.PatientID,
			"visit_id":       requested.VisitID,
			"observation_id": requested.ObservationID,
			"code":           requested.Code,
			"requested_at":   requested.RequestedAt,
			"requested_by":   e.Actor.UserID().String(),
			"requested_role": e.Actor.Role(),
			"reason_code":    requested.ReasonCode,
			"note":           requested.Note,
			"assigned_to":    requested.AssignedTo,
			"event_id":       e.EventID.String(),
			"global_seq":     e.GlobalSeq,
		})

	case "CORRECTION_APPLIED":
		var applied eventstore.CorrectionApplied
		if err := json.Unmarshal(e.Payload, &applied); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_correction_resolved", map[string]any{
			"request_id":     applied.RequestID,
			"status":         "APPLIED",
			"resolved_at":    applied.AppliedAt,
			"resolved_by":    e.Actor.UserID().String(),
			"resolved_role":  e.Actor.Role(),
			"note":           applied.Note,
			"replacement_id": applied.ReplacementID,
			// What else moved because this moved (criterion 3), so a screen can say it rather
			// than leaving a physician to compare numbers and guess.
			"recomputed": applied.Recomputed,
			"global_seq": e.GlobalSeq,
		})

	case "SUPERVISOR_OVERRIDE_APPLIED":
		var override eventstore.SupervisorOverrideApplied
		if err := json.Unmarshal(e.Payload, &override); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		// OVERRIDDEN rather than APPLIED. The status is what CP63 counts, and an operator whose
		// supervisor fixed a value for them has not answered a request — which is a different
		// thing to know about them from having answered it wrongly or not at all.
		return call(ctx, tx, "read.apply_correction_resolved", map[string]any{
			"request_id":     override.RequestID,
			"status":         "OVERRIDDEN",
			"resolved_at":    override.AppliedAt,
			"resolved_by":    e.Actor.UserID().String(),
			"resolved_role":  e.Actor.Role(),
			"note":           override.Note,
			"replacement_id": override.ReplacementID,
			"recomputed":     override.Recomputed,
			"global_seq":     e.GlobalSeq,
		})

	case "CORRECTION_REJECTED":
		var rejected eventstore.CorrectionRejected
		if err := json.Unmarshal(e.Payload, &rejected); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_correction_resolved", map[string]any{
			"request_id":    rejected.RequestID,
			"status":        "REJECTED",
			"resolved_at":   rejected.RejectedAt,
			"resolved_by":   e.Actor.UserID().String(),
			"resolved_role": e.Actor.Role(),
			// The reason is the whole of a rejection. "No" with no reason is how a flagging
			// culture dies: the physician who flagged it learns nothing and stops flagging.
			"note":       rejected.Reason,
			"global_seq": e.GlobalSeq,
		})
	}
	return nil
}

// Reset empties the table for a rebuild. The ledger keeps every request ever made.
func (Correction) Reset(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `DELETE FROM read.correction_request`)
	return err
}
