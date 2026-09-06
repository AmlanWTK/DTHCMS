package counseling

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

// SessionService writes what happened on the floor (CP56, §5.3).
//
// Four acts, and the split is the checkpoint:
//
//   - **Start** opens a session against the version that is published *now*, and records that
//     version on the session. It never reopens a completed one and never starts a second one
//     for the same checklist and visit.
//   - **Tick** covers exactly one item. There is no method here that takes a list, and adding
//     one would end criterion 1 — twenty items covered has to be twenty acts by whoever
//     covered them, because §5.4's spot-questioning asks about one item at a time.
//   - **Untick** takes a tick back and requires a reason (criterion 3).
//   - **Complete** says the counsellor is finished. It ticks nothing: a completion that
//     covered the outstanding items would be the batch attribution criterion 1 forbids,
//     wearing a different name.
//
// The gate is not here. CP57 decides whether an incomplete session may reach the physician;
// this package's job is to record honestly what was and was not covered, and a writer that
// refused an unfinished session would make the gate unenforceable by making the evidence
// unrecordable.
type SessionService struct {
	store    *Store
	events   *eventstore.Store
	clock    interface{ Now() time.Time }
	notifier Notifier
}

func NewSessionService(store *Store, events *eventstore.Store,
	clk interface{ Now() time.Time }) *SessionService {
	return &SessionService{store: store, events: events, clock: clk}
}

// WithNotifier attaches the realtime bridge. Criterion 4 — progress on the traffic board
// within two seconds — is its whole purpose.
//
// Optional, and nil publishes nothing. A tick that succeeded and was not broadcast is a board
// a few seconds stale; a tick refused because Redis blinked is a counsellor stuck in front of a
// patient. Only one of those is worth failing a write for.
func (s *SessionService) WithNotifier(n Notifier) *SessionService {
	copied := *s
	copied.notifier = n
	return &copied
}

// Notifier carries progress to the realtime gateway.
//
// An interface because `counseling` may not import `realtime` — the same boundary `visit` has,
// and for the same reason: the gateway relays notifications and must not grow a second,
// differently-shaped copy of what a module means by "progress".
type Notifier interface {
	CounselingProgressed(ctx context.Context, p Progress)
}

// Progress is what the board and the physician's screen are told. No item text: a wall display
// in a public waiting area is not a place to put what a patient is being counselled about.
type Progress struct {
	SessionID  uuid.UUID
	FacilityID uuid.UUID
	PatientID  uuid.UUID
	VisitID    uuid.UUID

	// Kind is "counseling.ticked", "counseling.unticked", "counseling.started" or
	// "counseling.completed".
	Kind string

	// Covered and Mandatory are the progress indicator, as two numbers rather than a
	// percentage — "5 of 7" is what a counsellor says out loud, and a percentage rounds the
	// difference between finished and nearly finished away.
	Covered   int
	Mandatory int
	Complete  bool

	At time.Time
}

// Start opens a session, or returns the one already open for this checklist and visit.
//
// Idempotent by design rather than by accident: a counsellor whose phone lost the reply and
// pressed start again must land in the session they already have, not a second empty copy of
// it. The unique index says the same thing at the database, for every path that is not this
// one.
func (s *SessionService) Start(ctx context.Context, in StartSession) (Session, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Session{}, err
	}

	existing, err := s.store.SessionFor(ctx, in.VisitID, in.TemplateID)
	switch {
	case err == nil:
		return s.store.Session(ctx, existing.ID)
	case !errors.Is(err, ErrNoSession):
		return Session{}, err
	}

	// The version is resolved once, here, and written into the event. Resolving it on every
	// read would mean a checklist republished at eleven changed what the patient in front of
	// the counsellor at 10:58 was asked about.
	published, err := s.store.Published(ctx, in.TemplateID)
	if err != nil {
		// A template with nothing published is a draft in progress, and the refusal says so
		// rather than "no such template" — the counsellor is holding a phone that listed it.
		if errors.Is(err, ErrNotFound) {
			return Session{}, ErrNothingPublished
		}
		return Session{}, err
	}

	sessionID := uuid.New()
	now := s.clock.Now().UTC()
	visit := in.VisitID
	payload := eventstore.CounselingSessionStarted{
		SessionID:       sessionID.String(),
		FacilityID:      actor.FacilityID().String(),
		PatientID:       in.PatientID.String(),
		VisitID:         in.VisitID.String(),
		TemplateID:      in.TemplateID.String(),
		TemplateVersion: published.Version,
		StartedAt:       now,
	}
	if err := s.append(ctx, in.EventID, "COUNSELING_SESSION_STARTED",
		in.PatientID, &visit, actor, in.LedgerSource, now, payload); err != nil {
		return Session{}, err
	}

	session, err := s.store.Session(ctx, sessionID)
	if err != nil {
		return Session{}, err
	}
	s.announce(ctx, session, "counseling.started", now)
	return session, nil
}

// StartSession is a counsellor opening a checklist for the patient in front of them.
type StartSession struct {
	// EventID is the client's, so a phone that lost the reply and retried writes the same
	// session rather than a second one. Offline ticking (CP64–CP67) queues events with ids
	// generated on the device, and this is that mechanism used early.
	EventID uuid.UUID

	PatientID  uuid.UUID
	VisitID    uuid.UUID
	TemplateID uuid.UUID

	LedgerSource eventstore.Source
}

// TickItem is one item covered.
type TickItem struct {
	EventID   uuid.UUID
	SessionID uuid.UUID
	ItemCode  string
	Note      string

	LedgerSource eventstore.Source
}

// Tick covers one item, and only one.
//
// **This method is acceptance criterion 1.** There is no `TickAll`, no list parameter and no
// "complete the rest" flag, and that absence is the design: the attribution the physician's
// panel shows is only as good as the smallest act it records.
func (s *SessionService) Tick(ctx context.Context, in TickItem) (Session, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Session{}, err
	}
	in.ItemCode = strings.ToUpper(strings.TrimSpace(in.ItemCode))
	in.Note = strings.TrimSpace(in.Note)

	session, err := s.open(ctx, in.SessionID)
	if err != nil {
		return Session{}, err
	}
	if err := s.onTheList(ctx, session, in.ItemCode); err != nil {
		return Session{}, err
	}
	// An item that already has a live tick is refused rather than silently re-attributed. See
	// `ErrAlreadyTicked`: overwriting who covered an item is the one way this record can lose
	// the answer §5.4 exists to ask for, and it would happen on a mis-tap.
	for _, tick := range session.Ticks {
		if tick.ItemCode != in.ItemCode || !tick.Live() {
			continue
		}
		// The same request arriving twice is the same act: a phone that lost the reply, or an
		// offline queue replaying what it wrote in a room with no signal. It gets the session
		// it already produced. Anything else is a second person's press, and that is the 409.
		if in.EventID != uuid.Nil && tick.EventID == in.EventID {
			return session, nil
		}
		return Session{}, ErrAlreadyTicked
	}

	now := s.clock.Now().UTC()
	visit := session.VisitID
	payload := eventstore.CounselingItemTicked{
		SessionID:  session.ID.String(),
		FacilityID: actor.FacilityID().String(),
		PatientID:  session.PatientID.String(),
		VisitID:    session.VisitID.String(),
		ItemCode:   in.ItemCode,
		Note:       in.Note,
		TickedAt:   now,
	}
	if err := s.append(ctx, in.EventID, "COUNSELING_ITEM_TICKED",
		session.PatientID, &visit, actor, in.LedgerSource, now, payload); err != nil {
		return Session{}, err
	}

	after, err := s.store.Session(ctx, session.ID)
	if err != nil {
		return Session{}, err
	}
	s.announce(ctx, after, "counseling.ticked", now)
	return after, nil
}

// UntickItem takes a tick back, with the reason criterion 3 requires.
type UntickItem struct {
	EventID   uuid.UUID
	SessionID uuid.UUID
	ItemCode  string
	Reason    string

	LedgerSource eventstore.Source
}

// Untick takes one tick back.
//
// The reason is required here, in the event's own validation, and by a database constraint. It
// is not ceremony: an un-tick with no reason is indistinguishable from a mis-tap, and telling
// those apart is the entire value of recording the un-tick at all.
func (s *SessionService) Untick(ctx context.Context, in UntickItem) (Session, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Session{}, err
	}
	in.ItemCode = strings.ToUpper(strings.TrimSpace(in.ItemCode))
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		return Session{}, ErrReasonRequired
	}

	session, err := s.open(ctx, in.SessionID)
	if err != nil {
		return Session{}, err
	}

	tick, err := s.store.q.CounselingTick(ctx,
		dbgen.CounselingTickParams{SessionID: session.ID, ItemCode: in.ItemCode})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Session{}, ErrNotTicked
	case err != nil:
		return Session{}, err
	case tick.UndoneAt != nil:
		return Session{}, ErrNotTicked
	}

	now := s.clock.Now().UTC()
	visit := session.VisitID
	payload := eventstore.CounselingItemUnticked{
		SessionID:  session.ID.String(),
		FacilityID: actor.FacilityID().String(),
		PatientID:  session.PatientID.String(),
		VisitID:    session.VisitID.String(),
		ItemCode:   in.ItemCode,
		Reason:     in.Reason,
		UntickedAt: now,
	}
	if err := s.append(ctx, in.EventID, "COUNSELING_ITEM_UNTICKED",
		session.PatientID, &visit, actor, in.LedgerSource, now, payload); err != nil {
		return Session{}, err
	}

	after, err := s.store.Session(ctx, session.ID)
	if err != nil {
		return Session{}, err
	}
	s.announce(ctx, after, "counseling.unticked", now)
	return after, nil
}

// Complete closes a session.
//
// It ticks nothing and refuses nothing. A counsellor may finish with items outstanding — the
// patient left, the interpreter did not arrive, the insulin corner was closed — and the record
// then says exactly that. CP57's gate is what decides whether such a visit may reach the
// physician, and it can only decide it because this write did not quietly prevent it happening.
func (s *SessionService) Complete(ctx context.Context, eventID, sessionID uuid.UUID,
	source eventstore.Source) (Session, error) {

	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Session{}, err
	}
	session, err := s.open(ctx, sessionID)
	if err != nil {
		return Session{}, err
	}

	now := s.clock.Now().UTC()
	visit := session.VisitID
	payload := eventstore.CounselingSessionCompleted{
		SessionID:   session.ID.String(),
		FacilityID:  actor.FacilityID().String(),
		PatientID:   session.PatientID.String(),
		VisitID:     session.VisitID.String(),
		CompletedAt: now,
	}
	if err := s.append(ctx, eventID, "COUNSELING_SESSION_COMPLETED",
		session.PatientID, &visit, actor, source, now, payload); err != nil {
		return Session{}, err
	}

	after, err := s.store.Session(ctx, session.ID)
	if err != nil {
		return Session{}, err
	}
	s.announce(ctx, after, "counseling.completed", now)
	return after, nil
}

// open reads a session and refuses a finished one.
func (s *SessionService) open(ctx context.Context, id uuid.UUID) (Session, error) {
	session, err := s.store.Session(ctx, id)
	if err != nil {
		return Session{}, err
	}
	if session.Complete() {
		return Session{}, ErrSessionComplete
	}
	return session, nil
}

// onTheList refuses an item the session's frozen version does not contain.
//
// The database refuses it too, by trigger. This one exists so a counsellor sees a sentence
// rather than a 500 — and the trigger exists because the client that sends a stale item code is
// by definition one holding a list from before a republish.
func (s *SessionService) onTheList(ctx context.Context, session Session, code string) error {
	for _, item := range session.Items {
		if item.ItemCode == code {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrUnknownItem, code)
}

// announce tells the realtime gateway, after the write has committed.
//
// Counted here rather than in the bridge because "how much of this checklist is covered" is
// this package's fact: the bridge's job is to know about topics and permissions, and a bridge
// computing progress would be a second answer to a question the gate also asks.
func (s *SessionService) announce(ctx context.Context, session Session, kind string, at time.Time) {
	if s.notifier == nil {
		return
	}
	mandatory := 0
	for _, item := range session.Items {
		if item.Mandatory {
			mandatory++
		}
	}
	s.notifier.CounselingProgressed(ctx, Progress{
		SessionID:  session.ID,
		FacilityID: session.FacilityID,
		PatientID:  session.PatientID,
		VisitID:    session.VisitID,
		Kind:       kind,
		Covered:    mandatory - len(session.Outstanding),
		Mandatory:  mandatory,
		Complete:   session.Complete(),
		At:         at,
	})
}

func (s *SessionService) append(ctx context.Context, eventID uuid.UUID, eventType string,
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
		// Counselling is ticked on a phone (§5.3, [R-01]). A caller that names its own source
		// — the offline queue does — overrides this; a caller that names none is the app in
		// the room, online.
		source = eventstore.SourceMobileOnline
	}
	envelope := eventstore.Envelope{
		EventID: eventID,
		// The visit is the aggregate, unlike history's patient. A counselling session is an
		// episode of one visit: it starts, is walked and ends inside it, and nothing about it
		// carries forward to the next one the way a diagnosis does.
		AggregateType: "VISIT",
		AggregateID:   *visit,
		PatientID:     &patient,
		VisitID:       visit,
		EventType:     eventType,
		EventVersion:  1,
		OccurredAt:    now,
		Actor:         actor,
		Source:        source,
		Payload:       encoded,
	}
	return s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbgen.Queries) error {
		_, err := s.events.AppendInTx(ctx, tx, envelope)
		return err
	})
}
