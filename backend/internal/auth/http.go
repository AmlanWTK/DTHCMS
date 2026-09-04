package auth

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// Handlers serve the authentication endpoints.
type Handlers struct {
	sessions     *Sessions
	store        SessionStore
	secondFactor *SecondFactor
	logger       *slog.Logger
	audit        AuditRecorder

	// facility is the clinic these endpoints belong to.
	//
	// One today. It is a field rather than a lookup because a login must not depend on a
	// query that could fail, and a parameter rather than a request field because letting a
	// caller name their own facility is an authorisation decision dressed as a form field.
	facility uuid.UUID

	// secureCookies is false only for plain-http local development. It is a parameter so
	// that turning it off is a deployment mistake somebody can find, rather than a scheme
	// check buried in a helper.
	secureCookies bool

	refreshLifetime time.Duration
}

// HandlersConfig assembles them.
type HandlersConfig struct {
	Sessions        *Sessions
	Store           SessionStore
	SecondFactor    *SecondFactor
	Logger          *slog.Logger
	FacilityID      uuid.UUID
	SecureCookies   bool
	RefreshLifetime time.Duration
	// Audit receives sign-outs (sign-ins are recorded by the session service). Nil records
	// nothing.
	Audit AuditRecorder
}

func NewHandlers(cfg HandlersConfig) *Handlers {
	if cfg.RefreshLifetime == 0 {
		cfg.RefreshLifetime = DefaultLifetimes().Refresh
	}
	return &Handlers{
		sessions: cfg.Sessions, store: cfg.Store, secondFactor: cfg.SecondFactor, logger: cfg.Logger,
		facility: cfg.FacilityID, secureCookies: cfg.SecureCookies, audit: cfg.Audit,
		refreshLifetime: cfg.RefreshLifetime,
	}
}

// refreshCookie is the name and scope of the refresh credential.
//
// Path is narrow on purpose. A cookie the browser attaches by itself is attached to requests
// the user did not intend, so it is scoped to the only two endpoints that have any use for
// it: the one that rotates it and the one that ends it.
const (
	refreshCookie = "dthcms.refresh"
	refreshPath   = "/v1/auth"
)

// Mount attaches the endpoints.
//
// Login and refresh sit outside the authenticated group, because they cannot require what
// they exist to issue. Everything else is behind it.
func (h *Handlers) Mount(r chi.Router) {
	public, session := httpx.Public(), httpx.Session()
	r.Method("POST", "/login", httpx.Declare(public, h.login))
	r.Method("POST", "/login/second-factor", httpx.Declare(public, h.loginSecondFactor))
	r.Method("POST", "/refresh", httpx.Declare(public, h.refresh))

	r.Group(func(private chi.Router) {
		private.Use(httpx.Authenticate(h.logger, &Identifier{Sessions: h.sessions, Store: h.store}))
		// The device proof was checked before the caller was known (the group's chain runs
		// VerifyDevice ahead of these handlers); now that it is, a session opened from a
		// device must be arriving from it.
		private.Use(httpx.EnforceDeviceBinding(h.logger))
		private.Method("GET", "/me", httpx.Declare(session, h.me))
		private.Method("GET", "/sessions", httpx.Declare(session, h.listSessions))
		private.Method("POST", "/logout", httpx.Declare(session, h.logout))
		private.Method("POST", "/logout-all", httpx.Declare(session, h.logoutAll))

		// The second factor (CP17). Enrolling and confirming need only a session — that is
		// what enrolment is for. Removing the factor or regenerating its recovery codes are
		// privileged: they need a fresh code, presented as a step-up, so that a session left
		// open on a desk cannot quietly take the factor down.
		private.Method("GET", "/second-factor", httpx.Declare(session, h.secondFactorStatus))
		private.Method("POST", "/second-factor/enrol", httpx.Declare(session, h.secondFactorEnrol))
		private.Method("POST", "/second-factor/confirm", httpx.Declare(session, h.secondFactorConfirm))
		private.Method("POST", "/step-up", httpx.Declare(session, h.stepUp))
		private.With(httpx.RequireStepUp(h.logger, h.stepUpVerifier(), PurposeDisableSecondFactor)).
			Method("POST", "/second-factor/disable", httpx.Declare(session, h.secondFactorDisable))
		private.With(httpx.RequireStepUp(h.logger, h.stepUpVerifier(), PurposeRecoveryCodes)).
			Method("POST", "/second-factor/recovery-codes", httpx.Declare(session, h.secondFactorRecoveryCodes))
	})
}

// stepUpVerifier adapts the service to the platform's interface. Nil when the second factor
// is not wired, in which case RequireStepUp refuses everything — the safe default.
func (h *Handlers) stepUpVerifier() httpx.StepUpVerifier {
	if h.secondFactor == nil {
		return nil
	}
	return &StepUpAdapter{SecondFactor: h.secondFactor}
}

// StepUpAdapter turns the platform's string session id into the uuid the service wants.
type StepUpAdapter struct{ SecondFactor *SecondFactor }

func (a *StepUpAdapter) ConsumeStepUp(ctx context.Context, token, sessionID, purpose string) error {
	id, err := uuid.Parse(sessionID)
	if err != nil {
		return ErrStepUpInvalid
	}
	return a.SecondFactor.ConsumeStepUp(ctx, token, id, purpose)
}

// --- login ---

// Transport names how a client wants its credentials delivered.
//
// The browser gets cookies: it must not hold the token (ADR-0010). The station app gets
// the tokens in the body and holds them itself — the refresh token in the Keystore, the
// access token in memory — because a native client's cookie jar is not secure storage,
// and because a jar that quietly re-sends a rotated-away refresh token would trip the
// reuse detector and sign the operator out.
//
// A client that chooses bearer gets no cookies at all, for that second reason.
const (
	TransportCookie = "cookie"
	TransportBearer = "bearer"
)

type loginRequest struct {
	EmployeeCode string `json:"employee_code"`
	Password     string `json:"password"`
	// Transport is "cookie" (the default) or "bearer".
	Transport string `json:"transport,omitempty"`
}

type loginResponse struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
	User        meUser    `json:"user"`

	// Present only for the bearer transport. A browser never sees its refresh token.
	RefreshToken     string     `json:"refresh_token,omitempty"`
	RefreshExpiresAt *time.Time `json:"refresh_expires_at,omitempty"`
}

func transportOf(name string) (string, bool) {
	switch name {
	case "", TransportCookie:
		return TransportCookie, true
	case TransportBearer:
		return TransportBearer, true
	}
	return "", false
}

// deliver writes the credentials the way the client asked for them.
func (h *Handlers) deliver(w http.ResponseWriter, transport string, creds Credentials, user meUser) {
	body := loginResponse{
		AccessToken: creds.AccessToken, ExpiresAt: creds.AccessExpiry, User: user,
	}
	switch transport {
	case TransportBearer:
		body.RefreshToken = creds.RefreshToken
		expiry := creds.RefreshExpiry
		body.RefreshExpiresAt = &expiry
	default:
		h.setSessionCookies(w, creds)
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

func (h *Handlers) login(w http.ResponseWriter, r *http.Request) {
	var body loginRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	transport, ok := transportOf(body.Transport)
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrBadRequest.WithDetail(
			errors.New("transport must be \"cookie\" or \"bearer\"")))
		return
	}

	creds, err := h.sessions.Login(r.Context(), LoginRequest{
		FacilityID:   h.facility,
		Code:         body.EmployeeCode,
		Password:     body.Password,
		UserAgent:    r.UserAgent(),
		ClientDigest: clientDigest(r),
		DeviceID:     deviceIDFrom(r),
	})
	if err != nil {
		// The password was right and a code is owed. Not a session, not a refusal: 202,
		// with the challenge the code must come back with.
		var required *SecondFactorRequired
		if errors.As(err, &required) {
			httpx.WriteJSON(w, http.StatusAccepted, challengeResponse{
				Challenge: required.Challenge.Token, ExpiresAt: required.Challenge.ExpiresAt,
			})
			return
		}
		// One error, whatever happened. The reason is in core.login_attempt.
		if errors.Is(err, ErrAuthentication) {
			httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
			return
		}
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}

	user, err := h.store.UserByID(r.Context(), creds.Session.UserID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}

	h.deliver(w, transport, creds, h.describe(r.Context(), user))
}

// --- the second step of a sign-in ---

type challengeResponse struct {
	Challenge string    `json:"challenge"`
	ExpiresAt time.Time `json:"expires_at"`
}

type secondFactorLoginRequest struct {
	Challenge    string `json:"challenge"`
	Code         string `json:"code,omitempty"`
	RecoveryCode string `json:"recovery_code,omitempty"`
	Transport    string `json:"transport,omitempty"`
}

func (h *Handlers) loginSecondFactor(w http.ResponseWriter, r *http.Request) {
	var body secondFactorLoginRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	transport, ok := transportOf(body.Transport)
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrBadRequest.WithDetail(
			errors.New("transport must be \"cookie\" or \"bearer\"")))
		return
	}
	if body.Challenge == "" || (body.Code == "" && body.RecoveryCode == "") {
		httpx.WriteError(w, r, h.logger, errs.ErrBadRequest.WithDetail(
			errors.New("a challenge and either a code or a recovery code are required")))
		return
	}

	creds, err := h.sessions.CompleteSecondFactor(r.Context(), SecondFactorRequest{
		Challenge:    body.Challenge,
		Proof:        Proof{Code: body.Code, RecoveryCode: body.RecoveryCode},
		UserAgent:    r.UserAgent(),
		ClientDigest: clientDigest(r),
		DeviceID:     deviceIDFrom(r),
	})
	if err != nil {
		if errors.Is(err, ErrAuthentication) {
			httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
			return
		}
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}

	user, err := h.store.UserByID(r.Context(), creds.Session.UserID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	h.deliver(w, transport, creds, h.describe(r.Context(), user))
}

// --- refresh ---

type refreshRequest struct {
	// RefreshToken is how the bearer transport presents its credential. A browser sends
	// nothing here; its credential is the cookie.
	RefreshToken string `json:"refresh_token,omitempty"`
}

// presentedRefresh reads the refresh credential from wherever the client put it.
//
// The body is consulted first. A native client that has both — a token it stored and a
// stale one its HTTP library kept in a cookie jar — means the stored one, and reading the
// cookie first would hand the reuse detector a false theft.
func (h *Handlers) presentedRefresh(w http.ResponseWriter, r *http.Request) (token, transport string, err error) {
	if r.ContentLength > 0 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body refreshRequest
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			return "", "", err
		}
		if body.RefreshToken != "" {
			return body.RefreshToken, TransportBearer, nil
		}
	}
	cookie, err := r.Cookie(refreshCookie)
	if err != nil || cookie.Value == "" {
		return "", "", errs.ErrUnauthenticated
	}
	return cookie.Value, TransportCookie, nil
}

func (h *Handlers) refresh(w http.ResponseWriter, r *http.Request) {
	presented, transport, err := h.presentedRefresh(w, r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}

	creds, err := h.sessions.RefreshFrom(r.Context(), presented, deviceIDFrom(r))
	switch {
	case errors.Is(err, ErrRefreshReused):
		// A security event, logged as one — but the client is told exactly what an expired
		// token is told. An attacker replaying a stolen token learns nothing from the
		// response about whether the replay was noticed.
		h.logger.WarnContext(r.Context(), "refresh token reused; family revoked",
			"path", r.URL.Path)
		if transport == TransportCookie {
			h.clearSessionCookies(w)
		}
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	case errors.Is(err, ErrSessionInvalid):
		if transport == TransportCookie {
			h.clearSessionCookies(w)
		}
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	case err != nil:
		// Not an authentication outcome: the database refused, or the rotation failed. This
		// must not be dressed as "please sign in again" — the sign-in would fail for the
		// same reason, the operator would be locked out with a message that blames them,
		// and the alert would say "auth" when the incident is the database. The cookie is
		// left alone, because the token is still good and the next attempt may succeed.
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}

	user, err := h.store.UserByID(r.Context(), creds.Session.UserID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}

	h.deliver(w, transport, creds, h.describe(r.Context(), user))
}

// --- who am I ---

type meUser struct {
	ID           string   `json:"id"`
	EmployeeCode string   `json:"employee_code"`
	NameEN       string   `json:"name_en"`
	NameBN       string   `json:"name_bn"`
	Status       string   `json:"status"`
	Roles        []string `json:"roles"`
	// Permissions lets an interface hide what the operator cannot do. It is a courtesy, not
	// a control: the control is server-side and arrives at CP20. A screen that hides a
	// button has not prevented anything.
	Permissions []string `json:"permissions"`
	// Grants says which role confers which permissions (CP20), so that an interface can
	// scope itself to the hat the person chooses [R-02] and send it as X-Active-Role.
	Grants []grantView `json:"grants"`
	// SecondFactor tells the interface whether to take this person to enrolment before
	// anything else.
	SecondFactor secondFactorView `json:"second_factor"`
}

type grantView struct {
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
	// Station is where this role works in the patient journey, when it works one. The
	// station app needs it: an operator's queue is their station's queue, and a screen that
	// asks them to choose is a screen where somebody calls a patient to the wrong room
	// (CP39). Empty for the administrative roles, which work no station.
	Station string `json:"station,omitempty"`
}

type secondFactorView struct {
	Required          bool       `json:"required"`
	Enrolled          bool       `json:"enrolled"`
	Pending           bool       `json:"pending"`
	RecoveryCodesLeft int        `json:"recovery_codes_left"`
	ConfirmedAt       *time.Time `json:"confirmed_at,omitempty"`
}

func viewOf(status SecondFactorStatus) secondFactorView {
	return secondFactorView(status)
}

func (h *Handlers) me(w http.ResponseWriter, r *http.Request) {
	caller, ok := httpx.CallerFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}

	id, err := uuid.Parse(caller.UserID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}

	user, err := h.store.UserByID(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.describe(r.Context(), user))
}

// describe assembles the view of a user that the client needs.
//
// Roles and permissions are read here rather than carried in the token, which is the whole
// point of ADR-0011: a grant revoked a minute ago is absent from the next response, without
// anybody having to reissue anything.
func (h *Handlers) describe(ctx context.Context, user User) meUser {
	view := meUser{
		ID: user.ID.String(), EmployeeCode: user.Code,
		NameEN: user.NameEN, NameBN: user.NameBN, Status: string(user.Status),
		Roles: []string{}, Permissions: []string{}, Grants: []grantView{},
	}

	if reader, ok := h.store.(interface {
		RolesForUser(context.Context, uuid.UUID) ([]Role, error)
		PermissionsForRole(context.Context, RoleCode) ([]string, error)
	}); ok {
		if roles, err := reader.RolesForUser(ctx, user.ID); err == nil {
			for _, role := range roles {
				view.Roles = append(view.Roles, string(role.Code))
				perms, err := reader.PermissionsForRole(ctx, role.Code)
				if err != nil {
					perms = []string{}
				}
				view.Grants = append(view.Grants, grantView{
					Role: string(role.Code), Permissions: perms, Station: string(role.Station),
				})
			}
		}
	}

	if reader, ok := h.store.(interface {
		PermissionsForUser(context.Context, uuid.UUID) ([]string, error)
	}); ok {
		if codes, err := reader.PermissionsForUser(ctx, user.ID); err == nil {
			view.Permissions = codes
		}
	}

	if h.secondFactor != nil {
		if status, err := h.secondFactor.Status(ctx, user.ID); err == nil {
			view.SecondFactor = viewOf(status)
		}
	}
	return view
}

// --- the second factor ---

// caller resolves the request's session to the user and session it belongs to.
func (h *Handlers) caller(w http.ResponseWriter, r *http.Request) (User, Session, bool) {
	c, ok := httpx.CallerFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return User{}, Session{}, false
	}
	userID, err1 := uuid.Parse(c.UserID)
	sessionID, err2 := uuid.Parse(c.SessionID)
	if err1 != nil || err2 != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return User{}, Session{}, false
	}
	user, err := h.store.UserByID(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return User{}, Session{}, false
	}
	return user, Session{ID: sessionID, UserID: userID, FacilityID: user.FacilityID}, true
}

func (h *Handlers) requireSecondFactor(w http.ResponseWriter, r *http.Request) bool {
	if h.secondFactor == nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnavailable.WithDetail(
			errors.New("the second factor is not configured on this server")))
		return false
	}
	return true
}

func (h *Handlers) secondFactorStatus(w http.ResponseWriter, r *http.Request) {
	if !h.requireSecondFactor(w, r) {
		return
	}
	user, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	status, err := h.secondFactor.Status(r.Context(), user.ID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, viewOf(status))
}

type enrolmentResponse struct {
	// Secret is the base32 seed, for typing into an app by hand. URI is the same seed as
	// the otpauth:// URI a QR code carries. Both are the secret; the client shows them once
	// and keeps neither.
	Secret string `json:"secret"`
	URI    string `json:"otpauth_uri"`
}

func (h *Handlers) secondFactorEnrol(w http.ResponseWriter, r *http.Request) {
	if !h.requireSecondFactor(w, r) {
		return
	}
	user, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	enrolment, err := h.secondFactor.BeginEnrolment(r.Context(), user, clientDigest(r))
	if err != nil {
		if errors.Is(err, ErrAlreadyEnrolled) {
			httpx.WriteError(w, r, h.logger, errs.ErrConflict.WithDetail(err))
			return
		}
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, enrolmentResponse(enrolment))
}

type codeRequest struct {
	Code string `json:"code"`
}

type recoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

func (h *Handlers) secondFactorConfirm(w http.ResponseWriter, r *http.Request) {
	if !h.requireSecondFactor(w, r) {
		return
	}
	user, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	var body codeRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	codes, err := h.secondFactor.ConfirmEnrolment(r.Context(), user, body.Code, clientDigest(r))
	switch {
	case errors.Is(err, ErrBadCode):
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithField(
			"code", "The code did not match. Check the time on your phone and try the next one."))
		return
	case errors.Is(err, ErrNotEnrolled), errors.Is(err, ErrAlreadyEnrolled):
		httpx.WriteError(w, r, h.logger, errs.ErrConflict.WithDetail(err))
		return
	case err != nil:
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, recoveryCodesResponse{RecoveryCodes: codes})
}

type disableRequest struct {
	Reason string `json:"reason"`
}

func (h *Handlers) secondFactorDisable(w http.ResponseWriter, r *http.Request) {
	if !h.requireSecondFactor(w, r) {
		return
	}
	user, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	var body disableRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		reason = "disabled by the account holder"
	}
	if err := h.secondFactor.Disable(r.Context(), user, &user.ID, reason, clientDigest(r)); err != nil {
		if errors.Is(err, ErrNotEnrolled) {
			httpx.WriteError(w, r, h.logger, errs.ErrConflict.WithDetail(err))
			return
		}
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handlers) secondFactorRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	if !h.requireSecondFactor(w, r) {
		return
	}
	user, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	codes, err := h.secondFactor.RegenerateRecoveryCodes(r.Context(), user, clientDigest(r))
	if err != nil {
		if errors.Is(err, ErrNotEnrolled) {
			httpx.WriteError(w, r, h.logger, errs.ErrConflict.WithDetail(err))
			return
		}
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, recoveryCodesResponse{RecoveryCodes: codes})
}

type stepUpRequest struct {
	Purpose      string `json:"purpose"`
	Code         string `json:"code,omitempty"`
	RecoveryCode string `json:"recovery_code,omitempty"`
}

type stepUpResponse struct {
	Token     string    `json:"step_up_token"`
	Purpose   string    `json:"purpose"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (h *Handlers) stepUp(w http.ResponseWriter, r *http.Request) {
	if !h.requireSecondFactor(w, r) {
		return
	}
	user, session, ok := h.caller(w, r)
	if !ok {
		return
	}
	var body stepUpRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if body.Code == "" && body.RecoveryCode == "" {
		httpx.WriteError(w, r, h.logger, errs.ErrBadRequest.WithDetail(
			errors.New("a code or a recovery code is required")))
		return
	}

	stepUp, err := h.secondFactor.IssueStepUp(r.Context(), user, session, body.Purpose,
		Proof{Code: body.Code, RecoveryCode: body.RecoveryCode}, clientDigest(r))
	switch {
	case errors.Is(err, ErrUnknownPurpose):
		httpx.WriteError(w, r, h.logger, errs.ErrBadRequest.WithDetail(err))
		return
	case errors.Is(err, ErrNotEnrolled), errors.Is(err, ErrEnrolmentPending):
		// The person cannot step up because they have no factor. Told plainly: the fix is
		// enrolment, and the interface takes them there.
		httpx.WriteError(w, r, h.logger, errs.ErrConflict.WithDetail(err))
		return
	case errors.Is(err, ErrBadCode), errors.Is(err, ErrCodeReplayed):
		// As with passwords: one refusal, whichever it was.
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	case err != nil:
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, stepUpResponse{
		Token: stepUp.Token, Purpose: stepUp.Purpose, ExpiresAt: stepUp.ExpiresAt,
	})
}

// --- sessions ---

type sessionView struct {
	ID         string    `json:"id"`
	UserAgent  string    `json:"user_agent"`
	IssuedAt   time.Time `json:"issued_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Current    bool      `json:"current"`
}

func (h *Handlers) listSessions(w http.ResponseWriter, r *http.Request) {
	caller, ok := httpx.CallerFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	id, err := uuid.Parse(caller.UserID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}

	live, err := h.sessions.Sessions(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}

	out := make([]sessionView, 0, len(live))
	for _, session := range live {
		out = append(out, sessionView{
			ID: session.ID.String(), UserAgent: session.UserAgent,
			IssuedAt: session.IssuedAt, LastSeenAt: session.LastSeenAt,
			// Marked so the screen can say "this device" rather than inviting someone to
			// end the session they are reading it on.
			Current: session.ID.String() == caller.SessionID,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// --- logging out ---

func (h *Handlers) logout(w http.ResponseWriter, r *http.Request) {
	caller, ok := httpx.CallerFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	sessionID, err := uuid.Parse(caller.SessionID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}

	if err := h.sessions.Logout(r.Context(), sessionID, nil, "signed out"); err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	h.recordLogout(r, caller, sessionID)

	h.clearSessionCookies(w)
	httpx.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handlers) logoutAll(w http.ResponseWriter, r *http.Request) {
	caller, ok := httpx.CallerFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	userID, err := uuid.Parse(caller.UserID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}

	n, err := h.sessions.LogoutEverywhere(r.Context(), userID, &userID, "signed out everywhere")
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}

	h.clearSessionCookies(w)
	httpx.WriteJSON(w, http.StatusOK, map[string]int{"ended": n})
}

// --- cookies ---

// setSessionCookies writes both credentials for a browser client.
//
// The access token goes in httpx.SessionCookie on every path, because every request needs
// it; the refresh token goes in a cookie scoped to /v1/auth, because only two endpoints
// have any use for it and a credential should not travel further than that. Both are
// httpOnly (ADR-0010): a cross-site scripting hole can act as the user in the page, but it
// cannot take either credential away with it.
//
// The response body carries the access token as well, for the station app, which holds it
// itself and sends it as a bearer header. A browser client ignores the body; a native
// client ignores the cookie.
func (h *Handlers) setSessionCookies(w http.ResponseWriter, creds Credentials) {
	http.SetCookie(w, &http.Cookie{
		Name: httpx.SessionCookie, Value: creds.AccessToken, Path: "/",
		HttpOnly: true, Secure: h.secureCookies, SameSite: http.SameSiteLaxMode,
		Expires: creds.AccessExpiry,
	})
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookie, Value: creds.RefreshToken, Path: refreshPath,
		HttpOnly: true, Secure: h.secureCookies, SameSite: http.SameSiteLaxMode,
		Expires: creds.RefreshExpiry,
	})
}

func (h *Handlers) clearSessionCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: httpx.SessionCookie, Value: "", Path: "/",
		HttpOnly: true, Secure: h.secureCookies, SameSite: http.SameSiteLaxMode,
		MaxAge: -1,
	})
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookie, Value: "", Path: refreshPath,
		HttpOnly: true, Secure: h.secureCookies, SameSite: http.SameSiteLaxMode,
		MaxAge: -1,
	})
}

// clientDigest fingerprints the caller's address for throttling.
//
// Hashed rather than stored: per-address throttling needs to recognise the same caller, not
// to keep a list of who connected from where for as long as the table lives.
//
// The address is taken from the socket, never from X-Forwarded-For. A header a client can
// set is a throttle a client can escape, and the reverse proxy in front of this will be
// configured to be the source of truth at CP03.
//
// The host alone, not host:port. RemoteAddr carries the ephemeral port, which changes on
// every new connection — so digesting it whole would hand out a fresh identity to anyone
// who reconnects between attempts, and the per-client throttle would count nothing.
func clientDigest(r *http.Request) []byte {
	if r.RemoteAddr == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// No port — a unix socket, or a test that set a bare address. Use it as given.
		host = r.RemoteAddr
	}
	if host == "" {
		return nil
	}
	return DigestOfRaw([]byte(host))
}

// recordLogout tells the audit log a person signed out. Best effort: the session is
// already gone, and a sign-out is not refused because the trail could not be written.
func (h *Handlers) recordLogout(r *http.Request, caller httpx.Caller, sessionID uuid.UUID) {
	if h.audit == nil {
		return
	}
	userID, err1 := uuid.Parse(caller.UserID)
	facilityID, err2 := uuid.Parse(caller.FacilityID)
	if err1 != nil || err2 != nil {
		return
	}
	_ = h.audit.RecordAudit(r.Context(), AuditEntry{
		Kind: "session.logout", ActorID: userID, ActorCode: caller.Code, ActorRole: caller.ActiveRole,
		FacilityID: facilityID, ClientDigest: clientDigest(r), At: time.Now().UTC(),
		After: map[string]any{"session_id": sessionID.String()},
	})
}

// --- identifying a request ---

// Identifier adapts Sessions to the platform's Authenticator interface.
//
// It exists because platform may not import a module, so the middleware knows only that
// something can turn a token into a caller.
type Identifier struct {
	Sessions *Sessions
	Store    SessionStore
}

// Identify resolves the token, then reads the caller's permissions.
func (i *Identifier) Identify(ctx context.Context, token string) (httpx.Caller, error) {
	user, session, err := i.Sessions.Authenticate(ctx, token)
	if err != nil {
		return httpx.Caller{}, err
	}

	caller := httpx.Caller{
		UserID:     user.ID.String(),
		FacilityID: user.FacilityID.String(),
		SessionID:  session.ID.String(),
		Code:       user.Code,
	}
	if session.DeviceID != nil {
		caller.DeviceID = session.DeviceID.String()
	}

	if reader, ok := i.Store.(interface {
		PermissionsForUser(context.Context, uuid.UUID) ([]string, error)
	}); ok {
		if codes, err := reader.PermissionsForUser(ctx, user.ID); err == nil {
			caller.Permissions = codes
		}
	}
	return caller, nil
}

var _ httpx.Authenticator = (*Identifier)(nil)
