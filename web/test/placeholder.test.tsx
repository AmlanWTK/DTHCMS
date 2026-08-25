import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { PageHeader } from '@/components/PageHeader';
import { PlaceholderPage } from '@/components/PlaceholderPage';

import { renderWithProviders } from './render';

/**
 * The title block and the not-built-yet screen.
 *
 * The placeholder is worth testing for one reason that is not obvious: it is a status
 * report, not a "coming soon". Naming the checkpoint that fills a screen is what lets a
 * reviewer walking the shell tell whether the plan is on track. A placeholder that lost
 * its checkpoint reference would still look perfectly fine.
 */

describe('the title block', () => {
  it('renders the title as the page heading', () => {
    // One h1 per screen, from one component, so headings cannot drift apart between
    // route groups — which matters for screen readers more than for looks.
    renderWithProviders(<PageHeader title="Queue" />);
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Queue');
  });

  it('omits the description entirely when there is none', () => {
    const { container } = renderWithProviders(<PageHeader title="Queue" />);
    expect(container.querySelector('.app-page__description')).toBeNull();
  });

  it('renders the description when given one', () => {
    renderWithProviders(<PageHeader title="Queue" description="Patients waiting" />);
    expect(screen.getByText('Patients waiting')).toBeInTheDocument();
  });
});

describe('a screen that does not exist yet', () => {
  // Real keys, not invented ones. next-intl only warns on an unresolved key, so a test
  // written against a key that does not exist passes while asserting nothing — which is
  // exactly what the first draft of this file did.
  const props = {
    titleKey: 'page.patients.title',
    descriptionKey: 'page.patients.description',
    areaKey: 'nav.patients',
    checkpoint: 'CP32',
  };

  it('names the checkpoint that fills it', () => {
    // The whole point. "Coming soon" tells a reviewer nothing about whether the
    // checkpoint is late.
    renderWithProviders(<PlaceholderPage {...props} />);
    expect(screen.getByText(/CP32/)).toBeInTheDocument();
  });

  it('still renders a proper page heading, so the shell reads consistently', () => {
    renderWithProviders(<PlaceholderPage {...props} />);
    expect(screen.getByRole('heading', { level: 1 })).toBeInTheDocument();
  });

  it('says the same thing in Bangla', () => {
    // Not a translation check — that is the message-completeness test's job. This is
    // that the checkpoint reference survives the switch, since it is the one token in
    // the sentence that must not be translated.
    renderWithProviders(<PlaceholderPage {...props} />, { locale: 'bn' });
    expect(screen.getByText(/CP32/)).toBeInTheDocument();
  });
});
