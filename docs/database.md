# Database conventions

Established at CP06. Every later checkpoint follows them, and several are enforced by
the database itself rather than by review.

---

## 1. Six schemas, and why the boundary is a privilege

| Schema     | Holds                                                   | Authoritative?                         |
| ---------- | ------------------------------------------------------- | -------------------------------------- |
| `core`     | Users, roles, devices, facilities, templates, formulary | Yes                                    |
| `ledger`   | The append-only clinical event log                      | **Yes — the clinical source of truth** |
| `read`     | Projections built from the ledger                       | No — rebuildable                       |
| `ops`      | Jobs, metrics, rollups, migration bookkeeping           | Yes                                    |
| `docs`     | Document metadata (bytes live in object storage)        | Yes                                    |
| `research` | Anonymised marts, with no join path back to `core`      | Derived                                |

Three group roles hold the privileges. They are `NOLOGIN` and hold no passwords; login
users are granted membership, so the migration that defines the privilege model contains
no secret and is identical in every environment.

| Role               | `core`    | `ledger`           | `read`              | `ops`     | `research` |
| ------------------ | --------- | ------------------ | ------------------- | --------- | ---------- |
| `dthcms_app`       | full CRUD | **SELECT, INSERT** | **SELECT only**     | full      | no access  |
| `dthcms_projector` | SELECT    | SELECT             | full DML + TRUNCATE | full      | no access  |
| `dthcms_research`  | no access | no access          | no access           | no access | SELECT     |

The two entries in bold are the point of the whole checkpoint.

`dthcms_app` has no `UPDATE`, `DELETE` or `TRUNCATE` on `ledger`. A correction to a
clinical record is a new event that supersedes the old one, never an edit to it — and
that is not a rule the code is asked to remember. An `UPDATE` against the event log
fails at the database, in every environment, including a `psql` session at eleven at
night during an incident.

`dthcms_app` cannot write to `read` at all. Projections are derived; a projection that
can be corrected by hand is a projection that will be, once, and will then agree with
nothing. Only the projector writes there.

`dthcms_research` holds `USAGE` on `research` alone. Anonymisation that depends on the
analyst querying the right schema is not anonymisation. A notebook connected with this
role cannot read a patient's name because the rows are not reachable from it.

### Two connections, not one

`DTHCMS_POSTGRES_URL` is the application's, using a role with the privileges in the
table above. `DTHCMS_POSTGRES_MIGRATION_URL` belongs to `cmd/migrate` alone and owns the
schema. Configuration refuses to start in production if they are the same string:
migrating with the application's connection means granting every request handler the
privileges needed to drop a table or make the ledger writable.

Locally, `make migrate` creates login roles (`dthcms_app_local`,
`dthcms_projector_local`, `dthcms_research_local`) that are members of the group roles,
and the application's default URL uses the first of them. A developer therefore runs
with production-shaped privileges: a forbidden write fails on the machine where it was
written, not in staging a week later.

---

## 2. Migrations

Numbered, sequential, goose-format, in `backend/migrations/`, embedded into the binary
so the migrations that passed CI are the migrations that run.

```
backend/migrations/00005_short_description.sql
```

```sql
-- What this migration does, and why it is being done — not a restatement of the SQL.

-- +goose Up
...
-- +goose Down
...
```

Function bodies, `DO` blocks and anything else containing `;` inside `$$` must be
wrapped in `-- +goose StatementBegin` / `-- +goose StatementEnd`, or goose will split it
mid-statement.

### Commands

```
make migrate            # apply everything pending, then verify invariants
make migrate-status     # what has been applied
make migrate-verify     # check checksums and invariants; change nothing
make migrate-down       # roll back one migration (refused in production)
```

### Rules

**An applied migration is never edited.** A migration that has run will not run again,
so an edit leaves the repository and the database describing different schemas, silently,
until an environment is built from scratch and turns out different. The runner stores a
SHA-256 of every file when it applies it and checks them on every later run, so this
produces an error naming the file rather than a mystery six weeks later. Fix a mistake
with a new migration.

**Every migration is reversible, or says why it is not.** `down` exists for development
and test. It is refused in production: a down migration that drops a column drops the
data in it, and recovery there is restore-from-backup plus a forward migration.

**Migrations are applied by one role.** `ALTER DEFAULT PRIVILEGES` grants only over
objects created by the role that issued it, so a table created by a different role
arrives without the intended privileges — including a `ledger` table the application can
rewrite. Nothing fails at the time. `core.assert_invariants()` runs after every migration
and turns that into a loud failure.

**Migrations never run at application start-up.** `cmd/migrate` is a separate binary and
an explicit deployment step. A service that migrates its own database at boot will
eventually do it during an unplanned restart in the middle of a clinic.

---

## 3. Table conventions

### Identifiers

`uuid` primary keys, named `id`. Never a bigserial: station apps generate identifiers
offline, before they have ever spoken to the server (ADR-0004), and a sequence cannot do
that. Client-generated ids are UUIDv7 so they still sort by creation time.

### Timestamps

`timestamptz`, always — never `timestamp`. Stored UTC, rendered in the facility's
timezone (`core.facility.timezone`). The clinic is `Asia/Dhaka` today, which is exactly
why the assumption must not be baked into a column type.

### Audit columns

Every mutable table in `core`, `ops` and `docs`:

```sql
created_at timestamptz NOT NULL DEFAULT now(),
updated_at timestamptz NOT NULL DEFAULT now(),
created_by uuid,   -- core.app_user, foreign key from CP15
updated_by uuid
```

followed by `SELECT core.attach_updated_at('schema.table');`. `updated_at` is set by the
database, not the application: an application-set timestamp records when the application
believed it wrote, which is a different fact.

The `ledger` carries none of these. An event already records who wrote it, from which
device, at which station and when, inside the write envelope [R-03]. A second,
differently-shaped record of the same fact is a second thing to disagree with.

### `facility_id`

Every table in `core`, `ledger`, `read` and `docs` carries

```sql
facility_id uuid NOT NULL REFERENCES core.facility(id)
```

or has a row in `core.facility_scope_exemption` giving a reason. DTHC is one clinic
today; §15.3 Phase 4 anticipates more, and adding a tenancy discriminator to a populated
clinical database is among the worst migrations there is (D-61). The exemption route is
deliberately awkward — a row, with a written reason — so that skipping the convention
requires a decision rather than an oversight. `core.assert_facility_scoping()` enforces
it after every migration run.

### Naming

Singular table names (`facility`, not `facilities`). `snake_case` throughout. Booleans
read as assertions (`is_active`). Enumerations are `text` with a `CHECK`, not a
PostgreSQL `enum`: adding a value to an enum type is a migration that locks, and clinical
vocabularies grow.

---

## 4. Invariants checked after every migration

`core.assert_invariants()` runs every assertion **registered in `ops.invariant`**, in
sequence order, and fails on the first violation.

The registry arrived at CP07 of the migration set, for a reason worth stating: the runner
used to log a hard-coded list of what it had checked, CP15 added two assertions, and the log
went on naming four while six ran. A list maintained in two places drifts, and this was
already the second time. So the set is data now — adding an assertion is one `INSERT`, and
the log line is correct by construction. A registration naming a function that does not
exist is refused at the moment it is written, and `TestEveryAssertionIsRegistered` fails if
an assertion exists that nothing runs.

Currently registered:

| Assertion                      | Fails when                                                                                                       |
| ------------------------------ | ---------------------------------------------------------------------------------------------------------------- |
| `assert_ledger_append_only()`  | `dthcms_app` holds `UPDATE`, `DELETE` or `TRUNCATE` on any `ledger` table                                        |
| `assert_read_models_derived()` | `dthcms_app` holds any write privilege in `read`                                                                 |
| `assert_research_isolated()`   | `dthcms_research` can reach `core`, `ledger`, `read` or `docs`                                                   |
| `assert_facility_scoping()`    | a table lacks `facility_id` and has no exemption                                                                 |
| `assert_users_undeletable()`   | `dthcms_app` holds `DELETE` on `core.app_user` or `core.user_role`                                               |
| `assert_rbac_constraints()`    | blueprint §4.4's access rules are violated, a permission is granted to no role, or an active station has no role |

Adding a guarantee means writing the function and registering it, not adding it to the Go
code, so that it is checked by anything that touches the database — including a manual
`SELECT core.assert_invariants();` during an incident.

The last two arrived at CP15 and are the pattern worth copying. `assert_rbac_constraints()`
turns three sentences of the blueprint — a nutritionist has no access to prescriptions, a
pharmacist sees dosing but not diagnoses, registration is blinded to sensitive clinical data
— into queries that fail a migration. Prose in a specification cannot fail a build; an
assertion can, in every environment, including the one nobody remembered to test. See
[`identity.md`](identity.md) §1.

---

## 5. Generated query code

`sqlc` reads the migrations as its schema, so generated types cannot drift from the
database: a column renamed in a migration and not in a query is a compile error rather
than a failure the first time a clinician saves a form.

**Run it through Docker: `make sqlc`, or `.\scripts\dev.ps1 sqlc` on Windows.**

Not from a locally installed binary, and specifically not from `go install`. sqlc v1.27.0
embeds the Postgres parser as a WebAssembly module, and the wazero runtime vendored with
that release predates Go 1.25 — the toolchain this project now requires. A binary built
that way faults on start-up:

```
panic: start function[17] failed: wasm error: out of bounds memory access
```

It fails while parsing the _migrations_, before it reads a single query, so the message
says nothing about any SQL you wrote. Which is worth knowing, because the natural reading
of a crash during `sqlc generate` is that a query is malformed.

The published image carries a binary built with a toolchain that works, and pins the same
version CI installs. That second property matters as much as the first: a local sqlc one
version ahead rewrites every generated file's header, and `sqlc diff` in CI then reports a
difference that is entirely about the tool.

```
make sqlc          # regenerate
make sqlc-check    # what CI runs; fails if the committed output is stale
```

Query files live beside the module that owns them and are listed explicitly in
`backend/sqlc.yaml`. The list is added to in the checkpoint that creates each module,
which keeps "which modules talk to the database" a reviewable list rather than a property
of the directory tree. Generated code is committed; CI fails if it is stale.

---

## 6. Testing

`backend/internal/platform/migrate/migrate_test.go` runs against a real PostgreSQL and
skips without one:

```
DTHCMS_TEST_POSTGRES_URL=postgres://dthcms:dthcms_local_only@127.0.0.1:5433/postgres?sslmode=disable
```

It is not mocked. Everything it asserts is a property of PostgreSQL's privilege system,
and a mock would only confirm that the mock agrees with the test. Each test builds its
own database and drops it afterwards, so no test depends on another's leftovers.

The suite connects **as the application role** and tries to rewrite the ledger, the way a
bug or a well-meant hotfix would. It also proves that loosening a grant, editing an
applied migration, or creating a table without `facility_id` each fail loudly.

### Known gap

The plan's _migrate-up on a restored snapshot_ is partially covered:
`TestMigrationsRunOverExistingData` migrates over a populated database, but there is only
one wave of migrations and no production snapshot to restore, because there is no
production data yet. The full restore-and-migrate test lands with the event store
(CP23). Recorded in `docs/progress.md` rather than assumed away.
