import type { Meta, StoryObj } from '@storybook/react';
import { useState } from 'react';

import { Input } from './Input.js';
import { NumericInput } from './NumericInput.js';
import { Select } from './Select.js';

const meta = {
  title: 'Primitives/Form controls',
  component: Input,
  // `label` is required on every control, so the meta needs a default for stories that
  // render their own tree. That the type insists on it is the point: an unlabelled input
  // should not be constructible, in a story or anywhere else.
  args: { label: 'Label' },
} satisfies Meta<typeof Input>;

export default meta;
type Story = StoryObj<typeof meta>;

export const TextInput: Story = {
  render: () => (
    <div className="sb-stack">
      <Input label="Patient name" description="As written on the referral" />
      <Input label="National ID" required description="Twelve digits, no spaces" />
      <Input label="National ID" error="Enter twelve digits." defaultValue="1234" />
      <Input label="Phone" disabled defaultValue="+8801700000000" />
      <Input label="Search" labelHidden placeholder="Search patients" before="⌕" />
    </div>
  ),
};

/**
 * The clinically important one.
 *
 * Note the third and fourth fields. 22 mmol/L is warned about and still recorded — it is
 * not a typing mistake, it is a patient who needs attention. 900 is refused, because no
 * patient has a fasting glucose of 900 and the decimal point has slipped.
 */
export const Numeric: Story = {
  render: function NumericStory() {
    const [normal, setNormal] = useState('5.4');
    const [high, setHigh] = useState('22');
    const [impossible, setImpossible] = useState('900');
    const [empty, setEmpty] = useState('');

    return (
      <div className="sb-stack">
        <NumericInput
          label="Fasting glucose"
          unit="mmol/L"
          value={normal}
          onValueChange={setNormal}
          plausible={{ min: 3, max: 20 }}
          possible={{ min: 0, max: 60 }}
        />
        <NumericInput
          label="Fasting glucose"
          unit="mmol/L"
          value={high}
          onValueChange={setHigh}
          plausible={{ min: 3, max: 20 }}
          possible={{ min: 0, max: 60 }}
        />
        <NumericInput
          label="Fasting glucose"
          unit="mmol/L"
          value={impossible}
          onValueChange={setImpossible}
          plausible={{ min: 3, max: 20 }}
          possible={{ min: 0, max: 60 }}
        />
        <NumericInput
          label="Weight"
          unit="kg"
          value={empty}
          onValueChange={setEmpty}
          required
          description="To one decimal place"
        />
      </div>
    );
  },
};

export const Dropdown: Story = {
  render: () => (
    <div className="sb-stack">
      <Select
        label="Station"
        options={[
          { value: 'reception', label: 'Reception' },
          { value: 'vitals', label: 'Vitals' },
          { value: 'anthropometry', label: 'Anthropometry' },
          { value: 'lab', label: 'Laboratory' },
        ]}
      />
      <Select label="Station" required options={[{ value: 'vitals', label: 'Vitals' }]} />
      <Select
        label="Station"
        disabled
        options={[{ value: 'vitals', label: 'Vitals' }]}
        defaultValue="vitals"
      />
      <Select
        label="Station"
        error="Choose a station before continuing."
        options={[{ value: 'vitals', label: 'Vitals' }]}
      />
    </div>
  ),
};
