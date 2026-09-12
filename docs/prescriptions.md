# The prescription

CP80. The clinic's primary output artefact, and the most medico-legally significant object in the
system. Everything on this page follows from one sentence.

> **Nobody edits a signed prescription.**

Not the physician who wrote it, not an administrator, not a developer with the application's
database password. A change to a signed prescription is a **correction**: a new prescription that
says what it corrects and why, while the original stays in the record exactly as it was written.

The formulary the items come from is [`formulary.md`](formulary.md); the safety engine that checks
them is [`medication-rules.md`](medication-rules.md); the ledger underneath is
[`event-store.md`](event-store.md).

---

## 1. The state machine

Seven states, twelve legal edges, thirty-seven refused cells.

```
                    ┌──────────────────────────────────────────┐
                    │                                          ▼
  DRAFT ──submit──► QA_REVIEW ──sign──► SIGNED ──print──► PRINTED
    │  ◄──bounce──┘                       │  │                │
    │                                     │  └──dispense──────┼──► DISPENSED
    │                                     │                   │        │
    └──cancel──┐         ┌────cancel──────┘                   │        │
               ▼         ▼                                    │        │
            CANCELLED ◄──┘                                    │        │
                                                              │        │
       SIGNED / PRINTED / DISPENSED ──correct──► CORRECTED ◄───┴────────┘
```

| From        | May become                                       |
| ----------- | ------------------------------------------------ |
| `DRAFT`     | `QA_REVIEW`, `CANCELLED`                         |
| `QA_REVIEW` | `DRAFT` (bounce), `SIGNED`, `CANCELLED`          |
| `SIGNED`    | `PRINTED`, `DISPENSED`, `CANCELLED`, `CORRECTED` |
| `PRINTED`   | `DISPENSED`, `CORRECTED`                         |
| `DISPENSED` | `CORRECTED`                                      |
| `CANCELLED` | — terminal                                       |
| `CORRECTED` | — terminal                                       |

**The matrix is rows, not code.** `core.prescription_transition` holds the twelve edges with the
event type each carries, a bilingual note, and the checkpoint that owns the workflow behind it.
The Go `Machine` is built from those rows at start-up rather than from a literal, because two
copies of a state machine agree until somebody edits one — and the failure then is an application
that permits a transition the trigger refuses, or worse, the other way round, which writes a
permanent ledger event the projection then cannot apply.

**Cancelling stops at `PRINTED`.** Once the paper is in the patient's hand the pharmacy may act on
it, so the record has to show a replacement rather than an absence. That is the line, and it is
the one judgement in the matrix.

**Four of the twelve edges belong to screens that do not exist yet.** `PRESCRIPTION_QA_BOUNCED` is
CP83's, `PRESCRIPTION_SIGNED` is CP84's, `PRESCRIPTION_PRINTED` is CP89's,
`PRESCRIPTION_DISPENSED` is CP118's. The transitions are complete and tested; the service methods
exist; **there is no HTTP route in front of any of them.** A signing endpoint without step-up 2FA
would be a hole, not a head start.

## 2. Signed immutability, in four places

Only one of them is application code, and that is deliberate: a handler check is one refactor from
disappearing.

| #   | Mechanism                                                  | What it stops                                                  |
| --- | ---------------------------------------------------------- | -------------------------------------------------------------- |
| 1   | `GRANT SELECT` and nothing else to `dthcms_app`            | Any statement this process could issue, correct or malicious   |
| 2   | `core.prescription_is_frozen()`                            | Any content change, **for every role including the projector** |
| 3   | `core.prescription_item_is_frozen()`                       | Any item write while the prescription is not `DRAFT`           |
| 4   | `core.prescription_transition_is_legal()`                  | Any status change that is not a row of the transition table    |
| +   | `core.prescription_refuses_truncate()` + `REVOKE TRUNCATE` | The one statement that bypasses row triggers                   |
| +   | Invariant 123 (`assert_prescriptions_cannot_be_rewritten`) | A later migration widening the grant or dropping a trigger     |

`prescription_is_frozen()` is written as **an explicit list of what may change**, not of what may
not: the status, the stamp columns belonging to the status being entered, the correction link, and
bookkeeping. A column added to this table next year is immutable by default, which is the right
default for this object.

**`DRAFT`, not "not signed".** Item writes are frozen from the moment a prescription leaves
`DRAFT`, including in `QA_REVIEW`. A prescription whose lines changed underneath the reviewer is a
prescription that was reviewed and then altered — the same defect as editing a signed one, one
step earlier. A bounce returns it to `DRAFT` and the lines become editable again.

**Nothing is ever deleted.** A removed item keeps its row with `removed_at`, `removed_by` and
`removed_reason`, because "what was on this prescription at 14:05" has to stay answerable after the
item came off it at 14:06.

### The one hole, named

The projector may `DELETE`, and only under `SET LOCAL dthcms.rebuilding = 'on'`. A rebuild has to
be able to empty the table; refusing that would mean the read model could never be reconstructed
from the ledger, which is the guarantee the whole design rests on.

It is deliberately **not a security boundary** — a role that can set a GUC can set that one — and
does not need to be. What it protects is the _projection_; the ledger behind it is append-only by
grant, rule and trigger (CP23), so a projection somebody emptied rebuilds into exactly what it was.
What the flag stops is the accident: a stray `DELETE` in a later migration or a maintenance script,
which would otherwise silently remove a signed prescription from the read model with nothing to
notice it.

## 3. The price at the time of prescribing

Criterion 3, and the reason it is not simply a join.

CP75 never edits a price: a new one supersedes the old and both stay, each with its own period.
That makes "what did this cost on 4 March" answerable — by a query, today, against a table somebody
could still `ALTER`.

A prescription needs a stronger guarantee, because what it records is not a commercial fact but
**what a patient was told to pay**. So:

1. `Service.AddItem` reads `formulary.PriceOn(facility, product, today)` at the instant the line is
   written;
2. the **amount** goes into the `PRESCRIPTION_ITEM_ADDED` payload as an integer number of poisha,
   together with the price row's id, the day it took effect, and its verification state **as it was
   then**;
3. `read.apply_prescription_item_added` writes those numbers straight onto the row and **never
   reads `core.medication_price`**;
4. `core.prescription_item_is_frozen()` refuses any `UPDATE` that would move a price column;
5. `PRESCRIPTION_ITEM_MODIFIED` has no price field at all, and the function that applies it does
   not touch one.

Step 3 is the load-bearing one. Rebuild `read.prescription_item` from the ledger in 2030 and every
line still carries the price of the day it was written — which a join would silently reprice on
every rebuild. `TestThePriceAtPrescribingTimeIsNeverBackUpdated` sets a price, drafts a
prescription, raises the price, asserts the draft still says 9.50, **then rebuilds the projection
and asserts it again**. Without the rebuild the test would pass against the wrong implementation.

**A product with no price captures no price, not a zero.** Zero would tell a patient a medicine is
free.

## 4. Correction, including after dispensing

The plan left this open. The decision, and it is flagged for Dr. Nahid:

- A correction is **always permitted** from `SIGNED`, `PRINTED` or `DISPENSED`.
- It is a **new prescription**, in `DRAFT`, naming what it corrects (`corrects_prescription_id`)
  and why (`correction_reason`). The original moves to `CORRECTED`, is linked forward
  (`corrected_by_prescription_id`), and **is never edited and never removed from the record**.
- When the original had already been dispensed, the correction records that fact **on itself**
  (`corrects_dispensed_original`). It has to be on the correction, because the original's status
  becomes `CORRECTED` the same instant and stops saying `DISPENSED` — a reader who went to look at
  the original would find nothing.

**What the pharmacy does next is a clinical and operational question and this system does not
answer it.** It records that a dispensed prescription was corrected and makes that visible.

**One correction per prescription**, enforced by a unique index. Two sheets each claiming to
supersede the same one would leave no reader able to say which is current. A correction that itself
needs correcting is corrected in turn — a chain, not a fan.

**A draft is edited, not corrected.** Offering both would make "correct this" mean two different
things depending on a status the physician cannot see from the button.

**The correction is a separate aggregate**, with its own event stream. Appending it to the
original's stream would make the original's history contain events that are not about it, and would
make "what did this prescription say" a question about where in the stream to stop reading.

## 5. Carry-forward

`carry_forward_from` copies every **live** line of a previous prescription onto a new one,
**re-priced at today's price** — because carrying a line forward is prescribing it again today, and
what the patient will pay is today's price rather than last month's. Each copy names the line it
came from.

`carry_forward_confirmed` must be `true`. Its absence is a **422, not a silent skip**: a physician
who meant to carry forward and got an empty sheet notices; one who did not mean to and got last
month's six drugs might not.

The source must belong to the same patient. Carrying another patient's prescription forward is the
worst mistake this feature could make and it is one id typed wrong away.

## 6. The events

Eleven types, on an aggregate of their own — `PRESCRIPTION` — so that "everything that ever
happened to this sheet" is `Stream("PRESCRIPTION", id, 1)` rather than a filter over a patient's
whole clinical life.

| Event                           | Carries                                                |
| ------------------------------- | ------------------------------------------------------ |
| `PRESCRIPTION_CREATED`          | patient, visit, and the correction/carry-forward links |
| `PRESCRIPTION_ITEM_ADDED`       | the line, **and the price as a number**                |
| `PRESCRIPTION_ITEM_MODIFIED`    | how it is taken. **No price field exists.**            |
| `PRESCRIPTION_ITEM_REMOVED`     | the reason                                             |
| `PRESCRIPTION_SUBMITTED_FOR_QA` | the transition                                         |
| `PRESCRIPTION_QA_BOUNCED`       | the transition (CP83 adds a v2 with the station)       |
| `PRESCRIPTION_SIGNED`           | the transition (CP84 adds a v2 with the signature)     |
| `PRESCRIPTION_PRINTED`          | the transition                                         |
| `PRESCRIPTION_DISPENSED`        | the transition (CP118 adds a v2 with per-item detail)  |
| `PRESCRIPTION_CANCELLED`        | the reason, which is required                          |
| `PRESCRIPTION_CORRECTED`        | the correction that supersedes it                      |

The seven transitions share one payload shape and differ in their **name**, which is what the
ledger records and what a reader looks for. Three of them grow: each becomes a **version 2 with an
upcaster** rather than gaining a field on the version 1, so an event written today stays decodable
and a check run against it stays reproducible (§7.10).

## 7. Acceptance criteria, and where each is proven

| Criterion                                               | Test                                                                                                                                                                               |
| ------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (1) Signed prescriptions cannot be modified by any path | `TestASignedPrescriptionCannotBeModifiedThroughTheService`, `…ThroughTheAPI`, `TestTheDatabaseRefusesToEditASignedPrescription`                                                    |
| (2) Every item change is an event with attribution      | `TestEveryItemChangeIsAnEventWithAttribution`                                                                                                                                      |
| (3) The price is captured and never back-updated        | `TestThePriceAtPrescribingTimeIsNeverBackUpdated`, `TestAProductWithNoPriceProducesNoPriceRatherThanZero`                                                                          |
| (4) Illegal transitions are rejected                    | `TestEveryCellOfTheTransitionMatrix` (49 cells), `TestTheServiceRefusesEveryIllegalTransitionOnRealPrescriptions`, `TestTheDatabaseRefusesAnIllegalTransitionEvenFromTheProjector` |
| Items are frozen from `QA_REVIEW`, not from `SIGNED`    | `TestItemsAreFrozenInQAReviewAndNotOnlyOnceSigned`                                                                                                                                 |
| Correction semantics, including after dispensing        | `TestACorrectionSupersedesAndNeverEdits`, `TestCorrectingADispensedPrescriptionRecordsThatMedicineHasLeftTheCounter`                                                               |
| Carry-forward needs an explicit yes                     | `TestCarryForwardNeedsAnExplicitYes`, `TestCarryForwardRefusesAnotherPatientsPrescription`                                                                                         |
| Go and the database agree about what is editable        | `TestGoAndTheDatabaseAgreeAboutWhatIsEditable`                                                                                                                                     |
| The payload carries no diagnosis                        | `TestThePrescriptionPayloadCarriesNoDiagnosis`                                                                                                                                     |
| CP78 runs against a saved prescription                  | `TestTheSafetyCheckRunsAgainstASavedPrescription`                                                                                                                                  |

## 8. Manual verification

```bash
# Sign a prescription through the service (no route: CP84 owns signing), then:
curl -i -X PATCH "$API/v1/prescriptions/$RX/items/$ITEM" \
  -H "Authorization: Bearer $TOKEN" -H 'X-Requested-With: DTHCMS' \
  -H "Idempotency-Key: $(uuidgen)" -H 'Content-Type: application/json' \
  -d '{"dose":"2 tablets","frequency":"twice daily"}'
# 409, and the message names the correction path — in both languages.
```

```sql
-- As dthcms_app: permission denied on all four.
UPDATE read.prescription SET status = 'DRAFT' WHERE id = '...';
DELETE FROM read.prescription WHERE id = '...';
UPDATE read.prescription_item SET dose = 'ten tablets' WHERE id = '...';
DELETE FROM read.prescription_item WHERE id = '...';

-- As dthcms_projector, which DOES hold the grant: the trigger refuses anyway.
UPDATE read.prescription SET signed_at = now() WHERE id = '...';
UPDATE read.prescription_item SET dose = 'ten tablets' WHERE id = '...';
TRUNCATE read.prescription CASCADE;

-- And the matrix, readable in one statement.
SELECT from_status, to_status, event_type, owned_by FROM core.prescription_transition
 ORDER BY from_status, to_status;
```

## 9. Open

- **The pharmacist holds `prescription.read`.** §4.4 blinds them to diagnoses, and that holds here
  by construction — there is no diagnosis column on either table, and
  `TestThePrescriptionPayloadCarriesNoDiagnosis` keeps it that way as fields are added. CP118
  builds the pharmacy queue with its own redaction golden test on the raw response.
- **Correction after dispensing** is decided (§4) and is Dr. Nahid's to confirm. The clinical
  question of what the pharmacy then does is explicitly not answered here.
- **`quantity` is modelled and unused.** CP89's printed sheet and CP119's inventory will want it;
  nothing computes it from dose × frequency × duration today, because "one tablet twice daily for
  thirty days" is sixty tablets only when the strength and the dispense unit agree, and guessing
  wrong prints a number a pharmacist will dispense against.
- **No prescription-level total.** Deliberate: a sum over lines whose prices are individually
  provisional would look like a bill. CP89 decides whether the printed sheet carries one.
