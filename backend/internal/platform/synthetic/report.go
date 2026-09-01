package synthetic

import (
	"fmt"
	"io"
	"sort"
)

// WriteSummary prints the generated distributions beside the profile's stated ones.
//
// This is the check a clinician can actually run. The Go tests assert the same shares within
// a tolerance and fail on a number; this prints the whole table, so a reviewer can see that
// the generator is 0.4 points light on PCOS and decide whether that matters — a judgement
// no assertion threshold can make for them.
func WriteSummary(w io.Writer, p *Profile, people []Patient) {
	n := len(people)
	if n == 0 {
		fmt.Fprintln(w, "no patients generated")
		return
	}

	fmt.Fprintf(w, "profile v%d, authored %s by %s\n", p.Version, p.AuthoredOn, p.AuthoredBy)
	fmt.Fprintf(w, "%d patients\n", n)

	section(w, "PRESENTING PROBLEM")
	counts := map[string]int{}
	for _, q := range people {
		counts[q.ProblemKey]++
	}
	for _, key := range problemOrder {
		row(w, key, float64(counts[key])/float64(n), p.PresentingProblem[key])
	}

	section(w, "DEMOGRAPHICS")
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
	row(w, "adult", float64(adults)/float64(n), p.Population.AdultShare)
	row(w, "paediatric", float64(paediatric)/float64(n), p.Population.PaediatricShare)
	row(w, "female (of adults)", float64(adultFemale)/float64(adults), p.Population.AdultFemaleShare)
	row(w, "urban", float64(urban)/float64(n), p.Population.UrbanShare)
	rowNoTarget(w, "pregnant", float64(pregnant)/float64(n))

	section(w, "DIABETES")
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
		fmt.Fprintln(w, "  no patients with diabetes")
		return
	}
	fmt.Fprintf(w, "  %d patients (%.1f%% of the cohort)\n", withDM, 100*float64(withDM)/float64(n))
	row(w, "type 2", float64(types[Type2])/float64(withDM), p.Diabetes.Type2)
	row(w, "type 1", float64(types[Type1])/float64(withDM), p.Diabetes.Type1)
	row(w, "gestational", float64(types[Gestational])/float64(withDM), p.Diabetes.Gestational)
	row(w, "other/secondary", float64(types[SecondaryDM])/float64(withDM), p.Diabetes.OtherSecondary)
	row(w, "on insulin", float64(onInsulin)/float64(withDM), p.Prescribing.InsulinShare)
	fmt.Fprintf(w, "  %-28s %8.2f    %8.2f  (median)\n",
		"baseline HbA1c (mean)", hba1c/float64(withDM), p.Diabetes.HbA1cAtPresentation.Median)

	section(w, "THYROID")
	fmt.Fprintf(w, "  %d patients (%.1f%% of the cohort)\n", withThyroid, 100*float64(withThyroid)/float64(n))
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
		rowNoTarget(w, k, float64(cats[ThyroidCategory(k)])/float64(withThyroid))
	}
	row(w, "diabetes AND thyroid (of cohort)", float64(both)/float64(n),
		p.Comorbidity["diabetesAndThyroid"].Value)

	section(w, "MISSINGNESS")
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
	fmt.Fprintf(w, "  %d visits generated\n", visits)
	rowNoTarget(w, "not attended", float64(notAttended)/float64(visits))
	rowNoTarget(w, "attended, no HbA1c", float64(noHbA1c)/float64(visits))
	rowNoTarget(w, "attended, no fasting glucose", float64(noGlucose)/float64(visits))

	section(w, "NAMES")
	traditions := map[string]int{}
	for _, q := range people {
		traditions[q.Tradition]++
	}
	row(w, "muslim", float64(traditions["muslim"])/float64(n), p.NamesAndLanguage.MuslimShare)
	row(w, "hindu", float64(traditions["hindu"])/float64(n), p.NamesAndLanguage.HinduShare)
	row(w, "other", float64(traditions["other"])/float64(n), p.NamesAndLanguage.OtherShare)
}

func section(w io.Writer, title string) {
	fmt.Fprintf(w, "\n%s\n  %-28s %8s    %8s\n", title, "", "generated", "profile")
}

func row(w io.Writer, label string, got, want float64) {
	flag := "  "
	if diff := got - want; diff > 0.015 || diff < -0.015 {
		flag = " *" // worth a look; not necessarily wrong
	}
	fmt.Fprintf(w, "%s%-28s %7.1f%%    %7.1f%%\n", flag, label, got*100, want*100)
}

func rowNoTarget(w io.Writer, label string, got float64) {
	fmt.Fprintf(w, "  %-28s %7.1f%%          —\n", label, got*100)
}
