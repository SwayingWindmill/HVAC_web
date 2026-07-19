import { spawn } from 'node:child_process';
import process from 'node:process';

const isWindows = process.platform === 'win32';
const npmCommand = isWindows ? 'npm.cmd' : 'npm';
const uvCommand = isWindows ? 'uv.exe' : 'uv';
const children = [];
let stopping = false;

const sharedEnvironment = {
  ...process.env,
  FORCE_COLOR: process.env.FORCE_COLOR || '1',
};

const services = [
  {
    name: 'energy-agent',
    command: uvCommand,
    args: [
      'run',
      '--directory',
      'agents/energy-agent',
      'langgraph',
      'dev',
      '--config',
      'langgraph.json',
      '--port',
      '8123',
      '--no-browser',
    ],
    environment: sharedEnvironment,
  },
  {
    name: 'ai-runtime',
    command: process.execPath,
    args: ['runtimes/copilot-runtime/server.mjs'],
    environment: sharedEnvironment,
  },
  {
    name: 'hvac-web',
    command: npmCommand,
    args: ['run', 'dev:web'],
    environment: {
      ...sharedEnvironment,
      VITE_COPILOTKIT_RUNTIME_URL:
        process.env.VITE_COPILOTKIT_RUNTIME_URL || '/api/v1/copilotkit',
      VITE_AI_AGENT_PROFILE: process.env.VITE_AI_AGENT_PROFILE || 'energyagent',
    },
  },
];

function terminateProcessTree(child) {
  if (!child.pid || child.killed) return;

  if (isWindows) {
    const killer = spawn('taskkill.exe', ['/pid', String(child.pid), '/T', '/F'], {
      stdio: 'ignore',
      windowsHide: true,
    });
    killer.unref();
    return;
  }

  child.kill('SIGTERM');
}

function stop(exitCode = 0) {
  if (stopping) return;
  stopping = true;

  for (const child of children) terminateProcessTree(child);

  setTimeout(() => process.exit(exitCode), 250).unref();
}

for (const service of services) {
  console.log(`[dev:energyagent] starting ${service.name}`);
  const child = spawn(service.command, service.args, {
    cwd: process.cwd(),
    env: service.environment,
    stdio: 'inherit',
    windowsHide: false,
  });

  children.push(child);

  child.once('error', (error) => {
    console.error(`[dev:energyagent] ${service.name} failed to start:`, error.message);
    stop(1);
  });

  child.once('exit', (code, signal) => {
    if (stopping) return;
    console.error(
      `[dev:energyagent] ${service.name} exited unexpectedly (${signal || code || 0})`,
    );
    stop(code || 1);
  });
}

process.once('SIGINT', () => stop(0));
process.once('SIGTERM', () => stop(0));
process.once('SIGHUP', () => stop(0));
