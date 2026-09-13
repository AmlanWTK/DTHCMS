package rbac

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// Reach: what "the patients this station may touch" means (ADR-0036 §1).
//
// # The premise that had to go first
//
// The engine used to ask `resource.StationID == subject.StationID` — that a patient record
// carries a station and that it matches. It does not, and no column says otherwise: there
// is no station on `core.patient` and there should not be one. A patient does not belong to
// a station. They walk through twelve of them in ninety minutes, and the last desk they sat
// at owns them no more than the next one does.
//
// What the schema does record, twice and already, is the *passage*: `core.queue_entry` and
// `core.encounter`, both carrying station_code, patient_id and visit_id, both maintained by
// CP39 for reasons that have nothing to do with authorisation. So reach is a relationship
// read off those two tables, not a property read off the patient.
//
// # Two strengths, because reading and writing are not the same act
//
// A station may *record against* a patient it currently has — called, or in service, or with
// an open encounter. A patient finished with an hour ago is not theirs to amend; that is what
// the correction workflow is for, and it is deliberately a different, flagged path.
//
// A station may *read* any patient it has had in this visit, `done` and `skipped` included.
// The counsellor must be able to re-open what they just recorded and the nutritionist must
// be able to check a measurement taken upstream, and refusing that makes the software slower
// than the paper it replaces.
//
// The two are separate types below, and neither converts to the other. That is deliberate
// and it is the only reason they are types at all: a `bool` would let a handler compute the
// read answer, look at it, and act on it for a write — one line, entirely plausible in
// review, and it would silently give every station a write reach over every patient it saw
// today. `AuthorizeStationRead` will not accept a WriteReach and `AuthorizeStationWrite`
// will not accept a ReadReach, so that mistake does not compile.
//
// # "Current visit", said out loud
//
// The current visit is the patient's one `open` visit — `core.visit` has a partial unique
// index that makes "one open visit per patient" a property rather than a convention, so
// "the" is exact rather than hopeful.
//
// A patient with no open visit therefore has no current visit, and **no station reaches
// them**. Not "the newest visit", which would leave last month's nutritionist holding a
// reach for ever; not "any visit", which is the same thing said more slowly. This is the
// case ADR-0036 warns will feel wrong the first time somebody hits it — most often on a
// patient registered but not yet routed — and it is stated here rather than left to emerge
// from an empty join, so that the answer is a decision somebody made and not a side effect.
// The facility-wide roles (physician, QA, admin, and the registration and records desks for
// their own work, ADR-0036 §2) reach such a patient, and break-glass reaches them for
// everybody else.

// WriteReach is the answer to "does this station currently have this patient".
//
// Obtainable only from Reacher.WriteReachAt, and accepted only by AuthorizeStationWrite.
type WriteReach struct {
	station string
	held    bool
	// visitOpen distinguishes "this patient is not in the building" from "this patient is
	// in the building and is somebody else's". Both refuse; only the log is told which.
	visitOpen bool
}

// ReadReach is the answer to "has this station had this patient at any point in this visit".
//
// Obtainable only from Reacher.ReadReachAt, and accepted only by AuthorizeStationRead.
type ReadReach struct {
	station   string
	held      bool
	visitOpen bool
}

// Reacher answers the two questions against the clinic's records.
//
// An interface rather than the concrete store so that the engine's own tests can drive the
// decision logic without a database, and so that a caller which has already resolved the
// reach for other reasons is not made to resolve it twice. The production implementation is
// PostgresReach, below, and it is the only one that ships.
type Reacher interface {
	// WriteReachAt reports whether station holds patient right now, in the current visit.
	WriteReachAt(ctx context.Context, facilityID uuid.UUID, station string, patientID uuid.UUID) (WriteReach, error)
	// ReadReachAt reports whether station has had patient at any point in the current visit.
	ReadReachAt(ctx context.Context, facilityID uuid.UUID, station string, patientID uuid.UUID) (ReadReach, error)
}

type reacherKey struct{}

// WithReacher attaches the reach store to a request.
//
// It rides the context for the same reason the subject does: the alternative is a field on
// every one of the twenty-odd handler structs in the clinical modules, threaded through
// every constructor, which is a change large enough that some of it would be got wrong and
// none of it would be interesting. The route guard puts it there once, beside the subject
// it has just resolved, and a handler that finds none fails closed.
func WithReacher(ctx context.Context, r Reacher) context.Context {
	return context.WithValue(ctx, reacherKey{}, r)
}

// ReacherFrom returns the reach store the route guard attached, if any.
func ReacherFrom(ctx context.Context) (Reacher, bool) {
	r, ok := ctx.Value(reacherKey{}).(Reacher)
	return r, ok && r != nil
}

// ErrNoReacher: a station-scoped decision was asked for on a context carrying no reach
// store. A deployment mistake — the composition root did not wire one — refused as
// forbidden so that it is safe as well as visible.
var ErrNoReacher = errors.New("rbac: no reach store on the context; a station-scoped resource cannot be judged")

// AuthorizeStationWrite is the service layer's check for an act that records against a
// patient.
//
// The whole decision, in order: the subject the route resolved, the permission, and then —
// only if the permission's reach is narrower than the facility, so only when it can change
// the answer — the reach query. A physician pays nothing for this call; a nutritionist pays
// one indexed query.
func AuthorizeStationWrite(ctx context.Context, action Action, kind string, patientID uuid.UUID) error {
	return authorizeAtStation(ctx, action, kind, patientID, false)
}

// AuthorizeStationRead is the service layer's check for reading a patient's record.
//
// Same shape, the wider of the two reaches. Never use it to guard a write: the reach it
// applies includes patients this station finished with, and recording against one of those
// is what the correction workflow exists to make visible.
func AuthorizeStationRead(ctx context.Context, action Action, kind string, patientID uuid.UUID) error {
	return authorizeAtStation(ctx, action, kind, patientID, true)
}

// authorizeAtStation is the shared body. The bool is not a reach and never escapes: it
// chooses which of the two queries to run, and the two answers it can produce are separate
// types that meet nowhere.
func authorizeAtStation(ctx context.Context, action Action, kind string, patientID uuid.UUID, reading bool) error {
	subject, ok := SubjectFrom(ctx)
	if !ok {
		return errs.ErrForbidden.WithDetail(ErrNoSubject)
	}
	if patientID == uuid.Nil {
		// A caller that has not resolved a patient cannot be asking a question about one.
		// Refused rather than queried, because a nil patient id matches nothing and an
		// EXISTS that is false for the wrong reason reads exactly like one that is false
		// for the right one.
		return errs.ErrForbidden.WithDetail(fmt.Errorf(
			"denied %s: the handler asked for a station reach without a patient [no_resource]", action))
	}

	resource := Resource{Kind: kind, FacilityID: subject.FacilityID, ID: patientID}

	// The reach the permission grants this subject decides whether a query is needed at
	// all. For ScopeAny it cannot change the answer, and running it would be a cost paid
	// on every request by the roles that never fail it.
	scope, held := widestScope(effectiveRoles(subject), action)
	switch {
	case !held || scope == ScopeAny:
		// Nothing a reach query could change. The facility comparison and the blueprint's
		// deny rules are the whole decision, and Authorize makes them.
		return Authorize(ctx, action, resource)
	case scope == ScopeOwn:
		// A field worker, whose reach is "the records I made" (ADR-0036 has nothing to say
		// about this one; it is CP19's rule and it stands).
		//
		// A *write* satisfies it by construction: the record being created is owned by the
		// person creating it, and there is nothing yet to compare an owner against. That is
		// AuthorizeCreation's argument, applied here rather than repeated at each call site.
		// It is worth being blunt about what that means: on the write path this branch
		// constrains nothing beyond what the route guard already decided. It is named in
		// CP84's report under "where the retrofit is cosmetic", because it is.
		//
		// A *read* is a different matter and is left to Can, which refuses unless the
		// caller has actually looked the record up and can name its owner. A field worker
		// reading a record they did not make is refused, which is the rule.
		if !reading {
			return AuthorizeCreation(ctx, action)
		}
		return Authorize(ctx, action, resource)
	}
	if subject.StationCode == "" {
		// A station-scoped permission held by somebody standing nowhere. Refused without a
		// query: there is no station to ask about.
		return errs.ErrForbidden.WithDetail(fmt.Errorf(
			"denied %s: the active role works no station, and this permission reaches only a station [out_of_scope]", action))
	}

	reacher, ok := ReacherFrom(ctx)
	if !ok {
		return errs.ErrForbidden.WithDetail(ErrNoReacher)
	}

	var station string
	var held2, visitOpen bool
	if reading {
		reach, err := reacher.ReadReachAt(ctx, subject.FacilityID, subject.StationCode, patientID)
		if err != nil {
			// A reach that could not be established is not a reach. The error is the log's;
			// the caller gets the same 403 as everybody else.
			return errs.ErrForbidden.WithDetail(fmt.Errorf("reading the station's reach: %w", err))
		}
		station, held2, visitOpen = reach.station, reach.held, reach.visitOpen
	} else {
		reach, err := reacher.WriteReachAt(ctx, subject.FacilityID, subject.StationCode, patientID)
		if err != nil {
			return errs.ErrForbidden.WithDetail(fmt.Errorf("reading the station's reach: %w", err))
		}
		station, held2, visitOpen = reach.station, reach.held, reach.visitOpen
	}

	if !held2 {
		// Two different sentences for the log, one response. The desk that cannot see a
		// patient needs to be told the difference between "not in the building" and "not
		// yours", but the *client* must not be: a 403 that varies by the state of a record
		// is a way to ask questions about records you may not read. The difference is
		// recorded here, free of PHI — no patient id, no name, no visit — and the operator
		// hears it from the person reading the log.
		why := "this station has no queue entry or encounter for them in the current visit"
		if !visitOpen {
			why = "the patient has no open visit, so there is no current visit for any station to reach through"
		}
		return errs.ErrForbidden.WithDetail(fmt.Errorf("denied %s: %s [out_of_scope, rule station_reach]", action, why))
	}

	// The reach was found, and the station it was found at is the subject's own — which is
	// what makes it honest to put that station on the resource. Can then applies the same
	// station comparison it applies everywhere else, so there is one rule and not two.
	resource.StationCode = station
	return Authorize(ctx, action, resource)
}

// --- the HTTP-shaped door ---

// GuardPatientRead judges a read of one patient and, on a refusal, writes it.
//
// The same decision as AuthorizeStationRead with the two lines every handler would
// otherwise write around it. It exists because the alternative — each of the twenty-odd
// clinical modules growing its own three-line wrapper — is twenty chances to write the
// refusal differently, and a 403 that differs between modules is a 403 that tells the
// caller which module refused.
//
// Reports whether the handler may continue. A false means the response has already been
// written and nothing further should be.
func GuardPatientRead(w http.ResponseWriter, r *http.Request, logger *slog.Logger, action Action, kind string, patientID uuid.UUID) bool {
	if err := AuthorizeStationRead(r.Context(), action, kind, patientID); err != nil {
		httpx.WriteError(w, r, logger, err)
		return false
	}
	return true
}

// GuardPatientWrite is the same for an act that records against the patient. The narrower
// of the two reaches: only the patient this station currently holds.
func GuardPatientWrite(w http.ResponseWriter, r *http.Request, logger *slog.Logger, action Action, kind string, patientID uuid.UUID) bool {
	if err := AuthorizeStationWrite(r.Context(), action, kind, patientID); err != nil {
		httpx.WriteError(w, r, logger, err)
		return false
	}
	return true
}

// --- the test double ---

// ReacherForTest is a Reacher that answers the same way for every patient.
//
// It exists for the module tests — allergy, consent, counseling, nutrition, exercise,
// clinical — which drive real routes with a fake authorizer in order to test what the
// *domain* does, not what the engine decides. Those tests have no queue entries, because a
// nutrition test is not about queues, and making each of them stage a visit and a queue row
// to reach its own handler would be testing the engine eight times over by accident.
//
// What it therefore does NOT prove is anything at all about reach. A test using this has
// said "assume this station holds this patient" — which is the assumption the real clinic
// makes true and which internal/rbac's reach_db_test.go and cmd/api's escalation test hold
// against the real tables and the real query. If this were the only Reacher in the test
// suite, the reach rule would be unverified; it is not, and the two tests that verify it are
// named here so a reader can check that claim rather than take it.
//
// dthclint refuses a call to this from anything but a _test.go file, exactly as it does for
// eventstore.ActorForTest and ReaderForTest.
//
//dthclint:testonly
func ReacherForTest(reaches bool) Reacher { return fixedReach{reaches: reaches} }

type fixedReach struct{ reaches bool }

func (f fixedReach) WriteReachAt(_ context.Context, _ uuid.UUID, station string, _ uuid.UUID) (WriteReach, error) {
	return WriteReach{station: station, held: f.reaches, visitOpen: true}, nil
}

func (f fixedReach) ReadReachAt(_ context.Context, _ uuid.UUID, station string, _ uuid.UUID) (ReadReach, error) {
	return ReadReach{station: station, held: f.reaches, visitOpen: true}, nil
}

// GrantedForTest is the context a real route guard would have left behind.
//
// The module tests each carry a hand-written Authorizer that attaches a principal and
// nothing else, because before ADR-0036 nothing else was needed: the service layer had no
// call sites, so a subject on the context was a thing nobody read. It is read now, by every
// scoped handler, and a test authorizer that leaves none produces a 403 that looks like a
// policy refusal and is really a missing fixture.
//
// So this is the one function that says what "authorised" means for a test: the subject the
// resolver would have built, standing where the role stands, and a reach store. The reach
// store is ReacherForTest — read its note for what that costs — and the subject is real, so
// a test whose role genuinely lacks a permission is still refused for the real reason.
//
//dthclint:testonly
func GrantedForTest(ctx context.Context, caller httpx.Caller, role, station string) context.Context {
	userID, _ := uuid.Parse(caller.UserID)
	facilityID, _ := uuid.Parse(caller.FacilityID)
	subject := Subject{
		UserID: userID, FacilityID: facilityID,
		Roles: []auth.RoleCode{auth.RoleCode(role)}, ActiveRole: auth.RoleCode(role),
		StationCode: station, Permissions: auth.NewPermissionSet(caller.Permissions...),
	}
	// fixedReach directly rather than ReacherForTest, because dthclint's testonly check
	// reads the call site and not the caller: one test-only door calling another looks
	// exactly like production code calling one.
	return WithReacher(WithSubject(ctx, subject), fixedReach{reaches: true})
}
