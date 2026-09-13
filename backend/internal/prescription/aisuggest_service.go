package prescription

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/ai"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Asking the agent, and answering it one line at a time (CP82).
//
// # The order of the gates, and why the allergy one is first
//
// §2 lists four refusals. Three are about the answer and one is about the patient, and the one
// about the patient runs **before the gateway is called at all**:
//
//	1  the prescription is a draft                       — else REFUSED, no call
//	2  the patient has an allergy status (CP54)          — else REFUSED, no call
//	3  the formulary has something left after §2          — else REFUSED, no call
//	4  ... the model is asked ...
//	5  every item is re-checked against the formulary and the controlled register, and dropped
//	   rather than repaired
//
// Step 2 could have been a filter on the answer, and it would have been wrong. CP54 made allergy
// status a hard stop for prescribing; a machine that formed a proposal about such a patient and
// then discarded it has still spent that patient's clinical picture on a model call, and a
// deployment where the discard was one refactor from being a warning is a deployment where the
// stop has quietly become soft. So there is no answer to discard.
//
// # What the physician is told when nothing comes back
//
// Always a sentence, in both languages, and never a 404. D-15's rule is *fail visible, never fail
// silent, never fail invented*, and the failure it is really about is an empty panel — which tells
// a physician nothing about whether the system tried. "The AI proposed nothing" and "nobody has
// asked" and "the model could not be reached" are three different facts and the panel says which.
//
// # No PHI leaves this file
//
// Nothing here logs a drug, a dose, a patient or a suggestion. The two log lines that exist carry
// an agent code, a run id and a count, which is what an operator needs to find the row and nothing
// that would make the log a second copy of the record. This is the highest-risk surface in the
// system for that mistake and it is the one place where the temptation is strongest, because a
// model's answer is exactly the thing a developer wants to print.

// Briefing is the clinical context the agent is shown, and the identifiers the gateway strikes out
// of it.
//
// An interface rather than an import, for the reason every seam in this module is one:
// `architecture.json` does not let `prescription` import `synthesis` or `patient`, and it should
// not — a prescribing module that could read a patient record directly would be a second answer to
// "what does the model get to see". What crosses is CP71's already-assembled context and an
// [ai.Subject], and the composition root is where the two are fetched.
//
// Both halves come from one method on purpose. A caller that could get the payload without the
// subject would be a caller that could send a clinical picture to a model with nothing to strike
// out of it, and the gateway's minimiser would then have only its patterns to work with.
type Briefing interface {
	PrescribingBriefing(ctx context.Context, facility, patient, visit uuid.UUID) (ai.Subject, map[string]any, error)
}

// AllergyGate answers CP54's question about one patient.
//
// It returns `core.allergy_status()`'s own word — the same function the trigger on the queue
// calls — so that "does this patient have allergy status" cannot be answered two ways. A status of
// `NONE_RECORDED` is §2's hard stop.
type AllergyGate interface {
	AllergyStatus(ctx context.Context, facility, patient uuid.UUID) (string, error)
}

// AllergyStatusNoneRecorded is the one answer that stops the agent.
//
// Duplicated from `allergy.StatusNone` rather than imported, because `prescription` may not import
// `allergy` — and a mistyped constant here fails **closed**, which is the direction that matters:
// a typo makes the comparison never match, and a comparison that never matches refuses nothing.
// That is the failure this file's tests would not catch on their own — they drive the gate through
// a stub — so `TestTheAllergyStopComparesAgainstWhatTheDatabaseActuallySays` asks
// `core.allergy_status()` itself and compares its answer to this string.
const AllergyStatusNoneRecorded = "NONE_RECORDED"

// SuggestConfig wires the agent into the handlers.
type SuggestConfig struct {
	// Gateway is CP70's, and the only path to a model. Nil in a process that does not do AI work
	// (and in every process until the composition root wires it), which makes the routes answer
	// [ErrSuggestionsUnavailable] loudly rather than answering "no suggestions" quietly.
	Gateway  *ai.Gateway
	Briefing Briefing
	Allergy  AllergyGate
}

// wired reports whether this process can ask for suggestions at all.
func (c SuggestConfig) wired() bool {
	return c.Gateway != nil && c.Briefing != nil && c.Allergy != nil
}

// ---------------------------------------------------------------------------
// Asking
// ---------------------------------------------------------------------------

// Suggest asks the agent for medicines to propose against one draft.
//
// It always records a run, and the run is the answer: a refusal, a failure and an empty list are
// all states a physician is shown rather than errors a client has to interpret. The only errors
// this returns are the ones that mean the caller asked about something that is not theirs or is
// not there.
func (s *Service) Suggest(ctx context.Context, prescriptionID uuid.UUID) (Run, error) {
	if !s.suggest.wired() {
		return Run{}, ErrSuggestionsUnavailable
	}
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Run{}, err
	}
	facility := actor.FacilityID()
	now := s.now()

	sheet, err := s.store.ByID(ctx, prescriptionID, facility)
	if err != nil {
		return Run{}, err
	}

	run := Run{
		ID: uuid.New(), PrescriptionID: sheet.ID, PatientID: sheet.PatientID,
		VisitID: sheet.VisitID, RequestedAt: now, RequestedBy: actor.UserID(),
	}

	// Gate 1. Suggestions are offered against a sheet somebody is writing.
	if !sheet.Status.Editable() {
		return s.settleRun(ctx, facility, refuse(run, RefusalNotADraft))
	}

	// Gate 2. §2's hard stop, before anything is assembled and before anything is sent.
	//
	// A status that cannot be read is treated as absent, which refuses. The alternative — proceed
	// when the allergy table is unavailable — would make a database outage into a moment when the
	// machine suggests around CP54's stop, and that is precisely the direction not to fail in.
	status, err := s.suggest.Allergy.AllergyStatus(ctx, facility, sheet.PatientID)
	if err != nil {
		s.logger.WarnContext(ctx, "the allergy status could not be read; the prescribing agent is refusing rather than proposing",
			"agent_code", AgentCode, "run_id", run.ID.String(), "error", err.Error())
		status = AllergyStatusNoneRecorded
	}
	if status == AllergyStatusNoneRecorded {
		return s.settleRun(ctx, facility, refuse(run, RefusalNoAllergyStatus))
	}

	// Gate 3. What the agent is allowed to choose from, after §2's formulary and controlled-drug
	// filters. An empty shortlist is recorded as its own refusal rather than as an empty answer:
	// "we asked and it proposed nothing" and "there was nothing to propose" are different facts.
	candidates, err := s.store.Candidates(ctx, facility, day(now), MaxCandidates)
	if err != nil {
		return Run{}, err
	}
	if len(candidates) == 0 {
		return s.settleRun(ctx, facility, refuse(run, RefusalNoCandidates))
	}

	subject, briefing, err := s.suggest.Briefing.PrescribingBriefing(ctx, facility,
		sheet.PatientID, sheet.VisitID)
	if err != nil {
		run.State, run.FailureDetail = RunFailed, "the clinical context could not be assembled"
		return s.settleRun(ctx, facility, run)
	}

	// CP81's dose guidance, in the payload beside the shortlist.
	//
	// Two reasons, and the second is the load-bearing one. Clinically, the clinic's own approved
	// starting doses are better guidance than whatever a general model remembers about Bangladeshi
	// prescribing. Mechanically, the gateway's grounding check refuses any number in the model's
	// prose that is not in the payload — so a payload that did not carry the usual doses would
	// refuse every well-formed suggestion for writing one. A defaults table that cannot be read
	// costs the guidance and not the run.
	defaults, err := s.store.PrescribingDefaults(ctx, facility)
	if err != nil {
		s.logger.WarnContext(ctx, "the prescribing defaults could not be read; the agent is being asked without them",
			"agent_code", AgentCode, "run_id", run.ID.String(), "error", err.Error())
		defaults = nil
	}

	// Read once and used twice: to keep a scheduled molecule out of the dose guidance before the
	// call, and to re-check the answer after it.
	controlledGenerics, err := s.store.controlledGenerics(ctx)
	if err != nil {
		return Run{}, err
	}

	result, err := s.suggest.Gateway.Invoke(ctx, ai.Request{
		AgentCode: AgentCode, Subject: subject,
		Payload: payloadFor(briefing, candidates, defaults, controlledGenerics, sheet),
	})
	if err != nil {
		run.State = RunFailed
		run.FailureDetail = gatewayFailure(err)
		// The error's own text is deliberately not stored. It can carry an excerpt of the model's
		// answer — a grounding finding does — and an excerpt of an answer about a patient is the
		// one thing this column must not hold. The gateway already keeps the detail on
		// `core.ai_interaction`, where the identifier constraints apply.
		s.logger.WarnContext(ctx, "the prescribing agent produced no suggestions",
			"agent_code", AgentCode, "run_id", run.ID.String(), "outcome", run.FailureDetail)
		return s.settleRun(ctx, facility, run)
	}

	interaction := result.InteractionID
	run.State = RunReady
	run.InteractionID = &interaction
	run.PromptVersion, run.ModelVersion = result.PromptVersion, result.ModelVersion

	kept, drops, err := s.sift(ctx, facility, sheet, candidates, result.Output, now)
	if err != nil {
		return Run{}, err
	}
	run.Suggestions = kept
	run.OfferedCount = len(kept)
	run.DroppedReasons = drops
	for _, n := range drops {
		run.DroppedCount += n
	}
	return s.settleRun(ctx, facility, run)
}

// refuse stamps a run that never reached a model.
func refuse(run Run, why Refusal) Run {
	run.State, run.Refusal = RunRefused, why
	return run
}

// settleRun writes the run and its suggestions in one transaction and returns it with its
// sentences filled in.
func (s *Service) settleRun(ctx context.Context, facility uuid.UUID, run Run) (Run, error) {
	err := s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbgen.Queries) error {
		if err := s.store.insertRun(ctx, tx, run, facility); err != nil {
			return err
		}
		for i := range run.Suggestions {
			// Stamped here rather than in `sift`, which does not know the run's id yet and should
			// not: it decides what survives §2 and nothing about bookkeeping.
			run.Suggestions[i].RunID = run.ID
		}
		for _, offered := range run.Suggestions {
			if err := s.store.insertSuggestion(ctx, tx, run.ID, facility,
				run.PrescriptionID, offered); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Run{}, err
	}
	return describe(run), nil
}

// payloadFor is what the model is shown.
//
// CP71's assembled context, plus two things it does not carry: the shortlist this server built,
// and the lines already on the draft. The second matters more than it looks — a model that cannot
// see what the physician has already written proposes it again, and a panel whose first suggestion
// is the drug on line one is a panel he stops reading, which is §3's whole concern.
//
// The briefing is copied rather than mutated. It belongs to the caller, and a function that
// quietly added keys to somebody else's map would make the stored synthesis context and the sent
// payload differ by whatever this function happened to add.
func payloadFor(briefing map[string]any, candidates []Candidate, defaults []PrescribingDefault,
	controlled map[uuid.UUID]bool, sheet Prescription) map[string]any {

	out := make(map[string]any, len(briefing)+3)
	for k, v := range briefing {
		out[k] = v
	}
	out["formulary_candidates"] = candidates

	// The clinic's own starting doses, stripped to the four fields a prescriber uses. The whole
	// row carries an approval block, a source citation and two rationales, and none of that is
	// something the model should be reasoning from — a suggestion's reasoning must rest on the
	// patient's facts, not on a citation the model can quote back.
	guidance := []map[string]any{}
	for _, d := range defaults {
		// §2 again, in the second list. The shortlist subtracts the controlled register and the
		// guidance table has to as well: CP81 seeds a starting dose for pregabalin, so a payload
		// that carried the whole table would name a scheduled drug to the model even though it
		// could not propose one — and "the model was never shown it" is the first and cheapest of
		// §2's two locks.
		if controlled[d.GenericID] {
			continue
		}
		guidance = append(guidance, map[string]any{
			"generic": d.GenericName, "strength": d.Strength,
			"dose": d.Dose, "frequency": d.Frequency, "duration_days": d.DurationDays,
			"route": d.Route,
		})
	}
	out["usual_starting_doses"] = guidance

	lines := []map[string]any{}
	for _, item := range sheet.Items {
		if !item.Live() {
			continue
		}
		lines = append(lines, map[string]any{
			// `generic` and not `generic_name`: see [Candidate.GenericName] for why a key ending
			// in `name` cannot be sent.
			"generic": item.GenericName, "product_label": item.Label,
			"strength": item.Strength, "dose": item.Dose, "frequency": item.Frequency,
		})
	}
	out["current_prescription"] = lines

	// **Round-tripped through JSON before it is handed to the gateway, and this is not tidiness.**
	//
	// The minimiser walks `map[string]any`, `[]any` and `string`. A `[]Candidate` is none of those,
	// so a payload carrying typed structs is *silently skipped* — not scrubbed, not indexed, not
	// checked. The scrubber would not see a name inside one, and CP72's grounding index would not
	// see the numbers, so every dose the model copied out of the shortlist would be reported as
	// invented and every answer refused. Both failures are quiet and only one of them is safe.
	//
	// The fix is to hand the gateway nothing it cannot walk. `synthesis.payloadOf` does the same
	// thing for the same reason; the difference is that it happened to be written that way and this
	// comment exists because this one was not.
	encoded, err := json.Marshal(out)
	if err != nil {
		return out
	}
	var flat map[string]any
	if err := json.Unmarshal(encoded, &flat); err != nil {
		return out
	}
	return flat
}

// gatewayFailure turns a gateway error into a word an operator can group by.
//
// The word and never the sentence. A gateway error can carry an excerpt of the model's answer, and
// the model's answer is about a patient; the sentence belongs on `core.ai_interaction`, where the
// identifier constraints apply, and the word belongs here, where a screen reads it.
func gatewayFailure(err error) string {
	switch {
	case errors.Is(err, ai.ErrFreeTierRefused), errors.Is(err, ai.ErrPayloadNamesAPerson),
		errors.Is(err, ai.ErrIdentifierSurvived):
		return "REFUSED"
	case errors.Is(err, ai.ErrUngrounded):
		return "UNGROUNDED"
	case errors.Is(err, ai.ErrInvalidOutput):
		return "INVALID_OUTPUT"
	case errors.Is(err, ai.ErrCircuitOpen), errors.Is(err, ai.ErrProviderUnavailable),
		errors.Is(err, ai.ErrProviderRateLimited), errors.Is(err, ai.ErrProviderRefused),
		errors.Is(err, ai.ErrProviderRejected):
		return "PROVIDER"
	case errors.Is(err, context.DeadlineExceeded):
		return "TIMEOUT"
	}
	return "INTERNAL"
}

// ---------------------------------------------------------------------------
// Sifting the answer (§2)
// ---------------------------------------------------------------------------

// sift turns the model's answer into suggestions, dropping everything §2 forbids.
//
// **Dropped and not repaired.** §2 is explicit: *"a suggestion that fails validation is dropped,
// not repaired — a repaired suggestion is one nobody wrote."* So there is no branch below that
// fills in a missing frequency, rounds a duration into range, or substitutes the nearest product
// for one that is not in the formulary. An item that does not arrive complete and legal does not
// arrive.
//
// The drop counts go on the run. A deployment where the model keeps proposing controlled drugs is
// a prompt that needs work, and the only way anybody learns that is a number somebody can see.
func (s *Service) sift(ctx context.Context, facility uuid.UUID, sheet Prescription,
	candidates []Candidate, output map[string]any, now time.Time) ([]Suggestion, map[string]int, error) {

	shortlist := make(map[string]Candidate, len(candidates))
	for _, c := range candidates {
		shortlist[c.Ref] = c
	}
	// Asked of the database rather than inferred from the shortlist's absence, so that a product
	// that is controlled and a product that is merely off the list are counted apart.
	controlled, err := s.store.controlledProducts(ctx, facility)
	if err != nil {
		return nil, nil, err
	}
	already := map[uuid.UUID]bool{}
	for _, item := range sheet.Items {
		if item.Live() && item.ProductID != nil {
			already[*item.ProductID] = true
		}
	}

	drops := map[string]int{}
	drop := func(why Drop) { drops[string(why)]++ }

	kept := []Suggestion{}
	seen := map[uuid.UUID]bool{}
	for _, raw := range asObjects(output["suggestions"]) {
		if len(kept) >= MaxSuggestions {
			drop(DropOverCap)
			continue
		}
		candidate, listed := shortlist[strings.TrimSpace(text(raw["product_ref"]))]
		if !listed {
			// §2's first refusal. The model answered with a handle that was not on the list it
			// was given, which is either a transcription error or an invention; either way the
			// clinic cannot dispense what it names.
			drop(DropNotInFormulary)
			continue
		}
		productID := candidate.ProductID
		// §2's second, asked again against the register rather than inferred from the shortlist's
		// having excluded it.
		//
		// **What this can and cannot catch, plainly.** For a well-formed answer against a shortlist
		// built moments earlier it cannot fire: the shortlist already subtracted the register, so a
		// ref that resolves is a ref to something that was not controlled when the query ran. What
		// it catches is the register changing between the two queries — a physician adding a
		// molecule mid-consultation — and, more usefully, the day the shortlist comes from CP76's
		// in-memory formulary cache instead of a live query, at which point this stops being the
		// second lock and becomes the only one. It is cheap, and a check that is cheap and becomes
		// load-bearing later is worth having before it does.
		if controlled[productID] {
			drop(DropControlled)
			continue
		}
		if seen[productID] || already[productID] {
			drop(DropDuplicate)
			continue
		}

		dose := strings.TrimSpace(text(raw["dose"]))
		frequency := strings.TrimSpace(text(raw["frequency"]))
		route := strings.TrimSpace(text(raw["route"]))
		if dose == "" || frequency == "" {
			drop(DropIncomplete)
			continue
		}
		duration, durationOK := wholeNumber(raw["duration_days"])
		if !durationOK || duration < 1 || duration > 3650 {
			drop(DropIncomplete)
			continue
		}
		rationaleEN := strings.TrimSpace(text(raw["rationale_en"]))
		rationaleBN := strings.TrimSpace(text(raw["rationale_bn"]))
		basis := basisOf(raw["basis"])
		if rationaleEN == "" || rationaleBN == "" || len(basis) == 0 {
			drop(DropNoRationale)
			continue
		}

		seen[productID] = true
		kept = append(kept, Suggestion{
			ID: uuid.New(), Ordinal: len(kept) + 1,
			ProductID: candidate.ProductID, Label: candidate.Label,
			GenericName: candidate.GenericName, Strength: candidate.Strength,
			FormCode: candidate.FormCode,
			Dose:     dose, Frequency: frequency, DurationDays: &duration, Route: route,
			RationaleEN: rationaleEN, RationaleBN: rationaleBN, Basis: basis,
			OfferedAt: now,
		})
	}
	return kept, drops, nil
}

// ---------------------------------------------------------------------------
// Deciding (§4)
// ---------------------------------------------------------------------------

// Deciding is one physician's answer to one suggestion.
type Deciding struct {
	EventID        uuid.UUID
	PrescriptionID uuid.UUID
	SuggestionID   uuid.UUID
	Kind           DecisionKind

	// Edit carries the physician's own values, for [DecisionEdited]. Every field is optional and
	// an absent one keeps the suggestion's. An edit that changes nothing is refused — see
	// [ErrEditChangesNothing].
	Edit EditedLine

	// ReasonCode and Note are §5's, and only a rejection may carry them.
	ReasonCode string
	Note       string

	LedgerSource eventstore.Source
}

// EditedLine is what the physician changed.
//
// Pointers throughout, because "the physician did not touch the duration" and "the physician set
// the duration to nothing" are different acts and a zero value cannot tell them apart.
type EditedLine struct {
	Dose         *string
	Frequency    *string
	DurationDays *int
	Route        *string

	InstructionsEN string
	InstructionsBN string
}

// ErrEditChangesNothing is an edit identical to what was offered.
//
// Refused rather than recorded, and this is the one refusal in CP82 that is about the quality of
// the signal rather than about safety. §4's table says an edit *"produces a prescription line that
// differs"*; an edit that differs in nothing is an acceptance, and recording it as an edit would
// put a row in the trail saying the physician changed the model's mind about something when he did
// not. Phase 3 reads that trail.
var ErrEditChangesNothing = errors.New("an edit that changes nothing is an acceptance")

// Decide records what the physician did with one suggestion, and — for an acceptance or an edit —
// writes the prescription line in the same transaction.
//
// # This function is CP82's acceptance criterion 1
//
// It is the only caller in this repository that passes a non-nil origin to `Service.addItem`, and
// it does so inside a transaction that also inserts the decision row. Migration 00069's deferred
// constraint trigger checks the pair at COMMIT, so a future edit to this function that wrote the
// line and forgot the decision would not produce a half-recorded acceptance — it would produce a
// transaction that cannot commit.
//
// The order inside the transaction is item-then-decision only because the decision needs the item's
// id. It could be either way round and the guarantee would be the same, which is exactly what
// deferring the check bought.
func (s *Service) Decide(ctx context.Context, in Deciding) (Decision, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Decision{}, err
	}
	facility := actor.FacilityID()
	now := s.now()

	switch in.Kind {
	case DecisionAccepted, DecisionEdited, DecisionRejected:
	default:
		return Decision{}, ErrUnknownDecision
	}
	in.ReasonCode = strings.TrimSpace(in.ReasonCode)
	in.Note = strings.TrimSpace(in.Note)
	if in.Kind != DecisionRejected && (in.ReasonCode != "" || in.Note != "") {
		// Refused rather than dropped. A client sending a rejection reason with an acceptance has
		// misunderstood which button was pressed, and absorbing it would put a reason nobody meant
		// into the one signal §5 exists to collect.
		return Decision{}, ErrReasonBelongsToRejection
	}
	if len([]rune(in.Note)) > MaxDecisionNote {
		in.Note = string([]rune(in.Note)[:MaxDecisionNote])
	}
	if in.ReasonCode != "" {
		known, err := s.store.knownRejectReason(ctx, in.ReasonCode)
		if err != nil {
			return Decision{}, err
		}
		if !known {
			return Decision{}, ErrUnknownReason
		}
	}

	sheet, err := s.store.ByID(ctx, in.PrescriptionID, facility)
	if err != nil {
		return Decision{}, err
	}
	if !sheet.Status.Editable() {
		// A rejection is refused here too, and deliberately. A decision recorded against a sheet
		// that has left DRAFT is a decision taken after the moment it could have meant anything,
		// which is the shape of a back-dated note.
		return Decision{}, ErrNotEditable
	}

	offered, err := s.store.suggestion(ctx, in.SuggestionID, in.PrescriptionID, facility)
	if err != nil {
		return Decision{}, err
	}
	existing, err := s.store.decisionOf(ctx, offered.ID)
	if err != nil {
		return Decision{}, err
	}
	if existing != nil {
		return Decision{}, ErrAlreadyDecided
	}

	issued, changed := offered.applying(in.Edit)
	if in.Kind == DecisionEdited && !changed {
		return Decision{}, ErrEditChangesNothing
	}
	if in.Kind == DecisionAccepted {
		// An acceptance produces *"a prescription line identical to the suggestion"* (§4). Any
		// edit fields on an acceptance are ignored rather than applied — the line is the
		// suggestion, and a caller that wanted otherwise should have said EDITED.
		issued = offered.asOffered()
	}

	decision := Decision{
		ID: uuid.New(), SuggestionID: offered.ID, Kind: in.Kind,
		DecidedBy: actor.UserID(), DecidedAt: now,
		ReasonCode: in.ReasonCode, Note: in.Note,
	}
	if in.EventID == uuid.Nil {
		in.EventID = uuid.New()
	}

	if in.Kind == DecisionRejected {
		if err := s.rejectTx(ctx, actor, sheet, offered, &decision, in, now); err != nil {
			return Decision{}, err
		}
		return decision, nil
	}
	if err := s.acceptTx(ctx, actor, sheet, offered, issued, &decision, in, now); err != nil {
		return Decision{}, err
	}
	return decision, nil
}

// acceptTx writes the line and the decision together.
func (s *Service) acceptTx(ctx context.Context, actor eventstore.Actor, sheet Prescription,
	offered Suggestion, issued issuedLine, decision *Decision, in Deciding, now time.Time) error {

	itemID := uuid.New()
	decision.ItemID = &itemID

	eventType := "AI_SUGGESTION_ACCEPTED"
	if in.Kind == DecisionEdited {
		eventType = "AI_SUGGESTION_EDITED"
	}

	return s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries) error {
		// The line. `addItem` is the same function every hand-typed line goes through — same
		// price capture, same validation, same event — with one argument set: the suggestion it
		// came from. There is no second way to write a line, which is what keeps CP80's rules
		// from having a CP82-shaped hole in them.
		if _, err := s.addItemTx(ctx, tx, q, actor, Addition{
			EventID: in.EventID, PrescriptionID: sheet.ID,
			ProductID: &offered.ProductID,
			Dose:      issued.Dose, Frequency: issued.Frequency,
			DurationDays: issued.DurationDays, Route: issued.Route,
			InstructionsEN: in.Edit.InstructionsEN, InstructionsBN: in.Edit.InstructionsBN,
			LedgerSource: in.LedgerSource,
		}, itemID, &offered.ID, now); err != nil {
			return err
		}

		// The decision event. Its own event, on the prescription's stream, so that "what happened
		// to this sheet" reads as a sequence of acts by people rather than as a line appearing
		// from nowhere.
		if err := s.appendInTx(ctx, tx, uuid.New(), eventType, sheet.ID,
			&sheet.PatientID, &sheet.VisitID, actor, in.LedgerSource, now,
			decidedPayload(sheet, offered, *decision, issued)); err != nil {
			return err
		}

		// The row the deferred trigger is about to look for.
		return s.store.insertDecision(ctx, tx, sheet.FacilityID, *decision, in.EventID)
	})
}

// rejectTx records a decision that produces nothing.
func (s *Service) rejectTx(ctx context.Context, actor eventstore.Actor, sheet Prescription,
	offered Suggestion, decision *Decision, in Deciding, now time.Time) error {

	return s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbgen.Queries) error {
		if err := s.appendInTx(ctx, tx, in.EventID, "AI_SUGGESTION_REJECTED", sheet.ID,
			&sheet.PatientID, &sheet.VisitID, actor, in.LedgerSource, now,
			decidedPayload(sheet, offered, *decision, issuedLine{})); err != nil {
			return err
		}
		return s.store.insertDecision(ctx, tx, sheet.FacilityID, *decision, in.EventID)
	})
}

// decidedPayload is what goes in the ledger.
//
// **Both halves travel.** §4: *"If the system records only the final line, the fact that the model
// said 500 mg and the physician wrote 850 mg is lost."* The suggestion row already holds the offer
// and cannot be edited, so this is the second copy — in the one store nobody can rewrite at all,
// and the one a medico-legal review reads years later without joining to anything.
func decidedPayload(sheet Prescription, offered Suggestion, decision Decision,
	issued issuedLine) eventstore.AIPrescribingSuggestionDecided {

	out := eventstore.AIPrescribingSuggestionDecided{
		FacilityID:     sheet.FacilityID.String(),
		PatientID:      sheet.PatientID.String(),
		VisitID:        sheet.VisitID.String(),
		PrescriptionID: sheet.ID.String(),
		SuggestionID:   offered.ID.String(),
		RunID:          offered.RunID.String(),
		AgentCode:      AgentCode,
		Decision:       string(decision.Kind),

		OfferedProductID: offered.ProductID.String(),
		OfferedLabel:     offered.Label,
		OfferedGeneric:   offered.GenericName,
		OfferedDose:      offered.Dose,
		OfferedFrequency: offered.Frequency,
		OfferedRoute:     offered.Route,

		RejectReasonCode: decision.ReasonCode,
		RejectNote:       decision.Note,
		DecidedAt:        decision.DecidedAt,
	}
	if offered.DurationDays != nil {
		out.OfferedDurationDays = *offered.DurationDays
	}
	if decision.ItemID != nil {
		out.ItemID = decision.ItemID.String()
		out.IssuedDose = issued.Dose
		out.IssuedFrequency = issued.Frequency
		out.IssuedRoute = issued.Route
		if issued.DurationDays != nil {
			out.IssuedDurationDays = *issued.DurationDays
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// The offered line and the issued one
// ---------------------------------------------------------------------------

// issuedLine is what actually goes onto the prescription.
//
// A type of its own, unexported, so that the suggestion and the line the physician issued cannot
// be passed to each other's functions by accident. They have the same fields and they mean
// different things, and §4 is entirely about not confusing them.
type issuedLine struct {
	Dose         string
	Frequency    string
	DurationDays *int
	Route        string
}

// asOffered is the suggestion, unchanged. What an acceptance issues.
func (s Suggestion) asOffered() issuedLine {
	return issuedLine{Dose: s.Dose, Frequency: s.Frequency,
		DurationDays: s.DurationDays, Route: s.Route}
}

// applying overlays the physician's edits and reports whether anything actually changed.
//
// **It returns a new value and does not touch the receiver.** The suggestion is stored as offered
// and is never mutated — the database refuses an UPDATE, and this is the Go side of the same rule:
// there is no method on [Suggestion] with a pointer receiver anywhere in this package.
func (s Suggestion) applying(edit EditedLine) (issuedLine, bool) {
	out := s.asOffered()
	changed := false
	if edit.Dose != nil && strings.TrimSpace(*edit.Dose) != "" && strings.TrimSpace(*edit.Dose) != s.Dose {
		out.Dose, changed = strings.TrimSpace(*edit.Dose), true
	}
	if edit.Frequency != nil && strings.TrimSpace(*edit.Frequency) != "" &&
		strings.TrimSpace(*edit.Frequency) != s.Frequency {
		out.Frequency, changed = strings.TrimSpace(*edit.Frequency), true
	}
	if edit.Route != nil && strings.TrimSpace(*edit.Route) != s.Route {
		out.Route, changed = strings.TrimSpace(*edit.Route), true
	}
	if edit.DurationDays != nil && (s.DurationDays == nil || *edit.DurationDays != *s.DurationDays) {
		days := *edit.DurationDays
		out.DurationDays, changed = &days, true
	}
	if strings.TrimSpace(edit.InstructionsEN) != "" || strings.TrimSpace(edit.InstructionsBN) != "" {
		// Instructions are not part of the suggestion — the model does not write patient
		// instructions, CP81's approved templates do — so adding one is a change to the line
		// without being a disagreement with the model. It still counts as an edit, because the
		// line the patient receives differs from the one that was offered.
		changed = true
	}
	return out, changed
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

// SuggestionsFor is the panel's read: the newest run for one draft, or the never-asked state.
//
// Never a 404. Every state is a 200 with a name and a sentence, for D-15's reason and for CP71's:
// an empty panel tells a physician nothing about whether the system tried.
func (s *Service) SuggestionsFor(ctx context.Context, prescriptionID, facility uuid.UUID) (Run, error) {
	run, err := s.store.CurrentRun(ctx, prescriptionID, facility)
	if errors.Is(err, ErrNotFound) {
		return describe(Run{PrescriptionID: prescriptionID, State: "", Suggestions: []Suggestion{}}), nil
	}
	if err != nil {
		return Run{}, err
	}
	return describe(run), nil
}

// describe fills in the two sentences the panel shows.
//
// Bilingual, and each one says what happened rather than that something did. "No suggestions" and
// "the model was not asked because this patient has no allergy status recorded" are the same empty
// column and completely different facts, and the second is one a physician can act on in thirty
// seconds by sending the patient back to the history station.
func describe(run Run) Run {
	if run.Suggestions == nil {
		run.Suggestions = []Suggestion{}
	}
	switch {
	case run.State == "":
		run.MessageEN = "The AI has not been asked to suggest anything for this prescription."
		run.MessageBN = "এই ব্যবস্থাপত্রের জন্য এআই-এর কাছে কোনো প্রস্তাব চাওয়া হয়নি।"
	case run.State == RunRefused && run.Refusal == RefusalNoAllergyStatus:
		run.MessageEN = "No suggestion was asked for: this patient has no recorded allergy status. " +
			"Record it at the history station first — nothing is proposed around that stop."
		run.MessageBN = "কোনো প্রস্তাব চাওয়া হয়নি: এই রোগীর অ্যালার্জির অবস্থা নথিতে নেই। " +
			"আগে ইতিহাস কেন্দ্রে সেটি লিখুন — ওই বাধা এড়িয়ে কিছু প্রস্তাব করা হয় না।"
	case run.State == RunRefused && run.Refusal == RefusalNotADraft:
		run.MessageEN = "Suggestions are offered only while a prescription is a draft."
		run.MessageBN = "ব্যবস্থাপত্র খসড়া অবস্থায় থাকাকালীনই কেবল প্রস্তাব দেওয়া হয়।"
	case run.State == RunRefused:
		run.MessageEN = "There was nothing in this clinic's formulary for the AI to propose."
		run.MessageBN = "এই ক্লিনিকের ওষুধতালিকায় এআই-এর প্রস্তাব করার মতো কিছু ছিল না।"
	case run.State == RunFailed:
		run.MessageEN = "The AI could not be asked, or its answer was refused. Prescribe as usual; " +
			"nothing here is waiting on it."
		run.MessageBN = "এআই-কে জিজ্ঞাসা করা যায়নি, অথবা তার উত্তর গ্রহণ করা হয়নি। " +
			"স্বাভাবিকভাবেই ব্যবস্থাপত্র লিখুন; এর জন্য কিছু আটকে নেই।"
	case len(run.Suggestions) == 0:
		run.MessageEN = "The AI proposed nothing for this patient. That is an answer, not a failure."
		run.MessageBN = "এই রোগীর জন্য এআই কিছু প্রস্তাব করেনি। এটি একটি উত্তর, ব্যর্থতা নয়।"
	default:
		run.MessageEN = "AI-proposed, and not prescribed. Each one needs your accept, edit or reject."
		run.MessageBN = "এআই-এর প্রস্তাব, ব্যবস্থাপত্র নয়। প্রতিটির জন্য আপনার গ্রহণ, সংশোধন বা বাতিল প্রয়োজন।"
	}
	return run
}

// ---------------------------------------------------------------------------
// Small shared pieces
// ---------------------------------------------------------------------------

// asObjects reads a JSON array of objects out of a model answer, tolerating anything else.
//
// Tolerating rather than erroring: the gateway has already validated the answer against the
// agent's schema, so a shape that reaches here and is wrong is a schema and a prompt that disagree
// — which is a defect worth a drop count rather than a five-hundred on a physician's screen.
func asObjects(v any) []map[string]any {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if object, ok := item.(map[string]any); ok {
			out = append(out, object)
		}
	}
	return out
}

func text(v any) string {
	s, _ := v.(string)
	return s
}

// wholeNumber reads an integer from a model answer, which decodes numbers as json.Number.
func wholeNumber(v any) (int, bool) {
	switch typed := v.(type) {
	case json.Number:
		n, err := typed.Int64()
		return int(n), err == nil
	case float64:
		return int(typed), typed == float64(int(typed))
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(typed))
		return n, err == nil
	}
	return 0, false
}

// basisOf reads the fact references a suggestion rests on.
func basisOf(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	sort.Strings(out)
	return out
}
