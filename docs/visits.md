# Visits and encounters

_CP38 · blueprint §3, §11.1, §14.2 · ADR-0023_

A **visit** is one journey through the clinic. A **encounter** is one station touch. The
second is the one that pays for itself: encounters cost almost nothing to record and make
§14.2's bottleneck analysis a query rather than a project.

---

## 1. The state machine

```
        ┌──────────┐  close   ┌──────────┐
        │   open   │─────────▶│  closed  │
        │          │◀─────────│          │
        └──────────┘  reopen  └──────────┘
             │  abandon             ▲
             ▼                      │
        ┌───────────┐               │
        │ abandoned │───────────────┘
        └───────────┘   reopen
```

Four legal moves and nothing else, held by a **trigger** as well as by Go. There will be a
projector, a repair script and later modules writing these rows, and a rule enforced in one
handler is a rule the other three do not have.

In Go the machine is _data_ rather than a switch, so the test enumerates every pair — including
the six that must fail — instead of the four somebody remembered.

**`abandoned` is not `closed`.** §14.2 counts throughput, and a visit nobody completed must not
be counted as a completed journey. The number that results is the one somebody puts in a report.

## 2. §11.1's visit memory

A closing visit records four things, and a database invariant refuses one that does not:

| Field              | Why                                                                     |
| ------------------ | ----------------------------------------------------------------------- |
| chief complaint    | What the patient came with, in their own words                          |
| diagnoses          | What it turned out to be                                                |
| plan               | What was decided                                                        |
| next review (days) | **A number**, because the outreach engine reads it to decide who is due |

The chief complaint is captured at _open_, by the desk, and may be corrected at close — the
desk hears "sugar problem" and the physician writes what it turned out to be.

A closed visit **cannot be edited in place**: a trigger refuses it. §4.3's correction principle
applies to a visit as much as to a value. Reopening is the path, and it is recorded, counted on
the record, and needs a reason.

> **Still open:** the _policy_ for when a visit may be reopened is an operational confirmation.
> The mechanism is built; the rule that says "only the physician who closed it, only the same
> day" is a check that can be added without changing anything else.

## 3. Encounters

One row per station touch, with a start, an end, a person and a role.

| Outcome        | Means                       |
| -------------- | --------------------------- |
| `completed`    | The station is done         |
| `skipped`      | Not needed for this patient |
| `bounced`      | Sent back, typically by QA  |
| `patient_left` | They did not wait           |

`bounced` is its own outcome rather than a completed touch with a note, because §14.2 counts
rework and a bounce recorded as "completed" makes rework invisible — which is the one number a
quality gate exists to produce. A patient returning to a station is a **new encounter**, for
the same reason.

An encounter **ends once**. A trigger refuses a second finish.

## 4. Two races, held by indexes

| Rule                                     | Held by                                           |
| ---------------------------------------- | ------------------------------------------------- |
| One open visit per patient               | `visit_one_open_per_patient` (partial unique)     |
| One open encounter per station per visit | `encounter_one_open_per_station` (partial unique) |

Both are races two tablets lose in the same millisecond, and no amount of care in a handler
wins them. The handler's check exists only to produce a useful message; the index is what
holds. Both are proved with twelve concurrent attempts: exactly one succeeds.

A second attempt to open answers `409` **with the visit the patient already has**, so the desk
sends them to the queue rather than trying again.

## 5. Timing

`GET /v1/visits/{id}` returns two numbers, computed on read:

- **`total_seconds`** — how long the patient has been in the building.
- **`waiting_seconds`** — the total minus time actually at a station.

The second is what a patient experiences. Twenty minutes of care inside two hours in the
building is the complaint, and it is the number a clinic can act on.

Both are computed rather than stored because both change while the visit is open, and a stored
total is a total that is wrong between updates.

## 6. The visit code

`V-2026-0914-017` — the seventeenth patient of that morning, spoken at a desk and called out by
a queue board. Gapless per facility per clinic day, under a row lock like the clinical id: a
gap reads as a lost patient.

The **clinic day is its own column**, in Asia/Dhaka. A visit opened at 23:50 and closed at
00:10 belongs to one day, and the board asks for it by that day all night.

## 7. Permissions

| Permission     | Held by                                     |
| -------------- | ------------------------------------------- |
| `visit.open`   | Registration, field workers, administrators |
| `visit.close`  | Physician, QA, administrators               |
| `visit.read`   | Every station role                          |
| `visit.attend` | Every station role                          |

Four new permissions, added by migration as CP21 added one. Reusing
`patient.write.demographics` would mean a physician closing a visit needs the permission to
rewrite a name — the over-grant §4.4 exists to stop.

**Reopening needs `visit.close`, not `visit.open`.** It undoes a close, and the authority that
should hold it is the one that made the decision.

## 8. The API

| Route                                                  | Needs          |
| ------------------------------------------------------ | -------------- |
| `POST /v1/visits`                                      | `visit.open`   |
| `GET /v1/visits/today?day=`                            | `visit.read`   |
| `GET /v1/visits/{id}`                                  | `visit.read`   |
| `GET /v1/patients/{id}/visits`                         | `visit.read`   |
| `POST /v1/visits/{id}/close`                           | `visit.close`  |
| `POST /v1/visits/{id}/abandon`                         | `visit.close`  |
| `POST /v1/visits/{id}/reopen`                          | `visit.close`  |
| `POST /v1/visits/{id}/encounters`                      | `visit.attend` |
| `POST /v1/visits/{id}/encounters/{encounterId}/finish` | `visit.attend` |

Every transition is an event on the `VISIT` aggregate, written in the same transaction as the
row it describes: a visit row with no event behind it is a fact with no history, and an event
with no row is a queue board that does not know the patient arrived.

The **retry check comes before the "already open" check**. Order matters and getting it wrong
is subtle: a tablet that opened a visit, lost the reply and pressed again would otherwise be
told the patient already has one — which is true, is the visit it just opened, and reads at the
desk as somebody else's mistake.

## 9. What still needs a real morning

The plan's own risk note: **the real clinic flow may not match this state machine.** Four states
and a linear station journey is what §3 describes; a morning at DTHC may not be. Walking a
synthetic patient through five stations is proved by test; walking a real day's patients through
it is the manual verification this checkpoint asks for and cannot do for itself.
