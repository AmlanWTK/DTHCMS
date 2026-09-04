# ADR-0019 · A realtime message invalidates a query key and never writes to the cache

- **Status:** Accepted
- **Date:** 2026-09-03
- **Checkpoint:** CP27
- **Supersedes:** —

## Context

CP26 built the gateway. CP27 connects web and mobile to it, and the plan's own risk note
says what to be afraid of: "subtle cache bugs — the invalidate-don't-mutate rule is the
mitigation and is enforced in review."

Review is not a mechanism. A rule that lives in a reviewer's memory holds until the
afternoon somebody is in a hurry and the reviewer is on leave, and the failure it prevents
is the kind nobody notices: a screen showing a value that is subtly wrong, indefinitely,
with no error anywhere.

Three questions needed answering: what a message is allowed to do to client state; how
"recovered on reconnect" works when the gateway does not replay; and how a station app
authenticates a socket when a browser cannot.

## Decision

### 1. A message produces query keys, and the module that maps them cannot produce anything else

`realtimeInvalidations(message)` returns `QueryKey[]`. `@dthcms/api-client/realtime-keys`
exports exactly three things — that function, `gapInvalidations`, and the `queryKeys`
factory — and none of them takes a cache or returns data.

The rule is enforced three ways, in ascending order of how much they would actually catch:

- The module has no API for writing. There is nothing to call.
- A test enumerates the module's exports and fails if a fourth appears.
- The provider test spies on `setQueryData` and asserts it is never called, delivering a
  message whose summary deliberately carries a number so that a naive implementation would
  be caught.

The cost is a request per message. That request goes through the endpoint's authorisation,
its serialiser and its field-level redaction (CP20), which is exactly what makes the result
safe to put in front of a clinician. A value that reached the screen through a socket has
been through none of them.

_Considered and rejected:_ writing the message's `summary` into the cache for the fields it
carries, invalidating only for the rest. It is faster and it is the beginning of the bug:
two paths produce the screen's state, and the day they disagree there is no log that
explains what the clinician saw.

### 2. A gap is closed by refetching what this client is watching

The gateway holds no store of past messages and does not replay (ADR-0018). So "data missed
while disconnected is recovered on reconnect" is implemented as: notice the gap, invalidate
the topics this client holds.

Two things count as a gap — a reconnect where the cursor is already non-zero, and the
gateway reporting that this connection fell behind — and both take the same path.

Not the whole cache. The topics a client holds are the screens people are looking at, which
is exactly the set that has to be right; invalidating everything on every wifi blip would
make a clinic's morning one long refetch storm on a connection that has just proven itself
unreliable.

A consequence worth naming: criterion 2 (recovery) and criterion 4 (no duplicates) become
one mechanism rather than two that can disagree. Nothing is ever appended from a socket, so
there is nothing to duplicate.

### 3. One client, in the shared package

`createRealtimeClient` lives in `@dthcms/api-client`, with an injectable socket. Two
implementations would drift, and the symptom — "the tablet updates and the dashboard does
not" — takes a day to diagnose and looks like a server bug the whole time.

Backoff is 1s → 30s, ×2, with 30% downward jitter. The jitter is the part that matters: a
clinic's tablets all lose the same access point at the same moment, and without it they all
come back in the same millisecond.

`offline` is a separate state from `reconnecting`, and the client enters it after five
failures. Reconnecting is the normal condition of clinic wifi and deserves a quiet
indicator; offline means the screen is a snapshot, and an operator about to act on it
should know.

### 4. Credentials go where the platform allows, and never in the URL

The browser sends the session cookie, which it attaches to a handshake by itself
(ADR-0010). React Native sends `Authorization` and the CP18 device signature as headers,
which its WebSocket accepts and a browser's does not.

Neither puts anything in the query string. A token in a URL is a token in an access log, a
`Referer` header and the browser's history — and a WebSocket URL is logged by every proxy
between the tablet and the clinic server.

**Token refresh mid-connection needs no client mechanism at all.** The gateway re-resolves
the subject on a timer (CP26), so a refreshed token or a changed role takes effect on the
connection that is already open. Only a subject that cannot be resolved at all produces
`reauthentication_failed`, and the client's response is to reconnect — which re-runs the
handshake with whatever credential the app now holds. The plan asks for "token refresh
mid-connection handled without dropping data"; nothing is dropped because nothing was being
carried that a refetch does not recover.

### 5. Subscriptions are reference-counted and tied to mounted components

`useRealtimeTopics(topics)` subscribes on mount and releases on unmount. Two components
watching one patient share one subscription.

This is the client half of the gateway's per-message RBAC cost: the work is proportional to
what is actually on a screen somewhere, not to what a client once asked for and forgot.

### 6. The mobile provider is not unit-tested; its decisions are

`mobile/vitest.config.mts` already records the rule — rendering React Native outside a
device proves nothing a device would not disprove — for the i18n provider and the
connectivity hook. The realtime provider joins them.

What was done instead is to move the two things worth asserting out of it:
`handshakeHeaders` (which credential goes on the wire) and `connectionAction` (what a
background transition means) are pure and are tested. The React wiring around them is the
Maestro flow's job, on hardware, when D-59 names the device.

## Consequences

- Every realtime update costs one request. At a clinic's write rate that is nothing; at a
  different scale it would be worth measuring before it is worth changing.
- Adding a message kind means adding its keys to one shared map. Forgetting to leaves the
  screen correct but not refreshed early, which is a missing feature rather than a wrong
  value — the right way round for this failure to land.
- A newer gateway publishing a kind an older client has never heard of invalidates nothing
  and raises nothing. During a rolling deploy that is the normal state of the world.
- The station app must re-sign the handshake on every reconnect, so a device whose key has
  been revoked stops connecting within one backoff.
- **Open:** nothing publishes yet. The whole channel is exercised against a socket double
  on the client and against a real Chromium client on the gateway; the first end-to-end
  clinical message arrives at CP29.

## Alternatives considered

- **Mutating the cache from messages, with invalidation as a fallback.** The performance
  argument is real and the safety argument wins: see §1.
- **A single "something changed" signal with no kinds, invalidating everything.** Simplest
  possible client, and it turns every measurement recorded anywhere in the clinic into a
  full refetch on every open screen. The gateway already knows what changed; throwing that
  away to save a lookup table is a poor trade.
- **A Redis stream the client can replay from.** Would make `resume` a real replay and the
  gateway a second, differently-permissioned copy of the ledger. Rejected at CP26 for the
  same reason and the reasoning is unchanged here.
- **Subscribing to everything a role may see, once, at sign-in.** Fewer subscribe commands
  and far more fan-out work, most of it for screens nobody is looking at.
