package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// What a physician does with a suggestion (CP73, §8's right panel, §7.3).
//
// The argument for storing this at all, and for storing it as an intent rather than as a
// prescription, is on [Decision]. This file is the mechanism.
//
// # Why there is no read model
//
// One visit's decisions are folded from that visit's own event stream. The stream is bounded
// by one journey through the clinic — a few dozen events including every queue movement and
// every counselling tick — and the fold is a switch over one event type. A read model would
// be a table, a projection, a migration and an invariant for data whose only reader is the
// screen that wrote it.
//
// §4.1 says the event log and not the current-state table is the source of truth. That is
// usually a statement about where the truth *comes* from; here it is literally where it is
// read, and the honest consequence is stated on [Decision]: cross-visit questions about
// rejection rates need a projection that does not exist yet.

// ErrNoSuchSuggestion is a decision naming something the current run did not suggest.
//
// Refused rather than stored. A decision against a reference nobody can resolve is a row that
// will be rendered against nothing forever, and the usual cause is a client holding a stale
// generation — which is a state worth telling the client about rather than absorbing.
var ErrNoSuchSuggestion = errors.New("dashboard: that suggestion is not in the current summary")

// ErrVisitClosed is a decision on a visit nobody is in.
var ErrVisitClosed = errors.New("dashboard: this visit is closed")

// ErrUnknownDecision is a decision that is not one of the three.
var ErrUnknownDecision = errors.New("dashboard: a decision is accepted, edited or rejected")

// ErrEditNeedsText is [Edited] with nothing edited.
//
// Refused rather than downgraded to an acceptance. "The physician agreed with a changed
// version" and "the physician agreed" are different records, and silently turning the first
// into the second would lose the change the physician made.
var ErrEditNeedsText = errors.New("dashboard: an edited suggestion carries the edited wording")

// MaxDecisionText bounds the edited wording and the note.
//
// Generous rather than tight — a physician explaining why they are rejecting a drafted
// insulin dose should not be truncated mid-sentence — and bounded at all because this text is
// written into an event payload that is read by everything downstream of the ledger.
const MaxDecisionText = 2000

// Deciding is one physician's answer to one suggestion.
type Deciding struct {
	EventID uuid.UUID
	VisitID uuid.UUID
	// PatientID is who the caller believes the visit belongs to. Checked rather than trusted:
	// a caller who holds one valid visit id could otherwise write a decision onto another
	// patient's consultation by changing the path, and the refusal is the same 404 a missing
	// patient gives so that the mismatch does not confirm the visit exists.
	PatientID uuid.UUID
	// Ref is the suggestion's stable handle, as the dashboard payload gave it.
	Ref  string
	Kind DecisionKind
	// Edited is the physician's own wording. Required for [Edited], refused for the others —
	// see [ErrEditNeedsText], and see below for why an edit on an acceptance is refused
	// rather than ignored.
	Edited string
	Note   string

	FacilityID uuid.UUID
	Source     eventstore.Source
}

// Decide records what the physician did.
//
// # Why the suggestion is resolved before the event is written
//
// The decision names a reference, and the reference has to exist in the summary the physician
// was actually looking at. Resolving it here means the event payload can carry the
// suggestion's kind, its label and the generation it came from — so the ledger row is
// readable years later without joining back to a synthesis run that may have been superseded
// four times since.
//
// It also means a client that has been left open across a re-run is told, rather than writing
// a decision that will never render. That is the [ErrNoSuchSuggestion] path, and it is a 409
// on the wire: the request is well formed and the caller is entitled to make it, and the
// remedy is to reload rather than to send different fields.
//
// # Why a closed visit is refused
//
// A consultation that is over is a consultation whose drafts nobody is acting on. Accepting a
// suggestion against it would put an intent into the record after the moment it could have
// meant anything, which is the shape of a back-dated note.
func (s *Service) Decide(ctx context.Context, req Deciding) (Decision, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Decision{}, err
	}
	switch req.Kind {
	case Accepted, Edited, Rejected:
	default:
		return Decision{}, ErrUnknownDecision
	}
	req.Edited = strings.TrimSpace(req.Edited)
	req.Note = strings.TrimSpace(req.Note)
	if req.Kind == Edited && req.Edited == "" {
		return Decision{}, ErrEditNeedsText
	}
	if req.Kind != Edited && req.Edited != "" {
		// Refused rather than dropped. A client sending edited text on an acceptance has
		// misunderstood which button was pressed, and quietly discarding a physician's own
		// wording is the worst available answer to that.
		return Decision{}, ErrEditNeedsText
	}
	if len([]rune(req.Edited)) > MaxDecisionText || len([]rune(req.Note)) > MaxDecisionText {
		return Decision{}, ErrEditNeedsText
	}

	current, err := s.visits.ByID(ctx, req.VisitID, req.FacilityID)
	if err != nil {
		if errors.Is(err, visit.ErrNotFound) {
			return Decision{}, ErrNoSuchPatient
		}
		return Decision{}, err
	}
	if req.PatientID != uuid.Nil && current.PatientID != req.PatientID {
		return Decision{}, ErrNoSuchPatient
	}
	if current.Status != visit.Open {
		return Decision{}, ErrVisitClosed
	}

	view, err := s.synthesis.Current(ctx, req.VisitID, req.FacilityID)
	if err != nil {
		return Decision{}, err
	}
	_, assistant := summaryOf(view)
	found, ok := findSuggestion(assistant, req.Ref)
	if !ok {
		return Decision{}, ErrNoSuchSuggestion
	}

	now := s.clock.Now().UTC()
	decision := Decision{
		Kind: req.Kind, Edited: req.Edited, Note: req.Note,
		DecidedBy: actor.UserID(), DecidedRole: actor.Role(), DecidedAt: now,
		Generation: assistant.Generation,
	}

	payload := eventstore.AISuggestionDecided{
		FacilityID: req.FacilityID.String(),
		PatientID:  current.PatientID.String(),
		VisitID:    req.VisitID.String(),
		Ref:        found.Ref,
		// The kind, the origin and the label are **copied** onto the event rather than left to
		// be joined from the synthesis run. A run is superseded by every re-run, and a ledger
		// row that could only be read by resolving a superseded run is a ledger row that
		// stops being readable exactly when somebody most needs it — during a review, years
		// later, of a decision somebody is being asked about.
		SuggestionKind: string(found.Kind),
		Origin:         string(found.Origin),
		Label:          found.Label,
		Decision:       string(req.Kind),
		Edited:         req.Edited,
		Note:           req.Note,
		Generation:     assistant.Generation,
		DecidedAt:      now,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Decision{}, err
	}

	eventID := req.EventID
	if eventID == uuid.Nil {
		eventID = uuid.New()
	}
	source := req.Source
	if source == "" {
		source = eventstore.SourceWeb
	}
	patientID := current.PatientID
	_, err = s.events.Append(ctx, eventstore.Envelope{
		EventID: eventID,
		// The visit is the aggregate, like the synthesis events this answers. A decision is
		// about one consultation: the same physician meeting the same patient next month is
		// answering a different draft about a different set of measurements.
		AggregateType: "VISIT", AggregateID: req.VisitID,
		PatientID: &patientID, VisitID: &req.VisitID,
		EventType: "AI_SUGGESTION_DECIDED", EventVersion: 1,
		OccurredAt: now, Actor: actor, Source: source,
		Payload: encoded,
	})
	if err != nil {
		return Decision{}, err
	}
	return decision, nil
}

func findSuggestion(assistant *Assistant, ref string) (Suggestion, bool) {
	if assistant == nil {
		return Suggestion{}, false
	}
	for _, s := range assistant.Suggestions {
		if s.Ref == ref {
			return s, true
		}
	}
	return Suggestion{}, false
}

// applyDecisions folds the visit's stream onto the suggestions.
//
// # Last one wins, and the earlier ones stay
//
// A physician who rejects a drafted drug and then changes their mind writes a second event.
// The panel shows the second; the ledger holds both, and it holds them in order with the
// actor and the time on each. Nothing is updated and nothing is deleted, which is the whole
// of §4.5 as it reaches this feature — and it means "they accepted it after first rejecting
// it" is a question the record can answer, which is precisely the kind of question a review
// asks and a current-state table cannot.
//
// # Why a decision from an older generation is still shown
//
// A re-run produces a new generation and often the same suggestions. Hiding a decision made
// against generation 2 when the panel is showing generation 3 would make a physician decide
// the same drafts again every time the summary refreshed, which is how a panel teaches people
// to click through it. So the decision is shown with its generation on it, and the screen
// says when the two differ — the physician decides whether their earlier answer still stands,
// which is a clinical judgement rather than something a fold should make for them.
func (s *Service) applyDecisions(ctx context.Context, visitID uuid.UUID, assistant *Assistant) error {
	events, err := s.events.Stream(ctx, "VISIT", visitID, 0, maxVisitStream)
	if err != nil {
		return err
	}
	// Newest wins, so the stream is walked in order and each reference overwritten. Sorting
	// first rather than trusting the read: `Stream` returns sequence order today, and a fold
	// whose correctness depends on a store's ordering guarantee is a fold that breaks quietly
	// the day the query grows a join.
	sort.SliceStable(events, func(i, j int) bool { return events[i].Sequence < events[j].Sequence })

	latest := map[string]Decision{}
	for _, ev := range events {
		if ev.EventType != "AI_SUGGESTION_DECIDED" {
			continue
		}
		var payload eventstore.AISuggestionDecided
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			// A payload this build cannot read is skipped rather than fatal. The suggestion
			// draws as undecided, which is the safe direction: the physician is asked again
			// rather than shown a decision nobody can render.
			continue
		}
		latest[payload.Ref] = Decision{
			Kind: DecisionKind(payload.Decision), Edited: payload.Edited, Note: payload.Note,
			DecidedBy: ev.Actor.UserID(), DecidedRole: ev.Actor.Role(),
			DecidedAt: payload.DecidedAt, Generation: payload.Generation,
		}
	}
	if len(latest) == 0 {
		return nil
	}
	for i := range assistant.Suggestions {
		if decision, ok := latest[assistant.Suggestions[i].Ref]; ok {
			copied := decision
			assistant.Suggestions[i].Decision = &copied
		}
	}
	return nil
}

// maxVisitStream bounds the fold.
//
// A visit's stream is one journey: a registration touch, a dozen station encounters, its
// measurements, its counselling ticks and its synthesis runs. Two thousand is far above any
// of that and far below anything that would cost real time, and it exists so that a visit
// somebody has reopened forty times cannot make the dashboard slow. If it is ever hit, the
// oldest decisions are the ones dropped — which is the right way round, because the fold
// keeps the newest per reference.
const maxVisitStream = 2000
