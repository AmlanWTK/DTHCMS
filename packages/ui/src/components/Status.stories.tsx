import type { Meta, StoryObj } from '@storybook/react';

import { clinicalStatusNames } from '@dthcms/design-tokens';

import { AlertBanner } from './AlertBanner';
import { Badge } from './Badge';
import { StatusPill } from './StatusPill';

const meta = {
  title: 'Clinical/Status',
  component: StatusPill,
  args: { status: 'normal' },
} satisfies Meta<typeof StatusPill>;

export default meta;
type Story = StoryObj<typeof meta>;

/**
 * All seven, tinted.
 *
 * Switch the theme and the language in the toolbar. Every pill keeps its icon and its
 * word in both — which is what makes them readable when the colours converge for an
 * operator with a colour vision deficiency.
 */
export const AllStatuses: Story = {
  render: () => (
    <div className="sb-row">
      {clinicalStatusNames.map((status) => (
        <StatusPill key={status} status={status} />
      ))}
    </div>
  ),
};

export const Solid: Story = {
  render: () => (
    <div className="sb-row">
      {clinicalStatusNames.map((status) => (
        <StatusPill key={status} status={status} emphasis="solid" />
      ))}
    </div>
  ),
};

export const Small: Story = {
  render: () => (
    <div className="sb-row">
      {clinicalStatusNames.map((status) => (
        <StatusPill key={status} status={status} size="sm" />
      ))}
    </div>
  ),
};

/** The most compact form still carries two signals: the icon stays, the word is announced. */
export const LabelHidden: Story = {
  render: () => (
    <div className="sb-row">
      {clinicalStatusNames.map((status) => (
        <StatusPill key={status} status={status} labelHidden />
      ))}
    </div>
  ),
};

export const Badges: Story = {
  render: () => (
    <div className="sb-row">
      <Badge>12</Badge>
      <Badge tone="brand">New</Badge>
      <Badge tone="info">Queued</Badge>
    </div>
  ),
};

export const Alerts: Story = {
  render: () => (
    <div className="sb-stack" style={{ maxWidth: '40rem' }}>
      <AlertBanner tone="critical" title="Critical value recorded">
        Fasting glucose 22.4 mmol/L. The physician has been notified.
      </AlertBanner>
      <AlertBanner tone="borderline" title="Close to a threshold">
        HbA1c 6.4%. Consider repeating in three months.
      </AlertBanner>
      <AlertBanner tone="stale" title="Last measured 14 months ago" />
      <AlertBanner tone="info" title="Synced" onDismiss={() => {}}>
        18 observations uploaded.
      </AlertBanner>
    </div>
  ),
};
