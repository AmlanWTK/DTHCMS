package synthesis_test

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

	"github.com/AmlanWTK/DTHCMS/backend/internal/ai"
	"github.com/AmlanWTK/DTHCMS/backend/internal/allergy"
	"github.com/AmlanWTK/DTHCMS/backend/internal/assessment"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/exercise"
	"github.com/AmlanWTK/DTHCMS/backend/internal/history"
	"github.com/AmlanWTK/DTHCMS/backend/internal/jobs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/nutrition"
	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/config"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
	"github.com/AmlanWTK/DTHCMS/backend/internal/synthesis"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// The synthesis against a real database, a real queue and a real gateway (CP71).
//
// Everything here needs the database, and the reason is the same one CP70 gives: the rules this
// checkpoint depends on are enforced in SQL as well as in Go. `core.ai_synthesis.context` carries
// `ops.carries_identifier` as a check constraint, the generation is allocated by a unique index
// rather than by a number this process chose, and the SLA deadline is stamped by the queue. A test
// that stubbed the store would prove the Go half and assert nothing about the half that exists to
// catch the Go half being wrong.
//
// The queue is real too — `jobs.Store`, writing to `ops.job` — because acceptance criterion 1 of
// CP69 is that a job enqueued in a rolled-back transaction never runs, and that is only true if the
// insert really is in the caller's transaction.

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
	store     *synthesis.Store
	service   *synthesis.Service
	jobs      *jobs.Store
	provider  *ai.Mock
	aiStore   *ai.Store
}

const testPepper = "0123456789abcdef0123456789abcdef"

func newRig(t *testing.T, tier config.AITier, env config.Environment) *rig {
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
		// Ten in the morning in Faridpur. The hour matters: a clock near midnight UTC would put a
		// visit's clinic day on one side of a boundary and its measurements on the other.
		clock:    clock.NewFixed(time.Date(2026, 9, 14, 4, 0, 0, 0, time.UTC)),
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		provider: ai.NewMock(),
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

	registry, err := ai.LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	r.aiStore = ai.NewStore(pool)
	if err := registry.Deploy(ctx, r.aiStore, r.clock.Now()); err != nil {
		t.Fatal(err)
	}
	minimiser, err := ai.NewMinimiser(testPepper)
	if err != nil {
		t.Fatal(err)
	}
	gateway := ai.NewGateway(ai.GatewayConfig{
		Store: r.aiStore, Registry: registry, Provider: r.provider, Minimiser: minimiser,
		Tier: tier, Env: env, Facility: r.facility,
		Clock: r.clock, Logger: r.log, Jitter: func() float64 { return 0 },
	})

	clinicalStore := clinical.NewStore(pool)
	r.clinicals = clinical.NewService(clinicalStore, r.events, r.clock)
	r.visitStr = visit.NewStore(pool)
	r.visits = visit.NewService(r.visitStr, r.events, r.clock)
	r.store = synthesis.NewStore(pool)
	r.jobs = jobs.NewStore(pool)

	r.service = synthesis.NewService(synthesis.ServiceConfig{
		Store: r.store,
		Stations: synthesis.Stations{
			Visits: r.visitStr, Clinical: clinicalStore, Growth: r.clinicals,
			History: history.NewStore(pool), Allergies: allergy.NewStore(pool),
			Lifestyle: assessment.NewService(assessment.NewStore(pool), r.events, r.clock),
			Nutrition: nutrition.NewStore(pool), Exercise: exercise.NewStore(pool),
		},
		Patients: patient.NewStore(pool),
		Gateway:  gateway,
		Queue:    testQueue{store: r.jobs},
		Events:   r.events, Clock: r.clock, Logger: r.log,
	})
	// The automatic trigger, wired exactly as `cmd/api` wires it. Without this the test would be
	// exercising the button and calling it criterion 2.
	r.visits = r.visits.OnStationFinished(hook{service: r.service}).WithLogger(r.log)
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

type hook struct{ service *synthesis.Service }

func (h hook) EncounterFinished(ctx context.Context, tx pgx.Tx, touch visit.StationTouch) error {
	return h.service.EncounterFinished(ctx, tx, touch)
}

func (r *rig) seed(t *testing.T) {
	t.Helper()
	if _, err := r.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'CA01', 'Clinical Assistant', 'ক্লিনিক্যাল সহকারী', 'active')`,
		r.user, r.facility); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SQL.Exec(`
		INSERT INTO core.device (id, facility_id, name, kind, status, enrolled_at)
		VALUES ($1, $2, 'Tablet 1', 'tablet', 'active', now())`, r.device, r.facility); err != nil {
		t.Fatal(err)
	}
	r.patient = uuid.New()
	if _, err := r.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, name_bn, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          registered_by, registered_at)
		VALUES ($1, $2, 'DTHC-FRD-2026-000901', 'Ayesha Rahman', 'আয়েশা রহমান', 'female',
		        DATE '1985-06-14', 'day', 'national_id', '+8801711111102', 'active', $3, now())`,
		r.patient, r.facility, r.user); err != nil {
		t.Fatal(err)
	}
	// CP54's gate: a patient with no allergy status cannot enter a queue after the history
	// station. Written into the read model rather than through the allergy service for the same
	// reason `visit`'s own tests do it — this is a test of the synthesis, not of station 4.
	if _, err := r.SQL.Exec(`
		INSERT INTO read.allergy_assertion (id, facility_id, patient_id, kind,
		                                    asserted_at, asserted_by, asserted_role,
		                                    event_id, global_seq)
		VALUES ($1, $2, $3, 'NO_KNOWN_ALLERGY', now(), $4, 'HISTORY', $5,
		        (SELECT coalesce(max(global_seq), 0) + 1 FROM read.allergy_assertion))`,
		uuid.New(), r.facility, r.patient, r.user, uuid.New()); err != nil {
		t.Fatal(err)
	}
}

// ctx is a request context carrying a verified principal, which is the only way to obtain an actor.
func (r *rig) ctx() context.Context {
	return httpx.WithPrincipal(context.Background(), httpx.Principal{
		UserID: r.user.String(), FacilityID: r.facility.String(),
		SessionID: uuid.NewSHA1(r.user, []byte("session")).String(),
		Code:      "CA01", DeviceID: r.device.String(),
		Role: "CLINICAL_ASSISTANT", Station: "STN_EXAMINATION",
	})
}

// actor is the worker's, for Perform. `ActorForService` is held to cmd/worker by dthclint, so the
// test uses the test door — which is the same shape and is what the linter's own note anticipates.
func (r *rig) actor() eventstore.Actor {
	return eventstore.ActorForTest(r.user, r.device, r.facility, "SYSTEM", "STN_CONSULTATION")
}

// openVisit opens a follow-up visit and walks it to the last station before the consultation,
// leaving that one unfinished. `finishExamination` is what completes it.
func (r *rig) openVisit(t *testing.T) {
	t.Helper()
	ctx := r.ctx()
	opened, err := r.visits.Open(ctx, visit.Opening{
		EventID: uuid.New(), PatientID: r.patient, VisitType: visit.FollowUp,
		ChiefComplaint: "tired for two weeks, feet tingling at night", Source: eventstore.SourceWeb,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.visit = opened.ID

	for _, station := range []string{"STN_REGISTRATION", "STN_ANTHROPOMETRY", "STN_COUNSELING"} {
		r.walkStation(t, station)
	}
	r.record(t, "BODY_WEIGHT", 71.5, "kg")
	r.record(t, "BP_SYSTOLIC", 148, "mm[Hg]")
	r.record(t, "BP_DIASTOLIC", 92, "mm[Hg]")
}

func (r *rig) walkStation(t *testing.T, station string) {
	t.Helper()
	ctx := r.ctx()
	enc, err := r.visits.Arrive(ctx, r.visit, visit.Arrival{
		EventID: uuid.New(), StationCode: station, Source: eventstore.SourceWeb,
	})
	if err != nil {
		t.Fatalf("arriving at %s: %v", station, err)
	}
	if _, err := r.visits.Depart(ctx, enc.ID, visit.Departure{
		EventID: uuid.New(), Outcome: "completed", Source: eventstore.SourceWeb,
	}); err != nil {
		t.Fatalf("departing %s: %v", station, err)
	}
}

func (r *rig) record(t *testing.T, code string, value float64, unit string) {
	t.Helper()
	v := value
	visitID := r.visit
	if _, _, err := r.clinicals.RecordBatch(r.ctx(), clinical.Batch{
		EventID: uuid.New(), PatientID: r.patient, VisitID: &visitID,
		LedgerSource: eventstore.SourceWeb,
		Records: []clinical.Recording{{
			EventID: uuid.New(), PatientID: r.patient, VisitID: &visitID,
			Code: code, Value: &v, Unit: unit,
			EffectiveAt: r.clock.Now(), Source: clinical.Station,
			LedgerSource: eventstore.SourceWeb,
		}},
	}); err != nil {
		t.Fatalf("recording %s: %v", code, err)
	}
}

// pending is the run currently queued, and fails the test when there is none.
func (r *rig) pending(t *testing.T) synthesis.Run {
	t.Helper()
	view, err := r.service.Current(context.Background(), r.visit, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if view.Run == nil {
		t.Fatalf("no synthesis run exists for this visit; state is %s", view.State)
	}
	return *view.Run
}

// --- the tests ---

// TestFinishingTheLastStationAsksForTheSummaryWithoutAnybodyPressingAnything.
//
// Acceptance criterion 2, and the whole of §7.1's *"the physician can trigger it but by design
// never needs to"*. The trigger fires from a station touch ending, inside that touch's own
// transaction, and this is the test that says so rather than the test that says the button works.
func TestFinishingTheLastStationAsksForTheSummaryWithoutAnybodyPressingAnything(t *testing.T) {
	r := newRig(t, config.TierMock, config.EnvTest)
	r.openVisit(t)

	// Nothing yet: the examination station has not finished, so the stations are not complete.
	view, err := r.service.Current(context.Background(), r.visit, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != synthesis.NotRequested {
		t.Fatalf("a summary was requested before the stations were done: %s", view.State)
	}

	r.walkStation(t, "STN_EXAMINATION")

	run := r.pending(t)
	if run.Trigger != synthesis.Automatic {
		t.Errorf("the run was triggered %s; nobody pressed anything", run.Trigger)
	}
	if run.State != synthesis.Pending {
		t.Errorf("the run is %s, want PENDING", run.State)
	}
	if run.SLADeadline == nil {
		t.Error("the run carries no SLA deadline; §7.1's five minutes is not being measured")
	}
	// The job is really on the queue, in the same transaction the station touch was written in.
	queued, err := r.jobs.List(context.Background(), "AVAILABLE", synthesis.JobKind, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 {
		t.Fatalf("%d synthesis jobs are queued, want 1", len(queued))
	}
	// And the ledger says a summary was asked for, with the trigger on it — which is the only
	// place criterion 2 can be measured from later.
	if got := r.eventTypes(t); !contains(got, "AI_SYNTHESIS_REQUESTED") {
		t.Errorf("the ledger carries %v, with no AI_SYNTHESIS_REQUESTED", got)
	}
}

// TestASummaryIsProducedAndNamesWhatProducedIt.
//
// The happy path end to end, and the two things a physician's screen has to be able to say about
// the page they are reading: it is AI-generated, and this exact prompt and model produced it.
func TestASummaryIsProducedAndNamesWhatProducedIt(t *testing.T) {
	r := newRig(t, config.TierMock, config.EnvTest)
	r.openVisit(t)
	r.walkStation(t, "STN_EXAMINATION")

	run := r.pending(t)
	if err := r.service.Perform(context.Background(), r.actor(), synthesis.JobArgs{
		SynthesisID: run.ID, VisitID: r.visit, Generation: run.Generation,
	}); err != nil {
		t.Fatalf("performing the run: %v", err)
	}

	view, err := r.service.Current(context.Background(), r.visit, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != synthesis.Ready {
		t.Fatalf("the summary is %s: %s", view.State, view.Run.FailureDetail)
	}
	if !view.AIGenerated || !view.Run.AIGenerated {
		t.Error("the summary is not marked AI-generated; criterion 3")
	}
	if view.Degraded {
		t.Error("a ready summary reports the screen as degraded")
	}
	if view.Run.PromptVersion == "" || view.Run.ModelVersion == "" || view.Run.InteractionID == nil {
		t.Errorf("the summary does not name what produced it: prompt %q model %q interaction %v",
			view.Run.PromptVersion, view.Run.ModelVersion, view.Run.InteractionID)
	}
	if view.Run.MetSLA == nil || !*view.Run.MetSLA {
		t.Errorf("met_sla is %v; a run that finished immediately should have met its deadline", view.Run.MetSLA)
	}
	if len(view.Run.Context.Facts) == 0 {
		t.Error("the stored context carries no facts; CP72 would have nothing to ground against")
	}

	// The gateway recorded the call, which is where the cost and the payload are reviewed.
	interaction, err := r.aiStore.Interaction(context.Background(), r.facility, *view.Run.InteractionID)
	if err != nil {
		t.Fatalf("the interaction the summary names is not in the outbound log: %v", err)
	}
	if interaction.AgentCode != synthesis.AgentCode {
		t.Errorf("the interaction is against agent %q", interaction.AgentCode)
	}
	if got := r.eventTypes(t); !contains(got, "AI_SYNTHESIS_COMPLETED") {
		t.Errorf("the ledger carries %v, with no AI_SYNTHESIS_COMPLETED", got)
	}
}

// TestTheOutboundPayloadCarriesNoIdentifier.
//
// The rule CP70 exists for, checked at the one place CP71 could break it: the assembler builds the
// payload, so an assembler that copied a name would defeat the gateway's key-refusal by putting the
// name in a value rather than in a key. The database check constraint on `outbound` is the other
// half; this asserts the payload a person would read in the log.
func TestTheOutboundPayloadCarriesNoIdentifier(t *testing.T) {
	r := newRig(t, config.TierMock, config.EnvTest)
	r.openVisit(t)
	r.walkStation(t, "STN_EXAMINATION")

	run := r.pending(t)
	if err := r.service.Perform(context.Background(), r.actor(), synthesis.JobArgs{
		SynthesisID: run.ID, VisitID: r.visit, Generation: run.Generation,
	}); err != nil {
		t.Fatal(err)
	}
	view, err := r.service.Current(context.Background(), r.visit, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if view.Run.InteractionID == nil {
		t.Fatal("no interaction to inspect")
	}
	interaction, err := r.aiStore.Interaction(context.Background(), r.facility, *view.Run.InteractionID)
	if err != nil {
		t.Fatal(err)
	}
	payload := string(interaction.Outbound)
	for _, forbidden := range []string{
		"Ayesha", "Rahman", "আয়েশা", "8801711111102", "1985-06-14",
		r.patient.String(),
	} {
		if contains([]string{payload}, forbidden) || indexOf(payload, forbidden) {
			t.Errorf("the payload sent to the model carries %q", forbidden)
		}
	}
	// And the stored context, which is the copy CP72 will read and the physician's screen renders.
	stored, err := json.Marshal(view.Run.Context)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Ayesha", "Rahman", "8801711111102"} {
		if indexOf(string(stored), forbidden) {
			t.Errorf("the stored context carries %q", forbidden)
		}
	}
}

// TestAFailedRunLeavesTheDegradedStateAndNotAnEmptyScreen.
//
// Acceptance criterion 4, and D-15 in as many words: *"fail visible, never fail silent, never fail
// invented"*. The physician gets a state, a sentence, and the whole assembled record — not a 404
// and not a blank panel.
func TestAFailedRunLeavesTheDegradedStateAndNotAnEmptyScreen(t *testing.T) {
	r := newRig(t, config.TierMock, config.EnvTest)
	r.openVisit(t)
	r.walkStation(t, "STN_EXAMINATION")

	// The model is down, in the way a model is actually down.
	r.provider.Respond = func(ai.ProviderRequest) (ai.ProviderResponse, error) {
		return ai.ProviderResponse{}, ai.ErrProviderUnavailable
	}

	run := r.pending(t)
	err := r.service.Perform(context.Background(), r.actor(), synthesis.JobArgs{
		SynthesisID: run.ID, VisitID: r.visit, Generation: run.Generation,
	})
	if err == nil {
		t.Error("a provider outage returned no error to the queue, so the job would not be retried")
	}

	view, err := r.service.Current(context.Background(), r.visit, r.facility)
	if err != nil {
		t.Fatalf("the read failed after a failed run, which is the empty screen criterion 4 forbids: %v", err)
	}
	if view.State != synthesis.Failed {
		t.Fatalf("the run is %s after the provider refused every attempt", view.State)
	}
	if !view.Degraded {
		t.Error("a failed run does not report the screen as degraded")
	}
	if view.MessageEN == "" || view.MessageBN == "" {
		t.Error("a failed run carries no sentence for the screen, in one language or the other")
	}
	if view.Run.FailureKind != synthesis.FailureProvider {
		t.Errorf("the failure is classified %q, want PROVIDER", view.Run.FailureKind)
	}
	// The whole point of D-15: the structured record is still there to render.
	if len(view.Run.Context.Facts) == 0 {
		t.Error("a failed run kept no context, so there is nothing to show the physician instead")
	}
	if view.Run.Context.Visit.Complaint == "" {
		t.Error("a failed run's context has no chief complaint in it")
	}
	if !view.Requestable {
		t.Error("a failed run does not offer the button")
	}
	if got := r.eventTypes(t); !contains(got, "AI_SYNTHESIS_FAILED") {
		t.Errorf("the ledger carries %v, with no AI_SYNTHESIS_FAILED", got)
	}
}

// TestARealPatientIsRefusedOnAFreeCredentialAndTheScreenStillWorks.
//
// ADR-0032's rule reaching the physician's screen. The refusal is the gateway's; what this checks
// is that CP71 turns it into a degraded state with a sentence rather than into a silent absence —
// and that the run is *not* retried, because a refusal will be refused identically five times.
func TestARealPatientIsRefusedOnAFreeCredentialAndTheScreenStillWorks(t *testing.T) {
	r := newRig(t, config.TierFree, config.EnvTest)
	r.openVisit(t)
	r.walkStation(t, "STN_EXAMINATION")

	run := r.pending(t)
	if err := r.service.Perform(context.Background(), r.actor(), synthesis.JobArgs{
		SynthesisID: run.ID, VisitID: r.visit, Generation: run.Generation,
	}); err != nil {
		t.Errorf("a tier refusal was returned to the queue for retry; it will be refused identically: %v", err)
	}

	view, err := r.service.Current(context.Background(), r.visit, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != synthesis.Failed || view.Run.FailureKind != synthesis.FailureRefused {
		t.Fatalf("a real patient on a free credential produced %s/%s", view.State, view.Run.FailureKind)
	}
	if !view.Degraded || view.MessageEN == "" {
		t.Error("the refusal did not produce a degraded screen with a sentence on it")
	}

	// And now the deliberate act that makes the exception, which is the only thing that opens the
	// free tier: an entry in the register, with a reason, exactly as cmd/synthload writes one.
	if err := r.aiStore.RegisterSynthetic(context.Background(), r.patient, r.facility,
		"fabricated by the synthetic caseload loader for development and evaluation", nil,
		r.clock.Now()); err != nil {
		t.Fatal(err)
	}
	second, err := r.service.Request(r.ctx(), synthesis.Requesting{
		VisitID: r.visit, Trigger: synthesis.Manual,
		EventID: uuid.New(), Source: eventstore.SourceWeb,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.service.Perform(context.Background(), r.actor(), synthesis.JobArgs{
		SynthesisID: second.ID, VisitID: r.visit, Generation: second.Generation,
	}); err != nil {
		t.Fatal(err)
	}
	view, err = r.service.Current(context.Background(), r.visit, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != synthesis.Ready {
		t.Fatalf("a registered synthetic subject was still refused on the free tier: %s / %s",
			view.State, view.Run.FailureDetail)
	}
}

// TestARerunWithNothingNewCostsNoModelCall.
//
// "Incremental re-run when material new data arrives" has a second half nobody states: when nothing
// material has arrived, nothing should be spent. The run still happens — it assembles, compares and
// records UNCHANGED — because "we looked and there was nothing new" is a different fact from
// "nobody looked", and only one of them should reassure anybody.
func TestARerunWithNothingNewCostsNoModelCall(t *testing.T) {
	r := newRig(t, config.TierMock, config.EnvTest)
	r.openVisit(t)
	r.walkStation(t, "STN_EXAMINATION")

	first := r.pending(t)
	if err := r.service.Perform(context.Background(), r.actor(), synthesis.JobArgs{
		SynthesisID: first.ID, VisitID: r.visit, Generation: first.Generation,
	}); err != nil {
		t.Fatal(err)
	}
	callsAfterFirst := r.interactionCount(t)

	// A re-run asked for with nothing changed in between.
	second, err := r.service.Request(r.ctx(), synthesis.Requesting{
		VisitID: r.visit, Trigger: synthesis.Rerun,
		EventID: uuid.New(), Source: eventstore.SourceWeb,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.service.Perform(context.Background(), r.actor(), synthesis.JobArgs{
		SynthesisID: second.ID, VisitID: r.visit, Generation: second.Generation,
	}); err != nil {
		t.Fatal(err)
	}
	settled, err := r.store.ByID(context.Background(), second.ID, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if settled.State != synthesis.Unchanged {
		t.Fatalf("a re-run with nothing new is %s, want UNCHANGED", settled.State)
	}
	if got := r.interactionCount(t); got != callsAfterFirst {
		t.Errorf("a re-run with nothing new made %d extra model calls", got-callsAfterFirst)
	}

	// And now something material: a corrected blood pressure, which is the checkpoint's own
	// example of what must trigger a re-run.
	r.record(t, "BP_SYSTOLIC", 168, "mm[Hg]")
	third, err := r.service.Request(r.ctx(), synthesis.Requesting{
		VisitID: r.visit, Trigger: synthesis.Rerun,
		EventID: uuid.New(), Source: eventstore.SourceWeb,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.service.Perform(context.Background(), r.actor(), synthesis.JobArgs{
		SynthesisID: third.ID, VisitID: r.visit, Generation: third.Generation,
	}); err != nil {
		t.Fatal(err)
	}
	settled, err = r.store.ByID(context.Background(), third.ID, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if settled.State != synthesis.Ready {
		t.Fatalf("a re-run after a new blood pressure is %s, want READY", settled.State)
	}
	if got := r.interactionCount(t); got <= callsAfterFirst {
		t.Error("a materially changed record did not reach the model")
	}
	// The earlier summary is kept and marked superseded, not overwritten: what the physician was
	// looking at ten minutes ago is exactly the question a review asks.
	runs, err := r.service.History(context.Background(), r.visit, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Fatalf("%d runs are recorded, want 3", len(runs))
	}
	for _, run := range runs {
		if run.Generation < settled.Generation && run.SupersededAt == nil {
			t.Errorf("generation %d was not marked superseded", run.Generation)
		}
	}
}

// TestPerformingTheSameRunTwiceDoesNotProduceTwoSummaries.
//
// At-least-once delivery is the queue's honest contract: an expired lease returns the job to the
// queue and a second worker picks it up. A handler that ran the model again would double the bill
// and put a second, different narrative under the same generation.
func TestPerformingTheSameRunTwiceDoesNotProduceTwoSummaries(t *testing.T) {
	r := newRig(t, config.TierMock, config.EnvTest)
	r.openVisit(t)
	r.walkStation(t, "STN_EXAMINATION")

	run := r.pending(t)
	args := synthesis.JobArgs{SynthesisID: run.ID, VisitID: r.visit, Generation: run.Generation}
	if err := r.service.Perform(context.Background(), r.actor(), args); err != nil {
		t.Fatal(err)
	}
	before := r.interactionCount(t)

	if err := r.service.Perform(context.Background(), r.actor(), args); err != nil {
		t.Errorf("a replayed job returned an error instead of doing nothing: %v", err)
	}
	if got := r.interactionCount(t); got != before {
		t.Errorf("a replayed job made %d extra model calls", got-before)
	}
	runs, err := r.service.History(context.Background(), r.visit, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Errorf("%d runs exist after one job was delivered twice, want 1", len(runs))
	}
}

// TestAskingTwiceWhileOneIsRunningDoesNotQueueTwo.
//
// A physician pressing the button twice, or two stations finishing in the same second. One run in
// flight per visit, and the second caller gets the run that is actually going to answer them.
func TestAskingTwiceWhileOneIsRunningDoesNotQueueTwo(t *testing.T) {
	r := newRig(t, config.TierMock, config.EnvTest)
	r.openVisit(t)
	r.walkStation(t, "STN_EXAMINATION")

	first := r.pending(t)
	second, err := r.service.Request(r.ctx(), synthesis.Requesting{
		VisitID: r.visit, Trigger: synthesis.Manual,
		EventID: uuid.New(), Source: eventstore.SourceWeb,
	})
	if err != nil {
		t.Fatalf("asking again while one is queued returned an error: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("a second request created run %s while %s was already queued", second.ID, first.ID)
	}
	queued, err := r.jobs.List(context.Background(), "AVAILABLE", synthesis.JobKind, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 {
		t.Errorf("%d jobs are queued for one visit, want 1", len(queued))
	}
}

// TestAClosedVisitIsRefused. Producing a summary for a visit the physician has finished with would
// be paying a model to brief nobody.
func TestAClosedVisitIsRefused(t *testing.T) {
	r := newRig(t, config.TierMock, config.EnvTest)
	r.openVisit(t)
	r.walkStation(t, "STN_EXAMINATION")

	if _, err := r.visits.Close(r.ctx(), r.visit, visit.Closing{
		EventID: uuid.New(), Diagnoses: "Type 2 diabetes", Plan: "continue metformin",
		NextReviewDays: 90, Source: eventstore.SourceWeb,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := r.service.Request(r.ctx(), synthesis.Requesting{
		VisitID: r.visit, Trigger: synthesis.Manual,
		EventID: uuid.New(), Source: eventstore.SourceWeb,
	})
	if !errors.Is(err, synthesis.ErrVisitNotOpen) {
		t.Errorf("requesting a summary for a closed visit returned %v", err)
	}
}

// TestTheSLAIsMeasuredAndCountsTheManualOnes.
//
// Acceptance criterion 1 needs a number and criterion 2 needs a ratio; both come from this report,
// and a report that could not distinguish an automatic run from a button press could not answer the
// second question at all.
func TestTheSLAIsMeasuredAndCountsTheManualOnes(t *testing.T) {
	r := newRig(t, config.TierMock, config.EnvTest)
	r.openVisit(t)
	r.walkStation(t, "STN_EXAMINATION")

	run := r.pending(t)
	if err := r.service.Perform(context.Background(), r.actor(), synthesis.JobArgs{
		SynthesisID: run.ID, VisitID: r.visit, Generation: run.Generation,
	}); err != nil {
		t.Fatal(err)
	}

	now := r.clock.Now()
	measured, err := r.service.SLA(context.Background(), r.facility,
		now.Add(-time.Hour), now.Add(time.Hour), 300)
	if err != nil {
		t.Fatal(err)
	}
	if measured.Finished != 1 || measured.Met != 1 {
		t.Errorf("the SLA report says %d finished and %d met, want 1 and 1",
			measured.Finished, measured.Met)
	}
	if measured.Manual != 0 {
		t.Errorf("the SLA report counts %d manual runs; nobody pressed anything", measured.Manual)
	}
	if measured.BudgetSeconds != 300 {
		t.Errorf("the report carries budget %d, want the queue's 300", measured.BudgetSeconds)
	}
}

// TestTheDatabaseRefusesAContextThatNamesAPerson.
//
// The constraint, not the Go. `ops.carries_identifier` guards `core.ai_synthesis.context` exactly as
// it guards the gateway's outbound payload, so an assembler that one day copied a telephone number
// into a note cannot store the context — and therefore cannot show it to anybody.
func TestTheDatabaseRefusesAContextThatNamesAPerson(t *testing.T) {
	r := newRig(t, config.TierMock, config.EnvTest)
	r.openVisit(t)
	r.walkStation(t, "STN_EXAMINATION")

	run := r.pending(t)
	_, err := r.pool.Exec(context.Background(),
		`UPDATE core.ai_synthesis SET context = $2 WHERE id = $1`,
		run.ID, []byte(`{"facts":[{"ref":"x","note":"call 01711234567"}]}`))
	if err == nil {
		t.Fatal("the database accepted an assembled context carrying a telephone number")
	}
	// And the same object without the number is accepted, so the constraint is refusing the
	// number rather than refusing everything.
	if _, err := r.pool.Exec(context.Background(),
		`UPDATE core.ai_synthesis SET context = $2 WHERE id = $1`,
		run.ID, []byte(`{"facts":[{"ref":"x","note":"call the ward"}]}`)); err != nil {
		t.Fatalf("the constraint refuses a context with nothing wrong with it: %v", err)
	}
}

// --- small helpers ---

func (r *rig) interactionCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := r.SQL.QueryRow(
		`SELECT count(*) FROM core.ai_interaction WHERE agent_code = $1 AND status IN ('SUCCEEDED', 'CACHED')`,
		synthesis.AgentCode).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (r *rig) eventTypes(t *testing.T) []string {
	t.Helper()
	rows, err := r.SQL.Query(`SELECT DISTINCT event_type FROM ledger.event WHERE visit_id = $1`, r.visit)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatal(err)
		}
		out = append(out, kind)
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

func indexOf(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}()
}
