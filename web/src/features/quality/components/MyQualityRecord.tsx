'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { ApiError, fieldMessages } from '@dthcms/api-client';
import { AlertBanner, Skeleton } from '@dthcms/ui';

import type { Locale } from '@/lib/i18n/config';

import { DEFAULT_WINDOW_DAYS, getMyRecord, myRecordKey, windowIsAcceptable } from '../api/quality';

import { QualityRecordView } from './QualityRecordView';
import { ThresholdList } from './ThresholdList';
import { WindowPicker } from './WindowPicker';

/**
 * My own correction record (CP63, §4.3, ADR-0029 §3).
 *
 * # This is the screen the checkpoint stands or falls on
 *
 * The plan states the risk in its own words: *"a metric that feels punitive damages data
 * honesty — staff hide errors instead of correcting them."* Every other surface in this
 * feature is read by somebody looking at a colleague. This one is read by the person the
 * numbers are about, and what they conclude from it in the first ten seconds decides whether
 * the whole mechanism produces honest data or careful data.
 *
 * So the target is that it reads as **here is your month**, and never as a report card.
 * Concretely, that meant four decisions:
 *
 * **No permission stands in front of it.** The endpoint requires a session and nothing else,
 * and this screen asks for nothing either. An operator who has to be granted something before
 * they may see their own correction count is an operator who will assume the count is being
 * kept from them — and CP62 had already made that mistake once, with the field worker who
 * could not see the correction addressed to them.
 *
 * **The rules are on the same screen as the numbers.** `ThresholdList` is below the record,
 * not on a help page, because "what would raise a flag about me" is the second question
 * anybody has and the first one they will not ask out loud.
 *
 * **Nothing is compared with anybody.** There is no clinic average here, no position, no
 * trend arrow, and no colour that means bad. A comparison is what turns a record into a
 * ranking, and it can be added by accident in a single line of JSX.
 *
 * **A flag reaches the operator here, not from their supervisor.** The gateway publishes
 * `quality.flag_raised` on this person's own topic, invalidating `queryKeys.quality()`, so a
 * pattern raised while they are on this screen appears on it. A flag somebody first learns
 * about from their supervisor is a flag that felt like an ambush, and an ambush is what makes
 * people stop correcting things.
 *
 * # Why the failure state is worded the way it is
 *
 * An unreadable record and an empty one look identical, and the second is good news. So a
 * failure says so plainly: this screen could not read it, rather than there is nothing here.
 * Silence on this screen, of all screens, would be read as something being withheld.
 */
export function MyQualityRecord() {
  const t = useTranslations('quality');
  const locale = useLocale() as Locale;

  const [days, setDays] = useState(DEFAULT_WINDOW_DAYS);

  const record = useQuery({
    queryKey: myRecordKey(days),
    queryFn: () => getMyRecord(days),
  });

  /*
   * A window the server refuses. The picker cannot produce one, so this is not a form error to
   * correct — it is the client and the server disagreeing about what a window may be, which is
   * worth saying in the server's own words rather than behind "could not be read". Its message
   * arrives in both languages against the `days` field.
   */
  const refusedWindow =
    record.error instanceof ApiError ? fieldMessages(record.error, locale).days : undefined;

  return (
    <div className="app-stack" data-testid="my-quality-record">
      <p className="app-page__description">{t('mine.lead')}</p>

      <WindowPicker
        days={days}
        // Guarded here as well as in the picker, so that a window the server would refuse
        // cannot reach the query key either — a cache entry under an impossible window is a
        // refusal that repeats itself on every render.
        onChange={(next) => {
          if (windowIsAcceptable(next)) setDays(next);
        }}
        busy={record.isFetching}
      />

      {record.isPending && <Skeleton height="14rem" />}

      {record.isError && (
        <AlertBanner
          tone="critical"
          title={refusedWindow ?? t('mine.unavailable')}
          data-testid="record-unavailable"
        >
          {refusedWindow === undefined ? t('mine.unavailableBody') : t('window.refused')}
        </AlertBanner>
      )}

      {record.data !== undefined && <QualityRecordView record={record.data} mine />}

      <ThresholdList />
    </div>
  );
}
