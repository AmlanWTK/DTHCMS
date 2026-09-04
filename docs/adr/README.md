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

| ADR                                                          | Title                                                                                                 | Status       |
| ------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------- | ------------ |
| [0001](0001-record-architecture-decisions.md)                | Record architecture decisions                                                                         | Accepted     |
| [0002](0002-modular-monolith.md)                             | Modular monolith rather than microservices                                                            | Accepted     |
| [0003](0003-event-sourcing-scope.md)                         | Event-source clinical data only                                                                       | Accepted     |
| [0004](0004-offline-first-from-phase-1.md)                   | Build the station app offline-first from Phase 1                                                      | Accepted     |
| [0005](0005-self-implemented-staff-auth.md)                  | Implement staff authentication in-house                                                               | Accepted     |
| [0006](0006-postgresql-first.md)                             | PostgreSQL now, AlloyDB only when measured                                                            | Accepted     |
| [0007](0007-gemini-provider-and-tier.md)                     | Google Gemini as the AI provider, with a tier guard                                                   | Accepted     |
| [0008](0008-database-enforced-append-only-ledger.md)         | The append-only ledger is enforced by database privilege                                              | Accepted     |
| [0009](0009-vendor-neutral-observability.md)                 | OpenTelemetry throughout; the backend is configuration                                                | Accepted     |
| [0010](0010-no-session-tokens-in-web-storage.md)             | Session tokens never live in web storage                                                              | Accepted     |
| [0011](0011-opaque-access-tokens.md)                         | The access token is opaque, not signed                                                                | Accepted     |
| [0012](0012-secrets-at-rest.md)                              | Small secrets are sealed with a configured, rotatable key                                             | Accepted     |
| [0013](0013-device-keys-in-secure-storage.md)                | A device's key is a software seed in Keystore-encrypted storage                                       | Accepted     |
| [0014](0014-audit-module.md)                                 | The security audit log is its own module, hash-chained, with an offline-verifiable export signature   | Accepted     |
| [0015](0015-event-store-append-path.md)                      | One append path, an aggregate-locked gapless sequence, a per-aggregate hash chain with a daily anchor | Accepted     |
| [0016](0016-unforgeable-envelope-and-idempotency.md)         | An unforgeable actor, `Idempotency-Key` required, and its records in `ops`                            | Accepted     |
| [0017](0017-projection-framework.md)                         | A synchronous projection is a database function; asynchronous ones follow `global_seq` with no queue  | Accepted     |
| [0018](0018-in-house-websocket-and-the-realtime-contract.md) | RFC 6455 in-house; realtime is a notification, not a second read path                                 | Accepted     |
| [0019](0019-realtime-on-the-client.md)                       | A realtime message invalidates a query key and never writes to the cache                              | Accepted     |
| [0020](0020-patient-identity-and-the-research-boundary.md)   | Two identifiers, an unreachable link, and a date of birth that carries its own provenance             | Accepted     |
| [0021](0021-browser-device-identity.md)                      | A browser session names the workstation it was opened at, and does not authenticate as one            | **Proposed** |

| [0022](0022-layered-versioned-consent-enforced-by-privilege.md) | Consent is layered and versioned, and research inclusion is enforced by database privilege | Accepted |
| [0023](0023-visits-and-encounters.md) | A visit is the journey, an encounter is one stop, and both state machines are in the database | Accepted |
| [0024](0024-one-observation-model-with-the-unit-rule-in-the-database.md) | One observation model, with the unit rule in the database | Accepted |
| [0025](0025-two-implementations-of-every-formula-held-together-by-fixtures.md) | Two implementations of every formula, held together by fixtures | Accepted |
| [0026](0026-the-growth-reference-is-seeded-data-and-the-protocol-is-a-table.md) | The growth reference is seeded data, and the protocol that picks it is a table | Accepted |
| [0027](0027-an-alert-is-raised-inside-the-transaction-and-delivery-is-a-separate-fact.md) | An alert is raised inside the transaction that stored the value, and delivery is a separate fact | Accepted |
| [0028](0028-history-and-allergies-are-their-own-modules-with-items-that-outlive-a-visit.md) | Medical history and allergies are their own modules, holding items that outlive a visit | Accepted |

Template: [`0000-template.md`](0000-template.md)
