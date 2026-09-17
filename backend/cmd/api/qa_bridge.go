package main

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
	"github.com/AmlanWTK/DTHCMS/backend/internal/qa"
)

// Station 10's two bridges (CP83).
//
// # The demographics one, and why it is an interface at all
//
// `qa` reads the modules it consults directly — it is not under CP78's stricter isolation,
// because a QA officer legitimately works on one named patient's file. The register is the
// exception. `internal/patient` is thirty columns of identity, photographs, identifiers and merge
// history, and what rule 14 needs from it is an age and a sex. Importing the whole module for two
// fields would put the register inside the blast radius of every future change to this station,
// so the two fields cross a four-line interface instead.
type qaDemographicsBridge struct {
	patients *patient.Store
	clock    interface{ Now() time.Time }
}

var _ qa.Demographics = (*qaDemographicsBridge)(nil)

func (b *qaDemographicsBridge) now() time.Time {
	if b.clock == nil {
		return time.Now().UTC()
	}
	return b.clock.Now().UTC()
}

// AgeAndSex returns fractional years and the register's sex.
//
// A patient this caller cannot see returns "not recorded" for both rather than an error, for the
// reason `medsafetyFactsBridge.Age` does the same: the route itself is permission-guarded, so
// this is the absence and not the refusal. And the absence is the fail-closed direction here —
// rule 14 treats an unknown age as inside its band, so a patient whose record could not be read
// gets the question asked rather than skipped.
func (b *qaDemographicsBridge) AgeAndSex(ctx context.Context, facility,
	id uuid.UUID) (*float64, string, error) {

	record, err := b.patients.ByID(ctx, id, facility)
	if err != nil {
		return nil, "", nil //nolint:nilerr // absence, not failure — see the comment.
	}
	years := float64(record.Birth.Age(b.now()))
	if years < 0 {
		return nil, string(record.Sex), nil
	}
	return &years, string(record.Sex), nil
}

// ---------------------------------------------------------------------------
// The audit trail
// ---------------------------------------------------------------------------

// qaAuditBridge puts station 10's three recordable acts into the security trail.
//
// # Why the security trail and not the clinical ledger
//
// Both, in fact, and they carry different things. The clinical facts — the findings, the bounce
// reason, the acknowledged warnings — are in the ledger and in `read.qa_review`, because they are
// a patient's record. What goes here is *who did it*: the log somebody reads when asking "who
// cleared this file", "how often is this consultant overriding" and "who changed the checklist".
//
// **Nothing here carries clinical content.** Not a drug, not a missing test, not a diagnosis.
// The counts say a decision happened and what shape it was; the override's reason travels because
// the reason is the whole point of the override being legible, and it is a sentence about a
// clinical situation rather than about the patient.
type qaAuditBridge struct {
	recorder *audit.Recorder
}

var _ qa.Audit = (*qaAuditBridge)(nil)

func (b *qaAuditBridge) Decided(ctx context.Context, d qa.DecisionAudit) error {
	actor := d.ActorID
	kind := "qa.cleared"
	if d.Outcome == qa.OutcomeBounced {
		kind = "qa.bounced"
	}
	_, err := b.recorder.Record(ctx, audit.Entry{
		Kind:       kind,
		FacilityID: d.FacilityID,
		ActorID:    &actor,
		ActorCode:  d.ActorCode,
		ActorRole:  d.ActorRole,
		PatientID:  &d.PatientID,
		Details: map[string]any{
			"visit":        d.VisitID.String(),
			"prescription": d.PrescriptionID.String(),
			// The station a patient was sent to is not clinical detail: it is a room.
			"bounce_station": d.BounceStation,
			"blocking":       d.Blocking,
			"warnings":       d.Warnings,
			"acknowledged":   d.Acknowledged,
			// Whether this clearance stood on an override is the single most important thing
			// this row can say, because a clearance granted over a block is a different act from
			// a clearance granted over nothing.
			"on_override": d.OnOverride,
		},
	})
	return err
}

func (b *qaAuditBridge) Overridden(ctx context.Context, o qa.OverrideAudit) error {
	actor := o.ActorID
	_, err := b.recorder.Record(ctx, audit.Entry{
		Kind:       "qa.overridden",
		FacilityID: o.FacilityID,
		ActorID:    &actor,
		ActorCode:  o.ActorCode,
		ActorRole:  o.ActorRole,
		PatientID:  &o.PatientID,
		Details: map[string]any{
			"visit":        o.VisitID.String(),
			"prescription": o.PrescriptionID.String(),
			"reason":       o.Reason,
			"blocking":     o.Blocking,
		},
	})
	return err
}

// RuleChanged is a change to the checklist itself — configuration, with no patient in it.
//
// The field names travel and the values do not. `core.qa_rule` is versioned by `updated_at` and
// `updated_by`; a trail row carrying the new severity would be a second, differently-shaped copy
// of the rule, and the failure mode of two copies is the one this whole checkpoint is about.
func (b *qaAuditBridge) RuleChanged(ctx context.Context, c qa.RuleAudit) error {
	actor := c.ActorID
	_, err := b.recorder.Record(ctx, audit.Entry{
		Kind:       "qa.rule_changed",
		FacilityID: c.FacilityID,
		ActorID:    &actor,
		ActorCode:  c.ActorCode,
		ActorRole:  c.ActorRole,
		Details: map[string]any{
			"rule":    c.RuleCode,
			"changed": c.Change,
		},
	})
	return err
}
