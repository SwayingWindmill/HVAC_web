# 04 设备详情 — Workspace Brief

**Status: IMPLEMENTED / BROWSER REVIEWED**  
**Current route:** `/sites/$siteId/devices/$deviceId`  
**IA role:** “设备”Workspace 的 hidden durable detail。  
**Design inheritance:** 36 Surface 阶段已验收的 **07「设备详情」是当前完整设备详情的页面母体**。36→10 调整的是专业工作流的 owner 与出口，不重新设计 07 的主体信息层级。

## Accepted 36-Surface anatomy

```text
Device identity
↓
Independent State Strip
  运行状态
  连接状态
  数据新鲜度
  数据质量
↓
Primary investigation column
  当前运行
  当前注意事项
  最近观测
  工程点位
↓
Lower-weight side column
  对象关系
  设备资料
  专业出口
```

这是当前 detail route 的结构基线。

## 10-Workspace consolidation rules

36→10 后允许变化的是出口归属：

- 趋势证据进入“运行/趋势”owner；
- 告警与诊断进入对应事件调查 owner；
- 工单与验证进入工作 owner；
- 控制 / 策略 / 执行进入安全控制与自动化 owner；
- 数据质量、语义模型与集成治理进入系统 owner。

但上述职责重分配**不能**把完整 Device Detail 改成新的裸 section 页面、Dashboard Card wall、Tab 详情或另一套 Inspector。

## Visual hierarchy

- entity name 是唯一主标题；
- Independent State Strip 保持四个互相独立事实；
- `当前运行` 是主视觉 section；
- `当前注意事项`、`最近观测`、`工程点位`位于主调查列；
- `对象关系`、`设备资料`、`专业出口`位于低权重侧列；
- Semantic Card 可以用于这些完整业务 section；工程点位仍使用标准 DataTableBlock；
- 不使用 synthetic health score；
- 不把 point writability 解释为当前 control authority；
- 专业动作不在 entity header 与侧栏重复出现。

## Browser acceptance

- 首屏有 identity + Independent State Strip；
- 页面包含且只按上述 hierarchy 呈现：
  `当前运行 / 当前注意事项 / 最近观测 / 工程点位 / 对象关系 / 设备资料 / 专业出口`；
- 768px 下主调查列先于侧列堆叠，无横向 overflow；
- 工程点位保留 tablecn/TanStack 标准表格；
- 36→10 只改变出口目的地和职责归属，不改变完整详情的成熟页面母体。
