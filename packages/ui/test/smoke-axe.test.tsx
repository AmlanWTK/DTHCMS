import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { Button } from '../src/components/Button';
import { checkA11y, describeViolations } from './axe';

describe('axe harness', () => {
  it('runs in jsdom and finds nothing wrong with a good button', async () => {
    const { container } = render(<Button variant="primary">Save</Button>);
    const results = await checkA11y(container);
    expect(results.violations.length, describeViolations(results)).toBe(0);
  });

  it('actually detects a violation when there is one', async () => {
    // If this passes trivially, the harness is not doing anything.
    const { container } = render(
      <button type="button">
        <img src="x.png" />
      </button>,
    );
    const results = await checkA11y(container);
    expect(results.violations.length).toBeGreaterThan(0);
  });
});
