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
	// assurance is how deviceID was established — httpx.AssuranceProven for a signature,
	// httpx.AssuranceNamed for a typed workstation code (CP82, ADR-0021).
	//
	// It is here rather than derived downstream because deviceID now carries two different
	// strengths of claim, and the difference is not visible in the id. A reader who has
	// only the id will read it as the stronger one — that is the reading that makes the
	// record look better, and it is the misreading ADR-0021 accepts responsibility for
	// preventing. Whatever demands a proven device (a signed prescription, CP84) asks this
	// and not the id.
	assurance string
}

func (a Actor) UserID() uuid.UUID     { return a.userID }
func (a Actor) DeviceID() uuid.UUID   { return a.deviceID }
func (a Actor) Role() string          { return a.role }
func (a Actor) Station() string       { return a.station }
func (a Actor) FacilityID() uuid.UUID { return a.facilityID }
func (a Actor) Code() string          { return a.code }

// Assurance is how the device was established: httpx.AssuranceProven, httpx.AssuranceNamed,
// or empty — which ActorFrom never produces, because it refuses an actor with no device at
// all.
//
// Exposed, and not merely stored, because a caller that needs a *proven* device has to be
// able to ask. Before CP82 the question could not be asked at all: every device_id was
// proven, so "has a device" and "proved a device" were the same test, and every site that
// wrote the first meant the second. They are now different tests, and a site that still
// writes the first has silently been weakened.
func (a Actor) Assurance() string { return a.assurance }

// Proven reports whether the device behind this actor was established by a signature.
//
// The named form of the question, for the handful of places that must demand it — a signed
// prescription is the one ADR-0021 names explicitly. Written as a method rather than left to
// each caller comparing strings, so that "what counts as proven" has one definition and
// adding a third strength later is one edit rather than a search.
func (a Actor) Proven() bool { return a.assurance == httpx.AssuranceProven }

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
	// ErrNoDevice means the session was opened without a device, or with one whose
	// strength it did not record.
	//
	// A clinical event's device_id is evidence [R-03]. CP82 settled D-71 by letting a
	// browser session *name* an enrolled workstation (ADR-0021), so a browser can now
	// supply one — but this refusal is unchanged and deliberately so. **NAMED is a device;
	// absent is still absent.** A session that named no workstation, or named one this
	// clinic does not have, still writes nothing clinical: the weaker claim was made
	// admissible, not the missing one.
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
	// A device with no account of how it was established is refused, like a device that
	// does not parse. The database makes the pairing impossible on the session row
	// (session_device_binding_coherent, migration 00065), so a principal that reaches here
	// carrying one and not the other did not come from a session, and an envelope built
	// from it would be attributing an event to a machine on nobody's authority.
	assurance := strings.TrimSpace(principal.DeviceAssurance)
	if assurance != httpx.AssuranceProven && assurance != httpx.AssuranceNamed {
		return Actor{}, ErrNoDevice
	}
	return Actor{
		userID: userID, deviceID: deviceID, role: principal.Role,
		station: principal.Station, facilityID: facilityID, code: principal.Code,
		assurance: assurance,
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
func ActorForTest(userID, deviceID, facilityID uuid.UUID, role, station string, assurance ...string) Actor {
	return Actor{
		userID: userID, deviceID: deviceID, role: role,
		station: station, facilityID: facilityID, code: "TEST",
		assurance: assuranceForTest(deviceID, assurance),
	}
}

// assuranceForTest resolves the optional trailing argument the two test constructors take.
//
// Variadic rather than a sixth positional parameter, and the reason is worth stating because
// the alternative looks tidier. There are forty-odd calls to these two constructors across
// the suite, and every one of them was written before a device could be anything but proven.
// Making them all pass "PROVEN" would be forty edits that change nothing and one diff in
// which the two calls that actually matter — the ones asserting what a NAMED workstation may
// and may not do — are invisible.
//
// So the default is the honest reading of what those tests meant: a device id is a proven
// device, no device id is no device. A test that cares says so, and stands out for it.
func assuranceForTest(deviceID uuid.UUID, given []string) string {
	if len(given) > 0 {
		return given[0]
	}
	if deviceID == uuid.Nil {
		return ""
	}
	return httpx.AssuranceProven
}

// SystemUserID is who an automatic event is attributed to.
//
// A fixed, reserved UUID rather than a seeded account row, and version 4 with an all-zero
// tail so that it is unmistakable in a log, a query and an audit export. There is no such
// person; that is the point. An escalation nobody performed must not be attributed to
// somebody who happened to be logged in, and attributing it to a blank would fail the
// ledger's own validation — so it is attributed to the clinic's own scheduler, by name.
var SystemUserID = uuid.MustParse("00000000-0000-4000-8000-000000000001")

// SystemRole is the role such an event is written under. Not a role in core.role, and it
// never will be: nobody can be granted it, so nobody can act as it.
const SystemRole = "SYSTEM"

// ActorForService builds the actor for work the clinic configured and the server performed on
// a schedule, with no person in the loop.
//
// There is exactly one of those today: the escalation sweep (CP50). An alert nobody
// acknowledged has to advance down the chain whether or not anybody is at a keyboard, and the
// event that records the advance still has to say who wrote it — "an event attributed to
// nobody is worse than a write that did not happen" applies to the machine as much as to a
// person.
//
// The door is narrow on purpose. The moment a constructor like this exists, any handler could
// attribute its own writes to "the system" and step outside the attribution guarantee the
// unexported fields exist to enforce. dthclint holds it to the worker.
//
//dthclint:callableFrom cmd/worker
func ActorForService(facility uuid.UUID, service string) Actor {
	return Actor{
		userID:     SystemUserID,
		deviceID:   SystemUserID,
		role:       SystemRole,
		facilityID: facility,
		code:       service,
	}
}
