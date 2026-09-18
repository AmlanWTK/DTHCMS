# ADR-0036 · A station reaches the patients on its queue, not patients that belong to it

- **Status:** **Accepted under standing delegation** — Dr Nahid delegated the open decisions to
  Amlan on 12 Sep 2026. This one is **clinical access policy** and he should read it: it decides
  which staff member can open which patient's record. Nothing here is irreversible; it is a query
  and a catalogue, both changeable without touching recorded data.
- **Date:** 2026-09-12
- **Checkpoint:** raised while retrofitting CP20's service layer, ahead of CP82
- **Deciders:** Amlan Sarkar (pending Dr Nahid's review)
- **Blueprint reference:** §3 (the twelve-station journey), §4.4 (role permissions), [R-02], [R-03]
- **Related decisions:** ADR-0021 (browser device identity) · D-46 · CP20 · CP39 (the station queue)

## Context

CP20 described authorisation in three layers — endpoint, service, serialiser — and built the
first and third. **The service layer was never written: `rbac.Authorize` had zero call sites in
the repository, production and test alike.** The endpoint guard covered for its absence by
asking the resource question itself, against a `Resource{Kind: "route"}` that structurally
carries no station and no owner. A station-scoped permission can never satisfy that, so it
always denied.

Measured consequence, before this ADR: **nine of the twelve stations could not perform a single
clinical action**, and `POST /v1/patients` — registration, the clinic's front door — was 403 for
every role in the catalogue. Only the six roles that happen to receive facility-wide scope
worked at all. Two faults hid each other: every browser write was already failing earlier on the
device check (ADR-0021), and every test that drove these routes did so as a physician.

Fixing the layering is mechanical. The question it exposes is not:

**What does "the patients this station may touch" actually mean in a clinic where a patient
walks through twelve stations in ninety minutes?**

The code assumed `resource.StationID == subject.StationID` — that a patient record carries a
station and matches. That was never going to work, and not because of a type mismatch
(`core.encounter.station_code` is `text`, `rbac.Resource.StationID` was a `uuid`, which is its
own honest problem). It fails because **the premise is wrong. A patient does not belong to a
station.** Nothing in the schema says otherwise: there is no station column on `core.patient`,
and there should not be. A patient passes _through_ stations, and that passage is already
recorded twice — `core.queue_entry` and `core.encounter`, both carrying `station_code`,
`patient_id` and `visit_id`.

## Decision

**A station role reaches a patient through the queue, not through a property of the patient.**

### 1. Reach is a relationship, evaluated against today's visit

A subject holding a station role reaches a patient when that patient has, **in the current
visit**, either a `core.queue_entry` for the subject's station, or a `core.encounter` at it.

Two different strengths, because reading and writing are not the same act:

|           | Reach                                                                                | Why                                                                                                                                                                                                                                       |
| --------- | ------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Write** | queue status `called` or `in_service`, or an **open** encounter at this station      | You may record against a patient you currently have. A patient you finished with an hour ago is not yours to amend — that is what the correction workflow (CP62) is for, and it is deliberately a different, flagged path.                |
| **Read**  | any queue entry for this station in the current visit, `done` and `skipped` included | The counsellor must be able to re-open what they just recorded, and the nutritionist must be able to check a measurement taken upstream before the patient sits down. Refusing that makes the software slower than the paper it replaces. |

Evaluated at the service layer, against the real patient, in one indexed query. Never inferred
from a route.

### 2. Registration is facility-wide, and the scope table was wrong about it

`patient.write.demographics` and `patient.consent.*` are given `ScopeAny` for `REGISTRATION`.

This is a correction, not a loosening. Registration **creates** the patient: at the moment the
permission is exercised there is no patient to scope against, so a station-scoped rule is not
strict — it is incoherent, and it is why `AuthorizeCreation` exists and is documented as
vacuous on purpose rather than dressed up as a check. The desk also legitimately amends any
patient in the facility, because correcting a mistyped name is what the desk is for. The same
applies to `RECORDS`, whose entire job is the facility's records.

The honest statement is that these permissions were never station-scoped in the blueprint; the
scope table applied one rule to everything with a `patient.` prefix and swept them in.

### 2(b). Break-glass is facility-wide, for the same reason and more urgently

`POST /v1/audit/break-glass` carried `patient.read.clinical` or `patient.read.demographics`,
whichever the caller happened to hold. Both begin with `patient.`, so both are station-scoped
for the nine station roles by §1, and the route judges no resource — so it was refused with
`scope_not_enforced` for the nutritionist, the exercise specialist, the history officer, the
counsellor, the anthropometry officer, the clinical assistant, the pharmacist and the
prescription educator.

This is §2's incoherence again and it bites harder. A permission exercised _before or outside_
a station relationship cannot be narrowed by one: at registration there is no patient yet to
scope against; at break-glass the whole act is **asking to be excused from the relationship**.
A scope check here would have to ask the very question the caller is asking permission to
stop asking. And under the Consequences below, §1 has just made this the only way past a reach
refusal — so a station-scoped break-glass is not merely strict, it is a fire escape locked from
the inside, and there is then no way past at all.

There was a third fault, quieter and worse. `anyOf` takes the _weaker_ permission, and
`patient.read.demographics` is facility-wide for the registration desk (§2) and for the
reviewing roles. So the registration clerk and the patient relations officer could open the
emergency door, and the nutritionist standing in front of the patient could not. A door whose
width is decided by whichever read permission the caller happens to be carrying is not a door
with a policy.

**Break-glass becomes its own permission, `emergency.break_glass`, and it reaches the
facility.** The name carries no `patient.` prefix deliberately: `isClinical` decides reach by
prefix, so `patient.break_glass` would re-acquire station scope in a later tidy-up with every
test still green. Invariant 129 holds that from the database side, as invariant 128 does for
`reference.read`.

Its safety does not come from scope, and it never did. It comes from four things, each
checkable where it is written down: it is **its own sensitive permission**, held by the nine
roles that stand in front of a patient and by no administrative desk — not registration, not
the pharmacist (§4.4 blinds both, and `assert_rbac_constraints` refuses them the grant), not
Records (already facility-wide by §2), not the administrator, who _acknowledges_ the alarm and
should not be able to raise it; it requires a **step-up** with its own purpose, consumed and
spent per door; it is **time-boxed**, four hours by default and twenty-four at most, with the
ceiling as a database CHECK rather than a constant; and it is **loudly audited** — the access is
closed again if the audit row cannot be chained, and a high-severity alert stands on every
administrator's console until one of them acknowledges it.

Before this change there was no break-glass permission at all. That is worth saying plainly,
because "make break-glass facility-wide" sounds like a loosening and is not one: the door is
narrower after this than before it, by six roles.

### 3. `Resource` identifies a station the way the rest of the schema does

`Resource.StationID uuid` becomes `Resource.StationCode string`. `core.role.station_code`,
`core.encounter.station_code` and `core.queue_entry.station_code` are all `text`; the `uuid`
existed nowhere else and was the reason nothing could be plumbed into it.

### 4. The station on the subject comes from the session, not from a header

The active role names its station (`core.role.station_code`). `Resolver.Subject` is passed that
station instead of `nil`. A client cannot assert its own station, exactly as it cannot assert
its own role — [R-02] and the `X-Active-Role` rule already settled this argument.

### 5. Fail closed, and be loud about it

A route whose handler does not judge the resource stays refused, with a reason that says
`scope_not_enforced` rather than the old lie `out_of_scope`. Three independent mechanisms hold
it: a runtime scope debt that stops an unjudged handler emitting 2xx, a `dthclint` check that
fails the build, and the gate itself. Routes open one at a time, each by a deliberate act.

## Alternatives considered

**Give every clinical role facility-wide reach.** One line, and the clinic works tomorrow.
Rejected: it makes every staff member able to open every patient's record, which is the outcome
§4.4 exists to prevent, and it would be discovered by an audit rather than by us.

**Keep `resource.StationID == subject.StationID`, and put a station on the patient.** Rejected:
it encodes a falsehood. A patient is not the property of the last desk they sat at, and the
column would be wrong from the moment they stood up.

**Scope by encounter only, dropping the queue.** Cleaner, and it breaks the clinic: the
anthropometry officer must open the record to call the patient in, which happens _before_ an
encounter is opened. Rejected for the same reason the write rule is narrower than the read rule
— these are two genuinely different moments.

**Scope by visit rather than by station** ("anyone working today may touch today's patients").
Tempting, and it is roughly how the paper clinic behaves. Rejected because it makes the station
column decorative and gives the exercise specialist the same reach as the consultant.

## Consequences

**Good**

- Nine stations can do their jobs, and the rule matches how the clinic actually runs.
- Reach is computed from records that already exist and are already maintained by CP39; no new
  bookkeeping and nothing to keep in sync.
- "Why could this person open that record" is answerable after the fact, because the queue entry
  that granted the reach is still there.

**Bad — and accepted knowingly**

- Authorisation now costs a query per request on scoped routes. Indexed on
  `(facility_id, visit_id, station_code)`, but it is not free, and CP93's load testing must
  measure it rather than assume.
- A patient with no queue entry is reachable by nobody but the facility-wide roles. That is
  correct and it will feel wrong the first time somebody hits it — most likely on a patient
  registered but not yet routed. The error message must say which, or the desk will think the
  system is broken.
- Break-glass (CP22) becomes load-bearing rather than ornamental, since it is now the only way
  past a reach refusal. Its audit trail needs to be watched, not merely written. It was itself
  station-scoped when this was written, which made the sentence above false for eight roles;
  §2(b) is the correction.

**Revisit when**

- The clinic runs more than one site, where "today's visit" stops being unambiguous.
- A station legitimately needs a patient it never queued — the likeliest is Records pulling a
  historical file, which §2 already handles by making Records facility-wide.
- CP93 measures the per-request cost and finds it material.

## What is needed to accept this

Dr Nahid's answer to one question: **should a station officer be able to open the record of a
patient who is not on their queue today?** This ADR says no, with break-glass as the exception.
If the real clinic works the other way — and a small clinic where everyone knows everyone may
well — then §1 becomes visit-scoped rather than station-scoped, which is a change to one query.
