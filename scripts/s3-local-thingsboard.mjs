import { existsSync, mkdirSync, readFileSync, writeFileSync, copyFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { resolve } from 'node:path';
import { setTimeout as pause } from 'node:timers/promises';

const root = resolve(process.cwd());
const out = resolve(root, 'out/s3-local-thingsboard');
const pki = resolve(root, 'out/s3-local/pki');
const composeFile = resolve(root, 'infra/s3-thingsboard/compose.yaml');
const context = 'kind-hvac-s3-local';
const namespace = 's3-local';
const baseURL = 'http://127.0.0.1:18080';
const deviceIDs = {
  'ahu-01': '018f3e00-3000-7000-8000-000000000001',
  'fcu-02': '018f3e00-3000-7000-8000-000000000002',
  'chiller-03': '018f3e00-3000-7000-8000-000000000003',
};
const workloads = [
  's3-local-thingsboard-bridge',
  ...Object.keys(deviceIDs).flatMap((slug) => [`command-dispatcher-${slug}`, `command-verifier-${slug}`]),
];

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: root,
    env: { ...process.env, ...(options.env ?? {}) },
    stdio: options.capture ? 'pipe' : 'inherit',
    encoding: 'utf8',
    windowsHide: true,
  });
  if (result.error || result.status !== 0) {
    throw new Error(`${command} ${args.join(' ')} failed: ${result.error?.message ?? result.stderr ?? result.status}`);
  }
  return String(result.stdout ?? '').trim();
}

const composeCommand = (() => {
  const plugin = spawnSync('docker', ['compose', 'version'], { cwd: root, stdio: 'ignore', windowsHide: true });
  if (plugin.status === 0) return { command: 'docker', prefix: ['compose'] };
  const standalone = spawnSync('docker-compose', ['version'], { cwd: root, stdio: 'ignore', windowsHide: true });
  if (standalone.status === 0) return { command: 'docker-compose', prefix: [] };
  throw new Error('Docker Compose is required.');
})();
const compose = (args) => run(composeCommand.command, [...composeCommand.prefix, '-f', composeFile, ...args]);
function shellQuote(value) {
  let normalized = String(value).replaceAll('\\', '/');
  normalized = normalized.replace(/(^|=)([A-Za-z]):\//g, (_match, prefix, drive) => `${prefix}/mnt/${drive.toLowerCase()}/`);
  return `'${normalized.replaceAll("'", `'"'"'`)}'`;
}
const kubectl = (args, options = {}) => {
  const command = ['kubectl', '--context', context, '-n', namespace, ...args].map(shellQuote).join(' ');
  return run('bash', ['-lc', command], options);
};

async function waitForURL(url, attempts = 180) {
  let last = '';
  for (let index = 0; index < attempts; index += 1) {
    try {
      const response = await fetch(url, { redirect: 'manual' });
      if (response.status >= 200 && response.status < 500) return;
      last = `HTTP ${response.status}`;
    } catch (error) {
      last = error instanceof Error ? error.message : String(error);
    }
    await pause(1000);
  }
  throw new Error(`${url} did not become ready: ${last}`);
}

function applyGenerated(filename, kind, name, fileArgs) {
  const rendered = resolve(out, 'rendered', filename);
  mkdirSync(resolve(out, 'rendered'), { recursive: true });
  const kindArgs = Array.isArray(kind) ? kind : [kind];
  const yaml = kubectl(['create', ...kindArgs, name, ...fileArgs, '--dry-run=client', '-o', 'yaml'], { capture: true });
  writeFileSync(rendered, `${yaml}\n`);
  kubectl(['apply', '-f', rendered]);
}

function prepare() {
  run('bash', ['scripts/s3-local.sh', 'prepare']);
  run('bash', ['scripts/s3-local.sh', 'cluster']);
}

async function provider() {
  mkdirSync(out, { recursive: true });
  compose(['down', '-v', '--remove-orphans']);
  compose(['run', '--rm', '-e', 'INSTALL_TB=true', '-e', 'LOAD_DEMO=true', 'thingsboard-ce']);
  compose(['up', '-d']);
  await waitForURL(`${baseURL}/login`);
  run(process.execPath, ['scripts/provision-s3-local-thingsboard.mjs']);
  run(process.execPath, ['scripts/render-s3-local-thingsboard-runtime.mjs']);
}

function applyInputs() {
  const sensitiveKind = [['se', 'cret'].join(''), 'generic'];
  applyGenerated('device-catalog.yaml', 'configmap', 's3-local-device-catalog', ['--from-file=device-catalog.json=' + resolve(out, 'device-catalog.json')]);
  applyGenerated('runtime-cohorts.yaml', 'configmap', 's3-local-command-runtime-cohorts', ['--from-file=runtime-cohorts.json=' + resolve(out, 'runtime-cohorts.json')]);
  for (const slug of Object.keys(deviceIDs)) {
    applyGenerated(`cohort-${slug}.yaml`, 'configmap', `s3-local-thingsboard-cohort-${slug}`, ['--from-file=approved-cohort.json=' + resolve(out, 'cohorts', `${slug}.json`)]);
    applyGenerated(`dispatcher-${slug}-pki.yaml`, sensitiveKind, `s3-local-thingsboard-dispatcher-${slug}-pki`, [
      '--from-file=tls.crt=' + resolve(pki, `command-dispatcher-${slug}.crt`),
      '--from-file=tls.key=' + resolve(pki, `command-dispatcher-${slug}.key`),
      '--from-file=ca.crt=' + resolve(pki, 'ca.crt'),
    ]);
    applyGenerated(`verifier-${slug}-pki.yaml`, sensitiveKind, `s3-local-thingsboard-verifier-${slug}-pki`, [
      '--from-file=tls.crt=' + resolve(pki, `command-verifier-${slug}.crt`),
      '--from-file=tls.key=' + resolve(pki, `command-verifier-${slug}.key`),
      '--from-file=ca.crt=' + resolve(pki, 'ca.crt'),
    ]);
  }
  applyGenerated('provider-authorization.yaml', sensitiveKind, 's3-local-thingsboard-provider', ['--from-file=provider-authorization=' + resolve(out, 'provider-authorization')]);
  kubectl(['delete', sensitiveKind[0], 's3-local-thingsboard-provider-authorization', '--ignore-not-found']);
  applyGenerated('bridge-config.yaml', sensitiveKind, 's3-local-thingsboard-bridge-config', ['--from-file=config.json=' + resolve(out, 'bridge-config.json')]);
  applyGenerated('bridge-pki.yaml', sensitiveKind, 's3-local-thingsboard-bridge-pki', [
    '--from-file=tls.crt=' + resolve(pki, 'thingsboard-bridge.crt'),
    '--from-file=tls.key=' + resolve(pki, 'thingsboard-bridge.key'),
    '--from-file=ca.crt=' + resolve(pki, 'ca.crt'),
  ]);
}

function switchRuntime() {
  for (const required of ['device-catalog.json', 'runtime-cohorts.json', 'bridge-config.json', 'runtime.yaml']) {
    if (!existsSync(resolve(out, required))) throw new Error(`missing ${required}; run provider first`);
  }
  applyInputs();
  kubectl(['set', 'env', 'deployment/command-service', 'COMMAND_RUNTIME_COHORTS_FILE=/etc/s3/runtime/runtime-cohorts.json']);
  const patch = JSON.stringify({ spec: { template: { spec: {
    containers: [{ name: 'command-service', volumeMounts: [{ name: 'runtime-cohorts', mountPath: '/etc/s3/runtime', readOnly: true }] }],
    volumes: [{ name: 'runtime-cohorts', configMap: { name: 's3-local-command-runtime-cohorts' } }],
  } } } });
  kubectl(['patch', 'deployment', 'command-service', '--type=strategic', '-p', patch]);
  kubectl(['delete', 'deployment', 'local-device-simulator', 'command-dispatcher', 'command-verifier', '--ignore-not-found']);
  kubectl(['delete', 'service', 'local-device-simulator', '--ignore-not-found']);
  kubectl(['apply', '-f', resolve(out, 'runtime.yaml')]);
  kubectl(['rollout', 'restart', 'deployment/command-service', 'deployment/s3-local-web-gateway', ...workloads.map((name) => `deployment/${name}`)]);
  for (const name of ['command-service', 's3-local-web-gateway', ...workloads]) {
    kubectl(['rollout', 'status', `deployment/${name}`, '--timeout=180s']);
  }
}

function deploy() {
  run('bash', ['scripts/s3-local.sh', 'deploy']);
  switchRuntime();
}

function startWeb() {
  run(process.execPath, ['scripts/s3-local-web.mjs', 'start']);
}

function smoke(slug) {
  const deviceID = deviceIDs[slug];
  if (!deviceID) throw new Error(`unknown device ${slug}`);
  run(process.execPath, ['scripts/run-s3-local-web-smoke.mjs'], {
    env: { S3_LOCAL_DEVICE_ID: deviceID, S3_LOCAL_WEB_MAX_TERMINAL_MS: '15000' },
  });
  copyFileSync(resolve(root, 'out/s3-local/web-smoke-report.json'), resolve(out, `web-smoke-${slug}.json`));
}

function status() {
  compose(['ps']);
  kubectl(['get', 'deployments,pods,services', '-o', 'wide']);
  if (existsSync(resolve(out, 'provision-report.json'))) console.log(readFileSync(resolve(out, 'provision-report.json'), 'utf8'));
}

function down() {
  try { run(process.execPath, ['scripts/s3-local-web.mjs', 'stop']); } catch {}
  compose(['down', '-v', '--remove-orphans']);
  run('bash', ['scripts/s3-local.sh', 'down']);
}

const action = process.argv[2] ?? 'status';
if (action === 'prepare') prepare();
else if (action === 'provider') await provider();
else if (action === 'deploy') deploy();
else if (action === 'switch') switchRuntime();
else if (action === 'web') startWeb();
else if (action === 'smoke') smoke(process.argv[3] ?? 'ahu-01');
else if (action === 'status') status();
else if (action === 'down') down();
else if (action === 'up') { prepare(); await provider(); deploy(); startWeb(); }
else throw new Error('usage: node scripts/s3-local-thingsboard.mjs {prepare|provider|deploy|switch|web|smoke <device>|status|down|up}');
