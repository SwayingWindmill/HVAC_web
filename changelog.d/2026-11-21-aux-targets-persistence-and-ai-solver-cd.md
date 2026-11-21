---
date: 2026-11-21
category: 新增
---
- 持久化优化建议的多回路协同目标并在发布打包流程纳管外部 Python 优化服务：在 `core_registry.optimization_recommendations` 新增 `auxiliary_targets` 数组字段并在服务读写中完整落库；在网页「当前建议」页面条件展示「协同变频与风机建议」面板，呈现冷冻水泵、冷却水泵与冷却塔风机建议目标；同时将 `ai-solver-service`（`python` / `optional`）接入 `deploy/platform/phase1/cd/` 发布清单与增量打包脚本。
