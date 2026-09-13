// Package rbac is the one place a question of the form "may this person do this to that"
// is answered (CP19).
//
// Blueprint §4.4 states its access rules in prose; CP15 turned three of them into database
// invariants over the catalogue. This package turns the whole model into a function:
//
//	Can(subject, action, resource) → Decision
//
// deny by default, explicit deny beating any allow, with the reason attached — so that a
// refusal can be explained in a log line or an audit screen, and so that the decision
// matrix test can hold the model against the blueprint one cell at a time.
//
// Nothing here touches HTTP or the database. CP20 wires the function into the middleware
// and the serialiser; this package only decides.
package rbac

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
)

// Action is a permission code from the catalogue: "prescription.read".
type Action = string

// Subject is who is asking, as the engine needs to know them.
//
// Roles are every live grant. ActiveRole is the hat being worn now [R-02]: one operator
// may hold three roles, and each event stamps the role active at write time, so the
// decision is made for that role. Empty means "no hat chosen" — the union of every role's
// permissions applies, and every role's restrictions do too.
type Subject struct {
	UserID     uuid.UUID
	FacilityID uuid.UUID
	Roles      []auth.RoleCode
	ActiveRole auth.RoleCode
	// StationCode is where the person is standing right now, for the station-scoped
	// roles: "STN_NUTRITION". Empty for a role that works no station.
	//
	// Text and not a uuid, because every other place the schema names a station names it
	// this way — core.role.station_code, core.encounter.station_code,
	// core.queue_entry.station_code are all text. The uuid this field used to be existed
	// nowhere else in the system, which is why nothing was ever plumbed into it and why it
	// was nil on every HTTP request for the life of the engine (ADR-0036 §3).
	StationCode string
	// Permissions is the union across live roles, as /v1/auth/me reports it. When
	// ActiveRole is set the engine narrows to that role's own permissions.
	Permissions auth.PermissionSet
}

// Resource is the thing acted on, described by the facts the rules need. A caller that
// does not know a fact leaves it zero, and the rule that needs it denies.
type Resource struct {
	// Kind names the resource for the explanation: "patient", "prescription", "device".
	Kind string
	// FacilityID is the facility the resource belongs to. Zero for a resource that has
	// none (the catalogue itself); anything else must match the subject's.
	FacilityID uuid.UUID
	// StationCode is the station this resource is reachable from — the station the
	// subject was found to hold the patient at, not a property of the patient.
	//
	// A patient does not belong to a station and there is no column that says they do.
	// What fills this in is the reach query (reach.go), which asks whether the subject's
	// station has this patient in the current visit; when it does, the answer it writes
	// here is the subject's own station, because that is the station the reach was found
	// at. Empty means no station reaches this resource.
	StationCode string
	// OwnerID is who created the resource, for roles scoped to their own records.
	OwnerID *uuid.UUID
	// Sensitive marks a resource that carries a diagnosis or a clinical interpretation.
	// Blinded roles are refused these whatever their permissions say.
	Sensitive bool
	// ID is the resource itself, when the caller has one in hand and neither a station nor
	// an owner is the fact the rule needs. It is never compared to anything; it is here so
	// that a caller who has genuinely looked something up can say so, and be told apart
	// from a caller who filled in nothing. See identified.
	ID uuid.UUID
}

// identified reports whether this Resource describes a particular thing.
//
// A Resource with a Kind and a facility and nothing else is not a thing; it is the shape of
// a thing. The route guard used to build exactly that — Resource{Kind: "route", FacilityID:
// f} — and hand it to Can, which duly found that a station-scoped role's station did not
// match the resource's absent one and refused. Every clinical write in the clinic answered
// 403 for months, and the reason it gave was "out_of_scope", which sent everybody looking
// at the scope table rather than at the caller. Can now refuses that call with
// ReasonNoResource instead: not "you are out of reach" but "you asked the wrong layer".
func (r Resource) identified() bool {
	return r.StationCode != "" || r.OwnerID != nil || r.ID != uuid.Nil
}

// Scope is how far a role's permission reaches.
type Scope string

const (
	// ScopeAny: every resource in the facility.
	ScopeAny Scope = "any"
	// ScopeOwnStation: resources at the station the person is working now.
	ScopeOwnStation Scope = "own_station"
	// ScopeOwn: resources the person created.
	ScopeOwn Scope = "own"
)

// Reason is why a decision came out as it did. A closed list, so a dashboard can count
// them and a test can name them.
type Reason string

const (
	ReasonAllowed           Reason = "allowed"
	ReasonUnknownAction     Reason = "unknown_action"
	ReasonNoSubject         Reason = "no_subject"
	ReasonRoleNotHeld       Reason = "active_role_not_held"
	ReasonPermissionNotHeld Reason = "permission_not_held"
	ReasonOtherFacility     Reason = "other_facility"
	ReasonExplicitDeny      Reason = "explicit_deny"
	ReasonBlinded           Reason = "blinded_resource"
	ReasonOutOfScope        Reason = "out_of_scope"
	// ReasonNoResource: a scope narrower than ScopeAny had to be applied and the Resource
	// named no thing to apply it to. A programming error at the calling layer, never a
	// statement about the caller — see Resource.identified.
	ReasonNoResource Reason = "no_resource"
	// ReasonScopeNotEnforced: the route was reachable, but the reach its permission grants
	// this role is narrower than the facility and the route did not declare that its
	// handler settles that (httpx.PermissionScoped). Refused because the alternative is a
	// resource nobody checked. See Reaches.
	ReasonScopeNotEnforced Reason = "scope_not_enforced"
)

// Decision is the answer, with its working.
type Decision struct {
	Allowed bool
	Reason  Reason
	// Rule names the rule that decided, for a deny: "nutritionist_no_prescriptions".
	Rule string
	// Scope is the reach that applied, for an allow.
	Scope Scope
	// Deferred is the reach an *endpoint-layer* allow did not apply, and which the service
	// layer therefore still owes on the real resource. Empty from Can, which applies every
	// reach it finds; set by Reaches when the reach is narrower than the facility.
	Deferred Scope
	// Detail is a sentence for a human, free of PHI by construction: it names roles,
	// actions and rules, never people or patients.
	Detail string
}

// Explain renders the decision for a log line or an audit screen.
func (d Decision) Explain(action Action) string {
	if d.Allowed {
		return fmt.Sprintf("allowed %s (scope %s)", action, d.Scope)
	}
	if d.Rule != "" {
		return fmt.Sprintf("denied %s: %s [%s, rule %s]", action, d.Detail, d.Reason, d.Rule)
	}
	return fmt.Sprintf("denied %s: %s [%s]", action, d.Detail, d.Reason)
}

func deny(reason Reason, rule, detail string) Decision {
	return Decision{Allowed: false, Reason: reason, Rule: rule, Detail: detail}
}

// Can is the decision function.
//
// The order of the checks is the order of the explanations a person would want: is that
// even a thing (unknown action), is there a rule that says no regardless, do you hold it
// at all, is it yours to reach, is it within your reach.
func Can(subject Subject, action Action, resource Resource) Decision {
	if d, ok := permitted(subject, action, resource); !ok {
		return d
	}

	if resource.FacilityID != uuid.Nil && resource.FacilityID != subject.FacilityID {
		return deny(ReasonOtherFacility, "", "the resource belongs to another facility")
	}

	// Scope: the widest reach any effective role grants for this action.
	scope, ok := widestScope(effectiveRoles(subject), action)
	if !ok {
		return deny(ReasonPermissionNotHeld, "", fmt.Sprintf("%s is not granted by an effective role", action))
	}
	if scope != ScopeAny && !resource.identified() {
		// The caller asked a resource question with no resource. Answering "out of scope"
		// would be a lie with a plausible ring to it; this says which layer got it wrong.
		return deny(ReasonNoResource, "resource_required",
			fmt.Sprintf("%s reaches %s, and the resource names no station, owner or identity to measure that against; "+
				"the endpoint layer wants Reaches, the service layer wants a resource it has looked up", action, scope))
	}
	switch scope {
	case ScopeAny:
	case ScopeOwnStation:
		if resource.StationCode == "" || subject.StationCode == "" || resource.StationCode != subject.StationCode {
			return deny(ReasonOutOfScope, "station_scope",
				fmt.Sprintf("%s reaches only the station being worked; the resource is not at it", action))
		}
	case ScopeOwn:
		if resource.OwnerID == nil || *resource.OwnerID != subject.UserID {
			return deny(ReasonOutOfScope, "own_scope",
				fmt.Sprintf("%s reaches only records the person created", action))
		}
	}

	return Decision{Allowed: true, Reason: ReasonAllowed, Scope: scope}
}

// Reaches is the endpoint layer's question: may this subject reach this route at all?
//
// # Why it is a different function from Can
//
// Can answers "may this person do this to *that*". A route does not know what "that" is:
// the patient has not been loaded, the observation has not been parsed, nothing has been
// looked up — by design, because a 403 that depended on a lookup would say whether the
// thing exists. So the route guard used to invent a resource, `Resource{Kind: "route"}`,
// and hand it to Can. Can did what it was asked and measured a station-scoped role's reach
// against a resource standing at no station, which cannot match, and refused. `POST
// /v1/patients` answered 403 to every role in the catalogue, because the only two roles
// that hold `patient.write.demographics` are a station role and a field worker and both
// are scoped narrower than the facility. Seventy-eight declared routes were refused the
// same way. The 403 said "out_of_scope", which is a sentence about the caller, so the
// caller is where everybody looked.
//
// Reaches answers only the questions a route can answer: does the permission exist, is
// there a subject, is the hat held, does a blueprint rule refuse it outright, is the
// permission granted, is the request addressed to this person's facility. It applies no
// resource scope at all. What it does instead is *report* the scope it declined to apply,
// in Decision.Deferred, so the caller knows a resource check is still owed and can refuse
// the route outright if nothing downstream is going to make one.
//
// facilityID is the facility the request is addressed to. Zero skips the comparison, for a
// caller that has no facility in hand.
func Reaches(subject Subject, action Action, facilityID uuid.UUID) Decision {
	if d, ok := permitted(subject, action, Resource{}); !ok {
		return d
	}
	if facilityID != uuid.Nil && facilityID != subject.FacilityID {
		return deny(ReasonOtherFacility, "", "the request is addressed to another facility")
	}
	scope, ok := widestScope(effectiveRoles(subject), action)
	if !ok {
		return deny(ReasonPermissionNotHeld, "", fmt.Sprintf("%s is not granted by an effective role", action))
	}
	d := Decision{Allowed: true, Reason: ReasonAllowed, Scope: scope}
	if scope != ScopeAny {
		// Not a refusal, and not an allow to act on anything either: a debt. The route may
		// be entered; the resource has not been judged, and somebody downstream must judge
		// it before a response is written. httpx carries that obligation.
		d.Deferred = scope
	}
	return d
}

// Sees reports whether the subject may see a field guarded by the permission, for the
// serialiser: the same rules as Can minus facility and reach, which a field has none of.
func Sees(subject Subject, permission Action) bool {
	_, ok := permitted(subject, permission, Resource{})
	return ok
}

func effectiveRoles(subject Subject) []auth.RoleCode {
	if subject.ActiveRole != "" {
		return []auth.RoleCode{subject.ActiveRole}
	}
	return subject.Roles
}

// permitted runs the checks that need no resource facts beyond sensitivity: the action
// exists, there is a subject, the hat is held, no rule refuses, the permission is held.
func permitted(subject Subject, action Action, resource Resource) (Decision, bool) {
	if !knownActions[action] {
		return deny(ReasonUnknownAction, "", "no such permission in the catalogue"), false
	}
	if subject.UserID == uuid.Nil {
		return deny(ReasonNoSubject, "", "no authenticated subject"), false
	}

	// The roles the decision is made for: the active one, or all of them.
	if subject.ActiveRole != "" && !holds(subject.Roles, subject.ActiveRole) {
		return deny(ReasonRoleNotHeld, "", fmt.Sprintf("the active role %s is not held", subject.ActiveRole)), false
	}
	effective := effectiveRoles(subject)

	// Explicit denies beat any allow, and every effective role is checked: a person
	// wearing no particular hat is bound by every rule that binds any hat they own. They
	// are checked before the permission, so that the explanation names the blueprint's
	// rule rather than the catalogue's silence — the rule is the reason the catalogue is
	// silent.
	for _, role := range effective {
		for _, rule := range denyRules {
			if rule.applies(role, action, resource) {
				return deny(rule.reason, rule.name, rule.detail), false
			}
		}
	}

	// The permission must be held — by the active role when there is one, so that a
	// physician who also holds a station role does not carry the physician's reach into
	// the station's hat.
	if subject.ActiveRole != "" {
		if !RolePermissions[subject.ActiveRole].Has(action) {
			return deny(ReasonPermissionNotHeld, "",
				fmt.Sprintf("%s does not grant %s", subject.ActiveRole, action)), false
		}
	} else if !subject.Permissions.Has(action) {
		return deny(ReasonPermissionNotHeld, "", fmt.Sprintf("%s is not held by any live role", action)), false
	}
	return Decision{}, true
}

// Holds reports whether a subject holds a permission at all, ignoring scope.
//
// It is deliberately weaker than Can and has exactly one legitimate use: deciding whether a
// *subscription* may be opened, where there is no resource yet to measure a scope against
// (CP26). Every actual delivery still goes through Can, with the station the event happened
// at, so a station-scoped role's reach is enforced where it can be — on the message.
//
// Anywhere a resource exists, use Can. A permission check without a resource is not an
// access decision.
func Holds(subject Subject, action Action) bool {
	if !knownActions[action] {
		return false
	}
	if subject.ActiveRole != "" {
		if !holds(subject.Roles, subject.ActiveRole) {
			return false
		}
		return RolePermissions[subject.ActiveRole].Has(action)
	}
	return subject.Permissions.Has(action)
}

// RoleGrants reports whether one role's own permissions include an action.
//
// A string-keyed door onto the same catalogue `Can` reads, for the modules that hold a
// verified role from the principal and need to ask a question about it without a Subject —
// a Subject would mean a database read the engine has already done.
//
// Added at CP42, where the permission a write needs depends on the *body*: a height needs
// `observation.write.anthro` and a blood pressure needs `observation.write.vitals`, and
// neither can be a constant on a route. The active role rather than the union is the whole
// of [R-02]: an operator holding both hats must not record a blood pressure while wearing
// the anthropometry one, because the event would be attributed to a role not allowed to
// have taken it.
//
// It is not a security boundary on its own. The route guard has already refused a caller
// holding none of the write permissions; this narrows to the one the code actually needs.
func RoleGrants(role string, action Action) bool {
	if role == "" || !knownActions[action] {
		return false
	}
	return RolePermissions[auth.RoleCode(role)].Has(action)
}

func holds(roles []auth.RoleCode, role auth.RoleCode) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}

// widestScope returns the broadest reach among the effective roles that grant the action.
func widestScope(roles []auth.RoleCode, action Action) (Scope, bool) {
	best, found := ScopeOwn, false
	for _, role := range roles {
		if !RolePermissions[role].Has(action) {
			continue
		}
		found = true
		s := scopeFor(role, action)
		if rank(s) > rank(best) {
			best = s
		}
	}
	return best, found
}

func rank(s Scope) int {
	switch s {
	case ScopeAny:
		return 2
	case ScopeOwnStation:
		return 1
	default:
		return 0
	}
}

// --- explicit denies: blueprint §4.4, as rules with names ---

type denyRule struct {
	name   string
	reason Reason
	detail string
	// applies reports whether the rule refuses this role doing this action to this
	// resource. Rules are conservative: a fact the resource does not carry is not a pass.
	applies func(role auth.RoleCode, action Action, resource Resource) bool
}

// Sensitive permissions, as a set, for the blinding rules.
var sensitive = auth.NewPermissionSet(auth.SensitivePermissions...)

// Blinded roles must not see a diagnosis or a clinical interpretation, whatever form it
// takes: the permission (they do not hold one, and the database asserts it) or a resource
// that carries one under a permission they do hold.
var blinded = map[auth.RoleCode]bool{auth.RoleRegistration: true, auth.RolePharmacist: true}

var denyRules = []denyRule{
	{
		name:   "nutritionist_no_prescriptions",
		reason: ReasonExplicitDeny,
		detail: "blueprint §4.4: the nutritionist has no access to prescriptions",
		applies: func(role auth.RoleCode, action Action, _ Resource) bool {
			return role == auth.RoleNutritionist && strings.HasPrefix(action, "prescription.")
		},
	},
	{
		name:   "pharmacist_no_diagnoses",
		reason: ReasonExplicitDeny,
		detail: "blueprint §4.4: the pharmacist sees drugs and dosing only; diagnoses are hidden",
		applies: func(role auth.RoleCode, action Action, _ Resource) bool {
			return role == auth.RolePharmacist && (strings.HasPrefix(action, "diagnosis.") || sensitive.Has(action))
		},
	},
	{
		name:   "registration_blinded",
		reason: ReasonExplicitDeny,
		detail: "blueprint §4.4: registration is blinded to sensitive clinical data",
		applies: func(role auth.RoleCode, action Action, _ Resource) bool {
			return role == auth.RoleRegistration && sensitive.Has(action)
		},
	},
	{
		name:   "blinded_role_sensitive_resource",
		reason: ReasonBlinded,
		detail: "the resource carries a diagnosis or clinical interpretation, which this role may not see",
		applies: func(role auth.RoleCode, action Action, resource Resource) bool {
			return blinded[role] && resource.Sensitive && isRead(action)
		},
	},
	{
		name:   "field_worker_no_facility_records",
		reason: ReasonExplicitDeny,
		detail: "a field worker records outreach captures; clinic records are not theirs to read",
		applies: func(role auth.RoleCode, action Action, _ Resource) bool {
			return role == auth.RoleFieldWorker && (strings.HasPrefix(action, "records.") || strings.HasPrefix(action, "diagnosis."))
		},
	},
}

func isRead(action Action) bool {
	return strings.Contains(action, ".read") || strings.HasSuffix(action, ".query") || action == auth.PermQaReview
}

// --- scope ---

// scopeFor is a role's reach for an action.
//
// Clinical actions on a patient are scoped to the station for the station roles: a nurse
// at anthropometry reads the anthropometry queue, not the clinic. The reviewing roles —
// physician, junior doctor, QA — reach any patient; so do the administrative roles for
// the administrative actions, which have no station. Field workers reach the captures
// they made.
//
// The two exceptions below are corrections, not loosenings, and ADR-0036 §2 is their
// argument. The first draft of this function applied one rule to everything with a
// `patient.` prefix and swept two desks in with it that the blueprint never put there.
func scopeFor(role auth.RoleCode, action Action) Scope {
	if !isClinical(action) {
		return ScopeAny
	}
	if deskWide(role, action) {
		return ScopeAny
	}
	switch role {
	case auth.RolePhysician, auth.RoleJuniorDoctor, auth.RoleQa, auth.RoleAdmin, auth.RoleCrm, auth.RoleResearcher:
		return ScopeAny
	case auth.RoleFieldWorker:
		return ScopeOwn
	default:
		return ScopeOwnStation
	}
}

// deskWide names the two desks whose job is the facility rather than a queue (ADR-0036 §2).
//
// # Registration
//
// Registration *creates* the patient. At the moment `patient.write.demographics` is
// exercised there is no patient to measure a station against, so a station-scoped rule
// there is not strict — it is incoherent, and `AuthorizeCreation` exists because of it.
// The desk also legitimately amends any patient in the facility: correcting a mistyped
// name for somebody who is already at station 7 is what the desk is for, and a rule that
// refuses it sends the correction through the break-glass path, which is worse in every
// direction. Consent is the same act at the same desk and moves with it; reading back the
// demographics they just wrote is the same act again.
//
// This does not blind Registration to anything: `registration_blinded` above still refuses
// it every sensitive permission, and it holds no clinical permission beyond these.
//
// # Records
//
// The records office's entire job is the facility's records — pulling a historical file for
// a patient who is not on anybody's queue today is the job, not an exception to it. Its
// reach was the reason ADR-0036 could say "a station legitimately needing a patient it
// never queued" is already handled.
//
// `patient.merge` is deliberately *not* in this list even though RECORDS holds it. A merge
// is irreversible in effect and is the one act here where "the whole facility" is the wrong
// default; it keeps its station reach and its step-up.
func deskWide(role auth.RoleCode, action Action) bool {
	switch role {
	case auth.RoleRegistration:
		return action == auth.PermPatientWriteDemographics ||
			action == auth.PermPatientReadDemographics ||
			strings.HasPrefix(action, "patient.consent.")
	case auth.RoleRecords:
		return action == auth.PermPatientReadDemographics ||
			strings.HasPrefix(action, "records.")
	}
	return false
}

// ReachOf is one role's reach for one action, as a fact anybody may read.
//
// scopeFor stays unexported because it is a rule; this is the same answer, exported,
// because two things outside the engine need to ask it and neither is making a decision
// with it: the generated access matrix, and the route sweep that reports which routes
// declare a permission no role can exercise facility-wide. A sweep that had to infer the
// reach from a sequence of Can calls would be inferring it, and would drift.
//
// It is not an authorisation check. Nothing may act on this; act on Can or Reaches.
func ReachOf(role auth.RoleCode, action Action) Scope {
	if !knownActions[action] || !RolePermissions[role].Has(action) {
		return ""
	}
	return scopeFor(role, action)
}

// isClinical: actions on a patient's record, as opposed to on the clinic's configuration.
func isClinical(action Action) bool {
	for _, prefix := range []string{
		"patient.", "observation.", "counseling.tick", "records.", "lab.", "diagnosis.",
		"prescription.", "ai.", "education.", "qa.",
	} {
		if strings.HasPrefix(action, prefix) {
			return true
		}
	}
	return false
}

// knownActions is the catalogue as a set.
var knownActions = func() map[string]bool {
	m := make(map[string]bool, len(auth.AllPermissions))
	for _, p := range auth.AllPermissions {
		m[p] = true
	}
	return m
}()
