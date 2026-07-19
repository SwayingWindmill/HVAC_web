import { spawn } from 'node:child_process';
import process from 'node:process';

const isWindows = process.platform === 'win32';
const uvCommand = isWindows ? 'uv.exe' : 'uv';
const children = [];
const logs = new Map();

const sleep = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

function start(name, command, args, environment) {
  const child = spawn(command, args, {
    cwd: process.cwd(),
    env: environment,
    stdio: ['ignore', 'pipe', 'pipe'],
    windowsHide: true,
  });

  let output = '';
  logs.set(name, () => output);
  child.stdout.setEncoding('utf8');
  child.stderr.setEncoding('utf8');
  child.stdout.on('data', (chunk) => {
    output += chunk;
  });
  child.stderr.on('data', (chunk) => {
    output += chunk;
  });
  children.push({ name, child });
  return child;
}

function terminateProcessTree(child) {
  if (!child.pid) return;

  if (isWindows) {
    const killer = spawn('taskkill.exe', ['/pid', String(child.pid), '/T', '/F'], {
      stdio: 'ignore',
      windowsHide: true,
    });
    return new Promise((resolve) => killer.once('exit', resolve));
  }

  child.kill('SIGTERM');
  return Promise.race([
    new Promise((resolve) => child.once('exit', resolve)),
    sleep(2_000).then(() => child.kill('SIGKILL')),
  ]);
}

async function waitForJson(url, expectedStatus, label, child, timeoutMilliseconds = 60_000) {
  const deadline = Date.now() + timeoutMilliseconds;
  let lastError = null;

  while (Date.now() < deadline) {
    if (child.exitCode !== null || child.signalCode !== null) {
      const output = logs.get(label)?.() || '';
      throw new Error(`${label} exited before readiness.\n${output}`);
    }

    try {
      const response = await fetch(url, { signal: AbortSignal.timeout(2_000) });
      const text = await response.text();
      if (response.status === expectedStatus) {
        return { response, text, json: JSON.parse(text) };
      }
      lastError = new Error(`${label} returned HTTP ${response.status}: ${text}`);
    } catch (error) {
      lastError = error;
    }

    await sleep(500);
  }

  const output = logs.get(label)?.() || '';
  throw new Error(`${label} did not become ready: ${lastError?.message || 'unknown error'}\n${output}`);
}

const environment = {
  ...process.env,
  OPENAI_API_KEY: process.env.OPENAI_API_KEY || 'verification-only-key',
  OPENAI_MODEL: process.env.OPENAI_MODEL || 'verification-only-model',
  ENERGY_DATA_SEED: process.env.ENERGY_DATA_SEED || '20260716',
};

try {
  const agent = start(
    'energy-agent',
    uvCommand,
    [
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
    environment,
  );

  const agentHealth = await waitForJson(
    'http://127.0.0.1:8123/health',
    200,
    'energy-agent',
    agent,
  );
  if (agentHealth.json.service !== 'energyagent-agent') {
    throw new Error(`Unexpected EnergyAgent health payload: ${agentHealth.text}`);
  }

  const runtime = start(
    'ai-runtime',
    process.execPath,
    ['runtimes/copilot-runtime/server.mjs'],
    {
      ...environment,
      AI_RUNTIME_HOST: '127.0.0.1',
      AI_RUNTIME_PORT: '3001',
      ENERGY_AGENT_URL: 'http://127.0.0.1:8123',
      ENERGY_AGENT_GRAPH_ID: 'sample_agent',
    },
  );

  const runtimeHealth = await waitForJson(
    'http://127.0.0.1:3001/health',
    200,
    'ai-runtime',
    runtime,
    30_000,
  );
  if (runtimeHealth.json.agent?.status !== 'ok') {
    throw new Error(`Unexpected Runtime health payload: ${runtimeHealth.text}`);
  }

  const infoResponse = await fetch('http://127.0.0.1:3001/api/v1/copilotkit/info', {
    signal: AbortSignal.timeout(3_000),
  });
  const infoText = await infoResponse.text();
  if (!infoResponse.ok || !infoText.includes('default')) {
    throw new Error(`Runtime info failed (${infoResponse.status}): ${infoText}`);
  }

  console.log('EnergyAgent health: 200 (energyagent-agent)');
  console.log('Copilot Runtime health: 200 (upstream Agent ready)');
  console.log('Copilot Runtime info: 200 (default Agent registered)');
} finally {
  await Promise.allSettled(children.reverse().map(({ child }) => terminateProcessTree(child)));
}
