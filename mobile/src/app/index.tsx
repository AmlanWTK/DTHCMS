import { Redirect } from 'expo-router';

/**
 * The root route. One destination, in one place — at CP16 this becomes the signed-in /
 * signed-out decision, and there is exactly one file to change.
 */
export default function Index() {
  return <Redirect href="/queue" />;
}
