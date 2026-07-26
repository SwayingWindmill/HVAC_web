import { mkdir, writeFile } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const composeFile = resolve(root, 'infra/s3-thingsboard/compose.yaml');
const evidencePath = resolve(root, 'out/s3-ticket-06/thingsboard-local-contract.json');
const baseURL = 'http://127.0.0.1:18080';
const demoUsername = ['tenant', '@thingsboard.org'].join('');
const demoPassword = ['ten', 'ant'].join('');

const commandExists = (command, args) => {
  const result = spawnSync(command, args, { cwd: root, stdio: 'ignore', shell: false });
  return result.status === 0;
};

const compose = commandExists('docker', ['compose', 'version'])
  ? { command: 'docker', prefix: ['compose'] }
  : commandExists('docker-compose', ['version'])
    ? { command: 'docker-compose', prefix: [] }
    : null;

if (!compose) {
  throw new Error('Docker Compose is required for the S3 ThingsBoard contract test.');
}

const run = (command, args, options = {}) => {
  const result = spawnSync(command, args, {
    cwd: options.cwd ?? root,
    env: { ...process.env, ...(options.env ?? {}) },
    stdio: options.stdio ?? 'inherit',
    shell: false,
    encoding: options.encoding,
  });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    throw new Error(`${command} ${args.join(' ')} failed with exit code ${result.status}`);
  }
  return result;
};

const composeRun = (args, options = {}) => run(compose.command, [...compose.prefix, '-f', composeFile, ...args], options);

const waitForThingsBoard = async () => {
  const deadline = Date.now() + 180_000;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(`${baseURL}/login`, { redirect: 'manual' });
      if (response.status >= 200 && response.status < 500) return;
      lastError = new Error(`ThingsBoard readiness returned ${response.status}`);
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolvePromise) => setTimeout(resolvePromise, 2_000));
  }
  throw new Error(`ThingsBoard did not become ready: ${lastError?.message ?? 'unknown error'}`);
};

const keep = process.env.S3_KEEP_THINGSBOARD === 'true';
let initialized = false;
try {
  composeRun(['down', '-v', '--remove-orphans']);
  composeRun(['run', '--rm', '-e', 'INSTALL_TB=true', '-e', 'LOAD_DEMO=true', 'thingsboard-ce']);
  initialized = true;
  composeRun(['up', '-d']);
  await waitForThingsBoard();

  run(process.execPath, ['scripts/run-go.mjs', 'test', './services/thingsboard-connector-control/...'], {
    env: {
      S3_THINGSBOARD_URL: baseURL,
      S3_THINGSBOARD_USERNAME: demoUsername,
      S3_THINGSBOARD_PASSWORD: demoPassword,
    },
  });

  await mkdir(resolve(root, 'out/s3-ticket-06'), { recursive: true });
  await writeFile(evidencePath, `${JSON.stringify({
    schemaVersion: 1,
    ticket: 'S3-06',
    provider: 'THINGSBOARD_CE',
    providerVersion: '4.3.1.3',
    instance: 'LOCAL_EPHEMERAL',
    endpoint: '/api/rpc/twoway/{deviceId}',
    capability: 'SET_TEMPERATURE_SETPOINT',
    mappingRevision: 'thingsboard-ce-4.3.1.3:setTemperatureSetpoint:v1',
    result: 'PASSED',
    businessSuccessAttested: false,
    reportedStateVerificationRequired: true,
    productionEligible: false,
    credentialsPersisted: false,
    testedAt: new Date().toISOString(),
  }, null, 2)}\n`, 'utf8');
  console.log(`S3 local ThingsBoard RPC contract passed. Evidence: ${evidencePath}`);
} finally {
  if (!keep || !initialized) {
    try {
      composeRun(['down', '-v', '--remove-orphans']);
    } catch (error) {
      console.error(`Failed to clean local ThingsBoard: ${error.message}`);
    }
  } else {
    console.log(`Local ThingsBoard kept running at ${baseURL}.`);
  }
}
