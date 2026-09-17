# ADR-0038 · Rendering a code for a human is one leaf module

- **Status:** **Accepted under standing delegation** — Amlan, 13 Sep 2026. An engineering
  decision. The clinical content it renders — `core.observation_code`'s display names and the
  `looks_for` phrase on each QA rule — is Dr Nahid's and is reviewed as reference data, not here.
- **Date:** 2026-09-13
- **Checkpoint:** CP83 follow-up (defect found by reading a screenshot)
- **Deciders:** Amlan Sarkar
- **Blueprint reference:** §3 step 10, §6, §10.6
- **Related decisions:** ADR-0037 (a module boundary drawn around one screen) · CP42 · CP82 · CP83

## Context

Two checkpoints independently put an internal code in front of a clinician.

CP83's QA review screen rendered `no HBA1C in the last 6 months, and none ordered`, and
`no MONOFILAMENT_LEFT or MONOFILAMENT_RIGHT in the last 1 year`. CP82's suggestion panel rendered
`obs.hba1c:2026-09-01 · obs.egfr:2026-09-01 · dx.type_2_diabetes_mellitus`.

Neither is a typo or a translation gap. Both are what a screen looks like when it is written by
somebody holding a `[]string` of codes and no way to ask what they are called — and both were
found by a person looking at a picture, not by a test, because every test asserted that the
finding *named the right code*, which it did.

The important part is that it happened twice, independently, for the same reason. A third screen
would have done it again. So the fix is not two fixes.

## Decision

**One module, `internal/clinicalterm`, owns the question "what is this code called, in the
language the reader is reading". Every module that renders a code uses it.**

`architecture.json` gains:

```
"clinicalterm": ["platform"]
```

and `clinicalterm` is added to `qa` and `prescription`.

It is a leaf, depending on `platform` and nothing clinical, so any module that renders a code can
reach it without the graph acquiring a cycle. It reads `core.observation_code`, which is
facility-independent reference data, and takes no patient identifier, no name and no value — so
it carries no PHI and needs no facility scope.

It also owns the phrasings that were wrong for the same underlying reason: `in the last 1 year`
rather than `in the last year`, `in the last 180 days` rather than `in the last six months`,
`2026-09-01` rather than `1 Sep 2026`, and Latin digits inside a Bangla sentence. Those are not
about codes, but they are all "a value formatted by whoever happened to be holding it", and one
place that gets them right is worth more than three places that each nearly do.

## What the module deliberately does **not** decide

**Which codes mean one clinical thing together.** `CHOL_LDL, CHOL_TOTAL, CHOL_HDL, TRIGLYCERIDE`
is a lipid profile — but that is a statement about what *one QA rule* means by listing them, not
a fact about the codes: a different rule could list `CHOL_LDL` alone and mean an LDL. A dictionary
of "codes that go together" living in this package, or worse in the web client, would be
reference data in a place nobody with `qa.rule.write` can edit.

So that phrase is a column on the rule — `core.qa_rule.looks_for_en` / `looks_for_bn` — beside the
codes it summarises, changed by the same person with the same permission and no release. The
database refuses a rule that names several codes, or any counselling item, without one.

## Consequences

- A screen that renders a code now has a shared, tested way to render it, and the next checkpoint
  that needs one does not reach for the string it is holding.
- One more module in the allowlist, and one more thing to wire in the composition root. The
  lexicon is a process-long cache: a code added by a migration while the server runs is *spelled*
  rather than named until the next restart. That is a worse sentence and not a wrong one.
- An unknown code renders as its words (`Chol ldl`) rather than as itself. That is deliberately
  imperfect English: it is the honest signal of a row that needs fixing, it is reported by
  invariant 138, and it cannot be mistaken for content the way `CHOL_LDL` could.
- The code is never discarded. Findings carry `subject_codes` and suggestions keep their raw
  `basis`, rendered by the client as a detail, so a person debugging a rule at eight in the
  evening is not left worse off than the officer.

## What was considered and rejected

**A lookup table in the web client.** Fastest, and wrong in two ways: it duplicates reference data
the database already owns, and it would have to be duplicated again in the mobile app.

**Rendering at write time — storing the sentence beside the row.** Rejected for CP82's `basis`
specifically: the stored row must stay exactly what CP72's grounding arm validated, and a
rendering frozen at offer time goes stale the day somebody corrects a display name. Rendering on
read costs one cached map lookup and is always current.

**Putting it in `clinical`, which owns observations.** `clinical` is not a leaf; `prescription`
may not import it, and making it able to would open a much wider door than the one this needs.
