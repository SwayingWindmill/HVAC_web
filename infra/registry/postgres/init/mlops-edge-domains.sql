/*
  扩展 MLOps 元数据域与端侧治理读写权限（#921）：
  1. 幂等建表（兼容 manifest.v1.json 未登记 009k-mlops-metadata-v2.sql 的新库）并扩展 owner_domain 枚举至 EDGE_CONTROL 与 EDGE_FDD
  2. 为 optimization_runtime 补充 6 张 MLOps 元数据表的 RLS 策略与 SELECT/INSERT/UPDATE/DELETE 权限
*/
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

CREATE TABLE IF NOT EXISTS core_registry.mlops_artifacts (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  owner_domain text NOT NULL CHECK (owner_domain IN ('FORECAST', 'OPTIMIZATION', 'EDGE_CONTROL', 'EDGE_FDD')),
  artifact_type text NOT NULL CHECK (artifact_type IN ('MODEL', 'DATASET', 'FEATURE_SET', 'EVALUATION', 'EXPLAINABILITY', 'OTHER')),
  owner_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(owner_version_id)),
  object_uri text NOT NULL CHECK (length(btrim(object_uri)) BETWEEN 1 AND 2048),
  checksum_sha256 text NOT NULL CHECK (checksum_sha256 ~ '^[a-f0-9]{64}$'),
  media_type text NOT NULL CHECK (length(btrim(media_type)) BETWEEN 1 AND 128),
  size_bytes bigint CHECK (size_bytes IS NULL OR size_bytes >= 0),
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, owner_domain, owner_version_id, artifact_type, checksum_sha256),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id)
);

CREATE TABLE IF NOT EXISTS core_registry.mlops_evaluations (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  owner_domain text NOT NULL CHECK (owner_domain IN ('FORECAST', 'OPTIMIZATION', 'EDGE_CONTROL', 'EDGE_FDD')),
  owner_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(owner_version_id)),
  evaluation_type text NOT NULL CHECK (evaluation_type IN ('OFFLINE', 'SHADOW', 'PRODUCTION', 'REGRESSION')),
  dataset_artifact_id uuid,
  metrics jsonb NOT NULL CHECK (jsonb_typeof(metrics) = 'object'),
  outcome text NOT NULL CHECK (outcome IN ('PASSED', 'FAILED', 'INCONCLUSIVE')),
  evaluator text NOT NULL CHECK (length(btrim(evaluator)) BETWEEN 1 AND 256),
  evaluated_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, dataset_artifact_id) REFERENCES core_registry.mlops_artifacts(tenant_id, id)
);

CREATE TABLE IF NOT EXISTS core_registry.mlops_approvals (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  owner_domain text NOT NULL CHECK (owner_domain IN ('FORECAST', 'OPTIMIZATION', 'EDGE_CONTROL', 'EDGE_FDD')),
  owner_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(owner_version_id)),
  evaluation_id uuid,
  decision text NOT NULL CHECK (decision IN ('APPROVED', 'REJECTED')),
  actor_type text NOT NULL CHECK (actor_type IN ('USER', 'WORKLOAD')),
  actor_id text NOT NULL CHECK (length(btrim(actor_id)) BETWEEN 1 AND 256),
  reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 2048),
  policy_revision text NOT NULL CHECK (length(btrim(policy_revision)) BETWEEN 1 AND 256),
  decided_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, evaluation_id) REFERENCES core_registry.mlops_evaluations(tenant_id, id)
);

CREATE TABLE IF NOT EXISTS core_registry.mlops_deployment_metadata (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  owner_domain text NOT NULL CHECK (owner_domain IN ('FORECAST', 'OPTIMIZATION', 'EDGE_CONTROL', 'EDGE_FDD')),
  owner_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(owner_version_id)),
  approval_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(approval_id)),
  environment text NOT NULL CHECK (environment IN ('SHADOW', 'PRODUCTION')),
  status text NOT NULL CHECK (status IN ('PLANNED', 'ACTIVE', 'RETIRED', 'ROLLED_BACK')),
  serving_ref text NOT NULL CHECK (length(btrim(serving_ref)) BETWEEN 1 AND 512),
  effective_from timestamptz NOT NULL,
  effective_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, approval_id) REFERENCES core_registry.mlops_approvals(tenant_id, id),
  CHECK (effective_to IS NULL OR effective_to > effective_from),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.mlops_drift_observations (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  deployment_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(deployment_id)),
  metric_code text NOT NULL CHECK (length(btrim(metric_code)) BETWEEN 1 AND 128),
  observed_value double precision NOT NULL,
  threshold_value double precision NOT NULL,
  drift_status text NOT NULL CHECK (drift_status IN ('NORMAL', 'WARNING', 'BREACHED')),
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(evidence) = 'object'),
  observed_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, deployment_id) REFERENCES core_registry.mlops_deployment_metadata(tenant_id, id)
);

CREATE TABLE IF NOT EXISTS core_registry.mlops_rollback_records (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  deployment_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(deployment_id)),
  from_owner_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(from_owner_version_id)),
  to_owner_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(to_owner_version_id)),
  reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 2048),
  actor_type text NOT NULL CHECK (actor_type IN ('USER', 'WORKLOAD')),
  actor_id text NOT NULL CHECK (length(btrim(actor_id)) BETWEEN 1 AND 256),
  status text NOT NULL CHECK (status IN ('REQUESTED', 'APPLIED', 'FAILED')),
  requested_at timestamptz NOT NULL,
  applied_at timestamptz,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, deployment_id) REFERENCES core_registry.mlops_deployment_metadata(tenant_id, id),
  CHECK ((status = 'APPLIED' AND applied_at IS NOT NULL) OR (status <> 'APPLIED')),
  CHECK (applied_at IS NULL OR applied_at >= requested_at)
);

DO $$
DECLARE
  tbl text;
  con_rec record;
BEGIN
  FOREACH tbl IN ARRAY ARRAY['mlops_artifacts', 'mlops_evaluations', 'mlops_approvals', 'mlops_deployment_metadata'] LOOP
    FOR con_rec IN
      SELECT conname
      FROM pg_constraint
      WHERE conrelid = format('core_registry.%I', tbl)::regclass
        AND contype = 'c'
        AND pg_get_constraintdef(oid) LIKE '%owner_domain%'
    LOOP
      EXECUTE format('ALTER TABLE core_registry.%I DROP CONSTRAINT IF EXISTS %I', tbl, con_rec.conname);
    END LOOP;
    EXECUTE format(
      'ALTER TABLE core_registry.%I ADD CONSTRAINT %I CHECK (owner_domain IN (''FORECAST'', ''OPTIMIZATION'', ''EDGE_CONTROL'', ''EDGE_FDD''))',
      tbl, tbl || '_owner_domain_check'
    );
  END LOOP;
END $$;

CREATE INDEX IF NOT EXISTS mlops_artifacts_owner_idx ON core_registry.mlops_artifacts (tenant_id, owner_domain, owner_version_id, artifact_type);
CREATE INDEX IF NOT EXISTS mlops_evaluations_owner_idx ON core_registry.mlops_evaluations (tenant_id, owner_domain, owner_version_id, evaluated_at DESC);
CREATE INDEX IF NOT EXISTS mlops_deployment_active_idx ON core_registry.mlops_deployment_metadata (tenant_id, owner_domain, owner_version_id, environment, status);
CREATE INDEX IF NOT EXISTS mlops_drift_deployment_idx ON core_registry.mlops_drift_observations (tenant_id, deployment_id, observed_at DESC);
CREATE INDEX IF NOT EXISTS mlops_rollback_deployment_idx ON core_registry.mlops_rollback_records (tenant_id, deployment_id, requested_at DESC);

ALTER TABLE core_registry.mlops_artifacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_artifacts FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_evaluations ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_evaluations FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_approvals ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_approvals FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_deployment_metadata ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_deployment_metadata FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_drift_observations ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_drift_observations FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_rollback_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_rollback_records FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS mlops_artifacts_migrator_all ON core_registry.mlops_artifacts;
CREATE POLICY mlops_artifacts_migrator_all ON core_registry.mlops_artifacts FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);
DROP POLICY IF EXISTS mlops_evaluations_migrator_all ON core_registry.mlops_evaluations;
CREATE POLICY mlops_evaluations_migrator_all ON core_registry.mlops_evaluations FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);
DROP POLICY IF EXISTS mlops_approvals_migrator_all ON core_registry.mlops_approvals;
CREATE POLICY mlops_approvals_migrator_all ON core_registry.mlops_approvals FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);
DROP POLICY IF EXISTS mlops_deployment_metadata_migrator_all ON core_registry.mlops_deployment_metadata;
CREATE POLICY mlops_deployment_metadata_migrator_all ON core_registry.mlops_deployment_metadata FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);
DROP POLICY IF EXISTS mlops_drift_observations_migrator_all ON core_registry.mlops_drift_observations;
CREATE POLICY mlops_drift_observations_migrator_all ON core_registry.mlops_drift_observations FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);
DROP POLICY IF EXISTS mlops_rollback_records_migrator_all ON core_registry.mlops_rollback_records;
CREATE POLICY mlops_rollback_records_migrator_all ON core_registry.mlops_rollback_records FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS mlops_artifacts_runtime_scope ON core_registry.mlops_artifacts;
CREATE POLICY mlops_artifacts_runtime_scope ON core_registry.mlops_artifacts FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
DROP POLICY IF EXISTS mlops_evaluations_runtime_scope ON core_registry.mlops_evaluations;
CREATE POLICY mlops_evaluations_runtime_scope ON core_registry.mlops_evaluations FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
DROP POLICY IF EXISTS mlops_approvals_runtime_scope ON core_registry.mlops_approvals;
CREATE POLICY mlops_approvals_runtime_scope ON core_registry.mlops_approvals FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
DROP POLICY IF EXISTS mlops_deployment_metadata_runtime_scope ON core_registry.mlops_deployment_metadata;
CREATE POLICY mlops_deployment_metadata_runtime_scope ON core_registry.mlops_deployment_metadata FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
DROP POLICY IF EXISTS mlops_drift_observations_runtime_scope ON core_registry.mlops_drift_observations;
CREATE POLICY mlops_drift_observations_runtime_scope ON core_registry.mlops_drift_observations FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
DROP POLICY IF EXISTS mlops_rollback_records_runtime_scope ON core_registry.mlops_rollback_records;
CREATE POLICY mlops_rollback_records_runtime_scope ON core_registry.mlops_rollback_records FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));

DROP POLICY IF EXISTS mlops_artifacts_optimization_all ON core_registry.mlops_artifacts;
CREATE POLICY mlops_artifacts_optimization_all ON core_registry.mlops_artifacts FOR ALL TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
DROP POLICY IF EXISTS mlops_evaluations_optimization_all ON core_registry.mlops_evaluations;
CREATE POLICY mlops_evaluations_optimization_all ON core_registry.mlops_evaluations FOR ALL TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
DROP POLICY IF EXISTS mlops_approvals_optimization_all ON core_registry.mlops_approvals;
CREATE POLICY mlops_approvals_optimization_all ON core_registry.mlops_approvals FOR ALL TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
DROP POLICY IF EXISTS mlops_deployment_metadata_optimization_all ON core_registry.mlops_deployment_metadata;
CREATE POLICY mlops_deployment_metadata_optimization_all ON core_registry.mlops_deployment_metadata FOR ALL TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
DROP POLICY IF EXISTS mlops_drift_observations_optimization_all ON core_registry.mlops_drift_observations;
CREATE POLICY mlops_drift_observations_optimization_all ON core_registry.mlops_drift_observations FOR ALL TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
DROP POLICY IF EXISTS mlops_rollback_records_optimization_all ON core_registry.mlops_rollback_records;
CREATE POLICY mlops_rollback_records_optimization_all ON core_registry.mlops_rollback_records FOR ALL TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));

REVOKE ALL ON core_registry.mlops_artifacts, core_registry.mlops_evaluations, core_registry.mlops_approvals,
  core_registry.mlops_deployment_metadata, core_registry.mlops_drift_observations, core_registry.mlops_rollback_records FROM PUBLIC;
GRANT SELECT ON core_registry.mlops_artifacts, core_registry.mlops_evaluations, core_registry.mlops_approvals,
  core_registry.mlops_deployment_metadata, core_registry.mlops_drift_observations, core_registry.mlops_rollback_records TO s1_core_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON core_registry.mlops_artifacts, core_registry.mlops_evaluations, core_registry.mlops_approvals,
  core_registry.mlops_deployment_metadata, core_registry.mlops_drift_observations, core_registry.mlops_rollback_records TO optimization_runtime;

COMMENT ON TABLE core_registry.mlops_artifacts IS 'AI 与端侧算法模型及超参产物登记表，覆盖 FORECAST、OPTIMIZATION、EDGE_CONTROL 与 EDGE_FDD。';
COMMENT ON COLUMN core_registry.mlops_artifacts.id IS '产物记录主键（UUIDv7）。';
COMMENT ON COLUMN core_registry.mlops_artifacts.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.mlops_artifacts.site_id IS '适用站点标识（为空表示租户级产物）。';
COMMENT ON COLUMN core_registry.mlops_artifacts.owner_domain IS '归属算法域：FORECAST、OPTIMIZATION、EDGE_CONTROL 或 EDGE_FDD。';
COMMENT ON COLUMN core_registry.mlops_artifacts.artifact_type IS '产物类型（MODEL、DATASET、FEATURE_SET、EVALUATION、EXPLAINABILITY 或 OTHER）。';
COMMENT ON COLUMN core_registry.mlops_artifacts.owner_version_id IS '算法版本逻辑标识（UUIDv7）。';
COMMENT ON COLUMN core_registry.mlops_artifacts.object_uri IS '产物存储引用地址或端侧配置包标识。';
COMMENT ON COLUMN core_registry.mlops_artifacts.checksum_sha256 IS '产物内容规范化 SHA-256 校验摘要（64 位十六进制）。';
COMMENT ON COLUMN core_registry.mlops_artifacts.media_type IS '产物媒体类型。';
COMMENT ON COLUMN core_registry.mlops_artifacts.size_bytes IS '产物字节大小。';
COMMENT ON COLUMN core_registry.mlops_artifacts.metadata IS '端侧模型与控制超参结构化元数据及签名信息 JSON 对象。';
COMMENT ON COLUMN core_registry.mlops_artifacts.created_at IS '记录创建时间（UTC）。';

COMMENT ON TABLE core_registry.mlops_evaluations IS '模型离线基准、影子对照及回归评估结果记录表。';
COMMENT ON COLUMN core_registry.mlops_evaluations.id IS '评估记录主键（UUIDv7）。';
COMMENT ON COLUMN core_registry.mlops_evaluations.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.mlops_evaluations.site_id IS '所属站点标识（可为空）。';
COMMENT ON COLUMN core_registry.mlops_evaluations.owner_domain IS '归属算法域：FORECAST、OPTIMIZATION、EDGE_CONTROL 或 EDGE_FDD。';
COMMENT ON COLUMN core_registry.mlops_evaluations.owner_version_id IS '被评估版本标识（UUIDv7）。';
COMMENT ON COLUMN core_registry.mlops_evaluations.evaluation_type IS '评估类型：OFFLINE、SHADOW、PRODUCTION 或 REGRESSION。';
COMMENT ON COLUMN core_registry.mlops_evaluations.dataset_artifact_id IS '关联基准数据集或产物记录主键。';
COMMENT ON COLUMN core_registry.mlops_evaluations.metrics IS '评估偏差与样本数量等结构化指标 JSON 对象。';
COMMENT ON COLUMN core_registry.mlops_evaluations.outcome IS '评估结论：PASSED、FAILED 或 INCONCLUSIVE。';
COMMENT ON COLUMN core_registry.mlops_evaluations.evaluator IS '执行评估的工作负载或操作员标识。';
COMMENT ON COLUMN core_registry.mlops_evaluations.evaluated_at IS '评估完成业务时间（UTC）。';
COMMENT ON COLUMN core_registry.mlops_evaluations.created_at IS '记录创建时间（UTC）。';

COMMENT ON TABLE core_registry.mlops_approvals IS '模型与端侧超参发布审批记录表。';
COMMENT ON COLUMN core_registry.mlops_approvals.id IS '审批记录主键（UUIDv7）。';
COMMENT ON COLUMN core_registry.mlops_approvals.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.mlops_approvals.site_id IS '适用站点标识（可为空）。';
COMMENT ON COLUMN core_registry.mlops_approvals.owner_domain IS '归属算法域：FORECAST、OPTIMIZATION、EDGE_CONTROL 或 EDGE_FDD。';
COMMENT ON COLUMN core_registry.mlops_approvals.owner_version_id IS '审批对应版本标识（UUIDv7）。';
COMMENT ON COLUMN core_registry.mlops_approvals.evaluation_id IS '审批依据的评估记录主键（可为空）。';
COMMENT ON COLUMN core_registry.mlops_approvals.decision IS '审批结果：APPROVED 或 REJECTED。';
COMMENT ON COLUMN core_registry.mlops_approvals.actor_type IS '审批执行主体类型：USER 或 WORKLOAD。';
COMMENT ON COLUMN core_registry.mlops_approvals.actor_id IS '审批主体标识。';
COMMENT ON COLUMN core_registry.mlops_approvals.reason IS '审批意见或原因说明。';
COMMENT ON COLUMN core_registry.mlops_approvals.policy_revision IS '审批时生效的策略版本。';
COMMENT ON COLUMN core_registry.mlops_approvals.decided_at IS '审批决定生效时间（UTC）。';
COMMENT ON COLUMN core_registry.mlops_approvals.created_at IS '记录创建时间（UTC）。';

COMMENT ON TABLE core_registry.mlops_deployment_metadata IS '云端与边缘模型版本部署状态表。';
COMMENT ON COLUMN core_registry.mlops_deployment_metadata.id IS '部署记录主键（UUIDv7）。';
COMMENT ON COLUMN core_registry.mlops_deployment_metadata.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.mlops_deployment_metadata.site_id IS '部署目标站点标识（可为空）。';
COMMENT ON COLUMN core_registry.mlops_deployment_metadata.owner_domain IS '归属算法域：FORECAST、OPTIMIZATION、EDGE_CONTROL 或 EDGE_FDD。';
COMMENT ON COLUMN core_registry.mlops_deployment_metadata.owner_version_id IS '部署版本标识（UUIDv7）。';
COMMENT ON COLUMN core_registry.mlops_deployment_metadata.approval_id IS '前置审批记录主键（UUIDv7）。';
COMMENT ON COLUMN core_registry.mlops_deployment_metadata.environment IS '部署环境：SHADOW（影子模式）或 PRODUCTION（生产生效）。';
COMMENT ON COLUMN core_registry.mlops_deployment_metadata.status IS '部署状态：PLANNED、ACTIVE、RETIRED 或 ROLLED_BACK。';
COMMENT ON COLUMN core_registry.mlops_deployment_metadata.serving_ref IS '目标运行网关或服务路由引用。';
COMMENT ON COLUMN core_registry.mlops_deployment_metadata.effective_from IS '部署生效起始时间（UTC）。';
COMMENT ON COLUMN core_registry.mlops_deployment_metadata.effective_to IS '部署下线或失效时间（UTC，未失效时为空）。';
COMMENT ON COLUMN core_registry.mlops_deployment_metadata.revision IS '部署单调版本号。';
COMMENT ON COLUMN core_registry.mlops_deployment_metadata.created_at IS '记录创建时间（UTC）。';
COMMENT ON COLUMN core_registry.mlops_deployment_metadata.updated_at IS '记录最近更新时间（UTC）。';

COMMENT ON TABLE core_registry.mlops_drift_observations IS '在线部署偏差监测与阈值突破观测记录表。';
COMMENT ON COLUMN core_registry.mlops_drift_observations.id IS '观测记录主键（UUIDv7）。';
COMMENT ON COLUMN core_registry.mlops_drift_observations.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.mlops_drift_observations.site_id IS '所属站点标识（可为空）。';
COMMENT ON COLUMN core_registry.mlops_drift_observations.deployment_id IS '关联部署记录主键（UUIDv7）。';
COMMENT ON COLUMN core_registry.mlops_drift_observations.metric_code IS '观测偏差指标编码。';
COMMENT ON COLUMN core_registry.mlops_drift_observations.observed_value IS '实际观测偏差值。';
COMMENT ON COLUMN core_registry.mlops_drift_observations.threshold_value IS '允许最大偏差阈值。';
COMMENT ON COLUMN core_registry.mlops_drift_observations.drift_status IS '偏差状态：NORMAL、WARNING 或 BREACHED。';
COMMENT ON COLUMN core_registry.mlops_drift_observations.evidence IS '观测证据详情 JSON 对象。';
COMMENT ON COLUMN core_registry.mlops_drift_observations.observed_at IS '观测采集业务时间（UTC）。';
COMMENT ON COLUMN core_registry.mlops_drift_observations.created_at IS '记录创建时间（UTC）。';

COMMENT ON TABLE core_registry.mlops_rollback_records IS '模型部署一键或自动回退审计记录表。';
COMMENT ON COLUMN core_registry.mlops_rollback_records.id IS '回退记录主键（UUIDv7）。';
COMMENT ON COLUMN core_registry.mlops_rollback_records.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.mlops_rollback_records.site_id IS '所属站点标识（可为空）。';
COMMENT ON COLUMN core_registry.mlops_rollback_records.deployment_id IS '被回退部署记录主键（UUIDv7）。';
COMMENT ON COLUMN core_registry.mlops_rollback_records.from_owner_version_id IS '回退前运行版本标识。';
COMMENT ON COLUMN core_registry.mlops_rollback_records.to_owner_version_id IS '回退目标历史版本标识。';
COMMENT ON COLUMN core_registry.mlops_rollback_records.reason IS '触发回退的原因说明。';
COMMENT ON COLUMN core_registry.mlops_rollback_records.actor_type IS '回退发起主体类型：USER 或 WORKLOAD。';
COMMENT ON COLUMN core_registry.mlops_rollback_records.actor_id IS '回退发起主体标识。';
COMMENT ON COLUMN core_registry.mlops_rollback_records.status IS '回退执行状态：REQUESTED、APPLIED 或 FAILED。';
COMMENT ON COLUMN core_registry.mlops_rollback_records.requested_at IS '发起回退请求时间（UTC）。';
COMMENT ON COLUMN core_registry.mlops_rollback_records.applied_at IS '回退完成生效时间（UTC）。';
COMMENT ON COLUMN core_registry.mlops_rollback_records.created_at IS '记录创建时间（UTC）。';

RESET ROLE;

COMMIT;
