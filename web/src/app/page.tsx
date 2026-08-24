import { redirect } from 'next/navigation';

/**
 * The root path.
 *
 * At CP16 this becomes a decision — signed in goes to the landing screen for the active
 * role, signed out goes to /login. Until then it is one destination, in one place, so
 * that when the decision arrives there is exactly one file to change.
 */
export default function RootPage() {
  redirect('/dashboard');
}
