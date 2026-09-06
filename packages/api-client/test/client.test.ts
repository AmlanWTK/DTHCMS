import { afterEach, describe, expect, it, vi } from 'vitest';
import { z } from 'zod';

import {
  ApiError,
  NetworkError,
  REQUEST_ID_HEADER,
  apiFetch,
  createApiClient,
  fieldCodes,
  unwrap,
} from '../src/index';

/**
 * The runtime, not the generated types — those are checked by `tsc` and by the
 * regeneration diff in CI.
 *
 * What is worth a test is the half a generator cannot produce: which failure becomes
 * which class, whether both languages survive to the screen, and whether the correlation
 * ID an operator will read down the phone actually arrives.
 */

function respond(body: unknown, init: { status?: number; headers?: Record<string, string> } = {}) {
  return new Response(JSON.stringify(body), {
    status: init.status ?? 200,
    headers: { 'Content-Type': 'application/json', ...init.headers },
  });
}

const envelope = {
  error: {
    code: 'patient.not_found',
    kind: 'not_found',
    message: 'No such patient.',
    message_bn: 'এমন কোনো রোগী নেই।',
    correlation_id: 'abc-123',
  },
};

afterEach(() => {
  vi.restoreAllMocks();
});

describe('the generated client', () => {
  it('calls the path the contract declares', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(respond({ status: 'ok', service: 'api', version: '0.1.0-dev' }));
    const client = createApiClient({ baseUrl: 'http://api.test', fetch: fetchMock });

    const result = await unwrap(client.GET('/healthz'));

    expect(result).toEqual({ status: 'ok', service: 'api', version: '0.1.0-dev' });
    const request = fetchMock.mock.calls[0]?.[0] as Request;
    expect(request.url).toBe('http://api.test/healthz');
  });

  it('sends credentials, because the session is a cookie', async () => {
    // ADR-0010. If this ever stops being sent, every authenticated request silently
    // becomes anonymous.
    const fetchMock = vi
      .fn()
      .mockResolvedValue(respond({ status: 'ok', service: 'api', version: '0.1.0-dev' }));
    const client = createApiClient({ baseUrl: 'http://api.test', fetch: fetchMock });

    await unwrap(client.GET('/healthz'));

    const request = fetchMock.mock.calls[0]?.[0] as Request;
    expect(request.credentials).toBe('include');
  });

  it('turns an error response into an ApiError carrying both languages', async () => {
    const fetchMock = vi.fn().mockResolvedValue(respond(envelope, { status: 404 }));
    const client = createApiClient({ baseUrl: 'http://api.test', fetch: fetchMock });

    const error = await unwrap(client.GET('/version')).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).messageEN).toBe('No such patient.');
    expect((error as ApiError).messageBN).toBe('এমন কোনো রোগী নেই।');
    expect((error as ApiError).correlationID).toBe('abc-123');
  });

  it('turns a request that never arrived into a NetworkError', async () => {
    // The distinction is what lets a screen say "you are offline" instead of "the clinic
    // server rejected this", which are different instructions to the person reading.
    const fetchMock = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'));
    const client = createApiClient({ baseUrl: 'http://api.test', fetch: fetchMock });

    await expect(unwrap(client.GET('/healthz'))).rejects.toBeInstanceOf(NetworkError);
  });

  it('treats a 503 from /readyz as an error, not as data', async () => {
    // /readyz answers 503 with a perfectly well-formed body. A client that branched on
    // the body alone would report a dead database as healthy.
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        respond(
          { status: 'unready', service: 'api', version: '0.1.0-dev', checks: { postgres: 'down' } },
          { status: 503 },
        ),
      );
    const client = createApiClient({ baseUrl: 'http://api.test', fetch: fetchMock });

    const error = await unwrap(client.GET('/readyz')).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).retryable).toBe(true);
  });
});

describe('the schema-validated path', () => {
  const schema = z.object({ status: z.string() });

  it('parses the body rather than trusting it', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond({ status: 'ok' })));
    await expect(apiFetch('/healthz', { schema })).resolves.toEqual({ status: 'ok' });
  });

  it('rejects a body that does not match, instead of returning undefined fields', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond({ state: 'ok' })));
    await expect(apiFetch('/healthz', { schema })).rejects.toThrow();
  });

  it('prefers the correlation ID in the body over the one in the header', async () => {
    // The body's is the one written into the log line. The header is a fallback for a
    // response that never reached the handler — a proxy timeout, say.
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValue(
          respond(envelope, { status: 404, headers: { [REQUEST_ID_HEADER]: 'header-999' } }),
        ),
    );

    const error = (await apiFetch('/patients/1', { schema }).catch((e: unknown) => e)) as ApiError;
    expect(error.correlationID).toBe('abc-123');
  });

  it('falls back to the header when the body is not an envelope', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response('<html>502</html>', {
          status: 502,
          headers: { [REQUEST_ID_HEADER]: 'header-999' },
        }),
      ),
    );

    const error = (await apiFetch('/patients/1', { schema }).catch((e: unknown) => e)) as ApiError;
    expect(error.correlationID).toBe('header-999');
    expect(error.code).toBe('unknown');
    // Still bilingual. A gateway error is exactly when the operator is least likely to be
    // reading the language the proxy happens to speak.
    expect(error.messageBN).not.toBe(error.messageEN);
  });
});

describe('what is worth retrying', () => {
  it.each([
    [400, false],
    [403, false],
    [404, false],
    [422, false],
    [408, true],
    [429, true],
    [500, true],
    [503, true],
  ])('status %i retryable: %s', (status, retryable) => {
    const error = new ApiError({
      status,
      code: 'x',
      kind: 'y',
      messageEN: '',
      messageBN: '',
      correlationID: '',
    });
    expect(error.retryable).toBe(retryable);
  });
});

describe('a server that says how long to wait', () => {
  const rateLimited = {
    error: {
      code: 'RATE_LIMITED',
      kind: 'technical',
      message: 'Too many requests.',
      message_bn: 'অনেক বেশি অনুরোধ।',
      correlation_id: 'abc-123',
    },
  };

  async function failing(headers: Record<string, string>): Promise<ApiError> {
    const fetchMock = vi.fn().mockResolvedValue(respond(rateLimited, { status: 429, headers }));
    const client = createApiClient({ baseUrl: 'http://api.test', fetch: fetchMock });
    return (await unwrap(client.GET('/healthz')).catch((error: unknown) => error)) as ApiError;
  }

  it('carries the number to the caller instead of dropping it at the boundary', async () => {
    // §6e. Without this the sync engine falls back to its own ladder, which is a guess about a
    // network made by the side that cannot see the bucket.
    expect((await failing({ 'Retry-After': '45' })).retryAfterSeconds).toBe(45);
  });

  it('reports nothing rather than zero when the server said nothing', async () => {
    // Unknown is not the same as none. Zero would send a client that trusts it straight back
    // into the refusal it was just given.
    expect((await failing({})).retryAfterSeconds).toBeNull();
    // The HTTP-date form is dropped for the same reason: turning it into a duration needs a
    // trustworthy local clock, and this client runs on tablets whose clocks are the thing the
    // sync protocol assumes is wrong.
    expect(
      (await failing({ 'Retry-After': 'Wed, 21 Oct 2026 07:28:00 GMT' })).retryAfterSeconds,
    ).toBeNull();
  });

  it('reads it off a refusal that is not one of ours at all', async () => {
    // A proxy in front of the API answering 429 with its own HTML. There is nothing else in that
    // response worth having.
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        new Response('<html>too many</html>', { status: 429, headers: { 'Retry-After': '120' } }),
      );
    const client = createApiClient({ baseUrl: 'http://api.test', fetch: fetchMock });
    const error = (await unwrap(client.GET('/healthz')).catch(
      (cause: unknown) => cause,
    )) as ApiError;
    expect(error.code).toBe('unknown');
    expect(error.retryAfterSeconds).toBe(120);
  });
});

describe('a field that carries codes rather than a sentence', () => {
  const refusal = (fields: Record<string, string>) =>
    new ApiError({
      status: 422,
      code: 'VALIDATION_FAILED',
      kind: 'validation',
      messageEN: 'Some values need correcting.',
      messageBN: 'কিছু তথ্য সংশোধন করতে হবে।',
      fields,
      correlationID: 'req-1',
    });

  it('splits a comma-separated list and trims each code', () => {
    // The envelope types `fields` as string-to-string, so a list has to arrive as one string.
    // Decoding it here rather than in each screen is what stops two surfaces splitting on
    // different separators and disagreeing about a code with a stray space.
    expect(
      fieldCodes(refusal({ missing_conditions: 'A_ONE, B_TWO' }), 'missing_conditions'),
    ).toEqual(['A_ONE', 'B_TWO']);
    expect(
      fieldCodes(refusal({ missing_conditions: 'A_ONE,B_TWO' }), 'missing_conditions'),
    ).toEqual(['A_ONE', 'B_TWO']);
    expect(fieldCodes(refusal({ missing_conditions: '  A_ONE  ' }), 'missing_conditions')).toEqual([
      'A_ONE',
    ]);
  });

  it('is empty for a field the server did not name, and drops empty entries', () => {
    expect(fieldCodes(refusal({}), 'missing_conditions')).toEqual([]);
    expect(fieldCodes(refusal({ missing_conditions: '' }), 'missing_conditions')).toEqual([]);
    expect(fieldCodes(refusal({ missing_conditions: 'A_ONE,,' }), 'missing_conditions')).toEqual([
      'A_ONE',
    ]);
  });

  it('reads the same codes whatever the interface language is', () => {
    // A code is the same string in both languages. Reading it out of `fieldsBN` would make a
    // Bangla interface depend on the server having duplicated it there.
    const both = new ApiError({
      status: 422,
      code: 'VALIDATION_FAILED',
      kind: 'validation',
      messageEN: 'Some values need correcting.',
      messageBN: 'কিছু তথ্য সংশোধন করতে হবে।',
      fields: { missing_conditions: 'A_ONE' },
      fieldsBN: {},
      correlationID: 'req-2',
    });
    expect(fieldCodes(both, 'missing_conditions')).toEqual(['A_ONE']);
  });
});
