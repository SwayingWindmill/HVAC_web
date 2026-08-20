import { spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { createServer as createTCPServer } from 'node:net';
import { dockerComposeInvocation } from './docker-cli.mjs';

export function runCommand(command: string, args: string[], options: Record<string, unknown> = {}) {
  const result = spawnSync(command, args, {
    encoding: 'utf8',
    windowsHide: true,
    ...options,
  });
  if (result.error || result.status !== 0) {
    throw new Error(`${command} ${args.join(' ')} failed: ${result.error?.message ?? String(result.stderr ?? '').trim() ?? result.status}`);
  }
  return String(result.stdout ?? '').trim();
}

export async function findAvailablePort(label: string, requestedPort = 0) {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: Number(requestedPort) || 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') {
    server.close();
    throw new Error(`${label} port allocator did not expose a TCP address`);
  }
  await new Promise<void>((resolveClose, rejectClose) => {
    server.close((error) => error ? rejectClose(error) : resolveClose());
  });
  return address.port;
}

export function expectEqual(actual: string, expected: string, label: string) {
  if (actual !== expected) throw new Error(`${label}: expected ${expected}, got ${actual}`);
}

export type PostgresAuthorityReport = {
  schemaVersion: number;
  slice: string;
  ticket: string;
  status: string;
  startedAt: string;
  postgresImage: string;
  assertions: Record<string, unknown>;
  completedAt?: string;
  error?: string;
};

type HarnessOptions = {
  root: string;
  composePath: string;
  projectName: string;
  database: string;
  hostPortEnvName: string;
  portAllocatorLabel: string;
  requestedPort?: number | string;
};

export async function createPostgresComposeHarness(options: HarnessOptions) {
  const {
    root,
    composePath,
    projectName,
    database,
    hostPortEnvName,
    portAllocatorLabel,
    requestedPort = 0,
  } = options;
  const postgresHostPort = await findAvailablePort(portAllocatorLabel, Number(requestedPort) || 0);
  const composeEnvironment = { ...process.env, [hostPortEnvName]: String(postgresHostPort) };
  const containerName = `${projectName}-postgres-1`;

  function run(command: string, args: string[], commandOptions: Record<string, unknown> = {}) {
    return runCommand(command, args, { cwd: root, ...commandOptions });
  }

  function compose(args: string[]) {
    const invocation = dockerComposeInvocation(['-p', projectName, '-f', composePath, ...args]);
    return run(invocation.command, invocation.args, { env: composeEnvironment });
  }

  function psql(sql: string, { expectFailure = false } = {}) {
    const result = spawnSync('docker', [
      'exec', containerName, 'psql', '-U', 'postgres', '-d', database, '-v', 'ON_ERROR_STOP=1', '-Atqc', sql,
    ], { cwd: root, encoding: 'utf8', windowsHide: true });
    const stdout = String(result.stdout ?? '').trim();
    const stderr = String(result.stderr ?? '').trim();
    if (expectFailure) {
      if (!result.error && result.status === 0) throw new Error(`SQL unexpectedly succeeded: ${sql}`);
      return `${stdout}\n${stderr}`.trim();
    }
    if (result.error || result.status !== 0) {
      throw new Error(`SQL failed: ${result.error?.message ?? stderr ?? result.status}\n${sql}`);
    }
    return stdout;
  }

  return {
    postgresHostPort,
    run,
    compose,
    psql,
    pause: (milliseconds: number) => new Promise<void>((resolvePause) => setTimeout(resolvePause, milliseconds)),
  };
}
