package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth/devicesig"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
)

// Devices (CP18, D-46).
//
// A device is enrolled by an administrator, holds a private key it made itself, and signs
// every request. The server keeps the public key, the status, and what happened. That is
// what makes the device_id on a clinical event evidence [R-03]: nothing but that tablet's
// Keystore could have produced the signature the server checked before it accepted the
// write.
//
// The lifecycle is small and one-directional at the end:
//
//	pending ──enrol──▶ active ◀──reinstate── suspended
//	                     │  └──suspend────────▶ ┘
//	                     ├──revoke──▶ revoked   (terminal)
//	                     └──lost────▶ lost      (terminal)
//
// Revoked and lost differ in one thing: an event queued on a lost device and arriving later
// is quarantined rather than refused outright, because the person who had the tablet when
// it went missing may have entered real values that morning. The event store (CP23)
// consults the status at ingest; this checkpoint gives it something to consult.

// DeviceStatus is where a device is in its lifecycle.
type DeviceStatus string

const (
	DevicePending   DeviceStatus = "pending"
	DeviceActive    DeviceStatus = "active"
	DeviceSuspended DeviceStatus = "suspended"
	DeviceRevoked   DeviceStatus = "revoked"
	DeviceLost      DeviceStatus = "lost"
)

// Terminal reports whether no transition leads out of the status.
func (s DeviceStatus) Terminal() bool { return s == DeviceRevoked || s == DeviceLost }

// DeviceKind is what the hardware is.
type DeviceKind string

const (
	DeviceTablet  DeviceKind = "tablet"
	DevicePhone   DeviceKind = "phone"
	DeviceDesktop DeviceKind = "desktop"
)

var deviceKinds = map[DeviceKind]bool{DeviceTablet: true, DevicePhone: true, DeviceDesktop: true}

// Device is one enrolled (or about to be enrolled) piece of hardware.
type Device struct {
	ID         uuid.UUID
	FacilityID uuid.UUID
	Name       string
	Kind       DeviceKind
	Status     DeviceStatus

	EnrolledBy *uuid.UUID
	EnrolledAt *time.Time

	Model      string
	OSVersion  string
	AppVersion string
	LastSeenAt *time.Time

	StatusChangedAt time.Time
	StatusChangedBy *uuid.UUID
	StatusReason    string

	CreatedAt time.Time
}

// DeviceKey is a public key a device has, or had.
type DeviceKey struct {
	ID        uuid.UUID
	DeviceID  uuid.UUID
	PublicKey ed25519.PublicKey
	CreatedAt time.Time
	RetiredAt *time.Time
}

// DeviceEnrolment is a one-time code, as the store sees it (digest only).
type DeviceEnrolment struct {
	ID         uuid.UUID
	DeviceID   uuid.UUID
	FacilityID uuid.UUID
	IssuedBy   uuid.UUID
	IssuedAt   time.Time
	ExpiresAt  time.Time
	ConsumedAt *time.Time
}

// Usable reports whether the code may still enrol a device.
func (e DeviceEnrolment) Usable(now time.Time) bool {
	return e.ConsumedAt == nil && now.Before(e.ExpiresAt)
}

// DeviceEvent is one line in a device's history.
type DeviceEvent struct {
	DeviceID   uuid.UUID
	FacilityID uuid.UUID
	ActorID    *uuid.UUID
	Kind       string
	Detail     map[string]any
	At         time.Time
}

// DeviceMetadata is what a device says about itself. Display only; never trusted for a
// decision.
type DeviceMetadata struct {
	Model      string
	OSVersion  string
	AppVersion string
}

// Event kinds. Match the CHECK in migration 00010.
const (
	DeviceEventEnrolmentIssued  = "enrolment_issued"
	DeviceEventEnrolmentFailed  = "enrolment_failed"
	DeviceEventEnrolled         = "enrolled"
	DeviceEventKeyRotated       = "key_rotated"
	DeviceEventSuspended        = "suspended"
	DeviceEventReinstated       = "reinstated"
	DeviceEventRevoked          = "revoked"
	DeviceEventLost             = "lost"
	DeviceEventSignatureRefused = "signature_refused"
	DeviceEventSessionBound     = "session_bound"
)

// EnrolmentCodeLifetime is how long a code works. Fifteen minutes: long enough to walk a
// tablet from the office to the room it lives in, short enough that a code written on a
// sticky note is worthless by lunch.
const EnrolmentCodeLifetime = 15 * time.Minute

// Errors the handlers map to status codes.
var (
	ErrDeviceNotFound      = errors.New("no such device")
	ErrDeviceRefused       = errors.New("the device may not make requests")
	ErrDeviceTerminal      = errors.New("the device has been revoked or reported lost and cannot change")
	ErrDeviceTransition    = errors.New("the device is not in a state that allows this change")
	ErrDeviceNameTaken     = errors.New("a device with that name already exists")
	ErrDeviceKindUnknown   = errors.New("unknown device kind")
	ErrEnrolmentInvalid    = errors.New("the enrolment code is not valid")
	ErrDeviceKeyInUse      = errors.New("that public key is already enrolled")
	ErrDeviceReplay        = errors.New("the request nonce has been seen before")
	ErrDeviceSessionBound  = errors.New("the session belongs to a different device")
	ErrDeviceProofRequired = errors.New("this session was opened from a device and must be used from it")
)

// DeviceStore is what the service needs from the database.
type DeviceStore interface {
	CreateDevice(ctx context.Context, facilityID uuid.UUID, name string, kind DeviceKind, by uuid.UUID) (Device, error)
	DeviceByID(ctx context.Context, id uuid.UUID) (Device, error)
	DevicesForFacility(ctx context.Context, facilityID uuid.UUID) ([]Device, error)
	ActivateDevice(ctx context.Context, id, by uuid.UUID, at time.Time, meta DeviceMetadata) (Device, error)
	ChangeDeviceStatus(ctx context.Context, id uuid.UUID, to DeviceStatus, by *uuid.UUID, reason string, at time.Time) (Device, error)
	TouchDevice(ctx context.Context, id uuid.UUID, at time.Time, appVersion string) error

	InsertDeviceKey(ctx context.Context, deviceID, facilityID uuid.UUID, pub ed25519.PublicKey) (DeviceKey, error)
	LiveDeviceKey(ctx context.Context, deviceID uuid.UUID) (DeviceKey, error)
	RetireDeviceKeys(ctx context.Context, deviceID uuid.UUID, at time.Time, reason string) (int, error)

	CreateDeviceEnrolment(ctx context.Context, e DeviceEnrolment, digest []byte) (DeviceEnrolment, error)
	DeviceEnrolmentByDigest(ctx context.Context, digest []byte) (DeviceEnrolment, error)
	ConsumeDeviceEnrolment(ctx context.Context, id uuid.UUID, at time.Time) (bool, error)
	ExpirePendingEnrolments(ctx context.Context, deviceID uuid.UUID, at time.Time) (int, error)

	RecordDeviceEvent(ctx context.Context, e DeviceEvent) error
	DeviceEventsForDevice(ctx context.Context, deviceID uuid.UUID, limit int) ([]DeviceEvent, error)

	RevokeSessionsForDevice(ctx context.Context, deviceID uuid.UUID, at time.Time, by *uuid.UUID, reason string) (int, error)
}

// NonceStore remembers request nonces for as long as a replay would be inside the clock
// skew. Redis in production (platform/cache), memory in tests.
type NonceStore interface {
	// Remember records the key and reports whether it was new. A false return is a replay.
	Remember(ctx context.Context, key string, ttl time.Duration) (fresh bool, err error)
}

// Devices is the service.
type Devices struct {
	store  DeviceStore
	nonces NonceStore
	clock  clock.Clock
}

// DevicesConfig assembles it.
type DevicesConfig struct {
	Store  DeviceStore
	Nonces NonceStore
	Clock  clock.Clock
}

func NewDevices(cfg DevicesConfig) *Devices {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	return &Devices{store: cfg.Store, nonces: cfg.Nonces, clock: cfg.Clock}
}

// --- enrolment ---

// IssueEnrolment creates a device and the code that will enrol it. The code is returned
// once, here, and nowhere else.
func (d *Devices) IssueEnrolment(ctx context.Context, actor Actor, name string, kind DeviceKind) (Device, string, time.Time, error) {
	if !actor.Permissions.Has(PermDeviceEnroll) {
		return Device{}, "", time.Time{}, ErrNotPermitted
	}
	if !deviceKinds[kind] {
		return Device{}, "", time.Time{}, ErrDeviceKindUnknown
	}
	name = strings.TrimSpace(name)
	if len(name) < 2 {
		return Device{}, "", time.Time{}, errors.New("a device needs a name")
	}
	device, err := d.store.CreateDevice(ctx, actor.FacilityID, name, kind, actor.UserID)
	if err != nil {
		return Device{}, "", time.Time{}, err
	}
	code, expires, err := d.issueCode(ctx, actor, device)
	return device, code, expires, err
}

// ReissueEnrolment issues a fresh code for a device that exists — a reinstalled app, a
// replaced tablet keeping its name. The old key is retired when the new one arrives, not
// now: until then the device keeps working.
func (d *Devices) ReissueEnrolment(ctx context.Context, actor Actor, deviceID uuid.UUID) (Device, string, time.Time, error) {
	if !actor.Permissions.Has(PermDeviceEnroll) {
		return Device{}, "", time.Time{}, ErrNotPermitted
	}
	device, err := d.get(ctx, actor.FacilityID, deviceID)
	if err != nil {
		return Device{}, "", time.Time{}, err
	}
	if device.Status.Terminal() {
		return Device{}, "", time.Time{}, ErrDeviceTerminal
	}
	code, expires, err := d.issueCode(ctx, actor, device)
	return device, code, expires, err
}

func (d *Devices) issueCode(ctx context.Context, actor Actor, device Device) (string, time.Time, error) {
	now := d.clock.Now()
	if _, err := d.store.ExpirePendingEnrolments(ctx, device.ID, now); err != nil {
		return "", time.Time{}, err
	}
	code, err := newEnrolmentCode()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := now.Add(EnrolmentCodeLifetime)
	if _, err := d.store.CreateDeviceEnrolment(ctx, DeviceEnrolment{
		DeviceID: device.ID, FacilityID: device.FacilityID, IssuedBy: actor.UserID,
		IssuedAt: now, ExpiresAt: expires,
	}, DigestOf(normaliseEnrolmentCode(code))); err != nil {
		return "", time.Time{}, err
	}
	d.event(ctx, device, &actor.UserID, DeviceEventEnrolmentIssued, map[string]any{"expires_at": expires})
	return code, expires, nil
}

// Enrol spends a code: the device presents it with a fresh public key and becomes active.
//
// Every refusal is ErrEnrolmentInvalid. A code that does not exist, one that expired, one
// already used and one whose device was revoked in the meantime are all told the same
// thing, because the person typing it learns nothing from the difference and an attacker
// probing codes would.
func (d *Devices) Enrol(ctx context.Context, code string, pub ed25519.PublicKey, meta DeviceMetadata) (Device, DeviceKey, error) {
	now := d.clock.Now()
	if len(pub) != ed25519.PublicKeySize {
		return Device{}, DeviceKey{}, ErrEnrolmentInvalid
	}
	enrolment, err := d.store.DeviceEnrolmentByDigest(ctx, DigestOf(normaliseEnrolmentCode(code)))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Device{}, DeviceKey{}, ErrEnrolmentInvalid
		}
		return Device{}, DeviceKey{}, err
	}
	if !enrolment.Usable(now) {
		return Device{}, DeviceKey{}, ErrEnrolmentInvalid
	}
	device, err := d.store.DeviceByID(ctx, enrolment.DeviceID)
	if err != nil {
		return Device{}, DeviceKey{}, err
	}
	if device.Status.Terminal() {
		d.event(ctx, device, nil, DeviceEventEnrolmentFailed, map[string]any{"reason": "device is " + string(device.Status)})
		return Device{}, DeviceKey{}, ErrEnrolmentInvalid
	}

	// Spend the code first. If anything after this fails the code is gone, and the
	// administrator issues another — which is the safe direction to fail in.
	spent, err := d.store.ConsumeDeviceEnrolment(ctx, enrolment.ID, now)
	if err != nil {
		return Device{}, DeviceKey{}, err
	}
	if !spent {
		return Device{}, DeviceKey{}, ErrEnrolmentInvalid
	}

	if _, err := d.store.RetireDeviceKeys(ctx, device.ID, now, "re-enrolled with a new key"); err != nil {
		return Device{}, DeviceKey{}, err
	}
	key, err := d.store.InsertDeviceKey(ctx, device.ID, device.FacilityID, pub)
	if err != nil {
		return Device{}, DeviceKey{}, err
	}
	device, err = d.store.ActivateDevice(ctx, device.ID, enrolment.IssuedBy, now, cleanMeta(meta))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Device{}, DeviceKey{}, ErrEnrolmentInvalid
		}
		return Device{}, DeviceKey{}, err
	}
	d.event(ctx, device, &enrolment.IssuedBy, DeviceEventEnrolled, map[string]any{
		"key_id": key.ID, "app_version": device.AppVersion, "model": device.Model,
	})
	return device, key, nil
}

// RotateKey replaces a device's key on its own request. The request that carries the new
// key is signed with the old one — that is what proves the caller holds the device — so the
// middleware has already verified the device before this runs.
func (d *Devices) RotateKey(ctx context.Context, deviceID uuid.UUID, pub ed25519.PublicKey) (DeviceKey, error) {
	now := d.clock.Now()
	if len(pub) != ed25519.PublicKeySize {
		return DeviceKey{}, errors.New("public key must be 32 bytes")
	}
	device, err := d.store.DeviceByID(ctx, deviceID)
	if err != nil {
		return DeviceKey{}, err
	}
	if device.Status != DeviceActive {
		return DeviceKey{}, ErrDeviceRefused
	}
	if _, err := d.store.RetireDeviceKeys(ctx, deviceID, now, "rotated by the device"); err != nil {
		return DeviceKey{}, err
	}
	key, err := d.store.InsertDeviceKey(ctx, deviceID, device.FacilityID, pub)
	if err != nil {
		return DeviceKey{}, err
	}
	d.event(ctx, device, nil, DeviceEventKeyRotated, map[string]any{"key_id": key.ID})
	return key, nil
}

// --- verification ---

// Verified is what the middleware learns about a request that carried a valid proof.
type Verified struct {
	DeviceID   uuid.UUID
	FacilityID uuid.UUID
	Name       string
	KeyID      uuid.UUID
}

// Verify checks a request's proof: the device exists and is active, the signature is by its
// live key, the timestamp is fresh, and the nonce has not been seen. On success the device's
// last-seen and app version are updated.
//
// Order matters for what is logged. Status is checked before the signature so that a
// revoked tablet still in somebody's bag is refused quietly (it is expected), while a
// signature that fails under an active device's key is recorded as an event — that is a
// forgery or a corrupted Keystore, and either is worth an administrator's attention.
func (d *Devices) Verify(ctx context.Context, proof devicesig.Proof, appVersion string) (Verified, error) {
	now := d.clock.Now()
	id, err := uuid.Parse(proof.DeviceID)
	if err != nil {
		return Verified{}, devicesig.ErrMalformed
	}
	device, err := d.store.DeviceByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Verified{}, ErrDeviceRefused
		}
		return Verified{}, err
	}
	if device.Status != DeviceActive {
		return Verified{}, ErrDeviceRefused
	}
	key, err := d.store.LiveDeviceKey(ctx, device.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Verified{}, ErrDeviceRefused
		}
		return Verified{}, err
	}
	if err := devicesig.Verify(key.PublicKey, proof, now); err != nil {
		if errors.Is(err, devicesig.ErrSignature) {
			d.event(ctx, device, nil, DeviceEventSignatureRefused, map[string]any{
				"method": proof.Method, "path": proof.Path,
			})
		}
		return Verified{}, err
	}
	if d.nonces != nil {
		fresh, err := d.nonces.Remember(ctx, "device-nonce:"+device.ID.String()+":"+proof.Nonce, 2*devicesig.MaxSkew)
		if err != nil {
			return Verified{}, fmt.Errorf("checking the request nonce: %w", err)
		}
		if !fresh {
			d.event(ctx, device, nil, DeviceEventSignatureRefused, map[string]any{
				"method": proof.Method, "path": proof.Path, "reason": "replay",
			})
			return Verified{}, ErrDeviceReplay
		}
	}
	// Best effort: a failure to record last-seen is not a reason to refuse the request.
	_ = d.store.TouchDevice(ctx, device.ID, now, truncate(strings.TrimSpace(appVersion), 40))
	return Verified{DeviceID: device.ID, FacilityID: device.FacilityID, Name: device.Name, KeyID: key.ID}, nil
}

// --- lifecycle ---

// Suspend refuses a device until it is reinstated.
func (d *Devices) Suspend(ctx context.Context, actor Actor, id uuid.UUID, reason string) (Device, error) {
	return d.transition(ctx, actor, id, DeviceSuspended, DeviceEventSuspended, reason, DeviceActive)
}

// Reinstate brings a suspended device back.
func (d *Devices) Reinstate(ctx context.Context, actor Actor, id uuid.UUID, reason string) (Device, error) {
	return d.transition(ctx, actor, id, DeviceActive, DeviceEventReinstated, reason, DeviceSuspended)
}

// Revoke ends a device for good: key retired, sessions ended, status terminal. Effective on
// the next request, because the next request will find no live key and a status that is
// not active.
func (d *Devices) Revoke(ctx context.Context, actor Actor, id uuid.UUID, reason string) (Device, error) {
	return d.transition(ctx, actor, id, DeviceRevoked, DeviceEventRevoked, reason, DeviceActive, DeviceSuspended, DevicePending)
}

// MarkLost is Revoke with a flag the event store reads at ingest: events queued on this
// device and arriving later are quarantined, not accepted and not discarded.
func (d *Devices) MarkLost(ctx context.Context, actor Actor, id uuid.UUID, reason string) (Device, error) {
	return d.transition(ctx, actor, id, DeviceLost, DeviceEventLost, reason, DeviceActive, DeviceSuspended, DevicePending)
}

func (d *Devices) transition(ctx context.Context, actor Actor, id uuid.UUID, to DeviceStatus, event, reason string, from ...DeviceStatus) (Device, error) {
	if !actor.Permissions.Has(PermDeviceRevoke) {
		return Device{}, ErrNotPermitted
	}
	reason = strings.TrimSpace(reason)
	if len(reason) < 3 {
		return Device{}, ErrReasonRequired
	}
	device, err := d.get(ctx, actor.FacilityID, id)
	if err != nil {
		return Device{}, err
	}
	if device.Status.Terminal() {
		return Device{}, ErrDeviceTerminal
	}
	allowed := false
	for _, f := range from {
		if device.Status == f {
			allowed = true
		}
	}
	if !allowed {
		return Device{}, ErrDeviceTransition
	}
	now := d.clock.Now()
	if to.Terminal() {
		if _, err := d.store.RetireDeviceKeys(ctx, id, now, "device "+string(to)+": "+reason); err != nil {
			return Device{}, err
		}
		if _, err := d.store.RevokeSessionsForDevice(ctx, id, now, &actor.UserID, "device "+string(to)); err != nil {
			return Device{}, err
		}
		if _, err := d.store.ExpirePendingEnrolments(ctx, id, now); err != nil {
			return Device{}, err
		}
	}
	device, err = d.store.ChangeDeviceStatus(ctx, id, to, &actor.UserID, reason, now)
	if err != nil {
		return Device{}, err
	}
	d.event(ctx, device, &actor.UserID, event, map[string]any{"reason": reason})
	return device, nil
}

// --- reading ---

// List returns every device of the facility, in name order.
func (d *Devices) List(ctx context.Context, actor Actor) ([]Device, error) {
	if !actor.Permissions.HasAny(PermDeviceEnroll, PermDeviceRevoke, PermAuditRead) {
		return nil, ErrNotPermitted
	}
	return d.store.DevicesForFacility(ctx, actor.FacilityID)
}

// Get returns one device of the actor's facility.
func (d *Devices) Get(ctx context.Context, actor Actor, id uuid.UUID) (Device, error) {
	if !actor.Permissions.HasAny(PermDeviceEnroll, PermDeviceRevoke, PermAuditRead) {
		return Device{}, ErrNotPermitted
	}
	return d.get(ctx, actor.FacilityID, id)
}

// Events returns a device's recent history, newest first.
func (d *Devices) Events(ctx context.Context, actor Actor, id uuid.UUID, limit int) ([]DeviceEvent, error) {
	if _, err := d.Get(ctx, actor, id); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return d.store.DeviceEventsForDevice(ctx, id, limit)
}

// Describe returns a device by id without an actor — for a device asking about itself,
// whose identity the middleware already proved.
func (d *Devices) Describe(ctx context.Context, id uuid.UUID) (Device, error) {
	device, err := d.store.DeviceByID(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return Device{}, ErrDeviceNotFound
	}
	return device, err
}

func (d *Devices) get(ctx context.Context, facilityID, id uuid.UUID) (Device, error) {
	device, err := d.store.DeviceByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Device{}, ErrDeviceNotFound
		}
		return Device{}, err
	}
	// A device in another facility is, to this actor, a device that does not exist.
	if device.FacilityID != facilityID {
		return Device{}, ErrDeviceNotFound
	}
	return device, nil
}

func (d *Devices) event(ctx context.Context, device Device, actor *uuid.UUID, kind string, detail map[string]any) {
	if detail == nil {
		detail = map[string]any{}
	}
	// Recording is best effort in the sense that a failure to write history must not undo
	// the thing that happened — but it is never silent.
	_ = d.store.RecordDeviceEvent(ctx, DeviceEvent{
		DeviceID: device.ID, FacilityID: device.FacilityID, ActorID: actor,
		Kind: kind, Detail: detail, At: d.clock.Now(),
	})
}

// --- codes ---

// An enrolment code is ten base32 characters — fifty bits — shown as XXXXX-XXXXX. Base32
// rather than digits because a person types it once, on a tablet, from a screen across the
// room: no O/0 and I/1 confusion, and half the length of the decimal equivalent.
const enrolmentCodeLength = 10

var enrolmentAlphabet = base32.StdEncoding.WithPadding(base32.NoPadding)

func newEnrolmentCode() (string, error) {
	raw := make([]byte, 7) // 56 bits; the first ten characters are 50 of them
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	code := enrolmentAlphabet.EncodeToString(raw)[:enrolmentCodeLength]
	return code[:5] + "-" + code[5:], nil
}

// normaliseEnrolmentCode forgives case, spaces and dashes, and the two confusable
// substitutions a person makes when reading a code off a screen.
func normaliseEnrolmentCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		switch r {
		case ' ', '-', '\t':
			continue
		case '0':
			r = 'O'
		case '1':
			r = 'I'
		}
		b.WriteRune(r)
	}
	return b.String()
}

func cleanMeta(m DeviceMetadata) DeviceMetadata {
	return DeviceMetadata{
		Model:      truncate(strings.TrimSpace(m.Model), 80),
		OSVersion:  truncate(strings.TrimSpace(m.OSVersion), 40),
		AppVersion: truncate(strings.TrimSpace(m.AppVersion), 40),
	}
}
