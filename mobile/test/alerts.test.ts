import { describe, expect, it } from 'vitest';

import {
  actionTextOf,
  alarmShouldSound,
  breachLine,
  breachOf,
  deliveryOf,
  nameOf,
  ordered,
  ruleFor,
  type CriticalAlert,
  type CriticalRule,
} from '../src/features/alerts/state';

/**
 * Critical values, on the phone (CP50).
 *
 * The modal itself is a React Native component and is judged on a device, by a clinical
 * assistant, against the plan's own manual verification: enter an SpO2 of 88 and confirm the
 * alarm sounds. What is checked here is every decision behind it — which is all of them,
 * because they were deliberately kept out of the screen.
 *
 * The one worth stating plainly: **the phone does not decide whether an alert exists.** The
 * server raises it, inside the transaction that stored the value, and the write's own
 * response carries it back. `ruleFor` and `breachOf` exist so the field can turn red while
 * the operator is still typing — and they are held to the server's own resolution order,
 * because a phone that disagreed would either sound an alarm nobody raised or, far worse,
 * stay quiet when the server did.
 */

const rule = (over: Partial<CriticalRule> = {}): CriticalRule => ({
  id: over.id ?? 'r1',
  code: 'SPO2',
  approved: false,
  ...over,
});

// The server sends them most specific first. These are in that order, deliberately: the
// general rule is last, exactly as `core.critical_value_for` would rank it.
const rules: CriticalRule[] = [
  rule({ id: 'hr-infant', code: 'HEART_RATE', max_age_years: 1, low: 80, high: 200 }),
  rule({
    id: 'hr-toddler',
    code: 'HEART_RATE',
    min_age_years: 1,
    max_age_years: 6,
    low: 60,
    high: 180,
  }),
  rule({ id: 'hr-adult', code: 'HEART_RATE', min_age_years: 18, low: 40, high: 130 }),
  rule({ id: 'spo2', code: 'SPO2', low: 92 }),
];

const alert = (over: Partial<CriticalAlert> = {}): CriticalAlert =>
  ({
    id: over.id ?? 'a1',
    patient_id: 'p1',
    observation_id: 'o1',
    code: 'SPO2',
    value: 88,
    unit: '%',
    breached: 'low',
    threshold: 92,
    raised_at: '2026-09-14T09:00:00Z',
    raised_by: 'u1',
    status: 'OPEN',
    escalation_step: 1,
    delivered: true,
    recipients: 1,
    ...over,
  }) as CriticalAlert;

describe('which rule applies', () => {
  it('takes the first match, and never ranks anything itself', () => {
    expect(ruleFor(rules, 'HEART_RATE', { sex: 'male', ageYears: 0.5 })?.id).toBe('hr-infant');
    expect(ruleFor(rules, 'HEART_RATE', { sex: 'female', ageYears: 3 })?.id).toBe('hr-toddler');
    expect(ruleFor(rules, 'HEART_RATE', { sex: 'male', ageYears: 41 })?.id).toBe('hr-adult');
  });

  it('treats the upper age bound as exclusive, exactly as the server does', () => {
    // A child on their first birthday is out of the infant band and into the toddler one. An
    // off-by-one here is a band that overlaps or a band with a hole, and the hole is silent.
    expect(ruleFor(rules, 'HEART_RATE', { sex: 'male', ageYears: 0.99 })?.id).toBe('hr-infant');
    expect(ruleFor(rules, 'HEART_RATE', { sex: 'male', ageYears: 1 })?.id).toBe('hr-toddler');
  });

  it('has nothing to say about an age no band covers', () => {
    // Between six and eighteen, in this cut-down fixture. The honest answer is no rule, not
    // the nearest one — an alarm from a band that was not written for this patient is worse
    // than no alarm, because somebody will believe it.
    expect(ruleFor(rules, 'HEART_RATE', { sex: 'male', ageYears: 10 })).toBeNull();
  });

  it('has nothing to say about a code with no rule', () => {
    expect(ruleFor(rules, 'BODY_WEIGHT', { sex: 'male', ageYears: 41 })).toBeNull();
  });

  it('respects a sex-specific rule', () => {
    const gendered = [rule({ id: 'f', code: 'SPO2', sex: 'female', low: 94 }), ...rules];
    expect(ruleFor(gendered, 'SPO2', { sex: 'female', ageYears: 30 })?.id).toBe('f');
    expect(ruleFor(gendered, 'SPO2', { sex: 'male', ageYears: 30 })?.id).toBe('spo2');
  });
});

describe('whether a value is critical', () => {
  it.each([
    [91.9, 'low'],
    [88, 'low'],
  ])('flags %s as %s', (value, breached) => {
    expect(breachOf(value, ruleFor(rules, 'SPO2', { sex: 'male', ageYears: 41 }))?.breached).toBe(
      breached,
    );
  });

  it('is strict on the threshold itself', () => {
    // 92 is not below 92. A phone that used <= would alarm on an ordinary saturation, and the
    // third time that happened the operator would stop reading the alarms.
    expect(breachOf(92, ruleFor(rules, 'SPO2', { sex: 'male', ageYears: 41 }))).toBeNull();
    expect(breachOf(130, ruleFor(rules, 'HEART_RATE', { sex: 'male', ageYears: 41 }))).toBeNull();
    expect(
      breachOf(131, ruleFor(rules, 'HEART_RATE', { sex: 'male', ageYears: 41 }))?.breached,
    ).toBe('high');
  });

  it('says nothing when there is no rule', () => {
    expect(breachOf(10, null)).toBeNull();
  });

  it('does not invent a ceiling where the rule has none', () => {
    // Oxygen saturation has a floor and no ceiling. A phone that flagged 100% would flag
    // every healthy patient.
    expect(breachOf(100, ruleFor(rules, 'SPO2', { sex: 'male', ageYears: 41 }))).toBeNull();
  });
});

describe('the order the operator reads them in', () => {
  it('puts a low breach first', () => {
    // A hypoglycaemia and a hypertension in the same entry are both urgent, and only one of
    // them is treated in the next thirty seconds with something from a drawer.
    const shown = ordered([
      alert({ id: 'high', code: 'BP_SYSTOLIC', breached: 'high' }),
      alert({ id: 'low', code: 'GLUCOSE_RANDOM', breached: 'low' }),
    ]);
    expect(shown.map((a) => a.id)).toEqual(['low', 'high']);
  });

  it('is stable for two breaches of the same kind', () => {
    const shown = ordered([
      alert({ id: 'sys', code: 'BP_SYSTOLIC', breached: 'high' }),
      alert({ id: 'dia', code: 'BP_DIASTOLIC', breached: 'high' }),
    ]);
    expect(shown.map((a) => a.code)).toEqual(['BP_DIASTOLIC', 'BP_SYSTOLIC']);
  });

  it('does not mutate what it was given', () => {
    const given = [alert({ id: 'a', breached: 'high' }), alert({ id: 'b', breached: 'low' })];
    ordered(given);
    expect(given.map((a) => a.id)).toEqual(['a', 'b']);
  });
});

describe('what the operator is asked to do about delivery', () => {
  it('says carry on when every alert reached a screen', () => {
    expect(deliveryOf([alert({ delivered: true }), alert({ id: 'b', delivered: true })])).toBe(
      'delivered',
    );
  });

  it('says walk when even one of them did not', () => {
    // Criterion 4. One undelivered alert in a set is enough: the operator cannot know which
    // of the two the consultant is missing, and the instruction that covers both is to go.
    expect(deliveryOf([alert({ delivered: true }), alert({ id: 'b', delivered: false })])).toBe(
      'walk',
    );
  });
});

describe('when the alarm sounds', () => {
  it('sounds while there is an unread alert', () => {
    expect(alarmShouldSound([alert()], false)).toBe(true);
  });

  it('stops only when the operator says they have read it', () => {
    // Not on a timer, and not when the modal appears. An alarm that stops by itself is an
    // alarm somebody can be in the next room for.
    expect(alarmShouldSound([alert()], true)).toBe(false);
  });

  it('does not sound when there is nothing to sound about', () => {
    expect(alarmShouldSound([], false)).toBe(false);
  });
});

describe('what the card says', () => {
  it('shows the number and the line it crossed', () => {
    // The operator has just typed the first; what they have not got in their head is the
    // second, and a screen that omits it asks them to remember one under pressure.
    expect(breachLine(alert({ value: 88, threshold: 92, unit: '%' }))).toEqual({
      value: '88%',
      limit: '92%',
    });
  });

  it('keeps one decimal where a value has one', () => {
    expect(breachLine(alert({ value: 3.4, threshold: 3, unit: 'mmol/L' })).value).toBe(
      '3.4 mmol/L',
    );
  });

  it('closes up a percentage and spaces a written unit', () => {
    // "88%" is right and "204mmHg" is not — and in Bangla, where the unit is a word, running
    // them together makes the number end in a cluster nobody reads at a glance.
    expect(breachLine(alert({ value: 204, threshold: 180, unit: 'mmHg' })).value).toBe('204 mmHg');
    expect(breachLine(alert({ value: 39.8, threshold: 39.5, unit: '°C' })).value).toBe('39.8°C');
  });

  it('leaves the dimensionless unit off', () => {
    expect(breachLine(alert({ value: 2, threshold: 1, unit: '1' })).value).toBe('2');
  });

  it('reads the action in the operator’s own language', () => {
    const withAction = alert({ action_en: 'Give oxygen.', action_bn: 'অক্সিজেন দিন।' });
    expect(actionTextOf(withAction, 'en')).toBe('Give oxygen.');
    expect(actionTextOf(withAction, 'bn')).toBe('অক্সিজেন দিন।');
    expect(actionTextOf(alert(), 'en')).toBe('');
  });

  it('names the code the way the registry names it', () => {
    const named = alert({ display_en: 'Oxygen saturation', display_bn: 'অক্সিজেন সম্পৃক্তি' });
    expect(nameOf(named, 'en')).toBe('Oxygen saturation');
    expect(nameOf(named, 'bn')).toBe('অক্সিজেন সম্পৃক্তি');
  });

  it('falls back to the code rather than showing a blank', () => {
    // Only reachable for a code added between the server's deploy and the phone's — and a
    // blank where "Oxygen saturation" belongs is worse than SPO2.
    expect(nameOf(alert(), 'en')).toBe('SPO2');
  });
});
