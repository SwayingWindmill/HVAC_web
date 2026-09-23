import { create } from 'zustand';

interface UiState {
  currentSiteId: string | null;
  sidebarCollapsed: boolean;
  setCurrentSiteId: (siteId: string | null) => void;
  setSidebarCollapsed: (collapsed: boolean) => void;
}

/** Client-only UI state. Server facts remain in ShellRuntime/Query. */
export const useUiStore = create<UiState>((set) => ({
  currentSiteId: null,
  sidebarCollapsed: false,
  setCurrentSiteId: (currentSiteId) => set({ currentSiteId }),
  setSidebarCollapsed: (sidebarCollapsed) => set({ sidebarCollapsed }),
}));
