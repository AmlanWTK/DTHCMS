'use client';

import { useTranslations } from 'next-intl';
import { useId, useState, type ReactNode } from 'react';

/**
 * A machine-written sentence with its grounding anchors rendered as marks (CP73 defect fix).
 *
 * # What was wrong
 *
 * CP72's grounding check requires every clinical claim in the summary to carry a fact
 * reference, and the model writes them inline: *"systolic pressure has risen since March
 * [obs.bp_systolic:2026-09-08]"*. The panel rendered the narrative as plain text, so the
 * physician read the bracket too.
 *
 * That is not cosmetic. §8's target is a summary comprehended in sixty seconds, and a page of
 * prose with a forty-character identifier every second sentence is not a page anybody reads
 * at that speed — the eye stops at each bracket, and the sentence has to be re-entered.
 * Worse, the identifiers are the part that looks technical, so they read as the important
 * part.
 *
 * # Why the anchors are not simply stripped
 *
 * The grounding check depends on them. `internal/ai/grounding.go` walks the narrative,
 * checks each bracketed reference against the record, and reports `citations_checked`; a
 * client that removed them from the payload would remove them from the only evidence the
 * verdict is computed over. And on a medico-legal review the anchor *is* the answer: which
 * value the machine's sentence was drawn from.
 *
 * So the anchor stays in the data, and the **rendering** changes: it becomes a superscript
 * mark, and the reference itself is one interaction away — hover, keyboard focus, or a tap,
 * the same three ways in that CP61's attribution uses, for the same reason. The clinic reads
 * screens on cheap tablets where there is no hover at all.
 *
 * # What counts as an anchor
 *
 * The server's own shape, mirrored: `[a.b.c]` or `[a.b:2026-09-08]` — lower-case dotted
 * segments, optionally a colon and an ISO date. Deliberately the same expression the backend
 * uses to decide what to *check*, because the two must agree on what an anchor is; a client
 * that took a looser view would swallow `[HbA1c 12.9]`, which is not a citation and which the
 * backend explicitly leaves in the prose for the number arm to check.
 *
 * The strict shape is the safe direction to be wrong in: something that is not an anchor is
 * rendered exactly as it was written, which is what plain text already did.
 */

/**
 * The reference shape, mirroring `refShape` in `backend/internal/ai/grounding.go`.
 *
 * Kept in step with the server by the test named *the anchor shape matches the server's*,
 * which is the only thing that will notice if one of them changes.
 */
const ANCHOR = /\[([a-z][a-z0-9_]*(?:\.[a-z0-9_]+)*(?::\d{4}-\d{2}-\d{2}(?:\.[a-z]+)?)?)\]/g;

export interface GroundedTextProps {
  /** One paragraph of machine-written prose, anchors and all. */
  children: string;
}

export function GroundedText({ children }: GroundedTextProps) {
  const parts: ReactNode[] = [];
  let cursor = 0;
  let counter = 0;

  // `matchAll` on a fresh iteration each render: the regex is module-level and carries the
  // `g` flag, so reusing `lastIndex` across calls would make every second render skip half
  // the anchors — a bug that shows up as a component that works once.
  for (const match of children.matchAll(ANCHOR)) {
    const index = match.index ?? 0;
    if (index > cursor) parts.push(children.slice(cursor, index));
    parts.push(
      <Anchor key={`${index}-${match[1]}`} reference={match[1] ?? ''} ordinal={++counter} />,
    );
    cursor = index + match[0].length;
  }
  if (cursor < children.length) parts.push(children.slice(cursor));

  // The whitespace an anchor leaves behind. "risen since March [obs.x] and the weight" has a
  // space on each side of the bracket, and removing the bracket alone leaves two — visible
  // in justified prose as a hole. Collapsed only where a mark was actually removed.
  return <>{parts}</>;
}

/**
 * One superscript mark.
 *
 * A `<button>` and not a `<sup>` with a `title`: a title attribute is invisible to a
 * keyboard, arrives after a delay a reader on a tablet never waits through, and does not
 * exist at all on touch. This is the same disclosure pattern the attribution panel uses, cut
 * down to one line of text.
 *
 * The number is what the reader sees. The reference is what they get when they ask — and it
 * is `aria-describedby`, so a screen reader speaks it on focus rather than announcing a
 * button whose contents are invisible to it.
 */
function Anchor({ reference, ordinal }: { reference: string; ordinal: number }) {
  const t = useTranslations('dashboard.summary');
  const panelId = useId();
  const [pinned, setPinned] = useState(false);

  return (
    <span className="dash-anchor" data-open={pinned}>
      <button
        type="button"
        className="dash-anchor__mark"
        aria-describedby={panelId}
        aria-label={t('anchorLabel', { reference })}
        data-testid="grounding-anchor"
        onClick={() => setPinned((open) => !open)}
        onKeyDown={(event) => {
          if (event.key === 'Escape' && pinned) {
            setPinned(false);
            event.stopPropagation();
          }
        }}
      >
        <sup>{ordinal}</sup>
      </button>
      <span role="tooltip" id={panelId} className="dash-anchor__panel">
        <span className="dash-anchor__heading">{t('anchorHeading')}</span>
        <code>{reference}</code>
      </span>
    </span>
  );
}
