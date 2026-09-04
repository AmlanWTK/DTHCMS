'use client';

import { useTranslations } from 'next-intl';

import { Input, Select } from '@dthcms/ui';

import {
  ageOn,
  birthDateProblem,
  documentNeedsExactDate,
  readDate,
  type DateParts,
} from '@dthcms/shared-schemas';

/**
 * Date of birth, designed against the error rather than merely validating for it (CP32).
 *
 * [R-06] makes exact age clinically load-bearing: pediatric percentiles are computed from it.
 * The failure that matters is not a refused form but an accepted one — an operator types
 * 1085 for 1985, the record is created, and every percentile that patient ever gets is wrong
 * in a way nothing downstream can detect.
 *
 * Three things guard against it:
 *
 *   - **Three fields, not one.** A single text box invites 14/06/85, 06/14/85 and 14-6-1985,
 *     and the operator cannot see which the system read.
 *   - **The age, echoed in words, as the year is typed.** "941 years" is obvious in a way
 *     that "1085" is not, and it appears before the form is submitted rather than after.
 *   - **Day and month are optional.** A patient who knows only their birth year is ordinary
 *     here. Recording 1 January with `precision: year` is honest; demanding a day produces
 *     an invented one that a percentile calculation treats as exact.
 */
export function BirthDateField({
  value,
  onChange,
  source,
  onSourceChange,
  today,
  serverError,
}: {
  value: DateParts;
  onChange: (parts: DateParts) => void;
  source: string;
  onSourceChange: (source: string) => void;
  /** Injected so the echo is testable and so the clinic's calendar decides it. */
  today: Date;
  serverError?: string;
}) {
  const t = useTranslations('patients.register.birth');

  const parsed = readDate(value);
  const age = parsed ? ageOn(parsed.iso, parsed.precision, today) : null;
  const problem = parsed ? birthDateProblem(parsed.iso, today) : null;
  const mismatch = documentNeedsExactDate(source, parsed?.precision ?? null);

  const echo = (() => {
    if (serverError) return { tone: 'error' as const, text: serverError };
    if (value.year.length > 0 && value.year.length < 4) return null;
    if (!parsed && value.year !== '') return { tone: 'error' as const, text: t('unreadable') };
    if (problem === 'future') return { tone: 'error' as const, text: t('future') };
    if (problem === 'implausible') return { tone: 'error' as const, text: t('implausible') };
    if (!age) return null;
    // The echo itself. Months as well as years, because months are what make a wrong year
    // obvious on a child and years alone are what make it invisible.
    return {
      tone: 'ok' as const,
      text: age.approximate
        ? t('ageApproximate', { years: age.years })
        : t('age', { years: age.years, months: age.months }),
    };
  })();

  return (
    <div className="app-birth">
      <div className="app-birth__parts" role="group" aria-label={t('legend')}>
        <Input
          label={t('day')}
          data-testid="dob-day"
          inputMode="numeric"
          maxLength={2}
          autoComplete="off"
          value={value.day}
          placeholder={t('dayPlaceholder')}
          onChange={(event) => onChange({ ...value, day: digits(event.target.value, 2) })}
        />
        <Input
          label={t('month')}
          data-testid="dob-month"
          inputMode="numeric"
          maxLength={2}
          autoComplete="off"
          value={value.month}
          placeholder={t('monthPlaceholder')}
          onChange={(event) => onChange({ ...value, month: digits(event.target.value, 2) })}
        />
        <Input
          label={t('year')}
          data-testid="dob-year"
          inputMode="numeric"
          maxLength={4}
          autoComplete="off"
          required
          value={value.year}
          placeholder={t('yearPlaceholder')}
          onChange={(event) => onChange({ ...value, year: digits(event.target.value, 4) })}
          error={echo?.tone === 'error' ? ' ' : undefined}
        />
      </div>

      {/* The echo is a live region: it changes while the operator is still typing, and it
          is the whole point of the field. */}
      <p
        className="app-birth__echo"
        data-tone={echo?.tone}
        aria-live="polite"
        data-testid="age-echo"
      >
        {echo?.text ?? t('hint')}
      </p>

      <Select
        label={t('source')}
        data-testid="dob-source"
        description={t('sourceHelp')}
        required
        value={source}
        onChange={(event) => onSourceChange(event.target.value)}
        placeholder={t('sourcePlaceholder')}
        warning={mismatch ? t('documentIsExact') : undefined}
        options={[
          { value: 'birth_certificate', label: t('sources.birth_certificate') },
          { value: 'national_id', label: t('sources.national_id') },
          { value: 'passport', label: t('sources.passport') },
          { value: 'immunisation_card', label: t('sources.immunisation_card') },
          { value: 'patient_stated', label: t('sources.patient_stated') },
          { value: 'guardian_stated', label: t('sources.guardian_stated') },
          { value: 'estimated', label: t('sources.estimated') },
        ]}
      />
    </div>
  );
}

function digits(raw: string, max: number): string {
  return raw.replace(/[^0-9]/g, '').slice(0, max);
}
