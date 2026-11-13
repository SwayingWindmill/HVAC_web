import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { Agent, fetch as undiciFetch, type RequestInit as UndiciRequestInit } from 'undici';

import { questions as knownQuestions, SONGYAXUAN_SITE_ID } from './questions.ts';
import { runSuite, summarizeSuiteReport, type EvalTransport, type SuiteReport, type SuiteSummary } from './run.ts';

export const DEFAULT_EVAL_ORIGIN = 'https://localhost:8443';
export const DEFAULT_EVAL_TRIALS = 3;
export const DEFAULT_EVAL_POLL_MS = 5_000;
export const DEFAULT_EVAL_RATE_WINDOW_MS = 60_000;
export const DEFAULT_EVAL_TIMEOUT_MS = 180_000;
const RATE_LIMITED_RETRIES = 3;
const LOOPBACK_HOSTS = new Set(['localhost', '127.0.0.1', '::1', '[::1]']);

const FORBIDDEN_SECRET_FLAGS = new Set([
  '--token',
  '--session-token',
  '--password',
  '--username',
  '--totp',
  '--totp-code',
  '--secret',
  '--api-key',
]);

export interface ParsedEvalCliArgs {
  readonly help: boolean;
  readonly origin: string;
  readonly siteId: string;
  readonly trials: number;
  readonly only?: readonly string[] | undefined;
  readonly output?: string | undefined;
  readonly pollMs: number;
  readonly rateWindowMs: number;
  readonly timeoutMs: number;
}

export interface EvalCliRunResult {
  readonly exitCode: number;
  readonly reason: string;
  readonly summary?: SuiteSummary | undefined;
  readonly report?: SuiteReport | undefined;
}

export interface RunEvalCliOptions {
  readonly env?: Readonly<Record<string, string | undefined>> | undefined;
  readonly sleep?: ((ms: number) => Promise<void>) | undefined;
  readonly onProgress?: ((line: string) => void) | undefined;
}

function normalizeOrigin(raw: string): string {
  let parsed: URL;
  try {
    parsed = new URL(raw.trim());
  } catch {
    throw new Error(`Invalid --origin "${raw}": expected an http:// or https:// URL`);
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new Error(`Invalid --origin "${raw}": only http:// and https:// origins are supported`);
  }
  return parsed.origin;
}

function parsePositiveInt(raw: string, flagName: string): number {
  const trimmed = raw.trim();
  if (!/^\d+$/.test(trimmed)) {
    throw new Error(`Invalid ${flagName} "${raw}": expected a positive integer`);
  }
  const value = Number.parseInt(trimmed, 10);
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new Error(`Invalid ${flagName} "${raw}": expected a positive integer`);
  }
  return value;
}

function readNextValue(argv: readonly string[], index: number, flag: string): { value: string; nextIndex: number } {
  const next = argv[index + 1];
  if (next === undefined || next.startsWith('--')) {
    throw new Error(`Missing value for ${flag}`);
  }
  return { value: next, nextIndex: index + 1 };
}

/** Parse CLI arguments for the authenticated operations-agent evaluation runner. */
export function parseEvalCliArguments(
  argv: readonly string[],
  env: Readonly<Record<string, string | undefined>> = process.env,
): ParsedEvalCliArgs {
  let help = false;
  let origin = normalizeOrigin(env.OPERATIONS_AGENT_EVAL_ORIGIN?.trim() || DEFAULT_EVAL_ORIGIN);
  let siteId = SONGYAXUAN_SITE_ID;
  let trials = DEFAULT_EVAL_TRIALS;
  const only: string[] = [];
  let output: string | undefined;
  let pollMs = DEFAULT_EVAL_POLL_MS;
  let rateWindowMs = DEFAULT_EVAL_RATE_WINDOW_MS;
  let timeoutMs = DEFAULT_EVAL_TIMEOUT_MS;

  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i] ?? '';
    if (arg === '--') continue;
    const eqIndex = arg.indexOf('=');
    const flagName = eqIndex >= 0 ? arg.slice(0, eqIndex) : arg;

    if (FORBIDDEN_SECRET_FLAGS.has(flagName)) {
      throw new Error(
        `Refusing ${flagName} on CLI arguments to avoid leaking credentials in process listings; pass credentials via OPERATIONS_AGENT_EVAL_* environment variables instead.`,
      );
    }

    if (arg === '--help' || arg === '-h') {
      help = true;
      continue;
    }
    if (flagName === '--origin') {
      const { value, nextIndex } = eqIndex >= 0
        ? { value: arg.slice(eqIndex + 1), nextIndex: i }
        : readNextValue(argv, i, '--origin');
      i = nextIndex;
      origin = normalizeOrigin(value);
      continue;
    }
    if (flagName === '--site') {
      const { value, nextIndex } = eqIndex >= 0
        ? { value: arg.slice(eqIndex + 1), nextIndex: i }
        : readNextValue(argv, i, '--site');
      i = nextIndex;
      const trimmed = value.trim();
      if (!trimmed) throw new Error('Invalid --site: value must not be empty');
      siteId = trimmed;
      continue;
    }
    if (flagName === '--trials') {
      const { value, nextIndex } = eqIndex >= 0
        ? { value: arg.slice(eqIndex + 1), nextIndex: i }
        : readNextValue(argv, i, '--trials');
      i = nextIndex;
      trials = parsePositiveInt(value, '--trials');
      continue;
    }
    if (flagName === '--only') {
      const { value, nextIndex } = eqIndex >= 0
        ? { value: arg.slice(eqIndex + 1), nextIndex: i }
        : readNextValue(argv, i, '--only');
      i = nextIndex;
      for (const token of value.split(',')) {
        const trimmed = token.trim();
        if (trimmed) only.push(trimmed);
      }
      continue;
    }
    if (flagName === '--output') {
      const { value, nextIndex } = eqIndex >= 0
        ? { value: arg.slice(eqIndex + 1), nextIndex: i }
        : readNextValue(argv, i, '--output');
      i = nextIndex;
      const trimmed = value.trim();
      if (!trimmed) throw new Error('Invalid --output: path must not be empty');
      output = trimmed;
      continue;
    }
    if (flagName === '--poll-ms') {
      const { value, nextIndex } = eqIndex >= 0
        ? { value: arg.slice(eqIndex + 1), nextIndex: i }
        : readNextValue(argv, i, '--poll-ms');
      i = nextIndex;
      pollMs = parsePositiveInt(value, '--poll-ms');
      continue;
    }
    if (flagName === '--rate-window-ms') {
      const { value, nextIndex } = eqIndex >= 0
        ? { value: arg.slice(eqIndex + 1), nextIndex: i }
        : readNextValue(argv, i, '--rate-window-ms');
      i = nextIndex;
      rateWindowMs = parsePositiveInt(value, '--rate-window-ms');
      continue;
    }
    if (flagName === '--timeout-ms') {
      const { value, nextIndex } = eqIndex >= 0
        ? { value: arg.slice(eqIndex + 1), nextIndex: i }
        : readNextValue(argv, i, '--timeout-ms');
      i = nextIndex;
      timeoutMs = parsePositiveInt(value, '--timeout-ms');
      continue;
    }
    throw new Error(`Unknown argument: ${arg}`);
  }

  if (only.length > 0) {
    const validIds = new Set(knownQuestions.map((question) => question.id));
    const unknownIds = only.filter((id) => !validIds.has(id));
    if (unknownIds.length > 0) {
      throw new Error(`Unknown question id(s) in --only: ${unknownIds.join(', ')}`);
    }
  }

  return {
    help,
    origin,
    siteId,
    trials,
    only: only.length > 0 ? [...new Set(only)] : undefined,
    output,
    pollMs,
    rateWindowMs,
    timeoutMs,
  };
}

class PreconditionUnmetError extends Error {
  constructor(message: string) {
    super(`PRECONDITION_UNMET: ${message}`);
  }
}

function createDispatcher(origin: string): Agent | undefined {
  const parsed = new URL(origin);
  if (parsed.protocol === 'https:' && LOOPBACK_HOSTS.has(parsed.hostname)) {
    return new Agent({ connect: { rejectUnauthorized: false } });
  }
  return undefined;
}

function normalizeHeaders(headers: HeadersInit | undefined): Record<string, string> {
  const record: Record<string, string> = {};
  if (!headers) return record;
  if (headers instanceof Headers) {
    headers.forEach((value, key) => {
      record[key] = value;
    });
    return record;
  }
  if (Array.isArray(headers)) {
    for (const [key, value] of headers) {
      if (key !== undefined && value !== undefined) {
        record[key] = value;
      }
    }
    return record;
  }
  for (const [key, value] of Object.entries(headers)) {
    if (value !== undefined) {
      record[key] = value;
    }
  }
  return record;
}

function normalizeBody(body: BodyInit | null | undefined): string | undefined {
  if (body === undefined || body === null) return undefined;
  if (typeof body === 'string') return body;
  throw new Error('Operations agent eval CLI transport only supports string request bodies');
}

function readStringProperty(value: unknown, key: string): string | undefined {
  if (typeof value !== 'object' || value === null || !(key in value)) return undefined;
  const candidate = Reflect.get(value, key);
  return typeof candidate === 'string' && candidate.length > 0 ? candidate : undefined;
}

async function fetchJson(
  url: string,
  init: UndiciRequestInit,
): Promise<{ readonly status: number; readonly body: unknown }> {
  let responseStatus = 0;
  try {
    const response = await undiciFetch(url, init);
    responseStatus = response.status;
    const text = await response.text();
    const parsedBody = text.trim() ? JSON.parse(text) : undefined;
    return { status: response.status, body: parsedBody };
  } catch (error) {
    if (responseStatus === 0) {
      const detail = error instanceof Error ? error.message : String(error);
      throw new PreconditionUnmetError(`failed to reach ${url} (${detail})`);
    }
    throw error;
  }
}

async function authenticateSession(
  origin: string,
  dispatcher: Agent | undefined,
  env: Readonly<Record<string, string | undefined>>,
): Promise<{ readonly sessionToken: string; readonly ownsSession: boolean }> {
  const presetToken = env.OPERATIONS_AGENT_EVAL_SESSION_TOKEN?.trim();
  if (presetToken) {
    return { sessionToken: presetToken, ownsSession: false };
  }

  const username = env.OPERATIONS_AGENT_EVAL_USERNAME?.trim();
  const password = env.OPERATIONS_AGENT_EVAL_PASSWORD;
  if (!username || !password) {
    throw new PreconditionUnmetError(
      'set OPERATIONS_AGENT_EVAL_SESSION_TOKEN or OPERATIONS_AGENT_EVAL_USERNAME + OPERATIONS_AGENT_EVAL_PASSWORD before running operations-agent:eval.',
    );
  }

  const loginUrl = `${origin}/api/v1/auth/app/sessions`;
  const login = await fetchJson(loginUrl, {
    method: 'POST',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
    ...(dispatcher ? { dispatcher } : {}),
  });

  if (login.status === 201) {
    const token = readStringProperty(login.body, 'sessionToken');
    if (!token) {
      throw new Error('POST /api/v1/auth/app/sessions succeeded without a sessionToken');
    }
    return { sessionToken: token, ownsSession: true };
  }

  if (login.status === 202) {
    const step = readStringProperty(login.body, 'step');
    const pendingLogin = readStringProperty(login.body, 'pendingLogin');
    if (step !== 'totp' || !pendingLogin) {
      throw new Error(`POST /api/v1/auth/app/sessions returned unsupported HTTP 202 challenge`);
    }
    const totpCode = env.OPERATIONS_AGENT_EVAL_TOTP_CODE?.trim();
    if (!totpCode) {
      throw new PreconditionUnmetError(
        'account requires TOTP verification; set OPERATIONS_AGENT_EVAL_TOTP_CODE and retry.',
      );
    }
    const totp = await fetchJson(`${origin}/api/v1/auth/app/sessions/totp`, {
      method: 'POST',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
      body: JSON.stringify({ pendingLogin, code: totpCode }),
      ...(dispatcher ? { dispatcher } : {}),
    });
    if (totp.status !== 201) {
      throw new Error(`POST /api/v1/auth/app/sessions/totp answered ${totp.status}`);
    }
    const token = readStringProperty(totp.body, 'sessionToken');
    if (!token) {
      throw new Error('POST /api/v1/auth/app/sessions/totp succeeded without a sessionToken');
    }
    return { sessionToken: token, ownsSession: true };
  }

  throw new Error(`POST /api/v1/auth/app/sessions answered ${login.status}`);
}

const defaultSleep = (ms: number): Promise<void> => new Promise((resolvePromise) => setTimeout(resolvePromise, ms));

function createAuthenticatedTransport(params: {
  readonly origin: string;
  readonly sessionToken: string;
  readonly dispatcher: Agent | undefined;
  readonly rateWindowMs: number;
  readonly sleep: (ms: number) => Promise<void>;
}): EvalTransport {
  return async (path, init = {}) => {
    const url = `${params.origin}${path.startsWith('/') ? path : `/${path}`}`;
    const body = normalizeBody(init.body);
    const headers: Record<string, string> = {
      Accept: 'application/json',
      ...normalizeHeaders(init.headers),
      Authorization: `Bearer ${params.sessionToken}`,
    };
    const method = init.method ?? 'GET';

    for (let attempt = 0; ; attempt += 1) {
      const response = await undiciFetch(url, {
        method,
        headers,
        ...(body !== undefined ? { body } : {}),
        ...(params.dispatcher ? { dispatcher: params.dispatcher } : {}),
      });
      if (response.status === 429 && attempt < RATE_LIMITED_RETRIES) {
        await params.sleep(params.rateWindowMs);
        continue;
      }
      if (!response.ok) {
        throw new Error(`${method} ${path} answered ${response.status}`);
      }
      return response.json();
    }
  };
}

/** Run the operations-agent evaluation suite against the live gateway using Bearer session auth. */
export async function runEvalCli(
  args: ParsedEvalCliArgs,
  options: RunEvalCliOptions = {},
): Promise<EvalCliRunResult> {
  const env = options.env ?? process.env;
  const sleep = options.sleep ?? defaultSleep;
  const onProgress = options.onProgress ?? ((line: string) => process.stdout.write(`${line}\n`));
  const dispatcher = createDispatcher(args.origin);

  let sessionToken: string | undefined;
  let ownsSession = false;

  try {
    try {
      const auth = await authenticateSession(args.origin, dispatcher, env);
      sessionToken = auth.sessionToken;
      ownsSession = auth.ownsSession;
    } catch (error) {
      if (error instanceof PreconditionUnmetError) {
        return { exitCode: 2, reason: error.message };
      }
      throw error;
    }

    const transport = createAuthenticatedTransport({
      origin: args.origin,
      sessionToken,
      dispatcher,
      rateWindowMs: args.rateWindowMs,
      sleep,
    });

    const report = await runSuite({
      siteId: args.siteId,
      trials: args.trials,
      only: args.only,
      pollMs: args.pollMs,
      rateWindowMs: args.rateWindowMs,
      runTimeoutMs: args.timeoutMs,
      sleep,
      transport,
      onProgress,
    });

    const summary = summarizeSuiteReport(report);
    if (args.output) {
      const outputAbs = resolve(args.output);
      mkdirSync(dirname(outputAbs), { recursive: true });
      writeFileSync(outputAbs, `${JSON.stringify({ summary, report }, null, 2)}\n`);
    }

    return {
      exitCode: summary.passed ? 0 : 1,
      reason: summary.passed
        ? `Operations agent eval passed (${summary.passedAllQuestions}/${summary.totalQuestions} questions, ${summary.passedTrials}/${summary.totalTrials} trials).`
        : `Operations agent eval had failing questions (${summary.passedAllQuestions}/${summary.totalQuestions} questions, ${summary.passedTrials}/${summary.totalTrials} trials).`,
      summary,
      report,
    };
  } finally {
    if (ownsSession && sessionToken) {
      try {
        await undiciFetch(`${args.origin}/api/v1/auth/logout`, {
          method: 'POST',
          headers: { Authorization: `Bearer ${sessionToken}` },
          ...(dispatcher ? { dispatcher } : {}),
        });
      } catch {
        // Logout cleanup is best-effort after grading finishes.
      }
    }
    if (dispatcher) {
      await dispatcher.close();
    }
  }
}

const USAGE = [
  'Usage: node --experimental-strip-types services/operations-agent-service/evals/cli.ts [options]',
  '  --origin=<url>          Gateway origin (default: OPERATIONS_AGENT_EVAL_ORIGIN or https://localhost:8443)',
  `  --site=<siteId>         Target site id (default: ${SONGYAXUAN_SITE_ID})`,
  `  --trials=<n>            Trials per question (default: ${DEFAULT_EVAL_TRIALS})`,
  '  --only=<id1,id2>        Subset of question ids (repeatable)',
  '  --output=<path>         Optional JSON summary + report output path',
  `  --poll-ms=<ms>          Poll interval in ms (default: ${DEFAULT_EVAL_POLL_MS})`,
  `  --rate-window-ms=<ms>   429 retry wait in ms (default: ${DEFAULT_EVAL_RATE_WINDOW_MS})`,
  `  --timeout-ms=<ms>       Per-trial timeout in ms (default: ${DEFAULT_EVAL_TIMEOUT_MS})`,
  '  --help                  Show this help text',
  '',
  'Authentication (env vars only; secret flags are rejected):',
  '  OPERATIONS_AGENT_EVAL_SESSION_TOKEN',
  '  or OPERATIONS_AGENT_EVAL_USERNAME + OPERATIONS_AGENT_EVAL_PASSWORD (+ optional OPERATIONS_AGENT_EVAL_TOTP_CODE)',
].join('\n');

async function main(): Promise<void> {
  const args = parseEvalCliArguments(process.argv.slice(2));
  if (args.help) {
    process.stdout.write(`${USAGE}\n`);
    return;
  }

  const outcome = await runEvalCli(args);
  if (outcome.summary) {
    process.stdout.write(`${JSON.stringify(outcome.summary, null, 2)}\n`);
  }
  if (outcome.exitCode !== 0) {
    process.stderr.write(`${outcome.reason}\n`);
  }
  process.exitCode = outcome.exitCode;
}

const currentScriptPath = fileURLToPath(import.meta.url);
const invokedScriptPath = process.argv[1] ? resolve(process.argv[1]) : null;
if (invokedScriptPath === resolve(currentScriptPath)) {
  main().catch((error: unknown) => {
    const message = error instanceof Error ? error.message : String(error);
    process.stderr.write(`${message}\n`);
    process.exitCode = 1;
  });
}
