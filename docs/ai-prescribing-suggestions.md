# AI prescribing suggestions (CP82)

**Status:** authored by Amlan under Dr Nahid's standing delegation, 13 Sep 2026. The clinical
content below — the rejection vocabulary, what a suggestion may contain, and the refusal rules —
is **physician content and needs Dr Nahid's review.** It is written so that reviewing it is
reading one page, not reading code.

**Decides D-28** (physician approval workflow granularity): **per item, with an optional reason
on rejection.** The plan already assumed per-item. The reason capture is the addition, and it is
what makes the Phase 3 pattern learning worth building — a trail of rejections with no reasons
records that he said no, which is the least interesting half of the fact.

---

## 1. The boundary, stated once

**The AI cannot prescribe. It can only propose, and a proposal is a different kind of object
from a prescription line.**

This is not a policy expressed in validation. It is expressed in the schema: a suggestion lives
in `core.ai_prescribing_suggestion` and a prescription line lives in `core.prescription_item`,
and **nothing copies one into the other except an explicit physician action that records who did
it and when.** There is no code path — no batch accept, no "accept all", no default-on — by
which a suggestion becomes a line without a person doing it one line at a time.

Three consequences worth being explicit about:

- A physician who ignores the AI panel entirely produces a prescription with no AI items in it,
  and the prescription is not marked as having involved AI, because it did not.
- The medication safety engine (CP78) runs on the **final content of the prescription**,
  whatever the origin of each line. An accepted suggestion gets no easier passage than a line
  typed by hand, and a hand-typed line gets no harder one. The engine is not told which is which.
- A suggestion that is never acted on is **not** a rejection. It expires with the draft and is
  recorded as unactioned. Conflating "he said no" with "he did not look" would poison the
  learning signal and, worse, would let silence be read as a decision.

## 2. What a suggestion may contain, and what it may not

A suggestion proposes a **medicine already in the DTHC formulary** (CP75), with a dose, a
frequency, a duration and a route. It carries the reasoning that produced it and the facts it
rests on, because a suggestion a physician cannot audit in five seconds is a suggestion he will
either rubber-stamp or ignore, and both are failures.

A suggestion **may not**:

- name a medicine that is not in the formulary — the clinic cannot dispense it, and a suggestion
  that sends the patient elsewhere is a suggestion that should have been a note;
- propose a controlled or scheduled drug;
- propose stopping or changing a medicine prescribed by another physician outside DTHC, which is
  a conversation, not a line item;
- propose anything for a patient with no recorded allergy status. Not "propose cautiously" —
  propose nothing. CP54 made allergy status a hard stop for prescribing, and a machine that
  suggests around a hard stop teaches people the stop is soft.

The model is given the CP71 synthesis context and the formulary, and is constrained to a schema.
The gateway validates the output against that schema regardless of what the model returns, and a
suggestion that fails validation is **dropped, not repaired** — a repaired suggestion is one
nobody wrote.

## 3. Why suggestions are visually distinct until accepted

Automation bias is the named risk in the plan, and it is the real one. A physician who has
accepted forty correct suggestions will accept the forty-first without reading it.

So: an unaccepted suggestion never looks like a prescription line. It sits in its own column,
in its own visual treatment, and it cannot be reached by the keyboard flow that moves between
prescription lines. Accepting it is a deliberate act with its own target, not the next press of
the key you were already pressing.

The accept rate is worth watching, and watching it is Quality's job rather than the
prescriber's — the same argument as the counselling override rate. A rate near 100% means either
a very good model or a physician who has stopped reading, and those two look identical from
inside the prescription.

## 4. The three decisions

| Decision | What it records | What it produces |
|---|---|---|
| **Accept** | who, when, the suggestion as offered | a prescription line identical to the suggestion |
| **Edit** | who, when, the suggestion as offered **and** the line as issued | a prescription line that differs, with the difference recoverable |
| **Reject** | who, when, an optional reason from §5 and optional free text | nothing |

**Edit is the most valuable of the three and the easiest to get wrong.** If the system records
only the final line, the fact that the model said 500 mg and the physician wrote 850 mg is lost —
and that difference is the entire training signal. So the suggestion is stored as offered, by
value, and is never mutated by the edit.

An accepted or edited line is an ordinary prescription line from that moment on. It can be
changed again, removed, or carried forward, and it follows every rule CP80 already enforces. Its
origin is recorded and does not privilege it.

## 5. Rejection reasons

Optional, because a physician mid-clinic with a patient in front of him should be able to
dismiss a suggestion in one action. Encouraged, because the reason is the signal.

Authored as a clinician. Each has an English and a Bengali label; the Bengali is clinical
register, not literal translation.

| Code | English | Bengali |
|---|---|---|
| `NOT_INDICATED` | Not indicated for this patient | এই রোগীর ক্ষেত্রে প্রযোজ্য নয় |
| `CONTRAINDICATED` | Contraindicated — renal, hepatic or cardiac | প্রতিনির্দেশিত — কিডনি, লিভার বা হৃদযন্ত্রজনিত |
| `ALLERGY` | Allergy or previous adverse reaction | অ্যালার্জি বা পূর্বে বিরূপ প্রতিক্রিয়া |
| `INTERACTION` | Interacts with current therapy | বর্তমান ওষুধের সঙ্গে বিক্রিয়া |
| `DUPLICATE` | Duplicates therapy already prescribed | ইতিমধ্যে দেওয়া ওষুধেরই পুনরাবৃত্তি |
| `WRONG_DOSE` | Dose, frequency or duration wrong | মাত্রা, সময় বা মেয়াদ ঠিক নেই |
| `PREFER_ALTERNATIVE` | Prefer a different agent in this class | এই শ্রেণিতে অন্য ওষুধ পছন্দ |
| `COST` | Patient cannot afford it | রোগীর সাধ্যের বাইরে |
| `AVAILABILITY` | Not reliably available | নিয়মিত পাওয়া যায় না |
| `ADHERENCE` | Adherence or patient preference | রোগীর পছন্দ বা নিয়ম মেনে চলার সমস্যা |
| `TOO_EARLY` | Defer — reassess at the next visit | এখন নয় — পরের বার পুনর্বিবেচনা |
| `INSUFFICIENT_DATA` | Not enough information to decide | সিদ্ধান্ত নেওয়ার মতো তথ্য নেই |

Two notes on this list, for Dr Nahid's review specifically:

- `COST` and `AVAILABILITY` are separate on purpose. In Faridpur they are different facts with
  different fixes — one is answered by a cheaper generic, the other by a supply conversation —
  and collapsing them would lose the distinction in exactly the data that would tell us which
  is the bigger problem.
- `INSUFFICIENT_DATA` is a rejection of the *suggestion*, not of the *drug*. It should
  correlate with CP71's recorded gaps, and if it does not, the synthesis is not surfacing what
  the prescriber is actually missing.

These are stored as reference data, not as a Go enum, so Dr Nahid can change the wording or add
a reason without a code release — the same rule as the counselling templates and the formulary.

## 6. What is deliberately not built

Learning from the decision trail. That is Phase 3. CP82 records the trail completely and
correctly and uses it for nothing, which is the right order: a trail collected badly cannot be
fixed retrospectively, and a model trained on a trail nobody has reviewed should not exist yet.
