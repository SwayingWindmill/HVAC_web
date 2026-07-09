import { useCallback, useRef, useState } from 'react';
import { USE_MOCK } from './config';
import { mockGetLatest, MOCK_DEVICES, MOCK_KEYS } from './mock';

/**
 * AI 中心聊天层（#20 / 依据 #17 调研结论）。
 *
 * v1 采用 mock-first：本文件提供自包含的 `useAiChat` hook，模拟流式输出并注入
 * 实时遥测快照（与 #11 的 MockTransport 同范式）。真实后端 `/api/v1/ai/chat`（SSE，
 * OpenAI 兼容流）落地后，只需把本文件的 `useAiChat` 实现替换为 Vercel AI SDK 的
 * `useChat({ api: '/api/v1/ai/chat' })`（#17 推荐的成熟库），页面消费形状保持不变。
 *
 * 只读红线：hook 只产出文本，绝不调用任何设备下发/写接口。
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
  setInput: (v: string) => void;
  send: (text?: string) => void;
  isStreaming: boolean;
  stop: () => void;
  clear: () => void;
  suggested: string[];
}

// 设备可读名（mock 应答引用，避免 api 层反向依赖页面）
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

interface Snapshot {
  power: number;
  cop: number;
  load: number;
  supply: number;
  ret: number;
  weakest: { name: string; cop: number };
  count: number;
}

async function readSnapshot(): Promise<Snapshot> {
  const rows = await Promise.all(MOCK_DEVICES.map((d) => mockGetLatest(d, MOCK_KEYS)));
  const num = (k: string) => rows.reduce((s, r) => s + (r[k]?.value ?? 0), 0);
  const avg = (k: string) => num(k) / rows.length;
  const power = num('power');
  const cop = avg('cop');
  const load = avg('load');
  const supply = avg('supplyTemp');
  const ret = avg('returnTemp');
  // 找 COP 最低的设备
  let weakId = MOCK_DEVICES[0];
  let weakCop = Infinity;
  rows.forEach((r, i) => {
    const c = r.cop?.value ?? Infinity;
    if (c < weakCop) {
      weakCop = c;
      weakId = MOCK_DEVICES[i];
    }
  });
  return {
    power: Math.round(power),
    cop: Math.round(cop * 100) / 100,
    load: Math.round(load),
    supply: Math.round(supply * 10) / 10,
    ret: Math.round(ret * 10) / 10,
    weakest: { name: DEVICE_LABEL[weakId] ?? weakId, cop: Math.round(weakCop * 100) / 100 },
    count: MOCK_DEVICES.length,
  };
}

function buildAnswer(text: string, s: Snapshot): string {
  const t = text.toLowerCase();
  const asks = (...kw: string[]) => kw.some((k) => t.includes(k));

  if (asks('功率', 'power', '能耗', '用电')) {
    return `当前园区共 ${s.count} 台设备实时运行，总功率约 ${s.power} kW，综合 COP 约 ${s.cop}。其中冷水机组是主要用电负荷。与基线相比，当前负荷率 ${s.load}%，仍有优化空间——可在部分负荷时段减少运行台数或提高冷冻水温度设定来降耗。`;
  }
  if (asks('cop', '能效', '效率')) {
    return `综合 COP 当前约 ${s.cop}。能效最差的是 ${s.weakest.name}，COP 仅约 ${s.weakest.cop}，明显低于健康阈值 4.5，建议优先排查其运行工况（如冷凝器脏堵、冷冻水流量不足或负载不匹配）。`;
  }
  if (asks('节能', '优化', '建议', '省')) {
    return `基于当前数据，可考虑三项节能动作：① 提高冷冻水供水温度设定 0.5~1℃（当前约 ${s.supply}℃），预计降耗 2~4%；② 部分负荷时段将冷冻水泵由 3 台减为 2 台，当前负荷率 ${s.load}% 时通常够用；③ 优化冷却塔启停策略，使逼近度维持在合理区间。这些建议可在「优化」页提交审批，本助手只做查询、不会直接下发设备指令。`;
  }
  if (asks('温度', '供水', '回水', '温差')) {
    const dt = Math.round((s.ret - s.supply) * 10) / 10;
    return `当前冷冻水供水温度约 ${s.supply}℃、回水温度约 ${s.ret}℃，供回水温差 ${dt}℃。健康区间一般为 4~6℃，当前${dt >= 4 && dt <= 6 ? '处于正常区间' : dt < 4 ? '温差偏小，可能存在流量过大或负荷不足' : '温差偏大，请关注末端换热或水泵频率'}。`;
  }
  if (asks('告警', '报警', '故障', '异常')) {
    return `本助手是只读查询，不接触告警/工单系统。设备实时健康请查看「FDD 诊断」与「告警闭环」页面；如需对异常设备生成处置工单，请到对应页面操作。`;
  }
  // 兜底：给一个带实时数据的概览
  return `我已读取当前实时遥测：园区总功率约 ${s.power} kW，综合 COP 约 ${s.cop}，综合负荷率 ${s.load}%，冷冻水供/回水 ${s.supply}/${s.ret}℃。能效最弱的是 ${s.weakest.name}（COP ${s.weakest.cop}）。你可以问我「总功率」「COP」「节能建议」或「供水温度」等。注意：我是只读助手，不能控制任何设备。`;
}

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));
let seq = 0;
const nextId = () => `m${Date.now()}-${seq++}`;

export function useAiChat(): AiChatHelpers {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [input, setInput] = useState('');
  const [isStreaming, setIsStreaming] = useState(false);
  const abortRef = useRef(false);
  const inputRef = useRef('');
  inputRef.current = input;

  const send = useCallback(
    async (raw?: string) => {
      const text = (raw ?? inputRef.current).trim();
      if (!text || isStreaming) return;

      const userMsg: ChatMessage = { id: nextId(), role: 'user', content: text };
      const assistantId = nextId();
      setMessages((m) => [...m, userMsg, { id: assistantId, role: 'assistant', content: '' }]);
      setInput('');
      setIsStreaming(true);
      abortRef.current = false;

      const answer = buildAnswer(text, await readSnapshot());
      // 模拟流式逐字输出
      const tokens = answer.match(/[\s\S]{1,3}/g) ?? [answer];
      for (const tk of tokens) {
        if (abortRef.current) break;
        await sleep(16);
        setMessages((m) =>
          m.map((msg) => (msg.id === assistantId ? { ...msg, content: msg.content + tk } : msg)),
        );
      }
      setIsStreaming(false);
    },
    [isStreaming],
  );

  const stop = useCallback(() => {
    abortRef.current = true;
    setIsStreaming(false);
  }, []);

  const clear = useCallback(() => {
    abortRef.current = true;
    setIsStreaming(false);
    setMessages([]);
  }, []);

  return { messages, input, setInput, send, isStreaming, stop, clear, suggested: SUGGESTED_QUESTIONS };
}

export const AI_MOCK_MODE = USE_MOCK;
