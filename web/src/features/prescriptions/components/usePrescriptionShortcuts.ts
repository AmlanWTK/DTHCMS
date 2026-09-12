'use client';

import { useEffect } from 'react';

/**
 * The prescription editor's shortcuts, and why they are not the dashboard's (CP73, CP81).
 *
 * # Single keys are wrong on this screen
 *
 * CP73's dashboard binds `1`, `2`, `3`, `r`, `p` and `?` with no modifier, guarded by "nothing
 * fires while focus is in a field". That is right for a reading screen where typing is the
 * exception.
 *
 * This screen is typing. A physician is in the medicine search, the dose field or a removal reason
 * essentially the whole time he is here, so a single-key binding would be inert exactly when it is
 * wanted — and the one moment it were not inert would be the moment focus had slipped somewhere
 * unexpected, which is the worst time for `p` to open a preview.
 *
 * So every shortcut here takes **Alt**, and every one of them works while typing. Alt is chosen
 * over Ctrl and Meta because those belong to the browser and the operating system: `Ctrl+P` is
 * print and `Cmd+1` is a tab, and taking either from a physician who has used them for twenty
 * years would be taking something that is not ours.
 *
 * # The keys that are not shortcuts, and matter more
 *
 * The fast path is not a shortcut at all. **Enter** takes the focused strength in the combobox and
 * then writes the line; **Escape** abandons the row and returns to the search box; **Tab** reaches
 * every control in order. Those are the keys a four-item prescription is actually made of, and
 * they are handled in the components that own the fields — a global listener that swallowed Enter
 * would break the browser's own form behaviour everywhere else on the page.
 *
 * # Why the list is exported
 *
 * The on-screen help and this table are one array, so the card a physician learns from cannot
 * drift from what the screen does.
 */

export interface PrescriptionShortcutHandlers {
  focusSearch: () => void;
  focusLines: () => void;
  focusSafety: () => void;
  togglePreview: () => void;
  toggleHelp: () => void;
  carryForward: () => void;
}

export const PRESCRIPTION_SHORTCUTS = [
  { keys: 'Alt+M', action: 'search' },
  { keys: 'Alt+L', action: 'lines' },
  { keys: 'Alt+S', action: 'safety' },
  { keys: 'Alt+P', action: 'preview' },
  { keys: 'Alt+C', action: 'carryForward' },
  { keys: 'Alt+/', action: 'help' },
  { keys: 'Enter', action: 'commit' },
  { keys: 'Escape', action: 'abandon' },
] as const;

export function usePrescriptionShortcuts(handlers: PrescriptionShortcutHandlers): void {
  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.defaultPrevented) return;
      // Alt alone. Ctrl and Meta belong to the browser; a chord with both is somebody's
      // window manager.
      if (!event.altKey || event.ctrlKey || event.metaKey) return;

      // `event.key` under Alt is the composed character on some layouts, so the physical key
      // is read from `event.code` where it exists. Without this, Alt+M on a Bengali keyboard
      // layout does nothing and the shortcut looks broken to exactly the people the
      // bilingual requirement is for.
      const key = (event.code || '').replace(/^Key/, '').toLowerCase() || event.key.toLowerCase();

      switch (key) {
        case 'm':
          handlers.focusSearch();
          break;
        case 'l':
          handlers.focusLines();
          break;
        case 's':
          handlers.focusSafety();
          break;
        case 'p':
          handlers.togglePreview();
          break;
        case 'c':
          handlers.carryForward();
          break;
        case 'slash':
        case '/':
        case '?':
          handlers.toggleHelp();
          break;
        default:
          return;
      }
      event.preventDefault();
    }

    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [handlers]);
}
