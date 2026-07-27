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
  'scripts/s3-local.sh',
  'scripts/s3-local-web.mjs',
  'scripts/run-s3-local-web-smoke.mjs',
  'apps/hvac-web/src/api/commands.ts',
  'apps/hvac-web/src/pages/Commands/index.tsx',
  'services/command-service/cmd/s3-local-seed/main.go',
  'services/platform-gateway/cmd/s3-local-web-gateway/main.go',
  'services/platform-gateway/cmd/s3-local-web-gateway/main_test.go',
  'services/thingsboard-connector-control/cmd/s3-local-device-simulator/main.go',
  'services/thingsboard-connector-control/cmd/s3-local-device-simulator/main_test.go',
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
const simulator = read('services/thingsboard-connector-control/cmd/s3-local-device-simulator/main.go');
const seed = read('services/command-service/cmd/s3-local-seed/main.go');
const localGateway = read('services/platform-gateway/cmd/s3-local-web-gateway/main.go');
const localGatewayTests = read('services/platform-gateway/cmd/s3-local-web-gateway/main_test.go');
const commandApi = read('apps/hvac-web/src/api/commands.ts');
const commandPage = read('apps/hvac-web/src/pages/Commands/index.tsx');

requireMatch(yaml, /name:\s+s3-local\b/, 's3-local namespace');
requireMatch(yaml, /s3\.hvac\/formal-certification:\s+"false"/, 'local certification denial label');
for (const image of ['command-service', 'command-dispatcher', 'command-verifier', 'device-simulator', 'command-seed', 'command-migrator', 'web-gateway']) {
  requireMatch(yaml, new RegExp(`image:\\s+hvac-s3-local\\/${image}:dev`), `local ${image} image`);
}
requireMatch(yaml, /postgres:16\.4-bookworm@sha256:[0-9a-f]{64}/, 'pinned PostgreSQL image');
requireMatch(yaml, /name:\s+s3-local-web-gateway[\s\S]*?type:\s+ClusterIP/, 'local Web Gateway ClusterIP');
requireMatch(yaml, /s3\.hvac\/access:\s+kubectl-port-forward-only/, 'port-forward-only access marker');

const localImageCount = (yaml.match(/image:\s+hvac-s3-local\//g) ?? []).length;
const neverPullCount = (yaml.match(/imagePullPolicy:\s+Never/g) ?? []).length;
if (localImageCount !== 7 || neverPullCount !== 7) {
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
requireMatch(launcher, /issue_certificate platform-gateway/, 'Gateway workload certificate');
requireMatch(launcher, /web-csrf-token/, 'random local Web CSRF input');
requireMatch(launcher, /s3-local-web-gateway-pki/, 'generated Web Gateway projected values');
requireMatch(launcher, /formalCertificationClaim": false/, 'local smoke claim denial');
requireMatch(launcher, /s3-local-smoke-\$\(date -u \+%s%N\)/, 'unique smoke idempotency key');
requireMatch(launcher, /SUCCEEDED\\\|VERIFIED\\\|1\\\|s2:sha256:/, 'database smoke terminal state');
forbidMatch(launcher, /formalCertificationClaim": true/, 'formal smoke claim');

requireMatch(simulator, /tls\.VersionTLS13/, 'TLS 1.3 simulator minimum');
requireMatch(simulator, /tls\.RequireAndVerifyClientCert/, 'reported-state mTLS');
requireMatch(simulator, /spiffe:\/\/hvac\.local\/command-verifier/, 'verifier SPIFFE restriction');
requireMatch(seed, /OpenPostgresStore/, 'production PostgreSQL store reuse');
requireMatch(seed, /ExpiresAt:\s+now\.Add\(25 \* time\.Second\)/, 'bounded local authorization lifetime');

for (const token of ['tls.VersionTLS13', 'X-Command-Grant', 'X-Command-Read-Context', 'X-CSRF-Token', 'spiffe://hvac.local/platform-gateway', 'formal_certification_claim", false']) {
  requireMatch(localGateway, new RegExp(token.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), `local Gateway invariant ${token}`);
}
for (const test of ['TestLocalWebGatewayCreatesExactShortLivedCommandGrant', 'TestLocalWebGatewayReadUsesExactDelegation', 'TestLocalWebGatewayRejectsMutationBeforeUpstream']) {
  requireMatch(localGatewayTests, new RegExp(test), `local Gateway test ${test}`);
}

requireMatch(commandApi, /COMMAND_PUBLIC_ROUTES_ENABLED = false as const/, 'production Command route denial');
requireMatch(commandApi, /import\.meta\.env\.DEV/, 'development-only local Command gate');
requireMatch(commandApi, /VITE_S3_LOCAL_COMMANDS/, 'explicit local Command flag');
requireMatch(commandPage, /LOCAL \/ KIND/, 'local Web environment label');
requireMatch(commandPage, /refetchInterval/, 'Command status polling');
requireMatch(commandPage, /\? 1000 : false/, 'one-second non-terminal polling');

requireMatch(webManager, /kubectl[\s\S]*port-forward/, 'Gateway port-forward manager');
requireMatch(webManager, /VITE_API_MODE:\s*'real'/, 'real frontend API mode');
requireMatch(webManager, /VITE_S3_LOCAL_COMMANDS:\s*'true'/, 'local frontend Command flag');
requireMatch(webManager, /formalCertificationClaim:\s*false/, 'Web process claim denial');
requireMatch(webSmoke, /\/api\/v1\/principal/, 'same-origin principal preflight');
requireMatch(webSmoke, /\/api\/v1\/commands/, 'same-origin Command submission');
requireMatch(webSmoke, /SUCCEEDED/, 'Web smoke terminal status');
requireMatch(webSmoke, /formalCertificationClaim:\s*false/, 'Web smoke claim denial');

requireMatch(readme, /http:\/\/127\.0\.0\.1:5173\/commands/, 'local Web URL documentation');
requireMatch(readme, /local-web-integration-passed/, 'local Web result documentation');
requireMatch(readme, /formalCertificationClaim: false/, 'certification boundary documentation');
requireMatch(readme, /No Ingress, NodePort, LoadBalancer/, 'public route denial documentation');

console.log(`S3 local profile check passed: files=${requiredFiles.length}; localImages=${localImageCount}; publicRoutes=0; web=port-forward-only; formalClaim=false`);
