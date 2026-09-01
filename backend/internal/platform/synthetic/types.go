// Package synthetic generates believable fictional patients from a clinician-authored
// case-mix profile.
//
// Everything here is invented. Nothing in this package reads, derives from, or resembles a
// real patient record, and the profile it samples from is a clinician's aggregate
// impression rather than an extract — see docs/synthetic-data-profile.md.
//
// # Why the shape matters
//
// A generator that samples each field independently produces a population that is wrong in
// a way nobody notices: a 9-year-old with type 2 diabetes of 20 years' duration, a patient
// on four drugs with an HbA1c of 5.4, a pregnant man. The screens look fine, the load tests
// look fine, and the first incoherent record anyone actually reads is in a live clinic.
//
// So sampling is conditional throughout. Age constrains diagnosis, diagnosis constrains
// medicines, renal function constrains dose, and every laboratory series is a trajectory
// rather than a set of unrelated draws.
//
// # What is deliberately imperfect
//
// Real records are not tidy. Tests are missed, visits are skipped, patients stop coming.
// The profile asks for that explicitly, and §Missingness implements it — because a system
// tested only against complete records meets its first incomplete one in a clinic.
package synthetic

import "time"

// Sex as recorded. Two values, because that is what the clinic's forms carry; if that
// changes, this is the one place it changes.
type Sex string

const (
	Female Sex = "female"
	Male   Sex = "male"
)

// PresentingProblem is what brought the patient in — one per patient, matching how the
// profile's §2 was answered.
type PresentingProblem string

const (
	ProblemDiabetes         PresentingProblem = "diabetes"
	ProblemThyroid          PresentingProblem = "thyroid"
	ProblemObesity          PresentingProblem = "obesity_metabolic"
	ProblemPCOS             PresentingProblem = "pcos"
	ProblemGrowth           PresentingProblem = "growth_puberty"
	ProblemBone             PresentingProblem = "bone_calcium_vitamin_d"
	ProblemAdrenal          PresentingProblem = "adrenal"
	ProblemPituitary        PresentingProblem = "pituitary"
	ProblemMaleReproductive PresentingProblem = "male_sexual_reproductive"
)

// DiabetesType, where diabetes is present at all.
type DiabetesType string

const (
	Type2       DiabetesType = "type_2"
	Type1       DiabetesType = "type_1"
	Gestational DiabetesType = "gestational"
	SecondaryDM DiabetesType = "other_secondary"
)

// ThyroidCategory, where thyroid disease is present.
type ThyroidCategory string

const (
	OvertHypothyroid       ThyroidCategory = "overt_primary_hypothyroid"
	SubclinicalHypothyroid ThyroidCategory = "subclinical_hypothyroid"
	Hyperthyroid           ThyroidCategory = "hyperthyroid_graves"
	EuthyroidGoitre        ThyroidCategory = "euthyroid_goitre"
	NoduleSurveillance     ThyroidCategory = "nodule_surveillance"
	PostAblative           ThyroidCategory = "post_surgical_or_rai"
	CancerFollowUp         ThyroidCategory = "cancer_follow_up"
)

// Patient is one invented person.
type Patient struct {
	ID   string `json:"id"`
	Name Name   `json:"name"`
	// Tradition drives the name pool only. It is generated because a Bangladeshi clinic's
	// name list is not uniform, and a register full of one community's names reads wrong.
	Tradition string `json:"tradition"`

	Sex            Sex       `json:"sex"`
	DateOfBirth    time.Time `json:"date_of_birth"`
	AgeYears       int       `json:"age_years"`
	Urban          bool      `json:"urban"`
	RecordInBangla bool      `json:"record_in_bangla"`

	Presenting PresentingProblem `json:"presenting_problem"`
	// ProblemKey is the profile's own JSON key for Presenting. Carried so the generator can
	// look the patient up in caseMix without reversing the display name, and so a distribution
	// test can compare like with like against the profile.
	ProblemKey string `json:"-"`

	Diabetes *DiabetesProfile `json:"diabetes,omitempty"`
	Thyroid  *ThyroidProfile  `json:"thyroid,omitempty"`

	// HeightM is a property of the person, sampled once. Recomputing it per visit produced a
	// patient who lost fifteen kilograms between two appointments — invisible in every
	// distribution check, obvious the moment a record was read whole.
	HeightM       float64      `json:"height_m,omitempty"`
	BMI           float64      `json:"bmi,omitempty"`
	Comorbidities []string     `json:"comorbidities,omitempty"`
	Medications   []Medication `json:"medications,omitempty"`

	Visits []Visit `json:"visits"`

	// Pregnant is generated only where it is possible, and it changes prescribing.
	Pregnant bool `json:"pregnant,omitempty"`

	// ForcedCase names the scenario this patient was generated to exercise, when one was
	// asked for. Empty for an ordinary sampled patient.
	ForcedCase string `json:"forced_case,omitempty"`
}

// DiabetesProfile is the diabetes half of a patient, where present.
type DiabetesProfile struct {
	Type           DiabetesType `json:"type"`
	AgeAtDiagnosis int          `json:"age_at_diagnosis"`
	DurationYears  float64      `json:"duration_years"`
	DiagnosedHere  bool         `json:"diagnosed_here"`
	BaselineHbA1c  float64      `json:"baseline_hba1c"`
	OnInsulin      bool         `json:"on_insulin"`
	EGFR           float64      `json:"egfr_ml_min_1_73m2"`
}

// ThyroidProfile is the thyroid half, where present.
type ThyroidProfile struct {
	Category ThyroidCategory `json:"category"`
	// TSH is nil where the value is reported as undetectable rather than measured — the
	// profile calls for that explicitly, and a client that cannot render "undetectable"
	// will silently print 0.00, which reads as a normal-ish number.
	TSH              *float64 `json:"tsh_miu_l"`
	TSHUndetectable  bool     `json:"tsh_undetectable,omitempty"`
	LevothyroxineMcg int      `json:"levothyroxine_mcg_daily,omitempty"`
	CautiousStart    bool     `json:"cautious_start,omitempty"`
}

// Medication as it would actually be written.
type Medication struct {
	Drug  string `json:"drug"`
	Dose  string `json:"dose"`
	Class string `json:"class"`
}

// Visit is one encounter. A patient's visits are a trajectory, not a set of samples.
type Visit struct {
	Date     time.Time `json:"date"`
	Number   int       `json:"number"`
	Attended bool      `json:"attended"`
	// HbA1c is nil when the test was not done — missed, delayed, or unaffordable. That
	// happens often enough in a real record that a screen must handle it.
	HbA1c          *float64 `json:"hba1c,omitempty"`
	FastingGlucose *float64 `json:"fasting_glucose_mmol_l,omitempty"`
	Weight         *float64 `json:"weight_kg,omitempty"`
	// HeightCm is recorded for children at every visit. Growth velocity is the measurement a
	// paediatric endocrine clinic exists to read, and a visit series that does not move is a
	// series nobody can review.
	HeightCm *float64 `json:"height_cm,omitempty"`
	Note     string   `json:"note,omitempty"`
}
