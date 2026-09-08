package dashboard_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/allergy"
	"github.com/AmlanWTK/DTHCMS/backend/internal/assessment"
	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/counseling"
	"github.com/AmlanWTK/DTHCMS/backend/internal/dashboard"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/exercise"
	"github.com/AmlanWTK/DTHCMS/backend/internal/history"
	"github.com/AmlanWTK/DTHCMS/backend/internal/jobs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/nutrition"
	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
	"github.com/AmlanWTK/DTHCMS/backend/internal/synthesis"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// The physician's dashboard against a real database (CP73, §8).
//
// # Why none of this is a unit test with fakes
//
// The thing being built is a **fan-out over six modules' real queries**. A test that handed
// the service six fakes would assert that the fan-out calls its own fakes, which is a
// statement about this file rather than about the screen. Every failure worth catching here —
// a replaced observation appearing in a sparkline, an allergy status collapsed to a list
// length, a decision folded onto the wrong suggestion — happens at the seam between this
// module and a real row.
//
// # What each headline test is defending against, in one sentence
//
//   - **The two allergy states.** `NONE_RECORDED` and `NO_KNOWN_ALLERGY` both arrive with an
//     empty list and mean opposite things, and the dangerous collapse is the one that looks
//     safe: a prescriber reads "none" and writes the penicillin.
//   - **A refused panel drawn as an empty one.** Same shape of error one layer up: "you may
//     not see this" and "this patient has none of these" are opposite facts.
//   - **A corrected value in a sparkline.** A trend that included replaced rows would draw an
//     operator's typo as a clinical event.
//   - **A machine's proposal and the assembler's arithmetic drawn alike.** Criterion 3 is not
//     a label on the panel; it is a mark on each item, because the panel is a mixture.
//   - **A decision against the wrong suggestion.** A reference derived from a list position
//     would move every recorded answer the day a re-run reordered the model's output.
//   - **The Asian BMI cut-offs.** A BMI of 24 is "normal" internationally and "overweight"
//     here, and the whole screening pathway hangs on which side of that line somebody falls.

type rig struct {
	*testsupport.DB
	pool  *pgxpool.Pool
	clock *clock.Fixed
	log   *slog.Logger

	facility uuid.UUID
	user     uuid.UUID
	device   uuid.UUID
	patient  uuid.UUID
	visit    uuid.UUID

	events    *eventstore.Store
	visits    *visit.Service
	visitStr  *visit.Store
	clinicals *clinical.Service
	values    *clinical.Store
	synthesis *synthesis.Service
	synthStr  *synthesis.Store
	service   *dashboard.Service
	emergency *fakeEmergency
}

func newRig(t *testing.T) *rig {
	t.Helper()
	base := testsupport.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)

	pool, err := pgxpool.New(ctx, base.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	r := &rig{
		DB: base, pool: pool, user: uuid.New(), device: uuid.New(),
		// Ten in the morning in Faridpur. The hour matters: a clock near midnight UTC would
		// put a visit's clinic day on one side of a boundary and its measurements on the other.
		clock:     clock.NewFixed(time.Date(2026, 9, 14, 4, 0, 0, 0, time.UTC)),
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		emergency: &fakeEmergency{},
	}
	if err := base.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&r.facility); err != nil {
		t.Fatal(err)
	}

	r.events = eventstore.New(eventstore.Config{
		Pool: pool, Clock: r.clock, Synchronous: projection.NewSyncSet(projection.Default),
	})
	if err := projection.NewEngineWithEvents(pool, projection.Default, r.events).Register(ctx); err != nil {
		t.Fatal(err)
	}
	r.seed(t)

	r.values = clinical.NewStore(pool)
	r.clinicals = clinical.NewService(r.values, r.events, r.clock)
	r.visitStr = visit.NewStore(pool)
	r.visits = visit.NewService(r.visitStr, r.events, r.clock)
	r.synthStr = synthesis.NewStore(pool)

	r.synthesis = synthesis.NewService(synthesis.ServiceConfig{
		Store: r.synthStr,
		Stations: synthesis.Stations{
			Visits: r.visitStr, Clinical: r.values, Growth: r.clinicals,
			History: history.NewStore(pool), Allergies: allergy.NewStore(pool),
			Lifestyle: assessment.NewService(assessment.NewStore(pool), r.events, r.clock),
			Nutrition: nutrition.NewStore(pool), Exercise: exercise.NewStore(pool),
		},
		Patients: patient.NewStore(pool),
		Queue:    testQueue{store: jobs.NewStore(pool)},
		Events:   r.events, Clock: r.clock, Logger: r.log,
	})

	r.service = dashboard.NewService(dashboard.Config{
		Patients: patient.NewStore(pool), Visits: r.visitStr,
		Clinical: r.clinicals, Values: r.values,
		History: history.NewStore(pool), Allergies: allergy.NewStore(pool),
		Counseling: counseling.NewStore(pool),
		Synthesis:  r.synthesis, Events: r.events,
		Emergency:  r.emergency, Clock: r.clock,
	})
	return r
}

// testQueue is the composition root's adapter, copied because a test is a composition root too.
type testQueue struct{ store *jobs.Store }

func (q testQueue) EnqueueSynthesis(ctx context.Context, tx pgx.Tx, now time.Time,
	args synthesis.JobArgs, dedupeKey string) (synthesis.Enqueued, error) {

	job, err := q.store.EnqueueTx(ctx, tx, now, jobs.Enqueueing{
		Kind: synthesis.JobKind, Args: args, DedupeKey: dedupeKey,
	})
	if err != nil {
		return synthesis.Enqueued{}, err
	}
	return synthesis.Enqueued{JobID: job.ID, SLADeadline: job.SLADeadline}, nil
}

// fakeEmergency stands in for CP22's break-glass service.
//
// The one dependency here that *is* faked, and deliberately: `audit` may not be imported by
// this module at all, so what is being tested is the seam — that an open door reaches the
// screen — rather than the audit module's own query, which has its own tests.
type fakeEmergency struct{ note *dashboard.BreakGlassNote }

func (f *fakeEmergency) OpenFor(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*dashboard.BreakGlassNote, error) {
	return f.note, nil
}

func (r *rig) seed(t *testing.T) {
	t.Helper()
	if _, err := r.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'PH01', 'Dr Nahid', 'ডা. নাহিদ', 'active')`,
		r.user, r.facility); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SQL.Exec(`
		INSERT INTO core.device (id, facility_id, name, kind, status, enrolled_at)
		VALUES ($1, $2, 'Tablet 1', 'tablet', 'active', now())`, r.device, r.facility); err != nil {
		t.Fatal(err)
	}
	r.patient = r.register(t, "DTHC-FRD-2026-000901", "Ayesha Rahman", "আয়েশা রহমান",
		"female", "1985-06-14")
	r.assertNoKnownAllergies(t, r.patient)
}

// register puts a patient in the read model directly.
//
// Through SQL rather than through `patient.Service.Register`, and the reason is worth stating
// because it is the opposite of what `cmd/synthload` does: the loader exists to prove the
// write path, and this exists to test a *read*. Driving four registrations through the
// duplicate matcher and the sealer would make a dashboard test fail when identity changed.
func (r *rig) register(t *testing.T, clinicalID, en, bn, sex, birth string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := r.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, name_bn, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          registered_by, registered_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7::date, 'day', 'national_id', '+8801711111102',
		        'active', $8, now())`,
		id, r.facility, clinicalID, en, bn, sex, birth, r.user); err != nil {
		t.Fatal(err)
	}
	return id
}

func (r *rig) assertNoKnownAllergies(t *testing.T, id uuid.UUID) {
	t.Helper()
	if _, err := r.SQL.Exec(`
		INSERT INTO read.allergy_assertion (id, facility_id, patient_id, kind,
		                                    asserted_at, asserted_by, asserted_role,
		                                    event_id, global_seq)
		VALUES ($1, $2, $3, 'NO_KNOWN_ALLERGY', now(), $4, 'HISTORY', $5,
		        (SELECT coalesce(max(global_seq), 0) + 1 FROM read.allergy_assertion))`,
		uuid.New(), r.facility, id, r.user, uuid.New()); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) ctx() context.Context {
	return httpx.WithPrincipal(context.Background(), httpx.Principal{
		UserID: r.user.String(), FacilityID: r.facility.String(),
		SessionID: uuid.NewSHA1(r.user, []byte("session")).String(),
		Code:      "PH01", DeviceID: r.device.String(),
		Role: "PHYSICIAN", Station: "STN_CONSULTATION",
	})
}

// physician is the subject the route guard would have resolved.
//
// Built by hand rather than through `rbac.Resolver`, because the resolver reads role grants
// this test would otherwise have to seed — and what is being tested is what the dashboard
// does *with* a subject, not how one is produced. The permissions come from the real
// catalogue, so a role that loses a grant tomorrow changes this test's answer.
func (r *rig) physician() rbac.Subject {
	return r.as(auth.RolePhysician)
}

func (r *rig) as(role auth.RoleCode) rbac.Subject {
	return rbac.Subject{
		UserID: r.user, FacilityID: r.facility,
		Roles: []auth.RoleCode{role}, ActiveRole: role,
		Permissions: rbac.RolePermissions[role],
	}
}

func (r *rig) openVisit(t *testing.T) {
	t.Helper()
	opened, err := r.visits.Open(r.ctx(), visit.Opening{
		EventID: uuid.New(), PatientID: r.patient, VisitType: visit.FollowUp,
		ChiefComplaint: "tired for two weeks, feet tingling at night",
		Source:         eventstore.SourceWeb,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.visit = opened.ID
}

// record writes one value, at a given day, optionally replacing an earlier one.
func (r *rig) record(t *testing.T, code string, value float64, unit string, at time.Time) uuid.UUID {
	t.Helper()
	v := value
	visitID := r.visit
	rec := clinical.Recording{
		EventID: uuid.New(), PatientID: r.patient, Code: code, Value: &v, Unit: unit,
		EffectiveAt: at, Source: clinical.Station, LedgerSource: eventstore.SourceWeb,
	}
	if visitID != uuid.Nil {
		rec.VisitID = &visitID
	}
	obs, _, err := r.clinicals.Record(r.ctx(), rec)
	if err != nil {
		t.Fatalf("recording %s: %v", code, err)
	}
	return obs.ID
}

func (r *rig) assemble(t *testing.T, subject rbac.Subject) dashboard.View {
	t.Helper()
	view, err := r.service.Assemble(context.Background(), dashboard.Request{
		PatientID: r.patient, FacilityID: r.facility, Subject: subject,
	})
	if err != nil {
		t.Fatalf("assembling the dashboard: %v", err)
	}
	return view
}

// --- the panels ---

func TestEveryPanelFromSectionEightArrivesInOneRead(t *testing.T) {
	// Acceptance criterion 2 as a value assertion. The panels are named individually rather
	// than counted, because a count would pass on a response that had the right number of
	// wrong things in it.
	r := newRig(t)
	r.openVisit(t)
	r.record(t, "BODY_HEIGHT", 158, "cm", r.clock.Now())
	r.record(t, "BODY_WEIGHT", 60, "kg", r.clock.Now())
	r.record(t, "HBA1C", 8.2, "%#ngsp", r.clock.Now())
	r.record(t, "BP_SYSTOLIC", 148, "mm[Hg]", r.clock.Now())

	view := r.assemble(t, r.physician())

	if view.Patient.NameEN == "" || view.Patient.AgeText == "" {
		t.Errorf("the snapshot has no demographics: %+v", view.Patient)
	}
	if view.Visit == nil || view.Visit.ChiefComplaint == "" {
		t.Errorf("the visit and its complaint are missing: %+v", view.Visit)
	}
	if view.Allergies == nil {
		t.Error("the allergy state is absent; it is never omitted for a caller who got this far")
	}
	if len(view.Vitals) == 0 {
		t.Error("no current values")
	}
	if view.Counseling == nil {
		t.Error("the counselling checkpoint is absent for a visit that exists")
	}
	if view.Summary == nil {
		t.Error("the centre panel is absent for a physician")
	}
	if view.Assistant == nil {
		t.Error("the right panel is absent for a physician")
	}
	// The trend is what §8 calls a sparkline, and one value is a series of one — which is
	// still a series, and still drawn.
	if !hasTrend(view, "HBA1C") {
		t.Errorf("no HbA1c trend; got %v", trendCodes(view))
	}
}

func TestTheSnapshotSaysWhoEnteredEveryValue(t *testing.T) {
	// Criterion 5. The dashboard cannot make attribution one interaction away if it hands the
	// client a number with nobody's name attached — so the assertion is on the payload, not on
	// a screen: every value carries the person, the role and the station it came from.
	r := newRig(t)
	r.openVisit(t)
	r.record(t, "HBA1C", 8.2, "%#ngsp", r.clock.Now())

	view := r.assemble(t, r.physician())

	for _, obs := range view.Vitals {
		if obs.RecordedBy == uuid.Nil {
			t.Errorf("%s has no author on it", obs.Code)
		}
		if obs.RecordedRole == "" {
			t.Errorf("%s has no role on it", obs.Code)
		}
	}
	for _, trend := range view.Trends {
		for _, point := range trend.Points {
			if point.RecordedBy == uuid.Nil {
				t.Errorf("a %s sparkline point has no author on it", trend.Code)
			}
		}
	}
}

func TestNobodyHasAskedIsNotDrawnTheSameWayAsNoKnownAllergies(t *testing.T) {
	// CP54's whole argument, carried into CP73's payload. Both states arrive with an empty
	// list; only one of them is safe to read as reassurance, and the unsafe one is the one
	// that looks safe.
	r := newRig(t)
	r.openVisit(t)

	asked := r.assemble(t, r.physician())
	if asked.Allergies.Status != "NO_KNOWN_ALLERGY" {
		t.Fatalf("expected NO_KNOWN_ALLERGY, got %q", asked.Allergies.Status)
	}
	if len(asked.Allergies.Allergies) != 0 {
		t.Fatalf("this patient has no allergies recorded; got %d", len(asked.Allergies.Allergies))
	}

	// A second patient nobody has asked. The lists are identical; the status is not.
	unasked := r.register(t, "DTHC-FRD-2026-000902", "Rafiq Hasan", "রফিক হাসান", "male", "1979-02-02")
	view, err := r.service.Assemble(context.Background(), dashboard.Request{
		PatientID: unasked, FacilityID: r.facility, Subject: r.physician(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Allergies.Status != "NONE_RECORDED" {
		t.Fatalf("a patient nobody has asked must read NONE_RECORDED, got %q", view.Allergies.Status)
	}
	if view.Allergies.Status == asked.Allergies.Status {
		t.Fatal("the two allergy states are indistinguishable on the payload, which is the " +
			"failure this test exists for: a prescriber reads 'none' and writes the penicillin")
	}
	if len(view.Allergies.Allergies) != len(asked.Allergies.Allergies) {
		t.Fatal("the two states differ by their list length, which means this test would pass " +
			"even if the status were dropped")
	}
}

func TestAPanelTheCallerMayNotSeeIsAbsentAndNamed(t *testing.T) {
	// The same class of error one layer up from the allergy one. An absent panel and an empty
	// panel mean opposite things, and a screen that could not tell them apart would report "no
	// active conditions" to somebody who was simply not shown them.
	r := newRig(t)
	r.openVisit(t)

	// The registration officer holds `patient.read.demographics` and nothing clinical. They
	// would never reach this endpoint — the route refuses them — but the service must still
	// name what it withheld, because the route is not the only caller a service ever has.
	view := r.assemble(t, r.as(auth.RoleRegistration))

	if view.Conditions != nil {
		t.Errorf("a role blinded from clinical history was given %d conditions", len(view.Conditions))
	}
	if view.Summary != nil {
		t.Error("a role without ai.synthesis.read was given the AI narrative")
	}
	if view.Assistant != nil {
		t.Error("a role without ai.synthesis.read was given the AI suggestions")
	}
	named := map[string]bool{}
	for _, omission := range view.Omitted {
		named[omission.Panel] = true
		if omission.ReasonEN == "" || omission.ReasonBN == "" {
			t.Errorf("%s was withheld without a sentence in both languages", omission.Panel)
		}
	}
	for _, panel := range []string{"active_conditions", "summary"} {
		if !named[panel] {
			t.Errorf("%s was withheld and not named; an absent panel that says nothing is "+
				"indistinguishable from a patient who has none of that thing", panel)
		}
	}
}

func TestTheAllergyStateReachesEvenARoleThatSeesNothingElse(t *testing.T) {
	// The one panel with no gate of its own. A header with no allergy line looks like a
	// patient with no allergies, so there is no caller who may see this screen and may not see
	// the strip.
	r := newRig(t)
	r.openVisit(t)
	view := r.assemble(t, r.as(auth.RoleRegistration))
	if view.Allergies == nil {
		t.Fatal("the allergy state was withheld; a missing strip reads as 'no allergies'")
	}
}

// --- the trends ---

func TestACorrectedValueIsNotDrawnInTheSparkline(t *testing.T) {
	// A trend that included replaced rows would draw an operator's typo as a clinical event —
	// a step down and back up that a physician would try to explain.
	r := newRig(t)
	r.openVisit(t)
	day := r.clock.Now()
	wrong := r.record(t, "HBA1C", 12.5, "%#ngsp", day.AddDate(0, -3, 0))
	r.record(t, "HBA1C", 9.0, "%#ngsp", day.AddDate(0, -6, 0))

	// The correction: a new value that replaces the first.
	v := 8.4
	visitID := r.visit
	if _, _, err := r.clinicals.Record(r.ctx(), clinical.Recording{
		EventID: uuid.New(), PatientID: r.patient, VisitID: &visitID,
		Code: "HBA1C", Value: &v, Unit: "%#ngsp", EffectiveAt: day.AddDate(0, -3, 0),
		Source: clinical.Station, LedgerSource: eventstore.SourceWeb,
		Replaces: &wrong, ReplacedStatus: clinical.Corrected,
	}); err != nil {
		t.Fatal(err)
	}

	view := r.assemble(t, r.physician())
	trend := trendFor(t, view, "HBA1C")
	for _, point := range trend.Points {
		if point.Status != clinical.Active {
			t.Errorf("a %s point is %s; a replaced value has no place in a trend",
				point.Code, point.Status)
		}
		// Asserted on the *entered* value rather than the canonical one, because that is
		// what the operator typed and what the mistake looked like. HbA1c is stored in
		// mmol/mol and read in NGSP %, and a test that compared against 12.5 in the
		// canonical unit would be comparing against a number nobody ever entered.
		if point.EnteredValue != nil && *point.EnteredValue == 12.5 {
			t.Error("the corrected 12.5 is still in the sparkline")
		}
	}
	if len(trend.Points) != 2 {
		t.Fatalf("expected two live points, got %d", len(trend.Points))
	}
	// Oldest first, always. Two screens disagreeing about the direction would draw the same
	// patient improving and deteriorating.
	if !trend.Points[0].EffectiveAt.Before(trend.Points[1].EffectiveAt) {
		t.Error("the points are not oldest first")
	}
	if trend.Change == nil {
		t.Fatal("two points and no change computed")
	}
	// The change is computed from the two live points and in their own canonical unit, so it
	// is asserted against them rather than against the numbers that were typed. What matters
	// is that it is the *live* pair: a change computed against the corrected 12.5 would tell a
	// physician this patient's control collapsed and then recovered.
	if trend.Change.From != *trend.Points[0].Value || trend.Change.To != *trend.Points[1].Value {
		t.Errorf("the change is not the first and last live points: %+v", trend.Change)
	}
	if trend.Change.Delta >= 0 {
		t.Errorf("9.0%% to 8.4%% is an improvement and the delta is %v", trend.Change.Delta)
	}
}

func TestTenYearsOfValuesStillProducesFivePoints(t *testing.T) {
	// The performance corpus in miniature, and the property that makes the endpoint's cost a
	// property of the *clinic* rather than of the patient. A dashboard whose payload grew with
	// the length of the record would be slowest for exactly the patient it matters most for.
	r := newRig(t)
	r.openVisit(t)
	day := r.clock.Now()
	for quarter := 40; quarter >= 1; quarter-- {
		r.record(t, "HBA1C", 7.0+float64(quarter)/40, "%#ngsp", day.AddDate(0, -quarter*3, 0))
	}

	view := r.assemble(t, r.physician())
	trend := trendFor(t, view, "HBA1C")
	if len(trend.Points) != dashboard.TrendPoints {
		t.Fatalf("expected %d points from forty values, got %d",
			dashboard.TrendPoints, len(trend.Points))
	}
	// The newest five, not the oldest five. A sparkline of a patient's first year in care is
	// a chart of somebody who no longer exists.
	newest := trend.Points[len(trend.Points)-1]
	if !newest.EffectiveAt.After(day.AddDate(0, -6, 0)) {
		t.Errorf("the last point is %s, which is not one of the newest five", newest.EffectiveAt)
	}
}

// --- the BMI ---

func TestTheBodyMassClassUsesTheAsianCutOffs(t *testing.T) {
	// A BMI of 24 is "normal" internationally and "overweight" in a Bangladeshi patient, and
	// the whole screening pathway hangs on which side of that line somebody falls.
	r := newRig(t)
	r.openVisit(t)
	// 158cm and 60kg is 24.03 — inside the international normal band and outside the Asian one.
	r.record(t, "BODY_HEIGHT", 158, "cm", r.clock.Now())
	r.record(t, "BODY_WEIGHT", 60, "kg", r.clock.Now())
	visitID := r.visit
	if _, _, err := r.clinicals.RecordBatch(r.ctx(), clinical.Batch{
		EventID: uuid.New(), PatientID: r.patient, VisitID: &visitID,
		LedgerSource: eventstore.SourceWeb, AsianScale: true,
		Derive:       []clinical.Derivable{clinical.DeriveBMI},
	}); err != nil {
		t.Fatal(err)
	}

	view := r.assemble(t, r.physician())
	if view.BodyMass == nil {
		t.Fatal("a derived BMI exists and the snapshot has no body-mass card")
	}
	if view.BodyMass.Scale != "asian" {
		t.Errorf("the scale is %q; this clinic is in Faridpur", view.BodyMass.Scale)
	}
	if view.BodyMass.Class != "overweight" {
		t.Errorf("a BMI of %.2f banded as %q; on the Asian cut-offs it is overweight, and "+
			"banding it 'normal' is the international scale leaking in",
			*view.BodyMass.Observation.Value, view.BodyMass.Class)
	}
	if view.BodyMass.ClassVersion == "" {
		t.Error("no class version; a band with no version cannot be read against a screenshot")
	}
	// The card carries the stored observation, not a recomputed number — so who derived it is
	// one interaction away like everything else.
	if view.BodyMass.Observation.RecordedBy == uuid.Nil {
		t.Error("the BMI has no author on it")
	}
}

// --- the AI panels ---

func TestTheRightPanelSaysWhatWroteEachItem(t *testing.T) {
	// Criterion 3, as a property of each item rather than of the panel. The panel is a
	// mixture: a model's proposal and the assembler's arithmetic look identical on a screen
	// and are not the same kind of claim.
	r := newRig(t)
	r.openVisit(t)
	r.requestSummary(t)

	view := r.assemble(t, r.physician())
	if view.Assistant == nil {
		t.Fatal("no right panel")
	}
	if len(view.Assistant.Suggestions) == 0 {
		t.Fatal("a freshly requested summary has gaps in its context and the panel is empty")
	}
	for _, s := range view.Assistant.Suggestions {
		switch s.Origin {
		case dashboard.OriginModel, dashboard.OriginSystem:
		default:
			t.Errorf("%s carries origin %q; an unmarked item is an unmarked machine claim",
				s.Ref, s.Origin)
		}
		if s.Kind == dashboard.KindGap && s.Origin != dashboard.OriginSystem {
			t.Errorf("%s is a deterministic gap marked as model output; marking the "+
				"assembler's arithmetic as AI trains a physician to discount the one item "+
				"on the panel that is certainly true", s.Ref)
		}
	}
	if !view.Assistant.AIGenerated {
		t.Error("the panel does not declare itself AI-generated")
	}
}

func TestAPendingSummaryStillCarriesTheDeterministicGaps(t *testing.T) {
	// D-15's degraded state, as the right panel sees it. A physician does not lose "no HbA1c
	// in twelve months" because a provider is slow — that finding never needed a model.
	r := newRig(t)
	r.openVisit(t)
	r.requestSummary(t)

	view := r.assemble(t, r.physician())
	if view.Summary == nil {
		t.Fatal("no centre panel")
	}
	if !view.Summary.Degraded {
		t.Fatal("a pending run is not marked degraded, so the screen would draw an absent " +
			"narrative as a finished one")
	}
	if view.Summary.Narrative != "" {
		t.Error("a pending run has a narrative, which no model has written")
	}
	if view.Summary.MessageEN == "" || view.Summary.MessageBN == "" {
		t.Error("the degraded state has no sentence in one of the two languages")
	}
	gaps := 0
	for _, s := range view.Assistant.Suggestions {
		if s.Origin == dashboard.OriginSystem {
			gaps++
		}
	}
	if gaps == 0 {
		t.Fatal("a degraded panel with nothing on it; the assembler's own findings do not " +
			"depend on a model having answered")
	}
}

func TestASummaryNobodyHasAskedForIsAStateAndNotAnAbsence(t *testing.T) {
	r := newRig(t)
	r.openVisit(t)

	view := r.assemble(t, r.physician())
	if view.Summary == nil {
		t.Fatal("the centre panel is absent for a physician on a visit with no run")
	}
	if view.Summary.State != synthesis.NotRequested {
		t.Errorf("state is %q; a visit nobody has asked about is NOT_REQUESTED", view.Summary.State)
	}
	if !view.Summary.Requestable {
		t.Error("the button is not offered on a visit that has never been summarised")
	}
	if view.Assistant == nil {
		t.Error("the right panel is absent rather than empty; 'nothing has been suggested' " +
			"and 'you may not see suggestions' are different facts")
	}
}

// --- accept, edit, reject ---

func TestARejectionIsRecordedAndComesBackOnTheNextRead(t *testing.T) {
	// The right panel's three buttons are state, not a colour. A rejection that vanished would
	// leave no evidence the physician had considered the draft.
	r := newRig(t)
	r.openVisit(t)
	r.requestSummary(t)

	before := r.assemble(t, r.physician())
	target := before.Assistant.Suggestions[0]

	if _, err := r.service.Decide(r.ctx(), dashboard.Deciding{
		VisitID: r.visit, PatientID: r.patient, FacilityID: r.facility,
		Ref: target.Ref, Kind: dashboard.Rejected, Note: "already ordered last week",
	}); err != nil {
		t.Fatalf("recording a rejection: %v", err)
	}

	after := r.assemble(t, r.physician())
	found := false
	for _, s := range after.Assistant.Suggestions {
		if s.Ref != target.Ref {
			continue
		}
		found = true
		if s.Decision == nil {
			t.Fatal("the rejection did not come back; a decision the screen forgets is a " +
				"decision the physician makes again every time the panel refreshes")
		}
		if s.Decision.Kind != dashboard.Rejected {
			t.Errorf("the decision came back as %q", s.Decision.Kind)
		}
		if s.Decision.Note != "already ordered last week" {
			t.Errorf("the note was lost: %q", s.Decision.Note)
		}
		if s.Decision.DecidedBy != r.user {
			t.Error("the decision has nobody's name against it")
		}
		if s.Decision.Generation < 1 {
			t.Error("the decision names no generation, so nothing can tell whether it was " +
				"made against the summary on screen")
		}
	}
	if !found {
		t.Fatalf("%s is no longer in the panel", target.Ref)
	}
	// And every other suggestion is untouched — a fold that wrote the decision onto the wrong
	// line, or onto all of them, is the failure a reference derived from a list position would
	// produce.
	decided := 0
	for _, s := range after.Assistant.Suggestions {
		if s.Decision != nil {
			decided++
		}
	}
	if decided != 1 {
		t.Fatalf("one decision was recorded and %d suggestions carry one", decided)
	}
}

func TestChangingYourMindWritesASecondEventAndTheLaterOneWins(t *testing.T) {
	r := newRig(t)
	r.openVisit(t)
	r.requestSummary(t)
	target := r.assemble(t, r.physician()).Assistant.Suggestions[0]

	for _, kind := range []dashboard.DecisionKind{dashboard.Rejected, dashboard.Accepted} {
		if _, err := r.service.Decide(r.ctx(), dashboard.Deciding{
			VisitID: r.visit, PatientID: r.patient, FacilityID: r.facility,
			Ref: target.Ref, Kind: kind,
		}); err != nil {
			t.Fatalf("recording %s: %v", kind, err)
		}
	}

	after := r.assemble(t, r.physician())
	for _, s := range after.Assistant.Suggestions {
		if s.Ref == target.Ref && s.Decision.Kind != dashboard.Accepted {
			t.Fatalf("the panel shows %q; the later decision is what stands", s.Decision.Kind)
		}
	}

	// Both are in the ledger. Nothing is updated and nothing is deleted, which is what makes
	// "they accepted it after first rejecting it" a question the record can answer.
	var kinds []string
	events, err := r.events.Stream(context.Background(), "VISIT", r.visit, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.EventType != "AI_SUGGESTION_DECIDED" {
			continue
		}
		var payload eventstore.AISuggestionDecided
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, payload.Decision)
	}
	if len(kinds) != 2 || kinds[0] != "REJECTED" || kinds[1] != "ACCEPTED" {
		t.Fatalf("the ledger holds %v; both decisions must survive, in order", kinds)
	}
}

func TestADecisionAboutSomethingTheSummaryDoesNotSuggestIsRefused(t *testing.T) {
	// The stale-panel case. A decision naming a reference nobody can resolve is a row that
	// renders against nothing forever.
	r := newRig(t)
	r.openVisit(t)
	r.requestSummary(t)

	_, err := r.service.Decide(r.ctx(), dashboard.Deciding{
		VisitID: r.visit, PatientID: r.patient, FacilityID: r.facility,
		Ref: "diagnosis:something_no_model_said", Kind: dashboard.Accepted,
	})
	if !errors.Is(err, dashboard.ErrNoSuchSuggestion) {
		t.Fatalf("expected ErrNoSuchSuggestion, got %v", err)
	}
}

func TestAnEditWithNothingEditedIsRefusedRatherThanTreatedAsAnAcceptance(t *testing.T) {
	r := newRig(t)
	r.openVisit(t)
	r.requestSummary(t)
	target := r.assemble(t, r.physician()).Assistant.Suggestions[0]

	if _, err := r.service.Decide(r.ctx(), dashboard.Deciding{
		VisitID: r.visit, PatientID: r.patient, FacilityID: r.facility,
		Ref: target.Ref, Kind: dashboard.Edited,
	}); !errors.Is(err, dashboard.ErrEditNeedsText) {
		t.Fatalf("expected ErrEditNeedsText, got %v", err)
	}
	// And nothing was written. A refusal that still appended would be worse than none.
	view := r.assemble(t, r.physician())
	for _, s := range view.Assistant.Suggestions {
		if s.Decision != nil {
			t.Fatalf("%s carries a decision after a refused edit", s.Ref)
		}
	}
}

func TestADecisionOnAnotherPatientsVisitIsRefused(t *testing.T) {
	r := newRig(t)
	r.openVisit(t)
	r.requestSummary(t)
	target := r.assemble(t, r.physician()).Assistant.Suggestions[0]

	other := r.register(t, "DTHC-FRD-2026-000903", "Nasrin Akter", "নাসরিন আক্তার", "female", "1990-01-01")
	_, err := r.service.Decide(r.ctx(), dashboard.Deciding{
		VisitID: r.visit, PatientID: other, FacilityID: r.facility,
		Ref: target.Ref, Kind: dashboard.Accepted,
	})
	if !errors.Is(err, dashboard.ErrNoSuchPatient) {
		t.Fatalf("a decision written onto another patient's consultation was allowed: %v", err)
	}
}

// --- break-glass ---

func TestReadingThroughTheEmergencyDoorSaysSoOnTheScreen(t *testing.T) {
	// CP22 alarms every administrator. What it cannot do is tell the person using it, while
	// they are using it — and an emergency access somebody has forgotten is open has stopped
	// being an emergency.
	r := newRig(t)
	r.openVisit(t)

	ordinary := r.assemble(t, r.physician())
	if ordinary.Access.Basis != dashboard.BasisNormal {
		t.Fatalf("an ordinary read reports basis %q", ordinary.Access.Basis)
	}
	if ordinary.Access.BreakGlass != nil {
		t.Fatal("an ordinary read carries a break-glass note")
	}

	r.emergency.note = &dashboard.BreakGlassNote{
		ID: uuid.New(), Justification: "unconscious patient, no attendant, needs insulin history",
		GrantedAt: r.clock.Now().Add(-time.Hour), ExpiresAt: r.clock.Now().Add(3 * time.Hour),
	}
	emergency := r.assemble(t, r.physician())
	if emergency.Access.Basis != dashboard.BasisBreakGlass {
		t.Fatalf("a break-glass read reports basis %q", emergency.Access.Basis)
	}
	if emergency.Access.BreakGlass == nil || emergency.Access.BreakGlass.Justification == "" {
		t.Fatal("the banner has no justification on it; reading back the sentence the " +
			"clinician typed an hour ago is the reminder the door is still open")
	}
}

// --- the serialiser ---

func TestTheResponseSerialisesAndDropsWhatARoleMayNotSee(t *testing.T) {
	// The first production use of CP20's serialiser, and the layer that fails closed on a
	// field somebody adds next year. Two assertions: the type is serialisable at all — a
	// clinical field with no `visible` tag makes it not — and the guarded field really is
	// removed from the bytes rather than nulled.
	r := newRig(t)
	r.openVisit(t)
	r.record(t, "HBA1C", 8.2, "%#ngsp", r.clock.Now())

	view := r.assemble(t, r.physician())
	if view.Summary == nil || view.Conditions == nil {
		t.Fatal("the physician's own view is missing the fields this test is about, so the " +
			"assertions below would pass against a payload that never had them")
	}

	mine := decode(t, r.physician(), view)
	if _, present := mine["active_conditions"]; !present {
		t.Error("the physician did not receive active_conditions")
	}
	identity, _ := mine["patient"].(map[string]any)
	if _, present := identity["clinical_id"]; !present {
		t.Error("the physician did not receive the clinical id")
	}

	// The pharmacist: `patient.read.demographics` and `patient.read.allergies`, and nothing
	// clinical. The guarded fields must be **gone from the bytes** — not null, not empty.
	//
	// Decoded into a fresh map each time, which is not a style preference: `json.Unmarshal`
	// into a map that already has keys *merges* rather than replaces, so a second decode into
	// the same variable leaves the first payload's keys behind and every assertion below
	// passes whatever the serialiser did. That is exactly how this test first passed while
	// asserting nothing, and it is the shape of hole this project has been bitten by twice.
	blinded := decode(t, r.as(auth.RolePharmacist), view)
	if _, present := blinded["active_conditions"]; present {
		t.Error("a role blinded from clinical history received the active_conditions key; " +
			"'may not see' has to mean the key is absent from the bytes")
	}
	if _, present := blinded["summary"]; present {
		t.Error("a role without ai.synthesis.read received the summary key")
	}
	if _, present := blinded["allergies"]; !present {
		t.Error("the allergy state was serialised away; the pharmacist is exactly who needs it")
	}
}

// decode serialises the view for one subject and returns the keys that survived.
//
// A fresh map every call. See the note in the test above: a shared one silently merges.
func decode(t *testing.T, subject rbac.Subject, view dashboard.View) map[string]any {
	t.Helper()
	raw, err := rbac.Marshal(subject, view)
	if err != nil {
		t.Fatalf("the dashboard payload is not serialisable: %v\n\n"+
			"A clinical-looking field with no `visible` tag makes the whole type refuse; "+
			"that is the mechanism working, and the fix is the tag rather than the rule.", err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// --- helpers ---

// requestSummary asks for a run and leaves it pending.
//
// Pending rather than ready, and that is the state most of these tests want: there is no model
// in this rig, and a `READY` run would have to be faked. What a pending run has is a real
// assembled context with real gaps in it, which is what the right panel's deterministic half
// is built from — and it is also D-15's degraded state, which is the one a screen gets wrong.
func (r *rig) requestSummary(t *testing.T) {
	t.Helper()
	if _, err := r.synthesis.Request(r.ctx(), synthesis.Requesting{
		VisitID: r.visit, Trigger: synthesis.Manual,
		EventID: uuid.New(), Source: eventstore.SourceWeb,
	}); err != nil {
		t.Fatalf("requesting a summary: %v", err)
	}
}

func hasTrend(view dashboard.View, code string) bool {
	for _, trend := range view.Trends {
		if trend.Code == code {
			return true
		}
	}
	return false
}

func trendCodes(view dashboard.View) []string {
	out := make([]string, 0, len(view.Trends))
	for _, trend := range view.Trends {
		out = append(out, trend.Code)
	}
	return out
}

func trendFor(t *testing.T, view dashboard.View, code string) dashboard.Trend {
	t.Helper()
	for _, trend := range view.Trends {
		if trend.Code == code {
			return trend
		}
	}
	t.Fatalf("no %s trend; got %v", code, trendCodes(view))
	return dashboard.Trend{}
}
