package patient

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// Search and the summary card (CP31).

// AuditRecorder is how this module tells the security audit log that somebody searched.
//
// An interface rather than an import, because patient may not import audit and audit may
// not import patient; cmd/api owns the bridge between them, as it does for auth (CP22).
type AuditRecorder interface {
	RecordPatientAccess(ctx context.Context, entry AccessEntry) error
}

// AccessEntry is one look at the register, described **without the search term**.
//
// The term is the patient's name. Writing it into the audit trail would put PHI in a table
// read by administrators who may hold no clinical permission at all — and it is not what a
// review needs. What a review needs is that a search happened, how it was framed, and how
// many rows came back: fifty name searches in a minute by one operator is what exfiltration
// looks like from the inside, and the term adds nothing to that picture.
type AccessEntry struct {
	Kind       string
	ActorID    uuid.UUID
	ActorCode  string
	ActorRole  string
	FacilityID uuid.UUID
	// By is how the search was framed — "clinical_id", "phone", "name" — never the term.
	By string
	// Count is how many rows the caller was shown.
	Count int
	// PatientID is set for a record that was opened.
	PatientID *uuid.UUID
	Target    string
	At        time.Time
}

func (h *Handlers) mountSearch(p chi.Router) {
	read := httpx.Permission(PermPatientReadDemographics)
	p.Method("GET", "/", httpx.Declare(read, h.search))
	p.Method("GET", "/today", httpx.Declare(read, h.today))
	p.Method("GET", "/{id}/summary", httpx.Declare(read, h.summary))
}

func (h *Handlers) search(w http.ResponseWriter, r *http.Request) {
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	term := strings.TrimSpace(r.URL.Query().Get("q"))
	if term == "" {
		// An empty search is not an error and not the whole register: it is the question
		// "who is here today", which has its own endpoint and its own index.
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"patients": []SearchResult{}, "page": 1})
		return
	}

	query := SearchQuery{
		Term:          term,
		IncludeMerged: r.URL.Query().Get("include_merged") == "true",
		Page:          atoiOr(r.URL.Query().Get("page"), 1),
		PageSize:      atoiOr(r.URL.Query().Get("page_size"), DefaultPageSize),
	}
	results, err := h.store.Search(r.Context(), actor.FacilityID(), query, h.clock.Now())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}

	h.recordAccess(r, AccessEntry{
		Kind: "patient.searched", ActorID: actor.UserID(), ActorCode: actor.Code(),
		ActorRole: actor.Role(), FacilityID: actor.FacilityID(),
		By: framing(term), Count: len(results), At: h.clock.Now(),
	})

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"patients": results,
		"page":     max(query.Page, 1),
	})
}

func (h *Handlers) today(w http.ResponseWriter, r *http.Request) {
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	results, total, err := h.store.Today(r.Context(), actor.FacilityID(), h.clock.Now(),
		atoiOr(r.URL.Query().Get("limit"), MaxPageSize))
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	// Deliberately not audited. "Who is in the building today" is the screen every station
	// leaves open all day; recording it would fill the trail with one line per refresh and
	// bury the searches that matter.
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"patients": results, "total": total})
}

// summaryView is the header card every clinical screen carries: who this is, how old, what
// is on file, and whether the record has been merged.
type summaryView struct {
	ID           uuid.UUID        `json:"id"`
	ClinicalID   string           `json:"clinical_id"`
	NameEN       string           `json:"name_en"`
	NameBN       string           `json:"name_bn"`
	Sex          string           `json:"sex"`
	Birth        birthView        `json:"birth"`
	PhoneMasked  string           `json:"phone_masked"`
	District     string           `json:"district"`
	Upazila      string           `json:"upazila"`
	Identifiers  []identifierView `json:"identifiers"`
	Status       string           `json:"status"`
	MergedIntoID *uuid.UUID       `json:"merged_into_id,omitempty"`
	RegisteredAt time.Time        `json:"registered_at"`
}

func (h *Handlers) summary(w http.ResponseWriter, r *http.Request) {
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}

	// An old card, an old report or an old event may name a record that has since been
	// merged away. Following the redirect here means every screen that opens a patient
	// lands on the live record without each of them remembering to.
	live, err := h.store.SurvivingID(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	found, err := h.store.ByID(r.Context(), live, actor.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	identifiers := h.view(r, found).Identifiers

	h.recordAccess(r, AccessEntry{
		Kind: "patient.viewed", ActorID: actor.UserID(), ActorCode: actor.Code(),
		ActorRole: actor.Role(), FacilityID: actor.FacilityID(),
		PatientID: &found.ID, Target: found.ClinicalID, At: h.clock.Now(),
	})

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"patient": summaryView{
		ID: found.ID, ClinicalID: found.ClinicalID,
		NameEN: found.NameEN, NameBN: found.NameBN, Sex: string(found.Sex),
		Birth: birthView{
			Date:      found.Birth.Date.In(Dhaka).Format(time.DateOnly),
			Precision: string(found.Birth.Precision),
			Source:    string(found.Birth.Source),
			Age:       found.Birth.Age(h.clock.Now()),
		},
		PhoneMasked: maskPhone(found.PhonePrimary),
		District:    found.Address.District, Upazila: found.Address.Upazila,
		Identifiers:  identifiers,
		Status:       string(found.Status),
		MergedIntoID: found.MergedIntoID,
		RegisteredAt: found.RegisteredAt,
	}})
}

// recordAccess writes the audit entry, and never fails the request for it.
//
// A clinician who cannot open a patient because the audit table is busy is a worse outcome
// than an audit line that is late — but a *missing* line is a hole in the trail, so it is
// logged loudly rather than swallowed.
func (h *Handlers) recordAccess(r *http.Request, entry AccessEntry) {
	if h.audit == nil {
		return
	}
	if err := h.audit.RecordPatientAccess(r.Context(), entry); err != nil {
		h.logger.ErrorContext(r.Context(), "a patient access was not audited",
			"kind", entry.Kind, "actor", entry.ActorCode, "error", err)
	}
}

// framing says how a search was expressed, for the audit line. Never the term itself.
func framing(term string) string {
	switch {
	case clinicalIDLike.MatchString(term) || (isAllDigits(term) && len(term) <= 6):
		return "clinical_id"
	case phonePattern(term) != "":
		return "phone"
	default:
		return "name"
	}
}

func atoiOr(raw string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
