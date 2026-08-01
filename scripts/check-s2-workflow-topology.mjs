import { readdir, readFile } from 'node:fs/promises';
import { extname, resolve } from 'node:path';

import { resolveCapabilityTask } from './domain-task-matrix.mjs';

const root = resolve(process.cwd());
const workflowsRoot = resolve(root, '.github', 'workflows');
const packageJSON = JSON.parse(await readFile(resolve(root, 'package.json'), 'utf8'));
const workflowFiles = await readdir(workflowsRoot);
const failures = [];

const assert = (condition, message) => {
  if (!condition) failures.push(message);
};

const capabilities = [
  ['s2-telemetry-baseline.yml', 's2:telemetry-baseline', 'out/s2-telemetry-baseline'],
  ['s2-iam-authorization.yml', 's2:iam-authorization', 'out/s2-iam-authorization'],
  ['s2-telemetry-runtime-snapshot.yml', 's2:telemetry-runtime-snapshot', 'out/s2-telemetry-runtime-snapshot'],
  ['s2-telemetry-ingest.yml', 's2:telemetry-ingest', 'out/s2-telemetry-ingest'],
  ['s2-gateway-snapshot.yml', 's2:gateway-snapshot', 'out/s2-gateway-snapshot'],
  ['s2-realtime-backend.yml', 's2:realtime-backend', 'out/s2-realtime-backend'],
  ['s2-telemetry-live-client.yml', 's2:telemetry-live-client', 'out/s2-telemetry-live-client'],
  ['s2-shadow-routing.yml', 's2:shadow-routing', 'out/s2-shadow-routing'],
  ['s2-hvac-web-presence.yml', 's2:hvac-web-presence', 'out/s2-hvac-web-presence'],
  ['s2-security-observability.yml', 's2:security-observability', 'out/s2-security-observability'],
  ['s2-telemetry-release.yml', 's2:telemetry-release', 'out/s2-telemetry-release'],
  ['s2-telemetry-cutover.yml', 's2:telemetry-cutover', 'out/s2-completion-evidence'],
];

const retiredEvidencePrefix = ['out/s2', 'ticket-'].join('-');
const governedExtensions = new Set(['.go', '.js', '.json', '.md', '.mjs', '.ts', '.tsx', '.yaml', '.yml']);
const governedRoots = ['.github/workflows', 'contracts', 'deploy/s2', 'docs/operations', 'libs', 'scripts', 'services'];

async function collectGovernedFiles(relativeDirectory) {
  const directory = resolve(root, relativeDirectory);
  const files = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const relativePath = `${relativeDirectory}/${entry.name}`;
    if (entry.isDirectory()) {
      files.push(...await collectGovernedFiles(relativePath));
    } else if (governedExtensions.has(extname(entry.name))) {
      files.push(relativePath);
    }
  }
  return files;
}

assert(
  packageJSON.scripts?.['s2:topology:check'] === 'node scripts/check-s2-workflow-topology.mjs',
  'stable s2:topology:check package command is missing or drifted',
);

for (const scriptName of Object.keys(packageJSON.scripts ?? {})) {
  assert(!/^s2:ticket-(?:0[1-9]|1[0-2])$/.test(scriptName), `retired S2 Ticket command must not return: ${scriptName}`);
}
assert(!JSON.stringify(packageJSON.scripts ?? {}).includes(retiredEvidencePrefix), 'package scripts still reference a retired S2 Ticket evidence directory');

for (const file of workflowFiles) {
  assert(!/^s2-ticket-/i.test(file), `Ticket-named S2 workflow must not return: ${file}`);
}

const startsWithSharedTopologyGate = (command, packageCommand) => {
  if (packageCommand?.startsWith('npm run s2:topology:check && ')) return true;
  const taskMatch = packageCommand?.match(/^node scripts\/run-capability-task\.mjs --task=([^\s]+)$/u);
  if (!taskMatch || taskMatch[1] !== command) return false;
  try {
    return resolveCapabilityTask(command)[0]?.label === 'npm run s2:topology:check';
  } catch {
    return false;
  }
};

for (const [file, command, evidenceDirectory] of capabilities) {
  assert(workflowFiles.includes(file), `stable S2 workflow is missing: ${file}`);
  const packageCommand = packageJSON.scripts?.[command];
  assert(typeof packageCommand === 'string', `stable S2 package command is missing: ${command}`);
  assert(
    startsWithSharedTopologyGate(command, packageCommand),
    `${command} must execute the shared S2 topology gate first`,
  );
  if (!workflowFiles.includes(file)) continue;

  const workflow = await readFile(resolve(workflowsRoot, file), 'utf8');
  assert(workflow.includes(`npm run ${command}`), `${file} does not invoke ${command}`);
  assert(workflow.includes(evidenceDirectory), `${file} does not publish or consume stable evidence under ${evidenceDirectory}`);
  assert(!workflow.includes('npm run s2:ticket-'), `${file} still invokes a Ticket command`);
  assert(!workflow.includes(retiredEvidencePrefix), `${file} still references a Ticket evidence directory`);
  assert(!workflow.includes('name: s2-ticket-'), `${file} still publishes a Ticket-named artifact`);
}

const releaseWorkflow = await readFile(resolve(workflowsRoot, 's2-telemetry-release.yml'), 'utf8');
assert(
  releaseWorkflow.includes('npm run s2:security-observability'),
  'S2 release certification does not use the stable security and observability command',
);

for (const relativePath of (await Promise.all(governedRoots.map(collectGovernedFiles))).flat()) {
  const source = await readFile(resolve(root, relativePath), 'utf8');
  assert(!source.includes(retiredEvidencePrefix), `${relativePath} still references a retired S2 Ticket evidence directory`);
}

if (failures.length > 0) {
  console.error(failures.map((failure) => `- ${failure}`).join('\n'));
  process.exit(1);
}

console.log(`S2 workflow topology passed: ${capabilities.length} stable capability commands, stable evidence directories, and no Ticket wrappers.`);
