package education

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// The reach check for this station's two patient routes (ADR-0036).
//
// Two strengths, and the difference is the ADR's rather than this module's:
//
//   - **Reading** reaches every patient this station has had in the current visit, `done`
//     included. The officer must be able to re-open what they just recorded, and the physician —
//     whose reach is facility-wide — must be able to see last visit's competency at this one,
//     which is CP92's second acceptance criterion and would be unreachable under a write-strength
//     rule.
//   - **Recording** reaches only the patient this station is currently holding. An assessment
//     written against somebody who left an hour ago is what the correction workflow exists to
//     make visible, and it should not be reachable by typing the wrong id into a form.
//
// The patient id comes from the path in both cases, and neither is trusted. What is trusted is
// the station, and the station comes from the confirmed role — never from a header.

func (h *Handlers) mayRead(w http.ResponseWriter, r *http.Request, patient uuid.UUID) bool {
	if err := rbac.AuthorizeStationRead(r.Context(), PermRead, "education", patient); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return false
	}
	return true
}

func (h *Handlers) mayWrite(w http.ResponseWriter, r *http.Request, patient uuid.UUID) bool {
	if err := rbac.AuthorizeStationWrite(r.Context(), PermRecord, "education", patient); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return false
	}
	return true
}
