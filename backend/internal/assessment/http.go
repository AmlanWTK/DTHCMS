package assessment

import (
	"errors"
	"log/slog"
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

// The lifestyle assessment over HTTP (CP58).
//
// # Why the catalogue is readable by anybody who records values
//
// The questionnaire is published literature with no patient in it, and a station app fetches the
// whole thing once per session so that it can work offline for the rest of the morning. Gating
// that behind the lifestyle write permission would mean a nutritionist could not see what station
// 3 asks — and the counselling engine (CP55) already established that reference data an operator
// needs in order to comply with a rule should not need the permission to write under it.
//
// # Why writing needs the station's own permission
//
// `observation.write.lifestyle` is what §4.4 gives station 3, and a questionnaire answered at
// station 3 is that station's act. It is deliberately not a new permission: an assessment is
// lifestyle data by another shape, and inventing a second grant would be an access review with one
// more line and no more meaning.

const (
	// PermWriteLifestyle is station 3's own.
	PermWriteLifestyle = "observation.write.lifestyle"
	// PermReadValues is what every clinical reader holds.
	PermReadValues = "observation.read.values"
)

// Handlers serve the assessment.
type Handlers struct {
	service *Service
	store   *Store
	clock   interface{ Now() time.Time }
	logger  *slog.Logger
}

// HandlersConfig builds Handlers.
type HandlersConfig struct {
	Service *Service
	Store   *Store
	Clock   interface{ Now() time.Time }
	Logger  *slog.Logger
}

// NewHandlers builds them.
func NewHandlers(cfg HandlersConfig) *Handlers {
	return &Handlers{service: cfg.Service, store: cfg.Store, clock: cfg.Clock, logger: cfg.Logger}
}

// Mount attaches /v1/assessments.
func (h *Handlers) Mount(r chi.Router) {
	read := httpx.Permission(PermReadValues, PermWriteLifestyle)
	write := httpx.Permission(PermWriteLifestyle)

	r.Route("/assessments", func(a chi.Router) {
		// The catalogue, with the questions. One request per session; the station app then works
		// offline. It includes the instruments this clinic may **not** run, with the sentence
		// saying why — a clinician who expected to find PHQ-9 deserves that rather than a gap.
		a.Method("GET", "/instruments", httpx.Declare(read, h.instruments))
		a.Method("POST", "/", httpx.Declare(write, h.record))
		// Recompute the composite from the record as it stands. The commonest sequence at this
		// station is "type the four numbers, save", which can take a patient from two assessed
		// domains to four without any questionnaire being answered — and a score that could only
		// be produced by submitting one would be stale exactly when the operator had just
		// finished making it computable.
		a.Method("POST", "/score", httpx.Declare(write, h.score))
	})
}

// MountPatient hangs the per-patient reads off a patient, through CP36's `Sub` hook.
func (h *Handlers) MountPatient(p chi.Router) {
	read := httpx.Permission(PermReadValues, PermWriteLifestyle)
	p.Method("GET", "/{id}/assessments", httpx.Declare(read, h.forPatient))
	// Where this patient stands, **without writing**. The POST recomputes and stores; this only
	// looks. A screen that had to write in order to ask would either pollute the ledger every
	// time somebody opened a tab, or say nothing until they saved something.
	p.Method("GET", "/{id}/lifestyle-scoring", httpx.Declare(read, h.standing))
}

func (h *Handlers) instruments(w http.ResponseWriter, r *http.Request) {
	// The questions come down unless a caller says otherwise. A list of instrument names is
	// almost never what anybody wants, and two round trips on a tablet at the start of a clinic
	// session is two chances for the second one to fail.
	// The cheapest possible re-check: a tablet that only wants to know whether the catalogue
	// moved should not pay for five instruments' names, purposes and licence notes to find out.
	if strings.TrimSpace(r.URL.Query().Get("version_only")) != "" {
		version, err := h.store.CatalogueVersion(r.Context())
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"catalogue_version": version})
		return
	}
	withItems := strings.TrimSpace(r.URL.Query().Get("names_only")) == ""
	instruments, err := h.store.Instruments(r.Context(), withItems)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	// When the catalogue last changed. A tablet holds this for a morning and works from it
	// offline; without the marker the only signal that a version was republished is a 422 on an
	// item code at submit, after the patient has answered.
	version, err := h.store.CatalogueVersion(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"instruments": instruments, "catalogue_version": version,
	})
}

type recordRequest struct {
	EventID        string   `json:"event_id,omitempty"`
	PatientID      string   `json:"patient_id"`
	VisitID        string   `json:"visit_id,omitempty"`
	InstrumentCode string   `json:"instrument_code"`
	Answers        []Answer `json:"answers"`
}

func (h *Handlers) record(w http.ResponseWriter, r *http.Request) {
	var body recordRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	patient, err := uuid.Parse(strings.TrimSpace(body.PatientID))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("patient_id",
			"That is not a patient identifier.", "এটি কোনও রোগীর পরিচিতি নয়।"))
		return
	}
	in := Recording{
		PatientID: patient, InstrumentCode: strings.TrimSpace(body.InstrumentCode),
		Answers: body.Answers, LedgerSource: sourceOf(r),
	}
	if raw := strings.TrimSpace(body.EventID); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("event_id",
				"That is not an event identifier.", "এটি কোনও ইভেন্টের পরিচিতি নয়।"))
			return
		}
		in.EventID = id
	}
	if raw := strings.TrimSpace(body.VisitID); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("visit_id",
				"That is not a visit identifier.", "এটি কোনও ভিজিটের পরিচিতি নয়।"))
			return
		}
		in.VisitID = &id
	}

	response, scoring, err := h.service.Record(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateAssessment(err))
		return
	}
	// The scoring is reported beside the response and its score is allowed to be absent — with
	// the reason. Silence would be worse: an operator who finished a questionnaire and saw no
	// score would assume the save failed, and a client left to work out which domains are still
	// needed has to re-implement a decision that lives on the server.
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"response": response, "scoring": scoring,
	})
}

func (h *Handlers) standing(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	reader, err := eventstore.ReaderFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"scoring": h.service.Standing(r.Context(), id, reader.FacilityID()),
	})
}

type scoreRequest struct {
	PatientID string `json:"patient_id"`
	VisitID   string `json:"visit_id,omitempty"`
}

func (h *Handlers) score(w http.ResponseWriter, r *http.Request) {
	var body scoreRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	patient, err := uuid.Parse(strings.TrimSpace(body.PatientID))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("patient_id",
			"That is not a patient identifier.", "এটি কোনও রোগীর পরিচিতি নয়।"))
		return
	}
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	var visit *uuid.UUID
	if raw := strings.TrimSpace(body.VisitID); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("visit_id",
				"That is not a visit identifier.", "এটি কোনও ভিজিটের পরিচিতি নয়।"))
			return
		}
		visit = &id
	}
	scoring := h.service.Score(r.Context(), patient, visit, actor, sourceOf(r))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"scoring": scoring})
}

func (h *Handlers) forPatient(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	reader, err := eventstore.ReaderFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	responses, err := h.store.ForPatient(r.Context(), id, reader.FacilityID(), limit)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"responses": responses})
}

// sourceOf is which surface wrote the event, decided the same way `clinical` decides it: by
// whether the request carries a device header. A copy rather than an import, because it reads a
// header this module also reads, and a shared helper would put a routing detail in a package that
// has no other business with HTTP.
func sourceOf(r *http.Request) eventstore.Source {
	if strings.TrimSpace(r.Header.Get("X-DTHCMS-Device")) != "" {
		return eventstore.SourceMobileOnline
	}
	return eventstore.SourceWeb
}

func translateAssessment(err error) error {
	switch {
	case errors.Is(err, ErrUnknownInstrument):
		return errs.ErrValidation.WithFieldIn("instrument_code",
			"That questionnaire is not in the catalogue.",
			"এই প্রশ্নপত্রটি তালিকায় নেই।")
	case errors.Is(err, ErrInstrumentNotLicensed):
		// 422 rather than 403: nothing is wrong with who is asking. The questionnaire itself may
		// not be used here, and the message says so rather than reading as a permission problem
		// somebody would try to solve by changing a role.
		return errs.New("INSTRUMENT_NOT_LICENSED", errs.KindValidation, http.StatusUnprocessableEntity,
			"This clinic has not licensed that questionnaire, so its answers cannot be recorded.",
			"এই ক্লিনিক ওই প্রশ্নপত্রের লাইসেন্স নেয়নি, তাই এর উত্তর সংরক্ষণ করা যাবে না।").WithDetail(err)
	case errors.Is(err, ErrNoPublishedVersion):
		return errs.ErrValidation.WithFieldIn("instrument_code",
			"That questionnaire has no published version yet.",
			"এই প্রশ্নপত্রের কোনও প্রকাশিত সংস্করণ এখনও নেই।")
	case errors.Is(err, ErrUnknownItem):
		return errs.ErrValidation.WithFieldIn("answers",
			"One of those answers is to a question this questionnaire does not ask.",
			"ওই উত্তরগুলির একটি এমন প্রশ্নের, যা এই প্রশ্নপত্রে নেই।")
	case errors.Is(err, ErrUnknownOption):
		return errs.ErrValidation.WithFieldIn("answers",
			"That is not one of the answers offered for that question.",
			"ওই প্রশ্নের জন্য দেওয়া উত্তরগুলির মধ্যে এটি নেই।")
	case errors.Is(err, ErrWrongAnswerShape):
		return errs.ErrValidation.WithFieldIn("answers",
			"That answer is not the shape the question asks for.",
			"উত্তরটি প্রশ্নের চাওয়া ধরনের নয়।")
	case errors.Is(err, ErrItemMissing):
		return errs.ErrValidation.WithFieldIn("answers",
			"A question that has to be answered was left blank.",
			"উত্তর দেওয়া বাধ্যতামূলক এমন একটি প্রশ্ন ফাঁকা রয়ে গেছে।")
	case errors.Is(err, ErrOutOfRange):
		return errs.ErrValidation.WithFieldIn("answers",
			"That number is outside what the question allows.",
			"সংখ্যাটি প্রশ্নে অনুমোদিত সীমার বাইরে।")
	case errors.Is(err, ErrNoResponse):
		return errs.ErrNotFound
	default:
		return errs.ErrInternal.WithDetail(err)
	}
}
