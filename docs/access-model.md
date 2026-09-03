# The access model

What decides whether a person may do a thing in DTHCMS, where that decision is made, and
how it is proven. The specification of record is the code: `backend/internal/rbac`, and the
decision matrix test that walks every role against every permission. This page is the
reading guide.

Companion: [`access-matrix.md`](access-matrix.md), generated from the engine — every cell.

## 1. One function

```
rbac.Can(subject, action, resource) → Decision
```

There is one place a question of the form "may this person do this to that" is answered
(CP19). Endpoints, services and serialisers ask it (CP20); none of them contains a
permission check of its own. Scattered checks are how clinical systems leak: each one is
written under a different assumption, and the one that was wrong is found by an audit.

The answer is a **Decision**: allowed or not, a **reason** from a closed list, the **rule**
that decided when one did, and a sentence a person can read. `Decision.Explain(action)`
renders it for a log line or an audit screen, and it never contains a name or a patient —
roles, actions and rules only.

## 2. Deny by default, and the order of the checks

Everything is refused unless a rule allows it, and the checks run in the order a person
would want them explained:

1. **Is that a thing?** An action not in the catalogue — a typo, an old name, `*` — is
   `unknown_action`. Nothing is ever granted by accident of spelling.
2. **Is there a rule that says no regardless?** The blueprint's constraints (§3 below) are
   explicit denies, checked before anything else and beating any allow. A future
   migration that hands the pharmacist `diagnosis.read` changes nothing here.
3. **Is the permission held?** By the active role when one is chosen; by any live role
   otherwise.
4. **Is it in this facility?** A resource from another facility is refused as if it did
   not exist.
5. **Is it within reach?** The role's scope (§4).

## 3. The blueprint's rules, as rules

Blueprint §4.4 states three constraints in prose. CP15 made them database invariants over
the catalogue — a migration that grants the wrong permission fails. CP19 makes them
decisions, so that they hold for a resource as well as for a permission:

| Rule                               | What it refuses                                                          |
| ---------------------------------- | ------------------------------------------------------------------------ |
| `nutritionist_no_prescriptions`    | NUTRITIONIST, any `prescription.*`                                       |
| `pharmacist_no_diagnoses`          | PHARMACIST, any `diagnosis.*` and any sensitive permission               |
| `registration_blinded`             | REGISTRATION, any sensitive permission                                   |
| `blinded_role_sensitive_resource`  | REGISTRATION or PHARMACIST reading any resource that carries a diagnosis |
| `field_worker_no_facility_records` | FIELD_WORKER, any `records.*` or `diagnosis.*` — outreach captures only  |

"Sensitive" is the list CP15 marked in the catalogue: `patient.read.clinical`,
`records.read`, `diagnosis.read`, `diagnosis.write`, `ai.synthesis.read`.

The fourth rule is the one the others need. A pharmacist holds `prescription.read`; a
prescription is a resource; a prescription that still carries its diagnosis is a resource
the pharmacist may not see. The engine refuses it as `blinded_resource`, and CP20's
serialiser is what makes sure a pharmacist never receives one — the field is removed, not
hidden, and a golden test on the raw JSON proves it.

## 4. Roles, hats and reach

**A person may hold several roles at once** [R-02]: an assistant covering anthropometry
and vitals, a physician covering a station on a short day. The engine sees both the
roles held and the **active role** — the hat being worn now, which the web's role switcher
and the station app's current station set, and which every event stamps at write time.

- Wearing a hat, the decision is made for that hat alone: its permissions, its rules,
  its reach. A physician wearing the anthropometry hat cannot read a diagnosis from it.
- Wearing none, every permission of every role is held — and every rule of every role
  binds. A person holding PHYSICIAN and NUTRITIONIST with no hat chosen cannot read a
  prescription until they choose the physician's. Choose a hat.

**Reach** is how far a held permission extends:

| Scope     | Meaning                                         | Who                                                  |
| --------- | ----------------------------------------------- | ---------------------------------------------------- |
| `any`     | every resource in the facility                  | PHYSICIAN, JUNIOR_DOCTOR, QA, ADMIN, CRM, RESEARCHER |
| `station` | resources at the station being worked right now | every station role, for clinical actions             |
| `own`     | resources the person created                    | FIELD_WORKER                                         |

Administrative actions — users, roles, devices, formulary, reports — have no station and
reach `any` for whoever holds them. A resource "at a station" is a patient queued there
today; the queue (CP33) is what supplies the fact, and until it exists a station-scoped
decision on a resource with no station is a refusal, which is the safe direction.

## 5. The cache, and how long a revocation can lag

Roles are read from the database and memoised for a person for at most **thirty seconds**
(`rbac.CacheWindow`). Every grant, revocation and status change through the identity
service drops the person's entry, so in the normal case the change is felt on the next
request. The window is the bound for the abnormal case — an invalidation that did not
reach a second process, a cache that was down when it was sent.
`TestRevocationTakesEffectWithinTheWindow` holds both: the immediate path through the
service, and a hand-edited grant that the cache was never told about.

A suspended account resolves to no roles at all, whatever it held.

## 6. How it is proven

- **The decision matrix** (`TestDecisionMatrix`): eighteen roles × sixty-two permissions,
  every cell asserted against the catalogue and the three named constraints, stated in the
  test independently of the engine so that the test does not merely agree with itself.
- **The matrix document** (`TestMatrixDocumentIsCurrent`): `access-matrix.md` is generated
  by `go run ./tools/accessmatrix` and compared byte for byte. It is what Dr. Nahid reads
  against the blueprint; it cannot drift from the code.
- **The grant table** (`TestRolePermissionsMatchTheDatabase`): the Go copy of the grants
  and migration 00006 are compared both ways, so a permission added to one and not the
  other fails.
- **The named criteria**: nutritionist × prescription read, pharmacist × diagnosis read,
  registration × sensitive read, an unknown action, and the revocation window — each its
  own test, each named for the criterion.

## 7. Enforcement (CP20)

Three layers, one engine, and a fourth thing that is not a layer but a boot check.

**The route.** Every route is registered through `httpx.Declare` with `Public()`,
`Session()` or `Permission(...)`. `NewRouter` walks the finished router and returns an
error naming any route that was registered otherwise; `cmd/api` refuses to start on it.
A permission-guarded route asks the engine — `rbac.HTTPAuthorizer`, which resolves the
caller through the cache and reads `X-Active-Role` — before the handler runs, and leaves
the resolved Subject on the context. The 403 is the same words whatever the reason
(`TestAForbiddenAnswerDoesNotRevealExistence`); the reason and the rule go to the log.

**The service.** `rbac.Authorize(ctx, action, resource)` reads that Subject back and
decides the resource with its facts — station, owner, sensitivity. Same 403.

**The serialiser.** `rbac.Marshal(subject, value)` removes the fields a subject may not
see, by `visible:"permission"` tags, at any depth. It is default-restrictive: a field whose
JSON name looks clinical and carries no tag makes the whole type unserialisable, so a new
endpoint cannot leak a diagnosis by omission. The golden files in `internal/rbac/testdata`
are the exact bytes a pharmacist and a registration officer receive.

**The audit.** `TestEveryRouteDeclaresItsRequirement` in `cmd/api` is the hand-written
table of every route and what it takes; `TestEveryGuardedRouteRefusesARoleWithoutItsPermission`
walks the declarations and hits each guarded route as a researcher. A route added tomorrow
is in both the day it exists.

What CP20 does not do: the station scope still has no queue to ask (CP33), the station app
does not yet send a hat (its screens arrive with the stations), and break-glass is CP22.
