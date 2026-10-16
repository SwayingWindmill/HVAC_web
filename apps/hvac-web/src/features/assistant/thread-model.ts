import type {
  AgentArtifact,
  AgentInputRequestArtifact,
  AgentSessionEvent,
  AgentSessionSnapshot,
} from '@/api/generated/operationsAgentSessions.gen';

// Folds a session snapshot and the live events after it into what the assistant panel shows:
// one user turn per operator message or answered question, and one assistant turn per Run
// holding its steps, its streamed answer and its finding or question.

type Snapshot = AgentSessionSnapshot;
type Run = Snapshot['runs'][number];
type ToolExecution = Snapshot['toolExecutions'][number];
type Finding = Extract<AgentArtifact, { kind: 'FINDING' }>['finding'];

export type AssistantPart =
  | { readonly type: 'text'; readonly text: string }
  | {
    readonly type: 'tool-call';
    readonly toolCallId: string;
    readonly toolName: string;
    readonly args: Record<string, never>;
    readonly result?: string;
    readonly isError?: boolean;
  }
  | {
    readonly type: 'data-finding';
    /** evidenceTools: the Tools the evidence came from, once each, so the card can link their pages. */
    readonly data: { readonly finding: Finding; readonly evidenceTools: readonly string[] };
  }
  | { readonly type: 'data-input-request'; readonly data: AgentInputRequestArtifact['request'] };

export type AssistantMessage =
  | { readonly id: string; readonly role: 'user'; readonly content: readonly AssistantPart[] }
  | {
    readonly id: string;
    readonly role: 'assistant';
    readonly status:
      | { readonly type: 'running' }
      | { readonly type: 'complete'; readonly reason: 'stop' }
      | { readonly type: 'incomplete'; readonly reason: 'cancelled' | 'error' };
    readonly metadata: { readonly runId: string };
    readonly content: readonly AssistantPart[];
  };

export interface AssistantThread {
  readonly sessionId: string | null;
  readonly revision: number | null;
  readonly isRunning: boolean;
  readonly activeRunId: string | undefined;
  readonly messages: readonly AssistantMessage[];
  readonly suggestions: readonly { readonly prompt: string }[];
  readonly pendingInput: AgentInputRequestArtifact | null;
}

export const emptyAssistantThread: AssistantThread = {
  sessionId: null,
  revision: null,
  isRunning: false,
  activeRunId: undefined,
  messages: [],
  suggestions: [],
  pendingInput: null,
};

const FAILED_TOOL_RESULT = '没有读到数据';
// The closing Tools show as the finding or the question, not as steps.
const TERMINAL_TOOLS = new Set(['investigation_complete', 'investigation_request_input']);

interface Working {
  snapshot: Snapshot;
  runs: Map<string, Run>;
  tools: Map<string, ToolExecution>;
  artifacts: Map<string, AgentArtifact>;
  /** The latest turn's streamed text per Run, kept until the Run's ASSISTANT messages are committed. */
  streamed: Map<string, { readonly messageId: string; readonly text: string }>;
}

const workingFrom = (snapshot: Snapshot): Working => ({
  snapshot,
  runs: new Map(snapshot.runs.map((run) => [run.id, run])),
  tools: new Map(snapshot.toolExecutions.map((execution) => [execution.id, execution])),
  artifacts: new Map(snapshot.artifacts.map((artifact) => [artifact.id, artifact])),
  streamed: new Map(),
});

const apply = (working: Working, event: AgentSessionEvent): Working => {
  switch (event.type) {
    case 'session.snapshot':
      return workingFrom(event.payload.snapshot);
    case 'run.started':
    case 'run.completed':
    case 'run.failed':
      working.runs.set(event.payload.run.id, event.payload.run);
      return working;
    case 'assistant.delta':
      if (event.runId !== null) {
        // A Run speaks once per model turn; only its latest turn is the answer so far.
        const current = working.streamed.get(event.runId);
        const text = current?.messageId === event.payload.messageId ? current.text + event.payload.delta : event.payload.delta;
        working.streamed.set(event.runId, { messageId: event.payload.messageId, text });
      }
      return working;
    case 'tool.started':
      if (event.runId !== null && !working.tools.has(event.payload.toolExecutionId)) {
        working.tools.set(event.payload.toolExecutionId, {
          id: event.payload.toolExecutionId,
          sessionId: event.sessionId,
          runId: event.runId,
          toolName: event.payload.toolName,
          argumentsDigest: 'pending',
          status: 'RUNNING',
          startedAt: event.at,
          finishedAt: null,
          resultSummary: null,
          provenance: [],
          failureCode: null,
        });
      }
      return working;
    case 'tool.completed':
      working.tools.set(event.payload.toolExecution.id, event.payload.toolExecution);
      return working;
    case 'artifact.created':
    case 'input.required':
      working.artifacts.set(event.payload.artifact.id, event.payload.artifact);
      return working;
  }
};

const toolPart = (execution: ToolExecution): AssistantPart => {
  const base = { type: 'tool-call' as const, toolCallId: execution.id, toolName: execution.toolName, args: {} };
  if (execution.status === 'RUNNING') return base;
  const isError = execution.status !== 'COMPLETED';
  return { ...base, result: isError ? FAILED_TOOL_RESULT : (execution.resultSummary ?? ''), isError };
};

const runStatus = (run: Run): Extract<AssistantMessage, { role: 'assistant' }>['status'] => {
  switch (run.status) {
    case 'RUNNING':
      return { type: 'running' };
    case 'COMPLETED':
      return { type: 'complete', reason: 'stop' };
    case 'CANCELLED':
      return { type: 'incomplete', reason: 'cancelled' };
    case 'FAILED':
      return { type: 'incomplete', reason: 'error' };
  }
};

const inputAnswerText = (working: Working, artifact: Extract<AgentArtifact, { kind: 'INPUT_RESPONSE' }>): string => {
  const request = working.artifacts.get(artifact.requestArtifactId);
  if (request?.kind !== 'INPUT_REQUEST' || request.request.response.kind !== 'SINGLE_SELECT') return artifact.value;
  return request.request.response.choices.find((choice) => choice.value === artifact.value)?.label ?? artifact.value;
};

export const assistantThreadFrom = (
  snapshot: Snapshot | null,
  events: readonly AgentSessionEvent[],
): AssistantThread => {
  if (snapshot === null && events.length === 0) return emptyAssistantThread;
  const initial = snapshot ?? (events[0]?.type === 'session.snapshot' ? events[0].payload.snapshot : null);
  if (initial === null) return emptyAssistantThread;
  const working = events.reduce(apply, workingFrom(initial));

  const runs = [...working.runs.values()].sort((left, right) => left.startedAt - right.startedAt);
  const tools = [...working.tools.values()]
    .filter((execution) => !TERMINAL_TOOLS.has(execution.toolName))
    .sort((left, right) => left.startedAt - right.startedAt);
  const artifacts = [...working.artifacts.values()];
  const answers = new Set(artifacts.flatMap((artifact) => (artifact.kind === 'INPUT_RESPONSE' ? [artifact.requestArtifactId] : [])));

  type Turn = { readonly at: number; readonly message: AssistantMessage };
  const turns: Turn[] = [];
  for (const message of working.snapshot.messages) {
    if (message.role === 'OPERATOR') {
      turns.push({ at: message.createdAt, message: { id: message.id, role: 'user', content: [{ type: 'text', text: message.content }] } });
    }
  }
  for (const artifact of artifacts) {
    if (artifact.kind === 'INPUT_RESPONSE') {
      turns.push({ at: artifact.createdAt, message: { id: artifact.id, role: 'user', content: [{ type: 'text', text: inputAnswerText(working, artifact) }] } });
    }
  }
  for (const run of runs) {
    // Text before a Tool call narrates the search; the Run's last turn is its answer.
    const committed = working.snapshot.messages.filter((message) => message.role === 'ASSISTANT' && message.runId === run.id).at(-1);
    const text = committed?.content ?? working.streamed.get(run.id)?.text;
    const content: AssistantPart[] = [
      ...tools.filter((execution) => execution.runId === run.id).map(toolPart),
      ...(text === undefined ? [] : [{ type: 'text' as const, text }]),
    ];
    for (const artifact of artifacts) {
      if (artifact.runId !== run.id) continue;
      if (artifact.kind === 'FINDING') {
        const evidenceTools = [...new Set(artifact.finding.evidenceRefs.flatMap((ref) => {
          const execution = working.tools.get(ref.toolExecutionId);
          return execution === undefined ? [] : [execution.toolName];
        }))];
        content.push({ type: 'data-finding', data: { finding: artifact.finding, evidenceTools } });
      }
      if (artifact.kind === 'INPUT_REQUEST') content.push({ type: 'data-input-request', data: artifact.request });
    }
    turns.push({ at: run.startedAt, message: { id: run.id, role: 'assistant', status: runStatus(run), metadata: { runId: run.id }, content } });
  }
  turns.sort((left, right) => left.at - right.at);

  const activeRun = runs.find((run) => run.status === 'RUNNING');
  const lastRun = runs.at(-1);
  const lastFinding = lastRun === undefined || activeRun !== undefined
    ? undefined
    : artifacts.find((artifact): artifact is Extract<AgentArtifact, { kind: 'FINDING' }> => artifact.kind === 'FINDING' && artifact.runId === lastRun.id);
  const pendingInput = activeRun === undefined
    ? artifacts.find((artifact): artifact is AgentInputRequestArtifact => artifact.kind === 'INPUT_REQUEST' && !answers.has(artifact.id)) ?? null
    : null;

  return {
    sessionId: working.snapshot.session.id,
    revision: working.snapshot.session.revision,
    isRunning: activeRun !== undefined,
    activeRunId: activeRun?.id,
    messages: turns.map((turn) => turn.message),
    suggestions: lastFinding?.finding.recommendedNext.map((prompt) => ({ prompt })) ?? [],
    pendingInput,
  };
};
