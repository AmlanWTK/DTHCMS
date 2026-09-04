# ADR-0023 · The state machine lives in the database, and a station touch is a row

- **Status:** Accepted
- **Date:** 2026-09-03
- **Checkpoint:** CP38
- **Deciders:** Dr. K. M. Nahid Ul Haque, Amlan Sarkar
- **Blueprint reference:** §3, §11.1, §14.2, §4.3
- **Related decisions:** Visit reopening policy (**operational confirmation, open**)
- **Supersedes:** —

## Context

A visit is the spine of the clinic. §11.1 wants "which patient came when, with what problem"
answerable in one query forever; §14.2 wants throughput and bottleneck analytics; every station
screen wants to know which visit it is looking at.

Two things could have been much cheaper now and much worse later.

**A visit could have been a status column somebody sets.** Four states and a handler that
writes them is a day's work. It is also how a visit ends up `closed` and then `abandoned`, how
a closed visit's diagnosis quietly changes, and how the first person to write a repair script
produces a row nothing in the application would have allowed.

**Encounters could have been skipped.** Nothing in Phase 1 needs them. But timing is only
recordable _while it is happening_: a station touch not written down at 10:14 cannot be
reconstructed in March, and §14.2's analytics would then start from the day somebody thought
of it rather than from the day the clinic opened.

## Decision

**The state machine is in the database as well as in Go.** A trigger on `core.visit` allows
`open→closed`, `open→abandoned`, `closed→open` and `abandoned→open`, and nothing else. In Go
the same machine is _data_ — `legalTransitions` — rather than a switch, so the test enumerates
every pair including the ones nobody would think to try.

The duplication is deliberate. There will be a projector, a repair script and later modules
writing these rows, and a rule enforced in one handler is a rule the other three do not have.
The Go copy exists so a refusal reaches the operator as a sentence rather than as a constraint
name.

**An encounter per station touch, with a start, an end and a person.** Cheap now; it is what
makes "the counselling station is where mornings go" a fact somebody can act on rather than an
impression somebody argues about.

`bounced` is its own outcome rather than a completed touch with a note, because §14.2 counts
rework and a bounce recorded as "completed" makes rework invisible — which is the one number a
quality gate exists to produce. A patient returning to a station is a **new encounter**, for
the same reason.

**Two concurrency rules are partial unique indexes, not checks.** One open visit per patient;
one open encounter per station per visit. Both are races two tablets lose in the same
millisecond, and no amount of care in a handler wins them. The handler's check exists only to
produce a useful message; the index is what holds. Both are tested with twelve goroutines.

**`abandoned` is not `closed`.** §14.2 counts throughput, and a visit nobody completed must not
be counted as a completed journey — the number that results is the one somebody puts in a
report.

**A closed visit cannot be edited in place.** The trigger refuses it. §4.3's correction
principle applies to a visit as much as to a value, and reopening — which is recorded, counted
and requires a reason — is the path.

**Four new permissions**, added by migration as CP21 added one: `visit.open`, `visit.close`,
`visit.read`, `visit.attend`. Reusing `patient.write.demographics` would mean a physician
closing a visit needs the permission to rewrite a name, which is the over-grant §4.4 exists to
stop. Reopening needs `visit.close`, not `visit.open`: it undoes a close, and the authority
that should hold it is the one that made the decision.

**The retry check comes before the "already open" check.** Order matters and getting it wrong
is subtle: a tablet that opened a visit, lost the reply and pressed again would otherwise be
told the patient already has one — which is true, is the visit it just opened, and reads at the
desk as somebody else's mistake.

**The clinic day is its own column**, in Asia/Dhaka. A visit opened at 23:50 and closed at
00:10 belongs to one day, and the queue board asks for it by that day all night.

## Consequences

`waiting_seconds` — the visit's duration minus time at stations — is computed on read rather
than stored, because both change while the visit is open and a stored total is a total that is
wrong between updates. It is the number a clinic acts on: twenty minutes of care inside two
hours in the building is the complaint.

The visit code is gapless per facility per clinic day, under a row lock like the clinical id.
It is spoken at a desk, so a gap reads as a lost patient.

**Still open:** the _policy_ for when a visit may be reopened is an operational confirmation
Dr. Nahid owes. The mechanism is built and every reopening is recorded with a reason and
counted on the record, so a visit reopened three times is visible to whoever looks; the rule
that says "only the physician who closed it, and only the same day" is a check that can be
added without changing anything else.

The plan's own risk note stands: **the real clinic flow may not match this state machine.**
Four states and a linear station journey is what §3 describes; a morning at DTHC may not be.
Validating it against a real day's patients before the pilot is the manual verification this
checkpoint asks for and cannot do for itself.
