import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, relative, resolve } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';

interface IgnoreEntry {
  readonly type: string;
  readonly value: string;
}

interface ProjectConfig {
  readonly miniprogramRoot?: string;
  readonly packOptions?: {
    readonly ignore?: readonly IgnoreEntry[];
  };
}

export interface StaticImportViolation {
  readonly importer: string;
  readonly specifier: string;
  readonly target: string;
  readonly rule: IgnoreEntry;
}

const appRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');

function toPosixPath(value: string): string {
  return value.replace(/\\/g, '/');
}

function normalizeRuleValue(value: string): string {
  return toPosixPath(value).replace(/^\.\//, '').replace(/^\/+/, '').replace(/\/+$/, '');
}

export function matchingIgnoreRule(relPath: string, rules: readonly IgnoreEntry[]): IgnoreEntry | null {
  const normalized = toPosixPath(relPath).replace(/^\/+/, '');
  for (const rule of rules) {
    const target = normalizeRuleValue(rule.value);
    if (rule.type === 'file' && normalized === target) {
      return rule;
    }
    if (rule.type === 'folder' && (normalized === target || normalized.startsWith(`${target}/`))) {
      return rule;
    }
  }
  return null;
}

function collectTsFiles(directory: string): string[] {
  const files: string[] = [];
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const entryPath = resolve(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...collectTsFiles(entryPath));
    } else if (entry.isFile() && entry.name.endsWith('.ts') && !entry.name.endsWith('.d.ts')) {
      files.push(entryPath);
    }
  }
  return files.sort();
}

export function collectStaticModuleSpecifiers(filePath: string, source: string): string[] {
  const sourceFile = ts.createSourceFile(filePath, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const specifiers: string[] = [];

  const visit = (node: ts.Node): void => {
    if ((ts.isImportDeclaration(node) || ts.isExportDeclaration(node))
      && node.moduleSpecifier
      && ts.isStringLiteralLike(node.moduleSpecifier)) {
      specifiers.push(node.moduleSpecifier.text);
    } else if (ts.isImportEqualsDeclaration(node)
      && ts.isExternalModuleReference(node.moduleReference)
      && ts.isStringLiteralLike(node.moduleReference.expression)) {
      specifiers.push(node.moduleReference.expression.text);
    } else if (ts.isCallExpression(node)
      && ts.isIdentifier(node.expression)
      && node.expression.text === 'require'
      && node.arguments.length === 1
      && ts.isStringLiteralLike(node.arguments[0]!)) {
      specifiers.push(node.arguments[0]!.text);
    }
    ts.forEachChild(node, visit);
  };

  visit(sourceFile);
  return specifiers;
}

function isExistingFile(filePath: string): boolean {
  try {
    return statSync(filePath).isFile();
  } catch {
    return false;
  }
}

function resolveImportTarget(
  miniprogramDir: string,
  importerPath: string,
  specifier: string,
  knownFiles?: ReadonlySet<string>,
): string | null {
  if (!specifier.startsWith('.') && !specifier.startsWith('/')) return null;

  const baseTarget = specifier.startsWith('/')
    ? resolve(miniprogramDir, specifier.slice(1))
    : resolve(dirname(importerPath), specifier);

  const candidates = /\.[cm]?[jt]s$/i.test(baseTarget)
    ? [baseTarget]
    : [`${baseTarget}.ts`, resolve(baseTarget, 'index.ts'), baseTarget];

  for (const candidate of candidates) {
    const rel = toPosixPath(relative(miniprogramDir, candidate));
    if (knownFiles ? knownFiles.has(rel) : isExistingFile(candidate)) {
      return rel;
    }
  }

  return toPosixPath(relative(miniprogramDir, candidates[0]!));
}

export function findIgnoredStaticImports(
  miniprogramDir: string,
  files: ReadonlyMap<string, string>,
  ignoreRules: readonly IgnoreEntry[],
): StaticImportViolation[] {
  const knownFiles = new Set(files.keys());
  const violations: StaticImportViolation[] = [];

  for (const [relPath, source] of files) {
    if (matchingIgnoreRule(relPath, ignoreRules) !== null) continue;
    const importerPath = resolve(miniprogramDir, relPath);
    for (const specifier of collectStaticModuleSpecifiers(importerPath, source)) {
      const target = resolveImportTarget(miniprogramDir, importerPath, specifier, knownFiles);
      if (target === null || target.startsWith('..')) continue;
      const rule = matchingIgnoreRule(target, ignoreRules);
      if (rule !== null) {
        violations.push({ importer: relPath, specifier, target, rule });
      }
    }
  }

  return violations;
}

test('no non-ignored miniprogram module statically imports a file or folder listed in packOptions.ignore', () => {
  const config = JSON.parse(readFileSync(resolve(appRoot, 'project.config.json'), 'utf8')) as ProjectConfig;
  const ignoreRules = config.packOptions?.ignore ?? [];
  assert.ok(ignoreRules.length > 0, 'project.config.json should list packOptions.ignore rules');

  for (const rule of ignoreRules) {
    assert.ok(
      rule.type === 'file' || rule.type === 'folder',
      `unsupported packOptions.ignore type ${JSON.stringify(rule.type)} for ${rule.value}`,
    );
  }

  const miniprogramDir = resolve(appRoot, config.miniprogramRoot ?? 'miniprogram');
  const files = new Map<string, string>();
  for (const filePath of collectTsFiles(miniprogramDir)) {
    const relPath = toPosixPath(relative(miniprogramDir, filePath));
    files.set(relPath, readFileSync(filePath, 'utf8'));
  }

  const violations = findIgnoredStaticImports(miniprogramDir, files, ignoreRules);
  assert.deepEqual(
    violations,
    [],
    violations
      .map((item) => `${item.importer} imports ${item.specifier} (${item.target}), which is excluded by packOptions.ignore (${item.rule.type}: ${item.rule.value})`)
      .join('\n'),
  );
});

test('packOptions static-import check fails on regressions like active pages importing ignored lib or folder modules', () => {
  const miniprogramDir = resolve(appRoot, 'miniprogram');
  const syntheticFiles = new Map<string, string>([
    ['pages/me/index.ts', "import { clearAssistantChoice } from '../../lib/assistant-choice';\nimport { ask } from '../../pages/assistant/index';\nexport { clearAssistantChoice, ask };\n"],
    ['lib/assistant-choice.ts', 'export const clearAssistantChoice = () => {};\n'],
    ['pages/assistant/index.ts', 'export const ask = () => {};\n'],
  ]);
  const ignoreRules: readonly IgnoreEntry[] = [
    { type: 'file', value: 'lib/assistant-choice.ts' },
    { type: 'folder', value: 'pages/assistant' },
  ];

  const violations = findIgnoredStaticImports(miniprogramDir, syntheticFiles, ignoreRules);
  assert.deepEqual(
    violations.map((item) => [item.importer, item.target]),
    [
      ['pages/me/index.ts', 'lib/assistant-choice.ts'],
      ['pages/me/index.ts', 'pages/assistant/index.ts'],
    ],
  );
});
