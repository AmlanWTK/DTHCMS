package synthesis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Persistence for the synthesis (CP71).
//
// Two things live here and nothing else: the run rows, and [Gather], which is the one function in
// this package allowed to touch the station modules. Everything between the two — the whole of what
// decides what a summary says — is [Assemble], which reads no database at all.

// Errors this package returns.
var (
	// ErrNotFound is a run, or a visit, that is not there.
	ErrNotFound = errors.New("synthesis: not found")
	// ErrVisitNotOpen is a request for a visit that has closed. Refused rather than served:
	// §7.1's summary exists to be read before the consultation, and producing one for a closed
	// visit would be paying a model to brief nobody.
	ErrVisitNotOpen = errors.New("synthesis: the visit is not open")
	// ErrAlreadyRunning is a request made while a run for the same context is already in flight.
	// Not an error at the call site — the caller asked for a summary and one is coming — which is
	// why the handler answers 200 with the run that is already going rather than a refusal.
	ErrAlreadyRunning = errors.New("synthesis: a run for this visit is already in flight")
	// ErrClaimed is a worker finding a run another worker already started. At-least-once delivery
	// makes this ordinary rather than exceptional.
	ErrClaimed = errors.New("synthesis: that run has already been claimed")
)

// Store is the run table.
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

// NewStore builds one.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: dbgen.New(pool)} }

// InTransaction runs fn in one transaction.
//
// Exported because the synthesis is enqueued in the transaction that created it, and the queue's
// `EnqueueTx` takes the caller's transaction by design — so somebody has to own one, and it is this
// store rather than the queue's.
func (s *Store) InTransaction(ctx context.Context,
	fn func(context.Context, pgx.Tx, *dbgen.Queries) error) error {

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, tx, s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Run is one row of `core.ai_synthesis`, as the service and the API see it.
//
// `AIGenerated` is always true and is never omitted from the wire. It looks redundant on a struct
// whose whole purpose is to carry an AI answer, right up until something serialises this into a
// record, a print or an export — and then the marker is the only thing separating a draft from a
// fact. §10.6's third permanent invariant asks for exactly that, everywhere it appears.
type Run struct {
	ID         uuid.UUID `json:"id"`
	VisitID    uuid.UUID `json:"visit_id"`
	PatientID  uuid.UUID `json:"patient_id"`
	Generation int       `json:"generation"`

	State   State   `json:"state"`
	Trigger Trigger `json:"trigger"`

	AIGenerated bool `json:"ai_generated"`

	// Context is what the model was shown. On the payload rather than behind a second endpoint,
	// because D-15's degraded screen is built from it: when there is no narrative, the assembled
	// record is what the physician reads instead.
	Context Context `json:"context"`
	// Output is the model's validated answer, absent until there is one.
	Output json.RawMessage `json:"output,omitempty"`

	PromptVersion string     `json:"prompt_version,omitempty"`
	ModelVersion  string     `json:"model_version,omitempty"`
	InteractionID *uuid.UUID `json:"ai_interaction_id,omitempty"`

	RequestedAt time.Time  `json:"requested_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`

	SLADeadline *time.Time `json:"sla_deadline,omitempty"`
	// MetSLA is nil while the run is unfinished, and never `omitempty` once it is set: a false
	// here is the whole point of measuring.
	MetSLA *bool `json:"met_sla"`

	FailureKind   FailureKind `json:"failure_kind,omitempty"`
	FailureDetail string      `json:"failure_detail,omitempty"`

	// Grounding is §10.2 step 4's verdict on this run's answer, and it is on the wire because the
	// screen has to be able to say *why* a summary is being withheld. Never `omitempty`: a missing
	// verdict and a NOT_CHECKED verdict are different facts, and a client that could not tell them
	// apart would render "checked" for a run nobody checked.
	Grounding GroundingState `json:"grounding_state"`
	// GroundingFindings is how many claims failed. Zero on a PASSED run, and the number a reviewer
	// is about to open in the defect queue on a FAILED one.
	GroundingFindings int `json:"grounding_findings"`

	SupersededAt *time.Time `json:"superseded_at,omitempty"`

	// Unexported, and not on the wire: a client is only ever shown its own facility's runs, and
	// the hash is an implementation detail of the re-run decision rather than something a screen
	// has any use for.
	materialSHA256 string
	facility       uuid.UUID
}

// Stale reports whether a freshly assembled context differs materially from the one behind this
// run. The one call site of [Context.MaterialSHA256] outside its own tests.
func (r Run) Stale(fresh Context) bool { return r.materialSHA256 != fresh.MaterialSHA256() }

// Current is the newest run for a visit, or [ErrNotFound] when there has never been one.
func (s *Store) Current(ctx context.Context, visitID, facility uuid.UUID) (Run, error) {
	row, err := s.q.CurrentSynthesis(ctx, dbgen.CurrentSynthesisParams{
		VisitID: visitID, FacilityID: facility,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	return runOf(dbgen.SynthesisByIDRow(row))
}

// History is every run for a visit, newest first.
func (s *Store) History(ctx context.Context, visitID, facility uuid.UUID) ([]Run, error) {
	rows, err := s.q.SynthesisHistory(ctx, dbgen.SynthesisHistoryParams{
		VisitID: visitID, FacilityID: facility,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Run, 0, len(rows))
	for _, row := range rows {
		run, err := runOf(dbgen.SynthesisByIDRow(row))
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}

// ByID is one run.
func (s *Store) ByID(ctx context.Context, id, facility uuid.UUID) (Run, error) {
	row, err := s.q.SynthesisByID(ctx, dbgen.SynthesisByIDParams{ID: id, FacilityID: facility})
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	return runOf(row)
}

// SLA is acceptance criterion 1 as a number.
type SLA struct {
	Since    time.Time `json:"since"`
	Until    time.Time `json:"until"`
	Finished int64     `json:"finished"`
	Met      int64     `json:"met"`
	Failed   int64     `json:"failed"`
	// Manual is how many of them somebody had to ask for. Criterion 2 says the physician should
	// never need to, and this is the only number that can show whether that is true in practice.
	Manual int64 `json:"manual"`
	// P95Seconds is the ninety-fifth percentile from request to finish. The plan asks for ≥95% of
	// visits inside the SLA, so the ninety-fifth percentile is the statistic that answers the
	// question directly rather than a mean that hides the tail.
	P95Seconds float64 `json:"p95_seconds"`
	// BudgetSeconds is the queue's own budget for this kind, carried onto the report so that a
	// reader is not comparing a percentile against a number they have to remember.
	BudgetSeconds int `json:"budget_seconds"`
}

// MeasureSLA reports the window.
func (s *Store) MeasureSLA(ctx context.Context, facility uuid.UUID, since, until time.Time) (SLA, error) {
	from, to := since.UTC(), until.UTC()
	row, err := s.q.SynthesisSLA(ctx, dbgen.SynthesisSLAParams{
		FacilityID: facility, Since: &from, Until: &to,
	})
	if err != nil {
		return SLA{}, err
	}
	return SLA{
		Since: since.UTC(), Until: until.UTC(),
		Finished: row.Finished, Met: row.Met, Failed: row.Failed, Manual: row.Manual,
		P95Seconds: row.P95Seconds,
	}, nil
}

// --- writes ---

// insert creates a run inside the caller's transaction.
func (s *Store) insert(ctx context.Context, q *dbgen.Queries, in Run, ctxJSON []byte,
	material string, jobID *uuid.UUID, by *uuid.UUID) (Run, error) {

	params := dbgen.InsertSynthesisParams{
		ID: in.ID, FacilityID: in.facilityID(), VisitID: in.VisitID, PatientID: in.PatientID,
		State: string(in.State), Trigger: string(in.Trigger),
		Context: ctxJSON, MaterialSha256: material,
		RequestedAt: in.RequestedAt.UTC(), SlaDeadline: in.SLADeadline,
	}
	if jobID != nil {
		params.JobID = uuid.NullUUID{UUID: *jobID, Valid: true}
	}
	if by != nil {
		params.RequestedBy = uuid.NullUUID{UUID: *by, Valid: true}
	}
	row, err := q.InsertSynthesis(ctx, params)
	if err != nil {
		return Run{}, err
	}
	return runOf(dbgen.SynthesisByIDRow(row))
}

// claim moves a PENDING run to RUNNING, or reports that somebody else already has it.
func (s *Store) claim(ctx context.Context, id uuid.UUID, at time.Time) (Run, error) {
	row, err := s.q.StartSynthesis(ctx, dbgen.StartSynthesisParams{ID: id, StartedAt: &at})
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrClaimed
	}
	if err != nil {
		return Run{}, err
	}
	return runOf(dbgen.SynthesisByIDRow(row))
}

// finish writes the terminal state.
func (s *Store) finish(ctx context.Context, id uuid.UUID, in finishing) (Run, error) {
	state := in.grounding
	if state == "" {
		state = GroundingNotChecked
	}
	params := dbgen.FinishSynthesisParams{
		ID: id, State: string(in.state), Context: in.contextJSON,
		MaterialSha256: in.material, Output: in.output,
		FinishedAt: &in.at, FailureDetail: truncate(in.detail, 2000),
		GroundingState:    string(state),
		GroundingFindings: int32(in.groundingFindings), //nolint:gosec // one per token in an answer the agent's schema bounds
	}
	if in.interaction != nil {
		params.AiInteractionID = uuid.NullUUID{UUID: *in.interaction, Valid: true}
	}
	if in.promptVersion != "" {
		v := in.promptVersion
		params.PromptVersion = &v
	}
	if in.modelVersion != "" {
		v := in.modelVersion
		params.ModelVersion = &v
	}
	if in.kind != "" {
		k := string(in.kind)
		params.FailureKind = &k
	}
	row, err := s.q.FinishSynthesis(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrClaimed
	}
	if err != nil {
		return Run{}, err
	}
	run, err := runOf(dbgen.SynthesisByIDRow(row))
	if err != nil {
		return Run{}, err
	}
	if in.state == Ready {
		// Only a READY run supersedes. A failed one leaves the previous summary standing, which is
		// D-15 read carefully: the physician keeps the older briefing with its own timestamp rather
		// than losing it because a re-run could not reach the model.
		if err := s.q.SupersedeEarlierSyntheses(ctx, dbgen.SupersedeEarlierSynthesesParams{
			At: &in.at, VisitID: run.VisitID, Generation: int32(run.Generation), //nolint:gosec // bounded by the column
		}); err != nil {
			return Run{}, err
		}
	}
	return run, nil
}

type finishing struct {
	state         State
	at            time.Time
	contextJSON   []byte
	material      string
	output        []byte
	interaction   *uuid.UUID
	promptVersion string
	modelVersion  string
	kind          FailureKind
	detail        string

	// grounding is CP72's verdict, taken from the gateway's [ai.Result] rather than computed here.
	// The zero value is NOT_CHECKED, which is what a run that never reached a model should record
	// and — because of `ai_synthesis_ready_is_grounded` — is also what makes a caller that forgot
	// to carry the verdict across produce a database error rather than a readable summary.
	grounding         GroundingState
	groundingFindings int
}

// GroundingState is the verdict as this module records it. The same three strings the column
// accepts, mirrored rather than imported from `ai`: this is a value on a clinical row and a reader
// of the JSON should not have to know which package the gateway lives in.
type GroundingState string

const (
	GroundingNotChecked GroundingState = "NOT_CHECKED"
	GroundingPassed     GroundingState = "PASSED"
	GroundingFailed     GroundingState = "FAILED"
)

// facilityID is carried on the struct without being serialised: a client is only ever shown its own
// facility's runs, and the value is needed for the insert.
func (r Run) facilityID() uuid.UUID { return r.facility }

func runOf(row dbgen.SynthesisByIDRow) (Run, error) {
	out := Run{
		ID: row.ID, VisitID: row.VisitID, PatientID: row.PatientID,
		Generation: int(row.Generation), State: State(row.State), Trigger: Trigger(row.Trigger),
		AIGenerated: true,
		Output:      json.RawMessage(row.Output),
		RequestedAt: row.RequestedAt, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt,
		SLADeadline: row.SlaDeadline, MetSLA: row.MetSla,
		FailureDetail: row.FailureDetail, SupersededAt: row.SupersededAt,
		Grounding: GroundingState(row.GroundingState), GroundingFindings: int(row.GroundingFindings),
		materialSHA256: row.MaterialSha256, facility: row.FacilityID,
	}
	if row.AiInteractionID.Valid {
		id := row.AiInteractionID.UUID
		out.InteractionID = &id
	}
	if row.PromptVersion != nil {
		out.PromptVersion = *row.PromptVersion
	}
	if row.ModelVersion != nil {
		out.ModelVersion = *row.ModelVersion
	}
	if row.FailureKind != nil {
		out.FailureKind = FailureKind(*row.FailureKind)
	}
	if len(row.Context) > 0 {
		if err := json.Unmarshal(row.Context, &out.Context); err != nil {
			return Run{}, fmt.Errorf("synthesis: the stored context does not decode: %w", err)
		}
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
