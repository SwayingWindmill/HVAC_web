import { spawnSync, type SpawnSyncOptionsWithStringEncoding, type SpawnSyncReturns } from 'node:child_process';

const boundedInteger = (name: string, value: unknown, minimum: number, maximum: number): number => {
  const parsed = Number.parseInt(String(value), 10);
  if (!Number.isInteger(parsed) || parsed < minimum || parsed > maximum) {
    throw new Error(`${name} must be an integer between ${minimum} and ${maximum}`);
  }
  return parsed;
};

const boundedDetail = (value: unknown): string => String(value ?? '').trim().slice(0, 2000);
const defaultPause = (milliseconds: number): Promise<void> => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

export function dockerComposeInvocation(args: string[]): { command: string; args: string[] } {
  return { command: 'docker', args: ['compose', ...args] };
}

export function runDockerCompose<TResult, TOptions = Record<string, unknown>>(
  run: (command: string, args: string[], options?: TOptions) => TResult,
  args: string[],
  options?: TOptions,
): TResult {
  const invocation = dockerComposeInvocation(args);
  return run(invocation.command, invocation.args, options);
}

export interface PullDockerImageOptions {
  attempts?: number;
  retryBaseMs?: number;
  timeoutMs?: number;
  spawn?: (
    command: string,
    args: readonly string[],
    options: SpawnSyncOptionsWithStringEncoding,
  ) => Partial<SpawnSyncReturns<string>>;
  pause?: (milliseconds: number) => Promise<void>;
  warn?: (message: string) => void;
  dockerBinary?: string;
  cwd?: string;
  env?: NodeJS.ProcessEnv;
}

export async function pullDockerImageWithRetry(
  image: string,
  options: PullDockerImageOptions = {},
): Promise<Partial<SpawnSyncReturns<string>>> {
  if (typeof image !== 'string' || !image.trim()) throw new Error('docker image reference is required');
  if (!/@sha256:[0-9a-f]{64}$/.test(image)) throw new Error('docker image reference must use an immutable sha256 digest');

  const attempts = boundedInteger('docker pull attempts', options.attempts ?? 5, 1, 10);
  const retryBaseMs = boundedInteger('docker pull retry base milliseconds', options.retryBaseMs ?? 1000, 0, 10000);
  const timeoutMs = boundedInteger('docker pull timeout milliseconds', options.timeoutMs ?? 45000, 1000, 120000);
  const spawn = options.spawn ?? spawnSync;
  const pause = options.pause ?? defaultPause;
  const warn = options.warn ?? console.warn;
  let lastFailure = '';

  for (let attempt = 1; attempt <= attempts; attempt += 1) {
    const result = spawn(options.dockerBinary ?? 'docker', ['pull', image], {
      cwd: options.cwd,
      env: options.env ?? process.env,
      encoding: 'utf8',
      timeout: timeoutMs,
      windowsHide: true,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    if (!result.error && result.status === 0 && !result.signal) return result;

    lastFailure = JSON.stringify({
      exitCode: result.status,
      signal: result.signal,
      error: boundedDetail(result.error?.message),
      stderr: boundedDetail(result.stderr),
      stdout: boundedDetail(result.stdout),
    });
    if (attempt < attempts) {
      warn(`docker pull attempt ${attempt}/${attempts} failed for immutable image; retrying`);
      await pause(Math.min(attempt * retryBaseMs, 10000));
    }
  }

  throw new Error(`docker pull failed after ${attempts} attempts for ${image}: ${lastFailure}`);
}
