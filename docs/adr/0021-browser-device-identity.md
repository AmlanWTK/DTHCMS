# ADR-0021 · A browser session names the workstation it was opened at, and does not authenticate as one

- **Status:** **Proposed** — needs Dr Nahid's decision before the registration desk can use the web
- **Date:** 2026-09-03
- **Checkpoint:** raised at CP32; the decision is D-71
- **Deciders:** Dr. K. M. Nahid Ul Haque, Amlan Sarkar
- **Blueprint reference:** [R-03], §15.2
- **Related decisions:** D-46 (a clinical write must name its device) · ADR-0010 (no session tokens in web storage) · ADR-0013 (a device's key lives in secure storage)

## Context

CP29 built patient registration and CP32 built the registration desk. The desk cannot use it.

`eventstore.ActorFrom` refuses to build an attribution envelope without a `device_id`, because
[R-03] and D-46 say a clinical write is evidence and evidence names the machine it came from.
A tablet supplies one: it holds an Ed25519 key in Android's Keystore and signs every request
(CP18, ADR-0013). A browser cannot. Anywhere a browser could keep a private key —
localStorage, IndexedDB, a cookie — is reachable by any script that gets onto the page, which
is the reason ADR-0010 already forbids keeping a session token there. A key that an XSS can
steal is not a device identity; it is a second session token with a longer life.

So today a registration from the web is refused with `DEVICE_REQUIRED`. That is the correct
behaviour and it is also a dead end: registration involves more typing than any other station,
which is exactly why CP32 made the web its primary surface.

The question is not "how does a browser authenticate as a device". It is **what is the
`device_id` on a clinical event actually for**, and what would satisfy that honestly.

Reading the requirement: it is there so that a year later somebody can ask "which machine in
this clinic produced this record", to bound an incident to a set of machines, and to make a
revoked tablet's queued events quarantinable. It is _attribution_, and it is corroborating —
the event already names the person, their role, their station and their session.

## Decision (proposed)

A browser session is **bound at sign-in to an enrolled workstation, named rather than proven.**

1. An administrator enrols each desk's computer as a `desktop` device, exactly as they enrol a
   tablet, and the enrolment produces a short **workstation code** — `FRD-REG-1` — which is
   printed and stuck to the monitor. It is not a secret and is not treated as one.
2. The sign-in form carries a workstation field. The server resolves the code to an active
   device in the caller's facility and records it on the session row, which is server-side and
   already has a nullable `device_id` from CP18.
3. Every event from that session names that device, through the same `ActorFrom` path as a
   tablet's. No code changes in the patient module, the ledger, or anything downstream.
4. The browser stores the code — not a token — so the desk types it once. A code in web
   storage is a label, not a credential; ADR-0010's rule is unaffected.
5. `core.device_event` records the binding, so "which sessions claimed to be at FRD-REG-1" is
   answerable.

**The honesty this rests on:** the workstation is _named_, not _authenticated_. Somebody who
types another desk's code produces an event attributed to the wrong machine. That is stated
here rather than glossed, and it is why the code identifies a room rather than granting
anything: it carries no privilege, opens no door, and every event it appears on also names the
person, whose credential _is_ authenticated.

## Alternatives considered

**Keep refusing.** Honest, and it makes the registration desk unusable on the surface built for
it. Rejected as a permanent answer; it is the current state only because no decision has been
taken.

**A key in IndexedDB, non-extractable via WebCrypto.** A non-extractable key genuinely cannot
be read out, so this is better than it first appears. Rejected for two reasons: any script on
the page can still _use_ it to sign, so it authenticates the browser profile rather than the
machine; and it is silently destroyed by clearing site data, which would take a registration
desk down mid-clinic with no diagnosable cause.

**Client certificates.** Real device authentication, and the right answer for a bank. Rejected
as disproportionate: it needs a certificate authority, per-machine provisioning, and a renewal
process, for a twelve-station clinic whose threat model is a mistyped code rather than a
forged one.

**A small signed agent on each desktop.** Correct, and the upgrade path if this ever matters.
Rejected for now as a whole piece of software to build, ship and update for one field on an
event.

**Record no device for browser sessions.** The smallest change, and it quietly weakens [R-03]
for the station that produces the most records. Rejected: an absent `device_id` and a possibly
wrong one are different, and only one of them is visible.

## Consequences

**Good**

- The registration desk works on the surface built for it.
- Nothing downstream changes: one attribution path, one envelope, one set of tests.
- No key material in a browser, so ADR-0010 and ADR-0013 stay intact and unqualified.
- "Which machine produced this record" is answerable for web sessions, as it already is for
  tablets.

**Bad — and we accept these knowingly**

- A mistyped or borrowed code misattributes the machine. The person is still correct, and the
  audit trail records both.
- The workstation code is one more thing to enrol and one more thing to replace when a desk's
  computer is swapped.
- `device_id` now means two different strengths of claim — proven on a tablet, asserted in a
  browser. Anything that reasons about it must know which, so the session records how it was
  established.

**Revisit when**

- A clinical write from a browser becomes something with legal weight on its own — a signed
  prescription (CP58) is the obvious candidate, and it should keep the device requirement it
  has rather than inherit this one.
- The clinic runs more than one site, where a code printed in Faridpur being typed in Dhaka is
  no longer a hypothetical.
- Anything makes a signed workstation agent cheap — a managed fleet, an existing endpoint
  agent — at which point this becomes the fallback rather than the mechanism.

## What is needed to accept this

One decision from Dr Nahid: **is a named workstation enough attribution for a browser-entered
clinical record, given that the person is authenticated and the machine is not?** If yes, this
is roughly a day's work across auth, the sign-in screen and the device console. If no, the
registration desk stays on tablets and CP32's web form is used only where the session already
comes from an enrolled desktop.
