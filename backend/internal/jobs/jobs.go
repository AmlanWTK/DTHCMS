// Package jobs is the background work queue (CP69, §8.4, §8.5, §7.1).
//
// # Why this is a table and not River
//
// ADR-0031, in full. The short version: §8.4's stated reason for River is *"Postgres-backed: no
// dual-write between the database and a broker"*, and that property is what a table in this
// database **is**. What a framework would additionally have brought — its own opaque `args` column
// — is the one thing this checkpoint cannot afford, because *"job payloads may reference but must
// not embed PHI"* is a rule this system enforces with a check constraint and an invariant rather
// than with a convention.
//
// # The four properties this package exists to hold
//
//  1. **A job enqueued in a rolled-back transaction never runs.** [Store.EnqueueTx] takes the
//     caller's transaction. There is no enqueue that opens its own connection, so there is no call
//     site that can accidentally break this.
//  2. **A failed job retries per policy, then dead-letters visibly.** Every attempt is kept with
//     what it said, so a discarded job is something an operator reads rather than a counter that
//     went up.
//  3. **The SLA is measured.** A kind's budget becomes a deadline on the row at enqueue and a
//     boolean at completion. §7.1's five minutes is a number, not a claim.
//  4. **A worker restart causes no duplicate effect.** Delivery is at-least-once and honestly so —
//     an expired lease returns the job to the queue, which is the only recovery available to a
//     queue not writing its completion inside the work's own transaction. Every job here is
//     idempotent, which §8.5 already required.
//
// # What this package will not do
//
// It does not import a single domain module, and `architecture.json` allows it `platform` and
// `rbac` only. A queue that knew what a patient was would be a second write path into the clinical
// record within a checkpoint or two — and the handler for a kind is registered by whoever owns
// that kind, in `cmd/worker`, where the composition root already lives.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/logging"
)

// Errors this package returns.
var (
	// ErrUnknownKind is a kind that is not in the catalogue. A job cannot name one, and a worker
	// cannot register a handler for one: both are refused, in the same fail-at-startup spirit as
	// the route registry.
	ErrUnknownKind = errors.New("jobs: unknown job kind")
	// ErrAlreadyQueued is a dedupe key that already has a live job. Not an error at the call
	// site — the caller asked for the work to happen and it is going to — but distinguishable,
	// so a caller that cares can tell.
	ErrAlreadyQueued = errors.New("jobs: a live job already holds that key")
	// ErrCarriesPHI is an argument object with a key that must never hold a value. Refused in Go
	// before the database refuses it, so the message names the key.
	ErrCarriesPHI = errors.New("jobs: a job argument carries a key that must never hold a value")
	// ErrNotFound is a job that is not there.
	ErrNotFound = errors.New("jobs: not found")
	// ErrNotRetryable is a retry asked for a job that has not been dead-lettered.
	ErrNotRetryable = errors.New("jobs: only a dead-lettered job can be retried")
	// ErrNotCancellable is a cancel asked for a job that has already started. A running job is
	// not cancellable: the worker holding it would carry on regardless, and a status saying
	// otherwise would be a lie on a screen.
	ErrNotCancellable = errors.New("jobs: only a job that has not started can be cancelled")
	// ErrAlreadyPaused and ErrNotPaused make a no-op distinguishable from a success, so an
	// operator who pauses a kind twice is told the second one did nothing.
	ErrAlreadyPaused = errors.New("jobs: that kind is already paused")
	ErrNotPaused     = errors.New("jobs: that kind is not paused")
)

// Kind is one registered job type.
type Kind struct {
	Kind  string `json:"kind"`
	Class string `json:"job_class"`
	Queue string `json:"queue"`
	// Priority: higher runs first. One integer rather than a priority per class, because a worker
	// claiming work needs one ORDER BY and an operator reading the table needs one number.
	Priority int `json:"priority"`

	MaxAttempts       int `json:"max_attempts"`
	BackoffSeconds    int `json:"backoff_seconds"`
	BackoffCapSeconds int `json:"backoff_cap_seconds"`

	// SLASeconds is how long this kind has from enqueue to finish before it has missed. Nil for a
	// kind with no promise attached, which is most of them.
	SLASeconds *int `json:"sla_seconds,omitempty"`

	DescriptionEN string `json:"description_en"`
	DescriptionBN string `json:"description_bn"`

	Paused   bool       `json:"paused"`
	PausedAt *time.Time `json:"paused_at,omitempty"`
	// PausedBy and the names: attributed on the way out as well as on the way in. It was being
	// recorded and never shown, so the one screen an operator opens during an incident could say
	// a kind was stopped and not who stopped it — which leaves the log as the only place that
	// person is findable, and that is not what "findable afterwards" means.
	PausedBy       *uuid.UUID `json:"paused_by,omitempty"`
	PausedByCode   string     `json:"paused_by_code,omitempty"`
	PausedByNameEN string     `json:"paused_by_name_en,omitempty"`
	PausedByNameBN string     `json:"paused_by_name_bn,omitempty"`
}

// Job is one unit of work.
type Job struct {
	ID    uuid.UUID       `json:"id"`
	Kind  string          `json:"kind"`
	Queue string          `json:"queue"`
	Args  json.RawMessage `json:"args"`

	Priority    int `json:"priority"`
	Attempt     int `json:"attempt"`
	MaxAttempts int `json:"max_attempts"`

	Status string `json:"status"`

	RunAt      time.Time  `json:"run_at"`
	EnqueuedAt time.Time  `json:"enqueued_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`

	LeasedBy    string     `json:"leased_by,omitempty"`
	LeasedUntil *time.Time `json:"leased_until,omitempty"`

	SLADeadline *time.Time `json:"sla_deadline,omitempty"`
	// MetSLA is nil while the job is unfinished, or for a kind with no deadline. Never
	// `omitempty` when it is set — a false here is the whole point of measuring.
	MetSLA *bool `json:"met_sla"`

	LastError string `json:"last_error,omitempty"`
	DedupeKey string `json:"dedupe_key,omitempty"`

	DescriptionEN string `json:"description_en,omitempty"`
	DescriptionBN string `json:"description_bn,omitempty"`

	Attempts []Attempt `json:"attempts,omitempty"`
}

// Attempt is one failure, kept with what it said.
type Attempt struct {
	Attempt  int       `json:"attempt"`
	Worker   string    `json:"worker"`
	FailedAt time.Time `json:"failed_at"`
	// RanForMS distinguishes a job that failed after four minutes from one that failed
	// immediately. Different problems.
	RanForMS int64  `json:"ran_for_ms"`
	Error    string `json:"error"`
}

// Health is one row of the queue-health page.
//
// Every registered kind produces one, whether or not anything is queued. A dashboard assembled
// from what is in the queue cannot report the failure it exists to catch: a job type that has
// stopped being enqueued at all.
type Health struct {
	Kind  string `json:"kind"`
	Class string `json:"job_class"`
	Queue string `json:"queue"`

	Paused bool `json:"paused"`
	// The pause, on the health row too, because this is the screen somebody is looking at when
	// they are trying to work out why nothing is happening.
	PausedAt       *time.Time `json:"paused_at,omitempty"`
	PausedByNameEN string     `json:"paused_by_name_en,omitempty"`
	PausedByNameBN string     `json:"paused_by_name_bn,omitempty"`

	SLASeconds *int `json:"sla_seconds,omitempty"`

	Available int `json:"available"`
	Running   int `json:"running"`
	// OldestAvailableSeconds is the number to alert on. Depth says how much there is; age says
	// how long the oldest thing has been ignored, and a queue of two that has not moved in an
	// hour is a worse state than a queue of four hundred that is draining.
	OldestAvailableSeconds int `json:"oldest_available_seconds"`
	// OldestDueSeconds is **the number to alert on**, and it differs from the one above in a way
	// that matters: total age counts a job sitting out an exponential backoff as "waiting", so one
	// failing SMS on a kind with a half-hour cap would light the row up while the retry policy
	// behaved exactly as designed. This one measures from `run_at` over jobs that are actually
	// due, which is what an alert is really asking: is there work nobody is doing right now.
	OldestDueSeconds int `json:"oldest_due_seconds"`

	Succeeded int `json:"succeeded"`
	Discarded int `json:"discarded"`
	Cancelled int `json:"cancelled"`
	// DeadLetters is **not windowed**, and sits beside the windowed counts rather than instead of
	// them. A kind can read "0% given up on, 0 of 40 finished" while forty dead letters from last
	// night are still waiting for somebody — both true, and a reader who assumed one set would
	// conclude the screen was lying.
	DeadLetters int `json:"dead_letters"`

	// FailureRate and SLAAttainment are **null rather than zero when nothing finished in the
	// window**. "No failures" and "nothing ran" are different states, and a dashboard showing 0%
	// for both hides a stopped queue behind the healthiest-looking number on the page.
	FailureRate *float64 `json:"failure_rate"`

	SLAMeasured   int      `json:"sla_measured"`
	SLAMet        int      `json:"sla_met"`
	SLAAttainment *float64 `json:"sla_attainment"`

	// SecondsSinceLastFinish is -1 when this kind has never finished anything, which is the
	// single most interesting thing this row can say.
	SecondsSinceLastFinish int `json:"seconds_since_last_finish"`
}

// Store reads and writes the queue.
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

// NewStore builds one.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: dbgen.New(pool)} }

// Enqueueing is a job about to be created.
type Enqueueing struct {
	Kind string
	// Args may reference but must not embed PHI: ids, codes and counts, never a name, a number a
	// clinician typed, or a value. Checked here so the message names the key, and again by a
	// database constraint so a second application cannot skip the check.
	Args any
	// DedupeKey makes enqueue-once free. A second live job with the same kind and key is
	// absorbed, so a caller that retries its own transaction — or two stations that both notice
	// the same visit is ready — produce one job.
	DedupeKey string
	// RunAt delays the job. Zero means now.
	RunAt time.Time
}

// EnqueueTx writes the job **in the caller's transaction**.
//
// This is acceptance criterion 1, made structural. There is deliberately no `Enqueue` that opens
// its own transaction: a convenience helper like that is the one call site that would quietly
// break the guarantee, and it would be reached for exactly when somebody was in a hurry.
func (s *Store) EnqueueTx(ctx context.Context, tx pgx.Tx, now time.Time, in Enqueueing) (Job, error) {
	args, err := encodeArgs(in.Args)
	if err != nil {
		return Job{}, err
	}
	if key, bad := carriesPHI(args); bad {
		return Job{}, fmt.Errorf("%w: %s", ErrCarriesPHI, key)
	}

	runAt := in.RunAt
	if runAt.IsZero() {
		runAt = now
	}
	params := dbgen.EnqueueJobParams{
		ID: uuid.New(), Args: args, RunAt: runAt.UTC(), Now: now.UTC(), Kind: in.Kind,
	}
	if strings.TrimSpace(in.DedupeKey) != "" {
		key := in.DedupeKey
		params.DedupeKey = &key
	}

	row, err := s.q.WithTx(tx).EnqueueJob(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		// Two different reasons for no row, and they are not the same news. An unknown kind is a
		// programming error; an absorbed duplicate is the system working.
		if params.DedupeKey != nil {
			return Job{}, ErrAlreadyQueued
		}
		return Job{}, fmt.Errorf("%w: %s", ErrUnknownKind, in.Kind)
	}
	if err != nil {
		return Job{}, err
	}
	return Job{
		ID: row.ID, Kind: row.Kind, Queue: row.Queue, Priority: int(row.Priority),
		Status: row.Status, Attempt: int(row.Attempt), MaxAttempts: int(row.MaxAttempts),
		RunAt: row.RunAt, EnqueuedAt: row.EnqueuedAt, SLADeadline: row.SlaDeadline,
		Args: args, DedupeKey: in.DedupeKey,
	}, nil
}

// Kinds is the registered catalogue.
func (s *Store) Kinds(ctx context.Context) ([]Kind, error) {
	rows, err := s.q.JobKinds(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Kind, 0, len(rows))
	for _, row := range rows {
		k := Kind{
			Kind: row.Kind, Class: row.JobClass, Queue: row.Queue, Priority: int(row.Priority),
			MaxAttempts: int(row.MaxAttempts), BackoffSeconds: int(row.BackoffSeconds),
			BackoffCapSeconds: int(row.BackoffCapSeconds),
			DescriptionEN:     row.DescriptionEn, DescriptionBN: row.DescriptionBn,
			Paused: row.PausedAt != nil, PausedAt: row.PausedAt,
			PausedByCode:   row.PausedByCode,
			PausedByNameEN: row.PausedByNameEn, PausedByNameBN: row.PausedByNameBn,
		}
		if row.PausedBy.Valid {
			by := row.PausedBy.UUID
			k.PausedBy = &by
		}
		if row.SlaSeconds != nil {
			seconds := int(*row.SlaSeconds)
			k.SLASeconds = &seconds
		}
		out = append(out, k)
	}
	return out, nil
}

// Health is the dashboard's numbers.
func (s *Store) Health(ctx context.Context, window time.Duration, now time.Time) ([]Health, error) {
	rows, err := s.q.JobHealth(ctx, dbgen.JobHealthParams{
		WindowSeconds: int32(window.Seconds()), Now: now.UTC(),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Health, 0, len(rows))
	for _, row := range rows {
		h := Health{
			Kind: row.Kind, Class: row.JobClass, Queue: row.Queue, Paused: row.Paused,
			PausedAt:       row.PausedAt,
			PausedByNameEN: row.PausedByNameEn, PausedByNameBN: row.PausedByNameBn,
			Available: int(row.Available), Running: int(row.Running),
			OldestAvailableSeconds: int(row.OldestAvailableSeconds),
			OldestDueSeconds:       int(row.OldestDueSeconds),
			Succeeded:              int(row.Succeeded), Discarded: int(row.Discarded),
			Cancelled: int(row.Cancelled), DeadLetters: int(row.DeadLetters),
			SLAMeasured: int(row.SlaMeasured), SLAMet: int(row.SlaMet),
			SecondsSinceLastFinish: int(row.SecondsSinceLastFinish),
			FailureRate:            percentOf(row.FailureRate),
			SLAAttainment:          percentOf(row.SlaAttainment),
		}
		if row.SlaSeconds != nil {
			seconds := int(*row.SlaSeconds)
			h.SLASeconds = &seconds
		}
		out = append(out, h)
	}
	return out, nil
}

// List is the operator's view of what is in the queue and what has failed.
// The `met` filter is tri-state: nil for every job, false for the ones that missed their deadline,
// true for the ones that kept it. The middle one is what makes the attainment figure on the
// dashboard something a reader can open rather than something they have to believe.
func (s *Store) List(ctx context.Context, status, kind string, met *bool, limit int) ([]Job, error) {
	params := dbgen.JobsByStatusParams{RowLimit: int32(limit), MetSla: met}
	if strings.TrimSpace(status) != "" {
		value := status
		params.Status = &value
	}
	if strings.TrimSpace(kind) != "" {
		value := kind
		params.Kind = &value
	}
	rows, err := s.q.JobsByStatus(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(rows))
	for _, row := range rows {
		out = append(out, jobOf(jobRow(row)))
	}
	return out, nil
}

// ByID reads one job with every attempt it made.
//
// The attempts come with it rather than behind a second call: an operator opening a dead-lettered
// job is opening it to read the errors, and a screen that made them click again would be a screen
// built for the shape of the API rather than for the question.
func (s *Store) ByID(ctx context.Context, id uuid.UUID) (Job, error) {
	row, err := s.q.JobByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	job := jobOf(jobRow(row))

	attempts, err := s.q.AttemptsFor(ctx, id)
	if err != nil {
		return Job{}, err
	}
	job.Attempts = make([]Attempt, 0, len(attempts))
	for _, a := range attempts {
		job.Attempts = append(job.Attempts, Attempt{
			Attempt: int(a.Attempt), Worker: a.Worker, FailedAt: a.FailedAt,
			RanForMS: a.RanForMs, Error: a.Error,
		})
	}
	return job, nil
}

// Retry puts a dead-lettered job back, with its attempt count reset so the policy applies again
// from the start. An operator retrying a job that failed five times means "try again", not "try
// once more".
func (s *Store) Retry(ctx context.Context, id uuid.UUID, now time.Time) (Job, error) {
	if _, err := s.q.RetryJob(ctx, dbgen.RetryJobParams{ID: id, Now: now.UTC()}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return s.whyNot(ctx, id, ErrNotRetryable)
		}
		return Job{}, err
	}
	// Re-read rather than assembling a Job from the UPDATE's RETURNING clause. The clause carries
	// five columns; a Job has twenty, and the rest would serialise as Go zero values — `args:
	// null` against a schema that requires an object, `enqueued_at` in the year one, `attempt 0
	// of 0`. A client typed against the contract and trusting the body would render nonsense, and
	// the only reason it would not be noticed is that a careful client refetches anyway.
	return s.ByID(ctx, id)
}

// Cancel stops a job that has not started.
func (s *Store) Cancel(ctx context.Context, id uuid.UUID, reason string, now time.Time) (Job, error) {
	if _, err := s.q.CancelJob(ctx, dbgen.CancelJobParams{
		ID: id, Reason: reason, Now: now.UTC(),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return s.whyNot(ctx, id, ErrNotCancellable)
		}
		return Job{}, err
	}
	// Re-read, for the same reason Retry does.
	return s.ByID(ctx, id)
}

// whyNot separates "there is no such job" from "that job is in the wrong state".
//
// One 404 covering both would send an operator looking for a typo when the real answer is that
// somebody else retried it thirty seconds ago.
func (s *Store) whyNot(ctx context.Context, id uuid.UUID, wrongState error) (Job, error) {
	if _, err := s.ByID(ctx, id); err != nil {
		return Job{}, err
	}
	return Job{}, wrongState
}

// Pause stops a kind being claimed, without a deploy. The alternative during an incident is a
// deploy.
func (s *Store) Pause(ctx context.Context, kind string, by uuid.UUID, now time.Time) (Kind, error) {
	if _, err := s.q.PauseKind(ctx, dbgen.PauseKindParams{
		Kind: kind, PausedBy: by, Now: now.UTC(),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return s.whyNotKind(ctx, kind, ErrAlreadyPaused)
		}
		return Kind{}, err
	}
	// Re-read, so the answer carries the whole kind including who paused it — the same reason
	// Retry re-reads rather than assembling a half-filled struct from a RETURNING clause.
	return s.kind(ctx, kind)
}

// Resume undoes it.
func (s *Store) Resume(ctx context.Context, kind string) (Kind, error) {
	if _, err := s.q.ResumeKind(ctx, kind); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return s.whyNotKind(ctx, kind, ErrNotPaused)
		}
		return Kind{}, err
	}
	return s.kind(ctx, kind)
}

// kind reads one row of the catalogue. Small enough to read whole: the catalogue is nine rows
// today and will not be hundreds, and a second query filtering in SQL would buy nothing.
func (s *Store) kind(ctx context.Context, kind string) (Kind, error) {
	kinds, err := s.Kinds(ctx)
	if err != nil {
		return Kind{}, err
	}
	for _, k := range kinds {
		if k.Kind == kind {
			return k, nil
		}
	}
	return Kind{}, fmt.Errorf("%w: %s", ErrUnknownKind, kind)
}

func (s *Store) whyNotKind(ctx context.Context, kind string, wrongState error) (Kind, error) {
	kinds, err := s.Kinds(ctx)
	if err != nil {
		return Kind{}, err
	}
	for _, k := range kinds {
		if k.Kind == kind {
			return Kind{}, wrongState
		}
	}
	return Kind{}, fmt.Errorf("%w: %s", ErrUnknownKind, kind)
}

// PHIKeys is the database's copy of the list, for the drift test.
//
// The class came with CP70, and it is compared here rather than in a second test because the
// failure it guards against is the same one: two representations of one list drifting apart. A key
// that is IDENTIFIER in Go and CLINICAL in the database would be a key the AI gateway's Go check
// refuses and its database constraint permits — which is the check that exists to be the backstop
// silently becoming narrower than the thing it backs.
func (s *Store) PHIKeys(ctx context.Context) (map[string]logging.PHIKey, error) {
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

// InTransaction runs fn against a transaction on this store's pool.
func (s *Store) InTransaction(ctx context.Context,
	fn func(context.Context, pgx.Tx) error) error {

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// carriesPHI says whether a job argument holds a key that must never carry a value, and which.
//
// The same rule as `logging.PHIKeys`, checked in a third place. It is checked here as well as by
// the database constraint so that the message names the key: a constraint violation says
// "job_args_carry_no_phi", which tells a developer that they broke a rule and not which word broke
// it, and a rule that does not say what to do instead gets worked around.
//
// Recursive, because `{"patient": {"name": "..."}}` hides it one level down and a check that only
// looked at the top level would pass exactly the payload somebody was most likely to write.
func carriesPHI(args json.RawMessage) (string, bool) {
	var decoded any
	if err := json.Unmarshal(args, &decoded); err != nil {
		// Unparseable JSON is the database's problem, not this check's. Refusing here would
		// produce a confusing message about PHI for a payload whose real fault is a broken
		// marshaller.
		return "", false
	}
	return walkForPHI(decoded)
}

func walkForPHI(value any) (string, bool) {
	switch v := value.(type) {
	case map[string]any:
		for key, inner := range v {
			if banned := phiKeyIn(key); banned != "" {
				return banned, true
			}
			if found, bad := walkForPHI(inner); bad {
				return found, true
			}
		}
	case []any:
		for _, inner := range v {
			if found, bad := walkForPHI(inner); bad {
				return found, true
			}
		}
	}
	return "", false
}

// phiKeyIn matches the whole key and any `_`-suffixed form, the same two shapes the log handler
// checks: `patient_name` and `guardian_phone` are what a developer reaches for when the bare key
// feels wrong.
func phiKeyIn(key string) string {
	lower := strings.ToLower(key)
	if logging.IsPHIKey(lower) {
		return lower
	}
	if idx := strings.LastIndex(lower, "_"); idx >= 0 {
		if logging.IsPHIKey(lower[idx+1:]) {
			return lower
		}
	}
	return ""
}

// backoffFor is how long to wait before the next attempt: exponential from the kind's base,
// doubling each time, capped, with jitter.
//
// The jitter is not decoration. Without it, twenty jobs that failed together because one
// dependency was down retry together, hit the same dependency at the same instant, and fail
// together again — a thundering herd that turns a brief outage into a repeating one.
func backoffFor(kind Kind, attempt int, jitter func() float64) time.Duration {
	base := kind.BackoffSeconds
	for i := 1; i < attempt && base < kind.BackoffCapSeconds; i++ {
		base *= 2
	}
	if base > kind.BackoffCapSeconds {
		base = kind.BackoffCapSeconds
	}
	// Up to a quarter either side, so a herd spreads over half a window rather than arriving as
	// one.
	spread := float64(base) * 0.5 * (jitter() - 0.5)
	seconds := float64(base) + spread
	if seconds < 1 {
		seconds = 1
	}
	return time.Duration(seconds * float64(time.Second))
}

// defaultJitter is the production source of randomness. A test passes its own.
func defaultJitter() float64 { return rand.Float64() }

// encodeArgs turns whatever a caller passed into the JSON that will be stored.
func encodeArgs(args any) (json.RawMessage, error) {
	if args == nil {
		return json.RawMessage(`{}`), nil
	}
	if raw, ok := args.(json.RawMessage); ok {
		if len(raw) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return raw, nil
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func jobOf(row jobRow) Job {
	job := Job{
		ID: row.ID, Kind: row.Kind, Queue: row.Queue, Priority: int(row.Priority),
		Args: row.Args, Status: row.Status,
		Attempt: int(row.Attempt), MaxAttempts: int(row.MaxAttempts),
		RunAt: row.RunAt, EnqueuedAt: row.EnqueuedAt,
		StartedAt: row.StartedAt, FinishedAt: row.FinishedAt,
		SLADeadline: row.SlaDeadline, MetSLA: row.MetSla,
		LastError:     row.LastError,
		DescriptionEN: row.DescriptionEn, DescriptionBN: row.DescriptionBn,
	}
	if row.DedupeKey != nil {
		job.DedupeKey = *row.DedupeKey
	}
	if row.LeasedBy != nil {
		job.LeasedBy = *row.LeasedBy
	}
	job.LeasedUntil = row.LeasedUntil
	return job
}

// jobRow flattens the two generated row types that differ only in name, so the mapping above is
// written once.
type jobRow struct {
	ID            uuid.UUID
	Kind          string
	Queue         string
	Priority      int32
	Args          []byte
	DedupeKey     *string
	Status        string
	Attempt       int32
	MaxAttempts   int32
	RunAt         time.Time
	EnqueuedAt    time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
	LeasedBy      *string
	LeasedUntil   *time.Time
	SlaDeadline   *time.Time
	MetSla        *bool
	LastError     string
	DescriptionEn string
	DescriptionBn string
}

// percentOf turns a numeric percentage into a float, keeping null as null.
//
// Null is load-bearing here: "no failures" and "nothing ran" are different states, and a dashboard
// showing 0% for both hides a stopped queue behind the healthiest number on the page.
func percentOf(n pgtype.Numeric) *float64 {
	if !n.Valid || n.NaN {
		return nil
	}
	value := new(big.Float).SetInt(n.Int)
	value.SetMantExp(value, int(n.Exp)*4)
	if n.Exp != 0 {
		value = new(big.Float).SetInt(n.Int)
		scale := new(big.Float).SetFloat64(1)
		ten := big.NewFloat(10)
		for i := int32(0); i < n.Exp; i++ {
			scale.Mul(scale, ten)
		}
		for i := n.Exp; i < 0; i++ {
			scale.Quo(scale, ten)
		}
		value.Mul(value, scale)
	}
	out, _ := value.Float64()
	return &out
}
