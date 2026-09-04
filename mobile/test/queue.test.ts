import { describe, expect, it } from 'vitest';

import {
  callOrder,
  called,
  isPriority,
  nextUp,
  waitTone,
  waitedLabel,
  waiting,
  type QueueRow,
} from '@/features/queue/state';

// Imported from the state module rather than the feature's index: the index re-exports the
// component, and these tests run in plain Node without a JSX transform. Same arrangement as
// CP33's registration tests, and the same reason — the logic worth testing is here.

/**
 * My station's queue, as the screen holds it (CP39).
 *
 * The ordering is mirrored from the server's index rather than trusted from the response,
 * because a station screen that reorders on a stale render is a screen that calls the wrong
 * person — and the operator has no way to tell.
 */

const row = (over: Partial<QueueRow>): QueueRow => ({
  id: 'e1',
  visit_id: 'v1',
  patient_id: 'p1',
  station_code: 'STN_EXAMINATION',
  position: 5,
  status: 'waiting',
  priority: 0,
  entered_at: '2026-09-14T04:00:00Z',
  waited_seconds: 0,
  ...over,
});

describe('the call order', () => {
  it('puts priority ahead of arrival', () => {
    const rows = [
      row({ id: 'first', entered_at: '2026-09-14T04:00:00Z' }),
      row({ id: 'second', entered_at: '2026-09-14T04:10:00Z' }),
      row({
        id: 'urgent',
        entered_at: '2026-09-14T04:20:00Z',
        priority: 5,
        priority_reason: 'Random glucose 24.1 mmol/L with ketones.',
      }),
    ];
    expect(callOrder(rows).map((r) => r.id)).toEqual(['urgent', 'first', 'second']);
    expect(nextUp(rows)?.id).toBe('urgent');
  });

  it('falls back to arrival, then to the id, so the order never wobbles', () => {
    // Two patients who arrived in the same second must not swap places between renders.
    const rows = [
      row({ id: 'bbb', entered_at: '2026-09-14T04:00:00Z' }),
      row({ id: 'aaa', entered_at: '2026-09-14T04:00:00Z' }),
    ];
    expect(callOrder(rows).map((r) => r.id)).toEqual(['aaa', 'bbb']);
    expect(callOrder(rows.reverse()).map((r) => r.id)).toEqual(['aaa', 'bbb']);
  });

  it('separates who is waiting from who this operator has already called', () => {
    const rows = [
      row({ id: 'waiting-1' }),
      row({ id: 'called-1', status: 'called' }),
      row({ id: 'done-1', status: 'done' }),
    ];
    expect(waiting(rows).map((r) => r.id)).toEqual(['waiting-1']);
    expect(called(rows).map((r) => r.id)).toEqual(['called-1']);
    // Nobody left to call once the only waiting patient has been called.
    expect(nextUp([row({ status: 'called' })])).toBeNull();
  });
});

describe('the waiting time as a person reads it', () => {
  it('says "just now" rather than "0 min"', () => {
    // "0" reads as a bug, and an operator who thinks the screen is broken stops reading it.
    expect(waitedLabel(0)).toBe('just now');
    expect(waitedLabel(59)).toBe('just now');
  });

  it('rounds to minutes, because nobody acts on thirteen seconds', () => {
    expect(waitedLabel(133)).toBe('2 min');
    expect(waitedLabel(60 * 45)).toBe('45 min');
  });

  it('says hours once it has been hours', () => {
    expect(waitedLabel(60 * 60)).toBe('1 hr');
    expect(waitedLabel(60 * 95)).toBe('1 hr 35 min');
  });
});

describe('how alarming a wait looks', () => {
  it('leaves an ordinary wait alone', () => {
    // A screen where everything is red is a screen nobody looks at. Twenty minutes is a
    // normal wait at a busy clinic.
    expect(waitTone(0)).toBe('normal');
    expect(waitTone(60 * 20)).toBe('normal');
  });

  it('escalates at half an hour and again at an hour', () => {
    expect(waitTone(60 * 30)).toBe('borderline');
    expect(waitTone(60 * 59)).toBe('borderline');
    expect(waitTone(60 * 60)).toBe('high');
  });
});

describe('priority', () => {
  it('is anything above ordinary', () => {
    expect(isPriority(row({ priority: 0 }))).toBe(false);
    expect(isPriority(row({ priority: 1 }))).toBe(true);
  });
});
