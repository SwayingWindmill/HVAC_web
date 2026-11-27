---
date: 2026-11-27
category: 修复
---
- 修复优化服务在 ClickHouse 检查运行评估记录去重时误用仅具写入权限账号（`optimization_service_writer`）导致 `ACCESS_DENIED` 的问题，改为使用最小权限只读账号（`optimization_service_reader`）执行存在性查询，确保各站点每小时只读编排建议（`STAGING_ONLY`）正常落库。
