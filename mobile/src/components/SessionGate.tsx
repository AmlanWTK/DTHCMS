import { useRouter, useSegments } from 'expo-router';
import { useEffect, type ReactNode } from 'react';
import { ActivityIndicator, View } from 'react-native';

import { useTokens } from '@/lib/tokens';
import { useSession } from '@/stores/session';

/**
 * Where a person may be, given who they are.
 *
 * Signed out and anywhere but the sign-in screen: sent to sign in. Signed in and on the
 * sign-in screen: sent to the queue. Not yet known: a spinner, not the sign-in form —
 * the usual answer on a clinic tablet is "the same nurse as an hour ago", recovered from
 * the Keystore without a keystroke, and a form that flashed at her every restart would
 * teach her to distrust it.
 *
 * A courtesy, not a control. The server refuses every request a bypassed gate makes.
 */
export function SessionGate({ children }: { children: ReactNode }) {
  const status = useSession((state) => state.status);
  const hydrate = useSession((state) => state.hydrate);
  const segments = useSegments();
  const router = useRouter();
  const { colors } = useTokens();

  const onSignIn = segments[0] === '(auth)';
  // The device screen is reachable signed out: an administrator enrols a tablet before
  // anyone has signed in on it.
  const open = onSignIn || segments[0] === '(device)';

  useEffect(() => {
    if (status === 'unknown') void hydrate();
  }, [status, hydrate]);

  useEffect(() => {
    if (status === 'anonymous' && !open) router.replace('/login');
    if (status === 'authenticated' && onSignIn) router.replace('/queue');
  }, [status, onSignIn, open, router]);

  if (status === 'unknown') {
    return (
      <View
        accessibilityRole="progressbar"
        style={{
          flex: 1,
          alignItems: 'center',
          justifyContent: 'center',
          backgroundColor: colors.surface.sunken,
        }}
      >
        <ActivityIndicator color={colors.brand.solid} />
      </View>
    );
  }

  return <>{children}</>;
}
