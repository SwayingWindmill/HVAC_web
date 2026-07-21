import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const argument = (name) => process.argv.find((value) => value.startsWith(`--${name}=`))?.slice(name.length + 3);
const cleanup = argument('cleanup') !== 'false';
const reportPath = resolve(root, argument('report') ?? 'out/platform-component-pocs/envoy-gateway.json');
const clusterName = argument('cluster') ?? `hvac-component-poc-${process.pid}`;
const namespace = 'platform-component-poc';
const startedAt = new Date().toISOString();
let portForward = null;
let clusterCreated = false;

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

async function run(command, args, options = {}) {
  const child = spawn(command, args, {
    cwd: root,
    env: process.env,
    shell: false,
    windowsHide: true,
    stdio: options.inherit ? 'inherit' : ['ignore', 'pipe', 'pipe'],
  });
  if (options.inherit) {
    const code = await new Promise((resolveCode) => child.once('exit', resolveCode));
    if (code !== 0) throw new Error(`${command} ${args.join(' ')} exited with ${code}`);
    return { stdout: '', stderr: '' };
  }
  let stdout = '';
  let stderr = '';
  child.stdout.on('data', (chunk) => { stdout += String(chunk); });
  child.stderr.on('data', (chunk) => { stderr += String(chunk); });
  const result = await new Promise((resolveResult) => {
    child.once('error', (error) => resolveResult({ error, code: null }));
    child.once('exit', (code) => resolveResult({ error: null, code }));
  });
  if (result.error || result.code !== 0) {
    throw new Error(`${command} ${args.join(' ')} failed: ${stderr.trim() || stdout.trim() || result.error?.message || result.code}`);
  }
  return { stdout, stderr };
}

async function waitFor(check, message, timeoutMs = 120_000) {
  const deadline = Date.now() + timeoutMs;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const result = await check();
      if (result) return result;
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolvePause) => setTimeout(resolvePause, 500));
  }
  throw new Error(`${message}${lastError ? `: ${lastError.message}` : ''}`);
}

async function fetchJson(path) {
  const response = await fetch(`http://127.0.0.1:18081${path}`);
  const text = await response.text();
  let body = null;
  try { body = JSON.parse(text); } catch { body = text; }
  return { status: response.status, body };
}

async function routeOwner(expected) {
  return waitFor(async () => {
    const response = await fetchJson('/api/v1/status');
    return response.status === 200 && response.body?.owner === expected ? response : false;
  }, `Gateway route did not converge to ${expected}`);
}

async function writeReport(body) {
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(body, null, 2)}\n`);
}

try {
  const lock = JSON.parse(await readFile(resolve(root, 'pocs/platform-components/versions.lock.json'), 'utf8'));
  const component = lock.components.envoyGateway;
  const installerResponse = await fetch(component.installer);
  assert(installerResponse.ok, `Envoy Gateway installer download failed with ${installerResponse.status}`);
  const installer = Buffer.from(await installerResponse.arrayBuffer());
  const digest = createHash('sha256').update(installer).digest('hex');
  assert(digest === component.installerSha256, `Envoy Gateway installer digest mismatch: ${digest}`);
  const installerPath = resolve(root, 'out/platform-component-pocs/envoy-gateway-install.yaml');
  await mkdir(dirname(installerPath), { recursive: true });
  await writeFile(installerPath, installer);

  await run('kind', ['create', 'cluster', '--name', clusterName, '--wait', '120s']);
  clusterCreated = true;
  await run('kubectl', ['apply', '--server-side', '-f', installerPath]);
  await run('kubectl', ['-n', 'envoy-gateway-system', 'rollout', 'status', 'deployment/envoy-gateway', '--timeout=180s']);
  await run('kubectl', ['apply', '-f', 'pocs/platform-components/envoy/manifests.yaml']);
  await run('kubectl', ['-n', namespace, 'rollout', 'status', 'deployment/go-edge', '--timeout=120s']);
  await run('kubectl', ['-n', namespace, 'rollout', 'status', 'deployment/legacy-private', '--timeout=120s']);
  await run('kubectl', ['-n', namespace, 'wait', '--for=condition=Programmed', 'gateway/platform', '--timeout=180s']);

  const service = await waitFor(async () => {
    const services = JSON.parse((await run('kubectl', ['get', 'service', '-A', '-o', 'json'])).stdout);
    return services.items.find((item) => {
      const labels = item.metadata?.labels ?? {};
      const annotations = item.metadata?.annotations ?? {};
      const ownsGateway = labels['gateway.envoyproxy.io/owning-gateway-name'] === 'platform'
        || annotations['gateway.envoyproxy.io/owning-gateway-name'] === 'platform';
      const hasHTTPPort = item.spec?.ports?.some((port) => port.port === 80);
      const generatedNameMatches = String(item.metadata?.name).includes('platform')
        && String(item.metadata?.name) !== 'envoy-gateway';
      return hasHTTPPort && (ownsGateway || generatedNameMatches);
    }) ?? false;
  }, 'Envoy data-plane Service was not created');

  portForward = spawn('kubectl', ['-n', service.metadata.namespace, 'port-forward', `service/${service.metadata.name}`, '18081:80'], {
    cwd: root,
    env: process.env,
    shell: false,
    windowsHide: true,
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let portForwardError = '';
  portForward.stderr.on('data', (chunk) => { portForwardError += String(chunk); });
  await waitFor(async () => {
    if (portForward.exitCode !== null) throw new Error(portForwardError || 'port-forward exited');
    try {
      const response = await fetchJson('/api/v1/status');
      return response.status > 0;
    } catch {
      return false;
    }
  }, 'Envoy port-forward did not become ready');

  const initial = await routeOwner('go-edge');
  const unregistered = await fetchJson('/legacy');
  assert(unregistered.status === 404, `unregistered Legacy path returned ${unregistered.status}`);

  await run('kubectl', ['-n', namespace, 'patch', 'httproute', 'platform-status', '--type=json', '-p', JSON.stringify([
    { op: 'replace', path: '/metadata/annotations/poc.hvac~1route-revision', value: 'legacy-canary-v2' },
    { op: 'replace', path: '/spec/rules/0/backendRefs/0/name', value: 'legacy-private' },
  ])]);
  const canary = await routeOwner('legacy-private');

  await run('kubectl', ['-n', namespace, 'patch', 'httproute', 'platform-status', '--type=json', '-p', JSON.stringify([
    { op: 'replace', path: '/metadata/annotations/poc.hvac~1route-revision', value: 'go-primary-v3' },
    { op: 'replace', path: '/spec/rules/0/backendRefs/0/name', value: 'go-edge' },
  ])]);
  const rolledBack = await routeOwner('go-edge');

  await writeReport({
    schemaVersion: 1,
    component: 'envoy-gateway',
    status: 'passed',
    startedAt,
    finishedAt: new Date().toISOString(),
    version: component.version,
    installerSha256: digest,
    clusterName,
    dataPlaneService: `${service.metadata.namespace}/${service.metadata.name}`,
    initial,
    unregistered,
    canary,
    rolledBack,
    businessIdentityHeadersAdded: false,
    conclusion: 'Suitable only as the Kubernetes traffic layer below the Go platform-gateway.',
  });
  console.log(`Envoy Gateway POC passed: ${reportPath}`);
} catch (error) {
  await writeReport({
    schemaVersion: 1,
    component: 'envoy-gateway',
    status: 'failed',
    startedAt,
    finishedAt: new Date().toISOString(),
    clusterName,
    error: error instanceof Error ? error.message : String(error),
  });
  throw error;
} finally {
  if (portForward) portForward.kill('SIGTERM');
  if (cleanup && clusterCreated) {
    try { await run('kind', ['delete', 'cluster', '--name', clusterName]); } catch (error) { console.error(error.message); }
  }
}
