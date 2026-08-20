import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const text = async (path) => readFile(resolve(root, path), 'utf8');
const json = async (path) => JSON.parse(await text(path));
const assert = (condition, message) => { if (!condition) throw new Error(`Invalid S2 Ticket 11 release asset: ${message}`); };
const output = resolve(root, 'out/s2-telemetry-release/release-assets.json');

const [envelope, gates, attestationSchema, runtimeImage, historyProjectorImage, migratorImage, migrationScript, kindManifest, capacity, kindRunner, bundleBuilder, bundleVerifier, imageWriter, imageMerger, reportWriter, workflow, runbook, runtimeMetrics, gatewayMetrics, runtimeMain, centrifugoConfigCheck, dockerPullRetry, packageJSON] = await Promise.all([
  json('deploy/s2/release-envelope.v1.json'),
  json('deploy/s2/release-gates.v1.json'),
  json('deploy/s2/full-capacity-attestation.schema.json'),
  text('deploy/s2/images/telemetry-runtime.Dockerfile'),
  text('deploy/s2/images/telemetry-history-projector.Dockerfile'),
  text('deploy/s2/images/telemetry-runtime-migrator.Dockerfile'),
  text('deploy/s2/images/run-telemetry-migrations.sh'),
  text('deploy/s2/kind/rollout-probe.yaml'),
  text('scripts/run-s2-capacity-certification.mjs'),
  text('scripts/run-s2-kind-rollout.mjs'),
  text('scripts/build-s2-release-evidence.mjs'),
  text('scripts/verify-s2-release-evidence.mjs'),
  text('scripts/write-s2-image-evidence.mjs'),
  text('scripts/merge-s2-image-evidence.mjs'),
  text('scripts/write-s2-release-report.mjs'),
  text('.github/workflows/s2-telemetry-release.yml'),
  text('docs/operations/s2-capacity-release-evidence.md'),
  text('services/telemetry-runtime-service/internal/telemetry/metrics.go'),
  text('services/platform-gateway/internal/gateway/telemetry_metrics.go'),
  text('services/telemetry-runtime-service/cmd/telemetry-runtime-service/main.go'),
  text('scripts/run-s2-realtime-centrifugo-config-check.mjs'),
  text('scripts/lib/docker-cli.mjs'),
  json('package.json'),
]);

assert(envelope.schemaVersion === 1 && envelope.name === 'initial-production-release-envelope-v1', 'release envelope identity drifted');
assert(envelope.steadyState.durationSeconds === 3600 && envelope.peak.durationSeconds === 900, 'formal duration drifted');
assert(envelope.steadyState.connections === 5000 && envelope.steadyState.subscriptions === 50000 && envelope.steadyState.businessRevisionsPerSecond === 2000, 'load envelope drifted');
assert(envelope.steadyState.minimumHeadroomFraction === 0.30, 'headroom floor drifted');
assert(envelope.failureScenarios.reconnectClients === 10000 && envelope.failureScenarios.reconnectClientsPerSecond === 1000 && envelope.failureScenarios.revocationsPerSecond === 100, 'failure rates drifted');
assert(envelope.transportBounds.clientQueueBytesMaximum === 262144 && envelope.transportBounds.publicationBytesMaximum === 65536, 'transport bounds drifted');
assert(attestationSchema.properties?.steadyStateSeconds?.minimum === 3600 && attestationSchema.properties?.peakSeconds?.minimum === 900, 'formal attestation schema duration drifted');
assert(attestationSchema.properties?.manualApproval?.const === true, 'formal attestation does not require manual approval');
assert(attestationSchema.properties?.load?.properties?.connections?.minimum === 5000 && attestationSchema.properties?.reconnect?.properties?.clientsPerSecond?.minimum === 1000, 'formal attestation load/failure schema drifted');

for (const marker of ['FROM golang:1.25.12-bookworm AS build', 'COPY tools ./tools', 'gcr.io/distroless/static-debian12:nonroot', 'USER 65532:65532', 'ENTRYPOINT ["/telemetry-runtime"]']) {
  assert(runtimeImage.includes(marker), `runtime image is missing ${marker}`);
}
for (const marker of ['FROM golang:1.25.12-bookworm AS build', 'gcr.io/distroless/static-debian12:nonroot', 'USER 65532:65532', 'ENTRYPOINT ["/telemetry-history-projector"]']) {
  assert(historyProjectorImage.includes(marker), `history projector image is missing ${marker}`);
}
for (const marker of ['FROM postgres:16-alpine@sha256:57c72fd2a128e416c7fcc499958864df5301e940bca0a56f58fddf30ffc07777', 'rm -f /usr/local/bin/gosu', 'USER postgres', '001-s2-telemetry-baseline.sql', '004-s2-telemetry-history-outbox.sql', '005-s2-realtime-backend.sql', 'run-telemetry-migrations', 'chmod 0555']) {
  assert(migratorImage.includes(marker), `migrator image is missing ${marker}`);
}
for (const marker of ['ON_ERROR_STOP=1', '/migrations/*.sql', 'sha256sum', 'pg_advisory_lock', 'telemetry_runtime.schema_migrations', "status IN ('APPLYING', 'APPLIED')", 'REVOKE ALL ON telemetry_runtime.schema_migrations', "RAISE EXCEPTION 'migration hash mismatch'", "RAISE EXCEPTION 'incomplete migration requires operator review'", "RAISE EXCEPTION 'failed to record applied migration'"]) {
  assert(migrationScript.includes(marker), `migrator is missing deterministic execution marker: ${marker}`);
}
for (const marker of ['runAsNonRoot: true', 'maxUnavailable: 0', 'freshSnapshotRequired: "true"', 'automountServiceAccountToken: false', 'readOnlyRootFilesystem: true']) {
  assert(kindManifest.includes(marker), `Kind rollout fixture is missing ${marker}`);
}

for (const marker of ['clean-runner-preflight', 'formalReleaseEligible', 'approved-wall-clock-attestation', 'manualApproval', 'snapshotP99Seconds', 'recoveryOrSnapshotFailureFraction', 'failureScenarios', 'postRevocationDeliveries', 'repository SHA', 'did not exercise 5k connections', 'did not exercise 50k subscriptions', 'did not exercise 2k revisions/s', 'computed headroom', 'minimum measured headroom', 'zero-tolerance counter']) {
  assert(capacity.includes(marker), `capacity certification is missing ${marker}`);
}
assert(capacity.includes("profile === 'preflight'") && capacity.includes('wall-clock-attestation') && capacity.includes("certificationLevel: 'formal'"), 'formal capacity path is not separately gated');
for (const marker of ["'rollout', 'undo'", "routeRevision: 'R3'", "freshSnapshotRequired: 'true'", 'databaseDownMigrationPerformed: false']) {
  assert(kindRunner.includes(marker), `Kind rollback runner is missing ${marker}`);
}
for (const marker of ['release-evidence.intoto.json', 'SHA256SUMS', 'allSecurityZeroInvariants', 'reviewerCanVerifyOffline', 'formally attested runtime, history projector, and migrator images']) {
  assert(bundleBuilder.includes(marker), `release evidence builder is missing ${marker}`);
}
assert(bundleVerifier.includes('digest mismatch') && bundleVerifier.includes('reviewerCanVerifyOffline') && bundleVerifier.includes('subject digest mismatch'), 'offline verifier is incomplete');
assert(imageWriter.includes('telemetry-history-projector') && imageWriter.includes('highOrCriticalVulnerabilities') && imageWriter.includes('secretFindings') && imageWriter.includes('non-root') && imageWriter.includes('CycloneDX') && imageWriter.includes('formal image evidence requires'), 'image evidence writer is incomplete');
assert(imageMerger.includes('production-image-report.json') && imageMerger.includes('sbom-provenance-report.json') && imageMerger.includes('copyFile') && imageMerger.includes('formalReleaseEligible'), 'image merger is incomplete');
assert(reportWriter.includes('cleanRunner') && reportWriter.includes('commands') && reportWriter.includes('sha256') && reportWriter.includes('sources'), 'standard report writer is incomplete');
for (const marker of ['hvac_s2_snapshot_requests_total', 'hvac_s2_publications_total', 'hvac_s2_recovery_attempts_total', 'hvac_s2_revocation_events_total', 'hvac_s2_quarantine_records_total']) {
  assert(runtimeMetrics.includes(marker), `Telemetry Runtime production metrics are missing ${marker}`);
}
for (const marker of ['hvac_s2_upstream_requests_total', 'hvac_s2_upstream_duration_seconds']) {
  assert(gatewayMetrics.includes(marker), `Gateway production metrics are missing ${marker}`);
}
assert(runtimeMain.includes('DiagnosticsHandler()') && runtimeMain.includes('TELEMETRY_DIAGNOSTICS_ADDR'), 'Telemetry Runtime diagnostics endpoint is missing');
assert(centrifugoConfigCheck.includes('pullDockerImageWithRetry') && centrifugoConfigCheck.includes("'--pull=never'"), 'Centrifugo config check is missing bounded immutable-image pull handling');
for (const marker of ['immutable sha256 digest', 'attempts ?? 5', "['pull', image]", 'failed after ${attempts} attempts', 'slice(0, 2000)']) {
  assert(dockerPullRetry.includes(marker), `Docker pull retry helper is missing ${marker}`);
}

const requiredReports = gates.requiredEvidence.filter((path) => !path.endsWith('release-evidence.intoto.json') && !path.endsWith('SHA256SUMS')).map((path) => path.split('/').at(-1));
for (const source of [bundleBuilder, bundleVerifier]) {
  assert(source.includes('deploy/s2/release-gates.v1.json') && source.includes('requiredEvidence'), 'release evidence tooling is not derived from the authoritative gate list');
}

const jobs = ['contracts-and-static', 'security-negative', 'postgres-integration', 'history-integration', 'transport-integration', 'capacity-and-failure', 'browser-real-mode', 'production-images', 'kind-rollout-rollback', 'release-evidence'];
for (const job of jobs) assert(workflow.includes(`  ${job}:`), `workflow job ${job} is missing`);
for (const marker of ['runs-on: ubuntu-24.04', 'actions/checkout@v6', 'actions/setup-go@v6', 'cache-dependency-path: |', '**/go.sum', 'go.work.sum', 'actions/setup-node@v6', 'actions/upload-artifact@v6', 'actions/download-artifact@v6', 'gitleaks/gitleaks-action@v2', 'npm run s2:telemetry-release', 'npm run s2:security-observability', 'npm run s2:postgres-integration', 'npm run s2:history:integration', 'npm run s2:transport-integration', 'npm run s2:hvac-web:browser', 'docker/setup-buildx-action@v4', 'docker/login-action@v4', 'docker/build-push-action@v7', 'telemetry-history-projector', 'deploy/s2/images/telemetry-history-projector.Dockerfile', 'actions/attest-build-provenance@v4', 'Stage flat image evidence artifact', 'path: out/s2-image-artifact/*', "tr -d '\\r\\n'", "printf 'image=%s\\nscan_ref=%s\\ndigest=%s\\nuser=%s\\n'", 'Generate CycloneDX SBOM', 'format: cyclonedx', 'severity: HIGH,CRITICAL', 'buildkit-mode-max', 'audit:s2-kind-rollout', 's2:release-evidence']) {
  assert(workflow.includes(marker), `release workflow is missing ${marker}`);
}
assert(workflow.includes('options: [preflight, full]') && workflow.includes('wall_clock_attestation_json') && workflow.includes('S2_CAPACITY_PROFILE'), 'formal workflow profile or attestation input is missing');
assert(workflow.includes('needs: [contracts-and-static, security-negative, postgres-integration, history-integration, transport-integration, capacity-and-failure, browser-real-mode, production-images, kind-rollout-rollback]'), 'release evidence is not blocked by all jobs');

for (const script of ['build:telemetry-runtime-image', 'build:telemetry-history-projector-image', 'build:telemetry-runtime-migrator', 'test:s2-migrator-image', 'test:docker-pull-retry', 's2:postgres-integration', 's2:history:integration', 's2:transport-integration', 's2:release:check', 's2:capacity', 'audit:s2-kind-rollout', 's2:release-evidence', 's2:release-evidence:verify', 'test:s2-release-evidence', 'test:s2-capacity', 'test:dependency-audit-retry', 's2:telemetry-release']) {
  assert(packageJSON.scripts?.[script], `package script ${script} is missing`);
}
for (const marker of ['preflight', 'formal', '60-minute', '15-minute', 'not production certification', 'SHA256SUMS', 'in-toto', 'fresh Snapshot']) {
  assert(runbook.toLowerCase().includes(marker.toLowerCase()), `Runbook is missing ${marker}`);
}

await mkdir(dirname(output), { recursive: true });
await writeFile(output, `${JSON.stringify({ schemaVersion: 1, ticket: 70, status: 'passed', workflowJobs: jobs, requiredReports }, null, 2)}\n`);
console.log(`S2 Ticket 11 release assets passed: ${output}`);
