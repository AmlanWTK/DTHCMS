'use client';

import { useTranslations } from 'next-intl';

import { Button } from '@dthcms/ui';

import { WINDOW_CHOICES, windowIsAcceptable } from '../api/jobs';

/**
 * How far back the rates look (CP69).
 *
 * # Why the window is a control at all
 *
 * Every rate on the health table is over a window, and the right window depends on which
 * question is being asked. *Is this happening now* is fifteen minutes. *Did the queue stop
 * overnight* is a day, and asking it with an hour's window produces a screen full of "nothing
 * ran" that says nothing about whether that is new. The server takes `window_seconds` and the
 * screen has to let somebody use it, or the endpoint's most useful parameter is one only a
 * developer with curl can reach.
 *
 * # Why the choices are filtered against the server's range
 *
 * `GET /v1/ops/jobs/health` **silently clamps** a window outside 60…86400 back to an hour
 * rather than refusing it. That makes an out-of-range request the worst kind of failure — a
 * screen showing correct arithmetic under the wrong caption — so this control cannot be the
 * thing that produces one. The table still renders its sentence from `window_seconds` on the
 * response, so even a clamp arriving from somewhere else would be labelled honestly.
 */
export interface WindowPickerProps {
  windowSeconds: number;
  onChange: (windowSeconds: number) => void;
  /** Disabled while a request is in flight, so a double tap cannot queue two windows. */
  busy?: boolean;
}

export function WindowPicker({ windowSeconds, onChange, busy = false }: WindowPickerProps) {
  const t = useTranslations('jobs');

  return (
    <div
      className="app-jobs__window-picker"
      role="group"
      aria-label={t('window.label')}
      data-testid="window-picker"
    >
      {WINDOW_CHOICES.filter(windowIsAcceptable).map((choice) => (
        <Button
          key={choice}
          variant={choice === windowSeconds ? 'secondary' : 'quiet'}
          aria-pressed={choice === windowSeconds}
          disabled={busy}
          data-testid={`window-${choice}`}
          onClick={() => onChange(choice)}
        >
          {t(`window.choice.${choice}`)}
        </Button>
      ))}
    </div>
  );
}
