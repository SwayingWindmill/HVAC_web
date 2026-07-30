import { readdir, readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const workflowsRoot = resolve(root, '.github', 'workflows');
const packageJSON = JSON.parse(await readFile(resolve(root, 'package.json'), 'utf8'));
const workflowFiles = await readdir(workflowsRoot);
const failures = [];

const assert = (condition, message) => {
  if (!condition) failures.push(message);
};

const capabilities = [
  ['s2-telemetry-baseline.yml', 's2:telemetry-baseline'],
  ['s2-iam-authorization.yml', 's2:iam-authorization'],
  ['s2-telemetry-runtime-snapshot.yml', 's2:telemetry-runtime-snapshot'],
  ['s2-telemetry-ingest.yml', 's2:telemetry-ingest'],
  ['s2-gateway-snapshot.yml', 's2:gateway-snapshot'],
  ['s2-realtime-backend.yml', 's2:realtime-backend'],
  ['s2-telemetry-live-client.yml', 's2:telemetry-live-client'],
  ['s2-shadow-routing.yml', 's2:shadow-routing'],
  ['s2-hvac-web-presence.yml', 's2:hvac-web-presence'],
  ['s2-security-observability.yml', 's2:security-observability'],
  ['s2-telemetry-release.yml', 's2:telemetry-release'],
  ['s2-telemetry-cutover.yml', 's2:telemetry-cutover'],
];

assert(
  packageJSON.scripts?.['s2:topology:check'] === 'node scripts/check-s2-workflow-topology.mjs',
  'stable s2:topology:check package command is missing or drifted',
);

for (const scriptName of Object.keys(packageJSON.scripts ?? {})) {
  assert(!/^s2:ticket-(?:0[1-9]|1[0-2])$/.test(scriptName), `retired S2 Ticket command must not return: ${scriptName}`);
}

for (const file of workflowFiles) {
  assert(!/^s2-ticket-/i.test(file), `Ticket-named S2 workflow must not return: ${file}`);
}

for (const [file, command] of capabilities) {
  assert(workflowFiles.includes(file), `stable S2 workflow is missing: ${file}`);
  const packageCommand = packageJSON.scripts?.[command];
  assert(typeof packageCommand === 'string', `stable S2 package command is missing: ${command}`);
  assert(
    packageCommand?.startsWith('npm run s2:topology:check && '),
    `${command} must execute the shared S2 topology gate first`,
  );
  if (!workflowFiles.includes(file)) continue;

  const workflow = await readFile(resolve(workflowsRoot, file), 'utf8');
  assert(workflow.includes(`npm run ${command}`), `${file} does not invoke ${command}`);
  assert(!workflow.includes('npm run s2:ticket-'), `${file} still invokes a Ticket command`);
}

const releaseWorkflow = await readFile(resolve(workflowsRoot, 's2-telemetry-release.yml'), 'utf8');
assert(
  releaseWorkflow.includes('npm run s2:security-observability'),
  'S2 release certification does not use the stable security and observability command',
);

if (failures.length > 0) {
  console.error(failures.map((failure) => `- ${failure}`).join('\n'));
  process.exit(1);
}

console.log(`S2 workflow topology passed: ${capabilities.length} stable capability commands and no Ticket command wrappers.`);
