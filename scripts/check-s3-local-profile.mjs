import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const requiredFiles = [
  'deploy/s3/local/README.md',
  'deploy/s3/local/kind-config.yaml',
  'deploy/s3/local/namespace.yaml',
  'deploy/s3/local/postgres.yaml',
  'deploy/s3/local/migrator-job.yaml',
  'deploy/s3/local/runtime.yaml',
  'deploy/s3/local/seed-job.yaml',
  'deploy/s3/local-thingsboard/README.md',
  'deploy/s3/local-thingsboard/bridge.yaml',
  'infra/s3-thingsboard/compose.yaml',
  'scripts/s3-local.sh',
  'scripts/s3-local-web.mjs',
  'scripts/run-s3-local-web-smoke.mjs',
  'scripts/provision-s3-local-thingsboard.mjs',
  'scripts/render-s3-local-thingsboard-runtime.mjs',
  'scripts/s3-local-thingsboard.mjs',
  'apps/hvac-web/src/api/commands.ts',
  'apps/hvac-web/src/api/command-contract.ts',
  'apps/hvac-web/src/pages/Commands/index.tsx',
  'services/command-service/cmd/command-service/main.go',
  'services/command-service/cmd/command-service/main_test.go',
  'services/command-service/pkg/commandservice/runtime_http.go',
  'services/command-service/pkg/commandservice/runtime_http_test.go',
  'services/command-service/cmd/s3-local-seed/main.go',
  'services/platform-gateway/cmd/s3-local-web-gateway/main.go',
  'services/platform-gateway/cmd/s3-local-web-gateway/main_test.go',
  'services/thingsboard-connector-control/cmd/s3-local-device-simulator/main.go',
  'services/thingsboard-connector-control/cmd/s3-local-device-simulator/main_test.go',
  'services/thingsboard-connector-control/cmd/s3-local-thingsboard-bridge/main.go',
];

const read = (path) => readFileSync(resolve(root, path), 'utf8');
const requireMatch = (value, pattern, label) => {
  if (!pattern.test(value)) throw new Error(`${label} is missing`);
};
const forbidMatch = (value, pattern, label) => {
  if (pattern.test(value)) throw new Error(`${label} is forbidden`);
};
for (const path of requiredFiles) read(path);

const yaml = requiredFiles.filter((path) => path.endsWith('.yaml')).map(read).join('\n');
const launcher = read('scripts/s3-local.sh');
const webManager = read('scripts/s3-local-web.mjs');
const webSmoke = read('scripts/run-s3-local-web-smoke.mjs');
const readme = read('deploy/s3/local/README.md');
const thingsBoardReadme = read('deploy/s3/local-thingsboard/README.md');
const thingsBoardCompose = read('infra/s3-thingsboard/compose.yaml');
const thingsBoardProvisioner = read('scripts/provision-s3-local-thingsboard.mjs');
const thingsBoardRenderer = read('scripts/render-s3-local-thingsboard-runtime.mjs');
const thingsBoardManager = read('scripts/s3-local-thingsboard.mjs');
const simulator = read('services/thingsboard-connector-control/cmd/s3-local-device-simulator/main.go');
const thingsBoardBridge = read('services/thingsboard-connector-control/cmd/s3-local-thingsboard-bridge/main.go');
const seed = read('services/command-service/cmd/s3-local-seed/main.go');
const commandServiceMain = read('services/command-service/cmd/command-service/main.go');
const commandRuntime = read('services/command-service/pkg/commandservice/runtime_http.go');
const commandRuntimeTests = read('services/command-service/pkg/commandservice/runtime_http_test.go');
const localGateway = read('services/platform-gateway/cmd/s3-local-web-gateway/main.go');
const localGatewayTests = read('services/platform-gateway/cmd/s3-local-web-gateway/main_test.go');
const commandApi = read('apps/hvac-web/src/api/commands.ts');
const commandContract = read('apps/hvac-web/src/api/command-contract.ts');
const commandPage = read('apps/hvac-web/src/pages/Commands/index.tsx');

requireMatch(yaml, /name:\s+s3-local\b/, 's3-local namespace');
requireMatch(yaml, /s3\.hvac\/formal-certification:\s+"false"/, 'local certification denial label');
for (const image of ['command-service', 'command-dispatcher', 'command-verifier', 'device-simulator', 'command-seed', 'command-migrator', 'web-gateway', 'thingsboard-bridge']) {
  requireMatch(yaml, new RegExp(`image:\\s+hvac-s3-local\\/${image}:dev`), `local ${image} image`);
}
requireMatch(yaml, /postgres:16\.4-bookworm@sha256:[0-9a-f]{64}/, 'pinned PostgreSQL image');
requireMatch(yaml, /name:\s+s3-local-web-gateway[\s\S]*?type:\s+ClusterIP/, 'local Web Gateway ClusterIP');
requireMatch(yaml, /name:\s+s3-local-thingsboard-bridge[\s\S]*?type:\s+ClusterIP/, 'local ThingsBoard Bridge ClusterIP');
requireMatch(yaml, /s3\.hvac\/access:\s+kubectl-port-forward-only/, 'port-forward-only access marker');

const localImageCount = (yaml.match(/image:\s+hvac-s3-local\//g) ?? []).length;
const neverPullCount = (yaml.match(/imagePullPolicy:\s+Never/g) ?? []).length;
if (localImageCount !== 8 || neverPullCount !== 8) {
  throw new Error(`local image pull policy mismatch: images=${localImageCount} never=${neverPullCount}`);
}

forbidMatch(yaml, /kind:\s+Ingress\b/, 'Ingress');
forbidMatch(yaml, /type:\s+(?:LoadBalancer|NodePort)\b/, 'public Service type');
forbidMatch(yaml, /hostNetwork:\s+true\b/, 'host networking');
forbidMatch(yaml, /hostPort:\s*\d+/, 'host port');
forbidMatch(yaml, /imagePullPolicy:\s+Always\b/, 'mutable image pull');
forbidMatch(yaml, /image:\s+[^\s]+:latest\b/, 'latest image tag');
forbidMatch(yaml, /formal-certification:\s+"true"/, 'formal certification claim');
forbidMatch(yaml, /\[(?:PLACEHOLDER|REDACTED_SECRET|LOCAL_[A-Z_]+)\]/, 'unrendered placeholder');

requireMatch(launcher, /KIND_VERSION="v0\.32\.0"/, 'pinned kind version');
requireMatch(launcher, /KIND_SHA256="50030de23cf40a18505f20426f6a8506bedf13c6e509244bd1fa9463721b0f54"/, 'pinned kind checksum');
requireMatch(launcher, /hvac-s3-local\/web-gateway:dev/, 'Web Gateway build/load image');
requireMatch(launcher, /hvac-s3-local\/thingsboard-bridge:dev/, 'ThingsBoard Bridge build/load image');
requireMatch(launcher, /issue_certificate platform-gateway/, 'Gateway workload certificate');
requireMatch(launcher, /issue_certificate command-dispatcher-ahu-01[\s\S]*spiffe:\/\/hvac\.local\/command-dispatcher\/ahu-01/, 'AHU Dispatcher workload certificate');
requireMatch(launcher, /issue_certificate command-verifier-chiller-03[\s\S]*spiffe:\/\/hvac\.local\/command-verifier\/chiller-03/, 'Chiller Verifier workload certificate');
requireMatch(launcher, /issue_certificate thingsboard-bridge/, 'ThingsBoard Bridge workload certificate');
requireMatch(launcher, /web-csrf-token/, 'random local Web CSRF input');
requireMatch(launcher, /s3-local-web-gateway-pki/, 'generated Web Gateway projected values');
requireMatch(launcher, /formalCertificationClaim": false/, 'local smoke claim denial');
requireMatch(launcher, /s3-local-smoke-\$\(date -u \+%s%N\)/, 'unique smoke idempotency key');
requireMatch(launcher, /SUCCEEDED\\\|VERIFIED\\\|1\\\|s2:sha256:/, 'database smoke terminal state');
forbidMatch(launcher, /formalCertificationClaim": true/, 'formal smoke claim');

requireMatch(simulator, /tls\.VersionTLS13/, 'TLS 1.3 simulator minimum');
requireMatch(simulator, /tls\.RequireAndVerifyClientCert/, 'reported-state mTLS');
requireMatch(simulator, /spiffe:\/\/hvac\.local\/command-verifier/, 'verifier SPIFFE restriction');
requireMatch(thingsBoardBridge, /tls\.VersionTLS13/, 'ThingsBoard Bridge TLS 1.3 minimum');
requireMatch(thingsBoardBridge, /tls\.RequireAndVerifyClientCert/, 'ThingsBoard Bridge reported-state mTLS');
requireMatch(thingsBoardBridge, /host\.docker\.internal/, 'ThingsBoard Bridge local host restriction');
requireMatch(thingsBoardBridge, /setTemperatureSetpoint/, 'ThingsBoard virtual Device RPC method');
requireMatch(seed, /OpenPostgresStore/, 'production PostgreSQL store reuse');
requireMatch(seed, /S3_LOCAL_TENANT_ID/, 'local seed Tenant scope');
requireMatch(seed, /S3_LOCAL_COMMAND_POINT_ID/, 'local seed canonical Command Point');
requireMatch(seed, /ExpiresAt:\s+now\.Add\(25 \* time\.Second\)/, 'bounded local authorization lifetime');
requireMatch(launcher, /"schemaVersion": 2[\s\S]*"tenantId"[\s\S]*"commandPointId"[\s\S]*"verificationPointKey"/, 'base local Device catalog v2 authority');

for (const token of ['tls.VersionTLS13', 'X-Command-Grant', 'X-Command-Read-Context', 'X-CSRF-Token', 'spiffe://hvac.local/platform-gateway', 'formal_certification_claim", false', 'commandPointId', 'verificationPointKey', 'catalog.SchemaVersion != 2']) {
  if (!localGateway.includes(token)) throw new Error(`local Gateway invariant ${token} is missing`);
}
for (const test of ['TestLocalWebGatewayCreatesExactShortLivedCommandGrant', 'TestLocalWebGatewayReadUsesExactDelegation', 'TestLocalWebGatewayRejectsMutationBeforeUpstream', 'TestLocalDeviceProjectionDoesNotExposeAuthorityMetadata', 'TestLoadDeviceCatalogRequiresV2TenantAndPointAuthority']) {
  requireMatch(localGatewayTests, new RegExp(test), `local Gateway test ${test}`);
}

requireMatch(commandApi, /COMMAND_LOCAL_ROUTES_ENABLED = API_MODE === 'real'/, 'local Command route environment gate');
requireMatch(commandApi, /import\.meta\.env\.DEV/, 'development-only local Command gate');
requireMatch(commandApi, /VITE_S3_LOCAL_COMMANDS/, 'explicit local Command flag');
requireMatch(commandContract, /pointId:\s+commandUUIDv7Schema/, 'public Command canonical Point projection');
requireMatch(localGateway, /OrganizationID\s+string `json:"organizationId"`/, 'local Device Organization projection');
requireMatch(localGateway, /TenantID\s+string `json:"tenantId"`/, 'local Device Tenant projection');
requireMatch(localGateway, /CommandPointID\s+string `json:"commandPointId"`/, 'local Device Command Point projection');
requireMatch(localGateway, /SiteID\s+string `json:"siteId"`/, 'local Device Site projection');
requireMatch(commandPage, /LOCAL \/ KIND/, 'local Web environment label');
requireMatch(commandPage, /S3 本地集成环境/, 'local Web safety notice');
requireMatch(commandPage, /const mayCreate = can\(role, 'create', 'command'\) && API_MODE === 'mock'/, 'local UI mutation denial');
requireMatch(commandPage, /refetchInterval/, 'Command status polling');
requireMatch(commandPage, /\? 1000 : false/, 'one-second non-terminal polling');

requireMatch(webManager, /kubectl[\s\S]*port-forward/, 'Gateway port-forward manager');
requireMatch(webManager, /18081:8080/, 'Gateway port-forward conflict-free port');
requireMatch(webManager, /VITE_API_MODE:\s*'real'/, 'real frontend API mode');
requireMatch(webManager, /VITE_S3_LOCAL_COMMANDS:\s*'true'/, 'local frontend Command flag');
requireMatch(webManager, /formalCertificationClaim:\s*false/, 'Web process claim denial');
requireMatch(webSmoke, /\/api\/v1\/principal/, 'same-origin principal preflight');
requireMatch(webSmoke, /\/api\/v1\/commands/, 'same-origin Command submission');
requireMatch(webSmoke, /SUCCEEDED/, 'Web smoke terminal status');
requireMatch(webSmoke, /S3_LOCAL_WEB_MAX_TERMINAL_MS/, 'configurable Web smoke terminal ceiling');
requireMatch(webSmoke, /terminalDurationMs/, 'Web smoke terminal latency evidence');
requireMatch(webSmoke, /commandPointID/, 'Web smoke canonical Command Point assertion');
requireMatch(webSmoke, /command\.parameters\?\.setpointC/, 'Web smoke canonical parameter projection');
requireMatch(webSmoke, /formalCertificationClaim:\s*false/, 'Web smoke claim denial');

requireMatch(readme, /http:\/\/127\.0\.0\.1:5173\/commands/, 'local Web URL documentation');
requireMatch(readme, /local-web-integration-passed/, 'local Web result documentation');
requireMatch(readme, /formalCertificationClaim: false/, 'certification boundary documentation');
requireMatch(readme, /No Ingress, NodePort, LoadBalancer/, 'public route denial documentation');

requireMatch(thingsBoardCompose, /thingsboard\/tb-node:4\.3\.1\.3/, 'pinned ThingsBoard CE image');
requireMatch(thingsBoardCompose, /127\.0\.0\.1:18080:8080/, 'loopback-only ThingsBoard UI');
for (const device of ['AHU-01', 'FCU-02', 'Chiller-03']) {
  requireMatch(thingsBoardProvisioner, new RegExp(device), `virtual Device ${device}`);
}
for (const deviceID of [
  '018f3e00-3000-7000-8000-000000000001',
  '018f3e00-3000-7000-8000-000000000002',
  '018f3e00-3000-7000-8000-000000000003',
]) {
  requireMatch(thingsBoardProvisioner, new RegExp(deviceID), `virtual Device ID ${deviceID}`);
}
requireMatch(thingsBoardProvisioner, /formalCertificationClaim:\s*false/, 'ThingsBoard provisioning claim denial');
requireMatch(thingsBoardRenderer, /command-dispatcher-\$\{device\.slug\}/, 'per-Device Dispatcher rendering');
requireMatch(thingsBoardRenderer, /command-verifier-\$\{device\.slug\}/, 'per-Device Verifier rendering');
requireMatch(thingsBoardManager, /127\.0\.0\.1:18080/, 'ThingsBoard manager loopback endpoint');
requireMatch(thingsBoardManager, /upgradeDeviceCatalog/, 'ThingsBoard Device catalog v2 upgrade');
requireMatch(thingsBoardManager, /commandPointIDs/, 'ThingsBoard canonical Command Point identities');
requireMatch(thingsBoardManager, /COMMAND_RUNTIME_COHORTS_FILE/, 'multi-Cohort Command Runtime wiring');
requireMatch(thingsBoardManager, /S3_LOCAL_WEB_MAX_TERMINAL_MS:\s*'15000'/, 'ThingsBoard 15-second terminal latency gate');
forbidMatch(thingsBoardProvisioner, /formalCertificationClaim:\s*true/, 'ThingsBoard formal certification claim');

requireMatch(commandServiceMain, /COMMAND_RUNTIME_COHORTS_FILE/, 'Command Service multi-Cohort file gate');
requireMatch(commandRuntime, /map\[string\]RuntimeCohort/, 'SPIFFE-bound Runtime Cohort map');
requireMatch(commandRuntime, /handler\.dispatchers\[identity\]/, 'Dispatcher SPIFFE Cohort selection');
requireMatch(commandRuntime, /handler\.verifiers\[identity\]/, 'Verifier SPIFFE Cohort selection');
requireMatch(commandRuntimeTests, /TestRuntimeHTTPSelectsExactMultiCohortByWorkloadIdentity/, 'multi-Cohort workload isolation test');

requireMatch(thingsBoardReadme, /http:\/\/127\.0\.0\.1:18080/, 'ThingsBoard UI documentation');
requireMatch(thingsBoardReadme, /http:\/\/127\.0\.0\.1:5173\/commands/, 'ThingsBoard HVAC Web documentation');
requireMatch(thingsBoardReadme, /127\.0\.0\.1:18081/, 'Gateway internal port documentation');
requireMatch(thingsBoardReadme, /15-second submit-to-`SUCCEEDED \/ VERIFIED` ceiling/, 'ThingsBoard local terminal latency documentation');
requireMatch(thingsBoardReadme, /terminalDurationMs/, 'ThingsBoard terminal latency evidence documentation');
requireMatch(thingsBoardReadme, /does not produce formal S3 certification evidence/, 'ThingsBoard certification boundary documentation');

console.log(`S3 local profile check passed: files=${requiredFiles.length}; localImages=${localImageCount}; virtualDevices=3; publicRoutes=0; web=port-forward-only; formalClaim=false`);
