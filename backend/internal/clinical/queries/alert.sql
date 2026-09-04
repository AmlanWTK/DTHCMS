-- Critical values and their escalation (CP50).

-- name: CriticalValueRules :many
-- Every rule, for a station app to evaluate locally so the alarm sounds in the operator's
-- hand at the instant they type, not after a round trip they may not have signal for.
--
-- **Ordered most specific first, in exactly the order core.critical_value_for resolves.**
-- The same trick as the plausibility rules and the reference ranges: the client's rule is
-- "the first entry whose predicate matches", and it never ranks anything itself. A phone
-- that ranked them could sound an alarm the server did not raise — or, far worse, stay
-- quiet when the server did.
SELECT * FROM core.critical_value_rule
 ORDER BY code,
          num_nonnulls(sex, min_age_years, max_age_years) DESC,
          coalesce(max_age_years, 200) - coalesce(min_age_years, 0),
          updated_at;

-- name: CriticalValueRuleFor :one
-- The rule that applies to one patient and one code, resolved by the database.
SELECT * FROM core.critical_value_for(@p_code::text, @p_sex::text, @p_age_years::numeric);

-- name: EscalationChain :many
-- The chain, in order. Read by the worker on every sweep rather than cached: an escalation
-- window edited at nine o'clock should apply to the alert raised at nine-oh-one.
SELECT * FROM core.escalation_step ORDER BY step_number;

-- name: OpenAlerts :many
-- The consultant's priority surface: everything unacknowledged in this facility, newest
-- first. Small by construction — an alert list long enough to need paging is a clinic in
-- trouble, and the answer to that is not pagination.
--
-- The display names are joined rather than left to the client. An alert is a message, and a
-- message that says "SPO2 88" makes whoever reads it look the code up; one that says "Oxygen
-- saturation" does not. The registry is the only place those names live (CP42), so this is a
-- join rather than a copy.
SELECT sqlc.embed(a), c.display_en, c.display_bn FROM read.critical_alert a
  JOIN core.observation_code c ON c.code = a.code
 WHERE a.facility_id = $1 AND a.status = 'OPEN'
 ORDER BY a.raised_at DESC
 LIMIT $2;

-- name: AlertsForPatient :many
-- One patient's alert history, acknowledged ones included. What a physician reads before
-- deciding whether today's reading is the first of its kind.
SELECT sqlc.embed(a), c.display_en, c.display_bn FROM read.critical_alert a
  JOIN core.observation_code c ON c.code = a.code
 WHERE a.patient_id = $1 AND a.facility_id = $2
 ORDER BY a.raised_at DESC
 LIMIT $3;

-- name: AlertByID :one
SELECT sqlc.embed(a), c.display_en, c.display_bn FROM read.critical_alert a
  JOIN core.observation_code c ON c.code = a.code
 WHERE a.id = $1 AND a.facility_id = $2;

-- name: AlertsForObservation :many
-- Every alert raised by one recorded value. Normally one; more than one when a value was
-- entered, corrected and entered again.
SELECT sqlc.embed(a), c.display_en, c.display_bn FROM read.critical_alert a
  JOIN core.observation_code c ON c.code = a.code
 WHERE a.observation_id = $1 AND a.facility_id = $2
 ORDER BY a.raised_at;

-- name: AlertsDueForEscalation :many
-- Open alerts whose current step has been standing longer than the next step's window.
--
-- The whole sweep in one statement, and deliberately so: the alternative is the worker
-- reading every open alert and doing the arithmetic in Go, which is the same arithmetic in
-- a second place, running against a clock that is not the database's.
SELECT sqlc.embed(a), c.display_en, c.display_bn,
       s.step_number AS next_step, s.notify_role AS next_role,
       s.note_en AS next_note_en, s.note_bn AS next_note_bn
  FROM read.critical_alert a
  JOIN core.observation_code c ON c.code = a.code
  JOIN core.escalation_step s ON s.step_number = a.escalation_step + 1
 WHERE a.status = 'OPEN'
   AND a.raised_at + make_interval(secs => s.after_seconds) <= @now::timestamptz
 ORDER BY a.raised_at
 LIMIT @max_alerts::int;

-- name: ObservationAnswers :many
-- Every vocabulary, in clinical order. Whole rather than per code: the examination screen
-- needs eleven of these the moment the patient sits down, and eleven round trips on a clinic
-- connection is the difference between a two-minute examination and a five-minute one.
SELECT * FROM core.observation_answer
 WHERE retired_at IS NULL
 ORDER BY code, ordering;
