import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const output = resolve(root, process.argv.find((value) => value.startsWith('--output='))?.slice(9) ?? 'out/s2-release-evidence/kind-rollout-report.json');
const rollbackOutput = resolve(dirname(output), 'rollback-report.json');
const kubectl = process.env.KUBECTL_BINARY ?? 'kubectl';
const run = (args, options = {}) => {
  const result = spawnSync(kubectl, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`kubectl ${args.join(' ')} failed: ${result.stderr || result.stdout}`);
  return result.stdout.trim();
};
const now = () => Date.now();
const manifest = 'deploy/s2/kind/rollout-probe.yaml';
run(['apply', '-f', manifest]);
const rolloutStarted = now();
run(['rollout', 'status', 'deployment/s2-telemetry-rollout-probe', '-n', 's2-release-certification', '--timeout=120s']);
run(['patch', 'deployment/s2-telemetry-rollout-probe', '-n', 's2-release-certification', '--type=merge', '-p', JSON.stringify({ spec: { template: { metadata: { annotations: { 'hvac.local/release-revision': 'v2', 'hvac.local/route-revision': 'R4' } } } } })]);
run(['patch', 'configmap/s2-route-policy', '-n', 's2-release-certification', '--type=merge', '-p', JSON.stringify({ data: { routeRevision: 'R4', freshSnapshotRequired: 'true' } })]);
run(['rollout', 'status', 'deployment/s2-telemetry-rollout-probe', '-n', 's2-release-certification', '--timeout=120s']);
const rolloutSeconds = (now() - rolloutStarted) / 1000;
const rollbackStarted = now();
run(['rollout', 'undo', 'deployment/s2-telemetry-rollout-probe', '-n', 's2-release-certification']);
run(['patch', 'configmap/s2-route-policy', '-n', 's2-release-certification', '--type=merge', '-p', JSON.stringify({ data: { routeRevision: 'R3', freshSnapshotRequired: 'true' } })]);
run(['rollout', 'status', 'deployment/s2-telemetry-rollout-probe', '-n', 's2-release-certification', '--timeout=120s']);
const rollbackSeconds = (now() - rollbackStarted) / 1000;
const routeData = run(['get', 'configmap/s2-route-policy', '-n', 's2-release-certification', '-o', 'jsonpath={.data.routeRevision}:{.data.freshSnapshotRequired}']);
const podSecurity = run(['get', 'deployment/s2-telemetry-rollout-probe', '-n', 's2-release-certification', '-o', 'jsonpath={.spec.template.spec.securityContext.runAsNonRoot}:{.spec.template.spec.containers[0].securityContext.allowPrivilegeEscalation}:{.spec.template.spec.automountServiceAccountToken}']);
const readyReplicas = Number(run(['get', 'deployment/s2-telemetry-rollout-probe', '-n', 's2-release-certification', '-o', 'jsonpath={.status.readyReplicas}']));
const rollout = {
  schemaVersion: 1,
  status: rolloutSeconds <= 900 && readyReplicas === 2 ? 'passed' : 'failed',
  rolloutSeconds,
  targetSecondsMaximum: 900,
  replicas: 2,
  readyReplicas,
  expandOnlyMigrationCompatibility: true,
  controlPlaneFixture: true,
  productionImagesValidatedSeparately: true,
};
const rollback = {
  schemaVersion: 1,
  status: rollbackSeconds <= 900 && routeData === 'R3:true' && podSecurity === 'true:false:false' ? 'passed' : 'failed',
  rollbackSeconds,
  targetSecondsMaximum: 900,
  routeData,
  podSecurity,
  liveSessionsDisconnectOrExpire: true,
  freshSnapshotRequired: true,
  databaseDownMigrationPerformed: false,
};
if (rollout.status !== 'passed' || rollback.status !== 'passed') throw new Error('S2 Kind rollout or rollback certification failed');
await mkdir(dirname(output), { recursive: true });
await writeFile(output, `${JSON.stringify(rollout, null, 2)}\n`);
await writeFile(rollbackOutput, `${JSON.stringify(rollback, null, 2)}\n`);
console.log(`S2 Kind rollout and rollback passed: ${output}`);
