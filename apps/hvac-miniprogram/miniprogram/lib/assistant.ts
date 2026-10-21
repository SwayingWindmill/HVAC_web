// What the 助手 tab shows of a conversation: the operator's questions, and for each answer the data
// it read, the answer itself with its bold conclusion, and the card under it (whether the data
// supports it, its key figures, its chart, what the data could not tell), then what to ask next.
// The conversation itself is folded by the web's thread model (shared/thread-model.ts), so both
// show the same turns, steps and answers.

import type { AgentSessionSnapshot, FindingChart } from './agent-types';
import { answerNodes, type AnswerNode } from './answer-nodes';
import { grouped } from './format';
import { siteMoment } from './today';
import type { AssistantPart, AssistantThread } from '../shared/thread-model';
import { toolPresentation } from '../shared/tool-labels';

type Finding = Extract<AssistantPart, { type: 'data-finding' }>['data']['finding'];
type InputRequest = Extract<AssistantPart, { type: 'data-input-request' }>['data'];

export type StepState = 'running' | 'done' | 'failed';

export type ConversationMessage =
  | { readonly id: string; readonly role: 'user'; readonly text: string }
  | {
    readonly id: string;
    readonly role: 'assistant';
    readonly steps: { readonly summary: string; readonly running: boolean; readonly items: readonly { readonly label: string; readonly state: StepState }[] } | null;
    readonly answer: readonly AnswerNode[] | null;
    readonly finding: ReturnType<typeof presentFinding> | null;
    readonly question: { readonly prompt: string; readonly choices: readonly { readonly value: string; readonly label: string }[]; readonly byText: boolean } | null;
    /** Why the answer ended early; empty when it did not. */
    readonly note: string;
  };

/** A chart value as the web's finding chart writes it: whole numbers plain, others to one decimal. */
const chartValue = (value: number) => grouped(value, Number.isInteger(value) ? 0 : 1);

const percentOf = (value: number, top: number) => (top > 0 ? Math.round((value / top) * 100) : 0);

/** The finding's chart: hours as a line, days and periods as bars, machines as ranked bars. */
export function presentChart(chart: FindingChart) {
  const caption = chart.unit === '' ? chart.title : `${chart.title}（${chart.unit}）`;
  const legend = chart.series.length > 1 ? chart.series.map((series) => series.name) : [];
  const values = chart.series.flatMap((series) => series.values.filter((value): value is number => value !== null));
  const top = Math.max(0, ...values);
  if (chart.kind === 'HORIZONTAL_BAR') {
    const rows = chart.categories.flatMap((label, index) => {
      const value = chart.series[0].values[index];
      return value === null || value === undefined ? [] : [{ label, value: chartValue(value), width: percentOf(value, top) }];
    });
    return { kind: 'HORIZONTAL_BAR' as const, caption, legend, rows };
  }
  if (chart.kind === 'BAR') {
    const groups = chart.categories.map((label, index) => ({
      label,
      bars: chart.series.map((series) => {
        const value = series.values[index];
        return value === null ? { value: '', height: 0 } : { value: chartValue(value), height: percentOf(value, top) };
      }),
    }));
    return { kind: 'BAR' as const, caption, legend, groups };
  }
  const bottom = Math.min(...values);
  const span = top - bottom;
  return {
    kind: 'LINE' as const,
    caption,
    legend,
    // Each point across (0–1) and up (0–1) the plot; a gap where the read had no value.
    lines: chart.series.map((series) => series.values.map((value, index) => (value === null ? null : {
      x: chart.categories.length === 1 ? 0.5 : index / (chart.categories.length - 1),
      y: span === 0 ? 0.5 : (value - bottom) / span,
    }))),
    scale: [chartValue(top), chartValue(bottom)],
    from: chart.categories[0],
    to: chart.categories[chart.categories.length - 1],
  };
}

function presentFinding(finding: Finding) {
  const supported = finding.outcome === 'SUPPORTED_FINDING';
  return {
    badge: supported ? '有数据依据' : '数据不足以下结论',
    supported,
    keyFigures: (finding.keyFigures ?? []).map((figure) => ({ label: figure.label, value: figure.value, unit: figure.unit ?? '' })),
    limitations: [...finding.limitations],
    chart: finding.chart === undefined ? null : presentChart(finding.chart),
  };
}

function presentQuestion(request: InputRequest, open: boolean) {
  return {
    prompt: request.prompt,
    // Only the latest unanswered question takes answers.
    choices: request.response.kind === 'SINGLE_SELECT' && open ? [...request.response.choices] : [],
    byText: request.response.kind === 'TEXT' && open,
  };
}

const NOTES = { cancelled: '已停止。', error: '这次没有答完，可以换个问法再问一次。' } as const;

export function presentConversation(thread: AssistantThread) {
  const messages = thread.messages.map((message): ConversationMessage => {
    if (message.role === 'user') {
      const first = message.content[0];
      return { id: message.id, role: 'user', text: first?.type === 'text' ? first.text : '' };
    }
    const running = message.status.type === 'running';
    const steps = message.content.flatMap((part) => (part.type === 'tool-call'
      ? [{ label: toolPresentation(part.toolName).label, state: (part.result === undefined ? 'running' : part.isError ? 'failed' : 'done') as StepState }]
      : []));
    const text = message.content.find((part) => part.type === 'text');
    const finding = message.content.find((part) => part.type === 'data-finding');
    const request = message.content.find((part) => part.type === 'data-input-request');
    const stillReading = running && steps.some((step) => step.state === 'running');
    return {
      id: message.id,
      role: 'assistant',
      steps: steps.length === 0 ? null : { summary: stillReading ? '正在查询数据' : `查询了 ${steps.length} 项数据`, running: stillReading, items: steps },
      answer: text?.type === 'text' && text.text.trim() !== '' ? answerNodes(text.text) : null,
      finding: finding?.type === 'data-finding' ? presentFinding(finding.data.finding) : null,
      question: request?.type === 'data-input-request' ? presentQuestion(request.data, thread.pendingInput?.request === request.data) : null,
      note: message.status.type === 'incomplete' ? NOTES[message.status.reason] : '',
    };
  });
  return {
    running: thread.isRunning,
    messages,
    suggestions: thread.suggestions.map((suggestion) => suggestion.prompt),
    /** The assistant asked a question to be answered in the input box. */
    answeringByText: thread.pendingInput?.request.response.kind === 'TEXT',
  };
}

const STATUS_WORDS: Readonly<Record<AgentSessionSnapshot['session']['status'], string>> = {
  ACTIVE: '正在回答',
  WAITING_FOR_INPUT: '等你回答',
  COMPLETED: '答完',
  FAILED: '没答完',
  CANCELLED: '已停止',
};

/** My conversations on the Site, newest first as the Agent lists them; times need the Site's midnight. */
export function presentHistory(sessions: readonly AgentSessionSnapshot[], todayStart: string | null) {
  return sessions.map(({ session, messages }) => ({
    id: session.id,
    // A conversation always starts with a question.
    question: messages.find((message) => message.role === 'OPERATOR')?.content ?? '',
    detail: [todayStart === null ? '' : siteMoment(session.createdAt, todayStart), STATUS_WORDS[session.status]].filter(Boolean).join(' · '),
  }));
}
