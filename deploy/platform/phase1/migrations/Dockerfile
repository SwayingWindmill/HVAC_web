FROM postgres:16.4-bookworm

WORKDIR /opt/hvac
COPY deploy/platform/phase1/migrations/bootstrap-roles.sql /opt/hvac/migrations/bootstrap-roles.sql
COPY deploy/platform/phase1/migrations/migration-list.tsv /opt/hvac/migrations/migration-list.tsv
COPY deploy/platform/phase1/migrations/run-phase1-migrations.sh /opt/hvac/migrations/run-phase1-migrations.sh
COPY deploy/platform/phase1/migrations/verify-phase1-schema.sh /opt/hvac/migrations/verify-phase1-schema.sh
COPY deploy/platform/phase1/postgres/init/001-create-phase1-databases.sql /opt/hvac/migrations/create-databases.sql

COPY infra/identity/postgres/init/001-identity-baseline.sql /opt/hvac/repo/infra/identity/postgres/init/001-identity-baseline.sql
COPY infra/identity/postgres/init/002-identity-runtime-grants.sql /opt/hvac/repo/infra/identity/postgres/init/002-identity-runtime-grants.sql
COPY infra/identity/postgres/init/003-identity-directory-least-privilege.sql /opt/hvac/repo/infra/identity/postgres/init/003-identity-directory-least-privilege.sql
COPY infra/identity/postgres/init/004-identity-auth-assurance.sql /opt/hvac/repo/infra/identity/postgres/init/004-identity-auth-assurance.sql
COPY infra/s0-durable/postgres/init/001-s0-durable.sql /opt/hvac/repo/infra/s0-durable/postgres/init/001-s0-durable.sql
COPY infra/s0-durable/postgres/init/002-s0-session-idle.sql /opt/hvac/repo/infra/s0-durable/postgres/init/002-s0-session-idle.sql
COPY infra/s0-durable/postgres/init/002-s1-registry-routing-audit.sql /opt/hvac/repo/infra/s0-durable/postgres/init/002-s1-registry-routing-audit.sql
COPY infra/s0-durable/postgres/init/003-operations-audit-ledger.sql /opt/hvac/repo/infra/s0-durable/postgres/init/003-operations-audit-ledger.sql
COPY infra/s0-durable/postgres/init/004-audit-postgres-outbox.sql /opt/hvac/repo/infra/s0-durable/postgres/init/004-audit-postgres-outbox.sql
COPY infra/s0-durable/postgres/init/005-s0-session-assurance.sql /opt/hvac/repo/infra/s0-durable/postgres/init/005-s0-session-assurance.sql
COPY infra/s0-durable/postgres/init/006-s04-audit-search.sql /opt/hvac/repo/infra/s0-durable/postgres/init/006-s04-audit-search.sql

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
COPY infra/s1-registry/postgres/init/007c-counter-semantics.sql /opt/hvac/repo/infra/s1-registry/postgres/init/007c-counter-semantics.sql
COPY infra/s1-registry/postgres/init/008-s5-work-order-authorization.sql /opt/hvac/repo/infra/s1-registry/postgres/init/008-s5-work-order-authorization.sql
COPY infra/s1-registry/postgres/init/009-energy-data-foundation.sql /opt/hvac/repo/infra/s1-registry/postgres/init/009-energy-data-foundation.sql
COPY infra/s1-registry/postgres/init/009a-energy-topology-metering-v2.sql /opt/hvac/repo/infra/s1-registry/postgres/init/009a-energy-topology-metering-v2.sql
COPY infra/s1-registry/postgres/init/009b-settlement-foundation-v2.sql /opt/hvac/repo/infra/s1-registry/postgres/init/009b-settlement-foundation-v2.sql
COPY infra/s1-registry/postgres/init/009c-metric-model-v2.sql /opt/hvac/repo/infra/s1-registry/postgres/init/009c-metric-model-v2.sql
COPY infra/s1-registry/postgres/init/009d-data-governance-v2.sql /opt/hvac/repo/infra/s1-registry/postgres/init/009d-data-governance-v2.sql
COPY infra/s1-registry/postgres/init/009e-forecast-model-v2.sql /opt/hvac/repo/infra/s1-registry/postgres/init/009e-forecast-model-v2.sql
COPY infra/s1-registry/postgres/init/009f-optimization-model-v2.sql /opt/hvac/repo/infra/s1-registry/postgres/init/009f-optimization-model-v2.sql
COPY infra/s1-registry/postgres/init/009g-object-storage-governance-v2.sql /opt/hvac/repo/infra/s1-registry/postgres/init/009g-object-storage-governance-v2.sql
COPY infra/s1-registry/postgres/init/009i-backend-integration-v2.sql /opt/hvac/repo/infra/s1-registry/postgres/init/009i-backend-integration-v2.sql
COPY infra/s1-registry/postgres/init/009l-scheduler-job-v1.sql /opt/hvac/repo/infra/s1-registry/postgres/init/009l-scheduler-job-v1.sql
COPY infra/s1-registry/postgres/init/009-s04-tenant-iam-admin.sql /opt/hvac/repo/infra/s1-registry/postgres/init/009-s04-tenant-iam-admin.sql
COPY services/outbound-delivery-service/migrations/001_s15_outbound_delivery.sql /opt/hvac/repo/services/outbound-delivery-service/migrations/001_s15_outbound_delivery.sql
COPY infra/s1-registry/postgres/init/010-s05-registry-writer.sql /opt/hvac/repo/infra/s1-registry/postgres/init/010-s05-registry-writer.sql
COPY infra/s1-registry/postgres/init/011-s08-metric-lifecycle.sql /opt/hvac/repo/infra/s1-registry/postgres/init/011-s08-metric-lifecycle.sql
COPY infra/s1-registry/postgres/init/012-s19-settlement-projections.sql /opt/hvac/repo/infra/s1-registry/postgres/init/012-s19-settlement-projections.sql
COPY infra/s1-registry/postgres/init/013-s22-intelligence-products.sql /opt/hvac/repo/infra/s1-registry/postgres/init/013-s22-intelligence-products.sql
COPY infra/connectivity/postgres/init/001-s09-connectivity.sql /opt/hvac/repo/infra/connectivity/postgres/init/001-s09-connectivity.sql
COPY infra/connectivity/postgres/init/002-s11-command-reply-evidence.sql /opt/hvac/repo/infra/connectivity/postgres/init/002-s11-command-reply-evidence.sql
COPY services/rule-runtime-service/migrations/001_s20_rule_runtime_core.sql /opt/hvac/repo/services/rule-runtime-service/migrations/001_s20_rule_runtime_core.sql
COPY infra/s1-registry/postgres/init/013-s23-maintenance-retirement.sql /opt/hvac/repo/infra/s1-registry/postgres/init/013-s23-maintenance-retirement.sql

COPY infra/s2-telemetry/postgres/init/001-s2-telemetry-baseline.sql /opt/hvac/repo/infra/s2-telemetry/postgres/init/001-s2-telemetry-baseline.sql
COPY infra/s2-telemetry/postgres/init/002-s2-telemetry-runtime-snapshot.sql /opt/hvac/repo/infra/s2-telemetry/postgres/init/002-s2-telemetry-runtime-snapshot.sql
COPY infra/s2-telemetry/postgres/init/003-s2-telemetry-ingest.sql /opt/hvac/repo/infra/s2-telemetry/postgres/init/003-s2-telemetry-ingest.sql
COPY infra/s2-telemetry/postgres/init/004-s2-telemetry-history-outbox.sql /opt/hvac/repo/infra/s2-telemetry/postgres/init/004-s2-telemetry-history-outbox.sql
COPY infra/s2-telemetry/postgres/init/004a-s2-tenant-scope.sql /opt/hvac/repo/infra/s2-telemetry/postgres/init/004a-s2-tenant-scope.sql
COPY infra/s2-telemetry/postgres/init/004b-s2-point-identity.sql /opt/hvac/repo/infra/s2-telemetry/postgres/init/004b-s2-point-identity.sql
COPY infra/s2-telemetry/postgres/init/004c-s2-counter-semantics.sql /opt/hvac/repo/infra/s2-telemetry/postgres/init/004c-s2-counter-semantics.sql
COPY infra/s2-telemetry/postgres/init/004d-s2-redis-latest.sql /opt/hvac/repo/infra/s2-telemetry/postgres/init/004d-s2-redis-latest.sql
COPY infra/s2-telemetry/postgres/init/005-s2-realtime-backend.sql /opt/hvac/repo/infra/s2-telemetry/postgres/init/005-s2-realtime-backend.sql

COPY services/command-service/migrations/001_s3_command_runtime.sql /opt/hvac/repo/services/command-service/migrations/001_s3_command_runtime.sql
COPY services/command-service/migrations/002_s3_target_runtime.sql /opt/hvac/repo/services/command-service/migrations/002_s3_target_runtime.sql
COPY services/command-service/migrations/003_s3_tenant_scope.sql /opt/hvac/repo/services/command-service/migrations/003_s3_tenant_scope.sql
COPY services/command-service/migrations/004_s3_command_point_identity.sql /opt/hvac/repo/services/command-service/migrations/004_s3_command_point_identity.sql
COPY services/command-service/migrations/005_s11_edge_execution_evidence.sql /opt/hvac/repo/services/command-service/migrations/005_s11_edge_execution_evidence.sql
COPY services/alarm-service/migrations/001_s4_alarm_runtime.sql /opt/hvac/repo/services/alarm-service/migrations/001_s4_alarm_runtime.sql
COPY services/alarm-service/migrations/002_s4_alarm_lifecycle.sql /opt/hvac/repo/services/alarm-service/migrations/002_s4_alarm_lifecycle.sql
COPY services/alarm-service/migrations/003_s4_tenant_scope.sql /opt/hvac/repo/services/alarm-service/migrations/003_s4_tenant_scope.sql
COPY services/alarm-service/migrations/004_s4_event_provenance.sql /opt/hvac/repo/services/alarm-service/migrations/004_s4_event_provenance.sql
COPY services/alarm-service/migrations/005_s13_alarm_orthogonal.sql /opt/hvac/repo/services/alarm-service/migrations/005_s13_alarm_orthogonal.sql
COPY services/alarm-service/migrations/006_s14_alarm_evaluator.sql /opt/hvac/repo/services/alarm-service/migrations/006_s14_alarm_evaluator.sql
COPY services/work-order-service/migrations/001_s5_work_order_runtime.sql /opt/hvac/repo/services/work-order-service/migrations/001_s5_work_order_runtime.sql
COPY services/work-order-service/migrations/002_s5_work_order_create_assignment.sql /opt/hvac/repo/services/work-order-service/migrations/002_s5_work_order_create_assignment.sql
COPY services/work-order-service/migrations/003_s5_work_order_lifecycle.sql /opt/hvac/repo/services/work-order-service/migrations/003_s5_work_order_lifecycle.sql
COPY services/work-order-service/migrations/004_s5_tenant_scope.sql /opt/hvac/repo/services/work-order-service/migrations/004_s5_tenant_scope.sql
COPY services/work-order-service/migrations/005_s19_alarm_links.sql /opt/hvac/repo/services/work-order-service/migrations/005_s19_alarm_links.sql

RUN ! grep -R -E 'local-only|fixture-only' /opt/hvac/repo \
    && chmod 0555 /opt/hvac/migrations/run-phase1-migrations.sh /opt/hvac/migrations/verify-phase1-schema.sh
ENTRYPOINT ["/opt/hvac/migrations/run-phase1-migrations.sh"]
