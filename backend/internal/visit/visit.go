// Package visit is the patient's journey through the clinic (CP38, §3, §11.1, §14.2).
//
// Two things, and the second is the one that pays for itself. A **visit** is one journey: it
// opens when the patient arrives, passes through stations, and closes when the physician is
// done, carrying §11.1's memory — the complaint, the diagnoses, the plan and the next review
// interval. An **encounter** is one station touch, with a start, an end and a person.
//
// Encounters cost almost nothing to record and make §14.2's bottleneck analysis a query
// rather than a project: "the counselling station is where mornings go" becomes a fact
// somebody can act on rather than an impression somebody argues about.
//
// The state machine lives in the database as well as here (ADR-0023). Not because the Go is
// untrustworthy, but because there will be a projector, a repair script and a future module
// writing these rows, and a rule enforced in one handler is a rule the other three do not
// have.
package visit

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// Dhaka is the clinic's calendar. A visit opened at 23:50 and closed at 00:10 belongs to one
// clinic day, and the queue board asks for it by that day all night.
var Dhaka = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Dhaka")
	if err != nil {
		// +06:00 all year: Bangladesh has no daylight saving, so a fixed zone is exact
		// rather than approximate when the tzdata is missing from a scratch image.
		return time.FixedZone("Asia/Dhaka", 6*60*60)
	}
	return loc
}()

// Status is where a visit stands.
type Status string

const (
	// Open — the patient is in the building.
	Open Status = "open"
	// Closed — the physician finished and §11.1's summary is recorded.
	Closed Status = "closed"
	// Abandoned — the patient left before being seen. Deliberately not Closed: §14.2's
	// throughput must not count a journey nobody completed as a completed journey.
	Abandoned Status = "abandoned"
)

// EncounterStatus is where one station touch stands.
type EncounterStatus string

const (
	InProgress EncounterStatus = "in_progress"
	Finished   EncounterStatus = "finished"
	// Bounced — sent back, typically by QA. Its own state because §14.2 counts rework, and
	// a bounce recorded as "finished" makes rework invisible.
	Bounced EncounterStatus = "bounced"
)

// Type is what kind of visit this is.
type Type string

const (
	New              Type = "new"
	FollowUp         Type = "follow_up"
	OutreachReferral Type = "outreach_referral"
)

// legalTransitions is the state machine, stated once.
//
// Written as data rather than as a switch so the test can enumerate every pair — including
// the ones that must fail — instead of testing the four somebody remembered.
var legalTransitions = map[Status][]Status{
	Open:      {Closed, Abandoned},
	Closed:    {Open}, // a reopen, which is recorded
	Abandoned: {Open}, // the patient came back the same day
}

// CanTransition says whether a visit may move from one state to another.
func CanTransition(from, to Status) bool {
	for _, allowed := range legalTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// Visit is one journey through the clinic.
type Visit struct {
	ID         uuid.UUID `json:"id"`
	FacilityID uuid.UUID `json:"-"`
	PatientID  uuid.UUID `json:"patient_id"`

	VisitCode string `json:"visit_code"`
	VisitType Type   `json:"visit_type"`

	ChiefComplaint string `json:"chief_complaint"`
	Status         Status `json:"status"`
	StatusReason   string `json:"status_reason,omitempty"`

	ClinicDay time.Time `json:"clinic_day"`

	OpenedAt time.Time  `json:"opened_at"`
	OpenedBy uuid.UUID  `json:"-"`
	ClosedAt *time.Time `json:"closed_at,omitempty"`
	ClosedBy *uuid.UUID `json:"-"`

	// §11.1's four, recorded at close.
	Diagnoses      string     `json:"diagnoses,omitempty"`
	Plan           string     `json:"plan,omitempty"`
	NextReviewDays *int       `json:"next_review_days,omitempty"`
	NextReviewOn   *time.Time `json:"next_review_on,omitempty"`

	ReopenedCount int `json:"reopened_count"`
}

// Encounter is one station touch.
type Encounter struct {
	ID          uuid.UUID `json:"id"`
	VisitID     uuid.UUID `json:"visit_id"`
	PatientID   uuid.UUID `json:"-"`
	StationCode string    `json:"station_code"`

	Status EncounterStatus `json:"status"`

	StartedAt   time.Time  `json:"started_at"`
	StartedBy   uuid.UUID  `json:"-"`
	StartedRole string     `json:"started_role"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`

	Outcome string `json:"outcome,omitempty"`
	Notes   string `json:"notes,omitempty"`

	// Seconds is how long the patient was at this station, computed on read so a screen does
	// not have to. Nil while the encounter is open.
	Seconds *int `json:"seconds,omitempty"`
}

// Summary is what a visit looks like with its journey attached.
type Summary struct {
	Visit      Visit       `json:"visit"`
	Encounters []Encounter `json:"encounters"`
	// TotalSeconds is how long the patient has been in the building. What §14.2 calls
	// throughput starts here.
	TotalSeconds int `json:"total_seconds"`
	// WaitingSeconds is time not at any station — the gaps between encounters, which is
	// what a patient actually experiences as waiting.
	WaitingSeconds int `json:"waiting_seconds"`
}

// The refusals this package raises.
var (
	// ErrAlreadyOpen is a second visit for a patient who already has one.
	ErrAlreadyOpen = errors.New("visit: this patient already has an open visit")
	// ErrNotFound is a visit or encounter that does not exist, or belongs to another facility.
	ErrNotFound = errors.New("visit: not found")
	// ErrIllegalTransition is a state change the machine does not allow.
	ErrIllegalTransition = errors.New("visit: that is not a legal change of state")
	// ErrNotOpen is an action that needs an open visit.
	ErrNotOpen = errors.New("visit: the visit is not open")
	// ErrAlreadyAtStation is a second encounter at a station the patient is already at.
	ErrAlreadyAtStation = errors.New("visit: the patient is already at that station")
	// ErrEncounterFinished is finishing an encounter that has already ended.
	ErrEncounterFinished = errors.New("visit: that station touch has already ended")
	// ErrSummaryIncomplete is a close missing what §11.1 asks for.
	ErrSummaryIncomplete = errors.New("visit: a closing visit records the complaint, the diagnoses, the plan and the next review")
	// ErrReasonRequired is a reopening or an abandonment with no usable reason.
	ErrReasonRequired = errors.New("visit: that needs a reason a reader can act on")
	// ErrUnknownStation is a station this facility does not have.
	ErrUnknownStation = errors.New("visit: that is not a station at this clinic")
)

// Opening is what a registration desk sends to open a visit.
type Opening struct {
	EventID        uuid.UUID
	PatientID      uuid.UUID
	VisitType      Type
	ChiefComplaint string
	Source         eventstore.Source
}

func (o Opening) validate() error {
	switch o.VisitType {
	case New, FollowUp, OutreachReferral:
	default:
		return fmt.Errorf("visit: %q is not a kind of visit", o.VisitType)
	}
	if o.PatientID == uuid.Nil {
		return errors.New("visit: a visit needs a patient")
	}
	return nil
}

// Closing is §11.1's summary.
type Closing struct {
	EventID uuid.UUID
	// ChiefComplaint may be corrected at close: the desk hears "sugar problem" and the
	// physician writes what it turned out to be. Empty keeps what the visit already has.
	ChiefComplaint string
	Diagnoses      string
	Plan           string
	NextReviewDays int
	Source         eventstore.Source
}

// ReviewInterval is the range a next-review interval may take: a day to ten years.
const (
	MinReviewDays = 1
	MaxReviewDays = 3650
)

func (c Closing) validate() error {
	if strings.TrimSpace(c.Diagnoses) == "" || strings.TrimSpace(c.Plan) == "" {
		return ErrSummaryIncomplete
	}
	if c.NextReviewDays < MinReviewDays || c.NextReviewDays > MaxReviewDays {
		return fmt.Errorf("%w: the next review interval must be %d..%d days",
			ErrSummaryIncomplete, MinReviewDays, MaxReviewDays)
	}
	return nil
}

// Arrival is a patient reaching a station.
type Arrival struct {
	EventID     uuid.UUID
	StationCode string
	Source      eventstore.Source
}

// Departure is a station finishing with a patient.
type Departure struct {
	EventID uuid.UUID
	Outcome string
	Note    string
	Source  eventstore.Source
}

func (d Departure) validate() error {
	for _, allowed := range eventstore.EncounterOutcomes {
		if allowed == d.Outcome {
			return nil
		}
	}
	return fmt.Errorf("visit: %q is not how a station touch ends", d.Outcome)
}

// ClinicDayOf is the clinic day a moment belongs to.
func ClinicDayOf(at time.Time) time.Time {
	local := at.In(Dhaka)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, Dhaka)
}
