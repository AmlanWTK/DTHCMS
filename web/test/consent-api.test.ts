import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError, API_BASE_URL } from '@/lib/api';
import {
  CONSENT_TYPES,
  NEEDS_EVIDENCE,
  NEEDS_WITNESS,
  consentHistory,
  consentTemplates,
  evidenceUploadURL,
  grantConsent,
  listConsents,
  newEventId,
  revokeConsent,
} from '@/features/consent/api/consent';

/**
 * The requests a consent makes (CP36, §15.1).
 *
 * A consent is the clinic's answer to "was this lawful in March?", and the answer is
 * assembled from what these five calls put on the wire. None of the failures below is
 * visible on screen, and all of them are only discovered when somebody asks:
 *
 *  - **a template version sent by the client.** The server looks up the wording in force and
 *    records its digest. A client that could name a version could record a consent to words
 *    the patient never saw — so the absence of that field is asserted, not assumed.
 *  - **a revocation that deletes.** Nothing is deleted; a revocation is recorded beside the
 *    grant. A DELETE here would destroy the grant that made March's SMS lawful.
 *  - **a reused idempotency key or event id.** Two consents taken from one patient in one
 *    sitting are two events. Sharing a key would make the second a replay of the first, and
 *    the clinic would hold one record where it should hold two.
 *  - **a forgery guard left off a write**, which the API refuses with a 422 nobody can read.
 *
 * The unwrapping is asserted too — `.consents`, `.entries`, `.templates`, `.consent` — because
 * a caller handed the envelope instead of the list renders an empty panel, which reads on
 * screen exactly like a patient nobody has asked.
 */

interface Recorded {
  request: Request;
  body: unknown;
}

/** Routes by method and path. Anything unlisted is a test bug, and says so. */
function server(routes: Record<string, (request: Request) => Response>): Recorded[] {
  const seen: Recorded[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const request = new Request(input, init);
    const key = `${request.method} ${new URL(request.url).pathname}`;
    const handler = routes[key];
    if (!handler) throw new Error(`no route for ${key}`);
    // Read the body before the handler does; a Request body can only be consumed once.
    const raw = await request.clone().text();
    seen.push({ request, body: raw === '' ? undefined : (JSON.parse(raw) as unknown) });
    return handler(request);
  });
  vi.stubGlobal('fetch', fetchMock);
  return seen;
}

function respond(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req_consent' },
  });
}

const GRANTED = {
  consent_type: 'research',
  status: 'granted',
  template_version: 3,
  language: 'bn',
  capture_method: 'verbal_attested',
  granted_at: '2026-03-04T04:42:00Z',
  granted_by_code: 'R001',
  has_evidence: false,
};

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('the shape of the capture screen', () => {
  it('offers all five consents, in the order a desk works through them', () => {
    // Care first, because it is the one the visit cannot proceed without; outreach last,
    // because it is the one the clinic wants and the patient owes least.
    expect(CONSENT_TYPES).toEqual([
      'care',
      'communication',
      'research',
      'ai_processing',
      'outreach',
    ]);
  });

  it('knows which methods leave an image and which need a second person', () => {
    // A thumbprint is both: the patient cannot read what they marked, so somebody attests
    // that it was explained, and the mark itself is still filed.
    expect(NEEDS_EVIDENCE).toEqual(['signature', 'thumbprint']);
    expect(NEEDS_WITNESS).toEqual(['thumbprint', 'verbal_attested']);
    expect(NEEDS_EVIDENCE).toContain('thumbprint');
    expect(NEEDS_WITNESS).toContain('thumbprint');
  });

  it('mints a fresh event id every time it is asked', () => {
    // The event id is what makes a retried capture one event rather than two. A constant
    // here would collapse every consent in the clinic into one row.
    const ids = new Set(Array.from({ length: 20 }, () => newEventId()));
    expect(ids.size).toBe(20);
    for (const id of ids) expect(id).toMatch(UUID);
  });
});

describe('reading what a patient has agreed to', () => {
  it('asks for this patient and hands back the list, not the envelope', async () => {
    const seen = server({
      'GET /v1/patients/p-1/consents': () => respond({ consents: [GRANTED] }),
    });

    await expect(listConsents('p-1')).resolves.toEqual([GRANTED]);
    expect(seen[0]!.request.url).toBe(`${API_BASE_URL}/v1/patients/p-1/consents`);
  });

  it('escapes a patient id rather than pasting it into the path', async () => {
    const seen = server({
      'GET /v1/patients/p%2F1/consents': () => respond({ consents: [] }),
    });

    await expect(listConsents('p/1')).resolves.toEqual([]);
    expect(seen).toHaveLength(1);
  });

  it('reads the history as entries, which is the audit answer', async () => {
    // The list says what is true now. The history says what was true in March, which is the
    // question anybody ever actually asks about a consent.
    const entry = {
      consent_type: 'communication',
      action: 'revoked',
      occurred_at: '2026-03-10T04:42:00Z',
      event_id: '0190a8f2-0000-7000-8000-00000000000a',
      actor_code: 'R001',
    };
    const seen = server({
      'GET /v1/patients/p-1/consents/history': () => respond({ entries: [entry] }),
    });

    await expect(consentHistory('p-1')).resolves.toEqual([entry]);
    expect(seen[0]!.request.url).toBe(`${API_BASE_URL}/v1/patients/p-1/consents/history`);
  });

  it.each(['en', 'bn'] as const)('asks for the %s wording by language', async (language) => {
    // The words shown are the words consented to. Fetching the wrong language would put an
    // English form in front of a patient who reads Bangla and file it as informed.
    const seen = server({
      'GET /v1/consent-templates': () =>
        respond({ templates: [{ consent_type: 'care', language }] }),
    });

    await consentTemplates(language);

    expect(new URL(seen[0]!.request.url).searchParams.get('language')).toBe(language);
  });

  it('hands back the templates rather than the envelope', async () => {
    const templates = [{ consent_type: 'care', version: 2, language: 'bn' }];
    server({ 'GET /v1/consent-templates': () => respond({ templates }) });

    await expect(consentTemplates('bn')).resolves.toEqual(templates);
  });

  it('raises the shared ApiError when the caller may not read consents', async () => {
    // [R-02]: the hat being worn decides. A pharmacist opening a patient gets a refusal the
    // panel has to show as a sentence, not as an empty list of consents.
    server({
      'GET /v1/patients/p-1/consents': () =>
        respond(
          {
            error: {
              code: 'FORBIDDEN',
              kind: 'permission',
              message: 'Not permitted for this role.',
              message_bn: 'এই ভূমিকার জন্য অনুমোদিত নয়।',
            },
          },
          403,
        ),
    });

    const error = await listConsents('p-1').catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).messageBN).toBe('এই ভূমিকার জন্য অনুমোদিত নয়।');
  });
});

describe('recording a consent', () => {
  const grant = () =>
    grantConsent('p-1', {
      consent_type: 'research',
      language: 'bn',
      capture_method: 'verbal_attested',
      witnessed_by: '0190a8f2-0000-7000-8000-00000000000b',
    });

  it('never names the wording it consented to', async () => {
    // The whole reason the field is absent. The server resolves the active template and puts
    // its version, language and digest into the event; a client that could name one could
    // record a consent to words the patient never saw.
    const seen = server({
      'POST /v1/patients/p-1/consents': () => respond({ consent: GRANTED }),
    });

    await grant();

    const body = seen[0]!.body as Record<string, unknown>;
    expect(body).not.toHaveProperty('template_version');
    expect(body).not.toHaveProperty('template_digest');
    expect(body).not.toHaveProperty('digest');
  });

  it('sends the event id, the type, the language and how it was captured', async () => {
    const seen = server({
      'POST /v1/patients/p-1/consents': () => respond({ consent: GRANTED }),
    });

    await grant();

    const body = seen[0]!.body as Record<string, unknown>;
    expect(body.consent_type).toBe('research');
    expect(body.language).toBe('bn');
    expect(body.capture_method).toBe('verbal_attested');
    expect(body.witnessed_by).toBe('0190a8f2-0000-7000-8000-00000000000b');
    expect(String(body.event_id)).toMatch(UUID);
  });

  it('carries the forgery guard and an idempotency key on the write', async () => {
    // CP24. Without the key a retried capture is a second consent event; without the guard
    // the API refuses the write with a 422 that reads like a validation error.
    const seen = server({
      'POST /v1/patients/p-1/consents': () => respond({ consent: GRANTED }),
    });

    await grant();

    expect(seen[0]!.request.headers.get('X-Requested-With')).toBe('DTHCMS');
    expect(seen[0]!.request.headers.get('Idempotency-Key')).toMatch(UUID);
  });

  it('treats a second consent from the same patient as a second event', async () => {
    // Two consents in one sitting is the ordinary case at registration. Sharing a key or an
    // event id would let the server answer the second from the first's stored response, and
    // the clinic would hold one record where it should hold two.
    const seen = server({
      'POST /v1/patients/p-1/consents': () => respond({ consent: GRANTED }),
    });

    await grant();
    await grant();

    const keys = seen.map((call) => call.request.headers.get('Idempotency-Key'));
    const events = seen.map((call) => (call.body as { event_id: string }).event_id);
    expect(keys[0]).not.toBe(keys[1]);
    expect(events[0]).not.toBe(events[1]);
  });

  it('sends the evidence key and digest for a signature, and no bytes', async () => {
    // The image goes straight to object storage. If it ever travelled through the API, every
    // signature in the clinic would be in an application log.
    const seen = server({
      'POST /v1/patients/p-1/consents': () => respond({ consent: GRANTED }),
    });

    await grantConsent('p-1', {
      consent_type: 'care',
      language: 'bn',
      capture_method: 'signature',
      evidence_key: 'consent/p-1/0190a8f2.png',
      evidence_sha256: 'a'.repeat(64),
    });

    const body = seen[0]!.body as Record<string, unknown>;
    expect(body.evidence_key).toBe('consent/p-1/0190a8f2.png');
    expect(body.evidence_sha256).toBe('a'.repeat(64));
    expect(JSON.stringify(body)).not.toContain('data:image');
  });

  it('carries who consented when it was not the patient', async () => {
    // A guardian for a minor. Recorded on the grant, because "the patient consented" is not
    // what happened and a record that says so is wrong.
    const seen = server({
      'POST /v1/patients/p-1/consents': () => respond({ consent: GRANTED }),
    });

    await grantConsent('p-1', {
      consent_type: 'care',
      language: 'bn',
      capture_method: 'paper_form',
      paper_reference: 'CONSENT/2026/0137',
      granted_for_relation: 'mother',
      granted_for_name: 'Shirin Akter',
    });

    const body = seen[0]!.body as Record<string, unknown>;
    expect(body.granted_for_relation).toBe('mother');
    expect(body.granted_for_name).toBe('Shirin Akter');
    expect(body.paper_reference).toBe('CONSENT/2026/0137');
  });

  it('hands back the consent, not the envelope', async () => {
    server({ 'POST /v1/patients/p-1/consents': () => respond({ consent: GRANTED }) });
    await expect(grant()).resolves.toEqual(GRANTED);
  });
});

describe('withdrawing a consent', () => {
  const REVOKED = { ...GRANTED, status: 'revoked', revoked_at: '2026-03-10T04:42:00Z' };

  it('posts to a sub-resource rather than deleting the grant', async () => {
    // Nothing is deleted. The grant stays and the revocation is recorded beside it, because
    // both are needed to answer whether a message sent in March was lawful when it was sent.
    const seen = server({
      'POST /v1/patients/p-1/consents/communication/revoke': () => respond({ consent: REVOKED }),
    });

    await expect(revokeConsent('p-1', 'communication')).resolves.toEqual(REVOKED);
    expect(seen[0]!.request.method).toBe('POST');
    expect(seen[0]!.request.url).toBe(
      `${API_BASE_URL}/v1/patients/p-1/consents/communication/revoke`,
    );
  });

  it('assumes the patient asked, because that is who usually does', async () => {
    // The default matters: an unset `requested_by` recorded as "clinic" would turn a
    // patient's withdrawal into an administrative one in the audit trail.
    const seen = server({
      'POST /v1/patients/p-1/consents/outreach/revoke': () => respond({ consent: REVOKED }),
    });

    await revokeConsent('p-1', 'outreach');

    const body = seen[0]!.body as Record<string, unknown>;
    expect(body.requested_by).toBe('patient');
    expect(body).not.toHaveProperty('reason');
  });

  it.each(['guardian', 'clinic'] as const)('records that the %s asked instead', async (who) => {
    const seen = server({
      'POST /v1/patients/p-1/consents/care/revoke': () => respond({ consent: REVOKED }),
    });

    await revokeConsent('p-1', 'care', { requested_by: who, reason: 'Asked us to stop.' });

    const body = seen[0]!.body as Record<string, unknown>;
    expect(body.requested_by).toBe(who);
    expect(body.reason).toBe('Asked us to stop.');
  });

  it('sends a fresh event id and idempotency key for the withdrawal', async () => {
    const seen = server({
      'POST /v1/patients/p-1/consents/research/revoke': () => respond({ consent: REVOKED }),
    });

    await revokeConsent('p-1', 'research');

    expect(String((seen[0]!.body as { event_id: string }).event_id)).toMatch(UUID);
    expect(seen[0]!.request.headers.get('Idempotency-Key')).toMatch(UUID);
    expect(seen[0]!.request.headers.get('X-Requested-With')).toBe('DTHCMS');
  });
});

describe('where a signature is put', () => {
  it('asks for a PNG destination and returns the key, the URL and its expiry', async () => {
    // PNG only: a signature is line art, and JPEG artefacts around thin strokes are exactly
    // what makes a mark arguable later.
    const answer = {
      object_key: 'consent/p-1/0190a8f2.png',
      upload_url: 'https://storage.example/put?sig=abc',
      expires_at: '2026-03-04T04:52:00Z',
    };
    const seen = server({
      'POST /v1/patients/p-1/consents/evidence-url': () => respond(answer),
    });

    await expect(evidenceUploadURL('p-1')).resolves.toEqual(answer);
    expect((seen[0]!.body as Record<string, unknown>).content_type).toBe('image/png');
  });

  it('never asks for a key of its own choosing', async () => {
    // The key is the server's. One a client could choose is one that could be pointed at
    // somebody else's signature.
    const seen = server({
      'POST /v1/patients/p-1/consents/evidence-url': () =>
        respond({ object_key: 'k', upload_url: 'u', expires_at: 'e' }),
    });

    await evidenceUploadURL('p-1');

    const body = seen[0]!.body as Record<string, unknown>;
    expect(body).not.toHaveProperty('object_key');
    expect(Object.keys(body)).toEqual(['content_type']);
  });

  it('is a write, and carries the guard and a key like one', async () => {
    const seen = server({
      'POST /v1/patients/p-1/consents/evidence-url': () =>
        respond({ object_key: 'k', upload_url: 'u', expires_at: 'e' }),
    });

    await evidenceUploadURL('p-1');

    expect(seen[0]!.request.headers.get('X-Requested-With')).toBe('DTHCMS');
    expect(seen[0]!.request.headers.get('Idempotency-Key')).toMatch(UUID);
  });
});
