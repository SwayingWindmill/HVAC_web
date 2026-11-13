import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import http from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import type { AgentSessionSnapshot } from '../src/agent/index.ts';
import { parseEvalCliArguments, runEvalCli } from '../evals/cli.ts';
import { SONGYAXUAN_SITE_ID } from '../evals/questions.ts';

function readJsonFixture(name: string): unknown {
  return JSON.parse(readFileSync(new URL(`./fixtures/agent-eval/${name}.json`, import.meta.url), 'utf8')) as unknown;
}

function passingDailySnapshot(): AgentSessionSnapshot {
  const base = readJsonFixture('daily-electricity-cooling-cop') as AgentSessionSnapshot;
  const assistantIndex = base.messages.findIndex((message) => message.role === 'ASSISTANT');
  const existing = assistantIndex >= 0 ? base.messages[assistantIndex] : undefined;
  const tableMatch = existing?.content.match(/\| 日期[\s\S]*?\| 10月16日[^\n]*/u);
  const cleanTable = tableMatch?.[0] ?? '';
  const updatedContent = `**松雅轩 10月10日到16日每天用电、供冷和综合能效已查出，其中14日能效最低为 3.45。**\n\n${cleanTable}`;
  return {
    ...base,
    messages: base.messages.map((message, index) => (
      index === assistantIndex ? { ...message, content: updatedContent } : message
    )),
    artifacts: base.artifacts.map((artifact) => (
      artifact.kind === 'FINDING'
        ? {
            ...artifact,
            finding: {
              ...artifact.finding,
              limitations: artifact.finding.limitations.map((line) => line.replaceAll('区间', '时段')),
            },
          }
        : artifact
    )),
  };
}

function startJsonServer(
  handler: (req: http.IncomingMessage, bodyText: string, res: http.ServerResponse) => void,
): Promise<{ server: http.Server; origin: string }> {
  return new Promise((resolvePromise, rejectPromise) => {
    const server = http.createServer((req, res) => {
      const chunks: Buffer[] = [];
      req.on('data', (chunk: Buffer) => chunks.push(chunk));
      req.on('end', () => {
        handler(req, Buffer.concat(chunks).toString('utf8'), res);
      });
    });
    server.once('error', rejectPromise);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (!address || typeof address === 'string') {
        rejectPromise(new Error('Expected TCP address info from http.Server'));
        return;
      }
      resolvePromise({ server, origin: `http://127.0.0.1:${address.port}` });
    });
  });
}

function sendJson(res: http.ServerResponse, status: number, payload: unknown): void {
  res.writeHead(status, { 'Content-Type': 'application/json' });
  res.end(payload === undefined ? undefined : JSON.stringify(payload));
}

test('parseEvalCliArguments validates options and rejects secret-carrying CLI flags', () => {
  const parsed = parseEvalCliArguments(
    ['--only=daily-electricity-cooling-cop', '--trials=1', '--poll-ms=20', '--timeout-ms=5000'],
    { OPERATIONS_AGENT_EVAL_ORIGIN: 'https://127.0.0.1:8443/' },
  );
  assert.equal(parsed.origin, 'https://127.0.0.1:8443');
  assert.equal(parsed.siteId, SONGYAXUAN_SITE_ID);
  assert.equal(parsed.trials, 1);
  assert.deepEqual(parsed.only, ['daily-electricity-cooling-cop']);

  for (const secretFlag of ['--token=abc', '--password=secret', '--username=op', '--totp=123456']) {
    assert.throws(() => parseEvalCliArguments([secretFlag]), /Refusing .* on CLI arguments/);
  }
  assert.throws(() => parseEvalCliArguments(['--only=nonexistent-question']), /Unknown question id/);
});

test('runEvalCli returns exitCode 2 PRECONDITION_UNMET when credentials are missing', async () => {
  const args = parseEvalCliArguments(['--origin=http://127.0.0.1:1', '--only=daily-electricity-cooling-cop', '--trials=1']);
  const result = await runEvalCli(args, { env: {} });
  assert.equal(result.exitCode, 2);
  assert.match(result.reason, /PRECONDITION_UNMET/);
});

test('runEvalCli authenticates with TOTP, retries 429, polls to completion, grades real snapshot, writes report, and logs out', async () => {
  const recordedSnapshot = passingDailySnapshot();
  const recordedSeries = readJsonFixture('energy-series') as Record<string, {
    timezone: string;
    electricity: unknown[];
    cooling: unknown[];
    devices?: unknown[];
  }>;
  const periodSeries = Object.values(recordedSeries)[0];
  assert.ok(periodSeries, 'expected recorded energy-series fixture entry');

  let authLoginAttempts = 0;
  let totpAttempts = 0;
  let pollCount = 0;
  let logoutCalls = 0;
  let rateLimitedOnce = false;
  const authHeadersSeen: string[] = [];

  const { server, origin } = await startJsonServer((req, bodyText, res) => {
    const url = req.url ?? '/';
    const authHeader = typeof req.headers.authorization === 'string' ? req.headers.authorization : '';

    if (req.method === 'POST' && url === '/api/v1/auth/app/sessions') {
      authLoginAttempts += 1;
      const body = JSON.parse(bodyText) as { username?: string; password?: string };
      assert.equal(body.username, 'songyaxuan-eval');
      assert.equal(body.password, 'fixture-pw-unused-outside-test');
      sendJson(res, 202, { step: 'totp', pendingLogin: 'pending-1' });
      return;
    }

    if (req.method === 'POST' && url === '/api/v1/auth/app/sessions/totp') {
      totpAttempts += 1;
      const body = JSON.parse(bodyText) as { pendingLogin?: string; code?: string };
      assert.equal(body.pendingLogin, 'pending-1');
      assert.equal(body.code, '654321');
      sendJson(res, 201, { sessionToken: 'bearer-tok-1', expiresAt: '2026-11-14T00:00:00Z' });
      return;
    }

    if (req.method === 'POST' && url === '/api/v1/auth/logout') {
      assert.equal(authHeader, 'Bearer bearer-tok-1');
      logoutCalls += 1;
      res.writeHead(204);
      res.end();
      return;
    }

    authHeadersSeen.push(authHeader);
    assert.equal(authHeader, 'Bearer bearer-tok-1');

    if (req.method === 'GET' && url === '/api/v1/principal') {
      sendJson(res, 200, { session: {} });
      return;
    }
    if (req.method === 'GET' && url === `/api/v1/sites/${SONGYAXUAN_SITE_ID}`) {
      sendJson(res, 200, { tenantId: 'tenant-1', timezone: periodSeries.timezone });
      return;
    }
    if (req.method === 'POST' && url === '/api/v1/analytics/energy-series') {
      const body = JSON.parse(bodyText) as { energyType?: string; breakdown?: string };
      if (body.energyType === 'electricity') {
        sendJson(res, 200, {
          points: periodSeries.electricity,
          ...(body.breakdown === 'device' && periodSeries.devices ? { devices: periodSeries.devices } : {}),
        });
      } else {
        sendJson(res, 200, { points: periodSeries.cooling });
      }
      return;
    }
    if (req.method === 'POST' && url === `/api/v1/sites/${SONGYAXUAN_SITE_ID}/operations/agent-sessions`) {
      if (!rateLimitedOnce) {
        rateLimitedOnce = true;
        sendJson(res, 429, { error: 'rate limited' });
        return;
      }
      const activeSnapshot: AgentSessionSnapshot = {
        ...recordedSnapshot,
        session: {
          ...recordedSnapshot.session,
          status: 'ACTIVE',
          activeRunId: recordedSnapshot.runs[0]?.id ?? 'run-1',
          revision: 1,
        },
      };
      sendJson(res, 201, activeSnapshot);
      return;
    }
    if (req.method === 'GET' && url === `/api/v1/sites/${SONGYAXUAN_SITE_ID}/operations/agent-sessions/${recordedSnapshot.session.id}`) {
      pollCount += 1;
      sendJson(res, 200, {
        ...recordedSnapshot,
        session: { ...recordedSnapshot.session, status: 'COMPLETED', activeRunId: null, revision: 2 },
      });
      return;
    }

    sendJson(res, 404, { error: `Unhandled route ${req.method} ${url}` });
  });

  const scratch = mkdtempSync(join(tmpdir(), 'hvac-agent-eval-cli-'));
  const outputPath = join(scratch, 'eval-output.json');
  const sleepCalls: number[] = [];

  try {
    const progress: string[] = [];
    const args = parseEvalCliArguments([
      `--origin=${origin}`,
      '--only=daily-electricity-cooling-cop',
      '--trials=1',
      `--output=${outputPath}`,
      '--poll-ms=10',
      '--rate-window-ms=25',
      '--timeout-ms=5000',
    ]);
    const outcome = await runEvalCli(args, {
      env: {
        OPERATIONS_AGENT_EVAL_USERNAME: 'songyaxuan-eval',
        OPERATIONS_AGENT_EVAL_PASSWORD: 'fixture-pw-unused-outside-test',
        OPERATIONS_AGENT_EVAL_TOTP_CODE: '654321',
      },
      sleep: async (ms) => {
        sleepCalls.push(ms);
      },
      onProgress: (line) => {
        progress.push(line);
      },
    });

    assert.equal(authLoginAttempts, 1);
    assert.equal(totpAttempts, 1);
    assert.equal(pollCount, 1);
    assert.equal(logoutCalls, 1);
    assert.ok(authHeadersSeen.length >= 4);
    assert.deepEqual(sleepCalls, [25, 10]);
    assert.equal(outcome.exitCode, 0, `${outcome.reason} | ${progress.join('; ')}`);
    assert.equal(outcome.summary?.passed, true);
    assert.equal(outcome.summary?.passedAllQuestions, 1);

    const writtenRaw = readFileSync(outputPath, 'utf8');
    assert.equal(writtenRaw.includes('bearer-tok-1'), false);
    assert.equal(writtenRaw.includes('fixture-pw-unused-outside-test'), false);
    const written = JSON.parse(writtenRaw) as { summary: { passed: boolean; totalQuestions: number } };
    assert.equal(written.summary.passed, true);
    assert.equal(written.summary.totalQuestions, 1);
  } finally {
    rmSync(scratch, { recursive: true, force: true });
    await new Promise<void>((resolveClose) => server.close(() => resolveClose()));
  }
});

test('runEvalCli cancels active sessions on timeout when using preset token without logging it out', async () => {
  const recordedSnapshot = readJsonFixture('daily-electricity-cooling-cop') as AgentSessionSnapshot;
  const recordedSeries = readJsonFixture('energy-series') as Record<string, {
    timezone: string;
    electricity: unknown[];
    cooling: unknown[];
  }>;
  const periodSeries = Object.values(recordedSeries)[0];
  assert.ok(periodSeries);

  let cancelCalls = 0;
  let logoutCalls = 0;

  const { server, origin } = await startJsonServer((req, bodyText, res) => {
    const url = req.url ?? '/';
    if (req.method === 'POST' && url === '/api/v1/auth/logout') {
      logoutCalls += 1;
      res.writeHead(204);
      res.end();
      return;
    }
    if (req.method === 'GET' && url === '/api/v1/principal') {
      sendJson(res, 200, { session: {} });
      return;
    }
    if (req.method === 'GET' && url === `/api/v1/sites/${SONGYAXUAN_SITE_ID}`) {
      sendJson(res, 200, { tenantId: 'tenant-1', timezone: periodSeries.timezone });
      return;
    }
    if (req.method === 'POST' && url === '/api/v1/analytics/energy-series') {
      sendJson(res, 200, { points: req.url ? (JSON.parse(bodyText) as { energyType?: string }).energyType === 'electricity' ? periodSeries.electricity : periodSeries.cooling : [] });
      return;
    }
    if (req.method === 'POST' && url === `/api/v1/sites/${SONGYAXUAN_SITE_ID}/operations/agent-sessions`) {
      sendJson(res, 201, {
        ...recordedSnapshot,
        session: {
          ...recordedSnapshot.session,
          status: 'ACTIVE',
          activeRunId: recordedSnapshot.runs[0]?.id ?? 'run-7',
          revision: 7,
        },
      });
      return;
    }
    if (req.method === 'GET' && url.endsWith(`/${recordedSnapshot.session.id}`)) {
      sendJson(res, 200, {
        ...recordedSnapshot,
        session: {
          ...recordedSnapshot.session,
          status: 'ACTIVE',
          activeRunId: recordedSnapshot.runs[0]?.id ?? 'run-7',
          revision: 7,
        },
      });
      return;
    }
    if (req.method === 'POST' && url.endsWith(':cancel')) {
      cancelCalls += 1;
      const body = JSON.parse(bodyText) as { expectedRevision?: number };
      assert.equal(body.expectedRevision, 7);
      sendJson(res, 200, {
        ...recordedSnapshot,
        session: { ...recordedSnapshot.session, status: 'CANCELLED', activeRunId: null, revision: 8 },
      });
      return;
    }
    sendJson(res, 404, { error: 'not found' });
  });

  try {
    const args = parseEvalCliArguments([
      `--origin=${origin}`,
      '--only=daily-electricity-cooling-cop',
      '--trials=1',
      '--poll-ms=10',
      '--timeout-ms=50',
    ]);
    const outcome = await runEvalCli(args, {
      env: { OPERATIONS_AGENT_EVAL_SESSION_TOKEN: 'external-session-tok' },
      sleep: (ms) => new Promise((resolvePromise) => setTimeout(resolvePromise, ms)),
      onProgress: () => undefined,
    });

    assert.ok(cancelCalls >= 1);
    assert.equal(logoutCalls, 0);
    assert.equal(outcome.exitCode, 1);
  } finally {
    await new Promise<void>((resolveClose) => server.close(() => resolveClose()));
  }
});
