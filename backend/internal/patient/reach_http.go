package patient

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// The patient in the path, and whether this person may have it (ADR-0036).
//
// Two functions rather than one with a flag, and the reason is the one the engine gives for
// having two reaches at all: a read reaches every patient this station has had in the
// current visit, and a write reaches only the one it currently holds. A `patientInPath(w, r,
// perm, write bool)` would put that difference at thirty call sites as a boolean literal,
// where it is invisible in review and wrong exactly once.
//
// Both refuse the same way a bad id does. A 404 for a malformed id and a 403 for a patient
// out of reach would let a caller sort ids into "exists" and "does not", which is the
// enumeration a refusal is supposed to close rather than open — so the refusal the engine
// returns is written through the same writer with the same envelope, and only the log can
// tell the two apart.

// patientToRead resolves the {id} in the path and judges whether the caller may read it.
func (h *Handlers) patientToRead(w http.ResponseWriter, r *http.Request, action rbac.Action) (uuid.UUID, bool) {
	id, ok := h.patientParam(w, r)
	if !ok {
		return uuid.Nil, false
	}
	if err := rbac.AuthorizeStationRead(r.Context(), action, "patient", id); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return uuid.Nil, false
	}
	return id, true
}

// patientToWrite resolves the {id} in the path and judges whether the caller may record
// against it now.
func (h *Handlers) patientToWrite(w http.ResponseWriter, r *http.Request, action rbac.Action) (uuid.UUID, bool) {
	id, ok := h.patientParam(w, r)
	if !ok {
		return uuid.Nil, false
	}
	if err := rbac.AuthorizeStationWrite(r.Context(), action, "patient", id); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return uuid.Nil, false
	}
	return id, true
}
