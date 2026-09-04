// Package clinical is the observation model: one uniform way to record every measured
// clinical value, with units handled correctly (CP42, blueprint §6, §11).
//
// # Why one module and not ten
//
// Ten stations record values. Ten bespoke tables and ten bespoke endpoints would make the
// timeline, the research extract and the FHIR mapping ten times harder, and would guarantee
// that the eleventh station invented an eleventh shape. So there is one table, one event
// type, one write path — and a code registry that says what each kind of value *is*: its
// category, its shape, its unit dimension, its plausibility band, and who may write it.
//
// # Where the safety lives
//
// Not here. The unit rule — a unit-bearing observation cannot be stored without a valid unit
// — is a trigger on the read model and a standing invariant, both in migration 00026. This
// package refuses a bad write early, so the operator sees a sentence rather than a 500, but
// the refusal that *matters* is the one a projection rebuild and a hand-written UPDATE at
// three in the morning also hit.
//
// That division is the point. A validation in Go protects the person typing; a constraint in
// the database protects the record.
package clinical

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// Category is §6's seven. A discriminator rather than seven tables: the difference between a
// vital and a lab result is what it means, not what shape it is.
type Category string

const (
	Anthro    Category = "ANTHRO"
	Vital     Category = "VITAL"
	Exam      Category = "EXAM"
	Lab       Category = "LAB"
	Derived   Category = "DERIVED"
	Screening Category = "SCREENING"
	PRO       Category = "PRO"
)

// Categories is the closed list, in the order §6 gives them.
var Categories = []Category{Anthro, Vital, Exam, Lab, Derived, Screening, PRO}

// ValueType is the shape of a value.
type ValueType string

const (
	Numeric    ValueType = "numeric"
	Text       ValueType = "text"
	Boolean    ValueType = "boolean"
	Coded      ValueType = "coded"
	Structured ValueType = "structured"
)

// Source is where a value came from.
//
// Not decoration. A number a patient reported at home and a number an operator measured on a
// calibrated scale are different evidence, and a physician deciding a dose deserves to know
// which. OCR in particular: CP105 will read values off photographed lab reports, and a
// transcription is not a measurement.
type Source string

const (
	Station Source = "STATION"
	OCR     Source = "OCR"
	Field   Source = "FIELD"
	Device  Source = "DEVICE"
	Patient Source = "PATIENT"
)

// Sources is the closed list.
var Sources = []Source{Station, OCR, Field, Device, Patient}

// Status is where an observation stands.
type Status string

const (
	// Active is the value.
	Active Status = "ACTIVE"
	// Corrected means it was wrong and has been replaced.
	Corrected Status = "CORRECTED"
	// Superseded means it was right and has been re-measured. A different fact from
	// Corrected, and a report that conflated them would count re-measurements as errors.
	Superseded Status = "SUPERSEDED"
)

// Unit is one unit and how it converts to its dimension's canonical unit.
type Unit struct {
	Code        string  `json:"code"`
	Dimension   string  `json:"dimension"`
	IsCanonical bool    `json:"is_canonical"`
	Factor      float64 `json:"-"`
	Offset      float64 `json:"-"`
	DisplayEN   string  `json:"display_en"`
	DisplayBN   string  `json:"display_bn"`
	Decimals    int     `json:"decimals"`
}

// Code is one entry in the registry: what a kind of value is.
type Code struct {
	Code      string    `json:"code"`
	Category  Category  `json:"category"`
	ValueType ValueType `json:"value_type"`
	// Dimension is empty for a unitless code. A numeric code with no dimension is a number
	// with no unit, which the database refuses to create.
	Dimension string `json:"dimension,omitempty"`
	LOINC     string `json:"loinc,omitempty"`
	DisplayEN string `json:"display_en"`
	DisplayBN string `json:"display_bn"`
	// MinCanonical and MaxCanonical are plausibility in the canonical unit, not clinical
	// judgement — the band outside which a number is a typing error. Critical values are
	// CP50.
	MinCanonical *float64 `json:"min_canonical,omitempty"`
	MaxCanonical *float64 `json:"max_canonical,omitempty"`
	// WritePermission is what §4.4 requires to record this code. Per code in the schema even
	// though it is per category in practice, because "the nutritionist writes diet-related
	// values and not vitals" is a rule about values.
	WritePermission string `json:"write_permission"`
	// CanonicalUnit is the unit values of this code are stored in. Empty for a unitless code.
	CanonicalUnit string `json:"canonical_unit,omitempty"`
	// Units are every unit a value of this code may be *entered* in.
	Units []Unit `json:"units,omitempty"`
}

// Unitless reports a code that takes no unit.
func (c Code) Unitless() bool { return c.Dimension == "" }

// Observation is one recorded value.
type Observation struct {
	ID          uuid.UUID  `json:"id"`
	PatientID   uuid.UUID  `json:"patient_id"`
	VisitID     *uuid.UUID `json:"visit_id,omitempty"`
	EncounterID *uuid.UUID `json:"encounter_id,omitempty"`

	Code      string    `json:"code"`
	Category  Category  `json:"category"`
	ValueType ValueType `json:"value_type"`

	// Value is canonical: what everything computes from. Unit is its unit.
	Value *float64 `json:"value,omitempty"`
	Unit  string   `json:"unit,omitempty"`

	// EnteredValue and EnteredUnit are what the operator typed, exactly. Showing a value
	// back in the unit it was entered in is a read of these two, not a round trip — 154 lb
	// comes back as 154 lb, not as 69.85 kg converted back to 153.99 lb.
	EnteredValue *float64 `json:"entered_value,omitempty"`
	EnteredUnit  string   `json:"entered_unit,omitempty"`

	ValueText string          `json:"value_text,omitempty"`
	ValueBool *bool           `json:"value_bool,omitempty"`
	ValueCode string          `json:"value_code,omitempty"`
	ValueJSON json.RawMessage `json:"value_json,omitempty"`

	EffectiveAt time.Time  `json:"effective_at"`
	RecordedAt  time.Time  `json:"recorded_at"`
	Source      Source     `json:"source"`
	Status      Status     `json:"status"`
	ReplacedBy  *uuid.UUID `json:"replaced_by,omitempty"`

	RecordedBy   uuid.UUID `json:"recorded_by"`
	RecordedRole string    `json:"recorded_role"`
	StationCode  string    `json:"station_code,omitempty"`
	Note         string    `json:"note,omitempty"`

	// For a DERIVED value: which equation produced it, which version, and what it saw. The
	// inputs are what the formula *actually saw* — a weight corrected an hour later does not
	// change what this BMI was computed from (CP43).
	Formula string             `json:"formula,omitempty"`
	Version string             `json:"formula_version,omitempty"`
	Inputs  map[string]float64 `json:"inputs,omitempty"`
}

// Recording is one value being written.
type Recording struct {
	EventID     uuid.UUID
	PatientID   uuid.UUID
	VisitID     *uuid.UUID
	EncounterID *uuid.UUID

	Code string

	Value     *float64
	Unit      string
	ValueText string
	ValueBool *bool
	ValueCode string
	ValueJSON json.RawMessage

	// EffectiveAt is when the value was true. Defaulted to now by the handler when a client
	// does not say, because most values are recorded as they are taken — but it is a
	// separate field because a value read off a lab report from Tuesday is not a value from
	// today, and a timeline that pretended otherwise would order it wrongly.
	EffectiveAt time.Time
	// Source is what kind of evidence the value is: measured at a station, transcribed from
	// a photographed report, reported by the patient. LedgerSource is which *surface* wrote
	// the event, and comes from the request.
	//
	// Two vocabularies on purpose, because the surface cannot know the evidence: a value a
	// patient reported at home and a value an operator measured both arrive from the same
	// tablet, and a physician deciding a dose needs to know which of those they are reading.
	Source       Source
	LedgerSource eventstore.Source
	Note         string

	// Replaces and ReplacedStatus correct or supersede an earlier observation.
	Replaces       *uuid.UUID
	ReplacedStatus Status

	// Formula, Version and Inputs are set only for a DERIVED value, and only by the server
	// (CP43). A derived value with no formula is a number nobody can reproduce; one with no
	// version is a number nobody can interpret two years from now, because CKD-EPI has
	// already changed once. The database refuses a DERIVED row without all three.
	Formula string
	Version string
	Inputs  map[string]float64
}

// The refusals this module raises. Each maps to a sentence an operator can act on; the
// mapping is in http.go.
var (
	// ErrUnknownCode is a code that is not in the registry.
	ErrUnknownCode = errors.New("clinical: that is not an observation code")
	// ErrRetiredCode is a code that was withdrawn. Values recorded under it before it was
	// withdrawn are still facts; new ones are not accepted.
	ErrRetiredCode = errors.New("clinical: that observation code has been withdrawn")
	// ErrUnitRequired is the one this checkpoint exists for: a number with no unit.
	ErrUnitRequired = errors.New("clinical: this value needs the unit it was measured in")
	// ErrUnitNotAllowed is a unit on a value that has none — a boolean finding with a "kg".
	ErrUnitNotAllowed = errors.New("clinical: this value takes no unit")
	// ErrWrongDimension is a weight recorded in centimetres. Not a conversion problem: a
	// different measurement.
	ErrWrongDimension = errors.New("clinical: that unit does not measure this")
	// ErrImplausible is a value outside the code's plausibility band — a typing error, not
	// a clinical finding.
	ErrImplausible = errors.New("clinical: that value is outside the plausible range")
	// ErrWrongShape is a text value for a numeric code, or two values at once.
	ErrWrongShape = errors.New("clinical: that is not the shape this observation takes")
	// ErrNotFound is an observation that is not this facility's, or does not exist. The
	// same error for both: a 403 must not reveal whether a resource exists.
	ErrNotFound = errors.New("clinical: no such observation")
	// ErrAlreadyReplaced is correcting an observation that has already been corrected.
	ErrAlreadyReplaced = errors.New("clinical: that value has already been replaced")
	// ErrNotPermitted is a role writing a category it does not hold.
	ErrNotPermitted = errors.New("clinical: that role may not record this kind of value")
)

// validate checks everything decidable without the registry.
//
// The registry checks — is this code real, does it take a unit, is the number plausible —
// need the database and happen in the service. What is here is the shape: exactly one value,
// a source that exists, an effective time.
func (r Recording) validate() error {
	if strings.TrimSpace(r.Code) == "" {
		return ErrUnknownCode
	}
	set := 0
	if r.Value != nil {
		set++
	}
	if strings.TrimSpace(r.ValueText) != "" {
		set++
	}
	if r.ValueBool != nil {
		set++
	}
	if strings.TrimSpace(r.ValueCode) != "" {
		set++
	}
	if len(r.ValueJSON) > 0 {
		set++
	}
	if set != 1 {
		return ErrWrongShape
	}
	found := false
	for _, source := range Sources {
		if r.Source == source {
			found = true
		}
	}
	if !found {
		return ErrWrongShape
	}
	if r.EffectiveAt.IsZero() {
		return ErrWrongShape
	}
	if r.Replaces != nil && r.ReplacedStatus != Corrected && r.ReplacedStatus != Superseded {
		return ErrWrongShape
	}
	return nil
}

// shapeOf reports the value type a Recording actually carries, so the service can compare it
// with the one the registry declares.
func (r Recording) shapeOf() ValueType {
	switch {
	case r.Value != nil:
		return Numeric
	case r.ValueBool != nil:
		return Boolean
	case strings.TrimSpace(r.ValueCode) != "":
		return Coded
	case len(r.ValueJSON) > 0:
		return Structured
	default:
		return Text
	}
}
