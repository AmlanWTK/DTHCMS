import NetInfo from '@react-native-community/netinfo';
import { useEffect, useState } from 'react';

/**
 * Whether the device believes it can reach a network.
 *
 * The same honesty as the web shell's banner: this is a statement about the device, not
 * about the clinic server, and the wording downstream reflects that. The strong signal —
 * a sync that cannot deliver — arrives with the outbox at CP64, and this hook is the
 * weak early warning that the station app shows meanwhile.
 *
 * Starts as online. A shell that flashed "no connection" on every cold start before the
 * first probe resolved would teach operators to ignore the banner.
 */
export function useConnectivity(): { online: boolean } {
  const [online, setOnline] = useState(true);

  useEffect(() => {
    const unsubscribe = NetInfo.addEventListener((state) => {
      setOnline(state.isConnected !== false);
    });
    return unsubscribe;
  }, []);

  return { online };
}
