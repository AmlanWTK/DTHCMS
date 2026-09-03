package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth/devicesig"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// DeviceHandlers serve the device endpoints (CP18).
//
// Two mounts. MountAuth puts the enrolment endpoint in the unauthenticated corner — a
// tablet enrolling has no session and no key yet; the code is its credential. Mount puts
// everything else under /v1/devices, inside the full chain.
type DeviceHandlers struct {
	devices *Devices
	store   interface {
		UserByID(ctx context.Context, id uuid.UUID) (User, error)
		PermissionsForUser(ctx context.Context, id uuid.UUID) ([]string, error)
	}
	logger *slog.Logger
}

// DeviceHandlersConfig assembles them.
type DeviceHandlersConfig struct {
	Devices *Devices
	Store   *PostgresStore
	Logger  *slog.Logger
}

func NewDeviceHandlers(cfg DeviceHandlersConfig) *DeviceHandlers {
	return &DeviceHandlers{devices: cfg.Devices, store: cfg.Store, logger: cfg.Logger}
}

// MountAuth attaches the enrolment endpoint to the /v1/auth group.
func (h *DeviceHandlers) MountAuth(r chi.Router) {
	r.Method("POST", "/device/enrol", httpx.Declare(httpx.Public(), h.enrol))
}

// Mount attaches the administrative and self-service endpoints under /v1/devices.
//
// Every route declares its requirement (CP20). Reading the list takes any of the three
// permissions that have a reason to look; issuing codes takes device.enroll; changing a
// status takes device.revoke. The device's own endpoints take a session and a verified
// device, which RequireDevice enforces before the handler.
func (h *DeviceHandlers) Mount(r chi.Router) {
	reads := httpx.Permission(PermDeviceEnroll, PermDeviceRevoke, PermAuditRead)
	enrol := httpx.Permission(PermDeviceEnroll)
	revoke := httpx.Permission(PermDeviceRevoke)

	r.Route("/devices", func(d chi.Router) {
		d.Method("GET", "/", httpx.Declare(reads, h.list))
		d.Method("POST", "/", httpx.Declare(enrol, h.issue))
		// The device asking about itself: a session, from a verified device. A literal
		// segment, which chi matches ahead of {id}.
		self := d.With(httpx.RequireDevice(h.logger))
		self.Method("GET", "/self", httpx.Declare(httpx.Session(), h.self))
		self.Method("POST", "/self/rotate-key", httpx.Declare(httpx.Session(), h.rotateKey))
		d.Method("GET", "/{id}", httpx.Declare(reads, h.get))
		d.Method("GET", "/{id}/events", httpx.Declare(reads, h.events))
		d.Method("POST", "/{id}/enrolments", httpx.Declare(enrol, h.reissue))
		d.Method("POST", "/{id}/suspend", httpx.Declare(revoke, h.suspend))
		d.Method("POST", "/{id}/reinstate", httpx.Declare(revoke, h.reinstate))
		d.Method("POST", "/{id}/revoke", httpx.Declare(revoke, h.revoke))
		d.Method("POST", "/{id}/lost", httpx.Declare(revoke, h.lost))
	})
}

// --- verifier adapter ---

// DeviceVerifierAdapter turns the platform's proof into the auth module's.
type DeviceVerifierAdapter struct{ Devices *Devices }

func (a *DeviceVerifierAdapter) VerifyDevice(ctx context.Context, p httpx.DeviceProof) (httpx.DeviceIdentity, error) {
	verified, err := a.Devices.Verify(ctx, devicesig.Proof{
		DeviceID: p.DeviceID, Timestamp: p.Timestamp, Nonce: p.Nonce, Signature: p.Signature,
		Method: p.Method, Path: p.Path, BodyDigest: p.BodyDigest,
	}, p.AppVersion)
	if err != nil {
		if errors.Is(err, devicesig.ErrMalformed) {
			return httpx.DeviceIdentity{}, httpx.ErrDeviceProofMalformed
		}
		return httpx.DeviceIdentity{}, err
	}
	return httpx.DeviceIdentity{
		DeviceID: verified.DeviceID.String(), FacilityID: verified.FacilityID.String(),
		Name: verified.Name, KeyID: verified.KeyID.String(),
	}, nil
}

var _ httpx.DeviceVerifier = (*DeviceVerifierAdapter)(nil)

// deviceIDFrom reads the verified device off the request, for binding a session.
func deviceIDFrom(r *http.Request) *uuid.UUID {
	device, ok := httpx.DeviceFrom(r.Context())
	if !ok {
		return nil
	}
	id, err := uuid.Parse(device.DeviceID)
	if err != nil {
		return nil
	}
	return &id
}

// --- views ---

type deviceView struct {
	ID              uuid.UUID  `json:"id"`
	Name            string     `json:"name"`
	Kind            DeviceKind `json:"kind"`
	Status          string     `json:"status"`
	EnrolledAt      *time.Time `json:"enrolled_at"`
	Model           string     `json:"model"`
	OSVersion       string     `json:"os_version"`
	AppVersion      string     `json:"app_version"`
	LastSeenAt      *time.Time `json:"last_seen_at"`
	StatusChangedAt time.Time  `json:"status_changed_at"`
	StatusReason    string     `json:"status_reason"`
	CreatedAt       time.Time  `json:"created_at"`
}

func viewDevice(d Device) deviceView {
	return deviceView{
		ID: d.ID, Name: d.Name, Kind: d.Kind, Status: string(d.Status),
		EnrolledAt: d.EnrolledAt, Model: d.Model, OSVersion: d.OSVersion, AppVersion: d.AppVersion,
		LastSeenAt: d.LastSeenAt, StatusChangedAt: d.StatusChangedAt, StatusReason: d.StatusReason,
		CreatedAt: d.CreatedAt,
	}
}

type enrolmentIssued struct {
	Device    deviceView `json:"device"`
	Code      string     `json:"code"`
	ExpiresAt time.Time  `json:"expires_at"`
}

type deviceEventView struct {
	Kind    string         `json:"kind"`
	ActorID *uuid.UUID     `json:"actor_id"`
	Detail  map[string]any `json:"detail"`
	At      time.Time      `json:"at"`
}

// --- helpers ---

// actorFrom builds the service's Actor from the request's caller. The permission set
// comes with the caller (the Identifier resolved it), so this is a parse, not a query.
func (h *DeviceHandlers) actorFrom(w http.ResponseWriter, r *http.Request) (Actor, bool) {
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
	return Actor{UserID: userID, FacilityID: facilityID, Permissions: NewPermissionSet(caller.Permissions...)}, true
}

func (h *DeviceHandlers) deviceID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		// Not a uuid is not a device — and "not found" rather than "bad request", so the
		// answer does not differ between a malformed id and a real one in another facility.
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return uuid.Nil, false
	}
	return id, true
}

// writeDeviceError maps the service's errors onto the envelope.
func (h *DeviceHandlers) writeDeviceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotPermitted):
		httpx.WriteError(w, r, h.logger, errs.ErrForbidden)
	case errors.Is(err, ErrDeviceNotFound):
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
	case errors.Is(err, ErrDeviceNameTaken):
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithField("name", "a device with that name already exists"))
	case errors.Is(err, ErrDeviceKindUnknown):
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithField("kind", "must be tablet, phone or desktop"))
	case errors.Is(err, ErrReasonRequired):
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithField("reason", "a reason of at least three characters is required"))
	case errors.Is(err, ErrDeviceTerminal), errors.Is(err, ErrDeviceTransition):
		httpx.WriteError(w, r, h.logger, errs.ErrConflict.WithDetail(err))
	case errors.Is(err, ErrDeviceKeyInUse):
		httpx.WriteError(w, r, h.logger, errs.ErrConflict.WithDetail(err))
	case errors.Is(err, ErrEnrolmentInvalid):
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated.WithDetail(err))
	case errors.Is(err, ErrDeviceRefused):
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated.WithDetail(err))
	default:
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
	}
}

// --- enrolment ---

type enrolRequest struct {
	Code       string `json:"code"`
	PublicKey  string `json:"public_key"`
	Model      string `json:"model"`
	OSVersion  string `json:"os_version"`
	AppVersion string `json:"app_version"`
}

type enrolResponse struct {
	Device deviceView `json:"device"`
	KeyID  uuid.UUID  `json:"key_id"`
}

func (h *DeviceHandlers) enrol(w http.ResponseWriter, r *http.Request) {
	var body enrolRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if strings.TrimSpace(body.Code) == "" {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithField("code", "required"))
		return
	}
	pub, err := devicesig.DecodePublicKey(body.PublicKey)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithField("public_key", err.Error()))
		return
	}
	device, key, err := h.devices.Enrol(r.Context(), body.Code, pub, DeviceMetadata{
		Model: body.Model, OSVersion: body.OSVersion, AppVersion: body.AppVersion,
	})
	if err != nil {
		h.writeDeviceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, enrolResponse{Device: viewDevice(device), KeyID: key.ID})
}

// --- administration ---

type issueRequest struct {
	Name string     `json:"name"`
	Kind DeviceKind `json:"kind"`
}

func (h *DeviceHandlers) issue(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFrom(w, r)
	if !ok {
		return
	}
	var body issueRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if len(strings.TrimSpace(body.Name)) < 2 {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithField("name", "at least two characters"))
		return
	}
	device, code, expires, err := h.devices.IssueEnrolment(r.Context(), actor, body.Name, body.Kind)
	if err != nil {
		h.writeDeviceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, enrolmentIssued{Device: viewDevice(device), Code: code, ExpiresAt: expires})
}

func (h *DeviceHandlers) reissue(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFrom(w, r)
	if !ok {
		return
	}
	id, ok := h.deviceID(w, r)
	if !ok {
		return
	}
	device, code, expires, err := h.devices.ReissueEnrolment(r.Context(), actor, id)
	if err != nil {
		h.writeDeviceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, enrolmentIssued{Device: viewDevice(device), Code: code, ExpiresAt: expires})
}

func (h *DeviceHandlers) list(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFrom(w, r)
	if !ok {
		return
	}
	devices, err := h.devices.List(r.Context(), actor)
	if err != nil {
		h.writeDeviceError(w, r, err)
		return
	}
	views := make([]deviceView, 0, len(devices))
	for _, d := range devices {
		views = append(views, viewDevice(d))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"devices": views})
}

func (h *DeviceHandlers) get(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFrom(w, r)
	if !ok {
		return
	}
	id, ok := h.deviceID(w, r)
	if !ok {
		return
	}
	device, err := h.devices.Get(r.Context(), actor, id)
	if err != nil {
		h.writeDeviceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, viewDevice(device))
}

func (h *DeviceHandlers) events(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFrom(w, r)
	if !ok {
		return
	}
	id, ok := h.deviceID(w, r)
	if !ok {
		return
	}
	events, err := h.devices.Events(r.Context(), actor, id, 50)
	if err != nil {
		h.writeDeviceError(w, r, err)
		return
	}
	views := make([]deviceEventView, 0, len(events))
	for _, e := range events {
		views = append(views, deviceEventView{Kind: e.Kind, ActorID: e.ActorID, Detail: e.Detail, At: e.At})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"events": views})
}

type reasonRequest struct {
	Reason string `json:"reason"`
}

func (h *DeviceHandlers) transition(w http.ResponseWriter, r *http.Request,
	do func(context.Context, Actor, uuid.UUID, string) (Device, error)) {
	actor, ok := h.actorFrom(w, r)
	if !ok {
		return
	}
	id, ok := h.deviceID(w, r)
	if !ok {
		return
	}
	var body reasonRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	device, err := do(r.Context(), actor, id, body.Reason)
	if err != nil {
		h.writeDeviceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, viewDevice(device))
}

func (h *DeviceHandlers) suspend(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, h.devices.Suspend)
}

func (h *DeviceHandlers) reinstate(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, h.devices.Reinstate)
}

func (h *DeviceHandlers) revoke(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, h.devices.Revoke)
}

func (h *DeviceHandlers) lost(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, h.devices.MarkLost)
}

// --- the device itself ---

func (h *DeviceHandlers) self(w http.ResponseWriter, r *http.Request) {
	identity, ok := httpx.DeviceFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrDeviceRequired)
		return
	}
	id, err := uuid.Parse(identity.DeviceID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrDeviceRequired)
		return
	}
	device, err := h.devices.Describe(r.Context(), id)
	if err != nil {
		h.writeDeviceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, viewDevice(device))
}

type rotateKeyRequest struct {
	PublicKey string `json:"public_key"`
}

func (h *DeviceHandlers) rotateKey(w http.ResponseWriter, r *http.Request) {
	identity, ok := httpx.DeviceFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrDeviceRequired)
		return
	}
	id, err := uuid.Parse(identity.DeviceID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrDeviceRequired)
		return
	}
	var body rotateKeyRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	pub, err := devicesig.DecodePublicKey(body.PublicKey)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithField("public_key", err.Error()))
		return
	}
	key, err := h.devices.RotateKey(r.Context(), id, pub)
	if err != nil {
		h.writeDeviceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"key_id": key.ID})
}
