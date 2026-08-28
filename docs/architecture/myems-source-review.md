# MyEMS 源码审查记录

状态：REVIEWED / v6.7.0  
上游仓库：https://github.com/MyEMS/myems  
固定发布：v6.7.0  
固定提交标识：be6e6ce8ddeac57afb04bddb9621501fb555cab0  
审查日期：2026-08-24

## 1. 审查边界

本次只审查 MyEMS 对 Backend/UI 规划有直接影响的内容：

- API 与模块组合方式；
- 历史数据 cleaning、normalization、aggregation 处理链；
- system、historical、energy、billing、carbon、baseline、plan、prediction、FDD、reporting 等数据内容；
- Modbus TCP acquisition 与中心处理的部署分工；
- admin/web UI 的职责分工。

不把 MyEMS 的 README 宣传语、单一 UI 技术栈描述或默认端口当成目标架构证据。

## 2. 已审查的官方源码与文档

固定 tag 的官方入口：

- [v6.7.0 release](https://github.com/MyEMS/myems/releases/tag/v6.7.0)
- [README.md](https://github.com/MyEMS/myems/blob/v6.7.0/README.md)
- [myems-api](https://github.com/MyEMS/myems/tree/v6.7.0/myems-api)
- [myems-modbus-tcp](https://github.com/MyEMS/myems/tree/v6.7.0/myems-modbus-tcp)
- [myems-cleaning](https://github.com/MyEMS/myems/tree/v6.7.0/myems-cleaning)
- [myems-normalization](https://github.com/MyEMS/myems/tree/v6.7.0/myems-normalization)
- [myems-aggregation](https://github.com/MyEMS/myems/tree/v6.7.0/myems-aggregation)
- [database](https://github.com/MyEMS/myems/tree/v6.7.0/database)
- [myems-admin](https://github.com/MyEMS/myems/tree/v6.7.0/myems-admin)
- [myems-web](https://github.com/MyEMS/myems/tree/v6.7.0/myems-web)

| 证据 | 观察到的事实 | 对目标架构的意义 |
| --- | --- | --- |
| README.md | 仓库根目录把 myems-api、myems-admin、myems-web、myems-modbus-tcp、myems-cleaning、myems-normalization、myems-aggregation、database 列为主要模块 | MyEMS 把采集、中心 API、数据处理、管理 UI、用户 UI 和数据库内容明确分开 |
| myems-api | 官方模块目录说明它是面向 MyEMS 组件和第三方应用的 REST API，包含 app.py、core、reports 等职责 | API 是中心产品入口；报表查询属于后端 read model，不应由 UI 直接拼装数据库 |
| myems-modbus-tcp | 独立的 Modbus TCP 采集模块 | 协议 acquisition 应与能源数据处理解耦；当前阶段可用 MQTT/simulator adapter 替代真实协议，但不能把 adapter 当成能源领域 |
| myems-cleaning | 独立历史数据清洗模块 | 原始采集值和可分析值不是同一种事实；需要质量状态和可重跑处理记录 |
| myems-normalization | 独立历史能源规范化模块，包含 meter、virtualmeter、virtualpoint、data repair 等内容 | 计量绑定、虚拟计量、数据修复是能源域的一部分，不能只归到 generic telemetry |
| myems-aggregation | 独立汇总模块，覆盖 energy、billing、carbon、tariff、space/store/tenant 等多维内容 | 能源、账单、碳排和空间维度应成为 Backend 一级内容模块，而不是 UI 临时计算 |
| database | 官方数据库设计按 system、historical、energy、billing、carbon、energy baseline、energy model、energy plan、energy prediction、FDD、user、reporting、production 等用途组织 | 目标架构要按数据 ownership 和处理生命周期分层；不直接照搬“一个用途一个物理数据库” |
| database historical 设计 | 同时存在原始历史值与 latest 值，并包含质量/发布等字段和 UTC 时间语义 | Current 与 History 必须有明确 authority、质量和时间契约 |
| myems-admin | 面向系统设置、数据源、计量表、点、空间、费率、绑定关系等配置操作 | 管理 UI 和能源分析 UI 是不同工作空间，不应混成单一看板 |
| myems-web | 面向能源数据可视化、趋势、空间、设备和报表，release notes 继续增加报表分析能力 | UI 需要围绕能源业务任务组织，而不只是设备 telemetry 监控 |
| v6.7.0 release notes | 增加能流图环检查、计量表绑定重复检查、空间总览、趋势 min/max/avg 等 | 图结构约束、绑定唯一性和分析统计是实际产品不变量，不是可选装饰 |

## 3. 源码导出的结构结论

### 3.1 采集、处理、查询是三类不同职责

MyEMS 的模块拆分不是简单的“多个进程”。其关键区别是：

~~~text
Protocol Acquisition
        ↓
Historical Cleaning
        ↓
Energy Normalization / Repair
        ↓
Energy / Billing / Carbon Aggregation
        ↓
API Read Models / Reports / UI
~~~

采集值、质量修正后的值、规范化能源值、汇总值和报告值具有不同的语义、重算条件和保留周期。它们不能只共享一个 telemetry 表再靠查询参数区分。

### 3.2 能源管理模型不是 generic IoT 模型的别名

MyEMS 的 system 内容包含 energy category/item、equipment、meter、point、space、tenant、virtual meter、tariff、calendar、energy flow 等。这个模型直接服务于分摊、对比、账单、碳排和报告。

因此本项目的 Device/Point/Telemetry 模型不能被视为能源管理 Backend 的完整模型。至少需要补齐：

- Energy Category / Energy Item；
- Meter 与 Meter Binding；
- Virtual Meter / Virtual Point；
- Space / Asset 的计量归属；
- Tariff / Calendar；
- Carbon Factor；
- Baseline / Plan / Forecast；
- Billing / Carbon / FDD / Reporting 的数据 ownership。

### 3.3 数据库数量不是架构目标

MyEMS 的多数据库组织提供了清晰的用途分区，但不能推出“本项目也必须创建同样数量的数据库”。本项目应先固定逻辑数据集、事实类型、权威归属、重算边界和查询模式，再决定 PostgreSQL schema、ClickHouse 表或独立数据库。

### 3.4 Admin 与 Web 是两个产品任务

Admin 解决配置、绑定、权限和系统维护；Web 解决趋势、对比、能流、能耗、报表和运行分析。目标 UI 应保留这种任务分工，即使第一阶段仍使用一个 React 工程和一个部署单元。

## 4. ADOPT / ADAPT / REJECT

### ADOPT

- 清洗 → 规范化 → 汇总的能源数据处理链；
- 原始值、latest/current、标准化能源事实、汇总事实和报告 read model 的语义分离；
- Meter、Virtual Meter、Energy Category、Tariff、Carbon Factor、Baseline、Billing、FDD、Reporting 作为 Backend 内容模块；
- Admin 与用户能源分析 UI 的任务分离；
- 数据修复必须留下质量和处理记录，而不是覆盖原始 telemetry；
- 能流图和 meter binding 的环检查/重复性检查作为业务不变量。

### ADAPT

- MyEMS 的模块拆分适配为本项目现有 Go 进程中的逻辑 module；当前不因为有多个逻辑模块就立刻拆成多个生产进程；
- MyEMS 的多数据库用途适配为 PostgreSQL + ClickHouse + Redis + Object Storage 的逻辑数据集，物理拆分由写入/读取压力证明；
- MyEMS 的 Modbus acquisition 适配为 MQTT/HTTP/simulator adapter；未来真实 Modbus/BACnet/OPC UA 仍必须通过同一 acquisition seam；
- MyEMS 的 REST 报表接口适配为面向 read model 的 Backend query module，不允许 UI 直接依赖几十个领域表；
- MyEMS 的空间/设备层级适配为本项目的 Tenant → Site → Space → Asset → Device → Point，并保持 Point 的 canonical identity。

### REJECT

- 不复制 MyEMS 的全部物理数据库数量；
- 不把定时 normalization/aggregation worker 当作秒级控制器；
- 不把 MyEMS 采集模块直接当作 HVAC Edge Control Plane；
- 不把单一 UI README 的技术栈描述当成架构裁决；
- 不复制 MyEMS 表结构作为本项目 schema；只吸收职责、事实类型和不变量。

## 5. 对本项目的直接修改意见

以下是源码审查后的 LOCAL-CHANGE，不是“可选优化”：

1. 将 Backend 顶层规划从“Telemetry + Metric”改为“Telemetry ingestion + Energy data processing + Energy business content”；
2. 新增 cleaning、normalization、aggregation 的明确 module ownership 和 run/quality/lineage 记录；
3. 在 Registry/Point 之上新增 Meter、Virtual Meter、Energy Category/Item、Tariff、Carbon、Baseline、Billing、FDD、Reporting 的领域 seam；
4. 将当前偏运行监控的 UI 拆为 Operations、Energy Management、Administration 三个工作空间；
5. 将现有报表/趋势查询从通用 telemetry 查询中抽出，面向稳定 read model 提供接口；
6. 对 meter binding、virtual meter dependency、energy flow graph 增加发布前一致性检查；
7. 保留现有单服务器部署方式，但删除“未来再补能源 processing chain”的模糊表述，P1 即建立最小可重跑闭环。

## 6. 尚未审查、禁止下结论的内容

- MyEMS 所有企业版能力与控制闭环的完整行为；
- MyEMS 当前 master 在 v6.7.0 之后的未发布变更；
- MyEMS UI 全量组件和页面之间的稳定接口；
- MyEMS 的生产级故障恢复、幂等和跨模块事务契约。
