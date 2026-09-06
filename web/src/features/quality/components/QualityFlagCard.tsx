'use client';

import { useLocale, useTranslations } from 'next-intl';

import { AlertBanner, Card } from '@dthcms/ui';

import { PersonLine } from '@/features/corrections';
import { bilingual } from '@/lib/bilingual';
import type { Locale } from '@/lib/i18n/config';

import { isOpen, type QualityFlag } from '../api/quality';

import { AnswerQualityFlag } from './AnswerQualityFlag';
import { CorrectionRate } from './CorrectionRate';
import { hourLabel, labelOr, qualityDate, qualityMoment } from './qualityText';

/**
 * One raised pattern, with everything a supervisor reads before speaking to anybody (CP63).
 *
 * # What a flag is, and what it is not
 *
 * It is a row saying *these three corrections have the same shape, and here is the first
 * question worth asking*. It is not a finding, a verdict, or a fact about how good anybody is
 * at their job. Everything on this card is arranged to keep that distinction visible, because
 * the moment it slips the plan's stated risk arrives: *"a metric that feels punitive damages
 * data honesty — staff hide errors instead of correcting them."*
 *
 * # `threshold_approved: false` is on the face of the card, on every card
 *
 * Every threshold this system ships with is a proposal with `approved_at` null. The notice
 * therefore appears on each flag rather than once at the top of a list, and it is not
 * collapsed away — a flag is read alone as often as in a list: quoted in a message, shown to
 * the person it is about, photographed and sent to a colleague. A flag that travelled without
 * that sentence would be a statement of clinic policy that no clinician has made.
 *
 * # The counts are drawn through the same component as everywhere else
 *
 * `observed_count` never appears without `entries_count`. Both were frozen into the row when
 * the flag was raised, precisely so the evidence does not move when the window slides — and a
 * flag showing only the numerator would be the accusation this whole design exists to avoid.
 * The rate is `'not-taken'` rather than `null`: a flag never computed a ratio, and saying
 * "eight more entries and a rate appears" underneath a flag raised on four hundred of them
 * would be a false statement dressed as caution.
 *
 * # The suggested action comes from the server, in the reader's language
 *
 * `action_en` / `action_bn` are on the threshold row and are rendered from it. There is no
 * copy of them in the message files, because the clinic can change what it wants somebody to
 * do about a pattern without shipping a release — and two sources for that sentence would
 * mean the screen and the database eventually give different advice.
 *
 * Two of the three seeded actions point away from the operator on purpose: *check the
 * instrument and the unit before concluding anything about the operator*, and *this is
 * usually a rota problem rather than a person problem*. Those sentences are the substance of
 * the feature, not decoration on it.
 *
 * # The evidence is the working, and it says when it was true
 *
 * The corrections behind the flag: when, at what hour on the clinic's clock, on which
 * measurement, for which stated reason — each with the display pair the server joined in, so
 * the one screen a supervisor reads before speaking to somebody is not a list of database
 * identifiers. **No patient and no clinical value**: a supervisor reading a patient's numbers
 * through their staff's record would be reading clinical data through a side door, and a
 * database invariant refuses a flag that carries either.
 *
 * Each row's status is what that request was **at the moment the flag was raised**, frozen with
 * everything else, and `status_as_of` says when that was. The label is on the row rather than
 * left implicit: a request that was open then and answered since still reads OPEN here, and a
 * supervisor who took that for the live state would go looking for a colleague who has already
 * replied.
 *
 * # An answered flag is drawn as answered
 *
 * The operator's own record now carries flags answered in the last thirty days as well as open
 * ones, so this card has to make the difference obvious rather than only correct. A note that
 * appeared on somebody's device and then silently vanished when a supervisor closed it would
 * teach them that things are decided about them out of sight — the same failure as never
 * telling them, arriving a fortnight later. So an answered flag keeps its place, states what
 * was decided, quotes the reason, and names the person who decided it.
 */
export interface QualityFlagCardProps {
  flag: QualityFlag;
  /**
   * Whether to name whose record this is.
   *
   * Off on somebody's own screen, where the name would be their own and the sentence would
   * read as a file being kept about them; on in the supervisor's queue, where a flag with no
   * name on it is a flag nobody can act on.
   */
  showOperator?: boolean;
  /**
   * Whether this reader may answer it. The control is not drawn otherwise and nothing is said
   * — a button that exists in order to be refused teaches people the software is unreliable.
   */
  mayAnswer?: boolean;
}

/** The four the contract defines. Anything else renders as itself rather than as an error. */
const KNOWN_CORRECTION_STATUSES = ['OPEN', 'APPLIED', 'REJECTED', 'OVERRIDDEN'];

export function QualityFlagCard({
  flag,
  showOperator = false,
  mayAnswer = false,
}: QualityFlagCardProps) {
  const t = useTranslations('quality');
  // The status of a correction is the corrections feature's word for it, borrowed rather than
  // written again. Two names for one status is how one screen comes to say "corrected by
  // somebody else" where another says "overridden" about the same row.
  const tCorrection = useTranslations('corrections');
  const locale = useLocale() as Locale;

  const pattern = bilingual(flag.threshold_en ?? '', flag.threshold_bn ?? '', locale);
  const action = bilingual(flag.action_en ?? '', flag.action_bn ?? '', locale);
  const answered = !isOpen(flag);

  return (
    <Card elevation="raised" as="article" className="app-quality__flag">
      <div
        data-testid={`quality-flag-${flag.id}`}
        data-status={flag.status}
        data-approved={String(flag.threshold_approved)}
      >
        <h3 className="app-quality__flag-title">
          {pattern === null ? (
            // A threshold row that has been deleted out from under a flag. The code is the
            // honest answer; inventing a sentence for it would describe a rule nobody wrote.
            <code>{flag.threshold_code}</code>
          ) : (
            <span lang={pattern.lang}>{pattern.text}</span>
          )}
        </h3>

        {answered && (
          // Said at the top rather than at the bottom. On the operator's own record this card
          // sits among open ones, and "somebody has already dealt with this" is the first thing
          // they need to know about it — not the last.
          <p className="app-quality__flag-state" data-testid={`flag-state-${flag.id}`}>
            {flag.status === 'DISMISSED' ? t('flag.wasDismissed') : t('flag.wasAcknowledged')}
          </p>
        )}

        {!flag.threshold_approved && (
          /*
           * Never hidden, never collapsed, and never below the fold of the card. The numbers
           * behind this flag are a proposal nobody has signed off; drawn without this it would
           * read as clinic policy, which would be a false statement about a colleague made by
           * software.
           */
          <AlertBanner
            tone="borderline"
            title={t('flag.unapprovedTitle')}
            className="app-quality__unapproved"
          >
            {t('flag.unapprovedBody')}
          </AlertBanner>
        )}

        {showOperator && (
          <p className="app-quality__flag-line">
            <span className="app-quality__label">{t('flag.whoseRecord')}</span>{' '}
            <PersonLine
              nameEn={flag.operator_name_en}
              nameBn={flag.operator_name_bn}
              code={flag.operator_code}
              id={flag.operator_id}
              testId={`flag-operator-${flag.id}`}
            />
          </p>
        )}

        <p className="app-quality__flag-line">
          <CorrectionRate
            corrections={flag.observed_count}
            entries={flag.entries_count}
            rate="not-taken"
            rateFloor={null}
            testId={`flag-counts-${flag.id}`}
          />
        </p>

        <p className="app-quality__flag-line app-quality__hint">
          {t('flag.window', {
            from: qualityDate(Date.parse(flag.window.from), locale),
            to: qualityDate(Date.parse(flag.window.to), locale),
          })}
          {' · '}
          {t('flag.raised', { when: qualityMoment(Date.parse(flag.raised_at), locale) })}
        </p>

        {action !== null && (
          <div className="app-quality__action" data-testid={`flag-action-${flag.id}`}>
            <p className="app-quality__label">{t('flag.suggested')}</p>
            <p lang={action.lang}>{action.text}</p>
          </div>
        )}

        <div className="app-quality__evidence">
          <p className="app-quality__label">{t('flag.evidence')}</p>
          {flag.evidence.length === 0 ? (
            // A flag whose working cannot be read. Said out loud: a supervisor about to speak
            // to a colleague on the strength of a row needs to know the row is not showing it.
            <p className="app-quality__hint" data-testid={`flag-no-evidence-${flag.id}`}>
              {t('flag.noEvidence')}
            </p>
          ) : (
            <>
              <ul className="app-quality__evidence-list">
                {/* The server's order — most recent first — so a supervisor reads the thing that
                    just happened rather than the thing that happened four weeks ago. */}
                {flag.evidence.map((item) => (
                  <li key={item.request_id} className="app-quality__evidence-row">
                    <span className="app-quality__evidence-when">
                      {qualityDate(Date.parse(item.at), locale)}
                      {' · '}
                      {t('flag.atHour', { time: hourLabel(item.hour, locale) })}
                    </span>
                    <span className="app-quality__evidence-what">
                      {labelOr(item.code_en, item.code_bn, locale, item.code)}
                    </span>
                    <span className="app-quality__evidence-why">
                      {labelOr(item.reason_en, item.reason_bn, locale, item.reason_code)}
                    </span>
                    <span className="app-quality__evidence-status">
                      {KNOWN_CORRECTION_STATUSES.includes(item.status)
                        ? tCorrection(`status.${item.status}`)
                        : item.status}
                    </span>
                  </li>
                ))}
              </ul>
              {/* When the statuses above were true. They are frozen with the rest of the row,
                  so a request that was waiting then and has been answered since still reads as
                  waiting — and a supervisor who took that for the live state would go looking
                  for a colleague who has already replied. */}
              <p className="app-quality__hint" data-testid={`flag-frozen-${flag.id}`}>
                {t('flag.statusAsOf', {
                  when: qualityMoment(
                    Date.parse(flag.evidence[0]?.status_as_of ?? flag.raised_at),
                    locale,
                  ),
                })}
              </p>
            </>
          )}
        </div>

        {answered && (
          <div className="app-quality__answered" data-testid={`flag-answered-${flag.id}`}>
            <p className="app-quality__hint">
              {flag.resolved_at !== undefined &&
                t('flag.answeredAt', {
                  when: qualityMoment(Date.parse(flag.resolved_at), locale),
                })}
            </p>
            {flag.resolved_by !== undefined && (
              <p className="app-quality__flag-line">
                <span className="app-quality__label">{t('flag.answeredBy')}</span>{' '}
                {/* Names now, joined by the server the same way the operator's are, so the two
                    people on one row are named the same way. The uuid stays a fallback for the
                    directory and never reaches the screen. */}
                <PersonLine
                  nameEn={flag.resolved_by_name_en}
                  nameBn={flag.resolved_by_name_bn}
                  code={flag.resolved_by_code}
                  id={flag.resolved_by}
                  testId={`flag-answerer-${flag.id}`}
                />
              </p>
            )}
            {flag.resolution !== undefined && flag.resolution.trim() !== '' && (
              <p className="app-quality__resolution" data-testid={`flag-resolution-${flag.id}`}>
                {flag.resolution}
              </p>
            )}
          </div>
        )}

        {!answered && mayAnswer && <AnswerQualityFlag flag={flag} />}
      </div>
    </Card>
  );
}
