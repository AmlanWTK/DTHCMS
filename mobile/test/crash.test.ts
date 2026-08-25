import { afterEach, describe, expect, it, vi } from 'vitest';

import { installCrashHandler, scrub, toCrashReport } from '../src/lib/crash';

/**
 * The scrubber, tested the way the backend's redaction is: with the things that must
 * never survive, not with the things that may.
 */

describe('scrub', () => {
  it('removes long digit runs — phones, NIDs, registration numbers', () => {
    expect(scrub('failed for 01712345678')).not.toContain('01712345678');
    expect(scrub('failed for 01712345678')).toContain('[scrubbed-number]');
  });

  it('removes email addresses', () => {
    expect(scrub('user rahima@example.com not found')).not.toContain('rahima@example.com');
  });

  it('removes values behind sensitive labels', () => {
    const out = scrub('render failed: name: Rahima Khatun');
    expect(out).not.toContain('Rahima');
    expect(out).toContain('[scrubbed]');
  });

  it('keeps the parts an engineer needs', () => {
    const out = scrub('Cannot read property "solid" of undefined at AppButton');
    expect(out).toContain('AppButton');
    expect(out).toContain('solid');
  });
});

describe('toCrashReport', () => {
  it('scrubs both message and stack', () => {
    const error = new Error('boom for 01712345678');
    error.stack = 'Error: boom for 01712345678\n  at QueueScreen';
    const crash = toCrashReport(error, true);
    expect(crash.message).not.toContain('01712345678');
    expect(crash.stack).not.toContain('01712345678');
    expect(crash.stack).toContain('QueueScreen');
    expect(crash.isFatal).toBe(true);
  });

  it('copes with a thrown non-Error', () => {
    expect(toCrashReport('just a string', false).message).toBe('just a string');
  });
});

describe('the choke point', () => {
  const original = (globalThis as { ErrorUtils?: unknown }).ErrorUtils;

  afterEach(() => {
    (globalThis as { ErrorUtils?: unknown }).ErrorUtils = original;
    vi.restoreAllMocks();
  });

  function fakeErrorUtils() {
    let handler: (error: unknown, isFatal?: boolean) => void = () => {};
    const previous = vi.fn();
    handler = previous;
    return {
      getGlobalHandler: () => handler,
      setGlobalHandler: (next: typeof handler) => {
        handler = next;
      },
      current: () => handler,
      previous,
    };
  }

  it('scrubs before anything is reported', () => {
    // The whole reason the seam exists ahead of the vendor: whatever is wired in later
    // receives text that has already been through the scrubber, so wiring a collector
    // cannot accidentally ship PHI.
    const utils = fakeErrorUtils();
    (globalThis as { ErrorUtils?: unknown }).ErrorUtils = utils;
    const logged = vi.spyOn(console, 'error').mockImplementation(() => {});

    installCrashHandler();
    utils.current()(new Error('save failed for 01712345678'), true);

    const report = logged.mock.calls[0]?.[1] as { message: string };
    expect(report.message).not.toContain('01712345678');
    expect(report.message).toContain('[scrubbed-number]');
  });

  it('calls the handler it replaced, rather than swallowing the crash', () => {
    /*
     * The previous handler shows the red box in development and performs the platform's
     * fatal-crash teardown in production. Swallowing it would turn a crash into a hang —
     * a station that looks alive and does nothing, which is worse for an operator than
     * an app that closes.
     */
    const utils = fakeErrorUtils();
    (globalThis as { ErrorUtils?: unknown }).ErrorUtils = utils;
    vi.spyOn(console, 'error').mockImplementation(() => {});

    installCrashHandler();
    const boom = new Error('boom');
    utils.current()(boom, true);

    expect(utils.previous).toHaveBeenCalledWith(boom, true);
  });

  it('marks a non-fatal error as non-fatal', () => {
    const utils = fakeErrorUtils();
    (globalThis as { ErrorUtils?: unknown }).ErrorUtils = utils;
    const logged = vi.spyOn(console, 'error').mockImplementation(() => {});

    installCrashHandler();
    utils.current()(new Error('recoverable'), undefined);

    const report = logged.mock.calls[0]?.[1] as { isFatal: boolean };
    expect(report.isFatal).toBe(false);
  });

  it('does nothing at all where ErrorUtils does not exist', () => {
    // ErrorUtils is a React Native global. This module is imported by tests and tooling
    // that run in plain Node, and installing must be a no-op there rather than a crash
    // in the crash handler.
    delete (globalThis as { ErrorUtils?: unknown }).ErrorUtils;
    expect(() => installCrashHandler()).not.toThrow();
  });
});
