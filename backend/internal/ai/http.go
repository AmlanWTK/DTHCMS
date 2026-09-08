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
// Almost every route is a GET. There is nothing to change about the gateway here — no pause, no
// retry, no knob — which is why CP70 shipped one permission rather than two, unlike the queue
// (CP69) where retrying a dead letter runs code against a patient's record.
//
// # The one exception, added at CP72, and why it is not a knob
//
// `POST .../grounding-defects/{id}/review` records a person's verdict on a grounding violation:
// the model really did invent something, or the check was wrong about it. It changes nothing about
// what the gateway does, nothing about what a physician sees, and nothing about any patient's
// record — it is a *measurement*, and it is the only place acceptance criterion 2's
// false-positive rate can come from once the system is running against real prose. A frozen test
// set can tell you what the validator does to twenty answers written last September; only a person
// reading today's refusals can tell you whether it is refusing things it should not.
//
// It has its own permission (`ai.quality.review`) rather than sharing `ai.gateway.read`, because
// reading the log and pronouncing on it are different acts, and the number this produces is the
// input to any future argument for loosening the check.

// PermRead is the permission every route here needs.
//
// Sensitive, and the argument is in migration 00052: the outbound payload names no person, by
// construction and by three checks, but it carries that person's clinical picture in full — which
// is exactly what §4.4 blinds registration and the pharmacist to. A permission that showed a
// pharmacist every diagnosis in the clinic on the grounds that the name had been removed would be
// §4.4 defeated by a technicality.
const PermRead = "ai.gateway.read"

// PermReview is the permission on the one mutation. Sensitive: a defect carries an excerpt of what
// the model wrote about a patient, and you cannot classify what you may not read.
const PermReview = "ai.quality.review"

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

	review := httpx.Permission(PermReview)

	r.Route("/ops/ai", func(a chi.Router) {
		a.Method("GET", "/interactions", httpx.Declare(read, h.interactions))
		a.Method("GET", "/interactions/{id}", httpx.Declare(read, h.interaction))
		a.Method("GET", "/spend", httpx.Declare(read, h.spend))
		a.Method("GET", "/prompts", httpx.Declare(read, h.prompts))
		// CP72.
		a.Method("GET", "/grounding-defects", httpx.Declare(read, h.defects))
		a.Method("POST", "/grounding-defects/{id}/review", httpx.Declare(review, h.reviewDefect))
		a.Method("GET", "/evaluation-runs", httpx.Declare(read, h.evaluations))
	})
}

// defects is the reviewer's queue.
func (h *Handlers) defects(w http.ResponseWriter, r *http.Request) {
	filter := DefectFilter{
		Status: strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("status"))),
		Arm:    strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("arm"))),
		// Thirty days rather than the outbound log's one, because a defect is worked through over
		// days and a queue that hid last week's would hide exactly the backlog that makes the
		// false-positive rate unmeasurable.
		Since: h.clock.Now().Add(-30 * 24 * time.Hour),
		Limit: 100,
	}
	if filter.Status != "" && filter.Status != "OPEN" && filter.Status != "REVIEWED" {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("status",
			"A defect is either OPEN or REVIEWED.",
			"একটি ত্রুটি হয় OPEN নয়তো REVIEWED অবস্থায় থাকে।"))
		return
	}
	if filter.Arm != "" && !knownArm(filter.Arm) {
		// Refused rather than ignored, the same rule the status filter above follows: a filter that
		// quietly does nothing shows a reviewer the whole queue while they believe they are looking
		// at one arm of it.
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("arm",
			"That is not one of the grounding check's arms.",
			"এটি গ্রাউন্ডিং যাচাইয়ের কোনো ধাপ নয়।"))
		return
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			filter.Limit = parsed
		}
	}

	defects, err := h.store.Defects(r.Context(), h.facility(r), filter)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	health, err := h.store.Health(r.Context(), h.facility(r), h.clock.Now().Add(-24*time.Hour))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	// The rates beside the queue, and `false_positive_rate` is **absent** rather than zero when
	// nothing has been reviewed. A reviewer opening this page to decide whether the check is too
	// strict must not be shown 0% by a system that has never been told.
	rates := make([]map[string]any, 0, len(health))
	for _, row := range health {
		entry := map[string]any{
			"agent_code": row.AgentCode, "passed": row.Passed, "failed": row.Failed,
			"not_required": row.NotRequired, "defects_open": row.DefectsOpen,
			"defects_reviewed": row.DefectsReviewed,
		}
		if rate := row.FalsePositiveRate(); rate != nil {
			entry["false_positive_rate"] = *rate
		}
		rates = append(rates, entry)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"defects": defects, "grounding": rates, "as_of": h.clock.Now().UTC(),
	})
}

// reviewDefect records a person's verdict.
func (h *Handlers) reviewDefect(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	var body struct {
		Classification string `json:"classification"`
		Note           string `json:"note"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	body.Classification = strings.ToUpper(strings.TrimSpace(body.Classification))
	if !KnownClassification(body.Classification) {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("classification",
			"A verdict is TRUE_POSITIVE, FALSE_POSITIVE or VALIDATOR_DEFECT.",
			"রায় হবে TRUE_POSITIVE, FALSE_POSITIVE অথবা VALIDATOR_DEFECT।"))
		return
	}
	// The same twenty characters the check constraint requires, refused here so the reviewer gets a
	// sentence in their own language rather than a database error. Saying the check was wrong is
	// the claim that will later be used to argue for loosening it, and it has to be defensible.
	if body.Classification != "TRUE_POSITIVE" && len(strings.TrimSpace(body.Note)) < 20 {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("note",
			"Say in at least twenty characters why the check was wrong about this.",
			"যাচাইটি কেন ভুল ছিল তা অন্তত কুড়ি অক্ষরে লিখুন।"))
		return
	}

	var reviewer uuid.UUID
	if principal, ok := httpx.PrincipalFrom(r.Context()); ok {
		reviewer, _ = uuid.Parse(principal.UserID)
	}
	defect, err := h.store.ReviewDefect(r.Context(), Reviewing{
		DefectID: id, Facility: h.facility(r), Classification: body.Classification,
		ReviewedBy: reviewer, Note: strings.TrimSpace(body.Note), At: h.clock.Now().UTC(),
	})
	switch {
	case errors.Is(err, ErrDefectClosed):
		// 409 and not 404: the request is well formed and the caller is entitled to make it, and
		// the remedy is to read the colleague's verdict rather than to send different fields.
		httpx.WriteError(w, r, h.logger, errDefectClosed.WithDetail(err))
		return
	case err != nil:
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	h.logger.InfoContext(r.Context(), "a grounding defect was classified",
		"defect_id", defect.ID.String(), "agent_code", defect.AgentCode,
		"arm", string(defect.Arm), "classification", defect.Classification)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"defect": defect})
}

// evaluations is the trend §10.5 asks for.
func (h *Handlers) evaluations(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	runs, err := h.store.EvaluationRuns(r.Context(),
		strings.TrimSpace(r.URL.Query().Get("agent_code")), limit)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	// The two rates are computed here rather than stored, for the reason the migration gives: a
	// stored quotient is a number that stops agreeing with its own numerator. Absent when the run
	// measured nothing of that kind.
	out := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		entry := map[string]any{"run": run}
		if rate := run.DetectionRate(); rate != nil {
			entry["detection_rate"] = *rate
		}
		if rate := run.FalsePositiveRate(); rate != nil {
			entry["false_positive_rate"] = *rate
		}
		out = append(out, entry)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"runs": out, "as_of": h.clock.Now().UTC()})
}

func knownArm(arm string) bool {
	switch GroundingArm(arm) {
	case ArmCitation, ArmNumber, ArmDate, ArmDrug:
		return true
	}
	return false
}

// errDefectClosed is this module's second addition to the error catalogue.
//
// Its own code rather than the platform's bare CONFLICT, because a client has to be able to tell
// this from an idempotency-key conflict on the same route: the remedy here is to read what a
// colleague already decided, not to retry with a new key.
var errDefectClosed = errs.New("AI_DEFECT_ALREADY_REVIEWED", errs.KindConflict, http.StatusConflict,
	"Somebody has already recorded a verdict on this grounding defect.",
	"এই গ্রাউন্ডিং ত্রুটির বিষয়ে ইতিমধ্যে কেউ রায় দিয়েছেন।")

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
		"TIMEOUT", "CIRCUIT_OPEN", "REFUSED_TIER", "REFUSED_PHI", "UNGROUNDED":
		return true
	}
	return false
}
