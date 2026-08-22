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
}

// NewRouter builds the base router with the middleware chain in its documented order,
// and mounts the operational endpoints.
//
// Health endpoints sit outside the authenticated chain deliberately: an orchestrator
// probing readiness has no credentials, and requiring them would make the service
// permanently unready.
func NewRouter(opts RouterOptions) *chi.Mux {
	r := chi.NewRouter()

	r.Use(Recover(opts.Logger))
	r.Use(RequestID(opts.IDs))
	r.Use(AccessLog(opts.Logger))
	r.Use(SecurityHeaders)
	r.Use(CORS(opts.AllowedOrigins))
	r.Use(BodyLimit(opts.MaxBodyBytes))
	r.Use(Timeout(opts.RequestTimeout))

	if opts.Health != nil {
		r.Get("/healthz", opts.Health.Live)
		r.Get("/readyz", opts.Health.Ready)
		r.Get("/version", opts.Health.BuildInfo)
	}

	// Everything under /v1 will require authentication, device verification and
	// authorisation. The chain is wired now; each link is implemented at its checkpoint.
	r.Route("/v1", func(v1 chi.Router) {
		v1.Use(Authenticate(opts.Logger))
		v1.Use(VerifyDevice(opts.Logger))
		v1.Use(Authorize(opts.Logger))
		v1.Use(RateLimit(opts.Logger))

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

	return r
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
