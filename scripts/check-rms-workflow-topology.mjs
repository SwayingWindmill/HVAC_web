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

for (const retired of ['rms-web-auth.yml', 'rms-web-build.yml', 'rms-web-routing.yml']) {
  assert(!workflowFiles.includes(retired), `retired RMS daily workflow must not return: ${retired}`);
}
for (let ticket = 1; ticket <= 8; ticket += 1) {
  const suffix = String(ticket).padStart(2, '0');
  assert(!workflowFiles.includes(`rms-ticket-${suffix}.yml`), `retired RMS Ticket workflow must not return: rms-ticket-${suffix}.yml`);
  assert(!packageJSON.scripts?.[`rms:ticket-${suffix}`], `retired rms:ticket-${suffix} command must not return`);
  assert(!packageJSON.scripts?.[`rms:ticket-${suffix}:browser`], `retired rms:ticket-${suffix}:browser command must not return`);
}
for (const retired of [
  'rms-02-effective-capabilities.yml',
  'rms-03-authenticated-shell.yml',
  'rms-04-capability-routes.yml',
  'rms-05-authorized-site-scope.yml',
  'rms-06-protected-scope-purge.yml',
  'rms-07-trusted-shell-chrome.yml',
  'rms-08-real-shell-certification.yml',
]) {
  assert(!workflowFiles.includes(retired), `retired RMS workflow must not return: ${retired}`);
}

for (const [file, name, command] of [
  ['rms-web-browser.yml', 'RMS Web Browser', 'rms:web-browser'],
  ['rms-web-certification.yml', 'RMS Web Certification', 'rms:web-certification'],
]) {
  assert(workflowFiles.includes(file), `stable RMS workflow is missing: ${file}`);
  assert(typeof packageJSON.scripts?.[command] === 'string', `stable RMS command is missing: ${command}`);
  if (!workflowFiles.includes(file)) continue;
  const workflow = await readFile(resolve(workflowsDir, file), 'utf8');
  assert(workflow.includes(`name: ${name}`), `${file} has the wrong stable name`);
  assert(workflow.includes(`npm run ${command}`), `${file} does not invoke ${command}`);
  assert(!workflow.includes('rms:ticket-') && !workflow.includes('rms-ticket-'), `${file} still contains Ticket topology`);
}

if (workflowFiles.includes('rms-web-browser.yml')) {
  const browserWorkflow = await readFile(resolve(workflowsDir, 'rms-web-browser.yml'), 'utf8');
  assert(browserWorkflow.includes('runs-on: windows-latest'), 'RMS Browser must remain a Windows real-browser workflow');
  assert(browserWorkflow.includes("'modules/iam/**'") && browserWorkflow.includes("'cmd/energy-api/**'"), 'RMS Browser must watch current IAM and energy-api source roots');
  assert(!browserWorkflow.includes('services/iam-service') && !browserWorkflow.includes('services/platform-gateway'), 'RMS Browser still watches retired service roots');
}

if (workflowFiles.includes('rms-web-certification.yml')) {
  const certificationWorkflow = await readFile(resolve(workflowsDir, 'rms-web-certification.yml'), 'utf8');
  assert(certificationWorkflow.includes('workflow_dispatch:'), 'RMS Certification must remain explicitly dispatchable');
  assert(!/^\s{2}(pull_request|push):/m.test(certificationWorkflow), 'RMS Certification must not run automatically on PR or push');
  assert(certificationWorkflow.includes('modules/iam/go.mod') && certificationWorkflow.includes('cmd/energy-api/go.mod'), 'RMS Certification must use current IAM and energy-api modules');
  assert(!certificationWorkflow.includes('services/iam-service') && !certificationWorkflow.includes('services/platform-gateway'), 'RMS Certification still references retired service roots');
}

const certificationRunner = await readFile(resolve(root, 'scripts/run-rms-real-shell-certification.mjs'), 'utf8');
assert(certificationRunner.includes("out', 'rms-web-certification"), 'RMS certification runner does not use the stable evidence directory');
assert(!certificationRunner.includes("out', 'rms-08") && !certificationRunner.includes('rms:ticket-'), 'RMS certification runner regressed to Ticket-scoped evidence');

if (failures.length > 0) {
  console.error(failures.map((failure) => `- ${failure}`).join('\n'));
  process.exit(1);
}

console.log('RMS workflow topology passed: daily checks use the domain matrix, Browser stays active, Certification is manual, and retired Ticket workflows stay retired.');
