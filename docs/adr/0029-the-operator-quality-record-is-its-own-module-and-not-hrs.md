# ADR-0029 · The operator quality record is its own module, and it is not HR's

**Status:** Accepted · **Date:** 5 Sep 2026 · **Checkpoints:** CP63 · **Amends:** §8.2's module list, and the CP15 permission seed

## Context

CP62 routes a correction to the operator who typed the value. CP63 is what makes that worth
doing: §4.3 says the purpose is that _"recurring patterns per operator surface so targeted
retraining happens and the same mistake does not repeat."_ Without it the corrections are
bookkeeping.

Two questions had to be answered before a line was written, and both are about people rather
than about code.

### Where does the code live?

`internal/clinical` already holds the correction workflow, and putting the quality record beside
it is one import away. But the quality record reads no clinical value, writes no observation,
and must not name a patient. Its subject is a member of staff. Everything it needs from the
correction workflow is already in the database — `read.correction_request` carries the operator,
the reason code, the observation code and the timestamp, denormalised at CP62 precisely so that
counting them would not require a join to a table that every correction rewrites.

`internal/hr` exists in the allowlist and is the obvious other candidate. That is the second
question, and it is not an architectural one.

### Who reads it?

`hr.performance.read` already exists — _"Read staff quality and throughput records"_ — and HR
holds it. Reusing it would have taken ten minutes and no migration.

The plan puts _"performance-linked pay or discipline"_ explicitly out of scope, and names the
cultural risk in its own words: _"a metric that feels punitive damages data honesty — staff hide
errors instead of correcting them."_ A permission that hands an operator's error history to the
department that sets pay puts the out-of-scope item back in, whatever anybody intends by it. The
mechanism decides what happens under pressure, not the intention behind it.

There is also a plainer argument. Reading a correction record usefully means knowing that a
weight is measured on a scale somebody else calibrates, that the same code going wrong three
times is usually the instrument, and that corrections after four in the afternoon are a rota
problem. That is a clinical supervisor's knowledge. Handing the same numbers to somebody without
it produces confident conclusions about the wrong thing.

## Decision

### 1. `internal/quality`, importing only `platform` and `rbac`

Added to `backend/architecture.json` as `"quality": ["platform", "rbac"]` — a shorter list than
any clinical module has. It cannot import `clinical`, so it cannot grow into a second place
where observations are read or written; it reads its own queries over `read.correction_request`
and `read.observation` and returns counts.

It records a raised flag on the security audit trail through an interface it declares, which
`cmd/api` implements with the audit recorder — the same bridge pattern as `alertBridge` and
`counselingAuditBridge`. No module imports `audit` and that stays true.

### 2. Two new permissions, and `hr.performance.read` is left alone

`quality.read.team` (marked sensitive) and `quality.flag.resolve`, granted to `PHYSICIAN`, `QA`
and `ADMIN`. HR keeps `hr.performance.read` for the throughput reporting CP140 will build; it
does not reach the correction record.

### 3. An operator's own record needs no permission at all

`GET /v1/quality/me` requires a session and nothing else. It reads the caller's own id from the
session, so there is no version of it that returns somebody else's work, and there is no patient
in the response.

Gating it behind a permission would repeat the mistake CP62 made and had to undo: the field
worker who could not see the correction addressed to them because they hold no read permission.
An operator who has to be granted something before they may see their own error count is an
operator who will assume the count is being kept from them.

### 4. No aggregation table, and the flags are stored

The plan names `operator_quality_records`. It is not built. This clinic records on the order of
two thousand values a day; a thirty-day window is sixty thousand rows and counting them grouped
by author against an index is milliseconds. A nightly job would buy nothing and cost the one
failure this feature cannot survive — a job that stops quietly, leaving a supervisor reading
numbers that were true last Tuesday and an operator told about a pattern they fixed a week ago.

**Flags** are stored in `core.quality_flag`, because a flag is a thing that happened: raised on a
window that has since slid past, acknowledged by a named person, still legible a year later. The
counts behind it are frozen into the row when it is raised, so the evidence does not move when
the window does. `core` rather than `read`, and an audit entry rather than a clinical event,
because ADR-0003 event-sources clinical data and this is not clinical data — it follows
`core.break_glass_access` and `core.admin_alert`, which are the same shape of administrative
fact.

### 5. A database invariant refuses a patient in a staff record

`assert_no_quality_record_names_a_patient` fails if any key in a flag's evidence looks like a
patient reference or a clinical value. "We would never put a patient id in there" is not a
mechanism; a supervisor reading a patient's values through their staff's error history would be
reading clinical data through a side door, and the door is now nailed shut rather than merely
unmarked.

## Consequences

**Good.** The correction record cannot become an HR record by somebody reusing a permission that
was already there. The module cannot grow into a second clinical write path, because it cannot
import one. An operator can always see their own number, which is the only real defence against
the cultural risk. The numbers are always current, because there is nothing to fall behind.

**Bad, and we accept it.** Two permissions where one existed, and an access review that now has
to explain why `hr.performance.read` and `quality.read.team` are different things — the answer is
in this record, which is exactly why it is written. The counts are computed on every read, so if
this clinic grows an order of magnitude the supervisor's list will need a materialised view; the
queries are written so that the view can replace them without the API changing.

**Unresolved.** The three thresholds are proposals with `approved_at` null, and the API says so.
Until a clinician approves them the flags they raise are a demonstration of the mechanism rather
than a policy anybody should act on. Whether QA should hold `quality.flag.resolve` as well as
`quality.read.team` is Dr. Nahid's call; both are seeded and either is one row to change.
