# Design system

Established at CP09. Two packages: `@dthcms/design-tokens` holds every value, `@dthcms/ui`
holds the web primitives built from them.

Full detail lives beside the code — [`packages/design-tokens/README.md`](../packages/design-tokens/README.md)
and [`packages/ui/README.md`](../packages/ui/README.md). This page is the decisions.

---

## 1. Colour is generated, not chosen

Ramps are computed in OKLCH from a hue and a chroma ceiling:

```json
"brand": { "hue": 195, "chroma": 0.11 }
```

The point is not elegance. It is that **the brand is a placeholder** — DTHC's identity is
Dr. Nahid's to decide — and a hand-picked hex ramp would have to be re-verified by eye
after every change, drifting in hue as it darkens. A generated ramp lands on the same
lightness targets whatever the hue, so the contrast assertions hold across a swap.

Building it this way caught something a hand-picked palette would have shipped. At ramp
step 600, **neither white nor near-black reached 4.5:1** against the teal — 3.80:1 and
4.30:1. Then every one of the seven clinical statuses turned out to sit in the same
lightness dead zone: 4.05:1 to 4.36:1, all just under AA, none of them visibly wrong.

That band is exactly where an accessibility failure ships. Both moved to step 700, and the
foreground is now computed rather than declared, so whichever hue is chosen cannot silently
produce an unreadable primary button.

## 2. Colour is never the only signal

Every clinical status carries a colour, an **icon**, and a **bilingual label**, and the
type makes all three mandatory. Rendering colour alone requires deliberately discarding
them; `StatusPill` has no prop for it.

The acceptance criterion asks for statuses "distinguishable in a colour-blindness
simulation". Taken literally that is not achievable and should not be attempted: under
deuteranopia red and green converge, and a palette avoiding every confusable pair could not
use red for critical and green for normal — a worse interface for the eleven operators in
twelve with typical colour vision, and an abandonment of a convention every clinician reads
fluently.

So the guarantee enforced is the second half, and it is the one carrying the safety
property: **every pair that collapses is separated by a different icon and a different
label**. That holds on a failing tablet backlight and on paper, which a simulator check
does not.

On paper it has to, because the printer in a Bangladeshi clinic is monochrome. The print
stylesheet appends the status label as text to anything marked `data-status`.

## 3. Bengali is not Latin at a different size

Bengali glyphs hang from a headstroke rather than sitting on a baseline, and conjuncts
extend well below it. At Latin leading, the matras of one line touch the descenders of the
line above — legible in a sentence, unreadable in a dense clinical table.

Every type step therefore carries a **line height per script**, and `[lang='bn']` switches
the family and the leading _together_. Setting one without the other is the specific
mistake that pairing prevents.

Inter and Noto Sans Bengali, both SIL Open Font License, **bundled rather than
CDN-loaded**. A station tablet may be offline for a whole clinic session, and a font that
fails to load falls back to whatever Android has — which for Bengali is frequently a face
with poor conjunct coverage. Text degrades into broken ligatures with nothing reporting an
error.

`clinicalValue` pins the Latin family regardless of interface language, with tabular
figures. A glucose reading must look identical in both interfaces: digits that change shape
with the language are digits somebody transcribes wrongly onto a paper chart.

## 4. Impossible is not the same as unusual

`NumericInput` takes two ranges. `possible` rejects; `plausible` warns and records anyway.

A fasting glucose of 22 mmol/L is not a typing mistake — it is a patient who needs
attention now, and an interface that refuses it loses the finding. A reading of 900 is a
slipped decimal point. Conflating the two produces either a system that discards real
findings or one that accepts nonsense, and clinical software usually picks the first
without noticing.

It also does not use `type="number"`, which reports an empty value for unparseable input —
making "12..5" and "nothing entered" the same state. For a measurement they must never be.

## 5. Open decisions

| Decision                                         | Default taken                                        | Needs                                                        |
| ------------------------------------------------ | ---------------------------------------------------- | ------------------------------------------------------------ |
| DTHC brand colour                                | Placeholder teal, hue 195                            | Dr. Nahid                                                    |
| Numerals in a Bengali interface — `7.8` or `৭.৮` | ASCII for measurements, doses, dates and identifiers | Dr. Nahid                                                    |
| Bengali clinical labels (সংকটজনক, সীমান্তবর্তী…) | My translations                                      | Dr. Nahid's review — his professional register, tied to D-24 |

The numeral question is not typographic. A reading transcribed onto a paper chart, read
back over a phone, or compared against a lab printout using ASCII digits is a place where
two numeral systems in circulation is a transcription error waiting to happen.

## 6. Departures from the implementation plan

Both are in `@dthcms/ui`, both are reversible, and both are recorded here rather than made
quietly.

**Primitives ship a token-driven stylesheet rather than Tailwind classes.** A shared
library styled with utility classes renders correctly only if every consumer runs Tailwind
with the same configuration; a stylesheet keyed to token variables works identically in
Next.js, Storybook and a jsdom test. Applications may still use Tailwind for layout — this
concerns what the primitives themselves ship.

**`Select` uses the native element rather than Radix.** On an Android tablet the native
control opens the platform picker: a large, touch-sized list the operator already knows,
wired to the system's accessibility services, at no bundle cost. Radix earns its place when
options need rich content — a drug name with strength and form on separate lines — and that
is a domain component, which can wrap Radix then.

## 7. Known gaps

| Gap                                         | Why                                                                                                                                                                         | Lands at |
| ------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------- |
| Visual regression snapshots                 | Screenshot baselines produced on one machine fail instantly on another; font rasterisation differs between Linux and Windows. They need a fixed environment, which means CI | CP03     |
| Validation on a real low-end Android device | Bengali conjunct rendering and the 48px targets are verified in a browser and in the stylesheet, not on the hardware                                                        | CP11     |
| Print layout                                | Print _variables_ exist; page furniture, margins and headers are a checkpoint of their own                                                                                  | CP89     |
