# `@dthcms/ui`

Eleven web primitives, built on `@dthcms/design-tokens`. Bilingual, accessible, and
free of clinical knowledge — a component here knows what a status _looks_ like; it does
not know what makes a glucose reading high.

```bash
pnpm --filter @dthcms/ui test        # 182 assertions, axe on every component in both languages
pnpm --filter @dthcms/ui storybook   # http://localhost:6006
```

```tsx
import '@dthcms/design-tokens/css';
import '@dthcms/design-tokens/print';
import '@dthcms/ui/styles.css';

import { LanguageProvider, NumericInput, StatusPill } from '@dthcms/ui';
```

---

## The components

| Component      | Notes                                                                 |
| -------------- | --------------------------------------------------------------------- |
| `Button`       | Four variants, three sizes. `loading` disables.                       |
| `Input`        | Label is a required prop, not an optional one.                        |
| `NumericInput` | Clinical values. Warns without blocking. See below.                   |
| `Select`       | Native element, deliberately.                                         |
| `Card`         | Three elevations.                                                     |
| `Badge`        | Counts and categories. No clinical tones, deliberately.               |
| `StatusPill`   | Colour **and** icon **and** word. The criterion-4 component.          |
| `AlertBanner`  | Live-region behaviour chosen per tone.                                |
| `Skeleton`     | Announces itself. A skeleton is silence to a screen reader otherwise. |
| `EmptyState`   | Nothing here — and that being correct.                                |
| `ErrorState`   | Something failed. Shows the correlation ID.                           |

## Decisions worth knowing about

### `NumericInput` does not use `type="number"`

Three reasons, each of which has caused a real incident somewhere:

- A number input reports an **empty value** when its contents are unparseable, so
  `12..5` and "nothing entered" are the same state to the application. For a clinical
  measurement they must never be.
- The **scroll wheel and arrow keys change the value** while it has focus. On a tablet, a
  stray touch on a focused field silently alters a recorded reading.
- Browsers **localise the decimal separator** inconsistently, so the same keystrokes give
  different values on different devices.

`inputMode="decimal"` still gets the numeric keypad on Android, which is the only thing
`type="number"` was wanted for.

### Impossible is not the same as unusual

`possible` rejects. `plausible` warns and records anyway.

A fasting glucose of 22 mmol/L is not a typing mistake to be corrected — it is a patient
who needs attention now, and an interface that refuses to record it loses the finding. A
reading of 900 is a slipped decimal point. Setting the two ranges apart is the whole point
of the component; set `possible` wide, because a range that rejects a real reading leaves
the operator no way forward but to record something false.

### `Select` uses the native element

**A departure from the implementation plan, which names Radix.** On an Android tablet the
native control opens the platform picker: a large, scrollable, touch-sized list the
operator already knows, wired to the system's own accessibility services, at no bundle
cost. A custom listbox on the same device is a smaller target with hand-rolled keyboard
handling.

Radix earns its place when options need rich content — a drug name with a strength and a
form on separate lines. That is a domain component, and it can wrap Radix then without
this primitive having done so first. Say the word and I will swap it.

### The stylesheet has no literals

Not one hex code, `rgb()`, or `px` value outside two unavoidable places — and
`styles.test.ts` greps for them. A hex code in a component stylesheet is a value that will
not follow the theme, will not invert for dark mode, and will not turn black for print.

**This is also a departure from the plan, which names Tailwind v4.** A shared library
styled with utility classes only renders correctly if every consumer runs Tailwind with
the same configuration; a stylesheet keyed to token variables works identically in
Next.js, in Storybook, and in a jsdom test. Applications can still use Tailwind for
layout — this is about what the primitives themselves ship.

## Accessibility

- **axe runs on every component in both languages** — 139 of the 182 assertions. The
  `color-contrast` rule is disabled in the unit run because jsdom has no layout engine and
  reports "cannot tell", which looks like coverage and is not. Contrast is checked far
  more thoroughly in `@dthcms/design-tokens`, and axe checks it for real in Storybook,
  where a browser is present.
- **Touch targets** are asserted from the stylesheet: `md` and `lg` buttons and every text
  control are at least 48px. `sm` deliberately is not, and is documented as pointer-only.
- **Focus** uses `:focus-visible`, so a ring appears for keyboard navigation but does not
  linger after a touch, where it reads as "still selected".
- **Reduced motion** stops looping animations outright. Collapsing the duration is not
  enough: a spinner completing a revolution every millisecond is worse than one that does
  not spin.

## Storybook

Theme and language are **toolbar globals**, not story arguments. Every story therefore
renders in four combinations, and a reviewer flips two switches rather than relying on
each story author to write four variants.

`@storybook/addon-a11y` is configured with `test: 'error'` — a story with a violation
fails rather than being annotated. An accessibility panel nobody opens is a check nobody
runs.
