// Migrations already registered on main have run on the server and are frozen: their content
// never changes, and one leaves the list only when a remaining migration drops its tables.

import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

const name = (raw) => raw.replaceAll('"', '').toLowerCase();

const createdTables = (sql) =>
  [...sql.matchAll(/\bcreate\s+(?:unlogged\s+)?table\s+(?:if\s+not\s+exists\s+)?([\w."]+)/giu)].map((match) => name(match[1]));

const droppedTables = (sql) =>
  [...sql.matchAll(/\bdrop\s+table\s+(?:if\s+exists\s+)?([^;]+)/giu)].flatMap((match) =>
    match[1]
      .replace(/\b(?:cascade|restrict)\b/giu, '')
      .split(',')
      .map((table) => name(table.trim())),
  );

const droppedSchemas = (sql) =>
  [...sql.matchAll(/\bdrop\s+schema\s+(?:if\s+exists\s+)?([^;]+?)\s+cascade\b/giu)].flatMap((match) =>
    match[1].split(',').map((schema) => name(schema.trim())),
  );

export function findFreezeViolations(base, head) {
  const violations = [];
  const remaining = head.list.map((path) => head.read(path));
  const dropped = new Set(remaining.flatMap(droppedTables));
  const droppedSchema = new Set(remaining.flatMap(droppedSchemas));
  const isDropped = (table) => dropped.has(table) || droppedSchema.has(table.split('.')[0]);
  for (const path of base.list) {
    if (head.list.includes(path)) {
      if (head.read(path) !== base.read(path)) {
        violations.push(`${path} was already registered; its content must not change. Add a new migration instead.`);
      }
      continue;
    }
    for (const table of createdTables(base.read(path))) {
      if (!isDropped(table)) {
        violations.push(`${path} was removed from the list, but no remaining migration drops ${table}.`);
      }
    }
  }
  return violations;
}

const listPath = 'deploy/platform/phase1/migrations/migration-list.tsv';
const parseList = (text) => text.split(/\r?\n/u).filter(Boolean).map((line) => line.split('|')[1]);

const git = (...args) => {
  const result = spawnSync('git', args, { encoding: 'utf8', maxBuffer: 64 << 20 });
  if (result.status !== 0) throw new Error(`git ${args.join(' ')} failed: ${result.stderr.trim()}`);
  return result.stdout;
};

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const baseArgument = process.argv.slice(2).find((argument) => argument.startsWith('--base='));
  const baseRef = baseArgument?.slice('--base='.length) || process.env.GITHUB_BASE_SHA || 'origin/main';
  const forkPoint = git('merge-base', baseRef, 'HEAD').trim();
  const base = { list: parseList(git('show', `${forkPoint}:${listPath}`)), read: (path) => git('show', `${forkPoint}:${path}`) };
  const head = { list: parseList(readFileSync(listPath, 'utf8')), read: (path) => readFileSync(path, 'utf8') };
  const violations = findFreezeViolations(base, head);
  if (violations.length > 0) {
    console.error(violations.join('\n'));
    process.exit(1);
  }
  console.log(`Registered migrations unchanged since ${forkPoint.slice(0, 8)}.`);
}
