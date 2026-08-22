# ADR-0005: Implement staff authentication in-house

- **Status:** Accepted
- **Date:** 2026-08-22
- **Blueprint reference:** §15.1 (RBAC + 2FA), §4.2 (device and identity model), R-02, R-03
- **Related decision:** D-43

## Context

DTHCMS authenticates clinic staff — on the order of tens to low hundreds of accounts, all
created by an administrator. It never authenticates patients or the public.

Its identity requirements are unusual in three ways:

1. **Device-bound sessions.** Every clinical event carries a `device_id` that must be
   trustworthy, not self-declared (R-03).
2. **In-session role switching.** One operator may hold several station roles at once and
   switch between them without logging out (R-02), and each event records the role that
   was active at the moment of writing.
3. **Attribution is a clinical and medico-legal requirement**, not a convenience.

Identity providers are built for a different problem: many users, self-service signup,
federated identity, consumer recovery flows. Every one of them would need custom claims,
a local user mirror, and custom device binding to meet the above — and would place
credentials outside whatever residency boundary D-01 settles on.

## Decision

Implement staff authentication inside the backend: argon2id password hashing, TOTP second
factor, short-lived signed access tokens, opaque rotating refresh tokens with reuse
detection, a server-side session registry, and server-issued device credentials.

**Conditions, non-negotiable:** no custom cryptography anywhere; only mature, maintained
libraries for primitives; and an explicit external security review of this code at CP94
before any real patient data exists.

## Alternatives considered

**Google Identity Platform / Firebase Auth.** Natural alongside GCP, and would still need
custom claims, a local mirror and custom device binding. Adds a dependency and a data
residency question to the most sensitive data in the system.

**Keycloak / Ory / Zitadel self-hosted.** Powerful and standards-based; substantial
operational surface for a two-person team, and still requires the same custom work.

**Auth0 / Clerk.** Excellent developer experience, priced for consumer scale, and puts
staff credentials in a third party outside Bangladesh.

## Consequences

**Good**

- Device binding, role switching and per-event attribution are designed together rather
  than bolted onto a model that did not anticipate them.
- Credentials stay inside whatever hosting boundary D-01 settles on.
- No third-party outage can stop the clinic logging in.

**Bad — and we accept these knowingly**

- **We own the security of our own authentication.** This is the strongest argument against
  the decision, and the reason for the mandatory external review at CP94 and again at CP156.
- No free SSO, no free social login, no vendor security team watching for us.
- Password reset is administrator-mediated by design, which is safe but requires two
  administrators to exist so nobody can be locked out (D-70).

**Revisit when** DTHCMS needs to authenticate anyone other than clinic staff — patients,
partner organisations, or federated hospital identities. That is a different problem, and
an identity provider would probably win it.
