package audit

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// Handlers serve /v1/audit.
//
// Reading the trail needs audit.read (administrators, QA, the chief consultant — see the
// catalogue). Breaking the glass needs a clinical role and a step-up; acknowledging an
// alert needs audit.read. Exporting is itself audited, because an export is a copy of the
// trail leaving the system.
type Handlers struct {
	recorder     *Recorder
	store        *PostgresStore
	breakGlass   *BreakGlass
	signer       *Signer
	facilityName func(uuid.UUID) string
	stepUp       httpx.StepUpVerifier
	clock        clock.Clock
	logger       *slog.Logger
}

type HandlersConfig struct {
	Recorder   *Recorder
	Store      *PostgresStore
	BreakGlass *BreakGlass
	Signer     *Signer
	// FacilityName is printed on the export; nil prints the id.
	FacilityName func(uuid.UUID) string
	// StepUp verifies the token a break-glass request carries; nil refuses every one.
	StepUp httpx.StepUpVerifier
	Clock  clock.Clock
	Logger *slog.Logger
}

// PurposeBreakGlass is the step-up purpose the door requires. Declared here and known to
// auth's purpose list; the two are compared in a test.
const PurposeBreakGlass = "break_glass"

// PermAuditRead and PermBreakGlass name the catalogue permissions this module checks.
// Strings rather than auth's constants because audit does not import auth; the contract
// test in cmd/api compares them with the catalogue.
const (
	PermAuditRead           = "audit.read"
	PermPatientReadClinical = "patient.read.clinical"
	PermPatientReadDemo     = "patient.read.demographics"
)

func NewHandlers(cfg HandlersConfig) *Handlers {
	h := &Handlers{
		recorder: cfg.Recorder, store: cfg.Store, breakGlass: cfg.BreakGlass, signer: cfg.Signer,
		facilityName: cfg.FacilityName, stepUp: cfg.StepUp, clock: cfg.Clock, logger: cfg.Logger,
	}
	if h.clock == nil {
		h.clock = clock.Real{}
	}
	if h.logger == nil {
		h.logger = slog.Default()
	}
	if h.facilityName == nil {
		h.facilityName = func(id uuid.UUID) string { return id.String() }
	}
	return h
}

// Mount attaches the endpoints under /v1/audit.
func (h *Handlers) Mount(r chi.Router) {
	read := httpx.Permission(PermAuditRead)
	r.Route("/audit", func(a chi.Router) {
		a.Method("GET", "/events", httpx.Declare(read, h.events))
		a.Method("GET", "/kinds", httpx.Declare(httpx.Session(), h.kinds))
		a.Method("GET", "/chain", httpx.Declare(read, h.chain))
		a.Method("GET", "/export", httpx.Declare(read, h.export))
		a.Method("GET", "/signing-key", httpx.Declare(httpx.Session(), h.signingKey))
		a.Method("GET", "/alerts", httpx.Declare(read, h.alerts))
		a.Method("POST", "/alerts/{id}/acknowledge", httpx.Declare(read, h.acknowledgeAlert))

		// The door. Permission first, then the step-up, for the reason the console gives
		// (a person without the permission learns nothing about the door).
		clinical := httpx.Permission(PermPatientReadClinical, PermPatientReadDemo)
		stepped := httpx.RequireStepUp(h.logger, h.stepUp, PurposeBreakGlass)(http.HandlerFunc(h.openBreakGlass))
		a.Method("POST", "/break-glass", httpx.Declare(clinical, stepped.ServeHTTP))
		a.Method("GET", "/break-glass", httpx.Declare(read, h.activeBreakGlass))
		a.Method("GET", "/break-glass/mine", httpx.Declare(httpx.Session(), h.myBreakGlass))
		a.Method("POST", "/break-glass/{id}/end", httpx.Declare(httpx.Session(), h.endBreakGlass))
		a.Method("POST", "/break-glass/{id}/acknowledge", httpx.Declare(read, h.acknowledgeBreakGlass))
	})
}

// --- views ---

type eventView struct {
	Seq        int64          `json:"seq"`
	Kind       string         `json:"kind"`
	LabelEN    string         `json:"label_en"`
	LabelBN    string         `json:"label_bn"`
	RecordedAt time.Time      `json:"recorded_at"`
	Actor      subjectView    `json:"actor"`
	ActorRole  string         `json:"actor_role"`
	Target     *subjectView   `json:"target"`
	PatientID  *uuid.UUID     `json:"patient_id"`
	DeviceID   *uuid.UUID     `json:"device_id"`
	Reason     string         `json:"reason"`
	Details    map[string]any `json:"details"`
	SentenceEN string         `json:"sentence_en"`
	SentenceBN string         `json:"sentence_bn"`
	Hash       string         `json:"hash"`
}

type subjectView struct {
	ID   *uuid.UUID `json:"id"`
	Code string     `json:"code"`
}

type accessView struct {
	ID             uuid.UUID  `json:"id"`
	UserID         uuid.UUID  `json:"user_id"`
	ActiveRole     string     `json:"active_role"`
	ScopeKind      string     `json:"scope_kind"`
	ScopeRef       string     `json:"scope_ref"`
	Justification  string     `json:"justification"`
	GrantedAt      time.Time  `json:"granted_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	EndedAt        *time.Time `json:"ended_at"`
	EndReason      string     `json:"end_reason"`
	AcknowledgedAt *time.Time `json:"acknowledged_at"`
	AuditSeq       *int64     `json:"audit_seq"`
}

type alertView struct {
	ID        uuid.UUID      `json:"id"`
	Kind      string         `json:"kind"`
	Severity  string         `json:"severity"`
	MessageEN string         `json:"message_en"`
	MessageBN string         `json:"message_bn"`
	Reference map[string]any `json:"reference"`
	AuditSeq  *int64         `json:"audit_seq"`
	CreatedAt time.Time      `json:"created_at"`
}

func viewEvent(ev Event) eventView {
	v := eventView{
		Seq: ev.Seq, Kind: ev.Kind, LabelEN: Label(ev.Kind, English), LabelBN: Label(ev.Kind, Bangla),
		RecordedAt: ev.RecordedAt, Actor: subjectView{ID: ev.ActorID, Code: ev.ActorCode}, ActorRole: ev.ActorRole,
		PatientID: ev.PatientID, DeviceID: ev.DeviceID, Reason: ev.Reason, Details: ev.Details,
		SentenceEN: Describe(ev, English), SentenceBN: Describe(ev, Bangla),
		Hash: fmt.Sprintf("%x", ev.Hash),
	}
	if ev.TargetUserID != nil || ev.TargetCode != "" {
		v.Target = &subjectView{ID: ev.TargetUserID, Code: ev.TargetCode}
	}
	if v.Details == nil {
		v.Details = map[string]any{}
	}
	return v
}

func viewAccess(a Access) accessView {
	return accessView{
		ID: a.ID, UserID: a.UserID, ActiveRole: a.ActiveRole, ScopeKind: a.ScopeKind, ScopeRef: a.ScopeRef,
		Justification: a.Justification, GrantedAt: a.GrantedAt, ExpiresAt: a.ExpiresAt, EndedAt: a.EndedAt,
		EndReason: a.EndReason, AcknowledgedAt: a.AcknowledgedAt, AuditSeq: a.AuditSeq,
	}
}

func viewAlert(a Alert) alertView {
	ref := a.Reference
	if ref == nil {
		ref = map[string]any{}
	}
	return alertView{
		ID: a.ID, Kind: a.Kind, Severity: a.Severity, MessageEN: a.MessageEN, MessageBN: a.MessageBN,
		Reference: ref, AuditSeq: a.AuditSeq, CreatedAt: a.CreatedAt,
	}
}

// --- helpers ---

func (h *Handlers) actor(w http.ResponseWriter, r *http.Request) (Actor, bool) {
	caller, ok := httpx.CallerFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return Actor{}, false
	}
	userID, err1 := uuid.Parse(caller.UserID)
	facilityID, err2 := uuid.Parse(caller.FacilityID)
	if err1 != nil || err2 != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return Actor{}, false
	}
	actor := Actor{
		UserID: userID, FacilityID: facilityID, Code: caller.Code,
		ActiveRole: strings.TrimSpace(r.Header.Get(httpx.ActiveRoleHeader)), Client: clientDigest(r),
	}
	if sid, err := uuid.Parse(caller.SessionID); err == nil {
		actor.SessionID = &sid
	}
	return actor, true
}

func (h *Handlers) id(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handlers) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrJustificationRequired):
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithField("justification", "at least twenty characters, typed"))
	case errors.Is(err, ErrScopeRequired):
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithField("scope", err.Error()))
	case errors.Is(err, ErrAlreadyEnded), errors.Is(err, ErrAlreadyAcknowledged):
		httpx.WriteError(w, r, h.logger, errs.ErrConflict.WithDetail(err))
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
	case errors.Is(err, ErrNotPermitted):
		httpx.WriteError(w, r, h.logger, errs.ErrForbidden)
	default:
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
	}
}

// clientDigest is the SHA-256 of the socket's host — the same derivation auth uses, so
// the two trails agree about who "the same client" is.
func clientDigest(r *http.Request) []byte {
	if r.RemoteAddr == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if host == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(host))
	return sum[:]
}

// queryFrom reads the viewer's filters. Dates are day-granular in Dhaka: "2026-09-03" means
// that day on the clinic's wall clock, not in UTC.
func (h *Handlers) queryFrom(r *http.Request, facilityID uuid.UUID) (Query, error) {
	q := Query{FacilityID: facilityID, Limit: 100}
	get := r.URL.Query().Get
	if v := get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			return q, errs.ErrValidation.WithField("limit", "1 to 500")
		}
		q.Limit = n
	}
	if v := get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			return q, errs.ErrValidation.WithField("before", "a sequence number")
		}
		q.Before = n
	}
	if v := get("kind"); v != "" {
		if !Known(v) {
			return q, errs.ErrValidation.WithField("kind", "unknown kind")
		}
		q.Kind = v
	}
	q.ActorCode = strings.ToUpper(strings.TrimSpace(get("actor")))
	q.SubjectCode = strings.ToUpper(strings.TrimSpace(get("person")))
	if v := get("patient"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return q, errs.ErrValidation.WithField("patient", "a patient id")
		}
		q.PatientID = &id
	}
	if v := get("from"); v != "" {
		d, err := time.ParseInLocation("2006-01-02", v, Dhaka)
		if err != nil {
			return q, errs.ErrValidation.WithField("from", "YYYY-MM-DD")
		}
		q.Since = d
	}
	if v := get("to"); v != "" {
		d, err := time.ParseInLocation("2006-01-02", v, Dhaka)
		if err != nil {
			return q, errs.ErrValidation.WithField("to", "YYYY-MM-DD")
		}
		q.Until = d.AddDate(0, 0, 1)
	}
	return q, nil
}

// --- reads ---

func (h *Handlers) events(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	q, err := h.queryFrom(r, actor.FacilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	events, err := h.store.Query(r.Context(), q)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	views := make([]eventView, 0, len(events))
	for _, ev := range events {
		views = append(views, viewEvent(ev))
	}
	var next *int64
	if len(events) == q.Limit && len(events) > 0 {
		last := events[len(events)-1].Seq
		next = &last
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"events": views, "next_before": next})
}

func (h *Handlers) kinds(w http.ResponseWriter, r *http.Request) {
	type kindView struct {
		Kind    string `json:"kind"`
		LabelEN string `json:"label_en"`
		LabelBN string `json:"label_bn"`
	}
	out := make([]kindView, 0, len(Kinds))
	for _, k := range KindList() {
		out = append(out, kindView{Kind: k, LabelEN: Label(k, English), LabelBN: Label(k, Bangla)})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"kinds": out})
}

type chainView struct {
	OK       bool   `json:"ok"`
	Checked  int64  `json:"checked"`
	HeadSeq  int64  `json:"head_seq"`
	BrokenAt *int64 `json:"broken_at"`
	Problem  string `json:"problem"`
	Strays   int64  `json:"strays"`
}

func (h *Handlers) chain(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	v, err := h.recorder.Verify(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	outcome := fmt.Sprintf("%d rows intact", v.Checked)
	if !v.OK {
		outcome = v.Problem
	}
	// The verification is itself an audit event — and, when it failed, an alert. A row
	// appended after a break does not repair the chain; the break stays where it is.
	_, _ = h.recorder.Record(r.Context(), Entry{
		Kind: "audit.verified", FacilityID: actor.FacilityID, ActorID: &actor.UserID, ActorCode: actor.Code,
		ActorRole: actor.ActiveRole, SessionID: actor.SessionID, ClientDigest: actor.Client,
		Details: map[string]any{"outcome": outcome, "checked": v.Checked, "ok": v.OK},
	})
	view := chainView{OK: v.OK, Checked: v.Checked, HeadSeq: v.HeadSeq, Problem: v.Problem, Strays: v.Strays}
	if !v.OK {
		view.BrokenAt = &v.BrokenAt
		h.chainBroken(r.Context(), actor.FacilityID, v)
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

// chainBroken is what a failed verification does beyond reporting: a row of its own
// (appended after the break — the chain from here on is sound, the break stays visible)
// and an alert every administrator sees. The nightly verifier calls the same thing.
func (h *Handlers) chainBroken(ctx context.Context, facilityID uuid.UUID, v Verification) {
	ev, err := h.recorder.Record(ctx, Entry{
		Kind: "audit.chain_broken", FacilityID: facilityID,
		Details: map[string]any{"seq": v.BrokenAt, "problem": v.Problem, "checked": v.Checked},
	})
	var seq *int64
	if err == nil {
		seq = &ev.Seq
	}
	_, _ = h.store.RaiseAlert(ctx, Alert{
		FacilityID: facilityID, Kind: "chain_broken", Severity: "high",
		MessageEN: fmt.Sprintf("The audit chain failed verification at row %d: %s", v.BrokenAt, v.Problem),
		MessageBN: fmt.Sprintf("অডিট চেইন %d নম্বর সারিতে যাচাইয়ে ব্যর্থ হয়েছে: %s", v.BrokenAt, v.Problem),
		Reference: map[string]any{"broken_at": v.BrokenAt}, AuditSeq: seq, CreatedAt: h.clock.Now(),
	})
}

func (h *Handlers) signingKey(w http.ResponseWriter, r *http.Request) {
	if h.signer == nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnavailable)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"key_id": h.signer.KeyID(), "algorithm": Algorithm, "public_key": h.signer.PublicKey(),
	})
}

// Signature headers on an export. The file is the body; the signature rides beside it
// so the browser can save both.
const (
	HeaderSignature = "X-Audit-Signature"
	HeaderKeyID     = "X-Audit-Key-Id"
	HeaderDigest    = "X-Audit-Digest"
)

func (h *Handlers) export(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	if h.signer == nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnavailable)
		return
	}
	q, err := h.queryFrom(r, actor.FacilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	q.Limit = 500
	events, err := h.store.Query(r.Context(), q)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	verification, err := h.recorder.Verify(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	now := h.clock.Now()
	// The export is recorded before it is produced, so the trail shows the export even
	// if the download is abandoned — and the report itself cannot contain its own row.
	_, _ = h.recorder.Record(r.Context(), Entry{
		Kind: "audit.exported", FacilityID: actor.FacilityID, ActorID: &actor.UserID, ActorCode: actor.Code,
		ActorRole: actor.ActiveRole, SessionID: actor.SessionID, ClientDigest: actor.Client, At: now,
		Details: map[string]any{"count": len(events), "filter": filterOf(r)},
	})
	out := BuildExport(h.signer, events, ExportOptions{
		FacilityName: h.facilityName(actor.FacilityID), RequestedBy: actor.Code,
		Since: q.Since, Until: q.Until, Filter: filterOf(r), GeneratedAt: now, Chain: verification,
	})
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", out.Filename))
	w.Header().Set(HeaderSignature, out.Signature.Value)
	w.Header().Set(HeaderKeyID, out.Signature.KeyID)
	w.Header().Set(HeaderDigest, out.Signature.Digest)
	w.Header().Set("Access-Control-Expose-Headers", strings.Join([]string{HeaderSignature, HeaderKeyID, HeaderDigest, "Content-Disposition"}, ", "))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.PDF)
}

func filterOf(r *http.Request) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"kind", "actor", "person", "patient", "from", "to"} {
		if v := r.URL.Query().Get(k); v != "" {
			out[k] = v
		}
	}
	return out
}

// --- alerts ---

func (h *Handlers) alerts(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	alerts, err := h.store.OpenAlerts(r.Context(), actor.FacilityID, 50)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	views := make([]alertView, 0, len(alerts))
	for _, a := range alerts {
		views = append(views, viewAlert(a))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"alerts": views})
}

func (h *Handlers) acknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	alert, err := h.store.AcknowledgeAlert(r.Context(), actor, id, h.clock.Now())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// A break-glass alert acknowledged is the access acknowledged, for the record.
	if alert.Kind == "break_glass" {
		if raw, ok := alert.Reference["access_id"].(string); ok {
			if accessID, err := uuid.Parse(raw); err == nil {
				_, _ = h.breakGlass.Acknowledge(r.Context(), actor, accessID)
			}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, viewAlert(alert))
}

// --- break-glass ---

type openRequest struct {
	ScopeKind     string `json:"scope_kind"`
	ScopeRef      string `json:"scope_ref"`
	Justification string `json:"justification"`
	Hours         int    `json:"hours"`
}

func (h *Handlers) openBreakGlass(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body openRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	access, err := h.breakGlass.Open(r.Context(), actor, OpenRequest{
		ScopeKind: body.ScopeKind, ScopeRef: body.ScopeRef, Justification: body.Justification,
		Duration: time.Duration(body.Hours) * time.Hour,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, viewAccess(access))
}

func (h *Handlers) activeBreakGlass(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	list, err := h.breakGlass.Active(r.Context(), actor.FacilityID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"accesses": viewAccesses(list)})
}

func (h *Handlers) myBreakGlass(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	list, err := h.breakGlass.ForUser(r.Context(), actor.UserID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"accesses": viewAccesses(list)})
}

func viewAccesses(list []Access) []accessView {
	out := make([]accessView, 0, len(list))
	for _, a := range list {
		out = append(out, viewAccess(a))
	}
	return out
}

type reasonRequest struct {
	Reason string `json:"reason"`
}

func (h *Handlers) endBreakGlass(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	var body reasonRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	// One's own access, or anyone's with audit.read. A 404 either way for a stranger's:
	// the door's existence is not for everyone to see.
	caller, _ := httpx.CallerFrom(r.Context())
	existing, err := h.store.q.BreakGlassByID(r.Context(), id)
	if err != nil || existing.FacilityID != actor.FacilityID || (existing.UserID != actor.UserID && !hasPermission(caller, PermAuditRead)) {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	access, err := h.breakGlass.End(r.Context(), actor, id, body.Reason)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, viewAccess(access))
}

func (h *Handlers) acknowledgeBreakGlass(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	access, err := h.breakGlass.Acknowledge(r.Context(), actor, id)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, viewAccess(access))
}

func hasPermission(c httpx.Caller, perm string) bool {
	for _, p := range c.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}
