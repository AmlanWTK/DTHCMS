import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { clinicalStatusNames } from '@dthcms/design-tokens';
import type { Language } from '@dthcms/design-tokens';

import {
  AlertBanner,
  Badge,
  Button,
  Card,
  EmptyState,
  ErrorState,
  Input,
  LanguageProvider,
  NumericInput,
  Select,
  Skeleton,
  StatusPill,
} from '../src/index';
import { checkA11y, describeViolations } from './axe';

/**
 * CP09 acceptance criterion 2: every primitive renders correctly in Bangla and English.
 *
 * Every case below runs twice, once per language, because the failure this guards against
 * is not a component that breaks in Bangla — it is one that was only ever looked at in
 * English. A missing translation, a label that reads "undefined", a control whose
 * accessible name vanishes when the language changes: all silent, all only visible if
 * something renders the second language on purpose.
 */

const LANGUAGES: Language[] = ['en', 'bn'];

function renderIn(language: Language, ui: React.ReactElement) {
  return render(<LanguageProvider language={language}>{ui}</LanguageProvider>);
}

/** Every primitive, in the states worth rendering. Named so a failure says which one. */
const CASES: Array<{ name: string; render: () => React.ReactElement }> = [
  { name: 'Button primary', render: () => <Button variant="primary">Save</Button> },
  { name: 'Button loading', render: () => <Button loading>Save</Button> },
  { name: 'Button disabled', render: () => <Button disabled>Save</Button> },
  { name: 'Button danger', render: () => <Button variant="danger">Delete</Button> },
  {
    name: 'Button icon only',
    render: () => <Button aria-label="Close" iconStart="x" />,
  },
  {
    name: 'Input',
    render: () => <Input label="National ID" description="Twelve digits" />,
  },
  {
    name: 'Input with error',
    render: () => <Input label="National ID" error="Enter twelve digits." />,
  },
  { name: 'Input disabled', render: () => <Input label="National ID" disabled /> },
  {
    name: 'NumericInput',
    render: () => (
      <NumericInput label="Fasting glucose" unit="mmol/L" value="5.4" onValueChange={() => {}} />
    ),
  },
  {
    name: 'NumericInput warning',
    render: () => (
      <NumericInput
        label="Fasting glucose"
        unit="mmol/L"
        value="22"
        plausible={{ min: 3, max: 20 }}
        onValueChange={() => {}}
      />
    ),
  },
  {
    name: 'NumericInput error',
    render: () => (
      <NumericInput
        label="Fasting glucose"
        unit="mmol/L"
        value="900"
        possible={{ min: 0, max: 60 }}
        onValueChange={() => {}}
      />
    ),
  },
  {
    name: 'Select',
    render: () => (
      <Select
        label="Station"
        options={[
          { value: 'vitals', label: 'Vitals' },
          { value: 'lab', label: 'Laboratory' },
        ]}
      />
    ),
  },
  { name: 'Card', render: () => <Card header="Latest readings">Body</Card> },
  { name: 'Badge', render: () => <Badge tone="brand">12</Badge> },
  { name: 'Skeleton', render: () => <Skeleton lines={3} /> },
  { name: 'EmptyState', render: () => <EmptyState>No patients in the queue.</EmptyState> },
  {
    name: 'ErrorState',
    render: () => <ErrorState onRetry={() => {}} correlationId="0190a000-7000" />,
  },
  { name: 'ErrorState offline', render: () => <ErrorState variant="offline" /> },
  {
    name: 'AlertBanner critical',
    render: () => <AlertBanner tone="critical" title="Critical value recorded" />,
  },
  {
    name: 'AlertBanner dismissible',
    render: () => <AlertBanner tone="info" title="Synced" onDismiss={() => {}} />,
  },
  ...clinicalStatusNames.map((status) => ({
    name: `StatusPill ${status}`,
    render: () => <StatusPill status={status} />,
  })),
  ...clinicalStatusNames.map((status) => ({
    name: `StatusPill ${status} solid`,
    render: () => <StatusPill status={status} emphasis="solid" />,
  })),
];

describe('every primitive, in both languages', () => {
  for (const language of LANGUAGES) {
    describe(language, () => {
      for (const testCase of CASES) {
        it(`${testCase.name} renders`, () => {
          const { container } = renderIn(language, testCase.render());
          expect(container.firstChild).not.toBeNull();
          // "undefined" or "[object Object]" in the output means a translation lookup or
          // a token read failed - which renders without throwing and looks like content.
          expect(container.textContent).not.toContain('undefined');
          expect(container.textContent).not.toContain('[object Object]');
        });

        it(`${testCase.name} has no accessibility violations`, async () => {
          const { container } = renderIn(language, testCase.render());
          const results = await checkA11y(container);
          expect(results.violations.length, describeViolations(results)).toBe(0);
        });
      }
    });
  }
});

describe('the language actually reaches the DOM', () => {
  it('sets lang, so the browser shapes the right script', () => {
    // Not cosmetic. The lang attribute drives font selection through the token
    // stylesheet, tells the browser which script to shape, and tells a screen reader
    // which language to pronounce. A component that changed only its text would look
    // right and be announced in English.
    const { container } = renderIn('bn', <Button>Save</Button>);
    expect(container.querySelector('[lang="bn"]')).not.toBeNull();
  });

  it('translates built-in copy', () => {
    const english = renderIn('en', <EmptyState />);
    expect(english.container.textContent).toContain('Nothing here yet');

    const bangla = renderIn('bn', <EmptyState />);
    expect(bangla.container.textContent).toContain('এখনও কিছু নেই');
  });

  it('translates every clinical status label', () => {
    for (const status of clinicalStatusNames) {
      const english = renderIn('en', <StatusPill status={status} />);
      const bangla = renderIn('bn', <StatusPill status={status} />);

      expect(english.container.textContent?.trim(), status).not.toBe('');
      expect(bangla.container.textContent?.trim(), status).not.toBe('');
      expect(bangla.container.textContent, `${status} was not translated`).not.toBe(
        english.container.textContent,
      );
    }
  });
});
