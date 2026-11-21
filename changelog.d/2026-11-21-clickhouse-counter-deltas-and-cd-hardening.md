---
date: 2026-11-21
category: 修复
---
- 优化 ClickHouse `telemetry_history.counter_deltas_from` 视图为单遍线性扫描（避免多次自连接导致的 25 次基表扫描、内存激增与查询超时堆积），并补全 `analytics.energy_hourly_states` 只读权限以消除工况锚点投影 `Code: 497` 鉴权报错。
- 在 ClickHouse 默认查询配置开启 `cancel_http_readonly_queries_on_client_close=1` 并在能耗 ClickHouse 读取客户端去重 `tenants`/`sites`/`points` 过滤数组，防止客户端超时断开后查询在服务端残留堆积。
- 加固生产发布脚本中 `connectivity-spool` 命名卷及镜像层 `65532:65532` 权限，并自动检测 ClickHouse 单文件挂载 XML（如 `query-log-profile.xml`）的 inode 漂移并按需重建容器使配置生效。
