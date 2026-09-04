# ADR-0020 · Two identifiers, an unreachable link, and a date of birth that carries its own provenance

- **Status:** Accepted
- **Date:** 2026-09-03
- **Checkpoint:** CP28
- **Deciders:** Dr. K. M. Nahid Ul Haque, Amlan Sarkar
- **Blueprint reference:** §3 Step 1, §12, §15.1, [R-06]
- **Related decisions:** D-47 (store the NID or a hash only) · the socio-economic baseline · the required field set · the phone format rules
- **Supersedes:** —

## Context

The patient is the record everything downstream hangs from, and four of its properties are
expensive or impossible to change once a clinic has registered a thousand people.

**The date of birth is clinical.** [R-06] makes pediatric percentile calculation depend on
exact age. But a patient who knows only their birth year is ordinary in Bangladesh, and a
system that demands a day gets one invented at the desk. An invented day is worse than a
missing one, because a percentile computed from it is a clinical number that looks like a
measurement.

**The National ID is the strongest duplicate key the clinic has and the most sensitive field
it holds.** §3 Step 1 asks for strict duplicate-record prevention; duplicate patients destroy
longitudinal history and corrupt research cohorts, and are very hard to fix after the fact.
Deterministic matching on the NID is what makes that work. Storing the number to do it is
what makes a database dump a national-scale disclosure.

**§12 makes the Research ID an identity concern from Step 1.** The plan says, in as many
words, that the Research ID must not be derivable from the clinical ID, and that this is
worth getting right at CP28 because it is very expensive to fix later. The failure it guards
against is not a leak: it is a research dataset that looks anonymous and is not.

**The socio-economic baseline is a research design decision.** Changing the category list
later invalidates cohort comparability — a paper published against one list cannot be
compared with one published against another. Confirmed with Dr Nahid before writing the
migration: the standard six-field set, with the clinical-minimum required set (English name,
sex, date of birth, one mobile, consent reference) and everything else prompted and
skippable.

## Decision

### 1. The date of birth is mandatory and carries two companions

`birth_date` is `NOT NULL`, validated by a trigger against the clinic's calendar — nothing in
the future, nothing implying an age over 130 — and never defaulted. Beside it:

- `dob_precision` — `day`, `month` or `year`. Below `day`, the rest of the date is a
  placeholder, and anything derived from it carries that uncertainty. A patient who knows
  only the year is recorded as 1 January of that year with `precision = year`, which is
  honest, sortable and filterable.
- `dob_verified_by` — what established it: a birth certificate, a national ID, a passport,
  an immunisation card, the patient, a guardian, or an estimate. A researcher comparing
  pediatric growth must be able to tell a document from a recollection.

The domain also refuses the combination that is almost always a transcription error: a
document source with less than day precision. A birth certificate has a day on it.

The two are validated in three places on purpose — in Go before the event, in the event
payload, and by the database — because each is a floor under the one above it. The Go
validation gives a registration officer a field-specific bilingual message; the payload
validation stops a future handler writing an unqualified date into an immutable ledger; the
trigger stops a bulk import doing the same thing without going through Go at all.

### 2. An identifier is a digest, a sealed value and a mask — never a number

Per identifier, `core.patient_identifier` holds:

- `digest` — HMAC-SHA-256 of the normalised number under a **deployment-wide pepper**, with
  the identifier kind mixed into the MAC. Deployment-wide and not per row, because the digest
  has to match _across_ patients for duplicate detection to work, which is exactly what a
  per-row salt would prevent. A plain hash of a ten-digit NID is reversible by anyone with a
  laptop and a weekend; the pepper is what makes the digest useless outside this deployment.
- `sealed` — secretbox ciphertext under the CP12 key ring, with the kind as associated data
  so a ciphertext cannot be moved to a row of another kind and opened there. Openable by the
  service, not by a dump.
- `masked` — `**** **** 6789`, which is what a screen shows without a step-up. Four visible
  characters, because that is the convention every bank in Bangladesh uses and the number a
  patient recognises as their own.

`core.assert_no_plaintext_identifiers()` runs at every start and refuses a `sealed` column
that reads like a number or a `masked` column with nothing hidden. That is the failure nobody
notices otherwise: the schema says sealed, the dump says `1990123456789`, and every layer
assumes another layer handled it.

### 3. The clinical ID is gapless; the research ID is random

`clinical_id` — `DTHC-FRD-2026-000137` — is drawn from `core.clinical_id_counter` under a row
lock, one counter per facility per clinic year. A sequence would be simpler and would leave
gaps when a transaction rolls back; a clinic that finds `…-000138` with no `…-000137` spends
an afternoon looking for the missing person. The lock serialises registrations within one
facility-year, which at a few hundred a day costs nothing.

`research_id` — `RS-` and 26 Crockford base-32 characters — comes from `crypto/rand` and from
nothing else. Not a hash of the patient id, not a keyed hash of the clinical id: a derived
identifier becomes reversible the day the key leaks, and the point is that the research
dataset survives that day. It carries no ordering, so sorting research rows does not
reconstruct registration order, which with a birth year and a sex is a person.

### 4. The link between them lives in a schema neither role can reach

`research.research_subject` holds the anonymised row: a facility code, an enrolment **month**,
a birth **year**, a sex and the six cohorting variables. No name, no identifier, no address,
no exact date.

`identity_link.research_subject` holds `patient_id ↔ research_id` and lives in its own schema.
`dthcms_research` has no `USAGE` on it at all. `dthcms_app` may `INSERT` and may not `SELECT`,
`UPDATE` or `DELETE` — registration assigns the research id in the same transaction as the
patient, and going the other way, from a finding back to a person, is an IRB decision carried
out by the owner rather than a query a handler can make. `core.assert_research_link_sealed()`
checks both halves at every start.

Anonymisation that depends on an analyst querying the right schema is not anonymisation.

### 5. The event carries the demographics and neither secret

`PATIENT_REGISTERED` carries the complete demographics, flat, so the projection is a straight
copy. It carries the identifier **kinds** and not the numbers: the ledger is append-only, so
a number written into an event could never be re-sealed under a rotated key nor removed for a
patient who withdraws consent. It carries no research id, because that would put the
re-identification link in a table the application can read.

The socio-economic vocabulary is defined once, in the event schema, and the domain takes its
lists from there. The ledger is the system of record and an event is immutable, so a category
that has once been written into an event exists for as long as the deployment does. That
makes the event schema the one place the list can live without the domain, the API's enum and
the database `CHECK` drifting into three vocabularies.

`PATIENT_REGISTERED` v1 — a five-field placeholder registered at CP23 — is **replaced rather
than versioned**, because no such event has ever been appended: the patient schema arrives
here, at CP28. From the first production append, a change to a payload is a new version with
an upcaster, per §7.10. Correcting an unused placeholder is not that, and introducing a v2
with a lossy upcaster for zero rows would make the catalogue permanently harder to read for
no reader's benefit.

## Alternatives considered

**Store the NID in plain text, and control access with a permission.** Rejected under D-47.
It makes every backup, every replica and every debugging dump a disclosure, and permissions
do not survive a copy of the file.

**Per-row salt instead of a deployment pepper.** Cryptographically preferable in isolation,
and it destroys the duplicate detection that is the reason the digest exists. The pepper is
the compromise that keeps both properties: matching works, and the digests are worthless
outside this deployment.

**Derive the research ID from the patient ID with a keyed hash.** Tempting because it needs no
storage and cannot collide. Rejected: it is reversible to anyone holding the key, so the
anonymisation depends on key custody forever rather than on there being nothing to reverse.

**Put the research ID on `core.patient`.** One less table and one less join. Rejected: it puts
the re-identification link in a row the application reads on every patient screen, so
"research cannot be linked back" would depend on nobody selecting the column.

**A lookup table for the socio-economic categories.** Editable without a migration, which is
precisely the problem. A research variable whose category list can be edited from the
application is a variable whose cohorts stop being comparable, quietly, between one paper and
the next. `CHECK` constraints make adding a category a migration, which is the review it
deserves.

**Make the whole socio-economic block required.** Rejected with Dr Nahid: a registration desk
that cannot finish a record without an income band the accompanying relative does not know is
a desk that holds a queue, and the number it eventually types is not data.

## Consequences

**Good**

- A date of birth is either exact or honestly labelled, and every consumer can tell which.
- A database dump contains no readable identifier, and a digest lifted from it says nothing
  about a number in another deployment.
- The research dataset is anonymous by construction: there is no key to leak and no join to
  forget to omit.
- Clinical IDs are gapless, so an absent number means a missing person and is worth
  investigating.
- Every registration writes the patient, the identifiers, the anonymised row and the link in
  one transaction, so no patient is silently outside their cohort.

**Bad — and we accept these knowingly**

- The pepper is a single deployment-wide secret. If it leaks _and_ the attacker has the
  database, the NID space is small enough to enumerate. Mitigated only by treating it as a
  CP12 secret with the same handling as the sealing keys.
- The counter's row lock serialises registrations within a facility-year. At a clinic's rate
  this is invisible; at a hypothetical hundred registrations a second it would not be.
- Adding a socio-economic category is a migration, which is slower than a form change. That
  is the intended cost.
- Re-identifying a research subject requires the owner and a direct database session. That is
  the intended friction, and it means a legitimate IRB-approved linkage is a manual job.

**Revisit when**

- A second clinic makes `facility_code` in `research.research_subject` a re-identifier rather
  than a comparison variable — at which point small-cell suppression stops being optional.
- Registration volume makes the counter lock measurable in the p99 of `POST /v1/patients`.
- A regulator or an IRB asks for a documented break-glass procedure for re-identification,
  which will need an audited path rather than a psql session.
