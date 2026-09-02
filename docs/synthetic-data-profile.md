# Synthetic data profile — v1

**Status: authored.** Completed 27 August 2026 by Dr. K. M. Nahid-Ul-Haque (MBBS MMC;
Diploma in Endocrinology, BIRDEM), Diabetes, Thyroid & Hormone Care, Faridpur, via
[`caseload-questionnaire.md`](caseload-questionnaire.md).

The machine-readable profile is `backend/internal/testdata/profile.v1.json`. That file is
what the generator reads; this page is why it says what it says.

---

## 1. What these numbers are, and are not

In the author's own words, kept because it governs how they may be used:

> These percentages are synthetic-data priors, not epidemiological estimates. They have
> been normalized for internal consistency and are not audited prevalence figures. No
> patient-level data were used.

So: **nothing here is a source for a clinical claim, a publication, or a business case.**
It is a shape for inventing believable fictional patients. Recalibration is explicitly
permitted when prospective, consented, properly governed aggregate clinic data exist.

Every distribution sums to 1.00 exactly — checked, not assumed.

## 2. What the answers changed

Four things in the response were not what the questionnaire was expecting, and each has
consequences beyond test data.

**Obesity is the largest presenting problem, at 34%** — larger than diabetes (21%) or
thyroid (21%). The clinic's name leads with diabetes; its caseload leads with obesity and
metabolic disease. Worth remembering when CP73 lays out a dashboard.

**The record is mixed-script by default.** About 95% of patients would have administrative
and narrative text in Bangla, while _nearly 100%_ prefer diagnoses, investigations,
medicines and other clinical terms in English. That is not a test-data fact — it is a
product finding. It says a single record routinely contains both scripts in the same
sentence, and it bears directly on the still-open question about Bengali numerals.

**Patients present late and respond hard.** Median HbA1c at first presentation is 10.0%
(range 7.5–14.0), only ~5% are at target on arrival, ~50% already have an established
complication — and yet ~70% of those retained in care reach target within a year, with a
typical fall of 3–5 percentage points in six months. A generator that produced gentle
trajectories from a median of 8% would be modelling a different clinic entirely.

**Combination therapy is the norm, early.** SGLT2 inhibitors ~80%, DPP-4 ~60%, GLP-1 ~50%,
insulin ~30%, sulfonylureas only ~10%. Those overlap heavily and deliberately: presenting
near 10% means metformin monotherapy is often skipped.

## 3. One figure worth confirming

**70% new patients / 30% follow-ups on a typical clinic day.** For an established practice
this is normally the other way round, and it drives the entire visit-generation model,
queue volumes and the CP93 load profile.

It is plausible as written — DTHC is described as a small, growing, research-oriented
clinic — and it sits consistently beside "typically 4 visits in a patient's first year"
and "about 10% do not return within a year". It is flagged in the profile rather than
silently adopted. **Confirm or correct before the generator is used for load testing.**

## 4. Denominators are not interchangeable

The comorbidity answers were given against different denominators, deliberately, and the
profile preserves each one:

| Figure                                                    | Denominator                                      |
| --------------------------------------------------------- | ------------------------------------------------ |
| Hypertension 50%, dyslipidaemia 80%                       | adults with type 2 diabetes or metabolic disease |
| Retinopathy 30%, neuropathy 50%, nephropathy 20%, foot 5% | the diabetes caseload                            |
| Fatty liver 50%                                           | type 2 diabetes, obesity or metabolic syndrome   |
| Ischaemic heart disease 20%                               | the type 2 diabetes caseload                     |
| Diabetes **and** thyroid disease 20%                      | the total clinic caseload                        |

Flattening these into "% of all patients" would produce a population that is wrong in a way
no reviewer would spot by eye.

## 5. What the generator was asked for beyond the questionnaire

The free-text answer asked for considerably more than the form did, and it is right to. It
is recorded in `generatorRequirements` in the profile, each item tagged with the checkpoint
that can actually implement it:

| Requirement                                                                                                  | Buildable at           |
| ------------------------------------------------------------------------------------------------------------ | ---------------------- |
| Realistic relationships among age, sex, diagnosis, labs, medicines, contraindications                        | CP13                   |
| Plausible longitudinal response to treatment                                                                 | CP13                   |
| **Realistic missingness** — delayed tests, missed visits, loss to follow-up                                  | CP13                   |
| Dose changes driven by eGFR, pregnancy, hepatic status, hypoglycaemia risk                                   | CP77/CP78 (needs D-22) |
| Care processes — counselling, adherence, injection technique, foot care, screening, mental health, referrals | CP55 onward            |

The missingness requirement deserves its emphasis. Synthetic data is almost always too
tidy, and a system tested only against complete records handles incomplete ones for the
first time in a clinic.

The care-process items cannot be generated because the tables that would record them do not
exist. That is not a deferral of convenience — see §6.

## 6. What CP13's generator can and cannot do

**The database has no patient tables.** Migrations to date create `core.facility`,
`core.facility_scope_exemption` and `ops.migration_checksum`. Patient registration is CP29.

So the generator cannot write patients to a database, and CP13's stated manual verification
— "generate 1,000 synthetic patients; browse them in the web app" — cannot be performed as
written, because neither the tables nor the screens exist. The plan is internally optimistic
here, in the same way it was about CP11's on-device acceptance.

What is buildable now: the population model. Sampling a coherent cohort from this profile
and emitting it as structured records, with the database sink added when there is a schema
to write into. The clinical review — do these thousand people look like DTHC's patients? —
can be done against that output without a screen.

## 7. Named test cases

The clinician named ten cases the software must be exercised against, recorded in
`testCases` in the profile. The generator should produce each **on demand**, not merely by
chance:

severe hyperglycaemia · hypoglycaemia · pregnancy · paediatric growth and puberty ·
CKD-related medication limits · thyroid extremes · insulin initiation and titration ·
treatment intolerance · polypharmacy · conflicting or missing laboratory data

A generator that can only sample randomly will produce most of these eventually and none
of them reliably. Being able to ask for one is what makes them useful in a test.

## 8. The generator, and where it goes beyond the profile

`backend/internal/platform/synthetic` samples this profile;
`backend/cmd/synthgen` is the command that drives it.

```
synthgen -n 5000 -seed 42 -out cohort.ndjson     a citable population
synthgen -n 20000 -summary                        every share, beside the profile's
synthgen -review -n 30 -with-cases -out review.html   a page for clinical review
synthgen -case hypoglycaemia -n 5                 one scenario, on demand
```

Every run is reproducible: the seed and the as-of date are inputs, never read from the
clock. A record someone objects to can be regenerated exactly.

On Linux and in CI, `make synth`, `make synth-summary` and `make synth-review` wrap those.
On Windows, where there is no `make`, `scripts/dev.ps1` carries the same three:

```powershell
.\scripts\dev.ps1 synth -N 5000 -Seed 42
.\scripts\dev.ps1 synth-summary
.\scripts\dev.ps1 synth-review
```

### 8.1 What the profile did not answer

Dr. Nahid gave three marginal distributions — the presenting mix, 20% paediatric, 70%
female among adults — but not the joint distribution between them. Nothing in the answers
says how much of the paediatric caseload presents with obesity rather than growth, and the
generator cannot sample a person without deciding.

That interpolation lives in `mix.go` and is **not attributed to him**. What makes it safe is
that it is constrained rather than free: `reconcile()` checks that the mixture it implies
reproduces every share he did give, and the profile is refused at load if it does not. So
the table can be argued with clinically, but it cannot silently drift.

Two figures are _derived_ rather than sampled, because they are properties of the whole
caseload that no per-patient draw can reach:

- **The adult type 1 rate.** Every child with diabetes here is type 1, and children are a
  fixed share of the caseload, so drawing the stated 2% for adults as well puts the caseload
  at twice its stated share. It did: type 1 came out at 8.7% before this was solved for.
- **The remainder rates for obesity and insulin.** Half the caseload is obese and a third
  present _for_ obesity; sampling the other two-thirds at one half again lands the total at
  67%. The same shape applies to insulin, which every type 1 patient is already on.

### 8.2 What needs a clinician's eye

Four areas are extrapolation and are marked `CLINICAL REVIEW WANTED` in the source:

| Where                               | What                                                                         |
| ----------------------------------- | ---------------------------------------------------------------------------- |
| `mix.go` — `caseMix`                | who presents with what, by age and sex                                       |
| `mix.go` — cross-over rates         | reaching the stated 20% means ~half of either condition carries the other    |
| `anthropometry.go`                  | stature, BMI-for-age and growth velocity — the paediatric numbers especially |
| `generator.go` — `otherMedications` | prescribing for PCOS, bone, adrenal, pituitary and male reproductive         |

The last one exists because the profile's prescribing answers are about diabetes. Four of
the nine presenting problems would otherwise leave clinic with nothing written.

### 8.3 What rendering the review page found

The page was built to be reviewed by a clinician. Rendering it caught three defects first,
none of which any distribution check could see:

1. **Height was recomputed at every visit**, so a patient went 73.5 kg → 58.5 kg between two
   appointments a quarter apart. Mean weight across the cohort was correct throughout.
2. **Children had no height, weight or BMI at all** — a paediatric endocrine clinic's records
   with the one measurement it exists to read left blank.
3. **The cautious-levothyroxine-start rule was dead code.** It read `p.Comorbidities` from
   inside a function that runs _before_ comorbidities are assigned, so its check for
   ischaemic heart disease saw an empty slice and never fired once.

Each now has a regression test. The third is the instructive one: the code was right and the
ordering was wrong, which is invisible on inspection and invisible in the output.

## 9. Provenance

Answers were given as aggregate clinical impression. No patient records, extracts or
individual cases were used or requested — the questionnaire says so explicitly and the
response confirms it. The completed document is retained as the source record.
