# ADR-0004: Build the station app offline-first from Phase 1

- **Status:** Accepted
- **Date:** 2026-08-22
- **Blueprint reference:** §15.2 — "a Wi-Fi drop must never lose a station entry"

## Context

Every clinical station is operated from a phone (§2.1, R-01), on clinic Wi-Fi, by staff who
cannot pause a patient's visit while the network recovers. The blueprint requires that a
Wi-Fi drop never loses an entry.

Offline capability is not a feature that can be added later. It determines how every screen
reads and writes data. Retrofitting it into a mobile app with twenty existing screens is a
rewrite of the data layer and all of them.

## Decision

The station app's local SQLite store and outbox are built at **CP64, before the majority of
the clinical stations ship**, and every screen writes through a local command handler —
never directly to the API. The sync engine drains the outbox in the background.

State-changing commands that carry invariants (signing a prescription, closing a visit,
dispensing, merging patients) deliberately **require connectivity**. Measurements never do.

## Alternatives considered

**Online-only for Phase 1, offline in Phase 2.** Ships the first stations sooner, then
rewrites the data layer of every screen already built. Rejected: it front-loads speed and
back-loads a rewrite, and it breaks a stated blueprint requirement in the interim.

**A general-purpose sync framework** (WatermelonDB, PowerSync, Replicache). These replicate
mutable rows; DTHCMS queues immutable events with client-generated IDs, and its correction
semantics are domain-specific. Bending a framework to that shape is more work, and more
surprising, than a purpose-built engine of roughly 1,500 lines. Revisited at CP54.

## Consequences

**Good**

- The clinic keeps working through a network failure, which is the actual requirement.
- Because the queue holds immutable facts rather than row updates, most "conflicts" do not
  exist: two operators recording two measurements is two facts, not a collision.
- The local store doubles as the cache, so the UI is fast even when the network is fine.

**Bad — and we accept these knowingly**

- The sync engine is the most subtle code in the mobile app, and bugs there corrupt clinical
  data silently — the worst failure class in the system. It gets a dedicated test matrix
  (implementation plan §13.10) and a nightly soak test, permanently.
- Encrypted local storage means a lost phone is a manageable incident rather than a breach,
  but it adds key-management work.
- Some operations are deliberately unavailable offline, which must be explained clearly in
  the UI rather than failing mysteriously.

**Revisit when** never, for the station app. The decision is load-bearing for a blueprint
requirement.
