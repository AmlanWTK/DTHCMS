# ADR-0003: Event-source clinical data only

- **Status:** Accepted
- **Date:** 2026-08-22
- **Blueprint reference:** §4.1, §4.3, §4.5, §15.1

## Context

The blueprint is unambiguous: "The event log, not the current-state table, is the source
of truth" (§4.1), corrections never overwrite (§4.3), and the append-only log serves
clinical governance, medico-legal defence and research integrity simultaneously (§4.5).

Event sourcing delivers exactly that. It also costs real complexity: projections to build
and rebuild, replay tests to keep honest, and queries that are harder to write than a
`SELECT` against a normalised table.

Not every table in DTHCMS needs those guarantees. Nobody will ever need to prove who
changed a formulary price three years ago to the standard required of a blood-pressure
reading — a versioned row with an audit trigger answers that completely.

## Decision

Event-source the data whose wrongness must be **provably traceable to a person forever**:
observations, vitals, anthropometry, diagnoses, allergies, counseling ticks, prescriptions
and their signing, consent, corrections, merges, dispensing, document extraction confirmations.

Everything else uses conventional versioned CRUD with audit triggers: formulary and prices,
templates and rule content, users and roles, inventory quantities, queue state, operational
metrics.

The test for which side a table falls on: _does a wrong value here need to be provably
traceable to a person, forever?_

## Alternatives considered

**Event-source everything.** Consistent and conceptually clean; adds projection and query
complexity to inventory counts and staff rosters for no clinical or legal benefit. Rejected
as cost without return.

**Event-source nothing; rely on audit-log tables.** Simpler, and it cannot satisfy §4.3's
correction workflow — where the original value must remain queryable and attributable after
correction — without reinventing event sourcing badly. Rejected.

## Consequences

**Good**

- The blueprint's attribution, correction, audit and research-integrity requirements all
  fall out of one mechanism rather than four.
- Historical state is reconstructible: a research extract taken last March can be reproduced
  exactly by replaying to that point.
- Offline sync becomes tractable, because immutable facts do not conflict (see ADR-0004).

**Bad — and we accept these knowingly**

- Two persistence patterns in one codebase; contributors must know which applies. The
  boundary is documented here and in `docs/engineering-standards.md`.
- Reads require projections, which can drift. Mitigated by a replay-equivalence test that
  runs in CI on every change — without it, "the log is the source of truth" quietly becomes
  false.
- Corrections cascade to derived values, which must be recomputed and re-versioned rather
  than overwritten.

**Revisit when** a table currently on the CRUD side acquires a medico-legal or research
requirement it cannot meet. Moving a table into the ledger later is possible but expensive;
when in doubt at design time, prefer the ledger.
