import { create } from 'zustand';
import {
  mockSuggestions, mockFdd, mockWorkOrders,
  type Suggestion, type FddEntry, type WorkOrder, type TicketStatus,
} from '@/mock/data';

// Cross-page state for the optimize / FDD / alarms loop (#7 paradigm).
// Mock-seeded; swap to src/api calls later. Keeps 提交审批 / 生成工单 flowing across routes.
interface OpsState {
  suggestions: Suggestion[];
  workOrders: WorkOrder[];
  generatedFddIds: string[];
  submitApproval: (id: string) => void; // draft -> pending
  approve: (id: string) => void; // pending -> approved
  reject: (id: string) => void; // pending -> rejected
  dispatch: (id: string) => void; // approved -> dispatched (real, 二次确认)
  simulateDispatch: (id: string) => void; // approved -> dispatched (demo 一键模拟)
  generateWorkOrder: (fdd: FddEntry) => void; // FDD -> /alarms work order
  setTicketStatus: (id: string, status: TicketStatus) => void;
}

const toStatus = (list: Suggestion[], id: string, status: Suggestion['status']) =>
  list.map((s) => (s.id === id ? { ...s, status } : s));

export const useOps = create<OpsState>((set) => ({
  suggestions: mockSuggestions.map((s) => ({ ...s })),
  workOrders: mockWorkOrders.map((w) => ({ ...w })),
  generatedFddIds: [],
  submitApproval: (id) => set((st) => ({ suggestions: toStatus(st.suggestions, id, 'pending') })),
  approve: (id) => set((st) => ({ suggestions: toStatus(st.suggestions, id, 'approved') })),
  reject: (id) => set((st) => ({ suggestions: toStatus(st.suggestions, id, 'rejected') })),
  dispatch: (id) => set((st) => ({ suggestions: toStatus(st.suggestions, id, 'dispatched') })),
  simulateDispatch: (id) => set((st) => ({ suggestions: toStatus(st.suggestions, id, 'dispatched') })),
  generateWorkOrder: (fdd) =>
    set((st) => {
      if (st.generatedFddIds.includes(fdd.id)) return st;
      const wo: WorkOrder = {
        id: `WO-${Math.floor(Math.random() * 9000 + 1000)}`,
        source: 'fdd',
        device: fdd.device,
        severity: fdd.severity,
        title: fdd.phenomenon,
        description: `FDD ${fdd.id} 生成：${fdd.recommended}`,
        status: 'open',
        createdAt: '刚刚',
      };
      return {
        workOrders: [wo, ...st.workOrders],
        generatedFddIds: [...st.generatedFddIds, fdd.id],
      };
    }),
  setTicketStatus: (id, status) =>
    set((st) => ({ workOrders: st.workOrders.map((w) => (w.id === id ? { ...w, status } : w)) })),
}));

// FDD list is static; expose it for the page (no mutation needed).
export const fddList = mockFdd;
