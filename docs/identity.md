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

## 7. Sessions (CP16)

Established at CP16, pass one: the schema and the rules. The endpoints and the two login
screens follow in pass two.

### 7.1 The access token is opaque

[ADR-0011](adr/0011-opaque-access-tokens.md) amends D-44. The short version: CP16's own
acceptance criterion 3 says revocation takes effect within one request, which obliges the
server to consult the session registry on every request — so a signature would verify a
token that is about to be checked statefully anyway.

What that removes is worth naming: algorithm confusion, `alg: none`, unvalidated `kid`,
signing-key rotation, clock skew, and claims that were true when signed and false when read.
None of them can happen to a random string looked up in a table. What it costs is one
indexed lookup per request, which criterion 3 already obliged us to pay.

The token is 32 random bytes; the database holds only its SHA-256 digest, so a leak of the
session table yields nothing presentable. SHA-256 rather than argon2id there is deliberate
and is not an inconsistency with the password column: a password is low-entropy and must be
expensive to guess, a 256-bit token cannot be guessed at all.

### 7.2 Refresh families, and why reuse is treated as theft

A refresh token is **spent** when it is exchanged, and its successor inherits its
`family_id`. If a spent token arrives again, exactly one of two things has happened: a client
retried after a dropped response, or somebody else has a copy.

**The server cannot tell which, and only one reading is safe to act on.** So the whole family
is revoked — every token and every session descended from that login — and everyone involved
logs in again. That is disruptive on a bad network and correct on a bad day. Assuming a retry
instead would mean a stolen token keeps working.

A rotated token keeps its predecessor's expiry rather than getting a fresh one. Otherwise a
session used daily never expires, and "log in again every fortnight" quietly becomes "never
log in again".

### 7.3 Failing identically

Every failed login returns one error with one message: unknown code, wrong password,
suspended account, throttled. Three things make that more than a message:

- **An unknown account still costs a hash verification**, against a dummy hash whose result
  is discarded. Without it the server answers "does this person work here" by refusing
  faster.
- **The status check happens _after_ the password is verified**, so an attacker cannot learn
  that an account exists and is suspended without also knowing its password.
- **The delay is applied before the answer**, not after and not only on failure, so response
  time carries no information either.

The real reason is written to `core.login_attempt`, which an administrator can read and an
attacker cannot, and which the application role may not rewrite — an attempt log a
compromised account can edit is not a log.

### 7.4 A delay, not a lockout

Two free attempts, then one second doubling to a thirty-second cap, counting failures in the
last fifteen minutes, per employee code **and** per client address — whichever is worse.

Not a lockout, deliberately. Employee codes are printed on rosters and called across a clinic
floor, so a lockout hands anybody who knows one the ability to keep a doctor out of the
system on the morning they need it. A delay costs an attacker their throughput without
costing the real user their job. The cap exists because an unbounded delay is a lockout
wearing a different hat, and holds a request open long enough to become its own denial of
service.

### 7.5 What suspension does

Nothing to the grants. `Authenticate` checks the user's status on every request, so
suspending an account ends every session it holds at once — no walking the sessions, no
partial state if it fails halfway, and reinstating is one status change rather than a
re-grant.

### 7.6 Two more database invariants

| Assertion                           | Fails when                                                               |
| ----------------------------------- | ------------------------------------------------------------------------ |
| `assert_credentials_undeletable()`  | the application role can delete a session or **rewrite a login attempt** |
| `assert_no_plaintext_credentials()` | a column on `session` or `refresh_token` looks like it holds a token     |

The second one is the interesting one. "Digests only" is a rule that lasts exactly until
somebody adds a convenience column during an incident. As a schema property, the migration
that adds it fails instead.

### 7.7 What pass one does not include

The endpoints (`/v1/auth/login`, `/refresh`, `/logout`, `/me`), their OpenAPI definitions,
the pgx store adapter, and the web and mobile login screens. The service and its rules are
finished and tested; what remains is wiring.

## 8. Open

**The role list is aspirational until the staffing is known.** All eighteen roles are seeded
because the catalogue is the expensive part and does not change; `core.station.is_staffed`
defaults to **false**, so the traffic-control board will not route a patient to a room
nobody works. Turning a station on is an operational act, recorded — not a consequence of a
seed running. Correcting the picture when DTHC's actual staffing is known is one migration
setting flags, not a redesign.

**Argon2id parameters need benchmarking on the real server.** 46 MiB, two passes, is in
the band OWASP recommends, but the target is 250–500 ms per hash and what that costs depends
on hardware nobody has bought yet (D-30 waits on D-01). The parameters travel inside each
hash, so raising them later does not invalidate a single password: an old hash verifies under
the parameters it was made with and is upgraded on the next successful login.

**The Bangla role and station names are the engineer's translations**, like every other
Bengali clinical label in the system, and carry the same standing invitation to be
corrected.
