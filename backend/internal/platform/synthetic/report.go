package synthetic

import (
	"fmt"
	"io"
	"sort"
)

// report writes the summary and remembers the first error rather than checking eleven of
// them at the call sites.
//
// The alternative was `_, _ = fmt.Fprintf(...)` on every line, which is not error handling —
// it is silencing the linter and calling it done. A summary written to a full disk or a
// closed pipe should say so once, at the end, to whoever asked for it.
type report struct {
	w   io.Writer
	err error
}

func (r *report) printf(format string, args ...any) {
	if r.err != nil {
		return
	}
	_, r.err = fmt.Fprintf(r.w, format, args...)
}

// row prints a generated share beside the profile's, flagging a gap worth a look.
func (r *report) row(label string, got, want float64) {
	flag := "  "
	if diff := got - want; diff > 0.015 || diff < -0.015 {
		flag = " *" // worth a look; not necessarily wrong
	}
	r.printf("%s%-28s %7.1f%%    %7.1f%%\n", flag, label, got*100, want*100)
}

// rowNoTarget prints a figure the profile does not state, so there is nothing to compare.
func (r *report) rowNoTarget(label string, got float64) {
	r.printf("  %-28s %7.1f%%          —\n", label, got*100)
}

func (r *report) section(title string) {
	r.printf("\n%s\n  %-28s %8s    %8s\n", title, "", "generated", "profile")
}

// WriteSummary prints the generated distributions beside the profile's stated ones.
//
// This is the check a clinician can actually run. The Go tests assert the same shares within
// a tolerance and fail on a number; this prints the whole table, so a reviewer can see that
// the generator is 0.4 points light on PCOS and decide whether that matters — a judgement
// no assertion threshold can make for them.
func WriteSummary(w io.Writer, p *Profile, people []Patient) error {
	out := &report{w: w}

	n := len(people)
	if n == 0 {
		out.printf("%s\n", "no patients generated")
		return out.err
	}

	out.printf("profile v%d, authored %s by %s\n", p.Version, p.AuthoredOn, p.AuthoredBy)
	out.printf("%d patients\n", n)

	out.section("PRESENTING PROBLEM")
	counts := map[string]int{}
	for _, q := range people {
		counts[q.ProblemKey]++
	}
	for _, key := range problemOrder {
		out.row(key, float64(counts[key])/float64(n), p.PresentingProblem[key])
	}

	out.section("DEMOGRAPHICS")
	var adults, adultFemale, pregnant, urban, paediatric int
	for _, q := range people {
		if q.AgeYears >= 18 {
			adults++
			if q.Sex == Female {
				adultFemale++
			}
		} else {
			paediatric++
		}
		if q.Pregnant {
			pregnant++
		}
		if q.Urban {
			urban++
		}
	}
	out.row("adult", float64(adults)/float64(n), p.Population.AdultShare)
	out.row("paediatric", float64(paediatric)/float64(n), p.Population.PaediatricShare)
	out.row("female (of adults)", float64(adultFemale)/float64(adults), p.Population.AdultFemaleShare)
	out.row("urban", float64(urban)/float64(n), p.Population.UrbanShare)
	out.rowNoTarget("pregnant", float64(pregnant)/float64(n))

	out.section("DIABETES")
	var withDM, onInsulin, withThyroid, both int
	types := map[DiabetesType]int{}
	var hba1c float64
	for _, q := range people {
		if q.Thyroid != nil {
			withThyroid++
		}
		if q.Diabetes == nil {
			continue
		}
		withDM++
		types[q.Diabetes.Type]++
		hba1c += q.Diabetes.BaselineHbA1c
		if q.Diabetes.OnInsulin {
			onInsulin++
		}
		if q.Thyroid != nil {
			both++
		}
	}
	if withDM == 0 {
		out.printf("%s\n", "  no patients with diabetes")
		return out.err
	}
	out.printf("  %d patients (%.1f%% of the cohort)\n", withDM, 100*float64(withDM)/float64(n))
	out.row("type 2", float64(types[Type2])/float64(withDM), p.Diabetes.Type2)
	out.row("type 1", float64(types[Type1])/float64(withDM), p.Diabetes.Type1)
	out.row("gestational", float64(types[Gestational])/float64(withDM), p.Diabetes.Gestational)
	out.row("other/secondary", float64(types[SecondaryDM])/float64(withDM), p.Diabetes.OtherSecondary)
	out.row("on insulin", float64(onInsulin)/float64(withDM), p.Prescribing.InsulinShare)
	out.printf("  %-28s %8.2f    %8.2f  (median)\n",
		"baseline HbA1c (mean)", hba1c/float64(withDM), p.Diabetes.HbA1cAtPresentation.Median)

	out.section("THYROID")
	out.printf("  %d patients (%.1f%% of the cohort)\n", withThyroid, 100*float64(withThyroid)/float64(n))
	cats := map[ThyroidCategory]int{}
	for _, q := range people {
		if q.Thyroid != nil {
			cats[q.Thyroid.Category]++
		}
	}
	keys := make([]string, 0, len(cats))
	for k := range cats {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	for _, k := range keys {
		out.rowNoTarget(k, float64(cats[ThyroidCategory(k)])/float64(withThyroid))
	}
	out.row("diabetes AND thyroid (of cohort)", float64(both)/float64(n),
		p.Comorbidity["diabetesAndThyroid"].Value)

	out.section("MISSINGNESS")
	var visits, notAttended, noHbA1c, noGlucose int
	for _, q := range people {
		for _, v := range q.Visits {
			visits++
			if !v.Attended {
				notAttended++
				continue
			}
			if q.Diabetes != nil {
				if v.HbA1c == nil {
					noHbA1c++
				}
				if v.FastingGlucose == nil {
					noGlucose++
				}
			}
		}
	}
	out.printf("  %d visits generated\n", visits)
	out.rowNoTarget("not attended", float64(notAttended)/float64(visits))
	out.rowNoTarget("attended, no HbA1c", float64(noHbA1c)/float64(visits))
	out.rowNoTarget("attended, no fasting glucose", float64(noGlucose)/float64(visits))

	out.section("NAMES")
	traditions := map[string]int{}
	for _, q := range people {
		traditions[q.Tradition]++
	}
	out.row("muslim", float64(traditions["muslim"])/float64(n), p.NamesAndLanguage.MuslimShare)
	out.row("hindu", float64(traditions["hindu"])/float64(n), p.NamesAndLanguage.HinduShare)
	out.row("other", float64(traditions["other"])/float64(n), p.NamesAndLanguage.OtherShare)

	return out.err
}
