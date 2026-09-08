package main

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/dashboard"
)

// The two things the physician's dashboard needs from modules it may not import (CP73).
//
// `dashboard` composes six clinical modules and is deliberately kept out of `audit`, which
// depends on `platform` alone. Both bridges live here, in the one place allowed to know both
// sides, exactly as `auditBridge` and `counselingAuditBridge` do.

// dashboardAuditBridge records a dashboard view in the security trail (§4.5).
//
// # Why this is `patient.viewed` and not a kind of its own
//
// A review asking *who opened this patient's record* must get one answer. A second event kind
// would mean every such query missed half the openings until somebody remembered to add it —
// and the person writing that query is doing it during an investigation, which is the worst
// moment to discover an incomplete list. So the kind is the one CP31 already registered and
// CP37's timeline already reuses, and what distinguishes the three is `by`.
//
// # Why the basis is on the line
//
// "Who read this record" and "on what authority" are the same question asked twice. CP22's
// `break_glass.opened` row records that a door was opened; it cannot record what was read
// through it, because break-glass is not a read. This line closes that gap: a reviewer
// filtering `patient.viewed` sees which openings happened under an emergency access without
// joining two tables on a time range.
type dashboardAuditBridge struct {
	recorder *audit.Recorder
}

var _ dashboard.AuditRecorder = (*dashboardAuditBridge)(nil)

func (b *dashboardAuditBridge) RecordDashboardView(ctx context.Context, e dashboard.AccessEntry) error {
	details := map[string]any{
		// The sentence template reads `{actor} opened the record of patient {target}` and
		// takes a count; one dashboard is one record.
		"count": 1,
		"by":    "dashboard",
	}
	if e.Basis != "" && e.Basis != string(dashboard.BasisNormal) {
		// Only when it is not the ordinary basis. A key present on every line is a key nobody
		// reads, and the one that matters is the exception.
		details["basis"] = e.Basis
	}
	patientID := e.PatientID
	entry := audit.Entry{
		Kind: "patient.viewed", FacilityID: e.FacilityID,
		ActorCode: e.ActorCode, ActorRole: e.ActorRole,
		PatientID: &patientID, At: e.At, Details: details,
	}
	if e.ActorID != uuid.Nil {
		id := e.ActorID
		entry.ActorID = &id
	}
	_, err := b.recorder.Record(ctx, entry)
	return err
}

// breakGlassBridge answers "is this record open to me through the emergency door".
//
// # Why the filtering happens here rather than in a query
//
// `audit.BreakGlass.ForUser` returns this person's accesses; the dashboard wants the live one
// covering this patient. Adding a `ForUserAndPatient` query to the audit module would put a
// patient id into a WHERE clause in the module that exists to police access, for the benefit
// of one caller — and the list is at most a handful of rows for one person. The filtering is
// three conditions and they are all stated below rather than left in SQL somebody has to open
// a second file to read.
type breakGlassBridge struct {
	service *audit.BreakGlass
	clock   interface{ Now() time.Time }
}

var _ dashboard.EmergencyAccess = (*breakGlassBridge)(nil)

func (b *breakGlassBridge) OpenFor(ctx context.Context, userID, patientID, facilityID uuid.UUID) (*dashboard.BreakGlassNote, error) {
	accesses, err := b.service.ForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := b.clock.Now().UTC()
	var newest *dashboard.BreakGlassNote
	for _, access := range accesses {
		switch {
		case access.FacilityID != facilityID:
			continue
		case access.ScopeKind != "patient" || access.ScopeRef != patientID.String():
			// An access opened for something other than this patient. `other` scopes exist —
			// a clinician who broke the glass to reach a report rather than a record — and
			// treating one as cover for a patient read would be widening a door somebody
			// deliberately narrowed.
			continue
		case access.EndedAt != nil:
			// Ended early, by the clinician or by an administrator. A door somebody closed is
			// closed, whatever its expiry says.
			continue
		case !access.ExpiresAt.After(now):
			continue
		}
		note := dashboard.BreakGlassNote{
			ID: access.ID, Justification: access.Justification,
			GrantedAt: access.GrantedAt, ExpiresAt: access.ExpiresAt,
			Acknowledged: access.AcknowledgedAt != nil,
		}
		// The newest, when somebody has opened two. Showing the oldest would show an expiry
		// that has already passed while a later access is what is actually holding the door.
		if newest == nil || note.GrantedAt.After(newest.GrantedAt) {
			copied := note
			newest = &copied
		}
	}
	return newest, nil
}
