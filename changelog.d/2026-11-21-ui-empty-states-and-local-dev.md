---
date: 2026-11-21
category: 修复
---
- 修复能耗分析空态图表报错、月度报告无数据空态提示与本地调试启动体验问题：
  - **能耗流向空态防护**：在 `EnergyDestination` 的 `EnergyFlow` / `FlowNodeShape` 增加有限坐标守卫并在无流向链路时显示「所选时段暂无设备用电流向数据」，消除无数据日触发的 Recharts `<text x="NaN">` 控制台报错。
  - **月度报告骨架屏与空月提示**：为 `MonthlyReport` 与 `LiveMonthlyReport` 补充贴合纸面报告结构的加载骨架卡片；无计量数据时展示「所选月份暂无计量数据，待冷站表计读数接入后自动汇总。」状态提示，并在 `EnergyTrend` 无数据时展示空态说明而非空白图表。
  - **本地 `command` mTLS 与 AI 服务条件启动**：为 `command-service` 身份补齐 `serverAuth,clientAuth` 双用途扩展并纳入 `issue-workload-identities.mjs`；本地 `local:up` 检测到未配置 `DEEPSEEK_API_KEY` 时自动跳过启动 `operations-agent-service`，避免无密钥时容器循环重启。
