package synthesis

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/allergy"
	"github.com/AmlanWTK/DTHCMS/backend/internal/assessment"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/exercise"
	"github.com/AmlanWTK/DTHCMS/backend/internal/history"
	"github.com/AmlanWTK/DTHCMS/backend/internal/nutrition"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// Reading the stations (CP71).
//
// This file is the *only* place in this package that touches another module, and it contains no
// decisions: it reads, it converts, it hands the result to [Assemble]. The separation is the reason
// the interesting half of this checkpoint has a test that needs no database.
//
// # Why this reads the modules directly rather than through interfaces
//
// Seven ports with seven fakes would be seven more things to keep in step with seven modules that
// are still moving, and the fakes would then be what the assembler was tested against — which is
// how a test suite ends up describing a system nobody has. The seam that matters is one layer up:
// [Assemble] takes a value, and a value is what a test constructs.

// Stations is the set of reads one synthesis needs.
//
// A struct of stores rather than a long parameter list, because the composition root wires it once
// and every caller here would otherwise repeat the same seven arguments.
type Stations struct {
	Visits    *visit.Store
	Clinical  *clinical.Store
	Growth    *clinical.Service
	History   *history.Store
	Allergies *allergy.Store
	Lifestyle *assessment.Service
	Nutrition *nutrition.Store
	Exercise  *exercise.Store
}

// PriorVisitDepth is how many earlier visits the summary carries.
//
// Three. §11.1's memory is per visit and the useful question is "what happened last time, and was
// the plan working" — which three answers and ten does not. Ten would also be ten diagnoses and ten
// plans of free text in a payload that has to fit on one page at the other end.
const PriorVisitDepth = 3

// Gather reads everything one synthesis needs.
//
// The demographics are a parameter rather than a read, and that is the identifier rule showing
// through the signature: the patient record is opened in exactly one place in this package —
// `service.go`, where the gateway's [ai.Subject] is built from it — and nothing downstream of here
// can see a name, a phone number or a date of birth, because nothing downstream of here is given
// one.
//
// Errors are the interesting part. A visit that does not exist is [ErrNotFound] and the run fails;
// **a station module that cannot answer is not**. A nutrition read that fails costs the summary its
// nutrition section and nothing else, and the gap that leaves is reported to the model like any
// other absence. That asymmetry is D-15 applied inside the assembly: the alternative is a summary
// nobody gets because one of seven optional reads was slow.
func Gather(ctx context.Context, s Stations, visitID, facility uuid.UUID, who Demographics,
	note func(string, error)) (Raw, error) {

	if note == nil {
		note = func(string, error) {}
	}

	current, err := s.Visits.ByID(ctx, visitID, facility)
	if err != nil {
		return Raw{}, ErrNotFound
	}

	raw := Raw{
		Location:     visit.Dhaka,
		Visit:        current,
		Demographics: who,
		Trends:       map[string][]clinical.Observation{},
	}

	if encounters, err := s.Visits.Encounters(ctx, visitID, facility); err != nil {
		note("encounters", err)
	} else {
		raw.Encounters = encounters
	}
	if planned, err := s.Visits.Planned(ctx, facility, current.VisitType); err != nil {
		note("station_sequence", err)
	} else {
		raw.Planned = planned
	}

	if observations, err := s.Clinical.ForPatient(ctx, current.PatientID, facility, "", 200); err != nil {
		note("observations", err)
	} else {
		raw.Current = observations
	}
	if ranges, err := s.Clinical.Ranges(ctx); err != nil {
		note("reference_ranges", err)
	} else {
		raw.Ranges = ranges
	}
	// The unit catalogue decides how a unit is written and to how many decimals its values are
	// shown. Without it the payload says "196 mm[Hg]" and "54.099 mmol/mol" — the machine's
	// spelling of a physician's unit, and a precision no assay has.
	if units, err := s.Clinical.Units(ctx); err != nil {
		note("units", err)
	} else {
		raw.Units = units
	}
	for _, code := range TrendCodes {
		series, err := s.Clinical.History(ctx, current.PatientID, facility, code, TrendDepth*3)
		if err != nil {
			note("trend:"+code, err)
			continue
		}
		raw.Trends[code] = series
	}
	if alerts, err := s.Clinical.AlertsForPatient(ctx, current.PatientID, facility, alertDepth); err != nil {
		note("alerts", err)
	} else {
		raw.Alerts = alerts
	}

	// [R-06]. The growth service already scores every measurement against the seeded reference with
	// the standard, its version and the L, M and S parameters recorded per value. Nothing here
	// recomputes any of it.
	if growth, err := s.Growth.GrowthFor(ctx, current.PatientID, facility); err != nil {
		if !errors.Is(err, clinical.ErrNotFound) {
			note("growth", err)
		}
	} else {
		raw.Growth = growth
	}

	if items, err := s.History.ForPatient(ctx, current.PatientID); err != nil {
		note("history", err)
	} else {
		raw.History = items
	}
	if state, err := s.Allergies.For(ctx, current.PatientID); err != nil {
		note("allergies", err)
	} else {
		raw.Allergies = state
	}

	if s.Lifestyle != nil {
		if scoring := s.Lifestyle.Standing(ctx, current.PatientID, facility); scoring != nil {
			raw.Lifestyle = scoring
		}
	}
	if s.Nutrition != nil {
		// The recall for the clinic day, not the newest recall on record. A summary that quoted a
		// recall from six months ago as though it were today's would be wrong in a way nobody
		// reading the page could see.
		if recall, err := s.Nutrition.Recall(ctx, current.PatientID, facility, current.ClinicDay); err != nil {
			note("nutrition", err)
		} else if recall.Totals.Entries > 0 {
			raw.Nutrition = &recall
		}
	}
	if s.Exercise != nil {
		if assessed, err := s.Exercise.LiveAssessment(ctx, current.PatientID, facility); err == nil {
			raw.Exercise = &assessed
		}
		if plan, err := s.Exercise.LivePlan(ctx, current.PatientID, facility); err == nil {
			raw.Plan = &plan
		}
	}

	// Earlier visits, and only closed ones: an open visit is this one, or one somebody forgot to
	// close, and neither carries §11.1's summary. The read asks for more than it keeps because the
	// filtering happens here rather than in SQL — `visit` owns that query and a second opinion
	// about "which visits count" living in this module is how two modules end up disagreeing.
	if visits, err := s.Visits.ForPatient(ctx, current.PatientID, facility, PriorVisitDepth*4); err != nil {
		note("prior_visits", err)
	} else {
		for _, prior := range visits {
			if prior.ID == current.ID || prior.Status != visit.Closed {
				continue
			}
			raw.PriorVisits = append(raw.PriorVisits, prior)
			if len(raw.PriorVisits) == PriorVisitDepth {
				break
			}
		}
	}

	return raw, nil
}

// alertDepth is how many critical values the summary carries. Twenty: more than any one visit
// raises, few enough that a patient with a long history does not fill the payload with alerts from
// three years ago.
const alertDepth = 20

// ageText is the age in words a clinician would use.
//
// Months under two years, years after: "eighteen months" and "forty-one years" are how the two ends
// of this clinic's caseload are described, and a model given only a month count writes "the
// 492-month-old patient".
func ageText(months int) string {
	switch {
	case months < 0:
		return ""
	case months < 24:
		return plural(months, "month")
	default:
		return plural(months/12, "year")
	}
}
