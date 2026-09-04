# Consent

_CP36 · blueprint §15.1, §11.2, §12, §7 · ADR-0022 · decision **D-02 (open)**_

The engine, not the wording. What exactly a patient agrees to, in what words, with what
withdrawal semantics, is a legal question with Dr. Nahid and counsel. Everything here is what
has to be true whatever the answer turns out to be, arranged so that loading the approved text
later is an `INSERT` and not a migration.

Until then `/v1/consent-templates` is empty, the capture screen says so plainly, and taking a
consent answers `503`. That is the honest state of a deployment that is not finished — and a
`422` would send a registration desk looking for a mistake it did not make.

---

## 1. Five consents, not one

| Type            | What it permits                        | Where it is enforced    |
| --------------- | -------------------------------------- | ----------------------- |
| `care`          | Examination, tests and treatment       | The clinical write path |
| `communication` | Telephone calls and SMS (§11.2)        | `consent.Sender`        |
| `research`      | An anonymised row in the cohort (§12)  | **Database privilege**  |
| `ai_processing` | The AI gateway reading the record (§7) | `consent.Guard`         |
| `outreach`      | Home visits and camp invitations       | `consent.Sender`        |

Layered rather than blanket, which is D-02's recommendation and the only shape that survives
contact with the questions the clinic actually asks. A patient who wants treatment but not an
SMS at seven in the morning is expressing two preferences; a single "I consent" box records
neither of them and answers the wrong question when somebody later asks what they agreed to.

Three states, and `absent` is not `revoked`. Never asked is work the desk has not done; asked
and withdrawn is a decision the patient made. An interface that conflates them produces staff
who re-ask people who said no.

## 2. Versioned, with the words themselves

`core.consent_template` holds the **full text** per type per language — not a key into a
translation file, because a translation file is edited and what a patient was shown must be
retrievable exactly.

- A template becomes `active` and is then **immutable by trigger**. A change is a new version;
  the old one stays readable forever because consents point at it.
- A template is **never deleted**. A version that vanishes turns a recorded consent into a
  consent to nothing.
- Only **one active version** per type per language at a time. Two would mean two patients on
  the same morning consented to different words with no way to tell which.
- The event carries the version, the language **and the SHA-256 of the body**, so a template
  row altered later by somebody with database access is detectable from the ledger rather than
  merely unlikely.

The version is looked up by the **server**, never sent by the client. A client that could name
the version it consented against could record a consent to words the patient never saw.

## 3. How it was taken

| Method            | Needs                   | Why                                                        |
| ----------------- | ----------------------- | ---------------------------------------------------------- |
| `signature`       | a PNG in object storage | The image is the evidence                                  |
| `thumbprint`      | a PNG **and** a witness | The only other person present is the operator recording it |
| `verbal_attested` | a witness               | An attestation nobody witnessed is an assertion            |
| `paper_form`      | the form reference      | Without it nobody can find the paper                       |

`verbal_attested` is here because refusing it would not make consent better recorded — it
would make it recorded on paper and not here. It is weaker evidence than a thumbprint and the
record says which it is, which is the honest arrangement.

The image follows CP34's path exactly: a pre-signed `PUT`, the client uploads straight to
storage, the API is told the key. A signature that never enters the API process cannot end up
in a request log. PNG only — a signature is line art, and JPEG artefacts around thin strokes
are exactly what makes one arguable.

`granted_for_relation` and `granted_for_name` record a guardian consenting for a patient who
could not. Whether that is sufficient in law for a minor is D-02's; the engine records what
happened either way.

## 4. Revocation

**Its own event, never an update of the grant.** The grant is what was true then and stays
retrievable; the revocation is what is true now. Both are needed to answer "was this message
lawful when it was sent", which is the question a complaint actually asks.

A reason is **optional**, deliberately. A patient withdrawing consent does not owe anybody an
explanation, and a mandatory field would be filled in with "revoked" by an operator standing in
front of somebody who wants to leave.

Revoking something that was never granted is refused: it would record a withdrawal that never
happened, and a patient asking "did you stop" deserves a truthful answer rather than a
reassuring row. Revoking twice is not refused — it is a retry.

**Effective immediately.** §15.1's budget is one minute; the row the gate reads is written by
the same `COMMIT` as the event, so there is no interval in which the ledger and the enforcement
disagree.

## 5. Enforcement, at the point of use

§15.1's phrase is "consent enforcement at the point of use, not merely recorded". A consent
table that nothing consults is a compliance artefact, not a control. Two mechanisms, chosen so
that forgetting is not one of the failure modes.

### Research: by privilege

`dthcms_research` has **no `SELECT` on `research.research_subject` at all**. It reads
`research.cohort`, a view filtered on `research_consent`. A researcher cannot query somebody
who said no even by writing the query themselves.

The flag is maintained by the consent derivation, which is the one `SECURITY DEFINER` function
permitted to cross `identity_link` — the link between a patient and their research subject that
neither the application nor research may read.

It defaults to **false**: research is opt-in, so a subject appears in the cohort when they
agree and disappears when they withdraw.

`core.assert_research_needs_consent()` holds both halves as an invariant: research may not read
the base table, and must be able to read the view.

> **The rule privilege cannot enforce:** a research mart is built from `research.cohort`, never
> from `research.research_subject`. The projector and the owner can both reach the base table,
> so a mart written against it would compile and would quietly include people who withdrew.

### Everything else: by the shape of the interface

`consent.Sender` is declared in the consent package, not in the communication module. A module
that wants to send holds a `Sender`; the only `Sender` the composition root constructs is the
gated one. There is no code path from "I have something to say" to "it was sent" that does not
pass a consent check, because the un-gated sender is not reachable from a call site.

Declaring the interface in the communication module and remembering to wrap it would be the
same design with a step somebody can skip.

`consent.Guard` is the same check for things that are not messages — the AI gateway, an export,
a report.

A **purpose** with no consent behind it is refused rather than allowed. A new kind of outbound
action added without deciding which consent covers it is exactly the mistake this catches.

| Purpose     | Consent         |
| ----------- | --------------- |
| `treat`     | `care`          |
| `remind`    | `communication` |
| `invite`    | `outreach`      |
| `analyse`   | `research`      |
| `interpret` | `ai_processing` |

### The gate fails closed

A read that errors is a refusal, not an allow. A gate that fails open is a gate that sends
messages during a database incident.

The cache is five seconds and the service invalidates a patient's entry on every write, so in
practice the next question after a revocation is already answered correctly. Five is chosen to
be obviously inside the one-minute budget rather than close to it.

## 6. The API

| Route                                           | Needs                       |
| ----------------------------------------------- | --------------------------- |
| `GET /v1/consent-templates?language=`           | `patient.consent.record`    |
| `GET /v1/patients/{id}/consents`                | `patient.read.demographics` |
| `GET /v1/patients/{id}/consents/history`        | `patient.read.demographics` |
| `POST /v1/patients/{id}/consents`               | `patient.consent.record`    |
| `POST /v1/patients/{id}/consents/evidence-url`  | `patient.consent.record`    |
| `POST /v1/patients/{id}/consents/{type}/revoke` | `patient.consent.revoke`    |

The list returns **all five types**, with the ones never asked about marked `absent`. A list of
only what exists cannot show a desk what it has not done, and "we never asked" is the answer
that matters at the point of care.

Revocation is a `POST` to a sub-resource rather than a `DELETE`, because nothing is deleted.

## 7. The screen

`/patients/{id}/consent`. Five rows, always all five, with a state rail down the edge so the
panel reads at a glance — the question it exists to answer is asked by somebody about to do
something, and they need the answer from across a desk.

Taking a consent **replaces the list** rather than appearing under it. On a tablet held between
an operator and a patient, a form below five rows is a form nobody scrolls to.

The **wording is on screen before the button is reachable**. A screen where a consent can be
recorded without the text on it records a consent nobody read out.

## 8. What is still D-02's

- Whether withdrawal removes already-published cohort data.
- Who consents for a minor, beyond recording that somebody did.
- Whether verbal attestation is acceptable in law.
- The wording itself, in both languages, for all five.

The engine records what happened either way. The policy is a legal answer, and this document
will name it once it exists.
