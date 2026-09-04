// Package projection turns events into queryable read models, rebuildably (CP25,
// blueprint §7.8).
//
// §4.1 says the event log is the source of truth. That is only sustainable if everything
// derived from it can be thrown away and rebuilt, and if somebody can prove the rebuilt
// version is the same as the one built incrementally. This package is the machinery for
// both, and `TestAReplayProducesTheSameReadModels` is the proof.
//
// # Two modes
//
// A projection is synchronous or asynchronous, and the choice is clinical rather than
// technical.
//
//	Synchronous   Staleness would be wrong on a screen someone is looking at now: the
//	              junior doctor must see the measurement the nurse entered a second ago
//	              (§4.1). It runs inside the append transaction, so the event and the read
//	              model commit together or not at all.
//
//	Asynchronous  A count, a board, a report. It catches up from `global_seq`, may lag by
//	              a second, and its failure must never stop an append (criterion 4).
//
// Synchronous costs latency on every write and takes the projection's locks inside the
// clinical write path, so the answer is asynchronous unless a clinician would notice.
//
// # Why a synchronous projection is a database function
//
// `core.assert_read_models_derived()` (CP03) refuses to start a service where the
// application role can write to `read`. A synchronous projection runs as the application.
// The two are reconciled by a SECURITY DEFINER function with a pinned search_path: the
// application may call `read.apply_visit_vital(jsonb)` and may do nothing else to the
// table — it cannot delete a row or set a value the events do not imply. The rebuild calls
// the same function, so the incremental and replayed derivations are the same code rather
// than two implementations tested to agree. ADR-0017 records the reasoning.
package projection

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// Mode is when a projection runs.
type Mode string

const (
	// Synchronous runs inside the append transaction.
	Synchronous Mode = "synchronous"
	// Asynchronous catches up from the global sequence.
	Asynchronous Mode = "asynchronous"
)

// Status is what the register says about a projection's health.
type Status string

const (
	// Healthy: keeping up, nothing skipped.
	Healthy Status = "healthy"
	// Degraded: an event was dead-lettered and skipped, so the model is knowingly
	// incomplete. It keeps running — the alternative is that one bad event stops every
	// later one from being projected.
	Degraded Status = "degraded"
	// Rebuilding: a rebuild is in flight and these rows are not to be trusted.
	Rebuilding Status = "rebuilding"
)

// Projection derives a read model from events.
//
// Every implementation must be **idempotent**: applying the same event twice leaves the
// model exactly as applying it once did. That is not a nicety — a runner that crashes
// between applying a batch and advancing its checkpoint will re-apply the batch, a rebuild
// re-applies everything by definition, and the replay-equivalence test is precisely the
// assertion that these produce the same bytes.
//
// It must also tolerate **out-of-order** application, because a rebuild that fell behind
// and caught up can present an older event after a newer one. Both are usually the same
// mechanism: key the write on the aggregate and guard it with `global_seq`.
type Projection interface {
	// Name is the key in read.projection_state. Stable for the life of the projection:
	// renaming one loses its checkpoint and forces a rebuild.
	Name() string

	// Version is the version of the *derivation*. Change it whenever the computation
	// changes, and the runner rebuilds before it trusts the model again (§7.10). Not
	// changing it after changing the logic leaves half the rows computed the old way,
	// which is the failure this number exists to prevent.
	Version() int

	Mode() Mode

	// Handles is a cheap filter on the event type, applied before the payload is decoded.
	Handles(eventType string) bool

	// Apply writes the derived state for one event, in the caller's transaction. It is
	// called only for event types Handles accepted.
	Apply(ctx context.Context, tx pgx.Tx, e eventstore.Event) error

	// Reset empties everything this projection owns, for a rebuild. Called in the
	// rebuild's transaction, as the projector.
	Reset(ctx context.Context, tx pgx.Tx) error
}

// Registry is the set of projections a process knows about.
//
// Registration is explicit and panics on a duplicate name, for the reason the event
// registry does: two projections quietly sharing a checkpoint row would each see half the
// events and neither would be wrong enough to notice.
type Registry struct {
	byName map[string]Projection
	order  []string
}

func NewRegistry(projections ...Projection) *Registry {
	r := &Registry{byName: map[string]Projection{}}
	for _, p := range projections {
		r.Register(p)
	}
	return r
}

func (r *Registry) Register(p Projection) {
	name := p.Name()
	if name == "" || strings.ContainsAny(name, " '\"") {
		panic(fmt.Sprintf("projection: %q is not a usable name", name))
	}
	if p.Version() < 1 {
		panic(fmt.Sprintf("projection %s: version must be at least 1", name))
	}
	if _, exists := r.byName[name]; exists {
		panic(fmt.Sprintf("projection %s is registered twice", name))
	}
	if p.Mode() != Synchronous && p.Mode() != Asynchronous {
		panic(fmt.Sprintf("projection %s: unknown mode %q", name, p.Mode()))
	}
	r.byName[name] = p
	r.order = append(r.order, name)
	sort.Strings(r.order)
}

// Lookup returns a projection by name.
func (r *Registry) Lookup(name string) (Projection, bool) {
	p, ok := r.byName[name]
	return p, ok
}

// Names lists every registered projection, sorted — so a rebuild's order is the same on
// every machine and a log is comparable between runs.
func (r *Registry) Names() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// InMode lists the projections of one mode, sorted.
func (r *Registry) InMode(mode Mode) []Projection {
	var out []Projection
	for _, name := range r.order {
		if p := r.byName[name]; p.Mode() == mode {
			out = append(out, p)
		}
	}
	return out
}

// Default is the catalogue this deployment runs. Each clinical checkpoint adds its own
// beside its feature; the two here are the framework's references and are real read models
// in their own right.
var Default = NewRegistry(VisitVital{}, StationActivity{}, Patient{}, PatientTimeline{})
