import { describe, expect, it } from 'vitest';

import {
  ANTHRO_FIELDS,
  canonicalValue,
  deltaOf,
  emptyForm,
  hasBlocking,
  isEmpty,
  panelOf,
  parsedValue,
  previousFrom,
  previousMeasurementsFrom,
  toBatch,
  warningsFor,
  type FormState,
} from '../src/features/anthropometry/form';

/**
 * Station 2's form (CP45).
 *
 * The layout is verified on a real phone. What is testable here is the part that decides
 * whether the record is right: what a half-typed number contributes, what the panel computes
 * from, and — the one that matters most — that the request carries the number the operator
 * typed rather than the number this file converted.
 */

const facts = { sex: 'male' as const, ageYears: 41 };

function filled(values: Partial<Record<string, [string, string]>>): FormState {
  const form = emptyForm();
  for (const [key, pair] of Object.entries(values)) {
    if (pair === undefined) continue;
    form[key as keyof FormState] = { text: pair[0], unit: pair[1] };
  }
  return form;
}

describe('a field being typed', () => {
  it('holds text, so a decimal point does not vanish mid-keystroke', () => {
    // "72." is what the field contains for one keystroke on the way to 72.5. A form that
    // parsed to a number on every change could not hold it, and the operator would watch
    // their decimal point disappear.
    expect(parsedValue({ text: '72.', unit: 'kg' })).toBe(72);
    expect(parsedValue({ text: '72.5', unit: 'kg' })).toBe(72.5);
  });

  it('treats empty, blank and unparseable as not measured', () => {
    for (const text of ['', '  ', 'abc', '-']) {
      expect(parsedValue({ text, unit: 'kg' }), text).toBeNull();
    }
  });

  it('treats zero as a refusal, not a measurement', () => {
    // A screen that took 0 as a weight would show a BMI of Infinity, which renders as an
    // empty cell: a wrong answer wearing a missing answer's clothes.
    expect(parsedValue({ text: '0', unit: 'kg' })).toBeNull();
    expect(parsedValue({ text: '-5', unit: 'kg' })).toBeNull();
  });

  it('converts into the unit the record stores', () => {
    expect(canonicalValue({ text: '154', unit: '[lb_av]' })).toBeCloseTo(69.85322, 5);
    expect(canonicalValue({ text: '66', unit: 'in' })).toBeCloseTo(167.64, 5);
    expect(canonicalValue({ text: '5.5', unit: '[ft_i]' })).toBeCloseTo(167.64, 5);
  });

  it('says nothing rather than guessing at a unit it does not know', () => {
    expect(canonicalValue({ text: '70', unit: 'stone' })).toBeNull();
  });
});

describe('the panel', () => {
  it('computes from the canonical values, not from what was typed', () => {
    // The whole point. 154 lb is 69.85 kg and a BMI of 24.2 — overweight on this clinic's
    // scale. A panel that took 154 as kilograms would show 53 and call the same patient
    // obese II, on a screen the operator reads aloud.
    const form = filled({ weight: ['154', '[lb_av]'], height: ['170', 'cm'] });
    const panel = panelOf(form, facts);
    expect(panel.bmi?.value).toBeCloseTo((154 * 0.45359237) / 1.7 / 1.7, 9);
    expect(panel.obesity_class).toBe('overweight');
  });

  it('names what it is still waiting for instead of showing zero', () => {
    const panel = panelOf(filled({ height: ['170', 'cm'] }), facts);
    expect(panel.bmi).toBeUndefined();
    expect(panel.needs?.bmi).toEqual(['weight']);
    // Ideal weight needs only a height, so it is there already.
    expect(panel.ideal_body_weight?.value).toBeCloseTo(65.937, 3);
  });

  it('uses the Asian cut-offs, because that is what this clinic uses', () => {
    // 69.36 kg at 170 cm is a BMI of 24.0: "normal" internationally, "overweight" here, and
    // the whole screening pathway hangs on which side of that line somebody falls.
    const panel = panelOf(filled({ weight: ['69.36', 'kg'], height: ['170', 'cm'] }), facts);
    expect(panel.obesity_class).toBe('overweight');
  });

  it('is fast enough to run on every keystroke', () => {
    // Criterion (1): within 200ms of the last input, computed locally.
    const form = filled({
      weight: ['72.5', 'kg'],
      height: ['170', 'cm'],
      waist: ['92', 'cm'],
      hip: ['98', 'cm'],
    });
    const started = performance.now();
    for (let i = 0; i < 1000; i += 1) panelOf(form, facts);
    expect(performance.now() - started).toBeLessThan(200);
  });
});

describe('the comparison with last visit', () => {
  it('says how far the value has moved', () => {
    expect(deltaOf(74.5, 72.5)).toEqual({ change: 2, direction: 'up' });
    expect(deltaOf(70.5, 72.5)?.direction).toBe('down');
  });

  it('says "unchanged" rather than showing a zero', () => {
    // A patient whose weight has not moved in three months is a finding, and a blank there
    // would be hiding it.
    expect(deltaOf(72.5, 72.5)).toEqual({ change: 0, direction: 'same' });
  });

  it('says nothing when there is nothing to compare with', () => {
    expect(deltaOf(72.5, undefined)).toBeNull();
    expect(deltaOf(null, 72.5)).toBeNull();
  });

  it('compares canonical values, so pounds and kilograms are not a delta', () => {
    const previous = previousFrom([
      { code: 'BODY_WEIGHT', value: 154 * 0.45359237 },
      { code: 'BODY_HEIGHT', value: 170 },
    ]);
    const typedInPounds = canonicalValue({ text: '154', unit: '[lb_av]' });
    expect(deltaOf(typedInPounds, previous.weight)?.direction).toBe('same');
  });

  it('takes the newest of several values for a code', () => {
    // The API returns newest first; taking the last would compare against the patient's
    // first ever visit.
    const previous = previousFrom([
      { code: 'BODY_WEIGHT', value: 72.5 },
      { code: 'BODY_WEIGHT', value: 80 },
    ]);
    expect(previous.weight).toBe(72.5);
  });
});

describe('what gets sent', () => {
  const ids = {
    batch: 'b0000000-0000-4000-8000-000000000000',
    patient: 'p0000000-0000-4000-8000-000000000000',
    perField: (key: string) => `e-${key}`,
  };

  it('sends the number as typed with the unit as selected', () => {
    // Not the canonical conversion. The server's conversion is the one that decides what is
    // stored (CP42); posting a converted number would quietly make the phone authoritative
    // about a clinical value.
    const form = filled({ weight: ['154', '[lb_av]'], height: ['170', 'cm'] });
    const body = toBatch(form, ids);
    const weight = body.observations.find((o) => o.code === 'BODY_WEIGHT');
    expect(weight).toEqual({
      event_id: 'e-weight',
      code: 'BODY_WEIGHT',
      value: 154,
      unit: '[lb_av]',
    });
  });

  it('leaves out the fields nobody measured', () => {
    const body = toBatch(filled({ height: ['170', 'cm'] }), ids);
    expect(body.observations).toHaveLength(1);
  });

  it('names the derivations rather than computing them', () => {
    const body = toBatch(filled({ height: ['170', 'cm'] }), ids);
    expect(body.derive).toEqual(['BMI', 'BMR', 'IBW', 'WHR']);
    // And nothing in the body is a computed value.
    expect(JSON.stringify(body)).not.toContain('bmi');
  });

  it('gives every value its own event id and the batch its own', () => {
    // Each measurement's id makes that measurement's retry safe; the batch's id makes the
    // derived values' retry safe, because the client cannot name those in advance.
    const body = toBatch(filled({ height: ['170', 'cm'], weight: ['72', 'kg'] }), ids);
    const eventIDs = body.observations.map((o) => o.event_id);
    expect(new Set(eventIDs).size).toBe(2);
    expect(eventIDs).not.toContain(body.event_id);
  });

  it('will not save an untouched form', () => {
    expect(isEmpty(emptyForm())).toBe(true);
    expect(isEmpty(filled({ height: ['170', 'cm'] }))).toBe(false);
  });
});

describe('the form itself', () => {
  it('offers every field in the order the measurements are taken', () => {
    expect(ANTHRO_FIELDS.map((f) => f.key)).toEqual([
      'height',
      'weight',
      'waist',
      'hip',
      'bodyFat',
      'muscle',
    ]);
  });

  it('offers a unit an operator with an imperial instrument can pick', () => {
    // A scale that reads in pounds and a screen that only takes kilograms is an operator
    // converting in their head, twice a year wrongly, by a factor of 2.2.
    for (const key of ['height', 'weight', 'waist', 'hip'] as const) {
      const field = ANTHRO_FIELDS.find((f) => f.key === key)!;
      expect(field.units.length, key).toBeGreaterThan(1);
    }
  });

  it('starts every field in the canonical unit', () => {
    const form = emptyForm();
    expect(form.height.unit).toBe('cm');
    expect(form.weight.unit).toBe('kg');
    expect(form.bodyFat.unit).toBe('%');
  });
});

describe('plausibility on the phone (CP46)', () => {
  const rules = [
    {
      code: 'BODY_HEIGHT',
      min_age_years: 18,
      absolute_min: 100,
      absolute_max: 230,
      plausible_min: 135,
      plausible_max: 200,
      max_increase: 2,
      approved: false,
    },
    {
      code: 'BODY_WEIGHT',
      min_age_years: 18,
      absolute_min: 20,
      absolute_max: 350,
      plausible_min: 30,
      plausible_max: 180,
      max_increase: 15,
      max_decrease: 15,
      approved: false,
    },
  ];
  const subject = { sex: 'male' as const, ageYears: 41 };

  it('warns as the number is typed, not after saving', () => {
    const warnings = warningsFor(filled({ height: ['205', 'cm'] }), rules, subject, {});
    expect(warnings.height).toEqual(
      expect.objectContaining({ severity: 'warn', kind: 'high', limit: 200 }),
    );
  });

  it('stops an impossible value outright', () => {
    const warnings = warningsFor(filled({ height: ['15', 'cm'] }), rules, subject, {});
    expect(warnings.height?.severity).toBe('stop');
    expect(hasBlocking(warnings)).toBe(true);
  });

  it('checks the canonical value, so pounds are not weighed against a kilogram band', () => {
    // 400 lb is 181.4 kg — just outside the plausible band. Checking 400 against it would
    // refuse every heavy patient measured on an imperial scale.
    const warnings = warningsFor(filled({ weight: ['400', '[lb_av]'] }), rules, subject, {});
    expect(warnings.weight?.kind).toBe('high');
    const fine = warningsFor(filled({ weight: ['150', '[lb_av]'] }), rules, subject, {});
    expect(fine.weight).toBeUndefined();
  });

  it('lets a confirmation clear a warning but never a stop', () => {
    const warn = warningsFor(filled({ height: ['205', 'cm'] }), rules, subject, {}, { height: true });
    expect(warn.height).toBeUndefined();
    const stop = warningsFor(filled({ height: ['15', 'cm'] }), rules, subject, {}, { height: true });
    expect(stop.height?.severity).toBe('stop');
  });

  it('sends the confirmation with the value it belongs to', () => {
    const body = toBatch(filled({ height: ['205', 'cm'], weight: ['72', 'kg'] }), {
      batch: 'b',
      patient: 'p',
      perField: (key) => `e-${key}`,
      confirmed: (key) => key === 'height',
    });
    const height = body.observations.find((o) => o.code === 'BODY_HEIGHT');
    const weight = body.observations.find((o) => o.code === 'BODY_WEIGHT');
    expect(height?.confirmed).toBe(true);
    // Not blanket-confirmed. An operator who confirmed one unusual height has not vouched
    // for every other number on the form.
    expect(weight?.confirmed).toBeUndefined();
  });

  it('warns on the change since last time, with the date it is measuring from', () => {
    const previous = previousMeasurementsFrom([
      { code: 'BODY_HEIGHT', value: 172, effective_at: '2026-03-01T09:00:00Z' },
    ]);
    const warnings = warningsFor(
      filled({ height: ['184', 'cm'] }),
      rules,
      subject,
      previous,
      {},
      new Date('2026-09-01T09:00:00Z'),
    );
    expect(warnings.height).toEqual(
      expect.objectContaining({ kind: 'rose', previous: 172 }),
    );
  });
});
