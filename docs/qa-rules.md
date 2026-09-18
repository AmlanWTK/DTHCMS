# The QA rule set (CP83)

**Status:** authored by Amlan under Dr Nahid's standing delegation, 13 Sep 2026. **Every rule
below is physician content and needs his review.** The plan names six rules and leaves "the full
QA rule set beyond the named ones" as an open clinical decision; this closes it.

**Resolves CP83's open decision.** Rules are reference data, editable without a code release —
acceptance criterion 5 — so everything here is a row Dr Nahid can change, retire or re-sever.

---

## 1. What this station is for, and what it must not become

Step 10 is the last point at which the clinic can notice that a file is incomplete **while the
patient is still in the building**. That is its whole value. Once the patient has walked out with
paper, a missing HbA1c is a missing HbA1c for three months.

Two failure modes, and they pull in opposite directions:

- **Too few rules** and the station is theatre — a person clicking "cleared" on a file nobody
  checked, which is worse than no station because it creates a record saying somebody did.
- **Too many rules** and the queue backs up on a busy Thursday, the officer starts overriding to
  clear the floor, and the overrides become the process. A rule that is routinely overridden has
  not made the clinic safer; it has taught the staff that clearance is a formality.

So the set below is deliberately small, and **every rule has to answer: if this is missing, would
I want the patient brought back before they leave?** If the honest answer is no, it is not a QA
rule — it is a report that somebody reviews next week.

## 2. Two severities, and the difference is the patient's feet

|           | Meaning                                                            | Effect                                                                          |
| --------- | ------------------------------------------------------------------ | ------------------------------------------------------------------------------- |
| **BLOCK** | The file is not safe to close                                      | Cannot clear. Bounces to a named station                                        |
| **WARN**  | The file is incomplete, and the consultant may still have a reason | Clears, but the officer must acknowledge it and the acknowledgement is recorded |

A BLOCK sends the patient back up the corridor. That cost is real — an elderly patient who
travelled from Boalmari does not want another forty minutes — so BLOCK is reserved for things
where the alternative is worse.

**Overrides are a consultant-level act with a reason, and the override rate is Quality's to watch,
not the prescriber's.** Same argument as the counselling gate: the answer to a rising override rate
is a person asking why, and that person should not be the one granting them.

## 3. The rules

Each names the station it bounces to, because "incomplete" without "whose" is a rule that stalls.

### 3.1 Safety — inherited from the engines that already exist

| #   | Rule                                                               | Severity  | Bounces to   |
| --- | ------------------------------------------------------------------ | --------- | ------------ |
| 1   | An unresolved interaction or duplicate from the CP78 safety engine | **BLOCK** | Consultation |
| 2   | Allergy status not asserted for this patient                       | **BLOCK** | History      |
| 3   | A prescribed drug the patient is recorded allergic to              | **BLOCK** | Consultation |

Rule 2 is already a hard stop at the queue (CP54). It is repeated here because a hard stop that
was overridden upstream must not pass silently downstream, and because the QA officer is the last
person who can catch it.

### 3.2 Diabetes — the file a diabetic should not leave with

| #   | Rule                                                                                                                 | Severity  | Bounces to             |
| --- | -------------------------------------------------------------------------------------------------------------------- | --------- | ---------------------- |
| 4   | Diabetic with **no HbA1c recorded or ordered** in the last 6 months                                                  | **BLOCK** | Consultation           |
| 5   | Diabetic with **no blood pressure recorded this visit**                                                              | **BLOCK** | Vitals                 |
| 6   | Prescribed metformin, SGLT2 inhibitor or a renally-dosed agent with **no eGFR inside the facility's recency window** | **BLOCK** | Consultation           |
| 7   | Diabetic with **no foot examination in 12 months**                                                                   | WARN      | Examination            |
| 8   | Diabetic with **no retinopathy screening recorded in 12 months**                                                     | WARN      | Consultation           |
| 9   | Type 2 diabetic with **no lipid profile in 12 months**                                                               | WARN      | Consultation           |
| 10  | **Insulin or a GLP-1 pen prescribed and no education station record this visit**                                     | **BLOCK** | Prescription education |

Rule 4 is the plan's named rule and the clinic's core measure. "Recorded **or ordered**" matters:
a consultant who has ordered the test has done the right thing, and blocking him for the lab's
turnaround would be blocking the wrong person.

Rule 5 is a BLOCK and I want to defend it, because it looks heavy for a missing observation.
Blood pressure is thirty seconds at a station the patient already walked past, and in a diabetic
it is the number most likely to be what actually kills them. A diabetes clinic that lets a file
close with no BP is a diabetes clinic treating one risk factor and not looking at the other.

Rule 6 is where CP79's renal dosing meets CP83. Metformin's dose is a function of eGFR; prescribing
it against an eGFR from two years ago is prescribing against a number that may no longer exist.
The window is the facility's, not a constant — CP79 already made that configurable.

Rule 10 is the link between stations 11 and 10, and it is the rule I would most expect to be
argued about. A first insulin pen handed over with nobody watching the patient use it is the
commonest avoidable treatment failure in this clinic's population. If it proves too heavy in
practice, the right relaxation is to restrict it to a **first** prescription of that device rather
than to downgrade it to a WARN.

### 3.3 Thyroid

| #   | Rule                                                                                          | Severity  | Bounces to   |
| --- | --------------------------------------------------------------------------------------------- | --------- | ------------ |
| 11  | Levothyroxine or an antithyroid drug prescribed with **no TSH in 6 months**                   | **BLOCK** | Consultation |
| 12  | **Carbimazole or propylthiouracil prescribed and the agranulocytosis warning not counselled** | **BLOCK** | Counselling  |
| 13  | Antithyroid drug started with **no baseline full blood count** recorded or ordered            | WARN      | Consultation |

Rule 12 is the one on this page I would argue hardest for. Agranulocytosis is rare, sudden, and
survivable **only if the patient knows that a sore throat and fever means stop the drug and get a
blood count today.** A patient who was never told has no way to act on a symptom they will
otherwise treat as flu. The counselling tick already exists (CP55–57); this rule says the
prescription does not leave without it.

### 3.4 Anything prescribed to a woman who could be pregnant

| #   | Rule                                                                                                         | Severity  | Bounces to |
| --- | ------------------------------------------------------------------------------------------------------------ | --------- | ---------- |
| 14  | A drug flagged teratogenic prescribed to a woman aged 15–49 with **no pregnancy status recorded this visit** | **BLOCK** | History    |

Carbimazole in the first trimester, ACE inhibitors and ARBs, statins, and several others this
clinic prescribes weekly. The teratogenic flag is a property of the molecule and belongs on the
CP77 rule table, not in this document — **which drugs carry it is Dr Nahid's list to write**, and
until he writes it this rule fires for nothing.

The age band is a proxy and a crude one. It will occasionally ask an irrelevant question, and that
is the correct direction to be wrong in.

### 3.5 Completeness — the file as a record

| #   | Rule                                                              | Severity  | Bounces to         |
| --- | ----------------------------------------------------------------- | --------- | ------------------ |
| 15  | No diagnosis coded on this visit                                  | **BLOCK** | Consultation       |
| 16  | Counselling checklist for this visit incomplete                   | **BLOCK** | Counselling        |
| 17  | A mandatory station's data missing for this visit's route         | WARN      | The station itself |
| 18  | Weight not recorded this visit and a weight-based dose prescribed | **BLOCK** | Anthropometry      |

Rule 15 is a BLOCK for a reason beyond tidiness: an uncoded visit is invisible to §12's research
and to every report the clinic will ever run on itself. A year of uncoded visits is a year that
cannot be analysed, and nobody notices until somebody asks a question of the data.

## 4. What is deliberately not a rule

- **Anything about whether the prescription is _right_.** QA checks that the file is complete, not
  that the consultant's judgement was correct. A station that second-guesses the prescription would
  need a clinician in it, and this clinic has one.
- **Cost, or whether the patient can afford it.** Real, important, and it belongs at the pharmacy
  counter and in the consultation — not in a gate that bounces the patient backwards.
- **Anything measured in adherence.** The education station records it; a patient is not blocked
  from leaving because they missed doses last week.

## 5. Configurability, and the thing that must not be configurable

Every rule above is a row: severity, the bounce station, the recency window, and whether it is
enabled at all. Dr Nahid changes them without a release.

**The one thing that is not a row is that clearance is required.** Printing without QA clearance
must be impossible server-side, whatever the rule table says — an empty rule table means every
prescription clears, not that clearance is skipped. Those two are different, and only one of them
is safe.

---

# Appendix · The content the inert rules were waiting for

Added 13 Sep 2026, after CP83 reported rules 12 and 14 firing for nothing. Both were inert because
of content I owed, not because of anything wrong with the engine. **Both lists below are mine and
need Dr Nahid's sign-off before the rules are switched on.**

## A1 · The agranulocytosis counselling item (unblocks rule 12)

A new item on the counselling checklist, shown when an antithyroid drug is on the sheet.

> **English:** "This medicine can rarely stop your body making the white cells that fight
> infection. If you get a sore throat, mouth ulcers or fever while taking it, **stop the medicine
> and get a blood count the same day.** Do not wait to see if it settles. Bring the result to us,
> or call the clinic."
>
> **Bangla:** "এই ওষুধে খুব কম ক্ষেত্রে শরীরে জীবাণুর বিরুদ্ধে লড়াই করা শ্বেত রক্তকণিকা তৈরি বন্ধ হয়ে যেতে
> পারে। ওষুধ খাওয়ার সময় গলাব্যথা, মুখে ঘা বা জ্বর হলে **ওষুধ বন্ধ করে সেদিনই রক্তের সিবিসি পরীক্ষা
> করান।** সেরে যায় কি না দেখার জন্য অপেক্ষা করবেন না। রিপোর্ট আমাদের দেখান বা ক্লিনিকে ফোন করুন."

Three things in that wording are load-bearing and should survive editing. **"Stop the medicine"**
comes before "get a blood count", because a patient who waits for the test while still taking the
drug is the patient who dies of this. **"Do not wait to see if it settles"** exists because every
instinct says a sore throat is flu. And it names the three symptoms rather than saying "signs of
infection", which is a phrase that means nothing to somebody who is not a clinician.

## A2 · Molecules that carry the teratogenic flag (unblocks rule 14)

The flag decides one thing: **whether the pregnancy question gets asked.** It does not refuse the
drug, and it must not be read as a contraindication list — several of these are the right drug for
the right woman, and the point is that somebody asked first.

| Molecule or class                         | Why it is on the list                                                                                                                                                                 |
| ----------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Carbimazole / methimazole**             | First-trimester embryopathy — aplasia cutis, choanal and oesophageal atresia. The single most important row here, and the reason propylthiouracil is preferred in the first trimester |
| **Propylthiouracil**                      | Preferred in the first trimester, but the answer still changes management, and it carries its own hepatotoxicity                                                                      |
| **ACE inhibitors** (class)                | Fetopathy — renal failure, oligohydramnios, skull hypoplasia                                                                                                                          |
| **Angiotensin receptor blockers** (class) | The same, and prescribed here as often                                                                                                                                                |
| **Statins** (class)                       | Guidance is still to stop before conception, though the evidence has softened                                                                                                         |
| **SGLT2 inhibitors** (class)              | Not classical teratogens; contraindicated in pregnancy and to be discontinued                                                                                                         |
| **GLP-1 receptor agonists** (class)       | Discontinue before conception — semaglutide needs about two months' washout, so the question has to be asked well before anyone is pregnant                                           |
| **Spironolactone**                        | Anti-androgen: feminisation of a male fetus. **This clinic prescribes it for PCOS**, to exactly the women rule 14 exists for                                                          |
| **Bisphosphonates** (class)               | Long skeletal half-life, so the exposure outlasts the prescription                                                                                                                    |
| **Testosterone**                          | Virilisation of a female fetus. Already on CP82's do-not-propose register for its own reasons                                                                                         |

**Deliberately not flagged, and each for a reason worth stating**, because a list like this tends to
grow by anxiety: **metformin** (used in pregnancy, not teratogenic), **insulin** (the safest option
in pregnancy and the thing patients are switched _to_), **levothyroxine** (essential, and the dose
goes _up_ in pregnancy — flagging it would teach the opposite of the right instinct), and
**cabergoline** (stopped at conception in prolactinoma, but that is a management decision rather
than a teratogenic one).

## A3 · What I would still add to the answer list

`PREGNANCY_STATUS` has six answers and they are good. The one I would consider adding is a reason
behind "not applicable" — hysterectomy or confirmed sterilisation is the commonest genuine reason a
woman in the 15–49 band is not at risk, and recording it means she is not asked again every visit.
That is a convenience rather than a safety matter, so it can wait.
