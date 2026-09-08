package ai

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// The grounding check (CP72, §10.2 step 4, §10.6 permanent invariant 5).
//
// # What this is checking, and against what
//
// *"Before any AI summary reaches the physician, every number in it is matched against the
// structured record."* The record it is matched against is **not the database**. It is the payload
// the model was actually shown, which the gateway holds in its hand at the moment of the check and
// stores on `core.ai_interaction.outbound` afterwards.
//
// ADR-0033 argues that at length and it is the decision the whole file rests on, so the short
// version: a summary written at 10:04 and checked at 10:06 against the live record, after a
// correction landed at 10:05, would be reported as a hallucination that never happened. Grounding
// is a claim about what the model was shown. Re-deriving the record to answer it would also be a
// second implementation of the assembler, and the two would disagree the first time either moved.
//
// # Four arms, and each is a different kind of claim
//
// **Citation.** The prompt requires every clinical claim to carry a fact reference, and the fact
// index is a flat list of what the assembler put in front of the model. So a reference is checked
// by set membership: it is in the index or it is invented. This arm has no false positives by
// construction, which is why the prompt was designed to make it possible (ADR-0033).
//
// **Number.** Every numeric token the model wrote into prose must be a numeric token that appears
// somewhere in the payload — the same string, normalised the same way. Not "within a tolerance":
// [synthesis.Fact.Value] is formatted once, as a string, precisely so that this comparison is
// exact rather than a tolerance somebody has to defend.
//
// **Date.** Every date, written as `2026-03-12` or as `12 March 2026`, must be a date the payload
// carries. §10.2's own example — *"HbA1c 8.2 on 12 March"* — is a number and a date, and a check
// that only did the number would pass a summary that moved a result by three months.
//
// **Drug.** Every drug name must be a drug this clinic can name. **This arm has nothing to check
// against today**: there is no formulary until CP75, and rather than stub a drug list — which
// would certify as verified whatever list somebody typed — the arm reports every drug name as
// unverifiable and the answer is blocked. See [DrugCheck].
//
// # What is deliberately not checked, and what that costs
//
// **Numbers that are JSON values rather than prose.** `"confidence": 0.8` is a field of the
// answer whose meaning is fixed by the agent's schema, not a quotation from the record; a model
// cannot make a clinical claim in it that the schema did not already invite. Checking it would
// flag every confidence score in the system, which is where this rule came from — nineteen of the
// twenty-four findings in the first run of this check over CP71's corpus were the confidence
// field. The cost is stated rather than hidden: an agent whose output schema grows a numeric
// *clinical* field would have that field unchecked, and the answer is that agents quote the
// record in prose and cite it, which is what the citation arm is for.
//
// **A bare month name.** "since March", with no day and no year, is not checked. The month names
// collide with ordinary words — "May" at the start of a sentence is the one that bites — and a
// claim that vague is not one the citation arm would let stand anyway. A month with a day or a
// year attached is checked.
//
// **Counts.** "a further 7 measurements", "all 4 stations before the consultation". Counting the
// things you were shown is not inventing, and a check that flagged it would have blocked a
// quarter of CP71's twenty summaries — measured, not guessed. The rule is narrow and derived from
// the payload rather than from a list somebody maintains: a whole number followed, within four
// words, by the **name of a collection the payload actually contains** is a count. See [isCount]
// for why the four-word window is bounded the way it is, and for the false negative it admits.
//
// # Where the block lives
//
// Here, in the sense that [Gateway.Invoke] refuses to return the output. But not only here:
// migration 00054 writes the same rule as two check constraints and an invariant, because a check
// that lives in one place is a check that a future code path can be written around. See the
// migration header.

// GroundingArm names which check fired. Four, because they are fixed by different people: a
// citation failure is a model ignoring an instruction, a number or a date is a model inventing,
// and a drug is a name nothing in this system can verify.
type GroundingArm string

const (
	ArmCitation GroundingArm = "CITATION"
	ArmNumber   GroundingArm = "NUMBER"
	ArmDate     GroundingArm = "DATE"
	ArmDrug     GroundingArm = "DRUG"
)

// GroundingState is the verdict, and the same four strings the database stores.
type GroundingState string

const (
	// GroundingNotChecked is an answer that never reached the check: the call failed, or the row
	// predates CP72. Its own state rather than a blank, because "we did not look" and "we looked
	// and it was fine" must never be the same value in a column somebody counts.
	GroundingNotChecked GroundingState = "NOT_CHECKED"
	// GroundingNotRequired is an agent `core.ai_agent` exempts, with a written reason.
	GroundingNotRequired GroundingState = "NOT_REQUIRED"
	GroundingPassed      GroundingState = "PASSED"
	GroundingFailed      GroundingState = "FAILED"
)

// GroundingFinding is one thing the model said that the payload does not support.
type GroundingFinding struct {
	Arm GroundingArm `json:"arm"`
	// Path is where in the answer, in the agent's own vocabulary: `narrative_en`,
	// `red_flags[0].statement`. A reviewer reads this first.
	Path string `json:"path"`
	// Token is the offending text itself: the number, the date, the reference, the drug name.
	Token string `json:"token"`
	// Excerpt is the sentence around it, bounded and checked against the PHI pattern list before
	// it is stored. Empty when the surrounding text could not be shown safely.
	Excerpt string `json:"excerpt,omitempty"`
	Reason  string `json:"reason"`
}

// GroundingReport is the verdict plus what it took to reach it.
//
// The counts are not decoration. "Zero violations" from a check that examined nothing is the
// failure mode a grounding check is most likely to have — a payload whose fact index did not
// decode, an output whose prose field was renamed — and it is indistinguishable from success
// unless the report says how much it looked at. The evaluation harness asserts on these.
type GroundingReport struct {
	State    GroundingState     `json:"state"`
	Findings []GroundingFinding `json:"findings"`

	Citations int `json:"citations_checked"`
	Numbers   int `json:"numbers_checked"`
	Dates     int `json:"dates_checked"`
	Drugs     int `json:"drugs_checked"`

	// DrugArm is ARMED or UNARMED. Reported on every verdict so that a dashboard cannot show a
	// clean grounding rate while the drug arm has been unarmed for six months and nobody noticed.
	DrugArm string `json:"drug_arm"`
}

// OK reports whether the answer may be shown.
func (r GroundingReport) OK() bool {
	return r.State == GroundingPassed || r.State == GroundingNotRequired
}

// Summary is the one line a log or an error carries.
func (r GroundingReport) Summary() string {
	if len(r.Findings) == 0 {
		return "no ungrounded claims"
	}
	byArm := map[GroundingArm]int{}
	for _, f := range r.Findings {
		byArm[f.Arm]++
	}
	arms := make([]string, 0, len(byArm))
	for _, arm := range []GroundingArm{ArmCitation, ArmNumber, ArmDate, ArmDrug} {
		if byArm[arm] > 0 {
			arms = append(arms, fmt.Sprintf("%d %s", byArm[arm], strings.ToLower(string(arm))))
		}
	}
	first := r.Findings[0]
	return fmt.Sprintf("%s (first: %s at %s — %s)",
		strings.Join(arms, ", "), strconv.Quote(first.Token), first.Path, first.Reason)
}

// DrugCheck answers whether a name is a medicine this clinic has a record of.
//
// # This is a seam, and today nothing is plugged into it
//
// §10.4's A1 requires that *"drug names [are] validated against the formulary"* and that *"an
// unrecognised drug name is dropped, not displayed"*. There is no formulary: it is CP75, and
// `architecture.json` does not let this module import one anyway — so the composition root will
// wire it in the way it wires [AlertRaiser], when there is something to wire.
//
// A nil DrugCheck is **unarmed**, and unarmed does not mean silent. Every drug name the model
// wrote becomes a finding, and the answer is blocked. That is the fail-closed reading of A1's own
// rule: with no formulary, every drug name is unrecognised, and an unrecognised drug name may not
// be displayed.
//
// The cost is real and worth stating plainly rather than discovering: **with a real model and no
// formulary, any summary that drafts a medication is withheld in full.** CP71's deterministic
// composer names no drug, so nothing is blocked today; a Gemini answer to the same prompt very
// probably would be. Two ways out, and both are decisions this checkpoint does not get to take:
// CP75 supplies the formulary, or the agent's prompt stops inviting `draft_medications` until it
// does. Blocking the whole answer rather than quietly removing the item is deliberate — a
// redaction the physician cannot see is a second thing to trust, and the missing formulary should
// be visible rather than tolerable.
type DrugCheck func(name string) bool

// DrugKeys are the output field names that carry a drug name.
//
// A key list rather than a path list, for the reason `logging.PHIKeys` is one: an agent added in
// two years will name its field `drug` or `medication` because every other agent does, and a rule
// keyed on `draft_medications[].drug` would silently stop applying to it. The cost is that a field
// called something else is missed, which is what `TestTheSynthesisSchemaStillNamesADrugKey` is
// for — it fails if CP71's schema renames the field out from under this list.
var DrugKeys = map[string]bool{
	"drug":         true,
	"drug_name":    true,
	"medication":   true,
	"medicine":     true,
	"generic_name": true,
	"brand_name":   true,
}

// --- the index ---

// Grounds is everything the model was shown, in the shapes the check needs.
//
// Built from the payload rather than from a [synthesis.Context]: this package may not import that
// one, and more importantly it should not — the same check has to serve CP107's chronology
// summariser and CP133's scribe, and an index that knew one agent's Go types would serve one
// agent.
type Grounds struct {
	refs  map[string]bool
	kinds map[string]bool

	numbers map[string]bool
	dates   map[ymd]bool

	// nouns are the names of the collections the payload contains, derived from its own keys.
	// See [isCount].
	nouns map[string]bool
}

type ymd struct{ year, month, day int }

// GroundsFrom indexes a payload.
//
// Everything is taken from the payload as it stands, including the parts the assembler did not
// call facts: a number in a station name or a prior visit's plan is still a number the model was
// shown, and refusing it would be refusing a quotation. The *fact index* is used for one thing
// only — deciding whether a citation names something real — because that is the one place the
// assembler made an explicit promise about what may be cited.
func GroundsFrom(payload map[string]any) Grounds {
	g := Grounds{
		refs: map[string]bool{}, kinds: map[string]bool{},
		numbers: map[string]bool{}, dates: map[ymd]bool{}, nouns: map[string]bool{},
	}
	g.index(payload, "")

	// The fact index, when the payload has one. An agent without a `facts` array still gets the
	// number and date arms; what it loses is the citation arm, and it loses it visibly — the
	// report's `citations_checked` is zero and the harness reports that as a case with nothing
	// checked rather than as a clean pass.
	for _, fact := range asSlice(payload["facts"]) {
		object, ok := fact.(map[string]any)
		if !ok {
			continue
		}
		ref, _ := object["ref"].(string)
		if ref == "" {
			continue
		}
		g.refs[ref] = true
		if kind, _, found := strings.Cut(ref, "."); found && kind != "" {
			g.kinds[kind] = true
		}
	}
	return g
}

func (g Grounds) index(value any, key string) {
	switch typed := value.(type) {
	case map[string]any:
		for name, child := range typed {
			g.index(child, name)
		}
	case []any:
		if key != "" {
			// `current_measurements` contributes "measurements" and "current_measurements";
			// `prior_visits` contributes "visits" and "prior_visits". Both spellings, because a
			// model writes whichever reads better in the sentence.
			g.noun(key)
			if at := strings.LastIndex(key, "_"); at >= 0 {
				g.noun(key[at+1:])
			}
		}
		for _, child := range typed {
			g.index(child, key)
		}
	case string:
		for _, match := range isoDate.FindAllString(typed, -1) {
			if parsed, ok := parseISO(match); ok {
				g.dates[parsed] = true
			}
		}
		for _, match := range numberToken.FindAllString(isoDate.ReplaceAllString(typed, " "), -1) {
			g.numbers[normaliseNumber(match)] = true
		}
	case json.Number:
		g.numbers[normaliseNumber(typed.String())] = true
	case float64:
		g.numbers[normaliseNumber(strconv.FormatFloat(typed, 'f', -1, 64))] = true
	case bool, nil:
		// Neither is a claim about a measurement.
	}
}

func (g Grounds) noun(word string) {
	word = strings.ToLower(strings.TrimSpace(word))
	if word == "" {
		return
	}
	g.nouns[word] = true
	g.nouns[strings.TrimSuffix(word, "s")] = true
}

// Facts is how many citable references the payload declared. Zero means the agent has no fact
// index and the citation arm had nothing to check against — which callers should be able to see.
func (g Grounds) Facts() int { return len(g.refs) }

// --- the check ---

// Check runs all four arms over one model answer.
//
// Pure: no clock, no database, no network. The answer is the object the model produced **before**
// the gateway restores the subject's identifiers, so nothing here or in anything it produces can
// carry a patient's name — which is what lets a finding's excerpt be stored.
func (g Grounds) Check(output map[string]any, drugs DrugCheck) GroundingReport {
	report := GroundingReport{State: GroundingPassed, DrugArm: "UNARMED"}
	if drugs != nil {
		report.DrugArm = "ARMED"
	}
	g.walk(output, "", drugs, &report)

	// Findings come out in walk order, which is map order, which is random. Sorted so that two
	// runs over the same answer produce the same defect rows and the same CI output — an
	// evaluation harness whose diff moves on its own is one nobody reads.
	sort.SliceStable(report.Findings, func(i, j int) bool {
		if report.Findings[i].Path != report.Findings[j].Path {
			return report.Findings[i].Path < report.Findings[j].Path
		}
		if report.Findings[i].Arm != report.Findings[j].Arm {
			return report.Findings[i].Arm < report.Findings[j].Arm
		}
		return report.Findings[i].Token < report.Findings[j].Token
	})
	if len(report.Findings) > 0 {
		report.State = GroundingFailed
	}
	return report
}

func (g Grounds) walk(value any, path string, drugs DrugCheck, report *GroundingReport) {
	switch typed := value.(type) {
	case map[string]any:
		names := make([]string, 0, len(typed))
		for name := range typed {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			child := typed[name]
			if DrugKeys[strings.ToLower(name)] {
				if text, ok := child.(string); ok && strings.TrimSpace(text) != "" {
					g.checkDrug(text, join(path, name), drugs, report)
					continue
				}
			}
			g.walk(child, join(path, name), drugs, report)
		}
	case []any:
		for i, child := range typed {
			g.walk(child, fmt.Sprintf("%s[%d]", path, i), drugs, report)
		}
	case string:
		g.checkString(typed, path, report)
	}
	// Numbers, booleans and nulls are schema-validated fields of the answer rather than
	// quotations. See the package comment for why, and for what that costs.
}

func (g Grounds) checkDrug(name, path string, drugs DrugCheck, report *GroundingReport) {
	report.Drugs++
	trimmed := strings.TrimSpace(name)
	if drugs == nil {
		report.Findings = append(report.Findings, GroundingFinding{
			Arm: ArmDrug, Path: path, Token: truncateToken(trimmed),
			Excerpt: "",
			Reason: "there is no formulary to verify a drug name against (CP75), so no drug name " +
				"can be shown; §10.4 A1 requires an unrecognised drug name not to be displayed",
		})
		return
	}
	if !drugs(trimmed) {
		report.Findings = append(report.Findings, GroundingFinding{
			Arm: ArmDrug, Path: path, Token: truncateToken(trimmed),
			Reason: "the formulary has no medicine by that name",
		})
	}
}

func (g Grounds) checkString(text, path string, report *GroundingReport) {
	trimmed := strings.TrimSpace(text)

	// A string that is *entirely* a reference is a citation — this is how `citations[]` and every
	// `basis[]` are checked, without this package needing to know either field's name. The kind
	// test keeps ordinary prose out: "i.e" parses as a reference by grammar alone, and would be a
	// finding in every summary that used it.
	if looksLikeRef(trimmed) && (hasRefDate(trimmed) || g.kinds[refKind(trimmed)]) {
		report.Citations++
		if !g.refs[trimmed] {
			report.Findings = append(report.Findings, GroundingFinding{
				Arm: ArmCitation, Path: path, Token: truncateToken(trimmed),
				Reason: "the answer cites a fact reference that is not in the context it was given",
			})
		}
		return
	}

	// Prose. Bracketed citations are checked and then blanked out, so that the date inside
	// `[obs.hba1c:2026-03-12]` is not scanned twice — but **only** when the bracket really held a
	// reference. `[HbA1c 12.9]` is not a citation, and blanking it would be a hole in the number
	// arm shaped exactly like a square bracket.
	remaining := []byte(text)
	for _, span := range bracketed.FindAllStringSubmatchIndex(text, -1) {
		inner := text[span[2]:span[3]]
		if !looksLikeRef(inner) {
			continue
		}
		report.Citations++
		if !g.refs[inner] {
			report.Findings = append(report.Findings, GroundingFinding{
				Arm: ArmCitation, Path: path, Token: truncateToken(inner),
				Excerpt: g.excerpt(text, span[0], span[1]),
				Reason:  "the answer cites a fact reference that is not in the context it was given",
			})
		}
		blank(remaining, span[0], span[1])
	}

	scan := string(remaining)
	for _, span := range isoDate.FindAllStringIndex(scan, -1) {
		report.Dates++
		token := scan[span[0]:span[1]]
		parsed, ok := parseISO(token)
		if !ok || !g.dates[parsed] {
			report.Findings = append(report.Findings, GroundingFinding{
				Arm: ArmDate, Path: path, Token: token,
				Excerpt: g.excerpt(text, span[0], span[1]),
				Reason:  "no fact in the context carries that date",
			})
		}
		blank(remaining, span[0], span[1])
	}

	scan = string(remaining)
	for _, span := range proseDateSpans(scan) {
		report.Dates++
		token := strings.TrimSpace(scan[span.start:span.end])
		if !g.hasDate(span.value) {
			report.Findings = append(report.Findings, GroundingFinding{
				Arm: ArmDate, Path: path, Token: truncateToken(token),
				Excerpt: g.excerpt(text, span.start, span.end),
				Reason:  "no fact in the context falls on that date",
			})
		}
		blank(remaining, span.start, span.end)
	}

	scan = string(remaining)
	for _, span := range numberToken.FindAllStringIndex(scan, -1) {
		token := scan[span[0]:span[1]]
		report.Numbers++
		if g.numbers[normaliseNumber(token)] {
			continue
		}
		if isCount(token, scan[span[1]:], g.nouns) {
			continue
		}
		report.Findings = append(report.Findings, GroundingFinding{
			Arm: ArmNumber, Path: path, Token: token,
			Excerpt: g.excerpt(text, span[0], span[1]),
			Reason:  "no fact in the context carries that value",
		})
	}
}

// hasDate resolves a prose date against the payload's dates.
//
// A partial date resolves against any context date that agrees on the parts it gave. "12 March"
// resolves if something in the context happened on the twelfth of March in any year the context
// covers; "March 2026" resolves if anything at all is dated inside that month. That is looser than
// the ISO arm on purpose: a model writing a partial date is being vague rather than specific, and
// treating vagueness as fabrication would flag "reviewed in June" for a June the record contains.
func (g Grounds) hasDate(want ymd) bool {
	for have := range g.dates {
		if want.year != 0 && want.year != have.year {
			continue
		}
		if want.month != 0 && want.month != have.month {
			continue
		}
		if want.day != 0 && want.day != have.day {
			continue
		}
		return true
	}
	return false
}

// excerpt is the sentence around a finding, bounded and safe to store.
//
// Bounded because `core.ai_grounding_defect.excerpt` is capped at four hundred characters, and
// because a defect carrying a whole narrative is a defect table that becomes a second copy of the
// clinical record. Safe because the same pattern list that guards the outbound payload is run over
// it first: this text comes from the *pre-restore* answer and therefore names a pseudonym rather
// than a person, and this check is what notices the day that stops being true.
func (g Grounds) excerpt(text string, from, to int) string {
	const window = 70
	start, end := from-window, to+window
	if start < 0 {
		start = 0
	}
	if end > len(text) {
		end = len(text)
	}
	for start > 0 && !utf8Boundary(text, start) {
		start--
	}
	for end < len(text) && !utf8Boundary(text, end) {
		end++
	}
	out := strings.TrimSpace(text[start:end])
	if start > 0 {
		out = "… " + out
	}
	if end < len(text) {
		out += " …"
	}
	if len(out) > 380 {
		out = strings.TrimSpace(out[:380]) + " …"
	}
	for _, pattern := range excerptPatterns {
		if pattern.Matches(out) {
			// Nothing rather than a scrubbed version. A scrubbed excerpt reads as prose the model
			// wrote and is not, and the finding already carries the token, the path and the
			// interaction id — which is enough to open the answer itself and read the sentence in
			// the place it belongs. The empty string is also what the column defaults to, so a
			// defect with no excerpt is not a special case anywhere downstream.
			return ""
		}
	}
	return out
}

// excerptPatterns is the shared list, compiled once. The same list the gateway minimises with and
// the same list `ops.pii_pattern` holds — a fifth copy would be a fifth thing to drift.
var excerptPatterns = func() []Pattern {
	compiled, err := CompilePatterns(DefaultPatterns)
	if err != nil {
		// The same position `internal/synthesis` takes: a package that cannot compile the
		// scrubber's rules must not start with a scrubber that silently dropped one.
		panic("ai: the PHI pattern list does not compile: " + err.Error())
	}
	return compiled
}()

// --- the small deterministic pieces ---

var (
	// A number as a model writes one: thousands separators allowed, a decimal part allowed, and
	// no sign — "-2.6" and "2.6" are the same claim about the same measurement, and the payload
	// stores the delta with its sign while the prose sometimes drops it.
	numberToken = regexp.MustCompile(`[0-9]+(?:,[0-9]{3})*(?:\.[0-9]+)?`)
	isoDate     = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}`)
	// The prompt asks for `[obs.hba1c:2026-03-12]` after the claim it supports. The inner group is
	// what gets tested for reference shape.
	bracketed = regexp.MustCompile(`\[([^\[\]\n]{1,120})\]`)
	// The reference grammar `internal/synthesis`'s `ref` builds: `kind.slug`, optionally `:date`,
	// optionally a letter suffix for a same-day duplicate. Lower case throughout, because that is
	// what `slug` produces and a model that shouted one has not cited the thing it was shown.
	refShape = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:\.[a-z0-9_]+)*(?::[0-9]{4}-[0-9]{2}-[0-9]{2}(?:\.[a-z]+)?)?$`)
)

func looksLikeRef(s string) bool {
	if len(s) < 3 || len(s) > 120 || !strings.Contains(s, ".") {
		return false
	}
	return refShape.MatchString(s)
}

func hasRefDate(s string) bool { return strings.Contains(s, ":") }

func refKind(s string) string {
	kind, _, _ := strings.Cut(s, ".")
	return kind
}

// normaliseNumber makes "8.20", "8.2" and "08.2" one string.
//
// The same normalisation on both sides, which is the only thing that matters: the payload's
// numbers went through it when the index was built, and a token from the answer goes through it
// here. Trailing zeros are trimmed because `synthesis.number` trims them, and thousands separators
// are dropped because a model writes 1,200 where the record says 1200.
func normaliseNumber(s string) string {
	s = strings.ReplaceAll(s, ",", "")
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	s = strings.TrimLeft(s, "0")
	if s == "" || strings.HasPrefix(s, ".") {
		s = "0" + s
	}
	return s
}

// isCount decides whether a whole number is the model counting something it was shown.
//
// The rule: the token is an integer, and within the next four whitespace-separated words there is
// the **name of a collection the payload contains** — `measurements`, `stations`, `gaps`, `facts`
// — with nothing but words and numbers in between. Four words because "3 of 5 pre-consultation
// stations" needs four; the run stops at the first token carrying punctuation, which is what keeps
// "Pulse 88 /min; 5 measurements" from treating 88 as a count of measurements.
//
// The false negative it admits, stated rather than discovered: a model that wrote "HbA1c 9
// measurements" would have its 9 accepted. That sentence does not occur in clinical prose, and the
// alternative — flagging every count — was measured against CP71's twenty summaries and blocked
// five of them.
func isCount(token, rest string, nouns map[string]bool) bool {
	if strings.ContainsAny(token, ".,") {
		return false
	}
	seen := 0
	for _, word := range strings.Fields(rest) {
		if seen >= 4 {
			return false
		}
		seen++
		lower := strings.ToLower(word)
		if nouns[lower] || nouns[strings.TrimSuffix(lower, "s")] {
			return true
		}
		if !continuable(word) {
			return false
		}
	}
	return false
}

// continuable says whether a word can stand between a number and the noun it counts. Letters,
// hyphens and digits only: any punctuation ends the phrase, and ending the phrase is what stops a
// measurement two clauses away from being read as a count.
func continuable(word string) bool {
	for _, r := range word {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' {
			return false
		}
	}
	return word != ""
}

type dateSpan struct {
	start, end int
	value      ymd
}

var (
	dayMonth   = regexp.MustCompile(`(?i)\b([0-9]{1,2})(?:st|nd|rd|th)?[ ]+([a-z]{3,9})\b(?:,?[ ]+([0-9]{4})\b)?`)
	monthDay   = regexp.MustCompile(`(?i)\b([a-z]{3,9})[ ]+([0-9]{1,2})(?:st|nd|rd|th)?\b(?:,?[ ]+([0-9]{4})\b)?`)
	monthYear  = regexp.MustCompile(`(?i)\b([a-z]{3,9})[ ]+([0-9]{4})\b`)
	monthNames = map[string]int{
		"jan": 1, "january": 1, "feb": 2, "february": 2, "mar": 3, "march": 3,
		"apr": 4, "april": 4, "may": 5, "jun": 6, "june": 6, "jul": 7, "july": 7,
		"aug": 8, "august": 8, "sep": 9, "sept": 9, "september": 9, "oct": 10, "october": 10,
		"nov": 11, "november": 11, "dec": 12, "december": 12,
	}
)

// proseDateSpans finds dates written the way a person writes them.
//
// Three forms, tried in the order that resolves the most text: "12 March 2026", "March 12, 2026",
// "March 2026". A bare month is deliberately not a form — see the package comment.
func proseDateSpans(text string) []dateSpan {
	var out []dateSpan
	taken := make([]bool, len(text)+1)
	consider := func(span []int, month string, day, year int) {
		mon, known := monthNames[strings.ToLower(month)]
		if !known {
			return
		}
		for i := span[0]; i < span[1]; i++ {
			if taken[i] {
				return
			}
		}
		for i := span[0]; i < span[1]; i++ {
			taken[i] = true
		}
		out = append(out, dateSpan{start: span[0], end: span[1], value: ymd{year: year, month: mon, day: day}})
	}
	for _, span := range dayMonth.FindAllStringSubmatchIndex(text, -1) {
		day := atoiAt(text, span, 2)
		if day < 1 || day > 31 {
			continue
		}
		consider(span, group(text, span, 4), day, atoiAt(text, span, 6))
	}
	for _, span := range monthDay.FindAllStringSubmatchIndex(text, -1) {
		day := atoiAt(text, span, 4)
		if day < 1 || day > 31 {
			continue
		}
		consider(span, group(text, span, 2), day, atoiAt(text, span, 6))
	}
	for _, span := range monthYear.FindAllStringSubmatchIndex(text, -1) {
		year := atoiAt(text, span, 4)
		if year < 1900 || year > 2200 {
			continue
		}
		consider(span, group(text, span, 2), 0, year)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

func group(text string, span []int, at int) string {
	if at+1 >= len(span) || span[at] < 0 {
		return ""
	}
	return text[span[at]:span[at+1]]
}

func atoiAt(text string, span []int, at int) int {
	value, err := strconv.Atoi(group(text, span, at))
	if err != nil {
		return 0
	}
	return value
}

func parseISO(s string) (ymd, bool) {
	if len(s) != 10 {
		return ymd{}, false
	}
	year, err1 := strconv.Atoi(s[0:4])
	month, err2 := strconv.Atoi(s[5:7])
	day, err3 := strconv.Atoi(s[8:10])
	if err1 != nil || err2 != nil || err3 != nil {
		return ymd{}, false
	}
	return ymd{year: year, month: month, day: day}, true
}

// blank replaces a span with spaces so that later arms do not read it again, while every remaining
// offset still points where it did. Rewriting the string instead would move every subsequent
// finding's excerpt off by the length of what was removed.
func blank(b []byte, from, to int) {
	for i := from; i < to && i < len(b); i++ {
		b[i] = ' '
	}
}

func utf8Boundary(s string, at int) bool {
	return at <= 0 || at >= len(s) || s[at]&0xC0 != 0x80
}

func truncateToken(s string) string {
	if len(s) <= 200 {
		return s
	}
	return strings.TrimSpace(s[:197]) + "..."
}

func asSlice(value any) []any {
	out, _ := value.([]any)
	return out
}
