import { existsSync, readFileSync, rmSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { beforeAll, describe, expect, it } from 'vitest';

import { build } from '../src/build.js';
import {
  clinicalStatusNames,
  clinicalStatuses,
  layout,
  ramps,
  themes,
  typography,
} from '../src/index.js';

/**
 * CP09 acceptance criterion 1: one token source feeds web, mobile and print.
 *
 * The interesting question is not whether the build runs — it is whether the three
 * outputs actually agree. A pipeline that emitted web CSS from the JSON and a mobile
 * theme from a hand-maintained copy would build cleanly, pass a typecheck, and drift
 * apart over months until a button was one shade off on Android and nobody could say
 * when it started.
 *
 * So each test below takes a value from the resolved source and looks for that exact
 * value in each artefact. The only way to pass is for the artefact to have been generated
 * from the source.
 */

const packageRoot = join(dirname(fileURLToPath(import.meta.url)), '..');
const dist = join(packageRoot, 'dist');

const read = (name: string): string => readFileSync(join(dist, name), 'utf8');

let css = '';
let printCss = '';
let tailwind = '';
let nativewind = '';

beforeAll(() => {
  // Build from scratch, so the test can never pass against a stale dist — including the
  // case where the build stopped emitting a file and the old one is still sitting there.
  //
  // Called in process rather than spawned. `execFileSync('npx', ['tsx', ...])` passed on
  // Linux and failed on Windows with ENOENT, because npx there is npx.cmd and
  // execFileSync without a shell will not resolve a .cmd. A test that only runs on the
  // machine it was written on is not a test.
  rmSync(dist, { recursive: true, force: true });
  build();

  css = read('tokens.css');
  printCss = read('tokens.print.css');
  tailwind = read('tailwind-theme.js');
  nativewind = read('nativewind-theme.js');
}, 60_000);

describe('the build produces every artefact', () => {
  it('writes all five outputs', () => {
    for (const name of [
      'tokens.css',
      'tokens.print.css',
      'tailwind-theme.js',
      'nativewind-theme.js',
      'tokens.json',
    ]) {
      expect(existsSync(join(dist, name)), name).toBe(true);
    }
  });

  it('marks every output as generated', () => {
    for (const [name, contents] of [
      ['tokens.css', css],
      ['tokens.print.css', printCss],
      ['tailwind-theme.js', tailwind],
      ['nativewind-theme.js', nativewind],
    ] as const) {
      expect(contents, name).toContain('Do not edit');
    }
  });
});

describe('web, mobile and print carry the same values', () => {
  it('agrees on the brand solid colour', () => {
    const brandSolid = themes.light.brand.solid;
    expect(brandSolid).toMatch(/^#[0-9a-f]{6}$/);

    expect(css, 'web CSS').toContain(brandSolid);
    expect(tailwind, 'Tailwind theme').toContain(brandSolid);
    expect(nativewind, 'NativeWind theme').toContain(brandSolid);
  });

  it('agrees on every clinical status colour, in both themes', () => {
    for (const name of clinicalStatusNames) {
      for (const theme of ['light', 'dark'] as const) {
        const { solid, onSolid } = clinicalStatuses[name].colors[theme];

        expect(css, `${theme} ${name} solid in web CSS`).toContain(solid);
        expect(tailwind, `${theme} ${name} solid in Tailwind`).toContain(solid);
        expect(nativewind, `${theme} ${name} solid in NativeWind`).toContain(solid);
        expect(css, `${theme} ${name} onSolid in web CSS`).toContain(onSolid);
      }
    }
  });

  it('agrees on the whole neutral ramp', () => {
    for (const value of Object.values(ramps.neutral)) {
      expect(css, `${value} in web CSS`).toContain(value);
      expect(tailwind, `${value} in Tailwind`).toContain(value);
      expect(nativewind, `${value} in NativeWind`).toContain(value);
    }
  });

  it('agrees on the touch target', () => {
    // 48px in the JSON: 3rem for the web, the integer 48 for React Native, which has no
    // notion of rem.
    expect(layout.size.touchTarget).toBe(48);
    expect(css).toContain('--touch-target: 3rem');
    expect(nativewind).toContain('"touchTarget": 48');
  });

  it('agrees on the type scale', () => {
    for (const [step, spec] of Object.entries(typography.scale)) {
      if (step.startsWith('$')) continue;
      const { size } = spec as { size: number };
      expect(nativewind, `${step} size in NativeWind`).toContain(`"${step}": ${size}`);
    }
  });
});

describe('bilingual typography reaches the stylesheet', () => {
  it('emits a line height per script for every step', () => {
    for (const step of Object.keys(typography.scale)) {
      if (step.startsWith('$')) continue;
      expect(css, `${step} latin`).toContain(`--leading-${step}-latin:`);
      expect(css, `${step} bengali`).toContain(`--leading-${step}-bengali:`);
    }
  });

  it('switches font and line height together on the language attribute', () => {
    // Changing the family without the leading is the specific failure this pairing
    // prevents: Bengali matras collide with the line above at Latin leading.
    expect(css).toContain("[lang='bn']");
    expect(css).toContain('--font-ui: var(--font-bengali)');
    expect(css).toContain('--leading-ui: var(--leading-base-bengali)');
  });

  it('bundles the Bengali family rather than trusting the device', () => {
    expect(css).toContain('Noto Sans Bengali');
  });
});

describe('print carries meaning that colour cannot', () => {
  it('writes out a text label for every clinical status, in both languages', () => {
    // The guarantee that replaces "distinguishable in greyscale". On a monochrome laser
    // printer - the printer a Bangladeshi clinic actually owns - colour conveys nothing,
    // so the status has to arrive as words.
    for (const name of clinicalStatusNames) {
      const status = clinicalStatuses[name];
      expect(printCss, `${name} selector`).toContain(`[data-status='${name}']`);
      expect(printCss, `${name} English label`).toContain(status.label.en);
      expect(printCss, `${name} Bangla label`).toContain(status.label.bn);
    }
  });

  it('drops shadows and animation', () => {
    expect(printCss).toContain('box-shadow: none');
    expect(printCss).toContain('--duration-normal: 0ms');
  });

  it('keeps a clinical value from splitting across a page break', () => {
    expect(printCss).toContain('[data-clinical-value]');
    expect(printCss).toContain('break-inside: avoid');
  });
});

describe('dark mode', () => {
  it('responds to the OS preference and to an explicit override', () => {
    expect(css).toContain('@media (prefers-color-scheme: dark)');
    expect(css).toContain("[data-theme='dark']");
    // The preference block yields to an explicit light choice, so a user who has chosen
    // light in a dark-mode OS gets light.
    expect(css).toContain(":root:not([data-theme='light'])");
  });
});

describe('reduced motion', () => {
  it('collapses durations without removing transitions', () => {
    expect(css).toContain('@media (prefers-reduced-motion: reduce)');
    // 1ms, not 0: transitionend still fires, so state machines built on it do not stall
    // for exactly the users who asked for less motion.
    expect(css).toMatch(/prefers-reduced-motion[\s\S]*--duration-normal: 1ms/);
  });
});
