# The realtime gateway

CP26. The thing that makes §4.1's sentence true: _the junior doctor's screen updates
instantly — no refresh._ ADR-0018 records the decisions; `docs/event-store.md` is where the
facts come from and `docs/projections.md` is what the screens read.

## 1. The shape of it

```
a write commits  →  the publisher turns the event into a Message
                 →  Redis pub/sub, so every instance sees it
                 →  each instance fans it out to its own connections
                 →  each connection's RBAC filter decides, per message, before the socket
```

Four things about that are load-bearing:

- **After commit only.** A message published inside a transaction may describe a write that
  then rolls back, and there is no un-publishing it.
- **RBAC per message, not per subscription.** A topic is a routing key, not a permission.
- **The socket is a nicety; the pull is the truth.** A dropped connection loses no data,
  because the client reconciles by reading.
- **The gateway never reads the ledger.** It relays what it is handed. A gateway that
  queried would be a second, slower, differently-permissioned read path over clinical data.

## 2. Connecting

`GET /v1/realtime`, upgraded to a WebSocket. Authentication is the ordinary chain —
`Authenticate` then `VerifyDevice` — so a socket is opened by exactly the credential an HTTP
request would need: the session cookie for the browser (ADR-0010), the bearer token and
device signature for the station app.

Nothing is read from the query string. A token in a URL is a token in an access log, in a
`Referer`, and in a browser's history.

The path is `/v1/realtime` and not the plan's `/realtime`: every other endpoint is versioned,
and a long-lived protocol needs versioning more than a request does.

**Origin.** Same-origin only, plus the configured allowlist. A WebSocket handshake is exempt
from the same-origin policy and carries cookies, so without this any page the user happens
to be reading could open a socket as them. A browser test asserts the refusal.

## 3. Topics

Four kinds, and the list is closed:

| Topic              | What it carries                     |
| ------------------ | ----------------------------------- |
| `patient:{uuid}`   | everything about one patient        |
| `station:{uuid}`   | everything happening at one station |
| `queue:{facility}` | the traffic board                   |
| `user:{uuid}`      | messages addressed to one person    |

A client may hold at most 200 subscriptions.

Subscribing is checked coarsely — same facility; your own user topic; a clinical read
permission held at all — and refusals are **named** rather than silent. A subscription that
quietly delivered nothing would be the hardest kind of bug to find from the client's side,
and an enumerable one: a client could learn which users exist by watching which topics error.

## 4. The protocol

Commands, as JSON text frames:

```json
{"type": "subscribe",   "topics": ["patient:0190a8f2-…"]}
{"type": "unsubscribe", "topics": ["patient:0190a8f2-…"]}
{"type": "resume",      "since": 4412}
{"type": "ping"}
```

Envelopes, back:

| `type`         | Meaning                                                            |
| -------------- | ------------------------------------------------------------------ |
| `welcome`      | Connected. Carries `cursor`.                                       |
| `subscribed`   | The topics that took effect.                                       |
| `refused`      | The topics your role may not watch, named.                         |
| `unsubscribed` | The topics removed.                                                |
| `message`      | A notification. Carries `seq`, `topic`, `kind`, ids and a summary. |
| `resumed`      | `cursor` and `dropped` — fetch everything after `cursor` by HTTP.  |
| `pong`         | An answer to `ping`.                                               |
| `error`        | `malformed_command`, `unknown_command`, `reauthentication_failed`. |
| `closing`      | The instance is shutting down. Reconnect.                          |

A `message` envelope carries `dropped` when this connection missed messages by being too
slow. Non-zero is the client's instruction to reconcile by pull.

**`resume` does not replay.** The gateway holds no store of past messages: inventing one
would be a second copy of the ledger with different access rules and different retention.
What `resume` returns is precisely what the client must fetch over HTTP, which is why
criterion 3 (reconnect without loss or duplicates) and criterion 4 (a dropped socket never
loses data) are one mechanism rather than two that can disagree.

## 5. Who sees what

The filter asks `rbac.Can` — the same engine that decides an HTTP request — with three facts:

- the **facility**, checked before anything else;
- **sensitivity**, so a blinded role (registration, pharmacy) is refused a diagnosis whatever
  its permissions say (§4.4);
- **the station the write happened at**, which is what a station-scoped role's reach is
  measured against. An anthropometry officer receives what was recorded at anthropometry; a
  physician, whose reach is the clinic, receives it wherever it was recorded.

The permission asked for is the message's own `requires`, so a new kind of message states
its own requirement and nothing in the gateway holds a list of kinds.

**A connection outlives its token**, so the subject is resolved again every minute. A role
revoked at 09:05 stops applying at the next message, not at the next reconnect.

## 6. Heartbeat, backpressure and limits

A ping every 25 seconds; the read deadline is two and a half beats, so one missed beat is
tolerated and two are not.

Each connection has a bounded queue. A full queue drops the message and counts it — it does
not block, because one slow tablet must not stall every other screen in the clinic. Nothing
is lost: the events are in the ledger.

| Limit                      | Default | On exceeding                                                     |
| -------------------------- | ------- | ---------------------------------------------------------------- |
| Connections per user       | 8       | The **oldest** is closed — the newest is the one being looked at |
| Connections per device     | 4       | The oldest is closed                                             |
| Connections per process    | 5000    | `503` with `Retry-After`                                         |
| Queued messages per socket | 256     | Dropped, counted, and reported to the client                     |

These are guesses and should be measured against a real clinic before they are trusted.

## 7. Across instances

One Redis channel, `dthcms:realtime`. Every instance subscribes and filters locally against
its own fan-out index; one channel per topic would mean thousands of subscriptions and an
instance that can miss a topic it started caring about a moment ago.

Redis pub/sub delivers nothing to a subscriber that was disconnected at the moment of
publication. That is acceptable here and only here, for the reason in §1: the ledger is the
record. The bridge re-subscribes with a backoff after a Redis restart rather than giving up.

## 8. The protocol implementation

`internal/realtime/ws` is RFC 6455's server half, written here because no module proxy is
reachable and the gateway is a named blueprint requirement (ADR-0018). It is verified three
ways, because no one of them is enough:

1. the frame layer against hand-written bytes, one case per rule, each citing its section;
2. this repository's client and server talking over a real socket;
3. **Chromium driving the gateway** — the only client the web application actually has, and
   the one that would not share a misreading of the document with the implementation.

If the proxy becomes reachable, replacing the package with `coder/websocket` and keeping
every test is the honest move: they are written against the protocol, not the implementation.

## 9. Metrics

| Metric                                | What it says                                  |
| ------------------------------------- | --------------------------------------------- |
| `dthcms_realtime_connections`         | Open connections on this instance             |
| `dthcms_realtime_connections_closed`  | Closes, by reason                             |
| `dthcms_realtime_messages_delivered`  | Messages written to a socket, by topic kind   |
| `dthcms_realtime_messages_refused`    | Messages withheld by the RBAC filter          |
| `dthcms_realtime_messages_dropped`    | Messages a subscriber was too slow to receive |
| `dthcms_realtime_connection_lifetime` | How long connections last                     |

Refused is counted apart from delivered on purpose: a refusal is the filter working, and a
sudden change in the ratio is either a permission change or a bug.

## 10. Acceptance criteria, and where each is proven

| Criterion                                                    | Test                                                                                                                              |
| ------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------- |
| (1) An update appears on subscribed clients in <1s at load   | `TestAMessageReachesEverySubscriberAndNobodyElse`, `TestTwoHundredConcurrentConnections`                                          |
| (2) A subscriber never receives what their role cannot read  | `TestANutritionistNeverReceivesAPrescription`, `TestABlindedRoleIsRefusedSensitiveMessages`, `TestAnotherFacilityReceivesNothing` |
| (3) Reconnection resumes without loss and without duplicates | `TestReconnectionResumesFromTheCursor`                                                                                            |
| (4) A dropped socket never causes data loss                  | `TestASlowClientIsDroppedFromRatherThanBlockingEveryoneElse` — dropped, counted, told                                             |
| Multi-client fan-out                                         | `TestAMessageReachesEverySubscriberAndNobodyElse`                                                                                 |
| 200 concurrent connections                                   | `TestTwoHundredConcurrentConnections`                                                                                             |
| Message ordering per topic                                   | `TestMessagesArriveInOrderOnATopic`                                                                                               |
| Redis bridging across instances                              | `TestAMessagePublishedOnOneInstanceReachesAnother`, `TestTheFilteringFactsSurviveTheJourney`                                      |
| Re-authentication on a live connection                       | `TestARevokedRoleStopsReceivingWithoutReconnecting`, `TestAnAccountThatLosesItsAccessIsDisconnected`                              |
| Connection limits                                            | `TestOneProcessRefusesMoreConnectionsThanItsCeiling`, `TestTheOldestConnectionGoesWhenOnePersonOpensTooMany`                      |
| RFC 6455 conformance                                         | `internal/realtime/ws/ws_test.go`, `TestChromiumSpeaksToTheGateway`                                                               |
| Cross-origin refusal                                         | `TestChromiumOnAnotherOriginIsRefused`                                                                                            |

## 11. Manual verification

The plan's check needs the clinical write path, which arrives at CP29. Until then, the
gateway can be exercised directly:

```bash
make up
cd backend && go run ./cmd/realtime          # listens on DTHCMS_HTTP_ADDR
# in another shell, publish by hand:
redis-cli PUBLISH dthcms:realtime '{"seq":1,"topic":"queue:<facility-uuid>","kind":"queue.changed",
  "requires":"patient.read.demographics","facility_id":"<facility-uuid>","at":"2026-09-03T04:42:00Z"}'
```

Then open the physician dashboard and a station app side by side, enter a value on the
phone, and watch it appear on the dashboard without a refresh, in under a second. That is
CP27's verification and this checkpoint's reason to exist.

## 12. Open

- **Nothing publishes yet.** No route appends a clinical event until CP29, so
  `realtime.Publisher` is the interface the write path will call after commit. CP27 wires
  the clients to the other end.
- **The connection limits are guesses** and should be measured against a real clinic.
- **Presence indicators** ("who else is looking at this patient") are explicitly out of
  scope and would need a second Redis structure; they are not free.
- **A slow rural link** may want `permessage-deflate`, which this implementation does not
  have. Measure before writing it.
