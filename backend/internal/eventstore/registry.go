package eventstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// The event registry (§7.3, §7.10): which event types exist, for which aggregate, at which
// version, with which payload shape — and how an old version is read as the current one.
//
// A payload is a Go struct with a Validate method rather than a JSON Schema document,
// because the checks a clinical payload needs — a height in centimetres between 30 and
// 250, a unit that is the canonical one — are easier to say and to test in Go than in
// schema vocabulary, and the type is what the projections will decode into anyway.
// Unknown fields are refused: a client that sends a field the server does not know is a
// client whose version the server does not know.

// Payload is what a registered event type decodes its content into.
type Payload interface {
	Validate() error
}

// Type describes one event type at one version.
type Type struct {
	Name      string
	Version   int
	Aggregate string
	// New returns an empty payload of this version to decode into.
	New func() Payload
	// Upcast, when set, maps this version's payload to the *next* version's. Chained at
	// read time until the current version is reached; never deleted (§7.10).
	Upcast func(raw json.RawMessage) (json.RawMessage, error)
}

// Registry holds the types. One per process; Default is the one the store uses.
type Registry struct {
	mu    sync.RWMutex
	types map[string]map[int]Type
}

func NewRegistry() *Registry {
	return &Registry{types: map[string]map[int]Type{}}
}

// Register adds a type. Registering the same name and version twice is a programming
// error and panics at start-up, where it is cheap.
func (r *Registry) Register(t Type) {
	if t.Name == "" || t.Version < 1 || t.Aggregate == "" || t.New == nil {
		panic(fmt.Sprintf("eventstore: incomplete registration for %q v%d", t.Name, t.Version))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.types[t.Name] == nil {
		r.types[t.Name] = map[int]Type{}
	}
	if _, dup := r.types[t.Name][t.Version]; dup {
		panic(fmt.Sprintf("eventstore: %s v%d registered twice", t.Name, t.Version))
	}
	r.types[t.Name][t.Version] = t
}

// Lookup returns a type and whether it exists.
func (r *Registry) Lookup(name string, version int) (Type, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.types[name][version]
	return t, ok
}

// Current is the highest registered version of a type, or 0.
func (r *Registry) Current(name string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	best := 0
	for v := range r.types[name] {
		if v > best {
			best = v
		}
	}
	return best
}

// Names lists the registered types, sorted, for the documentation and its test.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.types))
	for n := range r.types {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Decode validates a raw payload against the named type and version: the shape (no
// unknown fields, no wrong types) and the type's own rules.
func (r *Registry) Decode(name string, version int, raw json.RawMessage) (Payload, error) {
	t, ok := r.Lookup(name, version)
	if !ok {
		return nil, fmt.Errorf("%w: %s v%d", ErrUnknownEventType, name, version)
	}
	p := t.New()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(p); err != nil {
		return nil, fmt.Errorf("%w: %s v%d: %v", ErrInvalidPayload, name, version, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("%w: %s v%d: trailing content", ErrInvalidPayload, name, version)
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %s v%d: %v", ErrInvalidPayload, name, version, err)
	}
	return p, nil
}

// Upcast brings a stored payload from its version to the current one, one step at a
// time. A version with no path forward is an error, not a silent pass-through.
func (r *Registry) Upcast(name string, version int, raw json.RawMessage) (json.RawMessage, int, error) {
	current := r.Current(name)
	if current == 0 {
		return nil, 0, fmt.Errorf("%w: %s", ErrUnknownEventType, name)
	}
	for version < current {
		t, ok := r.Lookup(name, version)
		if !ok || t.Upcast == nil {
			return nil, 0, fmt.Errorf("%s v%d has no upcaster to v%d", name, version, version+1)
		}
		next, err := t.Upcast(raw)
		if err != nil {
			return nil, 0, fmt.Errorf("upcasting %s v%d: %w", name, version, err)
		}
		raw = next
		version++
	}
	return raw, version, nil
}

// Default is the process's registry, populated by init below with the initial catalogue.
var Default = NewRegistry()

// --- the initial catalogue (§7.3, the types the first clinical checkpoints need) ---

// Measurement is the payload of every *_RECORDED anthropometric or vital event: a code,
// a value in the canonical SI unit, the unit named so a reader never has to guess, and
// how it was taken.
type Measurement struct {
	Code   string  `json:"code"`
	Value  float64 `json:"value"`
	Unit   string  `json:"unit"`
	Method string  `json:"method,omitempty"`
}

// measurementRules are the plausibility bands per code: not clinical judgement, which is
// CP50's critical-value table, but the range outside which a number is a typing error.
var measurementRules = map[string]struct {
	unit     string
	min, max float64
}{
	"HEIGHT":       {"cm", 30, 250},
	"WEIGHT":       {"kg", 1, 400},
	"WAIST":        {"cm", 20, 250},
	"HIP":          {"cm", 20, 250},
	"BP_SYSTOLIC":  {"mmHg", 40, 300},
	"BP_DIASTOLIC": {"mmHg", 20, 200},
	"PULSE":        {"bpm", 20, 250},
	"SPO2":         {"%", 40, 100},
	"TEMP":         {"C", 30, 45},
}

func (m Measurement) Validate() error {
	rule, ok := measurementRules[m.Code]
	if !ok {
		return fmt.Errorf("unknown measurement code %q", m.Code)
	}
	if m.Unit != rule.unit {
		return fmt.Errorf("%s is recorded in %s, not %q", m.Code, rule.unit, m.Unit)
	}
	if m.Value < rule.min || m.Value > rule.max {
		return fmt.Errorf("%s %g %s is outside the plausible band %g–%g", m.Code, m.Value, m.Unit, rule.min, rule.max)
	}
	return nil
}

// BloodPressure is BP_RECORDED: two numbers that belong together.
type BloodPressure struct {
	Systolic  float64 `json:"systolic"`
	Diastolic float64 `json:"diastolic"`
	Unit      string  `json:"unit"`
	Position  string  `json:"position,omitempty"`
	Arm       string  `json:"arm,omitempty"`
}

func (b BloodPressure) Validate() error {
	if b.Unit != "mmHg" {
		return errors.New("blood pressure is recorded in mmHg")
	}
	if b.Systolic < 40 || b.Systolic > 300 || b.Diastolic < 20 || b.Diastolic > 200 {
		return fmt.Errorf("%g/%g is outside the plausible band", b.Systolic, b.Diastolic)
	}
	if b.Diastolic >= b.Systolic {
		return fmt.Errorf("diastolic %g is not below systolic %g", b.Diastolic, b.Systolic)
	}
	return nil
}

// --- the patient aggregate (CP28) ---
//
// The vocabulary the socio-economic baseline is drawn from lives here rather than in the
// patient package, and `patient` takes its exported lists from these. The ledger is the
// system of record and an event is immutable, so a category that has once been written
// into an event exists for as long as the deployment does; that makes the event schema the
// right place for the list, and it means the domain, the API's enum and the database CHECK
// cannot quietly drift into three different vocabularies (§12, ADR-0020).
var (
	PatientSexes         = []string{"female", "male", "other"}
	PatientDOBPrecisions = []string{"day", "month", "year"}
	PatientDOBSources    = []string{
		"birth_certificate", "national_id", "passport", "immunisation_card",
		"patient_stated", "guardian_stated", "estimated",
	}
	PatientIdentifierKinds = []string{
		"national_id", "birth_certificate", "passport", "driving_licence", "other",
	}
	PatientEducationLevels = []string{
		"none", "primary", "secondary", "higher_secondary",
		"graduate", "postgraduate", "madrasa", "unknown",
	}
	PatientOccupationCategories = []string{
		"agriculture", "day_labour", "factory_worker", "service_private",
		"service_government", "business", "homemaker", "student",
		"retired", "unemployed", "other", "unknown",
	}
	PatientIncomeBands    = []string{"under_10k", "10k_25k", "25k_50k", "50k_100k", "over_100k", "unknown"}
	PatientResidenceTypes = []string{"urban", "semi_urban", "rural", "unknown"}
	PatientMedicinePayers = []string{"self", "family", "employer", "ngo", "government", "unknown"}
)

// clinicalID is DTHC-FRD-2026-000137: a facility code, the clinic year, a gapless counter.
var clinicalID = regexp.MustCompile(`^[A-Z][A-Z0-9-]{2,15}-[0-9]{4}-[0-9]{6}$`)

// bdMobile is the normalised form the patient schema stores.
var bdMobile = regexp.MustCompile(`^\+8801[3-9][0-9]{8}$`)

// PatientRegistered is the first event of every patient aggregate: the complete
// demographics, as submitted, at the moment the person became a patient (CP28, CP29).
//
// Flat rather than nested, deliberately: the read model is a table, the projection is then
// a straight copy, and a nested payload would mean a mapping layer whose only job is to
// flatten — one more place for a field to be dropped silently.
//
// Two things are deliberately *not* here, and both are decisions rather than omissions:
//
//	the identifier numbers   Only the kinds. A national ID written into an event could
//	                         never be re-sealed under a rotated key, nor removed for a
//	                         patient who withdraws consent, because the ledger is
//	                         append-only. The sealed values live in
//	                         core.patient_identifier, where a key rotation can reach them.
//	the research id          Putting it here would place the re-identification link in a
//	                         table the application can read, which is exactly what
//	                         identity_link exists to prevent (§12).
type PatientRegistered struct {
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`
	ClinicalID string `json:"clinical_id"`

	NameEN string `json:"name_en"`
	NameBN string `json:"name_bn,omitempty"`
	Sex    string `json:"sex"`

	// BirthDate is YYYY-MM-DD in the clinic's calendar, and the two fields beside it say
	// how much of it is real and what established it. A percentile computed from a date
	// with no precision beside it is a clinical number that looks like a measurement [R-06].
	BirthDate    string `json:"birth_date"`
	DOBPrecision string `json:"dob_precision"`
	DOBSource    string `json:"dob_source"`

	PhonePrimary   string `json:"phone_primary"`
	PhoneSecondary string `json:"phone_secondary,omitempty"`

	Division    string `json:"division,omitempty"`
	District    string `json:"district,omitempty"`
	Upazila     string `json:"upazila,omitempty"`
	AddressLine string `json:"address_line,omitempty"`
	Postcode    string `json:"postcode,omitempty"`

	EmergencyName     string `json:"emergency_name,omitempty"`
	EmergencyRelation string `json:"emergency_relation,omitempty"`
	EmergencyPhone    string `json:"emergency_phone,omitempty"`

	// The §12 cohorting baseline. Absent means not captured; "unknown" means asked and not
	// known, which is itself a finding.
	EducationLevel     string `json:"education_level,omitempty"`
	OccupationCategory string `json:"occupation_category,omitempty"`
	IncomeBand         string `json:"income_band,omitempty"`
	HouseholdSize      int    `json:"household_size,omitempty"`
	ResidenceType      string `json:"residence_type,omitempty"`
	MedicinePayer      string `json:"medicine_payer,omitempty"`

	IdentifierKinds []string `json:"identifier_kinds,omitempty"`

	// ConsentReference is the consent record this registration was taken under. §15.1
	// makes consent tracking binding, and a patient record with no consent behind it is one
	// nothing may lawfully be done with.
	ConsentReference string `json:"consent_reference"`
}

func (p PatientRegistered) Validate() error {
	if len(p.FacilityID) != 36 || len(p.PatientID) != 36 {
		return errors.New("facility_id and patient_id are required")
	}
	if !clinicalID.MatchString(p.ClinicalID) {
		return fmt.Errorf("clinical_id %q is not FACILITY-YYYY-NNNNNN", p.ClinicalID)
	}
	if strings.TrimSpace(p.NameEN) == "" {
		return errors.New("name_en is required")
	}
	if err := oneOf("sex", p.Sex, PatientSexes); err != nil {
		return err
	}

	// The date, and the two fields that say what it is worth.
	born, err := time.Parse(time.DateOnly, p.BirthDate)
	if err != nil {
		return fmt.Errorf("birth_date %q is not YYYY-MM-DD", p.BirthDate)
	}
	if born.Year() < 1890 {
		return fmt.Errorf("birth_date %q implies an implausible age", p.BirthDate)
	}
	if err := oneOf("dob_precision", p.DOBPrecision, PatientDOBPrecisions); err != nil {
		return err
	}
	if err := oneOf("dob_source", p.DOBSource, PatientDOBSources); err != nil {
		return err
	}

	// Normalised, not merely present: a number stored three ways is a number that matches
	// nothing, and an SMS reminder that fails for a fraction of patients (§11) fails
	// silently.
	if !bdMobile.MatchString(p.PhonePrimary) {
		return fmt.Errorf("phone_primary %q is not a normalised Bangladeshi mobile", p.PhonePrimary)
	}

	for _, check := range []struct {
		field, value string
		allowed      []string
	}{
		{"education_level", p.EducationLevel, PatientEducationLevels},
		{"occupation_category", p.OccupationCategory, PatientOccupationCategories},
		{"income_band", p.IncomeBand, PatientIncomeBands},
		{"residence_type", p.ResidenceType, PatientResidenceTypes},
		{"medicine_payer", p.MedicinePayer, PatientMedicinePayers},
	} {
		if check.value == "" {
			continue // not captured, which the confirmed required set allows
		}
		if err := oneOf(check.field, check.value, check.allowed); err != nil {
			return err
		}
	}
	if p.HouseholdSize != 0 && (p.HouseholdSize < 1 || p.HouseholdSize > 40) {
		return fmt.Errorf("household_size %d is outside 1-40", p.HouseholdSize)
	}

	for _, kind := range p.IdentifierKinds {
		if err := oneOf("identifier_kinds", kind, PatientIdentifierKinds); err != nil {
			return err
		}
	}
	// The numbers must not travel, and a client that sends one has misunderstood something
	// that matters. Caught here rather than ignored, because a payload silently dropped is
	// a payload somebody will assume was stored.
	if strings.TrimSpace(p.ConsentReference) == "" {
		return errors.New("consent_reference is required")
	}
	return nil
}

func oneOf(field, value string, allowed []string) error {
	for _, candidate := range allowed {
		if candidate == value {
			return nil
		}
	}
	return fmt.Errorf("%s %q is not one of %s", field, value, strings.Join(allowed, ", "))
}

// PatientMerged records that two records were one person (CP30).
//
// Emitted on the **losing** aggregate, because that is the record whose meaning changed:
// from here on it redirects. The survivor's own history is untouched, and an event on it
// would say nothing that this one does not.
//
// A merge is never automatic and never a delete. `Justification` is required and is
// free text: "duplicate" is not a justification, and six months later the question is
// always "why did we decide these were the same person".
type PatientMerged struct {
	FacilityID string `json:"facility_id"`
	MergedID   string `json:"merged_id"`
	SurvivorID string `json:"survivor_id"`
	// Score is what the matcher thought at the moment of the decision, and Decision is
	// what the person did with that. A merge performed against a low score is a human
	// overruling the machine, which is legitimate and is exactly the case somebody will
	// want to review later.
	Score         float64 `json:"score"`
	Decision      string  `json:"decision"`
	Justification string  `json:"justification"`
	// CandidateIDs is the rest of the list that was on screen, so the decision can be
	// reconstructed even after the matcher's weights are tuned.
	CandidateIDs []string `json:"candidate_ids,omitempty"`
}

var mergeDecisions = []string{"blocked_match", "reviewed_match", "manual"}

func (p PatientMerged) Validate() error {
	if len(p.FacilityID) != 36 || len(p.MergedID) != 36 || len(p.SurvivorID) != 36 {
		return errors.New("facility_id, merged_id and survivor_id are required")
	}
	if p.MergedID == p.SurvivorID {
		return errors.New("a record cannot be merged into itself")
	}
	if p.Score < 0 || p.Score > 1 {
		return fmt.Errorf("score %g is outside 0..1", p.Score)
	}
	if err := oneOf("decision", p.Decision, mergeDecisions); err != nil {
		return err
	}
	// Ten characters is not a quality bar; it is enough to stop "dup" and "same".
	if len(strings.TrimSpace(p.Justification)) < 10 {
		return errors.New("a merge needs a justification a reviewer can act on")
	}
	return nil
}

// PatientPhotoCaptured records that a photograph was taken (CP34).
//
// The **key**, never the bytes and never a URL. A URL in an immutable event is a URL that
// expires fifteen minutes later and is then a permanent piece of misleading history; the key
// is what the object is called, and a reader mints a fresh signed URL from it.
//
// The digest travels too, so a photograph that silently changes in storage is detectable
// from the ledger rather than only from the row that points at it.
type PatientPhotoCaptured struct {
	FacilityID  string `json:"facility_id"`
	PatientID   string `json:"patient_id"`
	ObjectClass string `json:"object_class"`
	ObjectKey   string `json:"object_key"`
	ContentType string `json:"content_type"`
	ByteSize    int64  `json:"byte_size"`
	SHA256      string `json:"sha256"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	// ReplacesKey names the photograph this one supersedes, if any. A replacement is a new
	// object and a new event; nothing is overwritten, so a chart printed last month can
	// still be explained.
	ReplacesKey string `json:"replaces_key,omitempty"`
}

var photoTypes = []string{"image/jpeg", "image/png", "image/webp"}

// MaxPhotoBytes is eight megabytes. A clinic phone's camera produces two to four after the
// client-side resize; eight leaves room and still refuses somebody uploading a video.
const MaxPhotoBytes = 8 << 20

func (p PatientPhotoCaptured) Validate() error {
	if len(p.FacilityID) != 36 || len(p.PatientID) != 36 {
		return errors.New("facility_id and patient_id are required")
	}
	if strings.TrimSpace(p.ObjectKey) == "" || strings.Contains(p.ObjectKey, "..") {
		return fmt.Errorf("object_key %q is not usable", p.ObjectKey)
	}
	if p.ObjectClass != "identifier" {
		// A photograph is identifier-class data. Storing one anywhere else would put a
		// face outside the residency boundary D-01 is about.
		return fmt.Errorf("a patient photograph is identifier-class, not %q", p.ObjectClass)
	}
	if err := oneOf("content_type", p.ContentType, photoTypes); err != nil {
		return err
	}
	if p.ByteSize <= 0 || p.ByteSize > MaxPhotoBytes {
		return fmt.Errorf("byte_size %d is outside 1..%d", p.ByteSize, MaxPhotoBytes)
	}
	if len(p.SHA256) != 64 {
		return errors.New("sha256 must be the hex digest of the object")
	}
	return nil
}

// FieldChange is one field of a correction: what it was and what it is now.
//
// Both, always. A correction that records only the new value is a correction that cannot be
// read back — and "the letter I have says something different" is a question somebody asks
// about a record years after the person who changed it has left.
type FieldChange struct {
	Field    string `json:"field"`
	Previous string `json:"previous"`
	Current  string `json:"current"`
}

// PatientDemographicsCorrected is a demographic value put right (CP35, §4.3).
//
// The correction principle applies to demographics as much as to clinical values, and the
// date of birth is why it has to: a wrong one changes every pediatric percentile ever
// computed for that patient, and those numbers have already been read and acted on.
//
// The payload carries the changes *and* the corrected values, which is redundant on purpose.
// The changes are what a person reads; the values are what the projection applies. Deriving
// one from the other at read time would make the history depend on a parser rather than on
// what was recorded.
type PatientDemographicsCorrected struct {
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`

	Changes []FieldChange `json:"changes"`
	// Reason is required and is free text. "Correction" is not a reason; "the NID card says
	// 1985, the registration desk typed 1958" is.
	Reason string `json:"reason"`
	// HighImpact marks a correction to a field that other values were computed from. It is
	// the flag somebody searches on when a percentile is questioned.
	HighImpact bool `json:"high_impact"`

	CorrectedBy     string `json:"corrected_by"`
	CorrectedByCode string `json:"corrected_by_code,omitempty"`
	CorrectedAt     string `json:"corrected_at"`

	// The corrected values, present only for the fields that changed. A nil field is a
	// field this correction did not touch, which is what stops a correction of one value
	// silently rewriting another.
	NameEN         *string `json:"name_en,omitempty"`
	NameBN         *string `json:"name_bn,omitempty"`
	NameKeyEN      *string `json:"name_key_en,omitempty"`
	Sex            *string `json:"sex,omitempty"`
	BirthDate      *string `json:"birth_date,omitempty"`
	DOBPrecision   *string `json:"dob_precision,omitempty"`
	DOBSource      *string `json:"dob_source,omitempty"`
	PhonePrimary   *string `json:"phone_primary,omitempty"`
	PhoneSecondary *string `json:"phone_secondary,omitempty"`
	Division       *string `json:"division,omitempty"`
	District       *string `json:"district,omitempty"`
	Upazila        *string `json:"upazila,omitempty"`
	AddressLine    *string `json:"address_line,omitempty"`
	Postcode       *string `json:"postcode,omitempty"`
}

// HighImpactFields are the demographic fields other values are computed from.
//
// Changing one of these invalidates something that has already been read: a date of birth
// changes every age and every percentile, a sex changes reference ranges and cohorts, a name
// changes what a duplicate check would have found. `ops.derived_dependency` records what
// depends on what; this is the list that decides whether a correction needs a step-up.
var HighImpactFields = []string{"birth_date", "dob_precision", "sex", "name_en"}

func (p PatientDemographicsCorrected) Validate() error {
	if len(p.FacilityID) != 36 || len(p.PatientID) != 36 {
		return errors.New("facility_id and patient_id are required")
	}
	if len(p.Changes) == 0 {
		// A correction that changed nothing is a history entry that tells a reader
		// nothing, and it would sit in the trail looking like something happened.
		return errors.New("a correction must change something")
	}
	for _, change := range p.Changes {
		if strings.TrimSpace(change.Field) == "" {
			return errors.New("every change names a field")
		}
		if change.Previous == change.Current {
			return fmt.Errorf("%s was not changed", change.Field)
		}
	}
	if len(strings.TrimSpace(p.Reason)) < 10 {
		return errors.New("a correction needs a reason a reader can act on")
	}
	if len(p.CorrectedBy) != 36 {
		return errors.New("corrected_by is required")
	}
	if p.BirthDate != nil {
		if _, err := time.Parse(time.DateOnly, *p.BirthDate); err != nil {
			return fmt.Errorf("birth_date %q is not YYYY-MM-DD", *p.BirthDate)
		}
	}
	if p.Sex != nil {
		if err := oneOf("sex", *p.Sex, PatientSexes); err != nil {
			return err
		}
	}
	if p.DOBPrecision != nil {
		if err := oneOf("dob_precision", *p.DOBPrecision, PatientDOBPrecisions); err != nil {
			return err
		}
	}
	if p.PhonePrimary != nil && !bdMobile.MatchString(*p.PhonePrimary) {
		return fmt.Errorf("phone_primary %q is not a normalised Bangladeshi mobile", *p.PhonePrimary)
	}
	return nil
}

// --- consent (CP36, §15.1, D-02) ---

// The five things a patient consents to, each independently grantable and revocable.
//
// Layered rather than blanket, which is D-02's recommendation and the only shape that
// survives contact with the questions the clinic actually asks. A patient who wants
// treatment but not an SMS at seven in the morning, or treatment but not their anonymised
// row in a paper, is expressing two different preferences; a single "I consent" box records
// neither of them and answers the wrong question when somebody later asks what they agreed
// to.
var ConsentTypes = []string{
	// Treatment itself. Without it there is nothing lawful to do with the record.
	"care",
	// Telephone calls and SMS. §11.2 asks for it at checkout; a reminder is not treatment.
	"communication",
	// Inclusion in the anonymised research cohort (§12). Opt-in, never assumed.
	"research",
	// Processing of the record by the AI gateway (§7). Separate because a patient may accept
	// a human reading their notes and not a model.
	"ai_processing",
	// Community outreach follow-up — a home visit, a camp invitation.
	"outreach",
}

// How the consent was actually taken.
//
// `verbal_attested` is here because refusing it would not make consent better recorded; it
// would make it recorded on paper and not here. A staff attestation with a witness named is
// weaker evidence than a thumbprint and the record says which it is, which is the honest
// arrangement.
var ConsentCaptureMethods = []string{"signature", "thumbprint", "verbal_attested", "paper_form"}

// The languages a template may be shown in. What was *shown* is what was consented to, so
// this travels with the record rather than being inferred from the reader's setting later.
var ConsentLanguages = []string{"en", "bn"}

// ConsentGranted records a patient agreeing to one thing (CP36).
//
// The template **version and language** are part of the event, not a lookup. "The patient
// consented to research" is not an answer anybody can act on years later; "the patient was
// shown research consent version 3 in Bangla on 14 September 2026, and a thumbprint was
// taken, witnessed by employee REG-04" is. The wording itself is retrievable by version, and
// a version that has been consented against can never be edited.
//
// The evidence is an **object key**, never bytes: a signature image is identifier-class data
// and follows the same path a photograph does (CP34).
type ConsentGranted struct {
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`

	ConsentType     string `json:"consent_type"`
	TemplateVersion int    `json:"template_version"`
	Language        string `json:"language"`
	// TemplateDigest is the SHA-256 of the exact text shown. A template row could in
	// principle be replaced by somebody with database access; the digest in the ledger is
	// what makes that detectable.
	TemplateDigest string `json:"template_digest"`

	CaptureMethod string `json:"capture_method"`
	// EvidenceKey is the signature or thumbprint image, when there is one.
	EvidenceKey    string `json:"evidence_key,omitempty"`
	EvidenceSHA256 string `json:"evidence_sha256,omitempty"`
	// PaperReference is the form number, when the consent was taken on paper.
	PaperReference string `json:"paper_reference,omitempty"`

	// WitnessedBy is the second person present. Required for a thumbprint and for a verbal
	// attestation: those are the two methods where the only other party is the operator
	// recording it, and an attestation nobody witnessed is an assertion.
	WitnessedBy     string `json:"witnessed_by,omitempty"`
	WitnessedByCode string `json:"witnessed_by_code,omitempty"`

	// GrantedFor is who gave it when the patient could not: a guardian for a minor. Empty
	// means the patient themselves.
	GrantedForRelation string `json:"granted_for_relation,omitempty"`
	GrantedForName     string `json:"granted_for_name,omitempty"`
}

func (c ConsentGranted) Validate() error {
	if len(c.FacilityID) != 36 || len(c.PatientID) != 36 {
		return errors.New("facility_id and patient_id are required")
	}
	if err := oneOf("consent_type", c.ConsentType, ConsentTypes); err != nil {
		return err
	}
	if err := oneOf("capture_method", c.CaptureMethod, ConsentCaptureMethods); err != nil {
		return err
	}
	if err := oneOf("language", c.Language, ConsentLanguages); err != nil {
		return err
	}
	if c.TemplateVersion < 1 {
		return errors.New("template_version is required: a consent with no version is a consent to nothing in particular")
	}
	if len(c.TemplateDigest) != 64 {
		return errors.New("template_digest must be the hex sha256 of the text that was shown")
	}
	switch c.CaptureMethod {
	case "signature", "thumbprint":
		if strings.TrimSpace(c.EvidenceKey) == "" || len(c.EvidenceSHA256) != 64 {
			return fmt.Errorf("a %s consent needs its image: evidence_key and evidence_sha256", c.CaptureMethod)
		}
	case "paper_form":
		if strings.TrimSpace(c.PaperReference) == "" {
			return errors.New("a paper consent needs the form reference, or nobody can find it")
		}
	}
	if c.CaptureMethod == "thumbprint" || c.CaptureMethod == "verbal_attested" {
		if strings.TrimSpace(c.WitnessedBy) == "" {
			return fmt.Errorf("a %s consent needs a witness", c.CaptureMethod)
		}
	}
	if (c.GrantedForName == "") != (c.GrantedForRelation == "") {
		return errors.New("a consent given by somebody else needs both their name and their relation")
	}
	return nil
}

// ConsentRevoked records a patient withdrawing one consent (CP36).
//
// Its own event on the same aggregate, never an update of the grant. The grant is what was
// true then and stays retrievable; the revocation is what is true now. Both are needed to
// answer "was this message lawful when it was sent", which is the question that actually
// gets asked.
type ConsentRevoked struct {
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`

	ConsentType string `json:"consent_type"`
	// Reason is optional, and deliberately so. A patient withdrawing consent does not owe
	// anybody an explanation, and a mandatory field here would be filled in with "revoked"
	// by an operator standing in front of somebody who wants to leave.
	Reason string `json:"reason,omitempty"`
	// RequestedBy is who asked: the patient, a guardian, or the clinic itself withdrawing
	// something it should not have taken.
	RequestedBy string `json:"requested_by"`
}

var consentRequesters = []string{"patient", "guardian", "clinic"}

func (c ConsentRevoked) Validate() error {
	if len(c.FacilityID) != 36 || len(c.PatientID) != 36 {
		return errors.New("facility_id and patient_id are required")
	}
	if err := oneOf("consent_type", c.ConsentType, ConsentTypes); err != nil {
		return err
	}
	return oneOf("requested_by", c.RequestedBy, consentRequesters)
}

// --- visits and encounters (CP38, §3, §11.1, §14.2) ---

// The kinds of visit. `outreach_referral` is separate because §14 counts it as a different
// funnel: a patient who arrived from a camp is not a walk-in, and a clinic measuring its
// outreach needs to be able to tell.
var VisitTypes = []string{"new", "follow_up", "outreach_referral"}

// Why a visit ended without the patient being seen.
var VisitAbandonReasons = []string{"patient_left", "referred_out", "clinic_closed", "duplicate", "other"}

// VisitOpened is a patient arriving (CP38).
//
// The chief complaint is on this event rather than the closing one, because it is what the
// patient said at the door and the whole journey is arranged around it. §11.1 asks for it as
// part of the visit's memory.
type VisitOpened struct {
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`
	VisitCode  string `json:"visit_code"`
	VisitType  string `json:"visit_type"`
	// ChiefComplaint in the patient's own words where possible. Free text on purpose: a
	// coded complaint taken at a registration desk is a coded guess.
	ChiefComplaint string `json:"chief_complaint,omitempty"`
	// ClinicDay in Asia/Dhaka, because a visit opened at 23:50 belongs to that day all night.
	ClinicDay string `json:"clinic_day"`
	Reason    string `json:"reason,omitempty"`
}

func (v VisitOpened) Validate() error {
	if len(v.FacilityID) != 36 || len(v.PatientID) != 36 {
		return errors.New("facility_id and patient_id are required")
	}
	if strings.TrimSpace(v.VisitCode) == "" {
		return errors.New("visit_code is required: a visit nobody can call out is a visit nobody can queue")
	}
	if err := oneOf("visit_type", v.VisitType, VisitTypes); err != nil {
		return err
	}
	if len(v.ClinicDay) != 10 {
		return errors.New("clinic_day must be a date in the clinic's calendar")
	}
	return nil
}

// VisitClosed is the physician finishing, with §11.1's summary.
//
// All four are on the event, not looked up later: "which patient came when, with what
// problem" has to be answerable from the ledger alone, forever, even if every read model is
// rebuilt or replaced.
type VisitClosed struct {
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`
	VisitCode  string `json:"visit_code"`

	ChiefComplaint string `json:"chief_complaint"`
	Diagnoses      string `json:"diagnoses"`
	Plan           string `json:"plan"`
	// NextReviewDays is a number rather than "in three months" because the outreach engine
	// reads it to decide who is due.
	NextReviewDays int    `json:"next_review_days"`
	NextReviewOn   string `json:"next_review_on,omitempty"`

	// Stations is the journey, for the record. A closed visit that does not say where the
	// patient went is a closed visit somebody has to reconstruct from encounters.
	Stations []string `json:"stations,omitempty"`
}

func (v VisitClosed) Validate() error {
	if len(v.FacilityID) != 36 || len(v.PatientID) != 36 {
		return errors.New("facility_id and patient_id are required")
	}
	if strings.TrimSpace(v.ChiefComplaint) == "" {
		return errors.New("chief_complaint is required at close (§11.1)")
	}
	if strings.TrimSpace(v.Diagnoses) == "" {
		return errors.New("diagnoses are required at close (§11.1)")
	}
	if strings.TrimSpace(v.Plan) == "" {
		return errors.New("a plan is required at close (§11.1)")
	}
	if v.NextReviewDays < 1 || v.NextReviewDays > 3650 {
		return fmt.Errorf("next_review_days %d is outside 1..3650 (§11.1)", v.NextReviewDays)
	}
	return nil
}

// VisitAbandoned is a visit that ended without the patient being seen.
//
// Its own event, not a close with empty fields. §14.2 counts throughput, and a visit nobody
// completed must not be counted as a completed journey — the number that results is the one
// somebody puts in a report.
type VisitAbandoned struct {
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`
	VisitCode  string `json:"visit_code"`
	Reason     string `json:"reason"`
	Note       string `json:"note,omitempty"`
}

func (v VisitAbandoned) Validate() error {
	if len(v.FacilityID) != 36 || len(v.PatientID) != 36 {
		return errors.New("facility_id and patient_id are required")
	}
	return oneOf("reason", v.Reason, VisitAbandonReasons)
}

// VisitReopened is a closed visit opened again.
//
// Recorded rather than silent, because §4.3's correction principle applies: a closed visit
// that changes without saying so is exactly what it forbids. When the policy for *when* this
// is allowed is confirmed, it becomes a check; the event is the same either way.
type VisitReopened struct {
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`
	VisitCode  string `json:"visit_code"`
	Reason     string `json:"reason"`
	// Attempt is which reopening this is. A visit reopened three times is a visit somebody
	// should look at.
	Attempt int `json:"attempt"`
}

func (v VisitReopened) Validate() error {
	if len(v.FacilityID) != 36 || len(v.PatientID) != 36 {
		return errors.New("facility_id and patient_id are required")
	}
	if len(strings.TrimSpace(v.Reason)) < 10 {
		return errors.New("reopening a closed visit needs a reason a reader can act on")
	}
	if v.Attempt < 1 {
		return errors.New("attempt must say which reopening this is")
	}
	return nil
}

// EncounterStarted is a patient arriving at one station.
type EncounterStarted struct {
	FacilityID  string `json:"facility_id"`
	PatientID   string `json:"patient_id"`
	VisitID     string `json:"visit_id"`
	EncounterID string `json:"encounter_id"`
	StationCode string `json:"station_code"`
}

func (e EncounterStarted) Validate() error {
	if len(e.FacilityID) != 36 || len(e.PatientID) != 36 || len(e.VisitID) != 36 {
		return errors.New("facility_id, patient_id and visit_id are required")
	}
	if len(e.EncounterID) != 36 {
		return errors.New("encounter_id is required so the finish can name the same touch")
	}
	if !strings.HasPrefix(e.StationCode, "STN_") {
		return fmt.Errorf("station_code %q is not a station", e.StationCode)
	}
	return nil
}

// How a station touch ended.
var EncounterOutcomes = []string{"completed", "skipped", "bounced", "patient_left"}

// EncounterFinished is a station done with a patient.
//
// `bounced` is its own outcome rather than a completed encounter with a note, because §14.2
// counts rework and a bounce recorded as "completed" makes rework invisible — which is the
// one number a quality gate exists to produce.
type EncounterFinished struct {
	FacilityID  string `json:"facility_id"`
	PatientID   string `json:"patient_id"`
	VisitID     string `json:"visit_id"`
	EncounterID string `json:"encounter_id"`
	StationCode string `json:"station_code"`
	Outcome     string `json:"outcome"`
	Note        string `json:"note,omitempty"`
	// SecondsAtStation is the measured duration, carried on the event so §14.2's analysis
	// does not depend on two timestamps surviving every future migration of the read model.
	SecondsAtStation int `json:"seconds_at_station"`
}

func (e EncounterFinished) Validate() error {
	if len(e.FacilityID) != 36 || len(e.PatientID) != 36 || len(e.VisitID) != 36 {
		return errors.New("facility_id, patient_id and visit_id are required")
	}
	if len(e.EncounterID) != 36 {
		return errors.New("encounter_id is required")
	}
	if err := oneOf("outcome", e.Outcome, EncounterOutcomes); err != nil {
		return err
	}
	if e.SecondsAtStation < 0 {
		return errors.New("seconds_at_station cannot be negative")
	}
	return nil
}

// --- the station queue (CP39, §5.2, §14.2) ---

// Why a patient left a station queue.
var QueueOutcomes = []string{"served", "skipped", "rerouted", "left"}

// QueueEntered is a patient joining one station's queue.
type QueueEntered struct {
	FacilityID  string `json:"facility_id"`
	PatientID   string `json:"patient_id"`
	VisitID     string `json:"visit_id"`
	EntryID     string `json:"entry_id"`
	StationCode string `json:"station_code"`
	Position    int    `json:"position"`
	Priority    int    `json:"priority"`
	// PriorityReason is required whenever the priority is not ordinary. Jumping a queue
	// without a reason is the thing a queue exists to prevent.
	PriorityReason string `json:"priority_reason,omitempty"`
}

func (q QueueEntered) Validate() error {
	if len(q.FacilityID) != 36 || len(q.PatientID) != 36 || len(q.VisitID) != 36 {
		return errors.New("facility_id, patient_id and visit_id are required")
	}
	if len(q.EntryID) != 36 {
		return errors.New("entry_id is required so the call can name the same place in the queue")
	}
	if !strings.HasPrefix(q.StationCode, "STN_") {
		return fmt.Errorf("station_code %q is not a station", q.StationCode)
	}
	if q.Priority < 0 || q.Priority > 9 {
		return fmt.Errorf("priority %d is outside 0..9", q.Priority)
	}
	if q.Priority > 0 && strings.TrimSpace(q.PriorityReason) == "" {
		return errors.New("a patient jumping the queue needs a reason")
	}
	return nil
}

// QueueCalled is an operator claiming the next patient.
//
// Its own event because it is the moment the board changes for everybody, and because
// "called at 10:14 by REG-04, seen at 10:19" is the pair §14.2 measures a fetch time from.
type QueueCalled struct {
	FacilityID  string `json:"facility_id"`
	PatientID   string `json:"patient_id"`
	VisitID     string `json:"visit_id"`
	EntryID     string `json:"entry_id"`
	StationCode string `json:"station_code"`
	// WaitedSeconds is how long they were in this queue, carried on the event so §14.2 does
	// not depend on two timestamps surviving every future migration of the read model.
	WaitedSeconds int `json:"waited_seconds"`
}

func (q QueueCalled) Validate() error {
	if len(q.FacilityID) != 36 || len(q.PatientID) != 36 || len(q.VisitID) != 36 {
		return errors.New("facility_id, patient_id and visit_id are required")
	}
	if len(q.EntryID) != 36 {
		return errors.New("entry_id is required")
	}
	if q.WaitedSeconds < 0 {
		return errors.New("waited_seconds cannot be negative")
	}
	return nil
}

// QueueLeft is a patient leaving one station's queue.
type QueueLeft struct {
	FacilityID  string `json:"facility_id"`
	PatientID   string `json:"patient_id"`
	VisitID     string `json:"visit_id"`
	EntryID     string `json:"entry_id"`
	StationCode string `json:"station_code"`
	Outcome     string `json:"outcome"`
	Reason      string `json:"reason,omitempty"`
	// ReroutedTo is where they went instead. Required for a reroute: "sent elsewhere" with
	// no elsewhere is a patient nobody can find.
	ReroutedTo    string `json:"rerouted_to,omitempty"`
	WaitedSeconds int    `json:"waited_seconds"`
}

func (q QueueLeft) Validate() error {
	if len(q.FacilityID) != 36 || len(q.PatientID) != 36 || len(q.VisitID) != 36 {
		return errors.New("facility_id, patient_id and visit_id are required")
	}
	if len(q.EntryID) != 36 {
		return errors.New("entry_id is required")
	}
	if err := oneOf("outcome", q.Outcome, QueueOutcomes); err != nil {
		return err
	}
	if q.Outcome == "rerouted" {
		if strings.TrimSpace(q.ReroutedTo) == "" || len(strings.TrimSpace(q.Reason)) < 5 {
			return errors.New("a reroute says where and why")
		}
	}
	return nil
}

// --- observations (CP42, §6, §11) ---

// ObservationSources is where a value came from. Not decoration: a number a patient
// reported at home and a number an operator measured with a calibrated scale are different
// evidence, and a physician deciding a dose deserves to know which.
var ObservationSources = []string{"STATION", "OCR", "FIELD", "DEVICE", "PATIENT"}

// ObservationRecorded is one measured clinical value (CP42).
//
// One payload for every station, which is the whole point of the checkpoint: ten bespoke
// event types would make the timeline, the research extract and the FHIR mapping ten times
// harder, and would guarantee the eleventh station invented an eleventh shape.
//
// # The value fields
//
// Exactly one of them is set, chosen by the code's declared value type. They are separate
// fields rather than one `any` because a ledger payload is decoded years later by code
// nobody has read since, and `any` there means a runtime type assertion in a projection.
//
// # The unit
//
// `Value` and `Unit` are what the operator *entered* — 154 and lb, not 69.85 and kg. The
// canonical value is derived on the way into the read model, by the database, from
// `core.unit`. Putting the conversion in the ledger would freeze today's conversion factor
// into every event ever written; putting it in the projection means a factor corrected
// later corrects the whole history on the next rebuild.
type ObservationRecorded struct {
	ObservationID string `json:"observation_id"`
	FacilityID    string `json:"facility_id"`
	PatientID     string `json:"patient_id"`
	VisitID       string `json:"visit_id,omitempty"`
	EncounterID   string `json:"encounter_id,omitempty"`

	Code string `json:"code"`

	// Numeric values, as entered. Unit is required for a code with a dimension and refused
	// for one without; the registry decides which, and the database enforces it.
	Value *float64 `json:"value,omitempty"`
	Unit  string   `json:"unit,omitempty"`

	ValueText string          `json:"value_text,omitempty"`
	ValueBool *bool           `json:"value_bool,omitempty"`
	ValueCode string          `json:"value_code,omitempty"`
	ValueJSON json.RawMessage `json:"value_json,omitempty"`

	// EffectiveAt is when the thing was true; the envelope's OccurredAt is when it was
	// written down. A blood pressure taken at 09:05 and entered at 09:20 has two times, and
	// a timeline that used the second would order it wrongly beside a promptly-entered one.
	EffectiveAt time.Time `json:"effective_at"`

	Source string `json:"source"`

	// Replaces is the observation this one supersedes or corrects, when it does. The earlier
	// row stops being the value and says which row took its place; it is never deleted.
	Replaces string `json:"replaces,omitempty"`
	// ReplacedStatus is what the earlier row becomes: CORRECTED (it was wrong) or SUPERSEDED
	// (it was right and has been re-measured). Two different facts, and a report that
	// conflated them would count a re-measurement as an error rate.
	ReplacedStatus string `json:"replaced_status,omitempty"`

	// Note is what the operator typed with the value: the cuff size, which arm, "patient
	// could not stand". Free text, because a coded list of caveats never has the one that
	// happened.
	Note string `json:"note,omitempty"`

	// ImplausibleConfirmed and ImplausibleReason record that the operator was warned this
	// value was outside its plausible band and said it was right anyway (CP46).
	//
	// In the ledger rather than only in the read model, because the question it answers is
	// historical: a rule that gets overridden twenty times a week is a rule that is wrong,
	// and the clinic should be able to find that out from its own record rather than from
	// opinion. Optional fields on an existing payload — an event written before this
	// checkpoint simply has neither, which decodes as "not confirmed", which is true.
	ImplausibleConfirmed bool   `json:"implausible_confirmed,omitempty"`
	ImplausibleReason    string `json:"implausible_reason,omitempty"`

	// Formula, FormulaVersion and Inputs belong to a DERIVED value (CP43): which equation
	// produced it, which version of that equation, and what it was given.
	//
	// The version is the load-bearing one. CKD-EPI was revised in 2021 to remove a race
	// coefficient, and a stored eGFR with no version cannot afterwards be told apart from
	// one computed under the old equation. The inputs are stored rather than re-derived
	// because they are what the formula *actually saw* — a weight corrected an hour later
	// does not change what a BMI was computed from.
	Formula        string             `json:"formula,omitempty"`
	FormulaVersion string             `json:"formula_version,omitempty"`
	Inputs         map[string]float64 `json:"inputs,omitempty"`
}

func (o ObservationRecorded) Validate() error {
	if len(o.ObservationID) != 36 {
		return errors.New("observation_id is required")
	}
	if len(o.FacilityID) != 36 || len(o.PatientID) != 36 {
		return errors.New("facility_id and patient_id are required")
	}
	if strings.TrimSpace(o.Code) == "" {
		return errors.New("code is required")
	}
	if err := oneOf("source", o.Source, ObservationSources); err != nil {
		return err
	}
	if o.EffectiveAt.IsZero() {
		return errors.New("effective_at is required: when the value was true, not when it was typed")
	}
	// Exactly one value shape. The registry decides which is right for the code — that
	// check needs the database and belongs there — but "none of them" and "two of them" are
	// decidable here, and both are bugs a projection should never have to guess about.
	set := 0
	if o.Value != nil {
		set++
	}
	if strings.TrimSpace(o.ValueText) != "" {
		set++
	}
	if o.ValueBool != nil {
		set++
	}
	if strings.TrimSpace(o.ValueCode) != "" {
		set++
	}
	if len(o.ValueJSON) > 0 {
		set++
	}
	if set != 1 {
		return fmt.Errorf("an observation carries exactly one value, not %d", set)
	}
	if o.Value != nil && strings.TrimSpace(o.Unit) == "" {
		// A number with no unit is the failure this whole checkpoint exists to prevent.
		// The database refuses it too; refusing it here means it never reaches the ledger,
		// where it would be permanent.
		return errors.New("a numeric observation carries the unit it was entered in")
	}
	if o.Replaces != "" && len(o.Replaces) != 36 {
		return errors.New("replaces must be an observation id")
	}
	if o.ReplacedStatus != "" && o.ReplacedStatus != "CORRECTED" && o.ReplacedStatus != "SUPERSEDED" {
		return fmt.Errorf("replaced_status %q is neither CORRECTED nor SUPERSEDED", o.ReplacedStatus)
	}
	if o.Replaces == "" && o.ReplacedStatus != "" {
		return errors.New("replaced_status names what happened to the row in `replaces`, and there is none")
	}
	// A formula without a version, or a version without a formula, is half of an answer.
	// Whether this code *needs* them is the registry's question and the database's to
	// enforce; what is decidable here is that the pair is whole.
	if (o.Formula == "") != (o.FormulaVersion == "") {
		return errors.New("a derived value names both its formula and that formula's version")
	}
	if o.Formula != "" && len(o.Inputs) == 0 {
		return errors.New("a derived value records what it was computed from")
	}
	return nil
}

func init() {
	measurement := func() Payload { return &Measurement{} }
	for _, name := range []string{"HEIGHT_RECORDED", "HEIGHT_CORRECTED", "WEIGHT_RECORDED", "WEIGHT_CORRECTED",
		"WAIST_RECORDED", "HIP_RECORDED", "PULSE_RECORDED", "SPO2_RECORDED", "TEMP_RECORDED"} {
		Default.Register(Type{Name: name, Version: 1, Aggregate: "VISIT", New: measurement})
	}
	Default.Register(Type{Name: "BP_RECORDED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &BloodPressure{} }})
	Default.Register(Type{Name: "BP_CORRECTED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &BloodPressure{} }})
	Default.Register(Type{Name: "PATIENT_REGISTERED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &PatientRegistered{} }})
	Default.Register(Type{Name: "PATIENT_MERGED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &PatientMerged{} }})
	Default.Register(Type{Name: "PATIENT_PHOTO_CAPTURED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &PatientPhotoCaptured{} }})
	Default.Register(Type{Name: "PATIENT_DEMOGRAPHICS_CORRECTED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &PatientDemographicsCorrected{} }})
	Default.Register(Type{Name: "CONSENT_GRANTED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &ConsentGranted{} }})
	Default.Register(Type{Name: "CONSENT_REVOKED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &ConsentRevoked{} }})
	Default.Register(Type{Name: "QUEUE_ENTERED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &QueueEntered{} }})
	Default.Register(Type{Name: "QUEUE_CALLED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &QueueCalled{} }})
	Default.Register(Type{Name: "QUEUE_LEFT", Version: 1, Aggregate: "VISIT", New: func() Payload { return &QueueLeft{} }})
	Default.Register(Type{Name: "VISIT_OPENED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &VisitOpened{} }})
	Default.Register(Type{Name: "VISIT_CLOSED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &VisitClosed{} }})
	Default.Register(Type{Name: "VISIT_ABANDONED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &VisitAbandoned{} }})
	Default.Register(Type{Name: "VISIT_REOPENED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &VisitReopened{} }})
	Default.Register(Type{Name: "ENCOUNTER_STARTED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &EncounterStarted{} }})
	Default.Register(Type{Name: "ENCOUNTER_FINISHED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &EncounterFinished{} }})
	// One event type for every measured value (CP42). CORRECTED is the same payload with
	// `replaces` set; a separate type would mean every consumer had to handle two.
	Default.Register(Type{Name: "OBSERVATION_RECORDED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &ObservationRecorded{} }})
}
