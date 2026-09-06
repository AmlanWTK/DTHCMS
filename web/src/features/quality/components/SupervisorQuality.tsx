'use client';

import { useQuery } from '@tanstack/react-query';
import { useTranslations } from 'next-intl';
import { useState } from 'react';

import { AlertBanner, Button, EmptyState, Skeleton } from '@dthcms/ui';

import { usePermission } from '@/lib/use-permission';

import {
  allThresholdsAreProposals,
  flagsKey,
  getOperatorRecord,
  listFlags,
  operatorRecordKey,
  DEFAULT_WINDOW_DAYS,
  windowIsAcceptable,
} from '../api/quality';

import { OperatorQualityList } from './OperatorQualityList';
import { QualityFlagCard } from './QualityFlagCard';
import { QualityRecordView } from './QualityRecordView';
import { WindowPicker } from './WindowPicker';

/**
 * The supervisor's surface: the patterns worth a conversation, and whose they are (CP63).
 *
 * # Who reads this, and why it is not HR's
 *
 * `quality.read.team`, held by the chief consultant, QA and the administrator —
 * deliberately **not** `hr.performance.read`, which already existed and which HR holds
 * (ADR-0029 §2). The plan puts performance-linked pay and discipline out of scope, and a
 * permission that hands an operator's correction history to the department that sets pay puts
 * it back in whatever anybody intends by it. There is also a plainer reason: reading these
 * numbers usefully means knowing that a weight comes off a scale somebody else calibrates,
 * that the same code going wrong three times is usually the instrument, and that corrections
 * after four in the afternoon are a rota question. That is a clinical supervisor's knowledge,
 * and the same numbers handed to somebody without it produce confident conclusions about the
 * wrong thing.
 *
 * # The order of the two halves is the argument
 *
 * The flags come first and the operator list second, and that is not layout. A flag names one
 * pattern and carries the suggested first question — *check the instrument before concluding
 * anything about the operator*; *this is usually a rota problem rather than a person problem*.
 * A supervisor who reads those first arrives at the list already holding the right frame. A
 * supervisor who reads the list first arrives at the flags having already decided who the
 * problem is.
 *
 * # Answering is a separate permission from reading
 *
 * `quality.flag.resolve`. Where the reader does not hold it the answer controls are simply not
 * drawn and nothing is said about them: a control that exists in order to be refused teaches
 * people that the software is unreliable. Whether QA should hold both is Dr. Nahid's call and
 * both are seeded; this screen reads whichever answer the server gives.
 *
 * # One person's record opens in place
 *
 * Not a separate route, because the window the supervisor chose has to travel with them — a
 * record read over a different period from the list it was opened from is two screens quietly
 * disagreeing — and because coming back to the list is one press rather than a browser
 * gesture on a tablet.
 *
 * # Why this screen polls nothing and refreshes on nothing
 *
 * A raised flag is published on the topic of the person it is *about*, and there is deliberately
 * no supervisor topic: "who supervises whom" is a presence question this module has no business
 * answering. So this queue is read when it is opened, and two supervisors working it at once
 * find out about each other through the `409` on the second answer — which reads as *a colleague
 * got there first* rather than as a failure, and is the honest thing for it to say.
 *
 * # The window means the same thing in all three sections
 *
 * It scopes what happened in the period — raised or answered — and never hides an open flag,
 * because "what is still waiting" is not a question about a date range. So the queue, the
 * roster's flag counts and the record a supervisor opens next all agree at any `days`, and
 * nothing on this screen has to explain why they might not.
 */
export function SupervisorQuality() {
  const t = useTranslations('quality');

  const [days, setDays] = useState(DEFAULT_WINDOW_DAYS);
  const [includeAnswered, setIncludeAnswered] = useState(false);
  const [openOperator, setOpenOperator] = useState<string | null>(null);

  const mayAnswer = usePermission('quality.flags.resolve');

  const flags = useQuery({
    queryKey: flagsKey(days, includeAnswered),
    // The same window as the list and the records below it. The control at the top of this
    // screen used to scope two of its three sections and leave this one unbounded, which is
    // three answers to "which month am I looking at" on one screen.
    queryFn: () => listFlags(days, { includeAnswered }),
  });

  const record = useQuery({
    queryKey: operatorRecordKey(openOperator ?? '', days),
    queryFn: () => getOperatorRecord(openOperator ?? '', days),
    enabled: openOperator !== null,
  });

  return (
    <div className="app-stack" data-testid="supervisor-quality">
      <p className="app-page__description">{t('team.lead')}</p>

      <WindowPicker
        days={days}
        onChange={(next) => {
          if (windowIsAcceptable(next)) setDays(next);
        }}
        busy={record.isFetching || flags.isFetching}
      />

      <section className="app-stack" data-testid="flag-queue">
        <h2 className="app-quality__section">{t('flags.queueTitle')}</h2>

        <div className="app-quality__order" role="group" aria-label={t('flags.showLabel')}>
          <Button
            variant={includeAnswered ? 'quiet' : 'secondary'}
            aria-pressed={!includeAnswered}
            data-testid="flags-open-only"
            onClick={() => setIncludeAnswered(false)}
          >
            {t('flags.openOnly')}
          </Button>
          <Button
            variant={includeAnswered ? 'secondary' : 'quiet'}
            aria-pressed={includeAnswered}
            data-testid="flags-all"
            onClick={() => setIncludeAnswered(true)}
          >
            {t('flags.all')}
          </Button>
        </div>

        {flags.isPending && <Skeleton height="10rem" />}

        {flags.isError && (
          // Critical and worded so it cannot be read as reassurance. An unreadable queue and
          // an empty one look identical, and one of them means a pattern is sitting on a
          // colleague's record that nobody has been asked to look at.
          <AlertBanner tone="critical" title={t('flags.unavailable')}>
            {t('flags.unavailableBody')}
          </AlertBanner>
        )}

        {flags.data !== undefined && allThresholdsAreProposals(flags.data) && (
          // Once at the top as well as on every card. A supervisor about to work through a
          // queue should know before the first row that none of these numbers has been signed
          // off by a clinician.
          <AlertBanner tone="borderline" title={t('flags.allProposalsTitle')}>
            {t('flags.allProposalsBody')}
          </AlertBanner>
        )}

        {flags.data !== undefined && flags.data.length === 0 && (
          <EmptyState icon="check" title={t('flags.queueEmptyTitle')}>
            {includeAnswered ? t('flags.queueEmptyAllBody') : t('flags.queueEmptyBody')}
          </EmptyState>
        )}

        {flags.data !== undefined && flags.data.length > 0 && (
          <ul className="app-quality__flags">
            {flags.data.map((flag) => (
              <li key={flag.id}>
                <QualityFlagCard flag={flag} showOperator mayAnswer={mayAnswer} />
              </li>
            ))}
          </ul>
        )}
      </section>

      {openOperator === null ? (
        <OperatorQualityList days={days} onOpen={setOpenOperator} />
      ) : (
        <section className="app-stack" data-testid="operator-record">
          <Button variant="quiet" data-testid="back-to-list" onClick={() => setOpenOperator(null)}>
            {t('operators.back')}
          </Button>

          {record.isPending && <Skeleton height="14rem" />}

          {record.isError && (
            <AlertBanner tone="critical" title={t('operators.recordUnavailable')}>
              {t('operators.recordUnavailableBody')}
            </AlertBanner>
          )}

          {record.data !== undefined && (
            <QualityRecordView
              record={record.data.record}
              mine={record.data.mine}
              mayAnswerFlags={mayAnswer}
            />
          )}
        </section>
      )}
    </div>
  );
}
