'use client';

import { useLocale, useTranslations } from 'next-intl';

import type { Locale } from '@/lib/i18n/config';

import type { RecordingCapability } from '../api/capability';
import type {
  EducationChecklist,
  EducationCompetency,
  EducationReference,
  EducationState,
} from '../api/education';

/**
 * One device's checklist, scored on what the patient did (CP92 §5).
 *
 * # Three buttons per line, and why there is no fourth and no default
 *
 * §5's three states are the whole of this station's output. There is no "not assessed" button
 * because there is no such state: an item nobody scored has no row, and the absence of a row is
 * what says the officer did not get to it. A fourth button would turn that honest absence into a
 * recorded judgement, and a default selection would turn it into a recorded judgement nobody
 * made.
 *
 * "Corrected today" sits in the middle because that is where it belongs on the scale and because
 * it is the one an officer in a hurry will otherwise skip past. §5: a patient who has been
 * injecting into one spot for a year and was corrected today is a different patient from one who
 * never needed correcting, and next visit's officer needs to know which.
 *
 * # What "last time" is doing on the screen
 *
 * Beside each item, quietly: the state that item was last left in. It is the reason the three
 * states exist rather than two — *the thing to check next time is exactly what was corrected
 * last time* — and an officer who has to open another screen to find it will not.
 *
 * It is drawn as history and never as a default. Pre-selecting last visit's answer would produce
 * a screen that records what the patient did in March unless somebody actively disagrees, which
 * is the failure this station exists to prevent.
 *
 * # It cannot be rendered by a reader who may not record
 *
 * `capability` is required and there is no way to construct one outside
 * `useRecordingCapability`. A physician's screen holds `null` there and therefore cannot mount
 * this component at all — the read mode is a different component that draws the same items as
 * settled facts. That is the structural half of the fix: not a disabled button, an absent one.
 *
 * # Why the critical items are marked and not weighted
 *
 * Resuspending a cloudy insulin, the air-shot and holding for ten are the three that silently
 * cost a patient their dose. They carry a mark so an officer's eye goes to them. They carry no
 * extra weight in any score, because §8 forbids a score: *"a percentage would be easier to plot
 * and would lose the only thing this station produces that nobody else can."*
 */

export interface TechniqueChecklistProps {
  /**
   * Proof that this reader may record. Required, unused in the body, and that is the point:
   * the compiler will not let a caller who has not narrowed `useRecordingCapability()` render
   * a control that writes.
   */
  capability: RecordingCapability;
  checklist: EducationChecklist;
  states: EducationReference['states'];
  /** What has been answered so far, by observation code. */
  answers: Record<string, EducationState>;
  /** What the last officer saw, by observation code. */
  previous: Map<string, EducationCompetency>;
  onAnswer: (code: string, state: EducationState) => void;
  disabled?: boolean;
}

export function TechniqueChecklist({
  checklist,
  states,
  answers,
  previous,
  onAnswer,
  disabled = false,
}: TechniqueChecklistProps) {
  const t = useTranslations('education');
  const locale = useLocale() as Locale;
  const bn = locale === 'bn';

  return (
    <section className="edu-checklist" data-testid={`checklist-${checklist.code}`}>
      <h3 className="edu-checklist__title">{bn ? checklist.title_bn : checklist.title_en}</h3>
      <ol className="edu-checklist__items">
        {checklist.items.map((item) => {
          const answered = answers[item.code];
          const last = previous.get(item.code);
          return (
            <li
              className="edu-checklist__item"
              key={item.code}
              data-testid={`item-${item.code}`}
              data-critical={item.is_critical ? 'true' : undefined}
              data-answered={answered ?? undefined}
            >
              <div className="edu-checklist__text">
                <span className="edu-checklist__ordinal">{item.ordinal}</span>
                <span className="edu-checklist__wording" lang={bn ? 'bn' : 'en'}>
                  {bn ? item.text_bn : item.text_en}
                </span>
                {item.is_critical && (
                  <span className="edu-checklist__critical" title={t('checklist.criticalWhy')}>
                    {t('checklist.critical')}
                  </span>
                )}
                {last && (
                  // History, not a default. The wording says "last time" out loud so that a
                  // glance cannot read it as this visit's answer.
                  <span
                    className="edu-checklist__last"
                    data-state={last.state}
                    data-testid={`last-${item.code}`}
                  >
                    {t('checklist.lastTime', { state: stateLabel(states, last.state, bn) })}
                  </span>
                )}
              </div>
              <div
                className="edu-checklist__states"
                role="radiogroup"
                aria-label={bn ? item.text_bn : item.text_en}
              >
                {states.map((option) => (
                  <button
                    key={option.state}
                    type="button"
                    role="radio"
                    aria-checked={answered === option.state}
                    className="edu-checklist__state"
                    data-state={option.state}
                    data-selected={answered === option.state ? 'true' : undefined}
                    data-testid={`state-${item.code}-${option.state}`}
                    disabled={disabled}
                    onClick={() => onAnswer(item.code, option.state)}
                  >
                    {bn ? option.display_bn : option.display_en}
                  </button>
                ))}
              </div>
            </li>
          );
        })}
      </ol>
    </section>
  );
}

function stateLabel(
  states: EducationReference['states'],
  state: EducationState,
  bn: boolean,
): string {
  const found = states.find((option) => option.state === state);
  // The raw code is the honest fallback, exactly as the dashboard does for an unnamed
  // observation code: a blank where a clinical finding should be is worse than an ugly one.
  if (!found) return state;
  return bn ? found.display_bn : found.display_en;
}
