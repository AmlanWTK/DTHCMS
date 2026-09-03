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
	// DeviceID is empty for a session opened without a device (a browser today, D-71).
	DeviceID string
	// Role is the hat, as the engine confirmed it — never the raw X-Active-Role header.
	Role string
	// Station is where the person is working, when the role is a station's. Empty otherwise.
	Station string
}

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
