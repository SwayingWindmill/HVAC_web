# Backend 能源平台目标架构

状态：PROPOSED / SOURCE-ALIGNED  
范围：当前 Backend + UI 纵向切片  
不包含：生产 Edge Host、真实现场协议驱动、Edge 本地控制闭环

## 1. 设计结论

Backend 不应继续围绕 generic telemetry 扩张。三份参考项目源码表明，目标平台必须同时具备：

1. ThingsBoard 风格的平台底座：租户、实体关系、接入、事件路由、告警、实时订阅；
2. MyEMS 风格的能源内容：计量、能源分类、清洗、规范化、汇总、账单、碳排、基线、预测、FDD、报表；
3. OpenEMS 风格的未来控制 seam：Cloud governed intent 与 Edge cycle/effective value 分离。

当前阶段只落地前两项的最小可用闭环，并冻结第三项的接口语义。

## 2. 逻辑模块

~~~text
Platform Foundation
├─ IAM / Tenant / User / Authorization
├─ Site / Space / Asset / Device / Point Registry
├─ Relation / Ownership / Topology
└─ Manifest / Capability Read Model

Data Plane
├─ Ingestion Adapter
├─ Telemetry Acceptance / Quality
├─ Current Projection
├─ Historical Fact
└─ Realtime Subscription

Energy Domain
├─ Energy Category / Item
├─ Meter / Meter Binding
├─ Virtual Meter / Virtual Point
├─ Cleaning / Correction
├─ Normalization
├─ Aggregation
├─ Tariff / Calendar / Billing
├─ Carbon Factor / Carbon Accounting
├─ Baseline / Plan / Forecast
└─ FDD / Reporting

Automation and Governance
├─ Rule / Event Automation
├─ Alarm / Notification
├─ Work Order
├─ Command Governance / Approval / Audit
└─ Future Edge Intent / Readback Evidence

Presentation
├─ Operations Query
├─ Energy Query
├─ Administration Query
└─ Dashboard / Widget / Export Read Model
~~~

这些是逻辑 module，不要求当前每个 module 都有独立进程。当前物理部署仍以现有 Phase 1 约束为准；只有当写入模式、失败隔离或伸缩压力被验证后才拆进程。

## 3. 模块 ownership

| Module | 自己拥有的事实 | 可以依赖 | 不得拥有 |
| --- | --- | --- | --- |
| Registry | Tenant、Site、Space、Asset、Device、Point、关系、能力声明 | IAM、Manifest | telemetry history、能源汇总 |
| Ingestion | 接收事件、映射结果、接收状态、幂等键 | Registry、Transport adapter | 业务 KPI、报警最终状态 |
| Telemetry | Raw telemetry、quality、current/history 投影、realtime publication | Ingestion、storage | tariff、billing、carbon 规则 |
| Energy Processing | cleaning decision、correction fact、normalized energy fact、aggregation run | Telemetry、Meter、Energy Model | 协议连接、UI 页面状态 |
| Energy Content | Meter、Virtual Meter、Category、Tariff、Carbon、Baseline、Plan | Registry、Energy Processing | MQTT 会话、Edge cycle |
| Alarm/FDD | alarm lifecycle、diagnosis result、notification intent、FDD finding | Telemetry、Energy facts、Rule | raw data 重写、设备驱动 |
| Command Governance | command identity、approval、lease、audit、outcome | IAM、Registry、Rule | 最终 actuator value、设备 I/O |
| Query/Reporting | 面向 UI 的稳定 read model、report snapshot、export job | 各领域公开事实 | 反向写领域表 |

## 4. 依赖规则

1. Adapter 只负责协议解码、身份映射和传输状态；能源语义进入 Energy Processing。
2. Raw telemetry 不被清洗结果覆盖。修复产生 Correction Fact，并通过 processing run 进入下游。
3. Normalization 和 Aggregation 必须可重跑；每次 run 记录输入窗口、规则版本、依赖版本、状态和输出 revision。
4. Metric 是计算机制，不是全部能源业务模型。Energy Category、Meter、Tariff、Carbon、Baseline 等不能隐藏在一个 generic Metric module 中。
5. Rule/Alarm 可触发通知、工单和治理流程，但不能替代 OpenEMS 风格 Edge Controller/Scheduler/Arbiter。
6. UI 只读 Query/Reporting module 的 read model；禁止直接依赖 PostgreSQL/ClickHouse 表结构。
7. Command Governance 只产生 governed intent。未来 Edge 负责安全约束、仲裁和 effective value；当前 simulator 只能用于验证这条 seam。
8. Redis 只保存可重建 projection；业务事实和审计事实必须有明确的 PostgreSQL/ClickHouse authority。

## 5. Backend 最小纵向闭环

第一条可交付链路应是：

~~~text
Registry
  ↓
MQTT / Simulator Adapter
  ↓
Raw Telemetry + Quality
  ↓
Current + Historical Projection
  ↓
Cleaning / Normalization
  ↓
Hourly / Daily Energy Aggregation
  ↓
Energy Query + Trend + Comparison UI
~~~

这条链路先覆盖单站点、单租户、有限 point 类型和基础能源指标，但必须把事实类型、质量、时间、重跑和 provenance 定义完整。不要先做大量空模块再补数据链。

## 6. ThingsBoard / OpenEMS / MyEMS 的取舍

| 机制 | 结论 | 本项目动作 |
| --- | --- | --- |
| ThingsBoard Tenant/Customer/Entity relation | ADAPT | 保留平台层级，但使用 HVAC 的 Site/Space/Asset/Device/Point 语义 |
| ThingsBoard Rule Node | ADAPT | 先实现最小事件自动化，用于告警/通知/工作流；不做低代码全家桶 |
| ThingsBoard Dashboard/Widget/Alias | ADOPT | 进入 UI read model 和视图配置设计 |
| OpenEMS Channel/Process Image/Cycle | FREEZE-SEAM | 当前不部署 Edge；Command 接口保留 intent/effective/readback 语义 |
| OpenEMS Nature/Driver/Bridge | ADAPT | 未来作为 capability/driver/protocol seam，不让 Backend 依赖厂家字段 |
| MyEMS cleaning/normalization/aggregation | ADOPT | P1 直接建立最小可重跑处理链 |
| MyEMS 多数据库 | ADAPT | 先按逻辑数据集和 ownership 组织，再决定物理存储 |
| MyEMS Admin/Web 分工 | ADOPT | UI 组织为 Administration 与 Energy Management 两类任务 |

## 7. 对现有项目的明确修改

- 把 Telemetry 从“主要产品能力”降为 Data Plane 的输入与事实层；
- 为 Energy Processing 建立一等 module，而不是继续把能源计算散落在 telemetry、metric、UI 查询中；
- 把 Energy Content 从隐含表结构提升为明确领域；
- 将 Report/Trend/Comparison 从通用 telemetry endpoint 抽成 read model；
- 为每个 processing run 增加 input watermark、quality summary、rule/version、output revision；
- 将 command outcome 分为 accepted、delivered、applied、constrained、readback verified、expired、unknown；
- 当实现阶段开始时，删除被替代的旧路径，不增加兼容层。

## 8. 验收条件

- 一个输入事件可追溯到 point、meter、normalization rule、aggregation run 和 UI read model；
- 原始事实不会被清洗或手工修复覆盖；
- 同一 processing run 重试不会生成重复的业务事实；
- UI 不需要知道领域表的物理分布；
- 通用规则失败不会破坏 telemetry 接收和能源事实的一致性；
- Cloud command 不会伪装成已经完成的现场执行。

## 9. 来源

- docs/architecture/reference-baselines.md
- docs/architecture/thingsboard-source-review.md
- docs/architecture/openems-source-review.md
- docs/architecture/myems-source-review.md
- docs/architecture/thingsboard-openems-benchmark.md

