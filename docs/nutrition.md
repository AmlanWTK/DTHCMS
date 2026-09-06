# The nutrition assessment

CP59. Station 7's 24-hour recall, and §12.1's diet–outcome data.

---

## Two assistants, one recall, and nothing to merge

Criterion 2, and [R-01]/[R-02]: _"a second assistant may enter food habits from their own
device"_ — concurrently, each attributed.

The obvious implementation is a document that two devices edit, and then a lock, a merge, or
last-write-wins. All three answer a question this shape does not have, and the third loses a
patient's breakfast without telling anybody.

**A 24-hour recall is not a document. It is a set of things a patient said they ate.** Each is its
own row, written once, by one named person, and never edited. Two operators adding items to the
same recall cannot conflict because they never write the same row — the conflict is designed out
rather than resolved, which is the only version of concurrent editing that is correct at four in
the afternoon with a queue waiting.

There is therefore no "start an assessment" endpoint, no session to acquire and nothing to close.

The one real collision is the **duplicate**: the same food entered twice by two people who did not
see each other. That is handled where duplicates belong — visibly. Every write returns the whole
day, so the operator who just added rice sees what their colleague added while they were typing,
and a duplicate is taken back with a reason and a name. **Either operator may withdraw either
entry**, because requiring the original recorder to remove it would mean the duplicate stays until
they come back from the next patient.

## Household measures

The plan says it plainly: portion sizes in cups, pieces and spoons. A patient does not say "one
hundred and twenty grams of rice"; they say "two cups". A screen that asks for grams asks the
operator to do a conversion in their head in front of a patient, and the number that reaches the
record is then the operator's arithmetic rather than the patient's answer.

So the entry carries **the measure the patient used and how many** — and the grams the table says
that weighs. Both, because the answer as given is the evidence and the grams are an interpretation
of it: a record holding only the weight could never be re-derived when the portion table is
corrected.

Every portion says how big it is — "one medium ruti", "a small teacup" — because a measure without
a size is a measure two operators use differently.

**Grams is the escape hatch**, and the only universal measure. An operator who knows the weight
should be able to say so rather than translating it into cups, and a measure the table cannot weigh
is refused rather than guessed: a guessed weight becomes a calorie count somebody acts on.

## The food table is a content dependency, and every row says so

The plan calls it out: this needs a national nutrition institute table or equivalent, sourced or
authored, and it is not a coding problem.

What ships is a **starter list of what this clinic actually sees** — twenty-four foods, twenty-nine
portions — so that station 7 runs on day one and the nutritionist has something concrete to correct
rather than an empty picker. Every row names where its figures came from, `approved_at` is null on
all of it, and the API reports both. An invariant refuses a food with no stated source, so the first
one somebody adds by hand cannot arrive without provenance.

The numbers are given to one or two significant figures. That is the honest precision for "one cup
of cooked rice"; a table quoting 129.4 kcal would be pretending otherwise.

## The arithmetic is the table's

Criterion 3. The client sends the food, the measure and the count; **the grams and the energy are
computed by the server** from the composition table, and the projection does it again on a rebuild.
A client that sent them would be sending its own arithmetic, and a calorie count is a number people
act on.

The per-entry figures are copied onto the row rather than joined at read time, so a recall taken
today still adds up to what the operator was shown after somebody corrects the composition table
next month. `core.assert_every_diet_entry_matches_the_food_table` checks that the copies still agree
with the table, which is what notices a hand-written row.

The day's totals are **summed over the entries that still stand**, never accumulated. A withdrawn
entry has to take its calories with it, and a total that added and never subtracted would drift the
first time an operator corrected a duplicate — which, at a station where two people are entering one
recall, is the first hour.

## The intake values

`ENERGY_INTAKE`, `PROTEIN_INTAKE`, `CARB_INTAKE` and `FAT_INTAKE` are ordinary CP42 derived
observations, rewritten after every entry, so §12.1's analysis reads them beside every other value
and they inherit the correction cascade, the timeline, the research extract and the attribution.

They are dated to **the day being recalled**, not the day they were recorded. A timeline that placed
Monday's eating on the Tuesday it was described would misorder every recall.

Their formula version is `0.1.0-proposed`, and the reason is the food table rather than the
arithmetic: adding up a day's entries is not a formula anybody disputes, but the numbers being added
come from a table nobody has approved. A total computed against the starter list must stay
identifiable once a national table replaces it.

## Small decisions worth recording

**The recall date defaults to yesterday.** A recall taken today is almost always about yesterday. A
screen defaulting to today would be wrong by default, and an operator correcting the date on every
patient will stop correcting it.

**The hour is optional.** A patient who cannot remember when they ate still remembers the meal, and
requiring a time would produce invented ones.

**The picker matches synonyms.** "roti", "ruti", "chapati" and "রুটি" are one food; a picker that
only matched the formal name is a picker somebody gives up on. Trigram search over the names and the
synonyms, prefix matches first.

**The portions come with the foods**, in one query. A picker that showed a food and then asked what
a cup of it weighed would be a second round trip inside the four minutes criterion 1 allows for the
whole recall.

**Appends to one patient serialise.** The ledger's gapless per-aggregate sequence means concurrent
writes to one patient queue behind each other. Eight at once complete in under two seconds; two —
which is the real scenario — are imperceptible. Worth knowing when sizing the connection pool:
`DTHCMS_POSTGRES_MAX_CONNS` defaults to ten, and a pool smaller than the concurrent write count
surfaces as a request timeout that looks exactly like a deadlock.

## Deliberately not built

- **No AI diet plan.** CP132, and out of scope here by the plan's own words.
- **No "assessment" object.** See the top of this document; it is the design.
- **No client-side calorie arithmetic.** The client adds up what the server returned; it never
  multiplies a portion by a composition figure.
- **No renal or hepatic gate.** The plan asks that status be _carried in as context_ — the
  physician's screen shows the eGFR beside the recall — not that the station refuse to record food.

## Open questions for the clinic

- **The food composition source.** A national institute table, or the clinic's own authored one.
  Everything seeded is a starter list and says so.
- **The portion weights**, which are the numbers an operator's four minutes actually depends on. A
  nutritionist watching one clinic will find the wrong ones in a morning.
- **What is missing from the list.** Twenty-four foods is enough to start and not enough to finish;
  the honest measure is how often an operator cannot find what a patient ate.
- The Bengali food names and portion notes, drafted here.
