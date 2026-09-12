'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';

import { Icon } from '@dthcms/ui';

import type { Locale } from '@/lib/i18n/config';

import {
  searchFormulary,
  searchKey,
  type FormularySearchEntry,
  type FormularySearchStrength,
} from '../api/formulary';
import { named } from './formularyText';

/**
 * The two-letter prescribing autocomplete (CP76, §10.1).
 *
 * # Why a result is a brand and a strength is chosen afterwards
 *
 * Levothyroxine is stocked here as Thyrox in six strengths and Thyrin in four. A list of products
 * would answer "th" with ten rows of levothyroxine before it reached anything else, and the
 * checkpoint's own manual verification — *the intended drug is in the top three* — would be
 * unreachable for any brand whose molecule has more than three strengths.
 *
 * So each row is a brand, and its strengths are chips on the row. The physician chooses the
 * medicine and then the strength, which is the order he chooses them in anyway.
 *
 * **This is the UI decision I am least sure of**, and it is Dr. Nahid's rather than mine. The
 * alternative — one row per strength, no second keystroke — is faster for a drug stocked in one
 * strength and unusable for levothyroxine. It is written down in docs/medication-rules.md.
 *
 * # Keyboard first, and what that means precisely
 *
 * ↓ and ↑ move between brands. ← and → move between the strengths of the focused brand. Enter
 * takes the focused strength. Escape closes without choosing. Tab is left alone, because a
 * physician who has decided to leave the field should leave it.
 *
 * Nothing here needs a mouse, and nothing needs a modifier. A combobox whose fast path is
 * Ctrl-something is one people use with the mouse.
 *
 * # Why it does not debounce very much
 *
 * 120ms, which is short. The server answers from an in-process copy of the formulary and the
 * measured p99 is well under two milliseconds on loopback; the debounce is there to stop four
 * requests going out while somebody types "metf", not because the request is expensive. A long
 * debounce on a fast endpoint is latency somebody added on purpose.
 *
 * # What it says about the ranking
 *
 * When the server reports `ranking_complete: false` — which it does until CP80 gives it the
 * physician's own prescribing history — the list footer says so. An interface that presented an
 * alphabetical list as a personalised one would be making a claim the system cannot keep, and the
 * day the claim becomes true nobody would notice the difference.
 */
export interface MedicineChoice {
  productId: string;
  tradeName: string;
  genericName: string;
  strength: string;
  form: string;
  dispenseUnit: string;
  priceBdt?: string;
  priceVerification?: string;
}

export interface MedicineComboboxProps {
  onChoose?: (choice: MedicineChoice) => void;
  /** Placeholder override, for a caller with a narrower purpose. */
  placeholderKey?: string;
  autoFocus?: boolean;
}

const DEBOUNCE_MS = 120;

export function MedicineCombobox({ onChoose, autoFocus = false }: MedicineComboboxProps) {
  const t = useTranslations('formulary');
  const locale = useLocale() as Locale;

  const [typed, setTyped] = useState('');
  const [query, setQuery] = useState('');
  const [open, setOpen] = useState(false);
  const [brandIndex, setBrandIndex] = useState(0);
  const [strengthIndex, setStrengthIndex] = useState(0);
  const [chosen, setChosen] = useState<MedicineChoice | null>(null);

  const listId = useId();
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const timer = setTimeout(() => setQuery(typed.trim()), DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [typed]);

  const search = useQuery({
    queryKey: searchKey(query, 8),
    queryFn: () => searchFormulary(query, 8),
    // One character is a legal search and returns a lot; nothing is fetched for none.
    enabled: query.length > 0,
    // The server's own cache refreshes on a watermark every ten seconds. Holding a result here
    // for longer than that would mean a price the pharmacist corrected this morning staying on
    // the physician's screen until he reloaded the page.
    staleTime: 10_000,
  });

  const entries: FormularySearchEntry[] = useMemo(() => search.data?.entries ?? [], [search.data]);

  useEffect(() => {
    setBrandIndex(0);
    setStrengthIndex(0);
  }, [query]);

  const choose = useCallback(
    (entry: FormularySearchEntry, strength: FormularySearchStrength) => {
      const choice: MedicineChoice = {
        productId: strength.product_id,
        tradeName: entry.trade_name,
        genericName: entry.generic_name,
        strength: strength.strength,
        form: named(entry.form_name_en, entry.form_name_bn, locale),
        dispenseUnit: named(strength.unit_name_en, strength.unit_name_bn, locale),
        priceBdt: strength.price?.amount_bdt,
        priceVerification: strength.price?.verification,
      };
      setChosen(choice);
      setOpen(false);
      onChoose?.(choice);
    },
    [locale, onChoose],
  );

  function onKeyDown(event: React.KeyboardEvent<HTMLInputElement>) {
    if (!open || entries.length === 0) {
      if (event.key === 'ArrowDown' && entries.length > 0) {
        setOpen(true);
        event.preventDefault();
      }
      return;
    }
    const entry = entries[brandIndex];
    if (!entry) return;
    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault();
        setBrandIndex((i) => (i + 1) % entries.length);
        setStrengthIndex(0);
        break;
      case 'ArrowUp':
        event.preventDefault();
        setBrandIndex((i) => (i - 1 + entries.length) % entries.length);
        setStrengthIndex(0);
        break;
      case 'ArrowRight':
        event.preventDefault();
        setStrengthIndex((i) => (i + 1) % entry.strengths.length);
        break;
      case 'ArrowLeft':
        event.preventDefault();
        setStrengthIndex((i) => (i - 1 + entry.strengths.length) % entry.strengths.length);
        break;
      case 'Enter':
        event.preventDefault();
        {
          const strength = entry.strengths[strengthIndex] ?? entry.strengths[0];
          if (strength) choose(entry, strength);
        }
        break;
      case 'Escape':
        event.preventDefault();
        setOpen(false);
        break;
      default:
        break;
    }
  }

  const showList = open && query.length > 0;

  return (
    <div className="combobox">
      <div className="combobox-field">
        <Icon name="pill" size={18} />
        <input
          ref={inputRef}
          className="combobox-input"
          type="text"
          role="combobox"
          aria-expanded={showList}
          aria-controls={listId}
          aria-autocomplete="list"
          autoFocus={autoFocus}
          autoComplete="off"
          spellCheck={false}
          placeholder={t('search.placeholder')}
          value={typed}
          onChange={(e) => {
            setTyped(e.target.value);
            setOpen(true);
          }}
          onFocus={() => setOpen(true)}
          onKeyDown={onKeyDown}
        />
        {typed !== '' ? (
          <button
            type="button"
            className="combobox-clear"
            aria-label={t('search.clear')}
            onClick={() => {
              setTyped('');
              setQuery('');
              setChosen(null);
              inputRef.current?.focus();
            }}
          >
            <Icon name="x" size={16} />
          </button>
        ) : null}
      </div>

      {showList ? (
        <div className="combobox-list" id={listId} role="listbox">
          {search.isPending ? <p className="combobox-note">{t('search.searching')}</p> : null}

          {!search.isPending && entries.length === 0 ? (
            <p className="combobox-note">{t('search.none', { query: typed })}</p>
          ) : null}

          {entries.map((entry, i) => (
            <BrandRow
              key={`${entry.trade_name}:${entry.form_code}`}
              entry={entry}
              locale={locale}
              focused={i === brandIndex}
              strengthIndex={i === brandIndex ? strengthIndex : -1}
              onPickStrength={(s) => choose(entry, s)}
              onHover={() => {
                setBrandIndex(i);
                setStrengthIndex(0);
              }}
            />
          ))}

          {search.data ? (
            <p className="combobox-footer">
              {t('search.total', { shown: entries.length, total: search.data.total })}
              {search.data.normalised_query !== typed.trim().toLowerCase() ? (
                <span> · {t('search.normalised', { as: search.data.normalised_query })}</span>
              ) : null}
              {search.data.ranking_complete ? null : (
                <span className="combobox-caveat"> · {t('search.rankingIncomplete')}</span>
              )}
            </p>
          ) : null}
        </div>
      ) : null}

      {chosen ? (
        <p className="combobox-chosen" data-testid="combobox-chosen">
          <Icon name="check" size={14} />
          <strong>
            {chosen.tradeName} {chosen.strength}
          </strong>
          <span>
            {chosen.genericName} · {chosen.form}
          </span>
          {chosen.priceBdt ? (
            <span>
              {t('currency', { amount: chosen.priceBdt })}
              {chosen.priceVerification === 'PROVISIONAL' ? ` · ${t('state.unchecked')}` : ''}
            </span>
          ) : (
            <span>{t('state.none')}</span>
          )}
        </p>
      ) : null}
    </div>
  );
}

function BrandRow({
  entry,
  locale,
  focused,
  strengthIndex,
  onPickStrength,
  onHover,
}: {
  entry: FormularySearchEntry;
  locale: Locale;
  focused: boolean;
  strengthIndex: number;
  onPickStrength: (s: FormularySearchStrength) => void;
  onHover: () => void;
}) {
  const t = useTranslations('formulary');
  return (
    <div
      className="combobox-row"
      role="option"
      aria-selected={focused}
      data-focused={focused ? 'true' : undefined}
      onMouseEnter={onHover}
    >
      <div className="combobox-row-head">
        <strong className="combobox-brand">{entry.trade_name}</strong>
        <span className="combobox-generic">{entry.generic_name}</span>
        <span className="combobox-form">
          {named(entry.form_name_en, entry.form_name_bn, locale)}
        </span>
        <span className="combobox-class">
          {named(entry.class_name_en, entry.class_name_bn, locale)}
        </span>
      </div>
      <div className="combobox-strengths">
        {entry.strengths.map((s, i) => (
          <button
            key={s.product_id}
            type="button"
            className="combobox-strength"
            data-focused={focused && i === strengthIndex ? 'true' : undefined}
            onClick={() => onPickStrength(s)}
          >
            {/* The strength and the unit stay in Latin script and ASCII digits in both
                interfaces, per the design system: a dose that changes shape with the language is
                a dose somebody transcribes wrongly onto a paper chart. */}
            <span className="combobox-strength-value">{s.strength}</span>
            <span className="combobox-strength-price">
              {s.price ? (
                <>
                  {t('currency', { amount: s.price.amount_bdt })}
                  {s.price.verification === 'PROVISIONAL' ? (
                    <Icon name="alert-triangle" size={12} />
                  ) : null}
                </>
              ) : (
                t('state.none')
              )}
            </span>
          </button>
        ))}
      </div>
    </div>
  );
}
