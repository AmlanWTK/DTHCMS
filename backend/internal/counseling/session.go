package counseling

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Sessions: one walk through one checklist, on the floor (CP56, §5.3).
//
// # Why a session is a row and a tick is a row
//
// Criterion 1 is that every tick carries its own attribution and timestamp. That is not a
// reporting requirement — §5.4's method is the physician asking the patient "what were you told
// about injection sites?" and then looking at who told them. A session that stored its progress
// as a list of covered codes would answer "the counselling was done by X", which is the wrong
// question when two counsellors and an insulin corner were involved.
//
// # Why the outstanding list comes from the database
//
// `core.counseling_outstanding(uuid)` is read here, by CP57's gate, and by the physician's
// panel. Three implementations of "what is still missing" is how a phone shows a green tick
// while a gate refuses the patient standing in front of it.

// Session is one checklist being walked for one visit.
type Session struct {
	ID         uuid.UUID `json:"id"`
	FacilityID uuid.UUID `json:"facility_id"`
	PatientID  uuid.UUID `json:"patient_id"`
	VisitID    uuid.UUID `json:"visit_id"`

	// TemplateID and TemplateVersion together name the frozen version this session is walking.
	// CP55's criterion 2 lives in this pair: a checklist republished mid-session does not
	// change what this patient was asked about.
	TemplateID      uuid.UUID `json:"template_id"`
	TemplateVersion int       `json:"template_version"`

	TemplateCode string `json:"template_code,omitempty"`
	TitleEN      string `json:"title_en,omitempty"`
	TitleBN      string `json:"title_bn,omitempty"`

	StartedAt   time.Time `json:"started_at"`
	StartedBy   uuid.UUID `json:"started_by"`
	StartedRole string    `json:"started_role,omitempty"`
	// The person, named. A physician asking "who told you about injection sites" is asking about
	// a colleague, and a bare uuid answers a different question. Joined rather than copied at
	// write time, so somebody who changes their name reads correctly on last year's work.
	StartedByCode   string `json:"started_by_code,omitempty"`
	StartedByNameEN string `json:"started_by_name_en,omitempty"`
	StartedByNameBN string `json:"started_by_name_bn,omitempty"`

	CompletedAt       *time.Time `json:"completed_at,omitempty"`
	CompletedBy       string     `json:"completed_by,omitempty"`
	CompletedByCode   string     `json:"completed_by_code,omitempty"`
	CompletedByNameEN string     `json:"completed_by_name_en,omitempty"`
	CompletedByNameBN string     `json:"completed_by_name_bn,omitempty"`

	// ApprovedAt is null while the checklist's content is a proposal (D-53). Carried on the
	// session so a panel can say so without a second read of the version it is walking.
	ApprovedAt *time.Time `json:"approved_at,omitempty"`

	// Items is the frozen list, when the caller asked for the whole session.
	Items []Item `json:"items,omitempty"`
	Ticks []Tick `json:"ticks,omitempty"`
	// Outstanding is the mandatory items with no live tick — the same list CP57's gate reads.
	//
	// Always serialised, empty list and all. `omitempty` here would make "everything is
	// covered" and "you are looking at an index row that was not asked" the same absence, and a
	// screen cannot tell those apart without guessing.
	Outstanding []string `json:"outstanding"`
}

// Complete says whether a person has closed this session. Not "everything is ticked": a session
// is finished when the counsellor says so, and whether the mandatory items are covered is a
// different question with a different answer.
func (s Session) Complete() bool { return s.CompletedAt != nil }

// Tick is one item covered, by one person, at one time.
type Tick struct {
	ItemCode string `json:"item_code"`

	TickedAt   time.Time `json:"ticked_at"`
	TickedBy   uuid.UUID `json:"ticked_by"`
	TickedRole string    `json:"ticked_role,omitempty"`
	// **This is what makes §5.4 workable.** The role tells a counsellor from a nutritionist; it
	// does not tell two counsellors apart, and "who taught you this" is a question about a
	// person. The name is joined from the staff record rather than copied onto the tick.
	// CP61. Which phone, and which room's queue it was standing in.
	DeviceID    string `json:"device_id,omitempty"`
	StationCode string `json:"station_code,omitempty"`

	TickedByCode   string `json:"ticked_by_code,omitempty"`
	TickedByNameEN string `json:"ticked_by_name_en,omitempty"`
	TickedByNameBN string `json:"ticked_by_name_bn,omitempty"`

	// Note is §5.3's optional per-item note: what this counsellor wants the physician to know
	// about this item for this patient.
	Note string `json:"note,omitempty"`

	// A tick taken back keeps its row. The withdrawal is the interesting record — somebody
	// covered an item and then somebody decided they had not.
	UndoneAt       *time.Time `json:"undone_at,omitempty"`
	UndoneBy       string     `json:"undone_by,omitempty"`
	UndoneByCode   string     `json:"undone_by_code,omitempty"`
	UndoneByNameEN string     `json:"undone_by_name_en,omitempty"`
	UndoneByNameBN string     `json:"undone_by_name_bn,omitempty"`
	UndoneReason   string     `json:"undone_reason,omitempty"`
	UndoCount      int        `json:"undo_count"`

	// EventID is the ledger event that wrote this tick. Not serialised — a client has no use
	// for it — but read here so a replayed request can be recognised as the tick it already
	// made rather than refused as a second one. The offline queue (CP64–CP67) replays exactly
	// this way, and a phone that came back into signal must not be told it is wrong.
	EventID uuid.UUID `json:"-"`
}

// Live says whether this tick counts. A withdrawn one is history, not progress.
func (t Tick) Live() bool { return t.UndoneAt == nil }

var (
	// ErrNoSession is a tick against a session that is not there.
	ErrNoSession = errors.New("counseling: no such counselling session")

	// ErrSessionComplete is a write to a session somebody already closed. Not silently
	// reopened: a session closed at 11:04 and ticked at 11:40 is either a mistake or a
	// different encounter, and both deserve a person's decision rather than a merge.
	ErrSessionComplete = errors.New("counseling: that session is finished")

	// ErrUnknownItem is a tick against an item the session's version does not contain — a
	// client holding a list from before a republish, which the frozen version exists to
	// prevent mattering.
	ErrUnknownItem = errors.New("counseling: that item is not on this session's checklist")

	// ErrNotTicked is an un-tick of something nobody ticked.
	ErrNotTicked = errors.New("counseling: that item is not ticked")

	// ErrAlreadyTicked is a tick on an item that already has a live one.
	//
	// Refused rather than accepted, because the alternative is silent re-attribution: the
	// second tick would overwrite who covered the item and when, and §5.4's question — "who
	// told you about injection sites?" — would come back with the name of whoever pressed last.
	// Taking a tick back is an act with a reason (criterion 3); replacing one quietly is not an
	// act at all. A mis-tap on an item somebody else covered is therefore an un-tick with a
	// reason, then a tick.
	ErrAlreadyTicked = errors.New("counseling: that item is already ticked")

	// ErrReasonRequired is criterion 3, refused before it reaches the ledger.
	ErrReasonRequired = errors.New("counseling: un-ticking an item needs a reason")

	// ErrNothingPublished is a session start against a template with no published version. A
	// draft is not something to hand a counsellor.
	ErrNothingPublished = errors.New("counseling: that checklist has no published version")
)

// Session reads one session with its frozen items, its ticks and what is outstanding.
func (s *Store) Session(ctx context.Context, id uuid.UUID) (Session, error) {
	row, err := s.q.StartedCounselingSession(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrNoSession
		}
		return Session{}, err
	}
	session := sessionFrom(row)

	items, err := s.Items(ctx, session.TemplateID, session.TemplateVersion)
	if err != nil {
		return Session{}, err
	}
	session.Items = items

	ticks, err := s.Ticks(ctx, id)
	if err != nil {
		return Session{}, err
	}
	session.Ticks = ticks

	outstanding, err := s.Outstanding(ctx, id)
	if err != nil {
		return Session{}, err
	}
	session.Outstanding = outstanding
	return session, nil
}

// SessionFor finds the session already open for this visit and checklist, if there is one.
func (s *Store) SessionFor(ctx context.Context, visit, template uuid.UUID) (Session, error) {
	row, err := s.q.CounselingSessionForVisitTemplate(ctx,
		dbgen.CounselingSessionForVisitTemplateParams{VisitID: visit, TemplateID: template})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrNoSession
		}
		return Session{}, err
	}
	session := Session{
		ID: row.ID, FacilityID: row.FacilityID, PatientID: row.PatientID, VisitID: row.VisitID,
		TemplateID: row.TemplateID, TemplateVersion: int(row.TemplateVersion),
		StartedAt: row.StartedAt, StartedBy: row.StartedBy, StartedRole: row.StartedRole,
		CompletedAt: row.CompletedAt, CompletedBy: uuidText(row.CompletedBy),
	}
	outstanding, err := s.Outstanding(ctx, session.ID)
	if err != nil {
		return Session{}, err
	}
	session.Outstanding = outstanding
	return session, nil
}

// SessionsForVisit is every checklist this visit has been walked through.
func (s *Store) SessionsForVisit(ctx context.Context, visit uuid.UUID) ([]Session, error) {
	rows, err := s.q.CounselingSessionsForVisit(ctx, visit)
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(rows))
	for _, row := range rows {
		session := Session{
			ID: row.ID, FacilityID: row.FacilityID, PatientID: row.PatientID, VisitID: row.VisitID,
			TemplateID: row.TemplateID, TemplateVersion: int(row.TemplateVersion),
			TemplateCode: row.TemplateCode, TitleEN: row.TitleEn, TitleBN: row.TitleBn,
			ApprovedAt: row.ApprovedAt,
			StartedAt:  row.StartedAt, StartedBy: row.StartedBy, StartedRole: row.StartedRole,
			StartedByCode:   row.StartedByCode,
			StartedByNameEN: row.StartedByNameEn, StartedByNameBN: row.StartedByNameBn,
			CompletedAt: row.CompletedAt, CompletedBy: uuidText(row.CompletedBy),
			CompletedByCode:   row.CompletedByCode,
			CompletedByNameEN: row.CompletedByNameEn, CompletedByNameBN: row.CompletedByNameBn,
		}
		// The index carries what is outstanding too. One small query per session — a visit has
		// two or three — and it buys the thing that was otherwise ambiguous: with the field
		// filled everywhere, an empty list always means "nothing is missing" rather than
		// sometimes meaning "this row was not asked". The physician's panel reads this list.
		outstanding, err := s.Outstanding(ctx, session.ID)
		if err != nil {
			return nil, err
		}
		session.Outstanding = outstanding
		out = append(out, session)
	}
	return out, nil
}

// Ticks is every tick on a session, withdrawn ones included.
func (s *Store) Ticks(ctx context.Context, session uuid.UUID) ([]Tick, error) {
	rows, err := s.q.CounselingTicks(ctx, session)
	if err != nil {
		return nil, err
	}
	out := make([]Tick, 0, len(rows))
	for _, row := range rows {
		out = append(out, Tick{
			ItemCode: row.ItemCode,
			TickedAt: row.TickedAt, TickedBy: row.TickedBy, TickedRole: row.TickedRole,
			TickedByCode:   row.TickedByCode,
			TickedByNameEN: row.TickedByNameEn, TickedByNameBN: row.TickedByNameBn,
			Note:           row.Note,
			UndoneAt:       row.UndoneAt,
			UndoneBy:       uuidText(row.UndoneBy),
			UndoneByCode:   row.UndoneByCode,
			UndoneByNameEN: row.UndoneByNameEn, UndoneByNameBN: row.UndoneByNameBn,
			UndoneReason: row.UndoneReason,
			UndoCount:    int(row.UndoCount),
			EventID:      row.EventID,
		})
	}
	return out, nil
}

// Outstanding is the mandatory items with no live tick, in the order they are walked.
//
// Read from `core.counseling_outstanding(uuid)` with pgx rather than through sqlc, which reads
// a set-returning function as one opaque composite column. The function is the single
// definition CP57's gate also reads — a second copy of this rule in Go is how a gate and a
// screen come to disagree.
func (s *Store) Outstanding(ctx context.Context, session uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT item_code FROM core.counseling_outstanding($1)`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		out = append(out, code)
	}
	return out, rows.Err()
}

func sessionFrom(row dbgen.StartedCounselingSessionRow) Session {
	return Session{
		ID: row.ID, FacilityID: row.FacilityID, PatientID: row.PatientID, VisitID: row.VisitID,
		TemplateID: row.TemplateID, TemplateVersion: int(row.TemplateVersion),
		TemplateCode: row.TemplateCode, TitleEN: row.TitleEn, TitleBN: row.TitleBn,
		ApprovedAt: row.ApprovedAt,
		StartedAt:  row.StartedAt, StartedBy: row.StartedBy, StartedRole: row.StartedRole,
		StartedByCode:   row.StartedByCode,
		StartedByNameEN: row.StartedByNameEn, StartedByNameBN: row.StartedByNameBn,
		CompletedAt: row.CompletedAt, CompletedBy: uuidText(row.CompletedBy),
		CompletedByCode:   row.CompletedByCode,
		CompletedByNameEN: row.CompletedByNameEn, CompletedByNameBN: row.CompletedByNameBn,
	}
}

func uuidText(id uuid.NullUUID) string {
	if !id.Valid {
		return ""
	}
	return id.UUID.String()
}
