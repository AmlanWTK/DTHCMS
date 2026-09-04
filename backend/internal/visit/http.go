package visit

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// Visits over HTTP (CP38).
//
// The shape is the journey: open, arrive, depart, close. Each is its own permission —
// `visit.open` for the registration desk, `visit.attend` for a station, `visit.close` for the
// physician and QA — because reusing `patient.write.demographics` would mean a physician
// closing a visit needs the permission to rewrite a name.

const (
	PermVisitOpen   = "visit.open"
	PermVisitClose  = "visit.close"
	PermVisitRead   = "visit.read"
	PermVisitAttend = "visit.attend"
)

type Handlers struct {
	service *Service
	store   *Store
	clock   clock.Clock
	logger  *slog.Logger
}

type HandlersConfig struct {
	Service *Service
	Store   *Store
	Clock   clock.Clock
	Logger  *slog.Logger
}

func NewHandlers(cfg HandlersConfig) *Handlers {
	h := &Handlers{service: cfg.Service, store: cfg.Store, clock: cfg.Clock, logger: cfg.Logger}
	if h.clock == nil {
		h.clock = clock.Real{}
	}
	if h.logger == nil {
		h.logger = slog.Default()
	}
	return h
}

func (h *Handlers) Mount(r chi.Router) {
	r.Route("/visits", func(v chi.Router) {
		v.Method("POST", "/", httpx.Declare(httpx.Permission(PermVisitOpen), h.open))
		v.Method("GET", "/today", httpx.Declare(httpx.Permission(PermVisitRead), h.today))
		v.Method("GET", "/{id}", httpx.Declare(httpx.Permission(PermVisitRead), h.byID))
		v.Method("POST", "/{id}/close", httpx.Declare(httpx.Permission(PermVisitClose), h.close))
		v.Method("POST", "/{id}/abandon", httpx.Declare(httpx.Permission(PermVisitClose), h.abandon))
		// Reopening needs the permission to close, not the permission to open: it undoes a
		// close, and the authority that should hold it is the one that made the decision.
		v.Method("POST", "/{id}/reopen", httpx.Declare(httpx.Permission(PermVisitClose), h.reopen))
		v.Method("POST", "/{id}/encounters", httpx.Declare(httpx.Permission(PermVisitAttend), h.arrive))
		v.Method("POST", "/{id}/encounters/{encounterId}/finish",
			httpx.Declare(httpx.Permission(PermVisitAttend), h.depart))
		h.mountQueue(v)
	})
}

// MountPatient attaches the per-patient views. Called by the patient module's Sub hook, so a
// patient does not have to know that visits exist.
func (h *Handlers) MountPatient(p chi.Router) {
	p.Method("GET", "/{id}/visits", httpx.Declare(httpx.Permission(PermVisitRead), h.forPatient))
}

// --- opening ---

type openRequest struct {
	EventID        string `json:"event_id"`
	PatientID      string `json:"patient_id"`
	VisitType      string `json:"visit_type"`
	ChiefComplaint string `json:"chief_complaint"`
}

func (h *Handlers) open(w http.ResponseWriter, r *http.Request) {
	var req openRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	eventID, bad := requiredUUID(req.EventID, "event_id",
		"A client-generated UUID is required so a retry does not open a second visit.",
		"পুনরায় পাঠালে যেন দ্বিতীয় ভিজিট তৈরি না হয়, সে জন্য ক্লায়েন্ট-নির্মিত একটি UUID আবশ্যক।")
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}
	patientID, bad := requiredUUID(req.PatientID, "patient_id",
		"Which patient has arrived.", "কোন রোগী এসেছেন।")
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}

	opened, err := h.service.Open(r.Context(), Opening{
		EventID: eventID, PatientID: patientID, VisitType: Type(req.VisitType),
		ChiefComplaint: req.ChiefComplaint, Source: sourceOf(r),
	})
	if err != nil {
		if errors.Is(err, ErrAlreadyOpen) {
			// 409 with the visit they already have, so the desk sends them to the queue
			// rather than trying again.
			httpx.WriteJSON(w, http.StatusConflict, map[string]any{
				"error": map[string]any{
					"code": "VISIT_ALREADY_OPEN", "kind": "conflict",
					"message":    "This patient already has an open visit.",
					"message_bn": "এই রোগীর একটি ভিজিট ইতিমধ্যে খোলা আছে।",
				},
				"visit": opened,
			})
			return
		}
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"visit": opened})
}

// --- reading ---

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
	summary, err := h.store.Summarise(r.Context(), id, actor.FacilityID(), h.clock.Now())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, summary)
}

func (h *Handlers) today(w http.ResponseWriter, r *http.Request) {
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	// The clinic's calendar, not UTC. Asking a Dhaka clinic for "today" in UTC gives it the
	// wrong six hours of its own morning.
	day := ClinicDayOf(h.clock.Now())
	if raw := r.URL.Query().Get("day"); raw != "" {
		parsed, err := time.ParseInLocation(time.DateOnly, raw, Dhaka)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("day",
				"Use a date like 2026-09-14.", "2026-09-14 এর মতো একটি তারিখ দিন।"))
			return
		}
		day = parsed
	}
	visits, err := h.store.OnDay(r.Context(), actor.FacilityID(), day)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"day": day.Format(time.DateOnly), "visits": visits,
	})
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
	visits, err := h.store.ForPatient(r.Context(), id, actor.FacilityID(), 50)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"visits": visits})
}

// --- closing ---

type closeRequest struct {
	EventID        string `json:"event_id"`
	ChiefComplaint string `json:"chief_complaint"`
	Diagnoses      string `json:"diagnoses"`
	Plan           string `json:"plan"`
	NextReviewDays int    `json:"next_review_days"`
}

func (h *Handlers) close(w http.ResponseWriter, r *http.Request) {
	id, ok := h.idParam(w, r, "id")
	if !ok {
		return
	}
	var req closeRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	eventID, bad := requiredUUID(req.EventID, "event_id",
		"A client-generated UUID is required.", "ক্লায়েন্ট-নির্মিত একটি UUID আবশ্যক।")
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}
	closed, err := h.service.Close(r.Context(), id, Closing{
		EventID: eventID, ChiefComplaint: req.ChiefComplaint,
		Diagnoses: req.Diagnoses, Plan: req.Plan,
		NextReviewDays: req.NextReviewDays, Source: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"visit": closed})
}

type reasonRequest struct {
	EventID string `json:"event_id"`
	Reason  string `json:"reason"`
	Note    string `json:"note"`
}

func (h *Handlers) abandon(w http.ResponseWriter, r *http.Request) {
	id, req, eventID, ok := h.reasonBody(w, r)
	if !ok {
		return
	}
	out, err := h.service.Abandon(r.Context(), id, eventID, req.Reason, req.Note, sourceOf(r))
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"visit": out})
}

func (h *Handlers) reopen(w http.ResponseWriter, r *http.Request) {
	id, req, eventID, ok := h.reasonBody(w, r)
	if !ok {
		return
	}
	out, err := h.service.Reopen(r.Context(), id, eventID, req.Reason, sourceOf(r))
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"visit": out})
}

// --- encounters ---

type arriveRequest struct {
	EventID     string `json:"event_id"`
	StationCode string `json:"station_code"`
}

func (h *Handlers) arrive(w http.ResponseWriter, r *http.Request) {
	id, ok := h.idParam(w, r, "id")
	if !ok {
		return
	}
	var req arriveRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	eventID, bad := requiredUUID(req.EventID, "event_id",
		"A client-generated UUID is required.", "ক্লায়েন্ট-নির্মিত একটি UUID আবশ্যক।")
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}
	encounter, err := h.service.Arrive(r.Context(), id, Arrival{
		EventID: eventID, StationCode: req.StationCode, Source: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"encounter": encounter})
}

type departRequest struct {
	EventID string `json:"event_id"`
	Outcome string `json:"outcome"`
	Note    string `json:"note"`
}

func (h *Handlers) depart(w http.ResponseWriter, r *http.Request) {
	encounterID, ok := h.idParam(w, r, "encounterId")
	if !ok {
		return
	}
	var req departRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	eventID, bad := requiredUUID(req.EventID, "event_id",
		"A client-generated UUID is required.", "ক্লায়েন্ট-নির্মিত একটি UUID আবশ্যক।")
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}
	if req.Outcome == "" {
		req.Outcome = "completed"
	}
	encounter, err := h.service.Depart(r.Context(), encounterID, Departure{
		EventID: eventID, Outcome: req.Outcome, Note: req.Note, Source: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"encounter": encounter})
}

// --- helpers ---

func (h *Handlers) reasonBody(w http.ResponseWriter, r *http.Request) (uuid.UUID, reasonRequest, uuid.UUID, bool) {
	id, ok := h.idParam(w, r, "id")
	if !ok {
		return uuid.Nil, reasonRequest{}, uuid.Nil, false
	}
	var req reasonRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return uuid.Nil, reasonRequest{}, uuid.Nil, false
	}
	eventID, bad := requiredUUID(req.EventID, "event_id",
		"A client-generated UUID is required.", "ক্লায়েন্ট-নির্মিত একটি UUID আবশ্যক।")
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return uuid.Nil, reasonRequest{}, uuid.Nil, false
	}
	return id, req, eventID, true
}

func (h *Handlers) idParam(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		// 404 rather than 400, for the same reason CP20 gives: an answer that distinguishes
		// a malformed id from an unknown one is a way to learn which ids exist.
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return uuid.Nil, false
	}
	return id, true
}

func requiredUUID(raw, field, en, bn string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errs.ErrValidation.WithFieldIn(field, en, bn)
	}
	return id, nil
}

func translate(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errs.ErrNotFound.WithDetail(err)
	case errors.Is(err, ErrAlreadyOpen), errors.Is(err, ErrAlreadyAtStation),
		errors.Is(err, ErrEncounterFinished):
		return errs.ErrConflict.WithDetail(err)
	case errors.Is(err, ErrIllegalTransition), errors.Is(err, ErrNotOpen),
		errors.Is(err, ErrSummaryIncomplete), errors.Is(err, ErrReasonRequired),
		errors.Is(err, ErrUnknownStation):
		return errs.ErrValidation.WithDetail(err)
	case errors.Is(err, eventstore.ErrNoDevice):
		return errs.ErrDeviceRequired.WithDetail(err)
	case errors.Is(err, eventstore.ErrNoRole):
		return errs.ErrForbidden.WithDetail(err)
	case errors.Is(err, eventstore.ErrNoPrincipal):
		return errs.ErrUnauthenticated.WithDetail(err)
	}
	return err
}

func sourceOf(r *http.Request) eventstore.Source {
	principal, ok := httpx.PrincipalFrom(r.Context())
	if ok && principal.DeviceID != "" {
		return eventstore.SourceMobileOnline
	}
	return eventstore.SourceWeb
}
