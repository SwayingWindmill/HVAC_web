import { access, mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const matrixPath = resolve(root, 'deploy/acceptance/phase1-simulator-acceptance.v1.json');
const reportPath = resolve(root, 'out/phase1-acceptance/traceability-static.json');
const matrix = JSON.parse(await readFile(matrixPath, 'utf8'));
const pkg = JSON.parse(await readFile(resolve(root, 'package.json'), 'utf8'));
const assert = (condition, message) => { if (!condition) throw new Error(message); };

assert(matrix.schemaVersion === 1, 'Phase 1 acceptance schemaVersion must be 1');
assert(matrix.program === '智慧能源系统第一阶段 Simulator Acceptance', 'Phase 1 acceptance program name drifted');
assert(matrix.sourceDesign === '智慧能源系统测试与验收体系设计', 'Phase 1 source design is missing');
assert(Array.isArray(matrix.gates) && matrix.gates.length >= 10, 'Phase 1 acceptance requires at least ten automated gates');
assert(Array.isArray(matrix.requirements) && matrix.requirements.length >= 20, 'Phase 1 acceptance requires at least twenty mapped requirements');

for (const [key, value] of Object.entries(matrix.hardGates ?? {})) {
  assert(Number.isInteger(value) && value === 0, `hard gate ${key} must remain zero-tolerance`);
}

const gateIds = new Set();
for (const gate of matrix.gates) {
  assert(/^GATE-[A-Z0-9-]+$/.test(gate.id), `invalid gate id ${gate.id}`);
  assert(!gateIds.has(gate.id), `duplicate gate id ${gate.id}`);
  gateIds.add(gate.id);
  assert(typeof gate.command === 'string' && gate.command.trim(), `gate ${gate.id} has no command`);
  if (gate.command.startsWith('npm run ')) {
    const script = gate.command.slice('npm run '.length).trim();
    assert(pkg.scripts?.[script], `gate ${gate.id} references missing npm script ${script}`);
  } else if (gate.command.startsWith('node ')) {
    const scriptPath = gate.command.trim().split(/\s+/)[1];
    assert(scriptPath?.startsWith('scripts/'), `gate ${gate.id} node command must target scripts/`);
    await access(resolve(root, scriptPath));
  } else {
    throw new Error(`gate ${gate.id} uses unsupported command form`);
  }
  for (const evidence of gate.evidence ?? []) {
    assert(evidence.startsWith('out/'), `gate ${gate.id} evidence must stay under out/: ${evidence}`);
  }
}

const requirementIds = new Set();
const classCounts = { SOFTWARE_REQUIRED: 0, DEFERRED_HARDWARE: 0, DEFERRED_FORMAL: 0 };
for (const requirement of matrix.requirements) {
  assert(/^REQ-[A-Z0-9-]+$/.test(requirement.id), `invalid requirement id ${requirement.id}`);
  assert(!requirementIds.has(requirement.id), `duplicate requirement id ${requirement.id}`);
  requirementIds.add(requirement.id);
  assert(Object.hasOwn(classCounts, requirement.class), `invalid requirement class ${requirement.class}`);
  classCounts[requirement.class] += 1;
  assert(typeof requirement.title === 'string' && requirement.title.trim(), `${requirement.id} has no title`);
  assert(typeof requirement.acceptance === 'string' && requirement.acceptance.trim(), `${requirement.id} has no acceptance text`);
  assert(Array.isArray(requirement.gateIds), `${requirement.id} gateIds must be an array`);
  if (requirement.class === 'SOFTWARE_REQUIRED') {
    assert(requirement.gateIds.length > 0, `${requirement.id} is software-required but has no automated gate`);
  } else {
    assert(requirement.gateIds.length === 0, `${requirement.id} is deferred but still points at an automated PASS gate`);
  }
  for (const gateId of requirement.gateIds) assert(gateIds.has(gateId), `${requirement.id} references unknown gate ${gateId}`);
}

assert(classCounts.SOFTWARE_REQUIRED >= 15, 'Phase 1 software acceptance coverage is too small');
assert(classCounts.DEFERRED_HARDWARE >= 2, 'Real-device deferrals must remain explicit');
assert(classCounts.DEFERRED_FORMAL >= 2, 'Formal-duration/UAT deferrals must remain explicit');
assert(matrix.requirements.some((item) => item.id === 'REQ-REC-001'), 'Backup/Destroy/Restore requirement is missing');
assert(matrix.requirements.some((item) => item.id === 'REQ-METRIC-001'), 'Energy Golden Dataset requirement is missing');
assert(matrix.requirements.some((item) => item.id === 'REQ-HW-001' && item.class === 'DEFERRED_HARDWARE'), 'Real meter must not be silently treated as tested');
assert(matrix.requirements.some((item) => item.id === 'REQ-FORMAL-001' && item.class === 'DEFERRED_FORMAL'), 'Formal endurance must not be claimed by preflight');
await access(resolve(root, 'modules/energy/internal/energy/testdata/phase1-golden.v1.json'));
await access(resolve(root, 'scripts/run-phase1-postgres-restore-test.mjs'));
await access(resolve(root, 'docs/operations/phase1-simulator-acceptance.md'));

const mappedGateIds = new Set(matrix.requirements.flatMap((item) => item.gateIds));
for (const gateId of gateIds) assert(mappedGateIds.has(gateId), `automated gate ${gateId} is not traceable to any requirement`);

const report = {
  schemaVersion: 1,
  status: 'passed',
  checkedAt: new Date().toISOString(),
  requirementCount: matrix.requirements.length,
  gateCount: matrix.gates.length,
  classCounts,
  hardGates: matrix.hardGates,
  sourceDesign: matrix.sourceDesign,
};
await mkdir(dirname(reportPath), { recursive: true });
await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
console.log(`Phase 1 Simulator Acceptance static check passed: requirements=${matrix.requirements.length}, gates=${matrix.gates.length}`);
