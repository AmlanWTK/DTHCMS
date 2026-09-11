package counseling

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// Counselling templates over HTTP (CP55).
//
// # Why publishing is not a PATCH
//
// Saving a draft is cheap and reversible. Publishing puts a checklist on every phone on the
// floor within seconds, freezes it forever, and retires whatever was there before. A single
// "save" endpoint with a `status` field would make those the same request, and the difference
// is the whole of criterion 2.
//
// So publishing has its own endpoint, its own permission, its own step-up, and its own audit
// entry. A physician who meant to fix a typo cannot accidentally do the other thing.
//
// # Why the audit entry rather than a clinical event
//
// A template is configuration, not a patient's record. It belongs in the security audit trail
// where role grants and credential resets live — the log somebody reviews when asking "who
// changed what" — rather than in the clinical ledger, which is about people.

const (
	PermRead    = "counseling.template.read"
	PermWrite   = "counseling.template.write"
	PermPublish = "counseling.template.publish"
	// PurposePublish is auth.PurposePublishCounseling. The contract test keeps them equal.
	PurposePublish = "counseling.publish"
)

// Publication is one template going live, described without a patient in it.
type Publication struct {
	FacilityID uuid.UUID
	ActorID    uuid.UUID
	ActorCode  string
	ActorRole  string

	Template string
	Version  int
	Items    int
}

// Auditor is how a publication reaches the security audit trail.
//
// An interface rather than the recorder itself, because `counseling` may not import `audit` —
// and the reason that rule exists is worth restating: a module able to write audit entries
// directly would grow a second, differently-shaped way of describing what happened, and the
// trail's value is that there is exactly one.
//
// Nil records nothing, which is what the contract test's handler carries.
type Auditor interface {
	TemplatePublished(ctx context.Context, p Publication) error
	GateOverridden(ctx context.Context, o GateOverride) error
}

// GateOverride is one patient sent past the counselling gate, on its way to the security audit
// trail (CP57 criterion 3).
//
// The reason travels; the item text does not. An audit row is read by people reviewing how often
// the valve is used, and what a patient was not counselled about is clinical detail that belongs
// on the physician's panel rather than in the security log.
type GateOverride struct {
	FacilityID uuid.UUID
	ActorID    uuid.UUID
	ActorCode  string
	ActorRole  string

	VisitID   uuid.UUID
	PatientID uuid.UUID

	Reason  string
	Missing int
}

type Handlers struct {
	store   *Store
	service *Service
	// sessions is the floor half (CP56). Separate from `service` because authoring and ticking
	// are different products that happen to share a vocabulary: one is a physician at a desk
	// now and then, the other is twelve stations all day.
	sessions *SessionService
	// visits answers "whose visit is this". `counseling` may import `visit`, so this is the
	// store rather than an interface — and it is read through rather than trusting a patient id
	// in a query string, which a phone could change to somebody else's.
	visits Visits
	// conditions is how a patient's coded conditions reach the assignment rules. An interface,
	// because they live in `history` and this module may not import it — see suggestions.go.
	conditions Conditions
	audit      Auditor
	stepUp     httpx.StepUpVerifier
	// clock is the one the override rate view measures its window against. Injected rather
	// than `time.Now()` for the reason CP54's assertion rates are: a test on a fixed clock and
	// a handler on the wall clock disagree about what "today" is, and the bug that hides is a
	// window that silently excludes the row somebody is asking about.
	clock  interface{ Now() time.Time }
	logger *slog.Logger
}

// Visits is the little of `visit` this package needs: which patient a visit belongs to.
type Visits interface {
	PatientOf(ctx context.Context, visit, facility uuid.UUID) (uuid.UUID, error)
}

// ErrNoVisit is what a Visits implementation's "no such visit" must be wrapped in, so the
// handlers can answer 404 without importing `visit` for one sentinel.
//
// Until CP74 there was no such contract: PatientOf's not-found came back as an opaque error
// and the checklist route answered **500** to a visit id that simply did not exist, which is
// the worst kind of wrong answer — it tells a counsellor the clinic's server is broken when
// the truth is that they followed a stale link.
var ErrNoVisit = errors.New("counseling: no such visit")

type HandlersConfig struct {
	Store      *Store
	Service    *Service
	Sessions   *SessionService
	Visits     Visits
	Conditions Conditions
	Audit      Auditor
	StepUp     httpx.StepUpVerifier
	Clock      interface{ Now() time.Time }
	Logger     *slog.Logger
}

func NewHandlers(cfg HandlersConfig) *Handlers {
	clk := cfg.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	return &Handlers{
		store: cfg.Store, service: cfg.Service, sessions: cfg.Sessions,
		visits: cfg.Visits, conditions: cfg.Conditions, audit: cfg.Audit,
		stepUp: cfg.StepUp, clock: clk, logger: cfg.Logger,
	}
}

// Mount attaches the template surface under /v1/counseling.
func (h *Handlers) Mount(r chi.Router) {
	read := httpx.Permission(PermRead)
	write := httpx.Permission(PermWrite)
	publish := httpx.Permission(PermPublish)

	r.Route("/counseling", func(c chi.Router) {
		// The rooms and their sequence (§5.2). Reference data a station app fetches once and
		// groups its screen by — the grouping *is* the flow.
		c.Method("GET", "/rooms", httpx.Declare(read, h.rooms))
		// Which diagnosis calls for which checklist. Read by the session start (CP56) and by
		// anybody wondering why a patient got the list they got.
		c.Method("GET", "/assignments", httpx.Declare(read, h.assignments))

		c.Route("/templates", func(t chi.Router) {
			t.Method("GET", "/", httpx.Declare(read, h.templates))
			t.Method("POST", "/", httpx.Declare(write, h.createTemplate))

			t.Route("/{templateId}", func(one chi.Router) {
				one.Method("GET", "/", httpx.Declare(read, h.template))
				one.Method("POST", "/versions", httpx.Declare(write, h.newVersion))
				one.Method("GET", "/versions/{version}", httpx.Declare(read, h.version))
				one.Method("PUT", "/versions/{version}", httpx.Declare(write, h.saveDraft))

				// Its own endpoint, its own permission, its own step-up. See the note above.
				stepped := httpx.RequireStepUp(h.logger, h.stepUp, PurposePublish)(
					http.HandlerFunc(h.publish))
				one.Method("POST", "/versions/{version}/publish",
					httpx.Declare(publish, stepped.ServeHTTP))
			})
		})

		// The floor half (CP56): sessions, ticks, un-ticks and completion.
		h.MountSessions(c)
		// The gate and its valve (CP57).
		h.MountGate(c)
	})
}

func (h *Handlers) rooms(w http.ResponseWriter, r *http.Request) {
	rooms, err := h.store.Rooms(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rooms": rooms})
}

func (h *Handlers) assignments(w http.ResponseWriter, r *http.Request) {
	// A coding may be supplied to ask "what would this diagnosis get", which is what a session
	// start does and what an author checking their rule wants.
	system := strings.TrimSpace(r.URL.Query().Get("system"))
	version := strings.TrimSpace(r.URL.Query().Get("version"))
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if system != "" && version != "" && code != "" {
		matches, err := h.store.For(r.Context(), strings.ToUpper(system), version, code)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"matches": matches})
		return
	}

	assignments, err := h.store.Assignments(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"assignments": assignments})
}

func (h *Handlers) templates(w http.ResponseWriter, r *http.Request) {
	templates, err := h.store.Templates(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"templates": templates})
}

func (h *Handlers) template(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "templateId")
	if !ok {
		return
	}
	versions, err := h.store.Versions(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	if len(versions) == 0 {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"versions": versions})
}

func (h *Handlers) version(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "templateId")
	if !ok {
		return
	}
	number, ok := h.versionParam(w, r)
	if !ok {
		return
	}
	version, err := h.store.Version(r.Context(), id, number)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"version": version})
}

type createTemplateRequest struct {
	Code    string `json:"code"`
	TitleEN string `json:"title_en"`
	TitleBN string `json:"title_bn"`
}

func (h *Handlers) createTemplate(w http.ResponseWriter, r *http.Request) {
	var body createTemplateRequest
	if !h.decode(w, r, &body) {
		return
	}
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	template, err := h.service.CreateTemplate(r.Context(),
		body.Code, body.TitleEN, body.TitleBN, actor)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"template": template})
}

type newVersionRequest struct {
	Notes string `json:"notes,omitempty"`
}

func (h *Handlers) newVersion(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "templateId")
	if !ok {
		return
	}
	var body newVersionRequest
	if !h.decode(w, r, &body) {
		return
	}
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	version, err := h.service.NewVersion(r.Context(), id, body.Notes, actor)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"version": version})
}

type saveDraftRequest struct {
	Notes string `json:"notes,omitempty"`
	Items []struct {
		ItemCode   string `json:"item_code"`
		Ordering   int    `json:"ordering,omitempty"`
		TextEN     string `json:"text_en"`
		TextBN     string `json:"text_bn"`
		GuidanceEN string `json:"guidance_en,omitempty"`
		GuidanceBN string `json:"guidance_bn,omitempty"`
		Mandatory  bool   `json:"mandatory"`
		Room       string `json:"room"`
	} `json:"items"`
}

func (h *Handlers) saveDraft(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "templateId")
	if !ok {
		return
	}
	number, ok := h.versionParam(w, r)
	if !ok {
		return
	}
	var body saveDraftRequest
	if !h.decode(w, r, &body) {
		return
	}

	draft := Draft{Notes: body.Notes, Items: make([]DraftItem, 0, len(body.Items))}
	for _, item := range body.Items {
		draft.Items = append(draft.Items, DraftItem{
			ItemCode: item.ItemCode, Ordering: item.Ordering,
			TextEN: item.TextEN, TextBN: item.TextBN,
			GuidanceEN: item.GuidanceEN, GuidanceBN: item.GuidanceBN,
			Mandatory: item.Mandatory, Room: item.Room,
		})
	}
	version, err := h.service.SaveDraft(r.Context(), id, number, draft)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"version": version})
}

func (h *Handlers) publish(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "templateId")
	if !ok {
		return
	}
	number, ok := h.versionParam(w, r)
	if !ok {
		return
	}
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	version, err := h.service.Publish(r.Context(), id, number, actor)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}

	// Audited, because publishing is the act that changes what every counsellor asks every
	// patient from this second onwards. Recorded after the write rather than before: an audit
	// entry for a publish that then failed is a log that says something happened which did not.
	if h.audit != nil {
		if principal, ok := httpx.PrincipalFrom(r.Context()); ok {
			facility, _ := uuid.Parse(principal.FacilityID)
			user, _ := uuid.Parse(principal.UserID)
			code := id.String()
			if template, terr := h.store.templateByID(r.Context(), id); terr == nil {
				code = template.Code
			}
			if err := h.audit.TemplatePublished(r.Context(), Publication{
				FacilityID: facility, ActorID: user,
				ActorCode: principal.Code, ActorRole: principal.Role,
				Template: code, Version: number, Items: len(version.Items),
			}); err != nil {
				// A failed audit write does not un-publish the template, and pretending it
				// does would be worse. Logged, and the entry's absence is itself detectable —
				// the trail is hash-chained.
				h.logger.ErrorContext(r.Context(), "recording a template publication",
					"error", err, "template", code, "version", number)
			}
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"version": version})
}

// --- the small shared pieces ---

func (s *Store) templateByID(ctx context.Context, id uuid.UUID) (Template, error) {
	row, err := s.q.CounselingTemplateByID(ctx, id)
	if err != nil {
		return Template{}, err
	}
	return Template{ID: row.ID, Code: row.Code, TitleEN: row.TitleEn, TitleBN: row.TitleBn}, nil
}

func (h *Handlers) decode(w http.ResponseWriter, r *http.Request, into any) bool {
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrBadRequest.WithDetail(err))
		return false
	}
	return true
}

func (h *Handlers) uuidParam(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	parsed, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return uuid.Nil, false
	}
	return parsed, true
}

func (h *Handlers) versionParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	parsed, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil || parsed < 1 {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return 0, false
	}
	return parsed, true
}

func (h *Handlers) actor(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	principal, present := httpx.PrincipalFrom(r.Context())
	if !present {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return uuid.Nil, false
	}
	parsed, err := uuid.Parse(principal.UserID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated.WithDetail(err))
		return uuid.Nil, false
	}
	return parsed, true
}

func translate(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errs.ErrNotFound
	case errors.Is(err, ErrNotDraft):
		// 409 rather than 422: the request is well-formed and the author is allowed to make
		// it; the version simply moved on under them, which is a state conflict.
		return errs.ErrConflict.WithDetail(err)
	case errors.Is(err, ErrDuplicateCode):
		return errs.ErrValidation.WithFieldIn("code",
			"A template with that code already exists.",
			"ওই কোডে একটি টেমপ্লেট আগে থেকেই আছে।")
	case errors.Is(err, ErrNotBilingual):
		return errs.ErrValidation.WithFieldIn("items",
			"Every item must read in both languages before publishing.",
			"প্রকাশের আগে প্রতিটি বিষয় দুই ভাষাতেই লিখতে হবে।")
	case errors.Is(err, ErrEmpty):
		return errs.ErrValidation.WithFieldIn("items",
			"A checklist with no items cannot be published.",
			"বিষয়বিহীন তালিকা প্রকাশ করা যায় না।")
	case errors.Is(err, ErrUnknownRoom):
		return errs.ErrValidation.WithFieldIn("room",
			"That is not one of the counselling rooms.",
			"এটি কাউন্সেলিং রুমগুলোর একটি নয়।")
	default:
		return errs.From(err)
	}
}
