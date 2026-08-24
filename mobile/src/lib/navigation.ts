/**
 * Every route in the station app, in one place.
 *
 * Same rule as the web shell: this is the single source, and it is load-bearing. The
 * screens render from it, `navigation.test.ts` asserts each entry has a file on disk,
 * and nothing on disk may exist without an entry — a screen no list points at is a
 * screen no operator can reach.
 *
 * §14.10 names the groups: (auth), (queue), (station), (patient), (sync). A group here
 * is organisation, not URL — the path is the file name inside it.
 */

export interface MobileRoute {
  /** Path as Expo Router sees it. */
  href: string;
  /** File under src/app, so the test can find it. */
  file: string;
  /** Key under `screen.` in the message files. */
  labelKey: string;
  /** The checkpoint that fills the screen, or null for one that is real at CP11. */
  checkpoint: string | null;
}

export const MOBILE_ROUTES: readonly MobileRoute[] = [
  {
    href: '/login',
    file: '(auth)/login.tsx',
    labelKey: 'screen.login',
    checkpoint: 'CP16',
  },
  {
    href: '/queue',
    file: '(queue)/queue.tsx',
    labelKey: 'screen.queue',
    checkpoint: 'CP33',
  },
  {
    href: '/station',
    file: '(station)/station.tsx',
    labelKey: 'screen.station',
    checkpoint: 'CP45',
  },
  {
    href: '/patient',
    file: '(patient)/patient.tsx',
    labelKey: 'screen.patient',
    checkpoint: 'CP42',
  },
  {
    href: '/sync',
    file: '(sync)/sync.tsx',
    labelKey: 'screen.sync',
    checkpoint: 'CP64',
  },
];
