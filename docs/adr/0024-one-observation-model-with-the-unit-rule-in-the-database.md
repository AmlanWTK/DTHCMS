# ADR-0024 — One observation model, with the unit rule in the database

**Status:** Accepted · **Date:** 2026-09-14 · **Checkpoint:** CP42

## Context

Ten of the clinic's twelve stations record measured values: heights and weights, blood
pressures, lab results, examination findings, a patient's own 1–10 score. §6 names seven
categories and five value shapes.

Two things had to be decided.

**How many tables.** A table per station is the obvious first move and the one that makes the
timeline, the research extract and the FHIR mapping ten times harder — and guarantees that
the eleventh station invents an eleventh shape.

**Where the unit rule lives.** Unit errors are a classic patient-safety failure. A weight of
154 with no unit is 154 kg or 154 lb depending on who reads it, and a drug dose computed from
it is wrong by a factor of 2.2. The blueprint asks for this to be _structurally prevented_.

## Decision

**One table, one event type, one write endpoint, and a code registry that says what each kind
of value is.** `core.observation_code` declares a code's category, shape, unit dimension,
plausibility band and required permission. Adding a station is adding rows.

**The unit rule is a trigger on the read model, not a validation in a service.**
`core.observation_is_well_formed()` refuses a row lacking the unit its code requires — from
any path, including a projection rebuild and a hand-written `UPDATE`. A standing invariant,
`core.assert_measurements_carry_their_units()`, fails a deployment where the trigger has been
weakened.

The Go service checks the same things first, so an operator sees a sentence in their own
language rather than a 500. That is not duplication to be removed: **the service protects the
person typing and the trigger protects the record.**

**Both values are stored** — the canonical one for arithmetic and the entered one exactly as
typed. Reading a value back in the unit it was entered in is then a read rather than a round
trip.

**The conversion happens in the database, on the way into the read model.** Not in the
ledger: converting on the way in would freeze today's factor into every event ever written.
Not in the client: a client that computed the canonical value could arrive at a different one
from the server's.

## Consequences

**Good.** One shape for the timeline, the research extract and FHIR to consume. The unit rule
is true of every row however it arrived. A conversion factor corrected later corrects the
whole history on the next rebuild. A new station is data.

**Costs.** A generic model is harder to read than `heights (id, patient_id, cm)` — the
registry has to be consulted to know what a row means. `read.observation` is wide, with five
value columns of which exactly one is set. Category-based RBAC needs a per-request lookup of
the code's permission, because the permission a write needs depends on the body rather than
the route.

**The risk the plan named** — over-generalisation making the model unusable — is real and was
checked the way the plan asked: the model was validated against five station forms
(anthropometry, vitals, examination, lab, patient-reported) before the registry was seeded,
and all five fit without a special case.

## Alternatives considered

**A table per category.** Seven tables instead of ten is not a different answer, and the seven
still differ only in what they mean.

**EAV with a `text` value column.** Loses the plausibility band, the unit dimension and every
index that makes a trend query fast.

**Enforcing units in the API only.** The projection rebuild path would not hit it, and a
rebuild is exactly when a systematic error becomes permanent.
