import type { ReactNode } from 'react';
import { View } from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { useTranslations } from 'use-intl';

import { theme, useTokens } from '@/lib/tokens';
import { AppText } from '@/components/AppText';
import { ConnectionIndicator } from '@/components/ConnectionIndicator';
import { LanguageToggle } from '@/components/LanguageToggle';
import { OfflineBanner } from '@/components/OfflineBanner';
import { RoleSwitcher } from '@/components/RoleSwitcher';
import { SignOutButton } from '@/components/SignOutButton';
import { SyncPill } from '@/components/SyncPill';
import { CorrectionNotice } from '@/features/corrections';
import { QualityNotice } from '@/features/quality';

/**
 * The frame every station screen sits in: safe area, title, the language switch and the
 * connection banner. The mobile analogue of the web AppShell, sized for a hand.
 *
 * The correction notice is here for the same reason the role switcher is: it has to be true of
 * every screen. CP62 routes a flagged value to whoever typed it, and criterion 4 is that they
 * are told **on their device** — an operator taking vitals has no reason to go looking at a
 * queue of corrections, so the queue has to come to them. It draws nothing at all when there
 * is nothing waiting, which is nearly always.
 */
export function ScreenShell({ titleKey, children }: { titleKey: string; children: ReactNode }) {
  const t = useTranslations();
  const { colors } = useTokens();

  return (
    <SafeAreaView className="flex-1" style={{ backgroundColor: colors.surface.sunken }}>
      <View className="flex-1" style={{ padding: theme.spacing['4'], gap: theme.spacing['4'] }}>
        <View className="flex-row items-center justify-between">
          <View>
            <AppText size="xs" style={{ color: colors.text.muted }}>
              {t('app.name')}
            </AppText>
            <AppText size="xl" weight="bold">
              {t(titleKey as never)}
            </AppText>
          </View>
          <View className="flex-row items-center" style={{ gap: theme.spacing['2'] }}>
            {/*
              Before the connection indicator, because the two answer different questions and this
              is the more important one. "Live" is about whether this screen is current; the pill is
              about whether a morning of measurements exists anywhere but here. An operator with
              thirty seconds between patients reads the first thing in the row.

              In the shell rather than on the sync screen because the sync screen is somewhere an
              operator goes when they already suspect something. §13.9's whole argument is that the
              indicator has to be true; CP67's addition is that it has to be *where they are*.
            */}
            <SyncPill />
            <ConnectionIndicator />
            <LanguageToggle />
            <SignOutButton />
          </View>
        </View>

        {/* Always on screen, because criterion 3 asks that the active role be
            unmistakable — and it is also the only way to change hats, so an operator
            cannot be wearing one they cannot see (CP41). */}
        <RoleSwitcher />

        <OfflineBanner />

        {/* Below the connection banner and above the screen. A colleague's question about a
            number is not an alarm and must not be drawn above the fact that this tablet
            cannot reach the server — but it must be above the fold, because a notice an
            operator has to scroll to is a notice that waits until tomorrow. */}
        <CorrectionNotice />

        {/* Below the correction notice, because a value waiting to be looked at again is a
            thing to do now and a note on a record is a thing to read. Both are in the shell
            for the same reason: CP62's criterion 4 and ADR-0029 both turn on the person
            being told on their own device rather than by their supervisor, and neither can
            rely on somebody opening a screen they have no reason to open. It draws nothing
            at all when there is no open note, which is nearly always. */}
        <QualityNotice />

        <View className="flex-1">{children}</View>
      </View>
    </SafeAreaView>
  );
}
