# `maestro/offline/` — §13.10 on the clinic's hardware (CP68)

**These flows cannot run yet.** They wait on **D-59**, the same blocker as `../smoke.yaml`: the
clinic's device model and Android floor. That is not a scheduling excuse. The four things this
directory exists to check are the four the container cannot reach — SQLCipher on a real
filesystem, a real radio, the operating system's own clock, and a tablet that can genuinely run
out of space — and every one of them is a property of a device rather than of a driver.

They are written now so that the day the device arrives, verification is an afternoon rather than
a project.

```bash
maestro test mobile/maestro/offline/                      # once a device is connected
maestro test mobile/maestro/offline/airplane-mid-entry.yaml
```

`setAirplaneMode` needs **Maestro 1.36 or later** and an Android device: it drives the operating
system rather than the app. On iOS it is unavailable, which is one more reason the clinic's tablets
being Android is a decision worth having in writing (D-59).

---

## What runs where

Every §13.10 scenario runs in CI on every push, against the in-memory store and `FakeClinic`
(`mobile/test/offline-matrix.test.ts`). This column says what a **device** adds, which is never
"the same thing again": the CI half proves the engine's decisions, and the device half proves the
platform underneath them behaves as the CI half assumes.

| §13.10 scenario                 | Runs in CI | Flow here                       | What only a tablet answers                                                                  |
| ------------------------------- | ---------- | ------------------------------- | ------------------------------------------------------------------------------------------- |
| Airplane mode mid-entry         | ✅         | `airplane-mid-entry.yaml`       | NetInfo reporting the radio, the pill repainting, SQLCipher committing with no signal       |
| App killed with a full queue    | ✅         | `killed-with-a-full-queue.yaml` | The database file surviving the process, and the Keystore releasing the key on a cold start |
| 200 queued events               | ✅         | —                               | Nothing about correctness. How long it takes and what it costs the battery is **CP93**      |
| One rejection in fifty          | ✅         | —                               | Whether the sentence on the screen is one a clinical assistant can act on, in Bangla        |
| Device clock three hours wrong  | ✅         | — (see the checklist below)     | The OS clock. No Maestro command sets it; `adb shell` does                                  |
| Token expires while offline     | ✅         | — (see the checklist below)     | A real token expiring, and the refreshing fetch below the engine                            |
| Device revoked while offline    | ✅         | — (see the checklist below)     | An administrator revoking a real enrolment while a real tablet is out of range              |
| Duplicate submission of a batch | ✅         | —                               | Nothing. The idempotency is the server's and is tested against a real PostgreSQL            |
| Flaky network                   | ✅         | `flaky-network.yaml`            | Real TCP, real timeouts, a real radio. Shaping is applied outside the flow — see below      |
| Storage full                    | ✅         | — (see the checklist below)     | SQLCipher raising `SQLITE_FULL` rather than this repository's driver raising its own        |

`sync-status-is-never-a-lie.yaml` is not one of the ten. It checks §13.9 — the indicator saying
the work is with the clinic only when it is — in both scripts, because that sentence reaching an
operator's eye is not something a function's return value can prove.

## The four that need a person, not a flow

These are the scenarios where the interesting step is something Maestro cannot do. Each is a few
minutes with a tablet and is worth doing **once per device model**, not once per release.

**Clock three hours wrong.** Turn off automatic time, then:

```bash
adb shell su 0 date -s "$(date -u -d '+3 hours' +%m%d%H%M%Y.%S)"   # rooted or emulator
# then run: maestro test mobile/maestro/offline/airplane-mid-entry.yaml
```

Expect: entries are recorded with the tablet's own (wrong) time, the sync screen says the clock is
ahead and that the clinic will hold what is recorded, and the entries appear in the clinic's
quarantine with the times the tablet gave them. **The tablet must not quietly correct them.** Then
fix the clock and confirm the next entries are accepted.

**Token expires while offline.** Sign in, put the tablet in airplane mode, record several
measurements, and wait out the refresh token's lifetime (or have an administrator revoke the
session). Reconnect. Expect: either it refreshes silently and the queue goes, or the screen asks
for a sign-in and **the queue is untouched** — never an empty queue and a quiet pill.

**Device revoked while offline.** Record a morning's work in airplane mode. Have an administrator
revoke the enrolment in the admin console. Reconnect. Expect: every entry is handed to the clinic's
quarantine, none is accepted into the record, the screen says the clinic no longer accepts this
tablet and names how many entries have not reached the record — and says **do not wipe it**.

**Storage full.** Fill the tablet (`adb shell fallocate -l <n>G /sdcard/filler` on a device that
allows it, or copy video until the OS complains). Record a measurement. Expect: the entry is
refused with a sentence about space, **nothing is half-written**, and after freeing space the same
measurement can be recorded and delivered.

## Shaping the network for `flaky-network.yaml`

The flow does not shape the network — Maestro drives the app, not the access point, and a flow that
claimed otherwise would pass on a perfect connection while reporting that a bad one had been
tested. Apply one of these first:

```bash
# An emulator: Android's own throttle
adb -s emulator-5554 emu network speed edge
adb -s emulator-5554 emu network delay gprs

# A real tablet on a real access point, from a Linux router or a laptop sharing the connection
sudo tc qdisc add dev wlan0 root netem loss 10% delay 3000ms
sudo tc qdisc del dev wlan0 root netem        # afterwards
```

Ten per cent loss and three seconds of latency are §13.10's own numbers.

## When one of these fails

**Quarantine it, do not delete it, and do not re-run until it goes green.** A device flow that is
re-run until it passes is a flow that has been switched off without anybody saying so, and CP68's
named risk is exactly that: flaky device tests eroding trust in CI. Move the file to
`maestro/offline/quarantined/` with a comment at the top saying what was observed and on which
device, and open the question with the same weight as a failing unit test — because on this
subsystem, an intermittent failure is a report of intermittent data loss until somebody proves
otherwise.
