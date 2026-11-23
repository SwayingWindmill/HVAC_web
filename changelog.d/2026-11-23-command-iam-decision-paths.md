---
date: 2026-11-23
category: 修复
---
- 修复控制服务（`commandservice`）调用 IAM 决策接口的内部路径（由错误路径修正为 `/internal/v1/registry/decision` 与 `/internal/v1/command/decision`），并在 IAM 允许已认证调用方将自身 SPIFFE 身份声明为 `grantPresenter`（同时在默认配置中收录 `command-service`），解决 `GET /api/v1/commands` 与 `GET /api/v1/sites/{siteId}/control-state` 持续返回 `503 IAM_UNAVAILABLE` 的问题。
