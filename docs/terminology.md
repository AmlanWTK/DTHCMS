# The coded vocabulary, and the licence question underneath it

CP52. Blueprint §8, §9.1, decision D-24.

Every diagnosis and every complaint this clinic records has to become a **code**, or none of
the things that make the record worth keeping are possible: the research extract, the FHIR
mapping, the "how many people did we treat for PCOS last year" question, the referral letter a
hospital's system can read. Free text answers none of them.

---

## What D-24 leaves open, and what it does not

| Question                             | Status                       | What this checkpoint did about it                  |
| ------------------------------------ | ---------------------------- | -------------------------------------------------- |
| May we embed **SNOMED CT**?          | **open — D-24**              | registered as unusable; content refused by a rule  |
| **ICD-10 or ICD-11** as the backbone | open, and it does not matter | both are rows; a coding carries which one it used  |
| May we embed **ICD**?                | not open — WHO permits it    | ICD-10 2019 is loaded, ICD-11 registered and empty |

SNOMED CT requires an Affiliate licence, and whether Bangladesh confers free use has to be
verified with SNOMED International. Nothing in this repository is SNOMED-derived.

That is a claim worth exactly as much as the mechanism that enforces it, so there is one:

```sql
core.assert_no_unlicensed_terminology_is_embedded()
```

It runs after every migration and in the unit suite. Load a single SNOMED concept and the
deployment fails, in every environment, rather than in the one where somebody thought to look.
`core.terminology_map` exists so the day D-24 answers, mappings are rows and nothing above them
changes.

---

## Why a coding carries its version

ICD-10's `E11.9` and ICD-11's `5A11` are not the same concept. A code with no system and no
version is a string, and a diagnosis recorded in 2026 has to still mean in 2032 what the person
who recorded it meant — after the clinic has moved to ICD-11 and the WHO has revised the block.

So **criterion 2 is a shape, not a habit**: `{system, version, code}` travels together,
everywhere.

The API is built so a careless client cannot break it. A caller may omit `version`; it is
answered with the resolved one **in the response body**, beside the results. The picker
therefore has both halves in hand at the moment the clinician chooses, rather than the
recording code guessing later.

The one thing the server will not do is silently substitute. A caller asking for a version this
deployment has not loaded gets `422`, not the default — because a coding stamped with a version
nobody searched is a lie that is discovered years later, by somebody trying to read it back.

---

## The ranking, in four tiers

One SQL statement. Not a query plus a Go sort: a ranking split across two languages is one
nobody can reason about or reproduce.

| Tier  | Matches                                 | Because                                              |
| ----- | --------------------------------------- | ---------------------------------------------------- |
| **1** | the code itself, typed                  | doctors who know the code type the code              |
| **2** | a **clinic favourite**, on a word start | this clinic diagnoses E11.9 forty times a day        |
| **3** | any title or synonym, on a word start   | the rest of the classification                       |
| **4** | trigram similarity ≥ 0.25               | `diabetis` is what a phone keyboard produces at 11am |

Two details carry most of the weight.

**Word starts, not string prefixes.** A clinician typing `dia` means diabetes. A plain prefix
match answers "Diabetic polyneuropathy" before "Type 2 diabetes mellitus", which is the
diagnosis this clinic makes more often than any other.

**The tier comes back with the row.** "Why is that third" is the question every search gets
asked, and a ranking nobody can inspect is a ranking nobody can tune. The web and mobile pickers
both render it as a quiet reason beside the result.

The floor under tier 4 is 0.25. Without it, every keystroke returns the whole catalogue in an
arbitrary order, the list flickers, and the ranking is noise.

---

## Criterion 1 is met by knowing which twenty

> the 20 most common DTHC diagnoses are each findable within 3 keystrokes

This is not reached by a cleverer search. It is reached by **ranking the twenty** and by giving
each of them the words people actually type — including the Bengali ones.

`TestEveryFavouriteIsFoundWithinThreeKeystrokes` reads the criterion literally: for every ranked
concept it takes three characters from the concept's own display or one of its own synonyms —
which is exactly what somebody reaching for it would start typing — and fails if none of them
brings it back. Adding a favourite without giving it the words for it fails that test.

**The ranking itself is Dr. Nahid's.** The seeded twenty are a proposal drawn from what an
endocrine clinic sees; the mechanism does not care which twenty they are.

---

## Bengali is not a nice-to-have here

A picker that works in English and returns nothing for থাইরয়েড is a picker half this clinic
stops using inside a week — and the thing they fall back to is free text, which is the failure
this checkpoint exists to prevent.

So: 163 synonyms across both languages, trigram indexes on both displays, and two standing
rules. `assert_favourites_are_bilingual` refuses a ranked diagnosis with an empty Bengali
display — a favourite is a button on a screen that renders in two languages, and one with no
Bengali is a blank button for half the staff.

**The Bengali labels are still mine, not a clinician's.** They are on the open-questions list.

---

## The surface

| Endpoint                         | Answers                                            |
| -------------------------------- | -------------------------------------------------- |
| `GET /v1/terminology/systems`    | what exists, who publishes it, and what we may use |
| `GET /v1/terminology/search`     | the ranking, capped at 25                          |
| `GET /v1/terminology/favourites` | the clinic's list, in rank order                   |
| `GET /v1/terminology/concept`    | one coding read back, plus its mappings            |

One permission, `terminology.read`, and it is **not** `diagnosis.read`. There is no patient in
these tables. Guarding the picker with the clinical permission would mean a history officer who
is allowed to type a complaint needs the permission to read somebody's diagnoses — the exact
over-grant §4.4 exists to stop. It is granted to the history officer, the clinical assistant,
both grades of doctor and QA; not to the wall board, the research account or the field worker.

**Nothing here is audited.** There is no subject to record, and the audit trail is not a
keystroke log.

An empty query is the favourites — not everything, and not an error. A picker opens before
anybody has typed, and what it should show then is the clinic's own list. That is most of the
times it is opened.

---

## Criterion 4: p95 under 150 ms

Measured through the real handler against the real database, over the queries typing actually
produces — the growing prefixes of one word, in both scripts, plus codes and misspellings.

Observed: **p95 4 ms, median 4 ms, worst 5 ms** over 112 samples.

The number is not a performance target for its own sake. An autocomplete slower than a person
types is one they finish typing over, and the list that lands is the answer to a query they
have already moved past.

---

## What is still open

- **D-24** — SNOMED licensing, and ICD-10 versus ICD-11. Blocking nothing here; both are rows.
- **The twenty favourites and their ranks** are a proposal.
- **The Bengali clinical labels and synonyms** are mine.
- The picker is built and tested but **not yet on a station screen**. CP53 is its first
  consumer; wiring a diagnosis picker into an existing station now would put it at the wrong
  station.
