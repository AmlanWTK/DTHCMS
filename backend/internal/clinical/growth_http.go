package clinical

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// Growth over HTTP (CP47, read by CP48's card and chart).
//
// Two endpoints, and the split matters. The patient's own growth is patient data and needs
// `observation.read.values`; the reference curves are **published tables** and are the same
// for every child in the world, so they are their own endpoint that a client can fetch once
// and cache for the session. Serving the curves inside the patient response would mean
// re-sending eight hundred points every time somebody opened a chart.

func (h *Handlers) growthForPatient(w http.ResponseWriter, r *http.Request) {
	id, ok := h.idParam(w, r, "id")
	if !ok {
		return
	}
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	growth, err := h.service.GrowthFor(r.Context(), id, actor.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}

	// The obesity flag travels with the current BMI-for-age, because it *is* a reading of
	// that value and computing it anywhere else would be a second copy of [R-06]'s
	// threshold. CP48 draws it; nothing recomputes it.
	response := map[string]any{"growth": growth}
	if current, ok := growth.Current[BMIForAge]; ok {
		ninetyFifth, err := h.service.valueAtPercentile(r.Context(), BMIForAge, growth.Sex,
			current.AgeMonths, 95)
		if err == nil {
			name, ratio := obesityFlag(current, ninetyFifth)
			response["weight_status"] = map[string]any{
				"class": name,
				// Percent of the 95th percentile — CDC's own convention, and the only thing
				// that discriminates above the 99th percentile, where the percentile scale
				// stops telling two very different children apart.
				"percent_of_95th": ratio,
				"bmi_at_95th":     ninetyFifth,
				"standard":        current.Standard,
			}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, response)
}

func (h *Handlers) growthCurves(w http.ResponseWriter, r *http.Request) {
	indicator := Indicator(strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("indicator"))))
	known := false
	for _, candidate := range Indicators {
		if candidate == indicator {
			known = true
		}
	}
	if !known {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("indicator",
			"Ask for HFA, WFA or BFA.", "HFA, WFA বা BFA চান।"))
		return
	}
	sex := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sex")))
	if sex != "male" && sex != "female" {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("sex",
			"The published tables cover two sexes.", "প্রকাশিত টেবিল দুটি লিঙ্গ কভার করে।"))
		return
	}

	from, to := 0.0, 240.5
	if raw := r.URL.Query().Get("from_months"); raw != "" {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil {
			from = parsed
		}
	}
	if raw := r.URL.Query().Get("to_months"); raw != "" {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil {
			to = parsed
		}
	}

	curves, err := h.service.CurvesFor(r.Context(), indicator, sex, from, to)
	if errors.Is(err, ErrNotApplicable) {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("sex",
			"The published tables cover two sexes.", "প্রকাশিত টেবিল দুটি লিঙ্গ কভার করে।"))
		return
	}
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"curves": curves})
}
