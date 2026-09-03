# Architecture boundaries

DTHCMS is a modular monolith (ADR-0002). That only stays true if the boundaries are
enforced by the build rather than by memory. This document explains the rules;
`backend/architecture.json` is the machine-readable version, and
`backend/tools/dthclint` fails the build on violations.

## The rule

A module may import **only** the modules listed for it in `architecture.json`.

```jsonc
"prescription": ["platform", "eventstore", "rbac", "visit", "formulary", "medsafety"]
```

Anything else is a violation, reported with file and line:

```
internal/prescription/service.go:6: [arch] module "prescription" may not import module "records"
    "prescription" may import: platform, eventstore, rbac, visit, formulary, medsafety.
    If this dependency is genuinely correct, it is an architecture change: write an ADR
    and update architecture.json in the same PR.
```

## Why these boundaries and not others

They are drawn along the seams a future service extraction would follow (implementation
plan §5.2), so that if the clinic ever outgrows one process, the split is a refactor rather
than a rewrite.

Three properties are deliberate:

- **`platform` depends on nothing.** Everything depends on it, so any dependency out of it
  would create a cycle through most of the system. A test enforces this.
- **`ai` depends only on `platform`.** The AI gateway is isolated on purpose: it must be
  possible to reason about — and audit — everything that reaches a model, in one place.
- **`eventstore` depends only on `platform`.** The ledger is the foundation; it must not
  know about the domains writing to it.

## Composition roots are exempt

`cmd/` and `tools/` may import anything. That is where the application is assembled, and
assembly is precisely the job of knowing about every part.

## Test files are exempt

A `_test.go` file may import across a boundary. This is not leniency:

- it is not in the shipped binary, and the rule governs what the binary depends on;
- a test-only import cannot become a production dependency by accident. The moment a
  non-test file in the package uses the imported package, that file must import it too —
  and _that_ import is checked. The compiler and the checker together still make the
  boundary absolute for production code.

What it buys is the ability to test a module against the real thing. `realtime` may not
import `auth`; its RBAC filter is nevertheless meaningless unless a test can build a subject
holding real roles and ask for real permissions, and a test asserting against invented
strings would assert nothing at all.

`TestArchStillCatchesProductionCodeBesideAnExemptTest` holds the other half.

## Changing a boundary

1. Write an ADR: what forces the change, what alternatives you rejected, what it costs.
2. Update `architecture.json` in the same pull request.
3. Expect the reviewer to ask whether the two modules are really two modules.

"It was easier" is not a reason. If a dependency feels necessary but wrong, the usual causes
are: the logic belongs in a third module both may depend on; the caller should receive an
interface rather than reach for an implementation; or the boundary was drawn in the wrong
place — which is an honest finding worth an ADR of its own.

## Running the checks

```bash
cd backend
go run ./tools/dthclint all       # every check; what CI runs
go run ./tools/dthclint arch
go run ./tools/dthclint phi
go run ./tools/dthclint testonly
```

## The test-only check

Some constructors exist so that a test can build a value production code must never build
by hand — `eventstore.ActorForTest`, `httpx.CallerForTest`. The compiler cannot express
"only from a test", so `dthclint testonly` does: a function whose doc comment carries

```go
//dthclint:testonly
```

may be called from `_test.go` files and nowhere else, including from `cmd` and `tools`. The
next test-only door gets the same treatment by writing the directive above it, in the change
that opens it. See `docs/write-path.md` for why the first one exists.

## The PHI check

The second check exists because logging a patient's name is the easiest serious mistake to
make in clinical software. Logs are not access-controlled, are not covered by the clinical
audit trail, and are routinely shipped to third-party services.

`dthclint phi` fails the build when a patient identifier or credential is used as a
structured-logging key: `name`, `nid`, `national_id`, `phone`, `address`, `dob`, `email`,
`diagnosis`, `password`, `token` and similar.

Log `patient_id` instead, and resolve it through the audited path when a human genuinely
needs to know who it is. Where a flagged key provably holds no patient data, suppress it
**on that line, with a reason**:

```go
log.Info("support ticket", "name", deviceLabel) // phicheck:ignore device label, not a person
```

A suppression without a reason is a review comment.
