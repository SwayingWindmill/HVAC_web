import type { Pool, QueryResultRow } from 'pg';

import type { AgentToolUsage, AgentUsage, AgentUsageReader } from '../../application/index.js';

interface RunDayRow extends QueryResultRow {
  readonly day: string;
  readonly completed: string;
  readonly failed: string;
  readonly cancelled: string;
  readonly average_duration_ms: string | null;
  readonly input_tokens: string;
  readonly output_tokens: string;
}

interface ToolDayRow extends QueryResultRow {
  readonly day: string;
  readonly tool_name: string;
  readonly calls: string;
  readonly failed: string;
}

// A Run, and the Tools it called, count on the Site-local day the Run started.
const runDay = `(to_timestamp(r.started_at_ms / 1000.0) AT TIME ZONE $3)::date`;
const siteRunsInRange = `
  JOIN agent_operations.agent_sessions s ON s.session_id = r.session_id
  WHERE s.tenant_id = $1 AND s.site_id = $2 AND ${runDay} BETWEEN $4::date AND $5::date`;

/** Daily figures straight from the Session records, aggregated in the database (#597). */
export const createPostgresAgentUsageReader = (pool: Pool): AgentUsageReader => Object.freeze({
  async daily(tenantId: string, siteId: string, timezone: string, fromDate: string, toDate: string): Promise<AgentUsage> {
    const values = [tenantId, siteId, timezone, fromDate, toDate];
    const [days, runs, tools] = await Promise.all([
      pool.query<{ readonly day: string }>(
        `SELECT d::date::text AS day FROM generate_series($1::date, $2::date, interval '1 day') AS d ORDER BY 1`,
        [fromDate, toDate],
      ),
      pool.query<RunDayRow>(
        `SELECT ${runDay}::text AS day,
           count(*) FILTER (WHERE r.status = 'COMPLETED') AS completed,
           count(*) FILTER (WHERE r.status = 'FAILED') AS failed,
           count(*) FILTER (WHERE r.status = 'CANCELLED') AS cancelled,
           round(avg(r.finished_at_ms - r.started_at_ms) FILTER (WHERE r.finished_at_ms IS NOT NULL)) AS average_duration_ms,
           coalesce(sum(r.input_tokens), 0) AS input_tokens,
           coalesce(sum(r.output_tokens), 0) AS output_tokens
         FROM agent_operations.agent_runs r
         ${siteRunsInRange}
         GROUP BY 1`,
        values,
      ),
      pool.query<ToolDayRow>(
        `SELECT ${runDay}::text AS day, t.tool_name,
           count(*) AS calls,
           count(*) FILTER (WHERE t.status = 'FAILED') AS failed
         FROM agent_operations.agent_tool_executions t
         JOIN agent_operations.agent_runs r ON r.session_id = t.session_id AND r.run_id = t.run_id
         ${siteRunsInRange}
         GROUP BY 1, 2
         ORDER BY 1, 2`,
        values,
      ),
    ]);
    const runsByDay = new Map(runs.rows.map((row) => [row.day, row]));
    const toolsByDay = new Map<string, AgentToolUsage[]>();
    for (const row of tools.rows) {
      const list = toolsByDay.get(row.day) ?? [];
      list.push(Object.freeze({ toolName: row.tool_name, calls: Number(row.calls), failed: Number(row.failed) }));
      toolsByDay.set(row.day, list);
    }
    return Object.freeze({
      schemaVersion: 1,
      timezone,
      days: Object.freeze(days.rows.map(({ day }) => {
        const row = runsByDay.get(day);
        return Object.freeze({
          date: day,
          completed: Number(row?.completed ?? 0),
          failed: Number(row?.failed ?? 0),
          cancelled: Number(row?.cancelled ?? 0),
          averageDurationMs: row?.average_duration_ms == null ? null : Number(row.average_duration_ms),
          inputTokens: Number(row?.input_tokens ?? 0),
          outputTokens: Number(row?.output_tokens ?? 0),
          tools: Object.freeze(toolsByDay.get(day) ?? []),
        });
      })),
    });
  },
});
