import { create } from 'zustand';

/**
 * Interface state that is not server state and not URL state.
 *
 * Deliberately small. Anything that should survive a reload or be shareable belongs in
 * the URL; anything that came from the server belongs in TanStack Query. What is left is
 * this: whether a panel is open.
 */

interface UiState {
  sidebarOpen: boolean;
  setSidebarOpen: (open: boolean) => void;
  toggleSidebar: () => void;
}

export const useUiStore = create<UiState>((set) => ({
  sidebarOpen: false,
  setSidebarOpen: (sidebarOpen) => set({ sidebarOpen }),
  toggleSidebar: () => set((state) => ({ sidebarOpen: !state.sidebarOpen })),
}));
