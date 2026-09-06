# The offline sync protocol

CP65. The server half of §15.2's guarantee: **a Wi-Fi drop must never lose a station entry.**

---

## What was already true before this checkpoint

Two of the five acceptance criteria hold without a line of sync code, and it matters to say which
so that nothing here is mistaken for re-implementing them.

- **"Duplicate submission produces no duplicate events."** `event_id` is client-generated and is the
  ledger's key (CP23). `Append` returns the first row with `Duplicate` set. A batch replayed in full
  lands as forty duplicates and no new rows.
- **"`occurred_at` is preserved; `recorded_at` reflects arrival."** The envelope has carried both
  since CP23, for exactly this checkpoint.

What is left is the batch, and four decisions inside it.

## One transaction per event, not per batch

Criterion 1 — _a bad event never blocks the rest of its batch_ — is not achievable with a batch
transaction, and savepoints buy nothing: a rejected event has nothing to roll back, because the
reason it was rejected is that it never got written.

So each event is appended on its own. **The cost is that a batch is not atomic**, and that is stated
rather than hidden: a client whose connection drops halfway has some events in the ledger and some
not. That is precisely the case `event_id` idempotency exists for — resend the whole batch and the
landed ones come back `DUPLICATE`.

## Ordering, and what "preserved" can honestly mean

Events are applied in the order the client sent them, and events for one aggregate are applied
serially. That is criterion 4.

The interesting case is the failure. If the third of forty observations for one patient is rejected,
**the remaining thirty-seven proceed** — they do not depend on it, because an observation is a fact
about a moment rather than a mutation of a state, and blocking them would hold a morning's work
hostage to one typo.

The exception is an event that _declares_ a dependency by carrying `expected_sequence` (§7.9). That
event is saying "apply me only if the aggregate is where I think it is", and after an earlier
failure it is not. Attempting it would produce a sequence conflict — an answer that is true and
misleading, because it points the client at the wrong problem — so it comes back `BLOCKED` without
being attempted, and the client resends after resolving the first.

| Outcome       | Means                                                              | What a client does                                                                                                                                     |
| ------------- | ------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `ACCEPTED`    | It is in the ledger.                                               | Drop it from the outbox.                                                                                                                               |
| `DUPLICATE`   | It was already there.                                              | Drop it. **Not an error** — it is what a resent batch looks like, and a client that treated it as one would never make progress after a lost response. |
| `REJECTED`    | It will never be accepted as sent.                                 | Stop resending. `reason_code` says why.                                                                                                                |
| `QUARANTINED` | Held for a person to decide about.                                 | Stop resending; it is not lost.                                                                                                                        |
| `BLOCKED`     | Not attempted, because an earlier event on the same record failed. | Keep it; send again after the first is resolved.                                                                                                       |

## The receipt

A `batch_id` is required, and it is the client's receipt number.

The case it exists for is not duplicate submission — the ledger absorbs those. It is the client that
sent fifty events, had them processed, and **lost the response on the way back**. Without a receipt
it can only resend and hope, or drop and hope. With one it asks `GET /v1/sync/batches/{id}` and is
told, per event.

A batch that was already processed is answered from its stored results without touching the ledger,
which has a second benefit: a device revoked _since_ that batch cannot change the outcome of one
that was already accepted.

**Only if it is closed.** The receipt row is opened before the first event, so a server interrupted
halfway leaves one that exists and reports nothing. Answering from it would tell a client "your
fifty events produced no results" for ever — the opposite of what this endpoint promises. So an open
receipt is **reprocessed**, which is safe precisely because `event_id` is the ledger's key: whatever
landed comes back `DUPLICATE` and nothing lands twice. `closed` on the receipt is how a client tells
a finished batch from an interrupted one.

**The sending device is the one that signed, not the one in the body.** `device_id` is optional and,
when present, is _checked_ rather than trusted — a mismatch is refused, because it means the client
is confused about which device it is. Trusting it would have let anyone holding a station write
permission attribute a batch, its quarantine rows and another tablet's sync state to a device that
did not send it. Ledger attribution was never at risk — `eventstore.Actor` is unforgeable — but
device attribution was, and the quarantine is read by somebody deciding whether to trust a
particular tablet.

**`global_seq` is stored as well as returned.** The receipt exists for the client that lost the
response, and that client needs the cursor as much as the outcome — without it, it pulls its own
writes back down.

There are two idempotency mechanisms here, at two layers, and both earn their place.
`Idempotency-Key` is the transport's — a request retried inside the cache window gets the stored
response without the handler running. `batch_id` is the protocol's, and it is durable: a client that
comes back a day later, or after a restart, can still ask. The header cannot do that because its
cache expires; the batch id cannot do the first because by the time the handler reads it the work
has begun.

## The revoked device

This is the one case with no automatic answer, and it is why this checkpoint has tables.

A station tablet is revoked at nine in the morning. It has been offline since eight and holds forty
real blood pressures, taken by a real operator, on patients who have gone home.

- **Accepting them defeats the revocation.** Whatever caused it — the tablet was stolen, the operator
  left, the key leaked — is exactly the reason not to trust what it sends.
- **Dropping them loses a morning of clinical measurements, silently.** The operator believes the
  work is recorded. Nobody finds out until a physician wonders why a patient has no vitals.

So neither. The events are **held, in full, outside the ledger**, and a person decides.

### The scenario was unreachable, and that was the sharpest bug in this checkpoint

The device middleware refused any non-active device before a handler ran, so the revoked tablet's
push met a **401 indistinguishable from an expired token** — and a client following §13.8's "wipe on
revocation" would then destroy exactly the forty measurements the quarantine exists to preserve. An
entire designed mechanism that could not fire, found by the client-side review rather than by any
server test, because every server test constructed the handler directly and never went through the
middleware that made it impossible.

The fix moves the _status_ decision, not the authentication. `POST /v1/sync/events` is the one route
a correctly-signed but no-longer-active device may reach, named explicitly in `QuarantineRoutes` at
the composition root. The signature must still be by the device's live key, the timestamp fresh and
the nonce unseen; an unknown device, a forged signature or a replay are refused here exactly as
everywhere else. What is permitted is a revoked device **handing over what it already has**, to be
judged by a person. It still cannot read a patient, open a visit, pull events, or record anything.

(The route list lives beside the router rather than on the route's own `httpx.Declare` requirement
because chi runs `Use` middleware before resolving the route pattern, so there is nothing for a
declaration to be read from. Whole method-and-path pairs, never prefixes, so no route inherits the
exemption by sharing a prefix with one that has it.) Everything
about `ops.sync_quarantine` follows from the two things it must never do: lose the content, and let
it into the record without somebody's name on the decision.

- The whole envelope is stored, which makes this the one table outside `ledger` and `read` that can
  hold clinical values. Reading it is a **sensitive permission of its own**.
- Nothing is ever deleted — `DELETE` is revoked from the application role. A discarded measurement
  with no trace is the silent loss this table exists to prevent, one step later.
- The triage list carries **no clinical values**: event type, when, which device, which operator. A
  supervisor sees "eleven blood pressures and two weights" without a measurement appearing on a
  screen that shows a count. The values are on the single-event read, which is a separate request and
  a deliberate one.
- Three invariants (94, 95, 96) hold the rest: every resolution names a person, every released event
  is actually in the ledger, and every receipt agrees with its own per-event results.

### Who decides, and whose name goes on it

Reading the quarantine is the physician's and the administrator's. **Releasing is the physician's
alone**, and the asymmetry is the point: the administrator revoked the device, and somebody who can
both refuse a device and then admit its data has undone their own control.

A released event keeps its **original `occurred_at`** — a blood pressure taken at 08:40 and released
at four in the afternoon is a fact about 08:40, and dating it at release would put it after every
measurement taken since, on every timeline, forever.

It carries the **releasing physician's** name, not the operator's. That is not a compromise forced by
`eventstore.Actor` being unforgeable, though it is: it is the accurate statement of who is
answerable. The reason this event is in the permanent record is that a physician decided it should
be, on evidence from a device somebody had already refused to trust. Attributing it to the operator
would say it entered on that operator's authority, which is exactly what the revocation denied.

The operator is not lost. The original user, device, hold reason and quarantine id go into the
event's metadata, so a reader years later sees a value recorded by the physician **on behalf of** a
named operator, from a named device, released out of quarantine for a stated reason — the whole
truth rather than half of it.

## The clock

Skew is measured on every batch and reported on every receipt, not only when it is large: a client
that only learns about skew once it is a problem cannot correct for it gradually. A client that
sends no clock reading gets `clock_skew_ms` **absent** rather than zero — unknown is not the same as
none.

Only the _future_ direction is refused. Being in the past is unbounded and always fine — a device
offline for a week is the entire point of this checkpoint. An `occurred_at` more than five minutes
ahead of the clinic's clock is **held rather than rejected**: the measurement is real and only its
timestamp is untrustworthy, so a person can say what time it actually was. Left alone it would sort
above every real measurement forever, and nothing downstream could distinguish it from a genuine
future-dated record, because there is no such thing.

## The pull

Scoped twice: to the caller's facility, and to the event types their permissions allow.

The second is a **declared allowlist**, in `internal/offline/pullable.go`. An event type nobody has
decided about is not pullable at all, and `TestEveryEventTypeIsDecidedAbout` fails the build rather
than letting the default be whatever the query happens to do. A denylist would put a type added next
month on every phone in the clinic before anybody noticed — working correctly, for months.

Each pullable type names **the same permission that guards its HTTP route**. Two different answers to
"may this person see this" is how §4.4's blinding quietly stops meaning anything.

What is pullable today is narrow and argued for one line at a time: who the patient is, the visit
being worked within, what other stations have recorded, the allergy that has to reach everyone who
meets the patient, the counselling checklist, and a critical value somebody has to acknowledge.
Medical history, diagnoses, consent, corrections and the gates are not — each with its reason in the
file.

`cursor` is on every page, including an empty one. A client that had to infer it from the last event
would have none when the page was empty and would ask the same question forever.

## Reference data

`GET /v1/sync/reference` returns a row per catalogue: a count and a fingerprint. One small request
tells a phone holding six catalogues for a morning whether any of them moved.

A fingerprint rather than a timestamp because several of these tables have no `updated_at`, and
adding one to each would be five migrations to answer a question a hash already answers. It says
_that_ something changed, not what — a client that needs to know re-fetches the catalogue, which is
one request and the thing it was going to do anyway.

## Limits, and why a refusal beats a truncation

`MaxBatch` is 500. Exceeding it is a **refusal**, not a truncation: a client told "accepted" for a
prefix and left to work out which is a client that drops the rest.

The plan lists the batch size as tunable by measurement, and it should be tuned — but the shape of
the answer matters more than the number.

### Opening the push to a refused device left a hole, and closing it took two layers

The quarantine and the exemption that makes it reachable are each right on their own. Together they
were a problem nobody had before them:

- `POST /v1/sync/events` is the one route a device the clinic has **revoked** may reach, so that a
  tablet holding a morning's blood pressures can hand them over instead of destroying them.
- `dthcms_app` has no `DELETE` on `ops.sync_quarantine`, so that a record of what was refused
  cannot be tidied away.

A stolen tablet with a live key could therefore append permanent rows to a table that is never
pruned, as fast as it liked. The damage is not "the disk filled" — it is a triage list nobody can
finish, which is how the quarantine stops being read at all, which is how the silent loss this
whole design prevents arrives anyway.

**A per-device rate limit** (`syncRateLimits` in `cmd/api`, the token bucket in
`platform/cache`) bounds the rate. Twelve pushes at once and twelve a minute sustained, per device
— a tablet coming back into signal sends four or five batches back to back and must not be slowed
down for it, and twelve batches a minute is six thousand events a minute, far more than a station
generates and far less than a loop can. The pull's budget is much looser, because a device seeding
itself has a lot of pages to walk and what a fast pull costs is bandwidth, not a row somebody has
to read.

That limiter **fails open** — a clinic must not stop taking blood pressures because Redis
restarted — which is only defensible because of the second layer.

**A per-device cap on held events** (`migrations/00050_sync_limits.sql`) bounds the total. Two
thousand awaiting review, enforced by a `BEFORE INSERT` trigger, checked by invariant 97, and
therefore true whether or not the cache is up and whether or not some future code path remembers to
ask. The number lives in `ops.quarantine_cap()` and nowhere else: a constant in Go beside it would
be a second number that agrees until somebody changes one.

Three things about the ceiling are worth stating plainly, because each was a choice:

- **At the ceiling an event is `BLOCKED`, never `REJECTED`.** A conforming client drops a rejected
  event. Rejecting here would lose a real blood pressure because a supervisor is behind on their
  reading — the silent loss, by a new door. Blocked means "keep it, send it again".
- **The allowance is cleared by triage, not by time**, because it counts `HELD` rows. The resource
  actually being protected is somebody's attention, so tying the ceiling to the state of their
  queue is the honest coupling.
- **A refused device at its ceiling is turned away whole (429), a working one is not.** Every event
  from a refused device would be held, so at the ceiling nothing in its batch can land, and opening
  a batch row and five hundred result rows to say so would just move the flood into the receipt
  tables. A device that is _not_ refused — a newer app during a rolling deploy, sending a type this
  server has not learned — keeps writing its valid measurements, and only the ones that would have
  needed a hold come back blocked. A ceiling that locked a working tablet out of recording clinical
  values would be the denial of service arriving from our own side.

### What the ceiling looks like on the tablet

The server half is only half the answer. A client that is told "no room" and keeps asking is the
flood arriving from the honest side, and the first version of this client did exactly that: a
`QUARANTINE_FULL` block went back into the very next batch, because the outbox only holds a blocked
row back when an _earlier event on the same record_ is unresolved — and a device-wide ceiling is not
that. Every tick: push, blocked, push, blocked, on the operator's battery and against the very
limiter that had just been added.

So a ceilinged event gets its own outbox state, `AWAITING_TRIAGE`, rather than sharing
`BLOCKED_LOCAL` with a reason code. The two are cleared by different people in different buildings,
and the number on the operator's screen is the difference between "you have something to fix" and
"somebody at the clinic does". It waits half an hour, jittered, off the backoff ladder entirely —
the thing being waited on is a person's reading list, not a network.

Two honesty problems came out of building the screen, and both are worth recording because they are
the kind that survive review:

**The status must not read as "queued".** "3 waiting to be sent" over three entries that will not
move until a physician acts is true and useless. The panel says _"3 cannot be sent until the clinic
reviews what this tablet has already sent"_, and the label is "Waiting for room at the clinic" —
never "waiting to be sent", which reads as "give it a minute".

**"Last checked" must name a moment the clinic actually answered at.** The timestamp is written in
exactly two places, both of them a reply: the receipt transaction that parks a `QUARANTINE_FULL`
event, and the whole-batch 429. Writing it where the button is pressed would claim a check at a
moment the tablet had not spoken to anybody — a tablet in a corridor with no signal, telling its
operator it had just asked. And the button ("Check with the clinic again") is floored from the
clinic's _last answer_ rather than the last press, so twenty presses are one question, while a press
that never left the building costs nothing and may be repeated at once.

Nothing on the device can observe triage — the tablet is refused everywhere except the push, so it
cannot even ask. Half an hour is a poll. The honesty lives in the copy, which names the clinic and
the physician, not in the timer.

### The recovery path was a trap, and it predates all of this

Found while testing the above, and it is the sharpest bug of the pair. A revoked device mid-handover
whose connection drops leaves its rows `IN_FLIGHT` with a batch id. The next attempt does what the
protocol says: it asks `GET /v1/sync/batches/{id}` for the receipt it never received. That route is
not in the quarantine exemption — only the push is — so it collects a 401, three of those halt the
tablet with `DEVICE_REFUSED`, and the morning is stranded. Permanently: the receipt resolution runs
_before_ the push loop, so the device never reaches the push again, and the deliberate human
`resume()` walks straight back into it. A probe against the pre-fix build handed over 5 of 12
measurements and then stopped for good.

That is the silent loss this whole checkpoint exists to prevent, arriving through the recovery path.

The fix is one branch on the client, not a wider exemption on the server: **during a handover, do
not ask for the receipt — send the batch again under the same id.** `Push` checks the stored receipt
_before_ it reads the device, so a re-push of a batch that did close is answered from the record
without touching the ledger. That is the receipt lookup, obtained through the one route a refused
device may use. A batch left open is reprocessed instead, which is safe for the reason everything
here is safe: `event_id` is the ledger's key. Widening the exemption would have added a second route
a refused device may reach in order to buy something the first one already gives.

The two layers overlap on purpose, and the test suite says so: removing the Go counter alone leaves
every test passing (the trigger catches it), and removing the trigger alone leaves every test but
one passing (the counter catches it). Neither is load-bearing by itself. `TestTheDatabaseRefuses...`
is the one that inserts straight past the service, which is the only test that can tell whether the
layer that survives a Redis outage is still there.

## Open decisions

- **The batch size limit.** 500 is the checkpoint's own figure and roughly a full clinic day for one
  station. Tune it from real traffic; keep the refusal.
- **The per-device quarantine cap.** Two thousand held events. Sized so an honest tablet never
  approaches it and a supervisor could not clear it in a sitting anyway; tune it from real traffic
  the first time a device is genuinely revoked with a backlog. What is not tunable is that it
  exists.
- **The five rate-limit numbers** (two rules, burst and interval each, and the coalescing window on
  the outage warning). Guesses shaped by argument rather than by measurement. The push's is the one
  to watch: too tight and a tablet back from a day offline takes longer than it should to catch up.
- **The clock tolerance.** Five minutes forward. Too tight quarantines honest devices with drifting
  clocks; too loose lets a genuinely wrong one into a timeline.
- **What else becomes pullable.** Every addition is a line in `pullable.go` with a permission and a
  reason, and should stay that way.
- **Who reads the quarantine in practice.** This shares CP57's and CP63's failure mode: a queue
  nobody looks at is worse than none, because it looks like oversight. A held event that nobody
  decides about is a measurement that never reaches the record, which is the silent loss the whole
  design exists to prevent, arriving by a slower route.

## Carried into CP67 and CP68

Four things the offline work surfaced that are not this checkpoint's to answer, recorded here so
they are not rediscovered:

- **A critical value recorded offline pages nobody until it syncs.** CP50's escalation runs on the
  server, against events in the ledger, so a dangerous reading taken at a station with no signal is
  a dangerous reading nobody is told about for as long as the tablet is out. The device knows the
  reference ranges — it has the catalogue — so it can say something to the operator standing there;
  what it cannot do is escalate. Whether the tablet should refuse to hold such a value quietly, or
  say loudly that it is holding one, is a clinical decision, not a protocol one.
- **The reference cache has no age limit.** §13.7 requires refusing writes against a catalogue
  beyond a configured age, and nothing enforces one: a tablet that has not synced for a month
  records against a month-old formulary and range table without complaint. The seven-day default in
  the plan is unapproved and the enforcement is unwritten.
- **The status pill is true and will become misleading.** Only vitals are converted to the offline
  path; the other eleven stations write straight through and never enter the queue. "Everything on
  this tablet is with the clinic" is therefore true of what the queue knows and silent about what it
  does not — correct today because those stations simply fail without signal, and a trap the moment
  a second station is converted and the sentence stops covering it.
- **A halted tablet still accepts new work**, deliberately: an operator at the bedside must be able
  to write a measurement down. That is right, and it makes the halt's visibility CP67's problem — a
  tablet quietly accumulating work it has already stopped trying to deliver is the same silent loss
  with a calmer screen.
