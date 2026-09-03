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

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/auth/pwhash"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform"
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
	// No route appends yet — the first is patient registration at CP29 — but the store is
	// assembled here so that adding one is a handler and not an assembly problem, and so
	// that nobody has to remember a synchronous projection needs attaching at all.
	// TestTheClinicalStoreCarriesItsSynchronousProjections holds that together.
	rt.Logger.Info("clinical write path ready",
		"synchronous_projections", strings.Join(synchronousNames(), ", "),
		"note", "no route appends yet; the first is patient registration at CP29",
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
