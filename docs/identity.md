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

### 7.7 The endpoints, and what testing them against the database found

Pass two added the six endpoints (`POST /v1/auth/login`, `/refresh`, `/logout`,
`/logout-all`; `GET /v1/auth/me`, `/sessions`), their OpenAPI definitions, the pgx store, and
`http_db_test.go` — the six exercised end to end through the real router against a real
database, with cookies asserted on directly.

The service had 22 passing tests against an in-memory store before that file existed. The
database tests found two defects on their first run, both invisible to a map:

**Every refresh would have failed.** `RotateRefresh` marked the old token used with
`replaced_by = <successor>` and _then_ inserted the successor, on the belief that a foreign key
inside a transaction is checked at commit. It is not — PostgreSQL checks it at the end of the
statement unless the constraint is `DEFERRABLE`, and this one is not. The store now inserts the
successor first. The in-memory store has no foreign keys, so its rotation test passed
throughout.

**The per-client throttle counted nothing.** `clientDigest` hashed `r.RemoteAddr` whole, which
is `host:port`, and the port is ephemeral — a fresh one on every connection. An attacker
cycling through employee codes from one machine got a new identity per attempt and was never
slowed by the per-client rule (the per-code rule still applied). The digest is now of the host
alone, and four tests pin that: same host across ports, different hosts, `X-Forwarded-For`
ignored, IPv6.

One more change fell out of debugging the first: the refresh handler had been turning _any_
error into 401 "please sign in again". An internal failure — the database refusing mid-rotation
— now returns 500 with a correlation id and leaves the cookie alone, because the sign-in it
would have prompted fails for the same reason, and an alert that says "auth" for a database
incident sends the responder to the wrong place.

### 7.8 One token, two transports, and the forgery guard

The station app holds the access token and sends `Authorization: Bearer`. The web
application **never holds it**: the same token arrives in an `httpOnly` cookie
(`dthcms.session`, every path, expiring with the token) that the browser attaches and script
cannot read. That is ADR-0010, and pass two had drifted from it — the handlers set only the
refresh cookie and the middleware read only the header, which would have forced the browser
into the memory-only pattern the ADR explicitly rejected. Pass three restored the cookie
transport before a line of the login page was written on top of the wrong one.

A cookie the browser attaches by itself is attached to requests the user did not intend.
So **every request that changes state must carry `X-Requested-With: DTHCMS`**, from every
client, sign-in included. A form on another site cannot set the header; a script on another
site cannot send it without a CORS preflight the API refuses for any origin not on the
allowlist. Together with `SameSite=Lax` this is the "token on state-changing requests"
ADR-0010 promised. A header rather than a rotating synchroniser token because the API holds
no per-form state to synchronise against, and it is required of bearer clients too — one
rule with no branches cannot be bypassed by arriving through the other door.

The middleware prefers the header when both are present, so a client that deliberately
signed out on one surface is not silently re-authenticated by a stale cookie.

### 7.9 The web sign-in

`web/src/features/auth` is the form; `web/src/stores/session` is the state; the
`SessionGate` in `AppShell` is what keeps every shelled screen from rendering until
`/v1/auth/me` has answered. Three states, three outcomes: a skeleton before the first answer
(not the sign-in page — a tablet that flashed a login form on every reload would teach its
operators to distrust it), a redirect to `/login?next=…` on "nobody", the screen on a person.
The `next` parameter is user input and is confined to a path on this site, so the sign-in
page cannot be turned into an open redirect.

An expired access token is handled once, in `@dthcms/api-client`: a 401 triggers a single
refresh and a single retry, shared between every request that fails at the same moment.
That last property is not a nicety. The reuse detector treats a second exchange of one
refresh token as theft and revokes the family, so a naive per-request refresh would sign the
operator out every time a screen loaded two queries.

### 7.10 The station app

The bearer transport, end to end: `transport: "bearer"` at sign-in returns both tokens in the
body and sets no cookies; the refresh token goes in the device Keystore and the access token
stays in memory; refresh presents the stored token in the body of `/v1/auth/refresh`, and the
server reads the body before any cookie. `docs/mobile-shell.md` §7a has the reasoning and
the two failure modes it is designed around.

What remains for CP16 is the manual verification: sign in on web, let the token expire and
watch it refresh, revoke from another device and watch the first one drop; and the same on
a tablet, which waits on D-59.

## 8. The second factor (CP17)

TOTP, per D-45: it works with no network and no SMS bill, and every phone in the clinic can
run an authenticator app. Implemented against RFC 6238 in `internal/auth/totp` — sixty lines
and the RFC's own test vectors, rather than a dependency — with a drift window of one
thirty-second step either side.

### 8.1 Who must have one

Physicians, administrators, pharmacists and researchers (`TOTPRequiredRoles`). A person in
one of those roles can sign in with a password alone until they enrol, but `/v1/auth/me`
says `second_factor.required` and the interface takes them to enrolment before anything
else, and nothing privileged is possible until it is done. Floor staff may enrol; they are
not made to (device trust for them arrives at CP18).

### 8.2 What sign-in becomes

For an enrolled account the password earns a **challenge**, not a session: `202` with an
opaque token that lives five minutes. The code comes back with it to
`/v1/auth/login/second-factor`. A wrong code is one `401`, the same one a wrong password
gets, recorded as `bad_second_factor` and counted by the same throttle; five wrong codes
kill the challenge whatever the clock says, because the password behind it was right and
five wrong codes is somebody without the phone.

### 8.3 The replay guard

A code is valid for a step and the verifier forgives a step either side, so a code that was
right sixty seconds ago is still "right". `core.user_totp.last_used_step` records the last
step a code was accepted for, and a code at or before it is refused as a replay. A code read
over a shoulder cannot be typed in a second time.

### 8.4 Step-up

A privileged action asks for a fresh factor even inside a live session. `/v1/auth/step-up`
exchanges a code for a **token good for five minutes, this session, one purpose, one use**,
sent as `X-Step-Up-Token`. `httpx.RequireStepUp(purpose)` consumes it; a request without one
is `403 STEP_UP_REQUIRED`, which is distinct from `FORBIDDEN` on purpose — the person _may_
do this, and the client's right response is to open the prompt. The purposes are a closed
list (`prescription.sign`, `rbac.change`, `research.export`, `clinical.override`,
`second_factor.disable`, `second_factor.recovery_codes`); the first four have no endpoints
yet, and when they do they get the middleware, not a discussion.

Two endpoints use it today: disabling the factor and replacing the recovery codes. A session
left open on a desk cannot quietly take the factor down.

### 8.5 Recovery codes

Ten per enrolment, sixteen base32 characters each, shown exactly once, stored as SHA-256
digests. One works once; the row stays, marked used, which is the evidence. Re-enrolling or
regenerating revokes the previous sheet wholesale. Case, spaces and dashes are forgiven when
one is typed.

### 8.6 At rest

The seed is sealed before it reaches the database (ADR-0012); `core.recovery_code` and
`core.short_token` hold digests. The application role can delete none of it and rewrite no
`security_event`; `assert_second_factor_undeletable()` and
`assert_no_plaintext_second_factor()` are in the invariant registry.

### 8.7 The reset path (CP21)

A person who has lost their phone _and_ their recovery codes cannot step up, so cannot
disable their own factor. An administrator does it for them, in person, from the account
page of the console (`POST /v1/admin/users/{id}/second-factor/reset`): the seed is
disabled with a `security_event` naming the administrator and the stated reason, every
session of the person ends, and their next sign-in walks them through enrolling a new
authenticator. The administrator proves their own factor first — the route needs a
step-up minted for `credential.reset` — and cannot reset their own; that is what recovery
codes are for. See §11.

## 9. Devices (CP18)

D-46, realised: an administrator enrols every tablet and phone, each one holds a key it
made itself, and every request it makes is signed. That is what turns the `device_id` on a
clinical event from a claim into evidence [R-03]: the server accepted the write only after
checking a signature nothing but that device's key could have produced.

### 9.1 Enrolment

The console (`/admin/devices`) registers a device by name and returns a **one-time code**
— ten base32 characters, fifty bits, fifteen minutes, shown once. Somebody types it into
the tablet's device screen. The tablet generates a 32-byte Ed25519 seed, keeps it, and
sends the code with the public key to `POST /v1/auth/device/enrol`. The code is spent, the
device is `active`, the key is live. Every refusal is the same `401`: an unknown, expired,
spent code and one for a revoked device are indistinguishable to whoever is typing.

Re-enrolment is the same code, for a device that exists — a reinstalled app takes its
Keystore with it. The old key keeps working until the new one arrives, then is retired.

### 9.2 The signature

Four headers on every request from an enrolled device — `X-Device-Id`, `-Timestamp`,
`-Nonce`, `-Signature` — and the signature is Ed25519 over

```
METHOD "\n" path "\n" timestamp "\n" nonce "\n" hex(sha256(body)) "\n" device-id
```

Method and path bind it to this request; the body digest to these bytes; the timestamp
(±5 minutes) bounds how long a captured request could be replayed, and the nonce —
remembered in Redis for twice that — closes the rest. `internal/auth/devicesig` is the
reference; `mobile/src/lib/device-signing.ts` mirrors it, and the two test suites assert
the same vector, so drift on either side fails a build rather than a clinic morning.

`VerifyDevice` runs on every `/v1` request. A request with no device headers passes
through as a browser — unless the session it presents was opened from a device, in which
case it is refused: an access token lifted off a tablet is useless anywhere else, and so is
the refresh token (`Sessions.RefreshFrom` checks before rotating, so the tablet's own copy
is never spent by the attempt). A signature that fails under an active device's key is
recorded as a `signature_refused` event on that device: that is a forgery or a corrupted
Keystore, and either is worth an administrator's look.

### 9.3 What a device may not do

**Unenrolled and revoked devices cannot write clinical events.** `httpx.RequireDevice`
refuses with `403 DEVICE_REQUIRED` — its own code, because the person may do this, from a
tablet — and every clinical write route gets it when clinical write routes exist. The
test route in `device_db_test.go` proves the rule now. The event store adds the second
check at CP23: a device's status is consulted at ingest, and an event queued on a device
later reported **lost** is quarantined rather than accepted or dropped.

### 9.4 Lifecycle

`pending → active ↔ suspended → revoked | lost`. The last two are terminal: the key is
retired, every session on the device is ended, and the next request — signed or not — is
refused. A device is never deleted; an event written from it last year still names it.
Every transition needs a reason and writes a `device_event`; the application role can
delete none of the four tables and rewrite no event (`assert_devices_undeletable`), and an
active device always has exactly one live key (`assert_device_keys_sound`).

### 9.5 Where the private key lives, honestly

The plan says "private key in Android Keystore". What CP18 does: the seed is generated in
software on the device and kept in `expo-secure-store`, which the Android Keystore
encrypts at rest and which is wiped by an uninstall or a removed screen lock. It is not a
hardware-bound, non-exportable key; Expo has no module that generates an Ed25519 key
inside the secure element, and adding a native module for it is the "attestation, later,
optional" the plan already defers (D-46). ADR-0013 records the choice and what would
change it. The practical difference is small — an attacker with root on the tablet could
read the seed; one without cannot — and the thing that matters for [R-03] holds either
way: the key never leaves the device by any path the software offers.

### 9.6 The browser is not a device

A physician at a desktop signs in without one. Today that is fine, because nothing a
browser can do writes a clinical event. It stops being fine at the checkpoint that gives
the browser a clinical write. The recommended answer — a non-extractable WebCrypto Ed25519
key kept in IndexedDB, enrolled through the same code flow — is open decision D-71,
recorded in `progress.md`, and needs a decision before that checkpoint, not during it.

## 10. Open

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

## 11. The administration console (CP21)

Everything above becomes something a clinic manager can do without a developer:
`/v1/admin` and the two web pages behind it, **Administration → Users** and the account
page. It adds one permission to the catalogue, `user.credential.reset` (migration 00011),
and nothing structural.

### 11.1 What an administrator can do

| Act                                                 | Route                                          | Permission                                         | Step-up purpose    |
| --------------------------------------------------- | ---------------------------------------------- | -------------------------------------------------- | ------------------ |
| See everyone, and one account                       | `GET /v1/admin/users`, `/users/{id}`, `/roles` | `user.read`                                        | none               |
| Invite a colleague, with roles and a first password | `POST /v1/admin/users`                         | `user.invite`                                      | `user.manage`      |
| Activate, suspend, deactivate                       | `POST /v1/admin/users/{id}/status`             | `user.invite` / `user.suspend` / `user.deactivate` | `user.manage`      |
| Grant, revoke a role                                | `POST …/roles`, `POST …/roles/{role}/revoke`   | `role.grant` / `role.revoke`                       | `user.manage`      |
| Sign someone out everywhere                         | `POST …/sessions/end`                          | `user.credential.reset`                            | `credential.reset` |
| Set a password in person                            | `POST …/password`                              | `user.credential.reset`                            | `credential.reset` |
| Reset an authenticator                              | `POST …/second-factor/reset`                   | `user.credential.reset`                            | `credential.reset` |

Every write needs a **step-up** (§8.4). The purpose is split in two so a token minted to
manage an account cannot be spent on a credential, and each token is spent by one call.
The order of the checks is deliberate: the permission is decided _before_ the step-up is
looked for, so a person without the permission gets the same `FORBIDDEN` every other
route gives them and never learns that a door exists and which key it takes.

### 11.2 What it refuses

An administrator cannot suspend or deactivate themselves, revoke their own `ADMIN` role,
or reset their own authenticator (`ErrSelfAction`, 409). The lifecycle of §2 is enforced
unchanged — the console offers only the moves the status allows, and the server refuses the
rest with the list of what was possible. A suspension must carry a reason; a first password
is 12–128 characters, hashed like any other, never stored or logged, and shown to the
administrator exactly once so they can read it across the desk.

### 11.3 What it records

Each act produces one `AuditEntry` — kind, actor and active role, target, reason, a before
and after where a value changed, the client digest, and the time — handed to an
`AuditRecorder` the composition root wires in. The recorder is CP22's hash-chained ledger;
until it is connected the entries have nowhere to go and the acts still leave their
database-level traces (`core.user_role` history, `status_reason`, `security_event`).
Setting a password, resetting a factor and any status move away from `active` end every
session of the person, so a credential an administrator has just changed cannot be used
by a session that predates it.
