package main

import (
	"context"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/education"
	"github.com/AmlanWTK/DTHCMS/backend/internal/prescription"
)

// The one fact the education station needs from the prescription (CP92).
//
// Criterion 1 is that the right checklist appears with no manual selection, which means station
// 11 has to know what was prescribed at station 9. It does not need the sheet: not its status,
// not its doses, not its price capture, not its correction chain. It needs the product ids on
// the live lines, and everything after that is its own reference data.
//
// So `education` declares a two-line interface and this is the only place that implements it.
// The alternative — `education` importing `prescription` — would pull the safety engine, the
// formulary and the AI gateway into a module whose whole job is a checklist, and would make a
// clinic that ran the education station without prescribing impossible to configure.
//
// `prescription` learns nothing about this in either direction. Nothing in that module changes
// for CP92, and a clinic that removed station 11 would not touch it.

type prescribedDevices struct {
	store *prescription.Store
}

var _ education.Sheets = (*prescribedDevices)(nil)

// PrescribedDevices is this visit's live prescription lines.
//
// # Which prescription, when a visit has more than one
//
// It reads the patient's recent prescriptions and takes those belonging to this visit. A visit
// normally has exactly one, but a corrected sheet is a *second* prescription that supersedes the
// first (CP81), and both carry the same visit id — so taking "the one for this visit" without
// saying which would be a coin toss between the corrected sheet and the one it replaced.
//
// The newest wins. `ForPatient` returns newest first, and a correction is by construction newer
// than what it corrects. That is the right answer for this station: the officer is teaching the
// patient to use the device on the sheet they are about to be handed, not the one that was
// withdrawn ten minutes ago.
//
// # Why removed lines are dropped and cancelled sheets are not
//
// A line removed from the sheet is a device the patient is not going home with, and a checklist
// for it would be an assessment of something that is not happening. A *cancelled* prescription
// is a different matter and is deliberately still read: the station may legitimately be working
// from a sheet that the physician cancels while the patient is in the room, and a screen that
// emptied itself mid-assessment would lose what the officer had already recorded. The write is
// what is bounded by reach, not the read.
func (p *prescribedDevices) PrescribedDevices(ctx context.Context,
	patient, visit, facility uuid.UUID) ([]education.PrescribedLine, error) {

	sheets, err := p.store.ForPatient(ctx, patient, facility, 20)
	if err != nil {
		return nil, err
	}
	var newest *prescription.Prescription
	for i := range sheets {
		if sheets[i].VisitID != visit {
			continue
		}
		if newest == nil || sheets[i].CreatedAt.After(newest.CreatedAt) {
			newest = &sheets[i]
		}
	}
	if newest == nil {
		// No prescription for this visit yet. Not an error: a patient who reaches station 11
		// before the sheet is signed is early, and the officer still has the compliance question
		// and the improvement score to ask.
		return []education.PrescribedLine{}, nil
	}
	full, err := p.store.ByID(ctx, newest.ID, facility)
	if err != nil {
		return nil, err
	}
	out := make([]education.PrescribedLine, 0, len(full.Items))
	for _, item := range full.Items {
		if !item.Live() {
			continue
		}
		out = append(out, education.PrescribedLine{
			ProductID:    item.ProductID,
			Label:        item.Label,
			GenericName:  item.GenericName,
			DurationDays: item.DurationDays,
		})
	}
	return out, nil
}
