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

	// WorkstationCode is the label printed and stuck to a desk's monitor — FRD-REG-1 —
	// empty for every device that is not a named workstation (CP82, ADR-0021).
	//
	// It is not a credential, and the rest of this package is written so that no reader
	// can mistake it for one: it is never hashed, never compared in constant time, and
	// presenting it grants nothing. A person who types it produces events attributed to
	// that desk, which is a claim corroborated by the person's own authenticated
	// credential, not evidence about the machine.
	WorkstationCode string

	CreatedAt time.Time
}

// DeviceBinding is how a session's device was established.
//
// The distinction exists because device_id came to mean two different strengths of claim
// and nothing recorded which (ADR-0021's own "Bad" list). A downstream reader who cannot
// tell them apart will read the stronger one, because that is the reading that makes the
// audit trail look better.
type DeviceBinding string

const (
	// BindingProven: the server verified an Ed25519 signature made by a key the device
	// generated in secure storage and cannot export (CP18, ADR-0013). Evidence.
	BindingProven DeviceBinding = "PROVEN"
	// BindingNamed: somebody typed the workstation code printed on the monitor. A claim,
	// corroborated by the authenticated person beside it, and never more than that.
	BindingNamed DeviceBinding = "NAMED"
)

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
	ErrDeviceNotFound     = errors.New("no such device")
	ErrDeviceRefused      = errors.New("the device may not make requests")
	ErrDeviceTerminal     = errors.New("the device has been revoked or reported lost and cannot change")
	ErrDeviceTransition   = errors.New("the device is not in a state that allows this change")
	ErrDeviceNameTaken    = errors.New("a device with that name already exists")
	ErrDeviceKindUnknown  = errors.New("unknown device kind")
	ErrEnrolmentInvalid   = errors.New("the enrolment code is not valid")
	ErrDeviceKeyInUse     = errors.New("that public key is already enrolled")
	ErrDeviceReplay       = errors.New("the request nonce has been seen before")
	ErrDeviceSessionBound = errors.New("the session belongs to a different device")
	// ErrWorkstationUnknown — the typed code resolves to no active desk in this facility.
	//
	// It never reaches the person signing in as a refusal. Sign-in succeeds without a
	// device and says the workstation was not recognised; see Sessions.Login.
	ErrWorkstationUnknown = errors.New("no active workstation in this facility carries that code")
	// ErrWorkstationStation — the station name an enrolment was asked to mint a code for
	// is not something a code can be built from.
	ErrWorkstationStation = errors.New("a workstation needs a station name of at least two letters or digits")
	// ErrWorkstationHasNoKey — an enrolment code was asked for on a named workstation.
	// There is no key to exchange and there must not be one; the printed code is the
	// identity, and it is already visible in the device list.
	ErrWorkstationHasNoKey = errors.New("a named workstation has no key to enrol; its code is printed, not issued")
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

	// AssignWorkstationCode mints and takes the next free code for a desktop, and returns
	// it. The allocation happens in the database (core.assign_workstation_code), not here:
	// a code chosen in Go by reading the existing ones and adding one is unique only until
	// two administrators enrol at the same moment.
	AssignWorkstationCode(ctx context.Context, deviceID uuid.UUID, station string) (string, error)
	// DeviceByWorkstationCode resolves a printed code within one facility, whatever its
	// status. The facility is a parameter rather than a filter a caller may forget.
	DeviceByWorkstationCode(ctx context.Context, facilityID uuid.UUID, code string) (Device, error)
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
	// A named workstation has no key to re-exchange, and issuing it a code would be a way
	// to give it one: the enrolment endpoint takes a code and a public key and activates
	// the device that owns them. A desk that could both sign and be named by a printed code
	// would carry two claims of different strengths behind one device_id, which invariant
	// 60 (as amended by migration 00065) refuses outright. Reprinting the label is the
	// operation an administrator actually wants here, and the code is already in the list.
	if device.WorkstationCode != "" {
		return Device{}, "", time.Time{}, ErrWorkstationHasNoKey
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

// --- workstations (CP82, ADR-0021, D-71) ---

// A workstation code is a label, not a credential, and every line below is written to keep
// that true.
//
// It is stored in the clear, because hashing a string that is printed and stuck to a monitor
// would only make the admin console unable to show it. It is compared with an ordinary
// equality in an indexed lookup, not in constant time, because there is nothing secret to
// leak by taking longer on a near miss. Presenting it opens no door: it names a desk, and
// the person beside it is authenticated by a password and, for the roles that need one, a
// second factor.
//
// The one property that does have to be defended is that **resolving a code must not become
// part of authentication**. A failed sign-in must cost the same time, produce the same error
// and reveal the same nothing whether or not a workstation code was sent and whether or not
// it exists. That is why nothing here is reachable from the failure path: Sessions.Login
// resolves the workstation only after the password has already been verified and the account
// already found active, at which point the caller is authenticated and learns nothing they
// could not learn by reading the admin console they may or may not be allowed to open.

// normaliseWorkstationCode is what a person typing at a keyboard is forgiven.
//
// Upper case and no spaces, because the code on the monitor is upper case and somebody
// reading it aloud down a phone line will put spaces around the hyphens. Nothing else is
// repaired: a transposed character is a different desk, and quietly resolving it to the
// nearest match would be the one failure this design cannot have.
func normaliseWorkstationCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == ' ', r == '\t', r == '_':
			// A space or an underscore where a hyphen belongs: the two ways this code is
			// mistyped by somebody copying it off a label.
		default:
			// Anything else is kept, so that it fails the lookup rather than being
			// silently turned into a code that exists.
			b.WriteRune(r)
		}
	}
	return b.String()
}

// EnrolWorkstation registers a desk's computer and mints the code that names it.
//
// It is deliberately *not* IssueEnrolment with a different kind. Enrolling a tablet is a key
// exchange: an administrator issues a one-time code, somebody types it into the app, the app
// sends a public key it generated in the Keystore, and the device becomes active because it
// has proved it holds the private half. A desk has no such half and never will (ADR-0021),
// so there is nothing to exchange, nothing to wait for and nothing to spend. The desk is
// active from the moment the administrator writes it down.
//
// The order of the two writes is not arbitrary. The code is taken first and the device
// activated second, because the reverse leaves — if the allocation fails — an active device
// with neither a key nor a workstation code, which is the exact row invariant 60 raises on.
// Failing forward into a pending device with a code costs an administrator one retry;
// failing forward into an active one costs the next person to run `migrate verify` an hour.
func (d *Devices) EnrolWorkstation(ctx context.Context, actor Actor, name, station string) (Device, error) {
	if !actor.Permissions.Has(PermDeviceEnroll) {
		return Device{}, ErrNotPermitted
	}
	name = strings.TrimSpace(name)
	if len(name) < 2 {
		return Device{}, errors.New("a device needs a name")
	}
	if len(strings.TrimSpace(station)) < 2 {
		return Device{}, ErrWorkstationStation
	}

	now := d.clock.Now()
	device, err := d.store.CreateDevice(ctx, actor.FacilityID, name, DeviceDesktop, actor.UserID)
	if err != nil {
		return Device{}, err
	}
	code, err := d.store.AssignWorkstationCode(ctx, device.ID, station)
	if err != nil {
		return Device{}, fmt.Errorf("minting a workstation code: %w", err)
	}
	device.WorkstationCode = code

	device, err = d.store.ActivateDevice(ctx, device.ID, actor.UserID, now, DeviceMetadata{})
	if err != nil {
		return Device{}, fmt.Errorf("activating the workstation: %w", err)
	}

	// The same event kind a tablet's enrolment writes, because from the console's point of
	// view the same thing happened: this device became usable, on this day, because this
	// administrator said so. The detail says how, so that a reader can tell the two apart.
	d.event(ctx, device, &actor.UserID, DeviceEventEnrolled, map[string]any{
		"workstation_code": code,
		"binding":          string(BindingNamed),
		"note":             "a named workstation: no key, no signature, a printed code",
	})
	return device, nil
}

// ResolveWorkstation turns a typed code into the desk it names, inside one facility.
//
// Every refusal is ErrWorkstationUnknown — no such code, a suspended desk, a revoked one, a
// code belonging to another clinic. Not to protect a secret, because there is none, but
// because the distinctions are of no use to the person at the keyboard and the sign-in path
// must not grow a vocabulary an unauthenticated caller could probe for.
//
// The facility is the caller's own, always. A code resolved across facilities would let a
// label printed in Faridpur name a machine in Dhaka, which is the condition ADR-0021 names
// as the point at which this mechanism has to be revisited rather than stretched.
func (d *Devices) ResolveWorkstation(ctx context.Context, facilityID uuid.UUID, code string) (Device, error) {
	normalised := normaliseWorkstationCode(code)
	if normalised == "" {
		return Device{}, ErrWorkstationUnknown
	}
	device, err := d.store.DeviceByWorkstationCode(ctx, facilityID, normalised)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Device{}, ErrWorkstationUnknown
		}
		return Device{}, err
	}
	// The query is already scoped to the facility. Checked again because this is the one
	// check whose absence would be a cross-facility attribution, and a second equality is
	// cheaper than the review that would otherwise have to notice it never regressed.
	if device.FacilityID != facilityID {
		return Device{}, ErrWorkstationUnknown
	}
	if device.Status != DeviceActive {
		return Device{}, ErrWorkstationUnknown
	}
	return device, nil
}

// SignedDevice reports the device a signature named, and how strongly it may be recorded.
//
// Very nearly always PROVEN, which is the whole point of CP18. The exception is a device of
// a kind that has nowhere to keep a key an operating system will not hand out: a `desktop`
// enrolled with a key by some earlier path is still a machine whose identity a browser on it
// could borrow, and invariant 126 refuses PROVEN for one. Recording NAMED there understates
// what happened, and understating an attribution is the safe direction — the direction this
// column exists to stop code drifting in is the other one.
func (d *Devices) SignedDevice(ctx context.Context, deviceID uuid.UUID) (Device, DeviceBinding, error) {
	device, err := d.store.DeviceByID(ctx, deviceID)
	if err != nil {
		return Device{}, "", err
	}
	if device.Kind == DeviceTablet || device.Kind == DevicePhone {
		return device, BindingProven, nil
	}
	return device, BindingNamed, nil
}

// RecordSessionBinding writes the device history line for a session that named this device.
//
// This is what makes "which sessions claimed to be at FRD-REG-1" answerable, which ADR-0021
// lists as one of the five things the decision rests on. The detail carries the session id,
// the binding and the code — identifiers and a label, no name, no employee code, no patient,
// nothing that is PHI in any reading.
func (d *Devices) RecordSessionBinding(ctx context.Context, device Device, userID, sessionID uuid.UUID, binding DeviceBinding) {
	detail := map[string]any{
		"session_id": sessionID.String(),
		"binding":    string(binding),
	}
	if device.WorkstationCode != "" {
		detail["workstation_code"] = device.WorkstationCode
	}
	d.event(ctx, device, &userID, DeviceEventSessionBound, detail)
}

// --- verification ---

// Verified is what the middleware learns about a request that carried a valid proof.
type Verified struct {
	DeviceID   uuid.UUID
	FacilityID uuid.UUID
	Name       string
	KeyID      uuid.UUID
	// Status is the device's own, carried out rather than swallowed. Verify still refuses
	// anything but an active device, so for every ordinary route this is always DeviceActive;
	// VerifyWhateverTheStatus does not, and the one caller that asks for it has to know what it
	// is looking at.
	Status DeviceStatus
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
	return d.verify(ctx, proof, appVersion, true)
}

// VerifyWhateverTheStatus checks the signature and *reports* the device's status instead of
// refusing on it. Every other check — the device exists, the signature is by its live key, the
// timestamp is fresh, the nonce is unseen — is unchanged and absolute.
//
// # Why this exists, and why it is not the default
//
// CP65's quarantine is built for one scenario: a tablet revoked at nine that has been offline
// since eight and holds forty real blood pressures. They are held rather than accepted or dropped,
// because accepting defeats the revocation and dropping loses a morning of clinical measurements
// silently — the operator believes the work is recorded and nobody finds out until a physician
// wonders why a patient has no vitals.
//
// That scenario was **unreachable**. Verify refuses a non-active device before any handler runs,
// so the revoked tablet's push met a 401 indistinguishable from an expired token — and a client
// following §13.8's "wipe on revocation" would then destroy exactly the data the quarantine exists
// to preserve. An entire designed mechanism that could not fire.
//
// What this does *not* relax is authentication. The batch must still provably come from that
// device's live key; an unknown device, a wrong signature, a stale timestamp and a replayed nonce
// are all refused exactly as before. What moves is the **status** decision, and it moves up to
// exactly one route — the sync push, which does not act on the events but stores them for a person
// to judge. A revoked device still cannot read a patient, open a visit, or record anything: the
// one thing it may do is hand over what it already has.
func (d *Devices) VerifyWhateverTheStatus(ctx context.Context, proof devicesig.Proof,
	appVersion string) (Verified, error) {

	return d.verify(ctx, proof, appVersion, false)
}

func (d *Devices) verify(ctx context.Context, proof devicesig.Proof, appVersion string,
	requireActive bool) (Verified, error) {

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
	if requireActive && device.Status != DeviceActive {
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
	return Verified{
		DeviceID: device.ID, FacilityID: device.FacilityID, Name: device.Name,
		KeyID: key.ID, Status: device.Status,
	}, nil
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
