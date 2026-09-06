'use client';

import { useTranslations } from 'next-intl';

import type { components } from '@dthcms/api-client';

import { ValueWithAttribution, observationAttribution } from '@/features/attribution';
import type { AttributionVariant } from '@/features/attribution';

import { DualUnitValue } from './DualUnitValue';

type Observation = components['schemas']['Observation'];

/**
 * One recorded value, drawn the one way this application draws a recorded value (CP62).
 *
 * # Why this component exists at all
 *
 * Two rules already govern every clinical number on screen, and until CP62 nothing on the web
 * read a raw observation, so nothing had to satisfy both at once. [R-08] says a measurement
 * shows the clinical unit with the patient-familiar equivalent beneath it — `DualUnitValue`.
 * §4.2 says a reviewer sees who entered it without digging — `ValueWithAttribution`. CP62's
 * chain view draws the same value in four places (the chain, the derived list, the queue, the
 * correction form), and four call sites each remembering to nest one component inside the
 * other is three chances to draw a number with nobody's name against it.
 *
 * So the nesting is decided once, here, and the rest of this checkpoint hands over an
 * observation and gets back a value that is both.
 *
 * # Why it switches on `value_type` rather than on whether `value` is a number
 *
 * A code is numeric, or text, or boolean, or coded, and never two of them; `value_type` is
 * the record's own statement of which. Reaching for `value` and falling back to `value_text`
 * would draw a boolean `false` as "not recorded", because `false` and absent are
 * indistinguishable to a truthiness check — and "the patient does not smoke" reported as a
 * gap in the record is a clinical fact deleted by a rendering shortcut.
 *
 * A shape this build has no rendering for says so in a sentence rather than drawing nothing.
 * A blank where a value belongs reads as a value nobody recorded, which is a different and
 * much worse claim than "this screen cannot show this kind of value".
 */
export interface ObservationValueProps {
  observation: Observation;
  /**
   * What this value is — "Height", "Body mass index".
   *
   * Names the attribution control. A chain of six heights otherwise offers a screen reader
   * six buttons all called "Who entered this value", which is six ways of not answering.
   */
  label?: string;
  variant?: AttributionVariant;
  testId?: string;
}

export function ObservationValue({ observation, label, variant, testId }: ObservationValueProps) {
  return (
    <ValueWithAttribution
      attribution={observationAttribution(observation)}
      label={label}
      variant={variant}
      testId={testId ?? 'observation-value'}
    >
      <Shape observation={observation} label={label} />
    </ValueWithAttribution>
  );
}

/** The value itself, in whichever of the contract's five shapes this code uses. */
function Shape({ observation, label }: { observation: Observation; label?: string }) {
  const t = useTranslations('observations');

  switch (observation.value_type) {
    case 'numeric':
      return (
        <DualUnitValue
          value={observation.value}
          unit={observation.unit ?? ''}
          code={observation.code}
          label={label}
        />
      );

    case 'text': {
      const text = (observation.value_text ?? '').trim();
      // An empty text value is drawn as "not recorded" rather than as an empty line, for the
      // same reason DualUnitValue does it: a blank could be either, and a clinician has to go
      // and check which.
      return (
        <span className="app-observation__text" data-testid="observation-text">
          {text === '' ? t('notRecorded') : text}
        </span>
      );
    }

    case 'boolean':
      return (
        <span className="app-observation__text" data-testid="observation-boolean">
          {observation.value_bool === undefined
            ? t('notRecorded')
            : observation.value_bool
              ? t('yes')
              : t('no')}
        </span>
      );

    case 'coded': {
      const code = (observation.value_code ?? '').trim();
      return (
        <span className="app-observation__text" data-testid="observation-coded">
          {code === '' ? t('notRecorded') : code}
        </span>
      );
    }

    default:
      // `structured` today, and whatever the contract adds next. Said out loud: a screen that
      // silently drew nothing for a shape it had not been taught would look like a screen
      // where nobody had recorded anything.
      return (
        <span className="app-observation__text" data-testid="observation-unshowable">
          {t('shapeNotShown')}
        </span>
      );
  }
}
