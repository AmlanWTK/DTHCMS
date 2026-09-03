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
		"BP_CORRECTED", "BP_RECORDED", "HEIGHT_CORRECTED", "HEIGHT_RECORDED", "HIP_RECORDED", "PATIENT_REGISTERED",
		"PULSE_RECORDED", "SPO2_RECORDED", "TEMP_RECORDED", "VISIT_OPENED", "WAIST_RECORDED", "WEIGHT_CORRECTED", "WEIGHT_RECORDED",
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
