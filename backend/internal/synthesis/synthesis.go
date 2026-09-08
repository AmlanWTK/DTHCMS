// Package synthesis is the pre-consultation synthesis agent (CP71, §7.1, §10.4 A1, R-05, R-06).
//
// # The one sentence this package exists to make true
//
// *The patient always arrives ready.* By the time the physician opens a file, there is a page of
// narrative and a draft plan on the screen, assembled while the patient was still in counselling.
// §7.1 gives it five minutes and promises the physician never has to ask for it.
//
// # Deterministic assembly is half the checkpoint, and it is the half that decides quality
//
// What reaches the model is built **by code, from stored facts**. The model is never asked to go
// and look at anything, and there is nothing here it could look with: it receives one JSON object
// and answers with another.
//
// That is not an efficiency. It is the property that makes CP72's grounding check possible at all:
// every number, date and name in the output has to resolve to something the assembler put in, and
// "what the assembler put in" is a value stored beside the answer rather than a query somebody
// re-runs months later against code that has moved on. It is also why [Assemble] is a **pure
// function** of [Raw] — no database, no clock beyond the one it is handed, no model — so that the
// half of this checkpoint that decides whether a summary is any good can be tested by asserting
// against a value.
//
// The split is:
//
//	Gather   database → Raw     (one read per station module; no logic)
//	Assemble Raw      → Context (all the logic; no I/O)
//	Invoke   Context  → Output  (the gateway, which is CP70's)
//
// # The fact index, and why the model is asked to cite
//
// [Context.Facts] is a flat list of everything citable the assembler found, each with a short
// stable reference. The prompt requires every clinical claim to carry the references it rests on.
// Three things come out of that, and only the first is obvious:
//
//   - a reviewer can check a sentence against a row rather than against their memory of the file;
//   - CP72's validator becomes a set membership test over `context.facts`, not a re-derivation;
//   - a model that cannot find a fact to cite tends to say less, which is the failure direction to
//     prefer in a clinical summary.
//
// The references are readable (`obs.hba1c:2026-03-12`) rather than opaque, because the person who
// will most often need to follow one is a physician reading the page, not a program.
//
// # What is deliberately not in the context
//
// **No identifiers.** Not the name, not the phone number, not the date of birth — the age in months
// and the sex are the two demographics D-08 permits, and they are all [Assemble] is given. This is
// structural rather than careful: [Raw] has no field that could carry a name, so the pure function
// that builds the payload could not include one if it were written to.
//
// **No operator names.** §4.2's attribution is a property of the record and belongs on the screen;
// it is not something a summary needs, and a payload naming the four members of staff who touched
// this patient would be sending their personal data abroad to no clinical purpose.
//
// **No free text that trips the shared PHI pattern list.** A clinician's note reading "husband will
// bring the report, call 01711-xxxxxx" is withheld whole rather than scrubbed into a shape the
// model will read around. See [withheld].
//
// # English only, and that is a decision awaiting confirmation
//
// The narrative is English. Clinical shorthand does not translate cleanly, and a bilingual summary
// doubles the surface a hallucination can hide behind — a wrong sentence in Bangla would have to be
// caught by a grounding check reading Bangla, and CP72 does not have one. Everything patient-facing
// in this system stays bilingual; this is a document one physician reads. Recorded in
// `docs/progress.md` as needing Dr. Nahid's confirmation, because it is his call and not mine.
package synthesis

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AgentCode names this agent's prompt in the gateway's registry.
//
// The same string as the job kind in `ops.job_kind` on purpose: an operator looking at a queue that
// is backing up and an operator looking at an AI budget that is climbing are looking at the same
// thing, and two names for it would be two things nobody joins.
const AgentCode = "clinical.synthesis"

// JobKind is the queue kind that runs this work. Registered in migration 00048 with §7.1's five
// minutes as its SLA budget, before there was anything to run.
const JobKind = "clinical.synthesis"

// AssemblerVersion is the version of the deterministic assembly.
//
// Bumped whenever [Assemble] would produce a different context from the same [Raw]. It is part of
// the material hash, so bumping it makes every visit's next look at its own summary conclude that
// the summary is out of date — which is the correct consequence of changing what the model is
// shown, and the reason this is a constant somebody has to edit rather than a build timestamp.
const AssemblerVersion = "1.0.0"

// State is where one run of the synthesis stands.
//
// The states exist because of D-15, which is *"fail visible, never fail silent, never fail
// invented"*. A physician opening a patient learns something different from each of these, and a
// screen that could only distinguish "there is a summary" from "there is not" would tell them
// nothing about whether the system had tried.
type State string

const (
	// NotRequested is the absence of a row. It is a state rather than a 404 because a 404 makes a
	// client render an empty panel, and an empty panel is the thing criterion 4 forbids.
	NotRequested State = "NOT_REQUESTED"
	// Pending — enqueued, nothing has run. The screen says the summary is being prepared and shows
	// the structured record underneath it.
	Pending State = "PENDING"
	// Running — a worker has it.
	Running State = "RUNNING"
	// Ready — there is a narrative.
	Ready State = "READY"
	// Failed — this run stopped. The screen says so, names what kind of failure it was, and shows
	// the structured record. The clinic does not stop because AI stopped.
	Failed State = "FAILED"
	// Unchanged — a re-run was asked for, the assembler produced a context materially identical to
	// the one behind the current summary, and no model was called. Its own state rather than a
	// silent no-op, because "we looked and there was nothing new" is a different fact from "nobody
	// looked", and only one of them should reassure anybody.
	Unchanged State = "UNCHANGED"
)

// Trigger is how a run came to exist. §7.1's own distinction: automatic is the normal path and the
// button is the fallback, so a clinic whose runs are mostly MANUAL has failed acceptance criterion
// 2 and this is how anybody would find out.
type Trigger string

const (
	// Automatic — every station before the consultation has finished with this patient.
	Automatic Trigger = "AUTOMATIC"
	// Manual — somebody pressed *Analyze / Summarize / Comprehensive Report*.
	Manual Trigger = "MANUAL"
	// Rerun — material new data arrived after a summary already existed.
	Rerun Trigger = "RERUN"
)

// FailureKind classifies a failure by what the physician should do about it, not by which Go error
// was returned. There are six because there are six different sentences worth showing.
type FailureKind string

const (
	// FailureAssembly — the context could not be built. Nothing was sent anywhere. This is our
	// defect, not the provider's, and it is the only one of the six that will not fix itself.
	FailureAssembly FailureKind = "ASSEMBLY"
	// FailureRefused — the gateway refused to send it: an identifier in the payload, or a
	// free-tier credential against a real patient. Both are configuration or assembly faults with
	// a named cause in the outbound log.
	FailureRefused FailureKind = "REFUSED"
	// FailureProvider — the model was unreachable, rate-limited, or the circuit was open.
	FailureProvider FailureKind = "PROVIDER"
	// FailureTimeout — the call did not finish inside the agent's budget.
	FailureTimeout FailureKind = "TIMEOUT"
	// FailureInvalidOutput — the model answered and the answer failed its schema on every attempt.
	// Worth its own class: it is the failure that means the prompt and the model have drifted
	// apart, and it is the one that gets worse rather than better if nobody looks.
	FailureInvalidOutput FailureKind = "INVALID_OUTPUT"
	// FailureInternal — us.
	FailureInternal FailureKind = "INTERNAL"
	// FailureUngrounded — the model answered, the answer satisfied its schema, and it said
	// something the record does not support (CP72, §10.2 step 4).
	//
	// Its own class rather than a kind of INVALID_OUTPUT, because the sentence the physician reads
	// is different from all six others: nothing is unavailable and nothing timed out. The system
	// has an answer and is **withholding** it, and a screen that said "the model could not be
	// reached" would send somebody to check a network that is fine.
	//
	// Not retried, and that is the whole of the "defects, not silently retried" rule as it reaches
	// this module — see [retryable].
	FailureUngrounded FailureKind = "UNGROUNDED"
)

// Fact is one addressable thing the assembler put in front of the model.
//
// `Value` is a string rather than a number, and that is deliberate: grounding is a comparison, and a
// comparison between the model's "8.2" and a float64 8.199999999999999 is a comparison somebody has
// to write a tolerance for. Formatting once, here, in one place, makes the check exact — and makes
// the number the model reads the same number the validator will look for.
type Fact struct {
	// Ref is short, stable and readable: `obs.hba1c:2026-03-12`. Readable because the person most
	// likely to follow one is a physician reading the page, not a program.
	Ref string `json:"ref"`
	// Kind groups facts for the prompt and for CP72: observation, growth, history, allergy, visit,
	// plan, alert, station.
	Kind string `json:"kind"`
	// Label is what a clinician calls it.
	Label string `json:"label"`
	// Value is the fact, formatted once. Empty for a fact that is purely qualitative, where Note
	// carries it.
	Value string `json:"value,omitempty"`
	Unit  string `json:"unit,omitempty"`
	// On is the date the fact is about, `2006-01-02`, or empty for a standing fact such as an
	// allergy. Never a timestamp: a summary that cited a measurement to the second would invite a
	// precision the record does not have.
	On string `json:"on,omitempty"`
	// Note is the qualitative part: an interpretation the system already computed, or the words a
	// patient used. Never an operator's name and never anything that trips the PHI pattern list.
	Note string `json:"note,omitempty"`
}

// Demographics is what the model may be told about who this is: D-08's two, and nothing else.
//
// Months rather than years because [R-06]'s percentiles are meaningless at a year's resolution, and
// because a date of birth is an identifier.
type Demographics struct {
	AgeMonths int    `json:"age_months"`
	AgeText   string `json:"age"`
	Sex       string `json:"sex"`
}

// VisitContext is the journey so far.
type VisitContext struct {
	Type string `json:"type"`
	// ClinicDay is the day, not the moment. §7.1 is about one morning.
	ClinicDay string `json:"clinic_day"`
	// Complaint is why the patient says they came, in their own words as the desk recorded them.
	Complaint string `json:"chief_complaint,omitempty"`
	// Stations is every planned station for this visit type with what has happened at it. The
	// model is shown the ones that have *not* happened as well, because "no examination was done"
	// is clinical information and a summary that quietly omitted the station would read as though
	// the examination had been normal.
	Stations []StationState `json:"stations"`
	// Complete is true when every required station before the consultation has finished. It is
	// the condition the automatic trigger fires on and it is reported to the model, because a
	// summary assembled from a half-finished visit should say so.
	Complete bool `json:"pre_consultation_complete"`
}

// StationState is one planned station and what happened at it.
type StationState struct {
	Code     string `json:"code"`
	Position int    `json:"position"`
	Required bool   `json:"required"`
	// Status is done, in_progress, bounced or not_reached.
	Status string `json:"status"`
	// Outcome is the station's own word for how the touch ended, when it has ended.
	Outcome string `json:"outcome,omitempty"`
}

// Measurement is one observation as the model sees it.
type Measurement struct {
	Ref   string `json:"ref"`
	Code  string `json:"code"`
	Label string `json:"label"`
	Value string `json:"value"`
	Unit  string `json:"unit,omitempty"`
	On    string `json:"on"`
	// Flag is the system's own interpretation where it has one: `critical`, `abnormal`,
	// `implausible_confirmed`. Deterministic, computed elsewhere in this system, and handed over
	// rather than left for the model to infer — a model deciding for itself that 8.2 is high is a
	// model one prompt change away from deciding that 6.2 is.
	Flag string `json:"flag,omitempty"`
	// Source distinguishes a value a station typed from one read off a document (Phase 2). A
	// summary treating an OCR reading as equal to a measured one is a summary that has lost the
	// distinction §6.4 exists to preserve.
	Source string `json:"source,omitempty"`
}

// Trend is up to five values of one code, oldest first. §8's snapshot asks for the last five HbA1c;
// the model gets the same series so that "improving" is a claim it can support with references
// rather than an impression.
type Trend struct {
	Code   string       `json:"code"`
	Label  string       `json:"label"`
	Unit   string       `json:"unit,omitempty"`
	Points []TrendPoint `json:"points"`
	Change *TrendChange `json:"change,omitempty"`
}

// TrendPoint is one value in a series.
type TrendPoint struct {
	Ref   string `json:"ref"`
	On    string `json:"on"`
	Value string `json:"value"`
}

// TrendChange is the arithmetic done here rather than by the model.
//
// A language model asked to subtract two numbers will usually be right, and "usually" is the
// problem: the error is invisible, plausible, and in a clinical sentence. The difference and the
// interval are computed in Go, cited like any other fact, and the model's job is to say what they
// mean.
type TrendChange struct {
	Ref      string `json:"ref"`
	From     string `json:"from"`
	To       string `json:"to"`
	Delta    string `json:"delta"`
	OverDays int    `json:"over_days"`
}

// GrowthSummary is [R-06], and none of it is computed here.
//
// `internal/clinical` already scores height, weight and BMI against the seeded reference with the
// standard, the version and the L, M and S parameters recorded per value (CP48, D-21, ADR-0026).
// Re-deriving a z-score in a prompt would be the same arithmetic done a second time, worse, by
// something that cannot show its working — and the two answers would then disagree on the day the
// protocol moved.
type GrowthSummary struct {
	Applicable bool `json:"applicable"`
	// Note names why nothing was computed, when nothing was: too old for a reference, no
	// measurements yet, no reference for the recorded sex.
	Note string `json:"note,omitempty"`
	// Standard is the reference in force at this child's age, and Version its edition. Reported
	// because a percentile without its standard is a number with no meaning.
	Standard        string          `json:"standard,omitempty"`
	StandardVersion string          `json:"standard_version,omitempty"`
	Indicators      []GrowthReading `json:"indicators,omitempty"`
	// ObesityFlag is §7.1's own requirement: the ≥95th-percentile childhood obesity flag. A
	// string rather than a boolean, because "at or above the 95th" and "at or above the 85th" are
	// different clinical statements and the screen already draws both.
	ObesityFlag string `json:"obesity_flag,omitempty"`
}

// GrowthReading is one scored indicator.
type GrowthReading struct {
	Ref        string `json:"ref"`
	Indicator  string `json:"indicator"`
	Label      string `json:"label"`
	Value      string `json:"value"`
	Unit       string `json:"unit,omitempty"`
	Z          string `json:"z"`
	Percentile string `json:"percentile"`
	On         string `json:"on"`
	// Velocity is the change per year since the previous measurement of the same indicator, when
	// there is one far enough back to mean something. The single most useful paediatric number and
	// the one a physician cannot compute in their head from a list.
	Velocity string `json:"velocity_per_year,omitempty"`
	// StandardChanged marks a reading scored against a different reference from the one before it
	// (D-21's five-year boundary). Shown so that the model does not describe a step in the
	// percentile as a change in the child.
	StandardChanged bool `json:"standard_changed,omitempty"`
}

// HistoryItem is one thing the patient or their family has.
type HistoryItem struct {
	Ref  string `json:"ref"`
	Kind string `json:"kind"`
	// Label is the catalogue's words where the item is coded, and the patient's where it is not.
	Label string `json:"label"`
	Code  string `json:"code,omitempty"`
	// Said is what the patient told the officer, kept because "sugar since the flood" is often the
	// clinical detail and the coded title never is.
	Said     string `json:"said,omitempty"`
	Relation string `json:"relation,omitempty"`
	Duration string `json:"duration,omitempty"`
	Severity string `json:"severity,omitempty"`
	Dose     string `json:"dose,omitempty"`
	// Confirmed says somebody at this visit agreed the item is still true. Absent means nobody has
	// — which is exactly what station 4 is looking at, and a summary that presented an unconfirmed
	// three-year-old item as current would be asserting something nobody checked.
	Confirmed bool   `json:"confirmed"`
	On        string `json:"on,omitempty"`
}

// AllergyContext is the patient's allergy state, and the status is the load-bearing part.
//
// `unknown` is not `none`. CP54 made "no known allergies" an attributed assertion rather than a
// default precisely so that this distinction survives to here, and a summary that read an absence
// of records as an absence of allergies would be the exact failure that rule exists to prevent.
type AllergyContext struct {
	Status string        `json:"status"`
	Items  []AllergyItem `json:"items"`
}

// AllergyItem is one recorded allergy.
type AllergyItem struct {
	Ref         string `json:"ref"`
	Substance   string `json:"substance"`
	Code        string `json:"code,omitempty"`
	Reaction    string `json:"reaction"`
	Severity    string `json:"severity,omitempty"`
	Certainty   string `json:"certainty,omitempty"`
	IsEmergency bool   `json:"emergency"`
	On          string `json:"on,omitempty"`
}

// LifestyleContext is station 3's composite and its domains (CP58).
type LifestyleContext struct {
	Ref      string   `json:"ref,omitempty"`
	Score    string   `json:"score,omitempty"`
	Assessed []string `json:"assessed"`
	Missing  []string `json:"missing"`
	// Minimum is how many domains the composite needs. Reported so that an absent score reads as
	// "not enough was asked" rather than as "the patient scored nothing".
	Minimum int `json:"minimum_domains"`
}

// NutritionContext is station 7's 24-hour recall, as totals rather than as a food list.
//
// The list is forty rows of rice and lentils and it is not what a physician reads in a one-page
// summary; the four totals are. The entries stay in the record, and CP73's screen links to them.
type NutritionContext struct {
	RecallDate string `json:"recall_date,omitempty"`
	Entries    int    `json:"entries"`
	Kcal       string `json:"kcal,omitempty"`
	Protein    string `json:"protein_g,omitempty"`
	Carb       string `json:"carbohydrate_g,omitempty"`
	Fat        string `json:"fat_g,omitempty"`
	Ref        string `json:"ref,omitempty"`
}

// ExerciseContext is station 8: what the patient can do, what they must not, and what was issued.
type ExerciseContext struct {
	Ref               string   `json:"ref,omitempty"`
	WalksUnaided      *bool    `json:"walks_unaided,omitempty"`
	WalkMinutes       *int     `json:"walk_minutes,omitempty"`
	JointPain         string   `json:"joint_pain,omitempty"`
	Contraindications []string `json:"contraindications"`
	Asked             []string `json:"asked"`
	// PlanMinutesPerWeek is the issued plan's total, and PlanItems how many exercises it holds.
	PlanMinutesPerWeek *int `json:"plan_minutes_per_week,omitempty"`
	PlanItems          int  `json:"plan_items,omitempty"`
}

// AlertContext is one critical value that was raised on this patient (CP50).
type AlertContext struct {
	Ref       string `json:"ref"`
	Label     string `json:"label"`
	Value     string `json:"value"`
	Unit      string `json:"unit,omitempty"`
	Breached  string `json:"breached"`
	Threshold string `json:"threshold"`
	Status    string `json:"status"`
	Action    string `json:"action,omitempty"`
	On        string `json:"on"`
}

// PriorVisit is §11.1's memory of an earlier journey.
type PriorVisit struct {
	Ref       string `json:"ref"`
	On        string `json:"on"`
	Type      string `json:"type"`
	Complaint string `json:"chief_complaint,omitempty"`
	Diagnoses string `json:"diagnoses,omitempty"`
	Plan      string `json:"plan,omitempty"`
	// ReviewDue is the interval the physician set, in days, as a date. The single most useful
	// number for "did they come back when they were told to", and the model can only say that if
	// it is given both this and the current visit's day.
	ReviewDue string `json:"review_due,omitempty"`
}

// Gap is something the assembler expected and did not find.
//
// The most important section of the context and the one a model would never produce on its own: a
// language model shown a record with no HbA1c writes a summary that does not mention HbA1c, and the
// physician reads a confident page with a hole in it. Naming the holes deterministically is the
// cheapest correction available to this design, and it is what §8's *"missing-data alerts"* panel
// is built from.
type Gap struct {
	// Code is stable and groupable: `no_hba1c_in_12_months`, `station_not_reached`.
	Code string `json:"code"`
	// Detail is the sentence, in English, for the narrative.
	Detail string `json:"detail"`
	// Severity is `note` or `important`. Two levels rather than five: a list where everything is
	// urgent is a list nobody reads.
	Severity string `json:"severity"`
}

// Context is everything the model is shown. It is a value, and asserting against it is how the
// deterministic half of this checkpoint is tested.
type Context struct {
	// AssemblerVersion is the code that produced this. Part of the material hash: changing the
	// assembler changes what the model sees, and a summary produced by an older assembler is out
	// of date in exactly the way a summary produced from older measurements is.
	AssemblerVersion string `json:"assembler_version"`
	// AssembledAt is when. Deliberately **not** part of the material hash — otherwise every look
	// would conclude the summary was stale and every visit would pay for a model call a minute.
	AssembledAt string `json:"assembled_at"`

	Demographics Demographics `json:"demographics"`
	Visit        VisitContext `json:"visit"`

	Allergies AllergyContext `json:"allergies"`
	History   []HistoryItem  `json:"history"`

	Current []Measurement `json:"current_measurements"`
	Trends  []Trend       `json:"trends"`

	Growth *GrowthSummary `json:"growth,omitempty"`

	Lifestyle *LifestyleContext `json:"lifestyle,omitempty"`
	Nutrition *NutritionContext `json:"nutrition,omitempty"`
	Exercise  *ExerciseContext  `json:"exercise,omitempty"`

	Alerts      []AlertContext `json:"alerts"`
	PriorVisits []PriorVisit   `json:"prior_visits"`

	Gaps []Gap `json:"gaps"`

	// Facts is every citable thing above, flattened. The prompt requires the model to cite from
	// this list and nothing else; CP72's validator will check that it did.
	Facts []Fact `json:"facts"`
}

// MaterialSHA256 is the hash that decides whether a re-run is worth paying for.
//
// # Why materiality is decided here and nowhere else
//
// "Material new data" is a judgement, and the checkpoint is right that it will be wrong the first
// time. So it is made in exactly one function, stated in terms of the artefact rather than in terms
// of events, and changing it is changing this function.
//
// The rule: **a change is material when it changes what the model would be shown.** A corrected
// blood pressure changes a fact and is material. A typo in a phone number cannot possibly be
// material, because no phone number ever enters a [Context] — the identifier rule has already made
// that class of change invisible here, which is a pleasing accident of a decision taken for another
// reason. A new operator name, a device id, a re-print, a queue reroute: none of them reach a
// context either.
//
// # What the hash covers, and the two things it deliberately does not
//
// It covers the facts (reference, value, unit, date, note), the complaint, the station completion
// picture, the gaps, the allergy status, and the assembler version.
//
// It does not cover `assembled_at`, which would make every context different from every other and
// turn the check into a coin that always says yes. And it does not cover the *ordering* of anything
// the assembler could reorder without meaning to: the fact lines are sorted before hashing, so a
// database returning two same-day rows in a different order does not buy a model call.
//
// # The cost of being wrong in each direction
//
// Too sensitive: money and queue time, and a physician's page changing under them mid-consultation.
// Too blunt: a physician reads a summary that does not mention the potassium that came back ten
// minutes ago. The second is worse, so where the two are close this errs towards re-running — every
// fact is in, including qualitative notes, rather than a hand-picked subset of "important" codes
// that somebody would have to keep in step with the clinic.
func (c Context) MaterialSHA256() string {
	lines := make([]string, 0, len(c.Facts)+8)
	lines = append(lines,
		"assembler="+c.AssemblerVersion,
		"complaint="+strings.TrimSpace(c.Visit.Complaint),
		"allergy_status="+c.Allergies.Status,
		"complete="+strconv.FormatBool(c.Visit.Complete),
	)
	for _, station := range c.Visit.Stations {
		lines = append(lines, "station="+station.Code+"|"+station.Status+"|"+station.Outcome)
	}
	for _, gap := range c.Gaps {
		lines = append(lines, "gap="+gap.Code)
	}
	for _, fact := range c.Facts {
		lines = append(lines, "fact="+fact.Ref+"|"+fact.Value+"|"+fact.Unit+"|"+fact.On+"|"+fact.Note)
	}
	sort.Strings(lines)

	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// FactRefs is the set of references the model is allowed to cite. CP72 will want exactly this.
func (c Context) FactRefs() map[string]Fact {
	out := make(map[string]Fact, len(c.Facts))
	for _, fact := range c.Facts {
		out[fact.Ref] = fact
	}
	return out
}

// --- formatting, done once ---

// number formats a measured value the way it will be cited.
//
// One place, because the whole grounding argument rests on the string the model reads being the
// string the validator looks for. Trailing zeros are trimmed so that 8.20 and 8.2 are one fact
// rather than two; the cap at three decimals is because no measurement in this system is recorded
// to more and a float64 printed in full would put `8.199999999999999` in a clinical sentence.
func number(v float64) string {
	s := strconv.FormatFloat(v, 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// day formats a date the way every reference and every fact uses it.
//
// In the clinic's own calendar, not UTC: a measurement taken at 08:30 in Faridpur is on that day,
// and a summary that dated it to the previous evening because the server thinks in UTC would be
// wrong in a way a physician would notice and could not explain.
func day(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return ""
	}
	return t.In(loc).Format("2006-01-02")
}

// ref builds a fact reference: `obs.hba1c:2026-03-12`.
//
// Two properties matter and neither is obvious.
//
// It must be **stable** across runs, or a re-run would renumber every citation and the previous
// summary's references would point at nothing.
//
// And it must survive the gateway's free-text scrubber, which is harder than it looks. Two of the
// scrubber's six patterns bite here: seven contiguous digits, and **nine digits separated by any of
// space, dot, bracket, plus or hyphen**. A date written `20260312` trips the first. A date written
// `2026-03-12` is eight separated digits and passes — until something puts a ninth in front of it.
// `obs.spo2.2026-06-09` does exactly that: the code ends in a digit, the dot is a separator the
// pattern counts, and the whole reference becomes a nine-digit "telephone number" that leaves the
// building as `[NUMBER]`. The model is then asked to cite something it was never shown.
//
// So the separator before the date is a **colon**, which is not in the pattern's separator class and
// therefore breaks the run whatever the slug ends in. It is one character and it is the whole of the
// defence; `TestNoFactReferenceLooksLikeATelephoneNumberToTheScrubber` is what keeps it honest.
func ref(kind, slug, on string) string {
	out := kind
	if slug != "" {
		out += "." + slug
	}
	if on != "" {
		out += ":" + on
	}
	return out
}

// uniqueRef makes a reference unique within one context without making it unstable.
//
// Two HbA1c results on one day is not a hypothetical: a repeat after a suspicious first reading is
// ordinary. The first occurrence keeps the plain reference and only a duplicate acquires a suffix,
// which keeps a re-run's citations pointing where the previous summary's pointed for every fact but
// the one that genuinely moved.
//
// **The suffix is a letter and not a number, and that is not a style choice.** A reference already
// ends in a date, and `2026-09-14` is eight digits separated by hyphens; a numeric suffix makes
// `2026-09-14.2`, which is nine digits separated by characters the free-text scrubber counts — so
// the gateway would replace the whole reference with `[NUMBER]` and the model would be asked to
// cite something it had never been shown. The database's own copy of that rule refuses to store the
// context at all, which is how this was found rather than deployed.
func uniqueRef(seen map[string]int, candidate string) string {
	seen[candidate]++
	n := seen[candidate]
	if n == 1 {
		return candidate
	}
	return candidate + "." + letterSuffix(n)
}

// letterSuffix turns an occurrence index into a letter run: 2 → "b", 27 → "aa". No digits, ever;
// see [uniqueRef].
func letterSuffix(n int) string {
	if n < 2 {
		return ""
	}
	out := ""
	for n > 0 {
		n--
		out = string(rune('a'+n%26)) + out
		n /= 26
	}
	return out
}

// slug turns a label into something safe for a reference: lower case, no spaces, no digits runs to
// worry about, and short enough to read.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '/':
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
				b.WriteRune('_')
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if len(out) > 32 {
		out = strings.Trim(out[:32], "_")
	}
	if out == "" {
		out = "item"
	}
	return out
}
