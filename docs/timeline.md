# The patient timeline

_CP37 · blueprint §8 · `read.patient_timeline`_

One chronological read of everything known about a patient. The physician dashboard (CP73),
the timeline visualisation (CP74), the AI synthesis (CP71) and the records chronology (CP107)
all read this table rather than each writing its own query over the ledger.

That is the whole reason it exists as its own checkpoint. Four queries is four places for a
fact to be missing from one of them, and the one it is missing from is always the one somebody
is looking at.

---

## 1. One row shape, extensible by design

| Column                                                                         | What it holds                                           |
| ------------------------------------------------------------------------------ | ------------------------------------------------------- |
| `occurred_at`                                                                  | When it happened in the world                           |
| `recorded_at`                                                                  | When it reached the system                              |
| `category`                                                                     | The family a filter offers — **closed list**            |
| `kind`                                                                         | What the row actually is — **open**                     |
| `label_en` / `label_bn`                                                        | What a person reads                                     |
| `value`, `unit`, `value_num`                                                   | The value as shown, and numerically when it is a number |
| `actor_id`, `actor_code`, `actor_role`, `actor_station`, `device_id`, `source` | Attribution, on every row                               |
| `flags`                                                                        | `critical`, `corrected`, `amended`, `high`, `low`       |
| `event_id`, `event_type`, `global_seq`, `item`                                 | What produced it, and where in the ledger to look       |
| `needs_permission`                                                             | Which permission a reader needs to see this row         |

The plan's risk note asks for this directly: _design the row schema for extensibility now_. A
new kind is a row, not a column. The alternative is a table that gains three columns per
checkpoint and a query that has to know all of them.

`category` is closed and `kind` is open, deliberately. A new observation type must not need a
migration; a new **category** should be a decision, because it changes what a filter offers.

`occurred_at` and `recorded_at` are both kept. A vital taken at 09:10 and entered at 11:40
belongs at 09:10, and the difference is itself worth seeing.

## 2. Attribution on every row, never joined

§8's hover-to-see-who has to work everywhere. A timeline row whose author is resolved by a
join is a row that loses its author when the join is expensive, when the query is written in a
hurry, or when the person who recorded it has been deactivated.

So who did it, in which role, at which station, on which device, is denormalised onto the row.
`core.assert_timeline_rows_are_attributed()` refuses a row with no actor or no label, so a
future row type that forgets is caught by the migration suite rather than by somebody hovering.

The **employee code is resolved in the derivation**, not carried in the event. The ledger holds
the user id — that is the durable fact — and the code is a rendering of it, so a rebuild looks
it up rather than replaying a string that may since have changed.

## 3. Exactly once, and rebuildable

`UNIQUE (event_id, item)` is acceptance criteria 1 and 4 held by an index rather than by care.

- One event can produce **several rows** — a correction of three fields is three lines, because
  "the date of birth was corrected" is what somebody scrolls a timeline looking for and "the
  record was corrected" is not. `item` distinguishes them.
- A **re-delivered event** — a tablet that sent, lost the reply and sent again — updates the
  same rows rather than adding new ones.
- A **rebuild** replays the ledger through the same derivation and produces the same table,
  proved field by field by `TestARebuildReproducesTheTimelineIdentically` rather than by
  counting rows.

## 4. Permission filtering, in SQL

`needs_permission` is on the row and the filter is a `WHERE` clause.

A post-filter in Go looks equivalent and is not. It is how a total comes back larger than the
rows returned, and how a paging cursor skips what it hid — the second is worse, because the
user sees a short page and has no way to know why.

The permissions come from the **verified caller**, never from a parameter. A client that could
name the permissions to filter by is a client that could name all of them.

## 5. The API

`GET /v1/patients/{id}/timeline?from=&to=&types=&limit=&offset=`

- `from` and `to` take a date (read in the clinic's calendar, not UTC) or a full timestamp. A
  date-only `to` means **the whole of that day**: somebody asking for 1 to 31 January means
  January, and an exclusive bound at midnight silently drops the last day.
- `types` is a comma-separated list of categories. An unknown one is **refused**, not ignored —
  silently returning everything is how a "medication only" screen shows a diagnosis to somebody
  who filtered it out.
- The response carries `earliest` and `latest` for the whole record whatever window was asked
  for, so "nothing in this window" is distinguishable from "nothing at all".

A timeline read is audited as `patient.viewed` with `by: timeline`. It is the whole record in
one response, which is exactly what a bulk read looks like from the outside.

## 6. What is on it today

Version 1 (CP37) derived five administrative kinds. **Version 2** adds the clinical ones the
checkpoints since CP37 created — which is what CP74 has to draw. On the loaded synthetic cohort
that took the table from 62 rows to 6,334.

| Event                                | Category         | `needs_permission`          | Rows                          |
| ------------------------------------ | ---------------- | --------------------------- | ----------------------------- |
| `PATIENT_REGISTERED`                 | `registration`   | `patient.read.demographics` | one                           |
| `PATIENT_DEMOGRAPHICS_CORRECTED`     | `administrative` | `patient.read.demographics` | one per changed field         |
| `PATIENT_MERGED`                     | `administrative` | `patient.read.demographics` | one                           |
| `PATIENT_PHOTO_CAPTURED`             | `document`       | `patient.read.demographics` | one, carrying no key or URL   |
| `CONSENT_GRANTED` / `_REVOKED`       | `consent`        | `patient.read.demographics` | one per consent type          |
| `VISIT_OPENED`                       | `visit`          | `visit.read`                | one                           |
| `VISIT_CLOSED`                       | `visit`          | `visit.read`                | one, plus `visit.review_due`  |
| `ENCOUNTER_STARTED` / `_FINISHED`    | `visit`          | `visit.read`                | one, named for the station    |
| `OBSERVATION_RECORDED`               | `observation`    | `observation.read.values`   | one, `kind` = the code        |
| `ALLERGY_STATUS_ASSERTED`            | `observation`    | `patient.read.allergies`    | one                           |
| `CRITICAL_VALUE_ALERTED`             | `alert`          | `alert.read`                | one, flagged `critical` + end |
| `CRITICAL_VALUE_DELIVERY_ATTEMPTED`  | `communication`  | `alert.read`                | one, `value_num` = recipients |
| `DIET_ENTRY_RECORDED`                | `observation`    | `observation.read.values`   | one, on the day recalled      |
| `EXERCISE_ASSESSMENT_RECORDED`       | `observation`    | `observation.read.values`   | one                           |
| `AI_SYNTHESIS_COMPLETED` / `_FAILED` | `document`       | `ai.synthesis.read`         | one                           |

The photograph row deliberately carries **no object key and no URL**. A timeline row is read by
everyone who may read the record; the image is fetched from its own endpoint, which mints a
signed URL per request and audits the read.

### Three rules the v2 rows keep

**The permission is the one a reader genuinely needs, not the default.** An observation value
sits behind `observation.read.values`, which §4.4 blinds registration and the pharmacist to. A
timeline row is the one place a value could reach them without anybody writing a query for it,
and `TestAClerkWithOnlyDemographicsCannotReadAValue` is written so that deleting the `WHERE`
clause makes it fail.

**Labels come from the registry that owns them, in both languages.** Observation names come
from `core.observation_code`, station names from `core.station`, foods and measures and meals
from theirs. The derivation ships a label with holes in it — `{1} started` — and
`read.apply_timeline` fills them, exactly as it already resolves `actor_code`. A second copy of
the bilingual vocabulary in Go would be a Bangla label that is right in the picker and stale
here, which nobody notices because the two screens are never open together. The invariant now
refuses a blank `label_bn` as well as a blank `label_en`.

**`value`, `unit` and `value_num` describe one quantity between them.** `value_num` is the
canonical number, converted by `core.to_canonical` — the same function the write path uses, so
a rebuilt row and a live one cannot drift (ADR-0017) — and `value` is that number rounded to
the unit's own display precision. A closed visit's review interval is its **own row** rather
than a number bolted onto the visit code, because a row reading `V-2025-0118-001 days = 90` is
three columns contradicting each other.

### What is deliberately not on it

`QUEUE_ENTERED`, `QUEUE_CALLED` and `QUEUE_LEFT`. A queue entry is the clinic's logistics —
which line, what position, how many seconds — and on this deployment's own data every one of
them is shadowed by an `ENCOUNTER_*` event naming the same visit and the same station seconds
away. Deriving both would draw every station transition twice, and the second copy would be the
one that says nothing about the patient. The queue is read on the traffic board (CP40) and in
`read.station_activity`, by the floor supervisor whose question it answers.
`TestTheQueueIsDeliberatelyNotOnTheClinicalTimeline` holds the decision.

`AI_SYNTHESIS_REQUESTED` is out for the same reason: pressing the button is not a fact about
the patient, and the two outcomes that are — ready, and could not be produced — are both on.

Diagnoses, prescriptions and OCR-derived documents arrive with the checkpoints that create
them. Each raises `PatientTimeline.Version()`, which is what forces a rebuild — otherwise a
decade of history is missing the new kind and nothing says so.

## 7. Measured

`TestTheTimelineIsFastEnoughForATenYearPatient`, against a **300 ms** budget:

| Query                   | Worst  |
| ----------------------- | ------ |
| Whole history           | 5.6 ms |
| Last year               | 1.2 ms |
| Observations only       | 3.2 ms |
| Deep page (offset 1000) | 1.8 ms |

**p50 1.2 ms, p95 2.1 ms** over 48 queries, against **481,601 rows across 301 patients** — one
patient with a decade of quarterly visits at forty rows each, and three hundred neighbours with
the same, which is a few years of DTHC at the caseload §2 describes. `ANALYZE` is run before
measuring: the planner has never seen the table and autovacuum has not, and measuring against
stale statistics measures the wrong thing in both directions.

The test is skipped under `-short` because seeding takes forty seconds, and a suite a developer
stops running is a suite that stops finding things.

---

## 8. The scrubbable view (CP74)

`GET /v1/patients/{id}/timeline/spans?from=&to=&series=`

§8 asks for a "continuous, scrubbable, stock-chart-style view". This is the read behind it,
and it is a **second route rather than a bigger page of §5's**.

### Why the paged route could not serve it

`TimelineMaxPage` is 500, and the comment on it is right: _a decade of a diabetic patient's
history is thousands of rows and nothing renders them all at once_. CP74 is precisely the
screen that renders a decade at once, so that justification stops covering this consumer —
and the two obvious repairs are both wrong. Raising the cap raises it for every caller of a
paging list. Paging twenty times puts twenty round trips in front of the first pixel of a
screen whose acceptance criterion is _smooth interaction_.

So the response is bounded by **what can be drawn** rather than by how many rows exist.

### The duration bar is the point

A medication a patient has been on for two years is **one mark with a beginning and an end**,
not forty refill rows. That is what makes acceptance criterion 4 — _the correlation between
an intervention and a value change is visually apparent_ — achievable at all: starting a drug
becomes a bar with a beginning rather than a scatter of dots, and the eye reads "this started
here and the line bent there" without being told.

The fold is on the server so that two clients cannot do it two ways. Rows on a **durative**
lane are grouped by their English label — the only stable identity a timeline row carries for
its subject — the first opens a bar, later ones extend it and raise `count`, and a kind ending
in a closing verb (`stopped`, `discontinued`, `resolved`, …) closes it.

A bar nobody closed is **`open_ended`** and carries no `ended_at`. "We know it stopped on this
day" and "it was last seen on this day and may still be running" are different clinical facts;
`last_seen_at` is where the evidence stops, and the client draws everything past it
differently because that stretch is inference rather than record.

**The known limitation**: grouping on the label means two strengths of one drug are two bars,
which is right, and one drug relabelled mid-course is two bars, which is wrong. The honest fix
is a subject key on the projection row, and CP81's prescriptions are where one becomes
available.

### Lanes are §8's, not the projection's categories

`category` is a closed list and does not have a member for four of §8's six named lanes —
investigations, procedures, admissions, lifestyle interventions. A screen whose lane set was
the category set could not draw four of the six the checkpoint is judged on.

So a **lane** is decided from the category _unless the kind says otherwise_, and the kind is
consulted first: `admission.*`, `procedure.*`, `investigation.*`/`lab.*`,
`lifestyle.*`/`diet.*`/`exercise.*`. That is deliberately in step with §1's rule — a new kind
needs no migration, a new category is a decision — so a projection can put an admission on the
admissions lane without either. A category no lane names gets a lane of its own; nothing is
dropped.

### Lanes and series come from different places

**Lanes** are `read.patient_timeline`. **Series** are the observation read model, through an
interface `patient` is handed (`cmd/api/timeline_series_bridge.go`), because:

- an observation row carries `recorded_by` as a **user id**, which CP61's directory resolves
  to a name; a timeline row carries an employee code resolved at projection time, and
  criterion 3 wants a name in a tooltip;
- an observation carries `effective_at` _and_ `recorded_at`, its status, its unit and its own
  id — all of which a chart a physician can interrogate needs;
- it is gated by **`observation.read.values`**, which is narrower than the route's guard. A
  caller may read that a patient attended and not read their glucose, and the response says so
  in `omitted` rather than drawing an empty chart. "This patient has no HbA1c" and "you were
  not shown their values" are opposite facts.

A value that was **corrected or superseded** is returned, flagged, and drawn off the trend
line. Dropping it would answer "there was never a 14.2 here"; drawing the line through it would
show a trend the patient did not have.

### The units a clinician reads, and whose numbers the axis is

Two things about this chart are clinical rather than technical, and both were wrong first.

**HbA1c is drawn in NGSP %.** The record stores IFCC `mmol/mol` — the interoperable unit, and
the one the database converts into — but this clinic reads and prescribes in per cent. A chart
whose legend said `mmol/mol` and whose axis was marked 60, 70, 80 made a physician convert 66
to 8.2 in their head, on the one analyte the consultation turns on.

The fix is in CP44, not here: `CLINICAL_READING` names, per canonical unit, the unit a
clinician reads it in, and `dualUnit` puts that half first. Every screen in the application now
shows `8.2 % / 66 mmol/mol` rather than the other way round, the stored value is untouched, and
the next analyte whose clinical unit is not its storage unit is one line there and nothing
here. The chart's axis chooses its ticks in the reading unit and places them in the stored one,
through the same table's two directions — so it invents no arithmetic beside the record's.

**The value axis belongs to one named series and can never be unowned.** Each series is scaled
to its own extent, because a shared linear axis flattens HbA1c into a line at the bottom of the
plot. That is the right call and it has a cost: a bare column of numbers beside four lines is a
column a reader attaches to the wrong one. On the corpus that produced the first screenshots,
HbA1c, weight and both blood pressures all sat between the 60 and 80 gridlines — so a physician
reads a systolic of 70 off the axis, which is not a survivable blood pressure. The first honest
reaction to that screen is alarm and the second is distrust of the screen.

So the axis carries its owner's tint, its own rule, and **its name in words** above it; the
control naming it is derived rather than stored, so it is on a real series from the first frame
and moves when that series is turned off rather than being orphaned. The name is the signal
that survives greyscale and a photograph of the screen.

**Overlapping bars are packed into rows.** Two drugs running at once on one row means the later
covers the earlier, so the chart says a patient is on one drug when they are on two — and
adding a second agent, the commonest intervention there is, is invisible. Bars are laid out in
start order and each takes the first row whose previous bar has ended; a lane with no overlaps
stays one row.

### Measured

**The endpoint**, against a decade corpus of 10,000 observations and 36 marks, loopback:

| Query                                  | Time   | Payload |
| -------------------------------------- | ------ | ------- |
| Whole record, 5 series (10,200 points) | 105 ms | 3.8 MB  |
| Whole record, real patient (924 marks) | ~90 ms | 463 KB  |

**The chart**, in a real browser against the production build, `requestAnimationFrame`
timestamps recorded while a scripted pointer scrubs the full width and then zooms in and out:

| Viewport          | mean      | p50    | p95    | worst  |
| ----------------- | --------- | ------ | ------ | ------ |
| 1440×1100 desktop | 50–60 fps | 60 fps | 30 fps | 100 ms |
| 1024×1366 tablet  | 55–60 fps | 60 fps | 30 fps | 67 ms  |

Against a floor of **30 fps**. The p95 sits on the floor because it is dominated by the wheel
zoom, where the visible window changes and every series is re-sliced and re-reduced; the scrub
alone is a steady 60 fps.

**Three things hold that**, and each would be easy to undo:

1. **The chart's props are memoised in its parent.** The hovered value is state beside the
   chart, so every pointer move re-renders the parent; the first version rebuilt the lane array
   and the axis domain inline, so the chart's own memos all missed and a scrub re-sliced four
   series of two thousand points at pointer rate. **17 fps.** It also silently reset the zoom on
   every pointer move.
2. **Zoom is published to React once per animation frame**, not per pointer event.
3. **Only the visible window is read**, by binary search, and points are reduced to the pixel
   columns that exist — keeping each column's **extremes**, so no spike is lost. A hypoglycaemic
   reading a subsampling reduction would draw straight through is the failure that rule exists
   for.

**What the first measurement got wrong, recorded because it is the failure mode this project
keeps meeting.** The first run reported 58 fps on a desktop and 22 fps on a tablet. The
difference was not the device: at the desktop's shorter viewport the plot was **below the
fold**, so the scripted pointer never landed on it and the "scrub" was measuring an idle page.
The harness now scrolls the chart into view and refuses to run if the plot is off screen.

### What is not here

- **No compression.** The API does not gzip, so the 10,000-point response is 3.8 MB on the
  wire. That is a platform-wide gap rather than this route's, and it is the first thing to fix
  for a clinic connection.
- **Attribution is repeated per point.** Every point carries its own actor, role, station and
  source, which is what criterion 3 requires and is most of the payload. A per-response actor
  table with an index on each point would cut it by roughly two thirds and is the obvious
  follow-up.
- **Medications and diagnoses depend on a projection that does not emit them yet.** The lanes,
  the fold and the bars are proven by `spans_db_test.go` against rows written through
  `read.apply_timeline`; what a real prescription puts on the timeline is CP81's.
