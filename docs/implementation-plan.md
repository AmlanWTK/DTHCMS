# DTHCMS — Master Implementation Plan v1.0
### Checkpoint-Based Delivery Roadmap, Architecture & Open-Decision Register
**Derived from:** DTHCMS Complete Blueprint v2.0 — Developer Handover Edition (21 Aug 2026)
**Prepared for:** Dr. K. M. Nahid Ul Haque — Founder, DTHC
**Role of this document:** Technical plan only. **No code has been written. No checkpoint has been started.**
**Status:** Draft for review. Nothing here is implemented until CP01 is explicitly approved.

---

## TABLE OF CONTENTS

| § | Section |
|---|---------|
| 1 | Executive Summary |
| 2 | What the Blueprint Defines (Confirmed Requirements) |
| 3 | Open Decisions / Specification Gaps (D-01 – D-71) |
| 4 | Proposed Technology Stack |
| 5 | System Architecture |
| 6 | Domain Model |
| 7 | Event / Audit Architecture |
| 8 | Backend Architecture (Detail) |
| 9 | Database & Data Architecture |
| 10 | AI Architecture |
| 11 | OCR / NLP Architecture |
| 12 | Security Architecture |
| 13 | Offline / Synchronization Architecture |
| 14 | Modern UI/UX Strategy & Frontend Architecture |
| 15 | Complete Checkpoint List (CP01–CP160) |
| 16 | Detailed Checkpoint Specifications |
| 17 | Checkpoint Dependency Graph |
| 18 | Testing Strategy |
| 19 | Acceptance Strategy |
| 20 | Phase-Wise Timeline |
| 21 | Full Project Estimate |
| 22 | Definition of Done |
| 23 | Risks and Recommendations |
| 24 | Recommended CP01 |

*Your requested structure had 22 sections; this document follows it exactly, with two additional chapters — §8 Backend Architecture and §9 Database & Data Architecture — added to cover items 14 and 15 of your brief. All later sections shift by two.*

---

# 1. EXECUTIVE SUMMARY

## 1.1 What is being built

DTHCMS is not a clinic EMR with extras. Read literally, Blueprint v2.0 specifies **six systems that share one patient record**:

1. **A high-throughput, multi-station clinical capture platform** — 12 stations, 10–15 operators touching one patient inside 45 minutes, every write attributed to a person + device + station + timestamp, every station operated from a phone.
2. **An append-only clinical event ledger** — the event log, not the current-state table, is the source of truth (§4.1); corrections never overwrite (§4.3).
3. **A clinical intelligence layer** — asynchronous AI synthesis that must finish before the patient walks into the consultation room (§7.1), a deterministic medication-safety engine that AI may never bypass (§7.3), and an OCR/NLP pipeline that turns a shopping bag of paper into one chronological truth (§6).
4. **A patient-relationship engine (CRM)** — visit memory, preferred call windows, call→SMS fallback, two-way clinic line, no-show prediction (§11).
5. **A research organisation** — every patient is a research subject from registration (Research ID at Step 1), with named launch analyses and IRB-grade anonymisation (§12).
6. **An operating business brain** — HR/attendance, throughput and bottleneck analytics, micro-costing, closed-loop pharmacy and inventory, community outreach, and an executive dashboard (§13, §14).

## 1.2 The honest scale assessment

This is a **3–5 year programme for a small team**, not a 6-month build. The blueprint is internally coherent and unusually well specified for a clinical product — but it describes, in scope terms, something comparable to a commercial EMR plus a clinical research platform plus a CRM plus an ERP-lite. The plan below therefore does two things at once:

- It **sequences** the work so that a genuinely useful clinic system is live and reducing Dr. Nahid's cognitive load at the end of Phase 1, not at the end of the programme.
- It **refuses to compress** difficult features into vague checkpoints. Where a feature is hard (OCR of mixed Bangla/English lab reports; offline conflict handling; deterministic drug safety), it is split into multiple small, individually testable checkpoints, and the unresolved decisions inside it are named rather than silently invented.

**132 checkpoints** are defined. Each is small enough to implement and verify in isolation, each has objective acceptance criteria, and each states explicitly what is *not* in scope so review stays bounded.

## 1.3 The five decisions that gate everything

Five open decisions block or reshape large parts of the roadmap. They are detailed in §3 and summarised here because they should be resolved before CP01 is approved, or at latest during Phase 0:

| # | Decision | What it blocks | Recommendation |
|---|----------|----------------|----------------|
| D-01 | **Legal/regulatory posture under the Bangladesh Personal Data Protection Act, 2026** — hosting region, cross-border transfer of NID/biometric/health data, consent design, appointment of a Chief Data Officer | Hosting, AI provider choice, OCR provider choice, NID capture, backups, research export | Obtain written legal opinion in Phase 0; design for **data-residency-flexible deployment** so a move to in-country hosting is a config change, not a rewrite |
| D-02 | **AI provider & deployment model** — **provider now decided: Google Gemini.** Open part: free tier vs paid tier / Vertex AI | §7 agent roster, §7.1 synthesis SLA, cost model, PHI handling | Free tier for **development and evaluation only**; paid Gemini API or Vertex AI for anything touching a real patient, because Google's free-tier terms permit training on and human review of submitted content (D-07) |
| D-03 | **OCR/NLP engine strategy** (§16.4) | All of Phase 2 records digitisation | Two-week **bake-off spike (CP98)** against real DTHC documents before committing; recommended default is cloud OCR for layout+text plus schema-constrained LLM extraction, with human validation |
| D-04 | **Pediatric growth reference** — WHO vs CDC vs local, per age band (§16.2) | Percentile engine, childhood obesity flag, physician dashboard | WHO Growth Standards 0–5y + a single explicit choice for 5–19y; **Dr. Nahid's clinical decision, not the developer's** |
| D-05 | **Drug knowledge base source** — curated formulary vs licensed commercial database | Medication safety engine, interaction checks, renal dosing | Option A (curated, ~150–250 items, physician-approved) for Phase 1 exactly as the blueprint recommends; licensed database revisited at Phase 3 |

## 1.4 The three architectural positions taken in this plan

**(a) Modular monolith first, not microservices.** §15.1 says "Golang microservices." For a clinic with ~20 concurrent operators, splitting into microservices on day one buys distributed-systems pain and buys nothing in throughput. This plan builds **one Go binary with hard internal module boundaries** (enforced by import rules in CI) that runs in three modes — `api`, `worker`, `realtime` — plus **one genuinely separate service** where the language boundary is real: the Python **ML/OCR service**. Modules are drawn on the seams a future extraction would use, and §5.6 gives objective triggers for when to split.

**(b) Event sourcing where it earns its keep, not everywhere.** Clinical writes, corrections, prescriptions and consent are event-sourced with an immutable ledger. Reference data (formulary, templates, users, inventory levels) is ordinary versioned CRUD with audit triggers. Full event sourcing of inventory and HR would add cost with no clinical or medico-legal benefit. §7 defines the boundary precisely.

**(c) Offline-first is a Phase 1 requirement, not a Phase 4 polish.** §15.2 says a Wi-Fi drop must never lose a station entry. Retrofitting offline into a mobile app is a rewrite, so the mobile data layer is built offline-capable from **CP64**, before the first clinical station ships.

## 1.5 What Phase 1 delivers

At the end of Phase 1 the clinic runs on DTHCMS for real patients: mobile attributed entry at every station, instant derived values with dual units, counseling checklist with the fail-closed gate, live traffic board, correction workflow with the "who entered this" chip, asynchronous AI synthesis ready before Step 9, the physician dashboard, and the four-colour A4 prescription with graphs, warning library, back page and QR — with a full audit ledger behind all of it.

## 1.6 The single largest risk

**Not technology — clinical content ownership.** The counseling templates, the drug warning library (§9.2), the red-line abnormality rules (§6.4), the formulary and its monthly price review (§16.1), the exact Bangla wording of the non-endocrinologist notice (§9.3), and the growth-reference protocol (§16.2) are all **physician-authored content**. No developer can invent them safely. The roadmap therefore schedules **content-authoring windows** as first-class dependencies (CP55, CP75, CP77, CP86, CP108) — if that content is late, those checkpoints stall regardless of engineering velocity.

---

# 2. WHAT THE BLUEPRINT DEFINES (CONFIRMED REQUIREMENTS)

Everything in this section is treated as **settled** and needs no further confirmation. Requirement IDs map to Appendix A of the blueprint; § references are to the blueprint.

## 2.1 Non-negotiable design principles (§2)

| ID | Principle | Engineering consequence |
|----|-----------|------------------------|
| P-1 | **Mobile-first capture** [R-01] | Every station capture UI is a React Native phone screen. Web is for physician, admin, QA, research — not floor capture. |
| P-2 | **Universal attribution** [R-03] | Universal write envelope `{value, unit, user_id, device_id, role, station, timestamp}`. No write path may bypass it — enforced at the repository layer, not by convention. |
| P-3 | **Role-flexible devices** [R-02] | Roles bind to session, not hardware. In-app role switching without logout; each event stamps the role *active at write time*. |
| P-4 | **Instant computation** | BMI, BMR, ideal weight, percentiles, eGFR computed synchronously in the write response — client-side optimistic + server-authoritative. |
| P-5 | **Patient always arrives ready** [R-05, R-07] | Asynchronous synthesis queue with SLA; counseling gate before Step 9. |
| P-6 | **Dual-unit humanity** [R-08] | Storage in SI units; display component renders cm with ft/in beneath, kg with lb, everywhere. |
| P-7 | **Fail-closed QA** | File cannot close with missing mandatory data (diabetic without HbA1c recorded or ordered). Blocking, not advisory. |
| P-8 | **AI drafts, physician decides** | Generative AI restricted to summarisation/drafting. Prescribing logic deterministic. Digital signature mandatory. Permanent constraint. |
| P-9 | **Bangladesh context by default** | Bangla/English bilingual UI + outputs, BDT pricing, local trade names, local diet plans, SMS-first fallback. |

## 2.2 The 12 stations (§3)

Confirmed station list, personnel, data captured, AI role, and checkpoint per station:

| Step | Station | Key data | Hard checkpoint (blueprint's own) |
|------|---------|----------|-----------------------------------|
| 1 | Registration | Demographics, NID, photo, socio-economic baseline, emergency contact, **validated exact DOB** | Strict duplicate prevention |
| 2 | Anthropometry & Screening | Height, weight, waist/hip, body fat %, muscle mass | Reject impossible inputs |
| 3 | Counseling & Lifestyle | Pack-years, alcohol, sleep, stress indices, motivation, triggers, activity + counseling checklist | Composite Lifestyle Risk Score |
| 4 | Medical History | Complaints, comorbidities, family/surgical history, allergies, drugs, vaccinations | **Hard stop on allergies** — explicit NKA or coded entry |
| 5 | Clinical Exam & Vitals | BP, pulse, RR, temp, SpO₂, diabetic foot, neuropathy, retinopathy, CVS | Critical-value alerts (SpO₂<92%, BP>180/110) visual **and audible** |
| 6 | Medical Records Import | Scan everything; printed→pipeline, handwritten→image only | Chronological ordering, red-lines |
| 7 | Nutrition | 24-h recall, calories, meal timing, food habits | Local-food diet plans respecting renal/hepatic status |
| 8 | Exercise | Mobility, joints, baseline fitness | Contraindication-aware routine |
| 9 | Physician Consultation | Synthesis already complete | Physician approves/modifies every AI suggestion |
| 10 | QA Review | Automated checklist | Fail-closed clearance for printing |
| 11 | Prescription Education | Insulin/device technique, prior compliance | Recorded demonstration |
| 12 | Follow-Up & Monitoring | CRM, CGM sync, PROs, predictive outreach | Two-way clinic line |

## 2.3 Concurrency, attribution, governance (§4)

- WebSocket + Redis, event sourcing; **event log is the source of truth**, not the state table.
- Every reviewable value in every UI shows an **"Entered by" chip**.
- Correction workflow: flag → author identity shown immediately → correction request routed to author (supervisor override) → **new event referencing the original, original never deleted** → error tallied to operator quality record → recurrence patterns surface on the HR dashboard.
- RBAC with named examples: nutritionist (write diet, read labs, **no prescriptions**); registration (demographics, **blinded to sensitive diagnoses**); pharmacist (**drug list + dosing only, diagnoses hidden**).
- Review hierarchy assistant → junior doctor → Chief Consultant; **critical findings bypass the queue**.
- Append-only audit log, human-readable ("10:42 — JD_04 changed systolic BP 140 → 145").

## 2.4 Waiting-time counseling (§5) [R-07]

- Per-diagnosis templates; **diabetes template is the launch minimum** with 7 named seed items (disease understanding, complications awareness, food/diet, exercise, chronic self-care, glucometer use, insulin sites/technique).
- Templates **physician-configurable without code changes**.
- Room-based flow (Counseling → Nutrition → Insulin Corner, sequence configurable) with a **live Clinic Traffic Control board** that dynamically reroutes when a station backs up.
- Mobile ticking with full attribution.
- Physician verification view; unticked mandatory items impossible to miss.
- **Fail-closed gate**: no queueing to Step 9 until mandatory items ticked.

## 2.5 Records digitisation (§6) [R-09, R-10]

- Scan **every** document, no exception.
- **Handwritten prescriptions excluded from structured chronology** — stored as reference images, OCR not attempted. (This is a hard requirement, and a helpful one: it removes the single hardest OCR problem from scope.)
- For printed documents extract: date, facility, test/report type, reason (when stated), exact values.
- Absolute chronological ordering → single cross-provider investigations timeline.
- AI narrative problem history feeding the one-page synthesis.
- Red-line highlighting with named seed rules: raised creatinine and/or urinary pus cells; **eGFR auto-derived by CKD-EPI 2021** when creatinine+age+sex available; elevated BP values and series; PCO ultrasound — ovarian size/volume, cyst/follicle count, **computed interval change across serial scans stated explicitly**.
- Extracted values populate the longitudinal timeline.

## 2.6 AI framework (§7)

- Specialised sandboxed agents coordinated by the backend, **never one monolith**.
- Synthesis trigger model: final assistant presses one button; job queues asynchronously; **target SLA ≤5 minutes (tunable in testing)**; physician never waits.
- Agent roster (10): Medical Scribe, Clinical Assistant, Diagnostic Support, **Medication Safety Engine (deterministic, not generative)**, Nutrition Assistant, Exercise Assistant, Follow-Up Predictor, Outcome Monitoring & QA, Research Assistant, Global Knowledge/Guideline (Automated CME) with **weekly Clinical Digest batching and instant push only for critical safety recalls**.
- Endocrine-only tailoring for now [R-06].
- Safety split is permanent: generative = summarise/draft; prescribing = deterministic + mandatory human signature.

## 2.7 Physician dashboard (§8)

Zero raw forms. Three panels + timeline: Left snapshot (demographics, vitals, BMI, **sparklines of last 5 HbA1c**, active diagnoses, critical alerts, pediatric percentile card, counseling status); Centre one-page narrative with red-lines carried through; Right AI assistant (ICD-coded suggestions, missing-data alerts, drafted investigations/doses reflecting Dr. Nahid's historical prescribing patterns). Longitudinal **scrubbable stock-chart-style timeline** with hover-to-see-attribution.

## 2.8 Prescription (§9) [R-08, R-11, R-12, R-13]

- A4, **four-colour laser**, front + back page.
- Front: pre-printed clinic header, demographics, ICD-coded diagnoses, structured Rx, lifestyle advice, **QR verification**; bold medicine names; distinct dosages; typography readable by visually impaired patients; Bangla for patient-facing text.
- **Graphs on the prescription** [R-11]: HbA1c/weight/BP sparklines + full-colour gradient bars positioning BMI/HbA1c.
- **Patient-reported 1–10 improvement score** each visit, printed and plotted over time.
- Dual units; red = stop/danger, green = target achieved; QR to localized instruction videos.
- **Drug-specific warning library** [R-12], physician-authored, Bangla + English, auto-attached on match. Seed: GLP-1 (first-dose GI effects, transient, hydration, when to call), CNS/psychotropics (never stop abruptly; physician-only dose change), hypnotics (PRN only).
- **Back page** [R-13]: clinical assessment, plan/next steps, research & guideline rationale, plus the standing polite notice to non-endocrinologists (exact wording to be authored by Dr. Nahid; template reserves the block).

## 2.9 Medicine index (§10) [R-16]

Two-letter trade-name autocomplete returning generic, strength, form, **BDT unit price**; speed is a hard requirement. Option A (curated ~100–200 items) recommended for Phase 1; Option B (full database) revisited at Phase 3. Every prescribed/dispensed price flows into research metadata.

## 2.10 CRM (§11) [R-14]

Visit memory (date, chief complaint, diagnoses, plan, next review interval, queryable forever); contact preference captured **at checkout** (preferred call window, consent for calls and SMS, number to use); due follow-ups generate call tasks at preferred time; unanswered call → automatic SMS fallback, logged; two-way clinic line with inbound triage queue attaching to the record; predictive flagging of no-show/failure/deterioration risk.

## 2.11 Research (§12) [R-15]

Named launch analyses as **saved, refreshable dashboards**: HbA1c trajectories; exercise–outcome correlation; GLP-1 RA safety (AE %, incidence by starting dose, empirical dose-initiation analysis); GLP-1 RA efficacy (weight-loss response, semaglutide and tirzepatide); affordability & persistence with discontinuation-for-cost; multi-benefit dashboards (semaglutide/tirzepatide across diabetes, hypertension, obesity, dyslipidemia) and parallel dashboards for SGLT2 inhibitors (dapagliflozin, empagliflozin, canagliflozin) and linagliptin. Bangladesh pricing is a standing axis. Plus the Automated Hypothesis Engine, positive-deviance mining, and **strict PII stripping before any editorial review**. Stated targets: 2+ papers/audits monthly, 2 books annually.

## 2.12 Community outreach (§13)

React Native tablet field app; screening battery (height, weight, BMI, waist, BP, blood grouping, RBS, eye screening, family health assessment); **instant colourful printed/digital field report**; in-field diagnostics (HbA1c, Vit D/B12, OGTT collection); high-risk cohort monitoring; instant sync creating screening records, updating timelines, flagging abnormals for triage, auto-scheduling clinic follow-ups; population intelligence feeding surveillance and prevalence studies.

## 2.13 Governance, HR, finance (§14)

AI reviews **100%** of files; quality dashboards (wait times, QA discrepancy rates, guideline compliance); **Code Red closed-loop negative-outcome RCA** with forced systemic prevention update; biometric attendance synced with RBAC; station-to-station timestamp friction analysis and operator throughput scoring; error-quality linkage from §4.3; Patient Satisfaction Matrix correlated to specific operators; **Google Maps geospatial fleet management**; micro-costing (per-operator daily cost from biometric shift duration × throughput, philanthropic footprint, outreach logistics); closed-loop pharmacy with real-time inventory deduction and **diagnoses hidden by RBAC**; predictive supply chain; executive "CEO Co-Pilot" dashboard with auto-generated summaries, dynamic agendas, KPI tracking, macro-RCA, auto-assigned action items, quarterly Organizational Learning Matrix.

## 2.14 Confirmed technical stack elements (§15.1, §15.2)

Go backend on Google Cloud; PostgreSQL/AlloyDB with append-only event store as source of truth; React + Next.js web; React Native mobile/tablet; Redis + WebSockets; HL7 FHIR; RBAC + 2FA; explicit consent tracking; **immutable geo-redundant backups every 5 minutes**; Android-first operator devices; offline-tolerant write queue; async job infra with queue monitoring; bilingual i18n across UI/prescriptions/SMS/field reports; notification service with call tasks + programmable SMS gateway; four-colour A4 print pipeline including back page; the universal entry envelope.

## 2.15 Phase structure (§15.3)

Phase 0 prototype baseline → Phase 1 Clinic Core (MVP) → Phase 2 Memory & Relationship → Phase 3 Intelligence → Phase 4 Enterprise. **Phase acceptance = features live + audit trail verified end-to-end + Dr. Nahid's sign-off; no phase closes with open critical defects.** This plan preserves that structure exactly.

## 2.16 Requirement → checkpoint traceability

| Req | Short form | Primary checkpoints |
|-----|-----------|---------------------|
| R-01 | Mobile-first station entry | CP11, CP33, CP45, CP49, CP51, CP56, CP59, CP60 |
| R-02 | Multi-role on one device | CP19, CP41 |
| R-03 | Per-entry attribution | CP18, CP23, CP24, CP61 |
| R-04 | Error traceability & correction | CP62, CP63, CP140 |
| R-05 | Async pre-consultation synthesis | CP69, CP70, CP71 |
| R-06 | Endocrine-only AI; pediatric percentiles | CP47, CP48, CP70, CP71 |
| R-07 | Waiting-time counseling | CP55, CP56, CP57, CP40 |
| R-08 | Dual units | CP44, CP89 |
| R-09 | Records digitisation | CP96–CP107 |
| R-10 | Red-line abnormalities | CP108, CP109 |
| R-11 | Prescription graphs + patient score | CP87, CP88 |
| R-12 | Drug warning library | CP86 |
| R-13 | Back-page rationale | CP90 |
| R-14 | Follow-up CRM | CP111–CP117 |
| R-15 | Named research analyses | CP121–CP131 |
| R-16 | Medicine index + pricing | CP75, CP76 |
| R-17 | Dun & Bradstreet account | Admin item A-01 (§21.6) — not an engineering checkpoint |

---

# 3. OPEN DECISIONS / SPECIFICATION GAPS

**How to read this register.** Each entry states what the blueprint says, what is missing, the realistic options, a recommendation where one is defensible, and a **confirmation class**. Nothing in this register has been decided by me. Where a decision is marked `CLINICAL`, `LEGAL` or `SECURITY`, implementation must not proceed on assumption — the decision belongs to Dr. Nahid or to a qualified professional.

**Confirmation classes:**
`LEGAL` — requires qualified legal counsel · `CLINICAL` — requires Dr. Nahid's clinical authority · `SECURITY` — affects the security posture · `ARCH` — architectural, reversible only at high cost · `COMMERCIAL` — vendor/contract/cost · `CONTENT` — physician-authored content, not a technical choice · `OPS` — operational/hardware.

**Status legend:** 🔴 blocks a Phase 0/1 checkpoint · 🟠 blocks a Phase 2/3 checkpoint · 🟡 needed before the relevant phase, not urgent.

---

## 3.A LEGAL & REGULATORY (highest priority — these were not in the blueprint at all)

### D-01 · Bangladesh Personal Data Protection Act, 2026 compliance posture 🔴 `LEGAL` `ARCH`
**Blueprint says:** §15.1 mentions "cloud-native on Google Cloud" and §16.8 lists "Hosting region, data residency, and backup policy formal sign-off" as an open item. It does not mention any data protection statute.
**What is missing:** The blueprint predates or omits the regulatory reality. Public reporting indicates Bangladesh enacted a **Personal Data Protection Act in April 2026** (replacing a November 2025 ordinance), overseen by a **National Data Management Authority**, with explicit consent requirements, restrictions on cross-border transfer — reportedly heaviest for **unique identifiers (National ID, passport, TIN) and biometric/genetic data** — breach notification duties, a Chief Data Officer obligation for significant controllers, and administrative fines. DTHCMS captures **National ID (§3 Step 1), photographs/biometrics, fingerprint/facial attendance (§14.2), and bulk health data**, and proposes to send clinical text to third-party AI providers. That combination is squarely inside the strictest part of any such regime.
**Why this is decision #1:** It determines (a) whether Google Cloud regions outside Bangladesh are usable at all, (b) whether clinical text may leave the country for LLM/OCR processing, (c) whether NID images may be stored or must be hashed/discarded after verification, (d) whether the research export path needs formal ethics approval, (e) whether DTHC must appoint a Chief Data Officer.
**Options:**
- **A — Full offshore cloud (asia-south1 Mumbai / asia-southeast1 Singapore).** Cheapest, fastest, best managed services. Requires a defensible legal basis for cross-border transfer.
- **B — Offshore cloud with a data-classification split.** Identifiers and NID/biometric artefacts pinned to in-country storage or never stored; de-identified clinical payloads processed offshore. Moderate complexity.
- **C — In-country hosting** (Bangladeshi data centre / BCC national data centre / local provider). Maximum compliance comfort, materially worse managed-service availability, more DevOps burden, weaker DR options.
- **D — Hybrid:** in-country primary for identity + documents; offshore for compute/analytics on pseudonymised data.
**Recommendation:** Do **not** pick a hosting model until counsel answers three questions in writing: is health data "sensitive personal data" under the Act; what lawful basis and safeguards permit cross-border processing of health data and NID-derived data; and does DTHC qualify as a "significant" controller requiring a Chief Data Officer. **Engineering mitigation available immediately:** build to Option B's shape from CP03 — a single `DataClass` tag on every storage path (`IDENTIFIER`, `CLINICAL`, `DOCUMENT`, `DERIVED`, `ANALYTIC`) so relocating one class later is configuration plus a migration, not a redesign. This costs ~3 developer-days now and can save months.
**Confirmation required from:** Dr. Nahid + Bangladeshi data-protection counsel. *(The summary above is drawn from secondary sources and is not legal advice.)*

### D-02 · Consent model, scope and revocation 🔴 `LEGAL` `CLINICAL`
**Blueprint says:** "explicit consent tracking" (§15.1); consent for calls and SMS at checkout (§11.2); IRB-grade anonymised research dashboards (§12).
**What is missing:** What exactly is consented to, by whom, in what language, with what evidence, and what happens on withdrawal. Specifically: is research use opt-in or opt-out? Does withdrawal remove already-published cohort data? Who consents for minors? Is verbal consent with staff attestation acceptable, or is a signature/thumbprint required? Is a separate consent needed for AI processing of the record?
**Options:** (i) Single blanket consent at registration; (ii) **layered consent** — care, communication (calls/SMS), research use, AI processing, community-outreach follow-up, each independently grantable and revocable; (iii) care-only consent with research handled under a separate IRB protocol.
**Recommendation:** **(ii) layered consent**, versioned (every consent record stores the exact template version and language shown), with electronic capture of signature or thumbprint plus witnessing operator attribution. Model withdrawal as a first-class event that immediately gates communications and future research inclusion; the treatment of already-analysed data is a legal/ethics answer, not an engineering one.
**Confirmation required.**

### D-03 · Research ethics / IRB pathway 🟠 `LEGAL` `CLINICAL`
**Blueprint says:** "anonymized dashboards meeting IRB-grade integrity" (§12); 2+ papers monthly.
**What is missing:** Which ethics committee reviews DTHC research (institutional ERC, BMRC, national IRB)? Are the launch analyses (§12.1) retrospective audits of routine care (usually lighter review) or prospective research (full review)? Journal submission will require a stated approval number.
**Recommendation:** Establish the ethics pathway during Phase 1 so that Phase 3 dashboards are not built against the wrong data-governance model. Engineering-side, build the anonymisation pipeline (CP121) to satisfy the strictest plausible standard: no direct identifiers, date-shifting, k-anonymity checks on quasi-identifiers, and a documented re-identification key held separately.
**Confirmation required.**

### D-04 · Medico-legal status of the digital prescription and signature 🔴 `LEGAL` `CLINICAL`
**Blueprint says:** "every prescription requires the physician's digital signature" (§2, §7.3); QR verification (§9.1); an explicit anti-tamper intent (§9.3).
**What is missing:** Whether "digital signature" means (a) an authenticated in-app approval with cryptographic record, (b) a rendered image of a handwritten signature, or (c) a legally recognised digital signature under Bangladesh's electronic signature framework (issued by a licensed Certifying Authority). Also: retention period for prescriptions and clinical records; whether a printed signed copy is the legal artefact and the digital record merely evidentiary.
**Options:** **A** — application-level approval + server-side cryptographic signature over a canonical prescription payload (Ed25519 key held in Cloud KMS/HSM), plus signature image for human readability and QR verification against the signed hash. **B** — CA-issued qualified certificate integration. **C** — image only (weakest; not recommended).
**Recommendation:** **A now, designed so B is an upgrade** — sign a canonical JSON of the prescription, store the signature and key ID in the event ledger, print the hash inside the QR. This gives genuine tamper evidence regardless of the legal question, and a CA certificate can later sign the same canonical payload.
**Confirmation required (legal).**

### D-05 · Clinical record retention & deletion policy 🟠 `LEGAL`
**Blueprint says:** immutable append-only records; 5-minute geo-redundant backups (§15.1).
**What is missing:** How long records are retained; how a data-subject erasure request is reconciled with an append-only medico-legal ledger; retention for scanned third-party documents, call recordings (if any), and biometric attendance templates.
**Recommendation:** Retention schedule per data class, with erasure implemented as **crypto-shredding** (per-patient data encryption key destroyed) rather than row deletion, so the ledger's hash chain stays intact and auditable. Requires legal confirmation that this satisfies erasure obligations.
**Confirmation required.**

### D-06 · Ownership of the "do not alter this therapy" notice and clinical liability 🟡 `LEGAL` `CONTENT`
**Blueprint says:** §9.3 reserves a template block; exact Bangla/English wording to be authored by Dr. Nahid.
**What is missing:** Wording, and whether a categorical instruction to other physicians creates any liability exposure.
**Recommendation:** Dr. Nahid authors; counsel reviews once. Engineering treats it as versioned template content (CP90), never hardcoded.

---

## 3.B ARTIFICIAL INTELLIGENCE

### D-07 · LLM provider and model — **PROVIDER DECIDED: GOOGLE GEMINI** 🔴 `SECURITY` `LEGAL` `COMMERCIAL`
**Blueprint says:** §16.5 — "AI model/provider strategy for the synthesis pipeline and agents; confirm the ≤5-minute SLA after load testing." No provider named.
**Decision taken (22 Aug 2026):** **Google Gemini.** Architecturally this is a sound choice and fits the plan unchanged — the AI Gateway (CP70) already treats the provider as configuration, Gemini is available in Bangladesh, and using Google for both cloud and AI keeps one contract and one residency conversation.
**The open part is not the provider — it is the tier.** "Free tier" was specified. Google's Gemini API Terms draw a hard line between *Unpaid Services* (the free tier) and *Paid Services*:

| | **Free tier (Unpaid Services)** | **Paid tier / Vertex AI** |
|---|---|---|
| Training on your data | **Yes** — "Google uses the content you submit to the Services and any generated responses to provide, improve, and develop Google products and services" | **No** — "Google doesn't use your prompts… or responses to improve our products" |
| Human review | **Yes** — "Human reviewers may read, annotate, and process your API input and output" | No, beyond abuse detection |
| Google's own instruction | **"Do not submit sensitive, confidential, or personal information to the Unpaid Services."** | Data Processing Addendum applies |
| Rate limits | Small per-model RPM/TPM/RPD caps, visible in AI Studio; 429 on exceed | Tiered, raised by spend |
| SLA | None | Contractual |
| Region pinning | Not offered | Available on Vertex AI |

**What this means for DTHCMS:** patient summaries, records extraction, scanned documents, and dictated notes are health data — the most regulated category, and directly in scope of the Bangladesh Personal Data Protection Act, 2026 (D-01). Sending them to the free tier would breach **Google's own terms**, not merely a preference of mine, and would mean human reviewers outside Bangladesh may read DTHC patients' clinical data. **This is a hard stop for production.**

**Options:**
- **A — Free tier for development and evaluation only; paid Gemini API (Tier 1) for production.** Billing enabled on the same API; no training, no human review, DPA applies. Upgrade is instant once billing is set up.
- **B — Free tier for development; Vertex AI (Google Cloud) for production.** Same Gemini models, enterprise terms, **region pinning** (e.g. asia-south1 Mumbai / asia-southeast1 Singapore), single GCP contract and billing alongside the rest of the infrastructure, and the cleanest answer to D-01.
- **C — Free tier everywhere.** Only viable if **no** real patient data ever reaches the model — i.e. AI features are limited to synthetic data and demos. The clinic gets no AI in production.
- **D — Self-hosted open model** for production, free tier for development. Full data control, GPU cost and MLOps burden, materially weaker synthesis quality.

**Recommendation: B, with A as the fallback if Vertex setup is a burden early on.** Concretely:
- **All development, CI, testing, demos and prompt iteration run on the free tier against synthetic patients** (CP13's generator exists precisely for this). This is a legitimate and valuable use of the free tier and costs nothing.
- **The moment a real patient's data is involved, the gateway routes to Vertex AI (or the paid Gemini API).** CP70 makes this a configuration switch and a hard runtime guard, not a matter of remembering.
- PHI minimisation (D-08) still applies on the paid tier — belt and braces.
- Per-agent model tiering: a Flash-class model for high-volume, low-risk work (document classification, extraction, digest assembly); a Pro-class model for the pre-consultation synthesis, where quality determines whether Dr. Nahid trusts the system at all.

**Confirmation required from Dr. Nahid:** which of A / B / C. If the answer is C, the plan still works — but every AI checkpoint (CP70–CP72, CP109, CP130–CP137) delivers a system that runs on synthetic data only, and the clinic's AI features stay dark. I will not route real patient data through the free tier under any circumstances.

**Model pinning caveat:** free-tier and preview model aliases are deprecated quickly. Every agent pins an explicit model version (D-13), and the evaluation set (CP72) is what tells us whether a forced model migration has degraded clinical quality.

### D-08 · What actually goes to the model (PHI minimisation) 🔴 `SECURITY` `LEGAL`
**Blueprint says:** nothing explicit.
**What is missing:** Whether the synthesis prompt may contain name, NID, phone, address, exact DOB, and free-text that may contain identifiers.
**Recommendation:** **Default deny — and this is now more important, not less, given D-07's tier question.** The AI Gateway strips direct identifiers and substitutes a per-request pseudonym plus age-in-months, sex, and clinical payload; identifiers are re-attached client-side after the response returns. Free text passes through a PII scrubber with a human-reviewable log. This is cheap to build at CP70 and near-impossible to retrofit once 10 agents depend on the gateway.
**Confirmation required.**

### D-09 · AI orchestration architecture 🟠 `ARCH`
**Blueprint says:** "Specialized, sandboxed agents coordinated by the backend — never one hallucination-prone monolith" (§7).
**What is missing:** Whether "agents" means autonomous tool-using loops or deterministic pipelines with LLM steps.
**Options:** **A** — deterministic Go-orchestrated pipelines; each "agent" is a named, versioned prompt + schema + validator, invoked in a fixed sequence with no autonomous tool use. **B** — agent framework with tool calling and dynamic planning. **C** — mixture.
**Recommendation:** **A.** In a clinical setting, non-determinism in *control flow* is a much bigger risk than non-determinism in text. Deterministic pipelines are testable, replayable, and auditable; every step's input and output is stored. Tool-calling can be introduced later for the Research Assistant (§12.2), which is the only genuinely exploratory agent.
**Confirmation recommended (architectural).**

### D-10 · RAG architecture, embedding model, vector store 🟠 `ARCH`
**Blueprint says:** §7.2 — Global Knowledge & Guideline Engine ingests ADA/EASD/WHO/PubMed; §8 — AI suggestions reflect "Dr. Nahid's historical prescribing patterns and current guidelines."
**What is missing:** Everything: corpus sources and their licences, chunking, embedding model, vector store, retrieval evaluation, refresh cadence.
**Options for the store:** **A — `pgvector` in the existing PostgreSQL** (no new infrastructure, transactional consistency, ample for a corpus in the 10⁵–10⁶ chunk range). **B** — managed vector DB (Vertex Vector Search, Pinecone, Qdrant Cloud). **C** — self-hosted Qdrant/Weaviate.
**Recommendation:** **A (`pgvector`)**. It is very unlikely this corpus outgrows Postgres, and one fewer datastore is a real operational saving. Revisit only if retrieval latency at p95 exceeds budget with a realistic corpus.
**Also required and currently undecided:** whether guideline PDFs may be redistributed/stored (ADA Standards of Care and similar are copyrighted; ingestion for internal retrieval is usually acceptable, redistribution is not) — `LEGAL`, and whether "Dr. Nahid's historical prescribing patterns" means retrieval over his own past prescriptions (recommended, straightforward, and auditable) or a fine-tuned model (not recommended: expensive, opaque, hard to audit, hard to correct).

### D-11 · Bangla-language AI capability 🟠 `CLINICAL` `ARCH`
**Blueprint says:** bilingual outputs, Bangla patient instructions, Bangla SMS.
**What is missing:** Whether Bangla text is (a) generated by the LLM, (b) drawn from a pre-approved translated phrase library, or (c) machine-translated then physician-reviewed.
**Recommendation:** **(b) for anything printed or sent to a patient.** Patient-facing Bangla (drug warnings, prescription instructions, SMS templates, field reports) comes from a **physician-approved bilingual content library** with version control — never generated at run time. LLM-generated Bangla may be used only in internal drafts a physician reads before release. This is both a safety and a quality decision: clinical Bangla phrasing errors are unacceptable and unreviewed at scale.
**Confirmation required (clinical).**

### D-12 · Speech-to-text for the AI Medical Scribe 🟡 `ARCH` `COMMERCIAL` `LEGAL`
**Blueprint says:** §7.2 — "ambient conversation → structured SOAP notes."
**What is missing:** Ambient recording of a consultation is legally and ethically the most sensitive feature in the entire system. Also missing: whether the consultation is in Bangla, English, or code-switched (realistically code-switched, which is the hardest case for STT), microphone hardware, retention of audio, and patient consent.
**Options:** cloud STT (Google Chirp/Speech-to-Text, Azure, Deepgram, ElevenLabs Scribe, OpenAI Whisper API) vs self-hosted Whisper-family models; text-only fallback (physician dictation post-consultation rather than ambient capture).
**Recommendation:** **Defer to Phase 3 (CP133) and pilot in dictation mode first** — physician dictates a summary after the consultation rather than recording the patient encounter. This removes the consent and retention problem almost entirely while delivering most of the time saving. Ambient capture only after explicit patient consent design and legal review. Expect Bangla-English code-switched accuracy to require a dedicated evaluation set.
**Confirmation required.**

### D-13 · Model versioning, evaluation, and regression policy 🟠 `SECURITY` `CLINICAL`
**Blueprint says:** nothing.
**What is missing:** How to detect that a provider's silent model update has degraded clinical summaries.
**Recommendation:** Pin explicit model versions; never use floating "latest" aliases. Maintain a **frozen evaluation set** of de-identified real cases with physician-graded reference outputs; run it on every prompt change, model change, and weekly on the pinned model; block deployment on regression beyond an agreed threshold. **The threshold itself is a proposed value requiring Dr. Nahid's approval** (§17.4) — I will not invent a clinical accuracy number.

### D-14 · AI cost governance 🟡 `COMMERCIAL`
**What is missing:** Budget per patient encounter. With Gemini chosen (D-07), a Flash-class model for high-volume work and a Pro-class model only for synthesis is the main cost lever; the free tier's role is development, not production economics.
**Recommendation:** Meter every AI call with token counts and cost per patient/agent/model; set a monthly budget with alerting at 60/80/100%; cache aggressively (records summaries are recomputed only when new documents arrive). A realistic Phase 1 order-of-magnitude for a synthesis pipeline over one encounter is a few US cents to a few tens of cents depending on model tier and record volume — **this must be measured at CP71, not assumed**.

### D-15 · AI failure behaviour 🔴 `CLINICAL` `ARCH`
**What is missing:** What the physician sees when the AI is down, slow, or low-confidence.
**Recommendation:** **Fail visible, never fail silent, never fail invented.** If synthesis is incomplete at Step 9, the dashboard shows a clear "AI summary unavailable — raw structured data shown" state with all station data rendered directly. The clinic must be able to run entirely without AI. **Confirmation required** that this degraded mode is acceptable to Dr. Nahid.

---

## 3.C OCR / NLP

### D-16 · OCR engine and deployment model 🟠 `ARCH` `LEGAL` `COMMERCIAL`
**Blueprint says:** §16.4 — "OCR/NLP approach for records digitization: cloud service vs local model; Bangla-capable OCR requirement." Explicitly open.
**What is missing:** Everything except the exclusion of handwritten prescriptions, which is settled and helpful.
**Candidate architectures (full comparison in §11 of this plan):**
- **A — Cloud document AI** (Google Document AI / Cloud Vision, Azure Document Intelligence, AWS Textract). Best-in-class layout and table extraction, strong printed-English accuracy, managed. Bengali print support varies by product and must be tested, not assumed. Sends documents offshore → collides with D-01.
- **B — Self-hosted open-source** (PaddleOCR, Surya, docTR, Tesseract + Bengali traineddata, or a self-hosted vision-language model). Full data control, no per-page cost, GPU and MLOps burden, quality highly document-dependent.
- **C — Vision-language model extraction** (send the page image to a multimodal LLM with a strict output schema). Often the strongest on messy real-world lab reports and mixed scripts, but non-deterministic and needs confidence handling and validation. **With Gemini already chosen as the AI provider (D-07), Gemini vision is now a first-class candidate here** — one provider, one contract, one residency conversation, and strong mixed-script performance is plausible. It must still win the CP98 bake-off on measured accuracy rather than on convenience, and the same tier rule applies: **scanned patient documents contain names and NID numbers, so they may never go to the free tier.**
- **D — Hybrid (recommended shape):** deterministic OCR for text+layout+tables → LLM/rule-based structured extraction into a strict schema → confidence scoring → human validation queue for anything below threshold.
**Recommendation:** **Do not choose now.** Run **CP98, a time-boxed bake-off** against ~200 real anonymised DTHC documents (Bangla lab report, English lab report, mixed-script report, imaging report, discharge summary, ECG/Echo, poor-quality phone photo, faded thermal print). Decide on measured character accuracy, field-extraction accuracy, cost per page, and latency. **This remains an OPEN DECISION until that bake-off reports.**
**Confirmation required after the spike.**

### D-17 · Bangla / mixed-script handling 🟠 `ARCH`
**What is missing:** Real Bangladeshi lab reports are frequently **English-dominant with Bangla headers, patient names in Bangla, and Bangla units/labels** — a mixed-script case that many OCR stacks handle poorly, plus Bangla's conjunct glyphs are a known accuracy weak point.
**Recommendation:** Treat mixed-script as the **primary** case in the bake-off, not an edge case. Script-detection per text block, per-block engine routing if the bake-off shows one engine wins on Bangla and another on English. Normalise Unicode (NFC), and handle Bengali digits (০–৯) explicitly in numeric extraction — a silent failure here corrupts lab values, which is a patient-safety issue.

### D-18 · Document classification & the handwritten exclusion 🟠 `CLINICAL`
**Blueprint says:** handwritten prescriptions are stored as images, not OCR'd (§6.1).
**What is missing:** How "handwritten" is determined — automatically or by the Medical Records Officer.
**Recommendation:** **Operator-declared at upload, with an automatic classifier as a cross-check that can only flag, never override.** A misclassified handwritten document entering the structured pipeline is exactly the failure mode Dr. Nahid excluded. Also undecided: handling of *partly* handwritten printed reports (printed lab report with handwritten annotations) — recommend OCR the printed portion, retain the image, flag that annotations exist.

### D-19 · Extraction confidence, validation and human review thresholds 🟠 `CLINICAL`
**What is missing:** The threshold above which an extracted value enters the clinical timeline unreviewed.
**Recommendation:** Two-tier: values above threshold auto-populate but are visually marked as OCR-derived and remain correctable; below threshold they go to a validation queue and are excluded from the timeline until confirmed. **Any numeric lab value that will drive a clinical rule (creatinine → eGFR, HbA1c → QA gate) requires human confirmation regardless of confidence, for Phase 2 at minimum.** The exact threshold is a **proposed value requiring approval**, tuned on bake-off data.

### D-20 · Medical entity extraction target schema 🟠 `CLINICAL`
**What is missing:** The canonical list of lab analytes, units, and reference ranges DTHC uses.
**Recommendation:** Dr. Nahid (or the lab lead) supplies the analyte dictionary — name variants seen locally, canonical name, unit, conversion factors, and DTHC reference ranges. **LOINC** codes are recommended for interoperability and are freely licensable, but the local name-variant table is the part that cannot be bought. This is `CONTENT` work that gates CP104.

---

## 3.D CLINICAL RULES & KNOWLEDGE

### D-21 · Pediatric growth reference standard 🔴 `CLINICAL`
**Blueprint says:** §16.2 — "WHO vs CDC vs local reference, per age band — Dr. Nahid to specify the clinic protocol." §3 Step 2 requires percentiles from exact age; childhood obesity flagged at **≥95th percentile** [R-06].
**What is missing:** The choice, and the age-band split.
**Options:** **A** — WHO Growth Standards 0–5y + WHO 2007 References 5–19y (WHO-consistent; note that WHO 2007 defines obesity at >+2 SD, which is ≈97.7th percentile, not 95th). **B** — WHO 0–5y + CDC 2000 for 2–19y (the ≥95th-percentile obesity definition in the blueprint is the CDC convention). **C** — a local/regional reference.
**Recommendation:** The blueprint's own ≥95th-percentile rule points to **Option B**, but this is a clinical protocol decision and I will not make it. Whichever is chosen, the engine (CP47) must store the reference source and version alongside every computed percentile, so historical values remain interpretable if the protocol changes.
**Confirmation required (clinical) — this blocks CP47.**

### D-22 · Drug knowledge base & interaction source 🔴 `CLINICAL` `COMMERCIAL` `LEGAL`
**Blueprint says:** §10.2 — Option A curated formulary (recommended) vs Option B full database integration; §7.2 — Medication Safety Engine is deterministic; §16.1 also asks **who owns monthly price review**.
**What is missing:** The source of interaction, contraindication, renal-dosing and pregnancy rules. A curated formulary gives you *names and prices*; it does not give you *interaction logic*.
**Options:**
- **A — Physician-curated deterministic rule set** scoped to the endocrine formulary. Total control and auditability; coverage limited to what is authored; the authoring burden falls on Dr. Nahid.
- **B — Licensed commercial database** (First Databank, Medi-Span/Wolters Kluwer, Micromedex, Elsevier). Comprehensive, maintained, medico-legally defensible — and expensive, typically five-figure USD annually, with contracts oriented to Western markets and no Bangladeshi trade-name coverage.
- **C — Open/public sources** (RxNorm + NLM interaction data, DrugBank academic/commercial tiers, openFDA labels, DDInter). Cheap, but licence terms vary sharply for commercial clinical use and coverage/maintenance is uneven; the NLM's own interaction API was discontinued in 2021, which is a cautionary example of depending on free clinical data sources.
- **D — A + C:** curated rules as the authoritative layer, public data used only as an authoring aid for Dr. Nahid, never as a runtime decision source.
**Recommendation:** **D.** It matches the blueprint's deterministic-prescribing principle exactly, keeps the physician as the author of every clinical rule (which is also the medico-legally strongest position), and defers a large licence cost. The engine must **fail closed and state its coverage boundary**: if a prescribed drug is outside the curated set, the UI must say "no automated safety check available for this drug" rather than showing a reassuring green tick.
**Confirmation required (clinical + commercial) — blocks CP78.**

### D-23 · Renal, hepatic, pediatric and pregnancy dosing rules 🟠 `CLINICAL`
**Blueprint says:** medication safety = drugs × renal function × allergies × contraindications × current medications (§7.2); nutrition respects renal/hepatic status (§3 Step 7).
**What is missing:** The rules themselves, and whether pregnancy and pediatric dosing are in scope at all (the clinic is endocrine; pregnancy is very much in scope for thyroid, PCOS, and gestational diabetes).
**Recommendation:** Per-drug structured rule records authored by Dr. Nahid: eGFR bands with action (dose reduce / avoid / monitor), hepatic caution, pregnancy/lactation category and action, pediatric applicability. eGFR by **CKD-EPI 2021** for adults as specified; **bedside Schwartz** for pediatric patients — needs confirmation. **Confirmation required (clinical).**

### D-24 · Terminology: SNOMED CT, ICD, LOINC 🔴 `LEGAL` `COMMERCIAL` `ARCH`
**Blueprint says:** §3 Step 4 — "chief complaints (SNOMED-CT structured)"; §8 — "suggested ICD-coded diagnoses"; §9.1 — ICD-coded diagnoses on the prescription.
**What is missing:** SNOMED CT is **not freely usable everywhere** — use requires an Affiliate licence, and free-of-charge use generally depends on the country being a SNOMED International Member (or qualifying under a low-income-country waiver). Whether Bangladesh confers free use must be verified with SNOMED International before any SNOMED content is embedded. ICD-10/ICD-11 are published by WHO under far more permissive terms.
**Options:** **A** — ICD-11 (or ICD-10) as the coding backbone + an internal DTHC concept dictionary for complaints, with SNOMED mapping added later if licensing is resolved. **B** — SNOMED CT subset under a verified Affiliate licence. **C** — free-text complaints with internal tagging only (loses structured research value).
**Recommendation:** **A.** It satisfies "ICD-coded diagnoses" literally, removes a licence blocker from the critical path, and keeps a mapping table so SNOMED can be layered on. LOINC for lab analytes (freely licensable) is recommended.
**Confirmation required (legal/commercial) — affects CP52.**

### D-25 · Clinical guideline source of truth 🟠 `CLINICAL` `LEGAL`
**Blueprint says:** §7.2 ingests ADA/EASD/WHO/PubMed updates.
**What is missing:** Copyright status of ingested guidelines; which guideline is authoritative when ADA and EASD differ; who approves a guideline change before it alters system behaviour.
**Recommendation:** Guidelines inform **advisory** output only, never automated action, and every guideline-derived statement carries a citation. A physician-approval step gates any guideline update that changes a rule. **Confirmation required.**

### D-26 · Lifestyle Risk Score and other composite indices 🟠 `CLINICAL`
**Blueprint says:** §3 Step 3 — "composite Lifestyle Risk Score for behavioral cohorting"; "validated stress indices."
**What is missing:** Which validated instruments (PSS-10? PHQ-9? DASS-21? — several are copyrighted with licensing terms), and the formula for the composite score.
**Recommendation:** Dr. Nahid specifies the instruments (and DTHC verifies their licence terms) and defines the composite formula; the engine stores instrument version and raw item responses, never only the derived score. **Confirmation required (clinical) — blocks the scoring half of CP58.**

### D-27 · Critical-value thresholds and escalation 🔴 `CLINICAL`
**Blueprint says:** SpO₂<92%, BP>180/110 with visual and audible alerts (§3 Step 5); critical findings bypass the queue to the Consultant (§4.4).
**What is missing:** The full critical-value table (glucose, potassium if available, temperature, pulse, pediatric-specific vitals which differ by age), and the escalation protocol — who is notified, on what device, and what happens if nobody acknowledges.
**Recommendation:** Dr. Nahid authors the critical-value table including pediatric age bands; the system implements acknowledge-or-escalate with a timeout, and every alert and acknowledgement is an audited event. **Confirmation required (clinical) — blocks CP50.**

### D-28 · Physician approval workflow granularity 🟠 `CLINICAL`
**Blueprint says:** physician approves or modifies every AI suggestion (§3 Step 9); QA clearance before printing (§3 Step 10).
**What is missing:** Whether approval is per-item (each drug, each investigation) or per-prescription; whether the QA Officer can block the Chief Consultant; what the override path is when the physician disagrees with a deterministic safety rule.
**Recommendation:** **Per-item accept/edit/reject with a stored decision trail** (this is also the dataset that later teaches the system Dr. Nahid's prescribing patterns, per §8). Safety-rule override permitted **only with a typed reason**, recorded as an event and surfaced in QA. QA may bounce a file; the Chief Consultant may override QA with a recorded reason. **Confirmation required.**

### D-29 · FHIR version and profile scope 🟡 `ARCH`
**Blueprint says:** "Interoperability: HL7 FHIR" (§15.1), Phase 4.
**What is missing:** R4 vs R5; which resources; whether any real external partner exists yet.
**Recommendation:** **R4 (4.0.1)** — overwhelmingly the version with real-world implementation support. Map: Patient, Practitioner, Organization, Device, Encounter, Observation, Condition, AllergyIntolerance, MedicationRequest, DiagnosticReport, DocumentReference, CarePlan, Appointment, Consent, and **Provenance** (which maps DTHCMS's attribution envelope almost exactly). Build the mapping layer only when a named integration partner exists — FHIR without a consumer is expensive shelfware.

---

## 3.E INFRASTRUCTURE

### D-30 · Compute platform 🔴 `ARCH` `COMMERCIAL`
**Blueprint says:** "cloud-native on Google Cloud" (§15.1).
**Options:** **A — Cloud Run** (serverless containers; simplest ops; scale-to-zero; needs care for WebSockets and long-running workers). **B — GKE Autopilot** (full Kubernetes; more control; more ops burden and cost floor). **C — Compute Engine VMs** (simplest mental model, most manual). **D — hybrid: Cloud Run for API + a small GKE/VM footprint for the realtime gateway and GPU workloads.**
**Recommendation:** **A for the API and workers, with the realtime WebSocket gateway on Cloud Run configured for long-lived connections or on a small managed instance group if load testing shows Cloud Run's connection handling is a poor fit.** A single-clinic workload does not justify Kubernetes. Revisit at multi-branch scale (Phase 4).
**Confirmation recommended.** Contingent on D-01 — if hosting must be in-country, this whole entry is re-evaluated (likely VMs + self-managed Postgres, which adds meaningful DevOps effort and should be reflected in the estimate).

### D-31 · Database platform: PostgreSQL vs AlloyDB 🟠 `ARCH` `COMMERCIAL`
**Blueprint says:** "PostgreSQL / AlloyDB."
**Recommendation:** **Cloud SQL for PostgreSQL 16+ for Phases 0–2** — dramatically cheaper, fully sufficient for a single clinic's write volume, and wire-compatible. Migrate to **AlloyDB** only when analytical query load (Phase 3 research dashboards) or multi-branch scale justifies it; AlloyDB's columnar engine is genuinely valuable there. Design decision that makes this painless: **no AlloyDB-specific SQL anywhere**, and analytical queries isolated in a separate read model from day one.

### D-32 · Job queue technology 🟠 `ARCH`
**Blueprint says:** "async job infrastructure ... with queue monitoring" (§15.2).
**Options:** **A — River** (Postgres-backed Go job queue: jobs enqueue **inside the same transaction** as the event write, so a committed clinical event can never lose its follow-on job). **B — Asynq** (Redis-backed; faster, but enqueue is not transactional with the DB write). **C — Cloud Tasks / Pub-Sub** (managed, at-least-once, more moving parts).
**Recommendation:** **A (River).** Transactional enqueue is worth a great deal in an event-sourced clinical system, and it removes a whole class of "the event saved but the AI job never ran" bugs. Redis remains for pub/sub and caching, where it is the right tool.

### D-33 · Object storage & document handling 🟠 `ARCH` `LEGAL`
**Recommendation:** Google Cloud Storage with **customer-managed encryption keys (CMEK)** via Cloud KMS, uniform bucket-level access, object versioning, signed URLs with short TTL for client access (never public objects), lifecycle rules per retention policy (D-05), and separate buckets per data class (D-01). If D-01 forces in-country storage for documents, the abstraction (`blobstore` interface) must be in place from CP34 so an S3-compatible local provider can be substituted.

### D-34 · Secrets management 🔴 `SECURITY`
**Recommendation:** Google Secret Manager with workload identity; **no secrets in environment files, container images, or the repository**; a pre-commit secret scanner and CI secret scanning from CP01. Signing keys and per-patient data keys in Cloud KMS/HSM, never in the application database.

### D-35 · Monitoring, logging, tracing 🟠 `ARCH`
**Recommendation:** OpenTelemetry instrumentation in the Go backend from CP07 (vendor-neutral), exported to Cloud Monitoring/Trace/Logging initially. Structured JSON logs with a mandatory correlation ID and — critically — a **log redaction layer** so PHI never lands in logs (a common and serious failure in clinical systems). Dedicated dashboards: synthesis queue depth and SLA attainment (§7.1), WebSocket connections, offline sync backlog, OCR queue, AI cost.

### D-36 · CI/CD 🟠 `ARCH`
**Recommendation:** GitHub Actions (the blueprint already assumes GitHub, §17.2) → build, test, lint, security scan, migration dry-run → deploy to staging automatically → **manual approval gate to production**. Database migrations expand-and-contract, never destructive in a single release. Container images signed and scanned.

### D-37 · Backup & DR: the 5-minute claim 🔴 `ARCH` `OPS`
**Blueprint says:** "immutable geo-redundant backups every 5 minutes" (§15.1).
**What is missing:** Whether this means **RPO ≤5 minutes** (achievable and standard, via continuous WAL archiving / point-in-time recovery, not literal 5-minute snapshots) or literal snapshot cadence (wasteful and unnecessary). Also missing: RTO target, and whether "geo-redundant" survives D-01's residency constraints.
**Recommendation:** Interpret as **RPO ≤5 min / RTO ≤4 h**, delivered by Cloud SQL PITR + cross-region backup replication + immutable (retention-locked) object storage for documents and event archives, with **restore drills every quarter** — an untested backup is not a backup. **Confirm the RTO target with Dr. Nahid** (how long can the clinic run on paper?).

### D-38 · Data residency 🔴 `LEGAL` — see D-01. Blocks final infrastructure sign-off.

---

## 3.F COMMUNICATION

### D-39 · SMS gateway vendor (Bangladesh) 🟠 `COMMERCIAL` `OPS`
**Blueprint says:** §16.3 — vendor open; needs delivery reliability, **Bangla SMS support**, cost per message.
**What is missing:** Vendor choice, masking/sender-ID registration (BTRC-regulated in Bangladesh), and the practical fact that **Bangla SMS is Unicode-encoded, giving 70 characters per segment versus 160 for GSM-7** — which materially changes cost and message design.
**Candidates:** local aggregators (e.g. REVE SMS, SSL Wireless, MiM SMS, sms.bd and similar), operator-direct arrangements, or an international aggregator with BD routes. **Selection criteria to test in a paid trial:** delivery rate to all four major operators measured over ≥1,000 messages, Unicode Bangla rendering on low-end Android handsets, **delivery receipt (DLR) webhook support**, inbound/two-way support for the clinic line (§11.4), API quality, and per-message price.
**Recommendation:** Run a **two-vendor paid trial (CP114)** and integrate behind a provider-agnostic `Notifier` interface. Do not sign an annual contract before measuring delivery receipts. **Confirmation required (commercial).**

### D-40 · Voice / call infrastructure 🟠 `OPS` `ARCH`
**Blueprint says:** call task lists for staff (§15.2); "unanswered call → automatic SMS fallback" (§11.3).
**What is missing:** Whether calls are **placed manually by staff from a handset** (system only generates and tracks tasks) or **placed programmatically** via a telephony platform/PBX. This is a large architectural difference and the blueprint reads ambiguously.
**Recommendation:** **Phase 2 = staff-dialled with in-app task logging** — the system generates the call task at the preferred window, the staff member dials, and records outcome in two taps; "unanswered" is a human-recorded outcome that triggers automatic SMS. Programmable voice/IVR is a Phase 3+ addition if call volume justifies it. This avoids a telephony integration on the critical path. **Confirmation required.**

### D-41 · Two-way clinic line and inbound routing 🟠 `OPS`
**What is missing:** Whether the clinic number is a mobile number, a short code, or a masked sender ID; inbound SMS to a masked sender ID is often not supported, which would break §11.4's inbound path.
**Recommendation:** Verify inbound capability **with the vendor before selection**; if inbound SMS is impractical, propose WhatsApp Business API or a monitored clinic mobile with an operator app as the inbound channel. **Confirmation required.**

### D-42 · Communication consent, opt-out, and quiet hours 🟠 `LEGAL` `OPS`
**Recommendation:** Every outbound message checks a live consent state (D-02); a Bangla+English opt-out instruction in every campaign-type message; quiet-hours enforcement; per-patient rate limiting; full send/receipt log attached to the record. Transactional clinical follow-up and marketing must be separately consented and separately controllable.

---

## 3.G SECURITY & IDENTITY

### D-43 · Authentication provider 🔴 `SECURITY` `ARCH`
**Blueprint says:** "RBAC + 2FA" (§15.1). Provider unspecified.
**Options:** **A — self-implemented in the Go monolith** (argon2id, TOTP, refresh-token rotation). **B — Google Identity Platform / Firebase Auth. C — Keycloak / Ory / Zitadel self-hosted. D — Auth0/Clerk (commercial SaaS).**
**Recommendation:** **A.** This is contrarian but well-founded here: DTHCMS's identity requirements are unusual — device-bound sessions, in-session role switching [R-02], per-event attribution of the role active at write time [R-03], and staff (not public) users in the low hundreds. Every external IdP would need custom claims and a local mirror anyway. Self-implementing a well-understood staff-auth flow with a mature library is less total complexity than bending an IdP, and keeps credentials inside the residency boundary (D-01). **Non-negotiable conditions:** argon2id with sane parameters, no custom cryptography, dependency on maintained libraries, and a security review at CP94 and CP156.
**Confirmation required (security).**

### D-44 · Session & token strategy 🔴 `SECURITY` `ARCH`
**Recommendation:** Short-lived (10–15 min) signed access token carrying `user_id`, `active_role`, `device_id`, `session_id`; long-lived **opaque, rotating, revocable** refresh token stored server-side and bound to the device; every refresh rotates and detects reuse (a reused refresh token revokes the whole session family). Mobile stores tokens in Keychain/Keystore, never AsyncStorage. Server-side session registry so an administrator can revoke a lost phone instantly — a real clinic-floor scenario.

### D-45 · 2FA method 🔴 `SECURITY` `OPS`
**Blueprint says:** 2FA required; method unspecified.
**Options:** **A — TOTP** (authenticator app; free, offline, phishing-resistant-ish). **B — SMS OTP** (familiar in Bangladesh, but costs money per login, fails when the network fails — and the clinic explicitly must keep working during connectivity loss). **C — WebAuthn/passkeys** (strongest, but device support and staff familiarity are hurdles). **D — device trust**: full 2FA on first enrolment of a device, then a long-lived device trust so floor staff are not fighting a second factor 20 times a day.
**Recommendation:** **A + D.** TOTP at enrolment and for privileged roles (physician, admin, pharmacy, research export), device-trusted sessions for routine floor staff, mandatory step-up 2FA for prescription signing, RBAC changes, research export, and any correction override. **SMS OTP is specifically not recommended as the primary factor** because it breaks the offline requirement. **Confirmation required.**

### D-46 · Device authentication & enrolment 🔴 `SECURITY`
**What is missing:** [R-03] requires a trustworthy `device_id`. A client-generated identifier is trivially spoofable.
**Recommendation:** Admin-enrolled devices: each clinic phone/tablet is registered once by an administrator, receives a **server-issued device credential** (keypair, private key in Android Keystore), and every request is bound to that device. Unknown devices can authenticate a user but cannot write clinical events. Device revocation from the admin console. Optionally Play Integrity attestation later. **Confirmation required (security).**

### D-47 · Encryption at rest & key management 🟠 `SECURITY` `LEGAL`
**Recommendation:** Google-managed encryption at minimum; **CMEK via Cloud KMS** for the database, document buckets, and backups (this is also what makes crypto-shredding in D-05 possible). Application-level envelope encryption for the highest-sensitivity fields (NID number, biometric templates) with per-patient data keys. Decide **whether NID numbers are stored at all** — a salted hash for duplicate detection plus a masked display value may satisfy every stated requirement while removing the single most regulated field from the database. **Recommended; requires confirmation** because it affects Step 1's OCR feature.

### D-48 · Research anonymisation standard 🟠 `LEGAL` `CLINICAL`
**Recommendation:** A one-way pipeline into a separate research schema/project: direct identifiers removed, dates shifted per patient by a fixed random offset, ages >89 banded, free text excluded by default (or passed through a de-identification step and human-reviewed), geography truncated to district, and a documented small-cell suppression rule for dashboards. The re-identification map is stored separately with break-glass access that is itself audited. The **Research ID from Step 1 must not be derivable from the clinical ID** — a detail worth getting right at CP28, because it is very expensive to fix later.

### D-49 · Rate limiting, API security, input validation 🟡 `SECURITY`
**Recommendation:** Per-user and per-device rate limits (Redis token bucket), strict request-size caps, allow-list validation on every input at the transport boundary, parameterised SQL only, output encoding, security headers, CORS locked to known origins, upload scanning (type sniffing + size + optional AV) for the document pipeline, and idempotency keys on every mutating endpoint.

### D-50 · Penetration testing & security assurance 🟡 `SECURITY` `COMMERCIAL`
**Recommendation:** Internal automated scanning (dependency, container, SAST, DAST) from CP13; **an external penetration test before the first real-patient go-live** (end of Phase 1, CP94) and annually thereafter; a documented vulnerability-disclosure and patch SLA. Budget and vendor need confirmation.

---

## 3.H PRODUCT, CONTENT & OPERATIONS

### D-51 · The existing prototype — **RESOLVED: greenfield, no patient data** ✅ `ARCH` `OPS`
**Blueprint says:** Phase 0 is "Existing prototype: registration, anthropometry, basic entry."
**Answer received (22 Aug 2026):** There is no prototype to carry forward and **no existing patient data**. `D:\Project\DTHCMS` is empty.
**Consequences, all favourable:**
- Phase 0 is pure foundation. **CP08 (prototype assessment) collapses to a one-line decision record** and its 3 days are released.
- No migration checkpoint is needed, and no legacy schema constrains the domain model.
- **Every environment is synthetic until the pilot.** Development, CI, staging, load testing and demos run on the CP13 data generator, which means the Gemini free tier (D-07) is entirely appropriate for the whole build period. The paid-tier question only becomes live at CP95, when real patients first use the system.
- The clinic is not running on a system we must keep alive while replacing it — a rare and valuable freedom.
**No further confirmation required.**

### D-52 · Team composition — **ANSWERED: Dr. Nahid + Claude, with a succession caveat** 🟠 `OPS`
**Answer received (22 Aug 2026):** Dr. Nahid and Claude build this together — Claude implements, Dr. Nahid holds clinical authority, authors clinical content, and reviews and approves every checkpoint.
**What this working model requires, stated plainly:**
1. **The repository is our shared memory.** Claude does not carry memory between sessions; each session begins with no recollection of the last. The repo — code, this plan, the ADRs, the decision register, the runbooks — is the only continuity that exists. This is why CP01 and CP02 come first and are not bureaucracy.
2. **Nothing exists until it is committed.** Claude's working environment is an ephemeral cloud sandbox that is discarded. Every checkpoint's output must land in the repository and on Dr. Nahid's disk.
3. **Operations need a human with credentials.** Claude can write and run code, but production deployment, secrets, cloud billing, device enrolment and clinic hardware require Dr. Nahid or a delegate.
4. **Succession is the real risk, and it is manageable.** A system the clinic depends on daily should not be understandable only by its authors. The Definition of Done's documentation requirements and CP159's handover test — where someone who did not build the system performs each procedure from the runbooks alone — exist precisely so a hired engineer can take over later without a rewrite. **Recommendation: plan to bring in a second engineer by Phase 2**, when the OCR pipeline, CRM and pharmacy arrive in parallel.
5. **Pace is set by review, not by typing.** Checkpoints complete as fast as they can be reviewed and approved. The §20 timelines assume a working developer; a build paced by a practising physician's review capacity will be slower, and that is a reasonable trade for correctness.
**Remaining confirmation:** whether a second engineer is planned, and when.

### D-53 · Counseling template content 🔴 `CONTENT` `CLINICAL`
Seven diabetes items are named (§5.1); the actual counseling content, the tick criteria ("what counts as done"), and the templates for thyroid/obesity/PCOS/growth are not authored. Blocks the content half of CP55.

### D-54 · Drug warning library content 🔴 `CONTENT` `CLINICAL`
Three seed entries are given (§9.2); the library must be authored in Bangla and English per drug, by Dr. Nahid. Blocks CP86's content, not its engine.

### D-55 · Red-line rule library content 🟠 `CONTENT` `CLINICAL`
Seed rules named (§6.4); the extensible library — analyte thresholds, sex/age variation, series-based rules — must be physician-authored. Blocks CP108's content.

### D-56 · Formulary content & monthly price review owner 🔴 `CONTENT` `OPS`
§16.1 asks this explicitly. Options: clinic pharmacist (recommended — closest to real prices), a designated admin, or scraped/imported from the DGDA published price listings with manual verification (licence terms for reuse to be checked). **Confirmation required.**

### D-57 · Biometric attendance hardware 🟡 `OPS` `COMMERCIAL` `LEGAL`
§16.6 open. Note that biometric templates are among the most regulated categories under D-01. Recommendation: choose a device with a documented API or standard export (many ZKTeco-class devices expose a network SDK), store **templates, never raw images**, and encrypt them with a dedicated key. Alternative worth considering: face/QR check-in via the existing enrolled phones, avoiding new biometric hardware entirely. **Confirmation required.**

### D-58 · Printer standardisation 🟡 `OPS` `COMMERCIAL`
§16.7 open. Recommendation: standardise on one duplex colour laser model, validate the exact print pipeline against it (CP89), and pin a colour profile — "four-colour laser" output that looks different on a second printer model will undermine §9's brand intent. Field reports (§13) need a portable printer decision separately.

### D-59 · Clinic operating parameters 🟠 `OPS`
Unstated and needed for sizing and for the traffic board: patients per day now and at target; number of concurrent operators; number of devices; consultation rooms; opening hours; peak-hour distribution. **Confirmation required** — these are the inputs to the load tests in CP157 and to the queue model in CP39.

### D-60 · CGM integration 🟡 `ARCH` `COMMERCIAL`
§3 Step 12 mentions "CGM syncs" with no further specification. Dexcom, Abbott LibreView and similar each have distinct (and restrictive) partner API programmes; some have no accessible API in this region. **Recommendation:** treat as Phase 3+, scoped after confirming which CGM devices DTHC patients actually use, with manual CSV/PDF upload as the pragmatic first implementation.

### D-61 · Multi-branch / multi-tenancy 🟡 `ARCH`
§15.3 Phase 4 mentions "multi-branch scaling." Recommendation: include a `facility_id` on every relevant row **from CP06** even while a single facility exists. Adding a tenancy discriminator to a populated clinical database later is one of the most painful migrations there is; adding an unused column now costs nothing.

### D-62 · Patient-facing application 🟡 `ARCH`
The blueprint gives patients QR-linked videos, SMS, and printed reports — but no patient app or portal. Confirm this is deliberate (it is a defensible scope decision) so it does not surface late as an assumed requirement.

---

## 3.J DECISIONS ADDED IN REVIEW

*These were implicit in the architecture sections but had no register entry. They are recorded here so nothing is decided silently.*

### D-63 · Depth of OCR image preprocessing 🟠 `ARCH`
**Blueprint says:** nothing — §16.4 opens OCR generally.
**What is missing:** Whether preprocessing is a light normalisation step or a substantial pipeline (dewarp, shadow removal, adaptive binarisation, super-resolution), and whether it runs on-device at capture or server-side.
**Options:** **A** — light server-side normalisation only. **B** — full server-side pipeline (§11.3 step 3). **C** — on-device pre-checks (quality score, re-scan prompt) plus a full server-side pipeline.
**Recommendation:** **C.** The on-device quality gate is what makes "re-scan while the patient is still here" possible, and it prevents paying for OCR on unusable pages. Depth of the server-side stage is then tuned on CP98's measurements. **Confirmation after CP98.**

### D-64 · Layout detection and table extraction approach 🟠 `ARCH`
**What is missing:** Whether table structure comes from the OCR engine itself (cloud document AI products generally provide it), from a separate layout model, or from an LLM reading the page image.
**Options:** **A** — engine-native tables. **B** — separate layout/table model (e.g. a table-transformer class model). **C** — LLM extraction with a strict schema. **D** — A with C as fallback for irregular layouts.
**Recommendation:** **D**, decided on CP98's measured table accuracy. Lab panels are the highest-value target, so the decision should be made on lab-report accuracy specifically, not on average document accuracy. **Confirmation after CP98 — gates CP103.**

### D-65 · English-language OCR path 🟡 `ARCH`
**What is missing:** The register treats Bangla and mixed-script explicitly; English printed text is assumed easy. It usually is — but DTHC's English documents include faded thermal prints and phone photos, where the limiting factor is image quality, not language.
**Recommendation:** Measure English separately in CP98 (it is the majority of lab values) and allow a different engine for English blocks if per-script routing wins. Do not assume the English case is solved. **Confirmation after CP98.**

### D-66 · Embedding model, chunking and retrieval evaluation 🟠 `ARCH` `CLINICAL`
**Blueprint says:** §7.2's guideline engine, with no retrieval specification.
**What is missing:** Which embedding model (and whether it needs Bangla capability — for a guideline corpus, probably not; for retrieval over Dr. Nahid's own notes, possibly yes), chunk size and overlap, whether to retrieve at section or paragraph granularity, and how retrieval quality is measured.
**Options:** **A** — a hosted embedding API from the same provider as D-07 (simplest, one contract, but sends corpus text offshore — mostly public guideline text, which lowers the D-01 concern). **B** — a self-hosted open embedding model (full control, modest quality cost, no per-token fee at corpus scale). **C** — a multilingual model if Bangla retrieval is needed.
**Recommendation (updated after D-07):** Gemini's embedding models are now the obvious first candidate, and the guideline corpus is **largely public, non-patient text — one of the few places the free tier is genuinely appropriate**, subject to confirming the corpus contains no patient data. For embeddings over Dr. Nahid's own notes or patient records, the paid-tier rule applies. Otherwise, **B for the guideline corpus** — embeddings are computed once over largely public text, self-hosting is cheap at this scale, and it avoids a residency question entirely. Build a small labelled retrieval evaluation set (50–100 real clinical questions with known correct sources) before CP137 ships; retrieval quality, not generation quality, is what makes or breaks that agent. **Confirmation required — gates CP137.**

### D-67 · AI auditability and interaction retention 🟠 `SECURITY` `LEGAL`
**What is missing:** How long AI inputs and outputs are retained, whether the (PHI-minimised) prompt payload is stored in full or only hashed, and who may read the AI interaction log.
**Options:** **A** — store full minimised input and output indefinitely (maximum auditability, more stored data). **B** — store hashes plus the output, discarding inputs after N days. **C** — full storage with a retention window matched to the clinical record.
**Recommendation:** **C**, with access restricted to an audit role and the physician who owns the encounter. Reproducibility of a past clinical suggestion is a medico-legal asset; storing it also makes the evaluation set possible. **Confirmation required (ties to D-05).**

### D-68 · AI safety governance and incident handling 🟠 `CLINICAL`
**What is missing:** §10.6 states the safety invariants, but not who owns them, who may change them, and what happens when an unsafe AI output reaches a physician.
**Recommendation:** The invariants are treated as clinical policy, changeable only by Dr. Nahid and recorded as an ADR. Any grounding violation that reaches a human is a **defect with a mandatory root-cause record** — the same Code Red discipline as CP136, applied to the AI. Rate of such events becomes a tracked metric. **Confirmation required.**

### D-69 · Redis platform 🟡 `ARCH` `LEGAL`
**Blueprint says:** "Redis + WebSockets" (§15.1) — the technology is confirmed; the platform is not.
**Options:** **A** — Google Memorystore (managed, simplest). **B** — self-hosted Redis on a VM (needed if D-01 forces in-country hosting). **C** — Valkey or another fork if licensing matters.
**Recommendation:** **A**, contingent on D-01. Redis holds cache, pub/sub and session state — **no durable clinical data** — so a platform change is low-risk and is deliberately kept that way (which is also why the job queue is in Postgres, per D-32).

### D-70 · Account and administrator recovery 🔴 `SECURITY` `OPS`
**What is missing:** §12.2 specifies administrator-mediated password reset and rejects self-service email links. It does not say what happens when **the administrator** is locked out, or when the 2FA device of the sole physician account is lost mid-clinic.
**Options:** **A** — two administrator accounts held by different people, each able to recover the other (simple, effective, requires organisational discipline). **B** — sealed break-glass credentials in physical safekeeping, with any use alarming loudly. **C** — a cloud-console-level recovery procedure performed by the developer (fast, but concentrates power in one person — the opposite of what D-52 wants).
**Recommendation:** **A + B.** At least two administrators from day one, plus sealed break-glass credentials whose use raises an immediate alert and an audit entry. **Confirmation required before CP16 ships to production** — this is the kind of gap that is invisible until the morning it matters.

### D-71 · Communication retry and escalation policy 🟠 `OPS`
**Blueprint says:** unanswered call → automatic SMS fallback (§11.3).
**What is missing:** What happens after the SMS also fails or goes unanswered: how many attempts, over how many days, at what intervals, and when a human decides the patient is unreachable.
**Recommendation:** A configurable ladder — call → SMS on failure → second call on a different day/time window → alternate number → mark unreachable and surface to the clinical team, with counts and intervals set by the clinic and reviewed after the first month of real data. Every step logged against the patient record. **Confirmation required (operational) — gates CP114's configuration.**

---

## 3.I DECISION SUMMARY — WHAT I NEED BEFORE CP01

Only four answers are strictly required to *start*:

1. **D-51** — Does a prototype exist, and is there real patient data to carry forward?
2. **D-52** — Who is building this, and at what capacity?
3. **D-01** — At minimum, an interim direction on hosting region so CP03 sets up the right project (a definitive legal opinion can follow; the data-class abstraction protects the decision either way).
4. **Approval of this plan's architectural positions** — modular monolith, hybrid event sourcing, offline-first from Phase 1 (§1.4).

Everything else can be resolved in parallel with Phase 0, provided the ones marked 🔴 land before their gating checkpoint.

---

# 4. PROPOSED TECHNOLOGY STACK

Every library below is justified. Where the blueprint already fixed a choice (Go, Postgres, Next.js, React Native, Redis, WebSockets, FHIR, GCP), it is carried forward and marked *[confirmed]*.

## 4.1 Backend

| Concern | Choice | Why this and not something else |
|---|---|---|
| Language | **Go 1.23+** *[confirmed]* | Blueprint-fixed. Also correct: strong concurrency for WebSocket fan-out, single static binary, excellent operational profile. |
| HTTP routing | **`net/http` + `chi`** | Standard library is now capable enough; `chi` adds routing groups and middleware with no framework lock-in. Not Gin/Echo — no need for a framework's opinions when the standard library plus 400 lines of middleware does it. |
| DB access | **`pgx/v5` + `sqlc`** | `sqlc` generates type-safe Go from plain SQL: no ORM magic, no N+1 surprises, and the SQL stays reviewable — which matters when queries touch clinical data. Rejected GORM (reflection-heavy, hides query cost), rejected raw `database/sql` (unsafe scanning at this scale). |
| Migrations | **`golang-migrate`** or **`goose`** | Plain SQL up/down files in the repo, versioned with the code. |
| Job queue | **River** (Postgres-backed) | Transactional enqueue — a clinical event and its follow-on AI job commit or fail together (D-32). |
| Validation | **`go-playground/validator`** + hand-written domain invariants | Transport-layer validation is not domain validation; clinical invariants live in the domain layer. |
| Auth crypto | **`golang.org/x/crypto/argon2`**, **`pquerna/otp`** | Standard, audited, boring. No custom cryptography anywhere. |
| WebSockets | **`coder/websocket`** (formerly nhooyr) | Minimal, context-aware, no global state. |
| Observability | **OpenTelemetry Go SDK** | Vendor-neutral; can export to Cloud Monitoring now and elsewhere later (D-35). |
| Logging | **`log/slog`** (stdlib) + PHI redaction handler | Structured, stdlib, zero dependency risk. The redaction handler is DTHCMS-specific and mandatory. |
| Testing | **stdlib `testing`** + `testify/require` + **`testcontainers-go`** | Real Postgres and Redis in integration tests; mocking a database is how event-sourcing bugs escape to production. |
| API contract | **OpenAPI 3.1**, hand-written spec, generated TS client | Spec-first keeps mobile/web/backend honest and gives the mobile team a stable contract. |
| PDF / print | **Typst** or headless **Chromium (chromedp)** rendering HTML→PDF | Decision at CP89; Chromium gives full CSS control for the graph-heavy A4 layout and reuses web components; Typst gives more deterministic typography. Bench both against the real design. |

## 4.2 Web (Next.js)

| Concern | Choice | Why |
|---|---|---|
| Framework | **Next.js 15 (App Router) + React 19** *[confirmed]* | Blueprint-fixed. Used as an authenticated SPA-with-server-components, not a public marketing site. |
| Language | **TypeScript, `strict: true`** | Non-negotiable on a clinical codebase. |
| Styling | **Tailwind CSS v4 + CSS variables for design tokens** | Tokens as CSS variables let the same token set feed React Native (via a shared JSON source) and the print stylesheet. |
| Components | **Radix UI primitives**, DTHCMS components built on top (shadcn/ui-style, vendored) | Accessibility (focus management, ARIA, keyboard) done correctly by people who specialise in it; visual identity fully ours. Rejected MUI/Ant — both impose a visual language that reads as "hospital admin software," which §12 of the brief explicitly forbids. |
| Server state | **TanStack Query** | Caching, retries, background refetch, optimistic updates, and the WebSocket-invalidation pattern the realtime requirement needs. |
| Client state | **Zustand** (small, local) | Session, active role, UI state. Redux is unnecessary weight here. |
| Forms | **React Hook Form + Zod** | One Zod schema validates on client and generates types; the same schema shape is mirrored server-side. |
| Charts | **visx** or **Recharts** for the dashboard; **D3 directly** for the scrubbable longitudinal timeline | The timeline (§8) is a bespoke visualisation; no chart library will do it well. Sparklines and gradient bars are simple enough not to justify a heavy library. |
| i18n | **`next-intl`** + ICU message format | Bangla pluralisation and number formatting need ICU. Bengali-digit rendering is a per-locale formatting decision (D-11). |
| Tables | **TanStack Table** (headless) | Research/admin grids need virtualisation and column control without a visual opinion. |
| Testing | **Vitest + Testing Library + Playwright** | Component, integration, E2E. |

## 4.3 Mobile (React Native)

| Concern | Choice | Why |
|---|---|---|
| Framework | **React Native (Expo, dev-client)** *[confirmed]* | Expo's build/OTA tooling saves substantial time; the dev-client allows native modules (SQLite, Keystore, printing). |
| Navigation | **Expo Router** | File-based, matches the web mental model, deep-link ready. |
| Local database | **`op-sqlite`** (or `expo-sqlite`) with **SQLCipher** encryption | The offline queue and local cache must be a real relational store with transactions; SQLCipher because a lost clinic phone must not leak PHI (D-01/D-47). |
| Sync layer | **Custom outbox + sync engine** (§11) | Rejected WatermelonDB/PowerSync/Replicache: DTHCMS's model is append-only *events*, not row replication, and correction semantics are domain-specific. A purpose-built ~1,500-line sync engine is more predictable than bending a general framework. This is a considered decision, revisited at CP64. |
| Secure storage | **`expo-secure-store`** (Keychain/Keystore) | Tokens and device private key never in AsyncStorage. |
| State | **TanStack Query (with a SQLite persister) + Zustand** | Same mental model as web; offline-first cache persistence. |
| Styling | **NativeWind** (Tailwind for RN) + shared token JSON | One token source across web and mobile — the coherence §12 of the brief demands. |
| Forms | **React Hook Form + Zod** (shared schemas) | Identical validation rules on both clients and mirrored on the server. |
| Camera/scan | **`expo-camera`**, **`expo-image-manipulator`** | NID capture, document scanning, on-device downscaling before upload (bandwidth matters on clinic Wi-Fi). |
| Testing | **Jest + Testing Library RN**; **Maestro** for device E2E | Maestro handles the offline/airplane-mode flows that matter most here. |

## 4.4 Data & infrastructure

| Concern | Choice | Notes |
|---|---|---|
| OLTP + event store | **PostgreSQL 16** (Cloud SQL) → **AlloyDB** when analytics justify it *[confirmed family]* | D-31. Extensions: `pgcrypto`, `pg_trgm` (fuzzy duplicate detection, fast formulary autocomplete), `btree_gist`, `pgvector` (RAG). |
| Cache / pub-sub | **Redis 7 (Memorystore)** *[confirmed]* | Pub/sub fan-out for WebSockets, session registry, rate limiting, hot-path caching. **Not** the durable job queue (D-32). |
| Object storage | **Google Cloud Storage + CMEK** | D-33; behind a `blobstore` interface for portability. |
| Search | **Postgres `pg_trgm` + full-text**, initially | An external search engine is not justified at this data volume. Revisit only if measured. |
| ML/OCR service | **Python 3.12 + FastAPI** (separate service) | The only genuine language boundary: OCR, image preprocessing, and predictive models live where their libraries live. |
| Container/runtime | **Cloud Run** (API, workers, ML service); realtime gateway per D-30 | |
| IaC | **Terraform** | Reproducible environments; the disaster-recovery story depends on being able to rebuild. |
| CI/CD | **GitHub Actions** | D-36. |

## 4.5 Repository layout

A **single monorepo** (`dthcms/`) — one version of the API contract, one design-token source, atomic cross-stack changes:

```
dthcms/
├── docs/                       blueprint-v2.0.md, ADRs, runbooks, this plan
├── api/                        OpenAPI 3.1 spec (source of truth for contracts)
├── backend/                    Go modular monolith
│   ├── cmd/{api,worker,realtime,migrate}/
│   ├── internal/
│   │   ├── platform/           config, logging, telemetry, db, redis, blobstore, errors
│   │   ├── auth/               identity, sessions, 2FA, devices
│   │   ├── rbac/               policy engine
│   │   ├── eventstore/         append, read, replay, projections
│   │   ├── patient/  visit/  clinical/  counseling/  nutrition/  exercise/
│   │   ├── prescription/  formulary/  medsafety/  qa/
│   │   ├── records/            documents, OCR orchestration, chronology
│   │   ├── ai/                 gateway, prompts, agents, evaluation
│   │   ├── crm/  pharmacy/  research/  hr/  finance/  outreach/  fhir/
│   │   └── realtime/           WS hub, presence, subscriptions
│   └── migrations/
├── ml/                         Python FastAPI: OCR, preprocessing, predictive models
├── web/                        Next.js
├── mobile/                     React Native (station app)
├── field/                      React Native (community tablet app — Phase 4; shares packages/)
└── packages/
    ├── design-tokens/          single JSON source → CSS vars + NativeWind
    ├── api-client/             generated TypeScript client
    ├── shared-schemas/         Zod schemas shared by web + mobile
    └── clinical-calc/          BMI, BMR, eGFR, percentiles — one implementation, TS + Go parity tests
```

**`packages/clinical-calc` deserves a note:** derived clinical values are computed on the client for instant feedback (P-4) *and* on the server for authority. Two implementations of the CKD-EPI equation that disagree is a patient-safety bug. The Go and TypeScript implementations are therefore tested against a **shared fixture file of input/expected pairs** in CI (CP43).

---

# 5. SYSTEM ARCHITECTURE

## 5.1 Architectural stance

The blueprint says "Golang microservices" (§15.1). I recommend deviating, explicitly and on the record.

**The workload:** one clinic, 10–15 concurrent operators, perhaps 100–300 patients/day at target (D-59), a few hundred writes per minute at absolute peak. This is a workload a single well-built Go process handles with enormous headroom.

**What microservices would cost here:** distributed transactions across service boundaries in a system whose central guarantee is an ordered, attributed, immutable event ledger; N deployment pipelines; network failure modes between services that don't exist in-process; and a large permanent tax on a very small team.

**What this plan does instead — a modular monolith with enforced boundaries:**

- One Go binary, multiple run modes (`api`, `worker`, `realtime`, `migrate`).
- Each domain module owns its tables. **No module reads another module's tables directly** — cross-module access goes through an exported Go interface, exactly as it would through an HTTP call later.
- Boundaries enforced mechanically in CI (import-graph linting), not by good intentions.
- Modules are drawn on the seams a future extraction would follow.

**The genuinely separate services (3):**

| Service | Why separate |
|---|---|
| **ML/OCR service (Python)** | Real language boundary. The OCR, preprocessing and predictive-model ecosystem is Python. Also has a completely different scaling profile (bursty, CPU/GPU-heavy) and must never share a process with the request path. |
| **Realtime gateway** | Long-lived WebSocket connections have a different lifecycle and scaling profile from stateless HTTP. Same codebase, separate deployment. |
| **Worker pool** | Async jobs (synthesis, OCR orchestration, SMS, nightly audits) must not compete with request latency. Same codebase, separate deployment, independently scalable. |

## 5.2 When to split further — objective triggers

Extract a module into its own service only when one of these is measurably true:

1. It needs a different scaling axis (its resource use is >5× the rest and bursty) — e.g. the research query engine at Phase 3.
2. Its deployment cadence is genuinely independent and monolith deploys are blocking it.
3. It has different compliance boundaries (e.g. research data must live in a separately governed project under D-48).
4. A different team owns it end to end.
5. Its failure must not take down clinical capture — the strongest argument, and the reason the ML service is separate already.

"The blueprint said microservices" is not on this list. **The clinical capture path must never depend on a network hop to stay up.**

## 5.3 High-level architecture

```
┌────────────────────────────────────────────────────────────────────────────────┐
│                                  CLIENTS                                       │
│                                                                                │
│  ┌──────────────────┐  ┌──────────────────┐  ┌─────────────────────────────┐   │
│  │  STATION APP     │  │  FIELD APP       │  │  WEB (Next.js)              │   │
│  │  React Native    │  │  React Native    │  │  Physician dashboard        │   │
│  │  Android phones  │  │  tablets (P4)    │  │  QA · Admin · Pharmacy      │   │
│  │                  │  │                  │  │  Research · Exec · CRM      │   │
│  │  SQLCipher DB    │  │  SQLCipher DB    │  │                             │   │
│  │  Outbox queue    │  │  Outbox queue    │  │  TanStack Query + WS        │   │
│  │  OFFLINE-FIRST   │  │  OFFLINE-FIRST   │  │                             │   │
│  └────────┬─────────┘  └────────┬─────────┘  └──────────────┬──────────────┘   │
└───────────┼─────────────────────┼───────────────────────────┼──────────────────┘
            │  HTTPS (REST/JSON)  │                           │  HTTPS + WSS
            │  + WSS              │                           │
            ▼                     ▼                           ▼
┌────────────────────────────────────────────────────────────────────────────────┐
│                    EDGE  ·  Cloud Load Balancer + Cloud Armor                   │
│                    TLS termination · WAF · DDoS · rate limiting                 │
└───────────────┬──────────────────────────────────────────┬─────────────────────┘
                │                                          │
                ▼                                          ▼
┌───────────────────────────────────────┐   ┌───────────────────────────────────┐
│  API SERVICE   (Go, Cloud Run)        │   │  REALTIME GATEWAY (Go)            │
│  ┌─────────────────────────────────┐  │   │  WebSocket hub                    │
│  │ Middleware chain                │  │   │  auth handshake · subscriptions   │
│  │ trace → authn → device → RBAC   │  │   │  per-patient / per-station topics │
│  │ → rate limit → validate         │  │   │  RBAC-filtered fan-out            │
│  │ → idempotency → handler         │  │   │  presence (who is on this file)   │
│  └─────────────────────────────────┘  │   └───────────────┬───────────────────┘
│                                       │                   │ subscribe
│  DOMAIN MODULES (in-process)          │                   ▼
│  ┌───────────────────────────────────────────────────────────────────────┐    │
│  │ auth · rbac · patient · visit · clinical · counseling · nutrition     │    │
│  │ exercise · prescription · formulary · medsafety · qa · records        │    │
│  │ ai · crm · pharmacy · research · hr · finance · outreach · fhir       │    │
│  └───────────────────────────────────────────────────────────────────────┘    │
│                    ▲ all clinical writes go through ▼                          │
│  ┌───────────────────────────────────────────────────────────────────────┐    │
│  │        EVENT STORE MODULE — the single clinical write path            │    │
│  │  append(envelope) → validate → assign seq → hash-chain → commit       │    │
│  │  → enqueue projection + job (SAME TRANSACTION) → publish to Redis     │    │
│  └───────────────────────────────────────────────────────────────────────┘    │
└───────────────────────────────┬────────────────────────────────────────────────┘
                                │
        ┌───────────────────────┼───────────────────────┬──────────────────┐
        ▼                       ▼                       ▼                  ▼
┌────────────────┐  ┌──────────────────────┐  ┌──────────────┐  ┌──────────────────┐
│ PostgreSQL     │  │ Redis (Memorystore)  │  │ Cloud Storage│  │ WORKER POOL (Go) │
│ (Cloud SQL →   │  │ · pub/sub → WS       │  │ · documents  │  │ River queue      │
│  AlloyDB)      │  │ · session registry   │  │ · scans/photo│  │ ┌──────────────┐ │
│                │  │ · rate limits        │  │ · prescript. │  │ │ AI synthesis │ │
│ ┌────────────┐ │  │ · hot cache          │  │   PDFs       │  │ │ OCR orchestr.│ │
│ │ event_store│ │  │ · traffic board state│  │ · CMEK       │  │ │ SMS/call task│ │
│ │ (append-   │ │  └──────────────────────┘  │ · versioned  │  │ │ nightly QA   │ │
│ │  only,     │ │                            │ · immutable  │  │ │ projections  │ │
│ │  hash-     │ │                            │   backups    │  │ │ research ETL │ │
│ │  chained)  │ │                            └──────────────┘  │ └──────┬───────┘ │
│ ├────────────┤ │                                              └────────┼─────────┘
│ │ projections│ │                                                       │
│ │ (read      │ │           ┌───────────────────────────────────────────┘
│ │  models)   │ │           │
│ ├────────────┤ │           ▼
│ │ reference  │ │  ┌─────────────────────────────────────────────────────────────┐
│ │ (formulary,│ │  │  AI GATEWAY (in Go — the ONLY path to any model)             │
│ │  templates,│ │  │  PHI minimisation → prompt registry (versioned) → provider  │
│ │  rules)    │ │  │  → schema validation → clinical rule check → confidence     │
│ ├────────────┤ │  │  → store interaction (input, output, model, tokens, cost)   │
│ │ research   │ │  └───────────────┬───────────────────────┬─────────────────────┘
│ │ (separate  │ │                  │                       │
│ │  schema,   │ │                  ▼                       ▼
│ │  anonymis.)│ │      ┌───────────────────────┐  ┌────────────────────────────┐
│ └────────────┘ │      │ LLM PROVIDER (D-07)   │  │ ML/OCR SERVICE (Python)    │
│  pgvector RAG  │      │ synthesis · drafting  │  │ preprocess · OCR · layout  │
└────────────────┘      │ NEVER prescribes      │  │ table extract · classify   │
                        └───────────────────────┘  │ predictive models          │
                                                   └────────────────────────────┘
        ┌──────────────────────────────────────────────────────────────────┐
        │  EXTERNAL:  SMS gateway (D-39) · Google Maps (§14.2) · FHIR      │
        │             partners (P4) · biometric attendance device (D-57)   │
        └──────────────────────────────────────────────────────────────────┘
```

## 5.4 The one rule that holds it together

```
   EVERY clinical write, from every client, on every station,
   goes through ONE function:  eventstore.Append(ctx, envelope)

   There is no second write path. There is no "quick update" endpoint.
   If it isn't an event, it didn't happen.
```

This is what makes §4.2's attribution guarantee, §4.3's correction workflow, §4.5's audit trail, and §12's research integrity all true *by construction* instead of by discipline. Every downstream requirement — the "Entered by" chip, the correction chain, the replayable history, offline sync idempotency — falls out of this one constraint. It is enforced by making the projection tables writable **only** by the projection engine (separate database role), so a well-meaning shortcut cannot compile, let alone deploy.

## 5.5 Request lifecycle (clinical write)

```
1. Mobile: operator enters height 140 cm at the anthropometry station
2. Client generates event_id (UUIDv7) + idempotency_key; writes to LOCAL SQLite outbox
3. UI updates optimistically; clinical-calc computes BMI locally → instant display (P-4)
4. Sync engine POSTs the envelope {value, unit, user_id, device_id, role, station, ts, event_id}
5. API: trace → authn → device verify → RBAC (may this role write ANTHROPOMETRY?) →
        rate limit → schema validate → idempotency check (event_id seen? return prior result)
6. Domain: validate clinical invariants (plausibility bands, unit, patient state)
7. eventstore.Append → assign per-aggregate sequence → hash-chain → INSERT
   ── same transaction ──> projection task + AI/derived jobs enqueued (River)
8. COMMIT
9. Publish to Redis channel patient:{id} → realtime gateway → RBAC-filtered fan-out
10. Physician's dashboard and junior doctor's screen update with no refresh (§4.1)
11. Server response confirms event_id → client marks outbox row synced
```

If step 4 fails (Wi-Fi down), steps 5–11 happen later; steps 1–3 already gave the operator a complete, correct local experience. **That is the whole offline design in one sentence.**

## 5.6 Environments

`local` (docker-compose) → `dev` → `staging` (production-shaped, synthetic data only) → `production`. **No production data ever flows to a lower environment**; staging is seeded by a synthetic data generator built at CP13, which also serves load testing.

---

# 6. DOMAIN MODEL

## 6.1 Bounded contexts

The blueprint's 12 stations are a *workflow*, not a domain decomposition. Modelling one aggregate per station would fragment the patient record. The model below groups by **invariant boundary** — what must be consistent together:

| Context | Owns | Aggregate roots |
|---|---|---|
| **Identity & Access** | Who may do what, from where | User, Role, Device, Session |
| **Patient** | Patient identity and demographics | Patient (+ ResearchSubject) |
| **Encounter** | A visit through the clinic | Visit → Encounter → QueueEntry |
| **Clinical** | Everything measured or observed | Observation, Diagnosis, Allergy |
| **Counseling** | Templates, ticks, gate | CounselingSession |
| **Lifestyle** | Nutrition & exercise assessment and plans | NutritionPlan, ExercisePlan |
| **Records** | External documents and their extraction | MedicalDocument → ChronologyEntry |
| **Medication** | Formulary, safety rules, prescriptions | Prescription, MedicationProduct |
| **QA** | Fail-closed review | QAReview |
| **Communication** | Follow-up, calls, SMS, satisfaction | FollowUp, CallTask, Message |
| **Pharmacy** | Dispensing and stock | DispensingRecord, InventoryItem |
| **Research** | Cohorts and anonymised analysis | Cohort, ResearchDataset |
| **Operations** | HR, attendance, throughput, cost | StaffProfile, AttendanceRecord |
| **Outreach** | Community screening | ScreeningCamp, ScreeningRecord |
| **Audit** | The ledger everything writes to | Event, CorrectionRequest, Consent |

## 6.2 Core entity map

```
                       ┌──────────────┐
                       │   Facility   │  (multi-branch ready from CP06, D-61)
                       └──────┬───────┘
                              │
        ┌─────────────────────┼──────────────────────┐
        ▼                     ▼                      ▼
  ┌───────────┐        ┌────────────┐         ┌────────────┐
  │  Station  │        │   Device   │         │    User    │
  │ 12 types  │        │  enrolled  │◄────────┤  + Roles   │
  └─────┬─────┘        └─────┬──────┘         └──────┬─────┘
        │                    │                       │
        │      ┌─────────────┴───────────────────────┘
        │      │   every clinical write carries
        │      ▼   {user, device, role, station, ts}
        │  ┌────────────────────────────────────────────────────────────┐
        │  │                    EVENT  (append-only)                    │
        │  │  the source of truth for everything clinical               │
        │  └───────────────────────┬────────────────────────────────────┘
        │                          │ projects into
        │                          ▼
  ┌─────┴──────┐          ┌─────────────────────────────────────────┐
  │ QueueEntry │◄─────────┤              PATIENT                    │
  │ station    │          │  clinical_id · research_id (unblinded   │
  │ status     │          │  link held separately) · demographics   │
  │ enter/exit │          │  · NID(hashed) · photo · DOB(validated) │
  └────────────┘          │  · socio-economic · emergency contact   │
                          └───┬─────────────────────────────────────┘
                              │ 1..*
                              ▼
                        ┌───────────┐        ┌──────────────────┐
                        │   VISIT   │───────►│  ENCOUNTER       │
                        │ date      │  1..*  │  (station-level  │
                        │ chief     │        │   interaction)   │
                        │ complaint │        │  operator, in/out│
                        │ status    │        └──────────────────┘
                        └─────┬─────┘
   ┌──────────────────────────┼─────────────────────────────┬───────────────┐
   ▼                          ▼                             ▼               ▼
┌──────────────┐  ┌─────────────────────┐  ┌────────────────────┐  ┌──────────────┐
│ OBSERVATION  │  │ COUNSELING SESSION  │  │  DIAGNOSIS         │  │ PRESCRIPTION │
│ (the single  │  │  template_version   │  │  ICD code          │  │  status:     │
│  polymorphic │  │  ticks[] (attributed│  │  onset · status    │  │  DRAFT →     │
│  clinical    │  │  per item)          │  │  certainty         │  │  QA_REVIEW → │
│  value type) │  │  gate_satisfied     │  └────────────────────┘  │  SIGNED →    │
│              │  └─────────────────────┘                          │  DISPENSED   │
│ subtypes by  │                                                   │  · signature │
│ category:    │  ┌─────────────────────┐  ┌────────────────────┐  │  · QR hash   │
│ ANTHRO       │  │ NUTRITION PLAN      │  │ EXERCISE PLAN      │  └──────┬───────┘
│ VITAL        │  │  24h recall · kcal  │  │  routine · limits  │         │ 1..*
│ EXAM         │  │  local food items   │  │  contraindications │         ▼
│ LAB          │  └─────────────────────┘  └────────────────────┘  ┌──────────────┐
│ DERIVED      │                                                   │ PRESCRIPTION │
│ SCREENING    │  ┌──────────────────────────────────────────┐     │    ITEM      │
│ PRO (score)  │  │  MEDICAL DOCUMENT                        │     │ product·dose │
└──────┬───────┘  │  type · handwritten? · pages[] · blob    │     │ freq·duration│
       │          │        │                                 │     │ instructions │
       │          │        ▼ (printed only)                  │     │ warnings[]   │
       │          │  OCR RESULT → EXTRACTED VALUE ──────────────────┘  · price BDT │
       │          │        │            (confidence, status)  │     └──────────────┘
       │          │        ▼                                  │
       │          │  CHRONOLOGY ENTRY → RED-LINE FLAG         │
       │          └──────────────────┬───────────────────────┘
       │                             │
       └─────────────┬───────────────┘
                     ▼
          ┌────────────────────────┐
          │  LONGITUDINAL TIMELINE │  (read model, §8 of blueprint)
          │  merged: observations, │
          │  diagnoses, meds,      │
          │  documents, events     │
          └────────────────────────┘
```

## 6.3 Entity catalogue

Fields listed are the *identifying* ones, not exhaustive schemas (those are produced per checkpoint).

### Identity & access
| Entity | Key fields | Relationships & notes |
|---|---|---|
| **User** | id, employee_code, name (bn/en), phone, email, password_hash, totp_secret_enc, status, facility_id | 1..* UserRole. Never hard-deleted (attribution integrity) — deactivated. |
| **Role** | id, code, name, description, is_clinical | Permissions via RolePermission. Named roles: REGISTRATION, ANTHROPOMETRY, COUNSELOR, HISTORY, CLINICAL_ASSISTANT, JUNIOR_DOCTOR, RECORDS, NUTRITIONIST, EXERCISE, PHYSICIAN, QA, RX_EDUCATOR, PHARMACIST, CRM, RESEARCHER, HR, ADMIN, FIELD_WORKER |
| **UserRole** | user_id, role_id, granted_by, granted_at, revoked_at | Multiple concurrent roles [R-02] |
| **Permission** | code (e.g. `observation.write.anthro`), description | Resource-action-scope triples |
| **Device** | id, label, platform, enrolled_by, public_key, status, last_seen, facility_id | Server-issued credential (D-46). Revocable. |
| **Session** | id, user_id, device_id, active_role_id, issued_at, expires_at, refresh_family_id, revoked_at | Server-side registry; instant revocation |
| **Station** | id, code, name, room, sequence_hint, facility_id | Configurable order (§5.2) |

### Patient
| Entity | Key fields | Notes |
|---|---|---|
| **Patient** | id, clinical_id (human-readable), dob (validated, exact), dob_verified_by, sex, name_bn, name_en, phone(s), address, nid_hash, nid_masked, photo_blob_id, socioeconomic_*, emergency_contact, status, facility_id | DOB is mandatory and validated — pediatric percentiles depend on it [R-06] |
| **ResearchSubject** | id, research_id (opaque, **not derivable from clinical_id**), patient_link (stored in a separately-governed table), enrolled_at | D-48. Immutable from Step 1. |
| **PatientIdentifier** | patient_id, type (NID/BRN/passport/phone), value_hash, value_masked | Duplicate detection without storing raw identifiers where avoidable (D-47) |
| **PatientMergeRecord** | surviving_id, merged_id, reason, performed_by, event_id | Merges are events, never deletions |
| **Consent** | id, patient_id, type (CARE/COMMS/RESEARCH/AI/OUTREACH), template_version, language, granted, granted_at, evidence_blob_id, witnessed_by, revoked_at | Layered & versioned (D-02) |

### Encounter & workflow
| Entity | Key fields | Notes |
|---|---|---|
| **Visit** | id, patient_id, visit_date, visit_type, chief_complaint, diagnoses[], plan_summary, next_review_interval, status, closed_at, closed_by | §11.1 "which patient came when, with what problem" |
| **Encounter** | id, visit_id, station_id, operator_user_id, device_id, started_at, ended_at | One per station touch — the raw material for throughput analytics (§14.2) |
| **QueueEntry** | id, visit_id, station_id, state (WAITING/IN_PROGRESS/DONE/SKIPPED/BLOCKED), priority, entered_at, called_at, completed_at, reroute_reason | Powers the Clinic Traffic Control board (§5.2) |
| **StationGate** | visit_id, gate_code, satisfied, blocking_reason | Implements fail-closed gates (§5.5, §2 P-7) |

### Clinical
| Entity | Key fields | Notes |
|---|---|---|
| **Observation** | id, patient_id, visit_id, category, code (LOINC/internal), value_num/value_text/value_bool/value_json, unit, effective_at, source (STATION/OCR/FIELD/DEVICE/PATIENT), status (ACTIVE/CORRECTED/SUPERSEDED), event_id, entered_by, device_id, role, station | **The workhorse entity.** One table, category-discriminated, so the timeline, research extracts and FHIR mapping have one shape to consume |
| **DerivedValue** | observation_id, formula_code, formula_version, inputs[], value, unit | BMI, BMR, ideal weight, eGFR, percentile, z-score. Stores *which version of the formula and which reference standard* produced it (D-21) |
| **Diagnosis** | id, patient_id, code_system, code, display, onset, status, certainty, added_by, event_id | ICD-first (D-24) |
| **Allergy** | id, patient_id, substance_code, reaction, severity, asserted_by, no_known_allergies flag | Hard-stop at Step 4 |
| **VitalAlert** | id, observation_id, rule_code, severity, raised_at, acknowledged_by, acknowledged_at, escalated_at | Acknowledge-or-escalate (D-27) |
| **LabAnalyte** (reference) | code, name_variants[], canonical_unit, conversions, reference_ranges (by sex/age) | Authored content (D-20) |

### Counseling & lifestyle
**CounselingTemplate** (diagnosis_code, version, items[], mandatory_flags, published_by) · **CounselingSession** (visit_id, template_version, room_sequence, gate_satisfied) · **CounselingTick** (session_id, item_id, ticked_by, device_id, ticked_at, note) · **LifestyleAssessment** (visit_id, instrument_code+version, raw_responses, composite_score, score_formula_version) · **NutritionPlan** / **ExercisePlan** (visit_id, generated_by (AI/human), approved_by, content, contraindications_applied[])

### Records & digitisation
**MedicalDocument** (patient_id, uploaded_by, source_facility, document_type, is_handwritten, page_count, blob_ids[], classification_confidence, status) · **OCRResult** (document_id, page_no, engine, engine_version, raw_text, layout_json, mean_confidence, cost, latency) · **ExtractedValue** (document_id, analyte_code, value, unit, confidence, bbox, status (AUTO/PENDING_REVIEW/CONFIRMED/REJECTED), reviewed_by) · **ChronologyEntry** (patient_id, occurred_at, facility, document_type, reason, summary, document_id) · **RedLineFlag** (chronology_entry_id | observation_id, rule_code, rule_version, severity, rationale)

### Medication & prescription
**MedicationProduct** (trade_name, generic_id, strength, form, manufacturer, unit_price_bdt, price_updated_at, price_updated_by, dgda_reg_no, active) · **Generic** (name, atc_code, class) · **MedicationRule** (generic_id, rule_type (INTERACTION/CONTRA/RENAL/HEPATIC/PREGNANCY/PEDIATRIC/DUPLICATE), condition_json, action (BLOCK/WARN/INFO), message_bn, message_en, authored_by, approved_at, version) · **DrugWarning** (generic_id, warning_bn, warning_en, print_on_prescription, authored_by, approved_at, version) [R-12] · **Prescription** (visit_id, status, drafted_by, ai_draft_id, signed_by, signed_at, signature_alg, signature_value, canonical_hash, qr_token, pdf_blob_id, print_count) · **PrescriptionItem** (prescription_id, product_id, dose, frequency, duration, route, instructions_bn/en, warnings_applied[], unit_price_bdt, ai_suggested, physician_action (ACCEPTED/EDITED/REJECTED)) · **SafetyCheckResult** (prescription_id, rules_evaluated[], findings[], coverage_gap[], overridden_by, override_reason)

### QA, communication, pharmacy
**QAReview** (visit_id, reviewer, automated_findings[], manual_findings[], outcome (CLEARED/BOUNCED), bounced_to_station, resolved_at) · **PatientReportedScore** (visit_id, score 1–10, captured_by) [R-11] · **FollowUp** (patient_id, due_date, interval_source, status, risk_score, assigned_to) · **ContactPreference** (patient_id, preferred_window, alternate_number, call_consent, sms_consent, updated_at) · **CallTask** (follow_up_id, scheduled_for, attempted_at, outcome, notes, operator) · **Message** (patient_id, direction, channel (SMS/CALL/WHATSAPP), template_version, body, status, provider_message_id, delivery_receipt_at, cost) · **SatisfactionSurvey** (visit_id, score, comments, channel, linked_operators[]) · **InventoryItem** (product_id, facility_id, reorder_level) · **StockBatch** (item_id, batch_no, expiry, qty_on_hand, unit_cost) · **StockMovement** (batch_id, type (RECEIPT/DISPENSE/ADJUST/EXPIRE/RETURN), qty, ref_id, performed_by) · **DispensingRecord** (prescription_id, item_id, batch_id, qty, dispensed_by, dispensed_at, price_charged)

### Research, operations, outreach
**Cohort** (name, definition_json, definition_version, created_by, is_saved_dashboard) · **CohortMembership** (cohort_id, research_subject_id, entered_at, exited_at) · **ResearchDataset** (cohort_id, snapshot_at, anonymisation_profile_version, row_count, blob_id) · **Hypothesis** (generated_by (AI/human), statement, supporting_query, status, reviewed_by) · **StaffProfile** (user_id, designation, cost_per_hour, joined_at) · **AttendanceRecord** (user_id, device/biometric source, check_in, check_out, source_confidence) · **OperatorQualityRecord** (user_id, period, entries_count, corrections_received, correction_rate, retraining_flags) [R-04→§14.2] · **StationThroughput** (station_id, period, patients, median_handling_seconds, p90, bottleneck_score) · **CostEntry** (category, ref_id, amount_bdt, period, derivation) · **ScreeningCamp** (name, location_geo, date, team[], target_population) · **ScreeningRecord** (camp_id, person_ref, measurements[], risk_flags[], referred, follow_up_created) · **FieldTeamLocation** (team_id, geo_point, recorded_at) — Google Maps fleet view (§14.2)

### Audit & AI
**Event** (§7) · **CorrectionRequest** (target_event_id, requested_by, assigned_to, reason, status, resolved_event_id) · **AuditEvent** (non-clinical: logins, permission changes, exports, print, break-glass) · **AIInteraction** (agent_code, prompt_version, model, model_version, input_hash, input_ref, output_ref, tokens_in/out, cost, latency_ms, validation_status, human_action, patient_id, visit_id) · **PromptVersion** (agent_code, version, template, schema, published_by, published_at) · **AIEvaluationRun** (prompt_version, dataset_version, metrics_json, passed, run_at)

## 6.4 Relationships that matter most

1. **Patient 1—* Visit 1—* Encounter** — Encounter is what makes §14.2's throughput and bottleneck analysis possible without any extra instrumentation. Every station touch is already timestamped and attributed.
2. **Event *—1 Observation (projection)** — an Observation row is a *projection* of one or more events. Correcting a height creates a second event; the projection updates; the first event remains and is queryable. This is how §4.3's worked example (140 vs 150) works end to end.
3. **Prescription 1—* PrescriptionItem *—1 MedicationProduct *—1 Generic 1—* MedicationRule** — safety evaluation runs at the Generic level, pricing and display at the Product level. Getting this split right is what allows a curated formulary (D-22 Option A) to give real safety coverage.
4. **Patient 1—1 ResearchSubject** — deliberately a weak link: the mapping lives in a separately governed table so the research schema can be exported without it (D-48).
5. **MedicalDocument 1—* ExtractedValue —> Observation** — extracted values become Observations with `source = OCR` and a confidence, so the timeline is uniform and the physician can always see which numbers came from paper.
6. **Consent gates behaviour** — the communication module checks live consent before every send; the research ETL checks it before inclusion. Consent is not a display field; it is an enforcement point.

## 6.5 Entities the brief's list did not include but the blueprint requires

Facility · Station · QueueEntry · StationGate · CounselingTemplate/Tick · LifestyleAssessment · DerivedValue (with formula version) · VitalAlert · LabAnalyte · OCRResult · ExtractedValue · ChronologyEntry · RedLineFlag · MedicationRule · DrugWarning · SafetyCheckResult · QAReview · PatientReportedScore · ContactPreference · CallTask · SatisfactionSurvey · StockBatch/StockMovement · Hypothesis · OperatorQualityRecord · StationThroughput · CostEntry · ScreeningCamp · FieldTeamLocation · PromptVersion · AIEvaluationRun · CorrectionRequest · PatientMergeRecord · IdempotencyRecord · Snapshot.

---

# 7. EVENT / AUDIT ARCHITECTURE

## 7.1 Scope: what is event-sourced and what is not

| Event-sourced (immutable ledger is the truth) | Conventional CRUD + audit trigger |
|---|---|
| Every clinical observation, vital, anthropometric | Formulary products and prices (versioned rows) |
| Diagnoses, allergies, medication lists | Counseling/warning/rule template content (versioned rows) |
| Counseling ticks | Users, roles, permissions (audited) |
| Prescriptions: create, item change, sign, correct, print | Inventory quantities (movement ledger, which is a different pattern) |
| Consent grant/revoke | Queue state (derived, ephemeral) |
| Corrections and merges | Operational metrics (computed) |
| Document upload and extraction confirmation | Research dataset snapshots (immutable by nature) |
| Dispensing | Session/login records (append-only audit log, not domain events) |

**Rationale:** §4.1 and §15.1 make the event log the source of truth for the *clinical* record — where immutability serves medico-legal defence, correction traceability, and research integrity. Event-sourcing the inventory count or the staff roster would add cost and query complexity with no corresponding benefit. The boundary is drawn at: *does a wrong value here need to be provably traceable to a person forever?*

## 7.2 The event envelope

```jsonc
{
  "event_id":        "018f3a2c-...",   // UUIDv7, CLIENT-generated → idempotency + ordering hint
  "aggregate_type":  "PATIENT",        // PATIENT | VISIT | PRESCRIPTION | DOCUMENT | CONSENT | ...
  "aggregate_id":    "pat_01H...",     // partition key for ordering & replay
  "patient_id":      "pat_01H...",     // denormalised: every clinical event is patient-scoped
  "visit_id":        "vis_01H...",     // nullable (e.g. consent outside a visit)
  "sequence":        1427,             // SERVER-assigned, gapless per aggregate
  "global_seq":      98213771,         // SERVER-assigned, monotonic, for replay/CDC
  "event_type":      "HEIGHT_RECORDED",
  "event_version":   1,                // schema version of THIS event type
  "occurred_at":     "2026-08-22T10:41:58+06:00",  // when it happened (client clock, clinically meaningful)
  "recorded_at":     "2026-08-22T10:42:03+06:00",  // when the server accepted it (authoritative)
  "actor": {
    "user_id":   "usr_...",
    "device_id": "dev_...",
    "role":      "ANTHROPOMETRY",      // the role ACTIVE AT WRITE TIME [R-02/R-03]
    "station":   "ANTHROPOMETRY",
    "facility_id": "fac_dthc_faridpur"
  },
  "payload": { "code":"HEIGHT", "value":150.0, "unit":"cm", "method":"stadiometer" },
  "previous": null,                    // populated on CORRECTED events
  "correction": null,                  // { corrects_event_id, reason_code, reason_text }
  "source":  "MOBILE_ONLINE",          // MOBILE_ONLINE | MOBILE_OFFLINE_SYNC | WEB | OCR | FIELD | SYSTEM
  "metadata": {
    "app_version": "1.4.2",
    "correlation_id": "req_...",
    "offline_queued_at": null,
    "client_tz": "Asia/Dhaka"
  },
  "prev_hash": "sha256:...",           // hash chain over the previous event in this aggregate
  "hash":      "sha256:..."            // sha256(canonical_json(event without hash) ‖ prev_hash)
}
```

**Design notes on specific fields:**

- **`event_id` is client-generated (UUIDv7).** This is what makes offline retry safe: the same event replayed ten times is inserted once (unique constraint), and the API returns the original result. UUIDv7 is time-ordered, which keeps index locality good.
- **`occurred_at` vs `recorded_at` are both kept.** A measurement taken offline at 10:41 and synced at 14:20 is clinically a 10:41 measurement and forensically a 14:20 record. Conflating them loses information that matters in both directions. Client clock skew is measured at sync and stored in metadata.
- **`sequence` is server-assigned per aggregate and gapless.** It gives deterministic replay and optimistic concurrency (§7.9).
- **`actor.role` records the role active at write time,** not the user's current roles. When one operator holds three roles [R-02], "which hat were they wearing" must be answerable years later.
- **`prev_hash`/`hash` form a per-aggregate hash chain,** with a daily global chain anchor. This turns "append-only by policy" into "tamper-evident by mathematics" — a meaningful difference under §4.5's medico-legal purpose.

## 7.3 Event type catalogue (initial)

Naming convention: `NOUN_VERBPAST`, always past tense, always specific.

| Domain | Event types |
|---|---|
| Patient | `PATIENT_REGISTERED` · `PATIENT_DEMOGRAPHICS_CORRECTED` · `PATIENT_PHOTO_CAPTURED` · `PATIENT_IDENTIFIER_ADDED` · `PATIENT_MERGED` · `PATIENT_DECEASED_RECORDED` |
| Visit | `VISIT_OPENED` · `VISIT_STATION_ENTERED` · `VISIT_STATION_COMPLETED` · `VISIT_REROUTED` · `VISIT_GATE_SATISFIED` · `VISIT_GATE_BLOCKED` · `VISIT_CLOSED` |
| Anthropometry | `HEIGHT_RECORDED` · `HEIGHT_CORRECTED` · `WEIGHT_RECORDED` · `WEIGHT_CORRECTED` · `WAIST_RECORDED` · `HIP_RECORDED` · `BODYFAT_RECORDED` · `MUSCLEMASS_RECORDED` · `BMI_DERIVED` · `PERCENTILE_DERIVED` |
| Vitals & exam | `BP_RECORDED` · `BP_CORRECTED` · `PULSE_RECORDED` · `SPO2_RECORDED` · `TEMP_RECORDED` · `RR_RECORDED` · `FOOT_EXAM_RECORDED` · `NEUROPATHY_SCREEN_RECORDED` · `RETINOPATHY_SCREEN_RECORDED` · `CRITICAL_VALUE_ALERTED` · `CRITICAL_VALUE_ACKNOWLEDGED` · `CRITICAL_VALUE_ESCALATED` |
| History | `COMPLAINT_ADDED` · `COMORBIDITY_ADDED` · `ALLERGY_RECORDED` · `NO_KNOWN_ALLERGY_ASSERTED` · `MEDICATION_HISTORY_RECORDED` · `FAMILY_HISTORY_RECORDED` · `VACCINATION_RECORDED` |
| Diagnosis | `DIAGNOSIS_ADDED` · `DIAGNOSIS_AMENDED` · `DIAGNOSIS_RESOLVED` · `DIAGNOSIS_REMOVED_IN_ERROR` |
| Counseling | `COUNSELING_SESSION_STARTED` · `COUNSELING_ITEM_TICKED` · `COUNSELING_ITEM_UNTICKED` · `COUNSELING_SESSION_COMPLETED` |
| Lifestyle | `LIFESTYLE_ASSESSMENT_RECORDED` · `LIFESTYLE_SCORE_DERIVED` · `NUTRITION_ASSESSMENT_RECORDED` · `NUTRITION_PLAN_ISSUED` · `EXERCISE_ASSESSMENT_RECORDED` · `EXERCISE_PLAN_ISSUED` |
| Records | `DOCUMENT_UPLOADED` · `DOCUMENT_CLASSIFIED` · `DOCUMENT_MARKED_HANDWRITTEN` · `OCR_COMPLETED` · `OCR_FAILED` · `EXTRACTION_PROPOSED` · `EXTRACTION_CONFIRMED` · `EXTRACTION_REJECTED` · `CHRONOLOGY_REBUILT` · `REDLINE_RAISED` |
| Lab | `LAB_RESULT_RECORDED` · `LAB_RESULT_CORRECTED` · `LAB_ORDERED` |
| Prescription | `PRESCRIPTION_CREATED` · `PRESCRIPTION_ITEM_ADDED` · `PRESCRIPTION_ITEM_MODIFIED` · `PRESCRIPTION_ITEM_REMOVED` · `SAFETY_CHECK_RUN` · `SAFETY_WARNING_OVERRIDDEN` · `PRESCRIPTION_SUBMITTED_FOR_QA` · `PRESCRIPTION_QA_BOUNCED` · `PRESCRIPTION_QA_CLEARED` · `PRESCRIPTION_SIGNED` · `PRESCRIPTION_PRINTED` · `PRESCRIPTION_CORRECTED` · `PRESCRIPTION_CANCELLED` |
| AI | `AI_SYNTHESIS_REQUESTED` · `AI_SYNTHESIS_COMPLETED` · `AI_SYNTHESIS_FAILED` · `AI_SUGGESTION_ACCEPTED` · `AI_SUGGESTION_EDITED` · `AI_SUGGESTION_REJECTED` |
| Consent | `CONSENT_GRANTED` · `CONSENT_REVOKED` · `CONSENT_TEMPLATE_PUBLISHED` |
| Correction | `CORRECTION_REQUESTED` · `CORRECTION_ASSIGNED` · `CORRECTION_APPLIED` · `CORRECTION_REJECTED` · `SUPERVISOR_OVERRIDE_APPLIED` |
| CRM | `FOLLOWUP_SCHEDULED` · `CALL_TASK_CREATED` · `CALL_ATTEMPTED` · `CALL_CONNECTED` · `SMS_QUEUED` · `SMS_SENT` · `SMS_DELIVERED` · `SMS_FAILED` · `INBOUND_MESSAGE_RECEIVED` · `SATISFACTION_RECORDED` |
| Pharmacy | `MEDICATION_DISPENSED` · `DISPENSE_REVERSED` · `STOCK_RECEIVED` · `STOCK_ADJUSTED` |
| Field | `SCREENING_RECORDED` · `SCREENING_REFERRED` · `FIELD_REPORT_ISSUED` |

## 7.4 Storage schema (outline)

```sql
CREATE TABLE events (
  global_seq      BIGSERIAL PRIMARY KEY,
  event_id        UUID        NOT NULL UNIQUE,        -- idempotency
  aggregate_type  TEXT        NOT NULL,
  aggregate_id    UUID        NOT NULL,
  sequence        BIGINT      NOT NULL,               -- gapless per aggregate
  patient_id      UUID        NULL,
  visit_id        UUID        NULL,
  event_type      TEXT        NOT NULL,
  event_version   SMALLINT    NOT NULL DEFAULT 1,
  occurred_at     TIMESTAMPTZ NOT NULL,
  recorded_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  actor_user_id   UUID        NOT NULL,
  actor_device_id UUID        NOT NULL,
  actor_role      TEXT        NOT NULL,
  actor_station   TEXT        NULL,
  facility_id     UUID        NOT NULL,
  source          TEXT        NOT NULL,
  payload         JSONB       NOT NULL,
  previous        JSONB       NULL,
  correction      JSONB       NULL,
  metadata        JSONB       NOT NULL DEFAULT '{}',
  prev_hash       BYTEA       NULL,
  hash            BYTEA       NOT NULL,
  UNIQUE (aggregate_type, aggregate_id, sequence)
) PARTITION BY RANGE (recorded_at);            -- monthly partitions

-- Enforced append-only at the database level, not just in application code:
REVOKE UPDATE, DELETE ON events FROM app_write_role;
CREATE RULE events_no_update AS ON UPDATE TO events DO INSTEAD NOTHING;
CREATE RULE events_no_delete AS ON DELETE TO events DO INSTEAD NOTHING;

CREATE INDEX ON events (patient_id, occurred_at DESC);
CREATE INDEX ON events (visit_id) WHERE visit_id IS NOT NULL;
CREATE INDEX ON events (event_type, recorded_at DESC);
CREATE INDEX ON events (actor_user_id, recorded_at DESC);   -- "what did this operator enter today"
CREATE INDEX ON events USING GIN (payload jsonb_path_ops);
```

## 7.5 Idempotency

Three layers, because retries come from three places:

1. **`event_id` UNIQUE** — the fundamental guarantee. Replaying the same event is a no-op returning the original outcome.
2. **HTTP `Idempotency-Key` header** on every mutating endpoint, with a short-lived `idempotency_records` table storing the response — so a client that retries after a timeout gets the same response body, not a duplicate-key error it has to interpret.
3. **Job-level idempotency** — River jobs are keyed so a re-enqueued synthesis job for the same `(visit_id, input_hash)` returns the cached result instead of re-billing an LLM call.

## 7.6 Ordering

- **Within an aggregate:** total order by server-assigned `sequence`. This is the only ordering the domain relies on.
- **Globally:** `global_seq` gives a replay/CDC order but carries no cross-aggregate causal meaning, and the code must never assume it does.
- **Offline events:** carry `occurred_at` from the client. On sync, they are appended with the *current* sequence (they are new facts to the server) but sorted by `occurred_at` in every clinical view. A late-arriving 10:41 measurement appears in its correct clinical position with a subtle "synced late" indicator — the physician sees clinical truth; the auditor sees arrival truth.
- **Clock skew** is measured at every sync and stored; skew beyond a threshold raises an operational alert (a station phone with a wrong clock is a real and quietly damaging problem).

## 7.7 Correction semantics — the canonical 140/150 case

The blueprint's worked example is the acceptance test for this whole architecture:

```
10:42  HEIGHT_RECORDED       seq 41   value 150 cm   by ANTH_02 on dev_07
       → projection: observation.height = 150, entered_by = ANTH_02
       → derived: BMI recomputed, percentile recomputed

11:15  Physician sees 150, taps the value.
       UI immediately shows: "ANTH_02 · Anthropometry · dev_07 · 10:42"   [R-03]

11:15  CORRECTION_REQUESTED  seq 42   target seq 41  reason "implausible vs prior 140"
       → routed to ANTH_02's phone; supervisor override available

11:20  HEIGHT_CORRECTED      seq 43   value 140 cm
         previous  { value: 150, unit: cm }
         correction{ corrects_event_id: <seq41 id>, reason_code: TRANSCRIPTION,
                     reason_text: "misread stadiometer", corrected_by: ANTH_02 }
       → projection: observation.height = 140, status of prior = CORRECTED
       → derived values recomputed and re-versioned
       → OperatorQualityRecord[ANTH_02].corrections_received += 1     (§14.2)
       → HR dashboard pattern detection: 3rd transcription error in 30 days → retraining flag

FOREVER AFTER:
  • seq 41 still exists, still returns 150, still attributed to ANTH_02
  • the value history shows: 150 (10:42, ANTH_02) → 140 (11:20, ANTH_02, reason: transcription)
  • any research extract taken before 11:20 can be reproduced exactly by replaying to seq 42
```

**Rules:** original events are never mutated or deleted; a correction always states a reason (structured code + free text); the correcting user is recorded separately from the original author; supervisor override is a distinct event type; and corrections cascade to derived values, which are re-derived and versioned rather than overwritten.

## 7.8 Projections & replay

- **Projection engine** consumes events in `global_seq` order and writes read models (observations, timeline, dashboard snapshot, queue board). Projections are **idempotent and rebuildable**: `projection_checkpoint` stores the last applied `global_seq`, and a full rebuild is a documented, tested operation (CP23), not a heroic act.
- **Synchronous vs asynchronous:** clinically critical projections (the value just entered, the queue state) are updated **inside the same transaction** as the append, so the operator's next read is correct. Expensive projections (timeline aggregation, research mart, analytics) are asynchronous with a visible lag metric.
- **Replay tests** (CP25, and in CI forever after) assert that rebuilding all projections from the event log reproduces the current state byte-for-byte on a fixture dataset. This is the test that keeps event sourcing honest; without it, projections silently drift and the "source of truth" claim quietly becomes false.

## 7.9 Snapshots & concurrency

- **Snapshots** are not needed for Phase 1 (a patient with a decade of history is on the order of 10³–10⁴ events; replay is milliseconds). The `aggregate_snapshots` table is created in CP23 but stays unused until a measured need appears — building the mechanism is cheap, using it prematurely is complexity.
- **Concurrency control:** expected-sequence optimistic concurrency for commands that depend on current state (prescription signing, visit closing, merges). For independent facts — two operators recording different measurements — there is **no conflict by construction**, because both are simply new events. This is the deep reason event sourcing suits a 15-operator parallel workflow: most "concurrency problems" simply do not exist.
- **Two devices, same patient, same field:** both events are recorded; the projection applies last-write-by-`occurred_at`; **both values remain visible in the value history with their attribution**, and the UI flags a same-field double entry within a short window for review. Nothing is silently discarded — silent discard is exactly what §4.3 exists to prevent.

## 7.10 Versioning, migration, retention

- **Event schema versioning:** events are immutable, so a schema change means a **new `event_version`** with an **upcaster** function that maps old versions to the current shape at read time. Upcasters are never deleted, and each has a test with a real archived payload. Historical events are never rewritten.
- **Retention:** clinical events are retained indefinitely, subject to D-05. Partitions older than 24 months move to cheaper storage but stay queryable. Non-clinical audit events (logins, page views) have a shorter retention.
- **Archive integrity:** a nightly job verifies the hash chain over the previous day's partition and records the result; a monthly job re-verifies a random sample of history. A chain break is a P1 security incident, and the runbook says so.

## 7.11 The audit trail humans actually read (§4.5)

The ledger is the machine truth. Requirement §4.5 also asks for a human-readable line — "10:42 — JD_04 changed systolic BP 140 → 145". This is a **rendering layer** over events (CP22), not a second log: each event type has a bilingual template that produces the sentence. One truth, two presentations. Available on every value (hover/tap), every visit (full trail), every operator (their day), and exportable as a signed PDF for medico-legal use.

---

# 8. BACKEND ARCHITECTURE (DETAIL)

## 8.1 Layering inside a module

Every domain module has the same four-layer shape. Uniformity here is worth more than local cleverness — a new contributor learns one structure and can then read all twenty modules.

```
internal/<module>/
├── domain.go        entities, value objects, invariants — NO database, NO HTTP
├── service.go       use cases; orchestrates domain + repo + events; owns transactions
├── repo.go          persistence; sqlc-generated queries; the ONLY place SQL lives
├── http.go          handlers: decode → validate → call service → encode. No logic.
├── events.go        event types this module emits + their payload schemas + upcasters
├── projections.go   how this module's read models are built from events
└── *_test.go        domain tests (pure), service tests (testcontainers), http tests
```

**Dependency rule:** `http → service → domain`, `service → repo`, and nothing points back inwards. A module may import another module's `port.go` interface, never its `repo` or `domain`. Enforced in CI by an import-graph check (CP02) — a violation fails the build.

## 8.2 Module boundaries and their exported ports

| Module | Exports (used by others) | Consumes |
|---|---|---|
| `platform` | config, logger, tracer, db pool, redis, blobstore, clock, ids | — |
| `auth` | `Authenticator`, `SessionStore`, `DeviceVerifier` | platform |
| `rbac` | `PermissionChecker.Can(ctx, user, action, resource)` | auth |
| `eventstore` | `Appender.Append(ctx, envelope) (Event, error)`, `Reader`, `Replayer` | platform |
| `patient` | `PatientLookup`, `DuplicateDetector` | eventstore, rbac |
| `visit` | `VisitState`, `QueueManager`, `GateEvaluator` | patient, eventstore |
| `clinical` | `ObservationWriter`, `DerivedCalculator`, `AlertRaiser` | visit, eventstore |
| `formulary` | `ProductSearch`, `PriceLookup` | platform |
| `medsafety` | `SafetyEvaluator.Evaluate(ctx, draft) (Findings, Coverage)` | formulary, clinical |
| `prescription` | `PrescriptionService`, `Signer` | medsafety, qa, eventstore |
| `ai` | `Gateway.Invoke(ctx, agent, input) (Output, error)` | platform only (deliberately isolated) |
| `records` | `DocumentIngest`, `ChronologyBuilder` | ai, ml-client, eventstore |
| `crm` | `Notifier`, `FollowUpScheduler` | patient, consent |
| `realtime` | `Publisher.Publish(topic, msg)` | rbac |

## 8.3 The middleware chain (order is deliberate)

```
recover → requestID/trace → access log → CORS → body limit
  → authenticate (JWT verify, session live?) 
  → device verify (signature; is device enrolled and active?)
  → active-role resolve (from token; is the role still granted?)
  → RBAC authorize (action+resource; deny by default)
  → rate limit (per user + per device + per endpoint class)
  → idempotency (mutating verbs)
  → validate (schema)
  → handler
```

Rejecting an unauthenticated request before it can consume a rate-limit slot, and authorising before validation, are both intentional: the cheapest checks fail first, and error responses never leak whether a resource exists to someone not allowed to see it.

## 8.4 Transactions and the write path

- **One transaction per command.** The service layer opens it; repositories join it via context. Handlers never see a transaction.
- **Event append, projection update (synchronous ones), and job enqueue commit together.** This is why River (Postgres-backed) was chosen: no dual-write between the database and a broker (D-32).
- **Redis publication happens after commit**, in an `AFTER COMMIT` hook. If the publish fails, the data is still correct and the client reconciles on its next poll or reconnect — realtime is an optimisation, never a correctness dependency.

## 8.5 Background jobs

| Job class | Examples | Priority | Retry |
|---|---|---|---|
| Clinical-critical | AI synthesis (§7.1), critical alert escalation | Highest | Exponential, 5 attempts, then page |
| Patient-facing | SMS send, prescription PDF render | High | Exponential, 8 attempts |
| Pipeline | OCR orchestration, extraction, chronology rebuild | Medium | 3 attempts then human queue |
| Analytical | Research ETL, nightly QA audit, throughput rollup | Low | Retried next cycle |
| Maintenance | Hash-chain verify, partition rotation, backup verify | Low | Alert on failure |

Every job is idempotent, records start/end/duration, and reports to a queue-health dashboard with depth, oldest-unstarted-age, and failure rate. **The §7.1 five-minute SLA is measured as a real metric with an alert, not asserted.**

## 8.6 Error model

One error type with a stable machine code, an HTTP status, a bilingual user-facing message, and an internal detail that is logged but never returned. Clinical errors are distinguished from technical ones because the UI must treat them differently: `SAFETY_RULE_BLOCKED` gets a modal with an override path; `DB_TIMEOUT` gets a retry. Codes are enumerated in the OpenAPI spec so clients can branch on them without string matching.

## 8.7 Configuration & secrets

Config from environment with a typed, validated struct; the process **refuses to start** if a required value is missing or malformed (fail fast at deploy, never at 11:40 on a clinic morning). Secrets from Secret Manager at boot, never in config files. A `/healthz` (liveness), `/readyz` (dependencies) and `/version` (git SHA, build time) endpoint on every service.

## 8.8 Testing at the backend layer

Domain logic tests are pure and fast. Service tests run against **real Postgres and Redis via testcontainers** — mocking the database in an event-sourced system hides exactly the bugs that hurt. HTTP tests exercise the full middleware chain including RBAC denial paths. A **golden-file test per event type** pins its serialised shape, so an accidental schema change fails the build rather than corrupting the ledger.

---

# 9. DATABASE & DATA ARCHITECTURE

## 9.1 Schema separation

| Schema | Contents | Access |
|---|---|---|
| `core` | Reference & identity: users, roles, devices, facilities, stations, templates, formulary, rules | App read/write |
| `ledger` | `events`, `idempotency_records`, `aggregate_snapshots`, `audit_events` | App **INSERT only**; no UPDATE/DELETE grant |
| `read` | All projections: observations, timeline, dashboards, queue, quality records | **Projection role writes; app role reads only** |
| `ops` | Jobs (River), metrics rollups, throughput, cost | App read/write |
| `research` | Anonymised marts, cohorts, snapshots | Separate role; **no join path to `core.patients`** |
| `docs` | Document metadata (bytes in GCS) | App read/write |

The `read`-schema write restriction is what physically prevents the "quick update" that would break §5.4's single-write-path rule. It is a database grant, not a code review convention.

## 9.2 Conventions

- **Primary keys:** UUIDv7 everywhere (`id`), generated client- or server-side. Time-ordered, so index locality stays good; opaque, so they leak nothing. Human-facing identifiers (`clinical_id` like `DTHC-2026-04821`) are a separate, indexed, unique column.
- **Foreign keys:** enforced everywhere in `core` and `read`. The `ledger` deliberately has **no FKs to projections** — the ledger must remain valid even if a projection is dropped and rebuilt.
- **Timestamps:** `TIMESTAMPTZ` only, always UTC in storage, rendered in `Asia/Dhaka`. Every table carries `created_at`, `updated_at`; mutable reference tables also carry `created_by`, `updated_by`.
- **Soft deletion:** not used for clinical data (which is corrected, never deleted). Reference data uses `active BOOLEAN` + `deactivated_at`. `patients` are never deleted, only `status = MERGED | INACTIVE | DECEASED`.
- **Money:** `NUMERIC(12,2)` with an explicit `currency` column (always `BDT` today, but multi-currency-safe). Never floats.
- **Clinical numerics:** `NUMERIC`, never `FLOAT`. A rounding error in a dose is not an acceptable class of bug.
- **Units:** every measured value column pairs with a `unit` column and is stored in a canonical SI unit; conversion happens at the presentation edge (P-6).
- **Enums:** application-level constants in a lookup table with a check constraint, not Postgres `ENUM` types (which are painful to alter).
- **`facility_id`** on every relevant table from day one (D-61).
- **JSONB** for genuinely variable payloads (event payloads, rule conditions, layout JSON) — with a validating JSON Schema in code and a GIN index where queried. **Not** used to avoid designing a schema.

## 9.3 Indexing strategy (initial)

| Need | Index |
|---|---|
| Patient search by name (bn/en) & phone | `pg_trgm` GIN on normalised name; btree on phone |
| Duplicate detection | trigram similarity on name + exact on `nid_hash`, `dob`, phone |
| Formulary 2-letter autocomplete (§10.1 — hard speed requirement) | `pg_trgm` GIN on trade name + generic; plus a small in-memory cache in the API, since the formulary is a few hundred rows and changes rarely |
| Patient timeline | `(patient_id, occurred_at DESC)` on observations and events |
| Queue board | partial index on `state IN ('WAITING','IN_PROGRESS')` |
| Operator activity / quality | `(actor_user_id, recorded_at DESC)` on events |
| Follow-ups due | partial index on `status='PENDING' AND due_date <= ...` |
| RAG retrieval | `pgvector` HNSW on the knowledge chunk table |

Every index is justified by a named query. Indexes are added with the checkpoint whose query needs them, and `EXPLAIN` output for the critical paths is part of that checkpoint's deliverables.

## 9.4 Partitioning & growth

`events` partitioned monthly by `recorded_at`; `audit_events` and `messages` likewise. At a realistic 200 patients/day × ~150 events/patient ≈ 30k events/day ≈ 11M/year — comfortably small for Postgres, but partitioning from the start makes retention, archival and hash-chain verification per-partition operations rather than table-wide agony.

## 9.5 Migration strategy

- Plain SQL, forward-only in production, one concern per migration, reviewed like code.
- **Expand → migrate → contract**: add nullable column → backfill in batches → start writing both → switch reads → drop old. No release both writes a new schema and drops the old one.
- Every migration tested against a restored production-shaped dataset in staging before production; long-running backfills run as jobs, not as migrations.
- Migrations run as a separate `cmd/migrate` job with an approval gate — never automatically on application boot.

## 9.6 Encryption & data classification

Every table is tagged with a data class (D-01): `IDENTIFIER` (names, NID, phone, address, photos, biometrics) · `CLINICAL` (observations, diagnoses, prescriptions) · `DOCUMENT` (scans) · `DERIVED` · `ANALYTIC` (anonymised). Class drives storage location, encryption, retention and export rules. Database and buckets use CMEK; the highest-sensitivity fields additionally use application-level envelope encryption with per-patient keys — which is also what makes crypto-shredding possible (D-05).

## 9.7 Backup & recovery

Cloud SQL automated backups + PITR (WAL) targeting **RPO ≤ 5 minutes** (D-37) · cross-region backup replication · GCS object versioning with a retention lock for documents and event archives · **quarterly restore drills into an isolated project, timed and documented** · a documented RTO with a manual clinic fallback procedure (printed patient list for the day) so the clinic can operate during an outage. Backups are encrypted with a separate key from production, and restore-drill results are a Definition-of-Done item for the DR checkpoints.

## 9.8 Analytical / research data path

```
ledger.events ──► read.* projections ──► nightly ETL ──► research.* (anonymised)
                                            │
                                            ├─ direct identifiers dropped
                                            ├─ dates shifted per subject
                                            ├─ ages >89 banded
                                            ├─ geography truncated to district
                                            ├─ consent filter applied (D-02)
                                            └─ small-cell suppression on publish
```
Research dashboards query **only** `research.*`. No dashboard, notebook, or export tool is granted access to `core` or `read`. This is a grant-level guarantee, not a policy document (D-48).

---

# 10. AI ARCHITECTURE

## 10.1 The seven distinct technologies the blueprint calls "AI"

Treating all ten agents in §7.2 as "LLM chatbots" would be the single most dangerous architectural mistake available in this project. They are seven different technologies with different failure modes, different validation needs, and different levels of permitted authority:

| # | Technology | Used for | May it act alone? |
|---|---|---|---|
| 1 | **Generative LLM** | Summarisation, narrative drafting, note structuring, question suggestion | **Never.** Draft only, always labelled, always physician-reviewed before clinical effect |
| 2 | **Deterministic rule engine** | Medication safety, contraindications, renal dosing, QA checklist, critical values, red-line flags, fail-closed gates | **Yes — it is authoritative.** Hand-written rules, unit-tested, physician-approved, versioned |
| 3 | **Classical computation** | BMI, BMR, ideal weight, eGFR (CKD-EPI 2021), growth percentiles/z-scores, pack-years, composite scores | **Yes.** Pure functions with reference test vectors |
| 4 | **Traditional ML** | No-show risk, deterioration risk, procurement forecasting, bottleneck prediction | **Advisory only.** Ranks and flags; never blocks or prescribes |
| 5 | **Retrieval (RAG)** | Guideline citation, prior-prescribing-pattern retrieval, knowledge digest | **Retrieval is authoritative; interpretation is not.** Every claim carries its citation |
| 6 | **OCR / document AI** | Records digitisation (§11) | **No.** Confidence-gated; clinical-rule-driving values need human confirmation |
| 7 | **Speech-to-text** | Scribe (Phase 3, dictation-first — D-12) | **No.** Transcript is a draft |

## 10.2 The mandatory pipeline

```
        ┌──────────────────────────────────────────────────────────────┐
        │              STRUCTURED CLINICAL DATA (events)                │
        └───────────────────────────┬──────────────────────────────────┘
                                    ▼
                       ┌───────────────────────────┐
                       │   PHI MINIMISATION (D-08) │  identifiers stripped,
                       │   pseudonym + age_months  │  pseudonym substituted
                       └────────────┬──────────────┘
                                    ▼
                       ┌───────────────────────────┐
                       │   GENERATIVE AI           │  versioned prompt,
                       │   (draft / summary only)  │  pinned model version
                       └────────────┬──────────────┘
                                    ▼
                       ┌───────────────────────────┐
                       │   SCHEMA VALIDATION       │  strict JSON schema;
                       │   invalid → retry → fail  │  no free-form parsing
                       └────────────┬──────────────┘
                                    ▼
                       ┌───────────────────────────┐
                       │   GROUNDING CHECK         │  every numeric claim must
                       │   (anti-hallucination)    │  match a stored observation
                       └────────────┬──────────────┘
                                    ▼
                       ┌───────────────────────────┐
                       │   DETERMINISTIC CLINICAL  │  medication safety, dosing,
                       │   RULES  (authoritative)  │  contraindications, gates
                       └────────────┬──────────────┘
                                    ▼
                       ┌───────────────────────────┐
                       │   PHYSICIAN REVIEW        │  per-item accept / edit /
                       │   (mandatory)             │  reject, each recorded
                       └────────────┬──────────────┘
                                    ▼
                       ┌───────────────────────────┐
                       │   FINAL CLINICAL ACTION   │  signed prescription,
                       │   + digital signature     │  recorded diagnosis
                       └───────────────────────────┘
```

**The grounding check (step 4) deserves emphasis.** Before any AI summary reaches the physician, every number in it is matched against the structured record. "HbA1c 8.2 on 12 March" must correspond to an actual stored observation with that value and date, or the sentence is rejected and the summary is regenerated or flagged. This converts the most common and most dangerous LLM failure — a plausible invented number — from a clinical risk into a caught error. It is cheap to build because DTHCMS already has every value in structured form; it is impossible to build in systems whose records are free text.

## 10.3 The AI Gateway (CP70) — the only path to any model

```
ai.Gateway.Invoke(ctx, agentCode, input) →
   1  resolve agent config (prompt version, model, model version, schema, temperature, budget)
   2  authorise (does this caller have permission to invoke this agent for this patient?)
   2b TIER GUARD — if the payload derives from a real patient, refuse to use a free-tier
      credential; free tier is reachable only when the request is flagged synthetic (D-07)
   3  minimise PHI (D-08) and record what was stripped
   4  check cache (input hash — identical input never re-billed)
   5  call provider with timeout + circuit breaker + retry-with-jitter
   6  validate output against strict schema
   7  grounding check against structured data
   8  record AIInteraction: prompt version, model version, tokens, cost, latency, validation result
   9  return typed result + confidence + a mandatory `ai_generated: true` marker
```

Everything else in the system calls this and nothing else. Consequences: swapping providers is a config change (D-07); cost is metered per agent, per patient, per day (D-14); every AI output in the system's history is reproducible and auditable; and PHI minimisation cannot be forgotten by an individual feature.

## 10.4 Agent specifications

Each agent below is specified in the requested form. **"Model requirement"** is deliberately expressed as a capability class, not a product name, because D-07 is open.

---
**A1 · Pre-Consultation Synthesis Agent** (§7.1) [R-05] — *Phase 1, CP71*
- **Input:** all structured station data for the visit + prior visits summary + records chronology (Phase 2 onward) + prior prescriptions; PHI-minimised.
- **Processing:** deterministic assembly of a structured clinical context → LLM produces a one-page narrative + a structured draft plan (suggested diagnoses with ICD codes, missing investigations, drafted medications as *candidates only*) → grounding check → deterministic rules annotate the draft.
- **Model requirement:** strong long-context clinical reasoning; the highest-quality tier in the stack. This is the agent whose quality determines whether the physician trusts the system at all.
- **Output:** `{narrative_en, narrative_key_points, suggested_diagnoses[], missing_investigations[], draft_medications[], red_flags[], confidence, citations[]}`.
- **Validation:** JSON schema; grounding check on every number and date; ICD codes validated against the terminology table; drug names validated against the formulary (an unrecognised drug name is dropped, not displayed).
- **Safety constraints:** cannot write to the record; cannot create a prescription; output rendered with a persistent "AI DRAFT" treatment; never shown as if physician-authored.
- **Human approval:** mandatory, per item (D-28).
- **Storage:** `AIInteraction` + the synthesis output linked to the visit; retained for audit and for later evaluation.
- **Audit:** prompt version, model version, input hash, all physician accept/edit/reject decisions as events.
- **Evaluation metrics:** grounding-violation rate (target: 0 reaching the physician); physician acceptance rate per suggestion type; **completion within SLA (§7.1 ≤5 min, tunable)**; blinded physician quality rating on a fixed evaluation set. *Numeric quality thresholds are proposed values requiring Dr. Nahid's approval (§19.4).*
- **Failure behaviour:** on timeout/error, the dashboard renders the full structured data with a clear "AI summary unavailable" banner (D-15). The clinic never stops because AI stopped.

---
**A2 · Medication Safety Engine** (§7.2) — *Phase 1, CP78 — **NOT GENERATIVE***
- **Input:** draft prescription items + patient allergies + current medications + latest renal function (eGFR) + age + sex + pregnancy status + comorbidities.
- **Processing:** deterministic evaluation of physician-authored rules (D-22): duplicate therapy, drug–drug interaction, allergy match (including cross-class), contraindication by condition, renal/hepatic dose adjustment, pregnancy/lactation, pediatric applicability, max dose.
- **Model requirement:** **none — no model is involved.** This is code and data.
- **Output:** findings with severity `BLOCK | WARN | INFO`, each with rule ID, version, bilingual message, and rationale; plus an explicit **coverage report** listing drugs for which no rules exist.
- **Validation:** every rule has unit tests including negative cases; the engine has a golden test suite that must pass before every deploy.
- **Safety constraints:** **fails closed** — if renal function is missing and a rule requires it, the finding is "cannot verify safety: creatinine not available", never silence. A drug outside the curated set shows "no automated check available", never a green tick.
- **Human approval:** `BLOCK` findings require an override with a typed reason, recorded as an event and surfaced to QA.
- **Storage / audit:** `SafetyCheckResult` per prescription with the exact rule versions evaluated — reproducible years later.
- **Evaluation:** rule test coverage; override rate per rule (a rule overridden 90% of the time is a bad rule and must be reviewed); zero known false negatives on the test corpus.
- **Failure behaviour:** if the engine cannot run, **prescription signing is blocked**. This is the one place where a system failure must stop the workflow.

---
**A3 · Clinical Assistant** (chart review; suggests missing questions/examinations) — *Phase 3, CP132*
- Input: visit data + history + guideline retrieval. Processing: retrieval + LLM gap analysis. Output: prioritised suggestion list with citations. Validation: suggestions must reference an actual absent data point (checked deterministically). Safety: advisory; never blocks. Approval: physician dismisses or acts, both recorded. Metrics: acceptance rate, false-suggestion rate. Failure: panel hidden.

**A4 · Diagnostic Support (Bayesian differential)** — *Phase 3, CP132*
- Input: structured findings. Processing: **prefer an explicit Bayesian/scored model over free LLM generation** — the blueprint says "Bayesian," and a transparent likelihood model is auditable where a generated list is not. LLM used only to render explanations. Output: ranked differentials with contributing findings. Safety: explicitly advisory, endocrine-scoped [R-06]; must display "not a diagnosis". Approval: physician. Metrics: top-3 concordance with physician's final diagnosis on retrospective cases. Failure: panel hidden.

**A5 · Nutrition Assistant** — *Phase 1 (basic) / Phase 3 (full), CP59, CP132*
- Input: 24-h recall, anthropometry, renal/hepatic status, diagnoses, budget indicators. Processing: deterministic caloric/macro targets + **retrieval from a curated Bangladeshi food database** (a content dependency: local foods with portion sizes and composition — this must be sourced or authored, and is an open content item) + LLM assembles a plan from retrieved items only. Output: a plan built from real catalogued foods. Safety: hard constraints applied deterministically (renal → potassium/protein limits) *after* generation; a plan violating a constraint is rejected, not warned about. Approval: nutritionist reviews before issue. Metrics: constraint-violation rate (target 0), nutritionist edit rate. Failure: template plan.

**A6 · Exercise Assistant** — *Phase 1 (basic) / Phase 3, CP60, CP132*
- Same pattern; contraindication filter is deterministic (§3 Step 8: no high-impact cardio in severe neuropathy) and applied after generation. Approval: exercise specialist.

**A7 · Follow-Up Predictor** — *Phase 3, CP134*
- Input: adherence history, prior no-shows, distance, socio-economic markers, contact history, diagnosis, medication cost burden. Processing: **gradient-boosted classifier (not an LLM)** — tabular, explainable, cheap, retrainable, and it produces calibrated probabilities. Output: no-show probability + top contributing factors + recommended interval. Safety: advisory; must never be used to deprioritise care for vulnerable patients — an explicit fairness review of features (income, geography) is required before deployment. Approval: staff act on the outreach list. Metrics: AUC, calibration, precision@k; **subgroup fairness monitoring**. Failure: default interval from the physician's plan.

**A8 · Outcome Monitoring & QA Engine** (nightly audits) — *Phase 3, CP135*
- Input: closed files. Processing: **deterministic rule checks first** (missing HbA1c, missing counseling ticks, guideline deviations, drug duplication) then LLM only for narrative explanation. Output: QA findings queue + Code Red triggers (§14.1). Safety: findings are reviewed by a human QA officer; Code Red RCA is human-led with AI assistance. Metrics: finding precision (how many are real), time-to-close. Failure: findings queue empty, alert raised — a silent QA engine must itself alarm.

**A9 · Research Assistant / Hypothesis Engine** — *Phase 3, CP130–131*
- Input: **anonymised research mart only** (never `core`). Processing: statistical scanning + LLM hypothesis drafting + narrative writing. Output: draft proposals, draft manuscript sections, always marked AI-drafted. Safety: **PII stripping enforced by the data path, not by prompt instructions** (D-48); statistical claims re-verified deterministically before publication; no p-value or effect size ever quoted from the LLM — only from the analysis engine. Approval: researcher and Dr. Nahid. Metrics: proportion of hypotheses judged worth pursuing; statistical-claim error rate (target 0).

**A10 · Global Knowledge & Guideline Engine (Automated CME)** — *Phase 3, CP137*
- Input: ingested guideline/literature corpus (D-10, D-25) + active patient panel characteristics. Processing: RAG + change detection. Output: **weekly Clinical Digest**; instant push **only** for critical safety recalls (§7.2's explicit alert-fatigue control). Safety: every statement carries a citation and a link to source; no automated change to any rule — a guideline update creates a *proposal* that Dr. Nahid approves before it alters system behaviour (D-25). Metrics: physician-rated usefulness, false-alert rate, recall detection latency.

**A11 · Medical Scribe (STT → SOAP)** — *Phase 3, CP133, dictation-first (D-12)*
- Input: audio (consented). Processing: STT → LLM structuring into SOAP → grounding against structured data. Output: draft note. Safety: patient consent required and recorded; audio retention policy explicit; transcript and note both marked AI-generated. Approval: physician edits and signs the note; unsigned notes never enter the record. Metrics: word error rate on a Bangla/English code-switched evaluation set, physician edit distance. Failure: manual note entry.

## 10.5 Prompt & model governance

- Prompts are **versioned artefacts in the repository** with an `agent_code`, semantic version, input schema, output schema, and a changelog. They are deployed like code, reviewed like code.
- **Model versions are pinned.** No floating aliases (D-13). This matters more on Gemini than on most providers, because preview and free-tier model aliases are retired quickly (D-07).
- Every change to a prompt or model runs the **frozen evaluation set** in CI; results are recorded, and a regression blocks the merge.
- A/B comparison is possible offline (evaluation set) before production, never as a silent live experiment on patients.

## 10.6 AI safety invariants (permanent, non-negotiable)

1. AI never writes to the clinical record. It proposes; a human commits.
2. AI never prescribes. Prescribing logic is deterministic; a physician signature is required (§7.3).
3. Every AI-derived element is visually marked as AI-generated wherever it appears, including on printed output if it ever appears there.
4. Every AI output is stored with its inputs, prompt version, and model version.
5. Every numeric claim is grounded against structured data before display.
6. AI unavailability degrades the experience; it never stops care (D-15).
7. Deterministic rules override generative output, always and everywhere.
8. PHI minimisation is enforced by the gateway, not by individual features.

---

# 11. OCR / NLP ARCHITECTURE

## 11.1 The problem, stated honestly

§6 requires a pipeline that turns a bag of paper from ten different providers into one chronological clinical truth. The realistic input distribution at DTHC:

| Document type | Frequency | Difficulty |
|---|---|---|
| Printed lab report, English, clean laser print | High | Low |
| Printed lab report, mixed Bangla headers + English values | High | **Medium-high** (script mixing) |
| Thermal/dot-matrix print, faded | Medium | High |
| Photo of a report taken with a phone, skewed, shadowed | **Very high** (this is how documents actually arrive) | **High** |
| Imaging/ultrasound report with narrative + measurements | Medium | Medium (needs entity extraction, not just OCR) |
| Discharge summary, multi-page, tabular + narrative | Medium | Medium-high |
| ECG/Echo report with embedded measurement tables | Medium | Medium |
| **Handwritten prescription** | High | **Excluded by §6.1 — image only** |

The blueprint's exclusion of handwritten prescriptions removes the single hardest problem. What remains is achievable, but only with confidence gating and human validation — and **not** by dropping in one OCR library and hoping.

## 11.2 Candidate architectures (D-16 — the choice is OPEN)

| | **A · Cloud document AI** | **B · Self-hosted OSS** | **C · VLM extraction** | **D · Hybrid (recommended shape)** |
|---|---|---|---|---|
| Examples | Google Document AI / Cloud Vision, Azure Document Intelligence, AWS Textract | PaddleOCR, Surya, docTR, Tesseract+Bengali | Multimodal LLM with a strict output schema | Cloud/OSS OCR for text+layout+tables → LLM/rules for structured extraction |
| Printed English | Excellent | Good–very good | Very good | Excellent |
| Bangla print | **Must be measured, not assumed** | Varies sharply by engine; conjuncts are the weak point | Often surprisingly strong | Best of both, routed per block |
| Mixed script | Weak-to-medium historically | Weak unless routed per block | **Strongest** | Strong |
| Tables | Excellent (purpose-built) | Medium | Good | Excellent |
| Phone photos | Good (with preprocessing) | Needs heavy preprocessing | Good | Good |
| Determinism | High | High | **Low** (needs validation) | Mixed, gated |
| Data residency | Leaves the country (**D-01 conflict**) | Stays in-house | Leaves the country | Configurable per stage |
| Cost | Per page, predictable | GPU/CPU + engineering | Per page, higher | Moderate |
| Effort | Low | **High** | Low–medium | Medium |

**Recommendation: do not decide from a table. Decide from data.** CP98 runs a two-week bake-off on ~200 real anonymised DTHC documents covering the distribution above, measuring character accuracy, field-extraction accuracy on the analytes that matter, cost per page, and latency. **Until that bake-off reports, the OCR engine remains an OPEN DECISION (D-16).**

What can be committed to now, safely, is the **pipeline shape** — because it is identical regardless of which engine wins:

## 11.3 The pipeline

```
 1  UPLOAD          mobile/web capture · multi-page · auto edge-detect · client-side downscale
                    → DOCUMENT_UPLOADED event · original bytes to GCS (immutable, CMEK)
 2  CLASSIFY        operator declares type + handwritten? (authoritative, D-18)
                    ML classifier cross-checks and may FLAG, never override
                    → handwritten path: store image, attach to record, STOP (§6.1)
 3  PREPROCESS      deskew · dewarp · crop · denoise · shadow removal · adaptive binarisation
                    · DPI normalisation · orientation detect · per-page quality score
                    → below quality threshold: request a re-scan BEFORE burning OCR cost
 4  OCR             text + word-level confidence + bounding boxes + layout regions
                    script detection per block → per-script routing if the bake-off supports it
 5  STRUCTURE       table detection & cell extraction · header/footer removal
                    · section segmentation (patient block / result block / narrative / signature)
 6  EXTRACT         document date · facility · test/report type · reason (when stated)
                    · analyte → value → unit → reference range triples
                    · narrative findings (ultrasound: ovarian volume, follicle count, LVEF …)
                    schema-constrained; every field carries confidence + source bbox
 7  NORMALISE       analyte name variants → canonical code (LOINC, D-20)
                    · unit conversion to canonical SI · Bengali digits ০–৯ → ASCII
                    · date parsing across dd/mm/yy, dd-mmm-yyyy, Bangla month names
                    · plausibility check (HbA1c 82% is an OCR error, not a patient)
 8  CONFIDENCE      per-field score combining OCR confidence, extraction confidence,
                    plausibility, and cross-page consistency
 9  VALIDATE        ≥ threshold → auto-populate, marked "from document", correctable
                    < threshold → human validation queue with the image region highlighted
                    RULE-DRIVING VALUES (creatinine, HbA1c) → human confirmation ALWAYS (D-19)
10  PERSIST         confirmed values → Observations (source = OCR) → the same timeline
                    as station-entered values, with document provenance
11  CHRONOLOGY      absolute ordering by document date across all providers (§6.2)
12  RED-LINE        deterministic rule library (§6.4): creatinine/pus cells → CKD pattern,
                    eGFR auto-derived (CKD-EPI 2021), BP series, PCO serial change
13  SUMMARISE       LLM narrative problem history from the assembled chronology (§6.3)
                    → grounded against extracted values → feeds A1 synthesis
```

## 11.4 Human validation UI (CP105)

The validation screen is where OCR quality becomes clinical safety. Requirements: the document image and the extracted field side by side, with the source region highlighted; keyboard-driven confirm/correct so a records officer can clear dozens of fields per minute; batch confirm for high-confidence pages; every correction stored as training/evaluation signal; and full attribution on each confirmation (it is a clinical write like any other).

## 11.5 Evaluation metrics (measured at CP98 and continuously thereafter)

| Metric | Definition | Target |
|---|---|---|
| CER / WER | Character/word error rate, per script | *Proposed target requires approval; measured baseline set at CP98* |
| Field extraction accuracy | Correct value+unit for the analyte, per analyte class | Highest priority: HbA1c, creatinine, glucose, TSH, FT4, lipids |
| Date extraction accuracy | Correct document date (drives the entire chronology) | **Must be very high — a wrong date corrupts the timeline, which is the whole point of §6** |
| Confidence calibration | Does an 0.9 confidence mean 90% correct? | Reliability curve reviewed; miscalibration is worse than low confidence |
| Human review rate | % of fields needing manual confirmation | Drives operational cost; the honest measure of whether the pipeline is working |
| Cost & latency per page | | Budget input for D-14 |

**No accuracy threshold is asserted here.** Thresholds will be proposed after CP98 measures reality, and require Dr. Nahid's approval before they gate anything clinical.

## 11.6 Failure behaviour

Preprocessing quality failure → ask for a re-scan while the patient is still present (far cheaper than a wrong value). OCR engine failure → document stored and queued; the record shows "digitisation pending", never a partial extraction presented as complete. Extraction failure → the document remains attached and viewable as an image; the physician still sees the paper, which is what they had before DTHCMS existed. **The system must never be worse than paper.**

---

# 12. SECURITY ARCHITECTURE

> Regulatory statements below are **recommendations pending legal confirmation** (D-01). Where a control is presented as required by law, that requirement must be verified by counsel; where it is presented as good practice, it stands on its own merits.

## 12.1 Threat model (what we are actually defending against)

| Threat | Realistic? | Primary control |
|---|---|---|
| Lost or stolen clinic phone containing PHI | **Very** | SQLCipher-encrypted local DB, short token lifetime, remote device revocation, no PHI in OS backups |
| Shared/borrowed device causing misattribution | **Very** (§4.2 explicitly allows shared devices) | Per-user session, fast re-auth, short idle timeout on shared devices, device-bound events |
| Staff accessing records they have no business reading | **Very** | RBAC + audit + break-glass with justification + periodic access review |
| Credential sharing among floor staff | **Very** (the most common real-world failure in clinics) | Per-user 2FA enrolment, biometric attendance cross-check, anomaly detection on impossible-concurrency |
| Insider data exfiltration for commercial gain | Plausible | Export controls, rate limits, watermarked exports, audited research access |
| Ransomware / destructive attack | Plausible | Immutable backups with retention lock, separate key, restore drills |
| Compromise of an external AI/OCR provider | Plausible | PHI minimisation (D-08), contractual controls, ability to switch providers |
| Tampering with a prescription after signing | **Directly anticipated by §9.3** | Canonical-hash signature + QR verification + hash chain |
| Public internet attack on the API | Certain | Cloud Armor/WAF, rate limiting, no public endpoints beyond auth + QR verify |

## 12.2 Authentication (D-43, D-44, D-45)

- **Password:** argon2id, per-user salt, tuned parameters, minimum length over composition rules, breached-password check against a local corpus, no forced rotation without cause.
- **Access token:** 10–15 min, signed, carries `user_id`, `session_id`, `device_id`, `active_role`, `facility_id`.
- **Refresh token:** opaque, server-stored, device-bound, rotating; **reuse detection revokes the entire session family**.
- **2FA:** TOTP at enrolment and for privileged actions; device trust for routine floor work; **step-up 2FA mandatory** for prescription signing, RBAC changes, research export, supervisor override, and break-glass.
- **Recovery:** administrator-mediated reset with identity verification in person (a clinic has the enormous advantage that staff are physically present) + full audit. **No email-link self-service reset**, which is the most commonly abused recovery path.
- **Lockout:** progressive delay rather than hard lockout — a locked-out clinician mid-clinic is a patient-safety problem; a slowed attacker is not.

## 12.3 Device security (D-46)

Admin-enrolled devices only. Server-issued keypair with the private key in Android Keystore/iOS Keychain; every request signed or channel-bound. Unenrolled devices may authenticate a user for read-only web access but **may not write clinical events**. Device revocation is immediate and takes effect on the next request; queued offline events from a revoked device are quarantined for review rather than silently accepted or silently dropped.

## 12.4 Authorization / RBAC (§4.4)

- **Model:** role → permissions, permission = `resource.action[.scope]`. Deny by default; no implicit inheritance; explicit deny beats allow.
- **The blueprint's named constraints implemented literally:** nutritionist writes diet, reads labs, **cannot read prescriptions**; registration reads/writes demographics but is **blinded to sensitive diagnoses**; pharmacist sees **drugs and dosing only, diagnoses hidden**.
- **Field-level redaction, not just endpoint blocking.** A pharmacist fetching a prescription receives a payload with no diagnosis field at all — not a payload with a hidden diagnosis. This is enforced in the serialisation layer so no handler can leak it by accident.
- **Enforced in three places:** middleware (endpoint), service (resource ownership and scope), serialiser (field visibility). The UI *also* hides controls, but UI hiding is never the security boundary.
- **Break-glass:** an emergency-access path exists (a real clinical need), requires a typed justification, notifies an administrator immediately, and is prominently audited.
- **Access review:** quarterly report of who holds what, with dormant-permission flagging.

## 12.5 Encryption

- **In transit:** TLS 1.3 everywhere; HSTS; certificate pinning on mobile for the API domain; **no plaintext anywhere on the clinic LAN**, including to printers where the protocol allows.
- **At rest:** CMEK on database, buckets and backups; envelope encryption with per-patient keys for `IDENTIFIER`-class fields; SQLCipher on mobile with the key held in the OS keystore and released only after successful authentication.
- **Keys:** Cloud KMS/HSM; rotation schedule; separation between the production key and the backup key; the prescription signing key never leaves KMS (sign operations happen in KMS, the private key is not exportable).

## 12.6 Application security

Strict input validation at the transport boundary; parameterised SQL only (`sqlc` makes this structural); output encoding; CSP, `X-Frame-Options`, `Referrer-Policy` and friends; CORS restricted to known origins; upload handling with type sniffing, size caps, and a rendering sandbox for documents; per-user/device/endpoint rate limits; idempotency keys on mutations; **PHI redaction in logs and error reports** (including any third-party error reporter, which must be configured to scrub or not used at all).

## 12.7 Audit & monitoring

Two distinct trails: the **clinical ledger** (§7) and the **security audit log** (logins, failed logins, permission changes, exports, prints, break-glass, device enrolment/revocation, configuration changes). Both are append-only. Alerting on: impossible concurrency (one user, two distant devices), bulk read patterns, out-of-hours access spikes, repeated authorisation denials, export volume anomalies, hash-chain verification failure.

## 12.8 Privacy controls

Consent enforcement at the point of use, not merely recorded (D-02); data minimisation at every boundary, especially the AI gateway; documented retention per data class; crypto-shredding for erasure (D-05); research anonymisation as a one-way path with separated re-identification keys (D-48); and a documented, tested **breach response runbook** — including who notifies the National Data Management Authority and within what window, once counsel confirms the obligation.

## 12.9 Security assurance programme

| When | Activity |
|---|---|
| Every commit | Secret scanning, dependency vulnerability scan, SAST, lint |
| Every build | Container image scan, SBOM generation |
| Every checkpoint | Security section in the review; threat-model delta for new surface |
| CP94 (pre-pilot) | Internal security review + **external penetration test** (D-50) |
| CP156 (pre-production) | Full pentest, remediation, retest |
| Quarterly | Access review, restore drill, dependency refresh |
| Annually | External pentest, threat model refresh, key rotation review |

---

# 13. OFFLINE / SYNCHRONIZATION ARCHITECTURE

## 13.1 The requirement, precisely

§15.2: *"a Wi-Fi drop must never lose a station entry; events queue locally and sync with conflict-safe ordering."* Combined with §2's mobile-first principle, this means: **every station capture app must be fully functional with no network**, for the duration of a clinic session, with zero data loss and zero silent corruption.

Note the corollary that is easy to miss: **2FA by SMS would break this** (a staff member cannot log in during an outage). This is why D-45 recommends TOTP + device trust.

## 13.2 Why append-only events make this tractable

Most offline-sync horror stories come from replicating *mutable rows*: two devices edit the same record, and the system must guess. DTHCMS does not replicate rows. It queues **immutable facts**, each with a client-generated ID. Two operators recording two measurements is not a conflict — it is two facts. This is the strongest architectural argument for event sourcing in this system, and it is why the offline design is roughly 1,500 lines rather than a permanent source of bugs.

## 13.3 Local architecture (mobile)

```
┌───────────────────────────────────────────────────────────────┐
│  React Native app                                             │
│                                                               │
│  UI ──write──► Command handler ──► LOCAL SQLITE (SQLCipher)   │
│   ▲                                 ├─ outbox        (queue)  │
│   │                                 ├─ local_events  (applied)│
│   └──read──── Local projections ◄───┤ projections    (state)  │
│                                     ├─ reference_cache        │
│                                     └─ sync_meta              │
│                                                               │
│  Sync engine (background + on-connectivity + on-foreground):  │
│    PUSH  outbox (FIFO, batched, per-aggregate order preserved)│
│    PULL  server events since last cursor                      │
│    RECONCILE  apply server events to local projections        │
└───────────────────────────────────────────────────────────────┘
```

**Outbox row:** `{event_id (UUIDv7), aggregate, payload, occurred_at, attempts, last_error, state: PENDING|IN_FLIGHT|SYNCED|FAILED|QUARANTINED, created_at}`.

## 13.4 The sync protocol

**Push:**
1. Batch up to N pending events, ordered by `occurred_at`, preserving per-aggregate order.
2. `POST /sync/events` with an `Idempotency-Key` per batch and each event carrying its own `event_id`.
3. Server processes **each event independently** and returns a per-event result: `ACCEPTED | DUPLICATE | REJECTED(reason)`.
4. `ACCEPTED` and `DUPLICATE` both mark the outbox row `SYNCED` — duplicates are success, not error.
5. `REJECTED` (validation failure, revoked device, insufficient permission, patient merged) moves the row to `FAILED` with a human-readable reason and **surfaces it in the app**. Failed events are never silently dropped.
6. Partial batch success is normal and handled: one bad event never blocks the rest.

**Pull:** `GET /sync/events?since={global_seq}&scope=...` returns events for patients in the device's scope (today's visits, assigned station), RBAC-filtered, paginated, with a new cursor. Realtime WebSocket delivery is an accelerator on top of this; the cursor-based pull is the reliable path and the one that runs after any reconnection.

**Reference data** (formulary, templates, rules, terminology) syncs by version tag on app foreground; the app refuses to write a prescription with a stale formulary beyond a configured age.

## 13.5 Retry, backoff, and the failure ladder

Exponential backoff with jitter (2s → 5s → 15s → 60s → 5m, capped), immediate retry on connectivity regained, and retry on app foreground. After N failures with a retryable error, the event stays `PENDING` and the UI shows a persistent, honest sync indicator. After a non-retryable rejection, it moves to `FAILED` and appears in a **"Needs attention"** screen where the operator can view the reason, correct and resubmit, or escalate. **The operator is never told "synced" when it is not.**

## 13.6 Conflict handling — the two-device question, answered

**Scenario: two devices record height for the same patient within minutes.**

There is no lost-update conflict, because neither device is updating a row. Both events append. Then:

- The projection applies the later `occurred_at` as the current value.
- **Both values remain visible** in the value history with full attribution — this is exactly what §4.3 demands.
- The system raises a **`DOUBLE_ENTRY_DETECTED`** review flag when the same measurement code is recorded twice for the same visit within a configurable window with differing values. It appears on the physician/QA view with both attributions.
- Resolution uses the standard correction workflow (§7.7). Nothing is auto-discarded.

**Scenario: offline device records a value; meanwhile someone corrects that value online; the offline device syncs afterwards.**
The offline event appends with its true `occurred_at` (earlier), so the correction — which occurred later — remains the current value. The late event is visible in history, marked "synced late". Clinically correct, forensically complete.

**Scenario: a genuine state-machine conflict** (offline device tries to modify a prescription that was signed online in the meantime). This one *is* a real conflict, because signing is a state transition with an invariant. The server rejects with `REJECTED(prescription_already_signed)`, and the event lands in "Needs attention" for a human. **State-changing commands (sign, close visit, dispense, merge) are deliberately excluded from offline capability** — they require connectivity. Measurements never do.

## 13.7 What works offline and what does not

| Fully offline | Requires connectivity |
|---|---|
| All station data capture (anthropometry, vitals, exam, history, counseling ticks, nutrition, exercise) | Prescription signing |
| Derived value computation (local `clinical-calc`) | QA clearance and visit close |
| Patient lookup among today's cached patients | New patient registration against the **global** duplicate check (offline registration creates a provisional record flagged for duplicate review at sync) |
| Viewing cached patient history | AI synthesis |
| Document capture (queued for upload) | Dispensing (stock is authoritative server-side) |
| Local queue/traffic view (may be stale) | Cross-station realtime updates |

This split is a product decision as much as a technical one and **should be confirmed by Dr. Nahid** — particularly offline registration, where the trade-off is "keep working" versus "duplicate risk" (§3 Step 1 makes duplicate prevention a hard checkpoint).

## 13.8 Local data security and expiry

SQLCipher with the key in the OS keystore, released only after successful authentication · a cached-patient TTL (default: today's visits + 7 days) with automatic purge · immediate local wipe on logout, device revocation, or N consecutive auth failures · no PHI in OS-level backups (`allowBackup=false`, excluded from iCloud/Google backup) · no PHI in logs or crash reports.

## 13.9 User-visible sync status (a UX requirement, not a technical one)

A persistent status pill: **Synced** (grey, quiet) · **Syncing (n)** (blue) · **Offline — n queued** (amber, prominent) · **n need attention** (red, tappable). Per-record indicators on any value not yet confirmed by the server. A tappable sync detail screen listing queued and failed items with reasons and a manual retry.

Operators must be able to trust the indicator absolutely — a system that says "synced" when it is not will be discovered exactly once, and after that no one will trust the system again.

## 13.10 Testing offline (mandatory in CI and on-device)

Airplane mode mid-entry · kill the app with a full queue and relaunch · sync 200 queued events at once · server rejects one event in a batch of fifty · device clock set 3 hours wrong · token expires while offline (must refresh cleanly on reconnect, not lose the queue) · device revoked while offline · duplicate submission of an entire batch · flaky network (10% packet loss, 3s latency) · storage full. Each of these is a named test case in CP68 and stays in the regression suite forever.

---

# 14. MODERN UI/UX STRATEGY & FRONTEND ARCHITECTURE

## 14.1 The design brief in one line

**It must look and feel like a modern clinical product, not a hospital admin panel.** Dense where density helps a physician think, spacious where an operator's thumb needs a target, bilingual without looking like a translation, and fast enough that nobody waits for the software.

## 14.2 Design principles

1. **One design system, all surfaces.** Web, mobile, field app, print — one token source, one component vocabulary. No module gets its own look.
2. **Density is contextual.** The physician dashboard is information-dense by design; the anthropometry entry screen shows one field at a time in large type.
3. **Colour carries clinical meaning, and only clinical meaning.** Red = danger/stop, amber = caution, green = target achieved (§9.1's convention, applied consistently everywhere, not just on paper). Decorative colour never uses the clinical palette.
4. **Attribution is always one interaction away.** [R-03] Every value shows its "Entered by" chip on hover/tap. It is a component, not a per-screen decision.
5. **AI is always visibly AI.** A consistent treatment (marker + subtle background + label) for every AI-generated element, everywhere.
6. **The system states what it does not know.** Empty, loading, stale, offline, unverified and failed states are designed first-class — clinical software that hides uncertainty is dangerous software.
7. **Heads-up operation.** §15.2's requirement that operators look at the patient, not the screen: minimal typing, large targets, numeric keypads, steppers and pickers over free text, immediate confirmation, no confirmation dialogs for routine entry.
8. **Bangla and English are equals.** Not an afterthought toggle: type scale, line height, and component sizing are validated in both scripts (Bangla needs more line height, and truncation behaves differently).

## 14.3 Design tokens

| Token group | Definition |
|---|---|
| **Typography** | English UI: Inter (or system stack). Bangla: a high-quality Bengali family with full conjunct coverage — **Noto Sans Bengali** is the safe default; a licensed alternative may be chosen for brand. Type scale 12/14/16/18/20/24/30/36/48. Bangla line-height +15% over Latin. Tabular numerals mandatory for all clinical values. |
| **Spacing** | 4px base; scale 4/8/12/16/24/32/48/64. |
| **Radius** | 6px controls, 12px cards, 999px pills. |
| **Elevation** | Four levels; used sparingly — flat, bordered surfaces read as more clinical and print better. |
| **Colour — brand** | A restrained DTHC primary (deep teal/blue family, to be finalised with Dr. Nahid against DTHC's existing identity) + neutral greys. |
| **Colour — clinical semantic** | `critical` (red), `warning` (amber), `caution` (orange), `normal` (green), `info` (blue), `unknown` (grey). **Never reused decoratively.** Verified for colour-blind distinguishability, and never the sole carrier of meaning (always paired with an icon or label). |
| **Colour — AI** | One distinct AI accent (violet family) used only for AI-generated content. |
| **Motion** | 150ms micro, 250ms transitions, respects reduced-motion. No animation on clinical data changes beyond a brief highlight. |
| **Touch targets** | ≥48×48dp on mobile, ≥56dp for primary station actions. |

Tokens live in `packages/design-tokens` as JSON → CSS variables (web), NativeWind theme (mobile), and print stylesheet variables. One change propagates everywhere.

## 14.4 Component inventory (built once, in `packages/ui` + mobile equivalents)

**Foundations:** Button (primary/secondary/ghost/danger, 3 sizes, loading, disabled) · IconButton · Input · NumericInput (steppers, unit suffix, keypad) · Select · Combobox (async, used by the formulary autocomplete) · DatePicker (**with a Bangla-friendly, error-resistant DOB entry mode — this field is clinically load-bearing**) · Checkbox · Radio · Switch · Slider · Textarea · FileUpload/Scan · SearchField.

**Layout & structure:** AppShell · SidebarNav · Topbar · Breadcrumb · Card · Panel · Section · Tabs · Accordion · Drawer · Modal/Dialog · Sheet (mobile) · Split view · ScrollArea · Divider.

**Data:** DataTable (sortable, filterable, virtualised, column visibility) · KeyValueList · DefinitionRow · Stat/KPI tile · Sparkline · GradientBar (§9.1) · Chart wrappers · Timeline · **ValueWithAttribution** (the "Entered by" chip component) · **DualUnitValue** (P-6) · TrendIndicator · ReferenceRangeBar.

**Status & feedback:** Badge · StatusPill · ClinicalFlag · AlertBanner (4 severities) · Toast · InlineError · ProgressBar · StepIndicator · **SyncStatusPill** (§13.9) · **AIBadge** · ConfidenceIndicator (OCR) · Skeleton · EmptyState · ErrorState · SuccessState · OfflineBanner.

**Domain components:** PatientHeaderCard · PatientSearchResult · QueueCard · StationProgressBar · CounselingChecklist · VitalTile · CriticalAlertModal (visual + audible per §3 Step 5) · MedicationRow · SafetyFindingCard · PrescriptionPreview · DocumentViewer with region highlight · TimelineScrubber · CohortFilterBuilder.

Every component ships with: both themes, both languages, keyboard support, screen-reader labels, loading/empty/error states, and a Storybook entry. **A component without an empty state and an error state is not done** (§22).

## 14.5 Station app (mobile) UX

- **Login → today's queue → patient → my station's form.** Three taps to start entering. No nested menus.
- **One concept per screen**, large type, big targets. Numeric keypad opens automatically for numeric fields.
- **Instant derived feedback:** BMI appears the moment height and weight exist, with its interpretation band and dual units (P-4, P-6).
- **Validation is immediate and specific:** "Height 15 cm is outside the plausible range (50–250). Re-measure or confirm." Never a generic "invalid input".
- **Role switching** [R-02] from a persistent header control — one tap, no logout, and the header colour changes so the operator can see at a glance which hat they are wearing.
- **Sync status always visible** (§13.9).
- **Attribution shown on every value they can see**, including their own — because seeing your own name on your entries builds the habit the correction workflow depends on.
- **One-handed reachability:** primary actions in the bottom third of the screen.

## 14.6 Physician dashboard UX (§8) — the most important screen in the system

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ ◄ Queue    Ayesha Rahman · F · 42y · DTHC-2026-04821    [Prev visit ▾] [⚙]   │
├───────────────────┬────────────────────────────────┬─────────────────────────┤
│ SNAPSHOT          │  CLINICAL SUMMARY              │  AI ASSISTANT      [AI] │
│                   │                                │                         │
│ ⚠ PENICILLIN      │  42-year-old woman with type 2 │ Suggested diagnoses     │
│   ALLERGY         │  diabetes for 8 years, present-│  E11.9 T2DM  [✓][✎][✗] │
│ ⚠ BP 168/98       │  ing with 3 months of worsening│  E78.5 Dyslip [✓][✎][✗] │
│                   │  glycaemic control...          │                         │
│ BP    168/98  ▲   │                                │ Missing data            │
│ Pulse    88       │  Examination: ...              │  • Eye exam overdue 8mo │
│ BMI    31.4  ▲    │                                │  • Foot exam not done   │
│  (Obese I, Asian) │  Investigations (chronology):  │  • ACR not ordered      │
│ Wt    78.5 kg     │   12 Mar  HbA1c 8.2 ▲          │                         │
│  (173 lb)         │   12 Mar  Creatinine 1.4 ▲     │ Draft plan              │
│ Ht    158 cm      │   12 Mar  eGFR 46 ▲ (CKD G3a)  │  Metformin 1g BD  [✓]  │
│  (5′2″)           │   04 Jan  TSH 3.1              │  Empagliflozin... [✓]  │
│                   │                                │  ⚠ eGFR 46 — check     │
│ HbA1c last 5      │  ▬▬ red-lined abnormalities    │     dose guidance      │
│  ▁▃▅▆█  8.2       │     carried from documents     │                         │
│                   │                                │ Guideline: ADA 2026 §9  │
│ Counseling ✓6/7   │                                │  [view citation]        │
│  ✗ Insulin sites  │                                │                         │
├───────────────────┴────────────────────────────────┴─────────────────────────┤
│ TIMELINE  ◄──────────────────────────────────────────────────────────────►   │
│ 2019    2020    2021    2022    2023    2024    2025    2026                 │
│ ●Dx T2DM    ●Metformin ────────────────────────────────────────────          │
│                    ●HTN   ●Amlodipine ──────────────────────                 │
│  HbA1c  ─╱▔▔╲──╱▔▔▔╲────╱▔▔▔▔╲────╱▔▔▔                                       │
│  Weight ─────╲___╱▔▔▔╲______╱▔▔▔▔▔                                           │
│                                        ⬤ hover any point → "Entered by" chip │
└──────────────────────────────────────────────────────────────────────────────┘
```

Design decisions embedded above: alerts are the very first thing in the reading path; every abnormal value carries a direction arrow and its interpretation, not just a number; the AI column is visually distinct and every suggestion is accept/edit/reject (D-28); the counseling status is visible without navigation (§5.4); dual units are inline (P-6); the timeline is one continuous axis so cause and effect correlate visually (§8).

## 14.7 Print design (§9)

The prescription is a **designed artefact**, not a form dump: pre-printed-style header, generous white space, medicine names in bold at a larger size, dosage in a visually distinct treatment, Bangla instructions in a readable Bengali face at a size chosen for older patients, sparklines and gradient bars placed where the eye lands after the medicine list, QR bottom-right, and the back page laid out as a professional letter to a colleague rather than a data table. **Print CSS is developed against the actual clinic printer** (D-58) with a colour-managed proof, and reviewed by Dr. Nahid on paper — screen approval is not approval.

## 14.8 Accessibility

WCAG 2.2 AA as the target: contrast ≥4.5:1 for text and ≥3:1 for UI components, full keyboard operability on web, visible focus, semantic headings and landmarks, form labels and error association, screen-reader labels on every icon-only control, ≥48dp touch targets, respect for OS text scaling up to 200% without loss of function, no meaning conveyed by colour alone, and audible alerts paired with visual ones (§3 Step 5). **Explicitly relevant here:** §9.1 requires typography readable by visually impaired patients — that requirement applies to the screen as much as the paper.

## 14.9 Frontend architecture — web (Next.js)

```
web/src/
├── app/                          App Router; route groups by audience
│   ├── (auth)/                   login, 2FA, device enrolment
│   ├── (clinical)/               physician dashboard, patient, visit, timeline
│   ├── (stations)/               desktop fallbacks for station work
│   ├── (qa)/  (pharmacy)/  (crm)/  (research)/  (admin)/  (exec)/
│   └── verify/[token]/           PUBLIC prescription QR verification
├── features/                     FEATURE-FIRST, mirrors backend modules
│   └── prescription/
│       ├── api/                  typed hooks over the generated client
│       ├── components/           feature-local components
│       ├── hooks/   model/   schemas/
│       └── index.ts              the feature's public surface
├── components/ui/                design-system primitives (no domain knowledge)
├── lib/                          api client, auth, ws, i18n, permissions, formatters
├── stores/                       Zustand: session, activeRole, ui
└── styles/                       tokens.css, print.css
```

**Rules:** features never import each other's internals, only their `index.ts`. `components/ui` never imports from `features`. Server Components for initial data-heavy loads; Client Components where interaction or realtime is needed. Every clinical value renders through `<ValueWithAttribution>` and `<DualUnitValue>` — never as raw text, so P-2 and P-6 cannot be forgotten screen by screen.

**State:** server state in TanStack Query keyed by resource; **WebSocket messages invalidate query keys rather than mutating cache directly** (one code path for fresh data, whether it came from a refetch or a push). Client state in Zustand (session, active role, UI). Form state in React Hook Form + Zod schemas shared with mobile.

**Permissions:** a `usePermission()` hook and a `<Can action="prescription.sign">` wrapper; the UI hides what the user cannot do, and the server denies it independently.

**Errors:** error boundaries per route group; the error model from §8.6 mapped to bilingual user messages; a global offline/degraded banner; retry affordances on every failed query.

## 14.10 Frontend architecture — mobile (React Native)

```
mobile/src/
├── app/                  Expo Router: (auth), (queue), (station), (patient), (sync)
├── features/             anthropometry, vitals, history, counseling, nutrition,
│                         exercise, records-capture, education  (same shape as web)
├── db/                   SQLite schema, migrations, outbox, projections, sync engine
├── components/ui/        NativeWind components from the shared tokens
├── lib/                  api, auth, secure storage, connectivity, clinical-calc, i18n
└── stores/               session, activeRole, syncStatus
```

**Every write goes through the local command handler → outbox → sync engine.** No screen ever calls the API directly for a clinical write; that single rule is what makes the offline guarantee hold as the app grows (§13).

## 14.11 Internationalisation

ICU message format via `next-intl` (web) and `i18n-js`/`intl` (mobile) · every user-facing string in message files from CP09/CP10 — **retrofitting i18n is a multi-week tax and is avoided by never starting monolingual** · language preference per user with an instant in-app toggle · **clinical content (drug warnings, counseling items, SMS templates) lives in versioned database content, not in code message files**, because Dr. Nahid must be able to edit it without a release (D-11) · dates rendered `Asia/Dhaka`; Bengali vs ASCII numeral rendering is a locale setting to be confirmed (patients often read Bengali digits; lab values conventionally appear in ASCII).

---

# 15. COMPLETE CHECKPOINT LIST

**160 checkpoints.** Effort is in **developer-days for one experienced full-stack engineer**, excluding review cycles and excluding physician content-authoring time. Assumptions behind these numbers are stated in §21.

## PHASE 0 — FOUNDATION & STABILISATION (CP01–CP14)

| ID | Name | Depends on | Days |
|---|---|---|---|
| CP01 | Repository, monorepo scaffolding & CI skeleton | — | 3 |
| CP02 | Architecture guardrails, ADRs & coding standards | CP01 | 3 |
| CP03 | Cloud project, environments & IaC baseline | CP01, D-01 | 5 |
| CP04 | Local development environment (docker-compose) | CP01 | 2 |
| CP05 | Go backend skeleton & platform layer | CP02, CP04 | 5 |
| CP06 | Database foundation & migration framework | CP05 | 4 |
| CP07 | Observability baseline (logs, traces, metrics, PHI redaction) | CP05 | 4 |
| CP08 | Prototype assessment & data-migration decision | D-51 | 3 |
| CP09 | Design system foundation (tokens, typography, bilingual) | CP01 | 6 |
| CP10 | Web application shell (Next.js) | CP09 | 4 |
| CP11 | Mobile application shell (React Native/Expo) | CP09 | 5 |
| CP12 | API contract (OpenAPI) & generated clients | CP05, CP10, CP11 | 4 |
| CP13 | Test harness, quality gates & synthetic data generator | CP06, CP10, CP11 | 6 |
| CP14 | **Phase 0 review & architecture sign-off** | CP01–CP13 | 2 |

> **Phase 0 re-sequencing (22 Aug 2026).** Hosting is deferred (D-01 still open, and the clinic is not yet ready to host). Nothing in Phase 0 or early Phase 1 needs a cloud account: the whole stack runs locally under CP04's docker-compose. **CP03 (cloud project and IaC) therefore moves out of the Phase 0 critical path and is scheduled when the hosting decision is made** — realistically before CP69/CP70, and certainly before CP95. Revised Phase 0 order: **CP01 → CP02 → CP04 → CP05 → CP06 → CP07 → CP08 → CP09 → CP10 → CP11 → CP12 → CP13 → CP14**, with CP03 inserted when hosting is decided. CP08 is now a one-line decision record (D-51 resolved), releasing 3 days. Phase 0 effective effort drops to ~48 developer-days.
| | **Phase 0 subtotal** | | **56** |

## PHASE 1 — CLINIC CORE / MVP (CP15–CP95)

### Identity, access & audit foundations
| ID | Name | Depends on | Days |
|---|---|---|---|
| CP15 | User, role & permission data model | CP06 | 4 |
| CP16 | Password authentication & session management | CP15 | 6 |
| CP17 | TOTP 2FA & step-up authentication | CP16 | 4 |
| CP18 | Device enrolment, credentials & attribution | CP16 | 6 |
| CP19 | RBAC policy engine | CP15 | 5 |
| CP20 | RBAC enforcement (endpoint · service · serialiser) | CP19 | 5 |
| CP21 | Admin console: users, roles, devices | CP20, CP10 | 6 |
| CP22 | Security audit log & human-readable trail | CP16, CP20 | 4 |

### Event & realtime core
| ID | Name | Depends on | Days |
|---|---|---|---|
| CP23 | Event store schema, append path & hash chain | CP06 | 8 |
| CP24 | Write envelope contract & idempotency | CP23, CP18 | 5 |
| CP25 | Projection framework, checkpointing & replay | CP23 | 8 |
| CP26 | Realtime gateway (WebSocket + Redis pub/sub) | CP23, CP20 | 7 |
| CP27 | Client realtime integration (web + mobile) | CP26, CP10, CP11 | 5 |

### Patient identity
| ID | Name | Depends on | Days |
|---|---|---|---|
| CP28 | Patient domain model & migration | CP06, CP23 | 4 |
| CP29 | Patient registration API & Research ID | CP28, CP24 | 5 |
| CP30 | Duplicate detection engine | CP29 | 6 |
| CP31 | Patient search & retrieval API | CP28 | 4 |
| CP32 | Registration UI (web) | CP29, CP10 | 6 |
| CP33 | Registration on mobile | CP29, CP11 | 5 |
| CP34 | Patient photo capture & object storage | CP33, CP03 | 4 |
| CP35 | Patient edit & demographic correction | CP29, CP23 | 4 |
| CP36 | Consent capture & enforcement | CP29 | 6 |
| CP37 | Patient timeline read model v1 | CP25, CP31 | 5 |

### Visit & station workflow
| ID | Name | Depends on | Days |
|---|---|---|---|
| CP38 | Visit & encounter lifecycle | CP28, CP23 | 6 |
| CP39 | Station queue & assignment | CP38 | 6 |
| CP40 | Clinic Traffic Control board | CP39, CP26 | 7 |
| CP41 | In-session role switching on device | CP19, CP18 | 3 |

### Clinical capture
| ID | Name | Depends on | Days |
|---|---|---|---|
| CP42 | Observation model & units framework | CP23, CP38 | 6 |
| CP43 | Clinical calculation library (BMI/BMR/eGFR) + Go↔TS parity | CP42 | 5 |
| CP44 | Dual-unit display components [R-08] | CP43, CP09 | 3 |
| CP45 | Anthropometry capture (mobile) [R-01] | CP42, CP43, CP11 | 6 |
| CP46 | Plausibility validation & impossible-input rejection | CP45 | 4 |
| CP47 | Pediatric growth percentile engine | CP43, **D-21** | 8 |
| CP48 | Pediatric percentile card & obesity flag [R-06] | CP47 | 4 |
| CP49 | Vitals capture (mobile) | CP42, CP11 | 5 |
| CP50 | Critical value alerts & escalation | CP49, CP26, **D-27** | 6 |
| CP51 | Clinical examination capture (foot, neuropathy, retinopathy) | CP42, CP11 | 6 |
| CP52 | Terminology service (ICD + internal dictionary) | CP06, **D-24** | 6 |
| CP53 | Medical history capture | CP52, CP42 | 6 |
| CP54 | Allergy hard stop | CP53 | 4 |
| CP55 | Counseling template engine & admin UI [R-07] | CP06, CP21 | 7 |
| CP56 | Counseling mobile ticking | CP55, CP11 | 5 |
| CP57 | Counseling fail-closed gate & physician verification | CP56, CP39 | 5 |
| CP58 | Lifestyle assessment & composite risk score | CP42, **D-26** | 6 |
| CP59 | Nutrition assessment capture | CP42, CP11 | 6 |
| CP60 | Exercise assessment capture | CP42, CP11 | 5 |
| CP61 | Attribution UI everywhere ("Entered by") [R-03] | CP24, CP09 | 4 |
| CP62 | Correction & flagging workflow [R-04] | CP61, CP23 | 8 |
| CP63 | Operator quality record & recurrence detection | CP62 | 5 |

### Offline capability
| ID | Name | Depends on | Days |
|---|---|---|---|
| CP64 | Mobile local database (SQLCipher) & outbox | CP11, CP24 | 8 |
| CP65 | Sync protocol — server side | CP64, CP23 | 7 |
| CP66 | Sync engine — client side & reconciliation | CP65 | 10 |
| CP67 | Offline UX, failure ladder & "needs attention" | CP66 | 5 |
| CP68 | Offline test suite (device + CI) | CP67 | 6 |

### Asynchronous AI
| ID | Name | Depends on | Days |
|---|---|---|---|
| CP69 | Async job infrastructure & queue monitoring | CP06, CP07 | 5 |
| CP70 | AI gateway, PHI minimisation & prompt registry | CP69, **D-07, D-08** | 8 |
| CP71 | Pre-consultation synthesis agent v1 [R-05] | CP70, CP37 | 10 |
| CP72 | Grounding validation & AI evaluation harness | CP71 | 8 |
| CP73 | Physician dashboard v1 (three panels) | CP71, CP37, CP10 | 12 |
| CP74 | Longitudinal timeline v1 (scrubbable) | CP37, CP73 | 10 |

### Prescription engine
| ID | Name | Depends on | Days |
|---|---|---|---|
| CP75 | Medicine formulary model & admin UI [R-16] | CP06, CP21, **D-56** | 7 |
| CP76 | Formulary 2-letter autocomplete (performance-critical) | CP75 | 4 |
| CP77 | Medication rule model & physician authoring UI | CP75, **D-22** | 8 |
| CP78 | Deterministic medication safety engine | CP77, CP54 | 10 |
| CP79 | Renal dosing integration (eGFR-driven) | CP78, CP43 | 4 |
| CP80 | Prescription draft model & API | CP75, CP23 | 7 |
| CP81 | Prescription editor UI | CP80, CP76, CP10 | 10 |
| CP82 | AI draft integration & per-item accept/edit/reject | CP81, CP71, **D-28** | 6 |
| CP83 | QA engine & fail-closed clearance (Step 10) | CP80, CP57 | 8 |
| CP84 | Digital signature & signing flow | CP83, CP17, **D-04** | 7 |
| CP85 | QR verification & public verify page | CP84 | 4 |
| CP86 | Drug warning library [R-12] | CP75, **D-54** | 5 |
| CP87 | Prescription graphs & gradient bars [R-11] | CP37, CP09 | 6 |
| CP88 | Patient-reported 1–10 improvement score [R-11] | CP42 | 3 |
| CP89 | A4 print pipeline — front page | CP84, CP86, CP87, **D-58** | 10 |
| CP90 | Back page — evaluation & rationale [R-13] | CP89, **D-06** | 6 |
| CP91 | Bilingual patient instructions & content library | CP86, CP89, **D-11** | 5 |
| CP92 | Prescription education station (Step 11) | CP42, CP11 | 4 |

### Phase 1 close
| ID | Name | Depends on | Days |
|---|---|---|---|
| CP93 | Performance hardening & load testing | all Phase 1 | 8 |
| CP94 | Security review & external penetration test | CP93, **D-50** | 8 |
| CP95 | **Pilot deployment, training & Phase 1 sign-off** | CP94 | 10 |
| | **Phase 1 subtotal** | | **493** |

## PHASE 2 — MEMORY & RELATIONSHIP (CP96–CP120)

| ID | Name | Depends on | Days |
|---|---|---|---|
| CP96 | Document upload, storage & metadata pipeline | CP34, CP23 | 6 |
| CP97 | Document capture UX (multi-page mobile scan) | CP96, CP11 | 6 |
| CP98 | **OCR bake-off spike — resolves D-16** | CP96 | 10 |
| CP99 | Image preprocessing service (Python) | CP98 | 7 |
| CP100 | Document classification & handwritten exclusion [R-09] | CP99, **D-18** | 6 |
| CP101 | OCR integration (engine selected at CP98) | CP98, CP99 | 8 |
| CP102 | Mixed-script handling & numeral/date normalisation | CP101, **D-17** | 6 |
| CP103 | Table & lab-panel extraction | CP101 | 8 |
| CP104 | Medical entity extraction & analyte dictionary | CP103, **D-20** | 10 |
| CP105 | Confidence scoring & human validation queue UI | CP104, **D-19** | 8 |
| CP106 | Extracted values → observations (provenance-preserving) | CP105, CP42 | 5 |
| CP107 | Chronology engine [R-09] | CP106 | 7 |
| CP108 | Red-line abnormality rule engine [R-10] | CP107, **D-55** | 8 |
| CP109 | Records auto-summary & synthesis integration | CP107, CP71 | 6 |
| CP110 | Records viewer & document timeline UI | CP107, CP74 | 8 |
| CP111 | Follow-up scheduling engine | CP38 | 6 |
| CP112 | Contact preference & checkout capture [R-14] | CP36, CP111 | 4 |
| CP113 | Call task queue & CRM operator UI | CP111, CP112, **D-40** | 8 |
| CP114 | SMS gateway integration & call→SMS fallback | CP113, **D-39** | 8 |
| CP115 | Inbound messaging & triage queue | CP114, **D-41** | 6 |
| CP116 | Communication history & consent enforcement | CP114, CP36 | 5 |
| CP117 | Satisfaction capture & operator matrix | CP116 | 5 |
| CP118 | Pharmacy dispensing UI (RBAC-blinded) | CP84, CP20 | 7 |
| CP119 | Inventory, batches & real-time stock deduction | CP118 | 8 |
| CP120 | **Phase 2 hardening & sign-off** | CP96–CP119 | 8 |
| | **Phase 2 subtotal** | | **174** |

## PHASE 3 — INTELLIGENCE & RESEARCH (CP121–CP143)

| ID | Name | Depends on | Days |
|---|---|---|---|
| CP121 | Research schema & anonymisation ETL [R-15] | CP25, CP36, **D-48** | 10 |
| CP122 | Cohort builder & definition versioning | CP121 | 8 |
| CP123 | Research dashboard framework (saved, refreshable) | CP122 | 8 |
| CP124 | HbA1c trajectory dashboard | CP123 | 5 |
| CP125 | GLP-1 RA safety dashboard (AE %, dose-initiation) | CP123 | 6 |
| CP126 | GLP-1 RA efficacy & weight-response dashboard | CP123 | 5 |
| CP127 | Affordability & persistence dashboard (BDT lens) | CP123, CP75 | 6 |
| CP128 | Exercise–outcome correlation dashboard | CP123, CP60 | 5 |
| CP129 | Multi-benefit + SGLT2i/linagliptin dashboards | CP123 | 8 |
| CP130 | Automated hypothesis engine & positive-deviance mining | CP122, CP70 | 10 |
| CP131 | Research assistant & narrative/book engine | CP130 | 10 |
| CP132 | AI agents: Clinical Assistant & Diagnostic Support | CP70, CP72 | 12 |
| CP133 | AI Medical Scribe (dictation-first STT) | CP70, **D-12** | 12 |
| CP134 | Follow-Up Predictor (ML) | CP111, CP121 | 10 |
| CP135 | Nightly outcome monitoring & QA engine | CP83, CP69 | 8 |
| CP136 | Code Red negative-outcome RCA workflow | CP135 | 7 |
| CP137 | Guideline/CME engine & weekly Clinical Digest | CP70, **D-10, D-25** | 12 |
| CP138 | Biometric attendance & HR core | CP15, **D-57** | 8 |
| CP139 | Workflow friction & station throughput analytics | CP39, CP38 | 7 |
| CP140 | Staff performance, error linkage & retraining loop | CP63, CP139 | 6 |
| CP141 | Micro-costing engine | CP138, CP139 | 8 |
| CP142 | Predictive supply chain & procurement alerts | CP119, CP134 | 8 |
| CP143 | Executive dashboard ("CEO Co-Pilot") | CP139–CP142 | 12 |
| | **Phase 3 subtotal** | | **191** |

## PHASE 4 — ENTERPRISE & COMMUNITY (CP144–CP160)

| ID | Name | Depends on | Days |
|---|---|---|---|
| CP144 | Field app foundation (tablet, offline-heavy) | CP66, CP11 | 10 |
| CP145 | Field screening workflows & battery | CP144, CP42 | 10 |
| CP146 | Instant field report & portable printing | CP145, CP89 | 8 |
| CP147 | Field sync, triage & auto-scheduling | CP145, CP111 | 8 |
| CP148 | Google Maps fleet management & geospatial | CP144 | 8 |
| CP149 | Population health & surveillance dashboards | CP147, CP123 | 8 |
| CP150 | FHIR resource mapping (R4) | CP42, **D-29** | 10 |
| CP151 | FHIR API, Provenance & conformance testing | CP150 | 10 |
| CP152 | External integration & partner onboarding | CP151 | 8 |
| CP153 | Multi-branch / multi-facility support | CP19, CP38 | 12 |
| CP154 | AlloyDB migration & analytics scaling | CP121, **D-31** | 8 |
| CP155 | Backup, DR drills & RPO/RTO validation | CP03, **D-37** | 8 |
| CP156 | Full security assessment & penetration test #2 | CP153 | 8 |
| CP157 | Performance & load testing at target scale | CP153, **D-59** | 8 |
| CP158 | Production infrastructure hardening & cost optimisation | CP155 | 6 |
| CP159 | Runbooks, training materials & operational handover | CP158 | 8 |
| CP160 | **Production deployment & Phase 4 sign-off** | all | 6 |
| | **Phase 4 subtotal** | | **144** |

**Grand total: 1,058 developer-days** (sum of every checkpoint above) before review cycles, rework, content authoring, meetings, or leave. §21 converts this into calendar time under different staffing assumptions.

---

# 16. DETAILED CHECKPOINT SPECIFICATIONS

> **Format note.** Every checkpoint carries all requested fields. Fields that carry no work for that checkpoint are marked `—`; that is information, not an omission. In the compacted Phase 2–4 specs, fields are grouped onto fewer lines to keep each checkpoint readable on one screen. **Acceptance criteria are written to be objectively checkable** — where a number appears that has clinical meaning, it is marked as a *proposed value requiring approval*.

---

## PHASE 0 — FOUNDATION & STABILISATION

---

### CP01 · Repository, Monorepo Scaffolding & CI Skeleton
**Objective:** Establish the DTHCMS repository with its final directory structure, tooling, and a CI pipeline that runs on every commit.
**Why this checkpoint exists:** Everything else commits into this. Retrofitting a monorepo structure and CI after five modules exist is a week of churn; doing it first costs three days. §17.2 of the blueprint also makes establishing the repository an explicit handover action.
**Scope:** Monorepo per §4.5 · workspace tooling (pnpm workspaces for JS, Go modules) · `.editorconfig`, formatters (gofmt/goimports, Prettier), linters (golangci-lint, ESLint) · conventional-commit enforcement · PR template with a Definition-of-Done checklist · CODEOWNERS · branch protection · GitHub Actions workflow running format, lint, build, test on every push · Dependabot/Renovate · secret scanning · `docs/blueprint-v2.0.md` committed with its SHA-256 recorded (Appendix B) · this plan committed as `docs/implementation-plan.md`.
**Out of scope:** Any application code · deployment · cloud infrastructure · Docker images.
**Dependencies:** None. **This is the first checkpoint.**
**Technologies:** Git, GitHub, GitHub Actions, pnpm, Go modules, golangci-lint, ESLint, Prettier, gitleaks.
**Backend:** Empty `backend/` module that compiles. **Database:** — **API:** — **Frontend:** Empty workspace that builds. **Mobile:** Empty workspace that builds. **AI:** — **OCR/NLP:** — **Events/Audit:** — **Security:** Secret scanning + branch protection + signed commits (recommended).
**Testing:** One trivial test per workspace proving the runner works and reports correctly.
**Manual verification:** Clone the repo on a clean machine; run the documented bootstrap command; confirm all workspaces build. Open a PR with a deliberate lint error and confirm CI fails; fix it and confirm CI passes. Commit a fake secret and confirm the scanner blocks it.
**Acceptance criteria:** (1) `git clone` + documented setup command succeeds on a clean machine in under 15 minutes. (2) CI runs on every push and fails on lint, build or test failure. (3) A pushed secret is blocked. (4) `main` cannot be pushed to directly. (5) The blueprint's SHA-256 is recorded in the repo.
**Deliverables:** Repository, CI workflow, contributor README, blueprint + plan in `docs/`.
**Effort:** 3 days. **Risks:** Low. Over-engineering CI at this stage is the only real risk — keep it fast.
**Open decisions:** GitHub organisation (blueprint §17.2 recommends the existing Arrow Health org — confirm). Repository visibility (**must be private**).

---

### CP02 · Architecture Guardrails, ADRs & Coding Standards
**Objective:** Encode the architectural decisions from this plan as enforceable rules and written records.
**Why this checkpoint exists:** §5's modular monolith only stays modular if boundary violations fail the build. Documented-but-unenforced architecture decays within weeks.
**Scope:** ADR directory with the first records (modular monolith; hybrid event sourcing; offline-first; self-hosted auth; Postgres-first) · module boundary rules · **import-graph linter in CI** that fails on cross-module internal imports · error-handling conventions · naming conventions (events, tables, endpoints) · logging conventions including the PHI-redaction rule · code review checklist · the project Definition of Done (§22) committed as `docs/definition-of-done.md`.
**Out of scope:** Application code · the modules themselves.
**Dependencies:** CP01. **Technologies:** `go-arch-lint` or equivalent; ESLint boundary plugin; Markdown ADRs.
**Backend:** Lint configuration and module skeleton directories. **Database/API/Frontend/Mobile/AI/OCR:** — **Security:** Logging redaction convention documented and lint-checked. **Events/Audit:** Event naming convention (`NOUN_VERBPAST`) documented.
**Testing:** A deliberately-violating fixture package that CI must reject.
**Manual verification:** Add an import from `internal/patient` into `internal/prescription/repo.go`; confirm CI fails with a clear message; remove it; confirm CI passes.
**Acceptance criteria:** (1) A cross-module internal import fails CI. (2) At least five ADRs exist and are dated. (3) The Definition of Done is in the repo and linked from the PR template. (4) A logging call that passes a patient name fails the lint rule.
**Database:** — **API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** ADRs, guardrail configuration, standards documents.
**Effort:** 3 days. **Risks:** Rules too strict early can slow legitimate work — allow documented exceptions with an ADR.
**Open decisions:** None blocking.

---

### CP03 · Cloud Project, Environments & IaC Baseline
**Objective:** Create dev, staging and production environments as reproducible infrastructure-as-code.
**Why this checkpoint exists:** Every subsequent checkpoint deploys somewhere. Hand-built environments cannot be restored after an incident, which makes the §15.1 disaster-recovery requirement unachievable.
**Scope:** GCP projects (dev/staging/prod) with separate billing visibility · Terraform for VPC, Cloud SQL, Memorystore, GCS buckets (with data-class separation per D-01), Secret Manager, Artifact Registry, service accounts with least privilege, Cloud Run services (empty), load balancer + Cloud Armor · CMEK key rings · budget alerts · a documented environment matrix.
**Out of scope:** Application deployment (CP12/CP95) · AlloyDB (CP154) · production DNS/TLS for the public verify page (CP85).
**Dependencies:** CP01; **D-01 interim direction on region**.
**Technologies:** Terraform, GCP, Cloud KMS.
**Backend:** — **Database:** Cloud SQL Postgres 16 instances, private IP, automated backups + PITR enabled. **API:** — **Frontend/Mobile/AI/OCR:** — **Security:** Least-privilege service accounts, no default service account usage, CMEK, private networking, Secret Manager, audit logging on. **Events/Audit:** Cloud Audit Logs enabled and exported.
**Testing:** `terraform plan` clean on a fresh state; destroy-and-recreate the dev environment successfully.
**Manual verification:** Destroy and rebuild dev from Terraform; confirm it comes back identically. Confirm production Postgres is not reachable from the public internet. Confirm a budget alert fires on a test threshold.
**Acceptance criteria:** (1) All three environments exist and are defined entirely in Terraform. (2) Dev can be destroyed and recreated from code. (3) No database has a public IP. (4) No secret exists outside Secret Manager. (5) The data-class bucket separation from D-01 exists. (6) Budget alerts are configured.
**Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Terraform modules, environment matrix, cost baseline document.
**Effort:** 5 days. **Risks:** **D-01 may invalidate the region choice** — mitigated by keeping region a Terraform variable and never hardcoding it. Cloud cost surprises — mitigated by budget alerts on day one.
**Open decisions:** **D-01 (region/residency)** · D-30 (compute platform) can be provisionally Cloud Run.

---

### CP04 · Local Development Environment
**Objective:** One command brings up a complete local stack.
**Why this checkpoint exists:** Developer iteration speed compounds across 160 checkpoints; a slow or fragile local setup taxes every one of them.
**Scope:** docker-compose with Postgres 16 (+ required extensions), Redis, MinIO (S3-compatible GCS stand-in), Mailpit, and a mock LLM/OCR service returning canned responses · seed script · Makefile targets (`make up`, `make migrate`, `make seed`, `make test`) · hot reload for Go (`air`) and the web app.
**Out of scope:** Production parity · running the real mobile app against cloud services.
**Dependencies:** CP01. **Technologies:** Docker Compose, MinIO, Mailpit, air.
**Backend/Database:** Local instances with the same extensions as production. **API/Frontend/Mobile:** Local dev servers wired to the local API. **AI/OCR:** Mock services with deterministic canned responses — this also makes CI independent of paid external services. **Security:** Local-only credentials, clearly marked, never reused elsewhere.
**Testing:** CI runs the same compose stack, proving parity between local and CI.
**Manual verification:** On a clean machine, run `make up && make migrate && make seed` and reach a working local API and web app in under 10 minutes.
**Acceptance criteria:** (1) Single-command bring-up documented and working on Windows (the developer's stated environment), macOS and Linux. (2) Local Postgres has identical extensions and version to production. (3) The mock AI/OCR services allow full offline development. (4) `make test` passes locally and in CI identically.
**Backend:** — **Database:** — **API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Events/Audit:** —
**Deliverables:** `docker-compose.yml`, Makefile, seed data, developer README.
**Effort:** 2 days. **Risks:** Windows/Docker friction — verify explicitly on the actual development machine.
**Open decisions:** None.

---

### CP05 · Go Backend Skeleton & Platform Layer
**Objective:** A running Go service with the platform primitives every module will depend on.
**Why this checkpoint exists:** Config, logging, errors, database access and graceful shutdown are cross-cutting; building them once, correctly, before any domain code means twenty modules inherit them rather than each inventing its own.
**Scope:** `cmd/api`, `cmd/worker`, `cmd/realtime`, `cmd/migrate` entry points · typed validated configuration that fails fast · structured logging with correlation IDs · the unified error model (§8.6) · database pool with health checks · Redis client · blobstore interface with GCS and MinIO implementations · clock and ID (UUIDv7) abstractions for testability · graceful shutdown · `/healthz`, `/readyz`, `/version` · middleware chain skeleton (§8.3) with authentication and RBAC as no-op placeholders.
**Out of scope:** Any domain module · authentication logic · the event store.
**Dependencies:** CP02, CP04. **Technologies:** Go 1.23, chi, pgx/v5, slog, coder/websocket.
**Backend:** As above. **Database:** Connection and health only. **API:** Health and version endpoints; error envelope shape defined. **Frontend/Mobile/AI/OCR/Events:** — **Security:** Body-size limits, panic recovery, security headers, no stack traces in responses.
**Testing:** Unit tests for config validation and the error model; an integration test that starts the server and hits `/readyz` against a real database via testcontainers.
**Manual verification:** Start with a missing required env var — the process must exit immediately with a clear message naming the variable. Start correctly and confirm `/readyz` returns 200 and reports database and Redis status. Send SIGTERM during a request and confirm graceful drain.
**Acceptance criteria:** (1) Missing or invalid configuration prevents startup with an actionable message. (2) `/readyz` accurately reflects dependency health (verified by stopping Redis). (3) Every response carries a correlation ID also present in logs. (4) Graceful shutdown completes in-flight requests. (5) No panic escapes to the client.
**Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Events/Audit:** —
**Deliverables:** Running backend skeleton, platform packages, ADR on the error model.
**Effort:** 5 days. **Risks:** Over-abstraction. Keep the platform layer thin; abstractions earn their place by having two implementations.
**Open decisions:** None.

---

### CP06 · Database Foundation & Migration Framework
**Objective:** Migration tooling, schema conventions and the first schemas.
**Why this checkpoint exists:** Every data checkpoint depends on a safe, reviewable, reversible migration path. Schema conventions established late produce an inconsistent database nobody wants to query.
**Scope:** Migration tool wired into `cmd/migrate` and CI · the schema separation from §9.1 (`core`, `ledger`, `read`, `ops`, `docs`, `research`) with distinct database roles and grants · extensions (`pgcrypto`, `pg_trgm`, `btree_gist`) · conventions documented and lint-checked where possible · `facility_id` convention (D-61) · a `facilities` table with DTHC Faridpur seeded · timestamp/audit column conventions · migration test harness that applies all migrations to an empty database and to a restored snapshot.
**Out of scope:** Domain tables · the event store (CP23) · `pgvector` (CP137).
**Dependencies:** CP05. **Technologies:** golang-migrate or goose, sqlc, testcontainers.
**Database:** Schemas, roles, grants, extensions, conventions. **Backend:** sqlc configuration and generation in CI. **API/Frontend/Mobile/AI/OCR/Events:** — **Security:** Separate roles per schema; the application role has **no UPDATE/DELETE grant on `ledger`** and **no write grant on `read`** — established here so it can never be forgotten later. **Audit:** `created_by`/`updated_by` conventions.
**Testing:** Migrate-up from empty; migrate-up on a populated snapshot; a rollback test for reversible migrations; a CI check that generated sqlc code is current.
**Manual verification:** Run migrations on a fresh database and inspect the schema. Attempt an `UPDATE` on a `ledger` table as the application role and confirm permission denied.
**Acceptance criteria:** (1) Migrations apply cleanly to an empty database. (2) The application role cannot UPDATE or DELETE in `ledger` (verified by an explicit failing test). (3) The application role cannot write to `read`. (4) Conventions are documented and followed by every subsequent checkpoint. (5) Uncommitted sqlc output fails CI.
**API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Events/Audit:** —
**Deliverables:** Migration framework, base schemas, roles/grants, conventions document.
**Effort:** 4 days. **Risks:** Grant model too restrictive for legitimate operations — resolve by adding an explicit maintenance role rather than loosening the application role.
**Open decisions:** None.

---

### CP07 · Observability Baseline
**Objective:** Logs, metrics and traces from the first line of domain code, with PHI never leaving the system in a log line.
**Why this checkpoint exists:** Observability retrofitted after an incident is observability that did not exist when it mattered. The PHI-redaction requirement in particular must precede any code that handles patient data.
**Scope:** OpenTelemetry tracing across HTTP, database and Redis · RED metrics per endpoint · **a slog handler that redacts declared-sensitive fields**, with a lint rule and tests · correlation ID propagation into jobs and WebSocket sessions · exporters to Cloud Monitoring/Trace/Logging · initial dashboards (latency, error rate, saturation, database) · alert policies (error rate, latency, database connections, disk).
**Out of scope:** Business dashboards (CP143) · queue-specific monitoring (CP69).
**Dependencies:** CP05. **Technologies:** OpenTelemetry Go, Cloud Monitoring.
**Backend:** Instrumentation and redaction. **Database:** Query-level tracing with parameter values redacted. **Security:** **No PHI in logs, traces, metrics labels or error reports** — with an explicit test asserting that a log call containing a patient name emits a redacted value.
**Testing:** Unit tests for redaction (including nested structures and error wrapping); a trace-propagation integration test.
**Manual verification:** Trigger an error containing a patient name; inspect the exported log and confirm redaction. Follow one request end-to-end in the trace viewer.
**Acceptance criteria:** (1) Every request produces a trace with database spans. (2) A log call with a declared-sensitive field emits `[REDACTED]`, proven by test. (3) Dashboards exist for latency, error rate and saturation. (4) At least four alert policies are configured with a named recipient.
**API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Events/Audit:** —
**Deliverables:** Instrumentation, redaction handler, dashboards, alert policies, on-call notes.
**Effort:** 4 days. **Risks:** Redaction gaps — mitigated by an allow-list approach (log explicit fields) rather than a deny-list.
**Open decisions:** D-35 (final backend for logs/traces) — Cloud Monitoring assumed; OpenTelemetry keeps it swappable.

---

### CP08 · Prototype Assessment & Data-Migration Decision
**Objective:** Determine what, if anything, of the existing prototype (§15.3 Phase 0) is carried forward, and how existing patient data is migrated.
**Why this checkpoint exists:** The blueprint's Phase 0 is "existing prototype: registration, anthropometry, basic entry." **No prototype source or data has been provided, and the connected project folder is empty (D-51).** Until this is resolved, Phase 0 cannot be scoped honestly, and — much more importantly — any real patient data in that prototype is a migration obligation with clinical and legal weight.
**Scope:** Inventory of the prototype (stack, source availability, data volume, data quality, who uses it and how) · assessment of whether any code is reusable (default expectation: **no** — a prototype's value is the learning, not the code) · a data-migration plan if real patient data exists: field mapping, identity reconciliation, duplicate handling, provenance (imported records enter the ledger as `PATIENT_IMPORTED` events attributed to the migration, never presented as if freshly captured) · a written decision record.
**Out of scope:** Executing the migration (a separate checkpoint scheduled after CP29 if needed).
**Dependencies:** **D-51 — requires the prototype or a definitive statement that none exists.**
**Technologies:** Depends entirely on the prototype's stack.
**Backend/Database:** Mapping specification only. **Security:** Any prototype data handled under the same controls as production from the moment it is received. **Events/Audit:** Imported data must be distinguishable from captured data forever.
**Testing:** Data-profiling scripts; a dry-run migration into a scratch database with a reconciliation report.
**Manual verification:** Dr. Nahid reviews a sample of migrated records against the originals and confirms fidelity.
**Acceptance criteria:** (1) A written decision on reuse vs greenfield. (2) If data exists: a documented field mapping, a dry-run migration with a record-count and value-level reconciliation report, and an explicit provenance design. (3) If no prototype exists: a signed note recording that, closing D-51.
**Backend:** — **Database:** — **API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Assessment report, migration plan (or a note that none is required), decision record.
**Effort:** 3 days (assessment only; migration execution is separate and unestimatable until the data is seen).
**Risks:** **Real patient data of unknown quality is the highest-risk unknown in Phase 0.** Migration effort could be anywhere from 2 to 20 days.
**Open decisions:** **D-51 — this checkpoint exists to close it.**

---

### CP09 · Design System Foundation
**Objective:** The token set, typography, and first component layer that every screen in the system will use.
**Why this checkpoint exists:** §12 of the brief requires one coherent, modern design system. If UI work begins before tokens exist, each module invents its own spacing and colour and the result is exactly the "outdated hospital admin application" the brief forbids. Bilingual type must also be validated before any layout is built — Bangla changes line height and truncation behaviour.
**Scope:** `packages/design-tokens` as JSON → CSS variables + NativeWind theme + print variables · type scale validated in **both Bangla and English** · clinical semantic colour palette with contrast and colour-blind verification · spacing/radius/elevation/motion scales · the first primitives (Button, Input, NumericInput, Select, Card, Badge, StatusPill, AlertBanner, Skeleton, EmptyState, ErrorState) · Storybook with light/dark and bn/en switching · accessibility baseline documented.
**Out of scope:** Domain components (built with their features) · the physician dashboard layout (CP73) · print layout (CP89).
**Dependencies:** CP01. **Technologies:** Tailwind v4, Radix, NativeWind, Storybook.
**Frontend:** Token pipeline and primitives. **Mobile:** NativeWind theme consuming the same tokens. **Security:** — **AI/OCR/Events:** —
**Testing:** Visual regression snapshots per component × theme × language; automated accessibility checks (axe) on every story; contrast assertions on the token set.
**Manual verification:** View Storybook in Bangla and English; confirm no truncation or overlap; confirm 48dp touch targets on mobile primitives; test with OS text size at 200%.
**Acceptance criteria:** (1) One token source feeds web, mobile and print. (2) Every primitive renders correctly in Bangla and English. (3) All text meets ≥4.5:1 contrast, verified automatically. (4) Clinical semantic colours are distinguishable in a colour-blindness simulation and are never the sole carrier of meaning. (5) Every primitive has loading, empty, error and disabled states.
**Backend:** — **Database:** — **API:** — **AI:** — **OCR/NLP:** — **Events/Audit:** —
**Deliverables:** Token package, primitive components, Storybook, design system documentation.
**Effort:** 6 days. **Risks:** Bangla font licensing and rendering quality — validate the chosen family on real low-end Android devices, not only in a browser.
**Open decisions:** DTHC brand colour and Bangla typeface — **Dr. Nahid's confirmation** (brand identity).

---

### CP10 · Web Application Shell
**Objective:** A navigable, authenticated-shaped Next.js application with routing, layout, i18n and error handling in place.
**Why this checkpoint exists:** Establishes the frontend architecture from §14.9 before features arrive, so twenty features slot into a known structure.
**Scope:** App Router route groups by audience · AppShell, sidebar, topbar, breadcrumbs · TanStack Query provider with sensible defaults · i18n wiring with bn/en switching and message files · error boundaries and the global error page · loading and offline states · a placeholder login page · a permission-gating component wired to a stub · the feature folder convention with one example feature.
**Out of scope:** Real authentication (CP16) · any clinical screen.
**Dependencies:** CP09. **Technologies:** Next.js 15, React 19, TanStack Query, Zustand, next-intl.
**Frontend:** As above. **API:** Consumes the health endpoint only. **Security:** CSP and security headers configured; no tokens in `localStorage` (decision recorded now, enforced at CP16).
**Testing:** Component tests for the shell; a Playwright smoke test navigating every route group; an i18n test asserting no untranslated keys.
**Manual verification:** Navigate all route groups; switch language and confirm the entire shell changes including dates; force an error and confirm the boundary renders a useful bilingual message.
**Acceptance criteria:** (1) All route groups render with correct layout. (2) Language switching is instant and complete — no untranslated strings, verified by an automated check. (3) An unhandled error shows a friendly bilingual page and reports a correlation ID. (4) Lighthouse performance ≥90 on the shell.
**Backend:** — **Database:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Events/Audit:** —
**Deliverables:** Web shell, routing structure, i18n scaffolding, conventions documentation.
**Effort:** 4 days. **Risks:** Server/Client Component boundary confusion — settle the convention in an ADR now.
**Open decisions:** None.

---

### CP11 · Mobile Application Shell
**Objective:** A running React Native application on real Android hardware with navigation, theming, i18n and secure storage.
**Why this checkpoint exists:** §2's mobile-first principle makes this the primary clinical surface. Device realities (screen sizes, keyboards, performance on low-end Android) must be confronted at the start, not discovered at CP45.
**Scope:** Expo dev-client project · Expo Router navigation shape · NativeWind theme from shared tokens · i18n · secure storage wiring · connectivity detection · a placeholder login and queue screen · **a build running on a representative low-end Android device** · EAS build pipeline · crash reporting configured with PHI scrubbing.
**Out of scope:** Local database (CP64) · authentication (CP16) · any station form.
**Dependencies:** CP09. **Technologies:** Expo, Expo Router, NativeWind, expo-secure-store.
**Mobile:** As above. **Security:** Secure storage only for anything sensitive; `allowBackup=false`; crash reports scrubbed.
**Testing:** Component tests; a Maestro smoke flow on a device/emulator; a startup-performance measurement on the low-end target device.
**Manual verification:** Install on the actual phone model the clinic will use; navigate; switch language; rotate; test with the largest OS font setting; measure cold start.
**Acceptance criteria:** (1) The app installs and runs on the clinic's actual target device. (2) Cold start under a documented threshold on that device (**proposed: ≤3s** — to be confirmed once the device is known). (3) Bangla and English render correctly including at 200% font scale. (4) Nothing sensitive is stored outside secure storage. (5) An EAS build produces an installable artefact from CI.
**Backend:** — **Database:** — **API:** — **Frontend:** — **AI:** — **OCR/NLP:** — **Events/Audit:** —
**Deliverables:** Mobile shell, build pipeline, device compatibility notes.
**Effort:** 5 days. **Risks:** Low-end Android performance is a genuine constraint — **the target device model must be confirmed (D-59/hardware)** before this checkpoint, or the acceptance test is meaningless.
**Open decisions:** Clinic device model and Android version floor — **confirmation required**.

---

### CP12 · API Contract & Generated Clients
**Objective:** OpenAPI 3.1 as the contract of record, with a generated, typed TypeScript client for web and mobile.
**Why this checkpoint exists:** Three surfaces consuming an undocumented API produces drift and integration bugs. Generating clients from a single spec makes drift a build failure instead of a runtime surprise.
**Scope:** OpenAPI spec structure and conventions · the error envelope and standard responses documented · pagination, filtering, idempotency-header and versioning conventions · client generation into `packages/api-client` · **CI check that the spec matches the implemented routes** · a published API documentation page for the team.
**Out of scope:** Real endpoints beyond health.
**Dependencies:** CP05, CP10, CP11. **Technologies:** OpenAPI 3.1, openapi-typescript / orval.
**Backend:** Route-to-spec conformance check. **API:** The spec itself. **Frontend/Mobile:** Generated client wired into TanStack Query.
**Testing:** Spec linting; a contract test asserting every implemented route appears in the spec and vice versa; a generated-client compile check.
**Manual verification:** Add an endpoint without documenting it and confirm CI fails.
**Acceptance criteria:** (1) The spec validates and lints clean. (2) An undocumented route fails CI. (3) The generated client compiles and is used by both web and mobile. (4) Error envelope and idempotency conventions are documented with examples.
**Database:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Security:** No new attack surface; existing controls apply. **Events/Audit:** —
**Deliverables:** OpenAPI spec, generated client package, API conventions document.
**Effort:** 4 days. **Risks:** Spec drift if generation is manual — automate it in CI.
**Open decisions:** API versioning strategy (recommend URL-prefixed `/v1` with additive-only changes within a version).

---

### CP13 · Test Harness, Quality Gates & Synthetic Data Generator
**Objective:** The testing infrastructure every later checkpoint relies on, plus a realistic synthetic dataset.
**Why this checkpoint exists:** §18 of the brief demands a comprehensive testing strategy. It only happens if the harness is cheap to use from the first domain checkpoint. The synthetic data generator additionally unlocks staging, demos, load tests and AI evaluation — and it removes any temptation to copy production data into lower environments.
**Scope:** testcontainers-based integration harness with per-test database isolation · fixture builders · Playwright and Maestro E2E scaffolding · coverage reporting with a floor · CI gates (lint, test, coverage, security scan, migration check, spec conformance) · **a synthetic data generator producing realistic Bangladeshi patients** (names in both scripts, plausible ages/sexes, realistic diabetic/thyroid histories, HbA1c trajectories, prescriptions, documents) at configurable scale.
**Out of scope:** Load testing (CP93) · AI evaluation sets (CP72).
**Dependencies:** CP06, CP10, CP11. **Technologies:** testcontainers-go, Vitest, Playwright, Maestro, k6 (scaffold only).
**Backend/Database:** Harness and generator. **Frontend/Mobile:** E2E scaffolding.
**Security:** The generator produces **no real personal data**; a CI check forbids production dumps in lower environments.
**Testing:** The harness tests itself: a sample integration test proving database isolation between parallel tests.
**Manual verification:** Generate 1,000 synthetic patients; browse them in the web app; confirm names, ages and clinical values look plausible to a clinician's eye (Dr. Nahid should glance at this — implausible synthetic data produces misleading demos and bad load tests).
**Acceptance criteria:** (1) Integration tests run against real Postgres and Redis with per-test isolation. (2) The full CI gate runs in under 10 minutes. (3) The generator produces N patients with realistic bilingual names and clinically coherent histories. (4) Coverage reporting is enforced with an agreed floor.
**Backend:** — **Database:** — **API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Events/Audit:** —
**Deliverables:** Test harness, CI gates, synthetic data generator, testing guide.
**Effort:** 6 days. **Risks:** Slow CI kills the feedback loop — parallelise and cache aggressively from the start.
**Open decisions:** Coverage floor (**proposed: 70% overall, 90% on clinical calculation and safety-rule packages** — requires confirmation).

---

### CP14 · Phase 0 Review & Architecture Sign-Off
**Objective:** Formal review of the foundation with Dr. Nahid before clinical work begins.
**Why this checkpoint exists:** Phase acceptance in §15.3 requires sign-off. This is also the natural moment to confirm the 🔴 open decisions that gate Phase 1.
**Scope:** Walkthrough of the foundation · demonstration of CI, environments, design system and shells · review of ADRs · **structured review of every 🔴 open decision from §3**, with decisions recorded · updated risk register · confirmation of the Phase 1 checkpoint sequence.
**Out of scope:** Any implementation.
**Dependencies:** CP01–CP13.
**Manual verification:** Dr. Nahid sees a working (empty) system on a phone and on the web, in Bangla and English.
**Acceptance criteria:** (1) All Phase 0 checkpoints meet the Definition of Done. (2) Every 🔴 open decision is either resolved and recorded, or explicitly deferred with a named date and owner. (3) Written sign-off to proceed to Phase 1.
**Technologies:** — (review checkpoint). **Backend:** — **Database:** — **API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Security:** Security posture of the foundation reviewed. **Events/Audit:** — **Testing:** Verification that every Phase 0 checkpoint’s tests pass in CI, and that the CI gate itself is trustworthy.
**Deliverables:** Review record, updated decision register, signed phase gate.
**Effort:** 2 days. **Risks:** Unresolved 🔴 decisions cascade into Phase 1 stalls — this is the meeting where that is prevented.
**Open decisions:** All 🔴 items reviewed here.

---

## PHASE 1 — CLINIC CORE (MVP)

*From here the format tightens: same fields, fewer words. Fields marked `—` are not applicable.*

---

### CP15 · User, Role & Permission Data Model
**Objective:** The identity schema that every attributed write depends on.
**Why:** [R-03] requires `user_id` on every event; [R-02] requires multiple concurrent roles per user. The model must support both from the first migration.
**Scope:** `users`, `roles`, `permissions`, `role_permissions`, `user_roles` (with grant/revoke history) · the 18 seeded roles from §6.3 · permission catalogue covering every station and administrative action · user lifecycle (invited → active → suspended → deactivated, **never deleted**).
**Out of scope:** Authentication (CP16) · enforcement (CP19/CP20) · admin UI (CP21).
**Dependencies:** CP06. **Technologies:** Postgres, sqlc.
**Backend:** Repository + domain types. **Database:** Migrations + seed. **API:** — **Frontend/Mobile:** — **AI/OCR:** — **Security:** Password hash column present but unused; deactivation preserves attribution. **Events/Audit:** Role grant/revoke written to the security audit log (CP22).
**Testing:** Migration test; seed idempotency; a test asserting a user with three roles resolves the union of permissions correctly.
**Manual verification:** Inspect the seeded roles and permission catalogue against §4.4's named constraints (nutritionist has no prescription permission; pharmacist has no diagnosis permission).
**Acceptance criteria:** (1) All 18 roles seeded with permissions. (2) A user can hold multiple roles simultaneously. (3) Users cannot be hard-deleted (foreign keys and policy prevent it). (4) Permission catalogue covers every action named in §4.4.
**Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Migrations, seeds, domain model, permission catalogue document.
**Effort:** 4 days. **Risks:** An incomplete permission catalogue causes churn later — derive it from the station list systematically.
**Open decisions:** Final role list confirmation with Dr. Nahid (staffing reality may differ from the blueprint's 12 stations).

---

### CP16 · Password Authentication & Session Management
**Objective:** Users log in and hold a secure, revocable session.
**Why:** Nothing can be attributed before there is an authenticated actor. Every subsequent checkpoint depends on this.
**Scope:** argon2id hashing · login endpoint with progressive delay · access token (10–15 min) + opaque rotating refresh token with reuse detection · server-side session registry in Postgres with a Redis hot cache · logout and logout-all-devices · administrator-mediated password reset · session listing.
**Out of scope:** 2FA (CP17) · device binding (CP18) · RBAC (CP19).
**Dependencies:** CP15. **Technologies:** argon2id, JWT (access only), Redis.
**Backend:** Auth module + middleware activation. **Database:** `sessions`, `refresh_tokens`, `login_attempts`. **API:** `POST /auth/login`, `/auth/refresh`, `/auth/logout`, `GET /auth/me`. **Frontend:** Real login page, token handling in memory + httpOnly cookie for refresh. **Mobile:** Login screen, tokens in secure storage. **Security:** No tokens in `localStorage`; timing-safe comparison; generic failure messages; refresh reuse revokes the family. **Events/Audit:** `LOGIN_SUCCEEDED`, `LOGIN_FAILED`, `LOGOUT`, `SESSION_REVOKED` to the audit log.
**Testing:** Unit tests on hashing and token logic; integration tests for the full flow; a refresh-reuse test asserting family revocation; brute-force delay test.
**Manual verification:** Log in on web and mobile; let the access token expire and confirm a seamless refresh; revoke the session from another device and confirm immediate lockout on the next request.
**Acceptance criteria:** (1) Valid credentials produce a working session; invalid ones produce an indistinguishable error. (2) A reused refresh token revokes all sessions in that family. (3) Session revocation takes effect within one request. (4) No token is persisted in web `localStorage` (verified by inspection and an automated check). (5) Failed logins are rate-limited with progressive delay.
**AI:** — **OCR/NLP:** —
**Deliverables:** Auth module, login UIs, session management, security notes.
**Effort:** 6 days. **Risks:** Auth bugs are severe — this checkpoint gets an explicit security review before sign-off.
**Open decisions:** D-43 (confirm self-implemented approach), D-44 (token lifetimes).

---

### CP17 · TOTP 2FA & Step-Up Authentication
**Objective:** Second factor at enrolment and for privileged actions.
**Why:** §15.1 mandates 2FA; §13.1 explains why it must not be SMS-based (offline requirement).
**Scope:** TOTP secret generation, QR provisioning, verification, drift window · encrypted secret storage · single-use recovery codes · **step-up authentication** middleware for privileged actions · enforcement policy per role (mandatory for physician, admin, pharmacist, researcher).
**Out of scope:** WebAuthn · SMS OTP (explicitly not recommended).
**Dependencies:** CP16. **Technologies:** `pquerna/otp`, KMS-encrypted secrets.
**Backend:** 2FA module + step-up middleware. **Database:** `user_totp`, `recovery_codes`. **API:** enrol/verify/disable endpoints; `X-Step-Up-Token` handling. **Frontend/Mobile:** Enrolment flow with QR, verification screen, recovery code display-once. **Security:** Secrets encrypted at rest with a KMS key; recovery codes hashed; step-up token short-lived and single-purpose. **Events/Audit:** 2FA enrolment, disablement, step-up success/failure audited.
**Testing:** TOTP verification including clock drift; recovery code single use; a test asserting a privileged endpoint rejects a request without a valid step-up token.
**Manual verification:** Enrol with a real authenticator app; log in with the code; attempt a privileged action and confirm the step-up prompt; use a recovery code and confirm it cannot be reused.
**Acceptance criteria:** (1) TOTP enrolment works with standard authenticator apps. (2) Privileged endpoints are unreachable without step-up, proven by test. (3) Recovery codes work once. (4) 2FA secrets are encrypted at rest.
**Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** 2FA module, enrolment UI, step-up middleware, staff instructions in Bangla and English.
**Effort:** 4 days. **Risks:** Staff losing phones — the administrator reset path must be documented and rehearsed before pilot.
**Open decisions:** D-45 (which roles require mandatory 2FA vs device trust).

---

### CP18 · Device Enrolment, Credentials & Attribution
**Objective:** A trustworthy `device_id` on every clinical write [R-03].
**Why:** Attribution is only as good as the device identity. A self-declared identifier is not evidence.
**Scope:** Admin device enrolment · server-issued keypair, private key in Android Keystore · request signing or channel binding · device status lifecycle (active/suspended/revoked/lost) · device metadata (model, OS, app version, last seen) · **rule: unenrolled devices cannot write clinical events** · revocation with immediate effect and quarantine of queued events from revoked devices.
**Out of scope:** Play Integrity attestation (later, optional) · biometric attendance (CP138).
**Dependencies:** CP16. **Technologies:** Ed25519, Android Keystore, expo-secure-store.
**Backend:** Device module + verification middleware. **Database:** `devices`, `device_keys`, `device_events`. **API:** enrol/list/revoke; every request carries device proof. **Frontend:** Device management screen (admin). **Mobile:** Enrolment flow, key generation, request signing. **Security:** Private key never leaves the device; revocation immediate. **Events/Audit:** Enrolment and revocation audited; `device_id` becomes a mandatory envelope field.
**Testing:** Enrolment flow; forged device header rejected; revoked device rejected; key rotation.
**Manual verification:** Enrol a phone; write a value and confirm the device appears in attribution; revoke the device and confirm the next write fails with a clear message.
**Acceptance criteria:** (1) Clinical writes from unenrolled or revoked devices are rejected. (2) A forged `device_id` fails signature verification. (3) Every device appears in the admin console with last-seen and app version. (4) Revocation is effective on the next request.
**AI:** — **OCR/NLP:** —
**Deliverables:** Device module, mobile enrolment, admin device screen.
**Effort:** 6 days. **Risks:** Key loss on app reinstall requires re-enrolment — document the operational process for the clinic.
**Open decisions:** D-46 (attestation depth).

---

### CP19 · RBAC Policy Engine
**Objective:** A single authoritative `Can(user, action, resource)` decision function.
**Why:** §4.4's constraints must be evaluated in one place; scattered permission checks are how clinical systems leak data.
**Scope:** Policy evaluation with deny-by-default · resource scoping (own station / own patient today / any) · active-role resolution [R-02] · permission caching with correct invalidation on role change · decision explainability (`why was this denied`) for debugging and audit.
**Out of scope:** Enforcement wiring (CP20) · UI gating (CP21).
**Dependencies:** CP15. **Technologies:** Go, Redis cache.
**Backend:** `rbac` module. **Database:** Reads role/permission tables. **API:** — **Security:** Deny by default; explicit deny beats allow; role change invalidates cache within a bounded, tested window. **Events/Audit:** Denials logged with reason at debug level, aggregated for the security dashboard.
**Testing:** An exhaustive **decision matrix test**: every role × every permission × representative resources, asserted against a table derived from §4.4. This table becomes the living specification of the access model.
**Manual verification:** Run the matrix report and read it against the blueprint's named constraints.
**Acceptance criteria:** (1) Nutritionist denied on prescription read. (2) Pharmacist denied on diagnosis read. (3) Registration denied on sensitive diagnosis read. (4) Unknown action denied. (5) Role revocation takes effect within the documented cache window (**proposed: ≤30s**, requires confirmation).
**Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** RBAC engine, decision matrix test, access model document.
**Effort:** 5 days. **Risks:** Cache staleness on revocation — bounded and tested explicitly.
**Open decisions:** Cache window value.

---

### CP20 · RBAC Enforcement (Endpoint · Service · Serialiser)
**Objective:** Make the policy engine actually govern every response, including field-level redaction.
**Why:** §4.4 requires the pharmacist to not see diagnoses. Blocking an endpoint is not enough — the fields must not be in the payload.
**Scope:** Middleware enforcement per route · service-layer resource-ownership checks · **serialisation-layer field visibility rules** driven by permissions · a default-deny route registry (a route with no declared permission fails at startup, not at runtime) · standard 403 error semantics that do not leak existence.
**Out of scope:** Break-glass (CP22).
**Dependencies:** CP19. **Technologies:** Go middleware, struct-tag-driven serialisation.
**Backend:** Enforcement layers. **API:** Every route declares its required permission. **Frontend/Mobile:** Handle 403 consistently. **Security:** Three-layer enforcement; startup fails on undeclared routes. **Events/Audit:** Denials recorded.
**Testing:** Every endpoint tested with an unauthorised role; **a golden test asserting the exact JSON a pharmacist receives for a prescription contains no diagnosis key**; a startup test proving an undeclared route aborts boot.
**Manual verification:** Log in as a pharmacist and inspect the raw network response for a prescription — confirm the diagnosis field is absent, not merely hidden in the UI.
**Acceptance criteria:** (1) Every route declares a permission or the service refuses to start. (2) Field-level redaction verified by golden tests for pharmacist and registration roles. (3) 403 responses do not reveal whether the resource exists. (4) No endpoint bypasses the middleware (proven by a route-registry audit test).
**Database:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Enforcement layers, redaction rules, route registry audit.
**Effort:** 5 days. **Risks:** Field redaction missed on new endpoints — mitigated by making redaction declarative and default-restrictive.
**Open decisions:** Which diagnoses count as "sensitive" for the registration blinding rule (**clinical confirmation required**).

---

### CP21 · Admin Console: Users, Roles, Devices
**Objective:** Dr. Nahid or an administrator can manage staff and devices without a developer.
**Why:** Operational independence. A clinic that needs a developer to add a staff member cannot run.
**Scope:** User CRUD (invite, activate, suspend, deactivate) · role assignment with effective-permission preview · device list, enrolment approval, revocation · session listing and forced logout · password reset · a searchable audit view of administrative actions.
**Out of scope:** HR features (CP138) · clinical configuration (CP55, CP75).
**Dependencies:** CP20, CP10. **Technologies:** Next.js, TanStack Query/Table.
**Backend:** Admin endpoints. **API:** Admin CRUD. **Frontend:** Admin console. **Security:** All admin actions require step-up 2FA; every action audited. **Events/Audit:** Full audit trail with before/after values.
**Testing:** Component and E2E tests; an authorisation test proving non-admins cannot reach any admin route.
**Manual verification:** Create a user, assign two roles, log in as them on a phone, verify the permissions match the preview, then revoke a role and confirm the change takes effect.
**Acceptance criteria:** (1) A new staff member can be created and productive without developer involvement. (2) Effective permissions preview matches actual behaviour. (3) Every administrative action is audited with actor and timestamp. (4) Admin routes require step-up authentication.
**Database:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Admin console, admin API, operations guide.
**Effort:** 6 days. **Risks:** Low.
**Open decisions:** None.

---

### CP22 · Security Audit Log & Human-Readable Trail
**Objective:** The append-only non-clinical audit log, plus the rendering layer that turns events into readable sentences (§4.5).
**Why:** §4.5 asks for "10:42 — JD_04 changed systolic BP 140 → 145". That is a presentation of events, and it needs a home before clinical events exist.
**Scope:** `audit_events` table (append-only, hash-chained) · logins, permission changes, exports, prints, break-glass, config changes · **break-glass access with mandatory justification and immediate administrator notification** · the bilingual event-sentence template registry · an audit viewer with filters (user, date, patient, action) · signed PDF export of an audit trail.
**Out of scope:** Clinical event ledger (CP23) — separate by design.
**Dependencies:** CP16, CP20. **Technologies:** Postgres, template registry.
**Backend:** Audit module + renderer. **Database:** `audit_events` with append-only grants. **API:** Audit query endpoints (admin only). **Frontend:** Audit viewer. **Security:** Append-only enforced by grants; break-glass notification. **Events/Audit:** This is the checkpoint.
**Testing:** Append-only enforcement test; hash-chain verification test; renderer tests for both languages; a test asserting break-glass without justification is rejected.
**Manual verification:** Perform a role change, an export and a break-glass access; read all three in the audit viewer in Bangla and English; export a signed PDF.
**Acceptance criteria:** (1) Audit rows cannot be updated or deleted by the application role. (2) Every audited action renders as a correct human sentence in both languages. (3) Break-glass requires a typed justification and notifies an administrator within one minute. (4) The audit export PDF verifies against its signature.
**Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Audit module, viewer, sentence templates, export.
**Effort:** 4 days. **Risks:** Audit volume growth — partition from the start.
**Open decisions:** Audit retention period (D-05).

---

### CP23 · Event Store Schema, Append Path & Hash Chain
**Objective:** The single clinical write path (§5.4) and the immutable ledger behind it.
**Why:** This is the architectural heart. §4.1 makes the event log the source of truth; §4.5 makes it the audit trail; §12 makes it the research substrate. Every clinical checkpoint after this depends on it.
**Scope:** `events` table per §7.4, monthly partitions · append-only enforced by grants **and** rules · per-aggregate sequence assignment with gapless guarantee under concurrency · hash chaining per aggregate + a daily global anchor · the `Append(ctx, envelope)` API · event registry with payload schemas and versioning · `aggregate_snapshots` table (created, unused) · chain verification job.
**Out of scope:** Projections (CP25) · realtime publication (CP26) · idempotency layer (CP24).
**Dependencies:** CP06. **Technologies:** Postgres partitioning, SHA-256, advisory locks or sequence tables.
**Backend:** `eventstore` module. **Database:** Partitioned table, grants, rules, indexes from §7.4. **API:** Internal only at this stage. **Security:** No UPDATE/DELETE grant; hash chain. **Events/Audit:** The whole checkpoint.
**Testing:** Concurrency test — 100 goroutines appending to one aggregate produce a gapless sequence with no duplicates · append-only enforcement test · hash-chain verification including a deliberate tamper that must be detected · partition rotation test · 10,000-event insert performance benchmark.
**Manual verification:** Append events; attempt `UPDATE events SET payload=...` as the application role and confirm refusal; run the chain verifier and confirm success; tamper via a superuser and confirm the verifier detects it.
**Acceptance criteria:** (1) Sequences are gapless per aggregate under concurrent load, proven by test. (2) The application role cannot mutate or delete events. (3) The verifier detects any tampering. (4) Append latency p95 under a documented budget (**proposed: ≤50ms** at expected load). (5) Every event carries the full attribution envelope; an append missing any envelope field is rejected.
**Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Event store module, schema, verification job, event registry, ADR.
**Effort:** 8 days. **Risks:** **Highest-consequence checkpoint in Phase 1.** Sequence assignment under concurrency is subtle — allocate review time and test hard.
**Open decisions:** None (design settled in §7).

---

### CP24 · Write Envelope Contract & Idempotency
**Objective:** Make the universal write envelope [R-03] structurally impossible to bypass, and make every write safely retryable.
**Why:** §15.2 names the envelope as a universal contract; §13 requires offline retries to be safe.
**Scope:** Envelope type constructed only from an authenticated request context (user, device, active role, station, facility) · compile-time impossibility of hand-constructing an envelope outside the auth path · `event_id` uniqueness · HTTP `Idempotency-Key` middleware with a response cache table · job-level idempotency keys · client-side `event_id` generation contract documented for mobile.
**Out of scope:** The sync protocol (CP65).
**Dependencies:** CP23, CP18. **Technologies:** Go, Postgres.
**Backend:** Envelope constructor + idempotency middleware. **Database:** `idempotency_records` with TTL cleanup. **API:** `Idempotency-Key` documented on every mutating endpoint. **Mobile:** UUIDv7 generation utility. **Security:** Envelope cannot be spoofed from the client — role and device come from the verified session, not the request body. **Events/Audit:** Envelope completeness enforced at append.
**Testing:** Duplicate submission returns the original response, not an error · concurrent duplicate submissions produce exactly one event · a test proving the envelope cannot be constructed with a client-supplied `user_id`.
**Manual verification:** Submit the same measurement twice from the mobile app (kill the network mid-request and retry) and confirm exactly one value appears with one attribution.
**Acceptance criteria:** (1) Duplicate `event_id` never creates two events, under concurrency. (2) Retried requests with the same idempotency key return the identical response body. (3) Client-supplied identity fields are ignored. (4) An event missing any envelope field cannot be appended.
**Frontend:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Envelope contract, idempotency middleware, client guidance.
**Effort:** 5 days. **Risks:** Idempotency table growth — TTL and cleanup job included.
**Open decisions:** None.

---

### CP25 · Projection Framework, Checkpointing & Replay
**Objective:** Turn events into queryable read models, rebuildably.
**Why:** §4.1's "the event log is the source of truth" is only sustainable if the derived state can be rebuilt and proven correct.
**Scope:** Projection interface and registry · synchronous in-transaction projections for clinically critical reads · asynchronous projections with `projection_checkpoint` tracking and a lag metric · full rebuild command · **replay equivalence test** · projection versioning (a schema change triggers a rebuild) · dead-letter handling for projection failures.
**Out of scope:** Specific projections (built with their features) · research ETL (CP121).
**Dependencies:** CP23. **Technologies:** Go, Postgres, River.
**Backend:** Projection framework. **Database:** `read` schema, checkpoint table, projection-role grants. **API:** — **Security:** Only the projection role writes to `read`. **Events/Audit:** Rebuild operations audited.
**Testing:** **The critical test: replay all events from scratch and assert the rebuilt read models are byte-identical to the incrementally-built ones on a fixture dataset.** Plus idempotency (applying the same event twice changes nothing), out-of-order tolerance, and failure/dead-letter tests.
**Manual verification:** Run a full rebuild on the synthetic dataset; compare a checksum of every read table before and after; confirm identical.
**Acceptance criteria:** (1) Replay equivalence proven by an automated test that runs in CI on every change. (2) Projection lag is exposed as a metric with an alert. (3) A rebuild is a documented, single-command operation. (4) A failing projection does not block event appends.
**Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Projection framework, rebuild tooling, replay test, runbook.
**Effort:** 8 days. **Risks:** Rebuild time at scale — measure early; snapshots exist as the escape hatch if needed.
**Open decisions:** None.

---

### CP26 · Realtime Gateway (WebSocket + Redis)
**Objective:** Instant cross-station updates (§4.1: "the junior doctor's screen updates instantly — no refresh").
**Why:** This is a named blueprint requirement and a visible differentiator; it also underpins the traffic board (CP40).
**Scope:** WebSocket gateway with authenticated handshake (token + device) · subscription topics (`patient:{id}`, `station:{id}`, `queue:{facility}`, `user:{id}`) · **RBAC-filtered fan-out — a subscriber receives only what their role may see** · Redis pub/sub bridging across instances · heartbeat, reconnection with resume cursor, backpressure handling · connection metrics.
**Out of scope:** Client integration (CP27) · presence indicators (optional, later).
**Dependencies:** CP23, CP20. **Technologies:** coder/websocket, Redis pub/sub.
**Backend:** `realtime` module + `cmd/realtime`. **API:** `WSS /realtime`. **Security:** Auth on connect and re-auth on token refresh; per-message RBAC filtering; connection limits per user/device. **Events/Audit:** Publication occurs after commit only.
**Testing:** Multi-client fan-out test; RBAC filtering test (a nutritionist's socket must never receive a prescription event); reconnection with cursor resume; 200 concurrent connections; message ordering per topic.
**Manual verification:** Open the physician dashboard and a mobile station app side by side; enter a value on the phone and watch it appear on the dashboard **without refresh**, in under a second.
**Acceptance criteria:** (1) An update appears on subscribed clients in <1s at expected load. (2) A subscriber never receives data their role cannot read, proven by test. (3) Reconnection resumes without data loss and without duplicates. (4) A dropped WebSocket never causes data loss (the client reconciles by pull).
**Database:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Realtime gateway, subscription model, load test results.
**Effort:** 7 days. **Risks:** Cloud Run WebSocket behaviour — validate early; D-30 contingency is a dedicated instance group.
**Open decisions:** D-30 (hosting for the gateway).

---

### CP27 · Client Realtime Integration
**Objective:** Web and mobile consume realtime updates coherently.
**Why:** A push channel without a disciplined client integration produces stale-cache bugs that are hard to reproduce.
**Scope:** WebSocket client with reconnect/backoff · **messages invalidate TanStack Query keys rather than mutating the cache directly** · subscription lifecycle tied to the visible screen · a connection status indicator · mobile handling of background/foreground transitions.
**Out of scope:** Optimistic UI for offline (CP66).
**Dependencies:** CP26, CP10, CP11. **Technologies:** TanStack Query, native WebSocket.
**Frontend/Mobile:** Realtime hooks and status indicator. **Security:** Token refresh mid-connection handled without dropping data.
**Testing:** Reconnect-after-network-loss test; invalidation correctness test; a background/foreground transition test on device.
**Manual verification:** Disconnect Wi-Fi for 30 seconds with the dashboard open; reconnect; confirm the view catches up correctly with no duplicates and no stale values.
**Acceptance criteria:** (1) Reconnection is automatic with exponential backoff. (2) Data missed while disconnected is recovered on reconnect. (3) Connection status is visible to the user. (4) No duplicate rows appear after reconnection.
**Backend:** — **Database:** — **API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Events/Audit:** —
**Deliverables:** Realtime client hooks, status UI.
**Effort:** 5 days. **Risks:** Subtle cache bugs — the invalidate-don't-mutate rule is the mitigation and is enforced in review.
**Open decisions:** None.

---

### CP28 · Patient Domain Model & Migration
**Objective:** The patient schema, including the validated DOB and the Research ID relationship.
**Why:** [R-06] makes exact DOB clinically load-bearing (pediatric percentiles); §12 makes the Research ID an identity concern from Step 1, not an afterthought.
**Scope:** `patients` table with bilingual names, **validated exact DOB with a `dob_precision` and `dob_verified_by` field**, sex, contacts, address, socio-economic baseline fields, emergency contact, photo reference, status, `facility_id` · `patient_identifiers` (hashed + masked) · `research_subjects` with the link table in a separately-governed schema · human-readable `clinical_id` generator.
**Out of scope:** Registration API (CP29) · duplicate detection (CP30) · UI.
**Dependencies:** CP06, CP23. **Technologies:** Postgres, pg_trgm.
**Backend:** Domain types + repository. **Database:** Migrations + indexes for search and duplicate detection. **Security:** NID stored as salted hash + masked display by default (D-47); identifiers classed `IDENTIFIER` for encryption and residency. **Events/Audit:** `PATIENT_REGISTERED` event schema defined.
**Testing:** Migration test; `clinical_id` uniqueness under concurrency; DOB validation rules (no future dates, no implausible ages, precision handling).
**Manual verification:** Inspect the schema; confirm a research ID cannot be derived from a clinical ID by inspection.
**Acceptance criteria:** (1) DOB is mandatory and validated. (2) `clinical_id` is unique under concurrent creation. (3) `research_id` is opaque and not derivable from `clinical_id`. (4) Socio-economic baseline fields required by §12 cohorting exist.
**API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Schema, domain model, ID generators.
**Effort:** 4 days. **Risks:** Socio-economic field list is a research design decision — confirm with Dr. Nahid before finalising, as changing it later invalidates cohort comparability.
**Open decisions:** Socio-economic baseline field list (**clinical/research confirmation**); D-47 (store NID or hash only).

---

### CP29 · Patient Registration API & Research ID
**Objective:** Create a patient through the event path, with a Research ID assigned atomically.
**Why:** Step 1 of the journey; everything downstream needs a patient.
**Scope:** `POST /patients` emitting `PATIENT_REGISTERED` · atomic Research ID assignment · server-side validation (DOB, phone format per §3 Step 1, required fields) · projection to the `patients` read model · duplicate check hook (implemented in CP30) · registration of the initial consent record reference.
**Out of scope:** OCR of the NID · photo capture (CP34) · UI.
**Dependencies:** CP28, CP24. **Technologies:** Go, Postgres.
**Backend:** Patient module. **API:** `POST /patients`, `GET /patients/{id}`. **Security:** Requires `patient.create`; full envelope attribution. **Events/Audit:** `PATIENT_REGISTERED` with complete demographics in the payload.
**Testing:** Creation flow; validation failures with specific messages; idempotent re-submission; a test asserting the projection matches the event payload exactly.
**Manual verification:** Register a patient via the API; confirm the event, the projection and the Research ID all exist and are consistent; re-submit the same request and confirm no duplicate.
**Acceptance criteria:** (1) A patient is created with one event and one projection row. (2) Research ID is assigned in the same transaction. (3) Invalid DOB or phone is rejected with a field-specific bilingual message. (4) Re-submission with the same `event_id` creates nothing new.
**Database:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Registration API, event schema, projection.
**Effort:** 5 days. **Risks:** Phone validation rules for Bangladesh — confirm accepted formats (operator prefixes, +880 handling).
**Open decisions:** Phone format rules; required vs optional field list (**confirmation**).

---

### CP30 · Duplicate Detection Engine
**Objective:** Make §3 Step 1's "strict duplicate-record prevention" real.
**Why:** Duplicate patients destroy longitudinal history, corrupt research cohorts, and are the single most common data-quality failure in clinic systems. They are also very hard to fix after the fact.
**Scope:** Deterministic matching (exact NID hash, exact phone + DOB) · probabilistic matching (trigram similarity on bilingual names + DOB proximity + phone edit distance + address) · scored candidate list with a **blocking threshold** and a **review threshold** · a duplicate review UI showing side-by-side records · merge workflow emitting `PATIENT_MERGED` with full reversibility of the decision trail · handling of legitimately similar patients (twins, common names — Bangladeshi naming patterns produce many true near-matches, so the UI must make *not merging* easy).
**Out of scope:** Automatic merging (**never automatic** — a wrong merge is worse than a duplicate).
**Dependencies:** CP29. **Technologies:** `pg_trgm`, phonetic matching adapted for Bengali transliteration.
**Backend:** Matching engine. **Database:** Match indexes; `patient_merge_records`. **API:** `POST /patients/check-duplicates`, merge endpoints. **Frontend:** Duplicate review and merge screens. **Security:** Merge requires elevated permission + step-up. **Events/Audit:** Merge is an event; the merged record remains queryable and redirects to the survivor.
**Testing:** A labelled fixture set of true duplicates and true distinct-but-similar patients, with precision/recall measured; merge correctness (all events from both records resolve to the survivor); merge audit completeness.
**Manual verification:** Attempt to register an existing patient with a slightly different name spelling and confirm the system surfaces the match before creation. Merge two records and confirm the full combined history is visible and attributions are preserved.
**Acceptance criteria:** (1) Registering an exact NID/phone+DOB match is blocked with the existing record shown. (2) Near-matches are surfaced for review before creation. (3) Merge preserves every event from both records with original attribution. (4) Merges are never automatic. (5) Precision and recall measured on the labelled set and reported (**thresholds are proposed values requiring approval**).
**Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Matching engine, review UI, merge workflow, evaluation report.
**Effort:** 6 days. **Risks:** Over-aggressive matching frustrates registration staff during a busy clinic; under-aggressive creates duplicates. Tune with real data during pilot.
**Open decisions:** Matching thresholds (**require approval after measurement**).

---

### CP31 · Patient Search & Retrieval API
**Objective:** Find a patient in under a second, by any plausible handle.
**Why:** Used dozens of times per hour by every station. Slow search is the fastest way to lose staff goodwill.
**Scope:** Search by name (Bangla or English, partial, transliteration-tolerant), phone, clinical ID, NID · fuzzy ranking · pagination · today's-patients fast path · RBAC-filtered results · a patient summary endpoint assembling the header card.
**Out of scope:** Full timeline (CP37).
**Dependencies:** CP28. **Technologies:** `pg_trgm`, materialised search columns.
**Backend:** Search module with a normalised search column. **Database:** GIN trigram indexes. **API:** `GET /patients?q=`, `GET /patients/{id}/summary`. **Security:** Results respect RBAC; searches are audited (bulk-search patterns are a data-exfiltration signal).
**Testing:** Search relevance tests on the synthetic dataset in both scripts; performance test at 50,000 patients; RBAC filtering test.
**Manual verification:** Search a Bangla name typed in English transliteration and confirm the patient is found. Time ten searches.
**Acceptance criteria:** (1) p95 search latency <300ms at 50,000 patients. (2) Bangla and English name searches both work, including partial input. (3) Results are RBAC-filtered. (4) Searches are audited.
**Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Events/Audit:** —
**Deliverables:** Search API, indexes, performance benchmark.
**Effort:** 4 days. **Risks:** Transliteration coverage — collect real spelling variants during pilot and tune.
**Open decisions:** None.

---

### CP32 · Registration UI (Web)
**Objective:** The registration desk workflow on a larger screen.
**Why:** Registration involves more typing than other stations; a keyboard is genuinely faster here, so web is the primary surface for Step 1 (with mobile as CP33 for flexibility).
**Scope:** Registration form with sectioned layout · **DOB entry designed against error** (three-field or calendar with age echo — "42 years, 3 months" shown live so a wrong year is obvious) · inline duplicate warnings as the user types · socio-economic section · emergency contact · consent capture entry point · print/label of the clinical ID · post-registration routing to the queue.
**Out of scope:** Photo (CP34) · NID OCR.
**Dependencies:** CP29, CP10. **Technologies:** React Hook Form, Zod.
**Frontend:** Registration screens. **API:** Consumes CP29/CP30. **Security:** RBAC-gated. **Events/Audit:** Attribution automatic.
**Testing:** Form validation tests; E2E registration flow; duplicate-warning interaction test; accessibility audit.
**Manual verification:** Register five synthetic patients and time the flow. Deliberately enter a wrong birth year and confirm the live age echo makes it obvious.
**Acceptance criteria:** (1) A complete registration takes under **90 seconds** for an experienced operator (*proposed target, to be validated with real staff*). (2) DOB errors are visually obvious before submission. (3) Duplicate warnings appear before creation, not after. (4) All validation messages are bilingual and field-specific. (5) Keyboard-only completion is possible.
**Backend:** — **Database:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Registration UI, validation schemas, timing measurement.
**Effort:** 6 days. **Risks:** Form length causes drop-off — split into "essential now / complete later" with a clear completion indicator.
**Open decisions:** Which fields are mandatory at first contact vs completable later (**operational confirmation**).

---

### CP33 · Registration on Mobile
**Objective:** Registration from a phone when the desk is busy or during outreach.
**Why:** [R-01] mobile-first, and [R-02] role flexibility — but registration's typing load makes this a secondary surface, deliberately.
**Scope:** Mobile registration flow, step-by-step (one section per screen) · large touch targets · the same validation and duplicate checks · resume-in-progress registration.
**Out of scope:** Offline registration (deferred to CP66 with the provisional-record design from §13.7).
**Dependencies:** CP29, CP11.
**Mobile:** Registration screens. **API:** Same endpoints. **Security:** Same RBAC and attribution.
**Testing:** Device tests; validation parity tests with web (shared Zod schemas).
**Manual verification:** Register a patient on a phone one-handed; confirm parity of the resulting record with a web registration.
**Acceptance criteria:** (1) A patient registered on mobile is indistinguishable in the record from one registered on web, except for `device_id`. (2) Validation rules are identical (shared schemas, proven by test). (3) Interrupted registration can be resumed without data loss.
**Technologies:** Per the §4 stack; no new dependencies. **Backend:** — **Database:** — **Frontend:** — **AI:** — **OCR/NLP:** — **Events/Audit:** —
**Deliverables:** Mobile registration flow.
**Effort:** 5 days. **Risks:** Typing burden on mobile — mitigate with pickers, defaults and the step-by-step layout.
**Open decisions:** None.

---

### CP34 · Patient Photo Capture & Object Storage
**Objective:** Capture and store the patient photo securely, with the blobstore path proven end to end.
**Why:** §3 Step 1 requires photo/biometrics; this is also the first real use of object storage, so it establishes the pattern for documents (CP96).
**Scope:** Mobile camera capture with framing guide · client-side resize/compress · signed-URL upload direct to storage · CMEK-encrypted bucket in the `IDENTIFIER` data class · thumbnail generation · display with signed short-TTL URLs · replace-photo flow as an event.
**Out of scope:** Facial recognition · biometric matching (out of scope for the whole system unless Dr. Nahid requests it, and it would need its own legal review under D-01).
**Dependencies:** CP33, CP03. **Technologies:** GCS signed URLs, expo-image-manipulator.
**Backend:** Blob module + signed URL issuance. **Database:** Blob metadata. **API:** Upload-URL and attach endpoints. **Mobile/Frontend:** Capture and display. **Security:** **No public object access ever**; short-TTL signed URLs; photos never in device gallery or OS backup; access audited. **Events/Audit:** `PATIENT_PHOTO_CAPTURED`.
**Testing:** Upload/download round trip; signed URL expiry test; a test asserting objects are not publicly readable; image size/format limits.
**Manual verification:** Capture a photo, view it on the dashboard, copy the URL and confirm it stops working after expiry.
**Acceptance criteria:** (1) Photos are never publicly accessible. (2) Signed URLs expire within the configured TTL. (3) Photos are encrypted with CMEK. (4) Photo capture works on the target device in poor light with usable results.
**Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Blob pipeline, capture UI, storage policy.
**Effort:** 4 days. **Risks:** Photo storage growth — set size limits and lifecycle rules now.
**Open decisions:** Photo retention (D-05); whether photos leave the country (D-01).

---

### CP35 · Patient Edit & Demographic Correction
**Objective:** Correct demographic data through the event path, never by overwrite.
**Why:** §4.3's correction principle applies to demographics too — a wrong DOB changes every pediatric percentile ever computed for that patient.
**Scope:** Edit flow emitting `PATIENT_DEMOGRAPHICS_CORRECTED` with previous and new values and a reason · **DOB changes flagged as high-impact and requiring elevated permission**, with automatic recomputation and re-versioning of every affected derived value · demographic change history view.
**Out of scope:** Clinical value correction (CP62) — same pattern, different domain.
**Dependencies:** CP29, CP23.
**Backend:** Correction handler + recomputation trigger. **API:** `PATCH /patients/{id}` (event-emitting). **Frontend/Mobile:** Edit UI with reason capture. **Security:** DOB and identifier changes require elevated permission. **Events/Audit:** Full previous/new capture; history view.
**Testing:** Correction event structure; derived-value recomputation on DOB change; permission enforcement; history rendering.
**Manual verification:** Change a paediatric patient's DOB and confirm every percentile recomputes and the old values remain visible in history with their original computation date.
**Acceptance criteria:** (1) Original values remain retrievable forever. (2) Every correction records who, when and why. (3) A DOB change recomputes all dependent derived values and versions them. (4) Demographic history is viewable per patient.
**Technologies:** Per the §4 stack; no new dependencies. **Database:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Correction flow, recomputation logic, history view.
**Effort:** 4 days. **Risks:** Recomputation cascade scope — enumerate dependent values explicitly rather than relying on memory.
**Open decisions:** Which fields require elevated permission (**confirmation**).

---

### CP36 · Consent Capture & Enforcement
**Objective:** Layered, versioned consent that actually gates behaviour.
**Why:** §15.1 requires explicit consent tracking; §11.2 requires call/SMS consent; §12 requires research consent. D-02 makes this a legal decision point.
**Scope:** Consent template management (versioned, bilingual) · capture flow with signature or thumbprint on device, witnessing operator recorded · consent types: CARE, COMMUNICATION, RESEARCH, AI_PROCESSING, OUTREACH · revocation flow · **enforcement points**: the communication module checks consent before every send; the research ETL filters on it; the AI gateway checks it before processing · consent status visible on the patient header.
**Out of scope:** The legal wording of the templates (**Dr. Nahid + counsel**).
**Dependencies:** CP29. **Technologies:** Signature capture, versioned content.
**Backend:** Consent module + enforcement hooks. **Database:** `consents`, `consent_templates`. **API:** Grant/revoke/status. **Frontend/Mobile:** Capture with signature pad; status display. **Security:** Consent evidence stored as an immutable blob; revocation immediate. **Events/Audit:** `CONSENT_GRANTED`/`CONSENT_REVOKED`.
**Testing:** Consent gating tests (a revoked communication consent must block an SMS send, proven at the module boundary); template versioning; revocation propagation timing.
**Manual verification:** Revoke communication consent and confirm the patient disappears from the outreach list and that a manual send attempt is blocked with a clear reason.
**Acceptance criteria:** (1) Consent is captured with template version, language, evidence and witness. (2) Revocation blocks the relevant behaviour within one minute. (3) Each consent type is independently grantable and revocable. (4) Consent status is visible wherever it affects an action.
**Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Consent module, capture UI, enforcement hooks.
**Effort:** 6 days. **Risks:** **Template wording is a legal dependency (D-02)** — build the engine now, load the wording when approved.
**Open decisions:** D-02 (model, wording, minors, withdrawal semantics).

---

### CP37 · Patient Timeline Read Model v1
**Objective:** One chronological read model merging everything known about a patient.
**Why:** Feeds the physician dashboard (CP73), the timeline visualisation (CP74), the AI synthesis (CP71) and later the records chronology (CP107). Building it once, well, avoids four divergent queries.
**Scope:** A `read.patient_timeline` projection merging observations, diagnoses, medications, visits, documents, communications and alerts into a uniform shape (`when`, `what`, `value`, `source`, `attribution`, `flags`) · efficient range queries · incremental updates · **attribution carried on every row** so hover-to-see-who works everywhere (§8).
**Out of scope:** Visualisation (CP74) · OCR-derived entries (CP106).
**Dependencies:** CP25, CP31.
**Backend:** Timeline projection. **Database:** Indexed read table. **API:** `GET /patients/{id}/timeline?from=&to=&types=`. **Security:** RBAC-filtered per row type. **Events/Audit:** Rebuildable from the ledger.
**Testing:** Projection correctness against a fixture patient; replay equivalence; performance with 10 years of history; RBAC row filtering.
**Manual verification:** Load a synthetic patient with a decade of history and confirm the timeline is complete, correctly ordered, and attributed.
**Acceptance criteria:** (1) Every clinical fact appears exactly once in the timeline. (2) p95 query latency <300ms for a 10-year patient. (3) Every row carries attribution. (4) Rebuild reproduces it identically.
**Technologies:** Per the §4 stack; no new dependencies. **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Timeline projection, query API, performance report.
**Effort:** 5 days. **Risks:** Shape churn as new types are added — design the row schema for extensibility now.
**Open decisions:** None.

---

### CP38 · Visit & Encounter Lifecycle
**Objective:** Model the patient's journey through the clinic as first-class data.
**Why:** §11.1's visit memory, §14.2's throughput analytics, and every station's context all depend on this. Encounters are what make bottleneck analysis free later.
**Scope:** `VISIT_OPENED` → station encounters → `VISIT_CLOSED` · visit types (new, follow-up, outreach referral) · chief complaint capture · **encounter records with start/end per station touch, attributed** · visit status machine with legal transitions · reopening rules · next-review-interval capture at close.
**Out of scope:** Queue mechanics (CP39) · QA gate (CP83).
**Dependencies:** CP28, CP23.
**Backend:** Visit module. **Database:** `visits`, `encounters`. **API:** Visit CRUD + station enter/exit. **Frontend/Mobile:** Visit context in every station screen. **Security:** RBAC per action. **Events/Audit:** All transitions are events.
**Testing:** State machine tests including illegal transitions; encounter timing accuracy; concurrent station entry.
**Manual verification:** Walk a synthetic patient through five stations; confirm every encounter has correct timing and attribution; close the visit and confirm the summary.
**Acceptance criteria:** (1) Illegal state transitions are rejected. (2) Every station touch produces an encounter with accurate start/end and attribution. (3) A visit records chief complaint, diagnoses, plan and next review interval at close (§11.1). (4) A closed visit cannot be silently modified.
**Technologies:** Per the §4 stack; no new dependencies. **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Visit module, state machine, encounter tracking.
**Effort:** 6 days. **Risks:** Real clinic flow may not match the modelled state machine — validate with Dr. Nahid using a real day's patients before finalising.
**Open decisions:** Visit reopening policy (**operational confirmation**).

---

### CP39 · Station Queue & Assignment
**Objective:** Know where every patient is, and what is next.
**Why:** Prerequisite for the traffic board (§5.2), the counseling gate (§5.5), and throughput analytics (§14.2).
**Scope:** Queue entries per station with state machine · configurable station sequence per visit type · priority (critical findings jump the queue per §4.4) · call-next, skip, reroute with reason · waiting-time computation · queue events published to realtime.
**Out of scope:** The board UI (CP40).
**Dependencies:** CP38.
**Backend:** Queue module. **Database:** `queue_entries` with partial indexes. **API:** Queue query and transition endpoints. **Mobile:** "My station's queue" screen. **Security:** Station-scoped RBAC. **Events/Audit:** Queue transitions are events; reroutes carry reasons.
**Testing:** Concurrent call-next (two operators must not get the same patient); priority ordering; waiting-time accuracy.
**Manual verification:** Two operators call next simultaneously on the same station; confirm exactly one gets the patient and the other gets a clear message.
**Acceptance criteria:** (1) No patient is ever assigned to two operators at the same station. (2) Priority patients appear first. (3) Waiting times are accurate to the second. (4) Reroutes require a reason and are attributed.
**Technologies:** Per the §4 stack; no new dependencies. **Frontend:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Queue module, station queue screen.
**Effort:** 6 days. **Risks:** Race conditions — use database-level locking, tested under concurrency.
**Open decisions:** Default station sequences per visit type (**operational confirmation**).

---

### CP40 · Clinic Traffic Control Board
**Objective:** §5.2's live board showing every waiting patient's status, with dynamic rerouting.
**Why:** Named blueprint requirement; the operational nerve centre of a 12-station clinic and the thing that makes the parallel model visible to staff.
**Scope:** Large-screen board (wall display) showing patients by station with waiting time, counseling tick status and flags · realtime updates · **bottleneck highlighting** when a station's queue or wait exceeds a threshold · suggested reroutes with one-tap application · a compact mobile view for floor supervisors.
**Out of scope:** Predictive routing (Phase 3) · throughput analytics (CP139).
**Dependencies:** CP39, CP26.
**Frontend:** Board UI optimised for a wall display (large type, high contrast, glanceable). **API:** Board snapshot + realtime deltas. **Security:** Board shows minimum necessary information — **no diagnoses on a public-facing screen**; patient identification by initials or ID depending on the display's visibility to patients. **Events/Audit:** Reroutes attributed.
**Testing:** Realtime update correctness; board performance with 50 waiting patients; a privacy test asserting no sensitive field reaches the board payload.
**Manual verification:** Run the board on the clinic's actual display; walk patients through stations and confirm live updates; verify from a patient's seat that nothing sensitive is legible.
**Acceptance criteria:** (1) Board updates within 2s of a station event. (2) No clinical diagnosis appears on the board. (3) Bottlenecks are visually obvious at 5 metres. (4) A reroute applied from the board takes effect immediately and is attributed.
**Technologies:** Per the §4 stack; no new dependencies. **Backend:** — **Database:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Traffic board, reroute workflow, display setup notes.
**Effort:** 7 days. **Risks:** **Privacy on a public screen is a real risk** — settle the identification convention with Dr. Nahid before build.
**Open decisions:** Patient identification convention on the public board (**confirmation required**); bottleneck thresholds (**proposed values requiring approval**).

---

### CP41 · In-Session Role Switching on Device
**Objective:** [R-02] — one operator, multiple stations, one phone, no logout.
**Why:** Explicitly named in the blueprint as a staffing reality: "the same assistant enters BP, then switches to anthropometry entry, from the same phone."
**Scope:** Role switcher in the mobile header · **the active role changes the header colour and the available forms** · the active role at write time is stamped on every event · switching requires no re-authentication but is logged · a "you are currently acting as X" indicator that cannot be missed.
**Out of scope:** Simultaneous multi-role writes (a single write always has exactly one role).
**Dependencies:** CP19, CP18.
**Backend:** Active-role resolution in the session and envelope. **API:** Role-switch endpoint issuing an updated token. **Mobile:** Switcher UI. **Security:** Only granted roles are switchable; switching is audited. **Events/Audit:** `actor.role` reflects the active role, verified by test.
**Testing:** Write under role A, switch, write under role B; assert the two events carry different roles. Attempt to switch to an ungranted role and confirm rejection.
**Manual verification:** As one operator, record a BP, switch to anthropometry, record a height; open the audit trail and confirm the two entries show different stations and roles for the same user and device.
**Acceptance criteria:** (1) Switching takes ≤2 taps and no re-authentication. (2) Every event carries the role active at write time. (3) The active role is unmistakably visible on screen. (4) Switching to an ungranted role is impossible.
**Technologies:** Per the §4 stack; no new dependencies. **Database:** — **Frontend:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Role switching, visual indicators.
**Effort:** 3 days. **Risks:** Operator confusion about the current role — the colour-coded header is the mitigation and should be validated with real staff.
**Open decisions:** None.

---

### CP42 · Observation Model & Units Framework
**Objective:** One uniform way to record every measured clinical value, with units handled correctly.
**Why:** Ten stations record values. Ten bespoke tables would make the timeline, research extracts and FHIR mapping ten times harder. Unit errors are a classic patient-safety failure and must be structurally prevented.
**Scope:** `observations` read model + observation event family · category discriminator (ANTHRO, VITAL, EXAM, LAB, DERIVED, SCREENING, PRO) · code registry (internal + LOINC where applicable) · **canonical SI storage with unit metadata and validated conversion** · value types (numeric, text, boolean, coded, structured) · effective time vs record time · source (STATION/OCR/FIELD/DEVICE/PATIENT) · status lifecycle (ACTIVE/CORRECTED/SUPERSEDED).
**Out of scope:** Station-specific capture UIs · derived calculations (CP43).
**Dependencies:** CP23, CP38.
**Backend:** Observation module + unit library. **Database:** `read.observations`, `core.observation_codes`, `core.units`. **API:** Generic observation write and query. **Security:** RBAC per category (nutritionist writes diet-related, not vitals). **Events/Audit:** Every observation is an event.
**Testing:** Unit conversion round-trip tests with known values; category-based RBAC; a test asserting a value without a unit is rejected for unit-bearing codes.
**Manual verification:** Record values in non-canonical units (lb, inches) and confirm storage in canonical units with correct display back in the entered unit.
**Acceptance criteria:** (1) A unit-bearing observation cannot be stored without a valid unit. (2) Conversions round-trip without precision loss beyond a documented tolerance. (3) All seven categories are supported. (4) Every observation carries source and attribution.
**Technologies:** Per the §4 stack; no new dependencies. **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Observation module, unit library, code registry.
**Effort:** 6 days. **Risks:** Over-generalisation making the model unusable — validate against five real station forms before finalising.
**Open decisions:** Initial observation code list (**clinical confirmation**).

---

### CP43 · Clinical Calculation Library (+ Go↔TS Parity)
**Objective:** One correct implementation of every derived clinical value, proven identical on server and client.
**Why:** P-4 requires instant client-side computation; the server must be authoritative. Two implementations that disagree is a patient-safety bug, so parity is enforced by test.
**Scope:** BMI · BMR (formula choice documented) · ideal body weight · **WHO/Asian obesity classification** (§3 Step 2) · waist-hip ratio · **eGFR by CKD-EPI 2021** (§6.4) and bedside Schwartz for paediatrics (pending D-23) · pack-years · body surface area · **every function versioned, with the version stored on each derived value** · shared fixture file of input→expected pairs consumed by both Go and TypeScript test suites.
**Out of scope:** Growth percentiles (CP47) · risk scores (CP58).
**Dependencies:** CP42. **Technologies:** Go + TypeScript, shared JSON fixtures.
**Backend:** `clinical-calc` Go package. **Frontend/Mobile:** `packages/clinical-calc` TS package. **Database:** `derived_values` with formula version. **Security:** — **Events/Audit:** Derived values are events with their inputs recorded.
**Testing:** **Reference test vectors from published sources** for every formula (a CKD-EPI implementation must reproduce published worked examples exactly); **parity test asserting Go and TS produce identical results on every fixture**; boundary and invalid-input tests.
**Manual verification:** Compute eGFR by hand for three patients using the published equation and compare with the system output digit for digit. Dr. Nahid should verify at least the eGFR and obesity-class outputs personally.
**Acceptance criteria:** (1) Every formula matches published reference values exactly. (2) Go and TS agree on 100% of fixtures, enforced in CI. (3) Every derived value stores its formula version. (4) Invalid inputs return an explicit "cannot compute" rather than a wrong number.
**API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Both libraries, fixture set, formula documentation with sources.
**Effort:** 5 days. **Risks:** **Formula-selection errors are clinical errors.** Every formula's source is cited in the code and reviewed by Dr. Nahid.
**Open decisions:** BMR formula (Mifflin-St Jeor vs Harris-Benedict — **clinical confirmation**); paediatric eGFR (D-23).

---

### CP44 · Dual-Unit Display Components [R-08]
**Objective:** Every clinical value shows the clinical unit with the patient-familiar equivalent beneath.
**Why:** A named non-negotiable principle (§2). Implementing it as a shared component means it cannot be forgotten screen by screen.
**Scope:** `<DualUnitValue>` for web and mobile · cm↔ft/in, kg↔lb, and the conversion set the clinic actually uses · a consistent visual treatment (primary value prominent, secondary beneath in muted type) · configurable per-value-type · used in print (CP89) too.
**Out of scope:** Per-user unit preferences (the blueprint specifies both, always).
**Dependencies:** CP43, CP09.
**Frontend/Mobile:** Components. **Backend:** — **Security:** —
**Testing:** Conversion correctness; rendering snapshots in both languages; a lint rule or review checklist item that raw clinical values are not rendered directly.
**Manual verification:** Check height, weight and waist displays across every screen and on the printed prescription.
**Acceptance criteria:** (1) Height shows cm with ft/in beneath everywhere it appears. (2) Weight shows kg with lb beneath. (3) Rounding is consistent and documented. (4) The component is used on every surface including print.
**Technologies:** Per the §4 stack; no new dependencies. **Database:** — **API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Events/Audit:** —
**Deliverables:** Dual-unit components, usage guidance.
**Effort:** 3 days. **Risks:** Low. **Open decisions:** Which value types get dual display beyond height/weight (**confirmation**).

---

### CP45 · Anthropometry Capture (Mobile) [R-01]
**Objective:** Station 2 fully working on a phone, with instant derived values.
**Why:** The first complete clinical station — it proves the whole vertical slice (mobile → event → projection → realtime → dashboard) end to end.
**Scope:** Height, weight, waist, hip, body fat %, muscle mass · large numeric inputs with unit selectors · **instant BMI, BMR, ideal weight, obesity class displayed as the operator types** (P-4) · dual units (P-6) · previous-value comparison with delta · save with attribution · realtime publication.
**Out of scope:** Paediatric percentiles (CP47) · plausibility rejection (CP46, immediately next).
**Dependencies:** CP42, CP43, CP11.
**Mobile:** Anthropometry screens. **API:** Observation writes. **Backend:** Derived value computation on write. **Events/Audit:** `HEIGHT_RECORDED`, `WEIGHT_RECORDED`, `BMI_DERIVED` etc. **Security:** RBAC-gated to the anthropometry permission.
**Testing:** Device tests; derived value correctness; realtime propagation test; the full vertical slice as an E2E test.
**Manual verification:** With a real phone, record height and weight for a patient; watch BMI appear instantly with its class; confirm the physician's dashboard updates without refresh.
**Acceptance criteria:** (1) Derived values appear within 200ms of the last input, computed locally. (2) Server-computed values match client-computed values exactly. (3) The full entry takes under **30 seconds** (*proposed target*). (4) All values appear on the dashboard in under 1s. (5) Every value carries full attribution.
**Technologies:** Per the §4 stack; no new dependencies. **Database:** — **Frontend:** Captured values surface on the physician dashboard (CP73). **AI:** — **OCR/NLP:** —
**Deliverables:** Anthropometry station app, derived value display.
**Effort:** 6 days. **Risks:** Bioimpedance device integration is not specified — assume manual entry unless Dr. Nahid confirms a device with an accessible interface.
**Open decisions:** Bioimpedance device model and whether it can be integrated (**confirmation**).

---

### CP46 · Plausibility Validation & Impossible-Input Rejection
**Objective:** §3 Step 2's checkpoint: "rejection of impossible inputs (BMI 8, negative deltas)".
**Why:** The cheapest possible defence against data-quality failure, applied at the point of entry while the patient is still present and can be re-measured.
**Scope:** Per-code plausibility bands (absolute limits and age/sex-adjusted soft limits) · **hard rejection outside absolute limits; soft warning requiring explicit confirmation inside the implausible-but-possible band** · delta checks against the patient's own history (a 15cm height gain in an adult is rejected; a 3kg weekly weight loss warns) · bilingual, specific error messages · every soft-limit confirmation recorded as an event so the pattern is auditable.
**Out of scope:** Critical clinical values (CP50) — a different concept: implausible vs dangerous.
**Dependencies:** CP45.
**Backend:** Validation rules applied server-side (authoritative) and client-side (immediate). **Database:** `core.plausibility_rules`. **Mobile/Frontend:** Inline validation. **Events/Audit:** Confirmed soft-limit entries recorded with the confirmation.
**Testing:** Boundary tests per rule; delta-check tests; a test asserting server-side rejection even if the client is bypassed.
**Manual verification:** Enter height 15cm (must be rejected), height 195cm for an adult (must be accepted), and a height 12cm different from the last visit (must warn and require confirmation).
**Acceptance criteria:** (1) Absolute-limit violations cannot be saved by any client. (2) Soft-limit values require explicit confirmation, which is recorded. (3) Messages state the plausible range, not just "invalid". (4) Rules are data, editable without a code release.
**Technologies:** Per the §4 stack; no new dependencies. **API:** Uses the generic observation API from CP42. **Frontend:** Captured values surface on the physician dashboard (CP73); no dedicated web screen. **Mobile:** — **AI:** — **OCR/NLP:** — **Security:** No new attack surface; existing controls apply.
**Deliverables:** Validation engine, rule set, admin editing.
**Effort:** 4 days. **Risks:** Over-strict rules block legitimate extreme values — soft limits with confirmation solve this; the band values need clinical review.
**Open decisions:** **Plausibility bands are proposed values requiring Dr. Nahid's approval.**

---

### CP47 · Pediatric Growth Percentile Engine
**Objective:** [R-06] — height/weight/BMI percentiles and z-scores from exact age.
**Why:** A named hard requirement, and the reason DOB validation is mandatory at registration. Also the checkpoint most exposed to an unresolved clinical decision.
**Scope:** Reference data tables (LMS parameters) for the standard chosen in **D-21** · exact age in days/months from validated DOB · height-for-age, weight-for-age, BMI-for-age percentiles and z-scores, sex-specific · **the reference standard and version stored with every computed value** · growth-velocity calculation across visits · handling of age boundaries and out-of-range ages.
**Out of scope:** The percentile card UI (CP48) · head circumference and other paediatric measures unless requested.
**Dependencies:** CP43, **D-21 (blocking)**.
**Backend:** Percentile engine with embedded reference tables. **Database:** Reference tables + derived values with standard version. **API:** Percentile computation on observation write. **Security:** — **Events/Audit:** `PERCENTILE_DERIVED` with the standard version in the payload.
**Testing:** **Validation against published reference tables** — a set of known age/sex/measurement → percentile triples from the official source, matched exactly; boundary tests at the age-band transition; z-score/percentile consistency.
**Manual verification:** Dr. Nahid checks ten paediatric cases against the printed reference charts he currently uses. **This checkpoint is not complete without his verification.**
**Acceptance criteria:** (1) Computed percentiles match the official reference tables exactly on the validation set. (2) The standard and version are stored with every value. (3) Age is computed exactly (in days) from the validated DOB. (4) Out-of-range ages produce an explicit "not applicable" rather than an extrapolated number. (5) Dr. Nahid has verified a sample against his reference charts.
**Technologies:** Per the §4 stack; no new dependencies. **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Percentile engine, reference data, validation report.
**Effort:** 8 days. **Risks:** **Blocked entirely by D-21.** Also: reference data licensing and format — WHO and CDC both publish machine-readable LMS tables; their terms of use must be checked.
**Open decisions:** **D-21 — must be resolved before this checkpoint starts.**

---

### CP48 · Pediatric Percentile Card & Childhood Obesity Flag
**Objective:** Make paediatric growth instantly legible, with the ≥95th-percentile obesity flag [R-06].
**Why:** §8's snapshot panel requires a "pediatric percentile card where applicable"; the flag is a named requirement.
**Scope:** Percentile card component (current percentiles, z-scores, growth curve with the patient's trajectory plotted against reference curves) · **childhood obesity flag at the threshold set by D-21** with clear visual treatment · display on mobile after anthropometry entry and on the physician dashboard · plotted growth chart rendered for print.
**Out of scope:** Full growth-chart printing pack (can follow if Dr. Nahid wants a parent handout).
**Dependencies:** CP47.
**Frontend/Mobile:** Percentile card + growth curve chart. **Security:** — **Events/Audit:** Flag raising recorded.
**Testing:** Visual regression on the curve; flag threshold tests; rendering in both languages.
**Manual verification:** Enter a child's measurements on the phone; confirm the card appears immediately with percentiles matching CP47's engine, and that an obese child is unmistakably flagged.
**Acceptance criteria:** (1) The card appears immediately after entry for patients under the paediatric age cut-off. (2) The obesity flag triggers at exactly the agreed threshold. (3) The growth curve plots the patient's history against reference curves correctly. (4) The card renders correctly in Bangla.
**Technologies:** Per the §4 stack; no new dependencies. **Backend:** — **Database:** — **API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Percentile card, growth chart component.
**Effort:** 4 days. **Risks:** Chart readability on a phone — design for the small screen first.
**Open decisions:** Paediatric age cut-off (**clinical confirmation**); threshold per D-21.

---

### CP49 · Vitals Capture (Mobile)
**Objective:** Station 5's vitals, entered at the point of measurement.
**Why:** [R-01]; also the input to critical-value alerting (CP50).
**Scope:** BP (systolic/diastolic, arm, position), pulse, respiratory rate, temperature, SpO₂ · rapid-entry layout optimised for the sequence a clinician actually measures in · repeat-measurement support (BP measured twice is normal practice) · previous-value comparison · instant flagging of out-of-range values.
**Out of scope:** Alert escalation (CP50) · examination findings (CP51).
**Dependencies:** CP42, CP11.
**Mobile:** Vitals screens. **Backend:** Observation writes + range evaluation. **Events/Audit:** `BP_RECORDED`, `PULSE_RECORDED`, `SPO2_RECORDED` etc.
**Testing:** Device tests; multi-measurement handling; range evaluation.
**Manual verification:** Record a full vitals set in under 30 seconds on a phone while looking mostly at the patient (the heads-up requirement from §15.2).
**Acceptance criteria:** (1) A full vitals set takes under **30 seconds** (*proposed*). (2) Repeat measurements are stored as distinct observations, both retained. (3) Out-of-range values are visually flagged immediately. (4) Entry works one-handed.
**Technologies:** Per the §4 stack; no new dependencies. **Database:** Uses the CP42 observation tables; no new schema. **API:** Uses the generic observation write and query API from CP42. **Frontend:** Captured values surface on the physician dashboard (CP73). **AI:** — **OCR/NLP:** — **Security:** No new attack surface; existing controls apply.
**Deliverables:** Vitals station app.
**Effort:** 5 days. **Risks:** Layout must match real measurement order — sit with the clinical assistant and watch before designing.
**Open decisions:** Normal ranges per age band (**clinical confirmation**, overlaps D-27).

---

### CP50 · Critical Value Alerts & Escalation
**Objective:** §3 Step 5 — "immediate critical-value alerts (SpO₂ < 92%, BP > 180/110) with visual and audible warning", and §4.4's rule that critical findings bypass the queue.
**Why:** This is the system's most direct patient-safety feature.
**Scope:** Critical-value rule table (D-27) including paediatric age bands · **on-device visual + audible alert at the point of entry** · immediate push to the Chief Consultant's dashboard, bypassing the queue · acknowledge-or-escalate with a timeout · escalation chain · alert history per patient · **fail-safe: if the alert cannot be delivered, the entering operator is told to escalate verbally**.
**Out of scope:** Alert fatigue tuning (revisit after pilot data).
**Dependencies:** CP49, CP26, **D-27**.
**Backend:** Rule evaluation on write + escalation job. **Database:** `vital_alerts`, rule table. **API:** Acknowledge endpoint. **Mobile:** Audible + visual alert; cannot be dismissed without acknowledgement. **Frontend:** Priority alert surface on the physician dashboard. **Security:** Alerts respect RBAC but critical alerts always reach the consultant. **Events/Audit:** `CRITICAL_VALUE_ALERTED`, `_ACKNOWLEDGED`, `_ESCALATED`.
**Testing:** Rule evaluation for every threshold including paediatric bands; escalation timeout; delivery-failure path; an end-to-end test from mobile entry to consultant dashboard.
**Manual verification:** Enter SpO₂ 88% on a phone; confirm the audible alert sounds and the consultant's screen shows it within seconds; leave it unacknowledged and confirm escalation fires.
**Acceptance criteria:** (1) Named thresholds trigger visual **and** audible alerts at entry. (2) The consultant sees the alert within 5 seconds. (3) Unacknowledged alerts escalate within the configured window. (4) Delivery failure produces an explicit instruction to escalate verbally. (5) Every alert and acknowledgement is audited.
**Technologies:** Per the §4 stack; no new dependencies. **AI:** — **OCR/NLP:** —
**Deliverables:** Alert engine, escalation workflow, alert UI.
**Effort:** 6 days. **Risks:** Audible alerts in a busy clinic may be missed or ignored — validate the sound design on the floor. Alert fatigue if thresholds are too tight.
**Open decisions:** **D-27 — full critical-value table and escalation chain (blocking).**

---

### CP51 · Clinical Examination Capture
**Objective:** Station 5's structured examination: diabetic foot, neuropathy, retinopathy screening, cardiovascular signs.
**Why:** Named blueprint data; also feeds red-line rules and research (§12).
**Scope:** Diabetic foot examination (monofilament sites with a foot diagram, pulses, deformity, ulceration, callus) · neuropathy screening (vibration, ankle reflex, symptoms score) · retinopathy screening status and findings · cardiovascular examination · **structured, coded findings — not free text** · history-driven prompts (§3 Step 5: "history-driven exam prompts") · laterality handled explicitly.
**Out of scope:** Retinal image capture and grading (a separate future capability).
**Dependencies:** CP42, CP11.
**Mobile:** Examination screens with a tappable foot diagram. **Backend:** Structured observation storage. **Events/Audit:** `FOOT_EXAM_RECORDED` etc.
**Testing:** Structured capture round-trip; laterality correctness; prompt logic.
**Manual verification:** Dr. Nahid or a junior doctor performs a full diabetic foot exam using the app and confirms nothing clinically necessary is missing and nothing superfluous is demanded.
**Acceptance criteria:** (1) A complete diabetic foot exam is capturable in under **2 minutes** (*proposed*). (2) All findings are coded and queryable for research. (3) Laterality is explicit on every relevant finding. (4) History-driven prompts appear (e.g. a diabetic with prior ulcer prompts for ulcer status).
**Technologies:** Per the §4 stack; no new dependencies. **Database:** Uses the CP42 observation tables; no new schema. **API:** Uses the generic observation write and query API from CP42. **Frontend:** Captured values surface on the physician dashboard (CP73). **AI:** — **OCR/NLP:** — **Security:** No new attack surface; existing controls apply.
**Deliverables:** Examination station app, finding code set.
**Effort:** 6 days. **Risks:** Getting the clinical content right — **the finding set must be authored by Dr. Nahid**, not derived from generic templates.
**Open decisions:** Examination finding set and prompt rules (**clinical, content**).

---

### CP52 · Terminology Service (ICD + Internal Dictionary)
**Objective:** Coded diagnoses and complaints, without a licensing blocker.
**Why:** §8 and §9.1 require ICD-coded diagnoses; §3 Step 4 mentions SNOMED, which carries licensing questions (D-24).
**Scope:** ICD code set loaded and searchable (version per D-24) · internal DTHC concept dictionary for chief complaints with bilingual display terms and synonyms · fast autocomplete search · mapping table structure ready for SNOMED if licensing is resolved · **an endocrine-focused favourites set** so Dr. Nahid's common diagnoses are one keystroke away · code version tracking so historical codings remain interpretable.
**Out of scope:** SNOMED content (pending D-24) · full ICD browsing UI beyond search.
**Dependencies:** CP06, **D-24**.
**Backend:** Terminology module. **Database:** Code tables + trigram indexes. **API:** Search and lookup. **Frontend/Mobile:** Coded pickers. **Security:** — **Events/Audit:** Codings stored with code system and version.
**Testing:** Search relevance for common endocrine diagnoses in both languages; version handling; performance.
**Manual verification:** Dr. Nahid searches his twenty most common diagnoses and confirms each is findable in ≤3 keystrokes.
**Acceptance criteria:** (1) The 20 most common DTHC diagnoses are each findable within 3 keystrokes. (2) Every coding stores its system and version. (3) Bilingual display terms exist for the favourites set. (4) Search p95 <150ms.
**Technologies:** Per the §4 stack; no new dependencies. **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Terminology service, code sets, favourites list.
**Effort:** 6 days. **Risks:** ICD-11 vs ICD-10 choice affects downstream mapping — decide at D-24 before starting.
**Open decisions:** **D-24 (blocking).**

---

### CP53 · Medical History Capture
**Objective:** Station 4 — complaints, comorbidities, family and surgical history, current medications, vaccinations.
**Why:** Feeds diagnosis, safety checking, and the synthesis.
**Scope:** Coded chief complaints with duration and severity · comorbidity list with onset · family history structured by relation and condition · surgical history · **current medication list reconciled against the formulary** (this list is a direct input to the safety engine) · vaccination records · smoking/alcohol carried from the lifestyle station without duplicate entry.
**Out of scope:** Allergies (CP54 — deliberately separate because of the hard stop).
**Dependencies:** CP52, CP42.
**Mobile/Frontend:** History capture. **Backend:** Structured storage. **Events/Audit:** Per-item events so additions and removals are traceable.
**Testing:** Coded capture; medication reconciliation against the formulary; carry-forward from previous visits (with explicit confirmation, never silent).
**Manual verification:** Capture a complex patient's history; return at the next visit and confirm prior history is carried forward for confirmation rather than re-entry.
**Acceptance criteria:** (1) Complaints and comorbidities are coded, not free text. (2) Current medications link to formulary products where they exist. (3) Prior history is presented for confirmation at the next visit, never auto-accepted. (4) Every item is individually attributed.
**Technologies:** Per the §4 stack; no new dependencies. **Database:** Uses the CP42 observation tables; no new schema. **API:** Uses the generic observation write and query API from CP42. **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Security:** No new attack surface; existing controls apply.
**Deliverables:** History station app, reconciliation logic.
**Effort:** 6 days. **Risks:** History taking is slow — the carry-forward-and-confirm pattern is the main mitigation.
**Open decisions:** Which history elements are mandatory (**clinical confirmation**).

---

### CP54 · Allergy Hard Stop
**Objective:** §3 Step 4's checkpoint — no file proceeds without either "No Known Allergies" explicitly asserted or a coded allergy entry.
**Why:** A named fail-closed requirement, and a direct input to the medication safety engine.
**Scope:** Allergy capture (substance from a coded list, reaction, severity, certainty) · explicit `NO_KNOWN_ALLERGY_ASSERTED` event with attribution · **a blocking gate: the visit cannot progress past Step 4 without one or the other** · prominent allergy display on the patient header everywhere · allergy change history.
**Out of scope:** Cross-reactivity logic (CP78).
**Dependencies:** CP53.
**Backend:** Allergy module + gate. **Database:** `allergies`, gate state. **Mobile/Frontend:** Capture and prominent display. **Security:** — **Events/Audit:** Both assertion and entry are events; the assertion carries the asserting operator's identity.
**Testing:** Gate enforcement (attempt to advance without allergy status and confirm blocking); display presence on every patient-context screen.
**Manual verification:** Try to move a patient to the next station without allergy status; confirm it is impossible and the message explains why.
**Acceptance criteria:** (1) A visit cannot advance past the history station without allergy status. (2) "No Known Allergies" is an explicit, attributed assertion — never a default or an empty field. (3) Allergies appear on the patient header on every screen and on the prescription. (4) The gate cannot be bypassed by any client.
**Technologies:** Per the §4 stack; no new dependencies. **API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Allergy module, gate, header display.
**Effort:** 4 days. **Risks:** Operators may assert NKA reflexively to clear the gate — mitigate by requiring the assertion to be a deliberate action and by surfacing NKA rates per operator in QA.
**Open decisions:** Allergy substance code list source (**clinical confirmation**).

---

### CP55 · Counseling Template Engine & Admin UI [R-07]
**Objective:** Physician-configurable counseling checklists, "without code changes" (§5.1).
**Why:** A named requirement. The diabetes template is the launch minimum; thyroid, obesity, PCOS and growth follow without a release.
**Scope:** Template model (diagnosis-linked, versioned, ordered items, mandatory flags, room assignment, bilingual item text, optional guidance text per item) · admin authoring UI with preview and publish · versioning so completed sessions always reference the version used · **the 7 diabetes seed items from §5.1** · template assignment rules per diagnosis.
**Out of scope:** Mobile ticking (CP56) · the gate (CP57) · the counseling content itself (**D-53, Dr. Nahid**).
**Dependencies:** CP06, CP21.
**Backend:** Template module. **Database:** `counseling_templates`, `items`, versions. **API:** Template CRUD + publish. **Frontend:** Authoring UI. **Security:** Publishing requires elevated permission + step-up. **Events/Audit:** Template publication audited.
**Testing:** Versioning correctness (an in-flight session keeps its version); publish workflow; bilingual content validation.
**Manual verification:** Dr. Nahid creates a thyroid counseling template through the UI, publishes it, and sees it appear on a counselor's phone — with no developer involvement.
**Acceptance criteria:** (1) A new template can be authored and published by a physician without a code change. (2) Completed sessions retain the template version used. (3) The diabetes template with all 7 items is seeded. (4) Items exist in both languages before publishing is allowed.
**Technologies:** Per the §4 stack; no new dependencies. **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Template engine, authoring UI, diabetes seed template.
**Effort:** 7 days. **Risks:** **Content authoring (D-53) is the dependency, not the engine.** The engine can be complete while templates remain empty.
**Open decisions:** D-53 (content); tick criteria — what "done" means per item (**clinical**).

---

### CP56 · Counseling Mobile Ticking
**Objective:** §5.3 — the counselor ticks items on a phone, with full attribution.
**Why:** Explicitly mobile "because it is faster on the floor" [R-01].
**Scope:** Session start per visit with the right template · large tick targets · optional per-item note · un-tick with reason · progress indicator · room-based flow (§5.2) with configurable sequence · realtime publication so the traffic board and physician view update immediately.
**Out of scope:** Gate enforcement (CP57).
**Dependencies:** CP55, CP11.
**Mobile:** Counseling screens. **Backend:** Session and tick events. **Events/Audit:** `COUNSELING_ITEM_TICKED` with full attribution per item — each tick is individually attributable, as §5.3 requires.
**Testing:** Tick/un-tick flows; attribution per item; realtime propagation; offline behaviour (ticks queue like any other event).
**Manual verification:** Complete a full diabetes counseling session on a phone; confirm each tick is individually attributed and that the physician's view updates live.
**Acceptance criteria:** (1) Each tick carries its own attribution and timestamp. (2) A full 7-item session takes under **60 seconds** of interaction (*proposed*). (3) Un-ticking requires a reason and is recorded. (4) Progress is visible on the traffic board within 2s.
**Technologies:** Per the §4 stack; no new dependencies. **Database:** Uses the CP42 observation tables; no new schema. **API:** Uses the generic observation write and query API from CP42. **Frontend:** Captured values surface on the physician dashboard (CP73). **AI:** — **OCR/NLP:** — **Security:** No new attack surface; existing controls apply.
**Deliverables:** Counseling station app.
**Effort:** 5 days. **Risks:** Ticking without actually counseling — an integrity risk mitigated by the physician's spot-questioning (§5.4) and by time-per-item analytics.
**Open decisions:** None.

---

### CP57 · Counseling Fail-Closed Gate & Physician Verification
**Objective:** §5.5 — no patient reaches Step 9 until mandatory items are ticked; §5.4 — the physician can see and spot-check exactly what was covered.
**Why:** Two named requirements that together make counseling verifiable rather than assumed.
**Scope:** Gate evaluation at queue transition to the physician station · a clear blocked state with the missing items named and a one-tap route back to the right room · **supervisor override with a recorded reason** (a rigid gate with no escape valve will be worked around) · the physician's counseling panel showing ticked/unticked items with attribution and timing, designed for spot-questioning ("What did they teach you about injection sites?").
**Out of scope:** Other station gates (CP83 handles QA).
**Dependencies:** CP56, CP39.
**Backend:** Gate evaluator. **Frontend:** Physician counseling panel. **Mobile:** Blocked-state UI with remediation path. **Security:** Override requires elevated permission. **Events/Audit:** `VISIT_GATE_BLOCKED`, `VISIT_GATE_SATISFIED`, overrides recorded.
**Testing:** Gate blocking with missing mandatory items; override path; panel rendering; a test asserting no client can bypass the gate.
**Manual verification:** Attempt to queue a diabetic patient to the physician with 5 of 7 mandatory items ticked; confirm blocking, confirm the two missing items are named, complete them, confirm release.
**Acceptance criteria:** (1) Queueing to Step 9 is impossible with unticked mandatory items, enforced server-side. (2) The blocked message names exactly which items are missing. (3) Override requires elevated permission and a reason, and is audited. (4) The physician panel shows completion status with attribution and time per item.
**Technologies:** Per the §4 stack; no new dependencies. **Database:** — **API:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Gate engine, physician panel, override workflow.
**Effort:** 5 days. **Risks:** Gate causes clinic-floor friction on a busy day — the override valve plus override-rate monitoring is the answer.
**Open decisions:** Which items are mandatory per template (**clinical**); who may override (**operational**).

---

### CP58 · Lifestyle Assessment & Composite Risk Score
**Objective:** Station 3's structured lifestyle data and the Lifestyle Risk Score for behavioural cohorting.
**Why:** Named in §3 Step 3 and central to §12's research cohorting.
**Scope:** Smoking with computed pack-years · alcohol · sleep · **validated stress instrument(s) per D-26**, storing raw item responses not only totals · motivation and readiness-to-change · dietary triggers · activity baseline · composite score with a versioned formula.
**Out of scope:** Instrument licensing (**D-26 — some validated instruments are copyrighted**).
**Dependencies:** CP42, **D-26**.
**Backend:** Assessment + scoring module. **Database:** Instrument definitions, responses, scores with formula version. **Mobile:** Assessment screens. **Events/Audit:** `LIFESTYLE_ASSESSMENT_RECORDED`, `LIFESTYLE_SCORE_DERIVED`.
**Testing:** Score computation against worked examples; raw-response retention; formula versioning.
**Manual verification:** Complete an assessment and verify the composite score by hand against the agreed formula.
**Acceptance criteria:** (1) Raw item responses are stored, not just totals (essential for research). (2) Score formula version is stored with every score. (3) Pack-years computed correctly against reference examples. (4) Instrument version recorded.
**Technologies:** Per the §4 stack; no new dependencies. **API:** Uses the generic observation API from CP42. **Frontend:** Captured values surface on the physician dashboard (CP73); no dedicated web screen. **AI:** — **OCR/NLP:** — **Security:** No new attack surface; existing controls apply.
**Deliverables:** Lifestyle station app, scoring engine.
**Effort:** 6 days. **Risks:** **D-26 blocks the score.** The data capture can proceed with the formula deferred, if Dr. Nahid prefers.
**Open decisions:** **D-26 (instruments and formula).**

---

### CP59 · Nutrition Assessment Capture
**Objective:** Station 7's data: 24-hour recall, caloric intake, meal timing, food habits.
**Why:** Named station; input to the nutrition plan and to §12's exercise/diet–outcome analyses.
**Scope:** 24-hour recall with a **local Bangladeshi food picker** (portion sizes in household measures — cups, pieces, spoons — because that is how patients describe food) · caloric and macro estimation · meal timing · food habits and triggers · **multi-operator support: a second assistant may enter food habits from their own device** [R-01, R-02] · renal/hepatic status carried in as context.
**Out of scope:** AI-generated diet plans (basic version only here; full at CP132).
**Dependencies:** CP42, CP11.
**Mobile:** Nutrition screens with the food picker. **Database:** Food composition table. **Backend:** Caloric computation. **Events/Audit:** Attributed per entry, including split entry by two operators.
**Testing:** Caloric computation against known values; concurrent entry by two operators on one patient; food search performance.
**Manual verification:** The clinic nutritionist enters a real patient's 24-hour recall and confirms the food list covers what patients actually eat.
**Acceptance criteria:** (1) A 24-hour recall is capturable in under **4 minutes** (*proposed*). (2) Two operators can contribute to the same assessment concurrently, each attributed. (3) Caloric estimates match the food table's values. (4) The food picker covers the clinic's common foods.
**Technologies:** Per the §4 stack; no new dependencies. **API:** Uses the generic observation API from CP42. **Frontend:** Captured values surface on the physician dashboard (CP73); no dedicated web screen. **AI:** — **OCR/NLP:** — **Security:** No new attack surface; existing controls apply.
**Deliverables:** Nutrition station app, food database, computation.
**Effort:** 6 days. **Risks:** **The Bangladeshi food composition database is a content dependency** — source it (a national nutrition institute table or equivalent) or author it; this is not a coding problem.
**Open decisions:** Food composition data source (**content/licensing confirmation**).

---

### CP60 · Exercise Assessment Capture
**Objective:** Station 8: mobility, joint issues, baseline fitness, and a contraindication-aware routine.
**Why:** Named station; feeds §12.1's exercise–outcome correlation analysis, which requires structured adherence data from the start.
**Scope:** Mobility and joint assessment · baseline fitness · contraindication capture (severe neuropathy, retinopathy, cardiac limitation) · **deterministic contraindication rules filtering exercise options** (§3 Step 8: no high-impact cardio in severe neuropathy) · routine selection from a curated library with intensity levels · follow-up targets · printed exercise sheet.
**Out of scope:** AI-generated routines (CP132).
**Dependencies:** CP42, CP11.
**Mobile:** Exercise screens. **Database:** Exercise library with contraindication tags. **Backend:** Filter rules. **Events/Audit:** `EXERCISE_ASSESSMENT_RECORDED`, `EXERCISE_PLAN_ISSUED`.
**Testing:** Contraindication filtering (a patient with severe neuropathy must never be offered high-impact options, proven by test); target tracking across visits.
**Manual verification:** Create a plan for a patient with severe neuropathy and confirm contraindicated exercises are not merely warned about but **absent**.
**Acceptance criteria:** (1) Contraindicated exercises are excluded, not warned. (2) Adherence targets are structured and comparable across visits (required for §12.1). (3) The exercise sheet prints legibly in Bangla. (4) The library is editable without a code release.
**Technologies:** Per the §4 stack; no new dependencies. **API:** Uses the generic observation API from CP42. **Frontend:** Captured values surface on the physician dashboard (CP73); no dedicated web screen. **AI:** — **OCR/NLP:** — **Security:** No new attack surface; existing controls apply.
**Deliverables:** Exercise station app, exercise library, contraindication rules.
**Effort:** 5 days. **Risks:** Library content is physician-authored (**content dependency**).
**Open decisions:** Exercise library content and contraindication mapping (**clinical**).

---

### CP61 · Attribution UI Everywhere ("Entered by") [R-03]
**Objective:** §4.2's exact requirement: any reviewer sees who entered a value **instantly, without digging**.
**Why:** Dr. Nahid's stated requirement, verbatim, and the precondition for the correction workflow.
**Scope:** `<ValueWithAttribution>` component for web and mobile · hover on web / tap on mobile reveals operator, role, station, device, timestamp, and source (station entry vs OCR vs field) · a compact always-visible variant for dense views · attribution on timeline points (§8: "hovering any value reveals its Entered by attribution") · **a review convention that no clinical value is rendered as raw text anywhere**.
**Out of scope:** The correction workflow itself (CP62).
**Dependencies:** CP24, CP09.
**Frontend/Mobile:** Components + adoption across every existing screen. **API:** Attribution included in every clinical read payload. **Security:** Attribution respects RBAC (a role that may see the value may see who entered it).
**Testing:** Component tests; a **codebase audit test** or review checklist confirming no clinical value bypasses the component; attribution correctness after a correction (must show both original and corrector).
**Manual verification:** On the physician dashboard, tap five different values from five stations and confirm each shows the correct operator, role, device and time within one interaction.
**Acceptance criteria:** (1) Every clinical value in every UI exposes attribution in one interaction. (2) Attribution is correct after corrections (both authors visible). (3) OCR-sourced values are visibly distinguished from station-entered ones. (4) No screen renders a clinical value without the component.
**Technologies:** Per the §4 stack; no new dependencies. **Backend:** — **Database:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Events/Audit:** —
**Deliverables:** Attribution components, adoption across all screens.
**Effort:** 4 days. **Risks:** Visual clutter — the compact variant and hover-reveal keep dense views readable.
**Open decisions:** None.

---

### CP62 · Correction & Flagging Workflow [R-04]
**Objective:** Implement §4.3's canonical workflow end to end — the 140/150 height case.
**Why:** This is the requirement Dr. Nahid described most concretely, and the one that turns the audit trail from a compliance artefact into an operational quality tool.
**Scope:** Flag-a-value action available to physician, QA and senior operators · immediate display of author, device, station, timestamp · **correction request routed to the original author's device**, with supervisor override · correction capture with a structured reason code plus free text · `*_CORRECTED` event referencing the original · **cascade recomputation of all derived values, versioned not overwritten** · value history view showing the full chain · notification to the author · tally to the operator quality record (CP63).
**Out of scope:** HR dashboard aggregation (CP140).
**Dependencies:** CP61, CP23.
**Backend:** Correction module. **Database:** `correction_requests`; corrections in the ledger. **API:** Flag, assign, correct, reject. **Frontend:** Flagging and history UI. **Mobile:** Incoming correction requests with a clear action. **Security:** Only permitted roles may flag; only the author or a supervisor may correct. **Events/Audit:** `CORRECTION_REQUESTED/APPLIED/REJECTED`, `SUPERVISOR_OVERRIDE_APPLIED`.
**Testing:** **The full canonical scenario as an automated end-to-end test**: record 150 → flag → route → correct to 140 → assert the original event is intact and retrievable, the projection shows 140, BMI and percentile are recomputed, both values appear in history with correct attribution, and the operator tally incremented.
**Manual verification:** Execute the 140/150 scenario manually across two devices exactly as §4.3 describes, and confirm every step behaves as written in the blueprint.
**Acceptance criteria:** (1) The original value is never altered or deleted and remains queryable. (2) The correction records who, when, why, and what it corrects. (3) Derived values recompute and are versioned. (4) The author is notified on their device. (5) The value history shows the complete chain with both attributions. (6) The operator's quality tally increments.
**Technologies:** Per the §4 stack; no new dependencies. **AI:** — **OCR/NLP:** —
**Deliverables:** Correction workflow, history UI, notification, canonical E2E test.
**Effort:** 8 days. **Risks:** Cascade complexity when a corrected value feeds several derived values and an AI summary — enumerate dependents explicitly and re-run the synthesis when clinically material.
**Open decisions:** Reason code taxonomy (**clinical/operational confirmation**); who may flag and who may override (**operational**).

---

### CP63 · Operator Quality Record & Recurrence Detection
**Objective:** §4.3's stated purpose — "recurring patterns per operator surface so targeted retraining happens and the same mistake does not repeat."
**Why:** Explicitly named as *the whole point* of the correction mechanism. Without this, corrections are bookkeeping.
**Scope:** Per-operator quality record (entries made, corrections received, correction rate by category, time-of-day patterns) · **pattern detection** (repeated transcription errors, a specific measurement type, end-of-shift clustering) · retraining flags with thresholds · an operator-facing view of their own record (transparency reduces resentment and improves behaviour) · a supervisor view.
**Out of scope:** The full HR dashboard (CP140) · performance-linked pay or discipline (an organisational decision, not a system one).
**Dependencies:** CP62.
**Backend:** Quality aggregation job. **Database:** `operator_quality_records`. **API/Frontend:** Operator and supervisor views. **Security:** An operator sees only their own record; supervisors see their team. **Events/Audit:** Retraining flags recorded.
**Testing:** Aggregation correctness; pattern detection on synthetic error histories; permission scoping.
**Manual verification:** Generate three transcription errors for one operator within 30 days and confirm the retraining flag appears on the supervisor view with the supporting detail.
**Acceptance criteria:** (1) Correction rates are computed correctly per operator and category. (2) Recurrence patterns trigger flags at the agreed thresholds. (3) Operators can see their own record. (4) No operator can see another's record without supervisory permission.
**Technologies:** Per the §4 stack; no new dependencies. **API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Quality record engine, operator and supervisor views.
**Effort:** 5 days. **Risks:** **Cultural risk** — a metric that feels punitive damages data honesty (staff hide errors instead of correcting them). Frame as "quality support"; involve Dr. Nahid in how it is presented to staff.
**Open decisions:** Retraining thresholds (**proposed values requiring approval**); visibility policy.

---

### CP64 · Mobile Local Database & Outbox
**Objective:** The encrypted local store and write queue that make offline operation possible.
**Why:** §15.2 — a Wi-Fi drop must never lose a station entry. This must exist before any more station features are built on top of a network-dependent write path.
**Scope:** SQLCipher-encrypted SQLite · schema for `outbox`, `local_events`, `projections`, `reference_cache`, `sync_meta` · local migrations · the **command handler that is the only write path in the app** · local projections so the UI reads from SQLite, not from the network · key management via the OS keystore, released only after authentication · cache TTL and purge.
**Out of scope:** The sync protocol (CP65/CP66) — this checkpoint queues; it does not yet send.
**Dependencies:** CP11, CP24. **Technologies:** op-sqlite + SQLCipher.
**Mobile:** Local database layer. **Security:** Encrypted at rest; key in keystore; wipe on logout/revocation; no PHI in OS backups. **Events/Audit:** Client-generated `event_id` per §7.2.
**Testing:** Encryption verified (the database file must be unreadable without the key); migration tests; queue durability across app kill; storage-full behaviour.
**Manual verification:** Record entries with the network off; force-kill the app; relaunch; confirm every entry is still queued and visible.
**Acceptance criteria:** (1) The local database file is unreadable without the keystore key. (2) Queued events survive app termination and device restart. (3) The UI reads exclusively from local state (proven by working fully in airplane mode). (4) Logout wipes local PHI.
**Backend:** — **Database:** — **API:** — **Frontend:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Local database layer, outbox, encryption.
**Effort:** 8 days. **Risks:** SQLCipher build configuration on Expo — validate early; it is a known integration friction point.
**Open decisions:** Cache TTL (**operational confirmation**).

---

### CP65 · Sync Protocol — Server Side
**Objective:** The server endpoints that accept batched offline events and serve incremental pulls.
**Why:** The other half of the offline guarantee, and the place where idempotency and partial failure must be handled correctly.
**Scope:** `POST /sync/events` accepting a batch, processing **each event independently**, returning per-event `ACCEPTED | DUPLICATE | REJECTED(reason)` · `GET /sync/events?since=` with scope filtering and RBAC · reference-data version endpoint · clock-skew measurement and reporting · **revoked-device quarantine** rather than silent acceptance or silent loss.
**Out of scope:** Client engine (CP66).
**Dependencies:** CP64, CP23.
**Backend:** Sync module. **API:** Sync endpoints. **Security:** Device and session verified per batch; revoked device handling; per-device rate limits. **Events/Audit:** Late-synced events carry `source = MOBILE_OFFLINE_SYNC` and their original `occurred_at`.
**Testing:** Partial batch failure (one bad event among fifty); duplicate batch submission; large batch (500 events); revoked-device path; ordering preservation per aggregate; clock-skew handling.
**Manual verification:** Queue 100 events offline, reconnect, confirm all appear with correct original timestamps and attribution, and that resubmitting the same batch changes nothing.
**Acceptance criteria:** (1) A bad event never blocks the rest of its batch. (2) Duplicate submission produces no duplicate events. (3) `occurred_at` is preserved; `recorded_at` reflects arrival. (4) Per-aggregate ordering is preserved. (5) Events from revoked devices are quarantined and surfaced, never silently dropped.
**Technologies:** Per the §4 stack; no new dependencies. **Database:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Sync API, quarantine handling, protocol documentation.
**Effort:** 7 days. **Risks:** Large batches and timeouts — chunking and streaming are part of the design.
**Open decisions:** Batch size limits (tune by measurement).

---

### CP66 · Sync Engine — Client Side
**Objective:** Reliable, automatic, resumable synchronisation from the phone.
**Why:** The most subtle code in the mobile app; the whole offline promise rests on it.
**Scope:** Background sync (on connectivity regained, on foreground, on interval) · batching with per-aggregate ordering · exponential backoff with jitter · per-event result handling · reconciliation of pulled server events into local projections · **conflict surfacing per §13.6, never silent resolution** · reference data refresh · sync metrics visible to the operator.
**Out of scope:** The UX polish of the failure ladder (CP67).
**Dependencies:** CP65.
**Mobile:** Sync engine. **Security:** Token refresh mid-sync without losing the queue; sync halts cleanly on revocation. **Events/Audit:** Sync attempts logged locally for support.
**Testing:** The full §13.10 matrix — airplane mode mid-entry, app kill with a full queue, 200-event sync, one rejection in fifty, wrong device clock, token expiry while offline, device revoked while offline, duplicate batch, 10% packet loss, storage full.
**Manual verification:** Run a realistic clinic session entirely offline for 30 minutes across three stations, then reconnect and verify every entry arrives correctly attributed and correctly timestamped.
**Acceptance criteria:** (1) **Zero data loss** across every scenario in §13.10. (2) No duplicate events after any retry pattern. (3) Sync resumes automatically without user action. (4) Token expiry during offline operation does not lose the queue. (5) Conflicts are surfaced, never auto-resolved.
**Technologies:** Per the §4 stack; no new dependencies. **Backend:** — **Database:** — **API:** — **Frontend:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Sync engine, reconciliation logic, metrics.
**Effort:** 10 days. **Risks:** **The highest-risk mobile checkpoint.** Budget generous test time; bugs here corrupt clinical data silently, which is the worst failure class in the system.
**Open decisions:** Offline registration policy (§13.7 — **confirmation required**).

---

### CP67 · Offline UX, Failure Ladder & "Needs Attention"
**Objective:** The operator always knows the true state of their data.
**Why:** §13.9 — a sync indicator that lies destroys trust permanently.
**Scope:** Persistent sync status pill (Synced / Syncing(n) / Offline — n queued / n need attention) · per-record pending indicators · sync detail screen listing queued and failed items with plain-language reasons in both languages · manual retry · **"Needs attention" workflow** for rejected events with correct-and-resubmit or escalate · offline banner with an honest explanation of what still works.
**Out of scope:** — **Dependencies:** CP66.
**Mobile:** Status UI and remediation flows. **Security:** Failure reasons must not leak information the operator's role cannot see.
**Testing:** Status accuracy under every sync state; message clarity review in both languages; remediation flow tests.
**Manual verification:** Ask a non-technical staff member to explain, from the screen alone, whether their data is saved. If they cannot, the checkpoint is not done.
**Acceptance criteria:** (1) The indicator is accurate at all times (never shows Synced with a non-empty queue). (2) Failed items are listed with an actionable, non-technical, bilingual reason. (3) Manual retry works. (4) A non-technical staff member can correctly interpret every state in a usability test.
**Technologies:** Per the §4 stack; no new dependencies. **Backend:** — **Database:** — **API:** — **Frontend:** — **AI:** — **OCR/NLP:** — **Events/Audit:** —
**Deliverables:** Sync UX, needs-attention workflow, message catalogue.
**Effort:** 5 days. **Risks:** Alarm fatigue from an over-prominent indicator — calibrate with real staff.
**Open decisions:** None.

---

### CP68 · Offline Test Suite (Device + CI)
**Objective:** Make offline correctness a permanent, automated guarantee rather than a one-time manual check.
**Why:** Offline bugs regress easily and are invisible until data is lost. This suite is the regression net for the rest of the project's life.
**Scope:** Automated Maestro flows for the §13.10 scenarios · a network-condition simulator in CI · a server-side chaos harness (random rejections, timeouts, slow responses) · a data-integrity checker comparing local and server state after a sync run · **a nightly long-running soak test** simulating a full clinic day with intermittent connectivity.
**Out of scope:** Load testing (CP93).
**Dependencies:** CP67.
**Mobile/Backend:** Test infrastructure. **Testing:** This checkpoint *is* testing.
**Manual verification:** Review the nightly soak report; confirm zero integrity mismatches.
**Acceptance criteria:** (1) All §13.10 scenarios run automatically in CI. (2) The integrity checker reports zero mismatches after each run. (3) The nightly soak completes a simulated clinic day with zero data loss. (4) A deliberately introduced sync bug is caught by the suite (verified by mutation).
**Technologies:** Per the §4 stack; no new dependencies. **Backend:** — **Database:** — **API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Security:** No new attack surface; existing controls apply. **Events/Audit:** —
**Deliverables:** Offline test suite, chaos harness, integrity checker, nightly report.
**Effort:** 6 days. **Risks:** Flaky device tests eroding trust in CI — invest in stability, quarantine flaky tests rather than ignoring failures.
**Open decisions:** None.

---

### CP69 · Async Job Infrastructure & Queue Monitoring
**Objective:** The job system behind the §7.1 five-minute SLA, with the SLA measured rather than assumed.
**Why:** [R-05] depends on asynchronous processing that provably completes before the patient reaches Step 9. §15.2 explicitly requires queue monitoring.
**Scope:** River integration with **transactional enqueue** · job classes and priorities per §8.5 · retry policies with backoff · dead-letter handling with an operator-visible queue · scheduled/periodic jobs · **queue dashboard: depth, oldest-unstarted age, throughput, failure rate, SLA attainment per job type** · alerting on depth and age thresholds.
**Out of scope:** The synthesis job itself (CP71).
**Dependencies:** CP06, CP07. **Technologies:** River, Cloud Monitoring.
**Backend:** Job framework + `cmd/worker`. **Database:** River tables. **API:** Admin job status endpoints. **Frontend:** Queue health page. **Security:** Job payloads may reference but must not embed PHI. **Events/Audit:** Job failures visible and alertable.
**Testing:** Transactional enqueue (a rolled-back transaction must not leave a job); retry and dead-letter behaviour; concurrency; a test asserting SLA metrics are emitted.
**Manual verification:** Enqueue 100 jobs, watch the dashboard track them, kill a worker mid-run and confirm jobs resume without duplication.
**Acceptance criteria:** (1) A job enqueued in a rolled-back transaction never runs. (2) Failed jobs retry per policy then dead-letter visibly. (3) The dashboard shows depth, age and SLA attainment per job type. (4) Alerts fire on configured thresholds. (5) Worker restart causes no duplicate execution.
**Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Job framework, worker deployment, queue dashboard, alerts.
**Effort:** 5 days. **Risks:** Low. **Open decisions:** Alert thresholds (**operational**).

---

### CP70 · AI Gateway, PHI Minimisation & Prompt Registry
**Objective:** The single controlled path to any AI model (§10.3).
**Why:** Ten agents will exist. Building the gateway first means PHI minimisation, cost metering, versioning and auditability are inherited by all of them instead of being retrofitted ten times.
**Scope:** `Gateway.Invoke` per §10.3 · **Gemini adapter (D-07) with an explicit tier guard: a free-tier credential is only usable for requests flagged synthetic; any payload derived from a real patient must use the paid/Vertex credential or the call fails closed** · provider adapter interface with at least one real and one mock implementation · **PHI minimisation with a documented, tested strip-and-restore mechanism** (D-08) · versioned prompt registry in the repository · pinned model versions · strict output schema validation with retry · response caching by input hash · timeout, circuit breaker, and fallback · **`AIInteraction` record for every call** (prompt version, model version, tokens, cost, latency, validation result) · per-agent and per-day cost metering with budget alerts.
**Out of scope:** Any specific agent · grounding validation (CP72).
**Dependencies:** CP69, **D-07, D-08**.
**Backend:** `ai` module. **Database:** `ai_interactions`, `prompt_versions`. **API:** Internal. **Security:** **No identifier reaches a provider**; provider credentials in Secret Manager; the mock provider makes CI free and deterministic. **Events/Audit:** Every invocation recorded and queryable.
**Testing:** PHI minimisation tests including adversarial cases (identifiers embedded in free text); schema validation and retry; circuit breaker; cost accounting accuracy; cache correctness.
**Manual verification:** Invoke a test agent; inspect the recorded outbound payload and confirm no name, NID, phone or address is present; confirm the cost record matches the provider's reported usage.
**Acceptance criteria:** (1) No direct identifier is transmitted to any provider, proven by tests including free-text cases. (1b) **A real-patient payload cannot be sent on a free-tier credential — the call fails closed, proven by test.** (2) Every AI call is recorded with prompt version, model version, tokens and cost. (3) Provider is swappable by configuration. (4) Invalid model output is rejected, retried, and ultimately fails cleanly rather than being passed through. (5) Budget alerts fire at configured thresholds.
**Technologies:** Per the §4 stack; no new dependencies. **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** AI gateway, prompt registry, minimisation layer, cost metering.
**Effort:** 8 days. **Risks:** PHI leakage through free-text fields is the main one — the scrubber plus a human-reviewable outbound log is the mitigation.
**Open decisions:** **D-07 (provider), D-08 (minimisation policy).**

---

### CP71 · Pre-Consultation Synthesis Agent v1 [R-05]
**Objective:** §7.1 — the one-page synthesis and draft plan, ready before the patient walks in.
**Why:** The operational heart of "the patient always arrives ready" and the single most visible piece of value for Dr. Nahid.
**Scope:** Deterministic assembly of the clinical context from all completed stations · the synthesis prompt (versioned) producing narrative + structured draft per §10.4 A1 · **the "Analyze / Summarize / Comprehensive Report" button pressed by the final assistant** (§7.1), plus automatic triggering when all stations complete · asynchronous job with priority · SLA measurement · incremental re-run when material new data arrives · result stored and linked to the visit · **paediatric growth analytics included** [R-06].
**Out of scope:** Grounding validation (CP72, immediately next — but the two ship together for pilot) · records chronology input (Phase 2, CP109).
**Dependencies:** CP70, CP37.
**Backend:** Synthesis agent + job. **Database:** Synthesis results. **API:** Trigger and fetch endpoints. **Mobile:** The trigger button on the final assistant's screen. **Frontend:** Rendering on the dashboard (CP73). **AI:** This is the checkpoint. **Security:** PHI-minimised via CP70. **Events/Audit:** `AI_SYNTHESIS_REQUESTED/COMPLETED/FAILED`.
**Testing:** End-to-end on synthetic patients; SLA measurement under load; failure and timeout paths; a test asserting the synthesis contains no invented drug names (validated against the formulary).
**Manual verification:** Run a full synthetic patient through all stations, press the button, and confirm a clinically coherent one-page summary appears within the SLA. **Dr. Nahid reviews at least 20 summaries and rates their usefulness before this checkpoint is accepted.**
**Acceptance criteria:** (1) Synthesis completes within the SLA (**≤5 minutes per §7.1; the operational target should be tightened after measurement**) for ≥95% of visits under expected load. (2) The physician never needs to trigger it manually in normal flow. (3) Output is clearly labelled AI-generated. (4) Failure produces the degraded state from D-15, not an empty screen. (5) Dr. Nahid's review of 20 summaries meets an agreed usefulness bar (*criterion to be defined with him*).
**Technologies:** Per the §4 stack; no new dependencies. **OCR/NLP:** —
**Deliverables:** Synthesis agent, prompt, trigger UI, SLA dashboard.
**Effort:** 10 days. **Risks:** **Quality is the risk, not mechanics.** Expect several prompt iterations with Dr. Nahid. Budget for that explicitly rather than treating iteration as rework.
**Open decisions:** Usefulness acceptance bar (**clinical**); whether the summary is bilingual or English-only for the physician (**confirmation**).

---

### CP72 · Grounding Validation & AI Evaluation Harness
**Objective:** Make hallucination structurally detectable, and make AI quality measurable over time.
**Why:** §10.2's grounding check is the difference between an AI assistant that is safe and one that is a liability. §10.5 requires regression detection when models or prompts change.
**Scope:** **Grounding validator**: every numeric value, date and drug name in an AI output must resolve to a stored fact; violations block display and are logged as defects · a frozen evaluation set of de-identified real cases with physician-reviewed reference outputs · automated metrics (grounding violations, schema failures, latency, cost) · **CI gate running the evaluation set on every prompt or model change** · a quality dashboard trending metrics over time.
**Out of scope:** Clinical accuracy thresholds (**proposed values requiring Dr. Nahid's approval**).
**Dependencies:** CP71.
**Backend:** Validator + evaluation runner. **Database:** `ai_evaluation_runs`. **AI:** Core of the checkpoint. **Security:** The evaluation set is de-identified and access-controlled. **Events/Audit:** Grounding violations recorded as defects, not silently retried.
**Testing:** Inject known hallucinations (a fabricated HbA1c, a non-existent drug, a wrong date) and assert every one is caught; false-positive rate measured on correct outputs.
**Manual verification:** Deliberately corrupt a model response with a fabricated lab value and confirm it never reaches the physician's screen.
**Acceptance criteria:** (1) Every injected hallucination in the test set is detected. (2) The false-positive rate on correct outputs is low enough not to block legitimate summaries (measured and reported). (3) The evaluation set runs in CI on every prompt/model change. (4) Metrics trend on a dashboard. (5) A regression beyond the agreed threshold blocks deployment.
**Technologies:** Per the §4 stack; no new dependencies. **API:** — **Frontend:** — **Mobile:** — **OCR/NLP:** —
**Deliverables:** Grounding validator, evaluation set, CI gate, quality dashboard.
**Effort:** 8 days. **Risks:** Building the evaluation set requires physician time — schedule it, do not assume it.
**Open decisions:** Regression thresholds (**proposed values requiring approval**).

---

### CP73 · Physician Dashboard v1
**Objective:** §8's three-panel dashboard — the screen Dr. Nahid will spend his working life in.
**Why:** The blueprint's central promise of cognitive-load reduction lives or dies here.
**Scope:** **Left — Snapshot:** demographics, current vitals, BMI with class, **sparklines of the last 5 HbA1c**, active diagnoses, critical alerts, paediatric percentile card, counseling checklist status. **Centre — Clinical Summary:** the AI narrative with clear AI labelling and red-line carry-through (Phase 2). **Right — AI Assistant:** suggested ICD-coded diagnoses, missing-data alerts, drafted investigations and doses, each with accept/edit/reject. Plus: patient header with allergy banner, realtime updates, keyboard shortcuts, and a print/summary action.
**Out of scope:** The timeline (CP74) · prescription editing (CP81) · records panel (CP110).
**Dependencies:** CP71, CP37, CP10.
**Frontend:** The dashboard. **API:** Aggregated dashboard endpoint (one request, not twelve). **Security:** RBAC-filtered; break-glass path if the patient is outside the physician's normal scope. **Events/Audit:** Dashboard views audited (access to a clinical record is an auditable event).
**Testing:** Component and E2E tests; performance with a 10-year patient; realtime update tests; accessibility audit; rendering in Bangla.
**Manual verification:** **Dr. Nahid uses it for a full simulated consultation and confirms he can form a clinical picture in under 60 seconds without opening another screen.** That is the actual acceptance test.
**Acceptance criteria:** (1) Full dashboard loads in <1.5s p95 for a 10-year patient. (2) Every panel from §8 is present. (3) AI content is unmistakably marked. (4) Values update in realtime without refresh. (5) Attribution is one interaction away on every value. (6) Dr. Nahid confirms the 60-second comprehension target is met.
**Technologies:** Per the §4 stack; no new dependencies. **Backend:** — **Database:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Physician dashboard, aggregated API, performance report.
**Effort:** 12 days. **Risks:** Information density versus clarity — expect two or three design iterations with Dr. Nahid; schedule them.
**Open decisions:** Panel priority and default state (**clinical preference**).

---

### CP74 · Longitudinal Timeline v1
**Objective:** §8's "continuous, scrubbable, stock-chart-style view" correlating interventions and values on one time axis.
**Why:** Named requirement, and the visualisation that makes cause and effect legible "in seconds".
**Scope:** Horizontal time axis with zoom/pan/scrub · event lanes (diagnoses, medications with duration bars, investigations, procedures, admissions, lifestyle interventions) · **numeric value overlays** (HbA1c, weight, BP) on a shared axis · hover/tap reveals detail **and attribution** · date-range and type filters · performance with a decade of data · mobile-adapted view.
**Out of scope:** OCR-derived document entries (CP110) · predictive overlays.
**Dependencies:** CP37, CP73. **Technologies:** D3 (bespoke visualisation).
**Frontend:** Timeline component. **API:** Range-query endpoint from CP37. **Security:** RBAC per lane. **Events/Audit:** —
**Testing:** Rendering with 10,000 timeline points; interaction tests; visual regression; correctness of medication duration bars against the underlying data.
**Manual verification:** Load a 10-year synthetic patient; scrub across the decade; confirm smooth interaction and that starting a medication visibly aligns with the subsequent HbA1c change.
**Acceptance criteria:** (1) Smooth interaction (≥30fps) with 10 years of data. (2) All lanes from §8 render. (3) Hovering any value shows its attribution. (4) The correlation between an intervention and a value change is visually apparent. (5) Usable on a tablet.
**Backend:** — **Database:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Timeline component, range API, performance report.
**Effort:** 10 days. **Risks:** Custom visualisation is time-consuming and easy to underestimate — this is why it is its own checkpoint.
**Open decisions:** Which values are overlaid by default (**clinical preference**).

---

### CP75 · Medicine Formulary Model & Admin UI [R-16]
**Objective:** §10's curated formulary (Option A) with BDT pricing, maintainable by clinic staff.
**Why:** Every prescription depends on it; §16.1 asks explicitly who owns monthly price review.
**Scope:** `generics` (name, ATC class) and `medication_products` (trade name, strength, form, manufacturer, unit price BDT, DGDA registration, active flag) · admin CRUD with **bulk import from CSV** · **price history with effective dates** (essential for §12.3's affordability research, which needs the price at the time of prescribing, not today's price) · a monthly price-review workflow with a named owner and a reminder · **seeding of ~150–250 items** covering GLP-1 RAs, SGLT2 inhibitors, DPP-4 inhibitors, insulins, metformin, thyroid drugs, statins, antihypertensives and common adjuncts.
**Out of scope:** Full national database integration (Option B, Phase 3) · interaction rules (CP77).
**Dependencies:** CP06, CP21, **D-56**.
**Backend:** Formulary module. **Database:** Formulary tables + price history. **API:** CRUD, search, price lookup. **Frontend:** Formulary admin with price-review workflow. **Security:** Price changes audited with actor. **Events/Audit:** Price changes recorded with effective dates.
**Testing:** Price-history correctness (querying the price as of a past date must return the historical price); bulk import validation; search performance.
**Manual verification:** The clinic pharmacist updates ten prices through the UI and confirms both the new price and the historical record are correct.
**Acceptance criteria:** (1) Price as of any past date is retrievable. (2) Bulk import validates and reports errors per row. (3) The seed formulary covers DTHC's common prescriptions (**verified by Dr. Nahid against a month of real prescriptions**). (4) A monthly review reminder is issued to the named owner.
**Technologies:** Per the §4 stack; no new dependencies. **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Formulary module, admin UI, seed data, review workflow.
**Effort:** 7 days. **Risks:** **Seed content is the dependency.** Sourcing from DGDA published listings may help but requires verification and a licensing check.
**Open decisions:** **D-56 (price review owner and data source).**

---

### CP76 · Formulary 2-Letter Autocomplete
**Objective:** §10.1's hard speed requirement — two letters surface matching trade names with generic, strength, form and price.
**Why:** Used on every prescription line, dozens of times a day. Latency here is felt directly by the physician.
**Scope:** Trigram + prefix search across trade and generic names · **in-memory cache of the entire formulary in the API process** (a few hundred rows — this is the right answer and makes sub-10ms responses trivial) · ranked results (recent use by this physician, then frequency, then alphabetical) · keyboard-first interaction · display of generic, strength, form, price · handling of Bangla-script trade name entry.
**Out of scope:** Safety checking (CP78).
**Dependencies:** CP75.
**Backend:** Search + cache with invalidation on formulary change. **API:** `GET /formulary/search?q=`. **Frontend:** Combobox. **Security:** —
**Testing:** Latency benchmark; ranking correctness; cache invalidation on price/product change; two-letter result quality on the real seed set.
**Manual verification:** Dr. Nahid types two letters for his twenty most-prescribed drugs and confirms the intended drug is in the top three results every time.
**Acceptance criteria:** (1) p99 response <50ms including network on the clinic LAN. (2) Two characters return useful ranked results. (3) The physician's own recent prescriptions rank higher. (4) A formulary change is reflected within 60 seconds.
**Technologies:** Per the §4 stack; no new dependencies. **Database:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Events/Audit:** —
**Deliverables:** Autocomplete API, combobox component, benchmark.
**Effort:** 4 days. **Risks:** Low. **Open decisions:** None.

---

### CP77 · Medication Rule Model & Physician Authoring UI
**Objective:** Let Dr. Nahid author the deterministic safety rules the blueprint requires, without a developer.
**Why:** D-22's recommendation makes the physician the author of every clinical rule. That is only viable with a good authoring tool — otherwise the rules will not get written.
**Scope:** Rule model per §6.3 (interaction, contraindication, renal, hepatic, pregnancy, paediatric, duplicate-therapy, max-dose) with structured conditions · **an authoring UI that is comprehensible to a physician, not a programmer** (form-based, with plain-language preview of what the rule will do) · bilingual messages · severity (BLOCK/WARN/INFO) · versioning and approval workflow · **rule testing sandbox: author a rule, run it against a test patient, see the result before publishing** · import/export for review.
**Out of scope:** The evaluation engine (CP78) · licensed database integration.
**Dependencies:** CP75, **D-22**.
**Backend:** Rule model + validation. **Database:** `medication_rules` with versions. **API:** Rule CRUD, test-run. **Frontend:** Authoring UI + sandbox. **Security:** Publishing requires physician role + step-up; every change audited. **Events/Audit:** Rule publication audited with the full rule content.
**Testing:** Rule condition validation; sandbox correctness; versioning (a prescription checked yesterday must be re-checkable against yesterday's rule versions).
**Manual verification:** **Dr. Nahid authors five real rules unaided**, tests them in the sandbox, and publishes them. If he cannot do it unaided, the UI is not done.
**Acceptance criteria:** (1) A physician can author, test and publish a rule without developer help. (2) Every rule has a bilingual message and a severity. (3) Rule versions are retained; historical checks are reproducible. (4) The sandbox shows the exact effect before publishing.
**Technologies:** Per the §4 stack; no new dependencies. **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Rule model, authoring UI, sandbox, seed rules for the top 20 drugs.
**Effort:** 8 days. **Risks:** **This is where the D-22 decision becomes concrete.** If the curated approach proves too slow to author, the licensed-database conversation happens here rather than after go-live.
**Open decisions:** **D-22, D-23.**

---

### CP78 · Deterministic Medication Safety Engine
**Objective:** §7.2's Medication Safety Engine — "proposed drugs × renal function × allergies × contraindications × current medications. Not generative."
**Why:** The blueprint's most explicit safety constraint. This engine, not the AI, is the authority on prescribing safety.
**Scope:** Evaluation of a draft prescription against all rule types · allergy matching **including cross-class reactivity** · duplicate therapy detection (same generic, same class) · interaction checking within the prescription and against current medications from history · contraindication by diagnosis · **explicit coverage reporting** — which drugs had no rules · **fail-closed behaviour**: missing required data produces "cannot verify", never silence · findings ordered by severity with rule citations · sub-second evaluation.
**Out of scope:** Renal dosing detail (CP79) · override workflow (CP82).
**Dependencies:** CP77, CP54.
**Backend:** `medsafety` module. **API:** `POST /prescriptions/{id}/safety-check`. **Frontend:** Findings display in the editor. **Security:** — **Events/Audit:** `SAFETY_CHECK_RUN` with the exact rule versions evaluated.
**Testing:** **A golden test suite of clinical scenarios authored with Dr. Nahid**: penicillin allergy + amoxicillin must BLOCK; metformin + eGFR 25 must BLOCK; two ACE inhibitors must flag duplicate therapy; an unknown drug must report no coverage rather than pass. Plus: missing-data fail-closed tests, performance tests, and rule-version reproducibility tests.
**Manual verification:** Dr. Nahid attempts to prescribe ten deliberately unsafe combinations and confirms every one is caught with the correct severity and a clear message.
**Acceptance criteria:** (1) Every scenario in the golden suite produces the expected finding — **zero false negatives on that suite**. (2) Missing required data produces an explicit "cannot verify safety" finding. (3) Drugs without rules are reported as uncovered, never as safe. (4) Evaluation completes in <500ms. (5) Historical checks are reproducible against the rule versions used at the time.
**Technologies:** Per the §4 stack; no new dependencies. **Database:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Safety engine, golden test suite, coverage reporting.
**Effort:** 10 days. **Risks:** **Highest clinical-risk checkpoint in the system.** Requires Dr. Nahid's direct involvement in test authoring. Coverage limits must be communicated honestly in the UI — an incomplete rule set presented as complete safety checking would be worse than no engine at all.
**Open decisions:** D-22, D-23; cross-reactivity mappings (**clinical**).

---

### CP79 · Renal Dosing Integration
**Objective:** Wire eGFR into prescribing decisions automatically.
**Why:** §7.2 names renal function explicitly; §6.4 already derives eGFR. Diabetes and CKD co-occur constantly, making this the highest-yield dosing rule set for this clinic.
**Scope:** Latest-eGFR resolution with recency rules (**an eGFR from two years ago is not current renal function** — staleness must be handled explicitly) · CKD staging display · eGFR-banded dose rules per drug · automatic warnings on prescription draft · **fail-closed when creatinine is absent for a renally-cleared drug** · a visible renal status indicator on the prescription editor.
**Out of scope:** Dialysis dosing (unless Dr. Nahid confirms it is in scope).
**Dependencies:** CP78, CP43.
**Backend:** Renal resolution + rule application. **Frontend:** Renal status indicator. **Security:** — **Events/Audit:** Renal findings recorded with the eGFR value and date used.
**Testing:** eGFR banding correctness; staleness handling; fail-closed on missing creatinine; scenario tests with Dr. Nahid.
**Manual verification:** Prescribe metformin for a patient with eGFR 25 (must block), eGFR 45 (must warn with dose guidance), and no creatinine on file (must state that safety cannot be verified).
**Acceptance criteria:** (1) The eGFR used is displayed with its date. (2) Stale eGFR (older than the agreed window) triggers an explicit warning. (3) Missing creatinine for a renally-cleared drug fails closed. (4) Banding matches the authored rules exactly.
**Technologies:** Per the §4 stack; no new dependencies. **Database:** — **API:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Renal integration, staging display, rules.
**Effort:** 4 days. **Risks:** Staleness window is a clinical judgement.
**Open decisions:** eGFR recency window (**clinical — proposed: 6 months, requires approval**); dialysis scope.

---

### CP80 · Prescription Draft Model & API
**Objective:** The prescription aggregate, its lifecycle, and its event history.
**Why:** The prescription is the clinic's primary output artefact and the most medico-legally significant object in the system.
**Scope:** `Prescription` aggregate with the status machine DRAFT → QA_REVIEW → SIGNED → PRINTED/DISPENSED (+ CANCELLED, CORRECTED) · items with dose, frequency, duration, route, bilingual instructions, and **the price at the time of prescribing** · legal transitions only · **a signed prescription is immutable; changes require an explicit correction that supersedes it** · full event history · prior-prescription carry-forward with explicit confirmation.
**Out of scope:** Editor UI (CP81) · signing (CP84) · printing (CP89).
**Dependencies:** CP75, CP23.
**Backend:** `prescription` module. **Database:** Prescription tables (projections) with the ledger as truth. **API:** Draft CRUD, item operations. **Security:** RBAC — only prescribers create; **nobody edits a signed prescription**. **Events/Audit:** `PRESCRIPTION_CREATED`, `_ITEM_ADDED/MODIFIED/REMOVED`, `_CORRECTED`, `_CANCELLED`.
**Testing:** State machine including every illegal transition; immutability of signed prescriptions; carry-forward correctness; price capture at draft time.
**Manual verification:** Attempt to modify a signed prescription through the API and confirm rejection with a clear message pointing to the correction path.
**Acceptance criteria:** (1) Signed prescriptions cannot be modified by any path. (2) Every item change is an event with attribution. (3) The price at prescribing time is captured and never back-updated. (4) Illegal transitions are rejected.
**Technologies:** Per the §4 stack; no new dependencies. **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Prescription module, state machine, API.
**Effort:** 7 days. **Risks:** Correction semantics for prescriptions are subtle (a corrected prescription may already be dispensed) — define the rules with Dr. Nahid explicitly.
**Open decisions:** Correction policy after dispensing (**clinical/operational**).

---

### CP81 · Prescription Editor UI
**Objective:** The screen where Dr. Nahid actually prescribes — it must be faster than writing on paper.
**Why:** If prescribing is slower than paper, the system fails at its central promise regardless of everything else.
**Scope:** Fast item entry via the CP76 autocomplete · dose/frequency/duration with **smart defaults per drug** and keyboard-driven entry · **live safety findings from CP78 as items are added, not on submit** · instruction templates with bilingual text · carry-forward from the previous prescription with one-click confirm · reorder and remove · live preview of the printed output · draft autosave · keyboard shortcuts throughout.
**Out of scope:** AI drafting integration (CP82) · signing (CP84).
**Dependencies:** CP80, CP76, CP10.
**Frontend:** Editor. **API:** Draft operations + live safety check. **Security:** RBAC-gated to prescribers. **Events/Audit:** Every edit is an event.
**Testing:** Component and E2E tests; keyboard-only completion; live safety-check debouncing; autosave and recovery.
**Manual verification:** **Dr. Nahid writes ten real prescriptions and compares the time against writing them by hand.** If it is slower, this checkpoint iterates until it is not.
**Acceptance criteria:** (1) A typical 4-item prescription is completed in under **90 seconds** (*proposed target — validated against Dr. Nahid's paper baseline*). (2) Safety findings appear within 500ms of adding an item. (3) The editor is fully keyboard-operable. (4) A browser crash loses no more than the last few seconds of work. (5) The preview matches the printed output.
**Technologies:** Per the §4 stack; no new dependencies. **Backend:** — **Database:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Prescription editor, preview, shortcuts.
**Effort:** 10 days. **Risks:** **The most iterated UI in the system.** Budget for at least two redesign rounds with real use.
**Open decisions:** Default dose/frequency per drug (**clinical content**).

---

### CP82 · AI Draft Integration & Per-Item Decisions
**Objective:** §3 Step 9 — the physician approves or modifies every AI suggestion; the AI cannot prescribe.
**Why:** The explicit safety boundary, and the mechanism that later teaches the system Dr. Nahid's prescribing patterns (§8).
**Scope:** AI-drafted items from CP71 rendered in the editor **visually distinct from physician-entered items** · per-item accept / edit / reject with the decision recorded · **rejection reason capture (optional but encouraged)** · a running decision trail feeding future pattern learning · **the safety engine runs on the final prescription regardless of AI involvement** · an unmistakable rule: nothing AI-suggested enters a prescription without an explicit human action.
**Out of scope:** Learning from the decision trail (Phase 3).
**Dependencies:** CP81, CP71, **D-28**.
**Frontend:** AI item treatment and decision controls. **Backend:** Decision recording. **AI:** Consumes CP71 output. **Security:** — **Events/Audit:** `AI_SUGGESTION_ACCEPTED/EDITED/REJECTED` per item.
**Testing:** A test asserting no AI item can reach SIGNED status without an explicit accept event; decision trail completeness; visual distinction verified by snapshot.
**Manual verification:** Confirm that ignoring the AI panel entirely produces a prescription with no AI items, and that an accepted item is recorded as accepted with attribution.
**Acceptance criteria:** (1) No AI-suggested item appears in a signed prescription without an explicit accept or edit event. (2) AI items are visually distinct until accepted. (3) Every decision is recorded with the item and the physician. (4) The safety engine evaluates the final content regardless of origin.
**Technologies:** Per the §4 stack; no new dependencies. **Database:** — **API:** — **Mobile:** — **OCR/NLP:** —
**Deliverables:** AI draft integration, decision capture.
**Effort:** 6 days. **Risks:** Automation bias — accepting AI suggestions reflexively. Mitigate with the visual distinction and by monitoring accept rates in QA.
**Open decisions:** **D-28 (approval granularity).**

---

### CP83 · QA Engine & Fail-Closed Clearance (Step 10)
**Objective:** §3 Step 10's automated checklist and the fail-closed gate before printing.
**Why:** A named station and a named non-negotiable principle ("a file cannot close with missing mandatory data"). It is also what guarantees research-grade completeness (§12.2).
**Scope:** Automated checks: drug interactions and duplicates (from CP78), **missing counseling ticks**, **missing HbA1c recorded or ordered for a diabetic**, missing mandatory station data, missing allergy status, incomplete diagnosis coding · configurable rule set per diagnosis · QA officer review screen with findings · **outcome: CLEARED (printing enabled) or BOUNCED (returned to a named station with a reason)** · bounce routing and notification · consultant override with reason.
**Out of scope:** Nightly retrospective auditing (CP135).
**Dependencies:** CP80, CP57.
**Backend:** QA rule engine. **Database:** `qa_reviews`, rule config. **API:** Run checks, record outcome. **Frontend:** QA review screen. **Mobile:** Bounce notifications to the responsible station. **Security:** QA role; consultant override with step-up. **Events/Audit:** `PRESCRIPTION_QA_CLEARED/BOUNCED`, overrides recorded.
**Testing:** Each rule tested individually; the fail-closed gate (printing must be impossible without clearance, enforced server-side); bounce routing; override path.
**Manual verification:** Attempt to print a diabetic patient's prescription with no HbA1c recorded or ordered; confirm blocking; order the HbA1c; confirm clearance.
**Acceptance criteria:** (1) Printing is impossible without QA clearance, enforced server-side. (2) A diabetic file without HbA1c recorded or ordered cannot clear. (3) Bounces route to the correct station with a specific reason. (4) Overrides require elevated permission, a reason, and are audited. (5) The rule set is configurable without a code release.
**Technologies:** Per the §4 stack; no new dependencies. **AI:** — **OCR/NLP:** —
**Deliverables:** QA engine, review screen, bounce workflow.
**Effort:** 8 days. **Risks:** Too many QA rules create bottlenecks on a busy day — start with the named ones and add deliberately.
**Open decisions:** The full QA rule set beyond the named ones (**clinical**).

---

### CP84 · Digital Signature & Signing Flow
**Objective:** §2 and §7.3 — every prescription carries the physician's digital signature.
**Why:** The medico-legal core of the prescription, and the technical foundation for the anti-tamper intent in §9.3.
**Scope:** Canonical serialisation of the prescription (deterministic, versioned) · **signing via Cloud KMS with a non-exportable key** · signature and key ID stored in the ledger · **step-up 2FA required to sign** · a signature verification endpoint and CLI · signature image for human readability (separate from the cryptographic signature) · signed prescriptions become immutable · the physician's signing action recorded as an event.
**Out of scope:** CA-issued qualified certificates (D-04 Option B — upgrade path preserved).
**Dependencies:** CP83, CP17, **D-04**.
**Backend:** Signing module. **Database:** Signature fields on the prescription projection + ledger. **API:** Sign, verify. **Frontend:** Signing flow with step-up. **Security:** Private key never leaves KMS; step-up mandatory; a signature covering a canonical payload, not a rendered image. **Events/Audit:** `PRESCRIPTION_SIGNED` with signature, algorithm, key ID and canonical hash.
**Testing:** Signature verification round trip; **tamper detection** (modify one character of the payload and confirm verification fails); step-up enforcement; canonicalisation stability across versions and platforms.
**Manual verification:** Sign a prescription, verify it, then alter a stored field directly in the database and confirm verification now fails.
**Acceptance criteria:** (1) Every signed prescription verifies against its stored signature. (2) Any modification breaks verification, proven by test. (3) Signing requires step-up 2FA. (4) The signing key is non-exportable. (5) Signature and key ID are in the immutable ledger.
**Technologies:** Per the §4 stack; no new dependencies. **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Signing module, verification tooling, signing UI.
**Effort:** 7 days. **Risks:** Canonicalisation instability would break historical verification — pin the algorithm, version it, and test across platforms.
**Open decisions:** **D-04 (legal status of the signature).**

---

### CP85 · QR Verification & Public Verify Page
**Objective:** §9.1's QR verification — anyone holding the paper can confirm it is genuine and unaltered.
**Why:** Directly supports §9.3's anti-tamper intent, and is a visible credibility marker for DTHC.
**Scope:** QR encoding a verification token (**an opaque token, never patient data**) · a public verification page showing **minimum necessary information** — issuing physician, issue date, prescription ID, verification status, and item count — with **no clinical detail exposed publicly** · rate limiting and abuse protection · optional patient-side deeper access behind an additional factor (e.g. date of birth) if Dr. Nahid wants it · verification attempts logged.
**Out of scope:** Public access to full prescription content (**not recommended; requires explicit decision**).
**Dependencies:** CP84.
**Backend:** Verification endpoint. **Frontend:** Public page (the only unauthenticated surface in the system besides login). **Security:** **This is the system's only public attack surface** — strict rate limiting, no enumeration (tokens are high-entropy), no PHI, WAF rules, and separate monitoring. **Events/Audit:** Verification attempts logged.
**Testing:** Token entropy and enumeration resistance; rate limiting; a test asserting no PHI appears in the public response; QR scanning on real phones.
**Manual verification:** Print a prescription, scan the QR with three different phones, and confirm the verification page loads quickly and shows nothing sensitive.
**Acceptance criteria:** (1) The QR resolves on standard phone cameras. (2) The public page exposes no clinical information. (3) Tokens are not enumerable. (4) The endpoint is rate-limited and monitored. (5) A tampered prescription shows as unverified.
**Technologies:** Per the §4 stack; no new dependencies. **Database:** — **API:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Verification endpoint, public page, QR generation, security review.
**Effort:** 4 days. **Risks:** **Public endpoint security** — include it explicitly in the CP94 penetration test scope.
**Open decisions:** How much information the public page shows (**Dr. Nahid's decision; recommendation: minimum**).

---

### CP86 · Drug Warning Library [R-12]
**Objective:** §9.2's physician-curated warning library, auto-attaching the right warning to the right drug in both languages.
**Why:** A named requirement with three seed entries already specified by Dr. Nahid.
**Scope:** Warning model per generic or class (bilingual text, print flag, severity, display context) · authoring UI with approval workflow and versioning · **automatic attachment when a matching drug is prescribed** · rendering in the editor, on the printed prescription, and in patient instructions · **the three seed warnings from §9.2** (GLP-1 first-dose GI effects; CNS drugs never stopped abruptly with physician-only dose changes; hypnotics PRN only) · extensibility for every commonly prescribed endocrine drug.
**Out of scope:** The warning content beyond the seeds (**D-54, Dr. Nahid authors**).
**Dependencies:** CP75, **D-54**.
**Backend:** Warning module + matching. **Database:** `drug_warnings` with versions. **API:** CRUD + resolution for a prescription. **Frontend:** Authoring UI; display in editor and print. **Security:** Publishing requires physician approval; every version retained. **Events/Audit:** Warning versions attached to the prescription are recorded, so it is always knowable which warning text a patient actually received.
**Testing:** Matching correctness (class-level and generic-level); bilingual rendering; version pinning on issued prescriptions.
**Manual verification:** Prescribe semaglutide and confirm the GLP-1 warning attaches automatically and prints correctly in Bangla and English.
**Acceptance criteria:** (1) Prescribing a drug with a warning attaches it automatically. (2) Both languages render correctly on screen and on paper. (3) The exact warning version issued to a patient is recorded permanently. (4) Dr. Nahid can author and publish a new warning without a developer.
**Technologies:** Per the §4 stack; no new dependencies. **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Warning library, authoring UI, seed warnings.
**Effort:** 5 days. **Risks:** Content authoring is the dependency (**D-54**).
**Open decisions:** **D-54 (warning content).**

---

### CP87 · Prescription Graphs & Gradient Bars [R-11]
**Objective:** §9.1's "graphs on the prescription" — trend sparklines and full-colour gradient bars showing where the patient sits.
**Why:** A distinctive named requirement, and the mechanism that makes abstract numbers tangible for patients.
**Scope:** Sparklines for HbA1c, weight and BP over the last N visits · **gradient bars positioning current BMI and HbA1c against target/risk bands** with the patient's marker · colour coding per §9.1 (red = danger, green = target achieved) · **print-optimised rendering** (vector, colour-managed, legible at actual print size) · screen versions for the dashboard · graceful handling of sparse data (a first-visit patient has no trend — the design must not look broken).
**Out of scope:** The full print layout (CP89).
**Dependencies:** CP37, CP09.
**Frontend:** Chart components (screen + print variants). **Backend:** Series endpoints. **Security:** —
**Testing:** Visual regression at print resolution; sparse-data cases (0, 1, 2 data points); colour accuracy in CMYK; legibility at actual size.
**Manual verification:** **Print the graphs on the clinic's actual printer at actual size and confirm a patient with reading glasses can interpret them.** Screen review is insufficient.
**Acceptance criteria:** (1) Graphs are legible on paper at actual print size. (2) Colour coding follows §9.1's convention exactly. (3) Sparse data renders sensibly with an explanatory label. (4) Print output is vector, not a raster screenshot.
**Technologies:** Per the §4 stack; no new dependencies. **Database:** — **API:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Events/Audit:** —
**Deliverables:** Chart components, print variants, printed proof.
**Effort:** 6 days. **Risks:** Screen-to-print colour and size differences — proof on paper early.
**Open decisions:** Target/risk band values for the gradient bars (**clinical**).

---

### CP88 · Patient-Reported Improvement Score [R-11]
**Objective:** The 1–10 "how much better do you feel than before" score, captured each visit, printed, and plotted over time.
**Why:** A named requirement, and a genuinely valuable patient-reported outcome for §12's research.
**Scope:** Capture at the appropriate station (checkout or education — **to be confirmed operationally**) with a large, simple 1–10 selector suitable for low-literacy patients (numbers plus faces or a colour gradient) · storage as an observation (category PRO) · trend display on the dashboard and on the printed prescription · availability in research extracts.
**Out of scope:** Full PROM instruments (a Phase 3 consideration).
**Dependencies:** CP42.
**Mobile:** Capture screen. **Frontend:** Trend display. **Backend:** PRO observation. **Events/Audit:** Attributed like any observation, with the capturing operator recorded (relevant because the operator asking may influence the answer).
**Testing:** Capture and trend correctness; rendering in Bangla; accessibility for low-literacy users.
**Manual verification:** Capture the score with a real patient and confirm the interface is understood without explanation.
**Acceptance criteria:** (1) The score is captured in one interaction. (2) It plots over time on the dashboard and prescription. (3) It appears in research extracts. (4) The interface is comprehensible without literacy (verified with real patients).
**Technologies:** Per the §4 stack; no new dependencies. **Database:** Uses the CP42 observation tables; no new schema. **API:** Uses the generic observation write and query API from CP42. **AI:** — **OCR/NLP:** — **Security:** No new attack surface; existing controls apply.
**Deliverables:** Score capture, trend display.
**Effort:** 3 days. **Risks:** Score interpretation varies by who asks and how — standardise the question wording in both languages (**Dr. Nahid authors**).
**Open decisions:** Capture station and exact question wording (**clinical/operational**).

---

### CP89 · A4 Print Pipeline — Front Page
**Objective:** §9.1's front page, produced reliably on the clinic's four-colour laser printer.
**Why:** "The printed A4 sheet is the clinic's primary physical interface with the patient" — the highest-visibility output in the entire system.
**Scope:** Print rendering (technology chosen by benchmark: headless Chromium vs Typst) · the full §9.1 layout — clinic header, demographics, ICD-coded diagnoses, structured Rx with bold medicine names and distinct dosages, lifestyle advice, QR, graphs, dual units, colour coding · **typography chosen for readability by visually impaired patients** · Bangla patient-facing text · deterministic pagination · PDF stored immutably and linked to the prescription · reprint tracking (`PRESCRIPTION_PRINTED` events with count).
**Out of scope:** Back page (CP90).
**Dependencies:** CP84, CP86, CP87, **D-58**.
**Backend:** Render service (async job) + PDF storage. **API:** Render and download. **Frontend:** Print action with preview. **Security:** PDFs in the `DOCUMENT` data class; signed short-TTL download URLs; every print audited. **Events/Audit:** Print events with count and actor.
**Testing:** Render determinism (the same prescription must produce a byte-identical PDF); pagination with 1, 5 and 15 items; Bangla text rendering including conjuncts; colour output on the target printer; render latency.
**Manual verification:** **Print 20 real prescriptions on the clinic printer.** Dr. Nahid signs off on paper, not on screen. Include a long prescription, a paediatric one, and one with maximum warnings.
**Acceptance criteria:** (1) Output matches the approved design on the actual clinic printer. (2) Bangla renders correctly including conjuncts and numerals. (3) Rendering completes in <5s. (4) The same prescription always renders identically. (5) Every print is recorded. (6) Dr. Nahid's written sign-off on printed samples.
**Technologies:** Per the §4 stack; no new dependencies. **Database:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Print pipeline, layout templates, printed proofs, sign-off record.
**Effort:** 10 days. **Risks:** **Bangla typography in PDF is a known source of pain** (conjunct rendering, font embedding). Prototype this early — ideally a spike during CP09 — rather than discovering it here.
**Open decisions:** **D-58 (printer)**; final layout approval (**Dr. Nahid**).

---

### CP90 · Back Page — Evaluation & Rationale [R-13]
**Objective:** §9.3's reverse page: clinical assessment, plan, research/guideline rationale, and the standing notice to other physicians.
**Why:** Dr. Nahid's stated purpose — that any other doctor reading it understands the logic completely and has no scope to tamper with the therapy.
**Scope:** Structured back-page composition (assessment, plan and next steps, rationale per therapeutic choice with guideline citation where applicable) · **AI-drafted rationale that the physician edits and approves** (never printed unreviewed) · the reserved block for the non-endocrinologist notice with **Dr. Nahid's exact wording in Bangla and English** (D-06) · duplex print pipeline · a professional letter-style layout rather than a data table.
**Out of scope:** Automatic guideline citation generation (Phase 3, CP137).
**Dependencies:** CP89, **D-06**.
**Backend:** Back-page composition. **Frontend:** Back-page editor with preview. **AI:** Draft rationale via the gateway; **never printed without physician approval**. **Security:** — **Events/Audit:** Back-page content stored with the prescription; approval recorded.
**Testing:** Duplex rendering; overflow handling for long rationales; a test asserting unapproved AI text cannot print.
**Manual verification:** Print duplex; confirm alignment; Dr. Nahid confirms a colleague reading the back page would understand his reasoning.
**Acceptance criteria:** (1) The back page prints correctly duplex and aligned. (2) The standing notice appears in both languages with the approved wording. (3) AI-drafted rationale requires explicit approval before printing. (4) Long content paginates without truncation.
**Technologies:** Per the §4 stack; no new dependencies. **Database:** — **API:** — **Mobile:** — **OCR/NLP:** —
**Deliverables:** Back-page template, editor, notice block.
**Effort:** 6 days. **Risks:** **D-06 wording is a dependency**; the AI-drafted rationale must never be an unreviewed default.
**Open decisions:** **D-06 (notice wording).**

---

### CP91 · Bilingual Patient Instructions & Content Library
**Objective:** The versioned bilingual content that patients actually read.
**Why:** D-11's decision — patient-facing Bangla must come from an approved library, never from run-time generation.
**Scope:** Instruction template library (per drug, per condition, per device) in Bangla and English · authoring UI with approval and versioning · **variable substitution** (dose, frequency, timing) with validated Bangla grammar for the substituted forms · attachment to prescriptions · **QR links to localized instruction videos** (§9.1) with a video registry · reuse in SMS templates (Phase 2).
**Out of scope:** Producing the videos themselves (a clinic content project).
**Dependencies:** CP86, CP89, **D-11**.
**Backend:** Content module. **Database:** Versioned templates. **Frontend:** Authoring UI. **Security:** Approval required before use. **Events/Audit:** The exact content version given to a patient is recorded permanently.
**Testing:** Substitution correctness in Bangla (grammatical agreement is the tricky part); version pinning; QR link resolution.
**Manual verification:** A Bangla-speaking staff member reads ten generated instruction sets and confirms they are natural and unambiguous.
**Acceptance criteria:** (1) All patient-facing text comes from approved templates. (2) Variable substitution produces grammatically correct Bangla. (3) The version issued to each patient is recorded. (4) QR video links resolve correctly.
**Technologies:** Per the §4 stack; no new dependencies. **API:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Content library, authoring UI, video registry.
**Effort:** 5 days. **Risks:** Bangla grammatical agreement under substitution — design templates to avoid fragile constructions.
**Open decisions:** **D-11**; video content availability.

---

### CP92 · Prescription Education Station (Step 11)
**Objective:** Station 11 — record demonstrated technique and prior compliance.
**Why:** A named station whose stated purpose is fewer treatment failures from technique errors; also a research variable.
**Scope:** Device/insulin technique demonstration checklist (per device type) · competency rating · prior compliance assessment · **linkage to the prescribed devices so the right checklist appears automatically** · re-education flagging when technique is poor · handover of educational materials recorded.
**Out of scope:** Video content production.
**Dependencies:** CP42, CP11.
**Mobile:** Education station screens. **Backend:** Structured capture. **Events/Audit:** Attributed per item.
**Testing:** Checklist selection by prescribed device; competency capture; re-education flagging.
**Manual verification:** Prescribe an insulin pen and confirm the correct technique checklist appears at the education station without manual selection.
**Acceptance criteria:** (1) The checklist matches the prescribed device automatically. (2) Competency is recorded and visible to the physician next visit. (3) Poor technique raises a re-education flag. (4) Data is structured for §12 analysis.
**Technologies:** Per the §4 stack; no new dependencies. **Database:** Uses the CP42 observation tables; no new schema. **API:** Uses the generic observation write and query API from CP42. **Frontend:** Captured values surface on the physician dashboard (CP73). **AI:** — **OCR/NLP:** — **Security:** No new attack surface; existing controls apply.
**Deliverables:** Education station app, checklists.
**Effort:** 4 days. **Risks:** Checklist content is physician-authored (**content dependency**).
**Open decisions:** Device checklist content (**clinical**).

---

### CP93 · Performance Hardening & Load Testing
**Objective:** Prove the system holds up at the clinic's real and projected load before real patients depend on it.
**Why:** §1's requirement that "everything must be fast and flawless, with results available instantly upon input" is a performance requirement, and it needs measurement.
**Scope:** k6 load scenarios modelling a real clinic day (D-59's parameters) · concurrent operator simulation · WebSocket connection load · database query profiling and index tuning · N+1 elimination · API response budgets per endpoint class · mobile app performance on the low-end target device · **synthesis pipeline load testing to validate or revise the §7.1 SLA** · a documented performance baseline for regression detection.
**Out of scope:** Multi-branch scale (CP153) and its load testing (CP157).
**Dependencies:** All Phase 1 checkpoints. **Technologies:** k6, pg_stat_statements, profiling.
**Backend/Database/Frontend/Mobile:** Optimisation across the board. **Security:** Rate limits validated under load.
**Testing:** This checkpoint is testing.
**Manual verification:** Run a simulated full clinic day at 150% of expected volume and confirm the system stays responsive throughout.
**Acceptance criteria:** (1) p95 API latency within documented budgets at 150% of expected peak. (2) Realtime updates stay <1s at peak. (3) The §7.1 synthesis SLA is met at peak load, or the SLA is formally revised with Dr. Nahid's agreement. (4) No N+1 queries on the hot paths. (5) A performance baseline is recorded for future regression comparison.
**Backend:** — **Database:** — **API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Events/Audit:** —
**Deliverables:** Load test suite, performance report, optimisations, baseline.
**Effort:** 8 days. **Risks:** Discovering an architectural bottleneck late — mitigate by running a scaled-down load test at CP73 rather than waiting.
**Open decisions:** **D-59 (real clinic volumes)** — without these the load model is guesswork.

---

### CP94 · Security Review & External Penetration Test
**Objective:** Independent verification before real patient data enters the system.
**Why:** §1's premise that every patient's data matters. A first go-live with patient data and no external security assessment is an avoidable risk.
**Scope:** Internal review against the §12 architecture · dependency and container scanning with remediation · **external penetration test** covering authentication, authorisation (especially the RBAC field-redaction claims), the public verify page, the sync API, file upload, and the mobile app · remediation of findings by severity · retest of critical and high findings · a documented security posture statement.
**Out of scope:** Ongoing programme (established here, run continuously).
**Dependencies:** CP93, **D-50**.
**Security:** The whole checkpoint. **Events/Audit:** Findings tracked to closure.
**Testing:** Penetration test plus automated security regression tests for every fixed finding — a fixed vulnerability that regresses is worse than one never found.
**Manual verification:** Review the report with the tester; confirm every critical and high finding is closed and retested.
**Acceptance criteria:** (1) No unresolved critical or high findings at go-live. (2) Every finding has a regression test. (3) The RBAC field-redaction claims are independently verified. (4) Mobile local storage encryption is independently verified. (5) A written security posture statement exists.
**Technologies:** Per the §4 stack; no new dependencies. **Backend:** — **Database:** — **API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Pentest report, remediation record, regression tests, posture statement.
**Effort:** 8 days (plus external vendor time). **Risks:** Findings could require architectural change — schedule the test with slack before go-live, not the week before.
**Open decisions:** **D-50 (vendor and budget).**

---

### CP95 · Pilot Deployment, Training & Phase 1 Sign-Off
**Objective:** Go live with real patients, safely, with staff who know how to use the system.
**Why:** §15.3's phase acceptance: features live + audit trail verified end to end + Dr. Nahid's sign-off.
**Scope:** Production deployment with the full runbook · device provisioning and enrolment for every clinic phone · **staff training in Bangla with printed quick-reference cards per station** · a phased rollout (recommended: 2 stations for a week, then 6, then all 12 — a big-bang cutover on a live clinic floor is an unnecessary risk) · **parallel paper running for the first period** so nothing is lost if the system misbehaves · daily issue triage during the pilot · an end-to-end audit trail verification with Dr. Nahid · a rollback plan.
**Out of scope:** Phase 2 features.
**Dependencies:** CP94.
**All layers:** Deployment and verification. **Security:** Production secrets, monitoring and alerting live and tested. **Events/Audit:** End-to-end verification is an explicit acceptance item.
**Testing:** Smoke tests in production; a full patient journey with a synthetic patient in production before real patients; monitoring verification.
**Manual verification:** **Dr. Nahid walks one real patient (with consent) through all 12 stations and verifies the complete audit trail end to end** — every value, every operator, every device, every timestamp.
**Acceptance criteria:** (1) All 12 stations operate with real patients. (2) The end-to-end audit trail is verified by Dr. Nahid on a real journey. (3) Staff can complete their station's work unaided after training. (4) No critical defects open (§15.3: "no phase closes with open critical defects"). (5) Rollback is documented and rehearsed. (6) Written Phase 1 sign-off.
**Technologies:** Per the §4 stack; no new dependencies. **Backend:** — **Database:** — **API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —
**Deliverables:** Production system, training materials in Bangla, runbooks, rollback plan, sign-off record.
**Effort:** 10 days. **Risks:** **Staff adoption is the biggest risk in the entire programme** — the software can be perfect and still fail if the floor rejects it. Involve operators from Phase 0 usability testing onward, not at training.
**Open decisions:** Rollout sequence and pilot duration (**operational**).

---

## PHASE 2 — MEMORY & RELATIONSHIP

*Format compacts further for Phases 2–4: all fields present, expressed tightly. Phase 1's detail level will be restored for each of these checkpoints at the time it is scheduled, once Phase 1 learning is available.*

---

### CP96 · Document Upload, Storage & Metadata Pipeline
**Objective:** Ingest every document a patient brings (§6.1 "scan every single document, without exception"). **Why:** Foundation for all of §6; nothing in the records pipeline works without reliable ingest. **Scope:** Multi-page document model · direct-to-storage upload with signed URLs · metadata (source facility, declared type, page count, upload attribution) · virus/type validation · immutable original retention · document listing per patient. **Out of scope:** Classification (CP100) · OCR (CP101). **Dependencies:** CP34, CP23. **Technologies:** GCS, signed URLs. **Backend:** `records` module. **Database:** `medical_documents`, `document_pages`. **API:** Upload-URL, attach, list. **Frontend:** Document list + viewer. **Mobile:** Upload from capture. **AI/OCR:** — **Security:** `DOCUMENT` data class, CMEK, signed short-TTL access, all access audited. **Events/Audit:** `DOCUMENT_UPLOADED`. **Testing:** Large multi-page upload; interrupted upload resume; access control; storage lifecycle. **Manual verification:** Upload a 12-page report from a phone on clinic Wi-Fi; confirm all pages arrive in order and are viewable. **Acceptance criteria:** (1) Documents up to a defined page/size limit upload reliably on clinic Wi-Fi. (2) Originals are immutable and versioned. (3) No document is publicly accessible. (4) Every access is audited. **Deliverables:** Ingest pipeline, document viewer. **Effort:** 6 days. **Risks:** Upload reliability on poor connections — resumable uploads required. **Open decisions:** Page/size limits; retention (D-05).
**AI:** — **OCR/NLP:** None yet — ingest only; OCR begins at CP101.

---

### CP97 · Document Capture UX (Multi-Page Mobile Scan)
**Objective:** Make scanning a stack of paper fast enough that "scan everything" actually happens. **Why:** §6.1's rule fails in practice if capture is slow — this is a workflow risk, not a technical one. **Scope:** Rapid multi-page capture with auto edge detection, auto-crop, perspective correction · page reordering and re-shoot · quality feedback before accepting a page · batch upload with progress · offline queueing. **Out of scope:** OCR feedback (CP105). **Dependencies:** CP96, CP11. **Backend/Database/API:** — **Mobile:** Capture flow. **AI/OCR:** On-device quality scoring only. **Security:** Images never persist in the device gallery. **Events/Audit:** Capture attributed. **Testing:** Device tests across lighting conditions; queue behaviour offline. **Manual verification:** Scan a real 10-document patient bundle and time it. **Acceptance criteria:** (1) 10 pages captured in under **3 minutes** (*proposed*). (2) Poor-quality pages are flagged before upload. (3) Capture works fully offline. (4) No images leak to the device gallery. **Deliverables:** Capture flow. **Effort:** 6 days. **Risks:** Records officer throughput is the real constraint — validate with the actual staff member. **Open decisions:** None.
**Technologies:** Expo camera + image manipulation. **Backend:** — **Database:** — **API:** Upload endpoints from CP96. **Frontend:** — **AI:** — **OCR/NLP:** On-device quality scoring only.

---

### CP98 · OCR Bake-Off Spike — Resolves D-16
**Objective:** Choose the OCR architecture on measured evidence, not vendor claims. **Why:** §11.2 — the blueprint leaves this open (§16.4) and the wrong choice is expensive to unwind. **Scope:** ~200 real anonymised DTHC documents across the §11.1 distribution · evaluate ≥3 candidates (a cloud document AI, a self-hosted OSS stack, a VLM extraction approach) · measure character accuracy, field-extraction accuracy on priority analytes, date-extraction accuracy, cost per page, latency, and data-residency implications · a written recommendation with the evidence. **Out of scope:** Production integration (CP101). **Dependencies:** CP96. **Technologies:** Candidates per §11.2. **Backend:** Throwaway harness. **AI/OCR:** The whole checkpoint. **Security:** Test documents de-identified before leaving the clinic. **Testing:** Measurement is the deliverable. **Manual verification:** Dr. Nahid reviews the extraction outputs for ten documents and judges whether the quality is clinically usable. **Acceptance criteria:** (1) ≥3 candidates measured on the same corpus with the same metrics. (2) Bangla, English and mixed-script cases each measured separately. (3) Cost per page and latency reported per candidate. (4) A written recommendation, **and D-16 formally closed**. **Deliverables:** Evaluation corpus, benchmark harness, comparison report, decision record. **Effort:** 10 days. **Risks:** No candidate meets the quality bar — in which case the fallback (structured manual entry of key values with the image attached) must be planned rather than improvised. **Open decisions:** **This checkpoint exists to close D-16, D-17.**
**Database:** Throwaway benchmark tables. **API:** — **Frontend:** — **Mobile:** — **AI:** VLM extraction is one of the evaluated candidates. **OCR/NLP:** The entire checkpoint. **Events/Audit:** Benchmark results recorded as a decision artefact.

---

### CP99 · Image Preprocessing Service
**Objective:** Turn phone photos into OCR-ready images. **Why:** §11.3 step 3 — preprocessing quality dominates OCR accuracy on real-world captures. **Scope:** Python FastAPI service · deskew, dewarp, crop, denoise, shadow removal, adaptive binarisation, DPI normalisation, orientation detection · per-page quality score with a **re-scan request threshold** · before/after diagnostics retained for tuning. **Out of scope:** OCR itself. **Dependencies:** CP98. **Technologies:** Python, OpenCV. **Backend:** ML service + Go client. **Database:** Quality scores on pages. **API:** Internal preprocessing endpoint. **Mobile:** Surfaces re-scan requests. **Security:** Service is internal-only, no public ingress; documents streamed, not persisted in the service. **Events/Audit:** Preprocessing results recorded. **Testing:** Fixed corpus with before/after accuracy comparison proving preprocessing improves downstream OCR. **Manual verification:** Feed deliberately poor photos (skewed, shadowed, low light) and inspect the outputs. **Acceptance criteria:** (1) Measured OCR accuracy improvement over raw images on the CP98 corpus. (2) Poor pages are flagged for re-scan **before** OCR cost is incurred. (3) Processing <3s per page. **Deliverables:** Preprocessing service, quality scoring. **Effort:** 7 days. **Risks:** Over-processing degrading quality — always retain and OCR the original as a comparison. **Open decisions:** Re-scan threshold (*proposed, tuned on data*).
**Frontend:** Quality warnings surface at capture and in the queue. **AI:** — **OCR/NLP:** Preprocessing stage of the OCR pipeline.

---

### CP100 · Document Classification & Handwritten Exclusion [R-09]
**Objective:** Route printed documents into the pipeline and handwritten prescriptions out of it, per §6.1. **Why:** A named hard rule; misrouting a handwritten prescription into structured extraction is exactly the failure Dr. Nahid excluded. **Scope:** **Operator declaration is authoritative** (D-18) · an ML classifier as a cross-check that can flag but never override · document types (lab report, imaging, discharge summary, ECG/Echo, prescription-printed, prescription-handwritten, other) · handwritten documents stored, attached, viewable, and **excluded from extraction** · handling of printed documents with handwritten annotations (OCR the printed part, flag the annotation). **Out of scope:** Handwriting OCR (explicitly excluded by the blueprint). **Dependencies:** CP99, **D-18**. **Backend/ML:** Classifier. **Database:** Type and handwritten flag. **Mobile/Frontend:** Declaration UI + disagreement flagging. **AI/OCR:** Classification only. **Security:** — **Events/Audit:** `DOCUMENT_CLASSIFIED`, `DOCUMENT_MARKED_HANDWRITTEN`. **Testing:** Classifier accuracy on a labelled set; a test asserting no handwritten-flagged document ever enters extraction. **Manual verification:** Upload a mixed bundle; confirm handwritten prescriptions are stored as images only and appear correctly in the record. **Acceptance criteria:** (1) Handwritten-declared documents never enter extraction, proven by test. (2) Classifier disagreement with the operator raises a flag, never an override. (3) All handwritten documents remain viewable and attached. **Deliverables:** Classifier, declaration UI, exclusion enforcement. **Effort:** 6 days. **Risks:** Operator misdeclaration — the classifier cross-check is the mitigation. **Open decisions:** **D-18.**
**Technologies:** Python classifier, Go orchestration. **Backend:** Classification orchestration. **API:** Declaration and flag endpoints. **Frontend:** Disagreement flags in the records queue. **Mobile:** Operator declaration at capture. **AI:** Classifier only — advisory, never overriding. **OCR/NLP:** Gatekeeper for the OCR pipeline.

---

### CP101 · OCR Integration
**Objective:** Production OCR using the engine chosen at CP98. **Why:** §6.2's extraction depends on it. **Scope:** Engine adapter behind a stable interface (**so a future engine change is a swap, not a rewrite**) · async OCR jobs with retry · word-level confidence and bounding boxes · layout regions · raw output stored for re-processing without re-OCR cost · cost and latency metering. **Out of scope:** Structured extraction (CP104). **Dependencies:** CP98, CP99. **Backend/ML:** OCR service. **Database:** `ocr_results`. **API:** Internal. **Security:** Residency per D-01; cloud engines receive de-identified pages where feasible. **Events/Audit:** `OCR_COMPLETED`/`OCR_FAILED` with engine and version. **Testing:** Accuracy regression against the CP98 corpus (run on every engine or version change); failure and retry paths; cost accounting. **Manual verification:** Process 20 real documents and compare against the CP98 baseline. **Acceptance criteria:** (1) Production accuracy matches the bake-off baseline within tolerance. (2) Engine and version are recorded on every result. (3) Failures are visible, retried, and never silently partial. (4) The engine is swappable by configuration. **Deliverables:** OCR integration, accuracy regression suite. **Effort:** 8 days. **Risks:** Cloud engine quota and cost at volume. **Open decisions:** Closed by CP98.
**Technologies:** The engine selected at CP98, behind an adapter. **Backend:** OCR orchestration and adapter. **Frontend:** OCR status per document. **Mobile:** — **AI:** — **OCR/NLP:** Core of this checkpoint.

---

### CP102 · Mixed-Script Handling & Numeral/Date Normalisation
**Objective:** Handle the mixed Bangla/English reality of Bangladeshi lab reports correctly. **Why:** §11.2 — this is the specific case most likely to silently corrupt values. **Scope:** Per-block script detection · per-script engine routing if CP98 showed it helps · Unicode NFC normalisation · **Bengali numeral (০–৯) → ASCII conversion with explicit tests** · date parsing across the formats seen locally (dd/mm/yy, dd-mmm-yyyy, Bangla month names) · ambiguous-date detection (03/04/2026) raising a review flag rather than guessing. **Out of scope:** Translation. **Dependencies:** CP101, **D-17**. **Backend/ML:** Normalisation layer. **Testing:** A dedicated corpus of mixed-script documents; numeral conversion tests; a date-parsing test suite with ambiguous cases. **Manual verification:** Process ten mixed-script reports and verify every extracted number and date by hand. **Acceptance criteria:** (1) Bengali numerals convert correctly in 100% of test cases. (2) Ambiguous dates are flagged, never silently interpreted. (3) Mixed-script accuracy is measured and reported separately from single-script. **Deliverables:** Normalisation layer, script routing, date parser. **Effort:** 6 days. **Risks:** **A silent numeral error corrupts a clinical value** — hence explicit tests and human confirmation for rule-driving values (D-19). **Open decisions:** **D-17.**
**Technologies:** Python (normalisation), Go client. **Backend:** Normalisation layer in the ML service. **Database:** Normalised values on extraction records. **API:** Internal only. **Frontend:** Flagged ambiguities surface in the validation queue. **Mobile:** — **AI:** — **OCR/NLP:** Core of this checkpoint — script routing and post-OCR normalisation. **Security:** No new surface. **Events/Audit:** Ambiguity flags recorded on the extraction record.

---

### CP103 · Table & Lab-Panel Extraction
**Objective:** Extract structured rows from lab report tables. **Why:** Most clinical values in printed reports live in tables; free-text extraction alone loses the analyte-value-unit-range relationship. **Scope:** Table detection and cell extraction · header identification · analyte/value/unit/reference-range column mapping · multi-column and multi-panel layouts · footnote and comment handling · confidence per cell. **Out of scope:** Analyte normalisation (CP104). **Dependencies:** CP101. **Backend/ML:** Table extraction. **Database:** Structured table output. **Testing:** Table extraction accuracy on the corpus; complex layout cases; a test asserting a value is never associated with the wrong analyte. **Manual verification:** Extract a complex multi-panel report and verify every row by hand. **Acceptance criteria:** (1) Table extraction accuracy measured and reported on the corpus. (2) A value is never attached to the wrong analyte in the test set. (3) Reference ranges are captured where present. **Deliverables:** Table extraction, evaluation report. **Effort:** 8 days. **Risks:** Layout variety across labs — expect ongoing tuning as new labs appear. **Open decisions:** None.
**Technologies:** Python table-extraction libraries or the chosen engine’s table API. **Backend:** Table extraction stage. **API:** Internal only. **Frontend:** Extracted tables shown in the validation UI. **Mobile:** — **AI:** Optional LLM assist for irregular layouts, schema-constrained. **OCR/NLP:** Core of this checkpoint. **Security:** No new surface. **Events/Audit:** Extraction results recorded with engine version.

---

### CP104 · Medical Entity Extraction & Analyte Dictionary
**Objective:** §6.2's requirement: date, facility, test type, reason, and exact values. **Why:** This is what turns OCR text into clinical data. **Scope:** Document-level entities (date, facility, ordering physician, reason when stated) · **analyte dictionary with local name variants → canonical code (LOINC)** (D-20) · unit normalisation and conversion · narrative-value extraction for imaging reports (**ovarian volume, follicle count, LVEF** — named in §6.4) · plausibility checking against reference ranges (HbA1c 82% is an OCR error) · schema-constrained extraction with confidence per field. **Out of scope:** Red-line rules (CP108). **Dependencies:** CP103, **D-20**. **Backend/ML/AI:** Extraction pipeline via the AI gateway where an LLM is used. **Database:** `extracted_values`, analyte dictionary. **Security:** PHI minimisation per D-08 where cloud models are used. **Events/Audit:** `EXTRACTION_PROPOSED`. **Testing:** Field-level accuracy per analyte class; plausibility rejection tests; narrative extraction tests on real ultrasound reports. **Manual verification:** Dr. Nahid reviews extraction for 20 documents covering the analytes he most relies on. **Acceptance criteria:** (1) Priority analytes (HbA1c, creatinine, glucose, TSH, FT4, lipids) extract with measured accuracy above the agreed bar. (2) Document date accuracy is measured and reported — **it drives the whole chronology**. (3) Implausible values are flagged, never stored silently. (4) The analyte dictionary covers DTHC's common labs. **Deliverables:** Extraction pipeline, analyte dictionary, accuracy report. **Effort:** 10 days. **Risks:** Analyte dictionary completeness is a **content dependency (D-20)**. **Open decisions:** **D-20**; accuracy bars (*proposed values requiring approval*).
**Technologies:** LOINC, schema-constrained extraction. **Backend:** Extraction orchestration. **API:** Internal. **Frontend:** Extracted values in the validation queue. **Mobile:** — **AI:** LLM extraction via the CP70 gateway, schema-validated. **OCR/NLP:** Consumes CP101–CP103 output.

---

### CP105 · Confidence Scoring & Human Validation Queue
**Objective:** §11.4 — the screen where OCR output becomes trustworthy clinical data. **Why:** D-19 requires human confirmation for rule-driving values; without an efficient validation UI this becomes an unmanageable bottleneck. **Scope:** Composite confidence per field (OCR + extraction + plausibility + cross-page consistency) · **threshold routing**: auto-populate above, queue below, **always queue rule-driving values** · validation screen with the document image and the source region highlighted beside the extracted field · keyboard-driven confirm/correct · batch confirm for high-confidence pages · corrections captured as evaluation signal · queue prioritisation by clinical urgency. **Out of scope:** Retraining from corrections (Phase 3). **Dependencies:** CP104, **D-19**. **Backend:** Confidence scoring, queue. **Frontend:** Validation UI. **Security:** Validation is a clinical write with full attribution. **Events/Audit:** `EXTRACTION_CONFIRMED`/`REJECTED`. **Testing:** Threshold routing; confidence calibration measurement; UI throughput measurement. **Manual verification:** A records officer validates 50 fields and reports the experience; measure fields-per-minute. **Acceptance criteria:** (1) Rule-driving values always require confirmation regardless of confidence. (2) An officer validates ≥30 fields per minute on high-confidence pages (*proposed*). (3) Confidence is calibrated (measured reliability curve reported). (4) Every confirmation is attributed. **Deliverables:** Confidence engine, validation UI, calibration report. **Effort:** 8 days. **Risks:** Validation workload exceeding staff capacity — measure early; the human review rate is the honest measure of pipeline quality. **Open decisions:** **D-19 thresholds.**
**Technologies:** Per §4. **Database:** Confidence scores and queue state. **API:** Queue and confirmation endpoints. **Mobile:** — **AI:** — **OCR/NLP:** Confidence layer of the pipeline.

---

### CP106 · Extracted Values → Observations
**Objective:** Land confirmed extractions in the same clinical record as station-entered values, with provenance intact. **Why:** §6.4 — "all extracted values also populate the longitudinal timeline". **Scope:** Confirmed extractions become Observations with `source = OCR`, linked to the document, page and region · **visual distinction from station-entered values everywhere** · effective date from the document date, not the upload date · duplicate detection against existing observations (the same lab entered manually and extracted) · correction path identical to any other observation. **Out of scope:** Chronology assembly (CP107). **Dependencies:** CP105, CP42. **Backend:** Extraction-to-observation mapping. **Database:** Provenance links. **Frontend/Mobile:** Source indicator on values. **Events/Audit:** Observation events with document provenance. **Testing:** Provenance preservation; duplicate detection; effective-date correctness. **Manual verification:** Confirm an extracted HbA1c appears on the timeline at its report date, marked as document-derived, with a one-tap link to the source image. **Acceptance criteria:** (1) Every OCR-derived value links to its source document and region. (2) Effective date is the document date. (3) OCR values are visually distinguishable from entered values. (4) Duplicates are detected and surfaced, not silently double-counted. **Deliverables:** Mapping layer, provenance UI. **Effort:** 5 days. **Risks:** Duplicate counting corrupting research data — detection matters more here than convenience. **Open decisions:** None.
**Technologies:** Per §4. **API:** Observation write with provenance. **Frontend:** Source indicator on values. **Mobile:** Source indicator on values. **AI:** — **OCR/NLP:** Terminal stage of the OCR pipeline. **Security:** OCR-derived writes carry the confirming operator’s attribution.

---

### CP107 · Chronology Engine [R-09]
**Objective:** §6.2's single investigations timeline across all prior providers, in absolute chronological order. **Why:** The core promise of §6 — one clean chronological truth from fragmented paper. **Scope:** Chronology entries per document (date, facility, type, reason, summary) · absolute ordering with **explicit handling of undated documents** (flagged for date entry, never silently placed) · merging with clinic-generated data · per-provider grouping view · rebuild on new document arrival. **Out of scope:** Red-lines (CP108) · narrative summary (CP109). **Dependencies:** CP106. **Backend:** Chronology projection. **Database:** `chronology_entries`. **API:** Chronology query. **Frontend:** Chronology view. **Events/Audit:** `CHRONOLOGY_REBUILT`. **Testing:** Ordering correctness including same-day documents; undated handling; rebuild idempotency. **Manual verification:** Load a patient with documents from six providers over five years; confirm the order is correct and matches the paper when laid out chronologically. **Acceptance criteria:** (1) Documents are ordered by their true clinical date. (2) Undated documents are flagged, never guessed into position. (3) Clinic and external data merge into one timeline. (4) Rebuild is idempotent. **Deliverables:** Chronology engine, chronology view. **Effort:** 7 days. **Risks:** Wrong document dates from OCR silently corrupt the order — this is why CP104 treats date accuracy as the highest-priority extraction metric. **Open decisions:** None.
**Technologies:** Per §4. **Mobile:** Read-only chronology view. **AI:** — **OCR/NLP:** Consumes confirmed extractions. **Security:** RBAC as for any clinical read.

---

### CP108 · Red-Line Abnormality Rule Engine [R-10]
**Objective:** §6.4's red-lined abnormalities so the physician's eye lands on them instantly. **Why:** Named requirement with named seed rules. **Scope:** Deterministic rule engine over extracted and entered values · **seed rules from §6.4**: raised creatinine and/or urinary pus cells with CKD pattern recognition; **eGFR auto-derivation (CKD-EPI 2021) whenever creatinine, age and sex are available**; elevated BP values and series; **PCO ultrasound — ovarian size/volume, cyst/follicle count, with computed interval change across serial scans stated explicitly** · physician-authored extensible rule library with the same authoring pattern as CP77 · severity and rendering (red underline/flag) · rationale on each flag. **Out of scope:** AI-generated interpretation. **Dependencies:** CP107, **D-55**. **Backend:** Rule engine. **Database:** Rule library, `red_line_flags`. **Frontend:** Red-line rendering in chronology, timeline and dashboard. **Security:** — **Events/Audit:** `REDLINE_RAISED` with rule and version. **Testing:** Each seed rule tested with clinical scenarios; **serial-change computation tested with real serial ultrasound data**; false-positive rate measured. **Manual verification:** Dr. Nahid reviews flags on 20 real patients and confirms the flagged items are the ones he would have circled by hand. **Acceptance criteria:** (1) All §6.4 seed rules are implemented and correct. (2) eGFR derives automatically whenever inputs exist. (3) PCO serial change is computed and **stated explicitly as improvement or worsening**. (4) Flags render visibly wherever the value appears. (5) The library is physician-extensible. **Deliverables:** Red-line engine, seed rules, rendering. **Effort:** 8 days. **Risks:** Over-flagging causes the physician to stop looking — measure and tune the flag rate. **Open decisions:** **D-55 (rule content)**; thresholds (*clinical*).
**Technologies:** Per §4. **API:** Flag query endpoints. **Mobile:** Flags visible on mobile patient views. **AI:** — **OCR/NLP:** Operates on OCR-derived and entered values alike.

---

### CP109 · Records Auto-Summary & Synthesis Integration
**Objective:** §6.3's narrative problem history, feeding the physician's one-page synthesis. **Why:** Completes the §6 pipeline and materially improves CP71's output quality. **Scope:** LLM-generated narrative from the assembled chronology ("this patient had these problems, evolving in this order") · **grounded against extracted values** (§10.2) · caching keyed on chronology version so it recomputes only when documents change · integration as an input to the CP71 synthesis · clear AI labelling. **Out of scope:** — **Dependencies:** CP107, CP71. **Backend/AI:** Summary agent via the gateway. **Frontend:** Summary display with expandable source links. **Security:** PHI minimisation. **Events/Audit:** AI interaction recorded. **Testing:** Grounding validation; cache invalidation on new documents; quality evaluation on the frozen set. **Manual verification:** Dr. Nahid reads summaries for ten complex multi-provider patients and judges whether they save him reading time. **Acceptance criteria:** (1) Every claim in the narrative is grounded in an extracted or entered value. (2) The summary regenerates when new documents arrive. (3) It is clearly labelled AI-generated with links to sources. (4) Dr. Nahid confirms it reduces his reading time. **Deliverables:** Summary agent, synthesis integration. **Effort:** 6 days. **Risks:** Narrative quality on sparse or contradictory records. **Open decisions:** None.
**Technologies:** Per §4. **Backend:** Summary job. **Database:** Cached summary per chronology version. **API:** Summary fetch. **Mobile:** — **AI:** Generative summary, grounded and labelled. **OCR/NLP:** Consumes the chronology.

---

### CP110 · Records Viewer & Document Timeline UI
**Objective:** Let the physician move between the narrative, the extracted values, and the original image in one interaction. **Why:** Trust in extracted data requires the source to be one tap away. **Scope:** Document viewer with zoom, rotate, multi-page navigation · **extracted-value overlay on the image showing where each value came from** · chronology view with filters · document lane on the longitudinal timeline (CP74) · quick access from any OCR-derived value to its source region. **Out of scope:** Annotation tools. **Dependencies:** CP107, CP74. **Frontend:** Viewer and integration. **API:** Document and region endpoints. **Security:** Access audited. **Testing:** Viewer performance on large multi-page documents; overlay accuracy; mobile rendering. **Manual verification:** From an HbA1c value on the dashboard, reach the exact highlighted region of the original report in one tap. **Acceptance criteria:** (1) Any OCR-derived value links to its source region in one interaction. (2) Multi-page documents load progressively without blocking. (3) The document lane appears on the timeline. (4) Usable on a tablet. **Deliverables:** Records viewer, timeline integration. **Effort:** 8 days. **Risks:** Large image performance — progressive loading and thumbnails. **Open decisions:** None.
**Technologies:** Per §4; progressive image loading. **Backend:** Document and region endpoints. **Database:** Region coordinates from CP104. **Mobile:** Tablet-adapted viewer. **AI:** — **OCR/NLP:** Displays OCR provenance. **Events/Audit:** Document views audited.

---

### CP111 · Follow-Up Scheduling Engine
**Objective:** §11.1's next-review interval turned into an actionable schedule. **Why:** The foundation of the entire CRM; §12 also needs follow-up adherence data. **Scope:** Follow-up created at visit close with the physician's interval · due-date computation · status lifecycle (scheduled → due → contacted → attended/missed/rescheduled) · overdue detection · **the missed-follow-up list, which is the operational heart of §11** · per-diagnosis default intervals (physician-configurable). **Out of scope:** Prediction (CP134) · calling (CP113). **Dependencies:** CP38. **Backend:** Follow-up module. **Database:** `follow_ups`, partial index on due. **API:** CRUD, due lists. **Frontend:** Follow-up dashboard. **Events/Audit:** `FOLLOWUP_SCHEDULED`. **Testing:** Due computation across timezones and DST-free Asia/Dhaka; overdue detection; attendance reconciliation when the patient arrives. **Manual verification:** Close a visit with a 3-month interval; confirm the follow-up appears on the correct date and moves to overdue correctly. **Acceptance criteria:** (1) Every closed visit produces a follow-up with the physician's interval. (2) Overdue transitions occur on schedule. (3) Attendance auto-reconciles when the patient registers a new visit. (4) Default intervals are configurable. **Deliverables:** Follow-up engine, dashboard. **Effort:** 6 days. **Risks:** Low. **Open decisions:** Default intervals per diagnosis (*clinical*).
**Technologies:** Per §4. **Mobile:** Follow-up visible in the patient view. **AI:** — **OCR/NLP:** — **Security:** RBAC per role.

---

### CP112 · Contact Preference & Checkout Capture [R-14]
**Objective:** §11.2 — capture the preferred call window, consent, and number **before the patient leaves**. **Why:** Named requirement; calling at the wrong time is the main reason follow-up calls fail. **Scope:** Checkout station capture: preferred window ("always free" vs specific hours), alternate number, call and SMS consent (linked to CP36), preferred language · **explicitly telling the patient the clinic will call** (recorded as part of the interaction) · update path at any later contact · display of preferences wherever a call task appears. **Out of scope:** Calling (CP113). **Dependencies:** CP36, CP111. **Mobile/Frontend:** Checkout capture. **Backend:** Preference storage. **Security:** Consent enforcement at send time. **Events/Audit:** Preference changes recorded. **Testing:** Consent linkage; preference application to task scheduling. **Manual verification:** Capture preferences at checkout and confirm the resulting call task is scheduled inside the stated window. **Acceptance criteria:** (1) Every completed visit captures contact preferences or an explicit refusal. (2) Call tasks respect the preferred window. (3) Consent is captured separately for calls and SMS. (4) Preferences are visible to whoever makes the call. **Deliverables:** Checkout capture, preference model. **Effort:** 4 days. **Risks:** Checkout is a rushed moment — keep it to three taps. **Open decisions:** Checkout station ownership (*operational*).
**Technologies:** Per §4. **Database:** Contact preference table. **API:** Preference CRUD. **Frontend:** Checkout capture on web. **Mobile:** Checkout capture on mobile. **AI:** — **OCR/NLP:** —

---

### CP113 · Call Task Queue & CRM Operator UI
**Objective:** §11.3's call tasks generated at the right time, with outcomes logged. **Why:** The operational core of the follow-up system. **Scope:** Task generation from due follow-ups respecting preferred windows · a telemedicine operator work queue prioritised by risk and overdue days · **the patient context an operator needs on one screen** (last visit, diagnoses, plan, last complaint, prior call history) · outcome capture in two taps (connected / no answer / wrong number / requested callback / refused) · notes · automatic SMS fallback trigger on no-answer · reschedule. **Out of scope:** Programmable dialling (D-40 — staff-dialled for Phase 2). **Dependencies:** CP111, CP112, **D-40**. **Backend:** Task engine. **Database:** `call_tasks`. **Frontend:** CRM operator console. **Security:** CRM role sees contact and plan data, not the full clinical record. **Events/Audit:** `CALL_TASK_CREATED`, `CALL_ATTEMPTED`, `CALL_CONNECTED`. **Testing:** Window respect; prioritisation; outcome-to-fallback triggering. **Manual verification:** A telemedicine staff member works 20 tasks and reports throughput and friction. **Acceptance criteria:** (1) Tasks appear only within the patient's preferred window. (2) Outcome capture takes ≤2 taps. (3) No-answer triggers SMS fallback automatically. (4) Every touch attaches to the patient record (§11.3). **Deliverables:** Call queue, operator console. **Effort:** 8 days. **Risks:** Operator throughput — design the console around the call, not around the data model. **Open decisions:** **D-40.**
**Technologies:** Per §4. **API:** Task queue endpoints. **Mobile:** — **AI:** — **OCR/NLP:** —

---

### CP114 · SMS Gateway Integration & Fallback
**Objective:** §11.3's automatic SMS fallback, working reliably with Bangla content. **Why:** Named requirement; §15.2 requires a programmable SMS gateway. **Scope:** Provider-agnostic `Notifier` interface with **two provider implementations trialled** (D-39) · templated bilingual messages from the CP91 content library · **Unicode Bangla handling with correct segment counting and cost accounting** · delivery receipt webhooks · retry policy · consent and quiet-hours enforcement · opt-out handling · per-message cost recording · a send log attached to the patient record. **Out of scope:** Inbound (CP115). **Dependencies:** CP113, **D-39**. **Backend:** Notification module + provider adapters. **Database:** `messages` with delivery status and cost. **API:** Webhook receiver. **Frontend:** Message history and template management. **Security:** Webhook authentication; no clinical detail in SMS content (**a message is readable by anyone holding the phone** — this is a privacy decision requiring confirmation). **Events/Audit:** `SMS_QUEUED/SENT/DELIVERED/FAILED`. **Testing:** Provider adapter tests; **Bangla rendering verified on real low-end handsets**; delivery receipt handling; consent blocking; retry behaviour. **Manual verification:** Send 100 messages through each trialled provider to real numbers across all four operators; measure delivery rate and Bangla rendering. **Acceptance criteria:** (1) Delivery rate measured per provider over ≥100 messages before vendor selection. (2) Bangla renders correctly on low-end handsets. (3) Consent revocation blocks sending. (4) Delivery receipts update message status. (5) Cost per message is recorded. **Deliverables:** Notification module, provider adapters, delivery report, vendor recommendation. **Effort:** 8 days. **Risks:** Provider reliability varies; sender-ID/masking registration takes time — **start the vendor process weeks before this checkpoint**. **Open decisions:** **D-39**; how much clinical content SMS may contain (*privacy, confirmation required*).
**Technologies:** Provider SDK/HTTP behind the Notifier interface. **Mobile:** — **AI:** — **OCR/NLP:** —

---

### CP115 · Inbound Messaging & Triage Queue
**Objective:** §11.4's two-way clinic line — patients can text or call at any time and it attaches to their record. **Why:** Named as one of the strongest aspects of the system by Dr. Nahid. **Scope:** Inbound SMS webhook (or the alternative channel from D-41) · **sender-to-patient matching by phone with an unmatched queue** · triage queue with categories and priority · assignment and response · conversation view per patient · inbound call logging (manually recorded if telephony is staff-dialled). **Out of scope:** Automated response bots (**deliberately excluded — a clinical inbox answered by AI is a risk, not a feature**). **Dependencies:** CP114, **D-41**. **Backend:** Inbound routing. **Database:** Inbound messages, triage state. **Frontend:** Triage console with conversation threads. **Security:** Unmatched senders quarantined; no clinical data released to an unverified number. **Events/Audit:** `INBOUND_MESSAGE_RECEIVED`. **Testing:** Matching accuracy; unmatched handling; SLA measurement on triage response. **Manual verification:** Send an inbound message from a registered patient's number and from an unknown number; confirm correct routing for both. **Acceptance criteria:** (1) Inbound messages from known numbers attach to the correct patient automatically. (2) Unknown senders go to an unmatched queue, never to a guessed patient. (3) Every inbound touch is visible on the patient record. (4) Triage response time is measured. **Deliverables:** Inbound routing, triage console. **Effort:** 6 days. **Risks:** **D-41 may make inbound SMS impractical** — confirm the channel before building. **Open decisions:** **D-41.**
**Technologies:** Per §4. **API:** Inbound webhook + triage endpoints. **Mobile:** — **AI:** — **OCR/NLP:** —

---

### CP116 · Communication History & Consent Enforcement
**Objective:** One complete, consent-governed communication record per patient. **Why:** §11.3 — "every outbound and inbound touch attaches to the patient record"; D-42 requires enforcement, not just recording. **Scope:** Unified communication timeline (calls, SMS, inbound, appointments) · consent state visible and enforced at every send point · opt-out processing including inbound STOP handling · **quiet hours and per-patient rate limiting** (a patient receiving five automated messages in a day will opt out permanently) · communication preferences per channel. **Out of scope:** — **Dependencies:** CP114, CP36. **Backend:** Communication aggregation + enforcement. **Frontend:** Communication tab on the patient record. **Security:** Consent enforced at the module boundary, testable in isolation. **Events/Audit:** All communication events. **Testing:** Consent blocking at every send path (**including any future path — enforced structurally, not per-feature**); rate limiting; opt-out processing. **Manual verification:** Opt a patient out by SMS reply and confirm all outbound stops immediately. **Acceptance criteria:** (1) No message can be sent without valid consent, enforced at the module boundary. (2) Inbound opt-out takes effect immediately. (3) Rate limits prevent message flooding. (4) The full communication history is on the patient record. **Deliverables:** Communication timeline, enforcement layer. **Effort:** 5 days. **Risks:** A new feature bypassing enforcement — prevented by making the Notifier the only send path. **Open decisions:** Quiet hours and rate limits (*operational*).
**Technologies:** Per §4. **Database:** Unified communication view. **API:** Communication history endpoint. **Mobile:** — **AI:** — **OCR/NLP:** —

---

### CP117 · Satisfaction Capture & Operator Matrix
**Objective:** §14.2's Patient Satisfaction Matrix, correlated to the operators who handled the visit. **Why:** Named requirement; closes the quality loop with the patient's voice. **Scope:** Post-visit satisfaction capture via exit kiosk and/or SMS link · simple bilingual scale with an optional comment · **automatic correlation to the operators and physician who handled that visit** (available free from the encounter records) · aggregation per operator and per station · trend reporting · **response-bias awareness in presentation** (low response rates make small differences meaningless, and the UI should say so). **Out of scope:** Public reviews. **Dependencies:** CP116. **Backend:** Survey engine + correlation. **Database:** `satisfaction_surveys`. **Frontend:** Kiosk view + reporting. **Security:** Comments may contain PHI — handled accordingly; operator-level results visible only to supervisors. **Events/Audit:** `SATISFACTION_RECORDED`. **Testing:** Correlation correctness; response rate tracking; anonymity handling. **Manual verification:** Complete a visit, submit a survey by SMS link, and confirm it correlates to the right operators. **Acceptance criteria:** (1) Surveys correlate automatically to the visit's operators. (2) Response rate is tracked and displayed alongside every score. (3) Operator-level results are supervisor-only. (4) The kiosk is usable by low-literacy patients. **Deliverables:** Satisfaction capture, correlation, reporting. **Effort:** 5 days. **Risks:** **Correlating satisfaction to individuals is culturally sensitive** — agree the presentation and use with Dr. Nahid before release. **Open decisions:** Visibility policy (*operational*).
**Technologies:** Per §4. **API:** Survey submit and report endpoints. **Mobile:** — **AI:** — **OCR/NLP:** —

---

### CP118 · Pharmacy Dispensing UI (RBAC-Blinded)
**Objective:** §14.3's closed-loop pharmacy — authorised prescriptions route instantly to pharmacy with **diagnoses hidden**. **Why:** Named requirement and a direct test of the CP20 field-redaction guarantee. **Scope:** Pharmacy queue of signed prescriptions · **payload containing drugs and dosing only — no diagnoses, no clinical notes** · dispensing capture per item (full, partial, substituted with reason, unavailable) · patient identity verification step · dispensing receipt · realtime queue updates. **Out of scope:** Inventory deduction (CP119). **Dependencies:** CP84, CP20. **Backend:** Pharmacy module. **API:** Redacted prescription payload. **Frontend:** Pharmacy console. **Security:** **The redaction is verified by a golden test on the raw API response**, not by UI inspection. **Events/Audit:** `MEDICATION_DISPENSED` with attribution. **Testing:** Redaction golden test; partial dispensing; substitution recording; queue realtime. **Manual verification:** Log in as the pharmacist, open a prescription, and inspect the raw network payload for any diagnosis field. **Acceptance criteria:** (1) The pharmacy payload contains no diagnosis or clinical note, proven by golden test. (2) Signed prescriptions appear in the pharmacy queue within 2s. (3) Partial dispensing and substitution are capturable with reasons. (4) Every dispensing is attributed. **Deliverables:** Pharmacy console, redacted API. **Effort:** 7 days. **Risks:** Substitution without physician knowledge — notify the physician when a substitution occurs. **Open decisions:** Substitution policy (*clinical*).
**Technologies:** Per §4. **Database:** Dispensing records. **Mobile:** — **AI:** — **OCR/NLP:** —

---

### CP119 · Inventory, Batches & Real-Time Stock Deduction
**Objective:** §14.3 — dispensing auto-deducts central inventory in real time. **Why:** Named requirement and the data foundation for §14.3's predictive supply chain. **Scope:** Inventory items, batches with expiry, stock movements (receipt/dispense/adjust/expire/return) · **automatic deduction on dispensing within the same transaction** · reorder levels with alerts · expiry warnings · stock-take workflow with variance recording · valuation. **Out of scope:** Procurement prediction (CP142) · supplier management. **Dependencies:** CP118. **Backend:** Inventory module. **Database:** Inventory tables (movement ledger pattern). **Frontend:** Inventory console. **Security:** Stock adjustments require elevated permission and a reason (shrinkage is a real risk). **Events/Audit:** Stock movements recorded with actor and reason. **Testing:** Deduction atomicity with dispensing; negative-stock prevention; expiry logic; stock-take variance. **Manual verification:** Dispense an item and confirm stock decrements immediately and correctly, including batch selection. **Acceptance criteria:** (1) Dispensing and deduction are atomic — no dispensing without deduction. (2) Stock cannot go negative without an explicit, permissioned adjustment. (3) Expiring batches are flagged in advance. (4) Every movement is attributed with a reason. **Deliverables:** Inventory module, console, stock-take workflow. **Effort:** 8 days. **Risks:** Reconciling system stock with physical reality — the stock-take workflow and variance reporting are essential, not optional. **Open decisions:** Batch selection policy (FEFO recommended); stock-take frequency (*operational*).
**Technologies:** Per §4. **API:** Inventory and movement endpoints. **Mobile:** — **AI:** — **OCR/NLP:** —

---

### CP120 · Phase 2 Hardening & Sign-Off
**Objective:** Stabilise, measure and formally close Phase 2. **Why:** §15.3's phase acceptance rule. **Scope:** Performance testing of the records pipeline at real document volumes · OCR accuracy report against production data · CRM delivery-rate report · pharmacy reconciliation verification · security review of the new surface (uploads, webhooks, public inbound) · defect burn-down to zero critical · staff training on the new stations · sign-off. **Dependencies:** CP96–CP119. **Testing:** Full regression, performance, security. **Manual verification:** Dr. Nahid reviews a patient whose complete external history was digitised, and confirms the chronology matches the paper bundle. **Acceptance criteria:** (1) No open critical defects. (2) OCR accuracy on production documents reported and accepted. (3) SMS delivery rate reported and accepted. (4) Pharmacy stock reconciles against a physical count. (5) Written Phase 2 sign-off. **Deliverables:** Reports, remediation, sign-off record. **Effort:** 8 days. **Risks:** OCR accuracy on real documents falling short of the pilot corpus — the human validation queue absorbs this, at an operational cost that must be measured and accepted. **Open decisions:** None.

---

## PHASE 3 — INTELLIGENCE & RESEARCH
**Out of scope (continued):** New features — this is a stabilisation and acceptance checkpoint only. **Technologies:** Existing stack. **Backend:** Defect fixes only. **Database:** Defect fixes only. **API:** Defect fixes only. **Frontend:** Defect fixes only. **Mobile:** Defect fixes only. **AI:** Evaluation re-run on production data. **OCR/NLP:** Accuracy report on production documents. **Security:** Review of the Phase 2 surface (uploads, webhooks, inbound). **Events/Audit:** End-to-end audit verification.

---

### CP121 · Research Schema & Anonymisation ETL [R-15]
**Objective:** A one-way, consent-filtered, anonymised research data path (§9.8, D-48). **Why:** §12 requires IRB-grade integrity; anonymisation retrofitted onto a live analytics stack is never fully trustworthy. **Scope:** `research` schema with **no join path to `core`** · nightly ETL: direct identifiers removed, per-subject date shifting, age banding >89, geography truncated to district, free text excluded by default, consent filter applied · separately-held re-identification map with audited break-glass · small-cell suppression rule · data dictionary. **Out of scope:** Dashboards (CP123). **Dependencies:** CP25, CP36, **D-48**. **Backend:** ETL jobs. **Database:** Research schema, separate role. **Security:** **Grant-level separation** — the research role cannot reach clinical tables, proven by test. **Events/Audit:** ETL runs and any break-glass re-identification audited. **Testing:** A test asserting no direct identifier appears in any research table; consent filtering; date-shift consistency per subject; re-identification isolation. **Manual verification:** Attempt to identify a patient using only research schema access. **Acceptance criteria:** (1) No direct identifier exists in `research`. (2) Non-consented patients are absent. (3) Date shifting is consistent per subject (intervals preserved, absolute dates not). (4) The research role cannot query clinical schemas. **Deliverables:** Research schema, ETL, data dictionary, anonymisation profile. **Effort:** 10 days. **Risks:** Re-identification via quasi-identifier combinations — apply and document k-anonymity checks. **Open decisions:** **D-03, D-48.**
**Technologies:** Per §4; ETL in Go jobs. **API:** Internal ETL only. **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** —

---

### CP122 · Cohort Builder & Definition Versioning
**Objective:** Define, save and re-run patient cohorts reproducibly. **Why:** §12.1 requires saved refreshable dashboards; reproducibility is what makes results publishable. **Scope:** Visual cohort builder (demographics, diagnoses, medications with date ranges, lab criteria, outcome windows) · **versioned definitions so a published analysis can be reproduced exactly** · membership snapshots with entry/exit dates · export with the definition attached · cohort size preview with small-cell warnings. **Out of scope:** Statistics (CP123). **Dependencies:** CP121. **Backend:** Cohort engine. **Database:** `cohorts`, `cohort_memberships`, versions. **Frontend:** Builder UI. **Security:** Research role only; exports audited and watermarked. **Testing:** Definition-to-membership correctness; version reproducibility (**re-running a v1 definition must return the v1 population**); performance at scale. **Manual verification:** Build a "patients on semaglutide with ≥2 HbA1c results" cohort and verify membership by hand on a sample. **Acceptance criteria:** (1) A saved definition re-run later returns a reproducible population. (2) Definitions are versioned and attached to every export. (3) Small cohorts trigger suppression warnings. (4) Every export is audited. **Deliverables:** Cohort builder, versioning, export. **Effort:** 8 days. **Risks:** Query performance on complex temporal criteria. **Open decisions:** Export approval workflow (*governance*).
**Technologies:** Per §4. **API:** Cohort CRUD and preview. **Mobile:** — **AI:** — **OCR/NLP:** — **Events/Audit:** Definition changes and exports audited.

---

### CP123 · Research Dashboard Framework
**Objective:** The reusable framework behind §12.1's named dashboards. **Why:** Six named analyses share the same needs — building each bespoke would triple the work and produce six inconsistent statistical treatments. **Scope:** Dashboard definition model (cohort + metrics + visualisations + filters) · **a shared statistics layer** (descriptive statistics, distributions, trends, group comparison with appropriate tests, confidence intervals) · standard visual components consistent with the design system · scheduled refresh · export to CSV and publication-quality figures · **explicit small-cell suppression and missing-data reporting on every view** (a dashboard that silently hides missing data misleads). **Out of scope:** The individual dashboards. **Dependencies:** CP122. **Backend:** Statistics + dashboard engine. **Frontend:** Dashboard renderer. **Security:** Research role; exports audited. **Testing:** **Statistical correctness verified against R or Python reference implementations** on fixed datasets; suppression enforcement. **Manual verification:** Compare framework outputs against the same analysis run independently in R. **Acceptance criteria:** (1) Statistics match an independent reference implementation. (2) Missing-data counts are displayed on every view. (3) Small cells are suppressed. (4) Figures export at publication quality. **Deliverables:** Dashboard framework, statistics layer, export. **Effort:** 8 days. **Risks:** **Statistical errors would invalidate publications** — independent verification is mandatory, not optional. **Open decisions:** Statistical conventions (*research; Dr. Nahid or a statistician*).
**Technologies:** Per §4; visx/Recharts. **Database:** Reads `research` only. **API:** Dashboard query endpoints. **Mobile:** — **AI:** — **OCR/NLP:** — **Events/Audit:** Exports audited.

---

### CP124 · HbA1c Trajectory Dashboard
**Objective:** §12.1's first named analysis — how each patient's HbA1c developed, and improvement distributions by cohort. **Scope:** Individual trajectories · cohort-level change from baseline · distribution of improvement · time-to-target · stratification by demographics, treatment and adherence · missing-data transparency. **Dependencies:** CP123. **Backend/Frontend:** Dashboard definition + views. **Security:** Research role. **Testing:** Metric correctness on synthetic cohorts with known values; baseline-definition edge cases. **Manual verification:** Dr. Nahid checks the trajectory for five patients he knows well against his own recollection and their paper records. **Acceptance criteria:** (1) Trajectories match the underlying observations exactly. (2) Baseline and follow-up windows are explicitly defined and displayed. (3) Missing data is reported, not silently dropped. (4) The dashboard refreshes on schedule. **Deliverables:** HbA1c dashboard. **Effort:** 5 days. **Risks:** Baseline definition ambiguity — define explicitly with Dr. Nahid. **Open decisions:** Baseline and window definitions (*research*).
**Why this checkpoint exists:** One of §12.1’s named launch analyses; each is its own checkpoint so each can be clinically validated independently. **Out of scope (continued):** New data capture — this checkpoint visualises existing structured data; capture gaps are raised as separate checkpoints. **Technologies:** CP123 framework; no new dependencies. **Backend:** Dashboard definition and queries. **Database:** Reads `research` only. **API:** Dashboard query endpoints. **Frontend:** Dashboard views. **Mobile:** — **AI:** — **OCR/NLP:** — **Events/Audit:** Exports audited.

---

### CP125 · GLP-1 RA Safety Dashboard
**Objective:** §12.1 — percentage of patients experiencing adverse effects on semaglutide, incidence by starting dose, and **empirical dose-initiation analysis** ("what dose should treatment start at"). **Why:** Explicitly named; also the analysis with the most direct effect on Dr. Nahid's own prescribing. **Scope:** Adverse-effect capture requirements traced back to structured fields (this dashboard **depends on AE data being captured at follow-up** — if it is not captured, the dashboard cannot exist, and that capture requirement must be added at CP113/CP111) · incidence by starting dose and escalation pattern · time-to-onset · discontinuation for AE · dose-response visualisation. **Dependencies:** CP123. **Backend/Frontend:** Dashboard. **Testing:** Denominator correctness (**who is at risk and for how long** — person-time, not patient counts, is the correct denominator here). **Manual verification:** Dr. Nahid validates the AE rate against his clinical impression. **Acceptance criteria:** (1) Denominators are person-time-based and stated. (2) Incidence is broken down by starting dose. (3) Discontinuation reasons are distinguished. (4) The structured AE capture that feeds it exists upstream. **Deliverables:** GLP-1 safety dashboard, upstream AE capture requirements. **Effort:** 6 days. **Risks:** **AE data quality depends entirely on capture discipline at follow-up** — this must be designed into CP113's call script, not bolted on later. **Open decisions:** AE taxonomy (*clinical*).
**Out of scope (continued):** New data capture — this checkpoint visualises existing structured data; capture gaps are raised as separate checkpoints. **Technologies:** CP123 framework; no new dependencies. **Backend:** Dashboard definition and queries. **Database:** Reads `research` only. **API:** Dashboard query endpoints. **Frontend:** Dashboard views. **Mobile:** — **AI:** — **OCR/NLP:** — **Security:** Research role; AE data is de-identified. **Events/Audit:** Exports audited.

---

### CP126 · GLP-1 RA Efficacy & Weight-Response Dashboard
**Objective:** §12.1 — how many patients semaglutide helped lose weight, response distributions, and the same for tirzepatide. **Scope:** Weight change from baseline by drug, dose and duration · responder analysis at defined thresholds · HbA1c response alongside weight · non-responder characterisation · head-to-head comparison with appropriate caveats about confounding in observational data. **Dependencies:** CP123. **Testing:** Metric correctness; responder threshold handling. **Manual verification:** Cross-check ten patients' computed weight change against their records. **Acceptance criteria:** (1) Response thresholds are explicit and configurable. (2) Comparisons display confounding caveats prominently. (3) Follow-up duration is shown alongside every response figure. **Deliverables:** Efficacy dashboard. **Effort:** 5 days. **Risks:** Observational comparison presented as causal — the caveat display is a real requirement, not decoration. **Open decisions:** Responder thresholds (*clinical*).
**Why this checkpoint exists:** One of §12.1’s named launch analyses; each is its own checkpoint so each can be clinically validated independently. **Out of scope (continued):** New data capture — this checkpoint visualises existing structured data; capture gaps are raised as separate checkpoints. **Technologies:** CP123 framework; no new dependencies. **Backend:** Dashboard definition and queries. **Database:** Reads `research` only. **API:** Dashboard query endpoints. **Frontend:** Dashboard views. **Mobile:** — **AI:** — **OCR/NLP:** — **Security:** Research role; exports audited. **Events/Audit:** Exports audited.

---

### CP127 · Affordability & Persistence Dashboard
**Objective:** §12.1 and §10.3 — how patients bear the cost of semaglutide/tirzepatide in Bangladesh, and discontinuation-for-cost analysis. **Why:** Distinctive to this clinic's research programme and a genuine contribution to the local literature. **Scope:** Cost per patient per month using **the historical price at prescribing time** (CP75's price history) · persistence curves · discontinuation reason analysis with cost isolated · affordability stratified by socio-economic baseline (captured at CP28) · the BDT pricing lens as a standing axis on every relevant view. **Dependencies:** CP123, CP75. **Testing:** Historical price correctness; persistence computation. **Manual verification:** Verify cost calculations for five patients by hand. **Acceptance criteria:** (1) Costs use the price effective at prescribing time. (2) Persistence curves handle censoring correctly. (3) Discontinuation reasons distinguish cost from clinical causes. (4) Socio-economic stratification works. **Deliverables:** Affordability dashboard. **Effort:** 6 days. **Risks:** Discontinuation reason capture must exist upstream — trace it to CP113. **Open decisions:** Discontinuation reason taxonomy (*clinical*).
**Out of scope (continued):** New data capture — this checkpoint visualises existing structured data; capture gaps are raised as separate checkpoints. **Technologies:** CP123 framework; no new dependencies. **Backend:** Dashboard definition and queries. **Database:** Reads `research` only. **API:** Dashboard query endpoints. **Frontend:** Dashboard views. **Mobile:** — **AI:** — **OCR/NLP:** — **Security:** Research role; exports audited. **Events/Audit:** Exports audited.

---

### CP128 · Exercise–Outcome Correlation Dashboard
**Objective:** §12.1 — metabolic improvement as a function of recorded exercise adherence. **Scope:** Adherence metrics from CP60's structured targets · correlation with HbA1c, weight and BP change · stratification by exercise type and intensity · adjustment for confounders where feasible, with limitations stated. **Dependencies:** CP123, CP60. **Testing:** Correlation computation against a reference implementation. **Manual verification:** Sanity-check the direction and magnitude of associations with Dr. Nahid. **Acceptance criteria:** (1) Adherence is computed from structured data, not free text. (2) Statistical methods are documented on the dashboard. (3) Limitations of self-reported adherence are stated on the view. **Deliverables:** Exercise dashboard. **Effort:** 5 days. **Risks:** Self-reported adherence is weak data — say so on the dashboard rather than in a footnote. **Open decisions:** Adherence definition (*clinical*).
**Why this checkpoint exists:** One of §12.1’s named launch analyses; each is its own checkpoint so each can be clinically validated independently. **Out of scope (continued):** New data capture — this checkpoint visualises existing structured data; capture gaps are raised as separate checkpoints. **Technologies:** CP123 framework; no new dependencies. **Backend:** Dashboard definition and queries. **Database:** Reads `research` only. **API:** Dashboard query endpoints. **Frontend:** Dashboard views. **Mobile:** — **AI:** — **OCR/NLP:** — **Security:** Research role; exports audited. **Events/Audit:** Exports audited.

---

### CP129 · Multi-Benefit & SGLT2i/Linagliptin Dashboards
**Objective:** §12.1's multi-benefit dashboards — semaglutide and tirzepatide across diabetes, hypertension, obesity and dyslipidemia simultaneously, plus parallel dashboards for dapagliflozin, empagliflozin, canagliflozin and linagliptin. **Scope:** A parameterised drug-outcome dashboard template instantiated per drug class · simultaneous outcome panels (HbA1c, BP, weight, lipids) · consistent cohort and window definitions across drugs so comparisons are meaningful · a class-comparison view. **Dependencies:** CP123. **Testing:** Template instantiation correctness; cross-drug consistency of definitions. **Manual verification:** Dr. Nahid reviews one instantiated dashboard per class. **Acceptance criteria:** (1) One template serves all named drugs. (2) Definitions are identical across instances. (3) All four outcome domains appear on one view. (4) Adding a new drug requires configuration, not code. **Deliverables:** Parameterised dashboard, instances for all named drugs. **Effort:** 8 days. **Risks:** Small per-drug cohorts producing unstable estimates — suppression rules apply. **Open decisions:** None.
**Why this checkpoint exists:** One of §12.1’s named launch analyses; each is its own checkpoint so each can be clinically validated independently. **Out of scope (continued):** New data capture — this checkpoint visualises existing structured data; capture gaps are raised as separate checkpoints. **Technologies:** CP123 framework; no new dependencies. **Backend:** Dashboard definition and queries. **Database:** Reads `research` only. **API:** Dashboard query endpoints. **Frontend:** Dashboard views. **Mobile:** — **AI:** — **OCR/NLP:** — **Security:** Research role; exports audited. **Events/Audit:** Exports audited.

---

### CP130 · Automated Hypothesis Engine & Positive-Deviance Mining
**Objective:** §12.2 — automated pattern hunting, autonomously drafted research proposals, and aggressive tracking of unexpected successes. **Scope:** Systematic scanning for associations and subgroup anomalies with **multiple-comparison awareness** (a system that hunts thousands of associations will find false ones — correction and honest framing are mandatory) · positive-deviance detection (e.g. unexpected remission) with case surfacing for review · LLM-drafted proposals **clearly marked as hypotheses, never findings** · a researcher review queue. **Dependencies:** CP122, CP70. **AI:** Statistical scan first, LLM narrative second — never the reverse. **Testing:** False-discovery control verification on null datasets (**a scan of random data must produce almost no "findings"** — this is the acceptance test that matters). **Manual verification:** Review generated hypotheses with Dr. Nahid for plausibility and novelty. **Acceptance criteria:** (1) A scan of a null dataset produces findings at the expected false-positive rate, not more. (2) Every hypothesis reports its multiple-comparison context. (3) Output is framed as hypotheses requiring testing. (4) Positive-deviance cases are surfaced with their full clinical context. **Deliverables:** Hypothesis engine, positive-deviance detection, review queue. **Effort:** 10 days. **Risks:** **Data dredging presented as discovery** — the null-dataset test and honest framing are the guardrails. **Open decisions:** Correction method (*statistical*).
**Why this checkpoint exists:** §12.2 requires an automated hypothesis engine and positive-deviance mining; both are named discovery machinery. **Out of scope (continued):** Automated publication; every hypothesis is human-reviewed. **Technologies:** Statistical scan in Go/Python; LLM via CP70. **Backend:** Scan jobs and review queue. **Database:** Reads `research` only; hypothesis records. **API:** Hypothesis queue endpoints. **Frontend:** Review queue UI. **Mobile:** — **OCR/NLP:** — **Security:** Research role only. **Events/Audit:** Hypothesis generation and review recorded.

---

### CP131 · Research Assistant & Narrative Engine
**Objective:** §12's Automated Clinical Narrative Engine supporting the 2-papers-monthly and 2-books-annually targets, with strict PII stripping. **Scope:** Draft manuscript sections (methods from cohort definitions — which are already structured and therefore reliably describable; results from computed statistics; discussion drafted with citations) · **every statistic inserted programmatically from the analysis engine, never generated by the LLM** · book-length narrative assembly · PII stripping enforced by the data path · export to standard manuscript formats. **Dependencies:** CP130. **AI:** Drafting only. **Security:** Operates on `research` only. **Testing:** A test asserting no statistic in the output differs from the analysis engine's computed value; PII absence tests. **Manual verification:** Dr. Nahid reviews a generated methods and results section for accuracy. **Acceptance criteria:** (1) Every number in a draft traces to a computed statistic. (2) No PII appears in any output. (3) Drafts are marked AI-assisted. (4) Methods sections accurately describe the actual cohort definition. **Deliverables:** Narrative engine, manuscript export. **Effort:** 10 days. **Risks:** **Fabricated statistics in a manuscript would be catastrophic** — hence programmatic insertion only. **Open decisions:** Authorship and AI-assistance disclosure policy (*research ethics*).
**Why this checkpoint exists:** §12 sets targets of 2+ papers monthly and 2 books annually; drafting is where the time goes. **Out of scope (continued):** Submission workflow and journal formatting beyond standard export. **Technologies:** LLM via CP70; export tooling. **Backend:** Draft assembly. **Database:** Reads `research` only. **API:** Draft generation and export. **Frontend:** Draft editor. **Mobile:** — **OCR/NLP:** — **Events/Audit:** Draft generation recorded with prompt and model version.

---

### CP132 · AI Agents: Clinical Assistant & Diagnostic Support
**Objective:** §7.2's Clinical Assistant (missing questions/examinations) and Diagnostic Support (Bayesian differential). **Scope:** Per §10.4 A3 and A4 · gap analysis grounded in actually-absent data points · **an explicit scored/Bayesian differential model rather than free LLM generation**, with LLM used only for explanation · endocrine-scoped [R-06] · advisory-only presentation · physician dismissal recorded. **Dependencies:** CP70, CP72. **AI:** Both agents. **Security:** PHI minimisation. **Testing:** Gap detection accuracy; differential top-3 concordance with the physician's final diagnosis on retrospective cases; a test asserting no diagnostic output is presented as a diagnosis. **Manual verification:** Dr. Nahid reviews 20 cases and rates usefulness and safety. **Acceptance criteria:** (1) Gap suggestions reference genuinely missing data, verified deterministically. (2) Differentials are ranked with contributing findings shown. (3) Output is explicitly advisory. (4) Concordance measured on retrospective cases and reported. **Deliverables:** Both agents, evaluation report. **Effort:** 12 days. **Risks:** Automation bias and alert fatigue. **Open decisions:** Usefulness acceptance bar (*clinical*).
**Why this checkpoint exists:** Two of §7.2’s named agents; deferred to Phase 3 so Phase 1 proves the AI pipeline first. **Out of scope (continued):** Any autonomous clinical action; both agents are advisory. **Technologies:** CP70 gateway; scored model for differentials. **Backend:** Agent pipelines. **Database:** AI interaction records. **API:** Agent invocation and dismissal endpoints. **Frontend:** Dashboard panels. **Mobile:** — **OCR/NLP:** — **Events/Audit:** Suggestions and dismissals recorded as events.

---

### CP133 · AI Medical Scribe (Dictation-First)
**Objective:** §7.2's scribe, implemented safely per D-12 — dictation before ambient capture. **Scope:** Physician dictation after consultation → STT → structured SOAP draft → grounding against structured data → physician edit and sign · **Bangla/English code-switched evaluation set** built and measured before release · audio retention policy applied · **ambient capture only after explicit patient consent design and legal review**, and only if Dr. Nahid asks for it. **Dependencies:** CP70, **D-12**. **AI:** STT + structuring. **Security:** Consent, retention, encryption; audio in the `IDENTIFIER` class. **Testing:** WER on the code-switched evaluation set; SOAP structuring accuracy; a test asserting an unsigned note never enters the record. **Manual verification:** Dr. Nahid dictates ten consultations and measures the edit burden against typing. **Acceptance criteria:** (1) WER measured and reported on Bangla/English code-switched audio. (2) Notes require signature before entering the record. (3) Audio retention follows the approved policy. (4) The physician's edit time is lower than typing time (**otherwise the feature is not worth shipping**). **Deliverables:** Scribe agent, evaluation set, retention policy. **Effort:** 12 days. **Risks:** **Code-switched STT accuracy is the make-or-break variable** — measure before committing to the full build. **Open decisions:** **D-12.**
**Why this checkpoint exists:** §7.2 names the Medical Scribe; D-12 requires the safer dictation-first path before any ambient capture. **Out of scope (continued):** Ambient consultation recording until consent design and legal review are complete. **Technologies:** STT provider per D-12; CP70 gateway. **Backend:** Audio pipeline and structuring job. **Database:** Audio references, transcripts, draft notes. **API:** Upload, transcribe, draft, sign. **Frontend:** Note editor and signing. **Mobile:** Dictation capture. **OCR/NLP:** — **Events/Audit:** Note signing recorded as an event.

---

### CP134 · Follow-Up Predictor (ML)
**Objective:** §7.2 and §11.5 — no-show and deterioration risk driving proactive outreach. **Scope:** Feature engineering from adherence, prior no-shows, distance, socio-economic markers, contact history, diagnosis and cost burden · **gradient-boosted classifier, not an LLM** · calibrated probabilities · explanation via feature contributions · **fairness review across income and geography before deployment** · integration into the CP113 outreach queue · monitoring for drift. **Dependencies:** CP111, CP121. **AI:** Traditional ML. **Security:** Trained on research-schema data. **Testing:** AUC, calibration, precision@k on held-out data; **subgroup performance comparison**; drift monitoring. **Manual verification:** Compare predictions against actual attendance over one month before acting on them. **Acceptance criteria:** (1) Performance measured on held-out data and reported. (2) Calibration verified (a 30% prediction means 30% observed). (3) Subgroup fairness reviewed and documented. (4) Predictions rank the outreach queue; they never deprioritise care. **Deliverables:** Predictor, fairness review, monitoring. **Effort:** 10 days. **Risks:** **Using risk scores to deprioritise vulnerable patients would be an ethical failure** — the system uses them only to add outreach, never to remove it. **Open decisions:** Deployment threshold (*operational*).
**Why this checkpoint exists:** §7.2 and §11.5 require predictive flagging of no-shows and deterioration for proactive outreach. **Out of scope (continued):** Any use of risk scores to reduce care for high-risk patients. **Technologies:** Python ML service; gradient boosting. **Backend:** Scoring job and API. **Database:** Feature store and scores. **API:** Score retrieval. **Frontend:** Risk display in the outreach queue. **Mobile:** — **OCR/NLP:** — **Events/Audit:** Model version recorded with every score.

---

### CP135 · Nightly Outcome Monitoring & QA Engine
**Objective:** §14.1 — AI reviews 100% of files for missed diagnoses, medication errors, documentation gaps and follow-up failures. **Scope:** Nightly batch over closed files · **deterministic rule checks first**, LLM narrative explanation second · finding categories with severity · QA officer queue with triage · trend reporting on finding types · **an alert if the engine produces no findings at all** (silence usually means a broken job, not a perfect clinic). **Dependencies:** CP83, CP69. **AI:** Explanation only. **Testing:** Rule correctness; finding precision measured against QA officer judgement; job reliability. **Manual verification:** The QA officer reviews a night's findings and rates how many are genuine. **Acceptance criteria:** (1) 100% of closed files are reviewed nightly. (2) Finding precision is measured and reported. (3) Findings route to a human queue, never to automatic action. (4) A zero-finding night raises an operational alert. **Deliverables:** Nightly QA engine, findings queue, trends. **Effort:** 8 days. **Risks:** Low precision overwhelming the QA officer — tune rules before expanding coverage. **Open decisions:** Finding taxonomy (*clinical*).
**Why this checkpoint exists:** §14.1 requires AI review of 100% of files; retrospective auditing complements the real-time QA gate at CP83. **Out of scope (continued):** Automatic corrective action — findings go to a human queue. **Technologies:** Rules in Go; LLM narrative via CP70. **Backend:** Nightly job. **Database:** QA finding records. **API:** Findings queue endpoints. **Frontend:** QA findings console. **Mobile:** — **OCR/NLP:** — **Security:** QA role. **Events/Audit:** Findings and resolutions recorded.

---

### CP136 · Code Red Negative-Outcome RCA Workflow
**Objective:** §14.1's closed-loop Negative Outcome Engine — a severe side effect, negative experience or unexpected HbA1c spike triggers a rapid forensic root-cause analysis and a **forced systemic prevention update**. **Scope:** Trigger detection (reported AE, satisfaction outlier, unexpected clinical deterioration) · Code Red case creation with the full audited timeline of that patient's journey assembled automatically (**the event ledger makes this genuinely forensic — every value, operator, device and timestamp**) · structured RCA with classification (clinical error / operational failure / patient-side barrier) · **a mandatory prevention action that must be recorded and closed** · tracking of whether the same failure recurs. **Dependencies:** CP135. **Backend/Frontend:** Case management. **Security:** Sensitive; restricted access. **Events/Audit:** RCA cases and actions audited. **Testing:** Trigger detection; timeline assembly completeness; action closure enforcement. **Manual verification:** Run a real (or historical) case end to end with Dr. Nahid. **Acceptance criteria:** (1) A Code Red assembles the complete attributed patient journey automatically. (2) Classification is structured. (3) A case cannot close without a recorded prevention action. (4) Recurrence of the same failure mode is detected and surfaced. **Deliverables:** Code Red workflow, RCA templates, recurrence tracking. **Effort:** 7 days. **Risks:** **Blame culture** — the workflow must be explicitly systemic, not individual; Dr. Nahid should set that tone in the templates. **Open decisions:** Trigger thresholds (*clinical*).
**Why this checkpoint exists:** §14.1’s Code Red engine; the event ledger makes a genuinely forensic reconstruction possible. **Out of scope (continued):** Disciplinary process — the workflow is explicitly systemic. **Technologies:** Per §4. **Backend:** Case assembly from the ledger. **Database:** RCA case records. **API:** Case CRUD and action tracking. **Frontend:** RCA console. **Mobile:** — **AI:** Assists narrative assembly; classification is human. **OCR/NLP:** —

---

### CP137 · Guideline/CME Engine & Weekly Clinical Digest
**Objective:** §7.2's Global Knowledge & Guideline Engine with its explicit alert-fatigue control. **Scope:** Corpus ingestion (ADA/EASD/WHO/PubMed per D-25's licensing answers) · chunking, embedding, `pgvector` retrieval · change detection against the prior corpus version · **cross-referencing the active patient panel** when guidance changes · **weekly Clinical Digest batching, with instant push reserved for critical safety recalls** · every statement carries a citation · **guideline changes create proposals for Dr. Nahid's approval, never automatic rule changes**. **Dependencies:** CP70, **D-10, D-25**. **AI:** RAG. **Security:** Corpus licensing respected; no redistribution. **Testing:** Retrieval relevance evaluation; citation accuracy (**every citation must resolve to a real source — a fabricated citation is the classic RAG failure**); digest batching; recall-push path. **Manual verification:** Dr. Nahid reads four weekly digests and rates usefulness and noise. **Acceptance criteria:** (1) Every statement has a resolvable citation. (2) Non-critical updates batch weekly. (3) Critical safety recalls push immediately. (4) No system rule changes without physician approval. (5) Panel cross-referencing identifies genuinely affected patients. **Deliverables:** Knowledge engine, digest, proposal workflow. **Effort:** 12 days. **Risks:** Citation fabrication; corpus licensing. **Open decisions:** **D-10, D-25.**
**Why this checkpoint exists:** §7.2’s Automated CME engine, including its explicit alert-fatigue control. **Out of scope (continued):** Automatic modification of clinical rules — changes are proposals only. **Technologies:** pgvector, embeddings, CP70 gateway. **Backend:** Ingestion, retrieval, digest jobs. **Database:** Knowledge chunks with vector index. **API:** Digest and proposal endpoints. **Frontend:** Digest reader and proposal approval. **Mobile:** Digest readable on mobile. **OCR/NLP:** — **Events/Audit:** Proposal approvals recorded.

---

### CP138 · Biometric Attendance & HR Core
**Objective:** §14.2's biometric attendance synced with RBAC access. **Scope:** Device integration per D-57 · **templates stored, never raw biometric images**, encrypted with a dedicated key · check-in/out records with shift computation · staff profiles with cost-per-hour (feeding CP141) · leave and roster basics · **anomaly detection: attendance without system activity, or system activity without attendance** (the credential-sharing signal from §12.1). **Dependencies:** CP15, **D-57**. **Security:** **Biometric data is among the most regulated categories under D-01** — explicit legal review, staff consent, and a documented retention policy are prerequisites, not follow-ups. **Testing:** Device integration; template encryption; anomaly detection. **Manual verification:** Full check-in/out cycle with correlation against system activity. **Acceptance criteria:** (1) Only templates are stored, encrypted with a dedicated key. (2) Staff consent to biometric processing is recorded. (3) Attendance correlates with system activity, with anomalies flagged. (4) Retention policy is implemented. **Deliverables:** Attendance integration, HR core, anomaly detection. **Effort:** 8 days. **Risks:** **Legal exposure on biometrics (D-01)** and hardware integration variability. **Open decisions:** **D-57**; legal clearance.
**Why this checkpoint exists:** §14.2 requires biometric attendance synced with RBAC, and it is the cost basis for CP141. **Out of scope (continued):** Payroll processing. **Technologies:** Device SDK per D-57. **Backend:** Device integration and shift computation. **Database:** Attendance and staff profile tables. **API:** Attendance endpoints. **Frontend:** HR console. **Mobile:** — **AI:** — **OCR/NLP:** — **Events/Audit:** Attendance records and anomalies audited.

---

### CP139 · Workflow Friction & Station Throughput Analytics
**Objective:** §14.2's station-to-station timing, bottleneck detection and operator throughput scoring. **Why:** The encounter records from CP38 make this nearly free — the data already exists. **Scope:** Handling time per station per operator · queue wait times · bottleneck detection with historical patterns (e.g. "the nutrition queue backs up every Tuesday at 11") · throughput per operator daily/weekly/monthly · patient journey duration end to end · comparison against targets. **Dependencies:** CP39, CP38. **Testing:** Timing accuracy against known synthetic journeys; aggregation correctness. **Manual verification:** Compare computed handling times against stopwatch observation of a real station for one hour. **Acceptance criteria:** (1) Handling times match observed reality within a documented tolerance. (2) Bottlenecks are identified with supporting data. (3) Throughput is comparable across operators handling similar case mixes. (4) End-to-end journey time is reported per patient. **Deliverables:** Analytics engine, operations dashboard. **Effort:** 7 days. **Risks:** **Throughput metrics without case-mix adjustment are unfair and will be resented** — normalise, and present with that caveat. **Open decisions:** Target times per station (*operational*).
**Out of scope (continued):** Individual performance judgement — that is CP140, with normalisation. **Technologies:** Per §4. **Backend:** Aggregation jobs. **Database:** Throughput rollup tables. **API:** Analytics endpoints. **Frontend:** Operations dashboard. **Mobile:** — **AI:** — **OCR/NLP:** — **Security:** Supervisor scope. **Events/Audit:** —

---

### CP140 · Staff Performance, Error Linkage & Retraining Loop
**Objective:** §14.2's error-quality linkage feeding targeted retraining, joined with throughput and satisfaction. **Scope:** Unified operator view (throughput, correction rate from CP63, satisfaction correlation from CP117, attendance) · **case-mix normalisation** · retraining flag workflow with recorded completion · trend tracking to demonstrate whether retraining actually worked (which is the blueprint's stated purpose) · **the operator's own view of their record**. **Dependencies:** CP63, CP139. **Security:** Supervisor-scoped; operators see only themselves. **Testing:** Aggregation correctness; permission scoping; normalisation. **Manual verification:** Review one operator's full record with Dr. Nahid and assess fairness. **Acceptance criteria:** (1) Metrics are case-mix normalised. (2) Retraining flags link to specific evidence, not aggregate scores. (3) Post-retraining improvement is measurable. (4) Operators can see their own record. **Deliverables:** Performance dashboard, retraining workflow. **Effort:** 6 days. **Risks:** **Cultural — the most sensitive feature in the system.** Involve Dr. Nahid in framing and communication before release. **Open decisions:** Usage policy (*organisational*).
**Why this checkpoint exists:** §4.3 states the purpose of correction tallies is targeted retraining; this is where that loop closes. **Out of scope (continued):** Pay, discipline or ranking — organisational decisions, not system features. **Technologies:** Per §4. **Backend:** Aggregation and normalisation. **Database:** Operator performance rollups. **API:** Performance endpoints. **Frontend:** Supervisor and self views. **Mobile:** Operators can see their own record on mobile. **AI:** — **OCR/NLP:** — **Events/Audit:** Retraining flags and completions recorded.

---

### CP141 · Micro-Costing Engine
**Objective:** §14.3's real-time burn rate — per-operator daily cost, philanthropic footprint of free community diagnostics, and outreach logistics. **Scope:** Per-operator cost from shift duration × cost rate, allocated across throughput · cost per patient journey · cost per station · **free-care and community-diagnostic cost tracked as the philanthropic footprint** · consumable and medication cost from CP119 · outreach logistics cost (fuel, transport, time from fleet data, Phase 4) · burn-rate dashboard with drill-down. **Dependencies:** CP138, CP139. **Testing:** Cost allocation correctness against hand-computed examples; period boundary handling. **Manual verification:** Verify one day's total cost against actual known expenditure. **Acceptance criteria:** (1) Per-patient cost is computed and traceable to its components. (2) The philanthropic footprint is quantified separately. (3) Daily totals reconcile with actual expenditure within a documented tolerance. **Deliverables:** Costing engine, burn-rate dashboard. **Effort:** 8 days. **Risks:** Allocation methodology disputes — document the method explicitly and get agreement before reporting. **Open decisions:** Cost allocation methodology (*business*).
**Why this checkpoint exists:** §14.3’s micro-costing engine; it depends on attendance (CP138) and throughput (CP139). **Out of scope (continued):** Accounting integration and payroll. **Technologies:** Per §4. **Backend:** Cost allocation jobs. **Database:** Cost entry tables. **API:** Costing endpoints. **Frontend:** Burn-rate dashboard. **Mobile:** — **AI:** — **OCR/NLP:** — **Security:** Executive/finance role. **Events/Audit:** Cost methodology version recorded with each figure.

---

### CP142 · Predictive Supply Chain & Procurement Alerts
**Objective:** §14.3 — prescribing trends + seasonality + scheduled follow-ups → procurement alerts before critical stocks run out. **Scope:** Consumption forecasting per item (**scheduled follow-ups are a genuinely predictive input** — the system knows who is due and what they take) · seasonality where data supports it · lead-time-aware reorder points · alerts ahead of stock-out with recommended quantities · forecast accuracy tracking. **Dependencies:** CP119, CP134. **AI:** Time-series forecasting (statistical, not LLM). **Testing:** Backtesting on historical consumption; alert timing verification. **Manual verification:** Compare forecasts against actual consumption for one month before acting on them. **Acceptance criteria:** (1) Forecast accuracy is measured and reported. (2) Alerts fire ahead of stock-out by at least the lead time. (3) Scheduled follow-ups measurably improve forecasts over naive baselines. (4) Recommended quantities are explained. **Deliverables:** Forecasting engine, procurement alerts. **Effort:** 8 days. **Risks:** Insufficient history for seasonality early on — start with simple methods and state the uncertainty. **Open decisions:** Lead times per supplier (*operational*).
**Why this checkpoint exists:** §14.3’s predictive supply chain; scheduled follow-ups make the forecast genuinely informative. **Out of scope (continued):** Supplier ordering and procurement execution. **Technologies:** Python time-series forecasting. **Backend:** Forecast jobs and alerts. **Database:** Forecast and alert tables. **API:** Forecast endpoints. **Frontend:** Procurement dashboard. **Mobile:** — **OCR/NLP:** — **Security:** Pharmacy/admin role. **Events/Audit:** Alerts recorded.

---

### CP143 · Executive Dashboard ("CEO Co-Pilot")
**Objective:** §14.4's strategic management engine. **Scope:** One view across clinical performance, community impact, research productivity, financial sustainability and staff efficiency · KPI tracking (glycaemic-target attainment, BP control, adherence, outreach conversions, research output) · **anomaly detection driving auto-generated executive summaries and dynamic meeting agendas** · macro-RCA on underperforming initiatives · action items auto-assigned with escalation · **the quarterly Organizational Learning Matrix**: "What have we learned, and where do resources go next?" **Dependencies:** CP139–CP142. **AI:** Narrative summary from computed metrics, never invented numbers. **Security:** Executive role. **Testing:** KPI correctness against source data; anomaly detection; action-item lifecycle. **Manual verification:** Dr. Nahid uses it to run one real management meeting. **Acceptance criteria:** (1) Every KPI traces to source data and is verifiable. (2) The generated agenda reflects genuine anomalies. (3) Action items track to closure with escalation. (4) The quarterly matrix assembles automatically. (5) Dr. Nahid can run a management meeting from this screen alone. **Deliverables:** Executive dashboard, agenda generation, action tracking. **Effort:** 12 days. **Risks:** Vanity metrics displacing meaningful ones — choose KPIs with Dr. Nahid deliberately. **Open decisions:** KPI definitions and targets (*business*).

---

## PHASE 4 — ENTERPRISE & COMMUNITY
**Why this checkpoint exists:** §14.4’s CEO Co-Pilot — the view that turns all prior analytics into management decisions. **Out of scope (continued):** Automated decision-making; the dashboard informs, people decide. **Technologies:** Per §4; CP70 for narrative only. **Backend:** KPI aggregation and anomaly detection. **Database:** KPI rollups and action items. **API:** Executive endpoints. **Frontend:** Executive dashboard. **Mobile:** Read-only mobile view. **OCR/NLP:** — **Events/Audit:** Action items and closures recorded.

---

### CP144 · Field App Foundation
**Objective:** §13's React Native tablet application for community outreach — offline-heavy by nature. **Why:** Field conditions are the extreme case of the offline requirement: a rural camp may have no connectivity for a full day. **Scope:** Tablet-optimised RN app sharing `packages/` with the station app · **extended offline capability: a full day of screening with no connectivity** · larger local storage budget and cache policy · camp/session model grouping screenings · team assignment · device provisioning for field tablets · offline reference data (growth standards, risk thresholds, report templates). **Out of scope:** Screening workflows (CP145). **Dependencies:** CP66, CP11. **Mobile:** Field app shell. **Security:** Field devices are the highest theft-and-loss risk in the fleet — encrypted storage, aggressive auto-lock, remote revocation, minimal cached PHI. **Testing:** Full-day offline soak; storage limits; sync of a large day's backlog. **Manual verification:** Operate a full simulated camp day in airplane mode, then sync. **Acceptance criteria:** (1) A full day's screening (**target: 200 people**) works entirely offline. (2) The day's data syncs completely on reconnection. (3) Local data is encrypted and remotely revocable. (4) The app runs on the chosen field tablet. **Deliverables:** Field app foundation. **Effort:** 10 days. **Risks:** Battery and storage on all-day field use — measure on the real device. **Open decisions:** Field tablet model (*procurement*).
**Technologies:** Expo, SQLCipher, shared packages. **Backend:** Camp and team endpoints. **Database:** Camp, team and assignment tables. **API:** Field sync endpoints (extends CP65). **Frontend:** Camp administration on web. **AI:** — **OCR/NLP:** — **Events/Audit:** Field writes carry the same envelope as station writes.

---

### CP145 · Field Screening Workflows & Battery
**Objective:** §13's screening battery: height, weight, BMI, waist, BP, blood grouping, RBS, eye screening, family health assessment. **Scope:** Rapid screening flow optimised for queues of people (**seconds per person matters at a camp of 300**) · person record creation with minimal identifiers and consent · the full battery with instant derived values · risk flagging by threshold · family grouping · in-field diagnostics capture (HbA1c, Vitamin D/B12, OGTT sample collection) with specimen tracking · **linkage to an existing patient record when the person is already a DTHC patient**. **Dependencies:** CP144, CP42. **Security:** Field consent capture; minimal data collection. **Testing:** Throughput measurement; derived value correctness; duplicate/linkage handling. **Manual verification:** Screen 20 people in a simulated camp and measure time per person. **Acceptance criteria:** (1) A full screening takes under **3 minutes** per person (*proposed*). (2) Risk flags compute instantly offline. (3) Existing patients are recognised and linked, not duplicated. (4) Specimen tracking prevents mislabelling. **Deliverables:** Screening workflows, specimen tracking. **Effort:** 10 days. **Risks:** **Specimen mislabelling in field conditions is a genuine patient-safety risk** — design barcode/label handling carefully. **Open decisions:** Field consent model (*legal/clinical*).
**Why this checkpoint exists:** §13’s screening battery is the substance of the community wing; everything else in Phase 4 supports it. **Out of scope (continued):** Diagnosis or treatment in the field — screening and referral only. **Technologies:** Per CP144. **Backend:** Screening record handling. **Database:** Screening and specimen tables. **API:** Screening endpoints. **Frontend:** Camp review on web. **Mobile:** Field screening flows. **AI:** — **OCR/NLP:** — **Events/Audit:** `SCREENING_RECORDED` events with full attribution.

---

### CP146 · Instant Field Report & Portable Printing
**Objective:** §13 — each screened person immediately receives a simple, colourful report built for maximum comprehension. **Scope:** Report template (weight-for-age, BMI indicator, BP interpretation) using **colour and simple graphics rather than numbers alone**, in Bangla, designed for low literacy · portable printer integration (offline printing) · digital delivery by SMS as an alternative · referral instructions for flagged individuals. **Dependencies:** CP145, CP89. **Testing:** Print output on the portable device; comprehension testing with real community members. **Manual verification:** **Give the report to ten people at a camp and ask them what it says.** If they cannot explain their own result, redesign. **Acceptance criteria:** (1) Printing works offline in the field. (2) Community members can interpret their result unaided (verified by testing). (3) Bangla renders correctly on the portable printer. (4) Referral instructions are clear and actionable. **Deliverables:** Field report template, printer integration. **Effort:** 8 days. **Risks:** Portable printer reliability in heat and dust; Bangla rendering on thermal printers. **Open decisions:** Portable printer model (*procurement*, D-58).
**Why this checkpoint exists:** §13 requires each screened person to receive an immediate, comprehensible report. **Out of scope (continued):** Clinical interpretation beyond the agreed indicators. **Technologies:** Portable printer SDK; the CP89 render pipeline. **Backend:** Report rendering (offline-capable). **Database:** Report records. **API:** Report generation endpoint. **Frontend:** — **Mobile:** Print from the field app. **AI:** — **OCR/NLP:** — **Security:** Reports contain minimal personal data. **Events/Audit:** `FIELD_REPORT_ISSUED`.

---

### CP147 · Field Sync, Triage & Auto-Scheduling
**Objective:** §13's "deep interoperability" — field records sync instantly, create screening records, update timelines, flag abnormals for triage, and auto-schedule clinic follow-ups for high-risk individuals. **Scope:** Bulk sync of a camp day · screening records into the clinical record with `source = FIELD` · abnormal triage queue for clinical review · **automatic clinic follow-up scheduling for high-risk individuals** with contact capture · conversion tracking (screened → attended clinic), which is the real measure of whether outreach works. **Dependencies:** CP145, CP111. **Testing:** Bulk sync of 200 records; triage routing; auto-scheduling rules; conversion tracking. **Manual verification:** Sync a full camp day and verify every high-risk person has a follow-up scheduled and a contact attempt queued. **Acceptance criteria:** (1) A 200-record day syncs completely and correctly. (2) High-risk individuals are auto-scheduled per the agreed rules. (3) Abnormals reach the triage queue. (4) Screening-to-clinic conversion is measured. **Deliverables:** Field sync, triage, auto-scheduling, conversion reporting. **Effort:** 8 days. **Risks:** Auto-scheduling capacity — do not schedule more follow-ups than the clinic can absorb. **Open decisions:** High-risk criteria and scheduling rules (*clinical*).
**Why this checkpoint exists:** §13’s "deep interoperability" requirement — field data must reach the clinical record and generate action. **Out of scope (continued):** Automatic clinical decisions; triage is human-reviewed. **Technologies:** Per §4. **Backend:** Bulk sync and triage routing. **Database:** Triage queue. **API:** Bulk sync endpoints. **Frontend:** Triage console. **Mobile:** Sync status in the field app. **AI:** — **OCR/NLP:** — **Security:** RBAC on the triage queue. **Events/Audit:** `SCREENING_REFERRED`, follow-up creation events.

---

### CP148 · Google Maps Fleet Management & Geospatial
**Objective:** §14.2's live map of field teams for safety, coverage validation and route optimisation. **Scope:** Team location tracking with **explicit staff consent and clear boundaries on when tracking is active** (working hours only) · live map · coverage visualisation against target populations · route history and optimisation suggestions · geofenced camp check-in · **obesity/prevalence mapping** feeding §13's population intelligence. **Dependencies:** CP144. **Technologies:** Google Maps Platform. **Security:** **Staff location is personal data** — consent, working-hours-only tracking, and retention limits are required, not optional. **Testing:** Location accuracy; battery impact; consent enforcement; map performance with many points. **Manual verification:** Track a real field trip and verify accuracy, battery cost and consent behaviour. **Acceptance criteria:** (1) Tracking is active only during declared working hours with recorded consent. (2) Battery impact is within an acceptable measured bound. (3) Coverage maps correctly reflect screening activity. (4) Location retention follows the agreed policy. **Deliverables:** Fleet map, coverage visualisation, geospatial analysis. **Effort:** 8 days. **Risks:** **Staff privacy and morale** — be explicit and transparent with the team about what is tracked and why. Maps API cost at scale. **Open decisions:** Tracking policy and retention (*HR/legal*).
**Why this checkpoint exists:** §14.2 requires geospatial fleet management for safety, coverage validation and route optimisation. **Out of scope (continued):** Off-duty tracking of staff — explicitly excluded. **Backend:** Location ingestion and retention. **Database:** Location and route tables. **API:** Location endpoints. **Frontend:** Fleet map. **Mobile:** Location reporting with consent controls. **AI:** — **OCR/NLP:** — **Events/Audit:** Consent state recorded with tracking sessions.

---

### CP149 · Population Health & Surveillance Dashboards
**Objective:** §13's population intelligence — surveillance, prevalence studies, obesity mapping, public-health intervention research. **Scope:** Prevalence estimates by geography and demographic with **appropriate caveats about screening-sample bias** (a self-selected camp population is not a random sample, and any published prevalence figure must say so) · geographic risk mapping · trend tracking across camps · intervention outcome comparison · export for public-health reporting. **Dependencies:** CP147, CP123. **Testing:** Statistical correctness; denominator handling. **Manual verification:** Dr. Nahid reviews prevalence outputs against known regional figures. **Acceptance criteria:** (1) Denominators and sampling method are stated on every prevalence figure. (2) Selection-bias caveats are displayed, not footnoted. (3) Geographic aggregation respects the small-cell suppression rule. **Deliverables:** Population health dashboards. **Effort:** 8 days. **Risks:** Publishing biased prevalence estimates as population figures. **Open decisions:** Reporting standards (*research*).
**Why this checkpoint exists:** §13’s population intelligence — the output that positions DTHC as a regional public-health authority. **Out of scope (continued):** Publication workflow (CP131). **Technologies:** CP123 framework. **Backend:** Aggregation queries. **Database:** Reads `research` only. **API:** Dashboard endpoints. **Frontend:** Population dashboards. **Mobile:** — **AI:** — **OCR/NLP:** — **Security:** Research role; small-cell suppression. **Events/Audit:** Exports audited.

---

### CP150 · FHIR Resource Mapping (R4)
**Objective:** §15.1's HL7 FHIR interoperability, mapped properly. **Scope:** R4 mapping for Patient, Practitioner, Organization, Device, Encounter, Observation, Condition, AllergyIntolerance, MedicationRequest, DiagnosticReport, DocumentReference, CarePlan, Appointment, Consent, and **Provenance — which maps DTHCMS's attribution envelope almost exactly and is the resource that makes this system unusually well suited to FHIR** · terminology mapping (ICD, LOINC) · profile definitions · validation against the R4 specification. **Out of scope:** The API (CP151). **Dependencies:** CP42, **D-29**. **Testing:** Mapping correctness; validation against official FHIR validators; round-trip fidelity. **Manual verification:** Validate exported resources with the official FHIR validator. **Acceptance criteria:** (1) All mapped resources validate against R4. (2) Provenance correctly represents user, device, role and station for every clinical fact. (3) Terminology mappings are complete for exported data. (4) Round-trip preserves clinical meaning. **Deliverables:** Mapping layer, profiles, validation report. **Effort:** 10 days. **Risks:** Mapping loses local specificity — use extensions deliberately and document them. **Open decisions:** **D-29**; which profiles (national or international).
**Why this checkpoint exists:** §15.1 names HL7 FHIR; mapping is the prerequisite for any external integration. **Technologies:** FHIR R4, official validators. **Backend:** Mapping layer. **Database:** Mapping tables for terminology. **API:** Internal mapping only (API is CP151). **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Security:** No external exposure at this checkpoint. **Events/Audit:** Provenance derives from the attribution envelope.

---

### CP151 · FHIR API, Provenance & Conformance Testing
**Objective:** A conformant, secure FHIR API. **Scope:** RESTful FHIR endpoints (read, search, history) · **SMART on FHIR authorisation** or an equivalent OAuth2 scheme · capability statement · pagination and search parameters · **Provenance on every resource** · audit of all external access · rate limiting per client. **Dependencies:** CP150. **Security:** External API access is a significant new attack surface — separate authentication, separate rate limits, separate monitoring, and per-client scoping. **Testing:** Conformance test suite; authorisation tests; a test asserting no client can read outside its scope. **Manual verification:** Connect a third-party FHIR client and retrieve a patient record within scope. **Acceptance criteria:** (1) The capability statement is accurate and validates. (2) Authorisation scopes are enforced per client. (3) Every resource carries Provenance. (4) All external access is audited. (5) Conformance tests pass. **Deliverables:** FHIR API, auth, conformance report. **Effort:** 10 days. **Risks:** Data exposure through an over-permissive scope — default deny, per-client scoping, and an external review. **Open decisions:** Authorisation model; which partners (*business*).
**Why this checkpoint exists:** The externally-facing half of FHIR; separated from mapping so security review is focused. **Out of scope (continued):** Write access from external systems unless explicitly agreed. **Technologies:** FHIR R4, OAuth2/SMART. **Backend:** FHIR endpoints. **Database:** Client registration and scopes. **API:** The FHIR API itself. **Frontend:** Client administration. **Mobile:** — **AI:** — **OCR/NLP:** — **Events/Audit:** All external access audited per client.

---

### CP152 · External Integration & Partner Onboarding
**Objective:** Make the FHIR API usable by a real partner. **Why:** §15.3 lists interoperability in Phase 4, but **FHIR without a consumer is shelfware** — this checkpoint exists only when a named partner exists. **Scope:** Partner registration and credentialing · data-sharing agreement enforcement in scopes · sandbox environment with synthetic data · integration documentation · monitoring per partner · **a patient-consent check on every external disclosure**. **Dependencies:** CP151. **Security:** Consent-gated disclosure; per-partner audit. **Testing:** Sandbox onboarding; consent enforcement; monitoring. **Manual verification:** Onboard a test partner end to end using only the documentation. **Acceptance criteria:** (1) A partner can integrate using the documentation alone. (2) No data is disclosed without patient consent for that purpose. (3) Per-partner usage is monitored and rate-limited. (4) The sandbox contains no real data. **Deliverables:** Partner onboarding, sandbox, documentation. **Effort:** 8 days. **Risks:** **Building this before a partner exists is wasted effort** — defer until one is named. **Open decisions:** Named partners (*business*); data-sharing agreements (*legal*).
**Out of scope (continued):** Building integrations for hypothetical partners. **Technologies:** Per CP151. **Backend:** Partner management. **Database:** Partner and agreement records. **API:** Onboarding endpoints. **Frontend:** Partner console. **Mobile:** — **AI:** — **OCR/NLP:** — **Events/Audit:** Disclosures audited per partner and per patient.

---

### CP153 · Multi-Branch / Multi-Facility Support
**Objective:** §15.3's multi-branch scaling. **Why:** `facility_id` has been present since CP06; this checkpoint activates it. **Scope:** Facility management · **facility-scoped RBAC** (a user's roles are per-facility) · cross-facility patient access with consent and audit (**a patient visiting a second branch must be recognised, not re-registered**) · facility-scoped queues, inventory, staff and reporting · consolidated cross-facility reporting for the executive dashboard · facility-specific configuration (templates, formulary pricing, station sequences). **Dependencies:** CP19, CP38. **Testing:** Isolation tests (a user in facility A must not see facility B's operational data); cross-facility patient access with consent; reporting aggregation. **Manual verification:** Create a second facility, register a patient at A, and access them at B with consent. **Acceptance criteria:** (1) Facility data is isolated by default. (2) Cross-facility patient access requires consent and is audited. (3) Reporting aggregates correctly and can be filtered by facility. (4) No cross-facility data leakage, proven by test. **Deliverables:** Multi-facility support, isolation tests. **Effort:** 12 days. **Risks:** Isolation bugs leaking data across branches — extensive testing required. **Open decisions:** Cross-facility access policy (*clinical/legal*).
**Out of scope (continued):** Cross-facility clinical workflow beyond record access. **Technologies:** Per §4. **Backend:** Facility scoping across modules. **Database:** `facility_id` activation and isolation constraints. **API:** Facility-scoped endpoints. **Frontend:** Facility switching and administration. **Mobile:** Facility context in the station app. **AI:** — **OCR/NLP:** — **Security:** Isolation is the primary security property here. **Events/Audit:** Cross-facility access audited.

---

### CP154 · AlloyDB Migration & Analytics Scaling
**Objective:** Move to AlloyDB when — and only when — analytics load justifies it (D-31). **Scope:** Benchmark the actual research and analytics workload on Cloud SQL versus AlloyDB · if justified, migrate with a documented, rehearsed cutover · columnar engine configuration for analytical queries · read replica strategy · **a decision record either way** — deciding to stay on Cloud SQL is a valid and equally documented outcome. **Dependencies:** CP121, **D-31**. **Testing:** Benchmark comparison; migration rehearsal; data integrity verification post-migration. **Manual verification:** Compare query times on the real research workload before and after. **Acceptance criteria:** (1) A benchmark-based decision is documented. (2) If migrating: zero data loss verified, and a rollback plan exists and is rehearsed. (3) Analytical query performance improves measurably, or the migration does not happen. **Deliverables:** Benchmark report, migration (if justified), decision record. **Effort:** 8 days. **Risks:** Migrating without measured need adds cost for nothing. **Open decisions:** **D-31.**
**Why this checkpoint exists:** D-31 defers the AlloyDB decision until analytics load justifies it; this is where the decision is made on evidence. **Out of scope (continued):** Any AlloyDB-specific SQL that would prevent moving back. **Technologies:** AlloyDB, Cloud SQL, benchmark tooling. **Backend:** Connection configuration only. **Database:** The whole checkpoint. **API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Security:** CMEK and networking preserved on migration. **Events/Audit:** Migration recorded; integrity verified.

---

### CP155 · Backup, DR Drills & RPO/RTO Validation
**Objective:** Prove §15.1's backup and recovery promise rather than assuming it. **Scope:** PITR configuration validated to **RPO ≤5 minutes** · cross-region backup replication · immutable retention-locked object backups · **a full restore drill into an isolated project, timed** · documented RTO with a manual clinic fallback procedure · backup verification automation · **quarterly drill schedule with recorded results**. **Dependencies:** CP03, **D-37**. **Testing:** Full restore drill; partial (single-table) restore; point-in-time recovery to a specific minute; backup integrity verification. **Manual verification:** Perform a complete restore into a clean project and verify data integrity and timing against the RTO target. **Acceptance criteria:** (1) Measured RPO ≤5 minutes, demonstrated. (2) A full restore completes within the agreed RTO, measured and recorded. (3) Backups are immutable and cross-region. (4) A drill is scheduled quarterly and results are recorded. (5) A manual clinic fallback procedure exists and staff know it. **Deliverables:** Backup configuration, DR runbook, drill results, fallback procedure. **Effort:** 8 days. **Risks:** **An untested backup is not a backup** — the drill is the deliverable, not the configuration. **Open decisions:** **D-37 (RTO target)**; residency constraints on cross-region backup (D-01).
**Why this checkpoint exists:** §15.1 promises 5-minute geo-redundant backups; a promise is not a capability until it is restored from. **Out of scope (continued):** Application-level failover automation. **Technologies:** Cloud SQL PITR, GCS retention lock, Terraform. **Backend:** — **Database:** Backup and restore configuration. **API:** — **Frontend:** — **Mobile:** — **AI:** — **OCR/NLP:** — **Security:** Backup keys separated from production keys. **Events/Audit:** Drill results recorded.

---

### CP156 · Full Security Assessment & Penetration Test #2
**Objective:** Independent security verification of the complete system at production scale. **Scope:** External penetration test covering everything added since CP94 — FHIR API, field app, multi-facility isolation, public verify page, webhooks, admin console, biometric data handling · social-engineering assessment if agreed · remediation and retest · **an updated threat model** · a security posture statement for stakeholders. **Dependencies:** CP153. **Testing:** Penetration test plus regression tests for every finding. **Manual verification:** Review findings with the tester; verify closure of every critical and high finding. **Acceptance criteria:** (1) No unresolved critical or high findings. (2) Multi-facility isolation is independently verified. (3) Every finding has a regression test. (4) The threat model is updated. **Deliverables:** Pentest report, remediation, updated threat model. **Effort:** 8 days (plus vendor time). **Risks:** Late findings requiring architectural change — schedule with slack. **Open decisions:** Vendor and scope (*commercial*).
**Why this checkpoint exists:** Independent verification of everything added since CP94, before full production scale. **Out of scope (continued):** Remediation of findings rated informational. **Technologies:** External vendor tooling. **Backend:** Remediation only. **Database:** Remediation only. **API:** Remediation only. **Frontend:** Remediation only. **Mobile:** Remediation only. **AI:** AI gateway and PHI minimisation included in scope. **OCR/NLP:** Document pipeline included in scope. **Security:** The whole checkpoint. **Events/Audit:** Findings tracked to closure.

---

### CP157 · Performance & Load Testing at Target Scale
**Objective:** Validate performance at multi-branch, multi-year-data scale. **Scope:** Load testing at projected volumes (multiple facilities, years of accumulated data, concurrent field sync) · database performance with mature partitions · OCR pipeline throughput at production document volumes · AI pipeline cost and latency at scale · WebSocket connection scale · **capacity planning with a documented growth model**. **Dependencies:** CP153, **D-59**. **Testing:** This checkpoint is testing. **Manual verification:** Sustained load test at 200% of projected peak for one hour. **Acceptance criteria:** (1) Latency budgets met at 200% of projected peak. (2) Database performance is acceptable with 3+ years of simulated data. (3) A capacity model with cost projections is documented. (4) Bottlenecks are identified with remediation plans. **Deliverables:** Load test suite, capacity model, optimisations. **Effort:** 8 days. **Risks:** Discovering an architectural limit late — mitigated by the load testing already done at CP93. **Open decisions:** Growth projections (*business*).
**Why this checkpoint exists:** Phase 1’s load testing validated one clinic; this validates the multi-facility, multi-year reality. **Out of scope (continued):** Optimisation work beyond removing identified bottlenecks. **Technologies:** k6, profiling tools. **Backend:** Optimisation. **Database:** Query and partition tuning. **API:** Optimisation. **Frontend:** Optimisation. **Mobile:** Device performance verification. **AI:** Pipeline cost and latency at scale. **OCR/NLP:** Pipeline throughput at volume. **Security:** Rate limits validated under load. **Events/Audit:** Event append performance at volume.

---

### CP158 · Production Infrastructure Hardening & Cost Optimisation
**Objective:** A production environment that is secure, observable, resilient and not wasteful. **Scope:** Security hardening review of every GCP resource · least-privilege audit · network policy review · **cost analysis and optimisation** (right-sizing, committed use, storage lifecycle, AI cost controls) · autoscaling configuration · alert tuning (**every alert must be actionable — an alert nobody acts on trains people to ignore alerts**) · incident response runbooks · on-call rotation if applicable. **Dependencies:** CP155. **Security:** Full infrastructure review. **Testing:** Chaos testing (kill instances, sever the database connection, exhaust the queue); alert verification. **Manual verification:** Trigger each alert deliberately and confirm it reaches the right person with actionable content. **Acceptance criteria:** (1) Least-privilege verified on every service account. (2) Every alert is actionable and tested. (3) Cost is optimised with a documented monthly projection. (4) The system recovers automatically from single-instance failure. **Deliverables:** Hardened infrastructure, cost report, runbooks, alert catalogue. **Effort:** 6 days. **Risks:** Over-optimisation harming resilience. **Open decisions:** Budget targets (*business*).
**Why this checkpoint exists:** Production readiness is a distinct discipline from feature completeness. **Out of scope (continued):** New features. **Technologies:** Terraform, Cloud Monitoring. **Backend:** Configuration only. **Database:** Configuration only. **API:** — **Frontend:** — **Mobile:** — **AI:** Cost controls verified. **OCR/NLP:** — **Events/Audit:** Alert catalogue documented.

---

### CP159 · Runbooks, Training Materials & Operational Handover
**Objective:** The clinic can run, support and extend the system without depending on one person's memory. **Why:** Key-person risk (D-52) is a real risk to this programme. **Scope:** Operational runbooks (deployment, rollback, restore, incident response, common failures) · **staff training materials in Bangla per station**, with printed quick-reference cards · administrator guide · developer onboarding guide · architecture documentation kept current · **a support escalation process** · a knowledge-transfer session recorded. **Dependencies:** CP158. **Manual verification:** A person who did not build the system performs a deployment and a restore using only the runbooks. **Acceptance criteria:** (1) A new administrator can perform routine operations from the documentation alone. (2) Every station has training material in Bangla. (3) Runbooks are verified by someone other than their author. (4) A support escalation process exists with named owners. **Deliverables:** Runbooks, training materials, guides, recorded handover. **Effort:** 8 days. **Risks:** Documentation rotting — assign ownership and review dates. **Open decisions:** Support model (*business*).
**Out of scope (continued):** Feature work. **Technologies:** Documentation tooling. **Backend:** — **Database:** — **API:** API documentation published. **Frontend:** — **Mobile:** — **AI:** AI operations runbook included. **OCR/NLP:** OCR operations runbook included. **Security:** Incident response runbook included. **Events/Audit:** Audit export procedure documented. **Testing:** Documentation verified by having someone other than the author perform each procedure.

---

### CP160 · Production Deployment & Phase 4 Sign-Off
**Objective:** Full production operation across all phases, formally accepted. **Scope:** Final deployment of all Phase 4 capability · verification of every phase's acceptance criteria end to end · **a full audit-trail verification across the complete system** (§15.3's phase acceptance rule) · defect burn-down to zero critical · final performance and security verification · **a lessons-learned review** and a roadmap for what comes next. **Dependencies:** All checkpoints. **Manual verification:** Dr. Nahid verifies a complete patient journey — registration through community follow-up — with full audit trail, across all 160 checkpoints' capability. **Acceptance criteria:** (1) All phases meet their acceptance criteria. (2) No open critical defects. (3) The end-to-end audit trail is verified. (4) All runbooks and documentation are current. (5) Written final sign-off from Dr. Nahid. **Deliverables:** Production system, final verification report, lessons learned, forward roadmap. **Effort:** 6 days. **Risks:** Accumulated technical debt surfacing at the end — mitigated by hardening checkpoints at the end of every phase. **Open decisions:** Post-project support model.

---

# 17. CHECKPOINT DEPENDENCY GRAPH

## 17.1 Critical path (the spine)

```
CP01 Repository
  ↓
CP02 Architecture guardrails
  ↓
CP04 Local dev  ──►  CP05 Backend skeleton  ──►  CP06 Database foundation
                            ↓                          ↓
                     CP07 Observability            CP15 Users & roles
                                                       ↓
                                              CP16 Authentication
                                              ↓        ↓         ↓
                                     CP17 2FA   CP18 Devices   CP19 RBAC
                                                     ↓          ↓
                                                     └──► CP20 Enforcement
                                                                ↓
                                              ┌─────────────────┴──────────────┐
                                              ▼                                ▼
                                    CP23 EVENT STORE          CP22 Audit log (parallel)
                                              ↓
                                    CP24 Envelope + idempotency
                                              ↓
                          ┌───────────────────┼──────────────────┐
                          ▼                   ▼                  ▼
                  CP25 Projections     CP26 Realtime       CP28 Patient model
                          ↓                   ↓                  ↓
                          └──────────► CP27 Client RT     CP29 Registration
                                                                 ↓
                                                          CP30 Duplicates
                                                                 ↓
                                                          CP38 Visit lifecycle
                                                                 ↓
                                                          CP39 Queue ──► CP40 Traffic board
                                                                 ↓
                                                          CP42 Observations
                                                                 ↓
                                          ┌──────────────────────┼─────────────────┐
                                          ▼                      ▼                 ▼
                                  CP43 Calculations      CP45–CP60 Stations   CP52 Terminology
                                          ↓                      ↓
                                  CP47 Percentiles        CP61 Attribution
                                                                 ↓
                                                          CP62 Corrections ──► CP63 Quality
                                                                 ↓
                                                          CP64 Local DB
                                                                 ↓
                                                    CP65 ──► CP66 Sync ──► CP67 ──► CP68
                                                                 ↓
                                                          CP69 Jobs ──► CP70 AI Gateway
                                                                              ↓
                                                                      CP71 Synthesis ──► CP72 Grounding
                                                                              ↓
                                                                      CP73 Dashboard ──► CP74 Timeline
                                                                              ↓
                                                    CP75 Formulary ──► CP77 Rules ──► CP78 Safety engine
                                                                              ↓
                                                                      CP80 Prescription ──► CP81 Editor
                                                                              ↓
                                                                      CP83 QA ──► CP84 Signature
                                                                              ↓
                                                                      CP89 Print ──► CP90 Back page
                                                                              ↓
                                                              CP93 Perf ──► CP94 Security ──► CP95 GO LIVE
```

## 17.2 Phase-level dependency flow

```
PHASE 0  Foundation ─────────────────────────────────────────────┐
   │ repo · architecture · cloud · database · observability      │
   │ design system · shells · contracts · test harness           │
   ▼                                                             │
PHASE 1  Clinic Core ────────────────────────────────────────────┤
   │ identity → RBAC → EVENT STORE → patient → visit → stations  │
   │ → attribution → correction → OFFLINE → AI synthesis         │
   │ → dashboard → prescription → QA → signature → print         │
   ▼                                                             │
PHASE 2  Memory & Relationship ──────────────────────────────────┤
   │ documents → OCR → extraction → chronology → red-lines       │
   │ follow-up → CRM → SMS → inbound → pharmacy → inventory      │
   ▼                                                             │
PHASE 3  Intelligence & Research ────────────────────────────────┤
   │ anonymisation → cohorts → dashboards → hypothesis engine    │
   │ more AI agents → predictor → QA engine → guidelines         │
   │ HR → throughput → costing → supply chain → executive        │
   ▼                                                             │
PHASE 4  Enterprise & Community ─────────────────────────────────┘
     field app → screening → maps → population health
     FHIR → multi-branch → DR → security → production
```

## 17.3 Cross-cutting dependencies that are easy to miss

| Later checkpoint | Silently depends on | Consequence if missed |
|---|---|---|
| CP125 GLP-1 safety dashboard | Structured **adverse-event capture** designed into CP113's call workflow | The dashboard cannot be built; the data does not exist |
| CP127 Affordability | **Price history** in CP75 | Costs computed at today's prices, invalidating the analysis |
| CP128 Exercise correlation | **Structured adherence targets** in CP60 | Free-text adherence is unanalysable |
| CP134 Predictor | **Socio-economic baseline** in CP28 | A key predictive feature is absent |
| CP139 Throughput | **Encounter records** in CP38 | Requires retrofitting instrumentation |
| CP141 Micro-costing | **Cost rates** in CP138 | No cost basis |
| CP150 FHIR Provenance | **Attribution envelope** in CP24 | Provenance cannot be populated |
| CP153 Multi-branch | **`facility_id`** from CP06 | A painful migration on a populated clinical database |
| All research dashboards | **Consent capture** in CP36 | Analyses run on non-consented data |

**This table is the reason the plan is sequenced as it is.** Each of these is cheap when built early and expensive when retrofitted. They are the clearest argument against reordering the roadmap for short-term convenience.

## 17.4 Content dependencies (not engineering — these can stall checkpoints regardless of developer capacity)

| Content | Author | Gates |
|---|---|---|
| Counseling templates (D-53) | Dr. Nahid | CP55, CP57 |
| Drug warning library (D-54) | Dr. Nahid | CP86, CP91 |
| Medication safety rules (D-22/D-23) | Dr. Nahid | CP77, CP78, CP79 |
| Red-line rules (D-55) | Dr. Nahid | CP108 |
| Formulary + prices (D-56) | Pharmacist/admin | CP75, CP76 |
| Critical value table (D-27) | Dr. Nahid | CP50 |
| Growth standard (D-21) | Dr. Nahid | CP47, CP48 |
| Analyte dictionary (D-20) | Dr. Nahid/lab | CP104 |
| Non-endocrinologist notice (D-06) | Dr. Nahid | CP90 |
| Examination finding sets | Dr. Nahid | CP51, CP92 |
| Bangladeshi food composition data | Sourced/authored | CP59 |
| Exercise library | Dr. Nahid | CP60 |

**Recommendation:** start authoring this content during Phase 0. It is the most common cause of schedule slip in clinical software projects, and it is entirely independent of engineering progress.

---

# 18. TESTING STRATEGY

## 18.1 Philosophy

Three principles govern testing here:

1. **Test what would hurt.** A wrong dose, a lost offline entry, a leaked record, a broken audit chain, a hallucinated lab value. Coverage percentage is a proxy; these are the real targets.
2. **Test against real infrastructure.** Integration tests run against real Postgres and Redis (testcontainers). Mocking the database in an event-sourced system hides exactly the bugs that matter.
3. **Clinical logic gets clinical verification.** Unit tests prove the code does what the developer intended. Only Dr. Nahid can confirm the intention was clinically right. Both are required.

## 18.2 The test pyramid, DTHCMS-specific

| Layer | Scope | Tooling | Runs |
|---|---|---|---|
| Unit — pure domain | Calculations, rules, validation, state machines | Go `testing`, Vitest | Every commit, seconds |
| Unit — clinical reference | Formulas against **published reference values** | Shared fixtures | Every commit |
| Parity | Go ↔ TypeScript identical results | Shared fixture file | Every commit |
| Integration | Services + real DB/Redis | testcontainers-go | Every commit |
| Event/replay | Projection rebuild equivalence | Custom harness | Every commit |
| API contract | Spec ↔ implementation conformance | OpenAPI conformance | Every commit |
| Component | UI components, all states, both languages | Testing Library, Storybook | Every commit |
| E2E web | Critical journeys | Playwright | Every PR |
| E2E mobile | Station flows, **offline scenarios** | Maestro | Nightly + pre-release |
| Load | Clinic-day simulation | k6 | Weekly + pre-release |
| Security | SAST, DAST, dependency, secrets | Automated + external pentest | Commit / phase gate |
| AI evaluation | Frozen case set, grounding, regression | Custom harness | Every prompt/model change |
| OCR evaluation | Accuracy on the labelled corpus | Custom harness | Every engine/version change |
| Clinical acceptance | Dr. Nahid's verification | Manual, scripted | Every clinical checkpoint |

## 18.3 Backend testing

- **Unit:** every calculation against published reference values with citations; every rule with positive, negative and boundary cases; every state machine including illegal transitions.
- **Integration:** repository behaviour against real Postgres; transaction boundaries; **concurrency tests on event append** (gapless sequences under 100 concurrent writers); RBAC enforcement at service level.
- **API:** every endpoint's happy path, validation failures, authorisation denials, idempotent retries; golden JSON tests for **field-level redaction** (pharmacist, registration).
- **Database:** migration up/down; migration against a production-shaped snapshot; **append-only enforcement** (an UPDATE as the app role must fail); grant verification; index usage on hot queries via `EXPLAIN` assertions.
- **Event replay:** the flagship test — rebuild all projections from the ledger and assert byte-identical output against the incrementally-built state. Runs in CI on every change, forever.
- **Hash chain:** verification passes on valid data; **detects a deliberate tamper**.

## 18.4 Frontend testing

Component tests for every state (default, loading, empty, error, disabled) × theme × language. Automated accessibility (axe) on every Storybook story. Visual regression on design-system components and on print layouts. Integration tests for feature flows with a mocked API. Playwright E2E for: login + 2FA, registration, a full station entry, physician dashboard review, prescription creation through signing and printing, QA bounce, and RBAC denial paths. Performance budgets asserted in CI (bundle size, Lighthouse).

## 18.5 Mobile testing

Component and hook tests. **Device tests on the actual clinic hardware**, not only emulators. Maestro flows for each station. **The offline matrix from §13.10 is the highest-priority mobile suite**: airplane mode mid-entry, app kill with a full queue, 200-event sync, partial batch rejection, wrong device clock, token expiry offline, device revoked offline, duplicate batch, 10% packet loss, storage full. A nightly soak simulating a full clinic day with intermittent connectivity, with an automated local-versus-server integrity check. Battery and memory profiling on the low-end target device.

## 18.6 AI testing

| Test | Method | Gate |
|---|---|---|
| Grounding | Inject fabricated values, dates, drug names; assert 100% detection | Blocks release |
| Schema conformance | Malformed outputs rejected and retried, then failed cleanly | Blocks release |
| Factuality | Frozen evaluation set with physician-reviewed references | Regression blocks release |
| Regression on change | Full evaluation set on every prompt/model change | Blocks merge |
| Safety | Adversarial prompts attempting to make the model prescribe or diagnose authoritatively | Blocks release |
| PHI leakage | Adversarial inputs with identifiers embedded in free text | Blocks release |
| Cost | Per-encounter cost tracked against budget | Alert |
| Latency | SLA attainment under load | Blocks release |
| Failure behaviour | Provider outage simulation → degraded mode verified | Blocks release |

**No clinical accuracy threshold is proposed here.** Thresholds are set with Dr. Nahid after CP72 measures a baseline.

## 18.7 OCR testing

Character and word error rate per script (Bangla / English / mixed), measured separately. Field-extraction accuracy per analyte class, with **document date accuracy tracked as the highest-priority metric** (it drives the entire chronology). Confidence calibration reported as a reliability curve. Human review rate as the operational cost measure. Regression against the CP98 corpus on every engine or version change. End-to-end pipeline tests from upload through to a timeline entry.

## 18.8 Clinical testing

- **Golden scenario suite for medication safety**, authored with Dr. Nahid: each scenario states inputs and the required finding and severity. Zero false negatives on this suite is a release gate.
- **Formula verification** against published worked examples, with Dr. Nahid personally verifying eGFR, BMI classification and growth percentiles.
- **Prescription end-to-end**: draft → safety check → QA → sign → print → verify, with tamper detection tested.
- **Fail-closed verification**: every gate (allergy, counseling, QA, missing HbA1c) tested for bypass attempts from every client.

## 18.9 Security testing

Automated on every commit: secret scanning, dependency vulnerability scanning, SAST, container scanning. Per phase gate: DAST against staging, authorisation matrix verification, **field-redaction golden tests**, session and token security tests, rate-limit verification, upload security tests. External penetration tests at CP94 and CP156, with a regression test written for every finding. Audit integrity verified continuously by the hash-chain job.

## 18.10 Performance testing

Per-endpoint latency budgets asserted in CI on a fixed dataset. Clinic-day load simulation weekly. WebSocket connection scale tests. Database query profiling with regression alerts. Mobile startup and interaction performance on the low-end target device. OCR and AI pipeline throughput and cost at volume. **A recorded performance baseline** so regressions are detectable rather than merely felt.

## 18.11 What is deliberately not automated

Clinical appropriateness of AI summaries · usability of station flows under real clinic pressure · print quality on paper · comprehension of Bangla patient materials · whether the physician dashboard actually reduces cognitive load. **These require human judgement and are scheduled as manual verification steps in the checkpoints that need them.** Pretending they can be automated is how clinical software becomes technically correct and practically unusable.

---

# 19. ACCEPTANCE STRATEGY

## 19.1 System-level acceptance criteria

These hold across the whole system and are verified at every phase gate.

**Attribution & audit**
1. Every clinical write produces exactly one event carrying user, device, role, station and timestamp.
2. No clinical value exists in any read model without a corresponding ledger event (verified by a reconciliation job).
3. Original values remain immutable and retrievable after any correction.
4. Every correction records who, when, why, and what it corrects.
5. Any reviewer sees the author of any value within one interaction.
6. The event hash chain verifies; tampering is detected.
7. Projections rebuilt from the ledger reproduce current state exactly.

**Access control**
8. A user cannot access data outside their role's permissions — verified by an exhaustive decision-matrix test.
9. A pharmacist's prescription payload contains no diagnosis field (verified on the raw response, not the UI).
10. A nutritionist cannot read prescriptions by any endpoint.
11. Every administrative and clinical access is audited.
12. Revoked sessions and devices lose access within one request.

**Offline & sync**
13. Station entries survive application restart, device restart, and network loss.
14. Queued events synchronise automatically after connectivity returns, with no data loss.
15. Retries never create duplicate events.
16. Offline events retain their true clinical time and are marked as late-synced.
17. The sync indicator never shows "synced" while data is queued.
18. Rejected events are surfaced to the operator with an actionable reason.

**Realtime**
19. A value entered at one station appears on the physician's dashboard in under one second without refresh.
20. Realtime delivery never bypasses RBAC.
21. A dropped connection never causes data loss.

**Clinical safety**
22. Medication safety rules fail closed: missing required data yields "cannot verify", never silence.
23. Drugs outside the curated rule set are reported as unchecked, never as safe.
24. No prescription can be printed without QA clearance.
25. No prescription can be signed without step-up authentication.
26. A signed prescription cannot be modified; tampering breaks signature verification.
27. Critical values trigger visual and audible alerts and reach the consultant within five seconds.
28. A visit cannot pass the history station without explicit allergy status.
29. A visit cannot queue to the physician with unticked mandatory counseling items.
30. A diabetic file cannot close without HbA1c recorded or ordered.

**AI safety**
31. Every AI-generated element is visibly labelled as AI-generated.
32. No AI output enters the clinical record without an explicit human action.
33. Every numeric claim in AI output is grounded in stored data before display.
34. No direct identifier is transmitted to any AI provider.
35. AI unavailability degrades gracefully; the clinic continues to operate.
36. Every AI interaction is recorded with prompt version, model version, tokens and cost.

**Data & privacy**
37. Consent revocation blocks the relevant behaviour within one minute.
38. The research schema contains no direct identifiers and no non-consented subjects.
39. The research role cannot query clinical schemas.
40. Documents and photos are never publicly accessible.

## 19.2 Per-phase acceptance (per §15.3)

Every phase closes only when: **all features are live in production · the audit trail is verified end to end by Dr. Nahid on a real journey · no critical defects remain open · every checkpoint meets the Definition of Done · Dr. Nahid signs off in writing.**

## 19.3 Checkpoint acceptance

Every checkpoint has its own objective criteria (§16). A checkpoint is accepted only after Dr. Nahid's manual verification using the stated procedure. **No checkpoint proceeds to the next without explicit approval** — the workflow rule from the brief, applied literally.

## 19.4 Values requiring stakeholder approval

The following are **proposed engineering values, not clinical facts.** Each requires Dr. Nahid's (or a qualified party's) approval before it gates anything:

| Value | Proposed | Requires |
|---|---|---|
| AI synthesis SLA | ≤5 min per §7.1, tightened after measurement | Clinical |
| AI summary quality bar | To be defined after CP72 baseline | Clinical |
| AI regression threshold | To be defined after CP72 baseline | Clinical |
| OCR accuracy targets | To be defined after CP98 measurement | Clinical |
| OCR confidence thresholds | To be defined after CP98 calibration | Clinical |
| Plausibility bands per measurement | Draft from literature | Clinical |
| Critical value thresholds beyond the two named | Draft from guidelines | Clinical |
| eGFR staleness window | 6 months | Clinical |
| Childhood obesity threshold | ≥95th percentile per [R-06] — standard per D-21 | Clinical |
| Duplicate matching thresholds | After measurement on real data | Operational |
| Retraining flag thresholds | Draft | Operational |
| Station time targets | Draft | Operational |
| Test coverage floor | 70% overall / 90% clinical packages | Engineering |
| RPO / RTO | RPO ≤5 min / RTO ≤4h | Business |

**None of these numbers is asserted as a clinical standard.** They are starting points for a decision that belongs to the clinician.

---

# 20. PHASE-WISE TIMELINE

## 20.1 Estimating assumptions (stated explicitly, because the numbers are meaningless without them)

1. Effort is in **developer-days for one experienced full-stack engineer** who is already comfortable with Go, React, React Native and Postgres. A developer learning any of these should add 30–50% to the affected checkpoints.
2. Estimates cover **implementation + tests + documentation** for that checkpoint. They do **not** include: review cycles with Dr. Nahid, rework after review, clinical content authoring, meetings, recruitment, procurement lead times, vendor negotiation, or leave.
3. A realistic **overhead multiplier of 1.35×** is applied to raw effort to reach *effective* effort (review, rework, coordination, context switching, interruptions).
4. A working year is taken as **220 productive days** per person.
5. Parallel work across people is discounted by a **0.75 efficiency factor** (coordination cost, dependency waiting, integration).
6. **The 🔴 open decisions in §3 are resolved on time.** Every week a blocking decision is late is a week of slip on the dependent checkpoint, and no staffing level fixes that.
7. Physician availability for review, content authoring and clinical verification is assumed at roughly **half a day per week during Phase 0–1**, more during content-heavy periods. This is the assumption most likely to be optimistic.

## 20.2 Raw and effective effort by phase

| Phase | Checkpoints | Raw days | Effective days (×1.35) |
|---|---|---|---|
| Phase 0 — Foundation | CP01–CP14 (14) | 56 | 76 |
| Phase 1 — Clinic Core | CP15–CP95 (81) | 493 | 666 |
| Phase 2 — Memory & Relationship | CP96–CP120 (25) | 174 | 235 |
| Phase 3 — Intelligence & Research | CP121–CP143 (23) | 191 | 258 |
| Phase 4 — Enterprise & Community | CP144–CP160 (17) | 144 | 194 |
| **Total** | **160** | **1,058** | **1,428** |

## 20.3 Calendar time under three staffing scenarios

**Scenario A — one developer (the blueprint's literal handover to Amlan)**

| Phase | Calendar |
|---|---|
| Phase 0 | ~4 months |
| Phase 1 | ~3 years |
| Phase 2 | ~13 months |
| Phase 3 | ~14 months |
| Phase 4 | ~11 months |
| **Total** | **~6 years 6 months** |

*(1,428 effective days ÷ ~18.3 productive days per month.)*

*Assessment: not viable as a business plan.* Beyond the duration, a single developer holding a clinical system of this scope is an unacceptable key-person risk — illness, departure or burnout would halt the clinic's operations, not just its development. **If Scenario A is the reality, the scope must be reduced rather than the schedule stretched** (see §20.5).

**Scenario B — small team: 1 backend, 1 web, 1 mobile, plus a part-time lead/DevOps (≈3.5 FTE)**

| Phase | Calendar | Notes |
|---|---|---|
| Phase 0 | ~1.5 months | Highly parallel |
| Phase 1 | ~14 months | The critical path (event store → stations → prescription) limits parallelism |
| Phase 2 | ~5 months | Needs an OCR/ML capability |
| Phase 3 | ~5.5 months | Needs data/analytics capability |
| Phase 4 | ~4 months | |
| **Total** | **~2 years 6 months** |

*(≈48 effective developer-days per calendar month at 3.5 FTE × 0.75 efficiency.)*

*Assessment: realistic and recommended as the baseline plan.*

**Scenario C — full team: 2 backend, 1.5 web, 1.5 mobile, 1 ML/data, 1 lead/DevOps, 0.5 QA (≈7.5 FTE)**

| Phase | Calendar |
|---|---|
| Phase 0 | ~1 month |
| Phase 1 | ~8 months |
| Phase 2 | ~3 months |
| Phase 3 | ~3 months |
| Phase 4 | ~2.5 months |
| **Total** | **~1 year 6 months** |

*(Raw capacity arithmetic at 7.5 FTE would suggest faster still; these figures are deliberately discounted because the critical path and Dr. Nahid's review capacity — not developer hours — become the binding constraint at this team size.)*

*Assessment: fastest sensible option. Below this, adding people stops helping — the critical path and Dr. Nahid's review capacity become the binding constraints, not developer hours.*

## 20.4 What Dr. Nahid gets, and when (Scenario B)

| Month | Capability live |
|---|---|
| 1.5 | Foundation complete; nothing clinical yet |
| 4 | Login, RBAC, devices, event store, patient registration with duplicate prevention |
| 6 | Visit workflow, queue, traffic board, anthropometry and vitals on phones with instant derived values |
| 8 | All 12 stations capturing, attribution everywhere, correction workflow, counseling gate |
| 10 | **Offline operation** — the clinic keeps working through a Wi-Fi failure |
| 12 | AI synthesis, physician dashboard, longitudinal timeline |
| 14 | Prescription engine with safety, signature, graphs, warnings, back page, print |
| **15.5** | **PHASE 1 LIVE — the clinic runs on DTHCMS** |
| 18 | Records digitisation with OCR, chronology, red-lines |
| 20.5 | Follow-up CRM, SMS, two-way line, pharmacy, inventory — **PHASE 2** |
| 23 | Research dashboards, hypothesis engine, more AI agents |
| 26 | HR, throughput, micro-costing, executive dashboard — **PHASE 3** |
| 30 | Community field app, Maps, FHIR, multi-branch, production hardening — **PHASE 4** |

## 20.5 If the timeline must be shorter (a scope-reduction menu)

If Phase 1 must land faster, these are the honest options, in order of least clinical harm:

1. **Defer the community/field app entirely (Phase 4)** — already deferred; keep it that way.
2. **Defer FHIR until a named partner exists** (CP150–CP152, ~28 days). It has no consumer today.
3. **Defer multi-branch** (CP153, 12 days) until a second branch is real.
4. **Ship Phase 1 with a reduced station set** — registration, anthropometry, vitals, history, counseling, physician, prescription, QA — and add nutrition, exercise and education in a Phase 1.5. Saves ~15 days and, more importantly, reduces the training and adoption load on the floor.
5. **Defer the Traffic Control board** (CP40, 7 days) to Phase 1.5 — valuable, but the clinic already runs without it today.
6. **Defer the paediatric percentile engine** (CP47/CP48, 12 days) if paediatric volume is low — **but only Dr. Nahid can judge that**, and [R-06] suggests he will not want to.

**What must never be cut:** the event store and attribution (CP23/CP24), RBAC enforcement (CP20), offline capability (CP64–CP68), the deterministic safety engine (CP78), fail-closed gates (CP54/CP57/CP83), and the security review (CP94). Cutting any of these does not shorten the project — it moves the cost to a worse time, usually after real patient data is involved.

---

# 21. FULL PROJECT ESTIMATE

## 21.1 Effort summary

| | Raw days | Effective days | Person-months (22 d/mo) |
|---|---|---|---|
| Phase 0 | 56 | 76 | 3.5 |
| Phase 1 | 493 | 666 | 30 |
| Phase 2 | 174 | 235 | 11 |
| Phase 3 | 191 | 258 | 12 |
| Phase 4 | 144 | 194 | 9 |
| **Total** | **1,058** | **1,428** | **65 person-months** |

## 21.2 Complexity distribution

| Complexity | Checkpoints | Examples |
|---|---|---|
| **Very high** — architectural risk, needs the most experienced engineer | 8 | CP23 event store · CP25 projections · CP66 sync engine · CP70 AI gateway · CP78 safety engine · CP98 OCR bake-off · CP121 anonymisation · CP153 multi-branch |
| **High** — significant design work | 27 | CP26 realtime · CP62 corrections · CP71 synthesis · CP73 dashboard · CP74 timeline · CP89 print · CP104 extraction · CP130 hypothesis engine · CP143 executive · CP150 FHIR … |
| **Medium** — well-understood work, careful execution | ~85 | Most station capture, most dashboards, most admin UIs |
| **Low** — mechanical | ~40 | Component work, simple CRUD, configuration |

## 21.3 Costs beyond developer time (all require confirmation)

| Item | Nature | Notes |
|---|---|---|
| Cloud infrastructure | Recurring | Modest for one clinic in Phase 1; grows with documents, AI and analytics. **Measure from CP03's budget alerts rather than estimating blind.** |
| LLM API usage | Recurring, per encounter | **Gemini (D-07).** Free tier covers development and CI at no cost; production requires the paid tier or Vertex AI. Flash-class for high-volume work, Pro-class only for synthesis. Metered and budget-alerted from CP70; per-encounter cost measured at CP71 rather than assumed. |
| OCR processing | Recurring, per page | Depends on D-16; measured at CP98. |
| SMS | Recurring, per message | **Bangla is Unicode: 70 characters per segment, not 160** — material to the cost model. Measured at CP114. |
| External penetration tests | One-off × 2 | CP94, CP156. Vendor quotes needed. |
| Google Maps Platform | Recurring | Phase 4. |
| Biometric hardware | Capital | D-57. |
| Printers (clinic + portable) | Capital | D-58. |
| Clinic devices (phones, tablets, wall display) | Capital | Model confirmation needed for CP11's acceptance test. |
| Legal counsel (D-01, D-02, D-04) | One-off + retainer | **Not optional.** |
| Drug database licence | Recurring, if D-22 Option B | Materially changes the cost model. |
| Terminology licences (SNOMED, instruments) | Recurring, if used | D-24, D-26. |

## 21.4 The three biggest estimation risks

1. **OCR quality (Phase 2).** If the CP98 bake-off shows that no candidate reaches usable accuracy on real Bangladeshi mixed-script documents, Phase 2's records pipeline could grow substantially, or its scope must change to structured manual entry with attached images. **The bake-off is scheduled early in Phase 2 precisely so this is discovered before the dependent work is committed.**
2. **Clinical content authoring (all phases).** Twelve content dependencies (§17.4) are gated on physician time, not developer time. If content is late, checkpoints stall regardless of team size. This is the single most under-estimated risk in projects of this shape.
3. **Prescription UX iteration (CP81, CP89).** The prescription must be faster than paper and look better than anything the clinic has seen. That is achieved by iteration, and iteration is not free. Budget two to three redesign rounds explicitly rather than treating them as overruns.

## 21.5 Confidence

| Phase | Confidence | Why |
|---|---|---|
| Phase 0 | **High (±15%)** | Well-understood foundational work |
| Phase 1 | **Medium-high (±25%)** | Scope is clear; UX iteration and content dependencies are the variance |
| Phase 2 | **Low-medium (±50%)** | OCR outcome is genuinely unknown until CP98 |
| Phase 3 | **Medium (±35%)** | Research requirements will evolve as Dr. Nahid uses the data |
| Phase 4 | **Medium (±30%)** | Scope depends on real partners and real branch plans |

**These are estimates, not commitments.** The checkpoint structure exists so that estimates are re-calibrated continuously with real velocity data, rather than defended to the end of a fixed plan.

## 21.6 Administrative action items (from §17 of the blueprint — not engineering checkpoints)

| ID | Item | Owner | Notes |
|---|---|---|---|
| A-01 | **Dun & Bradstreet account** [R-17] — open the online account, link it to DTHC's existing DUNS number, verify the business profile matches DTHC's registered details, hand credential custody to Dr. Nahid | Amlan | Independent of the build; can proceed immediately. Not a software task, and should not consume engineering time on the critical path. |
| A-02 | Establish the DTHCMS repository (recommended: within the existing Arrow Health GitHub organisation), commit the blueprint as `docs/blueprint-v2.0.md` with all future revisions version-controlled | Amlan | **Folded into CP01.** |
| A-03 | Handover walkthrough with Dr. Nahid; log every question against a blueprint section number | Amlan | Recommended before CP01 approval; §16's open decisions are the natural agenda. |
| A-04 | Record the blueprint's SHA-256 fingerprint on ratification (Appendix B) | Amlan | **Folded into CP01.** |

---

# 22. DEFINITION OF DONE

A checkpoint is **not** done because the code works on the developer's machine. It is done when **all** of the following are true.

## 22.1 Universal criteria (every checkpoint)

**Implementation**
1. All items in SCOPE are implemented.
2. Nothing in OUT OF SCOPE has been implemented (scope creep is a defect).
3. Code follows the CP02 standards and passes all linters.
4. No architectural boundary violations (CI-enforced).
5. No `TODO` or commented-out code in the merged result; deferred work is a tracked issue, not a comment.

**Testing**
6. Unit tests cover the checkpoint's logic, including boundaries and failure paths.
7. Integration tests cover the checkpoint's data and service interactions against real infrastructure.
8. All tests pass in CI — **not "pass locally", not "pass except one flaky test"**.
9. Coverage meets the agreed floor for the affected packages.
10. Regression tests exist for every bug found during the checkpoint.

**Verification**
11. The MANUAL VERIFICATION procedure has been performed and the result recorded.
12. Every ACCEPTANCE CRITERION is objectively satisfied and demonstrated.
13. For clinical checkpoints: **Dr. Nahid has personally verified the clinical behaviour.**

**Quality**
14. No known blocking or critical bugs remain.
15. Known non-blocking issues are recorded as tracked items with severity.
16. Performance is within the stated budget for the affected paths.

**Security**
17. Security implications are assessed and documented.
18. New endpoints declare and enforce permissions.
19. New data is classified (§9.6) and handled per its class.
20. No secrets in code, config or logs; no PHI in logs, traces or error reports.
21. Automated security scans pass.

**Data**
22. Migrations are included, reversible where feasible, and tested against a production-shaped snapshot.
23. New tables follow the §9.2 conventions including `facility_id` where applicable.
24. Indexes exist for the queries introduced, justified by `EXPLAIN`.
25. Clinical writes go through the event store — **no exceptions**.

**Contracts**
26. New or changed endpoints are in the OpenAPI spec; the conformance check passes.
27. Generated clients are regenerated and committed.
28. Breaking changes are versioned and communicated.

**Interface**
29. UI work is integrated and reachable — not an orphaned component.
30. All states are implemented: loading, empty, error, success, offline, unauthorised.
31. Both languages render correctly.
32. Accessibility checks pass; keyboard operation works on web; touch targets meet the minimum on mobile.
33. Attribution and dual-unit components are used wherever clinical values appear.

**Observability**
34. Meaningful logs, metrics and traces exist for the new paths.
35. New failure modes are alertable.
36. New background jobs report to the queue dashboard.

**Documentation**
37. Architecture documentation is updated if the architecture changed.
38. An ADR exists for any significant decision.
39. Runbook entries exist for new operational procedures.
40. User-facing changes are reflected in training material.
41. **The open-decision register is updated** — decisions made are recorded; new ambiguities discovered are added.

## 22.2 Additional criteria by checkpoint type

| Type | Extra requirements |
|---|---|
| **Clinical** | Formulas verified against published references · Dr. Nahid's sign-off · fail-closed behaviour tested · clinical content version-recorded |
| **AI** | Grounding validation active · evaluation set run and recorded · PHI minimisation verified · AI labelling present · degraded mode tested · cost measured |
| **Offline** | Full §13.10 matrix passes · integrity check clean · sync status accuracy verified |
| **Security** | Threat model updated · authorisation tests for every new resource · penetration-test scope updated |
| **Print** | **Printed on the actual clinic printer and approved on paper** · Bangla rendering verified · deterministic output |
| **Research** | Anonymisation verified · statistics verified against an independent implementation · small-cell suppression active |

## 22.3 The phase gate (per §15.3)

A phase closes only when: every checkpoint in it is done · the audit trail is verified end to end by Dr. Nahid on a real patient journey · **no critical defects are open** · performance and security reviews have passed · training is delivered · **Dr. Nahid has signed off in writing**.

---

# 23. RISKS AND RECOMMENDATIONS

## 23.1 Risk register (ranked by expected impact)

| # | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| R1 | **Staff reject the system on the clinic floor** — it is slower than paper, or feels like surveillance | Medium | **Critical** | Involve operators in usability testing from Phase 0 · measure task times against the paper baseline and treat "slower than paper" as a defect · frame quality metrics as support, not policing (CP63, CP140) · phased station rollout (CP95) |
| R2 | **Regulatory non-compliance under the Bangladesh PDP Act 2026** (D-01) | Medium | **Critical** | Legal opinion in Phase 0 · data-class abstraction from CP03 so relocation is configuration · PHI minimisation before any AI/OCR provider is engaged |
| R3 | **Clinical content authoring does not keep pace** (§17.4) | **High** | High | Start authoring in Phase 0 · engine-before-content sequencing so engineering is never blocked waiting · authoring UIs designed for a physician, not a developer (CP77's acceptance test is literally "Dr. Nahid can do it unaided") |
| R4 | **Key-person risk** — one developer holds the system (D-52) | **High** in Scenario A | **Critical** | Documentation from CP01 · ADRs · runbooks · at minimum a second engineer onboarded during Phase 1 · CP159's handover test performed by someone who did not build it |
| R5 | **Silent data corruption via offline sync** (CP66) | Medium | **Critical** | The §13.10 matrix · automated integrity checking · nightly soak · surfacing rather than auto-resolving conflicts |
| R6 | **OCR quality below usable on real documents** (D-16) | Medium | High | CP98 bake-off before committing · human validation queue as the safety net · a planned fallback to structured manual entry with attached images |
| R7 | **AI hallucination reaching the physician** | Medium | **Critical** | Grounding validation (CP72) · deterministic rules override generative output · mandatory human approval · visible AI labelling |
| R8 | **Medication safety coverage gaps presented as safety** (D-22) | Medium | **Critical** | Explicit coverage reporting — "no automated check available" is displayed, never a reassuring green tick · fail-closed on missing data · golden scenario suite with zero false negatives |
| R9 | **Scope expansion** — the blueprint is broad and enthusiasm is high | **High** | High | The checkpoint gate is the control · every new requirement becomes a new checkpoint with an estimate, not an addition to a current one |
| R10 | **Dr. Nahid's review capacity becomes the bottleneck** | **High** | Medium | Batch reviews · asynchronous review with recorded video walkthroughs · a delegated reviewer for non-clinical checkpoints |
| R11 | **Prescription print quality disappoints** (Bangla typography, colour) | Medium | High | Prototype Bangla PDF rendering during Phase 0, not at CP89 · proof on the actual printer early |
| R12 | **SMS delivery unreliable or Bangla renders badly** (D-39) | Medium | Medium | Two-vendor paid trial with measured delivery rates before contracting · provider-agnostic interface |
| R13 | **Duplicate patient records accumulate** | Medium | High | CP30's blocking checks · measured precision/recall · never auto-merge · tune during pilot |
| R14 | **Cost overrun on AI/OCR at volume** | Medium | Medium | Metering and budget alerts from CP70 · caching · measure per-encounter cost at CP60 rather than assuming |
| R15 | **Event store performance degrades with years of data** | Low | High | Partitioning from CP23 · snapshot mechanism built and idle · load testing at CP93 and CP157 |
| R16 | **Third-party dependency changes under us** (model deprecation, OCR API change, SMS vendor exit) | Medium | Medium | Adapter interfaces everywhere external · pinned versions · at least one alternative identified per external dependency |
| R17 | **Biometric attendance creates legal exposure** (D-57, D-01) | Medium | High | Legal review before implementation · templates not images · consider the phone/QR alternative that avoids new biometric collection entirely |
| R18 | **Research data leakage or re-identification** | Low | **Critical** | Grant-level schema separation · anonymisation ETL · audited break-glass · k-anonymity checks |
| R19 | **Patient data reaches the Gemini free tier** — where Google's terms permit training and human review | Medium if unguarded | **Critical** | D-07's tier decision · the CP70 tier guard that fails closed · separate credentials for synthetic and production paths · a test that proves a real-patient payload cannot use a free-tier key |
| R20 | **Free-tier quotas or model deprecation break the §7.1 synthesis SLA** | Medium | High | Paid tier for production (no meaningful daily cap) · pinned model versions · the evaluation set (CP72) as the migration safety net · degraded mode (D-15) so the clinic never stops |

## 23.2 The recommendations that matter most

**1. Resolve D-51 before anything else.** Whether a prototype and real patient data exist changes Phase 0 materially. This is a one-email question with a large planning consequence.

**2. Get legal counsel engaged now (D-01, D-02, D-04).** The Bangladesh PDP Act 2026 landed after the blueprint was written and touches hosting, AI, OCR, NID handling, biometrics and research. Engineering can proceed on the data-class abstraction while counsel works, but the hosting decision cannot be finalised without an answer, and retrofitting residency is expensive.

**3. Do not build this with one developer.** Scenario A is a six-year plan with unacceptable key-person risk on a system the clinic will depend on daily. Scenario B (≈3.5 FTE) is the honest minimum for the full scope. If only one developer is available, cut scope (§20.5) rather than stretching the schedule.

**4. Start clinical content authoring in Phase 0.** Twelve content dependencies gate checkpoints across all four phases. None requires software to exist. Beginning now removes the most common cause of slip in this kind of project.

**5. Keep the AI boundary exactly where the blueprint puts it.** Generative AI drafts; deterministic rules decide; the physician signs. Every future feature request that blurs this line should be refused on the blueprint's own authority (§7.3: "a permanent constraint, not a phase-1 limitation").

**6. Treat "slower than paper" as a defect, not a trade-off.** The blueprint's whole premise is cognitive-load reduction. If registration, prescribing or counseling takes longer than the current process, the feature is not finished — regardless of how many requirements it satisfies.

**7. Run the OCR bake-off (CP98) early in Phase 2 — or earlier.** It is the largest single unknown in the plan, and there is no reason it cannot start during Phase 1 in parallel, as soon as a document corpus can be assembled. Bringing it forward converts a Phase 2 risk into a Phase 1 decision.

**8. Instrument the audit trail's usefulness, not just its existence.** §4.3's purpose is that mistakes stop recurring. Track whether correction rates actually fall after retraining. If they do not, the mechanism is bookkeeping and needs redesign — and that is worth knowing early.

**9. Protect Dr. Nahid's review time deliberately.** He is the clinical authority, the content author, the primary user and the approver. His time is the scarcest resource in this project and should be scheduled like the critical resource it is — batched reviews, asynchronous walkthroughs, and a delegated reviewer for non-clinical checkpoints.

**10. Revisit this plan at every phase gate.** A 160-checkpoint plan written before the first line of code will be wrong in its details. The checkpoint structure exists so that being wrong is cheap and correctable, not so that the plan can be defended.

## 23.3 What is genuinely excellent about this blueprint (worth saying)

Most specifications handed to a developer are vaguer than this one. Several decisions in it are unusually good and should be protected as the project evolves: the **event log as source of truth** (which makes attribution, correction, audit and research integrity all fall out of one mechanism); the **deterministic-prescribing boundary**; the **exclusion of handwritten prescriptions from OCR** (which removes the single hardest problem by clinical judgement rather than by technical struggle); the **fail-closed principle**; the **alert-fatigue control** on the guideline engine; and the **explicit statement of purpose behind the correction workflow** — that the point is preventing recurrence, not assigning blame. These are the marks of someone who has thought about how the system fails, not only about what it does.

---

# 24. RECOMMENDED CP01

## 24.1 Why CP01 is repository and CI, not a feature

It is tempting to start with something visible. It would be a mistake. Every one of the other 159 checkpoints commits into this repository, runs through this CI, and follows the conventions established alongside it. Three days spent here saves weeks of churn later — and it also delivers two of the blueprint's own §17 administrative actions (repository establishment, blueprint custody).

CP01 is also **the smallest possible checkpoint that proves the working agreement**: I implement, you review, you approve, we proceed. Testing that loop on something low-risk is worth doing before testing it on the event store.

## 24.2 CP01 specification (repeated here for convenience)

> **ID:** CP01 · **Name:** Repository, Monorepo Scaffolding & CI Skeleton · **Effort:** 3 days
> **Objective:** Establish the DTHCMS repository with its final structure, tooling, and a CI pipeline that runs on every commit.
> **Deliverables:** monorepo per §4.5 · workspace tooling · formatters and linters · commit conventions · PR template with the Definition-of-Done checklist · branch protection · GitHub Actions running format, lint, build and test · dependency updates · secret scanning · `docs/blueprint-v2.0.md` with its SHA-256 recorded · this plan committed as `docs/implementation-plan.md`.
> **Acceptance criteria:** (1) clean-machine clone and setup in under 15 minutes · (2) CI fails on lint, build or test failure · (3) a pushed secret is blocked · (4) `main` cannot be pushed to directly · (5) the blueprint's SHA-256 is recorded.
> **Manual verification:** clone on a clean machine and run the bootstrap command · open a PR with a deliberate lint error and confirm CI fails · commit a fake secret and confirm the scanner blocks it.

## 24.3 What I need from you before CP01 starts

**Required — reduced to two, after the answers of 22 August 2026:**

D-51 (no prototype, no patient data) and D-52 (Dr. Nahid + Claude) are answered. D-01 is no longer blocking, because hosting is deferred and Phase 0 runs entirely locally. What remains:

1. **Approval of the architectural positions in §1.4** — modular monolith rather than microservices; event sourcing for clinical data with conventional CRUD elsewhere; offline-first from Phase 1. These deviate from or extend the blueprint and should not be adopted silently.
2. **Where the code lives** — a private GitHub repository (blueprint §17.2 recommends the existing Arrow Health organisation), or, if you prefer to defer that, local-only in `D:\Project\DTHCMS` with the repository added later. A remote repository is strongly recommended from day one: it is the backup, the history, and the continuity between sessions.

**Noted, not blocking:** the D-07 tier decision matters at CP95, not now — every environment before the pilot is synthetic, so the Gemini free tier is appropriate for the entire build period.

**Useful but not blocking:**

5. GitHub organisation (blueprint §17.2 recommends the existing Arrow Health org) and repository visibility (**private**).
6. The clinic's device models — the phone the operators will use, the tablet, the printer — so CP11 and CP89 have real acceptance targets.
7. **D-59** — patients per day now and at target, concurrent operators, opening hours. These are the inputs to the queue model and the load tests.
8. Confirmation that you want to **begin clinical content authoring in parallel** (§17.4), and which item you would like to start with. My recommendation: the **diabetes counseling template** (D-53) and the **critical value table** (D-27), because they gate early Phase 1 checkpoints and are self-contained.

## 24.4 The working agreement, restated

```
   PLAN  (this document)
     ↓  you review and approve
   CP01  implement → test → I demonstrate → you review → fix if needed → you approve
     ↓
   CP02  … and so on, one checkpoint at a time
```

I will not begin CP01 until you approve this plan. I will not begin any checkpoint until the previous one is explicitly approved. I will not implement future checkpoints early. **If I discover a new ambiguity during implementation, I will stop and raise it as an open decision rather than deciding it silently** — and the open-decision register in §3 will be updated as a living document rather than left as a snapshot.

---

## APPENDIX — DOCUMENT CONTROL

| | |
|---|---|
| **Document** | DTHCMS Master Implementation Plan v1.0 |
| **Derived from** | DTHCMS Complete Blueprint v2.0 (21 August 2026) |
| **Prepared** | 22 August 2026 |
| **Status** | Draft for Dr. Nahid's review — **no implementation has begun** |
| **Checkpoints defined** | 160 (CP01–CP160) |
| **Open decisions** | 71 (D-01 – D-71); 24 marked 🔴 blocking |
| **Blueprint coverage** | §1–§17 and Appendix A (R-01 – R-17) — traceability at §2.16 |
| **Next action** | Your review; four answers required before CP01 (§24.3) |

**Sources consulted for the open-decision register** (external facts as of August 2026, to be verified independently where they bear on legal or contractual decisions):

- Bangladesh Personal Data Protection Act, 2026 — [Securiti overview](https://securiti.ai/bangladesh-personal-data-protection-act-overview/), [Digital Policy Alert record](https://digitalpolicyalert.org/change/18757-personal-data-protection-amendment-ordinance-2026-ordinance-no-23-of-2026)
- SNOMED CT licensing — [SNOMED International: Get SNOMED](https://www.snomed.org/get-snomed), [Member/Affiliate licensing](https://conf.spaces.snomed.org/wiki/spaces/MLDSDOC/pages/133145120/Member+Affiliate+Licensing+Information)
- Drug data sources — [FDB drug–drug interaction module](https://www.fdbhealth.com/solutions/medknowledge-drug-database/medknowledge-clinical-modules/drug-drug-interaction), [DrugBank](https://go.drugbank.com/), [NLM RxNav interaction APIs (discontinued)](https://lhncbc.nlm.nih.gov/RxNav/APIs/InteractionAPIs.html)
- Bangladesh drug registration and pricing — [DGDA price search](https://www.dgdagov.info/index.php/search-price)
- Bangladesh SMS gateways — [REVE SMS](https://www.revesms.com/), [MiM SMS pricing guide](https://www.mimsms.com/bulk-sms-price-in-bangladesh-2026)
- OCR engine landscape — [Surya OCR](https://tasarim.ai/en/models/surya-ocr), [open-source OCR comparison](https://unstract.com/blog/best-opensource-ocr-tools/)
- Google Cloud regions — [Google Cloud locations](https://cloud.google.com/about/locations/)

*— End of Implementation Plan v1.0 —*
**Why this checkpoint exists:** §15.3 requires a formal phase close; this is the final one, across the whole system. **Out of scope (continued):** New capability — verification and acceptance only. **Technologies:** Existing stack. **Backend:** Deployment only. **Frontend:** Deployment only. **Mobile:** Deployment only. **AI:** Final evaluation run recorded. **Security:** Final verification of the CP156 remediation. **Events/Audit:** Full end-to-end audit trail verification. **Testing:** Full regression, performance and security suites executed and recorded as release evidence.
