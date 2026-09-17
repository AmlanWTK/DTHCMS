package signing

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/prescription"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// Signing over HTTP (CP84).
//
// # Three routes, and the step-up is on exactly one of them
//
//   - `POST /v1/prescriptions/{id}/signature` — signs. `prescription.sign`, **plus a step-up**
//     for `auth.PurposeSignPrescription`. The purpose is the point: a token minted to suspend a
//     colleague, to override a QA finding or to export research data is refused here, because
//     `httpx.RequireStepUp` compares the purpose the token was minted for against the purpose
//     this route names, and the token is consumed on use so it authorises this signature and no
//     other.
//   - `GET /v1/prescriptions/{id}/signature` — the signature and a fresh verification.
//     `prescription.read`.
//   - `GET /v1/ops/verification-attempts` — the public endpoint's log, for the operations
//     console. `prescription.read` **is not enough**: the log is the abuse record for the
//     system's only public surface and belongs with the other operational views.
//
// # Every route declares and judges its resource (ADR-0036)
//
// Both per-prescription routes are `httpx.PermissionScoped`, resolve the prescription to its
// patient, and then ask `rbac.GuardPatientWrite` (signing) or `rbac.GuardPatientRead`
// (reading). The resolution happens **before** the authorisation check and both outcomes map to
// the same response, so a 403 does not say whether the prescription exists: a handler answering
// 404 for "no such prescription" and 403 for "not yours" is an oracle somebody walks ids
// through.
//
// # Why signing asks the *write* reach and reading asks the *read* one
//
// Signing a prescription is the most consequential write in this system. `AuthorizeStationWrite`
// is the narrower of the two reaches — only the patient this station currently holds — and it is
// the right one here for the same reason CP62 uses it for recording a value. A physician whose
// role is facility-wide is unaffected; a station role that somehow acquired `prescription.sign`
// would be held to the patient in front of them.
//
// # The response never carries the private key, and carries the token exactly once
//
// The signing response is the only place the verification token exists outside the printed
// sheet. It is not stored, not logged and not retrievable afterwards — see token.go. A physician
// who loses it before printing has to correct the prescription, which is the right cost for a
// value that must not be re-derivable.

const (
	// PermSign is CP15's existing permission. **No new permission**: `prescription.sign` has
	// been in the catalogue since CP15 and PHYSICIAN holds it, which is §4.4's access matrix.
	// Inventing a second one would have meant invariant 43 — the assertion that the matrix in
	// the blueprint is the matrix in the database — reporting a grant nobody decided.
	PermSign = "prescription.sign"
	// PermRead reads a signature and verifies it. The same permission that reads the
	// prescription, because a signature is a fact about a prescription and there is no reader
	// who may see the sheet and may not see whether it verifies.
	PermRead = prescription.PermRead

	// PurposeSign is the step-up purpose signing needs. `auth.PurposeSignPrescription` is the
	// same string; a contract test holds them equal, as it does for every other purpose.
	PurposeSign = "prescription.sign"
)

// Handlers serve signing.
type Handlers struct {
	service *Service
	store   *Store
	sheets  Sheets
	// stepUp verifies the second factor. Nil makes every signing refuse, which is the right
	// failure for a missing verifier: a signing route with no step-up is a hole, not a head
	// start — the same sentence CP83 wrote about its override and the same reason.
	stepUp httpx.StepUpVerifier
	logger *slog.Logger
}

// HandlersConfig builds them.
type HandlersConfig struct {
	Service *Service
	Store   *Store
	Sheets  Sheets
	StepUp  httpx.StepUpVerifier
	Logger  *slog.Logger
}

// NewHandlers builds the handlers.
func NewHandlers(cfg HandlersConfig) *Handlers {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Handlers{service: cfg.Service, store: cfg.Store, sheets: cfg.Sheets,
		stepUp: cfg.StepUp, logger: logger}
}

// Mount wires the authenticated routes.
func (h *Handlers) Mount(r chi.Router) {
	r.Route("/prescriptions/{prescriptionId}/signature", func(one chi.Router) {
		one.Method("GET", "/", httpx.Declare(httpx.PermissionScoped(PermRead), h.read))

		// The step-up sits between the permission and the handler. `RequireStepUp` consumes the
		// token, so a replayed one is refused by the second presentation rather than by
		// anything this handler remembers to check.
		//
		// **Not `PermissionScoped`, and the reason is the same one CP83 wrote down.** The only
		// role holding `prescription.sign` is PHYSICIAN, which reaches the whole facility, so
		// the route guard's decision is already the whole decision and
		// `TestNoRouteDeclaresAResourceCheckItDoesNotNeed` refuses a declaration that invites a
		// check nobody owes. The handler still calls `rbac.GuardPatientWrite` on the patient it
		// resolved; today that call cannot refuse anybody who reached the handler, and it is
		// **named as cosmetic in this checkpoint's report** for exactly that reason. It is kept
		// because it becomes load-bearing the day somebody grants `prescription.sign` to a
		// station role, and a door added later is a door somebody has to remember to add.
		stepped := httpx.RequireStepUp(h.logger, h.stepUp, PurposeSign)(http.HandlerFunc(h.sign))
		one.Method("POST", "/", httpx.Declare(httpx.Permission(PermSign), stepped.ServeHTTP))
	})
}

// MountOps attaches the public endpoint's abuse log.
//
// Separate from [Mount] because its reader is different: this is not a clinical view of a
// prescription, it is the monitoring the plan asks for on the system's only public surface, and
// it belongs beside `/v1/ops/jobs` and `/v1/ops/ai` rather than inside `/v1/prescriptions`.
func (h *Handlers) MountOps(r chi.Router) {
	r.Method("GET", "/ops/verification-attempts",
		httpx.Declare(httpx.Permission(PermVerificationLogRead), h.attempts))
}

// PermVerificationLogRead reads the public endpoint's attempt log.
//
// `audit.read`: the log is a security record about strangers probing a public surface, not a
// clinical record about a prescription, and the person who reads it is the person who reads the
// security dashboard.
const PermVerificationLogRead = "audit.read"

// ---------------------------------------------------------------------------
// Signing
// ---------------------------------------------------------------------------

type signBody struct {
	// EventID is the caller's idempotency handle on the ledger append, as on every other
	// mutating route in this system.
	EventID string `json:"event_id"`
}

// sign signs a cleared prescription. `prescription.sign`, with a step-up.
func (h *Handlers) sign(w http.ResponseWriter, r *http.Request) {
	id, sheet, ok := h.resolve(w, r)
	if !ok {
		return
	}
	if !rbac.GuardPatientWrite(w, r, h.logger, PermSign, "prescription", sheet.PatientID) {
		return
	}

	var body signBody
	if r.Body != nil {
		// A body is optional: the event id is a convenience for a client that wants to retry
		// safely, and a signing with no body mints one. Decoding failure is refused rather than
		// ignored, because a client that sent something malformed did not mean to send nothing.
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil &&
			!errors.Is(err, errEmptyBody) && err.Error() != "EOF" {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithDetail(err))
			return
		}
	}
	eventID := uuid.Nil
	if body.EventID != "" {
		parsed, err := uuid.Parse(body.EventID)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithDetail(err))
			return
		}
		eventID = parsed
	}

	signed, err := h.service.Sign(r.Context(), eventID, id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"signature": signed.Signature,
		// The token, exactly once. See token.go: what the database holds is its digest.
		"verification_token": signed.Token,
		// The path a QR code encodes. Composed here rather than on the client so that the
		// printed sheet and the public page cannot disagree about what a token URL looks like.
		"verification_path": VerificationPath(signed.Token),
		// §6, on every response that reports a signature: the picture on the paper is not this.
		"signature_image": TheImageIsNotTheSignature(),
	})
}

// errEmptyBody is the sentinel a missing body decodes to. Declared so the comparison above is a
// named thing rather than a string match on somebody else's error text.
var errEmptyBody = errors.New("EOF")

// ---------------------------------------------------------------------------
// Reading and verifying
// ---------------------------------------------------------------------------

// read returns the signature and a fresh verification, or — on a prescription that has not been
// signed — why it cannot be. `prescription.read`.
//
// **It answers 200 for an unsigned prescription rather than 403.** The refusal this route used to
// give was correct about the danger and wrong about where it sits: what must not be knowable is
// whether a prescription *exists*, and `resolve` has already settled that before this runs. A
// caller who reached here holds `prescription.read` on this patient in this facility. See the
// readiness block below for why they need the answer.
//
// **It verifies on every call rather than returning a stored verdict.** A cached verdict answers
// "did this verify when somebody last looked", which is a different question and the wrong one:
// the whole value of the signature is that it is recomputed against what is stored *now*.
func (h *Handlers) read(w http.ResponseWriter, r *http.Request) {
	id, sheet, ok := h.resolve(w, r)
	if !ok {
		return
	}
	if !rbac.GuardPatientRead(w, r, h.logger, PermRead, "prescription", sheet.PatientID) {
		return
	}
	// **Readiness is answered for an unsigned prescription too, and that is not a leak.** The
	// caller has already passed `resolve` — which proved the prescription exists in *their*
	// facility — and `GuardPatientRead`. Telling them, at that point, that their own patient's
	// prescription is not yet cleared reveals nothing they could not learn by reading the
	// prescription itself. A caller who fails either gate still gets the one 403 that does not
	// say whether anything exists.
	//
	// It exists because the prescriber cannot read station 10's answer any other way:
	// `GET /v1/prescriptions/{id}/qa` is behind `qa.review`, which PHYSICIAN does not hold. A
	// screen with no way to ask would have to offer the sign control always and let the
	// physician find the refusal — which is the CP92 defect, on the most consequential write in
	// this system.
	readiness, err := h.service.SigningReadiness(r.Context(), sheet.FacilityID, id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	body := map[string]any{
		"readiness": readiness,
		"signed":    readiness.Signed,
		// §6, beside every report of a signature: the picture on the paper is not this.
		"signature_image": TheImageIsNotTheSignature(),
	}
	if readiness.Signed {
		// Verified on this call rather than reported from a stored verdict. See the doc comment.
		verification, err := h.service.Verify(r.Context(), sheet.FacilityID, id)
		if err != nil {
			httpx.WriteError(w, r, h.logger, translate(err))
			return
		}
		body["verification"] = verification
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

func (h *Handlers) attempts(w http.ResponseWriter, r *http.Request) {
	attempts, err := h.store.Attempts(r.Context(), 100)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	// Digests, never addresses, and never a patient. The shape is deliberately awkward to read
	// by hand: this view answers "how much is somebody probing us", not "who scanned what".
	rows := make([]map[string]any, 0, len(attempts))
	for _, one := range attempts {
		rows = append(rows, map[string]any{
			"outcome": one.Outcome, "at": one.At,
			"client_group": shortDigest(one.ClientDigest),
			"verified":     one.Outcome == "VERIFIED",
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"attempts": rows})
}

// ---------------------------------------------------------------------------
// Plumbing
// ---------------------------------------------------------------------------

// resolve loads the prescription named in the path, in the caller's facility.
//
// It resolves **before** any authorisation decision so that "no such prescription" and "not
// yours" leave through the same door. Both answer 403, which is `errs.ErrForbidden`'s documented
// behaviour and the reason it exists.
func (h *Handlers) resolve(w http.ResponseWriter, r *http.Request) (uuid.UUID, prescription.Prescription, bool) {
	principal, present := httpx.PrincipalFrom(r.Context())
	if !present {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return uuid.Nil, prescription.Prescription{}, false
	}
	id, err := uuid.Parse(chi.URLParam(r, "prescriptionId"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrForbidden)
		return uuid.Nil, prescription.Prescription{}, false
	}
	facility, err := uuid.Parse(principal.FacilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrForbidden)
		return uuid.Nil, prescription.Prescription{}, false
	}
	sheet, err := h.sheets.ByID(r.Context(), id, facility)
	if err != nil {
		// Including ErrNotFound. See the doc comment.
		httpx.WriteError(w, r, h.logger, errs.ErrForbidden)
		return uuid.Nil, prescription.Prescription{}, false
	}
	return id, sheet, true
}

// translate turns a domain error into the one HTTP answer it maps to.
func translate(err error) error {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrNotSigned):
		// Both 403, not 404. A signature endpoint that answered 404 for "not signed" and 403
		// for "not yours" would tell a caller which prescriptions exist.
		return errs.ErrForbidden
	case errors.Is(err, ErrAlreadySigned):
		return errs.ErrConflict.WithDetail(err)
	case errors.Is(err, ErrNotCleared):
		return errs.ErrConflict.WithDetail(err)
	case errors.Is(err, ErrManagedSignerUnavailable):
		// A deployment problem, not the physician's. 503 rather than 500 so that an operator
		// reading the dashboard sees a dependency rather than a bug.
		return errs.ErrUnavailable.WithDetail(err)
	}
	var transition *prescription.TransitionError
	if errors.As(err, &transition) {
		return errs.ErrConflict.WithDetail(err)
	}
	return errs.ErrInternal.WithDetail(err)
}

// shortDigest is the first four bytes of a digest, hex, for grouping in a console.
//
// Four bytes, so that two attempts from the same client group together and nobody is tempted to
// treat the value as an address. A full digest in a console is a full digest in a screenshot.
func shortDigest(digest []byte) string {
	const hexDigits = "0123456789abcdef"
	if len(digest) < 4 {
		return ""
	}
	out := make([]byte, 0, 8)
	for _, b := range digest[:4] {
		out = append(out, hexDigits[b>>4], hexDigits[b&0x0f])
	}
	return string(out)
}
