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
  generateWorkOrder: (fdd: FddEntry) => string; // FDD -> /alarms work order
  setTicketStatus: (id: string, status: TicketStatus) => void;
}

const toStatus = (list: Suggestion[], id: string, status: Suggestion['status']) =>
  list.map((suggestion) => (suggestion.id === id ? { ...suggestion, status } : suggestion));

const nextWorkOrderId = (orders: WorkOrder[]) => {
  const maxId = orders.reduce((max, order) => {
    const value = Number(order.id.replace(/^WO-/, ''));
    return Number.isFinite(value) ? Math.max(max, value) : max;
  }, 500);
  return `WO-${maxId + 1}`;
};

export const useOps = create<OpsState>((set, get) => ({
  suggestions: mockSuggestions.map((suggestion) => ({ ...suggestion })),
  workOrders: mockWorkOrders.map((order) => ({ ...order })),
  generatedFddIds: [],
  submitApproval: (id) => set((state) => ({ suggestions: toStatus(state.suggestions, id, 'pending') })),
  approve: (id) => set((state) => ({ suggestions: toStatus(state.suggestions, id, 'approved') })),
  reject: (id) => set((state) => ({ suggestions: toStatus(state.suggestions, id, 'rejected') })),
  dispatch: (id) => set((state) => ({ suggestions: toStatus(state.suggestions, id, 'dispatched') })),
  simulateDispatch: (id) => set((state) => ({ suggestions: toStatus(state.suggestions, id, 'dispatched') })),
  generateWorkOrder: (fdd) => {
    const existing = get().workOrders.find((order) => order.sourceFddId === fdd.id);
    if (existing) return existing.id;

    const id = nextWorkOrderId(get().workOrders);
    const workOrder: WorkOrder = {
      id,
      source: 'fdd',
      sourceFddId: fdd.id,
      linkedAssetId: fdd.linkedAssetId,
      linkedSuggestionId: fdd.linkedSuggestionId,
      device: fdd.device,
      severity: fdd.severity,
      title: fdd.phenomenon,
      description: `FDD ${fdd.id} 生成：${fdd.recommended}`,
      impact: fdd.impact,
      recommendation: fdd.recommended,
      location: fdd.scope,
      rule: `来源诊断 ${fdd.id}`,
      status: 'open',
      createdAt: '刚刚',
    };

    set((state) => ({
      workOrders: [workOrder, ...state.workOrders],
      generatedFddIds: state.generatedFddIds.includes(fdd.id)
        ? state.generatedFddIds
        : [...state.generatedFddIds, fdd.id],
    }));
    return id;
  },
  setTicketStatus: (id, status) =>
    set((state) => ({
      workOrders: state.workOrders.map((order) => (
        order.id === id
          ? {
            ...order,
            status,
            assignee: status === 'assigned' && !order.assignee ? '运维值班组' : order.assignee,
          }
          : order
      )),
    })),
}));

// FDD list is static; expose it for the page (no mutation needed).
export const fddList = mockFdd;
