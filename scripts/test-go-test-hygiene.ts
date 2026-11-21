import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import test from 'node:test';

import {
  countTimeSleeps,
  findGoTestHygieneViolations,
  findGoTestSleepRatchetViolations,
  findUnsynchronizedLogBufferViolations,
} from './check-go-test-hygiene.ts';

test('time.Sleep ratchet rejects new test sleeps and baseline increases while ignoring comments/strings', () => {
  const scratch = mkdtempSync(join(tmpdir(), 'hvac-go-sleep-'));
  try {
    mkdirSync(join(scratch, 'pkg/a'), { recursive: true });
    writeFileSync(
      join(scratch, 'pkg/a/existing_test.go'),
      `package a
import "time"
// time.Sleep(time.Second) inside comment
const msg = "time.Sleep(time.Second)"
func TestExisting() {
	time.Sleep(10 * time.Millisecond)
}
`,
    );
    assert.equal(
      countTimeSleeps(`// time.Sleep(1)\nconst x = "time.Sleep(2)"\nfunc f() { time.Sleep(3) }\n`),
      1,
    );

    const baseline = { maxTotal: 1, counts: { 'pkg/a/existing_test.go': 1 } };
    assert.deepEqual(
      findGoTestSleepRatchetViolations({
        root: scratch,
        trackedFiles: ['pkg/a/existing_test.go'],
        baseline,
      }),
      [],
    );

    writeFileSync(
      join(scratch, 'pkg/a/new_test.go'),
      `package a
import "time"
func TestFlakySleep() {
	time.Sleep(50 * time.Millisecond)
}
`,
    );
    const newSleepViolations = findGoTestSleepRatchetViolations({
      root: scratch,
      trackedFiles: ['pkg/a/existing_test.go', 'pkg/a/new_test.go'],
      baseline,
    });
    assert.ok(
      newSleepViolations.some((v) => v.includes('pkg/a/new_test.go') && v.includes('baseline 0')),
      `expected new sleep rejection, got: ${newSleepViolations.join(' | ')}`,
    );
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
});

test('unsynchronized bytes.Buffer logger check catches PR #875/#911 race pattern and allows syncBuffer/synchronous handlers', () => {
  const buggyConcurrentTest = `package worker
import (
	"bytes"
	"log/slog"
)
func TestConcurrentLogs() {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	go func() {
		logger.Info("tick")
	}()
	_ = buf.String()
}
`;
  const violations = findUnsynchronizedLogBufferViolations('pkg/worker/main_test.go', buggyConcurrentTest);
  assert.ok(
    violations.some((v) => v.includes('unsynchronized bytes.Buffer') && v.includes('sync.Mutex')),
    `expected concurrent bytes.Buffer violation, got: ${violations.join(' | ')}`,
  );

  const safeSyncBufferTest = `package worker
import (
	"bytes"
	"log/slog"
	"sync"
)
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}
func TestSafeConcurrentLogs() {
	var buf syncBuffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	go func() {
		logger.Info("tick")
	}()
}
func TestSafeSyncHandler() {
	var local bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&local, nil))
	logger.Info("sync")
}
`;
  assert.deepEqual(findUnsynchronizedLogBufferViolations('pkg/worker/main_test.go', safeSyncBufferTest), []);
});

test('current repository Go tests satisfy sleep ratchet and concurrent log-buffer hygiene', () => {
  const root = resolve(process.cwd());
  const tracked = execFileSync('git', ['ls-files'], { cwd: root, encoding: 'utf8', maxBuffer: 16 << 20 })
    .split(/\r?\n/u)
    .filter(Boolean);
  assert.deepEqual(findGoTestHygieneViolations(root, tracked), []);
});
