# The medicine formulary, and what it cost

CP75. The curated formulary of §10 (Option A, D-56), the price history §12.3's affordability
research rests on, and the monthly price review §16.1 asks the clinic to name an owner for.

Every prescription depends on this module: CP76's two-letter autocomplete, CP77's rules, CP78's
safety engine and CP80's prescriptions are all built on it.

---

## 1. A price is a row with a date range, never a column on a product

§12.3 needs **the price at the time of prescribing**, not today's price. A patient who stopped
taking semaglutide in March because the price rose in February is invisible to an analysis that
prices March at February's number — and that is the analysis this clinic wants to publish.

So `core.medication_price` carries `[effective_from, effective_to)`, half-open, on the clinic's
calendar. Recording a new price **closes** the old row's range and opens a new one. Nothing is
edited: `core.medication_price_is_immutable` refuses a change to the amount, the start date, the
verification state, the origin or the recorder, and refuses to reopen a period that has closed.

The answer to _what did this cost on 4 March 2024_ is therefore:

```sql
SELECT * FROM core.medication_price
 WHERE product_id = $1
   AND effective_from <= DATE '2024-03-04'
   AND (effective_to IS NULL OR effective_to > DATE '2024-03-04');
```

and an `EXCLUDE USING gist` constraint over `daterange(effective_from, effective_to, '[)')`
guarantees at most one row satisfies it. **That is why the query is not `LIMIT 1`.** The Go layer
asks for every matching row and refuses if there is more than one (`ErrAmbiguousPrice`), because a
second answer means the date filter is wrong — and quietly returning the first would price every
prescription at whichever row the planner happened to hand back.

`effective_from <= day` is the half that carries the acceptance criterion. The obvious query —
_the most recent price for this product_ — returns a price recorded last week for a prescription
written last year, and looks entirely correct while doing it.

**Backdating** into a gap is allowed; backdating over a period another price already covers is a
`409`, not a silent overwrite. A price somebody has already reported on does not change.

## 2. Money is integer poisha

`bigint`, in poisha (1 BDT = 100 poisha), the same shape as CP70's `cost_micro_usd`.

- A `float64` cannot hold 0.34, and a research extract summing a year of unit prices is then wrong
  by an amount nobody can predict and that looks like rounding until it is checked against a
  receipt.
- The value space is exactly the space of prices a counter in Faridpur can charge, so "is this the
  same price" is integer equality rather than a tolerance somebody picks.
- `numeric` would be exact too and arrives in Go as `pgtype.Numeric`, which every call site has to
  remember to check and one eventually will not.

`ParseMoney` **refuses** anything finer than a poisha rather than rounding it: a price of 0.335 per
tablet is a per-unit figure somebody derived by dividing a pack price, and quietly storing 0.34
makes the pack price stop adding up.

**The limit, stated plainly:** a price _per IU_ or _per mL_ can be finer than a poisha. Every price
this clinic quotes is per tablet, vial, pen or cartridge, and those are whole poisha. Per-IU
insulin pricing would be a new column with its own scale, not a migration of this one.

## 3. Unverified means unverified

The 250 seeded prices are published MRP read off medex.com.bd on **8 September 2026**. They are
**not what this clinic charges**, and Dr. Nahid has not reviewed them. A price nobody has checked
that looks like a price somebody approved is the most dangerous thing this module could ship, so:

| Mechanism                                                                    | What it stops                                              |
| ---------------------------------------------------------------------------- | ---------------------------------------------------------- |
| `verification` is `PROVISIONAL` or `VERIFIED`, on every price the API serves | A screen drawing a number with no state beside it          |
| `price_names_who_recorded_it` — `(origin = 'SEED') = (recorded_by IS NULL)`  | A price change with nobody's name against it               |
| `price_seed_is_never_verified`                                               | A seeded row becoming approved without a person            |
| `assert_every_verified_price_names_a_person()` (invariant 110)               | A row that arrived some other way — a restore, a hand edit |

Clearing a provisional price therefore **requires** a person to write a new row with their name on
it, which is exactly what the monthly review does.

**DGDA registration numbers are null on all 250.** Not one was available from the source. The
column exists; a fabricated regulatory identifier in a clinical system is worse than an absent one.

## 4. Bulk import is per-row partial, and that is argued for

A supplier's price list is 250 lines. All-or-nothing means one typo on line 187 blocks the 249
correct prices — and what happens next is not "the pharmacist fixes line 187", it is three more
uploads and then the prices being typed by hand. A rule whose effect is that people stop using the
feature has not made anything safer.

Three things make partial acceptance safe here, and all three are present:

1. **The rows are independent.** Each line is one product and one price; nothing on line 188 depends
   on line 187. A partial result is a smaller set of complete facts, not a half-applied
   transaction. (This is precisely why a ledger append is all-or-nothing and this is not.)
2. **Nobody has to guess what landed.** Every line's outcome is a row in
   `core.formulary_import_row` — the line number from the person's own spreadsheet, the column at
   fault, and the reason in **both languages** — still readable tomorrow at
   `GET /v1/formulary/imports/{id}`. A check constraint refuses a rejected row that does not say
   why in both.
3. **Nothing lands by surprise.** `mode` defaults to `DRY_RUN`: the whole file is validated, the
   whole report is written, and not one product or price is touched.

One thing _is_ all-or-nothing: the persistence. The accepted rows' effects and the report commit in
a single transaction, so a database failure halfway leaves no import that half-happened.

**An unknown generic is a rejection, never a new molecule.** Every CP77 rule and CP78
duplicate-therapy check keys off the generic; a spreadsheet able to create "Metformin HCl" beside
"Metformin hydrochloride" would split one molecule's safety rules across two records and nothing
would look wrong until a patient was prescribed both.

**Importing is not verifying.** A price from a distributor's list is a fact about the distributor.
It lands `PROVISIONAL` unless the person uploading ticks "these are the prices we charge".

## 5. The monthly review: a role, and optionally a person

§16.1 asks who owns monthly price review. D-56 defaults it to **PHARMACIST**. Both shapes are
modelled, because the failures differ:

- **Role only** survives the pharmacist leaving and always has somebody the reminder can reach. It
  is also the classic way a recurring task goes undone: a task owned by "the pharmacists" is one
  nobody in particular has failed to do.
- **Person only** is what makes somebody feel responsible — and stops dead the month they are on
  leave, and permanently the month they resign, with nothing in the system noticing.

`core.formulary_review_owner` therefore always names a role and may name a person.
**The recommendation is the role as the floor with a named deputy on top.** Each cycle **copies
both** when it opens: who was asked to do March's review is a fact about March and does not change
because the job was reassigned in June.

### Monthly, on a queue that only does intervals

`ops.job_schedule.every_seconds` tops out at a day — ADR-0031 bought intervals rather than a cron
parser, on the reasoning that every periodic job this system had was "every N". A monthly review
does not fit that, and the answer is not to buy a cron parser: **"is a review due" is a domain
question**. `maintenance.formulary_price_review` runs daily and asks it. If the month already has a
cycle it does nothing; if the due day has not arrived it does nothing; otherwise it opens the cycle
and raises the reminder.

That makes the reminder idempotent by construction, and the unique index on
`(facility_id, period_month)` is the backstop: two workers that both claim the job produce one
cycle and one alert. The sweep reads the facility's own timezone, because a review "due on the
1st" means the 1st in Faridpur and a server in UTC is six hours behind that.

### Where the reminder actually goes, and the gap in it

A row in `core.admin_alert`, which is the notification channel this system has: every
administrator's console polls it every thirty seconds. Plus the open cycle itself, which the
formulary screen draws at the top for anybody holding `formulary.read`.

**Read the gap as a gap.** The pharmacist does not hold `alert.read`, so the console alert reaches
administrators rather than the owner. The owner sees the review because their own screen says so,
not because anything pushed it to them. SMS or e-mail needs a gateway this clinic does not yet have
(`docs/audit.md` §7 carries the same open item).

## 6. Who may do what

| Permission               | Held by                                                                    | Sensitive |
| ------------------------ | -------------------------------------------------------------------------- | --------- |
| `formulary.read`         | physician, junior doctor, pharmacist, Rx educator, QA, nutritionist, admin | no        |
| `formulary.write`        | pharmacist, physician, admin                                               | no        |
| `formulary.price.review` | pharmacist, physician, admin                                               | no        |

All three names were reserved in `core.permission` at CP06; CP75 is what grants them.

**None is sensitive, and that is a decision.** §4.4 blinds registration and the pharmacist from
diagnoses and clinical interpretations. A formulary holds trade names, strengths and prices — and
the pharmacist is the person §16.1 puts in charge of it. Marking these sensitive would be the
access model contradicting itself.

**The researcher holds none of them.** D-48 says the research role reaches the de-identified marts
and nothing else, and `assert_rbac_constraints()` enforces it. CP127's affordability lens gets its
prices through `research`, not through `core`.

## 7. Nothing is deleted

A withdrawn product is deactivated with a reason and a name (`product_withdrawal_is_attributed`);
its price history stays, because a prescription written last year was written at a price and that
is still the answer to what it cost. `dthcms_app` holds no `DELETE` on any table here — the
migration revokes it explicitly, because `ALTER DEFAULT PRIVILEGES` in `core` grants full CRUD —
and `assert_formulary_history_is_kept()` (invariant 112) re-checks it after every migration.

## 8. Acceptance criteria, and where each is proven

| Criterion                                            | Test                                                                                                                                                                                         |
| ---------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (1) The price as of any past date is retrievable     | `TestThePriceAsOfADateIsThePriceThatWasInForce` (7 dates around 3 prices, both boundaries), `TestAPriceQueryNeverReturnsAPriceThatHadNotTakenEffect`, `TestTheAsOfRouteAnswersThroughTheAPI` |
| The answer is unique                                 | `TestTwoPricesCannotCoverOneDay`; `EXCLUDE` constraint; invariant 111                                                                                                                        |
| A superseded price is never rewritten                | `TestASupersededPriceIsNeverRewritten` (five refused edits)                                                                                                                                  |
| (2) Bulk import reports errors per row               | `TestBulkImportReportsEveryRowsErrorInBothLanguages` (8 lines, 6 failure modes, both languages)                                                                                              |
| …and the report outlives the request                 | `TestTheImportReportSurvivesTheRequest`                                                                                                                                                      |
| …and a dry run changes nothing                       | `TestADryRunWritesTheReportAndChangesNothing`                                                                                                                                                |
| (3) The seed loads, and re-runs safely               | `TestEverySeededPriceIsProvisionalAndNamesNobody`, `TestTheSeedIsRerunnableAndDoesNotClobberAnApprovedPrice`, `TestTheSeedDoesNotFillAGapAroundAPriceAPersonRecorded`                        |
| (4) A monthly reminder reaches the named owner, once | `TestTheMonthlyReviewOpensOnceAndRemindsItsOwner`, `TestTheSweepWaitsForTheDueDay`, `TestANamedOwnerIsCarriedOntoTheCycleAndKeptThere`                                                       |
| Price changes are audited with the actor             | `TestTheAuditTrailNamesWhoChangedAPrice` (through HTTP), `TestAPriceCannotBeRecordedWithNobodysName`                                                                                         |
| A browser with no device can read and write          | `TestEveryReadIsReachableFromABrowserSessionWithNoDevice`; `dthclint readpath`                                                                                                               |
| Money never becomes a float                          | `money_test.go` — parse, refuse, render, round-trip                                                                                                                                          |

## 9. Open, and whose decision it is

| Question                                                                                             | Whose                |
| ---------------------------------------------------------------------------------------------------- | -------------------- |
| **All 250 seeded prices.** Published MRP, not this clinic's. Every one is provisional until reviewed | Dr. Nahid            |
| Whether the seed covers a month of real prescriptions (criterion 3)                                  | Dr. Nahid            |
| The 33 Bengali class names, and the Bengali forms and dispensing units                               | Dr. Nahid            |
| Role or named person as review owner — both work; the recommendation is both                         | Dr. Nahid            |
| Which day of the month the review is due (default: the 1st)                                          | Clinic               |
| A notifier that reaches the pharmacist rather than the administrators' console                       | Blocked on a gateway |
| DGDA registration numbers                                                                            | Blocked on a source  |
| Per-IU or per-mL pricing, if it is ever wanted                                                       | Dr. Nahid            |

---

## 10. The two-letter prescribing autocomplete (CP76)

§10.1's hard speed requirement, built on this module and shipped with it. The rule library CP77
adds on top is in [`medication-rules.md`](medication-rules.md).

### The whole formulary lives in the API process

The criterion is a p99 under 50ms **including network** on the clinic's LAN, and most of that
budget is spent before the handler runs. The plan's own answer is to hold the catalogue in memory,
and it is the right one: 250 products across 59 molecules is about 180 brand groups and well under
a megabyte, and it does not grow with patients, visits or prescriptions.

**Measured, not asserted.** 1000 real HTTP round trips against the running API over the real seed:

| p50     | p90     | p99     | max     |
| ------- | ------- | ------- | ------- |
| 1.16 ms | 1.49 ms | 3.98 ms | 5.18 ms |

Loopback, so a clinic LAN adds roughly half a millisecond each way. The cold first search — which
loads all 250 rows and indexes them — is 13 ms.

### A result is a brand, not a row

Levothyroxine is stocked as Thyrox in six strengths and Thyrin in four. A list of products answers
"th" with ten rows of levothyroxine before it reaches anything else, and the checkpoint's manual
verification — _the intended drug is in the top three_ — would be unreachable for any brand whose
molecule has more than three strengths.

So each result is a brand in a form, carrying its strengths as chips. **This is the UI decision
Dr. Nahid should make rather than me**: the alternative is one row per strength, which is one
keystroke faster for a drug stocked in one strength and unusable for levothyroxine.

### The ranking, and the two terms that are not there yet

```
1. match class       prefix > substring > trigram
2. days since this physician last prescribed it     ← CP80
3. how often he prescribes it                       ← CP80
4. match kind        trade-prefix > generic-prefix > later word > contains
5. alphabetical
```

Terms 2 and 3 have **no source until CP80 builds prescriptions**, so they are constant and the
response says `ranking_complete: false`; the combobox draws a note from it. Nothing was
substituted: ranking by anybody's prescribing would answer a different question, and a proxy would
produce an order that looks personalised and is not, which is very hard to notice as wrong. The
seam is `formulary.UsageSource` — CP80 writes an implementation and passes it to `CacheConfig`;
no ranking code changes.

Term 1 sits above the physician's own history deliberately. A drug he prescribes daily whose name
merely _contains_ what he typed does not outrank one that _begins_ with it: what he typed is
evidence about what he wants that his history is not.

### Bengali script

The query is transliterated grapheme by grapheme and folded like everything else. That alone is
not enough — Bengali writes no inherent vowel, so কমেট transliterates to "kmet" and will never be
a prefix of "komet" — so a Bengali query is **also** matched on the consonant skeleton, of which
"km" is a prefix of Comet's "kmt". The skeleton is not used for Latin queries; it would make "ms"
match Metformin, which is not a search.

Both sides are folded so that c/k/q, s/z, f/ph, v/w/b, y/i, x/ks and doubled letters are the same
search. `normalised_query` reports what the query became, which is the only way the Bengali case is
explainable to the person typing.

### The sixty seconds, and what actually carries it

Two mechanisms, and the slower one is the criterion. The write handlers drop the snapshot
immediately, which is a latency optimisation for changes made by _this_ process — and is
deliberately **not** what the test exercises, because a test that changes a price by calling into
the process it then queries has proved a function call works.

What carries criterion 4 is a refresh loop that asks the database for a watermark — a row count
and a newest-change instant — every ten seconds. `TestAChangeMadeOutsideThisProcessReachesTheAutocompleteInTime`
changes a price with SQL and polls the HTTP endpoint with the production interval: **measured at
10.1 s in the test and 7.6 s against the running API**.

The snapshot also carries the clinic day it was built for, because the price in force changes at
midnight with no row changing — and the monthly review records future-dated prices routinely. The
autocomplete serves **the price in force today**, not the price whose period happens to be open.

### Where two letters land, over the real seed

`2-letter rank` is the position among brands; `of` is how many brands matched. All twenty-four are
found on two letters, and all twenty-four are in the top three on three.

| Brand      | Typed | Rank | of  | 3 letters | Rank |
| ---------- | ----- | ---- | --- | --------- | ---- |
| Comet      | co    | 1    | 18  | com       | 1    |
| Secrin     | se    | 1    | 21  | sec       | 1    |
| Emjard     | em    | 1    | 20  | emj       | 1    |
| Mixtard 30 | mi    | 1    | 52  | mix       | 1    |
| Glarine    | gl    | 1    | 63  | gla       | 1    |
| Neurolin   | ne    | 1    | 39  | neu       | 1    |
| Dapaglip   | da    | 2    | 8   | dap       | 1    |
| Trulicity  | tr    | 2    | 7   | tru       | 1    |
| Rapilog    | ra    | 2    | 17  | rap       | 1    |
| Thyrin     | th    | 2    | 10  | thyr      | 1    |
| Rosuva     | ro    | 2    | 48  | ros       | 1    |
| Telmilok   | te    | 2    | 19  | tel       | 2    |
| Angilock   | an    | 2    | 30  | ang       | 1    |
| Linatab    | li    | 3    | 84  | lin       | 1    |
| Ozempic    | oz    | 3    | 31  | oze       | 1    |
| Fitaro     | fi    | 3    | 8   | fit       | 1    |
| Thyrox     | th    | 3    | 10  | thy       | 3    |
| Comprid    | co    | 4    | 18  | comp      | 2    |
| Viglita    | vi    | 4    | 19  | vig       | 2    |
| Anzitor    | an    | 4    | 30  | anz       | 1    |
| Bigmet     | bi    | 5    | 19  | big       | 3    |
| Ansulin    | an    | 5    | 30  | ans       | 2    |
| Sitagil    | si    | 6    | 41  | sit       | 1    |
| Carbizol   | ca    | 6    | 24  | car       | 3    |

Read the right-hand columns as the answer to the checkpoint's manual verification: **on two
letters six of the twenty-four are outside the top three**, and every one of those is a case where
the clinic stocks several brands beginning with the same two letters (Ansulin, Anzitor and
Angilock all answer "an"). One more keystroke settles all of them. Whether that is acceptable is
Dr. Nahid's to say; the numbers are here so he can say it rather than be told it.

The manufacturer is deliberately not searched here — "In" would otherwise return every Incepta
product and bury Insulatard. The admin list above still matches it.
