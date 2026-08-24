import { create } from 'zustand';

import type { Role } from '@/lib/permissions';

/**
 * The session store.
 *
 * A stub until CP16, and shaped so that replacing the stub does not change any consumer:
 * components ask for `activeRole`, not for "the fake role".
 *
 * Nothing here is persisted. That is not an omission — see ADR-0010. Session state lives
 * in an httpOnly cookie the browser sends and JavaScript cannot read; this store holds
 * only what the server told us about the current session, and it is gone on reload,
 * where it is fetched again.
 */

export interface SessionUser {
  id: string;
  displayName: string;
  roles: Role[];
}

interface SessionState {
  user: SessionUser | null;
  activeRole: Role | null;
  setUser: (user: SessionUser | null) => void;
  setActiveRole: (role: Role) => void;
}

/**
 * The placeholder operator.
 *
 * CP10 has no authentication, and a shell with no session renders as a permission-denied
 * page everywhere, which would make the route groups impossible to review. So the shell
 * runs as a physician until CP16 replaces this with a real session. It is a constant in
 * one place rather than a scattering of `?? 'physician'` defaults.
 */
export const PLACEHOLDER_USER: SessionUser = {
  id: '00000000-0000-0000-0000-000000000000',
  displayName: 'Placeholder operator',
  roles: ['physician', 'admin', 'researcher', 'pharmacy', 'crm', 'qa', 'executive', 'operator'],
};

export const useSessionStore = create<SessionState>((set) => ({
  user: PLACEHOLDER_USER,
  activeRole: 'physician',
  setUser: (user) => set({ user, activeRole: user?.roles[0] ?? null }),
  setActiveRole: (activeRole) => set({ activeRole }),
}));
