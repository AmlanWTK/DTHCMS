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
	// StationID is where the person is working right now, for the station-scoped roles.
	StationID *uuid.UUID
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
	// StationID is where the resource is right now — the station a patient is queued at
	// today — for roles scoped to their own station. Nil when it is nowhere.
	StationID *uuid.UUID
	// OwnerID is who created the resource, for roles scoped to their own records.
	OwnerID *uuid.UUID
	// Sensitive marks a resource that carries a diagnosis or a clinical interpretation.
	// Blinded roles are refused these whatever their permissions say.
	Sensitive bool
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
)

// Decision is the answer, with its working.
type Decision struct {
	Allowed bool
	Reason  Reason
	// Rule names the rule that decided, for a deny: "nutritionist_no_prescriptions".
	Rule string
	// Scope is the reach that applied, for an allow.
	Scope Scope
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
	switch scope {
	case ScopeAny:
	case ScopeOwnStation:
		if resource.StationID == nil || subject.StationID == nil || *resource.StationID != *subject.StationID {
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
func scopeFor(role auth.RoleCode, action Action) Scope {
	if !isClinical(action) {
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
