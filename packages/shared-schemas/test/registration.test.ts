import { describe, expect, it } from 'vitest';

import {
  ageOn,
  birthDateProblem,
  documentNeedsExactDate,
  isComplete,
  normalisePhone,
  readDate,
  requiredState,
  type DateParts,
} from '../src/registration';

/**
 * Registration's rules, tested here rather than only through the two forms that use them
 * (CP32, CP33).
 *
 * The web desk and the station app each have their own tests, and each proves that *its*
 * screen calls these functions. Neither proves the functions are right, and a rule that is
 * only ever exercised through a rendered form is a rule whose edges nobody has looked at.
 * The edges are the point: [R-06] makes the date of birth clinically load-bearing, and the
 * failure that matters is an accepted wrong date, not a refused good one.
 */

const parts = (over: Partial<DateParts> = {}): DateParts => ({
  day: '',
  month: '',
  year: '',
  ...over,
});

describe('readDate', () => {
  it('reads a full date at day precision', () => {
    expect(readDate(parts({ day: '9', month: '3', year: '1987' }))).toEqual({
      iso: '1987-03-09',
      precision: 'day',
    });
  });

  it('pads a single-digit day and month, because the ISO date is what gets stored', () => {
    expect(readDate(parts({ day: '1', month: '1', year: '2001' }))?.iso).toBe('2001-01-01');
  });

  it('accepts a year alone and records 1 January at year precision', () => {
    // The ordinary case in this clinic, not an edge: a patient who knows only the year has
    // given a complete answer, and 1 January is the honest placeholder rather than a
    // fabricated day that a percentile would then treat as exact.
    expect(readDate(parts({ year: '1960' }))).toEqual({ iso: '1960-01-01', precision: 'year' });
  });

  it('accepts a month and year at month precision', () => {
    expect(readDate(parts({ month: '7', year: '1960' }))).toEqual({
      iso: '1960-07-01',
      precision: 'month',
    });
  });

  it('refuses a day without a month, which says nothing', () => {
    expect(readDate(parts({ day: '14', year: '1990' }))).toBeNull();
  });

  it.each(['', '19', '190', '19900', 'abcd', '19 0', '-990'])(
    'refuses %o as a year: four digits or nothing',
    (year) => {
      expect(readDate(parts({ year }))).toBeNull();
    },
  );

  it.each([
    ['0', 'month'],
    ['13', 'month'],
    ['1.5', 'month'],
  ])('refuses %s as a %s', (month) => {
    expect(readDate(parts({ month, year: '1990' }))).toBeNull();
  });

  it.each(['0', '32', '2.5'])('refuses %s as a day', (day) => {
    expect(readDate(parts({ day, month: '6', year: '1990' }))).toBeNull();
  });

  it('refuses 31 February', () => {
    // The round-trip check, and the reason there is no table of month lengths here.
    expect(readDate(parts({ day: '31', month: '2', year: '1990' }))).toBeNull();
    expect(readDate(parts({ day: '30', month: '2', year: '1990' }))).toBeNull();
    expect(readDate(parts({ day: '31', month: '4', year: '1990' }))).toBeNull();
  });

  it('knows which Februaries have 29 days', () => {
    expect(readDate(parts({ day: '29', month: '2', year: '2000' }))?.iso).toBe('2000-02-29');
    expect(readDate(parts({ day: '29', month: '2', year: '2024' }))?.iso).toBe('2024-02-29');
    // 1900 was not a leap year, and 2023 was not either. A hand-written rule gets one of
    // these wrong; a round trip through the calendar gets neither wrong.
    expect(readDate(parts({ day: '29', month: '2', year: '1900' }))).toBeNull();
    expect(readDate(parts({ day: '29', month: '2', year: '2023' }))).toBeNull();
  });

  it('accepts a year a typing slip would produce, because catching it is not its job', () => {
    // 1085 for 1985 is a valid date. Nothing here can tell them apart, which is exactly why
    // the form echoes the age back in words and birthDateProblem exists.
    expect(readDate(parts({ day: '4', month: '6', year: '1085' }))?.iso).toBe('1085-06-04');
  });
});

describe('ageOn', () => {
  const today = new Date('2026-09-04T00:00:00Z');

  it('gives years and months', () => {
    expect(ageOn('1987-03-09', 'day', today)).toEqual({
      years: 39,
      months: 5,
      approximate: false,
    });
  });

  it('has not counted the birthday until the day arrives', () => {
    expect(ageOn('1990-09-05', 'day', today)).toMatchObject({ years: 35, months: 11 });
    expect(ageOn('1990-09-04', 'day', today)).toMatchObject({ years: 36, months: 0 });
  });

  it('borrows a year when the month has not come round', () => {
    expect(ageOn('1990-12-01', 'day', today)).toMatchObject({ years: 35, months: 9 });
  });

  it('reports an infant in months', () => {
    // The number the growth card is read against: "0 years, 4 months", not "0".
    expect(ageOn('2026-05-04', 'day', today)).toMatchObject({ years: 0, months: 4 });
  });

  it('marks anything less than a full date approximate', () => {
    expect(ageOn('1960-01-01', 'year', today)?.approximate).toBe(true);
    expect(ageOn('1960-07-01', 'month', today)?.approximate).toBe(true);
    expect(ageOn('1960-07-02', 'day', today)?.approximate).toBe(false);
  });

  it('makes a mistyped year obvious', () => {
    // The whole reason the echo is in the form: "941 years" is unmissable where "1085" is not.
    expect(ageOn('1085-06-04', 'day', today)?.years).toBe(941);
  });

  it('returns null for something that is not a date', () => {
    expect(ageOn('not-a-date', 'day', today)).toBeNull();
  });
});

describe('birthDateProblem', () => {
  const today = new Date('2026-09-04T00:00:00Z');

  it('passes an ordinary date', () => {
    expect(birthDateProblem('1987-03-09', today)).toBeNull();
  });

  it('refuses tomorrow', () => {
    expect(birthDateProblem('2026-09-05', today)).toBe('future');
  });

  it('accepts today, because a newborn is registered on the day they are born', () => {
    expect(birthDateProblem('2026-09-04', today)).toBeNull();
  });

  it('refuses a year no living patient has', () => {
    expect(birthDateProblem('1085-06-04', today)).toBe('implausible');
    expect(birthDateProblem('1895-01-01', today)).toBe('implausible');
  });

  it('leaves the oldest plausible patients alone', () => {
    // 130 years is the limit, and it is not exceeded until the year difference passes it.
    expect(birthDateProblem('1896-01-01', today)).toBeNull();
  });
});

describe('normalisePhone', () => {
  it.each([
    ['01712345678', '+8801712345678'],
    ['+8801712345678', '+8801712345678'],
    ['8801712345678', '+8801712345678'],
    ['1712345678', '+8801712345678'],
    ['  01712345678  ', '+8801712345678'],
    ['017-1234-5678', '+8801712345678'],
    ['017 1234 5678', '+8801712345678'],
    ['+880 1712-345678', '+8801712345678'],
  ])('reads %o as %o', (raw, expected) => {
    expect(normalisePhone(raw)).toBe(expected);
  });

  it.each([
    ['', 'nothing'],
    ['0171234567', 'one digit short'],
    ['017123456789', 'one digit long'],
    ['01212345678', 'an operator prefix that does not exist'],
    ['02712345678', 'a landline'],
    ['abc', 'letters'],
  ])('refuses %o (%s)', (raw) => {
    expect(normalisePhone(raw)).toBeNull();
  });

  it.each(['3', '4', '5', '6', '7', '8', '9'])('accepts the 01%s operator prefix', (digit) => {
    expect(normalisePhone(`01${digit}12345678`)).toBe(`+8801${digit}12345678`);
  });
});

const form = (over: Partial<Parameters<typeof requiredState>[0]> = {}) => ({
  nameEN: 'Rahima Khatun',
  sex: 'female',
  date: parts({ day: '9', month: '3', year: '1987' }),
  dobSource: 'national_id',
  phone: '01712345678',
  consentReference: 'CONSENT-2026-0041',
  ...over,
});

describe('requiredState', () => {
  it('is satisfied by the clinical minimum', () => {
    const state = requiredState(form());
    expect(state).toEqual({
      nameEN: true,
      sex: true,
      birthDate: true,
      phone: true,
      consent: true,
    });
    expect(isComplete(state)).toBe(true);
  });

  it('wants a name of at least two characters, and not just spaces', () => {
    expect(requiredState(form({ nameEN: 'R' })).nameEN).toBe(false);
    expect(requiredState(form({ nameEN: '   ' })).nameEN).toBe(false);
    expect(requiredState(form({ nameEN: ' Ra ' })).nameEN).toBe(true);
  });

  it('wants a sex chosen rather than left at the placeholder', () => {
    expect(requiredState(form({ sex: '' })).sex).toBe(false);
  });

  it('wants both a readable date and where it came from', () => {
    expect(requiredState(form({ date: parts({ year: '19' }) })).birthDate).toBe(false);
    expect(requiredState(form({ dobSource: '' })).birthDate).toBe(false);
    // A year alone is still a date, so long as its source is recorded.
    expect(requiredState(form({ date: parts({ year: '1960' }) })).birthDate).toBe(true);
  });

  it('wants a mobile the server would accept', () => {
    expect(requiredState(form({ phone: '0171234567' })).phone).toBe(false);
  });

  it('wants the consent record, and does not count whitespace as one', () => {
    expect(requiredState(form({ consentReference: '' })).consent).toBe(false);
    expect(requiredState(form({ consentReference: '   ' })).consent).toBe(false);
  });

  it.each(['nameEN', 'sex', 'birthDate', 'phone', 'consent'] as const)(
    'is incomplete while %s is missing',
    (field) => {
      const state = { ...requiredState(form()), [field]: false };
      expect(isComplete(state)).toBe(false);
    },
  );
});

describe('documentNeedsExactDate', () => {
  it.each(['birth_certificate', 'national_id', 'passport', 'immunisation_card'])(
    'queries a %s that carries only a year or a month',
    (source) => {
      expect(documentNeedsExactDate(source, 'year')).toBe(true);
      expect(documentNeedsExactDate(source, 'month')).toBe(true);
      expect(documentNeedsExactDate(source, 'day')).toBe(false);
    },
  );

  it('says nothing about a date the patient simply remembers', () => {
    expect(documentNeedsExactDate('recalled', 'year')).toBe(false);
    expect(documentNeedsExactDate('estimated', 'month')).toBe(false);
  });

  it('says nothing while there is no date to judge', () => {
    expect(documentNeedsExactDate('passport', null)).toBe(false);
  });
});
