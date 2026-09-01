# Phase 0 review and sign-off

CP14. The meeting that stops unresolved decisions cascading into Phase 1 stalls — which
the plan names as the largest risk to this project, ahead of anything technical.

This page is the agenda and the record. Work through it, write decisions in the blanks,
and §7 is what gets signed.

**Status: unsigned.** Two Phase 0 checkpoints are not closed (§2), so this cannot be
completed today. It is written now because §3 does not depend on them, and §3 is where the
value is.

---

## 1. How to use this

§3 is 20 decisions marked 🔴 in the plan's register. For each, criterion 2 of this
checkpoint asks for one of exactly two outcomes:

- **Resolved** — a decision, recorded here, with reasoning.
- **Deferred** — with a **named owner** and a **named date**. Not "later".

"We'll come back to it" is neither, and is how a register of 71 decisions becomes a
register nobody reads.

Most already carry a recommendation from the plan. Where I think the recommendation is
sound and the decision is technical, I say so and you can ratify it in a word. Where the
decision is clinical, legal, or commercial, I have not made one and will not.

## 2. Phase 0 against the Definition of Done

| CP   | Status       | Honest assessment                                                  |
| ---- | ------------ | ------------------------------------------------------------------ |
| CP01 | Done         | Repo, hooks, CI skeleton, custody hashes                           |
| CP02 | Done         | `dthclint`, 10 ADRs, standards, the DoD itself                     |
| CP03 | **Deferred** | Blocked on D-01. Correctly out of the Phase 0 path                 |
| CP04 | Done         | Compose stack; verified working this session                       |
| CP05 | Done         | Four binaries, config, logging, errors, health, shutdown           |
| CP06 | Done         | Migrations, six schemas, database-enforced append-only ledger      |
| CP07 | Done         | OTLP tracing and metrics, PHI redaction, dashboards, alerts        |
| CP08 | Closed       | One-line decision record (D-51)                                    |
| CP09 | Done         | Token pipeline, contrast contract, 11 primitives, Storybook        |
| CP10 | Done         | Nine route groups, bilingual shell, CSP                            |
| CP11 | **Done\***   | Shell complete; on-device acceptance waits on D-59                 |
| CP12 | Done         | Contract of record, conformance test, generated client             |
| CP13 | **Partial**  | Coverage gate and Go harness done; generator waits on the case-mix |
| CP14 | In progress  | This document                                                      |

**Criterion 1 — "All Phase 0 checkpoints meet the Definition of Done" — is not met.**
CP11 and CP13 are open, and CP03 is deferred by decision. That is the honest position.

### Two audit findings worth stating plainly

**CP11 was committed with CI red.** Its `typecheck` did not build the design tokens first,
so it failed on any clean checkout — including every CI run — while passing locally off a
stale `dist/`. It went unnoticed for two checkpoints. The DoD says "All tests pass in CI —
not 'pass locally'". The rule was right; it was not enforced.

**CP12 is committed but unreviewed.** It sits on a pushed branch with no pull request, so
it has had no approval and its CI result has not been read. CP13 is stacked on top of it.

Neither is a crisis. Both are the same failure: a gate that exists in a document rather
than in a habit. The coverage gate added at CP13 and the conformance test added at CP12
are the beginnings of the habit; the missing half is reading CI before moving on.

## 3. The 🔴 decisions

### 3.1 Already resolved — recording only

Two decisions in the register are settled by ADRs and were never marked closed.

| Decision                         | Settled by                                          | Outcome                                                               |
| -------------------------------- | --------------------------------------------------- | --------------------------------------------------------------------- |
| **D-07** LLM provider            | [ADR-0007](adr/0007-gemini-provider-and-tier.md)    | Google Gemini, free tier for development only, fail-closed tier guard |
| **D-43** Authentication provider | [ADR-0005](adr/0005-self-implemented-staff-auth.md) | Self-implemented in the Go monolith                                   |

☐ Confirm both, and mark them ✅ in the plan's register.

### 3.2 Cannot be decided without counsel

These are not deferrals of convenience. Nobody in this project is qualified to answer them,
and guessing produces a system that must be rebuilt rather than adjusted.

| #        | Decision                                        | What it blocks                                           | Needed from counsel                                                                                                                                              |
| -------- | ----------------------------------------------- | -------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **D-01** | PDP Act 2026 compliance posture                 | CP03 and everything deployed                             | Three written answers: is health data "sensitive personal data"; what lawful basis permits cross-border processing; are there localisation duties for NID images |
| **D-38** | Data residency                                  | Final infrastructure sign-off                            | Follows D-01                                                                                                                                                     |
| **D-02** | Consent model, scope, revocation                | Registration (CP29), research (CP121), all communication | Whether layered, versioned consent satisfies the Act                                                                                                             |
| **D-04** | Medico-legal status of the digital prescription | Prescription signing (CP80s)                             | Whether a server-side cryptographic signature is legally sufficient without a licensed DSC                                                                       |
| **D-24** | ICD version, SNOMED licence                     | Terminology (CP52)                                       | Whether SNOMED CT Affiliate licensing is available and affordable in Bangladesh                                                                                  |

Owner: **Dr. Nahid + legal counsel.** Target date: ☐ \_\_\_\_\_\_\_\_

**D-01 is the one to start this week.** Everything deployed waits on it, and counsel
answering three written questions is a matter of days once asked — but it cannot start
until it is asked.

### 3.3 Yours to author — clinical content

Engineering builds the machinery; the clinician supplies the medicine. None of these
blocks Phase 1 engineering, and all of them are the most common cause of slip in projects
of this shape, because they feel postponable until the week they are not.

| #        | Decision                                           | Blocks               | Decision       |
| -------- | -------------------------------------------------- | -------------------- | -------------- |
| **D-21** | Pediatric growth reference: WHO 5–19y, or CDC 2000 | CP47, CP48           | ☐ \_\_\_\_\_\_ |
| **D-27** | Critical-value table and escalation rules          | CP50                 | ☐ \_\_\_\_\_\_ |
| **D-53** | Counseling template content                        | CP55 content         | ☐ \_\_\_\_\_\_ |
| **D-54** | Drug warning library, Bangla and English           | CP86 content         | ☐ \_\_\_\_\_\_ |
| **D-22** | Drug knowledge base source                         | CP77, CP78           | ☐ \_\_\_\_\_\_ |
| —        | **Synthetic case-mix**                             | CP13 generator, CP93 | ☐ \_\_\_\_\_\_ |

On **D-21**: the blueprint's own ≥95th-percentile rule points to CDC 2000, but WHO 2007
defines obesity at >+2 SD ≈ 97.7th percentile. The two disagree about which children are
obese. That is a clinical protocol decision and I will not make it — but whichever is
chosen, CP47 must store the reference source and version with every computed percentile,
so a value can always be re-read against the standard it was computed under.

On **D-22**: the plan recommends option D — Dr. Nahid authors the interaction rules — on
the grounds that it matches the deterministic-prescribing principle and is the
medico-legally strongest position. It is also the most work for you. Worth deciding with
that trade-off explicit.

### 3.4 Technical — recommendation stands, needs ratifying

I think the plan's recommendation is right in each of these. Ratify, or overrule.

| #        | Decision                   | Recommendation                                                                                            | Ratify |
| -------- | -------------------------- | --------------------------------------------------------------------------------------------------------- | ------ |
| **D-30** | Compute platform           | Cloud Run for API and workers; realtime gateway measured before committing                                | ☐      |
| **D-34** | Secrets management         | Google Secret Manager with workload identity; no secrets in env files, images or the repo                 | ☐      |
| **D-37** | Backup and DR              | RPO ≤5 min / RTO ≤4 h via PITR, cross-region backups, retention-locked object storage, **restore drills** | ☐      |
| **D-44** | Session and token strategy | Short-lived signed access token; opaque, rotating, revocable refresh token bound to the device            | ☐      |
| **D-45** | 2FA method                 | TOTP for privileged roles; device-trusted sessions for floor staff; step-up for prescription signing      | ☐      |
| **D-46** | Device authentication      | Admin-enrolled devices, server-issued keypair in Android Keystore, every request device-bound             | ☐      |
| **D-08** | PHI minimisation for AI    | Default deny; gateway substitutes a pseudonym plus age-in-months, sex, clinical parameters                | ☐      |
| **D-15** | AI failure behaviour       | Fail visible, never silent, never invented                                                                | ☐      |

D-30, D-34 and D-37 depend on D-01 in practice — they name Google Cloud services, and
whether those are usable is what D-01 answers. Ratifying them now is still worth it: it
means D-01 unblocks a decision already made rather than starting a new discussion.

### 3.5 Operational

| #        | Decision                                         | Recommendation                                                                                                                      | Decision       |
| -------- | ------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------- | -------------- |
| **D-70** | Administrator recovery                           | Two administrators from day one, plus sealed break-glass credentials that alert on use. **Required before CP16 reaches production** | ☐ \_\_\_\_\_\_ |
| **D-56** | Formulary content and monthly price review owner | Clinic pharmacist — closest to real prices                                                                                          | ☐ \_\_\_\_\_\_ |
| **D-59** | Clinic device model and Android floor            | None taken. CP11 criteria 1–3 cannot be measured until named                                                                        | ☐ \_\_\_\_\_\_ |

**D-59 is cheap and unblocks a finished checkpoint.** CP11's shell is built and waiting;
naming a device turns three unverifiable acceptance criteria into an afternoon's work.

## 4. Risk register

| Risk                                     | State after Phase 0                                       | Mitigation                                                                          |
| ---------------------------------------- | --------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| Unresolved 🔴 decisions stall Phase 1    | **Live.** 20 open                                         | This document                                                                       |
| D-01 blocks all deployment               | **Live.** Not yet asked of counsel                        | Ask this week                                                                       |
| Clinical content authoring slips         | **Live.** None authored                                   | Six items in §3.3, no engineering dependency                                        |
| Gates exist on paper, not in habit       | **Reduced.** Coverage floors, conformance test, spec lint | Read CI before moving on                                                            |
| Single-engineer bus factor (D-52)        | **Live**                                                  | Plan recommends a second engineer by Phase 2                                        |
| Go work cannot be verified where written | **Live**                                                  | No Docker or Go proxy in the build sandbox; verified on Dr. Nahid's machine instead |

## 5. Phase 1 sequence

Unchanged from the plan: CP15 → CP16 (authentication) → CP17–CP20 (roles, devices,
authorisation) → CP21 onward.

**CP16 has two hard prerequisites from this document**: D-70 must be answered before it
reaches production, and D-44/D-45/D-46 shape what it builds. All three are in §3.4 and
§3.5 and can be settled today.

## 6. What CP14 still needs

| Item                                                                                | Blocked by                                       |
| ----------------------------------------------------------------------------------- | ------------------------------------------------ |
| Criterion 1 — all Phase 0 checkpoints meet the DoD                                  | CP11 (D-59), CP13 (case-mix)                     |
| Criterion 2 — every 🔴 decision resolved or dated                                   | This meeting                                     |
| Criterion 3 — written sign-off                                                      | Both of the above                                |
| Manual verification — the empty system on a phone and on the web, in both languages | D-59 for the phone; the web half can be done now |

## 7. Sign-off

> I have reviewed the Phase 0 foundation, the architecture decision records, and the open
> decisions above. Every decision marked 🔴 is either resolved and recorded, or deferred
> with a named owner and date. Phase 1 may begin.

**Dr. Nahid** ☐ \_\_\_\_\_\_\_\_\_\_\_\_\_\_\_\_ Date ☐ \_\_\_\_\_\_\_\_

Decisions taken here are copied into the plan's §3 register and into
[`progress.md`](progress.md) — this page is the meeting, those are the record.
