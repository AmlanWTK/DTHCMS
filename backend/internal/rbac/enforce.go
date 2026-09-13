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
	// Reach answers the station-reach question the service layer asks (ADR-0036 §1).
	//
	// Attached to the context beside the subject rather than injected into every handler,
	// for the reason WithReacher gives. Nil is a deployment mistake and fails closed: every
	// station-scoped resource check then refuses with ErrNoReacher, which is loud, safe and
	// impossible to mistake for a permission problem.
	Reach Reacher
}

var _ httpx.Authorizer = (*HTTPAuthorizer)(nil)

// Authorize resolves the caller to a Subject and asks Reaches for each permission the route
// accepts.
//
// Reaches and not Can, and the difference is the whole of CP83. Can answers a question
// about a resource; a route has none, so this used to hand it `Resource{Kind: "route"}` —
// a thing with no station, no owner and no identity. For any permission whose reach is
// narrower than the facility that is an automatic refusal, and it refused every clinical
// write in the clinic for months under the reason "out_of_scope". The route's question is
// narrower than Can's and now has its own function; the resource half is deferred to the
// handler and carried as a debt (httpx.ScopeDebt) so that deferring it cannot mean
// dropping it.
func (a *HTTPAuthorizer) Authorize(ctx context.Context, caller httpx.Caller, anyOf []string) (context.Context, httpx.AuthzDecision) {
	userID, err1 := uuid.Parse(caller.UserID)
	facilityID, err2 := uuid.Parse(caller.FacilityID)
	if err1 != nil || err2 != nil {
		return ctx, httpx.AuthzDecision{Reason: string(ReasonNoSubject), Detail: "caller ids do not parse"}
	}
	subject, err := a.Resolver.Subject(ctx, userID, facilityID, auth.RoleCode(caller.ActiveRole))
	if err != nil {
		return ctx, httpx.AuthzDecision{Reason: "resolver_error", Detail: err.Error()}
	}
	var last Decision
	for _, action := range anyOf {
		last = Reaches(subject, action, facilityID)
		if last.Allowed {
			// The subject for the service layer, and the principal for the write path
			// (CP24). This is the first moment the active role is known to be one the
			// person actually holds, which is why the envelope's identity is minted here
			// and nowhere earlier — and never from the request body.
			granted := WithSubject(ctx, subject)
			granted = httpx.WithPrincipal(granted, principalOf(caller, subject))
			// And the reach store, so the service layer can ask the question the route
			// cannot (ADR-0036 §1). Attached only on the allow, beside the subject, because
			// a caller who was refused at the door has nothing to ask about.
			if a.Reach != nil {
				granted = WithReacher(granted, a.Reach)
			}
			return granted, httpx.AuthzDecision{
				Allowed:  true,
				Reason:   string(last.Reason),
				Deferred: string(last.Deferred),
			}
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
		// Carried, not derived. The strength of a device claim is decided once, at sign-in,
		// by the thing that saw the signature or resolved the printed code; a guess made
		// here from the shape of an id would be a second answer to a question that already
		// has one.
		DeviceAssurance: caller.DeviceAssurance,
		Role:            string(subject.ActiveRole),
	}
	// Where they are standing, from the hat rather than from anything the client said.
	p.Station = subject.StationCode
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
	return s.Resolver.Subject(ctx, userID, facilityID, auth.RoleCode(activeRole))
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
	// The route guard deferred the resource question to here. It has now been answered on
	// a real resource, so the debt is settled and a response may be written. Settling only
	// on the allow is the point: a handler that refuses writes an error anyway, and a
	// handler that never asks writes nothing the guard will let out.
	httpx.SettleScopeDebt(ctx)
	return nil
}

// AuthorizeCreation is the service-layer check for an act that brings the resource into
// being.
//
// It exists because scope is a question about a thing, and a creation has no thing yet. A
// registration clerk's `patient.write.demographics` reaches "their own station"; the
// patient they are about to register is at no station, because there is no patient. Asking
// Can here would either deny every registration in the clinic — which is precisely the bug
// CP83 fixes — or be answered with a resource assembled out of the subject's own facts,
// which is a check that cannot fail and is worse than no check because it reads like one.
//
// So this says the true thing plainly: everything a creation can be judged on is what
// Reaches already judged — the hat, the rule, the permission, the facility — and the scope
// there is nothing to measure. It settles the debt on that basis, and it is written down
// here rather than at each call site so that there is one place to read and one place to
// audit.
//
// It is not a general escape from scope. Anywhere a resource exists — an update, a read, a
// correction, a merge — use Authorize with that resource. dthclint's scopecheck accepts
// either, and a reviewer who sees this one on a route that loads something by id should
// treat it as a defect.
func AuthorizeCreation(ctx context.Context, action Action) error {
	subject, ok := SubjectFrom(ctx)
	if !ok {
		return errs.ErrForbidden.WithDetail(ErrNoSubject)
	}
	d := Reaches(subject, action, subject.FacilityID)
	if !d.Allowed {
		return errs.ErrForbidden.WithDetail(fmt.Errorf("%s", d.Explain(action)))
	}
	httpx.SettleScopeDebt(ctx)
	return nil
}
