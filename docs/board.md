# The Clinic Traffic Control board

CP40. Blueprint §5.2.

One screen, on a wall, showing where every patient in the building is. It is what makes the
parallel twelve-station model visible: without it, "the patient is somewhere between
anthropometry and the physician" is a thing staff say to each other twenty times a morning.

---

## The screen is public

Patients sit in front of it for forty minutes. Everything below follows from that.

### The board does not query `core.visit`

It queries `core.board_row`, a view whose column list is an **allowlist** and whose growth is
refused by `core.assert_the_board_shows_nothing_clinical()`. A board that _could_ show a
diagnosis will one day show one — not because anyone decides to, but because somebody adds a
column to a query six months from now and the reviewer is tired.

Notably absent, each for its own reason:

| Field                                                    | Why not                                                                               |
| -------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `visit.diagnoses`, `visit.plan`, `visit.chief_complaint` | Clinical, obviously                                                                   |
| `queue_entry.priority_reason`                            | "Why is that person first" is a diagnosis read aloud — "critical glucose, seen first" |
| `patient.name_en`, `patient.name_bn`                     | A name is the identifier that makes every other field identifiable                    |

The allowlist is written out longhand. A rule expressed as "no column whose name contains
`diagnos`" would pass a column called `impression`, and the dangerous change is the one
nobody thought of.

### The payload carries no patient id

Stronger than the criterion asks, and deliberate. A patient id is a **join key**: anyone who
can read the board's JSON — the display's own account, a browser extension, a screenshot in a
group chat — could correlate a row on the wall with any other record keyed by that id.

The only handle a board row offers is a queue entry, and turning one into a patient requires
a second call under a permission the wall display does not hold.

### The identification convention is a decision, with a safe default

`core.board_setting.identify_by`:

| Value                | Shows                                    | For                                                                              |
| -------------------- | ---------------------------------------- | -------------------------------------------------------------------------------- |
| `code` **(default)** | `V-2026-0914-017`                        | A screen patients can read. Meaningless without the card the patient is holding. |
| `code_initials`      | `V-2026-0914-017 · K.M.N.`               | A screen only staff pass                                                         |
| `code_clinical`      | `V-2026-0914-017 · DTHC-FRD-2026-000137` | A staff-only screen                                                              |

Resolved **on the server**. The fields a convention excludes are never serialised, because a
redaction the client performs is a redaction that already travelled.

**Dr. Nahid owns this decision** — it depends on where the screen physically hangs. The
default is the cautious end because the other end has to be chosen deliberately, in a room,
looking at the wall.

---

## Permissions

Two, because they are two different jobs.

- **`board.read`** is the wall display's own permission, not `visit.read`. The machine bolted
  to the wall needs an account, and that account should be able to do exactly one thing.
  Reusing `visit.read` would give it the ability to pull any patient's visit history.
- **`visit.reroute`** is a floor supervisor's. Rerouting is deciding somebody else's queue is
  wrong, and an anthropometry officer who can push their own queue onto the next station is
  an anthropometry officer having a bad morning.

`board.read` also gates the realtime `queue:{facility}` subscription, so the display can hold
that one permission and nothing else. The `station:` topic deliberately did not move with it:
a station feed carries what was _recorded_ at that station, which is clinical.

---

## Bottlenecks

Two thresholds, both per facility and both **data** (`core.board_setting`), because a clinic
tunes them in the first week and a threshold in Go needs a deployment to change.

| Level           | Proposed                  | Trips on              |
| --------------- | ------------------------- | --------------------- |
| Busy (amber)    | 15 min wait **or** 4 deep | whichever comes first |
| Backed up (red) | 30 min wait **or** 7 deep | whichever comes first |

**Proposed values requiring approval.** The `OR` is deliberate in both directions: nine
people who all arrived in the last minute have no long wait yet and are already the problem;
one person sitting for forty minutes at an empty station means something has gone wrong with
that one person, and that is exactly what a supervisor should walk over and look at.

### Colour is never the only signal

Every station carries the level as a **word**, a **left rail**, and a surface colour. Red
alone fails for roughly one in twelve men who will work in this clinic, and it fails
completely on a projector whose lamp has been on for three years — which is the actual
failure mode of a screen that has hung on a wall.

---

## Suggested reroutes

The board proposes; a person applies. A board that moved patients on its own would be a board
nobody could explain to a patient asking why they were sent somewhere else.

A patient is suggested only when all of this holds:

- they are **waiting**, not called and not in service — moving somebody an operator has
  already stood up to fetch is how two people go looking for one patient;
- the station they are at is a bottleneck;
- the destination is calm, on their own visit type's planned journey, and **after** where
  they are now — a suggestion to go backwards is a suggestion to repeat a station;
- they are not already queued at the destination.

Longest wait first, because that is who a supervisor would move.

The board sends the **facts** — `from`, `to`, `from_waiting` — and the screen composes the
sentence in the language it is being read in. An earlier draft sent
`"STN_EXAMINATION has 8 waiting; STN_NUTRITION is free"` ready-composed, which put an English
sentence full of raw station codes in front of a supervisor reading Bangla. A suggestion
nobody can evaluate in one glance is one they will either ignore or obey blindly.

---

## Rerouting is atomic

`core.reroute_queue_entry` closes one queue entry and opens another in **one statement**.
Half a reroute is a patient who has left anthropometry and is standing in no queue at all —
invisible to the board, which is precisely the failure the board exists to prevent.

Both ledger entries' ids are derived from the request's `event_id`, so a tablet that lost the
reply and pressed again writes the same two ids and the ledger's primary key absorbs the
retry. A second supervisor acting on a stale board gets a `409` saying the patient has moved
on — not that the entry does not exist, which would read as a bug in the board.

---

## Two audiences, one component

The wall display and the supervisor's phone are the **same board at two sizes**, not two
boards. `?display=wall` scales the type up, covers the viewport and drops every control;
without it the page carries the reroute controls.

Building them as one component is what stops the wall and the phone disagreeing about who is
where, which is the single thing a traffic board must never do.

The wall variant is a takeover rather than a page inside the shell: the screen in the waiting
area has no navigation to use, nobody signs out of it, and every pixel spent on a sidebar is a
pixel not spent on the thing people are reading from five metres away.

---

## Freshness

A **socket and a poll**. The socket — `queue:{facility}`, published after every queue
transition — is what makes the two-second criterion true. The fifteen-second poll is what
makes the board correct after a night when the socket died and nobody was watching: a wall
display is the one screen in the building nobody reloads.

A failed publish never fails a write. The socket is a nicety, the pull is the truth (CP26).

---

## Manual verification still owed

- Run the board on the clinic's actual display, at the distance staff will read it from.
- Walk patients through stations and confirm the columns move.
- **Sit in a patient's chair and read the screen.** Confirm nothing on it identifies anyone
  to a stranger.
- Confirm the bottleneck thresholds against a real morning, and change them — they are data.
