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
			return WithSubject(ctx, subject), httpx.AuthzDecision{Allowed: true, Reason: string(last.Reason)}
		}
	}
	return ctx, httpx.AuthzDecision{Reason: string(last.Reason), Rule: last.Rule, Detail: last.Detail}
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
