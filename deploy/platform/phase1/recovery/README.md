# Phase 1 recovery runbook

This directory operationalizes `SE-OPS-009 V1.0 CURRENT CANDIDATE` for the canonical `Single Linux Server + Docker Compose` deployment.

The values in `recovery-targets.v1.json` are recovery objectives. They are not a zero-downtime or HA guarantee. Production attainment is demonstrated only by a timestamped restore drill using real backup artifacts and the actual deployment procedure.

## Frozen recovery objectives

| Component / data | RPO | RTO |
|---|---:|---:|
| Edge Safety / Local Control | 0 | Continuous |
| PostgreSQL Business SoT | <= 5 min | <= 2 h |
| Control Command / Audit | <= 5 min | <= 1 h |
| Settlement / Config / Device-Point-Topology | <= 5 min | <= 2 h |
| Telemetry | approximately 0 inside the Edge retention window | <= 4 h cloud service |
| Metric Series | <= 30 min | <= 4 h |
| Active Alarm | <= 15 min | <= 1 h |
| Alarm History | <= 15 min | <= 2 h |
| Redis | N/A, rebuildable | <= 15 min |
| MQTT Broker | N/A, not Business SoT | <= 30 min |
| Forecast / Optimization Result | <= 24 h | <= 2 h |
| Object Storage | <= 24 h | <= 4 h |
| Report / Export | <= 24 h | <= 8 h |

Failure-scenario objectives:

- Process/container failure: `RTO <= 5 min`.
- Database process failure with intact data disk: `RTO <= 30 min`.
- Recoverable server OS/runtime failure: `RTO <= 1 h`.
- Whole server replacement: `RTO <= 4 h`, only when spare/cold-standby hardware, external backup, versioned configuration and this recovery procedure are all available.

## RTO clock

`RTO Start` is the time an incident is confirmed to affect service. It is not the time an operator starts work.

`RTO End` is business restoration, not `docker ps` or container startup. The drill is complete only after the required business validation checks pass.

## Required prerequisites

Before a production deployment may claim these objectives, verify all of the following:

1. PostgreSQL has a daily base backup and continuous WAL archive. The current Compose baseline uses `archive_timeout=60s`, which is compatible with the 5-minute PostgreSQL RPO target only when WAL is actually copied to protected external backup storage.
2. `POSTGRES_WAL_ARCHIVE_DIR`, `POSTGRES_BASE_BACKUP_DIR` and `CLICKHOUSE_BACKUP_DIR` resolve to storage outside the failed production data disk. For whole-server recovery, the recovery copy must survive loss of the production server.
3. At least one protected off-server copy exists. A directory on the same failed disk is not disaster recovery.
4. Docker Compose, Nginx configuration, service configuration templates, database migrations, monitoring configuration and backup scripts are versioned.
5. Runtime credential material, TLS material, CA material and encryption keys have an independent encrypted recovery copy. Do not depend on a container filesystem.
6. Edge Store & Forward retention is sufficient for the claimed Telemetry effective RPO. The document baseline assumes an Edge retention window such as 3-7 days; the actual site value must be recorded in the drill.
7. Whole-server `RTO <= 4 h` requires a cold standby or hardware that can actually be supplied within the objective.

## Backup schedule

The Phase 1 infrastructure scheduler/systemd timer owns backup scheduling. Application Scheduler must not execute infrastructure backups.

- PostgreSQL base backup: daily.
- PostgreSQL WAL archive: continuous.
- ClickHouse important-data snapshot: daily.
- Configuration and credential-material backup: daily and after changes.
- Backup verification: daily.
- Restore drill: quarterly as the baseline cadence.

Use the existing backup profiles:

```bash
docker compose -f deploy/platform/phase1/compose.yaml --profile backup run --rm postgres-backup
docker compose -f deploy/platform/phase1/compose.yaml --profile backup run --rm clickhouse-backup
```

Run `backup/verify-recovery-readiness.sh` on the deployment host after the backup copy is mounted. It verifies the most recent PostgreSQL WAL archive age against the 5-minute target and verifies the checksum manifest for the newest base backup. This is backup-readiness evidence, not an RTO drill.

## Whole-server recovery sequence

Follow this order unless an incident-specific reason is recorded in the drill evidence:

1. Prepare Linux, data disks, network and clock synchronization.
2. Install Docker/runtime.
3. Restore versioned configuration and protected credential material.
4. Restore PostgreSQL base backup and replay WAL to the selected recovery point.
5. Validate PostgreSQL integrity before starting business services.
6. Restore/start MQTT.
7. Start Redis as an empty/rebuildable store if necessary.
8. Restore/start ClickHouse and verify a real write.
9. Start `energy-api`.
10. Start `iot-service`.
11. Start `telemetry-worker`.
12. Start `metric-worker`.
13. Start Realtime and `scheduler`.
14. Start Forecast/Optimization only when enabled for the deployment.
15. Reconcile incomplete Control Commands.
16. When the cloud data pipeline is ready, start rate-limited Edge Replay.
17. Perform the business validation checklist and record RTO completion.

PostgreSQL is restored first because it carries Device/Point metadata, Config, Metric definitions, Command state, Settlement, Audit, Outbox and Scheduler Job state.

## Control recovery

Before normal control operations resume, scan incomplete command states including `SENT`, `ACKED`, `EXECUTING` and `UNKNOWN`.

Do not blindly resend them. Reconcile from the PostgreSQL Command SoT against Edge/device readback, then resolve each command to `VERIFIED`, `FAILED` or `UNKNOWN` according to the existing Control/Safety contract.

Control RTO completion requires command-state reconciliation and working readback/safety checks; API readiness alone is insufficient.

## Scheduler and Outbox recovery

Scheduler state is protected with PostgreSQL. After PostgreSQL restoration, `scheduler` scans due Schedules, applies Misfire policy and recreates missing durable Jobs through the normal deduplication rules. Expired Worker leases are recovered through the durable Job state machine.

Pending PostgreSQL Outbox rows must remain intact and resume delivery. Never delete pending Outbox rows to make recovery appear clean.

## Edge Replay

Do not start a large replay while ClickHouse, Telemetry Worker or Metric Worker is unavailable. First establish a healthy cloud data pipeline, then enable replay with a controlled rate. Record the oldest retained Edge timestamp and the oldest successfully restored cloud timestamp in the drill evidence.

Telemetry effective RPO is approximately zero only inside the proven Edge retention window and only when the Edge copy survives the cloud failure.

## Recovery validation checklist

A whole-server drill is not complete until the applicable checks pass:

- PostgreSQL integrity and expected schema/migration state.
- ClickHouse write succeeds.
- Redis Latest is rebuilding from new Telemetry.
- MQTT TLS connectivity succeeds.
- Gateway reconnects.
- Telemetry flows into cloud history and Redis Latest.
- Telemetry lag is within the accepted recovery window.
- Metric calculation/backfill succeeds.
- Current Alarm state is reevaluated from current Telemetry and rules.
- Incomplete Control Commands are reconciled.
- Scheduler backlog and expired leases are understood and progressing.
- Pending Outbox rows are progressing.
- Disk capacity is safe.
- Host and service clocks are synchronized.

## Formal drill evidence

Copy `drill-record.template.json` outside Git and fill it during the drill. Do not check production credentials or backup contents into the repository.

Record at minimum:

- incident-confirmed timestamp (`RTO Start`),
- latest PostgreSQL recoverable timestamp,
- component/business restoration timestamps,
- business-validation completion timestamp (`RTO End`),
- actual RPO and RTO calculated from those timestamps,
- external-backup and cold-standby evidence,
- Edge retention/replay evidence when Telemetry RPO is claimed,
- command reconciliation result,
- Scheduler and Outbox backlog result,
- problems and remediation actions.

Validate a completed record with:

```bash
npm run deployment:phase1:recovery:verify -- --file=/path/to/drill-record.json
```

The verifier compares the recorded measurements with the frozen target contract. It does not create synthetic evidence and it does not turn a local container test into production attestation.
