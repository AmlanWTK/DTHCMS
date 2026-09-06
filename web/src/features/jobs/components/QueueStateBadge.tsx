'use client';

import { useTranslations } from 'next-intl';

import { Icon, type IconName } from '@dthcms/ui';

import type { QueueState } from '../api/jobs';

/**
 * One row's state, said three ways at once (CP69).
 *
 * A colour, an icon and a **word**, the same discipline the traffic board and every clinical
 * status in this system follow, and for the same two reasons: red and orange converge under
 * deuteranopia for roughly one man in twelve who will work in this clinic, and this screen is
 * read on a cheap tablet in a corridor and photographed into a group chat at two in the
 * morning. A dashboard that carried its meaning in hue alone would be carrying it nowhere.
 *
 * The eight states and their order are argued in `api/jobs.ts`. Two of them are worth naming
 * here because they are the ones an ordinary dashboard gets wrong:
 *
 * **`paused` is not a fault colour.** It is violet — the one hue on this page that is not on
 * the green-amber-red arc — because a paused kind is not going wrong, it has been *stopped*,
 * and drawing it in red would teach an operator to dismiss it as noise during the incident
 * they paused it for. It is unmistakable by being the only thing on the page that colour,
 * and the row says so again in a sentence.
 *
 * **`quiet` is grey, not green.** Nothing queued, nothing running, nothing finished in the
 * window — which is the shape of a healthy idle kind and equally the shape of a queue that
 * has stopped being fed. The row declines to guess, which is the whole reason `failure_rate`
 * is null rather than zero in the payload it came from.
 *
 * **`abandoned` is amber and not red.** Nothing is going wrong at this moment; jobs are
 * sitting where a policy left them, waiting for a person. Drawing it in the same red as a
 * queue that is failing right now would make the two indistinguishable at a glance, and the
 * acts they call for are different — one is *find out what is breaking*, the other is
 * *press retry*.
 */

const ICONS: Record<QueueState, IconName> = {
  paused: 'x',
  stuck: 'clock',
  failing: 'octagon-alert',
  abandoned: 'inbox',
  late: 'alert-triangle',
  never: 'wifi-off',
  quiet: 'help-circle',
  working: 'loader-circle',
  healthy: 'check',
};

export interface QueueStateBadgeProps {
  state: QueueState;
}

export function QueueStateBadge({ state }: QueueStateBadgeProps) {
  const t = useTranslations('jobs');

  return (
    <span className="app-jobs__state" data-state={state} data-testid={`state-${state}`}>
      <Icon name={ICONS[state]} size={14} />
      <span>{t(`state.${state}`)}</span>
    </span>
  );
}
