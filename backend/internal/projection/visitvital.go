package projection

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// VisitVital is the vitals strip: the current value of every measurement on a visit, with
// who took it and when [R-03].
//
// **Synchronous**, and the only projection in this package that is. §4.1's example is a
// junior doctor whose screen shows what the nurse just entered; a value that is a second
// stale there is a clinician looking at the wrong number. It costs one function call
// inside the append transaction.
//
// The derivation itself is `read.apply_visit_vital(jsonb)` (migration 00015). This type is
// the Go side of it: which events matter, and what the function is given. Keeping the
// derivation in one database function is what lets the application — which may not write
// to `read` at all — maintain the model, and what makes the incremental and rebuilt paths
// literally the same code (ADR-0017).
type VisitVital struct{}

var _ Projection = VisitVital{}

func (VisitVital) Name() string { return "visit_vital" }
func (VisitVital) Version() int { return 1 }
func (VisitVital) Mode() Mode   { return Synchronous }

// vitalEvents maps an event type to the measurement code it lands under, and whether it is
// a correction. A correction writes the same row as the event it corrects — that is the
// point of §7.7: the ledger keeps both, the read model shows the current truth.
var vitalEvents = map[string]struct {
	code      string
	corrected bool
}{
	"HEIGHT_RECORDED":  {"HEIGHT", false},
	"HEIGHT_CORRECTED": {"HEIGHT", true},
	"WEIGHT_RECORDED":  {"WEIGHT", false},
	"WEIGHT_CORRECTED": {"WEIGHT", true},
	"WAIST_RECORDED":   {"WAIST", false},
	"HIP_RECORDED":     {"HIP", false},
	"PULSE_RECORDED":   {"PULSE", false},
	"SPO2_RECORDED":    {"SPO2", false},
	"TEMP_RECORDED":    {"TEMP", false},
	"BP_RECORDED":      {"BP", false},
	"BP_CORRECTED":     {"BP", true},
}

func (VisitVital) Handles(eventType string) bool {
	_, ok := vitalEvents[eventType]
	return ok
}

func (v VisitVital) Apply(ctx context.Context, tx pgx.Tx, e eventstore.Event) error {
	kind, ok := vitalEvents[e.EventType]
	if !ok {
		return nil
	}
	if e.VisitID == nil {
		return fmt.Errorf("%s has no visit_id; the vitals strip is keyed on the visit", e.EventType)
	}

	row := map[string]any{
		"visit_id": e.VisitID.String(), "code": kind.code, "corrected": kind.corrected,
		"facility_id": e.Actor.FacilityID().String(),
		"taken_at":    e.OccurredAt, "recorded_at": e.RecordedAt,
		"actor_user_id": e.Actor.UserID().String(), "actor_role": e.Actor.Role(),
		"actor_station": e.Actor.Station(),
		"event_id":      e.EventID.String(), "global_seq": e.GlobalSeq,
	}
	if e.PatientID != nil {
		row["patient_id"] = e.PatientID.String()
	}

	switch kind.code {
	case "BP":
		var bp eventstore.BloodPressure
		if err := json.Unmarshal(e.Payload, &bp); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		row["value"], row["value_2"], row["unit"] = bp.Systolic, bp.Diastolic, bp.Unit
	default:
		var m eventstore.Measurement
		if err := json.Unmarshal(e.Payload, &m); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		row["value"], row["unit"] = m.Value, m.Unit
	}

	encoded, err := json.Marshal(row)
	if err != nil {
		return err
	}
	// The function advances the checkpoint itself, in this same transaction: for a
	// synchronous projection the event and the read model commit together or not at all,
	// and a checkpoint written separately could disagree with either.
	if _, err := tx.Exec(ctx, `SELECT read.apply_visit_vital($1::jsonb)`, encoded); err != nil {
		return fmt.Errorf("read.apply_visit_vital: %w", err)
	}
	return nil
}

func (VisitVital) Reset(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT read.reset_visit_vital()`)
	return err
}
