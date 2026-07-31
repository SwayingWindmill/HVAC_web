import { readFile, readdir } from 'node:fs/promises';
import { dirname, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

import ts from 'typescript';

export const OPERATIONS_AGENT_SERVICE_PACKAGE_NAME = '@hvac/operations-agent-service';

export const OPERATIONS_AGENT_SERVICE_MODULES = Object.freeze([
  'domain',
  'application',
  'runtime-langgraph',
  'model',
  'tools',
  'persistence',
  'transport-ag-ui',
  'scheduling',
  'observability',
  'bootstrap',
]);

const adapterModules = OPERATIONS_AGENT_SERVICE_MODULES.filter((name) => (
  !['domain', 'application', 'bootstrap'].includes(name)
));

const allowedInternalDependencies = Object.freeze({
  root: ['application', 'bootstrap'],
  domain: [],
  application: ['domain'],
  bootstrap: OPERATIONS_AGENT_SERVICE_MODULES.filter((name) => name !== 'bootstrap'),
  ...Object.fromEntries(adapterModules.map((name) => [name, ['application', 'domain']])),
});

const externalImportForbiddenModules = new Set(['domain', 'application']);
const toPortablePath = (value) => value.split(sep).join('/');
const removeSourceExtension = (value) => value.replace(/\.(?:[cm]?ts|[cm]?js)$/u, '');

const listTypeScriptFiles = async (directory) => {
  const files = [];
  let entries;
  try {
    entries = await readdir(directory, { withFileTypes: true });
  } catch (cause) {
    if (cause && typeof cause === 'object' && 'code' in cause && cause.code === 'ENOENT') return files;
    throw cause;
  }

  for (const entry of entries) {
    const absolutePath = resolve(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...await listTypeScriptFiles(absolutePath));
    } else if (entry.isFile() && /\.[cm]?ts$/u.test(entry.name) && !entry.name.endsWith('.d.ts')) {
      files.push(absolutePath);
    }
  }
  return files.sort();
};

const moduleForFile = (srcRoot, absolutePath) => {
  const path = toPortablePath(relative(srcRoot, absolutePath));
  if (path === 'index.ts') return 'root';
  const [moduleName] = path.split('/');
  return OPERATIONS_AGENT_SERVICE_MODULES.includes(moduleName) ? moduleName : null;
};

const importSpecifiers = (sourceText, fileName) => {
  const sourceFile = ts.createSourceFile(fileName, sourceText, ts.ScriptTarget.Latest, true);
  const imports = [];

  const visit = (node) => {
    if ((ts.isImportDeclaration(node) || ts.isExportDeclaration(node))
      && node.moduleSpecifier
      && ts.isStringLiteralLike(node.moduleSpecifier)) {
      imports.push(node.moduleSpecifier.text);
    } else if (ts.isCallExpression(node)
      && node.expression.kind === ts.SyntaxKind.ImportKeyword
      && node.arguments.length === 1
      && ts.isStringLiteralLike(node.arguments[0])) {
      imports.push(node.arguments[0].text);
    }
    ts.forEachChild(node, visit);
  };

  visit(sourceFile);
  return imports;
};

const expectedModuleEntry = (srcRoot, moduleName) => removeSourceExtension(
  resolve(srcRoot, moduleName, 'index.ts'),
);

const inspectImport = ({ srcRoot, sourceFile, sourceModule, specifier }) => {
  if (specifier === OPERATIONS_AGENT_SERVICE_PACKAGE_NAME
    || specifier.startsWith(`${OPERATIONS_AGENT_SERVICE_PACKAGE_NAME}/`)) {
    return [{
      code: 'SELF_PACKAGE_IMPORT_FORBIDDEN',
      file: toPortablePath(relative(srcRoot, sourceFile)),
      import: specifier,
      message: 'Service modules must use relative imports through another module index.ts entry.',
    }];
  }
  if (!specifier.startsWith('.')) {
    return externalImportForbiddenModules.has(sourceModule)
      ? [{
        code: 'EXTERNAL_IMPORT_FORBIDDEN',
        file: toPortablePath(relative(srcRoot, sourceFile)),
        import: specifier,
        message: `${sourceModule} cannot import external package ${specifier}.`,
      }]
      : [];
  }

  const resolvedTarget = removeSourceExtension(resolve(dirname(sourceFile), specifier));
  const relativeTarget = toPortablePath(relative(srcRoot, resolvedTarget));
  if (relativeTarget === '..' || relativeTarget.startsWith('../')) {
    return [{
      code: 'RELATIVE_IMPORT_OUTSIDE_SOURCE',
      file: toPortablePath(relative(srcRoot, sourceFile)),
      import: specifier,
      message: 'Service source imports must remain inside the service src directory.',
    }];
  }

  const targetModule = relativeTarget === 'index' ? 'root' : relativeTarget.split('/')[0];
  if (!OPERATIONS_AGENT_SERVICE_MODULES.includes(targetModule)) return [];
  if (targetModule === sourceModule) return [];

  const errors = [];
  if (!allowedInternalDependencies[sourceModule]?.includes(targetModule)) {
    errors.push({
      code: 'MODULE_DEPENDENCY_FORBIDDEN',
      file: toPortablePath(relative(srcRoot, sourceFile)),
      import: specifier,
      message: `${sourceModule} cannot depend on ${targetModule}.`,
    });
  }
  if (resolvedTarget !== expectedModuleEntry(srcRoot, targetModule)) {
    errors.push({
      code: 'CROSS_MODULE_INTERNAL_IMPORT',
      file: toPortablePath(relative(srcRoot, sourceFile)),
      import: specifier,
      message: `Cross-module imports must target ${targetModule}/index.ts.`,
    });
  }
  return errors;
};

const expectedExports = Object.freeze({
  '.': {
    types: './dist/index.d.ts',
    import: './dist/index.js',
  },
});

const inspectPackageExports = async (serviceRoot) => {
  let packageJson;
  try {
    packageJson = JSON.parse(await readFile(resolve(serviceRoot, 'package.json'), 'utf8'));
  } catch (cause) {
    return [{
      code: 'PACKAGE_MANIFEST_INVALID',
      file: 'package.json',
      import: null,
      message: cause instanceof Error ? cause.message : String(cause),
    }];
  }

  const errors = [];
  for (const [exportName, expected] of Object.entries(expectedExports)) {
    const actual = packageJson.exports?.[exportName];
    if (actual?.types !== expected.types || actual?.import !== expected.import) {
      errors.push({
        code: 'PACKAGE_EXPORT_MISSING',
        file: 'package.json',
        import: exportName,
        message: `${exportName} must export ${expected.types} and ${expected.import}.`,
      });
    }
  }
  for (const exportName of Object.keys(packageJson.exports ?? {})) {
    if (!(exportName in expectedExports)) {
      errors.push({
        code: 'PACKAGE_EXPORT_FORBIDDEN',
        file: 'package.json',
        import: exportName,
        message: 'Only the service root may be exported outside the modular monolith.',
      });
    }
  }
  return errors;
};

export const inspectOperationsAgentServiceBoundaries = async (serviceRoot, {
  requireCompleteModuleSet = true,
  requirePackageExports = true,
} = {}) => {
  const absoluteRoot = resolve(serviceRoot);
  const srcRoot = resolve(absoluteRoot, 'src');
  const files = await listTypeScriptFiles(srcRoot);
  const errors = [];
  const discoveredModules = new Set();

  for (const sourceFile of files) {
    const sourceModule = moduleForFile(srcRoot, sourceFile);
    if (!sourceModule) {
      errors.push({
        code: 'SOURCE_OUTSIDE_MODULE',
        file: toPortablePath(relative(srcRoot, sourceFile)),
        import: null,
        message: 'Every source file must belong to the root entry or an accepted module.',
      });
      continue;
    }
    if (sourceModule !== 'root') discoveredModules.add(sourceModule);

    const sourceText = await readFile(sourceFile, 'utf8');
    for (const specifier of importSpecifiers(sourceText, sourceFile)) {
      errors.push(...inspectImport({ srcRoot, sourceFile, sourceModule, specifier }));
    }
  }

  if (requireCompleteModuleSet) {
    for (const moduleName of OPERATIONS_AGENT_SERVICE_MODULES) {
      if (!files.some((file) => removeSourceExtension(file) === expectedModuleEntry(srcRoot, moduleName))) {
        errors.push({
          code: 'MODULE_ENTRY_MISSING',
          file: `${moduleName}/index.ts`,
          import: null,
          message: `Missing public entry for ${moduleName}.`,
        });
      }
    }
    if (!files.some((file) => removeSourceExtension(file) === removeSourceExtension(resolve(srcRoot, 'index.ts')))) {
      errors.push({
        code: 'ROOT_ENTRY_MISSING',
        file: 'index.ts',
        import: null,
        message: 'Missing service root public entry.',
      });
    }
  }

  if (requirePackageExports) errors.push(...await inspectPackageExports(absoluteRoot));

  return {
    valid: errors.length === 0,
    modules: OPERATIONS_AGENT_SERVICE_MODULES.filter((name) => discoveredModules.has(name)),
    sourceFiles: files.length,
    errors,
  };
};

const scriptPath = fileURLToPath(import.meta.url);
const repositoryRoot = resolve(dirname(scriptPath), '..');
const invokedPath = process.argv[1] ? resolve(process.argv[1]) : null;
if (invokedPath === scriptPath) {
  const serviceRoot = process.argv[2] ?? resolve(repositoryRoot, 'services/operations-agent-service');
  const result = await inspectOperationsAgentServiceBoundaries(serviceRoot);
  if (result.valid) {
    console.log(`Operations Agent service boundaries passed: ${result.modules.length} modules, ${result.sourceFiles} source files.`);
  } else {
    console.error('Operations Agent service boundaries failed.');
    for (const error of result.errors) {
      console.error(`- [${error.code}] ${error.file}${error.import ? ` -> ${error.import}` : ''}: ${error.message}`);
    }
    process.exitCode = 1;
  }
}
