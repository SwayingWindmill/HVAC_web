import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { cpSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { checkClickHouseMigrations } from './check-clickhouse-migrations.ts';
import {
  type DeploymentTierContract,
  resolveDeploymentTier,
  resolvePhase1ClickHouseNativeAddr,
} from './phase1-deployment-tier.ts';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');

function withSandboxRepo(mutate: (sandboxRoot: string) => void): () => void {
  const sandbox = mkdtempSync(join(tmpdir(), 'ch-migrations-test-'));
  try {
    for (const rel of [
      'deploy/platform/phase1/clickhouse-migrations',
      'deploy/platform/phase1/product-release.v1.json',
      'deploy/platform/phase1/runtime-inventory.v1.json',
      'deploy/platform/phase1/compose.yaml',
      'deploy/platform/phase1/wsl.override.yaml',
      'infra/telemetry/clickhouse/init',
    ]) {
      cpSync(join(repoRoot, rel), join(sandbox, rel), { recursive: true });
    }
    mutate(sandbox);
  } catch (err) {
    rmSync(sandbox, { recursive: true, force: true });
    throw err;
  }
  return () => {
    try {
      checkClickHouseMigrations(sandbox);
    } finally {
      rmSync(sandbox, { recursive: true, force: true });
    }
  };
}

test('repository ClickHouse migration contract passes', () => {
  checkClickHouseMigrations(repoRoot);
});

test('unregistered ClickHouse init SQL file fails closed', () => {
  const sandbox = mkdtempSync(join(tmpdir(), 'ch-migrations-unregistered-'));
  try {
    for (const rel of [
      'deploy/platform/phase1/clickhouse-migrations',
      'deploy/platform/phase1/product-release.v1.json',
      'deploy/platform/phase1/runtime-inventory.v1.json',
      'deploy/platform/phase1/compose.yaml',
      'deploy/platform/phase1/wsl.override.yaml',
      'infra/telemetry/clickhouse/init',
    ]) {
      cpSync(join(repoRoot, rel), join(sandbox, rel), { recursive: true });
    }
    writeFileSync(join(sandbox, 'infra/telemetry/clickhouse/init/099-untracked.sql'), 'SELECT 1;\n', 'utf8');
    assert.throws(() => checkClickHouseMigrations(sandbox), /1:1/);
  } finally {
    rmSync(sandbox, { recursive: true, force: true });
  }
});

test('drifted product-release clickhouseMigrationManifestSha256 fails closed', () => {
  const run = withSandboxRepo((sandbox) => {
    const releasePath = join(sandbox, 'deploy/platform/phase1/product-release.v1.json');
    const release = JSON.parse(readFileSync(releasePath, 'utf8')) as {
      schemaContract: { clickhouseMigrationManifestSha256: string };
    };
    release.schemaContract.clickhouseMigrationManifestSha256 = '0'.repeat(64);
    writeFileSync(releasePath, `${JSON.stringify(release, null, 2)}\n`, 'utf8');
  });
  assert.throws(run, /clickhouseMigrationManifestSha256/);
});

test('re-enabling ClickHouse /docker-entrypoint-initdb.d mount in wsl.override.yaml fails closed', () => {
  const run = withSandboxRepo((sandbox) => {
    const overridePath = join(sandbox, 'deploy/platform/phase1/wsl.override.yaml');
    const content = readFileSync(overridePath, 'utf8').replace(
      '      - phase1_wsl_clickhouse_data:/var/lib/clickhouse\n',
      '      - phase1_wsl_clickhouse_data:/var/lib/clickhouse\n      - ../../../infra/telemetry/clickhouse/init:/docker-entrypoint-initdb.d:ro\n',
    );
    writeFileSync(overridePath, content, 'utf8');
  });
  assert.throws(run, /\/docker-entrypoint-initdb\.d/);
});

test('dropping phase1-clickhouse-schema-preflight dependency on energy-api fails closed', () => {
  const run = withSandboxRepo((sandbox) => {
    const composePath = join(sandbox, 'deploy/platform/phase1/compose.yaml');
    const original = readFileSync(composePath, 'utf8');
    const mutated = original.replace(
      '      phase1-clickhouse-schema-preflight:\n        condition: service_completed_successfully\n',
      '',
    );
    writeFileSync(composePath, mutated, 'utf8');
  });
  assert.throws(run, /energy-api must depend on phase1-clickhouse-schema-preflight/);
});

test('missing ClickHouse credential template user fails closed', () => {
  const run = withSandboxRepo((sandbox) => {
    const credPath = join(sandbox, 'deploy/platform/phase1/clickhouse-migrations/clickhouse-role-credentials.sql.example');
    const lines = readFileSync(credPath, 'utf8').split('\n').filter((line) => !line.includes('phase1_schema_reader'));
    writeFileSync(credPath, lines.join('\n'), 'utf8');
  });
  assert.throws(run, /missing ClickHouse role/);
});

test('local plaintext password statement in init SQL fails closed', () => {
  const run = withSandboxRepo((sandbox) => {
    const sqlPath = join(sandbox, 'infra/telemetry/clickhouse/init/010-telemetry-history-writer.sql');
    writeFileSync(
      sqlPath,
      `${readFileSync(sqlPath, 'utf8')}\nCREATE USER IF NOT EXISTS bad_user IDENTIFIED WITH plaintext_password BY 'dev';\n`,
      'utf8',
    );
  });
  assert.throws(run, /local password statements/);
});

function updateManifestInSandbox(
  sandbox: string,
  mutate: (manifest: { equivalentSha256?: Record<string, string[]>; [key: string]: unknown }) => void,
): void {
  const manifestPath = join(sandbox, 'deploy/platform/phase1/clickhouse-migrations/clickhouse-manifest.v1.json');
  const manifest = JSON.parse(readFileSync(manifestPath, 'utf8')) as {
    equivalentSha256?: Record<string, string[]>;
    [key: string]: unknown;
  };
  mutate(manifest);
  const nextRaw = `${JSON.stringify(manifest, null, 2)}\n`;
  writeFileSync(manifestPath, nextRaw, 'utf8');

  const releasePath = join(sandbox, 'deploy/platform/phase1/product-release.v1.json');
  const release = JSON.parse(readFileSync(releasePath, 'utf8')) as {
    schemaContract: { clickhouseMigrationManifestSha256: string };
  };
  release.schemaContract.clickhouseMigrationManifestSha256 = createHash('sha256').update(nextRaw).digest('hex');
  writeFileSync(releasePath, `${JSON.stringify(release, null, 2)}\n`, 'utf8');
}

test('drifted clickhouse-migration-equivalent-sha256.tsv fails closed', () => {
  const run = withSandboxRepo((sandbox) => {
    const tsvPath = join(sandbox, 'deploy/platform/phase1/clickhouse-migrations/clickhouse-migration-equivalent-sha256.tsv');
    writeFileSync(tsvPath, 'infra/telemetry/clickhouse/init/025-clickhouse-complete-governance.sql|0000\n', 'utf8');
  });
  assert.throws(run, /clickhouse-migration-equivalent-sha256\.tsv/);
});

test('equivalentSha256 rejects unregistered migration, invalid sha256, and current file sha256', () => {
  const runUnregistered = withSandboxRepo((sandbox) => {
    updateManifestInSandbox(sandbox, (manifest) => {
      manifest.equivalentSha256 = {
        'infra/telemetry/clickhouse/init/999-missing.sql': ['a'.repeat(64)],
      };
    });
  });
  assert.throws(runUnregistered, /unregistered migration/);

  const runInvalidHash = withSandboxRepo((sandbox) => {
    updateManifestInSandbox(sandbox, (manifest) => {
      manifest.equivalentSha256 = {
        'infra/telemetry/clickhouse/init/025-clickhouse-complete-governance.sql': ['not-a-sha256'],
      };
    });
  });
  assert.throws(runInvalidHash, /invalid sha256/);

  const runCurrentHash = withSandboxRepo((sandbox) => {
    const current025Sha = createHash('sha256')
      .update(readFileSync(join(sandbox, 'infra/telemetry/clickhouse/init/025-clickhouse-complete-governance.sql')))
      .digest('hex');
    updateManifestInSandbox(sandbox, (manifest) => {
      manifest.equivalentSha256 = {
        'infra/telemetry/clickhouse/init/025-clickhouse-complete-governance.sql': [current025Sha],
      };
    });
  });
  assert.throws(runCurrentHash, /must not duplicate current sha256/);
});

test('resolvePhase1ClickHouseNativeAddr defaults clickhouse:9000 only in local mode and supports opt-out', () => {
  assert.equal(resolvePhase1ClickHouseNativeAddr({ localClickHouse: true }), 'clickhouse:9000');
  assert.equal(
    resolvePhase1ClickHouseNativeAddr({
      localClickHouse: true,
      runtimeEnvironment: { PHASE1_CLICKHOUSE_NATIVE_ADDR: '', TELEMETRY_CLICKHOUSE_NATIVE_ADDR: '' },
    }),
    'clickhouse:9000',
  );
  assert.equal(
    resolvePhase1ClickHouseNativeAddr({
      localClickHouse: true,
      runtimeEnvironment: { PHASE1_CLICKHOUSE_NATIVE_ADDR: 'custom-ch:9440' },
    }),
    'custom-ch:9440',
  );
  assert.equal(
    resolvePhase1ClickHouseNativeAddr({
      localClickHouse: true,
      runtimeEnvironment: { TELEMETRY_CLICKHOUSE_NATIVE_ADDR: 'telemetry-ch:9440' },
    }),
    'telemetry-ch:9440',
  );
  for (const sentinel of ['off', 'OFF', 'disabled', 'false', 'http']) {
    assert.equal(
      resolvePhase1ClickHouseNativeAddr({
        localClickHouse: true,
        runtimeEnvironment: {
          PHASE1_CLICKHOUSE_NATIVE_ADDR: sentinel,
          TELEMETRY_CLICKHOUSE_NATIVE_ADDR: 'clickhouse:9000',
        },
      }),
      '',
    );
    assert.equal(
      resolvePhase1ClickHouseNativeAddr({
        localClickHouse: true,
        runtimeEnvironment: { TELEMETRY_CLICKHOUSE_NATIVE_ADDR: sentinel },
      }),
      '',
    );
  }
  assert.equal(resolvePhase1ClickHouseNativeAddr({ localClickHouse: false }), '');
  assert.equal(
    resolvePhase1ClickHouseNativeAddr({
      localClickHouse: false,
      runtimeEnvironment: { PHASE1_CLICKHOUSE_NATIVE_ADDR: 'ext-ch.internal:9440' },
    }),
    'ext-ch.internal:9440',
  );
});

test('resolveDeploymentTier enables Native TCP for local-clickhouse and compose plumbs CA and password', () => {
  const contract = JSON.parse(
    readFileSync(join(repoRoot, 'deploy/platform/phase1/deployment-tiers.v1.json'), 'utf8'),
  ) as DeploymentTierContract;
  const compose = readFileSync(join(repoRoot, 'deploy/platform/phase1/compose.yaml'), 'utf8');

  const localResolved = resolveDeploymentTier({
    contract,
    compose,
    tierId: 'single-lite',
    environment: 'production',
    runtimeEnvironment: { TELEMETRY_CLICKHOUSE_NATIVE_ADDR: '' },
    additionalProfiles: ['local-postgres', 'local-clickhouse', 'local-redis'],
  });
  assert.equal(localResolved.environment.PHASE1_CLICKHOUSE_NATIVE_ADDR, 'clickhouse:9000');
  assert.equal(localResolved.environment.TELEMETRY_CLICKHOUSE_NATIVE_ADDR, 'clickhouse:9000');

  const optedOut = resolveDeploymentTier({
    contract,
    compose,
    tierId: 'single-lite',
    environment: 'production',
    runtimeEnvironment: { PHASE1_CLICKHOUSE_NATIVE_ADDR: 'off' },
    additionalProfiles: ['local-postgres', 'local-clickhouse', 'local-redis'],
  });
  assert.equal(optedOut.environment.PHASE1_CLICKHOUSE_NATIVE_ADDR, '');
  assert.equal(optedOut.environment.TELEMETRY_CLICKHOUSE_NATIVE_ADDR, '');

  const externalResolved = resolveDeploymentTier({
    contract,
    compose,
    tierId: 'single-lite',
    environment: 'production',
    additionalProfiles: ['local-postgres', 'local-redis'],
  });
  assert.equal(externalResolved.environment.PHASE1_CLICKHOUSE_NATIVE_ADDR, '');
  assert.equal(externalResolved.environment.TELEMETRY_CLICKHOUSE_NATIVE_ADDR, '');

  assert.match(compose, /TELEMETRY_CLICKHOUSE_PASSWORD:\s*\$\{TELEMETRY_CLICKHOUSE_PASSWORD:-\}/);
  assert.match(compose, /TELEMETRY_CLICKHOUSE_CA:\s*\$\{PHASE1_CLICKHOUSE_CA:-\$\{TELEMETRY_CLICKHOUSE_CA:-\}\}/);
  assert.match(compose, /TELEMETRY_CLICKHOUSE_NATIVE_ADDR:\s*\$\{PHASE1_CLICKHOUSE_NATIVE_ADDR:-\$\{TELEMETRY_CLICKHOUSE_NATIVE_ADDR:-\}\}/);
});
