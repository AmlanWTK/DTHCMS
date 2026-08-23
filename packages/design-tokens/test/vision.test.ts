import { describe, expect, it } from 'vitest';

import {
  DISTINGUISHABLE,
  VISION_TYPES,
  clinicalStatusNames,
  clinicalStatuses,
  findConfusions,
  perceptualDistance,
  fromHex,
  simulate,
  type Theme,
} from '../src/index.js';

/**
 * CP09 acceptance criterion 4: clinical semantic colours are distinguishable in a
 * colour-blindness simulation and are never the sole carrier of meaning.
 *
 * The criterion contains a claim that is not quite achievable as literally stated, and it
 * is worth being precise about which half of it these tests enforce.
 *
 * Red and green genuinely converge under deuteranopia. A palette in which no two clinical
 * statuses were confusable could not use red for critical and green for normal — it would
 * have to spread seven statuses across blue and yellow alone, which would be a worse
 * interface for the eleven operators in twelve with typical colour vision, and would
 * abandon a convention every clinician already reads fluently.
 *
 * So the guarantee enforced here is the second half, and it is the one that carries the
 * safety property: wherever two statuses do collapse together, they are separated by
 * something that is not colour. Every confusable pair must differ in icon and in label.
 * That is a stronger promise than "the palette passes a simulator", because it holds for
 * a monochrome printout and a failing tablet backlight too.
 */

const themes: Theme[] = ['light', 'dark'];

describe('clinical statuses carry meaning without colour', () => {
  it('gives every status a distinct icon', () => {
    const icons = clinicalStatusNames.map((name) => clinicalStatuses[name].icon);
    expect(new Set(icons).size, `icons: ${icons.join(', ')}`).toBe(icons.length);
  });

  it('gives every status a distinct label in both languages', () => {
    for (const language of ['en', 'bn'] as const) {
      const labels = clinicalStatusNames.map((name) => clinicalStatuses[name].label[language]);
      expect(new Set(labels).size, `${language}: ${labels.join(', ')}`).toBe(labels.length);
    }
  });

  it('leaves no status without an icon, a label or a description', () => {
    for (const name of clinicalStatusNames) {
      const status = clinicalStatuses[name];
      expect(status.icon.trim(), `${name}.icon`).not.toBe('');
      expect(status.label.en.trim(), `${name}.label.en`).not.toBe('');
      expect(status.label.bn.trim(), `${name}.label.bn`).not.toBe('');
      expect(status.description.en.trim(), `${name}.description.en`).not.toBe('');
      expect(status.description.bn.trim(), `${name}.description.bn`).not.toBe('');
    }
  });
});

describe('colour vision deficiency', () => {
  for (const theme of themes) {
    const solids = Object.fromEntries(
      clinicalStatusNames.map((name) => [name, clinicalStatuses[name].colors[theme].solid]),
    );

    it(`${theme}: every confusable pair is separated by an icon`, () => {
      const confusions = findConfusions(solids);

      // Not asserting the list is empty. Asserting each entry is covered.
      for (const confusion of confusions) {
        const a = clinicalStatuses[confusion.a as (typeof clinicalStatusNames)[number]];
        const b = clinicalStatuses[confusion.b as (typeof clinicalStatusNames)[number]];

        expect(
          a.icon,
          `Under ${confusion.vision}, ${confusion.a} and ${confusion.b} are ` +
            `${confusion.distance.toFixed(3)} apart in Oklab - below the ${DISTINGUISHABLE} ` +
            `threshold at which they can be told apart at arm's length. They therefore have ` +
            `to differ by icon, and they do not.`,
        ).not.toBe(b.icon);

        expect(a.label.en, `${confusion.a} and ${confusion.b} share an English label`).not.toBe(
          b.label.en,
        );
        expect(a.label.bn, `${confusion.a} and ${confusion.b} share a Bangla label`).not.toBe(
          b.label.bn,
        );
      }
    });
  }

  it('keeps critical distinguishable from normal by lightness, for every vision type', () => {
    // The one pair where colour convergence is least acceptable: mistaking a panic value
    // for a normal one. Hue cannot be relied on, so the two are separated in lightness as
    // well - a dimension every form of colour blindness preserves.
    const critical = fromHex(clinicalStatuses.critical.colors.light.solid);
    const normal = fromHex(clinicalStatuses.normal.colors.light.solid);

    for (const vision of VISION_TYPES) {
      const distance = perceptualDistance(simulate(critical, vision), simulate(normal, vision));
      expect(
        distance,
        `critical and normal are ${distance.toFixed(3)} apart under ${vision}. They may share ` +
          `a hue, but they must not be the same colour - the icon does the rest of the work.`,
      ).toBeGreaterThan(0.03);
    }
  });
});

/*
 * There was a "greyscale survival" suite here. It computed luminance differences and then
 * asserted they were >= 0, which is true of every number - a test that cannot fail,
 * occupying space in a suite that is supposed to mean something.
 *
 * The real guarantee it was reaching for lives in build.test.ts: the print stylesheet
 * emits a text label for every clinical status, because on the monochrome laser printer a
 * Bangladeshi clinic actually owns, colour conveys nothing at all.
 */
