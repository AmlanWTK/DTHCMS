# Observations, units and derived values

CP42, CP43 and CP44. Blueprint §2, §3, §6, §11.

Ten stations record measured values. This is the one way they all do it.

---

## Why one model

Ten bespoke tables would make the timeline, the research extract and the FHIR mapping ten
times harder, and would guarantee that the eleventh station invented an eleventh shape. So
there is one table (`read.observation`), one event type (`OBSERVATION_RECORDED`), one write
endpoint (`POST /v1/observations`) — and a **code registry** that says what each kind of
value is.

A code declares five things:

| Column              | What it decides                                                                |
| ------------------- | ------------------------------------------------------------------------------ |
| `category`          | Which of §6's seven this is: ANTHRO, VITAL, EXAM, LAB, DERIVED, SCREENING, PRO |
| `value_type`        | Its shape: numeric, text, boolean, coded, structured                           |
| `dimension`         | Which units it may be entered in — empty means it takes none                   |
| `min/max_canonical` | The band outside which the number is a typing error                            |
| `write_permission`  | Which permission recording it needs                                            |

Adding a station is adding rows to that registry, not a table and an endpoint.

---

## The unit rule

**A unit-bearing observation cannot be stored without a valid unit.**

A weight of 154 with no unit is 154 kg or 154 lb depending on who reads it, and a drug dose
computed from it is wrong by a factor of 2.2. The blueprint asks for this to be
_structurally prevented_, which means it cannot be a validation somebody remembers to run.

Three things together make it structural:

1. **The registry** says whether a code is unit-bearing, by naming its dimension.
2. **`core.observation_is_well_formed()`** is a trigger on `read.observation`. A row lacking
   the unit its code requires cannot be inserted — not "is rejected by the API", _cannot_,
   by anything, including a projection rebuild and a hand-written `UPDATE`.
3. **`core.assert_measurements_carry_their_units()`** is a standing invariant, so a schema
   change that weakened the trigger fails the deployment rather than the next audit.

The Go service checks the same things first, so an operator sees a sentence in their own
language rather than a 500. That is not redundancy to be tidied away: the service layer
protects the person typing, and the trigger protects the record.

### Both values are stored

| Column                         | What it is                                |
| ------------------------------ | ----------------------------------------- |
| `value_num` + `unit`           | Canonical. What everything computes from. |
| `entered_num` + `entered_unit` | Exactly as typed.                         |

That second pair is what makes "conversions round-trip without precision loss" true rather
than approximately true. Showing a value back in the unit it was entered in is a **read**,
not a round trip: 154 lb comes back as 154 lb, not as 69.85 kg converted back to 153.99 lb.

### The tolerance

`numeric` throughout, not `double precision`. Every factor in `core.unit` is a terminating
decimal, so `core.to_canonical` and `core.from_canonical` are exact inverses for every unit
in the table today.

**The documented tolerance is 1e-6**, checked by `core.assert_units_convert_both_ways()` over
every unit at deployment time. It is not zero because a factor added later may repeat — and
that check is what catches it, in a deployment, rather than in a patient's weight three
months later.

### Dimensions are analyte-specific where the chemistry demands it

`glucose_concentration` and `cholesterol_concentration` are separate dimensions even though
both are mmol/L and mg/dL, because the factor between them is a molar mass and glucose's is
not cholesterol's. One shared "concentration" dimension would silently convert a cholesterol
with glucose's constant — a wrong number that looks entirely plausible.

| Analyte      | Molecular weight | 1 mg/dL is     |
| ------------ | ---------------- | -------------- |
| Glucose      | 180.16 g/mol     | 0.05551 mmol/L |
| Cholesterol  | 386.65 g/mol     | 0.02586 mmol/L |
| Triglyceride | 885.4 g/mol      | 0.01129 mmol/L |
| Creatinine   | 113.12 g/mol     | 88.42 µmol/L   |

HbA1c converts by the IFCC/NGSP master equation, `IFCC = (NGSP − 2.15) × 10.929`, which is
linear and so fits the same factor-and-offset column. Temperature is the one offset
conversion.

### LOINC is empty where it is not known

An empty code is honest. A guessed one is a mapping error that surfaces years later, in an
export, in somebody else's system, after the person who guessed has left. CP150's FHIR
mapping is where the gaps get filled, by somebody looking them up.

---

## Correction and supersession

Nothing is deleted. An earlier row stops being the value and says which row took its place:

- **CORRECTED** — it was wrong.
- **SUPERSEDED** — it was right and has been re-measured.

Two different facts. A report that conflated them would count re-measurements as an error
rate, which is the kind of number that ends up in a quality review.

---

## Derived values (CP43)

A BMI is not a measurement. It is the output of an equation applied to two measurements, and
three facts about it have to survive: **which** equation, **which version**, and **what it
was given**. A DERIVED observation without all three cannot be stored.

The version is the one people forget and the one that matters most. CKD-EPI was revised in
2021 to remove a race coefficient that raised the estimate for Black patients with no
physiological justification and delayed referrals as a result. A stored "eGFR 62" with no
version cannot afterwards be told apart from one computed under the old equation — and a
system that silently recomputed the old values with the new equation would rewrite history.

The inputs are stored rather than re-derived because they are what the formula _actually
saw_: a weight corrected an hour later does not change what a BMI was computed from.

### The server computes

`POST /v1/observations/derive` takes no number. A client-computed value posted to the record
would make the client authoritative about a clinical value, and "it uses the same library"
does not fix that — an old app build, a modified one, or a request assembled by hand would
all be accepted.

`@dthcms/clinical-calc` exists so an operator sees a BMI the instant they type a height and a
weight (P-4). The record keeps the server's.

### The formulas, and where they come from

| Formula                        | Version | Source                                                  |
| ------------------------------ | ------- | ------------------------------------------------------- |
| BMI                            | 1.0.0   | Quetelet index; WHO TRS 894 (2000)                      |
| Obesity class, international   | 1.0.0   | WHO TRS 894 (2000), Table 2.1                           |
| Obesity class, **Asian**       | 1.0.0   | WHO expert consultation, Lancet 2004;363:157–163        |
| Waist-hip ratio                | 1.0.0   | WHO Expert Consultation, Geneva 2008                    |
| BMR, **Mifflin-St Jeor**       | 1.0.0   | Mifflin MD et al., Am J Clin Nutr 1990;51(2):241–247    |
| BMR, Harris-Benedict (revised) | 1.0.0   | Roza AM, Shizgal HM, Am J Clin Nutr 1984;40(1):168–182  |
| Ideal body weight              | 1.0.0   | Devine BJ, Drug Intell Clin Pharm 1974;8:650–655        |
| BSA, Du Bois                   | 1.0.0   | Du Bois D & Du Bois EF, Arch Intern Med 1916;17:863–871 |
| BSA, Mosteller                 | 1.0.0   | Mosteller RD, N Engl J Med 1987;317(17):1098            |
| eGFR, **CKD-EPI 2021**         | 2021.1  | Inker LA et al., N Engl J Med 2021;385:1737–1749        |
| eGFR, bedside Schwartz         | 2009.1  | Schwartz GJ et al., J Am Soc Nephrol 2009;20(3):629–637 |
| Pack-years                     | 1.0.0   | Definition (20 cigarettes = one pack)                   |

**The clinic uses the Asian obesity cut-offs**, and the difference is not cosmetic: a BMI of
24 is "normal" internationally and "overweight" in a Bangladeshi patient, and the whole
screening pathway hangs on which side of that line somebody falls.

**Mifflin-St Jeor is the default BMR** (the open decision this checkpoint carried). Harris-
Benedict was fitted in 1919 on a cohort that does not resemble a modern population and
overestimates resting expenditure by roughly 5%; Mifflin-St Jeor is what the Academy of
Nutrition and Dietetics recommends and what a nutritionist trained in the last thirty years
expects. Harris-Benedict stays available for comparison.

### Parity

Both implementations read `packages/clinical-calc/fixtures/reference.json`, and the Go and
TypeScript suites run the same case list through the same dispatch. Go and TS agreeing on
100% of fixtures is what green means on both sides.

**Every expected value in that file was computed independently from the published equation**,
not read off either implementation. A fixture derived from the code would prove only that the
code agrees with itself, which is exactly the failure this exists to catch.

Tolerance: 1e-9 absolute. Not exact, because the two runtimes order floating-point operations
differently and `pow()` is not required to be correctly rounded in either.

### Refusing rather than guessing

Every function returns a result _or_ a refusal. A BMI computed from a height of zero is
`Infinity`, which serialises to `null` and renders as an empty cell — a wrong answer that
looks like a missing one.

The four refusals are distinct because they send a person to different places:
`not_positive`, `out_of_range`, `sex_unsupported`, `missing_input`.

`sex_unsupported` is worth defending. `core.patient.sex` allows `other`; CKD-EPI and
Mifflin-St Jeor publish coefficients for two. Choosing one would be inventing clinical
evidence and averaging them would be inventing two, so the library refuses and the interface
asks.

---

## Dual-unit display (CP44)

[R-08], and a named non-negotiable in §2: **every clinical value shows the clinical unit with
the patient-familiar equivalent beneath it.**

The clinician thinks in one and the patient thinks in the other, and a screen that makes
either of them convert in their head is a screen where somebody converts wrongly.

### Rounding

| Canonical unit               | Written with | Second unit               | Written with |
| ---------------------------- | ------------ | ------------------------- | ------------ |
| cm                           | 1 dp         | in (or ft′in″ for height) | whole inches |
| kg                           | 1 dp         | lb                        | 1 dp         |
| °C                           | 1 dp         | °F                        | 1 dp         |
| mmHg, /min, %, mL/min/1.73m² | 0 dp         | —                         | —            |
| kg/m²                        | 1 dp         | —                         | —            |
| mmol/L (glucose)             | 1 dp         | mg/dL                     | 0 dp         |
| mmol/L (lipids)              | 2 dp         | mg/dL                     | 0 dp         |
| µmol/L                       | 0 dp         | mg/dL                     | 2 dp         |
| mmol/mol                     | 0 dp         | % NGSP                    | 1 dp         |

### Feet and inches are for height and nothing else

A waist of 94 cm is "37 inches" to everybody who has ever bought trousers. Rendering it as
3′1″ is arithmetically correct and clinically absurd — so the **code** decides, not the unit,
and a new circumference added to the registry gets plain inches by default.

### Values that show one unit

mmHg, /min, %, kg/m², mL/min/1.73m², m², kcal/day. Showing "128 mmHg / 17.1 kPa" beneath a
blood pressure would be noise on the one reading nobody in this clinic reads in kilopascals.
The absence is a decision, written down in `DISPLAY_PAIRS`.

### Where the display factors live, and why they are not fetched

`/v1/observations/units` deliberately does not return conversion factors: the conversion that
decides what is _stored_ happens once, in the database, so a client cannot arrive at a
different canonical value from the one the server will write.

Display is a different problem. A tablet in a corridor with no signal still has to draw
"69.5 kg / 153.2 lb", so the display factors live in `@dthcms/clinical-calc`. Two copies of a
factor is one copy that drifts — so `TestTheDisplayUnitsAgreeWithTheDatabase` reads the
TypeScript source from the Go suite and fails if they ever diverge.

### The rule is a test, not a checklist

`web/test/dual-unit.test.tsx` scans every `.tsx` under `web/src` and fails a build where a
screen renders an observation value without going through `DualUnitValue`. A checklist item
is followed on nine screens and forgotten on the tenth.

---

## What is still open

| Item                                                                                                                                                                                                                                               | Owner             | Blocks                                |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------- | ------------------------------------- |
| **D-23** — the age at which a patient stops being a child for eGFR. Schwartz's cohort is 1–16; adult CKD-EPI is fitted from 18; the two do not meet. The library refuses above 18 and leaves the boundary visible rather than picking one quietly. | Dr. Nahid         | CP47's paediatric work                |
| The initial observation code list is what §3 and §6 name plus what CP45–CP51 will need. A clinician should read it.                                                                                                                                | Dr. Nahid         | Nothing; codes are data               |
| LOINC gaps — hip circumference, mid-upper arm, body fat, eGFR, and the derived values                                                                                                                                                              | Dr. Nahid + Amlan | CP150's FHIR mapping                  |
| Which value types get dual display beyond height, weight, waist, hip, temperature and the analytes                                                                                                                                                 | Dr. Nahid         | Nothing; `DISPLAY_PAIRS` is one table |

---

## What CP45–CP49 added

The model above did not change. What was added were rows, one endpoint and two new kinds of
rule — which is the point of a registry: a station becomes real by adding data, not tables.

- **`MUSCLE_MASS` and `IBW`** (CP45). The ideal weight is `DERIVED`, so it cannot be typed:
  nobody measures one, and 00027's trigger refuses a derived value with no formula behind it.
- **`POST /v1/observations/batch`** (CP45). A station form in one transaction and one round
  trip. Still one ledger event per measurement — six measurements are six facts.
- **`core.plausibility_rule`** (CP46) and **`core.reference_range`** (CP49). Three different
  things a number can be outside, kept apart on purpose: a typing error, a value worth a
  second look, and a critical value (CP50). Both are data, both are served to the station app
  already ordered most specific first, and both carry an `approved` flag that is false until a
  clinician says otherwise.
- **`implausible_confirmed` and `implausible_reason`** on the observation and in the ledger
  (CP46), so an override is a fact the clinic can count rather than an opinion.
- **`core.growth_*`** and `/v1/patients/{id}/growth` (CP47). See `docs/growth.md`.
- **`BP_ARM`, `BP_POSITION`, `BP_CUFF`** (CP49) — coded observations sharing the reading's
  effective time, rather than columns on a blood-pressure row. Adding per-code columns to a
  uniform model is how ten stations became ten tables in every system this one replaces.

One correction to something the model got wrong. `ObservationsForPatient` broke ties on
`code`, so two values of the same code sharing an effective time gave an arbitrary "current"
value — and a BMI derived from the wrong one of two heights is a plausible-looking wrong
number. The tie-break is now the ledger's own sequence.
