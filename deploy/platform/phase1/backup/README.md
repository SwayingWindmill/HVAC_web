# Phase 1 backup contract

This directory implements the Phase 1 deployment-side backup contract from `SE-ARCH-DEPLOY-001 V1.0 CURRENT` and the recovery objectives from `SE-OPS-009 V1.0 CURRENT CANDIDATE` without introducing a database HA cluster.

## PostgreSQL

When `PHASE1_POSTGRES_MODE=local`, the canonical Compose PostgreSQL service supports continuous WAL archiving to `POSTGRES_WAL_ARCHIVE_DIR`, but archiving is enabled only when `POSTGRES_ARCHIVE_MODE=on`. Development and test environments default to `off`; staging/production must opt in only after the archive destination is writable, capacity-managed and independent from the PostgreSQL data volume. A base backup is executed with:

```bash
docker compose -f deploy/platform/phase1/compose.yaml --profile local-postgres --profile backup run --rm postgres-backup
```

When PostgreSQL is external, backup ownership follows that external deployment. The same `postgres-backup` container can target the configured external host only when the database permits `pg_basebackup`; managed-provider backup and PITR should otherwise be operated at the provider/data-plane layer rather than emulated on the application host.

For local PostgreSQL, Production must point both `POSTGRES_WAL_ARCHIVE_DIR` and `POSTGRES_BASE_BACKUP_DIR` at storage that is independent from the PostgreSQL data volume. For external PostgreSQL, the equivalent provider/self-managed backup location must be independent from the application host. A local directory on the same failed disk is not a disaster-recovery backup.

The base backup uses `pg_basebackup` with streamed WAL and writes `SHA256SUMS`. Continuous archive + a suitable base backup is the deployment prerequisite for PITR. This repository does not claim an RPO until a timestamped recovery drill measures it.

## ClickHouse

When `PHASE1_CLICKHOUSE_MODE=local`, ClickHouse exposes a single-node native backup disk at `CLICKHOUSE_BACKUP_DIR`. Run:

```bash
docker compose -f deploy/platform/phase1/compose.yaml --profile local-clickhouse --profile backup run --rm clickhouse-backup
```

Production must place `CLICKHOUSE_BACKUP_DIR` on protected backup storage rather than the ClickHouse data volume. When ClickHouse is external, backup and restore ownership follows that external data plane or managed provider; the application host does not emulate external ClickHouse durability.

## Scheduling

Phase 1 operators register backup invocations in one controlled deployment scheduler (for example one managed systemd timer set on the Linux application/data host). Do not add unrelated per-service cron loops.

A baseline policy is:

- PostgreSQL base backup: daily.
- PostgreSQL WAL archive: continuous, with archive timeout bounded by the deployment config.
- ClickHouse backup: daily after the main ingestion window, or according to site data volume.
- Restore test: periodic and mandatory; the existing standalone Destroy + Restore test may be run explicitly as software proof, but it is not an automatic release gate and does not replace a staging/production-like restore with real backup artifacts.

## Recovery objectives and verification

`SE-OPS-009 V1.0 CURRENT CANDIDATE` freezes the Phase 1 PostgreSQL objective at `RPO <= 5 min / RTO <= 2 h`; Control Command/Audit is `RPO <= 5 min / RTO <= 1 h`. Whole-server replacement is `RTO <= 4 h` only when spare/cold-standby hardware, an off-server backup, versioned configuration and a documented recovery procedure are all available.

The current PostgreSQL baseline uses `archive_timeout=60s`. Production WAL/base-backup paths must therefore be backed by protected storage outside the failed production server/data disk if those objectives are claimed. A same-server backup copy is not whole-server disaster recovery.

Run the host-side readiness check daily after the external backup mount is available:

```bash
POSTGRES_WAL_ARCHIVE_DIR=/mnt/smart-energy-backup/postgres/wal \
POSTGRES_BASE_BACKUP_DIR=/mnt/smart-energy-backup/postgres/base \
./deploy/platform/phase1/backup/verify-recovery-readiness.sh
```

It verifies that the newest archived WAL file is no older than the 5-minute readiness threshold and verifies the newest base-backup checksum manifest. This is backup-readiness evidence only.

Backup configuration and readiness checks are not RPO/RTO attestation. Production attainment requires the timestamped drill defined in `../recovery/README.md`, recording the incident-confirmed time, latest recoverable PostgreSQL time, component/business restoration times and final business-validation time.
