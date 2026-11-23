/*
  跨站点告警规则基准模板库与受控下发版本账本（#953）：
  1. 新增 alarm_runtime.rule_baseline_template 与不可变发布表 alarm_runtime.rule_baseline_template_release；
  2. 新增批量下发审计批次表 site_template_rollout_batch 与逐站点明细表 site_template_rollout_item；
  3. 扩展 alarm_runtime.site_rule_bundle_revision.source 允许 TEMPLATE_ROLLOUT 来源并保留强制 RLS 隔离。
*/
BEGIN;

SET LOCAL ROLE s4_alarm_migrator;

CREATE TABLE IF NOT EXISTS alarm_runtime.rule_baseline_template (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  template_key text NOT NULL CHECK (length(btrim(template_key)) BETWEEN 1 AND 128),
  display_name text NOT NULL CHECK (length(btrim(display_name)) BETWEEN 1 AND 256),
  description text NOT NULL DEFAULT '' CHECK (length(description) <= 2048),
  device_type text NOT NULL CHECK (length(btrim(device_type)) BETWEEN 1 AND 64),
  thing_model_code text NOT NULL DEFAULT '' CHECK (length(thing_model_code) <= 128),
  alarm_type text NOT NULL CHECK (length(btrim(alarm_type)) BETWEEN 1 AND 128),
  rule_kind text NOT NULL DEFAULT 'THRESHOLD' CHECK (rule_kind IN ('THRESHOLD', 'SLOPE', 'WINDOW', 'EXPRESSION')),
  default_severity text NOT NULL CHECK (default_severity IN ('INFO', 'WARNING', 'MINOR', 'MAJOR', 'CRITICAL')),
  default_execution_target text NOT NULL DEFAULT 'CLOUD' CHECK (default_execution_target IN ('CLOUD', 'EDGE', 'CLOUD_AND_EDGE')),
  bound_site_ids jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(bound_site_ids) = 'array'),
  latest_version integer NOT NULL DEFAULT 0 CHECK (latest_version >= 0),
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'ARCHIVED')),
  created_by text NOT NULL CHECK (length(btrim(created_by)) BETWEEN 1 AND 256),
  updated_by text NOT NULL CHECK (length(btrim(updated_by)) BETWEEN 1 AND 256),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, template_key)
);

CREATE INDEX IF NOT EXISTS rule_baseline_template_tenant_status_idx
  ON alarm_runtime.rule_baseline_template (tenant_id, status, updated_at DESC);

COMMENT ON TABLE alarm_runtime.rule_baseline_template IS '跨站点共用的物模型告警规则基准模板目录，定义默认条件模型、严重程度与云边执行位置。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template.id IS '基准模板主键标识（UUIDv7）。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template.tenant_id IS '所属租户标识，用于行级安全隔离（RLS）。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template.template_key IS '模板全局唯一编码，与站点派生规则的 templateKey 对齐关联。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template.display_name IS '控制台展示的模板中文名称。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template.description IS '模板用途、适用冷站设备约束及默认值说明。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template.device_type IS '适用的设备类型编码（如 CHILLER、CHILLED_PUMP、COOLING_TOWER）。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template.thing_model_code IS '可选关联的物模型模板编码。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template.alarm_type IS '模板派生规则的标准告警类型编码。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template.rule_kind IS '规则算法类型：THRESHOLD、SLOPE、WINDOW 或 EXPRESSION。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template.default_severity IS '模板默认告警严重程度：INFO、WARNING、MINOR、MAJOR 或 CRITICAL。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template.default_execution_target IS '默认执行位置：CLOUD、EDGE 或 CLOUD_AND_EDGE。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template.bound_site_ids IS '已绑定同步范围的目标站点 UUID 数组（JSONB）。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template.latest_version IS '当前最新已发布的基准模板版本号，未发布为 0。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template.status IS '模板生命周期状态：ACTIVE（启用）或 ARCHIVED（归档）。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template.created_by IS '创建模板的操作者主体标识。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template.updated_by IS '最近修改模板的操作者主体标识。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template.created_at IS '模板首次创建 UTC 时间戳。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template.updated_at IS '模板元信息或绑定范围最近更新 UTC 时间戳。';

ALTER TABLE alarm_runtime.rule_baseline_template ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.rule_baseline_template FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS rule_baseline_template_migrator_all ON alarm_runtime.rule_baseline_template;
CREATE POLICY rule_baseline_template_migrator_all ON alarm_runtime.rule_baseline_template
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS rule_baseline_template_runtime_all ON alarm_runtime.rule_baseline_template;
CREATE POLICY rule_baseline_template_runtime_all ON alarm_runtime.rule_baseline_template
  FOR ALL TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON TABLE alarm_runtime.rule_baseline_template FROM PUBLIC, s4_alarm_runtime;
GRANT SELECT, INSERT, UPDATE ON TABLE alarm_runtime.rule_baseline_template TO s4_alarm_runtime;

CREATE TABLE IF NOT EXISTS alarm_runtime.rule_baseline_template_release (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  template_id uuid NOT NULL REFERENCES alarm_runtime.rule_baseline_template(id) ON DELETE CASCADE,
  version integer NOT NULL CHECK (version > 0),
  rule_spec jsonb NOT NULL CHECK (jsonb_typeof(rule_spec) = 'object'),
  change_note text NOT NULL DEFAULT '' CHECK (length(change_note) <= 500),
  published_by text NOT NULL CHECK (length(btrim(published_by)) BETWEEN 1 AND 256),
  published_by_name text NOT NULL DEFAULT '' CHECK (length(published_by_name) <= 256),
  published_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, template_id, version)
);

CREATE INDEX IF NOT EXISTS rule_baseline_template_release_lookup_idx
  ON alarm_runtime.rule_baseline_template_release (tenant_id, template_id, version DESC);

DROP TRIGGER IF EXISTS rule_baseline_template_release_immutable ON alarm_runtime.rule_baseline_template_release;
CREATE TRIGGER rule_baseline_template_release_immutable
  BEFORE UPDATE OR DELETE ON alarm_runtime.rule_baseline_template_release
  FOR EACH ROW EXECUTE FUNCTION alarm_runtime.reject_alarm_evaluator_history_mutation();

COMMENT ON TABLE alarm_runtime.rule_baseline_template_release IS '不可变的基准规则模板版本发布快照，记录每次基准参数升级的完整规则规格。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template_release.id IS '模板发布版本主键标识（UUIDv7）。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template_release.tenant_id IS '所属租户标识，用于行级安全隔离（RLS）。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template_release.template_id IS '关联的基准模板主键。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template_release.version IS '模板单调递增的整数版本号（从 1 开始）。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template_release.rule_spec IS '该版本基准规则完整规格 JSON（含阈值条件、触发模式、联动动作与执行目标）。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template_release.change_note IS '本次模板升级变更说明。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template_release.published_by IS '发布该版本的账号主体标识。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template_release.published_by_name IS '发布该版本的操作者展示名。';
COMMENT ON COLUMN alarm_runtime.rule_baseline_template_release.published_at IS '版本发布 UTC 时间戳。';

ALTER TABLE alarm_runtime.rule_baseline_template_release ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.rule_baseline_template_release FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS rule_baseline_template_release_migrator_all ON alarm_runtime.rule_baseline_template_release;
CREATE POLICY rule_baseline_template_release_migrator_all ON alarm_runtime.rule_baseline_template_release
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS rule_baseline_template_release_runtime_select ON alarm_runtime.rule_baseline_template_release;
CREATE POLICY rule_baseline_template_release_runtime_select ON alarm_runtime.rule_baseline_template_release
  FOR SELECT TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

DROP POLICY IF EXISTS rule_baseline_template_release_runtime_insert ON alarm_runtime.rule_baseline_template_release;
CREATE POLICY rule_baseline_template_release_runtime_insert ON alarm_runtime.rule_baseline_template_release
  FOR INSERT TO s4_alarm_runtime
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON TABLE alarm_runtime.rule_baseline_template_release FROM PUBLIC, s4_alarm_runtime;
GRANT SELECT, INSERT ON TABLE alarm_runtime.rule_baseline_template_release TO s4_alarm_runtime;

CREATE TABLE IF NOT EXISTS alarm_runtime.site_template_rollout_batch (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  template_id uuid NOT NULL REFERENCES alarm_runtime.rule_baseline_template(id) ON DELETE CASCADE,
  template_version integer NOT NULL CHECK (template_version > 0),
  target_site_ids jsonb NOT NULL CHECK (jsonb_typeof(target_site_ids) = 'array'),
  summary jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(summary) = 'object'),
  created_by text NOT NULL CHECK (length(btrim(created_by)) BETWEEN 1 AND 256),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS site_template_rollout_batch_lookup_idx
  ON alarm_runtime.site_template_rollout_batch (tenant_id, template_id, created_at DESC);

DROP TRIGGER IF EXISTS site_template_rollout_batch_immutable ON alarm_runtime.site_template_rollout_batch;
CREATE TRIGGER site_template_rollout_batch_immutable
  BEFORE UPDATE OR DELETE ON alarm_runtime.site_template_rollout_batch
  FOR EACH ROW EXECUTE FUNCTION alarm_runtime.reject_alarm_evaluator_history_mutation();

COMMENT ON TABLE alarm_runtime.site_template_rollout_batch IS '告警规则基准模板向多个目标站点受控下发的批次不可变记录。';
COMMENT ON COLUMN alarm_runtime.site_template_rollout_batch.id IS '模板批量下发批次主键标识（UUIDv7）。';
COMMENT ON COLUMN alarm_runtime.site_template_rollout_batch.tenant_id IS '所属租户标识，用于行级安全隔离（RLS）。';
COMMENT ON COLUMN alarm_runtime.site_template_rollout_batch.template_id IS '下发的基准模板标识。';
COMMENT ON COLUMN alarm_runtime.site_template_rollout_batch.template_version IS '本次下发采用的模板版本号。';
COMMENT ON COLUMN alarm_runtime.site_template_rollout_batch.target_site_ids IS '本次下发选定的目标站点 UUID 列表（JSONB）。';
COMMENT ON COLUMN alarm_runtime.site_template_rollout_batch.summary IS '本次批量下发的统计摘要 JSON（应用站点数、保留覆写数、冲突数）。';
COMMENT ON COLUMN alarm_runtime.site_template_rollout_batch.created_by IS '发起受控下发的操作者主体标识。';
COMMENT ON COLUMN alarm_runtime.site_template_rollout_batch.created_at IS '批量下发执行 UTC 时间戳。';

ALTER TABLE alarm_runtime.site_template_rollout_batch ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.site_template_rollout_batch FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS site_template_rollout_batch_migrator_all ON alarm_runtime.site_template_rollout_batch;
CREATE POLICY site_template_rollout_batch_migrator_all ON alarm_runtime.site_template_rollout_batch
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS site_template_rollout_batch_runtime_select ON alarm_runtime.site_template_rollout_batch;
CREATE POLICY site_template_rollout_batch_runtime_select ON alarm_runtime.site_template_rollout_batch
  FOR SELECT TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

DROP POLICY IF EXISTS site_template_rollout_batch_runtime_insert ON alarm_runtime.site_template_rollout_batch;
CREATE POLICY site_template_rollout_batch_runtime_insert ON alarm_runtime.site_template_rollout_batch
  FOR INSERT TO s4_alarm_runtime
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON TABLE alarm_runtime.site_template_rollout_batch FROM PUBLIC, s4_alarm_runtime;
GRANT SELECT, INSERT ON TABLE alarm_runtime.site_template_rollout_batch TO s4_alarm_runtime;

CREATE TABLE IF NOT EXISTS alarm_runtime.site_template_rollout_item (
  id uuid PRIMARY KEY,
  batch_id uuid NOT NULL REFERENCES alarm_runtime.site_template_rollout_batch(id) ON DELETE CASCADE,
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  bundle_revision bigint,
  status text NOT NULL CHECK (status IN ('APPLIED', 'SKIPPED', 'FAILED')),
  preserved_override_count integer NOT NULL DEFAULT 0 CHECK (preserved_override_count >= 0),
  conflict_count integer NOT NULL DEFAULT 0 CHECK (conflict_count >= 0),
  detail text NOT NULL DEFAULT '' CHECK (length(detail) <= 1024),
  applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS site_template_rollout_item_batch_idx
  ON alarm_runtime.site_template_rollout_item (tenant_id, batch_id, applied_at DESC);

DROP TRIGGER IF EXISTS site_template_rollout_item_immutable ON alarm_runtime.site_template_rollout_item;
CREATE TRIGGER site_template_rollout_item_immutable
  BEFORE UPDATE OR DELETE ON alarm_runtime.site_template_rollout_item
  FOR EACH ROW EXECUTE FUNCTION alarm_runtime.reject_alarm_evaluator_history_mutation();

COMMENT ON TABLE alarm_runtime.site_template_rollout_item IS '告警规则模板下发批次在单个站点上的执行结果、保留覆写数及生效版本明细。';
COMMENT ON COLUMN alarm_runtime.site_template_rollout_item.id IS '单站下发结果明细主键（UUIDv7）。';
COMMENT ON COLUMN alarm_runtime.site_template_rollout_item.batch_id IS '所属模板下发批次标识。';
COMMENT ON COLUMN alarm_runtime.site_template_rollout_item.tenant_id IS '所属租户标识，用于行级安全隔离（RLS）。';
COMMENT ON COLUMN alarm_runtime.site_template_rollout_item.site_id IS '目标站点标识。';
COMMENT ON COLUMN alarm_runtime.site_template_rollout_item.bundle_revision IS '本次下发后在该站点写入的最新规则包修订号（未变更则为空或原号）。';
COMMENT ON COLUMN alarm_runtime.site_template_rollout_item.status IS '单站下发状态：APPLIED（已更新）、SKIPPED（无变更跳过）或 FAILED（失败）。';
COMMENT ON COLUMN alarm_runtime.site_template_rollout_item.preserved_override_count IS '下发过程中保留的站点级个性化覆写字段数量。';
COMMENT ON COLUMN alarm_runtime.site_template_rollout_item.conflict_count IS '模板变更与站点覆写发生冲突并自动保留站点值的字段数量。';
COMMENT ON COLUMN alarm_runtime.site_template_rollout_item.detail IS '单站下发摘要或错误信息说明。';
COMMENT ON COLUMN alarm_runtime.site_template_rollout_item.applied_at IS '单站执行下发的 UTC 时间戳。';

ALTER TABLE alarm_runtime.site_template_rollout_item ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.site_template_rollout_item FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS site_template_rollout_item_migrator_all ON alarm_runtime.site_template_rollout_item;
CREATE POLICY site_template_rollout_item_migrator_all ON alarm_runtime.site_template_rollout_item
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS site_template_rollout_item_runtime_select ON alarm_runtime.site_template_rollout_item;
CREATE POLICY site_template_rollout_item_runtime_select ON alarm_runtime.site_template_rollout_item
  FOR SELECT TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

DROP POLICY IF EXISTS site_template_rollout_item_runtime_insert ON alarm_runtime.site_template_rollout_item;
CREATE POLICY site_template_rollout_item_runtime_insert ON alarm_runtime.site_template_rollout_item
  FOR INSERT TO s4_alarm_runtime
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON TABLE alarm_runtime.site_template_rollout_item FROM PUBLIC, s4_alarm_runtime;
GRANT SELECT, INSERT ON TABLE alarm_runtime.site_template_rollout_item TO s4_alarm_runtime;

ALTER TABLE alarm_runtime.site_rule_bundle_revision
  DROP CONSTRAINT IF EXISTS site_rule_bundle_revision_source_check;
ALTER TABLE alarm_runtime.site_rule_bundle_revision
  ADD CONSTRAINT site_rule_bundle_revision_source_check
  CHECK (source IN ('WEB_WORKBENCH', 'CLI_SYNC', 'ROLLBACK', 'TEMPLATE_ROLLOUT'));

COMMIT;
