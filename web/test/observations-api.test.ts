import type { components } from '@dthcms/api-client';
import { describe, expect, it } from 'vitest';

import {
  chainsOf,
  codeEntry,
  computedFromCurrent,
  derivedFrom,
  enteredUnitOf,
  enteredValueOf,
  inputSeen,
  isCurrent,
  isReplaced,
  latest,
  original,
  unitsFor,
} from '@/features/observations';

/**
 * The rules behind the value chain (CP62, §4.3).
 *
 * These are the half of criterion 5 that has nothing to do with rendering: which rows belong
 * to which chain, in which order, and whether what a derived value was computed from is still
 * what stands. They are exercised through the screens as well, and separately here because the
 * interesting cases are the malformed ones — a history cut off by a limit, a replacement that
 * points at nothing, two measurements of one code on one day — and a rule that can only be
 * reached by mocking a network call is a rule nobody exercises.
 */

type Observation = components['schemas']['Observation'];
type ObservationCode = components['schemas']['ObservationCode'];

const PATIENT = '0190a8f2-0000-7000-8000-0000000000a1';
const OPERATOR = '0190a8f2-0000-7000-8000-0000000000c1';

function observation(over: Partial<Observation> = {}): Observation {
  return {
    id: 'obs-1',
    patient_id: PATIENT,
    code: 'BODY_HEIGHT',
    category: 'ANTHRO',
    value_type: 'numeric',
    value: 150,
    unit: 'cm',
    effective_at: '2026-09-14T04:00:00Z',
    recorded_at: '2026-09-14T04:00:00Z',
    source: 'STATION',
    status: 'ACTIVE',
    recorded_by: OPERATOR,
    recorded_role: 'ANTHROPOMETRY',
    ...over,
  };
}

describe('the chain', () => {
  it('follows replaced_by from the original to the value that stands', () => {
    // The order the events happened in and the order the sentence reads in: 150 was recorded,
    // then corrected to 140. Reversed, it would put the answer before the question.
    const chains = chainsOf([
      observation({ id: 'a', status: 'CORRECTED', replaced_by: 'b' }),
      observation({ id: 'b', value: 140 }),
    ]);

    expect(chains).toHaveLength(1);
    expect(chains[0]?.map((row) => row.id)).toEqual(['a', 'b']);
    expect(original(chains[0] ?? [])?.id).toBe('a');
    expect(latest(chains[0] ?? [])?.id).toBe('b');
  });

  it('finds the head by exclusion rather than by status', () => {
    // Asking for ACTIVE would find the *end* of each chain, and would find nothing at all in a
    // history where every row has been replaced — which is exactly the history somebody is
    // reading when they ask what happened.
    const chains = chainsOf([
      observation({ id: 'a', status: 'CORRECTED', replaced_by: 'b' }),
      observation({ id: 'b', status: 'SUPERSEDED', replaced_by: 'c' }),
      observation({ id: 'c', status: 'SUPERSEDED' }),
    ]);

    expect(chains).toHaveLength(1);
    expect(chains[0]?.map((row) => row.id)).toEqual(['a', 'b', 'c']);
  });

  it('keeps two independent measurements of one code apart', () => {
    const chains = chainsOf([
      observation({ id: 'today', effective_at: '2026-09-14T04:00:00Z' }),
      observation({ id: 'march', effective_at: '2026-03-02T04:00:00Z' }),
    ]);

    // Newest first: a physician opens this screen about today.
    expect(chains.map((chain) => chain[0]?.id)).toEqual(['today', 'march']);
  });

  it('ends a chain whose replacement did not come back', () => {
    // `limit` can cut a history off mid-chain. Inventing a continuation, or hiding the row
    // because its successor is missing, would both be a statement about data this client does
    // not have.
    const chains = chainsOf([observation({ id: 'a', status: 'CORRECTED', replaced_by: 'gone' })]);

    expect(chains[0]?.map((row) => row.id)).toEqual(['a']);
  });

  it('stops rather than looping when replaced_by points back into the chain', () => {
    // A cycle in a foreign key should be impossible. "Should be" is not a reason to write a
    // loop that hangs the browser on a patient's record: the walk stops and the rows already
    // collected are still drawn, because most of a chain is worth more than a blank screen.
    const chains = chainsOf([
      observation({ id: 'a', status: 'CORRECTED', replaced_by: 'b' }),
      observation({ id: 'b', status: 'CORRECTED', replaced_by: 'a' }),
    ]);

    expect(chains.flat().length).toBeLessThanOrEqual(4);
  });

  it('sorts a row with an unreadable timestamp rather than scrambling the list', () => {
    // NaN through a comparator silently reorders everything around it.
    const chains = chainsOf([
      observation({ id: 'broken', effective_at: 'not a date', recorded_at: 'not a date' }),
      observation({ id: 'today', effective_at: '2026-09-14T04:00:00Z' }),
    ]);

    expect(chains.map((chain) => chain[0]?.id)).toEqual(['today', 'broken']);
  });

  it('tells the current row from a replaced one without collapsing the two reasons', () => {
    // "Somebody typed the wrong number" and "the value was right and has been re-derived" are
    // different facts about different people.
    expect(isCurrent(observation())).toBe(true);
    expect(isReplaced(observation({ status: 'CORRECTED' }))).toBe(true);
    expect(isReplaced(observation({ status: 'SUPERSEDED' }))).toBe(true);
  });
});

describe('what was computed from a value', () => {
  const bmi = observation({
    id: 'bmi',
    code: 'BMI',
    category: 'DERIVED',
    value: 28.1,
    unit: 'kg/m2',
    inputs: { BODY_HEIGHT: 140, BODY_WEIGHT: 55 },
  });

  it('finds a derived value by what it says it was given', () => {
    // The server knows which derivations read a height; this client does not, and a copy of
    // that table here would be a second opinion that disagrees the day a formula changes.
    expect(derivedFrom([observation(), bmi], 'BODY_HEIGHT').map((row) => row.id)).toEqual(['bmi']);
    expect(derivedFrom([observation(), bmi], 'WAIST_CIRC')).toEqual([]);
  });

  it('claims nothing about a derived value that records no inputs', () => {
    const older = observation({ id: 'old-bmi', code: 'BMI', category: 'DERIVED' });
    expect(derivedFrom([older], 'BODY_HEIGHT')).toEqual([]);
  });

  it('says whether it was computed from the value that stands now', () => {
    expect(computedFromCurrent(bmi, 'BODY_HEIGHT', observation({ value: 140 }))).toBe(true);
    expect(computedFromCurrent(bmi, 'BODY_HEIGHT', observation({ value: 150 }))).toBe(false);
  });

  it('answers "we cannot tell" rather than "it is fine" when either number is missing', () => {
    // Rounding the unknown up to the reassuring answer is the one thing this must never do.
    expect(computedFromCurrent(bmi, 'BODY_HEIGHT', undefined)).toBeNull();
    expect(computedFromCurrent(bmi, 'WAIST_CIRC', observation())).toBeNull();
    expect(computedFromCurrent(bmi, 'BODY_HEIGHT', observation({ value: Number.NaN }))).toBeNull();
  });

  it('treats two numbers that agree to a thousandth as the same measurement', () => {
    // Both are floating point and arrived by different routes — one stored when the formula
    // ran, one from a fresh read. Treating them as different would report every BMI as stale.
    expect(computedFromCurrent(bmi, 'BODY_HEIGHT', observation({ value: 140.0000001 }))).toBe(true);
    expect(inputSeen(bmi, 'BODY_HEIGHT')).toBe(140);
    expect(inputSeen(bmi, 'WAIST_CIRC')).toBeUndefined();
  });
});

describe('the unit a value was entered in', () => {
  const codes: ObservationCode[] = [
    {
      code: 'BODY_WEIGHT',
      category: 'ANTHRO',
      value_type: 'numeric',
      display_en: 'Weight',
      display_bn: 'ওজন',
      write_permission: 'observation.write.anthro',
      canonical_unit: 'kg',
      units: [
        {
          code: '[lb_av]',
          dimension: 'mass',
          is_canonical: false,
          display_en: 'lb',
          display_bn: 'পাউন্ড',
          decimals: 1,
        },
        {
          code: 'kg',
          dimension: 'mass',
          is_canonical: true,
          display_en: 'kg',
          display_bn: 'কেজি',
          decimals: 1,
        },
      ],
    },
  ];

  it('reads back the number and the unit the operator actually typed', () => {
    // 154 lb is stored as 69.85 kg and read back as 154 lb. A correction form pre-filled with
    // kilograms would ask an operator to check a number they never wrote.
    const weight = observation({
      code: 'BODY_WEIGHT',
      value: 69.85,
      unit: 'kg',
      entered_value: 154,
      entered_unit: '[lb_av]',
    });

    expect(enteredValueOf(weight)).toBe(154);
    expect(enteredUnitOf(weight)).toBe('[lb_av]');
  });

  it('falls back to the canonical pair where nothing else was recorded', () => {
    expect(enteredValueOf(observation())).toBe(150);
    expect(enteredUnitOf(observation())).toBe('cm');
  });

  it('answers nothing for a unitless code, rather than an empty string', () => {
    // A form that sent `unit: ''` on a code that takes no unit would be refused by the server.
    expect(
      enteredUnitOf(observation({ unit: undefined, entered_unit: undefined })),
    ).toBeUndefined();
  });

  it('offers the canonical unit first', () => {
    // It is the unit the clinic works in and the one a value will almost always be corrected
    // in; the rest follow so that "entered in the wrong unit" is a correction somebody can make.
    expect(unitsFor(codeEntry(codes, 'BODY_WEIGHT')).map((unit) => unit.code)).toEqual([
      'kg',
      '[lb_av]',
    ]);
  });

  it('offers no unit at all for a code the registry has not arrived for', () => {
    expect(unitsFor(codeEntry(codes, 'BODY_HEIGHT'))).toEqual([]);
  });
});
