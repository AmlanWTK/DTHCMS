package eventstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
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

// PatientRegistered is the first event of every patient aggregate. Demographics proper
// are CP28's; this is the minimum that makes a patient exist.
type PatientRegistered struct {
	ClinicalID string `json:"clinical_id"`
	NameEN     string `json:"name_en"`
	NameBN     string `json:"name_bn"`
	Sex        string `json:"sex"`
	BirthDate  string `json:"birth_date"`
}

func (p PatientRegistered) Validate() error {
	if !strings.HasPrefix(p.ClinicalID, "DTHC-") {
		return errors.New("clinical_id must be a DTHC- identifier")
	}
	if strings.TrimSpace(p.NameEN) == "" && strings.TrimSpace(p.NameBN) == "" {
		return errors.New("a name in at least one language is required")
	}
	if p.Sex != "F" && p.Sex != "M" && p.Sex != "X" {
		return errors.New("sex must be F, M or X")
	}
	if len(p.BirthDate) != 10 {
		return errors.New("birth_date must be YYYY-MM-DD")
	}
	return nil
}

// VisitOpened opens a visit aggregate for a patient.
type VisitOpened struct {
	PatientID string `json:"patient_id"`
	Reason    string `json:"reason,omitempty"`
}

func (v VisitOpened) Validate() error {
	if len(v.PatientID) != 36 {
		return errors.New("patient_id is required")
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
	Default.Register(Type{Name: "VISIT_OPENED", Version: 1, Aggregate: "VISIT", New: func() Payload { return &VisitOpened{} }})
}
