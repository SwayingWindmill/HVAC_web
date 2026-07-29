import { useCallback, useEffect, useMemo } from 'react';
import { create } from 'zustand';
import { createJSONStorage, persist } from 'zustand/middleware';
import { useLocation } from 'react-router';
import { useAgent, useCopilotChatConfiguration } from '@copilotkit/react-core/v2';
import { useAiApplicationContext } from './context';

export type AiThreadKind = 'conversation' | 'investigation' | 'report';
export type AiThreadStatus = 'active' | 'waiting' | 'completed' | 'failed';

export type StoredAgentMessage = {
  id?: string;
  role?: string;
  content?: unknown;
  [key: string]: unknown;
};

export type AiThreadRecord = {
  id: string;
  title: string;
  titleLocked: boolean;
  kind: AiThreadKind;
  status: AiThreadStatus;
  scopeLabel: string;
  route: string;
  summary: string;
  createdAt: number;
  updatedAt: number;
  messageCount: number;
  pinned: boolean;
  archived: boolean;
  messages: StoredAgentMessage[];
};

type ThreadCreateInput = {
  scopeLabel: string;
  route: string;
  title?: string;
  kind?: AiThreadKind;
  messages?: StoredAgentMessage[];
  status?: AiThreadStatus;
};

type ThreadSyncInput = {
  scopeLabel: string;
  route: string;
  running: boolean;
};

type AiHistoryState = {
  threads: AiThreadRecord[];
  activeThreadId: string;
  popupHistoryOpen: boolean;
  createThread: (input: ThreadCreateInput) => string;
  selectThread: (threadId: string) => void;
  syncThreadMessages: (threadId: string, messages: readonly unknown[], input: ThreadSyncInput) => void;
  renameThread: (threadId: string, title: string) => void;
  togglePinned: (threadId: string) => void;
  archiveThread: (threadId: string, archived?: boolean) => void;
  deleteThread: (threadId: string) => void;
  setPopupHistoryOpen: (open: boolean) => void;
};

const HISTORY_STORAGE_KEY = 'hvac-ai-thread-history-v1';
const EMPTY_THREAD_TITLE = '新会话';
const now = Date.now();

const seedMessage = (id: string, role: 'user' | 'assistant', content: string): StoredAgentMessage => ({
  id,
  role,
  content,
});

const seedThreads: AiThreadRecord[] = [
  {
    id: 'demo-cost-peak',
    title: '峰时段费用异常分析',
    titleLocked: true,
    kind: 'investigation',
    status: 'completed',
    scopeLabel: '总部大楼 · 成本与绩效 · 本月',
    route: '/cost',
    summary: '峰时费用占比 42%，主要由冷机高负荷与末端延时运行共同造成。',
    createdAt: now - 1000 * 60 * 60 * 27,
    updatedAt: now - 1000 * 60 * 42,
    messageCount: 4,
    pinned: true,
    archived: false,
    messages: [
      seedMessage('demo-cost-user-1', 'user', '分析本月峰时段费用是否过高。'),
      seedMessage('demo-cost-assistant-1', 'assistant', '本月峰时费用占比约 42%，高于当前运营基线。主要增量集中在工作日 13:00–17:00，建议继续核对冷机负荷和末端延时运行。'),
      seedMessage('demo-cost-user-2', 'user', '主要贡献设备有哪些？'),
      seedMessage('demo-cost-assistant-2', 'assistant', '当前贡献度最高的是 CH-02 冷水机组、AHU-03 空调机组和一次冷冻水泵组。建议先从 CH-02 的 COP 变化和 AHU-03 的停机时刻展开调查。'),
    ],
  },
  {
    id: 'demo-chiller-cop',
    title: 'CH-02 COP 下降调查',
    titleLocked: true,
    kind: 'investigation',
    status: 'waiting',
    scopeLabel: '总部大楼 · CH-02 冷水机组',
    route: '/assets?device=CH-02',
    summary: '已确认冷凝侧效率下降，等待核对冷却塔风机反馈。',
    createdAt: now - 1000 * 60 * 60 * 52,
    updatedAt: now - 1000 * 60 * 60 * 4,
    messageCount: 3,
    pinned: false,
    archived: false,
    messages: [
      seedMessage('demo-cop-user-1', 'user', '调查 CH-02 最近一周 COP 下降原因。'),
      seedMessage('demo-cop-assistant-1', 'assistant', 'CH-02 在相同负荷下的 COP 较基线下降约 9%。冷凝温差持续偏高，且同组设备表现正常，冷凝侧效率下降是当前最可能根因。'),
      seedMessage('demo-cop-assistant-2', 'assistant', '仍需现场确认冷却塔风机反馈与冷凝器清洁状态，当前调查状态为等待人工核验。'),
    ],
  },
  {
    id: 'demo-weekly-report',
    title: '本周运营日报',
    titleLocked: true,
    kind: 'report',
    status: 'completed',
    scopeLabel: '总部大楼 · 全系统 · 本周',
    route: '/dashboard',
    summary: '本周综合 COP 4.81，完成 3 项优化评审，2 条工单存在 SLA 风险。',
    createdAt: now - 1000 * 60 * 60 * 72,
    updatedAt: now - 1000 * 60 * 60 * 20,
    messageCount: 2,
    pinned: false,
    archived: false,
    messages: [
      seedMessage('demo-report-user-1', 'user', '生成本周运营日报。'),
      seedMessage('demo-report-assistant-1', 'assistant', '本周总部大楼综合 COP 为 4.81，运行总体稳定。当前有 2 条高风险诊断、2 条 SLA 风险工单和 4 条待决策优化建议，需要在下周值班计划中持续跟进。'),
    ],
  },
  {
    id: 'demo-ahu-night',
    title: 'AHU-03 夜间延迟停机',
    titleLocked: true,
    kind: 'conversation',
    status: 'active',
    scopeLabel: '总部大楼 · AHU-03 空调机组',
    route: '/alarms',
    summary: '连续 3 晚延迟停机，预计额外消耗 344 kWh/晚。',
    createdAt: now - 1000 * 60 * 60 * 96,
    updatedAt: now - 1000 * 60 * 60 * 31,
    messageCount: 2,
    pinned: false,
    archived: false,
    messages: [
      seedMessage('demo-ahu-user-1', 'user', 'AHU-03 为什么连续夜间延迟停机？'),
      seedMessage('demo-ahu-assistant-1', 'assistant', 'AHU-03 连续 3 晚在计划停机后继续运行约 70 分钟，控制时序与人工覆盖记录需要进一步核对。预计额外消耗约 344 kWh/晚。'),
    ],
  },
  {
    id: 'demo-savings',
    title: '6 月节能收益总结',
    titleLocked: true,
    kind: 'report',
    status: 'completed',
    scopeLabel: '总部大楼 · 成本与绩效 · 6 月',
    route: '/cost',
    summary: '累计节能收益 ¥127,868，主要来自冷机群控和峰谷策略。',
    createdAt: now - 1000 * 60 * 60 * 24 * 8,
    updatedAt: now - 1000 * 60 * 60 * 24 * 3,
    messageCount: 2,
    pinned: false,
    archived: true,
    messages: [
      seedMessage('demo-saving-user-1', 'user', '总结 6 月节能收益。'),
      seedMessage('demo-saving-assistant-1', 'assistant', '6 月累计节能收益为 ¥127,868，主要来源为冷机群控优化、峰谷电价策略和夜间停机时序修正。'),
    ],
  },
];

const initialThreadId = 'current-thread';

const createInitialThread = (): AiThreadRecord => ({
  id: initialThreadId,
  title: EMPTY_THREAD_TITLE,
  titleLocked: false,
  kind: 'conversation',
  status: 'active',
  scopeLabel: '总部大楼 · 总览驾驶舱',
  route: '/dashboard',
  summary: '从当前页面开始新的运维分析。',
  createdAt: now,
  updatedAt: now,
  messageCount: 0,
  pinned: false,
  archived: false,
  messages: [],
});

const createThreadId = () => {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return `thread-${crypto.randomUUID()}`;
  return `thread-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
};

const cloneMessages = (messages: readonly unknown[]): StoredAgentMessage[] => {
  try {
    return JSON.parse(JSON.stringify(messages)) as StoredAgentMessage[];
  } catch {
    return messages.flatMap((message) => {
      if (!message || typeof message !== 'object') return [];
      const raw = message as StoredAgentMessage;
      return [{
        id: raw.id,
        role: raw.role,
        content: typeof raw.content === 'string' ? raw.content : String(raw.content ?? ''),
      }];
    });
  }
};

const textFromMessage = (message: StoredAgentMessage | undefined) => {
  if (!message) return '';
  if (typeof message.content === 'string') return message.content;
  if (Array.isArray(message.content)) {
    return message.content
      .map((part) => {
        if (typeof part === 'string') return part;
        if (part && typeof part === 'object' && 'text' in part) return String((part as { text?: unknown }).text ?? '');
        return '';
      })
      .filter(Boolean)
      .join(' ');
  }
  return '';
};

const stripContextEnvelope = (value: string) => value.replace(/^\[当前页面：[^\]]+\]\s*/, '').trim();

const truncate = (value: string, length: number) => {
  const normalized = value.replace(/\s+/g, ' ').trim();
  if (normalized.length <= length) return normalized;
  return `${normalized.slice(0, Math.max(1, length - 1))}…`;
};

const deriveTitle = (messages: StoredAgentMessage[]) => {
  const userMessage = messages.find((message) => message.role === 'user');
  const text = stripContextEnvelope(textFromMessage(userMessage));
  return text ? truncate(text, 28) : EMPTY_THREAD_TITLE;
};

const deriveSummary = (messages: StoredAgentMessage[], fallback: string) => {
  const assistantMessage = [...messages].reverse().find((message) => message.role === 'assistant');
  const userMessage = [...messages].reverse().find((message) => message.role === 'user');
  const text = textFromMessage(assistantMessage) || stripContextEnvelope(textFromMessage(userMessage));
  return text ? truncate(text, 72) : fallback;
};

const sortThreads = (threads: AiThreadRecord[]) => [...threads].sort((a, b) => {
  if (a.pinned !== b.pinned) return a.pinned ? -1 : 1;
  return b.updatedAt - a.updatedAt;
});

export const useAiHistory = create<AiHistoryState>()(persist((set) => ({
  threads: [createInitialThread(), ...seedThreads],
  activeThreadId: initialThreadId,
  popupHistoryOpen: false,
  createThread: (input) => {
    const id = createThreadId();
    const timestamp = Date.now();
    const messages = cloneMessages(input.messages ?? []);
    const thread: AiThreadRecord = {
      id,
      title: input.title?.trim() || deriveTitle(messages),
      titleLocked: Boolean(input.title?.trim()),
      kind: input.kind ?? 'conversation',
      status: input.status ?? 'active',
      scopeLabel: input.scopeLabel,
      route: input.route,
      summary: deriveSummary(messages, '从当前页面开始新的运维分析。'),
      createdAt: timestamp,
      updatedAt: timestamp,
      messageCount: messages.length,
      pinned: false,
      archived: false,
      messages,
    };
    set((state) => ({
      threads: sortThreads([thread, ...state.threads]),
      activeThreadId: id,
      popupHistoryOpen: false,
    }));
    return id;
  },
  selectThread: (threadId) => set((state) => ({
    activeThreadId: state.threads.some((thread) => thread.id === threadId) ? threadId : state.activeThreadId,
    popupHistoryOpen: false,
  })),
  syncThreadMessages: (threadId, rawMessages, input) => {
    const incomingMessages = cloneMessages(rawMessages);
    const timestamp = Date.now();
    set((state) => ({
      threads: sortThreads(state.threads.map((thread) => {
        if (thread.id !== threadId) return thread;
        const preserveStoredMessages = !input.running
          && incomingMessages.length === 0
          && thread.messages.length > 0;
        const messages = preserveStoredMessages ? thread.messages : incomingMessages;
        const title = thread.titleLocked || !messages.length ? thread.title : deriveTitle(messages);
        const messagesChanged = JSON.stringify(thread.messages) !== JSON.stringify(messages);
        const nextStatus = input.running
          ? 'active'
          : thread.status === 'active' && messages.length
            ? 'completed'
            : thread.status;
        return {
          ...thread,
          title,
          scopeLabel: input.scopeLabel || thread.scopeLabel,
          route: input.route || thread.route,
          summary: deriveSummary(messages, thread.summary),
          updatedAt: messagesChanged || input.running ? timestamp : thread.updatedAt,
          messageCount: messages.length,
          status: nextStatus,
          messages,
        };
      })),
    }));
  },
  renameThread: (threadId, title) => {
    const normalized = title.trim();
    if (!normalized) return;
    set((state) => ({
      threads: state.threads.map((thread) => thread.id === threadId
        ? { ...thread, title: truncate(normalized, 40), titleLocked: true, updatedAt: Date.now() }
        : thread),
    }));
  },
  togglePinned: (threadId) => set((state) => ({
    threads: sortThreads(state.threads.map((thread) => thread.id === threadId
      ? { ...thread, pinned: !thread.pinned, updatedAt: Date.now() }
      : thread)),
  })),
  archiveThread: (threadId, archived) => set((state) => ({
    threads: state.threads.map((thread) => thread.id === threadId
      ? { ...thread, archived: archived ?? !thread.archived, updatedAt: Date.now() }
      : thread),
  })),
  deleteThread: (threadId) => set((state) => {
    const remaining = state.threads.filter((thread) => thread.id !== threadId);
    const nextActive = state.activeThreadId === threadId
      ? remaining.find((thread) => !thread.archived)?.id ?? initialThreadId
      : state.activeThreadId;
    return {
      threads: remaining.length ? remaining : [createInitialThread()],
      activeThreadId: nextActive,
    };
  }),
  setPopupHistoryOpen: (popupHistoryOpen) => set({ popupHistoryOpen }),
}), {
  name: HISTORY_STORAGE_KEY,
  storage: createJSONStorage(() => localStorage),
  partialize: (state) => ({
    threads: state.threads,
    activeThreadId: state.activeThreadId,
  }),
  merge: (persisted, current) => {
    const stored = persisted as Partial<AiHistoryState> | undefined;
    const threads = Array.isArray(stored?.threads) && stored.threads.length
      ? stored.threads
      : current.threads;
    const activeThreadId = threads.some((thread) => thread.id === stored?.activeThreadId)
      ? String(stored?.activeThreadId)
      : threads.find((thread) => !thread.archived)?.id ?? initialThreadId;
    return {
      ...current,
      ...stored,
      threads: sortThreads(threads),
      activeThreadId,
      popupHistoryOpen: false,
    };
  },
}));

export const formatThreadTime = (timestamp: number) => {
  const date = new Date(timestamp);
  const current = new Date();
  const sameDay = date.toDateString() === current.toDateString();
  if (sameDay) return date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false });
  const yesterday = new Date(current);
  yesterday.setDate(current.getDate() - 1);
  if (date.toDateString() === yesterday.toDateString()) return '昨天';
  return date.toLocaleDateString('zh-CN', { month: 'numeric', day: 'numeric' });
};

export const threadKindLabel: Record<AiThreadKind, string> = {
  conversation: '会话',
  investigation: '调查',
  report: '报告',
};

export const threadStatusLabel: Record<AiThreadStatus, string> = {
  active: '进行中',
  waiting: '待确认',
  completed: '已完成',
  failed: '失败',
};

export const useActiveAiThread = () => {
  const threads = useAiHistory((state) => state.threads);
  const activeThreadId = useAiHistory((state) => state.activeThreadId);
  return useMemo(
    () => threads.find((thread) => thread.id === activeThreadId) ?? threads[0],
    [activeThreadId, threads],
  );
};

export function AiThreadHistoryBridge() {
  const { agent } = useAgent({ throttleMs: 40 });
  const context = useAiApplicationContext();
  const location = useLocation();
  const activeThreadId = useAiHistory((state) => state.activeThreadId);
  const threads = useAiHistory((state) => state.threads);
  const syncThreadMessages = useAiHistory((state) => state.syncThreadMessages);
  const activeThread = threads.find((thread) => thread.id === activeThreadId);
  const workspaceContext = activeThread?.messageCount
    ? { scopeLabel: activeThread.scopeLabel, route: activeThread.route }
    : { scopeLabel: context.scopeLabel, route: `${location.pathname}${location.search}` };

  useEffect(() => {
    const thread = threads.find((item) => item.id === activeThreadId);
    if (!thread?.messages.length || agent.messages.length > 0) return;
    const timer = window.setTimeout(() => {
      if (agent.messages.length === 0) {
        agent.setMessages(thread.messages as Parameters<typeof agent.setMessages>[0]);
      }
    }, 220);
    return () => window.clearTimeout(timer);
  }, [activeThreadId, agent, agent.messages.length, threads]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      syncThreadMessages(activeThreadId, agent.messages, {
        scopeLabel: workspaceContext.scopeLabel,
        route: workspaceContext.route,
        running: agent.isRunning,
      });
    }, agent.isRunning ? 80 : 180);
    return () => window.clearTimeout(timer);
  }, [activeThreadId, agent.isRunning, agent.messages, syncThreadMessages, workspaceContext.route, workspaceContext.scopeLabel]);

  return null;
}

export function useAiThreadController() {
  const { agent } = useAgent({ throttleMs: 40 });
  const configuration = useCopilotChatConfiguration();
  const context = useAiApplicationContext();
  const location = useLocation();
  const threads = useAiHistory((state) => state.threads);
  const activeThreadId = useAiHistory((state) => state.activeThreadId);
  const createThread = useAiHistory((state) => state.createThread);
  const selectThread = useAiHistory((state) => state.selectThread);
  const syncThreadMessages = useAiHistory((state) => state.syncThreadMessages);
  const setPopupHistoryOpen = useAiHistory((state) => state.setPopupHistoryOpen);
  const activeThread = threads.find((thread) => thread.id === activeThreadId);
  const workspaceContext = activeThread?.messageCount
    ? { scopeLabel: activeThread.scopeLabel, route: activeThread.route }
    : { scopeLabel: context.scopeLabel, route: `${location.pathname}${location.search}` };
  const newThreadContext = location.pathname === '/ai'
    ? { scopeLabel: `${context.buildingLabel} · 全系统`, route: '/ai' }
    : { scopeLabel: context.scopeLabel, route: `${location.pathname}${location.search}` };

  const saveCurrent = useCallback(() => {
    syncThreadMessages(activeThreadId, agent.messages, {
      scopeLabel: workspaceContext.scopeLabel,
      route: workspaceContext.route,
      running: agent.isRunning,
    });
  }, [activeThreadId, agent.isRunning, agent.messages, syncThreadMessages, workspaceContext.route, workspaceContext.scopeLabel]);

  const startNewThread = useCallback(() => {
    saveCurrent();
    const threadId = createThread({
      scopeLabel: newThreadContext.scopeLabel,
      route: newThreadContext.route,
      kind: 'conversation',
    });
    configuration?.startNewThread();
    agent.setMessages([]);
    setPopupHistoryOpen(false);
    return threadId;
  }, [agent, configuration, createThread, newThreadContext.route, newThreadContext.scopeLabel, saveCurrent, setPopupHistoryOpen]);

  const openThread = useCallback((threadId: string) => {
    const thread = threads.find((item) => item.id === threadId);
    if (!thread) return;
    saveCurrent();
    selectThread(threadId);
    agent.abortRun();
    agent.setMessages(thread.messages as Parameters<typeof agent.setMessages>[0]);
    setPopupHistoryOpen(false);
  }, [agent, saveCurrent, selectThread, setPopupHistoryOpen, threads]);

  return {
    threads,
    activeThreadId,
    activeThread: threads.find((thread) => thread.id === activeThreadId) ?? threads[0],
    startNewThread,
    openThread,
    saveCurrent,
  };
}
