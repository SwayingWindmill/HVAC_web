import { existsSync, readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const requiredFiles = [
  'libs/commandmodel/identity.go',
  'libs/commandmodel/identity_test.go',
  'libs/workloadtls/workloadtls.go',
  'libs/workloadtls/workloadtls_test.go',
  'services/command-service/cmd/command-service/main.go',
  'services/command-service/cmd/command-service/main_test.go',
  'services/command-service/pkg/commandservice/runtime_http.go',
  'services/command-service/pkg/commandservice/connector_evidence.go',
  'services/command-service/pkg/commandservice/grant_use.go',
  'services/command-service/migrations/002_s3_target_runtime.sql',
  'services/command-dispatcher/cmd/command-dispatcher/main.go',
  'services/command-dispatcher/cmd/command-verifier/main.go',
  'services/command-dispatcher/pkg/commanddispatcher/runtime_client.go',
  'services/command-dispatcher/pkg/commanddispatcher/reported_state_client.go',
  'services/thingsboard-connector-control/pkg/controlconnector/target_runtime.go',
  'services/telemetry-runtime-service/internal/telemetry/command_verifier_server.go',
  'deploy/s3/images/command-migrator.Dockerfile',
  'deploy/s3/target/README.md',
  'deploy/s3/target/namespace.yaml',
  'deploy/s3/target/serviceaccounts.yaml',
  'deploy/s3/target/configmaps.yaml',
  'deploy/s3/target/services.yaml',
  'deploy/s3/target/networkpolicies.yaml',
  'deploy/s3/target/disruption-budgets.yaml',
  'deploy/s3/target/migration-job.yaml',
  'deploy/s3/target/secret-provider-class.yaml',
  'deploy/s3/target/workloads/command-service.yaml',
  'deploy/s3/target/workloads/command-dispatcher.yaml',
  'deploy/s3/target/workloads/command-verifier.yaml',
  'deploy/s3/target/patches/telemetry-runtime-command-verifier.yaml',
  'deploy/s3/target/kustomization.yaml',
];

const failures = [];
const text = (path) => readFileSync(resolve(root, path), 'utf8');
const requireFile = (path) => {
  if (!existsSync(resolve(root, path))) failures.push(`missing file: ${path}`);
};
const requireText = (path, needle) => {
  const body = text(path);
  if (!body.includes(needle)) failures.push(`${path} is missing required text: ${needle}`);
};
const forbidText = (path, needle) => {
  const body = text(path);
  if (body.includes(needle)) failures.push(`${path} contains forbidden text: ${needle}`);
};

for (const path of requiredFiles) requireFile(path);
if (failures.length === 0) {
  const routes = [
    '/internal/v1/dispatches:claim',
    '/internal/v1/dispatches:resolve',
    '/internal/v1/verifications:claim',
    '/internal/v1/verifications:resolve',
    '/internal/v1/connector-evidence:prepare',
    '/internal/v1/connector-evidence:complete',
  ];
  for (const route of routes) requireText('services/command-service/pkg/commandservice/runtime_http.go', route);

  requireText('deploy/s3/target/README.md', '--build-arg SERVICE_PACKAGE=./services/command-service/cmd/command-service');
  requireText('deploy/s3/target/README.md', '--build-arg SERVICE_PACKAGE=./services/command-dispatcher/cmd/command-dispatcher');
  requireText('deploy/s3/target/README.md', '--build-arg SERVICE_PACKAGE=./services/command-dispatcher/cmd/command-verifier');
  forbidText('deploy/s3/target/README.md', '--build-arg SERVICE=');
  forbidText('deploy/s3/target/README.md', '--build-arg MODULE_PATH=');

  requireText('libs/workloadtls/workloadtls.go', 'tls.RequireAndVerifyClientCert');
  requireText('libs/workloadtls/workloadtls.go', 'tls.VersionTLS13');
  requireText('libs/workloadtls/workloadtls.go', 'GetCertificate');
  requireText('libs/workloadtls/workloadtls.go', 'GetClientCertificate');
  requireText('libs/workloadtls/workloadtls.go', 'defaultConnectionLifetime');
  requireText('libs/workloadtls/workloadtls.go', 'rotatingTransport');
  requireText('libs/workloadtls/workloadtls_test.go', 'TestRotatingTransportReplacesLongLivedConnections');
  requireText('services/command-service/cmd/command-service/main.go', 'workloadtls.NewServerTLSConfig');
  requireText('services/command-dispatcher/cmd/command-dispatcher/main.go', 'workloadtls.NewHTTPClient');
  requireText('services/command-dispatcher/cmd/command-verifier/main.go', 'workloadtls.NewHTTPClient');
  forbidText('services/command-service/cmd/command-service/main.go', 'tls.LoadX509KeyPair');
  forbidText('services/command-dispatcher/cmd/command-dispatcher/main.go', 'tls.LoadX509KeyPair');
  forbidText('services/command-dispatcher/cmd/command-verifier/main.go', 'tls.LoadX509KeyPair');
  requireText('services/command-service/pkg/commandservice/grant_use.go', 'command_grant_uses');
  requireText('services/command-service/pkg/commandservice/postgres_dispatch.go', 'ClaimDispatchForCohort');
  requireText('services/command-service/pkg/commandservice/postgres_verification.go', 'ClaimVerificationForCohort');
  requireText('services/command-service/pkg/commandservice/runtime_http.go', 'handler.organizationID, handler.siteID, handler.deviceID');
  forbidText('services/command-service/pkg/commandservice/runtime_http.go', 'json:"organizationId"');
  requireText('services/command-service/migrations/002_s3_target_runtime.sql', 'connector_evidence');
  requireText('services/command-service/migrations/002_s3_target_runtime.sql', 'command_grant_uses');
  requireText('services/command-service/migrations/002_s3_target_runtime.sql', "status = 'VERIFIED'");
  requireText('services/command-service/migrations/002_s3_target_runtime.sql', "maximum_delta = 1");

  requireText('services/thingsboard-connector-control/pkg/controlconnector/target_runtime.go', 'commandmodel.IsUUIDv7');
  requireText('libs/commandmodel/identity.go', 'func IsUUIDv7');
  requireText('services/thingsboard-connector-control/pkg/controlconnector/target_runtime.go', 'approvedMappingStatus');
  requireText('services/thingsboard-connector-control/pkg/controlconnector/target_runtime.go', 'workload');
  requireText('services/thingsboard-connector-control/pkg/controlconnector/target_runtime.go', 'secret');
  requireText('services/command-dispatcher/cmd/command-dispatcher/main.go', 'requireHTTPSOrigin');
  requireText('services/command-dispatcher/pkg/commanddispatcher/reported_state_client.go', 'validS2EvidenceID');
  requireText('services/telemetry-runtime-service/internal/telemetry/server.go', 'InternalCommandReportedStatePath');
  requireText('services/telemetry-runtime-service/internal/telemetry/command_verifier_server.go', 'allowedCommandVerifierSPIFFE');

  const targetFiles = requiredFiles.filter((path) => path.startsWith('deploy/s3/target/') && !path.endsWith('.md'));
  const targetBundle = targetFiles.map((path) => text(path)).join('\n');
  if (/kind:\s*Ingress\b/.test(targetBundle)) failures.push('target bundle must not contain an Ingress');
  if (/type:\s*(LoadBalancer|NodePort)\b/.test(targetBundle)) failures.push('target bundle must not expose LoadBalancer or NodePort Services');
  if (/LOCAL_VERIFIED/.test(targetBundle)) failures.push('target bundle must not permit LOCAL_VERIFIED mappings');

  requireText('deploy/s3/target/services.yaml', 'type: ClusterIP');
  requireText('deploy/s3/target/serviceaccounts.yaml', 'policy://[PROVIDER]/[CLUSTER]/cert-manager/command-service');
  requireText('deploy/s3/target/serviceaccounts.yaml', 'policy://[PROVIDER]/[CLUSTER]/cert-manager/command-dispatcher');
  requireText('deploy/s3/target/serviceaccounts.yaml', 'policy://[PROVIDER]/[CLUSTER]/cert-manager/command-verifier');
  requireText('scripts/check-s3-target-context.mjs', 'certificateApprovalPolicy');
  requireText('scripts/check-s3-target-context.mjs', 'projectedValueDriver');
  requireText('scripts/check-s3-target-context.mjs', 'projectedValueClassAttribute');
  requireText('scripts/check-s3-target-context.mjs', 'projected-value-driver-invalid');
  requireText('scripts/check-s3-target-context.mjs', 'projected-value-class-invalid');
  requireText('deploy/s3/target/configmaps.yaml', 'organizationId: "[APPROVED_ORGANIZATION_UUIDV7]"');
  requireText('deploy/s3/target/configmaps.yaml', 'siteId: "[APPROVED_SITE_UUIDV7]"');
  requireText('deploy/s3/target/configmaps.yaml', 'deviceId: "[APPROVED_DEVICE_UUIDV7]"');
  requireText('deploy/s3/target/workloads/command-service.yaml', 'COMMAND_APPROVED_ORGANIZATION_ID');
  requireText('deploy/s3/target/workloads/command-service.yaml', 'COMMAND_APPROVED_SITE_ID');
  requireText('deploy/s3/target/workloads/command-service.yaml', 'COMMAND_APPROVED_DEVICE_ID');
  requireText('deploy/s3/target/configmaps.yaml', '"mappingStatus": "VERIFIED"');
  requireText('deploy/s3/target/configmaps.yaml', '"providerContract": "THINGSBOARD_CE_4.3.1.3"');
  requireText('deploy/s3/target/configmaps.yaml', '"maximumSetpointDeltaC": 1');
  requireText('deploy/s3/target/configmaps.yaml', '"reportedStateKey": "zone.temperature_setpoint"');
  requireText('deploy/s3/target/networkpolicies.yaml', 'name: default-deny-all');
  requireText('deploy/s3/target/networkpolicies.yaml', 'name: command-dispatcher-egress');
  requireText('deploy/s3/target/networkpolicies.yaml', 'name: command-verifier-egress');
  requireText('deploy/s3/target/secret-provider-class.yaml', 'secrets-store.csi.x-k8s.io/v1');
  requireText('deploy/s3/target/secret-provider-class.yaml', 'name: thingsboard-connector-credential');
  requireText('deploy/s3/target/secret-provider-class.yaml', 'name: s3-command-service-database');
  requireText('deploy/s3/target/secret-provider-class.yaml', 'name: s3-command-migrator-database');
  requireText('deploy/s3/target/workloads/command-service.yaml', '[SECRETS_STORE_CSI_DRIVER]');
  requireText('deploy/s3/target/workloads/command-service.yaml', '[SECRETS_STORE_CLASS_ATTRIBUTE]');
  requireText('deploy/s3/target/workloads/command-dispatcher.yaml', '[SECRETS_STORE_CSI_DRIVER]');
  requireText('deploy/s3/target/workloads/command-dispatcher.yaml', '[SECRETS_STORE_CLASS_ATTRIBUTE]');
  requireText('deploy/s3/target/migration-job.yaml', '[SECRETS_STORE_CSI_DRIVER]');
  requireText('deploy/s3/target/migration-job.yaml', '[SECRETS_STORE_CLASS_ATTRIBUTE]');
  requireText('deploy/s3/target/workloads/command-service.yaml', '[SIGNED_IMAGE_COMMAND_SERVICE_DIGEST]');
  requireText('deploy/s3/target/workloads/command-dispatcher.yaml', '[SIGNED_IMAGE_COMMAND_DISPATCHER_DIGEST]');
  requireText('deploy/s3/target/workloads/command-verifier.yaml', '[SIGNED_IMAGE_COMMAND_VERIFIER_DIGEST]');
  requireText('deploy/s3/target/workloads/command-service.yaml', 's3.hvac/public-route: disabled');
  requireText('deploy/s3/target/workloads/command-service.yaml', 'fsGroup: 65532');
  requireText('deploy/s3/target/workloads/command-dispatcher.yaml', 'fsGroup: 65532');
  requireText('deploy/s3/target/workloads/command-verifier.yaml', 'fsGroup: 65532');
  requireText('deploy/s3/target/migration-job.yaml', 'fsGroup: 999');
  requireText('deploy/s3/target/workloads/command-dispatcher.yaml', 's3.hvac/database-access: forbidden');
  requireText('deploy/s3/target/workloads/command-verifier.yaml', 's3.hvac/database-access: forbidden');
  requireText('services/command-service/cmd/command-service/main.go', 'COMMAND_DATABASE_URL_FILE');
  forbidText('services/command-service/cmd/command-service/main.go', 'requiredEnv("COMMAND_DATABASE_URL")');
  requireText('deploy/s3/target/workloads/command-service.yaml', 'COMMAND_DATABASE_URL_FILE');
  forbidText('deploy/s3/target/workloads/command-service.yaml', 'name: COMMAND_DATABASE_URL,');
  requireText('deploy/s3/target/workloads/command-service.yaml', 's3-command-service-database');
  for (const [path, spiffe] of [
    ['deploy/s3/target/workloads/command-service.yaml', 'spiffe://hvac.local/command-service'],
    ['deploy/s3/target/workloads/command-dispatcher.yaml', 'spiffe://hvac.local/command-dispatcher'],
    ['deploy/s3/target/workloads/command-verifier.yaml', 'spiffe://hvac.local/command-verifier'],
  ]) {
    requireText(path, 'driver: csi.cert-manager.io');
    requireText(path, 'csi.cert-manager.io/uri-sans: ' + spiffe);
    requireText(path, 'csi.cert-manager.io/certificate-file: tls.crt');
    requireText(path, 'csi.cert-manager.io/privatekey-file: tls.key');
    requireText(path, 'csi.cert-manager.io/fs-group: "65532"');
    forbidText(path, 'csi.spiffe.io');
    forbidText(path, 'svid.pem');
    forbidText(path, 'svid_key.pem');
  }
  requireText('deploy/s3/target/workloads/command-service.yaml', 'command-service.s3-certification.svc.cluster.local');
  forbidText('deploy/s3/target/workloads/command-dispatcher.yaml', 'COMMAND_DATABASE_URL');
  forbidText('deploy/s3/target/workloads/command-verifier.yaml', 'COMMAND_DATABASE_URL');
  requireText('deploy/s3/target/migration-job.yaml', 'name: PGSERVICEFILE');
  requireText('deploy/s3/target/migration-job.yaml', 'name: PGSERVICE, value: s3-command-migrator');
  requireText('deploy/s3/images/run-command-migrations.sh', '001_s3_command_runtime.sql');
  requireText('deploy/s3/images/run-command-migrations.sh', '002_s3_target_runtime.sql');

  const rendered = spawnSync('kubectl', ['kustomize', 'deploy/s3/target'], {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
  });
  if (rendered.error) {
    failures.push(`kubectl kustomize could not run: ${rendered.error.message}`);
  } else if (rendered.status !== 0) {
    failures.push(`kubectl kustomize failed: ${String(rendered.stderr || rendered.stdout).trim()}`);
  } else {
    if (/kind:\s*Ingress\b/.test(rendered.stdout)) failures.push('rendered template contains an Ingress');
    if (/type:\s*(LoadBalancer|NodePort)\b/.test(rendered.stdout)) failures.push('rendered template exposes a public Service type');
  }
}

if (failures.length > 0) {
  console.error('S3 target runtime check failed:');
  for (const failure of failures) console.error(`- ${failure}`);
  process.exit(1);
}

console.log(`S3 target runtime check passed: files=${requiredFiles.length}; publicRoutes=0; targetService=ClusterIP; mapping=VERIFIED`);
