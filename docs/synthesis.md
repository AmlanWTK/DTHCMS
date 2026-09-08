# The pre-consultation synthesis

CP71. Blueprint §7.1 and §8, implementation plan §10.4 A1, R-05, R-06, D-14, D-15, D-28, ADR-0033.

The one thing this exists to make true: **the patient arrives ready.** By the time the physician
opens a file there is a page of narrative and a draft plan on the screen, assembled while the
patient was still in counselling, and nobody had to ask for it.

---

## The three steps, and which of them is a model

```
Gather    database → Raw       one read per station module, no logic
Assemble  Raw      → Context   all the logic, no I/O, no model, no clock but the one it is handed
Invoke    Context  → Output    the gateway (CP70), and the only step a model is in
```

`Assemble` is a **pure function**, and that is the checkpoint's phrase "deterministic assembly" made
into a property rather than an intention. What reaches the model is built by code from stored facts;
the model is never asked to go and look, and there is nothing here it could look with.

The half of this checkpoint that decides whether a summary is any good is `Assemble`, and it has
tests that need no database, no network and no key.

---

## What the model is shown

One JSON object. Sections, in the order a consultant reads:

| Section                | What is in it                                                              |
| ---------------------- | -------------------------------------------------------------------------- |
| `demographics`         | age in months, age in words, sex. **Nothing else about who this is.**      |
| `visit`                | type, clinic day, chief complaint, every planned station and its state     |
| `allergies`            | the status, then the items. `unknown` is not `none`                        |
| `history`              | conditions, medications, family history, and whether each was confirmed    |
| `current_measurements` | the **newest value per code**, with a flag where the system has an opinion |
| `trends`               | up to five points of nine codes, with the change **already computed**      |
| `growth`               | [R-06]: percentiles, z-scores, velocity and the ≥95th-centile flag         |
| `lifestyle`            | station 3's composite and which domains are still missing                  |
| `nutrition`            | the day's 24-hour recall as four totals                                    |
| `exercise`             | what the patient can do, what they must not, and the plan issued           |
| `alerts`               | critical values the clinic's own rules already raised                      |
| `prior_visits`         | §11.1's memory of the last three closed visits                             |
| `gaps`                 | **what is missing.** See below                                             |
| `facts`                | every citable thing above, flattened, each with a reference                |

### The fact index

```json
{
  "ref": "obs.hba1c:2026-03-12",
  "kind": "observation",
  "label": "HbA1c",
  "value": "8.2",
  "unit": "%",
  "on": "2026-03-12",
  "note": "outside_reference_range"
}
```

The prompt requires every clinical claim to cite the references it rests on, and the output schema
has a `citations` array. Three things follow, and only the first is obvious:

- a reviewer checks a sentence against a row rather than against their memory of the file;
- CP72's validator becomes set membership over `context.facts`, not a re-derivation of the record
  (it does exactly that, and adds a number and a date arm beside it — see `docs/ai-grounding.md`)
  months later against code that has moved on;
- a model that cannot find a fact to cite tends to say less, which is the failure direction to
  prefer in a clinical summary.

`value` is a **string**, formatted once. Grounding is a comparison, and a comparison between the
model's "8.2" and a `float64` of 8.199999999999999 is a comparison somebody has to write a tolerance
for.

### The references carry a colon, and it is load-bearing

`obs.hba1c:2026-03-12`, not `obs.hba1c.2026-03-12`. The gateway's scrubber replaces **nine digits
separated by space, dot, bracket, plus or hyphen** with `[NUMBER]`. A date is eight such digits;
anything that puts a ninth in front makes the whole reference a telephone number to the scrubber.
`obs.spo2.2026-06-09` does exactly that — the code ends in a digit and the dot counts — and the
reference would reach the model redacted, so it would be asked to cite something it never saw.

This was found by the database refusing to store the assembled context for a third of the synthetic
cohort, not by reasoning. A duplicate reference has the same trap and takes a **letter** suffix
(`…:2026-03-12.b`) for the same reason.

### The gaps section is the cheapest quality improvement here

A language model shown a record with no HbA1c writes a summary that does not mention HbA1c, and the
physician reads a confident page with a hole in it. Nothing about a model notices an absence;
noticing absences is what a deterministic pass is _for_.

The rules are deliberately few — a gap list of thirty entries is a gap list nobody reads:

| Code                     | Fires when                                                         |
| ------------------------ | ------------------------------------------------------------------ |
| `station_not_completed`  | a required pre-consultation station has no finished encounter      |
| `allergy_status_unknown` | nobody has recorded whether this patient has allergies             |
| `no_hba1c_in_12_months`  | diabetes in the history and no HbA1c inside a year (P-7, softened) |
| `no_blood_pressure`      | no systolic on record                                              |
| `no_weight`              | no weight, so no BMI and no trajectory                             |
| `no_renal_function`      | on regular medication with no creatinine inside a year             |
| `no_history_recorded`    | no medical history at any visit                                    |
| `history_not_confirmed`  | items carried forward that nobody confirmed today                  |

`diabetic` reads the **history**, never the HbA1c. A raised HbA1c is how somebody _becomes_
diabetic, and inferring the diagnosis from the number would report "no HbA1c for a diabetic patient"
for every patient who has never had one — the gap list would be noise on the day it should be
signal.

---

## What the model is deliberately not shown

**No identifiers.** Not the name, the phone number or the date of birth. This is structural rather
than careful: `Raw` — the input to `Assemble` — has no field that could hold one. The patient record
is opened in exactly one place in the module, where the gateway's `Subject` is built from it, and
CP70 strikes the identifiers out and puts them back.

**No operator names.** §4.2's attribution is a property of the record and belongs on the screen. A
payload naming the four members of staff who touched this patient would be sending their personal
data abroad to no clinical purpose — and a staff name is exactly the third-party name the scrubber
cannot catch.

**No free text that trips the shared pattern list.** A note reading "husband will bring the report,
call 01711-…" is **withheld whole**, not scrubbed. Scrubbing produces "call [NUMBER]", which a model
reads around and sometimes narrates, so the redaction becomes content — and a note with a telephone
number in it is a data-quality problem a summary should not launder. `core.ai_synthesis.context`
carries `ops.carries_identifier` as a check constraint, so the alternative to withholding is a
synthesis that cannot be stored at all.

---

## The trigger model

**Automatic is the primary path; the button is the fallback.** §7.1 gives the button to the last
assistant in the flow and then says the physician _"by design never needs to"_ press it.

The automatic trigger fires when a station touch ends, through a `StationHook` that `visit` declares
and the composition root wires — **inside the departing operator's transaction**, so a rolled-back
station touch takes its summary request with it (ADR-0031's whole point).

**"All stations complete" is `core.station_sequence`'s own definition:** every required station in
this visit type's plan, at a position before `STN_CONSULTATION`, with a finished encounter. Nothing
about that lives in Go. The sequence is an operational decision the clinic owns and will change.

A hook failure never fails the station touch — D-15 — and it runs in a **savepoint**, because a
failed statement aborts the whole PostgreSQL transaction and swallowing the error would lose the
clinical write a moment later while blaming the wrong thing.

`trigger` is recorded on every run as `AUTOMATIC`, `MANUAL` or `RERUN`. **A clinic whose runs are
mostly `MANUAL` has failed acceptance criterion 2**, and `GET /v1/ops/ai/synthesis-sla` is where
anybody would find out.

---

## Re-runs, and what "material" means

One place, one function: `Context.MaterialSHA256`. **A change is material when it changes what the
model would be shown.**

It covers the fact index, the complaint, the station picture, the allergy status, the gaps and the
assembler version. It does not cover `assembled_at` — otherwise every look would conclude the
summary was stale — and it does not cover orderings the assembler could change without meaning to.

A corrected blood pressure changes a fact and is material. A typo in a phone number cannot be
material, because no phone number ever enters a context. An operator's name, a device id, a queue
reroute: none of them reach one either.

A re-run assembles, compares, and **calls no model when the hash is unchanged**, recording state
`UNCHANGED`. That is a completion rather than a skipped job: "we looked and there was nothing new"
is a different fact from "nobody looked".

**The stated gap.** The re-run trigger fires from station touches and the button. A correction typed
at the consultation desk does not itself trigger one — the materiality machinery would handle it,
nothing calls it. Wiring `clinical`'s correction path to the same hook is a small change and is not
in this checkpoint.

---

## What the physician sees when it has not worked

D-15: _fail visible, never fail silent, never fail invented._ `GET /v1/visits/{id}/synthesis` **never
answers 404 for a visit that exists.**

| State           | The screen                                                                 |
| --------------- | -------------------------------------------------------------------------- |
| `NOT_REQUESTED` | "No AI summary has been prepared. The structured record is shown." Button. |
| `PENDING`       | "The AI summary is being prepared." Structured record underneath.          |
| `RUNNING`       | Same.                                                                      |
| `READY`         | The narrative, with a persistent AI-draft marking.                         |
| `UNCHANGED`     | The previous generation's narrative, saying the record has not changed.    |
| `FAILED`        | "AI summary unavailable — the structured record is shown", and why.        |

Every state carries the **assembled context**, which is what "raw structured data shown" means in
practice. A failed run keeps the context it was assembled with, so the physician's screen is a
complete structured record with a banner rather than an empty panel.

Failures are classified by what the physician should do about it, not by which Go error came back:
`ASSEMBLY` (our defect), `REFUSED` (the gateway would not send it), `PROVIDER`, `TIMEOUT`,
`INVALID_OUTPUT` (the model answered and the answer failed its schema on every attempt), `INTERNAL`.

**Only `PROVIDER` and `TIMEOUT` are retried.** An assembly fault will fail identically five times; a
refusal will be refused identically; an invalid answer has already been retried inside the gateway
against the same prompt. Retrying any of those spends the SLA budget on a foregone conclusion and
leaves the physician waiting longer for the same degraded screen.

---

## The SLA, and what has actually been measured

The budget is the **queue's**: `ops.job_kind.sla_seconds` for `clinical.synthesis`, seeded at
§7.1's 300 seconds and stamped onto the job at enqueue. There is no second copy in Go.

`GET /v1/ops/ai/synthesis-sla` reports, over a window: how many finished, how many met, how many
failed, how many were `MANUAL`, and the ninety-fifth percentile from request to finish — the
statistic acceptance criterion 1 asks about directly, rather than a mean that hides the tail.

**§7.1's five minutes is a ceiling, not a target.** The operational target should be tightened once
there is production data to tighten it against; nothing here proposes a number, because a number
invented now would be presented later as though it had been measured.

What _has_ been measured, on the twenty-visit synthetic run with no model contacted:

| Figure                             | Value                       | What it is worth                                 |
| ---------------------------------- | --------------------------- | ------------------------------------------------ |
| Assemble + store + record, per run | ~65 ms end to end           | real; the model call is not in it                |
| Assembled payload                  | ~4,600 input tokens         | **real** — this is the payload's actual size     |
| Answer                             | ~700 output tokens          | the composer's, not a model's                    |
| Cost at Gemini 2.5 Pro list rates  | ~1.3 US cents per encounter | D-14's order-of-magnitude, with the caveat below |

The token counts come from the mock's four-characters-per-token heuristic and the prices are
published list rates (`core.ai_model`, seeded at CP70 and marked as a proposal). The **input** side
is a genuine measurement of what this assembler produces for a real cohort patient; the output side
is not. D-14 asked for this to be measured at CP71 rather than assumed, and this is as far as it can
honestly be taken without a paid credential.

---

## Storage

One row per **run** — `core.ai_synthesis` — never an update in place. A re-run inserts the next
generation and stamps the previous one `superseded_at`, because "what was the physician looking at
ten minutes ago" is exactly the question a medico-legal review asks.

The row carries no `accepted` column. Per-item acceptance is D-28 and CP73's; a boolean here would
let a future reader think a summary had been agreed with when all that happened was a page load.

Three invariants (`migrate verify`):

| #   | Name                                            | What it refuses                                                   |
| --- | ----------------------------------------------- | ----------------------------------------------------------------- |
| 103 | `assert_every_synthesis_names_what_produced_it` | a summary a physician can read whose prompt and model are unknown |
| 104 | `assert_no_synthesis_is_stuck_in_flight`        | a screen that has said "preparing" for twenty minutes             |
| 105 | `assert_every_synthesis_kept_its_input`         | a summary with no context behind it                               |

Plus the check constraint that matters most: `NOT ops.carries_identifier(context)`, the same rule
the gateway's outbound payload carries, one step earlier and on the artefact the assembler owns.

---

## Permissions

| Permission             | Who holds it                                                                                   | Sensitive |
| ---------------------- | ---------------------------------------------------------------------------------------------- | --------- |
| `ai.synthesis.read`    | physician, junior doctor                                                                       | **yes**   |
| `ai.synthesis.request` | clinical assistant, junior doctor, nutritionist, exercise specialist, physician, administrator | no        |

Two permissions and not one, deliberately. The exercise specialist who finishes the last station
before the consultation presses the button and has no business reading what comes back; the
administrator can ask for a summary for support purposes and cannot read one. Reading is sensitive
because the narrative is the patient's whole clinical picture in prose, which is exactly what §4.4
blinds registration and the pharmacist from.

---

## The English-only decision

**The narrative is English.** Clinical shorthand does not translate cleanly, and a bilingual summary
doubles the surface a hallucination can hide behind — a wrong sentence in Bangla would have to be
caught by a grounding check that reads Bangla, and CP72 does not have one.

Everything patient-facing in this system stays bilingual, and so does the interface _around_ the
summary: the state messages, the failure sentences and the error bodies are all in both languages.
What is English is the document one physician reads.

**This is recorded in `progress.md` as awaiting Dr. Nahid's confirmation.** It is his call.

---

## What is missing, and why

**There is no formulary, so the drug-name test does not exist.** The plan's testing note for this
checkpoint asks for _"a test asserting the synthesis contains no invented drug names (validated
against the formulary)"_. The formulary is CP75. Nothing here stubs one, and nothing here pretends
the test exists. What can be checked today is the stronger property anyway — that every claim
resolves to something the assembler supplied — and that is CP72's, one checkpoint away. **The gap is
open and belongs to CP75.**

> **Updated at CP72.** The drug arm now exists and is _armed to refuse_: with no formulary, every
> drug name a model writes is unrecognised, and §10.4 A1's own rule — _"an unrecognised drug name is
> dropped, not displayed"_ — withholds the summary. So the gap has changed shape rather than
> closed. Nothing is blocked today because the deterministic composer names no drug; a real model
> answering this prompt very probably would be. `docs/ai-grounding.md` §5 states the cost and the
> two ways out, and `TestTheDrugArmPassesANameTheFormularyKnows` is the test that will stop being a
> description of an intention on the day CP75 wires a formulary into the composition root.

**There are no thyroid analytes.** `core.observation_code` has no TSH, no free T4 and no anti-TPO,
so a thyroid follow-up's summary carries the patient's weight and pressure and nothing about their
thyroid function. That is a real gap for a clinic with the word in its name. It is not this
checkpoint's to close — adding an analyte means a code, a unit, a plausibility band and a reference
range, all of which are clinical content Dr. Nahid owns — but the synthesis is where its absence
first becomes visible, so it is recorded here.

**The synthetic cohort has no coded history**, because `cmd/synthload` deliberately does not load
one: a coded diabetes diagnosis puts every patient behind CP57's counselling gate. So the twenty
review summaries all report `no_history_recorded`, and none of them exercises the history section,
the `diabetic` gap rule or the recorded-diagnosis path. **The twenty summaries are therefore a
weaker test of the assembler than they look**, and closing that means either loading counselling
sessions in the loader or accepting the gate for the evaluation cohort.

**Display precision is a property of the code, and the schema holds it per unit.** Values reach the
model with three trimmed decimals — "33.012 kg/m²" where a physician writes 33.0. `core.unit` has a
`decimals` column, and it cannot be used: HbA1c and oxygen saturation share `%`, the column says
nought decimals, and an HbA1c of 8.2 would round to 8. The right home for the number is
`core.observation_code`, one column, and it is not this checkpoint's to add. The unit's _name_ is
used — the payload says `mmHg`, not `mm[Hg]`.

**Records chronology is Phase 2** (CP109), so nothing from a scanned external document reaches a
summary yet. §10.4 A1 names it as an input; the assembler has no section for it.

---

## Reading the twenty summaries

`tools/synthshots` walks visits from the synthetic cohort through the real pipeline and writes one
page with the assembled context beside each output.

```
go run ./tools/synthshots -out ../shots/cp71-summaries.html -n 20
```

**No language model wrote any of them.** The provider is `ai.Mock` with a deterministic composer
attached, so the page is free, repeatable and needs no key — and says nothing about the prose a real
model would produce. The page opens with that in the largest words on it. Closing criterion 5 needs
the same command run against a paid credential.

What the page _is_ evidence about: what the assembler puts in front of a model, per patient, in
full; the shape a summary arrives in; and that the whole pipeline runs end to end on real cohort
data. Review the **right-hand column first** — a finding that is missing there is one no model can
recover.

---

## The manual verification for this checkpoint

```
1  make up, then: go run ./cmd/migrate up && go run ./cmd/devseed && go run ./cmd/synthload
2  start the worker with DTHCMS_WORKER_QUEUES including `clinical`
3  open a patient's visit and walk it to the last station before the consultation
4  finish that station's encounter — press nothing else
5  GET /v1/visits/{id}/synthesis
   → within seconds: state PENDING, degraded true, the structured record present
   → within the SLA: state READY, a narrative, and a prompt and model version on the run
6  stop the worker, open a second visit, and repeat step 4
   → the state stays PENDING and the screen shows the structured record with an explanation
   → it never shows an empty panel and never returns 404
7  GET /v1/ops/ai/synthesis-sla
   → `manual` should be zero: nobody pressed anything
```
