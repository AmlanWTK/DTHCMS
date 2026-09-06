# Commits for CP35–CP39

Five commits, one per checkpoint, in this order. Run from the repository root.

---

## CP35 — Patient edit & demographic correction

```bash
git add backend/migrations/00020_patient_corrections.sql \
        backend/internal/patient/correction.go \
        backend/internal/patient/correction_http.go \
        backend/internal/patient/correction_db_test.go \
        backend/internal/patient/registration_db_test.go \
        backend/internal/auth/secondfactor.go \
        web/src/features/patients/ web/src/app/'(clinical)'/patients/'[id]'/edit/ \
        web/test/correction.test.tsx web/e2e/corrections.spec.ts \
        web/src/features/auth/api/secondFactor.ts \
        web/src/styles/globals.css web/messages/ \
        api/openapi.yaml packages/api-client/src/schema.ts \
        docs/patients.md docs/progress.md

git commit -m "CP35: correct demographics through the event path, never by overwrite

A correction is an event carrying what the value was, what it is now and why;
the original stays in the ledger. Every request field is a pointer and nil means
not touching this, so a form that renders six fields cannot rewrite five of them
from a stale tab.

A correction that changes nothing is refused rather than recorded as a no-op --
including a telephone number retyped in a different format, which normalises to
no change. So is one with no usable reason. Both rules also hold in the database
(assert_corrections_are_explained), so an unexplained correction cannot exist
however it was written.

The date of birth, its precision, the sex and the English name are high-impact
and demand a step-up (patient_correct_identity). The plan asks for elevated
permission; the catalogue has no code for it, and the real risk is a session left
open on a desk rather than a person who should never have had the capability.
Because whether one is needed depends on what changed, the check sits in the
handler and not on the route.

ops.derived_dependency is the plan's risk note made explicit: a register of which
derived values depend on which fields, so the read model, the phonetic search key
and the anonymised cohort row move in one transaction and the next derived value
somebody adds is declared rather than discovered by a patient whose percentile
never updated.

The screen is a diff, not a form. The date of birth is CP32's three-field control
rather than a date input, because a native picker renders 06/14/1985 or
14/06/1985 depending on the browser's locale and this is the field a correction
most often exists to fix [R-06]. The authenticator code is asked for before
submitting, not after a 403, because on a tablet the second path loses the typing."
```

---

## CP36 — Consent capture & enforcement

```bash
git add backend/migrations/00021_consent.sql \
        backend/internal/consent/ \
        backend/internal/eventstore/registry.go backend/internal/eventstore/registry_test.go \
        backend/internal/projection/patient.go \
        backend/internal/patient/http.go \
        backend/cmd/api/main.go backend/cmd/api/contract_test.go \
        backend/architecture.json backend/sqlc.yaml \
        backend/internal/platform/dbgen/ \
        backend/internal/patient/store_db_test.go \
        web/src/features/consent/ web/src/app/'(clinical)'/patients/'[id]'/consent/ \
        web/test/consent.test.tsx web/e2e/consent.spec.ts web/test/styles.test.ts \
        api/openapi.yaml packages/api-client/src/schema.ts \
        docs/adr/0022-layered-versioned-consent-enforced-by-privilege.md \
        docs/consent.md docs/patients.md docs/progress.md

git commit -m "CP36: layered, versioned consent, enforced by privilege rather than by remembering

The engine, not the wording. D-02 is a legal decision with counsel; what is built
is everything that has to be true whatever the words turn out to be, arranged so
that loading them later is an INSERT.

Layered: five consents, each independently grantable and revocable. absent is
deliberately not revoked -- never asked is work the desk has not done, asked and
withdrawn is a decision the patient made, and an interface that conflates them
produces staff who re-ask people who said no.

Versioned: the template holds the full text, becomes immutable by trigger once
active, is never deleted, and the event carries the version, the language and the
SHA-256 of the body -- so a template row altered later by somebody with database
access is detectable from the ledger. The version is the server's; a client that
could name it could record a consent to words the patient never saw.

Enforced at the point of use, not merely recorded (15.1). Research is enforced by
privilege: dthcms_research loses SELECT on research_subject entirely and reads a
view filtered on live consent, so a researcher cannot query somebody who said no
even by writing the query themselves. Everything else goes through a gate whose
Sender interface is declared in the consent package, so the un-gated sender is
not reachable from a call site and there is no remember-to-check step to forget.

The one-minute revocation budget is met by construction: the row that says do not
send is written by the same COMMIT as the event saying so. The gate fails closed.

Two rules privilege cannot enforce are written down instead: a mart is built from
research.cohort, and a new outbound purpose gets an entry in the mapping.

Research is now opt-in, so a freshly registered patient is in the register and
not in the cohort. TestResearchCannotReachAnythingIdentified asserts the new truth."
```

---

## CP37 — Patient timeline read model v1

```bash
git add backend/migrations/00022_patient_timeline.sql \
        backend/internal/projection/timeline.go backend/internal/projection/projection.go \
        backend/internal/patient/timeline.go backend/internal/patient/timeline_http.go \
        backend/internal/patient/timeline_db_test.go \
        backend/internal/patient/queries/timeline.sql \
        backend/internal/patient/http.go \
        backend/cmd/api/contract_test.go \
        backend/internal/platform/dbgen/ \
        api/openapi.yaml packages/api-client/src/schema.ts \
        docs/timeline.md docs/progress.md

git commit -m "CP37: one chronological read model, attributed per row

Built once so that the physician dashboard, the timeline visualisation, the AI
synthesis and the records chronology do not each write their own query over the
ledger -- four queries is four places for a fact to be missing from one of them,
and the one it is missing from is always the one somebody is looking at.

One row shape, with category closed and kind open: a new observation type must
not need a migration, and a new category should be a decision.

Attribution on every row, denormalised. 8's hover-to-see-who has to work
everywhere, and attribution resolved by a join is attribution that disappears
when the person who recorded it has left; an invariant refuses a row with no
actor or no label. The employee code is resolved in the derivation rather than
carried in the event: the ledger holds the user id, the code is a rendering of
it, and a rebuild should look it up.

Exactly once is UNIQUE (event_id, item) rather than care -- a correction of three
fields is three lines, a re-delivered event updates them rather than adding more,
and a rebuild is compared field by field against the original rather than by
counting rows.

The permission filter is in SQL: a post-filter is how a total comes back larger
than the rows returned, and how paging skips what it hid.

Measured: p50 1.2 ms, p95 2.1 ms against a 300 ms budget, over 481,601 rows
across 301 patients, with ANALYZE run first."
```

---

## CP38 — Visit & encounter lifecycle

```bash
git add backend/migrations/00023_visits.sql \
        backend/internal/visit/visit.go backend/internal/visit/store.go \
        backend/internal/visit/service.go backend/internal/visit/http.go \
        backend/internal/visit/visit_db_test.go backend/internal/visit/acceptance_db_test.go \
        backend/internal/visit/queries/visit.sql \
        backend/internal/eventstore/registry.go backend/internal/eventstore/registry_test.go \
        backend/internal/auth/catalogue.go backend/internal/rbac/catalogue.go \
        backend/cmd/api/main.go backend/cmd/api/contract_test.go \
        backend/architecture.json backend/sqlc.yaml \
        backend/internal/platform/dbgen/ \
        api/openapi.yaml packages/api-client/src/schema.ts \
        docs/adr/0023-visits-and-encounters.md docs/visits.md \
        docs/access-matrix.md docs/progress.md

git commit -m "CP38: the visit state machine lives in the database, and a station touch is a row

A trigger allows open->closed, open->abandoned, closed->open, abandoned->open and
nothing else, because there will be a projector, a repair script and later modules
writing these rows, and a rule enforced in one handler is a rule the other three
do not have. In Go the machine is data rather than a switch, so the test
enumerates every pair including the six that must fail.

An encounter per station touch, with a start, an end, a person and a role. Cheap
now, and the only moment the timing is recordable at all: a touch not written
down at 10:14 cannot be reconstructed in March, and 14.2's analytics would start
from the day somebody thought of it. bounced is its own outcome because a bounce
recorded as completed makes rework invisible, and rework is the one number a
quality gate exists to produce.

Two races are partial unique indexes, not checks -- one open visit per patient,
one open encounter per station -- proved with twelve concurrent attempts each;
the handler's check exists only for the message.

abandoned is deliberately not closed: 14.2 must not count a journey nobody
completed as a completed one. A closed visit cannot be edited in place, and
reopening is recorded, counted and needs a reason.

Four new permissions by migration, as CP21 added one: reusing
patient.write.demographics would mean a physician closing a visit needs the
permission to rewrite a name. Reopening needs visit.close, not visit.open,
because it undoes a close.

waiting_seconds -- the visit minus time at stations -- is computed on read: it is
what a patient experiences, and twenty minutes of care inside two hours is the
complaint.

Still open: the real clinic flow may not match this machine. Walking a real
morning through it is the manual verification."
```

---

## CP39 — Station queue & assignment

```bash
git add backend/migrations/00024_station_queue.sql \
        backend/internal/visit/queue.go backend/internal/visit/queue_http.go \
        backend/internal/visit/queue_db_test.go \
        backend/internal/visit/queries/queue.sql backend/internal/visit/http.go \
        backend/internal/visit/visit_db_test.go \
        backend/internal/eventstore/registry.go backend/internal/eventstore/registry_test.go \
        backend/internal/auth/http.go \
        backend/cmd/api/main.go backend/cmd/api/contract_test.go \
        backend/internal/platform/dbgen/ \
        mobile/src/features/queue/ mobile/src/app/'(queue)'/queue.tsx \
        mobile/src/messages/ mobile/test/queue.test.ts \
        api/openapi.yaml packages/api-client/src/schema.ts \
        docs/queue.md docs/progress.md

git commit -m "CP39: the claim is FOR UPDATE SKIP LOCKED, not a check in a handler

No patient is ever assigned to two operators at the same station. Two operators
pressing call-next in the same second is the ordinary case, not the edge case: a
station with two chairs does it all morning.

core.call_next_at_station selects one waiting row under FOR UPDATE SKIP LOCKED.
The first caller locks the head of the queue and takes it; the second does not
block behind it and does not get the same row -- it skips to the next patient,
which is what the second operator wanted. Proved with ten patients and twelve
concurrent operators: every operator who gets somebody gets a different somebody,
and the two who miss out get 204 rather than an error, because an operator who is
free and finds an empty queue has not made a mistake.

Priority always says why: 4.4's critical findings jump the queue and so does a
physician's judgement, and neither may be anonymous.

Waiting is measured from when the patient joined this queue and stops when the
waiting stops. The board reports the longest wait as well as the average, because
an average of 27 minutes hides the person who has been sitting there since nine.

A reroute says where and why, refused three ways when it does not.

The station sequence per visit type is a table, not code -- the sequences are an
operational decision, and one in Go needs a deployment to change.

Also caught: a plpgsql function returning a bare composite returns a row of NULLs
when it has nothing, which the driver cannot scan -- an empty queue would have
been a 500. SETOF returns no rows, which is what an empty queue is.

On the phone, my station's queue: one action at the top that names the person it
will call, the wait beside each name colouring only once it is long, and the
priority reason on the chip. The station comes from the operator's role rather
than a picker -- /v1/auth/me gained station per grant. Screen delivered as a
flagged mock-up; Expo cannot run in the build environment."
```

---

## Then

```bash
git push
```

## Gate state at the end of CP39

| Gate                             | Result                                    |
| -------------------------------- | ----------------------------------------- |
| Go suite (real Postgres + Redis) | pass, exit 0                              |
| golangci-lint                    | 0 issues                                  |
| `sqlc diff`                      | clean                                     |
| `dthclint all`                   | no violations                             |
| redocly lint                     | valid                                     |
| prettier / eslint / typecheck    | clean                                     |
| vitest                           | 25 + 151 + 89 + 183 + 121 + 325 = **894** |
| Playwright                       | **93**                                    |
| Timeline at 481,601 rows         | p95 **2.1 ms** (budget 300 ms)            |
