import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError, NetworkError, API_BASE_URL } from '@/lib/api';
import {
  MIN_JUSTIFICATION,
  acknowledgeAlert,
  endBreakGlass,
  exportTrail,
  justificationAcceptable,
  listAlerts,
  listAuditEvents,
  listAuditKinds,
  listBreakGlass,
  myBreakGlass,
  openBreakGlass,
  verifyChain,
} from '@/features/audit/api/audit';
import type { AdminAlert, AuditEvent, BreakGlassAccess } from '@/features/audit/api/audit';

/**
 * The audit trail's client half (CP22).
 *
 * This is the module a hospital's answer to "who opened that record, and when?" travels
 * through, so the things worth pinning down here are the ones that fail silently:
 *
 *  - a filter the administrator typed with a stray space must still narrow the trail,
 *    and a filter they cleared must not narrow it to nothing;
 *  - a signed export is a PDF *and* three headers. A file saved without its key id,
 *    digest and signature cannot be verified by anyone afterwards, which makes it
 *    evidence of nothing — and the loss is invisible until somebody tries;
 *  - a refused export must refuse with the server's own sentence, not a bare "failed",
 *    because that sentence is what tells the administrator whether to ask for a
 *    permission or to stop asking;
 *  - every write carries the forgery guard and a fresh idempotency key. Break-glass
 *    additionally carries the step-up token in the header the server reads — put it in
 *    the wrong one and the emergency door simply does not open, at the moment somebody
 *    is standing in front of it;
 *  - twenty characters of justification means the same twenty on both sides, in whatever
 *    script the clinician types. A client that measured more leniently than the server
 *    would refuse the sentence only after it had been typed, in an emergency.
 */

function respond(body: unknown, init: { status?: number } = {}) {
  return new Response(JSON.stringify(body), {
    status: init.status ?? 200,
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req_audit_7' },
  });
}

type Handler = (request: Request) => Response | Promise<Response>;

/** A scripted API. Returns the requests it received, in order, to assert on. */
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

function query(request: Request): Record<string, string> {
  return Object.fromEntries(new URL(request.url).searchParams);
}

const refusal = {
  error: {
    code: 'FORBIDDEN',
    kind: 'permission',
    message: 'You do not have permission to read the audit trail.',
    message_bn: 'অডিট ট্রেইল দেখার অনুমতি আপনার নেই।',
  },
};

const event: AuditEvent = {
  seq: 4102,
  kind: 'patient.read',
  label_en: 'Patient record read',
  label_bn: 'রোগীর রেকর্ড দেখা হয়েছে',
  recorded_at: '2026-09-14T04:42:00Z',
  actor: { id: '0190a8f2-0000-7000-8000-00000000000a', code: 'E001' },
  actor_role: 'PHYSICIAN',
  target: null,
  patient_id: '0190a8f2-0000-7000-8000-00000000000b',
  device_id: null,
  reason: '',
  details: {},
  sentence_en: 'Dr Test read the record of patient V-2026-0914-017.',
  sentence_bn: 'ডা. পরীক্ষা V-2026-0914-017 রোগীর রেকর্ড দেখেছেন।',
  hash: 'b3d1f0a2c4',
};

const alert: AdminAlert = {
  id: '0190a8f2-0000-7000-8000-0000000000a1',
  kind: 'break_glass',
  severity: 'high',
  message_en: 'Dr Test opened an emergency access.',
  message_bn: 'ডা. পরীক্ষা জরুরি প্রবেশাধিকার নিয়েছেন।',
  reference: {},
  audit_seq: 4103,
  created_at: '2026-09-14T04:43:00Z',
};

const access: BreakGlassAccess = {
  id: '0190a8f2-0000-7000-8000-0000000000b1',
  user_id: '0190a8f2-0000-7000-8000-00000000000a',
  active_role: 'PHYSICIAN',
  scope_kind: 'patient',
  scope_ref: '0190a8f2-0000-7000-8000-00000000000b',
  justification: 'Unconscious patient, no consent obtainable.',
  granted_at: '2026-09-14T04:43:00Z',
  expires_at: '2026-09-14T08:43:00Z',
  ended_at: null,
  end_reason: '',
  acknowledged_at: null,
  audit_seq: 4103,
};

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('reading the trail', () => {
  it('asks for one page, newest first, and hands back the cursor for the next', async () => {
    const calls = server({
      'GET /v1/audit/events': () => respond({ events: [event], next_before: 4102 }),
    });

    const page = await listAuditEvents({});

    expect(page.events).toEqual([event]);
    // `next_before` becomes `nextBefore` here and nowhere else. A screen that read the
    // wire name would page forever on `undefined`.
    expect(page.nextBefore).toBe(4102);
    expect(query(calls[0]!)).toEqual({ limit: '50' });
  });

  it('drops the filters the administrator cleared and trims the ones they typed', async () => {
    // A blank box means "do not narrow by this". Sent as an empty string it means "match
    // the empty actor", and the trail comes back empty with nothing on screen to explain
    // why. A trailing space pasted from a spreadsheet does the same, more quietly.
    const calls = server({
      'GET /v1/audit/events': () => respond({ events: [], next_before: null }),
    });

    await listAuditEvents({
      kind: '  patient.read  ',
      actor: '',
      person: '   ',
      from: '2026-09-01',
    });

    expect(query(calls[0]!)).toEqual({ kind: 'patient.read', from: '2026-09-01', limit: '50' });
  });

  it('carries the cursor when asked for the page after the first', async () => {
    const calls = server({
      'GET /v1/audit/events': () => respond({ events: [], next_before: null }),
    });

    const page = await listAuditEvents({ patient: 'p1' }, 4102);

    expect(query(calls[0]!)).toEqual({ patient: 'p1', limit: '50', before: '4102' });
    // The last page says so, rather than handing back a cursor that would repeat itself.
    expect(page.nextBefore).toBeNull();
  });

  it('surfaces a refusal as the server’s own sentence, in both languages', async () => {
    server({ 'GET /v1/audit/events': () => respond(refusal, { status: 403 }) });

    const error = await listAuditEvents({}).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).code).toBe('FORBIDDEN');
    expect((error as ApiError).messageBN).toBe('অডিট ট্রেইল দেখার অনুমতি আপনার নেই।');
    expect((error as ApiError).correlationID).toBe('req_audit_7');
  });

  it('reads the kind registry the filter offers', async () => {
    const kinds = [{ kind: 'patient.read', label_en: 'Patient record read', label_bn: 'দেখা' }];
    server({ 'GET /v1/audit/kinds': () => respond({ kinds }) });

    await expect(listAuditKinds()).resolves.toEqual(kinds);
  });

  it('reports a broken chain rather than treating it as a failed request', async () => {
    // `ok: false` is an answer, not an error: the whole point of asking is to be told.
    server({
      'GET /v1/audit/chain': () =>
        respond({
          ok: false,
          checked: 4102,
          head_seq: 4102,
          broken_at: 3990,
          problem: 'hash mismatch',
          strays: 0,
        }),
    });

    const verification = await verifyChain();

    expect(verification.ok).toBe(false);
    expect(verification.broken_at).toBe(3990);
  });
});

describe('the signed export', () => {
  function pdf(headers: Record<string, string>) {
    return new Response('%PDF-1.7 audit', { status: 200, headers });
  }

  it('asks over the authenticated fetch, at the API origin, for a PDF', async () => {
    const calls = server({
      'GET /v1/audit/export': () => pdf({ 'Content-Type': 'application/pdf' }),
    });

    await exportTrail({ from: '2026-09-01', to: ' 2026-09-30 ' });

    const request = calls[0]!;
    expect(request.url.startsWith(`${API_BASE_URL}/v1/audit/export`)).toBe(true);
    expect(request.headers.get('Accept')).toBe('application/pdf');
    expect(query(request)).toEqual({ from: '2026-09-01', to: '2026-09-30' });
  });

  it('keeps the signature beside the file, because that is what makes it evidence', async () => {
    server({
      'GET /v1/audit/export': () =>
        pdf({
          'Content-Type': 'application/pdf',
          'Content-Disposition': 'attachment; filename="audit-2026-09.pdf"',
          'X-Audit-Key-Id': 'k-2026-01',
          'X-Audit-Digest': 'sha256-of-the-body',
          'X-Audit-Signature': 'ed25519-signature',
        }),
    });

    const exported = await exportTrail({});

    expect(exported.filename).toBe('audit-2026-09.pdf');
    expect(await exported.blob.text()).toBe('%PDF-1.7 audit');
    expect(exported.signature).toEqual({
      key_id: 'k-2026-01',
      algorithm: 'ed25519-sha256',
      sha256: 'sha256-of-the-body',
      signature: 'ed25519-signature',
    });
  });

  it('names the file itself when the server did not', async () => {
    // A download called "export" with no extension is one an administrator loses in a
    // folder of downloads a week later.
    server({ 'GET /v1/audit/export': () => pdf({ 'Content-Type': 'application/pdf' }) });

    const exported = await exportTrail({});

    expect(exported.filename).toBe('dthcms-audit.pdf');
    expect(exported.signature.key_id).toBe('');
  });

  it('refuses with the reason the server gave, not with "export failed"', async () => {
    // The PDF response cannot carry an error envelope a screen can read, so the refusal
    // is re-asked for on the typed path. What the administrator must end up seeing is
    // "you do not have permission", which is actionable, rather than a generic failure.
    let attempt = 0;
    server({
      'GET /v1/audit/export': () => {
        attempt += 1;
        return attempt === 1
          ? new Response('nope', { status: 403 })
          : respond(refusal, { status: 403 });
      },
    });

    const error = await exportTrail({ kind: 'patient.read' }).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).messageEN).toBe(
      'You do not have permission to read the audit trail.',
    );
    expect(attempt).toBe(2);
  });

  it('still fails when the second ask inexplicably succeeds', async () => {
    // Belt and braces: whatever happened, no caller is handed a SignedExport with no PDF
    // in it.
    let attempt = 0;
    server({
      'GET /v1/audit/export': () => {
        attempt += 1;
        return attempt === 1 ? new Response('', { status: 500 }) : respond({});
      },
    });

    await expect(exportTrail({})).rejects.toThrow('export failed');
  });

  it('reports a request that never left the building as a NetworkError', async () => {
    /*
     * The export is the one call in the module that goes round the typed client, because
     * the contract's JSON types cannot describe a PDF. Going round it once meant going
     * round the place where an unreachable server becomes a `NetworkError` — and a screen
     * asking `error instanceof NetworkError` to say "you are offline" would instead have
     * told the operator the clinic server refused them. Two different instructions to a
     * person standing in a corridor, and one of them is wrong.
     */
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));

    const error = await exportTrail({}).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(NetworkError);
    expect((error as NetworkError).cause).toBeInstanceOf(TypeError);
  });
});

describe('the administrator’s alerts', () => {
  it('lists what has not been acknowledged', async () => {
    server({ 'GET /v1/audit/alerts': () => respond({ alerts: [alert] }) });
    await expect(listAlerts()).resolves.toEqual([alert]);
  });

  it('acknowledges one by id, guarded and idempotent', async () => {
    const calls = server({
      'POST /v1/audit/alerts/0190a8f2-0000-7000-8000-0000000000a1/acknowledge': () =>
        respond({ ...alert, acknowledged_at: '2026-09-14T04:50:00Z' }),
    });

    await acknowledgeAlert(alert.id);

    const request = calls[0]!;
    expect(request.method).toBe('POST');
    // The forgery guard, on a request that changes state. Without it the server refuses
    // with a 403 that looks exactly like a missing permission.
    expect(request.headers.get('X-Requested-With')).toBe('DTHCMS');
    // A fresh key per gesture, so an administrator clicking twice acknowledges twice
    // rather than the second click being answered from the store as a replay.
    expect(request.headers.get('Idempotency-Key')).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    );
  });

  it('gives two acknowledgements two different keys', async () => {
    const calls = server({
      'POST /v1/audit/alerts/0190a8f2-0000-7000-8000-0000000000a1/acknowledge': () =>
        respond(alert),
    });

    await acknowledgeAlert(alert.id);
    await acknowledgeAlert(alert.id);

    expect(calls[0]!.headers.get('Idempotency-Key')).not.toBe(
      calls[1]!.headers.get('Idempotency-Key'),
    );
  });
});

describe('break-glass', () => {
  it('carries the step-up token in the header the server reads', async () => {
    // In any other header this is a privileged action that silently fails, at the one
    // moment somebody is standing in front of a locked record with a reason to open it.
    const calls = server({ 'POST /v1/audit/break-glass': () => respond(access, { status: 201 }) });

    const opened = await openBreakGlass(
      {
        scope_kind: 'patient',
        scope_ref: '0190a8f2-0000-7000-8000-00000000000b',
        justification: 'Unconscious patient, no consent obtainable.',
        hours: 4,
      },
      'step-up-token-abc',
    );

    const request = calls[0]!;
    expect(request.headers.get('X-Step-Up-Token')).toBe('step-up-token-abc');
    expect(request.headers.get('X-Requested-With')).toBe('DTHCMS');
    expect(await request.json()).toEqual({
      scope_kind: 'patient',
      scope_ref: '0190a8f2-0000-7000-8000-00000000000b',
      justification: 'Unconscious patient, no consent obtainable.',
      hours: 4,
    });
    expect(opened.expires_at).toBe('2026-09-14T08:43:00Z');
  });

  it('lists the doors this clinician has open, and the ones everybody has', async () => {
    const calls = server({
      'GET /v1/audit/break-glass/mine': () => respond({ accesses: [access] }),
      'GET /v1/audit/break-glass': () => respond({ accesses: [access, { ...access, id: 'b2' }] }),
    });

    await expect(myBreakGlass()).resolves.toHaveLength(1);
    await expect(listBreakGlass()).resolves.toHaveLength(2);
    // Two different endpoints, not one filtered client-side — the server decides which
    // accesses a person may see.
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/audit/break-glass/mine');
    expect(new URL(calls[1]!.url).pathname).toBe('/v1/audit/break-glass');
  });

  it('closes a door with the reason it was closed', async () => {
    const calls = server({
      'POST /v1/audit/break-glass/0190a8f2-0000-7000-8000-0000000000b1/end': () =>
        respond({ ...access, ended_at: '2026-09-14T05:10:00Z', end_reason: 'consent obtained' }),
    });

    const ended = await endBreakGlass(access.id, 'consent obtained');

    expect(await calls[0]!.json()).toEqual({ reason: 'consent obtained' });
    expect(calls[0]!.headers.get('X-Requested-With')).toBe('DTHCMS');
    expect(ended.end_reason).toBe('consent obtained');
  });
});

describe('the justification the door demands', () => {
  it('is twenty characters, matching the server', () => {
    expect(MIN_JUSTIFICATION).toBe(20);
  });

  /*
   * Counted in code points, on both sides.
   *
   * Bangla writes a syllable as a cluster — "রোগী" is four code points and two shapes to
   * the eye — so a sentence that looks ample can be short and one that looks terse can be
   * long. Whatever the count is, it has to be the count the server will make: a rule the
   * client applies more leniently means the refusal arrives after the clinician has
   * finished typing, in an emergency, which is when they have the least patience for it.
   */
  it.each([
    ['nineteen characters of English', 'Unconscious, no ICE', false],
    ['exactly twenty of English', 'Unconscious, no ICE.', true],
    ['a padded near-miss, once trimmed', '   Unconscious, no  ', false],
    ['nineteen Bangla code points', 'রোগী অজ্ঞান, সম্মতি', false],
    ['twenty-three Bangla code points', 'রোগী অজ্ঞান, সম্মতি নেই', true],
    ['nothing typed at all', '   ', false],
  ])('accepts %s: %s', (_case, text, acceptable) => {
    expect(justificationAcceptable(text)).toBe(acceptable);
  });
});
