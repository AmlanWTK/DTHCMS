package qa

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Reading the rule table, the reviews and the overrides (CP83).
//
// # The rule table is read on every review, and not cached
//
// Criterion 5 is that the rule set is configurable without a code release, and a process-lifetime
// cache would make that "configurable without a code release, but with a restart" — which is a
// release in every way that matters on a clinic floor at nine in the morning. Eighteen rows is
// one index scan.

// Store reads and writes the QA tables.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore builds one.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// ---------------------------------------------------------------------------
// The rules
// ---------------------------------------------------------------------------

const ruleColumns = `id, facility_id, code, kind, severity,
	coalesce(bounce_station_code, ''), window_days, params, enabled,
	title_en, title_bn, detail_en, detail_bn, looks_for_en, looks_for_bn,
	ordering, retired_at, updated_at`

func scanRule(row interface{ Scan(...any) error }) (Rule, error) {
	var r Rule
	var kind, severity string
	var window *int32
	var params []byte
	if err := row.Scan(&r.ID, &r.FacilityID, &r.Code, &kind, &severity,
		&r.BounceStation, &window, &params, &r.Enabled,
		&r.TitleEN, &r.TitleBN, &r.DetailEN, &r.DetailBN,
		&r.LooksForEN, &r.LooksForBN, &r.Ordering,
		&r.RetiredAt, &r.UpdatedAt); err != nil {
		return Rule{}, err
	}
	r.Kind, r.Sev = Kind(kind), Severity(severity)
	if window != nil {
		days := int(*window)
		r.WindowDays = &days
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &r.Params); err != nil {
			// A params blob this build cannot parse is a rule whose behaviour nobody can
			// predict. Refused rather than defaulted: a rule silently running with zero
			// parameters is the "present, enabled and permanently silent" failure again.
			return Rule{}, err
		}
	}
	return r, nil
}

// Rules is every rule row of one facility, retired ones included.
//
// Retired rows come back because the configuration screen shows them — a rule Dr Nahid turned off
// in March is the answer to "why did this stop firing", and a screen that hid it would make that
// question unanswerable from the interface.
func (s *Store) Rules(ctx context.Context, facility uuid.UUID) ([]Rule, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+ruleColumns+` FROM core.qa_rule WHERE facility_id = $1 ORDER BY ordering, code`,
		facility)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Rule{}
	for rows.Next() {
		one, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, one)
	}
	return out, rows.Err()
}

// Ruleset is the live, valid rules of one facility.
func (s *Store) Ruleset(ctx context.Context, facility uuid.UUID) (Ruleset, error) {
	rows, err := s.Rules(ctx, facility)
	if err != nil {
		return Ruleset{}, err
	}
	return NewRuleset(rows)
}

// RuleChange is what `qa.rule.write` may change.
//
// **Every field here is deliberately nilable, and `Kind` and `Code` are deliberately absent.**
// The shape of a rule is not configuration — a kind is a Go predicate, and a rule that changed
// kind would explain every past finding with a question it never asked. The database refuses it
// too (`qa_rule_stays_the_shape_it_was`); this type is what makes the refusal impossible to reach
// from a well-formed request rather than merely refused when it is.
type RuleChange struct {
	Severity      *Severity
	BounceStation *string
	WindowDays    *int
	Params        *Params
	Enabled       *bool
	TitleEN       *string
	TitleBN       *string
	DetailEN      *string
	DetailBN      *string
	// LooksForEN and LooksForBN are what the rule is hunting for, in words. Editable for the
	// same reason the codes are: the day somebody adds a fifth lipid code, the phrase that
	// summarises them is a row and not a release.
	LooksForEN *string
	LooksForBN *string
	Retired    *bool
}

// UpdateRule applies a change to one rule.
//
// Returns the rule as it now stands, so the caller does not re-read to render it. The database's
// own trigger is what refuses a window on a shape that has none, a station that does not exist
// and a change of kind — this method does not re-check them, because two copies of that judgement
// is how the application and the database come to disagree about what a valid rule is.
func (s *Store) UpdateRule(ctx context.Context, facility, id uuid.UUID, actor uuid.UUID,
	change RuleChange) (Rule, error) {

	sets := []string{"updated_by = $3"}
	args := []any{id, facility, actor}
	// The placeholder index is the argument count, always, which is why `set` appends before it
	// formats: a builder that numbered independently is how a WHERE clause comes to read one
	// argument and a SET clause another.
	set := func(column, suffix string, value any) {
		args = append(args, value)
		sets = append(sets, column+" = $"+fmtIndex(len(args))+suffix)
	}
	if change.Severity != nil {
		set("severity", "", string(*change.Severity))
	}
	if change.BounceStation != nil {
		// Empty means "no station", which is legal on a WARN and refused on a BLOCK by the
		// table's own constraint. nullif rather than a branch here, so that the constraint is
		// the one place that judgement lives.
		args = append(args, *change.BounceStation)
		sets = append(sets, "bounce_station_code = nullif($"+fmtIndex(len(args))+", '')")
	}
	if change.WindowDays != nil {
		set("window_days", "", *change.WindowDays)
	}
	if change.Params != nil {
		encoded, err := json.Marshal(*change.Params)
		if err != nil {
			return Rule{}, err
		}
		set("params", "::jsonb", string(encoded))
	}
	if change.Enabled != nil {
		set("enabled", "", *change.Enabled)
	}
	if change.TitleEN != nil {
		set("title_en", "", *change.TitleEN)
	}
	if change.TitleBN != nil {
		set("title_bn", "", *change.TitleBN)
	}
	if change.DetailEN != nil {
		set("detail_en", "", *change.DetailEN)
	}
	if change.DetailBN != nil {
		set("detail_bn", "", *change.DetailBN)
	}
	if change.LooksForEN != nil {
		set("looks_for_en", "", *change.LooksForEN)
	}
	if change.LooksForBN != nil {
		set("looks_for_bn", "", *change.LooksForBN)
	}
	if change.Retired != nil {
		// Retired, never deleted: `read.qa_review` names rules by code, and a review from last
		// year still has to be explicable.
		if *change.Retired {
			sets = append(sets, "retired_at = coalesce(retired_at, now())")
		} else {
			sets = append(sets, "retired_at = NULL")
		}
	}

	row := s.pool.QueryRow(ctx,
		`UPDATE core.qa_rule SET `+strings.Join(sets, ", ")+
			` WHERE id = $1 AND facility_id = $2 RETURNING `+ruleColumns, args...)
	one, err := scanRule(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Rule{}, ErrNotFound
	}
	return one, err
}

func fmtIndex(n int) string {
	// Small enough that strconv would be the only import it earned.
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// ---------------------------------------------------------------------------
// The decisions
// ---------------------------------------------------------------------------

const decisionColumns = `r.id, r.prescription_id, r.patient_id, r.visit_id, r.outcome,
	r.decided_at, r.decided_by, r.decided_role,
	coalesce(u.employee_code, ''), coalesce(u.name_en, ''), coalesce(u.name_bn, ''),
	coalesce(r.bounce_station_code, ''), r.reason_en, r.reason_bn,
	r.findings, r.acknowledged, r.override_id`

func scanDecision(row interface{ Scan(...any) error }) (Decision, error) {
	var d Decision
	var findings []byte
	var override uuid.NullUUID
	if err := row.Scan(&d.ID, &d.PrescriptionID, &d.PatientID, &d.VisitID, &d.Outcome,
		&d.DecidedAt, &d.DecidedBy, &d.DecidedRole,
		&d.DecidedByCode, &d.DecidedByNameEN, &d.DecidedByNameBN,
		&d.BounceStation, &d.ReasonEN, &d.ReasonBN,
		&findings, &d.Acknowledged, &override); err != nil {
		return Decision{}, err
	}
	if len(findings) > 0 {
		_ = json.Unmarshal(findings, &d.Findings)
	}
	if d.Findings == nil {
		d.Findings = []Finding{}
	}
	if override.Valid {
		d.OverrideID = &override.UUID
	}
	return d, nil
}

// StandingDecision is the decision on this prescription since it was last bounced, if there is
// one.
//
// The "since it was last bounced" clause is the same one `core.qa_clearance_stands()` applies,
// and it is here for a screen rather than for a gate: a second officer opening a prescription
// that was bounced this morning and resubmitted since should see an undecided file, not this
// morning's bounce sitting at the top of it.
func (s *Store) StandingDecision(ctx context.Context, facility, prescription uuid.UUID) (Decision, bool, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+decisionColumns+`
		  FROM read.qa_review r
		  JOIN read.prescription p ON p.id = r.prescription_id
		  LEFT JOIN core.app_user u ON u.id = r.decided_by
		 WHERE r.prescription_id = $1 AND r.facility_id = $2
		   AND r.decided_at >= coalesce(p.bounced_at, '-infinity'::timestamptz)
		 ORDER BY r.decided_at DESC LIMIT 1`, prescription, facility)

	one, err := scanDecision(row)
	switch {
	case err == nil:
		return one, true, nil
	case errors.Is(err, pgx.ErrNoRows):
		return Decision{}, false, nil
	default:
		return Decision{}, false, err
	}
}

// Decisions is every decision on one prescription, newest first. The whole history, bounces
// included, because "how many times did this sheet come back" is the question §14.2 counts.
func (s *Store) Decisions(ctx context.Context, facility, prescription uuid.UUID) ([]Decision, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+decisionColumns+`
		  FROM read.qa_review r
		  LEFT JOIN core.app_user u ON u.id = r.decided_by
		 WHERE r.prescription_id = $1 AND r.facility_id = $2
		 ORDER BY r.decided_at DESC`, prescription, facility)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Decision{}
	for rows.Next() {
		one, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, one)
	}
	return out, rows.Err()
}

// ClearanceStands asks the database the same question the gate asks.
//
// **It calls `core.qa_clearance_stands()` rather than repeating its SQL**, which is the whole
// point: a screen that said "cleared" while the trigger refused the signature would be the defect
// this station exists to be. One function, one answer.
func (s *Store) ClearanceStands(ctx context.Context, prescription uuid.UUID) (bool, error) {
	var stands bool
	err := s.pool.QueryRow(ctx, `SELECT core.qa_clearance_stands($1)`, prescription).Scan(&stands)
	return stands, err
}

// ---------------------------------------------------------------------------
// The overrides
// ---------------------------------------------------------------------------

const overrideColumns = `o.id, o.prescription_id, o.patient_id, o.visit_id,
	o.granted_at, o.granted_by, o.granted_role,
	coalesce(u.employee_code, ''), coalesce(u.name_en, ''), coalesce(u.name_bn, ''),
	o.reason, o.blocking_at_grant`

func scanOverride(row interface{ Scan(...any) error }) (Override, error) {
	var o Override
	err := row.Scan(&o.ID, &o.PrescriptionID, &o.PatientID, &o.VisitID,
		&o.GrantedAt, &o.GrantedBy, &o.GrantedRole,
		&o.GrantedByCode, &o.GrantedByNameEN, &o.GrantedByNameBN,
		&o.Reason, &o.BlockingAtGrant)
	return o, err
}

// Override reads the override standing on a prescription, if there is one.
func (s *Store) Override(ctx context.Context, facility, prescription uuid.UUID) (Override, bool, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+overrideColumns+`
		  FROM read.qa_override o
		  LEFT JOIN core.app_user u ON u.id = o.granted_by
		 WHERE o.prescription_id = $1 AND o.facility_id = $2`, prescription, facility)

	one, err := scanOverride(row)
	switch {
	case err == nil:
		return one, true, nil
	case errors.Is(err, pgx.ErrNoRows):
		return Override{}, false, nil
	default:
		return Override{}, false, err
	}
}

// Overrides is every override in a window, newest first, for the person whose job is to ask why.
//
// **This is Quality's view and not the prescriber's.** `docs/qa-rules.md` §2: the answer to a
// rising override rate is a person asking why, and that person should not be the one granting
// them. So the route in front of this is behind `qa.review`, which QA holds and PHYSICIAN does
// not, while `qa.override` is the other way round.
//
// The window is half-open — `[from, to)` — and both ends are whole days, for CP57's reason: a
// window that ended "now" would exclude the override granted a minute ago, which is the one
// somebody is asking about.
func (s *Store) Overrides(ctx context.Context, facility uuid.UUID, from, to time.Time) ([]Override, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+overrideColumns+`
		  FROM read.qa_override o
		  LEFT JOIN core.app_user u ON u.id = o.granted_by
		 WHERE o.facility_id = $1 AND o.granted_at >= $2 AND o.granted_at < $3
		 ORDER BY o.granted_at DESC`, facility, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Override{}
	for rows.Next() {
		one, err := scanOverride(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, one)
	}
	return out, rows.Err()
}

// Queue is the prescriptions waiting at station 10, oldest first.
//
// Oldest first and not newest: a queue is a line, and a QA screen that put the newest submission
// at the top would leave the patient who has been waiting longest at the bottom of it.
func (s *Store) Queue(ctx context.Context, facility uuid.UUID, limit int) ([]QueueEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.patient_id, p.visit_id, p.submitted_at,
		       coalesce(pt.name_en, ''), coalesce(pt.name_bn, ''),
		       coalesce(pt.clinical_id, ''),
		       (SELECT count(*) FROM read.prescription_item i
		         WHERE i.prescription_id = p.id AND i.removed_at IS NULL),
		       (SELECT count(*) FROM read.qa_review r WHERE r.prescription_id = p.id
		         AND r.outcome = 'BOUNCED')
		  FROM read.prescription p
		  LEFT JOIN read.patient pt ON pt.patient_id = p.patient_id AND pt.facility_id = p.facility_id
		 WHERE p.facility_id = $1 AND p.status = 'QA_REVIEW'
		 ORDER BY p.submitted_at
		 LIMIT $2`, facility, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []QueueEntry{}
	for rows.Next() {
		var one QueueEntry
		if err := rows.Scan(&one.PrescriptionID, &one.PatientID, &one.VisitID,
			&one.SubmittedAt, &one.PatientNameEN, &one.PatientNameBN, &one.ClinicalID,
			&one.ItemCount, &one.BounceCount); err != nil {
			return nil, err
		}
		out = append(out, one)
	}
	return out, rows.Err()
}

// QueueEntry is one prescription waiting for this station.
//
// It carries the patient's name because the officer calls it across a room, and the count of
// previous bounces because a sheet on its third visit to this desk is a different conversation
// from one on its first.
type QueueEntry struct {
	PrescriptionID uuid.UUID  `json:"prescription_id"`
	PatientID      uuid.UUID  `json:"patient_id"`
	VisitID        uuid.UUID  `json:"visit_id"`
	SubmittedAt    *time.Time `json:"submitted_at,omitempty"`

	PatientNameEN string `json:"patient_name_en,omitempty"`
	PatientNameBN string `json:"patient_name_bn,omitempty"`
	ClinicalID    string `json:"clinical_id,omitempty"`

	ItemCount   int `json:"item_count"`
	BounceCount int `json:"bounce_count"`
}

// Status is what `read.prescription` says about one sheet, for a caller that needs only the
// status and the two ids.
func (s *Store) Status(ctx context.Context, facility, prescription uuid.UUID) (string, uuid.UUID, uuid.UUID, error) {
	var status string
	var patient, visit uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT status, patient_id, visit_id FROM read.prescription WHERE id = $1 AND facility_id = $2`,
		prescription, facility).Scan(&status, &patient, &visit)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", uuid.Nil, uuid.Nil, ErrNotFound
	}
	return status, patient, visit, err
}

// InTransaction runs fn against one connection, in one transaction.
func (s *Store) InTransaction(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
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
