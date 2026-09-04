import { fieldMessages } from '@dthcms/api-client';
import type { ApiError, components } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';
import type { Locale } from '@/lib/i18n/config';

/**
 * The coded terminology client (CP52, §4.6).
 *
 * Four reads, no writes. Nothing here is audited and nothing here is a patient — a search
 * for "diabetes" is a search for a word, and the ledger has better things to hold.
 *
 * Three facts about the server shape everything below, and each of them is a thing this
 * layer must not quietly improve on.
 *
 * **An empty `q` is the favourites.** Not everything, and not an error. A picker opens
 * before anybody has typed, and the honest thing to show then is the clinic's own twenty.
 * `listFavourites` exists as well as the empty search because the contract gives it its own
 * endpoint for exactly that reason: a screen that opens on a list should not have to send a
 * search to get one.
 *
 * **The version in the response is the resolved one.** A caller may name no version, and
 * the server answers with the one it actually used. `codingOf` therefore reads the version
 * off the concept, never off a prop the screen was configured with — see its note. This is
 * the whole of acceptance criterion 2, and it is the one thing in this file that would be
 * expensive to discover was wrong.
 *
 * **A refusal is a sentence, not a status.** Searching SNOMED answers 422 because this
 * deployment is not licensed for it pending D-24, and an unloaded version answers 422 rather
 * than being silently replaced by the default. Both arrive as an `ApiError` carrying a field
 * message, and `refusalText` is what turns that into the words on screen. "No results" and
 * "we are not licensed for that" send a clinician to two different places.
 */

export type CodeSystem = components['schemas']['CodeSystem'];
export type Concept = components['schemas']['Concept'];
export type ConceptList = components['schemas']['ConceptList'];
export type ConceptMapping = components['schemas']['ConceptMapping'];

/**
 * What a coding is: three fields, and not one fewer.
 *
 * `E11.9` on its own is a string. `E11.9` in ICD-10 2019 is a diagnosis. The picker hands
 * this shape to its caller rather than a bare code so that a screen storing a coding
 * physically cannot store half of one.
 */
export interface Coding {
  system: string;
  version: string;
  code: string;
}

/**
 * A coding with the words to show it by — what the picker hands its caller.
 *
 * Both displays travel, not the one the picker happened to be rendering. The screen that
 * stores this may be printed in the other language tomorrow, and re-reading the catalogue to
 * find out what `E11.9` is called in Bangla is a round trip nobody should need to make.
 */
export type ConceptSelection = Pick<
  Concept,
  'system' | 'version' | 'code' | 'display_en' | 'display_bn'
>;

/** The server's ceiling, mirrored so a caller can ask for the whole page without guessing. */
export const MAX_RESULTS = 25;

/** Which terminologies exist, who publishes them, and which this deployment may use. */
export async function listCodeSystems(): Promise<CodeSystem[]> {
  const body = await unwrap(api.GET('/v1/terminology/systems'));
  return body.systems;
}

export interface SearchRequest {
  system: string;
  /** Omit for the system's default. The response says which was used. */
  version?: string;
  /** What was typed. Empty returns the favourites. */
  q?: string;
  limit?: number;
}

/**
 * Autocomplete, tolerant of a misspelling.
 *
 * Returned in the server's order and never re-sorted here. The tiers are a ranking somebody
 * tuned against how this clinic actually types, and a client that re-ordered them would be a
 * second opinion nobody can inspect.
 */
export async function searchConcepts(request: SearchRequest): Promise<ConceptList> {
  const { system, version, q, limit } = request;
  return unwrap(
    api.GET('/v1/terminology/search', {
      params: {
        query: {
          system,
          ...(version === undefined ? {} : { version }),
          ...(q === undefined ? {} : { q }),
          ...(limit === undefined ? {} : { limit }),
        },
      },
    }),
  );
}

/** The clinic's own list, in the order somebody ranked it. What a picker opens on. */
export async function listFavourites(request: {
  system: string;
  version?: string;
}): Promise<ConceptList> {
  const { system, version } = request;
  return unwrap(
    api.GET('/v1/terminology/favourites', {
      params: { query: { system, ...(version === undefined ? {} : { version }) } },
    }),
  );
}

/** One concept and its mappings. `mappings` is empty everywhere until D-24 answers. */
export async function readConcept(request: {
  system: string;
  version?: string;
  code: string;
}): Promise<{ concept: Concept; mappings: ConceptMapping[] }> {
  const { system, version, code } = request;
  return unwrap(
    api.GET('/v1/terminology/concept', {
      params: { query: { system, code, ...(version === undefined ? {} : { version }) } },
    }),
  );
}

/**
 * The words this concept is read in, in the reader's language.
 *
 * Bangla when there is Bangla and the interface is in Bangla; English otherwise. The
 * fallback is deliberate and it goes only one way: a clinician reading Bangla is better
 * served by an English diagnosis than by a blank row they cannot select. `display_en` is
 * required by the contract, so the last resort below should never fire — it is here because
 * a picker row with no text in it is a row a screen reader announces as nothing at all, and
 * the code is at least something a person can look up.
 */
export function conceptLabel(concept: Concept, locale: Locale): string {
  if (locale === 'bn' && concept.display_bn) return concept.display_bn;
  if (concept.display_en) return concept.display_en;
  return concept.display_bn || concept.code;
}

/**
 * The grouping this row is filed under, in the reader's language.
 *
 * Same fallback as the display, and the reason it exists at all is a screenshot: the first
 * Bangla render of this picker showed a list of Bengali diagnoses filed under English
 * chapter names. Half-bilingual is its own failure — it reads as an interface somebody
 * translated the easy parts of. A standing database invariant now refuses a heading with no
 * Bangla form, so the empty case here should be unreachable for seeded content; it stays
 * because an empty heading is legal for a concept that has none.
 */
export function conceptHeading(concept: Concept, locale: Locale): string {
  if (locale === 'bn' && concept.heading_bn) return concept.heading_bn;
  return concept.heading ?? '';
}

/**
 * The three fields that make a coding, taken from the concept the server returned.
 *
 * Read off the concept rather than assembled from the picker's props, because those are two
 * different versions whenever the caller named none: the prop is absent and the server
 * resolved a default. A coding stamped with a version nobody searched is a lie that is only
 * discovered years later, when somebody asks what `E11.9` meant in 2019.
 */
export function codingOf(concept: Concept): Coding {
  return { system: concept.system, version: concept.version, code: concept.code };
}

/**
 * The concept, narrowed to what a caller stores.
 *
 * Built from the concept alone, for the same reason `codingOf` is: every field here is one
 * the server returned about this row, and none of them is a default the screen supplied.
 */
export function selectionOf(concept: Concept): ConceptSelection {
  return {
    ...codingOf(concept),
    display_en: concept.display_en,
    ...(concept.display_bn === undefined ? {} : { display_bn: concept.display_bn }),
  };
}

/** The message keys `tierReason` chooses between. Exported so a caller can exhaust them. */
export type TierReasonKey = 'reason.code' | 'reason.favourite' | 'reason.word' | 'reason.spelling';

/**
 * Why this row is where it is, as a message key.
 *
 * A ranking nobody can inspect is a ranking nobody can tune — the contract returns the tier
 * for that reason, and a picker that received it and drew nothing would be throwing away the
 * only feedback the person tuning it ever gets. It also answers the question a clinician
 * actually asks of a fuzzy match: *why is this here?* "Close to what you typed" is the
 * difference between trusting the fourth row and scrolling past it.
 *
 * `null` on the favourites, which carry no tier because they are not a search. Their rank is
 * shown instead, and it says more than a tier would.
 */
export function tierReason(concept: Concept): TierReasonKey | null {
  switch (concept.tier) {
    case 1:
      return 'reason.code';
    case 2:
      return 'reason.favourite';
    case 3:
      return 'reason.word';
    case 4:
      return 'reason.spelling';
    default:
      return null;
  }
}

/**
 * What the server said, rather than what the client guessed.
 *
 * A 422 here is never generic. It is "SNOMED CT is not licensed for this deployment" or
 * "this version has not been loaded", and both send the person somewhere specific — to the
 * licence decision, or to whoever loads content. Replacing either with "something went
 * wrong" turns a solvable problem into a support call.
 *
 * The field messages come first because they name the parameter that was wrong; the
 * envelope's own message is the fallback. Bangla where the server has it, English where it
 * does not, because silence is worse than the wrong language.
 */
export function refusalText(error: ApiError, locale: string): string | null {
  const named = Object.values(fieldMessages(error, locale)).filter((text) => text.trim() !== '');
  if (named.length > 0) return named.join(' ');

  const envelope = locale === 'bn' ? error.messageBN || error.messageEN : error.messageEN;
  return envelope.trim() === '' ? null : envelope;
}
