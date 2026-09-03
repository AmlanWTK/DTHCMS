# API conventions

The contract of record is [`api/openapi.yaml`](../api/openapi.yaml). This page is the
reasoning behind its shape — the decisions a generated document cannot explain, and the
rules every checkpoint from CP16 onward is expected to follow without being asked.

Established at CP12. Detail beside the code: [`api/README.md`](../api/README.md),
[`packages/api-client`](../packages/api-client), [`packages/shared-schemas`](../packages/shared-schemas).

---

## 1. What CP12 built, and what it deliberately did not

Built: the OpenAPI 3.1 document, the error envelope and standard responses, the
pagination, filtering, idempotency and versioning conventions, a generated TypeScript
client used by both surfaces, a Go test that fails when the router and the document
disagree, a spec linter, and a self-contained documentation page.

**Not built, on purpose:** endpoints. The API serves `/healthz`, `/readyz` and `/version`
and nothing else, because nothing else exists yet. Clinical routes arrive with the modules
that own them — each adding its own paths to the same document as part of its own
checkpoint.

That is the point of doing this now rather than later. A convention agreed once, before
there is anything to migrate, costs a day. The same convention retrofitted across twelve
clinical stations is a rewrite, and the version of it that actually happens is "we left
the old ones alone".

## 2. Errors: one envelope, always

Every non-2xx response is the same shape. There is no second error format anywhere in the
API, and no endpoint gets to invent one.

```json
{
  "error": {
    "code": "VALIDATION_FAILED",
    "kind": "validation",
    "message": "Some values need correcting.",
    "message_bn": "কিছু তথ্য সংশোধন করতে হবে।",
    "fields": { "date_of_birth": "A date of birth cannot be in the future." },
    "correlation_id": "0198c4e2-7f3a-7000-8c1d-2b4e6a8f0c3d"
  }
}
```

**Clients branch on `code`, never on `message`.** Codes are stable; messages are written
for humans and will be reworded by someone who has never read the client.

**Both languages are always present.** Not a fallback, not a translation looked up later —
both, in the response. Half the clinic's staff work in Bangla, and an error is precisely
the moment a person needs their own language rather than their second one.

**`kind` decides how the interface responds**, and the distinction is not cosmetic:

| `kind`       | Meaning                                            | What the interface owes the user                     |
| ------------ | -------------------------------------------------- | ---------------------------------------------------- |
| `validation` | Malformed, or failed a rule. Fixable.              | Point at the field                                   |
| `auth`       | Not authenticated, or not permitted                | Sign in — never "you lack permission for patient 41" |
| `not_found`  | Does not exist, or the caller may not know it does | Say so plainly                                       |
| `conflict`   | Contradicts current state                          | Show what changed                                    |
| `clinical`   | A clinical rule blocked this                       | An override path, with a recorded reason             |
| `technical`  | The system failed. Not the user's fault.           | A retry button                                       |

An interface that shows the same red box for a blocked safety rule and a database timeout
teaches staff to dismiss both. That is the failure this column exists to prevent.

**`403` and `404` are deliberately indistinguishable** where the resource is one the caller
may not see. Telling somebody that a patient exists but is out of their reach is itself a
disclosure.

**Nothing internal crosses the boundary.** No query text, no file path, no library name.
The cause is logged against the correlation ID, where it is access-controlled.

### The correlation ID

Every response carries `X-Request-ID`, and error bodies repeat it in `correlation_id`. It
is in the body as well as the header because errors are reported from a clinic by
photographing the screen, and a header does not appear in a photograph.

Clients may send their own on the way in; the server generates a UUIDv7 when they do not.

## 3. Pagination: cursors, not pages

```
GET /v1/patients?cursor=eyJhZnRlciI6IjAxOThjNGUy&limit=50
```

```json
{
  "items": [{ "id": "…" }],
  "page": { "next_cursor": "eyJhZnRlciI6IjAxOThjNGZm", "has_more": true }
}
```

Every list endpoint returns `{ items, page }`, so a screen that can render one paginated
list can render all of them.

**Why cursors.** Clinical lists change underneath the person reading them. A queue gains a
patient while an operator is on the second page; with offsets, every subsequent row shifts
by one — so the operator sees one patient twice and never sees another at all. A cursor is
a position in the data rather than a count of rows skipped, so an insertion above it
changes nothing.

**`has_more` is the only end-of-list signal.** A page shorter than `limit` does not mean
the end; the server may return fewer items than asked for at any time, and a client that
stops early on a short page silently drops the tail of a list.

**What this costs:** no "jump to page 7", and no total count. Neither is something a
station operator has ever needed, and both are things a paging bug would cheerfully
provide.

## 4. Idempotency: required on every write

```
POST /v1/observations
Idempotency-Key: 0198c4e2-7f3a-7000-8c1d-2b4e6a8f0c3d
```

Not optional, not per-endpoint. **Every state-changing request carries a client-generated
UUIDv7**, and the server stores the outcome against it: a repeat returns the original
response rather than doing the work twice. The same key with a genuinely different body is
a `409 CONFLICT`, not a silent overwrite.

The key is generated **before the request is queued**, not when it is sent. That is what
makes an offline replay safe — the identifier travels with the event rather than being
assigned on arrival, so the same event replayed ten times is inserted once. It is the same
reasoning that made event IDs client-generated in the platform layer.

Without this, a clinic's ordinary dropped connection turns one recorded blood-pressure
reading into two rows in an append-only ledger. The ledger being append-only, nobody can
quietly tidy that up afterwards — which is exactly why it is append-only, and exactly why
this header is mandatory rather than encouraged.

**Automatic retry of writes is off on both surfaces** (`shouldRetryMutation`). Retrying a
write is a decision for the screen that owns it, made explicitly, with this header.

## 5. Filtering

Filters are query parameters named after the field they filter, with two shared ones worth
standardising now:

| Parameter       | Meaning                                                                                        |
| --------------- | ---------------------------------------------------------------------------------------------- |
| `updated_since` | Changed at or after an RFC 3339 instant. The station app's catch-up filter after time offline. |
| `q`             | Free-text search, where an endpoint supports it. Never a substitute for a real filter.         |

Filters combine with AND. An endpoint that needs OR needs a design conversation, not a
query-string convention.

## 6. Versioning: `/v1`, additive only

Clinical routes live under `/v1`. Within a version, changes are **additive only** — a new
optional field, a new endpoint, a new enum member clients may ignore.

Anything that would break a client already in the field requires `/v2`: removing a field,
narrowing a type, changing a status code, making an optional field required.

The rule is stricter than it looks, deliberately. **A station tablet may be running a build
that is weeks old and has been offline for a day.** The server cannot assume every client
has updated, and the client cannot be asked to update before it can sync — that is the
failure mode where a clinic session stops working because a deployment landed.

Deprecation is `deprecated: true` plus a `Deprecation` response header. The field keeps
working until the version is retired.

**Consuming clients must tolerate new enum members.** The Zod schemas parse `kind`
leniently for exactly this reason: an old build meeting a kind invented at CP90 should
still show the operator the message, not fail to parse the envelope carrying it.

The operational endpoints sit outside `/v1`. An orchestrator probing readiness has no
credentials and no interest in API versions.

## 6a. Authentication and the forgery guard (CP16)

One access token, two transports. The station app sends `Authorization: Bearer`; the web
application never holds the token and sends nothing — the same token travels in the
`httpOnly` `dthcms.session` cookie the browser attaches (ADR-0010). Both are declared as
security schemes and either satisfies the document's default `security`.

**Every request that changes state carries `X-Requested-With: DTHCMS`.** It is declared as
a required header parameter on every unsafe operation, so the generated types demand it;
`@dthcms/api-client` sends it on every request by default and exports `guarded` to satisfy
the type at a typed write call. The server refuses a request without it with 403 before
anything else is examined — sign-in included. Reads do not need it, and a browser's own
navigations could not carry it anyway.

A 401 on an authenticated call means the access token has expired or the session was
ended. The client refreshes once and retries once — `createRefreshingFetch`, shared
between every request that fails at the same moment, because the refresh token's reuse
detector treats a second exchange as theft. A 401 from `/v1/auth/refresh` means sign in
again.

## 6b. The second factor and step-up (CP17)

Sign-in may answer `202` instead of `200`: the password was right and a code is owed. The
body is a `ChallengeResponse` whose token goes back, with the code or a recovery code, to
`/v1/auth/login/second-factor`; that call answers like sign-in does. Both outcomes are in
the contract, so the generated client's return type makes the caller handle the second
step.

A privileged action may answer `403 STEP_UP_REQUIRED` — distinct from `FORBIDDEN`, because
the person is allowed to do this and merely has to prove it is still them. The client
exchanges a fresh code for a token at `/v1/auth/step-up` and repeats the request with
`X-Step-Up-Token`, declared as the `StepUpToken` header parameter on every operation that
needs one. The token is good for five minutes, one session, one purpose, one use. On the
web this is `useStepUp(purpose)` in the auth feature, which opens the prompt and resolves
to the token; a caller never sees the code.

## 6c. Device-signed requests (CP18)

An enrolled tablet signs every request: `X-Device-Id`, `X-Device-Timestamp`,
`X-Device-Nonce`, `X-Device-Signature`, Ed25519 over the method, path, timestamp, nonce,
body digest and device id (`docs/identity.md` §9.2). The generated client knows nothing
about this; the station app's fetch adds the headers after the bearer token, so a retry
after a refresh is re-signed with a fresh nonce. A browser sends none of them and is not a
device. A session opened from a device — sign-in and refresh both — is refused anywhere
else: `401`.

An action that needs a device and did not get one is `403 DEVICE_REQUIRED`, its own code
because the person may do this, from a tablet. Every clinical write route carries it.

## 6d. Authorisation (CP20)

Every route declares what it needs — public, a session, or one of a list of permissions —
and the server refuses to start with a route that declares nothing. A permission-guarded
route is decided by the RBAC engine before its handler runs (`docs/access-model.md`), so a
`403` is answered before anything is looked up and says nothing about whether the thing
exists: the same body for a real id, a random one and a malformed one. The working goes to
the log, with the rule that decided.

`X-Active-Role` names the hat the caller is wearing [R-02]. Optional; the web application
sends the role chosen in its switcher on every request, and the server decides for that
role alone — the same role the sidebar is scoped to, so the two cannot disagree about a
button. `/v1/auth/me` reports `grants`, which role confers which permissions, for exactly
this purpose.

Responses are shaped by the reader: a field a role may not see is absent from the bytes,
not null (`rbac.Marshal`, `visible:` tags). A pharmacist's prescription has no `diagnosis`
key. The contract documents the full shape; a client reads what it is sent.

## 7. How drift is prevented

Four checks, in three languages, all reading the same document:

| Check                                                                  | Where                                           | Catches                                  |
| ---------------------------------------------------------------------- | ----------------------------------------------- | ---------------------------------------- |
| Every served route is documented, and every documented route is served | `backend/cmd/api/contract_test.go`              | An endpoint added without documenting it |
| The Go error and health structs match the contract's schemas           | `backend/…/httpx/conformance_test.go`           | A renamed field on the producing side    |
| The Zod schemas match the contract's schemas                           | `packages/shared-schemas/test/contract.test.ts` | A renamed field on the consuming side    |
| The committed client matches what the spec generates                   | CI, `api-contract` job                          | A contract change with a stale client    |

Plus `redocly lint` on the document itself, so a missing `operationId` or an undescribed
response fails before it reaches a generator.

The second row is the one people forget. A path left in the document after the endpoint was
renamed generates a client method that 404s — and the generated client compiles perfectly
while doing it.

## 8. Departures from the plan

**`openapi-typescript` rather than `orval`.** The plan offered either. orval generates
TanStack Query hooks per endpoint, which sounds like more value and is, in a project whose
error handling is uninteresting. Ours is not: bilingual messages, a correlation ID that has
to reach the screen, a retry rule that turns on the difference between a read and a
clinical write. That layer is hand-written and tested once in `@dthcms/api-client`; the
generator supplies types, which is the part no human should be writing.

**`packages/shared-schemas` carries runtime parsers alongside the generated types.** Types
are erased at runtime. A deployed backend that renames a field ships happily past `tsc` and
surfaces as `undefined` in a table cell, three screens from the cause. Anything a clinician
reads a number off is parsed, not cast.

**The documentation page inlines its own JavaScript.** Redocly's output links Redoc from a
CDN and fonts from Google. Both are dropped by `scripts/build-api-docs.mjs`, for the reason
CP10 self-hosts the application's fonts: a reference page that fails when a third-party
host is unreachable fails on the day it is needed.

## 9. Open decisions

| Decision                                | Default taken                                      | Needs                                      |
| --------------------------------------- | -------------------------------------------------- | ------------------------------------------ |
| API versioning strategy                 | URL-prefixed `/v1`, additive-only within a version | Confirmed by the plan's own recommendation |
| Idempotency-key retention window        | Not yet chosen — decided with the store at CP24    | Amlan                                      |
| Deployed server origins in the contract | Only `localhost` is declared; hosting is deferred  | D-01                                       |

## 10. Carried forward

| Item                                                         | Blocked by              | Lands at |
| ------------------------------------------------------------ | ----------------------- | -------- |
| Server-side idempotency store and replay                     | A clinical write exists | CP24     |
| Rate limiting, and the `Retry-After` it documents            | API hardening           | CP49     |
| Real server origins, and re-enabling `no-server-example.com` | D-01                    | CP03     |
| Contract published somewhere the team can browse             | Hosting                 | CP03     |
