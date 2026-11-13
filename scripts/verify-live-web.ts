import { spawnSync } from 'node:child_process';
import { mkdirSync, statSync, writeFileSync } from 'node:fs';
import http from 'node:http';
import https from 'node:https';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { windowsPathToWsl } from './check-verify-runtime.ts';
import { localAdministratorBrowser } from './lib/local-administrator-browser.mjs';

export interface VerifyBrowserPage {
  setViewportSize(size: { width: number; height: number }): Promise<void>;
  on(event: 'console', listener: (message: { type(): string; text(): string }) => void): void;
  on(event: 'pageerror', listener: (error: Error) => void): void;
  on(event: 'response', listener: (response: { status(): number; url(): string }) => void): void;
  goto(url: string, options: { waitUntil: 'domcontentloaded'; timeout: number }): Promise<unknown>;
  waitForLoadState(state: 'networkidle', options: { timeout: number }): Promise<void>;
  waitForFunction(
    fn: () => boolean,
    arg: undefined,
    options: { timeout: number },
  ): Promise<unknown>;
  evaluate(
    fn: () => { scrollWidth: number; clientWidth: number; hasVisibleText: boolean },
  ): Promise<{ scrollWidth: number; clientWidth: number; hasVisibleText: boolean }>;
  screenshot(options: { path: string; fullPage: boolean }): Promise<unknown>;
  url(): string;
}

export interface VerifyBrowserSession {
  readonly browser: { close(): Promise<void> };
  readonly page: VerifyBrowserPage;
  readonly principal: unknown;
}

export const DEFAULT_LIVE_ORIGIN = 'https://localhost:8443';
export const DEFAULT_LIVE_SLUG = 'live-web';
export const DEFAULT_LIVE_ROUTES: readonly string[] = ['/operations/realtime', '/system?tab=site'];
export const DEFAULT_TIMEOUT_MS = 30000;
export const VIEWPORT_WIDTH = 1440;
export const VIEWPORT_HEIGHT = 900;

export interface ParsedLiveVerifyArgs {
  readonly help: boolean;
  readonly slug: string;
  readonly routes: readonly string[];
  readonly origin: string;
  readonly label?: string;
  readonly timeoutMs: number;
}

export interface OriginHealthCheckResult {
  readonly ok: boolean;
  readonly status: number | null;
  readonly reason: string;
}

export interface RouteVerificationRecord {
  readonly route: string;
  readonly url: string;
  readonly finalUrl: string;
  readonly screenshotFile: string;
  readonly screenshotBytes: number;
  readonly scrollWidth: number;
  readonly clientWidth: number;
  readonly overflowPixels: number;
  readonly hasVisibleText: boolean;
  readonly consoleErrors: readonly string[];
  readonly pageErrors: readonly string[];
  readonly serverErrors: readonly { readonly url: string; readonly status: number }[];
  readonly passed: boolean;
}

export interface LiveVerifyReport {
  readonly origin: string;
  readonly slug: string;
  readonly label?: string;
  readonly viewport: { readonly width: number; readonly height: number };
  readonly principalSubject: string | null;
  readonly verifiedAt: string;
  readonly passed: boolean;
  readonly routes: readonly RouteVerificationRecord[];
}

export interface LiveVerifyExecutionResult {
  readonly exitCode: number;
  readonly reason: string;
  readonly evidenceDir: string;
  readonly reportPath?: string;
  readonly report?: LiveVerifyReport;
}

export interface RunLiveVerificationOptions {
  readonly repoRoot?: string;
  readonly checkHealth?: (origin: string, timeoutMs: number) => Promise<OriginHealthCheckResult>;
  readonly openSession?: (origin: string) => Promise<VerifyBrowserSession>;
}

const VALID_SLUG_RE = /^[A-Za-z0-9._-]+$/;
const LOOPBACK_HOSTS = new Set(['localhost', '127.0.0.1', '::1', '[::1]']);

function validateSegment(value: string, flagName: string): string {
  const trimmed = value.trim();
  if (!trimmed || trimmed === '.' || trimmed === '..' || !VALID_SLUG_RE.test(trimmed)) {
    throw new Error(`Invalid ${flagName} "${value}": expected a single filename-safe segment [A-Za-z0-9._-]+`);
  }
  return trimmed;
}

function normalizeRoute(raw: string): string {
  const trimmed = raw.trim();
  if (!trimmed) {
    throw new Error('Route must not be empty');
  }
  return trimmed.startsWith('/') ? trimmed : `/${trimmed}`;
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
    throw new Error(`Invalid ${flagName} "${raw}": expected a positive integer in milliseconds`);
  }
  const value = Number.parseInt(trimmed, 10);
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new Error(`Invalid ${flagName} "${raw}": expected a positive integer in milliseconds`);
  }
  return value;
}

function routeToFileSlug(route: string): string {
  const cleaned = route
    .replace(/^\/+/, '')
    .replace(/[^A-Za-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
  return cleaned || 'root';
}

function readNextValue(argv: readonly string[], index: number, flag: string): { value: string; nextIndex: number } {
  const next = argv[index + 1];
  if (next === undefined || next.startsWith('--')) {
    throw new Error(`Missing value for ${flag}`);
  }
  return { value: next, nextIndex: index + 1 };
}

/** Parse CLI arguments for the Tier-2 live web verifier. */
export function parseLiveVerifyArguments(argv: readonly string[]): ParsedLiveVerifyArgs {
  let help = false;
  let slug = DEFAULT_LIVE_SLUG;
  const routes: string[] = [];
  let origin = DEFAULT_LIVE_ORIGIN;
  let label: string | undefined;
  let timeoutMs = DEFAULT_TIMEOUT_MS;

  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i] ?? '';
    if (arg === '--') continue;
    if (arg === '--help' || arg === '-h') {
      help = true;
      continue;
    }
    if (arg === '--slug' || arg.startsWith('--slug=')) {
      const { value, nextIndex } = arg.startsWith('--slug=')
        ? { value: arg.slice('--slug='.length), nextIndex: i }
        : readNextValue(argv, i, '--slug');
      i = nextIndex;
      slug = validateSegment(value, '--slug');
      continue;
    }
    if (arg === '--route' || arg.startsWith('--route=')) {
      const { value, nextIndex } = arg.startsWith('--route=')
        ? { value: arg.slice('--route='.length), nextIndex: i }
        : readNextValue(argv, i, '--route');
      i = nextIndex;
      routes.push(normalizeRoute(value));
      continue;
    }
    if (arg === '--routes' || arg.startsWith('--routes=')) {
      const { value, nextIndex } = arg.startsWith('--routes=')
        ? { value: arg.slice('--routes='.length), nextIndex: i }
        : readNextValue(argv, i, '--routes');
      i = nextIndex;
      for (const token of value.split(',')) {
        if (token.trim()) routes.push(normalizeRoute(token));
      }
      continue;
    }
    if (arg === '--origin' || arg.startsWith('--origin=')) {
      const { value, nextIndex } = arg.startsWith('--origin=')
        ? { value: arg.slice('--origin='.length), nextIndex: i }
        : readNextValue(argv, i, '--origin');
      i = nextIndex;
      origin = normalizeOrigin(value);
      continue;
    }
    if (arg === '--label' || arg.startsWith('--label=')) {
      const { value, nextIndex } = arg.startsWith('--label=')
        ? { value: arg.slice('--label='.length), nextIndex: i }
        : readNextValue(argv, i, '--label');
      i = nextIndex;
      label = validateSegment(value, '--label');
      continue;
    }
    if (arg === '--timeout-ms' || arg.startsWith('--timeout-ms=')) {
      const { value, nextIndex } = arg.startsWith('--timeout-ms=')
        ? { value: arg.slice('--timeout-ms='.length), nextIndex: i }
        : readNextValue(argv, i, '--timeout-ms');
      i = nextIndex;
      timeoutMs = parsePositiveInt(value, '--timeout-ms');
      continue;
    }
    throw new Error(`Unknown argument: ${arg}`);
  }

  return {
    help,
    slug,
    routes: routes.length > 0 ? routes : [...DEFAULT_LIVE_ROUTES],
    origin,
    ...(label !== undefined ? { label } : {}),
    timeoutMs,
  };
}

/** Check `${origin}/healthz` without mutating process-wide TLS settings. */
export async function checkOriginHealth(origin: string, timeoutMs = DEFAULT_TIMEOUT_MS): Promise<OriginHealthCheckResult> {
  const target = new URL('/healthz', origin);
  const requestTimeout = Math.min(timeoutMs, 10000);
  const isHttps = target.protocol === 'https:';
  const allowSelfSigned = isHttps && LOOPBACK_HOSTS.has(target.hostname);

  return new Promise<OriginHealthCheckResult>((resolvePromise) => {
    const onResponse = (response: http.IncomingMessage): void => {
      const status = response.statusCode ?? null;
      response.resume();
      if (status !== null && status >= 200 && status < 300) {
        resolvePromise({ ok: true, status, reason: `HTTP ${status}` });
      } else {
        resolvePromise({ ok: false, status, reason: `unexpected HTTP ${status ?? 'unknown'} from ${target.pathname}` });
      }
    };

    const req = isHttps
      ? https.request(
          target,
          {
            method: 'GET',
            timeout: requestTimeout,
            rejectUnauthorized: !allowSelfSigned,
          },
          onResponse,
        )
      : http.request(
          target,
          {
            method: 'GET',
            timeout: requestTimeout,
          },
          onResponse,
        );

    req.on('timeout', () => {
      req.destroy(new Error(`timed out after ${requestTimeout}ms`));
    });
    req.on('error', (error: Error) => {
      resolvePromise({ ok: false, status: null, reason: error.message });
    });
    req.end();
  });
}

function readPrincipalSubject(principal: unknown): string | null {
  if (typeof principal !== 'object' || principal === null) return null;
  if (!('principal' in principal)) return null;
  const nested = principal.principal;
  if (typeof nested !== 'object' || nested === null || !('subject' in nested)) return null;
  return typeof nested.subject === 'string' ? nested.subject : null;
}

/** Run Tier-2 live web route verification and capture 1440x900 evidence. */
export async function runLiveVerification(
  args: ParsedLiveVerifyArgs,
  options: RunLiveVerificationOptions = {},
): Promise<LiveVerifyExecutionResult> {
  const defaultRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
  const rootDir = options.repoRoot ?? defaultRoot;
  const evidenceDir = resolve(rootDir, '.scratch', 'verify', args.slug);
  const checkHealthFn = options.checkHealth ?? checkOriginHealth;
  const openSessionFn = options.openSession ?? ((origin: string) => localAdministratorBrowser(origin));

  const health = await checkHealthFn(args.origin, args.timeoutMs);
  if (!health.ok) {
    return {
      exitCode: 2,
      reason: `PRECONDITION_UNMET: ${args.origin}/healthz is unreachable (${health.reason}). Start the local hvac-local stack or pass --origin before running verify:live.`,
      evidenceDir,
    };
  }

  mkdirSync(evidenceDir, { recursive: true });

  const session = await openSessionFn(args.origin);
  const prefix = args.label ? `${args.label}-` : '';
  const records: RouteVerificationRecord[] = [];
  const writtenFiles: string[] = [];

  try {
    await session.page.setViewportSize({ width: VIEWPORT_WIDTH, height: VIEWPORT_HEIGHT });

    let activeConsoleErrors: string[] = [];
    let activePageErrors: string[] = [];
    let activeServerErrors: { readonly url: string; readonly status: number }[] = [];

    session.page.on('console', (message) => {
      if (message.type() === 'error') {
        activeConsoleErrors.push(message.text());
      }
    });
    session.page.on('pageerror', (error) => {
      activePageErrors.push(error.message);
    });
    session.page.on('response', (response) => {
      if (response.status() >= 500) {
        activeServerErrors.push({ url: response.url(), status: response.status() });
      }
    });

    for (let index = 0; index < args.routes.length; index += 1) {
      const route = args.routes[index] ?? '/';
      activeConsoleErrors = [];
      activePageErrors = [];
      activeServerErrors = [];

      const url = `${args.origin}${route}`;
      await session.page.goto(url, { waitUntil: 'domcontentloaded', timeout: args.timeoutMs });
      try {
        await session.page.waitForLoadState('networkidle', { timeout: Math.min(5000, args.timeoutMs) });
      } catch {
        // Pages backed by SSE or polling telemetry keep an active stream open; settle after initial data load instead.
      }
      try {
        await session.page.waitForFunction(
          () => (document.body?.innerText ?? '').trim().length > 0,
          undefined,
          { timeout: Math.min(10000, args.timeoutMs) },
        );
      } catch {
        // Still capture screenshot and report telemetry so blank-screen failures leave visual proof on disk.
      }

      const layout = await session.page.evaluate(() => ({
        scrollWidth: document.documentElement.scrollWidth,
        clientWidth: document.documentElement.clientWidth,
        hasVisibleText: (document.body?.innerText ?? '').trim().length > 0,
      }));

      const screenshotFile = `${prefix}${String(index + 1).padStart(2, '0')}-${routeToFileSlug(route)}.png`;
      const screenshotPath = join(evidenceDir, screenshotFile);
      await session.page.screenshot({ path: screenshotPath, fullPage: true });
      const screenshotBytes = statSync(screenshotPath).size;
      writtenFiles.push(screenshotPath);

      const overflowPixels = Math.max(0, layout.scrollWidth - layout.clientWidth);
      const passed =
        layout.hasVisibleText &&
        overflowPixels === 0 &&
        activeConsoleErrors.length === 0 &&
        activePageErrors.length === 0 &&
        activeServerErrors.length === 0 &&
        screenshotBytes > 0;

      records.push({
        route,
        url,
        finalUrl: session.page.url(),
        screenshotFile,
        screenshotBytes,
        scrollWidth: layout.scrollWidth,
        clientWidth: layout.clientWidth,
        overflowPixels,
        hasVisibleText: layout.hasVisibleText,
        consoleErrors: [...activeConsoleErrors],
        pageErrors: [...activePageErrors],
        serverErrors: [...activeServerErrors],
        passed,
      });
    }
  } finally {
    await session.browser.close();
  }

  const allPassed = records.every((record) => record.passed);
  const report: LiveVerifyReport = {
    origin: args.origin,
    slug: args.slug,
    ...(args.label !== undefined ? { label: args.label } : {}),
    viewport: { width: VIEWPORT_WIDTH, height: VIEWPORT_HEIGHT },
    principalSubject: readPrincipalSubject(session.principal),
    verifiedAt: new Date().toISOString(),
    passed: allPassed,
    routes: records,
  };

  const reportPath = join(evidenceDir, `${prefix}report.json`);
  writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  writtenFiles.push(reportPath);

  for (const filePath of writtenFiles) {
    const info = statSync(filePath);
    if (info.size <= 0) {
      throw new Error(`Verification evidence file is empty after teardown: ${filePath}`);
    }
  }

  return {
    exitCode: allPassed ? 0 : 1,
    reason: allPassed
      ? `Verified ${records.length} live route(s) at ${VIEWPORT_WIDTH}x${VIEWPORT_HEIGHT}; evidence saved in ${evidenceDir}`
      : `One or more live routes failed checks; evidence retained in ${evidenceDir}`,
    evidenceDir,
    reportPath,
    report,
  };
}

function bridgeThroughWsl(scriptPath: string, argv: readonly string[]): number {
  const wslScriptPath = windowsPathToWsl(scriptPath);
  const result = spawnSync(
    'wsl',
    [
      '-e',
      'bash',
      '-lc',
      'exec node --experimental-strip-types "$@"',
      '_',
      wslScriptPath,
      ...argv,
    ],
    {
      stdio: 'inherit',
      windowsHide: true,
    },
  );
  if (result.error) {
    throw new Error(`Failed to run WSL live web verifier: ${result.error.message}`);
  }
  return result.status ?? 1;
}

const USAGE = [
  'Usage: node --experimental-strip-types scripts/verify-live-web.ts [options]',
  '  --slug=<name>         Subdirectory under .scratch/verify/ (default: live-web)',
  '  --route=<path>        Route to verify (repeatable; default: /operations/realtime, /system?tab=site)',
  '  --routes=<p1,p2>      Comma-separated routes to verify',
  '  --origin=<url>        Live origin (default: https://localhost:8443)',
  '  --label=<name>        Optional filename prefix (e.g. before / after)',
  '  --timeout-ms=<ms>     Navigation/health timeout in ms (default: 30000)',
  '  --help                Show this help text',
].join('\n');

async function runCli(argv: readonly string[], scriptPath: string): Promise<void> {
  const parsed = parseLiveVerifyArguments(argv);
  if (parsed.help) {
    process.stdout.write(`${USAGE}\n`);
    return;
  }

  if (process.platform === 'win32') {
    process.exitCode = bridgeThroughWsl(scriptPath, argv);
    return;
  }

  const outcome = await runLiveVerification(parsed);
  if (outcome.report) {
    process.stdout.write(`${JSON.stringify(outcome.report, null, 2)}\n`);
  }
  if (outcome.exitCode !== 0) {
    process.stderr.write(`${outcome.reason}\n`);
  } else {
    process.stdout.write(`${outcome.reason}\n`);
  }
  process.exitCode = outcome.exitCode;
}

const currentScriptPath = fileURLToPath(import.meta.url);
const invokedScriptPath = process.argv[1] ? resolve(process.argv[1]) : null;
if (invokedScriptPath === resolve(currentScriptPath)) {
  runCli(process.argv.slice(2), currentScriptPath).catch((error: unknown) => {
    const message = error instanceof Error ? error.message : String(error);
    process.stderr.write(`${message}\n`);
    process.exitCode = 1;
  });
}
