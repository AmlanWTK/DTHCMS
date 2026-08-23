import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';

import { clinicalStatusNames, clinicalStatuses } from '@dthcms/design-tokens';

import {
  AlertBanner,
  Button,
  ErrorState,
  Input,
  LanguageProvider,
  NumericInput,
  Skeleton,
  StatusPill,
} from '../src/index.js';

describe('Button', () => {
  it('does not submit the form it happens to be inside', async () => {
    // The HTML default for a button inside a form is type="submit". In a clinical form
    // that means a "Add another reading" button silently records the observation.
    const onSubmit = vi.fn((event: React.FormEvent) => event.preventDefault());
    render(
      <form onSubmit={onSubmit}>
        <Button>Add another</Button>
      </form>,
    );

    await userEvent.click(screen.getByRole('button', { name: 'Add another' }));
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it('submits when asked to', async () => {
    const onSubmit = vi.fn((event: React.FormEvent) => event.preventDefault());
    render(
      <form onSubmit={onSubmit}>
        <Button type="submit">Save</Button>
      </form>,
    );

    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it('cannot be clicked twice while it is saving', async () => {
    // The double-write. A save button that stays live during the request is how one
    // observation becomes two rows in an append-only ledger that cannot delete either.
    const onClick = vi.fn();
    render(
      <Button loading onClick={onClick}>
        Save
      </Button>,
    );

    const button = screen.getByRole('button');
    await userEvent.click(button, { pointerEventsCheck: 0 });

    expect(onClick).not.toHaveBeenCalled();
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute('aria-busy', 'true');
  });
});

describe('NumericInput', () => {
  function Harness(props: Partial<React.ComponentProps<typeof NumericInput>> = {}) {
    const [value, setValue] = useState(props.value ?? '');
    return (
      <NumericInput
        label="Fasting glucose"
        unit="mmol/L"
        {...props}
        value={value}
        onValueChange={setValue}
      />
    );
  }

  it('refuses letters as they are typed', async () => {
    render(<Harness />);
    const input = screen.getByLabelText(/fasting glucose/i);

    await userEvent.type(input, '5.4abc');
    expect(input).toHaveValue('5.4');
  });

  it('allows a partly-typed number', async () => {
    // People type left to right: "12." exists on the way to "12.5". Rejecting it moves
    // the caret and loses the keystroke.
    render(<Harness />);
    const input = screen.getByLabelText(/fasting glucose/i);

    await userEvent.type(input, '12.');
    expect(input).toHaveValue('12.');
  });

  it('warns about an unusual reading without refusing it', async () => {
    // The distinction the component exists for. 22 mmol/L is not a typing mistake; it is
    // a patient who needs attention, and an interface that refuses to record it loses
    // the finding.
    render(<Harness value="22" plausible={{ min: 3, max: 20 }} />);

    const input = screen.getByLabelText(/fasting glucose/i);
    expect(input).toHaveValue('22');
    expect(input).not.toHaveAttribute('aria-invalid');

    const message = screen.getByRole('status');
    expect(message.textContent).toMatch(/outside the usual range/i);
  });

  it('rejects an impossible reading', async () => {
    render(<Harness value="900" possible={{ min: 0, max: 60 }} />);

    const input = screen.getByLabelText(/fasting glucose/i);
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(screen.getByRole('alert').textContent).toMatch(/between 0 and 60/i);
  });

  it('marks the value for print so it is not split across a page break', () => {
    render(<Harness value="5.4" />);
    expect(screen.getByLabelText(/fasting glucose/i)).toHaveAttribute('data-clinical-value');
  });

  it('announces the unit', () => {
    // Without this the field is announced as "Fasting glucose, 5.4" and the operator has
    // to know from memory whether that is mmol/L or mg/dL - a factor of eighteen.
    render(<Harness value="5.4" />);
    const input = screen.getByLabelText(/fasting glucose/i);
    const describedBy = input.getAttribute('aria-describedby') ?? '';
    const described = describedBy
      .split(' ')
      .map((id) => document.getElementById(id)?.textContent ?? '')
      .join(' ');
    expect(described).toContain('mmol/L');
  });

  it('does not use a number input', () => {
    // type="number" reports an empty value for unparseable input, so "12..5" and "nothing
    // entered" become the same state. For a clinical measurement they must never be.
    render(<Harness value="5.4" />);
    expect(screen.getByLabelText(/fasting glucose/i)).toHaveAttribute('type', 'text');
    expect(screen.getByLabelText(/fasting glucose/i)).toHaveAttribute('inputmode', 'decimal');
  });
});

describe('Field wiring', () => {
  it('associates the label with the control', () => {
    render(<Input label="National ID" />);
    expect(screen.getByLabelText('National ID')).toBeInTheDocument();
  });

  it('references the description, so it is announced', () => {
    render(<Input label="National ID" description="Twelve digits, no spaces" />);
    const input = screen.getByLabelText('National ID');
    const describedBy = input.getAttribute('aria-describedby');
    expect(describedBy).toBeTruthy();
    expect(document.getElementById(describedBy!)?.textContent).toBe('Twelve digits, no spaces');
  });

  it('announces the error before the description', () => {
    // When something is wrong, that is the part the person needs first.
    render(<Input label="National ID" description="Twelve digits" error="Too short." />);
    const ids = screen.getByLabelText('National ID').getAttribute('aria-describedby')!.split(' ');
    const texts = ids.map((id) => document.getElementById(id)?.textContent ?? '');
    expect(texts[0]).toContain('Too short.');
    expect(texts[1]).toContain('Twelve digits');
  });

  it('marks a required field for assistive technology, not only with an asterisk', () => {
    render(<Input label="National ID" required />);
    expect(screen.getByLabelText(/national id/i)).toBeRequired();
    expect(screen.getByLabelText(/required/i)).toBeInTheDocument();
  });
});

describe('StatusPill', () => {
  it('renders colour, an icon and a word for every status', () => {
    for (const status of clinicalStatusNames) {
      const { container, unmount } = render(<StatusPill status={status} />);
      const pill = container.querySelector('[data-status]')!;

      expect(pill.getAttribute('data-status'), status).toBe(status);
      expect(pill.querySelector('svg'), `${status} has no icon`).not.toBeNull();
      expect(pill.textContent?.trim(), `${status} has no label`).toBe(
        clinicalStatuses[status].label.en,
      );
      unmount();
    }
  });

  it('keeps the label available even when it is visually hidden', () => {
    // The most compact form still carries two signals, not one.
    render(<StatusPill status="critical" labelHidden />);
    expect(screen.getByText('Critical')).toBeInTheDocument();
    expect(document.querySelector('svg')).not.toBeNull();
  });

  it('sets data-status, which is what the print stylesheet appends a label to', () => {
    const { container } = render(<StatusPill status="high" />);
    expect(container.querySelector('[data-status="high"]')).not.toBeNull();
  });
});

describe('AlertBanner', () => {
  it('interrupts for a critical value and stays polite otherwise', () => {
    // A page that interrupts on every notice is one whose interruptions stop meaning
    // anything, which matters most on the day one of them is a panic value.
    const critical = render(<AlertBanner tone="critical" title="Critical value" />);
    expect(critical.container.querySelector('[role="alert"]')).not.toBeNull();
    critical.unmount();

    const info = render(<AlertBanner tone="info" title="Synced" />);
    expect(info.container.querySelector('[role="status"]')).not.toBeNull();
    expect(info.container.querySelector('[role="alert"]')).toBeNull();
  });

  it('gives the dismiss button an accessible name in both languages', () => {
    const english = render(<AlertBanner title="Synced" onDismiss={() => {}} />);
    expect(english.getByRole('button', { name: 'Dismiss' })).toBeInTheDocument();
    english.unmount();

    const bangla = render(
      <LanguageProvider language="bn">
        <AlertBanner title="Synced" onDismiss={() => {}} />
      </LanguageProvider>,
    );
    expect(bangla.getByRole('button', { name: 'বন্ধ করুন' })).toBeInTheDocument();
  });
});

describe('Skeleton', () => {
  it('announces that something is loading', () => {
    // A skeleton is decorative for sighted users and silence for everyone else.
    render(<Skeleton lines={2} />);
    expect(screen.getByRole('status')).toHaveTextContent('Loading…');
  });

  it('hides the placeholder bars from assistive technology', () => {
    const { container } = render(<Skeleton lines={3} />);
    const bars = container.querySelectorAll('.dthc-skeleton__line');
    expect(bars.length).toBe(3);
    for (const bar of bars) {
      expect(bar).toHaveAttribute('aria-hidden', 'true');
    }
  });
});

describe('ErrorState', () => {
  it('shows the correlation id, selectable', () => {
    // The one string that connects an operator on the phone to a trace in Tempo.
    render(<ErrorState correlationId="0190a000-7000-8000" />);
    expect(screen.getByText('0190a000-7000-8000')).toBeInTheDocument();
  });

  it('keeps technical detail collapsed until asked for', async () => {
    render(<ErrorState detail="pq: connection refused" />);
    expect(screen.queryByText(/connection refused/)).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: /technical detail/i }));
    expect(screen.getByText(/connection refused/)).toBeInTheDocument();
  });

  it('says work is not lost when offline, rather than apologising', () => {
    // Offline is a normal state for a station app, not a failure. The copy says what
    // happened and what happens next.
    render(<ErrorState variant="offline" />);
    expect(screen.getByText(/saved on this device/i)).toBeInTheDocument();
  });

  it('distinguishes an empty result from a failed one', () => {
    // "No patients in the queue" means the clinic is quiet. "Could not load the queue"
    // means there may be twenty people waiting. Showing the first when the second is
    // true is how a station goes unattended - so they are different components with
    // different roles, and this asserts they stay that way.
    const { container } = render(<ErrorState />);
    expect(container.querySelector('[role="alert"]')).not.toBeNull();
  });
});
