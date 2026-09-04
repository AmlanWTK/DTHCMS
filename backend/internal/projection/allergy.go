package projection

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// Allergy is the hard stop's read model (CP54, §3 step 4).
//
// **Synchronous, and this one has no arguable alternative.** The gate that stops a patient
// advancing past the history station is a trigger on the queue, and it reads these tables. A
// read model that caught up a second later would mean an operator records an allergy, taps
// "send to examination", and is told the patient has no allergy status — or, far worse, the
// order reverses under load and somebody advances on a status that has not landed.
//
// Three event types, two tables. The assertion's own projection function also withdraws any
// earlier one, because "no known allergies" and "we could not ask" cannot both be the current
// answer and a status function choosing between them by timestamp is a coin toss waiting to
// happen.
type Allergy struct{}

var _ Projection = Allergy{}

func (Allergy) Name() string { return "allergy" }
func (Allergy) Version() int { return 1 }
func (Allergy) Mode() Mode   { return Synchronous }

func (Allergy) Handles(eventType string) bool {
	switch eventType {
	case "ALLERGY_RECORDED", "ALLERGY_STATUS_ASSERTED", "ALLERGY_WITHDRAWN":
		return true
	}
	return false
}

func (Allergy) Apply(ctx context.Context, tx pgx.Tx, e eventstore.Event) error {
	switch e.EventType {
	case "ALLERGY_RECORDED":
		var recorded eventstore.AllergyRecorded
		if err := json.Unmarshal(e.Payload, &recorded); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_allergy_recorded", map[string]any{
			"allergy_id": recorded.AllergyID,
			// From the envelope, as every attribution is: the facility a record belongs to
			// is the one the writer was signed in to, not one a body can name.
			"facility_id":  e.Actor.FacilityID().String(),
			"patient_id":   recorded.PatientID,
			"visit_id":     recorded.VisitID,
			"code_system":  recorded.CodeSystem,
			"code_version": recorded.CodeVersion,
			"code":         recorded.Code,
			"said":         recorded.Said,
			"reaction":     recorded.Reaction,
			"severity":     recorded.Severity,
			"certainty":    recorded.Certainty,
			"note":         recorded.Note,
			"recorded_at":  recorded.RecordedAt,
			// Criterion 3's other half: an allergy on a screen is only worth as much as the
			// name beside it, because the next clinician's first question is who was told.
			"recorded_by":   e.Actor.UserID().String(),
			"recorded_role": e.Actor.Role(),
			"event_id":      e.EventID.String(),
			"global_seq":    e.GlobalSeq,
		})

	case "ALLERGY_STATUS_ASSERTED":
		var asserted eventstore.AllergyStatusAsserted
		if err := json.Unmarshal(e.Payload, &asserted); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_allergy_status_asserted", map[string]any{
			"assertion_id": asserted.AssertionID,
			"facility_id":  e.Actor.FacilityID().String(),
			"patient_id":   asserted.PatientID,
			"visit_id":     asserted.VisitID,
			"kind":         asserted.Kind,
			"reason":       asserted.Reason,
			"asserted_at":  asserted.AssertedAt,
			// Criterion 2. The one field in this module a review would actually turn on: a
			// client that could name the asserting user could put a colleague's name against
			// "no known allergies" for a patient who is allergic to penicillin.
			"asserted_by":   e.Actor.UserID().String(),
			"asserted_role": e.Actor.Role(),
			"event_id":      e.EventID.String(),
			"global_seq":    e.GlobalSeq,
		})

	case "ALLERGY_WITHDRAWN":
		var withdrawn eventstore.AllergyWithdrawn
		if err := json.Unmarshal(e.Payload, &withdrawn); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		if withdrawn.AllergyID != "" {
			return call(ctx, tx, "read.apply_allergy_removed", map[string]any{
				"allergy_id": withdrawn.AllergyID,
				"reason":     withdrawn.Reason,
				"removed_at": withdrawn.WithdrawnAt,
				"removed_by": e.Actor.UserID().String(),
			})
		}
		return call(ctx, tx, "read.apply_allergy_assertion_withdrawn", map[string]any{
			"assertion_id": withdrawn.AssertionID,
			"reason":       withdrawn.Reason,
			"withdrawn_at": withdrawn.WithdrawnAt,
			"withdrawn_by": e.Actor.UserID().String(),
		})
	}
	return nil
}

// Reset empties both tables for a rebuild. The ledger keeps every allergy ever recorded and
// every assertion ever made; these are only their current shape.
//
// The order matters no more than it usually does, but the gate does: a rebuild that emptied
// these tables and then replayed would, for the length of the replay, have a clinic in which
// no patient has allergy status. That is why a rebuild runs as `dthcms_projector` against a
// database nobody is queueing into, and why the gate is on the write path rather than the
// read one — a queue entry that already exists is not re-checked.
func (Allergy) Reset(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `DELETE FROM read.allergy_assertion`); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM read.allergy`)
	return err
}
