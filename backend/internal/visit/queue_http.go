package visit

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// The station queue over HTTP (CP39).
//
// Reads need `visit.read`; every transition needs `visit.attend`, which is the station
// operator's permission. The RBAC engine's own station scoping (§4.4) narrows it to the
// operator's own station; this module states the permission and does not re-implement the
// scoping, because two copies of a scoping rule is one copy that drifts.

func (h *Handlers) mountQueue(v chi.Router) {
	read := httpx.Permission(PermVisitRead)
	attend := httpx.Permission(PermVisitAttend)
	v.Method("POST", "/{id}/queue", httpx.Declare(attend, h.enqueue))
	v.Method("GET", "/{id}/queue", httpx.Declare(read, h.visitQueue))
}

// MountStations attaches the station-facing queue endpoints under /v1/stations.
func (h *Handlers) MountStations(r chi.Router) {
	read := httpx.Permission(PermVisitRead)
	attend := httpx.Permission(PermVisitAttend)
	r.Route("/stations", func(s chi.Router) {
		s.Method("GET", "/board", httpx.Declare(read, h.board))
		s.Method("GET", "/{station}/queue", httpx.Declare(read, h.stationQueue))
		s.Method("POST", "/{station}/call-next", httpx.Declare(attend, h.callNext))
		s.Method("POST", "/queue/{entryId}/leave", httpx.Declare(attend, h.leaveQueue))
	})
}

type enqueueRequest struct {
	EventID        string `json:"event_id"`
	StationCode    string `json:"station_code"`
	Priority       int    `json:"priority"`
	PriorityReason string `json:"priority_reason"`
}

func (h *Handlers) enqueue(w http.ResponseWriter, r *http.Request) {
	id, ok := h.idParam(w, r, "id")
	if !ok {
		return
	}
	var req enqueueRequest
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
	entry, err := h.service.Enqueue(r.Context(), id, Joining{
		EventID: eventID, StationCode: req.StationCode,
		Priority: req.Priority, PriorityReason: req.PriorityReason, Source: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateQueue(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"entry": entry})
}

func (h *Handlers) visitQueue(w http.ResponseWriter, r *http.Request) {
	id, ok := h.idParam(w, r, "id")
	if !ok {
		return
	}
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	entries, err := h.store.QueueForVisit(r.Context(), id, actor.FacilityID(), h.clock.Now())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateQueue(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (h *Handlers) stationQueue(w http.ResponseWriter, r *http.Request) {
	station, ok := h.stationParam(w, r)
	if !ok {
		return
	}
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	entries, err := h.store.Queue(r.Context(), actor.FacilityID(), station, h.clock.Now())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateQueue(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"station_code": station, "entries": entries,
	})
}

func (h *Handlers) board(w http.ResponseWriter, r *http.Request) {
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	now := h.clock.Now()
	day := ClinicDayOf(now)
	if raw := r.URL.Query().Get("day"); raw != "" {
		parsed, err := time.ParseInLocation(time.DateOnly, raw, Dhaka)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("day",
				"Use a date like 2026-09-14.", "2026-09-14 এর মতো একটি তারিখ দিন।"))
			return
		}
		day = parsed
	}
	loads, err := h.store.Board(r.Context(), actor.FacilityID(), day, now)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateQueue(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"day": day.Format(time.DateOnly), "stations": loads,
	})
}

type callNextRequest struct {
	EventID string `json:"event_id"`
}

func (h *Handlers) callNext(w http.ResponseWriter, r *http.Request) {
	station, ok := h.stationParam(w, r)
	if !ok {
		return
	}
	var req callNextRequest
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
	entry, err := h.service.CallNext(r.Context(), station, eventID, sourceOf(r))
	if err != nil {
		if errors.Is(err, ErrQueueEmpty) {
			// 204, not 404. An operator who is free and finds nobody waiting has not made a
			// mistake, and a screen that shows an error for it is a screen operators learn
			// to ignore.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		httpx.WriteError(w, r, h.logger, translateQueue(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"entry": entry})
}

type leaveQueueRequest struct {
	EventID    string `json:"event_id"`
	Outcome    string `json:"outcome"`
	Reason     string `json:"reason"`
	ReroutedTo string `json:"rerouted_to"`
}

func (h *Handlers) leaveQueue(w http.ResponseWriter, r *http.Request) {
	entryID, ok := h.idParam(w, r, "entryId")
	if !ok {
		return
	}
	var req leaveQueueRequest
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
	entry, err := h.service.Leave(r.Context(), entryID, Leaving{
		EventID: eventID, Outcome: req.Outcome, Reason: req.Reason,
		ReroutedTo: req.ReroutedTo, Source: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateQueue(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"entry": entry})
}

func (h *Handlers) stationParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	station := chi.URLParam(r, "station")
	if !strings.HasPrefix(station, "STN_") {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return "", false
	}
	return station, true
}

func translateQueue(err error) error {
	switch {
	case errors.Is(err, ErrAlreadyQueued), errors.Is(err, ErrQueueEntryClosed):
		return errs.ErrConflict.WithDetail(err)
	case errors.Is(err, ErrNotCalled), errors.Is(err, ErrRerouteIncomplete):
		return errs.ErrValidation.WithDetail(err)
	}
	return translate(err)
}
