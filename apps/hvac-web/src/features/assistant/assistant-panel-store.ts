import { create } from 'zustand';

// Whether the assistant panel is open, and a question a page asked it to prefill.
// The panel is chrome around every Site page, so this is UI state rather than URL state.

interface AssistantPanelState {
  readonly open: boolean;
  /** Prefilled by a page's 解释; the composer puts it in its input and clears it. */
  readonly draft: string | null;
  readonly toggle: () => void;
  readonly close: () => void;
  readonly explain: (question: string) => void;
  readonly clearDraft: () => void;
}

export const useAssistantPanel = create<AssistantPanelState>((set) => ({
  open: false,
  draft: null,
  toggle: () => set((state) => ({ open: !state.open })),
  close: () => set({ open: false }),
  explain: (question) => set({ open: true, draft: question }),
  clearDraft: () => set({ draft: null }),
}));
