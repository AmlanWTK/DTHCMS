package synthesis

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/ai"
	"github.com/AmlanWTK/DTHCMS/backend/internal/allergy"
	"github.com/AmlanWTK/DTHCMS/backend/internal/assessment"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/exercise"
	"github.com/AmlanWTK/DTHCMS/backend/internal/history"
	"github.com/AmlanWTK/DTHCMS/backend/internal/nutrition"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// The deterministic assembly (§10.4 A1, *"deterministic assembly of a structured clinical
// context"*).
//
// [Assemble] is a pure function of [Raw] and a clock. No database, no network, no model. Everything
// in this file could be run on a plane, and the test for it is a table of inputs and expected
// contexts rather than a fixture database — which is what "the assembly step should be
// independently testable without any model at all" has to mean if it means anything.

// TrendCodes are the measurements whose trajectory a physician reads rather than whose latest value
// they read.
//
// A short list on purpose. Five values of nine codes is forty-five numbers, which is already more
// than a one-page summary can carry, and a model given every code's history writes about whichever
// ones it happened to notice. These nine are the ones an endocrine follow-up turns on: control,
// weight, pressure, renal function and lipids.
//
// Ordered rather than a map, because the order they appear in the payload is the order the model
// tends to write about them, and control before cholesterol is the right order for this clinic.
//
// **What is missing and why:** there is no TSH, no free T4 and no anti-TPO, because
// `core.observation_code` has no thyroid analytes in it yet — a real gap for a clinic whose name
// contains the word, and one this checkpoint cannot close without inventing reference data. It is
// recorded in `docs/synthesis.md` rather than papered over with a hand-rolled code here.
var TrendCodes = []string{
	"HBA1C",
	"GLUCOSE_FASTING",
	"BODY_WEIGHT",
	"BMI",
	"BP_SYSTOLIC",
	"BP_DIASTOLIC",
	"CREATININE",
	"EGFR",
	"CHOL_LDL",
}

// TrendDepth is how many points of each trend the model is shown. Five, which is §8's own number
// for the HbA1c sparkline: enough to see a direction, few enough that the model quotes the series
// rather than summarising it away.
const TrendDepth = 5

// paediatricCeilingMonths is where the growth reference stops: twenty years, which is
// `clinical.Service.GrowthFor`'s own boundary. Repeated here rather than imported because the
// clinical module keeps it as a bare comparison; the day either moves, the pair is a two-line
// change and this comment is where the second line is.
const paediatricCeilingMonths = 240

// ConsultationStation is the station the summary has to be ready before. §7.1: *"always finishing
// before the patient reaches Step 9"*, and step 9 is this one.
const ConsultationStation = "STN_CONSULTATION"

// Raw is everything the assembler was given, exactly as the station modules returned it.
//
// # Why there is no patient record in here
//
// There is no field on this struct that could hold a name, a phone number or a date of birth, and
// that is the point: [Assemble] is the function that builds the payload, and a function that cannot
// see an identifier cannot forward one. The subject's identifiers travel separately, on the
// gateway's [ai.Subject], where CP70 strikes them out and puts them back.
//
// [Demographics] is the whole of what is left: an age in months and a sex, which is what D-08
// permits and what [R-06]'s percentiles need.
type Raw struct {
	// Location is the clinic's calendar. Dates in a context are clinic days, not UTC days.
	Location *time.Location

	Demographics Demographics

	Visit      visit.Visit
	Encounters []visit.Encounter
	Planned    []visit.PlannedStation

	// Current is the patient's live value of every code, newest per code.
	Current []clinical.Observation
	// Trends is the history of each of [TrendCodes], newest first as the store returns it.
	Trends map[string][]clinical.Observation
	// Ranges is the reference-range table, used to flag a value as outside normal. The whole
	// table, filtered here, because the store's read is a catalogue read and caching it per
	// patient would be a cache nobody invalidates.
	Ranges []clinical.ReferenceRange
	// Units is the unit catalogue, and it decides two things that a summary reads badly without:
	// how a unit is *written* and to how many decimals its values are shown.
	//
	// Values are stored in UCUM, which is right for a database and wrong on a page: a physician
	// reading "196 mm[Hg]" is reading a machine's spelling of their own unit. And a stored HbA1c is
	// a float — "54.099 mmol/mol" claims a precision no assay has. `core.unit` already carries both
	// answers, per unit, as the clinic's own decision; nothing here re-decides them.
	//
	// Empty is survivable: values fall back to the UCUM code and three decimals, which is ugly and
	// correct rather than absent.
	Units []clinical.Unit

	Growth clinical.Growth

	History   []history.Item
	Allergies allergy.State
	Alerts    []clinical.Alert

	Lifestyle *assessment.Scoring
	Nutrition *nutrition.Recall
	Exercise  *exercise.Assessment
	Plan      *exercise.Plan

	// PriorVisits are earlier closed visits, newest first.
	PriorVisits []visit.Visit
}

// number3 is [number] with the signature the shape's formatter takes; the unit is ignored, for the
// reason stated inside [display].
func number3(v float64, _ string) string { return number(v) }

// display turns the unit catalogue into the two lookups the assembler needs.
//
// A closure rather than a method, because it is derived from [Raw] and belongs to one assembly: two
// contexts assembled from different unit tables must not share a cache.
func display(units []clinical.Unit) (func(string) string, func(float64, string) string) {
	written := make(map[string]string, len(units))
	for _, unit := range units {
		if unit.DisplayEN != "" {
			written[unit.Code] = unit.DisplayEN
		}
	}
	writtenAs := func(code string) string {
		// UCUM spells "dimensionless" as `1`, which is correct and reads as nonsense: "waist-hip
		// ratio 0.879 1". A ratio has no unit and the payload says so by having none.
		if code == "1" || code == "" {
			return ""
		}
		if out, known := written[code]; known {
			return out
		}
		return code
	}
	// **The value is not rounded to the unit's decimals, and the reason is a defect in the schema
	// rather than a preference here.** `core.unit.decimals` is per *unit*, and HbA1c and oxygen
	// saturation share `%`: the column says nought decimals, which is right for a saturation of 97
	// and turns an HbA1c of 8.2 into 8. Display precision is a property of the *code*, and
	// `core.observation_code` does not carry one.
	//
	// So values keep [number]'s three trimmed decimals — exact, traceable, and occasionally uglier
	// than a physician would write ("33.012 kg/m²"). Inventing a per-code precision table in this
	// file would be exactly the second opinion about clinical display that everything else here
	// refuses to hold. The column belongs on the code registry; it is recorded in
	// `docs/synthesis.md` as an open item, and `core.unit.decimals` is deliberately not read here
	// rather than read and half-applied.
	return writtenAs, number3
}

// shape is the scaffolding one assembly shares: the calendar, the reference bookkeeping, the unit
// catalogue's two answers, and the place facts go.
//
// A struct rather than five parameters on every section, because five parameters is where the sixth
// gets added to only four of them.
type shape struct {
	loc *time.Location
	// seen counts reference candidates so a duplicate can take a suffix; minted records which
	// reference belongs to which observation.
	seen   map[string]int
	minted map[uuid.UUID]string
	// unit writes a unit the way a clinician does; format writes a value to that unit's decimals.
	unit   func(string) string
	format func(float64, string) string
	add    func(Fact)
}

// Assemble builds the context. Pure: same [Raw], same [Context], every time.
func Assemble(raw Raw, now time.Time) Context {
	loc := raw.Location
	if loc == nil {
		loc = visit.Dhaka
	}

	ctx := Context{
		AssemblerVersion: AssemblerVersion,
		AssembledAt:      now.In(loc).Format(time.RFC3339),
		Demographics:     raw.Demographics,
		Facts:            []Fact{},
		History:          []HistoryItem{},
		Current:          []Measurement{},
		Trends:           []Trend{},
		Alerts:           []AlertContext{},
		PriorVisits:      []PriorVisit{},
		Gaps:             []Gap{},
	}

	writtenAs, valueAs := display(raw.Units)
	// `minted` records which reference belongs to which *observation*, so a value that is both the
	// latest and the last point of a series is one fact with one reference.
	sh := &shape{
		loc: loc, seen: map[string]int{}, minted: map[uuid.UUID]string{},
		unit: writtenAs, format: valueAs,
		add: func(f Fact) { ctx.Facts = append(ctx.Facts, f) },
	}

	ctx.Visit = assembleVisit(raw, sh)
	ctx.Allergies = assembleAllergies(raw, sh)
	ctx.History = assembleHistory(raw, sh)
	ctx.Current = assembleCurrent(raw, sh)
	ctx.Trends = assembleTrends(raw, sh)
	ctx.Growth = assembleGrowth(raw, sh)
	ctx.Lifestyle = assembleLifestyle(raw, sh)
	ctx.Nutrition = assembleNutrition(raw, sh)
	ctx.Exercise = assembleExercise(raw, sh)
	ctx.Alerts = assembleAlerts(raw, sh)
	ctx.PriorVisits = assemblePriorVisits(raw, sh)
	ctx.Gaps = findGaps(raw, ctx, now, loc)

	return ctx
}

// --- the visit and its journey ---

func assembleVisit(raw Raw, sh *shape) VisitContext {
	out := VisitContext{
		Type:      string(raw.Visit.VisitType),
		ClinicDay: day(raw.Visit.ClinicDay, sh.loc),
		Complaint: withheld(raw.Visit.ChiefComplaint),
		Stations:  []StationState{},
	}

	// The last finished encounter per station wins. A patient sent back to a station and seen
	// again has two encounters, and the second is what happened.
	byStation := map[string]visit.Encounter{}
	for _, enc := range raw.Encounters {
		existing, had := byStation[enc.StationCode]
		if !had || enc.StartedAt.After(existing.StartedAt) {
			byStation[enc.StationCode] = enc
		}
	}

	planned := append([]visit.PlannedStation(nil), raw.Planned...)
	sort.Slice(planned, func(i, j int) bool { return planned[i].Position < planned[j].Position })

	complete := true
	for _, station := range planned {
		state := StationState{
			Code: station.StationCode, Position: station.Position,
			Required: station.Required, Status: "not_reached",
		}
		if enc, touched := byStation[station.StationCode]; touched {
			switch enc.Status {
			case visit.Finished:
				state.Status = "done"
			case visit.Bounced:
				state.Status = "bounced"
			default:
				state.Status = "in_progress"
			}
			state.Outcome = enc.Outcome
		}
		out.Stations = append(out.Stations, state)

		// Completeness is about the stations *before* the consultation, and only the required
		// ones. The definition is the station sequence's own — `core.station_sequence` per visit
		// type, with its `required` column — rather than a list in Go, because that table is the
		// clinic's operational decision and a second opinion about it in code would drift from it
		// the first time somebody reordered a morning.
		if station.StationCode == ConsultationStation {
			break
		}
		if station.Required && state.Status != "done" {
			complete = false
		}
	}
	out.Complete = complete

	if out.Complaint != "" {
		reference := uniqueRef(sh.seen, ref("visit", "complaint", out.ClinicDay))
		sh.add(Fact{
			Ref: reference, Kind: "visit", Label: "Chief complaint",
			On: out.ClinicDay, Note: out.Complaint,
		})
	}
	return out
}

// --- allergies ---

func assembleAllergies(raw Raw, sh *shape) AllergyContext {
	status := raw.Allergies.Status
	if strings.TrimSpace(status) == "" {
		// An empty status is "nobody has said". CP54's whole argument is that this is not the same
		// as "none", and the word the model reads has to preserve that: `unknown` produces "allergy
		// status has not been established", which is a sentence a physician acts on, and `none`
		// produces silence, which is a sentence they cannot act on because it was never written.
		status = "unknown"
	}
	out := AllergyContext{Status: status, Items: []AllergyItem{}}

	for _, item := range raw.Allergies.Allergies {
		substance := item.DisplayEN
		if strings.TrimSpace(substance) == "" {
			substance = withheld(item.Said)
		}
		if strings.TrimSpace(substance) == "" {
			substance = "unnamed substance"
		}
		reference := uniqueRef(sh.seen, ref("allergy", slug(substance), ""))
		entry := AllergyItem{
			Ref: reference, Substance: substance, Code: item.Code,
			Reaction: item.ReactionEN, Severity: item.Severity,
			Certainty: item.Certainty, IsEmergency: item.IsEmergency,
			On: day(item.RecordedAt, sh.loc),
		}
		out.Items = append(out.Items, entry)

		note := entry.Reaction
		if entry.Severity != "" {
			note += ", " + entry.Severity
		}
		if entry.IsEmergency {
			note += ", emergency reaction"
		}
		sh.add(Fact{
			Ref: reference, Kind: "allergy", Label: "Allergy: " + substance,
			On: entry.On, Note: note,
		})
	}

	// The status itself is a fact with a reference, so that a sentence claiming the patient has no
	// known allergies has something to cite — and so that CP72 can tell the difference between a
	// grounded claim and an assumption dressed as one.
	reference := uniqueRef(sh.seen, ref("allergy", "status", ""))
	sh.add(Fact{Ref: reference, Kind: "allergy", Label: "Allergy status", Value: status})
	return out
}

// --- history ---

func assembleHistory(raw Raw, sh *shape) []HistoryItem {
	out := make([]HistoryItem, 0, len(raw.History))
	for _, item := range raw.History {
		if strings.EqualFold(item.Status, "removed") {
			continue
		}
		label := item.DisplayEN
		if strings.TrimSpace(label) == "" {
			label = withheld(item.Said)
		}
		if strings.TrimSpace(label) == "" {
			continue
		}
		on := item.OnsetOn
		if on == "" {
			on = day(item.RecordedAt, sh.loc)
		}
		reference := uniqueRef(sh.seen, ref("history", slug(item.Kind+" "+label), ""))

		entry := HistoryItem{
			Ref: reference, Kind: item.Kind, Label: label, Code: item.Code,
			Said: withheld(item.Said), Relation: item.Relation,
			Severity: item.Severity, Dose: withheld(item.Dose),
			Confirmed: item.ConfirmedAt != nil, On: on,
		}
		if item.DurationDays != nil {
			entry.Duration = durationText(*item.DurationDays)
		}
		out = append(out, entry)

		note := item.Kind
		if entry.Relation != "" {
			note += ", " + entry.Relation
		}
		if entry.Duration != "" {
			note += ", " + entry.Duration
		}
		if !entry.Confirmed {
			// Said aloud in the fact rather than left as a boolean the model may not read. An
			// unconfirmed item is the patient's recollection from a previous visit, and a summary
			// that presents it as current is asserting something nobody checked today.
			note += ", not confirmed at this visit"
		}
		sh.add(Fact{Ref: reference, Kind: "history", Label: label, On: on, Note: note})
	}
	return out
}

// --- measurements ---

func assembleCurrent(raw Raw, sh *shape) []Measurement {
	// Alerts index the observations that were dangerous. The flag is carried across rather than
	// recomputed: the critical bands live in `core.critical_rule` and a second opinion about them
	// in this file would be a second set of thresholds nobody knows exists.
	critical := map[string]bool{}
	for _, alert := range raw.Alerts {
		critical[alert.ObservationID.String()] = true
	}

	// **The newest active value per code, and only that.**
	//
	// `clinical.Store.ForPatient` returns every active observation newest-first, and its own note
	// says the caller takes the first row for each code — a patient with five years of follow-up has
	// forty active rows and a dozen distinct codes. Feeding all forty to the model as
	// "current measurements" produced exactly what it sounds like: a narrative that opened with five
	// temperatures on five different dates before it reached the blood pressure. The trend sections
	// are where a series belongs, and they carry the change computed.
	//
	// The tie-break is the store's ordering, which is `effective_at DESC, global_seq DESC` — the
	// ledger's own sequence rather than anything decided here, because two values of one code can
	// share an effective time and picking arbitrarily between them is how a plausible-looking wrong
	// number reaches a page.
	newest := map[string]clinical.Observation{}
	order := make([]string, 0, len(raw.Current))
	for _, obs := range raw.Current {
		if obs.Status != clinical.Active {
			continue
		}
		if existing, seen := newest[obs.Code]; seen {
			if obs.EffectiveAt.After(existing.EffectiveAt) {
				newest[obs.Code] = obs
			}
			continue
		}
		newest[obs.Code] = obs
		order = append(order, obs.Code)
	}
	current := make([]clinical.Observation, 0, len(order))
	for _, code := range order {
		current = append(current, newest[code])
	}
	sort.SliceStable(current, func(i, j int) bool {
		if current[i].Category != current[j].Category {
			return categoryOrder(current[i].Category) < categoryOrder(current[j].Category)
		}
		return current[i].Code < current[j].Code
	})

	out := make([]Measurement, 0, len(current))
	for _, obs := range current {
		value, unit := sh.valueOf(obs)
		if value == "" {
			continue
		}
		on := day(obs.EffectiveAt, sh.loc)
		reference := uniqueRef(sh.seen, ref("obs", strings.ToLower(obs.Code), on))
		sh.minted[obs.ID] = reference

		measurement := Measurement{
			Ref: reference, Code: obs.Code, Label: labelOf(obs.Code),
			Value: value, Unit: unit, On: on, Source: string(obs.Source),
		}
		switch {
		case critical[obs.ID.String()]:
			measurement.Flag = "critical"
		case obs.ImplausibleConfirmed:
			// Confirmed-implausible is worth telling the model about: somebody was warned this
			// value was outside its band and said it was right anyway. It is a real value with a
			// caveat, and a summary that quoted it without the caveat would be more confident than
			// the record.
			measurement.Flag = "implausible_confirmed"
		case outsideRange(obs, raw.Ranges, raw.Demographics):
			measurement.Flag = "outside_reference_range"
		}
		out = append(out, measurement)

		sh.add(Fact{
			Ref: reference, Kind: "observation", Label: measurement.Label,
			Value: value, Unit: unit, On: on, Note: measurement.Flag,
		})
	}
	return out
}

func assembleTrends(raw Raw, sh *shape) []Trend {
	out := make([]Trend, 0, len(TrendCodes))
	for _, code := range TrendCodes {
		rows := raw.Trends[code]
		// Only active values. A corrected reading is not part of a trajectory — it never happened
		// — and a superseded one is, because it was right when it was taken.
		points := make([]clinical.Observation, 0, len(rows))
		for _, obs := range rows {
			if obs.Value == nil || obs.Status == clinical.Corrected {
				continue
			}
			points = append(points, obs)
		}
		if len(points) < 2 {
			// One point is not a trend, and a "trend" with one point invites a sentence about a
			// direction that does not exist. The value itself is already in `current_measurements`.
			continue
		}
		sort.Slice(points, func(i, j int) bool { return points[i].EffectiveAt.Before(points[j].EffectiveAt) })
		if len(points) > TrendDepth {
			points = points[len(points)-TrendDepth:]
		}

		trend := Trend{Code: code, Label: labelOf(code), Unit: sh.unit(points[0].Unit)}
		for _, obs := range points {
			on := day(obs.EffectiveAt, sh.loc)
			// The same reference the current-measurement section produced for *this observation*,
			// deliberately: a value that is both the latest and the last point of the trend is one
			// fact with one reference, not two the model could cite inconsistently.
			//
			// Keyed on the observation's id and not on the reference string. Two HbA1c results on
			// one day — a repeat after a suspicious first reading, which is ordinary — produce the
			// same candidate reference and are not the same fact; matching on the string merged
			// them, and one of the two values disappeared from the payload entirely.
			reference, already := sh.minted[obs.ID]
			if !already {
				reference = uniqueRef(sh.seen, ref("obs", strings.ToLower(obs.Code), on))
				sh.minted[obs.ID] = reference
				sh.add(Fact{
					Ref: reference, Kind: "observation", Label: trend.Label,
					Value: sh.format(*obs.Value, obs.Unit), Unit: sh.unit(obs.Unit), On: on,
				})
			}
			trend.Points = append(trend.Points, TrendPoint{
				Ref: reference, On: on, Value: sh.format(*obs.Value, obs.Unit),
			})
		}

		first, last := points[0], points[len(points)-1]
		days := int(last.EffectiveAt.Sub(first.EffectiveAt).Hours() / 24)
		changeRef := uniqueRef(sh.seen, ref("change", strings.ToLower(code), day(last.EffectiveAt, sh.loc)))
		delta := *last.Value - *first.Value
		trend.Change = &TrendChange{
			Ref: changeRef, From: sh.format(*first.Value, first.Unit),
			To:    sh.format(*last.Value, last.Unit),
			Delta: sh.signed(delta, last.Unit), OverDays: days,
		}
		// The subtraction is done here, not by the model. A language model asked to subtract two
		// numbers is usually right, and "usually" is the problem: the error is invisible, plausible,
		// and in a clinical sentence.
		sh.add(Fact{
			Ref: changeRef, Kind: "change", Label: trend.Label + " change",
			Value: sh.signed(delta, last.Unit), Unit: trend.Unit, On: day(last.EffectiveAt, sh.loc),
			Note: "over " + durationText(days),
		})
		out = append(out, trend)
	}
	return out
}

// --- growth [R-06] ---

func assembleGrowth(raw Raw, sh *shape) *GrowthSummary {
	// An adult gets no growth section at all, not even a note saying why. "Too old for a growth
	// reference" is true of a fifty-seven-year-old and is not information about them: a paediatric
	// section on every adult's summary is a line the physician learns to skip, and a line they learn
	// to skip is one they will skip on the child who needed it. The boundary is the clinical
	// module's own — twenty years, the top of the reference — rather than a second opinion here.
	if raw.Demographics.AgeMonths > paediatricCeilingMonths {
		return nil
	}
	growth := raw.Growth
	if !growth.Applicable {
		if growth.Note == "" {
			return nil
		}
		// For a child, a note with no percentiles is worth sending: "nothing measured yet" is why
		// the section is empty, and a section that simply vanished would leave the model free to
		// conclude the child was measured and found normal.
		return &GrowthSummary{Applicable: false, Note: growth.Note}
	}

	out := &GrowthSummary{Applicable: true, Indicators: []GrowthReading{}}
	for _, indicator := range clinical.Indicators {
		reading, present := growth.Current[indicator]
		if !present {
			continue
		}
		on := day(reading.EffectiveAt, sh.loc)
		reference := uniqueRef(sh.seen, ref("growth", strings.ToLower(string(indicator)), on))
		out.Standard, out.StandardVersion = reading.Standard, reading.StandardVersion

		entry := GrowthReading{
			Ref: reference, Indicator: string(indicator), Label: growthLabel(indicator),
			Value: sh.format(reading.Value, reading.Unit), Unit: sh.unit(reading.Unit),
			// Two decimals on the z-score and one on the centile. Three decimals of a percentile is
			// noise dressed as precision — "the 53.747th centile" invites a reader to believe the
			// number is that good, and it is a measurement taken with a stadiometer.
			Z: rounded(reading.Z, 2), Percentile: rounded(reading.P, 1), On: on,
		}
		// Velocity comes from the trajectory the clinical module already computed, including its
		// rule about intervals too short to mean anything. Recomputing it here would be the same
		// arithmetic, second-hand, and the two would disagree the first time either changed.
		for _, point := range growth.History[indicator] {
			if point.EffectiveAt.Equal(reading.EffectiveAt) && point.Velocity != nil {
				entry.Velocity = sh.signed(*point.Velocity, reading.Unit) + " " + sh.unit(reading.Unit) + "/year"
				entry.StandardChanged = point.StandardChanged
			}
		}
		out.Indicators = append(out.Indicators, entry)

		note := "z " + entry.Z + ", " + entry.Percentile + "th centile (" + reading.Standard + ")"
		if entry.Velocity != "" {
			note += ", " + entry.Velocity
		}
		if entry.StandardChanged {
			note += ", scored against a different reference from the previous reading"
		}
		sh.add(Fact{
			Ref: reference, Kind: "growth", Label: entry.Label,
			Value: entry.Value, Unit: entry.Unit, On: on, Note: note,
		})

		// §7.1 names one flag explicitly: the ≥95th-percentile childhood obesity flag [R-06]. It
		// is computed from the percentile the clinical module produced rather than from a BMI
		// threshold, because a BMI cut-off is an adult instrument and applying one to a child is
		// the error this whole section exists to avoid.
		if indicator == clinical.BMIForAge {
			switch {
			case reading.P >= 95:
				out.ObesityFlag = "bmi_for_age_at_or_above_95th_centile"
			case reading.P >= 85:
				out.ObesityFlag = "bmi_for_age_at_or_above_85th_centile"
			case reading.P < 3:
				out.ObesityFlag = "bmi_for_age_below_3rd_centile"
			}
			if out.ObesityFlag != "" {
				flagRef := uniqueRef(sh.seen, ref("growth", "flag", on))
				sh.add(Fact{
					Ref: flagRef, Kind: "growth", Label: "Paediatric BMI flag",
					Value: out.ObesityFlag, On: on,
				})
			}
		}
	}
	if len(out.Indicators) == 0 {
		return nil
	}
	return out
}

// --- the lifestyle, nutrition and exercise stations ---

func assembleLifestyle(raw Raw, sh *shape) *LifestyleContext {
	if raw.Lifestyle == nil {
		return nil
	}
	out := &LifestyleContext{
		Assessed: orEmpty(raw.Lifestyle.Assessed),
		Missing:  orEmpty(raw.Lifestyle.Missing),
		Minimum:  raw.Lifestyle.Minimum,
	}
	if score := raw.Lifestyle.Score; score != nil && score.Value != nil {
		on := day(score.EffectiveAt, sh.loc)
		out.Ref = uniqueRef(sh.seen, ref("lifestyle", "score", on))
		out.Score = number(*score.Value)
		sh.add(Fact{
			Ref: out.Ref, Kind: "lifestyle", Label: "Lifestyle composite",
			Value: out.Score, On: on,
			Note: "from " + strings.Join(out.Assessed, ", "),
		})
	}
	return out
}

func assembleNutrition(raw Raw, sh *shape) *NutritionContext {
	if raw.Nutrition == nil || raw.Nutrition.Totals.Entries == 0 {
		return nil
	}
	totals := raw.Nutrition.Totals
	out := &NutritionContext{
		RecallDate: raw.Nutrition.RecallDate, Entries: totals.Entries,
		Kcal:    number(totals.Kcal),
		Protein: number(totals.Protein),
		Carb:    number(totals.Carb),
		Fat:     number(totals.Fat),
	}
	out.Ref = uniqueRef(sh.seen, ref("nutrition", "recall", out.RecallDate))
	sh.add(Fact{
		Ref: out.Ref, Kind: "nutrition", Label: "24-hour recall energy",
		Value: out.Kcal, Unit: "kcal", On: out.RecallDate,
		Note: "protein " + out.Protein + " g, carbohydrate " + out.Carb + " g, fat " + out.Fat + " g",
	})
	return out
}

func assembleExercise(raw Raw, sh *shape) *ExerciseContext {
	if raw.Exercise == nil && raw.Plan == nil {
		return nil
	}
	out := &ExerciseContext{Contraindications: []string{}, Asked: []string{}}
	if a := raw.Exercise; a != nil {
		out.WalksUnaided, out.WalkMinutes, out.JointPain = a.WalksUnaided, a.WalkMinutes, a.JointPain
		out.Contraindications = orEmpty(a.Contraindications)
		out.Asked = orEmpty(a.Asked)
	}
	if p := raw.Plan; p != nil {
		minutes := p.MinutesPerWeek
		out.PlanMinutesPerWeek = &minutes
		out.PlanItems = len(p.Items)
	}
	out.Ref = uniqueRef(sh.seen, ref("exercise", "assessment", ""))
	note := "contraindications: none recorded"
	if len(out.Contraindications) > 0 {
		// Named rather than counted. A contraindication is the one thing on this station's record
		// that must survive into the summary intact: §3 step 8's rule is that a plan must not
		// contain something contraindicated, and a physician amending the plan needs to know which.
		note = "contraindications: " + strings.Join(out.Contraindications, ", ")
	}
	value := ""
	if out.PlanMinutesPerWeek != nil {
		value = number(float64(*out.PlanMinutesPerWeek))
	}
	sh.add(Fact{
		Ref: out.Ref, Kind: "exercise", Label: "Exercise plan, minutes per week",
		Value: value, Unit: "min/week", Note: note,
	})
	return out
}

// --- alerts and prior visits ---

func assembleAlerts(raw Raw, sh *shape) []AlertContext {
	out := make([]AlertContext, 0, len(raw.Alerts))
	for _, alert := range raw.Alerts {
		on := day(alert.RaisedAt, sh.loc)
		reference := uniqueRef(sh.seen, ref("alert", strings.ToLower(alert.Code), on))
		// The registry's display name where the alert carries one, and this module's own table where
		// it does not — a red flag reading "BP_SYSTOLIC 196" makes whoever reads it look the code
		// up, and the moment it matters is not a moment for lookups.
		label := alert.DisplayEN
		if label == "" {
			label = labelOf(alert.Code)
		}
		entry := AlertContext{
			Ref: reference, Label: label,
			Value: sh.format(alert.Value, alert.Unit), Unit: sh.unit(alert.Unit),
			Breached: alert.Breached, Threshold: sh.format(alert.Threshold, alert.Unit),
			Status: alert.Status, Action: alert.ActionEN, On: on,
		}
		out = append(out, entry)
		sh.add(Fact{
			Ref: reference, Kind: "alert", Label: "Critical value: " + label,
			Value: entry.Value, Unit: entry.Unit, On: on,
			Note: alert.Breached + " threshold " + entry.Threshold + ", " + alert.Status,
		})
	}
	return out
}

func assemblePriorVisits(raw Raw, sh *shape) []PriorVisit {
	out := make([]PriorVisit, 0, len(raw.PriorVisits))
	for _, prior := range raw.PriorVisits {
		on := day(prior.ClinicDay, sh.loc)
		reference := uniqueRef(sh.seen, ref("visit", "prior", on))
		entry := PriorVisit{
			Ref: reference, On: on, Type: string(prior.VisitType),
			Complaint: withheld(prior.ChiefComplaint),
			Diagnoses: withheld(prior.Diagnoses),
			Plan:      withheld(prior.Plan),
		}
		if prior.NextReviewOn != nil {
			entry.ReviewDue = day(*prior.NextReviewOn, sh.loc)
		}
		out = append(out, entry)

		note := entry.Diagnoses
		if entry.ReviewDue != "" {
			note += "; review due " + entry.ReviewDue
		}
		sh.add(Fact{
			Ref: reference, Kind: "visit", Label: "Previous visit",
			On: on, Note: note,
		})
	}
	return out
}

// --- the gaps ---

// findGaps names what is missing.
//
// A language model shown a record with no HbA1c writes a summary that does not mention HbA1c, and
// the physician reads a confident page with a hole in it. Nothing about the model's training makes
// it notice an absence; noticing absences is what a deterministic pass is *for*, and it is the
// cheapest quality improvement available to this design.
//
// The rules are deliberately few and each is defensible on its own. A gap list of thirty entries is
// a gap list nobody reads, and P-7's fail-closed QA gate (CP83) is where "this file may not close"
// belongs — this is advice to a physician about to see a patient, not a blocking rule.
func findGaps(raw Raw, ctx Context, now time.Time, loc *time.Location) []Gap {
	gaps := []Gap{}

	for _, station := range ctx.Visit.Stations {
		if station.Code == ConsultationStation {
			break
		}
		if station.Required && station.Status != "done" {
			gaps = append(gaps, Gap{
				Code:     "station_not_completed",
				Detail:   "The patient has not completed " + stationName(station.Code) + ", which this visit type requires before the consultation.",
				Severity: "important",
			})
		}
	}

	if ctx.Allergies.Status == "unknown" {
		gaps = append(gaps, Gap{
			Code:     "allergy_status_unknown",
			Detail:   "Nobody has recorded whether this patient has allergies. This is not the same as having none.",
			Severity: "important",
		})
	}

	// The HbA1c rule is P-7's, softened to advice. A diabetic file that closes without an HbA1c
	// recorded or ordered is blocked at CP83; a physician about to walk into the room should be
	// told before it gets that far.
	if diabetic(ctx) && !measuredWithin(raw, "HBA1C", now, 365) {
		gaps = append(gaps, Gap{
			Code:     "no_hba1c_in_12_months",
			Detail:   "No HbA1c has been recorded in the last twelve months for a patient with a diabetes diagnosis in their history.",
			Severity: "important",
		})
	}

	if !hasCode(ctx, "BP_SYSTOLIC") {
		gaps = append(gaps, Gap{
			Code:     "no_blood_pressure",
			Detail:   "No blood pressure has been recorded for this patient.",
			Severity: "important",
		})
	}
	if !hasCode(ctx, "BODY_WEIGHT") {
		gaps = append(gaps, Gap{
			Code:     "no_weight",
			Detail:   "No weight has been recorded, so BMI and any weight trajectory are unavailable.",
			Severity: "note",
		})
	}
	if !measuredWithin(raw, "CREATININE", now, 365) && onMedication(ctx) {
		gaps = append(gaps, Gap{
			Code:     "no_renal_function",
			Detail:   "No creatinine in the last twelve months for a patient on regular medication; renal dosing cannot be checked.",
			Severity: "note",
		})
	}

	if len(raw.History) == 0 {
		gaps = append(gaps, Gap{
			Code:     "no_history_recorded",
			Detail:   "No medical history has been recorded for this patient at any visit.",
			Severity: "important",
		})
	}

	// Unconfirmed history is the gap station 4 exists to close, and it is invisible on a screen
	// that shows conditions as a list. Counting rather than naming: the list is already in the
	// context and the point here is that some of it is stale.
	unconfirmed := 0
	for _, item := range ctx.History {
		if !item.Confirmed {
			unconfirmed++
		}
	}
	if unconfirmed > 0 && len(ctx.History) > 0 {
		gaps = append(gaps, Gap{
			Code:     "history_not_confirmed",
			Detail:   "Some history items carried forward from earlier visits have not been confirmed as still true at this visit.",
			Severity: "note",
		})
	}

	return gaps
}

// --- small deterministic helpers ---

// withheld is the free-text gate.
//
// Any string that trips the gateway's own pattern list is withheld **whole** rather than scrubbed.
// Two reasons, and the second is the one that decides it. Scrubbing produces "husband will bring
// the report, call [NUMBER]", which a model reads around and sometimes narrates — so the redaction
// itself becomes content. And a note that contains a telephone number is a note somebody typed into
// the wrong field, which is a data-quality problem a summary should not launder.
//
// The cost is a lost sentence of clinical prose in the rare case where a real note contains a long
// number or an honorific. That is the right direction: `core.ai_synthesis.context` carries the same
// check as a constraint, so the alternative to withholding is a synthesis that cannot be stored at
// all.
func withheld(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	for _, pattern := range scrubPatterns {
		if pattern.Matches(trimmed) {
			return "[withheld: this note contains something that looks like an identifier]"
		}
	}
	return trimmed
}

// scrubPatterns is the shared list, compiled once.
//
// The *same* list the gateway minimises with and the same list `ops.pii_pattern` holds, rather than
// a third copy: CP70's argument for one list with three enforcements applies exactly as much to a
// fourth. `CompilePatterns` fails loudly on an expression it cannot read, and this package refuses
// to initialise rather than run with a scrubber that silently dropped a rule.
var scrubPatterns = func() []ai.Pattern {
	compiled, err := ai.CompilePatterns(ai.DefaultPatterns)
	if err != nil {
		panic("synthesis: the shared PHI pattern list does not compile: " + err.Error())
	}
	return compiled
}()

func (sh *shape) valueOf(obs clinical.Observation) (string, string) {
	switch {
	case obs.Value != nil:
		return sh.format(*obs.Value, obs.Unit), sh.unit(obs.Unit)
	case obs.ValueBool != nil:
		if *obs.ValueBool {
			return "yes", ""
		}
		return "no", ""
	case strings.TrimSpace(obs.ValueCode) != "":
		return obs.ValueCode, ""
	case strings.TrimSpace(obs.ValueText) != "":
		return withheld(obs.ValueText), ""
	}
	return "", ""
}

// outsideRange flags a value against the seeded reference ranges.
//
// Most specific match wins, which is the order `Store.Ranges` already returns them in: a range for
// this sex and age band beats a range for everybody. An unapproved range still flags — every seeded
// range is a proposal until Dr. Nahid signs it (CP49) — because "we have a band nobody has approved"
// is a better basis for a hint than no band at all, and the flag is a hint rather than an alert.
func outsideRange(obs clinical.Observation, ranges []clinical.ReferenceRange, who Demographics) bool {
	if obs.Value == nil {
		return false
	}
	years := float64(who.AgeMonths) / 12
	for _, r := range ranges {
		if r.Code != obs.Code {
			continue
		}
		if r.Sex != "" && !strings.EqualFold(r.Sex, who.Sex) {
			continue
		}
		if r.MinAgeYears != nil && years < *r.MinAgeYears {
			continue
		}
		if r.MaxAgeYears != nil && years >= *r.MaxAgeYears {
			continue
		}
		if r.Low != nil && *obs.Value < *r.Low {
			return true
		}
		if r.High != nil && *obs.Value > *r.High {
			return true
		}
		return false
	}
	return false
}

// diabetic reads the history rather than guessing from an HbA1c.
//
// Deliberately: a raised HbA1c is how somebody *becomes* diabetic, and a rule that inferred the
// diagnosis from the number would report "no HbA1c for a diabetic patient" as a gap for every
// patient who has never had one — which is every new patient, and the gap list would then be noise
// on the day it should be signal.
func diabetic(ctx Context) bool {
	for _, item := range ctx.History {
		text := strings.ToLower(item.Label + " " + item.Code + " " + item.Said)
		if strings.Contains(text, "diabet") || strings.Contains(text, "dm_type") {
			return true
		}
	}
	return false
}

func onMedication(ctx Context) bool {
	for _, item := range ctx.History {
		if strings.EqualFold(item.Kind, "medication") {
			return true
		}
	}
	return false
}

func hasCode(ctx Context, code string) bool {
	for _, m := range ctx.Current {
		if m.Code == code {
			return true
		}
	}
	return false
}

func measuredWithin(raw Raw, code string, now time.Time, days int) bool {
	cutoff := now.AddDate(0, 0, -days)
	for _, obs := range raw.Current {
		if obs.Code == code && obs.Status == clinical.Active && obs.EffectiveAt.After(cutoff) {
			return true
		}
	}
	for _, obs := range raw.Trends[code] {
		if obs.Status != clinical.Corrected && obs.EffectiveAt.After(cutoff) {
			return true
		}
	}
	return false
}

func categoryOrder(c clinical.Category) int {
	switch c {
	case clinical.Vital:
		return 0
	case clinical.Anthro:
		return 1
	case clinical.Derived:
		return 2
	case clinical.Lab:
		return 3
	case clinical.Exam:
		return 4
	case clinical.Screening:
		return 5
	}
	return 6
}

// rounded formats to a fixed number of decimals, trailing zeros trimmed.
//
// Used where the number is *computed* rather than measured: a z-score and a centile come out of the
// LMS transform with as many digits as a float64 has, and printing them all claims a precision the
// measurement behind them does not have. Measured values keep [number]'s three decimals, because
// those are the stored value and grounding compares strings.
func rounded(v float64, places int) string {
	s := strconv.FormatFloat(v, 'f', places, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	return s
}

// signed formats a change with its sign and to its unit's decimals. A delta reading
// "-0.163 kg/m2" says something the instrument cannot; "-0.2" says what changed.
func (sh *shape) signed(v float64, unit string) string {
	if v > 0 {
		return "+" + sh.format(v, unit)
	}
	return sh.format(v, unit)
}

func durationText(days int) string {
	switch {
	case days <= 0:
		return ""
	case days < 60:
		return plural(days, "day")
	case days < 730:
		return plural(days/30, "month")
	}
	return plural(days/365, "year")
}

func plural(n int, word string) string {
	out := number(float64(n)) + " " + word
	if n != 1 {
		out += "s"
	}
	return out
}

func orEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// labelOf is the English display name for a code.
//
// A table here rather than a database read, and this is the one place in the assembler where that
// is the right way round: the label is part of the *payload's* vocabulary, not the clinic's, and a
// label edited in the registry next year must not silently change what an eight-month-old stored
// context said the model was shown. Codes not in the table fall through to the code itself, which is
// ugly on a page and honest — and an ugly label is how somebody notices a code has been added here
// without being added to the assembler.
var codeLabels = map[string]string{
	"BODY_HEIGHT":     "Height",
	"BODY_WEIGHT":     "Weight",
	"WAIST_CIRC":      "Waist circumference",
	"HIP_CIRC":        "Hip circumference",
	"MID_ARM_CIRC":    "Mid-upper arm circumference",
	"BODY_FAT_PCT":    "Body fat",
	"BP_SYSTOLIC":     "Systolic blood pressure",
	"BP_DIASTOLIC":    "Diastolic blood pressure",
	"HEART_RATE":      "Pulse",
	"RESP_RATE":       "Respiratory rate",
	"BODY_TEMP":       "Temperature",
	"SPO2":            "Oxygen saturation",
	"GLUCOSE_FASTING": "Fasting plasma glucose",
	"GLUCOSE_RANDOM":  "Random plasma glucose",
	"HBA1C":           "HbA1c",
	"CREATININE":      "Serum creatinine",
	"CHOL_TOTAL":      "Total cholesterol",
	"CHOL_HDL":        "HDL cholesterol",
	"CHOL_LDL":        "LDL cholesterol",
	"TRIGLYCERIDE":    "Triglycerides",
	"BMI":             "Body mass index",
	"WHR":             "Waist-hip ratio",
	"BSA":             "Body surface area",
	"BMR":             "Basal metabolic rate",
	"EGFR":            "eGFR (CKD-EPI 2021)",
	"PACK_YEARS":      "Pack-years",
	"LIFESTYLE_RISK":  "Lifestyle composite",
	"ENERGY_INTAKE":   "Energy intake",
	"PROTEIN_INTAKE":  "Protein intake",
	"CARB_INTAKE":     "Carbohydrate intake",
	"FAT_INTAKE":      "Fat intake",
}

func labelOf(code string) string {
	if label, known := codeLabels[code]; known {
		return label
	}
	return code
}

var stationNames = map[string]string{
	"STN_REGISTRATION":  "registration",
	"STN_ANTHROPOMETRY": "anthropometry and screening",
	"STN_COUNSELING":    "counselling",
	"STN_HISTORY":       "medical history",
	"STN_EXAMINATION":   "clinical examination and vitals",
	"STN_RECORDS":       "medical records import",
	"STN_NUTRITION":     "nutrition assessment",
	"STN_EXERCISE":      "exercise assessment",
	"STN_CONSULTATION":  "the physician consultation",
	"STN_QA":            "quality assurance review",
	"STN_RX_EDUCATION":  "prescription education",
	"STN_FOLLOWUP":      "follow-up",
}

func stationName(code string) string {
	if name, known := stationNames[code]; known {
		return name
	}
	return code
}

func growthLabel(indicator clinical.Indicator) string {
	switch indicator {
	case clinical.HeightForAge:
		return "Height for age"
	case clinical.WeightForAge:
		return "Weight for age"
	case clinical.BMIForAge:
		return "BMI for age"
	}
	return string(indicator)
}
