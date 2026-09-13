'use client';

import { useLocale, useTranslations } from 'next-intl';

import type { Locale } from '@/lib/i18n/config';

import type { RecordingCapability } from '../api/capability';
import {
  anchorFor,
  scaleValues,
  type EducationReference,
  type ImprovementScale,
} from '../api/education';

import { ScoreFace } from './ScoreFace';

/**
 * §2's question and §3's scale, as one interaction (CP88 criterion 1 and criterion 4).
 *
 * # What a patient sees, and what the operator sees
 *
 * The two are deliberately different things on one control.
 *
 * The **patient** is shown five faces on a colour ramp, each with its band's words underneath in
 * Bangla, and nothing else. They are not handed the tablet: §2 says the question is read aloud,
 * and the faces are what the officer turns the screen round to point at. A patient who cannot
 * read a numeral can put a finger on a face, and the five bands are five distinguishable things
 * at arm's length.
 *
 * The **operator** additionally sees the numeral, small, in the corner of each button. It is
 * there so they can read back "seven" to confirm, and so a supervisor watching can tell which
 * point was tapped. It is deliberately the quietest thing on the control: a large numeral would
 * make the numbers the subject and the faces the decoration, which is the wrong way round for
 * the person being asked.
 *
 * # Why every point is its own button rather than a slider
 *
 * Criterion 1 is that the score is captured in one interaction, and a slider is not one
 * interaction — it is a grab, a drag, a release and a glance to check where it landed, on a
 * tablet held at an angle in front of somebody else. Ten buttons is one tap. It also means the
 * control works from a keyboard and under a screen reader without anything extra, and that the
 * value cannot land between two points.
 *
 * # Why the bands are drawn as bands
 *
 * The buttons are grouped under their band's face and words, so the patient sees five things and
 * the operator sees ten. Ten unlabelled numerals would be the scale spec §3 says a patient
 * cannot use; five bands with no numbers underneath would lose [R-11]'s ten points.
 *
 * # The one thing this control will not do
 *
 * It does not offer a score when the server says this is a first visit. §2: *"a first-visit
 * score would be a number answering a different question."* The screen shows the
 * not-applicable state as an already-made decision rather than as a choice the officer has to
 * remember to make, because the choice they will actually make under time pressure is whichever
 * one is one tap away.
 */

export interface ImprovementScoreSelectorProps {
  /**
   * Proof that this reader may record.
   *
   * This is the control the token exists for. CP88 §1 moves the improvement question away from
   * the consultation because a patient asked by the person whose treatment it grades answers
   * upward — and a consultant who can hover over an 8 is being invited to think of the number
   * as adjustable. He cannot render this component.
   */
  capability: RecordingCapability;
  scale: ImprovementScale;
  /** The vocabulary of reasons the question may not apply. */
  reasons: EducationReference['not_applicable_reasons'];
  /** The value chosen so far, or null. */
  value: number | null;
  /** The not-applicable reason chosen so far, or null. */
  notApplicable: string | null;
  /** True when the server says there is no last visit to compare with. */
  firstVisit: boolean;
  onPick: (value: number) => void;
  onNotApplicable: (reason: string) => void;
  disabled?: boolean;
}

export function ImprovementScoreSelector({
  scale,
  reasons,
  value,
  notApplicable,
  firstVisit,
  onPick,
  onNotApplicable,
  disabled = false,
}: ImprovementScoreSelectorProps) {
  const t = useTranslations('education');
  const locale = useLocale() as Locale;
  const bn = locale === 'bn';

  const ranks = scale.anchors.length;
  const values = scaleValues(scale);

  return (
    <section className="edu-score" data-testid="improvement-score">
      <h3 className="edu-score__question" data-testid="improvement-question">
        {/* Both languages, always, and the reader's first. The officer reads the Bangla aloud
            and the English is what makes the record auditable by somebody who does not read
            Bangla — printing only one of them would make one of those two impossible. */}
        <span className="edu-score__question-primary" lang={bn ? 'bn' : 'en'}>
          {bn ? scale.question_bn : scale.question_en}
        </span>
        <span className="edu-score__question-secondary" lang={bn ? 'en' : 'bn'}>
          {bn ? scale.question_en : scale.question_bn}
        </span>
      </h3>

      {firstVisit ? (
        <p className="edu-score__first-visit" data-testid="improvement-first-visit">
          {t('score.firstVisit')}
        </p>
      ) : (
        <div
          className="edu-score__bands"
          role="radiogroup"
          aria-label={bn ? scale.question_bn : scale.question_en}
        >
          {scale.anchors.map((anchor) => {
            const inBand = values.filter(
              (candidate) => candidate >= anchor.from_value && candidate <= anchor.to_value,
            );
            return (
              <div
                className="edu-score__band"
                key={anchor.from_value}
                // The ramp position, so the stylesheet can tint the whole band rather than each
                // button. Colour is never the only signal — the face and the words carry it —
                // but it is the one a patient reads fastest across a desk.
                data-rank={anchor.face_rank}
              >
                <ScoreFace rank={anchor.face_rank} ranks={ranks} />
                <p className="edu-score__band-label" lang={bn ? 'bn' : 'en'}>
                  {bn ? anchor.label_bn : anchor.label_en}
                </p>
                <div className="edu-score__points">
                  {inBand.map((point) => (
                    <button
                      key={point}
                      type="button"
                      role="radio"
                      aria-checked={value === point}
                      className="edu-score__point"
                      data-testid={`score-${point}`}
                      data-selected={value === point ? 'true' : undefined}
                      disabled={disabled}
                      onClick={() => onPick(point)}
                    >
                      {/* The numeral, and the band's words as the accessible name. A screen
                          reader saying "seven" alone would give a blind operator the digit and
                          not the meaning, which is the same problem the faces solve for the
                          patient. */}
                      <span className="edu-score__numeral">{point}</span>
                      <span className="edu-score__spoken">
                        {point} — {bn ? anchor.label_bn : anchor.label_en}
                      </span>
                    </button>
                  ))}
                </div>
              </div>
            );
          })}
        </div>
      )}

      {/* What was chosen, read back in words. The officer confirms aloud, and this is what they
          read from — a selected button on a ten-point row is a small visual difference to check
          against a patient's answer. */}
      {value !== null && (
        <p className="edu-score__chosen" data-testid="improvement-chosen">
          {t('score.chosen', {
            value,
            label: labelFor(scale, value, bn),
          })}
        </p>
      )}

      <div className="edu-score__na">
        {reasons.map((reason) => (
          <button
            key={reason.code}
            type="button"
            className="edu-score__na-button"
            data-testid={`score-na-${reason.code}`}
            data-selected={notApplicable === reason.code ? 'true' : undefined}
            aria-pressed={notApplicable === reason.code}
            disabled={disabled}
            onClick={() => onNotApplicable(reason.code)}
          >
            {bn ? reason.display_bn : reason.display_en}
          </button>
        ))}
      </div>
      <p className="edu-score__na-note">{t('score.notApplicableNote')}</p>
    </section>
  );
}

function labelFor(scale: ImprovementScale, value: number, bn: boolean): string {
  const anchor = anchorFor(scale, value);
  if (anchor === null) return String(value);
  return bn ? anchor.label_bn : anchor.label_en;
}
