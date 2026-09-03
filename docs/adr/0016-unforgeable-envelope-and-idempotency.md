# ADR-0016 · An unforgeable actor, `Idempotency-Key` required, and its records in `ops`

- **Status:** Accepted
- **Date:** 2026-09-03
- **Checkpoint:** CP24
- **Supersedes:** —

## Context

CP23 gave the ledger an attribution envelope [R-03] and a table that refuses an incomplete
one. What it did not give was any reason to believe the envelope's contents. `Actor` was an
ordinary exported struct: any handler could write one down, and the obvious wrong thing —
decoding `user_id` and `role` from a request body because they happen to be in the JSON the
client sent — was a two-line mistake nobody would notice in review. CP24's objective is to
make the envelope "structurally impossible to bypass".

The second half is retries. §13 requires offline retries to be safe. §7.5 describes three
layers: the ledger's `event_id` (CP23), an HTTP response cache, and job-level keys. The
second layer needed a table, and the plan's §9.1 puts `idempotency_records` in `ledger`.

Four decisions followed.

## Decision

### 1. `Actor`'s fields are unexported; there are exactly two doors, and one is closed by a linter

`eventstore.Actor` keeps `userID`, `deviceID`, `role`, `station`, `facilityID` and `code`
unexported, with accessor methods. No package outside `eventstore` can construct one. The
two ways to obtain one:

- `ActorFrom(ctx)` — from the verified principal the middleware chain established.
- `ActorForTest(...)` — a test's shorthand, marked `//dthclint:testonly`.

The linter rule is new and general: a function whose doc comment carries the directive may
be called from `_test.go` files and nowhere else, including from the composition roots the
architecture check exempts. `httpx.CallerForTest` carries the same marker for the same
reason. The compiler stops other packages writing the value down; the linter stops the
test door being used to get around the compiler.

_Considered and rejected:_ an unexported constructor plus an exported `NewActor(user, device,
role, …)`. It reads as safe and is not — the parameters are exactly what a handler would fill
from a request body, and the type system cannot tell a verified string from a client-supplied
one.

### 2. The principal is minted by the authorisation engine, not by authentication

`rbac.HTTPAuthorizer.Authorize` puts `httpx.Principal` on the context at the moment it
allows a request. That is later than one might expect, and deliberately so: it is the first
moment the _active role_ is known to be one the person actually holds. `X-Active-Role` is a
client header until the resolver has confirmed it, and an envelope that recorded the header
would be recording the client's claim.

A consequence worth stating: a route that is not permission-guarded carries no principal,
so a clinical write from one cannot construct an envelope at all. That is the fail-closed
behaviour, not a gap.

`Principal` lives in `platform/httpx` because `rbac` fills it and `eventstore` reads it,
and the architecture allowlist lets both import platform and neither import the other.

### 3. `Idempotency-Key` is required, not optional

The contract has said "required on every state-changing request" since CP08. The middleware
now enforces it inside the authenticated chain: a mutating request without a key is `422`
before the handler runs. The alternative — treat a missing key as a pass-through, since the
ledger's `event_id` still makes a replayed _event_ safe — was rejected because it leaves the
guarantee to whichever client remembered, which is not a guarantee. The generated client
makes this visible: every call site that had not been updated failed to type-check.

Sign-in and refresh are outside the chain and take no key: there is no caller yet to scope
one to, and a contract test refuses a `/v1/auth/` endpoint that documents one.

Three details, each chosen against a specific failure:

- **The fingerprint is method, path and body** — not headers. A retry may legitimately carry
  a refreshed token or a new correlation id and is still the same request. A key presented
  with a _different_ request is `409 IDEMPOTENCY_KEY_REUSED`: answering it with the first
  request's response would tell a client a write happened that did not.
- **A claim is taken atomically** (`INSERT … ON CONFLICT DO NOTHING RETURNING *`), so two
  concurrent retries cannot both proceed. The loser is told `409 IDEMPOTENCY_IN_PROGRESS`
  rather than being handed a half-written answer.
- **`401`, `403`, `429` and `5xx` are never stored.** They describe the state of the session,
  the grant or the rate limiter at one instant; a cached `403` would keep answering for a day
  after the role was granted, and a cached `500` would turn a transient failure permanent.

### 4. The records live in `ops`, not `ledger`

The row is written before the handler runs, updated after it, and deleted when it expires.
`ledger` forbids UPDATE and DELETE by grant, by rule and by trigger (ADR-0008), and
`core.assert_ledger_append_only()` refuses to start a service where that is not true. A
cache of HTTP responses is operational data with a TTL, not a fact of history, so it belongs
in `ops` with the other operational tables. Nothing is lost: what must be immutable is the
event, and the event is in the ledger.

The primary key is `(user_id, key)`. A key is the client's to choose and carries no meaning,
so scoping it to the person is what stops one operator's key handing back another's response.

## Consequences

- A handler cannot attribute an event to anyone but the caller, and cannot forget to
  attribute it at all: `ActorFrom` returns an error rather than a blank actor, and
  `Envelope.Validate` names the missing fields.
- Adding a mutating endpoint means documenting `Idempotency-Key` and `409` on it; a contract
  test fails otherwise, and the middleware would refuse the endpoint's own clients.
- Every client must generate UUIDv7s. `@dthcms/shared-schemas` provides `uuidv7()` and
  `idempotencyKey()`; `@dthcms/api-client` provides `writing()` for a single gesture and
  `beginAttempt()` for a write that will be retried from an outbox.
- The `ops.idempotency_record` table grows with write traffic and is purged hourly by the
  worker. At the clinic's expected load — a few thousand writes a day, 24-hour TTL — it holds
  low thousands of rows.
- The response cache is bounded at 256 KiB per response; a larger response is served and not
  cached, and the retry re-runs the handler. The ledger's `event_id` still makes that safe.
- **A future decision, recorded now:** the middleware sits after `Authorize`, so a write that
  is refused never claims a key. If a later endpoint needs its refusal to be replayable —
  none does today — that ordering is what would have to change.

## Alternatives considered

- **Idempotency in the ledger's `event_id` alone.** Correct at the data layer and not enough
  at the transport layer: the client gets a duplicate-key error it has to interpret rather
  than the original response, and a request that writes several events, or none, has no
  answer at all.
- **A Redis-backed response cache.** Faster and wrong: the claim must survive a restart and be
  visible to every instance with the same transactional guarantees as the write it protects.
  A cache that can lose a claim mid-request is a cache that permits the duplicate.
- **Keying on the request body's hash alone, with no client key.** Two genuinely distinct
  measurements with the same value from the same station in the same minute are the same
  bytes. The client's key is what says "this is the same _attempt_", which is the thing the
  body cannot say.
