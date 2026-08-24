import { describe, expect, it } from 'vitest';

import semantic from '../src/tokens/semantic.json' with { type: 'json' };
import {
  CONTRAST,
  clinicalStatusNames,
  clinicalStatuses,
  contrastHex,
  formatRatio,
  themesByName,
  type ContrastRequirement,
  type Theme,
} from '../src/index';

/**
 * CP09 acceptance criterion 3: all text meets at least 4.5:1, verified automatically.
 *
 * The list of pairs comes from the contract in semantic.json rather than from this file.
 * That matters: a test that chooses its own pairs will, over time, choose the ones that
 * pass. Keeping the list beside the tokens means adding a role without classifying it is
 * visible in the diff of the token file, where a designer will see it, rather than buried
 * in a test nobody opens.
 */

const themeNames: Theme[] = ['light', 'dark'];

function roleValue(theme: Theme, reference: string): string {
  const [group, role] = reference.split('.');
  const value = themesByName[theme][group ?? '']?.[role ?? ''];
  if (value === undefined) {
    throw new Error(`the contract names ${reference}, which is not a role in the ${theme} theme`);
  }
  return value;
}

describe('contrast contract', () => {
  for (const theme of themeNames) {
    describe(theme, () => {
      for (const entry of semantic.contract.required) {
        const { fg, bg, level, why } = entry as {
          fg: string;
          bg: string;
          level: ContrastRequirement;
          why: string;
        };

        it(`${fg} on ${bg} meets ${CONTRAST[level]}:1`, () => {
          const foreground = roleValue(theme, fg);
          const background = roleValue(theme, bg);
          const ratio = contrastHex(foreground, background);

          expect(
            ratio,
            `${theme}: ${fg} (${foreground}) on ${bg} (${background}) is ${formatRatio(ratio)}, ` +
              `below the required ${CONTRAST[level]}:1.\n  Why this pair is required: ${why}`,
          ).toBeGreaterThanOrEqual(CONTRAST[level]);
        });
      }
    });
  }
});

describe('clinical status contrast', () => {
  for (const theme of themeNames) {
    for (const name of clinicalStatusNames) {
      const status = clinicalStatuses[name];

      for (const entry of semantic.contract.statusRequired) {
        const { fg, bg, level, why } = entry as {
          fg: string;
          bg: string;
          level: ContrastRequirement;
          why: string;
        };

        it(`${theme} ${name}: ${fg} on ${bg} meets ${CONTRAST[level]}:1`, () => {
          const foreground = status.colors[theme][fg as keyof typeof status.colors.light];
          // A background beginning with @ is a page role rather than one of the status's
          // own colours - status text used inline rather than inside a chip.
          const background = bg.startsWith('@')
            ? roleValue(theme, bg.slice(1))
            : status.colors[theme][bg as keyof typeof status.colors.light];

          const ratio = contrastHex(foreground, background);

          expect(
            ratio,
            `${theme} ${name}: ${fg} (${foreground}) on ${bg} (${background}) is ` +
              `${formatRatio(ratio)}, below ${CONTRAST[level]}:1.\n  Why: ${why}`,
          ).toBeGreaterThanOrEqual(CONTRAST[level]);
        });
      }
    }
  }
});

describe('the contract itself', () => {
  it('gives a reason for every requirement', () => {
    // Terse is fine here - several entries legitimately read "as above, on a card". The
    // bar is deliberately low for requirements and high for exemptions below, because
    // requirements do not erode: nobody weakens a standard by adding one.
    for (const entry of semantic.contract.required) {
      expect((entry as { why: string }).why.trim(), `${entry.fg} on ${entry.bg}`).not.toBe('');
    }
  });

  it('gives a reason for every exemption', () => {
    // An exemption with no stated reason is how an accessibility standard erodes: the
    // first one is justified, the tenth is copied from the ninth.
    for (const entry of semantic.contract.exempt) {
      expect((entry as { why: string }).why.length, entry.pair).toBeGreaterThan(40);
    }
  });

  it('names only roles that exist', () => {
    for (const theme of themeNames) {
      for (const entry of semantic.contract.required) {
        expect(() => roleValue(theme, entry.fg)).not.toThrow();
        expect(() => roleValue(theme, entry.bg)).not.toThrow();
      }
    }
  });
});
