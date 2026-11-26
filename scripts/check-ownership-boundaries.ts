import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { dirname, join, relative, resolve, sep } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

export type BoundaryRuleKind = 'cross-schema-sql' | 'peer-module-import' | 'untyped-map';

export interface BoundaryViolation {
  readonly rule: BoundaryRuleKind;
  readonly file: string;
  readonly line: number;
  readonly symbol: string;
  readonly message: string;
}

export interface OwnershipBoundariesResult {
  readonly violations: readonly BoundaryViolation[];
  readonly checkedFiles: number;
}

interface GoStringPiece {
  readonly value: string;
  readonly offsets: readonly number[];
  readonly lineOffsets: readonly number[];
}

interface GoImportEntry {
  readonly line: number;
  readonly importPath: string;
}

interface LexedGoFile {
  readonly imports: readonly GoImportEntry[];
  readonly stringGroups: readonly GoStringPiece[];
  readonly codeWithoutCommentsOrStrings: string;
}

interface SqlRelationRef {
  readonly schema: string;
  readonly relation: string;
  readonly line: number;
  readonly isWrite: boolean;
}

interface ExceptionRule {
  readonly unitPrefix: string;
  readonly schema: string;
  readonly relations: readonly string[];
  readonly allowWrite: boolean;
}

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const dataOwnershipRegistryPath = 'contracts/ownership/data-ownership.v1.json';
const allowedCrossModuleImport = 'github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi';
const peerImportPattern = /^github\.com\/quanlaihe\/hvac-web\/(modules|services)\/([^/]+)(?:\/.*)?$/u;
const untypedMapPattern = /\bmap\s*\[\s*string\s*\]\s*(?:any\b|interface\s*\{\s*\})/gu;

const SCHEMA_PRIMARY_OWNER: Readonly<Record<string, string>> = Object.freeze({
  identity: 'services/identity-service',
  iam: 'modules/iam',
  core_registry: 'modules/registry',
  asset_twin: 'modules/registry',
  connectivity: 'modules/connectivity',
  gateway: 'cmd/energy-api',
  telemetry_runtime: 'modules/telemetry',
  telemetry_history: 'modules/telemetry',
  analytics: 'modules/energy',
  command_runtime: 'modules/command',
  maintenance_ops: 'modules/maintenance',
  alarm_runtime: 'modules/alarm',
  notification_runtime: 'services/notification-service',
  work_order_runtime: 'modules/workorder',
  audit_ledger: 'modules/audit',
  audit: 'modules/audit',
  optimization: 'services/optimization-service',
  ops_agent: 'services/operations-agent-service',
  agent_operations: 'services/operations-agent-service',
  phase1_deployment: 'libs/schemagate',
});

const REGISTRY_WRITER_TO_UNIT: Readonly<Record<string, string>> = Object.freeze({
  'platform-gateway': 'cmd/energy-api',
  'audit-ledger-service': 'modules/audit',
  'iam-service': 'modules/iam',
  'platform-core-service': 'modules/registry',
  'telemetry-runtime-service': 'modules/telemetry',
  'telemetry-history-projector': 'modules/telemetry',
  'analytics-read-model-projector': 'modules/energy',
  'command-service': 'modules/command',
  'alarm-service': 'modules/alarm',
  'notification-service': 'services/notification-service',
  'work-order-service': 'modules/workorder',
});

// Phase 1 intelligence, metric, scheduler, and maintenance modules store their own RLS-isolated
// tables in core_registry inside hvac_s1; IAM and connectivity have separate schemas and must
// use *_v1 contract views or internal APIs instead of core_registry base tables.
const UNIT_OWNED_SCHEMAS: Readonly<Record<string, readonly string[]>> = Object.freeze({
  'services/identity-service': ['identity'],
  'cmd/energy-api': ['gateway'],
  'libs/sessionstore': ['gateway'],
  'libs/ownershipregistry': ['gateway'],
  'libs/schemagate': ['phase1_deployment'],
  'libs/schedulerjob': ['core_registry'],
  'modules/iam': ['iam'],
  'modules/registry': ['core_registry', 'asset_twin'],
  'modules/connectivity': ['connectivity'],
  'modules/telemetry': ['telemetry_runtime', 'telemetry_history', 'analytics'],
  'modules/energy': ['analytics', 'telemetry_history'],
  'modules/command': ['command_runtime'],
  'modules/alarm': ['alarm_runtime'],
  'services/notification-service': ['notification_runtime'],
  'modules/workorder': ['work_order_runtime'],
  'modules/audit': ['audit_ledger', 'audit'],
  'modules/scheduler': ['core_registry'],
  'modules/maintenance': ['core_registry', 'maintenance_ops'],
  'modules/metric': ['core_registry', 'analytics'],
  'services/fdd-service': ['core_registry'],
  'services/forecast-service': ['core_registry'],
  'services/mv-service': ['core_registry'],
  'services/report-service': ['core_registry'],
  'services/optimization-service': ['core_registry', 'optimization'],
});

// Cross-schema access declared in data-ownership.v1.json, ADRs (0020/0021), or operational relays.
const DECLARED_SQL_EXCEPTIONS: readonly ExceptionRule[] = Object.freeze([
  { unitPrefix: 'modules/iam/cmd/identity-reconciler/', schema: 'identity', relations: ['users'], allowWrite: false },
  { unitPrefix: 'modules/registry/internal/core/outbox_relay.go', schema: 'iam', relations: ['tenants'], allowWrite: false },
  { unitPrefix: 'modules/maintenance/', schema: 'connectivity', relations: ['gateway_credentials'], allowWrite: false },
  { unitPrefix: 'modules/maintenance/', schema: 'iam', relations: ['tenants', 'finalize_tenant_retirement'], allowWrite: false },
  { unitPrefix: 'modules/audit/', schema: 'gateway', relations: ['outbox'], allowWrite: true },
  { unitPrefix: 'services/notification-service/', schema: 'alarm_runtime', relations: ['notification_outbox'], allowWrite: true },
  { unitPrefix: 'cmd/telemetry-worker/', schema: 'telemetry_runtime', relations: ['telemetry_publication_outbox'], allowWrite: true },
  { unitPrefix: 'modules/metric/', schema: 'telemetry_history', relations: ['observations'], allowWrite: false },
  { unitPrefix: 'services/fdd-service/', schema: 'telemetry_history', relations: ['observations'], allowWrite: false },
  { unitPrefix: 'services/fdd-service/', schema: 'analytics', relations: ['site_meter_balance_hourly'], allowWrite: false },
  { unitPrefix: 'services/optimization-service/', schema: 'telemetry_history', relations: ['observations'], allowWrite: false },
  { unitPrefix: 'services/optimization-service/', schema: 'analytics', relations: ['optimization_evaluations'], allowWrite: true },
  { unitPrefix: 'services/forecast-service/', schema: 'analytics', relations: ['forecast_series'], allowWrite: true },
  { unitPrefix: 'services/forecast-service/', schema: 'analytics', relations: ['metric_result_facts'], allowWrite: false },
  { unitPrefix: 'services/mv-service/', schema: 'analytics', relations: ['metric_result_facts'], allowWrite: false },
  { unitPrefix: 'services/mv-service/', schema: 'analytics', relations: ['site_condition_anchors_hourly_latest'], allowWrite: false },
]);

const normalizePath = (value: string): string => value.split(sep).join('/').replaceAll('\\', '/');

const unitFromPath = (normalizedPath: string): string | null => {
  const match = /^(modules|services|cmd|libs)\/([^/]+)\//u.exec(normalizedPath);
  return match ? `${match[1]}/${match[2]}` : null;
};

const isNonTestGoFile = (normalizedPath: string): boolean =>
  normalizedPath.endsWith('.go') && !normalizedPath.endsWith('_test.go');

const loadRegistrySchemas = (rootDir: string): Readonly<Record<string, string>> => {
  const merged: Record<string, string> = { ...SCHEMA_PRIMARY_OWNER };
  const registryPath = join(rootDir, dataOwnershipRegistryPath);
  if (!existsSync(registryPath)) return merged;
  const parsed: unknown = JSON.parse(readFileSync(registryPath, 'utf8'));
  if (!parsed || typeof parsed !== 'object' || !('resources' in parsed) || !Array.isArray(parsed.resources)) {
    return merged;
  }
  for (const resource of parsed.resources) {
    if (!resource || typeof resource !== 'object') continue;
    const kind = 'kind' in resource ? resource.kind : undefined;
    const name = 'name' in resource ? resource.name : undefined;
    const writer = 'writer' in resource ? resource.writer : undefined;
    if (kind !== 'schema' || typeof name !== 'string') continue;
    if (!(name in merged)) {
      const mapped = typeof writer === 'string'
        ? (REGISTRY_WRITER_TO_UNIT[writer] ?? writer)
        : 'unknown';
      merged[name] = mapped;
    }
  }
  return merged;
};

const computeLineOffsets = (text: string): number[] => {
  const offsets = [0];
  for (let i = 0; i < text.length; i += 1) {
    if (text[i] === '\n') offsets.push(i + 1);
  }
  return offsets;
};

const lineForOffset = (lineOffsets: readonly number[], offset: number): number => {
  let low = 0;
  let high = lineOffsets.length - 1;
  while (low <= high) {
    const mid = (low + high) >> 1;
    if (lineOffsets[mid] <= offset) {
      if (mid + 1 >= lineOffsets.length || lineOffsets[mid + 1] > offset) return mid + 1;
      low = mid + 1;
    } else {
      high = mid - 1;
    }
  }
  return 1;
};

const decodeGoDoubleQuoted = (rawInner: string, startOffset: number): { value: string; offsets: number[] } => {
  let value = '';
  const offsets: number[] = [];
  let i = 0;
  while (i < rawInner.length) {
    const ch = rawInner[i];
    const absOffset = startOffset + 1 + i;
    if (ch === '\\' && i + 1 < rawInner.length) {
      const next = rawInner[i + 1];
      if (next === 'n') {
        value += '\n';
        offsets.push(absOffset);
        i += 2;
        continue;
      }
      if (next === 'r' || next === 't') {
        value += ' ';
        offsets.push(absOffset);
        i += 2;
        continue;
      }
      value += next;
      offsets.push(absOffset);
      i += 2;
      continue;
    }
    value += ch;
    offsets.push(absOffset);
    i += 1;
  }
  return { value, offsets };
};

const lexGoSource = (content: string): LexedGoFile => {
  const lineOffsets = computeLineOffsets(content);
  const codeChars: string[] = [];
  const rawTokens: Array<{ kind: 'ident' | 'string' | 'punct'; value: string; line: number; piece?: GoStringPiece }> = [];
  let i = 0;
  let line = 1;

  while (i < content.length) {
    const ch = content[i];

    if (ch === '\n') {
      codeChars.push('\n');
      line += 1;
      i += 1;
      continue;
    }

    if (ch === '/' && content[i + 1] === '/') {
      codeChars.push(' ', ' ');
      i += 2;
      while (i < content.length && content[i] !== '\n') {
        codeChars.push(' ');
        i += 1;
      }
      continue;
    }

    if (ch === '/' && content[i + 1] === '*') {
      codeChars.push(' ', ' ');
      i += 2;
      while (i < content.length && !(content[i] === '*' && content[i + 1] === '/')) {
        if (content[i] === '\n') {
          codeChars.push('\n');
          line += 1;
        } else {
          codeChars.push(' ');
        }
        i += 1;
      }
      if (i + 1 < content.length) {
        codeChars.push(' ', ' ');
        i += 2;
      }
      continue;
    }

    if (ch === '`') {
      const startLine = line;
      codeChars.push(' ');
      i += 1;
      let val = '';
      const offsets: number[] = [];
      while (i < content.length && content[i] !== '`') {
        const current = content[i];
        if (current === '\n') {
          codeChars.push('\n');
          line += 1;
        } else {
          codeChars.push(' ');
        }
        val += current;
        offsets.push(i);
        i += 1;
      }
      if (i < content.length) {
        codeChars.push(' ');
        i += 1;
      }
      rawTokens.push({
        kind: 'string',
        value: val,
        line: startLine,
        piece: { value: val, offsets, lineOffsets },
      });
      continue;
    }

    if (ch === '"') {
      const startLine = line;
      const startIdx = i;
      codeChars.push(' ');
      i += 1;
      while (i < content.length && content[i] !== '"') {
        if (content[i] === '\\' && i + 1 < content.length) {
          codeChars.push(' ', ' ');
          i += 2;
          continue;
        }
        if (content[i] === '\n') {
          codeChars.push('\n');
          line += 1;
        } else {
          codeChars.push(' ');
        }
        i += 1;
      }
      const endInner = i;
      if (i < content.length) {
        codeChars.push(' ');
        i += 1;
      }
      const decoded = decodeGoDoubleQuoted(content.slice(startIdx + 1, endInner), startIdx);
      rawTokens.push({
        kind: 'string',
        value: decoded.value,
        line: startLine,
        piece: { value: decoded.value, offsets: decoded.offsets, lineOffsets },
      });
      continue;
    }

    if (ch === "'") {
      codeChars.push(' ');
      i += 1;
      while (i < content.length && content[i] !== "'") {
        if (content[i] === '\\' && i + 1 < content.length) {
          codeChars.push(' ', ' ');
          i += 2;
          continue;
        }
        if (content[i] === '\n') {
          codeChars.push('\n');
          line += 1;
        } else {
          codeChars.push(' ');
        }
        i += 1;
      }
      if (i < content.length) {
        codeChars.push(' ');
        i += 1;
      }
      continue;
    }

    if (/[A-Za-z_]/u.test(ch)) {
      const start = i;
      while (i < content.length && /[A-Za-z0-9_]/u.test(content[i])) {
        codeChars.push(content[i]);
        i += 1;
      }
      rawTokens.push({ kind: 'ident', value: content.slice(start, i), line });
      continue;
    }

    if (/\s/u.test(ch)) {
      codeChars.push(ch);
      i += 1;
      continue;
    }

    codeChars.push(ch);
    rawTokens.push({ kind: 'punct', value: ch, line });
    i += 1;
  }

  const imports: GoImportEntry[] = [];
  for (let t = 0; t < rawTokens.length; t += 1) {
    const token = rawTokens[t];
    if (token.kind !== 'ident' || token.value !== 'import') continue;
    const next = rawTokens[t + 1];
    if (!next) break;
    if (next.kind === 'string') {
      imports.push({ line: next.line, importPath: next.value });
      t += 1;
      continue;
    }
    if ((next.kind === 'ident' || (next.kind === 'punct' && next.value === '.')) && rawTokens[t + 2]?.kind === 'string') {
      const target = rawTokens[t + 2];
      imports.push({ line: target.line, importPath: target.value });
      t += 2;
      continue;
    }
    if (next.kind === 'punct' && next.value === '(') {
      let k = t + 2;
      while (k < rawTokens.length) {
        const inner = rawTokens[k];
        if (inner.kind === 'punct' && inner.value === ')') break;
        if (inner.kind === 'string') {
          imports.push({ line: inner.line, importPath: inner.value });
        }
        k += 1;
      }
      t = k;
    }
  }

  // Merge adjacent `+`-concatenated string literals so split SQL statements keep context.
  const stringGroups: GoStringPiece[] = [];
  for (let t = 0; t < rawTokens.length; t += 1) {
    const token = rawTokens[t];
    if (token.kind !== 'string' || !token.piece) continue;
    let combinedValue = token.piece.value;
    const combinedOffsets = [...token.piece.offsets];
    while (
      t + 2 < rawTokens.length
      && rawTokens[t + 1].kind === 'punct'
      && rawTokens[t + 1].value === '+'
      && rawTokens[t + 2].kind === 'string'
      && rawTokens[t + 2].piece
    ) {
      const nextPiece = rawTokens[t + 2].piece!;
      combinedValue += nextPiece.value;
      combinedOffsets.push(...nextPiece.offsets);
      t += 2;
    }
    stringGroups.push({
      value: combinedValue,
      offsets: combinedOffsets,
      lineOffsets,
    });
  }

  return {
    imports,
    stringGroups,
    codeWithoutCommentsOrStrings: codeChars.join(''),
  };
};

type SqlToken =
  | { readonly kind: 'ident'; readonly lower: string; readonly upper: string; readonly quoted: boolean; readonly offset: number }
  | { readonly kind: 'string'; readonly value: string; readonly offset: number }
  | { readonly kind: 'dot' }
  | { readonly kind: 'punct'; readonly value: string };

const SQL_INDICATORS = new Set([
  'SELECT', 'INSERT', 'UPDATE', 'DELETE', 'MERGE', 'TRUNCATE', 'ALTER', 'DROP', 'CREATE', 'COPY',
  'FROM', 'JOIN', 'INTO', 'RETURNING', 'TO_REGCLASS', 'HAS_TABLE_PRIVILEGE', 'NEXTVAL', 'CURRVAL', 'SETVAL',
]);

const CATALOG_RELATION_FUNCTIONS = new Set(['TO_REGCLASS', 'HAS_TABLE_PRIVILEGE', 'NEXTVAL', 'CURRVAL', 'SETVAL']);

const tokenizeSql = (sql: string): { readonly tokens: readonly SqlToken[]; readonly hasSqlKeyword: boolean } => {
  const tokens: SqlToken[] = [];
  let hasSqlKeyword = false;
  let i = 0;

  while (i < sql.length) {
    const ch = sql[i];
    if (/\s/u.test(ch)) {
      i += 1;
      continue;
    }
    if (sql.startsWith('--', i)) {
      const nl = sql.indexOf('\n', i + 2);
      i = nl === -1 ? sql.length : nl + 1;
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
    if (ch === "'") {
      const start = i + 1;
      let val = '';
      i += 1;
      while (i < sql.length) {
        if (sql[i] === "'") {
          if (sql[i + 1] === "'") {
            val += "'";
            i += 2;
            continue;
          }
          i += 1;
          break;
        }
        val += sql[i];
        i += 1;
      }
      tokens.push({ kind: 'string', value: val, offset: start });
      continue;
    }
    if (ch === '"') {
      const start = i + 1;
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
      tokens.push({ kind: 'ident', lower: ident.toLowerCase(), upper: ident.toUpperCase(), quoted: true, offset: start });
      continue;
    }
    if (/[A-Za-z_]/u.test(ch)) {
      const start = i;
      while (i < sql.length && /[A-Za-z0-9_]/u.test(sql[i])) i += 1;
      const raw = sql.slice(start, i);
      const upper = raw.toUpperCase();
      if (SQL_INDICATORS.has(upper)) hasSqlKeyword = true;
      tokens.push({ kind: 'ident', lower: raw.toLowerCase(), upper, quoted: false, offset: start });
      continue;
    }
    if (ch === '.') {
      tokens.push({ kind: 'dot' });
      i += 1;
      continue;
    }
    tokens.push({ kind: 'punct', value: ch });
    i += 1;
  }

  return { tokens, hasSqlKeyword };
};

const isWriteTargetBefore = (tokens: readonly SqlToken[], index: number): boolean => {
  const words: string[] = [];
  for (let k = index - 1; k >= 0 && words.length < 6; k -= 1) {
    const tok = tokens[k];
    if (tok.kind !== 'ident' || tok.quoted) break;
    words.unshift(tok.upper);
  }
  if (words.length === 0) return false;
  const tail = words.join(' ');
  return (
    /(?:^| )(?:INSERT|INSERT IGNORE|REPLACE|MERGE) INTO$/u.test(tail)
    || /(?:^| )UPDATE(?: ONLY)?$/u.test(tail)
    || /(?:^| )DELETE FROM(?: ONLY)?$/u.test(tail)
    || /(?:^| )TRUNCATE(?: TABLE)?(?: ONLY)?$/u.test(tail)
    || /(?:^| )ALTER(?: MATERIALIZED)? (?:TABLE|VIEW)(?: IF EXISTS)?(?: ONLY)?$/u.test(tail)
    || /(?:^| )DROP(?: MATERIALIZED)? (?:TABLE|VIEW)(?: IF EXISTS)?$/u.test(tail)
    || /(?:^| )CREATE(?: OR REPLACE)?(?: TEMP| TEMPORARY)?(?: MATERIALIZED)? (?:TABLE|VIEW)(?: IF NOT EXISTS)?$/u.test(tail)
    || (words[words.length - 1] === 'COPY' && hasUpcomingFrom(tokens, index + 3))
  );
};

const hasUpcomingFrom = (tokens: readonly SqlToken[], startIndex: number): boolean => {
  for (let i = startIndex; i < tokens.length; i += 1) {
    const tok = tokens[i];
    if (tok.kind === 'punct' && tok.value === ';') return false;
    if (tok.kind === 'ident' && !tok.quoted) {
      if (tok.upper === 'FROM') return true;
      if (tok.upper === 'TO') return false;
    }
  }
  return false;
};

const enclosingFunctionCallName = (tokens: readonly SqlToken[], stringIndex: number): string | null => {
  let depth = 0;
  for (let k = stringIndex - 1; k >= 1; k -= 1) {
    const tok = tokens[k];
    if (tok.kind === 'punct') {
      if (tok.value === ')') depth += 1;
      else if (tok.value === '(') {
        if (depth === 0) {
          const fnTok = tokens[k - 1];
          return fnTok?.kind === 'ident' && !fnTok.quoted ? fnTok.upper : null;
        }
        depth -= 1;
      } else if (tok.value === ';') {
        return null;
      }
    }
  }
  return null;
};

const extractSqlRelations = (
  piece: GoStringPiece,
  knownSchemas: ReadonlySet<string>,
): SqlRelationRef[] => {
  const { tokens, hasSqlKeyword } = tokenizeSql(piece.value);
  if (!hasSqlKeyword) return [];

  const refs: SqlRelationRef[] = [];
  const lineAt = (localOffset: number): number => {
    const clamped = Math.min(Math.max(localOffset, 0), Math.max(piece.offsets.length - 1, 0));
    const sourceOffset = piece.offsets[clamped] ?? 0;
    return lineForOffset(piece.lineOffsets, sourceOffset);
  };

  for (let i = 0; i < tokens.length; i += 1) {
    const tok = tokens[i];

    if (i + 2 < tokens.length && tok.kind === 'ident' && tokens[i + 1].kind === 'dot') {
      const relationTok = tokens[i + 2];
      if (relationTok.kind === 'ident') {
        const schema = tok.lower;
        if (knownSchemas.has(schema)) {
          refs.push({
            schema,
            relation: relationTok.lower,
            line: lineAt(tok.offset),
            isWrite: isWriteTargetBefore(tokens, i),
          });
        }
        i += 2;
        continue;
      }
    }

    if (tok.kind === 'string') {
      const fnName = enclosingFunctionCallName(tokens, i);
      if (fnName && CATALOG_RELATION_FUNCTIONS.has(fnName)) {
        const match = /^\s*"?([a-z][a-z0-9_]*)"?\."?([a-z][a-z0-9_]*)"?\s*$/iu.exec(tok.value);
        if (match) {
          const schema = match[1].toLowerCase();
          const relation = match[2].toLowerCase();
          if (knownSchemas.has(schema)) {
            refs.push({
              schema,
              relation,
              line: lineAt(tok.offset),
              isWrite: false,
            });
          }
        }
      }
    }
  }

  return refs;
};

const isSqlReferenceAllowed = ({
  normalizedPath,
  unit,
  ref,
}: {
  readonly normalizedPath: string;
  readonly unit: string;
  readonly ref: SqlRelationRef;
}): boolean => {
  // Cross-schema *_v1 views are read-only contract surfaces (ADR 0016).
  if (ref.relation.endsWith('_v1') && !ref.isWrite) {
    return true;
  }

  // Exception rules take precedence on multi-table shared ClickHouse databases like analytics.
  for (const rule of DECLARED_SQL_EXCEPTIONS) {
    if (
      normalizedPath.startsWith(rule.unitPrefix)
      && ref.schema === rule.schema
      && rule.relations.includes(ref.relation)
    ) {
      return !ref.isWrite || rule.allowWrite;
    }
  }

  const ownedSchemas = UNIT_OWNED_SCHEMAS[unit] ?? [];
  if (ownedSchemas.includes(ref.schema)) {
    return true;
  }

  return false;
};

const formatSqlRemediation = ({
  file,
  ref,
  ownerMap,
}: {
  readonly file: string;
  readonly ref: SqlRelationRef;
  readonly ownerMap: Readonly<Record<string, string>>;
}): string => {
  const qualified = `${ref.schema}.${ref.relation}`;
  const owner = ownerMap[ref.schema] ?? ref.schema;
  if (ref.relation.endsWith('_v1') && ref.isWrite) {
    return `${file}:${ref.line}: cross-module SQL write to ${qualified} (owned by ${owner}); *_v1 contract views are read-only, use ${owner} internal API for mutations instead.`;
  }
  const verb = ref.isWrite ? 'write to' : 'read of';
  return `${file}:${ref.line}: cross-module SQL ${verb} ${qualified} (owned by ${owner}); query a ${ref.schema}.*_v1 contract view or call ${owner} internal API instead.`;
};

/**
 * Checks non-test Go files under modules/, services/, cmd/, or libs/ for forbidden cross-module SQL references.
 */
export const checkCrossSchemaSqlInFile = (
  filePath: string,
  content: string,
  schemaOwners: Readonly<Record<string, string>> = SCHEMA_PRIMARY_OWNER,
): BoundaryViolation[] => {
  const normalizedPath = normalizePath(filePath);
  if (!isNonTestGoFile(normalizedPath)) return [];
  const unit = unitFromPath(normalizedPath);
  if (!unit) return [];

  const knownSchemas = new Set(Object.keys(schemaOwners));
  const lexed = lexGoSource(content);
  const violations: BoundaryViolation[] = [];

  for (const piece of lexed.stringGroups) {
    for (const ref of extractSqlRelations(piece, knownSchemas)) {
      if (isSqlReferenceAllowed({ normalizedPath, unit, ref })) continue;
      violations.push({
        rule: 'cross-schema-sql',
        file: normalizedPath,
        line: ref.line,
        symbol: `${ref.schema}.${ref.relation}`,
        message: formatSqlRemediation({ file: normalizedPath, ref, ownerMap: schemaOwners }),
      });
    }
  }

  return violations;
};

/**
 * Checks all Go files under modules/ and services/ for forbidden peer module/service imports.
 */
export const checkPeerModuleImportsInFile = (filePath: string, content: string): BoundaryViolation[] => {
  const normalizedPath = normalizePath(filePath);
  if (!normalizedPath.endsWith('.go')) return [];
  const match = /^(modules|services)\/([^/]+)\//u.exec(normalizedPath);
  if (!match) return [];
  const ownUnit = `${match[1]}/${match[2]}`;

  const lexed = lexGoSource(content);
  const violations: BoundaryViolation[] = [];

  for (const entry of lexed.imports) {
    const targetMatch = peerImportPattern.exec(entry.importPath);
    if (!targetMatch) continue;
    const targetUnit = `${targetMatch[1]}/${targetMatch[2]}`;
    if (targetUnit === ownUnit) continue;
    if (entry.importPath === allowedCrossModuleImport) continue;

    violations.push({
      rule: 'peer-module-import',
      file: normalizedPath,
      line: entry.line,
      symbol: entry.importPath,
      message: `${normalizedPath}:${entry.line}: forbidden peer import ${entry.importPath} from ${ownUnit} into ${targetUnit}; communicate via internal HTTP/contract boundaries or ${allowedCrossModuleImport} instead.`,
    });
  }

  return violations;
};

/**
 * Checks non-test Go files under services/optimization-service/ and libs/intelligencemodel/ for untyped string maps.
 */
export const checkUntypedMapsInFile = (filePath: string, content: string): BoundaryViolation[] => {
  const normalizedPath = normalizePath(filePath);
  if (!isNonTestGoFile(normalizedPath)) return [];
  if (
    !normalizedPath.startsWith('services/optimization-service/')
    && !normalizedPath.startsWith('libs/intelligencemodel/')
  ) {
    return [];
  }

  const lexed = lexGoSource(content);
  const lineOffsets = computeLineOffsets(lexed.codeWithoutCommentsOrStrings);
  const violations: BoundaryViolation[] = [];
  untypedMapPattern.lastIndex = 0;

  let match: RegExpExecArray | null = untypedMapPattern.exec(lexed.codeWithoutCommentsOrStrings);
  while (match !== null) {
    const line = lineForOffset(lineOffsets, match.index);
    const matchedText = match[0].replace(/\s+/gu, ' ');
    violations.push({
      rule: 'untyped-map',
      file: normalizedPath,
      line,
      symbol: matchedText,
      message: `${normalizedPath}:${line}: forbidden untyped ${matchedText} in strongly-typed optimization package; define explicit typed structs instead.`,
    });
    match = untypedMapPattern.exec(lexed.codeWithoutCommentsOrStrings);
  }

  return violations;
};

const collectGoFiles = (rootDir: string): string[] => {
  const files: string[] = [];
  const visit = (absDir: string): void => {
    const entries = readdirSync(absDir, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name));
    for (const entry of entries) {
      const absPath = join(absDir, entry.name);
      if (entry.isDirectory()) {
        visit(absPath);
      } else if (entry.isFile() && entry.name.endsWith('.go')) {
        files.push(normalizePath(relative(rootDir, absPath)));
      }
    }
  };
  for (const topDir of ['modules', 'services', 'cmd', 'libs']) {
    const absTop = join(rootDir, topDir);
    if (existsSync(absTop)) visit(absTop);
  }
  return files.sort();
};

/**
 * Inspects all Go source files in rootDir against cross-schema SQL, peer import, and untyped map boundaries.
 */
export const inspectOwnershipBoundaries = (rootDir: string = repositoryRoot): OwnershipBoundariesResult => {
  const schemaOwners = loadRegistrySchemas(rootDir);
  const goFiles = collectGoFiles(rootDir);
  const violations: BoundaryViolation[] = [];

  for (const relPath of goFiles) {
    const content = readFileSync(join(rootDir, relPath), 'utf8');
    violations.push(
      ...checkCrossSchemaSqlInFile(relPath, content, schemaOwners),
      ...checkPeerModuleImportsInFile(relPath, content),
      ...checkUntypedMapsInFile(relPath, content),
    );
  }

  return { violations, checkedFiles: goFiles.length };
};

const invokedPath = process.argv[1] ? pathToFileURL(resolve(process.argv[1])).href : '';
if (invokedPath === import.meta.url) {
  const result = inspectOwnershipBoundaries();
  if (result.violations.length > 0) {
    console.error(`Ownership boundary check failed (${result.violations.length} violation(s) across ${result.checkedFiles} Go files):`);
    for (const violation of result.violations) {
      console.error(`- [${violation.rule}] ${violation.message}`);
    }
    process.exitCode = 1;
  } else {
    console.log(`Ownership boundaries passed across ${result.checkedFiles} Go files.`);
  }
}
