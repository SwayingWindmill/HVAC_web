import fs from 'node:fs';
import path from 'node:path';
import ts from 'typescript';

const CODE_EXTENSIONS = ['.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs'];

function normalizePath(value) {
  return value.replace(/\\/g, '/');
}

function parseTsConfig(tsconfigPath) {
  const configFile = ts.readConfigFile(tsconfigPath, ts.sys.readFile);
  if (configFile.error) {
    throw new Error(ts.flattenDiagnosticMessageText(configFile.error.messageText, '\n'));
  }
  return ts.parseJsonConfigFileContent(configFile.config, ts.sys, path.dirname(tsconfigPath)).options;
}

function sourceFileKind(filename) {
  if (filename.endsWith('.tsx')) return ts.ScriptKind.TSX;
  if (filename.endsWith('.jsx')) return ts.ScriptKind.JSX;
  if (filename.endsWith('.js') || filename.endsWith('.mjs') || filename.endsWith('.cjs')) return ts.ScriptKind.JS;
  return ts.ScriptKind.TS;
}

function collectModuleSpecifiers(sourceFile) {
  const specifiers = [];
  const unknownDynamicImports = [];

  function visit(node) {
    if ((ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) && node.moduleSpecifier && ts.isStringLiteral(node.moduleSpecifier)) {
      specifiers.push({ value: node.moduleSpecifier.text, dynamic: false });
    }
    if (ts.isCallExpression(node) && node.expression.kind === ts.SyntaxKind.ImportKeyword) {
      const [argument] = node.arguments;
      if (argument && ts.isStringLiteralLike(argument)) {
        specifiers.push({ value: argument.text, dynamic: true });
      } else {
        unknownDynamicImports.push(sourceFile.fileName);
      }
    }
    if (
      ts.isCallExpression(node)
      && ts.isPropertyAccessExpression(node.expression)
      && (node.expression.name.text === 'glob' || node.expression.name.text === 'globEager')
      && ts.isMetaProperty(node.expression.expression)
      && node.expression.expression.keywordToken === ts.SyntaxKind.ImportKeyword
    ) {
      unknownDynamicImports.push(sourceFile.fileName);
    }
    ts.forEachChild(node, visit);
  }

  visit(sourceFile);
  return { specifiers, unknownDynamicImports };
}

function resolveStyleImport(specifier, containingFile, sourceRoot) {
  if (!specifier.endsWith('.css')) return null;
  if (specifier.startsWith('@/')) return path.join(sourceRoot, specifier.slice(2));
  if (specifier.startsWith('.')) return path.resolve(path.dirname(containingFile), specifier);
  return null;
}

function resolveCodeImport(specifier, containingFile, compilerOptions, moduleResolutionHost) {
  const resolved = ts.resolveModuleName(specifier, containingFile, compilerOptions, moduleResolutionHost).resolvedModule?.resolvedFileName;
  if (!resolved) return null;
  const normalized = resolved.replace(/\.d\.ts$/, '.ts');
  if (fs.existsSync(normalized)) return normalized;
  return resolved;
}

export function collectRealDependencyGraph({ entry, tsconfig, sourceRoot }) {
  const absoluteEntry = path.resolve(entry);
  const absoluteSourceRoot = path.resolve(sourceRoot);
  const compilerOptions = parseTsConfig(path.resolve(tsconfig));
  const moduleResolutionHost = {
    fileExists: ts.sys.fileExists,
    readFile: ts.sys.readFile,
    realpath: ts.sys.realpath,
    directoryExists: ts.sys.directoryExists,
    getCurrentDirectory: ts.sys.getCurrentDirectory,
    getDirectories: ts.sys.getDirectories,
  };
  const visited = new Set();
  const edges = [];
  const unresolved = [];
  const unknownDynamicImports = [];

  function visit(filename) {
    const absolute = path.resolve(filename);
    if (visited.has(absolute)) return;
    visited.add(absolute);

    if (!CODE_EXTENSIONS.some((extension) => absolute.endsWith(extension))) return;
    const text = fs.readFileSync(absolute, 'utf8');
    const sourceFile = ts.createSourceFile(absolute, text, ts.ScriptTarget.Latest, true, sourceFileKind(absolute));
    const discovered = collectModuleSpecifiers(sourceFile);
    unknownDynamicImports.push(...discovered.unknownDynamicImports);

    for (const moduleSpecifier of discovered.specifiers) {
      const stylePath = resolveStyleImport(moduleSpecifier.value, absolute, absoluteSourceRoot);
      if (stylePath) {
        if (!fs.existsSync(stylePath)) {
          unresolved.push({ from: absolute, specifier: moduleSpecifier.value, dynamic: moduleSpecifier.dynamic });
          continue;
        }
        edges.push({ from: absolute, to: stylePath, specifier: moduleSpecifier.value, dynamic: moduleSpecifier.dynamic });
        continue;
      }

      const resolved = resolveCodeImport(moduleSpecifier.value, absolute, compilerOptions, moduleResolutionHost);
      if (!resolved) {
        if (moduleSpecifier.value.startsWith('.') || moduleSpecifier.value.startsWith('@/')) {
          unresolved.push({ from: absolute, specifier: moduleSpecifier.value, dynamic: moduleSpecifier.dynamic });
        }
        continue;
      }

      edges.push({ from: absolute, to: resolved, specifier: moduleSpecifier.value, dynamic: moduleSpecifier.dynamic });
      if (path.resolve(resolved).startsWith(absoluteSourceRoot + path.sep)) visit(resolved);
    }
  }

  visit(absoluteEntry);

  return {
    entry: absoluteEntry,
    sourceRoot: absoluteSourceRoot,
    files: [...visited].sort(),
    edges: edges.sort((left, right) => `${left.from}:${left.specifier}`.localeCompare(`${right.from}:${right.specifier}`)),
    unresolved,
    unknownDynamicImports: [...new Set(unknownDynamicImports)].sort(),
  };
}

export const REAL_FORBIDDEN_PATH_RULES = [
  { id: 'demo-entry', pattern: /\/src\/demo\// },
  { id: 'mock-data', pattern: /\/src\/mock\// },
  { id: 'mock-api', pattern: /\/src\/api\/mock\.(?:ts|tsx)$/ },
  { id: 'mock-ai', pattern: /\/src\/ai\// },
  { id: 'demo-store', pattern: /\/src\/store\// },
  { id: 'local-role-simulation', pattern: /\/src\/auth\/(?:permissions|RequirePermission)\.(?:ts|tsx)$/ },
  { id: 'demo-dashboard', pattern: /\/src\/pages\/Dashboard\// },
  { id: 'demo-bigscreen', pattern: /\/src\/pages\/BigScreen\// },
  { id: 'demo-energy', pattern: /\/src\/pages\/Energy\// },
  { id: 'demo-cost', pattern: /\/src\/pages\/Cost\// },
  { id: 'demo-alarm', pattern: /\/src\/pages\/Alarms\// },
  { id: 'demo-fdd', pattern: /\/src\/pages\/Fdd\// },
  { id: 'demo-optimize', pattern: /\/src\/pages\/Optimize\// },
  { id: 'demo-ai-page', pattern: /\/src\/pages\/Ai\// },
  { id: 'mode-switch-page', pattern: /\/src\/pages\/(?:Assets|System)\/index\.tsx$/ },
];

export function evaluateRealDependencyGraph(graph, rules = REAL_FORBIDDEN_PATH_RULES) {
  const violations = [];
  const reachableLocalPaths = new Set([
    ...graph.files,
    ...graph.edges
      .map((edge) => edge.to)
      .filter((filename) => path.resolve(filename).startsWith(graph.sourceRoot + path.sep)),
  ]);
  for (const filename of reachableLocalPaths) {
    const normalized = normalizePath(filename);
    for (const rule of rules) {
      if (rule.pattern.test(normalized)) violations.push({ rule: rule.id, file: normalized });
    }
  }
  for (const unresolved of graph.unresolved) {
    violations.push({ rule: 'unresolved-local-import', file: normalizePath(unresolved.from), specifier: unresolved.specifier });
  }
  for (const filename of graph.unknownDynamicImports) {
    violations.push({ rule: 'non-literal-dynamic-import', file: normalizePath(filename) });
  }
  return violations;
}

export function relativeGraphReport(graph, workspaceRoot, violations) {
  const relative = (value) => normalizePath(path.relative(workspaceRoot, value));
  return {
    schemaVersion: 1,
    entry: relative(graph.entry),
    sourceRoot: relative(graph.sourceRoot),
    files: graph.files.map(relative),
    edges: graph.edges.map((edge) => ({ ...edge, from: relative(edge.from), to: relative(edge.to) })),
    unresolved: graph.unresolved.map((item) => ({ ...item, from: relative(item.from) })),
    unknownDynamicImports: graph.unknownDynamicImports.map(relative),
    violations,
    passed: violations.length === 0,
  };
}
