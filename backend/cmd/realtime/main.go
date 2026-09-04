// Command realtime is the WebSocket gateway (CP26): the process that keeps clinic screens
// up to date without a refresh (§4.1).
//
// It is a separate binary from the API for one reason that matters: a WebSocket connection
// lives for hours, and a process holding thousands of them has a memory and file-descriptor
// profile nothing like a request/response server's. Deploying them together would mean
// every API restart drops every screen's connection, and every connection leak becomes an
// API outage.
//
// It shares the API's authentication, its device verification and its RBAC engine — the
// same code, not a second implementation — and reaches the rest of the system only through
// Redis. It never reads the ledger: what it relays is what a publisher hands it after a
// commit.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/version"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
	"github.com/AmlanWTK/DTHCMS/backend/internal/realtime"
)

func main() { os.Exit(run()) }

func run() int {
	ctx := context.Background()

	rt, err := platform.Boot(ctx, platform.Options{
		Service:    "realtime",
		NeedsDB:    true, // sessions, devices and role grants are resolved on every connect
		NeedsCache: true, // the pub/sub bridge
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "realtime: cannot start: %v\n", err)
		return 1
	}
	defer rt.Close()

	authStore := auth.NewPostgresStore(rt.DB.Pool)
	// No second factor and no password hasher: this process never signs anybody in. It
	// resolves a token that already exists, which is all a connection needs.
	sessions := auth.NewSessions(auth.SessionsConfig{Store: authStore, Clock: clock.Real{}})
	devices := auth.NewDevices(auth.DevicesConfig{Store: authStore, Nonces: rt.Cache, Clock: clock.Real{}})
	resolver := rbac.NewResolver(rbac.ResolverConfig{Grants: authStore, Cache: rt.Cache, Clock: clock.Real{}})

	metrics, err := realtime.NewMetrics(rt.Telemetry)
	if err != nil {
		rt.Logger.Error("cannot publish realtime metrics", "error", err.Error())
		return 1
	}
	defer func() { _ = metrics.Close() }()

	hub := realtime.NewHub(realtime.HubConfig{
		Filter: realtime.RBACFilter{}, Clock: clock.Real{}, Logger: rt.Logger, Metrics: metrics,
	})
	handler := realtime.NewHandler(realtime.HandlerConfig{
		Hub:            hub,
		Resolver:       &rbac.SubjectResolver{Resolver: resolver},
		Clock:          clock.Real{},
		Logger:         rt.Logger,
		AllowedOrigins: rt.Config.HTTP.AllowedOrigins,
		// Who has a live screen that could receive a critical value (CP50). The API reads
		// this set to decide whether an operator must be told to escalate in person, so it
		// is written here, where the connections actually are.
		Presence: realtime.NewPresence(rt.Cache.Client),
	})

	build := version.Current()
	health := &httpx.Health{
		Service: "realtime", Version: build.Version, Commit: build.Commit, BuildTime: build.BuildTime,
		Logger: rt.Logger, Timeout: 3 * time.Second,
		Dependencies: []httpx.Dependency{
			{Name: "postgres", Check: rt.DB.Check, Critical: true},
			// Redis is critical here in a way it is not for the API: without the bridge
			// this instance's sockets see only what this instance published, which is
			// worse than being unavailable because it is intermittently right.
			{Name: "redis", Check: rt.Cache.Check, Critical: true},
		},
	}

	router := gatewayRouter(gateway{
		Logger:         rt.Logger,
		IDs:            rt.IDs,
		Health:         health,
		Identifier:     &auth.Identifier{Sessions: sessions, Store: authStore},
		DeviceVerifier: &auth.DeviceVerifierAdapter{Devices: devices},
		AllowedOrigins: rt.Config.HTTP.AllowedOrigins,
		Realtime:       handler,
	})

	// The bridge runs beside the server: without it, this instance's sockets would only
	// ever see what this instance published.
	bridgeCtx, stopBridge := context.WithCancel(ctx)
	defer stopBridge()
	go func() {
		if err := realtime.NewBridge(rt.Cache.Client, hub, rt.Logger).Run(bridgeCtx); err != nil {
			rt.Logger.Error("realtime bridge stopped", "error", err.Error())
		}
	}()

	rt.Logger.Info("realtime gateway listening", "addr", rt.Config.HTTP.Addr, "path", "/v1/realtime")

	err = httpx.Serve(ctx, httpx.ServerOptions{
		Addr:    rt.Config.HTTP.Addr,
		Handler: router,
		Logger:  rt.Logger,
		// No write timeout: a WebSocket connection is long-lived by definition, and a
		// server-wide write deadline would cut every screen off at the same moment. The
		// per-write deadline inside the protocol is what bounds a slow socket.
		ReadTimeout:     0,
		WriteTimeout:    0,
		IdleTimeout:     0,
		ShutdownTimeout: rt.Config.HTTP.ShutdownTimeout,
	})
	stopBridge()
	hub.CloseAll(ctx, "shutting_down")

	if err != nil && !errors.Is(err, context.Canceled) {
		rt.Logger.Error("realtime gateway stopped with an error", "error", err.Error())
		return 1
	}
	return 0
}

// gateway is the surface this binary serves, as a type so the conformance test can build it
// without a database.
type gateway struct {
	Logger         *slog.Logger
	IDs            ids.Generator
	Health         *httpx.Health
	Identifier     httpx.Authenticator
	DeviceVerifier httpx.DeviceVerifier
	AllowedOrigins []string
	Realtime       http.Handler
}

// gatewayRouter builds the chain.
//
// It is not httpx.NewRouter, and the difference is the point: that chain wraps every
// request in a timeout and a body limit, both of which are exactly wrong for a connection
// that is meant to last for hours and to carry no request body at all. What is kept is
// everything that makes a request traceable and safe — panic recovery, a request id, the
// access log, the security headers, the origin allowlist — plus the same authentication and
// device verification every other endpoint uses.
func gatewayRouter(g gateway) *chi.Mux {
	r := chi.NewRouter()
	r.Use(httpx.Recover(g.Logger))
	r.Use(httpx.RequestID(g.IDs))
	r.Use(httpx.AccessLog(g.Logger))
	r.Use(httpx.SecurityHeaders)
	r.Use(httpx.CORS(g.AllowedOrigins))

	if g.Health != nil {
		r.Method(http.MethodGet, "/healthz", http.HandlerFunc(g.Health.Live))
		r.Method(http.MethodGet, "/readyz", http.HandlerFunc(g.Health.Ready))
		r.Method(http.MethodGet, "/version", http.HandlerFunc(g.Health.BuildInfo))
	}

	r.Route("/v1", func(v1 chi.Router) {
		v1.Use(httpx.Authenticate(g.Logger, g.Identifier))
		v1.Use(httpx.VerifyDevice(g.Logger, g.DeviceVerifier))
		if g.Realtime != nil {
			v1.Method(http.MethodGet, "/realtime", g.Realtime)
		}
	})
	return r
}
