import { spawn } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const argument = (name) => process.argv.find((value) => value.startsWith(`--${name}=`))?.slice(name.length + 3);
const reportPath = resolve(root, argument('report') ?? 'out/s0-release-evidence/kind-rollout-report.json');
const namespace = argument('namespace') ?? `s0-rollout-evidence-${process.pid}`;
const cleanup = argument('cleanup') !== 'false';
const image = argument('image') ?? 'registry.k8s.io/pause:3.10';
const deploymentName = 's0-rollout-proof';
const startedAt = new Date();
const observations = [];
let sourcePolicy = null;
let cluster = null;
let history = { afterUpdate: null, afterRollback: null };

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function pause(milliseconds) {
  return new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
}

async function run(command, args, options = {}) {
  const child = spawn(command, args, {
    cwd: root,
    env: process.env,
    stdio: ['pipe', 'pipe', 'pipe'],
    shell: false,
    windowsHide: true,
  });
  if (options.input !== undefined) child.stdin.end(options.input);
  else child.stdin.end();
  let stdout = '';
  let stderr = '';
  child.stdout.on('data', (chunk) => { stdout += String(chunk); });
  child.stderr.on('data', (chunk) => { stderr += String(chunk); });
  const result = await new Promise((resolveResult) => {
    child.once('error', (error) => resolveResult({ code: null, signal: null, error }));
    child.once('exit', (code, signal) => resolveResult({ code, signal, error: null }));
  });
  if (result.error || result.code !== 0 || result.signal) {
    const detail = stderr.trim() || stdout.trim() || result.error?.message || `exit ${result.code ?? result.signal}`;
    throw new Error(`${command} ${args.join(' ')} failed: ${detail}`);
  }
  return { stdout, stderr };
}

async function kubectl(args, options) {
  return run('kubectl', args, options);
}

function parseSourcePolicy(text) {
  const replicas = Number(text.match(/^\s*replicas:\s*(\d+)/m)?.[1]);
  const strategyType = text.match(/^\s*type:\s*(RollingUpdate|Recreate)/m)?.[1] ?? null;
  const maxUnavailable = Number(text.match(/maxUnavailable:\s*(\d+)/)?.[1]);
  const maxSurge = Number(text.match(/maxSurge:\s*(\d+)/)?.[1]);
  const terminationGracePeriodSeconds = Number(text.match(/terminationGracePeriodSeconds:\s*(\d+)/)?.[1]);
  assert(Number.isInteger(replicas), 'platform-gateway Deployment replicas could not be parsed');
  assert(strategyType, 'platform-gateway Deployment strategy type could not be parsed');
  assert(Number.isInteger(maxUnavailable), 'platform-gateway maxUnavailable could not be parsed');
  assert(Number.isInteger(maxSurge), 'platform-gateway maxSurge could not be parsed');
  assert(Number.isInteger(terminationGracePeriodSeconds), 'platform-gateway termination grace period could not be parsed');
  return { replicas, strategyType, maxUnavailable, maxSurge, terminationGracePeriodSeconds };
}

function namespaceManifest() {
  return {
    apiVersion: 'v1',
    kind: 'Namespace',
    metadata: {
      name: namespace,
      labels: {
        'pod-security.kubernetes.io/enforce': 'restricted',
        'pod-security.kubernetes.io/audit': 'restricted',
        'pod-security.kubernetes.io/warn': 'restricted',
      },
    },
  };
}

function workloadManifest() {
  return {
    apiVersion: 'apps/v1',
    kind: 'Deployment',
    metadata: {
      name: deploymentName,
      namespace,
      labels: { app: deploymentName },
      annotations: {
        's0.hvac/evidence-only': 'true',
        's0.hvac/source-workload': 'deploy/s0/staging/workloads/platform-gateway.yaml',
      },
    },
    spec: {
      replicas: sourcePolicy.replicas,
      revisionHistoryLimit: 4,
      strategy: {
        type: sourcePolicy.strategyType,
        rollingUpdate: {
          maxUnavailable: sourcePolicy.maxUnavailable,
          maxSurge: sourcePolicy.maxSurge,
        },
      },
      selector: { matchLabels: { app: deploymentName } },
      template: {
        metadata: {
          labels: { app: deploymentName },
          annotations: { 's0.hvac/release-revision': 'previous-compatible' },
        },
        spec: {
          automountServiceAccountToken: false,
          terminationGracePeriodSeconds: sourcePolicy.terminationGracePeriodSeconds,
          securityContext: {
            runAsNonRoot: true,
            seccompProfile: { type: 'RuntimeDefault' },
          },
          containers: [
            {
              name: 'workload',
              image,
              imagePullPolicy: 'IfNotPresent',
              env: [{ name: 'RELEASE_REVISION', value: 'previous-compatible' }],
              securityContext: {
                allowPrivilegeEscalation: false,
                readOnlyRootFilesystem: true,
                runAsNonRoot: true,
                runAsUser: 65532,
                capabilities: { drop: ['ALL'] },
              },
              resources: {
                requests: { cpu: '5m', memory: '8Mi' },
                limits: { cpu: '50m', memory: '32Mi' },
              },
            },
          ],
        },
      },
    },
  };
}

function disruptionBudgetManifest() {
  return {
    apiVersion: 'policy/v1',
    kind: 'PodDisruptionBudget',
    metadata: { name: deploymentName, namespace },
    spec: {
      minAvailable: 1,
      selector: { matchLabels: { app: deploymentName } },
    },
  };
}

function revisionFromDeployment(deployment) {
  const annotation = deployment.spec?.template?.metadata?.annotations?.['s0.hvac/release-revision'];
  const env = deployment.spec?.template?.spec?.containers?.find((container) => container.name === 'workload')?.env ?? [];
  const envRevision = env.find((entry) => entry.name === 'RELEASE_REVISION')?.value;
  assert(annotation === envRevision, `template annotation revision ${annotation} did not match env revision ${envRevision}`);
  return annotation;
}

async function snapshot(phase) {
  const deployment = JSON.parse((await kubectl(['-n', namespace, 'get', 'deployment', deploymentName, '-o', 'json'])).stdout);
  const replicaSets = JSON.parse((await kubectl(['-n', namespace, 'get', 'replicaset', '-l', `app=${deploymentName}`, '-o', 'json'])).stdout);
  const available = deployment.status?.availableReplicas ?? 0;
  const ready = deployment.status?.readyReplicas ?? 0;
  const updated = deployment.status?.updatedReplicas ?? 0;
  const desired = deployment.spec?.replicas ?? 0;
  const totalReplicaSetReplicas = replicaSets.items.reduce((sum, item) => sum + (item.status?.replicas ?? 0), 0);
  const observation = {
    at: new Date().toISOString(),
    phase,
    generation: deployment.metadata?.generation ?? null,
    observedGeneration: deployment.status?.observedGeneration ?? null,
    deploymentRevision: deployment.metadata?.annotations?.['deployment.kubernetes.io/revision'] ?? null,
    releaseRevision: revisionFromDeployment(deployment),
    desired,
    available,
    ready,
    updated,
    unavailable: deployment.status?.unavailableReplicas ?? 0,
    totalReplicaSetReplicas,
    replicaSets: replicaSets.items.map((item) => ({
      name: item.metadata?.name,
      revision: item.metadata?.annotations?.['deployment.kubernetes.io/revision'] ?? null,
      desired: item.spec?.replicas ?? 0,
      replicas: item.status?.replicas ?? 0,
      ready: item.status?.readyReplicas ?? 0,
      available: item.status?.availableReplicas ?? 0,
    })).sort((left, right) => String(left.name).localeCompare(String(right.name))),
  };
  return observation;
}

function appendObservation(observation) {
  const previous = observations.at(-1);
  const comparable = ({ at: _at, ...rest }) => rest;
  if (!previous || JSON.stringify(comparable(previous)) !== JSON.stringify(comparable(observation))) {
    observations.push(observation);
  }
}

async function observeRollout(phase, expectedRevision) {
  const deadline = Date.now() + 180_000;
  while (Date.now() < deadline) {
    const observation = await snapshot(phase);
    appendObservation(observation);
    assert(observation.available >= 1, `${phase}: available replicas fell below one`);
    assert(
      observation.totalReplicaSetReplicas <= sourcePolicy.replicas + sourcePolicy.maxSurge,
      `${phase}: ReplicaSet replicas exceeded maxSurge policy`,
    );
    const complete = observation.releaseRevision === expectedRevision
      && observation.observedGeneration >= observation.generation
      && observation.updated === sourcePolicy.replicas
      && observation.ready === sourcePolicy.replicas
      && observation.available === sourcePolicy.replicas;
    if (complete) return observation;
    await pause(250);
  }
  throw new Error(`${phase}: rollout did not converge to ${expectedRevision} within 180 seconds`);
}

async function writeReport(status, error = null) {
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify({
    schemaVersion: 1,
    ticket: '08-s0-release-evidence',
    type: 'kind-kubernetes-rollout',
    status,
    startedAt: startedAt.toISOString(),
    finishedAt: new Date().toISOString(),
    namespace,
    cleanup,
    image,
    sourcePolicy,
    cluster,
    observations,
    history,
    restoredVersion: observations.at(-1)?.releaseRevision ?? null,
    error,
  }, null, 2)}\n`);
}

try {
  const sourceText = await readFile(resolve(root, 'deploy/s0/staging/workloads/platform-gateway.yaml'), 'utf8');
  sourcePolicy = parseSourcePolicy(sourceText);
  assert(sourcePolicy.replicas === 2, 'platform-gateway staging replicas must remain 2');
  assert(sourcePolicy.strategyType === 'RollingUpdate', 'platform-gateway must use RollingUpdate');
  assert(sourcePolicy.maxUnavailable === 0, 'platform-gateway maxUnavailable must remain 0');
  assert(sourcePolicy.maxSurge === 1, 'platform-gateway maxSurge must remain 1');

  cluster = {
    context: (await kubectl(['config', 'current-context'])).stdout.trim(),
    kubectlVersion: JSON.parse((await kubectl(['version', '--client', '-o', 'json'])).stdout),
  };

  await kubectl(['apply', '-f', '-'], { input: `${JSON.stringify(namespaceManifest())}\n` });
  await kubectl(['apply', '-f', '-'], { input: `${JSON.stringify(workloadManifest())}\n` });
  await kubectl(['apply', '-f', '-'], { input: `${JSON.stringify(disruptionBudgetManifest())}\n` });
  await kubectl(['-n', namespace, 'rollout', 'status', `deployment/${deploymentName}`, '--timeout=180s']);

  const initial = await snapshot('initial');
  appendObservation(initial);
  assert(initial.releaseRevision === 'previous-compatible', 'initial release revision was not previous-compatible');
  assert(initial.available === 2, 'initial Deployment did not reach two available replicas');

  const currentPatch = {
    spec: {
      template: {
        metadata: { annotations: { 's0.hvac/release-revision': 'current' } },
        spec: {
          containers: [{
            name: 'workload',
            env: [{ name: 'RELEASE_REVISION', value: 'current' }],
          }],
        },
      },
    },
  };
  await kubectl(['-n', namespace, 'patch', 'deployment', deploymentName, '--type=strategic', '-p', JSON.stringify(currentPatch)]);
  await observeRollout('rolling-update', 'current');
  history.afterUpdate = (await kubectl(['-n', namespace, 'rollout', 'history', `deployment/${deploymentName}`])).stdout.trim();

  await kubectl(['-n', namespace, 'rollout', 'undo', `deployment/${deploymentName}`]);
  const restored = await observeRollout('rollback', 'previous-compatible');
  history.afterRollback = (await kubectl(['-n', namespace, 'rollout', 'history', `deployment/${deploymentName}`])).stdout.trim();
  assert(restored.available === 2, 'rollback did not restore two available replicas');
  assert(restored.releaseRevision === 'previous-compatible', 'rollback did not restore previous-compatible revision');
  assert(observations.every((observation) => observation.available >= 1), 'an observed rollout phase had zero available replicas');

  await writeReport('passed');
  console.log(`S0 Kind rolling update and rollback passed; report: ${reportPath}`);
} catch (error) {
  await writeReport('failed', error instanceof Error ? error.message : String(error));
  throw error;
} finally {
  if (cleanup) {
    try {
      await kubectl(['delete', 'namespace', namespace, '--ignore-not-found=true', '--wait=false']);
    } catch (cleanupError) {
      console.error(`Failed to schedule cleanup for namespace ${namespace}: ${cleanupError instanceof Error ? cleanupError.message : String(cleanupError)}`);
    }
  }
}
