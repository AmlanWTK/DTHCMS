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

// ---------------------------------------------------------------------------
// Critical values (CP50)
// ---------------------------------------------------------------------------

// CriticalValueAlerted is a measured value that means somebody has to act now.
//
// Appended in the same transaction as the OBSERVATION_RECORDED that set it off, which is the
// design's load-bearing property: there is no window in which a dangerous number is in the
// record and nothing is coming. Either both facts exist or neither does.
//
// The value is copied here rather than left behind the observation id, and that is deliberate.
// An observation can be corrected an hour later; the alert must still read as what the
// consultant was actually told at the time, because that is what anyone reviewing the episode
// needs to know.
type CriticalValueAlerted struct {
	AlertID       string `json:"alert_id"`
	FacilityID    string `json:"facility_id"`
	PatientID     string `json:"patient_id"`
	VisitID       string `json:"visit_id,omitempty"`
	ObservationID string `json:"observation_id"`

	Code     string  `json:"code"`
	ValueNum float64 `json:"value_num"`
	Unit     string  `json:"unit,omitempty"`

	// RuleID is the row that fired. Optional in the payload's shape and never absent in
	// practice: a rule deleted years later must not make its own alerts unreadable.
	RuleID string `json:"rule_id,omitempty"`
	// Breached is "low" or "high": which end. Both a screen and a reviewer need it — 3.0 is
	// as urgent as 25.0 and the two mean opposite things.
	Breached  string  `json:"breached"`
	Threshold float64 `json:"threshold"`

	// What to do, in both languages, as the rule said it at the time. Copied for the same
	// reason as the value: an alert is a message that was delivered, and editing the rule
	// afterwards must not rewrite what somebody was told.
	ActionEN string `json:"action_en,omitempty"`
	ActionBN string `json:"action_bn,omitempty"`

	RaisedAt time.Time `json:"raised_at"`
}

func (c CriticalValueAlerted) Validate() error {
	if len(c.AlertID) != 36 || len(c.FacilityID) != 36 || len(c.PatientID) != 36 {
		return errors.New("alert_id, facility_id and patient_id are required")
	}
	if len(c.ObservationID) != 36 {
		return errors.New("observation_id is required: an alert names the value that raised it")
	}
	if strings.TrimSpace(c.Code) == "" {
		return errors.New("code is required")
	}
	if c.Breached != "low" && c.Breached != "high" {
		return fmt.Errorf("breached is %q; it is low or high", c.Breached)
	}
	if c.RaisedAt.IsZero() {
		return errors.New("raised_at is required")
	}
	return nil
}

// CriticalValueDeliveryAttempted records whether the clinic was actually told.
//
// A separate event from the alert itself because it answers a question that cannot be
// answered when the alert is written: the alert is appended inside a transaction, and nothing
// may be published until that transaction has committed. So the alert is raised, the commit
// happens, delivery is attempted, and the outcome is appended as its own fact.
//
// This is criterion 4's evidence. `Recipients` of zero, or a non-empty `Error`, is the reason
// the operator who typed the value is told to go and find somebody — and it is the number the
// clinic should be reading in QA, because a week of undelivered alerts is a week in which the
// safety feature was decorative.
type CriticalValueDeliveryAttempted struct {
	AlertID     string    `json:"alert_id"`
	FacilityID  string    `json:"facility_id"`
	PatientID   string    `json:"patient_id"`
	Recipients  int       `json:"recipients"`
	Error       string    `json:"error,omitempty"`
	AttemptedAt time.Time `json:"attempted_at"`
}

func (c CriticalValueDeliveryAttempted) Validate() error {
	if len(c.AlertID) != 36 || len(c.FacilityID) != 36 || len(c.PatientID) != 36 {
		return errors.New("alert_id, facility_id and patient_id are required")
	}
	if c.Recipients < 0 {
		return errors.New("recipients cannot be negative")
	}
	if c.AttemptedAt.IsZero() {
		return errors.New("attempted_at is required")
	}
	return nil
}

// CriticalValueAcknowledged is a clinician saying they have it.
//
// The note is required, and short. "Seen" is not an acknowledgement; "giving oral glucose,
// rechecking in 15" is. The point of demanding it is not paperwork — it is that the next
// person to open the patient's record needs to know what was already done, and the two
// minutes after a critical value is when nobody has time to write it down twice.
type CriticalValueAcknowledged struct {
	AlertID        string    `json:"alert_id"`
	FacilityID     string    `json:"facility_id"`
	PatientID      string    `json:"patient_id"`
	AcknowledgedBy string    `json:"acknowledged_by"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
	Note           string    `json:"note"`
}

func (c CriticalValueAcknowledged) Validate() error {
	if len(c.AlertID) != 36 || len(c.FacilityID) != 36 || len(c.PatientID) != 36 {
		return errors.New("alert_id, facility_id and patient_id are required")
	}
	if len(c.AcknowledgedBy) != 36 {
		return errors.New("acknowledged_by is required")
	}
	if c.AcknowledgedAt.IsZero() {
		return errors.New("acknowledged_at is required")
	}
	if len(strings.TrimSpace(c.Note)) < 3 {
		return errors.New("an acknowledgement says what is being done about it")
	}
	return nil
}

// CriticalValueEscalated is the chain advancing because nobody answered.
//
// Written by the worker, not by a person, which is why it carries the step and the role
// rather than an actor's intent. A step with no role is the last one: it tells the operator
// who entered the value to go and find somebody, because a chain whose final link is another
// notification has no end.
type CriticalValueEscalated struct {
	AlertID     string    `json:"alert_id"`
	FacilityID  string    `json:"facility_id"`
	PatientID   string    `json:"patient_id"`
	Step        int       `json:"step"`
	NotifyRole  string    `json:"notify_role,omitempty"`
	EscalatedAt time.Time `json:"escalated_at"`
}

func (c CriticalValueEscalated) Validate() error {
	if len(c.AlertID) != 36 || len(c.FacilityID) != 36 || len(c.PatientID) != 36 {
		return errors.New("alert_id, facility_id and patient_id are required")
	}
	if c.Step < 1 {
		return errors.New("step counts from one")
	}
	if c.EscalatedAt.IsZero() {
		return errors.New("escalated_at is required")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Medical history (CP53)
// ---------------------------------------------------------------------------

// HistoryItemRecorded is one thing the patient brought with them: a complaint, another
// condition, something in the family, an operation, a medicine, a vaccination.
//
// **Per item, never per list.** A single HISTORY_TAKEN event carrying twenty items would make
// criterion 4 — every item individually attributed — a property of the list rather than of the
// item, and the question people actually ask is "who wrote *that*". It also makes the
// difference between removing one item and rewriting the history impossible to see.
//
// The coding is three fields and they travel together (CP52). All three may be absent: a
// history officer meets things the catalogue has no code for, and refusing to record them
// would push the item into a note field where nothing can find it. What is never absent is
// `Said` when there is no code — an item that names nothing asserts that the patient has
// something.
type HistoryItemRecorded struct {
	ItemID     string `json:"item_id"`
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`
	VisitID    string `json:"visit_id,omitempty"`

	Kind string `json:"kind"`

	CodeSystem  string `json:"code_system,omitempty"`
	CodeVersion string `json:"code_version,omitempty"`
	Code        string `json:"code,omitempty"`

	// What the patient actually said. Kept beside the coding rather than instead of it: the
	// catalogue's title is "Type 2 diabetes mellitus without complications" and the patient
	// said "sugar since the flood", and the second one is the clinical detail.
	Said string `json:"said,omitempty"`

	Relation       string `json:"relation,omitempty"`
	DurationDays   *int   `json:"duration_days,omitempty"`
	Severity       string `json:"severity,omitempty"`
	OnsetOn        string `json:"onset_on,omitempty"`
	OnsetPrecision string `json:"onset_precision,omitempty"`

	Dose      string `json:"dose,omitempty"`
	Frequency string `json:"frequency,omitempty"`

	// Criterion 2. Null on every item until the formulary exists, which is the honest shape
	// of "where they exist" — the state is recorded per item today so the day the formulary
	// arrives the work is matching rows rather than migrating a record with nowhere to put
	// the answer.
	FormularyProductID string `json:"formulary_product_id,omitempty"`
	Reconciliation     string `json:"reconciliation,omitempty"`

	RecordedAt time.Time `json:"recorded_at"`
}

func (h HistoryItemRecorded) Validate() error {
	if len(h.ItemID) != 36 || len(h.FacilityID) != 36 || len(h.PatientID) != 36 {
		return errors.New("item_id, facility_id and patient_id are required")
	}
	if strings.TrimSpace(h.Kind) == "" {
		return errors.New("kind is required")
	}
	// A coding is all three or none. Two out of three is the failure CP52 exists to prevent,
	// and catching it here means it cannot reach the ledger — where it would be permanent.
	coded := 0
	for _, part := range []string{h.CodeSystem, h.CodeVersion, h.Code} {
		if strings.TrimSpace(part) != "" {
			coded++
		}
	}
	if coded != 0 && coded != 3 {
		return errors.New("a coding is a system, a version and a code, or none of the three")
	}
	if coded == 0 && strings.TrimSpace(h.Said) == "" {
		return errors.New("an uncoded item must say what was meant")
	}
	if h.DurationDays != nil && *h.DurationDays < 0 {
		return errors.New("duration_days cannot be negative")
	}
	if h.RecordedAt.IsZero() {
		return errors.New("recorded_at is required")
	}
	return nil
}

// HistoryItemConfirmed is a person saying that a carried-forward item is still true.
//
// This event **is** acceptance criterion 3. The alternative — a read model that treats last
// month's history as this month's — would eventually assert in a signed document that a
// patient is on a drug they stopped in March, and nobody would be able to say who claimed
// that, because nobody did. Twenty items carried forward is twenty of these.
//
// Who confirmed is read from the envelope, never from the payload: a client that could name
// the confirming user could put a colleague's name on an assertion they never made.
type HistoryItemConfirmed struct {
	ItemID      string    `json:"item_id"`
	PatientID   string    `json:"patient_id"`
	VisitID     string    `json:"visit_id,omitempty"`
	ConfirmedAt time.Time `json:"confirmed_at"`
}

func (h HistoryItemConfirmed) Validate() error {
	if len(h.ItemID) != 36 || len(h.PatientID) != 36 {
		return errors.New("item_id and patient_id are required")
	}
	if h.ConfirmedAt.IsZero() {
		return errors.New("confirmed_at is required")
	}
	return nil
}

// HistoryItemAmended changes what is known about an item that is still the same item.
//
// What it cannot change is what the item *is*: not the kind, not the coding, not who first
// recorded it. Changing those is removing one item and adding another, and collapsing the two
// acts into one is how an audit trail stops answering "when did this become metformin".
//
// Every field is optional and absent means unchanged, which is why they are pointers and
// empty strings rather than a struct of values: a JSON body that omitted `severity` and one
// that set it to "" are different requests, and a screen that clears a field must be able to
// say so.
type HistoryItemAmended struct {
	ItemID    string `json:"item_id"`
	PatientID string `json:"patient_id"`
	VisitID   string `json:"visit_id,omitempty"`

	Said           string `json:"said,omitempty"`
	Severity       string `json:"severity,omitempty"`
	DurationDays   *int   `json:"duration_days,omitempty"`
	OnsetOn        string `json:"onset_on,omitempty"`
	OnsetPrecision string `json:"onset_precision,omitempty"`
	Dose           string `json:"dose,omitempty"`
	Frequency      string `json:"frequency,omitempty"`

	// ACTIVE or RESOLVED. A complaint that settled and a drug that was stopped are the same
	// transition, and neither is a deletion: "she had this and no longer does" is a clinical
	// fact worth more than a missing row.
	Status string `json:"status,omitempty"`

	FormularyProductID string `json:"formulary_product_id,omitempty"`
	Reconciliation     string `json:"reconciliation,omitempty"`

	AmendedAt time.Time `json:"amended_at"`
}

func (h HistoryItemAmended) Validate() error {
	if len(h.ItemID) != 36 || len(h.PatientID) != 36 {
		return errors.New("item_id and patient_id are required")
	}
	if h.Status != "" && h.Status != "ACTIVE" && h.Status != "RESOLVED" {
		return fmt.Errorf("status is %q; it is ACTIVE or RESOLVED", h.Status)
	}
	if h.AmendedAt.IsZero() {
		return errors.New("amended_at is required")
	}
	return nil
}

// HistoryItemRemoved marks an item as one that should not have been recorded.
//
// Distinct from RESOLVED, and the distinction is the point. "She had this and no longer does"
// is a clinical fact; "this was never true" is a correction. A single delete would collapse
// them, and the second one needs a reason attached — because an item somebody removed is an
// item somebody disagreed with, and what they disagreed with is the interesting part.
type HistoryItemRemoved struct {
	ItemID    string    `json:"item_id"`
	PatientID string    `json:"patient_id"`
	VisitID   string    `json:"visit_id,omitempty"`
	Reason    string    `json:"reason"`
	RemovedAt time.Time `json:"removed_at"`
}

func (h HistoryItemRemoved) Validate() error {
	if len(h.ItemID) != 36 || len(h.PatientID) != 36 {
		return errors.New("item_id and patient_id are required")
	}
	if strings.TrimSpace(h.Reason) == "" {
		return errors.New("a removal says why: an item removed for no reason cannot be reviewed")
	}
	if h.RemovedAt.IsZero() {
		return errors.New("removed_at is required")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Allergies (CP54)
// ---------------------------------------------------------------------------

// AllergyRecorded is one substance, what it did, and how sure anybody is.
//
// The substance is coded where the catalogue has it and in words where it does not — the same
// escape hatch a history item carries, and here the argument for it is stronger: an allergy
// nobody could code is far more dangerous sitting in a note field than it is here, marked as
// uncoded and visible on every screen.
type AllergyRecorded struct {
	AllergyID  string `json:"allergy_id"`
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`
	VisitID    string `json:"visit_id,omitempty"`

	CodeSystem  string `json:"code_system,omitempty"`
	CodeVersion string `json:"code_version,omitempty"`
	Code        string `json:"code,omitempty"`
	Said        string `json:"said,omitempty"`

	Reaction  string `json:"reaction"`
	Severity  string `json:"severity"`
	Certainty string `json:"certainty"`
	Note      string `json:"note,omitempty"`

	RecordedAt time.Time `json:"recorded_at"`
}

func (a AllergyRecorded) Validate() error {
	if len(a.AllergyID) != 36 || len(a.FacilityID) != 36 || len(a.PatientID) != 36 {
		return errors.New("allergy_id, facility_id and patient_id are required")
	}
	coded := 0
	for _, part := range []string{a.CodeSystem, a.CodeVersion, a.Code} {
		if strings.TrimSpace(part) != "" {
			coded++
		}
	}
	if coded != 0 && coded != 3 {
		return errors.New("a coding is a system, a version and a code, or none of the three")
	}
	if coded == 0 && strings.TrimSpace(a.Said) == "" {
		return errors.New("an uncoded allergy must name the substance in words")
	}
	if strings.TrimSpace(a.Reaction) == "" {
		return errors.New("reaction is required: an allergy nobody can describe cannot be acted on")
	}
	switch a.Severity {
	case "mild", "moderate", "severe", "life_threatening":
	default:
		return fmt.Errorf("severity is %q", a.Severity)
	}
	switch a.Certainty {
	case "suspected", "confirmed":
	default:
		return fmt.Errorf("certainty is %q; it is suspected or confirmed", a.Certainty)
	}
	if a.RecordedAt.IsZero() {
		return errors.New("recorded_at is required")
	}
	return nil
}

// AllergyStatusAsserted is somebody saying, in their own name, what the allergy answer is.
//
// **This event is acceptance criterion 2.** "No Known Allergies" must never be a default or an
// empty field, and the only way to make that structural is to require a positive act with an
// actor. There is no column anywhere that means "no allergies" by being blank.
//
// `UNABLE_TO_ASSESS` is the third state, and it exists so that there is no override. The
// unconscious patient and the child with no attendant are real, and the usual answer — a
// button that advances them anyway — is a gate with a shape people learn. This is allergy
// status: somebody looked, somebody is named, and the record says what was found, which is
// *not* that there are none.
type AllergyStatusAsserted struct {
	AssertionID string `json:"assertion_id"`
	FacilityID  string `json:"facility_id"`
	PatientID   string `json:"patient_id"`
	VisitID     string `json:"visit_id,omitempty"`

	Kind   string `json:"kind"`
	Reason string `json:"reason,omitempty"`

	AssertedAt time.Time `json:"asserted_at"`
}

func (a AllergyStatusAsserted) Validate() error {
	if len(a.AssertionID) != 36 || len(a.FacilityID) != 36 || len(a.PatientID) != 36 {
		return errors.New("assertion_id, facility_id and patient_id are required")
	}
	switch a.Kind {
	case "NO_KNOWN_ALLERGY":
		if strings.TrimSpace(a.Reason) != "" {
			return errors.New("no known allergies needs no reason")
		}
	case "UNABLE_TO_ASSESS":
		// Required, because the whole point of the third state is that it is reviewable
		// rather than a silent gap. "We could not ask" with no reason cannot be reviewed.
		if strings.TrimSpace(a.Reason) == "" {
			return errors.New("say why the allergy status could not be assessed")
		}
	default:
		return fmt.Errorf("kind is %q", a.Kind)
	}
	if a.AssertedAt.IsZero() {
		return errors.New("asserted_at is required")
	}
	return nil
}

// AllergyWithdrawn takes back an allergy or an assertion that should not have been recorded.
//
// One event for both, because it is one act with one reason and the difference is which id it
// names. What it is *not* is a deletion: an allergy somebody withdrew is an allergy somebody
// disagreed with, and a record that deleted it could not say which of the two happened.
type AllergyWithdrawn struct {
	// Exactly one of these. An event naming both, or neither, is refused.
	AllergyID   string `json:"allergy_id,omitempty"`
	AssertionID string `json:"assertion_id,omitempty"`

	PatientID string `json:"patient_id"`
	VisitID   string `json:"visit_id,omitempty"`

	Reason      string    `json:"reason"`
	WithdrawnAt time.Time `json:"withdrawn_at"`
}

func (a AllergyWithdrawn) Validate() error {
	named := 0
	if len(a.AllergyID) == 36 {
		named++
	}
	if len(a.AssertionID) == 36 {
		named++
	}
	if named != 1 {
		return errors.New("a withdrawal names exactly one allergy or one assertion")
	}
	if len(a.PatientID) != 36 {
		return errors.New("patient_id is required")
	}
	if strings.TrimSpace(a.Reason) == "" {
		return errors.New("a withdrawal says why: one nobody explained cannot be reviewed")
	}
	if a.WithdrawnAt.IsZero() {
		return errors.New("withdrawn_at is required")
	}
	return nil
}

// ---------------------------------------------------------------------------
// The correction workflow (CP62)
// ---------------------------------------------------------------------------

// LifestyleAssessmentRecorded is one questionnaire, answered (CP58, §3 step 3).
//
// # Why the answers are on the event and not only a reference to them
//
// The ledger is the record. A payload holding only a response id would make the answers
// reconstructible from a read model — and a read model is derived, rebuildable and, by design, not
// the truth. A rebuild that had lost the answer rows would have nothing to rebuild them from.
//
// # Why the score of each answer is on it too
//
// The option's score is a property of the *published version* the patient answered, and a published
// version is frozen. Copying it is therefore redundant today and load-bearing the day somebody
// decides a published version can be corrected: last year's totals stay reproducible from the
// ledger alone, without joining a table that has since changed underneath them.
type LifestyleAssessmentRecorded struct {
	ResponseID string `json:"response_id"`
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`
	VisitID    string `json:"visit_id,omitempty"`

	InstrumentCode string `json:"instrument_code"`
	// InstrumentVersion is the wording answered. An instrument whose questions change next year
	// must not make this year's answers read as answers to the new ones.
	InstrumentVersion int `json:"instrument_version"`

	Answers []InstrumentAnswer `json:"answers"`

	RecordedAt time.Time `json:"recorded_at"`
}

// InstrumentAnswer is one item's answer. Exactly one of the three value fields is set, matching
// the item's declared answer type; the database refuses a row that sets more than one.
type InstrumentAnswer struct {
	ItemCode   string   `json:"item_code"`
	OptionCode string   `json:"option_code,omitempty"`
	ValueNum   *float64 `json:"value_num,omitempty"`
	ValueBool  *bool    `json:"value_bool,omitempty"`
	// Score is what this answer was worth under the version answered.
	Score int `json:"score"`
}

// CorrectionRequested is somebody saying a value is wrong, and being specific about it.
//
// The request is a first-class event rather than a row somebody updates, because §4.3's whole
// argument is that the *asking* is the training signal: an operator who never learns they
// mistyped will mistype again, and a workflow that only recorded the fix would keep no count of
// what was asked and by whom.
//
// The reason is a code **and** free text. A code alone cannot say "the tape was against the wall,
// not the patient"; free text alone cannot be counted, and CP63's whole job is counting.
type CorrectionRequested struct {
	RequestID  string `json:"request_id"`
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`
	VisitID    string `json:"visit_id,omitempty"`

	// ObservationID is the value being flagged, and Code is its code — denormalised because
	// counting corrections by category must not depend on a row the correction replaces.
	ObservationID string `json:"observation_id"`
	Code          string `json:"code"`

	ReasonCode string `json:"reason_code"`
	Note       string `json:"note,omitempty"`

	// AssignedTo is whoever typed the value (§4.3). Recorded on the event rather than derived at
	// read time, because the person a request was routed to is a fact about that moment: the
	// value may be corrected by somebody else, and the operator's record is about what *they*
	// were asked to put right.
	AssignedTo string `json:"assigned_to"`

	RequestedAt time.Time `json:"requested_at"`
}

// ExerciseAssessmentRecorded is what station 8 found, and which conditions apply (CP60).
//
// # Why the contraindications are on the event
//
// The permitted exercise list is computed from this row and nothing a client sends. Putting the
// conditions in the ledger rather than only in a read model is what makes that guarantee survive
// a rebuild: replaying this event reconstructs the same filter, and a plan issued last month can
// still be read against the findings that were true when it was issued.
//
// # Why a second assessment supersedes rather than edits
//
// A patient whose foot ulcer healed between visits has a **new answer**, not a corrected one, and
// last month's plan must stay readable against last month's findings. An edit would rewrite the
// justification for a plan that was correct when it was given.
type ExerciseAssessmentRecorded struct {
	AssessmentID string `json:"assessment_id"`
	FacilityID   string `json:"facility_id"`
	PatientID    string `json:"patient_id"`
	VisitID      string `json:"visit_id,omitempty"`

	// WalksUnaided and WalkMinutes are baseline fitness in the two numbers the station can
	// actually obtain. Optional: a patient who cannot say how far they walk still has a
	// contraindication list, and requiring the number would produce invented ones.
	WalksUnaided *bool  `json:"walks_unaided,omitempty"`
	WalkMinutes  *int   `json:"walk_minutes,omitempty"`
	JointPain    string `json:"joint_pain,omitempty"`

	// Contraindications is the set that applies today, and Asked is the set that was **put to
	// the patient**. Two arrays rather than one, and the second is what makes the first honest:
	// with only `contraindications`, "we asked all five and none apply" and "we asked two and
	// skipped the neuropathy question" are byte-identical, and the filter computes the permitted
	// list as though the unasked question had been answered no.
	//
	// **Neither is ever omitted.** "None apply" is the fact the whole filter turns on, and an
	// absent key is indistinguishable from "not asked" — which is the distinction these two
	// fields exist to draw.
	Contraindications []string `json:"contraindications"`
	Asked             []string `json:"asked"`

	Note       string    `json:"note,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Validate refuses an assessment the filter could not be computed from.
func (e ExerciseAssessmentRecorded) Validate() error {
	if len(e.AssessmentID) != 36 || len(e.FacilityID) != 36 || len(e.PatientID) != 36 {
		return errors.New("assessment_id, facility_id and patient_id are required")
	}
	if e.WalkMinutes != nil && (*e.WalkMinutes < 0 || *e.WalkMinutes > 600) {
		return errors.New("walk_minutes is a number of minutes in one day")
	}
	asked := map[string]bool{}
	for _, code := range e.Asked {
		if strings.TrimSpace(code) == "" {
			return errors.New("a question with no code is not a question")
		}
		asked[code] = true
	}
	if len(asked) == 0 {
		// An assessment that asked nothing is not an assessment, and it would exclude the whole
		// library rather than nothing — a failure loud enough to be worth refusing here.
		return errors.New("asked is required: an assessment that put no questions is not an assessment")
	}
	seen := map[string]bool{}
	for _, code := range e.Contraindications {
		if strings.TrimSpace(code) == "" {
			return errors.New("a contraindication with no code is not a condition")
		}
		if seen[code] {
			return errors.New("a contraindication is recorded twice: " + code)
		}
		if !asked[code] {
			// The ledger's own copy of invariant 89. A finding about a question nobody put is
			// either a client bug or a claim nobody made, and both are worse stored than refused.
			return errors.New("a condition is recorded as applying that was not asked about: " + code)
		}
		seen[code] = true
	}
	if e.RecordedAt.IsZero() {
		return errors.New("recorded_at is required")
	}
	return nil
}

// ExerciseTarget is one exercise and how much of it (CP60, §12.1).
//
// Two numbers rather than a sentence, because §12.1's exercise-outcome analysis needs adherence
// comparable across visits from the first patient. "Walk more" is unanalysable; three times a
// week for twenty minutes subtracts from last visit's target.
type ExerciseTarget struct {
	ExerciseCode      string `json:"exercise_code"`
	TimesPerWeek      int    `json:"times_per_week"`
	MinutesPerSession int    `json:"minutes_per_session"`
	Ordering          int    `json:"ordering,omitempty"`
	Note              string `json:"note,omitempty"`
}

// ExercisePlanIssued is the routine a patient was given (CP60, §3 step 8).
//
// # Why it names the assessment
//
// The plan carries the assessment it was filtered against, frozen. Without it, "why was she given
// stair climbing in June" has no answer once her cardiac symptom is recorded in July — the plan
// would look like a mistake rather than a decision that was right on the evidence of the day.
//
// The read model's trigger refuses a contraindicated item on the way in, so a replay of a plan
// that should never have existed fails loudly rather than quietly reproducing it.
type ExercisePlanIssued struct {
	PlanID       string `json:"plan_id"`
	FacilityID   string `json:"facility_id"`
	PatientID    string `json:"patient_id"`
	VisitID      string `json:"visit_id,omitempty"`
	AssessmentID string `json:"assessment_id"`

	Items []ExerciseTarget `json:"items"`

	Note     string    `json:"note,omitempty"`
	IssuedAt time.Time `json:"issued_at"`
}

// Validate refuses a plan that is not a plan.
func (e ExercisePlanIssued) Validate() error {
	if len(e.PlanID) != 36 || len(e.FacilityID) != 36 || len(e.PatientID) != 36 {
		return errors.New("plan_id, facility_id and patient_id are required")
	}
	if len(e.AssessmentID) != 36 {
		// The plan's justification. A plan with no assessment is one nobody can say was safe.
		return errors.New("assessment_id is required: a plan names the findings it was filtered against")
	}
	if len(e.Items) == 0 {
		return errors.New("a plan with no exercises is not a plan")
	}
	seen := map[string]bool{}
	for _, item := range e.Items {
		if strings.TrimSpace(item.ExerciseCode) == "" {
			return errors.New("every target names its exercise")
		}
		if seen[item.ExerciseCode] {
			return errors.New("an exercise is targeted twice: " + item.ExerciseCode)
		}
		seen[item.ExerciseCode] = true
		// Criterion 2, at the ledger rather than only at the read model: a target that is not
		// two numbers is one §12.1 cannot compare, and it would be discovered a year later.
		if item.TimesPerWeek < 1 || item.TimesPerWeek > 14 {
			return errors.New("times_per_week is between once and twice a day: " + item.ExerciseCode)
		}
		if item.MinutesPerSession < 1 || item.MinutesPerSession > 240 {
			return errors.New("minutes_per_session is between a minute and four hours: " + item.ExerciseCode)
		}
	}
	if e.IssuedAt.IsZero() {
		return errors.New("issued_at is required")
	}
	return nil
}

// DietEntryRecorded is one thing a patient said they ate (CP59, station 7).
//
// # Why this is one event per food rather than one per recall
//
// Criterion 2 and [R-01]/[R-02]: two assistants contribute to one recall from two devices,
// concurrently, each attributed. A recall-shaped event would make that a merge — and a merge of two
// lists of food is a design where somebody's breakfast is silently dropped.
//
// One event per item removes the question. Two operators never write the same event, so there is
// nothing to reconcile; the same food entered twice is a *duplicate*, which is a thing a screen
// shows and a person withdraws, not a conflict a system resolves.
//
// # Why the quantity and the measure, and not the grams
//
// A patient says "two cups", not "three hundred grams". The answer as given is the evidence; the
// weight is an interpretation of it, and the projection computes it from the food table so that a
// corrected portion table can be re-derived against. A client that sent grams would be sending its
// own arithmetic, and the calorie count is a number people act on.
type DietEntryRecorded struct {
	EntryID    string `json:"entry_id"`
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`
	VisitID    string `json:"visit_id,omitempty"`

	// RecallDate is the day being recalled, which is usually **yesterday**. A recall taken on
	// Tuesday about Monday's food, recorded as Tuesday, would make every recall a day wrong.
	RecallDate string `json:"recall_date"`
	Meal       string `json:"meal"`
	// EatenAtHour is roughly when, on the clinic's wall clock. Optional: a patient who cannot
	// remember the hour still remembers the meal, and requiring it would produce invented ones.
	EatenAtHour *int `json:"eaten_at_hour,omitempty"`

	FoodCode    string  `json:"food_code"`
	MeasureCode string  `json:"measure_code"`
	Quantity    float64 `json:"quantity"`

	Note       string    `json:"note,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Validate refuses an entry nothing could be computed from.
func (d DietEntryRecorded) Validate() error {
	if len(d.EntryID) != 36 || len(d.FacilityID) != 36 || len(d.PatientID) != 36 {
		return errors.New("entry_id, facility_id and patient_id are required")
	}
	if strings.TrimSpace(d.RecallDate) == "" {
		return errors.New("recall_date is required: a recall with no day is not about anything")
	}
	if strings.TrimSpace(d.Meal) == "" {
		return errors.New("meal is required")
	}
	if strings.TrimSpace(d.FoodCode) == "" || strings.TrimSpace(d.MeasureCode) == "" {
		return errors.New("food_code and measure_code are required")
	}
	if d.Quantity <= 0 {
		return errors.New("quantity must be more than none")
	}
	if d.EatenAtHour != nil && (*d.EatenAtHour < 0 || *d.EatenAtHour > 23) {
		return errors.New("eaten_at_hour is an hour of the day")
	}
	if d.RecordedAt.IsZero() {
		return errors.New("recorded_at is required")
	}
	return nil
}

// DietEntryWithdrawn takes one entry back, with a reason and a name.
//
// Withdrawn rather than deleted, because two operators working one recall will occasionally record
// the same rice twice and the honest correction is one somebody signed — not a row that disappears
// while the other operator is still looking at it.
type DietEntryWithdrawn struct {
	EntryID     string    `json:"entry_id"`
	FacilityID  string    `json:"facility_id"`
	PatientID   string    `json:"patient_id"`
	Reason      string    `json:"reason"`
	WithdrawnAt time.Time `json:"withdrawn_at"`
}

// Validate refuses a withdrawal that says nothing.
func (d DietEntryWithdrawn) Validate() error {
	if len(d.EntryID) != 36 || len(d.FacilityID) != 36 || len(d.PatientID) != 36 {
		return errors.New("entry_id, facility_id and patient_id are required")
	}
	if strings.TrimSpace(d.Reason) == "" {
		return errors.New("a reason is required: an entry that vanishes with no reason is a gap somebody has to explain")
	}
	if d.WithdrawnAt.IsZero() {
		return errors.New("withdrawn_at is required")
	}
	return nil
}

// Validate refuses a response nothing downstream could score.
func (l LifestyleAssessmentRecorded) Validate() error {
	if len(l.ResponseID) != 36 || len(l.FacilityID) != 36 || len(l.PatientID) != 36 {
		return errors.New("response_id, facility_id and patient_id are required")
	}
	if strings.TrimSpace(l.InstrumentCode) == "" {
		return errors.New("instrument_code is required")
	}
	if l.InstrumentVersion < 1 {
		return errors.New("instrument_version is required: an answer must name the wording it answered")
	}
	if len(l.Answers) == 0 {
		// Criterion 1, enforced at the ledger rather than only at the read model: a response
		// with no items is a questionnaire nobody filled in, stored as though somebody had.
		return errors.New("a response with no answers is not a response")
	}
	seen := map[string]bool{}
	for _, a := range l.Answers {
		if strings.TrimSpace(a.ItemCode) == "" {
			return errors.New("every answer names its item")
		}
		if seen[a.ItemCode] {
			return errors.New("an item is answered twice: " + a.ItemCode)
		}
		seen[a.ItemCode] = true

		given := 0
		if a.OptionCode != "" {
			given++
		}
		if a.ValueNum != nil {
			given++
		}
		if a.ValueBool != nil {
			given++
		}
		if given != 1 {
			return errors.New("every answer carries exactly one value: " + a.ItemCode)
		}
	}
	if l.RecordedAt.IsZero() {
		return errors.New("recorded_at is required")
	}
	return nil
}

func (c CorrectionRequested) Validate() error {
	if len(c.RequestID) != 36 || len(c.FacilityID) != 36 || len(c.PatientID) != 36 {
		return errors.New("request_id, facility_id and patient_id are required")
	}
	if len(c.ObservationID) != 36 {
		return errors.New("observation_id is required: a flag is about one value")
	}
	if strings.TrimSpace(c.Code) == "" {
		return errors.New("code is required")
	}
	if strings.TrimSpace(c.ReasonCode) == "" {
		return errors.New("a reason code is required: a flag that does not say why cannot be counted")
	}
	if len(c.AssignedTo) != 36 {
		return errors.New("assigned_to is required: a request nobody is asked to answer is a note")
	}
	if c.RequestedAt.IsZero() {
		return errors.New("requested_at is required")
	}
	return nil
}

// CorrectionApplied is the person who typed the value putting it right.
//
// `ReplacementID` names the observation that now holds the value. The original is untouched: it
// keeps its row, stops being ACTIVE, and stays queryable — criterion 1 is that a correction adds
// rather than edits.
type CorrectionApplied struct {
	RequestID     string `json:"request_id"`
	FacilityID    string `json:"facility_id"`
	PatientID     string `json:"patient_id"`
	ObservationID string `json:"observation_id"`
	ReplacementID string `json:"replacement_id"`
	Note          string `json:"note,omitempty"`

	// Recomputed names the derived values that were recomputed because this one changed
	// (criterion 3). Recorded so that "what else moved when this moved" is answerable from the
	// ledger rather than by re-deriving history.
	Recomputed []string `json:"recomputed,omitempty"`

	AppliedAt time.Time `json:"applied_at"`
}

func (c CorrectionApplied) Validate() error {
	if len(c.RequestID) != 36 || len(c.FacilityID) != 36 || len(c.PatientID) != 36 {
		return errors.New("request_id, facility_id and patient_id are required")
	}
	if len(c.ObservationID) != 36 || len(c.ReplacementID) != 36 {
		return errors.New("observation_id and replacement_id are required")
	}
	if c.AppliedAt.IsZero() {
		return errors.New("applied_at is required")
	}
	return nil
}

// SupervisorOverrideApplied is somebody other than the author correcting the value.
//
// **Its own event, deliberately.** A boolean on `CORRECTION_APPLIED` would be a flag people
// forget to read, and the thing it distinguishes matters: a supervisor's correction must not land
// on the operator's quality record as though they had put it right themselves, and an operator
// who was never given the chance to fix their own mistake has not been given the training signal
// §4.3 exists to create. The patient in front of a physician cannot wait for somebody who has
// gone home, so the valve exists — and it is legible.
type SupervisorOverrideApplied struct {
	RequestID     string `json:"request_id"`
	FacilityID    string `json:"facility_id"`
	PatientID     string `json:"patient_id"`
	ObservationID string `json:"observation_id"`
	ReplacementID string `json:"replacement_id"`

	// AssignedTo is who the request was routed to and did not answer. On the event because the
	// question a supervisor's override raises is "why did the author not do this".
	AssignedTo string `json:"assigned_to"`
	Note       string `json:"note,omitempty"`

	Recomputed []string  `json:"recomputed,omitempty"`
	AppliedAt  time.Time `json:"applied_at"`
}

func (s SupervisorOverrideApplied) Validate() error {
	if len(s.RequestID) != 36 || len(s.FacilityID) != 36 || len(s.PatientID) != 36 {
		return errors.New("request_id, facility_id and patient_id are required")
	}
	if len(s.ObservationID) != 36 || len(s.ReplacementID) != 36 {
		return errors.New("observation_id and replacement_id are required")
	}
	if len(s.AssignedTo) != 36 {
		return errors.New("assigned_to is required: an override is about a request somebody else held")
	}
	if s.AppliedAt.IsZero() {
		return errors.New("applied_at is required")
	}
	return nil
}

// CorrectionRejected is the author saying the value is right as it stands.
//
// A reason is required. "No" with no reason is how a flagging culture dies: the physician who
// flagged it learns nothing, cannot tell a disagreement from an oversight, and stops flagging.
type CorrectionRejected struct {
	RequestID     string `json:"request_id"`
	FacilityID    string `json:"facility_id"`
	PatientID     string `json:"patient_id"`
	ObservationID string `json:"observation_id"`
	Reason        string `json:"reason"`

	RejectedAt time.Time `json:"rejected_at"`
}

func (c CorrectionRejected) Validate() error {
	if len(c.RequestID) != 36 || len(c.FacilityID) != 36 || len(c.PatientID) != 36 {
		return errors.New("request_id, facility_id and patient_id are required")
	}
	if len(c.ObservationID) != 36 {
		return errors.New("observation_id is required")
	}
	if strings.TrimSpace(c.Reason) == "" {
		return errors.New("a reason is required to reject a correction")
	}
	if c.RejectedAt.IsZero() {
		return errors.New("rejected_at is required")
	}
	return nil
}

// ---------------------------------------------------------------------------
// The gates a visit passes through (CP57)
// ---------------------------------------------------------------------------

// VisitGateBlocked is a patient stopped at a checkpoint, and what was missing.
//
// Recorded rather than merely refused, because the interesting number is not how often the gate
// held — it is which items keep being the ones missing at half past eleven. A refusal that left
// no trace would make the gate's own effectiveness unmeasurable, and the plan's mitigation for
// clinic-floor friction is *rate monitoring*, which needs rows.
//
// `Gate` names which checkpoint, so CP83's QA clearance uses the same two events rather than
// inventing a second vocabulary for the same fact.
type VisitGateBlocked struct {
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`
	VisitID    string `json:"visit_id"`

	Gate    string `json:"gate"`
	Station string `json:"station"`

	// Missing is what was outstanding at the moment of the refusal, by item code. Item codes
	// rather than text: the text belongs to a version, and the code is what survives a
	// rewording.
	Missing []string `json:"missing,omitempty"`

	BlockedAt time.Time `json:"blocked_at"`
}

func (v VisitGateBlocked) Validate() error {
	if len(v.FacilityID) != 36 || len(v.PatientID) != 36 || len(v.VisitID) != 36 {
		return errors.New("facility_id, patient_id and visit_id are required")
	}
	if strings.TrimSpace(v.Gate) == "" {
		return errors.New("gate is required: a refusal that does not say which checkpoint " +
			"stopped the patient cannot be acted on")
	}
	if v.BlockedAt.IsZero() {
		return errors.New("blocked_at is required")
	}
	return nil
}

// VisitGateSatisfied is a patient passing a checkpoint that had something to check.
//
// Not written for every queue entry — that would be a log of the whole clinic. It is written
// when a gate was evaluated and found nothing outstanding, which is the fact a QA review needs
// beside the refusals: "held 14 times, passed 300" is a working gate; "held 14 times, passed 14"
// is a gate nobody can get through.
type VisitGateSatisfied struct {
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`
	VisitID    string `json:"visit_id"`

	Gate    string `json:"gate"`
	Station string `json:"station"`

	// Overridden says the gate let the patient through on a recorded override rather than
	// because the work was done. Both are "satisfied" to the queue and they are not the same
	// clinical fact, so the event says which.
	Overridden bool `json:"overridden,omitempty"`

	SatisfiedAt time.Time `json:"satisfied_at"`
}

func (v VisitGateSatisfied) Validate() error {
	if len(v.FacilityID) != 36 || len(v.PatientID) != 36 || len(v.VisitID) != 36 {
		return errors.New("facility_id, patient_id and visit_id are required")
	}
	if strings.TrimSpace(v.Gate) == "" {
		return errors.New("gate is required")
	}
	if v.SatisfiedAt.IsZero() {
		return errors.New("satisfied_at is required")
	}
	return nil
}

// CounselingGateOverridden is somebody letting a patient past the counselling gate.
//
// The valve, and every field on it exists to make the valve legible. `Missing` is what was
// outstanding *at the moment it was granted* rather than something recomputed later, because
// items ticked afterwards would make a recomputed list say the override was for nothing. The
// reason is required here, in a CHECK constraint and in the handler — an override with no reason
// is the failure the whole design is arranged around.
type CounselingGateOverridden struct {
	OverrideID string `json:"override_id"`
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`
	VisitID    string `json:"visit_id"`

	Reason  string   `json:"reason"`
	Missing []string `json:"missing,omitempty"`

	GrantedAt time.Time `json:"granted_at"`
}

func (c CounselingGateOverridden) Validate() error {
	if len(c.OverrideID) != 36 || len(c.FacilityID) != 36 || len(c.PatientID) != 36 {
		return errors.New("override_id, facility_id and patient_id are required")
	}
	if len(c.VisitID) != 36 {
		return errors.New("visit_id is required: an override is granted for one visit")
	}
	if strings.TrimSpace(c.Reason) == "" {
		return errors.New("a reason is required to override the counselling gate")
	}
	if c.GrantedAt.IsZero() {
		return errors.New("granted_at is required")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Counselling on the floor (CP56)
// ---------------------------------------------------------------------------

// CounselingSessionStarted opens one walk through one checklist, for one visit.
//
// The template *version* travels in the payload rather than being resolved when the session is
// read. That is CP55's criterion 2 written as an event: a checklist republished while a
// counsellor is halfway down it does not change what this patient was asked about, and the
// ledger says which list it was even if every row in the read model is rebuilt.
type CounselingSessionStarted struct {
	SessionID  string `json:"session_id"`
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`
	VisitID    string `json:"visit_id"`

	TemplateID      string `json:"template_id"`
	TemplateVersion int    `json:"template_version"`

	StartedAt time.Time `json:"started_at"`
}

func (c CounselingSessionStarted) Validate() error {
	if len(c.SessionID) != 36 || len(c.FacilityID) != 36 || len(c.PatientID) != 36 {
		return errors.New("session_id, facility_id and patient_id are required")
	}
	if len(c.VisitID) != 36 {
		return errors.New("visit_id is required: a counselling session belongs to a visit")
	}
	if len(c.TemplateID) != 36 {
		return errors.New("template_id is required")
	}
	if c.TemplateVersion < 1 {
		return errors.New("template_version counts from one")
	}
	if c.StartedAt.IsZero() {
		return errors.New("started_at is required")
	}
	return nil
}

// CounselingItemTicked is one item covered, by one person, at one time.
//
// **This event is acceptance criterion 1.** One event per item, never per list: a single
// COUNSELING_SESSION_COMPLETED carrying seven item codes would make "who covered this" a
// property of the session, and §5.4's whole method is the physician asking the patient about
// one item and being able to find who taught it.
//
// The actor is not in the payload. It is in the envelope, like every other attribution in this
// system, because a client that could name the ticking user could put a colleague's name on
// counselling they never gave.
type CounselingItemTicked struct {
	SessionID  string `json:"session_id"`
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`
	VisitID    string `json:"visit_id"`

	ItemCode string `json:"item_code"`
	// §5.3's optional per-item note: what this counsellor wants the physician to know about
	// this item for this patient. Optional because most items have nothing to add and a
	// required note is a note people fill with a full stop.
	Note string `json:"note,omitempty"`

	TickedAt time.Time `json:"ticked_at"`
}

func (c CounselingItemTicked) Validate() error {
	if len(c.SessionID) != 36 || len(c.FacilityID) != 36 || len(c.PatientID) != 36 {
		return errors.New("session_id, facility_id and patient_id are required")
	}
	if strings.TrimSpace(c.ItemCode) == "" {
		return errors.New("item_code is required")
	}
	if c.TickedAt.IsZero() {
		return errors.New("ticked_at is required")
	}
	return nil
}

// CounselingItemUnticked takes a tick back, and says why.
//
// **This event is acceptance criterion 3.** The reason is required here rather than only in the
// handler, because the ledger is the copy that outlives every handler — and an un-tick with no
// reason is indistinguishable from a mis-tap, which is the one thing a quality review needs to
// tell apart.
type CounselingItemUnticked struct {
	SessionID  string `json:"session_id"`
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`
	VisitID    string `json:"visit_id"`

	ItemCode string `json:"item_code"`
	Reason   string `json:"reason"`

	UntickedAt time.Time `json:"unticked_at"`
}

func (c CounselingItemUnticked) Validate() error {
	if len(c.SessionID) != 36 || len(c.FacilityID) != 36 || len(c.PatientID) != 36 {
		return errors.New("session_id, facility_id and patient_id are required")
	}
	if strings.TrimSpace(c.ItemCode) == "" {
		return errors.New("item_code is required")
	}
	if strings.TrimSpace(c.Reason) == "" {
		return errors.New("a reason is required to un-tick an item")
	}
	if c.UntickedAt.IsZero() {
		return errors.New("unticked_at is required")
	}
	return nil
}

// CounselingSessionCompleted is the counsellor saying they are finished.
//
// It ticks nothing. A completion that also covered the outstanding items would be exactly the
// batch attribution criterion 1 forbids, and it would let a session be closed by somebody who
// counselled nobody.
type CounselingSessionCompleted struct {
	SessionID  string `json:"session_id"`
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`
	VisitID    string `json:"visit_id"`

	CompletedAt time.Time `json:"completed_at"`
}

func (c CounselingSessionCompleted) Validate() error {
	if len(c.SessionID) != 36 || len(c.FacilityID) != 36 || len(c.PatientID) != 36 {
		return errors.New("session_id, facility_id and patient_id are required")
	}
	if c.CompletedAt.IsZero() {
		return errors.New("completed_at is required")
	}
	return nil
}

// The pre-consultation synthesis (CP71, §7.1).
//
// Three types, not one with a status field. The three are asked about separately and by different
// people: "how often does the automatic trigger actually fire" is criterion 2, "how many summaries
// made the five minutes" is criterion 1, and "what failed and how often" is the D-15 degradation
// somebody has to notice. A single AI_SYNTHESIS event with an outcome column would make all three
// the same query with a filter, and the filter is what people forget.
//
// **None of these carries the summary.** The narrative lives in `core.ai_synthesis` and nowhere in
// the ledger, because §10.6's first permanent invariant is that AI never writes to the clinical
// record. What these events record is that the system *did something* about a visit — an
// operational fact with an actor and a time, which is precisely what the ledger is for.

// AISynthesisRequested is a run being asked for, automatically or by the button.
type AISynthesisRequested struct {
	FacilityID  string `json:"facility_id"`
	PatientID   string `json:"patient_id"`
	VisitID     string `json:"visit_id"`
	SynthesisID string `json:"synthesis_id"`

	AgentCode string `json:"agent_code"`
	// Trigger is AUTOMATIC, MANUAL or RERUN. Criterion 2 — *"the physician never needs to trigger
	// it manually in normal flow"* — is a claim about the ratio between the first two, and this is
	// the only field in the system that can settle it.
	Trigger    string `json:"trigger"`
	Generation int    `json:"generation"`

	RequestedAt time.Time `json:"requested_at"`
}

func (a AISynthesisRequested) Validate() error {
	if len(a.FacilityID) != 36 || len(a.PatientID) != 36 || len(a.VisitID) != 36 || len(a.SynthesisID) != 36 {
		return errors.New("facility_id, patient_id, visit_id and synthesis_id are required")
	}
	switch a.Trigger {
	case "AUTOMATIC", "MANUAL", "RERUN":
	default:
		return fmt.Errorf("%q is not how a synthesis is triggered", a.Trigger)
	}
	if strings.TrimSpace(a.AgentCode) == "" {
		return errors.New("agent_code is required")
	}
	if a.Generation < 1 {
		return errors.New("generation starts at 1")
	}
	if a.RequestedAt.IsZero() {
		return errors.New("requested_at is required")
	}
	return nil
}

// AISuggestionDecided is a physician accepting, editing or rejecting one item in §8's right
// panel (CP73).
//
// # Why this is an event and not a column
//
// It is the only record that a physician *considered* a machine's proposal. A screen that
// hid a rejected suggestion would leave no evidence it was ever weighed, which is the wrong
// record in both directions: medico-legally, "the system suggested a thyroid function test
// and the physician declined it" is a defensible sentence and an absent one is not; and
// operationally, the rejection rate per kind is the only measurement that says whether the
// panel earns the attention it costs.
//
// # What an acceptance is, and what it is not
//
// **An intent, never a prescription.** §7.3 makes the split permanent — generative models
// draft, deterministic databases and a human signature prescribe — and CP81 owns the writing
// of a drug into a prescription with its interaction check, its dose validation and its
// signature. Accepting a drafted metformin here puts this row in the ledger and puts nothing
// on any prescription.
//
// # Why the label and the kind are copied onto the payload
//
// A synthesis run is superseded by every re-run. A ledger row that could only be read by
// resolving a run that has since been replaced four times is a row that stops being readable
// exactly when somebody needs it — during a review, years later, of a decision they are being
// asked about. So the suggestion's own words travel with the decision.
type AISuggestionDecided struct {
	FacilityID string `json:"facility_id"`
	PatientID  string `json:"patient_id"`
	VisitID    string `json:"visit_id"`

	// Ref is the suggestion's stable handle: `diagnosis:type_2_diabetes_mellitus`. Derived
	// from the kind and the item's own text rather than from its position in the model's
	// answer, so that a re-read which returns the same items in a different order does not
	// move every decision onto the wrong line.
	Ref string `json:"ref"`
	// SuggestionKind is DIAGNOSIS, INVESTIGATION, MEDICATION or GAP.
	SuggestionKind string `json:"suggestion_kind"`
	// Origin is MODEL or SYSTEM. On the record because they are different acts: agreeing with
	// a language model's diagnosis and acknowledging that the assembler found no HbA1c are
	// not the same decision, and a rate computed over both would mean nothing.
	Origin string `json:"origin"`
	Label  string `json:"label"`

	Decision string `json:"decision"`
	// Edited is the physician's own wording, for an EDITED decision. Required for that one
	// and refused for the others: a record saying somebody changed something without saying
	// what is worse than no record.
	Edited string `json:"edited,omitempty"`
	Note   string `json:"note,omitempty"`

	// Generation is the synthesis run this was decided against. A decision made on generation
	// 2 and shown beside generation 3's suggestion is a decision about a different sentence,
	// and without this field nothing could tell them apart.
	Generation int       `json:"generation"`
	DecidedAt  time.Time `json:"decided_at"`
}

func (a AISuggestionDecided) Validate() error {
	if len(a.FacilityID) != 36 || len(a.PatientID) != 36 || len(a.VisitID) != 36 {
		return errors.New("facility_id, patient_id and visit_id are required")
	}
	if strings.TrimSpace(a.Ref) == "" {
		return errors.New("a decision names the suggestion it is about")
	}
	switch a.Decision {
	case "ACCEPTED", "EDITED", "REJECTED":
	default:
		return fmt.Errorf("%q is not a decision on a suggestion", a.Decision)
	}
	switch a.SuggestionKind {
	case "DIAGNOSIS", "INVESTIGATION", "MEDICATION", "GAP":
	default:
		return fmt.Errorf("%q is not a kind of suggestion", a.SuggestionKind)
	}
	switch a.Origin {
	case "MODEL", "SYSTEM":
	default:
		return fmt.Errorf("%q is not an origin; a suggestion is written by a model or derived by the assembler", a.Origin)
	}
	if strings.TrimSpace(a.Label) == "" {
		return errors.New("a decision carries the suggestion's own words")
	}
	// The one shape that would produce a record saying a physician changed something without
	// saying what. Refused in the ledger as well as in the service, because the ledger is what
	// a review reads and the service is what a future caller might forget to go through.
	if a.Decision == "EDITED" && strings.TrimSpace(a.Edited) == "" {
		return errors.New("an edited suggestion carries the edited wording")
	}
	if a.Decision != "EDITED" && strings.TrimSpace(a.Edited) != "" {
		return errors.New("only an edited suggestion carries edited wording")
	}
	if a.Generation < 1 {
		return errors.New("a decision names the generation it was made against")
	}
	if a.DecidedAt.IsZero() {
		return errors.New("decided_at is required")
	}
	return nil
}

// AISynthesisCompleted is a run that finished with something to show — or with a considered
// decision that there was nothing new to say.
type AISynthesisCompleted struct {
	FacilityID  string `json:"facility_id"`
	PatientID   string `json:"patient_id"`
	VisitID     string `json:"visit_id"`
	SynthesisID string `json:"synthesis_id"`

	AgentCode string `json:"agent_code"`
	// PromptVersion and ModelVersion are §10.6's fourth permanent invariant, in the ledger as well
	// as on the row: an interaction from eight months ago has to be resolvable to the exact text
	// and model that produced it, and the ledger is the copy nobody can update.
	PromptVersion string `json:"prompt_version,omitempty"`
	ModelVersion  string `json:"model_version,omitempty"`

	Generation int `json:"generation"`
	// State is READY or UNCHANGED. UNCHANGED is a completion: the record was checked and was
	// already current, which is a promise kept rather than work skipped.
	State string `json:"state"`
	// MetSLA is nil when the kind carried no deadline. Never omitted otherwise — a false here is
	// the whole point of measuring §7.1's five minutes.
	MetSLA *bool `json:"met_sla"`

	CompletedAt time.Time `json:"completed_at"`
}

func (a AISynthesisCompleted) Validate() error {
	if len(a.FacilityID) != 36 || len(a.PatientID) != 36 || len(a.VisitID) != 36 || len(a.SynthesisID) != 36 {
		return errors.New("facility_id, patient_id, visit_id and synthesis_id are required")
	}
	switch a.State {
	case "READY", "UNCHANGED":
	default:
		return fmt.Errorf("%q is not a completed synthesis state", a.State)
	}
	if a.State == "READY" && (strings.TrimSpace(a.PromptVersion) == "" || strings.TrimSpace(a.ModelVersion) == "") {
		// A summary a physician can read has to name what produced it, in the ledger too. The row
		// carries a check constraint saying the same thing; this is the copy that cannot be
		// updated afterwards.
		return errors.New("a READY synthesis records the prompt version and model version that produced it")
	}
	if a.Generation < 1 {
		return errors.New("generation starts at 1")
	}
	if a.CompletedAt.IsZero() {
		return errors.New("completed_at is required")
	}
	return nil
}

// AISynthesisFailed is a run that stopped, with the class of failure a physician's screen will name.
type AISynthesisFailed struct {
	FacilityID  string `json:"facility_id"`
	PatientID   string `json:"patient_id"`
	VisitID     string `json:"visit_id"`
	SynthesisID string `json:"synthesis_id"`

	AgentCode  string `json:"agent_code"`
	Generation int    `json:"generation"`
	// FailureKind is the class, not the message. The message is on the row and in the outbound
	// log; what belongs in the ledger is the fact somebody can count — a clinic where REFUSED
	// climbs has a configuration problem and a clinic where PROVIDER climbs has an outage, and
	// those need different people woken up.
	FailureKind string `json:"failure_kind"`

	FailedAt time.Time `json:"failed_at"`
}

func (a AISynthesisFailed) Validate() error {
	if len(a.FacilityID) != 36 || len(a.PatientID) != 36 || len(a.VisitID) != 36 || len(a.SynthesisID) != 36 {
		return errors.New("facility_id, patient_id, visit_id and synthesis_id are required")
	}
	switch a.FailureKind {
	// UNGROUNDED is CP72's, and it is the reason this list is worth keeping rather than replacing
	// with a length check: the ledger is where "how often did the model invent something" is
	// counted from, and a kind the ledger silently accepted under some other name would make that
	// count wrong in the one direction nobody would notice.
	//
	// It was added the hard way. The manual verification corrupted a model answer, the summary was
	// correctly withheld, and *the event could not be appended* — the row and the physician's
	// screen were right and the ledger had a hole in it, which is exactly the failure a manual
	// verification exists to find and which no unit test in this repository was looking for.
	case "ASSEMBLY", "REFUSED", "PROVIDER", "TIMEOUT", "INVALID_OUTPUT", "INTERNAL", "UNGROUNDED":
	default:
		return fmt.Errorf("%q is not a kind of synthesis failure", a.FailureKind)
	}
	if a.Generation < 1 {
		return errors.New("generation starts at 1")
	}
	if a.FailedAt.IsZero() {
		return errors.New("failed_at is required")
	}
	return nil
}

// ---------------------------------------------------------------------------

func init() {
	measurement := func() Payload { return &Measurement{} }
	for _, name := range []string{"HEIGHT_RECORDED", "HEIGHT_CORRECTED", "WEIGHT_RECORDED", "WEIGHT_CORRECTED",
		"WAIST_RECORDED", "HIP_RECORDED", "PULSE_RECORDED", "SPO2_RECORDED", "TEMP_RECORDED"} {
		Default.Register(Type{Name: name, Version: 1, Aggregate: "VISIT", New: measurement})
	}
	Default.Register(Type{Name: "BP_RECORDED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &BloodPressure{} }})
	Default.Register(Type{Name: "BP_CORRECTED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &BloodPressure{} }})
	Default.Register(Type{Name: "ALLERGY_RECORDED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &AllergyRecorded{} }})
	Default.Register(Type{Name: "ALLERGY_STATUS_ASSERTED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &AllergyStatusAsserted{} }})
	Default.Register(Type{Name: "ALLERGY_WITHDRAWN", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &AllergyWithdrawn{} }})
	Default.Register(Type{Name: "HISTORY_ITEM_RECORDED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &HistoryItemRecorded{} }})
	Default.Register(Type{Name: "HISTORY_ITEM_CONFIRMED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &HistoryItemConfirmed{} }})
	Default.Register(Type{Name: "HISTORY_ITEM_AMENDED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &HistoryItemAmended{} }})
	Default.Register(Type{Name: "HISTORY_ITEM_REMOVED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &HistoryItemRemoved{} }})
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
	// Critical values (CP50). Four types rather than three, because "the clinic was told"
	// is a different fact from "the value was dangerous", and only the first of those can
	// be known after the transaction has committed.
	Default.Register(Type{Name: "CRITICAL_VALUE_ALERTED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &CriticalValueAlerted{} }})
	Default.Register(Type{Name: "CRITICAL_VALUE_DELIVERY_ATTEMPTED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &CriticalValueDeliveryAttempted{} }})
	Default.Register(Type{Name: "CRITICAL_VALUE_ACKNOWLEDGED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &CriticalValueAcknowledged{} }})
	Default.Register(Type{Name: "CRITICAL_VALUE_ESCALATED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &CriticalValueEscalated{} }})
	// Counselling (CP56). Four types, and the split is criterion 1: a tick is its own event
	// with its own actor, so "who covered injection sites" has an answer per item rather than
	// per session.
	Default.Register(Type{Name: "COUNSELING_SESSION_STARTED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &CounselingSessionStarted{} }})
	Default.Register(Type{Name: "COUNSELING_ITEM_TICKED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &CounselingItemTicked{} }})
	Default.Register(Type{Name: "COUNSELING_ITEM_UNTICKED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &CounselingItemUnticked{} }})
	Default.Register(Type{Name: "COUNSELING_SESSION_COMPLETED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &CounselingSessionCompleted{} }})
	// The gates a visit passes (CP57). Two generic events rather than a counselling-shaped
	// pair, because CP83's QA clearance is the same fact about a different checkpoint — and an
	// override is its own event because it is the one act here somebody has to answer for.
	Default.Register(Type{Name: "VISIT_GATE_BLOCKED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &VisitGateBlocked{} }})
	Default.Register(Type{Name: "VISIT_GATE_SATISFIED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &VisitGateSatisfied{} }})
	Default.Register(Type{Name: "COUNSELING_GATE_OVERRIDDEN", Version: 1, Aggregate: "VISIT", New: func() Payload { return &CounselingGateOverridden{} }})
	// The correction workflow (CP62). Four types, and the split between the third and the
	// second is the checkpoint: a supervisor correcting somebody else's value is a different
	// fact from an author correcting their own, and a boolean on one event is a flag people
	// forget to read.
	Default.Register(Type{Name: "LIFESTYLE_ASSESSMENT_RECORDED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &LifestyleAssessmentRecorded{} }})
	Default.Register(Type{Name: "DIET_ENTRY_RECORDED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &DietEntryRecorded{} }})
	Default.Register(Type{Name: "DIET_ENTRY_WITHDRAWN", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &DietEntryWithdrawn{} }})
	Default.Register(Type{Name: "CORRECTION_REQUESTED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &CorrectionRequested{} }})
	Default.Register(Type{Name: "CORRECTION_APPLIED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &CorrectionApplied{} }})
	Default.Register(Type{Name: "SUPERVISOR_OVERRIDE_APPLIED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &SupervisorOverrideApplied{} }})
	Default.Register(Type{Name: "CORRECTION_REJECTED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &CorrectionRejected{} }})
	// Station 8 (CP60). Two types rather than one, because the assessment and the plan are
	// separate acts by possibly separate people: the findings are what the filter reads, and a
	// plan that welded them together could not be re-issued without re-asking the questions.
	Default.Register(Type{Name: "EXERCISE_ASSESSMENT_RECORDED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &ExerciseAssessmentRecorded{} }})
	Default.Register(Type{Name: "EXERCISE_PLAN_ISSUED", Version: 1, Aggregate: "PATIENT", New: func() Payload { return &ExercisePlanIssued{} }})
	// The pre-consultation synthesis (CP71). On the VISIT aggregate, because a summary is about
	// one journey through the clinic rather than about the patient: the same person next month
	// gets a different briefing from a different set of stations.
	Default.Register(Type{Name: "AI_SYNTHESIS_REQUESTED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &AISynthesisRequested{} }})
	Default.Register(Type{Name: "AI_SYNTHESIS_COMPLETED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &AISynthesisCompleted{} }})
	Default.Register(Type{Name: "AI_SYNTHESIS_FAILED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &AISynthesisFailed{} }})
	// The physician's answer to one of §8's drafted suggestions (CP73). On the VISIT aggregate
	// beside the run that produced the suggestion, because a decision is about one
	// consultation: the same physician meeting the same patient next month is answering a
	// different draft about a different set of measurements.
	Default.Register(Type{Name: "AI_SUGGESTION_DECIDED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &AISuggestionDecided{} }})
}
