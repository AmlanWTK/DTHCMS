package eventstore

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The registry and the hash, without a database.

type heightV1 struct {
	HeightCm float64 `json:"height_cm"`
}

func (h heightV1) Validate() error { return nil }

func TestAnOldVersionIsUpcastToTheCurrentOne(t *testing.T) {
	r := NewRegistry()
	r.Register(Type{
		Name: "TEST_HEIGHT", Version: 1, Aggregate: "VISIT",
		New: func() Payload { return &heightV1{} },
		// v1 stored a bare number in centimetres; v2 is the Measurement shape.
		Upcast: func(raw json.RawMessage) (json.RawMessage, error) {
			var old heightV1
			if err := json.Unmarshal(raw, &old); err != nil {
				return nil, err
			}
			return json.Marshal(Measurement{Code: "HEIGHT", Value: old.HeightCm, Unit: "cm"})
		},
	})
	r.Register(Type{Name: "TEST_HEIGHT", Version: 2, Aggregate: "VISIT", New: func() Payload { return &Measurement{} }})

	if r.Current("TEST_HEIGHT") != 2 {
		t.Fatalf("current = %d", r.Current("TEST_HEIGHT"))
	}
	// An archived v1 payload, exactly as a client of the time sent it.
	raw, version, err := r.Upcast("TEST_HEIGHT", 1, json.RawMessage(`{"height_cm": 150}`))
	if err != nil || version != 2 {
		t.Fatalf("upcast: v%d %v", version, err)
	}
	p, err := r.Decode("TEST_HEIGHT", version, raw)
	if err != nil {
		t.Fatal(err)
	}
	m := p.(*Measurement)
	if m.Code != "HEIGHT" || m.Value != 150 || m.Unit != "cm" {
		t.Errorf("upcast payload = %+v", m)
	}
	// A current payload passes through untouched.
	if _, v, err := r.Upcast("TEST_HEIGHT", 2, raw); err != nil || v != 2 {
		t.Errorf("current: v%d %v", v, err)
	}
	// A version with no path forward is an error, not a silent pass.
	r.Register(Type{Name: "TEST_ORPHAN", Version: 1, Aggregate: "VISIT", New: func() Payload { return &heightV1{} }})
	r.Register(Type{Name: "TEST_ORPHAN", Version: 2, Aggregate: "VISIT", New: func() Payload { return &heightV1{} }})
	if _, _, err := r.Upcast("TEST_ORPHAN", 1, raw); err == nil || !strings.Contains(err.Error(), "no upcaster") {
		t.Errorf("orphaned version: %v", err)
	}
}

func TestRegisteringTwicePanics(t *testing.T) {
	r := NewRegistry()
	r.Register(Type{Name: "X_DONE", Version: 1, Aggregate: "VISIT", New: func() Payload { return &heightV1{} }})
	defer func() {
		if recover() == nil {
			t.Fatal("a duplicate registration did not panic")
		}
	}()
	r.Register(Type{Name: "X_DONE", Version: 1, Aggregate: "VISIT", New: func() Payload { return &heightV1{} }})
}

func TestTheInitialCatalogueIsWhatTheDocumentationSays(t *testing.T) {
	want := []string{
		"BP_CORRECTED", "BP_RECORDED", "CONSENT_GRANTED",
		"CONSENT_REVOKED", "ENCOUNTER_FINISHED", "ENCOUNTER_STARTED",
		"HEIGHT_CORRECTED", "HEIGHT_RECORDED", "HIP_RECORDED",
		"OBSERVATION_RECORDED",
		"PATIENT_DEMOGRAPHICS_CORRECTED", "PATIENT_MERGED", "PATIENT_PHOTO_CAPTURED",
		"PATIENT_REGISTERED", "PULSE_RECORDED", "QUEUE_CALLED",
		"QUEUE_ENTERED", "QUEUE_LEFT", "SPO2_RECORDED",
		"TEMP_RECORDED", "VISIT_ABANDONED", "VISIT_CLOSED",
		"VISIT_OPENED", "VISIT_REOPENED", "WAIST_RECORDED",
		"WEIGHT_CORRECTED", "WEIGHT_RECORDED",
	}
	got := Default.Names()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("catalogue:\n got %v\nwant %v", got, want)
	}
	for _, name := range got {
		if !eventTypePattern.MatchString(name) || !strings.Contains(name, "_") {
			t.Errorf("%s is not NOUN_VERBPAST", name)
		}
	}
}

func TestMeasurementRulesRefuseTheImplausible(t *testing.T) {
	ok := Measurement{Code: "WEIGHT", Value: 72.5, Unit: "kg"}
	if err := ok.Validate(); err != nil {
		t.Error(err)
	}
	for _, bad := range []Measurement{
		{Code: "WEIGHT", Value: 72.5, Unit: "lb"},
		{Code: "WEIGHT", Value: 0.5, Unit: "kg"},
		{Code: "SPO2", Value: 101, Unit: "%"},
		{Code: "GLUCOSE", Value: 5, Unit: "mmol/L"},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
	if _, err := Default.Decode("HEIGHT_RECORDED", 1, json.RawMessage(`{"code":"HEIGHT","value":"150","unit":"cm"}`)); !errors.Is(err, ErrInvalidPayload) {
		t.Errorf("a string where a number belongs: %v", err)
	}
}

func TestTheHashIsCanonical(t *testing.T) {
	base := Envelope{
		EventID: uuid.MustParse("0190a8f2-0000-7000-8000-0000000000e1"), AggregateType: "VISIT",
		AggregateID: uuid.MustParse("0190a8f2-0000-7000-8000-0000000000a1"), EventType: "HEIGHT_RECORDED", EventVersion: 1,
		OccurredAt: time.Date(2026, 9, 3, 4, 42, 0, 0, time.UTC),
		Actor: ActorForTest(
			uuid.MustParse("0190a8f2-0000-7000-8000-000000000001"),
			uuid.MustParse("0190a8f2-0000-7000-8000-000000000002"),
			uuid.MustParse("0190a8f2-0000-7000-8000-000000000003"), "ANTHROPOMETRY", ""),
		Source: SourceWeb, Payload: json.RawMessage(`{"code":"HEIGHT","value":150.0,"unit":"cm"}`),
		Metadata: map[string]any{"n": 3, "app_version": "1.4.2"},
	}
	at := time.Date(2026, 9, 3, 4, 42, 3, 0, time.UTC)
	reference := hashOf(Genesis, 1, 1, at, base)

	// Key order and number spelling do not change the hash: what the database gives back
	// after JSONB normalisation must hash as what the client sent.
	reordered := base
	reordered.Payload = json.RawMessage(`{"unit": "cm", "value": 150, "code": "HEIGHT"}`)
	reordered.Metadata = map[string]any{"app_version": "1.4.2", "n": float64(3)}
	if string(hashOf(Genesis, 1, 1, at, reordered)) != string(reference) {
		t.Error("the hash depends on JSON spelling")
	}

	// Anything that matters does change it.
	for name, mutate := range map[string]func(e *Envelope){
		"the value": func(e *Envelope) { e.Payload = json.RawMessage(`{"code":"HEIGHT","value":140,"unit":"cm"}`) },
		"the actor": func(e *Envelope) {
			e.Actor = ActorForTest(uuid.New(), e.Actor.deviceID, e.Actor.facilityID, e.Actor.role, "")
		},
		"the device": func(e *Envelope) {
			e.Actor = ActorForTest(e.Actor.userID, uuid.New(), e.Actor.facilityID, e.Actor.role, "")
		},
		"the role": func(e *Envelope) {
			e.Actor = ActorForTest(e.Actor.userID, e.Actor.deviceID, e.Actor.facilityID, "PHYSICIAN", "")
		},
		"the time":     func(e *Envelope) { e.OccurredAt = e.OccurredAt.Add(time.Second) },
		"the metadata": func(e *Envelope) { e.Metadata["n"] = 4 },
	} {
		e := base
		e.Metadata = map[string]any{"n": 3, "app_version": "1.4.2"}
		mutate(&e)
		if string(hashOf(Genesis, 1, 1, at, e)) == string(reference) {
			t.Errorf("changing %s did not change the hash", name)
		}
	}
	if string(hashOf(Genesis, 2, 1, at, base)) == string(reference) || string(hashOf(Genesis, 1, 2, at, base)) == string(reference) {
		t.Error("the sequence numbers are not in the hash")
	}
	if string(hashOf(Genesis, 1, 1, at.Add(time.Microsecond), base)) == string(reference) {
		t.Error("recorded_at is not in the hash")
	}

	// The reference value itself, so that a change to the canonical form is a deliberate,
	// visible act: every stored hash depends on it.
	const pinned = "d85b3c00fcfa8bf7" // first eight bytes, hex
	if got := hexPrefix(reference); got != pinned {
		t.Errorf("canonical hash prefix is now %s (pinned %s) — a change here invalidates every stored chain; update the pin only with a migration plan", got, pinned)
	}
}

func hexPrefix(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 0; i < 8; i++ {
		out[i*2] = digits[b[i]>>4]
		out[i*2+1] = digits[b[i]&0xf]
	}
	return string(out)
}

func TestCanonicalJSONSortsKeysEverywhere(t *testing.T) {
	got := string(canonicalJSON(json.RawMessage(`{"b": [ {"z":1,"a":2} ], "a": 1.50, "c": "x"}`)))
	if got != `{"a":1.5,"b":[{"a":2,"z":1}],"c":"x"}` {
		t.Errorf("canonical = %s", got)
	}
}

// --- the patient aggregate (CP28) ---

func validPatientRegistered() PatientRegistered {
	return PatientRegistered{
		FacilityID:       "0190a000-0000-7000-8000-000000000001",
		PatientID:        "0190a8f2-0000-7000-8000-00000000000b",
		ClinicalID:       "DTHC-FRD-2026-000137",
		NameEN:           "Rahima Begum",
		NameBN:           "রহিমা বেগম",
		Sex:              "female",
		BirthDate:        "1979-04-12",
		DOBPrecision:     "day",
		DOBSource:        "national_id",
		PhonePrimary:     "+8801712345678",
		EducationLevel:   "secondary",
		IncomeBand:       "10k_25k",
		HouseholdSize:    5,
		IdentifierKinds:  []string{"national_id"},
		ConsentReference: "consent_2026_0001",
	}
}

func TestAPatientRegistrationCarriesTheCompleteDemographics(t *testing.T) {
	if err := validPatientRegistered().Validate(); err != nil {
		t.Fatalf("a complete registration was refused: %v", err)
	}
}

func TestAPatientRegistrationIsRefusedForWhatMattersLater(t *testing.T) {
	// Each of these is something that would be discovered months later, in a cohort or on
	// a growth chart, rather than at the moment it went wrong.
	for name, mutate := range map[string]func(*PatientRegistered){
		"no clinical id":                  func(p *PatientRegistered) { p.ClinicalID = "" },
		"a clinical id from nowhere":      func(p *PatientRegistered) { p.ClinicalID = "12345" },
		"no name":                         func(p *PatientRegistered) { p.NameEN = "  " },
		"a sex outside the three":         func(p *PatientRegistered) { p.Sex = "F" },
		"a birth date that is not a date": func(p *PatientRegistered) { p.BirthDate = "12 April 1979" },
		"a birth date in 1093":            func(p *PatientRegistered) { p.BirthDate = "1093-04-12" },
		"no precision":                    func(p *PatientRegistered) { p.DOBPrecision = "" },
		"an invented precision":           func(p *PatientRegistered) { p.DOBPrecision = "approximately" },
		"an invented source":              func(p *PatientRegistered) { p.DOBSource = "a guess" },
		"an unnormalised phone":           func(p *PatientRegistered) { p.PhonePrimary = "01712345678" },
		"an invented income band":         func(p *PatientRegistered) { p.IncomeBand = "12000" },
		"an impossible household":         func(p *PatientRegistered) { p.HouseholdSize = 41 },
		"an unknown identifier kind":      func(p *PatientRegistered) { p.IdentifierKinds = []string{"voter_card"} },
		"no consent":                      func(p *PatientRegistered) { p.ConsentReference = "" },
		"no patient id":                   func(p *PatientRegistered) { p.PatientID = "" },
	} {
		p := validPatientRegistered()
		mutate(&p)
		if err := p.Validate(); err == nil {
			t.Errorf("a registration with %s was accepted", name)
		}
	}
}

func TestTheSocioEconomicBaselineIsOptionalAndClosed(t *testing.T) {
	// Absent is a legitimate answer — the desk skipped it to keep the queue moving — and
	// "unknown" is a different, also legitimate one.
	bare := validPatientRegistered()
	bare.EducationLevel, bare.IncomeBand, bare.HouseholdSize = "", "", 0
	if err := bare.Validate(); err != nil {
		t.Fatalf("an uncaptured baseline was refused: %v", err)
	}
	for _, band := range PatientIncomeBands {
		p := validPatientRegistered()
		p.IncomeBand = band
		if err := p.Validate(); err != nil {
			t.Errorf("%q was refused: %v", band, err)
		}
	}
}

func TestAnIdentifierNumberCannotBeWrittenIntoTheLedger(t *testing.T) {
	// The ledger is append-only: a national ID written into an event could never be
	// re-sealed under a rotated key, nor removed for a patient who withdraws consent. The
	// schema has nowhere to put one, and a client that sends one is refused rather than
	// silently ignored — a dropped payload is a payload somebody will assume was stored.
	raw := json.RawMessage(`{
		"facility_id": "0190a000-0000-7000-8000-000000000001",
		"patient_id": "0190a8f2-0000-7000-8000-00000000000b",
		"clinical_id": "DTHC-FRD-2026-000137",
		"name_en": "Rahima Begum", "sex": "female",
		"birth_date": "1979-04-12", "dob_precision": "day", "dob_source": "national_id",
		"phone_primary": "+8801712345678", "consent_reference": "consent_2026_0001",
		"national_id": "1990123456789"
	}`)
	_, err := Default.Decode("PATIENT_REGISTERED", 1, raw)
	if err == nil {
		t.Fatal("a national ID was accepted into an event payload")
	}
	if !errors.Is(err, ErrInvalidPayload) {
		t.Errorf("refused for the wrong reason: %v", err)
	}
	if !strings.Contains(err.Error(), "national_id") {
		t.Errorf("the refusal did not name the offending field: %v", err)
	}
}

func TestTheResearchIDIsNotInTheLedger(t *testing.T) {
	// Putting it here would place the re-identification link in a table the application
	// can read, which is exactly what identity_link exists to prevent (§12).
	raw, err := json.Marshal(validPatientRegistered())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "research") {
		t.Errorf("the payload carries a research identifier: %s", raw)
	}
}
