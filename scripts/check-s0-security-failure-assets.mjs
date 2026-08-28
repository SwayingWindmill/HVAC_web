import { access, readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const assert = (condition, message) => {
  if (!condition) throw new Error(message);
};
const includesAll = (text, markers, label) => {
  for (const marker of markers) assert(text.includes(marker), `${label} is missing ${marker}`);
};

const matrix = JSON.parse(await read('tests/s0-security/security-failure-matrix.json'));
assert(matrix.schemaVersion === 1, 'security failure matrix schemaVersion must be 1');
assert(matrix.ticket === '07-security-and-failure-gates', 'security failure matrix is assigned to the wrong ticket');
assert(matrix.fixtures?.organizations?.length === 2, 'security fixtures require exactly two Organizations');
assert(new Set(matrix.fixtures.organizations.map((organization) => organization.id)).size === 2, 'security fixture Organization IDs must be distinct');
assert(matrix.fixtures.organizations.every((organization) => organization.users?.length >= 2), 'each Organization requires authorized and administrative identities');
assert(matrix.fixtures?.serviceIdentities?.length >= 4, 'security fixtures require distinct service identities');
assert(new Set(matrix.fixtures.serviceIdentities).size === matrix.fixtures.serviceIdentities.length, 'service identities must be distinct');
assert(matrix.cases?.length === 14, 'Ticket 07 requires fourteen mapped acceptance cases');
assert(new Set(matrix.cases.map((entry) => entry.id)).size === matrix.cases.length, 'security matrix case IDs must be unique');
for (const entry of matrix.cases) {
  assert(entry.requirement && entry.evidence?.length, `${entry.id} requires a requirement and evidence`);
  for (const path of entry.evidence) await access(resolve(root, path));
}

const upstream = new Map((matrix.upstream || []).map((entry) => [entry.project, entry]));
for (const [project, version, license] of [
  ['Shopify/toxiproxy', 'v2.12.0', 'MIT'],
  ['np-guard/netpol-analyzer', 'v1.4.4', 'Apache-2.0'],
  ['aquasecurity/trivy-action', 'v0.36.0', 'Apache-2.0'],
]) {
  const entry = upstream.get(project);
  assert(entry?.version === version && entry?.license === license, `${project} must be pinned to ${version} with ${license}`);
}
assert(upstream.get('Shopify/toxiproxy')?.digest === 'sha256:9378ed52a28bc50edc1350f936f518f31fa95f0d15917d6eb40b8e376d1a214e', 'Toxiproxy image must be pinned to the reviewed OCI digest');
assert(upstream.get('aquasecurity/trivy-action')?.commit === 'ed142fd0673e97e23eac54620cfb913e5ce36c25', 'Trivy action must be pinned to the reviewed commit');

const agents = await read('AGENTS.md');
includesAll(agents, ['Reuse-first implementation', 'search GitHub', 'pin the selected version or commit'], 'reuse-first repository policy');
const compose = await read('infra/durability/compose.yaml');
includesAll(compose, ['ghcr.io/shopify/toxiproxy:2.12.0@sha256:9378ed52a28bc50edc1350f936f518f31fa95f0d15917d6eb40b8e376d1a214e', 'S0_TOXIPROXY_POSTGRES_HOST_PORT'], 'Toxiproxy topology');
assert(!compose.includes('S0_TOXIPROXY_LEGACY_HOST_PORT'), 'active Toxiproxy topology must not expose a Legacy port');
const topology = await read('scripts/s0-durable-topology.mjs');
includesAll(topology, ['setPostgresAvailable', 'setPlatformStatusRevision', 'killProcess', 'serviceProcessGroups', "process.kill(-child.pid, signal)", "detached: process.platform !== 'win32'", 's0_postgres'], 'failure-injection topology');
assert(!topology.toLowerCase().includes('legacy'), 'active durable topology must not start or configure Legacy');
const browserAudit = await read('scripts/run-durable-session-browser-audit.mjs');
includesAll(browserAudit, ['ROUTE_AUDIT_FAILED', 'setPlatformStatusRevision', 'staleRevisionRejected', 'stopAudit(true)', 'stopRelay(true)', 'Outbox backlog', 'AUDIT_RECORD_NOT_FOUND'], 'production-shaped failure matrix');
assert(!browserAudit.toLowerCase().includes('legacy'), 'active browser audit must be Go-only');
const networkGate = await read('scripts/check-s0-network-policies.mjs');
includesAll(networkGate, ['netpol-analyzer', 'v1.4.4', '169.254.169.254', '10.0.0.1', 'browser-to-iam-denied', 'gateway-to-postgres-allowed'], 'NetworkPolicy gate');
const workflow = await read('.github/workflows/s0-supply-chain.yml');
includesAll(workflow, ['aquasecurity/trivy-action@ed142fd0673e97e23eac54620cfb913e5ce36c25', 'scanners: secret', 'security-failure-gates', 's0-security-gate-results'], 'CI security release gate');
const identity = await read('cmd/energy-api/internal/gateway/identity.go');
includesAll(identity, ['targetSession.TenantID != adminSession.TenantID', 'SESSION_NOT_FOUND'], 'cross-Tenant session revocation guard');
const identityTests = await read('cmd/energy-api/internal/gateway/auth_integration_test.go');
includesAll(identityTests, ['TestCrossTenantAdminCannotRevokeSession', 'admin-other-tenant'], 'cross-Tenant session tests');
const publicContract = await read('contracts/http/platform-gateway.openapi.yaml');
for (const forbiddenPath of ['/api/v1/routes/diagnostics', '/api/v1/route-diagnostics']) {
  assert(!publicContract.includes(forbiddenPath), `public Gateway contract must not expose ${forbiddenPath}`);
}
const docs = await read('docs/operations/s0-security-failure-gates.md');
for (const heading of ['## Reuse decisions', '## Release gate', '## Failure matrix', '## Evidence']) {
  assert(docs.includes(heading), `security failure documentation is missing ${heading}`);
}

console.log('S0 security, tenant and failure-injection assets are internally consistent.');
