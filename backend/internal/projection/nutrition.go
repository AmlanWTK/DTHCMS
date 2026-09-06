package projection

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// Nutrition is the 24-hour recall's read model (CP59, station 7).
//
// **Synchronous**, and for a reason particular to this station: two assistants may be entering one
// recall at the same time from two devices, and each has to see the other's items appear. A read
// model a second behind would show one operator a recall missing the rice the other just recorded,
// and the natural next act is to record it again — which is how a design that removed conflicts
// gets duplicates put back into it by the interface.
//
// The projection function computes the grams and the energy from the food table rather than taking
// them from the event, which is the other half of criterion 3: what a recall adds up to is the
// table's arithmetic, never a client's.
type Nutrition struct{}

var _ Projection = Nutrition{}

func (Nutrition) Name() string { return "nutrition" }
func (Nutrition) Version() int { return 1 }
func (Nutrition) Mode() Mode   { return Synchronous }

func (Nutrition) Handles(eventType string) bool {
	switch eventType {
	case "DIET_ENTRY_RECORDED", "DIET_ENTRY_WITHDRAWN":
		return true
	}
	return false
}

func (Nutrition) Apply(ctx context.Context, tx pgx.Tx, e eventstore.Event) error {
	switch e.EventType {
	case "DIET_ENTRY_RECORDED":
		var recorded eventstore.DietEntryRecorded
		if err := json.Unmarshal(e.Payload, &recorded); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		payload := map[string]any{
			"entry_id": recorded.EntryID,
			// From the envelope, as every attribution is.
			"facility_id":  e.Actor.FacilityID().String(),
			"patient_id":   recorded.PatientID,
			"visit_id":     recorded.VisitID,
			"recall_date":  recorded.RecallDate,
			"meal":         recorded.Meal,
			"food_code":    recorded.FoodCode,
			"measure_code": recorded.MeasureCode,
			"quantity":     recorded.Quantity,
			"note":         recorded.Note,
			"recorded_at":  recorded.RecordedAt,
			// Criterion 2's other half: a recall two people contributed to is only useful if you
			// can tell which half is whose.
			"recorded_by":   e.Actor.UserID().String(),
			"recorded_role": e.Actor.Role(),
			"station_code":  e.Actor.Station(),
			"device_id":     deviceOf(e),
			"source":        string(e.Source),
			"event_id":      e.EventID.String(),
			"global_seq":    e.GlobalSeq,
		}
		if recorded.EatenAtHour != nil {
			payload["eaten_at_hour"] = *recorded.EatenAtHour
		}
		return call(ctx, tx, "read.apply_diet_entry_recorded", payload)

	case "DIET_ENTRY_WITHDRAWN":
		var withdrawn eventstore.DietEntryWithdrawn
		if err := json.Unmarshal(e.Payload, &withdrawn); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_diet_entry_withdrawn", map[string]any{
			"entry_id": withdrawn.EntryID,
			// From the envelope, and scoped in the projection rather than only in the handler:
			// the projection is the half that holds under a rebuild.
			"facility_id":  e.Actor.FacilityID().String(),
			"reason":       withdrawn.Reason,
			"withdrawn_at": withdrawn.WithdrawnAt,
			"withdrawn_by": e.Actor.UserID().String(),
		})
	}
	return nil
}

// Reset empties the table for a rebuild.
//
// Worth noting what a rebuild does here that it does nowhere else: the grams and the energy are
// recomputed from the *current* food table, so a replay after somebody corrects a portion weight
// produces different numbers from the ones the operator saw. That is the right behaviour — the
// answer the patient gave is on the event and unchanged, and the interpretation of it should
// improve when the table does — but it is a real difference and worth knowing before running one.
func (Nutrition) Reset(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `DELETE FROM read.diet_entry`)
	return err
}
