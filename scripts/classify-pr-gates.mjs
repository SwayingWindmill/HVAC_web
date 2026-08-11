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
  if (browser) add(browserProfiles, ['rms', 's0', 's1', 's2']);
  addReason(file, reason);
};

const selectS0 = (file, reason, { integration = false, browser = false } = {}) => {
  add(contractProfiles, ['core']);
  add(unitProfiles, ['s0']);
  if (integration) add(integrationProfiles, ['s0']);
  if (browser) add(browserProfiles, ['s0']);
  addReason(file, reason);
};

const selectS1 = (file, reason, { integration = false, browser = false } = {}) => {
  add(contractProfiles, ['core', 's1']);
  add(unitProfiles, ['s1']);
  if (integration) add(integrationProfiles, ['s1']);
  if (browser) add(browserProfiles, ['s1']);
  addReason(file, reason);
};

const selectS2 = (file, reason, { integration = 's2-baseline', browser = false } = {}) => {
  add(contractProfiles, ['core', 's2']);
  add(unitProfiles, ['s2']);
  if (integration) add(integrationProfiles, [integration]);
  if (browser) add(browserProfiles, ['s2']);
  addReason(file, reason);
};

const selectS3 = (file, reason, { integration = false } = {}) => {
  add(contractProfiles, ['core', 's3']);
  add(unitProfiles, ['s3']);
  if (integration) add(integrationProfiles, ['s3']);
  addReason(file, reason);
};

const selectAnalytics = (file, reason, { integration = false } = {}) => {
  add(contractProfiles, ['core']);
  add(unitProfiles, ['analytics']);
  if (integration) add(integrationProfiles, ['analytics']);
  addReason(file, reason);
};

const selectOperationsAgent = (file, reason, { integration = false, browser = true } = {}) => {
  add(contractProfiles, ['core']);
  add(unitProfiles, ['operations-agent']);
  if (integration) add(integrationProfiles, ['operations-agent']);
  if (browser) add(browserProfiles, ['operations-agent']);
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
    add(unitProfiles, ['s0', 's1', 's2', 's3', 'analytics']);
    addReason(file, 'Go workspace graph changed');
  });
  match(file === 'AGENTS.md' || file === 'README.md' || file === 'LICENSE' || file.startsWith('.github/ISSUE_TEMPLATE/'), () => {
    addReason(file, 'repository documentation or metadata only');
  });
  match(file.startsWith('.github/workflows/'), () => selectBroad(file, 'workflow behavior changed'));
  match([
    'scripts/check-repository-governance.mjs',
    'scripts/classify-pr-gates.mjs',
    'scripts/domain-task-matrix.mjs',
    'scripts/package-script-long-chain-baseline.json',
    'scripts/run-capability-task.mjs',
    'scripts/run-domain-task.mjs',
    'scripts/run-pr-gate.mjs',
    'scripts/test-domain-task-matrix.mjs',
    'scripts/test-pr-gate-classifier.mjs',
    'scripts/test-repository-governance.mjs',
    'scripts/update-package-script-long-chain-baseline.mjs',
  ].includes(file), () => selectBroad(file, 'PR gate or domain task matrix implementation changed'));

  match(file.startsWith('apps/hvac-web/') || file.startsWith('runtimes/copilot-runtime/'), () => selectWeb(file, 'HVAC Web runtime changed'));
  match(
    file.startsWith('apps/hvac-web/src/api/operations')
      || file.startsWith('apps/hvac-web/src/real/OperationsInvestigation')
      || file.startsWith('apps/hvac-web/src/real/operations/'),
    () => selectOperationsAgent(file, 'Operations Workspace runtime changed', { integration: false }),
  );
  match(file.startsWith('contracts/'), () => {
    add(contractProfiles, ['core']);
    if (file.includes('operations-agent') || file.includes('operations-investigation')) {
      selectOperationsAgent(file, 'Operations Investigation contract changed', { integration: false });
    } else if (file.includes('telemetry') || file.includes('s2-')) selectS2(file, 'telemetry contract changed', { integration: false, browser: true });
    else if (file.includes('command') || file.includes('s3-')) selectS3(file, 'command contract changed');
    else selectBroad(file, 'shared contract changed');
  });

  match(file.startsWith('libs/identitycontext/') || file.startsWith('libs/oidctest/') || file.startsWith('libs/sessionevent/') || file.startsWith('libs/sessionstore/') || file.startsWith('libs/observability/') || file.startsWith('services/audit-ledger-service/') || file.startsWith('services/outbox-relay/') || file.startsWith('services/platform-core-service/'), () => selectS0(file, 'S0 identity, durability, or observability code changed', { integration: file.includes('session') || file.includes('outbox') }));
  match(file.startsWith('libs/ownershipregistry/') || file.startsWith('libs/registryauth/') || file.startsWith('services/legacy-migration-service/') || file.startsWith('tools/legacy-private-fixture/') || file.startsWith('deploy/s1/') || file.startsWith('infra/s1-'), () => selectS1(file, 'S1 registry capability changed', { integration: true, browser: file.includes('hvac-web') }));
  match(file.startsWith('libs/telemetryauth/') || file.startsWith('services/telemetry-runtime-service/') || file.startsWith('services/telemetry-shadow-comparator/') || file.startsWith('deploy/s2/') || file.startsWith('infra/s2-telemetry/'), () => {
    const lower = file.toLowerCase();
    const integration = lower.includes('realtime') || lower.includes('centrifugo') || lower.includes('005-s2-realtime')
      ? 's2-realtime'
      : lower.includes('history') || lower.includes('clickhouse') || lower.includes('projector')
        ? 's2-history'
        : lower.includes('ingest') || lower.includes('outbox')
          ? 's2-ingest'
          : 's2-baseline';
    selectS2(file, 'S2 telemetry capability changed', { integration, browser: lower.includes('live') || lower.includes('hvac-web') });
  });
  match(file.startsWith('libs/commandauth/') || file.startsWith('libs/commandmodel/') || file.startsWith('services/command-service/') || file.startsWith('services/command-dispatcher/') || file.startsWith('services/thingsboard-connector-control/') || file.startsWith('deploy/s3/'), () => selectS3(file, 'S3 command capability changed', { integration: true }));
  match(file.startsWith('libs/analyticsmodel/') || file.startsWith('services/telemetry-query-service/') || file.startsWith('services/analytics-read-model-projector/') || file.startsWith('deploy/analytics/'), () => selectAnalytics(file, 'analytics capability changed', { integration: true }));
  match(file.startsWith('services/operations-agent-service/') || file.startsWith('benchmarks/operations-agent/') || file.startsWith('infra/operations-agent/'), () => selectOperationsAgent(file, 'Operations Agent capability changed', { integration: true }));

  match(file.startsWith('services/iam-service/') || file.startsWith('services/platform-gateway/'), () => {
    selectS0(file, 'shared IAM or Gateway boundary changed', { browser: true });
    selectS1(file, 'shared IAM or Gateway boundary changed', { browser: true });
    selectS2(file, 'shared IAM or Gateway boundary changed', { integration: 's2-baseline', browser: true });
    selectS3(file, 'shared IAM or Gateway boundary changed');
    add(browserProfiles, ['rms']);
  });
  match(
    file.startsWith('services/platform-gateway/internal/gateway/operations_agent'),
    () => selectOperationsAgent(file, 'Operations Gateway boundary changed', { integration: false }),
  );

  match(file.startsWith('pocs/platform-components/'), () => {
    add(unitProfiles, ['pocs']);
    addReason(file, 'platform component POC changed');
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
      'scripts/check-repository-governance.mjs',
      'scripts/classify-pr-gates.mjs',
      'scripts/domain-task-matrix.mjs',
      'scripts/package-script-long-chain-baseline.json',
      'scripts/run-capability-task.mjs',
      'scripts/run-domain-task.mjs',
      'scripts/run-pr-gate.mjs',
      'scripts/test-domain-task-matrix.mjs',
      'scripts/test-pr-gate-classifier.mjs',
      'scripts/test-repository-governance.mjs',
      'scripts/update-package-script-long-chain-baseline.mjs',
    ].includes(file), () => {});
    scriptMatch(lower.includes('rms') || lower.includes('browser-audit') || lower.includes('ui-audit') || lower.includes('bigscreen') || lower.includes('ops-loop'), () => selectWeb(file, 'browser or RMS automation changed'));
    scriptMatch(lower.includes('s0-') || lower.includes('durable') || lower.includes('auth-principal') || lower.includes('platform-gateway'), () => selectS0(file, 'S0 automation changed', { integration: lower.includes('postgres'), browser: lower.includes('browser') || lower.includes('audit') }));
    scriptMatch(lower.includes('s1-') || lower.includes('registry'), () => selectS1(file, 'S1 automation changed', { integration: lower.includes('postgres'), browser: lower.includes('browser') || lower.includes('hvac-web') }));
    scriptMatch(lower.includes('s2-') || lower.includes('telemetry'), () => {
      const integration = lower.includes('realtime') ? 's2-realtime' : lower.includes('history') ? 's2-history' : lower.includes('ingest') ? 's2-ingest' : lower.includes('postgres') ? 's2-baseline' : false;
      selectS2(file, 'S2 automation changed', { integration, browser: lower.includes('browser') || lower.includes('live-client') || lower.includes('hvac-web') });
    });
    scriptMatch(lower.includes('s3-') || lower.includes('command'), () => selectS3(file, 'S3 automation changed', { integration: lower.includes('postgres') || lower.includes('thingsboard') }));
    scriptMatch(lower.includes('analytics'), () => selectAnalytics(file, 'analytics automation changed', { integration: lower.includes('history') || lower.includes('cube') }));
    scriptMatch(lower.includes('operations-agent') || lower.includes('operations-workspace') || lower.includes('operations-reconnect'), () => selectOperationsAgent(file, 'Operations Agent automation changed', { integration: lower.includes('postgres') || lower.includes('migration') }));
    scriptMatch(lower.includes('ownership') || lower.includes('contract') || lower.includes('production-rollout'), () => add(contractProfiles, ['core']));
    if (!scriptMatched) {
      unknown = true;
      selectBroad(file, 'unknown automation script selected the broad fail-closed suite');
    }
  });

  match(file.startsWith('docs/operations/'), () => {
    const lower = file.toLowerCase();
    if (lower.includes('rms')) add(contractProfiles, ['rms']);
    if (lower.includes('s1-') || lower.includes('registry')) add(contractProfiles, ['s1']);
    if (lower.includes('s2-') || lower.includes('telemetry')) add(contractProfiles, ['s2']);
    if (lower.includes('s3-') || lower.includes('command')) add(contractProfiles, ['s3']);
    addReason(file, 'operations contract or runbook changed');
  });

  match(file.startsWith('docs/') || file.startsWith('.github/') || file.startsWith('.agents/') || file.startsWith('.scratch/'), () => addReason(file, 'documentation or local tooling changed'));

  if (!matched) {
    unknown = true;
    selectBroad(file, 'unknown path selected the broad fail-closed suite');
  }
}

const browserWindowsProfileSet = new Set(gateProfileSets['browser-windows'].browser);
const browserLinuxProfileSet = new Set(gateProfileSets['browser-linux'].browser);
const browserWindowsProfiles = sorted([...browserProfiles].filter((profile) => browserWindowsProfileSet.has(profile)));
const browserLinuxProfiles = sorted([...browserProfiles].filter((profile) => browserLinuxProfileSet.has(profile)));
const classification = {
  schemaVersion: 1,
  changedFiles: sorted(files),
  broad,
  unknown,
  contracts: contractProfiles.size > 0,
  units: unitProfiles.size > 0,
  integrations: integrationProfiles.size > 0,
  browsers: browserProfiles.size > 0,
  browserWindows: browserWindowsProfiles.length > 0,
  browserLinux: browserLinuxProfiles.length > 0,
  contractProfiles: sorted(contractProfiles),
  unitProfiles: sorted(unitProfiles),
  integrationProfiles: sorted(integrationProfiles),
  browserProfiles: sorted(browserProfiles),
  browserWindowsProfiles,
  browserLinuxProfiles,
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
    `browser_windows=${classification.browserWindows}`,
    `browser_linux=${classification.browserLinux}`,
    `contract_profiles=${classification.contractProfiles.join(',')}`,
    `unit_profiles=${classification.unitProfiles.join(',')}`,
    `integration_profiles=${classification.integrationProfiles.join(',')}`,
    `browser_profiles=${classification.browserProfiles.join(',')}`,
    `browser_windows_profiles=${classification.browserWindowsProfiles.join(',')}`,
    `browser_linux_profiles=${classification.browserLinuxProfiles.join(',')}`,
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
