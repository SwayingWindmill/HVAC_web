import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import {
  collectLinuxVerifyRuntimeReport,
  DEFAULT_VERIFY_PORTS,
  isSupportedNodeVersion,
  parseListeningPortsFromProcNet,
  parseNodeVersion,
  parseVerifyArguments,
  readListeningPorts,
  windowsPathToWsl,
} from './check-verify-runtime.ts';

test('windowsPathToWsl converts drive paths cleanly without altering case inside subdirectories', () => {
  assert.equal(
    windowsPathToWsl('E:\\Code\\HVAC_web\\.worktrees\\harness-fast-io\\scripts\\check-verify-runtime.ts'),
    '/mnt/e/Code/HVAC_web/.worktrees/harness-fast-io/scripts/check-verify-runtime.ts',
  );
  assert.equal(windowsPathToWsl('D:/'), '/mnt/d');
  assert.throws(() => windowsPathToWsl('/mnt/e/Code/HVAC_web'), /Windows drive-letter path/);
});

test('Node version parsing enforces >= v22.22.0', () => {
  assert.equal(isSupportedNodeVersion(parseNodeVersion('v24.16.0')), true);
  assert.equal(isSupportedNodeVersion(parseNodeVersion('v22.22.0')), true);
  assert.equal(isSupportedNodeVersion(parseNodeVersion('v22.21.9')), false);
  assert.equal(isSupportedNodeVersion(parseNodeVersion('v20.19.0')), false);
  assert.throws(() => parseNodeVersion('invalid'), /Unable to parse Node\.js version/);
});

test('parseVerifyArguments parses defaults, custom ports, help, and rejects malformed inputs', () => {
  assert.deepEqual(parseVerifyArguments([]), { help: false, ports: DEFAULT_VERIFY_PORTS });
  assert.deepEqual(parseVerifyArguments(['--ports=5190, 8443,5190']), { help: false, ports: [5190, 8443] });
  assert.deepEqual(parseVerifyArguments(['5190', '8443,5190']), { help: false, ports: [5190, 8443] });
  assert.deepEqual(parseVerifyArguments(['--ports=']), { help: false, ports: [] });
  assert.deepEqual(parseVerifyArguments(['--help']), { help: true, ports: DEFAULT_VERIFY_PORTS });
  assert.throws(() => parseVerifyArguments(['--ports=abc']), /Invalid port value/);
  assert.throws(() => parseVerifyArguments(['--ports=70000']), /Port out of range/);
  assert.throws(() => parseVerifyArguments(['--bogus']), /Unknown argument/);
});

test('parseListeningPortsFromProcNet filters to TCP_LISTEN (0A) sockets and requested ports', () => {
  const sampleProcTcp = [
    '  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode',
    '   0: 0100007F:1446 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 12345',
    '   1: 0100007F:144A 0100007F:C350 01 00000000:00000000 00:00000000 00000000  1000        0 12346',
    '   2: 00000000:0CEA 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 12347',
    '',
  ].join('\n');

  assert.deepEqual(parseListeningPortsFromProcNet(sampleProcTcp, [5190, 5194, 8443]), [5190]);
  assert.deepEqual(parseListeningPortsFromProcNet(sampleProcTcp), [3306, 5190]);
});

test('readListeningPorts and collectLinuxVerifyRuntimeReport merge tcp/tcp6 files and reject non-Linux hosts', async () => {
  const scratchDir = mkdtempSync(join(tmpdir(), 'hvac-verify-runtime-'));
  try {
    const tcp4Path = join(scratchDir, 'tcp');
    const tcp6Path = join(scratchDir, 'tcp6');
    writeFileSync(
      tcp4Path,
      '  sl local_address rem_address st\n   0: 0100007F:1446 00000000:0000 0A\n',
    );
    writeFileSync(
      tcp6Path,
      '  sl local_address rem_address st\n   0: 00000000000000000000000000000001:20FB 00000000000000000000000000000000:0000 0A\n',
    );

    const listening = await readListeningPorts([5190, 5194, 8443], [tcp4Path, tcp6Path]);
    assert.deepEqual(listening, [5190, 8443]);

    const report = await collectLinuxVerifyRuntimeReport(['--ports=5190,8443'], {
      platform: 'linux',
      nodeVersion: 'v24.16.0',
      resolveBrowser: () => '/usr/bin/chromium',
      procPaths: [tcp4Path, tcp6Path],
    });
    assert.deepEqual(report, {
      platform: 'linux',
      nodeVersion: 'v24.16.0',
      minimumNodeVersion: 'v22.22.0',
      supportedNode: true,
      browserExecutable: '/usr/bin/chromium',
      checkedPorts: [5190, 8443],
      listeningPorts: [5190, 8443],
    });

    await assert.rejects(
      () => collectLinuxVerifyRuntimeReport([], { platform: 'darwin', procPaths: [tcp4Path] }),
      /requires platform "linux"/,
    );
  } finally {
    rmSync(scratchDir, { recursive: true, force: true });
  }
});
