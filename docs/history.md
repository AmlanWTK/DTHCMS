# What the patient brings with them

CP53. Blueprint §3 station 4, §11.1. Decision: ADR-0028.

Station 4 is a conversation, not a form. The officer asks what brought them in, what else they
have, what runs in the family, what they have had done, what they are taking, what they have
been vaccinated against — and then, if the patient has been here before, works down last
month's answers asking whether each one is still true.

The whole checkpoint is about making that last sentence true in the record.

---

## Why this is not an observation

The plan says: _"Uses the CP42 observation tables; no new schema."_ That instinct is right and
the conclusion is wrong, and the reason is worth stating plainly.

|                        | An observation               | A history item             |
| ---------------------- | ---------------------------- | -------------------------- |
| What it is             | a point measurement          | a thing with a life        |
| Changes?               | never                        | resolved, amended, removed |
| Spans visits?          | no — it happened at a moment | that is the entire point   |
| "Confirm last month's" | meaningless                  | station 4's whole job      |

Two of the four acceptance criteria are statements about persistence:

> 3. Prior history is presented for confirmation at the next visit, never auto-accepted.
> 4. Every item is individually attributed.

Neither is a sentence one can say about a measurement. Forcing it in leaves a JSON blob (free
text wearing a schema — and criterion 1 is precisely that complaints are _not_ free text) or
one observation per item with the concept in `value_code`, which throws away the duration, the
severity, the onset and the relation. Those are the parts a doctor reads.

ADR-0028 carries the full argument, and amends §8.2's module list.

---

## Six kinds, and their rules are data

| Kind               | Catalogue | Needs          | May carry              |
| ------------------ | --------- | -------------- | ---------------------- |
| `COMPLAINT`        | DTHC      | a duration     | severity               |
| `COMORBIDITY`      | ICD-10    | —              | severity, onset        |
| `FAMILY_HISTORY`   | ICD-10    | **a relation** | onset                  |
| `SURGICAL_HISTORY` | DTHC      | —              | onset                  |
| `MEDICATION`       | DTHC      | —              | onset, dose, frequency |
| `VACCINATION`      | DTHC      | —              | onset                  |

The rules are rows in `core.history_kind`, returned by `GET /v1/history/kinds`, checked by a
trigger on the read model, and rendered from by both clients. Not one of those is redundant:
the API is so a station app does not carry a hardcoded switch on kind; the trigger is so a
projection rebuild and a hand-written `UPDATE` meet the same rule; the Go layer is so an
officer sees a sentence in their own language rather than a 500.

**`code_system` per kind matters more than it looks.** A complaint coded in ICD would make the
record assert that a patient _presented with_ type 2 diabetes — a claim nobody made. The
server refuses that combination; the kinds endpoint is how a screen avoids offering it.

---

## Criterion 3 is a safety property, not a convenience

> Prior history is presented for confirmation at the next visit, never auto-accepted.

The failure this prevents is specific. A system that rolled last month's history into this
month's would eventually assert, in a signed clinical document, that a patient is on a drug
they stopped in March — and nobody would be able to say who claimed it, because nobody did.

So there is **no carry-forward write**. The list comes back with the date each item was last
confirmed; confirming is `POST /v1/history/items/{id}/confirm`, one request, one event, one
actor. Twenty items carried forward is twenty of them.

Four things hold that line:

- `confirmed_at` and `confirmed_by` have **no column default**, and a standing invariant fails
  the deployment if one appears — because that is the shape the failure would actually take:
  not a bad row, but somebody adding `DEFAULT now()` to make a test pass.
- There is **no batch endpoint**, and both clients have a named test asserting that no helper
  in the feature confirms a list. A "confirm all" button produces one action from a person and
  twenty assertions in the record, wearing that person's name.
- An **amendment confirms as it changes**, because somebody just made a fresh assertion — and
  leaving `confirmed_at` behind would show an item edited a minute ago as one nobody has
  looked at since last month.
- A **resolved item is not asked about**. "Is this still true?" is a question about what the
  record currently asserts; a resolved item is the record already answering, and prompting for
  it every visit would ask a clinician to re-confirm the past for the rest of a patient's life.

---

## Uncoded is allowed, and counted

Criterion 1 says complaints and comorbidities are coded rather than free text. The catalogue
will not have a code for everything a history officer meets, and refusing those items would
push them into a note field where nothing can find them — which is the outcome the criterion
exists to prevent, reached by enforcing it too hard.

So an item may carry no coding as long as it says what was meant. It is visibly uncoded on
both screens, and every one of them is counted at `GET /v1/history/uncoded`, by kind.

**That count is the point.** If it grows, the catalogue is wrong rather than the officers, and
the count is the list of concepts somebody should add.

---

## Criterion 2 is half-finished, on purpose

> Current medications link to formulary products where they exist.

The formulary is a later checkpoint. There is nothing to link to, so `formulary_product_id` is
null on every row today — and that is the honest reading of "where they exist".

What exists now is the **state**, on every medication from the moment it is written down:

|                | Means                                              |
| -------------- | -------------------------------------------------- |
| `UNRECONCILED` | nobody has looked                                  |
| `MATCHED`      | it is this product                                 |
| `NOT_STOCKED`  | somebody looked, and this clinic does not carry it |

`NOT_STOCKED` is a finding, not a failure: it is worth knowing before a prescription is
written. And the state is null — not `UNRECONCILED` — on everything that is not a drug, because
a column that says "unreconciled" on a vaccination makes "what has nobody checked" return the
wrong answer, and the person who writes that query will not notice, since the number will look
plausible.

Note that a **drug** is a terminology concept ("Metformin") and a **product** is stock
("Metformin 500 mg tablet, this brand, this price"). Conflating them means a patient's history
stops being recordable the day the clinic runs out.

---

## Smoking and alcohol are not here

The plan asks that they be "carried from the lifestyle station without duplicate entry", and
the honest way to carry something is not to copy it. They are observation codes owned by
station 6; station 4 displays them from `/v1/patients/{id}/observations` and never asks again.
A second copy would be two answers to one question with no way to tell which is current.

`GET /v1/history/kinds` returns `from_lifestyle_station` naming them, which is what stops a
history screen growing its own smoking field. The list is short today — smoking status and
alcohol use arrive as observation codes with the lifestyle assessment checkpoint.

---

## Resolving is not removing

|                                | Means                           | Takes        |
| ------------------------------ | ------------------------------- | ------------ |
| `PATCH { status: 'RESOLVED' }` | she had this and no longer does | nothing      |
| `POST /remove`                 | this was never true             | **a reason** |

A single delete collapses the two, and only one of them is a correction. Nothing is deleted
either way: a removed item stays readable by id, with the reason, because an item somebody
removed is an item somebody disagreed with — and what they disagreed with is the interesting
part.

---

## What is still open

- **The formulary link.** Criterion 2's other half, arriving with the formulary checkpoint.
- **Smoking status and alcohol use** as observation codes, at the lifestyle station.
- **The seeded vocabularies** — thirty-six operations, medicines and vaccinations, plus their
  synonyms — are a proposal. So are the Bengali labels.
- **The manual verification the plan asks for**: capture a complex patient's history, come back
  at the next visit, and confirm that the prior history is presented for confirmation rather
  than re-entry. No test stands in for that.
