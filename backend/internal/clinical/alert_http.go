package clinical

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// Critical values over HTTP (CP50).
//
// # Where an alert reaches a person
//
// Three places, and they are not interchangeable:
//
//  1. **In the write's own response.** The alarm has to sound in the hand that typed the
//     number, and the only thing certain to arrive there is the reply to the write. Not a
//     socket the phone may not have; not a second request its screen might not make.
//  2. **On the consultant's dashboard**, pushed over the realtime gateway, and re-read from
//     `GET /v1/alerts` on every reconnect — because CP26's design says the socket is a
//     nicety and the pull is the truth.
//  3. **In the patient's record**, for whoever opens it next week and needs to know this
//     happened.
//
// # Why acknowledging is a write and reading is not
//
// `alert.read` shows a clinician the board. `alert.acknowledge` is the act that stops the
// escalation, and it is deliberately not granted to the officer who entered the value: they
// already know, and a clinic where the person who typed the number can close their own alert
// is a clinic that can clear its board without a clinician ever seeing one.

const (
	// PermAlertRead is the consultant's board.
	PermAlertRead = "alert.read"
	// PermAlertAcknowledge stops an escalation.
	PermAlertAcknowledge = "alert.acknowledge"
)

// MountAlerts attaches the alert surface under /v1/alerts.
func (h *Handlers) MountAlerts(r chi.Router) {
	read := httpx.Permission(PermAlertRead)
	ack := httpx.Permission(PermAlertAcknowledge)
	r.Route("/alerts", func(a chi.Router) {
		a.Method("GET", "/", httpx.Declare(read, h.openAlerts))
		// The thresholds and the chain, as reference data. A station app fetches them once
		// and evaluates locally, which is what lets a phone with no signal still make a
		// noise — and it holds the same ordering the server resolves, so the two cannot
		// disagree about which rule fired.
		a.Method("GET", "/rules", httpx.Declare(read, h.criticalRules))
		a.Method("GET", "/escalation", httpx.Declare(read, h.escalationChain))
		a.Method("GET", "/{id}", httpx.Declare(read, h.alertByID))
		a.Method("POST", "/{id}/acknowledge", httpx.Declare(ack, h.acknowledge))
	})
}

// MountPatientAlerts hangs one patient's alert history off the patient routes.
func (h *Handlers) MountPatientAlerts(p chi.Router) {
	read := httpx.Permission(PermAlertRead)
	p.Method("GET", "/{id}/alerts", httpx.Declare(read, h.alertsForPatient))
}

// defaultAlertLimit bounds a list. An alert board long enough to need paging is a clinic in
// trouble, and the answer to that is not pagination.
const defaultAlertLimit = 100

func (h *Handlers) openAlerts(w http.ResponseWriter, r *http.Request) {
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	alerts, err := h.store.OpenAlerts(r.Context(), actor.FacilityID(), limitFrom(r, defaultAlertLimit))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"alerts": alerts})
}

func (h *Handlers) alertByID(w http.ResponseWriter, r *http.Request) {
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	id, bad := requiredUUID(chi.URLParam(r, "id"), "id")
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}
	alert, err := h.store.AlertByID(r.Context(), id, actor.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"alert": alert})
}

func (h *Handlers) alertsForPatient(w http.ResponseWriter, r *http.Request) {
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	patientID, bad := requiredUUID(chi.URLParam(r, "id"), "id")
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}
	alerts, err := h.store.AlertsForPatient(r.Context(), patientID, actor.FacilityID(),
		limitFrom(r, defaultAlertLimit))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"alerts": alerts})
}

func (h *Handlers) criticalRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.store.CriticalRules(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

func (h *Handlers) escalationChain(w http.ResponseWriter, r *http.Request) {
	chain, err := h.store.EscalationChain(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"steps": chain})
}

type acknowledgeRequest struct {
	Note string `json:"note"`
}

func (h *Handlers) acknowledge(w http.ResponseWriter, r *http.Request) {
	id, bad := requiredUUID(chi.URLParam(r, "id"), "id")
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}
	var req acknowledgeRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	alert, err := h.service.Acknowledge(r.Context(), id, req.Note)
	if err != nil {
		if errors.Is(err, ErrAlertClosed) {
			// 409 rather than 400: nothing the caller sent was wrong. Two clinicians
			// reaching for the same alert is the system working, and the response carries
			// the alert so the second one's screen can say who has it.
			httpx.WriteJSON(w, http.StatusConflict, map[string]any{
				"error": map[string]any{
					"code":       "alert_already_acknowledged",
					"message_en": "Somebody has already acknowledged this alert.",
					"message_bn": "এই সতর্কতা ইতিমধ্যে কেউ গ্রহণ করেছেন।",
				},
				"alert": alert,
			})
			return
		}
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"alert": alert})
}

// alertedResponse folds raised alerts into a write's response.
//
// `escalate_verbally` is criterion 4, computed here rather than left to each client: an
// undelivered alert is exactly the situation in which a screen must not be trusted to do its
// own reasoning, and three clients each deciding when to show the instruction is three
// chances for one of them to decide never.
func alertedResponse(body map[string]any, alerts []Alert) map[string]any {
	if len(alerts) == 0 {
		return body
	}
	body["alerts"] = alerts
	verbally := false
	for _, alert := range alerts {
		if !alert.Delivered {
			verbally = true
		}
	}
	body["escalate_verbally"] = verbally
	return body
}

func limitFrom(r *http.Request, fallback int32) int32 {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 || int32(value) > fallback {
		return fallback
	}
	return int32(value)
}
