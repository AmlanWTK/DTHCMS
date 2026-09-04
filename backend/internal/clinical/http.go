package clinical

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// Observations over HTTP (CP42).
//
// # One write endpoint, and why the permission is a variable
//
// Every station posts to the same place. The permission each post needs is **the one the
// code declares** — `observation.write.anthro` for a height, `observation.write.vitals` for
// a blood pressure — which cannot be expressed as a constant on a route.
//
// So the route declares the union of the write permissions (a caller with none of them has
// no business here at all, and the route guard says so) and the handler then checks the
// specific one the code requires. Both are needed: the route keeps unrelated roles out
// entirely, and the per-code check is what makes "the nutritionist writes diet-related
// values and not vitals" true.

const (
	PermObservationRead     = "observation.read.values"
	PermWriteAnthro         = "observation.write.anthro"
	PermWriteVitals         = "observation.write.vitals"
	PermWriteLifestyle      = "observation.write.lifestyle"
	PermWriteHistory        = "observation.write.history"
	PermWriteNutrition      = "observation.write.nutrition"
	PermWriteExercise       = "observation.write.exercise"
	PermCorrectionRequested = "observation.correct.request"
)

// writePermissions is the union the route guard asks for. Every code's own permission is one
// of these; `TestEveryCodeDeclaresAKnownPermission` keeps the two in step.
var writePermissions = []string{
	PermWriteAnthro, PermWriteVitals, PermWriteLifestyle,
	PermWriteHistory, PermWriteNutrition, PermWriteExercise,
}

type Handlers struct {
	service *Service
	store   *Store
	clock   interface{ Now() time.Time }
	logger  *slog.Logger
}

type HandlersConfig struct {
	Service *Service
	Store   *Store
	Clock   interface{ Now() time.Time }
	Logger  *slog.Logger
}

func NewHandlers(cfg HandlersConfig) *Handlers {
	return &Handlers{service: cfg.Service, store: cfg.Store, clock: cfg.Clock, logger: cfg.Logger}
}

// Mount attaches the registry and the write endpoint under /v1/observations.
func (h *Handlers) Mount(r chi.Router) {
	read := httpx.Permission(PermObservationRead)
	write := httpx.Permission(writePermissions...)
	r.Route("/observations", func(o chi.Router) {
		// The registry. Every signed-in clinical role may read it: it is reference data, it
		// contains no patient, and a station app fetches it once and then validates offline.
		o.Method("GET", "/codes", httpx.Declare(read, h.codes))
		o.Method("GET", "/units", httpx.Declare(read, h.units))
		o.Method("POST", "/", httpx.Declare(write, h.record))
		// Deriving is a write of a DERIVED value, and the codes that carry one declare an
		// existing write permission — so the same union guards it. What it does *not* accept
		// is a number: the server computes, from values already in the record (CP43).
		o.Method("POST", "/derive", httpx.Declare(write, h.derive))
		o.Method("GET", "/{id}", httpx.Declare(read, h.byID))
	})
}

// MountPatient hangs the per-patient reads off a patient, through CP36's `Sub` hook — so
// that `patient` still does not know this module exists.
func (h *Handlers) MountPatient(p chi.Router) {
	read := httpx.Permission(PermObservationRead)
	p.Method("GET", "/{id}/observations", httpx.Declare(read, h.forPatient))
	p.Method("GET", "/{id}/observations/{code}/history", httpx.Declare(read, h.history))
}

func (h *Handlers) codes(w http.ResponseWriter, r *http.Request) {
	codes, err := h.store.Registry(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"codes": codes})
}

func (h *Handlers) units(w http.ResponseWriter, r *http.Request) {
	units, err := h.store.Units(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"units": units})
}

type recordRequest struct {
	EventID        string          `json:"event_id"`
	PatientID      string          `json:"patient_id"`
	VisitID        string          `json:"visit_id,omitempty"`
	EncounterID    string          `json:"encounter_id,omitempty"`
	Code           string          `json:"code"`
	Value          *float64        `json:"value,omitempty"`
	Unit           string          `json:"unit,omitempty"`
	ValueText      string          `json:"value_text,omitempty"`
	ValueBool      *bool           `json:"value_bool,omitempty"`
	ValueCode      string          `json:"value_code,omitempty"`
	ValueJSON      json.RawMessage `json:"value_json,omitempty"`
	EffectiveAt    string          `json:"effective_at,omitempty"`
	Source         string          `json:"source,omitempty"`
	Note           string          `json:"note,omitempty"`
	Replaces       string          `json:"replaces,omitempty"`
	ReplacedStatus string          `json:"replaced_status,omitempty"`
}

func (h *Handlers) record(w http.ResponseWriter, r *http.Request) {
	var req recordRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	eventID, bad := requiredUUID(req.EventID, "event_id")
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}
	patientID, bad := requiredUUID(req.PatientID, "patient_id")
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}

	// The per-code permission. Checked here rather than on the route because it depends on
	// the body — see the note at the top of the file.
	spec, _, err := h.store.CodeByCode(r.Context(), strings.TrimSpace(req.Code))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	if spec.Code == "" {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("code",
			"That is not an observation code.", "এটি কোনো পর্যবেক্ষণ কোড নয়।"))
		return
	}
	principal, ok := httpx.PrincipalFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	if !roleGrants(principal.Role, spec.WritePermission) {
		// A 403 that says which permission, because the operator can act on that: it is
		// usually the wrong hat rather than the wrong person (CP41).
		httpx.WriteError(w, r, h.logger, errs.ErrForbidden.WithDetail(
			errors.New("clinical: recording "+spec.Code+" needs "+spec.WritePermission)))
		return
	}

	in := Recording{
		EventID: eventID, PatientID: patientID, Code: spec.Code,
		Value: req.Value, Unit: strings.TrimSpace(req.Unit),
		ValueText: req.ValueText, ValueBool: req.ValueBool,
		ValueCode: strings.TrimSpace(req.ValueCode), ValueJSON: req.ValueJSON,
		Source:       Source(strings.ToUpper(strings.TrimSpace(req.Source))),
		LedgerSource: sourceOf(r), Note: req.Note,
		ReplacedStatus: Status(strings.ToUpper(strings.TrimSpace(req.ReplacedStatus))),
	}
	if in.Source == "" {
		// A station operator with a tablet in their hand is the ordinary case, and making
		// them say so on every write is a field nobody fills in correctly.
		in.Source = Station
	}
	if req.VisitID != "" {
		id, bad := requiredUUID(req.VisitID, "visit_id")
		if bad != nil {
			httpx.WriteError(w, r, h.logger, bad)
			return
		}
		in.VisitID = &id
	}
	if req.EncounterID != "" {
		id, bad := requiredUUID(req.EncounterID, "encounter_id")
		if bad != nil {
			httpx.WriteError(w, r, h.logger, bad)
			return
		}
		in.EncounterID = &id
	}
	if req.Replaces != "" {
		id, bad := requiredUUID(req.Replaces, "replaces")
		if bad != nil {
			httpx.WriteError(w, r, h.logger, bad)
			return
		}
		in.Replaces = &id
		if in.ReplacedStatus == "" {
			// Correcting is the commoner of the two and the safer default: calling a
			// re-measurement a correction overstates the error rate, while calling a
			// correction a re-measurement hides one.
			in.ReplacedStatus = Corrected
		}
		if !roleGrants(principal.Role, PermCorrectionRequested) && in.ReplacedStatus == Corrected {
			httpx.WriteError(w, r, h.logger, errs.ErrForbidden.WithDetail(
				errors.New("clinical: correcting a recorded value needs "+PermCorrectionRequested)))
			return
		}
	}

	in.EffectiveAt = h.clock.Now().UTC()
	if req.EffectiveAt != "" {
		parsed, err := time.Parse(time.RFC3339, req.EffectiveAt)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("effective_at",
				"Use a time like 2026-09-14T09:05:00Z.",
				"2026-09-14T09:05:00Z এর মতো একটি সময় দিন।"))
			return
		}
		in.EffectiveAt = parsed.UTC()
	}

	observation, err := h.service.Record(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"observation": observation})
}

type deriveRequest struct {
	EventID   string `json:"event_id"`
	PatientID string `json:"patient_id"`
	VisitID   string `json:"visit_id,omitempty"`
	What      string `json:"what"`
}

func (h *Handlers) derive(w http.ResponseWriter, r *http.Request) {
	var req deriveRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	eventID, bad := requiredUUID(req.EventID, "event_id")
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}
	patientID, bad := requiredUUID(req.PatientID, "patient_id")
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}
	what := Derivable(strings.ToUpper(strings.TrimSpace(req.What)))
	if !derivable(what) {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("what",
			"That is not a value this server derives.",
			"এই মানটি সার্ভার হিসাব করে না।"))
		return
	}

	in := Derivation{
		EventID: eventID, PatientID: patientID, What: what,
		// This clinic. The library serves both scales and CP44's display shows which was
		// used, so the choice is visible rather than buried.
		AsianScale: true, LedgerSource: sourceOf(r),
	}
	if req.VisitID != "" {
		id, bad := requiredUUID(req.VisitID, "visit_id")
		if bad != nil {
			httpx.WriteError(w, r, h.logger, bad)
			return
		}
		in.VisitID = &id
	}

	observation, err := h.service.Derive(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"observation": observation})
}

func derivable(what Derivable) bool {
	for _, known := range Derivables {
		if known == what {
			return true
		}
	}
	return false
}

func (h *Handlers) byID(w http.ResponseWriter, r *http.Request) {
	id, ok := h.idParam(w, r, "id")
	if !ok {
		return
	}
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	observation, err := h.store.ByID(r.Context(), id, actor.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"observation": observation})
}

func (h *Handlers) forPatient(w http.ResponseWriter, r *http.Request) {
	id, ok := h.idParam(w, r, "id")
	if !ok {
		return
	}
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	category := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("category")))
	if category != "" && !knownCategory(category) {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("category",
			"That is not one of the seven categories.", "এটি সাতটি শ্রেণির একটিও নয়।"))
		return
	}
	rows, err := h.store.ForPatient(r.Context(), id, actor.FacilityID(), category, limitOf(r))
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"observations": rows})
}

func (h *Handlers) history(w http.ResponseWriter, r *http.Request) {
	id, ok := h.idParam(w, r, "id")
	if !ok {
		return
	}
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	rows, err := h.store.History(r.Context(), id, actor.FacilityID(),
		strings.TrimSpace(chi.URLParam(r, "code")), limitOf(r))
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"observations": rows})
}

// --- helpers ---

func (h *Handlers) idParam(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		// A malformed id and an id belonging to another facility answer the same way. A 404
		// that distinguished them would be an existence oracle.
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return uuid.Nil, false
	}
	return id, true
}

func requiredUUID(raw, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return uuid.Nil, errs.ErrValidation.WithFieldIn(field,
			"A UUID is required.", "একটি UUID আবশ্যক।")
	}
	return id, nil
}

func limitOf(r *http.Request) int {
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			return n
		}
	}
	return 0
}

func knownCategory(c string) bool {
	for _, category := range Categories {
		if string(category) == c {
			return true
		}
	}
	return false
}

// roleGrants asks whether the hat being worn confers a permission.
//
// The **active role's** permissions, not the union across every role the person holds. That
// distinction is the whole of [R-02]: an operator who holds both the anthropometry and the
// clinical-assistant roles must not be able to record a blood pressure while wearing the
// anthropometry hat, because the event would then be attributed to a role that is not
// allowed to have taken it.
//
// It reads the same catalogue the authorisation engine reads, so the route guard and this
// check cannot drift. `rbac.Holds` is not used because it needs a Subject, which needs a
// database read this handler has already had the engine do — the role on the principal is
// the engine's own answer, already verified.
func roleGrants(role, permission string) bool {
	return rbac.RoleGrants(role, permission)
}

// sourceOf is which surface wrote the event, from the request. Never from the body: a
// client that could name its own surface could name somebody else's.
func sourceOf(r *http.Request) eventstore.Source {
	if strings.TrimSpace(r.Header.Get("X-DTHCMS-Device")) != "" {
		return eventstore.SourceMobileOnline
	}
	return eventstore.SourceWeb
}

// translate turns the module's refusals into answers an operator can act on.
//
// The unit ones carry their own sentence rather than the generic validation message,
// because "this needs a unit" and "that unit measures something else" send a person to two
// different places on the form.
func translate(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errs.ErrNotFound
	case errors.Is(err, ErrUnitRequired):
		return errs.ErrValidation.WithFieldIn("unit",
			"Say which unit this was measured in.", "কোন এককে মাপা হয়েছে তা জানান।")
	case errors.Is(err, ErrUnitNotAllowed):
		return errs.ErrValidation.WithFieldIn("unit",
			"This value takes no unit.", "এই মানের কোনো একক হয় না।")
	case errors.Is(err, ErrWrongDimension):
		return errs.ErrValidation.WithFieldIn("unit",
			"That unit does not measure this.", "এই একক দিয়ে এটি মাপা হয় না।")
	case errors.Is(err, ErrImplausible):
		return errs.ErrValidation.WithFieldIn("value",
			"That value is outside the plausible range — check the number.",
			"এই মানটি সম্ভাব্য সীমার বাইরে — সংখ্যাটি দেখুন।")
	case errors.Is(err, ErrWrongShape):
		return errs.ErrValidation.WithFieldIn("value",
			"That is not the kind of value this observation takes.",
			"এই পর্যবেক্ষণে এই ধরনের মান হয় না।")
	case errors.Is(err, ErrUnknownCode), errors.Is(err, ErrRetiredCode):
		return errs.ErrValidation.WithFieldIn("code",
			"That is not an observation code that can be recorded.",
			"এই কোডে এখন কিছু লেখা যায় না।")
	case errors.Is(err, ErrInputsMissing):
		// Distinct from a refusal by the formula. "We have not measured their height" tells
		// an operator what to go and do; "that height cannot be right" tells them to look at
		// a field. Conflating them would send half the people to the wrong place.
		return errs.ErrValidation.WithFieldIn("what",
			"The values this is computed from have not been recorded yet.",
			"যেসব মান থেকে এটি হিসাব হয়, সেগুলো এখনো লেখা হয়নি।")
	case errors.Is(err, ErrCannotCompute):
		return errs.ErrValidation.WithFieldIn("what",
			"That cannot be computed from the values on record.",
			"রেকর্ডে থাকা মান থেকে এটি হিসাব করা যাচ্ছে না।")
	case errors.Is(err, ErrAlreadyReplaced):
		return errs.ErrConflict.WithDetail(err)
	case errors.Is(err, eventstore.ErrNoDevice):
		return errs.ErrForbidden.WithDetail(err)
	case errors.Is(err, eventstore.ErrNoRole):
		return errs.ErrForbidden.WithDetail(err)
	default:
		return errs.ErrInternal.WithDetail(err)
	}
}
