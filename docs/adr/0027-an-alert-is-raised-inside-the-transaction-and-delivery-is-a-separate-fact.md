# ADR-0027 · An alert is raised inside the transaction that stored the value, and delivery is a separate fact

**Status:** Accepted · **Date:** 4 Sep 2026 · **Checkpoint:** CP50 · **Decisions:** D-27 (open)

## Context

§3 step 5 asks for "immediate critical-value alerts (SpO₂ < 92%, BP > 180/110) with visual and
audible warning", and §4.4 says a critical finding bypasses the queue. The plan adds
acknowledge-or-escalate with a timeout, an escalation chain, and a fail-safe: **if the alert
cannot be delivered, the entering operator is told to escalate verbally.**

Two things about that are harder than they look.

The first is _when_ an alert comes into existence. The obvious design — store the value, then
notice afterwards that it was dangerous — leaves a window in which a saturation of 88% is in
the record and nothing is coming. The window is small and it is exactly the wrong window.

The second is what "delivered" means. A message published to a channel nobody is listening on
has been published. It has not been delivered, and the difference is the whole of the
fail-safe: an operator who is told "the consultant has this" when the consultant's tablet is
in a drawer has been told something false at the one moment it matters.

## Decision

### 1. The alert is appended in the same transaction as the observation

`clinical.Service.raise` runs inside `appendRecording`, against the same `pgx.Tx`, immediately
after the `OBSERVATION_RECORDED` event. Either both facts exist or neither does.

This is what makes the refusal case correct as well as the success one. A saturation of 20% is
below the absolute plausibility floor: nothing stores it, and nothing alerts on it either,
because an alert about a value that does not exist sends a consultant to a patient whose probe
had fallen off.

The alert's event id is **derived from the observation's**, so a tablet that lost the reply and
saved again writes the same alert id — the ledger absorbs the duplicate and the consultant's
phone rings once. A random id there would make a corridor with bad Wi-Fi into a phone that goes
off once per retry until somebody stops looking at it.

### 2. Nothing is published from inside the transaction

CP26 already says it: a message published from inside a transaction is a message about a write
that may still roll back, and there is no un-ringing a phone. So the sequence is: raise,
commit, then attempt delivery.

### 3. Delivery is its own event, because it is not knowable when the alert is written

`CRITICAL_VALUE_DELIVERY_ATTEMPTED` carries how many live screens the alert reached and, if the
publish failed, why. It is appended after the commit by whichever process attempted it.

This is why there are four event types where the plan named three. "The value was dangerous"
and "the clinic was told" are different facts with different timing, and folding the second
into the first would have meant either publishing before the commit or writing a field whose
value is a guess.

The read model's default is therefore **nobody was told** — which is the right default, because
an API that died between the commit and the publish leaves an alert that reads as undelivered,
which is what it is.

### 4. "Delivered" is measured by presence, not by a successful publish

`realtime.Presence` is a per-facility sorted set in Redis. A gateway connection whose subject
holds `alert.read` registers on connect, renews on every heartbeat, and is swept out one lease
after it stops answering. The API counts that set before publishing.

Two consequences are deliberate:

- A presence lookup that **fails** counts as zero. "We do not know" must read as "nobody",
  because the cost of wrongly telling an operator to walk down the corridor is a wasted minute
  and the cost of wrongly telling them not to is a patient.
- The lease is a small multiple of the heartbeat, so a tablet that went into a bag stops
  counting within a minute — inside the escalation chain's first window rather than outside it.

### 5. The escalation chain's last step names no role

Steps 1 and 2 notify the consultant and then the junior doctor as well. Step 3 notifies
**nobody**: it tells the operator who entered the value to go and find somebody in person.

A chain whose final link is another notification has no end. In a twelve-station clinic where
everyone is within thirty metres, the escalation that always works is a person walking to
another person, and a system that cannot say so is a system that quietly assumes its own
network is up.

### 6. Escalations are written by a sweep, under a service actor

A timer per alert lives in one process's memory, and is lost by the restart that is _most_
likely to have dropped the socket too. The worker sweeps durable state instead, so a worker
started thirty seconds late catches up in its first pass, and two workers running at once are
harmless — the escalation event's id is derived from the alert and the step.

That needs an actor no request produced. `eventstore.ActorForService` is that door, and it is
held to `cmd/worker` by a new dthclint directive (`//dthclint:callableFrom`), because the moment
such a constructor exists any handler could attribute its own writes to "the system" and step
outside the attribution guarantee the unexported `Actor` fields exist to enforce.

### 7. The thresholds and the chain are rows, and every row is a proposal

D-27 — the critical-value table and the escalation chain — is open and blocking, and it is Dr.
Nahid's to author. `approved_at` is null on every seeded row and the API reports it, because a
screen that presented these as settled would overstate what anybody has agreed to.

What is **not** provisional is the mechanism. Raising, delivery, acknowledgement, escalation and
the audit trail are built and tested; D-27 changes rows, not code.

## Consequences

- A critical value cannot exist in the record without its alert, and an alert cannot exist
  without the value that raised it. Neither can be lost by a crash between them.
- The clinic can answer "how often did an alert reach nobody" from its own data, which is the
  number that decides whether the safety feature is real or decorative.
- Four invariants stand behind the thresholds: none can sit outside what its code can hold,
  none may fall inside the matching normal range, the chain must start immediately and end with
  a person, and every rule must name a live numeric code.
- The audible alarm is a new dependency (`expo-audio`) and a new asset. React Native core has no
  sound API, and criterion 1 asks for an audible warning in as many words; the alternative was
  to ship the visual half and call it done.
- Two things are still owed and neither is code. D-27 must land. And the sound design has to be
  validated on the clinic floor — a tone that is inaudible over a busy waiting room, or so
  startling that staff turn the phone over, fails the criterion whatever the tests say.
