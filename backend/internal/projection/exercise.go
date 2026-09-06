package projection

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// Exercise is station 8's read model (CP60, §3 step 8).
//
// **Synchronous**, and for a reason particular to this station: the assessment is what the
// permitted-exercise filter reads, and the plan is chosen from that filter seconds later. A read
// model a moment behind would compute the options from the *previous* assessment — so a patient
// whose foot ulcer was recorded thirty seconds ago would still be offered walking. Criterion 1
// would then be true of the database and false of the clinic.
//
// The plan's items go in through `read.exercise_plan_item`, whose trigger refuses anything the
// frozen assessment forbids. That is deliberate on a projection: a rebuild that quietly accepted a
// contraindicated item would produce a read model in which criterion 1 is false, and a loud
// failure during a replay is the cheapest possible moment to discover it.
type Exercise struct{}

var _ Projection = Exercise{}

func (Exercise) Name() string { return "exercise" }
func (Exercise) Version() int { return 1 }
func (Exercise) Mode() Mode   { return Synchronous }

func (Exercise) Handles(eventType string) bool {
	switch eventType {
	case "EXERCISE_ASSESSMENT_RECORDED", "EXERCISE_PLAN_ISSUED":
		return true
	}
	return false
}

func (Exercise) Apply(ctx context.Context, tx pgx.Tx, e eventstore.Event) error {
	switch e.EventType {
	case "EXERCISE_ASSESSMENT_RECORDED":
		var recorded eventstore.ExerciseAssessmentRecorded
		if err := json.Unmarshal(e.Payload, &recorded); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		// Non-nil even when the assessment found nothing, so the projection writes `[]` rather
		// than a scalar `null`. The SQL guards against it, but a payload that says "not asked"
		// where it meant "none apply" is a difference nobody would spot in a rebuild.
		conditions := recorded.Contraindications
		if conditions == nil {
			conditions = []string{}
		}
		asked := recorded.Asked
		if asked == nil {
			asked = []string{}
		}
		payload := map[string]any{
			"assessment_id": recorded.AssessmentID,
			// From the envelope, as every attribution and every scope is.
			"facility_id":       e.Actor.FacilityID().String(),
			"patient_id":        recorded.PatientID,
			"visit_id":          recorded.VisitID,
			"joint_pain":        recorded.JointPain,
			"contraindications": conditions,
			// The other half of the filter. An event that carried findings and not questions
			// would rebuild into a read model that cannot tell a skipped question from a
			// negative answer.
			"asked":       asked,
			"note":        recorded.Note,
			"recorded_at": recorded.RecordedAt,
			// [R-03]: who found this, without digging.
			"recorded_by":   e.Actor.UserID().String(),
			"recorded_role": e.Actor.Role(),
			"station_code":  e.Actor.Station(),
			"device_id":     deviceOf(e),
			"source":        string(e.Source),
			"event_id":      e.EventID.String(),
			"global_seq":    e.GlobalSeq,
		}
		// Absent rather than zero. A patient who could not say how far they walk is a different
		// record from one who walks no minutes at all, and the column is nullable so that the
		// difference survives.
		if recorded.WalksUnaided != nil {
			payload["walks_unaided"] = *recorded.WalksUnaided
		}
		if recorded.WalkMinutes != nil {
			payload["walk_minutes"] = *recorded.WalkMinutes
		}
		return call(ctx, tx, "read.apply_exercise_assessment_recorded", payload)

	case "EXERCISE_PLAN_ISSUED":
		var issued eventstore.ExercisePlanIssued
		if err := json.Unmarshal(e.Payload, &issued); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		items := make([]map[string]any, 0, len(issued.Items))
		for _, item := range issued.Items {
			ordering := item.Ordering
			if ordering == 0 {
				ordering = 100
			}
			items = append(items, map[string]any{
				"exercise_code":       item.ExerciseCode,
				"times_per_week":      item.TimesPerWeek,
				"minutes_per_session": item.MinutesPerSession,
				"ordering":            ordering,
				"note":                item.Note,
			})
		}
		return call(ctx, tx, "read.apply_exercise_plan_issued", map[string]any{
			"plan_id":     issued.PlanID,
			"facility_id": e.Actor.FacilityID().String(),
			"patient_id":  issued.PatientID,
			"visit_id":    issued.VisitID,
			// Frozen: the findings this plan was filtered against, not the ones true now.
			"assessment_id": issued.AssessmentID,
			"items":         items,
			"note":          issued.Note,
			"issued_at":     issued.IssuedAt,
			"issued_by":     e.Actor.UserID().String(),
			"issued_role":   e.Actor.Role(),
			"station_code":  e.Actor.Station(),
			"device_id":     deviceOf(e),
			"source":        string(e.Source),
			"event_id":      e.EventID.String(),
			"global_seq":    e.GlobalSeq,
		})
	}
	return nil
}

// Reset empties the tables for a rebuild.
//
// Plans before assessments, because a plan references the assessment it was filtered against and
// the foreign key is the thing keeping that reference honest. The items go with the plan through
// `ON DELETE CASCADE`.
func (Exercise) Reset(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `DELETE FROM read.exercise_plan`); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM read.exercise_assessment`)
	return err
}
