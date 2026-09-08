'use client';

import { useTranslations } from 'next-intl';
import type { ReactNode } from 'react';

import { Icon, cx } from '@dthcms/ui';

/**
 * The enclosure every machine-written region on this screen sits inside (CP73 criterion 3).
 *
 * # Why a label was not enough, and what is here instead
 *
 * The criterion is *"AI-generated content is unmistakably marked"*, and the obvious
 * implementation — a small chip reading "AI" above the narrative — fails it in four ordinary
 * situations, all of which happen in this clinic every week:
 *
 *   1. **A scrolled panel.** §8's narrative is a page of prose. A physician reading the
 *      bottom half of it has scrolled the chip off the screen, and what is in front of them
 *      is unmarked clinical text.
 *   2. **A photograph of the screen** sent to a colleague, which is how a second opinion is
 *      actually asked for here. The crop rarely includes the top of the panel.
 *   3. **Colour that is not there.** Roughly one man in twelve working in this clinic cannot
 *      use hue; a tablet held near a window flattens it for everybody; the printed summary
 *      has none of it worth relying on.
 *   4. **Habituation.** A chip in the same place on every screen stops being read after a
 *      week. That is not a failure of the reader.
 *
 * So the marking is **four signals, and none of them is a colour**:
 *
 *   - **Enclosure.** The region has its own ground and a heavy left rule, so it is visibly a
 *     different kind of surface from the record beside it. This is the signal that survives
 *     a photograph: the boundary is where the machine's words start.
 *   - **A sticky header inside the region.** It stays at the top of the *panel* while the
 *     prose scrolls under it, so there is no scroll position at which the mark is off screen.
 *   - **A repeated gutter.** The left rule is not a line but a repeating column of the word
 *     "AI", set small and low-contrast, running the whole height of the region. A crop that
 *     shows three lines of narrative shows the gutter beside them.
 *   - **Words, in both languages.** "Written by AI — check every line before acting" and its
 *     Bengali, in the header. The physician may read either.
 *
 * # The cost, stated plainly
 *
 * This is visually loud, and it is loud on the two panels a physician looks at most. That is
 * the trade the criterion asks for, and it is the one place on this screen where visual
 * weight is a safety control rather than a style choice: the failure it prevents is a
 * clinician acting on a machine's sentence in the belief that a person wrote it, and there is
 * no version of that failure that is small.
 *
 * It also costs one design iteration that has not happened yet. The checkpoint's own risk
 * section says to expect two or three, and this treatment is the one to put in front of
 * Dr. Nahid first *because* it is loud — it is easier to argue a mark down than to discover a
 * year later that nobody was reading a subtle one.
 *
 * # What is not marked, and why that matters as much
 *
 * The right panel is a **mixture**. A drafted diagnosis is a model's proposal; a missing-data
 * alert is arithmetic the deterministic assembler did over the record, with no model
 * involved. Both live in §8's right panel. Marking the whole panel as AI would teach a
 * physician to discount the one item on it that is certainly true — so the panel carries this
 * enclosure only around the model's items, and each item additionally says in a word which it
 * is. See `SuggestionCard`.
 */

export interface AiMarkedProps {
  children: ReactNode;
  /**
   * What the region is, for the header: "Clinical summary", "Suggested diagnoses". Named so
   * that a screen reader announcing the region says what kind of machine output it is
   * entering rather than only that it is machine output.
   */
  label: string;
  /**
   * A model that is not answering. The header keeps every one of its marks and adds the
   * state, because a degraded panel is exactly where an unmarked sentence would be most
   * dangerous: what is left on screen is a mixture of the system's own findings and whatever
   * the last run said.
   */
  degraded?: boolean;
  testId?: string;
  className?: string;
}

export function AiMarked({ children, label, degraded, testId, className }: AiMarkedProps) {
  const t = useTranslations('dashboard.ai');

  return (
    <section
      className={cx('dash-ai', className)}
      data-testid={testId ?? 'ai-marked'}
      data-degraded={degraded ? 'true' : 'false'}
      // A landmark with an accessible name, so that a screen-reader user meets "AI-generated,
      // Clinical summary, region" on entry rather than discovering it from a visual treatment
      // they cannot see. The name leads with the machine, not with the topic.
      aria-label={`${t('regionLabel')} — ${label}`}
    >
      {/*
        The gutter. `aria-hidden` because it is the same sentence the header already says, and
        a screen reader announcing "AI" forty times down the side of a paragraph would be the
        accessibility equivalent of the thing this component exists to prevent.

        The repetition is CSS — a repeating background of the two letters — rather than forty
        spans, so that the column stays the height of the content whatever the content is, and
        so that a print stylesheet can keep it.
      */}
      <span className="dash-ai__gutter" aria-hidden="true" />

      <header className="dash-ai__header">
        <Icon name="sliders-horizontal" className="dash-ai__icon" aria-hidden />
        <div className="dash-ai__words">
          {/* Both languages, always, and not only the one the interface is set to. A summary
              is read over a shoulder by whoever is in the room, and the person who most needs
              to know a machine wrote this may not be the person who chose the language. */}
          <strong className="dash-ai__title">{t('title')}</strong>
          <span className="dash-ai__caution">{t('caution')}</span>
        </div>
      </header>

      <div className="dash-ai__body">{children}</div>
    </section>
  );
}

/**
 * The word beside one item, for a panel that mixes machine output with the record.
 *
 * A word and never only a tint, for the reasons in the component comment above, and the
 * *same* word in both states rather than a mark on one and nothing on the other: a
 * distinction carried by the absence of a mark cannot survive being cropped, and "no mark"
 * and "a mark I did not notice" look identical.
 */
export function OriginMark({ origin }: { origin: 'MODEL' | 'SYSTEM' }) {
  const t = useTranslations('dashboard.ai');
  return (
    <span className="dash-origin" data-origin={origin} data-testid={`origin-${origin}`}>
      <Icon name={origin === 'MODEL' ? 'sliders-horizontal' : 'clipboard-list'} aria-hidden />
      {t(origin === 'MODEL' ? 'origin.model' : 'origin.system')}
    </span>
  );
}
