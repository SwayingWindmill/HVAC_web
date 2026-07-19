# HVAC / ThingsBoard 参考契约基线

## 定位

本文件用于架构设计、接口建模、Mock、契约测试和首版实现，不代表任何目标 ThingsBoard 实例或具体设备固件的真实契约。

平台内部只依赖规范化的 Canonical Contract。ThingsBoard 遥测 Key、Attribute Key、Alarm Type、RPC Method 和响应格式均通过版本化 Device Profile Mapping 转换，禁止在领域服务、查询服务、AI Tool 或前端中硬编码厂商 Key。

真实设备接入生产前必须完成本文末尾的验证门禁；未验证字段不得标记为 `VERIFIED`，可继续以 `REFERENCE` 或 `INFERRED` 状态运行在开发、演示和影子模式。

## 设备类别参考集

首版按下列常见类别建立可扩展 Profile，不要求所有项目全部具备：

- `ENV_SENSOR`：温度、湿度、CO₂、空气质量等环境传感器；
- `AHU`：组合式空调机组；
- `FCU`：风机盘管；
- `VAV`：变风量末端；
- `CHILLER`：冷水机组；
- `COOLING_TOWER`：冷却塔；
- `PUMP`：冷冻水泵、冷却水泵、热水泵；
- `BOILER`：锅炉或热源设备；
- `HEAT_EXCHANGER`：换热器；
- `ENERGY_METER`：电、水、冷量、热量计量设备；
- `GENERIC_CONTROLLER`：PLC、DDC、网关或无法进一步分类的控制器。

设备类别是平台业务分类，不要求与 ThingsBoard Device Profile 名称一一相同。

## Canonical TelemetryPoint

每个标准化点位至少包含：

```text
point_id
organization_id
site_id
equipment_id
device_id
canonical_key
external_key
value_type
engineering_unit
semantic_kind
writable
quality
source_timestamp
ingested_at
mapping_version
source_sequence?
```

`canonical_key` 采用小写点分命名，例如 `air.supply.temperature`。`external_key` 保存 ThingsBoard 或设备实际 Key，只能存在于映射层。

每个点位必须声明：

- 值类型：`BOOLEAN | INT64 | FLOAT64 | STRING | ENUM | JSON`；
- 工程单位：UCUM 风格字符串或平台批准单位代码；
- 语义类型：`MEASUREMENT | STATE | SETPOINT | COUNTER | TOTALIZER | QUALITY | ALARM_INDICATOR`；
- 读写能力；
- 合理上下限与控制上下限；
- 正常上报周期、允许抖动、迟到窗口和 stale 阈值；
- 缩放、偏移、枚举转换和质量规则。

## 常见 Canonical Key

以下仅为内部标准键参考，不能据此假设目标设备使用同名 ThingsBoard Key。

### 环境与空间

| Canonical key | 类型 | 参考单位 | 语义 |
|---|---|---|---|
| `space.temperature` | FLOAT64 | `Cel` | 室内温度 |
| `space.temperature.setpoint` | FLOAT64 | `Cel` | 温度设定值 |
| `space.relative_humidity` | FLOAT64 | `%` | 相对湿度 |
| `space.co2` | FLOAT64 | `ppm` | CO₂ 浓度 |
| `space.occupancy` | BOOLEAN/ENUM | `1` | 占用状态 |
| `space.air_quality.index` | FLOAT64 | `1` | 空气质量指数 |

### 空气系统

| Canonical key | 类型 | 参考单位 |
|---|---|---|
| `air.outdoor.temperature` | FLOAT64 | `Cel` |
| `air.return.temperature` | FLOAT64 | `Cel` |
| `air.supply.temperature` | FLOAT64 | `Cel` |
| `air.mixed.temperature` | FLOAT64 | `Cel` |
| `air.supply.temperature.setpoint` | FLOAT64 | `Cel` |
| `air.duct.static_pressure` | FLOAT64 | `Pa` |
| `air.duct.static_pressure.setpoint` | FLOAT64 | `Pa` |
| `air.flow` | FLOAT64 | `m3/h` |
| `damper.outdoor.position` | FLOAT64 | `%` |
| `damper.return.position` | FLOAT64 | `%` |
| `valve.cooling.position` | FLOAT64 | `%` |
| `valve.heating.position` | FLOAT64 | `%` |
| `fan.supply.run_status` | BOOLEAN/ENUM | `1` |
| `fan.return.run_status` | BOOLEAN/ENUM | `1` |
| `fan.supply.speed` | FLOAT64 | `%` |
| `fan.return.speed` | FLOAT64 | `%` |
| `filter.differential_pressure` | FLOAT64 | `Pa` |
| `equipment.mode` | ENUM | `1` |
| `equipment.enable_status` | BOOLEAN/ENUM | `1` |

### 水系统与冷站

| Canonical key | 类型 | 参考单位 |
|---|---|---|
| `water.chilled.supply.temperature` | FLOAT64 | `Cel` |
| `water.chilled.return.temperature` | FLOAT64 | `Cel` |
| `water.chilled.supply.temperature.setpoint` | FLOAT64 | `Cel` |
| `water.cooling.supply.temperature` | FLOAT64 | `Cel` |
| `water.cooling.return.temperature` | FLOAT64 | `Cel` |
| `water.flow` | FLOAT64 | `m3/h` |
| `water.pressure` | FLOAT64 | `kPa` |
| `water.differential_pressure` | FLOAT64 | `kPa` |
| `pump.run_status` | BOOLEAN/ENUM | `1` |
| `pump.speed` | FLOAT64 | `%` |
| `chiller.run_status` | BOOLEAN/ENUM | `1` |
| `chiller.load_ratio` | FLOAT64 | `%` |
| `chiller.cooling_capacity` | FLOAT64 | `kW` |
| `chiller.cop` | FLOAT64 | `1` |
| `tower.fan.run_status` | BOOLEAN/ENUM | `1` |
| `tower.fan.speed` | FLOAT64 | `%` |

### 能耗与计量

| Canonical key | 类型 | 参考单位 | 语义 |
|---|---|---|---|
| `electric.power.active` | FLOAT64 | `kW` | 瞬时值 |
| `electric.energy.active_import` | FLOAT64 | `kWh` | 累计值 |
| `electric.voltage` | FLOAT64 | `V` | 瞬时值 |
| `electric.current` | FLOAT64 | `A` | 瞬时值 |
| `electric.power_factor` | FLOAT64 | `1` | 瞬时值 |
| `thermal.power.cooling` | FLOAT64 | `kW` | 瞬时冷量 |
| `thermal.energy.cooling` | FLOAT64 | `kWh` | 累计冷量 |
| `water.volume.total` | FLOAT64 | `m3` | 累计水量 |

累计值不得按普通 Gauge 处理；必须支持设备复位、回绕、换表和异常跳变。

## Attribute Scope 基线

### Client Scope：设备 reported state

常见内容：

- `firmware_version`、`hardware_version`；
- `model`、`serial_number`；
- `protocol`、`gateway_id`；
- `last_boot_time`；
- `reported_mode`、`reported_setpoint`；
- 设备自身诊断或能力声明。

平台默认只读。设备上报内容不直接成为业务主数据，需映射和校验。

### Shared Scope：平台下发 desired configuration

常见内容：

- 采样周期；
- 上报周期；
- 告警阈值；
- 时区或时间同步策略；
- 设备可接受的配置参数。

Shared Attribute 写入也属于受治理控制操作，必须经过 Command/Configuration Policy、审计和版本检查，不能由普通 CRUD API直接修改。

### Server Scope：平台和集成元数据

常见内容：

- `site_id`、`equipment_id`、`mapping_version`；
- 安装位置；
- 数据质量状态；
- 最近同步状态；
- 维护标签和集成诊断信息。

业务主数据权威仍在 Go/PostgreSQL；Server Attribute 只能作为 ThingsBoard 侧投影或集成辅助信息。

## Alarm 参考模型

平台规范化 Alarm 至少包含：

```text
alarm_id
organization_id
site_id
equipment_id
device_id
canonical_type
external_type
severity
state
started_at
acknowledged_at?
cleared_at?
acknowledged_by?
details
source_alarm_id
mapping_version
```

严重度统一为：

```text
INFO | WARNING | MINOR | MAJOR | CRITICAL
```

生命周期统一为：

```text
ACTIVE_UNACK
ACTIVE_ACK
CLEARED_UNACK
CLEARED_ACK
```

常见 Canonical Alarm Type：

- `DEVICE_OFFLINE`；
- `SENSOR_FAULT`；
- `TEMPERATURE_HIGH`、`TEMPERATURE_LOW`；
- `PRESSURE_HIGH`、`PRESSURE_LOW`；
- `FLOW_LOW`；
- `FILTER_DIRTY`；
- `FAN_FAULT`；
- `PUMP_FAULT`；
- `CHILLER_FAULT`；
- `FREEZE_PROTECTION`；
- `COMMUNICATION_FAULT`；
- `ENERGY_ANOMALY`；
- `DATA_STALE`、`DATA_QUALITY_BAD`。

外部 Alarm Type 必须通过映射表进入 Canonical Type。无法映射时保存为 `UNCLASSIFIED_EXTERNAL_ALARM`，不得丢弃原始 Type 和 Details。

## Command Schema 参考模型

平台不允许业务调用方直接提交任意 ThingsBoard `method + params`。调用方只能使用已注册的 Canonical Command：

- `SET_SETPOINT`；
- `SET_MODE`；
- `SET_SPEED`；
- `SET_POSITION`；
- `ENABLE`；
- `DISABLE`；
- `START`；
- `STOP`；
- `RESET_ALARM`；
- `SYNC_TIME`；
- `READ_STATUS`。

每个 Device Capability Profile 将 Canonical Command 映射为实际 RPC Method、Payload 和响应解析规则。

```text
command_type
capability_version
external_method
request_schema
response_schema
timeout_ms
persistent_allowed
idempotency_mode
risk_level
approval_policy
preconditions
postconditions
verification_points
```

参考风险分级：

- `READ_STATUS`：LOW；
- 小范围 `SET_SETPOINT`、`SET_SPEED`、`SET_POSITION`：MEDIUM；
- `ENABLE`、`DISABLE`、`START`、`STOP`、`RESET_ALARM`：HIGH；
- 可能影响安全联锁、设备保护或多设备联动的操作：CRITICAL，默认不允许由云端自动执行。

所有可写数值必须有 Device/Profile 级上下限、步长和单位。平台先转换为设备单位，再执行边界检查。

## ACK 和结果语义基线

外部 RPC 响应统一映射为：

```text
ACK_ACCEPTED
ACK_EXECUTED_SUCCESS
ACK_EXECUTED_FAILED
ACK_REJECTED
OUTCOME_UNKNOWN
```

HTTP 2xx 只证明 ThingsBoard 接口返回成功，不能默认等价于设备已执行。HTTP 超时、网络断开或迟到响应默认映射为 `OUTCOME_UNKNOWN`，除非目标 Adapter 已经通过真实设备测试证明更强语义。

命令完成可由以下证据之一确认：

1. 设备响应中携带稳定 command/attempt 标识；
2. ThingsBoard Persistent RPC 状态明确表明设备已处理；
3. 目标 reported point 在有效时间窗内达到期望状态；
4. 现场系统提供可验证 ACK。

仅有“请求已发送”不能标记 `EXECUTED_SUCCESS`。

## 数据质量和默认时间策略

在没有真实设备证据时采用可配置参考值，而不是写死到代码：

- 参考上报周期：5–30 秒；
- 遥测允许迟到窗口：5 分钟；
- 批量回补窗口：24 小时；
- stale 阈值：`max(3 × expected_interval, 60 秒)`；
- 设备时间与接收时间偏差超过 10 分钟时标记 `CLOCK_SKEW`；
- 无 source timestamp 时使用摄取时间并标记 `INGEST_TIME_ONLY`；
- 同设备、同 Key、同时间戳、同值可判为重复候选，但不得仅凭时间戳删除不同值。

上述数字属于参考默认值，必须可在 Organization/Site/Profile/Point 层覆盖。

## Mapping 状态

每个外部契约映射必须有状态：

```text
REFERENCE
INFERRED
VERIFIED
DEPRECATED
REJECTED
```

- `REFERENCE`：来自本基线；
- `INFERRED`：根据样本或配置推断，尚未人工确认；
- `VERIFIED`：通过目标实例、设备文档或受控测试确认；
- `DEPRECATED`：仍兼容但不再用于新设备；
- `REJECTED`：已证实错误或不安全。

生产控制只允许使用 `VERIFIED` 的命令映射。生产遥测可在显式标记和质量隔离下接收 `INFERRED` 映射，但不能直接驱动高风险自动控制。

## 生产接入验证门禁

每个 Device Profile 在生产启用前至少确认：

1. ThingsBoard Device Profile、实际设备型号和固件版本；
2. External Key 到 Canonical Key 的映射；
3. 值类型、工程单位、缩放、偏移、枚举和空值语义；
4. 正常上报频率、最大迟到、设备时间质量和 stale 阈值；
5. Client/Shared/Server Attribute 的权威与写入方向；
6. Alarm Type、Severity、ACK/Clear 生命周期；
7. 每个可用 RPC Method 的请求 Schema、响应 Schema、超时和离线行为；
8. 命令是否支持幂等键、Persistent RPC 和状态查询；
9. 至少一次无破坏性受控命令测试及真实 ACK 样本；
10. 设备离线、HTTP 超时、ACK 丢失、迟到 ACK 和执行后断线测试；
11. 控制上下限、联锁条件、审批级别和人工回退流程；
12. 契约样本脱敏后进入版本库或契约 Registry。

未通过门禁时：

- 遥测允许进入 Quarantine 或影子标准化；
- 只读查询可按风险开放；
- 自动化、AI 和计划任务不得向设备发送控制命令；
- 人工命令也必须被 Adapter 拒绝，而不是转发任意 RPC。

## 架构阶段结论

票据 06 和 07 可以基于本参考契约继续设计，但必须保持以下边界：

- 遥测 Pipeline 面向 Canonical Point，不绑定具体 ThingsBoard Key；
- Command Service 面向 Canonical Command，不暴露任意 RPC；
- 外部契约由版本化 Profile Mapping 和 Adapter 承担；
- 真实契约采集不再阻塞架构设计，但仍是生产设备接入和控制启用的硬门禁。
