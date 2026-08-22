# Engineering standards

Conventions that apply everywhere in DTHCMS. Where a rule can be enforced by a machine it
is, because a documented rule that nothing checks is a rule that decays. Where enforcement
isn't possible, the rule is stated here and asked for in code review.

Related: [`architecture-boundaries.md`](architecture-boundaries.md) · [`definition-of-done.md`](definition-of-done.md) · [`adr/`](adr/)

---

## 1. Principles that outrank convenience

1. **Every clinical write goes through the event store.** There is no second write path and
   no "quick update" endpoint. If it isn't an event, it didn't happen.
2. **No patient data outside its intended home.** Not in logs, not in traces, not in error
   reports, not in fixtures, not in a screenshot in an issue.
3. **Fail closed on anything clinical.** Missing data means "cannot verify", never silence
   and never a reassuring green tick.
4. **AI proposes, humans commit.** Generative output is a draft until a person accepts it.
5. **Say what you don't know.** Empty, stale, unverified, offline and failed states are
   designed first-class. Clinical software that hides uncertainty is dangerous software.

---

## 2. Go

### Layout inside a module

```
internal/<module>/
├── domain.go        entities, value objects, invariants — no database, no HTTP
├── service.go       use cases; orchestrates domain + repo + events; owns transactions
├── repo.go          persistence; the ONLY place SQL lives
├── http.go          decode → validate → call service → encode. No business logic.
├── events.go        event types this module emits, their payloads and upcasters
├── projections.go   how this module's read models are built from events
└── *_test.go
```

Dependency direction: `http → service → domain`, and `service → repo`. Nothing points back
inwards. A module may import another module's exported interface, never its internals.

### Naming

| Thing        | Convention                                            | Example                                            |
| ------------ | ----------------------------------------------------- | -------------------------------------------------- |
| Packages     | short, lowercase, no underscores, singular            | `prescription`, not `prescriptions` or `rx_utils`  |
| Interfaces   | what it does, not what it is                          | `PatientLookup`, `SafetyEvaluator`                 |
| Constructors | `New<Type>`                                           | `NewSafetyEngine`                                  |
| Test names   | what is being asserted                                | `TestSigningRejectsUnclearedPrescription`          |
| Context      | always first parameter, always named `ctx`            | `func (s *Service) Record(ctx context.Context, …)` |
| Errors       | lowercase, no trailing punctuation, wrapped with `%w` | `fmt.Errorf("appending event: %w", err)`           |

### Errors

- One error type with a stable machine code, an HTTP status, a bilingual user-facing
  message, and internal detail that is **logged but never returned**.
- Distinguish clinical errors from technical ones. `SAFETY_RULE_BLOCKED` gets a modal with
  an override path; `DB_TIMEOUT` gets a retry. Clients branch on the code, never on the message.
- Never ignore an error. `errcheck` enforces this; `_ = f()` requires a comment saying why.
- Never `panic` in request handling. The recovery middleware exists for genuine bugs, not
  as control flow.

### Logging

- `log/slog`, structured, always with a correlation ID.
- **Never log patient identifiers.** `tools/dthclint phi` fails the build on `name`, `nid`,
  `phone`, `address`, `dob`, credentials and similar keys. Log `patient_id` and resolve it
  through the audited path when a human genuinely needs to know who it is.
- Levels: `Error` — needs a human; `Warn` — degraded but handled; `Info` — significant state
  change; `Debug` — development only.
- A log line is not an audit trail. Audit goes to the ledger (clinical) or `audit_events`
  (security), never to stdout.

### Testing

- Domain logic: pure, fast, table-driven.
- Services and repositories: real Postgres and Redis via testcontainers.
- One behaviour per test; the name states the behaviour.
- Every event type has a golden test pinning its serialised shape, so an accidental schema
  change fails the build rather than corrupting the ledger.
- Clinical calculations are tested against **published reference values with a citation in
  the test**, not against what the implementation happens to produce.

---

## 3. TypeScript

- `strict: true`, always. `any` requires a comment justifying it.
- Feature-first structure; a feature exposes a public surface through its `index.ts` and
  never reaches into another feature's internals.
- `components/ui` holds design-system primitives and never imports from `features`.
- Server state in TanStack Query, keyed by resource. **WebSocket messages invalidate query
  keys; they never mutate the cache directly** — one code path for fresh data.
- Forms: React Hook Form with Zod schemas shared between web and mobile, so validation
  cannot drift between the two.
- Every clinical value renders through `<ValueWithAttribution>` and `<DualUnitValue>`.
  Raw clinical text in JSX is a review comment.

---

## 4. Database

- `snake_case`, plural table names, singular column names.
- UUIDv7 primary keys named `id`; human-facing identifiers are a separate unique column.
- `TIMESTAMPTZ` only, stored UTC, rendered `Asia/Dhaka`.
- `NUMERIC` for money and every clinical measurement. **Never floating point for a dose.**
- Every measured value has a `unit` column and is stored in a canonical SI unit.
- `facility_id` on every relevant table from the start, even with one facility.
- Foreign keys enforced in `core` and `read`; the `ledger` deliberately has none to
  projections, so it stays valid when a projection is dropped and rebuilt.
- Migrations are forward-only in production and follow expand → migrate → contract. No
  release both writes a new schema and drops the old one.

---

## 5. Events

- Naming: `NOUN_VERBPAST`, always past tense, always specific.
  `HEIGHT_RECORDED`, `PRESCRIPTION_SIGNED`, `CONSENT_REVOKED` — never `UPDATE_PATIENT`.
- Every event carries the full envelope: value, unit, `user_id`, `device_id`, `role`,
  `station`, timestamps. The envelope is constructed from the authenticated request context;
  it cannot be supplied by a client.
- `occurred_at` (clinical truth) and `recorded_at` (forensic truth) are both kept and never
  conflated.
- Events are immutable. A schema change is a **new `event_version`** with an upcaster, never
  a rewrite of history. Upcasters are never deleted and each has a test with a real archived payload.
- Corrections reference the event they correct and state a reason. The original stays queryable.

---

## 6. HTTP API

- Resource-oriented paths, plural nouns: `/v1/patients/{id}/observations`.
- Verbs live in the method, not the path. An exception is a genuine action that is not a
  resource mutation: `/v1/prescriptions/{id}/sign`.
- Every mutating endpoint accepts an `Idempotency-Key`.
- Every route declares its required permission. **A route with no declared permission fails
  at startup**, not at runtime.
- Errors return the standard envelope with a machine code. 403 responses never reveal
  whether the resource exists.

---

## 7. Git and review

- Conventional Commits, enforced by the commit-msg hook and CI. Scope with the checkpoint
  where it helps: `feat(cp06/db): add append-only ledger schema`.
- Branches: `cpNN-short-description`.
- One approval required. Clinical logic additionally needs Dr. Nahid's confirmation of the
  behaviour.

### Review checklist

Beyond the Definition of Done, a reviewer explicitly asks:

- Does this add a write path that bypasses the event store?
- Could any patient identifier reach a log, a trace, an error report or an AI provider?
- Does a failure of this code fail **closed**, or does it fail silently?
- Are the states designed — loading, empty, error, offline, unauthorised?
- If this encodes a clinical rule or threshold, **who approved that number?**
- Does this cross a module boundary that `architecture.json` does not allow, and if so, where
  is the ADR?
- Would a developer joining in a year understand why this is shaped the way it is?
