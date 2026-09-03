package rbac

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// Enforcement (CP20): the engine at the three layers.
//
//   - Endpoint: HTTPAuthorizer decides a route's declared permission before its handler
//     runs, and leaves the resolved Subject on the context.
//   - Service: Authorize reads that Subject back and decides a resource — the ownership,
//     station and sensitivity facts the route could not know.
//   - Serialiser: Marshal (serialise.go) removes the fields a subject may not see.
//
// One refusal at every layer: errs.ErrForbidden, with the same message whatever the
// cause, so that a 403 does not say whether the thing exists. The working goes to the
// log, never to the response.

// HTTPAuthorizer adapts the engine to the platform's route guard.
type HTTPAuthorizer struct {
	Resolver *Resolver
}

var _ httpx.Authorizer = (*HTTPAuthorizer)(nil)

// Authorize resolves the caller to a Subject and asks Can for each permission the route
// accepts, on a resource that is only the facility — the route knows nothing more.
func (a *HTTPAuthorizer) Authorize(ctx context.Context, caller httpx.Caller, anyOf []string) (context.Context, httpx.AuthzDecision) {
	userID, err1 := uuid.Parse(caller.UserID)
	facilityID, err2 := uuid.Parse(caller.FacilityID)
	if err1 != nil || err2 != nil {
		return ctx, httpx.AuthzDecision{Reason: string(ReasonNoSubject), Detail: "caller ids do not parse"}
	}
	subject, err := a.Resolver.Subject(ctx, userID, facilityID, auth.RoleCode(caller.ActiveRole), nil)
	if err != nil {
		return ctx, httpx.AuthzDecision{Reason: "resolver_error", Detail: err.Error()}
	}
	resource := Resource{Kind: "route", FacilityID: facilityID}

	var last Decision
	for _, action := range anyOf {
		last = Can(subject, action, resource)
		if last.Allowed {
			// The subject for the service layer, and the principal for the write path
			// (CP24). This is the first moment the active role is known to be one the
			// person actually holds, which is why the envelope's identity is minted here
			// and nowhere earlier — and never from the request body.
			granted := WithSubject(ctx, subject)
			granted = httpx.WithPrincipal(granted, principalOf(caller, subject))
			return granted, httpx.AuthzDecision{Allowed: true, Reason: string(last.Reason)}
		}
	}
	return ctx, httpx.AuthzDecision{Reason: string(last.Reason), Rule: last.Rule, Detail: last.Detail}
}

// principalOf is the verified identity the write envelope is built from: every field
// comes from something the server checked — the session behind the bearer token, the
// device the session was opened from, the role the resolver confirmed — and no field is
// read from the request body. A client that sends its own user_id, device_id or role is
// not consulted; see eventstore.ActorFrom, which reads only this.
func principalOf(caller httpx.Caller, subject Subject) httpx.Principal {
	p := httpx.Principal{
		UserID:     subject.UserID.String(),
		FacilityID: subject.FacilityID.String(),
		SessionID:  caller.SessionID,
		Code:       caller.Code,
		DeviceID:   caller.DeviceID,
		Role:       string(subject.ActiveRole),
	}
	if subject.StationID != nil {
		p.Station = subject.StationID.String()
	}
	return p
}

// SubjectResolver adapts Resolver to the narrower interface a caller outside the HTTP path
// needs (CP26's realtime gateway): a user, a facility, an active role, and nothing about
// stations — a socket is not standing anywhere.
//
// It exists so that realtime does not have to import auth for auth.RoleCode, which the
// architecture allowlist forbids and which would be a dependency on the identity module for
// the sake of a string.
type SubjectResolver struct {
	Resolver *Resolver
}

// Subject resolves the caller.
func (s *SubjectResolver) Subject(ctx context.Context, userID, facilityID uuid.UUID, activeRole string) (Subject, error) {
	return s.Resolver.Subject(ctx, userID, facilityID, auth.RoleCode(activeRole), nil)
}

// --- the subject on the context ---

type subjectKey struct{}

// WithSubject attaches a resolved subject for the service layer.
func WithSubject(ctx context.Context, s Subject) context.Context {
	return context.WithValue(ctx, subjectKey{}, s)
}

// SubjectFrom returns the subject the route guard resolved, if any.
func SubjectFrom(ctx context.Context) (Subject, bool) {
	s, ok := ctx.Value(subjectKey{}).(Subject)
	return s, ok
}

// ErrNoSubject: Authorize was called on a context no route guard prepared. A programming
// error, refused as forbidden so that the failure is safe as well as visible.
var ErrNoSubject = errors.New("rbac: no subject on the context; the route was not declared with a permission")

// Authorize is the service-layer check: the subject the route resolved, against a
// resource with its facts filled in. Returns nil, or errs.ErrForbidden — the same 403
// the route would have given, carrying the decision's working as its detail for the log
// and nothing for the response.
func Authorize(ctx context.Context, action Action, resource Resource) error {
	subject, ok := SubjectFrom(ctx)
	if !ok {
		return errs.ErrForbidden.WithDetail(ErrNoSubject)
	}
	d := Can(subject, action, resource)
	if !d.Allowed {
		return errs.ErrForbidden.WithDetail(fmt.Errorf("%s", d.Explain(action)))
	}
	return nil
}
