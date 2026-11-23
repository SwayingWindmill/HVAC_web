\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE connectivity_migrator;

CREATE TABLE IF NOT EXISTS connectivity.gateway_waveform_rules (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(site_id)),
  gateway_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(gateway_id)),
  rule_name text NOT NULL CHECK (length(btrim(rule_name)) BETWEEN 1 AND 120),
  enabled boolean NOT NULL DEFAULT true,
  device_code text NOT NULL CHECK (length(btrim(device_code)) BETWEEN 1 AND 128),
  point_code text NOT NULL CHECK (length(btrim(point_code)) BETWEEN 1 AND 128),
  trigger_type text NOT NULL CHECK (trigger_type IN ('THRESHOLD_HIGH', 'THRESHOLD_LOW', 'DELTA_JUMP', 'TRIP_STATE')),
  threshold_value double precision NOT NULL,
  delta_window_seconds integer NOT NULL DEFAULT 5 CHECK (delta_window_seconds BETWEEN 1 AND 300),
  pre_seconds integer NOT NULL DEFAULT 30 CHECK (pre_seconds BETWEEN 5 AND 300),
  post_seconds integer NOT NULL DEFAULT 60 CHECK (post_seconds BETWEEN 5 AND 300),
  sample_rate_hz integer NOT NULL DEFAULT 1 CHECK (sample_rate_hz BETWEEN 1 AND 10),
  point_codes_json jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(point_codes_json) = 'array' AND octet_length(point_codes_json::text) <= 8192),
  cooldown_seconds integer NOT NULL DEFAULT 120 CHECK (cooldown_seconds BETWEEN 5 AND 3600),
  updated_by text NOT NULL CHECK (length(btrim(updated_by)) BETWEEN 1 AND 256),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (updated_at >= created_at)
);

CREATE INDEX IF NOT EXISTS gateway_waveform_rules_gateway_idx
  ON connectivity.gateway_waveform_rules (tenant_id, gateway_id, updated_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS connectivity.gateway_waveform_captures (
  capture_id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(capture_id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(site_id)),
  gateway_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(gateway_id)),
  rule_id uuid REFERENCES connectivity.gateway_waveform_rules(id) ON DELETE SET NULL,
  trigger_source text NOT NULL CHECK (trigger_source IN ('AUTO_RULE', 'MANUAL')),
  trigger_reason text NOT NULL CHECK (length(btrim(trigger_reason)) BETWEEN 1 AND 512),
  status text NOT NULL CHECK (status IN ('PENDING', 'COMPLETED', 'FAILED')),
  triggered_at timestamptz NOT NULL,
  window_start_at timestamptz NOT NULL,
  window_end_at timestamptz NOT NULL CHECK (window_end_at >= window_start_at),
  sample_rate_hz integer NOT NULL DEFAULT 1 CHECK (sample_rate_hz BETWEEN 1 AND 10),
  sample_count integer NOT NULL DEFAULT 0 CHECK (sample_count >= 0),
  samples_json jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(samples_json) = 'array' AND octet_length(samples_json::text) <= 4194304),
  requested_by text NOT NULL CHECK (length(btrim(requested_by)) BETWEEN 1 AND 256),
  error_message text CHECK (error_message IS NULL OR length(error_message) <= 1024),
  created_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  CHECK (completed_at IS NULL OR completed_at >= created_at)
);

CREATE INDEX IF NOT EXISTS gateway_waveform_captures_gateway_idx
  ON connectivity.gateway_waveform_captures (tenant_id, gateway_id, triggered_at DESC, capture_id DESC);

CREATE TABLE IF NOT EXISTS connectivity.gateway_host_metrics (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(site_id)),
  gateway_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(gateway_id)),
  collected_at timestamptz NOT NULL,
  cpu_percent double precision NOT NULL CHECK (cpu_percent >= 0 AND cpu_percent <= 100),
  memory_used_percent double precision NOT NULL CHECK (memory_used_percent >= 0 AND memory_used_percent <= 100),
  disk_used_percent double precision NOT NULL CHECK (disk_used_percent >= 0 AND disk_used_percent <= 100),
  uptime_seconds bigint NOT NULL CHECK (uptime_seconds >= 0),
  rs485_tx_frames bigint NOT NULL CHECK (rs485_tx_frames >= 0),
  rs485_rx_errors bigint NOT NULL CHECK (rs485_rx_errors >= 0),
  rs485_error_rate_ppm double precision NOT NULL CHECK (rs485_error_rate_ppm >= 0),
  mqtt_rtt_ms double precision NOT NULL CHECK (mqtt_rtt_ms >= 0),
  mqtt_tls_version text NOT NULL DEFAULT '' CHECK (length(mqtt_tls_version) <= 32),
  mqtt_reconnect_count bigint NOT NULL DEFAULT 0 CHECK (mqtt_reconnect_count >= 0),
  active_ring_points integer NOT NULL DEFAULT 0 CHECK (active_ring_points >= 0),
  ring_buffer_bytes bigint NOT NULL DEFAULT 0 CHECK (ring_buffer_bytes >= 0)
);

CREATE INDEX IF NOT EXISTS gateway_host_metrics_gateway_idx
  ON connectivity.gateway_host_metrics (tenant_id, gateway_id, collected_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS connectivity.gateway_diagnostics_audit (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(site_id)),
  gateway_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(gateway_id)),
  action text NOT NULL CHECK (action IN ('WAVEFORM_TRIGGER', 'LOG_BUNDLE_PULL')),
  actor_id text NOT NULL CHECK (length(btrim(actor_id)) BETWEEN 1 AND 256),
  max_bytes integer NOT NULL DEFAULT 0 CHECK (max_bytes >= 0),
  since_minutes integer NOT NULL DEFAULT 0 CHECK (since_minutes >= 0),
  returned_bytes integer NOT NULL DEFAULT 0 CHECK (returned_bytes >= 0),
  redacted_count integer NOT NULL DEFAULT 0 CHECK (redacted_count >= 0),
  status text NOT NULL CHECK (status IN ('SUCCEEDED', 'DENIED', 'FAILED')),
  detail text NOT NULL DEFAULT '' CHECK (length(detail) <= 1024),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS gateway_diagnostics_audit_gateway_idx
  ON connectivity.gateway_diagnostics_audit (tenant_id, gateway_id, created_at DESC, id DESC);

ALTER TABLE connectivity.gateway_waveform_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.gateway_waveform_rules FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS gateway_waveform_rules_migrator_all ON connectivity.gateway_waveform_rules;
CREATE POLICY gateway_waveform_rules_migrator_all ON connectivity.gateway_waveform_rules
  FOR ALL TO connectivity_migrator USING (true) WITH CHECK (true);
DROP POLICY IF EXISTS gateway_waveform_rules_tenant_scope ON connectivity.gateway_waveform_rules;
CREATE POLICY gateway_waveform_rules_tenant_scope ON connectivity.gateway_waveform_rules
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE connectivity.gateway_waveform_captures ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.gateway_waveform_captures FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS gateway_waveform_captures_migrator_all ON connectivity.gateway_waveform_captures;
CREATE POLICY gateway_waveform_captures_migrator_all ON connectivity.gateway_waveform_captures
  FOR ALL TO connectivity_migrator USING (true) WITH CHECK (true);
DROP POLICY IF EXISTS gateway_waveform_captures_tenant_scope ON connectivity.gateway_waveform_captures;
CREATE POLICY gateway_waveform_captures_tenant_scope ON connectivity.gateway_waveform_captures
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE connectivity.gateway_host_metrics ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.gateway_host_metrics FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS gateway_host_metrics_migrator_all ON connectivity.gateway_host_metrics;
CREATE POLICY gateway_host_metrics_migrator_all ON connectivity.gateway_host_metrics
  FOR ALL TO connectivity_migrator USING (true) WITH CHECK (true);
DROP POLICY IF EXISTS gateway_host_metrics_tenant_scope ON connectivity.gateway_host_metrics;
CREATE POLICY gateway_host_metrics_tenant_scope ON connectivity.gateway_host_metrics
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE connectivity.gateway_diagnostics_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.gateway_diagnostics_audit FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS gateway_diagnostics_audit_migrator_all ON connectivity.gateway_diagnostics_audit;
CREATE POLICY gateway_diagnostics_audit_migrator_all ON connectivity.gateway_diagnostics_audit
  FOR ALL TO connectivity_migrator USING (true) WITH CHECK (true);
DROP POLICY IF EXISTS gateway_diagnostics_audit_tenant_scope ON connectivity.gateway_diagnostics_audit;
CREATE POLICY gateway_diagnostics_audit_tenant_scope ON connectivity.gateway_diagnostics_audit
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON
  connectivity.gateway_waveform_rules,
  connectivity.gateway_waveform_captures,
  connectivity.gateway_host_metrics,
  connectivity.gateway_diagnostics_audit
FROM PUBLIC;

GRANT SELECT, INSERT, UPDATE, DELETE ON
  connectivity.gateway_waveform_rules,
  connectivity.gateway_waveform_captures,
  connectivity.gateway_host_metrics,
  connectivity.gateway_diagnostics_audit
TO connectivity_runtime;

COMMENT ON TABLE connectivity.gateway_waveform_rules IS '边缘网关高频波形抓拍触发规则表，配置异常跳机、喘振阈值或突变条件及前后回流窗口。';
COMMENT ON COLUMN connectivity.gateway_waveform_rules.id IS '抓拍触发规则主键标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_waveform_rules.tenant_id IS '所属租户标识（UUIDv7），用于行级安全隔离。';
COMMENT ON COLUMN connectivity.gateway_waveform_rules.site_id IS '所属冷站标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_waveform_rules.gateway_id IS '所属边缘网关设备标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_waveform_rules.rule_name IS '触发规则展示名称，便于运维识别故障抓拍条件。';
COMMENT ON COLUMN connectivity.gateway_waveform_rules.enabled IS '规则是否启用自动监听触发。';
COMMENT ON COLUMN connectivity.gateway_waveform_rules.device_code IS '被监测的南向设备本地编码。';
COMMENT ON COLUMN connectivity.gateway_waveform_rules.point_code IS '被监测触发判定的关键点位编码。';
COMMENT ON COLUMN connectivity.gateway_waveform_rules.trigger_type IS '触发判定类型：THRESHOLD_HIGH（高限）、THRESHOLD_LOW（低限）、DELTA_JUMP（突变差值）或 TRIP_STATE（跳机状态）。';
COMMENT ON COLUMN connectivity.gateway_waveform_rules.threshold_value IS '触发阈值或突变量基准数值。';
COMMENT ON COLUMN connectivity.gateway_waveform_rules.delta_window_seconds IS '判定单位时间突变时所采用的时间窗长度（秒，1 至 300）。';
COMMENT ON COLUMN connectivity.gateway_waveform_rules.pre_seconds IS '触发点之前向前保留的秒级高频波形回流时长（秒，5 至 300）。';
COMMENT ON COLUMN connectivity.gateway_waveform_rules.post_seconds IS '触发点之后继续采集回流的高频波形时长（秒，5 至 300）。';
COMMENT ON COLUMN connectivity.gateway_waveform_rules.sample_rate_hz IS '瞬态波形环形缓存与回流采样频率（Hz，1 至 10）。';
COMMENT ON COLUMN connectivity.gateway_waveform_rules.point_codes_json IS '连带回流的多通道监控点位编码清单 JSON 数组。';
COMMENT ON COLUMN connectivity.gateway_waveform_rules.cooldown_seconds IS '两次自动触发之间的最小冷却间隔（秒，5 至 3600）。';
COMMENT ON COLUMN connectivity.gateway_waveform_rules.updated_by IS '最近修改或启用规则的操作人标识。';
COMMENT ON COLUMN connectivity.gateway_waveform_rules.created_at IS '规则创建时间（UTC）。';
COMMENT ON COLUMN connectivity.gateway_waveform_rules.updated_at IS '规则最近更新时间（UTC）。';

COMMENT ON TABLE connectivity.gateway_waveform_captures IS '边缘网关回流的秒级瞬态高频波形抓拍任务与样本存档表，独立保存且不污染常规能耗计费与在线率统计。';
COMMENT ON COLUMN connectivity.gateway_waveform_captures.capture_id IS '高频波形抓拍任务主键标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_waveform_captures.tenant_id IS '所属租户标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_waveform_captures.site_id IS '所属冷站标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_waveform_captures.gateway_id IS '执行抓拍回流的边缘网关设备标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_waveform_captures.rule_id IS '关联自动触发规则标识；手动抓拍时为 NULL。';
COMMENT ON COLUMN connectivity.gateway_waveform_captures.trigger_source IS '抓拍触发来源：AUTO_RULE（规则自动触发）或 MANUAL（运维手动触发）。';
COMMENT ON COLUMN connectivity.gateway_waveform_captures.trigger_reason IS '触发本次波形抓拍的具体原因说明。';
COMMENT ON COLUMN connectivity.gateway_waveform_captures.status IS '抓拍任务执行状态：PENDING（等待回流）、COMPLETED（已完成回流）或 FAILED（失败）。';
COMMENT ON COLUMN connectivity.gateway_waveform_captures.triggered_at IS '故障事件触发或手动下发抓拍的基准时间戳（UTC）。';
COMMENT ON COLUMN connectivity.gateway_waveform_captures.window_start_at IS '波形回流窗口起始时间（UTC）。';
COMMENT ON COLUMN connectivity.gateway_waveform_captures.window_end_at IS '波形回流窗口结束时间（UTC）。';
COMMENT ON COLUMN connectivity.gateway_waveform_captures.sample_rate_hz IS '本次回流波形的实际采样频率（Hz）。';
COMMENT ON COLUMN connectivity.gateway_waveform_captures.sample_count IS '回流样本点总条数。';
COMMENT ON COLUMN connectivity.gateway_waveform_captures.samples_json IS '秒级高频瞬态采样点位序列 JSON 数组，包含时间戳与多点位原始值。';
COMMENT ON COLUMN connectivity.gateway_waveform_captures.requested_by IS '发起抓拍的操作人或自动规则主体标识。';
COMMENT ON COLUMN connectivity.gateway_waveform_captures.error_message IS '抓拍或回流失败时的错误摘要说明。';
COMMENT ON COLUMN connectivity.gateway_waveform_captures.created_at IS '抓拍任务创建时间（UTC）。';
COMMENT ON COLUMN connectivity.gateway_waveform_captures.completed_at IS '抓拍波形回流完成时间戳（UTC）。';

COMMENT ON TABLE connectivity.gateway_host_metrics IS '边缘网关宿主机资源与南向串口、MQTT 链路自监控健康指标表。';
COMMENT ON COLUMN connectivity.gateway_host_metrics.id IS '自监控快照记录主键标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_host_metrics.tenant_id IS '所属租户标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_host_metrics.site_id IS '所属冷站标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_host_metrics.gateway_id IS '上报自监控指标的边缘网关设备标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_host_metrics.collected_at IS '边缘网关采集宿主及链路指标的 UTC 时间戳。';
COMMENT ON COLUMN connectivity.gateway_host_metrics.cpu_percent IS '网关宿主机 CPU 占用百分比（0 至 100）。';
COMMENT ON COLUMN connectivity.gateway_host_metrics.memory_used_percent IS '网关宿主机内存占用百分比（0 至 100）。';
COMMENT ON COLUMN connectivity.gateway_host_metrics.disk_used_percent IS '网关存储分区磁盘使用率百分比（0 至 100）。';
COMMENT ON COLUMN connectivity.gateway_host_metrics.uptime_seconds IS '网关边缘守护进程连续运行时长（秒）。';
COMMENT ON COLUMN connectivity.gateway_host_metrics.rs485_tx_frames IS 'RS485 总线累计发送请求帧总数。';
COMMENT ON COLUMN connectivity.gateway_host_metrics.rs485_rx_errors IS 'RS485 总线累计校验失败、超时或异常帧计数。';
COMMENT ON COLUMN connectivity.gateway_host_metrics.rs485_error_rate_ppm IS 'RS485 串口通信误码率（百万分比 PPM）。';
COMMENT ON COLUMN connectivity.gateway_host_metrics.mqtt_rtt_ms IS 'MQTT TLS 控制与保活通道最近测得的往返延迟（毫秒）。';
COMMENT ON COLUMN connectivity.gateway_host_metrics.mqtt_tls_version IS '当前 MQTT 会话协商使用的 TLS 协议版本（例如 TLSv1.3）。';
COMMENT ON COLUMN connectivity.gateway_host_metrics.mqtt_reconnect_count IS '网关 MQTT 客户端累计重连次数。';
COMMENT ON COLUMN connectivity.gateway_host_metrics.active_ring_points IS '边缘本地高频环形缓冲当前跟踪的点位数量。';
COMMENT ON COLUMN connectivity.gateway_host_metrics.ring_buffer_bytes IS '边缘本地高频环形缓冲当前占用的内存字节数。';

COMMENT ON TABLE connectivity.gateway_diagnostics_audit IS '网关远程受控诊断操作与进程日志按需拉取审计明细表（严禁开放任意交互 Shell）。';
COMMENT ON COLUMN connectivity.gateway_diagnostics_audit.id IS '远程诊断审计记录主键标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_diagnostics_audit.tenant_id IS '所属租户标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_diagnostics_audit.site_id IS '所属冷站标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_diagnostics_audit.gateway_id IS '目标边缘网关设备标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_diagnostics_audit.action IS '远程诊断动作类型：WAVEFORM_TRIGGER（手动波形抓拍）或 LOG_BUNDLE_PULL（受控进程日志拉取）。';
COMMENT ON COLUMN connectivity.gateway_diagnostics_audit.actor_id IS '发起远程诊断的操作主体标识。';
COMMENT ON COLUMN connectivity.gateway_diagnostics_audit.max_bytes IS '本次请求允许的最大返回字节上限（仅日志拉取适用）。';
COMMENT ON COLUMN connectivity.gateway_diagnostics_audit.since_minutes IS '本次请求追溯的日志时间范围（分钟）。';
COMMENT ON COLUMN connectivity.gateway_diagnostics_audit.returned_bytes IS '实际返回给运维端脱敏日志包的字节数。';
COMMENT ON COLUMN connectivity.gateway_diagnostics_audit.redacted_count IS '在网关或服务端完成敏感凭据脱敏替换的片段计数。';
COMMENT ON COLUMN connectivity.gateway_diagnostics_audit.status IS '操作执行结论：SUCCEEDED（成功）、DENIED（权限拒绝）或 FAILED（执行失败）。';
COMMENT ON COLUMN connectivity.gateway_diagnostics_audit.detail IS '本次诊断动作的说明或拦截拒绝原因摘要。';
COMMENT ON COLUMN connectivity.gateway_diagnostics_audit.created_at IS '审计事件写入时间戳（UTC）。';

RESET ROLE;

COMMIT;
