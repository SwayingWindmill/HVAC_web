# Dashboard Surface Contract

**Status: PRODUCT / INTERACTION CONTRACT; SHADCN APPLICATION SYSTEM SELECTED**

## Job

Dashboard 是站点运营概览与值班入口。它帮助用户快速回答：

- 当前是否有值得优先关注的运行问题；
- 核心站点事实是什么；
- 哪些告警和设备事项优先；
- 数据是否足够可信；
- 下一步应该进入哪个工作流。

## Truth

- 总览直接读取各事实的 owner（遥测快照与推送、energy-series、告警、工单、Registry），不再有 SiteDashboardSummary（ADR 0019）。
- Dashboard Overview 只能展示已有 owner-backed 数据，不能补造趋势、收益或设备状态。
- 告警 deep link 必须基于可唯一匹配的真实 Alarm。
- connectivity、freshness、quality、runtime 不得合并为不透明 health score。
- unknown / unavailable / not integrated / missing 不得显示为 0 或 healthy。

## Shadcn Application composition

```text
Page intro + local actions
4 concise summary Cards
Run posture / device evidence           Priority handling
Optimization opportunity                Data confidence        Continue workflows
```

### Page intro

包含：

- 当前 site；
- 页面标题；
- 最后更新时间 / site timezone；
- 少量高价值本地动作。

不建立 boxed PageHeader。

### Summary Cards

只用于权威事实，例如当前功率、今日用能、COP、设备可用率。禁止伪造 sparkline 或比较趋势。

### Run posture

显示透明的当前判断与构成证据。它不是算法不透明的总健康分。

### Priority handling

优先展示真实活动告警和设备 attention。列表项必须能进入真实下游调查工作流。

### Supporting cards

优化机会、数据可信度、继续处理使用当前 shadcn Card / Badge / Button 语法。

## Interaction

- Dashboard 不是控制页面；危险控制不直接从摘要 Card 发起。
- 页面允许 document-level Surface scroll。
- 路由上下文必须保持 site continuity。
- 快捷入口不得丢失真实权限边界。

## Visual authority

当前视觉 authority 是：

- `DESIGN.md`
- `docs/design-system/shadcn-redesign-2026-09-13.md`
- current shadcn/ui
- satnaing/shadcn-admin application patterns

旧 Dashboard 图片和 Control Desk Operational Home composition are no longer an authoritative visual target.
