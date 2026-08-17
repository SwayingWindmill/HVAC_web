# HVAC Web × OpenEMS 架构逐项裁决

状态：CURRENT TARGET  
机器权威：`contracts/architecture/edge-control-plane.v1.json`

## 1. 裁决原则

本裁决不以现有 HVAC Web 架构为默认正确答案，也不因 OpenEMS 更成熟而默认照搬。每个能力单独比较，优先采用对商业 HVAC 更安全、更可预测、更易维护、更容易接入真实设备的方案。

裁决含义：

- `OPENEMS`：OpenEMS 的架构机制明显更优，目标架构采用其语义；与现有设计冲突时修改现有设计。
- `HVAC`：现有 HVAC Web 方案更适合多租户商业 HVAC 平台，保留为目标。
- `MERGE`：两侧各有不可替代的优势，组合成新的目标设计。
- `REJECT`：不把该实现选择作为目标架构依赖。

这里的 `OPENEMS` 指采用机制，不等于复制 Java/OSGi 源码。实现语言与运行时仍需要独立证据。

## 2. 官方参考依据

本次只把 OpenEMS 官方文档和官方 GitHub 仓库作为外部事实依据：

- Edge Architecture：<https://openems.github.io/openems.io/openems/latest/edge/architecture.html>
- Edge Configuration：<https://openems.github.io/openems.io/openems/latest/edge/configuration.html>
- Implementing a device：<https://openems.github.io/openems.io/openems/latest/edge/implement.html>
- API Backend / resend / priority：<https://openems.github.io/openems.io/openems/latest/edge/controller.d/io.openems.edge.controller.api.backend.html>
- Real-Time Simulation：<https://openems.github.io/openems.io/openems/latest/simulation/realtime.html>
- Backend Architecture：<https://openems.github.io/openems.io/openems/latest/backend/architecture.html>
- EVSE Single / Cluster Controller：<https://openems.github.io/openems.io/openems/latest/edge/controller.d/io.openems.edge.controller.evse.html>
- Repository：<https://github.com/OpenEMS/openems>

OpenEMS Edge 的核心优势来自 IPO Cycle、Process Image、Controller/Scheduler、Nature/Channel、Bridge/Driver、Edge Timedata 和生产控制器可复用的 Simulator。它也明确把高层 EMS 软件定义为软实时，硬实时安全仍应由 PLC/设备保护完成。

## 3. 总体裁决

50 项能力的结果：

| Verdict | 数量 |
|---|---:|
| OPENEMS | 29 |
| HVAC | 8 |
| MERGE | 12 |
| REJECT | 1 |

结论不是“HVAC Web 加几个 OpenEMS 概念”，而是形成新的双控制面架构：

```text
Cloud Control Plane
├─ Tenant / IAM / RLS
├─ Registry / Equipment / Device / Sensor / Point
├─ S2 / ClickHouse / Analytics
├─ Alarm / Work Order
├─ Command Governance / Approval / Audit
├─ Forecast / Optimization
└─ Edge fleet configuration / desired intent
             │
             │ MQTT TLS
             ▼
HVAC Edge Control Plane
├─ Edge Component Registry
├─ Capability Profile Registry
├─ Channel Runtime
├─ Process Image
├─ Cycle
├─ Controller Runtime
├─ Scheduler / Control Arbiter
├─ Safety / Interlock Controllers
├─ Equipment Controllers
├─ Cluster Controllers
├─ Device Drivers
├─ Protocol Bridges
├─ Edge Manifest
├─ Edge Timedata
└─ Store & Forward / Replay
             │
     Modbus / BACnet / OPC UA / vendor protocol
             ▼
        Physical Equipment
```

Cloud Command 不再被定义为“直接拥有最终执行值”，而是**持久化、授权、可审计的控制 Intent**。最终有效设定值由 Edge 本地安全约束、联锁、控制器优先级和设备能力共同裁决。

## 4. 50 项逐项裁决

| ID | 能力 | 裁决 | 目标 | 当前 |
|---|---|---|---|---|
| EDGE-001 | 现场自治控制权 | OPENEMS | 快速闭环归 Edge；Cloud 管治理、策略和长周期 Intent | MISSING |
| EDGE-002 | Cloud 故障隔离 | MERGE | 保留现有隔离并扩展到本地 Controller/Schedule/Safety 持续运行 | PARTIAL |
| EDGE-003 | 硬实时安全边界 | OPENEMS | PLC/设备保护拥有硬实时安全，Edge 软件不得替代 | MISSING |
| EDGE-004 | IPO 控制周期 | OPENEMS | 明确 Input → Process → Output | MISSING |
| EDGE-005 | Process Image | OPENEMS | 一个 Cycle 内所有 Controller 读取同一不可变快照 | MISSING |
| EDGE-006 | value / nextValue 双缓冲 | OPENEMS | 异步 I/O 写 nextValue，Cycle 边界切换为 value | MISSING |
| EDGE-007 | 异步 I/O 与 Cycle 同步 | OPENEMS | 协议 I/O 不阻塞 Controller，通过 Cycle 边界同步 | MISSING |
| EDGE-008 | Controller Contract | OPENEMS | 标准 Edge Controller 接口、周期执行、失败状态 | MISSING |
| EDGE-009 | Scheduler / Priority | OPENEMS | Controller 确定性排序 | MISSING |
| EDGE-010 | Constraint Composition | OPENEMS | 高优先级先收缩可行域，后续 Controller 不可突破 | MISSING |
| EDGE-011 | Cloud Command 语义 | MERGE | Cloud 保留 durable lifecycle；Edge 将其作为本地 Intent | PARTIAL |
| EDGE-012 | Command 审批/审计/生命周期 | HVAC | Cloud IAM + Approval + Audit + durable state machine 保留 | IMPLEMENTED |
| EDGE-013 | Command Verification | MERGE | Edge 快速 readback；Cloud 持久化最终 outcome | PARTIAL |
| EDGE-014 | Remote Control Lease | OPENEMS | 远程 Intent 必须过期/续租，失联后归还本地控制 | MISSING |
| EDGE-015 | Safety / Interlock | MERGE | 安全与联锁下沉 Edge Controller；Cloud 保留政策和证据 | PARTIAL |
| MODEL-001 | Canonical Point | HVAC | Point 继续作为平台长期数据点身份 | IMPLEMENTED |
| MODEL-002 | Edge Channel | OPENEMS | 新增运行时 Channel：类型、单位、质量、权限、优先级 | MISSING |
| MODEL-003 | Point ↔ Channel | MERGE | Channel 映射 Point，但绝不取代 Point 身份 | MISSING |
| MODEL-004 | Equipment/Device/Sensor/Point | HVAC | 保留独立身份、历史 Binding、Sensor 生命周期 | IMPLEMENTED |
| MODEL-005 | Capability Profile / Nature | OPENEMS | Controller 面向能力，不面向厂家 Driver | MISSING |
| MODEL-006 | Required/Optional Channels | OPENEMS | Capability Profile 声明必需/可选点和控制限制 | MISSING |
| MODEL-007 | Edge Component Registry | OPENEMS | Edge 本地注册 Driver/Bridge/Controller/Channel/Service | MISSING |
| MODEL-008 | Edge Manifest | MERGE | Edge 自描述 + Cloud Registry reconciliation | MISSING |
| DEVICE-001 | Device Driver | OPENEMS | 厂家/型号逻辑隔离到 Driver | MISSING |
| DEVICE-002 | Protocol Bridge | OPENEMS | Modbus/BACnet/OPC UA/MQTT 连接生命周期统一复用 | MISSING |
| DEVICE-003 | Declarative Mapping | OPENEMS | Driver 主要声明地址映射、转换、写规则 | MISSING |
| DEVICE-004 | Poll Priority | OPENEMS | 关键状态高频，诊断低频 | MISSING |
| DEVICE-005 | RO/RW/WO Access | MERGE | Channel Access 来自 Point authority + Driver capability | PARTIAL |
| DEVICE-006 | Driver Capability Discovery | MERGE | Driver 显式暴露能力和边界，Registry reconciliation | MISSING |
| DATA-001 | Edge ↔ Cloud Transport | HVAC | 保留 MQTT TLS，不切换为 OpenEMS Backend WebSocket | IMPLEMENTED |
| DATA-002 | Edge Timedata | OPENEMS | 本地 latest/history 独立于“发送失败队列” | PARTIAL |
| DATA-003 | Store & Forward | MERGE | 现有 persistent queue 融入 Timedata/resend cursor | IMPLEMENTED |
| DATA-004 | Persistence Priority | OPENEMS | Point/Channel 决定常规上云优先级 | MISSING |
| DATA-005 | Edge Aggregation | OPENEMS | 可选聚合在 Edge 完成，降低 Cloud/DB 压力 | MISSING |
| DATA-006 | Resend Priority | OPENEMS | 故障/状态/控制证据优先于低价值诊断 | MISSING |
| DATA-007 | Change + Full Snapshot | OPENEMS | 变化立即发布，同时周期发送完整快照用于收敛 | MISSING |
| DATA-008 | Cloud Timedata / Analytics | HVAC | S2 + ClickHouse + PostgreSQL 分工保留 | IMPLEMENTED |
| SIM-001 | Simulator 复用生产 Controller | OPENEMS | 模拟与真实只替换 Driver/Protocol 边界 | PARTIAL |
| SIM-002 | Acting / Reacting Simulator | OPENEMS | 支持独立数据源模型与受控制动作影响的反应模型 | PARTIAL |
| SIM-003 | 真实物理动态/故障 | MERGE | ramp、延迟、min run/stop、fault、stale、noise、stuck actuator | PARTIAL |
| SIM-004 | Protocol-level Simulator | OPENEMS | Driver 可直接面对模拟 Modbus/BACnet 端点测试 | PARTIAL |
| CTRL-001 | Single Equipment Controller | OPENEMS | 单设备 Controller 管自身 state machine/sequence | MISSING |
| CTRL-002 | Cluster Controller | OPENEMS | Plant/Cluster 分配负荷；Single Controller 管单机 | MISSING |
| CTRL-003 | Energy/Time-slot Scheduler | OPENEMS | 借其调度思想，改造成 HVAC 热工/设备约束模型 | MISSING |
| CTRL-004 | Forecast / Optimization Placement | MERGE | 重计算可在 Cloud；Edge 执行 versioned schedule 并可离线 fallback | PARTIAL |
| CLOUD-001 | Tenant/IAM/RLS | HVAC | 保留当前多租户授权权威 | IMPLEMENTED |
| CLOUD-002 | Registry / Asset Lifecycle | HVAC | 保留 Area/Equipment/Device/Sensor/Point 和历史关系 | IMPLEMENTED |
| CLOUD-003 | Alarm/Work Order/Audit | HVAC | Edge 发事件证据，Cloud 继续拥有业务生命周期 | IMPLEMENTED |
| UI-001 | 自描述控制 UI | MERGE | Registry + Capability Profile + Edge Manifest 驱动 Equipment UX | PARTIAL |
| TECH-001 | 直接搬 OpenEMS Java/OSGi/Backend/UI | REJECT | 机制引进，技术栈单独用证据裁决，不设成目标依赖 | N/A |

## 5. 必须修改的现有架构假设

### 5.1 Edge Gateway 不再只是传输网关

旧基线表达为：

```text
Cloud -> Edge Gateway -> OT Protocol -> Device
```

目标改为：

```text
Cloud
  │ governed intent / config / MQTT
  ▼
HVAC Edge Control Plane
  ├─ local runtime state
  ├─ control cycle
  ├─ safety/interlock
  ├─ controller arbitration
  ├─ driver/bridge
  └─ local timedata
  │
  ▼
OT Device
```

### 5.2 Command Dispatcher 不再等价于最终控制决策

`Command Service` 仍负责 Cloud 中的 durable intent、授权、审批、幂等和审计。`Dispatcher` 只把合法 Intent 送达 Edge。Edge `Control Arbiter` 才决定本周期的 `effectiveValue`。

Command 应逐步补充：

```text
requestedValue
effectiveValue
constraintReason
winningController
controlCycle
intentExpiresAt
```

例如：

```text
Cloud requested frequency = 50 Hz
Safety max frequency       = 43 Hz
Effective frequency        = 43 Hz
Constraint reason          = SAFETY_LIMIT
```

### 5.3 Point 不被 Channel 替换

两者职责不同：

```text
Point   = durable platform identity / history / authority / provenance
Channel = live Edge runtime object / process-image state
```

目标映射：

```text
Point ID 019...
pointCode CHWP01_FREQUENCY
       ↕
Edge Component chwp01
Channel Frequency
Address chwp01/Frequency
```

### 5.4 Capability Profile 取代“厂家判断”

未来 Controller 不允许依赖类似：

```text
if vendor == ABB
if vendor == Schneider
```

而应依赖：

```text
VARIABLE_SPEED_PUMP
├─ required: RunState, FaultCode, Frequency
├─ capabilities: Start, Stop, ResetFault, SetFrequency
└─ limits: MinFrequency, MaxFrequency
```

ABB/Schneider/Siemens/Simulator Driver 都实现同一 Capability Profile。

## 6. Phase 1 新的 Edge 最小完成定义

在新的目标架构下，仅仅满足：

```text
MQTT telemetry works
MQTT command works
persistent queue works
```

不再足以声明“可生产控制 Edge 完成”。

Phase 1 Edge Foundation 至少应具备：

1. Channel Runtime；
2. Process Image；
3. Cycle；
4. Controller Contract；
5. Scheduler / Control Arbiter；
6. Capability Profile；
7. Device Driver；
8. Protocol Bridge；
9. Safety / Interlock Controller；
10. Cloud Command Intent Adapter + Lease；
11. Edge Manifest；
12. Edge Timedata + priority resend；
13. Simulator 与真实 Driver 共用控制接口。

因此当前 IoT Runtime / Control 按新目标应标为 `PARTIAL`，而不是继续显示完全 `ALIGNED`。

## 7. 实施顺序

### P1 — Edge Foundation

优先实现，不等优化算法：

```text
Channel Runtime
→ Process Image
→ Cycle
→ Controller
→ Scheduler / Arbiter
→ Capability Profile
→ Driver / Bridge
→ Safety / Interlock
→ Command Intent Lease
→ Edge Manifest
→ Edge Timedata / resend priority
```

第一批真实 Capability Profile：

```text
VARIABLE_SPEED_PUMP
CHILLER
COOLING_TOWER
ELECTRICITY_METER
WEATHER_STATION
```

第一批 Bridge：

```text
Modbus TCP
Modbus RTU
MQTT
```

BACnet 和 OPC UA 沿相同接口扩展，不为每个协议重新设计设备模型。

### P2 — Plant Control

```text
Single Equipment Controller
Cluster Controller
plant-level constraints
edge aggregation
dynamic Asset controls
```

中央机房重点：

```text
ChillerPlantController
ChilledWaterPumpClusterController
CoolingWaterPumpClusterController
CoolingTowerClusterController
```

### P3 — Intelligent Scheduling

吸收 OpenEMS EnergyScheduler 的时间片/模式思想，但使用 HVAC 模型：

```text
weather forecast
load forecast
tariff
plant COP
equipment min-run/min-stop
start count
thermal inertia
maximum demand
```

Cloud 可以计算长周期计划；Edge 必须能执行 versioned schedule，并在 Cloud 不可用时维持最后有效计划或本地 fallback。

## 8. OpenEMS runtime 是否直接采用

当前裁决**不把 Java/OSGi 作为目标依赖**，也不因我们已有 Go 代码就把 Go 固化成架构原则。

若后续需要决定“直接运行 OpenEMS Edge”还是“Go 实现同等 Edge 机制”，应做一个受限 A/B PoC：

```text
同一模拟/真实 VFD
同一 Modbus TCP
同一 VARIABLE_SPEED_PUMP Capability
同一 START/STOP/SET_FREQUENCY 场景
```

比较：

```text
CPU / RAM
启动与恢复时间
Driver 代码量
第二设备接入成本
控制周期 jitter
断网行为
故障恢复
配置复杂度
升级维护成本
```

实现栈由证据决定，但无论选择哪种运行时，本文件已经裁决的 Process Image、Cycle、Controller/Scheduler、Capability/Driver/Bridge 等目标语义不变。
