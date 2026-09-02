// Package auth owns identity: who works here, what they may do, and the record of who
// decided that.
//
// Everything downstream depends on it. [R-03] requires a user_id on every clinical event,
// so nothing can be attributed before this package can answer "who". [R-02] requires one
// person to hold several station roles at once, so a permission is never a property of a
// role alone — it is the union across every role a user currently holds.
//
// # Two layers over one set of rules
//
// The lifecycle transitions and the permission catalogue exist here in Go and also in the
// database, and that duplication is deliberate rather than accidental. The application
// needs to know that a transition is illegal *before* attempting it, so the caller gets a
// 422 naming the transition rather than a 500 naming a trigger. The database needs to know
// independently, because a rule that lives only in Go holds until someone opens psql during
// an incident or a second service is written.
//
// The risk of two representations is that they drift. That risk is answered by tests that
// compare them exactly, in both directions, against a real database — not by discipline.
//
// # What this package does not do
//
// Authentication (CP16), sessions (CP16), device binding (CP18) and enforcement (CP19,
// CP20) are separate checkpoints. This package answers what a user may do; it does not
// decide whether to let them.
package auth

import (
	"time"

	"github.com/google/uuid"
)

// Status is a user's position in the lifecycle.
type Status string

const (
	// StatusInvited — an account exists and has been offered, but no password is set.
	// The person cannot log in and holds no effective permissions.
	StatusInvited Status = "invited"

	// StatusActive — working. The only status whose role grants resolve to permissions.
	StatusActive Status = "active"

	// StatusSuspended — access withdrawn without touching the grants, so it can be
	// applied in the minute it is needed and reversed as quickly.
	StatusSuspended Status = "suspended"

	// StatusDeactivated — left. The row remains, because every value they ever entered
	// still has to name them [R-03].
	StatusDeactivated Status = "deactivated"
)

// User is a member of staff.
//
// There is no Delete anywhere in this package, and the database refuses one. A user who
// leaves is deactivated; their name stays attached to every value they entered.
type User struct {
	ID          uuid.UUID
	FacilityID  uuid.UUID
	Code        string // employee code, unique within the facility
	NameEN      string
	NameBN      string
	Phone       string
	Email       string
	Status      Status
	StatusNote  string
	StatusSince time.Time
	LastLoginAt *time.Time
	CreatedAt   time.Time
}

// Role is an entry in the catalogue.
type Role struct {
	ID          uuid.UUID
	Code        RoleCode
	NameEN      string
	NameBN      string
	Description string
	IsClinical  bool
	// Station is the role's primary station, where it has one. Administrative roles do
	// not work a station in the patient journey.
	Station StationCode
}

// Permission is one entry in the catalogue.
type Permission struct {
	Code        string
	Resource    string
	Action      string
	Scope       string
	Description string
	// Sensitive means holding this reveals a diagnosis or a clinical interpretation.
	// Blueprint §4.4 blinds registration and the pharmacist to exactly these.
	Sensitive bool
}

// Grant is one role held by one user, with the history of how it came and went.
//
// Revocation sets RevokedAt; the row is never deleted. That is what makes "who could sign
// a prescription on the fourteenth of March" a question with an answer.
type Grant struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	RoleID       uuid.UUID
	RoleCode     RoleCode
	FacilityID   uuid.UUID
	GrantedBy    *uuid.UUID
	GrantedAt    time.Time
	RevokedBy    *uuid.UUID
	RevokedAt    *time.Time
	RevokeReason string
}

// Live reports whether the grant is in force.
func (g Grant) Live() bool { return g.RevokedAt == nil }

// Station is a point of care in the patient journey.
type Station struct {
	ID       uuid.UUID
	Code     StationCode
	NameEN   string
	NameBN   string
	Room     string
	Sequence int
	// Staffed is false where the station exists in the design but nobody works it. The
	// queue must not route a patient to an empty room, and a four-person clinic should
	// not present itself as a fifteen-person one.
	Staffed bool
	Active  bool
}
