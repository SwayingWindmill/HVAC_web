import assert from 'node:assert/strict';
import test from 'node:test';
import { AssistantOwner } from '../../hvac-web/e2e/assistant-owner.ts';
import type { AgentSessionEvent, AgentSessionSnapshot } from '../miniprogram/lib/agent-types.ts';
import { agentEventReader } from '../miniprogram/lib/agent-stream.ts';
import { presentChart, presentConversation, presentHistory } from '../miniprogram/lib/assistant.ts';
import { assistantThreadFrom } from '../miniprogram/shared/thread-model.ts';

const SITE = '018f3e00-1000-7000-8000-000000000001';
const BASE = `/api/v1/sites/${SITE}/operations/agent-sessions`;

/**
 * Asks the Agent behind the Gateway and reads its Run's event stream as the phone does. The stream
 * ends with the Session's settled snapshot, so the events alone hold the whole conversation;
 * `opening` is the Session as the question left it (the stub updates its own objects as it streams).
 */
function ask(owner: AssistantOwner, question: string): AgentSessionEvent[] & { opening: AgentSessionSnapshot } {
  const created = owner.respond(SITE, 'POST', BASE, { message: question })!.body as AgentSessionSnapshot;
  const opening = structuredClone(created);
  const events: AgentSessionEvent[] = [];
  const stream = owner.respond(SITE, 'GET', `${BASE}/${created.session.id}/events`, null)!.stream!;
  agentEventReader((event) => events.push(event))(new TextEncoder().encode(stream).buffer as ArrayBuffer);
  return Object.assign(events, { opening });
}

test('an answered question shows the data read, the answer, the numbers it rests on, its chart, and what to ask next', () => {
  const view = presentConversation(assistantThreadFrom(null, ask(new AssistantOwner(), '这一小时为什么用电多？')));
  assert.equal(view.running, false);
  assert.deepEqual(view.messages[0], { id: view.messages[0].id, role: 'user', text: '这一小时为什么用电多？' });
  const answer = view.messages[1];
  assert.equal(answer.role, 'assistant');
  if (answer.role !== 'assistant') return;
  assert.deepEqual(answer.steps, { summary: '查询了 2 项数据', running: false, items: [{ label: '对比两个时段', state: 'done' }, { label: '读取各设备用电', state: 'done' }] });
  assert.deepEqual(answer.answer, [{ name: 'p', attrs: { class: 'answer-p' }, children: [{ type: 'text', text: '这一小时比前一天同一时段多用电 18 kWh，主要来自 2 号机组。' }] }]);
  assert.deepEqual(answer.finding, {
    badge: '有数据依据',
    supported: true,
    keyFigures: [{ label: '多用电', value: '+18', unit: 'kWh' }, { label: '综合能效', value: '3.5', unit: '' }],
    limitations: ['平台没有室外温度历史，无法判断负荷增加是否来自天气。'],
    chart: {
      kind: 'BAR', caption: '10月9日–10月10日 11:00 用电（kWh）', legend: [],
      groups: [
        { label: '10/9 11:00', bars: [{ value: '229', height: 93 }] },
        { label: '10/10 11:00', bars: [{ value: '247', height: 100 }] },
      ],
    },
  });
  assert.equal(answer.note, '');
  assert.deepEqual(view.suggestions, ['2 号机组为什么开机？', '明天同一时段要不要少开一台？']);
});

test('while the assistant works it says which data it is reading; a stopped or failed answer says so', () => {
  const events = ask(new AssistantOwner(), '今天能效怎么样？');
  // Halfway: the first read has started, nothing answered yet.
  const halfway = presentConversation(assistantThreadFrom(events.opening, [events[1]]));
  assert.equal(halfway.running, true);
  const working = halfway.messages[1];
  assert.ok(working.role === 'assistant');
  if (working.role !== 'assistant') return;
  assert.deepEqual(working.steps, { summary: '正在查询数据', running: true, items: [{ label: '对比两个时段', state: 'running' }] });
  assert.equal(working.answer, null);
  assert.deepEqual(halfway.suggestions, []);

  const settled = events.at(-1) as Extract<AgentSessionEvent, { type: 'session.snapshot' }>;
  const noteWhen = (status: 'CANCELLED' | 'FAILED') => {
    const snapshot = settled.payload.snapshot;
    const reply = presentConversation(assistantThreadFrom({ ...snapshot, runs: snapshot.runs.map((run) => ({ ...run, status })) }, [])).messages[1];
    return reply.role === 'assistant' ? reply.note : null;
  };
  assert.equal(noteWhen('CANCELLED'), '已停止。');
  assert.equal(noteWhen('FAILED'), '这次没有答完，可以换个问法再问一次。');
});

test('my conversations on the Site list the first question, when it began at the Site and whether it was answered', () => {
  const owner = new AssistantOwner();
  ask(owner, '昨天哪台机组用电最多？');
  ask(owner, '这一小时为什么用电多？');
  const sessions = owner.respond(SITE, 'GET', BASE, null)!.body as AgentSessionSnapshot[];
  // The Site day began 20 hours 5 minutes before the first question; the second came moments later.
  const start = new Date(sessions[1].session.createdAt - 20 * 3_600_000 - 5 * 60_000).toISOString();
  assert.deepEqual(presentHistory(sessions, start).map((entry) => [entry.question, entry.detail]), [
    ['这一小时为什么用电多？', '今天 20:05 · 答完'],
    ['昨天哪台机组用电最多？', '今天 20:05 · 答完'],
  ]);
  assert.deepEqual(presentHistory(sessions, null).map((entry) => entry.detail), ['答完', '答完']);
});

test('hours draw as a line with a gap where the read had none, machines as bars ranked by length', () => {
  const line = presentChart({ kind: 'LINE', title: '今天每小时用电', unit: 'kWh', categories: ['0:00', '1:00', '2:00'], series: [{ name: '用电', values: [100, null, 150.5] }] });
  assert.deepEqual(line, {
    kind: 'LINE', caption: '今天每小时用电（kWh）', legend: [],
    lines: [[{ x: 0, y: 0 }, null, { x: 1, y: 1 }]],
    scale: ['150.5', '100'], from: '0:00', to: '2:00',
  });
  const machines = presentChart({ kind: 'HORIZONTAL_BAR', title: '各台机组用电', unit: 'kWh', categories: ['C 机组', 'D 机组', 'A 机组'], series: [{ name: '用电', values: [1035, 517.5, null] }] });
  assert.deepEqual(machines, {
    kind: 'HORIZONTAL_BAR', caption: '各台机组用电（kWh）', legend: [],
    rows: [{ label: 'C 机组', value: '1,035', width: 100 }, { label: 'D 机组', value: '517.5', width: 50 }],
  });
});

test('a question from the assistant offers its choices, or asks for the answer in the input box', () => {
  const events = ask(new AssistantOwner(), '2 号机组今天用了多少电？');
  const settled = (events.at(-1) as Extract<AgentSessionEvent, { type: 'session.snapshot' }>).payload.snapshot;
  const asking = (response: Extract<AgentSessionSnapshot['artifacts'][number], { kind: 'INPUT_REQUEST' }>['request']['response']) => {
    const run = settled.runs[0];
    const request = { id: 'artifact-ask', sessionId: settled.session.id, runId: run.id, createdAt: run.startedAt + 45, kind: 'INPUT_REQUEST' as const, request: { prompt: '站点里叫"2 号机组"的有两台，看哪一台？', response } };
    return presentConversation(assistantThreadFrom({ ...settled, artifacts: [...settled.artifacts, request] }, []));
  };
  const choosing = asking({ kind: 'SINGLE_SELECT', choices: [{ value: 'b2', label: 'B 区 2 号机组' }, { value: 'c2', label: 'C 区 2 号机组' }] });
  const reply = choosing.messages[1];
  assert.deepEqual(reply.role === 'assistant' && reply.question, {
    prompt: '站点里叫"2 号机组"的有两台，看哪一台？', choices: [{ value: 'b2', label: 'B 区 2 号机组' }, { value: 'c2', label: 'C 区 2 号机组' }], byText: false,
  });
  assert.equal(choosing.answeringByText, false);
  assert.equal(asking({ kind: 'TEXT', maxLength: 200 }).answeringByText, true);
});
