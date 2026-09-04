'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { AlertBanner, Button, EmptyState, Skeleton } from '@dthcms/ui';

import { usePermission } from '@/lib/use-permission';
import type { Locale } from '@/lib/i18n/config';

import {
  HISTORY_KINDS_KEY,
  groupByKind,
  historyItemsKey,
  listHistoryKinds,
  listMedicalHistory,
  unconfirmedItems,
  type HistoryItem,
} from '../api/history';

import { AddHistoryItem } from './AddHistoryItem';
import { HistoryItemCard } from './HistoryItemCard';
import { kindLabel } from './historyText';

/**
 * Station 4's screen: everything the patient brought with them (CP53, §4.7).
 *
 * # Carry-forward is a question, not a default
 *
 * This is acceptance criterion 3 and it is the whole reason the screen is shaped like this.
 * A returning patient's history arrives with `confirmed_at` absent on every item, which
 * means nobody has said any of it is still true since the day it was written down. The
 * screen says so twice — once as a count at the top, once as a word on each row — and then
 * offers one button per item.
 *
 * There is **no confirm-all**, and its absence is deliberate rather than unfinished. One
 * click producing twenty assertions is auto-acceptance with a person's name attached to it:
 * the record would say a clinician asserted the patient is still on a drug they stopped in
 * March, and nobody could say who claimed it, because nobody did. The contract offers no
 * batch endpoint for the same reason, and a loop over this one behind a single button would
 * defeat that by hand. If twenty items are carried forward, twenty questions were asked.
 *
 * # Why the kinds and their rules come from the server
 *
 * The groups, their order and what each one asks for are `/v1/history/kinds`' answer, not a
 * table in this file. A screen that remembered which kinds need a relation would eventually
 * ask for one on a complaint, and a seventh kind added on the server would be invisible
 * here until somebody noticed.
 *
 * # Why an empty kind is still a heading
 *
 * "No family history recorded" and "family history not asked yet" are the same blank space
 * on a screen that hides empty groups, and only one of them is a finished history. Every
 * kind gets its heading so the desk can see what it has not done.
 *
 * # Why nothing is said with colour alone
 *
 * Unconfirmed is a word on the row and a count in a banner before it is a tint. Roughly one
 * man in twelve cannot use hue; a tablet held near a window flattens it for everybody; and
 * the screen is read at the end of a day by somebody deciding whether to ask again.
 */

/** The rules are reference data; re-reading them every visit would be a round trip for nothing. */
const KINDS_STALE_MS = 60 * 60 * 1000;

export interface MedicalHistoryProps {
  patientId: string;
  /** The visit this is being taken at, when there is one. Travels on every write. */
  visitId?: string;
}

export function MedicalHistory({ patientId, visitId }: MedicalHistoryProps) {
  const t = useTranslations('history');
  const locale = useLocale() as Locale;

  const mayWrite = usePermission('history.write');
  const mayConfirm = usePermission('history.confirm');

  const [adding, setAdding] = useState(false);

  const reference = useQuery({
    queryKey: HISTORY_KINDS_KEY,
    queryFn: listHistoryKinds,
    staleTime: KINDS_STALE_MS,
  });

  const items = useQuery({
    queryKey: historyItemsKey(patientId),
    queryFn: () => listMedicalHistory(patientId),
  });

  if (reference.isPending || items.isPending) return <Skeleton height="16rem" />;

  if (reference.isError || items.isError || !reference.data || !items.data) {
    // Critical rather than quiet. An empty history and an unreadable one look identical on
    // screen and mean opposite things, and one of them reads as "this patient takes nothing".
    return (
      <AlertBanner tone="critical" title={t('unavailable')}>
        {t('unavailableBody')}
      </AlertBanner>
    );
  }

  const { kinds, relations, from_lifestyle_station: lifestyle } = reference.data;
  const groups = groupByKind(items.data, kinds);
  const waiting = unconfirmedItems(items.data);

  return (
    <section className="app-history" aria-label={t('title')} data-testid="medical-history">
      {waiting.length > 0 && (
        // The count, before any row is read. `borderline` and not `critical`: nothing here
        // interrupts a screen reader mid-sentence, because an unconfirmed history is work
        // outstanding rather than a patient in danger.
        <AlertBanner tone="borderline" title={t('unconfirmed.title', { count: waiting.length })}>
          {t('unconfirmed.body')}
        </AlertBanner>
      )}

      {lifestyle.length > 0 && <LifestyleNote codes={lifestyle} />}

      {mayWrite &&
        (adding ? (
          <AddHistoryItem
            patientId={patientId}
            kinds={kinds}
            relations={relations}
            visitId={visitId}
            onRecorded={() => setAdding(false)}
            onCancel={() => setAdding(false)}
          />
        ) : (
          <div className="app-history__add">
            <Button variant="primary" onClick={() => setAdding(true)}>
              {t('add')}
            </Button>
          </div>
        ))}

      {items.data.length === 0 && (
        <EmptyState icon="inbox" title={t('empty.title')}>
          {t('empty.body')}
        </EmptyState>
      )}

      {groups.map(({ kind, items: rows }) => (
        <section
          key={kind.kind}
          className="app-history__group"
          aria-label={kindLabel(kind, locale)}
          data-kind={kind.kind}
        >
          <h3 className="app-history__group-title">{kindLabel(kind, locale)}</h3>

          {rows.length === 0 ? (
            <p className="app-history__none">{t('noneRecorded')}</p>
          ) : (
            <ul className="app-history__list">
              {rows.map((item: HistoryItem) => (
                <li key={item.id}>
                  <HistoryItemCard
                    item={item}
                    kind={kind}
                    relations={relations}
                    patientId={patientId}
                    visitId={visitId}
                    mayWrite={mayWrite}
                    mayConfirm={mayConfirm}
                  />
                </li>
              ))}
            </ul>
          )}
        </section>
      ))}
    </section>
  );
}

/**
 * What this screen deliberately does not ask.
 *
 * Smoking and alcohol are lifestyle observations recorded at station 6, and the plan asks
 * that they be carried forward "without duplicate entry". Simply omitting them would look
 * like an oversight to the officer taking the history — and the officer who thinks a field
 * is missing is the officer who writes it into the complaint box. So the codes are named,
 * with the reason beside them.
 */
function LifestyleNote({ codes }: { codes: readonly string[] }) {
  const t = useTranslations('history');

  return (
    <div className="app-history__lifestyle" data-testid="lifestyle-note">
      <p className="app-history__lifestyle-title">{t('lifestyle.title')}</p>
      <p className="app-history__lifestyle-body">{t('lifestyle.body')}</p>
      {/* The observation codes themselves, in ASCII, because they are identifiers somebody
          may quote to whoever configures the lifestyle station. */}
      <p className="app-history__lifestyle-codes">{codes.join(', ')}</p>
    </div>
  );
}
