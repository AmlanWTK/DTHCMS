package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Break-glass (CP22, D-70): the emergency door.
//
// A clinician who needs a record their role does not reach — the unconscious patient
// whose regular physician is away — says so in writing, proves it is them with their
// authenticator, and gets a bounded access. Three things happen at once and none can be
// skipped: the justification is stored, the audit chain gets a row, and every
// administrator's console shows an alert until one of them acknowledges it. The door
// exists because the alternative is staff sharing passwords; it is loud because that is
// what makes it safe to have.
//
// What the access *unlocks* is the clinical checkpoints' business: they ask ForUser and
// widen the RBAC subject accordingly. This module owns the door, the record and the alarm.

// Actor is the person at the console, as the handler read them from the session.
type Actor struct {
	UserID     uuid.UUID
	FacilityID uuid.UUID
	Code       string
	ActiveRole string
	SessionID  *uuid.UUID
	Client     []byte
}

// Access is one opened door.
type Access struct {
	ID             uuid.UUID
	FacilityID     uuid.UUID
	UserID         uuid.UUID
	ActiveRole     string
	ScopeKind      string
	ScopeRef       string
	Justification  string
	GrantedAt      time.Time
	ExpiresAt      time.Time
	EndedAt        *time.Time
	EndedBy        *uuid.UUID
	EndReason      string
	AcknowledgedBy *uuid.UUID
	AcknowledgedAt *time.Time
	AuditSeq       *int64
}

// Alert is one thing an administrator must see.
type Alert struct {
	ID             uuid.UUID
	FacilityID     uuid.UUID
	Kind           string
	Severity       string
	MessageEN      string
	MessageBN      string
	Reference      map[string]any
	AuditSeq       *int64
	CreatedAt      time.Time
	AcknowledgedBy *uuid.UUID
	AcknowledgedAt *time.Time
}

// OpenRequest is what the clinician types.
type OpenRequest struct {
	// ScopeKind is "patient" (ScopeRef is the patient id) or "other" (ScopeRef says what).
	ScopeKind string
	ScopeRef  string
	// Justification is typed, at least twenty characters. Criterion 3.
	Justification string
	// Duration defaults to DefaultAccess and is capped at MaxAccess.
	Duration time.Duration
}

const (
	// DefaultAccess is how long the door stays open unless the clinician says otherwise.
	DefaultAccess = 4 * time.Hour
	// MaxAccess is the most that can be asked for; the database CHECK agrees.
	MaxAccess = 24 * time.Hour
	// MinJustification is the shortest justification accepted — in runes, because a
	// Bengali sentence is not shorter for being written in Bengali.
	MinJustification = 20
)

var (
	ErrJustificationRequired = errors.New("break-glass: a justification of at least twenty characters is required")
	ErrScopeRequired         = errors.New("break-glass: say what the access is for")
	ErrAlreadyEnded          = errors.New("break-glass: this access has already ended")
	ErrAlreadyAcknowledged   = errors.New("break-glass: already acknowledged")
	ErrNotPermitted          = errors.New("break-glass: not permitted")
)

// BreakGlass is the service. It records through the same Recorder as everything else, and
// refuses the access if the record cannot be written: an emergency access nobody can
// review afterwards is the one thing this must never produce.
type BreakGlass struct {
	store    *PostgresStore
	recorder *Recorder
	clock    clock.Clock
	codes    CodeLookup
}

// CodeLookup answers "what is this person's employee code" for the sentences. It is an
// interface because the answer lives in auth's tables, which this module does not read;
// the composition root passes auth's store.
type CodeLookup interface {
	EmployeeCode(ctx context.Context, userID uuid.UUID) (string, error)
}

func NewBreakGlass(store *PostgresStore, recorder *Recorder, clk clock.Clock, codes CodeLookup) *BreakGlass {
	if clk == nil {
		clk = clock.Real{}
	}
	return &BreakGlass{store: store, recorder: recorder, clock: clk, codes: codes}
}

// Open opens the door: stores the access, chains the audit row, raises the alert.
func (b *BreakGlass) Open(ctx context.Context, actor Actor, req OpenRequest) (Access, error) {
	req.Justification = strings.TrimSpace(req.Justification)
	req.ScopeRef = strings.TrimSpace(req.ScopeRef)
	if utf8.RuneCountInString(req.Justification) < MinJustification {
		return Access{}, ErrJustificationRequired
	}
	if req.ScopeKind != "patient" && req.ScopeKind != "other" {
		return Access{}, ErrScopeRequired
	}
	if req.ScopeRef == "" {
		return Access{}, ErrScopeRequired
	}
	if req.ScopeKind == "patient" {
		if _, err := uuid.Parse(req.ScopeRef); err != nil {
			return Access{}, fmt.Errorf("%w: a patient is named by its id", ErrScopeRequired)
		}
	}
	if req.Duration <= 0 {
		req.Duration = DefaultAccess
	}
	if req.Duration > MaxAccess {
		req.Duration = MaxAccess
	}
	now := b.clock.Now()
	until := now.Add(req.Duration)

	row, err := b.store.q.OpenBreakGlass(ctx, dbgen.OpenBreakGlassParams{
		FacilityID: actor.FacilityID, UserID: actor.UserID, ActiveRole: actor.ActiveRole,
		ScopeKind: req.ScopeKind, ScopeRef: req.ScopeRef, Justification: req.Justification,
		GrantedAt: now, ExpiresAt: until,
	})
	if err != nil {
		return Access{}, err
	}
	access := accessFromRow(row)

	var patient *uuid.UUID
	if req.ScopeKind == "patient" {
		id, _ := uuid.Parse(req.ScopeRef)
		patient = &id
	}
	ev, err := b.recorder.Record(ctx, Entry{
		Kind: "break_glass.opened", FacilityID: actor.FacilityID,
		ActorID: &actor.UserID, ActorCode: actor.Code, ActorRole: actor.ActiveRole,
		PatientID: patient, SessionID: actor.SessionID,
		Reason: req.Justification, ClientDigest: actor.Client, At: now,
		Details: map[string]any{
			"access_id": access.ID.String(), "scope": scopeLabel(req), "scope_kind": req.ScopeKind,
			"scope_ref": req.ScopeRef, "until": until.UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		// The row exists and the chain does not know: close the door again rather than
		// leave an unrecorded access open.
		_, _ = b.store.q.EndBreakGlass(ctx, dbgen.EndBreakGlassParams{
			ID: access.ID, EndedAt: &now, EndedBy: uuid.NullUUID{UUID: actor.UserID, Valid: true},
			EndReason: "audit record could not be written",
		})
		return Access{}, err
	}
	_ = b.store.q.LinkBreakGlassAudit(ctx, dbgen.LinkBreakGlassAuditParams{ID: access.ID, AuditSeq: &ev.Seq})
	access.AuditSeq = &ev.Seq

	// The alarm. Raised in the same breath as the record: the criterion is "within one
	// minute", and the console polls every thirty seconds.
	ref, _ := json.Marshal(map[string]any{"access_id": access.ID.String(), "user_id": actor.UserID.String(), "audit_seq": ev.Seq})
	scope := scopeLabel(req)
	_, err = b.store.q.RaiseAdminAlert(ctx, dbgen.RaiseAdminAlertParams{
		FacilityID: actor.FacilityID, Kind: "break_glass", Severity: "high",
		MessageEn: fmt.Sprintf("%s broke the glass for %s until %s: %s",
			actor.Code, scope, until.In(Dhaka).Format("15:04"), req.Justification),
		MessageBn: fmt.Sprintf("%s %s পর্যন্ত %s-এর জন্য জরুরি প্রবেশাধিকার নিয়েছেন: %s",
			actor.Code, until.In(Dhaka).Format("15:04"), scope, req.Justification),
		Reference: ref, AuditSeq: &ev.Seq, CreatedAt: now,
	})
	if err != nil {
		return Access{}, fmt.Errorf("raising the alert: %w", err)
	}
	return access, nil
}

func scopeLabel(req OpenRequest) string {
	if req.ScopeKind == "patient" {
		return "patient " + req.ScopeRef
	}
	return req.ScopeRef
}

// Acknowledge is an administrator saying "I have seen this". It closes nothing.
func (b *BreakGlass) Acknowledge(ctx context.Context, actor Actor, id uuid.UUID) (Access, error) {
	now := b.clock.Now()
	row, err := b.store.q.AcknowledgeBreakGlass(ctx, dbgen.AcknowledgeBreakGlassParams{
		ID: id, AcknowledgedBy: uuid.NullUUID{UUID: actor.UserID, Valid: true}, AcknowledgedAt: &now,
	})
	if err != nil {
		if errors.Is(translate(err), ErrNotFound) {
			if _, lookup := b.store.q.BreakGlassByID(ctx, id); lookup == nil {
				return Access{}, ErrAlreadyAcknowledged
			}
			return Access{}, ErrNotFound
		}
		return Access{}, err
	}
	access := accessFromRow(row)
	if access.FacilityID != actor.FacilityID {
		return Access{}, ErrNotFound
	}
	_, _ = b.recorder.Record(ctx, Entry{
		Kind: "break_glass.acknowledged", FacilityID: actor.FacilityID,
		ActorID: &actor.UserID, ActorCode: actor.Code, ActorRole: actor.ActiveRole,
		TargetUserID: &access.UserID, TargetCode: b.codeOf(ctx, access.UserID),
		SessionID: actor.SessionID, ClientDigest: actor.Client, At: now,
		Details: map[string]any{"access_id": access.ID.String()},
	})
	return access, nil
}

// End closes the door before it expires — the clinician when they are done, or an
// administrator who decided it should not be open.
func (b *BreakGlass) End(ctx context.Context, actor Actor, id uuid.UUID, reason string) (Access, error) {
	now := b.clock.Now()
	existing, err := b.store.q.BreakGlassByID(ctx, id)
	if err != nil {
		return Access{}, translate(err)
	}
	if existing.FacilityID != actor.FacilityID {
		return Access{}, ErrNotFound
	}
	if existing.EndedAt != nil {
		return Access{}, ErrAlreadyEnded
	}
	row, err := b.store.q.EndBreakGlass(ctx, dbgen.EndBreakGlassParams{
		ID: id, EndedAt: &now, EndedBy: uuid.NullUUID{UUID: actor.UserID, Valid: true}, EndReason: strings.TrimSpace(reason),
	})
	if err != nil {
		return Access{}, translate(err)
	}
	access := accessFromRow(row)
	_, _ = b.recorder.Record(ctx, Entry{
		Kind: "break_glass.ended", FacilityID: actor.FacilityID,
		ActorID: &actor.UserID, ActorCode: actor.Code, ActorRole: actor.ActiveRole,
		TargetUserID: &access.UserID, TargetCode: b.codeOf(ctx, access.UserID),
		SessionID: actor.SessionID, Reason: strings.TrimSpace(reason), ClientDigest: actor.Client, At: now,
		Details: map[string]any{"access_id": access.ID.String()},
	})
	return access, nil
}

// Active lists the doors open at this facility right now.
func (b *BreakGlass) Active(ctx context.Context, facilityID uuid.UUID) ([]Access, error) {
	rows, err := b.store.q.ActiveBreakGlass(ctx, dbgen.ActiveBreakGlassParams{FacilityID: facilityID, ExpiresAt: b.clock.Now()})
	if err != nil {
		return nil, err
	}
	out := make([]Access, 0, len(rows))
	for _, row := range rows {
		out = append(out, accessFromRow(row))
	}
	return out, nil
}

// ForUser is what a person currently holds through the glass.
func (b *BreakGlass) ForUser(ctx context.Context, userID uuid.UUID) ([]Access, error) {
	rows, err := b.store.q.BreakGlassForUser(ctx, dbgen.BreakGlassForUserParams{UserID: userID, ExpiresAt: b.clock.Now()})
	if err != nil {
		return nil, err
	}
	out := make([]Access, 0, len(rows))
	for _, row := range rows {
		out = append(out, accessFromRow(row))
	}
	return out, nil
}

// codeOf looks a user's employee code up for the sentence; empty when it cannot.
func (b *BreakGlass) codeOf(ctx context.Context, userID uuid.UUID) string {
	if b.codes == nil {
		return ""
	}
	code, err := b.codes.EmployeeCode(ctx, userID)
	if err != nil {
		return ""
	}
	return code
}

// --- alerts ---

// RaiseAlert puts something in front of the administrators.
func (s *PostgresStore) RaiseAlert(ctx context.Context, a Alert) (Alert, error) {
	ref, _ := json.Marshal(a.Reference)
	if a.Reference == nil {
		ref = []byte("{}")
	}
	if a.Severity == "" {
		a.Severity = "high"
	}
	row, err := s.q.RaiseAdminAlert(ctx, dbgen.RaiseAdminAlertParams{
		FacilityID: a.FacilityID, Kind: a.Kind, Severity: a.Severity, MessageEn: a.MessageEN, MessageBn: a.MessageBN,
		Reference: ref, AuditSeq: a.AuditSeq, CreatedAt: a.CreatedAt,
	})
	if err != nil {
		return Alert{}, err
	}
	return alertFromRow(row), nil
}

// OpenAlerts is what the administrators' consoles poll.
func (s *PostgresStore) OpenAlerts(ctx context.Context, facilityID uuid.UUID, limit int) ([]Alert, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.q.OpenAdminAlerts(ctx, dbgen.OpenAdminAlertsParams{FacilityID: facilityID, Limit: int32(limit)}) //nolint:gosec // bounded above
	if err != nil {
		return nil, err
	}
	out := make([]Alert, 0, len(rows))
	for _, row := range rows {
		out = append(out, alertFromRow(row))
	}
	return out, nil
}

// AcknowledgeAlert marks one seen. Idempotent from the caller's side: a second
// acknowledgement is ErrAlreadyAcknowledged, not a change.
func (s *PostgresStore) AcknowledgeAlert(ctx context.Context, actor Actor, id uuid.UUID, now time.Time) (Alert, error) {
	row, err := s.q.AcknowledgeAdminAlert(ctx, dbgen.AcknowledgeAdminAlertParams{
		ID: id, AcknowledgedBy: uuid.NullUUID{UUID: actor.UserID, Valid: true}, AcknowledgedAt: &now, FacilityID: actor.FacilityID,
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(translate(err), ErrNotFound) {
			return Alert{}, ErrAlreadyAcknowledged
		}
		return Alert{}, err
	}
	return alertFromRow(row), nil
}

func accessFromRow(row dbgen.CoreBreakGlassAccess) Access {
	return Access{
		ID: row.ID, FacilityID: row.FacilityID, UserID: row.UserID, ActiveRole: row.ActiveRole,
		ScopeKind: row.ScopeKind, ScopeRef: row.ScopeRef, Justification: row.Justification,
		GrantedAt: row.GrantedAt, ExpiresAt: row.ExpiresAt, EndedAt: row.EndedAt, EndedBy: uuidPtr(row.EndedBy),
		EndReason: row.EndReason, AcknowledgedBy: uuidPtr(row.AcknowledgedBy), AcknowledgedAt: row.AcknowledgedAt,
		AuditSeq: row.AuditSeq,
	}
}

func alertFromRow(row dbgen.CoreAdminAlert) Alert {
	return Alert{
		ID: row.ID, FacilityID: row.FacilityID, Kind: row.Kind, Severity: row.Severity,
		MessageEN: row.MessageEn, MessageBN: row.MessageBn, Reference: detailsFromJSON(row.Reference),
		AuditSeq: row.AuditSeq, CreatedAt: row.CreatedAt, AcknowledgedBy: uuidPtr(row.AcknowledgedBy), AcknowledgedAt: row.AcknowledgedAt,
	}
}
