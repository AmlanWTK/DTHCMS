# Signing a prescription (CP84)

**Status:** authored by Amlan under Dr Nahid's standing delegation, 14 Sep 2026. Two of the
decisions below are **his and his counsel's**, and are marked. The rest are engineering decisions
taken because the checkpoint cannot be built without them.

**Does not resolve D-04.** That is the medico-legal status of a digital signature in Bangladesh
and it is counsel's answer, not mine. What this document does is separate the part that does not
depend on D-04 — which is nearly all of it — from the part that does.

---

## 1. What D-04 does and does not gate

The cryptography is the same whichever way counsel answers. A canonical payload, a signature over
it, a key that the application cannot read, verification that fails on any alteration — none of
that changes.

What D-04 decides is **whether the key must also be backed by a CA-issued qualified certificate**,
so that the signature is a *legally recognised* electronic signature rather than a cryptographic
fact the clinic can demonstrate. That is an addition, not a redesign: it changes where the key
lives and what accompanies it, and the design below keeps that upgrade path open by never assuming
the key is local.

So CP84 ships without D-04, and is not finished without it. The honest statement in the meantime:
**DTHCMS can prove a prescription has not been altered since it was signed, and cannot yet claim
that this satisfies Bangladeshi law.**

## 2. There is no key management service, and pretending otherwise would be the real defect

The plan says "signing via Cloud KMS with a non-exportable key". There is no cloud: D-01 is open,
CP03 was deferred, and every environment is docker-compose on a laptop. A non-exportable key
requires hardware or a managed service, and DTHCMS has neither.

Three ways to handle that, and only one is honest:

- **Pretend.** Put an Ed25519 private key in the database or a config file, call it non-exportable
  in the commit message, and meet the acceptance criterion on paper. This is the option that gets
  discovered during the CP94 penetration test, or later.
- **Wait for CP03.** Correct, and it stops CP84, CP85 and CP89 — the whole prescription path —
  behind a decision that is with a lawyer.
- **Build the seam and be explicit about which side of it we are on.** Signing goes through a
  `Signer` interface with two implementations: a **local** one for development and test, and a
  **managed** one that arrives with CP03. The interface is shaped by what a KMS can do, not by
  what a file can do, so adopting the real thing is a configuration change.

**Decision: the third.** And the property that makes it safe is stated rather than assumed: the
acceptance criterion *"the signing key is non-exportable"* is **not met by the local signer and
cannot be.** The local signer's key is a file. It is refused outside development environments by
the same shape of guard that refuses a free-tier AI credential outside dev — not a flag anybody
can set, a check the service makes about itself at boot.

A prescription signed by the local signer records **which signer signed it**, so a future audit
can tell the two apart without inference. That matters more than it looks: if the pilot ever runs
before CP03, we must be able to say exactly which prescriptions carry a weaker guarantee.

## 3. What is signed

**A canonical, versioned serialisation of the prescription — never the rendered image.**

The canonical form is deterministic: the same prescription serialises to the same bytes on any
platform, in any Go version, regardless of map ordering or locale. It covers the facts that make
the prescription what it is — the patient, the visit, the prescriber, every live item with its
dose, frequency, duration and route, the captured prices, and the QA clearance that permitted
signing. It **excludes** anything cosmetic: layout, fonts, the clinic's letterhead, the language
the screen happened to be in.

Two consequences worth stating:

- **Re-rendering the same prescription in the other language does not break the signature**, which
  is right — the Bangla and English sheets are two renderings of one clinical fact.
- **The signature does not attest to how the paper looked.** If that is ever needed, it is a second
  signature over the rendered artefact, and it is CP89's problem rather than this one's.

The canonical form is **versioned**, and the version travels with the signature. A change to the
serialisation must not silently invalidate every historical prescription; verification picks the
version the signature names. This is the failure mode the plan names as the checkpoint's main risk,
and versioning is the whole answer to it.

## 4. Step-up, and the device question ADR-0021 left open

**Signing requires a step-up second factor.** That is the plan's requirement and it is right: the
signature is the one act in this system that creates a medico-legal document, and re-proving the
person at that moment is proportionate.

ADR-0021's "Revisit when" says a signed prescription *"should keep the device requirement it has
rather than inherit this one"* — meaning signing might demand a **proven** device (a tablet with a
key in secure storage) rather than a **named** workstation (a code typed at a desk).

**Decision: signing does not require a proven device. It requires step-up, and it records the
device assurance on the signature.**

The reasoning, and I want it on the record because it goes against the ADR's instinct:

- The consultant signs at his desk, on a browser. Requiring a proven device means he signs on a
  tablet, or does not sign. A rule whose effect is that the clinic's prescriptions stop being
  signed has not made them safer.
- **The assurance here does not come from the machine.** It comes from a second factor the
  consultant holds and a key the application cannot read. A proven device adds a fourth fact to a
  chain that already has three, and the fourth is the weakest of them — a tablet in a drawer is
  proven and unattended.
- Recording the assurance is what preserves the option. Every signature says whether the session
  was PROVEN or NAMED, so if counsel or the pilot says otherwise, the rule tightens with the
  evidence already collected, and the historical record can be queried rather than guessed at.

**This is one for Dr Nahid to overrule if he disagrees**, and it is cheap to reverse: one condition
at the signing door.

## 5. Immutability, and what was already true

A signed prescription is immutable by every path. CP80 already built most of this — items are
writable only in DRAFT, `prescription_item_is_frozen()` enforces it, and migration 00063 stopped
the application rewriting the state machine that decides what DRAFT means. CP83 added the
clearance gate.

CP84 adds the part that makes it *checkable by a stranger*: a signature that fails verification if
any covered byte changed. The distinction matters. The existing triggers say the database will not
let you change it; the signature says you can prove nobody did, to somebody who does not trust the
database.

Tamper detection is proven by test — alter one character of a stored field and confirm verification
fails — and the manual verification in the plan is exactly right: sign, verify, change a field
directly in Postgres, verify again.

**Verification reads nothing mutable.** This is a correction to the first draft of this document,
and it is worth writing down because the defect it fixes is the one that would have damaged the
clinic most.

The canonical form covers the QA clearance that permitted the signature. The first implementation
looked that clearance up again at verification time, by prescription — "what stands now" rather
than "what was signed against". So if the standing decision ever moved underneath a signed
prescription — a second CLEARED row, a re-decided review, a rewritten `decided_at`, a projection
replay — verification would recompute different bytes and answer **not verified for a prescription
nobody had touched.**

Think about where that lands. Not a missed forgery: the opposite. A pharmacist in Faridpur scans a
genuine sheet and is told it may not be genuine. Do that twice and nobody scans the QR again, which
costs more than the feature was ever worth.

So the clearance is **copied onto the signature by value** — the review's identity and the instant
it was decided — and verification reconstructs it from the signature's own columns, calling
nothing. `verifyAgainst` does not even take a `context.Context`, so it cannot reach a database;
reintroducing the lookup means widening a function signature, which shows up in a diff.

The precedent was two tables away in both directions: CP80 copies the captured price onto the
prescription item rather than joining it, CP82 stores the AI suggestion exactly as offered, and
CP83's QA review stores its own findings the same way. The rule underneath all four is one rule —
**a record whose meaning depends on a row somebody can still edit is not a record.**

## 6. The signature image is not the signature

§7.3 wants the physician's signature visible on the paper. That image is a **picture**, stored and
rendered for human readability, and it has no cryptographic role whatsoever.

They must never be conflated, in the code or on the screen. A pasted image is trivially forged; the
signature is not. The printed sheet carries both, and CP85's QR is what lets a stranger check the
one that matters.

## 7. What CP84 does not do

- **Print.** CP89 owns the paper. CP84 makes the sheet signable and immutable; the transition to
  PRINTED exists, and what renders it does not.
- **Revocation.** A signed prescription that was wrong is **corrected**, which CP80 built: a new
  prescription supersedes it and both stay. There is no unsigning, and there should not be.
- **A certificate chain.** D-04, per §1.
