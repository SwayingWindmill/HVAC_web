import { spawnSync } from 'node:child_process';
import { access, mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const matrixPath = resolve(root, 'deploy/acceptance/phase1-simulator-acceptance.v1.json');
const outputDir = resolve(root, process.env.PHASE1_ACCEPTANCE_OUTPUT_DIR ?? 'out/phase1-acceptance');
const reportPath = resolve(outputDir, 'acceptance-report.json');
const summaryPath = resolve(outputDir, 'acceptance-summary.md');
const gateEvidenceDir = resolve(outputDir, 'gates');
const matrix = JSON.parse(await readFile(matrixPath, 'utf8'));
const profile = process.argv.find((value) => value.startsWith('--profile='))?.slice('--profile='.length) ?? 'simulator';
const selectedGate = process.argv.find((value) => value.startsWith('--gate='))?.slice('--gate='.length) ?? '';

if (!matrix.profiles?.[profile]) throw new Error(`unsupported acceptance profile: ${profile}`);
const gateMap = new Map(matrix.gates.map((gate) => [gate.id, gate]));
if (selectedGate && !gateMap.has(selectedGate)) throw new Error(`unknown acceptance gate: ${selectedGate}`);

function execute(command) {
  const tokens = command.trim().split(/\s+/);
  let executable;
  let args;
  if (tokens[0] === 'npm' && tokens[1] === 'run' && tokens.length === 3) {
    if (process.env.npm_execpath) {
      executable = process.execPath;
      args = [process.env.npm_execpath, 'run', tokens[2]];
    } else {
      executable = 'npm';
      args = ['run', tokens[2]];
    }
  } else if (tokens[0] === 'node') {
    executable = process.execPath;
    args = tokens.slice(1);
  } else {
    throw new Error(`unsupported acceptance command: ${command}`);
  }
  const started = Date.now();
  const result = spawnSync(executable, args, {
    cwd: root,
    env: process.env,
    encoding: 'utf8',
    windowsHide: true,
    maxBuffer: 32 * 1024 * 1024,
  });
  const stdout = String(result.stdout ?? '');
  const stderr = String(result.stderr ?? '');
  return {
    exitCode: result.status ?? 1,
    durationMs: Date.now() - started,
    stdoutTail: stdout.slice(-12000),
    stderrTail: stderr.slice(-12000),
    spawnError: result.error?.message ?? '',
  };
}

async function inspectEvidence(paths) {
  const results = [];
  for (const path of paths ?? []) {
    const absolute = resolve(root, path);
    try {
      await access(absolute);
      let status = 'present';
      if (path.endsWith('.json')) {
        const parsed = JSON.parse(await readFile(absolute, 'utf8'));
        if (typeof parsed.status === 'string') status = parsed.status;
      }
      results.push({ path, present: true, status });
    } catch (error) {
      results.push({ path, present: false, status: 'missing', error: error instanceof Error ? error.message : String(error) });
    }
  }
  return results;
}

await mkdir(outputDir, { recursive: true });
await mkdir(gateEvidenceDir, { recursive: true });
const gateResults = [];
const gatesToRun = selectedGate ? [gateMap.get(selectedGate)] : matrix.gates;
for (const gate of gatesToRun) {
  process.stdout.write(`\n[phase1-acceptance] ${gate.id}: ${gate.command}\n`);
  const execution = execute(gate.command);
  const evidence = await inspectEvidence(gate.evidence);
  const evidenceOK = evidence.every((item) => item.present && !['failed', 'error'].includes(String(item.status).toLowerCase()));
  const passed = execution.exitCode === 0 && !execution.spawnError && evidenceOK;
  const gateResult = {
    id: gate.id,
    category: gate.category,
    command: gate.command,
    formalMeasurement: gate.formalMeasurement ?? null,
    status: passed ? 'PASS' : 'FAIL',
    ...execution,
    evidence,
  };
  gateResults.push(gateResult);
  await writeFile(resolve(gateEvidenceDir, `${gateResult.id}.json`), `${JSON.stringify({ schemaVersion: 1, recordedAt: new Date().toISOString(), ...gateResult }, null, 2)}\n`);
  process.stdout.write(`[phase1-acceptance] ${gate.id}: ${passed ? 'PASS' : 'FAIL'} (${execution.durationMs}ms)\n`);
}

const resultByGate = new Map(gateResults.map((gate) => [gate.id, gate]));
const requirementResults = matrix.requirements.map((requirement) => {
  if (requirement.class === 'DEFERRED_HARDWARE') {
    return { ...requirement, status: 'DEFERRED_HARDWARE', blocking: false };
  }
  if (requirement.class === 'DEFERRED_FORMAL') {
    return { ...requirement, status: 'DEFERRED_FORMAL', blocking: false };
  }
  if (selectedGate && !requirement.gateIds.includes(selectedGate)) {
    return { ...requirement, status: 'NOT_RUN', blocking: false };
  }
  const gates = requirement.gateIds.map((id) => resultByGate.get(id)).filter(Boolean);
  const complete = gates.length === requirement.gateIds.length;
  const passed = complete && gates.every((gate) => gate.status === 'PASS');
  return {
    ...requirement,
    status: passed ? 'PASS' : 'FAIL',
    blocking: true,
    gateResults: gates.map((gate) => ({ id: gate.id, status: gate.status })),
  };
});

const fullRun = !selectedGate;
const softwareResults = requirementResults.filter((item) => item.class === 'SOFTWARE_REQUIRED');
const softwarePassed = fullRun && softwareResults.every((item) => item.status === 'PASS');
const deferredHardware = requirementResults.filter((item) => item.status === 'DEFERRED_HARDWARE');
const deferredFormal = requirementResults.filter((item) => item.status === 'DEFERRED_FORMAL');
const failedSoftware = softwareResults.filter((item) => item.status === 'FAIL');
const report = {
  schemaVersion: 1,
  program: matrix.program,
  sourceDesign: matrix.sourceDesign,
  profile,
  status: fullRun ? (softwarePassed ? 'passed' : 'failed') : 'partial',
  startedAt: new Date(Date.now() - gateResults.reduce((sum, item) => sum + item.durationMs, 0)).toISOString(),
  finishedAt: new Date().toISOString(),
  simulatorAcceptanceEligible: softwarePassed,
  hardwareAcceptanceEligible: false,
  formalPerformanceAcceptanceEligible: false,
  hardGates: matrix.hardGates,
  summary: {
    gatesTotal: gatesToRun.length,
    gatesPassed: gateResults.filter((item) => item.status === 'PASS').length,
    gatesFailed: gateResults.filter((item) => item.status === 'FAIL').length,
    softwareRequirementsTotal: softwareResults.length,
    softwareRequirementsPassed: softwareResults.filter((item) => item.status === 'PASS').length,
    softwareRequirementsFailed: failedSoftware.length,
    deferredHardware: deferredHardware.length,
    deferredFormal: deferredFormal.length,
  },
  gates: gateResults,
  requirements: requirementResults,
};
await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);

const lines = [
  '# Phase 1 Simulator Acceptance',
  '',
  `- Status: **${report.status.toUpperCase()}**`,
  `- Simulator acceptance eligible: **${report.simulatorAcceptanceEligible ? 'YES' : 'NO'}**`,
  `- Hardware acceptance eligible: **NO**`,
  `- Formal performance acceptance eligible: **NO**`,
  `- Automated gates: ${report.summary.gatesPassed}/${report.summary.gatesTotal} passed`,
  `- Software requirements: ${report.summary.softwareRequirementsPassed}/${report.summary.softwareRequirementsTotal} passed`,
  `- Deferred hardware requirements: ${report.summary.deferredHardware}`,
  `- Deferred formal requirements: ${report.summary.deferredFormal}`,
  '',
  '## Gate Results',
  '',
  ...gateResults.map((gate) => `- ${gate.status === 'PASS' ? '[x]' : '[ ]'} ${gate.id} — ${gate.status} — ${gate.durationMs} ms`),
  '',
  '## Requirement Traceability',
  '',
  ...requirementResults.map((item) => `- ${item.id} — ${item.status} — ${item.title}`),
  '',
  '## Explicit Limitations',
  '',
  '- Real meter / physical Modbus / field measurement comparison is DEFERRED_HARDWARE.',
  '- 24–72h endurance and formal values/s load evidence is DEFERRED_FORMAL.',
  '- Browser UAT / Production Acceptance is DEFERRED_FORMAL.',
  '- Capacity preflight is configuration evidence only and must not be presented as a measured endurance result.',
  '',
];
await writeFile(summaryPath, `${lines.join('\n')}\n`);
console.log(`\nPhase 1 Simulator Acceptance report: ${reportPath}`);
console.log(`Phase 1 Simulator Acceptance summary: ${summaryPath}`);
if (fullRun && !softwarePassed) process.exitCode = 1;
