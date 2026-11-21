---
date: 2026-11-21
category: 修复
---
- 为 ClickHouse `025-clickhouse-complete-governance.sql` 补登记 PR #937 之前的线上等价校验和（`f3fd1233...`），避免已应用旧版 `025` 的生产服务器在部署包含 `#937` 的版本时因哈希漂移中止迁移；同时将 `clickhouse-migration-list.tsv` 纳入 `npm run migrations:freeze:check` 冻结门禁，拦截后续原地修改已注册 ClickHouse 迁移文件。
