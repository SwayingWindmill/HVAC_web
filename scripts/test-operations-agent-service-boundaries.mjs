import assert from 'node:assert/strict';
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import {
  OPERATIONS_AGENT_SERVICE_MODULES,
  OPERATIONS_AGENT_SERVICE_PACKAGE_NAME,
  inspectOperationsAgentServiceBoundaries,
} from './check-operations-agent-service-boundaries.mjs';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repositoryServiceRoot = resolve(repositoryRoot, 'services/operations-agent-service');

const withTemporaryService = async (run) => {
  const root = await mkdtemp(join(tmpdir(), 'operations-agent-service-'));
  try {
    await mkdir(join(root, 'src'), { recursive: true });
    return await run(root);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
};

const writeSource = async (root, path, source) => {
  const absolutePath = join(root, 'src', path);
  await mkdir(resolve(absolutePath, '..'), { recursive: true });
  await writeFile(absolutePath, source, 'utf8');
};

test('repository Operations Agent service exposes the accepted module skeleton', async () => {
  const result = await inspectOperationsAgentServiceBoundaries(repositoryServiceRoot);

  assert.equal(result.valid, true, JSON.stringify(result.errors, null, 2));
  assert.deepEqual(result.modules, OPERATIONS_AGENT_SERVICE_MODULES);
  assert.equal(result.sourceFiles > 0, true);
});

test('domain and application cannot import adapters or external packages', async () => {
  for (const moduleName of ['domain', 'application']) {
    await withTemporaryService(async (root) => {
      await writeSource(root, `${moduleName}/index.ts`, [
        "import { persistenceModule } from '../persistence/index.js';",
        "import { z } from 'zod';",
        `export const moduleValue = '${moduleName}:' + persistenceModule;`,
        'export const schema = z.string();',
        '',
      ].join('\n'));
      await writeSource(root, 'persistence/index.ts', "export const persistenceModule = 'persistence';\n");

      const result = await inspectOperationsAgentServiceBoundaries(root, {
        requireCompleteModuleSet: false,
        requirePackageExports: false,
      });
      const codes = new Set(result.errors.map(({ code }) => code));

      assert.equal(result.valid, false);
      assert(codes.has('MODULE_DEPENDENCY_FORBIDDEN'));
      assert(codes.has('EXTERNAL_IMPORT_FORBIDDEN'));
    });
  }
});

test('cross-module imports must use the target public index', async () => {
  await withTemporaryService(async (root) => {
    await writeSource(root, 'application/index.ts', "export const applicationModule = 'application';\n");
    await writeSource(root, 'application/internal/private.ts', "export const privateValue = 1;\n");
    await writeSource(root, 'tools/index.ts', [
      "import { privateValue } from '../application/internal/private.js';",
      'export const toolsModule = privateValue;',
      '',
    ].join('\n'));

    const result = await inspectOperationsAgentServiceBoundaries(root, {
      requireCompleteModuleSet: false,
      requirePackageExports: false,
    });

    assert.equal(result.valid, false);
    assert(result.errors.some(({ code }) => code === 'CROSS_MODULE_INTERNAL_IMPORT'));
  });
});

test('application may depend on domain', async () => {
  await withTemporaryService(async (root) => {
    await writeSource(root, 'domain/index.ts', "export const domainModule = 'domain';\n");
    await writeSource(root, 'application/index.ts', [
      "import { domainModule } from '../domain/index.js';",
      'export const applicationModule = domainModule;',
      '',
    ].join('\n'));

    const result = await inspectOperationsAgentServiceBoundaries(root, {
      requireCompleteModuleSet: false,
      requirePackageExports: false,
    });

    assert.equal(result.valid, true, JSON.stringify(result.errors, null, 2));
  });
});

test('adapters cannot depend on another adapter', async () => {
  await withTemporaryService(async (root) => {
    await writeSource(root, 'model/index.ts', "export const modelModule = 'model';\n");
    await writeSource(root, 'tools/index.ts', [
      "import { modelModule } from '../model/index.js';",
      'export const toolsModule = modelModule;',
      '',
    ].join('\n'));

    const result = await inspectOperationsAgentServiceBoundaries(root, {
      requireCompleteModuleSet: false,
      requirePackageExports: false,
    });

    assert.equal(result.valid, false);
    assert(result.errors.some(({ code }) => code === 'MODULE_DEPENDENCY_FORBIDDEN'));
  });
});

test('service modules cannot bypass boundaries through a package self-import', async () => {
  await withTemporaryService(async (root) => {
    await writeSource(root, 'tools/index.ts', [
      `import { operationsAgentServiceModules } from '${OPERATIONS_AGENT_SERVICE_PACKAGE_NAME}';`,
      'export const toolsModule = operationsAgentServiceModules;',
      '',
    ].join('\n'));

    const result = await inspectOperationsAgentServiceBoundaries(root, {
      requireCompleteModuleSet: false,
      requirePackageExports: false,
    });

    assert.equal(result.valid, false);
    assert(result.errors.some(({ code }) => code === 'SELF_PACKAGE_IMPORT_FORBIDDEN'));
  });
});

test('tools cannot add direct data-store, Cube, ThingsBoard, or Command API paths', async () => {
  await withTemporaryService(async (root) => {
    await writeSource(root, 'tools/index.ts', [
      "export const forbidden = ['http://clickhouse:8123', 'http://cube:4000/load',",
      "  'http://thingsboard:8080', '/internal/v1/commands'];",
      '',
    ].join('\n'));

    const result = await inspectOperationsAgentServiceBoundaries(root, {
      requireCompleteModuleSet: false,
      requirePackageExports: false,
    });

    assert.equal(result.valid, false);
    assert(result.errors.some(({ code }) => code === 'FORBIDDEN_OWNER_BYPASS_PATH'));
  });
});

test('tools may document forbidden owners without creating a bypass path', async () => {
  await withTemporaryService(async (root) => {
    await writeSource(root, 'tools/index.ts', [
      '// Operations Agent tools must never call ClickHouse, Cube, ThingsBoard, or Command APIs.',
      "export const toolsModule = 'tools';",
      '',
    ].join('\n'));

    const result = await inspectOperationsAgentServiceBoundaries(root, {
      requireCompleteModuleSet: false,
      requirePackageExports: false,
    });

    assert.equal(result.valid, true, JSON.stringify(result.errors, null, 2));
  });
});
