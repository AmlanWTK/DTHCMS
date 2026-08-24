import { create } from 'zustand';

/**
 * The session — a stub with a real shape, mirroring the web shell's.
 *
 * CP16 replaces the contents; consumers ask for `operator`, not for "the fake operator",
 * so the replacement changes no screen. Nothing here persists: the credential that will
 * outlive a restart is CP16's business and lives behind the secure-storage wrapper.
 */

export interface OperatorSession {
  id: string;
  displayName: string;
}

export const PLACEHOLDER_OPERATOR: OperatorSession = {
  id: '00000000-0000-0000-0000-000000000000',
  displayName: 'Placeholder operator',
};

interface SessionState {
  operator: OperatorSession | null;
  setOperator: (operator: OperatorSession | null) => void;
}

export const useSession = create<SessionState>((set) => ({
  operator: PLACEHOLDER_OPERATOR,
  setOperator: (operator) => set({ operator }),
}));
