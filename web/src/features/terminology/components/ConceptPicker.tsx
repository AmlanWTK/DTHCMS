'use client';

import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useEffect, useId, useState, type KeyboardEvent, type ReactNode } from 'react';

import { AlertBanner, EmptyState, Input, Skeleton } from '@dthcms/ui';

import { ApiError, NetworkError } from '@/lib/api';
import { formatCount } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';

import {
  MAX_RESULTS,
  conceptHeading,
  conceptLabel,
  listFavourites,
  refusalText,
  searchConcepts,
  selectionOf,
  tierReason,
  type Concept,
  type ConceptSelection,
} from '../api/terminology';

import { ConceptChip } from './ConceptChip';

/**
 * The coded terminology picker (CP52, §4.6).
 *
 * # What it hands back
 *
 * `onSelect` is given a system, a version **and** a code, taken from the row the server
 * returned. That is acceptance criterion 2, and it is the only thing here that would be
 * expensive to get wrong: a coding with no version is a string, and the day somebody asks
 * what `E11.9` meant when it was recorded, the answer has to be in the record rather than in
 * whatever the picker was configured with at the time. The caller may name no version at
 * all — the server resolves its default and says which, and that is the one that travels.
 *
 * # Why it opens on the favourites
 *
 * The empty query is not an empty screen. Before a key is pressed this shows the twenty
 * diagnoses this clinic actually makes, ranked, which is how criterion 1 is met — three
 * keystrokes, because the common ones are already on screen — and it is what the picker
 * shows the majority of the times it is opened. The rank is drawn on the row so that it
 * reads as *the clinic's list* rather than as some results the search happened to return.
 *
 * # Why the query text is in the cache key
 *
 * A station operator types faster than a shared clinic connection answers, so the reply to
 * `dia` regularly lands after the reply to `diab`. Keying the query on the text makes the
 * older reply land in a cache entry nobody is looking at, rather than in the list — a
 * property of the structure, not a race that usually resolves the right way. There is no
 * abort, no sequence number and no "is this still the latest" check to get wrong later.
 *
 * # Why a failure is two different sentences
 *
 * A 422 is the server saying something specific: SNOMED CT is not licensed for this
 * deployment pending D-24, or this version has not been loaded. Its own words go on screen.
 * A network failure is something else entirely, and the useful half of that message is not
 * "try again" — it is that the clinician may write the diagnosis in their own words and let
 * somebody code it later. A picker that swallowed either one and showed an empty list would
 * be telling a clinician that their patient's diagnosis does not exist.
 *
 * # Why every row says why it is there
 *
 * The tier is on the row as a word: the code you typed, the clinic's list, a word match, a
 * close spelling. Never a colour and never a rank number alone. It answers the question a
 * clinician actually asks of a fuzzy match — *why is this here?* — and it is the only
 * feedback anybody tuning the ranking will ever get from the floor.
 */

/**
 * How long the picker waits before asking.
 *
 * 250ms is under the threshold at which a list feels like it is lagging behind the keyboard,
 * and long enough that typing "diabetes" is one request rather than eight on a connection
 * shared with the rest of the building.
 */
export const SEARCH_DEBOUNCE_MS = 250;

/** The cache key. Exported because the query text being *in* it is load-bearing — see above. */
export function conceptQueryKey(system: string, version: string | undefined, q: string) {
  return ['terminology', 'concepts', system, version ?? 'default', q] as const;
}

export interface ConceptPickerProps {
  /** Which terminology. `ICD10` today; `SNOMED` answers 422 and the picker says why. */
  system: string;
  /**
   * Which version, or none.
   *
   * Naming none is the normal case: the server resolves the system's default and reports
   * what it used. Nothing on screen or in `onSelect` is ever stamped with this prop.
   */
  version?: string;
  /** The chosen coding, or nothing. Controlled — the picker holds no selection of its own. */
  value?: ConceptSelection | null;
  onSelect: (selection: ConceptSelection) => void;
  /** Omit and the chip has no remove button, which is right where the field is mandatory. */
  onClear?: () => void;
  label?: ReactNode;
  description?: ReactNode;
  disabled?: boolean;
  /** Capped by the server at 25 whatever is asked for. */
  limit?: number;
}

export function ConceptPicker({
  system,
  version,
  value = null,
  onSelect,
  onClear,
  label,
  description,
  disabled = false,
  limit = MAX_RESULTS,
}: ConceptPickerProps) {
  const t = useTranslations('terminology');
  const locale = useLocale() as Locale;

  const listboxId = useId();

  /** What is in the box this instant. */
  const [text, setText] = useState('');
  /** What has been asked for. Trails `text` by the debounce, and is what the key is built on. */
  const [query, setQuery] = useState('');
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);

  useEffect(() => {
    if (text === query) return;
    const timer = setTimeout(() => setQuery(text), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [text, query]);

  const results = useQuery({
    queryKey: conceptQueryKey(system, version, query),
    // Two endpoints, one list. The empty query goes to `favourites` rather than to a search
    // with no `q`: the contract gives the clinic's list its own endpoint precisely so that a
    // screen opening on it does not have to send a search to get one.
    queryFn: () =>
      query === ''
        ? listFavourites({ system, version })
        : searchConcepts({ system, version, q: query, limit }),
    // Nothing is fetched until somebody puts the cursor in the box. A picker that loaded on
    // mount would cost a round trip on every screen that merely contains one.
    enabled: open,
    // Keeps the last list on screen while the next one is in flight, so the box does not
    // blink empty between keystrokes — which reads as "no such diagnosis" for the fraction
    // of a second it takes somebody to stop typing and look.
    placeholderData: keepPreviousData,
  });

  const concepts = results.data?.concepts ?? [];
  // Clamped rather than trusted: the list is replaced under it whenever a reply lands.
  const activeIndex = Math.min(active, Math.max(0, concepts.length - 1));

  // Back to the top whenever the list itself changes. Leaving the highlight on row four of a
  // list that now has two is how Enter selects something nobody looked at.
  useEffect(() => {
    setActive(0);
  }, [results.data]);

  const failure = results.error;
  /** The server's own sentence about why it refused — a licence, an unloaded version. */
  const refused =
    failure instanceof ApiError ? (refusalText(failure, locale) ?? t('refused')) : null;
  /** The request never arrived, which is a different instruction entirely. */
  const unreachable = failure instanceof NetworkError;
  /**
   * Whether there is a list on screen at all.
   *
   * The same boolean gates the markup, `aria-activedescendant` and what Enter does, because
   * those three disagreeing is precisely how a combobox comes to announce a row that is not
   * there or select one nobody can see.
   */
  const showList = open && failure === null && !results.isPending && concepts.length > 0;

  function choose(concept: Concept) {
    // From the concept, never from the props — see the note at the top of the file.
    onSelect(selectionOf(concept));
    setOpen(false);
    setText('');
    // Reset without waiting for the debounce, so re-opening shows the clinic's list rather
    // than the results of a search nobody is still making.
    setQuery('');
  }

  function onKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      if (!open) {
        setOpen(true);
        return;
      }
      if (!showList) return;
      const step = event.key === 'ArrowDown' ? 1 : -1;
      setActive((concepts.length + activeIndex + step) % concepts.length);
      return;
    }

    if (event.key === 'Enter') {
      const concept = showList ? concepts[activeIndex] : undefined;
      if (!concept) return;
      // Only when something is actually being chosen. A picker that swallowed every Enter
      // would stop the form around it submitting when the list is closed.
      event.preventDefault();
      choose(concept);
      return;
    }

    if (event.key === 'Escape') {
      event.preventDefault();
      // First press closes the list; a second clears what was typed. Closing without
      // clearing is what somebody wants when they meant to read the row behind the panel.
      if (open) setOpen(false);
      else setText('');
    }
  }

  return (
    <div className="app-concept-picker">
      {value && (
        <div className="app-concept-picker__chosen">
          <span className="app-concept-picker__chosen-label">{t('selected')}</span>
          <ConceptChip concept={value} onRemove={onClear} />
        </div>
      )}

      <Input
        label={label ?? t('label')}
        description={description ?? t('description')}
        placeholder={t('placeholder')}
        value={text}
        disabled={disabled}
        onChange={(event) => {
          setText(event.target.value);
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        onKeyDown={onKeyDown}
        // The full ARIA combobox pattern rather than an approximation of it. Focus never
        // leaves this input: the highlight travels as `aria-activedescendant`, which is what
        // lets a screen reader announce each row as it is reached instead of announcing
        // nothing at all. A station operator opens this forty times a day.
        role="combobox"
        // The panel is open, whatever is in it — a list, a skeleton, or the reason there is
        // no list. `aria-controls` and `aria-activedescendant` are set only while the listbox
        // is genuinely on the page: an id reference to an element that was never rendered is
        // a validation failure, and worse, it is one assistive technology follows.
        aria-expanded={open}
        aria-controls={showList ? listboxId : undefined}
        aria-autocomplete="list"
        aria-activedescendant={showList ? optionId(listboxId, activeIndex) : undefined}
        // The browser's own history dropdown would sit on top of the listbox.
        autoComplete="off"
        spellCheck={false}
      />

      {/* How many, spoken politely. Without it a keyboard user has no way of knowing whether
          typing another letter found more rows or emptied the list. */}
      <p className="dthc-visually-hidden" role="status">
        {open && results.isSuccess ? t('resultCount', { count: concepts.length }) : ''}
      </p>

      {open && (
        <div className="app-concept-picker__panel">
          {unreachable ? (
            // Not `critical`: this does not interrupt a screen reader mid-sentence, because
            // the clinic is not in danger — the catalogue is unreachable and there is
            // something useful the clinician can do instead.
            <AlertBanner tone="unknown" title={t('unreachable')}>
              {t('unreachableBody')}
            </AlertBanner>
          ) : refused !== null ? (
            // The server's own sentence as the headline. "Something went wrong" would send
            // somebody to the developer for a licence decision that is already written down.
            <AlertBanner tone="unknown" title={refused} />
          ) : results.isPending ? (
            <Skeleton height="8rem" />
          ) : concepts.length === 0 ? (
            <EmptyState
              icon="inbox"
              title={query === '' ? t('empty.noFavourites') : t('empty.title', { query })}
            >
              {query === '' ? t('empty.noFavouritesBody') : t('empty.body')}
            </EmptyState>
          ) : (
            <>
              {/* The resolved pair, stated once above the list. The rows below are all in it,
                  and seeing it here is what makes the version on the chip unsurprising. */}
              {results.data && (
                <p className="app-concept-picker__resolved">
                  {t('searchedIn', {
                    system: results.data.system,
                    version: results.data.version,
                  })}
                </p>
              )}

              <ul
                className="app-concept-picker__list"
                id={listboxId}
                role="listbox"
                aria-label={query === '' ? t('favouritesLabel') : t('listLabel')}
                aria-busy={results.isFetching || undefined}
              >
                {concepts.map((concept, index) => (
                  <li
                    key={`${concept.system}|${concept.version}|${concept.code}`}
                    id={optionId(listboxId, index)}
                    role="option"
                    aria-selected={index === activeIndex}
                    className="app-concept-picker__option"
                    data-active={index === activeIndex}
                    // Keeps focus in the input, which is what the combobox pattern requires:
                    // a mousedown that moved focus would close the list before the click.
                    onMouseDown={(event) => event.preventDefault()}
                    onMouseEnter={() => setActive(index)}
                    onClick={() => choose(concept)}
                  >
                    <span className="app-concept-picker__display">
                      {conceptLabel(concept, locale)}
                    </span>
                    <span className="app-concept-picker__code">{concept.code}</span>
                    {conceptHeading(concept, locale) && (
                      <span className="app-concept-picker__heading">
                        {conceptHeading(concept, locale)}
                      </span>
                    )}
                    <span className="app-concept-picker__why">
                      {concept.favourite_rank !== undefined && (
                        <span className="app-concept-picker__rank">
                          {/* A rank is a count in running text, so it follows the language:
                              "১ নম্বরে" in Bangla, not "1". `formatCount` is the house rule
                              — measurements and identifiers stay in ASCII digits, counts do
                              not, and a rank is not a measurement. */}
                          {t('rank', { rank: formatCount(concept.favourite_rank, locale) })}
                        </span>
                      )}
                      {/* A word, never a tint. The reason has to survive a photograph of this
                          screen and a reader who cannot use hue at all. */}
                      <Reason concept={concept} />
                    </span>
                  </li>
                ))}
              </ul>
            </>
          )}
        </div>
      )}
    </div>
  );
}

/** Why this row ranked where it did, or nothing on a favourite, which carries no tier. */
function Reason({ concept }: { concept: Concept }) {
  const t = useTranslations('terminology');
  const key = tierReason(concept);
  if (!key) return null;
  return <span className="app-concept-picker__reason">{t(key)}</span>;
}

/** Stable per row index, because `aria-activedescendant` is an id and not a position. */
function optionId(listboxId: string, index: number): string {
  return `${listboxId}-option-${index}`;
}
