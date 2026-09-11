package nutrition

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

// Station 7 over HTTP (CP59).
//
// # Why the food table is readable by every clinical reader
//
// It is reference data with no patient in it, and the physician reading a recall at station 8 needs
// the names to render it. Gating the table behind the nutrition write permission would mean a
// consultant could see "RICE_BOILED, 2 CUP" and not what either word meant.

const (
	// PermWriteNutrition is station 7's own.
	PermWriteNutrition = "observation.write.nutrition"
	// PermReadValues is what every clinical reader holds.
	PermReadValues = "observation.read.values"
)

// Handlers serve the recall.
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

// Mount attaches /v1/foods and /v1/diet.
func (h *Handlers) Mount(r chi.Router) {
	read := httpx.Permission(PermReadValues, PermWriteNutrition)
	write := httpx.Permission(PermWriteNutrition)

	r.Route("/foods", func(f chi.Router) {
		// The picker. Criterion 1's four minutes is mostly this.
		f.Method("GET", "/", httpx.Declare(read, h.search))
		f.Method("GET", "/measures", httpx.Declare(read, h.measures))
	})
	r.Route("/diet", func(d chi.Router) {
		d.Method("POST", "/", httpx.Declare(write, h.record))
		d.Method("POST", "/{id}/withdraw", httpx.Declare(write, h.withdraw))
	})
}

// MountPatient hangs the recall off a patient, through CP36's `Sub` hook.
func (h *Handlers) MountPatient(p chi.Router) {
	read := httpx.Permission(PermReadValues, PermWriteNutrition)
	p.Method("GET", "/{id}/diet", httpx.Declare(read, h.recall))
	p.Method("GET", "/{id}/diet/days", httpx.Declare(read, h.days))
}

func (h *Handlers) search(w http.ResponseWriter, r *http.Request) {
	limit := 25
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	foods, err := h.store.Search(r.Context(),
		r.URL.Query().Get("q"), r.URL.Query().Get("group"), limit)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"foods": foods})
}

func (h *Handlers) measures(w http.ResponseWriter, r *http.Request) {
	measures, err := h.store.Measures(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	meals, err := h.store.Meals(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"measures": measures,
		// The day's meals, in the order it happens and **with their names**, so a screen does not
		// hard-code them and web and mobile cannot invent different Bangla for MID_MORNING.
		"meals": meals,
		// Which day this clinic thinks a recall taken now is about, on **its own calendar**. The
		// client is told not to compute this, so the server has to say it.
		//
		// It is on the recall payload too, because this one is fetched once and held for a whole
		// clinic session: a session that crosses midnight would otherwise be checking against
		// yesterday's idea of today and would quietly stop warning about a date in the future.
		"recall_date_default": Yesterday(h.clock.Now()),
		"clinic_today":        Today(h.clock.Now()),
	})
}

type recordRequest struct {
	EventID     string  `json:"event_id,omitempty"`
	PatientID   string  `json:"patient_id"`
	VisitID     string  `json:"visit_id,omitempty"`
	RecallDate  string  `json:"recall_date"`
	Meal        string  `json:"meal"`
	EatenAtHour *int    `json:"eaten_at_hour,omitempty"`
	FoodCode    string  `json:"food_code"`
	MeasureCode string  `json:"measure_code"`
	Quantity    float64 `json:"quantity"`
	Note        string  `json:"note,omitempty"`
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
	in := Eating{
		PatientID: patient, RecallDate: body.RecallDate, Meal: body.Meal,
		EatenAtHour: body.EatenAtHour, FoodCode: body.FoodCode,
		MeasureCode: body.MeasureCode, Quantity: body.Quantity, Note: body.Note,
		LedgerSource: sourceOf(r),
	}
	// A recall taken today is almost always about yesterday. Defaulting to yesterday rather than
	// today is the difference between a screen that is right by default and one that is wrong by
	// default, and an operator correcting the date on every patient will stop correcting it.
	if strings.TrimSpace(in.RecallDate) == "" {
		in.RecallDate = Yesterday(h.clock.Now())
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

	recall, err := h.service.Record(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateNutrition(err))
		return
	}
	// The whole day comes back, not just the entry. Two assistants are working this recall and the
	// one who just added a food needs to see what the other one added while they were typing —
	// which is also what stops them recording it a second time.
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"recall": recall, "clinic_today": Today(h.clock.Now()),
	})
}

type withdrawRequest struct {
	EventID string `json:"event_id,omitempty"`
	Reason  string `json:"reason"`
}

func (h *Handlers) withdraw(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	var body withdrawRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var eventID uuid.UUID
	if raw := strings.TrimSpace(body.EventID); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("event_id",
				"That is not an event identifier.", "এটি কোনও ইভেন্টের পরিচিতি নয়।"))
			return
		}
		eventID = parsed
	}
	recall, err := h.service.Withdraw(r.Context(), eventID, id, body.Reason, sourceOf(r))
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateNutrition(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"recall": recall, "clinic_today": Today(h.clock.Now()),
	})
}

func (h *Handlers) recall(w http.ResponseWriter, r *http.Request) {
	id, reader, ok := h.patient(w, r)
	if !ok {
		return
	}
	day, _ := time.Parse("2006-01-02", Yesterday(h.clock.Now()))
	if raw := strings.TrimSpace(r.URL.Query().Get("date")); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("date",
				"That is not a day.", "এটি কোনও দিন নয়।"))
			return
		}
		day = parsed.UTC()
	}
	recall, err := h.store.Recall(r.Context(), id, reader.FacilityID(), day)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"recall": recall, "clinic_today": Today(h.clock.Now()),
	})
}

func (h *Handlers) days(w http.ResponseWriter, r *http.Request) {
	id, reader, ok := h.patient(w, r)
	if !ok {
		return
	}
	days, err := h.store.Days(r.Context(), id, reader.FacilityID(), 60)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"days": days})
}

func (h *Handlers) patient(w http.ResponseWriter, r *http.Request) (uuid.UUID, eventstore.Reader, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return uuid.Nil, eventstore.Reader{}, false
	}
	reader, err := eventstore.ReaderFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return uuid.Nil, eventstore.Reader{}, false
	}
	// An unknown patient answers 404 rather than 200 with an empty recall. On a screen showing a
	// day's food those two are indistinguishable, and the first is a mistyped id while the second
	// is a patient who genuinely ate nothing yet.
	known, err := h.store.KnowsPatient(r.Context(), id, reader.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return uuid.Nil, eventstore.Reader{}, false
	}
	if !known {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return uuid.Nil, eventstore.Reader{}, false
	}
	return id, reader, true
}

func sourceOf(r *http.Request) eventstore.Source {
	if strings.TrimSpace(r.Header.Get("X-DTHCMS-Device")) != "" {
		return eventstore.SourceMobileOnline
	}
	return eventstore.SourceWeb
}

// detailOf is the part of a wrapped refusal that names what went wrong, for a message an operator
// can act on. Deliberately narrow: it takes the text after the last colon, which is where these
// errors put the codes and never puts anything about a patient.
func detailOf(err error) string {
	text := err.Error()
	if at := strings.LastIndex(text, ": "); at >= 0 {
		return strings.TrimSpace(text[at+2:])
	}
	return ""
}

func translateNutrition(err error) error {
	switch {
	case errors.Is(err, ErrUnknownFood):
		return errs.ErrValidation.WithFieldIn("food_code",
			"That food is not in the table. Ask for it to be added rather than recording something close.",
			"এই খাবারটি তালিকায় নেই। কাছাকাছি কিছু না লিখে এটি যোগ করার অনুরোধ করুন।")
	case errors.Is(err, ErrUnknownMeasure):
		// The measure is named. "That measure of that food" is a sentence an operator cannot act
		// on and a support log cannot search.
		return errs.ErrValidation.WithFieldIn("measure_code",
			"The table does not know what that portion weighs ("+detailOf(err)+"). Record it in grams, or ask for the portion to be added.",
			"ওই পরিমাণের ওজন তালিকায় নেই ("+detailOf(err)+")। গ্রামে লিখুন, অথবা মাপটি যোগ করার অনুরোধ করুন।").WithDetail(err)
	case errors.Is(err, ErrBadQuantity):
		return errs.ErrValidation.WithFieldIn("quantity",
			"Say how many — more than none, and fewer than a hundred.",
			"কয়টি তা লিখুন — শূন্যের বেশি, একশোর কম।")
	case errors.Is(err, ErrBadMeal):
		return errs.ErrValidation.WithFieldIn("meal",
			"Choose one of the day's meals.",
			"দিনের যেকোনো একটি খাবারের সময় বেছে নিন।")
	case errors.Is(err, ErrBadDate):
		return errs.ErrValidation.WithFieldIn("recall_date",
			"That is not a day. A recall taken today is usually about yesterday.",
			"এটি কোনও দিন নয়। আজ নেওয়া রিকল সাধারণত গতকালের খাবারের।")
	case errors.Is(err, ErrNoEntry):
		return errs.ErrNotFound
	case errors.Is(err, ErrAlreadyWithdrawn):
		return errs.New("DIET_ENTRY_ALREADY_WITHDRAWN", errs.KindConflict, http.StatusConflict,
			"Somebody has already taken that entry back.",
			"ওই এন্ট্রিটি আগেই কেউ ফিরিয়ে নিয়েছেন।").WithDetail(err)
	case errors.Is(err, ErrReasonRequired):
		return errs.ErrValidation.WithFieldIn("reason",
			"Say why. An entry that vanishes with no reason is a gap the other operator has to explain.",
			"কারণ লিখুন। কারণ ছাড়া এন্ট্রি মুছে গেলে অন্য অপারেটরকে তা ব্যাখ্যা করতে হয়।")
	default:
		return errs.ErrInternal.WithDetail(err)
	}
}
