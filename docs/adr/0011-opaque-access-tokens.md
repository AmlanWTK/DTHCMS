# ADR-0011 · The access token is opaque, not signed

- **Status:** Accepted
- **Date:** 2026-09-02
- **Checkpoint:** CP16
- **Amends:** D-44, ratified at CP14 (1 September 2026)
- **Supersedes:** —

## Context

D-44 was ratified two days ago as "a short-lived **signed** access token; an opaque,
rotating, revocable refresh token bound to the device". Writing CP16 surfaced a tension
between the first half of that sentence and one of the checkpoint's own acceptance
criteria.

CP16 requires a **server-side session registry** and states, as criterion 3, that
**session revocation takes effect within one request**. Those two together mean the server
consults the registry on every authenticated request. There is no arrangement in which it
does not: a signature proves a token was issued, and says nothing about whether it has
since been revoked, and "within one request" leaves no window for a cache that can be
stale.

So the signed token is verified twice — once cryptographically, once against the registry —
and only the second verification can refuse a revoked session. **A token whose validity is
checked statefully on every request is stateless in name only.** The signature is doing no
work that the lookup is not already doing.

The reason to accept that cost is usually federation: several services verifying a token
without asking a central authority. ADR-0002 makes this a modular monolith, and the one
component that might separate — the realtime gateway — is explicitly to be _measured_
before it is committed to (D-30). We would be paying today for a split that may not happen,
with a mechanism that does not currently reduce a single database round trip.

## Decision

**The access token is 32 bytes of cryptographic randomness, sent to the client as URL-safe
base64, and stored server-side only as its SHA-256 digest.** Authentication is a lookup of
that digest in the session registry.

The refresh token keeps the shape D-44 gave it: opaque, rotating, revocable, with reuse
detection over a token family, and bound to a device once CP18 exists.

No JWT library enters the dependency tree.

SHA-256 rather than argon2id for the token digest is deliberate and is not an
inconsistency: a password is low-entropy and must be expensive to guess, while a 256-bit
random token cannot be guessed at all, so a slow hash buys nothing and costs a round trip's
latency on every request the clinic makes.

## Consequences

**What improves.**

Revocation becomes correct by construction rather than correct because we remembered to
check as well. There is exactly one place that decides whether a session is live, and no
second path that could disagree with it.

An entire family of failures leaves the project before it arrives: algorithm confusion,
`alg: none`, unvalidated `kid`, signing-key rotation, clock skew between issuer and
verifier, claims that were true when signed and false when read. None of these can occur
against a random string looked up in a table.

A leak of the session table yields nothing usable, because it holds digests.

**What it costs.**

One indexed lookup per authenticated request. That is the whole cost as shipped: CP16 does
the lookup against PostgreSQL directly, and a Redis hot cache is deliberately _not_ in front
of it until a measurement says the lookup is a problem (the D-30 discipline). If one is
added, the rule it must obey is fixed now: the cache may serve a _live_ session but never
satisfy a revoked one, so a revocation deletes the cache entry before it writes the database.

The token cannot be inspected offline. Nothing in this system inspects tokens offline.

If DTHCMS is ever genuinely split into services that must authenticate independently, this
becomes a decision to revisit — and the revisit is contained, because the token is opaque
to every client already. Changing what the server stores behind it changes no client.

**What CP17 and CP18 need from it, and get.**

CP17's step-up authentication needs a session to carry "this session has completed a second
factor, at this time". A row can hold that and be updated when the step-up happens. A
signed token could not be updated without reissuing it.

CP18's device binding needs the session to name the device that holds it, and needs
revoking a device to end its sessions at once. Both are columns and a query.

## Compliance

`docs/implementation-plan.md` §3 records D-44 as ratified with the original wording and
this amendment beside it. The register keeps the history rather than being rewritten: what
was decided at CP14, and what changed at CP16 when the code met the criterion.
