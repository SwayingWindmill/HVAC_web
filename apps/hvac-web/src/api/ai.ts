import { create } from 'zustand';
import { USE_MOCK } from './config';
import { mockGetLatest, MOCK_DEVICES, MOCK_KEYS } from './mock';

/**
 * 泉来禾 AI 运维助手聊天层。
 *
 * Mock 模式使用模块级 Zustand store，使全局抽屉和 /ai 完整工作台共享同一段会话、
 * 输入草稿和流式状态。真实后端接入后，页面层仍消费统一的 AssistantSession 形状。
 *
 * 只读红线：本层只产出文本，绝不调用任何设备下发或写接口。
 */

export type ChatRole = 'user' | 'assistant' | 'system';
export interface ChatMessage {
  id: string;
  role: ChatRole;
  content: string;
}

export interface AiChatHelpers {
  messages: ChatMessage[];
  input: string;
  setInput: (value: string) => void;
  replaceMessages: (messages: ChatMessage[]) => void;
  send: (text?: string) => Promise<void>;
  isStreaming: boolean;
  stop: () => void;
  clear: () => void;
  suggested: string[];
}

const DEVICE_LABEL: Record<string, string> = {
  'b1-z1-u1': '冷水机组 #1',
  'b1-z1-u2': '冷水机组 #2',
  'b1-z1-p3': '冷冻水泵 #3',
  'b1-z2-ahu1': '空调末端 AHU-1',
  'b1-z2-ahu2': '空调末端 AHU-2',
  'b1-z3-ahu7': '空调末端 AHU-7',
};

export const SUGGESTED_QUESTIONS = [
  '当前园区总功率和综合 COP 是多少？',
  '哪台设备 COP 最低、能效最差？',
  '有什么节能优化建议？',
  '供水温度和回水温度正常吗？',
];

export interface AiTelemetrySnapshot {
  power: number;
  cop: number;
  load: number;
  supply: number;
  ret: number;
  weakest: { id: string; name: string; cop: number };
  count: number;
}

export async function readAiSnapshot(): Promise<AiTelemetrySnapshot> {
  const rows = await Promise.all(MOCK_DEVICES.map((deviceId) => mockGetLatest(deviceId, MOCK_KEYS)));
  const sum = (key: string) => rows.reduce((total, row) => total + (row[key]?.value ?? 0), 0);
  const average = (key: string) => sum(key) / rows.length;
  let weakestDeviceId = MOCK_DEVICES[0];
  let weakestCop = Infinity;

  rows.forEach((row, index) => {
    const cop = row.cop?.value ?? Infinity;
    if (cop < weakestCop) {
      weakestCop = cop;
      weakestDeviceId = MOCK_DEVICES[index];
    }
  });

  return {
    power: Math.round(sum('power')),
    cop: Math.round(average('cop') * 100) / 100,
    load: Math.round(average('load')),
    supply: Math.round(average('supplyTemp') * 10) / 10,
    ret: Math.round(average('returnTemp') * 10) / 10,
    weakest: {
      id: weakestDeviceId,
      name: DEVICE_LABEL[weakestDeviceId] ?? weakestDeviceId,
      cop: Math.round(weakestCop * 100) / 100,
    },
    count: MOCK_DEVICES.length,
  };
}

function buildAnswer(text: string, snapshot: AiTelemetrySnapshot): string {
  const normalized = text.toLowerCase();
  const asks = (...keywords: string[]) => keywords.some((keyword) => normalized.includes(keyword));

  if (asks('功率', 'power', '能耗', '用电')) {
    return `当前园区共 ${snapshot.count} 台设备实时运行，总功率约 ${snapshot.power} kW，综合 COP 约 ${snapshot.cop}。其中冷水机组是主要用电负荷。与基线相比，当前负荷率 ${snapshot.load}%，仍有优化空间——可在部分负荷时段减少运行台数或提高冷冻水温度设定来降耗。`;
  }
  if (asks('cop', '能效', '效率')) {
    return `综合 COP 当前约 ${snapshot.cop}。能效最差的是 ${snapshot.weakest.name}，COP 仅约 ${snapshot.weakest.cop}，明显低于健康阈值 4.5，建议优先排查其运行工况（如冷凝器脏堵、冷冻水流量不足或负载不匹配）。`;
  }
  if (asks('节能', '优化', '建议', '省')) {
    return `基于当前数据，可考虑三项节能动作：① 提高冷冻水供水温度设定 0.5~1℃（当前约 ${snapshot.supply}℃），预计降耗 2~4%；② 部分负荷时段将冷冻水泵由 3 台减为 2 台，当前负荷率 ${snapshot.load}% 时通常够用；③ 优化冷却塔启停策略，使逼近度维持在合理区间。这些建议可在「优化」页提交审批，本助手只做查询、不会直接下发设备指令。`;
  }
  if (asks('温度', '供水', '回水', '温差')) {
    const delta = Math.round((snapshot.ret - snapshot.supply) * 10) / 10;
    return `当前冷冻水供水温度约 ${snapshot.supply}℃、回水温度约 ${snapshot.ret}℃，供回水温差 ${delta}℃。健康区间一般为 4~6℃，当前${delta >= 4 && delta <= 6 ? '处于正常区间' : delta < 4 ? '温差偏小，可能存在流量过大或负荷不足' : '温差偏大，请关注末端换热或水泵频率'}。`;
  }
  if (asks('告警', '报警', '故障', '异常')) {
    return '本助手是只读分析入口，不直接改变告警或工单状态。设备健康请结合「FDD 诊断」与「报警工单」页面核查；需要处置时，应由人工进入对应业务闭环。';
  }
  return `我已读取当前实时遥测：园区总功率约 ${snapshot.power} kW，综合 COP 约 ${snapshot.cop}，综合负荷率 ${snapshot.load}%，冷冻水供/回水 ${snapshot.supply}/${snapshot.ret}℃。能效最弱的是 ${snapshot.weakest.name}（COP ${snapshot.weakest.cop}）。你可以继续追问原因、证据、风险或建议动作。注意：我是只读助手，不能控制任何设备。`;
}

export function buildAiMockAnswerFromSnapshot(text: string, snapshot: AiTelemetrySnapshot): string {
  return buildAnswer(text, snapshot);
}

export async function buildAiMockAnswer(text: string): Promise<string> {
  return buildAnswer(text, await readAiSnapshot());
}

const sleep = (milliseconds: number) => new Promise<void>((resolve) => setTimeout(resolve, milliseconds));
let sequence = 0;
let streamToken = 0;
const nextId = () => `m${Date.now()}-${sequence++}`;

type AiChatState = Omit<AiChatHelpers, 'suggested'>;

const useAiChatStore = create<AiChatState>((set, get) => ({
  messages: [],
  input: '',
  isStreaming: false,
  setInput: (input) => set({ input }),
  replaceMessages: (messages) => {
    streamToken += 1;
    set({ messages, input: '', isStreaming: false });
  },
  send: async (raw) => {
    const text = (raw ?? get().input).trim();
    if (!text || get().isStreaming) return;

    const currentToken = ++streamToken;
    const userMessage: ChatMessage = { id: nextId(), role: 'user', content: text };
    const assistantId = nextId();
    set((state) => ({
      messages: [...state.messages, userMessage, { id: assistantId, role: 'assistant', content: '' }],
      input: '',
      isStreaming: true,
    }));

    try {
      const answer = buildAnswer(text, await readAiSnapshot());
      const tokens = answer.match(/[\s\S]{1,3}/g) ?? [answer];
      for (const token of tokens) {
        if (currentToken !== streamToken) break;
        await sleep(16);
        set((state) => ({
          messages: state.messages.map((message) => (
            message.id === assistantId
              ? { ...message, content: message.content + token }
              : message
          )),
        }));
      }
    } finally {
      if (currentToken === streamToken) set({ isStreaming: false });
    }
  },
  stop: () => {
    streamToken += 1;
    set({ isStreaming: false });
  },
  clear: () => {
    streamToken += 1;
    set({ messages: [], input: '', isStreaming: false });
  },
}));

export function useAiChat(): AiChatHelpers {
  const state = useAiChatStore();
  return { ...state, suggested: SUGGESTED_QUESTIONS };
}

export const AI_MOCK_MODE = USE_MOCK;
