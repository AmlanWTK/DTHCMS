package clinic

import (
	"net/http"

	"badscope/internal/platform/httpx"
	"badscope/internal/rbac"
)

type router interface {
	Method(method, pattern string, h any)
	Post(pattern string, h any)
}

type Handlers struct {
	obs *Observations
}

func (h *Handlers) Mount(r router) {
	// Settles through a helper on the receiver: reachable, and allowed.
	r.Method("POST", "/settled", httpx.Declare(httpx.PermissionScoped("observation.write.vitals"), h.settled))
	// Settles through a field's method: the h.photos.ViewURL() shape readpath was blind to.
	r.Method("POST", "/settled-via-field", httpx.Declare(httpx.PermissionScoped("observation.write.vitals"), h.viaField))
	// Nothing judges the resource. Reported.
	r.Method("POST", "/unsettled", httpx.Declare(httpx.PermissionScoped("observation.write.vitals"), h.unsettled))
	// Reported at the mount, because the handler is a closure nothing can walk.
	r.Method("POST", "/closure", httpx.Declare(httpx.PermissionScoped("observation.write.vitals"), func(http.ResponseWriter, *http.Request) {}))
	// Excused, with a reason.
	r.Method("POST", "/excused", httpx.Declare(httpx.PermissionScoped("observation.write.vitals"), h.excused))
	// Not scoped: the guard refuses the narrow roles itself, so there is nothing to owe.
	r.Method("POST", "/plain", httpx.Declare(httpx.Permission("observation.write.vitals"), h.unsettled))
	// The chi shorthand, and a receiver-less handler.
	r.Post("/free", httpx.Declare(httpx.PermissionScoped("observation.write.vitals"), freeFunction))
}

func (h *Handlers) settled(w http.ResponseWriter, r *http.Request) { h.judge(r) }

func (h *Handlers) judge(r *http.Request) {
	_ = rbac.Authorize(r.Context(), "observation.write.vitals", nil)
}

func (h *Handlers) viaField(w http.ResponseWriter, r *http.Request) { h.obs.judge(r) }

func (h *Handlers) unsettled(w http.ResponseWriter, r *http.Request) {}

//dthclint:scopecheck the queue this writes to belongs to no station; there is no resource to judge
func (h *Handlers) excused(w http.ResponseWriter, r *http.Request) {}

func freeFunction(w http.ResponseWriter, r *http.Request) {}

type Observations struct{}

func (o *Observations) judge(r *http.Request) {
	_ = rbac.AuthorizeCreation(r.Context(), "observation.write.vitals")
}

// decoy exists so that the test data carries the collision class readpath was twice wrong
// about: a second declaration named `unsettled`, on another type, which does settle.
type decoy struct{}

func (d decoy) unsettled(w http.ResponseWriter, r *http.Request) {
	_ = rbac.AuthorizeCreation(r.Context(), "observation.write.vitals")
}
