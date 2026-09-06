# ADR-0030 · Questionnaires are rows, the copyrighted ones are registered unusable, and the composite score says it is a proposal

**Status:** Accepted · **Date:** 5 Sep 2026 · **Checkpoints:** CP58 · **Amends:** §8.2's module list · **Relates to:** D-26

## Context

CP58 asks for station 3's lifestyle data and a composite Lifestyle Risk Score. The plan lists
**D-26 — some validated instruments are copyrighted** as an open decision, and says plainly:
_"D-26 blocks the score. The data capture can proceed with the formula deferred, if Dr. Nahid
prefers."_

Three separate problems hide inside that sentence, and they have different answers.

**The instruments.** PSS-10, PHQ-9 and IPAQ are the obvious choices for stress and activity. Each
is either copyrighted, or free under terms that were written with academic research in mind rather
than a fee-charging clinic. Typing their wording into a migration and sorting out the licence later
is the ordinary way this goes wrong — and it goes wrong invisibly, because a questionnaire in a
database looks exactly like a questionnaire somebody had the right to put there.

**The composite formula.** Nobody has agreed one. A score shipped without that agreement would be
used, cited and compared, and there would be no way afterwards to tell a score computed under the
agreed formula from one computed under a developer's guess.

**The mechanism.** None of the above blocks it. An instrument is rows, a response is rows, a total
is a function of the rows, and a score is a derived observation. All of that can ship, be tested,
and be correct before anybody decides which questionnaires go in it.

## Decision

### 1. An instrument is rows, versioned, with its licence on the row

`core.instrument`, `core.instrument_version`, `core.instrument_item`, `core.instrument_option`.
Adding a questionnaire is a seed change; changing its wording is a new version, and a response
names the version it answered so that last year's answers do not silently become answers to this
year's question.

Each instrument carries `copyright_holder`, `licence_note` and `usable`. A usable one **must** have
a licence note — a `CHECK` refuses an empty one — because a licensed row with nothing written on it
is how a licence nobody can produce becomes a licence nobody remembers questioning.

### 2. The copyrighted ones are registered by name, unusable, and an invariant refuses their items

PSS-10, PHQ-9 and IPAQ-short are **in the table**, with `usable = false`, no items, and a note
saying exactly what has to be confirmed. The row exists so the absence reads as a decision rather
than an oversight, and so a screen can say "not licensed here" instead of quietly omitting a
questionnaire a clinician expected to find.

`core.assert_no_unlicensed_instrument_holds_items` fails if any of them acquires an item. This is
CP52's SNOMED handling, applied again for the same reason: "we remembered not to type it in" is not
a control.

What ships usable is **AUDIT-C**, which the WHO publishes for free use and reproduction, and a
single readiness question **written here** and labelled as this clinic's own so that nobody later
cites it as though it were published.

### 3. Raw item responses, and no stored total

`read.instrument_answer` holds one row per item. Acceptance criterion 1 asks for it and §12 is the
reason: a total of 14 cannot be re-analysed, re-scored under a corrected formula, or compared with a
study that weighted the items differently. Stored items are data; a stored total is a number
somebody has to trust.

There is deliberately **no `total` column**. The total is `core.instrument_total(response)`, a
function over the rows. Two columns that ought to agree are two columns that will not, and on the
day they disagree nobody can say which was right.

### 4. The composite is a DERIVED observation, and its version says it is a proposal

`LIFESTYLE_RISK` is an ordinary CP42 derived value, so it inherits the correction cascade, the
timeline, the research extract and the attribution without a line of new code.

Its formula version is **`0.1.0-proposed`**, and the suffix is load-bearing. D-26 has not been
answered; a score computed today must be identifiable forever as one computed before anybody agreed
to the arithmetic. When a clinician approves a formula, that is `1.0.0` and every score under the
old string is visibly a different measurement.

### 5. A missing domain is not zero

The score is the **mean of the domains actually assessed**, and it reports how many those were.

The obvious implementation scores four domains out of four and treats an unanswered one as nothing.
It is wrong in the worst possible direction: the patient who answered nothing scores best, and the
operator who ran the whole questionnaire has produced a worse-looking record than the one who ran
none of it. Below three domains the calculation refuses outright — a "lifestyle risk" computed from
one answer is a number whose name promises far more than it holds.

### 6. `internal/assessment`, importing `platform`, `eventstore`, `rbac`, `visit`, `clinical`

Added to `backend/architecture.json`. It could have gone in `clinical`, and that was tried: an
instrument is not an observation, and forcing one into `read.observation` leaves the same two bad
options CP53 met — a JSON blob wearing a schema, or one observation per item, which throws away the
version, the item order and the option that was chosen.

It imports `clinical` rather than duplicating it, because the score it produces **is** a clinical
derived value and must be written by the one code path that knows how to write those. The
dependency runs assessment → clinical and never back.

## Consequences

**Good.** The mechanism ships and is tested before the content decision is made, which is the only
arrangement under which D-26 stops being a blocker. A questionnaire added later is a seed change.
Nothing copyrighted can be entered without somebody first writing down a licence, and the database
says so rather than a comment. The score cannot be mistaken for an approved one.

**Bad, and we accept it.** The clinic ships with **one** validated instrument and one question of
its own, which is thin for a station whose purpose is behavioural assessment — the stress and
activity domains are captured as plain numbers rather than through a validated scale, and that is
worse data than the alternative. The composite therefore runs on four domains where §3 step 3
describes six. Both improve the day D-26 is answered, and neither requires code.

**Also bad.** A score's meaning depends on which domains went into it, and a client that ignores
`domains` will compare two numbers that are not comparable. The field is required in the contract
and the value carries it, but nothing can force a reader to look.

**Unresolved.** D-26 itself: which instruments this clinic may run, and what the composite weighs.
Every band in the formula — thirty pack-years, an AUDIT-C of twelve, the seven-to-nine sleep window,
the WHO's hundred and fifty minutes — is a named constant precisely so that changing one reads as a
change to a judgement rather than to a number.
