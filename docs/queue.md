# The station queue

_CP39 · blueprint §5.2, §5.5, §14.2 · `core.queue_entry`_

Know where every patient is, and what is next. The Clinic Traffic Control board (CP40), the
counselling gate (§5.5) and throughput analytics all read this.

---

## 1. The one that matters

> **No patient is ever assigned to two operators at the same station.**

Two operators pressing "call next" in the same second is the **ordinary** case, not the edge
case — a station with two chairs does it all morning. It is a race no amount of care in a
handler wins, so the claim is not in a handler:

```sql
SELECT id FROM core.queue_entry
 WHERE facility_id = $1 AND station_code = $2 AND status = 'waiting'
 ORDER BY priority DESC, entered_at, id
 FOR UPDATE SKIP LOCKED
 LIMIT 1;
```

The first caller locks the head of the queue and takes it. The second **does not block behind
it** and does not get the same row — it skips to the next waiting patient, or finds none.

That is right in the database and right at a desk: the second operator wants _a_ patient, not
_that_ patient. `SKIP LOCKED` rather than a plain `FOR UPDATE` is the difference between a
station that keeps moving and a station where one slow fetch stops the other chair.

Proved with ten patients and twelve concurrent operators: every operator who gets somebody gets
a **different** somebody, and the two who miss out get `204`, not a duplicate.

`core.assert_queue_claims_are_exclusive()` holds the same rule as a standing invariant.

### An empty queue is `204`, not `404`

An operator who is free and finds nobody waiting has not made a mistake. A screen that shows an
error for it is a screen operators learn to ignore.

## 2. Priority

`0` is ordinary; higher jumps the queue. An integer rather than a boolean because "urgent" and
"this one now" are different, and a clinic will discover it needs the difference.

**A priority above zero always says why.** §4.4's critical findings jump the queue, and so does
a physician's judgement; neither may be anonymous, because jumping a queue without a reason is
the thing a queue exists to prevent. Enforced in the handler, by a `CHECK`, and by an invariant.

The ordering index is `(facility_id, station_code, priority DESC, entered_at) WHERE status =
'waiting'` — priority first, then arrival, in one index, so "call next" is a scan of exactly one
row.

## 3. Waiting times

Measured from **`entered_at`** — when the patient joined _this_ queue — not from when the visit
opened. A patient who waited two minutes for anthropometry after an hour in the building waited
two minutes for anthropometry.

Accurate to the second, and it **stops when the waiting stops**: at the call, or at the end,
whichever came first. A patient still waiting has a waiting time that grows.

The board reports both the average and the **longest** wait per station. The longest is the one
a supervisor acts on: an average of 27 minutes hides the person who has been sitting there since
nine.

## 4. Leaving a queue

| Outcome    | Means                                        |
| ---------- | -------------------------------------------- |
| `served`   | The station saw them                         |
| `skipped`  | Not needed for this patient                  |
| `rerouted` | Sent somewhere else — **says where and why** |
| `left`     | They went home                               |

A reroute with no destination is a patient nobody can find; a reroute with no reason is a
decision nobody can review. Both are refused — in the handler, by a `CHECK`, and by
`core.assert_queue_departures_are_explained()`. The destination must be a station this clinic
actually has.

Every reroute carries the operator, their role and their station on the event.

## 5. One live entry per station

`queue_one_live_per_station` is a partial unique index over `(visit_id, station_code)` where the
status is live. A patient waiting twice is a patient who will be called twice, and the second
call is the one nobody can explain.

Once an entry is resolved the patient may be queued at that station **again** — which is exactly
what a QA bounce needs, and what makes rework countable (§14.2).

## 6. The station sequence is data

`core.station_sequence` holds the planned journey per visit type. §3's twelve stations for a new
patient; a shorter one for a follow-up, who does not repeat the history and records stations.

A table rather than a constant because **the sequences are an operational decision** Dr. Nahid
owes, and a sequence in Go is a sequence that needs a deployment to change.

The `position` lands on the queue entry so the board can order by the journey — but it is for
ordering and for "what is next", **not for enforcement**. A patient sent back from QA has a
position behind them, and refusing that would be refusing the clinic's actual flow.

## 7. The API

| Route                                     | Needs          |
| ----------------------------------------- | -------------- |
| `GET /v1/stations/board?day=`             | `visit.read`   |
| `GET /v1/stations/{station}/queue`        | `visit.read`   |
| `GET /v1/visits/{id}/queue`               | `visit.read`   |
| `POST /v1/visits/{id}/queue`              | `visit.attend` |
| `POST /v1/stations/{station}/call-next`   | `visit.attend` |
| `POST /v1/stations/queue/{entryId}/leave` | `visit.attend` |

The RBAC engine's own station scoping (§4.4) narrows `visit.attend` to the operator's own
station. This module states the permission and does not re-implement the scoping: two copies of
a scoping rule is one copy that drifts.

Every transition is an event on the `VISIT` aggregate — `QUEUE_ENTERED`, `QUEUE_CALLED`,
`QUEUE_LEFT` — written in the same transaction as the row. `QUEUE_CALLED` carries the waiting
time it measured, so §14.2's analysis does not depend on two timestamps surviving every future
migration of the read model.

## 8. Still owed

The **default station sequences per visit type** are an operational confirmation. What is seeded
is §3's journey as the blueprint describes it, and changing it is an `UPDATE`, not a deployment.
