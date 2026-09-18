# The prescription editor

CP81. The screen Dr. Nahid prescribes in, and the contract that makes its preview the same
document CP89 will print.

The aggregate underneath is [`prescriptions.md`](prescriptions.md); the medicines come from
[`formulary.md`](formulary.md); the findings come from
[`medication-rules.md`](medication-rules.md).

> The plan's own sentence, and everything below follows from it: **it must be faster than writing
> on paper. If prescribing is slower than paper, the system fails at its central promise
> regardless of everything else.**

---

## 1. What was measured, and what the numbers mean

Against the real Go service, the real Postgres, the real 250-product formulary and the real
forty-eight unapproved rules — not a mock. `web/e2e/cp81-prescribe.spec.ts`, run with
`DTHCMS_E2E_LIVE=1`.

| Criterion                              | Target            | Measured                                   |
| -------------------------------------- | ----------------- | ------------------------------------------ |
| A typical four-item prescription       | <90s _(proposed)_ | **2.0 s**, 35 keystrokes, keyboard only    |
| Safety findings after adding an item   | <500ms            | **p95 454 ms**, min 79 ms, n = 8           |
| Fully keyboard-operable                | yes               | driven entirely by `page.keyboard`         |
| A browser crash loses seconds of work  | ≤ a few seconds   | **the line being typed, and nothing else** |
| The preview matches the printed output | —                 | one document, one hash — §4                |

**The two seconds is not a claim that a prescription takes two seconds.** The script types with no
pause between keystrokes and never stops to decide anything, so what it measures is the floor the
interface imposes: **the software costs about half a second per medicine**, and everything else in
a real consultation is thinking. The honest comparison against paper needs Dr. Nahid at the
keyboard with real patients, and until that happens the 90-second target in the plan is a number
nobody has validated against anything.

The four medicines were Comet 500 mg (metformin), Emjard 10 mg (empagliflozin), Rocovas 5 mg
(rosuvastatin) and Thyrin 25 mcg (levothyroxine) — a diabetic with hypothyroidism, which is this
clinic's commonest sheet. Three of the four had a dose suggestion and cost `two letters, Enter,
Enter`; the fourth had none at its strength and cost the dose and frequency typed by hand. That
split is the honest one: **twenty-eight suggestions against two hundred and fifty products.**

## 2. The four decisions the screen is made of

**One line at a time.** The alternative — a table of empty rows — looks more like a prescription
pad and is slower, because every row costs a decision about which cell to be in. Here there is one
place to type: two letters, `↓`/`↑` for the brand, `←`/`→` for the strength, **Enter** takes it,
focus lands on the dose, **Enter** writes the line and returns to the search box.

**The suggestion fills the fields, and is marked while it does.** This is the decision I am least
comfortable with. A default that has to be retyped saves nothing, so the fields are filled — but a
default a machine wrote must never look like one the physician set, so the filled row sits in a
dashed block naming the guidance it came from and saying, in both languages, that nobody at this
clinic has checked it. The risk accepted is a tired physician pressing Enter twice without
reading; the mitigation is that focus lands **on the dose**, so the suggested value is under the
cursor rather than somewhere on the page. Whether that is enough is a question for somebody who
prescribes for a living.

**Autosave is not a timer.** There is no client-side draft buffer anywhere in this feature.
Pressing Enter on a line posts `PRESCRIPTION_ITEM_ADDED` through CP80's event path, and that is the
save. Nothing is written to `localStorage`, so ADR-0010 holds by construction rather than by care.
Reopening the screen **resumes** the open draft for this visit rather than starting a second one,
which is what makes crash recovery work — recovery is "find the draft again", and the draft is
found by asking the server, not by remembering an id in the browser.

**The safety check runs on the lines, not on the submit.** Every successful write schedules a
check, debounced 250 ms. The panel shows the result for the lines that are actually on the sheet.

## 3. The unapproved rule library, on screen

This clinic has forty-eight rules and **none of them is approved**, so every check returns
`NO_RULES_APPROVED`. That is correct behaviour — CP77's whole design is that a rule nobody has read
cannot fire — and it means the panel a physician sees all day is the empty one.

**An empty panel reads as a clean result.** A green tick, a grey "no findings", a quiet space where
warnings would be: all three tell a physician that four medicines were checked and passed. None of
that happened.

So `NO_RULES_APPROVED` is the **loudest** state on the screen, not the quietest:

- a bordered block in the warning treatment, never grey and never green;
- the engine's own sentence, which ends _"This is not a clean result — it is no result"_;
- _"All 4 medicine(s) on this prescription are unchecked."_ — with the number;
- _"This clinic has 0 approved medication safety rules. Forty-eight are written and waiting"_ —
  and what would change it;
- every drug's coverage row drawn as **"no rule covers this medicine — not checked"**;
- **no tick, no green, and no word meaning safe anywhere in the component.**

`web/test/prescriptions.test.tsx` fails if the panel contains any of _no issues_, _no problems_,
_no findings_, _all clear_, _looks good_, _passed_, _no warnings_ or _safe_ while `rules_live` is
zero, and the browser suite asserts the same thing against the live engine.

A failed check is reported as a failed check — the previous result is dropped rather than left on
screen, because findings for a prescription that has since changed are worse than no findings.

## 4. The preview-to-print contract (for CP89)

**The contract is not "the same data". It is the same resolved document.**

`GET /v1/prescriptions/{id}/print-model` returns [`PrintModel`]. The server decides the ordering of
the lines, the wording of the directions in each language, what is omitted and why, the price
caveat and the status warning. CP81's preview and CP89's renderer both **render** that; neither
decides anything.

A renderer may choose type, spacing, page furniture, paper size and where the QR sits. It may not
choose content, order or wording. **If CP89 needs a fact the model does not carry, the fix is a
field on the model and a version bump** — not a lookup in the print service, because the moment the
printer reads the prescription directly the preview stops being a preview.

Three fields make that enforceable rather than aspirational:

| Field          | What it is for                                                                                                       |
| -------------- | -------------------------------------------------------------------------------------------------------------------- |
| `version`      | The model's shape. A renderer pins the one it was written against; `"1"` today.                                      |
| `content_hash` | SHA-256 over the whole model with `generated_at` zeroed. "Preview matches print" is an equality between two strings. |
| `generated_at` | The only clock-dependent field, and the only one the hash excludes.                                                  |

Nothing else in the model depends on the wall clock or on map iteration order, which is where
CP89's own criterion 4 — _the same prescription always renders identically_ — starts.

**What the model deliberately does not carry, and who owns each:**

| Absent                     | Why, and who adds it                                                                                                                          |
| -------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------- |
| Diagnoses                  | §4.4 blinds the pharmacist, and the pharmacist holds `prescription.read`. CP89 adds them **from the visit**, with its own redaction decision. |
| A prescription-level total | A sum over provisional prices reads as a bill (CP80 §9). `price.caveat_*` says instead how many lines are unpriced or unchecked.              |
| Signature bytes, QR        | CP84 and CP85. `signature` is a state and a bilingual note, so the preview can show the space and say honestly that nothing is in it.         |
| Graphs and gradient bars   | CP87.                                                                                                                                         |

**Both languages are on the sheet at once**, and switching the interface language does not change
what prints. The paper is read by two people: the pharmacist reads the medicine and the directions
in English, the patient and the family read the instruction in Bangla.

`status_caveat_en` / `status_caveat_bn` are non-empty for every state that is not signed. A printed
draft looks exactly like a prescription to a pharmacist, and that sentence is the only thing
between the two — which is why it is in the model rather than left to a renderer to remember.

## 5. The suggestions, and why they are inert

Migration 00064 adds `core.prescribing_default` (28 rows) and `core.instruction_template`
(14 bilingual sentences). Every one is drafted from published guidance — ADA Standards of Care
2025, BNF 88, ATA 2014/2016, KDIGO 2022, manufacturer labelling — cited on the row, and **approved
by nobody**. Invariant 124 refuses a seeded row that claims an approval.

- A default is keyed on **generic + strength**; an empty strength means every strength of the
  molecule. Resolution is exact strength first, molecule-wide second. A client that matched on the
  generic alone would offer metformin's 500 mg suggestion for a 1000 mg tablet.
- **Most medicines have no suggestion and get none.** A fallback that was right often enough to be
  trusted would be the machine prescribing.
- A default is **edited, not versioned** — what it produced is on the prescription, with its own
  event and attribution — and **any edit drops the approval**, enforced by a database trigger. A
  physician approved a sentence, not a row id.
- Approving is `medication.rule.publish`, which §4.4 grants to the physician alone. The screen is
  `/prescribing-defaults`.

Instruction templates are **fixed sentences with no substitution**, copied onto the item when
chosen. D-11 and CP91 own substituted Bangla; a fixed sentence is always grammatical, and a
prescription that pointed at a template row would say something different the day somebody edited
the template.

## 6. Keyboard

Every shortcut takes **Alt**, unlike CP73's dashboard, and every one works while typing. This
screen is typing: a single-key binding would be inert exactly when it is wanted. Alt rather than
Ctrl or Meta, because those belong to the browser and the operating system.

| Keys      | What it does                                      |
| --------- | ------------------------------------------------- |
| `Alt`+`M` | Back to the medicine box                          |
| `Alt`+`L` | The medicines on the sheet                        |
| `Alt`+`S` | The safety findings                               |
| `Alt`+`P` | Show or hide the printed sheet                    |
| `Alt`+`C` | Bring last time's medicines forward               |
| `Alt`+`/` | The list itself                                   |
| `Enter`   | Take the medicine, then put the line on the sheet |
| `Escape`  | Abandon the line and start again                  |

The keys that matter most are not shortcuts: `Enter`, `Escape` and `Tab` are what a four-item
prescription is actually made of.

## 7. The blocker this checkpoint found

**No clinical write from a browser works in this system today.** `eventstore.ActorFrom` refuses an
event whose principal carries no device — "a clinical write needs an enrolled device", which is
[R-03]'s evidence requirement — and a browser session carries none, because only the station app
enrols a device (CP18).

This is not a CP81 defect. It is a pre-existing gap that every web write screen shares, and CP81 is
simply the first screen where it is fatal, because the screen is nothing but writes. The
measurements above were taken through `scratch/cp81/device-proxy.mjs`, which enrols one device and
signs the browser's requests on their way past. **It is an instrument, not a fix**, and it stands
in for whatever the real answer turns out to be: a browser device enrolment, a station-bound web
session, or a deliberate exemption with its own audit treatment. That decision is Dr. Nahid's and
Amlan's, and nothing in the editor can be deployed without it.

Two smaller things the measurement ran into, both worth knowing:

- **A full page navigation does not reliably keep the session.** The access token is held in memory
  by design, so every navigation re-establishes it from the httpOnly refresh cookie, and that
  refresh races with the screen's own first reads often enough to land on the sign-in page.
- **A TOTP code cannot be used twice inside its thirty-second step**, and a second factor that was
  refused counts as a failed login for CP17's progressive throttle. Correct on both counts, and it
  makes any automated flow that signs in repeatedly slow in a way that looks like a bug.

## 8. Open — for Dr. Nahid

| Question                                                                           | What ships today                            |
| ---------------------------------------------------------------------------------- | ------------------------------------------- |
| **Should a suggestion fill the fields at all**, or only offer itself beside them?  | It fills them, marked as unchecked          |
| The twenty-eight doses, frequencies and durations themselves                       | Drafted from published guidance, unapproved |
| The fourteen patient instructions, in his clinical Bengali register                | Mine, unapproved                            |
| Default duration — 30 days for almost everything                                   | 30 days; 56 for weekly vitamin D            |
| Which medicines deserve a suggestion at all, beyond the twenty-eight               | The commonest twenty-eight                  |
| Whether the brand-then-strength combobox is the right order (CP76's open question) | Brand row, strength chips                   |
| His paper baseline for a four-item prescription                                    | Unmeasured; the plan's 90s is unvalidated   |
