# ADR-0006: PostgreSQL now, AlloyDB only when measured

- **Status:** Accepted
- **Date:** 2026-08-22
- **Blueprint reference:** §15.1 — "PostgreSQL / AlloyDB"
- **Related decision:** D-31

## Context

The blueprint names "PostgreSQL / AlloyDB" without choosing. AlloyDB is Google's
Postgres-compatible database with a columnar engine that is genuinely valuable for
analytical workloads; it also costs substantially more than Cloud SQL and has a higher
floor for a single clinic's transactional volume.

The analytical workload that would justify it — the research dashboards of blueprint §12 —
does not exist until Phase 3, and its shape cannot be honestly predicted before then.

## Decision

Use **PostgreSQL 16 (Cloud SQL, or local Postgres in development)** through Phases 0–2.
Benchmark the real research workload at CP154 and migrate to AlloyDB only if the
measurement justifies it. Deciding to stay is an equally valid, equally documented outcome.

To keep that door open at zero cost: **no AlloyDB-specific SQL anywhere**, and analytical
queries live in a separate read model from the start.

## Alternatives considered

**AlloyDB from day one.** Removes a future migration; pays for analytical capability years
before there is anything to analyse. Rejected as premature.

**A separate analytical database (BigQuery or similar) from the start.** Adds a second
datastore, a synchronisation path and a second query dialect while the clinic has no
research data yet. Reconsidered honestly at Phase 3.

## Consequences

**Good**

- Materially lower cost during the phases with no analytical load.
- Wire compatibility means the migration path stays open and cheap.
- Local development runs the same engine as production, which keeps behaviour honest.

**Bad — and we accept these knowingly**

- A migration may be needed later, with the cutover risk that implies. Mitigated by
  rehearsing it at CP154 with a documented rollback.
- We forgo AlloyDB's columnar acceleration in the meantime; Phase 3 dashboards may be
  slower until the decision point.

**Revisit at CP154, or earlier if** research query latency becomes a complaint rather than
a metric, or the transactional load outgrows a single Cloud SQL instance.
