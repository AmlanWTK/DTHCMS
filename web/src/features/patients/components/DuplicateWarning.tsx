'use client';

import { useLocale, useTranslations } from 'next-intl';

import { AlertBanner, Badge, Button, Card } from '@dthcms/ui';

import type { DuplicateCandidate, DuplicateMatch } from '../api/patients';
import { reasonText } from './reasonText';

/**
 * What the desk sees when somebody may already be registered (CP30).
 *
 * The whole design problem here is the *second* kind of warning. A blocked match is easy:
 * this identity number belongs to that record, look at it. A review match is hard, because
 * a Bangladeshi register legitimately contains many people named Md Rahim born in 1980, and
 * the officer will see this panel several times a morning. If it looks like an error, they
 * will learn to dismiss it; if it is too quiet, they will not read it.
 *
 * So three things are deliberate:
 *
 *   - **Not merging is one click and needs no reason.** The primary action on a review
 *     warning is "these are different people", and it is the button the eye lands on. The
 *     plan asks for exactly this, because Bangladeshi naming produces many true near-matches
 *     and a UI that makes *not* merging awkward produces wrong merges.
 *   - **The reasons are sentences, not a score.** "The name sounds the same and the birth
 *     year matches" is something an officer can check against the person in front of them.
 *     0.71 is not.
 *   - **Telephone numbers are masked.** This panel is read at a desk with the patient
 *     standing at it, and whoever is next in the queue can see the screen.
 */
export function DuplicateWarning({
  match,
  onOpen,
  onDismiss,
  dismissed,
}: {
  match: DuplicateMatch;
  /** Open one candidate's record side by side. */
  onOpen?: (candidate: DuplicateCandidate) => void;
  /** "These are different people." */
  onDismiss?: () => void;
  dismissed?: boolean;
}) {
  const t = useTranslations('patients.duplicates');

  if (match.verdict === 'clear' || match.candidates.length === 0) return null;

  const blocked = match.verdict === 'blocked';

  return (
    <section
      className="app-duplicates"
      aria-label={blocked ? t('blockedTitle') : t('reviewTitle')}
      data-verdict={match.verdict}
      data-dismissed={dismissed ? 'true' : undefined}
    >
      <AlertBanner
        tone={blocked ? 'critical' : 'high'}
        title={blocked ? t('blockedTitle') : t('reviewTitle')}
      >
        {blocked ? t('blockedBody') : t('reviewBody', { count: match.candidates.length })}
      </AlertBanner>

      <ul className="app-duplicates__list">
        {match.candidates.map((candidate) => (
          <li key={candidate.patient_id}>
            <CandidateCard candidate={candidate} onOpen={onOpen} />
          </li>
        ))}
      </ul>

      {!blocked && onDismiss ? (
        <div className="app-duplicates__decide">
          {/* The primary action, on purpose: most near-matches in this register are two
              different people, and the officer must be able to say so without friction. */}
          <Button variant="primary" onClick={onDismiss} disabled={dismissed}>
            {dismissed ? t('dismissed') : t('different')}
          </Button>
          <p className="app-duplicates__hint">{t('differentHint')}</p>
        </div>
      ) : null}
    </section>
  );
}

function CandidateCard({
  candidate,
  onOpen,
}: {
  candidate: DuplicateCandidate;
  onOpen?: (candidate: DuplicateCandidate) => void;
}) {
  const t = useTranslations('patients.duplicates');
  const locale = useLocale();

  return (
    <Card elevation="flat" className="app-duplicates__card">
      <header className="app-duplicates__head">
        <div>
          <p className="app-duplicates__name">{candidate.name_en}</p>
          {candidate.name_bn ? (
            <p className="app-duplicates__name-bn">{candidate.name_bn}</p>
          ) : null}
        </div>
        <Badge tone={candidate.deterministic ? 'brand' : 'neutral'}>{candidate.clinical_id}</Badge>
      </header>

      <dl className="app-duplicates__facts">
        <div>
          <dt>{t('birthDate')}</dt>
          <dd>{candidate.birth_date}</dd>
        </div>
        <div>
          <dt>{t('sex')}</dt>
          <dd>{t(`sexes.${candidate.sex}`)}</dd>
        </div>
        <div>
          <dt>{t('phone')}</dt>
          <dd>{candidate.phone_masked}</dd>
        </div>
        <div>
          <dt>{t('district')}</dt>
          <dd>{candidate.district || '—'}</dd>
        </div>
      </dl>

      <ul className="app-duplicates__reasons">
        {candidate.reasons.map((reason) => (
          <li key={reason.code}>{reasonText(reason, locale)}</li>
        ))}
      </ul>

      {onOpen ? (
        <Button variant="quiet" size="sm" onClick={() => onOpen(candidate)}>
          {t('open')}
        </Button>
      ) : null}
    </Card>
  );
}
