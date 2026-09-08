package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/AmlanWTK/DTHCMS/backend/internal/ai"
)

// The deterministic composer that stands in for a language model.
//
// # Why this exists and what it is not
//
// `ai.Mock`'s own answer is built from the agent's output schema — `{"narrative_en": "string", ...}`
// — which is exactly right for a unit test asserting the pipeline validated something, and useless
// to a physician asked whether a summary is any good. So the mock is given a `Respond` that reads
// the payload it was handed and writes a summary from it by rule.
//
// **This is not a model and does not pretend to be one.** It cannot infer, it cannot weigh two
// findings against each other, and it will never notice something the assembler did not name. What
// it can do is exercise the whole pipeline for free and deterministically, and show a reviewer what
// the model was given beside a floor for what could be said with it. Everything it writes is
// traceable to a fact in the payload — which makes it a useful *lower bound*: a real model that
// says less than this is not earning its cost, and a real model that says something not in here is
// saying something a reviewer must check against the context beside it.
//
// It deliberately produces **no drafted medications**. The checkpoint's testing note asks for a
// check that the synthesis invents no drug names, validated against the formulary — and there is no
// formulary until CP75. Rather than stub one, this composer names no drug at all, which is the only
// honest thing a rule-based writer can do about prescribing.

// compose is the provider seam: prompt in, answer out, nothing else.
func compose(req ai.ProviderRequest) (ai.ProviderResponse, error) {
	payload, err := payloadFrom(req.User)
	if err != nil {
		return ai.ProviderResponse{}, fmt.Errorf("%w: %v", ai.ErrProviderRejected, err)
	}
	answer := write(payload)
	encoded, err := json.Marshal(answer)
	if err != nil {
		return ai.ProviderResponse{}, fmt.Errorf("%w: %v", ai.ErrProviderRejected, err)
	}
	return ai.ProviderResponse{
		Text: string(encoded), ModelVersion: ai.MockModelVersion,
		InputTokens:  (len(req.System) + len(req.User) + 7) / 4,
		OutputTokens: (len(encoded) + 3) / 4,
		FinishReason: "STOP",
	}, nil
}

// payloadFrom pulls the JSON object back out of the rendered user template.
//
// The template ends `RECORD:\n{{payload}}`, so the object is everything after the last marker. A
// real provider receives the same string and has to find the object the same way — this is what a
// prompt *is* — and doing it by hand here rather than by re-deriving the context keeps the composer
// honest: it can only write from what the model would actually see, scrubbing and all.
func payloadFrom(user string) (map[string]any, error) {
	const marker = "RECORD:\n"
	at := strings.LastIndex(user, marker)
	if at < 0 {
		return nil, fmt.Errorf("the rendered prompt has no RECORD marker")
	}
	// A decoder rather than Unmarshal, and it stops at the end of the first value. The gateway
	// appends a repair instruction to the prompt when it retries after a schema violation, so on a
	// second attempt there is prose *after* the object — and a strict Unmarshal turns a recoverable
	// retry into a provider rejection, which then opens the circuit breaker for every visit behind
	// it. That is exactly what happened the first time this ran against the cohort.
	var out map[string]any
	if err := json.NewDecoder(strings.NewReader(user[at+len(marker):])).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// headline is what a one-page summary narrates value by value.
//
// Everything else the assembler found is still in the context, still citable and still on the
// physician's screen; what it is not is a sentence. A narrative that listed thirty measurements
// would exceed the agent's own four-thousand-character limit — which is how this was found: the
// schema refused the answer, the gateway retried, and the retry cost a model call for nothing.
// §7.1 asks for one page, and the schema is where that is enforced.
const (
	// maxNarratedValues and maxNarratedTrends keep the narrative inside the agent's own
	// four-thousand-character schema limit, which is §7.1's "one page" made enforceable. Both were
	// found by the schema refusing an answer rather than chosen: the first run against the cohort
	// produced narratives of six thousand characters for the patients with the longest records, the
	// gateway retried each one, and six visits burned three model calls apiece to arrive at the
	// same refusal. That is the failure mode a max_length exists to catch, and it caught it.
	maxNarratedValues = 12
	maxNarratedTrends = 5
	// maxCitations is the schema's. The narrative is trimmed to fit rather than the citation list
	// being truncated on its own: a citation list that named references the text does not contain
	// would be a grounding check passing over sentences nobody wrote.
	maxCitations = 60
)

var headline = map[string]bool{
	"HBA1C": true, "GLUCOSE_FASTING": true, "GLUCOSE_RANDOM": true,
	"BODY_WEIGHT": true, "BMI": true, "WAIST_CIRC": true, "WHR": true,
	"BP_SYSTOLIC": true, "BP_DIASTOLIC": true, "HEART_RATE": true,
	"BODY_TEMP": true, "SPO2": true,
	"CREATININE": true, "EGFR": true, "CHOL_LDL": true, "CHOL_TOTAL": true,
	"TRIGLYCERIDE": true, "LIFESTYLE_RISK": true,
}

// answer is the agent's output shape, mirrored here rather than imported.
//
// A separate declaration on purpose: this is a *provider*, and a provider that shared the agent's
// types could not produce an answer the agent's schema rejects. Keeping them apart means the schema
// validator is doing real work on this path rather than checking a struct against itself.
type answer struct {
	NarrativeEN           string               `json:"narrative_en"`
	KeyPoints             []string             `json:"key_points"`
	SuggestedDiagnoses    []suggestedDiagnosis `json:"suggested_diagnoses"`
	MissingInvestigations []missingItem        `json:"missing_investigations"`
	DraftMedications      []any                `json:"draft_medications"`
	RedFlags              []redFlag            `json:"red_flags"`
	Confidence            float64              `json:"confidence"`
	Citations             []string             `json:"citations"`
}

type suggestedDiagnosis struct {
	Label  string   `json:"label"`
	Status string   `json:"status,omitempty"`
	Basis  []string `json:"basis"`
}

type missingItem struct {
	Investigation string   `json:"investigation"`
	Why           string   `json:"why"`
	Basis         []string `json:"basis,omitempty"`
}

type redFlag struct {
	Severity  string   `json:"severity"`
	Statement string   `json:"statement"`
	Basis     []string `json:"basis,omitempty"`
}

// write is the whole composer.
func write(payload map[string]any) answer {
	cited := map[string]bool{}
	cite := func(ref string) string {
		if ref == "" {
			return ""
		}
		cited[ref] = true
		return " [" + ref + "]"
	}

	var sentences []string
	var points []string

	subject, _ := payload["subject"].(map[string]any)
	demo, _ := payload["demographics"].(map[string]any)
	visitBlock, _ := payload["visit"].(map[string]any)

	age := str(demo, "age")
	sex := str(demo, "sex")
	if age == "" {
		age = "adult"
	}
	pseudonym := str(subject, "pseudonym")

	opening := fmt.Sprintf("The patient (%s) is %s, aged %s, attending a %s visit on %s.",
		pseudonym, orDefault(sex, "of unrecorded sex"), age,
		strings.ReplaceAll(str(visitBlock, "type"), "_", "-"), str(visitBlock, "clinic_day"))
	if complaint := str(visitBlock, "chief_complaint"); complaint != "" {
		opening += " The complaint recorded at the desk is: " + strings.TrimRight(complaint, ".") + "."
	}
	sentences = append(sentences, opening)

	// The journey. A station not reached is clinical information, and saying so is one of the few
	// things a rule can do better than a model: it never forgets to look.
	done, total, outstanding := stations(visitBlock)
	if total > 0 {
		if len(outstanding) == 0 {
			sentences = append(sentences, fmt.Sprintf(
				"All %d stations before the consultation are complete.", total))
		} else {
			sentences = append(sentences, fmt.Sprintf(
				"%d of %d pre-consultation stations are complete; %s %s not been done, so anything that station records is absent from this summary.",
				done, total, humanList(outstanding), plural(len(outstanding), "has", "have")))
			points = append(points, "Incomplete journey: "+humanList(outstanding)+" outstanding")
		}
	}

	// The numbers. Only the headline set and anything flagged; the rest are counted, because a
	// one-page summary that listed every value would not be one page.
	var measured []string
	others := 0
	for _, entry := range list(payload, "current_measurements") {
		label, value, unit := str(entry, "label"), str(entry, "value"), str(entry, "unit")
		if label == "" || value == "" {
			continue
		}
		flag := str(entry, "flag")
		if !headline[str(entry, "code")] && flag == "" {
			others++
			continue
		}
		text := label + " " + value
		if unit != "" {
			text += " " + unit
		}
		switch flag {
		case "critical":
			text += " (flagged critical by the clinic's own rule)"
		case "outside_reference_range":
			text += " (outside the reference range)"
		case "implausible_confirmed":
			text += " (outside its plausible band and confirmed as correct by the operator)"
		}
		if len(measured) >= maxNarratedValues {
			others++
			continue
		}
		measured = append(measured, text+strings.TrimSuffix(cite(str(entry, "ref")), " "))
		if flag != "" {
			points = append(points, text)
		}
	}
	switch {
	case len(measured) > 0 && others > 0:
		sentences = append(sentences, fmt.Sprintf(
			"Recorded: %s. A further %d measurement%s are on the record and in the context beside this.",
			strings.Join(measured, "; "), others, plural(others, " is", "s")))
	case len(measured) > 0:
		sentences = append(sentences, "Recorded: "+strings.Join(measured, "; ")+".")
	case others > 0:
		sentences = append(sentences, fmt.Sprintf(
			"%d measurements are on the record, none of them flagged and none in the headline set.", others))
	default:
		sentences = append(sentences, "No measurements have been recorded for this patient.")
	}

	// The trajectories, with the arithmetic taken from the payload rather than done here — the
	// same rule the prompt gives a model. Capped for the same reason the values are: this is a page
	// a consultant reads in ninety seconds, and the schema enforces it.
	narrated := 0
	for _, trend := range list(payload, "trends") {
		change, _ := trend["change"].(map[string]any)
		if change == nil || narrated >= maxNarratedTrends {
			continue
		}
		narrated++
		label := str(trend, "label")
		delta := str(change, "delta")
		direction := "unchanged"
		switch {
		case strings.HasPrefix(delta, "+"):
			direction = "risen"
		case strings.HasPrefix(delta, "-"):
			direction = "fallen"
		}
		unit := str(trend, "unit")
		if unit != "" {
			unit = " " + unit
		}
		// The interval comes off the payload's own `over_days`, not from a date subtraction here.
		// The first version of this read a `note` field that the assembler does not put on a change,
		// and every trend sentence ended "over  ." — a reminder that a composer reading a payload by
		// key is as capable of citing a field that does not exist as a model is.
		over := ""
		if days, ok := change["over_days"].(float64); ok && days > 0 {
			over = fmt.Sprintf(" over %d days", int(days))
		}
		sentences = append(sentences, fmt.Sprintf("%s has %s from %s to %s%s%s%s.",
			label, direction, str(change, "from"), str(change, "to"), unit, over,
			strings.TrimSuffix(cite(str(change, "ref")), " ")))
		if direction != "unchanged" {
			points = append(points, fmt.Sprintf("%s %s %s%s", label, direction, delta, unit))
		}
	}

	// Paediatric growth [R-06]. Reported, never recalculated.
	if growth, ok := payload["growth"].(map[string]any); ok {
		for _, indicator := range listIn(growth, "indicators") {
			sentences = append(sentences, fmt.Sprintf(
				"%s is %s%s, on centile %s (z %s) against %s%s.",
				str(indicator, "label"), str(indicator, "value"),
				spaced(str(indicator, "unit")), str(indicator, "percentile"), str(indicator, "z"),
				orDefault(str(growth, "standard"), "the reference"),
				strings.TrimSuffix(cite(str(indicator, "ref")), " ")))
		}
		if flag := str(growth, "obesity_flag"); flag != "" {
			readable := strings.ReplaceAll(strings.ReplaceAll(flag, "_", " "), "bmi for age", "BMI for age")
			points = append(points, "Paediatric flag: "+readable)
			sentences = append(sentences, "Paediatric flag: "+readable+".")
		}
		if note := str(growth, "note"); note != "" {
			sentences = append(sentences, "Growth percentiles were not computed: "+
				strings.ReplaceAll(note, "_", " ")+".")
		}
	}

	// Allergies. The status is the load-bearing part, and `unknown` is not `none`.
	if allergies, ok := payload["allergies"].(map[string]any); ok {
		switch status := str(allergies, "status"); status {
		case "NO_KNOWN_ALLERGY":
			sentences = append(sentences, "Allergy status: no known allergies, recorded by a named person.")
		case "unknown", "":
			sentences = append(sentences, "Allergy status has not been established for this patient. This is not the same as having none.")
			points = append(points, "Allergy status not established")
		default:
			var named []string
			for _, item := range listIn(allergies, "items") {
				named = append(named, str(item, "substance")+" ("+str(item, "reaction")+")"+
					strings.TrimSuffix(cite(str(item, "ref")), " "))
			}
			if len(named) > 0 {
				sentences = append(sentences, "Recorded allergies: "+strings.Join(named, "; ")+".")
				points = append(points, "Allergic to "+strings.Join(firstFields(named), ", "))
			}
		}
	}

	// History.
	var conditions, medications []string
	diagnoses := []suggestedDiagnosis{}
	for _, item := range list(payload, "history") {
		label := str(item, "label")
		if label == "" {
			continue
		}
		ref := str(item, "ref")
		switch strings.ToLower(str(item, "kind")) {
		case "medication":
			medications = append(medications, label+spaced(str(item, "dose"))+strings.TrimSuffix(cite(ref), " "))
		default:
			conditions = append(conditions, label+strings.TrimSuffix(cite(ref), " "))
			diagnoses = append(diagnoses, suggestedDiagnosis{
				Label: label, Status: "recorded", Basis: []string{ref},
			})
		}
	}
	switch {
	case len(conditions) > 0:
		sentences = append(sentences, "Recorded history: "+strings.Join(conditions, "; ")+".")
	default:
		sentences = append(sentences, "No medical history has been recorded for this patient at any visit.")
	}
	if len(medications) > 0 {
		sentences = append(sentences, "Current medication on record: "+strings.Join(medications, "; ")+".")
	}

	// Critical values, which are the clinic's own judgement rather than this composer's.
	flags := []redFlag{}
	for _, alert := range list(payload, "alerts") {
		// "high the critical threshold of 180" is what reading a database column into a sentence
		// looks like. The column says which side was breached; the sentence needs the preposition.
		side := "past"
		switch strings.ToLower(str(alert, "breached")) {
		case "high":
			side = "above"
		case "low":
			side = "below"
		}
		statement := fmt.Sprintf("%s %s %s, %s the critical threshold of %s; the alert is %s.",
			str(alert, "label"), str(alert, "value"), str(alert, "unit"),
			side, str(alert, "threshold"),
			strings.ToLower(orDefault(str(alert, "status"), "open")))
		flags = append(flags, redFlag{
			Severity: "urgent", Statement: statement, Basis: []string{str(alert, "ref")},
		})
		cite(str(alert, "ref"))
		points = append(points, "Critical value: "+str(alert, "label")+" "+str(alert, "value"))
	}

	// Prior visits.
	priors := list(payload, "prior_visits")
	if len(priors) > 0 {
		last := priors[0]
		sentence := "The last recorded visit was on " + str(last, "on")
		if dx := str(last, "diagnoses"); dx != "" {
			sentence += ", with " + strings.TrimRight(dx, ".")
		}
		if plan := str(last, "plan"); plan != "" {
			sentence += "; the plan then was " + strings.TrimRight(plan, ".")
		}
		if due := str(last, "review_due"); due != "" {
			sentence += ", with review due " + due
		}
		sentences = append(sentences, sentence+strings.TrimSuffix(cite(str(last, "ref")), " ")+".")
	}

	// The gaps, last, because that is where a consultant's eye should finish.
	missing := []missingItem{}
	var gapText []string
	for _, gap := range list(payload, "gaps") {
		detail := str(gap, "detail")
		gapText = append(gapText, detail)
		// Only the gaps that really are *investigations*. "No medical history has been recorded" is
		// a gap and is not something to order, and listing it under investigations produced the
		// line "history recorded — No medical history has been recorded", which reads as nonsense
		// and is exactly the sort of thing a template does that a reader notices immediately.
		if name, orderable := investigations[str(gap, "code")]; orderable {
			missing = append(missing, missingItem{Investigation: name, Why: detail})
		}
		if str(gap, "severity") == "important" {
			// The gap's own sentence, not a slug turned back into words. `no_history_recorded`
			// became "Missing: history recorded", which is a double negative dressed as a bullet —
			// the clinic already wrote the sentence it wants said, and saying it is better than
			// deriving a worse one from its key.
			points = append(points, trim(detail, 300))
		}
	}
	if len(gapText) > 0 {
		sentences = append(sentences, "What is missing: "+strings.Join(gapText, " "))
	}

	// A suggested line only where the clinic's own reference range has already been crossed and
	// nothing in the history accounts for it. It restates a flag; it does not diagnose.
	if len(conditions) == 0 {
		for _, entry := range list(payload, "current_measurements") {
			if str(entry, "code") == "HBA1C" && str(entry, "flag") == "outside_reference_range" {
				diagnoses = append(diagnoses, suggestedDiagnosis{
					Label:  "Hyperglycaemia: HbA1c above the laboratory reference range, with no condition recorded in the history",
					Status: "suggested", Basis: []string{str(entry, "ref")},
				})
			}
		}
	}

	if len(points) == 0 {
		points = []string{"Nothing in this record stands out; the summary is short because the record is thin."}
	}
	if len(points) > 8 {
		points = points[:8]
	}

	narrative := fit(sentences)

	// The citations are read back out of the finished narrative rather than accumulated as it was
	// written. The two differ whenever a sentence is dropped to fit, and the version that matters is
	// the one a reviewer can check: every reference in the list appears in the text above it.
	refs := citationsIn(narrative)
	for ref := range cited {
		if len(refs) >= maxCitations {
			break
		}
		if !containsRef(refs, ref) && strings.Contains(narrative, ref) {
			refs = append(refs, ref)
		}
	}
	sort.Strings(refs)
	if len(refs) > maxCitations {
		refs = refs[:maxCitations]
	}

	return answer{
		NarrativeEN:           narrative,
		KeyPoints:             points,
		SuggestedDiagnoses:    capDiagnoses(diagnoses),
		MissingInvestigations: capMissing(missing),
		// Empty, and deliberately: there is no formulary until CP75, and a rule-based writer that
		// named a drug would be inventing one. See the note at the top of this file.
		DraftMedications: []any{},
		RedFlags:         capFlags(flags),
		Confidence:       confidence(payload, len(gapText)),
		Citations:        refs,
	}
}

// fit joins the sentences and stops at the last whole one that fits the schema's limit.
//
// Truncating mid-sentence would be worse than dropping the sentence: a clinical summary that ends
// half way through a finding is a summary somebody reads as complete.
func fit(sentences []string) string {
	const budget = 3800
	out := ""
	for _, sentence := range sentences {
		candidate := sentence
		if out != "" {
			candidate = out + " " + sentence
		}
		if len(candidate) > budget {
			break
		}
		out = candidate
	}
	return out
}

// citationsIn reads the fact references back out of a finished narrative.
var citationPattern = regexp.MustCompile(`\[([a-z][a-z0-9_.:-]{1,78})\]`)

func citationsIn(narrative string) []string {
	// An empty slice rather than a nil one: a nil slice marshals to `null`, the agent's schema says
	// `citations` is an array, and the gateway refused the answer. Found by the validator, which is
	// what a validator is for — and a reminder that "the field is optional" and "the field may be
	// null" are different claims.
	out := []string{}
	for _, match := range citationPattern.FindAllStringSubmatch(narrative, -1) {
		if !containsRef(out, match[1]) {
			out = append(out, match[1])
		}
	}
	return out
}

func containsRef(refs []string, want string) bool {
	for _, ref := range refs {
		if ref == want {
			return true
		}
	}
	return false
}

// confidence is a crude, stated function of how much of the record was present.
//
// Crude on purpose: a number a rule invented should not look like a calibrated probability, and the
// page says what it is. A real model's own confidence is one of the things Dr. Nahid's review has to
// judge, and the honest placeholder is one whose formula is written down.
func confidence(payload map[string]any, gaps int) float64 {
	score := 0.4
	if len(list(payload, "current_measurements")) >= 3 {
		score += 0.2
	}
	if len(list(payload, "trends")) > 0 {
		score += 0.1
	}
	if len(list(payload, "history")) > 0 {
		score += 0.15
	}
	if visitBlock, ok := payload["visit"].(map[string]any); ok {
		if complete, _ := visitBlock["pre_consultation_complete"].(bool); complete {
			score += 0.15
		}
	}
	score -= float64(gaps) * 0.05
	switch {
	case score < 0.05:
		return 0.05
	case score > 0.95:
		return 0.95
	}
	return float64(int(score*100)) / 100
}

// --- small readers over the decoded payload ---

func str(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func list(payload map[string]any, key string) []map[string]any {
	return listIn(payload, key)
}

func listIn(m map[string]any, key string) []map[string]any {
	if m == nil {
		return nil
	}
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if object, ok := item.(map[string]any); ok {
			out = append(out, object)
		}
	}
	return out
}

func stations(visitBlock map[string]any) (done, total int, outstanding []string) {
	for _, station := range listIn(visitBlock, "stations") {
		code := str(station, "code")
		if code == "STN_CONSULTATION" {
			break
		}
		required, _ := station["required"].(bool)
		if !required {
			continue
		}
		total++
		if str(station, "status") == "done" {
			done++
			continue
		}
		outstanding = append(outstanding, readableStation(code))
	}
	return done, total, outstanding
}

func readableStation(code string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(code, "STN_"), "_", " "))
}

// investigations maps the gap codes that name something a physician can actually order to the words
// they would order it in. A gap not in this map is still reported in the narrative; it is simply not
// an investigation.
var investigations = map[string]string{
	"no_hba1c_in_12_months": "HbA1c",
	"no_blood_pressure":     "Blood pressure",
	"no_weight":             "Weight and BMI",
	"no_renal_function":     "Serum creatinine and eGFR",
}

func readableGap(code string) string {
	if name, known := investigations[code]; known {
		return name
	}
	return strings.ReplaceAll(strings.TrimPrefix(code, "no_"), "_", " ")
}

func humanList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// trim keeps a sentence inside the schema's per-item limit without cutting a word in half.
func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndex(s[:n-1], " ")
	if cut < n/2 {
		cut = n - 1
	}
	return s[:cut] + "…"
}

func orDefault(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func spaced(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return " " + s
}

func firstFields(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, strings.Fields(item)[0])
	}
	return out
}

func capDiagnoses(in []suggestedDiagnosis) []suggestedDiagnosis {
	if len(in) > 6 {
		return in[:6]
	}
	return in
}

func capMissing(in []missingItem) []missingItem {
	if len(in) > 8 {
		return in[:8]
	}
	return in
}

func capFlags(in []redFlag) []redFlag {
	if len(in) > 6 {
		return in[:6]
	}
	return in
}

var _ = strconv.Itoa
