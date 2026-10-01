# HVAC Monitor Surface Contract

**Status: PRODUCT / INTERACTION CONTRACT; SHADCN APPLICATION SYSTEM SELECTED**

## Job

HVAC Monitor 是站点实时工程调查工作区。它必须帮助用户：

- 理解当前 HVAC 系统关系与关键运行事实；
- 从异常进入空间定位和设备证据；
- 查看真实测量/聚合能流证据；
- 选择设备并连续检查状态、测点和告警；
- 进入联动分析、策略、能耗、告警和设备详情等后续工作流。

## Truth

- 工程拓扑来自真实资产/设备模型，不用示例节点补齐画面。
- 运行、连接、freshness、quality、alarm 保持独立。
- energy evidence 不用线宽、箭头或数字暗示未经验证的流量或能量分配。
- Inspector 不向操作员显示 raw enum、UUID、trace、revision。
- realtime connection state 不等于设备 connectivity。

## Shadcn Application composition

```text
Page intro + local actions
Compact summary Cards
Line Tabs
├── topology
├── anomaly
└── energy evidence
Workspace Card
├── X6 engineering canvas OR measured energy evidence
└── optional Context Inspector Card
Supporting evidence Cards
```

### Page intro

包含当前 site、页面标题、更新时间和真实本地动作。不要复刻旧 HVAC 01–08 顶部模块。

### Summary cards

只放当前调查真正需要的少量事实，例如运行设备数、活动告警、当前功率、COP、供回水温度。

### Tabs

Use **current shadcn line Tabs**. 三种 view 共用同一业务上下文，不构造三套互不相干的页面。

### Engineering workspace

- topology：固定工程空间使用 X6；
- anomaly：复用同一空间记忆并突出异常对象；
- energy：只展示 measured / aggregated evidence；
- X6 节点和边可以 domain-specific，但外部 chrome 使用 shadcn Card/Tabs/Button/Badge 语法。

### Context Inspector

连续调查需要时出现在工作区旁边。Inspector 是普通 shadcn-style evidence surface，不是旧 Ant Drawer 或旧 Control Desk panel。

### Supporting evidence

实时证据、当前告警、继续调查使用标准 Card grammar。是否显示、显示多少由任务价值决定，不把“Evidence Dock”作为强制全站术语。

## Route / state

- shareable view/search state 由 TanStack Router Search Params 拥有；
- Feature 接收业务化 `search + onSearchChange`，不自己绑定 File Route API；
- Query 拥有 server state；
- realtime patch 不能建立第二套事实 authority。

## Visual authority

当前视觉 authority：

- `DESIGN.md`
- `docs/design-system/shadcn-redesign-2026-09-13.md`
- current shadcn/ui
- satnaing/shadcn-admin application patterns

旧 `HVAC运行监控_01-08` is no longer the sole or automatic visual source of truth. Control Desk Canvas + Inspector + Evidence Dock composition is also superseded as a mandatory visual formula.
