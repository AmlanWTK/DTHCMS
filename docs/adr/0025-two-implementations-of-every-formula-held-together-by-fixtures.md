# ADR-0025 — Two implementations of every formula, held together by fixtures

**Status:** Accepted · **Date:** 2026-09-14 · **Checkpoint:** CP43

## Context

P-4 asks for derived clinical values on screen the instant an operator types the inputs: a
BMI while they are still standing at the scale, on a tablet that may have no signal. That
means computing on the client.

The server has to compute them too, because the client is not authoritative about anything —
an old app build, a modified one, or a request assembled by hand would otherwise be accepted
as a clinical value.

So there are two implementations of the CKD-EPI equation in this system. **Two
implementations that disagree is a patient-safety bug.**

## Decision

**Both, held together by a shared fixture file, checked in CI.**

`packages/clinical-calc/fixtures/reference.json` holds every case. The Go suite
(`backend/internal/clinical/calc`) and the TypeScript suite (`packages/clinical-calc`) read
the same file and run the same case list through mirrored dispatch switches. "Go and TS agree
on 100% of fixtures" is what green means on both sides, not a claim somebody checks by hand.

**Every expected value was computed independently from the published equation**, not read off
either implementation. A fixture derived from the code would prove only that the code agrees
with itself, which is exactly the failure this exists to catch.

**Every function is versioned, and the version is stored on each derived value.** CKD-EPI was
revised in 2021 to remove a race coefficient; a stored eGFR with no version cannot afterwards
be told apart from one computed under the old equation, and a system that silently recomputed
the old values would rewrite history.

**The record keeps the server's number.** `POST /v1/observations/derive` accepts no value at
all — there is no field for one. The client's copy is for the operator's eyes while they type.

**Every function returns a result or a refusal, never a number it is not entitled to.** A BMI
from a height of zero is `Infinity`, which serialises to `null` and renders as an empty cell:
a wrong answer that looks like a missing one.

## Consequences

**Good.** The instant feedback P-4 asks for, with the server authoritative. A formula added
to one language and not the other is a failing test. A formula whose arithmetic changes must
bump a version, and old values stay interpretable.

**Costs.** Every formula is written twice and every change is made twice. The fixture file is
a third artefact to keep honest — and it is the one that must never be regenerated _from_ the
code, which is a discipline a future contributor could break without noticing.

The tolerance is 1e-9 rather than exact, because the two runtimes order floating-point
operations differently and `pow()` is not required to be correctly rounded in either.

## Alternatives considered

**Compute only on the server.** Simplest, and gives up P-4 — a spinner on every keystroke, and
nothing at all on a tablet with no signal.

**Compute only on the client.** Makes the client authoritative about a clinical value.

**Compile the Go to WebAssembly and ship one implementation.** One implementation is genuinely
better, and the cost is a WASM payload on a low-end Android tablet plus a build step that has
to run for both surfaces. Worth revisiting if the formula set grows much past a dozen; at that
point the fixture file is doing more work than it should.

**Generate both from a shared specification.** A code generator is a third implementation to
get right, and the thing it would generate is a dozen arithmetic expressions.
