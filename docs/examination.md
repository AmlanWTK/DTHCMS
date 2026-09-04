# The structured examination

CP51. Blueprint §3 step 5, §12.

Station 5's clinical examination: the diabetic foot, neuropathy, the eyes, the heart. Every
finding coded, every laterality explicit, and nothing free text.

---

## Why "coded" had to be made true

Before this checkpoint a `coded` observation meant only that `value_code` was not empty.
Nothing stopped a client sending `absent`, `Absent`, `not felt` and `absent?` for the same
finding on four consecutive Tuesdays — and a research extract holding all four has no way to
tell they were the same one. Criterion 2 asks that every finding be **coded and queryable for
research**, and that is a property of the record, not an intention.

So `core.observation_answer` is the vocabulary: which answers a coded observation may take, in
clinical order, in both languages. A trigger on `read.observation` enforces it, an invariant
keeps the trigger honest, and a station app fetches the whole set once and renders it as
buttons.

**This is a departure from the plan**, which says CP51 adds no schema. It is the only way the
criterion is enforceable rather than hoped for: a coded field nobody constrains is a text field
with a shorter name.

### Clinical order, not alphabetical

`ordering` is a scale. "Present, diminished, absent" reads one way round; a screen that sorted
it alphabetically would put absent first and make every examiner read the list twice — which,
twenty findings into a two-minute examination, is where mistakes come from.

`is_normal` marks the answer that means nothing abnormal, so a screen can pre-select it and a
report can count abnormal findings without a list of magic strings in three places.

---

## Laterality is in the code

`DP_PULSE_LEFT` and `DP_PULSE_RIGHT` are two codes, following the pattern CP42 set with the
monofilament. A `side` column would have been tidier, and would have made
`WHERE code = 'DP_PULSE'` silently mean "either foot" — which for a diabetic foot is the one
question nobody may be vague about.

`assert_lateral_findings_come_in_pairs` refuses a finding that exists for one side only,
because that is a screen that silently cannot record the other.

---

## The monofilament is structured, not coded

Ten sites, and which ones were missed. A single "abnormal" would lose the finding: early
neuropathy at the hallux and a forefoot that has lost protective sensation are different
appointments.

```json
{ "felt": { "hallux": false, "toe_3": true, "…": true } }
```

Three rules, all enforced on the server:

- **All ten sites, or none.** Nine is an examiner who was interrupted. Recording the tenth as
  "not felt" invents a finding; recording it as "felt" hides one; refusing it sends them back
  to the foot.
- **`{"not_tested": true}` is a finding.** A dressing, an amputation, a patient who would not
  take their sock off. Leaving the field blank is a record that cannot tell that apart from an
  examination nobody got round to.
- **Two missed sites is loss of protective sensation.** One is within the noise of a hurried
  examination; two is the threshold every published protocol uses, and it is the line the risk
  category is drawn from.

---

## The risk category is derived

`FOOT_RISK_LEFT` and `FOOT_RISK_RIGHT` are the first derivations that produce a **code** rather
than a number, and that is the point of a structured examination: the IWGDF category falls out
of the findings, so two examiners who record the same foot the same way cannot disagree about
its risk. An examiner who could type the category would be back to an opinion with a dropdown
in front of it.

| Category   | When                                                      |
| ---------- | --------------------------------------------------------- |
| `very_low` | sensation and circulation intact                          |
| `low`      | loss of protective sensation **or** an absent pedal pulse |
| `moderate` | two of sensation / circulation / deformity                |
| `high`     | a previous ulcer or amputation on that foot               |

A history of ulceration outranks everything, because the foot that ulcerated once is the foot
that ulcerates again — and a well-healed foot examines normally. A category computed from
today's findings alone would send that patient home in the lowest band.

Like every derived value it names its formula, its **version** (`iwgdf-2019.1`; the categories
were renumbered between the 2015 and 2019 guidance) and what it was given. For a categorisation
"what it was given" is four flags rather than two measurements — loss of sensation, poor
circulation, deformity, prior ulcer — which is exactly the question a reviewer asks six months
later.

A foot nobody has examined has no category. `ErrInputsMissing`, not a refusal: "we have not
examined the left foot" tells an operator what to go and do.

---

## History-driven prompts

Criterion 4, decided on the phone from what is already in the record, and every rule is a named
function with its own test — a prompt nobody can test is a prompt that quietly stops firing.

| Rule                               | Fires when                                          |
| ---------------------------------- | --------------------------------------------------- |
| `priorUlcerOrAmputation`           | any prior ulcer above Wagner 0, or an amputation    |
| `priorLossOfProtectiveSensation`   | the last monofilament on that side missed two sites |
| `priorSightThreateningRetinopathy` | a prior grade of severe non-proliferative or worse  |
| `retinopathyScreeningDue`          | the last screening status is `due` or `never`       |

They are drawn at the top of the screen, where an examiner reads before touching the patient.

---

## Who records it

`observation.write.exam`, granted to the clinical assistant, the junior doctor and the
consultant.

CP42 parked the four placeholder EXAM codes on `observation.write.history`, because at CP42
there was no examination station app and history was the only station recording anything of the
sort. That is now wrong in a way worth fixing: a foot examination happens at station 5, and the
history officer at station 4 does not have the patient's shoes off. The four older codes moved
with the new ones.

---

## What is still owed

- **The finding set itself.** The plan is explicit that the clinical content must be authored
  by Dr. Nahid rather than derived from generic templates. What is here is a proposal built
  from the published instruments — a ten-site monofilament, Wagner grading, the IWGDF
  stratification — and it is the shape rather than the content that this checkpoint settles.
- **Two minutes, with a stopwatch.** Criterion 1 is a junior doctor, a real foot and a real
  phone. The screen is built against it — one column, 54dp targets, the normal answer first,
  nothing needing two hands — but the number is a claim until somebody times it.
- **The diagram's site positions**, checked by somebody who applies a monofilament for a
  living. They are drawn from the standard sites; whether the drawing reads as a foot at arm's
  length is not something a test can answer.
