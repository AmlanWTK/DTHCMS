package main

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/exercise"
	"github.com/AmlanWTK/DTHCMS/backend/internal/nutrition"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// Today, half-way through.
//
// This is the part that makes the difference between a database with rows in it and a system
// that looks alive. A traffic board with nothing on it, an alert list with nothing on it and a
// "patients seen today" count of zero are three screens a developer cannot tell apart from
// three screens that are broken.
//
// # Everything is dated in the past, and that is load-bearing
//
// The clinic is laid out between an anchor an hour and a half ago and a minute ago, never
// between eight o'clock and now — because a developer who runs this at 07:00 would otherwise
// get a morning that has not happened yet, with queue entries whose waiting time is negative
// and a board that counts backwards. The anchor is pulled forward to midnight when an hour and
// a half ago was yesterday, so a run just after midnight still produces today's clinic rather
// than half of yesterday's.

// criticalToday is how many of today's patients have a value that should make a phone ring.
//
// Four, out of the dozen or so who have reached the examination station. Enough that /alerts
// and the physician's dashboard have a list rather than a single row, and few enough that the
// list still reads as "these four need answering" rather than as an outage.
const criticalToday = 4

// newToday is how many of the cohort are registered this morning rather than a year ago.
//
// Four. Enough that `GET /v1/patients/today` — which means *registered* today, not "in the
// building today", a distinction worth discovering on a full database rather than an empty one
// — returns a list, and few enough that the register still reads as a clinic with two years
// behind it.
const newToday = 4

// window is the stretch of today the clinic is laid out across.
type window struct {
	from time.Time
	span time.Duration
}

// at places a moment a fraction of the way through the window.
func (w window) at(fraction float64) time.Time {
	return w.from.Add(time.Duration(float64(w.span) * clamp(fraction, 0, 1)))
}

func todayWindow(now time.Time) window {
	from := now.Add(-90 * time.Minute)
	if !visit.ClinicDayOf(from).Equal(visit.ClinicDayOf(now)) {
		from = visit.ClinicDayOf(now)
	}
	span := now.Add(-time.Minute).Sub(from)
	if span < time.Minute {
		span = time.Minute
	}
	return window{from: from, span: span}
}

func (l *loader) clinic(ctx context.Context, people []loaded, n int) error {
	if n == 0 {
		return nil
	}
	w := todayWindow(time.Now().UTC())

	// Who is in today. Everybody registered this morning is in the building by definition;
	// the rest are a shuffle rather than the first n, because the cohort is ordered by nothing
	// in particular and "the first sixteen" would still correlate today's clinic with whatever
	// the generator happens to emit first — a demonstration in which every patient in the room
	// has the same presenting problem is a demonstration of the wrong thing.
	var order, returning []int
	for i := range people {
		if people[i].newToday {
			order = append(order, i)
		} else {
			returning = append(returning, i)
		}
	}
	for _, i := range l.rng.Perm(len(returning)) {
		if len(order) >= n {
			break
		}
		order = append(order, returning[i])
	}

	critical := 0
	for slot, index := range order {
		who := &people[index]

		// How far through the morning this patient is: one of the seven stations after
		// registration, taken in turn, so the board shows a floor with people spread across
		// it rather than a single queue at one door.
		stop := 1 + slot%(len(walk)-1)
		// Arrivals over the first half of the window: a patient who arrived early and is
		// still at station 2 is the bottleneck the board exists to make visible.
		arrived := w.at(0.02 + 0.48*float64(slot)/float64(n))
		step := time.Duration(float64(w.span) * 0.05)

		wantsCritical := stop > 4 && critical < criticalToday
		if err := l.attend(ctx, who, arrived, step, stop, wantsCritical); err != nil {
			l.log.Warn("a patient could not be put through today's clinic",
				"patient_id", who.person.ID.String(), "error", err.Error())
			continue
		}
		if wantsCritical {
			critical++
		}
	}
	return nil
}

// attend walks one patient as far through today as they have got, and leaves them there.
func (l *loader) attend(ctx context.Context, who *loaded, arrived time.Time,
	step time.Duration, stop int, critical bool) (err error) {

	kind := visit.FollowUp
	if who.lastSeen.IsZero() {
		kind = visit.New
	}

	l.moment(arrived)
	opened, err := l.visits.Open(l.as(ctx, "REGISTRATION", stationRegistration), visit.Opening{
		EventID: uuid.New(), PatientID: who.person.ID, VisitType: kind,
		ChiefComplaint: complaintFor(who.source), Source: eventstore.SourceWeb,
	})
	if err != nil {
		return fmt.Errorf("opening today's visit: %w", err)
	}
	l.tally.visitsOpen++
	// As in pastVisit: a visit half-way through this walk is left recorded as abandoned rather
	// than open, so a failure does not put a patient on the board who is not being seen.
	defer func() {
		if err != nil {
			l.tally.visitsOpen--
			l.abandonHalfBuilt(ctx, who, opened.ID)
		}
	}()

	// The stations behind them: queued, served, and departed, so that the wait times the
	// board and §14.2 read are real rather than assumed.
	for i := 0; i < stop; i++ {
		at := arrived.Add(time.Duration(i) * step)
		if err := l.finishedStation(ctx, who, opened.ID, i, at, step, critical); err != nil {
			return err
		}
	}

	// And where they are now: in the queue, and possibly already called or being seen.
	l.moment(arrived.Add(time.Duration(stop) * step))
	here := walk[stop]
	if _, err := l.visits.Enqueue(l.as(ctx, here.role, here.station), opened.ID, visit.Joining{
		EventID: uuid.New(), StationCode: here.station, Source: eventstore.SourceWeb,
	}); err != nil {
		return fmt.Errorf("queueing at %s: %w", here.station, err)
	}
	l.tally.queued++

	// A third of the floor is not merely waiting. `CallNext` claims whoever is at the head of
	// that station's queue rather than this patient — which is the point of CP39's
	// `FOR UPDATE SKIP LOCKED` and is also right here: the person who gets called is the one
	// who has waited longest, not the one this loop happens to be holding.
	switch l.rng.Intn(3) {
	case 0:
		l.callAndMaybeStart(ctx, here.station, here.role, false)
	case 1:
		l.callAndMaybeStart(ctx, here.station, here.role, true)
	}
	return nil
}

// finishedStation is one station today's patient has already been through.
func (l *loader) finishedStation(ctx context.Context, who *loaded, visitID uuid.UUID,
	index int, at time.Time, step time.Duration, critical bool) error {

	here := walk[index]
	actor := l.as(ctx, here.role, here.station)

	// Registration is where the visit was opened, so there is no queue to join for it: the
	// patient was in front of the desk, not waiting for it.
	var entry uuid.UUID
	if index > 0 {
		l.moment(at)
		joined, err := l.visits.Enqueue(actor, visitID, visit.Joining{
			EventID: uuid.New(), StationCode: here.station, Source: eventstore.SourceWeb,
		})
		if err != nil {
			return fmt.Errorf("queueing at %s: %w", here.station, err)
		}
		l.tally.queued++
		entry = joined.ID
	}

	l.moment(at.Add(step / 4))
	arrivedAt, err := l.visits.Arrive(actor, visitID, visit.Arrival{
		EventID: uuid.New(), StationCode: here.station, Source: eventstore.SourceWeb,
	})
	if err != nil {
		return fmt.Errorf("arriving at %s: %w", here.station, err)
	}
	l.tally.encounters++

	if err := l.stationWork(actor, who, visitID, arrivedAt.ID, here.station, critical); err != nil {
		return err
	}

	l.moment(at.Add(step * 3 / 4))
	if _, err := l.visits.Depart(actor, arrivedAt.ID, visit.Departure{
		EventID: uuid.New(), Outcome: "completed", Source: eventstore.SourceWeb,
	}); err != nil {
		return fmt.Errorf("leaving %s: %w", here.station, err)
	}
	if entry != uuid.Nil {
		if _, err := l.visits.Leave(actor, entry, visit.Leaving{
			EventID: uuid.New(), Outcome: "served", Source: eventstore.SourceWeb,
		}); err != nil {
			return fmt.Errorf("closing the queue entry at %s: %w", here.station, err)
		}
	}
	return nil
}

// stationWork is what actually gets written down at each station this morning.
func (l *loader) stationWork(ctx context.Context, who *loaded, visitID, encounterID uuid.UUID,
	station string, critical bool) error {

	switch station {
	case stationAnthropometry:
		// Today's weight, on the trajectory the cohort's last visit left off at, so the trend
		// view has a point at the right-hand edge rather than a gap of three months.
		last := lastMeasured(who.source)
		return l.anthropometry(ctx, who, last, visitID, encounterID, who.lastSeen.IsZero())

	case stationHistory:
		// Only for somebody who has never been asked — which today means the four registered
		// this morning. Asking again would be a second assertion of the same fact by the same
		// clinic on the same person, which CP54 would store faithfully and nobody wants.
		if who.allergyAsserted {
			return nil
		}
		return l.assertAllergy(ctx, who, &visitID)

	case stationExamination:
		return l.vitals(ctx, who, lastMeasured(who.source), visitID, encounterID, critical)

	case stationNutrition:
		return l.recall(ctx, who, visitID)

	case stationExercise:
		return l.assess(ctx, who, visitID)
	}
	return nil
}

// callAndMaybeStart advances the head of a station's queue.
//
// Failures are logged and swallowed rather than returned. Whether the station happens to have
// somebody waiting when this runs is a property of the shuffle above, and an empty queue is
// not a reason to abandon a patient's whole visit.
func (l *loader) callAndMaybeStart(ctx context.Context, station, role string, begin bool) {
	actor := l.as(ctx, role, station)
	called, err := l.visits.CallNext(actor, station, uuid.New(), eventstore.SourceWeb)
	if err != nil {
		return
	}
	if !begin {
		return
	}
	arrived, err := l.visits.Arrive(actor, called.VisitID, visit.Arrival{
		EventID: uuid.New(), StationCode: station, Source: eventstore.SourceWeb,
	})
	if err != nil {
		l.log.Debug("a called patient could not be started", "station", station, "error", err.Error())
		return
	}
	l.tally.encounters++
	if _, err := l.visits.BeginService(actor, called.ID, arrived.ID); err != nil {
		l.log.Debug("a started encounter could not be linked to its queue entry",
			"station", station, "error", err.Error())
	}
}

// recall is station 7's 24-hour recall: three or four things the patient remembers eating.
//
// Deliberately short. A recall with every meal filled in is not what a station produces in the
// four minutes it has; three entries and a gap is, and it is also what exercises the code that
// has to total a partial day.
func (l *loader) recall(ctx context.Context, who *loaded, visitID uuid.UUID) error {
	yesterday := l.clock.Now().In(visit.Dhaka).AddDate(0, 0, -1).Format(time.DateOnly)
	meals := []struct {
		meal, food, measure string
		quantity            float64
		hour                int
	}{
		{"BREAKFAST", "RUTI_ATTA", "PIECE", 2, 8},
		{"LUNCH", "RICE_BOILED", "PLATE", 1, 14},
		{"LUNCH", "DAL_MASUR", "BOWL", 1, 14},
		{"DINNER", "RICE_BOILED", "CUP", 2, 21},
	}
	for i, m := range meals[:3+l.rng.Intn(2)] {
		hour := m.hour
		if _, err := l.nutrition.Record(ctx, nutrition.Eating{
			EventID: uuid.New(), PatientID: who.person.ID, VisitID: &visitID,
			RecallDate: yesterday, Meal: m.meal, EatenAtHour: &hour,
			FoodCode: m.food, MeasureCode: m.measure, Quantity: m.quantity,
			LedgerSource: eventstore.SourceWeb,
		}); err != nil {
			return fmt.Errorf("recording recall entry %d: %w", i+1, err)
		}
		l.tally.nutrition++
	}
	return nil
}

// assess is station 8's contraindication assessment.
//
// Every live condition is answered, because the module refuses an assessment that skipped one
// — and rightly: a station that never showed the neuropathy question would produce an
// assessment byte-identical to a careful one, after which the permitted-exercise filter
// computes the list as though the answer were no.
func (l *loader) assess(ctx context.Context, who *loaded, visitID uuid.UUID) error {
	asked, err := l.exerciseStore.LiveConditionCodes(ctx)
	if err != nil {
		return fmt.Errorf("reading the contraindication catalogue: %w", err)
	}
	walks := true
	minutes := 10 + l.rng.Intn(35)
	if _, _, err := l.exercise.Record(ctx, exercise.Recording{
		EventID: uuid.New(), PatientID: who.person.ID, VisitID: &visitID,
		WalksUnaided: &walks, WalkMinutes: &minutes,
		// Nothing applies to most patients, which is the ordinary answer and the one that has
		// to be distinguishable from "not asked" — hence a present, empty list rather than nil.
		Contraindications: contraindicationsFor(who.source, asked),
		Asked:             asked,
		LedgerSource:      eventstore.SourceWeb,
	}); err != nil {
		return fmt.Errorf("recording the exercise assessment: %w", err)
	}
	l.tally.exercise++
	return nil
}

// moment puts the shared clock at an exact instant, for today's clinic where the spacing is
// measured against the wall rather than against a clinic timetable.
func (l *loader) moment(at time.Time) { l.clock.Current = at.UTC() }
