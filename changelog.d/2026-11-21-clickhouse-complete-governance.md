---
date: 2026-11-21
category: 修复
pr: 925
---
- 收口 ClickHouse 全链路存储治理、冷归档保留期与 Native TCP 批量写入（覆盖 `infra/telemetry/clickhouse/init/025-clickhouse-complete-governance.sql`、`modules/telemetry`、`modules/metric`、`services/settlement-service`、`cmd/telemetry-worker`、`cmd/metric-worker`、`deploy/platform/phase1/` 与 `scripts/`）：
  - **淘汰指标行级 `DELETE` 变更并补齐全表分区 TTL**：下线 `metric` 模块运行期逐行 `ALTER TABLE ... DELETE`（仅保留 PostgreSQL 清理墓碑标记），回收 `metric_engine_writer`/`metric_engine_runtime` 的 `ALTER DELETE` 权限，将 `analytics.metric_result_facts` 统一纳入 `ClickHouseRetentionPruner`（9 张保留清理表）与 `ColdArchiveExporter`（3 张先导出校验后清理表，默认 1825 天）并向 `analytics` 全部分区表补齐 `ttl_only_drop_parts = 1` 1825 天整块清理 TTL。
  - **`observations` 升级 `ReplacingMergeTree` 与设备前置排序键**：通过原子 `EXCHANGE TABLES` 将 `telemetry_history.observations` 重建为 `ReplacingMergeTree(received_at)`、排序键改为 `(tenant_id, site_id, device_id, telemetry_key, point_id, sensor_id, sampled_at)` 并压缩 `unit` 为 `LowCardinality(Nullable(String))`，同步保持物化视图与计数器视图 `Nullable(String)` 兼容；在 `analytics.metric_result_facts` 新增 `metric_binding_id_bf` 跳数索引，并让 `settlement-service` 查询自动带入主键前导列 `metric_id`。
  - **支持 ClickHouse Native TCP `PrepareBatch` 列式写入**：引入 `github.com/ClickHouse/clickhouse-go/v2` 实现 `ClickHouseNativeHistorySink`（`:9000` 列式二进制批量写入 + LZ4 压缩 + 确定性 `insert_deduplication_token` + `Code: 252` `TOO_MANY_PARTS` 退避），配置 `TELEMETRY_CLICKHOUSE_NATIVE_ADDR`（可选 `TELEMETRY_CLICKHOUSE_CA`）即可在 `telemetry-worker` 与 `telemetry-history-projector` 启用，未配置时保持平滑回退 HTTP `JSONEachRow` 并补齐 Docker `:9000` 集成回归测试。
