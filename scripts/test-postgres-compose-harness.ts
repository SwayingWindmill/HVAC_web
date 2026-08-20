import assert from 'node:assert/strict';
import test from 'node:test';
import { expectEqual, findAvailablePort, runCommand } from './lib/postgres-compose-harness.ts';

test('runCommand returns trimmed stdout and rejects non-zero exit', () => {
  assert.equal(
    runCommand(process.execPath, ['-e', "process.stdout.write('ok\\n')"]),
    'ok',
  );
  assert.throws(
    () => runCommand(process.execPath, ['-e', 'process.exit(7)']),
    /failed:/,
  );
});

test('findAvailablePort returns a usable TCP port', async () => {
  const port = await findAvailablePort('test');
  assert.ok(Number.isInteger(port));
  assert.ok(port > 0 && port <= 65535);
});

test('expectEqual reports the governed label', () => {
  expectEqual('same', 'same', 'value');
  assert.throws(() => expectEqual('actual', 'expected', 'role state'), /role state: expected expected, got actual/);
});
