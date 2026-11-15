import { existsSync, readFileSync } from 'node:fs';
import { basename, join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

export interface RegisteredMigration {
  readonly database: string;
  readonly path: string;
}

export interface MigrationConventionBaseline {
  readonly schemaVersion: 1;
  readonly maxGrandfatheredMigrations: number;
  readonly legacyDatabases: readonly string[];
  readonly grandfatheredMigrations: readonly string[];
}

export interface ConventionCheckInput {
  readonly migrations: readonly RegisteredMigration[];
  readonly files: Readonly<Record<string, string | undefined>>;
  readonly bootstrapSql?: string;
  readonly baseline: MigrationConventionBaseline;
}

export interface MigrationConventionResult {
  readonly violations: readonly string[];
  readonly liveSchemas: ReadonlyMap<string, string | undefined>;
  readonly liveTables: ReadonlyMap<string, string | undefined>;
}

type SqlToken =
  | { readonly type: 'ident'; readonly value: string; readonly upper: string; readonly quoted: boolean }
  | { readonly type: 'string'; readonly value: string }
  | { readonly type: 'dot' }
  | { readonly type: 'comma' }
  | { readonly type: 'other'; readonly value: string };

type ParsedStatement =
  | { readonly kind: 'meta'; readonly command: string; readonly arg: string }
  | { readonly kind: 'sql'; readonly tokens: readonly SqlToken[] };

const SNAKE_CASE_PATTERN = /^[a-z][a-z0-9_]*$/;
const VALID_DATABASE_PATTERN = /^hvac_[a-z][a-z0-9_]*$/;
const STAGE_DATABASE_PATTERN = /^hvac_s\d+$/;
const VALID_FILENAME_PATTERN = /^\d{3}[a-z]?[-_][a-z0-9]+(?:[-_][a-z0-9]+)*\.sql$/;
const STAGE_FILENAME_PATTERN = /(?:^|[-_])(?:s\d+|backup|tmp)(?:[-_.]|$)/;
const FORBIDDEN_IDENTIFIER_PATTERN = /(?:^|_)(?:s\d+|pre_s\d+|backup|tmp)(?:_|$)|(?:migration_report|identity_map)/;
const FORBIDDEN_ROLE_PATTERN = /(?:^|_)(?:s\d+|tmp|backup)(?:_|$)|local_only|fixture/;
const CHINESE_CHAR_PATTERN = /[\u4e00-\u9fff]/u;
const DOLLAR_TAG_PATTERN = /^\$(?:[A-Za-z_][A-Za-z0-9_]*)?\$/;

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const readNonNegativeInt = (record: Record<string, unknown>, field: string): number => {
  const value = record[field];
  if (typeof value !== 'number' || !Number.isInteger(value) || value < 0) {
    throw new Error(`Invalid ${field} in migration-conventions-baseline.json: expected non-negative integer.`);
  }
  return value;
};

const readStringArray = (record: Record<string, unknown>, field: string): string[] => {
  const value = record[field];
  if (!Array.isArray(value) || !value.every((entry) => typeof entry === 'string')) {
    throw new Error(`Invalid ${field} in migration-conventions-baseline.json: expected string array.`);
  }
  return [...value];
};

export const parseBaselineJson = (rawJson: string): MigrationConventionBaseline => {
  const parsed: unknown = JSON.parse(rawJson);
  if (!isRecord(parsed)) {
    throw new Error('migration-conventions-baseline.json must contain a JSON object.');
  }
  if (parsed.schemaVersion !== 1) {
    throw new Error('migration-conventions-baseline.json schemaVersion must equal 1.');
  }
  return {
    schemaVersion: 1,
    maxGrandfatheredMigrations: readNonNegativeInt(parsed, 'maxGrandfatheredMigrations'),
    legacyDatabases: readStringArray(parsed, 'legacyDatabases'),
    grandfatheredMigrations: readStringArray(parsed, 'grandfatheredMigrations'),
  };
};

export const parseMigrationListTsv = (content: string): RegisteredMigration[] => {
  const migrations: RegisteredMigration[] = [];
  for (const rawLine of content.split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line || line.startsWith('#')) continue;
    const pipeIndex = line.indexOf('|');
    if (pipeIndex <= 0) {
      throw new Error(`Malformed migration-list.tsv line: "${line}"`);
    }
    const database = line.slice(0, pipeIndex).trim();
    const path = line.slice(pipeIndex + 1).trim();
    if (!database || !path) {
      throw new Error(`Incomplete migration-list.tsv line: "${line}"`);
    }
    migrations.push({ database, path });
  }
  return migrations;
};

const parseSqlStatements = (sql: string): ParsedStatement[] => {
  const statements: ParsedStatement[] = [];
  let tokens: SqlToken[] = [];
  let i = 0;

  const flushStatement = (): void => {
    if (tokens.length > 0) {
      statements.push({ kind: 'sql', tokens });
      tokens = [];
    }
  };

  while (i < sql.length) {
    const ch = sql[i];

    if (ch === '\\' && tokens.length === 0) {
      const prevNewline = sql.lastIndexOf('\n', i - 1);
      const before = sql.slice(prevNewline + 1, i);
      if (/^[ \t]*$/.test(before)) {
        const lineEnd = sql.indexOf('\n', i);
        const lineText = (lineEnd === -1 ? sql.slice(i) : sql.slice(i, lineEnd)).trim();
        const spaceMatch = /^\\(\S+)(?:\s+(.*))?$/.exec(lineText);
        if (spaceMatch) {
          statements.push({
            kind: 'meta',
            command: spaceMatch[1].toLowerCase(),
            arg: (spaceMatch[2] ?? '').replace(/;$/, '').trim(),
          });
        }
        i = lineEnd === -1 ? sql.length : lineEnd + 1;
        continue;
      }
    }

    if (/\s/.test(ch)) {
      i += 1;
      continue;
    }

    if (sql.startsWith('--', i)) {
      const lineEnd = sql.indexOf('\n', i + 2);
      i = lineEnd === -1 ? sql.length : lineEnd + 1;
      continue;
    }

    if (sql.startsWith('/*', i)) {
      let depth = 1;
      i += 2;
      while (i < sql.length && depth > 0) {
        if (sql.startsWith('/*', i)) {
          depth += 1;
          i += 2;
        } else if (sql.startsWith('*/', i)) {
          depth -= 1;
          i += 2;
        } else {
          i += 1;
        }
      }
      continue;
    }

    if (ch === '$') {
      const tagMatch = DOLLAR_TAG_PATTERN.exec(sql.slice(i));
      if (tagMatch) {
        const tag = tagMatch[0];
        const closeIndex = sql.indexOf(tag, i + tag.length);
        const body = closeIndex === -1 ? sql.slice(i + tag.length) : sql.slice(i + tag.length, closeIndex);
        tokens.push({ type: 'string', value: body });
        i = closeIndex === -1 ? sql.length : closeIndex + tag.length;
        continue;
      }
    }

    const isEscapeString = (ch === 'e' || ch === 'E') && sql[i + 1] === "'";
    if (ch === "'" || isEscapeString) {
      let value = '';
      i += isEscapeString ? 2 : 1;
      while (i < sql.length) {
        const current = sql[i];
        if (isEscapeString && current === '\\' && i + 1 < sql.length) {
          value += sql[i + 1];
          i += 2;
          continue;
        }
        if (current === "'") {
          if (sql[i + 1] === "'") {
            value += "'";
            i += 2;
            continue;
          }
          i += 1;
          break;
        }
        value += current;
        i += 1;
      }
      tokens.push({ type: 'string', value });
      continue;
    }

    if (ch === '"') {
      let ident = '';
      i += 1;
      while (i < sql.length) {
        if (sql[i] === '"') {
          if (sql[i + 1] === '"') {
            ident += '"';
            i += 2;
            continue;
          }
          i += 1;
          break;
        }
        ident += sql[i];
        i += 1;
      }
      tokens.push({ type: 'ident', value: ident, upper: ident.toUpperCase(), quoted: true });
      continue;
    }

    if (/[A-Za-z_]/.test(ch)) {
      let end = i + 1;
      while (end < sql.length && /[A-Za-z0-9_$]/.test(sql[end])) end += 1;
      const raw = sql.slice(i, end);
      tokens.push({ type: 'ident', value: raw.toLowerCase(), upper: raw.toUpperCase(), quoted: false });
      i = end;
      continue;
    }

    if (ch === '.') {
      tokens.push({ type: 'dot' });
      i += 1;
      continue;
    }

    if (ch === ',') {
      tokens.push({ type: 'comma' });
      i += 1;
      continue;
    }

    if (ch === ';') {
      flushStatement();
      i += 1;
      continue;
    }

    tokens.push({ type: 'other', value: ch });
    i += 1;
  }

  flushStatement();
  return statements;
};

const kwAt = (tokens: readonly SqlToken[], index: number): string | undefined => {
  const token = tokens[index];
  return token && token.type === 'ident' && !token.quoted ? token.upper : undefined;
};

interface QualifiedName {
  readonly schema: string | undefined;
  readonly name: string;
  readonly nextIndex: number;
}

const readQualifiedName = (tokens: readonly SqlToken[], index: number): QualifiedName | undefined => {
  const first = tokens[index];
  if (!first || first.type !== 'ident') return undefined;
  const dot = tokens[index + 1];
  const second = tokens[index + 2];
  if (dot && dot.type === 'dot' && second && second.type === 'ident') {
    return { schema: first.value, name: second.value, nextIndex: index + 3 };
  }
  return { schema: undefined, name: first.value, nextIndex: index + 1 };
};

const schemaStem = (schema: string): string => schema.replace(/_(?:runtime|operations)$/, '');

const stripRedundantPrefix = (table: string, schema: string): string | undefined => {
  if (table.startsWith(`${schema}_`) && table.length > schema.length + 1) {
    return table.slice(schema.length + 1);
  }
  const stem = schemaStem(schema);
  if (stem.length > 0 && table.startsWith(`${stem}_`) && table.length > stem.length + 1) {
    return table.slice(stem.length + 1);
  }
  return undefined;
};

const validateObjectName = (
  violations: string[],
  migrationPath: string,
  kind: 'schema' | 'table' | 'view' | 'index',
  name: string,
): void => {
  if (!SNAKE_CASE_PATTERN.test(name)) {
    violations.push(`${migrationPath}: ${kind} name "${name}" must use lower snake_case matching ${SNAKE_CASE_PATTERN.source}.`);
    return;
  }
  if (FORBIDDEN_IDENTIFIER_PATTERN.test(name)) {
    violations.push(
      `${migrationPath}: ${kind} name "${name}" contains forbidden stage or backup token (s<n>, pre_s<n>, backup, tmp, migration_report, identity_map). Rename it to a domain noun.`,
    );
  }
};

const validateTableRules = (
  violations: string[],
  migrationPath: string,
  schema: string,
  table: string,
): void => {
  validateObjectName(violations, migrationPath, 'schema', schema);
  validateObjectName(violations, migrationPath, 'table', table);
  const suggested = stripRedundantPrefix(table, schema);
  if (suggested) {
    violations.push(
      `${migrationPath}: table "${schema}.${table}" repeats schema prefix "${schemaStem(schema)}_"; rename it to "${schema}.${suggested}".`,
    );
  }
};

const validateMigrationFileName = (violations: string[], migrationPath: string): void => {
  const fileName = basename(migrationPath);
  if (!VALID_FILENAME_PATTERN.test(fileName)) {
    violations.push(
      `${migrationPath}: migration filename "${fileName}" must match <NNN>-<domain-topic>.sql or <NNN>_<domain_topic>.sql in lowercase.`,
    );
    return;
  }
  if (STAGE_FILENAME_PATTERN.test(fileName)) {
    violations.push(
      `${migrationPath}: migration filename "${fileName}" encodes a stage or temporary tag (s<n>, backup, tmp). Name migrations after the domain change instead.`,
    );
  }
};

const seedBootstrapSchemas = (
  bootstrapSql: string | undefined,
  activeDatabases: ReadonlySet<string>,
  legacyDatabases: ReadonlySet<string>,
  liveSchemas: Map<string, string | undefined>,
  violations: string[],
): void => {
  if (!bootstrapSql) return;
  let currentDatabase = '';
  for (const stmt of parseSqlStatements(bootstrapSql)) {
    if (stmt.kind === 'meta') {
      if (stmt.command === 'connect' || stmt.command === 'c') {
        currentDatabase = stmt.arg.split(/\s+/)[0] ?? '';
      }
      continue;
    }
    if (!currentDatabase || !activeDatabases.has(currentDatabase)) continue;
    if (kwAt(stmt.tokens, 0) === 'CREATE' && kwAt(stmt.tokens, 1) === 'SCHEMA') {
      let pos = 2;
      if (kwAt(stmt.tokens, pos) === 'IF' && kwAt(stmt.tokens, pos + 1) === 'NOT' && kwAt(stmt.tokens, pos + 2) === 'EXISTS') {
        pos += 3;
      }
      const schemaTok = stmt.tokens[pos];
      if (schemaTok && schemaTok.type === 'ident') {
        const key = `${currentDatabase}:${schemaTok.value}`;
        if (!liveSchemas.has(key)) liveSchemas.set(key, undefined);
        if (!legacyDatabases.has(currentDatabase)) {
          validateObjectName(violations, 'deploy/platform/phase1/migrations/bootstrap-roles.sql', 'schema', schemaTok.value);
        }
      }
    }
  }
};

const readChineseComment = (valueTok: SqlToken | undefined): string | undefined => {
  if (valueTok?.type !== 'string') return undefined;
  const trimmed = valueTok.value.trim();
  return trimmed.length > 0 && CHINESE_CHAR_PATTERN.test(trimmed) ? trimmed : undefined;
};

const applyCommentTarget = ({
  kind,
  targetKey,
  displayName,
  valueTok,
  strict,
  migrationPath,
  liveTargets,
  violations,
}: {
  readonly kind: 'SCHEMA' | 'TABLE';
  readonly targetKey: string;
  readonly displayName: string;
  readonly valueTok: SqlToken | undefined;
  readonly strict: boolean;
  readonly migrationPath: string;
  readonly liveTargets: Map<string, string | undefined>;
  readonly violations: string[];
}): void => {
  if (!liveTargets.has(targetKey)) {
    if (strict) {
      violations.push(
        `${migrationPath}: COMMENT ON ${kind} "${displayName}" targets an unknown or dropped ${kind.toLowerCase()}.`,
      );
    }
    return;
  }
  const rawComment = valueTok?.type === 'string' ? valueTok.value.trim() : '';
  const validComment = readChineseComment(valueTok);
  if (strict && rawComment.length > 0 && !validComment) {
    violations.push(`${migrationPath}: COMMENT ON ${kind} "${displayName}" must be written in Chinese.`);
  }
  liveTargets.set(targetKey, validComment);
};

const handleSqlStatement = ({
  database,
  migrationPath,
  tokens,
  strict,
  liveSchemas,
  liveTables,
  violations,
}: {
  database: string;
  migrationPath: string;
  tokens: readonly SqlToken[];
  strict: boolean;
  liveSchemas: Map<string, string | undefined>;
  liveTables: Map<string, string | undefined>;
  violations: string[];
}): void => {
  const head = kwAt(tokens, 0);
  if (!head) return;

  if (head === 'CREATE') {
    let pos = 1;
    if (kwAt(tokens, pos) === 'OR' && kwAt(tokens, pos + 1) === 'REPLACE') pos += 2;

    let isTemp = false;
    while (pos < tokens.length) {
      const mod = kwAt(tokens, pos);
      if (mod === 'TEMP' || mod === 'TEMPORARY') {
        isTemp = true;
        pos += 1;
      } else if (mod === 'LOCAL' || mod === 'GLOBAL' || mod === 'UNLOGGED') {
        pos += 1;
      } else {
        break;
      }
    }

    const kind = kwAt(tokens, pos);

    if (kind === 'DATABASE') {
      const dbTok = tokens[pos + 1];
      if (strict && dbTok && dbTok.type === 'ident') {
        if (STAGE_DATABASE_PATTERN.test(dbTok.value) || !VALID_DATABASE_PATTERN.test(dbTok.value)) {
          violations.push(
            `${migrationPath}: database "${dbTok.value}" must use a business domain name like hvac_<domain> instead of a stage name hvac_s<n>.`,
          );
        }
      }
      return;
    }

    if (kind === 'SCHEMA') {
      pos += 1;
      if (kwAt(tokens, pos) === 'IF' && kwAt(tokens, pos + 1) === 'NOT' && kwAt(tokens, pos + 2) === 'EXISTS') pos += 3;
      const schemaTok = tokens[pos];
      if (schemaTok && schemaTok.type === 'ident') {
        const key = `${database}:${schemaTok.value}`;
        if (!liveSchemas.has(key)) liveSchemas.set(key, undefined);
        if (strict) validateObjectName(violations, migrationPath, 'schema', schemaTok.value);
      }
      return;
    }

    if (kind === 'ROLE' || kind === 'USER') {
      const roleTok = tokens[pos + 1];
      if (strict && roleTok && roleTok.type === 'ident') {
        if (!SNAKE_CASE_PATTERN.test(roleTok.value) || FORBIDDEN_ROLE_PATTERN.test(roleTok.value)) {
          violations.push(
            `${migrationPath}: role "${roleTok.value}" must use lower snake_case without stage tags (s<n>), tmp/backup, or test markers.`,
          );
        }
      }
      return;
    }

    if (kind === 'TABLE') {
      if (isTemp) return;
      pos += 1;
      if (kwAt(tokens, pos) === 'IF' && kwAt(tokens, pos + 1) === 'NOT' && kwAt(tokens, pos + 2) === 'EXISTS') pos += 3;
      const target = readQualifiedName(tokens, pos);
      if (!target) return;
      const resolvedSchema = target.schema ?? 'public';
      const tableKey = `${database}:${resolvedSchema}.${target.name}`;
      if (!liveTables.has(tableKey)) liveTables.set(tableKey, undefined);

      if (strict) {
        if (!target.schema) {
          violations.push(
            `${migrationPath}: persistent table "${target.name}" must be schema-qualified (<schema>.${target.name}).`,
          );
        } else {
          validateTableRules(violations, migrationPath, target.schema, target.name);
        }
        let postNameIndex = target.nextIndex;
        let foundAs = false;
        while (postNameIndex < tokens.length) {
          const current = tokens[postNameIndex];
          if (current.type === 'other' && current.value === '(') {
            let depth = 1;
            postNameIndex += 1;
            while (postNameIndex < tokens.length && depth > 0) {
              const nested = tokens[postNameIndex];
              if (nested.type === 'other' && nested.value === '(') depth += 1;
              if (nested.type === 'other' && nested.value === ')') depth -= 1;
              postNameIndex += 1;
            }
            continue;
          }
          if (kwAt(tokens, postNameIndex) === 'AS') {
            foundAs = true;
            break;
          }
          postNameIndex += 1;
        }
        if (foundAs) {
          violations.push(
            `${migrationPath}: persistent CREATE TABLE ${resolvedSchema}.${target.name} AS is forbidden; define an explicit normalized table schema instead of CTAS copies.`,
          );
        }
      }
      return;
    }

    if (kind === 'VIEW' || (kind === 'MATERIALIZED' && kwAt(tokens, pos + 1) === 'VIEW')) {
      pos += kind === 'MATERIALIZED' ? 2 : 1;
      if (kwAt(tokens, pos) === 'IF' && kwAt(tokens, pos + 1) === 'NOT' && kwAt(tokens, pos + 2) === 'EXISTS') pos += 3;
      const target = readQualifiedName(tokens, pos);
      if (strict && target) {
        if (!target.schema) {
          violations.push(`${migrationPath}: view "${target.name}" must be schema-qualified (<schema>.${target.name}).`);
        } else {
          validateObjectName(violations, migrationPath, 'schema', target.schema);
          validateObjectName(violations, migrationPath, 'view', target.name);
        }
      }
      return;
    }

    const isUniqueIndex = kind === 'UNIQUE' && kwAt(tokens, pos + 1) === 'INDEX';
    if (kind === 'INDEX' || isUniqueIndex) {
      pos += isUniqueIndex ? 2 : 1;
      if (kwAt(tokens, pos) === 'CONCURRENTLY') pos += 1;
      if (kwAt(tokens, pos) === 'IF' && kwAt(tokens, pos + 1) === 'NOT' && kwAt(tokens, pos + 2) === 'EXISTS') pos += 3;
      const next = tokens[pos];
      if (strict && next && next.type === 'ident' && kwAt(tokens, pos) !== 'ON') {
        validateObjectName(violations, migrationPath, 'index', next.value);
      }
    }
    return;
  }

  if (head === 'DROP') {
    const kind = kwAt(tokens, 1);
    if (kind === 'TABLE') {
      let pos = 2;
      if (kwAt(tokens, pos) === 'IF' && kwAt(tokens, pos + 1) === 'EXISTS') pos += 2;
      while (pos < tokens.length) {
        const stopKw = kwAt(tokens, pos);
        if (stopKw === 'CASCADE' || stopKw === 'RESTRICT') break;
        const target = readQualifiedName(tokens, pos);
        if (!target) break;
        const tableKey = `${database}:${target.schema ?? 'public'}.${target.name}`;
        liveTables.delete(tableKey);
        pos = target.nextIndex;
        if (tokens[pos]?.type === 'comma') {
          pos += 1;
        } else {
          break;
        }
      }
      return;
    }

    if (kind === 'SCHEMA') {
      let pos = 2;
      if (kwAt(tokens, pos) === 'IF' && kwAt(tokens, pos + 1) === 'EXISTS') pos += 2;
      while (pos < tokens.length) {
        const stopKw = kwAt(tokens, pos);
        if (stopKw === 'CASCADE' || stopKw === 'RESTRICT') break;
        const schemaTok = tokens[pos];
        if (!schemaTok || schemaTok.type !== 'ident') break;
        const schemaKey = `${database}:${schemaTok.value}`;
        liveSchemas.delete(schemaKey);
        const prefix = `${database}:${schemaTok.value}.`;
        for (const tableKey of [...liveTables.keys()]) {
          if (tableKey.startsWith(prefix)) liveTables.delete(tableKey);
        }
        pos += 1;
        if (tokens[pos]?.type === 'comma') {
          pos += 1;
        } else {
          break;
        }
      }
    }
    return;
  }

  if (head === 'ALTER' && kwAt(tokens, 1) === 'TABLE') {
    let pos = 2;
    if (kwAt(tokens, pos) === 'IF' && kwAt(tokens, pos + 1) === 'EXISTS') pos += 2;
    if (kwAt(tokens, pos) === 'ONLY') pos += 1;
    const source = readQualifiedName(tokens, pos);
    if (!source) return;
    const oldSchema = source.schema ?? 'public';
    const oldKey = `${database}:${oldSchema}.${source.name}`;

    if (kwAt(tokens, source.nextIndex) === 'RENAME' && kwAt(tokens, source.nextIndex + 1) === 'TO') {
      const nextTok = tokens[source.nextIndex + 2];
      if (!nextTok || nextTok.type !== 'ident') return;
      const newKey = `${database}:${oldSchema}.${nextTok.value}`;
      const existingComment = liveTables.get(oldKey);
      if (liveTables.has(oldKey)) {
        liveTables.delete(oldKey);
        liveTables.set(newKey, existingComment);
      }
      if (strict) validateTableRules(violations, migrationPath, oldSchema, nextTok.value);
      return;
    }

    if (kwAt(tokens, source.nextIndex) === 'SET' && kwAt(tokens, source.nextIndex + 1) === 'SCHEMA') {
      const nextTok = tokens[source.nextIndex + 2];
      if (!nextTok || nextTok.type !== 'ident') return;
      const newKey = `${database}:${nextTok.value}.${source.name}`;
      const existingComment = liveTables.get(oldKey);
      if (liveTables.has(oldKey)) {
        liveTables.delete(oldKey);
        liveTables.set(newKey, existingComment);
      }
      if (strict) validateTableRules(violations, migrationPath, nextTok.value, source.name);
    }
    return;
  }

  if (head === 'COMMENT' && kwAt(tokens, 1) === 'ON') {
    const targetKind = kwAt(tokens, 2);
    if (targetKind === 'SCHEMA') {
      const schemaTok = tokens[3];
      if (!schemaTok || schemaTok.type !== 'ident' || kwAt(tokens, 4) !== 'IS') return;
      applyCommentTarget({
        kind: 'SCHEMA',
        targetKey: `${database}:${schemaTok.value}`,
        displayName: schemaTok.value,
        valueTok: tokens[5],
        strict,
        migrationPath,
        liveTargets: liveSchemas,
        violations,
      });
      return;
    }

    if (targetKind === 'TABLE') {
      const target = readQualifiedName(tokens, 3);
      if (!target || kwAt(tokens, target.nextIndex) !== 'IS') return;
      const fullName = `${target.schema ?? 'public'}.${target.name}`;
      applyCommentTarget({
        kind: 'TABLE',
        targetKey: `${database}:${fullName}`,
        displayName: fullName,
        valueTok: tokens[target.nextIndex + 1],
        strict,
        migrationPath,
        liveTargets: liveTables,
        violations,
      });
    }
  }
};

export const inspectMigrationConventions = (input: ConventionCheckInput): MigrationConventionResult => {
  const violations: string[] = [];
  const legacyDbSet = new Set(input.baseline.legacyDatabases);
  const seenGrandfathered = new Set<string>();
  const registeredPaths = new Set(input.migrations.map((m) => m.path));
  const activeDatabases = new Set(input.migrations.map((m) => m.database));

  if (input.baseline.grandfatheredMigrations.length > input.baseline.maxGrandfatheredMigrations) {
    violations.push(
      `scripts/migration-conventions-baseline.json: grandfatheredMigrations count (${input.baseline.grandfatheredMigrations.length}) exceeds maxGrandfatheredMigrations (${input.baseline.maxGrandfatheredMigrations}).`,
    );
  }

  for (const path of input.baseline.grandfatheredMigrations) {
    if (seenGrandfathered.has(path)) {
      violations.push(`scripts/migration-conventions-baseline.json: duplicate grandfathered migration "${path}".`);
      continue;
    }
    seenGrandfathered.add(path);
    if (!registeredPaths.has(path)) {
      violations.push(
        `scripts/migration-conventions-baseline.json: "${path}" is no longer registered in migration-list.tsv; remove it and lower maxGrandfatheredMigrations.`,
      );
    }
  }

  const reportedDatabases = new Set<string>();
  for (const { database } of input.migrations) {
    if (reportedDatabases.has(database)) continue;
    reportedDatabases.add(database);
    if (legacyDbSet.has(database)) continue;
    if (STAGE_DATABASE_PATTERN.test(database) || !VALID_DATABASE_PATTERN.test(database)) {
      violations.push(
        `deploy/platform/phase1/migrations/migration-list.tsv: database "${database}" is forbidden; name new databases after business domains (hvac_<domain>) instead of stage numbers (hvac_s<n>).`,
      );
    }
  }

  const liveSchemas = new Map<string, string | undefined>();
  const liveTables = new Map<string, string | undefined>();
  seedBootstrapSchemas(input.bootstrapSql, activeDatabases, legacyDbSet, liveSchemas, violations);

  for (const { database, path } of input.migrations) {
    const strict = !seenGrandfathered.has(path);
    if (strict) validateMigrationFileName(violations, path);
    const sql = input.files[path];
    if (sql === undefined) {
      violations.push(`${path}: registered in migration-list.tsv but SQL file is missing.`);
      continue;
    }

    for (const stmt of parseSqlStatements(sql)) {
      if (stmt.kind !== 'sql') continue;
      handleSqlStatement({
        database,
        migrationPath: path,
        tokens: stmt.tokens,
        strict,
        liveSchemas,
        liveTables,
        violations,
      });
    }
  }

  for (const [schemaKey, comment] of liveSchemas) {
    if (!comment) {
      const [db, schema] = schemaKey.split(':');
      violations.push(
        `${db}: schema "${schema}" is missing a non-empty COMMENT ON SCHEMA ${schema} IS '...'; statement (Chinese description required).`,
      );
    }
  }

  for (const [tableKey, comment] of liveTables) {
    if (!comment) {
      const [db, fullTable] = tableKey.split(':');
      violations.push(
        `${db}: live table "${fullTable}" is missing a non-empty COMMENT ON TABLE ${fullTable} IS '...'; statement (Chinese description required).`,
      );
    }
  }

  return { violations, liveSchemas, liveTables };
};

export const findMigrationConventionViolations = (input: ConventionCheckInput): string[] => [
  ...inspectMigrationConventions(input).violations,
];

export const loadRepoConventionInput = (root: string): ConventionCheckInput => {
  const migrationListPath = join(root, 'deploy/platform/phase1/migrations/migration-list.tsv');
  const bootstrapPath = join(root, 'deploy/platform/phase1/migrations/bootstrap-roles.sql');
  const baselinePath = join(root, 'scripts/migration-conventions-baseline.json');

  const migrations = parseMigrationListTsv(readFileSync(migrationListPath, 'utf8'));
  const baseline = parseBaselineJson(readFileSync(baselinePath, 'utf8'));
  const bootstrapSql = existsSync(bootstrapPath) ? readFileSync(bootstrapPath, 'utf8') : undefined;
  const files: Record<string, string | undefined> = {};

  for (const { path } of migrations) {
    const absPath = join(root, path);
    files[path] = existsSync(absPath) ? readFileSync(absPath, 'utf8') : undefined;
  }

  return { migrations, files, bootstrapSql, baseline };
};

const runCli = (): number => {
  const root = resolve(process.cwd());
  const result = inspectMigrationConventions(loadRepoConventionInput(root));
  if (result.violations.length > 0) {
    for (const violation of result.violations) {
      console.error(`FAIL: ${violation}`);
    }
    return 1;
  }
  console.log(
    `OK: migration conventions passed (${result.liveSchemas.size} live schemas, ${result.liveTables.size} live tables commented).`,
  );
  return 0;
};

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  process.exitCode = runCli();
}
