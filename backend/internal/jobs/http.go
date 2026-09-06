package jobs

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// The queue over HTTP (CP69).
//
// # There is no enqueue endpoint
//
// Nothing outside this process may put work on the queue. A job is enqueued by the code that
// decided the work was needed, inside the transaction that made that decision true — which is
// acceptance criterion 1, and an HTTP endpoint that accepted a kind and a payload would be a way
// around it that somebody would eventually use because it was convenient.
//
// What is here is the operator's view: what is queued, what has failed and why, whether the SLA is
// being met, and the two controls — retry a dead-lettered job, pause a kind — that an incident
// actually needs.

const (
	// PermRead is queue health: a graph, with no patient in it by construction (invariant 90).
	PermRead = "ops.jobs.read"
	// PermManage is touching it. Separate, because retrying a dead-lettered job runs code
	// against a patient's record and pausing a kind stops the synthesis §7.1 promises will be
	// ready before the consultation. The floor supervisor who needs to know whether the queue is
	// healthy should not thereby be able to turn it off.
	PermManage = "ops.jobs.manage"
)

// defaultHealthWindow is what "recently" means on the dashboard when nobody says.
//
// An hour: long enough that a quiet queue still has something in it, short enough that a failure
// rate reflects what is happening now rather than what happened at the morning rush.
const defaultHealthWindow = time.Hour

// Handlers serve the queue.
type Handlers struct {
	store  *Store
	clock  interface{ Now() time.Time }
	logger *slog.Logger
}

// HandlersConfig builds Handlers.
type HandlersConfig struct {
	Store  *Store
	Clock  interface{ Now() time.Time }
	Logger *slog.Logger
}

// NewHandlers builds them.
func NewHandlers(cfg HandlersConfig) *Handlers {
	return &Handlers{store: cfg.Store, clock: cfg.Clock, logger: cfg.Logger}
}

// Mount attaches /v1/ops/jobs.
func (h *Handlers) Mount(r chi.Router) {
	read := httpx.Permission(PermRead)
	manage := httpx.Permission(PermManage)

	r.Route("/ops/jobs", func(j chi.Router) {
		j.Method("GET", "/health", httpx.Declare(read, h.health))
		j.Method("GET", "/kinds", httpx.Declare(read, h.kinds))
		j.Method("GET", "/", httpx.Declare(read, h.list))
		j.Method("GET", "/{id}", httpx.Declare(read, h.byID))
		j.Method("POST", "/{id}/retry", httpx.Declare(manage, h.retry))
		j.Method("POST", "/{id}/cancel", httpx.Declare(manage, h.cancel))
		j.Method("POST", "/kinds/{kind}/pause", httpx.Declare(manage, h.pause))
		j.Method("POST", "/kinds/{kind}/resume", httpx.Declare(manage, h.resume))
	})
}

func (h *Handlers) health(w http.ResponseWriter, r *http.Request) {
	window := defaultHealthWindow
	if raw := strings.TrimSpace(r.URL.Query().Get("window_seconds")); raw != "" {
		// Refused rather than clamped, for the same reason the status filter below is refused
		// rather than ignored: a window that quietly becomes an hour shows an operator an hour's
		// arithmetic while they believe they are looking at the last thirty seconds, and every
		// rate on the page then means something other than what they asked for.
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 60 || parsed > 86400 {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("window_seconds",
				"The window is between a minute and a day, in seconds.",
				"সময়সীমা এক মিনিট থেকে এক দিনের মধ্যে, সেকেন্ডে দিতে হবে।"))
			return
		}
		window = time.Duration(parsed) * time.Second
	}
	now := h.clock.Now().UTC()
	health, err := h.store.Health(r.Context(), window, now)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"queues": health,
		// On the payload rather than left to the client to assume, because every rate in the rows
		// is over this window and a screen that showed "2% failures" without saying "in the last
		// hour" is a screen that means something different at midnight.
		"window_seconds": int(window.Seconds()),
		"as_of":          now,
	})
}

func (h *Handlers) kinds(w http.ResponseWriter, r *http.Request) {
	kinds, err := h.store.Kinds(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"kinds": kinds})
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	status := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("status")))
	if status != "" && !knownStatus(status) {
		// Refused rather than silently ignored. A filter that quietly does nothing shows an
		// operator the whole queue while they believe they are looking at the failures.
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("status",
			"That is not a job status.", "এটি কোনও কাজের অবস্থা নয়।"))
		return
	}

	// The filter this checkpoint's headline number needs, and which it did not have.
	//
	// The queue-health page can say "63.6% on time, 14 of 22 met it" — and until this existed
	// there was no way to reach the eight that missed. They succeeded, so they are not among the
	// dead letters; they are finished, so they are not waiting; and the only filters were status
	// and kind. A physician saying "the summary was not ready at 10:15" could be shown a
	// percentage and not one of the jobs behind it, which makes "measured rather than asserted"
	// true of the number and false of the screen.
	var met *bool
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sla"))) {
	case "":
	case "missed":
		missed := false
		met = &missed
	case "met":
		kept := true
		met = &kept
	default:
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("sla",
			"Filter by whether the deadline was met: 'met' or 'missed'.",
			"সময়সীমা রক্ষা হয়েছে কি না, তা দিয়ে ছাঁকুন: 'met' বা 'missed'।"))
		return
	}

	jobs, err := h.store.List(r.Context(), status,
		strings.TrimSpace(r.URL.Query().Get("kind")), met, limit)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (h *Handlers) byID(w http.ResponseWriter, r *http.Request) {
	id, ok := h.jobID(w, r)
	if !ok {
		return
	}
	job, err := h.store.ByID(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateJobs(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"job": job})
}

func (h *Handlers) retry(w http.ResponseWriter, r *http.Request) {
	id, ok := h.jobID(w, r)
	if !ok {
		return
	}
	job, err := h.store.Retry(r.Context(), id, h.clock.Now())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateJobs(err))
		return
	}
	h.logger.InfoContext(r.Context(), "a dead-lettered job was retried by hand",
		"kind", job.Kind, "job_id", job.ID.String())
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"job": job})
}

type cancelRequest struct {
	Reason string `json:"reason"`
}

func (h *Handlers) cancel(w http.ResponseWriter, r *http.Request) {
	id, ok := h.jobID(w, r)
	if !ok {
		return
	}
	var body cancelRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		// A cancelled job that says nothing is a gap somebody has to explain later, and the
		// person who can explain it is the one cancelling it now.
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("reason",
			"Say why this job is being cancelled.",
			"এই কাজটি কেন বাতিল করা হচ্ছে, তা লিখুন।"))
		return
	}
	job, err := h.store.Cancel(r.Context(), id, reason, h.clock.Now())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateJobs(err))
		return
	}
	h.logger.InfoContext(r.Context(), "a job was cancelled by hand",
		"kind", job.Kind, "job_id", job.ID.String())
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"job": job})
}

func (h *Handlers) pause(w http.ResponseWriter, r *http.Request) {
	caller, ok := httpx.CallerFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	// Recorded on the kind, because pausing stops work a clinic is relying on and the person who
	// did it has to be findable afterwards.
	by, err := uuid.Parse(caller.UserID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	kind, err := h.store.Pause(r.Context(), chi.URLParam(r, "kind"), by, h.clock.Now())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateJobs(err))
		return
	}
	// At warn, not info: a paused kind is work that has stopped happening, and the log is where
	// somebody looks when they are trying to work out why.
	h.logger.WarnContext(r.Context(), "a job kind was paused", "kind", kind.Kind)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"kind": kind})
}

func (h *Handlers) resume(w http.ResponseWriter, r *http.Request) {
	kind, err := h.store.Resume(r.Context(), chi.URLParam(r, "kind"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateJobs(err))
		return
	}
	h.logger.InfoContext(r.Context(), "a job kind was resumed", "kind", kind.Kind)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"kind": kind})
}

func (h *Handlers) jobID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return uuid.Nil, false
	}
	return id, true
}

func knownStatus(status string) bool {
	switch status {
	case "AVAILABLE", "RUNNING", "SUCCEEDED", "DISCARDED", "CANCELLED":
		return true
	}
	return false
}

func translateJobs(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errs.ErrNotFound
	case errors.Is(err, ErrUnknownKind):
		return errs.ErrNotFound
	case errors.Is(err, ErrNotRetryable):
		// 409, and the message says what the state actually is. A 404 here would send an
		// operator looking for a typo when the real answer is that somebody else retried it
		// thirty seconds ago.
		return errs.New("JOB_NOT_RETRYABLE", errs.KindConflict, http.StatusConflict,
			"Only a job that has been given up on can be retried. This one has not.",
			"শুধু যেসব কাজ ছেড়ে দেওয়া হয়েছে সেগুলিই আবার চালানো যায়। এটি তেমন নয়।").WithDetail(err)
	case errors.Is(err, ErrNotCancellable):
		return errs.New("JOB_NOT_CANCELLABLE", errs.KindConflict, http.StatusConflict,
			"This job has already started, so it cannot be cancelled. It will finish or fail on its own.",
			"এই কাজটি শুরু হয়ে গেছে, তাই বাতিল করা যাবে না। এটি নিজেই শেষ হবে বা ব্যর্থ হবে।").WithDetail(err)
	case errors.Is(err, ErrAlreadyPaused):
		return errs.New("JOB_KIND_ALREADY_PAUSED", errs.KindConflict, http.StatusConflict,
			"That job type is already paused.",
			"এই ধরনের কাজ আগে থেকেই থামানো আছে।").WithDetail(err)
	case errors.Is(err, ErrNotPaused):
		return errs.New("JOB_KIND_NOT_PAUSED", errs.KindConflict, http.StatusConflict,
			"That job type is not paused.",
			"এই ধরনের কাজ থামানো নেই।").WithDetail(err)
	}
	return errs.ErrInternal.WithDetail(err)
}
