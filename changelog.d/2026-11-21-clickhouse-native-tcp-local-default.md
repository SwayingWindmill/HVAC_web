---
date: 2026-11-21
category: 修复
---
- 自动开启 Phase 1 本机 ClickHouse Native TCP 批量写入并补充部署失败容器日志诊断（覆盖 `scripts/phase1-deployment-tier.ts`、`deploy/platform/phase1/` 与 `modules/telemetry`）：
  - **本机模式默认开启 `:9000` Native TCP**：`resolvePhase1ClickHouseNativeAddr` 在 `PHASE1_CLICKHOUSE_MODE=local` 且未显式关闭时默认注入 `clickhouse:9000`（支持设置 `PHASE1_CLICKHOUSE_NATIVE_ADDR=off` 平滑回退 HTTP `JSONEachRow`），外部模式保持显式配置后再启用；`compose.yaml` 为 `telemetry-worker` 补齐 `TELEMETRY_CLICKHOUSE_NATIVE_ADDR`、`TELEMETRY_CLICKHOUSE_CA` 与 `TELEMETRY_CLICKHOUSE_PASSWORD` 透传，并在 `modules/telemetry` 集成测试中改用最小权限写角色 `telemetry_history_writer` 直连 `:9000` 验证 `PrepareBatch`。
  - **`deploy.sh` 增加启动失败容器日志诊断**：Compose 拉起失败时自动打印核心服务状态及异常容器最近 60 行日志，便于快速定位启动失败原因。
