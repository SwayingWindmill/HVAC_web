import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import {
  countGoSourceLines,
  findGoFileLineViolations,
  isGovernedGoSourceFile,
  type GoFileLinesBaseline,
} from './check-go-file-lines.ts';

const linesOf = (count: number): string =>
  `${Array.from({ length: count }, (_, index) => `// line ${index + 1}`).join('\n')}\n`;

test('isGovernedGoSourceFile includes product/tooling Go files and excludes tests, generated files, and non-Go roots', () => {
  for (const included of [
    'cmd/energy-api/main.go',
    'modules/telemetry/pkg/telemetry/server.go',
    'services/optimization-service/internal/optimization/solver.go',
    'libs/clickhousehttp/client.go',
    'tools/eg8200-simulator/pkg/sim/sim.go',
  ]) {
    assert.equal(isGovernedGoSourceFile(included), true, included);
  }

  for (const excluded of [
    'modules/telemetry/pkg/telemetry/server_test.go',
    'cmd/energy-api/internal/platformapi/api.gen.go',
    'cmd/energy-api/internal/gateway/operations_agent_contract_gen.go',
    'deploy/platform/phase1/cd/verify_test.go',
    'deploy/platform/phase1/cd/helper.go',
    'scripts/check-go-file-lines.ts',
  ]) {
    assert.equal(isGovernedGoSourceFile(excluded), false, excluded);
  }

  assert.equal(countGoSourceLines(''), 0);
  assert.equal(countGoSourceLines('package a\n'), 1);
  assert.equal(countGoSourceLines('package a\r\nfunc f() {}\r\n'), 2);
});

test('Go file-line ratchet enforces <= 800 on new files and strict downward ratchet on legacy caps', () => {
  const scratch = mkdtempSync(join(tmpdir(), 'hvac-go-lines-'));
  try {
    mkdirSync(join(scratch, 'modules/a'), { recursive: true });
    writeFileSync(join(scratch, 'modules/a/legacy.go'), linesOf(850), 'utf8');
    writeFileSync(join(scratch, 'modules/a/small.go'), linesOf(120), 'utf8');

    const validBaseline: GoFileLinesBaseline = {
      schemaVersion: 1,
      policy: 'go-file-lines-ratchet',
      maxLinesPerFile: 800,
      legacyFileCaps: {
        'modules/a/legacy.go': 850,
      },
    };
    assert.deepEqual(
      findGoFileLineViolations(scratch, ['modules/a/legacy.go', 'modules/a/small.go'], validBaseline),
      [],
    );

    // New/unlisted file > 800 fails
    writeFileSync(join(scratch, 'modules/a/new_giant.go'), linesOf(801), 'utf8');
    const newGiantViolations = findGoFileLineViolations(
      scratch,
      ['modules/a/legacy.go', 'modules/a/small.go', 'modules/a/new_giant.go'],
      validBaseline,
    );
    assert.equal(newGiantViolations.length, 1);
    assert.match(newGiantViolations[0], /modules\/a\/new_giant\.go: has 801 lines \(limit 800\)/);

    // Legacy file growing past cap fails
    writeFileSync(join(scratch, 'modules/a/legacy.go'), linesOf(851), 'utf8');
    const grewViolations = findGoFileLineViolations(
      scratch,
      ['modules/a/legacy.go', 'modules/a/small.go'],
      validBaseline,
    );
    assert.equal(grewViolations.length, 1);
    assert.match(grewViolations[0], /grew from 850 to 851 lines/);

    // Legacy file shrinking <= 800 requires removing stale baseline entry
    writeFileSync(join(scratch, 'modules/a/legacy.go'), linesOf(790), 'utf8');
    const shrunkViolations = findGoFileLineViolations(
      scratch,
      ['modules/a/legacy.go', 'modules/a/small.go'],
      validBaseline,
    );
    assert.equal(shrunkViolations.length, 1);
    assert.match(shrunkViolations[0], /stale entry for modules\/a\/legacy\.go \(790 <= 800\)/);

    // Deleted or exempt file in legacyFileCaps fails
    const deletedViolations = findGoFileLineViolations(
      scratch,
      ['modules/a/small.go'],
      validBaseline,
    );
    assert.equal(deletedViolations.length, 1);
    assert.match(deletedViolations[0], /stale entry for missing\/exempt file modules\/a\/legacy\.go/);

    // Stale cap <= maxLinesPerFile in baseline fails
    writeFileSync(join(scratch, 'modules/a/legacy.go'), linesOf(850), 'utf8');
    const staleCapViolations = findGoFileLineViolations(
      scratch,
      ['modules/a/legacy.go', 'modules/a/small.go'],
      {
        schemaVersion: 1,
        policy: 'go-file-lines-ratchet',
        maxLinesPerFile: 800,
        legacyFileCaps: { 'modules/a/legacy.go': 800 },
      },
    );
    assert.equal(staleCapViolations.length, 1);
    assert.match(staleCapViolations[0], /stale cap 800 for modules\/a\/legacy\.go/);
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
});

test('repository satisfies committed Go file-line baseline', () => {
  const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
  const trackedFiles = execFileSync('git', ['ls-files'], {
    cwd: repoRoot,
    encoding: 'utf8',
    maxBuffer: 16 << 20,
  })
    .split(/\r?\n/u)
    .filter(Boolean);
  assert.deepEqual(findGoFileLineViolations(repoRoot, trackedFiles), []);
});
