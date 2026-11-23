---
date: 2026-11-23
category: 修复
---
- 修复网关覆盖率心跳上报在遥测观察记录达到百万级时因全表扫描与超时预算过短触发 `mqtt_coverage_report_failed` 的问题：
  - 为 `telemetry_runtime.source_observations` 与 `telemetry_runtime.mqtt_runtime_events` 增加网关断联排查索引（`telemetry-coverage-disconnect-lookup-indexes.sql`），避免每次心跳全表扫描。
  - 将 `modules/telemetry` 覆盖率接口的逐设备锁和逐行写入合并为批量操作，并将 `connectivity` 在线心跳超时预算从 `5s` 对齐到 `15s`（同步放宽 `telemetry-worker` 写超时到 `20s`），同时为失败日志补充非敏 `failure_class`、`duration_ms`、`device_count` 与 `gateway_count` 定位字段。
