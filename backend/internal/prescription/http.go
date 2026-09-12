package prescription

import (
	"context"
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
	"github.com/AmlanWTK/DTHCMS/backend/internal/medsafety"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// The prescription API (CP80).
//
// # Which permissions, and why no new ones
//
// CP15's catalogue already holds them, granted against §4.4's access matrix and enforced by
// `core.assert_access_matrix_holds()` on every start. `prescription.draft` is "only prescribers
// create"; `prescription.read` is everybody who has a reason to see one. Inventing a
// `prescription.write` beside them — which the first draft of this checkpoint did — duplicated
// the first and, through an `ON CONFLICT` clause, silently flipped the second to sensitive,
// which broke the access-matrix invariant on the next verify. The database caught it.
//
// # What is deliberately not here
//
// **No signing route.** CP84 owns signing: canonical serialisation, a non-exportable KMS key,
// step-up 2FA. A `POST /sign` at this checkpoint would be a signing endpoint without step-up,
// which is a hole rather than a head start. Same for QA clearance (CP83), printing (CP89) and
// dispensing (CP118): the transitions exist in the machine and in [Service], the screens and
// their guards arrive with their checkpoints.
//
// # Reads use the read door
//
// Every GET here goes through `eventstore.ReaderFrom`, never `ActorFrom`. CP74's defect was 28
// GET routes unreachable from a browser because they opened the write door for the facility id;
// `dthclint readpath` is what stops it happening again, and it walks this file.

const (
	// PermRead — read a prescription and its items. PHYSICIAN, JUNIOR_DOCTOR, PHARMACIST, QA
	// and RX_EDUCATOR hold it (CP15).
	PermRead = "prescription.read"
	// PermDraft — create, edit, submit, cancel, correct. PHYSICIAN and JUNIOR_DOCTOR hold it,
	// which is §4.4's "only prescribers create".
	PermDraft = "prescription.draft"
	// PermSafetyCheck — run the deterministic engine against a prescription. CP78's own
	// permission, reused rather than duplicated: the object is the same patient's clinical
	// picture whether the items come from a body or from a saved draft.
	PermSafetyCheck = medsafety.PermCheck
)

// Handlers serve prescriptions.
type Handlers struct {
	service *Service
	store   *Store
	engine  *medsafety.Engine
	facts   medsafety.PatientFacts
	header  PatientHeader
	logger  *slog.Logger
	clock   interface{ Now() time.Time }
}

// HandlersConfig builds them.
type HandlersConfig struct {
	Service *Service
	Store   *Store
	// Engine and Facts wire CP78. The route is mounted either way; unwired, it refuses with
	// a 500 rather than answering. A safety check that returned "no findings" because the
	// engine was not plugged in would be the worst possible failure of this system, and the
	// only acceptable behaviour for a missing engine is to say so.
	Engine *medsafety.Engine
	Facts  medsafety.PatientFacts
	// Header resolves the demographics the printed sheet carries (CP81). Unwired, the print
	// model comes back with an unresolved patient block that says so, rather than with blanks.
	Header PatientHeader
	Logger *slog.Logger
	Clock  interface{ Now() time.Time }
}

// NewHandlers builds them.
func NewHandlers(cfg HandlersConfig) *Handlers {
	return &Handlers{
		service: cfg.Service, store: cfg.Store, engine: cfg.Engine, facts: cfg.Facts,
		header: cfg.Header, logger: cfg.Logger, clock: cfg.Clock,
	}
}

// Mount attaches everything under /v1/prescriptions.
func (h *Handlers) Mount(r chi.Router) {
	read := httpx.Permission(PermRead)
	draft := httpx.Permission(PermDraft)
	r.Route("/prescriptions", func(p chi.Router) {
		// Creation is `POST /v1/prescriptions` with the visit in the body, rather than
		// `POST /v1/visits/{id}/prescriptions`. Both are defensible; this one is chosen
		// because a prescription's identity is its own — it is corrected, superseded and
		// read years later without anybody caring which visit it was written at — and
		// because nesting it would have meant adding a sub-router hook to the visit module
		// for one route, which is a coupling CP81 and CP82 would then inherit.
		p.Method("POST", "/", httpx.Declare(draft, h.create))
		// The machine itself. A screen that draws which buttons are available reads this
		// rather than keeping its own copy of the matrix, which is the only way the screen
		// and the trigger cannot drift apart.
		p.Method("GET", "/statuses", httpx.Declare(read, h.statuses))
		p.Method("GET", "/{id}", httpx.Declare(read, h.byID))
		p.Method("POST", "/{id}/items", httpx.Declare(draft, h.addItem))
		p.Method("PATCH", "/{id}/items/{itemId}", httpx.Declare(draft, h.modifyItem))
		p.Method("DELETE", "/{id}/items/{itemId}", httpx.Declare(draft, h.removeItem))
		p.Method("POST", "/{id}/submit", httpx.Declare(draft, h.submit))
		p.Method("POST", "/{id}/cancel", httpx.Declare(draft, h.cancel))
		p.Method("POST", "/{id}/corrections", httpx.Declare(draft, h.correct))
		// Mounted unconditionally, even when the engine is not wired. A route that
		// appears only when a dependency is present is a route the contract test cannot
		// see, and §7.2's own risk note is about safety checking that looks complete and
		// is not — a 404 on the safety check would read to a client as "this prescription
		// does not need one". Unwired, it fails loudly instead.
		p.Method("POST", "/{id}/safety-check",
			httpx.Declare(httpx.Permission(PermSafetyCheck), h.safetyCheck))
		// The sheet as it will print (CP81 criterion 5). See printmodel.go for why this is a
		// route rather than a layout decision the browser makes.
		p.Method("GET", "/{id}/print-model", httpx.Declare(read, h.printModel))
	})
}

// MountPatient hangs the patient's list off the patient record.
func (h *Handlers) MountPatient(r chi.Router) {
	r.Method("GET", "/{id}/prescriptions", httpx.Declare(httpx.Permission(PermRead), h.forPatient))
}

func (h *Handlers) statuses(w http.ResponseWriter, r *http.Request) {
	statuses, err := h.store.Statuses(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	edges := []map[string]any{}
	for _, e := range h.service.Machine().Edges() {
		edges = append(edges, map[string]any{
			"from": e.From, "to": e.To, "event_type": e.EventType,
			"note_en": e.NoteEN, "note_bn": e.NoteBN, "owned_by": e.OwnedBy,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"statuses": statuses, "transitions": edges,
	})
}

func (h *Handlers) byID(w http.ResponseWriter, r *http.Request) {
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
	httpx.WriteJSON(w, http.StatusOK, h.decorate(r.Context(), sheet))
}

func (h *Handlers) forPatient(w http.ResponseWriter, r *http.Request) {
	reader, err := eventstore.ReaderFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	patient, ok := h.uuidParam(w, r, "id")
	if !ok {
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	sheets, err := h.store.ForPatient(r.Context(), patient, reader.FacilityID(), limit)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	out := make([]map[string]any, 0, len(sheets))
	for _, sheet := range sheets {
		out = append(out, h.decorate(r.Context(), sheet))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"prescriptions": out})
}

type createRequest struct {
	EventID   string `json:"event_id,omitempty"`
	PatientID string `json:"patient_id"`
	VisitID   string `json:"visit_id"`

	CarryForwardFrom string `json:"carry_forward_from,omitempty"`
	// CarryForwardConfirmed is the plan's explicit confirmation. Its absence is a refusal, not
	// a default: a physician who meant to carry forward and got an empty sheet notices; one
	// who did not mean to and got last month's six drugs might not.
	CarryForwardConfirmed bool `json:"carry_forward_confirmed,omitempty"`
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	var body createRequest
	if !h.decode(w, r, &body) {
		return
	}
	visit, err := uuid.Parse(strings.TrimSpace(body.VisitID))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("visit_id",
			"Say which visit this prescription is being written at.",
			"এই ব্যবস্থাপত্রটি কোন ভিজিটে লেখা হচ্ছে, তা জানান।"))
		return
	}
	patient, err := uuid.Parse(strings.TrimSpace(body.PatientID))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("patient_id",
			"Say which patient this prescription is for.",
			"এই ব্যবস্থাপত্র কোন রোগীর জন্য, তা জানান।"))
		return
	}
	eventID, ok := h.eventID(w, r, body.EventID)
	if !ok {
		return
	}
	carry, ok := h.optionalUUID(w, r, body.CarryForwardFrom, "carry_forward_from")
	if !ok {
		return
	}
	sheet, err := h.service.Create(r.Context(), Creation{
		EventID: eventID, PatientID: patient, VisitID: visit,
		CarryForwardFrom: carry, CarryForwardConfirmed: body.CarryForwardConfirmed,
		LedgerSource: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, h.decorate(r.Context(), sheet))
}

type itemRequest struct {
	EventID   string `json:"event_id,omitempty"`
	ProductID string `json:"product_id,omitempty"`
	Label     string `json:"label,omitempty"`

	LineNo       int      `json:"line_no,omitempty"`
	Dose         string   `json:"dose"`
	DailyDose    *float64 `json:"daily_dose,omitempty"`
	DoseUnit     string   `json:"dose_unit,omitempty"`
	Frequency    string   `json:"frequency"`
	DurationDays *int     `json:"duration_days,omitempty"`
	Route        string   `json:"route,omitempty"`
	Quantity     *float64 `json:"quantity,omitempty"`

	InstructionsEN string `json:"instructions_en,omitempty"`
	InstructionsBN string `json:"instructions_bn,omitempty"`
}

func (h *Handlers) addItem(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "id")
	if !ok {
		return
	}
	var body itemRequest
	if !h.decode(w, r, &body) {
		return
	}
	eventID, ok := h.eventID(w, r, body.EventID)
	if !ok {
		return
	}
	product, ok := h.optionalUUID(w, r, body.ProductID, "product_id")
	if !ok {
		return
	}
	item, err := h.service.AddItem(r.Context(), Addition{
		EventID: eventID, PrescriptionID: id, ProductID: product, Label: body.Label,
		Dose: body.Dose, DailyDose: body.DailyDose, DoseUnit: body.DoseUnit,
		Frequency: body.Frequency, DurationDays: body.DurationDays,
		Route: body.Route, Quantity: body.Quantity,
		InstructionsEN: body.InstructionsEN, InstructionsBN: body.InstructionsBN,
		LedgerSource: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, item)
}

func (h *Handlers) modifyItem(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "id")
	if !ok {
		return
	}
	itemID, ok := h.uuidParam(w, r, "itemId")
	if !ok {
		return
	}
	var body itemRequest
	if !h.decode(w, r, &body) {
		return
	}
	eventID, ok := h.eventID(w, r, body.EventID)
	if !ok {
		return
	}
	item, err := h.service.ModifyItem(r.Context(), Modification{
		EventID: eventID, PrescriptionID: id, ItemID: itemID, LineNo: body.LineNo,
		Dose: body.Dose, DailyDose: body.DailyDose, DoseUnit: body.DoseUnit,
		Frequency: body.Frequency, DurationDays: body.DurationDays,
		Route: body.Route, Quantity: body.Quantity,
		InstructionsEN: body.InstructionsEN, InstructionsBN: body.InstructionsBN,
		LedgerSource: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, item)
}

type reasonRequest struct {
	EventID string `json:"event_id,omitempty"`
	Reason  string `json:"reason"`
}

func (h *Handlers) removeItem(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "id")
	if !ok {
		return
	}
	itemID, ok := h.uuidParam(w, r, "itemId")
	if !ok {
		return
	}
	var body reasonRequest
	if !h.decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Reason) == "" {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("reason",
			"Say why this medicine is coming off the prescription.",
			"এই ওষুধটি ব্যবস্থাপত্র থেকে কেন বাদ দেওয়া হচ্ছে, তা জানান।"))
		return
	}
	eventID, ok := h.eventID(w, r, body.EventID)
	if !ok {
		return
	}
	if err := h.service.RemoveItem(r.Context(), eventID, id, itemID,
		body.Reason, sourceOf(r)); err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) submit(w http.ResponseWriter, r *http.Request) {
	h.move(w, r, func(eventID, id uuid.UUID, _ string) (Prescription, error) {
		return h.service.Submit(r.Context(), eventID, id, sourceOf(r))
	}, false)
}

func (h *Handlers) cancel(w http.ResponseWriter, r *http.Request) {
	h.move(w, r, func(eventID, id uuid.UUID, reason string) (Prescription, error) {
		return h.service.Cancel(r.Context(), eventID, id, reason, sourceOf(r))
	}, true)
}

func (h *Handlers) move(w http.ResponseWriter, r *http.Request,
	act func(uuid.UUID, uuid.UUID, string) (Prescription, error), reasonRequired bool) {

	id, ok := h.uuidParam(w, r, "id")
	if !ok {
		return
	}
	var body reasonRequest
	if !h.decode(w, r, &body) {
		return
	}
	if reasonRequired && strings.TrimSpace(body.Reason) == "" {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("reason",
			"Say why this prescription is being withdrawn.",
			"এই ব্যবস্থাপত্রটি কেন প্রত্যাহার করা হচ্ছে, তা জানান।"))
		return
	}
	eventID, ok := h.eventID(w, r, body.EventID)
	if !ok {
		return
	}
	sheet, err := act(eventID, id, strings.TrimSpace(body.Reason))
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.decorate(r.Context(), sheet))
}

type correctionBody struct {
	EventID string `json:"event_id,omitempty"`
	Reason  string `json:"reason"`
	VisitID string `json:"visit_id,omitempty"`
	// CopyItems carries the original's live lines onto the correction, re-priced at today's
	// price — because carrying a line forward is prescribing it again today.
	CopyItems bool `json:"copy_items,omitempty"`
}

func (h *Handlers) correct(w http.ResponseWriter, r *http.Request) {
	id, ok := h.uuidParam(w, r, "id")
	if !ok {
		return
	}
	var body correctionBody
	if !h.decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Reason) == "" {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("reason",
			"Say what was wrong with the prescription this replaces.",
			"যে ব্যবস্থাপত্রের জায়গা এটি নিচ্ছে, তাতে কী ভুল ছিল তা জানান।"))
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
	correction, err := h.service.Correct(r.Context(), CorrectionRequest{
		EventID: eventID, Corrects: id, Reason: body.Reason, VisitID: visit,
		CopyItems: body.CopyItems, LedgerSource: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, h.decorate(r.Context(), correction))
}

// decorate adds the status labels a screen would otherwise keep its own copy of.
func (h *Handlers) decorate(ctx context.Context, sheet Prescription) map[string]any {
	labels, err := h.store.Statuses(ctx)
	if err == nil {
		for _, label := range labels {
			if label.Status == sheet.Status {
				sheet.StatusNameEN, sheet.StatusNameBN = label.NameEN, label.NameBN
			}
		}
	}
	out := map[string]any{"prescription": sheet}
	// What may happen next, from the machine rather than from a client's idea of it. A screen
	// that computed this itself would be a third copy of the matrix.
	next := []map[string]any{}
	for _, edge := range h.service.Machine().Edges() {
		if edge.From != sheet.Status {
			continue
		}
		next = append(next, map[string]any{
			"to": edge.To, "note_en": edge.NoteEN, "note_bn": edge.NoteBN,
			"owned_by": edge.OwnedBy,
		})
	}
	out["available_transitions"] = next
	return out
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

// translate turns the module's refusals into answers a prescriber can act on, in both languages.
//
// **The important one is the illegal transition.** A 409 that said only "conflict" would leave
// the physician clicking the same button again; this one names the status the prescription is
// in and points at the correction path, which is the only thing he can actually do.
func translate(err error) error {
	var transition *TransitionError
	if errors.As(err, &transition) {
		return errs.ErrConflict.WithMessageIn(transition.MessageEN(), transition.MessageBN())
	}
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrItemNotFound):
		return errs.ErrNotFound
	case errors.Is(err, ErrNotEditable):
		return errs.ErrConflict.WithMessageIn(
			"This prescription is no longer a draft and cannot be edited. Correct it instead: "+
				"a correction is a new prescription that supersedes this one, and this one "+
				"stays in the record exactly as it was written.",
			"এই ব্যবস্থাপত্রটি আর খসড়া নয়, তাই বদলানো যাবে না। এর বদলে সংশোধনী দিন: সংশোধনী "+
				"একটি নতুন ব্যবস্থাপত্র যা এটির জায়গা নেয়, আর এটি যেমন লেখা হয়েছিল ঠিক তেমনই "+
				"নথিতে থেকে যায়।")
	case errors.Is(err, ErrItemRemoved):
		return errs.ErrConflict.WithMessageIn(
			"That medicine has already been taken off this prescription.",
			"এই ওষুধটি ইতিমধ্যেই ব্যবস্থাপত্র থেকে বাদ দেওয়া হয়েছে।")
	case errors.Is(err, ErrNotCorrectable):
		return errs.ErrConflict.WithMessageIn(
			"Only a signed prescription is corrected. This one is still being written — edit it.",
			"শুধু স্বাক্ষরিত ব্যবস্থাপত্রেই সংশোধনী দেওয়া হয়। এটি এখনও লেখা হচ্ছে — সরাসরি বদলান।")
	case errors.Is(err, ErrAlreadyCorrected):
		return errs.ErrConflict.WithMessageIn(
			"This prescription has already been corrected. Correct the correction instead, so "+
				"that the record stays a single chain rather than two sheets each claiming to "+
				"replace the same one.",
			"এই ব্যবস্থাপত্রটি ইতিমধ্যেই সংশোধন করা হয়েছে। এবার সংশোধনীটিকেই সংশোধন করুন, যাতে "+
				"নথিতে একটিই ধারা থাকে, একই ব্যবস্থাপত্রের জায়গা নেওয়ার দাবি করা দুটি কাগজ নয়।")
	case errors.Is(err, ErrCarryForwardNotConfirmed):
		return errs.ErrValidation.WithFieldIn("carry_forward_confirmed",
			"Confirm that the previous prescription's medicines should be carried forward.",
			"আগের ব্যবস্থাপত্রের ওষুধগুলো এখানে আনা হবে কি না, তা নিশ্চিত করুন।")
	case errors.Is(err, ErrInvalid):
		return errs.ErrValidation.WithFieldIn("label",
			"Choose a medicine, or write its name.",
			"একটি ওষুধ বাছুন, অথবা তার নাম লিখুন।")
	case errors.Is(err, eventstore.ErrNoPrincipal), errors.Is(err, eventstore.ErrNoRole):
		return errs.ErrUnauthenticated
	}
	return errs.ErrInternal.WithDetail(err)
}

// ---------------------------------------------------------------------------
// The CP78 seam
// ---------------------------------------------------------------------------

// safetyCheckBody reproduces a past check. The items are not in it, by construction.
type safetyCheckBody struct {
	// At reproduces a check against the rule versions live at that instant. CP78's criterion
	// 5, as a parameter. Absent means now.
	At *time.Time `json:"at,omitempty"`
}

// safetyCheck runs the deterministic engine against a saved prescription.
//
// # The seam CP78 left, used exactly as it was left
//
// CP78 built `POST /v1/patients/{id}/safety-check` taking a list of proposed items, and said in
// as many words what CP80's route would be: *load the prescription, map its items to
// [medsafety.Item], call the same [medsafety.Engine.Check]*. That is this function, and no
// evaluation logic moved — because none of it was ever keyed to a prescription id.
//
// The seam held. One thing about it is worth recording: CP78's `Item.Ref` is documented as "CP80
// will put the prescription item id here", and that is what happens below, which means a finding
// comes back pointing at a row the editor can highlight without the editor having to keep its
// own mapping from position to item. That was foresight rather than luck, and it is the reason
// this handler is thirty lines rather than a translation layer.
//
// **Removed lines are not sent.** A safety check is about what is on the prescription now; a
// drug the physician took off at 14:06 must not go on producing findings at 14:07, and a check
// that included it would train him to ignore findings.
//
// # It is a read, and it uses the read door
//
// A POST because it carries a body, not because it writes. Nothing here touches
// `eventstore.ActorFrom`.
func (h *Handlers) safetyCheck(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil || h.facts == nil {
		// Never in the real binary, which wires both. Loud rather than silent because the
		// silent alternative is a screen showing a clean safety check that never ran.
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(
			errors.New("the medication safety engine is not wired into this process")))
		return
	}
	reader, err := eventstore.ReaderFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	id, ok := h.uuidParam(w, r, "id")
	if !ok {
		return
	}
	var body safetyCheckBody
	// An empty body is a live check. Decoding failure is not: a client that sent something
	// unparseable asked for something, and answering it with "now" would answer a different
	// question from the one asked.
	if r.ContentLength > 0 {
		if !h.decode(w, r, &body) {
			return
		}
	}
	sheet, err := h.store.ByID(r.Context(), id, reader.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}

	at := h.now()
	if body.At != nil {
		at = body.At.UTC()
	}

	picture, unclassified, err := h.engine.Assemble(r.Context(), h.facts,
		reader.FacilityID(), sheet.PatientID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}

	proposed := make([]medsafety.Item, 0, len(sheet.Items))
	for _, item := range sheet.Items {
		if !item.Live() {
			continue
		}
		proposed = append(proposed, medsafety.Item{
			// The prescription item id, which is exactly what CP78's Ref was reserved for.
			Ref:       item.ID.String(),
			ProductID: item.ProductID,
			Generic:   item.GenericName,
			Label:     item.Label,
			DailyDose: item.DailyDose,
			DoseUnit:  item.DoseUnit,
		})
	}

	result, err := h.engine.Check(r.Context(), medsafety.Request{
		FacilityID: reader.FacilityID(), Proposed: proposed, Picture: picture, At: at,
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	// An allergy nobody could classify is folded in here rather than inside the engine, for
	// CP78's reason: the engine takes a picture, and this is a fact about how the picture was
	// read. Re-sorted and the verdict recomputed, so an unclassifiable anaphylaxis actually
	// blocks rather than sitting at the bottom of the list.
	if extra := medsafety.UnclassifiedAllergyFindings(unclassified); len(extra) > 0 {
		result = medsafety.Fold(result, extra, len(proposed))
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"prescription_id": sheet.ID,
		"status":          sheet.Status,
		"result":          result,
	})
}

func (h *Handlers) now() time.Time {
	if h.clock == nil {
		return time.Now().UTC()
	}
	return h.clock.Now().UTC()
}

// ---------------------------------------------------------------------------
// CP81 — what the editor needs that the aggregate does not carry
// ---------------------------------------------------------------------------

// The three reads and two writes CP81 adds.
//
// # Why the defaults are their own route rather than a field on the search result
//
// CP76's `/formulary/search` answers a keystroke and is measured in microseconds; hanging a dose
// suggestion off every strength of every brand would multiply its payload by the number of
// strengths for a fact that is about the molecule. The editor fetches the twenty-eight
// suggestions once when it opens and answers every line from memory, which is also what makes
// the suggestion appear in the same frame as the medicine rather than a round trip later.
//
// # Why approving is `medication.rule.publish` and not a new permission
//
// The question the permission answers is "may this person put the clinic's name on a piece of
// clinical content". §4.4 grants that to the physician alone and CP77 already spells it. A
// `prescribing.default.approve` beside it would be a second name for the same authority, and
// CP80's own header records what happened the last time this module invented a permission.

// PermApproveContent — approve a prescribing default or a patient instruction. PHYSICIAN alone.
const PermApproveContent = "medication.rule.publish"

// MountContent attaches the clinic content the editor reads.
//
// Its own mount rather than more routes inside `/prescriptions`, because neither of these is
// about a prescription: they are the clinic's content, read while writing one.
func (h *Handlers) MountContent(r chi.Router) {
	draft := httpx.Permission(PermDraft)
	approve := httpx.Permission(PermApproveContent)
	r.Method("GET", "/prescribing-defaults", httpx.Declare(draft, h.prescribingDefaults))
	r.Method("POST", "/prescribing-defaults/{id}/approval",
		httpx.Declare(approve, h.approveDefault))
	r.Method("GET", "/instruction-templates", httpx.Declare(draft, h.instructionTemplates))
	r.Method("POST", "/instruction-templates/{id}/approval",
		httpx.Declare(approve, h.approveTemplate))
}

func (h *Handlers) prescribingDefaults(w http.ResponseWriter, r *http.Request) {
	reader, err := eventstore.ReaderFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	defaults, err := h.store.PrescribingDefaults(r.Context(), reader.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	approved := 0
	for _, d := range defaults {
		if d.Approval.Approved {
			approved++
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"defaults": defaults,
		// Counted here rather than in the browser, for the same reason CP78 lifts
		// `uncovered_count` out of its coverage list: a number a client has to compute is a
		// number a client will forget to compute, and this is the number the editor has to
		// show beside the word "suggestion".
		"total":    len(defaults),
		"approved": approved,
	})
}

func (h *Handlers) instructionTemplates(w http.ResponseWriter, r *http.Request) {
	reader, err := eventstore.ReaderFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	templates, err := h.store.InstructionTemplates(r.Context(), reader.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	approved := 0
	for _, t := range templates {
		if t.Approval.Approved {
			approved++
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"templates": templates, "total": len(templates), "approved": approved,
	})
}

func (h *Handlers) approveDefault(w http.ResponseWriter, r *http.Request) {
	h.approve(w, r, h.store.ApproveDefault)
}

func (h *Handlers) approveTemplate(w http.ResponseWriter, r *http.Request) {
	h.approve(w, r, h.store.ApproveTemplate)
}

func (h *Handlers) approve(w http.ResponseWriter, r *http.Request,
	act func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, time.Time) (Approval, error)) {

	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	id, ok := h.uuidParam(w, r, "id")
	if !ok {
		return
	}
	approval, err := act(r.Context(), actor.FacilityID(), id, actor.UserID(), h.now())
	if errors.Is(err, ErrNoSuchContent) {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"approval": approval})
}

// printModel answers with the document CP89 will render.
//
// A read, through the read door, on `prescription.read` — the pharmacist holds it, and a
// pharmacist looking at the sheet as it will print is the point of the route existing at all.
func (h *Handlers) printModel(w http.ResponseWriter, r *http.Request) {
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
	facts, resolved := HeaderFacts{}, false
	if h.header != nil {
		// A patient this reader cannot see, or a header lookup that failed, produces an
		// unresolved block rather than a 500. The preview is still worth showing and it says
		// so in both languages — an empty name field that looked filled-in-later would be the
		// dishonest failure.
		if got, err := h.header.PrescriptionHeader(r.Context(),
			reader.FacilityID(), sheet.PatientID); err == nil {
			facts, resolved = got, true
		}
	}
	httpx.WriteJSON(w, http.StatusOK, PrintModelOf(sheet, facts, resolved, h.now()))
}
