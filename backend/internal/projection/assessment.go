package projection

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// Assessment is the lifestyle questionnaire read model (CP58, §3 step 3).
//
// **Synchronous.** An operator who answers three questions and taps "save" is looking at the
// screen that will show them back; a read model a second behind would show a half-filled
// questionnaire to the person who just filled it, and their next act would be to fill it again.
//
// The projection function writes the response and its item rows in one call, and the item rows go
// through a trigger that checks each answer against the item it claims to answer. That check is in
// the database rather than only in the service because a projection rebuild writes these rows too,
// and a rebuild that quietly accepted a numeric answer to a coded question would produce a read
// model nothing could score.
type Assessment struct{}

var _ Projection = Assessment{}

func (Assessment) Name() string { return "assessment" }
func (Assessment) Version() int { return 1 }
func (Assessment) Mode() Mode   { return Synchronous }

func (Assessment) Handles(eventType string) bool {
	return eventType == "LIFESTYLE_ASSESSMENT_RECORDED"
}

func (Assessment) Apply(ctx context.Context, tx pgx.Tx, e eventstore.Event) error {
	if e.EventType != "LIFESTYLE_ASSESSMENT_RECORDED" {
		return nil
	}
	var recorded eventstore.LifestyleAssessmentRecorded
	if err := json.Unmarshal(e.Payload, &recorded); err != nil {
		return fmt.Errorf("decoding %s: %w", e.EventType, err)
	}

	// The answers go into the payload as JSON rather than as a second call per item: a
	// questionnaire is one act, and a response whose fourth item failed to land would be a
	// half-answered questionnaire that looks complete.
	answers, err := json.Marshal(recorded.Answers)
	if err != nil {
		return fmt.Errorf("encoding the answers of %s: %w", e.EventType, err)
	}

	return call(ctx, tx, "read.apply_instrument_response_recorded", map[string]any{
		"response_id": recorded.ResponseID,
		// From the envelope, as every attribution is: the facility a record belongs to is the one
		// the writer was signed in to, not one a body can name.
		"facility_id":        e.Actor.FacilityID().String(),
		"patient_id":         recorded.PatientID,
		"visit_id":           recorded.VisitID,
		"instrument_code":    recorded.InstrumentCode,
		"instrument_version": recorded.InstrumentVersion,
		"recorded_at":        recorded.RecordedAt,
		"recorded_by":        e.Actor.UserID().String(),
		"recorded_role":      e.Actor.Role(),
		"station_code":       e.Actor.Station(),
		"device_id":          deviceOf(e),
		"source":             string(e.Source),
		"answers":            json.RawMessage(answers),
		"event_id":           e.EventID.String(),
		"global_seq":         e.GlobalSeq,
	})
}

// Reset empties both tables for a rebuild. The answers cascade from the responses, and the order
// is stated anyway so that a future reader does not have to check the foreign key to know it.
func (Assessment) Reset(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `DELETE FROM read.instrument_answer`); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM read.instrument_response`)
	return err
}
