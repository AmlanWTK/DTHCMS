# Synthetic data profile — v1 (awaiting clinical input)

The generator built at CP13 produces synthetic patients for testing, staging, demos, load
tests and AI evaluation. It needs to know what a DTHC patient actually looks like, and
that is a clinical question, not an engineering one — so this page is the machinery and
Dr. Nahid supplies the medicine.

**Status: unfilled.** Every `___` below is waiting on Dr. Nahid. The generator will not be
built against guessed distributions; implausible synthetic data produces misleading demos,
bad load tests and an AI evaluation set that rewards the wrong answers.

---

## Before you fill this in — one rule

**Aggregate proportions only. No records, no individuals, nothing identifiable.**

What is wanted here is "roughly 70% of the diabetes caseload is type 2" — a shape, not a
sample. Do not copy figures out of a patient management system, do not attach a
spreadsheet of visits, and do not work from a specific person's chart even from memory.
Your clinical impression of the distribution is exactly the right input and carries no
patient data at all.

If a number would be more accurate by looking it up, an approximation is fine. The
generator needs plausibility, not precision — an HbA1c distribution that is roughly right
is worth far more than one that is exactly right, and nothing here should cost you an
afternoon.

A shorthand is available anywhere below: write **`default`** and I will use a published
South Asian or Bangladeshi figure, cite the source in the generator config, and mark that
field `unverified` so it is visibly not your number.

---

## 1. Who attends

| Field                                     | Value                                      |
| ----------------------------------------- | ------------------------------------------ |
| Adult / paediatric split                  | ___% adult, ___% paediatric (under 18)     |
| Sex ratio, adults                         | ___% female, ___% male                     |
| Adult age distribution                    | median ___ years, most between ___ and ___ |
| Paediatric age distribution               | median ___ years, range ___ to ___         |
| Urban / rural                             | ___% urban, ___% rural                     |
| New patients vs follow-up, per clinic day | ___% new, ___% follow-up                   |

## 2. Why they attend

Proportion of the caseload by **primary** presenting problem. A patient with both diabetes
and hypothyroidism counts once here, under whichever brought them in; overlap is captured
in §6.

| Presenting problem                   | Share |
| ------------------------------------ | ----- |
| Diabetes mellitus                    | ___%  |
| Thyroid disorder                     | ___%  |
| Obesity / metabolic syndrome         | ___%  |
| PCOS                                 | ___%  |
| Growth or pubertal disorder          | ___%  |
| Osteoporosis / calcium and vitamin D | ___%  |
| Adrenal                              | ___%  |
| Pituitary                            | ___%  |
| Other (please name)                  | ___%  |

## 3. Diabetes

| Field                                                 | Value                                  |
| ----------------------------------------------------- | -------------------------------------- |
| Type 2                                                | ___% of the diabetes caseload          |
| Type 1                                                | ___%                                   |
| Gestational                                           | ___%                                   |
| Other / secondary / MODY                              | ___%                                   |
| Median age at diagnosis, type 2                       | ___ years                              |
| Median age at diagnosis, type 1                       | ___ years                              |
| Disease duration at first visit                       | median ___ years, range ___ to ___     |
| HbA1c at **first** presentation                       | median ___%, typical range ___ to ___% |
| Proportion at target (<7%) at first visit             | ___%                                   |
| Proportion at target after 12 months of care          | ___%                                   |
| Typical HbA1c fall in the first 6 months on treatment | ___ percentage points                  |
| Proportion presenting with a complication already     | ___%                                   |

## 4. Thyroid

| Field                                        | Value                              |
| -------------------------------------------- | ---------------------------------- |
| Primary hypothyroidism                       | ___% of the thyroid caseload       |
| Subclinical hypothyroidism                   | ___%                               |
| Hyperthyroidism / Graves                     | ___%                               |
| Goitre, euthyroid                            | ___%                               |
| Thyroid nodule under surveillance            | ___%                               |
| Post-thyroidectomy / post-RAI on replacement | ___%                               |
| Thyroid cancer follow-up                     | ___%                               |
| Female:male ratio in this group              | ___ : ___                          |
| TSH at presentation, hypothyroid             | median ___ mIU/L, range ___ to ___ |
| TSH at presentation, hyperthyroid            | median ___ mIU/L, range ___ to ___ |
| Typical levothyroxine starting dose          | ___ mcg/day                        |

## 5. Other endocrine — brief

Only if these appear often enough to matter in test data. Skip any that do not.

| Condition     | Share of caseload | Anything characteristic worth generating |
| ------------- | ----------------- | ---------------------------------------- |
| PCOS          | ___%              | ___                                      |
| Obesity       | ___%              | typical BMI range ___                    |
| Osteoporosis  | ___%              | ___                                      |
| Short stature | ___%              | ___                                      |
| Other         | ___%              | ___                                      |

## 6. Comorbidities

Proportion of patients carrying each, so that generated histories hang together rather
than being a diabetic with no other findings at all.

| Comorbidity                           | Share of patients |
| ------------------------------------- | ----------------- |
| Hypertension                          | ___%              |
| Dyslipidaemia                         | ___%              |
| Diabetic retinopathy                  | ___%              |
| Diabetic neuropathy                   | ___%              |
| Diabetic nephropathy / CKD            | ___%              |
| Diabetic foot                         | ___%              |
| NAFLD                                 | ___%              |
| Ischaemic heart disease               | ___%              |
| Both diabetes **and** thyroid disease | ___%              |

## 7. Medication patterns

What a generated prescription should plausibly contain. Named drugs and typical dosing,
as you would actually write them.

| Field                                                                        | Value          |
| ---------------------------------------------------------------------------- | -------------- |
| First-line, newly diagnosed type 2                                           | ___            |
| Most common two-drug combination                                             | ___            |
| Most common three-drug combination                                           | ___            |
| Proportion of type 2 patients on insulin                                     | ___%           |
| Typical insulin regimen                                                      | ___            |
| SGLT2 inhibitor use                                                          | ___% of type 2 |
| GLP-1 RA use                                                                 | ___% of type 2 |
| DPP-4 inhibitor use                                                          | ___% of type 2 |
| Sulfonylurea use                                                             | ___% of type 2 |
| Typical statin and dose                                                      | ___            |
| Anything commonly prescribed here that a Western guideline would not predict | ___            |

## 8. Visits and continuity

| Field                                              | Value      |
| -------------------------------------------------- | ---------- |
| Routine follow-up interval, stable patient         | ___ months |
| Follow-up interval, poorly controlled              | ___ weeks  |
| Proportion who do not return within 12 months      | ___%       |
| Typical number of visits in a patient's first year | ___        |
| Investigations ordered at a routine follow-up      | ___        |

## 9. Names and language

The generator produces names in both scripts. Two things only you can settle:

| Field                                                                           | Value |
| ------------------------------------------------------------------------------- | ----- |
| Approximate Muslim / Hindu / other name mix                                     | ___   |
| Proportion of patients whose record would be kept in Bangla rather than English | ___%  |

---

## What happens to this

Once filled, this becomes `backend/internal/testdata/profile.v1.json` — versioned, so a
dataset generated six months from now can be reproduced exactly, and so that changing the
profile is a visible decision rather than a quiet drift in what "realistic" means.

The manual verification for CP13 is that you look at 1,000 generated patients and say
whether they look like your clinic. This document is what that judgement is measured
against.
