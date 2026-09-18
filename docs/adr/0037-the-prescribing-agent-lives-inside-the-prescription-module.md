# ADR-0037 · The prescribing agent lives inside the prescription module

- **Status:** **Accepted under standing delegation** — Amlan, 13 Sep 2026. This one is an
  engineering decision rather than a clinical one, and Dr Nahid does not need to read it. What he
  does need to read is `docs/ai-prescribing-suggestions.md`, which this ADR implements.
- **Date:** 2026-09-13
- **Checkpoint:** CP82
- **Deciders:** Amlan Sarkar
- **Blueprint reference:** §3 step 9, §7.2, §7.3, §10.6 · D-28
- **Related decisions:** ADR-0007 (AI tier) · ADR-0033 (grounding is about the payload) · CP70 ·
  CP71 · CP80

## Context

`backend/architecture.json` is a module dependency allowlist and its header says changing a rule
requires an ADR. CP82 changes one rule:

```
"prescription": ["platform", "eventstore", "rbac", "visit", "formulary", "medsafety", "ai"]
```

`ai` is new. The question is whether the prescribing agent belongs inside the prescription module
or in a module of its own.

CP71 answered the same question the other way: `synthesis` is its own module and imports `ai`.
That was right there — a pre-consultation briefing is not a prescription, it is written by a
different trigger, read on a different screen, and stored in a table nothing else references.

CP82 is not that shape. The checkpoint is judged on one sentence:

> **No AI-suggested item can reach a SIGNED prescription without an explicit accept or edit
> event.**

and the specification is explicit that this is to be held _structurally_ rather than by
validation: _"nothing copies one into the other except an explicit physician action that records
who did it and when. There is no code path … by which a suggestion becomes a line without a person
doing it one line at a time."_

## Decision

**The prescribing agent is part of `prescription`, and `prescription` may import `ai`.**

### 1. Because a separate module would have had to be given the path this rule forbids

A `prescribing_ai` module holding the suggestions would still need the accepted line to appear on
the prescription. It cannot write `read.prescription_item` — nothing can; the application role
holds SELECT and the line arrives as a ledger event — so it would have to ask `prescription` to
append one. That means an exported method on `prescription.Service` taking a product, a dose and a
suggestion id.

**An exported method that turns a suggestion into a prescription line is exactly the code path the
criterion says must not exist.** Whoever imports `prescription` could call it: `qa`, `pharmacy`,
`fhir` and any module written later. The rule would then be held by everybody remembering not to,
which is the shape of rule this project has decided repeatedly not to build.

Inside the module the path can be made unreachable instead:

- `Addition` — the only argument `Service.AddItem` takes — has **no field naming a suggestion**,
  and there is nowhere to add one without editing this package.
- The unexported `Service.addItemTx` takes the origin as a second argument, and exactly one caller
  passes a non-nil value: `Service.Decide`.
- `Decide` writes the decision row and the item event **in one transaction**.

`TestThereIsNoWayToMarkALineAsAIWithoutADecision` asserts the first of those by reflection, so
adding the field is a failing test in the same commit rather than a behaviour change noticed later.

### 2. And because the database half is where the guarantee actually lives

The Go argument above is worth having and is not the guarantee. The guarantee is
`core.ai_origin_item_has_a_decision()`: a **deferred constraint trigger** that refuses to let a
transaction commit if `read.prescription_item.ai_suggestion_id` is set and no ACCEPTED or EDITED
decision names that exact line. It holds for `dthcms_projector`, for a support script, and for a
developer with the application's password, and `TestTheDatabaseRefusesAnAIOriginLineWithNoDecision`
drives it as the projector role to prove it.

Deferred rather than immediate because the decision names the line and the line names the
suggestion, so at INSERT time each is waiting for the other. Deferring makes the question the only
one worth asking — _may this transaction commit?_

Invariant 130 asks the same thing of the whole table afterwards, in both directions, so a row
written with triggers disabled is found by the verifier rather than by a physician.

### 3. What crosses the boundary is still narrow

`prescription` gains `ai` and **nothing else**. It still may not import `synthesis`, `patient` or
`allergy`. Three interfaces, implemented by bridges in `cmd/api`, are what actually cross:

| Interface                  | What crosses                                   | Why not an import                                                                  |
| -------------------------- | ---------------------------------------------- | ---------------------------------------------------------------------------------- |
| `ai.Gateway` (concrete)    | the only path to a model                       | the point of CP70 is that there is one                                             |
| `prescription.Briefing`    | CP71's assembled context and an `ai.Subject`   | `prescription` may not read a patient record                                       |
| `prescription.AllergyGate` | one word: `core.allergy_status()`'s own answer | CP54 settled that question in one function and a second answer is the failure mode |

## Alternatives considered

**A separate module with an exported `AddItemFromSuggestion` on `prescription`.** Rejected above:
it is the path the criterion forbids, exported.

**A separate module that appends the ledger event itself.** Worse. The event would then be built
in two places — CP80's `AddItem` captures the price of the day, resolves the product's words and
validates the line, and a second builder would drift from it. §1 requires an accepted suggestion to
get _no easier passage_ than a typed line, and two builders is how one of them gets an easier one.

**Hold the rule only in the database and let any module write the line.** Tempting, because the
database half is the strong half. Rejected because the Go half is what makes the failure a compile
error or a failing type test rather than a runtime refusal discovered by a physician mid-clinic —
and because "the constraint will catch it" is the argument that precedes every constraint somebody
later disables to get a migration through.

## Consequences

**Good**

- The criterion is held by a type and by a trigger, neither of which depends on a reviewer noticing.
- One function builds every prescription line, so CP80's price capture, validation and event shape
  apply to an accepted suggestion automatically.
- `architecture.json` still refuses `prescription` → `synthesis`, `patient` and `allergy`, which are
  the imports that would actually widen what this module can read.

**Bad — and accepted knowingly**

- `prescription` is now a large package: the aggregate, the machine, the editor's content, the
  safety seam and the agent. It is the biggest module in the repository and it will get bigger at
  CP83 and CP84. The split, when it comes, should be by _file_ rather than by module, because the
  thing holding CP82's criterion is precisely that these types share a package.
- The API process now builds an `ai.Gateway`, which CP71 deliberately avoided. That trade is argued
  where it is made, in `cmd/api/main.go`: a queued suggestion that arrives after the physician has
  signed is not a slower feature but no feature. The cost is bounded by the agent's own timeout and
  a failed run is a sentence on a screen.

**Revisit when**

- A second module needs to propose prescription lines. At that point the right move is a shared
  _decision_ type inside `prescription`, not an exported write.
- The package passes the size at which a reader cannot hold it. Split by file first, and only split
  by module if the criterion above can be restated in the database alone.
