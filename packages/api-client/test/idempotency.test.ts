import { describe, expect, it } from 'vitest';

import { IDEMPOTENCY_HEADER, beginAttempt, wasReplayed, writing } from '../src/idempotency';

describe('beginAttempt', () => {
  it('keeps one key across every retry of the attempt', () => {
    const attempt = beginAttempt();
    expect(attempt.params().header[IDEMPOTENCY_HEADER]).toBe(attempt.key);
    expect(attempt.params().header[IDEMPOTENCY_HEADER]).toBe(attempt.key);
  });

  it('gives a different key to a different attempt', () => {
    expect(beginAttempt().key).not.toBe(beginAttempt().key);
  });

  it('carries the forgery guard, so a write needs one call and not two', () => {
    expect(beginAttempt().params().header['X-Requested-With']).toBe('DTHCMS');
  });

  it('keeps the headers the caller adds', () => {
    const attempt = beginAttempt();
    expect(attempt.params({ 'X-Step-Up-Token': 'tok' }).header).toEqual({
      'X-Requested-With': 'DTHCMS',
      'X-Step-Up-Token': 'tok',
      [IDEMPOTENCY_HEADER]: attempt.key,
    });
  });

  it('accepts a key restored from an outbox row', () => {
    const stored = '0190a8f2-0000-7000-8000-0000000000aa';
    expect(beginAttempt(stored).key).toBe(stored);
  });

  it('refuses a key that is not a UUIDv7, rather than sending one the server will reject', () => {
    expect(() => beginAttempt('f47ac10b-58cc-4372-a567-0e02b2c3d479')).toThrow(/UUIDv7/);
    expect(() => beginAttempt('')).toThrow(/UUIDv7/);
  });
});

describe('writing', () => {
  it('is a fresh key each call — one user gesture, one attempt', () => {
    expect(writing().header[IDEMPOTENCY_HEADER]).not.toBe(writing().header[IDEMPOTENCY_HEADER]);
  });

  it('carries the forgery guard and anything else the call site needs', () => {
    const params = writing({ 'X-Step-Up-Token': 'tok' });
    expect(params.header['X-Requested-With']).toBe('DTHCMS');
    expect(params.header['X-Step-Up-Token']).toBe('tok');
    expect(params.header[IDEMPOTENCY_HEADER]).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-7/);
  });
});

describe('wasReplayed', () => {
  it('reads the header the server sets on a replay', () => {
    expect(wasReplayed(new Response(null, { headers: { 'Idempotency-Replayed': 'true' } }))).toBe(
      true,
    );
    expect(wasReplayed(new Response(null))).toBe(false);
  });
});
