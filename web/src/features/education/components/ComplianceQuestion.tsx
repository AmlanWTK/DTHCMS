'use client';

import { useLocale, useTranslations } from 'next-intl';

import type { Locale } from '@/lib/i18n/config';

import type { RecordingCapability } from '../api/capability';
import type { EducationReference } from '../api/education';

/**
 * §7's question, asked without inviting a lie (CP92).
 *
 * # The preamble is the control
 *
 * *"Did you take your medicine every day?"* is a question with one socially acceptable answer,
 * and the number that comes back from it is decoration. *"Most people miss a dose sometimes. In
 * the last week, how many times did you miss?"* tells the patient that missing doses is normal
 * and expected, which is what makes the true number sayable.
 *
 * So the preamble is rendered as part of the question and not as a caption, a tooltip or a
 * placeholder — it is the half that does the work, and the half a UI tidy-up removes first
 * because it looks like filler. It comes from the server with the question rather than from a
 * message file, so that there is exactly one wording and it cannot be shortened on one surface.
 *
 * # Why the count is buttons up to a point and then a field
 *
 * Nearly every real answer is nought to seven — a week has seven days and most regimens are
 * once or twice daily — and a row of buttons is one tap for that, on a tablet, while somebody is
 * talking. Above seven it is a field, because a patient on four-times-daily insulin who missed
 * eleven is a real answer and a row of fifty buttons is not a control.
 *
 * # Why the reasons appear only after a number above zero
 *
 * §7 asks for the reason *if any were missed*. Offering the list to somebody who missed none
 * invites an answer to a question nobody asked, and a coded reason attached to a zero is a row
 * that means nothing and will be counted by something.
 */

export interface ComplianceQuestionProps {
  /** Proof that this reader may record. See features/education/api/capability.ts. */
  capability: RecordingCapability;
  reference: EducationReference;
  missed: number | null;
  reasons: string[];
  onMissed: (count: number | null) => void;
  onToggleReason: (code: string) => void;
  disabled?: boolean;
}

/** How many taps are offered before the control becomes a field. A week, plus nought. */
const QUICK_ANSWERS = [0, 1, 2, 3, 4, 5, 6, 7];

export function ComplianceQuestion({
  reference,
  missed,
  reasons,
  onMissed,
  onToggleReason,
  disabled = false,
}: ComplianceQuestionProps) {
  const t = useTranslations('education');
  const locale = useLocale() as Locale;
  const bn = locale === 'bn';

  return (
    <section className="edu-compliance" data-testid="compliance">
      <h3 className="edu-compliance__question" data-testid="compliance-question">
        <span className="edu-compliance__primary" lang={bn ? 'bn' : 'en'}>
          {bn ? reference.compliance_question_bn : reference.compliance_question_en}
        </span>
        <span className="edu-compliance__secondary" lang={bn ? 'en' : 'bn'}>
          {bn ? reference.compliance_question_en : reference.compliance_question_bn}
        </span>
      </h3>

      <div
        className="edu-compliance__counts"
        role="radiogroup"
        aria-label={bn ? reference.compliance_question_bn : reference.compliance_question_en}
      >
        {QUICK_ANSWERS.map((count) => (
          <button
            key={count}
            type="button"
            role="radio"
            aria-checked={missed === count}
            className="edu-compliance__count"
            data-selected={missed === count ? 'true' : undefined}
            data-testid={`missed-${count}`}
            disabled={disabled}
            onClick={() => onMissed(count)}
          >
            {count}
          </button>
        ))}
        <label className="edu-compliance__more">
          <span className="edu-compliance__more-label">{t('compliance.more')}</span>
          <input
            type="number"
            min={0}
            inputMode="numeric"
            className="edu-compliance__field"
            data-testid="missed-other"
            disabled={disabled}
            value={missed !== null && missed > 7 ? missed : ''}
            onChange={(event) => {
              const raw = event.target.value.trim();
              // An empty field is *not* zero. "They said none" and "we have not asked" are
              // different facts, and the server keeps them apart — clearing the field has to
              // put the screen back into the second state rather than record the first.
              if (raw === '') {
                onMissed(null);
                return;
              }
              const parsed = Number.parseInt(raw, 10);
              onMissed(Number.isFinite(parsed) && parsed >= 0 ? parsed : null);
            }}
          />
        </label>
      </div>

      {missed !== null && missed > 0 && (
        <div className="edu-compliance__reasons" data-testid="compliance-reasons">
          <p className="edu-compliance__reasons-lead">{t('compliance.why')}</p>
          <div className="edu-compliance__reason-list">
            {reference.missed_dose_reasons.map((reason) => (
              <button
                key={reason.code}
                type="button"
                className="edu-compliance__reason"
                aria-pressed={reasons.includes(reason.code)}
                data-selected={reasons.includes(reason.code) ? 'true' : undefined}
                data-testid={`reason-${reason.code}`}
                disabled={disabled}
                onClick={() => onToggleReason(reason.code)}
              >
                {bn ? reason.display_bn : reason.display_en}
              </button>
            ))}
          </div>
          {/* More than one is allowed and normal. A patient who ran out because it cost too
              much has two reasons and the fix for each is different. */}
          <p className="edu-compliance__reasons-note">{t('compliance.severalReasons')}</p>
        </div>
      )}
    </section>
  );
}
