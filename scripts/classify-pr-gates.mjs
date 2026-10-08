import { spawnSync } from 'node:child_process';
import { appendFile, mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

import { gateProfileSets } from './domain-task-matrix.mjs';

const root = resolve(process.cwd());
const argumentsMap = new Map(
  process.argv.slice(2).map((argument) => {
    const separator = argument.indexOf('=');
    return separator === -1 ? [argument, 'true'] : [argument.slice(0, separator), argument.slice(separator + 1)];
  }),
);

const normalize = (value) => value.replaceAll('\\', '/').replace(/^\.\//, '').trim();
const sorted = (values) => [...values].sort();

async function changedFiles() {
  const filesFile = argumentsMap.get('--files-file');
  if (filesFile) {
    return (await readFile(resolve(root, filesFile), 'utf8'))
      .split(/\r?\n/u)
      .map(normalize)
      .filter(Boolean);
  }

  const explicitFiles = argumentsMap.get('--files');
  if (explicitFiles) return explicitFiles.split(',').map(normalize).filter(Boolean);

  const base = argumentsMap.get('--base') || process.env.GITHUB_BASE_SHA || 'origin/main';
  const head = argumentsMap.get('--head') || process.env.GITHUB_HEAD_SHA || 'HEAD';
  const gitEnvironment = { ...process.env };
  delete gitEnvironment.GIT_DIR;
  delete gitEnvironment.GIT_WORK_TREE;
  if (process.env.GIT_DIR === '(NULL)' || process.env.GIT_WORK_TREE === '(NULL)') {
    const worktreePointer = (await readFile(resolve(root, '.git'), 'utf8')).trim();
    if (worktreePointer.startsWith('gitdir: ')) {
      gitEnvironment.GIT_DIR = worktreePointer.slice('gitdir: '.length).trim();
      gitEnvironment.GIT_WORK_TREE = root;
    }
  }
  const result = spawnSync('git', ['diff', '--name-only', '--diff-filter=ACMRTUXB', `${base}...${head}`], {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
    env: gitEnvironment,
  });
  if (result.error || result.status !== 0) {
    const detail = result.error?.message || result.stderr?.trim() || String(result.status);
    throw new Error(`Unable to classify PR paths: ${detail}`);
  }
  return result.stdout.split(/\r?\n/u).map(normalize).filter(Boolean);
}

const files = [...new Set(await changedFiles())];
const contractProfiles = new Set();
const unitProfiles = new Set();
const integrationProfiles = new Set();
const browserProfiles = new Set();
const reasons = [];
let broad = false;
let unknown = false;

const add = (set, values) => values.forEach((value) => set.add(value));
const addReason = (file, reason) => reasons.push({ file, reason });
const selectBroad = (file, reason) => {
  broad = true;
  add(contractProfiles, gateProfileSets.all.contracts);
  add(unitProfiles, gateProfileSets.all.unit);
  addReason(file, reason);
};

const selectWeb = (file, reason, { browser = true } = {}) => {
  add(unitProfiles, ['web']);
  if (browser) add(browserProfiles, ['telemetry']);
  addReason(file, reason);
};

const selectPlatform = (file, reason, { integration = false, browser = false } = {}) => {
  add(contractProfiles, ['core']);
  add(unitProfiles, ['platform']);
  if (integration) add(integrationProfiles, ['platform']);
  if (browser) add(browserProfiles, ['platform']);
  addReason(file, reason);
};

const selectRegistry = (file, reason, { integration = false } = {}) => {
  add(contractProfiles, ['core', 'registry']);
  add(unitProfiles, ['registry']);
  if (integration) add(integrationProfiles, ['registry']);
  addReason(file, reason);
};

const selectTelemetry = (file, reason, { integration = false, browser = false } = {}) => {
  add(contractProfiles, ['core', 'telemetry']);
  add(unitProfiles, ['telemetry']);
  if (integration) add(integrationProfiles, ['telemetry']);
  if (browser) add(browserProfiles, ['telemetry']);
  addReason(file, reason);
};

const selectCommand = (file, reason, { integration = false } = {}) => {
  add(contractProfiles, ['core', 'command']);
  add(unitProfiles, ['command']);
  if (integration) add(integrationProfiles, ['command']);
  addReason(file, reason);
};

const selectAlarm = (file, reason, { integration = false } = {}) => {
  add(contractProfiles, ['core']);
  add(unitProfiles, ['alarm']);
  if (integration) add(integrationProfiles, ['alarm']);
  addReason(file, reason);
};

const selectWorkOrder = (file, reason, { integration = false, browser = false } = {}) => {
  add(contractProfiles, ['core']);
  add(unitProfiles, ['workorder']);
  if (integration) add(integrationProfiles, ['workorder']);
  if (browser) add(browserProfiles, ['workorder']);
  addReason(file, reason);
};

const selectAnalytics = (file, reason, { integration = false } = {}) => {
  add(contractProfiles, ['core']);
  add(unitProfiles, ['analytics']);
  if (integration) add(integrationProfiles, ['analytics']);
  addReason(file, reason);
};

const selectOperationsAgent = (file, reason, { integration = false } = {}) => {
  add(contractProfiles, ['core']);
  add(unitProfiles, ['operations-agent']);
  if (integration) add(integrationProfiles, ['operations-agent']);
  addReason(file, reason);
};

for (const file of files) {
  let matched = false;
  const match = (condition, callback) => {
    if (!condition) return;
    matched = true;
    callback();
  };

  match(file === 'package.json', () => selectBroad(file, 'root package scripts or dependency declarations changed'));
  match(file === 'package-lock.json', () => {
    add(unitProfiles, ['web']);
    addReason(file, 'dependency lock changed; compile and unit checks run, database and browser gates stay selective');
  });
  match(file === 'go.work' || file === 'go.work.sum', () => {
    add(unitProfiles, ['platform', 'registry', 'telemetry', 'command', 'alarm', 'workorder', 'analytics']);
    addReason(file, 'Go workspace graph changed');
  });
  match(file === 'AGENTS.md' || file === 'README.md' || file === 'LICENSE' || file.startsWith('.github/ISSUE_TEMPLATE/'), () => {
    addReason(file, 'repository documentation or metadata only');
  });
  match(file.startsWith('.github/workflows/'), () => selectBroad(file, 'workflow behavior changed'));
  match([
    'scripts/check-repository-governance.ts',
    'scripts/classify-pr-gates.mjs',
    'scripts/domain-task-matrix.mjs',
    'scripts/package-script-long-chain-baseline.json',
    'scripts/run-domain-task.mjs',
    'scripts/run-pr-gate.mjs',
    'scripts/test-domain-task-matrix.mjs',
    'scripts/test-pr-gate-classifier.mjs',
    'scripts/update-package-script-long-chain-baseline.mjs',
  ].includes(file), () => selectBroad(file, 'PR gate or domain task matrix implementation changed'));

  match(file.startsWith('apps/hvac-web/') || file.startsWith('runtimes/copilot-runtime/'), () => selectWeb(file, 'HVAC Web runtime changed'));
  match(file.startsWith('contracts/'), () => {
    add(contractProfiles, ['core']);
    if (file.includes('operations-agent') || file.includes('operations-investigation')) {
      selectOperationsAgent(file, 'Operations Investigation contract changed', { integration: false });
    } else if (file.includes('telemetry') || file.includes('s2-')) selectTelemetry(file, 'telemetry contract changed', { integration: false, browser: true });
    else if (file.includes('command') || file.includes('s3-')) selectCommand(file, 'command contract changed');
    else if (file.includes('alarm') || file.includes('s4-')) selectAlarm(file, 'Alarm contract changed');
    else if (file.includes('work-order') || file.includes('workorder') || file.includes('s5-')) selectWorkOrder(file, 'Work Order contract changed');
    else selectBroad(file, 'shared contract changed');
  });

  match(file.startsWith('libs/identitycontext/') || file.startsWith('libs/oidctest/') || file.startsWith('libs/sessionevent/') || file.startsWith('libs/sessionstore/') || file.startsWith('libs/observability/') || file.startsWith('modules/audit/') || file.startsWith('services/outbox-relay/') || file.startsWith('modules/registry/'), () => selectPlatform(file, 'platform identity, durability, or observability code changed', { integration: file.includes('session') || file.includes('outbox') }));
  match(file.startsWith('libs/ownershipregistry/') || file.startsWith('libs/registryauth/') || file.startsWith('tools/legacy-private-fixture/') || file.startsWith('deploy/s1/') || file.startsWith('infra/s1-'), () => selectRegistry(file, 'registry capability changed', { integration: true }));
  match(file.startsWith('libs/telemetryauth/') || file.startsWith('modules/connectivity/') || file.startsWith('modules/telemetry/pkg/telemetry/') || file.startsWith('modules/telemetry/pkg/telemetryapi/') || file.startsWith('modules/telemetry/cmd/telemetry-history-projector/') || file === 'modules/telemetry/go.mod' || file === 'modules/telemetry/go.sum' || file.startsWith('deploy/s2/') || file.startsWith('infra/telemetry/'), () => {
    const lower = file.toLowerCase();
    selectTelemetry(file, 'telemetry capability changed', {
      integration: true,
      browser: lower.includes('live') || lower.includes('hvac-web'),
    });
  });
  match(file.startsWith('libs/commandauth/') || file.startsWith('libs/commandmodel/') || file.startsWith('modules/command/') || file.startsWith('deploy/s3/') || file.startsWith('infra/command/'), () => selectCommand(file, 'command capability changed', { integration: true }));
  match(file.startsWith('libs/alarmauth/') || file.startsWith('libs/alarmmodel/') || file.startsWith('modules/alarm/') || file.startsWith('deploy/s4/') || file.startsWith('infra/alarm/'), () => selectAlarm(file, 'Alarm capability changed', { integration: true }));
  match(file.startsWith('libs/workorderauth/') || file.startsWith('libs/workordermodel/') || file.startsWith('modules/workorder/') || file.startsWith('deploy/s5/') || file.startsWith('infra/workorder/'), () => selectWorkOrder(file, 'Work Order capability changed', { integration: true }));
  match(file.startsWith('libs/analyticsmodel/') || file.startsWith('modules/telemetry/internal/analytics/') || file.startsWith('modules/telemetry/internal/cube/') || file.startsWith('modules/telemetry/internal/history/') || file.startsWith('modules/telemetry/internal/query/') || file.startsWith('modules/telemetry/pkg/queryservice/') || file.startsWith('modules/telemetry/cmd/telemetry-query-owner/') || file === 'modules/telemetry/go.mod' || file === 'modules/telemetry/go.sum' || file.startsWith('modules/energy/') || file.startsWith('deploy/analytics/'), () => selectAnalytics(file, 'analytics capability changed', { integration: true }));
  match(file.startsWith('services/operations-agent-service/') || file.startsWith('benchmarks/operations-agent/') || file.startsWith('infra/operations-agent/'), () => selectOperationsAgent(file, 'Operations Agent capability changed', { integration: true }));

  match(file.startsWith('modules/iam/') || file.startsWith('cmd/energy-api/'), () => {
    selectPlatform(file, 'shared IAM or Gateway boundary changed', { browser: true });
    selectRegistry(file, 'shared IAM or Gateway boundary changed');
    selectTelemetry(file, 'shared IAM or Gateway boundary changed', { integration: true, browser: true });
    selectCommand(file, 'shared IAM or Gateway boundary changed');
  });
  match(
    file.startsWith('cmd/energy-api/internal/gateway/operations_agent'),
    () => selectOperationsAgent(file, 'Operations Gateway boundary changed', { integration: false }),
  );

  match(file.startsWith('pocs/platform-components/'), () => {
    add(unitProfiles, ['pocs']);
    addReason(file, 'platform component POC changed');
  });
  match(file.startsWith('pocs/telemetry-shadow-comparator/') || file.startsWith('tools/legacy-registry-migrator/'), () => {
    addReason(file, 'retired migration or cutover evidence changed');
  });

  match(file.startsWith('scripts/'), () => {
    const lower = file.toLowerCase();
    let scriptMatched = false;
    const scriptMatch = (condition, callback) => {
      if (!condition) return;
      scriptMatched = true;
      callback();
    };
    scriptMatch([
      'scripts/check-repository-governance.ts',
      'scripts/classify-pr-gates.mjs',
      'scripts/domain-task-matrix.mjs',
      'scripts/package-script-long-chain-baseline.json',
      'scripts/run-domain-task.mjs',
      'scripts/run-pr-gate.mjs',
      'scripts/test-domain-task-matrix.mjs',
      'scripts/test-pr-gate-classifier.mjs',
      'scripts/update-package-script-long-chain-baseline.mjs',
    ].includes(file), () => {});
    scriptMatch(lower.includes('rms') || lower.includes('browser-audit'), () => selectWeb(file, 'browser or RMS automation changed'));
    scriptMatch(lower.includes('s0-') || lower.includes('durable') || lower.includes('auth-principal') || lower.includes('platform-gateway'), () => selectPlatform(file, 'platform automation changed', { integration: lower.includes('postgres'), browser: lower.includes('browser') || lower.includes('audit') }));
    scriptMatch(lower.includes('s1-') || lower.includes('registry'), () => selectRegistry(file, 'registry automation changed', { integration: lower.includes('postgres') }));
    scriptMatch(lower.includes('s2-') || lower.includes('telemetry'), () => {
      const integration = lower.includes('realtime') || lower.includes('history') || lower.includes('ingest') || lower.includes('postgres');
      selectTelemetry(file, 'telemetry automation changed', { integration, browser: lower.includes('browser') || lower.includes('live-client') || lower.includes('hvac-web') });
    });
    scriptMatch(lower.includes('s3-') || lower.includes('command'), () => selectCommand(file, 'command automation changed', { integration: lower.includes('postgres') }));
    scriptMatch(lower.includes('s4-') || lower.includes('alarm'), () => selectAlarm(file, 'Alarm automation changed', { integration: lower.includes('postgres') }));
    scriptMatch(lower.includes('s5-') || lower.includes('work-order') || lower.includes('workorder'), () => selectWorkOrder(file, 'Work Order automation changed', { integration: lower.includes('postgres'), browser: lower.includes('browser') }));
    scriptMatch(lower.includes('analytics'), () => selectAnalytics(file, 'analytics automation changed', { integration: lower.includes('history') || lower.includes('cube') }));
    scriptMatch(lower.includes('operations-agent'), () => selectOperationsAgent(file, 'Operations Agent automation changed', { integration: lower.includes('postgres') || lower.includes('migration') }));
    scriptMatch(lower.includes('ownership') || lower.includes('contract') || lower.includes('production-rollout'), () => add(contractProfiles, ['core']));
    if (!scriptMatched) {
      unknown = true;
      selectBroad(file, 'unknown automation script selected the broad fail-closed suite');
    }
  });

  match(file.startsWith('docs/operations/'), () => {
    const lower = file.toLowerCase();
    if (lower.includes('rms') || lower.includes('web')) add(contractProfiles, ['web']);
    if (lower.includes('s1-') || lower.includes('registry')) add(contractProfiles, ['registry']);
    if (lower.includes('s2-') || lower.includes('telemetry')) add(contractProfiles, ['telemetry']);
    if (lower.includes('s3-') || lower.includes('command')) add(contractProfiles, ['command']);
    addReason(file, 'operations contract or runbook changed');
  });

  match(file.startsWith('docs/') || file.startsWith('.github/') || file.startsWith('.agents/') || file.startsWith('.scratch/'), () => addReason(file, 'documentation or local tooling changed'));

  if (!matched) {
    unknown = true;
    selectBroad(file, 'unknown path selected the broad fail-closed suite');
  }
}

const classification = {
  schemaVersion: 1,
  changedFiles: sorted(files),
  broad,
  unknown,
  contracts: contractProfiles.size > 0,
  units: unitProfiles.size > 0,
  integrations: integrationProfiles.size > 0,
  browsers: browserProfiles.size > 0,
  contractProfiles: sorted(contractProfiles),
  unitProfiles: sorted(unitProfiles),
  integrationProfiles: sorted(integrationProfiles),
  browserProfiles: sorted(browserProfiles),
  reasons,
};

const reportPath = resolve(root, 'out/pr-gates/classification.json');
await mkdir(dirname(reportPath), { recursive: true });
await writeFile(reportPath, `${JSON.stringify(classification, null, 2)}\n`);

if (process.env.GITHUB_OUTPUT) {
  const outputs = [
    `contracts=${classification.contracts}`,
    `units=${classification.units}`,
    `integrations=${classification.integrations}`,
    `browsers=${classification.browsers}`,
    `contract_profiles=${classification.contractProfiles.join(',')}`,
    `unit_profiles=${classification.unitProfiles.join(',')}`,
    `integration_profiles=${classification.integrationProfiles.join(',')}`,
    `browser_profiles=${classification.browserProfiles.join(',')}`,
  ];
  await appendFile(process.env.GITHUB_OUTPUT, `${outputs.join('\n')}\n`);
}

if (process.env.GITHUB_STEP_SUMMARY) {
  const rows = [
    ['Contracts', classification.contractProfiles.join(', ') || 'none'],
    ['Unit', classification.unitProfiles.join(', ') || 'none'],
    ['Integration', classification.integrationProfiles.join(', ') || 'none'],
    ['Browser', classification.browserProfiles.join(', ') || 'none'],
    ['Fail closed', classification.broad ? 'yes' : 'no'],
  ];
  await appendFile(process.env.GITHUB_STEP_SUMMARY, `## PR gate classification\n\n| Gate | Profiles |\n|---|---|\n${rows.map(([gate, profiles]) => `| ${gate} | ${profiles} |`).join('\n')}\n`);
}

process.stdout.write(`${JSON.stringify(classification)}\n`);
