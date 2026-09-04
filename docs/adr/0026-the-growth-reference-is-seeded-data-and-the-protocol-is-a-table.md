# ADR-0026 · The growth reference is seeded data, and the protocol that picks it is a table

**Status:** Accepted · **Date:** 4 Sep 2026 · **Checkpoint:** CP47 · **Decisions:** D-21

## Context

[R-06] requires paediatric height, weight and BMI percentiles and z-scores from exact age,
with childhood obesity flagged at the 95th percentile. D-21 resolved which reference to use:
**WHO Child Growth Standards below 5.0 years, CDC 2000 from 5.0 years**, and recorded a
promise — that the choice would be **reversible by Dr. Nahid without a migration**.

Three things had to be decided to build it, and each has a way of going quietly wrong.

## Decision

### 1. The parameters are seeded rows, not code

`core.growth_lms` holds 1,452 rows of published L, M and S. They are inserted by a migration
and read at request time; nothing computes them and nothing is hard-coded in Go.

The alternative — a Go table, or worse a Go table _and_ a TypeScript one — is a table that
drifts. Two copies of an equation drift loudly, because a fixture catches the disagreement.
Two copies of a table drift silently, one row at a time, and no fixture catches the row
nobody happened to test.

**This is why CP47 has no TypeScript twin**, which ADR-0025 would otherwise require. What
ADR-0025 protects is a _formula_: a dozen lines two people can independently get right, where
a shared fixture proves they did. A lookup table is a different animal, and the right
protection for it is one copy plus provenance.

### 2. The protocol is `core.growth_band`, a table of three rows per indicator

Which standard applies to which age is data:

| Indicator      | 0 – 60 months | 60 – 240.5 months |
| -------------- | ------------- | ----------------- |
| Height-for-age | `WHO_2006`    | `CDC_2000`        |
| Weight-for-age | `WHO_2006`    | `CDC_2000`        |
| BMI-for-age    | `WHO_2006`    | `CDC_2000`        |

Changing the clinic's protocol is an `UPDATE` and a recomputation over stored measurements —
not a migration and not a release. That is what D-21 promised, and it is only true because
this table exists. Every stored percentile also carries its standard **and version**, so a
value computed in 2026 stays interpretable after the protocol moves.

### 3. The validation set is the publishers' own arithmetic

Both WHO and CDC print, beside their L/M/S, the cut-offs those parameters produce — WHO's
−3 SD … +3 SD columns and CDC's P3 … P97 columns. All of them are in
`packages/clinical-calc/fixtures/growth-reference.json`: 1,452 age points, roughly twelve
thousand printed values, **none of which anybody on this project wrote**.

`TestEveryPublishedCutOffIsReproducedFromTheSeededParameters` recomputes every one of them
from the seeded rows, comparing at the precision each value was printed to. A parameter
transcribed wrongly fails the build rather than agreeing with itself, which is the only kind
of check worth having over a table this size. `tools/growth/build_reference.py` is the script
that produced both artefacts, so the derivation is reproducible rather than a claim.

## Consequences

**Good.** One copy of the reference data, with provenance and a validation set nobody here
authored. D-21 stays reversible as promised. A client asks for percentiles rather than
carrying 100 KB of tables it would have to keep in step.

**Costs.** A phone with no signal cannot compute a percentile — unlike a BMI, which
`@dthcms/clinical-calc` computes locally. That is a real limitation and the right trade: a
percentile computed offline against a stale copy of the tables would be worse than one that
waits.

**The gap at five years, named rather than hidden.** WHO's last published row is at 60.0
months; CDC's first is at 60.5, because CDC publishes on half-months. A child in that
fortnight is inside the CDC band and outside the CDC table. The engine scores them against
CDC's first published row — `edgeToleranceMonths`, one month, checked by an invariant.
Moving the handover to 60.5 would contradict a recorded clinical decision to save a
fortnight; returning "not applicable" would tell a parent their five-year-old cannot be
plotted.

**Still owed, and the checkpoint is not complete without it.** Dr. Nahid checking ten
paediatric cases against the printed charts he uses today. No test can stand in for it: what
it verifies is that the right standard was chosen for _this_ clinic and this population,
which is a clinical question. D-21 itself carries the caveat — neither reference is derived
from South Asian children, and both under-call adiposity-related metabolic risk at a given
BMI in this population.

## Reversal

Change the rows in `core.growth_band`, recompute the stored percentiles, and historical
values remain interpretable because each names the standard it was computed under. Adding a
third standard is a migration that seeds its rows and a band that points at it — no code
change.
