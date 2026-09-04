import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError, API_BASE_URL, NetworkError } from '@/lib/api';
import {
  codingOf,
  conceptHeading,
  conceptLabel,
  listCodeSystems,
  listFavourites,
  readConcept,
  refusalText,
  searchConcepts,
  selectionOf,
  tierReason,
  type Concept,
} from '@/features/terminology/api/terminology';

/**
 * The client half of the coded picker (CP52).
 *
 * Four reads and a handful of pure functions, and only three things here are worth a test on
 * their own.
 *
 *  - **A version the caller did not name must not appear in the request.** `?version=` is not
 *    "the default" to a server that parses it, and the contract refuses an unloaded version
 *    rather than substituting one — which is the behaviour that keeps a coding honest.
 *  - **A 422 is a sentence, not a status.** SNOMED is registered and unlicensed pending D-24,
 *    and the server says so in words. `refusalText` is what carries those words to the
 *    screen; a client that replaced them with "something went wrong" would send somebody to
 *    the developer for a decision that is already written down.
 *  - **A coding is three fields.** `codingOf` and `selectionOf` read every one of them off
 *    the concept the server returned, never off what the caller asked for. That is acceptance
 *    criterion 2, tested here at the layer where it would first go wrong.
 */

function server(routes: Record<string, (request: Request) => Response>): Request[] {
  const seen: Request[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = new Request(input, init);
      seen.push(request);
      const key = `${request.method} ${new URL(request.url).pathname}`;
      const handler = routes[key];
      if (!handler) throw new Error(`no route for ${key}`);
      return handler(request);
    }),
  );
  return seen;
}

function respond(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req_term_1' },
  });
}

function refusal(fields: Record<string, string>, fieldsBN: Record<string, string> = {}) {
  return respond(
    {
      error: {
        code: 'validation_failed',
        kind: 'validation',
        message: 'The request could not be processed.',
        message_bn: 'অনুরোধটি প্রক্রিয়া করা যায়নি।',
        fields,
        fields_bn: fieldsBN,
        correlation_id: 'req_term_1',
      },
    },
    422,
  );
}

function concept(over: Partial<Concept> = {}): Concept {
  return {
    system: 'ICD10',
    version: '2019',
    code: 'E11.9',
    display_en: 'Type 2 diabetes mellitus without complications',
    display_bn: 'টাইপ ২ ডায়াবেটিস মেলিটাস, জটিলতাবিহীন',
    heading: 'Endocrine, nutritional and metabolic diseases',
    ...over,
  };
}

const LIST = { system: 'ICD10', version: '2019', concepts: [concept()] };

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('reading the catalogue', () => {
  it('hands back the systems rather than the envelope', async () => {
    const seen = server({
      'GET /v1/terminology/systems': () =>
        respond({
          systems: [
            {
              code: 'ICD10',
              title_en: 'ICD-10',
              title_bn: 'আইসিডি-১০',
              publisher: 'World Health Organization',
              usable: true,
              default_version: '2019',
            },
          ],
        }),
    });

    const systems = await listCodeSystems();

    expect(seen[0]!.url.startsWith(API_BASE_URL)).toBe(true);
    expect(systems).toHaveLength(1);
    expect(systems[0]!.code).toBe('ICD10');
  });

  it('names the system, the query and the limit on a search', async () => {
    const seen = server({ 'GET /v1/terminology/search': () => respond(LIST) });

    await searchConcepts({ system: 'ICD10', q: 'dia', limit: 10 });

    const url = new URL(seen[0]!.url);
    expect(url.pathname).toBe('/v1/terminology/search');
    expect(url.searchParams.get('system')).toBe('ICD10');
    expect(url.searchParams.get('q')).toBe('dia');
    expect(url.searchParams.get('limit')).toBe('10');
  });

  it('sends no version at all when the caller named none', async () => {
    // `?version=` is a version to a server that parses it, and the contract refuses one it
    // has not loaded rather than quietly using the default. Sending an empty one would turn
    // "use your default" into a refusal nobody asked for.
    const seen = server({ 'GET /v1/terminology/search': () => respond(LIST) });

    await searchConcepts({ system: 'ICD10', q: 'dia' });

    expect(new URL(seen[0]!.url).searchParams.has('version')).toBe(false);
  });

  it('sends the version when the caller named one', async () => {
    const seen = server({ 'GET /v1/terminology/search': () => respond(LIST) });

    await searchConcepts({ system: 'ICD10', version: '2016', q: 'dia' });

    expect(new URL(seen[0]!.url).searchParams.get('version')).toBe('2016');
  });

  it('sends no query at all rather than an empty one, which is the favourites', async () => {
    // `?q=` and no `q` mean the same thing to this server, but only one of them says so. The
    // picker reaches the favourites through their own endpoint; a caller that wants the same
    // answer from `search` should send the same request the contract describes.
    const seen = server({ 'GET /v1/terminology/search': () => respond(LIST) });

    await searchConcepts({ system: 'ICD10' });

    expect(new URL(seen[0]!.url).searchParams.has('q')).toBe(false);
  });

  it('keeps the resolved system and version beside the concepts', async () => {
    // The whole point of the envelope. A caller handed only `concepts` would have to invent
    // the version from somewhere, and there is nowhere honest to invent it from.
    server({ 'GET /v1/terminology/search': () => respond(LIST) });

    await expect(searchConcepts({ system: 'icd10', q: 'dia' })).resolves.toMatchObject({
      system: 'ICD10',
      version: '2019',
    });
  });

  it('asks the favourites endpoint for the clinic list, not a search with no query', async () => {
    const seen = server({ 'GET /v1/terminology/favourites': () => respond(LIST) });

    await listFavourites({ system: 'ICD10' });

    const url = new URL(seen[0]!.url);
    expect(url.pathname).toBe('/v1/terminology/favourites');
    expect(url.searchParams.has('version')).toBe(false);
  });

  it('names the version on the favourites when the caller pinned one', async () => {
    const seen = server({ 'GET /v1/terminology/favourites': () => respond(LIST) });

    await listFavourites({ system: 'ICD10', version: '2016' });

    expect(new URL(seen[0]!.url).searchParams.get('version')).toBe('2016');
  });

  it('reads a concept back without a version, for a code whose version is not known', async () => {
    const seen = server({
      'GET /v1/terminology/concept': () => respond({ concept: concept(), mappings: [] }),
    });

    await readConcept({ system: 'ICD10', code: 'E11.9' });

    expect(new URL(seen[0]!.url).searchParams.has('version')).toBe(false);
  });

  it('reads one concept back with its mappings', async () => {
    const seen = server({
      'GET /v1/terminology/concept': () => respond({ concept: concept(), mappings: [] }),
    });

    const read = await readConcept({ system: 'ICD10', version: '2019', code: 'E11.9' });

    // Query parameters rather than path segments, because an ICD-10 code contains a full stop
    // and a proxy is entitled to normalise a path.
    const url = new URL(seen[0]!.url);
    expect(url.searchParams.get('code')).toBe('E11.9');
    expect(url.searchParams.get('version')).toBe('2019');
    expect(read.concept.code).toBe('E11.9');
    expect(read.mappings).toEqual([]);
  });
});

describe('a refusal arrives as a refusal', () => {
  it('raises an ApiError carrying the field the server named', async () => {
    server({
      'GET /v1/terminology/search': () =>
        refusal({ system: 'SNOMED CT is not licensed here (D-24).' }),
    });

    const error = await searchConcepts({ system: 'SNOMED', q: 'dia' }).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(422);
    expect((error as ApiError).fields.system).toContain('not licensed');
  });

  it('raises a NetworkError when the request never arrives', async () => {
    // The picker draws these two differently — one says why the server refused, the other
    // says the clinician may write the diagnosis in their own words. It picks by the class.
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))),
    );

    await expect(listFavourites({ system: 'ICD10' })).rejects.toBeInstanceOf(NetworkError);
  });
});

describe('refusalText says what the server said', () => {
  function apiError(over: Partial<ConstructorParameters<typeof ApiError>[0]> = {}): ApiError {
    return new ApiError({
      status: 422,
      code: 'validation_failed',
      kind: 'validation',
      messageEN: 'The request could not be processed.',
      messageBN: 'অনুরোধটি প্রক্রিয়া করা যায়নি।',
      correlationID: 'req_term_1',
      ...over,
    });
  }

  it('prefers the field message, which names what was wrong', () => {
    const error = apiError({ fields: { system: 'SNOMED CT is not licensed here (D-24).' } });

    expect(refusalText(error, 'en')).toBe('SNOMED CT is not licensed here (D-24).');
  });

  it('reads the Bangla field message to a Bangla reader', () => {
    const error = apiError({
      fields: { system: 'SNOMED CT is not licensed here (D-24).' },
      fieldsBN: { system: 'এখানে স্নোমেড সিটি ব্যবহারের অনুমতি নেই (ডি-২৪)।' },
    });

    expect(refusalText(error, 'bn')).toBe('এখানে স্নোমেড সিটি ব্যবহারের অনুমতি নেই (ডি-২৪)।');
  });

  it('falls back to English when only English exists, rather than to silence', () => {
    // Silence is the worst of the three outcomes for somebody standing at a desk.
    const error = apiError({ fields: { version: 'Version 2016 has not been loaded.' } });

    expect(refusalText(error, 'bn')).toBe('Version 2016 has not been loaded.');
  });

  it('falls back to the envelope message when the server named no field', () => {
    expect(refusalText(apiError(), 'en')).toBe('The request could not be processed.');
    expect(refusalText(apiError(), 'bn')).toBe('অনুরোধটি প্রক্রিয়া করা যায়নি।');
  });

  it('joins several field messages rather than picking one', () => {
    const error = apiError({ fields: { system: 'Unknown system.', version: 'Unknown version.' } });

    expect(refusalText(error, 'en')).toBe('Unknown system. Unknown version.');
  });

  it('reads the English envelope to a Bangla reader when there is no Bangla envelope', () => {
    const error = apiError({ messageBN: '' });

    expect(refusalText(error, 'bn')).toBe('The request could not be processed.');
  });

  it('gives back nothing when the server said nothing, so a caller can supply its own words', () => {
    const error = apiError({ messageEN: '', messageBN: '' });

    expect(refusalText(error, 'en')).toBeNull();
  });
});

describe('a coding is three fields', () => {
  it('takes all three off the concept the server returned', () => {
    expect(codingOf(concept())).toEqual({ system: 'ICD10', version: '2019', code: 'E11.9' });
  });

  it('carries both displays into the selection, not only the one on screen', () => {
    // The record may be printed in the other language tomorrow, and re-reading the catalogue
    // to find out what E11.9 is called in Bangla is a round trip nobody should need.
    expect(selectionOf(concept())).toEqual({
      system: 'ICD10',
      version: '2019',
      code: 'E11.9',
      display_en: 'Type 2 diabetes mellitus without complications',
      display_bn: 'টাইপ ২ ডায়াবেটিস মেলিটাস, জটিলতাবিহীন',
    });
  });

  it('omits the Bangla display rather than storing an empty one', () => {
    const selection = selectionOf(concept({ display_bn: undefined }));

    expect('display_bn' in selection).toBe(false);
  });
});

describe('conceptLabel', () => {
  it('reads Bangla to a Bangla reader', () => {
    expect(conceptLabel(concept(), 'bn')).toBe('টাইপ ২ ডায়াবেটিস মেলিটাস, জটিলতাবিহীন');
  });

  it('reads English to an English reader even when Bangla exists', () => {
    expect(conceptLabel(concept(), 'en')).toBe('Type 2 diabetes mellitus without complications');
  });

  it('falls back to English for a Bangla reader when there is no Bangla', () => {
    // An English diagnosis is worth more than a blank row somebody cannot select.
    expect(conceptLabel(concept({ display_bn: undefined }), 'bn')).toBe(
      'Type 2 diabetes mellitus without complications',
    );
  });

  it('never returns an empty string', () => {
    // A row with no text is a row a screen reader announces as nothing at all. The code is at
    // least something a person can look up.
    expect(conceptLabel(concept({ display_en: '', display_bn: undefined }), 'en')).toBe('E11.9');
    expect(conceptLabel(concept({ display_en: '' }), 'en')).toBe(
      'টাইপ ২ ডায়াবেটিস মেলিটাস, জটিলতাবিহীন',
    );
  });
});

describe('conceptHeading', () => {
  it('reads the grouping in Bangla to a Bangla reader', () => {
    // The bug this exists for: a column of Bengali diagnoses filed under English chapter
    // names. Half-bilingual reads as an interface somebody translated the easy parts of.
    expect(conceptHeading(concept({ heading: 'Diabetes', heading_bn: 'ডায়াবেটিস' }), 'bn')).toBe(
      'ডায়াবেটিস',
    );
  });

  it('reads English to an English reader even when Bangla exists', () => {
    expect(conceptHeading(concept({ heading: 'Diabetes', heading_bn: 'ডায়াবেটিস' }), 'en')).toBe(
      'Diabetes',
    );
  });

  it('falls back to English rather than showing nothing', () => {
    // Unreachable for seeded content — a standing database rule refuses a heading with no
    // Bangla form — and kept anyway, because the fallback is what the screen does the day
    // somebody loads a code set the rule has not seen yet.
    expect(conceptHeading(concept({ heading: 'Diabetes' }), 'bn')).toBe('Diabetes');
  });

  it('is empty for a concept with no grouping, which is legal', () => {
    // A complaint in the clinic's own dictionary need not sit under anything.
    expect(conceptHeading(concept({ heading: undefined }), 'en')).toBe('');
    expect(conceptHeading(concept({ heading: undefined }), 'bn')).toBe('');
  });
});

describe('tierReason', () => {
  it('names each of the four tiers', () => {
    expect(tierReason(concept({ tier: 1 }))).toBe('reason.code');
    expect(tierReason(concept({ tier: 2 }))).toBe('reason.favourite');
    expect(tierReason(concept({ tier: 3 }))).toBe('reason.word');
    expect(tierReason(concept({ tier: 4 }))).toBe('reason.spelling');
  });

  it('has nothing to say about a favourite, which is not a search result', () => {
    // The favourites endpoint returns no tier. Its rank says more than a tier would.
    expect(tierReason(concept({ favourite_rank: 1 }))).toBeNull();
  });
});
