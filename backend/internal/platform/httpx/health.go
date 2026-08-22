package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Dependency is something the service needs in order to serve requests.
type Dependency struct {
	Name  string
	Check func(context.Context) error
	// Critical marks a dependency the service cannot serve without. A non-critical
	// failure degrades the service; a critical one makes it unready.
	Critical bool
}

// Health serves the three endpoints every deployment needs.
//
// The distinction between them matters operationally:
//
//	/healthz — is the process alive? Never touches a dependency. A failing database must
//	           not cause the orchestrator to kill and restart a perfectly healthy process.
//	/readyz  — can it serve? Checks dependencies, and reports which one is unhappy.
//	/version — what exactly is running? The first question in any incident.
type Health struct {
	Version      string
	Service      string
	Commit       string
	BuildTime    string
	Dependencies []Dependency
	Timeout      time.Duration
	Logger       *slog.Logger
}

type healthResponse struct {
	Status  string            `json:"status"`
	Service string            `json:"service"`
	Version string            `json:"version"`
	Checks  map[string]string `json:"checks,omitempty"`
}

// Live handles /healthz.
func (h *Health) Live(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, healthResponse{
		Status:  "ok",
		Service: h.Service,
		Version: h.Version,
	})
}

// Ready handles /readyz.
//
// Dependency errors are reported as a status word, never as the underlying error text:
// a connection error can contain a host, a user name or a password. The detail goes to
// the log, where it is access-controlled.
func (h *Health) Ready(w http.ResponseWriter, r *http.Request) {
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	checks := make(map[string]string, len(h.Dependencies))
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		unready bool
	)

	for _, dep := range h.Dependencies {
		wg.Add(1)
		go func(dep Dependency) {
			defer wg.Done()

			status := "ok"
			if err := dep.Check(ctx); err != nil {
				status = "unavailable"
				if h.Logger != nil {
					h.Logger.ErrorContext(ctx, "dependency check failed",
						"dependency", dep.Name,
						"critical", dep.Critical,
						"error", err.Error())
				}
			}

			mu.Lock()
			checks[dep.Name] = status
			if status != "ok" && dep.Critical {
				unready = true
			}
			mu.Unlock()
		}(dep)
	}

	wg.Wait()

	status, code := "ok", http.StatusOK
	if unready {
		status, code = "unready", http.StatusServiceUnavailable
	}

	WriteJSON(w, code, healthResponse{
		Status:  status,
		Service: h.Service,
		Version: h.Version,
		Checks:  checks,
	})
}

// BuildInfo handles /version. Named BuildInfo rather than Version because the struct
// already carries a Version field, and Go does not allow both.
func (h *Health) BuildInfo(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]string{
		"service":    h.Service,
		"version":    h.Version,
		"commit":     h.Commit,
		"build_time": h.BuildTime,
	})
}
