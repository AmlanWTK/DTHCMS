package clinical

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// The correction workflow (CP62, §4.3, [R-04]).
//
// # The 140/150 case
//
// §4.3 describes it concretely: an operator records a height of 150 cm; the physician is sure it
// is 140 and flags it; the request reaches the operator who typed it; they correct it; everything
// derived from it recomputes; both values stay in the record with both names against them.
//
// # Why this lives in `clinical` rather than in a module of its own
//
// The plan says "correction module", and a separate Go package was the first thing tried. It
// would have to import `clinical` for every one of its operations — reading the flagged value,
// writing the replacement, recomputing what depended on it — which is a boundary that exists on
// paper and nowhere else. The correction *is* a clinical write: the cascade in particular is the
// derivation engine run again, and putting it behind a module wall would mean either duplicating
// that engine or exporting it wholesale.
//
// # What is not here
//
// Nothing edits a value. Correcting is writing a **new** observation that replaces the old one —
// the mechanism CP42 already built, `Recording.Replaces` — and this file is the routing around
// it. The original keeps its row, stops being ACTIVE, and stays queryable, which is criterion 1.

// CorrectionReason is one of the ways a value can be wrong.
type CorrectionReason struct {
	Code      string `json:"code"`
	DisplayEN string `json:"display_en"`
	DisplayBN string `json:"display_bn"`
	// Transcription means the operator typed something other than what they read. CP63's
	// pattern detection is specifically about repeated transcription errors, and a flag on the
	// reason is what makes that a query rather than a guess at somebody's free text.
	Transcription bool `json:"transcription"`
	Ordering      int  `json:"ordering"`
}

// CorrectionRequest is one value somebody said was wrong.
type CorrectionRequest struct {
	ID        uuid.UUID  `json:"id"`
	PatientID uuid.UUID  `json:"patient_id"`
	VisitID   *uuid.UUID `json:"visit_id,omitempty"`

	ObservationID uuid.UUID `json:"observation_id"`
	Code          string    `json:"code"`

	RequestedAt   time.Time `json:"requested_at"`
	RequestedBy   uuid.UUID `json:"requested_by"`
	RequestedRole string    `json:"requested_role,omitempty"`
	// The names, joined rather than copied: a colleague who changes their name should read
	// correctly on a request they raised last year (CP61).
	RequestedByCode   string `json:"requested_by_code,omitempty"`
	RequestedByNameEN string `json:"requested_by_name_en,omitempty"`
	RequestedByNameBN string `json:"requested_by_name_bn,omitempty"`

	ReasonCode string `json:"reason_code"`
	Note       string `json:"note,omitempty"`

	// AssignedTo is whoever typed the value. §4.3 routes the request to them, because an
	// operator who never learns they mistyped will mistype again.
	AssignedTo       uuid.UUID `json:"assigned_to"`
	AssignedToCode   string    `json:"assigned_to_code,omitempty"`
	AssignedToNameEN string    `json:"assigned_to_name_en,omitempty"`
	AssignedToNameBN string    `json:"assigned_to_name_bn,omitempty"`

	Status string `json:"status"`

	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy     string     `json:"resolved_by,omitempty"`
	ResolvedRole   string     `json:"resolved_role,omitempty"`
	ResolutionNote string     `json:"resolution_note,omitempty"`
	ReplacementID  string     `json:"replacement_id,omitempty"`

	// Recomputed is what else moved because this moved (criterion 3). Reported rather than left
	// to be inferred: a physician reading "height corrected" should not have to compare two BMIs
	// to work out whether the second one followed, and an entry ending `:not-recomputed` says
	// plainly that one did not.
	Recomputed []string `json:"recomputed"`
}

// Open says whether this request is still waiting for an answer.
func (r CorrectionRequest) Open() bool { return r.Status == "OPEN" }

var (
	// ErrNoRequest is a correction request that is not there.
	ErrNoRequest = errors.New("clinical: no such correction request")

	// ErrAlreadyFlagged is a second flag on a value somebody has already flagged. The same
	// conversation, and two rows would route two corrections at one number.
	ErrAlreadyFlagged = errors.New("clinical: that value is already flagged")

	// ErrRequestClosed is an answer to a request somebody already answered.
	ErrRequestClosed = errors.New("clinical: that correction request is already answered")

	// ErrUnknownReason is a reason code that is not in the vocabulary.
	ErrUnknownReason = errors.New("clinical: no such correction reason")

	// ErrNotYours is somebody other than the value's author trying to correct it without the
	// supervisor's permission. The author corrects their own work; anybody else is an override,
	// and an override is a different event.
	ErrNotYours = errors.New("clinical: only the person who recorded the value may correct it")

	// ErrReasonRequired is a rejection with nothing said. "No" with no reason is how a flagging
	// culture dies.
	ErrReasonRequired = errors.New("clinical: say why the value stands")

	// ErrCannotFlagOwn is somebody flagging their own value. Not forbidden by the plan, and
	// refused here anyway: correcting your own value needs no request, and a flag on it would
	// put a correction on your own quality record that nobody asked you to make.
	ErrCannotFlagOwn = errors.New("clinical: correct your own value rather than flagging it")

	// ErrCannotFlagDerived is a flag on a computed value. A BMI is not typed by anybody; it is
	// what a height and a weight make. Routing a request at it would send the correction to
	// whoever happened to press "derive" — who cannot fix it, because there is nothing to
	// retype — while the wrong number it came from sits uncorrected. Flagging the input is the
	// act that works, and the cascade then moves the derived value on its own.
	ErrCannotFlagDerived = errors.New("clinical: flag the value this was computed from")
)

// Reasons is the vocabulary of why a value can be wrong.
func (s *Store) Reasons(ctx context.Context) ([]CorrectionReason, error) {
	rows, err := s.q.CorrectionReasons(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]CorrectionReason, 0, len(rows))
	for _, row := range rows {
		out = append(out, CorrectionReason{
			Code: row.Code, DisplayEN: row.DisplayEn, DisplayBN: row.DisplayBn,
			Transcription: row.IsTranscription, Ordering: int(row.Ordering),
		})
	}
	return out, nil
}

// Request reads one correction request.
func (s *Store) Request(ctx context.Context, id, facility uuid.UUID) (CorrectionRequest, error) {
	row, err := s.q.CorrectionRequestByID(ctx,
		dbgen.CorrectionRequestByIDParams{ID: id, FacilityID: facility})
	if errors.Is(err, pgx.ErrNoRows) {
		return CorrectionRequest{}, ErrNoRequest
	}
	if err != nil {
		return CorrectionRequest{}, err
	}
	return requestOf(dbgen.CorrectionRequestsForOperatorRow(row)), nil
}

// RequestsFor is what one operator is being asked to fix.
func (s *Store) RequestsFor(ctx context.Context, facility, operator uuid.UUID,
	openOnly bool, limit int) ([]CorrectionRequest, error) {

	rows, err := s.q.CorrectionRequestsForOperator(ctx, dbgen.CorrectionRequestsForOperatorParams{
		FacilityID: facility, AssignedTo: operator, Column3: openOnly,
		Limit: int32(limit), //nolint:gosec // bounded by the handler
	})
	if err != nil {
		return nil, err
	}
	out := make([]CorrectionRequest, 0, len(rows))
	for _, row := range rows {
		out = append(out, requestOf(row))
	}
	return out, nil
}

// RequestsForPatient is every flag ever raised on this patient's values.
func (s *Store) RequestsForPatient(ctx context.Context, patient, facility uuid.UUID,
	limit int) ([]CorrectionRequest, error) {

	rows, err := s.q.CorrectionRequestsForPatient(ctx, dbgen.CorrectionRequestsForPatientParams{
		PatientID: patient, FacilityID: facility,
		Limit: int32(limit), //nolint:gosec // bounded by the handler
	})
	if err != nil {
		return nil, err
	}
	out := make([]CorrectionRequest, 0, len(rows))
	for _, row := range rows {
		out = append(out, requestOf(dbgen.CorrectionRequestsForOperatorRow(row)))
	}
	return out, nil
}

func requestOf(row dbgen.CorrectionRequestsForOperatorRow) CorrectionRequest {
	out := CorrectionRequest{
		ID: row.ID, PatientID: row.PatientID,
		ObservationID: row.ObservationID, Code: row.Code,
		RequestedAt: row.RequestedAt, RequestedBy: row.RequestedBy,
		RequestedRole: row.RequestedRole, RequestedByCode: row.RequestedByCode,
		RequestedByNameEN: row.RequestedByNameEn, RequestedByNameBN: row.RequestedByNameBn,
		ReasonCode: row.ReasonCode, Note: row.Note,
		AssignedTo: row.AssignedTo, AssignedToCode: row.AssignedToCode,
		AssignedToNameEN: row.AssignedToNameEn, AssignedToNameBN: row.AssignedToNameBn,
		Status: row.Status, ResolvedAt: row.ResolvedAt,
		ResolvedRole: row.ResolvedRole, ResolutionNote: row.ResolutionNote,
		Recomputed: row.Recomputed,
	}
	if out.Recomputed == nil {
		out.Recomputed = []string{}
	}
	if row.VisitID.Valid {
		visit := row.VisitID.UUID
		out.VisitID = &visit
	}
	if row.ResolvedBy.Valid {
		out.ResolvedBy = row.ResolvedBy.UUID.String()
	}
	if row.ReplacementID.Valid {
		out.ReplacementID = row.ReplacementID.UUID.String()
	}
	return out
}

// Flagging is one value somebody says is wrong.
type Flagging struct {
	EventID       uuid.UUID
	ObservationID uuid.UUID
	ReasonCode    string
	Note          string

	LedgerSource eventstore.Source
}

// Flag raises a correction request against a value, routed to whoever typed it.
//
// The routing is the checkpoint. §4.3 sends the request to the original author because the point
// of the mechanism is that the person who made the mistake learns of it — a workflow where a
// supervisor quietly fixes everything produces a clean record and an operator who keeps mistyping.
func (s *Service) Flag(ctx context.Context, in Flagging) (CorrectionRequest, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return CorrectionRequest{}, err
	}
	in.ReasonCode = strings.ToUpper(strings.TrimSpace(in.ReasonCode))
	in.Note = strings.TrimSpace(in.Note)

	reasons, err := s.store.Reasons(ctx)
	if err != nil {
		return CorrectionRequest{}, err
	}
	if !known(reasons, in.ReasonCode) {
		return CorrectionRequest{}, fmt.Errorf("%w: %s", ErrUnknownReason, in.ReasonCode)
	}

	value, err := s.store.ByID(ctx, in.ObservationID, actor.FacilityID())
	if err != nil {
		return CorrectionRequest{}, err
	}
	if value.Status != Active {
		// Flagging a value that has already been replaced would route a correction at a number
		// nobody is looking at any more.
		return CorrectionRequest{}, ErrAlreadyReplaced
	}
	if value.Category == Derived {
		// See ErrCannotFlagDerived. The honest correction is on the input, and `Apply` then
		// recomputes this row without anybody flagging it.
		return CorrectionRequest{}, ErrCannotFlagDerived
	}
	if value.RecordedBy == actor.UserID() {
		return CorrectionRequest{}, ErrCannotFlagOwn
	}
	if _, err := s.store.q.OpenCorrectionRequestFor(ctx, in.ObservationID); err == nil {
		return CorrectionRequest{}, ErrAlreadyFlagged
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return CorrectionRequest{}, err
	}

	requestID := uuid.New()
	now := s.clock.Now().UTC()
	payload := eventstore.CorrectionRequested{
		RequestID:     requestID.String(),
		FacilityID:    actor.FacilityID().String(),
		PatientID:     value.PatientID.String(),
		ObservationID: in.ObservationID.String(),
		Code:          value.Code,
		ReasonCode:    in.ReasonCode,
		Note:          in.Note,
		AssignedTo:    value.RecordedBy.String(),
		RequestedAt:   now,
	}
	if value.VisitID != nil {
		payload.VisitID = value.VisitID.String()
	}
	if err := s.appendCorrection(ctx, in.EventID, "CORRECTION_REQUESTED",
		value.PatientID, value.VisitID, actor, in.LedgerSource, now, payload); err != nil {
		return CorrectionRequest{}, err
	}

	request, err := s.store.Request(ctx, requestID, actor.FacilityID())
	if err != nil {
		return CorrectionRequest{}, err
	}
	// The author's device is told, after the write. Criterion 4: the author is notified on
	// their device, which is what makes the request a request rather than a row in a report.
	if s.corrections != nil {
		s.corrections.CorrectionRequested(ctx, request)
	}
	return request, nil
}

// Correcting is the answer to a request: the value as it should have been.
type Correcting struct {
	EventID   uuid.UUID
	RequestID uuid.UUID

	// Value and Unit are the corrected reading, in whatever unit the operator is working in.
	Value *float64
	Unit  string
	// ValueText, ValueBool and ValueCode carry the non-numeric shapes, for the same reason
	// `Recording` does.
	ValueText string
	ValueBool *bool
	ValueCode string

	Note string

	// AsSupervisor says the caller holds the supervisor's permission. Decided by the handler,
	// which is where the route's declared permissions are known, rather than read from the
	// context here — a service that inspected permissions would be a second authorisation
	// decision beside the one the router already made.
	AsSupervisor bool

	LedgerSource eventstore.Source
}

// Apply corrects the flagged value and recomputes what depended on it.
//
// Three things happen, in one transaction, and the order is the design:
//
//  1. a **new** observation is written, replacing the flagged one (criterion 1: the original is
//     never altered — it keeps its row and stops being ACTIVE);
//  2. every live derived value computed from that code is recomputed, each as a new row
//     replacing the old one (criterion 3: derived values are versioned, not overwritten);
//  3. the request is answered — as `CORRECTION_APPLIED` if the author did it, and as
//     `SUPERVISOR_OVERRIDE_APPLIED` if somebody else did.
//
// The cascade is inside the transaction because a corrected height with a stale BMI beside it is
// worse than either an old height or a failed correction: it is two numbers that disagree, both
// looking equally official.
func (s *Service) Apply(ctx context.Context, in Correcting) (CorrectionRequest, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return CorrectionRequest{}, err
	}
	request, err := s.store.Request(ctx, in.RequestID, actor.FacilityID())
	if err != nil {
		return CorrectionRequest{}, err
	}
	if !request.Open() {
		return CorrectionRequest{}, ErrRequestClosed
	}

	// Who may answer: the person it was routed to, or somebody holding the supervisor's
	// permission. The two write different events, because an operator's quality record must not
	// read a supervisor's fix as though they had put it right themselves.
	mine := request.AssignedTo == actor.UserID()
	if !mine && !in.AsSupervisor {
		return CorrectionRequest{}, ErrNotYours
	}

	original, err := s.store.ByID(ctx, request.ObservationID, actor.FacilityID())
	if err != nil {
		return CorrectionRequest{}, err
	}

	now := s.clock.Now().UTC()
	var replacement uuid.UUID
	var recomputed []string

	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries) error {
		corrected := Recording{
			EventID: in.EventID, PatientID: original.PatientID, VisitID: original.VisitID,
			EncounterID: original.EncounterID,
			Code:        original.Code,
			Value:       in.Value, Unit: in.Unit,
			ValueText: in.ValueText, ValueBool: in.ValueBool, ValueCode: in.ValueCode,
			// The correction carries the *original's* effective time, not now: the measurement
			// happened when it happened, and re-stamping it would move a value on a trend line
			// to the moment somebody noticed the typo.
			EffectiveAt:    original.EffectiveAt,
			Source:         original.Source,
			Note:           strings.TrimSpace(in.Note),
			Replaces:       &request.ObservationID,
			ReplacedStatus: Corrected,
			LedgerSource:   in.LedgerSource,
		}
		id, _, appendErr := s.appendRecording(ctx, tx, q, actor, corrected)
		if appendErr != nil {
			return appendErr
		}
		replacement = id

		recomputed, appendErr = s.recompute(ctx, tx, q, actor, original, in.LedgerSource)
		return appendErr
	})
	if err != nil {
		return CorrectionRequest{}, err
	}

	kind := "CORRECTION_APPLIED"
	var payload any = eventstore.CorrectionApplied{
		RequestID: request.ID.String(), FacilityID: actor.FacilityID().String(),
		PatientID: original.PatientID.String(), ObservationID: request.ObservationID.String(),
		ReplacementID: replacement.String(), Note: strings.TrimSpace(in.Note),
		Recomputed: recomputed, AppliedAt: now,
	}
	if !mine {
		kind = "SUPERVISOR_OVERRIDE_APPLIED"
		payload = eventstore.SupervisorOverrideApplied{
			RequestID: request.ID.String(), FacilityID: actor.FacilityID().String(),
			PatientID: original.PatientID.String(), ObservationID: request.ObservationID.String(),
			ReplacementID: replacement.String(), AssignedTo: request.AssignedTo.String(),
			Note: strings.TrimSpace(in.Note), Recomputed: recomputed, AppliedAt: now,
		}
	}
	if err := s.appendCorrection(ctx, uuid.New(), kind,
		original.PatientID, original.VisitID, actor, in.LedgerSource, now, payload); err != nil {
		return CorrectionRequest{}, err
	}
	// CP63. The pattern detection looks at the operator whose record this lands on — the person
	// the request was routed to, not whoever answered it. A supervisor's override still belongs
	// on the operator's record; that is the whole reason it is a different event.
	s.reviewQuality(ctx, request.AssignedTo)
	return s.store.Request(ctx, request.ID, actor.FacilityID())
}

// reviewQuality asks the quality module to look at one operator, and swallows whatever it says.
//
// After the commit, and never in its way: a correction that failed because a counting query was
// slow would mean an operator cannot fix a wrong height, which is the wrong trade in every
// direction. A missed review is a flag raised on the next correction instead.
func (s *Service) reviewQuality(ctx context.Context, operator uuid.UUID) {
	if s.quality == nil || operator == uuid.Nil {
		return
	}
	s.quality.ReviewOperator(ctx, operator)
}

// Reject is the author saying the value is right as it stands.
func (s *Service) Reject(ctx context.Context, eventID, requestID uuid.UUID, reason string,
	supervisor bool, source eventstore.Source) (CorrectionRequest, error) {

	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return CorrectionRequest{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return CorrectionRequest{}, ErrReasonRequired
	}
	request, err := s.store.Request(ctx, requestID, actor.FacilityID())
	if err != nil {
		return CorrectionRequest{}, err
	}
	if !request.Open() {
		return CorrectionRequest{}, ErrRequestClosed
	}
	if request.AssignedTo != actor.UserID() && !supervisor {
		return CorrectionRequest{}, ErrNotYours
	}

	now := s.clock.Now().UTC()
	payload := eventstore.CorrectionRejected{
		RequestID: request.ID.String(), FacilityID: actor.FacilityID().String(),
		PatientID: request.PatientID.String(), ObservationID: request.ObservationID.String(),
		Reason: reason, RejectedAt: now,
	}
	if err := s.appendCorrection(ctx, eventID, "CORRECTION_REJECTED",
		request.PatientID, request.VisitID, actor, source, now, payload); err != nil {
		return CorrectionRequest{}, err
	}
	// A rejection is reviewed too, and the review counts it as nothing. Not a no-op: the shape
	// of somebody's month changes when a request closes, and asking here is what makes a
	// rejection *remove* a request from the open count rather than leaving it there until the
	// next correction happens to trigger a look.
	s.reviewQuality(ctx, request.AssignedTo)
	return s.store.Request(ctx, request.ID, actor.FacilityID())
}

// recompute re-derives every live value that was computed from the corrected code.
//
// **Criterion 3, and the reason it is a cascade rather than an update.** A BMI computed from a
// height of 150 is not "wrong"; it is the right answer to what was known at the time. So it is
// superseded by a new derivation rather than edited, and both stay in the record — the same rule
// the measurement itself follows.
//
// The dependency list comes from what each derivation *stored*: the inputs it actually read,
// keyed by code. A formula name would be a guess about what a formula reads; the inputs are the
// record of what it did read.
func (s *Service) recompute(ctx context.Context, tx pgx.Tx, q *dbgen.Queries,
	actor eventstore.Actor, original Observation, source eventstore.Source) ([]string, error) {

	recomputed := []string{}
	for _, what := range DerivationsReading(original.Code) {
		// Only what this patient actually has. A clinic that never measured a waist has no WHR
		// to move, and deriving one here would invent a value nobody asked for out of a
		// correction to something else.
		live, err := q.LiveDerivedValue(ctx, dbgen.LiveDerivedValueParams{
			PatientID: original.PatientID, FacilityID: actor.FacilityID(), Code: string(what),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		replaced := live.ID
		if _, err := s.appendDerivation(ctx, tx, q, actor, Derivation{
			EventID: uuid.New(), PatientID: original.PatientID, VisitID: original.VisitID,
			What: what, Replaces: &replaced, LedgerSource: source,
		}); err != nil {
			// A derivation that cannot be recomputed — its other input has since been removed,
			// or the formula refuses the new value — must not take the correction down with it.
			// The failure is named in the result instead, so a physician reading "height
			// corrected" is not left assuming everything downstream moved.
			recomputed = append(recomputed, string(what)+":not-recomputed")
			continue
		}
		recomputed = append(recomputed, string(what))
	}
	return recomputed, nil
}

func (s *Service) appendCorrection(ctx context.Context, eventID uuid.UUID, eventType string,
	patient uuid.UUID, visit *uuid.UUID, actor eventstore.Actor, source eventstore.Source,
	now time.Time, payload any) error {

	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if eventID == uuid.Nil {
		eventID = uuid.New()
	}
	if source == "" {
		source = eventstore.SourceWeb
	}
	envelope := eventstore.Envelope{
		EventID: eventID, AggregateType: "PATIENT", AggregateID: patient,
		PatientID: &patient, VisitID: visit,
		EventType: eventType, EventVersion: 1, OccurredAt: now,
		Actor: actor, Source: source, Payload: encoded,
	}
	return s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbgen.Queries) error {
		_, err := s.events.AppendInTx(ctx, tx, envelope)
		return err
	})
}

func known(reasons []CorrectionReason, code string) bool {
	for _, reason := range reasons {
		if reason.Code == code {
			return true
		}
	}
	return false
}
