# ADR-0018 · RFC 6455 in-house; realtime is a notification, not a second read path

- **Status:** Accepted
- **Date:** 2026-09-03
- **Checkpoint:** CP26
- **Supersedes:** —

## Context

§4.1 names the realtime gateway as a blueprint requirement and gives its acceptance test in
one sentence: "the junior doctor's screen updates instantly — no refresh". CP26 asks for a
WebSocket gateway with an authenticated handshake, four subscription topics, RBAC-filtered
fan-out, Redis pub/sub across instances, heartbeat, resume, backpressure and metrics.

The plan names `coder/websocket` as the technology. No module proxy is reachable from this
environment, so no dependency can be added. That is an accident of the environment, but it
forced a decision that would otherwise have been made by default, and the decision is worth
recording either way.

Three questions had to be answered: whether to build the gateway at all without a WebSocket
library; what a realtime message actually carries; and where the RBAC decision is made.

## Decision

### 1. The server half of RFC 6455 is implemented in `internal/realtime/ws`

The choice was between not building a named blueprint requirement and writing the protocol.
The protocol was written.

The scope is deliberately narrow: accept a handshake, read masked client frames, write
unmasked server frames, reassemble fragments, answer pings, close politely. No extensions,
no `permessage-deflate`, no subprotocol negotiation beyond echoing one the caller allows.
Everything RFC 6455 requires of a _server_ is implemented; everything it does not is not.
A client is included too, because the gateway's own tests need one that speaks the protocol
exactly.

**How it is verified**, in three layers, because no one of them is sufficient:

- `ws_test.go` drives the frame layer with bytes written by hand, one case per rule, each
  citing its section: an unmasked client frame (§5.1), a reserved bit (§5.2), a fragmented
  or oversized control frame (§5.5), an orphan continuation and interleaved messages (§5.4),
  invalid UTF-8 in a text frame (§5.6), and every close-code rule in §7.4.1. The handshake
  is checked against §1.3's own worked example.
- The client and the server halves talk to each other over a real socket.
- **Chromium drives the gateway** (`browser_test.go`). This is the layer that matters most.
  The first two were written by one person from one reading of the document, so they would
  agree about a misreading; Chromium would not, and Chromium is the client the web
  application actually has. A second browser test asserts that a page on _another_ origin
  is refused — a WebSocket handshake is exempt from the same-origin policy and carries the
  browser's cookies, so without that check any site the user is reading could open a socket
  as them.

Autobahn is the usual proof of a WebSocket implementation and cannot run here. If the module
proxy becomes reachable, the honest move is to replace this package with `coder/websocket`
and keep every test above: they are written against the protocol, not against the
implementation, and would pass unchanged.

### 2. A realtime message is a notification, and the pull is the truth

`realtime.Message` is not `eventstore.Event`, and the gateway may not import the ledger.
A message carries the topic, a dotted kind, the identifiers needed to fetch the thing, a
small non-identifying summary, and the ledger's `global_seq` as a cursor. It never carries a
clinical value or a name.

Two reasons, and the second is the one that matters:

- A full clinical payload on a socket is a second copy of the record travelling over a
  channel with its own access rules, its own logs and its own bugs.
- **A dropped connection must never lose data** (criterion 4). That is only true if the
  socket is a nicety and the client reconciles by reading. So the gateway holds no store of
  past messages and does not replay: `resume` returns the cursor and tells the client to
  fetch everything after it over HTTP. Criterion 3 and criterion 4 are then the same
  mechanism rather than two that can disagree.

The same reasoning decides backpressure. A queue per connection, bounded; a full queue drops
the message, counts it, and tells the client on the next envelope that fits. A blocking send
would let one slow tablet stall every other screen in the clinic, and an unbounded queue is
a memory leak with a client attached.

### 3. RBAC is decided per message, immediately before the bytes reach the socket

A topic is a routing key, not a permission. A nutritionist and a physician may both be
watching `patient:{id}` and must see different things. So the filter runs per connection, per
message, with the same engine that decides an HTTP request (CP20) — one set of rules asked
twice, rather than two sets that drift.

Three facts make the resource: the facility, the message's sensitivity, and **the station the
write happened at**. The last is why `Message.Station` exists. A station-scoped role — an
anthropometry officer, a pharmacist — reaches what is at the station it is working, and the
message is the only thing that knows which that was.

Subscribing is a coarser check on top (`rbac.Holds`, which ignores scope), and both are
needed. Per-message filtering already makes a wrong subscription harmless, but a subscription
is also an observable: a `user:{someone else}` subscription that silently delivered nothing
would still let a client enumerate which users exist by watching which ones error.

`rbac.Holds` is new and deliberately weak. It exists for this one case — deciding whether a
subscription may be opened, where there is no resource yet to measure a scope against — and
says so in its own doc comment. Anywhere a resource exists, `Can` is the answer.

**A connection outlives its token**, so the subject is resolved again on a timer. Without it a
role revoked at 09:05 would keep receiving what it was entitled to at 09:00 until the socket
dropped, which is the exact failure the "revoked within one request" rule (CP16) exists to
prevent on the HTTP side.

### 4. One Redis channel, and publication after commit only

Every instance subscribes to one channel and filters locally against its own fan-out index.
One channel per topic would mean thousands of Redis subscriptions and an instance that can
miss a topic it started caring about a moment ago.

Redis pub/sub delivers nothing to a subscriber that was disconnected at the moment of
publication. That is acceptable here **and only here**, because of decision 2: the ledger is
the record and the client reconciles by pull. Nothing in the bridge is allowed to become the
thing a clinical guarantee rests on.

Publication happens after commit. A message published from inside a transaction is a message
that may describe a write which then rolls back, and there is no un-publishing it.

### 5. The gateway is its own binary, at `/v1/realtime`

A WebSocket lives for hours; a process holding thousands of them has a memory and
file-descriptor profile nothing like a request/response server's. Deployed together, every
API restart would drop every screen and every connection leak would become an API outage.

It does not use `httpx.NewRouter`: that chain wraps every request in a request timeout and a
body limit, both exactly wrong for a connection meant to last hours and carry no body. It
keeps everything that makes a request traceable and safe — panic recovery, request id,
access log, security headers, the origin allowlist — and the same `Authenticate` and
`VerifyDevice` middleware every other endpoint uses.

The path is `/v1/realtime`, not the plan's `/realtime`. Every other endpoint is versioned and
a long-lived protocol needs versioning more than a request does: a client that reconnects
after a deployment must be able to tell that the shape of what it receives has changed.
`wss` versus `ws` is the ingress's business.

### 6. `_test.go` files are exempt from the module dependency allowlist

Surfaced by this checkpoint and general. `realtime` may not import `auth`, and its RBAC
filter is meaningless unless a test can build a subject holding real roles and ask for real
permissions; a test asserting against invented strings asserts nothing.

The exemption is safe because a test-only import cannot become a production dependency by
accident: the moment a non-test file uses the imported package, that file must import it too,
and _that_ import is checked. `TestArchStillCatchesProductionCodeBesideAnExemptTest` holds
the other half. `docs/architecture-boundaries.md` records it.

## Consequences

- DTHCMS owns a WebSocket implementation. It is ~600 lines, has no dependencies, and is
  tested against the RFC and against a real browser — but it is ours to maintain, and an
  extension somebody wants later (compression, for a slow rural link) is ours to write.
- The web and mobile clients must reconcile by pull after any gap. That is CP27's work and
  the contract is already stated: `resume` returns a cursor and says so.
- A new kind of message states its own required permission. Nothing in the gateway holds a
  list of message kinds, so adding one is a publisher change and not a gateway change.
- `realtime` is a third deployment unit alongside `api` and `projector`.
- **Open:** the publisher does not exist yet. Nothing appends clinical events until CP29, so
  nothing publishes; `realtime.Publisher` is the interface the write path will call after
  commit, and CP27 wires the clients to the other end.
- **Open:** connection limits (8 per user, 4 per device, 5000 per process) are guesses. They
  are configuration in everything but name and should be measured against a real clinic
  before they are trusted.

## Alternatives considered

- **Server-sent events instead of WebSocket.** One-directional, no framing to implement, and
  it works through every proxy. Rejected because subscriptions are a conversation — a client
  changes what it is watching as the user moves between patients — and doing that over a
  second channel is a WebSocket with extra steps. It remains the sensible fallback if a
  clinic's network turns out to break WebSocket.
- **Long polling.** Works everywhere and cannot meet "under a second" at any reasonable
  request rate.
- **Filtering at subscription time rather than per message.** Cheaper, and wrong: two roles
  watching the same patient must see different things, and a permission revoked mid-session
  must take effect before the next message rather than at reconnect.
- **Replaying missed messages from a Redis stream.** Would make `resume` a real replay and
  the gateway a second, differently-permissioned copy of the ledger — with its own retention,
  its own access rules and its own opportunity to disagree with the record. The pull is
  already correct; making it cheap is not worth making it a second source of truth.
