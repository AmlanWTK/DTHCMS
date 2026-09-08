// Command api serves the DTHCMS HTTP API.
//
// It serves the operational endpoints — /healthz, /readyz, /version — the authentication
// endpoints under /v1/auth, and an authenticated /v1 namespace whose middleware chain is
// wired ahead of the modules that will fill it. Clinical routes arrive with the modules
// that own them.
package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/ai"
	"github.com/AmlanWTK/DTHCMS/backend/internal/allergy"
	"github.com/AmlanWTK/DTHCMS/backend/internal/assessment"
	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/auth/pwhash"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/consent"
	"github.com/AmlanWTK/DTHCMS/backend/internal/counseling"
	"github.com/AmlanWTK/DTHCMS/backend/internal/dashboard"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/exercise"
	"github.com/AmlanWTK/DTHCMS/backend/internal/history"
	"github.com/AmlanWTK/DTHCMS/backend/internal/jobs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/nutrition"
	"github.com/AmlanWTK/DTHCMS/backend/internal/offline"
	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/blobstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/cache"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/config"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/idempotency"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/secretbox"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/version"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
	"github.com/AmlanWTK/DTHCMS/backend/internal/quality"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
	"github.com/AmlanWTK/DTHCMS/backend/internal/realtime"
	"github.com/AmlanWTK/DTHCMS/backend/internal/synthesis"
	"github.com/AmlanWTK/DTHCMS/backend/internal/terminology"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

func main() {
	if code := run(); code != 0 {
		os.Exit(code)
	}
}

func run() int {
	ctx := context.Background()

	rt, err := platform.Boot(ctx, platform.Options{
		Service:    "api",
		NeedsDB:    true,
		NeedsCache: true,
	})
	if err != nil {
		// Configuration problems are printed plainly: at this point there is no logger,
		// and the person reading this is trying to fix a deployment.
		fmt.Fprintf(os.Stderr, "api: cannot start: %v\n", err)
		if config.IsInvalid(err) {
			fmt.Fprintln(os.Stderr, "\nFix the settings above and start again. "+
				"Nothing is served until configuration is valid.")
		}
		return 1
	}
	defer rt.Close()

	build := version.Current()

	health := &httpx.Health{
		Service:   "api",
		Version:   build.Version,
		Commit:    build.Commit,
		BuildTime: build.BuildTime,
		Logger:    rt.Logger,
		Timeout:   3 * time.Second,
		Dependencies: []httpx.Dependency{
			{Name: "postgres", Check: rt.DB.Check, Critical: true},
			{Name: "redis", Check: rt.Cache.Check, Critical: true},
			{Name: "blobstore", Check: rt.Blob.Check, Critical: false},
		},
	}

	instrumentation, err := httpx.NewInstrumentation(rt.Telemetry)
	if err != nil {
		rt.Logger.Error("cannot create HTTP instruments", "error", err.Error())
		return 1
	}

	// Authentication (CP16).
	//
	// The facility is resolved once at start-up rather than per request. There is one today.
	// Looking it up here means a login cannot fail because a reference-data query did, and
	// means the process refuses to start when the seed is missing — which is a legible
	// failure at deploy time rather than every login refusing for a reason nobody can see.
	facilityRow, err := dbgen.New(rt.DB.Pool).GetFacilityByCode(ctx, "DTHC-FRD")
	if err != nil {
		rt.Logger.Error("cannot resolve the facility; has migrate run?",
			"code", "DTHC-FRD", "error", err.Error())
		return 1
	}

	authStore := auth.NewPostgresStore(rt.DB.Pool)

	// The security audit log (CP22): one recorder for the process, and the bridge that
	// lets auth speak to it without importing it.
	auditStore := audit.NewPostgresStore(rt.DB.Pool)
	auditRecorder := audit.NewRecorder(auditStore, clock.Real{}, rt.Logger)
	auditSigner, err := auditSignerFrom(rt.Config.Audit)
	if err != nil {
		rt.Logger.Error("cannot build the audit signing key", "error", err.Error())
		return 1
	}
	bridge := &auditBridge{recorder: auditRecorder}

	// The clinical write path (CP23) with its synchronous projections attached (CP25).
	// Patient registration is the first route to append through it (CP29).
	// TestTheClinicalStoreCarriesItsSynchronousProjections holds the assembly together.
	events := clinicalStore(rt.DB.Pool)
	rt.Logger.Info("clinical write path ready",
		"synchronous_projections", strings.Join(synchronousNames(), ", "),
		"head", clinicalHead(ctx, rt.DB.Pool))

	// The key ring for secrets at rest (ADR-0012). Config has already refused the local
	// key outside local and test; what is left to check is that the material parses.
	ring, err := secretRing(rt.Config.Secrets)
	if err != nil {
		rt.Logger.Error("cannot build the secret key ring", "error", err.Error())
		return 1
	}
	secondFactor := auth.NewSecondFactor(auth.SecondFactorConfig{
		Store: authStore, Users: authStore, Ring: ring, Clock: clock.Real{},
	})

	secondFactor.WithAudit(bridge)

	sessions := auth.NewSessions(auth.SessionsConfig{
		Store:        authStore,
		Hasher:       pwhash.New(pwhash.DefaultParams()),
		Clock:        clock.Real{},
		SecondFactor: secondFactor,
	}).WithAudit(bridge)

	// Devices (CP18). Redis remembers request nonces; the store holds the public keys.
	devices := auth.NewDevices(auth.DevicesConfig{Store: authStore, Nonces: rt.Cache, Clock: clock.Real{}})
	deviceHandlers := auth.NewDeviceHandlers(auth.DeviceHandlersConfig{
		Devices: devices, Store: authStore, Logger: rt.Logger,
	})

	authHandlers := auth.NewHandlers(auth.HandlersConfig{
		Sessions:     sessions,
		Store:        authStore,
		SecondFactor: secondFactor,
		Logger:       rt.Logger,
		FacilityID:   facilityRow.ID,
		// Off only for plain-http local development. A cookie without Secure travels in
		// clear over http, which on a clinic's shared wifi is the whole attack.
		SecureCookies: rt.Config.Env != config.EnvLocal,
		Audit:         bridge,
	})

	// Authorisation (CP19/CP20): the engine, its cache in Redis, and the identity service
	// telling it when a person's roles change.
	resolver := rbac.NewResolver(rbac.ResolverConfig{Grants: authStore, Cache: rt.Cache, Clock: clock.Real{}})
	identity := auth.NewService(authStore).WithInvalidator(resolver)

	// The administrator console (CP21).
	admin := auth.NewAdmin(auth.AdminConfig{
		Store: authStore, Identity: identity, Sessions: sessions, SecondFactor: secondFactor,
		Hasher: pwhash.New(pwhash.DefaultParams()), Clock: clock.Real{}, Audit: bridge,
	})
	adminHandlers := auth.NewAdminHandlers(auth.AdminHandlersConfig{
		Admin: admin, SecondFactor: secondFactor, Logger: rt.Logger,
	})

	// Patients (CP28, CP29). The sealer needs both the key ring — for the reversible half
	// — and the pepper, which is a separate secret because it is not rotatable: the
	// digests it produces are the duplicate-detection index.
	pepper, err := base64.StdEncoding.DecodeString(rt.Config.Secrets.IdentifierPepper)
	if err != nil {
		rt.Logger.Error("DTHCMS_IDENTIFIER_PEPPER is not valid base64", "error", err.Error())
		return 1
	}
	sealer, err := patient.NewIdentifierSealer(pepper, ring)
	if err != nil {
		rt.Logger.Error("cannot build the identifier sealer", "error", err.Error())
		return 1
	}
	// Object storage (CP34). The adapter speaks S3 to MinIO locally and to Google Cloud
	// Storage's interoperability endpoint in production, so moving the identifier class
	// into Bangladesh (D-01) is a bucket in a config file rather than a code change.
	blobs, err := objectStore(rt.Config.Blob)
	if err != nil {
		rt.Logger.Error("cannot build the object store", "error", err.Error())
		return 1
	}

	patientStore := patient.NewStore(rt.DB.Pool)
	// The duplicate matcher (CP30). Its thresholds are proposed values measured on the
	// labelled fixture set; they will be re-tuned against real spellings during the pilot,
	// which is why they are a field and not a constant.
	matcher := patient.NewMatcher(patientStore, sealer)
	// Consent (CP36). Built before the patient handlers because they mount its routes: a
	// consent hangs off a patient, and a patient must not know which modules do.
	//
	// The gate is what everything else in the process asks before acting on a patient, and
	// the service invalidates it on every write — so a revocation takes effect on the next
	// question rather than up to CacheTTL later.
	consentStore := consent.NewStore(rt.DB.Pool)
	consentService := consent.NewService(consentStore, events, clock.Real{})
	consentGate := consent.NewGate(consentStore, time.Now)
	consentService.Watching(consentGate)
	consentHandlers := consent.NewHandlers(consent.HandlersConfig{
		Service: consentService, Store: consentStore, Blobs: blobs,
		Clock: clock.Real{}, Logger: rt.Logger,
	})

	// Visits and encounters (CP38). Built before the patient handlers for the same reason
	// consent is: its per-patient view hangs off a patient, and a patient must not know
	// which modules do.
	visitStore := visit.NewStore(rt.DB.Pool)
	visitService := visit.NewService(visitStore, events, clock.Real{})
	if rt.Cache != nil {
		// The traffic board's feed (CP40). Attached here rather than inside the module
		// because `visit` may not import `realtime`; see board_bridge.go.
		visitService = visitService.Notify(&boardBridge{
			publisher: realtime.NewPublisher(rt.Cache.Client, rt.Logger),
			logger:    rt.Logger,
		})
	}
	visitHandlers := visit.NewHandlers(visit.HandlersConfig{
		Service: visitService,
		Store:   visitStore, Clock: clock.Real{}, Logger: rt.Logger,
	})

	// Observations (CP42). Built before the patient handlers because its per-patient reads
	// hang off a patient through the same `Sub` hook consent and visits use.
	clinicalStoreRead := clinical.NewStore(rt.DB.Pool)
	clinicalService := clinical.NewService(clinicalStoreRead, events, clock.Real{})
	if rt.Cache != nil {
		// Critical values reach the consultant's screen through the realtime gateway
		// (CP50). Attached here for the same reason the board's feed is: `clinical` may not
		// import `realtime`, and the translation between "a value is dangerous" and "a
		// message on somebody's topic" belongs in the one place allowed to know both.
		clinicalService = clinicalService.WithNotifier(&alertBridge{
			publisher: realtime.NewPublisher(rt.Cache.Client, rt.Logger),
			presence:  realtime.NewPresence(rt.Cache.Client),
			logger:    rt.Logger,
		})
		// A flagged value reaching the operator who typed it (CP62 criterion 4). A separate
		// bridge from the alert one because the two say different things to different people:
		// an alert is shouted at whoever can act, and this is one colleague being asked to
		// look at one number again.
		clinicalService = clinicalService.WithCorrectionNotifier(&correctionBridge{
			publisher: realtime.NewPublisher(rt.Cache.Client, rt.Logger),
			logger:    rt.Logger,
		})
		// An ordinary value landing on a screen that is already open (CP73 criterion 4). The
		// third bridge on this service and the only one about a morning that is going well:
		// the two above carry exceptions, and §8's snapshot is the first surface whose whole
		// content is somebody else's routine work.
		clinicalService = clinicalService.WithValueNotifier(&observationBridge{
			publisher: realtime.NewPublisher(rt.Cache.Client, rt.Logger),
			clock:     clock.Real{},
			logger:    rt.Logger,
		})
	}

	// The operator quality record (CP63). Its own module with the shortest import list in the
	// system — it may reach neither `clinical` nor `audit` nor `realtime` — so everything that
	// leaves it goes through `qualityBridge`. ADR-0029 has the argument, including why this is
	// not HR's.
	qualityStore := quality.NewStore(rt.DB.Pool)
	qualityOut := &qualityBridge{recorder: auditRecorder, logger: rt.Logger}
	if rt.Cache != nil {
		qualityOut.publisher = realtime.NewPublisher(rt.Cache.Client, rt.Logger)
	}
	qualityDetector := quality.NewDetector(qualityStore, clock.Real{}, qualityOut, qualityOut)
	qualityHandlers := quality.NewHandlers(quality.HandlersConfig{
		Store: qualityStore, Clock: clock.Real{},
		Audit: qualityOut, Notify: qualityOut, Logger: rt.Logger,
	})
	// A correction that has just been answered is the moment a pattern becomes visible. Attached
	// after the commit and out of the way of it — see `Service.reviewQuality`.
	clinicalService = clinicalService.WithQualityReviewer(&qualityReviewBridge{
		detector: qualityDetector, logger: rt.Logger,
	})

	clinicalHandlers := clinical.NewHandlers(clinical.HandlersConfig{
		Service: clinicalService,
		Store:   clinicalStoreRead, Clock: clock.Real{}, Logger: rt.Logger,
	})

	// Station 3's questionnaires and the composite lifestyle score (CP58). Its own module because
	// an instrument is not an observation — ADR-0030 — and it writes the composite through
	// `clinical`, which is the one code path that knows how to write a derived value.
	assessmentStore := assessment.NewStore(rt.DB.Pool)
	assessmentService := assessment.NewService(assessmentStore, events, clock.Real{}).
		WithDeriver(&assessmentDeriver{clinical: clinicalService})
	assessmentHandlers := assessment.NewHandlers(assessment.HandlersConfig{
		Service: assessmentService,
		Store:   assessmentStore, Clock: clock.Real{}, Logger: rt.Logger,
	})

	// Station 7's 24-hour recall (CP59). Its own module, and no session to fight over: a recall
	// is a set of entries, so two assistants entering one from two devices never write the same
	// row and there is nothing to reconcile.
	nutritionStore := nutrition.NewStore(rt.DB.Pool)
	nutritionHandlers := nutrition.NewHandlers(nutrition.HandlersConfig{
		Service: nutrition.NewService(nutritionStore, events, clock.Real{}).
			WithDeriver(&nutritionDeriver{clinical: clinicalService}),
		Store: nutritionStore, Clock: clock.Real{}, Logger: rt.Logger,
	})

	// Station 8's exercise assessment and plan (CP60). Its own module, and the reason is
	// criterion 1: the permitted-exercise filter lives in `core.exercises_permitted` and this is
	// the only surface that reads it, so there is no code path anywhere that returns the whole
	// library for a named patient.
	exerciseStore := exercise.NewStore(rt.DB.Pool)
	exerciseHandlers := exercise.NewHandlers(exercise.HandlersConfig{
		Service: exercise.NewService(exerciseStore, events, clock.Real{}),
		Store:   exerciseStore, Clock: clock.Real{}, Logger: rt.Logger,
	})

	// The offline sync protocol's server side (CP65). It appends through the same ledger every
	// other write uses — a late-arriving blood pressure is an ordinary observation that happened
	// at 08:40 and was heard about at 11:00, not a second kind of record.
	offlineStore := offline.NewStore(rt.DB.Pool)
	offlineHandlers := offline.NewHandlers(offline.HandlersConfig{
		Service: offline.NewService(offlineStore, events, clock.Real{}),
		Store:   offlineStore, Clock: clock.Real{}, Logger: rt.Logger,
	})

	// The per-device rate limiter (CP65's security line). Redis-backed so that every API
	// instance shares one budget per device rather than handing a client as many budgets as
	// there are instances. Nil when there is no cache, which leaves every route unlimited —
	// see httpx.RateLimit on failing open, and migration 00050 on what is bounded in the
	// database whether or not Redis is up.
	var limiter httpx.Limiter
	if rt.Cache != nil {
		limiter = cache.NewLimiter(rt.Cache.Client, "ratelimit:")
	} else {
		rt.Logger.Warn("no cache configured; per-device rate limits are not being applied")
	}

	// The background work queue (CP69). Read-only here: nothing outside the process that decided
	// the work was needed may enqueue, because acceptance criterion 1 is that the insert happens
	// in that decision's own transaction, and an HTTP endpoint accepting a kind and a payload
	// would be the way around it somebody eventually used.
	jobStore := jobs.NewStore(rt.DB.Pool)
	jobHandlers := jobs.NewHandlers(jobs.HandlersConfig{
		Store: jobStore, Clock: clock.Real{}, Logger: rt.Logger,
	})

	// The AI gateway's operational surface (CP70). Read-only, and there is deliberately no invoke
	// route: an agent decides a call is warranted from inside the module that owns the data and
	// calls ai.Gateway.Invoke directly. A route taking an agent code and a payload would put the
	// caller in charge of deciding what counts as an identifier, which is the one thing the whole
	// module exists to take away from them.
	//
	// The prompt registry is loaded and deployed here rather than in the worker, because the API is
	// what serves the registry screen and because a build whose prompts do not load should refuse
	// to start rather than discover it at the first synthesis. `Deploy` is the check that a prompt
	// has not been edited under an unchanged version number — the one change that would make every
	// stored interaction unreproducible.
	aiStore := ai.NewStore(rt.DB.Pool)
	promptRegistry, err := ai.LoadRegistry()
	if err != nil {
		rt.Logger.Error("refusing to start: the prompt registry does not load", "error", err.Error())
		return 1
	}
	if err := promptRegistry.Deploy(ctx, aiStore, clock.Real{}.Now()); err != nil {
		rt.Logger.Error("refusing to start: the prompt registry disagrees with the database",
			"error", err.Error())
		return 1
	}
	aiHandlers := ai.NewHandlers(ai.HandlersConfig{
		Store: aiStore, Clock: clock.Real{}, Logger: rt.Logger,
		// One facility today (D-61). Resolved from the row this process already looked up at
		// start-up rather than from the request, so that the day multi-tenancy is answered there is
		// exactly one function to change.
		Facility: func(*http.Request) uuid.UUID { return facilityRow.ID },
	})

	// The coded catalogue (CP52). No service and no events: a code set is loaded by
	// migration and a clinic does not edit the WHO's classification.
	terminologyHandlers := terminology.NewHandlers(terminology.HandlersConfig{
		Store: terminology.NewStore(rt.DB.Pool), Logger: rt.Logger,
	})

	// Medical history (CP53). Its own module rather than a corner of clinical, because a
	// history item has an identity that outlives the visit and an observation does not —
	// ADR-0028 has the argument.
	historyStore := history.NewStore(rt.DB.Pool)
	historyHandlers := history.NewHandlers(history.HandlersConfig{
		Service: history.NewService(historyStore, events, clock.Real{}),
		Store:   historyStore, Logger: rt.Logger,
	})

	// The allergy hard stop (CP54). The gate itself is a trigger on the queue — criterion 4
	// says no client may bypass it, and a check here would hold only for clients that come
	// through here. What this serves is the five-second act that satisfies it.
	allergyStore := allergy.NewStore(rt.DB.Pool)
	allergyHandlers := allergy.NewHandlers(allergy.HandlersConfig{
		Service: allergy.NewService(allergyStore, events, clock.Real{}),
		Store:   allergyStore, Clock: clock.Real{}, Logger: rt.Logger,
	})

	// Counselling templates (CP55). Authored by a physician rather than by a release; a
	// published version is frozen because a completed session references it.
	counselingStore := counseling.NewStore(rt.DB.Pool)

	// Ticking, on a phone, on the floor (CP56). Progress reaches the traffic board through
	// the realtime gateway, and through a bridge for the reason every bridge here exists:
	// `counseling` may not import `realtime`, and a module that could publish its own
	// messages would grow a second answer to "what does progress mean".
	counselingSessions := counseling.NewSessionService(counselingStore, events, clock.Real{})
	if rt.Cache != nil {
		counselingSessions = counselingSessions.WithNotifier(&counselingProgressBridge{
			publisher: realtime.NewPublisher(rt.Cache.Client, rt.Logger),
			logger:    rt.Logger,
		})
	}

	// The counselling checkpoint, as the queue sees it (CP57). The enforcement is a trigger on
	// `core.queue_entry`; this is what turns "blocked" into a sentence naming the items, which
	// is criterion 2 — and it is a bridge because `visit` may not import `counseling`.
	visitService = visitService.WithGate(&counselingGateBridge{store: counselingStore})

	// The pre-consultation synthesis (CP71). Wired last of the visit's attachments because it
	// needs every station's store, and attached to `visitService` so that a station touch ending
	// asks for a summary inside that touch's own transaction — which is acceptance criterion 2:
	// the automatic trigger is the primary path and the button is the fallback.
	//
	// **This process builds the service without a gateway, deliberately.** The API never contacts
	// a model: that separation is why `cmd/worker` exists at all, so that a burst of AI work can
	// never slow down a clinician entering a blood pressure. What the API does here is decide that
	// a summary is warranted, assemble the context, and queue the run; `Service.Perform` refuses to
	// run in a process with no gateway rather than acquiring one by accident.
	synthesisStore := synthesis.NewStore(rt.DB.Pool)
	synthesisService := synthesis.NewService(synthesis.ServiceConfig{
		Store: synthesisStore,
		Stations: synthesis.Stations{
			Visits: visitStore, Clinical: clinicalStoreRead, Growth: clinicalService,
			History: historyStore, Allergies: allergyStore,
			Lifestyle: assessmentService, Nutrition: nutritionStore, Exercise: exerciseStore,
		},
		Patients: patientStore,
		Queue:    synthesisQueue{store: jobStore},
		Events:   events, Clock: clock.Real{}, Logger: rt.Logger,
	})
	visitService = visitService.
		OnStationFinished(synthesisHook{service: synthesisService}).
		WithLogger(rt.Logger)
	synthesisHandlers := synthesis.NewHandlers(synthesis.HandlersConfig{
		Service: synthesisService, Store: synthesisStore, Clock: clock.Real{},
		Logger: rt.Logger, Budget: synthesisBudget(ctx, jobStore, rt.Logger),
		// One facility today (D-61), resolved from the row this process already looked up rather
		// than from the request — the same single point of change as the gateway's.
		Facility: func(*http.Request) uuid.UUID { return facilityRow.ID },
	})

	visitHandlers = visit.NewHandlers(visit.HandlersConfig{
		Service: visitService,
		Store:   visitStore, Clock: clock.Real{}, Logger: rt.Logger,
	})

	counselingHandlers := counseling.NewHandlers(counseling.HandlersConfig{
		Store:    counselingStore,
		Service:  counseling.NewService(counselingStore),
		Sessions: counselingSessions,
		// Which checklist a patient needs is decided by their coded conditions, which live in
		// `history` — a module `counseling` may not import. Both lookups arrive as interfaces
		// implemented in this binary; see counseling_lookup_bridge.go.
		Visits:     &visitPatients{store: visitStore},
		Conditions: &historyConditions{store: historyStore},
		Audit:      &counselingAuditBridge{recorder: auditRecorder},
		StepUp:     &auth.StepUpAdapter{SecondFactor: secondFactor},
		Logger:     rt.Logger,
	})

	// The break-glass door (CP22), built here rather than beside the rest of the audit
	// handlers because two things need it now: the console that opens one, and CP73's
	// dashboard, which tells the physician *while they are reading* that this is a record they
	// opened in an emergency. An access somebody has forgotten is open is an access that has
	// stopped being an emergency.
	breakGlass := audit.NewBreakGlass(auditStore, auditRecorder, clock.Real{}, authStore)

	// The physician's dashboard (CP73, §8): the three panels of §8 in one request.
	//
	// It is assembled here and nowhere else because it is the one module that composes six
	// others — every store below already exists, and the dashboard is a fan-out over them
	// rather than a seventh copy of their data.
	dashboardHandlers := dashboard.NewHandlers(dashboard.HandlersConfig{
		Service: dashboard.NewService(dashboard.Config{
			Patients: patientStore, Visits: visitStore,
			Clinical: clinicalService, Values: clinicalStoreRead,
			History: historyStore, Allergies: allergyStore, Counseling: counselingStore,
			Synthesis: synthesisService, Events: events,
			Emergency: &breakGlassBridge{service: breakGlass, clock: clock.Real{}},
			Clock:     clock.Real{},
		}),
		Logger: rt.Logger,
		Audit:  &dashboardAuditBridge{recorder: auditRecorder},
	})

	patientHandlers := patient.NewHandlers(patient.HandlersConfig{
		Service: patient.NewService(patient.ServiceConfig{
			Store: patientStore, Events: events, Sealer: sealer, Clock: clock.Real{},
			Duplicates: matcher.AsCheck(),
		}),
		Store: patientStore, Matcher: matcher,
		Photos: patient.NewPhotoService(patientStore, events, blobs, clock.Real{}),
		StepUp: &auth.StepUpAdapter{SecondFactor: secondFactor},
		Audit:  bridge,
		Sub: []func(chi.Router){
			consentHandlers.Mount, visitHandlers.MountPatient, clinicalHandlers.MountPatient,
			clinicalHandlers.MountPatientAlerts, clinicalHandlers.MountPatientCorrections,
			historyHandlers.MountPatient, allergyHandlers.MountPatient,
			assessmentHandlers.MountPatient, nutritionHandlers.MountPatient,
			exerciseHandlers.MountPatient, dashboardHandlers.MountPatient,
		},
		Clock: clock.Real{}, Logger: rt.Logger,
	})

	// The audit viewer and the exporter (CP22). `breakGlass` is built above, before the patient
	// handlers, because CP73's dashboard needs to be able to tell a physician that they are
	// reading a record through the emergency door.
	auditHandlers := audit.NewHandlers(audit.HandlersConfig{
		Recorder: auditRecorder, Store: auditStore, BreakGlass: breakGlass, Signer: auditSigner,
		FacilityName: func(uuid.UUID) string { return facilityRow.NameEn },
		StepUp:       &auth.StepUpAdapter{SecondFactor: secondFactor},
		Clock:        clock.Real{}, Logger: rt.Logger,
	})

	router, err := surface{
		Logger:         rt.Logger,
		IDs:            rt.IDs,
		AllowedOrigins: rt.Config.HTTP.AllowedOrigins,
		MaxBodyBytes:   rt.Config.HTTP.MaxBodyBytes,
		RequestTimeout: rt.Config.HTTP.WriteTimeout,
		Health:         health,

		Instrumentation: instrumentation,
		Identifier:      &auth.Identifier{Sessions: sessions, Store: authStore},
		Auth:            authHandlers,
		DeviceVerifier:  &auth.DeviceVerifierAdapter{Devices: devices},
		Devices:         deviceHandlers,
		Admin:           adminHandlers,
		Audit:           auditHandlers,
		Patients:        patientHandlers,
		Consent:         consentHandlers,
		Terminology:     terminologyHandlers,
		History:         historyHandlers,
		Allergies:       allergyHandlers,
		Counseling:      counselingHandlers,
		Quality:         qualityHandlers,
		Assessments:     assessmentHandlers,
		Nutrition:       nutritionHandlers,
		Exercise:        exerciseHandlers,
		Jobs:            jobHandlers,
		AI:              aiHandlers,
		Synthesis:       synthesisHandlers,
		Offline:         offlineHandlers,
		Directory: auth.NewDirectoryHandlers(auth.DirectoryHandlersConfig{
			Store: authStore, Clock: clock.Real{}, Logger: rt.Logger,
		}),
		Visits:   visitHandlers,
		Clinical: clinicalHandlers,
		// The one route a correctly-signed but no-longer-active device may reach (CP65). Without
		// it the quarantine could never fire: a tablet revoked while it was offline met a 401
		// indistinguishable from an expired token, and a client following §13.8's "wipe on
		// revocation" then destroyed the forty measurements the quarantine exists to preserve.
		//
		// It relaxes nothing about authentication — the signature, the timestamp and the nonce are
		// checked exactly as everywhere else. What it permits is a revoked device handing over
		// what it already has, to be judged by a person rather than accepted or lost.
		QuarantineRoutes: map[string]bool{"POST /v1/sync/events": true},
		RateLimits:       syncRateLimits(),
		Limiter:          limiter,
		Authorizer:       &rbac.HTTPAuthorizer{Resolver: resolver},
		Idempotency:      idempotency.New(rt.DB.Pool),
	}.router()
	if err != nil {
		rt.Logger.Error("refusing to start: the route table is not fully declared", "error", err.Error())
		return 1
	}

	err = httpx.Serve(ctx, httpx.ServerOptions{
		Addr:            rt.Config.HTTP.Addr,
		Handler:         router,
		Logger:          rt.Logger,
		ReadTimeout:     rt.Config.HTTP.ReadTimeout,
		WriteTimeout:    rt.Config.HTTP.WriteTimeout,
		IdleTimeout:     rt.Config.HTTP.IdleTimeout,
		ShutdownTimeout: rt.Config.HTTP.ShutdownTimeout,
	})
	if err != nil {
		rt.Logger.Error("server stopped with an error", "error", err.Error())
		return 1
	}
	return 0
}

// surface is the HTTP surface of the api binary: which endpoints exist and what runs in
// front of them.
//
// It is a type rather than a dozen lines inside run because the contract test has to walk
// the routes this binary serves, and run cannot be called without a database. Assembling
// the surface here means the test and the process are looking at the same route table
// rather than at two lists that are meant to match.
type surface struct {
	Logger         *slog.Logger
	IDs            ids.Generator
	AllowedOrigins []string
	MaxBodyBytes   int64
	RequestTimeout time.Duration
	Health         *httpx.Health

	Instrumentation *httpx.Instrumentation

	// Identifier resolves the access token on every authenticated request; Auth serves
	// the endpoints that issue one, which therefore sit outside that chain.
	Identifier *auth.Identifier
	Auth       *auth.Handlers

	// DeviceVerifier checks device signatures; Devices serves enrolment (unauthenticated
	// corner) and administration (inside the chain).
	DeviceVerifier httpx.DeviceVerifier
	Devices        *auth.DeviceHandlers

	// Admin serves the console (CP21).
	Admin *auth.AdminHandlers

	// Audit serves the trail, the export and the break-glass door (CP22).
	Audit *audit.Handlers

	// Patients serves registration and retrieval (CP29).
	Patients *patient.Handlers
	// Consent hangs its per-patient routes off Patients (see HandlersConfig.Sub) and mounts
	// the template endpoint itself (CP36).
	Consent *consent.Handlers
	// Visits mounts /v1/visits itself and hangs the per-patient list off Patients (CP38).
	Visits *visit.Handlers
	// Clinical mounts /v1/observations and hangs its per-patient reads off Patients (CP42).
	Clinical *clinical.Handlers
	// Terminology serves the coded catalogue: ICD and the clinic's own complaint dictionary
	// (CP52). No patient in it, so it hangs off nothing.
	Terminology *terminology.Handlers
	// History mounts /v1/history and hangs the per-patient list and write off Patients
	// (CP53).
	History *history.Handlers
	// Allergies mounts /v1/allergies and hangs the per-patient state, write and assertion off
	// Patients (CP54). The gate it exists to satisfy is in the database, not here.
	Allergies *allergy.Handlers
	// Counseling mounts /v1/counseling: the checklists a physician authors (CP55).
	Counseling *counseling.Handlers
	// Quality mounts /v1/quality: an operator's own correction record, and the supervisor's
	// view of the patterns across a team (CP63).
	Quality *quality.Handlers
	// Assessments mounts /v1/assessments: station 3's questionnaires (CP58).
	Assessments *assessment.Handlers
	// Nutrition mounts /v1/foods and /v1/diet: station 7's 24-hour recall (CP59).
	Nutrition *nutrition.Handlers
	// Exercise mounts /v1/exercise: station 8's assessment and its contraindication-filtered
	// plan (CP60). There is deliberately no route that returns the library unfiltered.
	Exercise *exercise.Handlers
	// Jobs mounts /v1/ops/jobs: the background work queue's health, its dead letters and the two
	// controls an incident needs (CP69). There is deliberately no enqueue route.
	Jobs *jobs.Handlers
	// AI mounts /v1/ops/ai: what the system sent to a model, what it cost against the budget, and
	// the prompt registry as deployed (CP70). There is deliberately no invoke route.
	AI *ai.Handlers
	// Synthesis hangs the pre-consultation summary off a visit and its SLA report off /v1/ops
	// (CP71). Two mounts rather than one because the two have different readers: the summary is
	// the physician's, and the SLA measurement is the floor supervisor's.
	Synthesis *synthesis.Handlers
	// Offline mounts /v1/sync: batched pushes from a device that was out of signal, the
	// incremental pull, and the quarantine a revoked device's events wait in (CP65).
	Offline *offline.Handlers
	// Directory serves /v1/directory: the names behind the ids every clinical value carries
	// (CP61). A session and nothing more — every role that may see a value may see who
	// entered it, and there is no patient in the response.
	Directory *auth.DirectoryHandlers

	// Authorizer decides every permission-guarded route (CP20).
	Authorizer httpx.Authorizer

	// QuarantineRoutes are the exact "METHOD /path" pairs a correctly-signed but no-longer-active
	// device may reach (CP65). Exactly one: the sync push, which holds what it receives for a
	// person to judge rather than acting on it.
	QuarantineRoutes map[string]bool

	// Limiter counts requests against RateLimits (CP65). Redis-backed, so every API instance
	// shares one budget per device.
	Limiter httpx.Limiter

	// RateLimits are the per-route, per-device budgets. See syncRateLimits.
	RateLimits map[string]httpx.Rule

	// Idempotency answers a retried mutating request from the store instead of running
	// the handler twice (CP24).
	Idempotency httpx.IdempotencyStore
}

func (s surface) router() (*chi.Mux, error) {
	opts := httpx.RouterOptions{
		Logger:          s.Logger,
		IDs:             s.IDs,
		AllowedOrigins:  s.AllowedOrigins,
		MaxBodyBytes:    s.MaxBodyBytes,
		RequestTimeout:  s.RequestTimeout,
		Health:          s.Health,
		Instrumentation: s.Instrumentation,
	}
	if s.Identifier != nil {
		opts.Authenticator = s.Identifier
	}
	if s.DeviceVerifier != nil {
		opts.DeviceVerifier = s.DeviceVerifier
	}
	if s.Auth != nil {
		opts.AuthRoutes = func(r chi.Router) {
			s.Auth.Mount(r)
			if s.Devices != nil {
				s.Devices.MountAuth(r)
			}
		}
	}
	opts.Routes = func(r chi.Router) {
		if s.Devices != nil {
			s.Devices.Mount(r)
		}
		if s.Admin != nil {
			s.Admin.Mount(r)
		}
		if s.Audit != nil {
			s.Audit.Mount(r)
		}
		if s.Patients != nil {
			s.Patients.Mount(r)
		}
		if s.Consent != nil {
			s.Consent.MountTemplates(r)
		}
		if s.Visits != nil {
			s.Visits.Mount(r)
			s.Visits.MountStations(r)
			s.Visits.MountBoard(r)
		}
		if s.Clinical != nil {
			s.Clinical.Mount(r)
			// The correction workflow (CP62). Its own surface rather than a branch of
			// /v1/observations, because a correction request outlives the value it is about:
			// an operator's queue is a list of things to answer, not a list of measurements.
			s.Clinical.MountCorrections(r)
			// Critical values (CP50). Its own top-level surface rather than a branch of
			// /v1/observations, because an alert outlives the value that raised it: the
			// consultant's board is a list of things that need answering, not a list of
			// measurements.
			s.Clinical.MountAlerts(r)
		}
		if s.Terminology != nil {
			s.Terminology.Mount(r)
		}
		if s.History != nil {
			s.History.Mount(r)
		}
		if s.Allergies != nil {
			s.Allergies.Mount(r)
		}
		if s.Quality != nil {
			s.Quality.Mount(r)
		}
		if s.Assessments != nil {
			s.Assessments.Mount(r)
		}
		if s.Nutrition != nil {
			s.Nutrition.Mount(r)
		}
		if s.Exercise != nil {
			s.Exercise.Mount(r)
		}
		if s.Jobs != nil {
			s.Jobs.Mount(r)
		}
		if s.AI != nil {
			s.AI.Mount(r)
		}
		if s.Synthesis != nil {
			s.Synthesis.MountVisit(r)
			s.Synthesis.MountOps(r)
		}
		if s.Offline != nil {
			s.Offline.Mount(r)
		}
		if s.Counseling != nil {
			s.Counseling.Mount(r)
		}
		if s.Directory != nil {
			s.Directory.Mount(r)
		}
	}
	opts.QuarantineRoutes = s.QuarantineRoutes
	opts.Limiter = s.Limiter
	opts.RateLimits = s.RateLimits
	if s.Authorizer != nil {
		opts.Authorizer = s.Authorizer
	}
	if s.Idempotency != nil {
		opts.Idempotency = s.Idempotency
	}
	return httpx.NewRouter(opts)
}

// clinicalStore is the ledger the API writes through: the append path of CP23 with the
// synchronous projections of CP25 inside its transaction.
//
// Asynchronous projections are deliberately absent. They run in cmd/projector, as
// dthcms_projector — the only role permitted to write read models — and their failure must
// never be able to fail an append (CP25 criterion 4).
func clinicalStore(pool *pgxpool.Pool) *eventstore.Store {
	return eventstore.New(eventstore.Config{
		Pool:        pool,
		Clock:       clock.Real{},
		Synchronous: projection.NewSyncSet(projection.Default),
	})
}

// clinicalHead is how many events the ledger holds, reported once at start. It also
// exercises the assembly: a store that cannot be built is a start-up failure rather than a
// surprise on the first clinical write.
func clinicalHead(ctx context.Context, pool *pgxpool.Pool) int64 {
	n, err := clinicalStore(pool).Count(ctx)
	if err != nil {
		return -1
	}
	return n
}

// synchronousNames is what the start-up line reports, so that a deployment's log says
// which read models are being maintained inside the write path.
func synchronousNames() []string {
	var names []string
	for _, p := range projection.Default.InMode(projection.Synchronous) {
		names = append(names, p.Name())
	}
	return names
}

// objectStore builds the blob adapter, or the placeholder that fails loudly.
//
// Unconfigured rather than an error when there are no credentials: a developer running the
// API to look at the registration form should not need MinIO up, and the photograph
// endpoints then answer 503 — which is honest — rather than appearing to work.
func objectStore(cfg config.BlobConfig) (blobstore.Store, error) {
	if cfg.Endpoint == "" || cfg.AccessKey == "" {
		return blobstore.Unconfigured{}, nil
	}
	buckets := map[blobstore.Class]string{}
	for name, bucket := range cfg.Buckets {
		class := blobstore.Class(name)
		if !class.Valid() {
			return nil, fmt.Errorf("blob: %q is not a data class", name)
		}
		buckets[class] = bucket
	}
	scheme := "http://"
	if cfg.UseSSL {
		scheme = "https://"
	}
	endpoint := cfg.Endpoint
	if !strings.HasPrefix(endpoint, "http") {
		endpoint = scheme + endpoint
	}
	return blobstore.NewS3(blobstore.S3Config{
		Endpoint: endpoint, Region: cfg.Region,
		AccessKey: cfg.AccessKey, SecretKey: cfg.SecretKey,
		Buckets: buckets,
		// MinIO addresses buckets by path; a cloud endpoint usually does not. Decided by
		// the scheme rather than configured, because getting it wrong produces a DNS
		// failure that looks like an outage.
		PathStyle: !cfg.UseSSL,
		Clock:     clock.Real{},
	})
}

// auditSignerFrom parses the export signing seed. Config has already refused the local
// seed outside local and test.
func auditSignerFrom(cfg config.AuditConfig) (*audit.Signer, error) {
	seed, err := base64.StdEncoding.DecodeString(cfg.SigningSeed)
	if err != nil {
		return nil, fmt.Errorf("DTHCMS_AUDIT_SIGNING_SEED is not base64: %w", err)
	}
	return audit.NewSigner(cfg.SigningKeyID, seed)
}

// secretRing builds the key ring from configuration: the current key first, then any
// previous keys still needed to open what they sealed.
func secretRing(cfg config.SecretsConfig) (*secretbox.Ring, error) {
	current, err := secretbox.ParseKey(cfg.KeyID, cfg.Key)
	if err != nil {
		return nil, err
	}
	keys := []secretbox.Key{current}
	for _, pair := range cfg.PreviousKeys {
		id, material, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("DTHCMS_SECRET_PREVIOUS_KEYS entry %q is not id=base64", pair)
		}
		key, err := secretbox.ParseKey(id, material)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return secretbox.NewRing(keys...)
}

// syncRateLimits is CP65's "per-device rate limits", and it is two routes rather than the whole
// API on purpose.
//
// D-49 wants per-user, per-device and per-endpoint-class limits everywhere. That is a hardening
// checkpoint of its own, with numbers taken from a real morning's traffic; switching a limiter on
// across sixty routes with numbers nobody has measured is how a clinic discovers rate limiting
// during a busy clinic. These two are the ones where the absence of a limit had a consequence, so
// they are the two that get one now.
//
// # The push is the tight one
//
// Since CP65 a device the clinic has **revoked** may still reach `POST /v1/sync/events`, because
// the alternative was destroying the measurements it is holding. What it writes there lands in
// `ops.sync_quarantine`, from which `dthcms_app` has no DELETE — every row is permanent, and every
// row is one a supervisor has to look at. An honest client stops when its backlog is delivered; a
// stolen tablet with a live key has no reason to.
//
// Twelve at once, then twelve a minute. A tablet coming back into signal after a day out sends
// four or five batches back to back and must not be slowed down for it, which is what the burst is
// for. Sustained, twelve batches a minute is six thousand events a minute — far more than a
// station generates and far less than a loop can.
//
// # The pull is the loose one
//
// A device seeding itself walks the ledger a page at a time, and a new tablet has a lot of pages
// to walk. The threat here is also different in kind: the pull is permission-scoped and returns
// only what this device may already see, so the harm from a fast one is bandwidth, not a permanent
// row somebody has to triage. Two a second sustained, and two minutes' worth in hand.
//
// Both are per device, not per person: the tablet is the unit of abuse, and keying on the person
// would let one tablet spend a fresh budget for every clinician who has ever signed in on it.
func syncRateLimits() map[string]httpx.Rule {
	return map[string]httpx.Rule{
		"POST /v1/sync/events": {Burst: 12, Every: 5 * time.Second},
		"GET /v1/sync/events":  {Burst: 240, Every: 500 * time.Millisecond},
	}
}
