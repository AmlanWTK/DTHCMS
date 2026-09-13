package httpx

import (
	"context"
	"net/http"
	"sync/atomic"
)

// The debt a route guard leaves behind (CP83).
//
// # What this is for
//
// The route guard decides whether a subject may *reach* a route. It cannot decide whether
// they may reach the *resource*, because at that moment nothing has been looked up — a 403
// that depended on a lookup would tell the caller whether the thing exists. So for a
// permission whose reach is narrower than the facility, the guard's allow is conditional:
// the resource still has to be judged, by the handler, on the thing it loaded.
//
// The obvious way to arrange that is a comment asking handlers to remember. That is how
// the previous arrangement failed in the other direction: the guard applied station scope
// to a resource that was not there, and refused everything, for months, because nothing
// made the wrong call impossible to write. A comment would have the same fate.
//
// So the obligation is an object. The guard opens a debt on the context; the authorisation
// engine settles it when it has actually judged a resource; and the response is refused if
// the handler tries to succeed with the debt still open. A handler that forgets does not
// leak — it fails, visibly, with the route's own name in the log.
//
// # Why it lives in platform
//
// The engine that opens the debt (rbac) and the plumbing that enforces it (this package)
// may both import platform and may not import each other, which is the same reason
// Principal lives here.

type scopeDebtKey struct{}

// ScopeDebt is one route's outstanding resource-scope obligation.
//
// Settled at most once and read from the serving goroutine, but a handler may fan out and
// settle from another, so the flag is atomic rather than a plain bool.
type ScopeDebt struct {
	// Scope names the reach the guard declined to apply — "own_station", "own" — for the
	// log line, never for the response.
	Scope   string
	settled atomic.Bool
}

// Settle records that the resource has been judged. Called by the authorisation engine,
// never by a handler directly: the whole point is that settling and judging are one act.
func (d *ScopeDebt) Settle() {
	if d != nil {
		d.settled.Store(true)
	}
}

// Outstanding reports whether the obligation is still open.
func (d *ScopeDebt) Outstanding() bool { return d != nil && !d.settled.Load() }

// WithScopeDebt attaches an unsettled obligation to the request context.
func WithScopeDebt(ctx context.Context, scope string) (context.Context, *ScopeDebt) {
	debt := &ScopeDebt{Scope: scope}
	return context.WithValue(ctx, scopeDebtKey{}, debt), debt
}

// ScopeDebtFrom returns the obligation the route guard left, if there is one.
func ScopeDebtFrom(ctx context.Context) (*ScopeDebt, bool) {
	d, ok := ctx.Value(scopeDebtKey{}).(*ScopeDebt)
	return d, ok
}

// SettleScopeDebt marks this request's obligation as met. A no-op on a context that
// carries none, so the engine's service-layer entry point can call it unconditionally.
func SettleScopeDebt(ctx context.Context) {
	if d, ok := ScopeDebtFrom(ctx); ok {
		d.Settle()
	}
}

// scopeGuard is the ResponseWriter that will not let an unsettled debt out of the door.
//
// It intercepts the first write rather than buffering the body: every response in this
// codebase goes through WriteJSON, which sets a status and then encodes, so the status is
// always known before a byte is written and there is nothing to unwind. A refusal replaces
// the status and swallows what the handler was about to say — which is right in both
// senses, because the handler was about to describe a resource nobody had authorised it
// to describe.
type scopeGuard struct {
	http.ResponseWriter
	req      *http.Request
	debt     *ScopeDebt
	onRefuse func(w http.ResponseWriter, r *http.Request)
	// decided is set once the guard has let a status through or replaced it. Everything
	// after that is the body of whichever answer won.
	decided bool
	refused bool
	wrote   bool
}

func (g *scopeGuard) WriteHeader(status int) {
	if !g.decided {
		g.decided = true
		if status >= 200 && status < 300 && g.debt.Outstanding() {
			g.refused = true
			g.onRefuse(g.ResponseWriter, g.req)
			return
		}
	}
	if g.refused {
		return
	}
	g.wrote = true
	g.ResponseWriter.WriteHeader(status)
}

func (g *scopeGuard) Write(b []byte) (int, error) {
	if !g.decided {
		// A handler that wrote a body without a status meant 200.
		g.WriteHeader(http.StatusOK)
	}
	if g.refused {
		// Reported as written so that an encoder does not treat the refusal as an I/O
		// failure and log a short write that did not happen.
		return len(b), nil
	}
	g.wrote = true
	return g.ResponseWriter.Write(b)
}

// finish covers the handler that returned without writing anything at all, which net/http
// would turn into an empty 200.
func (g *scopeGuard) finish() {
	if g.decided || g.wrote {
		return
	}
	if g.debt.Outstanding() {
		g.decided, g.refused = true, true
		g.onRefuse(g.ResponseWriter, g.req)
	}
}
