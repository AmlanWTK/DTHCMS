# DTHCMS — Digital Diabetes, Thyroid & Hormone Clinic Management System
## Complete Blueprint v2.0 — Developer Handover Edition
|  |  |
| Prepared by | Dr. K. M. Nahid Ul Haque — Endocrinologist; Founder, DTHC (Diabetic & Thyroid Health Care), Faridpur |
| Handed over to | Amlan — Software Developer |
| Date | 21 August 2026 |
| Supersedes | DTHCMS Blueprint v1.0 |
| Incorporates | Dr. Nahid's August 2026 dictation, merged in full (requirements R-01 → R-17, Appendix A) |
| Status | Handover draft — pending final ratification by Dr. Nahid |
Naming note: earlier draft passages used "CDLCMS." The canonical system name is DTHCMS in this and all future documents.

## 0. How to Read This Document (Amlan — start here)
This is the single, complete specification. It merges the original v1.0 blueprint with every new requirement from Dr. Nahid's August 2026 dictation. Nothing outside this document is authoritative.
§1–2 — Vision and the design principles that may never be violated.
§3–13 — Functional specification, module by module.
§14–15 — Technical architecture and phased delivery roadmap.
§16 — Open decisions that need Dr. Nahid's sign-off before or during the build.
§17 — Administrative action items assigned to you, including Dun & Bradstreet.
Appendix A — Delta Log: every new v2.0 requirement with an ID (R-01…R-17). Wherever you see a tag like [R-04] in the body, that clause is new or changed in v2.0 and must be treated as a hard requirement.
Log every question you raise against a section number so review stays traceable.

## 1. Vision & Philosophy
Ordinary clinic software and EMRs are passive digital filing cabinets: they store billing codes, cause "death by a thousand clicks," and treat patient data as a byproduct. DTHCMS is designed as an active Healthcare Operating System built on one uncompromising premise:
Every data point is important. Every patient is important. Every patient is a candidate for research.
The stated ambition is explicit: this system must be more modern than the best system in the world — a new creation, not an imitation. Everything must be fast and flawless, with results available instantly upon input.
Core philosophies:
The clinic as an intensive research engine. Clinical care and research are one activity. Every interaction, biometric, and behavioral response is captured in structured, queryable form. The clinic operates as a high-throughput clinical research organization at the point of care.
Cognitive load reduction. The physician is a diagnostician, not a data-entry clerk. The system synthesizes decades of history into seconds of reading.
The flywheel of continuous learning. Tracked outcomes teach the AI which interventions work for which demographics. Every patient today improves care for the patient tomorrow.
Proactive population health. The system reaches beyond clinic walls to find high-risk individuals before disease progresses.
Endocrine specialization first [R-06]. All AI prompts, rules, and templates are tailored strictly to an endocrinology clinic for now (diabetes, thyroid, obesity, growth, PCOS, reproductive endocrinology). Generalization is a later phase.

## 2. Non-Negotiable Design Principles
These override any implementation convenience. If a feature conflicts with a principle here, the feature changes.
Mobile-first data capture [R-01]. Every station operator enters data from a smartphone, not a laptop. BP, weight, height, food habits — all entered on mobile at the point of measurement.
Universal attribution [R-03]. Every single entry carries the operator's identity, the device used, the station/role, and a timestamp — immutably. Any reviewer must be able to see who entered this value instantly, without digging.
Role-flexible devices [R-02]. When staffing is short, one operator may perform multiple entry types from the same device. Roles attach to the logged-in user, not the hardware.
Instant computation. Derived values (BMI, BMR, growth percentiles, eGFR) compute the moment inputs land. No batch delays, no "refresh."
The patient always arrives ready [R-05, R-07]. By the time a patient enters the consultation room: counseling is complete and ticked, the AI summary is already generated, and external records are digitized. The physician never waits for the system.
Dual-unit humanity [R-08]. Clinical values store and display in clinical units (cm, kg) with the patient-familiar equivalent (feet/inches) shown directly beneath, so staff can inform the patient immediately.
Fail-closed QA. A file cannot close with missing mandatory data (e.g., a diabetic file without an HbA1c recorded or ordered).
AI drafts, the physician decides. Generative AI is confined to summarization and drafting. Prescribing logic lives in deterministic, hard-coded pharmacological rules, and every prescription requires the physician's digital signature.
Bangladesh context by default. Bangla/English bilingual UI and outputs; local drug trade names and BDT pricing; culturally local diet plans; SMS-first fallback communication.

## 3. The Complete Patient Journey — 12 Stations
The clinic operates as a highly parallel assembly line of specialized care. Ten to fifteen operators may touch the same patient inside a 45-minute window.
Step 1 — Registration. Personnel: Registration Officer. Data: demographics, National ID, photo/biometrics, socio-economic baseline (essential for research cohorting), emergency contact, and an accurate date of birth — mandatory and validated, because pediatric percentile calculation depends on exact age [R-06]. AI: OCR of the National ID to eliminate manual error; phone format validation; assignment of a unique immutable Research ID linked to the clinical ID. Checkpoint: strict duplicate-record prevention.
Step 2 — Anthropometry & Screening. Personnel: Anthropometry Officer, entering from their own phone [R-01]. Data: height, weight, waist/hip, body fat %, muscle mass (bioimpedance). AI: instant BMI, BMR, ideal weight, obesity class (WHO/Asian criteria); for pediatric patients, instant height/weight/BMI percentiles from exact age, with childhood obesity flagged at ≥ 95th percentile [R-06]. Display: height in cm with feet-and-inches beneath [R-08]. Checkpoint: rejection of impossible inputs (BMI 8, negative deltas).
Step 3 — Counseling & Lifestyle Assessment. Personnel: Clinical Counselor. Data: smoking pack-years, alcohol, sleep, validated stress indices, motivation, dietary triggers, activity baseline — plus the waiting-time counseling checklist, fully specified in §5 [R-07]. AI: composite Lifestyle Risk Score for behavioral cohorting.
Step 4 — Medical History. Personnel: Medical History Officer. Data: chief complaints (SNOMED-CT structured), comorbidities, family/surgical history, allergies, current drugs, vaccinations. AI: auto-tagging of drug classes; mapping free text to standard terminology. Checkpoint: hard stop on allergies — explicit "No Known Allergies" or a listed entry from a standard drug database.
Step 5 — Clinical Examination & Vitals. Personnel: Junior Doctor / Clinical Assistant, entering from mobile [R-01]. Data: BP, pulse, RR, temp, SpO₂; diabetic foot exam (monofilament, pulses); neuropathy and retinopathy screening; cardiovascular signs. AI: immediate critical-value alerts (SpO₂ < 92%, BP > 180/110) with visual and audible warning; history-driven exam prompts.
Step 6 — Medical Records Import. Personnel: Medical Records Officer. The complete digitization protocol — scan-everything intake, the handwritten-prescription exclusion rule, chronological ordering, and red-line highlighting — is specified in §6 [R-09, R-10].
Step 7 — Nutrition Assessment. Personnel: Clinical Nutritionist. Data: 24-hour recall, caloric intake, meal timing, food habits (may be entered by a second assistant on their own device [R-01, R-02]). AI: personalized diet options built on local Bangladeshi foods, caloric targets, and current renal/hepatic status.
Step 8 — Exercise Assessment. Personnel: Exercise Specialist. Data: mobility, joint issues, baseline fitness. AI: scalable routine avoiding contraindicated movements (no high-impact cardio in severe neuropathy). Output: exercise sheet with follow-up targets.
Step 9 — Physician Consultation (The Nexus). Personnel: Chief Consultant (Dr. K. M. Nahid Ul Haque). By this point the asynchronous AI pre-consultation analysis is already complete — the trigger model in §7 guarantees the physician never initiates or waits for it [R-05]. AI provides: one-page synthesis of Steps 1–8, drafted prescription and missing investigations, red-lined abnormalities from the records timeline. Checkpoint: the physician manually approves or modifies every AI suggestion; the AI cannot prescribe.
Step 10 — Quality Assurance Review. Personnel: QA Officer + automated engine. AI: automated checklist — drug interactions, duplicate medicines, missing counseling ticks, missing HbA1c for a diabetic. Outcome: clearance for printing, or the file bounces back for correction. Fail-closed.
Step 11 — Prescription Education. Personnel: Prescription Education Officer. Data: demonstrated insulin/device technique, prior compliance. Outcome: fewer treatment failures from technique errors.
Step 12 — Long-Term Monitoring & Follow-Up. Personnel: automated system + telemedicine staff. The full follow-up CRM — chief-complaint logging, preferred-contact-time capture, call-then-SMS fallback, and the two-way clinic line — is specified in §11 [R-14]. CGM syncs and patient-reported outcomes feed the longitudinal record; predictive algorithms flag likely no-shows and deterioration for proactive outreach.

## 4. Multi-Operator Concurrency, Attribution & Error Governance
### 4.1 Real-time concurrency
WebSocket + Redis architecture with event sourcing. When the nutritionist updates the diet, the junior doctor's screen updates instantly — no refresh. The event log, not the current-state table, is the source of truth.
### 4.2 Device & identity model [R-01, R-02, R-03]
Operators log into the mobile app on any clinic device; identity follows the login, not the phone.
One operator may hold multiple station roles simultaneously on one device when staffing is short — e.g., the same assistant enters BP, then switches to anthropometry entry, from the same phone [R-02].
Every write event carries the envelope: {value, unit, user_id, device_id, role, station, timestamp}.
Every reviewable value in every UI shows an "Entered by" chip. Dr. Nahid's exact requirement: when I see height entered as 150 where the true value is 140, I must be able to see directly in front of me who made that entry [R-03, R-04].
### 4.3 Error traceability & correction workflow [R-04]
Worked example (the canonical case): true height 140 cm, entered as 150 cm.
Any reviewer (physician, QA, senior operator) flags the value; the author's name, device, and timestamp are displayed immediately.
A correction request routes to the author (supervisor override available). The correction is stored as a new event referencing the original — the original is never deleted (append-only audit).
The error is tallied against the operator's quality record.
Recurring patterns per operator surface on the HR dashboard (§14) so targeted retraining happens and the same mistake does not repeat — the explicit purpose of the whole mechanism.
### 4.4 Role-based access control (RBAC)
Nutritionist: write diet; read labs; no access to prescriptions.
Registration: read/write demographics; blinded to sensitive clinical diagnoses.
Pharmacist: sees authorized drug list and dosing only; diagnoses hidden.
Review hierarchy: data flows upward — assistant → junior doctor → Chief Consultant. Critical findings bypass the queue straight to the Consultant's dashboard.
### 4.5 Immutable audit trail
An append-only log records every change with actor and time (e.g., "10:42 — JD_04 changed systolic BP 140 → 145"). This serves clinical governance, medico-legal defense, and research integrity simultaneously.

## 5. Waiting-Time Counseling Module [R-07]
Principle: waiting time becomes therapeutic time. Patients sitting outside the consultation room are never idle — they move through structured counseling, and the physician sees exactly what has been covered before the patient walks in.
### 5.1 The counseling checklist
Per-diagnosis templates; the diabetes template is the launch minimum and must include at least:
Understanding of the disease (what diabetes is)
Awareness of diabetes complications
Food habits and diet counseling
Exercise counseling
Chronic disease self-care counseling
Glucometer use
Insulin injection sites and technique
Templates are physician-configurable so thyroid, obesity, PCOS, and growth checklists can be added without code changes.
### 5.2 Room-based step flow
Counseling proceeds step-by-step through dedicated physical rooms — Counseling Room → Nutrition Room → Insulin Corner (sequence configurable). A live Clinic Traffic Control board shows every waiting patient's tick status and dynamically reroutes patients if one station backs up.
### 5.3 Mobile tick interface
The counselor ticks each completed item on a phone — deliberately mobile, not laptop, because it is faster on the floor [R-01]. Each tick carries full attribution (§4.2).
### 5.4 Physician verification
The consultation dashboard (§8) displays the completed checklist so Dr. Nahid can spot-question the patient on any covered topic ("What did they teach you about injection sites?"). Unticked mandatory items are impossible to miss.
### 5.5 Fail-closed gate
A patient is not queued to Step 9 until the mandatory items for their diagnosis are ticked (configurable per template).

## 6. External Medical Records Digitization [R-09, R-10]
Patients frequently arrive having seen many doctors — ten is not unusual. The system converts that fragmented paper history into one clean chronological truth.
### 6.1 Intake rules
Scan every single document the patient brings, without exception.
Handwritten prescriptions are excluded from the structured chronology. They are scanned and stored as reference images attached to the record, but OCR extraction is not attempted on them (unreliable, and explicitly excluded by Dr. Nahid). Everything printed — lab reports, imaging reports, discharge summaries, ECG/Echo reports — enters the structured pipeline.
### 6.2 Chronology engine
For every printed document, OCR + NLP extract: date, medical facility, what test/report it is, why it was done (when stated), and the exact values (e.g., "HbA1c 8.2", "LVEF 45%"). Documents are then arranged in absolute chronological order, producing a single investigations timeline across all prior providers.
### 6.3 Auto-summary
From the assembled chronology, the AI writes a concise narrative problem history — "this patient had these problems, evolving in this order" — which feeds the physician's one-page synthesis (§7).
### 6.4 Red-line abnormality highlighting [R-10]
Abnormal findings are rendered with red underlines/flags so the physician's eye lands on them instantly. Seed rules (extensible library, physician-curated):
Renal: raised creatinine and/or urinary pus cells flagged; eGFR auto-derived (CKD-EPI 2021) whenever creatinine, age, and sex are available, so a CKD pattern is visible at a glance.
Cardiovascular: elevated blood pressure values and series.
Gynecologic/endocrine: ultrasonography showing polycystic ovaries — the system extracts ovarian size/volume, cyst/follicle count, and computes interval change across serial scans (improvement or worsening stated explicitly).
All extracted values also populate the longitudinal timeline (§8.4), so cause and effect are visually correlatable.

## 7. Artificial Intelligence Framework
Specialized, sandboxed agents coordinated by the backend — never one hallucination-prone monolith.
### 7.1 Pre-Consultation Synthesis Agent — the trigger model [R-05]
This is the operational heart of "the patient arrives ready":
As stations complete, their structured data lands in the patient's live record.
The final assistant in the flow presses one button — Analyze / Summarize / Comprehensive Report. (The physician can trigger it but by design never needs to: the analysis takes time the physician does not have, so it runs while the patient is still in counseling.)
The job enters an asynchronous queue. Target SLA: complete summary ≤ 5 minutes (tune in testing), always finishing before the patient reaches Step 9.
Output: the one-page clinical overview and draft plan, including pediatric growth analytics — exact age from validated DOB, height/weight/BMI percentiles against the reference standard chosen in §16, and the ≥ 95th-percentile childhood obesity flag [R-06].
### 7.2 The agent roster
AI Medical Scribe — ambient conversation → structured SOAP notes.
AI Clinical Assistant — chart review; suggests missing questions/examinations.
AI Diagnostic Support — Bayesian differential diagnosis.
AI Medication Safety Engine — deterministic rules engine: proposed drugs × renal function × allergies × contraindications × current medications. Not generative.
AI Nutrition & Exercise Assistants — localized lifestyle drafting.
AI Follow-Up Predictor — adherence + socioeconomic markers → optimal follow-up interval and no-show risk.
AI Outcome Monitoring & QA Engines — nightly audits of closed files for standard-of-care deviations.
AI Research Assistant — continuous cohort mining for publication (§12).
AI Global Knowledge & Guideline Engine (Automated CME) — ingests ADA/EASD/WHO/PubMed updates; cross-references the active patient panel when guidance changes. Alert-fatigue control: updates batch into a weekly Clinical Digest; only critical safety recalls push instantly.
### 7.3 Safety architecture
Generative models: summarization and drafting only. Prescribing authority: deterministic databases + mandatory human digital signature. This split is a permanent constraint, not a phase-1 limitation.

## 8. Physician Intelligence Dashboard
Zero raw forms. Three panels plus the timeline.
Left — Patient Snapshot: demographics, current vitals, BMI, sparklines of the last 5 HbA1c values, active diagnoses, critical alerts ("Allergic to Penicillin"), pediatric percentile card where applicable, and the counseling checklist status (§5.4).
Center — Clinical Summary: the NLP agent's flowing one-page narrative: complaints, history, examination, labs — with red-lined abnormalities from §6.4 carried through.
Right — AI Assistant: suggested ICD-coded diagnoses; missing-data alerts ("eye exam overdue 6 months"); drafted investigations and doses reflecting Dr. Nahid's historical prescribing patterns and current guidelines.
Longitudinal timeline: a continuous, scrubbable, stock-chart-style view — diagnoses, medications, investigations, procedures, admissions, lifestyle interventions on one time axis, with clinical values overlaid so cause and effect correlate in seconds. Hovering any value reveals its "Entered by" attribution (§4.2).

## 9. Prescription Engine & Printed Output [R-08, R-11, R-12, R-13]
The printed A4 sheet is the clinic's primary physical interface with the patient — and, deliberately, with every other doctor and pharmacist who will ever read it. Four-color laser printing throughout.
### 9.1 Front page — patient- and pharmacist-facing
Pre-printed clinic header (Dr. K. M. Nahid Ul Haque, qualifications, registration number), patient demographics, ICD-coded diagnoses, structured Rx section, lifestyle advice, QR verification.
Medicine names bold; dosages visually distinct; clean typography readable by visually impaired patients; Bangla instructions wherever patient-facing.
Graphs on the prescription [R-11]: trend sparklines (HbA1c, weight, BP) and full-color gradient bars showing exactly where the patient's BMI/HbA1c sits — abstract numbers made tangible.
Patient-reported score [R-11]: each visit the patient rates improvement 1–10 versus before; the score prints and plots over time.
Dual units [R-08]: cm primary, feet/inches beneath.
Color code: red = stop/danger; green = target achieved.
QR codes: link to localized video instructions (e.g., insulin pen technique).
### 9.2 Drug-specific warning library [R-12]
A structured, physician-curated library auto-attaches the correct warning text (Bangla + English) whenever a matching drug is prescribed. Dr. Nahid authors and approves every warning. Seed entries:
Semaglutide / GLP-1 receptor agonists: nausea, vomiting, loose stools or stomach upset are common with the first dose(s) and on dose escalation; usually transient; hydration advice and when to contact the clinic.
CNS / psychotropic drugs: must never be stopped abruptly; continue as directed even while recovering; dose adjustment is the physician's alone — the patient never self-adjusts.
Hypnotics (sleep medication): PRN — take only on a night sleep does not come; not a fixed daily medicine.
The library is extensible: every commonly prescribed endocrine drug eventually carries its own clear indication and warning block.
### 9.3 Back page — professional-facing rationale [R-13]
The reverse of the prescription carries the doctor's evaluation and reasoning: clinical assessment, the plan and next steps, and the research/guideline rationale behind each choice. Purpose, in Dr. Nahid's words: any other doctor reading it understands the logic completely, and no doctor will have the courage to change it — there is no scope to tamper with this prescription.
It also carries a polite standing notice to non-endocrinologists: regarding diabetes and endocrine management, please do not alter this therapy; contact the clinic instead. (Exact Bangla and English wording to be authored by Dr. Nahid; the template reserves the block.)
### 9.4 Output result
A prescription that is simultaneously patient-friendly, pharmacist-friendly, and doctor-friendly — Dr. Nahid's stated triple requirement — and that doubles as a brand artifact for DTHC's clinical credibility.

## 10. Medicine Index & Pricing [R-16]
### 10.1 Prescribing autocomplete
Typing two letters in the Rx field surfaces matching trade names with generic name, strength, form, and unit price in BDT. Speed here is a hard requirement — this is used on every prescription.
### 10.2 Build approach — decision required (§16.1), with recommendation
Option A (recommended for Phase 1): a manually curated formulary of the clinic's common medicines (~100–200 items: GLP-1 RAs, SGLT2 inhibitors, DPP-4 inhibitors, insulins, metformin, thyroid drugs, statins, antihypertensives, common adjuncts), maintained through an admin UI with a monthly price-review owner. Rationale: total accuracy control, trivial cost, fastest to ship, and it covers the overwhelming majority of real prescriptions.
Option B: full national/commercial drug-database integration — revisit at Phase 3 once Option A proves the workflow.
Dr. Nahid's own framing: integrate a few common medicines manually first, then evaluate. Option A operationalizes exactly that.
### 10.3 Pricing as research metadata
Every dispensed/prescribed item's BDT price flows into the research module (§12) — the Bangladesh pricing context must always be visible in outcome and affordability analyses.

## 11. Follow-Up & Patient Communication System (CRM) [R-14]
Dr. Nahid identifies this as one of the strongest aspects of the entire system. The goal: a continuous three-way bond — patient ↔ doctors ↔ clinic.
### 11.1 Visit memory
Every visit records: date, chief complaint / presenting problem, diagnoses, plan, and the next review interval. "Which patient came when, with what problem" is answerable in one query, forever.
### 11.2 Contact preference capture — at checkout
Before leaving, every patient is told the clinic will call, and the system records:
Preferred call window ("always free" vs "busy — call at HH:MM"),
Consent for calls and SMS,
The number to use.
### 11.3 Outreach engine with SMS fallback
Due follow-ups generate a call task at the patient's preferred time.
Unanswered call → automatic SMS/text fallback, logged.
Every outbound and inbound touch attaches to the patient record.
### 11.4 Two-way clinic line
Patients can call or text the clinic number at any time; inbound messages route to a triage queue and attach to the record. Communication is continuous in both directions — the "wonderful combination" of patient, doctors, and clinic.
### 11.5 Predictive layer
The AI Follow-Up Predictor (§7.2) flags patients at high risk of missing appointments, failing therapy, or deteriorating, and moves them to proactive outreach before the scheduled date.

## 12. Research & Analytics Engine [R-15]
The clinic is fundamentally an intensive research base: a dedicated on-site research team, anonymized dashboards meeting IRB-grade integrity, and a target of 2+ high-quality papers, audits, or outcome studies monthly plus two full-length clinical books annually (via the Automated Clinical Narrative Engine, with strict PII stripping before any editorial review).
### 12.1 Named launch analyses — build these as saved, refreshable dashboards
HbA1c trajectories: how each patient's HbA1c developed; improvement distributions by cohort.
Exercise–outcome correlation: metabolic improvement as a function of recorded exercise adherence.
GLP-1 RA safety: percentage of patients experiencing adverse effects on semaglutide; incidence by starting dose; dose-initiation analysis (what dose should treatment start at, empirically).
GLP-1 RA efficacy: how many patients semaglutide helped lose weight; response distributions; same for tirzepatide.
Affordability & persistence: how patients bear the cost of semaglutide/tirzepatide in the Bangladesh context; discontinuation-for-cost analysis.
Multi-benefit dashboards: semaglutide and tirzepatide effects across diabetes, hypertension, obesity, and dyslipidemia simultaneously; parallel dashboards for SGLT2 inhibitors (dapagliflozin, empagliflozin, canagliflozin) and linagliptin.
Bangladesh pricing (§10.3) is a standing axis on every affordability and outcomes view.
### 12.2 Discovery machinery
Automated Hypothesis Engine: hunts patterns and autonomously drafts research proposals (e.g., demographic anomalies in drug efficacy).
Positive-deviance mining: aggressively tracks unexpected successes (e.g., disease remission) for reverse-engineering into clinic-wide protocols.
Data completeness enforcement: QA (Step 10) guarantees research-grade datasets — a diabetic file cannot close without an HbA1c recorded or ordered.

## 13. Community Outreach & Social Health Wing
A fully integrated preventive extension beyond clinic walls — schools, workplaces, rural communities.
Field operations: mobile teams on React Native tablets run awareness programs and screening (height, weight, BMI, waist, BP, blood grouping, RBS, eye screening, family health assessment).
Instant field reports: each screened person immediately receives a simple, colorful printed/digital report (weight-for-age, BMI indicator, BP interpretation) built for maximum comprehension.
In-field diagnostics: HbA1c, Vitamin D/B12, OGTT sample collection; dedicated monitoring of high-risk cohorts (elderly, pregnant women, high-risk genetics).
Deep interoperability: every field record syncs instantly to the central database — creating screening records, updating longitudinal timelines, flagging abnormals for triage, and auto-scheduling clinic follow-ups for high-risk individuals.
Population intelligence: the wing feeds surveillance, prevalence studies, obesity mapping, and public-health intervention research — positioning DTHC as a regional population-health authority.

## 14. Governance, QA, HR & Financial Intelligence
### 14.1 Quality improvement & clinical governance
AI reviews 100% of files for missed diagnoses, medication errors, documentation gaps, follow-up failures.
Quality Manager dashboards: wait times, QA discrepancy rates, guideline compliance percentages.
Closed-loop Negative Outcome Engine: a reported severe side effect, negative experience, or unexpected HbA1c spike triggers "Code Red" — a rapid forensic Root-Cause Analysis classifying the failure (clinical error / operational failure / patient-side barrier) and forcing a systemic prevention update so it never repeats.
### 14.2 Human-resource intelligence
Biometric attendance (fingerprint/facial) synced with RBAC access.
Workflow friction analysis: station-to-station timestamps compute handling times, detect bottlenecks (e.g., nutrition queue backing up), and score each operator's daily/weekly/monthly throughput.
Error-quality linkage: the §4.3 correction tallies feed each operator's record, driving targeted retraining.
Patient Satisfaction Matrix: post-visit scores (exit kiosk / SMS link) correlated to the specific operators and doctors who handled that patient.
Geospatial fleet management (Google Maps API): live map of field teams for safety, coverage validation, and route optimization.
### 14.3 Financial intelligence & pharmacy
Micro-costing engine: real-time burn rate — per-operator daily cost (biometric shift duration × throughput), the philanthropic footprint of free community diagnostics, and outreach logistics (fuel, transport, time via fleet data).
Closed-loop pharmacy: authorized prescriptions route instantly to the pharmacy UI (drugs and doses only — diagnoses hidden by RBAC); dispensing auto-deducts central inventory in real time.
Predictive supply chain: prescribing trends + seasonality + scheduled follow-ups → procurement alerts before critical stocks (e.g., specific insulin variants) run out.
### 14.4 Strategic management engine — the "CEO Co-Pilot"
Executive dashboard: clinical performance, community impact, research productivity, financial sustainability, staff efficiency in one view.
Auto-generated executive summaries and dynamic meeting agendas driven by detected anomalies.
KPI tracking (glycemic-target attainment, BP control, adherence, outreach conversions, research output), macro-RCA on underperforming initiatives, post-meeting action items auto-assigned/escalated, and a quarterly Organizational Learning Matrix: "What have we learned, and where do resources go next?"

## 15. Technical Architecture & Delivery Roadmap
### 15.1 Stack (carried from v1.0, confirmed)
Backend: Golang microservices, cloud-native on Google Cloud.
Database: PostgreSQL / AlloyDB; append-only event store as source of truth.
Web: React + Next.js. Mobile/tablet: React Native (operator apps, field apps).
Real-time: Redis + WebSockets. Interoperability: HL7 FHIR. Security: RBAC + 2FA, explicit consent tracking, immutable geo-redundant backups every 5 minutes.
### 15.2 v2.0 architecture additions
Android-first operator devices; UI optimized for one-hand phone entry on the clinic floor [R-01].
Offline-tolerant write queue: a Wi-Fi drop must never lose a station entry; events queue locally and sync with conflict-safe ordering.
Async job infrastructure for the Pre-Consultation Synthesis pipeline with the ≤ 5-minute SLA and queue monitoring [R-05].
Bilingual i18n (Bangla/English) across UI, prescriptions, SMS, and field reports.
Notification service: call-task lists for staff + programmable SMS gateway fallback [R-14].
Print pipeline for four-color laser A4, including back-page rendering [R-13].
Entry envelope as a universal write contract: {value, unit, user_id, device_id, role, station, timestamp} [R-03].
### 15.3 Phased delivery roadmap
| Phase | Scope | Key requirements |
| 0 — Now | Existing prototype: registration, anthropometry, basic entry | Baseline |
| 1 — Clinic Core (MVP) | Multi-device attributed mobile entry; error-correction workflow; counseling checklist + traffic board; async AI summary + pediatric percentiles; physician dashboard v1; prescription engine v1 (graphs, patient score, warning library, back page, dual units); curated medicine index; fail-closed QA | R-01–R-08, R-11–R-13, R-16 |
| 2 — Memory & Relationship | Records digitization + chronology + red-lines; follow-up CRM with call/SMS; satisfaction capture; closed-loop pharmacy | R-09, R-10, R-14 |
| 3 — Intelligence | Research dashboards + hypothesis engine; HR/ops analytics; micro-costing; revisit formulary integration (Option B) | R-15, R-16 review |
| 4 — Enterprise | Outreach field apps; strategic macro-brain; FHIR interoperability; multi-branch scaling | — |
Phase acceptance = features live + audit trail verified end-to-end + Dr. Nahid's sign-off. No phase closes with open critical defects.

## 16. Open Decisions — Dr. Nahid × Amlan
Medicine index: Option A (curated manual formulary, recommended) vs Option B (full database integration). Also: who owns monthly price review? [R-16]
Pediatric growth reference: WHO vs CDC vs local reference, per age band — Dr. Nahid to specify the clinic protocol. [R-06]
SMS/voice gateway vendor for Bangladesh (delivery reliability, Bangla SMS support, cost per message). [R-14]
OCR/NLP approach for records digitization: cloud service vs local model; Bangla-capable OCR requirement. [R-09]
AI model/provider strategy for the synthesis pipeline and agents; confirm the ≤ 5-minute SLA after load testing. [R-05]
Biometric attendance hardware model and vendor.
Printer standardization: four-color laser model(s) for prescriptions and field reports.
Hosting region, data residency, and backup policy formal sign-off.

## 17. Administrative Action Items — Assigned to Amlan
Dun & Bradstreet account [R-17]. DTHC already holds a DUNS number, obtained through direct email correspondence with D&B — no online account was ever created. Amlan will: open the D&B online account, link it to the existing DUNS number, verify that the business profile exactly matches DTHC's registered details, and hand credentials custody to Dr. Nahid. This is Dr. Nahid's direct instruction: "Amlan, you will open an account."
Source repository. Establish the DTHCMS repository (recommended: within the existing Arrow Health GitHub organization) and commit this document as docs/blueprint-v2.0.md, with all future revisions version-controlled there.
Handover walkthrough. Confirm receipt, schedule a walkthrough with Dr. Nahid, and log every question against a section number of this document.

## Appendix A — Delta Log: v1.0 → v2.0 (August 2026 Dictation)
Every requirement below originates in Dr. Nahid's August 2026 dictation and is binding.
| ID | Requirement (short form) | Blueprint section(s) |
| R-01 | Mobile-first station data entry (BP, anthropometry, food habits) from operators' phones | §2.1, §3, §4.2 |
| R-02 | One operator may make multiple entry types from a single device when staffing is short | §2.3, §4.2 |
| R-03 | Per-entry attribution — user + device + station + timestamp, visible instantly on review | §2.2, §4.2 |
| R-04 | Error traceability & correction workflow (canonical case: height 140 entered as 150) with recurrence prevention | §4.3, §14.2 |
| R-05 | Asynchronous AI pre-consultation analysis; "Analyze/Summarize/Comprehensive Report" button pressed by the final assistant; physician never waits; patient arrives ready | §2.5, §3 (Step 9), §7.1, §15.2 |
| R-06 | Endocrine-only AI tailoring for now; pediatric growth percentile engine with exact DOB; childhood obesity flag ≥ 95th percentile | §1.5, §3 (Steps 1–2), §7.1, §16.2 |
| R-07 | Waiting-time counseling checklist (7 seed items), room-by-room flow, mobile ticking, physician verification, fail-closed gate | §5 |
| R-08 | Dual-unit display — cm primary with feet/inches beneath; patient informed immediately | §2.6, §3 (Step 2), §9.1 |
| R-09 | External records: scan everything; exclude handwritten prescriptions from structured chronology; absolute chronological ordering with facility/test/reason; auto-summary | §6.1–6.3 |
| R-10 | Red-line highlighting of abnormalities (creatinine/pus cells → CKD pattern; BP; PCO — ovarian size, cyst count, serial change) | §6.4, §8 |
| R-11 | Ultra-modern prescription: graphs, gradient bars, patient-reported 1–10 improvement score | §9.1 |
| R-12 | Drug-specific warning library (GLP-1 first-dose GI effects; CNS drugs never stopped abruptly, physician-only dose changes; PRN hypnotics) | §9.2 |
| R-13 | Back-page physician evaluation + research rationale; anti-tamper credibility; polite standing notice to non-endocrinologists | §9.3 |
| R-14 | Follow-up CRM: chief-complaint memory, preferred call time/availability, call → SMS fallback, continuous two-way clinic line | §11 |
| R-15 | Named research analyses: HbA1c trajectories; exercise correlation; GLP-1 adverse-event % and starting-dose analysis; weight-loss response; affordability of semaglutide/tirzepatide; multi-benefit dashboards incl. SGLT2i and linagliptin; Bangladesh pricing lens | §12.1 |
| R-16 | Medicine index: 2-letter trade-name autocomplete with BDT pricing; manual curated formulary vs full integration decision (recommendation: curated first) | §10, §16.1 |
| R-17 | Administrative: open the Dun & Bradstreet account against the existing DUNS number | §17.1 |
Also embedded from the v1.0 multi-perspective stress test, now binding requirements: weekly Clinical Digest batching with instant safety-recall pushes (§7.2); heads-up UI so operators look at the patient, not the screen (§15.2); mandatory data completeness at QA (§2.7, §12.2); deterministic-only prescribing with human signature (§7.3); Clinic Traffic Control rerouting (§5.2); 5-minute geo-redundant backups, 2FA, and consent tracking (§15.1).

## Appendix B — Document Control
This document supersedes Blueprint v1.0 in full; v1.0 is retained for archive only.
Canonical system name: DTHCMS.
On Dr. Nahid's ratification, this file is frozen and its SHA-256 fingerprint recorded in the repository as the custody reference for v2.0.
Any change after freeze requires a v2.1 revision with its own delta log.
— End of Blueprint v2.0 —
