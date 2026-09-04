# Critical values, and what happens when nobody answers

CP50. Blueprint §3 step 5, §4.4, [R-01].

The system's most direct patient-safety feature, and the one with the most ways to be quietly
useless.

---

## Four bands, and only the last one makes a phone ring

| Band                       | Outside it, the value is…     | What happens                       |
| -------------------------- | ----------------------------- | ---------------------------------- |
| The registry (CP42)        | not storable at all           | refused, by a database trigger     |
| Plausibility (CP46)        | probably a typing error       | refused, or confirmed and recorded |
| The reference range (CP49) | not ordinary for this patient | the field turns amber              |
| **Critical (CP50)**        | **dangerous now**             | **an alarm, and a chain**          |

A systolic of 210 is storable, plausible, outside the normal range, and critical. Only the last
of those means somebody has to move.

Conflating the third with the fourth is how clinical software teaches its users to ignore both,
so they are four tables, four pieces of code and four different words on the screen.

---

## Every number here is a proposal

**D-27 is open and blocking.** The critical-value table and the escalation chain are Dr.
Nahid's to author. `approved_at` is null on every seeded row, the API reports it, and the
screens say so.

Two rows are not proposals: SpO₂ below 92% and a blood pressure above 180/110 are named in the
blueprint itself.

What is not provisional at all is the mechanism. Raising, delivery, acknowledgement, escalation
and the audit trail are built and tested. **D-27 changes rows, not code** — which is the whole
reason the thresholds are rows.

---

## The sequence, and why it is in that order

```
operator types 88 into the SpO2 field
  │
  ├─ the phone's own copy of the rules turns the field red         (offline, instant)
  │
  └─ save
       │  ┌─────────────────── one transaction ───────────────────┐
       │  │ OBSERVATION_RECORDED                                  │
       │  │ CRITICAL_VALUE_ALERTED                                │
       │  └───────────────────────────────────────────────────────┘
       │            commit
       │
       ├─ how many screens with alert.read are live right now?      (Redis presence)
       ├─ publish on the patient topic and on each of their topics
       ├─ CRITICAL_VALUE_DELIVERY_ATTEMPTED  ← how many, and any error
       │
       └─ the write's response carries the alert back to the phone
              → full-screen modal, alarm sounds, no way past it but "I have read this"
              → if nobody was watching: "Go and tell a doctor in person, now"
```

Then, if nobody acknowledges:

```
  0s    step 1  the consultant                     (immediate, at raise)
120s    step 2  the junior doctor as well          (a sweep in cmd/worker)
300s    step 3  no role at all — the operator is told to go and find somebody
```

### Why the alert is inside the transaction

Because the alternative leaves a window in which a dangerous number is in the record and
nothing is coming. Either both facts exist or neither does — and that is what makes the refusal
case correct too: a saturation of 20% is below the plausibility floor, so nothing stores it and
nothing alerts on it, because an alert about a value that does not exist sends a consultant to
a patient whose probe had fallen off.

### Why delivery is a separate event

It is not knowable when the alert is written. Nothing may be published until the transaction
has committed, so the alert is raised, the commit happens, delivery is attempted, and what
happened is appended as its own fact.

The read model's default is therefore **nobody was told**. An API that died between the commit
and the publish leaves an alert that reads as undelivered, which is exactly what it is.

### Why "delivered" is measured by presence

A message published to a channel nobody is listening on has been published. It has not been
delivered, and the difference is the whole of the fail-safe.

`realtime.Presence` is a per-facility sorted set in Redis. A gateway connection whose subject
holds `alert.read` joins it on connect, renews on every heartbeat, and is swept out one lease
after it stops answering. A presence lookup that **fails** counts as zero: "we do not know" must
read as "nobody", because the cost of wrongly telling an operator to walk down the corridor is a
wasted minute and the cost of wrongly telling them not to is a patient.

---

## Who may do what

| Permission          | Who has it               | What it is                                    |
| ------------------- | ------------------------ | --------------------------------------------- |
| `alert.read`        | PHYSICIAN, JUNIOR_DOCTOR | see the board, and count as somebody watching |
| `alert.acknowledge` | PHYSICIAN, JUNIOR_DOCTOR | stop the escalation                           |

Both are marked sensitive: a critical value is an interpretation of a measurement — _this
number means somebody is in danger_ — and §4.4's blinded roles do not receive interpretations.
The measurement itself is not blinded; the officer who took it sees the number they typed.

**The officer who entered the value cannot acknowledge the alert about it.** They already know:
the alarm sounded in their hand. A clinic where the person who typed the number can close their
own alert is a clinic that can clear its board without a clinician ever seeing one.

What they _can_ do is say they have read it — which stops the alarm — and, when nobody was
watching, go and find somebody.

---

## The acknowledgement note

Required, minimum three characters, and it is not paperwork.

"Seen" is not an acknowledgement. "Giving oral glucose, rechecking in 15" is — and the next
person to open this record needs the second one, at the moment when nobody has time to write it
down twice.

Three characters rather than something longer, deliberately: a longer minimum invites "noted."
typed to clear a dialog. What makes the note useful is not its length but that somebody had to
type something at the moment they took responsibility.

Two clinicians reaching for the same alert answers **409** with the alert attached, so the
second one's screen can say who has it. That is the system working, not an error.

---

## The escalation chain

Rows in `core.escalation_step`, for the same reason the thresholds are rows: the chain in a
twelve-station clinic where everyone is within thirty metres is not the chain in a hospital, and
the first month of real use will move these numbers.

The last step names **no role**, and that is the design rather than a gap. A chain whose final
link is another notification has no end. When the consultant and the junior doctor have both had
five minutes, the remaining escalation is a person walking to another person — and in a building
whose Wi-Fi has just failed, it is the only escalation path that still works.

The sweep lives in `cmd/worker` rather than the API because it must keep running when nobody is
making requests: an alert raised at 16:58 has to escalate at 17:00 whether or not anybody is
still typing. It reads durable state rather than holding timers, so a worker restarted after an
outage catches up in its first pass, and two workers running at once are harmless — the
escalation event's id is derived from the alert and the step.

Escalations are written under `eventstore.ActorForService`, a door held to `cmd/worker` by
dthclint. See ADR-0027.

---

## Four standing invariants

Checked after every migration, and again in the test suite:

| Invariant                                 | What it stops                                                                             |
| ----------------------------------------- | ----------------------------------------------------------------------------------------- |
| `assert_critical_thresholds_can_fire`     | a threshold outside the band its code can hold — a safety net with nothing under it       |
| `assert_critical_sits_outside_normal`     | a value the screen calls ordinary and the alarm calls an emergency                        |
| `assert_the_escalation_chain_is_walkable` | a chain with no immediate first step, no terminal last step, or one that does not advance |
| `assert_critical_rules_name_live_codes`   | a rule on a retired or non-numeric code                                                   |

---

## The alarm

Five pulses, a gap, then the same again — the rhythm IEC 60601-1-8 gives a high-priority medical
alarm, which is what makes an alarm identifiable as an alarm across a room full of other noises.
It plays in silent mode, because an alarm a switch on the side of the phone can silence is an
alarm the clinic will silence on the first day and never restore. It loops, because the failure
mode is somebody in the next room. It stops **only** when the operator says they have read it —
never on a timer.

Nothing in the sound path can throw. A phone on silent, a broken speaker, an audio session
another app has taken: none of them may stop a critical value being _shown_. The sound is the
second channel, not the first.

**This is not a conformance claim.** IEC 60601-1-8 also specifies levels and spectra that depend
on the device. The plan's own risk note says the sound design has to be validated on the floor,
and that is on the manual verification list below.

---

## What is still owed

- **D-27.** The thresholds and the chain, from Dr. Nahid. Blocking for go-live, not for the
  mechanism.
- **The sound, on the floor.** Play the alarm in the waiting area at eleven in the morning.
  Inaudible fails; so does startling enough that staff turn the phone face-down.
- **The five-second promise.** Criterion 2 is a property of the socket and the phone. Enter an
  SpO₂ of 88 on a real handset and watch the consultant's screen.
- **Alert fatigue.** Explicitly out of scope until there is pilot data. The number to watch is
  how often an alert is acknowledged with a note that says nothing was wrong — which is a query
  the clinic can now run against its own record.
