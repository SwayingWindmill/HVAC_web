import {
  type Pool,
  type PoolClient,
  type QueryResult,
  type QueryResultRow,
} from 'pg';

import type {
  AgentSessionState,
  AgentSessionStateStore,
} from '../../application/index.js';

interface Queryable {
  query<T extends QueryResultRow>(text: string, values?: readonly unknown[]): Promise<QueryResult<T>>;
}

interface SessionRow extends QueryResultRow {
  readonly session_id: string;
  readonly tenant_id: string;
  readonly site_id: string;
  readonly agent_definition_id: string;
  readonly created_by: string;
  readonly revision: string;
  readonly status: AgentSessionState['session']['status'];
  readonly active_run_id: string | null;
  readonly created_at_ms: string;
  readonly updated_at_ms: string;
}

interface RunRow extends QueryResultRow {
  readonly session_id: string;
  readonly run_id: string;
  readonly model_provider: string;
  readonly model_id: string;
  readonly status: AgentSessionState['runs'][number]['status'];
  readonly started_at_ms: string;
  readonly finished_at_ms: string | null;
  readonly input_tokens: string;
  readonly output_tokens: string;
  readonly model_calls: string;
  readonly tool_calls: string;
  readonly failure_code: string | null;
}

interface MessageRow extends QueryResultRow {
  readonly message_id: string;
  readonly session_id: string;
  readonly run_id: string | null;
  readonly role: AgentSessionState['messages'][number]['role'];
  readonly content: string;
  readonly created_at_ms: string;
}

interface ToolExecutionRow extends QueryResultRow {
  readonly tool_execution_id: string;
  readonly session_id: string;
  readonly run_id: string;
  readonly tool_name: string;
  readonly arguments_digest: string;
  readonly status: AgentSessionState['toolExecutions'][number]['status'];
  readonly started_at_ms: string;
  readonly finished_at_ms: string;
  readonly result_summary: string | null;
  readonly provenance: AgentSessionState['toolExecutions'][number]['provenance'];
  readonly failure_code: string | null;
}

interface ArtifactRow extends QueryResultRow {
  readonly artifact_payload: AgentSessionState['artifacts'][number];
}

interface SessionIdentityRow extends QueryResultRow {
  readonly session_id: string;
}

const toSafeInteger = (value: string, label: string): number => {
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed)) throw new Error(`${label} is outside the safe integer range.`);
  return parsed;
};

const loadAgentSessionState = async (
  queryable: Queryable,
  sessionId: string,
  lock: boolean,
): Promise<AgentSessionState | null> => {
  const sessionResult = await queryable.query<SessionRow>(
    `SELECT session_id, tenant_id, site_id, agent_definition_id, created_by,
            revision, status, active_run_id, created_at_ms, updated_at_ms
     FROM agent_operations.agent_sessions
     WHERE session_id = $1${lock ? ' FOR UPDATE' : ''}`,
    [sessionId],
  );
  const sessionRow = sessionResult.rows[0];
  if (sessionRow === undefined) return null;

  const runsResult = await queryable.query<RunRow>(
    `SELECT session_id, run_id, model_provider, model_id, status, started_at_ms,
            finished_at_ms, input_tokens, output_tokens, model_calls, tool_calls, failure_code
     FROM agent_operations.agent_runs
     WHERE session_id = $1
     ORDER BY started_at_ms, run_id`,
    [sessionId],
  );
  const messagesResult = await queryable.query<MessageRow>(
    `SELECT message_id, session_id, run_id, role, content, created_at_ms
     FROM agent_operations.agent_messages
     WHERE session_id = $1
     ORDER BY created_at_ms, message_id`,
    [sessionId],
  );
  const executionsResult = await queryable.query<ToolExecutionRow>(
    `SELECT tool_execution_id, session_id, run_id, tool_name, arguments_digest, status,
            started_at_ms, finished_at_ms, result_summary, provenance, failure_code
     FROM agent_operations.agent_tool_executions
     WHERE session_id = $1
     ORDER BY started_at_ms, tool_execution_id`,
    [sessionId],
  );
  const artifactsResult = await queryable.query<ArtifactRow>(
    `SELECT artifact_payload
     FROM agent_operations.agent_artifacts
     WHERE session_id = $1
     ORDER BY created_at_ms, artifact_id`,
    [sessionId],
  );

  const session = Object.freeze({
    id: sessionRow.session_id,
    tenantId: sessionRow.tenant_id,
    siteId: sessionRow.site_id,
    agentDefinitionId: sessionRow.agent_definition_id,
    createdBy: sessionRow.created_by,
    revision: toSafeInteger(sessionRow.revision, 'Session revision'),
    status: sessionRow.status,
    activeRunId: sessionRow.active_run_id,
    createdAt: toSafeInteger(sessionRow.created_at_ms, 'Session createdAt'),
    updatedAt: toSafeInteger(sessionRow.updated_at_ms, 'Session updatedAt'),
  }) as AgentSessionState['session'];

  return Object.freeze({
    session,
    runs: Object.freeze(runsResult.rows.map((row) => Object.freeze({
      id: row.run_id,
      sessionId: row.session_id,
      modelRef: Object.freeze({ provider: row.model_provider, model: row.model_id }),
      status: row.status,
      startedAt: toSafeInteger(row.started_at_ms, 'Run startedAt'),
      finishedAt: row.finished_at_ms === null ? null : toSafeInteger(row.finished_at_ms, 'Run finishedAt'),
      usage: Object.freeze({
        inputTokens: toSafeInteger(row.input_tokens, 'Run inputTokens'),
        outputTokens: toSafeInteger(row.output_tokens, 'Run outputTokens'),
        modelCalls: toSafeInteger(row.model_calls, 'Run modelCalls'),
        toolCalls: toSafeInteger(row.tool_calls, 'Run toolCalls'),
      }),
      failureCode: row.failure_code,
    }))),
    messages: Object.freeze(messagesResult.rows.map((row) => Object.freeze({
      id: row.message_id,
      sessionId: row.session_id,
      runId: row.run_id,
      role: row.role,
      content: row.content,
      createdAt: toSafeInteger(row.created_at_ms, 'Message createdAt'),
    }))),
    toolExecutions: Object.freeze(executionsResult.rows.map((row) => Object.freeze({
      id: row.tool_execution_id,
      sessionId: row.session_id,
      runId: row.run_id,
      toolName: row.tool_name,
      argumentsDigest: row.arguments_digest,
      status: row.status,
      startedAt: toSafeInteger(row.started_at_ms, 'ToolExecution startedAt'),
      finishedAt: toSafeInteger(row.finished_at_ms, 'ToolExecution finishedAt'),
      resultSummary: row.result_summary,
      provenance: Object.freeze([...row.provenance]),
      failureCode: row.failure_code,
    }))),
    artifacts: Object.freeze(artifactsResult.rows.map(({ artifact_payload }) => Object.freeze(artifact_payload))),
  });
};

const insertSession = async (client: PoolClient, state: AgentSessionState): Promise<void> => {
  const { session } = state;
  await client.query(
    `INSERT INTO agent_operations.agent_sessions (
       session_id, tenant_id, site_id, agent_definition_id, created_by,
       revision, status, active_run_id, created_at_ms, updated_at_ms
     ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
    [
      session.id,
      session.tenantId,
      session.siteId,
      session.agentDefinitionId,
      session.createdBy,
      session.revision,
      session.status,
      session.activeRunId,
      session.createdAt,
      session.updatedAt,
    ],
  );
};

const insertRun = async (
  client: PoolClient,
  run: AgentSessionState['runs'][number],
): Promise<void> => {
  await client.query(
    `INSERT INTO agent_operations.agent_runs (
       session_id, run_id, model_provider, model_id, status, started_at_ms, finished_at_ms,
       input_tokens, output_tokens, model_calls, tool_calls, failure_code
     ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
    [
      run.sessionId,
      run.id,
      run.modelRef.provider,
      run.modelRef.model,
      run.status,
      run.startedAt,
      run.finishedAt,
      run.usage.inputTokens,
      run.usage.outputTokens,
      run.usage.modelCalls,
      run.usage.toolCalls,
      run.failureCode,
    ],
  );
};

const updateRun = async (
  client: PoolClient,
  run: AgentSessionState['runs'][number],
): Promise<void> => {
  await client.query(
    `UPDATE agent_operations.agent_runs
     SET status = $3,
         finished_at_ms = $4,
         input_tokens = $5,
         output_tokens = $6,
         model_calls = $7,
         tool_calls = $8,
         failure_code = $9
     WHERE session_id = $1 AND run_id = $2`,
    [
      run.sessionId,
      run.id,
      run.status,
      run.finishedAt,
      run.usage.inputTokens,
      run.usage.outputTokens,
      run.usage.modelCalls,
      run.usage.toolCalls,
      run.failureCode,
    ],
  );
};

const insertMessage = async (
  client: PoolClient,
  message: AgentSessionState['messages'][number],
): Promise<void> => {
  await client.query(
    `INSERT INTO agent_operations.agent_messages (
       message_id, session_id, run_id, role, content, created_at_ms
     ) VALUES ($1,$2,$3,$4,$5,$6)`,
    [message.id, message.sessionId, message.runId, message.role, message.content, message.createdAt],
  );
};

const insertToolExecution = async (
  client: PoolClient,
  execution: AgentSessionState['toolExecutions'][number],
): Promise<void> => {
  await client.query(
    `INSERT INTO agent_operations.agent_tool_executions (
       tool_execution_id, session_id, run_id, tool_name, arguments_digest, status,
       started_at_ms, finished_at_ms, result_summary, provenance, failure_code
     ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11)`,
    [
      execution.id,
      execution.sessionId,
      execution.runId,
      execution.toolName,
      execution.argumentsDigest,
      execution.status,
      execution.startedAt,
      execution.finishedAt,
      execution.resultSummary,
      JSON.stringify(execution.provenance),
      execution.failureCode,
    ],
  );
};

const insertArtifact = async (
  client: PoolClient,
  artifact: AgentSessionState['artifacts'][number],
): Promise<void> => {
  await client.query(
    `INSERT INTO agent_operations.agent_artifacts (
       artifact_id, session_id, run_id, kind, artifact_payload, created_at_ms
     ) VALUES ($1,$2,$3,$4,$5::jsonb,$6)`,
    [artifact.id, artifact.sessionId, artifact.runId, artifact.kind, JSON.stringify(artifact), artifact.createdAt],
  );
};

const persistState = async (
  client: PoolClient,
  current: AgentSessionState | null,
  next: AgentSessionState,
): Promise<void> => {
  if (current === null) {
    await insertSession(client, next);
    for (const run of next.runs) await insertRun(client, run);
    for (const message of next.messages) await insertMessage(client, message);
    for (const execution of next.toolExecutions) await insertToolExecution(client, execution);
    for (const artifact of next.artifacts) await insertArtifact(client, artifact);
    return;
  }

  const currentRuns = new Map(current.runs.map((run) => [run.id, run]));
  for (const run of next.runs) {
    const persisted = currentRuns.get(run.id);
    if (persisted === undefined) await insertRun(client, run);
    else if (JSON.stringify(persisted) !== JSON.stringify(run)) await updateRun(client, run);
  }

  const currentMessageIds = new Set(current.messages.map(({ id }) => id));
  for (const message of next.messages) {
    if (!currentMessageIds.has(message.id)) await insertMessage(client, message);
  }
  const currentExecutionIds = new Set(current.toolExecutions.map(({ id }) => id));
  for (const execution of next.toolExecutions) {
    if (!currentExecutionIds.has(execution.id)) await insertToolExecution(client, execution);
  }
  const currentArtifactIds = new Set(current.artifacts.map(({ id }) => id));
  for (const artifact of next.artifacts) {
    if (!currentArtifactIds.has(artifact.id)) await insertArtifact(client, artifact);
  }

  if (JSON.stringify(current.session) !== JSON.stringify(next.session)) {
    await client.query(
      `UPDATE agent_operations.agent_sessions
       SET revision = $2, status = $3, active_run_id = $4, updated_at_ms = $5
       WHERE session_id = $1`,
      [next.session.id, next.session.revision, next.session.status, next.session.activeRunId, next.session.updatedAt],
    );
  }
};

const withTransaction = async <T>(pool: Pool, work: (client: PoolClient) => Promise<T>): Promise<T> => {
  const client = await pool.connect();
  try {
    await client.query('BEGIN');
    const result = await work(client);
    await client.query('COMMIT');
    return result;
  } catch (error) {
    try { await client.query('ROLLBACK'); } catch {}
    throw error;
  } finally {
    client.release();
  }
};

export const createPostgresAgentSessionStateStore = (
  operationsPool: Pool,
): AgentSessionStateStore => Object.freeze({
  get(sessionId: string) {
    return loadAgentSessionState(operationsPool, sessionId, false);
  },
  async list(tenantId: string, siteId: string) {
    const sessions = await operationsPool.query<SessionIdentityRow>(
      `SELECT session_id
       FROM agent_operations.agent_sessions
       WHERE tenant_id = $1 AND site_id = $2
       ORDER BY updated_at_ms DESC, session_id`,
      [tenantId, siteId],
    );
    const states: AgentSessionState[] = [];
    for (const { session_id: sessionId } of sessions.rows) {
      const state = await loadAgentSessionState(operationsPool, sessionId, false);
      if (state !== null) states.push(state);
    }
    return Object.freeze(states);
  },
  transact(
    sessionId: string,
    update: (current: AgentSessionState | null) => AgentSessionState,
  ) {
    return withTransaction(operationsPool, async (client) => {
      await client.query('SELECT pg_advisory_xact_lock(hashtextextended($1, 0))', [sessionId]);
      const current = await loadAgentSessionState(client, sessionId, true);
      const next = update(current);
      await persistState(client, current, next);
      return next;
    });
  },
});
