# Realtime on the client

CP27. How web and mobile consume the gateway (`docs/realtime.md`) without producing the
stale-cache bugs the plan warns about. ADR-0019 records the decisions.

## 1. One rule

**A message invalidates a query key. It never writes to the cache.**

`realtimeInvalidations(message)` returns keys, and `@dthcms/api-client/realtime-keys`
exports no function that returns data. That is not stylistic. A value written into the
cache from a socket is produced by a second path — one that skips the endpoint's
authorisation, its serialiser and its field-level redaction — and on the day the two paths
disagree, a clinician reads a number no endpoint ever returned and no log can explain.

Invalidating costs a request. That request is the safe one.

A test asserts the discipline directly: it enumerates the module's exports and fails if
anything but the three key functions appears, and the provider test spies on
`setQueryData` and requires that it is never called.

## 2. The client

`createRealtimeClient` in `@dthcms/api-client` is shared. Two clients would drift, and the
symptom is "the tablet updates and the dashboard does not", which takes a day to find.

| Concern       | What it does                                                                            |
| ------------- | --------------------------------------------------------------------------------------- |
| Reconnect     | Exponential backoff, 1s → 30s, ×2, with 30% downward jitter                             |
| Status        | `idle` → `connecting` → `live`, then `reconnecting`, then `offline` after five failures |
| Subscriptions | Reference-counted per topic; re-sent in full on every reconnect                         |
| Cursor        | The highest `seq` seen, kept across reconnects                                          |
| Gaps          | Reported to the application, which refetches — the gateway does not replay              |

The jitter is not decoration. Thirty tablets that lost the same access point reconnect in
the same millisecond without it, and knock the gateway over on the way back up.

The socket is injectable, because `WebSocket` is a browser global, React Native's is a
different implementation with a third parameter, and the tests need neither.

## 3. Subscriptions belong to screens

```tsx
function PatientScreen({ id }: { id: string }) {
  useRealtimeTopics([`patient:${id}`]);
  // …
}
```

Subscribed on mount, released on unmount. A clinic's sockets then carry what is on the
screens in front of people and nothing else — which is also what keeps the gateway's
per-message RBAC work proportional to what is actually being watched.

Reference counting means two components watching one patient share one subscription, and
it is dropped when the second of them unmounts. The hook sorts and joins its topics, so a
call site passing a fresh array literal on every render does not resubscribe on every
render.

## 4. What a gap means

The gateway holds no store of past messages (ADR-0018). So "data missed while disconnected
is recovered on reconnect" is implemented as: notice the gap, refetch what this client is
watching.

A gap is noticed two ways — a reconnect where the cursor is already non-zero, and the
gateway reporting on a message that this connection fell behind. Both call `onGap`, and
both providers respond by invalidating `gapInvalidations(client.topics())`.

Deliberately not the whole cache: invalidating everything on every wifi blip would make a
clinic's morning one long refetch storm. The topics a client holds _are_ the screens people
are looking at, which is exactly the set that has to be right.

This is why criterion 2 (recovery) and criterion 4 (no duplicates) are one mechanism rather
than two. Nothing is appended from a socket, so there is nothing to duplicate.

## 5. Credentials

| Surface | On the handshake                                                            |
| ------- | --------------------------------------------------------------------------- |
| Web     | The session cookie, attached by the browser (ADR-0010). Nothing in the URL. |
| Mobile  | `Authorization: Bearer …` plus the CP18 device signature, in headers.       |

React Native's WebSocket takes headers; a browser's does not. So the station app signs the
handshake exactly as it signs every other request, and the gateway checks it with the same
middleware. The signature carries a timestamp and a nonce, so it is minted immediately
before each connection — including each reconnect — and a stale one is refused, correctly.

Nothing goes in the query string on either surface. A token in a URL is a token in an
access log, in a `Referer` header, and in the browser's history.

**A token refresh mid-connection needs no special handling.** The gateway re-resolves the
subject on its own timer, so a refreshed token, a switched role or a revoked grant takes
effect on the connection that is already open. If the subject can no longer be resolved at
all, the gateway sends `reauthentication_failed` and the client reconnects, which re-runs
the whole handshake with whatever credential the app now holds.

## 6. Background and foreground (mobile)

`connectionAction(previous, next)` is the policy, and it is a pure function so it can be
tested without a device:

| Transition              | Action     | Why                                                                                                                                                       |
| ----------------------- | ---------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| → `background`          | disconnect | The OS will drop a suspended process's socket anyway, and a socket the app believes is open while the OS has killed it is how a screen goes quietly stale |
| `background` → `active` | resume     | The operator has just picked the tablet up and is about to act on what is on it; waiting out a 30-second backoff is wrong                                 |
| ↔ `inactive`            | none       | On iOS that is a notification banner, an incoming call, the app switcher — reconnecting for a banner means reconnecting several times a minute            |

Resuming re-signs the handshake before reconnecting, then reports a gap, so the screens
refetch. The web has the same shape on `visibilitychange` and `online`.

## 7. What the operator sees

Three states in the top bar, and the quietest is the normal one:

| State          | Appearance                                                                                                              |
| -------------- | ----------------------------------------------------------------------------------------------------------------------- |
| `live`         | A filled green dot and the word. Nothing else — "working normally" must not compete with a blood pressure for attention |
| `reconnecting` | An amber dot that pulses, and the word                                                                                  |
| `offline`      | A **hollow** ring, the word in bold, and a banner on the screen                                                         |

The shape changes as well as the colour, for the same reason the clinical statuses carry an
icon: under deuteranopia amber and orange converge, and on the clinic's monochrome printer
colour carries nothing at all. `prefers-reduced-motion` stops the pulse.

It is deliberately not a `StatusPill`. Those seven tones mean something specific about a
measurement, and spending one on a network condition is how a status vocabulary stops being
a vocabulary — the same reasoning the offline banner already records.

The banner appears only at `offline`, not at `reconnecting`: a banner that flashes on every
blip is a banner people learn to scroll past. What it says is narrow — the screen may be
behind — and it does not claim anything about whether writes will succeed, because they may
well: the API is a different connection.

## 8. Acceptance criteria, and where each is proven

| Criterion                                               | Test                                                                                                                                                     |
| ------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (1) Reconnection is automatic, with exponential backoff | `reconnects by itself, backing off further each time`; `backoffDelay` spread                                                                             |
| (2) Data missed while disconnected is recovered         | `reports a gap on reconnect and asks the gateway where it stands`; `refetches what it is watching after a reconnect`                                     |
| (3) Connection status is visible                        | `the indicator` suite; `e2e/realtime.spec.ts`; the screenshots in this checkpoint's report                                                               |
| (4) No duplicate rows after reconnection                | `passes each message to the application exactly once`; `never writes the message into the cache` — nothing is appended, so there is nothing to duplicate |
| Invalidation correctness                                | `packages/api-client/test/realtime-keys.test.ts` (11 cases)                                                                                              |
| Subscription lifecycle                                  | `subscribes when the screen appears and unsubscribes when it goes`; `moves the subscription when the operator opens a different patient`                 |
| Background/foreground                                   | `mobile/test/realtime-handshake.test.ts` — the policy as a pure function                                                                                 |
| Token refresh mid-connection                            | `reconnects when the session changed under the connection`                                                                                               |

## 9. Manual verification

The plan's check: open the dashboard, disconnect wifi for thirty seconds, reconnect.

```
Live  →  Reconnecting  →  Live
```

and the screen catches up by itself, with no duplicate rows and no stale values. What makes
that true is §4: on reconnect the client refetches what it is watching, so what appears is
what the API returns, not what a socket replayed.

Past about fifteen seconds of failure the indicator changes to **Not live** and a banner
appears. That is the honest state, and it is the one an operator most needs to be told
about.

## 10. Open

- **Nothing publishes yet.** No route appends a clinical event until CP29, so the channel
  carries nothing in a real deployment. Every path here is exercised against a socket
  double and, for the gateway, against a real Chromium client on the Go side.
- **The mobile provider has no automated test**, for the reason `mobile/vitest.config.mts`
  records: rendering React Native outside a device proves nothing a device would not
  disprove. The two decisions inside it — the handshake credential and the app-state policy
  — were lifted into `realtime-handshake.ts` precisely so they could be tested. The rest is
  the Maestro flow's job, on hardware, when D-59 names the device.
- **Optimistic UI for offline writes** is CP66 and explicitly out of scope here.
