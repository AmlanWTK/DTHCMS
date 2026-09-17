package clinical

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// Ordering an investigation over HTTP (CP83).
//
// # Why `lab.order` and not a new permission
//
// CP15's catalogue has held `lab.order` — *"Order an investigation"* — since migration 00006,
// granted to PHYSICIAN and JUNIOR_DOCTOR, with nothing behind it. This is what it was for. A new
// permission would have left the old one dangling and split the access matrix's answer to "who
// may order a test" across two rows.
//
// # Two routes, and the second one is why rule 4 can be satisfied on the floor
//
// `POST /v1/patients/{id}/investigation-orders` is the act. `GET .../investigation-orders` is how
// a screen shows what is already outstanding, so that a consultant bounced by QA for a missing
// HbA1c can see whether they already ordered one this morning rather than ordering a second.

// PermOrder is who may ask for an investigation.
const PermOrder = "lab.order"

// MountOrders hangs the two order routes off a patient.
func (h *Handlers) MountOrders(p chi.Router) {
	read := httpx.PermissionScoped(PermObservationRead)
	// **Not scoped.** `lab.order` is held by the physician and the junior doctor, both of whom
	// reach the whole facility, so a scoped declaration would open a debt nobody owes — see
	// `TestNoRouteDeclaresAResourceCheckItDoesNotNeed`. The read beside it *is* scoped, because
	// `observation.read.values` is held by the station roles, whose reach is their own queue.
	order := httpx.Permission(PermOrder)
	p.Method("GET", "/{id}/investigation-orders", httpx.Declare(read, h.orders))
	p.Method("POST", "/{id}/investigation-orders", httpx.Declare(order, h.order))
}

type orderBody struct {
	EventID string `json:"event_id"`
	VisitID string `json:"visit_id"`
	Code    string `json:"code"`
	Note    string `json:"note"`
}

func (h *Handlers) order(w http.ResponseWriter, r *http.Request) {
	patientID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	// ADR-0036: the route deferred the resource question and this answers it. Write strength,
	// because an order written against a patient who left an hour ago is the thing the
	// correction workflow exists to make visible.
	if !h.mayWriteFor(w, r, PermOrder, patientID) {
		return
	}

	var body orderBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithDetail(err))
		return
	}
	eventID, err := uuid.Parse(strings.TrimSpace(body.EventID))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("event_id",
			"a client-generated id makes a retried order one order",
			"ক্লায়েন্টের তৈরি আইডি থাকলে পুনরায় পাঠানো অনুরোধ একটিই থাকে"))
		return
	}
	in := Ordering{EventID: eventID, PatientID: patientID, Code: body.Code,
		Note: body.Note, Source: orderSourceOf(r)}
	if raw := strings.TrimSpace(body.VisitID); raw != "" {
		visitID, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("visit_id",
				"a visit id", "একটি পরিদর্শন আইডি"))
			return
		}
		in.VisitID = &visitID
	}

	order, err := h.service.Order(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateOrder(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"order": order})
}

func (h *Handlers) orders(w http.ResponseWriter, r *http.Request) {
	patientID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	if !h.mayReadPatient(w, r, patientID) {
		return
	}
	principal, present := httpx.PrincipalFrom(r.Context())
	if !present {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	facility, err := uuid.Parse(principal.FacilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated.WithDetail(err))
		return
	}

	visitRaw := strings.TrimSpace(r.URL.Query().Get("visit_id"))
	if visitRaw != "" {
		visitID, err := uuid.Parse(visitRaw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("visit_id",
				"a visit id", "একটি পরিদর্শন আইডি"))
			return
		}
		orders, err := h.store.OrdersForVisit(r.Context(), visitID, facility)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"orders": orders})
		return
	}

	// No visit named: every code the registry has, newest order per code. Unbounded in the same
	// way `Current` is, and bounded by the same thing — the registry — rather than by a limit
	// that would make an old order disappear without saying so.
	codes, err := h.store.Registry(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	wanted := make([]string, 0, len(codes))
	for _, code := range codes {
		wanted = append(wanted, code.Code)
	}
	orders, err := h.store.OrdersFor(r.Context(), patientID, facility, wanted)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"orders": orders})
}

func translateOrder(err error) error {
	switch {
	case errors.Is(err, ErrOrderCodeUnknown):
		return errs.ErrValidation.WithFieldIn("code",
			"that is not a measurement this clinic records",
			"এই ক্লিনিক এমন কোনো পরিমাপ নথিভুক্ত করে না")
	case errors.Is(err, ErrOrderNotOrderable):
		return errs.ErrValidation.WithFieldIn("code",
			"that measurement is computed from others, so it cannot be ordered",
			"এই পরিমাপটি অন্য মান থেকে হিসাব করা হয়, তাই এটি আলাদা করে দেওয়া যায় না")
	default:
		return errs.ErrInternal.WithDetail(err)
	}
}

func orderSourceOf(r *http.Request) eventstore.Source {
	if strings.TrimSpace(r.Header.Get("X-DTHCMS-Device")) != "" {
		return eventstore.SourceMobileOnline
	}
	return eventstore.SourceWeb
}
