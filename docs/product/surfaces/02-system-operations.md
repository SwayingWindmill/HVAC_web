# 02 运行 Workspace — Current Implementation Brief

**Status: IMPLEMENTED / BROWSER-REVIEWED**
**Primary route:** `/sites/$siteId/operations`
**Workspace view state:** `?view=systems|comfort`
**Secondary routes:** `/sites/$siteId/operations/trends`, `/sites/$siteId/operations/control`

## 1. Responsibility

“运行”是站点 HVAC 的高频操作工作区。它负责回答：

- 系统现在如何运行；
- 哪些设备群正在运行、停止或状态未知；
- 当前关键过程量是什么；
- 数据是否可信、是否存在需要继续调查的运行事项；
- 当前对象下一步应该进入趋势、设备、诊断还是即时控制。

它不是站点 KPI Dashboard，也不是把旧的 04 / 05 / 08 / 25 四个页面简单并排。

## 2. 36 Surface 输入与重新分工

本 Workspace 以原 36 Surface 的业务责任和已经实现的领域代码为输入，但不照搬旧页面外壳。

| 原 Surface | 当前归属 | 实现裁决 |
| --- | --- | --- |
| 04 系统运行 | `运行 → 系统运行` | Workspace core。保留当前过程量、设备群和对象级 Inspector，去掉虚构的运行模式、设计区间和设备型式描述。 |
| 05 趋势分析 | Secondary durable route | 仍是完整 evidence workspace；从系统、设备或空间上下文进入，URL 为 `/operations/trends`。 |
| 08 舒适与室内环境 | `运行 → 空间与环境` | 合并为 peer view，共享站点上下文；不再占一级页面。 |
| 25 控制中心 | Contextual secondary route | 不再是一级页面；从已理解的系统/设备上下文进入 `/operations/control`，继续保持安全前置条件、authority、ACK/readback 语义边界。 |

旧的 `/comfort`、`/trends`、`/control` route implementation 已从 runtime route tree 移除，不保留兼容页面。

## 3. Workspace composition

```text
Shell: 运行
└─ Workspace view tabs
   ├─ 系统运行
   │  ├─ Site / timezone / update / data quality
   │  ├─ compact operational FactStrip
   │  ├─ current process facts
   │  ├─ equipment-group list
   │  └─ Context Inspector
   │     ├─ selected group state
   │     ├─ current power / running population
   │     ├─ control source when authoritative
   │     ├─ related alarm context
   │     └─ Trend / Device / Diagnosis / Control exits
   └─ 空间与环境
      ├─ environmental context
      ├─ thermal / IAQ capability view
      ├─ space ledger
      └─ space inspector

Secondary
├─ Advanced Trend Studio
└─ Immediate Control
```

## 4. Visual / component rules

- Shell 和 workspace view 使用 shadcn Tabs；没有第二套页面标题。
- 系统运行首屏不采用“5 个独立 KPI Card” Dashboard 模板；当前事实使用项目 `FactStrip`。
- engineering canvas 和 Inspector 使用平面 section/border hierarchy，不使用 Card → Card → Card 嵌套。
- 异常状态才使用高视觉权重；正常/未知事实保持 neutral。
- 不显示前端推断出来的“自动优化模式”“自动控制运行中”“设计区间正常”“循环稳定”等结论。
- 设备群类型描述只显示 owner 已提供的信息，不根据 group key 编造机型、系统角色或控制策略。
- Control Authority 未提供时显示“未提供/未知”，不能从在线状态或点位可写性推导。
- 窄屏保持同一业务任务；系统对象详情使用 Sheet，不产生水平滚动。

## 5. URL ownership

`/sites/:siteId/operations`

- `view=systems|comfort`: Workspace peer view。
- `group`: 系统运行中选中的设备群。
- `comfortView`: 空间与环境内部的 thermal / air-quality view。
- `q / area / data / inspect`: 空间环境 ledger 和 inspector 状态。

`/sites/:siteId/operations/trends`

- 承担 Surface 05 的 durable trend/evidence 工作。

`/sites/:siteId/operations/control`

- 承担 Surface 25 的 contextual immediate-control 工作。

## 6. Semantic constraints

必须继续保持：

```text
Stopped ≠ Fault
Offline ≠ Fault
No telemetry ≠ Off

Alarm ≠ Finding
Finding ≠ Root cause

Writable ≠ Safe to control
Online ≠ Control authority
ACK ≠ Readback
Readback ≠ Verified behavior
```

数据源没有提供 mode / stage / authority / design range 时，UI 必须显示 unknown / not provided，而不是补一个看起来合理的状态。

## 7. Browser acceptance

当前实现必须通过：

- Sidebar 只有 10 Workspace，`运行` active；
- `/operations` Shell 标题为“运行”，不重复“系统运行”页面标题；
- `系统运行 / 空间与环境` 在同一 Workspace 内切换；
- view 切换写入 Router Search Params；
- `/operations/trends` 与 `/operations/control` 保持“运行” Workspace active；
- 系统运行中 `[data-slot=card]` 为 0；
- 不出现已知的推断性文案；
- desktop / 768px narrow 均无 horizontal overflow；
- frontend-review 下即时控制不会永久停在 skeleton；没有正式控制目标时显示正式 Empty State。
