'use client';

import { useLocale, useTranslations } from 'next-intl';

import { ValueWithAttribution, educationAttribution } from '@/features/attribution';
import { formatDate } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';

import {
  notApplicableReasonOf,
  scoreOf,
  type EducationCompetency,
  type EducationReference,
  type EducationSession,
} from '../api/education';

import { ScoreFace } from './ScoreFace';

/**
 * Station 11 as the physician reads it at the next consultation (CP92 criterion 2).
 *
 * # Why this is a separate component and not the form with its buttons switched off
 *
 * A disabled control is still a control. It occupies the shape of a thing that can be operated,
 * it invites the pointer, and the only thing between the reader and a 403 is an attribute
 * somebody has to remember to set. What a consultant needs here is not a form he may not submit
 * — it is *the record*: what the patient was able to do, who watched, and when.
 *
 * The improvement score is the sharpest case and the reason the separation is structural rather
 * than stylistic. CP88 §1 moves the question away from the consultation because a patient asked
 * by the person whose treatment it grades answers upward. A screen that draws the consultant ten
 * tappable numerals with one of them highlighted is a screen telling him the number is his to
 * adjust — and he would be adjusting it in the officer's name. So here the score is a sentence
 * with a face beside it and no affordance anywhere near it.
 *
 * # Every fact carries who recorded it
 *
 * §4.2's promise applies to this station as much as to a blood pressure, and arguably more:
 * *"what were you told about injection sites, and by whom"* is the question CP92 exists to make
 * answerable. Each row goes through `ValueWithAttribution`, the same component every clinical
 * value in the application is drawn by, so the answer is one interaction away rather than a
 * different gesture on this screen than on the dashboard.
 *
 * # Nothing here is rolled up
 *
 * There is no percentage and no count of "items passed". §8 forbids it: a percentage would be
 * easier to read and would lose the only thing this station produces that nobody else can, which
 * is *which specific step this specific patient got wrong*. The tally the officer's screen shows
 * while they work is a progress indicator for them, not a score for him.
 */

export interface RecordedAssessmentProps {
  session: EducationSession;
  reference: EducationReference;
}

export function RecordedAssessment({ session, reference }: RecordedAssessmentProps) {
  const t = useTranslations('education');
  const locale = useLocale() as Locale;
  const bn = locale === 'bn';

  const assessed = session.prior_competency;

  return (
    <div className="edu-record app-stack" data-testid="recorded-assessment">
      <p className="edu-record__lede">{t('read.lede')}</p>

      {/* 1. What the patient was able to do. */}
      {assessed.length === 0 ? (
        <p className="edu-record__none" data-testid="no-competency">
          {t('read.noCompetency')}
        </p>
      ) : (
        <section className="edu-record__items" data-testid="recorded-competency">
          <h3 className="edu-record__title">{t('read.competency')}</h3>
          <ol className="edu-record__list">
            {[...assessed]
              .sort(byChecklistThenOrdinal)
              .map((item) => (
                <li
                  className="edu-record__item"
                  key={item.code}
                  data-state={item.state}
                  data-testid={`recorded-${item.code}`}
                >
                  <ValueWithAttribution
                    attribution={educationAttribution(item)}
                    label={bn ? item.text_bn : item.text_en}
                    testId={`recorded-who-${item.code}`}
                  >
                    <span className="edu-record__row">
                      {/* The state first and in words. A reader scanning ten rows for the one
                          that went wrong is scanning for a word, not for a tint. */}
                      <span className="edu-record__state" data-state={item.state}>
                        {stateLabel(reference, item.state, bn)}
                      </span>
                      <span className="edu-record__wording" lang={bn ? 'bn' : 'en'}>
                        {bn ? item.text_bn : item.text_en}
                      </span>
                      {item.is_critical && (
                        <span className="edu-record__critical">{t('checklist.critical')}</span>
                      )}
                      <span className="edu-record__when">
                        {formatDate(Date.parse(item.observed_at), locale)}
                      </span>
                    </span>
                  </ValueWithAttribution>
                </li>
              ))}
          </ol>
        </section>
      )}

      {/* 2. What they said about missed doses. */}
      <section className="edu-record__compliance" data-testid="recorded-compliance">
        <h3 className="edu-record__title">{t('read.compliance')}</h3>
        {session.compliance_record === undefined ? (
          // Nobody asked. Said out loud rather than drawn as a zero — a patient nobody asked
          // and a patient who missed nothing are opposite facts arriving as the same blank.
          <p className="edu-record__none" data-testid="compliance-unasked">
            {t('read.complianceUnasked')}
          </p>
        ) : (
          <ValueWithAttribution
            attribution={educationAttribution(session.compliance_record)}
            label={t('read.compliance')}
            testId="recorded-who-compliance"
          >
            <span className="edu-record__row">
              <span className="edu-record__missed">
                {session.compliance_record.missed_doses === null
                  ? t('read.missedUnknown')
                  : t('read.missed', { count: session.compliance_record.missed_doses })}
              </span>
              {session.compliance_record.reasons.length > 0 && (
                <span className="edu-record__reasons">
                  {session.compliance_record.reasons
                    .map((code) => reasonLabel(reference, code, bn))
                    .join(' · ')}
                </span>
              )}
            </span>
          </ValueWithAttribution>
        )}
      </section>

      {/* 3. How they said they feel. */}
      <RecordedScore session={session} reference={reference} />
    </div>
  );
}

/**
 * The improvement score, as a statement rather than as a control.
 *
 * Three shapes and they are three different sentences: a number with its band, a recorded
 * not-applicable with the reason, and nobody having asked. The middle one is the one a screen
 * most easily loses — "first visit, no comparison" rendered as an empty score would read as a
 * patient who declined to answer, which is the confusion the two observation codes exist to
 * prevent in the record and which the screen must not reintroduce.
 */
function RecordedScore({ session, reference }: RecordedAssessmentProps) {
  const t = useTranslations('education');
  const locale = useLocale() as Locale;
  const bn = locale === 'bn';

  const record = session.improvement_record;
  const scale = reference.score_scale;

  if (record === undefined) {
    return (
      <section className="edu-record__score" data-testid="recorded-score">
        <h3 className="edu-record__title">{t('read.score')}</h3>
        <p className="edu-record__none" data-testid="score-unasked">
          {session.first_visit ? t('read.scoreFirstVisit') : t('read.scoreUnasked')}
        </p>
      </section>
    );
  }

  const value = scoreOf(record.answer);
  const reason = notApplicableReasonOf(record.answer);
  const anchor =
    value === null ? null : (scale.anchors.find((b) => value >= b.from_value && value <= b.to_value) ?? null);

  return (
    <section className="edu-record__score" data-testid="recorded-score">
      <h3 className="edu-record__title">{t('read.score')}</h3>
      {/* The question the patient was actually asked, quoted. A physician reading "7" without it
          is reading a number whose wording he has to remember, and §2's wording is the whole
          reason the number means one thing. */}
      <p className="edu-record__question" lang={bn ? 'bn' : 'en'}>
        {bn ? scale.question_bn : scale.question_en}
      </p>
      <ValueWithAttribution
        attribution={educationAttribution(record)}
        label={bn ? scale.question_bn : scale.question_en}
        testId="recorded-who-score"
      >
        {value === null ? (
          <span className="edu-record__na" data-testid="recorded-score-na">
            {naLabel(reference, reason, bn)}
          </span>
        ) : (
          <span className="edu-record__answer" data-testid="recorded-score-value">
            {anchor && <ScoreFace rank={anchor.face_rank} ranks={scale.anchors.length} size={28} />}
            <span className="edu-record__number">
              {value}
              <span className="edu-record__of">/{scale.max_value}</span>
            </span>
            {anchor && (
              <span className="edu-record__band">{bn ? anchor.label_bn : anchor.label_en}</span>
            )}
          </span>
        )}
      </ValueWithAttribution>
    </section>
  );
}

/** Checklist first, then the order the physician authored the items in. */
function byChecklistThenOrdinal(a: EducationCompetency, b: EducationCompetency): number {
  if (a.checklist_code !== b.checklist_code) {
    return a.checklist_code < b.checklist_code ? -1 : 1;
  }
  return a.ordinal - b.ordinal;
}

function stateLabel(reference: EducationReference, state: string, bn: boolean): string {
  const found = reference.states.find((option) => option.state === state);
  if (!found) return state;
  return bn ? found.display_bn : found.display_en;
}

function reasonLabel(reference: EducationReference, code: string, bn: boolean): string {
  const found = reference.missed_dose_reasons.find((reason) => reason.code === code);
  if (!found) return code;
  return bn ? found.display_bn : found.display_en;
}

function naLabel(reference: EducationReference, code: string | null, bn: boolean): string {
  if (code === null) return '';
  const found = reference.not_applicable_reasons.find((reason) => reason.code === code);
  if (!found) return code;
  return bn ? found.display_bn : found.display_en;
}
