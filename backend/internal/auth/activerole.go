package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// In-session role switching (CP41, [R-02]).
//
// The blueprint states the staffing reality plainly: "the same assistant enters BP, then
// switches to anthropometry entry, from the same phone." A clinic of twelve stations and
// nine staff cannot afford one login per hat.
//
// # Why this endpoint is not a token exchange
//
// The plan sketches "a role-switch endpoint issuing an updated token", and this is
// deliberately not that. Under ADR-0011 the session token is opaque and carries no
// authority: roles and permissions are read live on every request, which is what makes a
// grant revoked a minute ago absent from the next response without anything being reissued.
// A token that carried the active role would put authority back inside a bearer string and
// undo that, and it would mean a revoked role kept working until its token expired.
//
// So the active role travels as `X-Active-Role`, request by request, and the authorisation
// engine refuses one the person does not hold — which is criterion 4, enforced where every
// other authority decision is enforced rather than at a switch endpoint a client could skip.
//
// What this endpoint does is the part that genuinely has no other home:
//
//   - it **confirms** the role is held and hands back that role's permissions and station,
//     so the interface can redraw itself to one hat's worth of forms without guessing;
//   - it **records the switch**, which criterion 2's "switching is logged" asks for and
//     which no per-request header can provide.
//
// The second is the valuable one. Every event already carries the role active at write
// time, so "which hat were they wearing" is answerable one event at a time. What the events
// cannot answer is "when did they change, and to what" — the question somebody asks when a
// whole run of entries looks wrong, and the one this row answers.

// activeRoleRequest is the switch.
type activeRoleRequest struct {
	Role string `json:"role"`
	// From is the role the interface believed was active. Optional, and used only to make
	// the audit sentence readable — the server does not trust it for anything, because a
	// client that lied about it would be lying about its own previous state.
	From string `json:"from"`
}

type activeRoleResponse struct {
	Role  string    `json:"role"`
	Grant grantView `json:"grant"`
}

func (h *Handlers) switchActiveRole(w http.ResponseWriter, r *http.Request) {
	user, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	var req activeRoleRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	wanted := RoleCode(strings.ToUpper(strings.TrimSpace(req.Role)))
	if wanted == "" {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("role",
			"Say which role to act as.", "কোন ভূমিকায় কাজ করবেন তা জানান।"))
		return
	}

	// The whole of the check. `describe` reads the grants live from the database, so a role
	// revoked while the person was holding the phone is simply not in the list — no cache to
	// invalidate, no token to reissue.
	view := h.describe(r.Context(), user)
	var granted *grantView
	for i := range view.Grants {
		if RoleCode(view.Grants[i].Role) == wanted {
			granted = &view.Grants[i]
			break
		}
	}
	if granted == nil {
		// Criterion 4. Deliberately a 403 and not a 404: the roles a person holds are not a
		// secret from them, and "you do not have that role" is the sentence that makes the
		// switcher's disabled entries make sense.
		httpx.WriteError(w, r, h.logger, errs.ErrForbidden.WithDetail(
			errors.New("auth: that role is not granted to this user")))
		return
	}

	h.recordSwitch(r, user, req.From, string(wanted))

	httpx.WriteJSON(w, http.StatusOK, activeRoleResponse{
		Role: string(wanted), Grant: *granted,
	})
}

// recordSwitch writes the audit row. A failure to record is logged and not returned: the
// switch has already been *decided* — the client is going to send the new role on its next
// request whatever this endpoint answers, because the header is the mechanism. Failing the
// response would teach operators that the switcher is unreliable while changing nothing
// about what they can do.
func (h *Handlers) recordSwitch(r *http.Request, user User, from, to string) {
	if h.audit == nil {
		return
	}
	before := strings.ToUpper(strings.TrimSpace(from))
	if before == "" {
		if caller, ok := httpx.CallerFrom(r.Context()); ok {
			before = caller.ActiveRole
		}
	}
	if before == "" {
		before = "—"
	}
	target := user.ID
	entry := AuditEntry{
		Kind: "role.switched", ActorID: user.ID, ActorCode: user.Code, ActorRole: before,
		FacilityID: user.FacilityID, TargetUserID: &target, TargetCode: user.Code,
		Before:       map[string]any{"role": before},
		After:        map[string]any{"role": to},
		ClientDigest: clientDigest(r),
		At:           time.Now().UTC(),
	}
	if err := h.audit.RecordAudit(r.Context(), entry); err != nil {
		h.logger.Warn("a role switch was not written to the audit trail",
			"user", user.ID.String(), "to", to, "error", err.Error())
	}
}
