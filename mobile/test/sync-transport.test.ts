import { ApiError } from '@dthcms/api-client';
import { describe, expect, it, vi } from 'vitest';

/**
 * What the engine actually sends (CP66).
 *
 * Four calls, and three details in them that the protocol turns on and that a type checker cannot
 * see: the idempotency header carries the **batch id** rather than a fresh value, a receipt for a
 * batch the clinic has never seen is **null rather than an error**, and the pull asks from a
 * cursor rather than for everything.
 */

const state = vi.hoisted(() => ({
  calls: [] as { method: string; path: string; init?: Record<string, unknown> }[],
  answer: null as null | ((method: string, path: string) => { status: number; body: unknown }),
}));

vi.mock('@/lib/api', async () => {
  const { unwrap } = await import('@dthcms/api-client');
  const call = (method: string) => async (path: string, init?: Record<string, unknown>) => {
    state.calls.push({ method, path, init });
    const { status, body } = state.answer?.(method, path) ?? { status: 200, body: {} };
    const response = new Response(JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    });
    return status >= 400 ? { error: body, response } : { data: body, response };
  };
  return { unwrap, api: { GET: call('GET'), POST: call('POST') } };
});

const { httpTransport } = await import('../src/lib/sync/transport');

function refusal(code: string) {
  return {
    error: {
      code,
      kind: 'not_found',
      message: 'no such batch',
      message_bn: 'এমন কোনও ব্যাচ নেই',
      correlation_id: 'req_1',
    },
  };
}

describe('the four calls', () => {
  it('sends the batch id as the idempotency key, not a new one', () => {
    state.calls = [];
    state.answer = () => ({ status: 200, body: { batch_id: 'batch-9', results: [] } });
    const transport = httpTransport();
    void transport.push({ batch_id: 'batch-9', device_id: 'device-1', events: [] });

    const sent = state.calls[0];
    expect(sent?.path).toBe('/v1/sync/events');
    const header = (sent?.init as { params: { header: Record<string, string> } }).params.header;
    // Two idempotency mechanisms at two layers, and they are about the same batch: the header
    // answers a retry inside the server's cache window, the body's id answers one a day later.
    expect(header['Idempotency-Key']).toBe('batch-9');
    expect(header['X-Requested-With']).toBe('DTHCMS');
  });

  it('reports a batch the clinic has never seen as nothing, not as a failure', async () => {
    state.calls = [];
    state.answer = () => ({ status: 404, body: refusal('NOT_FOUND') });
    const receipt = await httpTransport().receipt('batch-never-arrived');
    // Null means "it never arrived, send it again". An exception here would make the engine
    // treat a push that was lost in the post as an error to back off from.
    expect(receipt).toBeNull();
  });

  it('lets every other refusal through as itself', async () => {
    state.calls = [];
    state.answer = () => ({ status: 401, body: refusal('UNAUTHENTICATED') });
    await expect(httpTransport().receipt('batch-1')).rejects.toBeInstanceOf(ApiError);
  });

  it('asks for events from a cursor, and for the catalogues by fingerprint', async () => {
    state.calls = [];
    state.answer = (_method, path) =>
      path === '/v1/sync/events'
        ? { status: 200, body: { events: [], cursor: 12, latest: 12, more: false } }
        : {
            status: 200,
            body: {
              catalogues: [{ catalogue: 'terminology', rows: 3, fingerprint: 'fp' }],
              server_time: '2026-09-05T08:00:00.000Z',
            },
          };
    const transport = httpTransport();
    const page = await transport.pull(12, 200);
    expect(page.cursor).toBe(12);
    expect(
      (state.calls[0]?.init as { params: { query: Record<string, number> } }).params.query,
    ).toEqual({ since: 12, limit: 200 });

    const reference = await transport.reference();
    expect(reference.catalogues).toHaveLength(1);
    expect(reference.server_time).toBe('2026-09-05T08:00:00.000Z');
  });

  it('reads where the clinic thinks this device has got to', async () => {
    state.calls = [];
    state.answer = () => ({
      status: 200,
      body: {
        state: {
          device_id: 'device-1',
          last_pulled_seq: 40,
          pushed_total: 12,
          quarantined_total: 0,
        },
        server_time: '2026-09-05T08:00:00.000Z',
      },
    });
    const answer = await httpTransport().state();
    expect(answer.state.last_pulled_seq).toBe(40);
  });
});
