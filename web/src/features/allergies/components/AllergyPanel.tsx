'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { AlertBanner, Button, Card, EmptyState, Input, Skeleton } from '@dthcms/ui';

import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';
import { usePermission } from '@/lib/use-permission';

import {
  ALLERGY_REACTIONS_KEY,
  allergyChangesKey,
  allergyStateKey,
  getAllergyState,
  listAllergyReactions,
  reasonAcceptable,
  withdrawAllergyAssertion,
  type AllergyAssertion,
  type AssertionKind,
} from '../api/allergies';

import { AllergyCard } from './AllergyCard';
import { AllergyChanges } from './AllergyChanges';
import { AssertAllergyStatus } from './AssertAllergyStatus';
import { RecordAllergy } from './RecordAllergy';

/**
 * The history station's allergy surface (CP54, §3 step 4).
 *
 * # Three ways to satisfy the gate, and no fourth
 *
 * One or more allergies recorded; "no known allergies" asserted; or "unable to assess" with
 * its reason. Those are the three controls on this screen and there is nothing else. There
 * is no skip, no "proceed anyway", no button that clears the gate without one of the three
 * real answers — the gate is a trigger on the queue table and a client could not bypass it
 * anyway, but a control that *looked* like a way past would teach the floor that one
 * exists, and the plan names that risk by hand: operators asserting NKA reflexively to
 * clear the gate. `allergies.test.tsx` has a named test whose whole job is to fail if such
 * a control appears here.
 *
 * # Why an empty list is never drawn as "no allergies"
 *
 * `NONE_RECORDED` and `NO_KNOWN_ALLERGY` both have no allergies in them and they are
 * opposite facts. The empty state here says *nobody has asked yet*, in those words, and the
 * standing assertion — when there is one — is a card with a person's name and a time on it
 * rather than an absence. The header strip above this panel says the same thing again, from
 * the same `status` field.
 *
 * # Why the assertion can be taken back
 *
 * An officer who tapped "no known allergies" on the wrong patient has put a claim into a
 * record a prescriber will rely on. Withdrawing it takes a reason, keeps both halves, and
 * re-closes the gate unless allergies are recorded — which is why the withdrawal
 * invalidates the state the header reads rather than only this screen's copy.
 */

/** The vocabulary is reference data; re-reading it every visit would be a round trip for nothing. */
const REACTIONS_STALE_MS = 60 * 60 * 1000;

export interface AllergyPanelProps {
  patientId: string;
  /** The visit this is being taken at, when there is one. Travels on every write. */
  visitId?: string;
}

/** Which of the three answers is being given, if any. One at a time; this is not a queue. */
type Open = 'record' | AssertionKind | null;

export function AllergyPanel({ patientId, visitId }: AllergyPanelProps) {
  const t = useTranslations('allergies');

  const mayRead = usePermission('allergies.view');
  const mayWrite = usePermission('allergies.write');

  const [open, setOpen] = useState<Open>(null);

  const state = useQuery({
    queryKey: allergyStateKey(patientId),
    queryFn: () => getAllergyState(patientId),
    enabled: mayRead,
  });

  const reactions = useQuery({
    queryKey: ALLERGY_REACTIONS_KEY,
    queryFn: listAllergyReactions,
    staleTime: REACTIONS_STALE_MS,
    enabled: mayRead,
  });

  if (!mayRead) {
    return <AlertBanner tone="unknown" title={t('notPermitted')} />;
  }

  if (state.isPending) return <Skeleton height="16rem" />;

  if (state.isError || !state.data) {
    // Critical rather than quiet. An unreadable allergy state and a patient with no
    // allergies look identical on screen and mean opposite things, and one of them reads as
    // "nothing to worry about" to the person handing over the medicine.
    return (
      <AlertBanner tone="critical" title={t('unavailable')}>
        {t('unavailableBody')}
      </AlertBanner>
    );
  }

  const { status, allergies, assertion } = state.data;

  return (
    <section className="app-allergies" aria-label={t('title')} data-testid="allergy-panel">
      {allergies.length === 0 && status === 'NONE_RECORDED' && (
        // Not "no allergies". Nobody has asked, which is the opposite fact and the one this
        // whole checkpoint exists to keep visible.
        <EmptyState icon="inbox" title={t('empty.title')}>
          {t('empty.body')}
        </EmptyState>
      )}

      {allergies.length > 0 && (
        <ul className="app-allergies__list">
          {/* Worst first, as the server sorted them. Nothing here re-sorts. */}
          {allergies.map((allergy) => (
            <li key={allergy.id}>
              <AllergyCard allergy={allergy} patientId={patientId} mayWrite={mayWrite} />
            </li>
          ))}
        </ul>
      )}

      {assertion && (
        <StandingAssertion
          assertion={assertion}
          patientId={patientId}
          mayWrite={mayWrite}
          status={status}
        />
      )}

      {mayWrite && open === null && (
        <div className="app-allergies__actions" data-testid="allergy-actions">
          <Button variant="primary" onClick={() => setOpen('record')}>
            {t('actions.record')}
          </Button>
          <Button variant="secondary" onClick={() => setOpen('NO_KNOWN_ALLERGY')}>
            {t('actions.noKnown')}
          </Button>
          <Button variant="secondary" onClick={() => setOpen('UNABLE_TO_ASSESS')}>
            {t('actions.unable')}
          </Button>
        </div>
      )}

      {open === 'record' &&
        (reactions.isPending ? (
          <Skeleton height="8rem" />
        ) : (
          <RecordAllergy
            patientId={patientId}
            reactions={reactions.data ?? []}
            visitId={visitId}
            onRecorded={() => setOpen(null)}
            onCancel={() => setOpen(null)}
          />
        ))}

      {(open === 'NO_KNOWN_ALLERGY' || open === 'UNABLE_TO_ASSESS') && (
        <AssertAllergyStatus
          patientId={patientId}
          kind={open}
          visitId={visitId}
          onAsserted={() => setOpen(null)}
          onCancel={() => setOpen(null)}
        />
      )}

      <AllergyChanges patientId={patientId} reactions={reactions.data ?? []} />
    </section>
  );
}

/**
 * The assertion that currently stands, and the way back from it.
 *
 * Drawn as a statement by a person rather than as a property of the patient: a name, a
 * time, and — on "unable to assess" — the reason, which is what makes the third state
 * reviewable rather than a silent gap wearing a label.
 */
function StandingAssertion({
  assertion,
  patientId,
  mayWrite,
  status,
}: {
  assertion: AllergyAssertion;
  patientId: string;
  mayWrite: boolean;
  status: string;
}) {
  const t = useTranslations('allergies');
  const locale = useLocale() as Locale;
  const client = useQueryClient();

  const [withdrawing, setWithdrawing] = useState(false);
  const [reason, setReason] = useState('');
  const [failure, setFailure] = useState<string | null>(null);

  const withdraw = useMutation({
    mutationFn: (why: string) => withdrawAllergyAssertion(assertion.id, why),
    onSuccess: () => {
      setWithdrawing(false);
      setFailure(null);
      void client.invalidateQueries({ queryKey: allergyStateKey(patientId) });
      void client.invalidateQueries({ queryKey: allergyChangesKey(patientId) });
    },
    onError: () => setFailure(t('withdrawFailed')),
  });

  return (
    <article
      className="app-allergy-assertion"
      data-testid="standing-assertion"
      data-kind={assertion.kind}
      // A live allergy outranks any assertion when the status is worked out, and both are
      // true statements about their own moment. The attribute says which is being shown.
      data-status={status}
    >
      <p className="app-allergy-assertion__kind">{t(`status.${assertion.kind}`)}</p>
      <p className="app-allergy-assertion__body">{t(`statusBody.${assertion.kind}`)}</p>

      {assertion.reason && (
        <p className="app-allergy-assertion__reason">
          {t('assertionReason', { reason: assertion.reason })}
        </p>
      )}

      <p className="app-allergy-assertion__attribution">
        {t('assertedBy', {
          at: formatDateTime(Date.parse(assertion.asserted_at), locale),
          who: assertion.asserted_by,
        })}
      </p>

      {failure && (
        <AlertBanner tone="critical" title={failure} onDismiss={() => setFailure(null)}>
          {t('nothingChanged')}
        </AlertBanner>
      )}

      {mayWrite && !withdrawing && (
        <div className="app-allergy-assertion__actions">
          <Button variant="quiet" onClick={() => setWithdrawing(true)}>
            {t('withdrawAssertion')}
          </Button>
        </div>
      )}

      {withdrawing && (
        <Card elevation="raised" className="app-allergy-assertion__panel" compact>
          <h4>{t('withdrawAssertionTitle')}</h4>
          <p>{t('withdrawAssertionBody')}</p>
          <Input
            label={t('withdrawReason')}
            description={t('withdrawReasonHint')}
            placeholder={t('withdrawAssertionPlaceholder')}
            required
            value={reason}
            disabled={withdraw.isPending}
            onChange={(event) => setReason(event.target.value)}
          />
          <div className="app-allergy-assertion__panel-actions">
            <Button
              variant="quiet"
              disabled={withdraw.isPending}
              onClick={() => setWithdrawing(false)}
            >
              {t('cancel')}
            </Button>
            <Button
              variant="primary"
              loading={withdraw.isPending}
              disabled={!reasonAcceptable(reason) || withdraw.isPending}
              onClick={() => withdraw.mutate(reason.trim())}
            >
              {t('withdrawConfirm')}
            </Button>
          </div>
        </Card>
      )}
    </article>
  );
}
