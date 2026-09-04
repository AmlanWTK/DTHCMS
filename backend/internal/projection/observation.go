package projection

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// Observation is every measured clinical value, in one read model (CP42, §6, §11).
//
// **Synchronous**, for the same reason `VisitVital` is: §4.1's junior doctor is looking at
// a screen that must show what the operator at the next station entered a second ago. A
// value that is asynchronously stale there is a clinician reading the wrong number, and no
// amount of "it catches up" makes that acceptable.
//
// The derivation is `read.apply_observation(jsonb)` (migration 00026). What this type does
// is hand it the event and nothing else — the conversion to canonical units, the plausibility
// band and the unit rule all live in the database, where a projection rebuild and a live
// write go through the same code (ADR-0017).
//
// That placement is deliberate and worth stating: the *ledger* stores what the operator
// entered, and the *projection* converts. Converting on the way into the ledger would freeze
// today's conversion factor into every event ever written; converting on the way out means a
// factor corrected later corrects the whole history on the next rebuild.
type Observation struct{}

var _ Projection = Observation{}

func (Observation) Name() string { return "observation" }
func (Observation) Version() int { return 1 }
func (Observation) Mode() Mode   { return Synchronous }

func (Observation) Handles(eventType string) bool {
	return eventType == "OBSERVATION_RECORDED"
}

func (Observation) Apply(ctx context.Context, tx pgx.Tx, e eventstore.Event) error {
	var recorded eventstore.ObservationRecorded
	if err := json.Unmarshal(e.Payload, &recorded); err != nil {
		return fmt.Errorf("decoding %s: %w", e.EventType, err)
	}

	row := map[string]any{
		"observation_id": recorded.ObservationID,
		"facility_id":    e.Actor.FacilityID().String(),
		"code":           recorded.Code,
		"visit_id":       recorded.VisitID,
		"encounter_id":   recorded.EncounterID,
		"effective_at":   recorded.EffectiveAt,
		"recorded_at":    e.RecordedAt,
		"source":         recorded.Source,
		"note":           recorded.Note,
		// Attribution from the envelope, never from the payload. A client that could name
		// the role an observation was written under could attribute its own writes.
		"recorded_by":   e.Actor.UserID().String(),
		"recorded_role": e.Actor.Role(),
		"station_code":  e.Actor.Station(),
		"device_id":     e.Actor.DeviceID().String(),
		"event_id":      e.EventID.String(),
		"global_seq":    e.GlobalSeq,
		// The operator was warned this value was outside its plausible band and said it was
		// right anyway (CP46). Carried into the read model so that "which rules are staff
		// overriding every day" is one query rather than a ledger replay.
		"implausible_confirmed": recorded.ImplausibleConfirmed,
		"implausible_reason":    recorded.ImplausibleReason,
	}
	if e.PatientID != nil {
		row["patient_id"] = e.PatientID.String()
	} else {
		row["patient_id"] = recorded.PatientID
	}

	// The entered value and the unit it was entered in. The database converts.
	if recorded.Value != nil {
		row["entered_num"] = *recorded.Value
		row["entered_unit"] = recorded.Unit
	}
	if recorded.ValueText != "" {
		row["value_text"] = recorded.ValueText
	}
	if recorded.ValueBool != nil {
		row["value_bool"] = *recorded.ValueBool
	}
	if recorded.ValueCode != "" {
		row["value_code"] = recorded.ValueCode
	}
	if len(recorded.ValueJSON) > 0 {
		row["value_json"] = json.RawMessage(recorded.ValueJSON)
	}
	if recorded.Replaces != "" {
		row["replaces"] = recorded.Replaces
		row["replaced_status"] = recorded.ReplacedStatus
	}
	if recorded.Formula != "" {
		row["formula"] = recorded.Formula
		row["formula_version"] = recorded.FormulaVersion
		row["inputs"] = recorded.Inputs
	}

	encoded, err := json.Marshal(row)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT read.apply_observation($1::jsonb)`, encoded); err != nil {
		return fmt.Errorf("read.apply_observation: %w", err)
	}
	return nil
}

func (Observation) Reset(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `DELETE FROM read.observation`)
	return err
}
