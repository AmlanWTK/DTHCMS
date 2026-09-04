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

| Event                            | Category         | Rows                        |
| -------------------------------- | ---------------- | --------------------------- |
| `PATIENT_REGISTERED`             | `registration`   | one                         |
| `PATIENT_DEMOGRAPHICS_CORRECTED` | `administrative` | one per changed field       |
| `PATIENT_MERGED`                 | `administrative` | one                         |
| `PATIENT_PHOTO_CAPTURED`         | `document`       | one, carrying no key or URL |
| `CONSENT_GRANTED` / `_REVOKED`   | `consent`        | one per consent type        |

Visits, observations, diagnoses and prescriptions arrive with the checkpoints that create them.
Each raises `PatientTimeline.Version()`, which is what forces a rebuild — otherwise a decade of
history is missing the new kind and nothing says so.

The photograph row deliberately carries **no object key and no URL**. A timeline row is read by
everyone who may read the record; the image is fetched from its own endpoint, which mints a
signed URL per request and audits the read.

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
