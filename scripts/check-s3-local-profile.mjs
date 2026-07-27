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
  'services/command-service/cmd/s3-local-seed/main.go',
  'services/thingsboard-connector-control/cmd/s3-local-device-simulator/main.go',
  'services/thingsboard-connector-control/cmd/s3-local-device-simulator/main_test.go',
];

function read(path) {
  return readFileSync(resolve(root, path), 'utf8');
}

function requireMatch(value, pattern, label) {
  if (!pattern.test(value)) throw new Error(`${label} is missing`);
}

function forbidMatch(value, pattern, label) {
  if (pattern.test(value)) throw new Error(`${label} is forbidden`);
}

for (const path of requiredFiles) read(path);

const yamlPaths = requiredFiles.filter((path) => path.endsWith('.yaml'));
const yaml = yamlPaths.map((path) => read(path)).join('\n');
const launcher = read('scripts/s3-local.sh');
const readme = read('deploy/s3/local/README.md');
const simulator = read('services/thingsboard-connector-control/cmd/s3-local-device-simulator/main.go');
const seed = read('services/command-service/cmd/s3-local-seed/main.go');

requireMatch(yaml, /name:\s+s3-local\b/, 's3-local namespace');
requireMatch(yaml, /s3\.hvac\/formal-certification:\s+"false"/, 'local certification denial label');
requireMatch(yaml, /image:\s+hvac-s3-local\/command-service:dev/, 'local command-service image');
requireMatch(yaml, /image:\s+hvac-s3-local\/command-dispatcher:dev/, 'local command-dispatcher image');
requireMatch(yaml, /image:\s+hvac-s3-local\/command-verifier:dev/, 'local command-verifier image');
requireMatch(yaml, /image:\s+hvac-s3-local\/device-simulator:dev/, 'local simulator image');
requireMatch(yaml, /image:\s+hvac-s3-local\/command-seed:dev/, 'local seed image');
requireMatch(yaml, /image:\s+hvac-s3-local\/command-migrator:dev/, 'local migrator image');
requireMatch(yaml, /postgres:16\.4-bookworm@sha256:[0-9a-f]{64}/, 'pinned PostgreSQL image');

const localImageCount = (yaml.match(/image:\s+hvac-s3-local\//g) ?? []).length;
const neverPullCount = (yaml.match(/imagePullPolicy:\s+Never/g) ?? []).length;
if (localImageCount !== 6 || neverPullCount !== 6) {
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
requireMatch(launcher, /formalCertificationClaim": false/, 'local smoke claim denial');
requireMatch(launcher, /s3-local-smoke-\$\(date -u \+%s%N\)/, 'unique smoke idempotency key');
requireMatch(launcher, /kubectl apply -f "\$RENDERED\/seed-job\.yaml"/, 'rendered seed Job');
requireMatch(launcher, /openssl verify -CAfile/, 'local PKI verification');
requireMatch(launcher, /SUCCEEDED\\\|VERIFIED\\\|1\\\|s2:sha256:/, 'database smoke terminal state');
forbidMatch(launcher, /formalCertificationClaim": true/, 'formal smoke claim');

requireMatch(simulator, /tls\.VersionTLS13/, 'TLS 1.3 simulator minimum');
requireMatch(simulator, /tls\.RequireAndVerifyClientCert/, 'reported-state mTLS');
requireMatch(simulator, /spiffe:\/\/hvac\.local\/command-verifier/, 'verifier SPIFFE restriction');
requireMatch(simulator, /X-Authorization/, 'provider authorization check');
requireMatch(seed, /OpenPostgresStore/, 'production PostgreSQL store reuse');
requireMatch(seed, /ExpiresAt:\s+now\.Add\(25 \* time\.Second\)/, 'bounded local authorization lifetime');

requireMatch(readme, /local-integration-passed/, 'local result documentation');
requireMatch(readme, /formalCertificationClaim: false/, 'certification boundary documentation');
requireMatch(readme, /No Ingress, NodePort, LoadBalancer/, 'public route denial documentation');

console.log(`S3 local profile check passed: files=${requiredFiles.length}; localImages=${localImageCount}; publicRoutes=0; formalClaim=false`);
