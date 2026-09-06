'use client';

import { useQuery } from '@tanstack/react-query';
import Link from 'next/link';
import { useTranslations } from 'next-intl';
import { useState } from 'react';

import { AlertBanner, Button, Card, EmptyState, Skeleton } from '@dthcms/ui';

import { patientSubroutePath } from '@/lib/navigation';
import { usePermission } from '@/lib/use-permission';
import { useSessionStore } from '@/stores/session';

import { answerRole, isOpen, listMyCorrections, myCorrectionsKey } from '../api/corrections';

import { AnswerCorrection } from './AnswerCorrection';
import { CorrectionTrail } from './CorrectionTrail';

/**
 * What I am being asked to fix (CP62, §4.3).
 *
 * # Why the operator has a queue at all
 *
 * §4.3 routes a correction request to **whoever typed the value** — not to a supervisor and not
 * to a shared pool — because an operator who never learns they mistyped will mistype again, and
 * a workflow where a supervisor quietly fixes everything produces a clean record and an
 * operator who keeps making the same mistake. This screen is that routing seen from the other
 * end.
 *
 * # Why the open queue and the history are two lists and not one
 *
 * "What is waiting for me" and "what have I answered" are different questions asked by
 * different people at different moments, and a screen that merged them would show an operator
 * with two open requests a list of forty. Open is the default; the history is one press away
 * and says plainly that it includes answered requests.
 *
 * # Why nothing here is counted, ranked or scored
 *
 * There is no total on this screen, no "requests this week", no comparison with anybody. CP63
 * builds the quality tally and it is quality's screen, not the operator's inbox. The plan's own
 * risk note says a metric that feels punitive makes staff hide errors instead of correcting
 * them, and an inbox that opened with a running count of your mistakes is the fastest way to
 * teach somebody to stop looking at it.
 *
 * # Why the order is the server's
 *
 * Oldest first. A queue answered newest-first is a queue where the oldest request is never
 * answered, and nothing here re-sorts it — a second opinion about the order would disagree
 * quietly with the one the endpoint was built around.
 */
export function CorrectionQueue() {
  const t = useTranslations('corrections');

  const [includeAnswered, setIncludeAnswered] = useState(false);

  const viewerId = useSessionStore((state) => state.user?.id);
  const mayApprove = usePermission('corrections.approve');

  const queue = useQuery({
    queryKey: myCorrectionsKey(includeAnswered),
    queryFn: () => listMyCorrections(includeAnswered),
  });

  return (
    <section className="app-stack" data-testid="correction-queue">
      <div className="app-corrections__queue-actions">
        <Button
          variant={includeAnswered ? 'quiet' : 'secondary'}
          data-testid="queue-open"
          aria-pressed={!includeAnswered}
          onClick={() => setIncludeAnswered(false)}
        >
          {t('queue.openOnly')}
        </Button>
        <Button
          variant={includeAnswered ? 'secondary' : 'quiet'}
          data-testid="queue-all"
          aria-pressed={includeAnswered}
          onClick={() => setIncludeAnswered(true)}
        >
          {t('queue.all')}
        </Button>
      </div>

      {queue.isPending && <Skeleton height="12rem" />}

      {queue.isError && (
        // Critical, and worded so it cannot be read as reassurance. An unreadable queue and an
        // empty one look the same as an absence, and one of them means a colleague is waiting
        // for an answer nobody knows they asked for.
        <AlertBanner tone="critical" title={t('queue.unavailable')}>
          {t('queue.unavailableBody')}
        </AlertBanner>
      )}

      {queue.data !== undefined && queue.data.length === 0 && (
        <EmptyState title={includeAnswered ? t('queue.emptyAll') : t('queue.empty')}>
          {t('queue.emptyBody')}
        </EmptyState>
      )}

      {queue.data !== undefined && queue.data.length > 0 && (
        <ul className="app-corrections__queue">
          {queue.data.map((request) => {
            const role = answerRole(request, { viewerId, mayApprove });
            return (
              <li key={request.id} data-testid={`queue-row-${request.id}`}>
                <Card elevation="raised" as="article">
                  {/* The trail draws no answer form of its own here: two forms for one
                      request on one screen is two buttons that do the same thing and
                      disagree about which one is busy. */}
                  <CorrectionTrail request={request} allowAnswer={false} />

                  <p className="app-corrections__queue-link">
                    <Link href={patientSubroutePath(request.patient_id, 'values')}>
                      {t('queue.openChain')}
                    </Link>
                  </p>

                  {isOpen(request) && role !== 'nobody' && (
                    <AnswerCorrection request={request} role={role} />
                  )}
                </Card>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
