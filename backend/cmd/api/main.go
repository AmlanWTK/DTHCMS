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
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/allergy"
	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/auth/pwhash"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/consent"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/history"
	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/blobstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/config"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/idempotency"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/secretbox"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/version"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
	"github.com/AmlanWTK/DTHCMS/backend/internal/realtime"
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
	}
	clinicalHandlers := clinical.NewHandlers(clinical.HandlersConfig{
		Service: clinicalService,
		Store:   clinicalStoreRead, Clock: clock.Real{}, Logger: rt.Logger,
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
			clinicalHandlers.MountPatientAlerts, historyHandlers.MountPatient,
			allergyHandlers.MountPatient,
		},
		Clock: clock.Real{}, Logger: rt.Logger,
	})

	// The audit viewer, the exporter and the break-glass door (CP22).
	breakGlass := audit.NewBreakGlass(auditStore, auditRecorder, clock.Real{}, authStore)
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
		Visits:          visitHandlers,
		Clinical:        clinicalHandlers,
		Authorizer:      &rbac.HTTPAuthorizer{Resolver: resolver},
		Idempotency:     idempotency.New(rt.DB.Pool),
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

	// Authorizer decides every permission-guarded route (CP20).
	Authorizer httpx.Authorizer

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
	}
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
