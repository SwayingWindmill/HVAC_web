---
date: 2026-11-23
category: 修复
---
- 修复总览页「今日用能」反复出现「今日能耗暂时读不到」（`POST /api/v1/analytics/energy-series` 返回 503）的慢性故障：
  - 在 `semantic/cube/model/cubes/energy_facts.yml` 消除 `period_end` 别名遮蔽并拆分缺表检测视图 `site_energy_missing`，恢复 ClickHouse 分区/主键裁剪并大幅减少子查询与扫描行数；
  - 将 ClickHouse 运行日志级别从默认 `trace` 调整为 `warning`，减少每条查询的日志开销；
  - 为 Go Cube 客户端补齐官方 `Continue wait` 有界轮询协议并记录 503 失败分类日志，同时为前端能耗查询补充有限退避重试。
