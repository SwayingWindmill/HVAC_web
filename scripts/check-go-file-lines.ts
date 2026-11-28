import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

export const GO_FILE_LINES_BASELINE_PATH = 'scripts/go-file-lines-baseline.json';
export const DEFAULT_MAX_GO_LINES_PER_FILE = 800;

export interface GoFileLinesBaseline {
  schemaVersion: number;
  policy: string;
  maxLinesPerFile: number;
  legacyFileCaps: Record<string, number>;
}

const governedGoRoots = Object.freeze(['cmd/', 'modules/', 'services/', 'libs/', 'tools/']);

const normalizePath = (value: string): string => value.replaceAll('\\', '/').replace(/^\.\//u, '').trim();

export function countGoSourceLines(source: string): number {
  const normalized = source.replaceAll('\r\n', '\n');
  if (normalized.length === 0) return 0;
  const trimmed = normalized.endsWith('\n') ? normalized.slice(0, -1) : normalized;
  return trimmed.length === 0 ? 0 : trimmed.split('\n').length;
}

export function isGovernedGoSourceFile(rawPath: string): boolean {
  const normalized = normalizePath(rawPath);
  if (!governedGoRoots.some((prefix) => normalized.startsWith(prefix))) return false;
  if (!normalized.endsWith('.go')) return false;
  return !normalized.endsWith('_test.go') && !normalized.endsWith('.gen.go') && !normalized.endsWith('_gen.go');
}

export function buildGoFileLinesBaseline(
  root: string,
  trackedFiles: readonly string[],
  maxLinesPerFile = DEFAULT_MAX_GO_LINES_PER_FILE,
): GoFileLinesBaseline {
  const legacyFileCaps: Record<string, number> = {};
  for (const file of trackedFiles.map(normalizePath).filter(isGovernedGoSourceFile).sort()) {
    const abs = join(root, file);
    if (!existsSync(abs)) continue;
    const lines = countGoSourceLines(readFileSync(abs, 'utf8'));
    if (lines > maxLinesPerFile) {
      legacyFileCaps[file] = lines;
    }
  }
  return {
    schemaVersion: 1,
    policy: 'go-file-lines-ratchet',
    maxLinesPerFile,
    legacyFileCaps,
  };
}

function isValidBaseline(baseline: unknown): baseline is GoFileLinesBaseline {
  if (!baseline || typeof baseline !== 'object' || Array.isArray(baseline)) return false;
  const candidate = baseline as Partial<GoFileLinesBaseline>;
  return (
    candidate.schemaVersion === 1
    && candidate.policy === 'go-file-lines-ratchet'
    && Number.isInteger(candidate.maxLinesPerFile)
    && (candidate.maxLinesPerFile ?? 0) > 0
    && candidate.legacyFileCaps !== null
    && typeof candidate.legacyFileCaps === 'object'
    && !Array.isArray(candidate.legacyFileCaps)
  );
}

export function findGoFileLineViolations(
  root: string,
  trackedFiles: readonly string[],
  baselineOverride?: GoFileLinesBaseline,
): string[] {
  const baselinePath = join(root, GO_FILE_LINES_BASELINE_PATH);
  if (!baselineOverride && !existsSync(baselinePath)) {
    return [`${GO_FILE_LINES_BASELINE_PATH} is missing; add the Go file-line baseline file`];
  }
  const baseline = baselineOverride ?? (JSON.parse(readFileSync(baselinePath, 'utf8')) as unknown);
  if (!isValidBaseline(baseline)) {
    return [`${GO_FILE_LINES_BASELINE_PATH}: invalid or unsupported baseline schema`];
  }

  const normalizedCaps: Record<string, number> = {};
  for (const [rawPath, cap] of Object.entries(baseline.legacyFileCaps)) {
    normalizedCaps[normalizePath(rawPath)] = cap;
  }

  const governedLineCounts = new Map<string, number>();
  const violations: string[] = [];

  for (const file of trackedFiles.map(normalizePath).filter(isGovernedGoSourceFile).sort()) {
    const abs = join(root, file);
    if (!existsSync(abs)) continue;
    const lines = countGoSourceLines(readFileSync(abs, 'utf8'));
    governedLineCounts.set(file, lines);

    const cap = normalizedCaps[file];
    if (cap === undefined) {
      if (lines > baseline.maxLinesPerFile) {
        violations.push(
          `${file}: has ${lines} lines (limit ${baseline.maxLinesPerFile}); split it into cohesive files instead of growing oversized Go files`,
        );
      }
    } else if (!Number.isInteger(cap) || cap <= baseline.maxLinesPerFile) {
      violations.push(
        `${GO_FILE_LINES_BASELINE_PATH}: stale cap ${String(cap)} for ${file} (already <= ${baseline.maxLinesPerFile}); remove it`,
      );
    } else if (lines > cap) {
      violations.push(
        `${file}: grew from ${cap} to ${lines} lines (${GO_FILE_LINES_BASELINE_PATH}); split it instead of expanding legacy oversized Go files`,
      );
    } else if (lines <= baseline.maxLinesPerFile) {
      violations.push(
        `${GO_FILE_LINES_BASELINE_PATH}: stale entry for ${file} (${lines} <= ${baseline.maxLinesPerFile}); remove it`,
      );
    }
  }

  for (const legacyPath of Object.keys(normalizedCaps).sort()) {
    if (!governedLineCounts.has(legacyPath)) {
      violations.push(
        `${GO_FILE_LINES_BASELINE_PATH}: stale entry for missing/exempt file ${legacyPath}; remove it`,
      );
    }
  }

  return violations;
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
  const trackedFiles = execFileSync('git', ['ls-files'], {
    cwd: root,
    encoding: 'utf8',
    maxBuffer: 16 << 20,
  })
    .split(/\r?\n/u)
    .filter(Boolean);

  if (process.argv.includes('--write-baseline')) {
    const baseline = buildGoFileLinesBaseline(root, trackedFiles);
    writeFileSync(join(root, GO_FILE_LINES_BASELINE_PATH), `${JSON.stringify(baseline, null, 2)}\n`, 'utf8');
    console.log(
      `Wrote ${GO_FILE_LINES_BASELINE_PATH}: ${Object.keys(baseline.legacyFileCaps).length} legacy files over ${baseline.maxLinesPerFile} lines.`,
    );
    process.exit(0);
  }

  const violations = findGoFileLineViolations(root, trackedFiles);
  if (violations.length > 0) {
    console.error(violations.join('\n'));
    process.exit(1);
  }
  console.log('Go file-line ratchet check passed.');
}
