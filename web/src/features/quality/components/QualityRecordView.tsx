'use client';

import { useLocale, useTranslations } from 'next-intl';

import { Card, EmptyState } from '@dthcms/ui';

import { PersonLine } from '@/features/corrections';
import type { Locale } from '@/lib/i18n/config';

import { rateIsAvailable, type QualityRecord } from '../api/quality';

import { CorrectionRate } from './CorrectionRate';
import { QualityFlagCard } from './QualityFlagCard';
import { hourLabel, labelOr, qualityDate } from './qualityText';

/**
 * One operator's month, drawn the same way for them and for their supervisor (CP63, §4.3).
 *
 * # Why there is one component and not two
 *
 * `GET /v1/quality/me` and `GET /v1/quality/operators/{id}` return the same shape, and the
 * contract carries a `mine` flag *specifically* so that one client component can serve both.
 * That is worth keeping. A supervisor and the person they supervise reading two different
 * renderings of the same month would start their conversation by disagreeing about what the
 * screen said — and the version that would drift is the operator's, because the supervisor's
 * is the one somebody is asked to improve.
 *
 * # What is on this screen, and what deliberately is not
 *
 * There is no total, no comparison with anybody else, no trend arrow and no colour that means
 * "bad". The plan's stated risk is that *"a metric that feels punitive damages data honesty —
 * staff hide errors instead of correcting them"*, and every one of those devices is a way of
 * saying "this is your position" without writing the sentence down.
 *
 * What is here is: how much work this was, what came back, what happened to it, and where the
 * corrections clustered — by reason, by measurement, and by hour of the clinic's day. The last
 * three are the whole of §4.3's purpose: *recurring patterns per operator surface so targeted
 * retraining happens*. A pattern is actionable; a number is not.
 *
 * # Every breakdown against its own denominator, and the hour one is the reason why
 *
 * `by_code` and `by_hour` each carry their own `entries`, and they are drawn against those
 * rather than against the month's total. The hour breakdown is where this stops being tidiness:
 * an operator who works only the late shift will always cluster late, and a screen showing the
 * numerator alone would present a rota as a person — which is exactly what the end-of-shift
 * threshold's own suggested action warns the reader against. Both go through the same component
 * as the headline figure, so the rule holds by construction rather than by care.
 *
 * `by_reason` has no denominator of its own and needs none: a reason is a slice of the
 * corrections, so its denominator is the corrections, and that is what it is drawn against.
 *
 * The server sends all three in stable code order rather than count-descending, deliberately: a
 * size-ranked list of the ways one person's values were questioned reads as a leaderboard.
 * Nothing here re-sorts them.
 *
 * # `rejected` sits beside `upheld`, and `overridden` sits apart from both
 *
 * A request the operator answered by saying the value stands, and was not overruled on, is an
 * operator who checked and was right. It is drawn in the same weight as the corrections that
 * led to a new value. And a value a *supervisor* corrected is its own line rather than part of
 * the operator's: CP62 made a supervisor's fix a different event precisely so that an operator's
 * record would not read it as though they had put it right themselves, and folding the two
 * together on the last screen would undo that at the last step.
 *
 * # Why the answered flags stay, and why the screen says for how long
 *
 * `/v1/quality/me` returns the open flags and the ones answered recently. A note that appeared
 * on somebody's device and then silently vanished when a supervisor closed it would teach them
 * that things are decided about them out of sight — which is the same failure as never telling
 * them, arriving a fortnight later.
 *
 * How long "recently" is comes off the payload as `answered_flags_kept_days` rather than being
 * written into a message. A sentence in a message file about how long something stays is a
 * sentence that goes on being said after somebody changes the rule, and this one is a promise
 * made to the person the notes are about.
 *
 * # An open flag is never outside the window
 *
 * The window scopes what *happened* in it — raised or answered — and never hides something
 * nobody has answered yet, so the record, the roster's flag counts and the supervisor's queue
 * agree at any `days`. There is deliberately nothing on this screen hedging about that.
 */
export interface QualityRecordViewProps {
  record: QualityRecord;
  /**
   * Whether the reader is looking at their own record.
   *
   * It changes two things: whether the heading names the person — on your own screen the name
   * would be your own, and the heading would read as a file being kept — and whether each flag
   * names whose record it is on. It does **not** change any number, any wording about the
   * numbers, or what is shown.
   */
  mine: boolean;
  /** Whether this reader holds `quality.flag.resolve` and may answer the flags drawn here. */
  mayAnswerFlags?: boolean;
}

export function QualityRecordView({
  record,
  mine,
  mayAnswerFlags = false,
}: QualityRecordViewProps) {
  const t = useTranslations('quality');
  const tCorrection = useTranslations('corrections');
  const locale = useLocale() as Locale;

  // A plain number rather than a formatted string: every message that draws it is an ICU
  // plural or a typed `{whole, number}`, and both need the number to do their job.
  const whole = record.corrections;

  return (
    <div className="app-stack" data-testid="quality-record">
      <Card elevation="raised" as="section">
        {!mine && (
          // The record carries its own identity now, so a supervisor opening somebody's month
          // gets a heading rather than a uuid — and without a second request to the staff
          // directory, which used to be the only way to find out whose month this was.
          <p className="app-quality__operator-name" data-testid="record-operator">
            <PersonLine
              nameEn={record.operator_name_en}
              nameBn={record.operator_name_bn}
              code={record.operator_code}
              id={record.operator_id}
              testId="record-operator-name"
            />
            {record.operator_status !== undefined && record.operator_status !== 'active' && (
              <span className="app-quality__hint">
                {' '}
                {record.operator_status === 'deactivated' || record.operator_status === 'suspended'
                  ? tCorrection(`person.presence.${record.operator_status}`)
                  : tCorrection('person.presence.other', { status: record.operator_status })}
              </span>
            )}
          </p>
        )}

        <p className="app-quality__hint" data-testid="quality-window">
          {t('window.covering', {
            from: qualityDate(Date.parse(record.window.from), locale),
            to: qualityDate(Date.parse(record.window.to), locale),
          })}
        </p>

        <p className="app-quality__headline">
          <CorrectionRate
            corrections={record.corrections}
            entries={record.entries}
            rate={record.rate}
            rateFloor={record.rate_floor}
            testId="record-rate"
          />
        </p>

        {record.corrections === 0 ? (
          <EmptyState icon="check" title={t('outcome.noneTitle')}>
            {t('outcome.noneBody', { entries: record.entries })}
          </EmptyState>
        ) : (
          <ul className="app-quality__outcomes" data-testid="record-outcomes">
            <li data-testid="outcome-upheld">
              {t('outcome.upheld', { part: record.upheld, whole })}
            </li>
            <li data-testid="outcome-overridden">
              {t('outcome.overridden', { part: record.overridden, whole })}
            </li>
            <li data-testid="outcome-rejected">
              {t('outcome.rejected', { part: record.rejected, whole })}
            </li>
            <li data-testid="outcome-open">{t('outcome.open', { part: record.open, whole })}</li>
          </ul>
        )}

        {record.corrections > 0 && (
          <p className="app-quality__hint" data-testid="stood-is-not-a-mark">
            {t('outcome.stoodNote')}
          </p>
        )}

        {!rateIsAvailable(record) && record.entries > 0 && (
          // Said once beside the figure rather than repeated: the rate line above already
          // names how many more values it would take.
          <p className="app-quality__hint" data-testid="rate-floor-note">
            {t('rate.floorNote')}
          </p>
        )}
      </Card>

      {record.corrections > 0 && (
        <Card elevation="raised" as="section">
          <h2 className="app-quality__section">{t('breakdown.title')}</h2>
          <p className="app-page__description">{t('breakdown.body')}</p>

          <div className="app-quality__breakdowns">
            <section data-testid="by-reason">
              <h3 className="app-quality__section-sub">{t('breakdown.byReason')}</h3>
              <p className="app-quality__hint">{t('breakdown.byReasonHint')}</p>
              <ul className="app-quality__breakdown-list">
                {record.by_reason.map((row) => (
                  <li key={row.reason_code}>
                    <span className="app-quality__breakdown-label">
                      {labelOr(row.display_en, row.display_bn, locale, row.reason_code)}
                    </span>{' '}
                    <span className="app-quality__breakdown-count">
                      {t('breakdown.share', { part: row.corrections, whole })}
                    </span>
                    {row.transcription && (
                      // Said rather than left implicit. A vocabulary whose consequences are
                      // invisible is one people learn to answer strategically — and this is
                      // the reason the transcription threshold counts.
                      <span className="app-quality__counted">{t('breakdown.counted')}</span>
                    )}
                  </li>
                ))}
              </ul>
            </section>

            <section data-testid="by-code">
              <h3 className="app-quality__section-sub">{t('breakdown.byCode')}</h3>
              <p className="app-quality__hint">{t('breakdown.byCodeHint')}</p>
              <ul className="app-quality__breakdown-list">
                {record.by_code.map((row) => (
                  <li key={row.code}>
                    <span className="app-quality__breakdown-label">
                      {labelOr(row.display_en, row.display_bn, locale, row.code)}
                    </span>{' '}
                    {/* Against how many of *that* measurement they took, not against the
                        month's total: three corrections on a weight is a different fact when
                        the operator weighed four hundred people and when they weighed nine. */}
                    <CorrectionRate
                      corrections={row.corrections}
                      entries={row.entries}
                      rate="not-taken"
                      rateFloor={null}
                      inline
                      testId={`by-code-${row.code}`}
                    />
                  </li>
                ))}
              </ul>
            </section>

            <section data-testid="by-hour">
              <h3 className="app-quality__section-sub">{t('breakdown.byHour')}</h3>
              <p className="app-quality__hint">{t('breakdown.byHourHint')}</p>
              <ul className="app-quality__breakdown-list">
                {record.by_hour.map((row) => (
                  <li key={row.hour}>
                    <span className="app-quality__breakdown-label">
                      {hourLabel(row.hour, locale)}
                    </span>{' '}
                    {/* The hour's own denominator, and this is the one that decides whether
                        the end-of-shift pattern is fair: somebody who works only the late
                        shift clusters late by definition, and the numerator alone would put
                        a rota in front of a supervisor as though it were a person. */}
                    <CorrectionRate
                      corrections={row.corrections}
                      entries={row.entries}
                      rate="not-taken"
                      rateFloor={null}
                      inline
                      testId={`by-hour-${row.hour}`}
                    />
                  </li>
                ))}
              </ul>
            </section>
          </div>
        </Card>
      )}

      <section className="app-stack" data-testid="record-flags">
        <h2 className="app-quality__section">{t('flags.title')}</h2>
        {mine && (
          /*
           * Said before the list rather than discovered from it. An operator who watched a note
           * about themselves appear and then vanish would learn that things are decided about
           * them out of sight, which is the same failure as never telling them.
           *
           * The number is the server's, off the payload, for the same reason `rate_floor` is:
           * a sentence about how long something stays that was written in a message file is a
           * sentence that goes on being said after somebody changes the rule.
           */
          <p className="app-quality__hint" data-testid="answered-flags-stay">
            {t('flags.answeredStay', { days: record.answered_flags_kept_days })}
          </p>
        )}
        {record.flags.length === 0 ? (
          <EmptyState icon="check" title={t('flags.noneTitle')}>
            {mine ? t('flags.noneBodyMine') : t('flags.noneBody')}
          </EmptyState>
        ) : (
          <ul className="app-quality__flags">
            {record.flags.map((flag) => (
              <li key={flag.id}>
                <QualityFlagCard flag={flag} showOperator={!mine} mayAnswer={mayAnswerFlags} />
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
