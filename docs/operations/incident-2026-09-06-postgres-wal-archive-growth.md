# 2026-09-06 remote test PostgreSQL WAL growth incident

## Impact

The remote test deployment at `hvac.swayingwindmill.online` became unavailable for authenticated use. The public Gateway returned `503 ROUTE_AUDIT_FAILED` for `/api/v1/principal`, so the web application could not establish a trusted Principal.

The failure was fail-closed by design: the Gateway refused to execute a route when its route-decision audit record could not be persisted.

## Runtime evidence

- Host root filesystem: 59 GiB, 100% used at incident discovery.
- Phase 1 PostgreSQL repeatedly restarted with `could not write lock file "postmaster.pid": No space left on device`.
- Phase 1 PostgreSQL volume size: about 27 GiB.
- PostgreSQL data directory breakdown:
  - `pg_wal`: about 27 GiB.
  - `base`: about 249 MiB.
- `pg_stat_archiver` showed `archived_count = 0` and a non-zero `failed_count`.
- Once free disk space was restored and PostgreSQL restarted, `/api/v1/principal` changed from `503 ROUTE_AUDIT_FAILED` to the expected unauthenticated `401 AUTHENTICATION_REQUIRED`, and the Phase 1 services returned to healthy state.

## Root cause

The canonical Phase 1 Compose topology enabled PostgreSQL continuous WAL archiving unconditionally, including local and remote test deployments. The remote test archive destination had never successfully archived a WAL segment. PostgreSQL therefore retained WAL in `pg_wal` instead of recycling it. WAL grew until the single-server root filesystem was exhausted.

This was a deployment-policy defect rather than business-data growth. The actual PostgreSQL relation data was only a small fraction of the volume.

## Immediate recovery

1. Reclaimed approximately 3.9 GiB of unused Docker build cache. Database volumes were not deleted or modified.
2. Restarted the Phase 1 PostgreSQL container after disk space became available.
3. Verified PostgreSQL and the dependent Phase 1 services returned healthy.
4. Verified the Gateway route-audit failure disappeared.

No files were manually deleted from `pg_wal`.

## Permanent corrective action

- PostgreSQL WAL archiving is opt-in through `POSTGRES_ARCHIVE_MODE`; the default is `off`.
- Development and remote test deployments keep archiving disabled unless they intentionally provision a verified archive destination.
- Staging and production examples explicitly enable archiving and retain the off-server-backup requirement from SE-OPS-009.
- The WSL override no longer mounts the same WAL archive directory under a second container path.
- The Phase 1 deployment checker enforces the environment boundary so unconditional archiving cannot be reintroduced accidentally.

## Final verification

After the permanent test-environment change was applied, PostgreSQL started with `archive_mode=off`, remained healthy, and recycled `pg_wal` from about 27 GiB to about 81 MiB without manual WAL deletion. The PostgreSQL data directory fell to about 330 MiB, Docker local-volume usage fell to about 2.46 GiB, and the 59 GiB root filesystem returned to about 54% usage with roughly 27 GiB available. The public Principal endpoint returned the expected unauthenticated `401 AUTHENTICATION_REQUIRED` instead of `503 ROUTE_AUDIT_FAILED`.

A roughly 1.4 GiB test WAL-archive copy was created during diagnosis when archive-directory permissions were temporarily corrected. It is not part of `pg_wal` or the PostgreSQL data volume, `archive_mode` is now off, and the copy is no longer growing. Its removal is ordinary test-backup cleanup; the automated execution environment blocked the final destructive cleanup command, so the residual copy is recorded here rather than silently treated as removed.

The existing 80%/90% disk alerts are now complemented by `Phase1HostDiskWillFillWithin24Hours`, which warns when recent free-space consumption projects disk exhaustion within 24 hours.

## Operational rule

Do not enable `POSTGRES_ARCHIVE_MODE=on` merely to make an environment look production-like. Enable it only when the archive destination is writable, capacity-managed, monitored, and independent from the PostgreSQL data volume. For production disaster recovery, the backup location must also survive loss of the application host.

When archiving is enabled, `pg_stat_archiver.failed_count`, archive freshness, and host disk usage are release/runtime signals. A growing `pg_wal` directory with no corresponding successful archive activity is an incident condition.
