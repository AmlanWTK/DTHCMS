# The write path: attribution and retries

CP24. Two guarantees, both about what a client cannot do. The first: a client cannot say
who it is — the attribution envelope [R-03] is built from what the server verified and from
nothing else. The second: a client can retry a write as often as it likes and the record
shows one value with one attribution.

The ledger itself is `docs/event-store.md`; the decisions are ADR-0016.

## 1. The actor

`eventstore.Actor` holds the person, the device, the hat, the station and the facility. Its
fields are **unexported**. No package outside `eventstore` can write one down — which means
no handler can attribute an event to anyone but the caller, and no request body can be the
source of any of it.

There are exactly two doors:

| Door                | Who may use it                                                       |
| ------------------- | -------------------------------------------------------------------- |
| `ActorFrom(ctx)`    | Production code. Reads the verified principal the chain established. |
| `ActorForTest(...)` | `_test.go` files only, enforced by `dthclint testonly`.              |

`ActorFrom` refuses rather than blanks: no principal, an unparseable id, a session with no
device, or no confirmed role each return a named error. An event attributed to nobody is
worse than a write that did not happen.

### Where the principal comes from

```
Authenticate      → Caller   (the session behind the bearer token)
VerifyDevice      →          (the signature behind the device headers)
Authorize         → Principal (the active role, once the resolver confirms the person holds it)
                     ↓
                  ActorFrom(ctx)
```

The principal is minted by the **authorisation** engine, not by authentication, because
that is the first moment the active role is known to be genuine. `X-Active-Role` is a client
header until then. A route that is not permission-guarded therefore carries no principal,
and a clinical write from one cannot build an envelope — fail-closed, by construction.

`httpx.Principal` lives in platform because `rbac` fills it and `eventstore` reads it, and
the architecture allowlist lets both import platform and neither import the other.

### The test-only rule

`dthclint testonly` is a general rule, not a special case for one function. A function whose
doc comment carries

```go
//dthclint:testonly
```

may be called from `_test.go` files and nowhere else — including `cmd` and `tools`, which
the architecture check exempts. Today it marks `eventstore.ActorForTest` and
`httpx.CallerForTest`. The next test-only door gets the same treatment by writing the
directive above it, in the change that opens it.

## 2. `Idempotency-Key`

Three layers protect a retry (§7.5):

| Layer        | What it protects                              | Where                          |
| ------------ | --------------------------------------------- | ------------------------------ |
| 1 `event_id` | The _event_ is stored once, whatever arrives  | CP23, `ledger.event_key`       |
| 2 the header | The _response_ is the original, byte for byte | CP24, `ops.idempotency_record` |
| 3 job keys   | A job runs once                               | CP69                           |

Layer 1 alone is not enough at the transport layer: a client that only had it would get a
duplicate-key error it has to interpret, and a request that writes several events, or none,
would have no answer at all.

### The contract

The header is **required** on every state-changing request inside `/v1`. A mutating request
without one is `422` before the handler runs. Sign-in and refresh (`/v1/auth/…`) sit outside
the authenticated chain and take no key: there is no caller yet to scope one to.

Generate a UUIDv7 when the operator commits the action, and send **that same key** on every
retry of that attempt — across a timeout, an app restart, a morning offline. A new action
gets a new key.

| Situation                         | Answer                                                 |
| --------------------------------- | ------------------------------------------------------ |
| Same key, same request, settled   | The stored response, with `Idempotency-Replayed: true` |
| Same key, first attempt in flight | `409 IDEMPOTENCY_IN_PROGRESS` — wait and retry         |
| Same key, **different** request   | `409 IDEMPOTENCY_KEY_REUSED` — a client bug            |
| No key on a mutating request      | `422` naming the header                                |

"The same request" means the same method, path and body. Headers are deliberately excluded:
a retry may carry a refreshed token or a new correlation id and is still the same request.

### What is stored, and what is not

Responses are kept 24 hours, up to 256 KiB. Beyond that the response is served normally and
simply not cached — layer 1 still holds.

`401`, `403`, `429` and `5xx` are **never** stored. They describe the state of the session,
the grant or the rate limiter at one instant; a cached `403` would keep answering for a day
after the role was granted, and a cached `500` would turn a transient failure into a
permanent one. The claim is released instead, so the client's next retry runs the handler.

### The table

`ops.idempotency_record`, primary key `(user_id, key)` — a key is one person's, so two
operators who choose the same key never see each other's responses. `fingerprint` is the
SHA-256 of method, path and body; `state` is `in_progress` or `complete`; `expires_at`
drives the purge.

It is in `ops` and not `ledger` on purpose: the row is updated and deleted, which the
ledger's append-only grant forbids by design (ADR-0008). This is a deliberate deviation
from the plan's §9.1 placement, recorded in the migration's own header.

The claim is taken with `INSERT … ON CONFLICT DO NOTHING RETURNING *` — one statement, so
two concurrent retries cannot both believe they are first.

## 3. Clients

```ts
import { writing, beginAttempt } from '@dthcms/api-client';

// One gesture the screen will not repeat on its own — the console's buttons.
api.POST('/v1/devices/{id}/suspend', { params: { ...writing(), path: { id } }, body });

// A write that will be retried — an outbox draining. Persist attempt.key with the row.
const attempt = beginAttempt(row.idempotencyKey);
api.POST(path, { params: { ...attempt.params(), path: { id } }, body });
```

`writing()` is deliberately a different name from `guarded`, which is what a state-changing
call needed before this checkpoint: every call site that had not been updated failed to
type-check rather than failing at runtime with a `422`.

`@dthcms/shared-schemas` provides the generator: `uuidv7()`, `idempotencyKey()`,
`isUuidV7()`, `uuidV7Timestamp()`. v7 rather than v4 because the first 48 bits are the Unix
millisecond, so ids sort in creation order — which makes an outbox that sorts by id replay
in the order the operator worked. On React Native, `react-native-get-random-values` must be
imported once at the entry point; without a crypto source the generator throws rather than
falling back to `Math.random`, because a weak `event_id` is a collision in a clinical ledger.

## 4. Acceptance criteria, and where each is proven

| Criterion                                                         | Test                                                                                                                                               |
| ----------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| (1) A duplicate `event_id` never creates two events, concurrently | `TestOneEventIdSurvivesFiftySimultaneousWriters`, `TestARetriedEventIdIsOneEventWhateverTheBodySays`, `TestSequencesAreGaplessUnderConcurrency`    |
| (2) A retried key returns the identical response body             | `TestARetriedRequestGetsTheIdenticalResponse` (Postgres), `TestARetryIsAnsweredFromTheStore`, `TestConcurrentRetriesClaimTheKeyOnce`               |
| (3) Client-supplied identity fields are ignored                   | `TestTheActorComesFromTheSessionAndNeverFromTheBody`, `TestAnActorCannotBeBuiltWithoutAVerifiedPrincipal`, `TestTheRealActorDoorIsMarkedAndUnused` |
| (4) An event missing an envelope field cannot be appended         | `TestAnIncompleteEnvelopeIsRejected` (module and table), `TestAnUnattributedEnvelopeIsRefusedBeforeTheDatabase`                                    |
| The header is required, in the real chain                         | `TestTheRouterRequiresAKeyOnEveryStateChangingRequest`                                                                                             |
| Every mutating endpoint documents it                              | `TestEveryMutatingEndpointDocumentsItsIdempotencyKey`                                                                                              |
| The TTL is enforced                                               | `TestExpiredRecordsArePurged`; the worker's hourly purge                                                                                           |
| The table's shape                                                 | `TestTheTableRefusesAMalformedRecord`                                                                                                              |

## 5. Manual verification

The plan's check is the field one: submit the same measurement twice from the station app,
killing the network mid-request, and confirm one value with one attribution. Until the
clinical endpoints exist (CP29 onward), the equivalent against the console:

```bash
KEY=$(uuidgen | tr 'A-Z' 'a-z')   # any 8–200 characters; the app sends a UUIDv7
curl -i -X POST "$API/v1/devices" \
  -H "Authorization: Bearer $TOKEN" -H 'X-Requested-With: DTHCMS' \
  -H "Idempotency-Key: $KEY" -H 'Content-Type: application/json' \
  -d '{"name":"Tablet 7","kind":"tablet"}'
# repeat verbatim: the same body, and Idempotency-Replayed: true
# change the body, same key: 409 IDEMPOTENCY_KEY_REUSED
# omit the header: 422 naming Idempotency-Key
```

```sql
SELECT key, state, status, length(body), expires_at FROM ops.idempotency_record;
SELECT ops.purge_expired_idempotency();   -- what the worker runs hourly
```

## 6. Open

- **D-71 browser device identity.** `ActorFrom` refuses a session with no device, so the
  browser cannot write clinical events. The console's writes are administrative and go
  through the audit trail, not the ledger. The decision is due before the first clinical
  screen on the web.
- **Job-level idempotency** (§7.5 layer 3) waits on the job framework at CP69. The purge
  runs on its own ticker in the worker until then.
- **The outbox** that persists `event_id` and `key` across restarts is CP64.
