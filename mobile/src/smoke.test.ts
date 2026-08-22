import { describe, expect, it } from 'vitest';

/**
 * CP01 smoke test — proves the mobile workspace's test runner executes in CI.
 * Device and offline testing arrive at CP11 and CP68.
 */
describe('mobile workspace', () => {
  it('runs its test suite', () => {
    expect(true).toBe(true);
  });
});
