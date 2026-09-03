package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
)

// RouterOptions configures the base router.
type RouterOptions struct {
	Logger         *slog.Logger
	IDs            ids.Generator
	AllowedOrigins []string
	MaxBodyBytes   int64
	RequestTimeout time.Duration
	Health         *Health

	// Instrumentation traces and measures every request. Optional: a nil value leaves
	// the chain otherwise identical, which is what the tests that care about routing
	// rather than telemetry want.
	Instrumentation *Instrumentation

	// Authenticator resolves the access token on every /v1 request. Nil leaves the chain a
	// pass-through, which is what the routing tests want and what the service was before
	// CP16 wired one in.
	Authenticator Authenticator

	// DeviceVerifier checks the signature on requests that claim a device (CP18). Nil
	// refuses any request that presents device headers and passes the rest through.
	DeviceVerifier DeviceVerifier

	// Authorizer decides permission-guarded routes (CP20). Nil refuses every one of them
	// and logs why: a service with no engine is not a service that should be serving.
	Authorizer Authorizer

	// AuthRoutes mounts the endpoints that must be reachable *without* a session — login
	// and refresh. They live under /v1 for versioning but outside the authenticated chain,
	// for the same reason the health endpoints do: a caller with no credentials cannot be
	// asked for one before being given the chance to obtain it.
	AuthRoutes func(chi.Router)

	// Routes mounts the authenticated modules under /v1, inside the full chain.
	Routes func(chi.Router)
}

// NewRouter builds the base router with the middleware chain in its documented order,
// mounts the operational endpoints, and audits every route for a declared requirement.
//
// Health endpoints sit outside the authenticated chain deliberately: an orchestrator
// probing readiness has no credentials, and requiring them would make the service
// permanently unready.
//
// The error is the audit's (CP20): a route registered without httpx.Declare. It is
// returned rather than logged so that the composition root refuses to start.
func NewRouter(opts RouterOptions) (*chi.Mux, error) {
	r := chi.NewRouter()

	r.Use(Recover(opts.Logger))
	r.Use(RequestID(opts.IDs))
	if opts.Instrumentation != nil {
		r.Use(opts.Instrumentation.Observe)
	}
	r.Use(AccessLog(opts.Logger))
	r.Use(SecurityHeaders)
	r.Use(CORS(opts.AllowedOrigins))
	r.Use(BodyLimit(opts.MaxBodyBytes))
	r.Use(Timeout(opts.RequestTimeout))

	if opts.Health != nil {
		r.Method(http.MethodGet, "/healthz", Declare(Public(), opts.Health.Live))
		r.Method(http.MethodGet, "/readyz", Declare(Public(), opts.Health.Ready))
		r.Method(http.MethodGet, "/version", Declare(Public(), opts.Health.BuildInfo))
	}

	// The unauthenticated corner of /v1. Login and refresh cannot sit behind the middleware
	// that requires what they exist to issue — but they do change state (they mint
	// credentials), so the forgery guard applies to them as it does to everything else.
	if opts.AuthRoutes != nil {
		r.Route("/v1/auth", func(a chi.Router) {
			a.Use(RequireRequestedWith(opts.Logger))
			// A login from a tablet is signed by the tablet, and the session it opens is
			// bound to it. There is no caller yet, so only the proof is checked here.
			a.Use(VerifyDevice(opts.Logger, opts.DeviceVerifier))
			opts.AuthRoutes(a)
		})
	}

	// Everything else under /v1 requires authentication, device verification and
	// authorisation. The chain is wired now; each link is implemented at its checkpoint.
	r.Route("/v1", func(v1 chi.Router) {
		v1.Use(RequireRequestedWith(opts.Logger))
		v1.Use(Authenticate(opts.Logger, opts.Authenticator))
		v1.Use(VerifyDevice(opts.Logger, opts.DeviceVerifier))
		v1.Use(Authorize(opts.Logger, opts.Authorizer))
		v1.Use(RateLimit(opts.Logger))

		if opts.Routes != nil {
			opts.Routes(v1)
		}

		v1.NotFound(func(w http.ResponseWriter, r *http.Request) {
			WriteError(w, r, opts.Logger, errs.ErrNotFound)
		})
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, opts.Logger, errs.ErrNotFound)
	})

	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, opts.Logger, errs.ErrBadRequest.WithDetail(
			errors.New("method not allowed for this path")))
	})

	if err := AuditRoutes(r); err != nil {
		return nil, err
	}
	return r, nil
}

// ServerOptions configures the HTTP server.
type ServerOptions struct {
	Addr            string
	Handler         http.Handler
	Logger          *slog.Logger
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration

	// Listener, when set, is used instead of binding Addr. Tests bind port 0 and need
	// to know which port they got; production leaves this nil.
	Listener net.Listener
}

// Serve runs the HTTP server until the process is asked to stop, then drains.
//
// Graceful shutdown matters here beyond tidiness: a station operator's request must not
// be lost because a deployment landed mid-save. In-flight requests finish; new ones are
// refused; the process exits when the last one completes or the timeout expires.
func Serve(ctx context.Context, opts ServerOptions) error {
	server := &http.Server{
		Addr:              opts.Addr,
		Handler:           opts.Handler,
		ReadTimeout:       opts.ReadTimeout,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      opts.WriteTimeout,
		IdleTimeout:       opts.IdleTimeout,
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)

	go func() {
		var err error
		if opts.Listener != nil {
			opts.Logger.Info("http server listening", "addr", opts.Listener.Addr().String())
			err = server.Serve(opts.Listener)
		} else {
			opts.Logger.Info("http server listening", "addr", opts.Addr)
			err = server.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		return err

	case <-ctx.Done():
		opts.Logger.Info("shutdown signal received, draining in-flight requests",
			"timeout", opts.ShutdownTimeout.String())

		shutdownCtx, cancel := context.WithTimeout(context.Background(), opts.ShutdownTimeout)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			opts.Logger.Error("graceful shutdown failed; forcing close", "error", err.Error())
			return server.Close()
		}

		opts.Logger.Info("shutdown complete")
		return nil
	}
}
