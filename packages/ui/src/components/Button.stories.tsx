import type { Meta, StoryObj } from '@storybook/react';

import { Button } from './Button.js';

const meta = {
  title: 'Primitives/Button',
  component: Button,
  args: { children: 'Save observation' },
} satisfies Meta<typeof Button>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Variants: Story = {
  render: (args) => (
    <div className="sb-row">
      <Button {...args} variant="primary" />
      <Button {...args} variant="secondary" />
      <Button {...args} variant="quiet" />
      <Button {...args} variant="danger" children="Delete" />
    </div>
  ),
};

export const Sizes: Story = {
  render: (args) => (
    <div className="sb-row">
      <Button {...args} size="sm" />
      <Button {...args} size="md" />
      <Button {...args} size="lg" />
    </div>
  ),
};

/** Loading disables. A save button that stays live is how one observation becomes two. */
export const Loading: Story = {
  args: { loading: true, variant: 'primary', loadingLabel: 'Saving…' },
};

export const Disabled: Story = { args: { disabled: true, variant: 'primary' } };

export const WithIcons: Story = {
  render: (args) => (
    <div className="sb-row">
      <Button {...args} iconStart="check" variant="primary" />
      <Button {...args} iconEnd="chevron-down" />
      <Button aria-label="Close" iconStart="x" />
    </div>
  ),
};

/** Full width, as the primary action on a station form. */
export const Block: Story = { args: { block: true, variant: 'primary' } };
