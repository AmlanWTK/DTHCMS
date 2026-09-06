import { ApiError } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

import type { DeviceSyncState, ReferenceVersion, SyncEvent, SyncPage, SyncReceipt } from './state';

/**
 * The four calls the sync engine makes, behind an interface (CP66).
 *
 * The engine never touches `fetch`, and the tests never touch the network. That is not only a
 * testing convenience: the interface is exactly the protocol's surface, so what the engine may do
 * is visible in six lines rather than distributed through a loop, and a fifth call — a shortcut
 * that read a patient straight from the server because it was easier than reconciling — would have
 * to be added here, in the open, rather than appearing inside a screen.
 *
 * The rule the local store makes and this interface keeps: **the UI reads from projections, never
 * from the network.** A station app with a `GET` in a render path is a station app that shows
 * nothing in a corridor.
 */

export interface PushBody {
  /** The receipt number. Generated once per batch attempt and kept until the batch is resolved. */
  batch_id: string;
  device_id: string;
  /** What this device's clock said when it sent this, so the clinic can measure the skew. */
  client_clock?: string;
  events: SyncEvent[];
}

export interface ReferenceAnswer {
  catalogues: ReferenceVersion[];
  server_time: string;
}

export interface StateAnswer {
  state: DeviceSyncState;
  server_time: string;
}

export interface SyncTransport {
  push(body: PushBody): Promise<SyncReceipt>;
  /**
   * The receipt for a batch that was already sent, or null when the clinic has never seen it.
   *
   * Null is the important answer and is not an error: it means the push never arrived, so the same
   * batch — same id, same events, same order — should simply be sent again.
   */
  receipt(batchId: string): Promise<SyncReceipt | null>;
  pull(since: number, limit: number): Promise<SyncPage>;
  reference(): Promise<ReferenceAnswer>;
  state(): Promise<StateAnswer>;
}

const guard = { 'X-Requested-With': 'DTHCMS' } as const;

/** The real one, over the station app's authenticated client. */
export function httpTransport(): SyncTransport {
  return {
    async push(body) {
      return unwrap(
        api.POST('/v1/sync/events', {
          // Two idempotency mechanisms, at two layers, and both earn their place
          // (`docs/sync.md`): the header answers a retry inside the server's cache window without
          // the handler running at all, and `batch_id` answers a client that comes back tomorrow.
          // The same value for both, because they are the same batch.
          params: { header: { ...guard, 'Idempotency-Key': body.batch_id } },
          body,
        }),
      );
    },

    async receipt(batchId) {
      try {
        return await unwrap(
          api.GET('/v1/sync/batches/{id}', { params: { path: { id: batchId } } }),
        );
      } catch (error) {
        if (error instanceof ApiError && error.status === 404) return null;
        throw error;
      }
    },

    async pull(since, limit) {
      return unwrap(api.GET('/v1/sync/events', { params: { query: { since, limit } } }));
    },

    async reference() {
      const body = await unwrap(api.GET('/v1/sync/reference'));
      return { catalogues: body.catalogues ?? [], server_time: body.server_time };
    },

    async state() {
      const body = await unwrap(api.GET('/v1/sync/state'));
      return { state: body.state, server_time: body.server_time };
    },
  };
}
