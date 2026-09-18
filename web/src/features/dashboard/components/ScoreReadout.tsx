'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale } from 'next-intl';

import {
  EDUCATION_REFERENCE_KEY,
  ScoreFace,
  anchorFor,
  readEducationReference,
} from '@/features/education';
import type { Locale } from '@/lib/i18n/config';

/**
 * An improvement score, drawn beside the measurements (CP88 §4).
 *
 * # Why the score is not drawn by `DualUnitValue`
 *
 * [R-08] is about measurements: a clinical unit with the patient-familiar equivalent beneath,
 * because a clinician thinks in one and a patient thinks in the other. A 1–10 score has no
 * second unit and no unit at all — it is stored against the dimensionless dimension so that the
 * database can enforce its band — and putting it through the dual-unit component would draw
 * "7.00" with an empty unit beside it, which is a number pretending to be a measurement.
 *
 * What the score needs instead is its **band**: seven is "a little better", and that is the
 * sentence a physician reads. So it is drawn as the numeral over the scale's maximum, with the
 * band's words and the same face the patient pointed at. One rendering of one scale, from the
 * same reference data the selector uses — two renderings is how a patient comes to point at a
 * different face on two screens.
 *
 * # Why a missing scale is not a missing number
 *
 * The band comes from `/v1/education/reference`, which this screen may not have loaded yet and
 * which a physician on a bad connection may not get at all. The numeral is drawn either way. A
 * score whose band is unavailable is still a score; a blank where a score should be is a screen
 * a physician mistrusts.
 */

export interface ScoreReadoutProps {
  value: number;
  size?: 'sm' | 'md';
}

export function ScoreReadout({ value, size = 'sm' }: ScoreReadoutProps) {
  const locale = useLocale() as Locale;
  const bn = locale === 'bn';

  const reference = useQuery({
    queryKey: EDUCATION_REFERENCE_KEY,
    queryFn: readEducationReference,
    staleTime: 60 * 60 * 1000,
  });

  const scale = reference.data?.score_scale;
  const anchor = scale ? anchorFor(scale, value) : null;

  return (
    <span className="dash-score" data-size={size} data-testid="score-readout">
      {anchor && <ScoreFace rank={anchor.face_rank} ranks={scale?.anchors.length ?? 5} size={22} />}
      <span className="dash-score__number">
        {value}
        {scale && <span className="dash-score__of">/{scale.max_value}</span>}
      </span>
      {anchor && (
        <span className="dash-score__band" data-rank={anchor.face_rank}>
          {bn ? anchor.label_bn : anchor.label_en}
        </span>
      )}
    </span>
  );
}
