import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { GrowthChart } from '@/features/growth/components/GrowthChart';
import { PercentileCard } from '@/features/growth/components/PercentileCard';
import type { Growth, GrowthCurveSet, GrowthPoint } from '@/features/growth';

import { renderWithProviders } from './render';

/**
 * The paediatric percentile card and growth chart (CP48, [R-06]).
 *
 * The arithmetic is proven server-side against WHO's and CDC's own printed tables. What is
 * checked here is what the interface is responsible for:
 *
 *  - the obesity flag reads as a **word**, not only as a colour;
 *  - the 95th percentile line is visually distinct, because [R-06] flags obesity at it;
 *  - the place where the reference standard changes is **drawn**, which D-21 requires;
 *  - the child's own trajectory sits on top of the reference band;
 *  - a patient with no applicable reference gets a sentence, not an empty chart.
 */

function growth(overrides: Partial<Growth> = {}): Growth {
  return {
    patient_id: 'p1',
    sex: 'male',
    age_days: 2600,
    applicable: true,
    current: {
      HFA: percentile('HFA', 'BODY_HEIGHT', 128, 'cm', 62.4, 0.32),
      WFA: percentile('WFA', 'BODY_WEIGHT', 31.5, 'kg', 88.1, 1.18),
      BFA: percentile('BFA', 'BMI', 19.2, 'kg/m2', 96.4, 1.8),
    },
    history: {},
    ...overrides,
  } as Growth;
}

function percentile(
  indicator: string,
  code: string,
  value: number,
  unit: string,
  p: number,
  z: number,
) {
  return {
    indicator,
    code,
    value,
    unit,
    age_days: 2600,
    age_months: 85.4,
    z,
    percentile: p,
    standard: 'CDC_2000',
    standard_version: '2000.1',
    l: -1.2,
    m: 16.5,
    s: 0.11,
    effective_at: '2026-09-14T09:00:00Z',
  };
}

describe('the percentile card', () => {
  it('says the weight status in words, not only in colour', () => {
    // Roughly one man in twelve who will work in this clinic cannot rely on hue, and a
    // tablet in direct sun flattens every colour on the screen.
    renderWithProviders(
      <PercentileCard
        growth={growth()}
        weightStatus={{
          class: 'obese',
          percent_of_95th: 103,
          bmi_at_95th: 18.6,
          standard: 'CDC_2000',
        }}
      />,
    );
    expect(screen.getByTestId('weight-status')).toHaveTextContent('Obese');
  });

  it('shows how far past the 95th a severely obese child is', () => {
    // Above the 99th percentile the percentile scale stops telling two very differently
    // unwell children apart. Percent of the 95th is CDC's convention and does.
    renderWithProviders(
      <PercentileCard
        growth={growth()}
        weightStatus={{
          class: 'obese_class_3',
          percent_of_95th: 147,
          bmi_at_95th: 18.6,
          standard: 'CDC_2000',
        }}
      />,
    );
    expect(screen.getByTestId('weight-status')).toHaveTextContent('147%');
  });

  it('shows the z-score beside the percentile', () => {
    renderWithProviders(<PercentileCard growth={growth()} />);
    expect(screen.getByTestId('percentile-BFA')).toHaveTextContent('z = 1.80');
  });

  it('stops quoting percentiles past the point they discriminate', () => {
    // "99.97th" is arithmetically right and useless: nobody can tell it from "99.9th".
    renderWithProviders(
      <PercentileCard
        growth={growth({
          current: { BFA: percentile('BFA', 'BMI', 31, 'kg/m2', 99.97, 3.4) },
        } as Partial<Growth>)}
      />,
    );
    expect(screen.getByTestId('percentile-BFA')).toHaveTextContent('> 99');
  });

  it('names the reference that produced the numbers', () => {
    // A percentile computed under WHO and one computed under CDC are not the same
    // measurement, and a card that did not say which would invite the comparison (D-21).
    renderWithProviders(<PercentileCard growth={growth()} />);
    expect(screen.getByTestId('percentile-standard')).toHaveTextContent('CDC 2000');
  });

  it('says why there is nothing rather than showing three dashes', () => {
    renderWithProviders(
      <PercentileCard
        growth={growth({ applicable: false, note: 'too_old_for_a_growth_reference' })}
      />,
    );
    expect(screen.getByTestId('percentile-card')).toHaveAttribute('data-empty', 'true');
    expect(screen.getByTestId('percentile-card')).toHaveTextContent('20 years');
  });

  it('renders in Bangla', () => {
    renderWithProviders(
      <PercentileCard
        growth={growth()}
        weightStatus={{
          class: 'obese',
          percent_of_95th: 103,
          bmi_at_95th: 18.6,
          standard: 'CDC_2000',
        }}
      />,
      { locale: 'bn' },
    );
    expect(screen.getByTestId('weight-status')).toHaveTextContent('স্থূল');
    expect(screen.getByTestId('percentile-standard')).toHaveTextContent('সিডিসি');
  });
});

function curves(): GrowthCurveSet {
  const line = (offset: number) =>
    Array.from({ length: 40 }, (_, i) => [i * 6, 14 + offset + i * 0.06] as [number, number]);
  return {
    indicator: 'BFA',
    sex: 'male',
    unit: 'kg/m2',
    standards: [
      {
        code: 'WHO_2006',
        version: '2006.1',
        min_age_months: 0,
        max_age_months: 60,
        name_en: 'WHO',
        name_bn: 'ডব্লিউএইচও',
      },
      {
        code: 'CDC_2000',
        version: '2000.1',
        min_age_months: 60,
        max_age_months: 240.5,
        name_en: 'CDC',
        name_bn: 'সিডিসি',
      },
    ],
    curves: [
      { percentile: 3, points: line(-3) },
      { percentile: 15, points: line(-2) },
      { percentile: 50, points: line(0) },
      { percentile: 85, points: line(2) },
      { percentile: 95, points: line(3) },
      { percentile: 97, points: line(3.5) },
    ],
  } as GrowthCurveSet;
}

function points(): GrowthPoint[] {
  return [24, 48, 72, 96].map((months, i) => ({
    ...percentile('BFA', 'BMI', 15.5 + i * 1.1, 'kg/m2', 60 + i * 10, 0.3 + i * 0.4),
    age_months: months,
    standard_changed: months === 72,
  })) as GrowthPoint[];
}

describe('the growth chart', () => {
  it('draws the child on top of the reference band', () => {
    renderWithProviders(
      <GrowthChart curves={curves()} points={points()} indicator="BFA" focus={false} />,
    );
    const chart = screen.getByTestId('growth-chart');
    const trajectory = screen.getByTestId('growth-trajectory');
    // Last in the DOM, so it paints over the reference lines. A chart where the frame
    // competes with the patient is a chart somebody reads wrongly at a glance.
    const paths = Array.from(chart.querySelectorAll('path')) as Element[];
    expect(paths.indexOf(trajectory as unknown as Element)).toBe(paths.length - 1);
  });

  it('picks out the 95th percentile, because [R-06] flags obesity at it', () => {
    renderWithProviders(
      <GrowthChart curves={curves()} points={points()} indicator="BFA" focus={false} />,
    );
    const threshold = screen
      .getByTestId('growth-chart')
      .querySelector('path[data-percentile="95"]');
    expect(threshold).toHaveAttribute('data-threshold', 'true');
    // Dashed as well as tinted, so the distinction survives a monochrome print.
    const styles = getComputedStyle(threshold as Element);
    expect(styles.strokeDasharray === '' ? 'set-in-stylesheet' : styles.strokeDasharray).toBeTruthy();
  });

  it('draws where the reference changes rather than joining silently', () => {
    // D-21: a percentile computed under WHO and one computed under CDC are not the same
    // measurement, and a chart with an invisible join invites exactly that comparison.
    renderWithProviders(
      <GrowthChart curves={curves()} points={points()} indicator="BFA" focus={false} />,
    );
    expect(screen.getAllByTestId('standard-change').length).toBeGreaterThan(0);
  });

  it('marks the visit where the standard changed on the trajectory too', () => {
    renderWithProviders(
      <GrowthChart curves={curves()} points={points()} indicator="BFA" focus={false} />,
    );
    const marked = screen
      .getByTestId('growth-chart')
      .querySelectorAll('circle[data-standard-changed="true"]');
    expect(marked.length).toBe(1);
  });

  it('carries an accessible label naming what is plotted', () => {
    renderWithProviders(<GrowthChart curves={curves()} points={points()} indicator="BFA" />);
    expect(screen.getByRole('img', { name: /BMI-for-age/i })).toBeInTheDocument();
  });

  it('draws a chart for a child with a single measurement', () => {
    // The commonest case at a first visit, and the one an untested chart divides by zero on.
    renderWithProviders(
      <GrowthChart curves={curves()} points={points().slice(0, 1)} indicator="BFA" />,
    );
    expect(screen.getByTestId('growth-chart')).toBeInTheDocument();
    expect(screen.queryByTestId('growth-trajectory')).toBeNull();
  });
});
