/**
 * Every route in the station app, in one place.
 *
 * Same rule as the web shell: this is the single source, and it is load-bearing. The
 * screens render from it, `navigation.test.ts` asserts each entry has a file on disk,
 * and nothing on disk may exist without an entry — a screen no list points at is a
 * screen no operator can reach.
 *
 * §14.10 names the groups: (auth), (queue), (station), (patient), (sync); CP18 adds
 * (device), reachable signed in or out. A group here
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
    href: '/device',
    file: '(device)/device.tsx',
    labelKey: 'screen.device',
    checkpoint: null,
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
    /*
     * The values somebody has asked this operator to look at again (CP62).
     *
     * In the station group rather than a group of its own: a correction is answered at the
     * station that took the measurement, by the person who took it, and §14.10's groups are
     * about where an operator is rather than about what a screen is called. Reached from the
     * shell's own notice, which appears on whatever screen they happen to be on — that is
     * criterion 4, and a screen only reachable by somebody who already knew to look for it
     * would satisfy the words and none of the point.
     */
    href: '/corrections',
    file: '(station)/corrections.tsx',
    labelKey: 'screen.corrections',
    checkpoint: 'CP62',
  },
  {
    /*
     * The operator's own quality record (CP63, ADR-0029).
     *
     * In the station group beside the correction queue it counts, and for the same reason:
     * §14.10's groups are about where an operator is, and the person reading this is at their
     * station at the end of a shift. Only their own — the supervisor's list needs
     * `quality.read.team` and is web-only, because reading a colleague's month usefully means
     * having the correction chain and the instrument log open beside it.
     *
     * Reached from the correction queue's standing link, and from the shell's own line when a
     * note is raised. Both, rather than either: a record reachable only on the day somebody
     * writes a note about you is a record you read as something kept from you until it is
     * used against you, which is the reading `/v1/quality/me` asks for no permission in order
     * to avoid.
     */
    href: '/quality',
    file: '(station)/quality.tsx',
    labelKey: 'screen.quality',
    checkpoint: 'CP63',
  },
  {
    href: '/register',
    file: '(patient)/register.tsx',
    labelKey: 'screen.register',
    checkpoint: 'CP33',
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
