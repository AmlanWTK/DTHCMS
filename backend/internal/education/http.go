package education

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// Station 11 over HTTP (CP88, CP92).
//
// Three routes, and the split between them is the checkpoint's own:
//
//   - the reference data, which has no patient in it and is `reference.read` — the checklists,
//     the scale, the vocabularies, fetched once and cached by a station app that may lose its
//     connection halfway through a clinic;
//   - the session, which is one patient and is `education.read` — held by the officer who
//     records it and by the two consulting roles who read it at the next visit (criterion 2);
//   - the assessment, which is the write and is `education.record`.
//
// **The improvement score does not have a route of its own**, and that is deliberate. A separate
// endpoint would be a second way in, guarded by a second declaration that somebody would one day
// widen. It is part of the assessment body, and what refuses it to the wrong person is the
// observation code's own `write_permission` — which also refuses it on the generic
// `POST /v1/observations` that every role with any write permission can already reach. One rule,
// enforced where the value is written rather than where it arrives.

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

// Mount attaches the reference data under /v1/education.
//
// `reference.read` and not `education.read`, for the reason migration 00067 spells out at
// length: a permission about a patient reaches only the station being worked, a list of
// checklists has no patient in it, and no handler could judge a resource that does not exist. A
// route declared the other way would be refused for the nine station roles and the officer's
// screen would load with no checklists at all.
func (h *Handlers) Mount(r chi.Router) {
	reference := httpx.Permission(PermReference)
	r.Route("/education", func(e chi.Router) {
		e.Method("GET", "/reference", httpx.Declare(reference, h.reference))
	})
}

// MountPatient hangs the session and the assessment off a patient.
//
// Both scoped (ADR-0036): the resource is the patient, and the reach is judged in the handler
// against the real patient rather than inferred from the route. Read strength for the session
// and write strength for the assessment, which is the distinction the ADR draws — the officer
// may read back a patient they finished with an hour ago, and may not record against one.
func (h *Handlers) MountPatient(r chi.Router) {
	read := httpx.PermissionScoped(PermRead)
	write := httpx.PermissionScoped(PermRecord)
	r.Method("GET", "/{id}/education", httpx.Declare(read, h.session))
	r.Method("POST", "/{id}/education", httpx.Declare(write, h.assessment))
}

// reference serves the checklists, the scale and the vocabularies.
//
//dthclint:scopecheck reference data: there is no patient in this response and therefore no resource to judge, which is exactly what migration 00067 decided for the other thirteen reference routes.
func (h *Handlers) reference(w http.ResponseWriter, r *http.Request) {
	out, err := h.store.Reference(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// session is what the officer sees, and what the physician sees at the next visit.
func (h *Handlers) session(w http.ResponseWriter, r *http.Request) {
	reader, err := eventstore.ReaderFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	patient, ok := h.patientFrom(w, r)
	if !ok {
		return
	}
	visit, ok := h.visitFrom(w, r)
	if !ok {
		return
	}
	if !h.mayRead(w, r, patient) {
		return
	}
	out, err := h.service.Session(r.Context(), patient, visit, reader.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// assessmentRequest is the body: the checklist answers, the compliance answer, and §2's answer.
type assessmentRequest struct {
	// EventID is the client's own id for this assessment, and it is what makes a retry safe.
	// Supplied by the caller rather than minted here, exactly as CP42's write does: a tablet
	// that lost the reply and pressed save again sends the same id, every value in the
	// assessment derives its ledger id from it, and the primary key absorbs the retry. A
	// server-minted id would turn every lost reply into a second assessment of a demonstration
	// that happened once.
	EventID    string       `json:"event_id"`
	VisitID    string       `json:"visit_id"`
	Items      []ItemResult `json:"items"`
	Compliance Compliance   `json:"compliance"`
	// Improvement carries its own shape rule: a score, or a reason the question did not apply,
	// never both and never a bare number that could be either. See [Answer.UnmarshalJSON].
	Improvement Answer `json:"improvement"`
}

// assessment records one visit's station 11.
func (h *Handlers) assessment(w http.ResponseWriter, r *http.Request) {
	// Asked for before anything is decoded, so that a request with no enrolled device is
	// refused with the sentence about the device rather than with a complaint about its body.
	// A clinical write is evidence and evidence names the machine it came from [R-03].
	if _, err := eventstore.ActorFrom(r.Context()); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	patient, ok := h.patientFrom(w, r)
	if !ok {
		return
	}
	var req assessmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("body",
			"That is not a readable assessment.", "এই তথ্য পড়া যাচ্ছে না।"))
		return
	}
	eventID, err := uuid.Parse(strings.TrimSpace(req.EventID))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("event_id",
			"An assessment carries its own id, so that pressing save twice records it once.",
			"প্রতিটি মূল্যায়নের নিজস্ব আইডি থাকে, যাতে দুবার সেভ করলেও একবারই লেখা হয়।"))
		return
	}
	visit, err := uuid.Parse(strings.TrimSpace(req.VisitID))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("visit_id",
			"An assessment names the visit it belongs to.",
			"মূল্যায়নটি কোন ভিজিটের, তা উল্লেখ করতে হবে।"))
		return
	}
	// The reach, judged against the real patient before anything is written (ADR-0036 §1).
	// Write strength: the officer may record only against the patient they are holding now.
	if !h.mayWrite(w, r, patient) {
		return
	}

	result, err := h.service.Record(r.Context(), Assessment{
		EventID:      eventID,
		PatientID:    patient,
		VisitID:      visit,
		Items:        req.Items,
		Compliance:   req.Compliance,
		Improvement:  req.Improvement,
		LedgerSource: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, result)
}

func (h *Handlers) patientFrom(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	patient, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return uuid.Nil, false
	}
	return patient, true
}

func (h *Handlers) visitFrom(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	visit, err := uuid.Parse(strings.TrimSpace(r.URL.Query().Get("visit_id")))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("visit_id",
			"The education screen is about one visit; name it.",
			"শিক্ষা কেন্দ্রের পর্দা একটি ভিজিট নিয়ে; কোনটি তা উল্লেখ করুন।"))
		return uuid.Nil, false
	}
	return visit, true
}

func sourceOf(r *http.Request) eventstore.Source {
	if strings.TrimSpace(r.Header.Get("X-DTHCMS-Device")) != "" {
		return eventstore.SourceMobileOnline
	}
	return eventstore.SourceWeb
}

// translate turns this module's refusals into answers an operator can act on.
//
// Each one maps to a sentence somebody can do something about while the patient is still in
// front of them, which is the only kind of error message this station can use.
func translate(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errs.ErrNotFound
	case errors.Is(err, ErrScoreOnFirstVisit):
		return errs.ErrValidation.WithFieldIn("improvement",
			"This is the patient's first visit, so there is no last visit to compare with. "+
				"Record it as not applicable rather than as a number.",
			"রোগী আজই প্রথম এসেছেন, তুলনা করার মতো আগের কোনো দিন নেই। "+
				"সংখ্যা না লিখে 'প্রযোজ্য নয়' হিসেবে লিখুন।")
	case errors.Is(err, ErrNotSelected):
		return errs.ErrValidation.WithFieldIn("items",
			"That checklist is not one this patient's prescription brings up. "+
				"Check the patient, or check the sheet.",
			"এই রোগীর ব্যবস্থাপত্র অনুযায়ী ওই তালিকাটি আসার কথা নয়। "+
				"রোগী ঠিক আছেন কি না, বা ব্যবস্থাপত্রটি মিলিয়ে দেখুন।")
	case errors.Is(err, ErrUnknownItem):
		return errs.ErrValidation.WithFieldIn("items",
			"That is not a checklist item.", "এটি তালিকার কোনো ধাপ নয়।")
	case errors.Is(err, ErrUnknownReason):
		return errs.ErrValidation.WithFieldIn("compliance",
			"That is not a reason this question takes.",
			"এই প্রশ্নের উত্তরে ওই কারণটি নেওয়া হয় না।")
	case errors.Is(err, ErrAnswerShape):
		return errs.ErrValidation.WithFieldIn("improvement",
			"An answer is a score or a reason it was not asked, never both.",
			"উত্তর হয় একটি স্কোর, নয়তো কেন জিজ্ঞাসা করা হয়নি তার কারণ — দুটো একসঙ্গে নয়।")
	case errors.Is(err, ErrNothingToRecord):
		return errs.ErrValidation.WithFieldIn("items",
			"There is nothing in this assessment to record.",
			"এই মূল্যায়নে লিখে রাখার মতো কিছু নেই।")
	case errors.Is(err, clinical.ErrNotPermitted):
		// The refusal CP88 exists to produce, and it arrives from the observation registry
		// rather than from anything in this package: the code's own `write_permission` names a
		// permission this role does not hold. Mapped to a forbidden that says nothing about the
		// patient, because nothing about the patient is the reason.
		return errs.ErrForbidden
	default:
		return err
	}
}
