import { readFile, readdir } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const assert = (condition, message) => {
  if (!condition) throw new Error(message);
};
const includesAll = (text, markers, label) => {
  for (const marker of markers) assert(text.includes(marker), `${label} is missing ${marker}`);
};

const packageJSON = JSON.parse(await read('package.json'));
for (const script of ['delivery:local', 'delivery:validate', 'delivery:check', 'delivery:render', 'security:dependency-audit', 'security:go-vuln', 'security:licenses', 'audit:s0-rollout', 'audit:delivery']) {
  assert(packageJSON.scripts?.[script], `package.json is missing ${script}`);
}
for (const script of ['test:legacy-compatibility', 'build:legacy-compatibility']) {
  assert(packageJSON.scripts?.[script], `package.json is missing ${script}`);
}

const localConfig = await read('deploy/s0/local.env.example');
const stagingConfig = await read('deploy/s0/staging.env.example');
for (const [label, config] of [['local', localConfig], ['staging', stagingConfig]]) {
  includesAll(config, ['S0_CONFIG_REVISION=', 'S0_TRUST_DOMAIN=', 'OIDC_ISSUER=', 'IAM_AUDIENCE=', 'AUDIT_AUDIENCE=', 'LEGACY_AUDIENCE=', 'ALLOW_PRODUCTION_EGRESS=false', 'THINGSBOARD_BASE_URL=', 'WEBHOOK_BASE_URL='], `${label} config`);
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
  'services/oidc-test-provider/cmd/oidc-test-provider/main.go',
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
const compatibilitySQL = await read('infra/s0-durable/postgres/compatibility/previous-writer.sql');
includesAll(compatibilitySQL, ['SET LOCAL ROLE gateway_runtime', 'SET LOCAL ROLE audit_consumer_runtime', 'omitting', 'ROLLBACK;'], 'previous writer compatibility test');

const serviceAccounts = await read('deploy/s0/staging/serviceaccounts.yaml');
for (const name of ['platform-gateway', 'iam-service', 'audit-ledger-service', 'outbox-relay', 'oidc-test-provider', 'legacy-private', 's0-migrator']) {
  assert(serviceAccounts.includes(`name: ${name}`), `missing ServiceAccount ${name}`);
}
assert((serviceAccounts.match(/automountServiceAccountToken: false/g) || []).length === 7, 'all ServiceAccounts must disable token automount');

const workloadDirectory = resolve(root, 'deploy/s0/staging/workloads');
const workloadFiles = (await readdir(workloadDirectory)).filter((name) => name.endsWith('.yaml')).sort();
assert(workloadFiles.length === 6, 'six S0 workload templates are required');
for (const file of workloadFiles) {
  const text = await read(`deploy/s0/staging/workloads/${file}`);
  includesAll(text, ['digest-required', 'render-before-apply', 'serviceAccountName:', 'terminationGracePeriodSeconds:', 'startupProbe:', 'livenessProbe:', 'readinessProbe:', 'runAsNonRoot: true', 'allowPrivilegeEscalation: false', 'readOnlyRootFilesystem: true', 'drop: ["ALL"]', 'requests:', 'limits:', '[SIGNED_IMAGE_'], file);
  if (file !== 'outbox-relay.yaml' && file !== 'oidc-test-provider.yaml') {
    assert(text.includes('maxUnavailable: 0'), `${file} rolling update must preserve availability`);
  }
}
const legacyWorkload = await read('deploy/s0/staging/workloads/legacy-private.yaml');
includesAll(legacyWorkload, ['LEGACY_FIXTURE_ADDR', '0.0.0.0:8445', 'runAsUser: 65532'], 'Legacy compatibility fixture workload');

const namespace = await read('deploy/s0/staging/namespace.yaml');
includesAll(namespace, ['pod-security.kubernetes.io/enforce: restricted', 'pod-security.kubernetes.io/audit: restricted'], 'staging namespace');
const networkPolicies = await read('deploy/s0/staging/networkpolicies.yaml');
includesAll(networkPolicies, ['default-deny-all', 'policyTypes: [Ingress, Egress]', 'gateway-only-private-services', 'legacy-private', 'platform-gateway', 'redpanda', 'postgres', 'otel-collector'], 'staging NetworkPolicy');
const budgets = await read('deploy/s0/staging/disruption-budgets.yaml');
assert((budgets.match(/minAvailable: 1/g) || []).length >= 4, 'availability-critical workloads require disruption budgets');
const migrationJob = await read('deploy/s0/staging/migration-job.yaml');
includesAll(migrationJob, ['serviceAccountName: s0-migrator', '[SIGNED_IMAGE_S0_MIGRATOR]', 'backoffLimit: 0', 'readOnlyRootFilesystem: true'], 'migration Job');
const renderer = await read('scripts/render-s0-staging.mjs');
includesAll(renderer, ['@sha256:', 'Missing staging binding', 'Unresolved placeholder', 'without logging binding values'], 'staging renderer');

const workflow = await read('.github/workflows/s0-supply-chain.yml');
includesAll(workflow, ['go-version: "1.25.12"', 'gitleaks/gitleaks-action', 'github/codeql-action/init', 'build_mode: manual', 'build_mode: none', 'upload: never', 'security:go-vuln', 'npm audit', 'security:dependency-audit', 'security:licenses', 'SERVICE_PACKAGE=./tools/legacy-private-fixture/cmd/legacy-private-fixture', 'sbom: true', 'provenance: mode=max', 'aquasecurity/trivy-action@ed142fd0673e97e23eac54620cfb913e5ce36c25', 'scanners: secret', 'trivy-secrets-${{ matrix.name }}.json', 'cosign sign', 'cosign verify', 'attest-build-provenance', "if: github.event.repository.visibility == 'public'", 'Record GitHub attestation skip', 'id-token: write'], 'supply-chain workflow');
assert(!workflow.includes('hvac-backend'), 'supply-chain workflow must not depend on the local migration reference');
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
