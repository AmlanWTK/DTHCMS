package clinical

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// The reach check, for the routes in this module that name a patient (ADR-0036).
//
// Two shapes of route need it and they get the two different reaches:
//
//   - recording a value is a write, and reaches only the patient this station currently
//     holds. Recording a blood pressure against somebody who left an hour ago is what the
//     correction workflow exists to make visible, and it should not be reachable by typing
//     the wrong patient id into the form.
//   - reading values back is a read, and reaches every patient this station has had in the
//     current visit — because the nutritionist checking the weight the anthropometry desk
//     took is the whole design of the twelve-station flow.
//
// The patient id here comes from the request *body* for a write and from the path for a
// read, which looks like an asymmetry and is not: in both cases it is a value the client
// supplied and neither is trusted. What is trusted is the station, and the station comes
// from the confirmed role.

// mayReadPatient judges a read of one patient's observations.
func (h *Handlers) mayReadPatient(w http.ResponseWriter, r *http.Request, patientID uuid.UUID) bool {
	if err := rbac.AuthorizeStationRead(r.Context(), PermObservationRead, "observation", patientID); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return false
	}
	return true
}

// mayWriteFor judges a recording against one patient.
//
// The action is the *specific* write permission the value needs — an anthropometry code
// needs `observation.write.anthro` and a blood pressure needs `observation.write.vitals` —
// because the route's requirement is a union of seven of them and judging the union would
// judge nothing. recordingFrom has already worked out which one applies to this value; this
// is the reach half of the same question.
func (h *Handlers) mayWriteFor(w http.ResponseWriter, r *http.Request, action string, patientID uuid.UUID) bool {
	if err := rbac.AuthorizeStationWrite(r.Context(), action, "observation", patientID); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return false
	}
	return true
}
