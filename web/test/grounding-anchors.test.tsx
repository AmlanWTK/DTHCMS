import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';

import { GroundedText } from '@/features/dashboard';

import { renderWithProviders } from './render';

/**
 * Grounding anchors in the clinical summary (CP73 defect).
 *
 * # The defect
 *
 * CP72's grounding check requires every clinical claim in the AI summary to carry a fact
 * reference, and the model writes them inline. The panel rendered the narrative as plain
 * text, so a physician read *"systolic pressure has risen since March
 * [obs.bp_systolic:2026-09-08]"* — bracket included, once or twice a sentence, down a page
 * of prose whose stated target is comprehension in sixty seconds.
 *
 * # Why this is three tests and not one
 *
 * Because there are three ways to "fix" it and two of them are worse than the defect:
 *
 *  1. **Strip the anchors from the payload.** The grounding check is computed over them, and
 *     a medico-legal review's whole question is which value a sentence came from. The
 *     assertion below is that the reference is still reachable.
 *  2. **Swallow anything in brackets.** `[HbA1c 12.9]` is not a citation — the backend
 *     leaves it in the prose deliberately, for the number arm to check — and a client that
 *     hid it would hide a number from the reader while the server was still checking it.
 *  3. **Render the mark and lose the sentence.** The words either side of an anchor are the
 *     clinical claim, and a fix that dropped a space or a clause would be a summary that
 *     says something slightly different from what was grounded.
 */

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..');

describe('an anchor is a mark, not forty characters of prose', () => {
  it('keeps the sentence intact and does not print the reference in it', async () => {
    renderWithProviders(
      <p>
        <GroundedText>
          Systolic pressure has risen since March [obs.bp_systolic:2026-09-08] and the weight is
          unchanged [obs.body_weight:2026-09-08].
        </GroundedText>
      </p>,
    );

    const paragraph = screen.getByText(/Systolic pressure has risen/);
    expect(paragraph.textContent).toContain('Systolic pressure has risen since March');
    expect(paragraph.textContent).toContain('and the weight');
    // The bracket is gone from what the eye reads.
    expect(paragraph.textContent).not.toContain('[obs.bp_systolic:2026-09-08]');
    // Two claims, two marks, numbered so a reader can refer to one.
    const marks = screen.getAllByTestId('grounding-anchor');
    expect(marks).toHaveLength(2);
    expect(marks[0]).toHaveTextContent('1');
    expect(marks[1]).toHaveTextContent('2');
  });

  it('still carries the reference, one interaction away', async () => {
    /*
     * The half that must not be lost. A mark with no way back to the reference is a footnote
     * with no footnote, and the person who needs it — a reviewer asking which value a
     * machine's sentence was drawn from — is exactly the person who cannot ask the machine.
     */
    renderWithProviders(
      <p>
        <GroundedText>Pressure has risen [obs.bp_systolic:2026-09-08].</GroundedText>
      </p>,
    );

    const mark = screen.getByTestId('grounding-anchor');
    await userEvent.click(mark);

    const panel = screen.getByRole('tooltip');
    expect(within(panel).getByText('obs.bp_systolic:2026-09-08')).toBeInTheDocument();
    // Described rather than merely adjacent, so a screen reader speaks the reference on
    // focus instead of announcing a button whose contents are invisible to it.
    expect(mark).toHaveAttribute('aria-describedby', panel.id);
  });

  it('leaves alone a bracket that is not a citation', () => {
    // The backend's own rule: `[HbA1c 12.9]` is not a reference, is left in the prose, and
    // is checked by the number arm. A client that hid it would hide a number the server is
    // still verifying.
    renderWithProviders(
      <p>
        <GroundedText>The last reading [HbA1c 12.9] was high.</GroundedText>
      </p>,
    );

    expect(screen.getByText(/\[HbA1c 12\.9\]/)).toBeInTheDocument();
    expect(screen.queryByTestId('grounding-anchor')).toBeNull();
  });

  it('renders every anchor on a second render', () => {
    /*
     * A regular expression with the `g` flag holds `lastIndex` between calls, so a
     * module-level one reused across renders skips every other match — a component that
     * works the first time and quietly half-works afterwards, which on a panel that
     * re-renders whenever the summary refreshes is most of the time.
     */
    const prose = 'One [obs.a:2026-01-01] two [obs.b:2026-01-02] three [obs.c:2026-01-03].';
    const { rerender } = renderWithProviders(
      <p>
        <GroundedText>{prose}</GroundedText>
      </p>,
    );
    expect(screen.getAllByTestId('grounding-anchor')).toHaveLength(3);

    rerender(
      <p>
        <GroundedText>{prose}</GroundedText>
      </p>,
    );
    expect(screen.getAllByTestId('grounding-anchor')).toHaveLength(3);
  });
});

describe('the anchor shape matches the server’s', () => {
  it('uses the same expression the grounding check uses to decide what to check', () => {
    /*
     * The two have to agree on what an anchor *is*. If the client took a looser view it
     * would hide text the server is still checking; a stricter one would print references
     * the server treats as citations. Neither fails any other test in this file, so the
     * source of truth is compared directly.
     */
    const server = readFileSync(
      join(webRoot, '..', 'backend', 'internal', 'ai', 'grounding.go'),
      'utf8',
    );
    const client = readFileSync(
      join(webRoot, 'src', 'features', 'dashboard', 'components', 'GroundedText.tsx'),
      'utf8',
    );

    // `refShape` in Go, anchored with ^$ and written over the *inside* of the brackets.
    const go = /refShape\s*=\s*regexp\.MustCompile\(`\^(.+)\$`\)/.exec(server)?.[1];
    expect(go, 'refShape not found in grounding.go — has it been renamed?').toBeDefined();

    const ts = /const ANCHOR = \/\\\[\((.+)\)\\\]\/g;/.exec(client)?.[1];
    expect(ts, 'ANCHOR not found in GroundedText.tsx').toBeDefined();

    // Go writes `[0-9]` where the TypeScript writes `\d`, and Go's `(?:…)` is the same
    // group. Normalised rather than compared byte for byte, because an equality that could
    // only be satisfied by writing Go's dialect into TypeScript would be a test people
    // delete.
    const normalise = (source: string) => source.replaceAll('[0-9]', '\\d').replaceAll(' ', '');
    expect(normalise(ts as string)).toBe(normalise(go as string));
  });
});
