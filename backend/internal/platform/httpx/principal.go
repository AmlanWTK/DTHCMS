package httpx

import "context"

// Principal is who the server decided is making this request — every field verified, none
// of it read from the request body (CP24).
//
// It exists because the write envelope [R-03] must record the person, the device, the hat
// they were wearing and where they were standing, and a client must not be able to name any
// of those. The middleware chain fills it in from things it checked itself: the session
// behind the bearer token, the signature behind the device headers, and the active role the
// authorisation engine confirmed the person actually holds.
//
// It lives in platform because both the engine that fills it (rbac) and the ledger that
// reads it (eventstore) may import platform and may not import each other.
type Principal struct {
	UserID     string
	FacilityID string
	SessionID  string
	// Code is the employee code, for the attribution line a person reads.
	Code string
	// DeviceID is empty for a session opened without a device.
	DeviceID string
	// DeviceAssurance says how DeviceID was established: AssuranceProven, AssuranceNamed,
	// or empty when there is no device at all (CP82, ADR-0021, D-71).
	//
	// It exists because device_id came to mean two different strengths of claim and
	// nothing recorded which. ADR-0021 lists that under the consequences it accepts
	// knowingly — "anything that reasons about it must know which" — and this field is the
	// "must know which". A reader who cannot tell them apart reads the stronger one,
	// because that is the reading that makes the audit trail look better.
	DeviceAssurance string
	// Role is the hat, as the engine confirmed it — never the raw X-Active-Role header.
	Role string
	// Station is where the person is working, when the role is a station's. Empty otherwise.
	Station string
}

// The two strengths a device claim can have.
//
// Strings rather than a Go type because Principal crosses a module boundary and is written
// to a database column whose CHECK spells these exact words (core.session.device_binding,
// migration 00065). One spelling, in one place, that the database also knows.
const (
	// AssuranceProven: the server verified an Ed25519 signature made by a key in the
	// device's secure storage and which cannot be exported (CP18, ADR-0013). Evidence.
	AssuranceProven = "PROVEN"
	// AssuranceNamed: somebody typed the workstation code printed on the monitor. A claim,
	// corroborated by the authenticated person beside it, and never more than that.
	AssuranceNamed = "NAMED"
)

type principalKey struct{}

// WithPrincipal puts the verified principal on the context.
//
// Called by the authorisation engine after it has resolved the subject, because that is the
// first moment the active role is known to be one the person holds. A route that is not
// permission-guarded therefore carries no principal, and a clinical write from such a route
// cannot construct an envelope — which is the fail-closed behaviour, not a gap.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the verified principal, if the chain established one.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
