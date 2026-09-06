'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { AlertBanner, Button, Card, Skeleton } from '@dthcms/ui';

import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';
import { isKnownRole } from '@/lib/permissions';
import { usePermission } from '@/lib/use-permission';

import {
  counselingGateKey,
  gateState,
  getCounselingGate,
  missingByRemediation,
  skippedAtGrant,
  type CounselingGateOverride,
  type CounselingMissingItem,
} from '../api/gate';

import { OverrideGate } from './OverrideGate';
import { missingChecklistTitle, missingLabel, missingRoomName, staffLabel } from './panelText';

/**
 * What the counselling checkpoint says about this visit, and what to do about it
 * (CP57, §5.5, criterion 2).
 *
 * # `overridden` is not `covered`, and this component is where that is kept true
 *
 * The gate reports two booleans. `blocked` is what the queue will do; `overridden` is why it
 * will not. A screen that drew "not blocked" one way would show a patient somebody waved
 * past exactly as it shows a patient whose counselling is finished — and would be telling a
 * physician the counselling was done. So there are three states here, they are three
 * different banners with three different tones and three different sentences, and the
 * overridden one leads with the fact that the checklist was **not** completed. What was
 * outstanding at the moment of the grant is listed underneath, from `missing_at_grant`, which
 * is a record of that moment rather than of now.
 *
 * # Why the missing items are two lists
 *
 * `session_id` present means somebody opened the checklist and stopped part way: there is a
 * session to go back to and a room to send the patient to. `session_id` absent means nobody
 * opened that checklist at all — the patient's record calls for it and no counsellor has
 * begun. Those are different people, different rooms and different sentences. One list would
 * send half of these patients to a counsellor who has nothing open for them.
 *
 * # Why the override button is only on a gate that is holding
 *
 * The server refuses an override when nothing is outstanding, with `422` against `reason`,
 * because a row in the rate view for an override that was not needed makes the rate lie. A
 * control that exists in order to be refused teaches an operator that the software is
 * unreliable, so it is not rendered — the same reasoning as every other permission-gated
 * control in this application, applied to state rather than to permission.
 */

export interface GateStateProps {
  visitId: string;
  /**
   * Whether the visit is still open.
   *
   * The override is hidden on a closed visit. Sending a patient past a checkpoint is an act
   * about a patient who is in the building and waiting to be seen; recorded against a visit
   * that ended last month it is a row in the rate view that describes nothing that happened.
   */
  visitOpen: boolean;
}

export function GateState({ visitId, visitOpen }: GateStateProps) {
  const t = useTranslations('counseling');
  const locale = useLocale() as Locale;
  const mayOverride = usePermission('counseling.gate.override');

  const [overriding, setOverriding] = useState(false);

  const gate = useQuery({
    queryKey: counselingGateKey(visitId),
    queryFn: () => getCounselingGate(visitId),
  });

  if (gate.isPending) return <Skeleton height="10rem" />;

  if (gate.isError || !gate.data) {
    // Critical, and worded so it cannot be read as reassurance. An unreadable gate and a
    // gate with nothing outstanding look the same as an absence, and one of them means a
    // patient is being held at a station with nobody able to say why.
    return (
      <AlertBanner tone="critical" title={t('panel.gate.unavailable')}>
        {t('panel.gate.unavailableBody')}
      </AlertBanner>
    );
  }

  const state = gateState(gate.data);
  const { unfinished, notStarted } = missingByRemediation(gate.data);

  return (
    <section
      className="app-counseling-panel__gate"
      aria-label={t('panel.gate.title')}
      data-testid="counseling-gate"
      data-state={state}
      data-blocked={gate.data.blocked}
      data-overridden={gate.data.overridden}
    >
      {state === 'blocked' && (
        <AlertBanner tone="critical" title={t('panel.gate.blocked')}>
          {t('panel.gate.blockedBody')}
        </AlertBanner>
      )}

      {state === 'overridden' && (
        // Borderline rather than normal, and the title says what happened rather than that
        // nothing is in the way. This is the banner the whole `overridden` field exists for.
        <AlertBanner tone="borderline" title={t('panel.gate.overridden')}>
          {t('panel.gate.overriddenBody')}
        </AlertBanner>
      )}

      {state === 'clear' && (
        <AlertBanner tone="normal" title={t('panel.gate.clear')}>
          {t('panel.gate.clearBody')}
        </AlertBanner>
      )}

      {gate.data.override && (
        <OverrideRecord override={gate.data.override} missing={gate.data.missing} locale={locale} />
      )}

      {unfinished.length > 0 && (
        <MissingList
          items={unfinished}
          locale={locale}
          kind="unfinished"
          title={t('panel.gate.missing.unfinishedTitle')}
          body={t('panel.gate.missing.unfinishedBody')}
        />
      )}

      {notStarted.length > 0 && (
        <MissingList
          items={notStarted}
          locale={locale}
          kind="notStarted"
          title={t('panel.gate.missing.notStartedTitle')}
          body={t('panel.gate.missing.notStartedBody')}
        />
      )}

      {mayOverride && visitOpen && state === 'blocked' && !overriding && (
        <div className="app-counseling-panel__gate-actions">
          <Button
            variant="secondary"
            data-testid="open-override"
            onClick={() => setOverriding(true)}
          >
            {t('panel.override.action')}
          </Button>
        </div>
      )}

      {overriding && (
        <OverrideGate
          visitId={visitId}
          onGranted={() => setOverriding(false)}
          onCancel={() => setOverriding(false)}
        />
      )}
    </section>
  );
}

/**
 * One remediation path's worth of outstanding items.
 *
 * Each line names the item in words, the checklist in words, and the room it belongs to,
 * because "go back and finish the counselling" is not an instruction anybody can act on and
 * "insulin technique, Diabetes counselling, insulin corner" is. All three arrive on the
 * missing item itself, so a refusal never waits on a second request to say which room.
 *
 * The order is the server's — by checklist, then by the room's place in §5.2's walk, then by
 * item code, in `core.counseling_gate_missing`'s own `ORDER BY`. Nothing re-sorts here: a
 * second opinion about the order a refusal reads in is one that would disagree quietly.
 */
function MissingList({
  items,
  locale,
  kind,
  title,
  body,
}: {
  items: readonly CounselingMissingItem[];
  locale: Locale;
  kind: 'unfinished' | 'notStarted';
  title: string;
  body: string;
}) {
  const t = useTranslations('counseling');

  return (
    <div className="app-counseling-panel__missing" data-testid={`missing-${kind}`}>
      <h3 className="app-counseling-panel__heading">{title}</h3>
      <p className="app-page__description">{body}</p>

      <ul className="app-counseling-panel__missing-list">
        {items.map((item) => {
          const label = missingLabel(item, locale);
          return (
            <li
              key={`${item.template_id}-${item.item_code}`}
              data-testid={`missing-item-${item.item_code}`}
              data-template={item.template_code}
            >
              <span className="app-counseling-item__text" lang={label?.lang ?? locale}>
                {label?.text ?? t('panel.gate.missing.noText')}
              </span>{' '}
              <span className="app-counseling-panel__room">
                {t('panel.gate.missing.inRoom', { room: missingRoomName(item, locale) })}
              </span>{' '}
              <span className="app-counseling-panel__checklist">
                {t('panel.gate.missing.checklist', {
                  title: missingChecklistTitle(item, locale),
                })}
              </span>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

/**
 * The override that stands on this visit: who, when, why, and what was skipped then.
 *
 * All four, because each answers a question the others cannot. "Who" is the accountability;
 * "why" is what the person reviewing override rates reads and the only thing that keeps the
 * valve honest; "what was skipped" is the clinical content of the decision.
 *
 * The skipped items are named in words where the gate still knows them, and by code where it
 * does not — which happens when somebody covered the item after the override was granted.
 * That is the honest answer: `missing_at_grant` records the moment, and covering an item
 * afterwards does not make the override retrospectively unnecessary.
 */
function OverrideRecord({
  override,
  missing,
  locale,
}: {
  override: CounselingGateOverride;
  missing: readonly CounselingMissingItem[];
  locale: Locale;
}) {
  const t = useTranslations('counseling');
  const tRole = useTranslations('role');

  // The person, then the role beside it. An override is the one act on this panel with
  // somebody's judgement in it, and "the chief consultant did this" is a description of a
  // post rather than of the colleague a reviewer will go and ask about it.
  const who = staffLabel(
    {
      code: override.granted_by_code,
      name_en: override.granted_by_name_en,
      name_bn: override.granted_by_name_bn,
      role: override.granted_role,
      id: override.granted_by,
    },
    locale,
  );
  const roleLabel =
    who.role === null ? undefined : isKnownRole(who.role) ? tRole(who.role) : who.role;

  const skipped = skippedAtGrant(override, missing);

  return (
    <Card elevation="flat" className="app-counseling-panel__override">
      <article data-testid="override-record">
        <h3 className="app-counseling-panel__heading">{t('panel.override.standing')}</h3>

        <p data-testid="override-granted">
          {t('panel.override.grantedAt', {
            when: formatDateTime(Date.parse(override.granted_at), locale),
          })}
        </p>

        <p data-testid="override-by">
          {/* Name first where there is one; the role alone where the staff record has gone,
              and a sentence saying so rather than a blank, because a blank where a person's
              name belongs reads as a rendering fault rather than as a fact. */}
          {who.name !== null && roleLabel !== undefined
            ? t('panel.override.byPerson', { name: who.name, role: roleLabel })
            : who.name !== null
              ? t('panel.override.byName', { name: who.name })
              : roleLabel !== undefined
                ? t('panel.override.byRole', { role: roleLabel })
                : t('panel.override.byUnknownRole')}
        </p>

        <p className="app-counseling-panel__staff">
          {/* The stable identifier, and only one of them. The names are joined from the staff
              record, so they follow somebody who changes their name; the code is what does
              not move, and it is what a reviewer quotes. The uuid appears only when there is
              no code to show. */}
          {who.code !== null ? (
            <>
              <span>{t('panel.override.staffCode')}</span> <code>{who.code}</code>
            </>
          ) : (
            <>
              <span>{t('panel.override.staffId')}</span> <code>{override.granted_by}</code>
            </>
          )}
        </p>

        <p data-testid="override-reason">
          {t('panel.override.reason', { reason: override.reason })}
        </p>

        <h4 className="app-counseling-panel__subheading">{t('panel.override.skippedTitle')}</h4>
        <p className="app-page__description">{t('panel.override.skippedBody')}</p>

        {skipped.length === 0 ? (
          // The server refuses an override with nothing outstanding, so this list is never
          // empty in practice. Said rather than rendered as a heading with nothing under it,
          // because a heading with nothing under it reads as a failed load.
          <p data-testid="override-skipped-empty">{t('panel.override.skippedNone')}</p>
        ) : (
          <ul className="app-counseling-panel__missing-list" data-testid="override-skipped">
            {skipped.map((entry) => {
              const label = entry.item ? missingLabel(entry.item, locale) : null;
              return (
                <li key={entry.itemCode} data-testid={`override-skipped-${entry.itemCode}`}>
                  <span className="app-counseling-item__text" lang={label?.lang ?? locale}>
                    {label?.text ?? entry.itemCode}
                  </span>
                  {entry.item && (
                    <>
                      {' '}
                      <span className="app-counseling-panel__room">
                        {t('panel.gate.missing.inRoom', {
                          room: missingRoomName(entry.item, locale),
                        })}
                      </span>
                    </>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </article>
    </Card>
  );
}
