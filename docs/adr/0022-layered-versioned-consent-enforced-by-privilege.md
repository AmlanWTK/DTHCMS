# ADR-0022 · Layered, versioned consent, enforced by privilege rather than by remembering

- **Status:** Accepted (the engine) · **Blocked** (the wording, D-02)
- **Date:** 2026-09-03
- **Checkpoint:** CP36
- **Deciders:** Dr. K. M. Nahid Ul Haque, Amlan Sarkar
- **Blueprint reference:** §15.1, §11.2, §12, §7
- **Related decisions:** D-02 (consent model, scope and revocation — deferred to counsel) · D-03 (IRB pathway) · D-05 (retention and erasure)
- **Supersedes:** —

## Context

§15.1 asks for "explicit consent tracking". §11.2 asks for consent before a call or an SMS.
§12 wants anonymised research dashboards of IRB-grade integrity. D-02 turns all of that into
one legal question that is not ours to answer: what exactly is consented to, by whom, in what
language, with what evidence, and what happens on withdrawal.

That question is with Dr. Nahid and counsel. It should not stop the software, because almost
none of the engineering depends on the answer — and the parts that do are the wording, which
is data.

There are three failures worth designing against, and they are all quiet ones.

**A single blanket consent answers the wrong question.** A patient who wants treatment but not
an SMS at seven in the morning has expressed two preferences. A record that says "consented"
cannot answer either of them, and the first time it matters is when somebody complains.

**A consent with no version is a consent to nothing in particular.** In 2031 somebody will ask
what a patient agreed to in 2026. "Research consent" is not an answer. Worse, template text
gets edited — a typo, a clarification, a lawyer's revision — and every consent recorded
against it silently comes to mean something different.

**A consent table that nothing consults is a compliance artefact, not a control.** The
research ETL that filters on consent is one refactor away from the ETL that forgot to. The
outreach job that checks before sending is one new code path away from the one that does not.
Enforcement that depends on remembering will eventually be forgotten, and the symptom is a
message sent to somebody who asked to be left alone — which nobody notices until they
complain, by which time it has happened many times.

## Decision

**Layered.** Five consent types — CARE, COMMUNICATION, RESEARCH, AI_PROCESSING, OUTREACH —
each independently grantable and revocable. This is D-02's option (ii) and its recommendation.

**Versioned, with the words themselves.** `core.consent_template` holds the full text per type
per language, with a SHA-256 of the body. A template becomes `active` and is then immutable by
trigger; a change is a new version, and the old one stays readable forever because consents
point at it. A template is never deleted. The event carries the version, the language **and
the digest**, so a template row altered later by somebody with database access is detectable
from the ledger rather than merely unlikely.

**Revocation is an event, never an update of the grant.** Both are needed to answer "was that
message lawful when it was sent", which is the question a complaint actually asks. A reason is
optional: a patient withdrawing consent does not owe anybody an explanation, and a mandatory
field would be filled in with "revoked" by an operator standing in front of somebody who wants
to leave.

**Enforcement at the point of use, in two forms.**

_Research is enforced by database privilege._ `dthcms_research` loses `SELECT` on
`research.research_subject` entirely and is granted `research.cohort`, a view filtered on live
consent. A researcher cannot query somebody who said no even by writing their own SQL. The
flag on the subject row is maintained by the consent derivation, which is the one
`SECURITY DEFINER` function permitted to cross `identity_link`.

_Everything else goes through a gate whose interface is declared here, not there._ A module
that wants to send holds a `consent.Sender`; the only `Sender` the composition root constructs
is the gated one. There is no path from "I have something to say" to "it was sent" that does
not pass a consent check, because the un-gated sender is not reachable. Declaring the
interface in the communication module and remembering to wrap it would be the same design with
a step somebody can skip.

**Immediate by construction.** §15.1's budget is one minute. The row the gate reads is written
by the same `COMMIT` as the event, so there is no interval in which the ledger and the
enforcement disagree. The gate caches for five seconds — chosen to be obviously inside the
budget rather than close to it — and the service invalidates a patient's entry on every write,
so in practice the next question after a revocation is already answered correctly.

**The gate fails closed.** A read that errors is a refusal, not an allow. A gate that fails
open is a gate that sends messages during a database incident.

**No wording is shipped.** Until D-02 is answered `/v1/consent-templates` is empty, the capture
screen says so plainly, and taking a consent answers `503`. That is the honest state of a
deployment that is not finished — and a `422` would send a registration desk looking for a
mistake it did not make.

## Consequences

Loading the approved text is an `INSERT`, and everything else already works. Adding a sixth
consent type is a migration and a vocabulary entry, which is the review it deserves.

Two rules now exist that privilege cannot enforce and a reviewer has to:

- **A research mart is built from `research.cohort`, never from `research.research_subject`.**
  The projector and the owner can both reach the base table, so a mart written against it
  would compile and would quietly include people who withdrew. The default `SELECT` on future
  tables in `research` is deliberately left in place — it is what lets a mart be added without
  a grant, and marts are aggregates.
- **A new outbound purpose gets an entry in `consent.Requires`.** One that does not is refused
  rather than allowed, so the mistake is loud; but somebody still has to decide which consent
  covers it.

What is _not_ decided here, and remains D-02's: whether withdrawal removes already-published
cohort data, who consents for a minor beyond recording that somebody did, and whether verbal
attestation is acceptable in law. The engine records what happened either way; the policy is
a legal answer.

`research.research_subject.research_consent` defaults to **false**, which means research is
opt-in and a register migrated into this schema starts with an empty cohort. That is the
correct default and it is also a visible change: `TestResearchCannotReachAnythingIdentified`
now asserts an empty cohort for a freshly registered patient, which is the new truth.
