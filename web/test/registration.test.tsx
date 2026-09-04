import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { NextIntlClientProvider } from 'next-intl';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  ageOn,
  birthDateProblem,
  documentNeedsExactDate,
  isComplete,
  normalisePhone,
  readDate,
  requiredState,
} from '@dthcms/shared-schemas';

import { RegistrationForm } from '@/features/patients';

import bn from '../messages/bn.json';
import en from '../messages/en.json';

/**
 * The registration desk (CP32).
 *
 * The date of birth gets most of the attention here, because it is the field where being
 * *wrong* is worse than being refused: a mistyped year is accepted, the record is created,
 * and every pediatric percentile that patient ever gets is wrong in a way nothing
 * downstream can detect [R-06].
 */

const TODAY = new Date('2026-09-03T00:00:00Z');

describe('reading three fields as a date', () => {
  it('accepts a year alone, because that is often all the patient knows', () => {
    expect(readDate({ day: '', month: '', year: '1958' })).toEqual({
      iso: '1958-01-01',
      precision: 'year',
    });
  });

  it('accepts a month without a day', () => {
    expect(readDate({ day: '', month: '06', year: '1985' })).toEqual({
      iso: '1985-06-01',
      precision: 'month',
    });
  });

  it('reads a complete date as exact', () => {
    expect(readDate({ day: '14', month: '06', year: '1985' })).toEqual({
      iso: '1985-06-14',
      precision: 'day',
    });
  });

  it('refuses a day with no month, which says nothing', () => {
    expect(readDate({ day: '14', month: '', year: '1985' })).toBeNull();
  });

  it('refuses dates that do not exist', () => {
    // Round-tripped rather than checked against a table of month lengths, which is one
    // leap-year rule away from being wrong.
    expect(readDate({ day: '31', month: '02', year: '1985' })).toBeNull();
    expect(readDate({ day: '29', month: '02', year: '1985' })).toBeNull();
    expect(readDate({ day: '29', month: '02', year: '1984' })).not.toBeNull();
    expect(readDate({ day: '00', month: '06', year: '1985' })).toBeNull();
    expect(readDate({ day: '14', month: '13', year: '1985' })).toBeNull();
  });

  it('refuses a year that is not four digits, so the echo does not flicker', () => {
    expect(readDate({ day: '', month: '', year: '85' })).toBeNull();
    expect(readDate({ day: '', month: '', year: '198' })).toBeNull();
  });
});

describe('the age echo', () => {
  it('gives years and months, because months are what make a wrong year obvious on a child', () => {
    expect(ageOn('2020-06-14', 'day', TODAY)).toEqual({ years: 6, months: 2, approximate: false });
    expect(ageOn('1985-06-14', 'day', TODAY)).toEqual({ years: 41, months: 2, approximate: false });
  });

  it('marks itself approximate when the date is not exact', () => {
    expect(ageOn('1958-01-01', 'year', TODAY)?.approximate).toBe(true);
  });

  it('handles a birthday that has not come round yet this year', () => {
    expect(ageOn('1985-12-14', 'day', TODAY)).toEqual({ years: 40, months: 8, approximate: false });
  });
});

describe('dates the server will refuse', () => {
  it('catches the future and the implausible before a round trip', () => {
    expect(birthDateProblem('2030-01-01', TODAY)).toBe('future');
    expect(birthDateProblem('1085-06-14', TODAY)).toBe('implausible');
    expect(birthDateProblem('1985-06-14', TODAY)).toBeNull();
  });

  it('flags a document with a date that is not exact', () => {
    // "Birth certificate, year only" is almost always a transcription error.
    expect(documentNeedsExactDate('birth_certificate', 'year')).toBe(true);
    expect(documentNeedsExactDate('national_id', 'month')).toBe(true);
    expect(documentNeedsExactDate('patient_stated', 'year')).toBe(false);
    expect(documentNeedsExactDate('birth_certificate', 'day')).toBe(false);
  });
});

describe('telephone numbers', () => {
  it('accepts every form the desk types and stores one', () => {
    for (const typed of [
      '01712345678',
      '+8801712345678',
      '8801712345678',
      '1712345678',
      '017 1234 5678',
    ]) {
      expect(normalisePhone(typed)).toBe('+8801712345678');
    }
  });

  it('refuses what is not a Bangladeshi mobile', () => {
    for (const bad of ['', '0171234567', '0288123456', '+919812345678', '০১৭১২৩৪৫৬৭৮']) {
      expect(normalisePhone(bad)).toBeNull();
    }
  });
});

describe('the required set', () => {
  const complete = {
    nameEN: 'Rahima Begum',
    sex: 'female',
    date: { day: '14', month: '06', year: '1985' },
    dobSource: 'national_id',
    phone: '01712345678',
    consentReference: 'consent_2026_0001',
  };

  it('is the clinical minimum and nothing more', () => {
    expect(isComplete(requiredState(complete))).toBe(true);
  });

  it('names each missing piece', () => {
    expect(requiredState({ ...complete, nameEN: 'R' }).nameEN).toBe(false);
    expect(requiredState({ ...complete, sex: '' }).sex).toBe(false);
    expect(requiredState({ ...complete, dobSource: '' }).birthDate).toBe(false);
    expect(requiredState({ ...complete, phone: '12345' }).phone).toBe(false);
    expect(requiredState({ ...complete, consentReference: '  ' }).consent).toBe(false);
  });
});

// --- the form itself ---

function withIntl(node: ReactNode, locale: 'en' | 'bn' = 'en') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <NextIntlClientProvider locale={locale} messages={locale === 'bn' ? bn : en}>
      <QueryClientProvider client={client}>{node}</QueryClientProvider>
    </NextIntlClientProvider>,
  );
}

describe('the registration form', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('{}', { status: 200 })),
    );
  });

  it('echoes the age as the year is typed', async () => {
    const user = userEvent.setup();
    withIntl(<RegistrationForm today={TODAY} />);

    await user.type(screen.getByLabelText(/^Year\b/), '1985');
    await user.type(screen.getByLabelText(/^Month\b/), '06');
    await user.type(screen.getByLabelText(/^Day\b/), '14');

    await waitFor(() => {
      expect(screen.getByTestId('age-echo').textContent).toMatch(/41 years, 2 months/);
    });
  });

  it('makes a mistyped year obvious before the form is submitted', async () => {
    // The whole point of the echo. "1085" is not visibly wrong; "over 130" is.
    const user = userEvent.setup();
    withIntl(<RegistrationForm today={TODAY} />);

    await user.type(screen.getByLabelText(/^Year\b/), '1085');
    await waitFor(() => {
      expect(screen.getByTestId('age-echo').textContent).toMatch(/over 130/i);
    });
  });

  it('says "about" when only the year is known', async () => {
    const user = userEvent.setup();
    withIntl(<RegistrationForm today={TODAY} />);

    await user.type(screen.getByLabelText(/^Year\b/), '1958');
    await waitFor(() => {
      expect(screen.getByTestId('age-echo').textContent).toMatch(/About 68 years old/);
    });
  });

  it('names what is still missing rather than only disabling the button', async () => {
    const user = userEvent.setup();
    withIntl(<RegistrationForm today={TODAY} />);

    const save = screen.getByRole('button', { name: /^Register$/ });
    expect(save).toBeDisabled();
    expect(screen.getByText(/Still needed/)).toBeTruthy();

    await user.type(screen.getByLabelText(/Name \(English\)/), 'Rahima Begum');
    await waitFor(() => {
      expect(screen.getByText(/Still needed/).textContent).not.toMatch(/English name/);
    });
  });

  it('is in Bangla when the interface is', () => {
    withIntl(<RegistrationForm today={TODAY} />, 'bn');
    expect(screen.getByText('কে এই ব্যক্তি')).toBeTruthy();
    expect(screen.getByText('সম্মতি')).toBeTruthy();
  });
});
