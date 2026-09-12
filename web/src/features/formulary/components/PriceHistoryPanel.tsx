'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { AlertBanner, Button, EmptyState, Input, Skeleton } from '@dthcms/ui';

import { ApiError } from '@/lib/api';
import { formatDate } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';
import { usePermission } from '@/lib/use-permission';

import {
  FORMULARY_KEY,
  REVIEW_KEY,
  getPriceHistory,
  getPriceOn,
  pricesKey,
  recordPrice,
  type FormularyProduct,
} from '../api/formulary';

import { presentation, priceState, recordedBy } from './formularyText';
import { PriceStateBadge } from './PriceStateBadge';

/**
 * Everything one medicine has ever cost, and the form that changes it (CP75, §12.3).
 *
 * # Why the history is the screen rather than a detail behind one
 *
 * The affordability research this whole module exists for is a question about the past, and the
 * person most likely to answer it wrongly is the one looking at a single current price. Putting
 * the history on the same panel as the price form makes the shape of the data visible at the
 * moment somebody changes it: they can see that the old row does not disappear, and that the new
 * one starts on a day.
 *
 * # The as-of box
 *
 * A date field that asks the server *what did this cost on that day* and shows the answer. It is
 * the checkpoint's first acceptance criterion, made operable — and it exists on the screen rather
 * than only in the API because the person who needs to check a past cost against a receipt is the
 * pharmacist, not a developer with curl.
 *
 * It answers "nothing" for a day before the history begins, in words. Not a zero, and not a
 * blank: a medicine the clinic had not priced yet is a real state and is what the row should say.
 *
 * # What the form will not let somebody do
 *
 * Edit a past price. There is no control for it, because there is no endpoint for it and no
 * database grant for it. Correcting a price is recording a new one from a date, and if the date
 * needs to be in the past the server decides whether that is allowed — a period already covered
 * is a `409` with a sentence, not a silent overwrite.
 */
export function PriceHistoryPanel({
  product,
  onClose,
}: {
  product: FormularyProduct;
  onClose: () => void;
}) {
  const t = useTranslations('formulary');
  const locale = useLocale() as Locale;
  const queryClient = useQueryClient();
  const mayWrite = usePermission('formulary.write');

  const [amount, setAmount] = useState('');
  const [from, setFrom] = useState('');
  const [confirmed, setConfirmed] = useState(true);
  const [note, setNote] = useState('');
  const [asOf, setAsOf] = useState('');

  const history = useQuery({
    queryKey: pricesKey(product.id),
    queryFn: () => getPriceHistory(product.id),
  });

  // Only asked once a date has been typed, and never on an empty box: an as-of query with no
  // date is a request for today's price, which is already on the row above.
  const lookup = useQuery({
    queryKey: [...pricesKey(product.id), 'asof', asOf],
    queryFn: () => getPriceOn(product.id, asOf),
    enabled: /^\d{4}-\d{2}-\d{2}$/.test(asOf),
  });

  // One place that turns a YYYY-MM-DD from the payload into the reader's date format.
  const day = (value: string) => formatDate(new Date(`${value}T00:00:00Z`), locale);

  const record = useMutation({
    mutationFn: () =>
      recordPrice({
        productId: product.id,
        amountBdt: amount.trim(),
        effectiveFrom: from.trim() || undefined,
        verified: confirmed,
        sourceNote: note.trim() || undefined,
      }),
    onSuccess: () => {
      setAmount('');
      setNote('');
      void queryClient.invalidateQueries({ queryKey: FORMULARY_KEY });
      void queryClient.invalidateQueries({ queryKey: REVIEW_KEY });
    },
  });

  const failure = record.error instanceof ApiError ? record.error : null;

  return (
    <section className="app-card formulary-panel" aria-label={t('history.title')}>
      <header className="app-card__heading formulary-panel__head">
        <div>
          <h2 className="app-card__title" lang="en">
            {product.trade_name}
          </h2>
          <p className="formulary-panel__sub">{presentation(product, locale)}</p>
          <p className="formulary-panel__sub" lang="en">
            {product.generic_name} · {product.manufacturer}
          </p>
        </div>
        <Button variant="quiet" onClick={onClose}>
          {t('history.close')}
        </Button>
      </header>

      {/* Criterion 1, operable. */}
      <div className="formulary-asof">
        {/* The label is the control's own, and **no `id` is passed**. `packages/ui`'s Input
            spreads the caller's props *after* the id the Field generated, so an explicit id
            replaces it on the input while the label's `htmlFor` keeps pointing at the
            generated one — a control that looks labelled and is not. Caught by the query in
            `formulary.test.tsx`; worth knowing before the next screen does it too. */}
        <Input
          type="date"
          label={t('asOf.label')}
          className="formulary-asof__field"
          value={asOf}
          onChange={(event) => setAsOf(event.target.value)}
        />
        <p className="formulary-asof__answer" aria-live="polite">
          {!/^\d{4}-\d{2}-\d{2}$/.test(asOf)
            ? t('asOf.hint')
            : lookup.isPending
              ? t('asOf.looking')
              : lookup.data
                ? t('asOf.answer', {
                    amount: t('currency', { amount: lookup.data.amount_bdt }),
                    day: formatDate(new Date(`${asOf}T00:00:00Z`), locale),
                  })
                : t('asOf.none', { day: formatDate(new Date(`${asOf}T00:00:00Z`), locale) })}
        </p>
      </div>

      {mayWrite && !product.withdrawn_at ? (
        <form
          className="formulary-priceform"
          onSubmit={(event) => {
            event.preventDefault();
            record.mutate();
          }}
        >
          <h3 className="formulary-priceform__title">{t('record.title')}</h3>
          <p className="formulary-priceform__note">{t('record.explain')}</p>
          <div className="formulary-priceform__row">
            <Input
              label={t('record.amount')}
              description={t('record.amountHelp')}
              inputMode="decimal"
              placeholder="12.50"
              value={amount}
              onChange={(event) => setAmount(event.target.value)}
            />
          </div>
          <div className="formulary-priceform__row">
            <Input
              label={t('record.from')}
              description={t('record.fromHelp')}
              type="date"
              value={from}
              onChange={(event) => setFrom(event.target.value)}
            />
          </div>
          <div className="formulary-priceform__row">
            <Input
              label={t('record.note')}
              value={note}
              onChange={(event) => setNote(event.target.value)}
              placeholder={t('record.notePlaceholder')}
            />
          </div>
          <label className="formulary-priceform__check">
            <input
              type="checkbox"
              checked={confirmed}
              onChange={(event) => setConfirmed(event.target.checked)}
            />
            <span>{t('record.confirm')}</span>
          </label>
          <p className="formulary-priceform__help">{t('record.confirmHelp')}</p>

          {failure ? (
            <AlertBanner tone="critical" title={t('record.refused')}>
              {/* The per-field message in the reader's own language, which is what the server
                  sends both halves of for exactly this moment. */}
              {locale === 'bn'
                ? (failure.fieldsBN.amount_bdt ??
                  failure.fieldsBN.effective_from ??
                  failure.messageBN)
                : (failure.fields.amount_bdt ?? failure.fields.effective_from ?? failure.messageEN)}
            </AlertBanner>
          ) : null}

          <Button type="submit" disabled={record.isPending || amount.trim() === ''}>
            {record.isPending ? t('record.saving') : t('record.save')}
          </Button>
        </form>
      ) : null}

      <h3 className="formulary-priceform__title">{t('history.title')}</h3>
      {history.isPending ? (
        <Skeleton />
      ) : history.data && history.data.length > 0 ? (
        <div className="app-table-wrap">
          <table className="app-table">
            <thead>
              <tr>
                <th scope="col">{t('history.period')}</th>
                <th scope="col">{t('history.price')}</th>
                <th scope="col">{t('history.state')}</th>
                <th scope="col">{t('history.who')}</th>
              </tr>
            </thead>
            <tbody>
              {history.data.map((price) => {
                const state = priceState(price);
                const who = recordedBy(price, locale);
                return (
                  <tr key={price.id}>
                    <td>
                      {price.effective_to
                        ? t('period.closed', {
                            from: day(price.effective_from),
                            to: day(price.effective_to),
                          })
                        : t('period.current', { from: day(price.effective_from) })}
                    </td>
                    <td className="app-table__primary">
                      {t('currency', { amount: price.amount_bdt })}
                    </td>
                    <td>
                      <PriceStateBadge state={state} />
                    </td>
                    <td>
                      {/* A seeded price names nobody, and the cell says so in words. A blank
                          here would read as "not loaded yet" rather than as "nobody has ever
                          taken responsibility for this number". */}
                      {who ?? <span className="formulary-nobody">{t('history.nobody')}</span>}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : (
        <EmptyState icon="pill" title={t('history.empty')}>
          {t('history.emptyBody')}
        </EmptyState>
      )}
    </section>
  );
}
