# `@dthcms/design-tokens`

One token source. Web CSS variables, a NativeWind theme for the station apps, and a print
stylesheet — all generated from the JSON files in `src/tokens/`.

```bash
pnpm --filter @dthcms/design-tokens build   # regenerate dist/
pnpm --filter @dthcms/design-tokens test    # 148 assertions, including every contrast pair
```

`dist/` is generated and not committed. An output that is both generated and checked in
gets edited by hand one afternoon, and nothing notices.

---

## Where a value lives

| File                     | Holds                                                         |
| ------------------------ | ------------------------------------------------------------- |
| `tokens/color.json`      | Brand and clinical hues, and the ramp geometry                |
| `tokens/semantic.json`   | Roles per theme, clinical statuses, the **contrast contract** |
| `tokens/typography.json` | Families, the bilingual scale, numeral policy                 |
| `tokens/layout.json`     | Spacing, radius, sizes, breakpoints                           |
| `tokens/motion.json`     | Durations, easings, reduced-motion behaviour                  |
| `tokens/elevation.json`  | Shadows per theme, Android elevation numbers                  |

Nothing else writes a value down. A hex code in a component or a spacing number in a
stylesheet is visible in review as something that did not come from here.

## Colours are generated, not picked

Ramps come from a hue and a chroma ceiling:

```json
"brand": { "hue": 195, "chroma": 0.11 }
```

Eleven steps are computed in OKLCH against fixed lightness targets. This is what makes
"changing the brand is one number" true rather than aspirational — a hand-picked ramp
drifts in hue as it darkens and has to be re-verified by eye after every change.

**The brand is a placeholder** pending DTHC's identity from Dr. Nahid. Change `hue`, run
the tests, and the contrast contract re-verifies every pair.

Two things are computed rather than declared, both because of that placeholder:

- **`text.onBrand`** picks whichever of white or near-black has more contrast against the
  brand. A yellow brand needs dark text and a navy one needs white; hardcoding either
  would break silently on whatever hue is chosen.
- **`onSolid`** does the same per clinical status, because amber and red need opposite
  answers.

Even so, one thing still needs a human: which ramp step is `solid`. At the placeholder
hue, step 600 sat in a lightness dead zone where _neither_ foreground reached 4.5:1 —
white gave 3.80:1, near-black 4.30:1. Step 700 gives 5.52:1. The dead zone moves with the
hue, so the contract re-checks it and the test suite fails with a message naming the line
to change rather than shipping an unreadable button.

## The contrast contract

`semantic.json` lists every pair that must meet a threshold, with a reason, and every pair
that is deliberately exempt, with a reason. The tests iterate that list; they do not
invent their own.

That structure is the point. A check that picks its own pairs will, over time, pick the
ones that pass. A check that demands 3:1 of every border — including a table rule whose
removal loses no information — fails honestly and uselessly, and gets weakened until it
passes. Every exemption here states what would be lost if the element vanished, which is
the actual WCAG test for whether it carries meaning.

Clinical values are held to **7:1**, above the AA floor, because they are the characters
whose misreading changes a decision.

## Colour is never the only signal

Every clinical status carries an icon and a bilingual label, and the type makes all three
mandatory. Rendering colour alone requires deliberately discarding them.

Under deuteranopia, red and green genuinely converge — several status pairs fall below the
distinguishability threshold, and no palette using red for critical and green for normal
can avoid that. So the test does not assert the palette passes a simulator. It asserts
that **every pair which collapses is separated by a different icon and a different label**.
That holds on a failing tablet backlight and on paper, which a simulator check does not.

Print goes further: `tokens.print.css` appends the status label as text to anything marked
`data-status`, because the printer in a Bangladeshi clinic is monochrome and colour arrives
carrying nothing.

## Bilingual type

Bengali is not Latin at a different size. Glyphs hang from a headstroke rather than sitting
on a baseline, and vowel signs and conjuncts extend well above and below the x-height. At
Latin leading, the matras of one line touch the descenders of the line above — legible in a
sentence, unreadable in a dense clinical table.

So every step carries a line height per script, and `[lang='bn']` switches the family and
the leading **together**. Setting one without the other is the specific mistake that
pairing prevents.

Both families are SIL Open Font License — Inter and Noto Sans Bengali — and are bundled
rather than loaded from a CDN. A station tablet may be offline for a whole clinic session,
and a font that fails to load falls back to whatever Android has, which for Bengali is
frequently a face with poor conjunct coverage. Text degrades into broken ligatures without
anything reporting an error.

`clinicalValue` pins the Latin family regardless of interface language, with tabular
figures. A glucose reading must look identical in both interfaces: digits that change shape
with the language are digits somebody transcribes wrongly onto a paper chart.

### Open decision: numerals

Bengali has its own digits (০১২৩৪৫৬৭৮৯), normal in Bengali prose. Whether a clinical value
in a Bengali interface shows `7.8` or `৭.৮` is not a typographic preference — a reading
transcribed onto a paper chart, read back over a phone, or compared against a lab printout
using ASCII digits is a place where two numeral systems in circulation is a transcription
error waiting to happen.

Defaulted to ASCII for anything that is a measurement, dose, date or identifier. **Needs
Dr. Nahid's confirmation.**

## Consuming it

```ts
import { clinicalStatuses, resolveTypeRole, themes } from '@dthcms/design-tokens';
```

```css
@import '@dthcms/design-tokens/css';
@import '@dthcms/design-tokens/print';

.value {
  color: var(--color-text-primary);
  font-size: var(--text-2xl);
  line-height: var(--leading-ui);
}
```

Components use semantic roles (`--color-surface-raised`), never ramp steps
(`--ramp-neutral-100`). The ramps are exported for one-off needs, and reaching for one is a
signal that a role is missing.
