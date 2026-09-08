'use client';

import { useLocale, useTranslations } from 'next-intl';

import { Icon } from '@dthcms/ui';

import type { Locale } from '@/lib/i18n/config';

import type { DashboardOmission } from '../api/dashboard';

/**
 * A panel the caller was not given, drawn as an absence rather than as an emptiness (CP73).
 *
 * # The one failure this component exists to prevent
 *
 * *"No active conditions"* and *"you were not shown the active conditions"* are opposite
 * facts, and a screen that drew a withheld panel as an empty one would be stating the first
 * while meaning the second. That is the same class of error as CP54's allergy status — an
 * absence that reads as reassurance — and it is the reason the server sends an `omitted` list
 * instead of simply leaving fields out.
 *
 * # Why the permission is named
 *
 * The remedy for a withheld panel is a grant, and the person who can make one needs to be
 * told which. A note reading "you may not see this" and nothing else sends somebody to an
 * administrator with no question to ask; `history.read` is a thing an administrator can act
 * on in one row.
 *
 * The permission is deliberately *not* named when the server did not send one — that is the
 * other state this component carries, a panel that could not be read at all — and the
 * sentence differs. "Nothing has been hidden from you" is worth saying in that case, because
 * a reader who has just been told a panel is missing will otherwise assume the first reason.
 *
 * # Why the sentence comes from the server
 *
 * Both languages, composed where the decision was made. A client that wrote its own sentence
 * for each panel would have one per panel per language, drifting from what the server
 * actually did — and the server is the only party that knows whether a panel was refused or
 * merely unreadable.
 */

export interface WithheldNoteProps {
  omission: DashboardOmission;
}

/**
 * The message key for a panel name.
 *
 * The server names a panel by its JSON path — `summary`, `access.break_glass`,
 * `trends.hba1c` — and a dot is how `next-intl` expresses nesting, so those paths cannot be
 * message keys as they stand. They are flattened here rather than renamed on the server,
 * because the panel identifier's job is to tell a *client developer* which field is missing
 * and a path does that better than a slug.
 *
 * A panel this build has no name for falls back to a generic one rather than rendering the
 * raw identifier: the sentence beneath is what the reader needs, and `trends.bp_systolic` in
 * a heading is a database identifier shown to a physician. That fallback is the reason this
 * is a function and not a string replacement at the call site — a panel added on the server
 * next month has to draw correctly on a client that has never heard of it.
 */
export function panelKey(panel: string, known: (key: string) => boolean): string {
  const flattened = panel.replaceAll('.', '_');
  return known(`panelName.${flattened}`) ? flattened : 'unknown';
}

export function WithheldNote({ omission }: WithheldNoteProps) {
  const t = useTranslations('dashboard');
  const locale = useLocale() as Locale;

  return (
    <section
      className="dash-card dash-card--withheld"
      data-testid={`withheld-${omission.panel}`}
      data-panel={omission.panel}
      // `status` rather than `alert`: a panel somebody may not see is not an emergency, and a
      // screen that interrupted for each one would interrupt four times for a pharmacist.
      role="status"
    >
      <h3 className="dash-card__title">
        <Icon name="shield-check" aria-hidden />{' '}
        {t(`panelName.${panelKey(omission.panel, (key) => t.has(key))}`)}
      </h3>
      <p className="dash-card__withheld-reason">
        {locale === 'bn' ? omission.reason_bn : omission.reason_en}
      </p>
      {omission.permission ? (
        <p className="dash-card__withheld-permission">
          {t('withheld.permission', { permission: omission.permission })}
        </p>
      ) : null}
    </section>
  );
}
