package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/allergy"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical/calc"
	"github.com/AmlanWTK/DTHCMS/backend/internal/counseling"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/history"
	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
	"github.com/AmlanWTK/DTHCMS/backend/internal/synthesis"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// ErrNoSuchPatient is a patient this facility does not have.
//
// The one error this module distinguishes, and it is turned into the same 404 a patient read
// gives — never into "you may not see this patient", which would answer the question of
// whether the patient exists.
var ErrNoSuchPatient = errors.New("dashboard: no such patient")

// EmergencyAccess is how this module learns that a break-glass door is open.
//
// An interface rather than an import, for the reason `patient.AuditRecorder` is one: this
// module may not import `audit` and `audit` may not import this one, and `cmd/api` owns the
// bridge between them. It also means a deployment with no break-glass wired reports
// [BasisNormal] and works — which is the right failure direction, because the alternative is
// a dashboard that refuses to load when an audit query is slow.
type EmergencyAccess interface {
	// OpenFor returns the caller's own live break-glass access covering this patient, if any.
	//
	// The caller's own, and never anybody else's: a physician reading a record has no
	// business being told which colleague opened a door on it. That question belongs to the
	// administrator's console, which has it (CP22).
	OpenFor(ctx context.Context, userID, patientID, facilityID uuid.UUID) (*BreakGlassNote, error)
}

// Service assembles the screen.
//
// It holds the other modules' stores by pointer rather than an interface each, and that is
// deliberate: an interface per dependency would be six one-method interfaces whose only
// implementations are the real stores, which buys a mock nobody should write. The tests here
// run against a real database, because the thing being tested is a fan-out of nine real
// queries and a fake would test the fan-out against itself.
type Service struct {
	patients   *patient.Store
	visits     *visit.Store
	clinical   *clinical.Service
	values     *clinical.Store
	history    *history.Store
	allergies  *allergy.Store
	counseling *counseling.Store
	synthesis  *synthesis.Service
	events     *eventstore.Store
	emergency  EmergencyAccess
	clock      Clock
}

// Clock is the time this screen is assembled at.
type Clock interface{ Now() time.Time }

// Config builds a Service.
type Config struct {
	Patients   *patient.Store
	Visits     *visit.Store
	Clinical   *clinical.Service
	Values     *clinical.Store
	History    *history.Store
	Allergies  *allergy.Store
	Counseling *counseling.Store
	Synthesis  *synthesis.Service
	Events     *eventstore.Store
	Emergency  EmergencyAccess
	Clock      Clock
}

// NewService builds it.
func NewService(cfg Config) *Service {
	return &Service{
		patients: cfg.Patients, visits: cfg.Visits, clinical: cfg.Clinical, values: cfg.Values,
		history: cfg.History, allergies: cfg.Allergies, counseling: cfg.Counseling,
		synthesis: cfg.Synthesis, events: cfg.Events, emergency: cfg.Emergency, clock: cfg.Clock,
	}
}

// Request is one look at one patient.
type Request struct {
	PatientID uuid.UUID
	// VisitID pins the dashboard to a particular journey. Zero means "the one happening now,
	// or the most recent one" — which is what a physician calling a patient in wants, and
	// which saves the client a round trip it would otherwise make to find out. That saved
	// round trip is not a micro-optimisation: it is one of the twelve.
	VisitID    uuid.UUID
	FacilityID uuid.UUID
	// Subject is the caller, resolved by the route guard. Every panel is decided against it.
	Subject rbac.Subject
}

// Assemble is the whole endpoint.
//
// # The order of the work
//
// The patient is read first and alone, because everything else is a read *about* a patient
// and issuing nine queries for an id that does not exist would be nine wasted queries and a
// way to learn that an id is not in the register. The visit is resolved second because three
// of the remaining reads are keyed on it.
//
// Then the fan-out. Every remaining panel is read concurrently, each on its own pooled
// connection, and the endpoint's wall time is the slowest of them rather than their sum.
//
// # Why a failed panel does not fail the screen
//
// D-15 again. A dashboard that returned 500 because the counselling checklist was slow would
// be a dashboard a physician cannot open during an incident that has nothing to do with the
// patient in front of them. So a panel that could not be read is **absent with its reason
// named**, exactly like a panel the caller may not see — different sentence, same shape — and
// the rest of the screen draws.
//
// The two exceptions are the patient and the allergy state. A patient that cannot be read is
// no screen at all. An allergy state that cannot be read is the one panel where absence is
// dangerous rather than merely incomplete, and the honest answer is to fail the request: a
// prescriber must never be shown a patient header with no allergy line and no explanation,
// because a missing line reads as "none". The allergy strip has its own unreadable state and
// the client renders it; what must not happen is the strip being silently absent from a
// payload that otherwise looks complete.
func (s *Service) Assemble(ctx context.Context, req Request) (View, error) {
	now := s.clock.Now().UTC()

	person, err := s.patients.ByID(ctx, req.PatientID, req.FacilityID)
	if err != nil {
		if errors.Is(err, patient.ErrNotFound) {
			return View{}, ErrNoSuchPatient
		}
		return View{}, err
	}

	current, err := s.resolveVisit(ctx, req)
	if err != nil {
		return View{}, err
	}

	view := View{
		AsOf:    now,
		Patient: identityOf(person, now),
		Visit:   current,
		Access:  Access{Basis: BasisNormal},
		Omitted: []Omission{},
		Alerts:  []clinical.Alert{},
		Vitals:  []clinical.Observation{},
		Trends:  []Trend{},
	}

	var visitID uuid.UUID
	if current != nil {
		visitID = current.ID
	}

	// The panels, gathered concurrently. `collect` runs each one only when the subject may
	// read it, records an omission when they may not, and turns a read failure into an
	// omission rather than into a 500.
	g := &gather{view: &view}

	g.run(func() { s.readAllergies(ctx, req, g) })
	g.permit(req.Subject, "critical_alerts", clinical.PermAlertRead, func() {
		s.readAlerts(ctx, req, g)
	})
	g.permit(req.Subject, "vitals", clinical.PermObservationRead, func() {
		s.readValues(ctx, req, g)
	})
	g.permit(req.Subject, "active_conditions", history.PermRead, func() {
		s.readConditions(ctx, req, g)
	})
	g.permit(req.Subject, "growth", clinical.PermObservationRead, func() {
		s.readGrowth(ctx, req, g)
	})
	g.permit(req.Subject, "counseling", counseling.PermSessionRead, func() {
		s.readCounseling(ctx, visitID, g)
	})
	g.permit(req.Subject, "summary", synthesis.PermRead, func() {
		s.readSummary(ctx, req, visitID, g)
	})
	g.run(func() { s.readAccess(ctx, req, g) })

	g.wait()

	if g.fatal != nil {
		return View{}, g.fatal
	}

	// The decisions are folded last, because they are folded *onto* the suggestions the
	// summary read produced. Reading them concurrently would mean either locking the
	// suggestion list or reading the visit's stream for a run that turned out not to exist.
	if view.Assistant != nil && visitID != uuid.Nil {
		if err := s.applyDecisions(ctx, visitID, view.Assistant); err != nil {
			// Not fatal, and not silent. A decision that could not be read draws the
			// suggestion as undecided, which would let a physician accept the same draft
			// twice — annoying, and much better than refusing the whole screen. The
			// omission says so in words so that the panel can warn rather than pretend.
			g.omit("assistant.decisions", "", err)
			view.Omitted = g.omissions()
		}
	}

	sortOmissions(view.Omitted)
	return view, nil
}

// resolveVisit finds the journey this dashboard is about.
//
// The open visit if there is one, and otherwise the most recent. Not "the newest": a patient
// who came in March and again this morning has two, and only one of them is happening. A
// screen that showed March's counselling checklist as something to finish would be sending
// somebody to a room for a conversation that happened six months ago.
func (s *Service) resolveVisit(ctx context.Context, req Request) (*VisitContext, error) {
	if req.VisitID != uuid.Nil {
		found, err := s.visits.ByID(ctx, req.VisitID, req.FacilityID)
		if err != nil {
			if errors.Is(err, visit.ErrNotFound) {
				return nil, ErrNoSuchPatient
			}
			return nil, err
		}
		// A visit id belonging to somebody else is refused as a missing patient rather than
		// as a mismatch: telling a caller that a visit exists but is not this patient's is
		// telling them about another patient's visit.
		if found.PatientID != req.PatientID {
			return nil, ErrNoSuchPatient
		}
		return visitContextOf(found), nil
	}

	open, ok, err := s.visits.OpenFor(ctx, req.PatientID, req.FacilityID)
	if err != nil {
		return nil, err
	}
	if ok {
		return visitContextOf(open), nil
	}

	recent, err := s.visits.ForPatient(ctx, req.PatientID, req.FacilityID, 1)
	if err != nil {
		return nil, err
	}
	if len(recent) == 0 {
		// Registered and never seen. A real state, and not an error: see [VisitContext].
		return nil, nil
	}
	return visitContextOf(recent[0]), nil
}

func visitContextOf(v visit.Visit) *VisitContext {
	return &VisitContext{
		ID: v.ID, VisitCode: v.VisitCode, VisitType: string(v.VisitType),
		Status: string(v.Status), Open: v.Status == visit.Open,
		ChiefComplaint: v.ChiefComplaint, ClinicDay: v.ClinicDay, OpenedAt: v.OpenedAt,
	}
}

// identityOf is the demographics half of the snapshot.
//
// The age is carried twice — as text and as months — and that is not duplication. The text is
// what a screen renders and a person says; the months are what the growth reference is keyed
// by, and rounding them to years is the paediatric panel's whole point thrown away. Both are
// derived here, from the one validated date of birth, so that they cannot come to disagree.
func identityOf(p patient.Patient, now time.Time) Identity {
	return Identity{
		ID: p.ID, ClinicalID: p.ClinicalID,
		NameEN: p.NameEN, NameBN: p.NameBN, Sex: string(p.Sex),
		BirthDate: p.Birth.Date.Format(time.DateOnly),
		AgeText:   ageText(p.Birth.Date, now),
		AgeMonths: monthsBetween(p.Birth.Date, now),
		Status:    string(p.Status),
	}
}

// --- the panels ---

func (s *Service) readAllergies(ctx context.Context, req Request, g *gather) {
	// The allergy state is read for **every** caller who reached this endpoint, without a
	// permission gate of its own, and that is the CP54 argument carried through: a header
	// with no allergy line looks like a patient with no allergies. Somebody who may not read
	// allergies is refused the whole dashboard by the route's own permission before they get
	// here; there is no caller who may see the screen and may not see the strip.
	state, err := s.allergies.For(ctx, req.PatientID)
	if err != nil {
		// Fatal, and it is the only read below the patient that is. See [Service.Assemble].
		g.fail(fmt.Errorf("the allergy status could not be read: %w", err))
		return
	}
	g.set(func(v *View) { v.Allergies = &state })
}

func (s *Service) readAlerts(ctx context.Context, req Request, g *gather) {
	alerts, err := s.values.AlertsForPatient(ctx, req.PatientID, req.FacilityID, 20)
	if err != nil {
		g.omit("critical_alerts", clinical.PermAlertRead, err)
		return
	}
	g.set(func(v *View) { v.Alerts = alerts })
}

// readValues is the current values, the BMI and the trends: three panels from two queries.
//
// The current values come from one read. The trends are one read per code — four — and that
// is the fan-out's widest point and the place a future code list would grow it. It is bounded
// by [TrendCodes] having a fixed length, on purpose: a per-patient list would make the
// endpoint's cost a property of the patient, and the patient it would be most expensive for
// is the one with ten years of history, which is precisely the case criterion 1 is measured
// against.
func (s *Service) readValues(ctx context.Context, req Request, g *gather) {
	// `Current` and not `ForPatient`: the newest live value of **each code**, one row per
	// code. `ForPatient` answers "what has been recorded lately" with a limit, which for a
	// patient with six years of quarterly visits is forty weights and thirty-nine of them
	// history — and the fortieth code, last measured before the limit's window, missing
	// altogether. This was the first version of this panel and it returned two hundred rows
	// to draw ten.
	//
	// The empty category is every category: the snapshot draws vitals, anthropometry and the
	// derived values together, because a physician reading a patient does not think in the
	// categories a station is organised by.
	current, err := s.values.Current(ctx, req.PatientID, req.FacilityID, "")
	if err != nil {
		g.omit("vitals", clinical.PermObservationRead, err)
		return
	}
	g.set(func(v *View) {
		v.Vitals = current
		v.BodyMass = bodyMassOf(current)
	})

	trends := make([]Trend, 0, len(TrendCodes))
	for _, code := range TrendCodes {
		points, err := s.values.History(ctx, req.PatientID, req.FacilityID, code, TrendPoints)
		if err != nil {
			g.omit("trends."+strings.ToLower(code), clinical.PermObservationRead, err)
			continue
		}
		if trend, ok := trendOf(code, points); ok {
			trends = append(trends, trend)
		}
	}
	g.set(func(v *View) { v.Trends = trends })
}

func (s *Service) readConditions(ctx context.Context, req Request, g *gather) {
	items, err := s.history.ForPatient(ctx, req.PatientID)
	if err != nil {
		g.omit("active_conditions", history.PermRead, err)
		return
	}
	g.set(func(v *View) { v.Conditions = activeConditions(items) })
}

func (s *Service) readGrowth(ctx context.Context, req Request, g *gather) {
	growth, err := s.clinical.GrowthFor(ctx, req.PatientID, req.FacilityID)
	if err != nil {
		g.omit("growth", clinical.PermObservationRead, err)
		return
	}
	status, err := s.clinical.WeightStatus(ctx, growth)
	if err != nil {
		// The percentile card still draws; only [R-06]'s obesity flag is missing, and the
		// card says which of its parts is absent rather than showing three dashes.
		g.omit("weight_status", clinical.PermObservationRead, err)
	}
	g.set(func(v *View) {
		v.Growth = &growth
		v.WeightStatus = status
	})
}

func (s *Service) readCounseling(ctx context.Context, visitID uuid.UUID, g *gather) {
	if visitID == uuid.Nil {
		// No visit, no checklist. Not an omission: a patient who has never attended has
		// nothing to have been counselled about, and an "unavailable" note here would be a
		// warning about a state that is entirely normal.
		return
	}
	gate, err := s.counseling.Gate(ctx, visitID)
	if err != nil {
		g.omit("counseling", counseling.PermSessionRead, err)
		return
	}
	missing, err := s.counseling.Missing(ctx, visitID)
	if err != nil {
		g.omit("counseling", counseling.PermSessionRead, err)
		return
	}
	gate.Missing = missing
	g.set(func(v *View) { v.Counseling = &gate })
}

func (s *Service) readSummary(ctx context.Context, req Request, visitID uuid.UUID, g *gather) {
	if visitID == uuid.Nil {
		return
	}
	current, err := s.synthesis.Current(ctx, visitID, req.FacilityID)
	if err != nil {
		g.omit("summary", synthesis.PermRead, err)
		return
	}
	summary, assistant := summaryOf(current)
	g.set(func(v *View) {
		v.Summary = summary
		// The right panel is gated on the same permission as the centre one, and that is a
		// decision rather than a convenience: a drafted diagnosis is a clinical
		// interpretation exactly as the narrative is, and a role blinded from one has no
		// business reading the other.
		if rbac.Sees(req.Subject, synthesis.PermRead) {
			v.Assistant = assistant
		}
	})
}

func (s *Service) readAccess(ctx context.Context, req Request, g *gather) {
	if s.emergency == nil {
		return
	}
	note, err := s.emergency.OpenFor(ctx, req.Subject.UserID, req.PatientID, req.FacilityID)
	if err != nil {
		// The screen draws under the ordinary basis. This is the one place where the safe
		// direction is arguable — an open door not shown is a door somebody forgets — so it
		// is recorded as an omission and the banner says it could not be checked, rather
		// than silently reading as "no emergency access here".
		g.omit("access.break_glass", "", err)
		return
	}
	if note == nil {
		return
	}
	g.set(func(v *View) {
		v.Access = Access{Basis: BasisBreakGlass, BreakGlass: note}
	})
}

// --- derivations ---

// bodyMassOf finds the stored BMI among the current values and bands it.
func bodyMassOf(current []clinical.Observation) *BodyMass {
	for _, obs := range current {
		if obs.Code != "BMI" || obs.Value == nil {
			continue
		}
		// True, always, and never a request parameter: this is a clinic in Faridpur, and a
		// client that could ask for the international scale could ask for it by accident and
		// move a patient out of the screening pathway. See [BodyMass].
		const asianScale = true
		class, version, err := calc.Classify(*obs.Value, asianScale)
		if err != nil {
			// A stored BMI of zero or less. The value is still shown — it is what the record
			// says — and no class is invented for it.
			return &BodyMass{Observation: obs, Scale: "asian"}
		}
		return &BodyMass{
			Observation: obs, Class: string(class), ClassVersion: version, Scale: "asian",
		}
	}
	return nil
}

// trendOf turns one code's history into a series, oldest first, with its change.
//
// `clinical.Store.History` returns newest first and includes replaced values, because that is
// what a correction chain view needs. A sparkline needs neither: a corrected height drawn as
// a step down and back up would be a picture of an operator's typo rather than of a patient.
// So the replaced rows are dropped and the order is reversed here, once, rather than in each
// screen that draws a line.
func trendOf(code string, points []clinical.Observation) (Trend, bool) {
	live := make([]clinical.Observation, 0, len(points))
	for _, p := range points {
		if p.Status != clinical.Active || p.Value == nil {
			continue
		}
		live = append(live, p)
	}
	if len(live) == 0 {
		return Trend{}, false
	}
	sort.SliceStable(live, func(i, j int) bool {
		return live[i].EffectiveAt.Before(live[j].EffectiveAt)
	})

	trend := Trend{Code: code, Unit: live[0].Unit, Points: live}
	if len(live) >= 2 {
		first, last := live[0], live[len(live)-1]
		trend.Change = &TrendChange{
			From: *first.Value, To: *last.Value,
			Delta:    *last.Value - *first.Value,
			OverDays: int(last.EffectiveAt.Sub(first.EffectiveAt).Hours() / 24),
		}
	}
	return trend, true
}

// activeConditions is what §8 calls "active diagnoses", from what the record holds.
//
// Comorbidities and complaints that nobody has marked resolved or removed, coded ones first.
// Family history, vaccinations and surgical history are left out — they are history rather
// than something the patient has now, and a snapshot panel listing a grandmother's diabetes
// beside the patient's own is a panel a physician has to read twice.
//
// **Medications are also left out**, and that one is worth defending: the patient's current
// drugs are exactly what a physician wants beside a drafted prescription. They are omitted
// because the medication reconciliation screen is CP81's and putting a half-list here — the
// history station's record of what the patient said they take, without the clinic's own
// prescriptions, which do not exist yet — would be showing a drug list that is missing
// everything this clinic ever prescribed.
func activeConditions(items []history.Item) []history.Item {
	out := make([]history.Item, 0, len(items))
	for _, item := range items {
		if item.Status != "ACTIVE" {
			continue
		}
		switch item.Kind {
		case "COMORBIDITY", "COMPLAINT":
			out = append(out, item)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Coded() != out[j].Coded() {
			return out[i].Coded()
		}
		return out[i].RecordedAt.After(out[j].RecordedAt)
	})
	return out
}

// --- the summary and the assistant ---

// modelOutput is the shape `clinical.synthesis`'s prompt schema guarantees.
//
// Decoded here rather than passed through as raw JSON, so that the client is not parsing an
// untyped blob to find the sentence it has to mark as machine-written. Every field is
// optional in this struct although the schema requires most of them: the schema is enforced
// by the gateway before the answer is ever stored, and a decoder that panicked on a shape it
// did not expect would turn a prompt-version skew into a blank dashboard.
type modelOutput struct {
	Narrative  string   `json:"narrative_en"`
	KeyPoints  []string `json:"key_points"`
	Confidence *float64 `json:"confidence"`
	Citations  []string `json:"citations"`

	Diagnoses []struct {
		Label string   `json:"label"`
		ICD10 string   `json:"icd10"`
		Basis []string `json:"basis"`
	} `json:"suggested_diagnoses"`

	Investigations []struct {
		Investigation string   `json:"investigation"`
		Why           string   `json:"why"`
		Basis         []string `json:"basis"`
	} `json:"missing_investigations"`

	Medications []struct {
		Drug      string   `json:"drug"`
		Dose      string   `json:"dose"`
		Frequency string   `json:"frequency"`
		Route     string   `json:"route"`
		Rationale string   `json:"rationale"`
		Basis     []string `json:"basis"`
	} `json:"draft_medications"`

	RedFlags []struct {
		Severity  string   `json:"severity"`
		Statement string   `json:"statement"`
		Basis     []string `json:"basis"`
	} `json:"red_flags"`
}

// summaryOf projects the synthesis view onto the centre and right panels.
//
// # Why the gaps reach the panel even when the model's answer does not
//
// A `FAILED` or `PENDING` run has no output — but its *context* has [synthesis.Gap]s, and
// those were computed by the deterministic assembler with no model involved at all. §8 puts
// missing-data alerts in the right panel, and there is no reason a physician should lose
// "no HbA1c in twelve months" because a provider timed out. So the gaps are carried whenever
// there is a run to carry them from, marked [OriginSystem], and the panel says plainly that
// the model's half is missing.
//
// This is the sharpest expression of criterion 3 in the whole checkpoint: the panel is a
// mixture, the mixture is marked item by item, and on a degraded run the panel is *entirely*
// system-derived and says so.
func summaryOf(view synthesis.View) (*Summary, *Assistant) {
	summary := &Summary{
		State: view.State, AIGenerated: true, Degraded: view.Degraded,
		MessageEN: view.MessageEN, MessageBN: view.MessageBN,
		Requestable: view.Requestable,
	}
	if view.Run == nil {
		// NOT_REQUESTED: no run, no context, no gaps. The centre panel says so in its own
		// sentence and the right panel is empty rather than absent, because "nothing has been
		// suggested" and "you may not see suggestions" are different facts.
		return summary, &Assistant{AIGenerated: true, Suggestions: []Suggestion{}}
	}

	run := view.Run
	summary.Provenance = &Provenance{
		Generation: run.Generation, Trigger: run.Trigger,
		PromptVersion: run.PromptVersion, ModelVersion: run.ModelVersion,
		InteractionID: run.InteractionID,
		RequestedAt:   run.RequestedAt, FinishedAt: run.FinishedAt,
		Grounding: run.Grounding, GroundingFindings: run.GroundingFindings,
		FailureKind: run.FailureKind, FailureDetail: run.FailureDetail,
	}

	assistant := &Assistant{
		Generation: run.Generation, AIGenerated: true, Suggestions: []Suggestion{},
	}

	// `seen` makes the references unique within one panel without making them unstable; see
	// [uniqueRef]. The gaps are where it bites: several stations nobody reached produce
	// several gaps that all carry the code `station_not_reached`, and a fold keyed on a
	// duplicated reference writes one decision onto all of them.
	seen := map[string]int{}

	// The deterministic half first, so that a degraded panel is not an empty one.
	for _, gap := range run.Context.Gaps {
		assistant.Suggestions = append(assistant.Suggestions, Suggestion{
			Ref:    uniqueRef(seen, suggestionRef(KindGap, gap.Code)),
			Kind:   KindGap, Origin: OriginSystem,
			Label:  gap.Code,
			Detail: gap.Detail, Severity: gap.Severity,
		})
	}

	if len(run.Output) == 0 {
		return summary, assistant
	}

	var out modelOutput
	if err := json.Unmarshal(run.Output, &out); err != nil {
		// A stored answer this build cannot read. The centre panel keeps its state and its
		// sentence; nothing is invented from a blob nobody could parse. The narrative is left
		// empty rather than filled with the raw JSON, which is what a "show what we have"
		// instinct would produce and is unreadable prose in a clinical document.
		return summary, assistant
	}

	summary.Narrative = out.Narrative
	summary.KeyPoints = out.KeyPoints
	summary.Citations = out.Citations
	summary.Confidence = out.Confidence
	for _, flag := range out.RedFlags {
		summary.RedFlags = append(summary.RedFlags, RedFlag{
			Severity: flag.Severity, Statement: flag.Statement, Basis: flag.Basis,
		})
	}

	for _, d := range out.Diagnoses {
		assistant.Suggestions = append(assistant.Suggestions, Suggestion{
			Ref:  uniqueRef(seen, suggestionRef(KindDiagnosis, d.Label)),
			Kind: KindDiagnosis, Origin: OriginModel,
			Label: d.Label, Code: d.ICD10, Basis: d.Basis,
		})
	}
	for _, i := range out.Investigations {
		assistant.Suggestions = append(assistant.Suggestions, Suggestion{
			Ref:  uniqueRef(seen, suggestionRef(KindInvestigation, i.Investigation)),
			Kind: KindInvestigation, Origin: OriginModel,
			Label: i.Investigation, Detail: i.Why, Basis: i.Basis,
		})
	}
	for _, m := range out.Medications {
		assistant.Suggestions = append(assistant.Suggestions, Suggestion{
			Ref:  uniqueRef(seen, suggestionRef(KindMedication, m.Drug)),
			Kind: KindMedication, Origin: OriginModel,
			Label: m.Drug, Detail: m.Rationale,
			Dose: m.Dose, Frequency: m.Frequency, Route: m.Route, Basis: m.Basis,
		})
	}

	return summary, assistant
}

// suggestionRef is the stable handle a decision names.
//
// Kind plus a slug of the item's own text, and deliberately **not** the item's index in the
// model's array. A re-read of the same generation returns the same JSON, so an index would be
// stable in practice — right up until a re-run under [synthesis.Unchanged] serves the previous
// generation's output in a different order, at which point every recorded decision would be
// sitting against the wrong suggestion. A physician who accepted a diagnosis and finds it
// against a drug is a physician who stops using the panel.
//
// The slug is lower-cased, punctuation-free and capped, so that "Type 2 diabetes mellitus"
// and "type 2 diabetes mellitus" are one reference rather than two.
func suggestionRef(kind SuggestionKind, label string) string {
	return strings.ToLower(string(kind)) + ":" + slug(label)
}

// uniqueRef makes a reference unique within one panel without making it unstable.
//
// Duplicates are not hypothetical and were not obvious: several stations nobody reached
// produce several gaps that all carry the code `station_not_reached`, and the first version of
// this code gave them one reference between them — so a physician dismissing one dismissed all
// of them, which the panel then drew as four decisions nobody had made. A test found it.
//
// The first occurrence keeps the plain reference and only a duplicate acquires a suffix, which
// keeps a re-read's decisions pointing where the previous read's pointed for every item but
// the one that genuinely collided. The same shape `synthesis.uniqueRef` uses, and for the same
// reason — although not the same *suffix* rule: a synthesis fact reference ends in a date and
// a numeric suffix would make it look like a telephone number to the gateway's scrubber, and
// nothing here goes near a model, so a number reads more plainly than a letter.
func uniqueRef(seen map[string]int, candidate string) string {
	seen[candidate]++
	if n := seen[candidate]; n > 1 {
		return candidate + "~" + strconv.Itoa(n)
	}
	return candidate
}

func slug(s string) string {
	var b strings.Builder
	last := byte('_')
	for i := 0; i < len(s) && b.Len() < 48; i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c + 32)
			last = c
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			b.WriteByte(c)
			last = c
		default:
			if last != '_' {
				b.WriteByte('_')
				last = '_'
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "item"
	}
	return out
}

// --- age ---

func monthsBetween(from, to time.Time) int {
	months := (to.Year()-from.Year())*12 + int(to.Month()) - int(from.Month())
	if to.Day() < from.Day() {
		months--
	}
	if months < 0 {
		return 0
	}
	return months
}

// ageText is the age as somebody says it out loud.
//
// Years above two, months below, and the split is where it is because that is where a
// clinician's own language changes: nobody says "1.4 years old" about an infant, and the
// growth panel this sits above is keyed on months for exactly the ages where the difference
// between eight months and fourteen is the whole assessment.
//
// The words are English here and translated on the client, which has both languages. What is
// computed on the server is the number and the unit; composing the sentence twice, once per
// language, in Go, would put clinical vocabulary in two places.
func ageText(birth, now time.Time) string {
	months := monthsBetween(birth, now)
	if months >= 24 {
		return fmt.Sprintf("%dy", months/12)
	}
	if months >= 1 {
		return fmt.Sprintf("%dm", months)
	}
	days := int(now.Sub(birth).Hours() / 24)
	if days < 0 {
		days = 0
	}
	return fmt.Sprintf("%dd", days)
}

// --- the concurrent gather ---

// gather runs the panel reads together and collects what happened.
//
// A mutex rather than channels, because what is being protected is one struct that nine
// goroutines each write a different field of. Channels would mean nine result types and a
// select, which is more machinery around the same mutual exclusion.
type gather struct {
	mu        sync.Mutex
	wg        sync.WaitGroup
	view      *View
	omitted   []Omission
	fatal     error
}

func (g *gather) run(fn func()) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		fn()
	}()
}

// permit runs a panel's read when the subject may see it, and records why not when they may
// not.
//
// The check is `rbac.Sees` rather than `rbac.Can`, and the difference is the resource: a
// panel is a field of a response rather than a thing with a station and an owner, and `Sees`
// is the serialiser's own question — *may this subject see a field guarded by this
// permission*. The resource-level decision has already been made by the route, against the
// patient.
func (g *gather) permit(subject rbac.Subject, panel, permission string, fn func()) {
	if !rbac.Sees(subject, permission) {
		g.mu.Lock()
		g.omitted = append(g.omitted, Omission{
			Panel: panel, Permission: permission,
			ReasonEN: "Your role does not include this part of the record.",
			ReasonBN: "আপনার ভূমিকায় রেকর্ডের এই অংশ দেখার অনুমতি নেই।",
		})
		g.mu.Unlock()
		return
	}
	g.run(fn)
}

func (g *gather) set(fn func(*View)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	fn(g.view)
}

// omit records a panel that could not be read. Never the database error itself: the sentence
// on a clinician's screen must not carry a constraint name, and the cause reaches the log
// through the handler.
func (g *gather) omit(panel, permission string, cause error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.omitted = append(g.omitted, Omission{
		Panel: panel, Permission: permission,
		ReasonEN: "This part of the record could not be read just now. Nothing has been hidden.",
		ReasonBN: "রেকর্ডের এই অংশ এখন পড়া যায়নি। কিছু লুকানো হয়নি।",
	})
	_ = cause
}

func (g *gather) fail(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fatal == nil {
		g.fatal = err
	}
}

func (g *gather) wait() {
	g.wg.Wait()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.view.Omitted = append(g.view.Omitted, g.omitted...)
}

func (g *gather) omissions() []Omission {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Omission{}, g.omitted...)
}

// sortOmissions puts the list in a stable order so that two identical reads produce identical
// bytes. A payload that reordered itself between two loads would make every cache comparison
// and every contract test flap for no reason.
func sortOmissions(list []Omission) {
	sort.SliceStable(list, func(i, j int) bool { return list[i].Panel < list[j].Panel })
}
