package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
	"github.com/AmlanWTK/DTHCMS/backend/internal/synthesis"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// The pre-consultation synthesis, in the worker (CP71, §7.1).
//
// # Why the model call happens here and only here
//
// The API decides that a summary is warranted, assembles the context and queues the run. It never
// contacts a model. That is the whole reason this process exists: a synthesis takes seconds to
// minutes, and a clinician entering a blood pressure must not be behind one in a queue. So this is
// the only process in the system that builds an [ai.Gateway], and `synthesis.Service.Perform`
// refuses to run in a process without one rather than acquiring one by accident.
//
// # The actor, and why the door to it is narrow
//
// A run has no person in it. The job was queued by an operator finishing a station touch or by
// somebody pressing a button, but the *work* happens minutes later with nobody at a keyboard, and
// the events it writes still have to say who wrote them. `eventstore.ActorForService` is the one
// door to that, and `dthclint` holds it to `cmd/worker` — see the note on the function itself for
// why a constructor like that must not be reachable from a handler.

// synthesisHandler builds the queue handler for `clinical.synthesis`.
//
// It returns an error rather than exiting, because a worker that cannot do synthesis can still do
// escalations, purges and OCR — and a process that refused to start over one kind would take the
// others down with it. `Registry.Check` then refuses at start-up if this queue's kind has no
// handler, which is the loud version of the same fact.
func synthesisHandler(rt *platform.Runtime, store *jobs.Store,
	facilityID uuid.UUID) (jobs.Handler, error) {

	registry, err := ai.LoadRegistry()
	if err != nil {
		return nil, fmt.Errorf("the prompt registry does not load: %w", err)
	}
	minimiser, err := ai.NewMinimiser(rt.Config.Secrets.IdentifierPepper)
	if err != nil {
		return nil, fmt.Errorf("the PHI minimiser does not build: %w", err)
	}

	// The provider is decided by the tier, and the tier is decided by configuration (D-07,
	// criterion 3 of CP70). `mock` contacts nothing; `free` and `paid` both speak the Gemini
	// protocol and differ only in the credential — which is what makes Vertex AI a base URL change
	// rather than a second adapter.
	var provider ai.Provider = ai.NewMock()
	if rt.Config.AI.Tier != "mock" {
		provider = ai.NewGemini(rt.Config.AI.BaseURL, rt.Config.AI.APIKey, rt.Config.AI.Timeout)
	}

	aiStore := ai.NewStore(rt.DB.Pool)

	var instruments *ai.Instruments
	if rt.Telemetry.Active() {
		meter := rt.Telemetry.Meter("dthcms/ai")
		if built, err := ai.NewInstruments(meter); err != nil {
			rt.Logger.Warn("AI instruments unavailable", "error", err.Error())
		} else {
			instruments = built
		}
		// CP72's quality gauges, on the worker for the reason the queue's are: they read tables,
		// and a dozen API instances polling them would be a dozen identical time series. The worker
		// is the process that is already awake, and it is also the only one that ever calls a
		// model — so the numbers and the work they describe are collected in one place.
		if err := ai.RegisterQualityMetrics(ai.QualityMetricsConfig{
			Store: aiStore, Meter: meter, Clock: clock.Real{}, Facility: facilityID,
			Agents: []string{synthesis.AgentCode}, Logger: rt.Logger,
		}); err != nil {
			rt.Logger.Warn("AI quality metrics unavailable", "error", err.Error())
		}
	}

	gateway := ai.NewGateway(ai.GatewayConfig{
		Store: aiStore, Registry: registry, Provider: provider, Minimiser: minimiser,
		Metrics: instruments,
		Tier:    rt.Config.AI.Tier, Env: rt.Config.Env, Facility: facilityID,
		Clock: clock.Real{}, Logger: rt.Logger,
	})

	events := eventstore.New(eventstore.Config{
		Pool:  rt.DB.Pool,
		Clock: clock.Real{},
		// The same synchronous projections the API writes through. A synthesis appends only
		// operational events — requested, completed, failed — but it appends them through the one
		// path everything else uses, because a second append path is a second set of rules about
		// what a projection sees.
		Synchronous: projection.NewSyncSet(projection.Default),
	})

	clinicalStore := clinical.NewStore(rt.DB.Pool)
	service := synthesis.NewService(synthesis.ServiceConfig{
		Store: synthesis.NewStore(rt.DB.Pool),
		Stations: synthesis.Stations{
			Visits:    visit.NewStore(rt.DB.Pool),
			Clinical:  clinicalStore,
			Growth:    clinical.NewService(clinicalStore, events, clock.Real{}),
			History:   history.NewStore(rt.DB.Pool),
			Allergies: allergy.NewStore(rt.DB.Pool),
			Lifestyle: assessment.NewService(assessment.NewStore(rt.DB.Pool), events, clock.Real{}),
			Nutrition: nutrition.NewStore(rt.DB.Pool),
			Exercise:  exercise.NewStore(rt.DB.Pool),
		},
		Patients: patient.NewStore(rt.DB.Pool),
		Gateway:  gateway,
		// The worker never enqueues a synthesis — it performs one — but the service takes a queue
		// because the same type serves both processes, and a nil here would be a nil waiting for
		// the day somebody moves a trigger. It is the real adapter, and it costs nothing unused.
		Queue:  workerQueue{store: store},
		Events: events, Clock: clock.Real{}, Logger: rt.Logger,
	})

	return func(ctx context.Context, job jobs.Running) error {
		var args synthesis.JobArgs
		if err := json.Unmarshal(job.Args, &args); err != nil {
			// A job whose arguments do not decode will never decode. Returning the error lets the
			// queue retry it to its limit and then dead-letter it visibly, which is where an
			// operator will see it — a swallowed decode failure would be a summary that silently
			// never appeared.
			return fmt.Errorf("synthesis: the job arguments do not decode: %w", err)
		}
		// The attempt gets whatever is left of the SLA rather than the whole of it, so that a job
		// picked up four minutes late does not spend another five minutes inside a promise that has
		// already expired. `Running.SLADeadline` exists for exactly this.
		attempt, cancel := context.WithTimeout(ctx, remaining(job, rt))
		defer cancel()
		return service.Perform(attempt, eventstore.ActorForService(facilityID, "SYNTHESIS"), args)
	}, nil
}

// remaining is how long this attempt gets.
//
// The SLA deadline when there is one and it is still in the future; otherwise the fallback. The
// fallback is not the SLA: a job that has already missed its deadline should still be *attempted*,
// because a summary four minutes late is worth far more to a physician than no summary at all — it
// simply must not hold a worker for another five minutes while it happens.
func remaining(job jobs.Running, rt *platform.Runtime) time.Duration {
	if left, has := job.Deadline(time.Now().UTC()); has && left > 30*time.Second {
		return left
	}
	rt.Logger.Info("a synthesis job is being attempted past its SLA deadline",
		"job_id", job.ID.String(), "attempt", job.Attempt)
	return lateAttemptBudget
}

// lateAttemptBudget is what a job past its deadline gets. Two minutes: the agent's own timeout is
// one hundred and twenty seconds, so this is enough for one attempt and not enough for three.
const lateAttemptBudget = 2 * time.Minute

// workerQueue is the queue adapter, identical in shape to the API's.
//
// Duplicated rather than shared, because the two composition roots are two binaries and the only
// place to share it would be a module — and a module that could enqueue a job is exactly what
// `architecture.json` refuses `synthesis`.
type workerQueue struct{ store *jobs.Store }

func (q workerQueue) EnqueueSynthesis(ctx context.Context, tx pgx.Tx, now time.Time,
	args synthesis.JobArgs, dedupeKey string) (synthesis.Enqueued, error) {

	job, err := q.store.EnqueueTx(ctx, tx, now, jobs.Enqueueing{
		Kind: synthesis.JobKind, Args: args, DedupeKey: dedupeKey,
	})
	if err != nil {
		return synthesis.Enqueued{}, err
	}
	return synthesis.Enqueued{JobID: job.ID, SLADeadline: job.SLADeadline}, nil
}
