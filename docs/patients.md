# The patient record

**CP28.** The person the clinic knows: the schema, the two identifiers, the validated date
of birth, and the boundary between a patient and a research subject.

The decisions behind all of it are in
[ADR-0020](adr/0020-patient-identity-and-the-research-boundary.md). This page is what a
developer needs in order to use the module correctly.

---

## 1. What a registration writes

One transaction, five things:

| Where                            | What                                                          |
| -------------------------------- | ------------------------------------------------------------- |
| `core.clinical_id_counter`       | the facility-year counter, advanced under a row lock          |
| `core.patient`                   | the record                                                    |
| `core.patient_identifier`        | one row per official number: a digest, a sealed value, a mask |
| `research.research_subject`      | the anonymised cohort row                                     |
| `identity_link.research_subject` | `patient_id ↔ research_id`, write-only to the application     |

`patient.Store.Create` does all five and takes an `InTx` callback that runs inside the same
transaction, before the commit. That is where CP29 appends `PATIENT_REGISTERED`: a patient
row with no event behind it is a fact with no history, and an event with no patient is worse.

If anything in that list fails, none of it happened — including the clinical id, which rolls
back with the counter. The series stays gapless.

---

## 2. The two identifiers

```
clinical_id   DTHC-FRD-2026-000137     spoken at a desk, printed on a card
research_id   RS-7K2MQ9XW4B6NPRTVYZ3H5  opaque, random, carries no ordering
```

The clinical id is gapless within a facility and clinic year (Asia/Dhaka, not UTC — a
registration at 00:30 on 1 January belongs to the new year). It is unique across the whole
deployment, not merely the facility, so a card carried between two DTHC clinics cannot mean
two people.

The research id comes from `crypto/rand` and from nothing else. **Do not** derive it, hash it
from anything, or sort by it expecting registration order. `patient.NewResearchID()` takes no
argument, which is deliberate: there is nothing about the patient it could be derived from.

---

## 3. The date of birth

Three fields, and all three matter:

| Field             | Meaning                                                                        |
| ----------------- | ------------------------------------------------------------------------------ |
| `birth_date`      | the date, `NOT NULL`, no future dates, nothing implying an age over 130        |
| `dob_precision`   | `day` \| `month` \| `year` — below `day` the rest of the date is a placeholder |
| `dob_verified_by` | what established it, from a birth certificate down to an estimate              |

A patient who knows only their birth year is recorded as **1 January of that year with
`precision = year`**. Not refused, and not given an invented day.

`BirthDate.Age(on)` is whole years by the clinic's calendar. Anything that renders an age or
a percentile from a record whose precision is not `day` **must** say so on the screen: a
percentile computed from a year-precision date can be a year out in either direction, and a
number that looks like a measurement but is not one is the failure [R-06] is about.

The domain also refuses a document source with less than day precision — "birth certificate,
year only" is almost always a transcription error, and catching it at the desk is much
cheaper than finding it in a growth chart two years later.

---

## 4. Official numbers

Never store, log, span-tag or event-write a national ID. What exists is:

```go
sealer, _ := patient.NewIdentifierSealer(pepper, ring)   // pepper ≥ 32 bytes, CP12 key ring
id, _ := sealer.Seal(patient.NationalID, "1990 1234 5678")
//  id.Digest  HMAC-SHA-256, kind mixed in — the duplicate key
//  id.Sealed  secretbox, kind as AAD — openable by the service only
//  id.Masked  "**** **** 5678" — what a screen shows
```

- **Matching** uses `sealer.Digest(kind, raw)` and `Store.ByIdentifier`. Normalisation strips
  everything that is presentation, so `1990-1234-5678` and `1990 1234 5678` are one number.
- **Displaying** uses `Masked`. Nothing else.
- **Revealing** uses `sealer.Open(id)`, behind a step-up, with an audit entry the caller
  writes. There is exactly one screen that may do this.

`core.assert_no_plaintext_identifiers()` runs at every start and refuses a `sealed` column
that reads like a number.

---

## 5. The research boundary

```
dthcms_research  →  research.research_subject          SELECT
                 →  everything else                    no USAGE at all

dthcms_app       →  research.research_subject          SELECT, INSERT
                 →  identity_link.research_subject     INSERT only
```

The application cannot read the link. This is not a convention that a future handler could
forget — it is an absent privilege, checked at every start by
`core.assert_research_link_sealed()` and `core.assert_research_isolated()`.

The anonymised row carries a facility code, an enrolment **month**, a birth **year**, a sex,
and the six cohorting variables. It carries no name, no identifier, no address and no exact
date, because a registration date to the day plus a birth year plus a sex narrows a small
population further than any cohort analysis needs.

---

## 6. The socio-economic baseline

Six fields, confirmed with Dr Nahid, with **closed** value lists defined once in the event
schema (`eventstore.Patient*`) and taken from there by the domain, the API enum and — as
`CHECK` constraints — the database.

| Field                 | Values                                                                                                                                          |
| --------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| `education_level`     | none, primary, secondary, higher_secondary, graduate, postgraduate, madrasa, unknown                                                            |
| `occupation_category` | agriculture, day_labour, factory_worker, service_private, service_government, business, homemaker, student, retired, unemployed, other, unknown |
| `income_band`         | under_10k, 10k_25k, 25k_50k, 50k_100k, over_100k, unknown (monthly household BDT)                                                               |
| `household_size`      | 1–40                                                                                                                                            |
| `residence_type`      | urban, semi_urban, rural, unknown                                                                                                               |
| `medicine_payer`      | self, family, employer, ngo, government, unknown                                                                                                |

**Absent and `unknown` are different answers.** Absent means the desk did not ask — which the
required set permits, so a queue keeps moving. `unknown` means it was asked and the patient
does not know, which is itself a finding: a household that cannot state its income is a
different cohort from one that was never asked.

Adding a category is a migration. That is the intended cost: a research variable whose list
can be edited from the application is a variable whose cohorts stop being comparable between
one paper and the next.

---

## 7. The required set

Required — the clinical minimum:

- `name_en`
- `sex`
- `birth_date`, with its precision and source
- `phone_primary`, a Bangladeshi mobile
- `consent_reference` (§15.1)

Everything else is prompted and skippable. A registration desk must be able to finish a
record while the patient walks to the next station.

Phone numbers are normalised to `+8801XXXXXXXXX` before they reach a column, and held to it
by a `CHECK`. `01712345678`, `+8801712345678`, `8801712345678` and `1712345678` are one
number; a number typed in Bengali numerals is refused rather than silently mangled. The
second number accepts a landline or an overseas number, because a patient reachable only on
a relative's landline still has to be registrable.

---

## 8. Validation, and why it happens three times

`Registration.Validate(now)` returns **every** problem, not the first, each naming a field
and carrying both languages. A desk that fixes one field, resubmits and is told about the
next one is a desk that holds a queue four times over.

The same rules exist in the event payload (`eventstore.PatientRegistered.Validate`) and in
the database. That is not redundancy — each is a floor under the one above:

| Layer     | Catches                                                        |
| --------- | -------------------------------------------------------------- |
| Go domain | a registration officer's typo, with a message they can act on  |
| Payload   | a future handler writing an unqualified date into a ledger     |
| Database  | a bulk import or a migration that never went through Go at all |

---

## 9. The event

`PATIENT_REGISTERED` carries the complete demographics, flat, so the projection is a copy
rather than a mapping. It carries the identifier **kinds** and not the numbers, and it
carries no research id. Both absences are load-bearing; ADR-0020 §5 says why.

---

## 10. The API

`POST /v1/patients` — needs `patient.write.demographics`. `GET /v1/patients/{id}` — needs
`patient.read.demographics`, scoped to the caller's facility.

The plan names a `patient.create` permission. There is no such code in the catalogue, and at
this clinic's size registering and correcting demographics are one authority held by one
desk. Splitting them is a catalogue change and a decision for the clinical lead; until then
the existing permission is used.

**`event_id` is the client's.** It is generated by the caller, is UUIDv7, and is the
idempotency key. A first registration returns `201`; the same `event_id` again returns `200`
with the original patient and `duplicate: true`, having written nothing — not even a clinical
id. The check is the ledger's own uniqueness on `event_id`, so the answer is the same a week
later as a second later, and no second table has to be kept in step with it.

**Validation returns every failing field at once**, each with a message in `fields` and the
same message in Bangla in `fields_bn`. An interface should prefer `fields_bn` when the locale
is Bangla and fall back to `fields` — an English sentence is better than no sentence.

**A clinical write needs an enrolled device** (D-46): the envelope's `device_id` is evidence.
A browser session has none until D-71 settles browser device identity, so a registration from
the web today is refused with `DEVICE_REQUIRED` rather than attributed to no device. That is
the correct behaviour and it is also a blocker for CP32; it is recorded in the open-decision
register.

**Nothing sensitive travels.** The event carries the identifier _kinds_ and not the numbers,
and no research id. The response carries masks. Neither ever appears in a log.

---

## 11. Duplicates (CP30)

`POST /v1/patients/check-duplicates` takes a registration body and answers before the record
exists. Two passes:

**Deterministic** — an identity number already on file, or the same mobile _and_ the same
exact date of birth. `verdict: blocked`; registration is refused and the record is shown.
There is no threshold to tune and no judgement to make.

**Probabilistic** — a name spelled differently, a date of birth a year out, a phone with a
transposed digit. `verdict: review` with a scored list. **Registration is not refused.** A
register in Bangladesh legitimately contains many people named Md Rahim born in 1980; a
matcher aggressive enough to catch every duplicate here would spend a busy morning telling an
officer that two different people are the same, after which the warnings stop being read.

### The phonetic key

`internal/platform/textmatch` folds the transliteration habits that actually occur:

| Rule           | Effect                                                          |
| -------------- | --------------------------------------------------------------- |
| aspirates      | Faruk = Farukh, Fatema = Fathima                                |
| `w` dropped    | Anwar = Anowar = Anoar                                          |
| `z → j`        | Zaman = Jaman                                                   |
| `v → b`        | Naveed = Nabid = Navid                                          |
| `q → k`        | Qadir = Kadir                                                   |
| vowels dropped | Mohammad = Muhammad = Mohammod (and `Md`, expanded to a symbol) |

Lossy in one direction on purpose: two spellings of one name must collide, and two different
names colliding is acceptable — that costs a candidate scoring then rejects, while a miss
costs a duplicate patient nobody notices for a year.

### Thresholds

`patient.DefaultThresholds` — block 0.95, review 0.62. **Proposed values.** The measurement
behind them is `TestPrecisionAndRecallOnTheLabelledSet`, which scores a hand-labelled set of
realistic cases and prints precision and recall. On that set: **recall 1.00, precision 0.60**.
The set is adversarial by construction — every negative is a hard one — so 0.60 is a worst
case, not the rate a desk will see. They are a struct field, not a constant, because they
will be re-tuned against real spellings during the pilot.

### Merging

`POST /v1/patients/{id}/merge` — `patient.merge` plus a `patient_merge` step-up. The id in
the path is the record that **stays**.

- **Never automatic.** A wrong merge is worse than a duplicate: a duplicate is two incomplete
  histories, a wrong merge is one history containing another person's clinical facts.
- **Never a delete.** The losing record keeps its row, its status becomes `merged`, and it
  points at the survivor. `read.surviving_patient(id)` follows the chain; every read that
  starts from an old card or an old report should go through it.
- **Always justified.** At least ten characters, plus the score, the decision and the
  candidate list that was on screen — so "why did we merge these two" is answerable after the
  matcher's weights have been retuned.

`core.assert_merges_are_redirects()` runs at every start and refuses a merged patient with no
survivor.

---

## 12. Search (CP31)

`GET /v1/patients?q=` takes a clinical id, a telephone number, or a name in either script.

**The route is decided in Go, from the shape of what was typed.** One query with four ORed
branches was the obvious design and it was wrong: looking up an exact clinical id cost 282 ms
at fifty thousand patients, because the row was found by an index and the query then paid for
three trigram scans that could not possibly match. Routing takes that to 1 ms. A handle that
finds nothing falls through to the name query, because somebody may type a name that looks
like a number and a search that returns nothing when it could have returned something is the
failure staff remember.

| Route       | Recognised as                                  | Index                                        |
| ----------- | ---------------------------------------------- | -------------------------------------------- |
| Clinical id | `DTHC-FRD-2026-000137`, or `000137` off a card | unique index; expression index on the serial |
| Telephone   | anything that normalises to `+8801XXXXXXXXX`   | b-tree on `phone_primary`                    |
| Name        | everything else                                | trigram GINs on both names and the key       |

Names are matched forgivingly and **across romanisation**: searching "Muhammad Raheem" finds
a patient registered as "Mohammad Rahim", because the name route reuses CP30's phonetic key.
The query is told which scripts the term uses — comparing a Latin term against the Bangla
column can never score above zero, and skipping it took the name route's p95 from 288 ms to
171 ms.

**Measured**: 50,000 patients, 96 searches, p50 108 ms, p95 171 ms, against a 300 ms budget
(`TestSearchIsFastEnoughOnAFullRegister`). The fixture's names are _more_ clustered than a
real register — ten given names and ten surnames across fifty thousand people — so this is a
pessimistic figure.

`GET /v1/patients/today` is who was registered on the clinic's current day, Asia/Dhaka. Its
own endpoint and its own index because it is the most frequent read in the building.

`GET /v1/patients/{id}/summary` is the header card, and it **follows the merge chain**: an id
from an old card or an old report returns the record that is live now.

### What the audit trail records

`patient.searched` carries **how the search was framed and how many rows came back — never
the term.** The term is a patient's name, and a name in the audit trail is PHI in a table read
by administrators who may hold no clinical permission at all. It is also not what a review
needs: fifty name searches in a minute by one operator is what exfiltration looks like from
the inside, and the term adds nothing to that picture.

`patient.viewed` records a record being opened. `/today` is deliberately **not** audited — it
is the screen every station leaves open all day, and one line per refresh would bury the
searches that matter.

---

## 13. The photograph (CP34)

A photograph is the only identity check that works at a desk where half the patients share a
name and a third have no document. It is also the most sensitive single field in the record:
a face is identifying in a way a name in Bengali script is not.

**The bytes never pass through the API.** The client asks for a pre-signed `PUT`, uploads
straight to object storage, and then tells the API the object is there. The API then **reads
the object back** and takes the size and the SHA-256 from what it read, not from what the
client claimed. A photograph that never enters the API process cannot end up in a request
log, a crash dump, a proxy buffer or a trace — which is where PHI images turn up in incident
reports.

| Step | Call                                  | What it does                                               |
| ---- | ------------------------------------- | ---------------------------------------------------------- |
| 1    | `POST /v1/patients/{id}/photo/upload` | Mints a pre-signed `PUT`, valid ≤ 15 minutes, one key      |
| 2    | `PUT` to storage                      | The client uploads; the API never sees the bytes           |
| 3    | `POST /v1/patients/{id}/photo`        | The API reads the object, records size and digest, appends |
| 4    | `GET /v1/patients/{id}/photo`         | A fresh signed read URL, ≤ 15 minutes, minted per request  |

**No object is ever public.** `MaxSignedTTL` is fifteen minutes and the signer refuses to
issue anything longer. A test proves that an unsigned URL is refused, an expired one is
refused, and a URL minted to read one object cannot be edited to read another or replayed as
a write.

**The database holds a reference, never bytes.** `core.assert_photos_are_referenced_not_stored()`
fails the invariant suite if a `bytea` column ever appears on a photograph table. Backups,
replicas and `pg_dump` files then do not contain faces.

**A replacement is a new object and a new event.** Nothing is overwritten, so a chart printed
last month showing a different face is explicable rather than mysterious. The partial unique
index `patient_photo_current` allows exactly one current photograph per patient while keeping
every superseded one.

The signing is SigV4 written on the standard library (`internal/platform/blobstore`), because
this environment has no module proxy and the choice was between an SDK that cannot be fetched
and two hundred lines of a well-specified algorithm. It is verified against a test server that
recomputes the signature the way MinIO does, so "the signature is correct" is proven rather
than assumed.

_Deferred:_ `expo-camera` could not be installed (no registry access), so native capture on
the station tablet is device work. The web path uses `capture="user"`, which opens the camera
on a phone browser and a file picker at a desk.

---

## 14. Corrections (CP35)

A registration desk mistypes. The question is not how to stop it — it is what the record looks
like afterwards.

**A correction is never an overwrite.** `PATCH /v1/patients/{id}` appends a
`PATIENT_DEMOGRAPHICS_CORRECTED` event carrying, for each field, what the value **was**, what
it **is now**, and **why**. The original value stays in the ledger forever; the read model
moves.

### Only what was sent

Every field on the request is a pointer, and `nil` means _not touching this_. A form that
renders six fields and posts all six would otherwise rewrite five of them with whatever it had
loaded, which is how a stale tab silently reverts a colleague's correction. The diff is
computed against the record as it is at that moment, inside the transaction.

A correction that changes nothing is **refused**, not accepted as a no-op — including a
telephone number retyped in a different format, which normalises to no change. A no-op in the
history looks like something happened, and somebody will spend an afternoon on it. So is a
correction with no usable reason: `ErrReasonRequired`.

`core.assert_corrections_are_explained()` holds both rules in the database as well, so a
correction row with an empty reason or an unchanged value cannot exist however it was written.

### High-impact fields need a step-up

| Field                                           | High impact | Why                                                               |
| ----------------------------------------------- | ----------- | ----------------------------------------------------------------- |
| `birth_date`, `dob_precision`, `sex`, `name_en` | yes         | Every percentile, reference range and cohort row already computed |
| phone, address, `name_bn`                       | no          | Nothing derived was computed from them                            |

The plan says a date-of-birth correction needs "elevated permission". The catalogue has no
code for it, and inventing one would mean deciding, per role, who may correct a name but not a
birth date. A **step-up** (`patient_correct_identity`) is elevation of the kind this system
already has, and it is the right kind here: the risk is a session left open on a desk, not a
person who should never have had the capability. Because whether one is needed depends on
_what changed_, the check is in the handler rather than on the route.

### What a correction invalidates

`ops.derived_dependency` is an explicit register of which derived values depend on which
fields, with an action of `recompute` or `review`:

| Derived                     | Depends on                                          | Action    |
| --------------------------- | --------------------------------------------------- | --------- |
| `read.patient`              | `birth_date`, `name_en`, `name_bn`, `phone_primary` | recompute |
| `research.research_subject` | `birth_date`, `sex`                                 | recompute |

The recomputations happen in the same transaction — the read model, the phonetic search key
and the anonymised cohort row all move together, so a corrected name is findable by the new
spelling immediately. The register exists so that the next derived value somebody adds is
_declared_ rather than discovered later by a patient whose percentile never updated. The
response carries what was invalidated, so a screen can say so.

### Reading the history

`GET /v1/patients/{id}/history` returns one row per changed field: field, previous, current,
reason, whether it was high-impact, who made it and when. A retried correction (the same
`event_id`) produces one event and one history row, not two.

### The screen

`/patients/{id}/edit` is a **diff, not a form**. Changed fields are marked, the previous
value is printed under each one, and the line above the button says how many fields will
change rather than the button merely greying out.

The date of birth is **CP32's three-field control, not a date input**. A native picker
renders `06/14/1985` in one browser locale and `14/06/1985` in another, and the field a
correction most often exists to fix is the last one that should be ambiguous about which
number is the month. The age echo comes with it — a year transposed to 1958 reads as "68
years, 2 months old" beside a patient who is plainly forty, which is the check that catches
it [R-06].

The authenticator code is asked for **before** the request is sent, not after a `403`. The
server still decides; asking first only means that on a tablet a refused correction does not
throw away the typing.

The history is on the same screen as the form. The most common reason a field looks wrong is
that somebody already corrected it and the operator is about to change it back.

---

## 15. Consent (CP36)

Consent has its own document — [consent.md](./consent.md) — because it is a module rather than
a field: five types, versioned bilingual wording, and enforcement at every boundary that acts
on a patient.

Two things belong here, beside the patient.

`consent_reference` on the registration record is the **paper** reference, and predates the
consent module. It is what the desk writes on the form; a recorded CARE consent under CP36 is
the structured version of the same fact, and the two are linked by the form number when a
consent is captured with `capture_method: paper_form`.

**Research is opt-in.** A registered patient has an anonymised row in `research.research_subject`
from the moment of registration — the row is what the research id and the identity link point at
— but `research_consent` is `false` until a RESEARCH consent is granted, and `research.cohort`,
the only thing the research role may read, filters on it. So a freshly registered patient is in
the register and not in the cohort, which is the correct default and a visible change from CP28.
