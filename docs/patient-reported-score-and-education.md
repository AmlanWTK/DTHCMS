# The improvement score (CP88) and the education station (CP92)

**Status:** authored by Amlan under Dr Nahid's standing delegation, 13 Sep 2026. Everything below
is **physician content and needs his review** — the question wording, the scale anchors, and every
checklist item. It is written so that reviewing it is reading one page.

**Resolves two open decisions the plan leaves to clinical judgement:** CP88's capture station and
question wording, and CP92's device checklist content.

---

# Part one · The 1–10 improvement score (CP88)

## 1. Who asks, and why it is not the physician

**Decision: the score is captured at the education station (Step 11), by the prescription
education officer — not by the physician, and not at the consultation.**

The plan leaves this "to be confirmed operationally" and treats it as a logistics question. It is
not. A patient asked by the consultant who has just changed their treatment how much better they
feel is being asked by the person whose work they are grading, in the room where they want to be a
good patient. The answer drifts upward, and it drifts most for the patients who most want to
please — which in this clinic means the elderly, the poor, and the ones who travelled furthest.

Every point of that drift is a point of false reassurance in the one number that is supposed to
tell us the treatment is working.

So it is asked by somebody with no stake in the answer, at the last station, once the prescription
is already written and cannot be changed by what the patient says. The capturing operator is
recorded on the observation, which the plan already requires — and it matters for exactly this
reason, so the analysis in §12 can ask whether the answer depends on who asked.

## 2. The question

One question, asked the same way every time. Read aloud; not handed over to read.

> **English:** "Compared with your last visit, how do you feel now?"
>
> **Bangla:** "গতবারের তুলনায় এখন আপনার কেমন লাগছে?"

Two things about the wording are deliberate and should not drift:

- **"Compared with your last visit"** anchors the comparison to a fixed point. Without it, patients
  compare against their worst day, their diagnosis, or how they felt this morning, and the number
  stops meaning one thing.
- **"How do you feel"**, not "how much better do you feel". The plan's shorthand is a leading
  question: it presupposes improvement and offers the patient a scale on which to agree. Asking how
  they feel lets the answer be worse.

For a first visit there is no last visit, so the score is **not asked**, and its absence is
recorded as not-applicable rather than as a missing value. A first-visit score would be a number
answering a different question.

## 3. The scale

1 to 10, per [R-11], with anchors a patient who cannot read still understands — five faces and a
colour ramp, with the numeral shown for the operator's benefit rather than the patient's.

| Score | English | Bangla |
|---|---|---|
| 1–2 | Much worse | অনেক খারাপ |
| 3–4 | A little worse | একটু খারাপ |
| **5** | **About the same** | **আগের মতোই** |
| 6–7 | A little better | একটু ভালো |
| 8–10 | Much better | অনেক ভালো |

**The weakness in this scale, stated rather than hidden.** With "the same" at 5, a patient has four
points to say they are worse and five to say they are better. It leans positive by construction. I
have kept 1–10 because [R-11] names it and because the alternative — 0–10 with a true midpoint at
5 — changes a requirement rather than an implementation.

If Dr Nahid prefers the symmetric version, it is a change to reference data and a scale bound, not
to the schema: the anchors live in a table, like the counselling templates and the rejection
vocabulary, so the wording and the banding can both change without a code release.

## 4. What it must not become

The score is stored as an observation in category PRO, and it is one number about how a person
feels. It is **not** a compliance measure, not a satisfaction score, and not a proxy for control.
A patient can feel much better with a rising HbA1c — that is precisely the case worth seeing on the
same axis, and it is why this plots on the dashboard beside the measurements rather than in a
panel of its own.

---

# Part two · The education station (CP92)

Station 11's purpose is fewer treatment failures caused by technique errors. Its output is not
"the patient was educated" — it is **what the patient was actually able to do, in front of
somebody, today.**

## 5. The rule that makes it work: demonstration, not explanation

Every item below is scored on what the patient **did**, not on what they said they understood.
Three states per item:

| State | Meaning |
|---|---|
| **Demonstrated** | Did it correctly, unprompted |
| **Corrected today** | Got it wrong, was shown, then did it correctly |
| **Unable** | Could not do it correctly even after being shown |

"Corrected today" is the most clinically useful of the three and the one a simpler design would
throw away. A patient who has been injecting into one spot for a year and was corrected today is a
different patient from one who never needed correcting, and next visit's officer needs to know
which — because the thing to check next time is exactly what was corrected last time.

**Any "Unable", or three or more "Corrected today", raises a re-education flag** visible to the
physician at the next visit. That threshold is a judgement and is a configurable number, not a
constant.

## 6. The checklists

The checklist is chosen automatically from the devices on the prescription — an insulin pen on the
sheet brings up the pen checklist with no manual selection, which is CP92's first acceptance
criterion. A patient on two devices gets both.

### 6.1 Insulin pen

The commonest device in this clinic and the one where technique errors are most consequential.

1. Checks the expiry date and that the insulin looks as it should
2. **Rolls a cloudy insulin (NPH or premix) gently between the palms until evenly milky — does not shake it**
3. Attaches a new needle for this injection
4. **Air-shot: dials 2 units, holds the pen upright, presses until a drop appears at the tip**
5. Dials the prescribed dose and can state what that dose is
6. Chooses a site and can name at least two sites they rotate between
7. Inserts at 90°, skin pinched only if they are thin or using a longer needle
8. **Holds the button down and counts to ten before withdrawing**
9. Removes the needle and disposes of it safely — not loose in household waste
10. States how the pen in use and the spare are stored: in use at room temperature, spare in the
    fridge, **never the freezer**

Items 2, 4 and 8 are the three that silently cost a patient their dose, and all three are invisible
in the record unless somebody watches. A premix that was not resuspended can deliver anywhere from
a fraction to a multiple of the intended dose. No air-shot and the first units are air. Withdrawing
early leaves insulin on the skin, and the patient sees the drop and believes they were given too
much.

### 6.2 Insulin vial and syringe

Still common where cost matters, and it has more steps to get wrong.

1. Checks expiry and appearance
2. Rolls a cloudy insulin — does not shake
3. Wipes the vial top and lets it dry
4. Draws air equal to the dose and injects it into the vial before drawing
5. Draws the dose and expels air bubbles, then re-checks the amount
6. **If mixing two insulins: draws the clear before the cloudy**, and can say why the order matters
7. Chooses and rotates the site
8. Inserts at 90° and holds before withdrawing
9. Disposes of the syringe safely, in a puncture-proof container
10. States correct storage

### 6.3 Blood glucose meter

1. **Washes and dries hands — water and soap, not alcohol alone** (alcohol residue and fruit sugar
   on unwashed fingers both give false readings, in opposite directions)
2. Checks the strips are in date and match the meter
3. **Lances the side of the fingertip, not the pad**, and rotates fingers
4. Gets an adequate drop without squeezing or milking the finger
5. Applies the drop correctly and waits for the result
6. Records the reading with the time and whether it was before or after food
7. **States their own hypo number and what they will do about it** — the single most important item
   on this list
8. States when they have been asked to test
9. Disposes of the lancet safely

### 6.4 GLP-1 receptor agonist pen

**Two checklists, not one. This is a correction to my first draft, and it matters.**

I originally wrote this section as "weekly GLP-1" and keyed it to the drug class. The class holds
semaglutide and dulaglutide, which are weekly — and **liraglutide, which is once daily**. A rule
matching the class would have handed a Victoza patient a checklist asking them to name the day of
the week they inject. That is not a cosmetic mismatch: the single commonest dosing error in this
class is a patient moving between a daily and a weekly agent and carrying the old rhythm across,
and a checklist that asks the wrong question about timing actively teaches the wrong one.

So the timing item is **generic-level, not class-level**. Items 1, 2, 4, 5, 8 and 9 below are
shared; item 3 and item 7 differ, and the difference is the whole point:

| | Weekly — semaglutide, dulaglutide | Daily — liraglutide |
|---|---|---|
| **3. Timing** | States the **day of the week** they will take it, and that it is the same day each week | States the **time of day** they will take it, and that it is about the same time each day |
| **7. Missed dose** | If remembered within 5 days, takes it and keeps the usual day; if longer, skips it and resumes on the usual day. **Never two doses to catch up** | Takes the next dose at the usual time. **Never two doses in one day, and no double dose to catch up** |

Everything below is the shared list, written for the weekly case; substitute the two rows above
for a daily agent.

1. Checks expiry and that the solution is clear and colourless
2. Prepares the device and attaches a needle where the device needs one
3. **States the day of the week they will take it, and that it is the same day each week**
4. Chooses and rotates the site
5. Holds for the count the device requires before withdrawing
6. **States that nausea in the first weeks is expected and usually settles, and that they should
   not stop without telling us** — the commonest reason a patient silently abandons this class
7. States what to do about a missed dose rather than doubling up
8. States correct storage
9. Disposes of the needle safely

## 7. Prior compliance, asked without inviting a lie

Also captured here, and the wording matters as much as it does in Part one. "Did you take your
medicine every day?" is a question with one socially acceptable answer.

> **English:** "Most people miss a dose sometimes. In the last week, how many times did you miss?"
>
> **Bangla:** "প্রায় সবারই কোনো না কোনো দিন ওষুধ বাদ পড়ে। গত এক সপ্তাহে আপনার কতবার বাদ পড়েছে?"

The preamble is not politeness. It tells the patient that missing doses is normal and expected,
which is what makes the true number sayable. Then, if any were missed, the reason — cost, forgot,
side effects, ran out, felt well enough to stop, could not get to the clinic — as a coded list,
because the fix for each is different and the distinction is invisible in a single adherence
percentage.

## 8. What is deliberately not built

Video content, and any scoring that rolls these items into a single competency percentage. A
percentage would be easier to plot and would lose the only thing this station produces that nobody
else can: which specific step this specific patient got wrong, in front of somebody, today.
