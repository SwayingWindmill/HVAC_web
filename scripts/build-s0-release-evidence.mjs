import { createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { basename, relative, resolve } from 'node:path';

const root = resolve(process.cwd());
const argument = (name) => process.argv.find((value) => value.startsWith(`--${name}=`))?.slice(name.length + 3);
const outputRoot = resolve(root, argument('output') ?? 'out/s0-release-evidence');
const imagesRoot = resolve(root, argument('images') ?? 'out/s0-release-input/images');
const repository = process.env.GITHUB_REPOSITORY ?? 'SwayingWindmill/HVAC_web';
const runId = process.env.GITHUB_RUN_ID ?? null;
const startedAt = new Date();

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function portable(path) {
  return relative(root, path).replaceAll('\\', '/');
}

async function filesUnder(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) files.push(...await filesUnder(path));
    else files.push(path);
  }
  return files;
}

async function sha256File(path) {
  return createHash('sha256').update(await readFile(path)).digest('hex');
}

async function readJSON(path, label = portable(path)) {
  assert(existsSync(path), `${label} is missing`);
  return JSON.parse(await readFile(path, 'utf8'));
}

async function run(command, args, options = {}) {
  const child = spawn(command, args, {
    cwd: root,
    env: process.env,
    stdio: ['ignore', 'pipe', 'pipe'],
    shell: false,
    windowsHide: true,
  });
  let stdout = '';
  let stderr = '';
  child.stdout.on('data', (chunk) => { stdout += String(chunk); });
  child.stderr.on('data', (chunk) => { stderr += String(chunk); });
  const exit = await new Promise((resolveExit) => {
    child.once('error', (error) => resolveExit({ code: null, signal: null, error }));
    child.once('exit', (code, signal) => resolveExit({ code, signal, error: null }));
  });
  if (exit.error || exit.code !== 0 || exit.signal) {
    if (options.optional) return null;
    const detail = stderr.trim() || stdout.trim() || exit.error?.message || `exit ${exit.code ?? exit.signal}`;
    throw new Error(`${command} ${args.join(' ')} failed: ${detail}`);
  }
  return { stdout, stderr };
}

async function commandVersion(command, args) {
  const result = await run(command, args, { optional: true });
  return result ? (result.stdout.trim() || result.stderr.trim()).slice(0, 4000) : null;
}

async function collectWorkflow() {
  const path = resolve(outputRoot, 'workflow-jobs.json');
  if (!runId) {
    if (existsSync(path)) return readJSON(path);
    const local = {
      schemaVersion: 1,
      status: 'local-unverified',
      repository,
      runId: null,
      jobs: [],
      artifacts: [],
      reason: 'GITHUB_RUN_ID was not available; formal completion requires a GitHub Actions release dispatch.',
    };
    await writeFile(path, `${JSON.stringify(local, null, 2)}\n`);
    return local;
  }

  const jobsResponse = await run('gh', ['api', `repos/${repository}/actions/runs/${runId}/jobs?per_page=100`]);
  const artifactsResponse = await run('gh', ['api', `repos/${repository}/actions/runs/${runId}/artifacts?per_page=100`]);
  const jobsPayload = JSON.parse(jobsResponse.stdout);
  const artifactsPayload = JSON.parse(artifactsResponse.stdout);
  const jobs = jobsPayload.jobs.map((job) => ({
    id: job.id,
    name: job.name,
    status: job.status,
    conclusion: job.conclusion,
    startedAt: job.started_at,
    completedAt: job.completed_at,
    url: job.html_url,
    steps: (job.steps ?? []).map((step) => ({ name: step.name, status: step.status, conclusion: step.conclusion })),
  }));
  const artifacts = artifactsPayload.artifacts.map((artifact) => ({
    id: artifact.id,
    name: artifact.name,
    sizeBytes: artifact.size_in_bytes,
    expired: artifact.expired,
    expiresAt: artifact.expires_at,
    archiveDownloadURL: artifact.archive_download_url,
  })).sort((left, right) => left.name.localeCompare(right.name));

  const jobSuccess = (name) => jobs.some((job) => job.name === name && job.conclusion === 'success');
  const codeqlJobs = jobs.filter((job) => job.name.startsWith('codeql'));
  const signedImageJobs = jobs.filter((job) => job.name.startsWith('signed-images'));
  const buildRecordArtifacts = artifacts.filter((artifact) => artifact.name.endsWith('.dockerbuild'));
  const trivyArtifacts = artifacts.filter((artifact) => artifact.name.startsWith('trivy-secrets-'));
  const releaseImageArtifacts = artifacts.filter((artifact) => artifact.name.startsWith('release-image-'));
  const securityArtifacts = artifacts.filter((artifact) => artifact.name === 's0-security-gate-results');
  const failedDependencies = jobs.filter((job) => job.name !== 'release-evidence'
    && job.status === 'completed'
    && !['success', 'skipped'].includes(job.conclusion));
  const passed = jobSuccess('verify')
    && jobSuccess('dependency-and-license')
    && jobSuccess('security-failure-gates')
    && codeqlJobs.length === 2
    && codeqlJobs.every((job) => job.conclusion === 'success')
    && signedImageJobs.length === 7
    && signedImageJobs.every((job) => job.conclusion === 'success')
    && buildRecordArtifacts.length === 7
    && trivyArtifacts.length === 7
    && releaseImageArtifacts.length === 7
    && securityArtifacts.length === 1
    && failedDependencies.length === 0;
  const report = {
    schemaVersion: 1,
    ticket: '08-s0-release-evidence',
    status: passed ? 'passed' : 'failed',
    repository,
    runId,
    runURL: `https://github.com/${repository}/actions/runs/${runId}`,
    checkedAt: new Date().toISOString(),
    summary: {
      verify: jobSuccess('verify'),
      dependencyAndLicense: jobSuccess('dependency-and-license'),
      securityFailureGates: jobSuccess('security-failure-gates'),
      codeqlJobs: codeqlJobs.length,
      signedImageJobs: signedImageJobs.length,
      buildRecordArtifacts: buildRecordArtifacts.length,
      trivyArtifacts: trivyArtifacts.length,
      releaseImageArtifacts: releaseImageArtifacts.length,
      securityGateArtifacts: securityArtifacts.length,
      failedDependencies: failedDependencies.map((job) => job.name),
    },
    jobs,
    artifacts,
  };
  await writeFile(path, `${JSON.stringify(report, null, 2)}\n`);
  return report;
}

async function collectImageManifests() {
  assert(existsSync(imagesRoot), `image manifest directory is missing: ${imagesRoot}`);
  const files = (await filesUnder(imagesRoot)).filter((path) => path.endsWith('.json'));
  const manifests = [];
  for (const path of files) {
    const value = JSON.parse(await readFile(path, 'utf8'));
    if (value.ticket === '08-s0-release-evidence' && value.immutableReference) manifests.push(value);
  }
  manifests.sort((left, right) => left.name.localeCompare(right.name));
  assert(manifests.length === 7, `release evidence requires seven image manifests; found ${manifests.length}`);
  assert(new Set(manifests.map((manifest) => manifest.name)).size === 7, 'release image manifest names are not unique');
  for (const manifest of manifests) {
    assert(/^sha256:[a-f0-9]{64}$/.test(manifest.digest), `${manifest.name} digest is invalid`);
    assert(manifest.immutableReference === `${manifest.image}@${manifest.digest}`, `${manifest.name} immutable reference is inconsistent`);
  }
  return manifests;
}

async function collectMigrationState() {
  const migrationRoot = resolve(root, 'infra/s0-durable/postgres');
  const sqlFiles = (await filesUnder(migrationRoot)).filter((path) => path.endsWith('.sql')).sort();
  const files = [];
  for (const path of sqlFiles) files.push({ path: portable(path), sha256: await sha256File(path), bytes: (await readFile(path)).length });
  const migrationSQL = await readFile(resolve(migrationRoot, 'init/001-s0-durable.sql'), 'utf8');
  const compatibilitySQL = await readFile(resolve(migrationRoot, 'compatibility/previous-writer.sql'), 'utf8');
  const expandDefaults = (migrationSQL.match(/traceparent text NOT NULL DEFAULT ''/g) ?? []).length;
  assert(expandDefaults === 3, `expected three rollback-window traceparent defaults, found ${expandDefaults}`);
  assert(compatibilitySQL.includes('previous') || compatibilitySQL.includes('traceparent'), 'previous-writer compatibility fixture is incomplete');
  return {
    status: 'passed',
    strategy: 'expand-contract',
    reverseMigrationDuringRollbackWindow: false,
    expandCompatibilityDefaults: expandDefaults,
    previousWriterFixture: 'infra/s0-durable/postgres/compatibility/previous-writer.sql',
    migrationIdentity: 's0_migrator',
    runtimeExecutesDDL: false,
    files,
  };
}

async function buildCompatibilityReport(workflow) {
  const openapi = JSON.parse(await readFile(resolve(root, 'contracts/http/platform-gateway.openapi.yaml'), 'utf8'));
  const eventLock = await readJSON(resolve(root, 'contracts/events/session-audit.v1.lock.json'));
  const ownershipLock = await readJSON(resolve(root, 'contracts/ownership/ownership.v1.lock.json'));
  const report = {
    schemaVersion: 1,
    ticket: '08-s0-release-evidence',
    status: workflow.status === 'passed' ? 'passed' : 'failed',
    checkedAt: new Date().toISOString(),
    publicContractVersion: openapi.info?.version,
    openapi: openapi.openapi,
    generatedClientsChecked: workflow.summary?.verify === true,
    eventCompatibilityLock: eventLock,
    ownershipCompatibilityLock: ownershipLock,
    previousWriterPostgresCompatibilityChecked: workflow.summary?.verify === true,
    rollbackWindow: 'previous-compatible application version remains valid across additive traceparent migration',
  };
  assert(report.publicContractVersion, 'OpenAPI info.version is missing');
  await writeFile(resolve(outputRoot, 'compatibility-report.json'), `${JSON.stringify(report, null, 2)}\n`);
  return report;
}

async function renderStaging(images) {
  const workRoot = resolve(root, 'out/s0-release-evidence-work');
  const bindingsPath = resolve(workRoot, 'bindings.json');
  const renderedRoot = resolve(workRoot, 'rendered');
  await rm(workRoot, { recursive: true, force: true });
  await mkdir(workRoot, { recursive: true });
  const byName = new Map(images.map((image) => [image.name, image.immutableReference]));
  const bindings = {
    SIGNED_IMAGE_PLATFORM_GATEWAY: byName.get('platform-gateway'),
    SIGNED_IMAGE_IAM_SERVICE: byName.get('iam-service'),
    SIGNED_IMAGE_AUDIT_LEDGER_SERVICE: byName.get('audit-ledger-service'),
    SIGNED_IMAGE_OUTBOX_RELAY: byName.get('outbox-relay'),
    SIGNED_IMAGE_OIDC_TEST_PROVIDER: byName.get('oidc-test-provider'),
    SIGNED_IMAGE_LEGACY_PRIVATE: byName.get('legacy-private'),
    SIGNED_IMAGE_S0_MIGRATOR: byName.get('s0-migrator'),
    GATEWAY_DATABASE_URL: 'postgres://s0_gateway@postgres:5432/s0?sslmode=require',
    SESSION_TOKEN_KEY: '[REDACTED_SECRET]',
    AUDIT_CONSUMER_DATABASE_URL: 'postgres://s0_audit_consumer@postgres:5432/s0?sslmode=require',
    AUDIT_QUERY_DATABASE_URL: 'postgres://s0_audit_query@postgres:5432/s0?sslmode=require',
    OUTBOX_DATABASE_URL: 'postgres://s0_outbox_relay@postgres:5432/s0?sslmode=require',
    MIGRATOR_DATABASE_PASSWORD: '[REDACTED_SECRET]',
    POSTGRES_HOST: 'postgres',
    WORKLOAD_CERT_PATH: '/var/run/s0/tls/tls.crt',
    WORKLOAD_KEY_PATH: '/var/run/s0/tls/tls.key',
    TRUST_BUNDLE_PATH: '/var/run/s0/trust/ca.crt',
    RUNTIME_BINDINGS_IAM_SERVICE: [],
    RUNTIME_BINDINGS_AUDIT_LEDGER_SERVICE: [],
    RUNTIME_BINDINGS_OUTBOX_RELAY: [],
    RUNTIME_BINDINGS_OIDC_TEST_PROVIDER: [],
    RUNTIME_BINDINGS_LEGACY_PRIVATE: [],
    RUNTIME_IDENTITY_MOUNTS: [],
    RUNTIME_IDENTITY_VOLUMES: [],
    RUNTIME_IDENTITY_AND_ROUTE_MOUNTS: [],
    RUNTIME_IDENTITY_AND_ROUTE_VOLUMES: [],
    RUNTIME_TRUST_MOUNTS: [],
    RUNTIME_TRUST_VOLUMES: [],
  };
  assert(Object.values(bindings).every((value) => value !== undefined), 'staging evidence bindings are missing an image');
  await writeFile(bindingsPath, `${JSON.stringify(bindings, null, 2)}\n`);
  const render = await run(process.execPath, [
    resolve(root, 'scripts/render-s0-staging.mjs'),
    `--bindings=${portable(bindingsPath)}`,
    `--output=${portable(renderedRoot)}`,
  ]);
  const renderedFiles = (await filesUnder(renderedRoot)).sort();
  const hashes = [];
  for (const path of renderedFiles) hashes.push({ path: relative(renderedRoot, path).replaceAll('\\', '/'), sha256: await sha256File(path) });
  const receipt = await readJSON(resolve(renderedRoot, 'render-receipt.json'));
  const report = {
    schemaVersion: 1,
    ticket: '08-s0-release-evidence',
    status: 'passed',
    renderedAt: receipt.renderedAt,
    source: receipt.source,
    fileCount: receipt.files.length,
    bindingNames: receipt.bindings,
    renderedFileHashes: hashes,
    rendererOutput: render.stdout.trim(),
    privateBindingValuesRetained: false,
  };
  await writeFile(resolve(outputRoot, 'staging-render-report.json'), `${JSON.stringify(report, null, 2)}\n`);
  await rm(workRoot, { recursive: true, force: true });
  return report;
}

async function reportDescriptor(path) {
  const value = await readJSON(path);
  return {
    path: portable(path),
    sha256: await sha256File(path),
    status: value.status ?? null,
    type: value.type ?? null,
  };
}

async function environmentVersions() {
  return {
    os: `${process.platform}/${process.arch}`,
    node: process.version,
    go: await commandVersion('go', ['version']),
    docker: await commandVersion('docker', ['version', '--format', '{{json .}}']),
    kubectl: await commandVersion('kubectl', ['version', '--client', '-o', 'json']),
    kind: await commandVersion('kind', ['version']),
    cosign: await commandVersion('cosign', ['version']),
    gh: await commandVersion('gh', ['--version']),
    githubRunner: {
      os: process.env.RUNNER_OS ?? null,
      arch: process.env.RUNNER_ARCH ?? null,
      environment: process.env.GITHUB_ACTIONS === 'true' ? 'github-actions' : 'local',
    },
  };
}

async function collectObservabilityAssets() {
  const assetPaths = [
    resolve(root, 'infra/s0-durable/observability/dashboards/s0-platform.json'),
    resolve(root, 'infra/s0-durable/observability/alerts/s0-platform.yaml'),
    resolve(root, 'infra/s0-durable/otel-collector-config.yaml'),
    resolve(root, 'infra/s0-durable/prometheus.yaml'),
  ];
  const assets = [];
  for (const path of assetPaths) {
    assert(existsSync(path), `observability evidence asset is missing: ${portable(path)}`);
    const text = await readFile(path, 'utf8');
    assets.push({
      path: portable(path),
      kind: path.includes('/dashboards/') || path.includes('\\dashboards\\') ? 'dashboard'
        : path.includes('/alerts/') || path.includes('\\alerts\\') ? 'alert-rules'
          : 'collector-or-scrape-config',
      sha256: await sha256File(path),
      bytes: Buffer.byteLength(text),
    });
  }
  const report = {
    schemaVersion: 1,
    ticket: '08-s0-release-evidence',
    type: 'observability-assets',
    status: 'passed',
    checkedAt: new Date().toISOString(),
    dashboardCount: assets.filter((asset) => asset.kind === 'dashboard').length,
    alertRuleFileCount: assets.filter((asset) => asset.kind === 'alert-rules').length,
    traceEvidence: 'out/s0-security/browser-journey-report.json',
    assets,
  };
  assert(report.dashboardCount >= 1, 'release evidence requires at least one dashboard asset');
  assert(report.alertRuleFileCount >= 1, 'release evidence requires at least one alert-rule asset');
  await writeFile(resolve(outputRoot, 'observability-assets-report.json'), `${JSON.stringify(report, null, 2)}\n`);
  return report;
}

function validateInvariants(browser) {
  const expected = {
    crossTenantSuccesses: 0,
    credentialLeakFindings: 0,
    duplicateAuditEffects: 0,
    lostCommittedSessionEvents: 0,
  };
  assert(browser.status === 'passed', 'browser journey report did not pass');
  assert(JSON.stringify(browser.invariants) === JSON.stringify(expected), `zero invariants were not proven: ${JSON.stringify(browser.invariants)}`);
  return expected;
}

await mkdir(outputRoot, { recursive: true });
const matrix = await readJSON(resolve(root, 'deploy/s0/release-evidence/acceptance-matrix.json'));
const images = await collectImageManifests();
const workflow = await collectWorkflow();
const compatibility = await buildCompatibilityReport(workflow);
const migrationState = await collectMigrationState();
const stagingRender = await renderStaging(images);
await collectObservabilityAssets();

const reportPaths = {
  securityFailure: resolve(root, 'out/s0-security/security-failure-gate-report.json'),
  networkPolicy: resolve(root, 'out/s0-security/network-policy-report.json'),
  browserJourney: resolve(root, 'out/s0-security/browser-journey-report.json'),
  rolloutModel: resolve(outputRoot, 'rollout-model-report.json'),
  kindRollout: resolve(outputRoot, 'kind-rollout-report.json'),
  imageVerification: resolve(outputRoot, 'image-verification-report.json'),
  scopeAudit: resolve(outputRoot, 'scope-audit-report.json'),
  compatibility: resolve(outputRoot, 'compatibility-report.json'),
  workflowJobs: resolve(outputRoot, 'workflow-jobs.json'),
  stagingRender: resolve(outputRoot, 'staging-render-report.json'),
  observabilityAssets: resolve(outputRoot, 'observability-assets-report.json'),
};
const reports = {};
for (const [name, path] of Object.entries(reportPaths)) reports[name] = await reportDescriptor(path);
const blockingReports = Object.entries(reports).filter(([name]) => name !== 'workflowJobs');
assert(blockingReports.every(([, report]) => report.status === 'passed'), `one or more release reports failed: ${JSON.stringify(blockingReports)}`);
assert(workflow.status === 'passed', `clean-environment workflow evidence did not pass: ${workflow.status}`);
assert(compatibility.status === 'passed', 'rollback-window compatibility evidence did not pass');
assert(stagingRender.status === 'passed', 'staging render evidence did not pass');

const browser = await readJSON(reportPaths.browserJourney);
const invariants = validateInvariants(browser);
const securityMatrix = await readJSON(resolve(root, 'tests/s0-security/security-failure-matrix.json'));
const environment = await environmentVersions();

const selfGeneratedEvidence = new Set([
  'out/s0-release-evidence/release-evidence.intoto.json',
  'out/s0-release-evidence/SHA256SUMS',
  'out/s0-release-evidence/acceptance-results.json',
]);
const architectureDecisionTrace = matrix.criteria.map((criterion) => {
  const evidence = criterion.runtimeEvidence.map((path) => ({
    path,
    present: selfGeneratedEvidence.has(path) || existsSync(resolve(root, path)),
  }));
  return {
    ...criterion,
    status: evidence.every((entry) => entry.present) ? 'passed' : 'failed',
    evidence,
  };
});
assert(architectureDecisionTrace.every((criterion) => criterion.status === 'passed'), 'one or more acceptance criteria are missing runtime evidence');
const acceptanceResults = {
  schemaVersion: 1,
  ticket: '08-s0-release-evidence',
  status: 'passed',
  checkedAt: new Date().toISOString(),
  criteria: architectureDecisionTrace.map((criterion) => ({ id: criterion.id, status: criterion.status, evidence: criterion.evidence })),
};
await writeFile(resolve(outputRoot, 'acceptance-results.json'), `${JSON.stringify(acceptanceResults, null, 2)}\n`);

const knownLimitations = [
  'Kind validates Kubernetes Deployment, ReplicaSet and rollback mechanics; cloud-specific ingress, workload identity and persistent-volume qualification remains environment-specific.',
  'The staging evidence render uses synthetic secret values and retains only a receipt and rendered-manifest hashes.',
  'OIDC, Legacy, Redpanda and telemetry dependencies are deterministic protocol fixtures rather than production service selections.',
  'GitHub Actions artifact retention is finite; long-term regulated archival and OCI publication of the full bundle are later infrastructure concerns.',
  'Complete legal-retention WORM archival is outside S0; the delivered security-event Audit Ledger is append-only and deduplicated.',
];
const runbooks = [
  'docs/operations/s0-delivery.md',
  'docs/operations/s0-observability.md',
  'docs/operations/s0-security-failure-gates.md',
  'docs/operations/s0-release-evidence.md',
  'docs/security/s0-authenticated-principal.md',
  'docs/security/s0-durable-session-audit.md',
  'docs/security/s0-route-data-ownership.md',
];
for (const path of runbooks) assert(existsSync(resolve(root, path)), `runbook is missing: ${path}`);

const statement = {
  _type: 'https://in-toto.io/Statement/v1',
  subject: images.map((image) => ({ name: image.image, digest: { sha256: image.digest.slice('sha256:'.length) } })),
  predicateType: 'https://hvac.local/attestations/s0-release-evidence/v1',
  predicate: {
    schemaVersion: 1,
    ticket: '08-s0-release-evidence',
    status: 'passed',
    generatedAt: new Date().toISOString(),
    source: {
      repository,
      commit: process.env.GITHUB_SHA ?? (await run('git', ['rev-parse', 'HEAD'])).stdout.trim(),
      ref: process.env.GITHUB_REF ?? null,
      workflow: process.env.GITHUB_WORKFLOW ?? null,
      runId,
      runURL: runId ? `https://github.com/${repository}/actions/runs/${runId}` : null,
    },
    environment,
    fixtures: {
      organizations: securityMatrix.fixtures.organizations,
      serviceIdentities: securityMatrix.fixtures.serviceIdentities,
      failureCases: securityMatrix.cases.map((testCase) => testCase.id),
      upstream: [
        ...securityMatrix.upstream,
        { project: 'in-toto/attestation', version: 'v1.2.0', purpose: 'Evidence Statement and Predicate structure' },
        { project: 'kubernetes-sigs/kind', version: 'v0.32.0', license: 'Apache-2.0', purpose: 'Disposable Kubernetes rollout environment' },
        { project: 'helm/kind-action', version: 'v1.14.0', commit: 'ef37e7f390d99f746eb8b610417061a60e82a6cc', license: 'Apache-2.0', purpose: 'Pinned Kind setup in GitHub Actions' },
      ],
    },
    workflow,
    images,
    reports,
    migrationState,
    architectureDecisionTrace,
    invariants,
    knownLimitations,
    runbooks,
    approval: {
      eligible: true,
      automatedDecision: 'all-blocking-evidence-passed',
      humanApprovalFabricated: false,
      reviewSurface: process.env.GITHUB_SERVER_URL && process.env.GITHUB_REPOSITORY ? `${process.env.GITHUB_SERVER_URL}/${process.env.GITHUB_REPOSITORY}` : null,
      finalDeclaration: 'S0 is complete. S1 is ready to enter implementation specification. This does not authorize S1 implementation without its own accepted specification.',
    },
  },
};
assert(statement.subject.length === 7, 'in-toto statement must contain seven image subjects');
assert(statement.predicate.architectureDecisionTrace.length === 14, 'in-toto statement must contain fourteen acceptance criteria');
assert(statement.predicate.status === 'passed' && statement.predicate.approval.eligible === true, 'release statement is not eligible for approval');

const statementPath = resolve(outputRoot, 'release-evidence.intoto.json');
await writeFile(statementPath, `${JSON.stringify(statement, null, 2)}\n`);
const summary = [
  '# S0 Release Evidence Bundle',
  '',
  `- Status: **${statement.predicate.status}**`,
  `- Repository: \`${repository}\``,
  `- Commit: \`${statement.predicate.source.commit}\``,
  `- Workflow run: ${statement.predicate.source.runURL ?? 'local-unverified'}`,
  `- Immutable images: ${images.length}`,
  `- Acceptance criteria: ${architectureDecisionTrace.length}`,
  '- Zero invariants: cross-tenant success, credential leakage, duplicate Audit effect and lost committed Session event all equal 0.',
  '',
  'The authoritative machine-readable statement is `release-evidence.intoto.json`. Verify file integrity with `SHA256SUMS`.',
  '',
].join('\n');
await writeFile(resolve(outputRoot, 'README.md'), summary);

const evidenceFiles = (await filesUnder(outputRoot))
  .filter((path) => basename(path) !== 'SHA256SUMS')
  .sort((left, right) => portable(left).localeCompare(portable(right)));
const checksumLines = [];
for (const path of evidenceFiles) checksumLines.push(`${await sha256File(path)}  ${relative(outputRoot, path).replaceAll('\\', '/')}`);
await writeFile(resolve(outputRoot, 'SHA256SUMS'), `${checksumLines.join('\n')}\n`);

console.log(`S0 Release Evidence Bundle passed: ${portable(statementPath)} (${Date.now() - startedAt.getTime()} ms).`);
