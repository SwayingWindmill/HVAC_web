import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import test from 'node:test';

import { findFreezeViolations, type MigrationTree } from './check-migration-freeze.ts';

const checkScript = resolve('scripts/check-migration-freeze.ts');

const tree = (files: Record<string, string | undefined>): MigrationTree => ({
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

test('adding a migration is allowed', () => {
  const base = tree({ 'infra/a/001.sql': 'CREATE TABLE a.items (id int);\n' });
  const head = tree({ 'infra/a/001.sql': 'CREATE TABLE a.items (id int);\n', 'infra/a/002.sql': 'ALTER TABLE a.items ADD COLUMN name text;\n' });

  assert.deepEqual(findFreezeViolations(base, head), []);
});

test('deleting the file of a migration that is still registered is a violation', () => {
  const base = tree({ 'infra/a/001.sql': 'CREATE TABLE a.items (id int);\n' });
  const head = tree({ 'infra/a/001.sql': undefined });

  assert.deepEqual(findFreezeViolations(base, head), [
    'infra/a/001.sql was already registered; its content must not change. Add a new migration instead.',
  ]);
});

test('a DROP inside a comment does not drop anything', () => {
  const base = tree({ 'infra/a/001.sql': 'CREATE TABLE a.items (id int);\n' });
  const head = tree({ 'infra/a/002.sql': '-- DROP TABLE a.items;\n/* DROP SCHEMA a CASCADE; */\n' });

  assert.deepEqual(findFreezeViolations(base, head), [
    'infra/a/001.sql was removed from the list, but no remaining migration drops a.items.',
  ]);
});

test('dropping the schema with CASCADE drops every table in it', () => {
  const base = tree({ 'services/rules/001.sql': 'CREATE TABLE rule_runtime.rules (id int);\n' });
  const head = tree({ 'infra/drop.sql': 'DROP SCHEMA IF EXISTS rule_runtime CASCADE;\n' });

  assert.deepEqual(findFreezeViolations(base, head), []);
});

test('ClickHouse DROP DATABASE/TABLE SYNC and backtick identifiers are recognized', () => {
  const baseDb = tree({
    'infra/telemetry/clickhouse/init/001.sql': 'CREATE TABLE IF NOT EXISTS telemetry_history.`obs_v1` (id UInt64) ENGINE = MergeTree ORDER BY id;\n',
  });
  const headDb = tree({ 'infra/telemetry/clickhouse/init/002.sql': 'DROP DATABASE IF EXISTS telemetry_history SYNC;\n' });
  assert.deepEqual(findFreezeViolations(baseDb, headDb), []);

  const headTable = tree({ 'infra/telemetry/clickhouse/init/002.sql': 'DROP TABLE IF EXISTS telemetry_history.`obs_v1` SYNC;\n' });
  assert.deepEqual(findFreezeViolations(baseDb, headTable), []);
});

async function withTempMigrationRepo(
  prefix: string,
  fn: (ctx: {
    git: (...args: string[]) => void;
    write: (path: string, content: string) => Promise<void>;
    check: () => ReturnType<typeof spawnSync>;
  }) => Promise<void>,
): Promise<void> {
  const repo = await mkdtemp(join(tmpdir(), prefix));
  const env: NodeJS.ProcessEnv = { ...process.env, GITHUB_BASE_SHA: 'main' };
  delete env.GIT_DIR;
  delete env.GIT_WORK_TREE;
  const git = (...args: string[]): void => {
    const result = spawnSync('git', ['-c', 'user.name=test', '-c', 'user.email=test@example.com', ...args], {
      cwd: repo,
      encoding: 'utf8',
      env,
    });
    assert.equal(result.status, 0, result.stderr);
  };
  const write = async (path: string, content: string): Promise<void> => {
    await mkdir(dirname(join(repo, path)), { recursive: true });
    await writeFile(join(repo, path), content);
  };
  const check = () => spawnSync(process.execPath, ['--experimental-strip-types', checkScript], { cwd: repo, encoding: 'utf8', env });
  try {
    await fn({ git, write, check });
  } finally {
    await rm(repo, { recursive: true, force: true });
  }
}

test('the command compares the branch with where it left main', async () => {
  await withTempMigrationRepo('migration-freeze-', async ({ git, write, check }) => {
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
    assert.match(String(result.stderr), /infra\/a\/001\.sql was already registered/u);
  });
});

test('editing a registered ClickHouse migration fails freeze check while appending a new one passes', async () => {
  await withTempMigrationRepo('ch-migration-freeze-', async ({ git, write, check }) => {
    git('init', '--quiet', '--initial-branch=main');
    await write(
      'deploy/platform/phase1/clickhouse-migrations/clickhouse-migration-list.tsv',
      'clickhouse|infra/telemetry/clickhouse/init/025-clickhouse-complete-governance.sql\n',
    );
    await write(
      'infra/telemetry/clickhouse/init/025-clickhouse-complete-governance.sql',
      'CREATE TABLE telemetry_history.observations (id UInt64) ENGINE = MergeTree ORDER BY id;\n',
    );
    git('add', '.');
    git('commit', '--quiet', '-m', 'base');
    git('switch', '--quiet', '-c', 'feature');

    await write(
      'deploy/platform/phase1/clickhouse-migrations/clickhouse-migration-list.tsv',
      'clickhouse|infra/telemetry/clickhouse/init/025-clickhouse-complete-governance.sql\nclickhouse|infra/telemetry/clickhouse/init/026-extra.sql\n',
    );
    await write('infra/telemetry/clickhouse/init/026-extra.sql', 'SELECT 1;\n');
    git('add', '.');
    git('commit', '--quiet', '-m', 'add clickhouse migration 026');
    assert.equal(check().status, 0);

    await write(
      'infra/telemetry/clickhouse/init/025-clickhouse-complete-governance.sql',
      'CREATE TABLE telemetry_history.observations (id UInt64) ENGINE = MergeTree ORDER BY id SETTINGS max_threads = 1;\n',
    );
    git('commit', '--quiet', '-am', 'edit registered 025');
    const result = check();
    assert.equal(result.status, 1);
    assert.match(
      String(result.stderr),
      /infra\/telemetry\/clickhouse\/init\/025-clickhouse-complete-governance\.sql was already registered/u,
    );
  });
});
