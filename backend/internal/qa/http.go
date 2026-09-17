package qa

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// Station 10 over HTTP (CP83).
//
// # Four permissions, and the split between two of them is the point
//
// `qa.review` reads the queue, the findings and **the override rate**. `qa.clear` and `qa.bounce`
// are the two decisions. `qa.override` is the consultant's valve, and it is deliberately not held
// by the officer who holds the other three.
//
// `docs/qa-rules.md` §2: *"the override rate is Quality's to watch, not the prescriber's"*. So the
// rate route is behind `qa.review` — QA and ADMIN — and the override route is behind
// `qa.override` — PHYSICIAN and ADMIN. A physician can grant one and cannot see how often they
// are granted; the officer watching the rate cannot grant one. That is the same separation CP57
// made and it is the only thing standing between a valve and a habit.
//
// # Every route declares and judges its resource (ADR-0036)
//
// The three that take a prescription id are `httpx.PermissionScoped`, and each resolves the
// prescription to its patient and then asks `rbac.AuthorizeStationRead` or `...StationWrite`. The
// queue is a list and uses `rbac.AuthorizeList`. The rule table is facility reference data with no
// patient in it, so it is a plain `httpx.Permission` — there is no resource to judge.
//
// # A 403 does not say whether the prescription exists
//
// `ErrNotFound` and a refused reach both answer the same way, which is why every handler resolves
// the prescription *before* the authorisation check and then maps both outcomes onto the same
// shape. A handler that returned 404 for "no such prescription" and 403 for "not yours" would be
// an oracle: try ids until one of them says 403.
//
// # No PHI in a log line
//
// The handlers log errors, never bodies. A finding names a drug and a bounce reason names a
// missing test, and both belong in the ledger rather than in a log somebody greps.

const (
	// PermReview reads the queue, a review and the override rate.
	PermReview = "qa.review"
	// PermClear records a clearance.
	PermClear = "qa.clear"
	// PermBounce records a bounce.
	PermBounce = "qa.bounce"
	// PermOverride is the consultant's valve.
	PermOverride = "qa.override"
	// PermRuleWrite changes the checklist. Criterion 5's route.
	PermRuleWrite = "qa.rule.write"

	// PurposeOverride is the step-up purpose an override needs. The contract test holds it
	// equal to `auth.PurposeQAOverride`.
	PurposeOverride = "qa.override"
)

// Audit is how a QA decision reaches the security audit trail.
//
// An interface rather than the recorder itself, because `qa` may not import `audit` — and the
// reason that rule exists is worth restating: a module able to write audit entries directly would
// grow a second, differently-shaped way of describing what happened, and the trail's value is
// that there is exactly one.
//
// Nil records nothing, which is what a contract test's handler carries.
type Audit interface {
	Decided(ctx context.Context, d DecisionAudit) error
	Overridden(ctx context.Context, o OverrideAudit) error
	RuleChanged(ctx context.Context, c RuleAudit) error
}

// DecisionAudit is one QA outcome on its way to the audit trail.
//
// **It carries no clinical content.** Not a drug, not a missing test, not a diagnosis. The counts
// and the outcome say a decision happened and what it was; what it was *about* is in the ledger
// and the review row, which is where a patient's record belongs.
type DecisionAudit struct {
	FacilityID uuid.UUID
	ActorID    uuid.UUID
	ActorCode  string
	ActorRole  string

	PatientID      uuid.UUID
	VisitID        uuid.UUID
	PrescriptionID uuid.UUID

	Outcome       Outcome
	BounceStation string
	Blocking      int
	Warnings      int
	Acknowledged  int
	RulesLive     int
	OnOverride    bool
}

// OverrideAudit is one consultant override. The reason travels; the findings do not.
type OverrideAudit struct {
	FacilityID uuid.UUID
	ActorID    uuid.UUID
	ActorCode  string
	ActorRole  string

	PatientID      uuid.UUID
	VisitID        uuid.UUID
	PrescriptionID uuid.UUID

	Reason   string
	Blocking int
}

// RuleAudit is a change to the checklist. Configuration, not a patient's record — which is why
// it goes to the security trail where role grants and credential resets live.
type RuleAudit struct {
	FacilityID uuid.UUID
	ActorID    uuid.UUID
	ActorCode  string
	ActorRole  string

	RuleCode string
	Change   string
}

// Handlers serve station 10.
type Handlers struct {
	service *Service
	store   *Store
	audit   Audit
	stepUp  httpx.StepUpVerifier
	clock   interface{ Now() time.Time }
	logger  *slog.Logger
}

// HandlersConfig builds them.
type HandlersConfig struct {
	Service *Service
	Store   *Store
	Audit   Audit
	// StepUp verifies the second factor an override needs. Nil makes every override refuse,
	// which is the right failure for a missing verifier: an override route with no step-up is a
	// hole, not a head start.
	StepUp httpx.StepUpVerifier
	Clock  interface{ Now() time.Time }
	Logger *slog.Logger
}

// NewHandlers builds the handlers.
func NewHandlers(cfg HandlersConfig) *Handlers {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Handlers{service: cfg.Service, store: cfg.Store, audit: cfg.Audit,
		stepUp: cfg.StepUp, clock: cfg.Clock, logger: logger}
}

func (h *Handlers) now() time.Time {
	if h.clock == nil {
		return time.Now().UTC()
	}
	return h.clock.Now().UTC()
}

// Mount wires the routes.
func (h *Handlers) Mount(c chi.Router) {
	// **Not `httpx.PermissionScoped`, and the reason is worth writing down.**
	//
	// Every role that holds `qa.review`, `qa.clear`, `qa.bounce` or `qa.override` reaches the
	// whole facility: QA, PHYSICIAN and ADMIN are all facility-wide for clinical actions
	// (`rbac.scopeFor`). A scoped declaration would open a resource-scope debt that the route
	// guard's own decision has already settled in substance, and `TestNoRouteDeclaresAResource
	// CheckItDoesNotNeed` refuses it — because a declaration inviting a check nobody owes is a
	// declaration a reader mistakes for a check somebody makes.
	//
	// The handlers still call `rbac.AuthorizeStationRead`/`...StationWrite` on the patient they
	// resolved. Today that call cannot refuse anybody who reached the handler, and it is named in
	// this checkpoint's report as cosmetic for exactly that reason. It is kept because it is the
	// door that becomes load-bearing the day somebody grants `qa.review` to a station role, and a
	// door added later is a door somebody has to remember to add.
	review := httpx.Permission(PermReview)
	clear := httpx.Permission(PermClear)
	bounce := httpx.Permission(PermBounce)
	override := httpx.Permission(PermOverride)

	c.Route("/qa", func(q chi.Router) {
		// The queue is a list with no single resource, so it uses the list door.
		q.Method("GET", "/queue", httpx.Declare(review, h.queue))

		// The rule table: facility reference data with no patient in it, so a plain permission.
		q.Method("GET", "/rules", httpx.Declare(httpx.Permission(PermReview, PermRuleWrite), h.rules))
		q.Method("PATCH", "/rules/{ruleId}", httpx.Declare(httpx.Permission(PermRuleWrite), h.patchRule))

		// **Quality's view, and deliberately not the prescriber's.** Behind `qa.review`, which
		// QA holds and PHYSICIAN does not, while `qa.override` is the other way round.
		q.Method("GET", "/overrides", httpx.Declare(httpx.Permission(PermReview), h.overrides))
	})

	c.Route("/prescriptions/{prescriptionId}/qa", func(one chi.Router) {
		one.Method("GET", "/", httpx.Declare(review, h.review))
		one.Method("GET", "/decisions", httpx.Declare(review, h.decisions))
		// **Two routes and not one with an `outcome` field.** They are two permissions —
		// `qa.clear` and `qa.bounce` are separate rows in CP15's catalogue — and a single route
		// declaring both would hand every clearer the ability to bounce and every bouncer the
		// ability to clear. A per-outcome check inside one handler would work and would be the
		// kind of check that is one refactor from being dropped; the route guard is not.
		one.Method("POST", "/clearance", httpx.Declare(clear, h.clear))
		one.Method("POST", "/bounce", httpx.Declare(bounce, h.bounce))

		// Its own route, its own permission, its own step-up (criterion 4).
		stepped := httpx.RequireStepUp(h.logger, h.stepUp, PurposeOverride)(
			http.HandlerFunc(h.override))
		one.Method("POST", "/override", httpx.Declare(override, stepped.ServeHTTP))
	})
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

func (h *Handlers) queue(w http.ResponseWriter, r *http.Request) {
	principal, present := httpx.PrincipalFrom(r.Context())
	if !present {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	if _, err := rbac.AuthorizeList(r.Context(), PermReview); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	entries, err := h.store.Queue(r.Context(), idOf(principal.FacilityID), 50)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"queue": entries})
}

// review runs the rules and returns what they found.
func (h *Handlers) review(w http.ResponseWriter, r *http.Request) {
	facility, id, patient, ok := h.resolve(w, r, false)
	if !ok {
		return
	}
	_ = patient

	review, err := h.service.Review(r.Context(), facility, id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	stands, err := h.store.ClearanceStands(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"review": review,
		// The two sentences the screen leads with, composed here rather than on the client, so
		// that the empty-rule-table case says what was *not* done in the same words everywhere.
		"summary_en": review.SummaryEN(),
		"summary_bn": review.SummaryBN(),
		"can_clear":  review.CanClear(),
		// What the database says, not what this process computed. A screen that showed the Go
		// answer while the trigger held a different one would be the defect this station is.
		"clearance_stands": stands,
	})
}

func (h *Handlers) decisions(w http.ResponseWriter, r *http.Request) {
	facility, id, _, ok := h.resolve(w, r, false)
	if !ok {
		return
	}
	decisions, err := h.store.Decisions(r.Context(), facility, id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"decisions": decisions})
}

// ---------------------------------------------------------------------------
// Deciding
// ---------------------------------------------------------------------------

type decisionBody struct {
	EventID       string   `json:"event_id"`
	Outcome       string   `json:"outcome"`
	Acknowledged  []string `json:"acknowledged"`
	BounceStation string   `json:"bounce_station_code"`
	ReasonEN      string   `json:"reason_en"`
	ReasonBN      string   `json:"reason_bn"`
}

// clear records a clearance. `qa.clear`.
func (h *Handlers) clear(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, OutcomeCleared)
}

// bounce records a bounce, with its station and its reason. `qa.bounce`.
func (h *Handlers) bounce(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, OutcomeBounced)
}

func (h *Handlers) decide(w http.ResponseWriter, r *http.Request, outcome Outcome) {
	principal, present := httpx.PrincipalFrom(r.Context())
	if !present {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	facility, id, _, ok := h.resolve(w, r, true)
	if !ok {
		return
	}

	var body decisionBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithDetail(err))
		return
	}
	eventID, err := uuid.Parse(strings.TrimSpace(body.EventID))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("event_id",
			"a client-generated id makes a retried decision one decision",
			"ক্লায়েন্টের তৈরি আইডি থাকলে পুনরায় পাঠানো সিদ্ধান্ত একটিই সিদ্ধান্ত থাকে"))
		return
	}

	decision, err := h.service.Decide(r.Context(), facility, Deciding{
		EventID: eventID, PrescriptionID: id, Outcome: outcome,
		Acknowledged:  body.Acknowledged,
		BounceStation: strings.TrimSpace(body.BounceStation),
		ReasonEN:      strings.TrimSpace(body.ReasonEN),
		ReasonBN:      strings.TrimSpace(body.ReasonBN),
		Source:        sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}

	if h.audit != nil {
		blocking, warnings := 0, 0
		for _, f := range decision.Findings {
			if f.Sev.Blocks() {
				blocking++
			} else {
				warnings++
			}
		}
		_ = h.audit.Decided(r.Context(), DecisionAudit{
			FacilityID: facility, ActorID: idOf(principal.UserID), ActorCode: principal.Code,
			ActorRole: principal.Role, PatientID: decision.PatientID,
			VisitID: decision.VisitID, PrescriptionID: id, Outcome: decision.Outcome,
			BounceStation: decision.BounceStation, Blocking: blocking, Warnings: warnings,
			Acknowledged: len(decision.Acknowledged), OnOverride: decision.OverrideID != nil,
		})
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"decision": decision})
}

// ---------------------------------------------------------------------------
// The override
// ---------------------------------------------------------------------------

type overrideBody struct {
	EventID string `json:"event_id"`
	Reason  string `json:"reason"`
}

// override is criterion 4, and all three halves of it are separately refusable.
//
//  1. **Elevated permission** — `qa.override`, declared on the route and held by PHYSICIAN and
//     ADMIN. A QA officer with every other permission in this file is refused here.
//  2. **Step-up** — `httpx.RequireStepUp` with this checkpoint's own purpose. A session that
//     authenticated an hour ago is refused until the consultant re-proves who they are.
//  3. **A reason** — refused by the service, by the event payload's `Validate`, and by a CHECK
//     constraint on `read.qa_override`.
func (h *Handlers) override(w http.ResponseWriter, r *http.Request) {
	principal, present := httpx.PrincipalFrom(r.Context())
	if !present {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	facility, id, _, ok := h.resolve(w, r, true)
	if !ok {
		return
	}
	var body overrideBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithDetail(err))
		return
	}
	eventID, err := uuid.Parse(strings.TrimSpace(body.EventID))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("event_id",
			"a client-generated id makes a retried override one override",
			"ক্লায়েন্টের তৈরি আইডি থাকলে পুনরায় পাঠানো ওভাররাইড একটিই থাকে"))
		return
	}
	if strings.TrimSpace(body.Reason) == "" {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("reason",
			"an override says why: the valve is acceptable only while it is legible",
			"ওভাররাইডে কারণ লিখতে হয়: এই ছাড় গ্রহণযোগ্য কেবল যতক্ষণ তা পড়া যায়"))
		return
	}

	out, err := h.service.Override(r.Context(), facility, eventID, id, body.Reason, sourceOf(r))
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}

	if h.audit != nil {
		_ = h.audit.Overridden(r.Context(), OverrideAudit{
			FacilityID: facility, ActorID: idOf(principal.UserID), ActorCode: principal.Code,
			ActorRole: principal.Role, PatientID: out.PatientID, VisitID: out.VisitID,
			PrescriptionID: id, Reason: out.Reason, Blocking: len(out.BlockingAtGrant),
		})
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"override": out})
}

// overrides is the rate view.
func (h *Handlers) overrides(w http.ResponseWriter, r *http.Request) {
	principal, present := httpx.PrincipalFrom(r.Context())
	if !present {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	// Whole days, half-open. CP57's reason: a window that ended "now" would exclude the override
	// granted a minute ago, which is the one somebody is asking about.
	to := h.now().Truncate(24*time.Hour).AddDate(0, 0, 1)
	from := to.AddDate(0, 0, -30)
	if raw := strings.TrimSpace(r.URL.Query().Get("from")); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("from",
				"a date like 2026-09-13", "২০২৬-০৯-১৩ ধরনের তারিখ"))
			return
		}
		from = parsed
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("to")); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("to",
				"a date like 2026-09-13", "২০২৬-০৯-১৩ ধরনের তারিখ"))
			return
		}
		to = parsed.AddDate(0, 0, 1)
	}

	list, err := h.store.Overrides(r.Context(), idOf(principal.FacilityID), from, to)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"from": from.Format("2006-01-02"), "to": to.Format("2006-01-02"),
		"overrides": list, "count": len(list),
	})
}

// ---------------------------------------------------------------------------
// The rule table — criterion 5
// ---------------------------------------------------------------------------

func (h *Handlers) rules(w http.ResponseWriter, r *http.Request) {
	principal, present := httpx.PrincipalFrom(r.Context())
	if !present {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	rules, err := h.store.Rules(r.Context(), idOf(principal.FacilityID))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	// The shapes travel with the rules, so a configuration screen can say what each rule's
	// parameters mean without holding its own copy of a catalogue the application cannot write.
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rules": rules, "kinds": AllKinds})
}

type ruleBody struct {
	Severity      *string `json:"severity"`
	BounceStation *string `json:"bounce_station_code"`
	WindowDays    *int    `json:"window_days"`
	Params        *Params `json:"params"`
	Enabled       *bool   `json:"enabled"`
	TitleEN       *string `json:"title_en"`
	TitleBN       *string `json:"title_bn"`
	DetailEN      *string `json:"detail_en"`
	DetailBN      *string `json:"detail_bn"`
	LooksForEN    *string `json:"looks_for_en"`
	LooksForBN    *string `json:"looks_for_bn"`
	Retired       *bool   `json:"retired"`
}

// patchRule is criterion 5's route: severity, bounce station, recency window, parameters and
// enabled, changed without a release.
//
// **`kind` and `code` are not in the body and cannot be.** A kind is a Go predicate; a rule that
// changed kind would explain every past finding with a question it never asked. The database
// refuses it as well, and this is the half that makes the refusal unreachable rather than merely
// enforced.
func (h *Handlers) patchRule(w http.ResponseWriter, r *http.Request) {
	principal, present := httpx.PrincipalFrom(r.Context())
	if !present {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	ruleID, err := uuid.Parse(chi.URLParam(r, "ruleId"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	var body ruleBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithDetail(err))
		return
	}

	change := RuleChange{
		BounceStation: body.BounceStation, WindowDays: body.WindowDays, Params: body.Params,
		Enabled: body.Enabled, TitleEN: body.TitleEN, TitleBN: body.TitleBN,
		DetailEN: body.DetailEN, DetailBN: body.DetailBN,
		LooksForEN: body.LooksForEN, LooksForBN: body.LooksForBN,
		Retired: body.Retired,
	}
	if body.Severity != nil {
		severity := Severity(strings.ToUpper(strings.TrimSpace(*body.Severity)))
		change.Severity = &severity
	}

	updated, err := h.store.UpdateRule(r.Context(), idOf(principal.FacilityID), ruleID,
		idOf(principal.UserID), change)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	// Validated *after* the write and reported, rather than before it and refused. The database
	// owns the refusals it can make; this catches the ones only Go knows — a recency rule left
	// with no codes, say — and a rule that fails it is returned with the problem named so the
	// screen can show it rather than leaving a silently inert rule behind.
	problem := ""
	if err := updated.Validate(); err != nil {
		problem = err.Error()
	}

	if h.audit != nil {
		_ = h.audit.RuleChanged(r.Context(), RuleAudit{
			FacilityID: idOf(principal.FacilityID), ActorID: idOf(principal.UserID),
			ActorCode: principal.Code, ActorRole: principal.Role,
			RuleCode: updated.Code, Change: changeSummary(change),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rule": updated, "problem": problem})
}

// changeSummary names the fields that moved, and carries none of their values.
//
// The audit trail answers "who changed the checklist and what did they touch"; the values are on
// the row, which is versioned by `updated_at` and `updated_by`. A summary carrying the new
// severity would be a second, differently-shaped copy of the rule.
func changeSummary(c RuleChange) string {
	fields := []string{}
	if c.Severity != nil {
		fields = append(fields, "severity")
	}
	if c.BounceStation != nil {
		fields = append(fields, "bounce_station")
	}
	if c.WindowDays != nil {
		fields = append(fields, "window_days")
	}
	if c.Params != nil {
		fields = append(fields, "params")
	}
	if c.Enabled != nil {
		fields = append(fields, "enabled")
	}
	if c.TitleEN != nil || c.TitleBN != nil || c.DetailEN != nil || c.DetailBN != nil ||
		c.LooksForEN != nil || c.LooksForBN != nil {
		fields = append(fields, "text")
	}
	if c.Retired != nil {
		fields = append(fields, "retired")
	}
	if len(fields) == 0 {
		return "nothing"
	}
	return strings.Join(fields, ", ")
}

// ---------------------------------------------------------------------------
// Shared
// ---------------------------------------------------------------------------

// resolve turns the path's prescription id into a facility, an id and a patient, and judges the
// caller's reach over that patient.
//
// **The resolution happens before the judgement and both failures answer the same way.** A
// handler that answered 404 for "no such prescription" and 403 for "not yours" would let a caller
// enumerate prescriptions by watching which answer they got.
func (h *Handlers) resolve(w http.ResponseWriter, r *http.Request, writing bool) (
	facility, id, patient uuid.UUID, ok bool) {

	principal, present := httpx.PrincipalFrom(r.Context())
	if !present {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	id, err := uuid.Parse(chi.URLParam(r, "prescriptionId"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	_, patient, _, err = h.store.Status(r.Context(), idOf(principal.FacilityID), id)
	if err != nil {
		// Not found and not yours are the same answer. The scope debt is settled by writing a
		// 404 rather than by an authorisation call, which is why this path is safe: a 404 tells
		// the caller nothing a guess would not have.
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}

	// ADR-0036: the route declared that it defers the resource question, and this is where it is
	// answered. Reading reaches every patient this station has had on the current visit; writing
	// reaches only the one it is holding.
	action := PermReview
	if writing {
		action = PermClear
	}
	if writing {
		err = rbac.AuthorizeStationWrite(r.Context(), action, "prescription", patient)
	} else {
		err = rbac.AuthorizeStationRead(r.Context(), action, "prescription", patient)
	}
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	return idOf(principal.FacilityID), id, patient, true
}

// translate maps this module's errors onto HTTP, each with its own bilingual sentence.
func translate(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errs.ErrNotFound
	case errors.Is(err, ErrNotUnderReview):
		return errs.ErrConflict.WithMessageIn(
			"This prescription is not with QA.",
			"এই ব্যবস্থাপত্রটি কিউএ-তে নেই।")
	case errors.Is(err, ErrBlocked):
		return errs.ErrConflict.WithMessageIn(
			"This file has findings that block clearance. Fix them, bounce it, or have a consultant record an override.",
			"এই ফাইলে এমন বিষয় আছে যা ছাড়পত্র আটকায়। ঠিক করুন, ফেরত পাঠান, অথবা কনসালট্যান্টকে দিয়ে ওভাররাইড লেখান।")
	case errors.Is(err, ErrWarningsNotAcknowledged):
		return errs.ErrValidation.WithMessageIn(
			"Every warning has to be acknowledged before this file clears.",
			"ছাড়পত্রের আগে প্রতিটি সতর্কবার্তা স্বীকার করতে হবে।")
	case errors.Is(err, ErrBounceNeedsAStation):
		return errs.ErrValidation.WithFieldIn("bounce_station_code",
			"a bounce names the station the patient walks back to",
			"ফেরত পাঠানোর সময় রোগী কোন কেন্দ্রে যাবেন তা লিখতে হয়")
	case errors.Is(err, ErrBounceNeedsAReason):
		return errs.ErrValidation.WithFieldIn("reason_en",
			"a bounce says what is wrong, in both languages",
			"ফেরত পাঠানোর কারণ দুই ভাষাতেই লিখতে হয়")
	case errors.Is(err, ErrOverrideReasonRequired):
		return errs.ErrValidation.WithFieldIn("reason",
			"an override says why", "ওভাররাইডের কারণ লিখতে হয়")
	case errors.Is(err, ErrNothingToOverride):
		return errs.ErrConflict.WithMessageIn(
			"Nothing is blocking this prescription, so there is nothing to override.",
			"এই ব্যবস্থাপত্রে কিছুই আটকাচ্ছে না, তাই ওভাররাইড করার কিছু নেই।")
	case errors.Is(err, ErrAlreadyOverridden):
		return errs.ErrConflict.WithMessageIn(
			"This prescription has already been overridden.",
			"এই ব্যবস্থাপত্রে ইতিমধ্যেই ওভাররাইড দেওয়া হয়েছে।")
	case errors.Is(err, ErrRuleInvalid), errors.Is(err, ErrUnknownKind):
		return errs.ErrValidation.WithDetail(err)
	default:
		return errs.ErrInternal.WithDetail(err)
	}
}

// sourceOf is how the decision reached the server, for the ledger envelope.
func sourceOf(r *http.Request) eventstore.Source {
	if strings.TrimSpace(r.Header.Get("X-DTHCMS-Device")) != "" {
		return eventstore.SourceMobileOnline
	}
	return eventstore.SourceWeb
}

// idOf parses an id the authentication chain produced.
//
// It cannot fail for a principal the chain built, so a failure returns the nil uuid rather than
// an error nobody can trigger — and the nil uuid matches no facility and no user, so the query it
// reaches returns nothing. Fail-closed by construction rather than by a branch.
func idOf(raw string) uuid.UUID {
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil
	}
	return parsed
}
