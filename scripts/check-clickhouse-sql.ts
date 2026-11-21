import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

export type ClickHouseSqlRuleId =
  | 'CH-ALIAS-SHADOW'
  | 'CH-RESERVED-ALIAS'
  | 'CH-WITH-ALIAS-SHADOW'
  | 'CH-FMT-VERB-MIX';

export interface ClickHouseSqlViolation {
  file: string;
  line: number;
  rule: ClickHouseSqlRuleId;
  message: string;
}

type TokenKind = 'word' | 'string' | 'param' | 'punct';

interface SqlToken {
  kind: TokenKind;
  text: string;
  lower: string;
  quoted: boolean;
  line: number;
}

const AGGREGATE_FUNCTION_PATTERN =
  /^(?:sum|min|max|avg|count|argMax|argMin|any|anyLast|uniq|quantile|groupArray|median|stddevPop|stddevSamp|varPop|varSamp)(?:If|State|Merge|Distinct|Exact|OrNull|OrDefault|Timing|Combinator)?$/iu;

const RESERVED_CLICKHOUSE_ALIASES = new Set<string>([
  'rollup',
  'cube',
  'totals',
  'array',
  'global',
  'final',
  'sample',
  'prewhere',
  'settings',
  'format',
  'window',
  'qualify',
  'having',
  'where',
  'group',
  'order',
  'limit',
  'offset',
  'union',
  'except',
  'intersect',
  'into',
  'from',
  'join',
  'using',
  'on',
  'select',
  'with',
  'distinct',
  'all',
  'by',
  'as',
]);

const SELECT_CLAUSE_END_KEYWORDS = new Set<string>([
  'from',
  'where',
  'prewhere',
  'group',
  'having',
  'window',
  'qualify',
  'order',
  'limit',
  'offset',
  'union',
  'except',
  'intersect',
  'into',
  'settings',
  'format',
]);

const WHERE_CLAUSE_END_KEYWORDS = new Set<string>([
  'group',
  'having',
  'window',
  'qualify',
  'order',
  'limit',
  'offset',
  'union',
  'except',
  'intersect',
  'into',
  'settings',
  'format',
  'prewhere',
  'where',
]);

const ALIAS_FOLLOWERS = new Set<string>([
  ',',
  ')',
  ';',
  'from',
  'where',
  'prewhere',
  'group',
  'having',
  'window',
  'qualify',
  'order',
  'limit',
  'offset',
  'union',
  'except',
  'intersect',
  'into',
  'settings',
  'format',
  'join',
  'left',
  'right',
  'inner',
  'outer',
  'full',
  'cross',
  'semi',
  'anti',
  'any',
  'all',
  'asof',
  'global',
  'array',
  'on',
  'using',
  'final',
  'sample',
]);

export function tokenizeSql(sql: string, baseLine = 1): SqlToken[] {
  const tokens: SqlToken[] = [];
  let line = baseLine;
  let i = 0;

  while (i < sql.length) {
    const ch = sql[i];

    if (ch === '\n') {
      line += 1;
      i += 1;
      continue;
    }
    if (/\s/u.test(ch)) {
      i += 1;
      continue;
    }

    // Line comment: -- ...
    if (ch === '-' && sql[i + 1] === '-') {
      i += 2;
      while (i < sql.length && sql[i] !== '\n') i += 1;
      continue;
    }

    // Block comment: /* ... */
    if (ch === '/' && sql[i + 1] === '*') {
      i += 2;
      while (i < sql.length && !(sql[i] === '*' && sql[i + 1] === '/')) {
        if (sql[i] === '\n') line += 1;
        i += 1;
      }
      i += 2;
      continue;
    }

    // Single-quoted string literal
    if (ch === "'") {
      const startLine = line;
      i += 1;
      while (i < sql.length) {
        if (sql[i] === '\n') line += 1;
        if (sql[i] === '\\') {
          i += 2;
          continue;
        }
        if (sql[i] === "'") {
          if (sql[i + 1] === "'") {
            i += 2;
            continue;
          }
          i += 1;
          break;
        }
        i += 1;
      }
      tokens.push({ kind: 'string', text: "''", lower: "''", quoted: false, line: startLine });
      continue;
    }

    // Double-quoted or backtick-quoted identifier
    if (ch === '"' || ch === '`') {
      const quote = ch;
      const startLine = line;
      let ident = '';
      i += 1;
      while (i < sql.length) {
        if (sql[i] === '\n') line += 1;
        if (sql[i] === '\\') {
          ident += sql[i + 1] ?? '';
          i += 2;
          continue;
        }
        if (sql[i] === quote) {
          if (sql[i + 1] === quote) {
            ident += quote;
            i += 2;
            continue;
          }
          i += 1;
          break;
        }
        ident += sql[i];
        i += 1;
      }
      tokens.push({ kind: 'word', text: ident, lower: ident.toLowerCase(), quoted: true, line: startLine });
      continue;
    }

    // ClickHouse query parameter syntax: {name:Type}
    if (ch === '{') {
      const startLine = line;
      let depth = 1;
      i += 1;
      while (i < sql.length && depth > 0) {
        if (sql[i] === '\n') line += 1;
        if (sql[i] === "'") {
          i += 1;
          while (i < sql.length && sql[i] !== "'") {
            if (sql[i] === '\\') i += 1;
            if (sql[i] === '\n') line += 1;
            i += 1;
          }
        } else if (sql[i] === '{') {
          depth += 1;
        } else if (sql[i] === '}') {
          depth -= 1;
        }
        i += 1;
      }
      tokens.push({ kind: 'param', text: '{}', lower: '{}', quoted: false, line: startLine });
      continue;
    }

    if (/[A-Za-z_]/u.test(ch)) {
      const start = i;
      i += 1;
      while (i < sql.length && /[A-Za-z0-9_$]/u.test(sql[i])) i += 1;
      const text = sql.slice(start, i);
      tokens.push({ kind: 'word', text, lower: text.toLowerCase(), quoted: false, line });
      continue;
    }

    if (/[0-9]/u.test(ch)) {
      const start = i;
      i += 1;
      while (i < sql.length && /[0-9.eExXa-fA-F_]/u.test(sql[i])) i += 1;
      const text = sql.slice(start, i);
      tokens.push({ kind: 'word', text, lower: text.toLowerCase(), quoted: false, line });
      continue;
    }

    tokens.push({ kind: 'punct', text: ch, lower: ch, quoted: false, line });
    i += 1;
  }

  return tokens;
}

function findMatchingParen(tokens: SqlToken[], openIndex: number): number {
  let depth = 0;
  for (let i = openIndex; i < tokens.length; i += 1) {
    if (tokens[i].text === '(') depth += 1;
    else if (tokens[i].text === ')') {
      depth -= 1;
      if (depth === 0) return i;
    }
  }
  return tokens.length - 1;
}

function splitTopLevelCommas(tokens: SqlToken[]): SqlToken[][] {
  const items: SqlToken[][] = [];
  let current: SqlToken[] = [];
  let depth = 0;
  for (const token of tokens) {
    if (token.text === '(') depth += 1;
    else if (token.text === ')' && depth > 0) depth -= 1;
    if (depth === 0 && token.text === ',') {
      items.push(current);
      current = [];
      continue;
    }
    current.push(token);
  }
  if (current.length > 0 || items.length > 0) items.push(current);
  return items;
}

interface AggregateAlias {
  aliasLower: string;
  aliasRaw: string;
  line: number;
  itemIndex: number;
}

function containsTopScopeAggregate(tokens: SqlToken[]): boolean {
  for (let i = 0; i < tokens.length - 1; i += 1) {
    const token = tokens[i];
    if (token.kind !== 'word' || token.quoted) continue;
    if (token.lower === 'select' && i > 0 && tokens[i - 1].text === '(') {
      i = findMatchingParen(tokens, i - 1);
      continue;
    }
    if (tokens[i + 1].text === '(' && AGGREGATE_FUNCTION_PATTERN.test(token.text)) {
      return true;
    }
  }
  return false;
}

// Collect identifier references outside nested (SELECT ...) subqueries, returning each reference
// along with whether it is unqualified (`x`) vs qualified (`tbl.x`) and whether it sits inside an
// aggregate function call.
interface IdentifierRef {
  lower: string;
  qualified: boolean;
  insideAggregate: boolean;
  line: number;
}

function collectIdentifierRefs(tokens: SqlToken[]): IdentifierRef[] {
  const refs: IdentifierRef[] = [];
  const parenKinds: Array<'agg' | 'subquery' | 'other'> = [];
  let aggDepth = 0;

  for (let i = 0; i < tokens.length; i += 1) {
    const token = tokens[i];
    if (token.text === '(') {
      const next = tokens[i + 1];
      if (next && next.kind === 'word' && !next.quoted && (next.lower === 'select' || next.lower === 'with')) {
        i = findMatchingParen(tokens, i);
        continue;
      }
      const prev = i > 0 ? tokens[i - 1] : undefined;
      if (prev && prev.kind === 'word' && !prev.quoted && AGGREGATE_FUNCTION_PATTERN.test(prev.text)) {
        parenKinds.push('agg');
        aggDepth += 1;
      } else {
        parenKinds.push('other');
      }
      continue;
    }
    if (token.text === ')') {
      const popped = parenKinds.pop();
      if (popped === 'agg' && aggDepth > 0) aggDepth -= 1;
      continue;
    }
    if (token.kind !== 'word') continue;

    // Function name call (`fn(...)`) is never a column/alias reference.
    if (tokens[i + 1]?.text === '(') continue;
    // Left side of `tbl.col` is a qualifier, not a column reference.
    if (tokens[i + 1]?.text === '.') continue;
    // Tuple field access like `last.1` is numeric, ignore pure numbers.
    if (/^[0-9]/u.test(token.text)) continue;

    const prev = i > 0 ? tokens[i - 1] : undefined;
    const isQualified = prev?.text === '.';
    refs.push({
      lower: token.lower,
      qualified: isQualified,
      insideAggregate: aggDepth > 0,
      line: token.line,
    });
  }

  return refs;
}

function isCastCallContext(parenStack: string[]): boolean {
  for (let i = parenStack.length - 1; i >= 0; i -= 1) {
    if (parenStack[i].includes('cast')) return true;
  }
  return false;
}

function checkReservedAliases(
  file: string,
  tokens: SqlToken[],
  violations: ClickHouseSqlViolation[],
): void {
  const parenCallStack: string[] = [];
  let inStatementHead = true; // e.g., CREATE VIEW ... AS SELECT ... before first SELECT/WITH body

  for (let i = 0; i < tokens.length; i += 1) {
    const token = tokens[i];
    if (token.text === '(') {
      const prev = i > 0 ? tokens[i - 1] : undefined;
      parenCallStack.push(prev?.kind === 'word' ? prev.lower : '');
      continue;
    }
    if (token.text === ')') {
      parenCallStack.pop();
      continue;
    }
    if (token.text === ';') {
      inStatementHead = true;
      continue;
    }

    if (token.kind === 'word' && !token.quoted) {
      if (token.lower === 'select' || (token.lower === 'with' && tokens[i + 1]?.text !== 'rollup' && tokens[i + 1]?.text !== 'cube' && tokens[i + 1]?.text !== 'totals')) {
        // After CREATE ... VIEW ... AS SELECT/WITH, we enter normal query clauses.
        if (i === 0 || tokens[i - 1].lower === 'as' || tokens[i - 1].text === '(') {
          inStatementHead = false;
        }
        if (i === 0) inStatementHead = false;
      }

      if (token.lower === 'as') {
        // `CREATE VIEW ... AS SELECT|WITH` starts a view query, never a column/table alias.
        if (inStatementHead) {
          inStatementHead = false;
          continue;
        }
        if (isCastCallContext(parenCallStack)) continue;
        const next = tokens[i + 1];
        if (!next || next.kind !== 'word' || next.quoted) continue;
        const afterNext = tokens[i + 2];
        // Skip type expressions like `AS Array(String)` or CTE `WITH x AS (...)`.
        if (afterNext?.text === '(' || afterNext?.text === '.') continue;
        // Skip GROUP BY ... WITH ROLLUP / WITH CUBE / WITH TOTALS (no AS anyway).
        if (!RESERVED_CLICKHOUSE_ALIASES.has(next.lower)) continue;
        // Skip ClickHouse expression CTE: `WITH <expr> AS <alias>` is handled cleanly; still flag reserved words there.
        if (!afterNext || ALIAS_FOLLOWERS.has(afterNext.lower)) {
          violations.push({
            file,
            line: next.line,
            rule: 'CH-RESERVED-ALIAS',
            message: `Unquoted alias "AS ${next.text}" collides with a ClickHouse reserved keyword (Code: 62); rename or double-quote the alias.`,
          });
        }
      }
    }
  }
}

function analyzeSelectScopes(
  file: string,
  tokens: SqlToken[],
  violations: ClickHouseSqlViolation[],
): void {
  // Recursively inspect every parenthesized block independently first.
  for (let i = 0; i < tokens.length; i += 1) {
    if (tokens[i].text === '(') {
      const close = findMatchingParen(tokens, i);
      analyzeSelectScopes(file, tokens.slice(i + 1, close), violations);
      i = close;
    }
  }

  // Split sequential statements by top-level `;` or `UNION ...` at depth 0.
  let pos = 0;
  while (pos < tokens.length) {
    // Handle ClickHouse expression WITH clauses preceding a top-level SELECT.
    const withAggMacros = new Map<string, { line: number; exprTokens: SqlToken[] }>();

    if (tokens[pos].kind === 'word' && !tokens[pos].quoted && tokens[pos].lower === 'with') {
      let scan = pos + 1;
      let depth = 0;
      while (scan < tokens.length) {
        if (tokens[scan].text === '(') depth += 1;
        else if (tokens[scan].text === ')' && depth > 0) depth -= 1;
        else if (depth === 0 && tokens[scan].kind === 'word' && !tokens[scan].quoted && tokens[scan].lower === 'select') {
          break;
        }
        scan += 1;
      }
      const withBody = tokens.slice(pos + 1, scan);
      for (const cteItem of splitTopLevelCommas(withBody)) {
        if (cteItem.length < 3) continue;
        // Standard CTE: `<name> AS (...)`
        const second = cteItem[1];
        if (second?.kind === 'word' && second.lower === 'as' && cteItem[2]?.text === '(') {
          continue;
        }
        // Expression CTE: `WITH <expr> AS <name>`
        const asIdx = cteItem.length - 2;
        if (asIdx >= 1 && cteItem[asIdx].kind === 'word' && !cteItem[asIdx].quoted && cteItem[asIdx].lower === 'as') {
          const macroName = cteItem[asIdx + 1];
          const exprTokens = cteItem.slice(0, asIdx);
          if (macroName.kind === 'word') {
            const refs = collectIdentifierRefs(exprTokens);
            if (refs.some((ref) => !ref.qualified && ref.lower === macroName.lower)) {
              violations.push({
                file,
                line: macroName.line,
                rule: 'CH-WITH-ALIAS-SHADOW',
                message: `ClickHouse WITH expression "... AS ${macroName.text}" references its own alias (cyclic alias).`,
              });
            }
            if (containsTopScopeAggregate(exprTokens)) {
              withAggMacros.set(macroName.lower, { line: macroName.line, exprTokens });
            }
          }
        }
      }
      pos = scan;
      if (pos >= tokens.length) break;
    }

    // Find next top-level SELECT at depth 0.
    if (!(tokens[pos].kind === 'word' && !tokens[pos].quoted && tokens[pos].lower === 'select')) {
      pos += 1;
      continue;
    }

    let selectEnd = pos + 1;
    let depth = 0;
    while (selectEnd < tokens.length) {
      const t = tokens[selectEnd];
      if (t.text === '(') depth += 1;
      else if (t.text === ')' && depth > 0) depth -= 1;
      else if (depth === 0 && t.text === ';') break;
      else if (
        depth === 0 &&
        t.kind === 'word' &&
        !t.quoted &&
        (t.lower === 'union' || t.lower === 'except' || t.lower === 'intersect')
      ) {
        break;
      }
      selectEnd += 1;
    }

    const stmt = tokens.slice(pos, selectEnd);
    pos = selectEnd + 1;

    // Extract SELECT projection slice, WHERE slice, PREWHERE slice at depth 0.
    let projStart = 1;
    if (stmt[projStart]?.kind === 'word' && !stmt[projStart].quoted && stmt[projStart].lower === 'distinct') {
      projStart += 1;
    }
    let projEnd = stmt.length;
    depth = 0;
    for (let k = projStart; k < stmt.length; k += 1) {
      if (stmt[k].text === '(') depth += 1;
      else if (stmt[k].text === ')' && depth > 0) depth -= 1;
      else if (depth === 0 && stmt[k].kind === 'word' && !stmt[k].quoted && SELECT_CLAUSE_END_KEYWORDS.has(stmt[k].lower)) {
        projEnd = k;
        break;
      }
    }

    const selectItems = splitTopLevelCommas(stmt.slice(projStart, projEnd));
    const aggAliases = new Map<string, AggregateAlias>();
    const parsedItems: Array<{ exprTokens: SqlToken[]; aliasLower?: string; itemIndex: number }> = [];

    selectItems.forEach((itemTokens, itemIndex) => {
      if (itemTokens.length === 0) return;
      let exprTokens = itemTokens;
      let aliasLower: string | undefined;
      let aliasRaw: string | undefined;
      let aliasLine = itemTokens[0].line;
      if (
        itemTokens.length >= 3 &&
        itemTokens[itemTokens.length - 2].kind === 'word' &&
        !itemTokens[itemTokens.length - 2].quoted &&
        itemTokens[itemTokens.length - 2].lower === 'as' &&
        itemTokens[itemTokens.length - 1].kind === 'word'
      ) {
        const aliasToken = itemTokens[itemTokens.length - 1];
        aliasLower = aliasToken.lower;
        aliasRaw = aliasToken.text;
        aliasLine = aliasToken.line;
        exprTokens = itemTokens.slice(0, -2);
      }

      parsedItems.push({ exprTokens, aliasLower, itemIndex });
      if (aliasLower && aliasRaw && containsTopScopeAggregate(exprTokens)) {
        aggAliases.set(aliasLower, { aliasLower, aliasRaw, line: aliasLine, itemIndex });
      }
    });

    // Rule 1a: Aggregate alias shadowed inside another SELECT aggregate in the same scope (PR #917).
    for (const item of parsedItems) {
      const refs = collectIdentifierRefs(item.exprTokens);
      for (const ref of refs) {
        if (!ref.insideAggregate) continue;
        const shadowed = aggAliases.get(ref.lower);
        if (shadowed && shadowed.itemIndex !== item.itemIndex) {
          violations.push({
            file,
            line: ref.line,
            rule: 'CH-ALIAS-SHADOW',
            message: `Aggregate expression references "${shadowed.aliasRaw}" which collides with aggregate alias "AS ${shadowed.aliasRaw}" in the same SELECT (ClickHouse Code: 184 ILLEGAL_AGGREGATION); rename the alias.`,
          });
        }
        const withAgg = withAggMacros.get(ref.lower);
        if (withAgg) {
          violations.push({
            file,
            line: ref.line,
            rule: 'CH-WITH-ALIAS-SHADOW',
            message: `Aggregate expression references WITH aggregate macro "${ref.lower}" declared on line ${withAgg.line} (nested aggregate / Code: 184).`,
          });
        }
      }
    }

    // Extract top-level WHERE, PREWHERE, and HAVING slices at depth 0 (PR #803 / #917).
    depth = 0;
    for (let k = projEnd; k < stmt.length; k += 1) {
      if (stmt[k].text === '(') {
        depth += 1;
        continue;
      }
      if (stmt[k].text === ')' && depth > 0) {
        depth -= 1;
        continue;
      }
      if (
        depth === 0 &&
        stmt[k].kind === 'word' &&
        !stmt[k].quoted &&
        (stmt[k].lower === 'where' || stmt[k].lower === 'prewhere' || stmt[k].lower === 'having')
      ) {
        const clauseKeyword = stmt[k].lower;
        let clauseEnd = k + 1;
        let innerDepth = 0;
        while (clauseEnd < stmt.length) {
          if (stmt[clauseEnd].text === '(') innerDepth += 1;
          else if (stmt[clauseEnd].text === ')' && innerDepth > 0) innerDepth -= 1;
          else if (
            innerDepth === 0 &&
            stmt[clauseEnd].kind === 'word' &&
            !stmt[clauseEnd].quoted &&
            WHERE_CLAUSE_END_KEYWORDS.has(stmt[clauseEnd].lower)
          ) {
            break;
          }
          clauseEnd += 1;
        }
        const clauseTokens = stmt.slice(k + 1, clauseEnd);
        for (const ref of collectIdentifierRefs(clauseTokens)) {
          const shadowed = aggAliases.get(ref.lower);
          if (clauseKeyword === 'having') {
            if (shadowed && ref.insideAggregate) {
              violations.push({
                file,
                line: ref.line,
                rule: 'CH-ALIAS-SHADOW',
                message: `HAVING aggregate references "${shadowed.aliasRaw}" which collides with aggregate alias "AS ${shadowed.aliasRaw}" in SELECT (ClickHouse Code: 184 ILLEGAL_AGGREGATION); drop or rename the SELECT alias.`,
              });
            }
            continue;
          }
          if (shadowed && !ref.qualified) {
            violations.push({
              file,
              line: ref.line,
              rule: 'CH-ALIAS-SHADOW',
              message: `WHERE/PREWHERE references unqualified "${shadowed.aliasRaw}", which resolves to SELECT aggregate alias "AS ${shadowed.aliasRaw}" (ClickHouse Code: 184); qualify with table alias or rename the SELECT alias.`,
            });
          }
          const withAgg = withAggMacros.get(ref.lower);
          if (withAgg && !ref.qualified) {
            violations.push({
              file,
              line: ref.line,
              rule: 'CH-WITH-ALIAS-SHADOW',
              message: `WHERE/PREWHERE references WITH aggregate macro "${ref.lower}" declared on line ${withAgg.line} (ClickHouse Code: 184).`,
            });
          }
        }
        k = clauseEnd - 1;
      }
    }
  }
}

export function findClickHouseSqlViolations(
  file: string,
  sql: string,
  baseLine = 1,
): ClickHouseSqlViolation[] {
  const violations: ClickHouseSqlViolation[] = [];
  const tokens = tokenizeSql(sql, baseLine);
  if (tokens.length === 0) return violations;
  checkReservedAliases(file, tokens, violations);
  analyzeSelectScopes(file, tokens, violations);
  return violations;
}

interface ExtractedGoString {
  text: string;
  line: number;
  inFmtSprintf: boolean;
}

function unescapeGoDoubleQuoted(rawInner: string): string {
  return rawInner
    .replace(/\\n/gu, '\n')
    .replace(/\\t/gu, '\t')
    .replace(/\\r/gu, '\r')
    .replace(/\\"/gu, '"')
    .replace(/\\\\/gu, '\\');
}

export function checkGoFmtVerbMix(formatText: string): boolean {
  const withoutPercentLiteral = formatText.replace(/%%/gu, '');
  const hasExplicitIndex = /%\[\d+\][+#\-0 *]*(?:\.\d+|\*)?[bcdeEfFgGoqsStTvUxX]/u.test(withoutPercentLiteral);
  if (!hasExplicitIndex) return false;
  return /%(?!\[\d+\])[+#\-0 *]*(?:\.\d+|\*)?[bcdeEfFgGoqsStTvUxX]/u.test(withoutPercentLiteral);
}

function sanitizeGoFmtForSql(text: string): string {
  return text
    .replace(/%%/gu, '%')
    .replace(/%(?:\[\d+\])?[+#\-0 *]*(?:\.\d+|\*)?[bcdeEfFgGoqsStTvUxX]/gu, 'ch_fmt_param');
}

function looksLikeClickHouseSql(text: string): boolean {
  return /\b(?:SELECT|CREATE\s+(?:OR\s+REPLACE\s+)?(?:MATERIALIZED\s+)?VIEW|INSERT\s+INTO|WITH\s+[A-Za-z_])/iu.test(text) &&
    (/\bFROM\b/iu.test(text) || /\bFORMAT\b/iu.test(text) || /\bVIEW\b/iu.test(text));
}

export function extractGoClickHouseSqlLiterals(goSource: string): ExtractedGoString[] {
  const results: ExtractedGoString[] = [];
  let line = 1;
  let i = 0;

  while (i < goSource.length) {
    const ch = goSource[i];
    if (ch === '\n') {
      line += 1;
      i += 1;
      continue;
    }
    // Line comment
    if (ch === '/' && goSource[i + 1] === '/') {
      while (i < goSource.length && goSource[i] !== '\n') i += 1;
      continue;
    }
    // Block comment
    if (ch === '/' && goSource[i + 1] === '*') {
      i += 2;
      while (i < goSource.length && !(goSource[i] === '*' && goSource[i + 1] === '/')) {
        if (goSource[i] === '\n') line += 1;
        i += 1;
      }
      i += 2;
      continue;
    }
    // Rune literal
    if (ch === "'") {
      i += 1;
      if (goSource[i] === '\\') i += 2;
      while (i < goSource.length && goSource[i] !== "'" && goSource[i] !== '\n') i += 1;
      i += 1;
      continue;
    }
    // Raw string `...` or double-quoted "..."
    if (ch === '`' || ch === '"') {
      const startLine = line;
      const before = goSource.slice(Math.max(0, i - 32), i);
      const inFmtSprintf = /fmt\.Sprintf\s*\(\s*$/u.test(before);
      let raw = '';
      if (ch === '`') {
        i += 1;
        const start = i;
        while (i < goSource.length && goSource[i] !== '`') {
          if (goSource[i] === '\n') line += 1;
          i += 1;
        }
        raw = goSource.slice(start, i);
        i += 1;
      } else {
        i += 1;
        const start = i;
        while (i < goSource.length) {
          if (goSource[i] === '\\') {
            i += 2;
            continue;
          }
          if (goSource[i] === '"' || goSource[i] === '\n') break;
          i += 1;
        }
        raw = unescapeGoDoubleQuoted(goSource.slice(start, i));
        i += 1;
      }
      if (looksLikeClickHouseSql(raw)) {
        results.push({ text: raw, line: startLine, inFmtSprintf });
      }
      continue;
    }
    i += 1;
  }

  return results;
}

export function findClickHouseViolationsInSource(file: string, content: string): ClickHouseSqlViolation[] {
  const violations: ClickHouseSqlViolation[] = [];
  if (file.endsWith('.sql')) {
    return findClickHouseSqlViolations(file, content, 1);
  }
  if (file.endsWith('.sh')) {
    const queryMatches = content.matchAll(/(?:query|\$\([^)]*)\s*"([^"]*)"/gsu);
    for (const match of queryMatches) {
      const sql = match[1].replace(/\$\{?[A-Za-z_][A-Za-z0-9_]*\}?/gu, 'sh_var');
      if (!looksLikeClickHouseSql(sql)) continue;
      const startLine = content.slice(0, match.index ?? 0).split('\n').length;
      violations.push(...findClickHouseSqlViolations(file, sql, startLine));
    }
    return violations;
  }
  if (file.endsWith('.go')) {
    for (const literal of extractGoClickHouseSqlLiterals(content)) {
      if (literal.inFmtSprintf && checkGoFmtVerbMix(literal.text)) {
        violations.push({
          file,
          line: literal.line,
          rule: 'CH-FMT-VERB-MIX',
          message: 'fmt.Sprintf SQL format string mixes positional verbs (%[1]s) with unindexed verbs (%s/%d); use consistent positional or unindexed verbs.',
        });
      }
      violations.push(...findClickHouseSqlViolations(file, sanitizeGoFmtForSql(literal.text), literal.line));
    }
  }
  return violations;
}

export function collectClickHouseCandidateFiles(root: string): string[] {
  const tracked = execFileSync('git', ['ls-files', '--', 'infra/telemetry/clickhouse', 'modules', 'services', 'cmd'], {
    cwd: root,
    encoding: 'utf8',
    maxBuffer: 16 << 20,
  })
    .split(/\r?\n/u)
    .filter(Boolean);

  return tracked.filter((file) => {
    if (file.endsWith('.sql') && file.startsWith('infra/telemetry/clickhouse/')) return true;
    if (file.endsWith('.sh') && file.startsWith('infra/telemetry/clickhouse/')) return true;
    if (!file.endsWith('.go') || file.endsWith('_test.go')) return false;
    if (file.toLowerCase().includes('clickhouse') || file.endsWith('/history/aggregate.go')) return true;
    const body = readFileSync(resolve(root, file), 'utf8');
    return /ClickHouse|FORMAT\s+(?:JSONEachRow|TabSeparated)/iu.test(body);
  });
}

export function checkClickHouseSqlRepository(root: string): ClickHouseSqlViolation[] {
  const files = collectClickHouseCandidateFiles(root);
  const violations: ClickHouseSqlViolation[] = [];
  for (const file of files) {
    const abs = resolve(root, file);
    if (!existsSync(abs)) continue;
    violations.push(...findClickHouseViolationsInSource(file, readFileSync(abs, 'utf8')));
  }
  return violations;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
  const violations = checkClickHouseSqlRepository(root);
  if (violations.length > 0) {
    for (const v of violations) {
      console.error(`${v.file}:${v.line} [${v.rule}] ${v.message}`);
    }
    process.exit(1);
  }
  console.log('ClickHouse SQL dialect check passed.');
}
