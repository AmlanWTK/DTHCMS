package history

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// Medical history over HTTP (CP53).
//
// # Why the acts are separate endpoints
//
// Confirming, amending and removing are three different claims about an item, and a single
// PUT that accepted a whole item would make them one. Criterion 3 depends on the difference:
// "somebody said this is still true" and "the software carried it forward" have to be
// distinguishable in the record, and they are only distinguishable if confirming is its own
// request with its own event.
//
// # Why there is no batch confirm
//
// It is the obvious convenience and it is the one this checkpoint must not offer. A screen
// with a "confirm all" button produces one action from a person and twenty assertions in the
// record — which is exactly the auto-acceptance criterion 3 forbids, wearing a person's name.
// Twenty items is twenty requests, and a station app can send them as fast as it likes.

const (
	PermRead    = "history.read"
	PermWrite   = "history.write"
	PermConfirm = "history.confirm"
)

// readers is the union that may look at the catalogue of kinds. Reference data with no
// patient in it, so anybody who works with a history may fetch it — but not the world: a
// screen that cannot read or write a history has no use for the shape of one.
var readers = []string{PermRead, PermWrite, PermConfirm}

type Handlers struct {
	service *Service
	store   *Store
	logger  *slog.Logger
}

type HandlersConfig struct {
	Service *Service
	Store   *Store
	Logger  *slog.Logger
}

func NewHandlers(cfg HandlersConfig) *Handlers {
	return &Handlers{service: cfg.Service, store: cfg.Store, logger: cfg.Logger}
}

// Mount attaches the catalogue and the per-item acts under /v1/history.
func (h *Handlers) Mount(r chi.Router) {
	r.Route("/history", func(hr chi.Router) {
		// The six kinds, their rules, and who a family history can be about. Fetched once by
		// a station app, which then renders the right fields per kind — the thing that stops
		// a screen asking for a relation on a complaint.
		hr.Method("GET", "/kinds", httpx.Declare(httpx.Permission(readers...), h.kinds))
		// How much of the record could not be coded. The number that keeps the uncoded
		// escape hatch honest: if it grows, the catalogue is wrong rather than the officers.
		hr.Method("GET", "/uncoded", httpx.Declare(httpx.Permission(PermRead), h.uncoded))

		hr.Route("/items/{itemId}", func(it chi.Router) {
			it.Method("GET", "/", httpx.Declare(httpx.Permission(PermRead), h.item))
			it.Method("POST", "/confirm",
				httpx.Declare(httpx.Permission(PermConfirm), h.confirm))
			it.Method("PATCH", "/", httpx.Declare(httpx.Permission(PermWrite), h.amend))
			it.Method("POST", "/remove", httpx.Declare(httpx.Permission(PermWrite), h.remove))
		})
	})
}

// MountPatient hangs the per-patient list and the write off the patient record, where the
// consent and visit sub-routes already hang.
func (h *Handlers) MountPatient(r chi.Router) {
	r.Route("/{id}/medical-history", func(m chi.Router) {
		// Not `/history`: that path is already the patient's demographic corrections, and two
		// different histories under one word is how somebody opens the wrong one in a hurry.
		m.Method("GET", "/", httpx.Declare(httpx.Permission(PermRead), h.forPatient))
		m.Method("POST", "/", httpx.Declare(httpx.Permission(PermWrite), h.record))
	})
}

func (h *Handlers) kinds(w http.ResponseWriter, r *http.Request) {
	kinds, err := h.store.Kinds(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	relations, err := h.store.Relations(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"kinds":     kinds,
		"relations": relations,
		// The plan asks that smoking and alcohol be "carried from the lifestyle station
		// without duplicate entry", and the honest way to carry something is not to copy it.
		// These are CP42 observation codes owned by station 6; station 4 shows them from
		// /v1/patients/{id}/observations and never asks for them again. Naming them here is
		// what stops a history screen growing its own smoking field, which would be two
		// answers to one question with no way to tell which is current.
		"from_lifestyle_station": []string{"PACK_YEARS"},
	})
}

func (h *Handlers) uncoded(w http.ResponseWriter, r *http.Request) {
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	counts, err := h.store.Uncoded(r.Context(), actor.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"uncoded": counts})
}

func (h *Handlers) forPatient(w http.ResponseWriter, r *http.Request) {
	patient, ok := h.uuidParam(w, r, "id")
	if !ok {
		return
	}
	items, err := h.store.ForPatient(r.Context(), patient)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handlers) item(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "itemId")
	if !ok {
		return
	}
	item, err := h.store.ByID(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"item": item})
}

type recordRequest struct {
	EventID string `json:"event_id"`
	VisitID string `json:"visit_id,omitempty"`
	Kind    string `json:"kind"`

	CodeSystem  string `json:"code_system,omitempty"`
	CodeVersion string `json:"code_version,omitempty"`
	Code        string `json:"code,omitempty"`
	Said        string `json:"said,omitempty"`

	Relation       string `json:"relation,omitempty"`
	DurationDays   *int   `json:"duration_days,omitempty"`
	Severity       string `json:"severity,omitempty"`
	OnsetOn        string `json:"onset_on,omitempty"`
	OnsetPrecision string `json:"onset_precision,omitempty"`

	Dose      string `json:"dose,omitempty"`
	Frequency string `json:"frequency,omitempty"`
}

func (h *Handlers) record(w http.ResponseWriter, r *http.Request) {
	patient, ok := h.uuidParam(w, r, "id")
	if !ok {
		return
	}
	var body recordRequest
	if !h.decode(w, r, &body) {
		return
	}
	eventID, ok := h.eventID(w, r, body.EventID)
	if !ok {
		return
	}
	visit, ok := h.optionalUUID(w, r, body.VisitID, "visit_id")
	if !ok {
		return
	}

	item, err := h.service.Record(r.Context(), Recording{
		EventID: eventID, PatientID: patient, VisitID: visit, Kind: body.Kind,
		CodeSystem: body.CodeSystem, CodeVersion: body.CodeVersion, Code: body.Code,
		Said:     body.Said,
		Relation: body.Relation, DurationDays: body.DurationDays, Severity: body.Severity,
		OnsetOn: body.OnsetOn, OnsetPrecision: body.OnsetPrecision,
		Dose: body.Dose, Frequency: body.Frequency,
		LedgerSource: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"item": item})
}

type confirmRequest struct {
	EventID string `json:"event_id"`
	VisitID string `json:"visit_id,omitempty"`
}

func (h *Handlers) confirm(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "itemId")
	if !ok {
		return
	}
	var body confirmRequest
	if !h.decode(w, r, &body) {
		return
	}
	eventID, ok := h.eventID(w, r, body.EventID)
	if !ok {
		return
	}
	visit, ok := h.optionalUUID(w, r, body.VisitID, "visit_id")
	if !ok {
		return
	}
	item, err := h.service.Confirm(r.Context(), eventID, id, visit, sourceOf(r))
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"item": item})
}

type amendRequest struct {
	EventID string `json:"event_id"`
	VisitID string `json:"visit_id,omitempty"`

	// Pointers all the way down: a body that omits `severity` leaves it alone, and one that
	// sends "" clears it. Collapsing the two would make a field impossible to empty.
	Said           *string `json:"said,omitempty"`
	Severity       *string `json:"severity,omitempty"`
	DurationDays   *int    `json:"duration_days,omitempty"`
	OnsetOn        *string `json:"onset_on,omitempty"`
	OnsetPrecision *string `json:"onset_precision,omitempty"`
	Dose           *string `json:"dose,omitempty"`
	Frequency      *string `json:"frequency,omitempty"`
	Status         *string `json:"status,omitempty"`

	FormularyProductID *string `json:"formulary_product_id,omitempty"`
	Reconciliation     *string `json:"reconciliation,omitempty"`
}

func (h *Handlers) amend(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "itemId")
	if !ok {
		return
	}
	var body amendRequest
	if !h.decode(w, r, &body) {
		return
	}
	eventID, ok := h.eventID(w, r, body.EventID)
	if !ok {
		return
	}
	visit, ok := h.optionalUUID(w, r, body.VisitID, "visit_id")
	if !ok {
		return
	}
	item, err := h.service.Amend(r.Context(), Amendment{
		EventID: eventID, ItemID: id, VisitID: visit,
		Said: body.Said, Severity: body.Severity, DurationDays: body.DurationDays,
		OnsetOn: body.OnsetOn, OnsetPrecision: body.OnsetPrecision,
		Dose: body.Dose, Frequency: body.Frequency, Status: body.Status,
		FormularyProductID: body.FormularyProductID, Reconciliation: body.Reconciliation,
		LedgerSource: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"item": item})
}

type removeRequest struct {
	EventID string `json:"event_id"`
	VisitID string `json:"visit_id,omitempty"`
	Reason  string `json:"reason"`
}

func (h *Handlers) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "itemId")
	if !ok {
		return
	}
	var body removeRequest
	if !h.decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Reason) == "" {
		// The reason is the point of the endpoint. An item removed for no reason cannot be
		// reviewed, and the difference between a correction and a mistake is what it says.
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("reason",
			"Say why this should not have been recorded.",
			"কেন এটি লেখা হওয়া উচিত ছিল না, তা জানান।"))
		return
	}
	eventID, ok := h.eventID(w, r, body.EventID)
	if !ok {
		return
	}
	visit, ok := h.optionalUUID(w, r, body.VisitID, "visit_id")
	if !ok {
		return
	}
	if err := h.service.Remove(r.Context(), eventID, id, body.Reason, visit,
		sourceOf(r)); err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- the small shared pieces ---

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

// eventID is the client's idempotency key. Optional: a browser has no offline queue and
// nothing to replay, so one is generated. A tablet sends its own, which is what makes a
// history taken over a bad connection produce one complaint rather than four.
func (h *Handlers) eventID(w http.ResponseWriter, r *http.Request, raw string) (uuid.UUID, bool) {
	if strings.TrimSpace(raw) == "" {
		return uuid.New(), true
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("event_id",
			"That is not an event id.", "এটি বৈধ ইভেন্ট আইডি নয়।"))
		return uuid.Nil, false
	}
	return parsed, true
}

func (h *Handlers) optionalUUID(w http.ResponseWriter, r *http.Request,
	raw, field string) (*uuid.UUID, bool) {

	if strings.TrimSpace(raw) == "" {
		return nil, true
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn(field,
			"That is not an identifier.", "এটি বৈধ আইডি নয়।"))
		return nil, false
	}
	return &parsed, true
}

func sourceOf(r *http.Request) eventstore.Source {
	if strings.TrimSpace(r.Header.Get("X-DTHCMS-Device")) != "" {
		return eventstore.SourceMobileOnline
	}
	return eventstore.SourceWeb
}

// translate turns the module's refusals into answers an officer can act on.
//
// Each per-kind rule gets its own field, because "this needs a duration" and "this kind has
// no severity" send a person to two different places on the form — and a screen that could
// only say "invalid" would be one people learn to fight rather than read.
func translate(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errs.ErrNotFound
	case errors.Is(err, ErrRemoved):
		return errs.ErrConflict
	case errors.Is(err, ErrUnknownKind):
		return errs.ErrValidation.WithFieldIn("kind",
			"That is not a kind of history this clinic records.",
			"এই ক্লিনিক ওই ধরনের ইতিহাস রাখে না।")
	case errors.Is(err, ErrPartialCoding):
		return errs.ErrValidation.WithFieldIn("code",
			"A code needs its terminology and version too.",
			"কোডের সঙ্গে টার্মিনোলজি ও সংস্করণও দিতে হবে।")
	case errors.Is(err, ErrNothingSaid):
		return errs.ErrValidation.WithFieldIn("said",
			"Choose a code, or write what the patient said.",
			"একটি কোড বাছুন, অথবা রোগী যা বলেছেন তা লিখুন।")
	case errors.Is(err, ErrWrongCatalogue):
		return errs.ErrValidation.WithFieldIn("code",
			"That code is from a different catalogue than this kind of history uses.",
			"এই ধরনের ইতিহাসের জন্য ওই তালিকার কোড নয়।")
	case errors.Is(err, ErrNeedsRelation):
		return errs.ErrValidation.WithFieldIn("relation",
			"Family history is about a relative. Say who.",
			"পরিবারের ইতিহাস কার, তা জানান।")
	case errors.Is(err, ErrNeedsDuration):
		return errs.ErrValidation.WithFieldIn("duration_days",
			"Say how long this has been going on.",
			"কত দিন ধরে হচ্ছে, তা জানান।")
	case errors.Is(err, ErrNoSeverity):
		return errs.ErrValidation.WithFieldIn("severity",
			"This kind of history carries no severity.",
			"এই ধরনের ইতিহাসে তীব্রতা লেখা হয় না।")
	case errors.Is(err, ErrNoOnset):
		return errs.ErrValidation.WithFieldIn("onset_on",
			"This kind of history carries no start date.",
			"এই ধরনের ইতিহাসে শুরুর তারিখ লেখা হয় না।")
	case errors.Is(err, ErrOnsetPartial):
		return errs.ErrValidation.WithFieldIn("onset_precision",
			"Say how exact the start date is: a day, a month or a year.",
			"শুরুর তারিখ কতটা নিশ্চিত — দিন, মাস না বছর — তা জানান।")
	case errors.Is(err, ErrNoDose):
		return errs.ErrValidation.WithFieldIn("dose",
			"Only a medicine carries a dose.",
			"শুধু ওষুধের ক্ষেত্রে মাত্রা লেখা হয়।")
	// A history is a clinical record, so the same device rule applies to writing one as to
	// writing an observation [R-03]: the event's device id is evidence, and a browser session
	// cannot supply one until D-71 is settled. Its own status code, so a screen can say why
	// rather than "no".
	case errors.Is(err, eventstore.ErrNoDevice):
		return errs.ErrDeviceRequired.WithDetail(err)
	case errors.Is(err, eventstore.ErrNoRole):
		return errs.ErrForbidden.WithDetail(err)
	case errors.Is(err, eventstore.ErrNoPrincipal):
		return errs.ErrUnauthenticated.WithDetail(err)
	default:
		return errs.From(err)
	}
}
