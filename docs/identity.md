# Identity: users, roles and permissions

Established at CP15. This is what every attributed write depends on: [R-03] puts a
`user_id` on every clinical event, so nothing can be recorded before this schema can answer
_who_.

The short version: **a permission is never a property of a role alone.** [R-02] lets one
operator hold several station roles at once — the same assistant enters a blood pressure,
then switches to anthropometry, from the same phone — so what a person may do is the union
across every role they currently hold, resolved live.

---

## 1. What is enforced by the database, and why

Four things do not live in Go, and each is deliberate. A rule that exists only in the
application holds until somebody opens `psql` during an incident, writes a maintenance
script, or adds a second service. These hold regardless.

| Guarantee                       | How                                                                                                                                      | Where |
| ------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- | ----- |
| A user is never deleted         | `dthcms_app` holds no `DELETE` on `core.app_user` or `core.user_role`; `core.assert_users_undeletable()` checks it after every migration | 00005 |
| Lifecycle transitions are legal | `core.assert_user_status_transition` trigger                                                                                             | 00005 |
| A suspension states a reason    | `CHECK app_user_suspension_explained`                                                                                                    | 00005 |
| Blueprint §4.4's access rules   | `core.assert_rbac_constraints()`, folded into `core.assert_invariants()`                                                                 | 00006 |

The last one is the interesting one. §4.4 states three rules in prose:

> Nutritionist: write diet; read labs; no access to prescriptions.
> Registration: read/write demographics; blinded to sensitive clinical diagnoses.
> Pharmacist: sees authorized drug list and dosing only; diagnoses hidden.

Prose cannot fail a build. Those three sentences are now queries that raise on every
`migrate up` in every environment, so a future checkpoint adding one convenient permission
to the pharmacist role finds out immediately rather than at an audit. A fourth checks D-48:
the researcher role reaches the de-identified schema and nothing else, which is the same
thing 00002 already enforces at the schema level — two layers saying one thing.

Two more assertions catch the failure modes of a catalogue rather than of a rule: a
permission granted to no role (a check that can never pass), and an active station no role
works (a room the queue can route a patient to and nobody can serve).

## 2. The lifecycle

```
invited ──▶ active ──▶ suspended ──▶ active
   │           │            │
   └───────────┴────────────┴──────▶ deactivated ──▶ active
```

**Deactivated is not terminal, deliberately.** Staff leave and come back, and re-inviting
them would create a second user row — which splits one person's history in two and defeats
the reason users are never deleted at all.

**Suspension is not revocation.** Suspending an account resolves it to zero permissions
without touching a single grant, so it takes effect in the minute it is needed and is
reversed as quickly. Walking a user's grants would be slower, would leave a partial state
if it failed halfway, and would have to be undone one by one.

The transition table exists twice: in `internal/auth/lifecycle.go` and in the database
trigger. The Go copy is there so a caller gets a 422 naming both states rather than a 500
naming a trigger. `TestLifecycleMatchesTheDatabase` walks all sixteen ordered pairs against
a real database and fails if they ever disagree — the duplication is answered by a test, not
by discipline.

## 3. The catalogue

**Eighteen roles** (blueprint §6.3), **twelve stations** (§3), **62 permissions** as
`resource.action.scope` triples.

Roles and permissions are **not** facility-scoped, and that is a decision rather than an
omission: what `PHARMACIST` means cannot differ between branches, or a permission audit
would have to be repeated per site and would eventually diverge. _Where_ a role applies is a
property of the grant (`core.user_role.facility_id`), not of the role.

Five permissions are marked `is_sensitive` — they reveal a diagnosis or a clinical
interpretation:

`patient.read.clinical` · `records.read` · `diagnosis.read` · `diagnosis.write` ·
`ai.synthesis.read`

That flag is what makes §4.4's blinding rules checkable by a query instead of by reading a
grant list carefully and hoping.

### One deliberate departure

**`patient.read.allergies` is not marked sensitive, and the pharmacist holds it.** §4.4 says
the pharmacist sees dosing and not diagnoses. A pharmacist who cannot see an allergy cannot
dispense safely. Allergies are therefore held apart from diagnoses, so the blinding rule
stands without costing a safety check. **Worth Dr. Nahid's confirmation** — it is the one
place this checkpoint reads the blueprint's intent rather than its letter.

## 4. Grants are history

Revoking a role sets `revoked_at`; the row stays. Granting the same role again inserts a new
one. A partial unique index allows exactly one live grant per user, role and facility, and
places no constraint on revoked rows — which is what lets a role be granted, revoked and
granted again across a career while "who could sign a prescription on the fourteenth of
March" stays a question with an answer.

Rolling the catalogue back once grants exist is refused with an explanation rather than a
foreign-key violation, because deleting `core.user_role` rows is precisely what this
checkpoint exists to prevent.

## 5. What the application decides

The database enforces what must hold for every writer. `internal/auth`'s `Service` holds
the rules that need context the database does not have — who is asking, and why:

- Granting a role requires `role.grant`; revoking requires `role.revoke` **and a stated
  reason**.
- **An administrator may not revoke their own `ADMIN` role**, or suspend or deactivate their
  own account. This is D-70 in miniature: a clinic with no administrator is locked out of
  its own system, and the likeliest route there is one person tidying up their own account.
  Another administrator may do it — which also means two people were involved.
- A deactivated user cannot be granted a role.

## 6. What is deliberately not here

Authentication (CP16), sessions (CP16), TOTP (CP17), device binding (CP18) and enforcement
(CP19, CP20) are their own checkpoints. `password_hash` and `totp_secret_enc` exist as
nullable columns from this migration because adding a column to a populated user table on
the morning authentication ships is a migration nobody wants to write under time pressure.

This package answers what a user _may_ do. It does not decide whether to let them.

## 7. Open

**The role list is aspirational until the staffing is known.** All eighteen roles are seeded
because the catalogue is the expensive part and does not change; `core.station.is_staffed`
defaults to **false**, so the traffic-control board will not route a patient to a room
nobody works. Turning a station on is an operational act, recorded — not a consequence of a
seed running. Correcting the picture when DTHC's actual staffing is known is one migration
setting flags, not a redesign.

**The Bangla role and station names are the engineer's translations**, like every other
Bengali clinical label in the system, and carry the same standing invitation to be
corrected.
