import { create } from 'zustand';

interface RealUiState {
  currentSiteId: string | null;
  sidebarCollapsed: boolean;
  setCurrentSiteId: (siteId: string | null) => void;
  setSidebarCollapsed: (collapsed: boolean) => void;
}

/** Real-only client state. Server facts remain in ShellRuntime/Query. */
export const useRealUiStore = create<RealUiState>((set) => ({
  currentSiteId: null,
  sidebarCollapsed: false,
  setCurrentSiteId: (currentSiteId) => set({ currentSiteId }),
  setSidebarCollapsed: (sidebarCollapsed) => set({ sidebarCollapsed }),
}));
