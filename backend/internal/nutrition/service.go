package nutrition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Recording a recall, and deriving the day's intake (CP59).
//
// # There is no "start an assessment" here, and that is the point
//
// A recall is not opened, held and closed; it is a set of entries against a patient and a date.
// Two assistants who both start recording simply both record — there is no session to fight over,
// no lock to acquire and nothing to reconcile when they finish, which is the whole of criterion 2.
//
// # The intake totals are derived after each entry
//
// `ENERGY_INTAKE` and its three companions are ordinary CP42 derived values, rewritten whenever
// the day's entries change. Rewritten rather than accumulated: a withdrawn entry has to take its
// calories with it, and a total that added and never subtracted would drift the first time an
// operator corrected a duplicate.

// Service records the recall.
type Service struct {
	store   *Store
	events  *eventstore.Store
	clock   interface{ Now() time.Time }
	derived Deriver

	// OnDeriveSkipped is told when a day's total could not be written. Nil in production: a total
	// that failed is not something to refuse a patient's breakfast over, and the next entry
	// rewrites it. A test sets it to see the reason.
	OnDeriveSkipped func(error)
}

// Deriver writes the day's intake as clinical derived values.
//
// An interface for the same reason `assessment` has one: this package holds exactly one opinion
// about clinical writes, which is that it does not perform them itself.
type Deriver interface {
	RecordDerived(ctx context.Context, in clinical.Recording) (clinical.Observation, error)
}

// NewService builds one.
func NewService(store *Store, events *eventstore.Store, clk interface{ Now() time.Time }) *Service {
	return &Service{store: store, events: events, clock: clk}
}

// WithDeriver attaches the thing that can write a clinical derived value.
func (s *Service) WithDeriver(d Deriver) *Service {
	s.derived = d
	return s
}

// Eating is one thing the patient said they ate.
type Eating struct {
	EventID   uuid.UUID
	PatientID uuid.UUID
	VisitID   *uuid.UUID

	RecallDate  string
	Meal        string
	EatenAtHour *int

	FoodCode    string
	MeasureCode string
	Quantity    float64
	Note        string

	LedgerSource eventstore.Source
}

// Record writes one entry and rewrites the day's totals.
func (s *Service) Record(ctx context.Context, in Eating) (Recall, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Recall{}, err
	}

	day, err := parseDay(in.RecallDate)
	if err != nil {
		return Recall{}, err
	}
	if !knownMeal(in.Meal) {
		return Recall{}, fmt.Errorf("%w: %s", ErrBadMeal, in.Meal)
	}
	// The ceiling depends on the measure, and the difference is not pedantry: a hundred cups is
	// nobody's lunch, while two hundred grams is an ordinary plate of rice. One limit for both
	// would either refuse grams — the escape hatch that stops an unweighable measure being a dead
	// end — or accept a hundred bowls of dal.
	// The ceiling comes from the measure's own row rather than from a code comparison here, so
	// that the second universal measure somebody seeds cannot be refused against the wrong limit.
	ceiling, err := s.ceilingFor(ctx, in.MeasureCode)
	if err != nil {
		return Recall{}, err
	}
	if in.Quantity <= 0 || in.Quantity > ceiling {
		return Recall{}, ErrBadQuantity
	}

	// The food and the measure are checked here as well as in the projection, so that an operator
	// gets a sentence rather than a five hundred. The projection's check is the one that holds
	// under a rebuild; this one is the one a person reads.
	food, err := s.store.q.FoodByCode(ctx, strings.TrimSpace(in.FoodCode))
	if errors.Is(err, pgx.ErrNoRows) {
		return Recall{}, fmt.Errorf("%w: %s", ErrUnknownFood, in.FoodCode)
	}
	if err != nil {
		return Recall{}, err
	}
	if grams, err := s.grams(ctx, food.Code, in.MeasureCode, in.Quantity); err != nil {
		return Recall{}, err
	} else if grams <= 0 {
		return Recall{}, fmt.Errorf("%w: %s of %s", ErrUnknownMeasure,
			strings.ToUpper(strings.TrimSpace(in.MeasureCode)), food.Code)
	}

	now := s.clock.Now().UTC()
	// The entry's id is derived from the event's, not invented. Two things follow, and both matter
	// at a station where two tablets are writing: a client can find the row it just wrote among
	// the other operator's identical ones, and a retried write with the same event id produces the
	// same entry id rather than a second row that only the ledger's uniqueness catches.
	eventID := in.EventID
	if eventID == uuid.Nil {
		eventID = uuid.New()
		in.EventID = eventID
	}
	payload := eventstore.DietEntryRecorded{
		EntryID:     EntryIDFor(eventID).String(),
		FacilityID:  actor.FacilityID().String(),
		PatientID:   in.PatientID.String(),
		RecallDate:  day.Format("2006-01-02"),
		Meal:        strings.ToUpper(strings.TrimSpace(in.Meal)),
		EatenAtHour: in.EatenAtHour,
		FoodCode:    food.Code,
		MeasureCode: strings.ToUpper(strings.TrimSpace(in.MeasureCode)),
		Quantity:    in.Quantity,
		Note:        strings.TrimSpace(in.Note),
		RecordedAt:  now,
	}
	if in.VisitID != nil {
		payload.VisitID = in.VisitID.String()
	}
	if err := s.append(ctx, in.EventID, "DIET_ENTRY_RECORDED",
		in.PatientID, in.VisitID, actor, in.LedgerSource, now, payload); err != nil {
		return Recall{}, err
	}

	recall, err := s.store.Recall(ctx, in.PatientID, actor.FacilityID(), day)
	if err != nil {
		return Recall{}, err
	}
	s.deriveIntake(ctx, in.PatientID, in.VisitID, recall, in.LedgerSource)
	return recall, nil
}

// Withdraw takes one entry back, with a reason and a name.
func (s *Service) Withdraw(ctx context.Context, eventID, entry uuid.UUID, reason string,
	source eventstore.Source) (Recall, error) {

	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Recall{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Recall{}, ErrReasonRequired
	}

	row, err := s.store.q.DietEntryByID(ctx, dbgen.DietEntryByIDParams{
		ID: entry, FacilityID: actor.FacilityID(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Recall{}, ErrNoEntry
	}
	if err != nil {
		return Recall{}, err
	}
	if row.WithdrawnAt != nil {
		return Recall{}, ErrAlreadyWithdrawn
	}

	// **Anybody at the station may withdraw an entry, including one somebody else recorded**, and
	// that is deliberate. The commonest reason to take an entry back is that two assistants
	// recorded the same rice; requiring the original recorder to be the one who removes it would
	// mean the duplicate stays until they come back from the next patient. The reason and the name
	// are what make that safe, and both are required.
	now := s.clock.Now().UTC()
	payload := eventstore.DietEntryWithdrawn{
		EntryID:     entry.String(),
		FacilityID:  actor.FacilityID().String(),
		PatientID:   row.PatientID.String(),
		Reason:      reason,
		WithdrawnAt: now,
	}
	var visit *uuid.UUID
	if row.VisitID.Valid {
		id := row.VisitID.UUID
		visit = &id
	}
	if err := s.append(ctx, eventID, "DIET_ENTRY_WITHDRAWN",
		row.PatientID, visit, actor, source, now, payload); err != nil {
		return Recall{}, err
	}

	recall, err := s.store.Recall(ctx, row.PatientID, actor.FacilityID(), row.RecallDate)
	if err != nil {
		return Recall{}, err
	}
	s.deriveIntake(ctx, row.PatientID, visit, recall, source)
	return recall, nil
}

// deriveIntake rewrites the day's four intake values.
//
// Rewritten rather than accumulated: a withdrawn entry has to take its calories with it, and a
// total that added and never subtracted would drift the first time an operator corrected a
// duplicate — which, at a station where two people are entering one recall, is the first hour.
//
// Errors are swallowed and reported through the hook. A recall entry that failed because a derived
// value could not be written would mean an operator cannot record a patient's breakfast because an
// arithmetic step is slow, which is the wrong trade.
func (s *Service) deriveIntake(ctx context.Context, patient uuid.UUID, visit *uuid.UUID,
	recall Recall, source eventstore.Source) {

	if s.derived == nil {
		return
	}
	// **Not `Entries == 0`.** That guard meant withdrawing the *last* standing entry left the four
	// intake values at their old figures, so a patient's record still said they ate a thousand
	// calories on a day with nothing on it — which is exactly the case the design note says must
	// not happen, and exactly the case an operator hits after recording the wrong patient and
	// taking it all back. A day with no food is a day with zero energy, and saying so is the
	// point.
	day, err := parseDay(recall.RecallDate)
	if err != nil {
		return
	}
	inputs := map[string]float64{
		"entries":      float64(recall.Totals.Entries),
		"contributors": float64(recall.Totals.Contributors),
	}
	for code, value := range map[string]float64{
		"ENERGY_INTAKE":  recall.Totals.Kcal,
		"PROTEIN_INTAKE": recall.Totals.Protein,
		"CARB_INTAKE":    recall.Totals.Carb,
		"FAT_INTAKE":     recall.Totals.Fat,
	} {
		amount := value
		recording := clinical.Recording{
			EventID: uuid.New(), PatientID: patient, VisitID: visit,
			Code: code, Value: &amount, Unit: "1",
			// The day being recalled, not the day it was recorded. A timeline that placed a
			// Monday's eating on the Tuesday it was described would misorder every recall.
			EffectiveAt:  day,
			Source:       clinical.Patient,
			LedgerSource: source,
			Formula:      "diet_recall_total", Version: DietTotalVersion,
			Inputs: inputs,
		}
		// **Supersede the previous total rather than writing beside it.** A day's intake is one
		// value that keeps being recomputed, not a series: without this, every entry left another
		// live `ENERGY_INTAKE` for the same day and a reader asking "what did they eat" got
		// whichever row the query happened to return first.
		//
		// SUPERSEDED rather than CORRECTED, and the distinction is CP42's: the earlier total was
		// not wrong, it was the right answer to a shorter list.
		if previous, ok := s.liveTotal(ctx, patient, code, day); ok {
			recording.Replaces = &previous
			recording.ReplacedStatus = clinical.Superseded
		}
		if _, err := s.derived.RecordDerived(ctx, recording); err != nil && s.OnDeriveSkipped != nil {
			s.OnDeriveSkipped(err)
		}
	}
}

// liveTotal is the day's current value for one intake code, if there is one.
func (s *Service) liveTotal(ctx context.Context, patient uuid.UUID, code string,
	day time.Time) (uuid.UUID, bool) {

	var id uuid.UUID
	err := s.store.pool.QueryRow(ctx, `
		SELECT id FROM read.observation
		 WHERE patient_id = $1 AND code = $2 AND status = 'ACTIVE'
		   AND effective_at = $3
		 ORDER BY global_seq DESC
		 LIMIT 1`, patient, code, day).Scan(&id)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// EntryIDFor is the entry a given write produces.
//
// Deterministic, so a client that sent an event id knows which row in the day is its own — the
// alternative is a screen that cannot distinguish its own rice from the other tablet's identical
// rice, recorded a second earlier.
func EntryIDFor(event uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(dietEntryNamespace, event[:])
}

// dietEntryNamespace is a fixed uuid; its only job is to keep the derived ids out of any other
// namespace's range.
var dietEntryNamespace = uuid.MustParse("6f0e5b2a-4c1d-4f6e-9a3b-7d2c8e5f1a90")

// ceilingFor is the most of one measure an entry may carry, read from the measure's own row.
func (s *Service) ceilingFor(ctx context.Context, measure string) (float64, error) {
	var ceiling float64
	err := s.store.pool.QueryRow(ctx,
		`SELECT max_quantity FROM core.food_measure WHERE code = $1`,
		strings.ToUpper(strings.TrimSpace(measure))).Scan(&ceiling)
	if err != nil {
		// An unknown measure. The refusal that names it comes from the portion lookup below; this
		// one only has to stop the quantity check accepting anything.
		return 0, nil
	}
	return ceiling, nil
}

// DietTotalVersion is the version of "add up a day's entries".
//
// It is a version rather than a bare sum because the *inputs* to that sum are the food table, and
// the food table is a content dependency nobody has approved. A total computed against the starter
// list must stay identifiable once a national table replaces it.
const DietTotalVersion = "0.1.0-proposed"

func (s *Service) append(ctx context.Context, eventID uuid.UUID, eventType string,
	patient uuid.UUID, visit *uuid.UUID, actor eventstore.Actor, source eventstore.Source,
	now time.Time, payload any) error {

	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if eventID == uuid.Nil {
		eventID = uuid.New()
	}
	if source == "" {
		source = eventstore.SourceWeb
	}
	envelope := eventstore.Envelope{
		EventID: eventID, AggregateType: "PATIENT", AggregateID: patient,
		PatientID: &patient, VisitID: visit,
		EventType: eventType, EventVersion: 1, OccurredAt: now,
		Actor: actor, Source: source, Payload: encoded,
	}
	return s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbgen.Queries) error {
		_, err := s.events.AppendInTx(ctx, tx, envelope)
		return err
	})
}

func (s *Service) grams(ctx context.Context, food, measure string, quantity float64) (float64, error) {
	var grams *float64
	err := s.store.pool.QueryRow(ctx,
		`SELECT core.food_grams($1, $2, $3::numeric)`, food,
		strings.ToUpper(strings.TrimSpace(measure)), quantity).Scan(&grams)
	if err != nil {
		return 0, err
	}
	if grams == nil {
		return 0, nil
	}
	return *grams, nil
}

// Dhaka is the clinic's wall clock.
//
// "Yesterday" has to be worked out in it, not in UTC. Between midnight and six in the morning local
// the two differ by a day, so a late session — or a queued offline write replayed overnight — would
// file the recall two days out. The client is told not to compute this, which makes the server the
// only place it can be right.
var Dhaka = time.FixedZone("Asia/Dhaka", 6*60*60)

// Yesterday is the day a recall taken now is almost always about, on the clinic's calendar.
func Yesterday(now time.Time) string {
	return now.In(Dhaka).AddDate(0, 0, -1).Format("2006-01-02")
}

// Today is the clinic's today, for the screen that has to warn about a date in the future.
func Today(now time.Time) string {
	return now.In(Dhaka).Format("2006-01-02")
}

func parseDay(raw string) (time.Time, error) {
	day, err := time.Parse("2006-01-02", strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %s", ErrBadDate, raw)
	}
	return day.UTC(), nil
}

func knownMeal(meal string) bool {
	meal = strings.ToUpper(strings.TrimSpace(meal))
	for _, candidate := range Meals {
		if candidate == meal {
			return true
		}
	}
	return false
}
