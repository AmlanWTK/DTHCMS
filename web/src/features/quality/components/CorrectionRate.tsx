'use client';

import { useTranslations } from 'next-intl';

/**
 * A correction count and the work it came out of, which is the only way either is drawn
 * (CP63, §4.3).
 *
 * # Why this is a component and not a line of JSX
 *
 * The plan's stated risk for this checkpoint is that *"a metric that feels punitive damages
 * data honesty — staff hide errors instead of correcting them."* The first and most reliable
 * way to build that metric is to put a number on a screen with a person's name beside it and
 * nothing to read it against. "Three corrections" is not a fact about anybody; three
 * corrections out of four hundred and twelve entries and three out of forty are two
 * different facts, and only one of them is worth a conversation.
 *
 * So the denominator is not a convention this feature follows carefully. It is a **type
 * error** to leave it out. `corrections` and `entries` are both required props, there is no
 * default for either, and there is no other component or message in this feature that draws
 * a correction count at all — `quality.test.tsx` asserts both halves: that this cannot be
 * called with only a numerator, and that no message in the namespace interpolates a count
 * placeholder without its denominator placeholder beside it.
 *
 * # Why the rate has three states and not two
 *
 * `rate` is a number, `null`, or `'not-taken'`, and the difference between the last two is
 * the whole point of the type:
 *
 *   - a **number** is a rate the server computed;
 *   - **`null`** is the server declining to compute one, because there were too few entries
 *     in the window for it to mean anything. It renders as a sentence, never as `0` and never
 *     as a dash. Zero is a claim about somebody's month; a dash is a claim that the answer is
 *     missing. Neither is true;
 *   - **`'not-taken'`** is a reading that carries no rate at all, which is what a raised flag
 *     is: it froze a count and a denominator at the moment it was raised and never computed a
 *     ratio. Folding that into `null` would put "too few entries" underneath a flag raised on
 *     four hundred of them.
 *
 * # And why the absent rate names a number
 *
 * The floor now comes down on the payload, so the sentence is *"eight more values this month
 * and a rate appears"* rather than *"too few"*. That difference is most of what makes this read
 * as arithmetic rather than as a judgement: a threshold somebody can count towards is a rule,
 * whereas an unexplained refusal to show a number is a decision being taken about them that
 * they cannot check. `rateFloor` is therefore a required prop too — a caller with no floor to
 * hand has to say so with `null` and gets the wordier sentence, rather than getting it by
 * forgetting.
 */

/** What is known about the rate. Three states, because two of them are not the same absence. */
export type RateReading = number | null | 'not-taken';

export interface CorrectionRateProps {
  /**
   * Corrections in the window. Required, and meaningless on its own — which is why the next
   * prop is required too.
   */
  corrections: number;
  /**
   * What they came out of: values this person typed in the same window. Required. There is no
   * default and there must never be one; a default denominator is a denominator somebody
   * forgot, rendered as though it were a fact.
   */
  entries: number;
  /** See `RateReading`. Required, so that "there is no rate here" is a stated answer. */
  rate: RateReading;
  /**
   * The entry count below which the server computes no rate, straight from the payload.
   *
   * `null` where this reading has no floor — a flag, which never took a ratio. Required rather
   * than optional so that the vaguer sentence is a choice somebody made rather than one they
   * fell into.
   */
  rateFloor: number | null;
  /**
   * Draws the sentence as a single line rather than a block. For a row in a list, where the
   * pair still travels together — the layout changes, the rule does not.
   */
  inline?: boolean;
  testId?: string;
}

export function CorrectionRate({
  corrections,
  entries,
  rate,
  rateFloor,
  inline = false,
  testId,
}: CorrectionRateProps) {
  const t = useTranslations('quality');

  // How many more values this window needs before there is a rate. Never zero or negative on
  // screen: the server returns a rate the moment the floor is met, so a "0 more entries"
  // sentence would only ever appear if the two disagreed, and it would read as nonsense.
  const stillNeeded = rateFloor === null ? 0 : Math.max(0, rateFloor - entries);

  return (
    <span
      className={inline ? 'app-quality__rate app-quality__rate--inline' : 'app-quality__rate'}
      data-testid={testId ?? 'correction-rate'}
    >
      <span className="app-quality__rate-count">
        {/*
         * The counts go in as **numbers**, not as pre-formatted strings, and that is
         * load-bearing twice over. A simple ICU argument is `String(value)`, so a plain
         * `{corrections}` would put Latin digits inside a Bangla sentence — and a plural form
         * needs a number to choose between "1 correction" and "3 corrections". Passing the
         * formatted string would silently break both.
         */}
        {t('rate.outOf', { corrections, entries })}
      </span>{' '}
      {rate === 'not-taken' ? null : rate === null ? (
        // Words, not a zero and not a dash — and where the floor is known, words with a number
        // in them, so the reader can count towards it instead of being told they fall short.
        <span className="app-quality__rate-absent" data-testid="rate-too-few">
          {stillNeeded > 0 ? t('rate.moreNeeded', { needed: stillNeeded }) : t('rate.tooFew')}
        </span>
      ) : (
        <span className="app-quality__rate-value" data-testid="rate-value">
          {/*
           * Per hundred entries, in running prose, so the numerals follow the reader's
           * language rather than staying in ASCII. The house rule keeps a *measurement* in
           * ASCII because somebody may copy it onto a paper chart or read it back over a
           * phone; a ratio in a sentence about somebody's month is not that, and mixing two
           * numeral systems inside one sentence is worse than either. `{rate, number}` in the
           * message is what does it — a bare `{rate}` would be `String(value)` and Latin.
           */}
          {t('rate.perHundred', { rate })}
        </span>
      )}
    </span>
  );
}
