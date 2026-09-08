package ai

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

// The gateway over HTTP (CP70).
//
// # There is no invoke endpoint, and there never will be
//
// Nothing outside this process may call a model. An agent decides that a call is warranted, from
// inside the module that owns the data, and calls [Gateway.Invoke] directly. An HTTP route that
// took an agent code and a payload would be a way around every rule in this package — the caller
// would be assembling the payload, so the caller would be deciding what counts as an identifier —
// and it would be reached for exactly when somebody was in a hurry.
//
// # What is here is the mitigation the plan names
//
// The checkpoint's stated risk is *"PHI leakage through free-text fields"* and its stated mitigation
// is *"the scrubber plus a human-reviewable outbound log"*. A log nobody can open is not
// reviewable, and psql at three in the morning is not a review process. So: the log, the payload of
// one call at a time, what it all cost against the budget, and the prompt registry as deployed.
//
// Every route is a GET. There is nothing to change here — no pause, no retry, no knob — which is
// why there is one permission rather than two, unlike the queue (CP69) where retrying a dead letter
// runs code against a patient's record.

// PermRead is the permission every route here needs.
//
// Sensitive, and the argument is in migration 00052: the outbound payload names no person, by
// construction and by three checks, but it carries that person's clinical picture in full — which
// is exactly what §4.4 blinds registration and the pharmacist to. A permission that showed a
// pharmacist every diagnosis in the clinic on the grounds that the name had been removed would be
// §4.4 defeated by a technicality.
const PermRead = "ai.gateway.read"

// defaultWindow is how far back the outbound log looks when nobody says. A day: long enough that
// this morning's synthesis is in it, short enough that the first page is not last month.
const defaultWindow = 24 * time.Hour

// Handlers serve the gateway's operational surface.
type Handlers struct {
	store  *Store
	clock  Clock
	logger *slog.Logger
	// facility resolves the caller's facility. Supplied by the composition root, because this
	// module may not import the one that knows how.
	facility func(*http.Request) uuid.UUID
}

// HandlersConfig builds Handlers.
type HandlersConfig struct {
	Store  *Store
	Clock  Clock
	Logger *slog.Logger
	// Facility resolves the caller's facility. Supplied by the composition root, because this
	// module may not import the one that knows how (D-61's multi-tenancy question is open, and a
	// gateway that hard-coded one facility would be the thing that has to be unpicked when it is
	// answered).
	Facility func(*http.Request) uuid.UUID
}

// NewHandlers builds them.
func NewHandlers(cfg HandlersConfig) *Handlers {
	return &Handlers{
		store: cfg.Store, clock: cfg.Clock, logger: cfg.Logger, facility: cfg.Facility,
	}
}

// Mount attaches /v1/ops/ai.
func (h *Handlers) Mount(r chi.Router) {
	read := httpx.Permission(PermRead)

	r.Route("/ops/ai", func(a chi.Router) {
		a.Method("GET", "/interactions", httpx.Declare(read, h.interactions))
		a.Method("GET", "/interactions/{id}", httpx.Declare(read, h.interaction))
		a.Method("GET", "/spend", httpx.Declare(read, h.spend))
		a.Method("GET", "/prompts", httpx.Declare(read, h.prompts))
	})
}

func (h *Handlers) interactions(w http.ResponseWriter, r *http.Request) {
	filter := InteractionFilter{
		AgentCode: strings.TrimSpace(r.URL.Query().Get("agent_code")),
		Status:    strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("status"))),
		Since:     h.clock.Now().Add(-defaultWindow),
		Limit:     50,
	}
	if filter.Status != "" && !knownStatus(filter.Status) {
		// Refused rather than silently ignored, the same rule the queue's status filter follows: a
		// filter that quietly does nothing shows a reviewer the whole log while they believe they
		// are looking at the refusals.
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("status",
			"That is not an AI interaction status.",
			"এটি কোনও এআই কলের অবস্থা নয়।"))
		return
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("window_seconds")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 60 || parsed > 30*86400 {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("window_seconds",
				"The window is between a minute and thirty days, in seconds.",
				"সময়সীমা এক মিনিট থেকে ত্রিশ দিনের মধ্যে, সেকেন্ডে দিতে হবে।"))
			return
		}
		filter.Since = h.clock.Now().Add(-time.Duration(parsed) * time.Second)
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			filter.Limit = parsed
		}
	}

	interactions, err := h.store.Interactions(r.Context(), h.facility(r), filter)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"interactions": interactions,
		"since":        filter.Since.UTC(),
		"as_of":        h.clock.Now().UTC(),
	})
}

func (h *Handlers) interaction(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	interaction, err := h.store.Interaction(r.Context(), h.facility(r), id)
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	// Logged, because opening one payload is the act the whole outbound log exists for and an
	// audit of the auditors is not a strange thing to want. No pseudonym and no patient id in the
	// line: the interaction id is enough to find it again.
	h.logger.InfoContext(r.Context(), "an AI outbound payload was opened for review",
		"interaction_id", interaction.ID.String(), "agent_code", interaction.AgentCode)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"interaction": interaction})
}

func (h *Handlers) spend(w http.ResponseWriter, r *http.Request) {
	now := h.clock.Now().UTC()
	dayStart := now.Truncate(24 * time.Hour)
	if raw := strings.TrimSpace(r.URL.Query().Get("day")); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("day",
				"Give the day as YYYY-MM-DD.", "দিনটি YYYY-MM-DD আকারে দিন।"))
			return
		}
		dayStart = parsed.UTC()
	}

	spend, err := h.store.DailySpend(r.Context(), h.facility(r), dayStart, dayStart.Add(24*time.Hour))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	// Thirty days of crossings beside the day's figures, because the question an administrator has
	// on opening this screen is rarely "what is today" and usually "has this been creeping".
	alerts, err := h.store.BudgetAlerts(r.Context(), h.facility(r), dayStart.AddDate(0, 0, -30), 100)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"day":    dayStart.Format("2006-01-02"),
		"spend":  spend,
		"alerts": alerts,
		"as_of":  now,
	})
}

func (h *Handlers) prompts(w http.ResponseWriter, r *http.Request) {
	// The content is included. A registry screen that showed version numbers and hashes but not the
	// prompt would answer "which version" and not "what did it say", and the second is the question
	// somebody has when an AI draft was wrong.
	prompts, err := h.store.DeployedPrompts(r.Context(), true)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	agents, err := h.store.Agents(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	models, err := h.store.Models(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	pinned := make([]Model, 0, len(models))
	for _, model := range models {
		pinned = append(pinned, model)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"agents":  agents,
		"prompts": prompts,
		"models":  pinned,
	})
}

func knownStatus(status string) bool {
	switch status {
	case "IN_FLIGHT", "SUCCEEDED", "CACHED", "INVALID_OUTPUT", "PROVIDER_ERROR",
		"TIMEOUT", "CIRCUIT_OPEN", "REFUSED_TIER", "REFUSED_PHI":
		return true
	}
	return false
}
