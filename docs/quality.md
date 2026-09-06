# The operator quality record

CP63. Blueprint §4.3. The checkpoint that decides whether CP62 was worth building:

> Recurring patterns per operator surface so targeted retraining happens and the same mistake does
> not repeat.

Without this, the corrections are a pile of rows nobody reads.

---

## The risk is the specification

The plan states it as a risk, in its own words:

> A metric that feels punitive damages data honesty — staff hide errors instead of correcting them.

Read as a caveat, that produces a paragraph in a README and a feature that quietly ruins the data
it was built to improve. Read as a design constraint, it decides almost everything below. Five
rules follow from it, and each is a mechanism rather than an intention:

**1. A count is never returned without its denominator.** Three corrections against four hundred
entries and against forty are different facts, and only one of them is a problem. Every breakdown
carries its own: by measurement, against how many of that measurement the operator took; by hour,
against how many values they entered in that hour. The hour one is not a nicety — an operator who
works only the late shift will always cluster late, and a screen showing the numerator alone
presents a rota as a person.

**2. A rate below twenty entries is null, and the floor is on the payload.** A rate computed from
four entries is noise, and rendering noise as a number invites somebody to act on it. The floor is
reported so a screen can say "eight more values and you will see a rate" rather than only "too
few"; being specific is most of what makes this read as arithmetic rather than as judgement.

**3. Rejected requests are reported separately and never counted as errors.** An operator who
defends a correct reading is doing the job. A metric that punished it would teach everybody to
accept every flag without looking, which is the opposite of the point.

**4. `upheld` and `overridden` are counted apart.** CP62 made a supervisor's fix a different event
precisely so an operator's record would not read it as their own. Folding them together here would
throw that distinction away at the one place it was created for, and tell somebody "you put three
values right" about values they never touched.

**5. An operator reads their own record with a session and no permission at all.** Somebody who has
to be granted something before they may see their own correction count will assume the count is
being kept from them. `GET /v1/quality/me` reads the caller's own id from the session, so there is
no version of it that returns anybody else's work, and there is no patient in the response.

## This is not HR's

`hr.performance.read` already existed — _"Read staff quality and throughput records"_ — and HR held
it. Reusing it would have taken ten minutes and no migration.

The plan puts _"performance-linked pay or discipline"_ explicitly out of scope. A permission that
hands an operator's error history to the department that sets pay puts it back in, whatever anybody
intends by it; the mechanism decides what happens under pressure, not the intention behind it.

There is a plainer argument too. Reading a correction record usefully means knowing that a weight is
measured on a scale somebody else calibrates, that the same code going wrong three times is usually
the instrument, and that corrections after four in the afternoon are a rota problem. That is a
clinical supervisor's knowledge. The same numbers in front of somebody without it produce confident
conclusions about the wrong thing.

So: `quality.read.team` and `quality.flag.resolve`, granted to the physician, QA and the
administrator. HR keeps `hr.performance.read` for the throughput reporting CP140 will build.
ADR-0029 has the full argument.

## The three patterns

§4.3 names them and all three are implemented. Each threshold is a **row**, unapproved, because the
plan lists the numbers as an open decision requiring approval — and because a threshold in a
constant is a threshold that needs a release to argue with.

| code                    | what it looks for                                          | proposed |
| ----------------------- | ---------------------------------------------------------- | -------- |
| `TRANSCRIPTION_3_IN_30` | three corrections whose reason means a number was mistyped | 30 days  |
| `SAME_CODE_3_IN_30`     | three corrections on one and the same measurement          | 30 days  |
| `END_OF_SHIFT_3_IN_14`  | three on values recorded after 16:00 in Faridpur           | 14 days  |

Every one of them also requires **twenty entries in the window**. Three corrections out of five
entries is a new operator on their first morning; a system that flags them for retraining on their
first morning teaches a clinic's staff to stop asking for help.

The transcription pattern is a query rather than a guess at somebody's free text because
`core.correction_reason.is_transcription` is a column — which is why CP62 put it there.

**One set of corrections raises one flag.** Three mistyped heights are both "repeated transcription
errors" and "the same measurement going wrong", and both are true; raising both hands a supervisor
two flags about one conversation, and a supervisor asked to have three conversations about three
corrections will have none of them. Thresholds are consulted in their own `ordering` and a
correction is evidence for one flag at a time — including flags already open from earlier passes,
which is the case the first implementation got wrong. A partial overlap still raises: three
corrections sharing one with an already-flagged pattern are two patterns with a coincidence in them.

## When it runs

After a correction is applied or rejected, on the operator whose record it lands on, in the request
that answered it — **after the commit and out of its way**. A correction that failed because a
counting query was slow would mean an operator cannot fix a wrong height, which is the wrong trade
in every direction. A missed review is a flag raised on the next correction instead.

Not on a schedule, and there is no aggregation table. This clinic records on the order of two
thousand values a day; a thirty-day window is sixty thousand rows and counting them grouped by
author against an index is milliseconds. A nightly job would buy nothing and cost the one failure
this feature cannot survive — a job that stops quietly, leaving a supervisor reading numbers that
were true last Tuesday and an operator told about a pattern they fixed a week ago.

## What is stored, and where

The counts are computed on read. The **flags** are stored, because a flag is a thing that happened:
raised on a window that has since slid past, acknowledged by a named person, still legible a year
later. The counts behind it are frozen into the row when it is raised, so the evidence does not move
when the window does.

`core.quality_flag`, not `read.`, and an audit entry rather than a clinical event. ADR-0003
event-sources clinical data, and this is not clinical data — it is an administrative fact about a
member of staff, the same shape as `core.break_glass_access` and `core.admin_alert`: never deleted,
acknowledged in place, linked to the security audit trail by `audit_seq`.

Two invariants, numbers 78 and 79:

- **every retraining flag can show what it was raised on** — a count below its own threshold, or
  evidence that is empty or longer than the count, is refused;
- **a staff quality record names no patient and carries no clinical value** — any key in the
  evidence that looks like a patient reference or a value fails the check. "We would never put a
  patient id in there" is not a mechanism; a supervisor reading a patient's numbers through their
  staff's error history would be reading clinical data through a side door, and the door is nailed
  shut rather than merely unmarked.

## The supervisor's list is a roster

By default `GET /v1/quality/operators` lists everybody who **recorded anything** in the window, not
everybody who was corrected. That is not a detail. A list every row of which has at least one
correction is structurally an accusation, whatever it is titled and however carefully each line is
annotated; the rows that make it a roster are the operators with four hundred entries and nothing
corrected. `only_corrected` narrows it for somebody who wants that.

Rows are ordered by employee code, not by count. Position in a list is itself a claim, and
denominators printed beside a number do not undo it.

## What the operator is told

A raised flag is published on the operator's own user topic, and so is its answer. The second half
is not symmetry for its own sake: a note that appeared on somebody's device and then silently
vanished when a supervisor closed it would teach them that things are decided about them out of
sight — the same ambush the design exists to avoid, arriving from the other end. Answered flags stay
on the record for thirty days for the same reason.

The message carries the flag's id, the threshold code and the status. No counts, no evidence: the
screen re-reads through the API, which puts the read back through the permission that decides
whether this reader may see a record at all.

## Deliberately not built

- **No retraining workflow.** A flag says "these three corrections have the same shape, and here is
  the first question to ask". What happens next is a conversation between two people, and a system
  that tracked its outcome would be building the performance-management tool the plan puts out of
  scope.
- **No comparison between operators.** No percentile, no ranking, no clinic average on anybody's own
  screen. Every one of those turns a record into a league table, and the plan's risk line is about
  exactly that.
- **No withdrawal of a flag.** A supervisor dismisses it, with a reason. The flag stays, because it
  happened.

## Open questions for the clinic

- The three thresholds. Every number is a proposal and the API says so; approving them is a row.
- Whether QA should hold `quality.flag.resolve` as well as `quality.read.team`.
- Who, in practice, reads the supervisor's list, and how often. A metric nobody looks at is the
  failure mode this checkpoint shares with CP57's override rate.
- The Bangla wording throughout, which is drafted and not reviewed by a clinician.
