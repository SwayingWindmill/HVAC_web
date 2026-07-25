import { pullDockerImageWithRetry } from './lib/docker-pull-retry.mjs';

const image = 'example.invalid/component:v1@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

{
  let failure;
  try {
    await pullDockerImageWithRetry('example.invalid/component:latest', { attempts: 1 });
  } catch (error) {
    failure = error;
  }
  assert(failure instanceof Error && failure.message.includes('immutable sha256 digest'), 'docker pull retry accepted a mutable image tag');
}

{
  const calls = [];
  const warnings = [];
  const pauses = [];
  const results = [
    { status: 1, signal: null, stdout: '', stderr: 'registry timeout' },
    { status: 1, signal: null, stdout: '', stderr: 'temporary EOF' },
    { status: 0, signal: null, stdout: 'pulled', stderr: '' },
  ];
  const result = await pullDockerImageWithRetry(image, {
    attempts: 5,
    retryBaseMs: 25,
    spawn: (command, args, options) => {
      calls.push({ command, args, options });
      return results.shift();
    },
    pause: async (milliseconds) => { pauses.push(milliseconds); },
    warn: (message) => { warnings.push(message); },
  });

  assert(result.status === 0, 'docker pull retry did not return the successful result');
  assert(calls.length === 3, `docker pull retry attempt count drifted: ${calls.length}`);
  assert(calls.every((call) => call.command === 'docker' && call.args.join(' ') === `pull ${image}`), 'docker pull retry command drifted');
  assert(calls.every((call) => call.options.timeout === 45000), 'docker pull retry per-attempt timeout drifted');
  assert(JSON.stringify(pauses) === JSON.stringify([25, 50]), `docker pull retry backoff drifted: ${JSON.stringify(pauses)}`);
  assert(warnings.length === 2 && warnings[1].includes('2/5'), 'docker pull retry warnings are incomplete');
}

{
  let calls = 0;
  let failure;
  try {
    await pullDockerImageWithRetry(image, {
      attempts: 3,
      retryBaseMs: 0,
      spawn: () => {
        calls += 1;
        return { status: 1, signal: null, stdout: '', stderr: 'context deadline exceeded' };
      },
      pause: async () => {},
      warn: () => {},
    });
  } catch (error) {
    failure = error;
  }

  assert(calls === 3, `docker pull retry exhaustion count drifted: ${calls}`);
  assert(failure instanceof Error, 'docker pull retry exhaustion did not fail closed');
  assert(failure.message.includes('after 3 attempts'), 'docker pull retry exhaustion is missing the attempt count');
  assert(failure.message.includes('context deadline exceeded'), 'docker pull retry exhaustion is missing bounded diagnostics');
}

console.log('Docker pull transient-retry and fail-closed tests passed.');
