package offline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Service accepts batches and serves pulls.
type Service struct {
	store  *Store
	events *eventstore.Store
	clock  interface{ Now() time.Time }
	// pullable maps an event type to the permission required to receive it on a device.
	pullable map[string]string
}

// NewService builds one.
func NewService(store *Store, events *eventstore.Store, clk interface{ Now() time.Time }) *Service {
	return &Service{store: store, events: events, clock: clk, pullable: Pullable()}
}

// Push processes one batch.
//
// # The shape of this function is the checkpoint
//
// Every acceptance criterion except the two the event store already provides is a line here, so
// the order is deliberate:
//
//  1. **The receipt is checked first.** A batch that was already processed is answered from its
//     stored results without touching the ledger. That is the case where the response was lost on
//     the way back, and re-running fifty appends to reach the same answer would be wasteful and,
//     worse, would let a device that has since been revoked change the outcome of a batch that was
//     already accepted.
//  2. **The device's status is read once**, not per event. A device is not revoked halfway through
//     fifty blood pressures, and reading it fifty times would only make the answer inconsistent.
//  3. **Events are applied in the client's order, grouped by aggregate**, so per-aggregate ordering
//     is preserved (criterion 4) while a failure on one patient cannot touch another (criterion 1).
func (s *Service) Push(ctx context.Context, in Batch) (Receipt, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Receipt{}, err
	}
	if len(in.Events) == 0 {
		return Receipt{}, ErrEmptyBatch
	}
	if len(in.Events) > MaxBatch {
		// Refused rather than truncated. A client told "accepted" for a prefix and left to work
		// out which is a client that drops the rest.
		return Receipt{}, fmt.Errorf("%w: %s, and the limit is %d",
			ErrBatchTooLarge, plural(len(in.Events), "event", "events"), MaxBatch)
	}

	now := s.clock.Now().UTC()
	reopened := false

	// Already done? Answer from the record — but only if it is **closed**.
	//
	// The batch row is opened before the first event, so a server interrupted halfway leaves a
	// row that exists and reports nothing. Answering from it would tell a client "your fifty
	// events produced no results" for ever, and `docs/sync.md` promises the opposite: ask for the
	// receipt and be told, per event. An open row is therefore reprocessed rather than replayed —
	// which is safe precisely because `event_id` is the ledger's key, so everything that did land
	// comes back DUPLICATE and nothing lands twice.
	if existing, found, err := s.store.Receipt(ctx, in.BatchID); err != nil {
		return Receipt{}, err
	} else if found && existing.Closed {
		existing.ServerTime = now
		return existing, nil
	} else if found {
		// Reprocessing an interrupted batch: the results of the first attempt stay, and
		// `RecordResult` is ON CONFLICT DO NOTHING, so an event that already has an answer keeps
		// the one it was given.
		reopened = true
	}

	// **The verified device, not the one in the body.** `actor.DeviceID()` comes from the
	// signature the middleware checked; `in.DeviceID` is a field a client wrote. They agree for
	// an honest client, and taking the body's would let anyone holding a station write permission
	// attribute a batch — and its quarantine rows, and another tablet's sync state — to a device
	// that did not send it. Ledger attribution was never at risk (the actor is unforgeable), but
	// device attribution was, and the quarantine is read by somebody deciding whether to trust a
	// particular tablet.
	//
	// A body that names a different device is refused rather than quietly overridden: it means
	// the client is confused about which device it is, and carrying on would record something
	// neither of us meant.
	if in.DeviceID != uuid.Nil && in.DeviceID != actor.DeviceID() {
		return Receipt{}, ErrDeviceMismatch
	}
	device, err := s.store.DeviceFor(ctx, actor.DeviceID(), actor.FacilityID())
	if err != nil {
		return Receipt{}, err
	}
	holdCode, holdReason := device.HoldReason()

	// How much of this device's quarantine allowance is left (migration 00050).
	//
	// Read once per batch, like the status, and for the same reason. It is a *budget*, not a
	// validity check: what it bounds is the number of events one device may leave for a person to
	// read, and the reason that needs bounding is that CP65 opened this route to a device the
	// clinic has refused, and `dthcms_app` may not DELETE what lands here.
	held, capacity, err := s.store.QuarantineLoad(ctx, device.ID)
	if err != nil {
		return Receipt{}, err
	}

	// A refused device with a full quarantine is turned away whole, before a row is written.
	//
	// The distinction matters and is not symmetry for its own sake. Every event from a refused
	// device is held, so at the ceiling *nothing* in this batch can land — opening a batch row and
	// five hundred result rows to say so would be the flood arriving through the receipt tables
	// instead. A device that is **not** refused is never turned away like this: its valid events
	// still go into the ledger, and only the ones that would have needed a hold — a clock far
	// ahead, a type this server does not know — come back BLOCKED. A rolling deploy must not lock
	// a working tablet out of recording blood pressures.
	if holdCode != "" && held >= int64(capacity) {
		return Receipt{}, fmt.Errorf("%w: %d awaiting review, and the limit is %d",
			ErrQuarantineFull, held, capacity)
	}

	// The batch row is opened **before** any event is processed, with zero counts, and closed at
	// the end with the real ones.
	//
	// Two reasons, and the second is the one that made this necessary rather than tidy. A
	// quarantined event points at its batch, so the batch has to exist before the first one is
	// held. And a crash halfway through leaves a receipt saying nothing was processed — which is
	// honest and is what a client should re-send against, where a missing receipt would leave it
	// unable to tell a batch that was never seen from one that was half done.
	if !reopened {
		if err := s.openBatch(ctx, in, actor, device, now); err != nil {
			return Receipt{}, err
		}
	}

	receipt := Receipt{
		BatchID: in.BatchID, ReceivedAt: now, ServerTime: now,
		Events: len(in.Events), ClockSkewMS: skewOf(in.ClientClock, now),
		Results: make([]Result, 0, len(in.Events)),
	}

	// Grouped by aggregate, and each group in the order the client sent it. A map of slices rather
	// than a sort, because sorting would lose the client's order within a group — which is the one
	// thing criterion 4 asks be kept.
	order := []string{}
	groups := map[string][]Incoming{}
	for _, e := range in.Events {
		key := e.AggregateType + "/" + e.AggregateID.String()
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], e)
	}

	// What is left of the allowance, counted down as this batch spends it. A local counter rather
	// than a re-read per event: the only writer to this device's quarantine inside this batch is
	// this loop, and a query per event would be fifty round trips to learn something already known.
	room := int64(capacity) - held
	if room < 0 {
		room = 0
	}

	results := map[uuid.UUID]Result{}
	for _, key := range order {
		failedEarlier := false
		for _, e := range groups[key] {
			result := s.one(ctx, in, device, holdCode, holdReason, e, actor, now, failedEarlier, &room)
			results[e.EventID] = result
			switch result.Outcome {
			case Rejected, Quarantined:
				// Later events on this aggregate that declare a dependency are now doomed. Ones
				// that do not are facts about their own moment and proceed — blocking them would
				// hold a morning's work hostage to one typo.
				failedEarlier = true
			}
		}
	}

	// Reported in the order the client sent, so a client can walk its outbox against the response
	// without building an index.
	for _, e := range in.Events {
		result := results[e.EventID]
		receipt.Results = append(receipt.Results, result)
		switch result.Outcome {
		case Accepted:
			receipt.Accepted++
		case Duplicate:
			receipt.Duplicated++
		case Rejected:
			receipt.Rejected++
		case Quarantined:
			receipt.Quarantined++
		case Blocked:
			receipt.Blocked++
		}
	}

	if err := s.closeBatch(ctx, in, device, receipt, now); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

// openBatch writes the receipt row with nothing counted yet.
func (s *Service) openBatch(ctx context.Context, in Batch, actor eventstore.Actor,
	device Device, now time.Time) error {

	params := dbgen.RecordBatchParams{
		ID: in.BatchID, DeviceID: device.ID, UserID: actor.UserID(),
		FacilityID: device.FacilityID, ReceivedAt: now,
		SkewMs: skewOf(in.ClientClock, now),
	}
	if in.ClientClock != nil {
		clock := in.ClientClock.UTC()
		params.ClientClock = &clock
	}
	_, err := s.store.q.RecordBatch(ctx, params)
	return err
}

// one processes a single event. Its own transaction, which is what makes criterion 1 true.
func (s *Service) one(ctx context.Context, batch Batch, device Device,
	holdCode, holdReason string, e Incoming, actor eventstore.Actor, now time.Time,
	failedEarlier bool, room *int64) Result {

	result := Result{EventID: e.EventID}

	// Declared a dependency on an aggregate whose earlier event failed: attempting it would
	// produce a sequence conflict, which says something misleading about why it did not land.
	if failedEarlier && e.ExpectedSequence != 0 {
		result.Outcome = Blocked
		result.ReasonCode = ReasonEarlierFailed
		result.Reason = "an earlier event for the same record did not land, and this one says it depends on that record's state"
		return result
	}

	// A device that was revoked or suspended while it was offline. Held rather than accepted or
	// dropped: accepting defeats revocation, dropping loses a morning of measurements silently.
	if holdCode != "" {
		return s.hold(ctx, batch, device, e, actor, now, holdCode, holdReason, room)
	}

	// A clock far enough ahead to damage a timeline. Also held rather than rejected: the
	// measurement is real, only its timestamp is not trustworthy, and a person can say what time
	// it actually was.
	if implausible(e.OccurredAt, now) {
		return s.hold(ctx, batch, device, e, actor, now, ReasonClockImplausible,
			fmt.Sprintf("this event is dated %s, which is more than %s ahead of the clinic's clock",
				e.OccurredAt.UTC().Format(time.RFC3339), ClockTolerance), room)
	}

	// A type nobody has registered cannot be validated, so it cannot be trusted — but it is also
	// probably a newer app talking to an older server, which is an ordinary state during a rolling
	// deploy and not something to throw away.
	if _, known := s.events.Registry().Lookup(e.EventType, e.EventVersion); !known {
		return s.hold(ctx, batch, device, e, actor, now, ReasonUnknownType,
			"this server does not know the event type "+e.EventType, room)
	}

	envelope := eventstore.Envelope{
		EventID: e.EventID, AggregateType: e.AggregateType, AggregateID: e.AggregateID,
		PatientID: e.PatientID, VisitID: e.VisitID,
		EventType: e.EventType, EventVersion: e.EventVersion,
		// **Preserved**, which is criterion 3's first half. The server assigns recorded_at, which
		// is its second: a blood pressure taken at 08:40 and synced at 11:00 is a fact about 08:40
		// that the record learned about at 11:00, and collapsing the two would put a morning of
		// measurements in the wrong order on every timeline.
		OccurredAt: e.OccurredAt,
		Actor:      actor,
		// Not MOBILE_ONLINE, whatever the client says. This is how a reader tells a value that was
		// entered at the bedside from one that arrived hours later through a queue, and letting a
		// client assert it would make the distinction worthless.
		Source:           eventstore.SourceMobileOfflineSync,
		Payload:          e.Payload,
		Metadata:         e.Metadata,
		ExpectedSequence: e.ExpectedSequence,
	}

	written, err := s.events.Append(ctx, envelope)
	switch {
	case err == nil && written.Duplicate:
		result.Outcome = Duplicate
		result.GlobalSeq = written.GlobalSeq
	case err == nil:
		result.Outcome = Accepted
		result.GlobalSeq = written.GlobalSeq
	case errors.Is(err, eventstore.ErrSequenceConflict):
		result.Outcome = Rejected
		result.ReasonCode = ReasonSequenceConflict
		result.Reason = "this record has moved on since the device last saw it"
	default:
		result.Outcome = Rejected
		result.ReasonCode = ReasonInvalidPayload
		// The message rather than the wrapped error: a validation failure names the field, and a
		// device that is told which field is one whose next version can stop sending it.
		result.Reason = err.Error()
	}
	return result
}

// hold writes the event to the quarantine, whole.
func (s *Service) hold(ctx context.Context, batch Batch, device Device, e Incoming,
	actor eventstore.Actor, now time.Time, code, reason string, room *int64) Result {

	// Out of allowance (migration 00050). BLOCKED and not REJECTED, deliberately: the event is
	// real and the client should keep it and send it again once somebody has worked through this
	// device's triage list. Rejecting would make a conforming client drop a blood pressure because
	// a supervisor is behind on their reading — the silent loss this table exists to prevent,
	// arriving through a new door.
	if *room <= 0 {
		return Result{
			EventID: e.EventID, Outcome: Blocked, ReasonCode: ReasonQuarantineFull,
			Reason: "this device already has as many events awaiting review as it may have; " +
				"somebody must work through them before it can hold more",
		}
	}

	result := Result{EventID: e.EventID, Outcome: Quarantined, ReasonCode: code, Reason: reason}

	envelope, err := json.Marshal(e)
	if err != nil {
		// Unmarshalable is unreachable — it came in as JSON — but a quarantine that failed to
		// store the content would be the silent loss this whole table exists to prevent, so it
		// is a rejection with the reason rather than a hold that holds nothing.
		result.Outcome = Rejected
		result.ReasonCode = ReasonRefused
		result.Reason = "this event could not be stored for review: " + err.Error()
		return result
	}

	params := dbgen.QuarantineParams{
		ID: uuid.New(), BatchID: batch.BatchID, EventID: e.EventID,
		DeviceID: device.ID, UserID: actor.UserID(), FacilityID: device.FacilityID,
		ReasonCode: code, Reason: reason, Envelope: envelope,
		EventType: e.EventType, OccurredAt: e.OccurredAt.UTC(), HeldAt: now,
	}
	if e.PatientID != nil {
		params.PatientID = uuid.NullUUID{UUID: *e.PatientID, Valid: true}
	}
	if _, err := s.store.q.Quarantine(ctx, params); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		// The trigger got there first: another push from this device filled the last of the
		// allowance between the count above and this insert. Rare, bounded by how many pushes one
		// device has in flight, and answered the same way the counter answers — the client keeps
		// the event and comes back. Told apart by SQLSTATE rather than by message, because the
		// message is a sentence somebody may reword.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == checkViolation {
			return Result{
				EventID: e.EventID, Outcome: Blocked, ReasonCode: ReasonQuarantineFull,
				Reason: "this device already has as many events awaiting review as it may have; " +
					"somebody must work through them before it can hold more",
			}
		}
		result.Outcome = Rejected
		result.ReasonCode = ReasonRefused
		result.Reason = "this event could not be stored for review: " + err.Error()
		return result
	}
	*room--
	return result
}

// closeBatch writes the counts and the per-event answers, in one transaction.
//
// One transaction even though the appends were separate: the receipt is the client's only durable
// account of what happened, and one recording forty of fifty answers would be worse than none — a
// client reading it would believe ten events were never seen and send them again, which is
// harmless, or believe the batch is incomplete and stall, which is not.
func (s *Service) closeBatch(ctx context.Context, in Batch, device Device,
	receipt Receipt, now time.Time) error {

	return s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.store.q.WithTx(tx)
		if err := q.CloseBatch(ctx, dbgen.CloseBatchParams{
			ID: in.BatchID, Now: &now,
			Events: int32(receipt.Events), Accepted: int32(receipt.Accepted),
			Duplicated: int32(receipt.Duplicated), Rejected: int32(receipt.Rejected),
			Quarantined: int32(receipt.Quarantined), Blocked: int32(receipt.Blocked),
		}); err != nil {
			return err
		}
		for _, r := range receipt.Results {
			params := dbgen.RecordResultParams{
				BatchID: in.BatchID, EventID: r.EventID, Outcome: string(r.Outcome),
				ReasonCode: r.ReasonCode, Reason: r.Reason,
			}
			if r.GlobalSeq != 0 {
				seq := r.GlobalSeq
				params.GlobalSeq = &seq
			}
			if err := q.RecordResult(ctx, params); err != nil {
				return err
			}
		}
		return q.NotePush(ctx, dbgen.NotePushParams{
			DeviceID: device.ID, Now: &now, SkewMs: receipt.ClockSkewMS,
			Pushed: int64(receipt.Accepted), Quarantined: int64(receipt.Quarantined),
		})
	})
}

// Page is one pull.
type Page struct {
	Events []PulledEvent `json:"events"`
	// Cursor is where to ask from next. Always present, even for an empty page: a client that had
	// to infer its next cursor from the last event would have no cursor at all when the page was
	// empty, and would ask the same question forever.
	Cursor int64 `json:"cursor"`
	// Latest is where the ledger is now, so a client can tell whether it has caught up without
	// asking for an empty page to find out.
	Latest int64 `json:"latest"`
	// More is Cursor < Latest, computed here rather than left to a client that would have to know
	// the ledger's sequence is gapless to get it right.
	More bool `json:"more"`
}

// Pull serves the incremental download.
//
// Scoped twice: to the caller's facility, and to the event types their permissions allow. The
// second is a **declared map** rather than a denylist, so a type nobody has decided about is not
// pullable at all — the alternative puts a type added next month on every phone in the clinic
// before anybody notices.
func (s *Service) Pull(ctx context.Context, since int64, limit int, held []string) (Page, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Page{}, err
	}
	types := s.allowedTypes(held)
	if len(types) == 0 {
		// No permission reaches any pullable type. An empty page rather than a 403: the caller is
		// allowed to ask, there is simply nothing they may receive, and a 403 would read as a
		// misconfiguration to a client that is behaving correctly.
		latest, err := s.store.q.LatestGlobalSeq(ctx)
		if err != nil {
			return Page{}, err
		}
		return Page{Events: []PulledEvent{}, Cursor: since, Latest: latest}, nil
	}

	rows, err := s.store.q.EventsSince(ctx, dbgen.EventsSinceParams{
		Since: since, FacilityID: actor.FacilityID(), EventTypes: types, RowLimit: int32(limit),
	})
	if err != nil {
		return Page{}, err
	}
	latest, err := s.store.q.LatestGlobalSeq(ctx)
	if err != nil {
		return Page{}, err
	}

	page := Page{Events: make([]PulledEvent, 0, len(rows)), Cursor: since, Latest: latest}
	for _, row := range rows {
		event := PulledEvent{
			EventID: row.EventID, GlobalSeq: row.GlobalSeq,
			AggregateType: row.AggregateType, AggregateID: row.AggregateID,
			EventType: row.EventType, EventVersion: int(row.EventVersion),
			OccurredAt: row.OccurredAt, RecordedAt: row.RecordedAt, Payload: row.Payload,
			ActorUserID: row.ActorUserID, ActorRole: row.ActorRole, Source: row.Source,
		}
		if row.PatientID.Valid {
			id := row.PatientID.UUID
			event.PatientID = &id
		}
		if row.VisitID.Valid {
			id := row.VisitID.UUID
			event.VisitID = &id
		}
		if row.ActorStation != nil {
			event.ActorStation = *row.ActorStation
		}
		page.Events = append(page.Events, event)
		page.Cursor = row.GlobalSeq
	}
	page.More = page.Cursor < page.Latest
	return page, nil
}

// allowedTypes is the intersection of what may be pulled and what this caller may read.
func (s *Service) allowedTypes(held []string) []string {
	has := make(map[string]bool, len(held))
	for _, p := range held {
		has[p] = true
	}
	out := make([]string, 0, len(s.pullable))
	for eventType, permission := range s.pullable {
		if has[permission] {
			out = append(out, eventType)
		}
	}
	// Sorted so the query plan and the tests are stable.
	sort.Strings(out)
	return out
}

// NotePulled records where a device has got to.
func (s *Service) NotePulled(ctx context.Context, device uuid.UUID, seq int64) error {
	now := s.clock.Now().UTC()
	return s.store.q.NotePull(ctx, dbgen.NotePullParams{
		DeviceID: device, Seq: seq, Now: &now,
	})
}

// Release appends a held event to the ledger with its original time and actor.
//
// The append and the status change are **one transaction**, and invariant 95 checks the whole table
// for the same reason: "released" set while the append failed would mark a clinical measurement as
// recovered when it is not there, which is worse than never releasing it because now nobody is
// looking for it.
func (s *Service) Release(ctx context.Context, id uuid.UUID, note string) (Held, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Held{}, err
	}
	held, err := s.store.HeldEvent(ctx, id, actor.FacilityID())
	if err != nil {
		return Held{}, err
	}
	if held.Status != "HELD" {
		return Held{}, ErrAlreadyResolved
	}

	var incoming Incoming
	if err := json.Unmarshal(held.Envelope, &incoming); err != nil {
		return Held{}, fmt.Errorf("the held envelope cannot be read: %w", err)
	}

	// # Whose name goes on a released event
	//
	// Not the original operator's, and this took a moment's thought because the instinct is the
	// other way. `eventstore.Actor` is deliberately unforgeable — the fields are unexported and
	// the only way to obtain one is from an authenticated request — so rebuilding the operator's
	// actor is not merely difficult, it is the thing that design exists to prevent.
	//
	// It is also the wrong answer. The reason this event is in the record is that **a physician
	// decided it should be**, on evidence from a device somebody had already refused to trust, and
	// their name on it is not a compromise: it is the accurate statement of who is answerable.
	// Attributing it to the operator would say a measurement entered the permanent record on that
	// operator's authority, which is precisely what the revocation denied.
	//
	// So the operator is not lost, it is **carried**: the original user, device, held reason and
	// quarantine id go into the event's metadata, so a reader of the ledger years later sees a
	// value recorded by the physician on behalf of a named operator, from a named device, released
	// out of quarantine for a stated reason — which is the whole truth rather than half of it.
	metadata := map[string]any{}
	for k, v := range incoming.Metadata {
		metadata[k] = v
	}
	metadata["released_from_quarantine"] = held.ID.String()
	metadata["original_operator_id"] = held.UserID.String()
	metadata["original_device_id"] = held.DeviceID.String()
	metadata["held_reason_code"] = held.ReasonCode
	metadata["held_at"] = held.HeldAt.UTC().Format(time.RFC3339)

	envelope := eventstore.Envelope{
		EventID: incoming.EventID, AggregateType: incoming.AggregateType,
		AggregateID: incoming.AggregateID, PatientID: incoming.PatientID,
		VisitID: incoming.VisitID, EventType: incoming.EventType,
		EventVersion: incoming.EventVersion,
		// The **original** time, always. A blood pressure taken at 08:40 and released at four in
		// the afternoon is a fact about 08:40; dating it at release would put it after every
		// measurement taken since, on every timeline, forever.
		OccurredAt: incoming.OccurredAt,
		Actor:      actor,
		Source:     eventstore.SourceMobileOfflineSync,
		Payload:    incoming.Payload,
		Metadata:   metadata,
	}

	now := s.clock.Now().UTC()
	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		written, err := s.events.AppendInTx(ctx, tx, envelope)
		if err != nil {
			return err
		}
		_, err = s.store.q.WithTx(tx).ReleaseHeldEvent(ctx, dbgen.ReleaseHeldEventParams{
			ID: id, FacilityID: actor.FacilityID(), Now: &now,
			ResolvedBy: uuid.NullUUID{UUID: actor.UserID(), Valid: true}, Note: note,
			ReleasedEventID: uuid.NullUUID{UUID: written.EventID, Valid: true},
		})
		return err
	})
	if err != nil {
		return Held{}, err
	}
	return s.store.HeldEvent(ctx, id, actor.FacilityID())
}

// Discard refuses a held event, by a named person, with a reason.
func (s *Service) Discard(ctx context.Context, id uuid.UUID, note string) (Held, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Held{}, err
	}
	now := s.clock.Now().UTC()
	_, err = s.store.q.DiscardHeldEvent(ctx, dbgen.DiscardHeldEventParams{
		ID: id, FacilityID: actor.FacilityID(), Now: &now,
		ResolvedBy: uuid.NullUUID{UUID: actor.UserID(), Valid: true}, Note: note,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Separate answers: "there is no such held event" and "somebody already decided about
		// this one" send a person to different places.
		if _, err := s.store.HeldEvent(ctx, id, actor.FacilityID()); err != nil {
			return Held{}, err
		}
		return Held{}, ErrAlreadyResolved
	}
	if err != nil {
		return Held{}, err
	}
	return s.store.HeldEvent(ctx, id, actor.FacilityID())
}
