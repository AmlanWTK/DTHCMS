package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// AdminHandlers serve /v1/admin (CP21).
//
// Every write needs a step-up (CP21 criterion 4): a session left open on a desk cannot
// invite a colleague or reset a password. Reads need the permission alone. The step-up
// purpose splits in two — managing an account and resetting a credential — so a token
// minted for one cannot be spent on the other.
type AdminHandlers struct {
	admin        *Admin
	secondFactor *SecondFactor
	logger       *slog.Logger
}

type AdminHandlersConfig struct {
	Admin        *Admin
	SecondFactor *SecondFactor
	Logger       *slog.Logger
}

func NewAdminHandlers(cfg AdminHandlersConfig) *AdminHandlers {
	return &AdminHandlers{admin: cfg.Admin, secondFactor: cfg.SecondFactor, logger: cfg.Logger}
}

// guarded declares a write: the permission is decided first, the step-up second. The
// order matters for what a caller learns — someone without the permission gets the same
// FORBIDDEN every other route gives them, never a STEP_UP_REQUIRED that would tell them
// the door exists and which key it takes.
func (h *AdminHandlers) guarded(perm httpx.Requirement, purpose string, handler http.HandlerFunc) http.Handler {
	var verifier httpx.StepUpVerifier
	if h.secondFactor != nil {
		verifier = &StepUpAdapter{SecondFactor: h.secondFactor}
	}
	stepped := httpx.RequireStepUp(h.logger, verifier, purpose)(handler)
	return httpx.Declare(perm, stepped.ServeHTTP)
}

// Mount attaches the console's endpoints under /v1/admin.
func (h *AdminHandlers) Mount(r chi.Router) {
	read := httpx.Permission(PermUserRead)
	reset := httpx.Permission(PermUserCredentialReset)

	r.Route("/admin", func(a chi.Router) {
		a.Method("GET", "/roles", httpx.Declare(read, h.roles))
		a.Method("GET", "/users", httpx.Declare(read, h.list))
		a.Method("GET", "/users/{id}", httpx.Declare(read, h.get))

		a.Method("POST", "/users", h.guarded(httpx.Permission(PermUserInvite), PurposeManageUsers, h.invite))
		a.Method("POST", "/users/{id}/status",
			h.guarded(httpx.Permission(PermUserInvite, PermUserSuspend, PermUserDeactivate), PurposeManageUsers, h.status))
		a.Method("POST", "/users/{id}/roles", h.guarded(httpx.Permission(PermRoleGrant), PurposeManageUsers, h.grant))
		a.Method("POST", "/users/{id}/roles/{role}/revoke", h.guarded(httpx.Permission(PermRoleRevoke), PurposeManageUsers, h.revoke))

		a.Method("POST", "/users/{id}/sessions/end", h.guarded(reset, PurposeResetCredential, h.endSessions))
		a.Method("POST", "/users/{id}/password", h.guarded(reset, PurposeResetCredential, h.setPassword))
		a.Method("POST", "/users/{id}/second-factor/reset", h.guarded(reset, PurposeResetCredential, h.resetSecondFactor))
	})
}

// --- views ---

type adminUserView struct {
	ID           string           `json:"id"`
	EmployeeCode string           `json:"employee_code"`
	NameEN       string           `json:"name_en"`
	NameBN       string           `json:"name_bn"`
	Phone        string           `json:"phone"`
	Email        string           `json:"email"`
	Status       string           `json:"status"`
	StatusReason string           `json:"status_reason"`
	StatusSince  time.Time        `json:"status_since"`
	LastLoginAt  *time.Time       `json:"last_login_at"`
	Roles        []string         `json:"roles"`
	Permissions  []string         `json:"permissions"`
	SecondFactor secondFactorView `json:"second_factor"`
	CreatedAt    time.Time        `json:"created_at"`
}

type adminAccountView struct {
	adminUserView
	Sessions []sessionView  `json:"sessions"`
	History  []grantHistory `json:"grant_history"`
}

type grantHistory struct {
	Role         string     `json:"role"`
	GrantedAt    time.Time  `json:"granted_at"`
	GrantedBy    *uuid.UUID `json:"granted_by"`
	RevokedAt    *time.Time `json:"revoked_at"`
	RevokedBy    *uuid.UUID `json:"revoked_by"`
	RevokeReason string     `json:"revoke_reason"`
}

type roleView struct {
	Code        string   `json:"code"`
	NameEN      string   `json:"name_en"`
	NameBN      string   `json:"name_bn"`
	IsClinical  bool     `json:"is_clinical"`
	Station     string   `json:"station"`
	Permissions []string `json:"permissions"`
}

func viewAccount(v AccountView) adminAccountView {
	roles := make([]string, 0, len(v.Roles))
	for _, r := range v.Roles {
		roles = append(roles, string(r.Code))
	}
	out := adminAccountView{
		adminUserView: adminUserView{
			ID: v.User.ID.String(), EmployeeCode: v.User.Code, NameEN: v.User.NameEN, NameBN: v.User.NameBN,
			Phone: v.User.Phone, Email: v.User.Email, Status: string(v.User.Status),
			StatusReason: v.User.StatusNote, StatusSince: v.User.StatusSince, LastLoginAt: v.User.LastLoginAt,
			Roles: roles, Permissions: v.Permissions.Codes(), SecondFactor: viewOf(v.SecondFactor),
			CreatedAt: v.User.CreatedAt,
		},
		Sessions: []sessionView{}, History: []grantHistory{},
	}
	for _, s := range v.Sessions {
		out.Sessions = append(out.Sessions, sessionView{
			ID: s.ID.String(), UserAgent: s.UserAgent, IssuedAt: s.IssuedAt, LastSeenAt: s.LastSeenAt,
		})
	}
	for _, g := range v.History {
		out.History = append(out.History, grantHistory{
			Role: string(g.RoleCode), GrantedAt: g.GrantedAt, GrantedBy: g.GrantedBy,
			RevokedAt: g.RevokedAt, RevokedBy: g.RevokedBy, RevokeReason: g.RevokeReason,
		})
	}
	return out
}

// --- helpers ---

func (h *AdminHandlers) actor(w http.ResponseWriter, r *http.Request) (Actor, bool) {
	caller, ok := httpx.CallerFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return Actor{}, false
	}
	userID, err1 := uuid.Parse(caller.UserID)
	facilityID, err2 := uuid.Parse(caller.FacilityID)
	if err1 != nil || err2 != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return Actor{}, false
	}
	return Actor{
		UserID: userID, FacilityID: facilityID, Permissions: NewPermissionSet(caller.Permissions...),
		ActiveRole: strings.TrimSpace(r.Header.Get(httpx.ActiveRoleHeader)),
	}, true
}

func (h *AdminHandlers) userID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return uuid.Nil, false
	}
	return id, true
}

func (h *AdminHandlers) writeAdminError(w http.ResponseWriter, r *http.Request, err error) {
	var transition *ErrTransition
	var validation *validationError
	switch {
	case errors.Is(err, ErrNotPermitted):
		httpx.WriteError(w, r, h.logger, errs.ErrForbidden)
	case errors.Is(err, ErrUserNotFound):
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
	case errors.Is(err, ErrEmployeeCodeUsed):
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithField("employee_code", "already in use"))
	case errors.Is(err, ErrUnknownRole):
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithField("role", err.Error()))
	case errors.Is(err, ErrWeakPassword):
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithField("password", err.Error()))
	case errors.Is(err, ErrReasonRequired):
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithField("reason", "a reason of at least three characters is required"))
	case errors.Is(err, ErrSelfAction):
		httpx.WriteError(w, r, h.logger, errs.ErrConflict.WithDetail(err))
	case errors.Is(err, ErrAlreadyHeld), errors.Is(err, ErrNotHeld):
		httpx.WriteError(w, r, h.logger, errs.ErrConflict.WithDetail(err))
	case errors.As(err, &transition):
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithField("status", transition.Error()))
	case errors.As(err, &validation):
		field := "body"
		if i := strings.Index(err.Error(), ":"); i > 0 {
			field = strings.ReplaceAll(err.Error()[:i], " ", "_")
		}
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithField(field, validation.msg))
	default:
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
	}
}

// --- reads ---

func (h *AdminHandlers) roles(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	perms, roles, err := h.admin.Roles(r.Context(), actor)
	if err != nil {
		h.writeAdminError(w, r, err)
		return
	}
	views := make([]roleView, 0, len(roles))
	for _, role := range roles {
		views = append(views, roleView{
			Code: string(role.Code), NameEN: role.NameEN, NameBN: role.NameBN, IsClinical: role.IsClinical,
			Station: string(role.Station), Permissions: perms[role.Code],
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"roles": views})
}

func (h *AdminHandlers) list(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var status *Status
	if q := r.URL.Query().Get("status"); q != "" {
		s := Status(q)
		if !isKnownStatus(s) {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithField("status", "unknown status"))
			return
		}
		status = &s
	}
	accounts, err := h.admin.List(r.Context(), actor, status)
	if err != nil {
		h.writeAdminError(w, r, err)
		return
	}
	views := make([]adminUserView, 0, len(accounts))
	for _, a := range accounts {
		views = append(views, viewAccount(a).adminUserView)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"users": views})
}

func (h *AdminHandlers) get(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.userID(w, r)
	if !ok {
		return
	}
	account, err := h.admin.Get(r.Context(), actor, id)
	if err != nil {
		h.writeAdminError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, viewAccount(account))
}

// --- writes ---

type inviteRequest struct {
	EmployeeCode string   `json:"employee_code"`
	NameEN       string   `json:"name_en"`
	NameBN       string   `json:"name_bn"`
	Phone        string   `json:"phone"`
	Email        string   `json:"email"`
	Roles        []string `json:"roles"`
	Password     string   `json:"password"`
}

func (h *AdminHandlers) invite(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body inviteRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	roles := make([]RoleCode, 0, len(body.Roles))
	for _, role := range body.Roles {
		roles = append(roles, RoleCode(strings.ToUpper(strings.TrimSpace(role))))
	}
	account, err := h.admin.Invite(r.Context(), actor, Invitation{
		Code: body.EmployeeCode, NameEN: body.NameEN, NameBN: body.NameBN, Phone: body.Phone, Email: body.Email,
		Roles: roles, Password: body.Password,
	}, clientDigest(r))
	if err != nil {
		h.writeAdminError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, viewAccount(account))
}

type statusRequest struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

func (h *AdminHandlers) status(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.userID(w, r)
	if !ok {
		return
	}
	var body statusRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	account, err := h.admin.ChangeStatus(r.Context(), actor, id, Status(body.Status), body.Reason, clientDigest(r))
	if err != nil {
		h.writeAdminError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, viewAccount(account))
}

type grantRequest struct {
	Role string `json:"role"`
}

func (h *AdminHandlers) grant(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.userID(w, r)
	if !ok {
		return
	}
	var body grantRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	account, err := h.admin.Grant(r.Context(), actor, id, RoleCode(strings.ToUpper(strings.TrimSpace(body.Role))), clientDigest(r))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.writeAdminError(w, r, ErrUnknownRole)
			return
		}
		h.writeAdminError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, viewAccount(account))
}

func (h *AdminHandlers) revoke(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.userID(w, r)
	if !ok {
		return
	}
	var body reasonRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	role := RoleCode(strings.ToUpper(chi.URLParam(r, "role")))
	account, err := h.admin.Revoke(r.Context(), actor, id, role, body.Reason, clientDigest(r))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.writeAdminError(w, r, ErrUnknownRole)
			return
		}
		h.writeAdminError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, viewAccount(account))
}

func (h *AdminHandlers) endSessions(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.userID(w, r)
	if !ok {
		return
	}
	var body reasonRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	n, err := h.admin.EndSessions(r.Context(), actor, id, body.Reason, clientDigest(r))
	if err != nil {
		h.writeAdminError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]int{"sessions_ended": n})
}

type passwordRequest struct {
	Password string `json:"password"`
	Reason   string `json:"reason"`
}

func (h *AdminHandlers) setPassword(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.userID(w, r)
	if !ok {
		return
	}
	var body passwordRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if err := h.admin.SetPassword(r.Context(), actor, id, body.Password, body.Reason, clientDigest(r)); err != nil {
		h.writeAdminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandlers) resetSecondFactor(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.userID(w, r)
	if !ok {
		return
	}
	var body reasonRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if err := h.admin.ResetSecondFactor(r.Context(), actor, id, body.Reason, clientDigest(r)); err != nil {
		h.writeAdminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
