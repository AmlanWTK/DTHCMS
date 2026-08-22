# ADR-0002: Modular monolith rather than microservices

- **Status:** Accepted
- **Date:** 2026-08-22
- **Blueprint reference:** §15.1 — "Backend: Golang microservices, cloud-native on Google Cloud"

## Context

The blueprint specifies Go microservices. The workload it describes is one clinic:
10–15 concurrent operators, on the order of 100–300 patients a day, a few hundred writes
per minute at absolute peak. That is a workload a single well-built Go process handles
with very large headroom.

The system's central guarantee is an ordered, attributed, immutable event ledger
(blueprint §4.1, §4.5). Splitting the writers of that ledger across process boundaries
turns every clinical write into a distributed-transaction problem.

The team is two people, one of whom has no memory between sessions.

## Decision

Build **one Go binary with hard internal module boundaries**, run in several modes
(`api`, `worker`, `realtime`, `migrate`). Modules own their tables and expose Go
interfaces; no module reads another module's tables directly. Boundaries are enforced
mechanically by `tools/dthclint arch` against `backend/architecture.json`, so a violation
fails the build rather than depending on discipline.

Three things are genuinely separate processes, for reasons that are not about fashion:

- **the ML/OCR service**, because Python is a real language boundary and its load is bursty;
- **the realtime gateway**, because long-lived WebSocket connections scale differently
  from stateless HTTP;
- **the worker pool**, because asynchronous jobs must never compete with request latency.

Module boundaries are drawn along the seams a future extraction would follow.

## Alternatives considered

**Microservices as specified.** Buys independent scaling and deployment we do not need at
this size, and costs distributed transactions across the ledger, N deployment pipelines,
and network failure modes that do not otherwise exist. A permanent tax on a two-person team.

**A single service with no internal boundaries.** Faster initially, and it reliably becomes
a system nobody can safely change. Rejected.

## Consequences

**Good**

- Clinical writes stay in-process: no network hop can break the audit trail.
- One deployment, one set of logs, one place to reason about a request.
- Extraction later is a refactor along a known seam, not a rewrite.

**Bad — and we accept these knowingly**

- We deviate from the blueprint's stated architecture. Recorded here so the deviation is
  deliberate and reviewable, not accidental.
- One process means one blast radius: a fatal bug in any module can take down the API.
  Mitigated by the separate worker and realtime processes, and by panic recovery.
- Scaling is coarse-grained — we scale the whole binary.

**Revisit when** any of these becomes measurably true (implementation plan §5.2):

- a module's resource use is more than 5× the rest and bursty;
- its deployment cadence is genuinely independent and monolith deploys are blocking it;
- it falls under a different compliance boundary;
- a separate team owns it end to end;
- its failure must not be able to take down clinical capture.

"The blueprint said microservices" is not on that list.
