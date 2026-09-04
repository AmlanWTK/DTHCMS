package patient

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/google/uuid"
)

// Corrections over HTTP (CP35).
//
// `PATCH`, because a correction is a change to some of a record and a `PUT` of the whole
// thing would make "which fields did the operator actually alter" unanswerable — and that is
// exactly the question the history has to answer.

// PurposeCorrectIdentity is the step-up a high-impact correction needs. Its own purpose, so
// a token minted to merge two records cannot be spent changing a date of birth.
const PurposeCorrectIdentity = "patient_correct_identity"

func (h *Handlers) mountCorrection(p chi.Router) {
	write := httpx.Permission(PermPatientWriteDemographics)
	read := httpx.Permission(PermPatientReadDemographics)
	p.Method("PATCH", "/{id}", httpx.Declare(write, h.correct))
	p.Method("GET", "/{id}/history", httpx.Declare(read, h.history))
}

type correctionRequest struct {
	EventID string `json:"event_id"`
	Reason  string `json:"reason"`

	// Pointers throughout: a field absent from the body is a field the operator is not
	// touching, and a form that renders six fields must not rewrite five of them.
	NameEN         *string `json:"name_en"`
	NameBN         *string `json:"name_bn"`
	Sex            *string `json:"sex"`
	BirthDate      *string `json:"birth_date"`
	DOBPrecision   *string `json:"dob_precision"`
	DOBSource      *string `json:"dob_source"`
	PhonePrimary   *string `json:"phone_primary"`
	PhoneSecondary *string `json:"phone_secondary"`
	Division       *string `json:"division"`
	District       *string `json:"district"`
	Upazila        *string `json:"upazila"`
	AddressLine    *string `json:"address_line"`
	Postcode       *string `json:"postcode"`
}

func (h *Handlers) correct(w http.ResponseWriter, r *http.Request) {
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	id, ok := h.patientParam(w, r)
	if !ok {
		return
	}
	var req correctionRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	eventID, err := uuid.Parse(req.EventID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("event_id",
			"A client-generated UUID is required.", "ক্লায়েন্ট-নির্মিত একটি UUID আবশ্যক।"))
		return
	}

	correction := req.correction(eventID, sourceOf(r))

	// The step-up is demanded before the work, not after: the operator should be asked for
	// their code while they still have the form in front of them.
	current, err := h.store.ByID(r.Context(), id, actor.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	if correction.NeedsStepUp(current) {
		if err := h.verifyStepUp(r, PurposeCorrectIdentity); err != nil {
			httpx.WriteError(w, r, h.logger, err)
			return
		}
	}

	applied, err := h.service.Correct(r.Context(), id, correction)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateCorrection(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"patient":     h.view(r, applied.Patient),
		"changes":     applied.Changes,
		"high_impact": applied.HighImpact,
		// What this invalidated, so the interface can say "three growth percentiles were
		// recomputed" rather than leaving the operator to wonder.
		"invalidated": applied.Invalidated,
		"event_id":    applied.Event.EventID,
	})
}

// verifyStepUp checks the token the request carries for one purpose.
//
// Done here rather than by the route's middleware because whether a step-up is needed
// depends on *what changed*, which is only known once the body is read against the record.
func (h *Handlers) verifyStepUp(r *http.Request, purpose string) error {
	if h.stepUp == nil {
		return errs.ErrStepUpRequired
	}
	token := r.Header.Get(httpx.StepUpHeader)
	if token == "" {
		return errs.ErrStepUpRequired
	}
	caller, _ := httpx.CallerFrom(r.Context())
	if err := h.stepUp.ConsumeStepUp(r.Context(), token, caller.UserID, purpose); err != nil {
		return errs.ErrStepUpRequired.WithDetail(err)
	}
	return nil
}

type correctionView struct {
	Field           string    `json:"field"`
	Previous        string    `json:"previous"`
	Current         string    `json:"current"`
	Reason          string    `json:"reason"`
	HighImpact      bool      `json:"high_impact"`
	CorrectedByCode string    `json:"corrected_by_code"`
	CorrectedAt     time.Time `json:"corrected_at"`
	EventID         uuid.UUID `json:"event_id"`
}

func (h *Handlers) history(w http.ResponseWriter, r *http.Request) {
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	id, ok := h.patientParam(w, r)
	if !ok {
		return
	}
	if _, err := h.store.ByID(r.Context(), id, actor.FacilityID()); err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	rows, err := h.store.History(r.Context(), id, actor.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	out := make([]correctionView, 0, len(rows))
	for _, row := range rows {
		out = append(out, correctionView{
			Field: row.Field, Previous: row.Previous, Current: row.Current,
			Reason: row.Reason, HighImpact: row.HighImpact,
			CorrectedByCode: row.CorrectedByCode, CorrectedAt: row.CorrectedAt,
			EventID: row.EventID,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"corrections": out})
}

func (r correctionRequest) correction(eventID uuid.UUID, source eventstore.Source) Correction {
	return Correction{
		EventID: eventID, Reason: r.Reason, Source: source,
		NameEN: r.NameEN, NameBN: r.NameBN, Sex: r.Sex,
		BirthDate: r.BirthDate, DOBPrecision: r.DOBPrecision, DOBSource: r.DOBSource,
		PhonePrimary: r.PhonePrimary, PhoneSecondary: r.PhoneSecondary,
		Division: r.Division, District: r.District, Upazila: r.Upazila,
		AddressLine: r.AddressLine, Postcode: r.Postcode,
	}
}

func translateCorrection(err error) error {
	switch {
	case errors.Is(err, ErrNothingToCorrect):
		return errs.ErrValidation.WithFieldIn("changes",
			"Nothing in that form differs from the record.",
			"এই ফর্মের কিছুই রেকর্ড থেকে আলাদা নয়।").WithDetail(err)
	case errors.Is(err, ErrReasonRequired):
		return errs.ErrValidation.WithFieldIn("reason",
			"Say why the record is being corrected. Somebody may read this in a year.",
			"রেকর্ডটি কেন সংশোধন করছেন লিখুন। এক বছর পরেও কেউ পড়তে পারেন।").WithDetail(err)
	}
	return translateForClient(err)
}
