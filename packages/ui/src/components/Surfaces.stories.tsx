import type { Meta, StoryObj } from '@storybook/react';

import { Button } from './Button';
import { Card } from './Card';
import { EmptyState } from './EmptyState';
import { ErrorState } from './ErrorState';
import { Skeleton } from './Skeleton';
import { StatusPill } from './StatusPill';

const meta = {
  title: 'Primitives/Surfaces',
  component: Card,
  // Card requires children, so stories that render their own tree still need a default.
  args: { children: null },
} satisfies Meta<typeof Card>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Cards: Story = {
  render: () => (
    <div className="sb-grid">
      <Card elevation="flat">Flat</Card>
      <Card elevation="raised">Raised</Card>
      <Card elevation="floating">Floating</Card>
      <Card header="Latest readings" footer={<Button size="sm">See all</Button>}>
        <div className="sb-row">
          <StatusPill status="high" />
          <span>Fasting glucose 8.2 mmol/L</span>
        </div>
      </Card>
    </div>
  ),
};

/** The four states every screen needs, side by side. */
export const States: Story = {
  render: () => (
    <div
      className="sb-grid"
      style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(20rem, 1fr))' }}
    >
      <Card>
        <Skeleton lines={4} />
      </Card>
      <Card>
        <EmptyState action={<Button size="sm">Register a patient</Button>}>
          No patients in the queue.
        </EmptyState>
      </Card>
      <Card>
        <ErrorState onRetry={() => {}} correlationId="0190a3f2-8c11-7e42" />
      </Card>
      <Card>
        <ErrorState variant="offline" />
      </Card>
    </div>
  ),
};

/**
 * Empty and error are different components on purpose.
 *
 * "No patients in the queue" means the clinic is quiet. "Could not load the queue" means
 * there may be twenty people waiting and the screen cannot see them. Showing the first
 * when the second is true is how a station goes unattended.
 */
export const EmptyIsNotError: Story = {
  render: () => (
    <div
      className="sb-grid"
      style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(20rem, 1fr))' }}
    >
      <Card>
        <EmptyState>No patients in the queue.</EmptyState>
      </Card>
      <Card>
        <ErrorState onRetry={() => {}}>The queue could not be loaded.</ErrorState>
      </Card>
    </div>
  ),
};

export const Loading: Story = {
  render: () => (
    <div className="sb-stack">
      <Skeleton lines={3} />
      <Skeleton shape="block" />
      <Skeleton shape="circle" />
    </div>
  ),
};
