---
date: 2026-11-21
category: 新增
---
- 为站点告警规则新增因果依赖 DAG 拓扑画布与在线编排工作台（`AlarmRuleDagCanvas`、`AlarmRuleEditorSheet`、`AlarmRuleConditionEditor`），支持规则新建、阈值/斜率/回差/多点表达式与联动动作编辑、未发布草稿实时试算，以及变更差异预检和一键热发布。
- 为网关与告警模块新增 `PUT /api/v1/alarm-rules`、`POST /api/v1/alarm-rules/diff`、`GET /api/v1/alarm-rules/revisions` 与 `POST /api/v1/alarm-rules/revisions/{revision}/rollback` 接口及 `alarm_runtime.site_rule_bundle_revision` 不可变版本账本，采用站点顾问锁与乐观锁版本号（`409 Conflict`）并发控制，保留未改动规则的持续计时与采样状态并在规避回退时清理停用规则残留计时。
- 为遥测告警计算引擎新增无锁 `atomic.Pointer` 站点规则快照缓存（`siteRuleCache`），并登记 `hvac_alarm_rule_publishes_total` 发布结果指标。
