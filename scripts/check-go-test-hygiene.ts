import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

export const GO_TEST_SLEEP_BASELINE_PATH = 'scripts/go-test-sleep-baseline.json';

export interface GoTestSleepBaseline {
  maxTotal: number;
  counts: Record<string, number>;
}

const normalizePath = (value: string): string => value.replaceAll('\\', '/').replace(/^\.\//u, '').trim();

export function stripGoCommentsAndStrings(source: string): string {
  const out: string[] = [];
  let i = 0;
  while (i < source.length) {
    const ch = source[i];
    // Line comment
    if (ch === '/' && source[i + 1] === '/') {
      out.push(' ', ' ');
      i += 2;
      while (i < source.length && source[i] !== '\n') {
        out.push(' ');
        i += 1;
      }
      continue;
    }
    // Block comment
    if (ch === '/' && source[i + 1] === '*') {
      out.push(' ', ' ');
      i += 2;
      while (i < source.length && !(source[i] === '*' && source[i + 1] === '/')) {
        out.push(source[i] === '\n' ? '\n' : ' ');
        i += 1;
      }
      if (i < source.length) {
        out.push(' ', ' ');
        i += 2;
      }
      continue;
    }
    // Raw string literal `...`
    if (ch === '`') {
      out.push(' ');
      i += 1;
      while (i < source.length && source[i] !== '`') {
        out.push(source[i] === '\n' ? '\n' : ' ');
        i += 1;
      }
      if (i < source.length) {
        out.push(' ');
        i += 1;
      }
      continue;
    }
    // Interpreted string literal "..." or rune '...'
    if (ch === '"' || ch === "'") {
      const quote = ch;
      out.push(' ');
      i += 1;
      while (i < source.length && source[i] !== quote) {
        if (source[i] === '\\' && i + 1 < source.length) {
          out.push(' ', source[i + 1] === '\n' ? '\n' : ' ');
          i += 2;
          continue;
        }
        out.push(source[i] === '\n' ? '\n' : ' ');
        i += 1;
      }
      if (i < source.length) {
        out.push(' ');
        i += 1;
      }
      continue;
    }
    out.push(ch);
    i += 1;
  }
  return out.join('');
}

export function countTimeSleeps(source: string): number {
  const stripped = stripGoCommentsAndStrings(source);
  const matches = stripped.match(/\btime\s*\.\s*Sleep\s*\(/gu);
  return matches ? matches.length : 0;
}

interface GoFunctionSlice {
  startLine: number;
  body: string;
}

function extractGoFunctions(strippedSource: string): GoFunctionSlice[] {
  const slices: GoFunctionSlice[] = [];
  let braceDepth = 0;
  let i = 0;
  let line = 1;

  while (i < strippedSource.length) {
    const ch = strippedSource[i];
    if (ch === '\n') {
      line += 1;
      i += 1;
      continue;
    }
    if (
      braceDepth === 0 &&
      strippedSource.startsWith('func', i) &&
      (i === 0 || !/[A-Za-z0-9_]/u.test(strippedSource[i - 1])) &&
      !/[A-Za-z0-9_]/u.test(strippedSource[i + 4] ?? '')
    ) {
      const startLine = line;
      let j = i + 4;
      let parenDepth = 0;
      while (j < strippedSource.length) {
        const c = strippedSource[j];
        if (c === '(') parenDepth += 1;
        else if (c === ')' && parenDepth > 0) parenDepth -= 1;
        else if (c === '{' && parenDepth === 0) break;
        j += 1;
      }
      if (j < strippedSource.length && strippedSource[j] === '{') {
        const bodyStart = j;
        let fnDepth = 0;
        let k = j;
        while (k < strippedSource.length) {
          if (strippedSource[k] === '{') fnDepth += 1;
          else if (strippedSource[k] === '}') {
            fnDepth -= 1;
            if (fnDepth === 0) {
              k += 1;
              break;
            }
          }
          k += 1;
        }
        slices.push({ startLine, body: strippedSource.slice(bodyStart, k) });
        line += strippedSource.slice(i, k).split('\n').length - 1;
        i = k;
        continue;
      }
    }
    if (ch === '{') braceDepth += 1;
    else if (ch === '}' && braceDepth > 0) braceDepth -= 1;
    i += 1;
  }
  return slices;
}

export function findUnsynchronizedLogBufferViolations(file: string, source: string): string[] {
  const violations: string[] = [];
  const stripped = stripGoCommentsAndStrings(source);
  for (const fn of extractGoFunctions(stripped)) {
    const hasConcurrency = /\bgo\s+/u.test(fn.body) || /\.Start(?:TLS)?\s*\(/u.test(fn.body);
    if (!hasConcurrency) continue;

    const bufferVars = new Set<string>();
    const varDeclMatches = fn.body.matchAll(
      /(?:^|\n)[ \t]*(?:var\s+([A-Za-z_]\w*)\s+\*?bytes\.Buffer\b|([A-Za-z_]\w*)\s*:=\s*(?:&\s*bytes\.Buffer\s*\{\s*\}|bytes\.Buffer\s*\{\s*\}|new\s*\(\s*bytes\.Buffer\s*\)))/gu,
    );
    for (const match of varDeclMatches) {
      const name = match[1] ?? match[2];
      if (name) bufferVars.add(name);
    }

    const loggerCalls = fn.body.matchAll(
      /\b(?:slog\.(?:NewTextHandler|NewJSONHandler)|log\.New|observability\.NewJSONLogger)\s*\(\s*([^,)]+)/gu,
    );
    for (const match of loggerCalls) {
      const targetLine = fn.startLine + fn.body.slice(0, match.index ?? 0).split('\n').length - 1;
      const firstArg = match[1].trim();
      const inlineBuffer = /^(?:&\s*bytes\.Buffer\s*\{\s*\}|new\s*\(\s*bytes\.Buffer\s*\))$/u.test(firstArg);
      const identMatch = firstArg.match(/^&?\s*([A-Za-z_]\w*)$/u);
      const localBuffer = identMatch ? bufferVars.has(identMatch[1]) : false;
      if (inlineBuffer || localBuffer) {
        violations.push(
          `${file}:${targetLine}: unsynchronized bytes.Buffer passed to concurrent logger (${match[0].split('(')[0]}); wrap writes and reads in a sync.Mutex buffer instead.`,
        );
      }
    }
  }
  return violations;
}

export function buildGoTestSleepBaseline(
  root: string,
  trackedFiles: readonly string[],
): GoTestSleepBaseline {
  const counts: Record<string, number> = {};
  let maxTotal = 0;
  for (const file of trackedFiles.map(normalizePath).filter((p) => p.endsWith('_test.go')).sort()) {
    const abs = join(root, file);
    if (!existsSync(abs)) continue;
    const count = countTimeSleeps(readFileSync(abs, 'utf8'));
    if (count > 0) {
      counts[file] = count;
      maxTotal += count;
    }
  }
  return { maxTotal, counts };
}

export function findGoTestSleepRatchetViolations(inputs: {
  root: string;
  trackedFiles: readonly string[];
  baseline: GoTestSleepBaseline;
}): string[] {
  const violations: string[] = [];
  const testFiles = inputs.trackedFiles.map(normalizePath).filter((p) => p.endsWith('_test.go'));
  const trackedSet = new Set(testFiles);

  for (const baselineFile of Object.keys(inputs.baseline.counts)) {
    if (!trackedSet.has(normalizePath(baselineFile))) {
      violations.push(
        `${GO_TEST_SLEEP_BASELINE_PATH}: lists stale test file ${baselineFile} no longer tracked; shrink the baseline.`,
      );
    }
  }

  let total = 0;
  for (const file of testFiles) {
    const abs = join(inputs.root, file);
    if (!existsSync(abs)) continue;
    const count = countTimeSleeps(readFileSync(abs, 'utf8'));
    total += count;
    const allowed = inputs.baseline.counts[file] ?? 0;
    if (count > allowed) {
      violations.push(
        `${file}: has ${count} time.Sleep() call(s) (baseline ${allowed}); replace sleeps with channels, injected clocks, or bounded condition polling and shrink ${GO_TEST_SLEEP_BASELINE_PATH}.`,
      );
    }
  }

  if (total > inputs.baseline.maxTotal) {
    violations.push(
      `${GO_TEST_SLEEP_BASELINE_PATH}: total time.Sleep() calls (${total}) exceeds baseline maxTotal (${inputs.baseline.maxTotal}).`,
    );
  }
  return violations;
}

export function findGoTestHygieneViolations(
  root: string,
  trackedFiles: readonly string[],
  baselineOverride?: GoTestSleepBaseline,
): string[] {
  const baseline =
    baselineOverride ??
    (JSON.parse(readFileSync(join(root, GO_TEST_SLEEP_BASELINE_PATH), 'utf8')) as GoTestSleepBaseline);
  const violations = findGoTestSleepRatchetViolations({ root, trackedFiles, baseline });

  for (const file of trackedFiles.map(normalizePath).filter((p) => p.endsWith('_test.go'))) {
    const abs = join(root, file);
    if (!existsSync(abs)) continue;
    violations.push(...findUnsynchronizedLogBufferViolations(file, readFileSync(abs, 'utf8')));
  }
  return violations;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
  const trackedFiles = execFileSync('git', ['ls-files'], {
    cwd: root,
    encoding: 'utf8',
    maxBuffer: 16 << 20,
  })
    .split(/\r?\n/u)
    .filter(Boolean);

  if (process.argv.includes('--write-baseline')) {
    const baseline = buildGoTestSleepBaseline(root, trackedFiles);
    writeFileSync(join(root, GO_TEST_SLEEP_BASELINE_PATH), `${JSON.stringify(baseline, null, 2)}\n`, 'utf8');
    console.log(
      `Wrote ${GO_TEST_SLEEP_BASELINE_PATH}: ${Object.keys(baseline.counts).length} files, maxTotal=${baseline.maxTotal}.`,
    );
    process.exit(0);
  }

  const violations = findGoTestHygieneViolations(root, trackedFiles);
  if (violations.length > 0) {
    console.error(violations.join('\n'));
    process.exit(1);
  }
  console.log('Go test hygiene check passed.');
}
