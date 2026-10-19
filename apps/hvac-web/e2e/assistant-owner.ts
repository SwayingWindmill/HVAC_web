// The Operations Agent behind the Gateway, as its Session rules apply: a new Session or a
// question starts a Run; the event stream opens with the Session's snapshot, replays the Run's
// steps, streamed answer and finding, and closes on the snapshot that ends the Run. The answer is
// scripted from the question, so a test reads the same reply every time.

import { readFileSync } from 'node:fs';

type Json = Record<string, any>;

// Fourteen Site-local days of figures, as the Agent's usage read returns them.
const usage: Json = JSON.parse(readFileSync(new URL('./fixtures/assistant-usage.json', import.meta.url), 'utf8'));

export interface AssistantReply {
  readonly tools: readonly string[];
  readonly text: readonly string[];
  readonly finding: Json;
}

const DEFAULT_REPLY: AssistantReply = {
  tools: ['energy_compare_periods', 'energy_by_equipment'],
  text: ['这一小时比前一天同一时段多用电 ', '18 kWh，主要来自 2 号机组。'],
  finding: {
    outcome: 'SUPPORTED_FINDING',
    keyFigures: [{ label: '多用电', value: '+18', unit: 'kWh' }, { label: '综合能效', value: '3.5' }],
    evidenceRefs: [],
    limitations: ['平台没有室外温度历史，无法判断负荷增加是否来自天气。'],
    recommendedNext: ['2 号机组为什么开机？', '明天同一时段要不要少开一台？'],
  },
};

export class AssistantOwner {
  readonly sessions: Json[] = [];
  /** What the next Run answers. */
  reply: AssistantReply = DEFAULT_REPLY;
  /** The Gateway has no Operations Agent configured. */
  unavailable = false;
  private sequence = 0;

  private id(kind: string): string {
    this.sequence += 1;
    return `${kind}-${String(this.sequence).padStart(4, '0')}`;
  }

  /** Routes under /api/v1/sites/{site}/operations/agent-sessions; undefined for any other path. */
  respond(siteId: string, method: string, path: string, body: Json | null): { status?: number; body?: unknown; stream?: string } | undefined {
    // The usage view (#597): the Gateway has already checked audit.read and the Site.
    if (path === `/api/v1/sites/${siteId}/operations/agent-usage` && method === 'GET') return { body: usage };
    const base = `/api/v1/sites/${siteId}/operations/agent-sessions`;
    if (!path.startsWith(base)) return undefined;
    if (this.unavailable) {
      return { status: 503, body: { type: 'about:blank', title: 'Operations Agent unavailable', status: 503, detail: 'The Operations Agent is not configured.', instance: path, code: 'OPERATIONS_AGENT_UNAVAILABLE', traceId: '0'.repeat(32), retryable: true } };
    }
    // The Agent lists the asker's conversations, the newest started first.
    if (path === base && method === 'GET') return { body: this.sessions.map((session) => session.settled) };
    if (path === base && method === 'POST') {
      const now = Date.now();
      const sessionId = this.id('session');
      const session: Json = { siteId, sessionId, createdAt: now, revision: 0, runs: [], messages: [], toolExecutions: [], artifacts: [], settled: null };
      this.sessions.unshift(session);
      return { status: 201, body: this.startRun(session, body!.message, now) };
    }
    const match = path.slice(base.length).match(/^\/([^/:]+)(\/events|:run|:cancel|:submit-input)?$/);
    const session = match ? this.sessions.find((candidate) => candidate.sessionId === match[1]) : undefined;
    if (!match || !session) return undefined;
    if (match[2] === '/events' && method === 'GET') return { stream: this.stream(session) };
    if (match[2] === ':run' && method === 'POST') {
      if (body?.expectedRevision !== session.revision) {
        return { status: 409, body: { type: 'about:blank', title: 'Agent Session conflict', status: 409, detail: 'revision', instance: path, code: 'SESSION_REVISION_CONFLICT', traceId: '0'.repeat(32), retryable: false } };
      }
      return { status: 202, body: this.startRun(session, body!.message, Date.now()) };
    }
    return undefined;
  }

  private snapshot(session: Json, status: string, activeRunId: string | null): Json {
    return {
      session: {
        id: session.sessionId, tenantId: 'tenant-e2e', siteId: session.siteId, agentDefinitionId: 'operations-investigation.v1',
        createdBy: 'principal-e2e', revision: session.revision, createdAt: session.createdAt, updatedAt: Date.now(), status, activeRunId,
      },
      runs: session.runs,
      messages: session.messages,
      toolExecutions: session.toolExecutions,
      artifacts: session.artifacts,
    };
  }

  private startRun(session: Json, message: string, now: number): Json {
    const runId = this.id('run');
    session.revision += 1;
    session.messages.push({ id: this.id('message'), sessionId: session.sessionId, runId: null, role: 'OPERATOR', content: message, createdAt: now });
    session.runs.push(run(session.sessionId, runId, 'RUNNING', now + 1));
    session.pending = { runId, startedAt: now + 1 };
    session.settled = this.snapshot(session, 'ACTIVE', runId);
    return session.settled;
  }

  // Replays the pending Run and settles the Session, as the service's live Run does.
  private stream(session: Json): string {
    const opening = session.settled;
    const pending = session.pending;
    const events: Json[] = [];
    let sequence = 0;
    const emit = (type: string, payload: Json, runId: string | null = pending?.runId ?? null) => {
      events.push({ version: 'hvac.agent.event/v1', type, sessionId: session.sessionId, runId, sequence, at: Date.now(), payload });
      sequence += 1;
    };
    emit('session.snapshot', { snapshot: opening });
    if (pending) {
      const { runId, startedAt } = pending;
      const executions = this.reply.tools.map((toolName, index) => ({
        id: this.id('tool'), sessionId: session.sessionId, runId, toolName, argumentsDigest: 'sha256:e2e', status: 'COMPLETED',
        startedAt: startedAt + 10 + index, finishedAt: startedAt + 20 + index, resultSummary: 'Tool completed.', provenance: [], failureCode: null,
      }));
      for (const execution of executions) {
        emit('tool.started', { toolExecutionId: execution.id, toolName: execution.toolName });
        emit('tool.completed', { toolExecution: execution });
      }
      const messageId = this.id('message');
      for (const delta of this.reply.text) emit('assistant.delta', { messageId, delta });
      const finding = {
        ...this.reply.finding,
        evidenceRefs: executions.slice(0, 1).map((execution) => ({ owner: 'ENERGY', resourceType: 'period-comparison', resourceId: 'compare', toolExecutionId: execution.id })),
      };
      const artifact = { id: this.id('artifact'), sessionId: session.sessionId, runId, createdAt: startedAt + 40, kind: 'FINDING', finding };
      emit('artifact.created', { artifact });
      const finished = run(session.sessionId, runId, 'COMPLETED', startedAt);
      session.runs = session.runs.map((candidate: Json) => (candidate.id === runId ? finished : candidate));
      session.toolExecutions.push(...executions);
      session.messages.push({ id: messageId, sessionId: session.sessionId, runId, role: 'ASSISTANT', content: this.reply.text.join(''), createdAt: startedAt + 30 });
      session.artifacts.push(artifact);
      session.revision += 1;
      session.pending = null;
      emit('run.completed', { run: finished });
    }
    session.settled = this.snapshot(session, 'COMPLETED', null);
    emit('session.snapshot', { snapshot: session.settled }, null);
    return events.map((event) => `event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`).join('');
  }
}

const run = (sessionId: string, id: string, status: string, startedAt: number): Json => ({
  id, sessionId, modelRef: { provider: 'deepseek', model: 'deepseek-flash' }, status, startedAt,
  finishedAt: status === 'RUNNING' ? null : startedAt + 50,
  usage: { inputTokens: 0, outputTokens: 0, modelCalls: 1, toolCalls: 2 }, failureCode: null,
});
