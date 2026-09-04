package visit

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// The traffic board over HTTP (CP40).

const (
	// PermBoardRead is the wall display's own permission, not `visit.read`. The screen in
	// the waiting area needs an account, and that account should be able to do exactly one
	// thing — see the note in migration 00025.
	PermBoardRead = "board.read"
	// PermVisitReroute is a floor supervisor's. Rerouting is deciding somebody else's queue
	// is wrong, which is not a station operator's call to make.
	PermVisitReroute = "visit.reroute"
)

// MountBoard attaches the traffic board under /v1/board.
func (h *Handlers) MountBoard(r chi.Router) {
	r.Route("/board", func(b chi.Router) {
		b.Method("GET", "/", httpx.Declare(httpx.Permission(PermBoardRead), h.trafficBoard))
		b.Method("POST", "/reroute/{entryId}",
			httpx.Declare(httpx.Permission(PermVisitReroute), h.reroute))
	})
}

func (h *Handlers) trafficBoard(w http.ResponseWriter, r *http.Request) {
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

	board, err := h.store.BoardSnapshot(r.Context(), actor.FacilityID(), day, now)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateQueue(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, board)
}

type rerouteRequest struct {
	EventID string `json:"event_id"`
	To      string `json:"to"`
	Reason  string `json:"reason"`
}

func (h *Handlers) reroute(w http.ResponseWriter, r *http.Request) {
	entryID, ok := h.idParam(w, r, "entryId")
	if !ok {
		return
	}
	var req rerouteRequest
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

	entry, err := h.service.Reroute(r.Context(), entryID, Rerouting{
		EventID: eventID, To: req.To, Reason: req.Reason, Source: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateBoard(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"entry": entry})
}

// translateBoard turns the reroute's refusals into answers a supervisor standing at a wall
// display can act on. `ErrNotWaiting` is the interesting one: two supervisors looking at the
// same board, one a second slower. Telling the second "that entry does not exist" would
// read as a bug in the board; telling them the patient has moved on is what happened.
func translateBoard(err error) error {
	switch {
	case errors.Is(err, ErrNotWaiting):
		return errs.ErrConflict.WithDetail(err)
	case errors.Is(err, ErrRerouteIncomplete):
		return errs.ErrValidation.WithFieldIn("reason",
			"A reroute says where the patient is going and why, in at least five characters.",
			"রি-রুট করতে হলে রোগী কোথায় যাচ্ছেন এবং কেন — অন্তত পাঁচটি অক্ষরে — জানাতে হবে।")
	default:
		return translateQueue(err)
	}
}
