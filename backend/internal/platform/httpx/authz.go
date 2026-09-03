package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
)

// Authorisation at the route (CP20).
//
// Every route declares what it needs — nothing, a session, or a permission — by being
// registered through Declare. A route that was registered any other way is found by
// AuditRoutes when the router is built, and the process refuses to start: the default is
// deny, and it is enforced at boot rather than discovered at an audit.
//
// The permission decision itself is the RBAC engine's (internal/rbac). Platform may not
// import a module, so the engine reaches the handler through the Authorizer interface,
// attached to the request by the Authorize middleware; the declared handler asks it and
// refuses with a 403 that says nothing about whether the resource exists — the handler
// has not run, so nothing has been looked up.

// Requirement is what a route needs before its handler runs.
type Requirement struct {
	// public: no session. Health, sign-in, refresh, device enrolment.
	public bool
	// anyOf: at least one of these permissions, decided by the engine. Empty with public
	// false means a session and nothing more.
	anyOf []string
}

// Public declares a route that needs no session.
func Public() Requirement { return Requirement{public: true} }

// Session declares a route that needs a session and no particular permission — the
// account's own things: who am I, sign out, my second factor.
func Session() Requirement { return Requirement{} }

// Permission declares a route that needs at least one of the named permissions.
func Permission(anyOf ...string) Requirement {
	if len(anyOf) == 0 {
		panic("httpx.Permission: a permission requirement must name at least one permission")
	}
	return Requirement{anyOf: anyOf}
}

// IsPublic reports whether the requirement admits a caller without a session.
func (r Requirement) IsPublic() bool { return r.public }

// Permissions returns what the requirement asks for, for the audit report.
func (r Requirement) Permissions() []string { return r.anyOf }

// AuthzDecision is the engine's answer, as the platform sees it.
type AuthzDecision struct {
	Allowed bool
	Reason  string
	Rule    string
	Detail  string
}

// Authorizer decides a permission for a caller. The returned context carries whatever the
// engine wants the handler to have — its resolved subject — so a service can make a
// resource-level decision without resolving the person again.
type Authorizer interface {
	Authorize(ctx context.Context, caller Caller, anyOf []string) (context.Context, AuthzDecision)
}

// ActiveRoleHeader names the hat the caller is wearing for this request [R-02]. Optional;
// absent means every role held. The engine refuses a role the caller does not hold.
const ActiveRoleHeader = "X-Active-Role"

type authzKey struct{}

type authz struct {
	logger     *slog.Logger
	authorizer Authorizer
}

// Authorize attaches the engine to the request. The decision is made by the declared
// handler, which knows its requirement; this middleware knows only who decides.
func Authorize(logger *slog.Logger, authorizer Authorizer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), authzKey{}, authz{logger: logger, authorizer: authorizer})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Declared is a handler with its requirement attached. AuditRoutes recognises the type.
type Declared struct {
	Requirement Requirement
	Handler     http.Handler
}

// Declare wraps a handler with its requirement.
func Declare(req Requirement, h http.HandlerFunc) *Declared {
	return &Declared{Requirement: req, Handler: h}
}

// ServeHTTP enforces the requirement, then runs the handler.
func (d *Declared) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if d.Requirement.public {
		d.Handler.ServeHTTP(w, r)
		return
	}
	caller, ok := CallerFrom(r.Context())
	if !ok {
		// A logger from the chain if there is one; the requirement is refused either way.
		a, _ := r.Context().Value(authzKey{}).(authz)
		writeRefusal(w, r, a.logger, errs.ErrUnauthenticated)
		return
	}
	if len(d.Requirement.anyOf) == 0 {
		d.Handler.ServeHTTP(w, r)
		return
	}

	a, _ := r.Context().Value(authzKey{}).(authz)
	if a.authorizer == nil {
		// No engine wired. Fail closed, loudly: this is a deployment mistake, not a
		// permission problem, and the log must say so.
		if a.logger != nil {
			a.logger.ErrorContext(r.Context(), "no authorizer wired; refusing a permission-guarded route",
				"path", r.URL.Path, "method", r.Method, "permissions", d.Requirement.anyOf)
		}
		writeRefusal(w, r, a.logger, errs.ErrForbidden)
		return
	}
	caller.ActiveRole = strings.TrimSpace(r.Header.Get(ActiveRoleHeader))
	ctx, decision := a.authorizer.Authorize(r.Context(), caller, d.Requirement.anyOf)
	if !decision.Allowed {
		// Recorded with its working, for the security dashboard (CP22) — and never with
		// anything about the resource, because nothing has been looked up.
		if a.logger != nil {
			a.logger.InfoContext(r.Context(), "authorisation denied",
				"path", r.URL.Path, "method", r.Method, "permissions", d.Requirement.anyOf,
				"reason", decision.Reason, "rule", decision.Rule, "active_role", caller.ActiveRole)
		}
		writeRefusal(w, r, a.logger, errs.ErrForbidden)
		return
	}
	d.Handler.ServeHTTP(w, r.WithContext(ctx))
}

func writeRefusal(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	WriteError(w, r, logger, err)
}

// --- the audit ---

// ErrUndeclaredRoutes is returned by AuditRoutes, wrapping the list.
var ErrUndeclaredRoutes = errors.New("routes without a declared requirement")

// AuditRoutes walks the router and refuses any endpoint that was not registered through
// Declare. Called by NewRouter; a failure there is a failure to start.
//
// Every route, not only /v1: a health endpoint is declared Public, and a route somebody
// adds at the root by mistake is exactly what this exists to catch.
func AuditRoutes(r chi.Routes) error {
	var undeclared []string
	err := chi.Walk(r, func(method, route string, handler http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/*")
		if route != "/" {
			route = strings.TrimSuffix(route, "/")
		}
		if route == "" || route == "/" || strings.Contains(route, "*") {
			return nil
		}
		if _, ok := handler.(*Declared); !ok {
			undeclared = append(undeclared, strings.ToUpper(method)+" "+route)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walking the router: %w", err)
	}
	if len(undeclared) > 0 {
		sort.Strings(undeclared)
		return fmt.Errorf("%w: %s (every route must be registered through httpx.Declare with "+
			"Public(), Session() or Permission(...); deny is the default, and it is enforced here)",
			ErrUndeclaredRoutes, strings.Join(undeclared, ", "))
	}
	return nil
}

// Declarations lists every route with its requirement, for the audit report and the
// contract test.
func Declarations(r chi.Routes) (map[string]Requirement, error) {
	out := map[string]Requirement{}
	err := chi.Walk(r, func(method, route string, handler http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/*")
		if route != "/" {
			route = strings.TrimSuffix(route, "/")
		}
		if route == "" || route == "/" || strings.Contains(route, "*") {
			return nil
		}
		if d, ok := handler.(*Declared); ok {
			out[strings.ToUpper(method)+" "+route] = d.Requirement
		}
		return nil
	})
	return out, err
}
