import { ApiError, NetworkError, fieldMessages } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

import type {
  CodeSystem,
  Concept,
  ConceptMapping,
  Locale,
  SearchAnswer,
  SearchRequest,
  Trouble,
} from './search';

/**
 * The four terminology calls, from the station (CP52).
 *
 * # A thin binding, and one seam that is not thin
 *
 * The first four functions are what they look like: the contract's four endpoints, unwrapped
 * into values or thrown errors like every other call this app makes. `runSearch` is the one
 * that earns its keep, and it does two things that belong here rather than in a screen.
 *
 * It picks the endpoint. An empty query is the clinic's favourites, and the catalogue offers
 * those as their own endpoint precisely so that a picker opening on a list does not have to
 * send a search to get one — criterion 1's twenty diagnoses arrive without a query being run.
 *
 * And it **returns failures rather than throwing them**, carrying the sequence number of the
 * request that failed. That is not squeamishness about exceptions. A failure with no sequence
 * number is a failure that cannot be aged, and an old request timing out would then be free
 * to wipe the results of a newer one that had already landed. Keeping the number attached all
 * the way through `search.ts`'s single door is what makes staleness arithmetic instead of
 * luck.
 */

/** Every terminology this deployment holds, and what may be done with each. */
export async function listSystems(): Promise<CodeSystem[]> {
  const body = await unwrap(api.GET('/v1/terminology/systems'));
  return body.systems;
}

/** The clinic's ranked list, in rank order. The response says which version answered. */
export async function listFavourites(params: {
  system: string;
  version?: string;
}): Promise<{ system: string; version: string; concepts: Concept[] }> {
  return unwrap(api.GET('/v1/terminology/favourites', { params: { query: queryOf(params) } }));
}

/** A search. An empty `q` returns the favourites; the server's ranking is never re-sorted. */
export async function searchConcepts(params: {
  system: string;
  version?: string;
  q?: string;
  limit?: number;
}): Promise<{ system: string; version: string; concepts: Concept[] }> {
  return unwrap(api.GET('/v1/terminology/search', { params: { query: queryOf(params) } }));
}

/**
 * One concept and its mappings.
 *
 * What a screen needs to render a coding recorded years ago under a version nobody searches
 * any more — which is the reason the version is stored in the first place.
 */
export async function getConcept(params: {
  system: string;
  version?: string;
  code: string;
}): Promise<{ concept: Concept; mappings: ConceptMapping[] }> {
  return unwrap(
    api.GET('/v1/terminology/concept', {
      params: { query: { ...queryOf(params), code: params.code } },
    }),
  );
}

/**
 * The query string, with the absent parts absent.
 *
 * `version: ''` means "whatever this system's default is" inside the picker, and it must
 * reach the wire as no parameter at all rather than as an empty one — an empty version is
 * refused by the server, and being refused for not choosing is not the same as not choosing.
 */
function queryOf(params: { system: string; version?: string; q?: string; limit?: number }) {
  const query: { system: string; version?: string; q?: string; limit?: number } = {
    system: params.system,
  };
  if (params.version !== undefined && params.version !== '') query.version = params.version;
  if (params.q !== undefined) query.q = params.q;
  if (params.limit !== undefined) query.limit = params.limit;
  return query;
}

/**
 * One request, answered — successfully or not, but always with its sequence number.
 *
 * This never throws. Everything a caller has to decide about a failure is already decided in
 * `troubleOf`, and everything it has to decide about ordering is decided by the number this
 * carries back.
 */
export async function runSearch(request: SearchRequest, locale: Locale): Promise<SearchAnswer> {
  try {
    const body =
      request.q.trim() === ''
        ? await listFavourites({ system: request.system, version: request.version })
        : await searchConcepts({
            system: request.system,
            version: request.version,
            q: request.q,
          });
    return {
      seq: request.seq,
      ok: true,
      system: body.system,
      version: body.version,
      concepts: body.concepts,
    };
  } catch (error) {
    return { seq: request.seq, ok: false, trouble: troubleOf(error, locale) };
  }
}

/**
 * The fields the server can name on a 422, in the order an operator can act on them.
 *
 * The handler names exactly one today. The order exists so that a future refusal naming two
 * is reported by the one the person at the tablet can do something about — the terminology
 * they are searching before the version they did not choose — rather than by whichever key
 * happened to serialise first.
 */
const FIELD_ORDER = ['system', 'version', 'limit', 'code', 'q'];

/**
 * Which field a refusal is about, and what the server said about it.
 *
 * Known fields first, in the order above. A field this build has never heard of still gets
 * shown — with its name, so that a support call can quote it — rather than being swallowed
 * because a newer server knows about a parameter this tablet does not. Sorted, so two of them
 * do not swap places between runs and leave two operators reading different sentences.
 */
function refusalOf(named: Record<string, string>): { field: string; message: string } {
  for (const field of FIELD_ORDER) {
    const message = named[field];
    if (message !== undefined) return { field, message };
  }
  for (const [field, message] of Object.entries(named).sort((a, b) => a[0].localeCompare(b[0]))) {
    return { field, message };
  }
  return { field: '', message: '' };
}

/**
 * What went wrong, in the three shapes the picker knows how to say.
 *
 * A 422 is shown **in the server's own words**. An unknown system, a version this deployment
 * has not loaded, SNOMED pending D-24: each is a sentence the backend already writes in both
 * languages, and a client that paraphrased them would be inventing a second, staler account
 * of somebody else's licensing. The Bangla is the server's too, falling back to its English
 * rather than to a blank space.
 *
 * Everything else is the catalogue being absent, which is a normal condition at a station and
 * not an error to argue with. `NetworkError` means the request never left the tablet; a 500
 * or a gateway page means it left and nothing useful came back. Both leave the operator in
 * the same place, and the picker's answer to both is that free text is still a way forward.
 */
export function troubleOf(error: unknown, locale: Locale): Trouble {
  if (error instanceof NetworkError) return { kind: 'unreachable' };

  if (error instanceof ApiError) {
    if (error.status === 422) {
      const refusal = refusalOf(fieldMessages(error, locale));
      return {
        kind: 'refused',
        field: refusal.field,
        // A 422 that named no field at all is still a refusal, and the server still wrote a
        // sentence for it. Showing nothing would be the one outcome worse than either.
        message: refusal.message === '' ? messageOf(error, locale) : refusal.message,
      };
    }
    return { kind: 'failed', message: messageOf(error, locale) };
  }

  // Something that is not an error this app throws. The screen supplies the sentence; there
  // is nothing here worth showing a clinician.
  return { kind: 'failed', message: '' };
}

function messageOf(error: InstanceType<typeof ApiError>, locale: Locale): string {
  const bengali = error.messageBN.trim();
  if (locale === 'bn' && bengali !== '') return bengali;
  return error.messageEN.trim();
}
