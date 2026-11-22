\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE connectivity_migrator;

CREATE TABLE IF NOT EXISTS connectivity.ota_packages (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)) REFERENCES iam.tenants(id),
  component text NOT NULL CHECK (component IN ('EDGE_GATEWAY', 'FIRMWARE')),
  version text NOT NULL CHECK (length(btrim(version)) BETWEEN 1 AND 64),
  target_os text NOT NULL CHECK (target_os IN ('linux')),
  target_arch text NOT NULL CHECK (target_arch IN ('armv7l', 'arm64', 'amd64')),
  hardware_models jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(hardware_models) = 'array' AND octet_length(hardware_models::text) <= 4096),
  min_compatible_version text CHECK (min_compatible_version IS NULL OR length(btrim(min_compatible_version)) BETWEEN 1 AND 64),
  file_name text NOT NULL CHECK (length(btrim(file_name)) BETWEEN 1 AND 255),
  file_size_bytes bigint NOT NULL CHECK (file_size_bytes > 0 AND file_size_bytes <= 134217728),
  sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  signature_alg text NOT NULL DEFAULT 'ED25519' CHECK (signature_alg = 'ED25519'),
  signing_key_id text NOT NULL CHECK (length(btrim(signing_key_id)) BETWEEN 1 AND 128),
  signature_b64 text NOT NULL CHECK (length(btrim(signature_b64)) BETWEEN 16 AND 512),
  storage_rel_path text NOT NULL CHECK (length(btrim(storage_rel_path)) BETWEEN 1 AND 512),
  release_notes text NOT NULL DEFAULT '' CHECK (length(release_notes) <= 2000),
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'RETIRED')),
  uploaded_by uuid NOT NULL CHECK (connectivity.is_uuid_v7(uploaded_by)),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, component, version, target_os, target_arch),
  CHECK (updated_at >= created_at)
);

CREATE INDEX IF NOT EXISTS ota_packages_tenant_created_idx
  ON connectivity.ota_packages (tenant_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS connectivity.ota_campaigns (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)) REFERENCES iam.tenants(id),
  name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 160),
  package_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(package_id)) REFERENCES connectivity.ota_packages(id),
  target_site_ids jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(target_site_ids) = 'array' AND octet_length(target_site_ids::text) <= 16384),
  target_tags jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(target_tags) = 'object' AND octet_length(target_tags::text) <= 8192),
  canary_percent integer NOT NULL CHECK (canary_percent BETWEEN 1 AND 100),
  failure_rate_threshold_pct integer NOT NULL CHECK (failure_rate_threshold_pct BETWEEN 1 AND 100),
  health_check_timeout_sec integer NOT NULL DEFAULT 120 CHECK (health_check_timeout_sec BETWEEN 10 AND 1800),
  auto_promote_rollout boolean NOT NULL DEFAULT true,
  status text NOT NULL CHECK (status IN ('CANARY_RUNNING', 'ROLLOUT_RUNNING', 'PAUSED', 'COMPLETED', 'ROLLED_BACK', 'FAILED')),
  pause_reason text CHECK (pause_reason IS NULL OR length(btrim(pause_reason)) BETWEEN 1 AND 256),
  reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 1000),
  created_by uuid NOT NULL CHECK (connectivity.is_uuid_v7(created_by)),
  updated_by uuid NOT NULL CHECK (connectivity.is_uuid_v7(updated_by)),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  completed_at timestamptz,
  CHECK (updated_at >= created_at)
);

CREATE INDEX IF NOT EXISTS ota_campaigns_tenant_created_idx
  ON connectivity.ota_campaigns (tenant_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS connectivity.ota_device_tasks (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)) REFERENCES iam.tenants(id),
  campaign_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(campaign_id)) REFERENCES connectivity.ota_campaigns(id),
  package_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(package_id)) REFERENCES connectivity.ota_packages(id),
  gateway_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(gateway_id)),
  site_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(site_id)),
  wave text NOT NULL CHECK (wave IN ('CANARY', 'ROLLOUT')),
  desired_action text NOT NULL CHECK (desired_action IN ('APPLY', 'ROLLBACK')),
  stage text NOT NULL CHECK (stage IN ('PENDING', 'DISPATCHED', 'DOWNLOADING', 'VERIFYING', 'APPLYING', 'HEALTH_CHECKING', 'SUCCEEDED', 'FAILED', 'ROLLED_BACK', 'CANCELLED')),
  progress_pct integer NOT NULL DEFAULT 0 CHECK (progress_pct BETWEEN 0 AND 100),
  current_version text NOT NULL DEFAULT '' CHECK (length(current_version) <= 64),
  target_version text NOT NULL CHECK (length(btrim(target_version)) BETWEEN 1 AND 64),
  error_code text CHECK (error_code IS NULL OR length(error_code) <= 128),
  error_message text CHECK (error_message IS NULL OR length(error_message) <= 1024),
  dispatched_at timestamptz,
  last_reported_at timestamptz,
  finished_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (campaign_id, gateway_id),
  CHECK (updated_at >= created_at)
);

CREATE INDEX IF NOT EXISTS ota_device_tasks_campaign_wave_idx
  ON connectivity.ota_device_tasks (tenant_id, campaign_id, wave, stage, id);

CREATE INDEX IF NOT EXISTS ota_device_tasks_gateway_active_idx
  ON connectivity.ota_device_tasks (tenant_id, gateway_id, updated_at DESC, id DESC);

ALTER TABLE connectivity.ota_packages ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.ota_packages FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS ota_packages_migrator_all ON connectivity.ota_packages;
CREATE POLICY ota_packages_migrator_all ON connectivity.ota_packages
  FOR ALL TO connectivity_migrator
  USING (true) WITH CHECK (true);
DROP POLICY IF EXISTS ota_packages_tenant_scope ON connectivity.ota_packages;
CREATE POLICY ota_packages_tenant_scope ON connectivity.ota_packages
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE connectivity.ota_campaigns ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.ota_campaigns FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS ota_campaigns_migrator_all ON connectivity.ota_campaigns;
CREATE POLICY ota_campaigns_migrator_all ON connectivity.ota_campaigns
  FOR ALL TO connectivity_migrator
  USING (true) WITH CHECK (true);
DROP POLICY IF EXISTS ota_campaigns_tenant_scope ON connectivity.ota_campaigns;
CREATE POLICY ota_campaigns_tenant_scope ON connectivity.ota_campaigns
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE connectivity.ota_device_tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.ota_device_tasks FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS ota_device_tasks_migrator_all ON connectivity.ota_device_tasks;
CREATE POLICY ota_device_tasks_migrator_all ON connectivity.ota_device_tasks
  FOR ALL TO connectivity_migrator
  USING (true) WITH CHECK (true);
DROP POLICY IF EXISTS ota_device_tasks_tenant_scope ON connectivity.ota_device_tasks;
CREATE POLICY ota_device_tasks_tenant_scope ON connectivity.ota_device_tasks
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON connectivity.ota_packages, connectivity.ota_campaigns, connectivity.ota_device_tasks FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON connectivity.ota_packages, connectivity.ota_campaigns, connectivity.ota_device_tasks TO connectivity_runtime;

COMMENT ON TABLE connectivity.ota_packages IS '网关与固件 OTA 升级包清单，保存版本号、适用平台架构、SHA-256 摘要、Ed25519 签名与存储路径。';
COMMENT ON COLUMN connectivity.ota_packages.id IS '升级包主键标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.ota_packages.tenant_id IS '所属租户标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.ota_packages.component IS '升级组件类型：EDGE_GATEWAY（边缘守护进程）或 FIRMWARE（底层固件）。';
COMMENT ON COLUMN connectivity.ota_packages.version IS '升级包语义化版本号（SemVer 2.0）。';
COMMENT ON COLUMN connectivity.ota_packages.target_os IS '目标操作系统，当前固定为 linux。';
COMMENT ON COLUMN connectivity.ota_packages.target_arch IS '目标 CPU 架构：armv7l、arm64 或 amd64。';
COMMENT ON COLUMN connectivity.ota_packages.hardware_models IS '兼容硬件型号白名单数组（JSON）；为空数组时不限制型号。';
COMMENT ON COLUMN connectivity.ota_packages.min_compatible_version IS '允许直接升级的最低当前版本号；为空时不限制起始版本。';
COMMENT ON COLUMN connectivity.ota_packages.file_name IS '上传时的原始文件名。';
COMMENT ON COLUMN connectivity.ota_packages.file_size_bytes IS '升级包二进制文件字节数，最大 128 MiB。';
COMMENT ON COLUMN connectivity.ota_packages.sha256 IS '升级包内容的 64 位小写十六进制 SHA-256 校验摘要。';
COMMENT ON COLUMN connectivity.ota_packages.signature_alg IS '升级包签名算法标识，固定为 ED25519。';
COMMENT ON COLUMN connectivity.ota_packages.signing_key_id IS '签发本次升级包的信任公钥标识。';
COMMENT ON COLUMN connectivity.ota_packages.signature_b64 IS '升级包规范签名载荷的 Base64 编码 Ed25519 签名。';
COMMENT ON COLUMN connectivity.ota_packages.storage_rel_path IS '升级包二进制在存储根目录下的相对路径。';
COMMENT ON COLUMN connectivity.ota_packages.release_notes IS '升级包发布说明。';
COMMENT ON COLUMN connectivity.ota_packages.status IS '升级包状态：ACTIVE（可发布）或 RETIRED（已停用）。';
COMMENT ON COLUMN connectivity.ota_packages.uploaded_by IS '上传登记该升级包的操作员主体标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.ota_packages.created_at IS '升级包登记创建时间（UTC）。';
COMMENT ON COLUMN connectivity.ota_packages.updated_at IS '升级包记录最近修改时间（UTC）。';

COMMENT ON TABLE connectivity.ota_campaigns IS '网关 OTA 分批灰度发布任务配置与整体执行状态，包含失败率熔断阈值与暂停回滚状态。';
COMMENT ON COLUMN connectivity.ota_campaigns.id IS '发布任务主键标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.ota_campaigns.tenant_id IS '所属租户标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.ota_campaigns.name IS 'OTA 发布任务名称。';
COMMENT ON COLUMN connectivity.ota_campaigns.package_id IS '本次发布使用的目标升级包标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.ota_campaigns.target_site_ids IS '限定发布范围的站点 UUIDv7 数组（JSON）；为空时覆盖操作员有权限且匹配标签的全部站点网关。';
COMMENT ON COLUMN connectivity.ota_campaigns.target_tags IS '匹配网关及绑定资产有效标签的键值对过滤条件（JSON 对象）。';
COMMENT ON COLUMN connectivity.ota_campaigns.canary_percent IS '灰度阶段首批下发的网关比例（1 到 100）。';
COMMENT ON COLUMN connectivity.ota_campaigns.failure_rate_threshold_pct IS '触发自动熔断暂停的失败或回滚百分比阈值（1 到 100）。';
COMMENT ON COLUMN connectivity.ota_campaigns.health_check_timeout_sec IS '网关切换新版本后等待自检确认健康的超时秒数（10 到 1800）。';
COMMENT ON COLUMN connectivity.ota_campaigns.auto_promote_rollout IS '灰度批次全部成功后是否自动推进全量批次下发。';
COMMENT ON COLUMN connectivity.ota_campaigns.status IS '发布任务状态：CANARY_RUNNING、ROLLOUT_RUNNING、PAUSED、COMPLETED、ROLLED_BACK 或 FAILED。';
COMMENT ON COLUMN connectivity.ota_campaigns.pause_reason IS '任务进入 PAUSED 状态的原因（手动暂停、失败率超限熔断或等待推进全量）。';
COMMENT ON COLUMN connectivity.ota_campaigns.reason IS '创建或操作本次 OTA 发布任务的变更原因。';
COMMENT ON COLUMN connectivity.ota_campaigns.created_by IS '创建本次发布任务的操作员主体标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.ota_campaigns.updated_by IS '最近一次修改或操作发布任务的主体标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.ota_campaigns.created_at IS '发布任务创建时间（UTC）。';
COMMENT ON COLUMN connectivity.ota_campaigns.updated_at IS '发布任务状态最近更新时间（UTC）。';
COMMENT ON COLUMN connectivity.ota_campaigns.completed_at IS '发布任务进入终态的时间（UTC）；运行中或暂停时为空。';

COMMENT ON TABLE connectivity.ota_device_tasks IS '单台网关在一次 OTA 发布任务中的波次分配、阶段进度与结果上报记录。';
COMMENT ON COLUMN connectivity.ota_device_tasks.id IS '单机升级子任务主键标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.ota_device_tasks.tenant_id IS '所属租户标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.ota_device_tasks.campaign_id IS '所属 OTA 发布任务标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.ota_device_tasks.package_id IS '目标升级包标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.ota_device_tasks.gateway_id IS '目标网关设备标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.ota_device_tasks.site_id IS '网关所属站点标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.ota_device_tasks.wave IS '所属发布波次：CANARY（灰度批次）或 ROLLOUT（全量批次）。';
COMMENT ON COLUMN connectivity.ota_device_tasks.desired_action IS '期望网关执行的动作：APPLY（升级）或 ROLLBACK（回滚到上一槽位）。';
COMMENT ON COLUMN connectivity.ota_device_tasks.stage IS '当前执行阶段：PENDING、DISPATCHED、DOWNLOADING、VERIFYING、APPLYING、HEALTH_CHECKING、SUCCEEDED、FAILED、ROLLED_BACK 或 CANCELLED。';
COMMENT ON COLUMN connectivity.ota_device_tasks.progress_pct IS '当前阶段进度百分比（0 到 100）。';
COMMENT ON COLUMN connectivity.ota_device_tasks.current_version IS '网关最近一次上报的当前运行版本号。';
COMMENT ON COLUMN connectivity.ota_device_tasks.target_version IS '本次任务目标版本号。';
COMMENT ON COLUMN connectivity.ota_device_tasks.error_code IS '失败或健康检查触发自动回滚时的结构化错误码。';
COMMENT ON COLUMN connectivity.ota_device_tasks.error_message IS '失败或回滚原因的详细说明文本。';
COMMENT ON COLUMN connectivity.ota_device_tasks.dispatched_at IS '向网关下发升级或回滚指令的时间（UTC）。';
COMMENT ON COLUMN connectivity.ota_device_tasks.last_reported_at IS '最近一次收到网关 OTA 进度或结果上报的时间（UTC）。';
COMMENT ON COLUMN connectivity.ota_device_tasks.finished_at IS '单机任务进入终态（成功、失败、已回滚或取消）的时间（UTC）。';
COMMENT ON COLUMN connectivity.ota_device_tasks.created_at IS '单机任务创建时间（UTC）。';
COMMENT ON COLUMN connectivity.ota_device_tasks.updated_at IS '单机任务最近更新时间（UTC）。';

RESET ROLE;

COMMIT;
