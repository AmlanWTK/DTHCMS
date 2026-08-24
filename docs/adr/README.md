# Architecture Decision Records

An ADR records **one decision, the context that forced it, and what it costs us** — written
at the moment the decision is made, when the reasoning is still fresh and the alternatives
still feel live.

They exist because this project has no other memory. Sessions end, people join and leave,
and six months from now the only way to know why DTHCMS is a modular monolith rather than
microservices — and whether that reasoning still holds — is to read the record written the
day it was decided.

## Rules

1. **One decision per record.** If it needs "and also", it is two ADRs.
2. **Never edit an accepted ADR's decision.** Reality moved on? Write a new ADR that
   supersedes it, and add a line to the old one pointing forward. The trail matters more
   than tidiness.
3. **Write the consequences honestly**, including the ones you dislike. An ADR that lists
   only benefits is marketing, and it will mislead whoever reads it under pressure.
4. **Number sequentially**, never reuse a number.
5. Status is one of `Proposed`, `Accepted`, `Superseded by ADR-nnnn`, `Deprecated`.

## When an ADR is required

- Adding, removing or changing a dependency rule in `backend/architecture.json`
- Introducing a new datastore, external service, or major library
- Changing an authentication, authorisation or audit mechanism
- Any deviation from `docs/blueprint-v2.0.md`
- Anything a future maintainer would reasonably ask "why on earth did they do that?" about

## Index

| ADR                                                  | Title                                                    | Status   |
| ---------------------------------------------------- | -------------------------------------------------------- | -------- |
| [0001](0001-record-architecture-decisions.md)        | Record architecture decisions                            | Accepted |
| [0002](0002-modular-monolith.md)                     | Modular monolith rather than microservices               | Accepted |
| [0003](0003-event-sourcing-scope.md)                 | Event-source clinical data only                          | Accepted |
| [0004](0004-offline-first-from-phase-1.md)           | Build the station app offline-first from Phase 1         | Accepted |
| [0005](0005-self-implemented-staff-auth.md)          | Implement staff authentication in-house                  | Accepted |
| [0006](0006-postgresql-first.md)                     | PostgreSQL now, AlloyDB only when measured               | Accepted |
| [0007](0007-gemini-provider-and-tier.md)             | Google Gemini as the AI provider, with a tier guard      | Accepted |
| [0008](0008-database-enforced-append-only-ledger.md) | The append-only ledger is enforced by database privilege | Accepted |
| [0009](0009-vendor-neutral-observability.md)         | OpenTelemetry throughout; the backend is configuration   | Accepted |
| [0010](0010-no-session-tokens-in-web-storage.md)     | Session tokens never live in web storage                 | Accepted |

Template: [`0000-template.md`](0000-template.md)
