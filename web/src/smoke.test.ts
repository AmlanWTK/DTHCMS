import { describe, expect, it } from 'vitest';

/**
 * CP01 smoke test. Its only job is to prove the web workspace's test runner executes
 * in CI and that a failing test fails the build. Real component tests arrive with the
 * design system (CP09) and the application shell (CP10).
 */
describe('web workspace', () => {
  it('runs its test suite', () => {
    expect(true).toBe(true);
  });
});
