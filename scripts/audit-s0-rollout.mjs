class ReplicaSet {
  constructor(version, replicas = 0) {
    this.version = version;
    this.ready = replicas;
  }
}

function assertAvailable(previous, current, phase) {
  const available = previous.ready + current.ready;
  if (available < 1) throw new Error(`${phase}: rolling policy allowed zero ready replicas`);
}

const previous = new ReplicaSet('previous-compatible', 2);
const current = new ReplicaSet('current', 0);
const observations = [];

assertAvailable(previous, current, 'initial');
current.ready += 1;
assertAvailable(previous, current, 'surge-current');
observations.push({ phase: 'surge-current', previous: previous.ready, current: current.ready });

previous.ready -= 1;
assertAvailable(previous, current, 'drain-previous-1');
current.ready += 1;
assertAvailable(previous, current, 'current-ready-2');
previous.ready -= 1;
assertAvailable(previous, current, 'current-only');
observations.push({ phase: 'current-only', previous: previous.ready, current: current.ready });

// Rollback is readiness-gated: a compatible previous replica becomes ready before a current replica drains.
previous.ready += 1;
assertAvailable(previous, current, 'rollback-surge-previous');
current.ready -= 1;
assertAvailable(previous, current, 'rollback-drain-current-1');
previous.ready += 1;
current.ready -= 1;
assertAvailable(previous, current, 'rollback-complete');
observations.push({ phase: 'rollback-complete', previous: previous.ready, current: current.ready });

if (previous.ready !== 2 || current.ready !== 0) throw new Error('rollback did not restore the previous compatible version');
console.log(`S0 rolling update and rollback model passed: ${JSON.stringify(observations)}`);
