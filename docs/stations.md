# The measuring stations

CP45, CP46 and CP49. Blueprint §3 steps 2 and 5, §15.2.

Anthropometry and vitals: the first two stations that are a whole vertical slice — a phone, a
form, an event, a projection, a socket, and a physician's dashboard that updates without
anybody refreshing.

---

## What the operator is doing while they use this

Not looking at the screen. §15.2's heads-up requirement is the single constraint that shaped
every decision below: their eyes are on the tape measure, the cuff, the oximeter and the
patient, and the phone is something they glance at.

So the number in each field is set at display size — large enough to confirm from arm's
length. The form is one column, because horizontal scanning needs attention. Every control is
at least a thumb wide. Nothing needs two hands.

### The fields are in the order the hands move

Anthropometry: height, weight, waist, hip, body fat, muscle mass. Vitals: blood pressure,
pulse, oxygen saturation, temperature, respiratory rate — the automated cuff reports the first
two together, the oximeter clips on next, and the thermometer and the count come after.

Neither order is alphabetical and neither is grouped by instrument. An operator working down a
screen in a different order from their hands skips one field every morning.

**The vitals order is proposed.** The plan's own risk note says to sit with the clinical
assistant and watch a morning before fixing it, and that has not happened.

### The unit selector is a row of buttons

Not a dropdown. A dropdown is two taps and a modal, and it hides the current unit behind a
chevron. A weight recorded in the wrong unit is wrong by a factor of 2.2, which is exactly the
error that reaches a dose.

Where a code has only one unit — per cent for body fat, /min for a pulse — no selector is
drawn at all. A control with one option teaches people to tap without reading.

---

## The panel is above the form, not below it

P-4 asks for derived values "as the operator types", and where they sit decides whether that
is useful. Below the form they are a result — something you scroll to after finishing. Above
it they are a **readout**: type a weight, glance up, watch the BMI move, and know you typed
what you meant.

That glance is the quality control.

The panel is arithmetic on what is on screen, for the operator's eyes only. It recomputes on
every keystroke and takes microseconds; criterion 1 allows 200 ms.

### Nothing on the phone computes a stored value

Saving posts the numbers **as typed** with the units **as selected**. The server converts, and
the server computes the derived values from what it has just written. The two agree because
they are the same equations held together by shared fixtures (ADR-0025) — not because the
phone sent its answer.

`packages/clinical-calc`'s `anthroPanel` and Go's `calc.AnthroPanel` are the _composition_
held to the same standard as the equations underneath it: one fixture, both suites, fourteen
cases including every half-filled state a form spends its first fifteen seconds in.

---

## One request, one transaction

`POST /v1/observations/batch`. An anthropometry entry is six measurements and four derived
values; ten round trips over clinic wifi is ten chances to lose one, and the entry is meant to
take under thirty seconds.

It is **one transaction, not one event**. Six measurements are six facts and the ledger records
six of them, each with its own id, its own idempotency and its own attribution. What the
transaction buys is that the record never holds three of six.

The derivations run last and **inside** it, so a BMI is computed from the height written a few
statements ago rather than from the previous visit's. That was a real bug, caught by a test:
the read the derivation used ordered ties by code rather than by ledger sequence, and two
measurements sharing an effective time produced an arbitrary "current" value.

A derivation whose inputs are not on record is skipped, not fatal. A waist with no hip is an
ordinary half-finished entry, and refusing the whole write to protect a ratio nobody promised
would throw away five good measurements.

---

## Three different things a number can be outside

This is the distinction clinical software routinely collapses, after which staff ignore all
three.

|                     | Question                             | Where                                             |
| ------------------- | ------------------------------------ | ------------------------------------------------- |
| **Plausibility**    | Is this a typing error?              | `core.observation_code`, `core.plausibility_rule` |
| **Reference range** | Is this ordinary for _this_ patient? | `core.reference_range`                            |
| **Critical**        | Does somebody have to act now?       | CP50                                              |

A systolic of 210 is entirely plausible, well outside the normal range, and a critical value.
Three different answers, three different responses, and a system that gave one would be wrong
twice.

### Plausibility: impossible, or merely implausible

A height of 15 cm is **impossible** — no client stores it, and no confirmation passes it. A
height of 205 cm is **implausible but possible**: rare, real, and a typing error far more often
than not, so it is accepted with an explicit confirmation that is recorded.

Refusing the second is the classic failure. A system that cannot record the tallest patient in
Faridpur is a system staff learn to work around, and that costs more than the typing errors
did.

Every confirmation is an event **and** a column on the observation, so "which rules are staff
overriding every day" is one query. A rule overridden twenty times a week is a rule that is
wrong, and the clinic should find that out from its own record rather than from opinion.

### The delta is the check a band cannot make

A weight of 58 kg is ordinary. 58 kg in somebody who was 72 kg in March is either a serious
clinical event or the wrong patient. An adult who has gained 12 cm of height since their last
visit has been measured badly once, and it is the commonest anthropometry error there is.

A **correction** is exempt: comparing against the value being replaced would refuse exactly the
write that fixes a mistyped number.

### The message states the range

An operator told their entry is "out of range" and not told _what_ range re-types the same
number. The refusal names the limit, in both languages, and adds the rule's own explanation
where it has one.

---

## Where the rules live, and why the client cannot disagree

Both rule sets are **data** — editable without a release, which is criterion 4 — and both are
served to the station app so the warning appears as the number is typed, on a tablet that may
have no signal.

The safety property is subtle and worth stating. `/v1/observations/plausibility` and
`/v1/observations/reference-ranges` return their rows **already ordered most specific first**,
in exactly the order the database resolves them. The client's rule is therefore "the first one
in this list whose predicate matches", and it never ranks anything.

A client that reimplemented the specificity ranking would one day show an operator one band
and be refused by another. Two Go tests sweep every code, both sexes and a range of ages
comparing the listed order against the server's own resolution.

---

## A blood pressure is not two numbers

It is two numbers, an arm, a position and a cuff. A series that silently mixes a sitting
left-arm reading with a supine right-arm one is a series nobody can read a trend from — so the
context is recorded as coded observations sharing the reading's effective time, rather than as
a note somebody sometimes types.

**Two readings are normal practice.** Both are kept as distinct observations, not averaged and
not replaced: the second is not a correction of the first, and a record that treated it as one
would lose the fact that they differed, which is often the finding. Each reading gets its own
effective time, which is what lets the timeline order them.

---

## Manual verification still owed

- Record a full anthropometry set on a real phone and time it. Thirty seconds is the target
  and it is _proposed_.
- Record a full vitals set while looking mostly at the patient. If that is not possible, the
  layout is wrong, not the operator.
- **Sit with the clinical assistant and watch a morning** before fixing the vitals field order.
- Confirm the plausibility bands against a real week. Every one of them is a proposal.
- Confirm the reference ranges, per age band, with a clinician. Every one of them is a
  proposal too, and the paediatric blood pressure bands are explicitly coarse until CP50
  brings the height-indexed table.
