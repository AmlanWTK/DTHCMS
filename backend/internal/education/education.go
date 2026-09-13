// Package education is station 11: what the patient was actually able to do, in front of
// somebody, today — and the one number they give about themselves (CP88, CP92).
//
// # Why two checkpoints share a package
//
// They share a station, a screen and an operator. CP88's improvement score is captured here and
// nowhere else, and the reason is the same reason CP92 exists: the education officer has no
// stake in the answer. The consultant who has just changed a treatment cannot ask how much
// better the patient feels without the answer drifting upward, and it drifts most for the
// patients who most want to please — which in this clinic means the elderly, the poor, and the
// ones who travelled furthest. Splitting the two into two packages would have made that shared
// argument a coincidence of two comments.
//
// # What this package does not own
//
// Any clinical table. Every value this station produces is an ordinary CP42 observation, written
// through `clinical.Service` — one write path, one event type, one timeline, one research
// extract. What is new is reference data: the question and its anchors, the four checklists, the
// mapping from a prescribed product to a device, and the threshold at which technique is called
// poor. All of it is a table, because all of it is a thing a clinician changes their mind about.
//
// # The three states, and the one a simpler design throws away
//
// Every checklist item is scored on what the patient *did*, not on what they said they
// understood, and there are three answers rather than two. "Corrected today" is the most
// clinically useful of the three: a patient who has been injecting into one spot for a year and
// was corrected today is a different patient from one who never needed correcting, and next
// visit's officer needs to know which — because the thing to check next time is exactly what was
// corrected last time. A boolean would have recorded them identically.
package education

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// Permissions this station's routes declare.
const (
	// PermRecord is the write: what was demonstrated, what was corrected, what the patient
	// could not do, and what they said about missed doses.
	PermRecord = "education.record"
	// PermRead is CP92 criterion 2 — competency visible to the physician at the next visit.
	// A separate permission from the write on purpose: the consultant must see this and must
	// not be the one who records it.
	PermRead = "education.read"
	// PermWritePRO is the improvement score. Held by RX_EDUCATOR alone (CP88 spec §1). It is
	// named here as well as in `clinical` because this package's route declares it and a
	// reader of this file should be able to see, without opening another one, that the score
	// is guarded by something the physician does not hold.
	PermWritePRO = "observation.write.pro"
	// PermReference is the clinic's dictionary (CP85): the checklists, the scale, the
	// vocabularies. No patient in any of it.
	PermReference = "reference.read"
)

// ScoreCode is the observation the improvement score is stored under, and NotApplicableCode is
// the marker that says the question was not asked.
//
// Two codes rather than one nullable value, and the difference is CP82's distinction applied to
// a number instead of a decision. **The absence of both rows means nobody got to it. A
// NotApplicableCode row means somebody decided the question did not apply.** A researcher
// counting first visits as low scorers, or as non-responders, would be wrong in opposite
// directions — and a single code with a magic value would have made both mistakes reachable by
// an ordinary `AVG()`.
const (
	ScoreCode         = "IMPROVEMENT_SCORE"
	NotApplicableCode = "IMPROVEMENT_SCORE_NA"

	// MissedDosesCode and MissReasonCode are §7's two halves: the number, asked with a preamble
	// that makes the true number sayable, and the reason, coded because the fix for each is
	// different and the distinction is invisible in a single adherence percentage.
	MissedDosesCode = "MEDICATION_MISSED_DOSES_7D"
	MissReasonCode  = "MEDICATION_MISS_REASON"

	// ReeducationFlagCode is raised by the server, never chosen by the officer.
	ReeducationFlagCode = "EDU_REEDUCATION_FLAG"

	// ScaleCode is the live 1–10 scale of [R-11]. Named rather than discovered so that a
	// deployment which has retired it fails loudly here instead of drawing a blank selector.
	ScaleCode = "IMPROVEMENT_1_10"
)

// ---------------------------------------------------------------------------
// The three states
// ---------------------------------------------------------------------------

// State is what an officer watched happen, per spec §5.
//
// A string type with a closed list rather than a boolean, and the closed list is checked against
// `core.observation_answer` by the database on every write — so a fourth state cannot be
// invented by a client, by a later refactor, or by a hand-written INSERT.
type State string

const (
	// Demonstrated: did it correctly, unprompted.
	Demonstrated State = "demonstrated"
	// CorrectedToday: got it wrong, was shown, then did it correctly. The clinically valuable
	// one, and the reason this is not a boolean.
	CorrectedToday State = "corrected_today"
	// Unable: could not do it correctly even after being shown.
	Unable State = "unable"
)

// States is the closed list, in the order a screen draws them: best to worst, which is the order
// an officer's hand moves down as the assessment goes badly.
var States = []State{Demonstrated, CorrectedToday, Unable}

// Valid reports a state the vocabulary contains.
func (s State) Valid() bool {
	for _, known := range States {
		if s == known {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// The answer to the improvement question
// ---------------------------------------------------------------------------

// Answer is what the education officer got when they asked §2's question.
//
// # Why the fields are unexported
//
// There are exactly two legal shapes — a score, or a reason the question did not apply — and the
// wrong call must be inexpressible rather than merely refused. A struct with two exported
// pointers can be built with both set, with neither set, and with a score of zero that looks
// deliberate; each of those would have to be caught by a validator, and a validator is a thing
// somebody can forget to call. Unexported fields plus two constructors means the illegal states
// have no literal.
//
// The zero Answer is legal and means *the question has not been answered*. That is a third
// thing, and it is the one that must not be confused with the other two: it is what a visit
// carries before the officer gets to it, and it is what `ScoreFor` returns when there is no row.
type Answer struct {
	// score is 1..10 when this Answer carries one, and 0 otherwise. 0 is safe as the sentinel
	// precisely because the scale starts at 1 and the database's plausibility band refuses a 0.
	score int
	// reason is a `core.pro_not_applicable_reason` code when the question did not apply.
	reason string
}

// Scored is the patient's answer.
//
// The bounds are not checked here and that is deliberate: they live in `core.pro_scale` and in
// the observation code's plausibility band, because [R-11]'s 1–10 may become a symmetric 0–10
// and spec §3 promises that is a data change. A constant here would be a third copy and the one
// that silently disagreed.
func Scored(value int) Answer { return Answer{score: value} }

// NotAsked records that the question did not apply, and why.
//
// For a first visit there is no last visit, so the score is not asked. Its absence is recorded
// rather than left as a gap, because a first-visit score would be a number answering a different
// question — and a missing row and a deliberate omission must not read the same.
func NotAsked(reason string) Answer { return Answer{reason: strings.TrimSpace(reason)} }

// Score is the number, and whether there is one.
func (a Answer) Score() (int, bool) { return a.score, a.score != 0 }

// NotApplicable is the reason the question did not apply, and whether it did not.
func (a Answer) NotApplicable() (string, bool) { return a.reason, a.reason != "" }

// Answered reports whether this Answer says anything at all. False is the zero value: nobody has
// asked yet, which is neither a score nor a decision that the question does not apply.
func (a Answer) Answered() bool { return a.score != 0 || a.reason != "" }

// MarshalJSON writes the two shapes as two different objects, with no key in common.
//
// A client testing `answer.score === undefined` cannot be fooled by a default, and a client
// testing `answer.not_applicable` cannot be fooled by an empty string — the same property CP82's
// suggestions have, for the same reason: silence must not be readable as a decision.
func (a Answer) MarshalJSON() ([]byte, error) {
	switch {
	case a.score != 0:
		return json.Marshal(map[string]any{"score": a.score})
	case a.reason != "":
		return json.Marshal(map[string]any{"not_applicable_reason": a.reason})
	default:
		return []byte("null"), nil
	}
}

// UnmarshalJSON refuses an object that carries both shapes or neither.
//
// This is where a client's mistake is caught, and it is the only place: everything downstream
// takes an Answer, and an Answer that reached this far is already one of the two legal shapes.
func (a *Answer) UnmarshalJSON(raw []byte) error {
	var body struct {
		Score               *int    `json:"score"`
		NotApplicableReason *string `json:"not_applicable_reason"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return err
	}
	hasScore := body.Score != nil
	hasReason := body.NotApplicableReason != nil && strings.TrimSpace(*body.NotApplicableReason) != ""
	switch {
	case hasScore && hasReason:
		return fmt.Errorf("%w: an answer is a score or a reason it was not asked, never both",
			ErrAnswerShape)
	case hasScore:
		*a = Scored(*body.Score)
	case hasReason:
		*a = NotAsked(*body.NotApplicableReason)
	default:
		*a = Answer{}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Reference data
// ---------------------------------------------------------------------------

// DeviceType is a kind of thing a patient has to operate.
type DeviceType struct {
	Code     string `json:"code"`
	NameEN   string `json:"name_en"`
	NameBN   string `json:"name_bn"`
	Ordering int    `json:"ordering"`
}

// Checklist is one device's list, as the physician authored it.
type Checklist struct {
	Code       string `json:"code"`
	DeviceType string `json:"device_type"`
	TitleEN    string `json:"title_en"`
	TitleBN    string `json:"title_bn"`
	Items      []Item `json:"items"`
}

// Item is one line of a checklist.
type Item struct {
	Ordinal int `json:"ordinal"`
	// Code is the observation code this item's answer is stored under. The join that makes
	// "no new clinical schema" true, and it is on the wire because the client posts answers by
	// code — a client that posted by ordinal would break the day an item was inserted.
	Code   string `json:"code"`
	TextEN string `json:"text_en"`
	TextBN string `json:"text_bn"`
	// Critical marks the items that silently cost a patient their dose — resuspension, the
	// air-shot, holding for ten. A screen may weight them and a report may count them. It must
	// never become a scoring weight: §8 forbids rolling these into a competency percentage,
	// because a percentage loses the only thing this station produces that nobody else can.
	Critical bool `json:"is_critical"`
}

// AnswerOption is one of the three states with its display text.
type AnswerOption struct {
	State     State  `json:"state"`
	DisplayEN string `json:"display_en"`
	DisplayBN string `json:"display_bn"`
	Ordering  int    `json:"ordering"`
}

// Scale is §2's question and §3's banding.
type Scale struct {
	Code         string   `json:"code"`
	QuestionEN   string   `json:"question_en"`
	QuestionBN   string   `json:"question_bn"`
	MinValue     int      `json:"min_value"`
	MaxValue     int      `json:"max_value"`
	NeutralValue int      `json:"neutral_value"`
	Anchors      []Anchor `json:"anchors"`
}

// Anchor is one band of the scale.
type Anchor struct {
	FromValue int    `json:"from_value"`
	ToValue   int    `json:"to_value"`
	LabelEN   string `json:"label_en"`
	LabelBN   string `json:"label_bn"`
	// FaceRank is 1 for the unhappiest face and n for the happiest. A rank rather than a glyph:
	// a glyph in the database is a rendering decision taken by whoever wrote the migration, and
	// the same rank has to drive a tablet, a printed sheet and a screen reader.
	FaceRank int `json:"face_rank"`
	Ordering int `json:"ordering"`
}

// AnchorFor is the band a value falls in, or false. The invariant in migration 00070 guarantees
// exactly one band claims each value, so a false here means the value is off the scale.
func (s Scale) AnchorFor(value int) (Anchor, bool) {
	for _, anchor := range s.Anchors {
		if value >= anchor.FromValue && value <= anchor.ToValue {
			return anchor, true
		}
	}
	return Anchor{}, false
}

// CodedOption is one entry of a coded vocabulary: why the question did not apply, why a dose was
// missed.
type CodedOption struct {
	Code      string `json:"code"`
	DisplayEN string `json:"display_en"`
	DisplayBN string `json:"display_bn"`
	Ordering  int    `json:"ordering"`
}

// Policy is when technique is poor enough to say so (spec §5).
type Policy struct {
	// UnableRaisesFlag: any single "Unable" raises the flag on its own.
	UnableRaisesFlag bool `json:"unable_raises_flag"`
	// CorrectedTodayThreshold: this many "Corrected today" answers raise it as well. Three, per
	// §5, and configurable because the threshold is a judgement rather than a fact.
	CorrectedTodayThreshold int `json:"corrected_today_threshold"`
}

// Raises applies the policy to one assessment's tally.
//
// One function, called by the service when it writes and by nothing else, so that the screen
// cannot draw a flag the record does not carry. A client-side copy of this arithmetic was the
// obvious optimisation and would have produced exactly the defect worth avoiding: a flag an
// officer saw and the physician never did.
func (p Policy) Raises(unable, correctedToday int) bool {
	if p.UnableRaisesFlag && unable > 0 {
		return true
	}
	return p.CorrectedTodayThreshold > 0 && correctedToday >= p.CorrectedTodayThreshold
}

// Reference is everything a station app needs before the patient sits down, in one fetch.
//
// One object rather than six endpoints, for the reason CP51's answer vocabularies are one
// endpoint: the officer needs all of it at the moment the patient sits down, and six round trips
// on a clinic connection is the difference between a station that keeps up and one that does
// not. It contains no patient, so it is `reference.read` and it caches.
type Reference struct {
	DeviceTypes  []DeviceType   `json:"device_types"`
	Checklists   []Checklist    `json:"checklists"`
	States       []AnswerOption `json:"states"`
	Scale        Scale          `json:"score_scale"`
	NotAskedWhy  []CodedOption  `json:"not_applicable_reasons"`
	MissReasons  []CodedOption  `json:"missed_dose_reasons"`
	Policy       Policy         `json:"reeducation_policy"`
	ComplianceEN string         `json:"compliance_question_en"`
	ComplianceBN string         `json:"compliance_question_bn"`
}

// The compliance question, with the preamble, exactly as spec §7 authored it.
//
// # Why the preamble is in the code and the checklists are in the database
//
// It is not politeness and it is not a label. *"Most people miss a dose sometimes"* tells the
// patient that missing doses is normal and expected, which is what makes the true number
// sayable; without it, "did you take your medicine every day" is a question with one socially
// acceptable answer and the number that comes back is decoration.
//
// It lives here rather than in a table because it is not a list — there is one of it, it is asked
// the same way every time, and a table with one row would invite an edit that quietly deleted
// the half that does the work. A screen renders these strings; it does not carry its own copy,
// and `TestTheComplianceQuestionKeepsItsPreamble` is what stops the sentence being shortened by
// somebody tidying up a UI string.
const (
	ComplianceQuestionEN = "Most people miss a dose sometimes. In the last week, how many times did you miss?"
	ComplianceQuestionBN = "প্রায় সবারই কোনো না কোনো দিন ওষুধ বাদ পড়ে। গত এক সপ্তাহে আপনার কতবার বাদ পড়েছে?"
)

// ---------------------------------------------------------------------------
// What the station records
// ---------------------------------------------------------------------------

// ItemResult is one checklist line's answer.
type ItemResult struct {
	Code  string `json:"code"`
	State State  `json:"state"`
}

// Compliance is §7's answer: how many doses were missed, and why.
type Compliance struct {
	// MissedDoses is the count. A pointer, because "they were not asked" and "they said none"
	// are different facts and a zero would merge them — the same distinction the improvement
	// score draws between a missing row and a not-applicable one, in a smaller place.
	MissedDoses *int `json:"missed_doses"`
	// Reasons are why, coded. Empty when nothing was missed, which is the only case where an
	// empty list is not a gap: §7 asks for the reason *if any were missed*.
	Reasons []string `json:"reasons"`
}

// Assessment is one visit's worth of station 11, written in one transaction.
//
// One call rather than one per item, and this is the opposite of the decision CP56 made for the
// counselling checklist — worth saying why, because the two look alike. A counselling tick is
// evidence that a specific topic was covered by a specific person, and §5.4's spot-questioning
// asks about one at a time, so twenty ticks must be twenty acts. A technique assessment is one
// act: the officer watches the patient inject, once, and scores ten lines about the one thing
// they just watched. Splitting it would record ten timestamps for a single demonstration and
// would let a half-written assessment sit in the record looking complete.
type Assessment struct {
	EventID   uuid.UUID
	PatientID uuid.UUID
	VisitID   uuid.UUID

	// Items are the lines that were scored. Not necessarily every line of every selected
	// checklist: an officer who ran out of time has recorded what they saw, and a service that
	// refused a partial assessment would make the record *less* true rather than more.
	Items []ItemResult

	Compliance Compliance

	// Improvement is §2's answer, or the zero Answer when the officer has not asked yet.
	Improvement Answer

	// LedgerSource is which surface wrote this — a tablet at the station, or the web. From the
	// request rather than from the body, like every other write in this system: a client that
	// could name its own surface could make an offline sync look like a person standing at a
	// desk.
	LedgerSource eventstore.Source
}

// ---------------------------------------------------------------------------
// What a later reader sees
// ---------------------------------------------------------------------------

// Competency is what one patient could and could not do, most recently.
//
// Per item and never rolled up, per spec §8: *"a percentage would be easier to plot and would
// lose the only thing this station produces that nobody else can — which specific step this
// specific patient got wrong, in front of somebody, today."* There is deliberately no Score
// field on this type, and adding one would be the change §8 forbids.
type Competency struct {
	Code  string `json:"code"`
	State State  `json:"state"`
	// ChecklistCode and Ordinal place the item, so a physician reading it does not have to know
	// that EDU_PEN_04 is the air-shot.
	ChecklistCode string `json:"checklist_code"`
	Ordinal       int    `json:"ordinal"`
	TextEN        string `json:"text_en"`
	TextBN        string `json:"text_bn"`
	Critical      bool   `json:"is_critical"`

	ObservedAt string    `json:"observed_at"`
	VisitID    uuid.UUID `json:"visit_id,omitempty"`

	// Who watched, and when. §4.2's promise applies here as much as to a blood pressure and
	// arguably more: "what were you told about injection sites, and by whom" is the question
	// CP92 exists to make answerable, and a competency row with no name against it answers
	// half of it. Carried whole rather than as a formatted string, so the screen draws it with
	// the same component every other clinical value on the application uses.
	RecordedBy   uuid.UUID `json:"recorded_by"`
	RecordedRole string    `json:"recorded_role"`
	RecordedAt   string    `json:"recorded_at"`
	StationCode  string    `json:"station_code,omitempty"`
	Source       string    `json:"source"`
}

// RecordedAnswer is the attribution every recorded value on this screen carries.
//
// One struct embedded in three places rather than five fields repeated three times: they are
// the same fact about three different values, and a screen that drew them differently would be
// a screen where one of them quietly lost its name.
type RecordedAnswer struct {
	RecordedBy   uuid.UUID `json:"recorded_by"`
	RecordedRole string    `json:"recorded_role"`
	RecordedAt   string    `json:"recorded_at"`
	EffectiveAt  string    `json:"effective_at"`
	StationCode  string    `json:"station_code,omitempty"`
	Source       string    `json:"source"`
}

// RecordedCompliance is §7's answer as it was recorded, for a reader rather than an asker.
//
// Nil when nobody asked at this visit — which is a third state beside "they said none" and
// "they said three", and the one a screen must not draw as a zero.
type RecordedCompliance struct {
	// MissedDoses is nil when the reasons were recorded and the count was not. Unusual and
	// possible: an officer who typed a reason and was interrupted.
	MissedDoses *int     `json:"missed_doses"`
	Reasons     []string `json:"reasons"`
	RecordedAnswer
}

// RecordedImprovement is §2's answer as it was recorded, with who asked it.
//
// The asker is the point of CP88 §1, not decoration: the score is captured by somebody with no
// stake in the answer, and a physician reading a 9 is entitled to see that the person who wrote
// it down was not the person whose treatment it grades.
type RecordedImprovement struct {
	Answer Answer `json:"answer"`
	RecordedAnswer
}

// Session is the education station's screen for one patient at one visit.
type Session struct {
	PatientID uuid.UUID `json:"patient_id"`
	VisitID   uuid.UUID `json:"visit_id"`

	// Checklists are the ones the prescription selected, in device order. Empty is an answer
	// and not an error: a patient on tablets alone operates no device, and the station's work
	// for them is the compliance question and the score.
	Checklists []Checklist `json:"checklists"`

	// Devices names why each checklist is here — which prescribed line brought it up. CP92's
	// criterion is that the checklist appears with no manual selection, and an officer looking
	// at a checklist they did not choose is owed the reason it is on their screen.
	Devices []SelectedDevice `json:"selected_devices"`

	// Unclassified are prescribed lines this station ought to recognise and does not.
	//
	// The distinction it draws is the one an empty `Checklists` cannot: a patient on tablets
	// alone brings up no checklist and that is correct, while a patient on a GLP-1 the formulary
	// gained last year also brings up none and is about to leave having been taught nothing
	// about the pen in their bag. On the screen those two are identical, so the second is named.
	//
	// It is the runtime half of spec §6.4's decision to give the GLP-1 rules no class-level
	// fallback. Inheriting a checklist from a sibling molecule would assert a dosing rhythm
	// nobody confirmed — a daily patient told to inject on Fridays — and the safer answer is to
	// select nothing and say so to the one person who can resolve it that morning.
	Unclassified []UnclassifiedDevice `json:"unclassified_devices"`

	// Improvement is this visit's answer, or the zero Answer when it has not been asked.
	Improvement Answer `json:"improvement"`
	// ImprovementRecord is the same answer with the person who asked it against it, or nil when
	// nobody has. Beside `Improvement` rather than replacing it because the two have different
	// readers: the officer's screen needs the value to seed a control, and the physician's needs
	// the value *and* the asker, because §1's whole argument is about who asked.
	ImprovementRecord *RecordedImprovement `json:"improvement_record,omitempty"`

	// ComplianceRecord is what the patient said about missed doses at this visit, or nil when
	// nobody asked. §7's question is asked here and read at the next consultation.
	ComplianceRecord *RecordedCompliance `json:"compliance_record,omitempty"`
	// FirstVisit is true when this patient has no earlier visit, which is when §2 says the
	// question is not asked at all. The server decides it rather than the screen, because a
	// screen that decided it from an empty history would call every new-to-this-clinic patient
	// a first visit.
	FirstVisit bool `json:"first_visit"`

	// PriorCompetency is what the last officer saw, per item. The thing to check next time is
	// exactly what was corrected last time.
	PriorCompetency []Competency `json:"prior_competency"`
	// ReeducationFlagged is whether the standing flag is raised from an earlier visit.
	ReeducationFlagged bool `json:"reeducation_flagged"`
}

// UnclassifiedDevice is a prescribed line whose kind this station knows and whose molecule it
// does not.
type UnclassifiedDevice struct {
	ProductID   uuid.UUID `json:"product_id"`
	GenericName string    `json:"generic_name"`
	ClassCode   string    `json:"class_code"`
	ClassNameEN string    `json:"class_name_en"`
	ClassNameBN string    `json:"class_name_bn"`
}

// SelectedDevice is one prescribed line and the checklist it brought up.
type SelectedDevice struct {
	ChecklistCode string    `json:"checklist_code"`
	DeviceType    string    `json:"device_type"`
	ProductID     uuid.UUID `json:"product_id"`
	// ProductLabel is the line as it was prescribed, copied onto the prescription at the moment
	// of prescribing — so a trade name corrected next year does not change what this said.
	ProductLabel string `json:"product_label"`
	GenericName  string `json:"generic_name,omitempty"`
}

// ---------------------------------------------------------------------------
// Refusals
// ---------------------------------------------------------------------------

var (
	// ErrAnswerShape is an improvement answer that is both a score and a not-applicable, or a
	// checklist answer that is neither of the three states.
	ErrAnswerShape = errors.New("education: that is not the shape this answer takes")
	// ErrUnknownItem is an answer against a code that is not on any live checklist.
	ErrUnknownItem = errors.New("education: that is not a checklist item")
	// ErrNotSelected is an answer against an item whose checklist this patient's prescription
	// did not select. Refused rather than accepted quietly: a pen checklist filled in for a
	// patient on tablets alone is either a mis-tap or a record about somebody else.
	ErrNotSelected = errors.New("education: this patient's prescription does not bring up that checklist")
	// ErrNoVisit is an assessment against a visit that is not this patient's open one.
	ErrNoVisit = errors.New("education: no such visit for this patient")
	// ErrScoreOnFirstVisit is a number where §2 says there is no question. Refused rather than
	// stored, because a first-visit score is a number answering a different question and it
	// would be averaged with the others.
	ErrScoreOnFirstVisit = errors.New("education: there is no last visit to compare with, so there is no score to record")
	// ErrUnknownReason is a not-applicable reason or a missed-dose reason outside the
	// vocabulary.
	ErrUnknownReason = errors.New("education: that is not a reason this question takes")
	// ErrNothingToRecord is an assessment with no items, no compliance answer and no score.
	ErrNothingToRecord = errors.New("education: there is nothing in this assessment to record")
	// ErrNotFound is a patient who is not this facility's, or does not exist. The same error
	// for both: a 403 must not reveal whether a resource exists.
	ErrNotFound = errors.New("education: no such patient")
)
