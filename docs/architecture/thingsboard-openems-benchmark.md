# ThingsBoard × OpenEMS × MyEMS 对标研究

日期：2026-08-24  
目标：为 HVAC Web 建立“三参考架构”，从 ThingsBoard、OpenEMS、MyEMS 各自擅长的问题域中选择更强机制。HVAC 当前实现不是金科玉律；任何现有设计都必须与参考项目的源码、文档和真实产品职责重新比较。

## 结论

HVAC Web 不应做成 ThingsBoard 与 OpenEMS 的拼盘，而应形成三层架构：

1. **HVAC Edge Control Plane**：采用 OpenEMS 的本地实时控制语义，并吸收 ThingsBoard Edge 的本地存储、规则处理和断线同步思想。
2. **HVAC Cloud Energy Platform**：采用 ThingsBoard 的多租户、实体关系、设备接入、规则/告警和可视化平台能力，同时保留 HVAC 的业务 ownership、命令治理和能源分析域。
3. **HVAC Energy Experience**：保留 Asset-centered UX，吸收 ThingsBoard 的 Dashboard/Widget/Entity Alias 组织方式和 OpenEMS 的实时 Channel/Manifest 自描述能力。

MyEMS 不是第四层，而是 Cloud Energy Platform 的重要内容参考：它在能源分类、空间/设备计量、历史数据治理、标准化、能耗/账单/碳排汇总、基线、预测和报表方面，优先级高于通用 IoT 平台的抽象能力。

核心分工：

- ThingsBoard 风格 Rule Engine 处理事件路由、数据转换、告警、通知和业务工作流。
- OpenEMS 风格 Edge Cycle 处理安全约束、快速闭环、控制优先级和设备写入。
- Cloud 产生 durable governed intent，Edge 决定实际 effective value；云端规则不能替代 Edge 本地控制。

## 官方事实

### ThingsBoard

- [Architecture Overview](https://thingsboard.io/docs/reference/architecture/) 将平台划分为 Transport、ThingsBoard Node、JS Executor 和 Web UI；平台核心包含 REST/WebSocket、Rule Engine、Actor System、设备状态和实体管理。其架构支持横向扩展，并用 Tenant → Customer → Devices/Assets 的层级做租户隔离。
- [Multi-Tenancy & Hierarchy](https://thingsboard.io/docs/concepts/multi-tenancy/) 将 Tenant、Customer、User 和设备/资产资源组织成权限范围。Tenant 是独立组织的顶层实体，Customer 用于安全地委派子组织访问。
- [Rule Nodes](https://thingsboard.io/docs/user-guide/rule-nodes/) 将规则链拆成过滤、丰富、转换、动作、外部发布和流程控制等 node；每个 node 处理消息并通过命名 relation 路由结果。
- [Integrations](https://thingsboard.io/docs/user-guide/integrations/) 将外部协议或平台转换成统一数据模型，支持 uplink/downlink converter；远程 integration 可以靠近现场运行并在断网时本地缓冲。
- [ThingsBoard Edge](https://thingsboard.io/docs/edge/key-concepts/edge-instance/) 在现场运行独立 Edge，支持本地设备接入、规则处理和同步；Edge 到 Cloud 的事件会在本地持久化，断线后恢复发送。
- [Telemetry synchronization](https://thingsboard.io/docs/edge/pe/key-concepts/telemetry-synchronization/) 明确 Edge 先把数据写入本地，再由 Push to Cloud/Push to Edge rule node 决定同步范围；本地可以保留不上传的数据。
- [Dashboards](https://thingsboard.io/docs/user-guide/dashboards/) 和 [Widgets](https://thingsboard.io/docs/user-guide/widgets/) 提供 Dashboard state、Entity Alias、time window、widget datasource、控制动作和 SCADA/工业监控类 widget。

### OpenEMS

- 本地参考 checkout：`E:/Code/openems`。HVAC ADR 0012 规定的裁决基线是 OpenEMS `2026.7.0` / commit `2e2792d`；当前本地 checkout 是更近的 develop dirty 状态，不作为无条件架构基线。
- [OpenEMS Edge Architecture](https://openems.github.io/openems.io/openems/latest/edge/architecture.html) 采用 Input-Process-Output；输入阶段形成 cycle 内不变的 Process Image，process 阶段运行 Controllers，output 阶段写入设备。
- OpenEMS Channel 同时维护 `value` 和 `nextValue`，只在 Switch Process Image 时切换，避免异步设备数据在同一 cycle 内改变控制依据。
- OpenEMS 通过 Nature/Channel 把设备能力与具体设备实现分开，Controllers 面向能力而不是厂商型号；Scheduler 负责控制顺序和组合。
- [OpenEMS Backend Architecture](https://openems.github.io/openems.io/openems/latest/backend/architecture.html) 将 Backend 的职责拆为 Metadata、Timedata、UI WebSocket、Edge Manager 和 Backend-to-Backend；Edge 通过独立连接接入 Backend。
- [Internal Component Communication](https://openems.github.io/openems.io/openems/latest/component-communication/index.html) 使用双向 WebSocket/JSON-RPC、Channel subscription、currentData 和 EdgeConfig；UI 可以根据 EdgeConfig 适配实际 Edge 配置。

### MyEMS

- [MyEMS 官方仓库](https://github.com/MyEMS/myems) 将系统定位为面向建筑、工厂、商场、医院和园区的能源/碳排采集、分析和报表系统；仓库组件包括 API、Admin/Web UI、Modbus TCP acquisition、cleaning、normalization、aggregation 和 database。
- [MyEMS API](https://github.com/MyEMS/myems/tree/master/myems-api) 是面向 MyEMS 组件和第三方应用的 Python RESTful API。
- [MyEMS Cleaning](https://github.com/MyEMS/myems/tree/master/myems-cleaning) 负责历史数据清洗；[Normalization](https://github.com/MyEMS/myems/tree/master/myems-normalization) 负责历史能源数据规范化；[Aggregation](https://github.com/MyEMS/myems/tree/master/myems-aggregation) 负责按多维度汇总能耗、账单和碳排数据。
- [MyEMS Database Design](https://github.com/MyEMS/myems/tree/master/database) 明确描述了按用途分离的多数据库架构，包括 system、historical、energy、billing、carbon、energy baseline、energy model、energy plan、energy prediction、FDD、user 和 reporting 等数据库。
- MyEMS 的系统数据库包含 energy category、energy item、data source、protocol、equipment、meter、point、space、tenant、combined equipment、virtual meter、tariff、calendar、energy flow diagram 和 control mode 等内容模型。
- MyEMS historical database 同时保存历史值与 latest-value 表，并记录数据质量字段；energy database 由 normalization 和 aggregation 生成小时、日、月、年等统计粒度。
- [MyEMS Modbus TCP](https://github.com/MyEMS/myems/tree/master/myems-modbus-tcp) 是独立采集服务，说明 MyEMS 把现场协议采集和 Cloud/central data processing 分开部署。
- 证据边界：MyEMS 官方 README、database 文档和 UI README 对部分 UI 技术栈描述并不完全一致；本对标只采纳稳定的模块职责和数据内容，不把单一 README 的技术栈描述当作架构裁决。

## HVAC Web 当前映射

| 参考能力 | HVAC 当前模块 | 判断 |
| --- | --- | --- |
| OpenEMS Channel / Process Image / Cycle / Controller / Scheduler | `libs/edgecontrol` | 已有控制内核，属于吸收成果 |
| OpenEMS Device/Nature 抽象 | Capability Profile、Component Registry、Point/Channel 映射 | 基础已具备，真实协议仍缺失 |
| OpenEMS Edge App | `tools/eg8200-simulator/internal/simulator/edge_runtime.go` | 目前只在 simulator 组装，没有正式生产 Edge Host |
| OpenEMS Timedata/resend | `libs/edgecontrol/timedata.go`、MQTT publisher、`libs/edgefleet` | 本地历史、队列、快照已有，但 Replay Coordinator 未闭合 |
| ThingsBoard Transport/Integration | `modules/iot` | MQTT transport 已有，仍需清楚区分 Cloud adapter 与 Edge ingress |
| ThingsBoard Entity hierarchy | Tenant/Site/Space/Asset/Device/Point | HVAC 领域模型比通用 Device/Asset 更具体，应保留 |
| ThingsBoard Rule Engine | 当前多个 telemetry/metric/alarm/control module | 应形成事件自动化能力，但不能接管 Edge 快速控制 |
| ThingsBoard Dashboard/Widget/Alias | React UI、Centrifugo、静态 profile/catalog | 实时链路已有，自描述 Manifest 到 UI 尚未闭合 |
| ThingsBoard Edge synchronization | `libs/edgefleet`、MQTT queue、telemetry runtime | 需要统一 Edge 数据同步、版本和 cursor 语义 |
| MyEMS 数据采集/清洗/规范化/汇总 | `modules/iot`、`modules/telemetry`、`modules/metric` | 当前已有分散能力，但尚未形成 MyEMS 式的能源数据 processing chain |
| MyEMS 多数据库能源内容模型 | PostgreSQL、ClickHouse、Redis 与现有领域表 | 方向相近，但 HVAC 仍需补齐 baseline、plan、prediction、carbon、billing、FDD 等明确 ownership |
| MyEMS 能源/计费/碳排/报表 UI | React real mode、telemetry/history 页面 | 当前 UI 更偏运行监控，能源管理报表和管理型分析深度不足 |

## 四方 Backend 模块对比

| Backend 能力 | ThingsBoard | OpenEMS | MyEMS | HVAC 应形成的判断 |
| --- | --- | --- | --- | --- |
| 设备接入 | 多协议 Transport/Integration + converter | Edge Driver/Bridge + Channel | 独立 Modbus TCP acquisition | 采用 adapter/Bridge 分层；协议接入与能源语义分开 |
| 当前状态 | Device actor/cache + telemetry | Channel value/nextValue + Process Image | latest-value 表 | Edge/Cloud 各自明确 current authority，不能只靠 Redis cache |
| 历史数据 | telemetry/time-series platform | Edge/Backend Timedata | historical DB + latest tables | 保留 ClickHouse 历史，同时吸收 MyEMS 的 latest/history、质量和修复语义 |
| 数据处理 | Rule Engine message pipeline | Cycle 内 Controller/Scheduler | cleaning → normalization → aggregation | 建立能源数据处理链；Rule Engine 不能替代 Energy normalization/aggregation |
| 能源模型 | 通用 Entity/Asset/Telemetry | Device Nature/Channel | energy category/item、meter、space、equipment、virtual meter | HVAC 的 Point/Asset 模型需增加能源分类、计量、虚拟测点和能流关系 |
| 多租户/权限 | Tenant/Customer/Entity isolation | Metadata/Edge authorization | tenant/space/user 表和管理界面 | 以 ThingsBoard 权限模型为参考，以 HVAC Registry/Asset ownership 落地 |
| 告警/自动化 | Rule Nodes、Alarm、Notification | Controller/Edge control | 能耗异常、FDD、报表/管理流程 | 分成 Event Rule、Alarm/FDD、Work Order 三类，不做一个万能规则模块 |
| 控制 | RPC/attributes/rule actions | 本地 Controller/Scheduler/Arbiter | 社区组件更偏数据管理；控制能力需按版本/企业版核实 | Cloud command governance + 未来 Edge intent；不把 MyEMS 数据汇总链当实时控制器 |
| 分析内容 | 通用 telemetry/dashboard | EMS control and energy logic | billing、carbon、baseline、plan、prediction、FDD、reporting | MyEMS 是能源业务内容的首要参考，优先补齐可解释的能源指标 |
| UI | 可配置 Dashboard/Widget/Alias | Live Channel/EdgeConfig | 用户分析 Web + 系统管理 Admin | UI 采用 HVAC Asset UX，吸收 TB 的可配置视图和 MyEMS 的报表/能源分析 |
| 扩展方式 | Rule Node、Integration、plugin-like modules | Component/Nature/Controller/Bridge | 独立 Python 服务和数据库处理模块 | 选择“稳定 domain module + 薄 adapter”，不要复制三套扩展机制 |

### 不同参考项目各自最强的地方

- **ThingsBoard 最强：**通用 IoT 平台的多租户、实体关系、消息路由、规则链、实时订阅和 Dashboard 产品化。
- **OpenEMS 最强：**能源设备控制的本地确定性、控制优先级、Channel/Process Image 和设备能力抽象。
- **MyEMS 最强：**能源管理业务内容的完整性，尤其是能源分类、计量分摊、历史治理、规范化、能耗/账单/碳排汇总、基线、预测和报表。
- **HVAC 当前最需要补强：**不要只继续增强 Telemetry/IoT 通道；要把 MyEMS 的能源内容模型和分析处理链提升为 Backend 一级模块，同时保留 OpenEMS 的未来控制 seam 和 ThingsBoard 的平台化权限/视图能力。

## ADOPT / ADAPT / REJECT

### ADOPT

#### From ThingsBoard

- Tenant → Customer/组织 → Site/Space/Asset/Device 的层级访问模型和关系查询。
- Transport/Integration/Converter 的接入模式：协议细节进入 adapter，平台内部接收统一消息。
- Rule Chain/Rule Node 的事件自动化模型，用于告警、通知、数据丰富、工作流和外部发布。
- Edge 本地持久化后再同步 Cloud 的离线模型，以及可选择性同步的策略。
- Dashboard state、Entity Alias、Widget datasource、time window 和动作配置。
- 设备 profile、能力声明和现场 Edge 配置下发的产品化经验。

#### From OpenEMS

- IPO、固定 cycle、不可变 Process Image、Channel `current/nextValue`。
- Controller、Scheduler、Arbiter 和按安全优先级执行的控制语义。
- Capability Profile 作为 HVAC Nature；控制器依赖能力，不依赖厂商型号。
- Driver 与 Protocol Bridge 分离；Bridge 拥有连接、调度、重试、序列化和连接故障。
- Edge Timedata、本地最新值/历史、full snapshot、historic resend 和成功游标推进。
- Edge Manifest 自描述；simulator 和 physical Driver 共用生产控制路径。

#### From MyEMS

- 能源分类、能源品种/能源项、计量点、设备、空间、虚拟计量、组合设备和能流图等业务内容模型。
- 清洗、规范化、汇总三段处理链；把原始采集值与可用于报表/考核的标准化能源值区分开。
- 按历史、能源、计费、碳、基线、计划、预测、FDD、报表等用途拆分数据 ownership，而不是把所有能源数据都当作一类 telemetry。
- latest-value 与历史值并存的查询优化，数据质量标记、数据修复和 UTC 时间统一。
- tariff、working calendar、billing、carbon factor、energy baseline 和 reporting 作为一等能源领域模块。

### ADAPT

- ThingsBoard 的通用 Entity 要适配成 HVAC 的 Tenant/Site/Space/Asset/Device/Point；Point 是设备测点的 canonical identity，Channel 是 Edge live runtime object。
- ThingsBoard Rule Engine 要放在 Cloud/Edge Data Plane 的事件侧；安全闭环、设备保护和秒级控制必须进入 OpenEMS 风格 Edge Cycle。
- ThingsBoard Edge 的同步模型要适配 HVAC 的 MQTT TLS、Cloud Telemetry Runtime、ClickHouse 历史和 durable Command lifecycle。
- OpenEMS EdgeConfig 要适配成 Edge Manifest + Cloud Registry reconciliation；Manifest 描述运行时能力，Registry 仍是 Cloud 产品主数据 authority。
- OpenEMS Backend 的 Timedata 要适配成 Edge local Timedata 与 Cloud analytical history 的双 ownership，不能让单一存储承担两边职责。
- OpenEMS UI 的 Channel/Config 自描述要适配成 Asset-centered UX；动态能力驱动控件，但不能牺牲 HVAC 的空间、资产、告警和工单语义。
- MyEMS 的能源内容模型要适配到 HVAC 的 Tenant/Site/Space/Asset/Device/Point；不能直接复制 MyEMS 的表结构，也不能假设一个 database per domain 就自动等于清晰 ownership。
- MyEMS 的清洗/规范化/汇总要适配成可重跑、可审计的 Backend processing module，并明确原始值、修正值、标准值、派生指标和报告值之间的关系。
- MyEMS 的报表/分析模块要适配为产品化 read model 和查询模块，避免让 UI 直接依赖几十个领域数据库。

### REJECT

- 不直接采用 OpenEMS Java/OSGi/Backend/UI 作为 HVAC 的目标依赖；ADR 0012 的 `TECH-001` 已明确拒绝技术栈移植。
- 不用 ThingsBoard Rule Engine 代替 Edge Controller/Scheduler/Arbiter；通用规则链不是安全实时控制器。
- 不把 ThingsBoard 的 generic Device/Asset 直接当成 HVAC 的完整主数据模型。
- 不把 Edge Manifest 变成 Cloud Registry 的替代品。
- 不因为对标 ThingsBoard 就提前建设所有协议、通用低代码规则和多节点基础设施；每项能力必须由真实 HVAC 场景驱动。
- 不把 MyEMS 的多数据库数量直接照搬；数据库拆分必须由数据 ownership、写入模式、查询模式、保留周期和扩展压力证明。
- 不把 MyEMS 的定时清洗/汇总任务直接当成实时流处理或闭环控制；两者的 freshness、失败恢复和一致性契约不同。

## 目标架构

```text
┌──────────────────────── HVAC Energy Experience ────────────────────────┐
│ Asset-centered UI · Dashboard/Widget · Entity Alias · Live Channel UI  │
│ Manifest/Capability-driven controls · Alarm/Work Order · Reports       │
└───────────────────────────────┬────────────────────────────────────────┘
                                │ HTTP / realtime read model
┌───────────────────────────────▼────────────────────────────────────────┐
│                         HVAC Cloud Energy Platform                      │
│ IAM/Tenant/Registry · Relation Graph · Telemetry Runtime/Query          │
│ Rule & Alarm Automation · Command Governance/Approval/Audit             │
│ Energy Normalization · Aggregation · Billing · Carbon · Baseline        │
│ Forecast · Optimization · FDD · Metric · Work Order · Reporting          │
│ MQTT/HTTP/Integration adapters · Manifest reconciliation · Fleet        │
└───────────────────────────────┬────────────────────────────────────────┘
                                │ MQTT TLS: telemetry / intent / sync
┌───────────────────────────────▼────────────────────────────────────────┐
│                         HVAC Edge Control Plane                         │
│ Edge Runtime Host · Manifest · local Timedata · Replay Coordinator      │
│ Channel / Process Image / Cycle · Controller / Scheduler / Arbiter       │
│ Safety / Interlock · Capability Profile · Device Driver · Protocol Bridge│
│ Local Rule/Alarm processing · Store & Forward · leased Cloud Intent      │
└───────────────────────────────┬────────────────────────────────────────┘
                                │ protocol-specific I/O
                         HVAC equipment / meters / sensors
```

## 分阶段路线

### P0：统一架构基线

- 将 `edge-control-plane.v1.json` 扩展为 ThingsBoard × OpenEMS × MyEMS 三参考记录，并保留各参考项目的官方 source review。
- 固定 ownership：Cloud Registry、Cloud Telemetry Runtime、Edge Process Image、Edge Timedata、Command Governance、Edge Intent。
- 固定统一消息分类：telemetry、attribute/config、alarm/event、command intent、readback/evidence、manifest/sync。
- 清理 alignment matrix 与 edge-control contract 的状态漂移，避免同一能力同时被记录为 MISSING 和 VERIFIED。

### P1：Backend 能源数据闭环与 UI 纵向切片

- 以 simulator/MQTT fixture 作为输入，先打通 Registry → ingestion → current/history → realtime → React UI。
- 明确原始 telemetry、清洗后值、规范化能源值、小时/日/月/年汇总值和报告值的 processing chain。
- 优先实现能源分类、设备/空间计量、latest/history、数据质量、基础能耗指标和历史趋势。
- 暂不开发生产 Edge Host/真实 Protocol Bridge；仅冻结未来 Edge 的 command/manifest/sync contract，保持 simulator adapter 可替换。

### P2：平台化 IoT 能力

- 建立关系型 Entity Read Model：Tenant/Site/Space/Asset/Device/Point 与 Edge Manifest/Capability Profile 的 reconciliation。
- 引入最小可用 Rule/Alarm automation：从当前真实告警、通知、维护和联动场景开始，不先做通用低代码平台。
- 将 Manifest/Capability 生成 UI 的控制 schema 和 telemetry projection，逐步减少静态设备字段。
- 增加 ThingsBoard 风格 Dashboard state、Entity Alias、Widget datasource 和时间窗口，但继续保持 Asset-centered UX。
- 增加 MyEMS 风格能源报表、能耗对比、账单/碳排、基线和数据修复流程；这些属于 Backend 内容模块，不只是 UI 图表。

### P3：控制、智能分析与 Edge 回归

- 在 Cloud 侧发展 Forecast、Tariff、Optimization、FDD、Peak Demand、Carbon 和多站点组合优化。
- 将 Optimization 输出约束为 Cloud Intent，统一经过 Edge lease、local arbitration、readback 和 evidence。
- 建立 Edge release、signed manifest、配置快照、灰度和回滚；这是生产 fleet 能力，不是单纯的 IoT 接入能力。
- 在 Backend/UI 稳定后，再把 Cloud Command 接到 simulator Edge Intent，验证治理、仲裁、readback 和证据链。
- 最后再决定生产 Edge Host、真实 Protocol Bridge 和 Edge Timedata Replay 的交付顺序。

## 成功标准

- 断网时，Edge 仍能执行安全控制、保留本地数据、管理本地告警，并在恢复后有证据可验证地收敛。
- 新增一个设备型号时，主要增加 Driver/Bridge mapping 和 Capability Profile，不修改 Controller、Cloud rule 和 UI 页面核心逻辑。
- 一个 Cloud command 能区分 accepted、delivered、applied、readback verified、constrained、expired 和 outcome unknown。
- UI 能从 Asset/Manifest/Capability 生成真实控件，不依赖静态厂商字段；同时保留 Tenant、Site、Space、Asset 的产品上下文。
- Cloud Rule Engine 的失败不会破坏 Edge 本地安全闭环；Edge Controller 的故障能被 Cloud 观察、告警和审计。
