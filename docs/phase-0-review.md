# Phase 0 review and sign-off

CP14. The meeting that stops unresolved decisions cascading into Phase 1 stalls — which the
plan names as the largest risk to this project, ahead of anything technical.

**Held 1 September 2026.** Dr. K. M. Nahid-Ul-Haque and the engineering side.

**Outcome: acceptance criterion 2 is met.** Every decision marked 🔴 in the register is now
either resolved and recorded, or deferred with a named owner and a named date. Criterion 1
is not met and criterion 3 is therefore conditional — §6 says exactly what remains and who
holds it.

---

## 1. Phase 0 against the Definition of Done

| CP   | Status       | Honest assessment                                                        |
| ---- | ------------ | ------------------------------------------------------------------------ |
| CP01 | Done         | Repo, hooks, CI skeleton, custody hashes                                 |
| CP02 | Done         | `dthclint`, 10 ADRs, standards, the DoD itself                           |
| CP03 | **Deferred** | Blocked on D-01. Correctly out of the Phase 0 path                       |
| CP04 | Done         | Compose stack, verified working                                          |
| CP05 | Done         | Four binaries, config, logging, errors, health, shutdown                 |
| CP06 | Done         | Migrations, six schemas, database-enforced append-only ledger            |
| CP07 | Done         | OTLP tracing and metrics, PHI redaction, dashboards, alerts              |
| CP08 | Closed       | One-line decision record (D-51)                                          |
| CP09 | Done         | Token pipeline, contrast contract, 11 primitives, Storybook              |
| CP10 | Done         | Nine route groups, bilingual shell, CSP                                  |
| CP11 | **Done\***   | Shell complete; on-device acceptance now unblocked by D-59               |
| CP12 | Done         | Contract of record, conformance test, generated client — **unreviewed**  |
| CP13 | **Done\***   | Harness, gates and generator complete; clinical read-through outstanding |
| CP14 | Done         | This document                                                            |

**Criterion 1 is not met.** Two checkpoints carry an asterisk and one is deferred by
decision. Neither asterisk is engineering work:

- **CP11** needs a physical tablet. D-59 named the floor today; one unit turns three
  unverifiable acceptance criteria into an afternoon.
- **CP13** needs a clinician to read the generated cohort. Nineteen tests assert the
  population matches Dr. Nahid's case-mix; only he can say whether a population that
  matches it still reads like his patients.

### Two audit findings, restated because they are not yet closed

**CP11 was committed with CI red.** Its `typecheck` did not build the design tokens first,
so it failed on any clean checkout — including every CI run — while passing locally off a
stale `dist/`. It went unnoticed for two checkpoints. The DoD says "All tests pass in CI —
not 'pass locally'". The rule was right; it was not enforced. **Fixed at CP12.**

**CP12 is committed but unreviewed.** It sits on a pushed branch with no pull request, so it
has had no approval and its CI result has not been read. CP13 is now stacked on top of it.
**Still open.** The blocker is access: the connected GitHub account cannot open a pull
request against `AmlanWTK/DTHCMS`.

Both are the same failure — a gate that lives in a document rather than in a habit. The
coverage floors, the route-to-spec conformance test and the profile-conformance tests are
the beginnings of the habit. The missing half is reading CI before moving on.

---

## 2. Decisions taken

Twenty-two decisions carried 🔴. **Fourteen resolved, eight deferred with owner and date.**

### 2.1 Confirmed — already settled, never marked closed

| #        | Decision                | Outcome                                                               |
| -------- | ----------------------- | --------------------------------------------------------------------- |
| **D-07** | LLM provider            | Google Gemini, free tier for development only, fail-closed tier guard |
| **D-43** | Authentication provider | Self-implemented in the Go monolith                                   |

Both were already built this way, per ADR-0007 and ADR-0005. Recording them removes two
settled items from a register whose value depends on nothing settled sitting in it.

### 2.2 Ratified — technical, recommendation stood

| #        | Decision                | Outcome                                                                               |
| -------- | ----------------------- | ------------------------------------------------------------------------------------- |
| **D-30** | Compute platform        | Cloud Run for API and workers; realtime gateway measured before committing            |
| **D-34** | Secrets management      | Google Secret Manager with workload identity; no secrets in env files, images or repo |
| **D-37** | Backup and DR           | RPO ≤5 min / RTO ≤4 h via PITR, cross-region backups, **rehearsed restore drills**    |
| **D-44** | Session and tokens      | Short-lived signed access token; opaque, rotating, revocable device-bound refresh     |
| **D-45** | 2FA method              | TOTP for privileged roles; device-trusted sessions for floor staff; step-up to sign   |
| **D-46** | Device authentication   | Admin-enrolled devices, keypair in Android Keystore, every request device-bound       |
| **D-08** | PHI minimisation for AI | Default deny; pseudonym plus age-in-months, sex and clinical parameters only          |
| **D-15** | AI failure behaviour    | Fail visible, never silent, never invented                                            |

**D-30, D-34 and D-37 are conditional on D-01.** They name Google Cloud services, and
whether those may be used at all is what counsel answers. Ratifying them anyway is
deliberate: when D-01 comes back, it unblocks a decision already made rather than opening a
fresh discussion at the worst moment.

**D-44, D-45 and D-46 shape CP16, CP17 and CP18** — the next substantial work after CP15.
They are settled before the code that depends on them exists, which is the whole point of
holding this meeting now.

### 2.3 Resolved — operational

| #        | Decision               | Outcome                                                                                |
| -------- | ---------------------- | -------------------------------------------------------------------------------------- |
| **D-70** | Administrator recovery | Two administrators from day one, plus sealed break-glass credentials that alert on use |
| **D-56** | Formulary and prices   | The clinic pharmacist owns content and the monthly price review                        |
| **D-59** | Device floor           | Android 12 (API 31), 4 GB RAM, 8–10in. One unit to be purchased for CP11 acceptance    |

**D-70 carries a gate**: two administrators and the break-glass credential must exist
_before CP16 reaches production_, not merely be intended. It is recorded in the plan as a
gate rather than a preference, because this is the kind of gap that is invisible until the
morning it matters.

### 2.4 Delegated to engineering, and decided

Dr. Nahid delegated both of these. Both are recorded in full in the plan's register with
their reasoning; the summary is here.

**D-21 — paediatric growth reference: WHO below 5.0 years, CDC 2000 from 5.0 to 19.**

Chosen mainly because it keeps [R-06] intact: the ratified rule flags childhood obesity at
≥95th percentile, which is the CDC convention, while WHO 2007 defines obesity at >+2 SD ≈
97.7th percentile. Choosing WHO throughout would not relabel that threshold — it would
change which children are flagged, which is not a decision to take as a side effect of
picking a chart. Secondarily: CDC's curves meet the adult cut-points at 20, so an
adolescent moving into adult follow-up does not change category for no clinical reason; and
CDC carries established severe-obesity extensions, which matter in a caseload where obesity
is the largest single presenting problem.

Two things stated plainly. **The switch at 5.0 years is a real discontinuity** and CP47 must
show it rather than hide it. And **neither reference is derived from South Asian children** —
both under-call adiposity-related risk at a given BMI in this population — so CP47 should
carry the BMI z-score alongside the percentile and, where waist is recorded, evaluate South
Asian action points as an additional signal.

**This decision is cheap to reverse.** CP47 stores the reference source and version with
every computed percentile, so changing protocol later is a recomputation, not a migration.
Dr. Nahid can overturn it after seeing it work.

**D-22 — drug knowledge base: Option D.** Physician-curated deterministic rules as the only
runtime authority; RxNorm and openFDA used while authoring, never at run time.

A commercial licence is out of scale for one clinic and carries no Bangladeshi trade names,
so the local mapping work remains yours either way. Public sources alone are not safe to
depend on — licence terms vary sharply for commercial clinical use, and the NLM's own
interaction API was discontinued in 2021. Between A and D there is no runtime difference at
all, so refusing the authoring aid buys nothing.

**The authoring burden is staged, not paid up front.** The engine fails closed and states
its coverage boundary — an uncovered drug shows "no automated safety check available",
never a reassuring tick — which is what makes partial coverage honest and starting small
legitimate. **The first worklist already exists**: the roughly twenty generics the CP13
generator prescribes are the drugs this clinic actually uses, and they cover the large
majority of prescribing volume.

---

## 3. Deferred, with owner and date

Not "later". Each of these has a name and a date, which is what criterion 2 asks for.

### 3.1 Counsel — target 8 September 2026

Owner: **Dr. Nahid + legal counsel.** The questions are drafted and held in
[`counsel-questions.md`](counsel-questions.md), ready to send.

| #        | Decision                             | Question                                          |
| -------- | ------------------------------------ | ------------------------------------------------- |
| **D-01** | PDP Act 2026 compliance posture      | Q1–Q4                                             |
| **D-38** | Data residency                       | Q2–Q3                                             |
| **D-02** | Consent model, scope, revocation     | Q5                                                |
| **D-04** | Medico-legal status of the signature | Q7                                                |
| **D-24** | SNOMED CT licensing                  | §4 — goes to SNOMED International, not to counsel |

The letter adds one question the register did not contain. **Q6 asks whether a retention
duty or a right to erasure prevails**, because the clinical record is designed as an
append-only ledger and that is, on its face, in tension with any obligation to erase. It is
an architectural question, and it is cheaper to ask now than to discover later.

**D-01 is the single highest-value action on this page.** Everything deployed waits on it,
and it is a matter of days once asked.

### 3.2 Clinical content — owner Dr. Nahid

| #        | Decision                                  | Target      | Blocks       |
| -------- | ----------------------------------------- | ----------- | ------------ |
| **D-27** | Critical-value table and escalation rules | 30 Sep 2026 | CP50         |
| **D-53** | Counselling template content              | 30 Nov 2026 | CP55 content |
| **D-54** | Drug warning library, Bangla and English  | 30 Nov 2026 | CP86 content |

D-27 is first because it is the shortest and the most safety-critical. D-54 shares a
worklist with the D-22 medication rules — the same drugs, in the same sitting.

**The synthetic case-mix**, listed here as open at the last draft, is closed: authored
27 August 2026, recorded in `backend/internal/testdata/profile.v1.json`, and driving the
CP13 generator.

---

## 4. Risk register

| Risk                                     | State after CP14                                            | Mitigation                                                                  |
| ---------------------------------------- | ----------------------------------------------------------- | --------------------------------------------------------------------------- |
| Unresolved 🔴 decisions stall Phase 1    | **Closed.** 14 resolved, 8 dated                            | This document                                                               |
| D-01 blocks all deployment               | **Live, but actionable.** Letter drafted, not yet sent      | Send by 8 Sep                                                               |
| Clinical content authoring slips         | **Reduced.** Dates set; none authored yet                   | D-27 by 30 Sep is the one to watch                                          |
| Gates exist on paper, not in habit       | **Reduced.** Coverage floors, conformance and profile tests | Read CI before moving on; open a PR for CP12                                |
| CP12 unreviewed on a pushed branch       | **Live.** GitHub account lacks write access                 | Resolve repository access                                                   |
| Single-engineer bus factor (D-52)        | **Live**                                                    | A second engineer by Phase 2                                                |
| Go work cannot be verified where written | **Live**                                                    | No Docker or Go proxy in the build sandbox; verified on Dr. Nahid's machine |

The first row moving from _live_ to _closed_ is what this checkpoint was for.

---

## 5. Phase 1 sequence

Unchanged: **CP15** (user, role and permission data model) → **CP16** (password
authentication and sessions) → **CP17–CP20** (2FA, devices, RBAC, authorisation) → CP21
onward.

CP16's three shaping decisions — D-44, D-45, D-46 — were settled today, and its production
gate — D-70 — is recorded. CP15 has no dependency on any open decision and can begin
immediately.

---

## 6. What remains before this page is signed

| Item                                                         | Holder      | Cost                         |
| ------------------------------------------------------------ | ----------- | ---------------------------- |
| Buy one tablet meeting the D-59 floor; run CP11 criteria 1–3 | Dr. Nahid   | One purchase, one afternoon  |
| Read the CP13 synthetic cohort and mark what reads wrong     | Dr. Nahid   | One sitting                  |
| Open a pull request for CP12 and read its CI result          | Engineering | Blocked on repository access |
| Send the counsel letter                                      | Dr. Nahid   | An email                     |

None of these blocks CP15. All four are the difference between a phase that is finished and
a phase that is merely not being worked on.

---

## 7. Sign-off

> I have reviewed the Phase 0 foundation, the architecture decision records, and the open
> decisions above. Every decision marked 🔴 is either resolved and recorded, or deferred
> with a named owner and date. Phase 1 may begin.

**Criterion 2 met, 1 September 2026.** Criteria 1 and 3 remain open on the four items in §6.

**Dr. Nahid** ☐ \_\_\_\_\_\_\_\_\_\_\_\_\_\_\_\_ Date ☐ \_\_\_\_\_\_\_\_

Decisions taken here are recorded in the plan's §3 register and in
[`progress.md`](progress.md) — this page is the meeting, those are the record.
