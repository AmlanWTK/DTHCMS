import { render, screen } from '@testing-library/react';
import { NextIntlClientProvider } from 'next-intl';
import type { ReactNode } from 'react';
import { describe, expect, it } from 'vitest';

import { DuplicateWarning, type DuplicateMatch } from '@/features/patients';

import bn from '../messages/bn.json';
import en from '../messages/en.json';

/**
 * The duplicate warning as a registration officer meets it (CP30).
 *
 * What is worth asserting here is not that the component renders — it is the two
 * decisions that make it usable on a busy morning: that "these are different people" is
 * the primary action on a review warning, and that the reasons appear in the reader's own
 * language. Both are things a screenshot cannot prove and a regression would not announce.
 */

function withIntl(node: ReactNode, locale: 'en' | 'bn' = 'en') {
  return render(
    <NextIntlClientProvider locale={locale} messages={locale === 'bn' ? bn : en}>
      {node}
    </NextIntlClientProvider>,
  );
}

const candidate = {
  patient_id: 'p-2',
  clinical_id: 'DTHC-FRD-2026-000482',
  name_en: 'Muhammad Raheem',
  name_bn: 'মোহাম্মদ রহিম',
  sex: 'male' as const,
  birth_date: '1985-01-01',
  phone_masked: '•••• 2202',
  district: 'Faridpur',
  registered_at: '2026-08-12T05:05:00Z',
  score: 0.86,
  deterministic: false,
  reasons: [
    {
      code: 'similar_name',
      message: 'The name sounds the same: Muhammad Raheem.',
      message_bn: 'নাম উচ্চারণ একই: মোহাম্মদ রহিম।',
    },
  ],
};

const review: DuplicateMatch = { verdict: 'review', candidates: [candidate] };
const blocked: DuplicateMatch = {
  verdict: 'blocked',
  candidates: [
    {
      ...candidate,
      deterministic: true,
      score: 1,
      reasons: [
        {
          code: 'same_identifier',
          message: 'This national ID already belongs to DTHC-FRD-2026-000482.',
          message_bn: 'এই জাতীয় পরিচয়পত্র নম্বর ইতিমধ্যে DTHC-FRD-2026-000482-এর নামে নিবন্ধিত।',
        },
      ],
    },
  ],
};

describe('the duplicate warning', () => {
  it('says nothing when there is nothing to say', () => {
    const { container } = withIntl(
      <DuplicateWarning match={{ verdict: 'clear', candidates: [] }} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('makes "these are different people" the primary action on a review', () => {
    // The decision this UI has to get right. In this register two people genuinely named
    // Md Rahim, born in the same year, in the same upazila, are ordinary — and an
    // interface that makes "different people" awkward produces wrong merges, which are
    // worse than duplicates.
    withIntl(<DuplicateWarning match={review} onDismiss={() => {}} />);
    const different = screen.getByRole('button', { name: /different people/i });
    expect(different.className).toMatch(/primary/);
  });

  it('offers no dismissal at all when the match is deterministic', () => {
    // A national ID that already belongs to somebody is not a judgement call, so there is
    // nothing for the officer to overrule.
    withIntl(<DuplicateWarning match={blocked} onDismiss={() => {}} />);
    expect(screen.queryByRole('button', { name: /different people/i })).toBeNull();
  });

  it('shows the reasons in the reader’s language', () => {
    withIntl(<DuplicateWarning match={review} />, 'bn');
    expect(screen.getByText('নাম উচ্চারণ একই: মোহাম্মদ রহিম।')).toBeTruthy();
    expect(screen.queryByText(/The name sounds the same/)).toBeNull();
  });

  it('never shows a whole telephone number', () => {
    // Read at a desk with the patient standing at it, and whoever is next in the queue
    // can see the screen.
    withIntl(<DuplicateWarning match={review} />);
    expect(screen.getByText('•••• 2202')).toBeTruthy();
    expect(screen.queryByText(/\+8801/)).toBeNull();
  });

  it('marks the panel with the verdict, so the shell can style it', () => {
    const { container } = withIntl(<DuplicateWarning match={blocked} />);
    expect(container.querySelector('[data-verdict="blocked"]')).toBeTruthy();
  });
});
