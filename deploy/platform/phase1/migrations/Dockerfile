FROM postgres:16.4-bookworm

WORKDIR /opt/hvac
COPY deploy/platform/phase1/migrations/bootstrap-roles.sql /opt/hvac/migrations/bootstrap-roles.sql
COPY deploy/platform/phase1/migrations/migration-list.tsv /opt/hvac/migrations/migration-list.tsv
COPY deploy/platform/phase1/migrations/run-phase1-migrations.sh /opt/hvac/migrations/run-phase1-migrations.sh
COPY deploy/platform/phase1/postgres/init/001-create-phase1-databases.sql /opt/hvac/migrations/create-databases.sql

COPY infra/s0-durable/postgres/init/001-s0-durable.sql /opt/hvac/repo/infra/s0-durable/postgres/init/001-s0-durable.sql
COPY infra/s0-durable/postgres/init/002-s1-registry-routing-audit.sql /opt/hvac/repo/infra/s0-durable/postgres/init/002-s1-registry-routing-audit.sql
COPY infra/s0-durable/postgres/init/003-operations-audit-ledger.sql /opt/hvac/repo/infra/s0-durable/postgres/init/003-operations-audit-ledger.sql
COPY infra/s0-durable/postgres/init/004-audit-postgres-outbox.sql /opt/hvac/repo/infra/s0-durable/postgres/init/004-audit-postgres-outbox.sql

COPY infra/s1-registry/postgres/init/001-s1-registry-baseline.sql /opt/hvac/repo/infra/s1-registry/postgres/init/001-s1-registry-baseline.sql
COPY infra/s1-registry/postgres/init/001a-tenant-foundation.sql /opt/hvac/repo/infra/s1-registry/postgres/init/001a-tenant-foundation.sql
COPY infra/s1-registry/postgres/init/001b-device-binding-invariants.sql /opt/hvac/repo/infra/s1-registry/postgres/init/001b-device-binding-invariants.sql
COPY infra/s1-registry/postgres/init/003-iam-runtime-identity-resolution.sql /opt/hvac/repo/infra/s1-registry/postgres/init/003-iam-runtime-identity-resolution.sql
COPY infra/s1-registry/postgres/init/004-iam-reconciliation.sql /opt/hvac/repo/infra/s1-registry/postgres/init/004-iam-reconciliation.sql
COPY infra/s1-registry/postgres/init/005-core-read-service.sql /opt/hvac/repo/infra/s1-registry/postgres/init/005-core-read-service.sql
COPY infra/s1-registry/postgres/init/006-s2-telemetry-authorization.sql /opt/hvac/repo/infra/s1-registry/postgres/init/006-s2-telemetry-authorization.sql
COPY infra/s1-registry/postgres/init/007-s4-alarm-authorization.sql /opt/hvac/repo/infra/s1-registry/postgres/init/007-s4-alarm-authorization.sql
COPY infra/s1-registry/postgres/init/007-spatial-sensor-point-model.sql /opt/hvac/repo/infra/s1-registry/postgres/init/007-spatial-sensor-point-model.sql
COPY infra/s1-registry/postgres/init/007a-tenant-spatial-scope.sql /opt/hvac/repo/infra/s1-registry/postgres/init/007a-tenant-spatial-scope.sql
COPY infra/s1-registry/postgres/init/007b-control-point-invariants.sql /opt/hvac/repo/infra/s1-registry/postgres/init/007b-control-point-invariants.sql
COPY infra/s1-registry/postgres/init/008-s5-work-order-authorization.sql /opt/hvac/repo/infra/s1-registry/postgres/init/008-s5-work-order-authorization.sql
COPY infra/s1-registry/postgres/init/009-energy-data-foundation.sql /opt/hvac/repo/infra/s1-registry/postgres/init/009-energy-data-foundation.sql
COPY infra/s1-registry/postgres/init/009c-metric-model-v2.sql /opt/hvac/repo/infra/s1-registry/postgres/init/009c-metric-model-v2.sql
COPY infra/s1-registry/postgres/init/009d-data-governance-v2.sql /opt/hvac/repo/infra/s1-registry/postgres/init/009d-data-governance-v2.sql
COPY infra/s1-registry/postgres/init/009g-object-storage-governance-v2.sql /opt/hvac/repo/infra/s1-registry/postgres/init/009g-object-storage-governance-v2.sql
COPY infra/s1-registry/postgres/init/009i-backend-integration-v2.sql /opt/hvac/repo/infra/s1-registry/postgres/init/009i-backend-integration-v2.sql

COPY infra/s2-telemetry/postgres/init/001-s2-telemetry-baseline.sql /opt/hvac/repo/infra/s2-telemetry/postgres/init/001-s2-telemetry-baseline.sql
COPY infra/s2-telemetry/postgres/init/002-s2-telemetry-runtime-snapshot.sql /opt/hvac/repo/infra/s2-telemetry/postgres/init/002-s2-telemetry-runtime-snapshot.sql
COPY infra/s2-telemetry/postgres/init/003-s2-telemetry-ingest.sql /opt/hvac/repo/infra/s2-telemetry/postgres/init/003-s2-telemetry-ingest.sql
COPY infra/s2-telemetry/postgres/init/004-s2-telemetry-history-outbox.sql /opt/hvac/repo/infra/s2-telemetry/postgres/init/004-s2-telemetry-history-outbox.sql
COPY infra/s2-telemetry/postgres/init/004a-s2-tenant-scope.sql /opt/hvac/repo/infra/s2-telemetry/postgres/init/004a-s2-tenant-scope.sql
COPY infra/s2-telemetry/postgres/init/004b-s2-point-identity.sql /opt/hvac/repo/infra/s2-telemetry/postgres/init/004b-s2-point-identity.sql
COPY infra/s2-telemetry/postgres/init/005-s2-realtime-backend.sql /opt/hvac/repo/infra/s2-telemetry/postgres/init/005-s2-realtime-backend.sql

COPY services/command-service/migrations/001_s3_command_runtime.sql /opt/hvac/repo/services/command-service/migrations/001_s3_command_runtime.sql
COPY services/command-service/migrations/003_s3_tenant_scope.sql /opt/hvac/repo/services/command-service/migrations/003_s3_tenant_scope.sql
COPY services/command-service/migrations/004_s3_command_point_identity.sql /opt/hvac/repo/services/command-service/migrations/004_s3_command_point_identity.sql
COPY services/alarm-service/migrations/001_s4_alarm_runtime.sql /opt/hvac/repo/services/alarm-service/migrations/001_s4_alarm_runtime.sql
COPY services/alarm-service/migrations/002_s4_alarm_lifecycle.sql /opt/hvac/repo/services/alarm-service/migrations/002_s4_alarm_lifecycle.sql
COPY services/alarm-service/migrations/003_s4_tenant_scope.sql /opt/hvac/repo/services/alarm-service/migrations/003_s4_tenant_scope.sql
COPY services/alarm-service/migrations/004_s4_event_provenance.sql /opt/hvac/repo/services/alarm-service/migrations/004_s4_event_provenance.sql
COPY services/work-order-service/migrations/001_s5_work_order_runtime.sql /opt/hvac/repo/services/work-order-service/migrations/001_s5_work_order_runtime.sql
COPY services/work-order-service/migrations/002_s5_work_order_create_assignment.sql /opt/hvac/repo/services/work-order-service/migrations/002_s5_work_order_create_assignment.sql
COPY services/work-order-service/migrations/003_s5_work_order_lifecycle.sql /opt/hvac/repo/services/work-order-service/migrations/003_s5_work_order_lifecycle.sql
COPY services/work-order-service/migrations/004_s5_tenant_scope.sql /opt/hvac/repo/services/work-order-service/migrations/004_s5_tenant_scope.sql

RUN find /opt/hvac/repo -type f -name '*.sql' -exec sed -i "/ALTER ROLE.*local-only/d" {} + \
    && ! grep -R -E 'local-only|fixture-only' /opt/hvac/repo \
    && chmod 0555 /opt/hvac/migrations/run-phase1-migrations.sh
ENTRYPOINT ["/opt/hvac/migrations/run-phase1-migrations.sh"]
