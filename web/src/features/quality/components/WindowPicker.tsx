'use client';

import { useTranslations } from 'next-intl';

import { Button } from '@dthcms/ui';

import { WINDOW_CHOICES, windowIsAcceptable } from '../api/quality';

/**
 * How much of the past a record covers (CP63).
 *
 * # Why buttons rather than a date range
 *
 * The server takes a whole number of days ending now, and that is the shape of the question
 * people actually ask: *is this still happening this week*, *what did last month look like*.
 * An arbitrary date range is a report, and a report is a thing somebody builds to make a
 * case. Four fixed windows cannot be tuned until they say what somebody wanted them to say.
 *
 * # Why the window is always stated on the screen and never only implied
 *
 * A count means nothing without knowing what it is a count of, and "over what period" is half
 * of that. The buttons say which window is showing, and the record beside them prints the two
 * dates in full — so a screenshot of this screen, which is how these numbers actually travel
 * between people, carries its own period with it.
 *
 * # Why the choices are filtered against the server's range
 *
 * A window outside 1–365 is now refused with `422` rather than quietly replaced with thirty,
 * which is the right call — a client asking for fourteen days and being handed thirty has two
 * windows pretending to be one. The filter is not defensive clutter: it means this control
 * cannot be the thing that produces that refusal, so a 422 reaching a screen is a real
 * disagreement with the server rather than a button doing what it was built to do.
 *
 * Every button is a `Button` at the default size, which the design system holds at the 48px
 * touch target: this is used on cheap tablets, standing up, one-handed.
 */
export interface WindowPickerProps {
  days: number;
  onChange: (days: number) => void;
  /** Disabled while a request is in flight, so a double tap cannot queue two windows. */
  busy?: boolean;
}

export function WindowPicker({ days, onChange, busy = false }: WindowPickerProps) {
  const t = useTranslations('quality');

  return (
    <div
      className="app-quality__window-picker"
      role="group"
      aria-label={t('window.label')}
      data-testid="window-picker"
    >
      {WINDOW_CHOICES.filter(windowIsAcceptable).map((choice) => (
        <Button
          key={choice}
          variant={choice === days ? 'secondary' : 'quiet'}
          aria-pressed={choice === days}
          disabled={busy}
          data-testid={`window-${choice}`}
          onClick={() => onChange(choice)}
        >
          {t(`window.days.${choice}`)}
        </Button>
      ))}
    </div>
  );
}
