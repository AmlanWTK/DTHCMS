import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError, NetworkError, API_BASE_URL } from '@/lib/api';
import { readBoard, reroute } from '@/features/board/api/board';
import type { TrafficBoard } from '@/features/board/api/board';

/**
 * The clinic traffic control board's client half (CP40).
 *
 * The board hangs on a wall in a waiting area, and a supervisor moves people between
 * queues from the same payload. So the things this file pins down are the ones whose
 * failure is either invisible or expensive:
 *
 *  - "today" is a clinic day in Asia/Dhaka, decided by the server. Sending `day=` empty
 *    or `day=undefined` from a browser whose clock is on another date is how a wall
 *    display quietly shows yesterday's queue for the first hour of the morning;
 *  - a reroute changes where a person is standing. It carries the forgery guard, a fresh
 *    idempotency key, and its own `event_id` — and two reroutes must never share either,
 *    or the second is answered from the store as a replay and the patient never moves;
 *  - a `409` has to arrive at the caller as an ApiError carrying the server's code, not
 *    as a thrown string or a swallowed success. It means somebody else moved this patient
 *    while both supervisors were looking at the same board, and the board needs
 *    refreshing rather than the write retrying.
 */

function respond(body: unknown, init: { status?: number } = {}) {
  return new Response(JSON.stringify(body), {
    status: init.status ?? 200,
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req_board_3' },
  });
}

type Handler = (request: Request) => Response | Promise<Response>;

function server(routes: Record<string, Handler>): Request[] {
  const calls: Request[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = new Request(input, init);
      calls.push(request);
      const key = `${request.method} ${new URL(request.url).pathname}`;
      const handler = routes[key];
      if (!handler) throw new Error(`no route for ${key}`);
      return handler(request);
    }),
  );
  return calls;
}

const ENTRY = '0190a8f2-0000-7000-8000-0000000000e1';
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

const board: TrafficBoard = {
  day: '2026-09-14',
  generated_at: '2026-09-14T04:42:00Z',
  settings: {
    identify_by: 'code',
    busy_wait_seconds: 900,
    busy_depth: 4,
    bottleneck_wait_seconds: 1_800,
    bottleneck_depth: 7,
  },
  stations: [
    {
      station_code: 'STN_EXAMINATION',
      position: 5,
      heat: 'bottleneck',
      waiting: 8,
      called: 1,
      in_service: 1,
      longest_wait_seconds: 1_800,
      entries: [
        {
          entry_id: ENTRY,
          visit_id: '0190a8f2-0000-7000-8000-0000000000v1',
          label: 'V-2026-0914-017',
          status: 'waiting',
          priority: 5,
          flagged: true,
          counseling_done: true,
          waited_seconds: 1_800,
        },
      ],
    },
  ],
  suggestions: [],
  waiting_total: 8,
  in_building_total: 10,
};

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('reading the board', () => {
  it('asks the server for today rather than naming a day the browser guessed', async () => {
    // No `day` parameter at all, not an empty one. The clinic day is Asia/Dhaka's, and a
    // tablet on the wrong date must not be able to shift the whole wall display.
    const calls = server({ 'GET /v1/board': () => respond(board) });

    await readBoard();

    const url = new URL(calls[0]!.url);
    expect(url.pathname).toBe('/v1/board');
    expect(url.search).toBe('');
    expect(calls[0]!.url.startsWith(API_BASE_URL)).toBe(true);
  });

  it('names the day when one is asked for', async () => {
    const calls = server({ 'GET /v1/board': () => respond({ ...board, day: '2026-09-13' }) });

    const yesterday = await readBoard('2026-09-13');

    expect(new URL(calls[0]!.url).searchParams.get('day')).toBe('2026-09-13');
    expect(yesterday.day).toBe('2026-09-13');
  });

  it('hands the payload through untouched, labels and all', async () => {
    // There is no client-side redaction here on purpose: `label` arrives already resolved
    // against the facility's convention, and there is no patient id in the payload to
    // leak. A transform in this layer would be a second place for that to go wrong.
    server({ 'GET /v1/board': () => respond(board) });

    await expect(readBoard()).resolves.toEqual(board);
  });

  it('surfaces a refusal as an ApiError the screen can explain', async () => {
    server({
      'GET /v1/board': () =>
        respond(
          {
            error: {
              code: 'FORBIDDEN',
              kind: 'permission',
              message: 'This account cannot read the board.',
              message_bn: 'এই অ্যাকাউন্ট বোর্ড দেখতে পারে না।',
            },
          },
          { status: 403 },
        ),
    });

    const error = await readBoard().catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).code).toBe('FORBIDDEN');
    expect((error as ApiError).messageBN).toBe('এই অ্যাকাউন্ট বোর্ড দেখতে পারে না।');
    expect((error as ApiError).correlationID).toBe('req_board_3');
  });

  it('says the wall display is offline rather than that the clinic refused it', async () => {
    // A screen on a wall loses wifi routinely. "No connection" is a different instruction
    // to whoever walks past it than "you are not allowed to see this".
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));

    await expect(readBoard()).rejects.toBeInstanceOf(NetworkError);
  });
});

describe('rerouting a waiting patient', () => {
  it('posts to the entry, with the destination and the reason', async () => {
    const calls = server({
      [`POST /v1/board/reroute/${ENTRY}`]: () => respond({ entry: { id: ENTRY } }),
    });

    await reroute(ENTRY, 'STN_NUTRITION', 'examination is backed up');

    const request = calls[0]!;
    expect(new URL(request.url).pathname).toBe(`/v1/board/reroute/${ENTRY}`);
    const body = (await request.json()) as Record<string, unknown>;
    expect(body.to).toBe('STN_NUTRITION');
    // Where the patient is going is in `to`; this is why. Both are required, and a
    // reroute with no reason is an unexplained queue change in an append-only ledger.
    expect(body.reason).toBe('examination is backed up');
    expect(body.event_id).toMatch(UUID);
  });

  it('carries the forgery guard and a fresh idempotency key', async () => {
    const calls = server({
      [`POST /v1/board/reroute/${ENTRY}`]: () => respond({ entry: { id: ENTRY } }),
    });

    await reroute(ENTRY, 'STN_NUTRITION', 'examination is backed up');

    const request = calls[0]!;
    expect(request.headers.get('X-Requested-With')).toBe('DTHCMS');
    expect(request.headers.get('Idempotency-Key')).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    );
  });

  it('gives two reroutes two identities, so the second one actually moves somebody', async () => {
    // Both halves matter. A repeated `event_id` is refused by the ledger; a repeated
    // idempotency key is answered from the store as a replay — which looks like success
    // and leaves the patient in the queue they were already in.
    const calls = server({
      [`POST /v1/board/reroute/${ENTRY}`]: () => respond({ entry: { id: ENTRY } }),
    });

    await reroute(ENTRY, 'STN_NUTRITION', 'first move');
    await reroute(ENTRY, 'STN_PHARMACY', 'second move');

    const first = (await calls[0]!.json()) as { event_id: string };
    const second = (await calls[1]!.json()) as { event_id: string };
    expect(first.event_id).not.toBe(second.event_id);
    expect(calls[0]!.headers.get('Idempotency-Key')).not.toBe(
      calls[1]!.headers.get('Idempotency-Key'),
    );
  });

  it('lets a 409 through as the board having moved on', async () => {
    // Not retried and not swallowed. The caller refreshes the board; retrying would be
    // arguing with a decision somebody else already made.
    const calls = server({
      [`POST /v1/board/reroute/${ENTRY}`]: () =>
        respond(
          {
            error: {
              code: 'ENTRY_NOT_LIVE',
              kind: 'conflict',
              message: 'That patient is no longer waiting here.',
              message_bn: 'এই রোগী আর এখানে অপেক্ষা করছেন না।',
            },
          },
          { status: 409 },
        ),
    });

    const error = await reroute(ENTRY, 'STN_NUTRITION', 'too slow').catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(409);
    expect((error as ApiError).code).toBe('ENTRY_NOT_LIVE');
    // A 4xx is not worth sending again — the request is wrong, not the moment.
    expect((error as ApiError).retryable).toBe(false);
    expect(calls).toHaveLength(1);
  });
});
