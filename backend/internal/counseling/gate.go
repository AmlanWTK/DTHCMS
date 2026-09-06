package counseling

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// The counselling gate, and the valve that keeps it usable (CP57, §5.5).
//
// # Why this gate has an override and CP54's does not
//
// The allergy gate has three honest answers, each of which takes five seconds, so an override
// would simply become the fast one. This gate asks for *seven conversations*, and the reasons
// they cannot happen are ordinary: the patient's daughter arrives with the car, the interpreter
// does not come, the insulin corner is closed. The plan says it plainly — "a rigid gate with no
// escape valve will be worked around" — and a gate people route around is worse than one with a
// recorded valve, because the routing-around is invisible.
//
// So the valve exists and everything about it is built to be seen: its own permission, its own
// event, a required reason, the missing items recorded as they stood, and a rate view for the
// person whose job is to ask why it happened eleven times today.
//
// # Where the enforcement is
//
// In a trigger on `core.queue_entry`, like CP54's. This package produces the *message* — which
// items, in which room, on which checklist — because criterion 2 asks for exactly that and a
// trigger's exception text is not a screen. The trigger is what holds for every path that does
// not come through here.

// GateStatus is what the gate says about one visit.
type GateStatus struct {
	VisitID uuid.UUID `json:"visit_id"`

	// Blocked is the answer the queue will give. False when nothing is missing *or* when an
	// override stands — and `Overridden` is what tells those two apart, because a screen that
	// drew them the same way would be telling a physician the counselling was done.
	Blocked    bool `json:"blocked"`
	Overridden bool `json:"overridden"`

	// Missing is criterion 2: exactly which items, on which checklist, in which room.
	Missing []MissingItem `json:"missing"`

	Override *Override `json:"override,omitempty"`
}

// MissingItem is one mandatory item nobody has covered.
type MissingItem struct {
	TemplateID   uuid.UUID `json:"template_id"`
	TemplateCode string    `json:"template_code"`
	// The checklist's own words, so a refusal reads as "Diabetes counselling" rather than
	// DIABETES on a phone that holds nothing else.
	TitleEN string `json:"title_en,omitempty"`
	TitleBN string `json:"title_bn,omitempty"`

	// SessionID is absent when nobody opened this checklist at all. That is the difference
	// between "go back and finish it" and "nobody has started this", which are two different
	// rooms to send the patient to — a blocked screen that could not tell them apart would send
	// half of them to the wrong one.
	SessionID string `json:"session_id,omitempty"`

	ItemCode string `json:"item_code"`
	Room     string `json:"room"`
	// The room's own names and its place in the walk, carried here so a client rendering one
	// refusal does not fetch the room catalogue to learn that INSULIN_CORNER reads "ইনসুলিন কর্নার".
	RoomEN   string `json:"room_en,omitempty"`
	RoomBN   string `json:"room_bn,omitempty"`
	RoomStep int    `json:"room_step,omitempty"`
	// The station that room belongs to, so a phone can mark the operator's own room on a
	// refusal without fetching the room catalogue for one boolean.
	RoomStation string `json:"room_station,omitempty"`
	TextEN      string `json:"text_en"`
	TextBN      string `json:"text_bn"`
}

// Override is the valve, once used.
type Override struct {
	ID          uuid.UUID `json:"id"`
	VisitID     uuid.UUID `json:"visit_id"`
	PatientID   uuid.UUID `json:"patient_id"`
	GrantedAt   time.Time `json:"granted_at"`
	GrantedBy   uuid.UUID `json:"granted_by"`
	GrantedRole string    `json:"granted_role,omitempty"`
	// The person, named. "Who decided this" is a question about a colleague, and a uuid answers
	// a different one. Joined from the staff record rather than copied at write time.
	GrantedByCode   string `json:"granted_by_code,omitempty"`
	GrantedByNameEN string `json:"granted_by_name_en,omitempty"`
	GrantedByNameBN string `json:"granted_by_name_bn,omitempty"`
	Reason          string `json:"reason"`

	// MissingAtGrant is what was outstanding when it was granted, not what is outstanding now.
	// Items covered afterwards would otherwise make the record say the override was for nothing.
	MissingAtGrant []string `json:"missing_at_grant"`
}

// GateName is what these events call this checkpoint. CP83's QA clearance is the same two
// events with a different name, which is why the name is a field rather than the event type.
const GateName = "COUNSELING"

var (
	// ErrAlreadyOverridden is a second override on the same visit. Not another act — the same
	// one — and answering plainly is more honest than stacking rows nobody reads.
	ErrAlreadyOverridden = errors.New("counseling: this visit's gate has already been overridden")

	// ErrNothingToOverride is an override on a visit the gate is not holding. Refused rather
	// than recorded: an override with no missing items is a row that makes the rate view lie.
	ErrNothingToOverride = errors.New("counseling: nothing is outstanding on this visit")

	// ErrOverrideReasonRequired is criterion 3, refused before it reaches the ledger.
	ErrOverrideReasonRequired = errors.New("counseling: an override needs a reason")
)

// Gate is what the gate says about one visit, missing items and all.
func (s *Store) Gate(ctx context.Context, visit uuid.UUID) (GateStatus, error) {
	missing, err := s.Missing(ctx, visit)
	if err != nil {
		return GateStatus{}, err
	}
	status := GateStatus{VisitID: visit, Missing: missing}

	override, found, err := s.Override(ctx, visit)
	if err != nil {
		return GateStatus{}, err
	}
	if found {
		status.Override = &override
		status.Overridden = true
	}
	status.Blocked = len(missing) > 0 && !found
	return status, nil
}

// Missing is the mandatory items nobody has covered, from the one database function the gate
// trigger, this API, the phone and the physician's panel all read.
func (s *Store) Missing(ctx context.Context, visit uuid.UUID) ([]MissingItem, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT template_id, template_code, title_en, title_bn, session_id,
		       item_code, room, room_en, room_bn, room_step, room_station, text_en, text_bn
		  FROM core.counseling_gate_missing($1)`, visit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []MissingItem{}
	for rows.Next() {
		var item MissingItem
		var session uuid.NullUUID
		if err := rows.Scan(&item.TemplateID, &item.TemplateCode, &item.TitleEN, &item.TitleBN,
			&session, &item.ItemCode, &item.Room, &item.RoomEN, &item.RoomBN, &item.RoomStep,
			&item.RoomStation, &item.TextEN, &item.TextBN); err != nil {
			return nil, err
		}
		if session.Valid {
			item.SessionID = session.UUID.String()
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Override reads the override standing on a visit, if there is one.
func (s *Store) Override(ctx context.Context, visit uuid.UUID) (Override, bool, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT o.id, o.visit_id, o.patient_id, o.granted_at, o.granted_by, o.granted_role,
		       coalesce(u.employee_code, ''), coalesce(u.name_en, ''), coalesce(u.name_bn, ''),
		       o.reason, o.missing_at_grant
		  FROM read.counseling_gate_override o
		  LEFT JOIN core.app_user u ON u.id = o.granted_by
		 WHERE o.visit_id = $1`, visit)

	var out Override
	err := row.Scan(&out.ID, &out.VisitID, &out.PatientID, &out.GrantedAt, &out.GrantedBy,
		&out.GrantedRole, &out.GrantedByCode, &out.GrantedByNameEN, &out.GrantedByNameBN,
		&out.Reason, &out.MissingAtGrant)
	switch {
	case err == nil:
		return out, true, nil
	case strings.Contains(err.Error(), "no rows"):
		return Override{}, false, nil
	default:
		return Override{}, false, err
	}
}

// Overrides is every override in a window, newest first, for the person whose job is to ask why.
//
// The plan's mitigation for "the gate causes clinic-floor friction" is the override plus
// *override-rate monitoring*, and monitoring that nobody can read is a plan on paper. The window
// is half-open — `[from, to)` — and both ends are whole days, for the reason CP54's assertion
// rates are: a window that ended "now" would exclude the override granted a minute ago, which is
// the one somebody is asking about.
func (s *Store) Overrides(ctx context.Context, facility uuid.UUID, from, to time.Time) ([]Override, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.visit_id, o.patient_id, o.granted_at, o.granted_by, o.granted_role,
		       coalesce(u.employee_code, ''), coalesce(u.name_en, ''), coalesce(u.name_bn, ''),
		       o.reason, o.missing_at_grant
		  FROM read.counseling_gate_override o
		  LEFT JOIN core.app_user u ON u.id = o.granted_by
		 WHERE o.facility_id = $1 AND o.granted_at >= $2 AND o.granted_at < $3
		 ORDER BY o.granted_at DESC`, facility, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Override{}
	for rows.Next() {
		var one Override
		if err := rows.Scan(&one.ID, &one.VisitID, &one.PatientID, &one.GrantedAt,
			&one.GrantedBy, &one.GrantedRole, &one.GrantedByCode, &one.GrantedByNameEN,
			&one.GrantedByNameBN, &one.Reason, &one.MissingAtGrant); err != nil {
			return nil, err
		}
		out = append(out, one)
	}
	return out, rows.Err()
}

// GrantOverride is somebody letting a patient past, in their own name.
//
// **Criterion 3.** The reason is required here, by the event's own validation and by a CHECK
// constraint; the permission is checked at the route; the act is audited by the handler. Four
// places, because the valve is acceptable only while it is legible, and the way a valve stops
// being legible is one of those four quietly not being there.
func (s *SessionService) GrantOverride(ctx context.Context, eventID uuid.UUID,
	visit, patient uuid.UUID, reason string, source eventstore.Source) (GateStatus, error) {

	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return GateStatus{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return GateStatus{}, ErrOverrideReasonRequired
	}

	status, err := s.store.Gate(ctx, visit)
	if err != nil {
		return GateStatus{}, err
	}
	if status.Overridden {
		return GateStatus{}, ErrAlreadyOverridden
	}
	if len(status.Missing) == 0 {
		return GateStatus{}, ErrNothingToOverride
	}

	codes := make([]string, 0, len(status.Missing))
	for _, item := range status.Missing {
		codes = append(codes, item.ItemCode)
	}

	now := s.clock.Now().UTC()
	payload := eventstore.CounselingGateOverridden{
		OverrideID: uuid.New().String(),
		FacilityID: actor.FacilityID().String(),
		PatientID:  patient.String(),
		VisitID:    visit.String(),
		Reason:     reason,
		Missing:    codes,
		GrantedAt:  now,
	}
	if err := s.append(ctx, eventID, "COUNSELING_GATE_OVERRIDDEN",
		patient, &visit, actor, source, now, payload); err != nil {
		return GateStatus{}, err
	}
	return s.store.Gate(ctx, visit)
}
