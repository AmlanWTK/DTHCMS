package synthetic

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"strings"
	"time"
)

//go:embed review.html.tmpl
var reviewTemplate embed.FS

// WriteReview renders a cohort as a page a clinician can read and mark up.
//
// This is CP13's manual verification, and it is the one check no test can perform. The Go
// tests prove the population matches the profile; only a doctor can say whether a patient
// who matches the profile still reads like someone who walked into his clinic. So the page
// is built for that judgement: every record is shown whole, missing values are printed as
// gaps rather than left blank, and the seed is on the masthead so a record he objects to can
// be regenerated and re-examined.
func WriteReview(w io.Writer, p *Profile, people []Patient, seed int64, asOf time.Time, command string) error {
	tmpl, err := template.New("review.html.tmpl").Funcs(template.FuncMap{
		"pct":       func(f float64) string { return fmt.Sprintf("%.0f%%", f*100) },
		"date":      func(t time.Time) string { return t.Format("2 Jan 2006") },
		"title":     titleCase,
		"problem":   problemLabel,
		"lab":       formatLab,
		"tsh":       formatTSH,
		"join":      strings.Join,
		"inc":       func(i int) int { return i + 1 },
		"mul":       func(a, b float64) float64 { return a * b },
		"hasSuffix": strings.HasSuffix,
	}).ParseFS(reviewTemplate, "review.html.tmpl")
	if err != nil {
		return fmt.Errorf("parsing the review template: %w", err)
	}

	return tmpl.Execute(w, reviewData{
		Profile:   p,
		Patients:  people,
		Seed:      seed,
		AsOf:      asOf,
		Generated: len(people),
		Command:   command,
	})
}

type reviewData struct {
	Profile   *Profile
	Patients  []Patient
	Seed      int64
	AsOf      time.Time
	Generated int
	// Command is the invocation that produced this page, printed in the footer. Reconstructed
	// from the flags rather than described, because a footer that tells a reader how to
	// regenerate a page and gets it wrong is worse than one that says nothing.
	Command string
}

// problemLabel writes the presenting problem the way it would be said out loud.
//
// Mechanically un-snaking the enum produced "Pcos" and "Bone calcium vitamin d" on the cards
// — which a reviewer reads as carelessness about everything else on the page.
var problemLabels = map[PresentingProblem]string{
	ProblemDiabetes:         "Diabetes",
	ProblemThyroid:          "Thyroid",
	ProblemObesity:          "Obesity & metabolic",
	ProblemPCOS:             "PCOS",
	ProblemGrowth:           "Growth & puberty",
	ProblemBone:             "Bone, calcium & vitamin D",
	ProblemAdrenal:          "Adrenal",
	ProblemPituitary:        "Pituitary",
	ProblemMaleReproductive: "Male sexual & reproductive",
}

func problemLabel(p PresentingProblem) string {
	if label, ok := problemLabels[p]; ok {
		return label
	}
	return titleCase(string(p))
}

// titleCase turns a snake_case enum into something a reader does not have to decode.
func titleCase(s string) string {
	s = strings.ReplaceAll(string(s), "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// formatLab prints a missing value as a visible gap.
//
// Blank would read as "normal" or "not applicable". The whole reason the generator omits
// laboratory values is that a system which has never met a missing one will not handle it,
// and a review page that hides them defeats the exercise.
func formatLab(v *float64, unit string) template.HTML {
	if v == nil {
		return template.HTML(`<span class="absent">not done</span>`)
	}
	return template.HTML(fmt.Sprintf(`%.1f<span class="unit">%s</span>`,
		*v, template.HTMLEscapeString(unit)))
}

// formatTSH honours the distinction the profile insisted on: undetectable is not zero.
func formatTSH(t *ThyroidProfile) template.HTML {
	if t == nil {
		return template.HTML(`<span class="absent">—</span>`)
	}
	if t.TSHUndetectable {
		return template.HTML(`<span class="flagged">undetectable</span>`)
	}
	if t.TSH == nil {
		return template.HTML(`<span class="absent">not done</span>`)
	}
	if *t.TSH > 100 {
		return template.HTML(fmt.Sprintf(`<span class="flagged">%.2f</span><span class="unit">mIU/L</span>`, *t.TSH))
	}
	return template.HTML(fmt.Sprintf(`%.2f<span class="unit">mIU/L</span>`, *t.TSH))
}
