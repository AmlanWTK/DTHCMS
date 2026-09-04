# ADR-0028 · Medical history and allergies are their own modules, holding items that outlive a visit

**Status:** Accepted · **Date:** 4 Sep 2026 · **Checkpoints:** CP53, CP54 · **Amends:** §8.2's module list

## Context

The plan is explicit about CP53: _"Uses the CP42 observation tables; no new schema."_ It says
much the same about CP54. §8.2's module allowlist has no `history` and no `allergy` in it,
which means the plan expected both to live inside `clinical`.

That instinct is right and worth taking seriously. CP42 exists precisely so that the eleventh
station does not invent an eleventh shape, and every checkpoint that adds a table is a
checkpoint that made the timeline, the research extract and the FHIR mapping harder.

But an observation and a history item are not the same kind of thing, and the difference is
not a matter of taste.

An **observation** is a point measurement: one code, one value, one moment, and it never
changes. That immutability is what makes the ledger, the timeline and the correction workflow
work at all.

A **history item** has an identity that persists across visits. A complaint that started three
weeks ago is still there next month. A patient is still on metformin, or has stopped. Two of
CP53's four acceptance criteria are statements about that persistence:

> 3. Prior history is presented for confirmation at the next visit, never auto-accepted.
> 4. Every item is individually attributed.

Neither is a sentence one can say about a point measurement. "Confirm the reading you took
last month" means nothing. "Is she still on metformin?" is the entire job of station 4.

Forcing it into `read.observation` leaves two options and both are bad. A JSON blob in a
`structured` value is free text wearing a schema — and criterion 1 is precisely that complaints
are **not** free text. One observation per item, with the concept in `value_code`, throws away
the duration, the severity, the onset and the relation, which are the parts a doctor reads.

CP51 met the same wall from the other side and resolved it the same way, adding
`core.observation_answer` so that "coded" would mean something rather than being a hope.

## Decision

### 1. History is a module, with an item that has an identity

`internal/history`, allowed to import `platform`, `eventstore`, `rbac` and `visit` — the same
set `clinical` has, and nothing more. `read.history_item` holds the item; four events give it
a life: recorded, confirmed, amended, removed.

The kind of item (complaint, comorbidity, family, surgical, medication, vaccination) is a row
in `core.history_kind`, and so are its rules — which kinds require a relation, which require a
duration, which carry a severity, which are drugs. Those rules are checked by a trigger on the
read model, so a projection rebuild and a hand-written `UPDATE` meet them too.

### 2. Allergies are a second module, not a seventh kind of history

CP54 says allergies are "deliberately separate because of the hard stop", and that separation
survives into the code. Everything else in a history is a fact somebody may record or not; an
allergy is a **gate**, and the difference is not the content but what the rest of the system is
allowed to do when the answer is missing. A module boundary is what stops a later change to
history's write path quietly weakening a prescribing block.

### 3. What is reused, and reused strictly

Every item carries CP52's coding — a system, a version and a code, all three or none — with a
foreign key to `core.terminology_concept`. A history kind names the catalogue it draws on, so a
screen cannot file an ICD diagnosis as a presenting complaint and make the record assert that a
patient _presented with_ type 2 diabetes.

Smoking and alcohol are **not** duplicated here. They are lifestyle observations at station 6,
CP42 codes, and the history read returns them by reference. A second copy would be two answers
to one question, and the wrong one would be whichever the reader happened to open.

### 4. Confirmation is an event, never a default

`confirmed_at` and `confirmed_by` have no column default, and a standing invariant fails the
deployment if one appears. This is the shape the failure would actually take — not a bad row,
but somebody adding `DEFAULT now()` to make a test pass, after which every item reads as
confirmed and none was.

## Consequences

**Two new modules and two new read models.** §8.2's allowlist is amended by this ADR, which is
what the allowlist's own rule requires.

**A history item is not on the observation timeline.** The timeline is measurements; a history
is what the patient brought with them. They are different reads and CP74's scrubbable timeline
will have to compose them rather than getting both from one table. That is the real cost here.

**The uncoded escape hatch is deliberate.** An item may carry no coding as long as it says what
was meant, because a history officer meets things the catalogue has no code for and refusing to
record them pushes the item into a note field where nothing can find it. Uncoded items are
countable, and that count is what tells Dr. Nahid which concepts to add.

**Criterion 2 is not finished.** Medications carry a reconciliation state and a nullable
formulary product id, and the id is null on every row until the formulary exists — a later
checkpoint. That is the honest reading of "link to formulary products **where they exist**",
and recording the state now means the day the formulary lands the work is matching rows rather
than migrating a record with nowhere to put the answer.
