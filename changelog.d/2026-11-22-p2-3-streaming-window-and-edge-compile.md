---
date: 2026-11-22
category: 新增
---
- 新增告警规则流式时间窗口统计聚合条件（`WINDOW` 规则类型与 `WINDOW_AGG` 条件），支持滑动窗口（`SLIDING`）与滚动窗口（`TUMBLING`）下的平均值、最小值、最大值、求和、样本计数、标准差及 `P95`/`P99` 分位数评估，并配备防重复采样签名去重与单通道有界环形样本缓冲。
- 新增云边统一规则中间表示（`libs/edgecontrol`）与站点边缘编译接口（`POST /api/v1/alarm-rules/compile-edge`），支持按 `executionTarget`（`CLOUD` / `EDGE` / `CLOUD_AND_EDGE`）与准入校验（条件类型、保护动作与上游 DAG 依赖）自动生成带 SHA-256 摘要校验的 `EdgeRuleBundle`，并将 `edgeRuleBundle` 接入网关云边配置影子（`config_shadow.go` 与 `gateway-shadow-edge-rules.sql` 迁移）。
- 升级网关模拟器边缘运行态（`tools/eg8200-simulator`），在优先级 15 槽位启用边缘规则仲裁执行（严格低于优先级 10 硬件保护限值、高于优先级 50 断网自主控制与优先级 100 云端指令），支持断网期间本地规则评估、设备保护动作触发以及离网告警事实暂存重连续传；并在控制台新增流式窗口编辑器、执行位置标记与「边缘编译下发」工作流。
