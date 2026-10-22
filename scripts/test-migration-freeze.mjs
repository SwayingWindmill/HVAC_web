import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import test from 'node:test';

import { findFreezeViolations } from './check-migration-freeze.mjs';

const checkScript = resolve('scripts/check-migration-freeze.mjs');

const tree = (files) => ({
  list: Object.keys(files),
  read: (path) => files[path],
});

test('editing a registered migration is a violation', () => {
  const base = tree({ 'infra/a/001.sql': 'CREATE TABLE a.items (id int);\n' });
  const head = tree({ 'infra/a/001.sql': 'CREATE TABLE a.items (id bigint);\n' });

  assert.deepEqual(findFreezeViolations(base, head), [
    'infra/a/001.sql was already registered; its content must not change. Add a new migration instead.',
  ]);
});

test('removing a registered migration without dropping its tables is a violation', () => {
  const base = tree({
    'infra/a/001.sql': 'CREATE TABLE IF NOT EXISTS a.items (id int);\nCREATE TABLE a.notes (id int);\n',
  });
  const head = tree({ 'infra/a/002.sql': 'DROP TABLE a.notes;\n' });

  assert.deepEqual(findFreezeViolations(base, head), [
    'infra/a/001.sql was removed from the list, but no remaining migration drops a.items.',
  ]);
});

test('dropping the schema with CASCADE drops every table in it', () => {
  const base = tree({ 'services/rules/001.sql': 'CREATE TABLE rule_runtime.rules (id int);\n' });
  const head = tree({ 'infra/drop.sql': 'DROP SCHEMA IF EXISTS rule_runtime CASCADE;\n' });

  assert.deepEqual(findFreezeViolations(base, head), []);
});

test('the command compares the branch with where it left main', async () => {
  const repo = await mkdtemp(join(tmpdir(), 'migration-freeze-'));
  const env = { ...process.env };
  delete env.GIT_DIR;
  delete env.GIT_WORK_TREE;
  const git = (...args) => {
    const result = spawnSync('git', ['-c', 'user.name=test', '-c', 'user.email=test@example.com', ...args], { cwd: repo, encoding: 'utf8', env });
    assert.equal(result.status, 0, result.stderr);
  };
  const write = async (path, content) => {
    await mkdir(dirname(join(repo, path)), { recursive: true });
    await writeFile(join(repo, path), content);
  };
  const check = () => spawnSync(process.execPath, [checkScript, '--base=main'], { cwd: repo, encoding: 'utf8', env });
  try {
    git('init', '--quiet', '--initial-branch=main');
    await write('deploy/platform/phase1/migrations/migration-list.tsv', 'hvac_s1|infra/a/001.sql\n');
    await write('infra/a/001.sql', 'CREATE TABLE a.items (id int);\n');
    git('add', '.');
    git('commit', '--quiet', '-m', 'base');
    git('switch', '--quiet', '-c', 'feature');

    assert.equal(check().status, 0);

    await write('infra/a/001.sql', 'CREATE TABLE a.items (id bigint);\n');
    git('commit', '--quiet', '-am', 'edit a registered migration');
    const result = check();
    assert.equal(result.status, 1);
    assert.match(result.stderr, /infra\/a\/001\.sql was already registered/u);
  } finally {
    await rm(repo, { recursive: true, force: true });
  }
});
