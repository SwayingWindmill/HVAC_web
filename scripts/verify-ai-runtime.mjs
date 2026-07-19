import { spawn } from 'node:child_process';
import process from 'node:process';

const runtimeUrl = 'http://127.0.0.1:3001';
const child = spawn(process.execPath, ['runtimes/copilot-runtime/server.mjs'], {
  cwd: process.cwd(),
  env: {
    ...process.env,
    AI_RUNTIME_HOST: '127.0.0.1',
    AI_RUNTIME_PORT: '3001',
  },
  stdio: ['ignore', 'pipe', 'pipe'],
});

let output = '';
let childExit = null;

child.stdout.setEncoding('utf8');
child.stderr.setEncoding('utf8');
child.stdout.on('data', (chunk) => {
  output += chunk;
});
child.stderr.on('data', (chunk) => {
  output += chunk;
});
child.once('exit', (code, signal) => {
  childExit = { code, signal };
});

const sleep = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

async function fetchWithDeadline(url) {
  return fetch(url, { signal: AbortSignal.timeout(2_000) });
}

async function waitForRuntime() {
  let lastError = null;

  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (childExit) {
      throw new Error(
        `AI Runtime exited before becoming ready (${childExit.signal || childExit.code || 0}).\n${output}`,
      );
    }

    try {
      const response = await fetchWithDeadline(`${runtimeUrl}/api/v1/copilotkit/info`);
      if (response.ok) return response;
      lastError = new Error(`Runtime info returned HTTP ${response.status}`);
    } catch (error) {
      lastError = error;
    }

    await sleep(250);
  }

  throw lastError || new Error('AI Runtime did not become ready.');
}

try {
  const infoResponse = await waitForRuntime();
  const infoText = await infoResponse.text();
  if (!infoText.includes('default')) {
    throw new Error(`Runtime info did not advertise the default Agent: ${infoText}`);
  }

  const healthResponse = await fetchWithDeadline(`${runtimeUrl}/health`);
  const health = await healthResponse.json();
  if (![200, 503].includes(healthResponse.status)) {
    throw new Error(`Unexpected health status ${healthResponse.status}: ${JSON.stringify(health)}`);
  }
  if (health.service !== 'hvac-copilot-runtime') {
    throw new Error(`Unexpected health payload: ${JSON.stringify(health)}`);
  }

  console.log(`AI Runtime info: ${infoResponse.status} (default Agent registered)`);
  console.log(`AI Runtime health: ${healthResponse.status} (${health.status})`);
} finally {
  child.kill('SIGTERM');
  await Promise.race([
    new Promise((resolve) => child.once('exit', resolve)),
    sleep(2_000),
  ]);

  if (!child.killed) child.kill('SIGKILL');
}
