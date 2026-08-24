import {
  Inter_400Regular,
  Inter_500Medium,
  Inter_600SemiBold,
  Inter_700Bold,
} from '@expo-google-fonts/inter';
import {
  NotoSansBengali_400Regular,
  NotoSansBengali_500Medium,
  NotoSansBengali_600SemiBold,
  NotoSansBengali_700Bold,
} from '@expo-google-fonts/noto-sans-bengali';
import { QueryClientProvider } from '@tanstack/react-query';
import { useFonts } from 'expo-font';
import { Stack } from 'expo-router';
import * as SplashScreen from 'expo-splash-screen';
import { StatusBar } from 'expo-status-bar';
import { useEffect } from 'react';

import '@/styles/global.css';

import { installCrashHandler } from '@/lib/crash';
import { I18nProvider } from '@/lib/i18n';
import { createQueryClient } from '@/lib/query';
import { useTokens } from '@/lib/tokens';

/*
 * Fonts are bundled in the binary, not fetched.
 *
 * A station tablet may be offline for a whole clinic session, and a Bengali fallback
 * face on low-end Android frequently has poor conjunct coverage — text degrades into
 * broken ligatures with nothing reporting an error. These packages ship the .ttf files
 * through npm, so the same faces the web self-hosts are compiled into the APK.
 *
 * The splash screen stays up until the fonts are in. A first paint in a fallback face
 * that then flashes into the real one is worse than 200ms more of splash.
 */
void SplashScreen.preventAutoHideAsync();

// Before the first render, so a crash during startup is also captured and scrubbed.
installCrashHandler();

/*
 * One client for the process, created at module scope rather than in the component.
 *
 * A client rebuilt on re-render throws away every cached response with it — which on a
 * station tablet means re-fetching over a connection that may not be there. Expo Router
 * remounts this layout on navigation, so this is not a hypothetical.
 */
const queryClient = createQueryClient();

export default function RootLayout() {
  const { colors, scheme } = useTokens();

  const [fontsLoaded] = useFonts({
    Inter: Inter_400Regular,
    'Inter-Medium': Inter_500Medium,
    'Inter-SemiBold': Inter_600SemiBold,
    'Inter-Bold': Inter_700Bold,
    NotoSansBengali: NotoSansBengali_400Regular,
    'NotoSansBengali-Medium': NotoSansBengali_500Medium,
    'NotoSansBengali-SemiBold': NotoSansBengali_600SemiBold,
    'NotoSansBengali-Bold': NotoSansBengali_700Bold,
  });

  useEffect(() => {
    if (fontsLoaded) void SplashScreen.hideAsync();
  }, [fontsLoaded]);

  if (!fontsLoaded) return null;

  return (
    <QueryClientProvider client={queryClient}>
      <I18nProvider>
        <StatusBar style={scheme === 'dark' ? 'light' : 'dark'} />
        <Stack
          screenOptions={{
            headerShown: false,
            contentStyle: { backgroundColor: colors.surface.sunken },
          }}
        />
      </I18nProvider>
    </QueryClientProvider>
  );
}
