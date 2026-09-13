/**
 * One of five faces, drawn from a rank rather than chosen from a set of five files (CP88 §3).
 *
 * # Why a face at all
 *
 * The scale's anchors have to be understood by a patient who cannot read — acceptance criterion
 * 4, and in this clinic it is not an edge case. A numeral means nothing to somebody who was
 * never taught numerals, a colour alone is unavailable to one man in twelve, and a word is the
 * thing being avoided. A mouth curve is understood by everybody who has ever seen another
 * person.
 *
 * # Why it is geometry and not an emoji
 *
 * An emoji is a font's opinion. The same code point is a different expression on Android, on
 * iOS and in a PDF, and one of those renderings is a yellow circle with sunglasses. A face
 * whose meaning depends on which device the officer happens to be holding cannot be the anchor
 * of a clinical scale. This one is four primitives, identical everywhere, and it prints.
 *
 * # Why the eyes change as well as the mouth
 *
 * A five-point ramp drawn with the mouth alone has two adjacent faces that differ by a few
 * pixels of curvature, which at the size a patient sees across a desk is no difference at all.
 * The brows move with the mouth — down and together at the unhappy end, level at neutral, up at
 * the happy end — so the two ends of the ramp differ in two ways rather than in one.
 *
 * Nothing here carries meaning on its own: every face is drawn beside its own words, in the
 * reader's language, and the band label is what a literate operator reads. The face is for the
 * person who cannot.
 */

export interface ScoreFaceProps {
  /** 1 for the unhappiest, n for the happiest, as the anchor's `face_rank` gives it. */
  rank: number;
  /** How many ranks the scale has, so a three-band or six-band scale still draws a full ramp. */
  ranks: number;
  size?: number;
}

export function ScoreFace({ rank, ranks, size = 44 }: ScoreFaceProps) {
  // Where this face sits on the ramp: 0 at the unhappiest, 1 at the happiest. Computed from the
  // scale's own rank count so that the symmetric 0–10 variant of spec §3 — which has a different
  // number of bands — draws a full ramp rather than four faces and a gap.
  const position = ranks <= 1 ? 0.5 : (rank - 1) / (ranks - 1);

  // The mouth, as a quadratic curve whose control point travels from above the line (a frown) to
  // below it (a smile). At the midpoint the control sits on the line, which draws the flat mouth
  // "about the same" needs.
  //
  // Below is a smile because SVG's y grows downward, and getting that backwards is exactly the
  // mistake this comment exists to stop somebody repeating: the first draft of this file drew a
  // beaming face on "much worse" and a miserable one on "much better", and it looked deliberate.
  const mouthLift = (position - 0.5) * 26;
  const mouth = `M 26 62 Q 50 ${62 + mouthLift} 74 62`;

  // The brows, and they move on one side of the scale only. At the unhappy end the inner ends
  // drop into a frown; from the midpoint upward they are level. A brow that kept tilting past
  // neutral would raise the inner ends on the happy faces, which reads as worried rather than
  // pleased — the wrong expression on the one band a patient is most likely to point at.
  const browTilt = Math.max(0, 0.5 - position) * 18;
  const browLift = (position - 0.5) * 5;

  return (
    <svg
      className="edu-face"
      viewBox="0 0 100 100"
      width={size}
      height={size}
      // Decorative. Everything it conveys is in the band's label beside it, which is real text in
      // the reader's language — so a screen reader reads the words rather than a description of a
      // drawing, and a face that failed to render leaves a complete control behind.
      aria-hidden="true"
      focusable="false"
    >
      <circle className="edu-face__head" cx="50" cy="50" r="40" />
      <circle className="edu-face__eye" cx="36" cy="42" r="4.5" />
      <circle className="edu-face__eye" cx="64" cy="42" r="4.5" />
      <path
        className="edu-face__brow"
        d={`M 28 ${31 - browLift} L 44 ${31 - browLift + browTilt}`}
      />
      <path
        className="edu-face__brow"
        d={`M 72 ${31 - browLift} L 56 ${31 - browLift + browTilt}`}
      />
      <path className="edu-face__mouth" d={mouth} fill="none" />
    </svg>
  );
}
