package eventstore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// The attribution envelope, made unforgeable (CP24, [R-03]).
//
// Every clinical event records who made it, on which device, wearing which hat, at which
// station, in which facility. That is only worth anything if a client cannot say those
// things about itself — so the fields here are unexported, and no code outside this package
// can write one down. There are exactly two ways to obtain an Actor:
//
//	ActorFrom(ctx)   the authenticated request context: the session behind the bearer
//	                 token, the signature behind the device headers, and the role the
//	                 authorisation engine confirmed the person holds.
//	ActorForTest()   a test's shorthand, which dthclint refuses outside a _test.go file.
//
// A request body that carries a user_id, a device_id or a role is not consulted by either.
// It cannot be: nothing in this file reads a request body, and nothing outside this file
// can set these fields.

// Actor is who made an event. Obtain one with ActorFrom.
type Actor struct {
	userID     uuid.UUID
	deviceID   uuid.UUID
	role       string
	station    string
	facilityID uuid.UUID
	// code is the employee code as it was at the time, for the attribution line a person
	// reads years later. Not hashed into the event: the id is the identity, the code is a
	// convenience the audit trail carries separately.
	code string
}

func (a Actor) UserID() uuid.UUID     { return a.userID }
func (a Actor) DeviceID() uuid.UUID   { return a.deviceID }
func (a Actor) Role() string          { return a.role }
func (a Actor) Station() string       { return a.station }
func (a Actor) FacilityID() uuid.UUID { return a.facilityID }
func (a Actor) Code() string          { return a.code }

// Zero reports an Actor nobody has filled in — what a hand-written Envelope{} carries, and
// what Validate refuses.
func (a Actor) Zero() bool {
	return a.userID == uuid.Nil && a.deviceID == uuid.Nil && a.facilityID == uuid.Nil
}

var (
	// ErrNoPrincipal means the request never established a verified identity. A route that
	// is not permission-guarded produces no principal, and a clinical write from one is a
	// wiring mistake, not a request to be honoured.
	ErrNoPrincipal = errors.New("eventstore: the request carries no verified principal")
	// ErrNoDevice means the session was opened without a device. A clinical event's
	// device_id is evidence [R-03]; a browser session cannot supply it until D-71 is
	// settled, so a write from one is refused here as it is at the route guard.
	ErrNoDevice = errors.New("eventstore: a clinical write needs an enrolled device")
	// ErrNoRole means no active role was confirmed. "Which hat were they wearing" must be
	// answerable years later [R-02], so an event cannot be written without an answer.
	ErrNoRole = errors.New("eventstore: no active role was confirmed for this request")
)

// ActorFrom builds the actor from the verified principal the middleware chain established.
//
// Every field comes from something the server checked itself. Nothing is read from the
// request. A missing piece is a refusal, never a blank: an event attributed to nobody is
// worse than a write that did not happen.
func ActorFrom(ctx context.Context) (Actor, error) {
	principal, ok := httpx.PrincipalFrom(ctx)
	if !ok {
		return Actor{}, ErrNoPrincipal
	}
	userID, err := uuid.Parse(principal.UserID)
	if err != nil {
		return Actor{}, fmt.Errorf("%w: the principal's user id does not parse", ErrNoPrincipal)
	}
	facilityID, err := uuid.Parse(principal.FacilityID)
	if err != nil {
		return Actor{}, fmt.Errorf("%w: the principal's facility id does not parse", ErrNoPrincipal)
	}
	if strings.TrimSpace(principal.DeviceID) == "" {
		return Actor{}, ErrNoDevice
	}
	deviceID, err := uuid.Parse(principal.DeviceID)
	if err != nil {
		return Actor{}, ErrNoDevice
	}
	if strings.TrimSpace(principal.Role) == "" {
		return Actor{}, ErrNoRole
	}
	return Actor{
		userID: userID, deviceID: deviceID, role: principal.Role,
		station: principal.Station, facilityID: facilityID, code: principal.Code,
	}, nil
}

// ActorForTest builds an actor without a request.
//
// dthclint refuses a call to this from anything but a _test.go file, which is the whole
// point: the compiler stops other packages writing an Actor down, and the linter stops
// this door being used to get around that. Production code has one way in, and it is
// ActorFrom.
//
//dthclint:testonly
func ActorForTest(userID, deviceID, facilityID uuid.UUID, role, station string) Actor {
	return Actor{
		userID: userID, deviceID: deviceID, role: role,
		station: station, facilityID: facilityID, code: "TEST",
	}
}
