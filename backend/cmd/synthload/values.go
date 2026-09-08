package main

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/synthetic"
)

// What the desk records, from what the generator invented.
//
// The generator's patient is a clinical shape — a diagnosis, a trajectory, a set of visits.
// A registration is an administrative one: a name, a number somebody can be telephoned on,
// an address in the district the clinic serves. Everything in this file is the translation
// between the two, and everything it invents is invented **here** rather than in the
// generator, because none of it is clinical and none of it was reviewed as clinical.

// upazilas are the ones that actually send patients to a clinic in Faridpur. A register whose
// addresses are all "Dhaka" is a register in which no catchment analysis means anything.
var upazilas = []string{
	"Faridpur Sadar", "Boalmari", "Alfadanga", "Madhukhali", "Bhanga",
	"Nagarkanda", "Charbhadrasan", "Sadarpur", "Saltha",
}

// relations are how a desk actually writes down an emergency contact. The *name* is left
// blank more often than not, which is not laziness on this command's part: a desk that has a
// number and a relationship writes those and moves the patient on, and a register in which
// every emergency contact is complete is a register that never exercises the half-filled case.
var relations = []string{"spouse", "son", "daughter", "brother", "mother", "neighbour"}

var (
	educations  = []string{"none", "primary", "secondary", "higher_secondary", "graduate", "madrasa"}
	occupations = []string{"agriculture", "day_labour", "business", "homemaker", "service_private",
		"service_government", "student", "retired"}
	incomes = []string{"under_10k", "10k_25k", "25k_50k", "50k_100k"}
	payers  = []string{"self", "family", "employer", "ngo"}
)

// registration turns one invented person into what a registration officer would have typed.
func (l *loader) registration(q synthetic.Patient) patient.Registration {
	// Continued from whatever the register already holds, not restarted at zero. See
	// loader.alreadyHere.
	serial := l.alreadyHere + l.tally.registered + l.tally.skipped

	sex := patient.SexFemale
	if q.Sex == synthetic.Male {
		sex = patient.SexMale
	}

	// The date of birth's *provenance* varies, because it is a field every growth chart and
	// every percentile depends on and a register in which every date came from the same place
	// would never exercise the distinction. A stated date is the common case in Bangladesh;
	// a document is the exception, and the validator insists a document carries an exact day.
	source := patient.SourcePatientStated
	if q.Urban && q.AgeYears >= 18 {
		source = patient.SourceNationalID
	}

	registration := patient.Registration{
		NameEN: q.Name.English, NameBN: q.Name.Bangla, Sex: sex,
		BirthDate:    q.DateOfBirth,
		DOBPrecision: patient.PrecisionDay,
		DOBSource:    source,
		PhonePrimary: mobile(serial),
		Address: patient.Address{
			Division: "Dhaka", District: "Faridpur",
			Upazila: upazilas[serial%len(upazilas)],
			// Only for the town. 7800 is Faridpur Sadar's; guessing one for Boalmari would be
			// a made-up postcode in a field somebody will one day post a letter to.
			Postcode: postcode(upazilas[serial%len(upazilas)]),
		},
		Emergency: patient.EmergencyContact{
			Relation: relations[l.rng.Intn(len(relations))],
			Phone:    mobile(serial + 1000),
		},
		Socio: patient.Socioeconomic{
			Education:     educations[l.rng.Intn(len(educations))],
			Occupation:    occupations[l.rng.Intn(len(occupations))],
			IncomeBand:    incomes[l.rng.Intn(len(incomes))],
			HouseholdSize: 2 + l.rng.Intn(7),
			Residence:     residence(q),
			MedicinePayer: payers[l.rng.Intn(len(payers))],
		},
		// Paper consent, referenced by the form the patient signed at the desk. §15.1 makes
		// the reference mandatory; what it points at is a filing cabinet until CP36's
		// electronic consent is being captured at the desk as well as served by the API.
		ConsentReference: fmt.Sprintf("CONSENT/%d/%04d", l.clock.Now().In(patient.Dhaka).Year(), serial+1),
	}

	// A national identity number for the adults who would carry one. Sealed and digested on
	// the way in — the digest is the duplicate index and the number itself is never stored
	// readable — so this also exercises the one path where a patient identifier is handled.
	if q.AgeYears >= 18 {
		registration.Identifiers = map[patient.IdentifierKind]string{
			patient.NationalID: fmt.Sprintf("%010d", 1_900_000_000+serial*7919),
		}
	}
	return registration
}

// mobile is a Bangladeshi mobile number that is distinct for every patient.
//
// Distinct on purpose rather than by luck: two patients sharing a number would score highly
// enough against the duplicate matcher to be worth a look, and a register that trips its own
// duplicate detection sixty times teaches a developer that the detector cries wolf.
func mobile(serial int) string {
	operators := []int{3, 4, 5, 6, 7, 8, 9}
	return fmt.Sprintf("01%d%08d", operators[serial%len(operators)], (serial*7919+1_234_567)%100_000_000)
}

func postcode(upazila string) string {
	if upazila == "Faridpur Sadar" {
		return "7800"
	}
	return ""
}

func residence(q synthetic.Patient) string {
	if q.Urban {
		return "urban"
	}
	return "rural"
}

// firstAttended is the earliest visit the patient actually turned up to, which is when they
// became a patient. A zero time means they never did.
func (l *loader) firstAttended(q synthetic.Patient) time.Time {
	for _, v := range q.Visits {
		if v.Attended {
			return v.Date
		}
	}
	return time.Time{}
}

// --- what the clinic writes down ---

// complaintFor is what the patient said at the door, which §11.1 keeps on the visit.
func complaintFor(q synthetic.Patient) string {
	switch q.Presenting {
	case synthetic.ProblemDiabetes:
		return "Follow-up for blood sugar"
	case synthetic.ProblemThyroid:
		return "Thyroid follow-up; tiredness"
	case synthetic.ProblemObesity:
		return "Weight gain and tiredness"
	case synthetic.ProblemPCOS:
		return "Irregular periods"
	case synthetic.ProblemGrowth:
		return "Concern about height and growth"
	case synthetic.ProblemBone:
		return "Bone and joint pain"
	case synthetic.ProblemAdrenal:
		return "Weakness and dizziness on standing"
	case synthetic.ProblemPituitary:
		return "Headache and blurred vision"
	case synthetic.ProblemMaleReproductive:
		return "Low energy and reduced libido"
	default:
		return "General endocrine review"
	}
}

// diagnosesFor is the free-text summary §11.1 asks for at close.
//
// Free text and not a code, deliberately. Coded diagnoses live in `history` and would drag
// the counselling gate in with them — a patient with a coded E11 needs a diabetes checklist
// completed before they may be queued for the physician, and inventing seven ticked
// conversations that nobody had would be inventing evidence rather than data.
func diagnosesFor(q synthetic.Patient) string {
	var parts []string
	if q.Diabetes != nil {
		switch q.Diabetes.Type {
		case synthetic.Type1:
			parts = append(parts, "Type 1 diabetes mellitus")
		case synthetic.Gestational:
			parts = append(parts, "Gestational diabetes")
		case synthetic.SecondaryDM:
			parts = append(parts, "Secondary diabetes")
		default:
			parts = append(parts, "Type 2 diabetes mellitus")
		}
	}
	if q.Thyroid != nil {
		parts = append(parts, strings.ReplaceAll(string(q.Thyroid.Category), "_", " "))
	}
	parts = append(parts, q.Comorbidities...)
	if len(parts) == 0 {
		parts = append(parts, strings.ReplaceAll(string(q.Presenting), "_", " "))
	}
	return strings.Join(parts, "; ")
}

// planFor is what was agreed, which is the other half of §11.1's memory.
func planFor(q synthetic.Patient, v synthetic.Visit) string {
	plan := []string{"Continue current medicines", "Diet and activity advice reinforced"}
	if v.HbA1c != nil && *v.HbA1c > 8 {
		plan = append(plan, "Treatment intensified; review sooner if symptomatic")
	}
	if q.Thyroid != nil && q.Thyroid.LevothyroxineMcg > 0 {
		plan = append(plan, fmt.Sprintf("Levothyroxine %d mcg daily", q.Thyroid.LevothyroxineMcg))
	}
	if v.Note != "" {
		plan = append(plan, v.Note)
	}
	return strings.Join(plan, ". ")
}

// --- measurements ---

// anthropometry is station 2's form: what the tape and the scale read, and the two numbers
// the server computes from them.
//
// One batch rather than five calls, because that is what the station screen sends and because
// a batch is one transaction: a height that landed and a weight that did not would leave a
// BMI computed from last quarter's weight, which is worse than no BMI.
func (l *loader) anthropometry(ctx context.Context, who *loaded, v synthetic.Visit,
	visitID, encounterID uuid.UUID, first bool) error {

	q := who.source
	at := l.clock.Now()
	var records []clinical.Recording

	// Height: once for an adult, every visit for a child. That is not a shortcut — CP46's
	// plausibility rule for an adult says a height that moves is a measuring error, and the
	// rule for a child limits the *rate* precisely because it is expected to move.
	switch {
	case v.HeightCm != nil:
		records = append(records, l.measure(who, visitID, encounterID, "BODY_HEIGHT", *v.HeightCm, "cm", at))
	case first && q.HeightM > 0:
		records = append(records, l.measure(who, visitID, encounterID, "BODY_HEIGHT", round(q.HeightM*100, 1), "cm", at))
	}
	if v.Weight != nil {
		records = append(records, l.measure(who, visitID, encounterID, "BODY_WEIGHT", *v.Weight, "kg", at))
	}
	if q.AgeYears >= 18 && q.HeightM > 0 && q.BMI > 0 {
		waist, hip := girths(q, l.rng.Float64())
		records = append(records,
			l.measure(who, visitID, encounterID, "WAIST_CIRC", waist, "cm", at),
			l.measure(who, visitID, encounterID, "HIP_CIRC", hip, "cm", at))
	}
	if len(records) == 0 {
		return nil
	}

	written, _, err := l.clinical.RecordBatch(ctx, clinical.Batch{
		EventID: uuid.New(), PatientID: who.person.ID, VisitID: &visitID,
		Records: records,
		// Asked for unconditionally. A derivation whose inputs are not in the record is
		// skipped by the batch rather than failing it, so "derive the BMI if you can" is one
		// line here instead of a condition that would drift from the one in the service.
		Derive:       []clinical.Derivable{clinical.DeriveBMI, clinical.DeriveWHR},
		AsianScale:   true,
		LedgerSource: eventstore.SourceWeb,
	})
	if err != nil {
		return fmt.Errorf("recording anthropometry: %w", err)
	}
	l.tally.observations += len(written)
	return nil
}

// vitals is station 5's form, plus whatever laboratory results came back with the patient.
//
// `critical` is what makes today's clinic worth looking at. Historical visits are recorded
// inside every critical band on purpose: an alert raised eight months ago that nobody
// acknowledged is still open today, and sixty of those would bury the four that are about a
// patient who is actually in the building.
func (l *loader) vitals(ctx context.Context, who *loaded, v synthetic.Visit,
	visitID, encounterID uuid.UUID, critical bool) error {

	q := who.source
	at := l.clock.Now()
	reading := l.reading(q)
	if critical {
		reading = l.makeCritical(reading)
	}

	records := []clinical.Recording{
		l.measure(who, visitID, encounterID, "BP_SYSTOLIC", reading.systolic, "mm[Hg]", at),
		l.measure(who, visitID, encounterID, "BP_DIASTOLIC", reading.diastolic, "mm[Hg]", at),
		l.measure(who, visitID, encounterID, "HEART_RATE", reading.pulse, "/min", at),
		l.measure(who, visitID, encounterID, "RESP_RATE", reading.resp, "/min", at),
		l.measure(who, visitID, encounterID, "BODY_TEMP", reading.temp, "Cel", at),
		l.measure(who, visitID, encounterID, "SPO2", reading.spo2, "%", at),
	}

	// Laboratory results, where the generator says the test was actually done. A missing one
	// is not a gap to fill: the profile asks for missed and unaffordable tests explicitly,
	// and a screen that has never met an absent HbA1c has never been tested.
	if v.HbA1c != nil {
		lab := l.measure(who, visitID, encounterID, "HBA1C", *v.HbA1c, "%#ngsp", at)
		// The report came back from a laboratory yesterday, not from the machine in the
		// room. effective_at is when the value was true, and a timeline that put it at the
		// moment somebody typed it would order it after the blood pressure it preceded.
		lab.EffectiveAt = at.Add(-24 * time.Hour)
		lab.Source = clinical.OCR
		records = append(records, lab)
	}
	if v.FastingGlucose != nil {
		lab := l.measure(who, visitID, encounterID, "GLUCOSE_FASTING", *v.FastingGlucose, "mmol/L", at)
		lab.EffectiveAt = at.Add(-24 * time.Hour)
		lab.Source = clinical.OCR
		records = append(records, lab)
	}
	if critical && reading.glucose > 0 {
		records = append(records,
			l.measure(who, visitID, encounterID, "GLUCOSE_RANDOM", reading.glucose, "mmol/L", at))
	}

	written, alerts, err := l.clinical.RecordBatch(ctx, clinical.Batch{
		EventID: uuid.New(), PatientID: who.person.ID, VisitID: &visitID,
		Records: records, LedgerSource: eventstore.SourceWeb,
	})
	if err != nil {
		return fmt.Errorf("recording vitals: %w", err)
	}
	l.tally.observations += len(written)
	l.tally.alerts += len(alerts)
	return nil
}

// measure fills in everything a Recording needs that is the same for every value here.
func (l *loader) measure(who *loaded, visitID, encounterID uuid.UUID,
	code string, value float64, unit string, at time.Time) clinical.Recording {

	amount := value
	encounter := encounterID
	visit := visitID
	return clinical.Recording{
		EventID: uuid.New(), PatientID: who.person.ID,
		VisitID: &visit, EncounterID: &encounter,
		Code: code, Value: &amount, Unit: unit,
		EffectiveAt:  at,
		Source:       clinical.Station,
		LedgerSource: eventstore.SourceWeb,
	}
}

// reading is one set of vital signs, generated rather than taken from the cohort.
//
// The cohort carries a laboratory trajectory and no vital signs at all, so everything here is
// this command's invention. It says so plainly because the distinction matters: the laboratory
// values in the register were reviewed by a clinician as a distribution, and these were not.
//
// Every value is kept inside the critical band for that patient's own age (see vitalBand). A
// register that raised an alert by accident is a register that teaches its readers to ignore
// alerts, which is the one thing an alert list must never teach.
type reading struct {
	systolic, diastolic, pulse, resp, temp, spo2 float64
	// glucose is set only when this reading is deliberately critical.
	glucose float64
}

// band is a closed interval a generated value is kept inside.
type band struct{ low, high float64 }

func (b band) hold(v float64, places int) float64 { return round(clamp(v, b.low, b.high), places) }

// vitalBand is one age group's ordinary centre and the interval that keeps it out of CP50.
//
// Two numbers per measurement rather than one: the mean is what makes the register look like a
// clinic, and the interval is what keeps it quiet. The intervals are taken from migration
// 00032's rules for *that* age band, one step inside each edge, which is why they are a table
// rather than one pair of constants — a pulse of 52 is unremarkable in an adult and below the
// critical floor for a four-year-old, and a single clamp would have raised alerts on children
// all morning while looking perfectly sensible in the code.
type vitalBand struct {
	upTo                                     int // exclusive upper age, 0 for "and above"
	systolic, diastolic, pulse, resp         float64
	systolicSD, diastolicSD, pulseSD, respSD float64
	safeSystolic, safeDiastolic, safePulse   band
	safeResp                                 band
	safeTemp                                 band
}

var vitalBands = []vitalBand{
	// Infants. Neither blood-pressure rule in 00032 reaches below one year, so there is no
	// critical band to stay inside — the interval here is simply a plausible one. The
	// temperature rule *does* reach them, and it is much tighter than the adult one, because
	// a fever in the first months of life is an emergency in a way it is not at any other age.
	{upTo: 1, systolic: 88, diastolic: 55, pulse: 130, resp: 38,
		systolicSD: 6, diastolicSD: 5, pulseSD: 10, respSD: 4,
		safeSystolic: band{70, 130}, safeDiastolic: band{40, 85},
		safePulse: band{85, 195}, safeResp: band{24, 60}, safeTemp: band{36.3, 37.9}},
	{upTo: 6, systolic: 95, diastolic: 60, pulse: 105, resp: 24,
		systolicSD: 7, diastolicSD: 5, pulseSD: 10, respSD: 3,
		safeSystolic: band{78, 135}, safeDiastolic: band{45, 90},
		safePulse: band{65, 175}, safeResp: band{18, 42}, safeTemp: band{35.5, 39.2}},
	{upTo: 12, systolic: 104, diastolic: 66, pulse: 90, resp: 20,
		systolicSD: 8, diastolicSD: 6, pulseSD: 9, respSD: 3,
		safeSystolic: band{78, 135}, safeDiastolic: band{45, 90},
		safePulse: band{55, 145}, safeResp: band{15, 34}, safeTemp: band{35.5, 39.2}},
	{upTo: 18, systolic: 112, diastolic: 70, pulse: 80, resp: 17,
		systolicSD: 9, diastolicSD: 6, pulseSD: 9, respSD: 2,
		safeSystolic: band{88, 155}, safeDiastolic: band{50, 95},
		safePulse: band{50, 135}, safeResp: band{12, 30}, safeTemp: band{35.5, 39.2}},
	{upTo: 0, systolic: 122, diastolic: 78, pulse: 76, resp: 15,
		systolicSD: 11, diastolicSD: 7, pulseSD: 9, respSD: 2,
		safeSystolic: band{92, 174}, safeDiastolic: band{56, 104},
		safePulse: band{45, 125}, safeResp: band{10, 28}, safeTemp: band{35.5, 39.2}},
}

func bandFor(age int) vitalBand {
	for _, b := range vitalBands {
		if b.upTo == 0 || age < b.upTo {
			return b
		}
	}
	return vitalBands[len(vitalBands)-1]
}

func (l *loader) reading(q synthetic.Patient) reading {
	b := bandFor(q.AgeYears)
	noise := func(spread float64) float64 { return l.rng.NormFloat64() * spread }

	// Hypertension lifts an adult's pressure and nothing else. It is the one comorbidity the
	// cohort carries that a vital sign should visibly reflect: a register in which the
	// hypertensive patients read the same as everybody else is a register in which no screen
	// that highlights a raised pressure can be looked at.
	lift := 0.0
	if q.AgeYears >= 18 && contains(q.Comorbidities, "hypertension") {
		lift = 20
	}

	r := reading{
		systolic:  b.safeSystolic.hold(b.systolic+lift+noise(b.systolicSD), 0),
		diastolic: b.safeDiastolic.hold(b.diastolic+lift/2+noise(b.diastolicSD), 0),
		pulse:     b.safePulse.hold(b.pulse+noise(b.pulseSD), 0),
		resp:      b.safeResp.hold(b.resp+noise(b.respSD), 0),
		temp:      b.safeTemp.hold(36.8+noise(0.25), 1),
		// A saturation below 92% is the blueprint's own critical value, so ordinary readings
		// stay a point clear of it.
		spo2: band{93, 100}.hold(97.5+noise(1), 0),
	}
	// A pulse pressure below 25 mmHg is not a reading anybody takes; two independent draws
	// produce one every few hundred patients, and it reads as a broken cuff rather than as a
	// person.
	if r.diastolic > r.systolic-25 {
		r.diastolic = b.safeDiastolic.hold(r.systolic-25, 0)
	}
	return r
}

// makeCritical pushes one reading over one of CP50's seeded thresholds.
//
// One of three presentations rather than "all the numbers are extreme", because the point is
// to show what the alert path does with a plausible emergency. A patient with a saturation of
// 88, a pulse of 190 and a temperature of 41 is not an emergency, it is a broken machine, and
// a screen full of those teaches nothing about triage.
//
// The values are just outside the adult bands in migration 00032 and comfortably inside CP46's
// plausibility bands, so each is stored without confirmation and raises exactly one alert.
func (l *loader) makeCritical(r reading) reading {
	switch l.rng.Intn(3) {
	case 0:
		// §3 step 5, named in the blueprint itself: below 92% on room air.
		r.spo2 = 88
	case 1:
		// Above 180/110: the consultant sees the patient before they leave the station.
		r.systolic, r.diastolic = 196, 116
	default:
		// The one this clinic will actually meet: a hypoglycaemia in the waiting area.
		r.glucose = 2.6
	}
	return r
}

// girths turns a body-mass index into a waist and a hip a tape measure could have read.
//
// Approximate and admittedly so. The cohort carries no circumferences, and the alternative to
// deriving them was leaving station 2's form half empty — which would mean the waist-hip ratio,
// the one derived value CP43 computes from two measured ones, never appears in the register at
// all.
func girths(q synthetic.Patient, jitter float64) (waist, hip float64) {
	heightCm := q.HeightM * 100
	waist = heightCm*0.44 + (q.BMI-22)*1.9 + (jitter-0.5)*4
	spread := 6.0
	if q.Sex == synthetic.Female {
		spread = 14.0
	}
	hip = waist + spread + (jitter-0.5)*4
	return round(clamp(waist, 50, 155), 1), round(clamp(hip, 58, 165), 1)
}

func clamp(v, low, high float64) float64 { return math.Max(low, math.Min(high, v)) }

func round(v float64, places int) float64 {
	scale := math.Pow(10, float64(places))
	return math.Round(v*scale) / scale
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// lastMeasured is the most recent visit at which somebody actually weighed this patient.
//
// Today's anthropometry continues the cohort's own trajectory rather than starting a new one:
// a weight taken this morning that ignored the four before it would put a step in every trend
// line, which is exactly the artefact a reviewer would take for a data bug.
func lastMeasured(q synthetic.Patient) synthetic.Visit {
	var fallback synthetic.Visit
	for i := len(q.Visits) - 1; i >= 0; i-- {
		v := q.Visits[i]
		if !v.Attended {
			continue
		}
		if fallback.Date.IsZero() {
			fallback = v
		}
		if v.Weight != nil {
			return v
		}
	}
	return fallback
}

// contraindicationsFor maps what the cohort knows about a patient onto station 8's questions.
//
// Two of the five, and the omissions are the interesting part. The cohort records that a
// patient has retinopathy or neuropathy; station 8 asks whether it is *proliferative* or
// *severe*, which is a different and much stronger claim. Answering yes from the weaker fact
// would put a contraindication on a patient nobody examined for it, and the whole of CP60 is
// that the permitted-exercise list is computed from this answer — so the exercise a patient is
// told not to do would rest on a finding this command invented.
func contraindicationsFor(q synthetic.Patient, asked []string) []string {
	applies := []string{}
	add := func(code string, when bool) {
		if when && contains(asked, code) {
			applies = append(applies, code)
		}
	}
	add("CARDIAC_LIMITATION", contains(q.Comorbidities, "ischaemic heart disease"))
	add("ACTIVE_FOOT_ULCER", contains(q.Comorbidities, "diabetic foot"))
	return applies
}
