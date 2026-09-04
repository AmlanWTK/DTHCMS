import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { NextIntlClientProvider } from 'next-intl';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import bn from '../messages/bn.json';
import en from '../messages/en.json';

const listConsents = vi.hoisted(() => vi.fn());
const consentTemplates = vi.hoisted(() => vi.fn());
const grantConsent = vi.hoisted(() => vi.fn());
const revokeConsent = vi.hoisted(() => vi.fn());
const evidenceUploadURL = vi.hoisted(() => vi.fn());

vi.mock('@/features/consent/api/consent', async () => {
  const actual = await vi.importActual<typeof import('@/features/consent/api/consent')>(
    '@/features/consent/api/consent',
  );
  return {
    ...actual,
    listConsents,
    consentTemplates,
    grantConsent,
    revokeConsent,
    evidenceUploadURL,
  };
});

const { ConsentPanel } = await import('@/features/consent/components/ConsentPanel');
const { bounds, hasMark } = await import('@/features/consent/lib/signature');

/**
 * The consent panel (CP36, §15.1).
 *
 * The assertions are the decisions, not the rendering: that all five consents appear
 * including the ones nobody asked about, that "never asked" is not drawn as a refusal, that
 * the wording is on screen before a consent can be recorded, and that withdrawing is one
 * click with an optional reason.
 */

function withIntl(node: ReactNode, locale: 'en' | 'bn' = 'en') {
  return render(
    <NextIntlClientProvider locale={locale} messages={locale === 'bn' ? bn : en}>
      {node}
    </NextIntlClientProvider>,
  );
}

const template = (kind: string) => ({
  consent_type: kind,
  version: 1,
  language: 'en',
  title: `${kind} consent`,
  body: `Placeholder wording for ${kind}. Pending D-02.`,
  digest: 'a'.repeat(64),
  status: 'active',
});

const TEMPLATES = ['care', 'communication', 'research', 'ai_processing', 'outreach'].map(template);

beforeEach(() => {
  listConsents.mockReset();
  consentTemplates.mockReset();
  grantConsent.mockReset();
  revokeConsent.mockReset();
  consentTemplates.mockResolvedValue(TEMPLATES);
  listConsents.mockResolvedValue([
    {
      consent_type: 'care',
      status: 'granted',
      template_version: 1,
      language: 'en',
      capture_method: 'signature',
      granted_at: '2026-09-14T04:42:00Z',
      granted_by_code: 'R001',
      has_evidence: true,
    },
    {
      consent_type: 'communication',
      status: 'revoked',
      template_version: 1,
      language: 'bn',
      capture_method: 'verbal_attested',
      granted_at: '2026-08-01T04:42:00Z',
      revoked_at: '2026-09-10T04:42:00Z',
      revoke_reason: 'The patient asked us to stop texting.',
      has_evidence: false,
    },
  ]);
});

describe('the consent panel', () => {
  it('lists all five consents, including the ones nobody asked about', async () => {
    withIntl(<ConsentPanel patientId="p-1" />);
    await screen.findByTestId('consent-panel');

    expect(screen.getByTestId('consent-care-status')).toHaveTextContent('Given');
    expect(screen.getByTestId('consent-communication-status')).toHaveTextContent('Withdrawn');
    // The three nobody has been asked about. A list of only what exists cannot show a desk
    // what it has not done.
    for (const kind of ['research', 'ai_processing', 'outreach']) {
      expect(screen.getByTestId(`consent-${kind}-status`)).toHaveTextContent('Not asked');
    }
  });

  it('distinguishes never asked from withdrawn', async () => {
    withIntl(<ConsentPanel patientId="p-1" />);
    await screen.findByTestId('consent-panel');

    const rows = document.querySelectorAll('.app-consent__row');
    const states = Array.from(rows).map((row) => row.getAttribute('data-status'));
    expect(states).toEqual(['granted', 'revoked', 'absent', 'absent', 'absent']);
  });

  it('shows the wording before the consent can be recorded', async () => {
    const user = userEvent.setup();
    withIntl(<ConsentPanel patientId="p-1" />);
    await screen.findByTestId('consent-panel');

    await user.click(screen.getByTestId('consent-research-take'));
    const wording = await screen.findByTestId('consent-wording');
    expect(wording).toHaveTextContent('Placeholder wording for research');
    expect(wording).toHaveTextContent('Version 1');
  });

  it('will not offer to take a consent with no approved wording', async () => {
    consentTemplates.mockResolvedValue([]);
    withIntl(<ConsentPanel patientId="p-1" />);
    await screen.findByTestId('consent-panel');

    expect(screen.getByTestId('consent-research-take')).toBeDisabled();
    expect(screen.getByText(/approved wording is not loaded/i)).toBeInTheDocument();
  });

  it('needs a witness before a spoken consent can be recorded', async () => {
    const user = userEvent.setup();
    withIntl(<ConsentPanel patientId="p-1" />);
    await screen.findByTestId('consent-panel');

    await user.click(screen.getByTestId('consent-research-take'));
    await user.click(screen.getByTestId('method-verbal_attested'));
    expect(screen.getByTestId('consent-record')).toBeDisabled();

    await user.type(screen.getByTestId('consent-witness'), 'Shirin Akter');
    expect(screen.getByTestId('consent-record')).toBeEnabled();
  });

  it('needs the form number before a paper consent can be recorded', async () => {
    const user = userEvent.setup();
    withIntl(<ConsentPanel patientId="p-1" />);
    await screen.findByTestId('consent-panel');

    await user.click(screen.getByTestId('consent-research-take'));
    await user.click(screen.getByTestId('method-paper_form'));
    expect(screen.getByTestId('consent-record')).toBeDisabled();

    await user.type(screen.getByTestId('consent-paper-reference'), 'CONSENT/2026/0137');
    expect(screen.getByTestId('consent-record')).toBeEnabled();
  });

  it('never sends a template version — the server decides which words were shown', async () => {
    const user = userEvent.setup();
    grantConsent.mockResolvedValue({
      consent_type: 'research',
      status: 'granted',
      has_evidence: false,
    });
    withIntl(<ConsentPanel patientId="p-1" />);
    await screen.findByTestId('consent-panel');

    await user.click(screen.getByTestId('consent-research-take'));
    await user.click(screen.getByTestId('method-verbal_attested'));
    await user.type(screen.getByTestId('consent-witness'), 'Shirin Akter');
    await user.click(screen.getByTestId('consent-record'));

    await waitFor(() => expect(grantConsent).toHaveBeenCalled());
    const [, body] = grantConsent.mock.calls[0]!;
    expect(body).not.toHaveProperty('template_version');
    expect(body).not.toHaveProperty('template_digest');
    expect(body.consent_type).toBe('research');
    expect(body.capture_method).toBe('verbal_attested');
  });

  it('withdraws in one click, with the reason optional', async () => {
    const user = userEvent.setup();
    revokeConsent.mockResolvedValue({
      consent_type: 'care',
      status: 'revoked',
      has_evidence: false,
    });
    withIntl(<ConsentPanel patientId="p-1" />);
    await screen.findByTestId('consent-panel');

    await user.click(screen.getByTestId('consent-care-revoke'));
    // Enabled with no reason typed: a patient withdrawing consent does not owe anybody an
    // explanation, and a required field would be filled in with "revoked".
    const confirm = screen.getByTestId('consent-revoke-confirm');
    expect(confirm).toBeEnabled();
    await user.click(confirm);

    await waitFor(() => expect(revokeConsent).toHaveBeenCalled());
    expect(revokeConsent.mock.calls[0]![2]).toEqual({ reason: undefined });
  });

  it('reads in Bangla', async () => {
    withIntl(<ConsentPanel patientId="p-1" />, 'bn');
    await screen.findByTestId('consent-panel');
    expect(screen.getByText('কল ও এসএমএস')).toBeInTheDocument();
    expect(screen.getByTestId('consent-research-status')).toHaveTextContent('জিজ্ঞাসা করা হয়নি');
  });
});

describe('the signature', () => {
  it('treats a tap as no mark at all', () => {
    expect(hasMark([[{ x: 1, y: 1 }]])).toBe(false);
    expect(hasMark([Array.from({ length: 8 }, (_, i) => ({ x: i, y: i }))])).toBe(true);
  });

  it('measures the ink rather than the box it was drawn in', () => {
    expect(bounds([])).toBeNull();
    expect(
      bounds([
        [
          { x: 10, y: 20 },
          { x: 40, y: 60 },
        ],
      ]),
    ).toEqual({ x: 10, y: 20, w: 30, h: 40 });
  });
});
