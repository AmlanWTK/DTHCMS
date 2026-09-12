'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { Button, EmptyState, Input, Select, Skeleton } from '@dthcms/ui';

import { formatCount, formatDate } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';

import {
  CATALOGUE_KEY,
  getCatalogue,
  listProducts,
  productsKey,
  type FormularyProduct,
  type ProductFilter,
} from '../api/formulary';

import { ImportPanel } from './ImportPanel';
import { PriceHistoryPanel } from './PriceHistoryPanel';
import { PriceStateBadge } from './PriceStateBadge';
import { ReviewPanel } from './ReviewPanel';
import { named, presentation, priceState } from './formularyText';

/**
 * The formulary, for the pharmacist who owns its prices (CP75, §10, §16.1).
 *
 * # Three surfaces, one screen, in the order the questions are asked
 *
 * The review panel answers *is this being maintained*, the list answers *what is in it and what
 * does it cost*, and the import panel answers *how do I change two hundred of them at once*. They
 * are one route because the first question is only ever asked in the ten seconds before the
 * second, and because the review's working list — **only the ones nobody has checked** — is a
 * filter on the same list rather than a separate report that could disagree with it.
 *
 * # What the list leads with
 *
 * The price and its state, together. Not the price alone: 250 of 250 prices here are published
 * MRP nobody at this clinic has looked at, and a column of plausible numbers with no state beside
 * them is a screen that reads as finished. Every row draws colour, an icon and a word for that
 * state — `PriceStateBadge`, and the three signals are the design system's rule rather than
 * decoration.
 *
 * # The medicine's identity stays in Latin script
 *
 * Trade name, strength, manufacturer and generic are `lang="en"` in both interfaces. Everything
 * around them — the class, the form, the unit, every header, every state, every empty state — is
 * the reader's language. A transliterated trade name is a different medicine at the pharmacy
 * counter, and inventing a second name for a drug is precisely what CP77's rules and CP78's
 * duplicate-therapy checks cannot survive.
 *
 * # The history opens in place
 *
 * Same reason the jobs console's detail does: coming back is one press rather than a browser
 * gesture, and nothing about the filter somebody had set has to be reconstructed from a URL.
 */
export function FormularyConsole() {
  const t = useTranslations('formulary');
  const locale = useLocale() as Locale;

  const [filter, setFilter] = useState<ProductFilter>({
    q: '',
    klass: '',
    activeOnly: false,
    unverifiedOnly: false,
  });
  const [openProduct, setOpenProduct] = useState<FormularyProduct | null>(null);
  const [showImport, setShowImport] = useState(false);

  const catalogue = useQuery({ queryKey: CATALOGUE_KEY, queryFn: getCatalogue });
  const products = useQuery({
    queryKey: productsKey(filter),
    queryFn: () => listProducts(filter),
  });

  const classOptions = [
    { value: '', label: t('filter.allClasses') },
    ...(catalogue.data?.classes ?? []).map((entry) => ({
      value: entry.code,
      label: named(entry.name_en, entry.name_bn, locale),
    })),
  ];

  return (
    <div className="app-stack formulary">
      <ReviewPanel />

      <section className="app-card" aria-label={t('list.title')}>
        <header className="app-card__heading">
          <h2 className="app-card__title">{t('list.title')}</h2>
          <Button variant="quiet" onClick={() => setShowImport((open) => !open)}>
            {showImport ? t('import.hide') : t('import.show')}
          </Button>
        </header>

        {showImport ? <ImportPanel /> : null}

        <div className="formulary-filters">
          <Input
            label={t('filter.search')}
            description={t('filter.searchHelp')}
            value={filter.q}
            onChange={(event) => setFilter((current) => ({ ...current, q: event.target.value }))}
          />
          <Select
            label={t('filter.klass')}
            options={classOptions}
            value={filter.klass}
            onChange={(event) =>
              setFilter((current) => ({ ...current, klass: event.target.value }))
            }
          />
          <label className="formulary-priceform__check">
            <input
              type="checkbox"
              checked={filter.unverifiedOnly}
              onChange={(event) =>
                setFilter((current) => ({ ...current, unverifiedOnly: event.target.checked }))
              }
            />
            <span>{t('filter.unverified')}</span>
          </label>
          <label className="formulary-priceform__check">
            <input
              type="checkbox"
              checked={filter.activeOnly}
              onChange={(event) =>
                setFilter((current) => ({ ...current, activeOnly: event.target.checked }))
              }
            />
            <span>{t('filter.active')}</span>
          </label>
        </div>

        {products.isPending ? (
          <Skeleton />
        ) : products.data && products.data.items.length > 0 ? (
          <>
            <p className="formulary-count">
              {t('list.showing', {
                shown: formatCount(products.data.items.length, locale),
                total: formatCount(products.data.total, locale),
              })}
            </p>
            <div className="app-table-wrap">
              <table className="app-table formulary-table">
                <thead>
                  <tr>
                    <th scope="col">{t('list.medicine')}</th>
                    <th scope="col">{t('list.klass')}</th>
                    <th scope="col">{t('list.price')}</th>
                    <th scope="col">{t('list.state')}</th>
                    <th scope="col">{t('list.since')}</th>
                    <th scope="col">
                      <span className="dthc-visually-hidden">{t('list.actions')}</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {products.data.items.map((product) => {
                    const state = priceState(product.price);
                    return (
                      <tr key={product.id} data-withdrawn={product.is_active ? undefined : 'true'}>
                        <th scope="row" className="app-table__primary">
                          {/* Latin script in both interfaces — see the note above. */}
                          <span lang="en">{product.trade_name}</span>
                          <span className="app-table__secondary">
                            {presentation(product, locale)}
                          </span>
                          <span className="app-table__secondary" lang="en">
                            {product.generic_name} · {product.manufacturer}
                          </span>
                          {!product.is_active ? (
                            <span className="formulary-withdrawn">{t('list.withdrawn')}</span>
                          ) : null}
                        </th>
                        <td>{named(product.class_name_en, product.class_name_bn, locale)}</td>
                        <td className="app-table__primary">
                          {product.price ? (
                            t('currency', { amount: product.price.amount_bdt })
                          ) : (
                            // Never a zero. A medicine nobody has priced is a real state and
                            // the cell says it in words.
                            <span className="formulary-nobody">{t('list.unpriced')}</span>
                          )}
                        </td>
                        <td>
                          <PriceStateBadge state={state} />
                        </td>
                        <td>
                          {product.price
                            ? formatDate(
                                new Date(`${product.price.effective_from}T00:00:00Z`),
                                locale,
                              )
                            : '—'}
                        </td>
                        <td className="app-table__actions">
                          <Button variant="quiet" onClick={() => setOpenProduct(product)}>
                            {t('list.open')}
                          </Button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </>
        ) : (
          <EmptyState icon="pill" title={t('list.empty')}>
            {filter.unverifiedOnly ? t('list.emptyUnverified') : t('list.emptyBody')}
          </EmptyState>
        )}
      </section>

      {openProduct ? (
        <PriceHistoryPanel product={openProduct} onClose={() => setOpenProduct(null)} />
      ) : null}
    </div>
  );
}
