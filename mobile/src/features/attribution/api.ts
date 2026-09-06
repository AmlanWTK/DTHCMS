import { useQuery } from '@tanstack/react-query';
import { useMemo } from 'react';

import { api, unwrap } from '@/lib/api';

import { NO_DIRECTORY, indexDirectory, type Directory, type DirectoryIndex } from './state';

/**
 * The attribution directory, fetched once and read everywhere (CP61, §4.2).
 *
 * # One request per session
 *
 * `GET /v1/directory` is session-only, carries no patient data, and is the same answer for
 * every screen for the whole clinic session: staff, devices and stations of this facility.
 * `DIRECTORY_QUERY_KEY` is one key, so React Query answers the second and hundredth caller
 * from cache and coalesces simultaneous first callers into a single request. Forty values on
 * one screen is one fetch.
 *
 * # Why the component reads this rather than taking a prop
 *
 * Every other feature in this application takes its data as props, and this one deliberately
 * does not. The reason is the one the server gave for not joining a name onto every clinical
 * value: if resolving the author is something each screen has to remember to arrange, then
 * every future screen is one forgotten prop away from rendering a value with no attribution —
 * and criterion 4 is that no screen renders a clinical value without it. The lookup has to be
 * invisible at the call site or it will not survive the next checkpoint.
 *
 * Nothing is decided here. The fetch is a fetch and `indexDirectory` is a pure function in
 * `state.ts` with tests beside it; this module owns caching and nothing else.
 *
 * # A directory that never arrives is not an error
 *
 * The clinic's link drops for stretches of a session by design (ADR-0004), and a tablet that
 * cannot reach the server still has ids, roles, stations, times and sources on every value it
 * is holding. So a failure resolves to `NO_DIRECTORY` rather than throwing: names go missing,
 * the reveal says why, and nothing else on the screen changes. A version of this that let the
 * failure propagate would blank the attribution on every value the moment one request failed,
 * which is precisely the behaviour that teaches people to ignore attribution.
 */

export const DIRECTORY_QUERY_KEY = ['directory'] as const;

/** The call itself. Throws like every other call this app makes. */
export async function getDirectory(): Promise<Directory> {
  return unwrap(api.GET('/v1/directory'));
}

/**
 * How long the directory is treated as current.
 *
 * A whole clinic session. Staff are not enrolled and stations are not renamed during a
 * morning, and a re-fetch on every screen change spends a connection that may not be there.
 * The response carries `as_of` so a screen can say how old its answer is — which is the honest
 * alternative to pretending a cached list is live.
 */
export const DIRECTORY_STALE_MS = 8 * 60 * 60_000;

/**
 * The directory as a lookup, cached across every value on every screen.
 *
 * Returns `NO_DIRECTORY` while loading and after a failure. The index is memoised on the
 * response object rather than rebuilt per component, so a list of forty rows builds three maps
 * once rather than forty times.
 */
export function useDirectoryIndex(): DirectoryIndex {
  const { data } = useQuery({
    queryKey: DIRECTORY_QUERY_KEY,
    queryFn: getDirectory,
    staleTime: DIRECTORY_STALE_MS,
    gcTime: DIRECTORY_STALE_MS,
    // The names are a courtesy on top of a value that is already complete without them. A
    // screen must not be held up, and a failure must not become a thrown error inside a row
    // that is rendering a clinical value.
    throwOnError: false,
  });
  return useMemo(() => indexOf(data), [data]);
}

/**
 * The index for one directory response, remembered against the response itself.
 *
 * A `WeakMap` rather than a single slot: React Query hands back the same object identity until
 * the data actually changes, so this is one build per fetch, and the entry disappears with the
 * response it belongs to.
 */
const indexes = new WeakMap<Directory, DirectoryIndex>();

function indexOf(directory: Directory | undefined): DirectoryIndex {
  if (directory === undefined) return NO_DIRECTORY;
  const existing = indexes.get(directory);
  if (existing !== undefined) return existing;
  const built = indexDirectory(directory);
  indexes.set(directory, built);
  return built;
}
