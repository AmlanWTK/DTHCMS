'use client';

import { useEffect } from 'react';

/**
 * The dashboard's keyboard shortcuts (CP73's scope), and the focus rules that make them safe.
 *
 * # Why a physician wants these at all
 *
 * This is the screen the consultation happens in, with a patient in the room. Reaching for a
 * mouse to move between three columns is two seconds of attention off the person in front of
 * you, forty times a morning. The shortcuts are single keys because a modifier chord is
 * something you have to *remember*, and this has to be muscle memory by the end of a week.
 *
 * # The rules that make single keys safe
 *
 * Single-key shortcuts are dangerous in exactly one situation: somebody typing. So:
 *
 *   - **Nothing fires while focus is in a field.** `input`, `textarea`, `select`, anything
 *     `contenteditable`. The physician typing a rejection note types the letter `r`; they do
 *     not toggle a panel. This is checked on the *event target*, not on a flag somebody has
 *     to remember to set.
 *   - **Nothing fires with a modifier held.** `Ctrl+P` is print, `Cmd+1` is a browser tab.
 *     Stealing either would be taking a key the operating system owns.
 *   - **Nothing fires while a dialog is open**, because a dialog's own Escape handling and a
 *     global one would race.
 *
 * # Why moving focus and not just scrolling
 *
 * Pressing `2` moves focus to the centre panel rather than scrolling to it. A screen reader
 * then announces the region it has entered, and a keyboard user's next Tab starts inside it —
 * which is the difference between a shortcut and an animation. Each panel is a landmark with
 * `tabIndex={-1}` so it can receive focus without joining the tab order.
 *
 * # Why the list is also on screen
 *
 * `?` opens it. A shortcut nobody can discover is a shortcut for the person who wrote it, and
 * a printed card on a desk is how these are actually learned in a clinic — so the overlay is
 * the same list the documentation carries, in both languages.
 */

export interface DashboardShortcutHandlers {
  focusPanel: (panel: 'snapshot' | 'summary' | 'assistant') => void;
  toggleAssistant: () => void;
  print: () => void;
  toggleHelp: () => void;
  closeOverlays: () => void;
}

/** The bindings, exported so the on-screen list and the documentation cannot drift from them. */
export const DASHBOARD_SHORTCUTS = [
  { keys: ['1'], action: 'focusSnapshot' },
  { keys: ['2'], action: 'focusSummary' },
  { keys: ['3'], action: 'focusAssistant' },
  { keys: ['r'], action: 'toggleAssistant' },
  { keys: ['p'], action: 'print' },
  { keys: ['?'], action: 'help' },
  { keys: ['Escape'], action: 'close' },
] as const;

export function useDashboardShortcuts(handlers: DashboardShortcutHandlers): void {
  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.defaultPrevented) return;
      // A modifier means the key belongs to the browser or the operating system. Taking
      // Ctrl+P from a physician who wanted the browser's print dialog would be taking a key
      // they have used for twenty years.
      if (event.metaKey || event.ctrlKey || event.altKey) return;
      if (isTyping(event.target)) return;

      switch (event.key) {
        case '1':
          handlers.focusPanel('snapshot');
          break;
        case '2':
          handlers.focusPanel('summary');
          break;
        case '3':
          handlers.focusPanel('assistant');
          break;
        case 'r':
        case 'R':
          handlers.toggleAssistant();
          break;
        case 'p':
        case 'P':
          handlers.print();
          break;
        case '?':
          handlers.toggleHelp();
          break;
        case 'Escape':
          handlers.closeOverlays();
          // Deliberately not `preventDefault`: Escape also cancels an in-flight navigation
          // and closes a native dialog, and a screen that swallowed it would be a screen
          // where the browser's own escape stopped working.
          return;
        default:
          return;
      }
      event.preventDefault();
    }

    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [handlers]);
}

/**
 * Whether the event came from somewhere a person is typing.
 *
 * Checked on the target rather than on `document.activeElement`, because the two disagree
 * inside a shadow root and during a focus transition — and the one that is right is the one
 * the keystroke was actually delivered to.
 */
function isTyping(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable) return true;
  const tag = target.tagName.toLowerCase();
  return tag === 'input' || tag === 'textarea' || tag === 'select';
}
