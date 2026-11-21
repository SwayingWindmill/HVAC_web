import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import test from 'node:test';

import {
  findPhase1MigrationSyncViolations,
  generatePhase1MigrationArtifacts,
  PHASE1_MANIFEST_PATH,
  renderMigrationDockerfile,
  syncPhase1Migrations,
} from './sync-phase1-migrations.ts';

const root = resolve(process.cwd());

test('migration generator splits large manifests into scratch stages of <= 75 copies (PR #826 regression)', () => {
  const migrations = Array.from({ length: 200 }, (_, i) => `infra/registry/postgres/init/m-${String(i).padStart(3, '0')}.sql`);
  const dockerfile = renderMigrationDockerfile({ databases: [{ name: 'hvac_s1', migrations }] });

  const stages = dockerfile.split(/^FROM\s+/mu).slice(1);
  assert.equal(stages.length, 4); // 3 scratch stages (5+200 = 205 -> 75 + 75 + 55) + final postgres stage
  for (const stage of stages.slice(0, -1)) {
    const copies = stage.split(/\n/u).filter((line) => line.startsWith('COPY ')).length;
    assert.ok(copies <= 75, `expected <= 75 copies in scratch stage, got ${copies}`);
  }
});

test('migration sync check rejects missing compose mount, oversized Dockerfile stage, and drifted tsv (PR #895/#916/#826)', () => {
  const manifestRaw = readFileSync(resolve(root, PHASE1_MANIFEST_PATH), 'utf8');
  const composeRaw = readFileSync(resolve(root, 'infra/registry/compose.yaml'), 'utf8');
  const expected = generatePhase1MigrationArtifacts(manifestRaw, composeRaw);

  // Real past mistake 1: missing infra/registry/compose.yaml mount (e.g. metric-backfill-recalc-job-grants.sql from #895)
  const composeMissingGrant = expected.registryCompose
    .split('\n')
    .filter((line) => !line.includes('metric-backfill-recalc-job-grants.sql'))
    .join('\n');
  const missingMountViolations = findPhase1MigrationSyncViolations({
    manifestRaw,
    migrationList: expected.migrationList,
    dockerfile: expected.dockerfile,
    registryCompose: composeMissingGrant,
  });
  assert.ok(
    missingMountViolations.some((msg) => msg.includes('metric-backfill-recalc-job-grants.sql') && msg.includes('migrations:sync')),
    `expected missing mount violation, got: ${missingMountViolations.join(' | ')}`,
  );

  // Real past mistake 2: unsplit Dockerfile stage > 100 layers (#826 OverlayFS limit)
  const unsplitCopies = Array.from({ length: 105 }, (_, i) => `COPY f${i}.sql /opt/hvac/repo/f${i}.sql`).join('\n');
  const oversizedDockerfile = `FROM scratch AS sources-1\n${unsplitCopies}\nFROM postgres:16.4-bookworm\n`;
  const oversizedViolations = findPhase1MigrationSyncViolations({
    manifestRaw,
    migrationList: expected.migrationList,
    dockerfile: oversizedDockerfile,
    registryCompose: expected.registryCompose,
  });
  assert.ok(
    oversizedViolations.some((msg) => msg.includes('OverlayFS limit 127')),
    `expected OverlayFS layer limit violation, got: ${oversizedViolations.join(' | ')}`,
  );

  // Real past mistake 3: drifted migration-list.tsv
  const tsvViolations = findPhase1MigrationSyncViolations({
    manifestRaw,
    migrationList: `${expected.migrationList}hvac_s1|infra/registry/postgres/init/extra.sql\n`,
    dockerfile: expected.dockerfile,
    registryCompose: expected.registryCompose,
  });
  assert.ok(
    tsvViolations.some((msg) => msg.includes('migration-list.tsv is out of sync')),
    `expected tsv drift violation, got: ${tsvViolations.join(' | ')}`,
  );
});

test('repository migration-list.tsv, Dockerfile, and infra/registry/compose.yaml match manifest.v1.json', () => {
  const violations = syncPhase1Migrations(root, { check: true });
  assert.deepEqual(violations, []);
});
