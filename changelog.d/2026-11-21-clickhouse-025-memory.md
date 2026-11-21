---
date: 2026-11-21
category: 修复
---
- 修复 ClickHouse `025-clickhouse-complete-governance.sql` 在大规模历史遥测数据下回填 `observations_v2` 触发 1 GiB 内存上限（`MEMORY_LIMIT_EXCEEDED`）的问题：将 `DROP VIEW` 延后至 `EXCHANGE TABLES` 之前，并为 `INSERT INTO ... SELECT` 追加单线程低内存块与外部排序参数，避免已有千万级遥测库执行 `025` 迁移中断。
