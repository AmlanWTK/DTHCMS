import { afterEach, describe, expect, it, vi } from 'vitest';

import { IDEMPOTENCY_HEADER, REQUESTED_WITH_HEADER } from '@dthcms/api-client';
import { isUuidV7 } from '@dthcms/shared-schemas';

import { STEP_UP_HEADER } from '@/features/auth';
import {
  HIGH_IMPACT_FIELDS,
  JUSTIFICATION_MIN,
  REASON_MIN,
  attachPhoto,
  checkDuplicates,
  correctPatient,
  getPatient,
  isHighImpact,
  justificationAcceptable,
  listCorrections,
  listMerges,
  mergePatients,
  newEventId,
  photoURL,
  reasonAcceptable,
  registerPatient,
  uploadTicket,
  type DuplicateProbe,
  type PatientRegistration,
} from '@/features/patients';
import { API_BASE_URL, ApiError } from '@/lib/api';

/**
 * What the patient module actually puts on the wire (CP29, CP30, CP34, CP35).
 *
 * These are the assertions nobody makes by looking at a screen. A photograph is governed by
 * rules that live entirely in the shape of a request: the bytes go to storage and never
 * through this API, so the attach call must carry a key and no image; the object key is the
 * server's, so a client that invents one can point a record at somebody else's face; and the
 * display URL is minted per request, so nothing here may keep one. A correction is
 * append-only for the same kind of reason — it is a PATCH carrying a reason and only the
 * fields that changed, because a request that posts everything it rendered makes "what did
 * they actually alter" unanswerable two years later, which is the one question the history
 * exists to answer.
 *
 * The headers matter as much as the bodies. A missing idempotency key means a double-tap on
 * a slow tablet registers a patient twice; a step-up token that arrives after a refusal
 * instead of before it means the operator retypes a merge justification they have already
 * typed once, on a keyboard they are holding in one hand.
 */

type Route = (request: Request) => Response | Promise<Response>;

function respond(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req_1' },
  });
}

/** Scripts the API and keeps a readable copy of every request that reached it. */
function server(routes: Record<string, Route>): Request[] {
  const calls: Request[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = new Request(input, init);
      calls.push(request.clone());
      const key = `${request.method} ${new URL(request.url).pathname}`;
      const handler = routes[key];
      if (!handler) throw new Error(`no route for ${key}`);
      return handler(request);
    }),
  );
  return calls;
}

async function bodyOf(request: Request): Promise<Record<string, unknown>> {
  return (await request.json()) as Record<string, unknown>;
}

const PATIENT_ID = '5f1d3e2a-0000-4000-8000-000000000001';

const patient = {
  id: PATIENT_ID,
  clinical_id: 'DTHC-FRD-2026-000137',
  name_en: 'Md Rahim Uddin',
  sex: 'male',
  birth: { date: '1985-03-14', precision: 'day', source: 'national_id' },
  phone_primary: '+8801712345678',
  address: {},
  status: 'active',
};

const registration: PatientRegistration = {
  event_id: '0192f0c0-0000-7000-8000-000000000001',
  name_en: 'Md Rahim Uddin',
  sex: 'male',
  birth_date: '1985-03-14',
  dob_precision: 'day',
  dob_source: 'national_id',
  phone_primary: '+8801712345678',
  consent_reference: 'consent-1',
};

const probe: DuplicateProbe = {
  name_en: registration.name_en,
  sex: registration.sex,
  birth_date: registration.birth_date,
  dob_precision: registration.dob_precision,
  dob_source: registration.dob_source,
  phone_primary: registration.phone_primary,
  consent_reference: registration.consent_reference,
};

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('event identifiers', () => {
  it('are never the same twice, because one is one clinical action', () => {
    // A reused event id is how a correction the operator meant to make is silently
    // swallowed as a replay of the previous one.
    const ids = new Set(Array.from({ length: 32 }, () => newEventId()));
    expect(ids.size).toBe(32);
    for (const id of ids) {
      expect(id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/);
    }
  });
});

describe('reading one patient', () => {
  it('returns the record itself rather than the envelope it arrived in', async () => {
    const calls = server({
      [`GET /v1/patients/${PATIENT_ID}`]: () => respond({ patient }),
    });

    await expect(getPatient(PATIENT_ID)).resolves.toMatchObject({
      clinical_id: patient.clinical_id,
    });
    expect(calls[0]?.url).toBe(`${API_BASE_URL}/v1/patients/${PATIENT_ID}`);
    expect(calls[0]?.method).toBe('GET');
  });

  it('raises a refusal as an ApiError carrying the Bangla the screen shows', async () => {
    server({
      [`GET /v1/patients/${PATIENT_ID}`]: () =>
        respond(
          {
            error: {
              code: 'patient.not_found',
              kind: 'not_found',
              message: 'No such patient.',
              message_bn: 'এমন কোনো রোগী নেই।',
            },
          },
          404,
        ),
    });

    const error = await getPatient(PATIENT_ID).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).messageBN).toBe('এমন কোনো রোগী নেই।');
  });
});

describe('registering a patient', () => {
  it('carries the forgery guard and a key that makes a double tap one patient', async () => {
    const calls = server({
      'POST /v1/patients': () => respond({ patient, duplicate: false }, 201),
    });

    await expect(registerPatient(registration)).resolves.toEqual({ patient, duplicate: false });

    const request = calls[0]!;
    expect(request.method).toBe('POST');
    expect(request.headers.get(REQUESTED_WITH_HEADER)).toBe('DTHCMS');
    // A UUIDv7 rather than any string: the server keys its replay store on it, and a v4
    // would sort randomly in that index.
    expect(isUuidV7(request.headers.get(IDEMPOTENCY_HEADER) ?? '')).toBe(true);
    expect(await bodyOf(request)).toEqual(registration);
  });

  it('reports the server calling the second attempt a duplicate rather than hiding it', async () => {
    // The desk needs to know it did not create a second record, not simply that nothing
    // went wrong.
    server({ 'POST /v1/patients': () => respond({ patient, duplicate: true }) });
    await expect(registerPatient(registration)).resolves.toMatchObject({ duplicate: true });
  });
});

describe('the duplicate check', () => {
  it('puts the name, the number and the date in a body, never in a URL', async () => {
    // A URL is written to every access log and proxy buffer between here and the server.
    const calls = server({
      'POST /v1/patients/check-duplicates': () => respond({ verdict: 'clear', candidates: [] }),
    });

    await checkDuplicates(probe);

    const request = calls[0]!;
    expect(request.method).toBe('POST');
    expect(request.url).toBe(`${API_BASE_URL}/v1/patients/check-duplicates`);
    expect(new URL(request.url).search).toBe('');
    expect(request.url).not.toContain('8801712345678');
    expect(await bodyOf(request)).toMatchObject({ phone_primary: '+8801712345678' });
  });

  it('takes a fresh key and a fresh event id every time the desk types', async () => {
    // The desk calls this repeatedly as a name is typed. Reusing one key across changing
    // bodies is exactly what a 409 means, so each call is its own action.
    const calls = server({
      'POST /v1/patients/check-duplicates': () => respond({ verdict: 'clear', candidates: [] }),
    });

    await checkDuplicates(probe);
    await checkDuplicates({ ...probe, name_en: 'Md Rahim Uddi' });

    const keys = calls.map((call) => call.headers.get(IDEMPOTENCY_HEADER));
    expect(keys[0]).not.toBe(keys[1]);
    const events = await Promise.all(calls.map(async (call) => (await bodyOf(call)).event_id));
    expect(events[0]).not.toBe(events[1]);
  });

  it('returns the verdict and its candidates as the server ranked them', async () => {
    const candidates = [
      { patient_id: 'a', clinical_id: 'DTHC-A', score: 92, reasons: ['phone'] },
      { patient_id: 'b', clinical_id: 'DTHC-B', score: 71, reasons: ['name'] },
    ];
    server({
      'POST /v1/patients/check-duplicates': () => respond({ verdict: 'review', candidates }),
    });

    const match = await checkDuplicates(probe);
    expect(match.verdict).toBe('review');
    expect(match.candidates.map((c) => c.clinical_id)).toEqual(['DTHC-A', 'DTHC-B']);
  });
});

describe('merging two histories', () => {
  it('carries the code the operator just proved, rather than retrying after a refusal', async () => {
    // Two clinical histories become one. Submitting first and asking for the code after a
    // 403 loses the justification the operator typed, on a tablet, one-handed.
    const calls = server({
      [`POST /v1/patients/${PATIENT_ID}/merge`]: () => respond({ survivor: patient }),
    });

    const survivor = await mergePatients('step-up-token', PATIENT_ID, {
      merged_id: 'other-1',
      score: 96,
      decision: 'reviewed_match',
      justification: 'Same NID and same mobile; the second record was made at the gate.',
    });

    expect(survivor).toMatchObject({ id: PATIENT_ID });
    const request = calls[0]!;
    expect(request.headers.get(STEP_UP_HEADER)).toBe('step-up-token');
    expect(isUuidV7(request.headers.get(IDEMPOTENCY_HEADER) ?? '')).toBe(true);
    const body = await bodyOf(request);
    expect(body).toMatchObject({ merged_id: 'other-1', decision: 'reviewed_match', score: 96 });
    expect(typeof body.event_id).toBe('string');
  });

  it('lists what has already been merged into this record', async () => {
    // The justification travels with the record. "Two patients became one" is not an
    // answer to somebody holding a discharge letter with the other number on it.
    const merges = [
      {
        merged_id: 'x',
        decision: 'reviewed_match',
        justification: 'Same NID, same mobile, same date of birth.',
        merged_by: 'DOC-002',
        merged_at: '2026-08-01T00:00:00Z',
      },
      {
        merged_id: 'y',
        decision: 'blocked_match',
        justification: 'The identity number already belonged to this record.',
        merged_by: 'DOC-002',
        merged_at: '2026-07-01T00:00:00Z',
      },
    ];
    const calls = server({
      [`GET /v1/patients/${PATIENT_ID}/merges`]: () => respond({ merges }),
    });

    const records = await listMerges(PATIENT_ID);
    expect(records.map((record) => record.justification)).toEqual([
      'Same NID, same mobile, same date of birth.',
      'The identity number already belonged to this record.',
    ]);
    expect(calls[0]?.method).toBe('GET');
  });

  it.each([
    ['', false],
    ['too short', false],
    ['         ', false],
    ['   nine ch   ', false],
    ['exactly 10', true],
    ['Same NID, same mobile, same date of birth.', true],
  ])('treats %j as an acceptable justification: %s', (text, acceptable) => {
    expect(justificationAcceptable(text)).toBe(acceptable);
  });

  it('mirrors the shortest justification the server will take', () => {
    // Mirrored so the button can be disabled instead of the request refused. The server
    // still has the last word, because a rule enforced only in a browser is not a rule.
    expect(JUSTIFICATION_MIN).toBe(10);
  });
});

describe('the photograph', () => {
  const ticket = {
    object_key: 'patients/5f1d/photo/01926c.jpg',
    upload_url: 'https://storage.example.invalid/put?sig=abc',
    expires_at: '2026-09-04T10:15:00Z',
    max_bytes: 8 * 1024 * 1024,
    content_types: ['image/jpeg'],
  };

  it('asks for an upload ticket carrying nothing but the content type', async () => {
    // The bytes are not in this request and never will be: a photograph that enters the API
    // process can end up in a request log, a crash dump or a proxy's buffer.
    const calls = server({
      [`POST /v1/patients/${PATIENT_ID}/photo/upload-url`]: () => respond(ticket),
    });

    await expect(uploadTicket(PATIENT_ID, 'image/jpeg')).resolves.toMatchObject({
      upload_url: ticket.upload_url,
    });

    const request = calls[0]!;
    expect(request.headers.get(REQUESTED_WITH_HEADER)).toBe('DTHCMS');
    expect(isUuidV7(request.headers.get(IDEMPOTENCY_HEADER) ?? '')).toBe(true);
    expect(Object.keys(await bodyOf(request))).toEqual(['content_type']);
  });

  it('sends the server’s own object key back unchanged, with no image bytes beside it', async () => {
    // A key a client could choose is a key that can be pointed at somebody else's
    // photograph; and size and digest are read from the object, not believed from here.
    const calls = server({
      [`POST /v1/patients/${PATIENT_ID}/photo`]: () =>
        respond(
          {
            photo: { ...ticket, byte_size: 41_233, url: 'https://s/x', content_type: 'image/jpeg' },
          },
          201,
        ),
    });

    await attachPhoto(PATIENT_ID, {
      object_key: ticket.object_key,
      content_type: 'image/jpeg',
      width: 640,
      height: 480,
    });

    const request = calls[0]!;
    const body = await bodyOf(request);
    expect(body.object_key).toBe(ticket.object_key);
    expect(Object.keys(body).sort()).toEqual([
      'content_type',
      'event_id',
      'height',
      'object_key',
      'width',
    ]);
    // No `data:` URI, no base64 blob smuggled in under another name.
    expect(JSON.stringify(body)).not.toContain('base64');
    expect(isUuidV7(request.headers.get(IDEMPOTENCY_HEADER) ?? '')).toBe(true);
  });

  it('mints a display URL per request and hands back when it dies', async () => {
    // Never a stored URL: one written into a row has expired by the time anybody reads it
    // and cannot be told from one that never worked.
    const calls = server({
      [`GET /v1/patients/${PATIENT_ID}/photo`]: () =>
        respond({
          url: 'https://storage.example.invalid/get?sig=xyz',
          expires_at: '2026-09-04T10:15:00Z',
        }),
    });

    const signed = await photoURL(PATIENT_ID);
    expect(signed.url).toContain('sig=');
    expect(signed.expires_at).toBe('2026-09-04T10:15:00Z');

    const request = calls[0]!;
    expect(request.method).toBe('GET');
    expect(request.body).toBeNull();
    // The signed URL is on the storage origin, not this API's.
    expect(signed.url.startsWith(API_BASE_URL)).toBe(false);
  });
});

describe('correcting a record', () => {
  const applied = {
    patient,
    changes: [{ field: 'postcode', previous: '7800', current: '7801' }],
    high_impact: false,
    invalidated: [],
    event_id: 'e-1',
  };

  it('is a PATCH carrying a reason and only the field being corrected', async () => {
    // Not a PUT of the whole record. A request that posts everything it rendered makes the
    // history unable to say what anybody actually altered, and lets a stale tab revert a
    // colleague's correction from two minutes ago.
    const calls = server({ [`PATCH /v1/patients/${PATIENT_ID}`]: () => respond(applied) });

    await correctPatient(PATIENT_ID, {
      event_id: '0192f0c0-0000-7000-8000-00000000000a',
      reason: 'The postcard came back; the postcode is 7801.',
      postcode: '7801',
    });

    const request = calls[0]!;
    expect(request.method).toBe('PATCH');
    expect(Object.keys(await bodyOf(request)).sort()).toEqual(['event_id', 'postcode', 'reason']);
    expect(request.headers.get(REQUESTED_WITH_HEADER)).toBe('DTHCMS');
    expect(isUuidV7(request.headers.get(IDEMPOTENCY_HEADER) ?? '')).toBe(true);
  });

  it('sends no step-up header at all for an ordinary field', async () => {
    // An empty header is not the same as an absent one: a proxy that logs headers would
    // record a token-shaped blank, and the server would have to decide what it meant.
    const calls = server({ [`PATCH /v1/patients/${PATIENT_ID}`]: () => respond(applied) });

    await correctPatient(PATIENT_ID, {
      event_id: '0192f0c0-0000-7000-8000-00000000000b',
      reason: 'The patient has moved to Boalmari.',
      upazila: 'Boalmari',
    });

    expect(calls[0]?.headers.has(STEP_UP_HEADER)).toBe(false);
  });

  it('carries the authenticator token when a date of birth is being changed', async () => {
    const calls = server({
      [`PATCH /v1/patients/${PATIENT_ID}`]: () =>
        respond({ ...applied, high_impact: true, changes: [] }),
    });

    const result = await correctPatient(
      PATIENT_ID,
      {
        event_id: '0192f0c0-0000-7000-8000-00000000000c',
        reason: 'The NID card reads 1958; the desk typed 1985.',
        birth_date: '1958-03-14',
      },
      'step-up-token',
    );

    expect(calls[0]?.headers.get(STEP_UP_HEADER)).toBe('step-up-token');
    expect(result.high_impact).toBe(true);
  });

  it('names what was rebuilt rather than reporting that values changed', async () => {
    server({
      [`PATCH /v1/patients/${PATIENT_ID}`]: () =>
        respond({
          ...applied,
          invalidated: [
            {
              derived_name: 'read.patient',
              depends_on: 'name_en',
              action: 'recompute',
              description: 'The search key is computed from the English name.',
            },
          ],
        }),
    });

    const result = await correctPatient(PATIENT_ID, {
      event_id: '0192f0c0-0000-7000-8000-00000000000d',
      reason: 'The NID card spells the name in full.',
      name_en: 'Mohammad Rahim Uddin',
    });

    expect(result.invalidated[0]?.derived_name).toBe('read.patient');
  });

  it('returns the history in the order the server gave it, newest first', async () => {
    // The order is the server's, and reversing it here would put a superseded value under
    // a heading that says it is current.
    const corrections = [
      { field: 'postcode', corrected_at: '2026-09-01T00:00:00Z' },
      { field: 'name_en', corrected_at: '2026-08-01T00:00:00Z' },
      { field: 'phone_primary', corrected_at: '2026-07-01T00:00:00Z' },
    ];
    const calls = server({
      [`GET /v1/patients/${PATIENT_ID}/history`]: () => respond({ corrections }),
    });

    const rows = await listCorrections(PATIENT_ID);
    expect(rows.map((row) => row.field)).toEqual(['postcode', 'name_en', 'phone_primary']);
    expect(calls[0]?.method).toBe('GET');
  });

  it('reads an empty history as a record never corrected, not as a failure', async () => {
    server({ [`GET /v1/patients/${PATIENT_ID}/history`]: () => respond({ corrections: [] }) });
    await expect(listCorrections(PATIENT_ID)).resolves.toEqual([]);
  });
});

describe('which corrections need a second factor', () => {
  it.each([
    ['name_en', true],
    ['sex', true],
    ['birth_date', true],
    ['dob_precision', true],
    ['postcode', false],
    ['phone_primary', false],
    ['address_line', false],
    ['name_bn', false],
  ])('treats %s as high impact: %s', (field, high) => {
    expect(isHighImpact([field])).toBe(high);
  });

  it('asks for the code once when a batch contains one dangerous field', () => {
    expect(isHighImpact(['postcode', 'upazila', 'sex'])).toBe(true);
    expect(isHighImpact(['postcode', 'upazila'])).toBe(false);
    expect(isHighImpact([])).toBe(false);
  });

  it('mirrors the server’s list rather than a paraphrase of it', () => {
    expect([...HIGH_IMPACT_FIELDS]).toEqual(['name_en', 'sex', 'birth_date', 'dob_precision']);
  });
});

describe('the reason a correction carries', () => {
  it.each([
    ['', false],
    ['typo', false],
    ['             ', false],
    ['  short  ', false],
    ['The NID card reads 1958; the desk typed 1985.', true],
  ])('treats %j as acceptable: %s', (text, acceptable) => {
    expect(reasonAcceptable(text)).toBe(acceptable);
  });

  it('is only a length, so the history is what makes a hollow reason visible', () => {
    // "Correction" is ten characters and clears this bar, though the copy on the screen
    // says it is not a reason. Nothing in a browser can judge that — which is precisely
    // why the reason is a column in the history somebody else reads, not a tooltip.
    expect(reasonAcceptable('correction')).toBe(true);
  });

  it('counts the same ten characters the contract does', () => {
    expect(REASON_MIN).toBe(10);
    expect(reasonAcceptable('a'.repeat(REASON_MIN))).toBe(true);
    expect(reasonAcceptable('a'.repeat(REASON_MIN - 1))).toBe(false);
  });
});
