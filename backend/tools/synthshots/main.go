// Command synthshots produces the twenty summaries Dr. Nahid reviews (CP71 criterion 5).
//
// # What this is for
//
// The checkpoint's fifth acceptance criterion is *"Dr. Nahid's review of 20 summaries meets an
// agreed usefulness bar"*, and there is no way to hold that review without twenty summaries in
// front of him. So this walks twenty visits from the synthetic cohort through the **real**
// pipeline — the real assembler, the real gateway, the real prompt, the real schema validator, the
// real storage — and writes one page showing, side by side for each patient, exactly what the model
// was given and exactly what came back.
//
// The context beside the output is the point. A physician reading only the narrative can say
// whether it reads well; a physician reading the context beside it can say whether the *right
// things were put in front of the model*, which is the half of this checkpoint that decides
// quality and the only half that can be fixed by anything other than a better model.
//
// # The provider, stated plainly because it matters to how the page should be read
//
// The provider is `ai.Mock` with a **deterministic composer** attached. No language model is
// contacted, no key is needed, nothing costs anything, and the same cohort produces the same page
// every time.
//
//	go run ./tools/synthshots -out /home/claude/shots/cp71-summaries.html
//
// The composer writes its narrative from the assembled context by rule. That means the page is
// honest evidence about three things and no evidence at all about a fourth:
//
//   - it shows what the assembler actually put in front of a model, per patient, in full;
//   - it shows the shape a summary arrives in and how it will read on a screen;
//   - it proves the whole pipeline runs end to end on real cohort data inside the SLA;
//   - it says **nothing** about the prose a real model would write, which is what criterion 5 is
//     ultimately about.
//
// Closing criterion 5 therefore needs a second run of this command against a paid Gemini
// credential. The page says so at the top, in the largest words on it, because a reviewer who
// mistook a template for a model's writing would approve or reject the wrong thing.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
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
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
	"github.com/AmlanWTK/DTHCMS/backend/internal/synthesis"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "synthshots:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		out   = flag.String("out", "cp71-summaries.html", "where to write the page")
		count = flag.Int("n", 20, "how many visits to summarise")
	)
	flag.Parse()

	ctx := context.Background()
	rt, err := platform.Boot(ctx, platform.Options{
		Service: "synthshots", NeedsDB: true, NoTelemetry: true,
	})
	if err != nil {
		return err
	}
	defer rt.Close()

	if rt.Config.Env.IsProduction() {
		return fmt.Errorf("refused: this writes a page of clinical summaries to a file, and the environment is production")
	}

	pool := rt.DB.Pool
	var facility uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT core.default_facility()`).Scan(&facility); err != nil {
		return fmt.Errorf("finding the facility; has migrate run? %w", err)
	}

	registry, err := ai.LoadRegistry()
	if err != nil {
		return err
	}
	if err := registry.Deploy(ctx, ai.NewStore(pool), time.Now().UTC()); err != nil {
		return err
	}
	minimiser, err := ai.NewMinimiser(rt.Config.Secrets.IdentifierPepper)
	if err != nil {
		return err
	}

	provider := ai.NewMock()
	provider.Respond = compose
	gateway := ai.NewGateway(ai.GatewayConfig{
		Store: ai.NewStore(pool), Registry: registry, Provider: provider, Minimiser: minimiser,
		Tier: rt.Config.AI.Tier, Env: rt.Config.Env, Facility: facility,
		Clock: clock.Real{}, Logger: rt.Logger,
	})

	events := eventstore.New(eventstore.Config{
		Pool: pool, Clock: clock.Real{}, Synchronous: projection.NewSyncSet(projection.Default),
	})
	clinicalStore := clinical.NewStore(pool)
	jobStore := jobs.NewStore(pool)
	service := synthesis.NewService(synthesis.ServiceConfig{
		Store: synthesis.NewStore(pool),
		Stations: synthesis.Stations{
			Visits: visit.NewStore(pool), Clinical: clinicalStore,
			Growth:    clinical.NewService(clinicalStore, events, clock.Real{}),
			History:   history.NewStore(pool),
			Allergies: allergy.NewStore(pool),
			Lifestyle: assessment.NewService(assessment.NewStore(pool), events, clock.Real{}),
			Nutrition: nutrition.NewStore(pool),
			Exercise:  exercise.NewStore(pool),
		},
		Patients: patient.NewStore(pool),
		Gateway:  gateway,
		Queue:    queue{store: jobStore},
		Events:   events, Clock: clock.Real{}, Logger: rt.Logger,
	})

	who, err := actorContext(ctx, pool, facility)
	if err != nil {
		return err
	}

	// The same actor the requests are attributed to. `eventstore.ActorForService` is held to
	// `cmd/worker` by dthclint, and rightly — the moment a constructor like that is reachable from
	// anywhere, any handler can attribute its own writes to "the system". So this command performs
	// its runs under the principal it requested them with, which is honest for a one-off tool: the
	// events say a person asked for these, and a person did.
	performer, err := eventstore.ActorFrom(who)
	if err != nil {
		return err
	}

	visits, err := pickVisits(ctx, pool, facility, *count)
	if err != nil {
		return err
	}
	if len(visits) == 0 {
		return fmt.Errorf("no visits found; run cmd/synthload first")
	}

	page := pageData{
		GeneratedAt: time.Now().UTC().Format("2 January 2006, 15:04 UTC"),
		Tier:        string(rt.Config.AI.Tier),
	}
	for _, candidate := range visits {
		started := time.Now()
		run, err := service.Request(who, synthesis.Requesting{
			VisitID: candidate.visitID, Trigger: synthesis.Manual,
			EventID: uuid.New(), Source: eventstore.SourceWeb,
		})
		if err != nil {
			rt.Logger.Warn("a summary could not be requested", "visit_id", candidate.visitID.String(), "error", err.Error())
			continue
		}
		// The worker's own call, made inline. The queue row exists and is left available; nothing
		// here is trying to be a worker, and a job that a real worker later picks up will find the
		// run already finished and do nothing, which is the idempotence the handler is built on.
		if err := service.Perform(ctx, performer, synthesis.JobArgs{
			SynthesisID: run.ID, VisitID: candidate.visitID, Generation: run.Generation,
		}); err != nil {
			rt.Logger.Warn("a summary could not be produced", "visit_id", candidate.visitID.String(), "error", err.Error())
		}
		view, err := service.Current(ctx, candidate.visitID, facility)
		if err != nil {
			return err
		}
		page.Summaries = append(page.Summaries, summaryOf(candidate, view, time.Since(started)))
	}

	rendered, err := render(page)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, rendered, 0o644); err != nil {
		return err
	}
	fmt.Printf("Wrote %d summaries to %s\n", len(page.Summaries), *out)
	return nil
}

// queue is the composition root's adapter, again. See cmd/api/synthesis_bridge.go.
type queue struct{ store *jobs.Store }

func (q queue) EnqueueSynthesis(ctx context.Context, tx pgx.Tx, now time.Time,
	args synthesis.JobArgs, dedupeKey string) (synthesis.Enqueued, error) {

	job, err := q.store.EnqueueTx(ctx, tx, now, jobs.Enqueueing{
		Kind: synthesis.JobKind, Args: args, DedupeKey: dedupeKey,
	})
	if err != nil {
		return synthesis.Enqueued{}, err
	}
	return synthesis.Enqueued{JobID: job.ID, SLADeadline: job.SLADeadline}, nil
}

// candidate is one visit to summarise, with the identifying detail the *page* needs.
//
// The name is read from the database here and never enters the pipeline: it is put on the page so
// that a physician can recognise the patient they are reviewing, and it reaches the model through
// nothing. That is the strip-and-restore boundary made visible — this file holds a name, and the
// twenty payloads beside it do not.
type candidate struct {
	visitID     uuid.UUID
	patientID   uuid.UUID
	nameEN      string
	clinicalRef string
	visitType   string
	clinicDay   time.Time
	complaint   string
}

func pickVisits(ctx context.Context, pool interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, facility uuid.UUID, count int) ([]candidate, error) {

	// Today's open visits first, because those are the ones a summary is actually for; closed
	// visits fill the rest so that the page still has twenty on a database loaded with `-today 0`.
	rows, err := pool.Query(ctx, `
		SELECT v.id, v.patient_id, p.name_en, p.clinical_id, v.visit_type, v.clinic_day,
		       coalesce(v.chief_complaint, '')
		  FROM core.visit v
		  JOIN core.patient p ON p.id = v.patient_id
		 WHERE v.facility_id = $1
		 ORDER BY (v.status = 'open') DESC, v.clinic_day DESC
		 LIMIT $2`, facility, count)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.visitID, &c.patientID, &c.nameEN, &c.clinicalRef,
			&c.visitType, &c.clinicDay, &c.complaint); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// actorContext builds a request context carrying the physician's principal.
//
// The same shape `cmd/synthload` uses, and for the same reason: an actor may only be obtained from
// a verified principal, and a command line has to construct one deliberately rather than being
// handed a door that skips the check.
func actorContext(ctx context.Context, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, facility uuid.UUID) (context.Context, error) {

	var userID uuid.UUID
	var code string
	if err := pool.QueryRow(ctx,
		`SELECT id, employee_code FROM core.app_user
		  WHERE facility_id = $1 AND status = 'active' ORDER BY employee_code LIMIT 1`,
		facility).Scan(&userID, &code); err != nil {
		return nil, fmt.Errorf("finding a user to attribute the requests to; has devseed run? %w", err)
	}
	return httpx.WithPrincipal(ctx, httpx.Principal{
		UserID: userID.String(), FacilityID: facility.String(),
		Code: code, DeviceID: shotsDevice.String(),
		Role: "PHYSICIAN", Station: "STN_CONSULTATION",
	}), nil
}

// shotsDevice is the device every request this command makes is attributed to. A reserved uuid
// belonging to no tablet, following the precedent `cmd/synthload` set: an invented enrolment would
// be a credential-shaped row somebody eventually treats as evidence.
var shotsDevice = uuid.MustParse("00000000-0000-4000-8000-000053407501")
