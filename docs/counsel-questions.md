# Questions for legal counsel

**Prepared:** 1 September 2026 · **For:** Dr. K. M. Nahid-Ul-Haque, Diabetes, Thyroid &
Hormone Care (DTHC), Faridpur · **Register:** D-01, D-38, D-02, D-04 · **Target: sent by
8 September 2026.**

This is a request for written advice. It is not itself advice — nobody on the engineering
side of this project is qualified to answer any of it, which is precisely why it is being
asked rather than assumed.

---

## 1. What is being built, and where it stands

DTHC is a specialist endocrine outpatient clinic. We are building a clinic management
system that will hold, for each patient:

- identity and demographic data, including **photographs of National ID (NID) cards**
- clinical observations — vitals, anthropometry, laboratory results
- diagnoses, prescriptions and dispensing records
- counselling and lifestyle records
- optionally, and separately consented, de-identified data used for clinical research

**No real patient data exists in the system today.** The entire build to date runs on
fictional patients generated from an invented case-mix, and nothing has been deployed to
any server. That is deliberate: these questions are being asked _before_ the decisions
they govern become expensive to reverse.

Two decisions are waiting on the answers. Where the system may be hosted, and how consent
is modelled. Both are cheap to change now and very costly to change after the first real
patient is registered.

## 2. What form of answer is useful

For each question: a direct answer, the provision or instrument it rests on, and — where
the position is unsettled — a statement that it is unsettled and what the prudent course
would be. A qualified "probably, subject to X" is far more useful than a confident answer
that has to be revisited.

---

## 3. The questions

### A. Classification of health data

**Q1.** Does the Personal Data Protection Act, 2026 treat health data — diagnoses,
laboratory results, prescriptions, clinical notes — as a **special or sensitive category**
of personal data attracting obligations beyond those applying to ordinary personal data?

If so, which obligations specifically: heightened consent, security standards, breach
notification thresholds, registration or notification with a regulator, appointment of a
data protection officer, or others?

### B. Cross-border processing and residency

**Q2.** May personal data of this kind be **processed or stored outside Bangladesh** — for
example on cloud infrastructure located in Singapore, India or Europe?

If it may, on what lawful basis, and subject to what conditions — an adequacy
determination, contractual safeguards, explicit consent, prior authorisation, or
registration?

If it may not, is the restriction absolute, or does it permit a copy abroad provided a
primary copy remains in Bangladesh?

**Q3.** Are **backup and disaster-recovery copies** treated the same as primary storage for
the purposes of Q2? This matters concretely: our resilience design assumes backups held in
a second geographic region, and if that region must be domestic, the design changes.

### C. National ID images

**Q4.** Are there **localisation, retention or handling duties specific to NID data or
images of NID cards** — whether under the Act, under National Identity Registration
legislation, or under Election Commission rules?

We would prefer not to store NID images at all if a verified reference number is
sufficient. Whether that choice is ours to make is part of the question.

### D. Consent

**Q5.** Would a **layered, versioned consent model** satisfy the Act's consent
requirements? Concretely, we propose separate and separately revocable consents for:

1. clinical care and the records it generates
2. communication by SMS or messaging application (appointment reminders, results)
3. inclusion of de-identified data in clinical research

with each consent recorded against the version of the wording the patient actually saw.

Two follow-ups:

- **What must be recorded to evidence valid consent** — wording version, timestamp,
  method, witness, signature?
- **What must happen operationally on revocation?** Does revoking research consent oblige
  us to remove already-extracted de-identified data from a research dataset, or only to
  exclude the patient going forward?

### E. Retention, erasure and the medical record

**Q6.** How long must medical records be **retained** under Bangladeshi law or BMDC
requirements, and does a patient's right to erasure (if the Act confers one) override that
retention duty, or yield to it?

This question is architectural, not merely procedural. The system's clinical record is
designed as an **append-only ledger**: a correction is recorded as a new entry and the
original remains, so that what a clinician saw at the moment they decided is always
recoverable. That is a patient-safety property and a medico-legal one. It is also, on its
face, in tension with any obligation to erase. We need to know which duty prevails before
the schema is built, because reconciling them afterwards is not a small change.

### F. The digital prescription

**Q7.** Is a prescription **generated and signed electronically by a registered physician**
legally valid in Bangladesh — for dispensing by a pharmacy, and as evidence?

Specifically: is a server-side cryptographic signature with a tamper-evident audit trail
sufficient, or is a **licensed Digital Signature Certificate** under the Information and
Communication Technology Act required for the signature to have legal effect?

Does BMDC impose any separate requirement as to form, content or signature on a
prescription issued electronically?

---

## 4. Not a question for counsel

**SNOMED CT licensing (D-24)** is recorded here only so the set is complete. It is
answered by SNOMED International, not by a lawyer: whether Bangladesh is a Member country
or qualifies under the low-income-country waiver, and therefore whether SNOMED CT content
may be embedded free of charge. **Nothing SNOMED-derived will be embedded until that is
confirmed in writing.** ICD-10 and ICD-11 are published by WHO under considerably more
permissive terms and are unaffected.

---

## 5. Why the timing matters

| Answer to | Determines                                                                                | Which currently blocks                                                           |
| --------- | ----------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| Q1–Q4     | Where the system may be hosted, and how NID data is handled                               | Cloud infrastructure (CP03), and every deployed environment                      |
| Q5        | The consent data model and the enforcement points around it                               | Patient registration (CP29), research export (CP121), all outbound communication |
| Q6        | Whether the append-only clinical ledger survives as designed                              | The clinical record schema                                                       |
| Q7        | Whether an electronic signature is sufficient, or a licensed certificate must be procured | Prescription signing (CP84)                                                      |

Q1–Q4 are the urgent ones. Nothing can be deployed until they are answered, and the
project is at the point where the next phase of work assumes an answer.
