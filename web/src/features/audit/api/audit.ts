import { writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import { STEP_UP_HEADER } from '@/features/auth';
import { ApiError, NetworkError, api, authenticatedFetch, unwrap } from '@/lib/api';

/**
 * The audit calls, typed against the contract (CP22).
 *
 * Reads are plain. The export is the one response the typed client cannot describe — a
 * PDF whose signature travels in headers — so it goes through the same authenticated
 * fetch and comes back as a file plus its signature, for the browser to save side by side.
 */

export type AuditEvent = components['schemas']['AuditEvent'];
export type AuditKind = components['schemas']['AuditKindList']['kinds'][number];
export type ChainVerification = components['schemas']['ChainVerification'];
export type AdminAlert = components['schemas']['AdminAlert'];
export type BreakGlassAccess = components['schemas']['BreakGlassAccess'];
export type BreakGlassRequest = components['schemas']['BreakGlassRequest'];

export interface AuditFilter {
  kind?: string;
  actor?: string;
  person?: string;
  patient?: string;
  from?: string;
  to?: string;
}

export interface AuditPage {
  events: AuditEvent[];
  nextBefore: number | null;
}

function cleaned(filter: AuditFilter): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(filter)) {
    if (v && v.trim()) out[k] = v.trim();
  }
  return out;
}

export async function listAuditEvents(filter: AuditFilter, before?: number): Promise<AuditPage> {
  const query: Record<string, string | number> = { ...cleaned(filter), limit: 50 };
  if (before) query.before = before;
  const result = await unwrap(api.GET('/v1/audit/events', { params: { query } }));
  return { events: result.events, nextBefore: result.next_before };
}

export async function listAuditKinds(): Promise<AuditKind[]> {
  const result = await unwrap(api.GET('/v1/audit/kinds'));
  return result.kinds;
}

export function verifyChain(): Promise<ChainVerification> {
  return unwrap(api.GET('/v1/audit/chain'));
}

export interface SignedExport {
  blob: Blob;
  filename: string;
  signature: { key_id: string; algorithm: 'ed25519-sha256'; sha256: string; signature: string };
}

/** Fetch the signed PDF. Throws the server's refusal like every other call. */
export async function exportTrail(filter: AuditFilter): Promise<SignedExport> {
  const params = new URLSearchParams(cleaned(filter));
  // The one call in this module that goes round the typed client, because the contract's
  // JSON types cannot describe a PDF. Going round it also means going round the place
  // where a request that never left the building becomes a NetworkError — so that
  // translation happens here instead. Without it the raw fetch TypeError escapes, and a
  // screen asking `error instanceof NetworkError` to say "you are offline" tells the
  // operator the server refused them instead. Two different instructions; one is wrong.
  let response: Response;
  try {
    response = await authenticatedFetch(`/v1/audit/export?${params.toString()}`, {
      headers: { Accept: 'application/pdf' },
    });
  } catch (cause) {
    if (cause instanceof ApiError || cause instanceof NetworkError) throw cause;
    throw new NetworkError(cause);
  }
  if (!response.ok) {
    // Let the typed path produce the error the rest of the app knows how to explain.
    await unwrap(api.GET('/v1/audit/export', { params: { query: cleaned(filter) } }));
    throw new Error('export failed');
  }
  const disposition = response.headers.get('Content-Disposition') ?? '';
  const match = /filename="?([^";]+)"?/.exec(disposition);
  return {
    blob: await response.blob(),
    filename: match?.[1] ?? 'dthcms-audit.pdf',
    signature: {
      key_id: response.headers.get('X-Audit-Key-Id') ?? '',
      algorithm: 'ed25519-sha256',
      sha256: response.headers.get('X-Audit-Digest') ?? '',
      signature: response.headers.get('X-Audit-Signature') ?? '',
    },
  };
}

export async function listAlerts(): Promise<AdminAlert[]> {
  const result = await unwrap(api.GET('/v1/audit/alerts'));
  return result.alerts;
}

export function acknowledgeAlert(id: string): Promise<AdminAlert> {
  return unwrap(
    api.POST('/v1/audit/alerts/{id}/acknowledge', { params: { ...writing(), path: { id } } }),
  );
}

export function openBreakGlass(
  request: BreakGlassRequest,
  token: string,
): Promise<BreakGlassAccess> {
  return unwrap(
    api.POST('/v1/audit/break-glass', {
      params: writing({ [STEP_UP_HEADER]: token }),
      body: request,
    }),
  );
}

export async function myBreakGlass(): Promise<BreakGlassAccess[]> {
  const result = await unwrap(api.GET('/v1/audit/break-glass/mine'));
  return result.accesses;
}

export async function listBreakGlass(): Promise<BreakGlassAccess[]> {
  const result = await unwrap(api.GET('/v1/audit/break-glass'));
  return result.accesses;
}

export function endBreakGlass(id: string, reason: string): Promise<BreakGlassAccess> {
  return unwrap(
    api.POST('/v1/audit/break-glass/{id}/end', {
      params: { ...writing(), path: { id } },
      body: { reason },
    }),
  );
}

/** The justification the server accepts: twenty characters, in any script. */
export const MIN_JUSTIFICATION = 20;

export function justificationAcceptable(text: string): boolean {
  return [...text.trim()].length >= MIN_JUSTIFICATION;
}
