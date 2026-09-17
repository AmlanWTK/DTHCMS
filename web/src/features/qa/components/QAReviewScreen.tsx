'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useMemo, useState } from 'react';

import { AlertBanner, Badge, Button, Card, Input, Select } from '@dthcms/ui';

import { StepUpCancelled, useStepUp } from '@/features/auth';
import { ApiError } from '@/lib/api';
import type { Locale } from '@/lib/i18n/config';

import {
  blockingOf,
  bouncePrescription,
  clearPrescription,
  defaultBounceStation,
  overrideQAGate,
  qaReviewKey,
  readQAReview,
  warningsOf,
  type QAReviewPage,
} from '../api/qa';
import {
  useBounceCapability,
  useClearCapability,
  useOverrideCapability,
} from '../api/capability';

import { FindingList } from './FindingList';

/**
 * Station 10's screen (CP83).
 *
 * # What the screen has to make impossible to misread
 *
 * Three things, and they are all the same kind of mistake — two states that look identical:
 *
 *  1. **A clean file and an unchecked one.** `rules_live: 0` means this clinic's checklist is
 *     empty, so nothing was checked. It is not the same as eighteen rules passing, and the
 *     summary sentence says which — in words, from the server, in both languages. A green tick
 *     for both would be the interface telling an officer somebody checked when nobody did.
 *  2. **"Would clear" and "has been cleared".** `can_clear` is a judgement about the findings;
 *     `clearance_stands` is a fact about the record. The gate on the prescription reads the
 *     second. They are drawn separately and the second is the one that says the file may be
 *     signed.
 *  3. **A warning and nothing.** A WARN clears — but only with an acknowledgement, and the
 *     acknowledgement is a tick the officer makes, one per warning, recorded by code. A screen
 *     that cleared over warnings silently would make WARN and "no rule" the same severity.
 *
 * # Who is handed which controls
 *
 * Four readers, four different screens, and none of them is another with its buttons disabled.
 * The capability tokens are the mechanism: a reader who does not hold `qa.clear` cannot be passed
 * one, and the clearance form takes one as a required prop, so the control is **absent** rather
 * than present and refused.
 *
 * The override is the sharpest case. It belongs to the consultant and not to the officer working
 * this desk — `docs/qa-rules.md` §2 puts the grant with the prescriber and the rate with Quality,
 * because the answer to a rising override rate is a person asking why. A QA officer looking at
 * this screen sees the blocked file and no way past it, which is correct: the way past it is to
 * fetch a consultant.
 */

export interface QAReviewScreenProps {
  prescriptionId: string;
}

export function QAReviewScreen({ prescriptionId }: QAReviewScreenProps) {
  const t = useTranslations('qa');
  const locale = useLocale() as Locale;
  const bn = locale === 'bn';
  const queryClient = useQueryClient();
  const requestStepUp = useStepUp();

  const mayClear = useClearCapability();
  const mayBounce = useBounceCapability();
  const mayOverride = useOverrideCapability();

  const review = useQuery({
    queryKey: qaReviewKey(prescriptionId),
    queryFn: () => readQAReview(prescriptionId),
  });

  const [acknowledged, setAcknowledged] = useState<Record<string, boolean>>({});
  const [station, setStation] = useState<string>('');
  const [reasonEN, setReasonEN] = useState('');
  const [reasonBN, setReasonBN] = useState('');
  const [overrideReason, setOverrideReason] = useState('');
  const [failure, setFailure] = useState<string | null>(null);

  const page: QAReviewPage | undefined = review.data;
  const blocking = useMemo(() => (page ? blockingOf(page.review) : []), [page]);
  const warnings = useMemo(() => (page ? warningsOf(page.review) : []), [page]);

  const stations = useMemo(() => {
    const table = (page?.review.stations ?? {}) as Record<string, string[]>;
    return Object.entries(table)
      .map(([code, names]) => ({
        value: code,
        label: (bn ? names?.[1] : names?.[0]) || code,
      }))
      .sort((a, b) => a.label.localeCompare(b.label));
  }, [page, bn]);

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: qaReviewKey(prescriptionId) });

  const clear = useMutation({
    mutationFn: () =>
      clearPrescription(prescriptionId, {
        event_id: crypto.randomUUID(),
        acknowledged: warnings.filter((w) => acknowledged[w.rule_code]).map((w) => w.rule_code),
      }),
    onSuccess: () => {
      setFailure(null);
      void invalidate();
    },
    onError: (error) => setFailure(messageOf(error, t('clear.failed'))),
  });

  const bounce = useMutation({
    mutationFn: () =>
      bouncePrescription(prescriptionId, {
        event_id: crypto.randomUUID(),
        // Sent explicitly rather than left to the server's default, so that what the officer
        // saw on the button is what the record says.
        bounce_station_code: station || defaultBounceStation(page!.review),
        reason_en: reasonEN.trim() || undefined,
        reason_bn: reasonBN.trim() || undefined,
      }),
    onSuccess: () => {
      setFailure(null);
      void invalidate();
    },
    onError: (error) => setFailure(messageOf(error, t('bounce.failed'))),
  });

  const override = useMutation({
    mutationFn: async () => {
      // The second factor is asked for **before** the request, and the token is a required
      // argument to the call: a caller who has not minted one does not compile. The server
      // refuses without it too; this is the half that makes the omission visible at build time.
      const token = await requestStepUp('qa.override', t('override.action'));
      return overrideQAGate(
        prescriptionId,
        { event_id: crypto.randomUUID(), reason: overrideReason.trim() },
        token,
      );
    },
    onSuccess: () => {
      setFailure(null);
      void invalidate();
    },
    onError: (error) => {
      // A consultant who closed the second-factor dialog has decided not to override. That is
      // not a failure and must not be reported as one.
      if (error instanceof StepUpCancelled) return;
      setFailure(messageOf(error, t('override.failed')));
    },
  });

  if (review.isPending) return <p>{t('loading')}</p>;
  if (review.isError || !page) return <AlertBanner tone="critical" title={t('unavailable')} />;

  const everyWarningAcknowledged = warnings.every((w) => acknowledged[w.rule_code]);
  const summary = bn ? page.summary_bn : page.summary_en;
  const decided = page.review.decision;

  return (
    <div className="app-stack" data-testid="qa-review">
      {/* The sentence the screen leads with, and the colour beside it.
          //
          // **`unknown` and not `normal` when no rule was asked.** The first draft of this screen
          // drew the empty-rule-table case with a green tick and a green ground, and the
          // screenshot is what caught it: the words said "nothing was checked" and the colour
          // said "all good", which is `docs/qa-rules.md` §1's theatre arriving through the
          // palette. An officer scanning the screen reads the colour first.
          //
          // `unknown` is the design system's own "this was not established", which is exactly
          // what an unchecked file is. */}
      <AlertBanner
        tone={
          page.review.rules_live === 0
            ? 'unknown'
            : blocking.length > 0
              ? 'critical'
              : warnings.length > 0
                ? 'high'
                : 'normal'
        }
        title={summary}
      >
        <p style={{ margin: 0 }}>
          {t('rulesLive', { count: page.review.rules_live })}
          {' · '}
          {page.clearance_stands ? t('gate.cleared') : t('gate.notCleared')}
        </p>
      </AlertBanner>

      {page.review.override && (
        <AlertBanner tone="high" title={t('override.standing')}>
          <p style={{ margin: 0 }}>
            {t('override.grantedBy', {
              who:
                (bn
                  ? page.review.override.granted_by_name_bn
                  : page.review.override.granted_by_name_en) ||
                page.review.override.granted_by_code ||
                '',
            })}
          </p>
          <p style={{ margin: '0.25rem 0 0' }}>{page.review.override.reason}</p>
        </AlertBanner>
      )}

      {decided && (
        <Card compact elevation="flat">
          <strong>
            {decided.outcome === 'CLEARED' ? t('decided.cleared') : t('decided.bounced')}
          </strong>
          {decided.outcome === 'BOUNCED' && (
            <p style={{ margin: '0.25rem 0 0' }}>
              {(bn ? decided.bounce_station_bn : decided.bounce_station_en) ||
                decided.bounce_station_code}
              {' — '}
              {bn ? decided.reason_bn : decided.reason_en}
            </p>
          )}
        </Card>
      )}

      <section className="app-stack">
        <h2>{t('blocking.title', { count: blocking.length })}</h2>
        <FindingList
          findings={blocking}
          empty={<p style={{ margin: 0 }}>{t('blocking.none')}</p>}
        />
      </section>

      <section className="app-stack">
        <h2>{t('warnings.title', { count: warnings.length })}</h2>
        <FindingList
          findings={warnings}
          empty={<p style={{ margin: 0 }}>{t('warnings.none')}</p>}
        />

        {/* The acknowledgement. Drawn only for a reader who may clear, because it is part of
            the clearance rather than a note anybody can leave. */}
        {mayClear && warnings.length > 0 && (
          <Card compact>
            <p style={{ marginTop: 0 }}>{t('warnings.acknowledgePrompt')}</p>
            {warnings.map((warning) => (
              <label
                key={warning.rule_code}
                style={{ display: 'block', margin: '0.35rem 0' }}
              >
                <input
                  type="checkbox"
                  checked={Boolean(acknowledged[warning.rule_code])}
                  onChange={(event) =>
                    setAcknowledged((current) => ({
                      ...current,
                      [warning.rule_code]: event.target.checked,
                    }))
                  }
                />{' '}
                {bn ? warning.title_bn : warning.title_en}
              </label>
            ))}
          </Card>
        )}
      </section>

      {failure && <AlertBanner tone="critical" title={failure} />}

      <div style={{ display: 'flex', gap: '0.75rem', flexWrap: 'wrap' }}>
        {/* **Absent, not disabled, for a reader who may not act.** A disabled button is still a
            statement that this person's job includes the act. */}
        {mayClear && (
          <Button
            variant="primary"
            data-testid="qa-clear"
            disabled={
              clear.isPending ||
              !page.can_clear ||
              !everyWarningAcknowledged ||
              page.clearance_stands
            }
            onClick={() => clear.mutate()}
          >
            {clear.isPending ? t('clear.saving') : t('clear.action')}
          </Button>
        )}
        {mayBounce && (
          <Button
            variant="secondary"
            data-testid="qa-bounce"
            disabled={bounce.isPending}
            onClick={() => bounce.mutate()}
          >
            {bounce.isPending ? t('bounce.saving') : t('bounce.action')}
          </Button>
        )}
      </div>

      {/* The bounce's own words. Optional: leaving them empty takes the finding's sentence,
          which is almost always what the officer would have typed. */}
      {mayBounce && (
        <Card compact header={<strong>{t('bounce.title')}</strong>}>
          <div className="app-stack">
            <Select
              label={t('bounce.station')}
              value={station || defaultBounceStation(page.review)}
              options={stations}
              onChange={(event) => setStation(event.target.value)}
            />
            <Input
              label={t('bounce.reasonEN')}
              value={reasonEN}
              placeholder={blocking[0]?.subject_en ?? ''}
              onChange={(event) => setReasonEN(event.target.value)}
            />
            <Input
              label={t('bounce.reasonBN')}
              value={reasonBN}
              placeholder={blocking[0]?.subject_bn ?? ''}
              onChange={(event) => setReasonBN(event.target.value)}
            />
          </div>
        </Card>
      )}

      {/* The valve. Drawn only for the consultant — the officer working this desk holds
          `qa.review` and not `qa.override`, and that separation is the checkpoint. */}
      {mayOverride && blocking.length > 0 && !page.review.override && (
        <Card compact header={<strong>{t('override.title')}</strong>}>
          <div className="app-stack">
            <p style={{ margin: 0 }}>{t('override.explain')}</p>
            <Input
              label={t('override.reason')}
              value={overrideReason}
              onChange={(event) => setOverrideReason(event.target.value)}
            />
            <div>
              <Button
                variant="danger"
                data-testid="qa-override"
                disabled={override.isPending || overrideReason.trim() === ''}
                onClick={() => override.mutate()}
              >
                {override.isPending ? t('override.saving') : t('override.action')}
              </Button>
            </div>
            <p style={{ margin: 0, opacity: 0.75 }}>{t('override.stepUp')}</p>
          </div>
        </Card>
      )}

      {/* What a reader who may look and not act is told, instead of being handed controls. */}
      {!mayClear && !mayBounce && !mayOverride && (
        <Badge tone="neutral">{t('readOnly')}</Badge>
      )}
    </div>
  );
}

function messageOf(error: unknown, fallback: string): string {
  if (error instanceof ApiError && error.message) return error.message;
  return fallback;
}
