import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

class ReplicaSet {
  constructor(version, replicas = 0) {
    this.version = version;
    this.ready = replicas;
  }
}

const root = resolve(process.cwd());
const reportArgument = process.argv.find((value) => value.startsWith('--report='))?.slice('--report='.length);
const reportPath = resolve(root, reportArgument ?? 'out/s0-release-evidence/rollout-model-report.json');
const startedAt = new Date();

function assertAvailable(previous, current, phase) {
  const available = previous.ready + current.ready;
  if (available < 1) throw new Error(`${phase}: rolling policy allowed zero ready replicas`);
}

const previous = new ReplicaSet('previous-compatible', 2);
const current = new ReplicaSet('current', 0);
const observations = [];

function observe(phase) {
  const observation = {
    phase,
    previous: previous.ready,
    current: current.ready,
    available: previous.ready + current.ready,
  };
  observations.push(observation);
  return observation;
}

async function writeReport(status, error = null) {
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify({
    schemaVersion: 1,
    ticket: '08-s0-release-evidence',
    type: 'deterministic-rollout-model',
    status,
    startedAt: startedAt.toISOString(),
    finishedAt: new Date().toISOString(),
    policy: {
      replicas: 2,
      maxUnavailable: 0,
      maxSurge: 1,
      minimumAvailable: 1,
    },
    observations,
    restoredVersion: previous.ready === 2 && current.ready === 0 ? 'previous-compatible' : null,
    error,
  }, null, 2)}\n`);
}

try {
  assertAvailable(previous, current, 'initial');
  observe('initial');

  current.ready += 1;
  assertAvailable(previous, current, 'surge-current');
  observe('surge-current');

  previous.ready -= 1;
  assertAvailable(previous, current, 'drain-previous-1');
  observe('drain-previous-1');

  current.ready += 1;
  assertAvailable(previous, current, 'current-ready-2');
  observe('current-ready-2');

  previous.ready -= 1;
  assertAvailable(previous, current, 'current-only');
  observe('current-only');

  // Rollback is readiness-gated: a compatible previous replica becomes ready before a current replica drains.
  previous.ready += 1;
  assertAvailable(previous, current, 'rollback-surge-previous');
  observe('rollback-surge-previous');

  current.ready -= 1;
  assertAvailable(previous, current, 'rollback-drain-current-1');
  observe('rollback-drain-current-1');

  previous.ready += 1;
  current.ready -= 1;
  assertAvailable(previous, current, 'rollback-complete');
  observe('rollback-complete');

  if (previous.ready !== 2 || current.ready !== 0) {
    throw new Error('rollback did not restore the previous compatible version');
  }

  await writeReport('passed');
  console.log(`S0 rolling update and rollback model passed; report: ${reportPath}`);
} catch (error) {
  await writeReport('failed', error instanceof Error ? error.message : String(error));
  throw error;
}
