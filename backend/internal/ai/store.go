package ai

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
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/logging"
)

// Store is everything the gateway keeps.
//
// No transaction helper and no exported pool, unlike the job queue's store. Nothing here writes two
// rows that have to land together: the interaction record is one row, opened before the call and
// updated after it, and a budget alert is a single insert whose `ON CONFLICT` is the whole of its
// concurrency story. A transaction spanning the provider call would hold a connection open for the
// length of a model round trip, which is the one thing this package must not do to the pool.
type Store struct {
	q *dbgen.Queries
}

// NewStore builds one.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{q: dbgen.New(pool)} }

// Provenance is how the gateway decided what a payload is about.
//
// Note the zero value. `Provenance("")` is not a valid provenance and every path that produces one
// goes through [Store.ProvenanceOf], which returns [ProvenanceRealPatient] on every branch except
// the one where the register says otherwise. There is no way to spell "safe" by accident.
type Provenance string

const (
	// ProvenanceSynthetic is a subject the register says is fabricated. The only provenance a
	// free-tier credential will carry.
	ProvenanceSynthetic Provenance = "SYNTHETIC"
	// ProvenanceRealPatient is everything else, including everything the register has not heard of
	// and everything a failed lookup could not answer.
	ProvenanceRealPatient Provenance = "REAL_PATIENT"
	// ProvenanceUnknown is a payload with no subject at all: a document nobody has linked to a
	// person yet. Treated exactly like a real patient at the tier guard — an unlinked scanned
	// report is a real patient's report, it just has not been filed yet — and kept distinct in the
	// record because "we could not tell" and "we checked and it is real" are different facts and
	// an audit will want to count them separately.
	ProvenanceUnknown Provenance = "UNKNOWN"
)

// MayUseFreeTier reports whether a payload of this provenance may go out on a free credential.
//
// One method, one caller, one line — and it is written as an allow rather than a deny so that a
// provenance added later is refused by default. Spelt the other way round (`!= REAL_PATIENT`), a
// fourth constant would silently become sendable, and nobody reviewing the addition would see the
// consequence.
func (p Provenance) MayUseFreeTier() bool { return p == ProvenanceSynthetic }

// ProvenanceOf decides what a payload is about, by lookup rather than by belief.
//
// Every branch that is not "the register says this subject is fabricated" returns something the
// tier guard refuses on a free credential. That includes the error branch: a database that cannot
// answer must not become permission, and a gateway that fell back to "synthetic" when its lookup
// failed would send a real patient to the free tier on the day the database was slow.
func (s *Store) ProvenanceOf(ctx context.Context, subject uuid.UUID) (Provenance, error) {
	if subject == uuid.Nil {
		return ProvenanceUnknown, nil
	}
	synthetic, err := s.q.IsSyntheticSubject(ctx, subject)
	if err != nil {
		return ProvenanceRealPatient, err
	}
	if synthetic {
		return ProvenanceSynthetic, nil
	}
	return ProvenanceRealPatient, nil
}

// RegisterSynthetic enters a subject in the register.
//
// The only way the free tier becomes reachable for anything. The reason is required and the
// database refuses one shorter than twenty characters, because the entire value of this register is
// that every row is a decision a reviewer can read — see the note at the top of migration 00052.
func (s *Store) RegisterSynthetic(ctx context.Context, subject, facility uuid.UUID,
	reason string, by *uuid.UUID, now time.Time) error {

	params := dbgen.RegisterSyntheticSubjectParams{
		SubjectID: subject, FacilityID: facility, Reason: reason, Now: now.UTC(),
	}
	if by != nil {
		params.RegisteredBy = uuid.NullUUID{UUID: *by, Valid: true}
	}
	return s.q.RegisterSyntheticSubject(ctx, params)
}

// Interaction is one recorded call, as the operator screens show it.
type Interaction struct {
	ID        uuid.UUID `json:"id"`
	AgentCode string    `json:"agent_code"`

	PromptVersion         string `json:"prompt_version,omitempty"`
	ModelVersion          string `json:"model_version,omitempty"`
	RequestedModelVersion string `json:"requested_model_version,omitempty"`

	Tier       string `json:"tier"`
	Provenance string `json:"provenance"`

	SubjectPatientID *uuid.UUID `json:"subject_patient_id,omitempty"`
	SubjectPseudonym string     `json:"subject_pseudonym,omitempty"`

	Status        string `json:"status"`
	RefusalDetail string `json:"refusal_detail,omitempty"`
	InputSHA256   string `json:"input_sha256"`

	InputTokens  int   `json:"input_tokens"`
	OutputTokens int   `json:"output_tokens"`
	CostMicroUSD int64 `json:"cost_micro_usd"`
	LatencyMS    int   `json:"latency_ms"`
	Attempts     int   `json:"attempts"`

	// OutputValid is null when the call never got as far as being validated. Three states rather
	// than two, because a dashboard that showed "not validated" as "invalid" would report a
	// provider outage as a quality problem.
	OutputValid  *bool `json:"output_valid"`
	UsedFallback bool  `json:"used_fallback"`

	// GroundingState is §10.2 step 4's verdict (CP72). On the outbound log because the question an
	// operator has about a call is not only "did it answer" but "was the answer usable", and those
	// are different columns for the same reason `output_valid` is not `status`.
	GroundingState    string `json:"grounding_state"`
	GroundingFindings int    `json:"grounding_findings"`

	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`

	// Outbound and Response are present only on the detail view. A list endpoint returning two
	// hundred clinical payloads would be a bulk export of the clinic's caseload wearing an
	// operational screen's clothes.
	Outbound json.RawMessage `json:"outbound,omitempty"`
	Response json.RawMessage `json:"response,omitempty"`
}

// recording is one row about to be written.
type recording struct {
	ID         uuid.UUID
	FacilityID uuid.UUID
	AgentCode  string

	PromptVersion         string
	ModelVersion          string
	RequestedModelVersion string

	Tier       string
	Provenance Provenance

	SubjectPatientID uuid.UUID
	SubjectPseudonym string

	Status        string
	Outbound      []byte
	Response      []byte
	RefusalDetail string
	InputSHA256   string

	InputTokens  int
	OutputTokens int
	CostMicroUSD int64
	LatencyMS    int
	Attempts     int
	OutputValid  *bool
	UsedFallback bool

	StartedAt  time.Time
	FinishedAt *time.Time
}

// Begin writes the record before the provider is contacted.
//
// The order is criterion 2. A row written after the answer comes back records only the calls that
// came back, which excludes exactly the ones somebody will want afterwards; and because the
// outbound column carries a constraint refusing anything that names a person, a payload that cannot
// be recorded is one that is never sent. The check is not merely alongside the call, it is in front
// of it.
func (s *Store) Begin(ctx context.Context, rec recording) (uuid.UUID, time.Time, error) {
	params := dbgen.BeginAIInteractionParams{
		ID: rec.ID, FacilityID: rec.FacilityID, AgentCode: rec.AgentCode,
		PromptVersion:         optional(rec.PromptVersion),
		ModelVersion:          optional(rec.ModelVersion),
		RequestedModelVersion: optional(rec.RequestedModelVersion),
		Tier:                  rec.Tier, Provenance: string(rec.Provenance),
		SubjectPseudonym: rec.SubjectPseudonym,
		Status:           rec.Status,
		Outbound:         rec.Outbound, Response: rec.Response,
		RefusalDetail: rec.RefusalDetail, InputSha256: rec.InputSHA256,
		InputTokens: int32(rec.InputTokens), OutputTokens: int32(rec.OutputTokens), //nolint:gosec // token counts are bounded by the model's own limits
		CostMicroUsd: rec.CostMicroUSD,
		LatencyMs:    int32(rec.LatencyMS), Attempts: int32(rec.Attempts), //nolint:gosec // bounded by the agent's timeout and max_attempts
		OutputValid: rec.OutputValid, UsedFallback: rec.UsedFallback,
		StartedAt: rec.StartedAt.UTC(), FinishedAt: rec.FinishedAt,
	}
	if rec.SubjectPatientID != uuid.Nil {
		params.SubjectPatientID = uuid.NullUUID{UUID: rec.SubjectPatientID, Valid: true}
	}
	row, err := s.q.BeginAIInteraction(ctx, params)
	if err != nil {
		return uuid.Nil, time.Time{}, err
	}
	return row.ID, row.StartedAt, nil
}

// Finish records what the provider said and what it cost.
func (s *Store) Finish(ctx context.Context, rec recording) error {
	return s.q.FinishAIInteraction(ctx, dbgen.FinishAIInteractionParams{
		ID: rec.ID, Status: rec.Status,
		ModelVersion:  optional(rec.ModelVersion),
		PromptVersion: optional(rec.PromptVersion),
		Response:      rec.Response, RefusalDetail: rec.RefusalDetail,
		InputTokens: int32(rec.InputTokens), OutputTokens: int32(rec.OutputTokens), //nolint:gosec // bounded by the model's own limits
		CostMicroUsd: rec.CostMicroUSD,
		LatencyMs:    int32(rec.LatencyMS), Attempts: int32(rec.Attempts), //nolint:gosec // bounded by the agent's timeout and max_attempts
		OutputValid: rec.OutputValid, UsedFallback: rec.UsedFallback,
		FinishedAt: rec.FinishedAt.UTC(),
	})
}

// Cached is a previous answer to an identical input.
type Cached struct {
	ID            uuid.UUID
	Response      []byte
	PromptVersion string
	ModelVersion  string
	InputTokens   int
	OutputTokens  int
	At            time.Time
}

// Cache looks for one.
func (s *Store) Cache(ctx context.Context, hash string, notBefore time.Time) (Cached, bool, error) {
	row, err := s.q.CachedAIResponse(ctx, dbgen.CachedAIResponseParams{
		InputSha256: hash, NotBefore: notBefore.UTC(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Cached{}, false, nil
	}
	if err != nil {
		return Cached{}, false, err
	}
	out := Cached{
		ID: row.ID, Response: row.Response,
		InputTokens: int(row.InputTokens), OutputTokens: int(row.OutputTokens), At: row.StartedAt,
	}
	if row.PromptVersion != nil {
		out.PromptVersion = *row.PromptVersion
	}
	if row.ModelVersion != nil {
		out.ModelVersion = *row.ModelVersion
	}
	return out, true, nil
}

// Spend is one line of the metering report.
type Spend struct {
	// AgentCode is "" for the deployment as a whole.
	AgentCode      string `json:"agent_code"`
	Calls          int64  `json:"calls"`
	SpendMicroUSD  int64  `json:"spend_micro_usd"`
	InputTokens    int64  `json:"input_tokens"`
	OutputTokens   int64  `json:"output_tokens"`
	BudgetMicroUSD int64  `json:"budget_micro_usd,omitempty"`
	// PercentOfBudget is null when no budget is configured for this agent, rather than zero.
	// "Nothing configured" and "nothing spent" are different states, and a screen showing 0% for
	// both hides an unmetered agent behind the healthiest-looking number on the page.
	PercentOfBudget *int `json:"percent_of_budget"`
}

// DailySpend is what has been spent between two instants, per agent and in total.
func (s *Store) DailySpend(ctx context.Context, facility uuid.UUID, from, to time.Time) ([]Spend, error) {
	rows, err := s.q.AIDailySpend(ctx, dbgen.AIDailySpendParams{
		FacilityID: facility, DayStart: from.UTC(), DayEnd: to.UTC(),
	})
	if err != nil {
		return nil, err
	}
	budgets, err := s.Budgets(ctx, facility)
	if err != nil {
		return nil, err
	}
	out := make([]Spend, 0, len(rows))
	for _, row := range rows {
		spend := Spend{
			AgentCode: row.AgentCode, Calls: row.Calls, SpendMicroUSD: row.SpendMicroUsd,
			InputTokens: row.InputTokens, OutputTokens: row.OutputTokens,
		}
		if budget, ok := budgets[row.AgentCode]; ok {
			spend.BudgetMicroUSD = budget.DailyMicroUSD
			percent := int(row.SpendMicroUsd * 100 / budget.DailyMicroUSD)
			spend.PercentOfBudget = &percent
		}
		out = append(out, spend)
	}
	return out, nil
}

// Budget is one configured limit.
type Budget struct {
	// AgentCode is "" for the deployment-wide budget: the one that catches a runaway in an agent
	// nobody was watching.
	AgentCode     string
	DailyMicroUSD int64
	Thresholds    []int
}

// Budgets reads them, keyed by agent code with "" for the deployment-wide one.
func (s *Store) Budgets(ctx context.Context, facility uuid.UUID) (map[string]Budget, error) {
	rows, err := s.q.AIBudgets(ctx, facility)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Budget, len(rows))
	for _, row := range rows {
		budget := Budget{DailyMicroUSD: row.DailyMicroUsd}
		if row.AgentCode != nil {
			budget.AgentCode = *row.AgentCode
		}
		budget.Thresholds = make([]int, 0, len(row.Thresholds))
		for _, t := range row.Thresholds {
			budget.Thresholds = append(budget.Thresholds, int(t))
		}
		out[budget.AgentCode] = budget
	}
	return out, nil
}

// BudgetAlert is one threshold crossing.
type BudgetAlert struct {
	AgentCode        string    `json:"agent_code"`
	Day              time.Time `json:"day"`
	ThresholdPercent int       `json:"threshold_percent"`
	SpendMicroUSD    int64     `json:"spend_micro_usd"`
	BudgetMicroUSD   int64     `json:"budget_micro_usd"`
	RaisedAt         time.Time `json:"raised_at"`
}

// RaiseBudgetAlert records a crossing and reports whether this is the first time.
//
// The insert *is* the decision: `ON CONFLICT DO NOTHING` means the second call today for the same
// threshold inserts nothing and returns false, so the alert fires once. Doing it the other way —
// reading a table, deciding, then writing — leaves a window in which two workers both read "not yet
// raised" and both tell somebody, which is how an operator learns to ignore this channel.
func (s *Store) RaiseBudgetAlert(ctx context.Context, facility uuid.UUID, agent string,
	day time.Time, threshold int, spend, budget int64, now time.Time) (BudgetAlert, bool, error) {

	row, err := s.q.RaiseAIBudgetAlert(ctx, dbgen.RaiseAIBudgetAlertParams{
		FacilityID: facility, AgentCode: agent, Day: day.UTC(),
		ThresholdPercent: int32(threshold), //nolint:gosec // a percentage, bounded by the budget row's own constraint
		SpendMicroUsd:    spend, BudgetMicroUsd: budget, Now: now.UTC(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return BudgetAlert{}, false, nil
	}
	if err != nil {
		return BudgetAlert{}, false, err
	}
	return BudgetAlert{
		AgentCode: row.AgentCode, Day: row.Day, ThresholdPercent: int(row.ThresholdPercent),
		SpendMicroUSD: row.SpendMicroUsd, BudgetMicroUSD: row.BudgetMicroUsd, RaisedAt: row.RaisedAt,
	}, true, nil
}

// BudgetAlerts is what has been crossed lately.
func (s *Store) BudgetAlerts(ctx context.Context, facility uuid.UUID, since time.Time, limit int) ([]BudgetAlert, error) {
	rows, err := s.q.AIBudgetAlerts(ctx, dbgen.AIBudgetAlertsParams{
		FacilityID: facility, Since: since.UTC(), RowLimit: int32(limit), //nolint:gosec // bounded by the handler
	})
	if err != nil {
		return nil, err
	}
	out := make([]BudgetAlert, 0, len(rows))
	for _, row := range rows {
		out = append(out, BudgetAlert{
			AgentCode: row.AgentCode, Day: row.Day, ThresholdPercent: int(row.ThresholdPercent),
			SpendMicroUSD: row.SpendMicroUsd, BudgetMicroUSD: row.BudgetMicroUsd, RaisedAt: row.RaisedAt,
		})
	}
	return out, nil
}

// InteractionFilter narrows the outbound log.
type InteractionFilter struct {
	AgentCode string
	Status    string
	Since     time.Time
	Limit     int
}

// Interactions is the outbound log, newest first, without the payloads.
func (s *Store) Interactions(ctx context.Context, facility uuid.UUID, filter InteractionFilter) ([]Interaction, error) {
	params := dbgen.AIInteractionsParams{
		FacilityID: facility, Since: filter.Since.UTC(), RowLimit: int32(filter.Limit), //nolint:gosec // bounded by the handler
	}
	if filter.AgentCode != "" {
		agent := filter.AgentCode
		params.AgentCode = &agent
	}
	if filter.Status != "" {
		status := filter.Status
		params.Status = &status
	}
	rows, err := s.q.AIInteractions(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]Interaction, 0, len(rows))
	for _, row := range rows {
		out = append(out, Interaction{
			ID: row.ID, AgentCode: row.AgentCode,
			PromptVersion: text(row.PromptVersion), ModelVersion: text(row.ModelVersion),
			RequestedModelVersion: text(row.RequestedModelVersion),
			Tier:                  row.Tier, Provenance: row.Provenance,
			SubjectPatientID: nullableUUID(row.SubjectPatientID),
			SubjectPseudonym: row.SubjectPseudonym,
			Status:           row.Status, RefusalDetail: row.RefusalDetail, InputSHA256: row.InputSha256,
			InputTokens: int(row.InputTokens), OutputTokens: int(row.OutputTokens),
			CostMicroUSD: row.CostMicroUsd, LatencyMS: int(row.LatencyMs), Attempts: int(row.Attempts),
			OutputValid: row.OutputValid, UsedFallback: row.UsedFallback,
			GroundingState: row.GroundingState, GroundingFindings: int(row.GroundingFindings),
			StartedAt: row.StartedAt, FinishedAt: row.FinishedAt,
		})
	}
	return out, nil
}

// ErrNotFound is an interaction that is not there, or is another facility's.
var ErrNotFound = errors.New("ai: not found")

// Interaction is one call with what was sent and what came back.
func (s *Store) Interaction(ctx context.Context, facility, id uuid.UUID) (Interaction, error) {
	row, err := s.q.AIInteractionByID(ctx, dbgen.AIInteractionByIDParams{ID: id, FacilityID: facility})
	if errors.Is(err, pgx.ErrNoRows) {
		return Interaction{}, ErrNotFound
	}
	if err != nil {
		return Interaction{}, err
	}
	return Interaction{
		ID: row.ID, AgentCode: row.AgentCode,
		PromptVersion: text(row.PromptVersion), ModelVersion: text(row.ModelVersion),
		RequestedModelVersion: text(row.RequestedModelVersion),
		Tier:                  row.Tier, Provenance: row.Provenance,
		SubjectPatientID: nullableUUID(row.SubjectPatientID),
		SubjectPseudonym: row.SubjectPseudonym,
		Status:           row.Status, RefusalDetail: row.RefusalDetail, InputSHA256: row.InputSha256,
		InputTokens: int(row.InputTokens), OutputTokens: int(row.OutputTokens),
		CostMicroUSD: row.CostMicroUsd, LatencyMS: int(row.LatencyMs), Attempts: int(row.Attempts),
		OutputValid: row.OutputValid, UsedFallback: row.UsedFallback,
		GroundingState: row.GroundingState, GroundingFindings: int(row.GroundingFindings),
		StartedAt: row.StartedAt, FinishedAt: row.FinishedAt,
		Outbound: row.Outbound, Response: row.Response,
	}, nil
}

// Model is one pinned version and what it costs.
type Model struct {
	Model                    string `json:"model"`
	ModelVersion             string `json:"model_version"`
	Provider                 string `json:"provider"`
	InputMicroUSDPerMillion  int64  `json:"input_micro_usd_per_million"`
	OutputMicroUSDPerMillion int64  `json:"output_micro_usd_per_million"`
	DescriptionEN            string `json:"description_en"`
	DescriptionBN            string `json:"description_bn"`
	Retired                  bool   `json:"retired"`
}

// Cost is what a call on this model came to, in micro-dollars.
//
// Integer arithmetic throughout, and rounded **up**. Money in a float is a number that stops adding
// up over a month of calls; rounding up means the meter is never optimistic, which is the direction
// a budget alert has to err in — a meter that under-reported would cross the threshold late, which
// is the same as not having one.
func (m Model) Cost(inputTokens, outputTokens int) int64 {
	return divideRoundingUp(int64(inputTokens)*m.InputMicroUSDPerMillion, 1_000_000) +
		divideRoundingUp(int64(outputTokens)*m.OutputMicroUSDPerMillion, 1_000_000)
}

func divideRoundingUp(numerator, denominator int64) int64 {
	if numerator <= 0 {
		return 0
	}
	return (numerator + denominator - 1) / denominator
}

// Models reads the pinned catalogue, keyed by version.
func (s *Store) Models(ctx context.Context) (map[string]Model, error) {
	rows, err := s.q.AIModels(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Model, len(rows))
	for _, row := range rows {
		out[row.ModelVersion] = Model{
			Model: row.Model, ModelVersion: row.ModelVersion, Provider: row.Provider,
			InputMicroUSDPerMillion:  row.InputMicroUsdPerMillion,
			OutputMicroUSDPerMillion: row.OutputMicroUsdPerMillion,
			DescriptionEN:            row.DescriptionEn, DescriptionBN: row.DescriptionBn,
			Retired: row.RetiredAt != nil,
		}
	}
	return out, nil
}

// Agent is one row of the agent catalogue.
type Agent struct {
	AgentCode     string `json:"agent_code"`
	Technology    string `json:"technology"`
	DescriptionEN string `json:"description_en"`
	DescriptionBN string `json:"description_bn"`
}

// Agents reads it.
func (s *Store) Agents(ctx context.Context) ([]Agent, error) {
	rows, err := s.q.AIAgents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Agent, 0, len(rows))
	for _, row := range rows {
		out = append(out, Agent{
			AgentCode: row.AgentCode, Technology: row.Technology,
			DescriptionEN: row.DescriptionEn, DescriptionBN: row.DescriptionBn,
		})
	}
	return out, nil
}

// DeployPrompt writes one version, reporting whether it was new and what is stored.
func (s *Store) DeployPrompt(ctx context.Context, prompt Prompt, now time.Time) (string, bool, error) {
	params := dbgen.DeployAIPromptVersionParams{
		AgentCode: prompt.AgentCode, Version: prompt.Version,
		Major: int32(prompt.major), Minor: int32(prompt.minor), Patch: int32(prompt.patch), //nolint:gosec // a semantic version, validated on the way in
		ModelVersion:  prompt.ModelVersion,
		ContentSha256: prompt.SHA256, Content: prompt.Content, Changelog: prompt.Changelog,
		Now: now.UTC(),
	}
	if prompt.FallbackModelVersion != "" {
		fallback := prompt.FallbackModelVersion
		params.FallbackModelVersion = &fallback
	}
	row, err := s.q.DeployAIPromptVersion(ctx, params)
	if err == nil {
		return row.ContentSha256, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}
	// No row means the version is already deployed. Read what is stored so the caller can compare
	// hashes — "already there with the same text" is start-up succeeding, and "already there with
	// different text" is the change that would make every interaction naming this version
	// unreproducible.
	stored, err := s.q.AIPromptVersion(ctx, dbgen.AIPromptVersionParams{
		AgentCode: prompt.AgentCode, Version: prompt.Version,
	})
	if err != nil {
		return "", false, err
	}
	return stored.ContentSha256, false, nil
}

// DeployedPrompt is one stored version, as the registry screen shows it.
type DeployedPrompt struct {
	AgentCode            string    `json:"agent_code"`
	Version              string    `json:"version"`
	ModelVersion         string    `json:"model_version"`
	FallbackModelVersion string    `json:"fallback_model_version,omitempty"`
	ContentSHA256        string    `json:"content_sha256"`
	Changelog            string    `json:"changelog"`
	DeployedAt           time.Time `json:"deployed_at"`
	Content              string    `json:"content,omitempty"`
}

// DeployedPrompts is the registry as the database holds it.
func (s *Store) DeployedPrompts(ctx context.Context, withContent bool) ([]DeployedPrompt, error) {
	rows, err := s.q.AIPromptVersions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DeployedPrompt, 0, len(rows))
	for _, row := range rows {
		prompt := DeployedPrompt{
			AgentCode: row.AgentCode, Version: row.Version, ModelVersion: row.ModelVersion,
			ContentSHA256: row.ContentSha256, Changelog: row.Changelog, DeployedAt: row.DeployedAt,
		}
		if row.FallbackModelVersion != nil {
			prompt.FallbackModelVersion = *row.FallbackModelVersion
		}
		if withContent {
			prompt.Content = row.Content
		}
		out = append(out, prompt)
	}
	return out, nil
}

// PIIPatterns is the database's copy of the scrubber's rules, for the drift test.
func (s *Store) PIIPatterns(ctx context.Context) ([]Pattern, error) {
	rows, err := s.q.PIIPatterns(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Pattern, 0, len(rows))
	for _, row := range rows {
		out = append(out, Pattern{
			Kind: row.Kind, Expression: row.Pattern, Replacement: row.Replacement,
			DescriptionEN: row.DescriptionEn, DescriptionBN: row.DescriptionBn,
		})
	}
	return out, nil
}

// TextCarriesIdentifier asks the database's copy of the scrubber about one string.
//
// Only the cross-engine agreement test calls this: the gateway uses the compiled Go patterns, and a
// round trip to the database for every string in every payload would be a per-call cost paid to
// answer a question the process can already answer. What the database's copy is for is the check
// constraint, which runs on the way in and cannot be skipped.
func (s *Store) TextCarriesIdentifier(ctx context.Context, candidate string) (bool, error) {
	return s.q.TextCarriesIdentifier(ctx, candidate)
}

// PayloadCarriesIdentifier asks it about a whole document.
func (s *Store) PayloadCarriesIdentifier(ctx context.Context, payload []byte) (bool, error) {
	return s.q.PayloadCarriesIdentifier(ctx, payload)
}

// PHIKeyClasses is the database's copy of the classified key list, for the drift test.
func (s *Store) PHIKeyClasses(ctx context.Context) (map[string]logging.PHIKey, error) {
	rows, err := s.q.PHIKeys(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]logging.PHIKey, len(rows))
	for _, row := range rows {
		out[row.Key] = logging.PHIKey{Guidance: row.Guidance, Class: logging.PHIClass(row.Class)}
	}
	return out, nil
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func text(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func nullableUUID(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	id := value.UUID
	return &id
}

// jsonOrNil encodes a value for a jsonb column, keeping nil as SQL NULL rather than as the four
// bytes "null" — which the outbound column's constraint would accept and which would read on the
// operator's screen as "we sent the JSON value null", a different and untrue thing.
func jsonOrNil(value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("ai: encoding for the record: %w", err)
	}
	return encoded, nil
}
