# The exercise assessment and plan

CP60. Station 8's mobility assessment, its contraindication filter, and the routine a patient is
handed on the way out.

---

## Excluded, not warned

Acceptance criterion 1 is four words — _"contraindicated exercises are excluded, not warned"_ — and
the checkpoint's manual verification says the same thing again in bold: a patient with severe
neuropathy must find the contraindicated options **absent**.

That decides where the filter lives, and it is the only decision in this checkpoint that matters.

A screen that received the whole library and hid part of it would be a warning wearing a different
colour. The data would be on the device, one bug or one "show all" affordance away from being
offered, and the operator's tap would be the only thing between a patient with an insensate foot
and a jumping routine. It would also pass a test that only read the list — which is why the test
for this reads the **bytes** of the response.

**So there is no endpoint that returns the library for a patient.** Not filtered-with-flags, not
paginated, not "for reference". `GET /v1/patients/{id}/exercise/options` computes the permitted set
on the server from the contraindications actually recorded, and the excluded rows never leave the
process.

The rule is enforced three times, and each catches something the others cannot:

1. **`core.exercises_permitted`**, a SQL function, computes the set. It is in the database rather
   than in Go for the reason every other gate here is: a rule that lives only in an application is
   a rule a second application does not have. The physician dashboard (CP73) and §12.1's research
   extract read the same function.
2. **`Service.Issue`** recomputes it and refuses any target outside it. A client that kept an old
   list, or never asked for one, or was written by somebody who did not read this file, is refused
   here — because criterion 1 must not depend on a screen behaving.
3. **`read.exercise_plan_item_is_permitted`**, a trigger, refuses it again on the way into the read
   model. A projection rebuild writes these rows too, and a rebuild that quietly accepted a
   contraindicated item would produce a read model in which criterion 1 is false.

Three checks of one rule reads like belt and braces. It is not: they guard a bad screen, a bad
client, and a bad replay, and only the second is what the plan's manual verification exercises.

## What is sent instead of the excluded rows

A count and the reasons. _"Three options are not shown because of severe neuropathy."_

That is transparency about the filter rather than an offer, and both halves earn their place. A
physician looking at eight options needs to know the library holds twelve, or a list that is short
on purpose is indistinguishable from a table that is missing rows. And a clinician who disagrees
with an exclusion needs a **sentence** to disagree with — which is why every row of
`core.exercise_contraindication` carries its reason, in both languages, rather than being a tag.

The reasons are named by **condition**, never by exercise. `excluded` is `library_size` minus what
was offered, deliberately not the sum of the per-condition counts: jogging is excluded by
neuropathy _and_ by an open ulcer, and each reason is true on its own, so the counts overlap.

## An assessment says what it asked, not only what it found

`contraindications` alone made "we asked all five and none apply" and "we asked two and skipped the
neuropathy question" **byte-identical rows**. The filter then computed the permitted list as though
the unasked question had been answered no — which is criterion 1's failure arriving through the
front door rather than through a screen.

The mobility fields already drew this distinction: `walks_unaided` is nullable because a patient
nobody asked is a different record from one who cannot. The five conditions the filter actually
turns on had no such state, so they have one now: `asked text[]` beside `contraindications text[]`.

Three consequences, and they hang together:

- **An assessment that leaves a live condition unanswered is refused**, and the refusal names which
  — a client working from a stale catalogue (an offline tablet, a rolling deploy) has to know which
  questions to fetch and put before it can record.
- **A condition that was never asked excludes.** Absence of evidence is not evidence of absence,
  and offering an exercise mapped to a question nobody put is the same "warned, not excluded"
  failure one step removed. The practical effect is that adding a condition to the catalogue
  narrows every existing patient's list until somebody asks them the new question — correct, and
  visible, because the options payload reports it as `NOT_ASKED` rather than as a finding. Saying
  the patient _has_ the condition would be a claim nobody made.
- **Retiring a condition stops it excluding.** Without that, a retired condition would exclude
  forever: it is absent from every new assessment's `asked` set and so reads as "never asked".

Invariant 89 keeps the two arrays honest: no assessment records a condition as applying that it did
not ask about. Unlike "every condition is answered", that one stays true forever — a catalogue
addition does not falsify it — which is why it is an invariant and not only a write-time check.

## No assessment means no list

`GET .../exercise/options` answers **409 `EXERCISE_NO_ASSESSMENT`** when nobody has answered the
questions yet.

The tempting alternative is to return the whole library, on the reasoning that nothing is known to
be contraindicated. That reasoning is wrong in the one way that matters here: _"no contraindications
recorded"_ and _"no contraindications"_ are different facts, and treating the first as the second
is exactly how a neuropathic patient is offered jogging by a station that skipped a screen.

So the questions come first. A plan needs an assessment anyway — `read.exercise_plan.assessment_id`
is `NOT NULL` — and refusing early means the operator meets the refusal before they have chosen
anything rather than after.

## The plan freezes the assessment

`read.exercise_plan.assessment_id` never moves.

A patient whose foot ulcer heals gets a **new** assessment, not an edited one, and the old plan
still points at the findings it was filtered against. Without that, _"why was she given stair
climbing in June"_ has no answer once her cardiac symptom is recorded in July: the plan would look
like a mistake rather than a decision that was right on the evidence of the day.

The same reasoning produces the staleness check. `POST /v1/exercise/plans` requires
`assessment_id` and compares it against the current one, answering **409
`EXERCISE_ASSESSMENT_SUPERSEDED`** when they differ. A plan chosen before a colleague recorded a
foot ulcer would otherwise be issued against the pre-ulcer list, and every filter check above would
pass — because they would all be checking the wrong assessment.

## Targets are two numbers

`times_per_week` and `minutes_per_session`, and nothing else counts as a target.

This is §12.1's requirement rather than a form's preference. The exercise–outcome correlation
(CP128) computes adherence from structured data, and "walk more" is unanalysable — a year of
free-text routines would be discovered to be useless at the moment somebody first tried to analyse
them. Invariant 87 refuses a target that cannot be counted.

`minutes_per_week` is derived once on the server, per item and per plan, so a screen, a printed
sheet and the research extract cannot each round it differently.

One exercise targeted twice is **refused**, not resolved by arrival order: two entries carry two
different weekly totals, and silently keeping the first puts a number in §12.1's adherence data
that nobody chose.

## The refusal is a sentence, not a status

`EXERCISE_CONTRAINDICATED` names the exercise, names the condition, and carries the reason from
`core.exercise_contraindication` — in both languages, with the codes in `fields` so a screen can
highlight the offending row. An operator told only "not allowed" tries the next high-impact option;
one told that a foot without protective sensation cannot feel an injury from repeated impact does
not.

That the reason is a **row** rather than a constant is the same decision that makes the mapping
arguable: this sentence is what a clinician who disagrees with an exclusion argues with, and a
constant would have made the mapping unarguable at the one place somebody actually meets it.

A `NOT_ASKED` exclusion gets a different sentence — _"jogging cannot be offered until the question
about severe peripheral neuropathy has been asked"_ — because the operator's next act is to ask the
patient something rather than to talk to a physician.

## Bangla is a precondition, not a translation pass

Criterion 3 asks that the sheet print legibly in Bangla. What that requires of the data is that
every exercise carries `how_bn` — the instruction the patient is actually handed — and invariant 88
refuses a library row, or an exclusion reason, that reads in only one language.

The instruction is joined onto a plan item from the library rather than copied onto the row, so a
correction to the Bangla reaches a sheet reprinted tomorrow.

The contraindication catalogue carries **questions**, not labels, for the same reason a form
matters more than a field: a checkbox saying "neuropathy" gets ticked for tingling toes; one asking
whether protective sensation is lost at a monofilament site does not. Every exclusion downstream is
only as good as the answer to that question.

## The library is rows

Criterion 4 asks that the library be editable without a code release, and the plan lists both the
content and the contraindication mapping as **open clinical decisions**. Both are therefore tables,
seeded with a working set that says plainly it is unapproved: twelve exercises and twenty-two
mappings, `approved_at` null on every exercise and the API reporting `approved: false`.

The mapping is a join table rather than a tag list on the exercise, because the interesting question
is per pair — _why_ is this excluded for this condition — and a reason a clinician can read is what
makes the mapping arguable rather than magic.

A row inserted into `core.exercise` is offered on the next request; a row inserted into
`core.exercise_contraindication` excludes on the next request; `retired_at` removes one. All three
are tested, because "editable without a code release" is a claim that decays quietly.

## What is recorded

| Event                          | When                             | Carries                                                   |
| ------------------------------ | -------------------------------- | --------------------------------------------------------- |
| `EXERCISE_ASSESSMENT_RECORDED` | Station 8 finishes the questions | Mobility, baseline fitness, and the conditions that apply |
| `EXERCISE_PLAN_ISSUED`         | The routine is given             | The frozen `assessment_id` and the targets                |

Two events rather than one, because the findings and the plan are separate acts by possibly
separate people — and a plan that had welded them together could not be re-issued at a follow-up
without re-asking the questions.

`contraindications` is on the event and is **never omitted**, even when empty. The permitted list is
computed from it, so an event that said "not asked" where it meant "none apply" would be a lie in
the ledger, and the ledger is the copy that survives.

## Invariants

| #   | What it refuses                                                       |
| --- | --------------------------------------------------------------------- |
| 86  | A plan item contraindicated for the patient it was issued to          |
| 87  | A target that is not a number of times a week and a number of minutes |
| 88  | An exercise, or an exclusion reason, that reads in only one language  |
| 89  | An assessment recording a condition it did not ask about              |

86 is the standing form of criterion 1. The trigger guards rows written from here on; the invariant
notices one that arrived some other way — a restored dump, a migration, a trigger somebody
disabled.

## Open clinical decisions

- **The exercise library.** Twelve rows, unapproved. What is missing is measured by how often an
  operator cannot find what a patient can actually do.
- **The contraindication mapping.** Twenty-two pairs, each with its reason. The reasons are the
  part to argue with.
- **The five conditions**, and their questions. §3 step 8 names three; an open foot ulcer and an
  uncontrolled pressure are here because they are the two an operator at this station meets first,
  and a list that omitted them would be one an examiner quietly works around.
- **The Bangla wording** throughout — the instructions especially, since those are what a patient
  reads at home with nobody to ask.
