# Phase 1 数据架构基线

本文件以 `架构规划/智慧能源系统数据架构设计.md` 为数据架构来源。项目内部实现可以比示意模型更细，但不得改变以下核心：统一对象模型、统一测点模型、统一时序模型、统一能源模型、统一拓扑模型。

## 六个核心模型

```text
Asset Model
Device Model
Point Model
Time Series Model
Energy Topology Model
Metric Model
```

当前 canonical 映射：

```text
Tenant              -> iam.tenants
Site                -> core_registry.sites
Space               -> core_registry.areas
Asset               -> core_registry.equipment
Device Product      -> core_registry.device_products
Device              -> core_registry.devices
Point Template      -> core_registry.point_templates
Point               -> core_registry.telemetry_points
Unit Registry       -> core_registry.unit_registry
Energy Type         -> core_registry.energy_types
Energy Direction    -> core_registry.energy_directions
Energy Node         -> core_registry.energy_nodes
Energy Edge         -> core_registry.energy_edges
Metric Definition   -> core_registry.metric_definitions
Current State       -> telemetry_runtime.latest_accepted_telemetry / device_presence
Raw Time Series     -> telemetry_history.observations
1 minute            -> telemetry_history.numeric_1min
15 minutes          -> telemetry_history.numeric_15min
1 hour              -> telemetry_history.numeric_hourly
1 day               -> telemetry_history.numeric_daily
Energy Interval     -> analytics.energy_interval_facts
```

## 对象与空间

`Equipment` 继续作为项目内部 Asset 名称。它和通信 `Device` 保持独立，并支持 `parent_equipment_id` 形成资产树；空间仍由 `Area` 递归模型表达。资产父子关系、设备通信关系和空间安装关系不得互相替代。

## Product / Point Template

设备点表采用：

```text
Device Product
   ↓
Point Template
   ↓
Device
   ↓
Telemetry Point
```

`devices.product_id` 和 `telemetry_points.point_template_id` 是渐进绑定字段。旧设备/旧 Point 可以在迁移期保持未绑定，但新型号批量接入不得继续复制匿名点表。

Point Template 的业务类型遵循文档：

```text
TELEMETRY
STATE
COUNTER
COMMAND
SETTING
```

现有 `telemetry_points.point_kind` 的 `MEASURED / CALCULATED / STATE / COMMAND / FEEDBACK` 继续表达当前运行时来源/控制语义，两者不互相替代。Counter/Setting 等业务语义由 Point Template 统一定义。

## Unit Registry

`unit_registry` 保存标准单位和必要的 canonical conversion metadata。输入侧仍可以检测 `UNIT_MISMATCH`，但最终标准单位必须来自 Registry，而不是各 Adapter 自行解释字符串。

## 能源模型

支持文档规定的 Energy Type：

```text
Electricity
Water
Gas
Steam
Heat
Cooling
CompressedAir
Hydrogen
```

支持方向：

```text
IMPORT
EXPORT
GENERATE
CONSUME
CHARGE
DISCHARGE
```

文档没有给出除 Electricity 之外各 Energy Type 的最终默认结算单位，因此只有 Electricity 在基线中绑定 `kWh`；其余类型必须由后续数据治理规则明确，不能静默猜测。

能源拓扑独立于空间模型：

```text
energy_nodes
   ↓
energy_edges
```

一个 Node 可以关联 Equipment、Device，也可以表示 Grid/Load 等没有直接通信 Endpoint 的逻辑能源节点。Edge 强制同 Tenant/Organization/Site，并显式记录 Energy Type 与 Direction。

## Metric

`metric_definitions` 落地文档规定的指标定义字段：Code、Name、Type、Unit、Calculation Method、Aggregation、Period。当前 `analytics.energy_interval_facts` 是专项能源事实，不等于完整 Metric Series 引擎，因此统一 Metric 仍保持 `PARTIAL`，直到 dependency/formula execution 和 generic metric series 完成。

## 时序与 Rollup

Raw 权威仍为：

```text
telemetry_history.observations
```

当前可重建聚合层：

```text
Raw
 ├─ 1 minute
 ├─ 15 minutes
 ├─ 1 hour
 └─ 1 day
```

这些 Aggregate 表不是新的数据权威。具体 Raw/1min/15min/1h/day retention 期限仍需按项目容量与业务要求确定；数据架构文档中的保留期限是示例，不在本基线中静默固化。

## 仍未完成

以下能力必须保持可见，不能因为本轮基础表存在就宣称完整数据架构已完成：

- Command / Alarm / Work Order 全域 `tenant_id`
- Generic Metric Series / dependency execution
- Energy Balance
- Tag / tag_relation
- Canonical Event
- Alarm Rule
- Tariff / tariff_period
- Site Weather Dataset
- Dataset / Feature / Model / Prediction
- optimization_run / input / result / schedule

机器可读状态：`contracts/data/phase1-data-architecture.v1.json`。

检查：

```bash
npm run data:phase1:check
```
