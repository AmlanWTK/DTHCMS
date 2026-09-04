# Paediatric growth

CP47 and CP48. [R-06], D-21, ADR-0026.

Height, weight and BMI percentiles from a child's **exact age in days**, and the card and
chart that make them legible. The obesity flag at the 95th percentile is a ratified
requirement; everything here exists to make that flag mean something.

---

## The reference, and why it is two references

**D-21, resolved:** WHO Child Growth Standards below 5.0 years, CDC 2000 from 5.0 years.

The short reason is [R-06] itself. It flags childhood obesity at the **95th percentile**,
which is the CDC convention; WHO 2007 defines obesity at >+2 SD ≈ the 97.7th. Choosing WHO
throughout would not have relabelled the threshold — it would have changed which children are
flagged, and narrowing a ratified safety rule as a side effect of picking a chart is not a
decision anybody should make quietly.

Two more reasons carried weight. CDC's BMI curves are built to meet the adult cut-points of
25 and 30 at twenty years, so an adolescent carried into adult follow-up does not change
category at the boundary for no clinical reason. And CDC's percentiles have an established
severity extension — class 2 at ≥120% of the 95th, class 3 at ≥140% — which matters in a
caseload where obesity is the single largest presenting problem.

Below five, WHO is not in dispute: it is a _standard_ (how children should grow) rather than a
_reference_ (how a sample did grow), and it is what under-5 nutrition work in Bangladesh
already uses.

### The switch at 5.0 years is a discontinuity, not a rounding detail

A percentile computed under WHO and one computed under CDC **are not the same measurement**.
So:

- every stored percentile carries its standard _and version_;
- the chart draws a rule where the reference changes, labelled;
- the point on the trajectory where it happened is marked;
- nothing compares a WHO percentile with a CDC one numerically.

### The protocol is a table

`core.growth_band` — three rows per indicator. Changing the clinic's protocol is an `UPDATE`
and a recomputation over stored measurements: not a migration, not a release. That was the
explicit promise D-21 was recorded under, and it is only true because the table exists.

---

## The numbers

The **published L, M and S parameters**, unmodified — 1,452 rows seeded by migration 00030.
`tools/growth/` holds the script that produced them, so the derivation is reproducible.

```
z = ((X/M)^L − 1) / (L·S)      L ≠ 0
z = ln(X/M) / S                L = 0
percentile = Φ(z)
```

Between published ages, L, M and S are interpolated linearly — what CDC's own documentation
instructs. At a published age it is a lookup, which is what makes the validation exact.

### The validation set is the publishers' own arithmetic

Both WHO and CDC print, beside their parameters, the cut-offs those parameters produce.
**All of them** — roughly twelve thousand printed values across 1,452 age points — are in
`packages/clinical-calc/fixtures/growth-reference.json`, and the Go suite recomputes every one
from the seeded rows at the precision it was printed to.

Nobody on this project wrote those expected numbers. A parameter transcribed wrongly fails the
build rather than agreeing with itself, which is the only check worth having over a table this
size.

### Exact age, in days

A percentile at "four years old" is not a number: for the same height, 4y 0m and 4y 11m are
roughly the 40th and the 25th percentile. Age is whole days from the validated date of birth,
converted to months as days ÷ 30.4375 — the conversion both standards specify. This is why DOB
validation is mandatory at registration.

### What it refuses

An age outside the reference returns **not applicable**, never an extrapolated number. A
percentile for a 25-year-old read off the end of a paediatric chart looks like every other
number on the screen and means nothing.

One deliberate exception, `edgeToleranceMonths`: WHO's last published row is at 60.0 months
and CDC's first is at 60.5, so a child in that fortnight is inside the CDC band and outside
the CDC table. They are scored against CDC's first published row. Moving the handover to 60.5
would contradict a recorded clinical decision to save two weeks; refusing would tell a parent
their five-year-old cannot be plotted.

---

## The card and the chart

### What a clinician reads first

The **flag**, in words, on a coloured ground — never colour alone. Then the three percentiles,
each with its z-score beneath.

Both, because they answer different questions. A percentile is what a parent understands; a
z-score is what a change over time is measured in. Past about the 99th percentile the
percentile stops discriminating while the z-score keeps going — and that is exactly the range
where a child is most unwell, so the card stops quoting percentiles there and lets the z-score
carry the precision.

### The chart is hand-drawn SVG

No charting library, and not out of asceticism. A growth chart is not a generic line chart,
and the three things that make it readable are the three a library makes hard:

1. the **95th percentile** has to be visually distinct — it is dashed as well as tinted, so
   the distinction survives a monochrome print;
2. the **join between two standards** has to be drawn;
3. the whole thing has to **print**, on a page a parent takes home.

Fifty lines of SVG does all three and adds nothing to the bundle.

The reference band is a frame — thin, quiet, low contrast. The patient's trajectory is the
heaviest thing on the chart and last in the DOM, so it paints on top. The failure mode of a
growth chart is a clinician reading the wrong line.

### No curve on the phone

The station app shows the card and a **position strip** — the reference band as a bar, the
child's percentile as a marker, the 95th as a fixed tick. Not a curve.

Two reasons, and the second is the real one. CP48 is explicitly "no new dependencies", and
curves in React Native mean `react-native-svg` and an APK rebuild. But a growth curve at five
inches and arm's length is unreadable anyway, and what the operator needs the instant they
save a child's measurements is not a trajectory — it is _which band, and is it flagged_. The
full chart lives on the physician's desktop, where it is read.

---

## Still owed

- **Dr. Nahid to check ten paediatric cases against the printed charts he uses today.**
  CP47 is not complete without this. No test can stand in for it: what it verifies is that the
  right standard was chosen for _this_ clinic, which is a clinical question.
- The paediatric age cut-off for showing the card at all (clinical confirmation).
- D-21's own caveat: neither reference is derived from South Asian children, and both
  under-call adiposity-related metabolic risk at a given BMI in this population. Where a waist
  circumference is recorded, CP58's risk scoring should evaluate it against South Asian action
  points as an **additional** signal — never as a replacement for the chosen reference.
