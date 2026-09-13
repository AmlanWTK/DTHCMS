'use client';

import { useTranslations } from 'next-intl';

/**
 * The re-education flag, as the officer sees it (CP92 §5, criterion 3).
 *
 * # Why it says *why* and not only *that*
 *
 * "Re-education needed" on its own is a verdict, and an officer who cannot see how it was
 * reached will either argue with it or ignore it. The tally — how many the patient could not do,
 * how many were corrected, and what the threshold is — is the working, and it is the same
 * working the record holds, returned by the server rather than recomputed here.
 *
 * # Why it appears when it is *not* raised as well
 *
 * A notice that only ever appears in the bad case teaches an officer that its absence means
 * nothing was checked. A clean assessment says so, quietly, in the same place — which is also
 * what the record does: the flag is written false rather than omitted, so that "this patient's
 * technique is fine" and "nobody has assessed this patient" are different rows.
 *
 * # The prior-visit case is a different sentence
 *
 * A flag standing from an earlier visit is a fact about the past, and the physician reading this
 * screen at the next consultation needs it to say so. Drawing it identically to a flag raised
 * today would make a patient who was flagged three months ago and has demonstrated correctly
 * since look like one who failed this morning.
 */

export interface ReeducationNoticeProps {
  /** Raised by this assessment. */
  raised: boolean;
  /** Standing from an earlier visit, when this assessment has not been recorded yet. */
  standing?: boolean;
  unable: number;
  correctedToday: number;
  threshold: number;
}

export function ReeducationNotice({
  raised,
  standing = false,
  unable,
  correctedToday,
  threshold,
}: ReeducationNoticeProps) {
  const t = useTranslations('education');

  if (!raised && !standing) {
    return (
      <p className="edu-flag" data-raised="false" data-testid="reeducation-flag">
        {t('flag.clear')}
      </p>
    );
  }

  return (
    <div
      className="edu-flag"
      data-raised="true"
      data-standing={standing ? 'true' : undefined}
      data-testid="reeducation-flag"
      // A status region and not an alert. It is important and it is not an emergency; a screen
      // that interrupts for everything is a screen whose interruptions mean nothing, and CP73
      // reserves `role="alert"` for the critical-value strip.
      role="status"
    >
      <p className="edu-flag__headline">{standing ? t('flag.standing') : t('flag.raised')}</p>
      {/*
        The working belongs to an assessment. A flag standing from an earlier visit has none
        here — the counts describe a session that has not happened yet — and printing them
        anyway produced "0 could not be done, 0 were corrected today" under a headline saying
        the patient was flagged months ago, which is two false numbers in the place the notice
        exists to be checked against.
      */}
      {!standing && (
        <p className="edu-flag__working" data-testid="reeducation-working">
          {t('flag.working', { unable, correctedToday, threshold })}
        </p>
      )}
    </div>
  );
}
