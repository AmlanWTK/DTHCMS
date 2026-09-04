import { describe, expect, it } from 'vitest';

import { evaluate, resolveRule, type PlausibilityRule } from '../src/plausibility';

/**
 * Impossible inputs, caught on the client (CP46).
 *
 * The server is authoritative and always checks. What is tested here is the half that has to
 * arrive in time to be acted on — while the operator is still holding the tape measure — and
 * the one property that makes it safe: this file ranks nothing. The rule that applies is the
 * first match in the order the server sent.
 */

const rules: PlausibilityRule[] = [
  // Deliberately in the order the API returns: most specific first, per code.
  {
    code: 'BODY_HEIGHT',
    min_age_years: 18,
    absolute_min: 100,
    absolute_max: 230,
    plausible_min: 135,
    plausible_max: 200,
    max_increase: 2,
    max_decrease: 4,
    note_en: 'An adult does not change height.',
    note_bn: 'প্রাপ্তবয়স্কের উচ্চতা বদলায় না।',
    approved: false,
  },
  {
    code: 'BODY_HEIGHT',
    min_age_years: 2,
    max_age_years: 18,
    absolute_min: 70,
    absolute_max: 210,
    plausible_min: 78,
    plausible_max: 195,
    max_increase_per_day: 0.15,
    approved: false,
  },
  {
    code: 'BODY_HEIGHT',
    max_age_years: 2,
    absolute_min: 30,
    absolute_max: 110,
    plausible_min: 44,
    plausible_max: 100,
    approved: false,
  },
  {
    code: 'BODY_FAT_PCT',
    sex: 'male',
    min_age_years: 18,
    absolute_min: 2,
    absolute_max: 70,
    plausible_min: 4,
    plausible_max: 50,
    approved: false,
  },
  { code: 'BODY_FAT_PCT', absolute_min: 1, absolute_max: 70, approved: false },
];

const adult = { sex: 'male' as const, ageYears: 41 };

describe('which rule applies', () => {
  it('takes the first match, because the server ordered them', () => {
    expect(resolveRule(rules, 'BODY_HEIGHT', adult)?.min_age_years).toBe(18);
    expect(resolveRule(rules, 'BODY_HEIGHT', { sex: 'male', ageYears: 5 })?.max_age_years).toBe(18);
    expect(resolveRule(rules, 'BODY_HEIGHT', { sex: 'male', ageYears: 1 })?.absolute_max).toBe(110);
  });

  it('treats an age band as inclusive below and exclusive above', () => {
    // Otherwise a child on their eighteenth birthday matches two rules or none.
    expect(resolveRule(rules, 'BODY_HEIGHT', { sex: 'male', ageYears: 18 })?.min_age_years).toBe(
      18,
    );
    expect(resolveRule(rules, 'BODY_HEIGHT', { sex: 'male', ageYears: 17.99 })?.max_age_years).toBe(
      18,
    );
  });

  it('prefers a sex-specific rule when the server put one first', () => {
    expect(resolveRule(rules, 'BODY_FAT_PCT', adult)?.plausible_max).toBe(50);
    expect(
      resolveRule(rules, 'BODY_FAT_PCT', { sex: 'female', ageYears: 41 })?.plausible_max,
    ).toBeUndefined();
  });

  it('says nothing for a code with no rule', () => {
    expect(resolveRule(rules, 'GLUCOSE_FASTING', adult)).toBeNull();
  });
});

describe('what a rule says about a value', () => {
  const rule = resolveRule(rules, 'BODY_HEIGHT', adult);

  it('stops an impossible value', () => {
    expect(evaluate(rule, 15)).toEqual(
      expect.objectContaining({ severity: 'stop', kind: 'low', limit: 100 }),
    );
  });

  it('warns about an unusual but possible one', () => {
    expect(evaluate(rule, 205)).toEqual(
      expect.objectContaining({ severity: 'warn', kind: 'high', limit: 200 }),
    );
  });

  it('lets a confirmation through the soft band and not the hard one', () => {
    expect(evaluate(rule, 205, { confirmed: true })).toBeNull();
    // No confirmation passes the absolute band. A client "fixed" to always confirm still
    // cannot store a height of 15 cm.
    expect(evaluate(rule, 15, { confirmed: true })?.severity).toBe('stop');
  });

  it('says nothing about an ordinary value', () => {
    expect(evaluate(rule, 172)).toBeNull();
  });

  it('says nothing when there is no rule or no value', () => {
    expect(evaluate(null, 172)).toBeNull();
    expect(evaluate(rule, null)).toBeNull();
  });

  it('carries the rule’s own explanation, in both languages', () => {
    const verdict = evaluate(rule, 205);
    expect(verdict?.note_en).toContain('adult');
    expect(verdict?.note_bn).toBeTruthy();
  });
});

describe('the change since last time', () => {
  const rule = resolveRule(rules, 'BODY_HEIGHT', adult);
  const march = new Date('2026-03-01T09:00:00Z');
  const september = new Date('2026-09-01T09:00:00Z');

  it('warns when an adult gains twelve centimetres', () => {
    const verdict = evaluate(rule, 184, {
      previous: { value: 172, at: march },
      now: september,
    });
    expect(verdict).toEqual(
      expect.objectContaining({ severity: 'warn', kind: 'rose', previous: 172 }),
    );
  });

  it('says nothing about ordinary measurement noise', () => {
    // If the rules fired on a centimetre, staff would confirm everything reflexively and the
    // confirmation would stop meaning anything.
    expect(evaluate(rule, 173, { previous: { value: 172, at: march }, now: september })).toBeNull();
  });

  it('checks the value itself before the change', () => {
    // A value that is wrong makes its delta wrong too, and telling somebody their height
    // changed by 130 cm when they typed 15 sends them to the wrong question.
    const verdict = evaluate(rule, 15, { previous: { value: 172, at: march }, now: september });
    expect(verdict?.kind).toBe('low');
  });

  it('needs a day between measurements before a rate means anything', () => {
    const child = resolveRule(rules, 'BODY_HEIGHT', { sex: 'male', ageYears: 5 });
    const morning = new Date('2026-09-01T09:00:00Z');
    const afternoon = new Date('2026-09-01T15:00:00Z');
    // Two measurements in one visit are a re-measurement, not a growth rate.
    expect(
      evaluate(child, 120, { previous: { value: 118, at: morning }, now: afternoon }),
    ).toBeNull();
  });

  it('applies a per-day rate over a real gap', () => {
    const child = resolveRule(rules, 'BODY_HEIGHT', { sex: 'male', ageYears: 5 });
    const march = new Date('2026-03-01T09:00:00Z');
    const april = new Date('2026-04-01T09:00:00Z');
    // 0.15 cm/day over 31 days is 4.65 cm. Ten is more than that.
    expect(evaluate(child, 128, { previous: { value: 118, at: march }, now: april })?.kind).toBe(
      'rose',
    );
  });
});
