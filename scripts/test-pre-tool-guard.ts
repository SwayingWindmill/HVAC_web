import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import test from 'node:test';

import {
  evaluateCommandGuard,
  evaluatePreToolPayload,
  isProtectedSecretPath,
  tokenizeShellSegments,
} from './hooks/pre-tool-guard.ts';

const runGuardCli = (runtime: 'agy' | 'claude', payload: unknown) =>
  spawnSync(
    process.execPath,
    ['--experimental-strip-types', 'scripts/hooks/pre-tool-guard.ts', `--runtime=${runtime}`],
    {
      cwd: process.cwd(),
      input: JSON.stringify(payload),
      encoding: 'utf8',
      windowsHide: true,
    },
  );

test('tokenizeShellSegments respects quotes and splits shell chains cleanly', () => {
  assert.deepEqual(
    tokenizeShellSegments('git commit -m "document git reset --hard && git clean -fd" && git push -u origin feat/x'),
    [
      ['git', 'commit', '-m', 'document git reset --hard && git clean -fd'],
      ['git', 'push', '-u', 'origin', 'feat/x'],
    ],
  );
  assert.deepEqual(tokenizeShellSegments('npm test 2>&1 | cat'), [['npm', 'test', '2>&1'], ['cat']]);
});

test('destructive git commands and direct pushes to main/master are denied', () => {
  const branchMain = () => 'main';
  const branchFeat = () => 'feat/pstack-phase1-skills-and-hooks';

  for (const cmd of [
    'git push --force origin feat/x',
    'git push -uf origin feat/x',
    'git push origin main',
    'git push origin HEAD:main',
    'git push origin +HEAD:refs/heads/master',
    'git reset --hard',
    'git reset --hard HEAD~1',
    'git clean -fd',
    'git checkout .',
    'git checkout -- .',
    'git restore .',
    'git restore --worktree .',
    'git branch -D main',
    'wsl -e bash -lc "cd /mnt/e/Code/HVAC_web && git reset --hard"',
    'cmd /c "git clean -fx"',
  ]) {
    const verdict = evaluateCommandGuard(cmd, { resolveCurrentBranch: branchFeat });
    assert.equal(verdict.action, 'deny', `expected deny for: ${cmd}`);
  }

  assert.equal(evaluateCommandGuard('git push', { resolveCurrentBranch: branchMain }).action, 'deny');
  assert.equal(evaluateCommandGuard('git push origin', { resolveCurrentBranch: branchMain }).action, 'deny');
});

test('safe git commands, feature branch deletions, and quoted strings in commits/PR bodies are allowed', () => {
  const branchFeat = () => 'feat/pstack-phase1-skills-and-hooks';

  for (const cmd of [
    'git push -u origin feat/pstack-phase1-skills-and-hooks',
    'git branch -D feat/pstack-phase1-skills-and-hooks',
    'git restore --staged .',
    'git commit -m "document git reset --hard and deploy/platform/phase1/runtime/"',
    'gh pr create --body "git clean -fd"',
  ]) {
    const verdict = evaluateCommandGuard(cmd, { resolveCurrentBranch: branchFeat });
    assert.equal(verdict.action, 'allow', `expected allow for: ${cmd}`);
  }
});

test('protected runtime secret paths are denied while .example templates remain readable', () => {
  for (const secretPath of [
    'deploy/platform/phase1/runtime/postgres.dsn',
    'E:\\Code\\HVAC_web\\deploy\\platform\\phase1\\environments\\server.runtime.env',
    'deploy/platform/phase1/observability/alert-receiver/smtp-password',
    'deploy/platform/phase1/observability/alert-receiver/wecom-webhook-url',
    'deploy/platform/phase1/observability/alert-receiver/smtp.env',
  ]) {
    assert.equal(isProtectedSecretPath(secretPath), true, secretPath);
    assert.equal(evaluateCommandGuard(`cat ${secretPath}`).action, 'deny', secretPath);
    assert.equal(
      evaluatePreToolPayload({ toolCall: { name: 'read_file', args: { Path: secretPath } } }, 'agy').action,
      'deny',
      secretPath,
    );
    assert.equal(
      evaluatePreToolPayload({ tool_name: 'Read', tool_input: { file_path: secretPath } }, 'claude').action,
      'deny',
      secretPath,
    );
  }

  const examplePath = 'deploy/platform/phase1/observability/alert-receiver.example/smtp.env.example';
  assert.equal(isProtectedSecretPath(examplePath), false);
  assert.equal(evaluateCommandGuard(`cat ${examplePath}`).action, 'allow');
  assert.equal(
    evaluatePreToolPayload({ toolCall: { name: 'read_file', args: { Path: examplePath } } }, 'agy').action,
    'allow',
  );
});

test('production deploys and local database resets require interactive approval', () => {
  for (const cmd of [
    'gh workflow run deploy-production.yml',
    'npm run local:reset',
    'node scripts/local-environment.mjs reset',
    'docker compose -f deploy/platform/phase1/compose.yaml down -v',
  ]) {
    assert.equal(evaluateCommandGuard(cmd).action, 'ask', cmd);
  }
});

test('CLI translates verdicts cleanly for both agy and claude runtimes', () => {
  const agyDeny = runGuardCli('agy', { toolCall: { name: 'run_command', args: { CommandLine: 'git reset --hard' } } });
  assert.equal(agyDeny.status, 0);
  assert.equal(JSON.parse(agyDeny.stdout).decision, 'deny');

  const agyAsk = runGuardCli('agy', { toolCall: { name: 'run_command', args: { CommandLine: 'npm run local:reset' } } });
  assert.equal(agyAsk.status, 0);
  assert.equal(JSON.parse(agyAsk.stdout).decision, 'force_ask');

  const agyAllow = runGuardCli('agy', { toolCall: { name: 'run_command', args: { CommandLine: 'git status --short' } } });
  assert.equal(agyAllow.status, 0);
  assert.equal(JSON.parse(agyAllow.stdout).decision, 'allow');

  const claudeDeny = runGuardCli('claude', { tool_name: 'Bash', tool_input: { command: 'git reset --hard' } });
  assert.equal(claudeDeny.status, 2);
  assert.match(claudeDeny.stderr, /BLOCKED by scripts\/hooks\/pre-tool-guard\.ts/);

  const claudeAsk = runGuardCli('claude', { tool_name: 'Bash', tool_input: { command: 'gh workflow run deploy-production.yml' } });
  assert.equal(claudeAsk.status, 0);
  assert.equal(JSON.parse(claudeAsk.stdout).hookSpecificOutput.permissionDecision, 'ask');
});
