import { fromHex, linearize, type RGB } from './space';

/**
 * WCAG 2.1 contrast, and the thresholds DTHCMS holds itself to.
 *
 * The blueprint (§12) requires a modern, accessible interface. In a clinic that is not an
 * abstraction: screens are read at arm's length on tablets under fluorescent light, often
 * by someone in a hurry, sometimes by a physician in their fifties whose near vision has
 * changed. Contrast is the difference between a value being read and being guessed.
 */

/** Relative luminance, WCAG 2.1 §Definitions. */
export function relativeLuminance(color: RGB): number {
  const { r, g, b } = linearize(color);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

/** Contrast ratio between two colours, 1 (identical) to 21 (black on white). */
export function contrastRatio(a: RGB, b: RGB): number {
  const first = relativeLuminance(a);
  const second = relativeLuminance(b);
  const lighter = Math.max(first, second);
  const darker = Math.min(first, second);
  return (lighter + 0.05) / (darker + 0.05);
}

export function contrastHex(a: string, b: string): number {
  return contrastRatio(fromHex(a), fromHex(b));
}

/**
 * The thresholds. AA, not AAA.
 *
 * AAA (7:1) sounds better and is the wrong target here: meeting it across a full clinical
 * palette forces colours so dark that the semantic distinctions between normal,
 * borderline and high compress into near-black, and the interface loses the information
 * the contrast was protecting. AA with every status also carrying an icon and a label is
 * the stronger guarantee.
 */
export const CONTRAST = {
  /** Body text, and any text below 18.66px regular or 14px bold. */
  text: 4.5,
  /** Large text: 18.66px+ regular, or 14px+ bold. */
  largeText: 3,
  /** Borders, focus rings, icons — anything whose shape carries meaning. */
  nonText: 3,
  /**
   * Clinical values specifically. A lab result, a blood pressure, a dose.
   *
   * Held above the AA floor deliberately: these are the characters a misreading of which
   * changes a clinical decision, and they are frequently small, dense and numeric.
   */
  clinicalValue: 7,
} as const;

export type ContrastRequirement = keyof typeof CONTRAST;

export interface ContrastCheck {
  pass: boolean;
  ratio: number;
  required: number;
}

export function checkContrast(
  foreground: string,
  background: string,
  requirement: ContrastRequirement = 'text',
): ContrastCheck {
  const ratio = contrastHex(foreground, background);
  const required = CONTRAST[requirement];
  return { pass: ratio >= required, ratio, required };
}

/** Formats a ratio the way accessibility reports do: 4.53:1. */
export function formatRatio(ratio: number): string {
  return `${ratio.toFixed(2)}:1`;
}

/**
 * Picks whichever candidate foreground has more contrast against a background.
 *
 * This exists because of the brand placeholder. `text.onBrand` was originally white,
 * which failed at 3.80:1 against the teal — and worse, would have failed differently for
 * whatever hue Dr. Nahid eventually chooses. A yellow brand needs dark text; a navy one
 * needs white; a mid-tone teal is close enough to the boundary that eyeballing it is
 * guesswork.
 *
 * Computing the foreground means "changing the brand is one number" stays true, rather
 * than being true until the first time someone picks a light hue and ships an
 * unreadable primary button.
 */
export function bestForeground(background: string, candidates: readonly string[]): string {
  let best = candidates[0];
  if (best === undefined) {
    throw new Error('bestForeground needs at least one candidate');
  }

  let bestRatio = contrastHex(best, background);
  for (const candidate of candidates.slice(1)) {
    const ratio = contrastHex(candidate, background);
    if (ratio > bestRatio) {
      best = candidate;
      bestRatio = ratio;
    }
  }
  return best;
}
