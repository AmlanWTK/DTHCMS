package clinical

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// The batch write endpoint (CP45).
//
// One request, one transaction, one round trip: the shape a station form actually has. See
// batch.go for why the transaction matters and why the derivations run inside it.

type batchRequest struct {
	// EventID is the batch's own id, which seeds the derived values' ledger ids so a retry
	// is absorbed rather than doubled.
	EventID   string `json:"event_id"`
	PatientID string `json:"patient_id"`
	VisitID   string `json:"visit_id,omitempty"`
	// Observations are the measured values, each exactly as the single-value endpoint takes
	// them. The same shape on purpose: a client that can write one value can write six
	// without learning a second vocabulary.
	Observations []recordRequest `json:"observations"`
	// Derive names the values the server should compute once the measurements have landed.
	Derive []string `json:"derive,omitempty"`
}

func (h *Handlers) recordBatch(w http.ResponseWriter, r *http.Request) {
	var req batchRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	principal, ok := httpx.PrincipalFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	eventID, bad := requiredUUID(req.EventID, "event_id")
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}
	patientID, bad := requiredUUID(req.PatientID, "patient_id")
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}

	in := Batch{
		EventID: eventID, PatientID: patientID,
		// This clinic. The library serves both scales and CP44's display shows which was
		// used, so the choice is visible rather than buried.
		AsianScale: true, LedgerSource: sourceOf(r),
	}
	if req.VisitID != "" {
		id, bad := requiredUUID(req.VisitID, "visit_id")
		if bad != nil {
			httpx.WriteError(w, r, h.logger, bad)
			return
		}
		in.VisitID = &id
	}

	for i := range req.Observations {
		item := req.Observations[i]
		// A batch says the patient once. Repeating it per item is a field a client can get
		// wrong in one of six places, which is exactly the mistake the service refuses —
		// so fill it in rather than asking.
		if strings.TrimSpace(item.PatientID) == "" {
			item.PatientID = req.PatientID
		}
		recording, err := h.recordingFrom(r, principal, item)
		if err != nil {
			// The index, because an operator who gets "that is not an observation code"
			// against a form of six values needs to know which one.
			httpx.WriteError(w, r, h.logger, withIndex(err, i))
			return
		}
		if in.VisitID != nil && recording.VisitID == nil {
			recording.VisitID = in.VisitID
		}
		in.Records = append(in.Records, recording)
	}

	for _, name := range req.Derive {
		what := Derivable(strings.ToUpper(strings.TrimSpace(name)))
		if !derivable(what) {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("derive",
				"That is not a value this server derives.",
				"এই মানটি সার্ভার হিসাব করে না।"))
			return
		}
		in.Derive = append(in.Derive, what)
	}

	written, err := h.service.RecordBatch(r.Context(), in)
	if err != nil {
		var item *BatchItemError
		if errors.As(err, &item) {
			httpx.WriteError(w, r, h.logger, withIndex(translate(item.Err), item.Index))
			return
		}
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"observations": written})
}

// withIndex says which value in the batch was refused. A validation failure against a form
// of six numbers is useless without it: "that is not an observation code" sends the operator
// to re-read all six.
//
// The index is added as an extra field rather than by rewriting the existing ones, so a
// client that already reads `fields.code` keeps working and one that wants to highlight the
// right row can.
func withIndex(err error, index int) error {
	problem := errs.From(err)
	position := strconv.Itoa(index + 1)
	return problem.WithFieldIn("observation",
		"Value "+position+" of this entry.",
		"এই এন্ট্রির "+position+" নম্বর মান।")
}
