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
	reader, err := eventstore.ReaderFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	growth, err := h.service.GrowthFor(r.Context(), id, reader.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}

	// The obesity flag travels with the current BMI-for-age, because it *is* a reading of
	// that value and computing it anywhere else would be a second copy of [R-06]'s
	// threshold. CP48 draws it; nothing recomputes it, and since CP73's snapshot panel needs
	// the same four values, [Service.WeightStatus] is the one place they are produced.
	//
	// A failure to read the reference tables leaves the flag off and the percentiles on. The
	// card says which of its parts is missing; refusing the whole growth response because one
	// interpolation could not be done would take the percentiles away too.
	response := map[string]any{"growth": growth}
	if status, err := h.service.WeightStatus(r.Context(), growth); err == nil && status != nil {
		response["weight_status"] = status
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
