import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { createServer } from 'node:net';
import { resolve } from 'node:path';

const root = resolve(process.cwd());

function findAvailablePort() {
  return new Promise((resolvePort, rejectPort) => {
    const probe = createServer();
    probe.unref();
    probe.once('error', rejectPort);
    probe.listen(0, '127.0.0.1', () => {
      const address = probe.address();
      if (!address || typeof address === 'string') {
        probe.close(() => rejectPort(new Error('Unable to allocate an audit preview port')));
        return;
      }
      probe.close((error) => {
        if (error) rejectPort(error);
        else resolvePort(address.port);
      });
    });
  });
}

const configuredPort = process.env.HVAC_AUDIT_PORT;
const port = configuredPort === undefined ? await findAvailablePort() : Number(configuredPort);
if (!Number.isInteger(port) || port < 1 || port > 65_535) {
  throw new Error(`Invalid HVAC_AUDIT_PORT: ${configuredPort}`);
}
const baseUrl = `http://127.0.0.1:${port}`;
const skipBuild = process.argv.includes('--skip-build');
const npmCommand = process.platform === 'win32' ? 'npm.cmd' : 'npm';
const npxCommand = process.platform === 'win32' ? 'npx.cmd' : 'npx';
const pause = (ms) => new Promise((resolvePause) => setTimeout(resolvePause, ms));

function run(command, args, options = {}) {
  return new Promise((resolveRun, rejectRun) => {
    const executable = process.platform === 'win32' ? 'cmd.exe' : command;
    const executableArgs = process.platform === 'win32'
      ? ['/d', '/s', '/c', [command, ...args].join(' ')]
      : args;
    const child = spawn(executable, executableArgs, {
      cwd: root,
      stdio: 'inherit',
      env: { ...process.env, ...options.env },
      shell: false,
    });
    child.once('error', rejectRun);
    child.once('exit', (code, signal) => {
      if (code === 0) resolveRun();
      else rejectRun(new Error(`${command} ${args.join(' ')} failed with ${signal ?? code}`));
    });
  });
}

async function waitForServer() {
  for (let index = 0; index < 160; index += 1) {
    try {
      const response = await fetch(`${baseUrl}/dashboard`);
      if (response.ok) return;
    } catch {}
    await pause(100);
  }
  throw new Error(`Preview server did not become ready at ${baseUrl}`);
}

async function stopServer(server) {
  if (server.exitCode !== null || server.signalCode !== null) return;
  server.kill('SIGTERM');
  const stopped = await Promise.race([
    once(server, 'exit').then(() => true),
    pause(1200).then(() => false),
  ]);
  if (!stopped && process.platform === 'win32') {
    spawnSync('taskkill', ['/PID', String(server.pid), '/T', '/F'], { stdio: 'ignore' });
  }
}

let preview;
try {
  if (!skipBuild) {
    console.log('\n[1/7] TypeScript validation');
    await run(npmCommand, ['run', 'lint']);

    console.log('\n[2/7] Production build');
    await run(npmCommand, ['run', 'build']);
  } else {
    console.log('\n[1-2/7] Reusing existing dist (--skip-build)');
  }

  console.log(`\n[3/7] Starting production preview at ${baseUrl}`);
  preview = spawn(process.execPath, [
    resolve(root, 'node_modules/vite/bin/vite.js'),
    'preview',
    'apps/hvac-web',
    '--config', 'apps/hvac-web/vite.config.ts',
    '--host', '127.0.0.1',
    '--port', String(port),
    '--strictPort',
  ], {
    cwd: root,
    stdio: 'inherit',
    env: process.env,
    shell: false,
  });
  preview.once('error', (error) => {
    console.error('Preview server error:', error);
  });
  await waitForServer();

  console.log('\n[4/7] Full-site UI audit');
  await run(process.execPath, ['scripts/ui-audit.mjs'], {
    env: { HVAC_AUDIT_BASE_URL: baseUrl },
  });

  console.log('\n[5/7] BigScreen layout audit');
  await run(process.execPath, ['scripts/bigscreen-layout-audit.mjs'], {
    env: { HVAC_AUDIT_BASE_URL: baseUrl },
  });

  console.log('\n[6/7] HVAC operations loop audit');
  await run(process.execPath, ['scripts/ops-loop-audit.mjs'], {
    env: { HVAC_AUDIT_BASE_URL: baseUrl },
  });

  console.log('\n[7/7] Impeccable design audit');
  await run(npxCommand, ['-y', 'impeccable', 'detect', 'apps/hvac-web/src', '--no-config']);

  console.log('\nRelease audit passed.');
} finally {
  if (preview) await stopServer(preview);
}
