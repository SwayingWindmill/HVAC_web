import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

export interface MigrationManifestDatabase {
  name: string;
  migrations: string[];
}

export interface MigrationManifest {
  schemaVersion?: number;
  databases: MigrationManifestDatabase[];
}

export const PHASE1_MANIFEST_PATH = 'deploy/platform/phase1/migrations/manifest.v1.json';
export const PHASE1_MIGRATION_LIST_PATH = 'deploy/platform/phase1/migrations/migration-list.tsv';
export const PHASE1_MIGRATION_DOCKERFILE_PATH = 'deploy/platform/phase1/migrations/Dockerfile';
export const REGISTRY_COMPOSE_PATH = 'infra/registry/compose.yaml';

const FIXED_MIGRATOR_COPIES = Object.freeze([
  'COPY deploy/platform/phase1/migrations/bootstrap-roles.sql /opt/hvac/migrations/bootstrap-roles.sql',
  'COPY deploy/platform/phase1/migrations/migration-list.tsv /opt/hvac/migrations/migration-list.tsv',
  'COPY deploy/platform/phase1/migrations/run-phase1-migrations.sh /opt/hvac/migrations/run-phase1-migrations.sh',
  'COPY deploy/platform/phase1/migrations/verify-phase1-schema.sh /opt/hvac/migrations/verify-phase1-schema.sh',
  'COPY deploy/platform/phase1/postgres/init/001-create-phase1-databases.sql /opt/hvac/migrations/create-databases.sql',
]);

// Three historical IAM capability migrations live in the manifest and migrator image, while the
// local S1 compose fixture still initializes without them to preserve its baseline role fixture.
const EXCLUDED_S1_COMPOSE_MIGRATIONS = new Set<string>([
  'infra/registry/postgres/init/014-s21-rule-management-capability.sql',
  'infra/registry/postgres/init/015-s04-role-binding-telemetry-revocation.sql',
  'infra/registry/postgres/init/016-s24-alarm-assign-capability.sql',
]);

export function parseMigrationManifest(raw: string): MigrationManifest {
  const parsed = JSON.parse(raw) as unknown;
  if (!parsed || typeof parsed !== 'object' || !Array.isArray((parsed as { databases?: unknown }).databases)) {
    throw new Error(`${PHASE1_MANIFEST_PATH} must define a databases array`);
  }
  const databases = (parsed as { databases: unknown[] }).databases.map((entry, index) => {
    if (!entry || typeof entry !== 'object') {
      throw new Error(`${PHASE1_MANIFEST_PATH}: databases[${index}] must be an object`);
    }
    const record = entry as { name?: unknown; migrations?: unknown };
    if (typeof record.name !== 'string' || record.name.length === 0 || !Array.isArray(record.migrations)) {
      throw new Error(`${PHASE1_MANIFEST_PATH}: databases[${index}] must include non-empty name and migrations array`);
    }
    const migrations = record.migrations.map((migration, migrationIndex) => {
      if (typeof migration !== 'string' || migration.length === 0 || /\s/u.test(migration)) {
        throw new Error(`${PHASE1_MANIFEST_PATH}: invalid migration path at ${record.name}[${migrationIndex}]`);
      }
      return migration;
    });
    return { name: record.name, migrations };
  });
  return {
    schemaVersion: typeof (parsed as { schemaVersion?: unknown }).schemaVersion === 'number'
      ? (parsed as { schemaVersion: number }).schemaVersion
      : undefined,
    databases,
  };
}

export function renderMigrationList(manifest: MigrationManifest): string {
  const lines: string[] = [];
  for (const database of manifest.databases) {
    for (const migration of database.migrations) {
      lines.push(`${database.name}|${migration}`);
    }
  }
  return `${lines.join('\n')}\n`;
}

export function renderMigrationDockerfile(manifest: MigrationManifest, maxCopiesPerStage = 75): string {
  if (!Number.isInteger(maxCopiesPerStage) || maxCopiesPerStage < FIXED_MIGRATOR_COPIES.length + 1) {
    throw new Error(`maxCopiesPerStage must be an integer >= ${FIXED_MIGRATOR_COPIES.length + 1}`);
  }
  const seen = new Set<string>();
  const allCopyLines: string[] = [...FIXED_MIGRATOR_COPIES];
  for (const database of manifest.databases) {
    for (const migration of database.migrations) {
      if (seen.has(migration)) continue;
      seen.add(migration);
      allCopyLines.push(`COPY ${migration} /opt/hvac/repo/${migration}`);
    }
  }

  const stages: string[][] = [];
  for (let offset = 0; offset < allCopyLines.length; offset += maxCopiesPerStage) {
    stages.push(allCopyLines.slice(offset, offset + maxCopiesPerStage));
  }

  const parts: string[] = [
    '# Generated from deploy/platform/phase1/migrations/manifest.v1.json via `npm run migrations:sync`.',
    `# Split COPY steps across scratch stages (<= ${maxCopiesPerStage} layers each) to stay under OverlayFS's 127-layer limit (#659, #826).`,
  ];

  stages.forEach((stageLines, index) => {
    parts.push(`FROM scratch AS sources-${index + 1}`);
    parts.push('');
    parts.push(...stageLines);
    parts.push('');
  });

  parts.push('FROM postgres:16.4-bookworm');
  parts.push('');
  parts.push('WORKDIR /opt/hvac');
  stages.forEach((_, index) => {
    parts.push(`COPY --from=sources-${index + 1} /opt/hvac /opt/hvac`);
  });
  parts.push("RUN ! grep -R -E 'local-only|fixture-only' /opt/hvac/repo \\");
  parts.push('    && chmod 0555 /opt/hvac/migrations/run-phase1-migrations.sh /opt/hvac/migrations/verify-phase1-schema.sh');
  parts.push('ENTRYPOINT ["/opt/hvac/migrations/run-phase1-migrations.sh"]');

  return `${parts.join('\n')}\n`;
}

export function resolveRegistryComposeMounts(manifest: MigrationManifest): string[] {
  const s1 = manifest.databases.find((database) => database.name === 'hvac_s1');
  if (!s1) {
    throw new Error(`${PHASE1_MANIFEST_PATH} must contain hvac_s1`);
  }

  const mounts: string[] = ['./postgres/init/000-bootstrap-identities.sql'];
  let saw001b = false;
  let saw009i = false;

  for (const migration of s1.migrations) {
    if (!EXCLUDED_S1_COMPOSE_MIGRATIONS.has(migration)) {
      if (migration.startsWith('infra/registry/postgres/init/')) {
        mounts.push(`./postgres/init/${migration.slice('infra/registry/postgres/init/'.length)}`);
      } else if (migration.startsWith('infra/connectivity/postgres/init/')) {
        mounts.push(`../connectivity/postgres/init/${migration.slice('infra/connectivity/postgres/init/'.length)}`);
      }
    }
    if (migration === 'infra/registry/postgres/init/001b-device-binding-invariants.sql') {
      mounts.push('./postgres/init/002-s1-registry-fixtures.sql');
      saw001b = true;
    } else if (migration === 'infra/registry/postgres/init/009i-backend-integration-v2.sql') {
      mounts.push('./postgres/init/009j-operations-support-domains-v2.sql');
      mounts.push('./postgres/init/009k-mlops-metadata-v2.sql');
      saw009i = true;
    }
  }

  if (!saw001b || !saw009i) {
    throw new Error(`${PHASE1_MANIFEST_PATH}: missing required registry anchor migrations for local compose splice`);
  }

  return mounts;
}

export function renderRegistryCompose(manifest: MigrationManifest, existingComposeText: string): string {
  const mounts = resolveRegistryComposeMounts(manifest);
  const volumeLines = [
    '    volumes:',
    '      # Generated from deploy/platform/phase1/migrations/manifest.v1.json via `npm run migrations:sync`.',
    '      # Mount migrations with deterministic numeric names. Relying on the host',
    '      # directory/glob collation made 001a execute before 001 on some locales.',
    ...mounts.map((sourcePath, index) => {
      const targetIndex = String((index + 1) * 10).padStart(4, '0');
      return `      - ${sourcePath}:/docker-entrypoint-initdb.d/${targetIndex}.sql:ro`;
    }),
  ].join('\n');

  const pattern = /^ {4}volumes:\r?\n[\s\S]*?(?=\r?\n {4}ports:)/mu;
  if (!pattern.test(existingComposeText)) {
    throw new Error(`${REGISTRY_COMPOSE_PATH} is missing expected "    volumes:" ... "    ports:" block`);
  }
  return existingComposeText.replace(pattern, volumeLines);
}

export interface Phase1MigrationArtifacts {
  migrationList: string;
  dockerfile: string;
  registryCompose: string;
}

export function generatePhase1MigrationArtifacts(
  manifestRaw: string,
  existingComposeText: string,
  maxCopiesPerStage = 75,
): Phase1MigrationArtifacts {
  const manifest = parseMigrationManifest(manifestRaw);
  return {
    migrationList: renderMigrationList(manifest),
    dockerfile: renderMigrationDockerfile(manifest, maxCopiesPerStage),
    registryCompose: renderRegistryCompose(manifest, existingComposeText),
  };
}

export function findPhase1MigrationSyncViolations(inputs: {
  manifestRaw: string;
  migrationList: string;
  dockerfile: string;
  registryCompose: string;
  fileExists?: (repoRelativePath: string) => boolean;
}): string[] {
  const violations: string[] = [];
  const manifest = parseMigrationManifest(inputs.manifestRaw);

  if (inputs.fileExists) {
    for (const database of manifest.databases) {
      for (const migration of database.migrations) {
        if (!inputs.fileExists(migration)) {
          violations.push(`${PHASE1_MANIFEST_PATH} lists missing migration file ${migration}`);
        }
      }
    }
  }

  const expected = generatePhase1MigrationArtifacts(inputs.manifestRaw, inputs.registryCompose);

  if (inputs.migrationList !== expected.migrationList) {
    violations.push(
      `${PHASE1_MIGRATION_LIST_PATH} is out of sync with ${PHASE1_MANIFEST_PATH}; run \`npm run migrations:sync\`.`,
    );
  }

  for (const stage of inputs.dockerfile.split(/^FROM\s+/mu).slice(1)) {
    const header = stage.split(/\r?\n/u, 1)[0].trim();
    const layers = stage.split(/\r?\n/u).filter((line) => /^(COPY|ADD|RUN)\s+/u.test(line.trim())).length;
    if (layers > 100) {
      violations.push(
        `${PHASE1_MIGRATION_DOCKERFILE_PATH} stage "${header}" has ${layers} layer instructions (> 100; OverlayFS limit 127); run \`npm run migrations:sync\`.`,
      );
    }
  }

  if (inputs.dockerfile !== expected.dockerfile) {
    violations.push(
      `${PHASE1_MIGRATION_DOCKERFILE_PATH} is out of sync with ${PHASE1_MANIFEST_PATH}; run \`npm run migrations:sync\`.`,
    );
  }

  const expectedMounts = resolveRegistryComposeMounts(manifest);
  const missingMounts = expectedMounts.filter((mount) => !inputs.registryCompose.includes(`${mount}:/docker-entrypoint-initdb.d/`));
  if (missingMounts.length > 0) {
    violations.push(
      `${REGISTRY_COMPOSE_PATH} is missing hvac_s1 mount(s): ${missingMounts.join(', ')}; run \`npm run migrations:sync\`.`,
    );
  } else if (inputs.registryCompose !== expected.registryCompose) {
    violations.push(
      `${REGISTRY_COMPOSE_PATH} mount order or numbering drifted from ${PHASE1_MANIFEST_PATH}; run \`npm run migrations:sync\`.`,
    );
  }

  return violations;
}

export function syncPhase1Migrations(root: string, options: { check: boolean }): string[] {
  const manifestRaw = readFileSync(resolve(root, PHASE1_MANIFEST_PATH), 'utf8');
  const migrationListPath = resolve(root, PHASE1_MIGRATION_LIST_PATH);
  const dockerfilePath = resolve(root, PHASE1_MIGRATION_DOCKERFILE_PATH);
  const composePath = resolve(root, REGISTRY_COMPOSE_PATH);

  const currentMigrationList = existsSync(migrationListPath) ? readFileSync(migrationListPath, 'utf8') : '';
  const currentDockerfile = existsSync(dockerfilePath) ? readFileSync(dockerfilePath, 'utf8') : '';
  const currentCompose = readFileSync(composePath, 'utf8');

  if (options.check) {
    return findPhase1MigrationSyncViolations({
      manifestRaw,
      migrationList: currentMigrationList,
      dockerfile: currentDockerfile,
      registryCompose: currentCompose,
      fileExists: (repoRelativePath) => existsSync(resolve(root, repoRelativePath)),
    });
  }

  const generated = generatePhase1MigrationArtifacts(manifestRaw, currentCompose);
  const manifest = parseMigrationManifest(manifestRaw);
  for (const database of manifest.databases) {
    for (const migration of database.migrations) {
      if (!existsSync(resolve(root, migration))) {
        throw new Error(`${PHASE1_MANIFEST_PATH} references non-existent file: ${migration}`);
      }
    }
  }

  writeFileSync(migrationListPath, generated.migrationList, 'utf8');
  writeFileSync(dockerfilePath, generated.dockerfile, 'utf8');
  writeFileSync(composePath, generated.registryCompose, 'utf8');
  return [];
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
  const checkOnly = process.argv.includes('--check');
  const violations = syncPhase1Migrations(root, { check: checkOnly });
  if (violations.length > 0) {
    console.error(violations.join('\n'));
    process.exit(1);
  }
  console.log(
    checkOnly
      ? 'Phase 1 migration artifacts match manifest.v1.json.'
      : 'Synchronized migration-list.tsv, migrations/Dockerfile, and infra/registry/compose.yaml from manifest.v1.json.',
  );
}
