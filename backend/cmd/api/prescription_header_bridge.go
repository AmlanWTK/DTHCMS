package main

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
	"github.com/AmlanWTK/DTHCMS/backend/internal/prescription"
)

// The name and age on the printed sheet (CP81, docs/prescriptions.md §10).
//
// `prescription` may not import `patient` (architecture.json), and the restriction is worth
// keeping rather than widening. What crosses this boundary is a clinical id, two names, a sex and
// an age — five display strings — so a print model cannot grow a national identifier because
// somebody added a field to the patient record, and no future field on `patient.Patient` reaches
// the sheet without a line being written here on purpose.
//
// # Why a failed lookup is not an error
//
// It returns one, and `printModel` turns it into an *unresolved* header rather than a 500. A
// preview of the medicine list is worth showing to a physician whose patient record is briefly
// unreadable; what would not be acceptable is showing blank name fields, because a blank field
// reads as "this will be filled in when it prints". The unresolved block says, in both languages,
// that it will not be.
type prescriptionHeaderBridge struct {
	patients *patient.Store
	clock    interface{ Now() time.Time }
}

var _ prescription.PatientHeader = (*prescriptionHeaderBridge)(nil)

func (b *prescriptionHeaderBridge) PrescriptionHeader(ctx context.Context,
	facility, id uuid.UUID) (prescription.HeaderFacts, error) {

	record, err := b.patients.ByID(ctx, id, facility)
	if err != nil {
		return prescription.HeaderFacts{}, err
	}
	facts := prescription.HeaderFacts{
		ClinicalID: record.ClinicalID,
		NameEN:     record.NameEN,
		NameBN:     record.NameBN,
		Sex:        string(record.Sex),
	}
	now := time.Now().UTC()
	if b.clock != nil {
		now = b.clock.Now().UTC()
	}
	// Whole years, and only when the record can support one. A negative age is a birth date
	// nobody has fixed yet, and printing "-3 years" on a prescription would be worse than
	// printing nothing — so it is nothing, and the sheet says the age is not recorded.
	if years := record.Birth.Age(now); years >= 0 {
		whole := years
		facts.AgeYears = &whole
	}
	return facts, nil
}
