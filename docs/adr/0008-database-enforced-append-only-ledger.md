# 8. The append-only ledger is enforced by database privilege, not application code

Date: 2026-08-23

## Status

Accepted

## Context

ADR-0003 settled _what_ is event-sourced: clinical data, and only clinical data. It did
not settle what makes the event log append-only.

The blueprint (§4.1, §4.3) requires that the event log is the source of truth and that a
correction never overwrites the original. The obvious implementation is a rule the code
follows: repositories expose `Append` and no `Update`, code review catches the rest.

That rule holds until it doesn't. The failure is not a developer deciding to violate the
architecture; it is a Tuesday afternoon, a projection that is visibly wrong in front of a
patient, and a one-line `UPDATE` in a `psql` session that fixes it. Nobody reviews that
statement. It leaves no trace beyond the row it changed, and every later replay of the
events produces a different answer from the stored one — with no way to tell which is
right, because the evidence was the thing that got edited.

This is also the property that makes the medico-legal record worth anything. "This is
what was recorded, and it has not been altered" is a claim about the system's mechanics.
If the mechanism is a convention, the claim is that everyone has always followed it.

## Decision

The application's database role is granted `SELECT` and `INSERT` on the `ledger` schema
and nothing else. No `UPDATE`, no `DELETE`, no `TRUNCATE`, now or by default on any table
created later. It is granted no write privilege at all on the `read` schema, where
projections live.

Migrations run under a separate connection with a separate role
(`DTHCMS_POSTGRES_MIGRATION_URL`), which owns the schema. Configuration refuses to start
in production if the two connections are the same.

Because `ALTER DEFAULT PRIVILEGES` applies only to objects created by the role that
issued it — an invisible condition — `core.assert_invariants()` re-checks the privilege
model after every migration run, and an explicit test connects as the application role
and attempts each forbidden statement.

Development is not exempt. `migrate dev-roles` creates local login roles that are members
of the same group roles, and the default local connection uses the restricted one.

## Consequences

**A forbidden write fails everywhere.** In CI, on a laptop, in a debugging session at
midnight. The guarantee does not depend on which code path reached the database.

**Legitimate maintenance needs an explicit role.** Some operation will one day genuinely
need to modify ledger rows — a GDPR-style erasure, a corrupted batch. The answer is a
separate, audited maintenance role used deliberately, never a loosening of the
application's grant. The risk is that the grant model feels obstructive and someone
widens it; the assertion functions make that visible, but only if the failure is treated
as a design question rather than a blocker to route around.

**Two connection strings to manage.** More configuration, and one more thing to get wrong
in a deployment — mitigated by the fail-fast rule that refuses to start when they match.

**Local setup gains a step.** `make migrate` must run before the application can connect,
because the restricted role does not exist until it does. This is the cost of developing
against the privileges production uses, and it is the cheaper end of the trade.

**Managed PostgreSQL is a constraint on hosting (D-01).** The model needs `CREATE ROLE`
and `GRANT`, which every managed PostgreSQL provides; it deliberately avoids event
triggers, which several restrict.
