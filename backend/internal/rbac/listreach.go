package rbac

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// Reach for a list, which is a different question from reach for a record (CP85).
//
// # Why AuthorizeStationRead cannot answer it
//
// ADR-0036 §1 defines reach as a relationship between a station and *one* patient: has this
// station had this person in the current visit. `GET /v1/patients` and
// `GET /v1/patients/today` return many, and there is no "the patient" to ask about. The
// route was therefore left on a plain requirement and refused for the nine station roles —
// safe, and the wrong answer for a clinic, because the anthropometry officer who must call
// the next patient in cannot find them.
//
// The honest shape is not a guard at all. It is a `WHERE` clause: the same relationship
// ADR-0036 §1 already defines, applied to every candidate row instead of to one named one.
// A row the subject could not open individually is a row that does not appear in the list.
//
// # Why the answer is a type and not a string
//
// A function returning `station string` can be called and its result dropped, and the
// mistake would be one line in a handler that already compiles. So the answer is an opaque
// value whose fields no other package can set, and the store's query parameter demands one:
// a handler that skips the restriction has nothing to pass, and the zero value it might
// reach for restricts to a station that does not exist and an owner who is nobody, which
// matches no rows. Forgetting fails closed and forgetting loudly is the next best thing to
// not being able to forget.
//
// # What it must not do
//
// It must not turn a list into an existence oracle. A restricted list answers 200 with
// fewer rows, exactly as an unrestricted list answers 200 with fewer rows when the clinic is
// quiet. Nothing in the response says how many rows were withheld, no status code changes,
// and no count is reported that was computed before the restriction — the `total` on the
// day list is the count *of the rows this subject may see*, for that reason and not for
// tidiness.

// ListReach is the row restriction a multi-row read must apply.
//
// Obtainable only from AuthorizeList. Its zero value reaches nothing.
type ListReach struct {
	// wide: every row in the facility. The facility-wide roles, who must not pay for a
	// predicate that cannot change their answer.
	wide bool
	// station is the station whose patients the subject reaches, for ScopeOwnStation.
	station string
	// owner is the subject's own id, for ScopeOwn — the field worker, whose reach is the
	// records they made (CP19's rule, which ADR-0036 does not cover and does not change).
	owner uuid.UUID
}

// FacilityWide reports whether the list may be served unrestricted.
func (l ListReach) FacilityWide() bool { return l.wide }

// Station is the station the rows must be restricted to, or "" for none.
func (l ListReach) Station() string {
	if l.wide {
		return ""
	}
	return l.station
}

// Owner is the person whose own records the rows must be restricted to, or uuid.Nil.
func (l ListReach) Owner() uuid.UUID {
	if l.wide {
		return uuid.Nil
	}
	return l.owner
}

// AuthorizeList is the service layer's check for a read that returns many rows.
//
// It returns the restriction the caller must apply, and settles the route's scope debt on
// the strength of it. The settlement is an assertion, in the same sense AuthorizeCreation's
// is, and it is worth naming rather than dressing up: nothing here has judged a resource,
// because the resources are the rows and they have not been fetched. What makes it more
// than a formality is the shape rather than the check — the only ListReach a handler can
// obtain is this one, and the store cannot be called without one.
func AuthorizeList(ctx context.Context, action Action) (ListReach, error) {
	subject, ok := SubjectFrom(ctx)
	if !ok {
		return ListReach{}, errs.ErrForbidden.WithDetail(ErrNoSubject)
	}
	// The door first: the hat, the blueprint's deny rules, the permission, the facility.
	// Reaches answers exactly those and reports the reach it did not apply.
	d := Reaches(subject, action, subject.FacilityID)
	if !d.Allowed {
		return ListReach{}, errs.ErrForbidden.WithDetail(fmt.Errorf("%s", d.Explain(action)))
	}

	switch d.Scope {
	case ScopeAny:
		httpx.SettleScopeDebt(ctx)
		return ListReach{wide: true}, nil
	case ScopeOwn:
		httpx.SettleScopeDebt(ctx)
		return ListReach{owner: subject.UserID}, nil
	}

	if subject.StationCode == "" {
		// A station-scoped permission held by somebody standing nowhere. There is no station
		// whose rows they could be shown, and showing none would be a list that is silently
		// always empty — which reads as "the clinic is empty" rather than as a refusal.
		return ListReach{}, errs.ErrForbidden.WithDetail(fmt.Errorf(
			"denied %s: the active role works no station, and this permission reaches only a station [out_of_scope]", action))
	}
	httpx.SettleScopeDebt(ctx)
	return ListReach{station: subject.StationCode}, nil
}

// AuthorizeOwnList is the check for a list that returns only the caller's own rows.
//
// The ownership reach ADR-0036 has no section for. §1 answers "is this patient at your
// station", which is the wrong question for `GET /v1/corrections/mine`: a correction request
// belongs to the operator it names, the patient it is about has walked on hours ago, and the
// field worker the workflow deliberately admits stands at no station at all. The ADR is
// missing a §1(c) and this is it in code — the reach is the subject's own id, it needs no
// station, and it needs no change to the ADR to be correct.
//
// It returns the id the query must filter on. Restricting to the caller's own rows is
// narrower than every reach in the scope table — narrower than ScopeAny, narrower than a
// station, and exactly ScopeOwn — so any subject the door admits is satisfied by it. That is
// the argument for settling the debt here, and it is the whole argument: it does not depend
// on which of the route's permissions let the caller in.
//
// anyOf is the route's own permission union. The subject is admitted on the first one they
// hold, which is what the route guard already did.
func AuthorizeOwnList(ctx context.Context, anyOf ...Action) (uuid.UUID, error) {
	subject, ok := SubjectFrom(ctx)
	if !ok {
		return uuid.Nil, errs.ErrForbidden.WithDetail(ErrNoSubject)
	}
	var last Decision
	for _, action := range anyOf {
		last = Reaches(subject, action, subject.FacilityID)
		if last.Allowed {
			httpx.SettleScopeDebt(ctx)
			return subject.UserID, nil
		}
	}
	if len(anyOf) == 0 {
		return uuid.Nil, errs.ErrForbidden.WithDetail(
			fmt.Errorf("denied: an own-list check named no permission [%s]", ReasonPermissionNotHeld))
	}
	return uuid.Nil, errs.ErrForbidden.WithDetail(fmt.Errorf("%s", last.Explain(anyOf[len(anyOf)-1])))
}

// --- the test doors ---

// FacilityWideListReachForTest is the restriction a facility-wide role receives.
//
// It exists so that the search benchmark and the store's own tests can drive the
// *unrestricted* statement — the one a physician runs — without staging a session, a role
// and a subject to get an answer the scope table already fixes. A test using it has said
// "assume this caller sees the facility", which is true of six roles and of nothing else.
//
// It proves nothing about reach, and it is not how a handler obtains one: dthclint refuses
// a call to it from anything but a _test.go file.
//
//dthclint:testonly
func FacilityWideListReachForTest() ListReach { return ListReach{wide: true} }

// StationListReachForTest is the restriction a station role receives.
//
// The same bargain as above, for the other half of the measurement: what the restricted
// statement costs. The escalation test that matters drives the real route and gets its reach
// from AuthorizeList, as production does.
//
//dthclint:testonly
func StationListReachForTest(station string) ListReach { return ListReach{station: station} }
