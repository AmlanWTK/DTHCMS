import { describe, expect, it } from 'vitest';

import { scrub, toCrashReport } from '../src/lib/crash';

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
