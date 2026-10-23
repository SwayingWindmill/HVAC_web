// Migrations already registered on main have run on the server and are frozen: their content
// never changes, and one leaves the list only when a remaining migration drops its tables.

import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

const normalizeIdentifier = (raw: string): string => raw.replaceAll('"', '').toLowerCase();
const withoutComments = (sql: string): string => sql.replace(/\/\*[\s\S]*?\*\//gu, '').replace(/--[^\n]*/gu, '');

const createdTables = (sql: string): string[] =>
  [...sql.matchAll(/\bcreate\s+(?:unlogged\s+)?table\s+(?:if\s+not\s+exists\s+)?([\w."]+)/giu)].map((match) =>
    normalizeIdentifier(match[1]),
  );

const droppedTables = (sql: string): string[] =>
  [...withoutComments(sql).matchAll(/\bdrop\s+table\s+(?:if\s+exists\s+)?([^;]+)/giu)].flatMap((match) =>
    match[1]
      .replace(/\b(?:cascade|restrict)\b/giu, '')
      .split(',')
      .map((table) => normalizeIdentifier(table.trim())),
  );

const droppedSchemas = (sql: string): string[] =>
  [...withoutComments(sql).matchAll(/\bdrop\s+schema\s+(?:if\s+exists\s+)?([^;]+?)\s+cascade\b/giu)].flatMap((match) =>
    match[1].split(',').map((schema) => normalizeIdentifier(schema.trim())),
  );

// The registered migration paths, and a reader that returns undefined for a path whose file is gone.
export type MigrationTree = { list: string[]; read: (path: string) => string | undefined };

export function findFreezeViolations(base: MigrationTree, head: MigrationTree): string[] {
  const violations: string[] = [];
  const remaining = head.list.map((path) => head.read(path)).filter((sql): sql is string => sql !== undefined);
  const droppedTableNames = new Set(remaining.flatMap(droppedTables));
  const droppedSchemaNames = new Set(remaining.flatMap(droppedSchemas));
  const isDropped = (table: string): boolean => droppedTableNames.has(table) || droppedSchemaNames.has(table.split('.')[0]);
  for (const path of base.list) {
    if (head.list.includes(path)) {
      if (head.read(path) !== base.read(path)) {
        violations.push(`${path} was already registered; its content must not change. Add a new migration instead.`);
      }
      continue;
    }
    for (const table of createdTables(base.read(path) ?? '')) {
      if (!isDropped(table)) {
        violations.push(`${path} was removed from the list, but no remaining migration drops ${table}.`);
      }
    }
  }
  return violations;
}

const listPath = 'deploy/platform/phase1/migrations/migration-list.tsv';
const parseList = (text: string): string[] => text.split(/\r?\n/u).filter(Boolean).map((line) => line.split('|')[1]);

const git = (...args: string[]): string => {
  const result = spawnSync('git', args, { encoding: 'utf8', maxBuffer: 64 << 20 });
  if (result.status !== 0) throw new Error(`git ${args.join(' ')} failed: ${result.stderr.trim()}`);
  return result.stdout;
};

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const forkPoint = git('merge-base', process.env.GITHUB_BASE_SHA || 'origin/main', 'HEAD').trim();
  const base: MigrationTree = { list: parseList(git('show', `${forkPoint}:${listPath}`)), read: (path) => git('show', `${forkPoint}:${path}`) };
  const head: MigrationTree = {
    list: parseList(readFileSync(listPath, 'utf8')),
    read: (path) => (existsSync(path) ? readFileSync(path, 'utf8') : undefined),
  };
  const violations = findFreezeViolations(base, head);
  if (violations.length > 0) {
    console.error(violations.join('\n'));
    process.exit(1);
  }
  console.log(`Registered migrations unchanged since ${forkPoint.slice(0, 8)}.`);
}
