package projection

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// History is what the patient brings with them, kept current across visits (CP53, §3
// station 4, §11.1).
//
// **Synchronous.** Station 4 records an item and station 5 has to see it in the same minute —
// the patient walks between the two rooms — and station 8's prescribing depends on the
// medication list being what was just taken rather than what the projector has caught up to.
// A read model that lagged here would show a physician a history that is a few seconds old in
// exactly the situation where a few seconds is a different patient.
//
// Four event types, one row, and each answers a different question about the same item:
//
//   - **recorded** — somebody wrote this down. Creates the row and is the only event that
//     decides what the item *is*.
//   - **confirmed** — somebody said it is still true. This is acceptance criterion 3, and it
//     is an event rather than a column default because that is the entire difference between
//     "a person asserted this" and "the software assumed it".
//   - **amended** — what is known about it changed. Never what it is: the kind and the coding
//     are fixed at recording, because changing them is removing one item and adding another.
//   - **removed** — it should not have been recorded. Distinct from RESOLVED, which is a
//     clinical fact rather than a correction.
//
// None of them can un-happen, and each guards on the row's state in the database function, so
// a replay in any order lands on the same row.
type History struct{}

var _ Projection = History{}

func (History) Name() string { return "history_item" }
func (History) Version() int { return 1 }
func (History) Mode() Mode   { return Synchronous }

func (History) Handles(eventType string) bool {
	switch eventType {
	case "HISTORY_ITEM_RECORDED",
		"HISTORY_ITEM_CONFIRMED",
		"HISTORY_ITEM_AMENDED",
		"HISTORY_ITEM_REMOVED":
		return true
	}
	return false
}

func (History) Apply(ctx context.Context, tx pgx.Tx, e eventstore.Event) error {
	switch e.EventType {
	case "HISTORY_ITEM_RECORDED":
		var item eventstore.HistoryItemRecorded
		if err := json.Unmarshal(e.Payload, &item); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		row := map[string]any{
			"item_id": item.ItemID,
			// From the envelope, like every other projection: the facility a record belongs
			// to is the facility the writer was signed in to, not one a body can name.
			"facility_id":     e.Actor.FacilityID().String(),
			"patient_id":      item.PatientID,
			"visit_id":        item.VisitID,
			"kind":            item.Kind,
			"code_system":     item.CodeSystem,
			"code_version":    item.CodeVersion,
			"code":            item.Code,
			"said":            item.Said,
			"relation":        item.Relation,
			"severity":        item.Severity,
			"onset_on":        item.OnsetOn,
			"onset_precision": item.OnsetPrecision,
			"dose":            item.Dose,
			"frequency":       item.Frequency,

			"formulary_product_id": item.FormularyProductID,
			"reconciliation":       item.Reconciliation,

			"recorded_at": item.RecordedAt,
			// Criterion 4, and it comes from the envelope for the reason every attribution
			// does: a client that could name the recording user could put a colleague's name
			// on a clinical assertion they never made.
			"recorded_by":   e.Actor.UserID().String(),
			"recorded_role": e.Actor.Role(),

			"event_id":   e.EventID.String(),
			"global_seq": e.GlobalSeq,
		}
		// Absent rather than empty: the projection function reads "" as null, and a duration
		// of zero days ("since this morning") is a real answer that must not become null.
		if item.DurationDays != nil {
			row["duration_days"] = *item.DurationDays
		} else {
			row["duration_days"] = ""
		}
		return call(ctx, tx, "read.apply_history_item_recorded", row)

	case "HISTORY_ITEM_CONFIRMED":
		var confirmed eventstore.HistoryItemConfirmed
		if err := json.Unmarshal(e.Payload, &confirmed); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_history_item_confirmed", map[string]any{
			"item_id":      confirmed.ItemID,
			"confirmed_at": confirmed.ConfirmedAt,
			"confirmed_by": e.Actor.UserID().String(),
			"visit_id":     confirmed.VisitID,
		})

	case "HISTORY_ITEM_AMENDED":
		var amended eventstore.HistoryItemAmended
		if err := json.Unmarshal(e.Payload, &amended); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		row := map[string]any{
			"item_id":         amended.ItemID,
			"said":            amended.Said,
			"severity":        amended.Severity,
			"onset_on":        amended.OnsetOn,
			"onset_precision": amended.OnsetPrecision,
			"dose":            amended.Dose,
			"frequency":       amended.Frequency,
			"status":          amended.Status,
			"amended_at":      amended.AmendedAt,
			"amended_by":      e.Actor.UserID().String(),
			"visit_id":        amended.VisitID,
		}
		if amended.DurationDays != nil {
			row["duration_days"] = *amended.DurationDays
		} else {
			row["duration_days"] = ""
		}
		// The reconciliation key is present only when the amendment actually decided
		// something about the formulary. The database function branches on the key rather
		// than on the value, because "matched to nothing" and "not asked" are different
		// answers and only one of them should clear a product id.
		if amended.Reconciliation != "" {
			row["reconciliation"] = amended.Reconciliation
			row["formulary_product_id"] = amended.FormularyProductID
		}
		return call(ctx, tx, "read.apply_history_item_amended", row)

	case "HISTORY_ITEM_REMOVED":
		var removed eventstore.HistoryItemRemoved
		if err := json.Unmarshal(e.Payload, &removed); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_history_item_removed", map[string]any{
			"item_id":    removed.ItemID,
			"reason":     removed.Reason,
			"removed_at": removed.RemovedAt,
			"removed_by": e.Actor.UserID().String(),
		})
	}
	return nil
}

// Reset empties the read model for a rebuild. The ledger keeps every item ever recorded,
// every confirmation and every removal; this table is only their current shape.
func (History) Reset(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `DELETE FROM read.history_item`)
	return err
}
