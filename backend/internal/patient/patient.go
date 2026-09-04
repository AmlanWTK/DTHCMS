// Package patient is the person the clinic knows (CP28, blueprint §3 Step 1).
//
// Everything downstream hangs from a patient, so this package is deliberately narrow: the
// domain types, the two identifier generators, and the validation that has to hold before
// a row exists. Registration itself — the event, the projection, the HTTP surface — is
// CP29 and lives in registration.go.
//
// Three things here are worth reading as decisions rather than as code (ADR-0020):
//
//	the date of birth   mandatory, validated, and carrying both how precise it is and what
//	                    established it, because pediatric percentiles depend on exact age
//	                    [R-06] and a grandmother's recollection is not a birth certificate
//	the identifiers     a peppered digest for matching, a sealed value for retrieval, a
//	                    mask for display — never a readable number (D-47)
//	the two ids         `clinical_id` is spoken at a desk; `research_id` is opaque and
//	                    carries no ordering, so a research row cannot be traced to a
//	                    registration position (§12)
package patient

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// Sex is what the clinic records. Three values, because the clinical questions that use it
// — reference ranges, pregnancy, percentile curves — have three answers and not two.
type Sex string

const (
	SexFemale Sex = "female"
	SexMale   Sex = "male"
	SexOther  Sex = "other"
)

// DOBPrecision says how much of the birth date is real.
//
// A patient who knows only their birth year is common in Bangladesh, and the honest record
// is 1 January of that year with the precision beside it — not a made-up day that a
// percentile calculation will treat as exact.
type DOBPrecision string

const (
	PrecisionDay   DOBPrecision = "day"
	PrecisionMonth DOBPrecision = "month"
	PrecisionYear  DOBPrecision = "year"
)

// DOBSource is what established the date. A researcher comparing pediatric growth has to
// be able to tell a birth certificate from an estimate.
type DOBSource string

const (
	SourceBirthCertificate DOBSource = "birth_certificate"
	SourceNationalID       DOBSource = "national_id"
	SourcePassport         DOBSource = "passport"
	SourceImmunisation     DOBSource = "immunisation_card"
	SourcePatientStated    DOBSource = "patient_stated"
	SourceGuardianStated   DOBSource = "guardian_stated"
	SourceEstimated        DOBSource = "estimated"
)

// Status is where a patient record stands. A duplicate is merged, never deleted: a record
// that is removed takes its history with it.
type Status string

const (
	StatusActive   Status = "active"
	StatusInactive Status = "inactive"
	StatusDeceased Status = "deceased"
	StatusMerged   Status = "merged"
)

// IdentifierKind is which official number.
type IdentifierKind string

const (
	NationalID       IdentifierKind = "national_id"
	BirthCertificate IdentifierKind = "birth_certificate"
	Passport         IdentifierKind = "passport"
	DrivingLicence   IdentifierKind = "driving_licence"
	OtherIdentifier  IdentifierKind = "other"
)

// Socioeconomic is §12's cohorting baseline, confirmed with Dr Nahid at CP28.
//
// Six fields, and the value lists are closed. A research variable whose categories can be
// edited from the application is a variable whose cohorts stop being comparable between
// one paper and the next; adding a category is a migration, which is the review it
// deserves.
//
// Every field is optional, and empty means two different things on purpose. "" is *not
// captured* — the desk skipped it to keep the queue moving, which the confirmed required
// set allows. "unknown" is *asked, and the patient does not know*, which is itself a
// finding: a household that cannot state its monthly income is a different cohort from one
// that was never asked.
type Socioeconomic struct {
	Education     string `json:"education_level,omitempty"`
	Occupation    string `json:"occupation_category,omitempty"`
	IncomeBand    string `json:"income_band,omitempty"`
	HouseholdSize int    `json:"household_size,omitempty"`
	Residence     string `json:"residence_type,omitempty"`
	MedicinePayer string `json:"medicine_payer,omitempty"`
}

// The permitted values, in the order a form should offer them.
//
// Taken from the event schema rather than restated. The ledger is the system of record and
// an event is immutable, so a category that has once been written into an event exists for
// as long as the deployment does — which makes the event schema the one place the list can
// live without the domain, the OpenAPI enum and the database CHECK drifting into three
// vocabularies (ADR-0020).
var (
	EducationLevels      = eventstore.PatientEducationLevels
	OccupationCategories = eventstore.PatientOccupationCategories
	// Monthly household income in BDT. Bands rather than a figure: an exact number is
	// rarely known, is answered less honestly, and is not what a cohort comparison uses.
	IncomeBands    = eventstore.PatientIncomeBands
	ResidenceTypes = eventstore.PatientResidenceTypes
	// Who pays for medicines. §12 names affordability of semaglutide and tirzepatide as an
	// output, and "can the patient afford it" and "does an employer pay" are different
	// questions with different answers.
	MedicinePayers = eventstore.PatientMedicinePayers
)

// Address is Bangladesh's administrative division, as a registration desk knows it.
type Address struct {
	Division    string `json:"division,omitempty"`
	District    string `json:"district,omitempty"`
	Upazila     string `json:"upazila,omitempty"`
	AddressLine string `json:"address_line,omitempty"`
	Postcode    string `json:"postcode,omitempty"`
}

// EmergencyContact is who to telephone.
type EmergencyContact struct {
	Name     string `json:"name,omitempty"`
	Relation string `json:"relation,omitempty"`
	Phone    string `json:"phone,omitempty"`
}

// BirthDate is the date and everything that qualifies it.
type BirthDate struct {
	Date      time.Time    `json:"date"`
	Precision DOBPrecision `json:"precision"`
	Source    DOBSource    `json:"source"`
	// VerifiedBy and VerifiedAt are set when a document was actually seen.
	VerifiedBy *uuid.UUID `json:"-"`
	VerifiedAt *time.Time `json:"-"`
}

// Age is the age in whole years on a given day, by the clinic's calendar.
//
// Below day precision this is an approximation and the caller is expected to know it —
// which is what Precision is for. A percentile computed from a year-precision birth date
// can be a year out either way, and a screen that shows it must say so.
//
// Both dates are read in Dhaka, and the comparison is month-and-day rather than day-of-year:
// a leap year shifts every day-of-year after February by one, so comparing ordinals makes
// everybody born in a leap year a year younger for one day. That is a wrong age on a
// percentile chart, which is a clinical number.
func (b BirthDate) Age(on time.Time) int {
	born := b.Date.In(Dhaka)
	day := on.In(Dhaka)
	years := day.Year() - born.Year()
	if day.Month() < born.Month() || (day.Month() == born.Month() && day.Day() < born.Day()) {
		years--
	}
	if years < 0 {
		return 0
	}
	return years
}

// Patient is the record.
type Patient struct {
	ID         uuid.UUID `json:"id"`
	FacilityID uuid.UUID `json:"-"`
	ClinicalID string    `json:"clinical_id"`

	NameEN string    `json:"name_en"`
	NameBN string    `json:"name_bn,omitempty"`
	Sex    Sex       `json:"sex"`
	Birth  BirthDate `json:"birth"`

	PhonePrimary   string `json:"phone_primary"`
	PhoneSecondary string `json:"phone_secondary,omitempty"`

	Address   Address          `json:"address"`
	Emergency EmergencyContact `json:"emergency_contact"`
	Socio     Socioeconomic    `json:"socioeconomic"`

	PhotoObjectKey string `json:"-"`

	Status       Status     `json:"status"`
	StatusReason string     `json:"status_reason,omitempty"`
	MergedIntoID *uuid.UUID `json:"merged_into_id,omitempty"`

	RegisteredBy *uuid.UUID `json:"-"`
	RegisteredAt time.Time  `json:"registered_at"`
	CreatedAt    time.Time  `json:"-"`
	UpdatedAt    time.Time  `json:"-"`
}

// Identifier is an official number as it is stored: never as a number.
type Identifier struct {
	ID         uuid.UUID
	FacilityID uuid.UUID
	PatientID  uuid.UUID
	Kind       IdentifierKind
	// Digest is the peppered HMAC used for matching. A plain hash of a ten-digit NID is
	// reversible by anyone with a laptop and a weekend; the pepper is what makes this
	// useless outside this deployment.
	Digest []byte
	// Sealed is the secretbox ciphertext, openable by the service and not by a dump.
	Sealed []byte
	KeyID  string
	// Masked is what a screen shows without a step-up.
	Masked        string
	CaptureMethod string
	VerifiedAt    *time.Time
	VerifiedBy    *uuid.UUID
}

// Errors the domain raises. Each is a field-specific refusal, because "invalid patient" is
// not something a registration officer can act on.
type FieldError struct {
	Field   string
	Code    string
	Message string
	// MessageBN is the same sentence in Bangla. Half the clinic's staff read it, and a
	// validation message that appears only in English is a message they will guess at.
	MessageBN string
}

func (e FieldError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Message) }

// Errors is what Validate returns: every problem, not the first, so a desk fixes a form
// once rather than four times.
type Errors []FieldError

func (e Errors) Error() string {
	parts := make([]string, 0, len(e))
	for _, item := range e {
		parts = append(parts, item.Error())
	}
	return strings.Join(parts, "; ")
}

// Fields returns the field names that failed, for a log line free of patient data.
func (e Errors) Fields() []string {
	out := make([]string, 0, len(e))
	for _, item := range e {
		out = append(out, item.Field)
	}
	return out
}

func (e Errors) OrNil() error {
	if len(e) == 0 {
		return nil
	}
	return e
}
