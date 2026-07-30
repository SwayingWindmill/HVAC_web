import { readdir, readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const workflowsDir = resolve(root, '.github/workflows');
const workflowFiles = await readdir(workflowsDir);
const packageJSON = JSON.parse(await readFile(resolve(root, 'package.json'), 'utf8'));

const failures = [];
const assert = (condition, message) => {
  if (!condition) failures.push(message);
};

assert(
  packageJSON.scripts?.['rms:topology:check'] === 'node scripts/check-rms-workflow-topology.mjs',
  'stable rms:topology:check package command is missing or drifted',
);

const capabilities = [
  { file: 'rms-web-build.yml', name: 'RMS Web Build', command: 'rms:web-build' },
  { file: 'rms-web-auth.yml', name: 'RMS Web Auth', command: 'rms:web-auth' },
  { file: 'rms-web-routing.yml', name: 'RMS Web Routing', command: 'rms:web-routing' },
  { file: 'rms-web-browser.yml', name: 'RMS Web Browser', command: 'rms:web-browser' },
  { file: 'rms-web-certification.yml', name: 'RMS Web Certification', command: 'rms:web-certification' },
];

const retiredWorkflow = (...segments) => `${segments.join('-')}.yml`;
const retiredWorkflows = [
  retiredWorkflow('rms', 'ticket', '01'),
  retiredWorkflow('rms', '02', 'effective', 'capabilities'),
  retiredWorkflow('rms', '03', 'authenticated', 'shell'),
  retiredWorkflow('rms', '04', 'capability', 'routes'),
  retiredWorkflow('rms', '05', 'authorized', 'site', 'scope'),
  retiredWorkflow('rms', '06', 'protected', 'scope', 'purge'),
  retiredWorkflow('rms', '07', 'trusted', 'shell', 'chrome'),
  retiredWorkflow('rms', '08', 'real', 'shell', 'certification'),
];
for (const file of retiredWorkflows) {
  assert(!workflowFiles.includes(file), `retired RMS workflow must not return: ${file}`);
}
for (let ticket = 1; ticket <= 8; ticket += 1) {
  const suffix = String(ticket).padStart(2, '0');
  assert(!packageJSON.scripts?.[`rms:ticket-${suffix}`], `retired rms:ticket-${suffix} command must not return`);
  assert(!packageJSON.scripts?.[`rms:ticket-${suffix}:browser`], `retired rms:ticket-${suffix}:browser command must not return`);
}

for (const capability of capabilities) {
  assert(workflowFiles.includes(capability.file), `stable RMS workflow is missing: ${capability.file}`);
  assert(packageJSON.scripts?.[capability.command], `stable RMS package command is missing: ${capability.command}`);
  if (!workflowFiles.includes(capability.file)) continue;

  const workflow = await readFile(resolve(workflowsDir, capability.file), 'utf8');
  const selfTrigger = `- '.github/workflows/${capability.file}'`;
  const topologyTrigger = "- 'scripts/check-rms-workflow-topology.mjs'";
  assert(workflow.includes(`name: ${capability.name}`), `${capability.file} has the wrong stable name`);
  assert(workflow.split(selfTrigger).length - 1 === 2, `${capability.file} must watch itself for pull requests and main pushes`);
  assert(workflow.split(topologyTrigger).length - 1 === 2, `${capability.file} must watch the shared topology gate for pull requests and main pushes`);
  assert(workflow.includes('npm run rms:topology:check'), `${capability.file} does not execute the shared RMS topology gate`);
  assert(workflow.includes(`npm run ${capability.command}`), `${capability.file} does not invoke ${capability.command}`);
  assert(!workflow.includes('rms:ticket-'), `${capability.file} still invokes a Ticket command`);
  assert(!workflow.includes('rms-ticket-'), `${capability.file} still contains Ticket workflow topology`);
}

const buildCommand = packageJSON.scripts?.['rms:web-build'] ?? '';
for (const marker of ['test:rms-real-build-audit', 'rms:real:graph', 'build:real', 'build:demo', 'rms:real:bundle']) {
  assert(buildCommand.includes(marker), `rms:web-build is missing ${marker}`);
}

const authCommand = packageJSON.scripts?.['rms:web-auth'] ?? '';
for (const marker of ['contracts:check', 'rms:principal-capabilities:contract', 'test:identity', 'libs/identitycontext', 'services/iam-service', 'services/platform-gateway', 'build:iam', 'build:gateway', 'npm run lint', 'npm run build']) {
  assert(authCommand.includes(marker), `rms:web-auth is missing ${marker}`);
}

const routingCommand = packageJSON.scripts?.['rms:web-routing'] ?? '';
for (const marker of ['contracts:check', 'rms:trusted-shell:test', 'npm run lint', 'rms:real:graph', 'build:real', 'build:demo', 'rms:real:bundle']) {
  assert(routingCommand.includes(marker), `rms:web-routing is missing ${marker}`);
}
for (const testFile of [
  'scripts/test-rms-authenticated-shell-policy.mjs',
  'scripts/test-rms-feature-route-policy.mjs',
  'scripts/test-rms-site-routing.mjs',
  'scripts/test-rms-protected-scope.mjs',
  'scripts/test-rms-realtime-status.mjs',
  'scripts/test-rms-shell-runtime.mjs',
]) {
  assert((packageJSON.scripts?.['rms:trusted-shell:test'] ?? '').includes(testFile), `rms:trusted-shell:test is missing ${testFile}`);
}

const browserCommand = packageJSON.scripts?.['rms:web-browser'] ?? '';
assert(browserCommand.includes('rms:web-auth:browser'), 'rms:web-browser is missing the Principal capability audit');
assert(browserCommand.includes('rms:web-routing:browser'), 'rms:web-browser is missing the trusted Shell audit');
assert(
  packageJSON.scripts?.['rms:web-auth:browser'] === 'node scripts/run-rms-principal-capability-browser-audit.mjs',
  'RMS Web Auth browser command drifted',
);
assert(
  packageJSON.scripts?.['rms:web-routing:browser'] === 'node scripts/run-rms-authenticated-shell-browser-audit.mjs',
  'RMS Web Routing browser command drifted',
);

assert(packageJSON.scripts?.['rms:web-certification'] === 'npm run rms:certify', 'RMS Web Certification no longer owns rms:certify');
const certificationRunner = await readFile(resolve(root, 'scripts/run-rms-real-shell-certification.mjs'), 'utf8');
assert(certificationRunner.includes("out', 'rms-web-certification"), 'RMS certification runner does not use the stable evidence directory');
assert(certificationRunner.includes("npmGate('browser', 'rms:web-routing:browser')"), 'RMS certification does not use the stable Shell browser command');
assert(!certificationRunner.includes("out', 'rms-08"), 'RMS certification runner still uses Ticket-scoped evidence');
assert(!certificationRunner.includes('rms:ticket-'), 'RMS certification runner still invokes a Ticket command');

const graphRunner = await readFile(resolve(root, 'scripts/check-rms-real-build-graph.mjs'), 'utf8');
const bundleRunner = await readFile(resolve(root, 'scripts/check-rms-real-bundle.mjs'), 'utf8');
const evidenceBuilder = await readFile(resolve(root, 'scripts/build-rms-real-shell-certification.mjs'), 'utf8');
const browserRunner = await readFile(resolve(root, 'scripts/run-rms-authenticated-shell-browser-audit.mjs'), 'utf8');
for (const [label, content] of [
  ['build graph', graphRunner],
  ['bundle', bundleRunner],
  ['certification builder', evidenceBuilder],
]) {
  assert(content.includes('rms-web-build'), `RMS ${label} does not use the stable build evidence directory`);
  assert(!content.includes('rms-01'), `RMS ${label} still uses Ticket 01 evidence topology`);
}
assert(evidenceBuilder.includes('rms-web-certification'), 'RMS certification builder does not use the stable certification evidence directory');
assert(browserRunner.includes('rms-web-certification'), 'RMS browser audit does not use the stable certification evidence directory');
assert(!evidenceBuilder.includes('rms-08'), 'RMS certification builder still uses Ticket 08 evidence topology');
assert(!browserRunner.includes("'rms-08'"), 'RMS browser audit still uses Ticket 08 evidence topology');

const securityWorkflow = await readFile(resolve(workflowsDir, 'security-79-react-router.yml'), 'utf8');
assert(
  packageJSON.scripts?.['security:ticket-79:browser']?.includes('rms:web-routing:browser'),
  'Security 79 does not use the stable RMS Shell browser command',
);
assert(securityWorkflow.includes('out/rms-web-certification/browser-evidence.json'), 'Security 79 does not upload stable RMS browser evidence');
assert(!securityWorkflow.includes('out/rms-08/browser-evidence.json'), 'Security 79 still uploads Ticket-scoped RMS browser evidence');

if (failures.length > 0) {
  console.error(failures.map((failure) => `- ${failure}`).join('\n'));
  process.exit(1);
}

console.log(`RMS workflow topology passed: ${capabilities.length} stable capability workflows and no Ticket wrappers.`);
