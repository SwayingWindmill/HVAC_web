---
date: 2026-11-23
category: 修复
---

- **修复站点重复注册与生产滚动发布时 `observation_coverage_check` 约束冲突**：将 `scripts/site-onboarding.mjs` 中 `telemetry_runtime.observation_coverage` 的冲突策略对齐 `modules/telemetry/pkg/telemetry/registry_sync.go` 改为 `ON CONFLICT (device_id) DO NOTHING;`，保留实时离线/断流覆盖状态且避免在 `continuous_since` 为 `NULL` 时触发检查约束失败；同步在 `scripts/phase1-central-plant-simulator.mjs` 的 `DO UPDATE` 中以 `COALESCE` 补全 `continuous_since`，并补充回归测试。
