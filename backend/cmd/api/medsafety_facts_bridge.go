package main

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/allergy"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/history"
	"github.com/AmlanWTK/DTHCMS/backend/internal/medsafety"
	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
)

// Reading a patient into a clinical picture (CP78).
//
// `medsafety` may import only `platform`, `formulary` and `clinical` (architecture.json), and
// the facts §7.2 multiplies the proposed drugs by live in four modules. This bridge is where
// they are read, and the restriction it works around is worth keeping rather than widening: what
// crosses into `medsafety` is six clinical facts and **no patient identifier**, so a picture
// cannot acquire one on the way in and no log line in that module can ever say which patient the
// eGFR of 24 belonged to.
//
// # Nil means nobody asked, and every method here is careful about it
//
// This is the whole fail-closed contract, and it is easy to get wrong in the direction that
// looks fine. Returning `[]string{}` where the truth is "the diagnosis list was not read" turns
// every contraindication rule from *cannot verify* into *does not apply*, silently, with no
// error anywhere. So each method below says in its comment which absence it is reporting, and
// `TestTheFactsBridgeDistinguishesNobodyAskedFromNoneRecorded` holds it to that.
//
// # Two facts this system does not record at all, stated rather than faked
//
// **Pregnancy status is not a field anywhere in DTHCMS.** There is no observation code for it and
// no history kind. The only thing in the record that implies it is the ICD-10 code O24.4,
// diabetes arising in pregnancy, which a patient cannot carry without being pregnant. So that is
// what [pregnancyFrom] derives, and everything else is **unknown** — which makes all seven of
// CP77's pregnancy rules answer "cannot verify" for every patient who is not coded O24.4. That is
// the correct behaviour and it is also a gap somebody has to close; it is written up for Dr.
// Nahid rather than papered over with a default of NOT_PREGNANT, which would silently disarm the
// two BLOCK rules on ACE inhibitors and ARBs.
//
// **Hepatic impairment is not graded anywhere either.** No observation code, no assessment. This
// bridge therefore always returns unknown, and the three hepatic rules always answer "cannot
// verify". Same reasoning: a default of NONE would be a fabricated clinical assessment.
type medsafetyFactsBridge struct {
	patients     *patient.Store
	observations *clinical.Store
	histories    *history.Store
	allergies    *allergy.Store
	clock        interface{ Now() time.Time }
}

var _ medsafety.PatientFacts = (*medsafetyFactsBridge)(nil)

func (b *medsafetyFactsBridge) now() time.Time {
	if b.clock == nil {
		return time.Now().UTC()
	}
	return b.clock.Now().UTC()
}

// Age returns the patient's age in years, or nil when the birth date is not usable.
//
// Fractional rather than whole years, because CP77's paediatric rules compare against a number
// and "under 18" applied to a person who is seventeen years and eleven months has to be true.
func (b *medsafetyFactsBridge) Age(ctx context.Context, facility, id uuid.UUID) (*float64, error) {
	record, err := b.patients.ByID(ctx, id, facility)
	if err != nil {
		// A patient this caller cannot see is not an error the engine can do anything with,
		// and the age is simply unknown. The route itself is permission-guarded; this is the
		// absence, not the refusal.
		return nil, nil //nolint:nilerr // absence, not failure — see the comment.
	}
	years := float64(record.Birth.Age(b.now()))
	if years < 0 {
		return nil, nil
	}
	return &years, nil
}

// Pregnancy derives what the record can support, and unknown otherwise. See the type comment.
func (b *medsafetyFactsBridge) Pregnancy(ctx context.Context, facility, id uuid.UUID) (string, error) {
	items, err := b.histories.ForPatient(ctx, id)
	if err != nil {
		return "", err
	}
	return pregnancyFrom(items), nil
}

// pregnancyFrom is the one derivation the record supports.
//
// O24.4 is "diabetes mellitus arising in pregnancy", and it is the only concept in this
// database's ICD-10 subset that entails pregnancy. An active, unremoved O24.4 on the problem
// list means the patient is pregnant; anything else means **nobody has recorded it**, which is
// the empty string and which fails closed.
//
// Deliberately not widened to "she was coded O24.4 at some point". A gestational diabetes code
// from two years ago says nothing about today, and a pregnancy rule firing on it would be the
// kind of false positive that teaches a physician to click past the true ones.
func pregnancyFrom(items []history.Item) string {
	for _, item := range items {
		if item.Status != "ACTIVE" {
			continue
		}
		if item.Kind != "COMORBIDITY" && item.Kind != "COMPLAINT" {
			continue
		}
		if strings.EqualFold(item.Code, "O24.4") {
			return "PREGNANT"
		}
	}
	return ""
}

// Renal returns the most recent eGFR and when it was effective.
//
// The value and its date, never the value alone. CP79 owns the staleness window — an eGFR from
// two years ago is not current renal function — and it needs the date this returns to apply one.
// Returning the value without it would make that checkpoint's first task undoing this one.
func (b *medsafetyFactsBridge) Renal(ctx context.Context, facility, id uuid.UUID) (*float64, *time.Time, error) {
	rows, err := b.observations.History(ctx, id, facility, "EGFR", 1)
	if err != nil {
		return nil, nil, err
	}
	for _, row := range rows {
		if row.Value == nil {
			continue
		}
		// A zero eGFR is anuric renal failure and not "unknown". Copied rather than
		// referenced, because the row is reused by the loop.
		value := *row.Value
		when := row.EffectiveAt
		return &value, &when, nil
	}
	return nil, nil, nil
}

// Hepatic is always unknown. See the type comment: nothing in this system grades liver function,
// and a default of NONE would be a fabricated assessment that disarms two BLOCK rules.
func (b *medsafetyFactsBridge) Hepatic(context.Context, uuid.UUID, uuid.UUID) (string, error) {
	return "", nil
}

// Diagnoses returns the patient's coded conditions.
//
// **Nil means the list could not be read**; an empty non-nil slice means it was read and there
// are none. Comorbidities and family history both, because CP77's `GLP1-MTC` rule is keyed to
// medullary thyroid carcinoma *or MEN2 in a first-degree relative* — a family history code is
// what that rule is actually about, and dropping family history would make the one rule that
// blocks a semaglutide prescription unable to fire.
//
// Uncoded items are skipped, because a rule matches on a code. That is a real limit and it is
// visible: CP53 counts uncoded history items precisely because the safety engine cannot match
// them.
func (b *medsafetyFactsBridge) Diagnoses(ctx context.Context, facility, id uuid.UUID) ([]string, error) {
	items, err := b.histories.ForPatient(ctx, id)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, item := range items {
		if item.Status != "ACTIVE" || item.Code == "" {
			continue
		}
		switch item.Kind {
		case "COMORBIDITY", "FAMILY_HISTORY", "COMPLAINT", "SURGICAL_HISTORY":
			out = append(out, item.Code)
		}
	}
	return out, nil
}

// Allergies returns what the patient reports.
//
// **Nil when allergy status has not been established.** CP54 models exactly this: `NONE_RECORDED`
// is "nobody has asked", and it is a different fact from `NO_KNOWN_ALLERGY`, which is an
// attributed assertion by a named person. The first must fail closed and the second must not, and
// this is the one line in the bridge where that distinction is made.
func (b *medsafetyFactsBridge) Allergies(ctx context.Context, facility, id uuid.UUID) ([]medsafety.ReportedAllergy, error) {
	state, err := b.allergies.For(ctx, id)
	if err != nil {
		return nil, err
	}
	switch state.Status {
	case allergy.StatusNone, allergy.StatusUnable:
		// Nobody has asked, or somebody tried and could not. Both are "not established".
		// UNABLE_TO_ASSESS is deliberately on this side of the line: a patient who could not
		// be asked is a patient whose allergies are unknown, and the reviewable reason CP54
		// requires does not make them known.
		return nil, nil
	}
	out := make([]medsafety.ReportedAllergy, 0, len(state.Allergies))
	for _, item := range state.Allergies {
		out = append(out, medsafety.ReportedAllergy{
			Code: item.Code, Display: item.DisplayEN, Said: item.Said,
			Emergency: item.IsEmergency || item.Severity == "life_threatening",
		})
	}
	// Non-nil even when empty: NO_KNOWN_ALLERGY is somebody's assertion that there are none,
	// which is what legitimately lets an allergy rule answer "does not apply".
	return out, nil
}

// CurrentMedications returns what the patient is already taking.
//
// **Nil when the list could not be read.** An empty non-nil slice means the medication history
// was read and there is nothing on it — which is what lets an interaction rule written against
// current medications answer "does not apply" instead of "cannot verify".
//
// Uncoded items carry their words through as the generic name. The engine will fail to resolve
// most of them and report them uncovered, which is the honest outcome: "the white tablet for
// blood pressure" is a medication the safety engine genuinely cannot check anything against.
func (b *medsafetyFactsBridge) CurrentMedications(ctx context.Context, facility, id uuid.UUID) ([]medsafety.Item, error) {
	items, err := b.histories.ForPatient(ctx, id)
	if err != nil {
		return nil, err
	}
	out := []medsafety.Item{}
	for _, item := range items {
		if item.Kind != "MEDICATION" || item.Status != "ACTIVE" {
			continue
		}
		entry := medsafety.Item{
			Ref:     item.ID.String(),
			Generic: firstNonBlank(item.DisplayEN, item.Said),
			Label:   firstNonBlank(item.Said, item.DisplayEN),
		}
		if item.FormularyProductID != "" {
			if productID, parseErr := uuid.Parse(item.FormularyProductID); parseErr == nil {
				entry.ProductID = &productID
				// The product is the better identification, so the free text stops being
				// the generic and stays only as the label.
				entry.Generic = ""
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

func firstNonBlank(in ...string) string {
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
