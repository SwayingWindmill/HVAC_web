import { access, mkdir, readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const matrix = JSON.parse(await readFile(resolve(root, 'deploy/acceptance/phase1-simulator-acceptance.v1.json'), 'utf8'));
const outputDir = resolve(root, process.env.PHASE1_ACCEPTANCE_OUTPUT_DIR ?? 'out/phase1-acceptance');
const gateDir = resolve(outputDir, 'gates');
const gateResults = [];

for (const gate of matrix.gates) {
  const path = resolve(gateDir, `${gate.id}.json`);
  try {
    await access(path);
    const result = JSON.parse(await readFile(path, 'utf8'));
    const valid = result.schemaVersion === 1 && result.id === gate.id && result.command === gate.command && ['PASS', 'FAIL'].includes(result.status);
    gateResults.push(valid ? result : { id: gate.id, command: gate.command, status: 'FAIL', error: 'invalid gate evidence record' });
  } catch (error) {
    gateResults.push({ id: gate.id, command: gate.command, status: 'FAIL', error: error instanceof Error ? error.message : String(error) });
  }
}

const goWork = await readFile(resolve(root, 'go.work'), 'utf8');
const workspaceModules = [...goWork.matchAll(/^\s*(\.\/(?:libs|services|tools)\/[^\s)]+)\s*$/gm)]
  .map((match) => match[1].replace(/^\.\//, ''))
  .sort();
const goVulnReports = await Promise.all([1, 2, 3, 4].map(async (index) => {
  const path = resolve(root, `out/security/go-vuln-shard-${index}-of-4.json`);
  try {
    const report = JSON.parse(await readFile(path, 'utf8'));
    return { index, path, report };
  } catch (error) {
    return { index, path, report: null, error: error instanceof Error ? error.message : String(error) };
  }
}));
const scannedModules = goVulnReports.flatMap((item) => Array.isArray(item.report?.modules) ? item.report.modules : []);
const scanCounts = new Map();
for (const modulePath of scannedModules) scanCounts.set(modulePath, (scanCounts.get(modulePath) ?? 0) + 1);
const missingModules = workspaceModules.filter((modulePath) => !scanCounts.has(modulePath));
const duplicateModules = [...scanCounts.entries()].filter(([, count]) => count !== 1).map(([modulePath, count]) => ({ modulePath, count }));
const unexpectedModules = [...scanCounts.keys()].filter((modulePath) => !workspaceModules.includes(modulePath));
const goVulnCoverageComplete = goVulnReports.every((item) => item.report?.schemaVersion === 1
  && item.report?.status === 'passed'
  && item.report?.govulncheckVersion === 'v1.1.4'
  && item.report?.shard?.index === item.index
  && item.report?.shard?.count === 4
  && item.report?.workspaceModuleCount === workspaceModules.length)
  && missingModules.length === 0
  && duplicateModules.length === 0
  && unexpectedModules.length === 0
  && scannedModules.length === workspaceModules.length;

const resultByGate = new Map(gateResults.map((gate) => [gate.id, gate]));
const requirements = matrix.requirements.map((requirement) => {
  if (requirement.class === 'DEFERRED_HARDWARE') return { ...requirement, status: 'DEFERRED_HARDWARE', blocking: false };
  if (requirement.class === 'DEFERRED_FORMAL') return { ...requirement, status: 'DEFERRED_FORMAL', blocking: false };
  const gates = requirement.gateIds.map((id) => resultByGate.get(id));
  const passed = gates.every((gate) => gate?.status === 'PASS')
    && (requirement.id !== 'REQ-SEC-001' || goVulnCoverageComplete);
  return {
    ...requirement,
    status: passed ? 'PASS' : 'FAIL',
    blocking: true,
    gateResults: gates.map((gate, index) => ({ id: requirement.gateIds[index], status: gate?.status ?? 'MISSING' })),
  };
});

const software = requirements.filter((item) => item.class === 'SOFTWARE_REQUIRED');
const simulatorAcceptanceEligible = software.every((item) => item.status === 'PASS');
const report = {
  schemaVersion: 1,
  program: matrix.program,
  sourceDesign: matrix.sourceDesign,
  profile: 'simulator',
  status: simulatorAcceptanceEligible ? 'passed' : 'failed',
  assembledAt: new Date().toISOString(),
  simulatorAcceptanceEligible,
  hardwareAcceptanceEligible: false,
  formalPerformanceAcceptanceEligible: false,
  hardGates: matrix.hardGates,
  securityCoverage: {
    goVulnCoverageComplete,
    workspaceModuleCount: workspaceModules.length,
    scannedModuleCount: scannedModules.length,
    missingModules,
    duplicateModules,
    unexpectedModules,
  },
  summary: {
    gatesTotal: gateResults.length,
    gatesPassed: gateResults.filter((item) => item.status === 'PASS').length,
    gatesFailed: gateResults.filter((item) => item.status !== 'PASS').length,
    softwareRequirementsTotal: software.length,
    softwareRequirementsPassed: software.filter((item) => item.status === 'PASS').length,
    softwareRequirementsFailed: software.filter((item) => item.status !== 'PASS').length,
    deferredHardware: requirements.filter((item) => item.status === 'DEFERRED_HARDWARE').length,
    deferredFormal: requirements.filter((item) => item.status === 'DEFERRED_FORMAL').length,
  },
  gates: gateResults,
  requirements,
};

await mkdir(outputDir, { recursive: true });
await writeFile(resolve(outputDir, 'acceptance-report.json'), `${JSON.stringify(report, null, 2)}\n`);
const summary = [
  '# Phase 1 Simulator Acceptance',
  '',
  `- Status: **${report.status.toUpperCase()}**`,
  `- Simulator acceptance eligible: **${simulatorAcceptanceEligible ? 'YES' : 'NO'}**`,
  `- Automated gates: ${report.summary.gatesPassed}/${report.summary.gatesTotal} passed`,
  `- Software requirements: ${report.summary.softwareRequirementsPassed}/${report.summary.softwareRequirementsTotal} passed`,
  `- Go vulnerability coverage: ${report.securityCoverage.scannedModuleCount}/${report.securityCoverage.workspaceModuleCount} modules, complete=${report.securityCoverage.goVulnCoverageComplete}`,
  `- Deferred hardware: ${report.summary.deferredHardware}`,
  `- Deferred formal: ${report.summary.deferredFormal}`,
  '',
  '## Gate Results',
  '',
  ...gateResults.map((gate) => `- ${gate.status === 'PASS' ? '[x]' : '[ ]'} ${gate.id} — ${gate.status}${gate.error ? ` — ${gate.error}` : ''}`),
  '',
  '## Requirement Traceability',
  '',
  ...requirements.map((item) => `- ${item.id} — ${item.status} — ${item.title}`),
  '',
  '## Scope Boundary',
  '',
  '- Real meter and physical Modbus remain DEFERRED_HARDWARE.',
  '- Formal endurance/load and browser UAT remain DEFERRED_FORMAL.',
  '- Simulator PASS is not a production Go decision.',
  '',
].join('\n');
await writeFile(resolve(outputDir, 'acceptance-summary.md'), `${summary}\n`);
console.log(`Phase 1 Simulator Acceptance assembled: ${report.status}; gates=${report.summary.gatesPassed}/${report.summary.gatesTotal}; software=${report.summary.softwareRequirementsPassed}/${report.summary.softwareRequirementsTotal}`);
if (!simulatorAcceptanceEligible) process.exitCode = 1;
