# The lifestyle assessment

CP58. Blueprint §3 step 3, §12. Station 3's structured behavioural data and the composite risk
score §12's cohorting reads.

---

## D-26 blocks the content, not the mechanism

The plan lists **D-26 — some validated instruments are copyrighted** as an open decision and says
_"D-26 blocks the score."_ Three separate things hide in that sentence and they have different
answers.

**The instruments.** PSS-10, PHQ-9 and IPAQ are the obvious choices for stress and activity. Each
is either copyrighted or free under terms written with academic research in mind rather than a
fee-charging clinic. Typing their wording into a migration and sorting the licence out later is the
ordinary way this goes wrong, and it goes wrong invisibly: a questionnaire in a database looks
exactly like a questionnaire somebody had the right to put there.

**The composite formula.** Nobody has agreed one. A score shipped without that agreement would be
used, cited and compared, and there would afterwards be no way to tell a score computed under the
agreed formula from one computed under a developer's guess.

**The mechanism.** None of that blocks it. An instrument is rows, a response is rows, a total is a
function of the rows, and a score is a derived observation.

So the mechanism shipped, and the content is a seed change. ADR-0030 has the full argument.

## What is registered, and what may be run

| instrument    | domain    | usable | why                                                                         |
| ------------- | --------- | ------ | --------------------------------------------------------------------------- |
| `AUDIT_C`     | alcohol   | yes    | published by the WHO for free use, including in clinical settings           |
| `READINESS_1` | readiness | yes    | one question, written here, and labelled as this clinic's own               |
| `PSS_10`      | stress    | **no** | Cohen / Mind Garden; clinical use in a fee-charging clinic unconfirmed      |
| `PHQ_9`       | stress    | **no** | Pfizer; widely said to be free, and that has not been verified here         |
| `IPAQ_SHORT`  | activity  | **no** | free for non-commercial research; a fee-charging clinic is a different case |

The three unusable ones are **in the table**, with no items and a note saying what has to be
confirmed. The row exists so the absence reads as a decision rather than an oversight, and so a
screen can say "not licensed here" instead of quietly omitting a questionnaire a clinician expected
to find. `core.assert_no_unlicensed_instrument_holds_items` fails if any of them acquires an item —
CP52's SNOMED handling, applied again for the same reason: "we remembered not to type it in" is not
a control.

`READINESS_1` is deliberately labelled as this clinic's own writing rather than presented as an
instrument. §3 step 3 asks for readiness-to-change and every validated alternative is behind D-26;
one honest question is better than an unlicensed good one, and better than nothing.

## Raw items, and no stored total

Acceptance criterion 1, and it decides the schema. §12's cohorting is done on behaviour, and a total
of 14 on a scale cannot be re-analysed, re-scored under a corrected formula, or compared against a
study that weighted the items differently. Stored items are data; a stored total is a number
somebody has to trust.

There is deliberately **no `total` column**. The total is `core.instrument_total(response)`, a
function over the item rows, and every payload computes it on the way out. Two columns that ought to
agree are two columns that will not, and on the day they disagree nobody can say which was right. A
test asserts the column does not exist.

**The score of each answer comes from the server.** A client sends the option chosen; the points
come from the option table. A client that could send a score could send any total it liked, and the
total is what the research reads.

**A response names the version it answered** (criterion 4). An instrument whose wording changes next
year must not make this year's answers read as answers to the new question.

**Answering again supersedes.** The old response keeps its row, the same rule every clinical value
follows: an operator who ran the questionnaire twice because the patient corrected themselves has
produced two facts, not one edit.

## The composite

`LIFESTYLE_RISK`, an ordinary CP42 derived observation — so it inherits the correction cascade, the
timeline, the research extract and the attribution without a line of new code.

Its formula version is **`0.1.0-proposed`** and the suffix is load-bearing: a score computed today
must be identifiable forever as one computed before anybody agreed to the arithmetic. When a
clinician approves a formula that is `1.0.0`, and every score under the old string is visibly a
different measurement.

Four domains, each scored 0–1 and averaged, then × 100:

| domain   | read from              | zero at        | one at          |
| -------- | ---------------------- | -------------- | --------------- |
| smoking  | `PACK_YEARS` (derived) | none           | 30 pack-years   |
| alcohol  | the AUDIT-C total      | 0              | 12              |
| sleep    | `SLEEP_MINUTES`        | 7–9 hours      | 4 hours outside |
| activity | `ACTIVE_MINUTES_WEEK`  | 150 min a week | none            |

Every band is a named constant, so that changing one reads as a change to a judgement rather than to
a number.

**A missing domain is left out of the mean, never counted as zero.** The obvious implementation
scores four out of four and treats an unanswered domain as nothing; it is wrong in the worst
possible direction, because the patient who answered nothing then scores best and the operator who
ran the whole questionnaire has produced a worse-looking record than the one who ran none of it.

**Below three domains it refuses.** A "lifestyle risk" from one answer is a number whose name
promises far more than it holds. The response is still recorded — the answers are the record — and
the API returns `score: null` beside it rather than saying nothing, because an operator who finished
a questionnaire and saw no score would reasonably assume the save failed.

The formula exists twice, in Go and in TypeScript, held together by the shared fixtures in
`packages/clinical-calc/fixtures/reference.json` — the same discipline every derived value in this
system follows (ADR-0025).

## Pack-years, finally reachable

CP43 declared `PACK_YEARS` and left it refusing to compute, because nothing recorded a smoking
history. CP58 gives station 3 `CIGARETTES_PER_DAY` and `SMOKING_YEARS`, so the derivation works —
and, because CP62's cascade reads the derivation registry, a correction to either input now moves
the pack-years and the composite with it.

## Units

`SLEEP_MINUTES` and `ACTIVE_MINUTES_WEEK` are stored in canonical minutes under a new `duration`
dimension, so an operator may type either hours or minutes and the record holds one number. That is
CP42's whole argument applied to a place it would have been easy to skip: without it, the operator
who thinks in hours and the one who thinks in minutes record the same amount of sleep as two
different numbers under one code.

The counts — cigarettes a day, years smoked — are dimensionless, in the same sense `PACK_YEARS`
already was. "Twenty a day" is a pure number and the period is in the code's name, not in a unit
anybody could convert.

## Deliberately not built

- **No stress or activity questionnaire.** Both candidates are behind D-26. The domains are captured
  as plain numbers instead, which is worse data, and the fix is a licence rather than code.
- **No scoring of numeric items.** Scoring a free number needs a band table nobody has agreed, and
  inventing one here would be inventing clinical evidence. The number is stored and the composite
  reads it directly.
- **No trend.** "Has this patient's lifestyle risk improved" is a real question and a chart on the
  physician dashboard (CP73), not a second stored number here.

## Open questions for the clinic

- **D-26 itself**: which instruments this clinic may run. Every unusable row names what must be
  confirmed.
- **The composite's bands.** Thirty pack-years, an AUDIT-C of twelve, the seven-to-nine sleep window,
  the WHO's hundred and fifty minutes — all proposals.
- **The three-domain floor**, and whether a score from three domains should be shown beside one from
  four at all.
- The Bengali wording of every question and option, drafted here and not reviewed by a clinician.
