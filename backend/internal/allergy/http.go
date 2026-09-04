package allergy

import (
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
)

// The hard stop over HTTP (CP54).
//
// # Why reading is not a sensitive permission
//
// `patient.read.allergies` is held by the pharmacist and the prescription educator — roles
// §4.4 blinds to diagnoses — and that is deliberate rather than an oversight. An allergy has
// to reach the person handing over the medicine. Blinding them to it would mean the last
// person who could catch the mistake is the one person who cannot see the warning.
//
// # What this surface does not do
//
// It does not enforce the gate. Criterion 4 says the gate cannot be bypassed by any client,
// and a check here would hold only for clients that go through here. The enforcement is a
// trigger on `core.queue_entry`; what these endpoints do is let a station satisfy it in five
// seconds, and let a screen say *why* the next button is not available yet.

const (
	PermRead  = "patient.read.allergies"
	PermWrite = "allergy.write"
)

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

// Mount attaches the vocabulary, the withdrawals and the QA view under /v1/allergies.
func (h *Handlers) Mount(r chi.Router) {
	read := httpx.Permission(PermRead)
	write := httpx.Permission(PermWrite)
	r.Route("/allergies", func(a chi.Router) {
		// The reaction vocabulary. Reference data a station fetches once and renders as
		// buttons — which is what makes "coded, not free text" something an officer can
		// comply with in the seconds this question actually gets.
		a.Method("GET", "/reactions", httpx.Declare(read, h.reactions))
		// The plan's own mitigation for the risk it names: operators asserting NKA
		// reflexively to clear the gate. In front of a QA officer, never in a rule.
		a.Method("GET", "/assertion-rates", httpx.Declare(httpx.Permission("qa.review"), h.rates))
		a.Method("POST", "/{allergyId}/withdraw", httpx.Declare(write, h.withdrawAllergy))
		a.Method("POST", "/assertions/{assertionId}/withdraw",
			httpx.Declare(write, h.withdrawAssertion))
	})
}

// MountPatient hangs the per-patient state, the write and the assertion off the patient record.
func (h *Handlers) MountPatient(r chi.Router) {
	read := httpx.Permission(PermRead)
	write := httpx.Permission(PermWrite)
	r.Route("/{id}/allergies", func(p chi.Router) {
		// Criterion 3's endpoint. Every patient-context screen reads this, and it answers
		// the status as well as the list — because an empty list and "nobody has asked" are
		// opposite facts and a header that showed both as blank would be lying about one.
		p.Method("GET", "/", httpx.Declare(read, h.forPatient))
		p.Method("GET", "/history", httpx.Declare(read, h.history))
		p.Method("POST", "/", httpx.Declare(write, h.record))
		// Criterion 2. Its own endpoint, not a flag on the write: "she reacts to penicillin"
		// and "she says she reacts to nothing" are different claims, and one endpoint taking
		// either would make the assertion a matter of which fields happened to be filled in.
		p.Method("POST", "/assert", httpx.Declare(write, h.assert))
	})
}

func (h *Handlers) reactions(w http.ResponseWriter, r *http.Request) {
	reactions, err := h.store.Reactions(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"reactions": reactions})
}

func (h *Handlers) forPatient(w http.ResponseWriter, r *http.Request) {
	patient, ok := h.uuidParam(w, r, "id")
	if !ok {
		return
	}
	state, err := h.store.For(r.Context(), patient)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"status":    state.Status,
		"satisfied": state.Satisfied(),
		"allergies": state.Allergies,
		"assertion": state.Assertion,
	})
}

func (h *Handlers) history(w http.ResponseWriter, r *http.Request) {
	patient, ok := h.uuidParam(w, r, "id")
	if !ok {
		return
	}
	changes, err := h.store.History(r.Context(), patient)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"changes": changes})
}

type recordRequest struct {
	EventID string `json:"event_id"`
	VisitID string `json:"visit_id,omitempty"`

	CodeSystem  string `json:"code_system,omitempty"`
	CodeVersion string `json:"code_version,omitempty"`
	Code        string `json:"code,omitempty"`
	Said        string `json:"said,omitempty"`

	Reaction  string `json:"reaction"`
	Severity  string `json:"severity"`
	Certainty string `json:"certainty"`
	Note      string `json:"note,omitempty"`
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
	state, err := h.service.Record(r.Context(), Recording{
		EventID: eventID, PatientID: patient, VisitID: visit,
		CodeSystem: body.CodeSystem, CodeVersion: body.CodeVersion, Code: body.Code,
		Said:     body.Said,
		Reaction: body.Reaction, Severity: body.Severity, Certainty: body.Certainty,
		Note:         body.Note,
		LedgerSource: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	h.writeState(w, http.StatusCreated, state)
}

type assertRequest struct {
	EventID string `json:"event_id"`
	VisitID string `json:"visit_id,omitempty"`
	Kind    string `json:"kind"`
	Reason  string `json:"reason,omitempty"`
}

func (h *Handlers) assert(w http.ResponseWriter, r *http.Request) {
	patient, ok := h.uuidParam(w, r, "id")
	if !ok {
		return
	}
	var body assertRequest
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
	state, err := h.service.Assert(r.Context(), eventID, patient, body.Kind, body.Reason,
		visit, sourceOf(r))
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	h.writeState(w, http.StatusCreated, state)
}

type withdrawRequest struct {
	EventID string `json:"event_id"`
	VisitID string `json:"visit_id,omitempty"`
	Reason  string `json:"reason"`
}

func (h *Handlers) withdrawAllergy(w http.ResponseWriter, r *http.Request) {
	h.withdraw(w, r, "allergyId", func(ctx *http.Request, eventID, id uuid.UUID,
		reason string, visit *uuid.UUID) (State, error) {

		return h.service.WithdrawAllergy(ctx.Context(), eventID, id, reason, visit, sourceOf(ctx))
	})
}

func (h *Handlers) withdrawAssertion(w http.ResponseWriter, r *http.Request) {
	h.withdraw(w, r, "assertionId", func(ctx *http.Request, eventID, id uuid.UUID,
		reason string, visit *uuid.UUID) (State, error) {

		return h.service.WithdrawAssertion(ctx.Context(), eventID, id, reason, visit, sourceOf(ctx))
	})
}

// withdraw is the shared half of the two withdrawals. Factored out rather than duplicated
// because the reason rule is the interesting part and it must be the same for both: taking
// back "no known allergies" and taking back an allergy are equally consequential, and neither
// may happen without somebody saying why.
func (h *Handlers) withdraw(w http.ResponseWriter, r *http.Request, param string,
	act func(*http.Request, uuid.UUID, uuid.UUID, string, *uuid.UUID) (State, error)) {

	id, ok := h.uuidParam(w, r, param)
	if !ok {
		return
	}
	var body withdrawRequest
	if !h.decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Reason) == "" {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("reason",
			"Say why this is being taken back.", "কেন এটি প্রত্যাহার করা হচ্ছে, তা জানান।"))
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
	state, err := act(r, eventID, id, strings.TrimSpace(body.Reason), visit)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	h.writeState(w, http.StatusOK, state)
}

func (h *Handlers) rates(w http.ResponseWriter, r *http.Request) {
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	// A window, defaulting to the last thirty days. A rate over all time flattens the thing
	// somebody is looking for, which is a change in one operator's behaviour.
	//
	// The interval is half-open — `from <= t < to` — so that two consecutive windows count
	// every assertion exactly once. That makes the default bounds **whole days** rather than
	// "thirty days ago until this instant": an upper bound of `now` would silently exclude
	// the assertion somebody just made, which is the one a QA officer checking their own
	// screen is most likely to look for and least likely to doubt.
	now := h.clock.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	to := today.AddDate(0, 0, 1)
	from := today.AddDate(0, 0, -30)
	if raw := r.URL.Query().Get("from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("from",
				"Give a date and time.", "তারিখ ও সময় দিন।"))
			return
		}
		from = parsed
	}
	if raw := r.URL.Query().Get("to"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("to",
				"Give a date and time.", "তারিখ ও সময় দিন।"))
			return
		}
		to = parsed
	}
	rates, err := h.store.Rates(r.Context(), actor.FacilityID(), from, to)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"from": from, "to": to, "operators": rates,
	})
}

func (h *Handlers) writeState(w http.ResponseWriter, status int, state State) {
	httpx.WriteJSON(w, status, map[string]any{
		"status":    state.Status,
		"satisfied": state.Satisfied(),
		"allergies": state.Allergies,
		"assertion": state.Assertion,
	})
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

// translate turns the module's refusals into answers an operator can act on.
func translate(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errs.ErrNotFound
	case errors.Is(err, ErrAlreadyWithdrawn):
		return errs.ErrConflict
	case errors.Is(err, ErrPartialCoding):
		return errs.ErrValidation.WithFieldIn("code",
			"A code needs its terminology and version too.",
			"কোডের সঙ্গে টার্মিনোলজি ও সংস্করণও দিতে হবে।")
	case errors.Is(err, ErrNothingNamed):
		return errs.ErrValidation.WithFieldIn("said",
			"Choose the substance, or write what the patient said.",
			"কোন জিনিসে সমস্যা, তা বাছুন বা রোগী যা বলেছেন তা লিখুন।")
	case errors.Is(err, ErrUnknownReaction):
		return errs.ErrValidation.WithFieldIn("reaction",
			"Choose what happened from the list.",
			"কী হয়েছিল, তালিকা থেকে বাছুন।")
	case errors.Is(err, ErrReasonRequired):
		return errs.ErrValidation.WithFieldIn("reason",
			"Say why the allergy question could not be answered.",
			"অ্যালার্জির প্রশ্নের উত্তর কেন পাওয়া যায়নি, তা জানান।")
	case errors.Is(err, ErrReasonNotWanted):
		return errs.ErrValidation.WithFieldIn("reason",
			"No known allergies needs no reason.",
			"কোনো অ্যালার্জি নেই — এর জন্য কারণ লাগে না।")
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
