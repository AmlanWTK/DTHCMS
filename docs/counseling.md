# Counselling checklists

CP55. Blueprint §5.1 and §5.2, [R-07]. Decision pending: **D-53** (the content itself).

> Physician creates a template, publishes it, and it appears on a counsellor's phone with no
> developer involved.

A checklist is content, and content that needs a deployment to change is content that stops
being true. Everything below follows from one sentence in the criteria — a published version is
frozen — and from the fact that a completed counselling session keeps the version it used.

---

## Templates, versions, items

```
counseling_template          the checklist as a thing with a name        (DIABETES)
  counseling_template_version  one revision, DRAFT | PUBLISHED | RETIRED   (version 3)
    counseling_item              one line a counsellor ticks                (GLUCOMETER)
counseling_assignment        which diagnosis calls for which checklist   (ICD10 E11 → DIABETES)
counseling_room              the three rooms §5.2 walks through
```

Status moves **DRAFT → PUBLISHED → RETIRED and nowhere else**, enforced by
`counseling_version_status_moves_forward`. There is no un-publishing: sessions reference the
version they used, so un-publishing would leave them pointing at something the floor can no
longer see. Retiring is the honest alternative, because a retired version still reads.

A partial unique index, `counseling_one_published_version`, allows exactly one live version per
template. Publishing therefore retires the incumbent, and the two statements run inside one
transaction — not as a single data-modifying CTE, which sees one snapshot and races its own
index.

---

## Why a published version is frozen in the database

`counseling_published_versions_are_frozen` is a trigger on `counseling_item` for INSERT, UPDATE
and DELETE. Editing a published version is not a validation failure; it is a rewrite of what
every counsellor was asked to cover last October, with nothing afterwards looking wrong. The
API answers `409`, and the trigger means the answer is the same for a support script.

The consequence on screen is that there is no read-only editor. `TemplateWorkspace` branches
once: a draft, to somebody who may write, gets `DraftEditor`; anything else gets `VersionView`,
which contains no inputs at all. A disabled form teaches an author that the block is temporary
and sets them hunting for the state in which it lifts. `counseling.test.tsx` has a named test
that fails if a textbox, a combobox or a checkbox ever appears inside a published version.

---

## Publishing is its own act

Three deliberate steps: press, confirm against a statement of what will happen, then a step-up
token minted for `counseling.publish` and nothing else.

- **Its own permission.** `counseling.template.publish`, granted to `PHYSICIAN`. Reading is
  granted to nearly every clinical role, because a counsellor about to work through a checklist
  and a nutritionist about to be handed the patient both need to see what it asks.
- **Its own step-up purpose.** Declared in `auth.PurposePublishCounseling`, listed in
  `knownPurposes`, and in the `StepUpRequest` enum in `api/openapi.yaml`. A token good for
  resetting a password must not be spendable on changing what every counsellor asks every
  patient tomorrow.
- **Its own component.** `DraftEditor` owns exactly one write and it is the save; `PublishPanel`
  owns the other. They are separated by construction rather than by layout, so a physician
  fixing a typo at the end of a clinic day cannot put a checklist on every phone by pressing the
  nearer button.
- **Unsaved work blocks the button; a missing translation does not.** Publishing with unsaved
  edits would succeed and ship something other than what is on screen, and no server can catch
  that. A missing translation would _fail_, and the server — not this client's possibly stale
  copy — is the authority on whether it fails. So the gap is named loudly above the button and
  the button still works.

---

## Bilingual before publish, not before save

Criterion 4. An item with English and no Bangla is what a half-written draft looks like, so
`saveCounselingDraft` accepts it. `counseling_publishes_only_in_both_languages` refuses it at the
publish transition, the API refuses it before the trigger has to, and `publishBlockers` names
which item and which language before the author spends the round trip.

The server's refusal names the field `items` — correct for an API, useless on a screen with nine
rows — so the client turns it into "item 4, Bangla missing". The refusal is the server's; the
attribution is ours; both are shown.

---

## What the seeded diabetes checklist is, and is not

Migration `00037` seeds the seven §5.1 items, all mandatory, and publishes them — because the
alternative is a clinic whose first counselling session cannot start until somebody remembers to
press a button.

That version carries `published_source = 'MIGRATION'` and **no `published_by`**. An invented
user id would be the only attribution in this system naming somebody who did not do the thing.
A blank alone would read as data missing, so the column says plainly which it is.

Its `approved_at` is null and stays null. **D-53 is open**: those seven items are transcribed
from the plan and their Bangla and guidance are an engineer's. The template list says so on the
row — the checklist is live _and_ unapproved, and both facts have to be readable at once.
`contentApproved` and `publishedBySystem` exist so no screen answers either question by testing
a timestamp for truthiness.

---

## Assignment rules

`counseling_assignment` matches on a **code prefix**, not a code: `E11` catches the whole family,
which is what a clinician means by "type 2 diabetes"; a rule per member is sixteen rows that
drift apart. Higher `priority` wins where two rules match, and a patient with two conditions
gets one checklist per matching rule.

Only published versions are ever returned. A draft is not something to hand somebody on the
floor.

---

## Open

| What                                                                    | Who       |
| ----------------------------------------------------------------------- | --------- |
| **D-53** — the counselling content itself, in both languages            | Dr. Nahid |
| The seven seeded items' Bangla and guidance, which are mine             | Dr. Nahid |
| Whether the three rooms' order matches the corridor after the next move | Amlan     |

---

# On the floor: sessions and ticks

CP56, §5.3 and §5.2, [R-01] [R-07]. The other half: a counsellor with a phone.

## The shape

```
counseling_session   one walk through one checklist, for one visit
  counseling_tick      one item covered, by one person, at one time
```

The session holds `template_version`, which is where CP55's criterion 2 actually lives: a
checklist republished while a counsellor is halfway down it does not change what this patient
was asked about. A trigger refuses a change to that column, because the one way the guarantee
dies quietly is a well-meant UPDATE that "fixes" a session onto the current version.

A session is opened once per checklist per visit — a unique index says so. A counsellor whose
phone lost the reply and pressed start again lands back in the session they already have; a
second half-ticked copy of the same list is how two people each cover half of it and each
believe the other did the rest.

## One tick, one event, one actor

**Criterion 1.** There is no endpoint that ticks a list, no `TickAll`, and no "complete the
rest" flag — and a Go test walks `SessionService`'s methods and fails if any of them ever
grows a slice parameter. The cost is one request per item; the reason is §5.4's method, which
is the physician asking the patient _what were you told about injection sites_ and then
looking at who told them. A single event carrying seven codes would answer "the session was
run by X", which is the wrong question.

Ticking an item that already has a live tick is refused with
`COUNSELING_ITEM_ALREADY_COVERED` rather than accepted. The alternative is silent
re-attribution: the second press overwrites who covered the item and when, and §5.4's question
comes back with the name of whoever pressed last. A mis-tap on a colleague's item is therefore
an un-tick with a reason, then a tick.

The exception is the **same** event id arriving twice. That is one act, not two — a phone that
lost the reply, or an offline queue replaying what it wrote in a room with no signal — and it
is answered with the session rather than a refusal.

## Un-ticking keeps the row

**Criterion 3.** A reason is required by the handler, by the event's own validation and by a
database constraint. An un-tick with no reason is indistinguishable from a mis-tap, and telling
those apart is the entire value of recording it.

The row stays. `undone_at`, `undone_by` and `undone_reason` fill in, and `undo_count` remembers
how often it has happened even after a re-tick — "ticked and un-ticked three times" is exactly
what a quality review is looking for. A delete would satisfy the words and destroy the point.

## Completing ticks nothing

A counsellor may finish with items outstanding: the patient left, the interpreter did not
arrive, the insulin corner was closed. The record then says exactly that. A completion that
also covered the outstanding items would be the batch attribution criterion 1 forbids wearing a
different name, and it would let a session be closed by somebody who counselled nobody.

Whether such a visit may reach the physician is CP57's gate to decide — and it can only decide
it because this write did not quietly prevent the situation being recorded.

## What is still missing, in one place

`core.counseling_outstanding(uuid)` is read by the phone, by the physician's panel and by
CP57's gate. A second implementation of "what is outstanding" is how a phone shows a green tick
while a gate refuses the patient standing in front of it.

It is reported on every session, index rows included, and never omitted: with it sometimes
absent, "everything is covered" and "this row was not asked" would be the same empty answer.

## Which checklist, and who may tick it

A counsellor's phone knows the patient in front of it and nothing else.
`GET /v1/counseling/visits/{visitId}/checklists` answers which checklists that visit calls for,
matched against the patient's **live coded comorbidities** — a presenting complaint is coded in
the clinic's own dictionary and matches no ICD rule, and a family history is somebody else's
condition, so a checklist assigned from a mother's diabetes would have a counsellor teaching the
wrong person's disease. Sessions already open are included even when no rule matches them any
more: a rule retired at lunchtime must not make a half-ticked session vanish from the phone of
the counsellor walking it.

The patient's conditions live in `history`, which `counseling` may not import. They arrive
through an interface implemented in `cmd/api`. When the physician's diagnosis arrives at a later
checkpoint it becomes another source of codings passed to the same interface, and nothing inside
this module changes.

`counseling.tick` reaches the counsellor and the nutritionist, because the nutrition room is one
of §5.2's three. It does not reach the exercise specialist or the prescription educator: no room
maps to their stations, and who staffs the insulin corner is an operational decision that will be
two rows — a `station_code` and a grant — when somebody makes it.

## The board, within two seconds

**Criterion 4.** Every act announces itself: started, ticked, un-ticked, completed. The message
carries two numbers — covered and mandatory — and no item text. "Covered 6 of 7" on a screen in
a waiting room is a progress bar; "insulin technique — not yet covered" is a clinical detail
about the person standing in front of it.

Two topics, two permissions: the queue topic under `board.read` for the wall display, and the
patient's topic under `counseling.session.read` for the physician's panel. A failed publish is
not a failed tick — the socket is a nicety, the pull is the truth — so it is logged and dropped
rather than failing a counsellor's write with the patient still in the chair.

## Open

| What                                                                            | Who               |
| ------------------------------------------------------------------------------- | ----------------- |
| **The sixty seconds.** A stopwatch on a real floor; the tests pin the act count | Dr. Nahid + Amlan |
| Who staffs the insulin corner, and therefore which station its room belongs to  | Dr. Nahid         |
| The tick criteria — what "done" means per item (D-53)                           | Dr. Nahid         |

---

# The gate, and the valve (CP57)

§5.5 and §5.4. Decision open: **who may override** (operational).

> No patient reaches Step 9 until mandatory items are ticked.

## Two gates in this system, deliberately different shapes

CP54's allergy gate has **no** override. It has three honest answers, each of which takes five
seconds, so an override would simply become the fast one.

This gate is not like that. What it asks for is _seven conversations_, and the situations that
make them impossible are ordinary: the patient's daughter arrives with the car, the interpreter
does not come, the insulin corner is closed, the consultant is leaving. The plan says it plainly
— **"a rigid gate with no escape valve will be worked around"** — and a gate people route around
is worse than one with a recorded valve, because the routing-around is invisible.

So there is a valve, and everything about it is built to be seen.

## Where the gate is

A trigger on `core.queue_entry`, in front of the station whose `sequence_hint` matches the
consultation's or later. The counselling rooms themselves are not gated — a checkpoint refusing
the patient at the door of the room where it would be satisfied is not a checkpoint.

The API refuses first, and that is not redundancy: criterion 2 asks that the operator be told
**exactly which items are missing**, and a database exception is not a screen. `visit` asks a
`Gate` interface before the write, the counselling bridge answers with the items in both
languages, and the refusal comes back as `VISIT_GATE_BLOCKED` with the sentence. The trigger
catches everything that did not come through there.

## What counts as missing

Two things, and the second is the one that matters:

1. a session that exists with mandatory items nobody ticked, and
2. **a checklist the patient's record calls for that nobody ever opened.**

Without (2) the way past this gate is to never start a checklist — which is not a loophole
somebody has to find, it is what happens on a busy morning when the counselling room is skipped.
So a checklist called for and never opened counts as all of its mandatory items outstanding, and
those items carry no `session_id`, because "go back and finish it" and "nobody has started this"
are two different rooms to send the patient to.

`core.counseling_gate_missing(visit)` is the one definition. The trigger reads it, the API reads
it, the phone reads it, the physician's panel reads it. It is ordered — an unordered `UNION ALL`
comes back however the planner felt, and the same unchanged visit rendering its missing list
differently twice makes a physician think something moved.

## The valve

`POST /v1/counseling/visits/{visitId}/gate/override`, and every part of it exists to be legible:

- **its own permission**, `counseling.gate.override`, held by nobody who merely works at a
  station — seeded to the physician and the administrator, and _who may override is an open
  operational decision_;
- **a required reason**, enforced in the handler, in the event's validation and by a CHECK;
- **the missing items as they stood**, kept on the row rather than recomputed, because items
  covered afterwards would make the record say the valve was used for nothing;
- **an audit entry**, beside the role grants and the break-glass records — the log somebody
  reads when asking "who decided this";
- **a rate view** under `qa.review`, because the plan's mitigation for clinic-floor friction is
  the valve _plus_ override-rate monitoring, and monitoring nobody can read is a plan on paper.

An override on a visit the gate is not holding is refused: a row recorded where nothing was
outstanding makes the rate view lie. A second override is refused too — it is the same act, and
answering plainly beats stacking rows nobody reads.

**`overridden` is not `covered`.** Both are "allowed" to the queue and they are not the same
clinical fact, so the gate reports them separately and every screen draws them differently. A
panel that showed them the same way would be telling a physician the counselling was done.

## What the events are for

`VISIT_GATE_BLOCKED` and `VISIT_GATE_SATISFIED` are generic — the gate is a _field_, not the
event type — because CP83's QA clearance is the same fact about a different checkpoint. Both are
written, and the reason is that only both together are readable: "held 14 times, passed 300" is
a working checkpoint; "held 14, passed 14" is one nobody can get through.

They are written only where a gate actually applied. An event for every queue entry would be a
log of the whole clinic rather than a record of the gates.

## The physician's panel

§5.4's method is spot-questioning — the physician asks the patient what they were told about
injection sites, and then looks at **who told them**. So the panel is per item, not per session:
who covered it, when, in which role, with their note, and how long elapsed since the previous
tick. Elapsed time is between ticks, not attention: a counsellor who ticks four items at the end
shows three fast items and one slow one, and nothing here should present that as a fact about
the teaching.

A withdrawn tick is visible history with its reason, never an absence. The person is named —
the role tells a counsellor from a nutritionist and does not tell two counsellors apart, which
is exactly the question being asked.

## Open

| What                                                                         | Who               |
| ---------------------------------------------------------------------------- | ----------------- |
| **Who may override**, beyond the physician and the administrator seeded here | Dr. Nahid         |
| The override rate this clinic considers normal, and who watches it           | Dr. Nahid + Amlan |
| Five of seven ticked, refused, remedied, released — on a real floor          | Dr. Nahid + Amlan |
