package prescription

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// The AI suggestion API (CP82).
//
// # Which permissions, and the resource each route judges (ADR-0036 §5)
//
// Four routes and three answers.
//
// `GET /v1/ai-suggestion-reject-reasons` declares **`reference.read`**. §5's vocabulary is twelve
// bilingual labels with no patient in any of them, which is exactly the test migration 00067 sets:
// under `prescription.read` it would be refused to the nine station roles, whose reach for a
// `prescription.` permission is the station they are working. It judges no resource because there
// is none — the route is `httpx.Permission`, not `httpx.PermissionScoped`, so the guard answers it
// and no handler owes anything.
//
// `GET /v1/prescriptions/{id}/ai-suggestions` declares **`prescription.read`, scoped**. One panel
// is one patient. The handler loads the sheet and then judges that patient with
// `rbac.GuardPatientRead` before describing anything — the same shape `byID` uses, and the reason
// is the same: the 404 for a prescription in another facility and the 403 for one this reader
// cannot reach must not be distinguishable from outside.
//
// `POST /v1/prescriptions/{id}/ai-suggestions` and the decision route declare
// **`prescription.draft`**. Asking the AI to propose a medicine and answering it are prescribing
// acts — §4.4's *"only prescribers create"* — and the physician and junior doctor hold it. They
// are `httpx.Permission` rather than `PermissionScoped` for the reason every other write in this
// module is: `prescription.draft` is facility-wide for the two roles that hold it, so there is no
// station reach to defer, and a route that opted into scope would owe a judgement it has nothing
// narrower to make. The service still refuses a prescription in another facility, because every
// read it makes carries the actor's facility id.
//
// **No route accepts a suggestion's content.** The decision route takes a suggestion id and, for
// an edit, the physician's own values — never a product, never a label, never a price. A client
// cannot assert what was suggested, which is what keeps the stored offer the model's own words.
//
// # And no route accepts more than one suggestion
//
// §1: *"There is no code path — no batch accept, no 'accept all', no default-on — by which a
// suggestion becomes a line without a person doing it one line at a time."* The path parameter is
// singular, the body has no array in it, and there is nowhere in this file that loops over
// suggestions. That is the API half of the rule; the screen half is in `AISuggestionPanel.tsx`.

// PermSuggestReasons is the vocabulary's permission. See the header for why it is not
// `prescription.read`.
const PermSuggestReasons = PermReferenceRead

// MountSuggestions attaches CP82's routes.
//
// The reject-reason list is mounted at the top level rather than under `/prescriptions`, for the
// reason `MountContent` gives about the prescribing defaults: it is the clinic's content, read
// while writing a prescription, and it is not about one.
func (h *Handlers) MountSuggestions(r chi.Router) {
	draft := httpx.Permission(PermDraft)
	r.Method("GET", "/ai-suggestion-reject-reasons",
		httpx.Declare(httpx.Permission(PermSuggestReasons), h.rejectReasons))
	r.Route("/prescriptions/{id}/ai-suggestions", func(p chi.Router) {
		p.Method("GET", "/", httpx.Declare(httpx.PermissionScoped(PermRead), h.aiSuggestions))
		p.Method("POST", "/", httpx.Declare(draft, h.askForSuggestions))
		// Singular, and one at a time. See the header.
		p.Method("POST", "/{suggestionId}/decision", httpx.Declare(draft, h.decideSuggestion))
	})
}

// rejectReasons serves §5's vocabulary.
//
// A read through the read door. `reference.read` is held by every role but RESEARCHER, and a
// browser session has no device — which is the defect CP74 found in twenty-eight GET routes and
// `dthclint readpath` is what keeps this one out of the twenty-ninth.
func (h *Handlers) rejectReasons(w http.ResponseWriter, r *http.Request) {
	if _, err := eventstore.ReaderFrom(r.Context()); err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	reasons, err := h.store.RejectReasons(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"reasons": reasons, "total": len(reasons)})
}

// aiSuggestions is the panel's read: what was proposed and what the physician has done about it.
func (h *Handlers) aiSuggestions(w http.ResponseWriter, r *http.Request) {
	reader, err := eventstore.ReaderFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	id, ok := h.uuidParam(w, r, "id")
	if !ok {
		return
	}
	sheet, err := h.store.ByID(r.Context(), id, reader.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	// ADR-0036 §1, judged after the sheet is loaded and before anything about it is described.
	if !rbac.GuardPatientRead(w, r, h.logger, PermRead, "prescription", sheet.PatientID) {
		return
	}
	run, err := h.service.SuggestionsFor(r.Context(), sheet.ID, reader.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, run)
}

// askForSuggestions runs the agent against one draft.
//
// A 200 for every outcome the agent can reach, including a refusal and a failure, because each of
// those is a state with a sentence rather than an error a client has to interpret. The only
// non-200s are the ones that mean the caller asked about a prescription that is not theirs, or
// asked a process that has no agent wired into it.
func (h *Handlers) askForSuggestions(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "id")
	if !ok {
		return
	}
	run, err := h.service.Suggest(r.Context(), id)
	if errors.Is(err, ErrSuggestionsUnavailable) {
		// Loud rather than silent, exactly as the safety-check route is. A panel that showed "the
		// AI proposed nothing" because nothing was plugged in would be the worst available
		// failure: the physician would conclude the model had considered his patient and found
		// nothing worth adding.
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateSuggestion(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, run)
}

// decisionBody is one physician's answer to one suggestion.
//
// There is no product, no label, no price and no array. See the file header.
type decisionBody struct {
	EventID  string `json:"event_id,omitempty"`
	Decision string `json:"decision"`

	// The edit, field by field. Pointers, because "left alone" and "set to empty" are different
	// acts and a JSON zero value cannot tell them apart.
	Dose         *string `json:"dose,omitempty"`
	Frequency    *string `json:"frequency,omitempty"`
	DurationDays *int    `json:"duration_days,omitempty"`
	Route        *string `json:"route,omitempty"`

	InstructionsEN string `json:"instructions_en,omitempty"`
	InstructionsBN string `json:"instructions_bn,omitempty"`

	// §5. Both optional, and both refused on anything but a rejection.
	RejectReasonCode string `json:"reject_reason_code,omitempty"`
	RejectNote       string `json:"reject_note,omitempty"`
}

func (h *Handlers) decideSuggestion(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "id")
	if !ok {
		return
	}
	suggestionID, ok := h.uuidParam(w, r, "suggestionId")
	if !ok {
		return
	}
	var body decisionBody
	if !h.decode(w, r, &body) {
		return
	}
	eventID, ok := h.eventID(w, r, body.EventID)
	if !ok {
		return
	}
	decision, err := h.service.Decide(r.Context(), Deciding{
		EventID: eventID, PrescriptionID: id, SuggestionID: suggestionID,
		Kind: DecisionKind(strings.ToUpper(strings.TrimSpace(body.Decision))),
		Edit: EditedLine{
			Dose: body.Dose, Frequency: body.Frequency,
			DurationDays: body.DurationDays, Route: body.Route,
			InstructionsEN: body.InstructionsEN, InstructionsBN: body.InstructionsBN,
		},
		ReasonCode:   body.RejectReasonCode,
		Note:         body.RejectNote,
		LedgerSource: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateSuggestion(err))
		return
	}
	// The sheet comes back with it, because accepting a suggestion changes the prescription and a
	// client that had to make a second request to find out would draw the old one for a frame.
	sheet, err := h.store.ByID(r.Context(), id, decisionFacility(r))
	if err != nil {
		httpx.WriteJSON(w, http.StatusCreated, map[string]any{"decision": decision})
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"decision": decision, "prescription": sheet,
	})
}

// decisionFacility is the caller's facility, for the read-back after a decision.
//
// It reads the principal rather than taking a parameter, so that a client cannot ask for the sheet
// of a prescription in another facility by having just decided about one in this.
func decisionFacility(r *http.Request) (facility uuid.UUID) {
	if reader, err := eventstore.ReaderFrom(r.Context()); err == nil {
		return reader.FacilityID()
	}
	return facility
}

// translateSuggestion turns CP82's refusals into answers a prescriber can act on, in both
// languages.
//
// [ErrNoSuggestion] is a 404 and says nothing about whether the suggestion exists somewhere else,
// which is the house rule: a 403 or a "wrong prescription" message here would confirm a row a
// caller is not entitled to know about.
func translateSuggestion(err error) error {
	switch {
	case errors.Is(err, ErrNoSuggestion):
		return errs.ErrNotFound
	case errors.Is(err, ErrAlreadyDecided):
		return errs.ErrConflict.WithMessageIn(
			"You have already answered this suggestion. To change the prescription, edit or "+
				"remove the line itself — the answer you gave stays in the record.",
			"এই প্রস্তাবটির উত্তর আপনি ইতিমধ্যেই দিয়েছেন। ব্যবস্থাপত্র বদলাতে হলে লাইনটিই সংশোধন "+
				"করুন বা বাদ দিন — আপনার দেওয়া উত্তরটি নথিতে থেকে যাবে।")
	case errors.Is(err, ErrUnknownDecision):
		return errs.ErrValidation.WithFieldIn("decision",
			"A suggestion is accepted, edited or rejected.",
			"একটি প্রস্তাব গ্রহণ, সংশোধন বা বাতিল করা যায়।")
	case errors.Is(err, ErrEditChangesNothing):
		return errs.ErrValidation.WithFieldIn("dose",
			"Nothing was changed, so this is an acceptance rather than an edit. Accept it, or "+
				"change something first.",
			"কিছুই বদলানো হয়নি, তাই এটি সংশোধন নয় — গ্রহণ। প্রস্তাবটি গ্রহণ করুন, অথবা আগে কিছু বদলান।")
	case errors.Is(err, ErrReasonBelongsToRejection):
		return errs.ErrValidation.WithFieldIn("reject_reason_code",
			"A reason belongs to a rejection. Nothing else carries one.",
			"কারণ কেবল বাতিলের সঙ্গেই যায়। অন্য কিছুর সঙ্গে নয়।")
	case errors.Is(err, ErrUnknownReason):
		return errs.ErrValidation.WithFieldIn("reject_reason_code",
			"That is not one of this clinic's rejection reasons.",
			"এটি এই ক্লিনিকের বাতিলের কারণগুলোর একটি নয়।")
	}
	return translate(err)
}
