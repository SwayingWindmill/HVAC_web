import { readdir, readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const workflowsDir = resolve(root, '.github/workflows');
const packageJSON = JSON.parse(await readFile(resolve(root, 'package.json'), 'utf8'));
const workflowFiles = await readdir(workflowsDir);

const failures = [];
const assert = (condition, message) => {
  if (!condition) failures.push(message);
};

assert(packageJSON.scripts?.['s1:topology:check'] === 'node scripts/check-s1-workflow-topology.mjs', 'stable s1:topology:check package command is missing or drifted');

const capabilities = [
  { file: 's1-iam-provider-poc.yml', name: 'S1 IAM Provider POC', command: 's1:iam-provider-poc' },
];

for (const file of workflowFiles) {
  assert(!/^s1-ticket-.*\.yml$/.test(file), `retired S1 Ticket workflow must not return: ${file}`);
}
for (let ticket = 1; ticket <= 6; ticket += 1) {
  const suffix = String(ticket).padStart(2, '0');
  assert(!packageJSON.scripts?.[`s1:ticket-${suffix}`], `retired s1:ticket-${suffix} package command must not return`);
}

for (const capability of capabilities) {
  assert(workflowFiles.includes(capability.file), `stable workflow is missing: ${capability.file}`);
  assert(packageJSON.scripts?.[capability.command], `stable package command is missing: ${capability.command}`);
  if (!workflowFiles.includes(capability.file)) continue;

  const workflow = await readFile(resolve(workflowsDir, capability.file), 'utf8');
  const selfTrigger = `- '.github/workflows/${capability.file}'`;
  assert(workflow.includes(`name: ${capability.name}`), `${capability.file} has the wrong stable name`);
  assert(workflow.split(selfTrigger).length - 1 === 2, `${capability.file} must watch itself for pull requests and main pushes`);
  assert(workflow.includes('scripts/check-s1-workflow-topology.mjs'), `${capability.file} does not watch the shared S1 topology gate`);
  assert(workflow.includes('npm run s1:topology:check'), `${capability.file} does not execute the shared S1 topology gate`);
  assert(workflow.includes(`npm run ${capability.command}`), `${capability.file} does not invoke ${capability.command}`);
  assert(!workflow.includes('s1-ticket-'), `${capability.file} still contains Ticket workflow topology`);
}


const registryRunner = await readFile(resolve(root, 'scripts/run-s1-registry-postgres-tests.mjs'), 'utf8');
assert(registryRunner.includes('out/s1-registry-core/postgres-baseline.json'), 'Registry Core default PostgreSQL evidence path is not stable');
assert(!registryRunner.includes('out/s1-ticket-01'), 'Registry PostgreSQL runner still uses Ticket evidence topology');
const logtoRunner = await readFile(resolve(root, 'scripts/run-s1-logto-sdk-adoption-poc.mjs'), 'utf8');
assert(logtoRunner.includes('out/s1-iam-provider-poc/logto-comparison.json'), 'IAM Provider comparison evidence path is not stable');
assert(logtoRunner.includes('out/s1-iam-provider-poc/logto-sdk-adoption.json'), 'IAM Provider SDK evidence path is not stable');
const browserRunner = await readFile(resolve(root, 'scripts/run-s1-hvac-web-registry-browser-audit.mjs'), 'utf8');
assert(browserRunner.includes('out/s1-registry-web/hvac-web-registry-browser.json'), 'Registry Web browser evidence path is not stable');
const s2IamRunner = await readFile(resolve(root, 'scripts/run-s2-iam-postgres-tests.mjs'), 'utf8');
assert(s2IamRunner.includes('out/s1-registry-core/postgres-baseline.json'), 'S2 IAM no longer consumes stable Registry Core evidence');
assert(!s2IamRunner.includes('out/s1-ticket-01'), 'S2 IAM still consumes Ticket-scoped Registry evidence');

if (failures.length > 0) {
  console.error(failures.map((failure) => `- ${failure}`).join('\n'));
  process.exit(1);
}

console.log(`S1 workflow topology passed: ${capabilities.length} stable capability workflows and no Ticket wrappers.`);
