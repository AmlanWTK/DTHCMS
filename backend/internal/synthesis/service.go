package synthesis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/ai"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// The service: who asks for a summary, and what happens when one is produced (CP71).
//
// # The trigger model, which is acceptance criterion 2
//
// §7.1 gives the button to the last assistant in the flow and then says the physician *"by design
// never needs to"* press it. So the button is the fallback and the automatic path is primary, and
// the automatic path fires from the one event that means "the stations are done with this patient":
// a station touch ending.
//
// **What "all stations complete" means is not invented here.** `core.station_sequence` already holds
// the planned journey per visit type with a `required` flag per step — it is the clinic's own
// operational decision, and a second definition of completeness living in Go would drift from it the
// first morning somebody reordered the rooms. Completeness is therefore: *every required station in
// this visit type's plan, at a position before the consultation, has a finished encounter*.
//
// # Why the enqueue happens in the departing operator's own transaction
//
// [Queue] is an interface rather than an import for the same reason `visit.Notifier` is: this module
// may not import `jobs`, and the queue may not import a clinical module. What it buys is the
// property CP69 built its whole design around — `EnqueueTx` takes the caller's transaction, so a
// station touch that rolls back cannot leave a synthesis job behind that briefs a physician about a
// visit nobody is at.

// Queue is the job queue, seen from here.
//
// One method, and it names the work rather than taking a kind and a payload: a caller that could
// pass any kind would be a caller that could enqueue anything, and this module's business is one
// kind.
type Queue interface {
	// EnqueueSynthesis puts one run on the queue inside the caller's transaction.
	EnqueueSynthesis(ctx context.Context, tx pgx.Tx, now time.Time,
		args JobArgs, dedupeKey string) (Enqueued, error)
}

// JobArgs is what the queue carries.
//
// Ids and a count. Invariant 90 refuses a job argument that holds a name, a number a clinician typed
// or a clinical value, and this struct could not carry one if somebody tried: everything the handler
// needs it reads from the database under the run's own id.
type JobArgs struct {
	SynthesisID uuid.UUID `json:"synthesis_id"`
	VisitID     uuid.UUID `json:"visit_id"`
	Generation  int       `json:"generation"`
}

// Enqueued is what the queue reports back.
type Enqueued struct {
	JobID uuid.UUID
	// SLADeadline is the queue's, stamped from `ops.job_kind.sla_seconds`. Copied onto the run so
	// that §7.1's promise is measured against one clock — the queue's — rather than against
	// whichever process was asked.
	SLADeadline *time.Time
}

// Service is the agent.
type Service struct {
	store    *Store
	stations Stations
	patients *patient.Store
	gateway  *ai.Gateway
	queue    Queue
	events   *eventstore.Store
	clock    Clock
	logger   *slog.Logger
}

// Clock is the small slice of time this package needs.
type Clock interface{ Now() time.Time }

// ServiceConfig builds a Service.
type ServiceConfig struct {
	Store    *Store
	Stations Stations
	Patients *patient.Store
	Gateway  *ai.Gateway
	Queue    Queue
	Events   *eventstore.Store
	Clock    Clock
	Logger   *slog.Logger
}

// NewService builds one.
func NewService(cfg ServiceConfig) *Service {
	return &Service{
		store: cfg.Store, stations: cfg.Stations, patients: cfg.Patients,
		gateway: cfg.Gateway, queue: cfg.Queue, events: cfg.Events,
		clock: cfg.Clock, logger: cfg.Logger,
	}
}

// Requesting is one ask for a summary.
type Requesting struct {
	VisitID uuid.UUID
	Trigger Trigger
	// EventID makes the ledger append idempotent: the same request retried by an offline client
	// produces one event, because the ledger absorbs a repeat of an id it has already seen.
	EventID uuid.UUID
	Source  eventstore.Source
}

// Request asks for a summary and returns the run that will produce it.
//
// It opens its own transaction. The hook path does not — see [Service.EncounterFinished] — because
// there the decision was made inside somebody else's write and has to live or die with it.
func (s *Service) Request(ctx context.Context, in Requesting) (Run, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Run{}, err
	}
	var out Run
	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries) error {
		run, err := s.requestTx(ctx, tx, q, actor, in)
		out = run
		return err
	})
	if errors.Is(err, ErrAlreadyRunning) {
		// Not a failure. Somebody asked for a summary and one is on its way; the caller wants the
		// run, and a refusal here would make a physician press the button twice.
		return out, nil
	}
	if err != nil {
		return Run{}, err
	}
	return out, nil
}

// requestTx creates the run and its job inside the caller's transaction.
func (s *Service) requestTx(ctx context.Context, tx pgx.Tx, q *dbgen.Queries,
	actor eventstore.Actor, in Requesting) (Run, error) {

	facility := actor.FacilityID()
	current, err := s.stations.Visits.ByID(ctx, in.VisitID, facility)
	if err != nil {
		return Run{}, ErrNotFound
	}
	if current.Status != visit.Open {
		return Run{}, fmt.Errorf("%w: it is %s", ErrVisitNotOpen, current.Status)
	}

	// One run in flight per visit. Two stations finishing in the same second, or an operator
	// pressing the button twice, must not queue two model calls for one patient — and collapsing
	// them here rather than at the queue's dedupe key means the second caller gets the run that is
	// actually going to answer them.
	existing, err := s.store.Current(ctx, in.VisitID, facility)
	hadPrevious := err == nil
	switch {
	case err != nil && !errors.Is(err, ErrNotFound):
		return Run{}, err
	case hadPrevious && (existing.State == Pending || existing.State == Running):
		return existing, ErrAlreadyRunning
	}

	// The context is assembled at request time as well as at run time, and both are deliberate.
	// Here it gives the row something to show immediately: a physician who opens the file while the
	// job is still queued sees the structured record rather than an empty panel, which is most of
	// D-15's degraded state. At run time it is assembled again because the record may have moved on
	// in the seconds between, and the summary must describe the record as it is when the model sees
	// it rather than as it was when somebody pressed a button.
	//
	// On the hook path this first assembly is very slightly stale by construction: it reads through
	// the pool while the station touch that triggered it is still uncommitted, so it shows that
	// station as in progress. That is a placeholder for a screen opened in the next few seconds and
	// never the context a summary is written from — the worker re-assembles — so the staleness costs
	// nothing and paying for a transaction-scoped read of seven modules to avoid it would.
	assembled, err := s.assemble(ctx, in.VisitID, facility)
	if err != nil {
		return Run{}, err
	}
	encoded, err := json.Marshal(assembled)
	if err != nil {
		return Run{}, err
	}

	runID := uuid.New()
	now := s.clock.Now().UTC()
	// The generation is computed again by the INSERT, from `max(generation) + 1` under the unique
	// index, and that is the one that decides. This copy exists only for the dedupe key and the
	// event payload, and it is allowed to lose a race: two callers computing the same number means
	// one INSERT wins and the other is refused, which is the outcome this design wants.
	generation := 1
	if hadPrevious {
		generation = existing.Generation + 1
	}

	enqueued, err := s.queue.EnqueueSynthesis(ctx, tx, now, JobArgs{
		SynthesisID: runID, VisitID: in.VisitID, Generation: generation,
	}, "synthesis:"+in.VisitID.String()+":"+strconv.Itoa(generation))
	if err != nil {
		return Run{}, err
	}

	by := actor.UserID()
	seed := Run{
		ID: runID, VisitID: in.VisitID, PatientID: current.PatientID,
		State: Pending, Trigger: in.Trigger, RequestedAt: now,
		SLADeadline: enqueued.SLADeadline, facility: facility,
	}
	created, err := s.store.insert(ctx, q, seed, encoded, assembled.MaterialSHA256(),
		&enqueued.JobID, &by)
	if err != nil {
		return Run{}, err
	}

	if err := s.append(ctx, tx, actor, current, created, "AI_SYNTHESIS_REQUESTED",
		eventstore.AISynthesisRequested{
			FacilityID: facility.String(), PatientID: current.PatientID.String(),
			VisitID: in.VisitID.String(), SynthesisID: runID.String(),
			AgentCode: AgentCode, Trigger: string(in.Trigger),
			Generation: generation, RequestedAt: now,
		}, in.EventID, in.Source, now); err != nil {
		return Run{}, err
	}
	return created, nil
}

// --- the automatic trigger ---

// EncounterFinished is `visit`'s hook, called inside the transaction that finished the station
// touch. It implements [visit.StationHook].
//
// # Why an error here does not fail the departure
//
// A station touch is a clinical fact and a summary is a convenience. D-15's rule is that the clinic
// never stops because AI stopped, and refusing to record that a patient left the examination room
// because a queue insert failed would be exactly that failure. So the caller runs this inside a
// savepoint and rolls back to it on error — see `visit.Service.Depart` — and the physician's screen
// then reports `NOT_REQUESTED` with a button, which is the honest state.
func (s *Service) EncounterFinished(ctx context.Context, tx pgx.Tx, touch visit.StationTouch) error {
	if touch.Status != visit.Finished {
		// A bounce is a station sending the patient back, not finishing with them. Summarising at
		// that moment would brief the physician on a visit that is about to change.
		return nil
	}
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return err
	}
	facility := actor.FacilityID()

	current, err := s.stations.Visits.ByID(ctx, touch.VisitID, facility)
	if err != nil || current.Status != visit.Open {
		return nil
	}
	planned, err := s.stations.Visits.Planned(ctx, facility, current.VisitType)
	if err != nil {
		return err
	}
	// Only stations before the consultation trigger anything. The physician's own encounter
	// finishing, or the QA officer's, is not new information for a briefing that has already been
	// read — and a re-run fired by the consultation ending would change the page underneath the
	// person reading it.
	if !beforeConsultation(planned, touch.StationCode) {
		return nil
	}

	encounters, err := s.stations.Visits.Encounters(ctx, touch.VisitID, facility)
	if err != nil {
		return err
	}
	// The touch is added to what was read, and this is not belt and braces — it is required for
	// correctness. This hook runs *inside* the transaction that finished the encounter, and the
	// station stores read through the pool, so the row this hook was called about still reads as
	// `in_progress` to every query outside that transaction. Without the line below, completeness
	// would never be reached by the last station: the trigger would fire one station late, or on a
	// visit type where the last one is optional, never.
	//
	// The alternative — giving every station store a transaction-scoped twin so the hook could read
	// its own caller's uncommitted state — is a large change to seven modules to learn one fact the
	// caller already handed us.
	encounters = append(encounters, visit.Encounter{
		ID: touch.EncounterID, VisitID: touch.VisitID, StationCode: touch.StationCode,
		Status: touch.Status, StartedAt: touch.At, Outcome: touch.Outcome,
	})
	complete := preConsultationComplete(planned, encounters)

	existing, err := s.store.Current(ctx, touch.VisitID, facility)
	switch {
	case errors.Is(err, ErrNotFound):
		// Nothing has ever been asked for. The first run waits for the stations to be done, which
		// is what §7.1 means by the last assistant pressing the button — the automatic path fires
		// at the same moment for the same reason.
		if !complete {
			return nil
		}
		_, err = s.requestTx(ctx, tx, dbgen.New(tx), actor, Requesting{
			VisitID: touch.VisitID, Trigger: Automatic,
			EventID: uuid.New(), Source: eventstore.SourceSystem,
		})
	case err != nil:
		return err
	case existing.State == Pending || existing.State == Running:
		// Something is already coming. It will assemble at run time and pick this touch up.
		return nil
	default:
		// A summary exists and a station before the consultation has finished again — a bounce, a
		// re-measurement, a station revisited. Whether that is *material* is decided by the
		// assembler, in the worker, by comparing the hash: a run that finds nothing new records
		// UNCHANGED and never contacts a model. Deciding it here would mean assembling the whole
		// context inside somebody else's write transaction to answer a question that is usually
		// "no".
		_, err = s.requestTx(ctx, tx, dbgen.New(tx), actor, Requesting{
			VisitID: touch.VisitID, Trigger: Rerun,
			EventID: uuid.New(), Source: eventstore.SourceSystem,
		})
	}
	if errors.Is(err, ErrAlreadyRunning) {
		return nil
	}
	return err
}

// beforeConsultation reports whether a station comes before the physician in this plan.
func beforeConsultation(planned []visit.PlannedStation, station string) bool {
	consultation := -1
	position := -1
	for _, step := range planned {
		if step.StationCode == ConsultationStation {
			consultation = step.Position
		}
		if step.StationCode == station {
			position = step.Position
		}
	}
	if position < 0 {
		return false
	}
	if consultation < 0 {
		// A plan with no consultation in it is a plan this checkpoint has nothing to say about.
		return false
	}
	return position < consultation
}

// preConsultationComplete is the definition of "all stations complete", and it is the station
// sequence's own.
func preConsultationComplete(planned []visit.PlannedStation, encounters []visit.Encounter) bool {
	finished := map[string]bool{}
	for _, enc := range encounters {
		if enc.Status == visit.Finished {
			finished[enc.StationCode] = true
		}
	}
	consultation := -1
	for _, step := range planned {
		if step.StationCode == ConsultationStation {
			consultation = step.Position
		}
	}
	if consultation < 0 {
		return false
	}
	for _, step := range planned {
		if step.Position >= consultation || !step.Required {
			continue
		}
		if !finished[step.StationCode] {
			return false
		}
	}
	return true
}

// --- performing the run ---

// Perform does one queued run. The worker's handler is a thin wrapper around this.
//
// It is idempotent, which at-least-once delivery requires: a run another worker has already claimed
// returns nil rather than restarting, and a run that has already finished is not re-finished.
func (s *Service) Perform(ctx context.Context, actor eventstore.Actor, args JobArgs) error {
	if s.gateway == nil {
		// The API process builds this service without a gateway on purpose: it decides that a
		// summary is warranted and queues it, and never contacts a model. That separation is the
		// reason `cmd/worker` exists — a burst of AI work must not slow down a clinician entering a
		// blood pressure — so a run reaching this line in the wrong process is a wiring fault, and
		// it says so rather than panicking on a nil pointer three frames further in.
		return errors.New("synthesis: this process has no AI gateway; runs are performed by the worker")
	}
	facility := actor.FacilityID()
	now := s.clock.Now().UTC()

	run, err := s.store.claim(ctx, args.SynthesisID, now)
	if errors.Is(err, ErrClaimed) {
		s.logger.InfoContext(ctx, "a synthesis run was already claimed; nothing to do",
			"synthesis_id", args.SynthesisID.String())
		return nil
	}
	if err != nil {
		return err
	}

	current, err := s.stations.Visits.ByID(ctx, run.VisitID, facility)
	if err != nil {
		return s.fail(ctx, actor, run, visit.Visit{}, FailureAssembly, "the visit could not be read")
	}

	assembled, err := s.assemble(ctx, run.VisitID, facility)
	if err != nil {
		return s.fail(ctx, actor, run, current, FailureAssembly, err.Error())
	}

	// The re-run decision, in one comparison. See [Context.MaterialSHA256] for what "material"
	// means and why it is decided there.
	if previous, found := s.lastReady(ctx, run); found && !previous.Stale(assembled) {
		return s.settle(ctx, actor, run, current, finishing{
			state: Unchanged, at: s.clock.Now().UTC(),
			contextJSON: mustJSON(assembled), material: assembled.MaterialSHA256(),
			detail: "the record has not changed materially since generation " +
				strconv.Itoa(previous.Generation),
		})
	}

	subject, err := s.subjectFor(ctx, current.PatientID, facility, assembled.Demographics)
	if err != nil {
		return s.fail(ctx, actor, run, current, FailureInternal, err.Error())
	}

	payload, err := payloadOf(assembled)
	if err != nil {
		return s.fail(ctx, actor, run, current, FailureAssembly, err.Error())
	}

	result, err := s.gateway.Invoke(ctx, ai.Request{
		AgentCode: AgentCode, Subject: subject, Payload: payload,
	})
	if err != nil {
		return s.failFromGateway(ctx, actor, run, current, err)
	}

	output, err := json.Marshal(result.Output)
	if err != nil {
		return s.fail(ctx, actor, run, current, FailureInternal, err.Error())
	}
	interaction := result.InteractionID
	return s.settle(ctx, actor, run, current, finishing{
		state: Ready, at: s.clock.Now().UTC(),
		contextJSON: mustJSON(assembled), material: assembled.MaterialSHA256(),
		output: output, interaction: &interaction,
		promptVersion: result.PromptVersion, modelVersion: result.ModelVersion,
		// The gateway's verdict, carried across rather than recomputed. A [ai.Result] only ever
		// exists with a passing verdict — a failing one is `ai.ErrUngrounded` and was handled in
		// the branch above — so this is a record of a decision already taken, and the assertion in
		// [Service.settle] is what notices if that ever stops being true.
		grounding:         groundingOf(result.Grounding.State),
		groundingFindings: len(result.Grounding.Findings),
	})
}

// groundingOf maps the gateway's verdict on to the one this row records.
//
// Two vocabularies rather than one, and it is worth saying why the mapping is not the identity.
// The gateway has a NOT_REQUIRED state for an agent `core.ai_agent` exempts; `core.ai_synthesis`
// has no such value and its check constraint accepts only PASSED for a readable summary. So an
// exemption granted on the synthesis agent arrives here as NOT_CHECKED and the row is refused by
// the database — loudly, on the first run, rather than by quietly showing a physician an unchecked
// page. That is the third of the three independent enforcements, and it only works because this
// function refuses to translate an exemption into a pass.
func groundingOf(state ai.GroundingState) GroundingState {
	if state == ai.GroundingPassed {
		return GroundingPassed
	}
	if state == ai.GroundingFailed {
		return GroundingFailed
	}
	return GroundingNotChecked
}

// assemble is Gather then Assemble: the two halves, in the one place that runs both.
func (s *Service) assemble(ctx context.Context, visitID, facility uuid.UUID) (Context, error) {
	current, err := s.stations.Visits.ByID(ctx, visitID, facility)
	if err != nil {
		return Context{}, ErrNotFound
	}
	person, err := s.patients.ByID(ctx, current.PatientID, facility)
	if err != nil {
		return Context{}, err
	}
	now := s.clock.Now().UTC()
	who := Demographics{
		AgeMonths: ageMonths(person.Birth.Date, now),
		Sex:       string(person.Sex),
	}
	who.AgeText = ageText(who.AgeMonths)

	raw, err := Gather(ctx, s.stations, visitID, facility, who, func(section string, err error) {
		// A station read that failed costs its section and is logged, not returned. The context
		// then has a hole in it, and the hole is visible to the physician as a missing section
		// rather than as a summary that never arrived.
		s.logger.WarnContext(ctx, "a station could not be read while assembling a synthesis",
			"section", section, "visit_id", visitID.String(), "error", err.Error())
	})
	if err != nil {
		return Context{}, err
	}
	return Assemble(raw, now), nil
}

// subjectFor builds the gateway's subject: the identifiers to strike out and put back.
//
// This is the only place in this package that reads a patient's name, and it hands it straight to
// the one component whose job is to keep it inside the building. The labels are the gateway's own
// vocabulary; a label the minimiser does not recognise is still substituted, so adding one here is
// safe and forgetting one costs the exact match for that string rather than the whole guarantee.
func (s *Service) subjectFor(ctx context.Context, patientID, facility uuid.UUID,
	who Demographics) (ai.Subject, error) {

	person, err := s.patients.ByID(ctx, patientID, facility)
	if err != nil {
		return ai.Subject{}, err
	}
	identifiers := map[string]string{}
	for label, value := range map[string]string{
		"name_en": person.NameEN,
		"name_bn": person.NameBN,
		"phone":   person.PhonePrimary,
	} {
		if value != "" {
			identifiers[label] = value
		}
	}
	return ai.Subject{
		PatientID: patientID, AgeMonths: who.AgeMonths, Sex: who.Sex,
		Identifiers: identifiers,
	}, nil
}

// lastReady is the newest run before this one that actually produced a summary.
//
// Not simply "the previous generation": a failed run in between must not make the next one conclude
// the record is unchanged and skip the model, which would leave the physician looking at a summary
// two generations old with nothing saying so.
func (s *Service) lastReady(ctx context.Context, run Run) (Run, bool) {
	history, err := s.store.History(ctx, run.VisitID, run.facility)
	if err != nil {
		return Run{}, false
	}
	for _, candidate := range history {
		if candidate.Generation >= run.Generation {
			continue
		}
		if candidate.State == Ready {
			return candidate, true
		}
	}
	return Run{}, false
}

// failFromGateway turns a gateway error into a terminal run, carrying across whatever the error
// knows that a string does not.
//
// The one thing it knows and a string does not is *how many* claims failed. That number is on the
// row a physician's screen reads, next to the sentence explaining the withholding, and it is what
// tells somebody opening the defect queue how many rows to expect there. Parsing it back out of
// the error text would work and would break the first time the sentence changed.
func (s *Service) failFromGateway(ctx context.Context, actor eventstore.Actor, run Run,
	current visit.Visit, cause error) error {

	kind := classify(cause)
	var ungrounded *ai.UngroundedError
	if errors.As(cause, &ungrounded) {
		return s.settle(ctx, actor, run, current, finishing{
			state: Failed, at: s.clock.Now().UTC(),
			contextJSON: mustJSON(run.Context), material: run.Context.MaterialSHA256(),
			kind: kind, detail: cause.Error(),
			grounding: GroundingFailed, groundingFindings: len(ungrounded.Report.Findings),
		})
	}
	return s.fail(ctx, actor, run, current, kind, cause.Error())
}

func (s *Service) fail(ctx context.Context, actor eventstore.Actor, run Run, current visit.Visit,
	kind FailureKind, detail string) error {

	// The context that was assembled at request time is kept: a failed run still shows the
	// physician what the system knew, which is the whole of D-15's *"raw structured data shown"*.
	//
	// A grounding refusal records the verdict as well as the kind. The two say different things
	// and a screen needs both: the kind is why there is no summary, and the verdict is the reason
	// this row can never become one. The finding *count* lives on `core.ai_interaction` and in the
	// defect queue rather than being guessed at from an error string.
	out := finishing{
		state: Failed, at: s.clock.Now().UTC(),
		contextJSON: mustJSON(run.Context), material: run.Context.MaterialSHA256(),
		kind: kind, detail: detail,
	}
	if kind == FailureUngrounded {
		out.grounding = GroundingFailed
	}
	return s.settle(ctx, actor, run, current, out)
}

// settle writes the terminal row and the event, and never returns the queue an error for a run that
// finished.
//
// The distinction matters for the retry policy. A run that failed because the model was unreachable
// is finished as far as this table is concerned — the row says FAILED and the screen degrades — but
// the *job* should retry, because the next attempt may reach the model. So a provider failure
// returns the error to the queue after the row is written, and everything else returns nil.
func (s *Service) settle(ctx context.Context, actor eventstore.Actor, run Run,
	current visit.Visit, in finishing) error {

	// **The third enforcement, in Go, in this module.** The gateway already refuses to return an
	// ungrounded answer and the database already refuses to store a READY row without a passing
	// verdict; this is the one that catches a future caller inside this package assembling a
	// `finishing` by hand and forgetting to carry the verdict across from the result.
	//
	// It converts rather than panics, because a physician's screen is the thing at stake: the run
	// becomes a visible, explained failure with the structured record underneath it, which is what
	// D-15 asks for, rather than a five-hundred from a worker nobody is watching.
	if in.state == Ready && in.grounding != GroundingPassed {
		s.logger.ErrorContext(ctx, "a summary reached the terminal write without a passing grounding verdict; withholding it",
			"synthesis_id", run.ID.String(), "grounding_state", string(in.grounding))
		in = finishing{
			state: Failed, at: in.at, contextJSON: in.contextJSON, material: in.material,
			kind: FailureUngrounded, grounding: in.grounding,
			groundingFindings: in.groundingFindings,
			detail: "the summary was not accompanied by a passing grounding verdict and was withheld; " +
				"this is a defect in the pipeline rather than in the model's answer",
		}
	}

	finished, err := s.store.finish(ctx, run.ID, in)
	if errors.Is(err, ErrClaimed) {
		return nil
	}
	if err != nil {
		return err
	}

	eventType := "AI_SYNTHESIS_COMPLETED"
	var payload eventstore.Payload = eventstore.AISynthesisCompleted{
		FacilityID: run.facility.String(), PatientID: run.PatientID.String(),
		VisitID: run.VisitID.String(), SynthesisID: run.ID.String(),
		AgentCode: AgentCode, PromptVersion: finished.PromptVersion,
		ModelVersion: finished.ModelVersion, Generation: run.Generation,
		State: string(finished.State), MetSLA: finished.MetSLA, CompletedAt: in.at,
	}
	if in.state == Failed {
		eventType = "AI_SYNTHESIS_FAILED"
		payload = eventstore.AISynthesisFailed{
			FacilityID: run.facility.String(), PatientID: run.PatientID.String(),
			VisitID: run.VisitID.String(), SynthesisID: run.ID.String(),
			AgentCode: AgentCode, Generation: run.Generation,
			FailureKind: string(in.kind), FailedAt: in.at,
		}
	}
	if err := s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbgen.Queries) error {
		return s.append(ctx, tx, actor, current, finished, eventType, payload,
			// The event id is derived from the run and its outcome rather than random, so a worker
			// that dies between the row and the event and has its job replayed produces one event.
			// At-least-once delivery is the queue's honest contract; this is how a handler keeps
			// its side of it.
			deriveEventID(run.ID, eventType), eventstore.SourceSystem, in.at)
	}); err != nil {
		// The row is already written and the physician's screen is correct. An event that could
		// not be appended is a real defect and is logged at error, but re-failing the job would
		// re-run the model for a summary that already exists.
		s.logger.ErrorContext(ctx, "a synthesis finished but its event could not be appended",
			"synthesis_id", run.ID.String(), "event_type", eventType, "error", err.Error())
	}

	if in.state == Failed && retryable(in.kind) {
		return fmt.Errorf("synthesis: %s: %s", in.kind, in.detail)
	}
	return nil
}

// retryable says whether the queue should try this run again.
//
// Only the two failures another attempt could fix. An assembly fault is our defect and will fail
// identically five times; a refusal is a configuration or payload fault and will be refused
// identically; an answer that failed its schema has already been retried inside the gateway against
// the same prompt. Retrying any of those spends the SLA budget on a foregone conclusion and leaves
// the physician waiting longer for the same degraded screen.
//
// **UNGROUNDED is on the list of things not to retry, and it is the one that had to be argued
// for.** A model that invented an HbA1c would very likely not invent one on the next attempt, so a
// retry would usually "work" — and that is exactly the objection. The evidence would be gone, the
// defect record would describe an answer nobody was shown while the physician read a second answer
// nobody checked against the first, and the rate of the failure this checkpoint exists to measure
// would read as zero. CP70 retries a malformed answer because that is a transport problem; this is
// a quality problem, and quality problems are counted, not papered over.
func retryable(kind FailureKind) bool {
	return kind == FailureProvider || kind == FailureTimeout
}

// classify turns a gateway error into the sentence a physician's screen will show.
func classify(err error) FailureKind {
	switch {
	case errors.Is(err, ai.ErrFreeTierRefused), errors.Is(err, ai.ErrPayloadNamesAPerson),
		errors.Is(err, ai.ErrIdentifierSurvived):
		return FailureRefused
	case errors.Is(err, ai.ErrInvalidOutput):
		return FailureInvalidOutput
	case errors.Is(err, ai.ErrUngrounded):
		return FailureUngrounded
	case errors.Is(err, ai.ErrCircuitOpen), errors.Is(err, ai.ErrProviderUnavailable),
		errors.Is(err, ai.ErrProviderRateLimited), errors.Is(err, ai.ErrProviderRefused),
		errors.Is(err, ai.ErrProviderRejected):
		return FailureProvider
	case errors.Is(err, context.DeadlineExceeded):
		return FailureTimeout
	}
	return FailureInternal
}

func (s *Service) append(ctx context.Context, tx pgx.Tx, actor eventstore.Actor,
	current visit.Visit, run Run, eventType string, payload eventstore.Payload,
	eventID uuid.UUID, source eventstore.Source, now time.Time) error {

	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	visitID, patientID := run.VisitID, run.PatientID
	if current.ID != uuid.Nil {
		visitID, patientID = current.ID, current.PatientID
	}
	_, err = s.events.AppendInTx(ctx, tx, eventstore.Envelope{
		EventID: eventID, AggregateType: "VISIT", AggregateID: visitID,
		PatientID: &patientID, VisitID: &visitID,
		EventType: eventType, EventVersion: 1,
		OccurredAt: now, Actor: actor, Source: source, Payload: encoded,
	})
	return err
}

// --- the read the physician's screen makes ---

// View is what a client is given for one visit. It is never a 404.
//
// # This struct is acceptance criterion 4
//
// D-15 is *"fail visible, never fail silent, never fail invented"*, and the failure it is really
// about is a screen that shows nothing. A 404 for "no summary yet" makes every client render an
// empty panel, and an empty panel tells the physician nothing about whether the system tried. So
// every state is a 200 with a state name, a sentence in both languages, and — always — the
// assembled record, which is what the physician reads when there is no narrative.
type View struct {
	VisitID uuid.UUID `json:"visit_id"`
	State   State     `json:"state"`
	// AIGenerated marks the whole envelope, not just the narrative. §10.6's third permanent
	// invariant: every AI-derived element is marked wherever it appears.
	AIGenerated bool `json:"ai_generated"`
	// Degraded says the screen should show the structured record as its primary content. True for
	// everything except a summary that is ready and current.
	Degraded bool `json:"degraded"`
	// MessageEN and MessageBN are what the panel says. Bilingual because the person looking at a
	// clinic screen may be reading either — the *narrative* is English (see the package comment),
	// the interface around it is not.
	MessageEN string `json:"message_en"`
	MessageBN string `json:"message_bn"`
	// Run is absent only in NOT_REQUESTED.
	Run *Run `json:"run,omitempty"`
	// Requestable says the button should be enabled.
	Requestable bool `json:"requestable"`
}

// Current is the physician's read.
func (s *Service) Current(ctx context.Context, visitID, facility uuid.UUID) (View, error) {
	run, err := s.store.Current(ctx, visitID, facility)
	if errors.Is(err, ErrNotFound) {
		return View{
			VisitID: visitID, State: NotRequested, AIGenerated: true, Degraded: true,
			Requestable: true,
			MessageEN:   "No AI summary has been prepared for this visit. The structured record is shown.",
			MessageBN:   "এই ভিজিটের জন্য কোনো এআই সারসংক্ষেপ তৈরি হয়নি। কাঠামোবদ্ধ রেকর্ড দেখানো হচ্ছে।",
		}, nil
	}
	if err != nil {
		return View{}, err
	}
	view := View{VisitID: visitID, State: run.State, AIGenerated: true, Run: &run}
	switch run.State {
	case Pending, Running:
		view.Degraded = true
		view.MessageEN = "The AI summary is being prepared. The structured record is shown."
		view.MessageBN = "এআই সারসংক্ষেপ তৈরি হচ্ছে। কাঠামোবদ্ধ রেকর্ড দেখানো হচ্ছে।"
	case Failed:
		view.Degraded, view.Requestable = true, true
		view.MessageEN = "AI summary unavailable — the structured record is shown. " + failureSentence(run.FailureKind)
		view.MessageBN = "এআই সারসংক্ষেপ পাওয়া যায়নি — কাঠামোবদ্ধ রেকর্ড দেখানো হচ্ছে। " + failureSentenceBN(run.FailureKind)
	case Ready, Unchanged:
		view.Requestable = true
		view.MessageEN = "AI-generated draft. Every item needs the physician's review before it is used."
		view.MessageBN = "এআই-নির্মিত খসড়া। ব্যবহারের আগে প্রতিটি বিষয় চিকিৎসকের পর্যালোচনা প্রয়োজন।"
		if run.State == Unchanged {
			// An UNCHANGED run has no output of its own; the narrative on the screen is the
			// previous generation's. Saying so is the difference between a physician trusting the
			// timestamp and being misled by it.
			view.MessageEN += " The record has not changed since the last summary was prepared."
			view.MessageBN += " শেষ সারসংক্ষেপের পর রেকর্ডে গুরুত্বপূর্ণ কোনো পরিবর্তন হয়নি।"
		}
	}
	return view, nil
}

// History is every run for a visit, for the audit view.
func (s *Service) History(ctx context.Context, visitID, facility uuid.UUID) ([]Run, error) {
	return s.store.History(ctx, visitID, facility)
}

// SLA is the measurement acceptance criterion 1 is judged on.
func (s *Service) SLA(ctx context.Context, facility uuid.UUID, since, until time.Time, budget int) (SLA, error) {
	out, err := s.store.MeasureSLA(ctx, facility, since, until)
	if err != nil {
		return SLA{}, err
	}
	out.BudgetSeconds = budget
	return out, nil
}

func failureSentence(kind FailureKind) string {
	switch kind {
	case FailureAssembly:
		return "The clinical context could not be assembled; this is a defect and has been logged."
	case FailureRefused:
		return "The request was refused before it was sent, to protect patient data."
	case FailureProvider:
		return "The model could not be reached."
	case FailureTimeout:
		return "The model did not answer in time."
	case FailureInvalidOutput:
		return "The model's answer did not match the expected shape and was discarded rather than shown."
	case FailureUngrounded:
		return "The summary was withheld: a claim in it could not be traced to this patient's record. This has been logged as a defect."
	}
	return "The reason has been logged."
}

func failureSentenceBN(kind FailureKind) string {
	switch kind {
	case FailureAssembly:
		return "ক্লিনিক্যাল তথ্য একত্র করা যায়নি; এটি একটি ত্রুটি এবং নথিভুক্ত করা হয়েছে।"
	case FailureRefused:
		return "রোগীর তথ্য সুরক্ষার জন্য অনুরোধটি পাঠানোর আগেই বাতিল করা হয়েছে।"
	case FailureProvider:
		return "মডেলের সঙ্গে যোগাযোগ করা যায়নি।"
	case FailureTimeout:
		return "মডেল নির্ধারিত সময়ে উত্তর দেয়নি।"
	case FailureInvalidOutput:
		return "মডেলের উত্তর প্রত্যাশিত কাঠামোর সঙ্গে মেলেনি, তাই তা দেখানো হয়নি।"
	case FailureUngrounded:
		return "সারসংক্ষেপটি আটকে রাখা হয়েছে: এর একটি তথ্য রোগীর রেকর্ডে খুঁজে পাওয়া যায়নি। এটি ত্রুটি হিসেবে নথিভুক্ত হয়েছে।"
	}
	return "কারণটি নথিভুক্ত করা হয়েছে।"
}

// payloadOf turns the assembled context into the map the gateway takes.
//
// Through JSON rather than by hand, so that what the model is shown and what
// `core.ai_synthesis.context` stores are the same bytes by construction. A second, hand-written
// projection would be a second thing to keep in step, and the day it drifted the grounding check
// would be comparing the model's answer against a context the model never saw.
func payloadOf(c Context) (map[string]any, error) {
	encoded, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// deriveEventID makes a run's terminal event id a function of the run and the outcome.
//
// At-least-once delivery is the queue's honest contract, so a worker that dies between writing the
// row and appending the event will have its job replayed — and a random event id would then put two
// AI_SYNTHESIS_COMPLETED events in the ledger for one summary. A derived id lets the ledger absorb
// the repeat, which is the mechanism `jobs` already assumes every handler uses.
//
// A version-5 UUID under a fixed namespace: deterministic, collision-free for distinct inputs, and
// visibly not a random id to anybody reading the table.
func deriveEventID(runID uuid.UUID, eventType string) uuid.UUID {
	return uuid.NewSHA1(synthesisEventNamespace, []byte(runID.String()+"/"+eventType))
}

// synthesisEventNamespace is this agent's own namespace, so a derived id here can never collide
// with one derived somewhere else in the system for a different reason.
var synthesisEventNamespace = uuid.MustParse("6f9c1f30-7a3e-4d1b-9a5c-1c0b7e2d4a10")

func mustJSON(v any) []byte {
	encoded, err := json.Marshal(v)
	if err != nil {
		// A context that does not marshal is a programming error in this package's own structs,
		// caught by every test that assembles anything. Storing `{}` instead would violate
		// invariant 105 at the next verification, which is a worse and later failure.
		return []byte(`{}`)
	}
	return encoded
}

// ageMonths is whole months between a birth date and now.
func ageMonths(birth, now time.Time) int {
	if birth.IsZero() {
		return 0
	}
	years := now.Year() - birth.Year()
	months := int(now.Month()) - int(birth.Month())
	total := years*12 + months
	if now.Day() < birth.Day() {
		total--
	}
	if total < 0 {
		return 0
	}
	return total
}
