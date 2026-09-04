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
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Critical values: the fourth band, and the only one that makes a phone ring (CP50).
//
// # Where this sits
//
//	registry (CP42)      outside it, the number cannot be stored at all
//	plausibility (CP46)  outside it, it is probably a typing error
//	reference range      outside it, it is worth a second look — the field turns amber
//	critical (here)      outside it, somebody has to act now
//
// A systolic of 210 passes the first, passes the second, fails the third and fails this one,
// and only the last of those is an emergency. Conflating the amber flag with the alarm is
// exactly how a clinic learns to ignore both, so they are separate tables, separate code and
// separate words on the screen.
//
// # Raised inside the transaction that stored the value
//
// `raise` runs inside `appendRecording`, against the same transaction. There is no window in
// which a dangerous number is in the record and no alert is coming — either both facts exist
// or neither does. Nothing is *published* from in there: publishing before a commit is
// publishing something that may roll back, and there is no un-ringing a phone.
//
// # Delivery is a different question from raising
//
// An alert is raised whatever the state of the network. Whether it reached a live screen is
// attempted after the commit, recorded as its own event, and — when it failed — turned into
// the one thing that still works in a building with no Wi-Fi: the operator is told to walk.

// CriticalRule is one threshold, as the station app and the server both see it.
type CriticalRule struct {
	ID          string   `json:"id"`
	Code        string   `json:"code"`
	Sex         string   `json:"sex,omitempty"`
	MinAgeYears *float64 `json:"min_age_years,omitempty"`
	MaxAgeYears *float64 `json:"max_age_years,omitempty"`
	// Low and High are canonical, and strict: a value *below* Low or *above* High is
	// critical. Either may be absent — oxygen saturation has a floor and no ceiling.
	Low      *float64 `json:"low,omitempty"`
	High     *float64 `json:"high,omitempty"`
	ActionEN string   `json:"action_en,omitempty"`
	ActionBN string   `json:"action_bn,omitempty"`
	// Approved says whether Dr. Nahid has signed this threshold off. Every seeded row is a
	// proposal until D-27 lands, and a screen that presented them as settled would overstate
	// what anybody has agreed to.
	Approved bool `json:"approved"`
}

// EscalationStep is one link in the chain: who is told, and how long after.
//
// A step with no role is the last one. It does not notify anybody — it tells the operator who
// entered the value to go and find somebody, because a chain whose final link is another
// notification has no end.
type EscalationStep struct {
	Step         int    `json:"step"`
	AfterSeconds int    `json:"after_seconds"`
	NotifyRole   string `json:"notify_role,omitempty"`
	NoteEN       string `json:"note_en,omitempty"`
	NoteBN       string `json:"note_bn,omitempty"`
	Approved     bool   `json:"approved"`
}

// Alert is one critical value, as everything above the database sees it.
type Alert struct {
	ID uuid.UUID `json:"id"`
	// FacilityID is not serialised: a client is only ever shown its own facility's alerts,
	// and the sweep needs it to build the actor it writes the escalation under.
	FacilityID    uuid.UUID `json:"-"`
	PatientID     uuid.UUID `json:"patient_id"`
	VisitID       string    `json:"visit_id,omitempty"`
	ObservationID uuid.UUID `json:"observation_id"`

	Code string `json:"code"`
	// The code's display name, joined from the registry rather than left to the client. An
	// alert that says "SPO2 88" makes whoever reads it look the code up; one that says
	// "Oxygen saturation" does not, and the moment it matters is not a moment for lookups.
	DisplayEN string  `json:"display_en,omitempty"`
	DisplayBN string  `json:"display_bn,omitempty"`
	Value     float64 `json:"value"`
	Unit      string  `json:"unit,omitempty"`
	Breached  string  `json:"breached"`
	Threshold float64 `json:"threshold"`
	ActionEN  string  `json:"action_en,omitempty"`
	ActionBN  string  `json:"action_bn,omitempty"`

	RaisedAt    time.Time `json:"raised_at"`
	RaisedBy    uuid.UUID `json:"raised_by"`
	RaisedRole  string    `json:"raised_role,omitempty"`
	StationCode string    `json:"station_code,omitempty"`

	Status          string     `json:"status"`
	AcknowledgedBy  *uuid.UUID `json:"acknowledged_by,omitempty"`
	AcknowledgedAt  *time.Time `json:"acknowledged_at,omitempty"`
	Acknowledgement string     `json:"acknowledgement,omitempty"`

	EscalationStep int        `json:"escalation_step"`
	EscalatedAt    *time.Time `json:"escalated_at,omitempty"`

	// Delivered says the alert reached at least one live screen. Absent until the attempt has
	// been recorded, which is a few milliseconds after the value was stored — and permanently
	// absent if the API died in between, which reads as "nobody was told", which is true.
	Delivered     bool   `json:"delivered"`
	Recipients    int    `json:"recipients"`
	DeliveryError string `json:"delivery_error,omitempty"`
}

// Escalated reports whether the chain has moved past its first step with nobody answering.
func (a Alert) Escalated() bool { return a.EscalationStep > 1 }

var (
	// ErrAlertNotFound is a 404: no such alert in this facility.
	ErrAlertNotFound = errors.New("clinical: no such alert")
	// ErrAlertClosed is an acknowledgement of an alert somebody already took. Not an error
	// worth shouting about — two clinicians reaching for the same alert is the system
	// working — but the caller is told, so the screen can say who has it.
	ErrAlertClosed = errors.New("clinical: that alert has already been acknowledged")
	// ErrAcknowledgementEmpty refuses "seen" as an acknowledgement.
	ErrAcknowledgementEmpty = errors.New("clinical: an acknowledgement says what is being done")
)

// MinAcknowledgement is how short an acknowledgement may be.
//
// Three characters, which is barely a word, and deliberately not more. A longer minimum
// invites "noted." typed to clear a dialog; what makes an acknowledgement useful is not its
// length but that somebody had to type something at the moment they took responsibility.
const MinAcknowledgement = 3

// CriticalRules is every threshold, most specific first, for a station app to evaluate
// locally — so the alarm sounds in the operator's hand at the instant they type, on a phone
// that may have no signal at all.
func (s *Store) CriticalRules(ctx context.Context) ([]CriticalRule, error) {
	rows, err := s.q.CriticalValueRules(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]CriticalRule, 0, len(rows))
	for _, row := range rows {
		out = append(out, CriticalRule{
			ID:          row.ID.String(),
			Code:        row.Code,
			Sex:         stringOf(row.Sex),
			MinAgeYears: optionalNumeric(row.MinAgeYears),
			MaxAgeYears: optionalNumeric(row.MaxAgeYears),
			Low:         optionalNumeric(row.Low),
			High:        optionalNumeric(row.High),
			ActionEN:    row.ActionEn,
			ActionBN:    row.ActionBn,
			Approved:    row.ApprovedAt != nil,
		})
	}
	return out, nil
}

// EscalationChain is the chain in order, so a screen can say what happens next and when.
func (s *Store) EscalationChain(ctx context.Context) ([]EscalationStep, error) {
	rows, err := s.q.EscalationChain(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]EscalationStep, 0, len(rows))
	for _, row := range rows {
		out = append(out, EscalationStep{
			Step:         int(row.StepNumber),
			AfterSeconds: int(row.AfterSeconds),
			NotifyRole:   stringOf(row.NotifyRole),
			NoteEN:       row.NoteEn,
			NoteBN:       row.NoteBn,
			Approved:     row.ApprovedAt != nil,
		})
	}
	return out, nil
}

// raise evaluates the critical thresholds and, if one is breached, appends the alert.
//
// Runs inside `appendRecording`'s transaction, immediately after the observation event, and
// returns the alert so the write path can hand it back to the operator whose phone must now
// make a noise. A nil return is the ordinary case: most values are not critical.
//
// # Why the event id is derived rather than random
//
// The observation event id is the client's idempotency key. If a tablet loses the reply and
// saves again, the ledger returns the original observation event — and the alert's id, being
// derived from it, collides too, so the retry produces no second alert. A random id here
// would ring the consultant's phone once per lost reply.
func (s *Service) raise(ctx context.Context, tx pgx.Tx, q *dbgen.Queries, actor eventstore.Actor,
	in Recording, spec Code, canonical float64, facts patientFacts,
	observationID uuid.UUID) (*Alert, error) {

	if in.Value == nil {
		return nil, nil
	}
	rule, found, err := s.criticalRuleForTx(ctx, q, spec.Code, string(facts.sex), facts.ageYears)
	if err != nil || !found {
		return nil, err
	}

	low, high := optionalNumeric(rule.Low), optionalNumeric(rule.High)
	breached, threshold := "", 0.0
	switch {
	case low != nil && canonical < *low:
		breached, threshold = "low", *low
	case high != nil && canonical > *high:
		breached, threshold = "high", *high
	default:
		return nil, nil
	}

	alertID := uuid.New()
	now := s.clock.Now().UTC()
	payload := eventstore.CriticalValueAlerted{
		AlertID:       alertID.String(),
		FacilityID:    actor.FacilityID().String(),
		PatientID:     in.PatientID.String(),
		ObservationID: observationID.String(),
		Code:          spec.Code,
		ValueNum:      canonical,
		Unit:          spec.CanonicalUnit,
		RuleID:        rule.ID.String(),
		Breached:      breached,
		Threshold:     threshold,
		ActionEN:      rule.ActionEn,
		ActionBN:      rule.ActionBn,
		RaisedAt:      now,
	}
	if in.VisitID != nil {
		payload.VisitID = in.VisitID.String()
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	patient := in.PatientID
	envelope := eventstore.Envelope{
		EventID:       alertEventID(in.EventID),
		AggregateType: "PATIENT", AggregateID: in.PatientID, PatientID: &patient,
		EventType: "CRITICAL_VALUE_ALERTED", EventVersion: 1,
		OccurredAt: now, Actor: actor, Source: in.LedgerSource, Payload: encoded,
	}
	if in.VisitID != nil {
		visit := *in.VisitID
		envelope.VisitID = &visit
	}
	appended, err := s.events.AppendInTx(ctx, tx, envelope)
	if err != nil {
		return nil, err
	}
	if appended.Duplicate {
		// The retry case. The consultant was told the first time; do not tell them again.
		var earlier eventstore.CriticalValueAlerted
		if err := json.Unmarshal(appended.Payload, &earlier); err != nil {
			return nil, err
		}
		id, err := uuid.Parse(earlier.AlertID)
		if err != nil {
			return nil, err
		}
		alertID = id
	}

	return &Alert{
		ID: alertID, FacilityID: actor.FacilityID(),
		PatientID: in.PatientID, VisitID: payload.VisitID,
		ObservationID: observationID,
		Code:          spec.Code, Value: canonical, Unit: spec.CanonicalUnit,
		Breached: breached, Threshold: threshold,
		ActionEN: rule.ActionEn, ActionBN: rule.ActionBn,
		RaisedAt: now, RaisedBy: actor.UserID(), RaisedRole: actor.Role(),
		StationCode: actor.Station(), Status: "OPEN", EscalationStep: 1,
	}, nil
}

// alertNamespace derives an alert's event id from the observation event that raised it, so a
// retried write cannot raise the alert twice.
var alertNamespace = uuid.MustParse("2f6b19d4-8a07-4c53-9f2e-6b31d0a5c7e8")

func alertEventID(observationEvent uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(alertNamespace, []byte(observationEvent.String()+":critical"))
}

// criticalRuleForTx resolves the rule the database would resolve, on the transaction that is
// about to store the value. Never ranked in Go: a second implementation of the specificity
// ordering is a second answer to "which rule fired".
func (s *Store) criticalRuleForTx(ctx context.Context, q *dbgen.Queries,
	code, sex string, ageYears float64) (dbgen.CoreCriticalValueRule, bool, error) {

	row, err := q.CriticalValueRuleFor(ctx, dbgen.CriticalValueRuleForParams{
		PCode: code, PSex: sex, PAgeYears: numericOf(ageYears),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.CoreCriticalValueRule{}, false, nil
	}
	if err != nil {
		return dbgen.CoreCriticalValueRule{}, false, err
	}
	return row, true, nil
}

func (s *Service) criticalRuleForTx(ctx context.Context, q *dbgen.Queries,
	code, sex string, ageYears float64) (dbgen.CoreCriticalValueRule, bool, error) {
	return s.store.criticalRuleForTx(ctx, q, code, sex, ageYears)
}

// OpenAlerts is the consultant's priority surface: everything unacknowledged, newest first.
func (s *Store) OpenAlerts(ctx context.Context, facility uuid.UUID, limit int32) ([]Alert, error) {
	rows, err := s.q.OpenAlerts(ctx, dbgen.OpenAlertsParams{FacilityID: facility, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]Alert, 0, len(rows))
	for _, row := range rows {
		out = append(out, alertFrom(row.ReadCriticalAlert, row.DisplayEn, row.DisplayBn))
	}
	return out, nil
}

// AlertsForPatient is one patient's alert history, acknowledged ones included.
func (s *Store) AlertsForPatient(ctx context.Context, patientID, facility uuid.UUID, limit int32) ([]Alert, error) {
	rows, err := s.q.AlertsForPatient(ctx, dbgen.AlertsForPatientParams{
		PatientID: patientID, FacilityID: facility, Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Alert, 0, len(rows))
	for _, row := range rows {
		out = append(out, alertFrom(row.ReadCriticalAlert, row.DisplayEn, row.DisplayBn))
	}
	return out, nil
}

// AlertByID reads one alert in this facility.
func (s *Store) AlertByID(ctx context.Context, id, facility uuid.UUID) (Alert, error) {
	row, err := s.q.AlertByID(ctx, dbgen.AlertByIDParams{ID: id, FacilityID: facility})
	if errors.Is(err, pgx.ErrNoRows) {
		return Alert{}, ErrAlertNotFound
	}
	if err != nil {
		return Alert{}, err
	}
	return alertFrom(row.ReadCriticalAlert, row.DisplayEn, row.DisplayBn), nil
}

// AlertsForObservation is every alert one recorded value raised. Normally one; more when a
// value was entered, corrected and entered again.
func (s *Store) AlertsForObservation(ctx context.Context, observationID, facility uuid.UUID) ([]Alert, error) {
	rows, err := s.q.AlertsForObservation(ctx, dbgen.AlertsForObservationParams{
		ObservationID: observationID, FacilityID: facility,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Alert, 0, len(rows))
	for _, row := range rows {
		out = append(out, alertFrom(row.ReadCriticalAlert, row.DisplayEn, row.DisplayBn))
	}
	return out, nil
}

func alertFrom(row dbgen.ReadCriticalAlert, displayEN, displayBN string) Alert {
	alert := Alert{
		ID: row.ID, FacilityID: row.FacilityID, PatientID: row.PatientID,
		ObservationID: row.ObservationID,
		Code:          row.Code, Value: floatOrZero(row.ValueNum), Unit: stringOf(row.Unit),
		Breached: row.Breached, Threshold: floatOrZero(row.Threshold),
		ActionEN: row.ActionEn, ActionBN: row.ActionBn,
		RaisedAt: row.RaisedAt, RaisedBy: row.RaisedBy, RaisedRole: row.RaisedRole,
		StationCode: row.StationCode, Status: row.Status,
		Acknowledgement: row.Acknowledgement,
		EscalationStep:  int(row.EscalationStep), EscalatedAt: row.EscalatedAt,
		Recipients:    int(row.Recipients),
		DeliveryError: row.DeliveryError,
		Delivered:     row.Recipients > 0 && row.DeliveryError == "",
	}
	if row.VisitID.Valid {
		alert.VisitID = row.VisitID.UUID.String()
	}
	if row.AcknowledgedBy.Valid {
		by := row.AcknowledgedBy.UUID
		alert.AcknowledgedBy = &by
	}
	alert.AcknowledgedAt = row.AcknowledgedAt
	return alert
}

// Acknowledge is a clinician saying they have it, which is what stops the escalation.
//
// The note is required. "Seen" is not an acknowledgement; "giving oral glucose, rechecking in
// 15" is — and the next person to open the record needs the second one, at the moment when
// nobody has time to write it down twice.
func (s *Service) Acknowledge(ctx context.Context, alertID uuid.UUID, note string) (Alert, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Alert{}, err
	}
	note = strings.TrimSpace(note)
	if len(note) < MinAcknowledgement {
		return Alert{}, ErrAcknowledgementEmpty
	}

	existing, err := s.store.AlertByID(ctx, alertID, actor.FacilityID())
	if err != nil {
		return Alert{}, err
	}
	if existing.Status != "OPEN" {
		return existing, ErrAlertClosed
	}

	now := s.clock.Now().UTC()
	payload := eventstore.CriticalValueAcknowledged{
		AlertID:        alertID.String(),
		FacilityID:     actor.FacilityID().String(),
		PatientID:      existing.PatientID.String(),
		AcknowledgedBy: actor.UserID().String(),
		AcknowledgedAt: now,
		Note:           note,
	}
	if err := s.appendAlertEvent(ctx, actor, existing, "CRITICAL_VALUE_ACKNOWLEDGED",
		uuid.New(), now, payload); err != nil {
		return Alert{}, err
	}
	return s.store.AlertByID(ctx, alertID, actor.FacilityID())
}

// RecordDelivery appends what happened when the alert was pushed (criterion 4).
//
// Called after the transaction that raised the alert has committed, by whichever process
// attempted the delivery. `recipients` is how many live screens it reached; `cause` is the
// publish failure, if there was one. Zero and nil together is the honest and worrying case:
// the message went out and nobody was listening.
func (s *Service) RecordDelivery(ctx context.Context, alert Alert, recipients int, cause error) error {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return err
	}
	now := s.clock.Now().UTC()
	payload := eventstore.CriticalValueDeliveryAttempted{
		AlertID:     alert.ID.String(),
		FacilityID:  actor.FacilityID().String(),
		PatientID:   alert.PatientID.String(),
		Recipients:  recipients,
		AttemptedAt: now,
	}
	if cause != nil {
		payload.Error = cause.Error()
	}
	// Derived from the alert, so a retried delivery attempt cannot write a second record of
	// the first one.
	return s.appendAlertEvent(ctx, actor, alert, "CRITICAL_VALUE_DELIVERY_ATTEMPTED",
		uuid.NewSHA1(alertNamespace, []byte(alert.ID.String()+":delivery")), now, payload)
}

// Escalate advances one alert to the next step of the chain.
//
// The actor is passed in rather than read from the request context, because there is no
// request: a sweep running at three in the afternoon with nobody at a keyboard is what this
// is for. The worker supplies `eventstore.ActorForService`, which attributes the event to the
// clinic's scheduler by name rather than to whoever happened to be logged in.
func (s *Service) Escalate(ctx context.Context, actor eventstore.Actor, alert Alert,
	step int, notifyRole string) error {

	now := s.clock.Now().UTC()
	payload := eventstore.CriticalValueEscalated{
		AlertID:     alert.ID.String(),
		FacilityID:  actor.FacilityID().String(),
		PatientID:   alert.PatientID.String(),
		Step:        step,
		NotifyRole:  notifyRole,
		EscalatedAt: now,
	}
	// One event per alert per step. A sweep that ran twice, or two workers sweeping at once,
	// must not escalate the same alert to the same step twice — the ledger's duplicate
	// handling makes that a no-op rather than a second phone call.
	eventID := uuid.NewSHA1(alertNamespace, []byte(fmt.Sprintf("%s:escalate:%d", alert.ID, step)))
	return s.appendAlertEvent(ctx, actor, alert, "CRITICAL_VALUE_ESCALATED", eventID, now, payload)
}

// appendAlertEvent writes one of the three follow-on facts about an alert.
//
// The ledger source is SYSTEM for all of them, and that is accurate rather than lazy: an
// acknowledgement arrives over the web, but what it *is* is a decision recorded by this
// server about its own alert, not a clinical observation carried in from a station. The
// distinction matters to anyone reading §7.2's source column to ask where a fact came from.
func (s *Service) appendAlertEvent(ctx context.Context, actor eventstore.Actor, alert Alert,
	eventType string, eventID uuid.UUID, now time.Time, payload eventstore.Payload) error {

	const source = eventstore.SourceSystem
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	patient := alert.PatientID
	envelope := eventstore.Envelope{
		EventID:       eventID,
		AggregateType: "PATIENT", AggregateID: alert.PatientID, PatientID: &patient,
		EventType: eventType, EventVersion: 1,
		OccurredAt: now, Actor: actor, Source: source, Payload: encoded,
	}
	if alert.VisitID != "" {
		if visit, parseErr := uuid.Parse(alert.VisitID); parseErr == nil {
			envelope.VisitID = &visit
		}
	}
	return s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbgen.Queries) error {
		_, appendErr := s.events.AppendInTx(ctx, tx, envelope)
		return appendErr
	})
}

// floatOrZero reads a NOT NULL numeric column. The column cannot be null, so the only way
// this returns zero is a value that really is zero — which for a threshold is meaningful and
// for a measurement is a reading somebody should be looking at anyway.
func floatOrZero(n pgtype.Numeric) float64 {
	value, ok := numericValue(n)
	if !ok {
		return 0
	}
	return value
}

// Due is one alert that has waited longer than its next step allows, with that step attached.
type Due struct {
	Alert      Alert
	NextStep   int
	NotifyRole string
	NoteEN     string
	NoteBN     string
}

// DueForEscalation is the sweep: open alerts whose current step has stood longer than the
// next step's window allows.
//
// The arithmetic is the database's, deliberately. Doing it in Go would mean the same
// subtraction in a second place, against a clock that is not the one the alerts were
// timestamped by — and an escalation window that is wrong by a minute in the wrong direction
// is either an alarm nobody asked for or one that never comes.
func (s *Store) DueForEscalation(ctx context.Context, now time.Time, max int32) ([]Due, error) {
	rows, err := s.q.AlertsDueForEscalation(ctx, dbgen.AlertsDueForEscalationParams{
		Now: now, MaxAlerts: max,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Due, 0, len(rows))
	for _, row := range rows {
		out = append(out, Due{
			Alert:      alertFrom(row.ReadCriticalAlert, row.DisplayEn, row.DisplayBn),
			NextStep:   int(row.NextStep),
			NotifyRole: stringOf(row.NextRole),
			NoteEN:     row.NextNoteEn,
			NoteBN:     row.NextNoteBn,
		})
	}
	return out, nil
}
