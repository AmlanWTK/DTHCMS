'use client';

import { useQuery } from '@tanstack/react-query';
import Link from 'next/link';
import { useLocale, useTranslations } from 'next-intl';

import { Skeleton } from '@dthcms/ui';

import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';
import { patientSubroutePath } from '@/lib/navigation';
import { usePermission } from '@/lib/use-permission';

import {
  allergyStateKey,
  getAllergyState,
  hasEmergency,
  isEmergency,
  isUncoded,
  type Allergy,
  type AllergyState,
} from '../api/allergies';

import { reactionName, substanceName } from './allergyText';

/**
 * The strip the patient header carries on every screen (CP54, acceptance criterion 3).
 *
 * # Four statuses, and an empty list that means two opposite things
 *
 * `NONE_RECORDED` and `NO_KNOWN_ALLERGY` both arrive with no allergies in them. One says
 * nobody has asked; the other says somebody asked and was told there are none, and there is
 * a person's name behind that sentence. A strip that drew them the same way — a blank
 * space, a quiet "no allergies" — would be lying, and lying in the direction that looks
 * safe: the prescriber reads "none" and writes the penicillin. So the status word is drawn
 * from `status` and never from the length of the list, the two get different words, a
 * different tone and a different `data-status`, and neither is ever the empty string.
 *
 * `UNABLE_TO_ASSESS` is the third of those and the one most easily mis-drawn. It means
 * somebody looked at an unconscious patient with no attendant and could not get an answer.
 * It satisfies the gate — that is the whole reason there is no override — and it is
 * emphatically not a claim that there are no allergies. It is drawn as distinctly as an
 * allergy is, with its reason on screen, because the reason is what makes it reviewable.
 *
 * # Why the emergency leads, in words
 *
 * The server returns the list worst first — emergency reactions, then by severity — and
 * nothing here re-sorts it. The one that stops a heart is at the top rather than buried
 * under a rash from 1998, and it says **Emergency reaction** as a word before any colour is
 * involved. A tablet held near a window flattens hue, a clinic printer has none at all, and
 * roughly one man in twelve cannot use it. The word survives all three.
 *
 * # Why it says what will clear the gate
 *
 * `satisfied` is the gate's own question, answered by the same server that will refuse the
 * queue insert. When it is false this strip says plainly that the patient cannot go past
 * the history station and names the three answers that would change that — before anybody
 * walks to another station and discovers the refusal there. It offers none of them itself:
 * there is no button here that clears the gate, and there is no fourth answer anywhere.
 *
 * # Why a failure is never silence
 *
 * "This patient has no allergies" and "this screen could not find out" are different
 * sentences and only one of them is safe to imply. An unreadable state says so.
 */
export interface AllergyBannerProps {
  patientId: string;
}

export function AllergyBanner({ patientId }: AllergyBannerProps) {
  const t = useTranslations('allergies');
  const locale = useLocale() as Locale;

  const mayRead = usePermission('allergies.view');

  const state = useQuery({
    queryKey: allergyStateKey(patientId),
    queryFn: () => getAllergyState(patientId),
    enabled: mayRead,
  });

  // Registration does not hold `patient.read.allergies`; the pharmacist deliberately does.
  // Somebody who may not read this is shown nothing rather than an empty strip, which would
  // read as an answer.
  if (!mayRead) return null;

  if (state.isError) {
    return (
      <p className="app-allergy-strip__unavailable" role="status" data-testid="allergy-unreadable">
        {t('unavailable')}
      </p>
    );
  }

  if (state.isPending || !state.data) return <Skeleton height="3rem" />;

  return <AllergyStrip patientId={patientId} state={state.data} locale={locale} />;
}

/** The strip itself, given the state. Separated so the render is pure and testable. */
function AllergyStrip({
  patientId,
  state,
  locale,
}: {
  patientId: string;
  state: AllergyState;
  locale: Locale;
}) {
  const t = useTranslations('allergies');

  const emergency = hasEmergency(state);
  const { status, satisfied, allergies, assertion } = state;

  return (
    <section
      className="app-allergy-strip"
      // Interrupting is right for an emergency allergy on the screen where somebody is
      // about to prescribe, and wrong for everything else — a page that interrupts every
      // time is a page whose interruptions stop meaning anything.
      role={emergency ? 'alert' : 'status'}
      aria-live={emergency ? 'assertive' : 'polite'}
      aria-label={t('stripLabel')}
      data-testid="allergy-strip"
      data-status={status}
      data-satisfied={satisfied}
      data-emergency={emergency}
    >
      <p className="app-allergy-strip__status">
        {status === 'ALLERGIES_RECORDED'
          ? t('status.ALLERGIES_RECORDED', { count: allergies.length })
          : t(`status.${status}`)}
      </p>

      <p className="app-allergy-strip__lede">{t(`statusBody.${status}`)}</p>

      {!satisfied && (
        // The refusal, said before anybody tries. The gate is a trigger on the queue table;
        // this only explains it. There is no control here that would clear it.
        <div className="app-allergy-strip__gate" data-testid="allergy-gate">
          <p className="app-allergy-strip__gate-title">{t('gate.blocked')}</p>
          <p className="app-allergy-strip__gate-body">{t('gate.how')}</p>
        </div>
      )}

      {allergies.length > 0 && (
        <ul className="app-allergy-strip__list">
          {allergies.map((allergy) => (
            <li key={allergy.id}>
              <AllergyLine allergy={allergy} locale={locale} />
            </li>
          ))}
        </ul>
      )}

      {assertion && (
        <p className="app-allergy-strip__assertion" data-testid="allergy-assertion">
          {/* Named when it is not the headline. Recording an allergy does not withdraw an
              earlier "no known allergies" — both are true statements about their own
              moment, and a live allergy simply outranks the assertion — so on a patient
              with allergies this line would otherwise read as the attribution for *them*,
              which is somebody else's name against somebody else's claim. */}
          {assertion.kind === status ? '' : `${t(`status.${assertion.kind}`)} — `}
          {t('assertedBy', {
            at: formatDateTime(Date.parse(assertion.asserted_at), locale),
            who: assertion.asserted_by,
          })}
          {assertion.reason ? ` ${t('assertionReason', { reason: assertion.reason })}` : ''}
        </p>
      )}

      <Link className="app-link" href={patientSubroutePath(patientId, 'allergies')}>
        {t('open')}
      </Link>
    </section>
  );
}

/** One allergy on the strip: the word for the danger first, then what and what it did. */
function AllergyLine({ allergy, locale }: { allergy: Allergy; locale: Locale }) {
  const t = useTranslations('allergies');
  const urgent = isEmergency(allergy);

  return (
    <span
      className="app-allergy-strip__item"
      data-testid={`allergy-line-${allergy.id}`}
      data-emergency={urgent}
    >
      {/* The word, before the tint and before the substance. This is the part that has to
          survive a photograph of the screen. */}
      {urgent && <span className="app-allergy-strip__flag">{t('flag.emergency')}</span>}

      <span className="app-allergy-strip__substance">{substanceName(allergy, locale)}</span>

      {isUncoded(allergy) && (
        // Marked rather than hidden or dressed up. "The yellow tablet from the pharmacy near
        // the bridge" is a real allergy and not a coded one, and only one of those two facts
        // is safe to drop.
        <span className="app-allergy-strip__uncoded">{t('flag.uncoded')}</span>
      )}

      <span className="app-allergy-strip__reaction">{reactionName(allergy, locale)}</span>
      <span className="app-allergy-strip__severity">{t(`severity.${allergy.severity}`)}</span>
      <span className="app-allergy-strip__certainty">{t(`certainty.${allergy.certainty}`)}</span>
    </span>
  );
}
