package eventstore

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// Who is looking, as opposed to who is writing (CP74).
//
// # The bug this exists to end
//
// Actor is the *write* envelope. It refuses a request that carries no enrolled device,
// because a clinical event's device_id is evidence [R-03] and an event attributed to no
// device is worse than a write that did not happen. That is right, and it stays.
//
// Read handlers had no other door. So they called ActorFrom too — for the facility to scope
// the query to, and for the user and role to put on the audit entry — and inherited a
// refusal written for writes. A browser session has no device by design (D-71 is still
// open), so **28 of the 118 GET routes were unreachable from any browser**: the patient
// list, the traffic board, every station queue, the timeline, growth, observations, alerts,
// consents, corrections, the patient summary. Nearly a quarter of the read surface, failing
// with "this action must be done from an enrolled clinic device" on an action that was not a
// clinical write and needed no device to be safe.
//
// It went unnoticed because every domain test builds its identity with ActorForTest, which
// takes a device id as an argument and therefore always has one. Nothing exercised a read
// path with the identity a browser actually presents. `TestNoReadRouteDemandsAWriteEnvelope`
// in cmd/api is that missing test, and it drives a real login with no device through every
// declared GET route.
//
// # Why a separate type rather than a flag
//
// A `ActorFrom(ctx, allowNoDevice)` would put the decision at 90-odd call sites, where it
// would be got wrong once and never noticed. A Reader instead cannot be written with: the
// event store's Append takes an Actor, Actor's fields are unexported, and there is no
// conversion. A handler that holds a Reader can read and audit, and the compiler stops it
// recording an event — which is the property that actually wants enforcing.
//
// A Reader is still a *verified* identity. Every field comes from the same principal
// ActorFrom reads, established by the same middleware chain, and nothing is taken from the
// request body. The single difference is that a missing device is not fatal, because
// nothing here is evidence of anything.

// Reader is who is looking. Obtain one with ReaderFrom.
type Reader struct {
	userID     uuid.UUID
	role       string
	station    string
	facilityID uuid.UUID
	code       string
	// deviceID is the device the session was opened from, or uuid.Nil for a browser. Kept
	// so the audit trail can say "from this tablet" when it is true, and say nothing when
	// it is not. Never invented: an absent device is recorded as absent.
	deviceID uuid.UUID
}

func (r Reader) UserID() uuid.UUID     { return r.userID }
func (r Reader) Role() string          { return r.role }
func (r Reader) Station() string       { return r.station }
func (r Reader) FacilityID() uuid.UUID { return r.facilityID }
func (r Reader) Code() string          { return r.code }

// DeviceID is the device this session was opened from, or uuid.Nil for a browser. Callers
// that record it must be able to record its absence too.
func (r Reader) DeviceID() uuid.UUID { return r.deviceID }

// ReaderFrom builds the reading identity from the verified principal the middleware chain
// established.
//
// It refuses the same two things ActorFrom refuses — no principal, no confirmed role —
// because a query scoped to nobody's facility, or audited to nobody's role, is not a read
// worth serving. It does not refuse a missing device.
func ReaderFrom(ctx context.Context) (Reader, error) {
	principal, ok := httpx.PrincipalFrom(ctx)
	if !ok {
		return Reader{}, ErrNoPrincipal
	}
	userID, err := uuid.Parse(principal.UserID)
	if err != nil {
		return Reader{}, fmt.Errorf("%w: the principal's user id does not parse", ErrNoPrincipal)
	}
	facilityID, err := uuid.Parse(principal.FacilityID)
	if err != nil {
		return Reader{}, fmt.Errorf("%w: the principal's facility id does not parse", ErrNoPrincipal)
	}
	if principal.Role == "" {
		return Reader{}, ErrNoRole
	}
	// A device id that is present must parse. A device id that is absent is absent: the
	// zero value, which every caller that records it treats as "no device", never as a
	// device whose id happens to be all zeroes.
	var deviceID uuid.UUID
	if principal.DeviceID != "" {
		parsed, err := uuid.Parse(principal.DeviceID)
		if err != nil {
			return Reader{}, ErrNoDevice
		}
		deviceID = parsed
	}
	return Reader{
		userID: userID, role: principal.Role, station: principal.Station,
		facilityID: facilityID, code: principal.Code, deviceID: deviceID,
	}, nil
}

// ReaderForTest builds a reading identity without a request.
//
// Like ActorForTest, dthclint refuses a call to this from anything but a _test.go file.
// Pass uuid.Nil as deviceID to get the identity a browser presents — which is the case
// worth testing, and the one nothing tested.
//
//dthclint:testonly
func ReaderForTest(userID, deviceID, facilityID uuid.UUID, role, station string) Reader {
	return Reader{
		userID: userID, deviceID: deviceID, facilityID: facilityID,
		role: role, station: station, code: "TEST",
	}
}
