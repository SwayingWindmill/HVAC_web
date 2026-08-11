# Phase 1 backup contract

This directory implements the Phase 1 deployment-side backup contract from `架构规划/智慧能源系统部署与运维架构设计.md` without introducing a database HA cluster.

## PostgreSQL

The canonical Compose service enables continuous WAL archiving to `POSTGRES_WAL_ARCHIVE_DIR`. A base backup is executed with:

```bash
docker compose -f deploy/platform/phase1/compose.yaml --profile backup run --rm postgres-backup
```

Production must point both `POSTGRES_WAL_ARCHIVE_DIR` and `POSTGRES_BASE_BACKUP_DIR` at storage that is independent from the PostgreSQL data volume. That storage must be encrypted and access-controlled. A local directory on the same failed disk is not a disaster-recovery backup.

The base backup uses `pg_basebackup` with streamed WAL and writes `SHA256SUMS`. Continuous archive + a suitable base backup is the deployment prerequisite for PITR. This repository does not claim an RPO until a timestamped recovery drill measures it.

## ClickHouse

ClickHouse exposes a single-node native backup disk at `CLICKHOUSE_BACKUP_DIR`. Run:

```bash
docker compose -f deploy/platform/phase1/compose.yaml --profile backup run --rm clickhouse-backup
```

Production must place `CLICKHOUSE_BACKUP_DIR` on protected backup storage rather than the ClickHouse data volume.

## Scheduling

Phase 1 operators register backup invocations in one controlled deployment scheduler (for example one managed systemd timer set on the Linux application/data host). Do not add unrelated per-service cron loops.

A baseline policy is:

- PostgreSQL base backup: daily.
- PostgreSQL WAL archive: continuous, with archive timeout bounded by the deployment config.
- ClickHouse backup: daily after the main ingestion window, or according to site data volume.
- Restore test: periodic and mandatory; use the existing automated Destroy + Restore acceptance as the minimum software proof, then perform a staging restore with real backup artifacts.

## RPO/RTO boundary

Backup configuration is not an RPO/RTO attestation. `RPO/RTO` remains a formal acceptance item until the recovery drill records failure time, latest recoverable transaction/telemetry time, service restoration time and business verification time.
