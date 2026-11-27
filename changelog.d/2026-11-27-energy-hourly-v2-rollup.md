---
date: 2026-11-27
category: 修复
---
- 修复 `analytics.energy_hourly_facts_v2` 小时电量汇总仅覆盖单批 15 分钟增量且电费恒为 0 的缺陷：`telemetry-worker` 在投影和重算受影响小时时改为从 `analytics.energy_interval_facts` 回读该点位整小时全部可见区间重算总量，按 `analytics.tariff_price_windows` 分时电价窗口计算定点 `Decimal64(6)` 电度电费，并对清空或改期的旧维度键写入零区间墓碑行；同时新增 `--backfill-hourly-v2-since` 幂等回填命令用于补算历史小时电量与电费。
