package auth

import "sort"

// PermissionSet is what a user may do, resolved across every role they currently hold.
//
// A set rather than a list because [R-02] means overlap is the normal case: an assistant
// covering anthropometry and vitals holds two roles that both grant
// observation.read.values, and the answer to "may they?" must not depend on how many times
// it was granted.
type PermissionSet map[string]struct{}

// NewPermissionSet builds a set from codes, discarding duplicates.
func NewPermissionSet(codes ...string) PermissionSet {
	set := make(PermissionSet, len(codes))
	for _, code := range codes {
		set[code] = struct{}{}
	}
	return set
}

// Has reports whether the permission is held.
func (p PermissionSet) Has(code string) bool {
	_, ok := p[code]
	return ok
}

// HasAll requires every one. Used where an action needs more than one permission — signing
// a prescription needs both the draft and the signature right.
func (p PermissionSet) HasAll(codes ...string) bool {
	for _, code := range codes {
		if !p.Has(code) {
			return false
		}
	}
	return true
}

// HasAny is the readable form of a check against several alternatives.
func (p PermissionSet) HasAny(codes ...string) bool {
	for _, code := range codes {
		if p.Has(code) {
			return true
		}
	}
	return false
}

// Union merges another set in place, which is how a multi-role user's permissions are built.
func (p PermissionSet) Union(other PermissionSet) PermissionSet {
	for code := range other {
		p[code] = struct{}{}
	}
	return p
}

// Codes returns the permissions in sorted order.
//
// Sorted rather than in map order, because this ends up in a log line, a session claim and
// an administrative screen, and a set that reorders itself between two renders looks like a
// change when nothing changed.
func (p PermissionSet) Codes() []string {
	out := make([]string, 0, len(p))
	for code := range p {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

// Len is the number of distinct permissions held.
func (p PermissionSet) Len() int { return len(p) }

// Sensitive reports whether the set includes anything that reveals a diagnosis or a
// clinical interpretation.
//
// This is the check behind blueprint §4.4's blinding rules. The database asserts the same
// property of the seeded catalogue; this asks it of a particular user at a particular
// moment, which is the question an interface actually has.
func (p PermissionSet) Sensitive() bool {
	for _, code := range SensitivePermissions {
		if p.Has(code) {
			return true
		}
	}
	return false
}
