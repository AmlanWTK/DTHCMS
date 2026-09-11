'use client';

import { useLocale, useTranslations } from 'next-intl';

import {
  ValueWithAttribution,
  timelineMarkAttribution,
  timelineSeriesPointAttribution,
} from '@/features/attribution';
import { DualUnitValue } from '@/features/observations';
import { formatDate, formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';

import type { ChartHover } from './TimelineChart';
import { codeLabel, flagLabel, laneLabel } from './timelineText';

/**
 * What is under the pointer, and who put it there (CP74, criterion 3).
 *
 * # Why the attribution is `compact` and not a second disclosure
 *
 * CP61's component offers two variants. `disclosure` keeps the name behind one interaction,
 * which is right on a dense list of forty values. Here it would be wrong, and the criterion
 * says why: *hovering any value shows its attribution.* Putting the name behind a second
 * hover inside a panel that only exists because of the first would make the promise two
 * interactions, on a surface whose whole purpose is that it is one.
 *
 * So this is `compact` — the person's name and the source are on screen the moment the panel
 * is — and the panel underneath it still carries the station, the device, both timestamps and
 * the correction, for the reviewer who wants all of it.
 *
 * # Why it goes through that component at all
 *
 * Because there is exactly one place in this application that answers "who entered this",
 * and a chart that drew its own version would be the tenth screen that got it slightly
 * differently — the uuid instead of the name, the current staff list instead of the one that
 * keeps people who have left, a blank where "source not recorded" belongs. §4.2's rule is a
 * rule about a component, and this screen is not an exception to it.
 *
 * # Why the number goes through `DualUnitValue`
 *
 * [R-08] again: the clinical unit with the patient-familiar equivalent beneath it. A tooltip
 * is where a physician reads a number off to say out loud, which makes it the last place to
 * draw one a different way from every other screen.
 *
 * # Why a mark and a point are drawn by one panel and not two
 *
 * They answer the same question at the same moment, and a physician moving the pointer from
 * a medication bar to the line beneath it should not have the answer change shape under
 * them. What differs is the top line — a drug's name against a number and its unit — and
 * that is the only branch below.
 */

export interface TimelineDetailProps {
  hover: ChartHover;
}

export function TimelineDetail({ hover }: TimelineDetailProps) {
  const t = useTranslations('timeline');
  const locale = useLocale() as Locale;

  if (hover.kind === 'mark' && hover.mark !== undefined) {
    const mark = hover.mark;
    const label = locale === 'bn' ? mark.label_bn : mark.label_en;
    return (
      <div className="tl-detail" data-testid="timeline-detail" role="status" aria-live="polite">
        <p className="tl-detail__lane">{laneLabel(hover.laneKey ?? '', t)}</p>

        <ValueWithAttribution
          attribution={timelineMarkAttribution(mark)}
          label={label}
          variant="compact"
          testId="timeline-mark-attribution"
        >
          <span className="tl-detail__label">{label}</span>
        </ValueWithAttribution>

        <p className="tl-detail__when">
          {/* The span in words as well as in the bar's length. A bar's width is a picture of
              a duration and is unreadable when it is four pixels wide, which is what a
              two-week course looks like on a decade. */}
          {mark.ended_at !== undefined
            ? t('ranFromTo', {
                from: formatDate(Date.parse(mark.occurred_at), locale),
                to: formatDate(Date.parse(mark.ended_at), locale),
              })
            : mark.open_ended
              ? t('runningSince', { from: formatDate(Date.parse(mark.occurred_at), locale) })
              : formatDateTime(Date.parse(mark.occurred_at), locale)}
        </p>

        {mark.open_ended && mark.last_seen_at !== undefined && (
          // The honest edge of the bar. Everything to the right of this is the chart's
          // inference that a drug nobody stopped is still being taken, and a reviewer
          // deciding about adherence needs to know where the evidence stops.
          <p className="tl-detail__evidence">
            {t('lastRecorded', { at: formatDate(Date.parse(mark.last_seen_at), locale) })}
          </p>
        )}

        {mark.count > 1 && (
          <p className="tl-detail__count">{t('foldedRows', { count: mark.count })}</p>
        )}

        {mark.value !== undefined && mark.value !== '' && (
          <p className="tl-detail__value">
            {mark.value}
            {mark.unit !== undefined && mark.unit !== '' ? ` ${mark.unit}` : ''}
          </p>
        )}

        {mark.flags.length > 0 && (
          <ul className="tl-detail__flags">
            {mark.flags.map((flag) => (
              <li key={flag} data-flag={flag}>
                {flagLabel(flag, t)}
              </li>
            ))}
          </ul>
        )}
      </div>
    );
  }

  if (hover.kind === 'point' && hover.point !== undefined) {
    const point = hover.point;
    const name = codeLabel(hover.code ?? '', t);
    return (
      <div className="tl-detail" data-testid="timeline-detail" role="status" aria-live="polite">
        <p className="tl-detail__lane">{name}</p>

        <ValueWithAttribution
          attribution={timelineSeriesPointAttribution(point)}
          label={name}
          variant="compact"
          testId="timeline-point-attribution"
        >
          {/*
            CP44's component, not a formatted number. [R-08]: every clinical value shows the
            clinical unit with the patient-familiar equivalent beneath it — 69.9 kg and 154 lb
            — and this is the only thing in the application that does it consistently. A chart
            tooltip is exactly where a physician reads a number off to say out loud, so it is
            the last place to draw one a different way. The repository's own audit caught this
            file rendering the number raw.
          */}
          <DualUnitValue
            value={point.value_num}
            unit={point.unit ?? ''}
            code={hover.code ?? ''}
            size="sm"
          />
        </ValueWithAttribution>

        <p className="tl-detail__when">{formatDateTime(Date.parse(point.at), locale)}</p>

        {point.flags.length > 0 && (
          // A word, before anything else. A point that has been corrected is not the value
          // that stands today, and a reader taking a number off a chart has no other way to
          // know that — the shape of the dot is the second signal, never the only one.
          <ul className="tl-detail__flags">
            {point.flags.map((flag) => (
              <li key={flag} data-flag={flag}>
                {flagLabel(flag, t)}
              </li>
            ))}
          </ul>
        )}
      </div>
    );
  }

  return null;
}
