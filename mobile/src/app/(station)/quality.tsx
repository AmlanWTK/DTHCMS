import { ScrollView } from 'react-native';

import { ScreenShell } from '@/components/ScreenShell';
import { MyQualityRecord } from '@/features/quality';
import { theme } from '@/lib/tokens';

/**
 * My own record (CP63, §4.3, ADR-0029).
 *
 * # Why this sits in the station group
 *
 * §14.10's groups are about where an operator is, not about what a screen is called, and the
 * person reading this is at their station at the end of a shift — the same place, and often
 * the same minute, as the correction queue this record counts. It is not a review surface: the
 * supervisor's half of CP63 needs `quality.read.team` and is web-only, because reading a
 * colleague's month usefully means having the correction chain and the instrument log open
 * beside it.
 *
 * # How anybody gets here
 *
 * Two ways, and the second matters more than the first.
 *
 * The shell's own line, when something has been written on the record — that is ADR-0029's
 * "no ambush": the server publishes a raised note to the person it is about at the moment it
 * raises it, so a supervisor is never the first to mention it.
 *
 * And a standing link on the correction queue, which is there whether anything has been
 * written or not. A record reachable only on the day somebody raises a note is a record an
 * operator reads as something kept from them until it is used against them — which is exactly
 * the reading `/v1/quality/me` asks no permission in order to avoid.
 *
 * # It scrolls, and there is nothing else on it
 *
 * No patient header, no station header, no queue. This screen is about the person holding the
 * tablet; a patient banner above it would put somebody else's record on a screen that is
 * explicitly about the reader's own work.
 */
export default function QualityScreen() {
  return (
    <ScreenShell titleKey="screen.quality">
      <ScrollView contentContainerStyle={{ paddingBottom: theme.spacing['8'] }}>
        <MyQualityRecord />
      </ScrollView>
    </ScreenShell>
  );
}
