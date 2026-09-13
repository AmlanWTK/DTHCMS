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
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
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
	// The search and the day list are scoped (CP85), and what settles the scope is a `WHERE`
	// clause rather than a guard.
	//
	// Both return many patients, and ADR-0036 §1 answers a question about one, so there is no
	// resource for a handler to judge. There is, however, exactly the same relationship to
	// apply: a patient this station has not had in the current visit is a patient this
	// station cannot open individually, and is therefore a row that does not appear in the
	// list. `rbac.AuthorizeList` hands the handler that restriction and the store carries it
	// into the query; the facility-wide roles get a restriction that says "everything" and a
	// statement with no predicate in it, so they pay nothing.
	//
	// The rule a filtered list must keep, and the reason the count below is taken *after* the
	// restriction: fewer rows must be the only difference. The status code does not change,
	// no total is reported that was computed before the filter, and nothing says how many
	// rows were withheld — a list that told you what it was hiding would be the existence
	// oracle `GET /v1/patients/{id}` is careful not to be.
	read := httpx.PermissionScoped(PermPatientReadDemographics)
	p.Method("GET", "/", httpx.Declare(read, h.search))
	p.Method("GET", "/today", httpx.Declare(read, h.today))
	// One patient, so one reach question, so scoped.
	p.Method("GET", "/{id}/summary", httpx.Declare(
		httpx.PermissionScoped(PermPatientReadDemographics), h.summary))
}

func (h *Handlers) search(w http.ResponseWriter, r *http.Request) {
	reader, err := eventstore.ReaderFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	// Before the term is even read: which rows this subject may be shown. A refusal here is
	// the ordinary permission refusal — a station role standing nowhere, or a hat that does
	// not hold the permission — and it looks identical to every other 403.
	reach, err := rbac.AuthorizeList(r.Context(), PermPatientReadDemographics)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
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
		Reach:         reach,
	}
	results, err := h.store.Search(r.Context(), reader.FacilityID(), query, h.clock.Now())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}

	h.recordAccess(r, AccessEntry{
		Kind: "patient.searched", ActorID: reader.UserID(), ActorCode: reader.Code(),
		ActorRole: reader.Role(), FacilityID: reader.FacilityID(),
		By: framing(term), Count: len(results), At: h.clock.Now(),
	})

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"patients": results,
		"page":     max(query.Page, 1),
	})
}

func (h *Handlers) today(w http.ResponseWriter, r *http.Request) {
	reader, err := eventstore.ReaderFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	reach, err := rbac.AuthorizeList(r.Context(), PermPatientReadDemographics)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	results, total, err := h.store.Today(r.Context(), reader.FacilityID(), h.clock.Now(),
		atoiOr(r.URL.Query().Get("limit"), MaxPageSize), reach)
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
	reader, err := eventstore.ReaderFrom(r.Context())
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
	// Judged on the record that will actually be served, not on the id in the URL. A merged
	// record redirects, and a reach check made on the id the caller typed would be a check
	// on a record nobody is about to read.
	if err := rbac.AuthorizeStationRead(r.Context(), PermPatientReadDemographics, "patient", live); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	found, err := h.store.ByID(r.Context(), live, reader.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	identifiers := h.view(r, found).Identifiers

	h.recordAccess(r, AccessEntry{
		Kind: "patient.viewed", ActorID: reader.UserID(), ActorCode: reader.Code(),
		ActorRole: reader.Role(), FacilityID: reader.FacilityID(),
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
