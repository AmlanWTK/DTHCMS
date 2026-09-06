'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale } from 'next-intl';
import { useMemo } from 'react';

import type { Locale } from '@/lib/i18n/config';

import { buildLookup, type DirectoryLookup, type DirectoryState } from '../lib/lookup';

import { DIRECTORY_KEY, DIRECTORY_STALE_MS, readDirectory } from './directory';

/**
 * The directory, once per session, as a lookup every attribution can ask (CP61).
 *
 * # Why every value may call this
 *
 * A patient's timeline draws dozens of values and each one wants a name. This hook is
 * deliberately cheap to call from a leaf: TanStack Query deduplicates by key, so a screen
 * with forty attributions issues one request, and the hour-long `staleTime` means moving
 * between screens issues none at all.
 *
 * # Why it does not refetch on focus, when everything else does
 *
 * The application's default refetches on window focus because a physician who tabs back is
 * about to act on what is in front of them, and a colleague's value recorded two minutes ago
 * must not still be hidden behind a cache. None of that applies to a list of staff names: it
 * changes on the order of months, and refetching it every time somebody takes a call would
 * be a round trip on a shared connection to be told the same twelve names again.
 *
 * # Why a failure is not an error the caller has to handle
 *
 * There is no `isError` on the way out, and that is the point. A directory that could not be
 * read must not blank out every value on the screen — the values are the record, the names
 * are a convenience laid over it — so the failure becomes `state: 'unavailable'` and every
 * component keeps rendering what the clinical record itself carries: the role, the station,
 * the time. A hook that returned an error would invite a caller to render nothing.
 */
export function useDirectory(): DirectoryLookup {
  const locale = useLocale() as Locale;

  const query = useQuery({
    queryKey: DIRECTORY_KEY,
    queryFn: readDirectory,
    staleTime: DIRECTORY_STALE_MS,
    // Kept in memory well past the point it goes stale, so navigating away from every screen
    // that shows attribution and back again re-reads the cache rather than the network.
    gcTime: DIRECTORY_STALE_MS * 4,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });

  const state: DirectoryState = query.isPending
    ? 'loading'
    : query.data === undefined
      ? 'unavailable'
      : 'ready';

  // Memoised on the response rather than on the query object: `useQuery` returns a fresh
  // object on every render, and rebuilding three maps per render on a screen with forty
  // attributions is exactly the kind of cost that only shows up on the clinic's tablets.
  return useMemo(() => buildLookup(query.data ?? null, locale, state), [query.data, locale, state]);
}
