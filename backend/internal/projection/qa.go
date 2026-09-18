package projection

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// Station 10's decisions, and the orders that let rule 4 mean what it says (CP83).
//
// # Synchronous, and this one is not a preference
//
// `core.qa_clearance_stands()` reads `read.qa_review`, and the gate on `read.prescription` calls
// it. A clearance projected asynchronously would leave a window — seconds, on a busy queue —
// during which the officer has cleared the file, the screen says so, and the signature is refused
// by a trigger that cannot see the row yet. The physician standing at the next desk would be
// told the prescription has no QA clearance while looking at the clearance.
//
// So: same transaction as the event, always. This is the sharpest example in the system of why
// `Synchronous` exists.
//
// # Three event types, three functions, no shared shape
//
// A clearance and a bounce are one payload under two names (`eventstore.PrescriptionQADecided`),
// so they share a function. The override is its own event because it is the one act here somebody
// has to answer for, and the order belongs to a different table entirely.
type QA struct{}

var _ Projection = QA{}

func (QA) Name() string { return "qa_review" }
func (QA) Version() int { return 1 }
func (QA) Mode() Mode   { return Synchronous }

func (QA) Handles(eventType string) bool {
	switch eventType {
	case "PRESCRIPTION_QA_CLEARED",
		"PRESCRIPTION_QA_BOUNCE_DECIDED",
		"PRESCRIPTION_QA_OVERRIDDEN",
		"INVESTIGATION_ORDERED":
		return true
	}
	return false
}

func (QA) Apply(ctx context.Context, tx pgx.Tx, e eventstore.Event) error {
	switch e.EventType {
	case "PRESCRIPTION_QA_CLEARED", "PRESCRIPTION_QA_BOUNCE_DECIDED":
		var decided eventstore.PrescriptionQADecided
		if err := json.Unmarshal(e.Payload, &decided); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		findings := decided.Findings
		if len(findings) == 0 {
			findings = json.RawMessage("[]")
		}
		return call(ctx, tx, "read.apply_qa_reviewed", map[string]any{
			"review_id": decided.ReviewID,
			// From the envelope, like every other projection: the facility a record belongs to
			// is the one the writer was signed in to, not one a body can name.
			"facility_id":     e.Actor.FacilityID().String(),
			"prescription_id": decided.PrescriptionID,
			"patient_id":      decided.PatientID,
			"visit_id":        decided.VisitID,
			"outcome":         decided.Outcome,
			"decided_at":      decided.DecidedAt,
			// The person answerable for the decision, from the envelope. A client that could
			// name the deciding officer could put a colleague's name against a clearance they
			// never gave.
			"decided_by":          e.Actor.UserID().String(),
			"decided_role":        e.Actor.Role(),
			"bounce_station_code": decided.BounceStation,
			"reason_en":           decided.ReasonEN,
			"reason_bn":           decided.ReasonBN,
			"findings":            json.RawMessage(findings),
			"acknowledged":        decided.Acknowledged,
			"override_id":         decided.OverrideID,
			"event_id":            e.EventID.String(),
			"global_seq":          e.GlobalSeq,
		})

	case "PRESCRIPTION_QA_OVERRIDDEN":
		var override eventstore.PrescriptionQAOverridden
		if err := json.Unmarshal(e.Payload, &override); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_qa_overridden", map[string]any{
			"override_id":     override.OverrideID,
			"facility_id":     e.Actor.FacilityID().String(),
			"prescription_id": override.PrescriptionID,
			"patient_id":      override.PatientID,
			"visit_id":        override.VisitID,
			"granted_at":      override.GrantedAt,
			"granted_by":      e.Actor.UserID().String(),
			"granted_role":    e.Actor.Role(),
			"reason":          override.Reason,
			"blocking":        override.Blocking,
			"event_id":        e.EventID.String(),
			"global_seq":      e.GlobalSeq,
		})

	case "INVESTIGATION_ORDERED":
		var ordered eventstore.InvestigationOrdered
		if err := json.Unmarshal(e.Payload, &ordered); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_investigation_ordered", map[string]any{
			"order_id":     ordered.OrderID,
			"facility_id":  e.Actor.FacilityID().String(),
			"patient_id":   ordered.PatientID,
			"visit_id":     ordered.VisitID,
			"code":         ordered.Code,
			"ordered_at":   ordered.OrderedAt,
			"ordered_by":   e.Actor.UserID().String(),
			"ordered_role": e.Actor.Role(),
			"note":         ordered.Note,
			"event_id":     e.EventID.String(),
			"global_seq":   e.GlobalSeq,
		})
	}
	return nil
}

// Reset empties the three tables for a rebuild.
//
// **Order matters here**: `read.qa_review.override_id` references `read.qa_override`, so the
// reviews go first. A rebuild that tried the other way round would fail on the foreign key, and
// the failure would arrive during a rebuild, which is the worst time to discover it.
func (QA) Reset(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `DELETE FROM read.qa_review`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM read.qa_override`); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM read.investigation_order`)
	return err
}
