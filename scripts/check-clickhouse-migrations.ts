import { createHash } from 'node:crypto';
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import yaml from 'js-yaml';

interface ClickHouseManifest {
  schemaVersion: number;
  product: string;
  policy: {
    fixturesAllowed: boolean;
    testdataAllowed: boolean;
    localPasswordStatementsAllowed: boolean;
  };
  schemaReaderRole: string;
  databases: string[];
  migrations: string[];
  equivalentSha256?: Record<string, string[]>;
}

interface ProductRelease {
  schemaContract?: {
    clickhouseMigrationManifest?: string;
    clickhouseMigrationManifestSha256?: string;
  };
}

interface RuntimeInventory {
  classes?: {
    requiredStartupOneShot?: string[];
    operatorOneShot?: Array<{
      service?: string;
      profile?: string;
    }>;
  };
}

interface ComposeDependency {
  condition?: string;
  required?: boolean;
}

interface ComposeService {
  profiles?: string[];
  restart?: string;
  network_mode?: string;
  entrypoint?: string[];
  environment?: Record<string, string>;
  volumes?: string[];
  read_only?: boolean;
  depends_on?: Record<string, ComposeDependency>;
}

interface ComposeDocument {
  'x-default-env'?: Record<string, string>;
  services?: Record<string, ComposeService>;
}

const EXPECTED_DATABASES = ['telemetry_history', 'analytics'];
const CLICKHOUSE_CONSUMERS = [
  'energy-api',
  'telemetry-worker',
  'metric-worker',
  'cube',
  'forecast-service',
  'optimization-service',
  'fdd-service',
  'mv-service',
];

function sha256File(filePath: string): string {
  return createHash('sha256').update(readFileSync(filePath)).digest('hex');
}

function readUtf8Lf(filePath: string, label: string): string {
  const content = readFileSync(filePath, 'utf8');
  if (content.includes('\r')) {
    throw new Error(`${label} must use LF line endings`);
  }
  return content;
}

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) {
    throw new Error(message);
  }
}

export function checkClickHouseMigrations(repoRoot: string): void {
  const chDir = join(repoRoot, 'deploy/platform/phase1/clickhouse-migrations');
  const manifestPath = join(chDir, 'clickhouse-manifest.v1.json');
  const tsvPath = join(chDir, 'clickhouse-migration-list.tsv');
  const equivalentShaPath = join(chDir, 'clickhouse-migration-equivalent-sha256.tsv');
  const roleCredExamplePath = join(chDir, 'clickhouse-role-credentials.sql.example');
  const runScriptPath = join(chDir, 'run-clickhouse-migrations.sh');
  const verifyScriptPath = join(chDir, 'verify-clickhouse-schema.sh');
  const initDir = join(repoRoot, 'infra/telemetry/clickhouse/init');

  for (const required of [
    manifestPath,
    tsvPath,
    equivalentShaPath,
    roleCredExamplePath,
    runScriptPath,
    verifyScriptPath,
    initDir,
  ]) {
    assert(existsSync(required), `missing required ClickHouse migration artifact: ${required}`);
  }

  const manifest = JSON.parse(readUtf8Lf(manifestPath, 'clickhouse-manifest.v1.json')) as ClickHouseManifest;
  assert(manifest.schemaVersion === 1, 'clickhouse-manifest.v1.json schemaVersion must be 1');
  assert(manifest.product === 'hvac-web', 'clickhouse-manifest.v1.json product must be hvac-web');
  assert(
    manifest.policy &&
      manifest.policy.fixturesAllowed === false &&
      manifest.policy.testdataAllowed === false &&
      manifest.policy.localPasswordStatementsAllowed === false,
    'clickhouse-manifest.v1.json policy must forbid fixtures, testdata, and local passwords',
  );
  assert(manifest.schemaReaderRole === 'phase1_schema_reader', 'clickhouse-manifest.v1.json schemaReaderRole must be phase1_schema_reader');
  assert(
    Array.isArray(manifest.databases) &&
      manifest.databases.length === EXPECTED_DATABASES.length &&
      EXPECTED_DATABASES.every((db, idx) => manifest.databases[idx] === db),
    'clickhouse-manifest.v1.json databases must match telemetry_history, analytics',
  );
  assert(Array.isArray(manifest.migrations) && manifest.migrations.length > 0, 'clickhouse-manifest.v1.json migrations must be non-empty');

  const actualSqlFiles = readdirSync(initDir)
    .filter((name) => name.endsWith('.sql'))
    .sort()
    .map((name) => `infra/telemetry/clickhouse/init/${name}`);

  assert(
    manifest.migrations.length === actualSqlFiles.length &&
      manifest.migrations.every((entry, idx) => entry === actualSqlFiles[idx]),
    'clickhouse-manifest.v1.json migrations must match sorted infra/telemetry/clickhouse/init/*.sql 1:1',
  );

  const tsvLines = readUtf8Lf(tsvPath, 'clickhouse-migration-list.tsv')
    .split('\n')
    .filter((line) => line.length > 0);
  const tsvPaths = tsvLines.map((line) => {
    const match = /^clickhouse\|(.+)$/.exec(line);
    assert(match !== null, `invalid line in clickhouse-migration-list.tsv: ${line}`);
    return match[1];
  });
  assert(
    tsvPaths.length === manifest.migrations.length &&
      tsvPaths.every((entry, idx) => entry === manifest.migrations[idx]),
    'clickhouse-migration-list.tsv must match clickhouse-manifest.v1.json migrations 1:1',
  );

  const rawEquivalent = manifest.equivalentSha256 ?? {};
  assert(
    typeof rawEquivalent === 'object' && rawEquivalent !== null && !Array.isArray(rawEquivalent),
    'clickhouse-manifest.v1.json equivalentSha256 must be an object mapping migration path to legacy sha256 array',
  );
  const registeredSet = new Set(manifest.migrations);
  const expectedEquivalentLines: string[] = [];
  for (const [migrationPath, hashes] of Object.entries(rawEquivalent).sort(([a], [b]) => a.localeCompare(b))) {
    assert(
      registeredSet.has(migrationPath),
      `clickhouse-manifest.v1.json equivalentSha256 references unregistered migration: ${migrationPath}`,
    );
    assert(
      Array.isArray(hashes) && hashes.length > 0,
      `clickhouse-manifest.v1.json equivalentSha256.${migrationPath} must be a non-empty array`,
    );
    const currentSha = sha256File(join(repoRoot, migrationPath));
    const seenHashes = new Set<string>();
    for (const hash of hashes) {
      assert(
        typeof hash === 'string' && /^[0-9a-f]{64}$/.test(hash),
        `clickhouse-manifest.v1.json equivalentSha256.${migrationPath} contains invalid sha256: ${String(hash)}`,
      );
      assert(
        hash !== currentSha,
        `clickhouse-manifest.v1.json equivalentSha256.${migrationPath} must not duplicate current sha256: ${hash}`,
      );
      assert(
        !seenHashes.has(hash),
        `clickhouse-manifest.v1.json equivalentSha256.${migrationPath} contains duplicate sha256: ${hash}`,
      );
      seenHashes.add(hash);
      expectedEquivalentLines.push(`${migrationPath}|${hash}`);
    }
  }

  const eqTsvLines = readUtf8Lf(equivalentShaPath, 'clickhouse-migration-equivalent-sha256.tsv')
    .split('\n')
    .filter((line) => line.length > 0);
  for (const line of eqTsvLines) {
    assert(
      /^[^|]+\|[0-9a-f]{64}$/.test(line),
      `invalid line in clickhouse-migration-equivalent-sha256.tsv: ${line}`,
    );
  }
  assert(
    eqTsvLines.length === expectedEquivalentLines.length &&
      eqTsvLines.every((entry, idx) => entry === expectedEquivalentLines[idx]),
    'clickhouse-migration-equivalent-sha256.tsv must match clickhouse-manifest.v1.json equivalentSha256 1:1',
  );

  const declaredUsers = new Set<string>(['phase1_schema_reader']);
  const localPasswordPattern = /IDENTIFIED\s+WITH\s+(?:plaintext_password|sha256_password|double_sha1_password)\s+BY\b/i;
  for (const relPath of manifest.migrations) {
    const sql = readUtf8Lf(join(repoRoot, relPath), relPath);
    assert(!/testdata|fixture/i.test(sql), `${relPath} must not reference fixture or testdata`);
    assert(!localPasswordPattern.test(sql), `${relPath} must not contain local password statements`);
    for (const match of sql.matchAll(/CREATE\s+USER\s+IF\s+NOT\s+EXISTS\s+([a-zA-Z0-9_]+)/gi)) {
      declaredUsers.add(match[1]);
    }
  }

  const credExampleLines = readUtf8Lf(roleCredExamplePath, 'clickhouse-role-credentials.sql.example')
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.length > 0 && !line.startsWith('--'));
  const credUsers = new Set<string>();
  for (const line of credExampleLines) {
    const match = /^ALTER\s+USER\s+IF\s+EXISTS\s+([a-zA-Z0-9_]+)\s+IDENTIFIED\s+WITH\s+sha256_password\s+BY\s+'\[REDACTED_SECRET\]';$/i.exec(line);
    assert(match !== null, `unexpected statement in clickhouse-role-credentials.sql.example: ${line}`);
    const user = match[1];
    assert(!credUsers.has(user), `duplicate user in clickhouse-role-credentials.sql.example: ${user}`);
    credUsers.add(user);
  }
  for (const user of declaredUsers) {
    assert(credUsers.has(user), `missing ClickHouse role in clickhouse-role-credentials.sql.example: ${user}`);
  }
  for (const user of credUsers) {
    assert(declaredUsers.has(user), `unexpected extra ClickHouse role in clickhouse-role-credentials.sql.example: ${user}`);
  }

  const runScript = readUtf8Lf(runScriptPath, 'run-clickhouse-migrations.sh');
  for (const requiredSnippet of [
    'incomplete ClickHouse migration',
    'ClickHouse migration drift detected',
    'clickhouse-migration-equivalent-sha256.tsv',
    'equivalent checksum',
    'ReplacingMergeTree',
    'FINAL',
    '--param_',
    'phase1_schema_reader',
    'no_password',
    'REDACTED_SECRET',
  ]) {
    assert(runScript.includes(requiredSnippet), `run-clickhouse-migrations.sh missing required safeguard: ${requiredSnippet}`);
  }

  const verifyScript = readUtf8Lf(verifyScriptPath, 'verify-clickhouse-schema.sh');
  for (const requiredSnippet of [
    'ClickHouse schema compatibility state missing',
    'ClickHouse product version mismatch',
    'ClickHouse schema manifest mismatch',
    'FINAL',
  ]) {
    assert(verifyScript.includes(requiredSnippet), `verify-clickhouse-schema.sh missing required safeguard: ${requiredSnippet}`);
  }

  const releasePath = join(repoRoot, 'deploy/platform/phase1/product-release.v1.json');
  const release = JSON.parse(readFileSync(releasePath, 'utf8')) as ProductRelease;
  const manifestRel = 'deploy/platform/phase1/clickhouse-migrations/clickhouse-manifest.v1.json';
  assert(
    release.schemaContract?.clickhouseMigrationManifest === manifestRel,
    `product-release.v1.json clickhouseMigrationManifest must be ${manifestRel}`,
  );
  assert(
    release.schemaContract?.clickhouseMigrationManifestSha256 === sha256File(manifestPath),
    'product-release.v1.json clickhouseMigrationManifestSha256 does not match clickhouse-manifest.v1.json',
  );

  const inventoryPath = join(repoRoot, 'deploy/platform/phase1/runtime-inventory.v1.json');
  const inventory = JSON.parse(readFileSync(inventoryPath, 'utf8')) as RuntimeInventory;
  assert(
    inventory.classes?.requiredStartupOneShot?.includes('phase1-clickhouse-schema-preflight'),
    'runtime-inventory.v1.json requiredStartupOneShot must include phase1-clickhouse-schema-preflight',
  );
  assert(
    inventory.classes?.operatorOneShot?.some(
      (entry) => entry.service === 'phase1-clickhouse-migrator' && entry.profile === 'clickhouse-migration',
    ),
    'runtime-inventory.v1.json operatorOneShot must include phase1-clickhouse-migrator with profile clickhouse-migration',
  );

  const compose = yaml.load(readFileSync(join(repoRoot, 'deploy/platform/phase1/compose.yaml'), 'utf8')) as ComposeDocument;
  const wslOverride = yaml.load(readFileSync(join(repoRoot, 'deploy/platform/phase1/wsl.override.yaml'), 'utf8')) as ComposeDocument;

  assert(
    compose['x-default-env']?.PHASE1_PRODUCT_VERSION !== undefined,
    'compose.yaml x-default-env must define PHASE1_PRODUCT_VERSION',
  );

  for (const [name, doc] of [
    ['compose.yaml', compose],
    ['wsl.override.yaml', wslOverride],
  ] as const) {
    const chVolumes = doc.services?.clickhouse?.volumes ?? [];
    assert(
      !chVolumes.some((v) => v.includes('/docker-entrypoint-initdb.d')),
      `${name} clickhouse service must not mount /docker-entrypoint-initdb.d`,
    );
  }

  const migrator = compose.services?.['phase1-clickhouse-migrator'];
  assert(migrator !== undefined, 'compose.yaml must define phase1-clickhouse-migrator');
  assert(
    Array.isArray(migrator.profiles) && migrator.profiles.length === 1 && migrator.profiles[0] === 'clickhouse-migration',
    'phase1-clickhouse-migrator must use profiles: ["clickhouse-migration"]',
  );
  assert(migrator.network_mode === 'service:clickhouse', 'phase1-clickhouse-migrator must use network_mode: service:clickhouse');
  assert(migrator.depends_on?.clickhouse?.condition === 'service_healthy', 'phase1-clickhouse-migrator must depend on clickhouse service_healthy');
  assert(migrator.read_only === true, 'phase1-clickhouse-migrator must set read_only: true');
  assert(
    (migrator.volumes ?? []).includes('./clickhouse-migrations:/opt/hvac/clickhouse-migrations:ro') &&
      (migrator.volumes ?? []).includes('../../../infra/telemetry/clickhouse/init:/opt/hvac/repo/infra/telemetry/clickhouse/init:ro'),
    'phase1-clickhouse-migrator must mount clickhouse-migrations and init SQL read-only',
  );
  assert(
    JSON.stringify(migrator.entrypoint) === JSON.stringify(['/bin/bash', '/opt/hvac/clickhouse-migrations/run-clickhouse-migrations.sh']),
    'phase1-clickhouse-migrator entrypoint must invoke run-clickhouse-migrations.sh',
  );

  const preflight = compose.services?.['phase1-clickhouse-schema-preflight'];
  assert(preflight !== undefined, 'compose.yaml must define phase1-clickhouse-schema-preflight');
  assert(preflight.profiles === undefined, 'phase1-clickhouse-schema-preflight must not be profile-gated');
  assert(preflight.restart === 'no', 'phase1-clickhouse-schema-preflight must set restart: "no"');
  assert(
    preflight.depends_on?.clickhouse?.condition === 'service_healthy' &&
      preflight.depends_on?.clickhouse?.required === false,
    'phase1-clickhouse-schema-preflight must depend on clickhouse service_healthy with required: false',
  );
  assert(preflight.read_only === true, 'phase1-clickhouse-schema-preflight must set read_only: true');
  assert(
    preflight.environment?.PHASE1_PRODUCT_VERSION !== undefined,
    'phase1-clickhouse-schema-preflight must pass PHASE1_PRODUCT_VERSION',
  );
  assert(
    JSON.stringify(preflight.entrypoint) === JSON.stringify(['/bin/bash', '/opt/hvac/clickhouse-migrations/verify-clickhouse-schema.sh']),
    'phase1-clickhouse-schema-preflight entrypoint must invoke verify-clickhouse-schema.sh',
  );

  for (const serviceName of CLICKHOUSE_CONSUMERS) {
    const dep = compose.services?.[serviceName]?.depends_on?.['phase1-clickhouse-schema-preflight'];
    assert(
      dep?.condition === 'service_completed_successfully',
      `compose.yaml ${serviceName} must depend on phase1-clickhouse-schema-preflight service_completed_successfully`,
    );
  }
}

const isEntrypoint =
  process.argv[1] !== undefined && resolve(fileURLToPath(import.meta.url)) === resolve(process.argv[1]);

if (isEntrypoint) {
  const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
  checkClickHouseMigrations(repoRoot);
  console.log('ClickHouse migration governance check passed.');
}
