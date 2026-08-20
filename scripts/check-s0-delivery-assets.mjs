import { readFile, readdir } from 'node:fs/promises';
import { dirname, relative, resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const assert = (condition, message) => {
  if (!condition) throw new Error(message);
};
const includesAll = (text, markers, label) => {
  for (const marker of markers) assert(text.includes(marker), `${label} is missing ${marker}`);
};

async function collectSupplyChainScriptClosure(workflow, packageJSON) {
  const permissionsIndex = workflow.indexOf('\npermissions:');
  assert(permissionsIndex >= 0, 'supply-chain workflow is missing permissions boundary');
  const workflowBody = workflow.slice(permissionsIndex);
  const npmQueue = [...workflowBody.matchAll(/\bnpm run ([\w:-]+)/g)].map((match) => match[1]);
  const npmSeen = new Set();
  const scriptFiles = new Set([...workflowBody.matchAll(/scripts\/[\w./-]+\.(?:mjs|cjs|js)/g)].map((match) => match[0]));

  while (npmQueue.length > 0) {
    const name = npmQueue.shift();
    if (npmSeen.has(name)) continue;
    npmSeen.add(name);
    const command = packageJSON.scripts?.[name];
    assert(typeof command === 'string', `supply-chain workflow references missing npm script ${name}`);
    for (const match of command.matchAll(/\bnpm run ([\w:-]+)/g)) npmQueue.push(match[1]);
    for (const match of command.matchAll(/scripts\/[\w./-]+\.(?:mjs|cjs|js)/g)) scriptFiles.add(match[0]);
  }

  const fileQueue = [...scriptFiles];
  const fileSeen = new Set();
  while (fileQueue.length > 0) {
    const path = fileQueue.shift();
    if (fileSeen.has(path)) continue;
    fileSeen.add(path);
    const source = await read(path);
    const references = new Set([...source.matchAll(/scripts\/[\w./-]+\.(?:mjs|cjs|js)/g)].map((match) => match[0]));
    for (const match of source.matchAll(/['\"](\.\/[^'\"]+\.(?:mjs|cjs|js))['\"]/g)) {
      references.add(relative(root, resolve(root, dirname(path), match[1])).replaceAll('\\', '/'));
    }
    for (const reference of references) {
      if (!reference.startsWith('scripts/') || scriptFiles.has(reference)) continue;
      scriptFiles.add(reference);
      fileQueue.push(reference);
    }
  }

  return scriptFiles;
}

const packageJSON = JSON.parse(await read('package.json'));
for (const script of ['delivery:local', 'delivery:validate', 'delivery:check', 'delivery:render', 'security:dependency-audit', 'security:go-vuln', 'security:licenses', 'audit:s0-rollout', 'audit:delivery']) {
  assert(packageJSON.scripts?.[script], `package.json is missing ${script}`);
}
assert(!packageJSON.scripts?.['test:legacy-compatibility'], 'retired Legacy compatibility test must not be exposed as an active root script');
assert(!packageJSON.scripts?.['build:legacy-compatibility'], 'retired Legacy compatibility build must not be exposed as an active root script');

const localConfig = await read('deploy/s0/local.env.example');
const stagingConfig = await read('deploy/s0/staging.env.example');
for (const [label, config] of [['local', localConfig], ['staging', stagingConfig]]) {
  includesAll(config, ['S0_CONFIG_REVISION=', 'S0_TRUST_DOMAIN=', 'OIDC_ISSUER=', 'IAM_AUDIENCE=', 'AUDIT_AUDIENCE=', 'ALLOW_PRODUCTION_EGRESS=false', 'THINGSBOARD_BASE_URL=', 'WEBHOOK_BASE_URL='], `${label} config`);
  assert(!config.includes('LEGACY_'), `${label} config still exposes retired Legacy settings`);
  assert(!/ALLOW_PRODUCTION_EGRESS=(?!false)/.test(config), `${label} config enables production egress`);
}

const devTopology = await read('scripts/dev-s0-durable.mjs');
includesAll(devTopology, ['validate-s0-delivery-config.mjs', 'startS0DurableTopology', 'captureTelemetry: false', "process.once('SIGTERM'", 'OpenTelemetry Collector'], 'local delivery command');

const durableCompose = await read('infra/s0-durable/compose.yaml');
includesAll(durableCompose, ['S0_POSTGRES_HOST_PORT', 'S0_REDPANDA_HOST_PORT', 'S0_OTEL_GRPC_HOST_PORT', 'S0_PROMETHEUS_HOST_PORT'], 'durable Compose dynamic ports');
const durableTopology = await read('scripts/s0-durable-topology.mjs');
includesAll(durableTopology, ['findAvailablePort', 'composeOptions', 'postgresHostPort', 'redpandaHostPort'], 'durable topology port isolation');

const runtime = await read('libs/observability/runtime.go');
includesAll(runtime, ['/health/startup', '/health/live', '/health/ready', 'StatusServiceUnavailable', 'MarkReady', 'MarkNotReady'], 'observability probes');
const serviceMains = [
  'services/platform-gateway/cmd/platform-gateway/main.go',
  'services/iam-service/cmd/iam-service/main.go',
  'services/audit-ledger-service/cmd/audit-ledger-service/main.go',
  'services/outbox-relay/cmd/outbox-relay/main.go',
];
for (const path of serviceMains) {
  const text = await read(path);
  includesAll(text, ['MarkReady()', 'MarkNotReady()', 'SIGTERM'], path);
  assert(!text.includes('001-s0-durable.sql'), `${path} must not execute schema migration at startup`);
}

const goImage = await read('deploy/s0/images/go-service.Dockerfile');
includesAll(goImage, ['golang:1.25.12-bookworm', 'CGO_ENABLED=0', '-trimpath', 'distroless/static-debian12:nonroot', 'USER 65532:65532'], 'Go image');
const migratorImage = await read('deploy/s0/images/migrator.Dockerfile');
includesAll(migratorImage, ['001-s0-durable.sql', 'USER postgres', 'ON_ERROR_STOP=1'], 'migration image');
const dockerIgnore = await read('.dockerignore');
includesAll(dockerIgnore, ['.git', '**/node_modules', '**/.venv', '**/.env', 'agents', 'hvac-backend', 'dist', 'out'], 'Docker build context exclusions');

const bootstrapSQL = await read('infra/s0-durable/postgres/init/000-bootstrap-identities.sql');
includesAll(bootstrapSQL, ['CREATE ROLE s0_migrator', 'gateway_runtime', 'gateway_relay_runtime', 'audit_consumer_runtime', 'audit_query_runtime', 'AUTHORIZATION s0_migrator'], 'database identity bootstrap');
const migrationSQL = await read('infra/s0-durable/postgres/init/001-s0-durable.sql');
includesAll(migrationSQL, ['SET LOCAL ROLE s0_migrator', "traceparent text NOT NULL DEFAULT ''"], 'database migration');
assert((migrationSQL.match(/traceparent text NOT NULL DEFAULT ''/g) || []).length === 3, 'all rollback-window traceparent columns require a default');
const serviceAccounts = await read('deploy/s0/staging/serviceaccounts.yaml');
for (const name of ['platform-gateway', 'iam-service', 'audit-ledger-service', 'outbox-relay', 's0-migrator']) {
  assert(serviceAccounts.includes(`name: ${name}`), `missing ServiceAccount ${name}`);
}
assert((serviceAccounts.match(/automountServiceAccountToken: false/g) || []).length === 5, 'all ServiceAccounts must disable token automount');
assert(!serviceAccounts.includes('oidc-test-provider'), 'test OIDC provider must not have a staging ServiceAccount');

const workloadDirectory = resolve(root, 'deploy/s0/staging/workloads');
const workloadFiles = (await readdir(workloadDirectory)).filter((name) => name.endsWith('.yaml')).sort();
assert(workloadFiles.length === 4, 'four production-shaped S0 workload templates are required');
for (const file of workloadFiles) {
  const text = await read(`deploy/s0/staging/workloads/${file}`);
  includesAll(text, ['digest-required', 'render-before-apply', 'serviceAccountName:', 'terminationGracePeriodSeconds:', 'startupProbe:', 'livenessProbe:', 'readinessProbe:', 'runAsNonRoot: true', 'allowPrivilegeEscalation: false', 'readOnlyRootFilesystem: true', 'drop: ["ALL"]', 'requests:', 'limits:', '[SIGNED_IMAGE_'], file);
  if (file !== 'outbox-relay.yaml') {
    assert(text.includes('maxUnavailable: 0'), `${file} rolling update must preserve availability`);
  }
}
assert(!workloadFiles.includes('legacy-private.yaml'), 'retired Legacy workload must not be present in active staging');
assert(!workloadFiles.includes('oidc-test-provider.yaml'), 'test OIDC provider must not be present in active staging');

const namespace = await read('deploy/s0/staging/namespace.yaml');
includesAll(namespace, ['pod-security.kubernetes.io/enforce: restricted', 'pod-security.kubernetes.io/audit: restricted'], 'staging namespace');
const networkPolicies = await read('deploy/s0/staging/networkpolicies.yaml');
includesAll(networkPolicies, ['default-deny-all', 'policyTypes: [Ingress, Egress]', 'gateway-only-private-services', 'platform-gateway', 'redpanda', 'postgres', 'otel-collector'], 'staging NetworkPolicy');
assert(!networkPolicies.includes('legacy-private'), 'retired Legacy workload must not appear in active NetworkPolicy');
assert(!networkPolicies.includes('oidc-test-provider'), 'test OIDC provider must not appear in active staging NetworkPolicy');
const budgets = await read('deploy/s0/staging/disruption-budgets.yaml');
assert((budgets.match(/minAvailable: 1/g) || []).length >= 3, 'availability-critical workloads require disruption budgets');
const migrationJob = await read('deploy/s0/staging/migration-job.yaml');
includesAll(migrationJob, ['serviceAccountName: s0-migrator', '[SIGNED_IMAGE_S0_MIGRATOR]', 'backoffLimit: 0', 'readOnlyRootFilesystem: true'], 'migration Job');
const renderer = await read('scripts/render-s0-staging.mjs');
includesAll(renderer, ['@sha256:', 'Missing staging binding', 'Unresolved placeholder', 'without logging binding values'], 'staging renderer');

const workflow = await read('.github/workflows/s0-supply-chain.yml');
includesAll(workflow, ['go-version: "1.25.12"', 'gitleaks/gitleaks-action', 'github/codeql-action/init', 'build_mode: manual', 'build_mode: none', 'upload: never', 'security:go-vuln', 'npm audit', 'security:dependency-audit', 'security:licenses', 'sbom: true', 'provenance: mode=max', 'aquasecurity/trivy-action@ed142fd0673e97e23eac54620cfb913e5ce36c25', 'scanners: secret', 'trivy-secrets-${{ matrix.name }}.json', 'cosign sign', 'cosign verify', 'attest-build-provenance', "if: github.event.repository.visibility == 'public'", 'Record GitHub attestation skip', 'id-token: write'], 'supply-chain workflow');
assert(!workflow.includes('release-evidence-pr:'), 'S0 release evidence must not create a Kind cluster on pull requests');
assert(!workflow.includes('s0-release-evidence-pr'), 'S0 release evidence must not publish PR certification artifacts');
includesAll(workflow, ["if: startsWith(github.ref, 'refs/tags/s0-v') || github.event_name == 'workflow_dispatch'", 'Create disposable Kubernetes evidence cluster', 'npm run audit:s0-kind-rollout'], 'formal S0 release certification');
const triggerBlock = workflow.slice(0, workflow.indexOf('\npermissions:'));
assert(!triggerBlock.includes('"scripts/**"'), 'supply-chain pull-request paths must not watch every repository script');
const expectedTriggerScripts = await collectSupplyChainScriptClosure(workflow, packageJSON);
const configuredTriggerScripts = new Set([...triggerBlock.matchAll(/-\s+"(scripts\/[^"]+)"/g)].map((match) => match[1]));
const missingTriggerScripts = [...expectedTriggerScripts].filter((path) => !configuredTriggerScripts.has(path)).sort();
const extraTriggerScripts = [...configuredTriggerScripts].filter((path) => !expectedTriggerScripts.has(path)).sort();
assert(missingTriggerScripts.length === 0, `supply-chain pull-request paths are missing script dependencies: ${missingTriggerScripts.join(', ')}`);
assert(extraTriggerScripts.length === 0, `supply-chain pull-request paths contain unrelated scripts: ${extraTriggerScripts.join(', ')}`);
assert(!workflow.includes('hvac-backend'), 'supply-chain workflow must not depend on the local migration reference');
assert(!workflow.includes('legacy-private'), 'supply-chain workflow must not build the retired Legacy fixture');
const licenseGate = await read('scripts/check-production-licenses.mjs');
assert(!licenseGate.includes('hvac-backend'), 'license gate must not scan the local migration reference');
const dependencyBaseline = JSON.parse(await read('deploy/s0/security/dependency-audit-baseline.json'));
assert(dependencyBaseline.primaryOwner === 'security-platform', 'dependency audit baseline requires a Security Platform owner');
assert(dependencyBaseline.remediationIssue === '07-security-and-failure-gates', 'dependency audit baseline must be assigned to ticket 07');
assert(new Date(`${dependencyBaseline.expiresOn}T23:59:59Z`).getTime() > Date.now(), 'dependency audit baseline is expired');
for (const [name, project] of Object.entries(dependencyBaseline.projects || {})) {
  assert(Number(project.allowed?.critical || 0) === 0, `${name} baseline permits production critical vulnerabilities`);
}
const dependencyAudit = await read('scripts/check-npm-production-audit.mjs');
includesAll(dependencyAudit, ['--omit=dev', 'production critical vulnerabilities', 'expiresOn', 'vulnerabilities increased'], 'production dependency audit gate');

const rollout = await read('scripts/audit-s0-rollout.mjs');
includesAll(rollout, ['rolling policy allowed zero ready replicas', 'rollback-surge-previous', 'rollback-complete'], 'rolling update audit');
const docs = await read('docs/operations/s0-delivery.md');
for (const heading of ['## Ownership', '## Local delivery', '## Configuration contract', '## Probes and graceful shutdown', '## Staging security boundary', '## Signed supply chain', '## Rolling update and rollback', '## Expand-contract compatibility', '## Recovery']) {
  assert(docs.includes(heading), `delivery documentation is missing ${heading}`);
}

console.log('S0 delivery, staging and signed supply-chain assets are internally consistent.');
