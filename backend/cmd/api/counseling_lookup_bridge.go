package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/counseling"
	"github.com/AmlanWTK/DTHCMS/backend/internal/history"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// The two lookups the counselling floor needs, and neither of them belongs to it (CP56).
//
// A counsellor's phone knows the patient in front of it. To start the *right* checklist it needs
// two facts that live elsewhere: whose visit this is, and what that patient's coded conditions
// are. The first is `visit`'s, the second is `history`'s — and `counseling` may not import
// `history` at all. So both arrive as interfaces implemented here, in the composition root,
// which is the only place allowed to know about all three.
//
// The alternative — counselling querying `read.history_item` itself — would work today and would
// be the moment this module grew its own opinion about what a diagnosis is. When the physician's
// diagnosis arrives at a later checkpoint, it becomes another source of codings passed to the
// same interface, and nothing inside `counseling` changes.

type visitPatients struct {
	store *visit.Store
}

var _ counseling.Visits = (*visitPatients)(nil)

func (v *visitPatients) PatientOf(ctx context.Context, id, facility uuid.UUID) (uuid.UUID, error) {
	found, err := v.store.ByID(ctx, id, facility)
	if err != nil {
		// Translated at the seam, so `counseling` can answer 404 for a visit that does not
		// exist without importing `visit` for one sentinel. Before CP74 this error crossed
		// the boundary opaque and the checklist route answered 500 to a stale link.
		if errors.Is(err, visit.ErrNotFound) {
			return uuid.Nil, fmt.Errorf("%w: %s", counseling.ErrNoVisit, id)
		}
		return uuid.Nil, err
	}
	return found.PatientID, nil
}

type historyConditions struct {
	store *history.Store
}

var _ counseling.Conditions = (*historyConditions)(nil)

// CodedConditions is the patient's live comorbidities, as codings.
//
// **Comorbidities only, and only live ones.** A presenting complaint is what brought them in
// today and is coded in the clinic's own dictionary rather than ICD, so no assignment rule can
// match it; a family history is somebody else's condition, and a checklist assigned from a
// mother's diabetes would be a counsellor teaching the wrong person's disease. An item that was
// removed or resolved is not a condition the patient has.
func (h *historyConditions) CodedConditions(ctx context.Context,
	patient uuid.UUID) ([]counseling.Coding, error) {

	items, err := h.store.ForPatient(ctx, patient)
	if err != nil {
		return nil, err
	}
	out := make([]counseling.Coding, 0, len(items))
	for _, item := range items {
		if item.Kind != "COMORBIDITY" || item.Status != "ACTIVE" || !item.Coded() {
			continue
		}
		out = append(out, counseling.Coding{
			System: item.CodeSystem, Version: item.CodeVersion, Code: item.Code,
		})
	}
	return out, nil
}
