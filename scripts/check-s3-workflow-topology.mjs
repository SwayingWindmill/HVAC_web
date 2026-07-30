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

assert(packageJSON.scripts?.['s3:topology:check'] === 'node scripts/check-s3-workflow-topology.mjs', 'stable s3:topology:check package command is missing or drifted');

const capabilities = [
  {
    file: 's3-command-safety.yml',
    name: 'S3 Command Safety',
    command: 's3:command-safety',
  },
  {
    file: 's3-command-authority.yml',
    name: 'S3 Command Authority',
    command: 's3:command-authority',
  },
  {
    file: 's3-command-api.yml',
    name: 'S3 Command API',
    command: 's3:command-api',
  },
  {
    file: 's3-thingsboard-contract.yml',
    name: 'S3 ThingsBoard Contract',
    command: 's3:thingsboard-contract',
  },
  {
    file: 's3-command-ux.yml',
    name: 'S3 Command UX',
    command: 's3:command-ux',
  },
  {
    file: 's3-command-certification.yml',
    name: 'S3 Command Certification',
    command: 's3:certification:pr',
  },
];

for (let ticket = 1; ticket <= 9; ticket += 1) {
  const suffix = String(ticket).padStart(2, '0');
  assert(!workflowFiles.includes(`s3-ticket-${suffix}.yml`), `retired S3 Ticket ${suffix} workflow must not return`);
  assert(!packageJSON.scripts?.[`s3:ticket-${suffix}`], `retired s3:ticket-${suffix} package command must not return`);
}

for (const capability of capabilities) {
  assert(workflowFiles.includes(capability.file), `stable workflow is missing: ${capability.file}`);
  assert(packageJSON.scripts?.[capability.command], `stable package command is missing: ${capability.command}`);
  if (!workflowFiles.includes(capability.file)) continue;

  const workflow = await readFile(resolve(workflowsDir, capability.file), 'utf8');
  const selfPath = `.github/workflows/${capability.file}`;
  const selfTrigger = `- '${selfPath}'`;
  assert(workflow.includes(`name: ${capability.name}`), `${capability.file} has the wrong stable name`);
  assert(workflow.split(selfTrigger).length - 1 === 2, `${capability.file} must watch itself for pull requests and main pushes`);
  assert(workflow.includes(`npm run ${capability.command}`), `${capability.file} does not invoke ${capability.command}`);
  assert(workflow.includes('scripts/check-s3-workflow-topology.mjs'), `${capability.file} does not watch the shared S3 topology gate`);
  assert(workflow.includes('npm run s3:topology:check'), `${capability.file} does not execute the shared S3 topology gate`);
  assert(!workflow.includes('s3-ticket-'), `${capability.file} still contains Ticket workflow topology`);
}

const authority = packageJSON.scripts?.['s3:command-authority'] ?? '';
for (const marker of [
  's3:postgres:check',
  's3:governance-dispatch:check',
  's3:verification:check',
  'ownership:check',
  's3:postgres',
  './libs/commandauth/...',
  './libs/commandmodel/...',
  './services/command-service/...',
  './services/command-dispatcher/...',
  './services/thingsboard-connector-control/...',
  'npm run lint',
  'npm run build',
]) {
  assert(authority.includes(marker), `s3:command-authority is missing ${marker}`);
}

const certification = await readFile(resolve(workflowsDir, 's3-command-certification.yml'), 'utf8');
assert(certification.includes("tags: ['s3-v*']"), 'S3 certification no longer supports release-candidate tags');
assert(certification.includes("if: startsWith(github.ref, 'refs/tags/s3-v') || github.event_name == 'workflow_dispatch'"), 'signed target images are not restricted to tags or manual runs');
assert(certification.includes('needs: [certification-preflight]'), 'signed image publication does not depend on certification preflight');
assert(certification.includes('needs: [signed-target-images]'), 'image manifest does not depend on signed images');

const thingsBoardRunner = await readFile(resolve(root, 'scripts/run-s3-thingsboard-local-tests.mjs'), 'utf8');
assert(thingsBoardRunner.includes('out/s3-thingsboard-contract/thingsboard-local-contract.json'), 'ThingsBoard evidence path is not capability-scoped');
assert(!thingsBoardRunner.includes('out/s3-ticket-06'), 'ThingsBoard runner still uses Ticket evidence topology');

if (failures.length > 0) {
  console.error(failures.map((failure) => `- ${failure}`).join('\n'));
  process.exit(1);
}

console.log(`S3 workflow topology passed: ${capabilities.length} stable capability workflows and no Ticket wrappers.`);
