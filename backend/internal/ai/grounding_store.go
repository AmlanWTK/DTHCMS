package ai

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// The grounding check's own reads and writes (CP72).
//
// Kept in its own file rather than folded into `store.go` because the two halves answer different
// questions and are read by different people: `store.go` is the gateway's bookkeeping, and this is
// the quality record. The `Store` is one type because they are one table's worth of connection
// pool, and splitting it would put a second pool in the process to no purpose.

// ErrDefectClosed is a defect somebody has already classified.
//
// Its own error rather than "not found", because the two mean opposite things to a reviewer: one
// says the defect is not yours to see, the other says a colleague reached it first and their
// judgement stands until somebody deliberately reopens it.
var ErrDefectClosed = errors.New("ai: that grounding defect has already been reviewed")

// GroundingRequired asks the register whether this agent's answers must be grounded.
//
// **Fail closed.** A database that cannot answer returns `true` and the reason why, exactly as
// `ProvenanceOf` returns REAL_PATIENT when the synthetic register is unreadable: a lookup that
// failed must never become permission. The cost of the safe answer here is a summary withheld on
// the day the database is slow, which is a cost D-15's degraded screen is designed to absorb.
func (s *Store) GroundingRequired(ctx context.Context, agentCode string) (bool, string, error) {
	row, err := s.q.AIAgentGrounding(ctx, agentCode)
	if err != nil {
		return true, "", err
	}
	return row.GroundingRequired, row.GroundingExemptReason, nil
}

// RecordGrounding writes the verdict on to the interaction the answer came from.
//
// The status is passed rather than derived here so that the one place deciding what an ungrounded
// answer *is* stays in the gateway: this function writes what it is told, and the check constraint
// in migration 00054 refuses the combination that would be a lie.
func (s *Store) RecordGrounding(ctx context.Context, interaction uuid.UUID,
	state GroundingState, findings int, status string) error {

	return s.q.RecordAIGroundingVerdict(ctx, dbgen.RecordAIGroundingVerdictParams{
		ID: interaction, GroundingState: string(state),
		GroundingFindings: int32(findings), //nolint:gosec // one finding per token in an answer bounded by the agent's max_output_tokens
		Status:            status,
	})
}

// Defect is one recorded grounding violation, as the operator screens show it.
type Defect struct {
	ID            uuid.UUID `json:"id"`
	InteractionID uuid.UUID `json:"ai_interaction_id"`

	AgentCode     string `json:"agent_code"`
	PromptVersion string `json:"prompt_version"`
	ModelVersion  string `json:"model_version"`

	// SubjectPseudonym and never the patient id. A reviewer working the queue is judging whether a
	// model invented a number, which is a question about the answer and not about the person; the
	// interaction row carries the subject for the rare case where it matters, behind the
	// permission that already governs it.
	SubjectPseudonym string `json:"subject_pseudonym,omitempty"`

	Arm     GroundingArm `json:"arm"`
	Path    string       `json:"path"`
	Token   string       `json:"token"`
	Excerpt string       `json:"excerpt,omitempty"`
	Reason  string       `json:"reason"`

	DetectedAt time.Time `json:"detected_at"`

	Status         string     `json:"status"`
	Classification string     `json:"classification,omitempty"`
	ReviewedBy     *uuid.UUID `json:"reviewed_by,omitempty"`
	ReviewedAt     *time.Time `json:"reviewed_at,omitempty"`
	ReviewNote     string     `json:"review_note,omitempty"`
}

// Recording is what the gateway hands over about one call, so that a defect can be read on its own
// a week later without joining anything.
type Recording struct {
	InteractionID    uuid.UUID
	FacilityID       uuid.UUID
	AgentCode        string
	PromptVersion    string
	ModelVersion     string
	SubjectPatientID uuid.UUID
	SubjectPseudonym string
}

// RecordDefects writes one row per finding.
//
// One row per finding rather than one per call: an answer that invented two numbers and a date is
// three things somebody has to look at, and a single row saying "failed" loses the shape of the
// failure, which is the first thing anybody asks when the rate moves.
//
// Errors are returned rather than swallowed, and the caller logs them at error and carries on
// refusing the answer. That order matters: the refusal is the safety property and it must not
// depend on the defect table being writable, but a refusal whose evidence could not be written is
// itself a defect and invariant 107 will find it.
func (s *Store) RecordDefects(ctx context.Context, rec Recording, findings []GroundingFinding,
	at time.Time) error {

	for _, finding := range findings {
		params := dbgen.RecordAIGroundingDefectParams{
			ID: uuid.New(), FacilityID: rec.FacilityID, AiInteractionID: rec.InteractionID,
			AgentCode: rec.AgentCode, PromptVersion: rec.PromptVersion,
			ModelVersion:     rec.ModelVersion,
			SubjectPseudonym: rec.SubjectPseudonym,
			Arm:              string(finding.Arm), Path: finding.Path,
			Token: finding.Token, Excerpt: finding.Excerpt, Reason: finding.Reason,
			DetectedAt: at.UTC(),
		}
		if rec.SubjectPatientID != uuid.Nil {
			params.SubjectPatientID = uuid.NullUUID{UUID: rec.SubjectPatientID, Valid: true}
		}
		if err := s.q.RecordAIGroundingDefect(ctx, params); err != nil {
			return err
		}
	}
	return nil
}

// DefectFilter narrows the reviewer's queue.
type DefectFilter struct {
	Status string
	Arm    string
	Since  time.Time
	Limit  int
}

// Defects reads the queue.
func (s *Store) Defects(ctx context.Context, facility uuid.UUID, filter DefectFilter) ([]Defect, error) {
	params := dbgen.AIGroundingDefectsParams{
		FacilityID: facility, Since: filter.Since.UTC(),
		RowLimit: int32(filter.Limit), //nolint:gosec // bounded by the handler
	}
	if filter.Status != "" {
		params.Status = optional(filter.Status)
	}
	if filter.Arm != "" {
		params.Arm = optional(filter.Arm)
	}
	rows, err := s.q.AIGroundingDefects(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]Defect, 0, len(rows))
	for _, row := range rows {
		out = append(out, Defect{
			ID: row.ID, InteractionID: row.AiInteractionID,
			AgentCode: row.AgentCode, PromptVersion: row.PromptVersion,
			ModelVersion: row.ModelVersion, SubjectPseudonym: row.SubjectPseudonym,
			Arm: GroundingArm(row.Arm), Path: row.Path, Token: row.Token,
			Excerpt: row.Excerpt, Reason: row.Reason, DetectedAt: row.DetectedAt,
			Status: row.Status, Classification: text(row.Classification),
			ReviewedBy: nullableUUID(row.ReviewedBy), ReviewedAt: row.ReviewedAt,
			ReviewNote: row.ReviewNote,
		})
	}
	return out, nil
}

// Reviewing is one human verdict on the validator.
type Reviewing struct {
	DefectID       uuid.UUID
	Facility       uuid.UUID
	Classification string
	ReviewedBy     uuid.UUID
	Note           string
	At             time.Time
}

// KnownClassification is the closed set. Not an enum in Go for the same reason the database has a
// check constraint: the three words mean different things to the measurement, and a fourth
// invented at the API boundary would land in whichever bucket somebody guessed.
func KnownClassification(value string) bool {
	switch value {
	case "TRUE_POSITIVE", "FALSE_POSITIVE", "VALIDATOR_DEFECT":
		return true
	}
	return false
}

// ReviewDefect records the verdict, refusing a defect somebody has already judged.
func (s *Store) ReviewDefect(ctx context.Context, in Reviewing) (Defect, error) {
	params := dbgen.ReviewAIGroundingDefectParams{
		ID: in.DefectID, FacilityID: in.Facility,
		Classification: in.Classification, ReviewedAt: in.At.UTC(),
		ReviewNote: in.Note,
	}
	if in.ReviewedBy != uuid.Nil {
		params.ReviewedBy = uuid.NullUUID{UUID: in.ReviewedBy, Valid: true}
	}
	row, err := s.q.ReviewAIGroundingDefect(ctx, params)
	if err != nil {
		// No row came back, which the query's own `status = 'OPEN'` predicate makes mean exactly
		// one thing: either the defect is not this facility's, or a colleague classified it first.
		// Both are answered with the same 409, because telling a caller which would be telling
		// them that a defect they may not see exists.
		if errors.Is(err, pgx.ErrNoRows) {
			return Defect{}, ErrDefectClosed
		}
		return Defect{}, err
	}
	return Defect{
		ID: row.ID, InteractionID: row.AiInteractionID,
		AgentCode: row.AgentCode, PromptVersion: row.PromptVersion,
		ModelVersion: row.ModelVersion, SubjectPseudonym: row.SubjectPseudonym,
		Arm: GroundingArm(row.Arm), Path: row.Path, Token: row.Token,
		Excerpt: row.Excerpt, Reason: row.Reason, DetectedAt: row.DetectedAt,
		Status: row.Status, Classification: text(row.Classification),
		ReviewedBy: nullableUUID(row.ReviewedBy), ReviewedAt: row.ReviewedAt,
		ReviewNote: row.ReviewNote,
	}, nil
}

// GroundingHealth is one agent's quality picture over a window, as the dashboard reads it.
type GroundingHealth struct {
	AgentCode   string `json:"agent_code"`
	Passed      int64  `json:"passed"`
	Failed      int64  `json:"failed"`
	NotRequired int64  `json:"not_required"`

	DefectsOpen          int64 `json:"defects_open"`
	DefectsReviewed      int64 `json:"defects_reviewed"`
	DefectsFalsePositive int64 `json:"defects_false_positive"`
}

// FalsePositiveRate is criterion 2 measured in production rather than on a frozen set.
//
// Nil when nothing has been reviewed. Nil rather than zero, and the distinction is the whole
// value of the number: a validator nobody has checked has an *unknown* false-positive rate, and
// reporting that as 0% is exactly the claim this checkpoint's brief warns against.
func (h GroundingHealth) FalsePositiveRate() *float64 {
	if h.DefectsReviewed == 0 {
		return nil
	}
	rate := float64(h.DefectsFalsePositive) / float64(h.DefectsReviewed)
	return &rate
}

// Health reads it.
func (s *Store) Health(ctx context.Context, facility uuid.UUID, since time.Time) ([]GroundingHealth, error) {
	rows, err := s.q.AIGroundingHealth(ctx, dbgen.AIGroundingHealthParams{
		FacilityID: facility, Since: since.UTC(),
	})
	if err != nil {
		return nil, err
	}
	out := make([]GroundingHealth, 0, len(rows))
	for _, row := range rows {
		out = append(out, GroundingHealth{
			AgentCode: row.AgentCode, Passed: row.Passed, Failed: row.Failed,
			NotRequired: row.NotRequired, DefectsOpen: row.DefectsOpen,
			DefectsReviewed: row.DefectsReviewed, DefectsFalsePositive: row.DefectsFalsePositive,
		})
	}
	return out, nil
}

// --- the evaluation harness's storage ---

// EvaluationRun is one run of the frozen set, as it is recorded and as the dashboard reads it.
type EvaluationRun struct {
	ID uuid.UUID `json:"id"`

	AgentCode     string `json:"agent_code"`
	PromptVersion string `json:"prompt_version"`
	PromptSHA256  string `json:"prompt_sha256"`
	ModelVersion  string `json:"model_version"`

	CaseSet       string `json:"case_set"`
	CaseSetSHA256 string `json:"case_set_sha256"`
	GitSHA        string `json:"git_sha,omitempty"`
	Mode          string `json:"mode"`

	Cases    int `json:"cases"`
	Injected int `json:"injected"`
	Detected int `json:"detected"`
	Correct  int `json:"correct"`
	Blocked  int `json:"blocked"`

	SchemaFailures int    `json:"schema_failures"`
	LatencyP50MS   *int   `json:"latency_p50_ms,omitempty"`
	LatencyP95MS   *int   `json:"latency_p95_ms,omitempty"`
	CostMicroUSD   *int64 `json:"cost_micro_usd,omitempty"`

	Verdict          string `json:"verdict"`
	RegressionDetail string `json:"regression_detail,omitempty"`

	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// DetectionRate is criterion 1. Nil when the set injected nothing, because a run that tested no
// hallucinations has not measured a detection rate and 100% would be the most misleading available
// way to say so.
func (r EvaluationRun) DetectionRate() *float64 {
	if r.Injected == 0 {
		return nil
	}
	rate := float64(r.Detected) / float64(r.Injected)
	return &rate
}

// FalsePositiveRate is criterion 2 on the frozen corpus. Nil for the same reason.
func (r EvaluationRun) FalsePositiveRate() *float64 {
	if r.Correct == 0 {
		return nil
	}
	rate := float64(r.Blocked) / float64(r.Correct)
	return &rate
}

// EvaluationCase is what happened to one case.
type EvaluationCase struct {
	CaseID      string
	Expectation string
	Outcome     string
	Findings    []GroundingFinding
	LatencyMS   *int
}

// RecordEvaluation writes a run and its cases.
//
// Not in a transaction, and that is a decision rather than an oversight: `ops.ai_evaluation_run`
// is append-only and its cases cascade from it, so the failure mode of a half-written run is a run
// row with fewer cases than it claims — which the run's own `cases` count makes visible. A
// transaction here would mean holding one open across a hundred inserts from a CI job whose
// database is a container that is about to be thrown away.
func (s *Store) RecordEvaluation(ctx context.Context, run EvaluationRun, cases []EvaluationCase) error {
	params := dbgen.RecordAIEvaluationRunParams{
		ID: run.ID, AgentCode: run.AgentCode, PromptVersion: run.PromptVersion,
		PromptSha256: run.PromptSHA256, ModelVersion: run.ModelVersion,
		CaseSet: run.CaseSet, CaseSetSha256: run.CaseSetSHA256, GitSha: run.GitSHA,
		Mode:           run.Mode,
		Cases:          int32(run.Cases),          //nolint:gosec // the frozen set is committed to this repository
		Injected:       int32(run.Injected),       //nolint:gosec // ditto
		Detected:       int32(run.Detected),       //nolint:gosec // ditto
		Correct:        int32(run.Correct),        //nolint:gosec // ditto
		Blocked:        int32(run.Blocked),        //nolint:gosec // ditto
		SchemaFailures: int32(run.SchemaFailures), //nolint:gosec // ditto
		Verdict:        run.Verdict, RegressionDetail: run.RegressionDetail,
		StartedAt: run.StartedAt.UTC(), FinishedAt: run.FinishedAt.UTC(),
	}
	if run.LatencyP50MS != nil {
		value := int32(*run.LatencyP50MS) //nolint:gosec // a millisecond count bounded by the agent's timeout
		params.LatencyP50Ms = &value
	}
	if run.LatencyP95MS != nil {
		value := int32(*run.LatencyP95MS) //nolint:gosec // ditto
		params.LatencyP95Ms = &value
	}
	if run.CostMicroUSD != nil {
		params.CostMicroUsd = run.CostMicroUSD
	}
	if err := s.q.RecordAIEvaluationRun(ctx, params); err != nil {
		return err
	}
	for _, one := range cases {
		findings, err := json.Marshal(one.Findings)
		if err != nil {
			return err
		}
		if one.Findings == nil {
			findings = []byte("[]")
		}
		caseParams := dbgen.RecordAIEvaluationCaseParams{
			RunID: run.ID, CaseID: one.CaseID, Expectation: one.Expectation,
			Outcome: one.Outcome, Findings: findings,
		}
		if one.LatencyMS != nil {
			value := int32(*one.LatencyMS) //nolint:gosec // bounded by the agent's timeout
			caseParams.LatencyMs = &value
		}
		if err := s.q.RecordAIEvaluationCase(ctx, caseParams); err != nil {
			return err
		}
	}
	return nil
}

// EvaluationRuns is the trend, newest first.
func (s *Store) EvaluationRuns(ctx context.Context, agentCode string, limit int) ([]EvaluationRun, error) {
	params := dbgen.AIEvaluationRunsParams{RowLimit: int32(limit)} //nolint:gosec // bounded by the handler
	if agentCode != "" {
		params.AgentCode = optional(agentCode)
	}
	rows, err := s.q.AIEvaluationRuns(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]EvaluationRun, 0, len(rows))
	for _, row := range rows {
		run := EvaluationRun{
			ID: row.ID, AgentCode: row.AgentCode, PromptVersion: row.PromptVersion,
			PromptSHA256: row.PromptSha256, ModelVersion: row.ModelVersion,
			CaseSet: row.CaseSet, CaseSetSHA256: row.CaseSetSha256, GitSHA: row.GitSha,
			Mode: row.Mode, Cases: int(row.Cases), Injected: int(row.Injected),
			Detected: int(row.Detected), Correct: int(row.Correct), Blocked: int(row.Blocked),
			SchemaFailures: int(row.SchemaFailures),
			Verdict:        row.Verdict, RegressionDetail: row.RegressionDetail,
			StartedAt: row.StartedAt, FinishedAt: row.FinishedAt,
		}
		if row.LatencyP50Ms != nil {
			value := int(*row.LatencyP50Ms)
			run.LatencyP50MS = &value
		}
		if row.LatencyP95Ms != nil {
			value := int(*row.LatencyP95Ms)
			run.LatencyP95MS = &value
		}
		run.CostMicroUSD = row.CostMicroUsd
		out = append(out, run)
	}
	return out, nil
}

// LatestEvaluation is the most recent run for one agent, which is what the gauges read.
func (s *Store) LatestEvaluation(ctx context.Context, agentCode string) (EvaluationRun, bool, error) {
	row, err := s.q.LatestAIEvaluationRun(ctx, agentCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return EvaluationRun{}, false, nil
	}
	if err != nil {
		return EvaluationRun{}, false, err
	}
	return EvaluationRun{
		ID: row.ID, AgentCode: row.AgentCode, PromptVersion: row.PromptVersion,
		ModelVersion: row.ModelVersion, Mode: row.Mode, Cases: int(row.Cases),
		Injected: int(row.Injected), Detected: int(row.Detected),
		Correct: int(row.Correct), Blocked: int(row.Blocked),
		SchemaFailures: int(row.SchemaFailures), Verdict: row.Verdict,
		FinishedAt: row.FinishedAt,
	}, true, nil
}
