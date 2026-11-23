---
date: 2026-11-23
category: 新增
---
- 新增跨站点告警规则基准模板库与受控下发工作台（`/system?tab=governance&section=alarm-templates`）：支持按设备类型/物模型维护基准告警规则、发布不可变模板版本、绑定冷站同步范围、执行多站点差异预检（自动保留站点个性 `overriddenFields` 并高亮冲突），并在批次下发后按 `TEMPLATE_ROLLOUT` 记入各站规则包版本账本。
- 在告警规则页（`/operations/alarms?tab=rules`）新增「模板偏离核对」抽屉，支持对比单站派生规则与模板最新基准参数，并一键重置回模板默认值。
