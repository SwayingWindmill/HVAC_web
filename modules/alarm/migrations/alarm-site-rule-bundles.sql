/*
  站点告警规则在线热发布版本账本：保存每次经规则工作台热发布、CLI 同步或版本回滚产生的不可变规则包快照、内容摘要、变更说明与发布统计，
  配合乐观锁版本号拦截并发覆盖并支持一键按版本号回滚恢复。
*/
BEGIN;

SET LOCAL ROLE s4_alarm_migrator;

CREATE TABLE IF NOT EXISTS alarm_runtime.site_rule_bundle_revision (
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  bundle_revision bigint NOT NULL CHECK (bundle_revision > 0),
  bundle_digest text NOT NULL CHECK (bundle_digest ~ '^[a-f0-9]{64}$'),
  change_note text NOT NULL DEFAULT '' CHECK (length(change_note) <= 500),
  source text NOT NULL CHECK (source IN ('WEB_WORKBENCH', 'CLI_SYNC', 'ROLLBACK')),
  rolled_back_from_revision bigint CHECK (rolled_back_from_revision IS NULL OR rolled_back_from_revision > 0),
  summary jsonb NOT NULL CHECK (jsonb_typeof(summary) = 'object'),
  rules_snapshot jsonb NOT NULL CHECK (jsonb_typeof(rules_snapshot) = 'array'),
  published_at timestamptz NOT NULL,
  published_by text NOT NULL CHECK (length(btrim(published_by)) BETWEEN 1 AND 256),
  published_by_name text NOT NULL DEFAULT '' CHECK (length(published_by_name) <= 256),
  PRIMARY KEY (tenant_id, site_id, bundle_revision)
);

CREATE INDEX IF NOT EXISTS site_rule_bundle_revision_latest_idx
  ON alarm_runtime.site_rule_bundle_revision (tenant_id, site_id, bundle_revision DESC);

CREATE TRIGGER site_rule_bundle_revision_immutable
  BEFORE UPDATE OR DELETE ON alarm_runtime.site_rule_bundle_revision
  FOR EACH ROW EXECUTE FUNCTION alarm_runtime.reject_alarm_evaluator_history_mutation();

COMMENT ON TABLE alarm_runtime.site_rule_bundle_revision IS '站点告警规则集发布版本快照账本，记录每次热发布、命令行同步或回滚操作生成的不可变规则快照与摘要。';
COMMENT ON COLUMN alarm_runtime.site_rule_bundle_revision.tenant_id IS '所属租户标识，用于行级租户隔离（RLS）。';
COMMENT ON COLUMN alarm_runtime.site_rule_bundle_revision.site_id IS '所属站点标识，与租户标识和发布版本号共同构成主键。';
COMMENT ON COLUMN alarm_runtime.site_rule_bundle_revision.bundle_revision IS '站点内单调递增的规则包发布版本号（从 1 开始，只增不改）。';
COMMENT ON COLUMN alarm_runtime.site_rule_bundle_revision.bundle_digest IS '按规则配置内容规范序列化后计算的 SHA-256 签名摘要，用于幂等判定与一致性核验。';
COMMENT ON COLUMN alarm_runtime.site_rule_bundle_revision.change_note IS '发布人填写的本次变更原因或回滚说明（最多 500 字符）。';
COMMENT ON COLUMN alarm_runtime.site_rule_bundle_revision.source IS '版本来源：WEB_WORKBENCH 表示网页工作台热发布，CLI_SYNC 表示规则文件同步，ROLLBACK 表示版本回滚。';
COMMENT ON COLUMN alarm_runtime.site_rule_bundle_revision.rolled_back_from_revision IS '若本版本由回滚产生，记录目标历史版本号；正常发布与同步为 NULL。';
COMMENT ON COLUMN alarm_runtime.site_rule_bundle_revision.summary IS '本次变更对站点生效规则的影响统计 JSON（added、changed、unchanged、retired 及对应规则 ID）。';
COMMENT ON COLUMN alarm_runtime.site_rule_bundle_revision.rules_snapshot IS '该发布版本下完整站点规则集与 assignmentId 的不可变 JSON 数组快照，支持完整回滚。';
COMMENT ON COLUMN alarm_runtime.site_rule_bundle_revision.published_at IS '本次规则版本发布生效的 UTC 时间戳。';
COMMENT ON COLUMN alarm_runtime.site_rule_bundle_revision.published_by IS '执行本次发布或回滚的操作主体标识。';
COMMENT ON COLUMN alarm_runtime.site_rule_bundle_revision.published_by_name IS '执行本次发布或回滚的操作人员展示名称，便于前端版本历史直接展示。';

ALTER TABLE alarm_runtime.site_rule_bundle_revision ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.site_rule_bundle_revision FORCE ROW LEVEL SECURITY;

CREATE POLICY site_rule_bundle_revision_migrator_all ON alarm_runtime.site_rule_bundle_revision
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);

CREATE POLICY site_rule_bundle_revision_runtime_select ON alarm_runtime.site_rule_bundle_revision
  FOR SELECT TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY site_rule_bundle_revision_runtime_insert ON alarm_runtime.site_rule_bundle_revision
  FOR INSERT TO s4_alarm_runtime
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON TABLE alarm_runtime.site_rule_bundle_revision FROM PUBLIC, s4_alarm_runtime;
GRANT SELECT, INSERT ON alarm_runtime.site_rule_bundle_revision TO s4_alarm_runtime;

RESET ROLE;

COMMIT;
