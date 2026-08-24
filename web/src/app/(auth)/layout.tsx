import type { ReactNode } from 'react';

/*
 * The signed-out frame: no shell.
 *
 * A person who is not signed in has no navigation to be offered, and rendering a sidebar
 * full of areas they cannot reach would be an inventory of the application handed to
 * whoever is at the keyboard.
 */
export default function AuthLayout({ children }: { children: ReactNode }) {
  return <div className="app-centred">{children}</div>;
}
