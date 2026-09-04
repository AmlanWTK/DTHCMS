import { readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { DualUnitValue } from '@/features/observations';

import { renderWithProviders } from './render';

/**
 * Dual-unit display (CP44, [R-08], blueprint §2).
 *
 * Two halves. The first is what the component renders: the clinical unit with the
 * patient-familiar equivalent beneath, in both languages, with the documented rounding.
 *
 * The second is the one the plan asks for as "a lint rule or review checklist item that raw
 * clinical values are not rendered directly" — implemented as a test rather than a checklist,
 * because a checklist item is followed on nine screens and forgotten on the tenth. It scans
 * the application for a screen that renders an observation's value without going through
 * this component, and fails the build.
 */

describe('the component', () => {
  it('shows height in centimetres with feet and inches beneath', () => {
    renderWithProviders(<DualUnitValue value={168} unit="cm" code="BODY_HEIGHT" label="Height" />);

    expect(screen.getByText('168.0')).toBeInTheDocument();
    expect(screen.getByText('cm')).toBeInTheDocument();
    expect(screen.getByTestId('dual-unit-secondary')).toHaveTextContent('5′6″');
  });

  it('shows weight in kilograms with pounds beneath', () => {
    renderWithProviders(<DualUnitValue value={69.5} unit="kg" />);

    expect(screen.getByText('69.5')).toBeInTheDocument();
    expect(screen.getByTestId('dual-unit-secondary')).toHaveTextContent('153.2 lb');
  });

  it('shows a waist in plain inches, not in feet', () => {
    // 94 cm is "37 inches" to everybody who has ever bought trousers. 3′1″ is
    // arithmetically correct and clinically absurd.
    renderWithProviders(<DualUnitValue value={94} unit="cm" code="WAIST_CIRC" label="Waist" />);
    expect(screen.getByTestId('dual-unit-secondary')).toHaveTextContent('37');
    expect(screen.getByTestId('dual-unit-secondary')).not.toHaveTextContent('′');
  });

  it('shows a blood pressure alone, because nobody here reads kilopascals', () => {
    // The absence is deliberate. "128 mmHg / 17.1 kPa" would be noise on the one reading
    // nobody in this clinic reads in the second unit.
    renderWithProviders(<DualUnitValue value={128} unit="mm[Hg]" />);

    expect(screen.getByText('128')).toBeInTheDocument();
    expect(screen.queryByTestId('dual-unit-secondary')).toBeNull();
  });

  it('says a value is not recorded rather than drawing a dash', () => {
    // "Not recorded" and "zero" are different facts, and a dash that could be either is a
    // dash a clinician has to go and check.
    renderWithProviders(<DualUnitValue value={null} unit="kg" label="Weight" />);

    expect(screen.getByText('Not recorded')).toBeInTheDocument();
    expect(screen.queryByTestId('dual-unit-secondary')).toBeNull();
  });

  it('renders an unknown unit rather than a blank', () => {
    // A screen that showed nothing because a unit was added to the registry and not to the
    // display table would be a screen hiding a clinical value.
    renderWithProviders(<DualUnitValue value={42} unit="not-a-unit" />);
    expect(screen.getByText('42.0')).toBeInTheDocument();
  });

  it('writes the units in Bangla', () => {
    renderWithProviders(<DualUnitValue value={69.5} unit="kg" />, { locale: 'bn' });

    expect(screen.getByText('কেজি')).toBeInTheDocument();
    expect(screen.getByTestId('dual-unit-secondary')).toHaveTextContent('পাউন্ড');
  });

  it('keeps the secondary quieter than the primary at every size', () => {
    // Not a per-screen decision: an equivalent that competed with the clinical value would
    // be a screen where somebody reads the wrong number on a busy morning.
    const css = readFileSync(join(webRoot, 'src', 'styles', 'globals.css'), 'utf8');
    expect(css).toMatch(/\.app-dual__secondary\s*\{[^}]*--text-xs/);
    expect(css).toMatch(/\.app-dual__number\s*\{[^}]*--text-lg/);
  });
});

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..');

/** Every .tsx under src, so the scan cannot miss a screen somebody added. */
function componentFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry);
    if (statSync(path).isDirectory()) {
      out.push(...componentFiles(path));
      continue;
    }
    if (entry.endsWith('.tsx')) out.push(path);
  }
  return out;
}

describe('no screen renders a clinical value directly', () => {
  // The rule: a component that touches an observation's `value`, `entered_value` or `unit`
  // inside JSX must go through DualUnitValue. [R-08] is a non-negotiable, and the way a
  // non-negotiable stops being negotiated is that a build fails.
  const files = componentFiles(join(webRoot, 'src'));

  it('scans the whole application', () => {
    expect(files.length).toBeGreaterThan(20);
  });

  for (const file of files) {
    const source = readFileSync(file, 'utf8');
    const relative = file.slice(webRoot.length + 1);

    // The component itself is where the rendering happens, by design.
    if (relative.includes('DualUnitValue')) continue;

    // A JSX interpolation of an observation field. Narrow on purpose: `{x.value}` in a
    // select option is not a clinical value, so the match requires the observation-shaped
    // field names the API actually returns.
    const renders = /\{[^}]*\.(entered_value|value_num)\b[^}]*\}/.test(source);
    if (!renders) continue;

    it(`${relative} uses DualUnitValue`, () => {
      expect(
        source.includes('DualUnitValue'),
        `${relative} renders an observation value directly. [R-08] says every clinical ` +
          `value shows the clinical unit with the patient-familiar equivalent beneath it, ` +
          `and DualUnitValue is the only thing that does that consistently — including on ` +
          `the printed prescription.`,
      ).toBe(true);
    });
  }
});
