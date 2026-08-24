# ADR-0010 · Session tokens never live in web storage

- **Status:** Accepted
- **Date:** 2026-08-23
- **Checkpoint:** CP10 (decided), CP16 (enforced by the authentication it constrains)
- **Supersedes:** —

## Context

CP16 will issue a session credential. Where the browser keeps it is a decision that has
to be made before the code that keeps it is written, because every screen built in the
meantime is written against one answer or the other.

The two candidates are `localStorage` (or `sessionStorage`) read by JavaScript and
attached to each request as a header, or an `httpOnly` cookie the browser attaches on its
own and JavaScript cannot read.

The application is a clinical record. It renders patient names, free-text notes, uploaded
document text and, from CP96, OCR output from photographs of paper the clinic did not
write. Every one of those is a place a script tag can arrive from outside.

## Decision

**Session credentials are held in `httpOnly`, `Secure`, `SameSite=Lax` cookies. Nothing in
`web/` reads or writes `localStorage`, `sessionStorage` or `document.cookie` for anything
to do with a session.**

An ESLint rule in the repository root fails the build on any use of those APIs under
`web/src`, so this is a broken build rather than a code-review comment somebody misses at
two in the morning.

## Consequences

The reason for the choice is narrow and worth stating plainly: **cross-site scripting.**
A token in `localStorage` is readable by any script that runs on the page, so a single XSS
becomes credential theft, and the stolen credential works from the attacker's own machine
for as long as it is valid. The same XSS against an `httpOnly` cookie is still serious —
the attacker can act as the user _in that page_ — but they cannot take the credential
away with them, and closing the hole ends the access. That difference is the whole
decision.

Three consequences follow, none of them free:

**CSRF becomes our problem instead.** A cookie the browser attaches automatically is
attached on requests the user did not intend. `SameSite=Lax` covers most of it; CP16 adds
a token on state-changing requests. This is a well-understood problem with a standard
answer, which the token-theft problem is not.

**The API must be same-origin, or behind the same proxy.** A cookie will not travel to a
different origin without CORS credentials and a `SameSite=None` relaxation that undoes the
protection. CP03 therefore has to put the web application and the API behind one hostname.
That constraint is recorded here because it will look arbitrary to whoever configures it.

**Nothing about the session survives a reload in the client.** The Zustand session store
is deliberately not persisted; on reload the application asks the server who it is
talking to. That is one extra request, and it is also what makes revocation work at all.

The rule is about session credentials, not about all storage. A remembered filter, a
collapsed panel, an unsent draft — those may use storage when a screen needs them, and the
ESLint rule is the place to record such an exception, one call site at a time, with a
comment saying why it is not a credential.

## Alternatives considered

**A token in memory only, refreshed silently.** Genuinely strong against theft, and it is
what a single-page application with no server rendering would do. Rejected because this
application renders on the server: the first request for a page carries no JavaScript
state, so a memory-only token means every load starts unauthenticated and flashes a login
screen. A clinic tablet reloading between patients would do that all day.

**A token in `localStorage` with a short lifetime.** The usual mitigation, and it reduces
the window without closing it. Rejected because the window it leaves open is measured
against an attacker who is already running code on the page and can simply wait for the
next refresh.
