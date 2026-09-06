'use client';

import { useLocale, useTranslations } from 'next-intl';

import { staffLabel, useDirectory } from '@/features/attribution';
import type { Locale } from '@/lib/i18n/config';
import { isKnownRole } from '@/lib/permissions';

/**
 * A colleague named on a correction request (CP62, criterion 5).
 *
 * A correction has up to three people on it — whoever raised the flag, whoever it was routed
 * to, and whoever answered — and the contract names them three different ways. The first two
 * arrive with their names joined onto the row; the third arrives as a uuid and a role, because
 * the projection stores who answered and the read does not join a second staff record for it.
 * One component, so that a screen cannot name the flagger one way and the person who answered
 * another for the same request.
 *
 * # Why the row's own names win over the directory
 *
 * They are the same names — both come from the staff record — but the row's were joined by the
 * server for this read and cannot be missing while the directory request is still in flight.
 * Using the directory first would make a physician watch three names appear one after another
 * on a screen that already had them.
 *
 * # Why no uuid ever reaches the screen
 *
 * An unresolved id renders as the role plus a sentence saying the record could not be read. It
 * does not render as the uuid. A uuid answers a different question from the one being asked, it
 * cannot be read aloud, and a reviewer who copies one down has copied down something no
 * colleague on the floor can use. The consequence is accepted: on a request whose answerer is
 * not in the directory there is nothing on screen to quote, and the honest reading of that is
 * that the directory is the thing to fix.
 *
 * # Why the role stays beside the name
 *
 * The role says which hat the person was wearing when they did this, which is a property of
 * the act rather than of them — and it is the only half that survives when the staff record
 * cannot be read at all.
 */
export interface PersonLineProps {
  /** The names joined onto the row, where this read carries them. */
  nameEn?: string;
  nameBn?: string;
  /** The staff code — the identifier that does not move when somebody's name does. */
  code?: string;
  role?: string;
  /** The uuid, resolved through the directory only when the row carries no name. */
  id?: string;
  testId?: string;
}

export function PersonLine({ nameEn, nameBn, code, role, id, testId }: PersonLineProps) {
  const t = useTranslations('corrections');
  // The house phrase for naming a person — "{name} — {role}" — borrowed from CP61 rather than
  // written again here. A second copy of it is how one screen comes to say "Rina Akter, Clinical
  // counselor" where another says "Rina Akter — Clinical counselor" about the same act.
  const tAttribution = useTranslations('attribution');
  const tRole = useTranslations('role');
  const locale = useLocale() as Locale;
  const lookup = useDirectory();

  const carried = staffLabel({ name_en: nameEn, name_bn: nameBn, code, role }, locale);
  const resolved = carried.name === null && id !== undefined ? lookup.person(id) : null;

  const name = carried.name ?? resolved?.name ?? null;
  const staffCode = carried.code ?? resolved?.code ?? null;
  const roleText =
    carried.role === null ? null : isKnownRole(carried.role) ? tRole(carried.role) : carried.role;

  // Why there is no name, when there is no name. Still loading, looked up and not listed, and
  // a directory that could not be read are three different situations, and a reviewer deciding
  // whether to walk down the corridor and ask somebody needs to know which one this is.
  const aside =
    name !== null || id === undefined
      ? null
      : lookup.state === 'loading'
        ? t('person.resolving')
        : lookup.state === 'unavailable'
          ? t('person.namesUnavailable')
          : t('person.notListed');

  return (
    <span className="app-corrections__person" data-testid={testId ?? 'correction-person'}>
      <span className="app-corrections__person-name">
        {name !== null && roleText !== null
          ? tAttribution('byPerson', { name, role: roleText })
          : name !== null
            ? // A name with no role is the name and nothing else. There is no message for it on
              // purpose: a message whose whole body is `{name}` is a translation of nothing.
              name
            : roleText !== null
              ? roleText
              : t('person.unknown')}
      </span>

      {staffCode !== null && (
        <span className="app-corrections__person-code">
          {t('person.staffCode')} <code>{staffCode}</code>
        </span>
      )}

      {aside !== null && <span className="app-corrections__person-aside">{aside}</span>}

      {resolved !== null && resolved.status !== 'active' && (
        // "No longer at the clinic" rather than sending somebody to look for a colleague who
        // has gone. A fact about the roster, not about the correction or the person.
        <span className="app-corrections__person-presence">
          {resolved.status === 'deactivated' || resolved.status === 'suspended'
            ? t(`person.presence.${resolved.status}`)
            : // A status this build has not heard of renders as itself. Rounding an unknown one
              // up to "still here" would send somebody looking for a colleague who has left.
              t('person.presence.other', { status: resolved.status })}
        </span>
      )}
    </span>
  );
}
