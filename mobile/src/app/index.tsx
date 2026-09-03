import { Redirect } from 'expo-router';

import { useSession } from '@/stores/session';

/**
 * The root route: the signed-in / signed-out decision, in one place.
 *
 * The gate in the root layout already sends an anonymous visitor to sign in from
 * anywhere, so this only has to pick a home for a person who is signed in. It reads the
 * store rather than assuming, so that "/" never points a signed-out tablet at the queue.
 */
export default function Index() {
  const status = useSession((state) => state.status);
  return <Redirect href={status === 'authenticated' ? '/queue' : '/login'} />;
}
