package projection

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// Prescription is the clinic's primary output artefact, projected from the ledger (CP80).
//
// **Synchronous.** A physician who adds a line and immediately runs a safety check must be
// checking the line he just added; a read model that lagged here would run the safety engine
// against a prescription that is one item behind the screen, and the finding it did not produce
// would be the one that mattered.
//
// # Eleven event types, five functions
//
// Four functions for the four things that happen to the content, and one for every transition —
// because a transition is one rule and the rule lives in `core.prescription_transition`. A
// function per status would be seven copies of the same UPDATE differing in a column name.
//
// # The price is applied and never looked up
//
// `apply_prescription_item_added` writes `price_poisha` from the payload. Nothing in this
// projection reads `core.medication_price`, which is the whole of criterion 3: rebuild this
// table from the ledger in 2030 and every line still carries the price of the day it was
// written, because the alternative — a join — would quietly reprice history every time the
// model was rebuilt.
//
// # Idempotent and order-tolerant
//
// The insert functions are `ON CONFLICT DO NOTHING` on the row's own id; the transition function
// is a no-op when the row is already in the target status. A replayed event and a rebuild land
// on the same row. Out-of-order application is where this projection is deliberately *not*
// forgiving: `core.prescription_transition_is_legal()` refuses a status change that is not an
// edge, so a replay that presented SIGNED before QA_REVIEW fails loudly rather than writing a
// state the machine does not allow. For a prescription that is the right trade — a silently
// wrong status on this table is a silently wrong answer to "was this signed".
type Prescription struct{}

var _ Projection = Prescription{}

func (Prescription) Name() string { return "prescription" }
func (Prescription) Version() int { return 1 }
func (Prescription) Mode() Mode   { return Synchronous }

func (Prescription) Handles(eventType string) bool {
	switch eventType {
	case "PRESCRIPTION_CREATED",
		"PRESCRIPTION_ITEM_ADDED",
		"PRESCRIPTION_ITEM_MODIFIED",
		"PRESCRIPTION_ITEM_REMOVED",
		"PRESCRIPTION_SUBMITTED_FOR_QA",
		"PRESCRIPTION_QA_BOUNCED",
		"PRESCRIPTION_SIGNED",
		"PRESCRIPTION_PRINTED",
		"PRESCRIPTION_DISPENSED",
		"PRESCRIPTION_CANCELLED",
		"PRESCRIPTION_CORRECTED":
		return true
	}
	return false
}

func (Prescription) Apply(ctx context.Context, tx pgx.Tx, e eventstore.Event) error {
	switch e.EventType {
	case "PRESCRIPTION_CREATED":
		var created eventstore.PrescriptionCreated
		if err := json.Unmarshal(e.Payload, &created); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_prescription_created", map[string]any{
			"prescription_id": created.PrescriptionID,
			// From the envelope, like every other attribution: a body that could name the
			// facility could put a prescription on another clinic's books.
			"facility_id": e.Actor.FacilityID().String(),
			"patient_id":  created.PatientID,
			"visit_id":    created.VisitID,
			"created_at":  created.CreatedAt,
			// Criterion 2. The prescriber's name comes from the verified session and from
			// nowhere else.
			"created_by":                  e.Actor.UserID().String(),
			"created_role":                e.Actor.Role(),
			"corrects_prescription_id":    created.CorrectsPrescriptionID,
			"correction_reason":           created.CorrectionReason,
			"corrects_dispensed_original": created.CorrectsDispensedOriginal,
			"carried_forward_from":        created.CarriedForwardFrom,
			"event_id":                    e.EventID.String(),
			"global_seq":                  e.GlobalSeq,
		})

	case "PRESCRIPTION_ITEM_ADDED":
		var added eventstore.PrescriptionItemAdded
		if err := json.Unmarshal(e.Payload, &added); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		row := map[string]any{
			"item_id":         added.ItemID,
			"prescription_id": added.PrescriptionID,
			"facility_id":     e.Actor.FacilityID().String(),
			"line_no":         added.LineNo,
			"product_id":      added.ProductID,
			"product_label":   added.ProductLabel,
			"generic_name":    added.GenericName,
			"strength":        added.Strength,
			"form_code":       added.FormCode,
			"dose":            added.Dose,
			"dose_unit":       added.DoseUnit,
			"frequency":       added.Frequency,
			"route":           added.Route,
			"instructions_en": added.InstructionsEN,
			"instructions_bn": added.InstructionsBN,
			// The price, as a number from the payload. **Nothing here reads
			// core.medication_price.**
			"price_id":                  added.PriceID,
			"price_effective_from":      added.PriceEffectiveFrom,
			"price_verification":        added.PriceVerification,
			"carried_forward_from_item": added.CarriedForwardFromItem,
			// CP82. Where this line came from, carried out of the ledger so that a rebuild
			// reproduces the provenance rather than losing it — which is what makes the
			// constraint that refuses an undecided AI line ask the same question of a rebuilt
			// table as of a live one.
			"ai_suggestion_id": added.AISuggestionID,
			"recorded_at":      added.RecordedAt,
			"recorded_by":      e.Actor.UserID().String(),
			"event_id":         e.EventID.String(),
			"global_seq":       e.GlobalSeq,
		}
		// Absent rather than empty for every optional number: the projection function reads
		// "" as null, and a zero is a different clinical fact from an absence in each case.
		putNumber(row, "daily_dose", added.DailyDose)
		putNumber(row, "quantity", added.Quantity)
		putInt(row, "duration_days", added.DurationDays)
		if added.PricePoisha != nil {
			row["price_poisha"] = *added.PricePoisha
		} else {
			row["price_poisha"] = ""
		}
		if added.PriceCapturedAt != nil {
			row["price_captured_at"] = *added.PriceCapturedAt
		} else {
			row["price_captured_at"] = ""
		}
		return call(ctx, tx, "read.apply_prescription_item_added", row)

	case "PRESCRIPTION_ITEM_MODIFIED":
		var modified eventstore.PrescriptionItemModified
		if err := json.Unmarshal(e.Payload, &modified); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		row := map[string]any{
			"prescription_id": modified.PrescriptionID,
			"item_id":         modified.ItemID,
			"line_no":         modified.LineNo,
			"dose":            modified.Dose,
			"dose_unit":       modified.DoseUnit,
			"frequency":       modified.Frequency,
			"route":           modified.Route,
			"instructions_en": modified.InstructionsEN,
			"instructions_bn": modified.InstructionsBN,
			"modified_at":     modified.ModifiedAt,
			"modified_by":     e.Actor.UserID().String(),
			"event_id":        e.EventID.String(),
			"global_seq":      e.GlobalSeq,
		}
		putNumber(row, "daily_dose", modified.DailyDose)
		putNumber(row, "quantity", modified.Quantity)
		putInt(row, "duration_days", modified.DurationDays)
		return call(ctx, tx, "read.apply_prescription_item_modified", row)

	case "PRESCRIPTION_ITEM_REMOVED":
		var removed eventstore.PrescriptionItemRemoved
		if err := json.Unmarshal(e.Payload, &removed); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_prescription_item_removed", map[string]any{
			"prescription_id": removed.PrescriptionID,
			"item_id":         removed.ItemID,
			"reason":          removed.Reason,
			"removed_at":      removed.RemovedAt,
			"removed_by":      e.Actor.UserID().String(),
			"event_id":        e.EventID.String(),
			"global_seq":      e.GlobalSeq,
		})

	case "PRESCRIPTION_SIGNED":
		// The one transition that carries more than a status change (CP84).
		//
		// Decoded into the v2 shape whatever version was stored: v1's fields are a subset, so a
		// pre-CP84 event decodes with the signature fields empty — which is the truth about it,
		// not a reconstruction. `read.apply_prescription_signed` then applies such an event as
		// the transition alone, and the deferred trigger refuses the resulting state at commit.
		// That is the right division: the ledger keeps what was written and the constraint
		// refuses the projection it would produce, rather than this function deciding that an
		// event it cannot fully apply is not worth applying.
		var signed eventstore.PrescriptionSigned
		if err := json.Unmarshal(e.Payload, &signed); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_prescription_signed", map[string]any{
			"prescription_id": signed.PrescriptionID,
			"to_status":       signed.ToStatus,
			"reason":          signed.Reason,
			"correction_id":   signed.CorrectionID,
			"at":              signed.At,
			// From the envelope, like every other attribution here: a payload that could name
			// the signer could put a colleague's name on a controlled drug.
			"actor_id":    e.Actor.UserID().String(),
			"facility_id": e.Actor.FacilityID().String(),

			"canonical_version": signed.CanonicalVersion,
			"canonical_sha256":  signed.CanonicalSHA256,
			"algorithm":         signed.Algorithm,
			"signer_kind":       signed.SignerKind,
			"key_id":            signed.KeyID,
			"public_key":        signed.PublicKey,
			"signature":         signed.Signature,
			"device_assurance":  signed.DeviceAssurance,
			// The clearance **by value**, both halves. The canonical form covers them and
			// verification recomputes from these columns rather than from station 10's record,
			// so a signature that carried the review id without the instant it was decided
			// would be one nobody could re-verify from its own row.
			"qa_review_id":              signed.QAReviewID,
			"qa_cleared_at":             signed.QAClearedAt,
			"verification_token_digest": signed.VerificationTokenDigest,
			"verification_token_sealed": signed.VerificationTokenSealed,
			"verification_token_key_id": signed.VerificationTokenKeyID,

			"event_id":   e.EventID.String(),
			"global_seq": e.GlobalSeq,
		})

	default:
		// Every other transition. One function, because there is one rule and it lives in a
		// table.
		var moved eventstore.PrescriptionTransitioned
		if err := json.Unmarshal(e.Payload, &moved); err != nil {
			return fmt.Errorf("decoding %s: %w", e.EventType, err)
		}
		return call(ctx, tx, "read.apply_prescription_transition", map[string]any{
			"prescription_id": moved.PrescriptionID,
			"to_status":       moved.ToStatus,
			"reason":          moved.Reason,
			"correction_id":   moved.CorrectionID,
			"at":              moved.At,
			"actor_id":        e.Actor.UserID().String(),
			"event_id":        e.EventID.String(),
			"global_seq":      e.GlobalSeq,
		})
	}
}

// Reset empties the read model for a rebuild.
//
// `SET LOCAL dthcms.rebuilding = 'on'` is the one thing that lets a DELETE through
// `core.prescription_is_frozen()`. It is deliberately not a security boundary — a role that can
// set a GUC can set this one — and does not need to be: what it protects is the *projection*,
// and the ledger behind it is append-only by grant, rule and trigger, so a table somebody
// emptied rebuilds into exactly what it was. What the flag stops is the accident: a stray DELETE
// in a later migration or a maintenance script, which would otherwise silently remove a signed
// prescription from the read model with nothing to notice it.
//
// LOCAL, so it lasts exactly this transaction.
func (Prescription) Reset(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SET LOCAL dthcms.rebuilding = 'on'`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM read.prescription_signature`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM read.prescription_item`); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM read.prescription`)
	return err
}

func putNumber(row map[string]any, key string, v *float64) {
	if v == nil {
		row[key] = ""
		return
	}
	row[key] = *v
}

func putInt(row map[string]any, key string, v *int) {
	if v == nil {
		row[key] = ""
		return
	}
	row[key] = *v
}
