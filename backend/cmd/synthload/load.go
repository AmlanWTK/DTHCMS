package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/allergy"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/synthetic"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// The floor, in the order a patient walks it.
//
// Eight of the clinic's twelve stations: the sequence `core.station_sequence` holds for a new
// patient, less the four this command has nothing honest to put at. Records import would mean
// inventing a scanned document; QA would mean a review of work nobody did; prescription
// education and long-term follow-up would mean a prescription, and a register full of invented
// prescriptions is a register somebody will eventually quote from.
//
// Restated here rather than read from the database because what this list adds to the
// database's own sequence is the operator standing at each station, which is this command's
// business and not the clinic's configuration. A station the clinic reorders moves in
// `station_sequence`; who works there does not.
const (
	stationRegistration  = "STN_REGISTRATION"
	stationAnthropometry = "STN_ANTHROPOMETRY"
	stationCounseling    = "STN_COUNSELING"
	stationHistory       = "STN_HISTORY"
	stationExamination   = "STN_EXAMINATION"
	stationNutrition     = "STN_NUTRITION"
	stationExercise      = "STN_EXERCISE"
	stationConsultation  = "STN_CONSULTATION"
)

// walk is the sequence today's patients are part-way through. Its order is the facility's own
// station_sequence for a follow-up, read from the database at CP39 and restated here only so
// that this command knows which operator stands at each one.
var walk = []struct{ station, role string }{
	{stationRegistration, "REGISTRATION"},
	{stationAnthropometry, "CLINICAL_ASSISTANT"},
	{stationCounseling, "CLINICAL_ASSISTANT"},
	{stationHistory, "CLINICAL_ASSISTANT"},
	{stationExamination, "CLINICAL_ASSISTANT"},
	{stationNutrition, "NUTRITIONIST"},
	{stationExercise, "EXERCISE"},
	{stationConsultation, "PHYSICIAN"},
}

// loaded is one invented person and the record this command made of them.
type loaded struct {
	source synthetic.Patient
	person patient.Patient
	// shift moves the whole of one person's visit series back by up to a year. The generator
	// starts every patient's series exactly one year before its as-of date, which is right for
	// a cohort and wrong for a register: sixty people who all first attended in the same
	// fortnight is not a clinic that has been open for two years, and it makes every
	// "registered in the last N months" screen read as a single spike.
	shift time.Duration
	// lastSeen is when this patient was last in the building, so today's follow-up can be
	// dated after it rather than before.
	lastSeen time.Time
	// newToday marks somebody registering for the first time this morning. A register in
	// which nobody was registered today is a register where "patients registered today" —
	// a screen every station has open — is permanently empty, and a developer cannot tell
	// that from a broken query.
	newToday bool
	// allergyAsserted records that CP54's question has been answered for this person. Carried
	// rather than re-queried because it decides whether today's history station has anything
	// to do, and a patient whose whole visit series fell after the cut-off would otherwise
	// reach the examination queue with no answer on file and be refused by the trigger.
	allergyAsserted bool
}

func (l *loader) load(ctx context.Context, cohort []synthetic.Patient, todayCount int) error {
	// A few of the cohort are new patients rather than returning ones: registered this
	// morning, with no history behind them at all. They are what makes the difference between
	// a register that was filled in once and a clinic that is still taking people in.
	newcomers := min(newToday, todayCount, len(cohort))

	people := make([]loaded, 0, len(cohort))
	for i := range cohort {
		who, ok := l.register(ctx, cohort[i], i >= len(cohort)-newcomers)
		if !ok {
			continue
		}
		if !who.newToday {
			l.history(ctx, &who)
		}
		people = append(people, who)
	}
	if len(people) == 0 {
		return errors.New("nothing was loaded: every registration was refused")
	}
	if todayCount > len(people) {
		todayCount = len(people)
	}
	return l.clinic(ctx, people, todayCount)
}

// register is the desk — a year or two ago for most of the cohort, and this morning for the
// handful who are new.
func (l *loader) register(ctx context.Context, q synthetic.Patient, today bool) (loaded, bool) {
	who := loaded{source: q, shift: -time.Duration(l.rng.Intn(365)) * 24 * time.Hour, newToday: today}

	day := l.firstAttended(q).Add(who.shift)
	switch {
	case today:
		// At the head of today's window, so that the visit this person is about to be taken
		// through opens after they exist rather than at the same instant.
		day = todayWindow(time.Now().UTC()).from
		l.moment(day)
	case day.IsZero() || !day.Before(time.Now()):
		// A cohort generated with an as-of in the future, or a patient with nothing but
		// missed appointments. Neither is a failure worth stopping for, and registering
		// somebody on a date that has not happened would fail validation anyway.
		l.tally.skipped++
		return loaded{}, false
	default:
		l.at(day, 8, 20)
	}

	registration := l.registration(q)
	done, err := l.patients.Register(l.as(ctx, "REGISTRATION", stationRegistration),
		registration, uuid.New(), eventstore.SourceWeb)
	if err != nil {
		// Reported and counted rather than fatal. The one refusal that is expected here is
		// the duplicate matcher blocking a person the generator produced twice, and losing
		// fifty-nine patients because of the sixtieth is the wrong trade for a seed command.
		l.log.Warn("a synthetic patient could not be registered",
			"synthetic_id", q.ID, "error", err.Error())
		l.tally.skipped++
		return loaded{}, false
	}
	who.person = done.Patient
	if !today {
		who.lastSeen = day
	}
	l.tally.registered++
	return who, true
}

// history walks every visit the generator gave this patient that is already in the past.
//
// Closed, with the station journey and the measurements attached, because a closed visit is
// what a timeline and a trend view are made of. A patient with one blood pressure teaches
// nobody anything; the same patient with eight over two years is the thing this system is for.
func (l *loader) history(ctx context.Context, who *loaded) {
	first := true
	for i := range who.source.Visits {
		v := who.source.Visits[i]
		if !v.Attended {
			// A missed appointment leaves no trace in this system, which has no appointment
			// to miss. Skipping is the honest representation, not a visit marked abandoned:
			// abandoned means the patient came and left, which is a different fact.
			continue
		}
		day := v.Date.Add(who.shift)
		if !day.Before(time.Now().AddDate(0, 0, -1)) {
			// Anything from yesterday onwards belongs to today's clinic, or to a future this
			// command has no business writing.
			break
		}
		if err := l.pastVisit(ctx, who, v, day, first); err != nil {
			l.log.Warn("a historical visit could not be loaded",
				"patient_id", who.person.ID.String(), "error", err.Error())
			// Stop on this patient rather than trying the next visit. A half-built visit is
			// still *open*, and a patient may only have one open at a time — so carrying on
			// would turn one failure into a refusal for every remaining visit and for today's,
			// which is how a single readable error becomes twenty unreadable ones.
			break
		}
		who.lastSeen = day
		first = false
	}
}

// pastVisit is one complete, closed journey through the clinic.
func (l *loader) pastVisit(ctx context.Context, who *loaded, v synthetic.Visit,
	day time.Time, first bool) (err error) {

	kind := visit.FollowUp
	if first {
		kind = visit.New
	}

	l.at(day, 8, 40)
	opened, err := l.visits.Open(l.as(ctx, "REGISTRATION", stationRegistration), visit.Opening{
		EventID: uuid.New(), PatientID: who.person.ID, VisitType: kind,
		ChiefComplaint: complaintFor(who.source), Source: eventstore.SourceWeb,
	})
	if err != nil {
		return fmt.Errorf("opening the visit: %w", err)
	}
	// Anything that fails from here leaves a visit that was opened and never closed. Recorded
	// as abandoned rather than left open, because an open visit is a live fact — it holds the
	// patient's one open-visit slot and sits on the board forever — while an abandoned one is
	// a completed statement that the journey did not finish, which is what happened.
	defer func() {
		if err != nil {
			l.abandonHalfBuilt(ctx, who, opened.ID)
		}
	}()

	if err := l.encounter(ctx, opened.ID, stationRegistration, "REGISTRATION", day, 8, 45, nil); err != nil {
		return err
	}

	// Station 2. Height once for an adult — it does not change, and the plausibility rule
	// says so — and at every visit for a child, because growth velocity is the reading a
	// paediatric endocrine clinic exists to take.
	err = l.encounter(ctx, opened.ID, stationAnthropometry, "CLINICAL_ASSISTANT", day, 9, 5,
		func(ctx context.Context, encounter uuid.UUID) error {
			return l.anthropometry(ctx, who, v, opened.ID, encounter, first)
		})
	if err != nil {
		return err
	}

	// Station 4, once. The allergy answer is a gate rather than a field: without it the
	// database refuses to queue this patient past step 4 on any future visit, so a register
	// loaded without one would be a register where nobody can reach the physician.
	if first {
		err = l.encounter(ctx, opened.ID, stationHistory, "CLINICAL_ASSISTANT", day, 9, 25,
			func(ctx context.Context, _ uuid.UUID) error {
				return l.assertAllergy(ctx, who, &opened.ID)
			})
		if err != nil {
			return err
		}
	}

	err = l.encounter(ctx, opened.ID, stationExamination, "CLINICAL_ASSISTANT", day, 9, 45,
		func(ctx context.Context, encounter uuid.UUID) error {
			return l.vitals(ctx, who, v, opened.ID, encounter, false)
		})
	if err != nil {
		return err
	}

	if err := l.encounter(ctx, opened.ID, stationConsultation, "PHYSICIAN", day, 10, 20, nil); err != nil {
		return err
	}

	l.at(day, 10, 40)
	_, err = l.visits.Close(l.as(ctx, "PHYSICIAN", stationConsultation), opened.ID, visit.Closing{
		EventID:   uuid.New(),
		Diagnoses: diagnosesFor(who.source),
		Plan:      planFor(who.source, v),
		// Three months is this clinic's ordinary follow-up interval, which is what the
		// generator's own visit spacing assumes.
		NextReviewDays: 90,
		Source:         eventstore.SourceWeb,
	})
	if err != nil {
		return fmt.Errorf("closing the visit: %w", err)
	}
	l.tally.visitsClosed++
	return nil
}

// abandonHalfBuilt closes off a visit this command opened and could not finish.
//
// Best effort, and its own failure is only logged: the caller is already returning an error
// about the thing that went wrong first, and replacing that message with a second one about
// the tidying up would hide the fault somebody actually has to fix.
func (l *loader) abandonHalfBuilt(ctx context.Context, who *loaded, visitID uuid.UUID) {
	_, err := l.visits.Abandon(l.as(ctx, "REGISTRATION", stationRegistration), visitID, uuid.New(),
		"other", "synthload could not complete this visit", eventstore.SourceWeb)
	if err != nil {
		l.log.Warn("a half-built visit could not be abandoned",
			"patient_id", who.person.ID.String(), "error", err.Error())
	}
}

// encounter is one station touch: arrive, do the work, leave.
//
// The work runs between the two rather than after both, because an observation carries the
// encounter it was taken in and §14.2 counts the seconds a patient spent at a station. A
// measurement recorded after the encounter closed would be a value taken at a station the
// patient had already left.
func (l *loader) encounter(ctx context.Context, visitID uuid.UUID, station, role string,
	day time.Time, hour, minute int, work func(context.Context, uuid.UUID) error) error {

	l.at(day, hour, minute)
	arrived, err := l.visits.Arrive(l.as(ctx, role, station), visitID, visit.Arrival{
		EventID: uuid.New(), StationCode: station, Source: eventstore.SourceWeb,
	})
	if err != nil {
		return fmt.Errorf("arriving at %s: %w", station, err)
	}
	l.tally.encounters++

	if work != nil {
		if err := work(l.as(ctx, role, station), arrived.ID); err != nil {
			return err
		}
	}

	l.at(day, hour, minute+10)
	_, err = l.visits.Depart(l.as(ctx, role, station), arrived.ID, visit.Departure{
		EventID: uuid.New(), Outcome: "completed", Source: eventstore.SourceWeb,
	})
	if err != nil {
		return fmt.Errorf("leaving %s: %w", station, err)
	}
	return nil
}

// assertAllergy answers CP54's question in somebody's name.
//
// Almost always "no known allergies", occasionally a recorded one, because that is the
// distribution a clinic actually produces — and because the two are different rows rather
// than a row and an absence, which is the whole point of the checkpoint.
func (l *loader) assertAllergy(ctx context.Context, who *loaded, visitID *uuid.UUID) error {
	_, err := l.allergies.Assert(ctx, uuid.New(), who.person.ID,
		allergy.StatusNoKnown, "", visitID, eventstore.SourceWeb)
	if err != nil {
		return fmt.Errorf("asserting allergy status: %w", err)
	}
	who.allergyAsserted = true
	l.tally.allergies++
	return nil
}
