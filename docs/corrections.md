# Correcting a value

CP62. Blueprint §4.3, [R-04]. The checkpoint is one scenario, described concretely, and every
decision below is that scenario read closely:

> An operator records a height of 150 cm. The physician sees it, is sure it is 140, and flags it.
> The request reaches the operator who typed it. They correct it. Everything derived from it
> recomputes. Both values stay in the record, with both names against them.

---

## Nothing is edited

The word "correction" invites a mental model where a wrong number is replaced by a right one.
That model is not implemented anywhere in this system, and could not be: the ledger is
append-only by database privilege, so there is no code path that can rewrite a recorded value
even by mistake.

Correcting is **writing a new observation that replaces the old one** — CP42's `Recording.Replaces`,
which already existed. The original keeps its row, stops being `ACTIVE`, and stays queryable
forever. What CP62 adds is the _routing_ around that write: who may ask for it, who is asked, and
what else has to move.

So a corrected height reads, in the record, as two rows:

| value  | status       | recorded by      | replaced by |
| ------ | ------------ | ---------------- | ----------- |
| 150 cm | `SUPERSEDED` | the operator     | the new row |
| 140 cm | `ACTIVE`     | whoever fixed it | —           |

Both are in the patient's history, both name their author, and the one that was wrong is still
there. That is criterion 1, and it is also why "who changed this and when" never needs an audit
screen: the answer is two rows in the same table.

## The request goes to the person who typed it

§4.3 says so, and the reason is the whole point of the mechanism. A workflow where a supervisor
quietly fixes everything produces a clean record and an operator who keeps mistyping. The
request is addressed to the value's author, appears on **their** device, and is theirs to answer.

That routing forced a permission decision that took two attempts to get right. Answering a
request needs no permission at all beyond being someone who records values: asking an operator to
hold a permission before they may fix their own mistake means the mistakes stay. The first
version guarded the answering routes on `observation.read.values`, which looked like a harmless
stand-in for "a clinical user at all" — until the field worker, who records values without
holding the read permission, met a 403 in front of the only request they could have answered. The
guard is now every write permission plus the supervisor's, and the decision that matters is made
in the service, against the name on the request.

**Flagging** is the other half and needs `observation.correct.request`. Saying "that number looks
wrong" is a clinical judgement; it must not require the authority to change anybody's work. CP15
gave that permission to the stations that record values and to the physician; CP62 also gave it
to QA, and took it away from registration, which held it without holding the read permission that
would let them see a value in the first place.

**Nobody flags their own value.** Correcting your own number needs no request, and a flag on it
would put a correction on your own quality record that nobody asked you to make.

**Nobody flags a computed value.** A BMI is not typed by anybody; it is what a height and a weight
make. Routing a request at it would send the correction to whoever pressed "derive" — who has
nothing to retype — while the wrong number it came from sits uncorrected. The refusal says so, in
both languages, and points at the measurement to flag instead.

## The supervisor's valve, and why it is a different event

A patient in front of a physician cannot wait for an operator who has gone home. So somebody
holding `observation.correct.approve` may answer a request addressed to somebody else.

It writes `SUPERVISOR_OVERRIDE_APPLIED` rather than `CORRECTION_APPLIED`, and the request lands in
status `OVERRIDDEN` rather than `APPLIED`. The difference is not bookkeeping. CP63 reads these
rows as a training signal, and an operator's record must not show a supervisor's fix as though
they had put it right themselves — that would flatter the operator who never answers anything and
punish nobody.

The request keeps naming the operator it was routed to, whoever ends up resolving it.

## Why a reason is a code _and_ free text

Free text alone cannot be counted. A code alone cannot say what actually happened — "the tape was
against the wall, not the patient" is the sentence that stops the same error next week, and no
taxonomy contains it.

So both are required, and the taxonomy lives in rows (`core.correction_reason`) so that changing
it is a decision rather than a release:

| code                 | means                                       | transcription |
| -------------------- | ------------------------------------------- | ------------- |
| `TRANSCRIPTION`      | typed a different number from the one read  | yes           |
| `WRONG_UNIT`         | entered in the wrong unit                   | yes           |
| `MISREAD_INSTRUMENT` | misread the instrument                      | no            |
| `WRONG_PATIENT`      | recorded against the wrong patient          | no            |
| `REMEASURED`         | measured again; the first reading was wrong | no            |
| `OTHER`              | something else, described in the note       | no            |

The `transcription` flag is what makes CP63's "three transcription errors in thirty days" a query
rather than a guess at what somebody meant by their free text.

**A rejection must say why.** "No" with no reason is how a flagging culture dies: the physician
who is refused twice without explanation stops flagging, and the wrong numbers stay.

> The taxonomy is a proposal. The plan lists it as needing clinical and operational confirmation,
> and it is deliberately rows rather than an enum so that confirming it is a seed change.

## The cascade

Criterion 3: everything derived from a corrected value recomputes.

This looked like a one-line SQL dependency lookup and was not. A derived value stores the inputs
it _actually saw_, keyed by the names the formula uses — `height_cm`, `weight_kg` — not by
observation code, so `inputs ? 'BODY_HEIGHT'` matched nothing and the first implementation
recomputed silently nothing at all. The dependency now comes from the derivation registry in Go:
each `Derivable` declares what it reads, `DerivationsReading(code)` inverts that, and a test
(`TestEveryDerivationSaysWhatItReads`) keeps the declaration honest against the formulas.

The recomputation happens **in the same transaction** as the replacement. A correction that
committed the new height and then failed to move the BMI would leave a record that is internally
inconsistent and looks fine.

What was recomputed is reported on the request, as codes, rather than left to be inferred — and an
entry ending `:not-recomputed` says plainly that one did not. A physician reading "height
corrected" should not have to compare two BMIs to work out whether the second one followed.

## What the operator sees

Criterion 4: the author is notified on their device. `correction.requested` is published on the
author's own user topic — with **no value in the payload**, like every other realtime message here:
the message carries a notification, and the screen re-reads through the API, which is what puts
the read back through the same authorisation and redaction as every other read.

`GET /v1/corrections/mine` is that queue, open requests by default. It reads the caller's own id
from the session rather than taking one from the query string, so there is no version of it that
returns somebody else's work.

## The invariants

Two, numbers 76 and 77 of the 77 the migrator verifies:

- **every correction names who asked, who was asked and why** — a resolution with no resolver, or a
  rejection with nothing said, is refused by the database and not only by the handler;
- **a corrected value is still in the record, next to its correction** — a replacement whose original
  has vanished is the one failure that would make the whole feature a lie.

## Deliberately not built

- **No bulk correction.** Ten wrong values are ten conversations, and a screen that fixes them in
  one gesture is a screen that fixes nine correct ones by accident.
- **No withdrawal of a flag.** A physician who flagged the wrong value rejects the request they
  raised, with a reason. The flag stays in the record, because it happened.
- **No correction of a correction's reason.** The reason is what somebody believed at the time.

## Open questions for the clinic

- The reason taxonomy above, confirmed or replaced.
- Whether QA should hold `observation.correct.approve` as well as `observation.correct.request` —
  currently they may flag, and only the physician and junior doctor may override.
- The Bangla wording throughout, which is drafted and not yet reviewed by a clinician.
