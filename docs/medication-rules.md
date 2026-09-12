# The rules that check a prescription, and who is allowed to write them

CP77. §6.3's eight kinds of medication safety rule, the authoring screen D-22 makes necessary,
the sandbox that lets a rule be tried before it is trusted, and the versions CP78's engine will
evaluate.

CP76 is in [`formulary.md`](formulary.md) §10 — the two-letter autocomplete the prescription
editor enters medicines through. This page is the rules.

---

## 1. A rule nobody has approved cannot fire

This is the whole design, and everything else follows from it.

**Forty rules ship in migration 00057.** I drafted every one from published guidance — ADA
Standards of Care 2025, KDIGO 2022, the FDA labels, the ATA thyroid guidelines, the BNF — and
cited the source on the row. **Not one of them has been read by Dr. Nahid**, and D-22 says the
physician authors every clinical rule. So a seeded rule has to behave differently from a rule he
wrote, and "differently" has to mean _inert_, not _marked_.

The mechanism is the one CP75 used for an unverified price, because it worked:

| Mechanism                                                      | What it stops                                                         |
| -------------------------------------------------------------- | --------------------------------------------------------------------- |
| A version is live only inside `[effective_from, effective_to)` | A rule with no period being returned by any "which rules apply" query |
| `rule_version_live_means_approved`                             | A period without an approver, in either direction                     |
| `rule_version_status_matches_its_period`                       | A DRAFT with a period, or a PUBLISHED row without one                 |
| `EXCLUDE USING gist` over `(rule_id, tstzrange)`               | Two versions of one rule live at one instant                          |
| `assert_no_unapproved_rule_is_live()` (invariant 113)          | A row that arrived some other way — a restore, a hand edit            |
| `Version.Approved()` in `Ruleset.Findings`                     | The same thing again, in Go                                           |

The last one looks redundant with the first five and is not. `TestAnUnapprovedRuleCannotFire`
builds a `Ruleset` by hand with an unapproved version in it and asserts nothing fires; deleting
that check turns the test red. Two locks on one door is right when what is behind it is a rule I
wrote silently stopping a prescription Dr. Nahid meant to write.

**Seeded rules are visibly unapproved.** The library draws "Not approved" in amber with an icon
and a word — never colour alone — the banner at the top of the screen counts them, and opening one
shows the citation it came from above the form.

## 2. Why a version has a period rather than a flag

Criterion 3 is that historical checks are reproducible. A schema in which the live version is
`WHERE is_current` answers "what would this prescription do today", and there is no query that
recovers what it did in March. A half-open period answers both, with one statement:

```sql
WHERE v.effective_from IS NOT NULL
  AND v.effective_from <= $at
  AND (v.effective_to IS NULL OR v.effective_to > $at)
```

That statement is `RulesetAt`, and it is the seam CP78 builds on. `at` is `now()` for a live check
and the instant of a past check when reproducing one; nothing else changes between the two calls.

`timestamptz` rather than `date`, unlike a price. A price is a commercial fact that holds for a
day; a rule change takes effect at the moment publish is pressed, and two prescriptions eleven
minutes apart can legitimately be checked against different versions.

A published version is frozen by a trigger as well as by the handler. `TestAPublishedVersionCannotBeEdited`
proves both — the API answers `409`, and a raw `UPDATE` is refused.

## 3. The condition model, and why it has no OR

A condition is **a subject and a list of tests joined by AND**. No OR, no NOT, no nesting.

```json
{
  "subject": { "match": "GENERIC", "generics": ["metformin hydrochloride"] },
  "when": [{ "kind": "EGFR", "operator": "LT", "value": 30, "unit": "mL/min/1.73m2" }]
}
```

Three reasons, in order of how much they matter.

1. **A physician can read it.** "When metformin is prescribed and the eGFR is below 30" is a
   sentence. A parse tree with an OR inside a NOT is not, and the thing that goes wrong with an
   unreadable rule is not that it is wrong — it is that nobody notices it is wrong.
2. **CP78 can evaluate it in a loop.** No recursion, no precedence, no short-circuit surprises.
   O(rules × predicates) with a small constant is what "sub-second" is made of.
3. **Three-valued logic stays simple.** With absent data in play every test answers holds / does
   not hold / **cannot tell**, and the combination over an AND is obvious. The same three values
   through an OR and a NOT is where fail-closed becomes fail-quietly.

The cost is real and is stated: "avoid in heart failure **or** liver disease" is two rules. Two
rules a physician can each read beats one he cannot.

### The eight kinds, and what each may test

| Kind                | May test               | Needs, and fails closed without it                     |
| ------------------- | ---------------------- | ------------------------------------------------------ |
| `INTERACTION`       | `CO_PRESCRIBED`        | the current medication list, when the rule asks for it |
| `CONTRAINDICATION`  | `DIAGNOSIS`, `ALLERGY` | the coded diagnosis list; a recorded allergy status    |
| `RENAL`             | `EGFR`                 | a recent eGFR                                          |
| `HEPATIC`           | `HEPATIC`              | an assessment of liver function                        |
| `PREGNANCY`         | `PREGNANCY`            | pregnancy status                                       |
| `PAEDIATRIC`        | `AGE`                  | the patient's age                                      |
| `DUPLICATE_THERAPY` | `DUPLICATE`            | —                                                      |
| `MAX_DOSE`          | `DAILY_DOSE`           | the total daily dose, in the rule's own unit           |

The table is `allowedPredicates` in Go, and the authoring form's field list is built from the same
table through `GET /vocabulary` — so the form cannot offer a test the validator will refuse.

`DUPLICATE_THERAPY` is the one kind whose subject may be `ANY`, because it is about the
prescription rather than about a drug.

**A max-dose rule will not convert units.** A rule in mg meeting a dose in mcg answers "cannot
verify". A thousandfold error is what the conversion would eventually produce.

## 4. What CP78 inherits, exactly

CP77 ships the **per-rule** semantics, because the sandbox cannot exist without them: "see the
result before publishing" is an acceptance criterion, and a sandbox running a second
implementation would be showing the physician the behaviour of a program that never meets a
patient.

```go
// The seam.
func (s *Store) RulesetAt(ctx, facility uuid.UUID, at time.Time) (Ruleset, error)
func (v Version) Evaluate(rule Rule, c Context) Finding
func (rs Ruleset) Findings(c Context) []Finding
```

`Context` is the whole of what a rule may look at: age, pregnancy state, eGFR with its date,
hepatic grade, diagnoses, allergies, proposed drugs and current drugs. **There is no patient
identifier in it and there must not be.**

Nil versus empty is load-bearing throughout: `Diagnoses == nil` means nobody asked and fails
closed; `Diagnoses == []` means somebody asked and there were none. Same for allergies and current
medications; same for the `*float64` numbers, because a zero eGFR is anuric renal failure and not
"unknown".

**CP78 owns everything above one rule:** building a `Context` from a real patient (eGFR recency,
allergy expansion, current medications, coded diagnoses) · running a whole `Ruleset` over a draft
prescription · expanding an allergy through the cross-reactivity map · duplicate detection across
items · **coverage reporting — which proposed drugs no rule mentioned** · ordering findings ·
recording `SAFETY_CHECK_RUN` with the versions evaluated.

## 5. The sandbox runs on a patient somebody typed, not on a patient

Acceptance criterion 4. The test patient is four numbers and three lists on a form.

A physician testing a renal rule needs eGFR 29, eGFR 31 and no eGFR at all, and no patient in the
register is all three. It also keeps a clinical picture out of a module that has no business
holding one — and the sandbox is precisely where somebody would later have added a log line.
**Nothing in the request is logged, traced or counted**, and `TestTheSandboxLogsNothingAboutThePatient`
runs a picture through the handler with a captured logger and asserts that none of the age, the
eGFR, the diagnosis codes, the allergen or the drug name appears in it.

The result shows the verdict, the severity, the message in the reader's language, **the working**
— each test, its answer and why — and, above all of it, the sentence that the rule is not live and
that nothing on the screen is happening to any prescription.

## 6. Publishing: the physician, a confirmation, and a second factor

Three decisions stand between an author and a live rule. Press _Publish_; read the
plain-language sentence of what will happen and confirm; prove it is still you with a fresh
second factor (`medication_rule.publish`, five minutes, one session, one use).

The permission is not the guard on its own — an administrator can grant himself any role. What
the grant does is make the ordinary path not exist: **`medication.rule.write` and
`medication.rule.publish` are granted to `PHYSICIAN` and to nothing else**, which is D-22 stated
as a grant rather than as a convention.

Unsaved work blocks publishing; a rule the server might refuse does not. The distinction is who
can be right about it. Unsaved work would make the request _succeed_ and publish something other
than what is on screen, and no server can catch that. A validation problem would make it _fail_,
and the server is the authority on whether it fails.

### The audit entry carries the whole rule

Per the checkpoint, and the reason is a defect rather than a policy: a finding cites a rule code
and a version number, and months later — when a prescription written under it is questioned — the
rule's own table can answer only as long as nobody has published a v3 and nobody has withdrawn it.
Both are ordinary things to have happened.

So `medication_rule.published` carries the severity, both messages, the advice, the citation, the
condition document and the plain-language sentence, into the hash-chained trail where they cannot
be quietly edited.

## 7. The forty seeded rules

| Kind              | Count | Examples                                                                                    |
| ----------------- | ----- | ------------------------------------------------------------------------------------------- |
| Kidney function   | 8     | metformin below eGFR 30 (BLOCK) and 45 · sitagliptin dose bands · glibenclamide below 60    |
| Pregnancy         | 7     | ACE inhibitors and ARBs (BLOCK) · carbimazole · propylthiouracil · non-insulin agents       |
| Maximum dose      | 6     | metformin 2550 mg · glimepiride 8 mg · pioglitazone 45 mg · atorvastatin 80 mg              |
| Drug interaction  | 5     | ACE + ARB · sulphonylurea + insulin · statin + fibrate · levothyroxine + calcium            |
| Age               | 4     | pioglitazone, SGLT2, sulphonylurea and pregabalin under 18                                  |
| Contraindication  | 4     | pioglitazone in heart failure (BLOCK) · GLP-1 with MTC/MEN2 (BLOCK) · metformin in acidosis |
| Liver function    | 3     | pioglitazone and statins in severe impairment (BLOCK) · metformin                           |
| Duplicate therapy | 3     | the same molecule twice · two sulphonylureas · two statins                                  |

Every row cites its source. Five ICD-10 codes the contraindication rules need — `I50.9`, `K74.6`,
`E31.2`, `E87.2`, `N18.5` — are added by the same migration, because **a rule keyed to a code
nobody can record never fires and nothing says so**.

### Cross-reactivity

Six allergen groups and four directed cross-reactions, seeded unapproved and cited. The one worth
reading is `SULFONAMIDE_ANTIBIOTIC → SULFONAMIDE_NON_ANTIBIOTIC` at risk `NONE`: the
cross-reaction most prescribers assume between a co-trimoxazole rash and a sulphonylurea is not
supported by the evidence, and a map that simply omitted the row would leave the belief in place
and the patient without a useful drug.

## 8. Import and export

Export gives the whole library as a file with the plain-language form in both languages, for
reading away from a screen and arguing about with a colleague.

**Everything imported arrives as an unapproved draft, whatever the file says.** A file can be
edited by anybody and mailed by anybody; an import that could set an approval would be a way to
publish a clinical rule without a second factor. The report names the fields it ignored, because a
physician who exported an approved rule and imported it back would otherwise reasonably expect it
to still be approved.

Dry run by default. Acceptance is per rule; the persistence is one transaction. A rule identical
to the newest version already stored is reported `UNCHANGED` and adds nothing, so export → read →
change nothing → import does not leave forty new drafts to work through.

## 9. Acceptance criteria, and where each is proven

| Criterion                                             | Test                                                                                                                                       |
| ----------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| (1) A physician can author, test and publish unaided  | `TestAPhysicianCanAuthorTestAndPublishWithoutADeveloper` — the six steps, through the API                                                  |
| (2) Every rule has a bilingual message and a severity | `TestARuleNeedsBothLanguagesAndASeverityAndASource`; `TestEverySeededRuleIsUnapprovedCitesItsSourceAndReadsInBothLanguages` over all forty |
| …and the Bengali is Bengali                           | `TestEveryKindOfRuleCanBeSaidInBothLanguages` — refuses an English conjunction inside it                                                   |
| (3) Versions retained, historical checks reproducible | `TestACheckAgainstVersionOneStillReproducesAfterVersionTwoIsPublished`                                                                     |
| …and a published version is frozen                    | `TestAPublishedVersionCannotBeEdited` (API **and** raw SQL)                                                                                |
| …and withdrawal keeps everything                      | `TestWithdrawingARuleKeepsEveryVersionAndItsPeriod`                                                                                        |
| (4) The sandbox shows the exact effect                | `TestTheSandboxShowsTheExactEffectBeforePublishing` — fires / does not fire / cannot verify / not applicable                               |
| **A seeded rule cannot fire**                         | `TestNoneOfTheFortySeededRulesCanFire`, `TestAnUnapprovedRuleCannotFire`, and invariant 113                                                |
| …and approving it is what changes that                | `TestApprovingASeededRuleIsWhatMakesItFire`                                                                                                |
| Publishing needs the physician and a step-up          | `TestPublishingNeedsAStepUpAndTheRightPermission`                                                                                          |
| The publication is audited with the whole rule        | `TestAPublicationIsAuditedWithTheWholeRule`                                                                                                |
| Nothing imported is approved                          | `TestImportedRulesArriveUnapprovedHoweverTheFileIsMarked`                                                                                  |
| No PHI in the logs                                    | `TestTheSandboxLogsNothingAboutThePatient`                                                                                                 |
| A browser with no device can reach all of it          | `TestTheLibraryIsReachableFromABrowserSessionWithNoDevice`; `dthclint readpath`                                                            |

## 10. Open, and whose decision it is

| Question                                                                                                                                                                                                                                                                           | Whose     |
| ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------- |
| **All forty seeded rules.** Drafted from published guidance by a developer. Every one inert until read and approved                                                                                                                                                                | Dr. Nahid |
| **All four cross-reactivity mappings**, and the sulfonamide row in particular. CP78 will not expand an allergy through an unapproved mapping, and says so as a "cannot verify" — so until these are read, a penicillin allergy produces a visible refusal rather than a silent one | Dr. Nahid |
| **The eight rules CP78 added** (migration 00060), and the six molecules beside them                                                                                                                                                                                                | Dr. Nahid |
| **The 33 scenarios in [`medication-safety-golden-suite.md`](medication-safety-golden-suite.md)**, which are what the engine is held to. Drafted by a developer; every one says why it is expected to fire, so a disagreement has something to attach to                            | Dr. Nahid |
| Whether the **pharmacist** should be able to read the rule library — a screen of contraindication rules is a screen of diagnosis codes, which §4.4 keeps off his screen; the renal dosing rules are exactly what would help him catch a bad prescription                           | Dr. Nahid |
| Whether `DUP-GENERIC` should be a WARN or a BLOCK. Split dosing of levothyroxine is legitimate and would trip it                                                                                                                                                                   | Dr. Nahid |
| The Bengali clinical register throughout — rule names, severities, the plain-language renderer's phrasing (D-24)                                                                                                                                                                   | Dr. Nahid |
| Whether "one row per brand, strength chosen second" is the right shape for CP76's autocomplete (see formulary.md §10)                                                                                                                                                              | Dr. Nahid |

### Known gaps, named rather than left to be discovered

**A fixed-dose combination is not modelled as its components.** ~~`core.generic` holds
"Sitagliptin + Metformin hydrochloride" as a single molecule name, so `DUP-GENERIC` will not catch
Siglimet prescribed alongside Comet — which is metformin twice, and one of the commonest real
prescribing errors in a diabetes clinic.~~ **Closed by CP78, migration 00058.**

`core.generic_component` holds the molecules, `core.generic.components_status` says whether
anybody has written them out, and `Drug.Components` carries them into the evaluator. The rule model
needed nothing new, exactly as predicted: `DUP-GENERIC` now compares molecule _sets_ instead of
generic names, so Siglimet beside Comet fires and the finding names the molecule — "Siglimet +
Comet both contain metformin" — which is a claim a physician can check against the box.

Three things about it are worth knowing here rather than in the migration:

- **Fourteen of the sixty-five generics are combinations**, seven of them containing metformin.
  All sixty-five were decomposed by hand and every row cites where it came from, because a
  regular expression over the names cannot be reviewed and gets "Cholecalciferol (Vitamin D3)"
  and "Insulin degludec + Insulin aspart (premixed)" wrong in opposite directions.
- **UNDETERMINED is the default and it fails closed.** A generic arriving from a CSV import or
  the admin screen has no molecules written, and a duplicate question about it answers _cannot
  verify_ — never _no duplicate_. An unwritten molecule set intersects nothing, and "intersects
  nothing" and "is not a duplicate" are the same value meaning two very different things.
  Invariant 116 refuses a row that claims DETERMINED with no molecules; the Go layer checks it
  again at the point of use.
- **Class-level duplicate detection still cannot see a combination**, and that is now a named
  gap rather than a surprise. Pioglitazone + Glimepiride carries its own class code, so
  `DUP-SULPHONYLUREA` does not match it; only the molecule-level rule does. The golden suite
  asserts that pair explicitly.

The related question `DUP-GENERIC` WARN-or-BLOCK, above, is unchanged and is still Dr. Nahid's.
Split dosing of levothyroxine would trip it, and so — now — would a premixed insulin beside a
rapid analogue, which is a legitimate regimen.

**Gestational age is not in the model.** `CBZ-PREG` warns for the whole of pregnancy and asks the
physician to check the trimester, because the clinically correct rule — carbimazole before 16
weeks, propylthiouracil after — cannot be expressed without it.

**Seven more rules and six more molecules arrived with CP78.** `PEN-ALLERGY`,
`CEPH-PEN-ALLERGY`, `DUP-RAAS`, `RAAS-CROSS-DUP`, `SGLT2-MYCOTIC`, `NSAID-CKD-RAAS`,
`NSAID-RENAL-60` and `MET-CKD-DIAGNOSIS`, in migration 00060, together with amoxicillin, three
NSAIDs and two diuretics in `core.generic`. They exist because four of the scenarios the plan
names for CP78's golden suite could not otherwise be written — two ACE inhibitors needs a
class-level duplicate rule, and penicillin allergy plus amoxicillin needs a molecule this
formulary did not hold. **Every one is seeded unapproved on CP77's own terms and is inert until
read.** They are the largest single piece of clinical content a developer has added to this
system and they belong at the top of the review list.

**`RulesetAt` does not filter by rule activity.** A withdrawn rule's version is closed at the
instant of withdrawal, so it drops out of every later ruleset; the `is_active` flag is carried for
the screens rather than used by the query. That is correct but worth knowing before somebody adds
a filter that would break reproducibility.

---

## 11. Renal dosing (CP79)

CP43 derives eGFR from a creatinine. CP77 wrote eight banded renal rules. CP78 built the engine
that runs them. What was missing between them is two facts, and neither is a rule.

### 11.1 How old an eGFR may be

The plan's sentence: **an eGFR from two years ago is not current renal function.** Before this
checkpoint, `MET-RENAL-30` blocked metformin on a creatinine from 2023 with exactly the confidence
it blocks one from this morning, and nothing anywhere said which it was looking at.

`core.facility_renal_policy` holds the window, **per facility**, default **six months**, with a
citation and a place for a physician's name. `GET /v1/patients/{id}/renal-status` returns it, and
`renal` on every safety-check result carries it too.

**Months, not days, and applied on the calendar.** Expiry is `egfr_as_of + N months`, not
`+ 30N days`. Six calendar months before 12 September is 12 March — 184 days — so a 180-day
implementation calls a result taken exactly six months ago stale while every physician saying
"within six months" means it is not. `TestTheStalenessBoundaryIsExact` pins all six cases: five
months, exactly six, six months plus one second, seven months, one day inside and one day outside.

**Exactly at the window is still current.** Stale means the instant of the check is _after_ the
expiry. Either convention is defensible; having none is not.

**A stale eGFR still runs the rules.** It is the best information there is, and refusing to use
it would leave the physician with nothing. What changes is that `RENAL-EGFR-STALE` fires beside
the banded finding, at WARN, naming the date and the age in days — so `MET-RENAL-45` and "the
number it fired on is fourteen months old" arrive together.

**An eGFR with no date against it is never treated as current.** The facts bridge cannot produce
one — it returns the value and the instant together or neither — but a caller assembling a picture
by hand can, and a value whose age is unknown must not be quietly treated as fresh.

### 11.2 The window is applied even though nobody has approved it

This is the **one deliberate departure** from §1's rule that seeded clinical content is inert
until a physician reads it, and it is worth being explicit about.

There is no inert value for a staleness window. A rule that does not fire does not fire; a window
that does not apply means **every eGFR is treated as current**, which is the failure the whole
checkpoint exists to prevent. The seed would have made the system less safe by being unapproved
rather than more.

So the window applies, `policy.approved` is `false` and travels with every renal status the API
returns, and the indicator says _provisional_ in both languages until somebody's name is on it.
The honest position is "we are using six months and nobody has agreed to it", not "we are using
nothing".

### 11.3 Which drugs cannot be prescribed without one

CP78 already fails closed when a _rule_ meets an absent eGFR. A drug with no renal rule written
about it produced silence, and §7.2's position is that silence is the one answer that must never
happen.

`core.generic_renal_dependence` records, per molecule, whether prescribing it depends on knowing
the kidney function. **Two values and an absence:**

| State               | Means                                                       |
| ------------------- | ----------------------------------------------------------- |
| `EGFR_REQUIRED`     | cannot be prescribed safely without a current eGFR          |
| `EGFR_NOT_REQUIRED` | prescribing it does not turn on the kidney function         |
| **no row**          | **nobody has classified it** — reported, never read as safe |

### 11.4 Why it is not called `is_renally_cleared`

The plan's words are _fail closed when creatinine is absent for a **renally-cleared** drug_.
Implemented literally, that flag is wrong in both directions for drugs this clinic prescribes
every day:

- **Empagliflozin is not renally cleared** — it is glucuronidated — and you still must not start
  it without an eGFR, because below 25 it no longer lowers glucose. A clearance flag would have
  let it through with no kidney function on file.
- **Linagliptin is renally cleared by almost nobody's definition** (it is biliary), and that is
  precisely why every seeded renal rule offers it as the alternative. A flag keyed to clearance
  would have fired on it and taught the physician to ignore the column.

So the column records the question the prescriber actually has to answer. **This is a widening of
the plan's scope and it is on Dr. Nahid's list.**

### 11.5 The classification, and the two molecules deliberately left out

**Sixty-three of the formulary's sixty-five molecules are classified** in migration 00061, each
with a bilingual reason and a citation, each seeded unapproved. Thirty-one `EGFR_REQUIRED`,
thirty-two `EGFR_NOT_REQUIRED`.

**Two are deliberately unclassified**, and that is the honest answer rather than a gap:

- **Voglibose** — the same class as acarbose, and the published renal data is thinner. Not
  confident enough to say either thing in a system that will act on the answer.
- **Calcium lactate gluconate + Calcium carbonate + Vitamin D3** — calcium supplementation in CKD
  is not a clearance question at all. It is CKD-MBD, where a calcium load can be actively harmful
  at low eGFR, and the decision belongs to a physician rather than to a dependence flag.

Both come back from the engine as _renal handling not classified_, which is what CP78's coverage
model does with a drug no rule mentions, and for the same reason.

### 11.6 The three findings CP79 adds

| Code                   | Severity | Outcome         | When                                                                 |
| ---------------------- | -------- | --------------- | -------------------------------------------------------------------- |
| `RENAL-EGFR-ABSENT`    | BLOCK    | `CANNOT_VERIFY` | a drug classified `EGFR_REQUIRED` and no eGFR on file. **Per drug.** |
| `RENAL-EGFR-STALE`     | WARN     | `FIRES`         | the eGFR used is older than the window. **Once, per prescription.**  |
| `RENAL-NOT-CLASSIFIED` | WARN     | `CANNOT_VERIFY` | no eGFR, and nobody has classified this molecule. Per drug.          |

The severity of the first is the one judgement here and it is the plan's: fail closed means the
prescription does not proceed as though it had been checked, and a warning among warnings is not
that. It is `CANNOT_VERIFY` rather than `FIRES` because nothing is known to be wrong — what is
known is that nobody can tell.

The staleness finding is raised **once**, whatever is on the sheet, including when nothing on it
needs an eGFR: a physician looking at a two-year-old result should be told before deciding what to
prescribe, not only after proposing something renally dosed. Twelve copies of it down the finding
list would bury the eleven other things he has to read.

The third fires **only when there is no eGFR**. With one on file the banded rules run and CP78's
coverage model already reports what was and was not checked; a second uncovered-style finding
there would be noise on every ordinary prescription.

### 11.7 CKD staging is a GFR category, not a diagnosis

`stage` is KDIGO 2024 table 1 and nothing else. A diagnosis of chronic kidney disease needs the
abnormality present for more than three months and takes albuminuria into account; one eGFR of 52
is a G3a **number** and says nothing about whether this patient has CKD. The labels are written
that way in both languages, because a staging display that read "stage 3 kidney disease" off a
single blood test would be a diagnosis the software invented.

### 11.8 Dialysis is out of scope, and that is a decision

**Nothing in CP79 models dialysis.** Not a field, not a band, not a half-built path to mistake for
one.

A patient on haemodialysis has an eGFR that is not a dosing input: the number describes residual
function, the dosing is driven by the dialysis schedule and by whether the drug is dialysed out,
and applying a band to it produces a confident wrong answer where the honest output is no answer.
The plan lists dialysis scope as an open decision. Until Dr. Nahid answers it, a dialysis patient's
renal status reads exactly as any other patient's, which is a limitation somebody should know
about rather than a behaviour anybody should rely on.

### 11.9 Open, for Dr. Nahid

1. **Six months.** Approve it, or change the number. Until somebody does, every renal status in
   the system is marked provisional.
2. **The sixty-three classifications.** Expect to disagree with some. `Acarbose` and the statins
   are the ones most likely to be argued about.
3. **Voglibose and the calcium combination**, unclassified on purpose.
4. **Dialysis.** In scope or not.
5. **`EGFR_REQUIRED` vs "renally cleared".** The plan said one thing and this implements a wider
   one. Confirm or reject.
