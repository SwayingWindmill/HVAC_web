# Wayfinder #309：从首个电表切片到计量、成本、碳排、报表、基线与优化的扩展顺序

状态：DECIDED / SOURCE-BACKED  
审查日期：2026-08-25  
范围：基于已锁定的 Energy Fact / Energy Content 基础，按源码证据裁决 MyEMS 的计量、费用、碳排、报表、基线能力，以及 ThingsBoard 关系/规则/Dashboard、OpenEMS 控制/调度语义何时进入 HVAC Web；给出依赖顺序、每项能力的领域事实/读模型/权限/人工审批前提和明确后置范围。不实现代码、数据库迁移、部署变更，也不为未到期的能力预埋字段。

## 1. 结论：七波依赖顺序

每一波只有在其全部前置波的领域事实与查询合同稳定后才可开出实施规格票据：

```text
W0 首个 electricity slice 落地并通过 #310 验收门禁（交接执行，不在地图内）
 │
W1 Energy Fact rebuild generation、旧事实 supersede/tombstone、并发恢复语义
 │  （已从雾区毕业为独立票据；一切宣称"历史可修正/结果可复现"的能力都阻塞在它后面）
 ├──────────────┬──────────────┐
 ▼              ▼              ▼
W2 计量广度     W3 成本对账     （W2/W3 可并行）
 第二能源类型    现有 Settlement/Tariff 链 KEEP，
 + Energy       新增 Energy 合同输入适配
 Category；     与对账读模型
 之后 Virtual
 Meter、Offline
 Meter、Space/Asset 分摊
 │
 ▼
W4 碳排：Emission Factor 内容契约 + electricity-first 碳聚合读模型
 │
 ▼
W5 基线：working calendar 内容 + 版本化 Baseline 定义 + 对比读模型
 │
 ▼
W6 报表：组合 Aggregate + Cost + Carbon 的 read model，pin dataset revision
 │
 ▼
W7 优化建议链消费成本+基线合同；闭环 dispatch 执行不在本路线内

横切约束：
- ThingsBoard 关系/规则/Dashboard 能力作为平台能力随 W2–W6 消费稳定 Energy 合同，
  在任何波次都不得拥有能源事实或 MeterBinding。
- OpenEMS Cycle/Controller/Scheduler 维持 DEFER；只保留 intent/manifest/readback/
  evidence 的未来语义冻结，不产生实施票据（触发条件见雾区）。
```

## 2. 本轮实际核对的证据

### 2.1 HVAC 本地实现（本轮逐文件核对）

| 文件 | 实际观察 | 对扩展顺序的约束 |
| --- | --- | --- |
| [`services/settlement-service/internal/settlement/postgres.go`](../../services/settlement-service/internal/settlement/postgres.go) | 结算按 `core_registry.tariff_assignments JOIN tariff_versions JOIN tariff_periods` 取得 RELEASED 且事件时间命中的费率版本，并读取 `settlement_metric_bindings` 绑定 Metric 角色。 | 费用链已存在且带版本/生效期/发布状态治理。 |
| [`services/settlement-service/internal/settlement/clickhouse.go`](../../services/settlement-service/internal/settlement/clickhouse.go) | 结算数量来源是 `analytics.metric_result_facts`（Metric Engine 结果），**不读取 `analytics.energy_interval_facts`**。 | 当前成本不依赖 Energy Fact/MeterBinding；这是需要显式裁决的分叉，不是已完成的对齐。 |
| [`infra/s1-registry/postgres/init/009b-settlement-foundation-v2.sql`](../../infra/s1-registry/postgres/init/009b-settlement-foundation-v2.sql) | 定义 settlement_boundaries/edges、tariffs、tariff_versions、tariff_periods、tariff_assignments、settlement_metric_bindings、settlement_periods/snapshots/change_candidates/revisions。 | Tariff 与结算快照/修订治理已是产品级内容模块。 |
| [`infra/s1-registry/postgres/init/009e-forecast-model-v2.sql`](../../infra/s1-registry/postgres/init/009e-forecast-model-v2.sql)、[`009f-optimization-model-v2.sql`](../../infra/s1-registry/postgres/init/009f-optimization-model-v2.sql) | forecast 有 feature set/model/training run/deployment/job 快照全链；optimization 有 policy version/input snapshot/run/dispatch_plans/dispatch_intervals。forecast 算法枚举含 `'BASELINE'`。 | 预测与优化已有 Registry 模型；其中 "BASELINE" 是 ML 算法选项，不是 MyEMS 式确定性能耗基线。 |
| [`infra/s1-registry/postgres/init/013-s22-intelligence-products.sql`](../../infra/s1-registry/postgres/init/013-s22-intelligence-products.sql) | ai_model_definitions/data egress/deployment/invocations、fdd_findings、optimization_recommendations；intelligence product 带 `baseline jsonb` 列。 | FDD/优化推荐走智能产品链；其 baseline 是产品快照字段，不是能源域 Baseline 事实。 |
| 全库检索 `emission|carbon_factor` 于 `infra/s1-registry/postgres/init/` | **零命中**。全仓库无 Emission Factor 或碳排模型。 | 碳排是 greenfield：必须先立内容契约，不得提前进实施路线。 |
| 全库检索 baseline 一级模型 | 除上述算法枚举与 jsonb 字段外无专门表。 | 能源 Baseline 同样是 greenfield 内容模块。 |
| `services/` 目录清单 | settlement、forecast、fdd、optimization、rule-runtime 均有真实 internal 实现（engine/service/runtime/jobs），非占位。 | 这些链路的裁决对象是"接入什么输入、何时进入"，不是"要不要新建"。 |

### 2.2 上游源码证据（引用已固定的审查记录）

上游三方均已在 [Wayfinder #303 研究记录](wayfinder-energy-reference-source-review-2026-08.md) 固定到具体提交并逐文件核对（ThingsBoard CE v4.3.1.1 `c2a52e46…`、OpenEMS 2026.7.0 `2e2792d…`、MyEMS v6.7.0 `be6e6ce8…`）；[MyEMS 源码审查](../architecture/myems-source-review.md) 与 [对标研究](../architecture/thingsboard-openems-benchmark.md) 记录了与本票据相关的文件级证据。本票据直接引用其中与扩展顺序相关的结论：

- MyEMS normalization 按 physical → virtual → offline meter 顺序生成小时能耗，data repair 留下质量与处理记录（`myems-normalization/meter.py`、`virtualmeter.py`、`offlinemeter.py`）；aggregation 同时计算 energy、billing、carbon，且都以规范化后的计量数据为输入（`myems-aggregation/main.py`、`meter_billing.py`）。
- MyEMS API 将 tariffs、billing、carbon、baseline、prediction、reports 注册为中心查询资源（`myems-api/app.py`）；Energy Category 是独立管理资源（`category.service.js`）。
- ThingsBoard Calculated Field 与关系图影响派生结果，但属于平台派生能力，不是能源清洗/结算/报告事实（`TbCalculatedFieldsNode.java` 及关系聚合集成测试）。
- OpenEMS Cycle/Timedata/Resend 属于 Edge 数据面与控制面，Backend/UI 阶段只冻结语义（`CycleWorker.java`、`ResendHistoricDataWorker.java`）。

## 3. 各能力族裁决

每项能力按下表四要素说明前提；没有对应证据的能力一律不得提前进入实施路线。

### 3.1 计量广度（第二能源类型、Energy Category、Virtual/Offline Meter、Space/Asset 分摊）

- **裁决：ADOPT（MyEMS 结构）/ ADAPT（按 HVAC 切片推进）**，是整个扩展路线的主干。
- **依赖的领域事实**：canonical Counter delta 与 Interval Fact（W0）；rebuild generation 语义（W1）——Binding 变更、Point revision 变化和历史修复在单站点单表时尚可 fail-fast，多表计/多类型后必然要求可重放修正。
- **依赖的内容模块**：Energy Category/Item 作为 Energy Content 的一级管理内容（对应 MyEMS `energy_category` 独立 CRUD）；Virtual Meter 表达式引用既有 Meter 事实并做环检查（对应 v6.7.0 release notes 的能流图环检查与绑定重复检查，Registry `009a` 已有 overlap 约束雏形）；Offline Meter 需要人工录入通道与审批留痕。
- **依赖的权限/审批**：Energy Content mutation 走 Administration 工作空间的发布-审批流（#307/#308 已定）；Space/Asset 分摊规则属于 released content，修改需重新发布并影响后续 Fact 归属。
- **进入条件**：W1 票据关闭；#310 门禁通过。
- **明确后置**：MyEMS 的 store/shopfloor/tenant 计费分摊对象（本项目当前无此业务场景）；一次引入全部能源类型（每次只做一个类型切片）。

### 3.2 成本（费用）

- **裁决：KEEP（现有 Settlement/Tariff 链）+ ADAPT（新增 Energy 合同输入适配与对账读模型）**。
- 源码证据表明两条链都已存在且各有治理：Tariff 有版本/生效期/发布状态；Settlement 有 boundary、metric binding、period、snapshot、change candidate、revision。MyEMS 证明 billing 是规范化计量数据的聚合输出，而本项目当前用 Metric 结果做输入——这不构成推翻 S19 的理由（Metric 链有独立 Run/Publication/Revision 治理），但构成一条必须显式补上的对账路径：计量电量（Energy Fact 聚合）与结算电量（Metric 结果）必须能互相核对，差异成为 change candidate。
- **依赖的领域事实**：稳定的 Energy Aggregate 查询合同（W0 之后即可开始设计）；可信的历史修正语义（W1），否则对账结论无法复现。
- **依赖的读模型**：Energy Series 合同（已冻结）+ 新增"结算口径 vs 计量口径"对账 read model。
- **依赖的权限/审批**：Settlement 既有审批/修订流不变；对账差异的人工确认进入既有 change candidate 流程。
- **进入条件**：W1 关闭后可与 W2 并行开票；不需要等碳排/基线/报表。
- **明确后置**：分时/阶梯之外的新计费模型；把 billing 直接改接到 Energy Fact 上（在 Metric 链被证明不足之前不做迁移）。

### 3.3 碳排

- **裁决：ADOPT（MyEMS 结构）/ greenfield 实施**，排在计量广度之后。
- 本轮核实仓库无任何 Emission Factor/Carbon 模型。MyEMS 证据显示 carbon = 规范化能耗 × 按能源类别的因子，与 billing 同属 aggregation 输出。因此碳排的前提不是"先有报表"，而是：(a) 多能源类型的计量事实（至少 electricity 先行）；(b) Emission Factor 作为 Energy Content 的一级版本化内容（发布/生效期/审批，结构对齐 Tariff 而非复制其表）。
- **依赖的领域事实**：按 Energy Category 可归组的 Interval Fact/Aggregate（W2 引入 Category 后才可按类别取因子）。
- **依赖的读模型**：碳聚合读模型（electricity-first 切片允许只覆盖电力因子）。
- **依赖的权限/审批**：因子版本的发布审批（Administration）；因子变更不追溯修改已发布聚合，除非走 W1 的 rebuild 流程。
- **进入条件**：W1 + W2（Category 部分）关闭。
- **明确后置**：Scope 2/3 之外的核算体系、组织级碳盘点、碳报告合规格式。

### 3.4 基线

- **裁决：ADAPT（MyEMS 确定性基线）/ 与现有 ML 基线显式分离**。
- 仓库现状有两处名为 baseline 但都不是能源域 Baseline：forecast 算法枚举值 `'BASELINE'`（ML 产物）、S22 智能产品的 `baseline jsonb`（产品快照）。MyEMS 的 baseline 是独立的确定性内容（按对象/日历的基线曲线，配合 working calendar）。两者必须分开命名与归属：能源 Baseline 归 Energy Processing/Content 链，ML 基线留在 forecast/intelligence 链。
- **依赖的领域事实**：足够历史深度的规范能耗事实（依赖 W0/W1 的可重放性）；Energy Category（W2）。
- **依赖的内容模块**：working calendar（节假日/工休日历）作为 released content；Baseline 定义/版本的发布治理。
- **依赖的权限/审批**：基线版本的发布审批；被考核对象可见性沿用工作空间权限。
- **进入条件**：W1 + W2 关闭，且历史窗口内有真实数据积累。
- **明确后置**：M&V 深度方法论、节能量审计级精度声明。

### 3.5 报表

- **裁决：ADAPT（MyEMS 的报表即后端 read model）**，整条路线的最后一段。
- MyEMS 将 reports 实现为 API 层 read model 而非 UI 计算；#304/#305 已锁定 Report 只消费稳定 Aggregate/Query 合同。报表的独特前提是**可复现**：同一份报表必须能绑定到明确的 dataset revision 重出——这只有在 W1 定义了 rebuild generation 之后才可能成立。
- **依赖的领域事实**：Energy Aggregate（W0）、Cost 对账（W3）、Carbon 聚合（W4）、Baseline 对比（W5）中报表声明要覆盖的部分。
- **依赖的读模型**：面向报表的固定 read model + 导出格式；不允许 UI 拼装跨域表。
- **依赖的权限/审批**：导出权限与租户隔离沿用 Gateway 能力模型；对外分发属人工动作。
- **进入条件**：W1 关闭且其覆盖的成本/碳排/基线合同到位。
- **明确后置**：自由拖拽的自助报表编辑器；MyEMS 全量报表清单。

### 3.6 优化

- **裁决：KEEP（既有 policy/run/dispatch 模型）+ 建议链先行；闭环执行不在本路线**。
- `009f` 与 optimization-service 已有完整模型；UI IA（#308）已把优化建议定位为"建议/证据对象，批准后转 Operations Control Preview"。命令侧的 Command Intent 治理证据已存在（command governance 与 readback 已交付）。缺的是输入侧：优化 run 消费的成本与基线输入必须来自 W3/W5 的稳定合同，而不是临时指标。
- **依赖的领域事实**：成本读模型（W3）、基线对比（W5）。
- **依赖的权限/审批**：建议 → 人工审批 → Control Preview 的既有链路不变。
- **进入条件**：W3 + W5 关闭。
- **明确后置**：dispatch plan 到现场设备的自动执行（Edge intent lease、本地仲裁、readback 证据）——等待雾区中的 Edge 触发条件，本路线内优化输出止步于"已批准的建议"。

### 3.7 ThingsBoard 关系/规则/Dashboard

- **裁决：ADAPT LATER，作为平台能力横切跟进，永不拥有能源事实**。
- 关系图：HVAC 的 Tenant→Site→Space→Asset→Device→Point 层级加 Registry 绑定已覆盖当前需求；通用关系图等到 Space/Asset 分摊与跨实体派生真实需要时（W2 内）再裁剪引入。
- 规则：rule-runtime（S20/S21/S22 已交付）继续作为事件自动化面；它消费 Energy 查询合同做能源异常检测的时机在 W2 之后，且不得替代 Energy Processing 的质量判定或聚合。
- Dashboard：Dashboard/Widget/Alias 的可配置化属于 UI 平台化（benchmark P2），以三工作空间信息架构稳定为前提，不阻塞任何能源波次。
- **明确后置**：通用低代码规则编辑器、Widget 市场。

### 3.8 OpenEMS 控制与调度语义

- **裁决：维持 DEFER，无变化**。
- 地图 Notes 已冻结 Edge 的 intent/manifest/readback/evidence 语义；本轮无新证据改变触发条件。Cycle/Controller/Scheduler/Arbiter 的实施规划继续挂在"Backend/UI 稳定后的容量、可靠性与业务门槛"雾区之下，不因本票据产生任何实施票据。

## 4. 明确后置范围汇总

以下内容在本路线内不做，除非目的地重划：

1. Edge 运行时、协议 Bridge、离线同步、闭环控制与优化 dispatch 自动执行；
2. 多写入者/HA Projector 部署形态（跟随雾区的集群评估）；
3. MyEMS 的 store/shopfloor/tenant 计费分摊对象与全量报表清单；
4. 通用低代码规则/Dashboard 编辑器；
5. Scope 2/3 之外的碳核算体系、M&V 深度方法论；
6. 为未到期能力预埋的字段、空测试矩阵或兼容双写。

## 5. 对地图的影响

- 雾区第 1 条（rebuild generation、supersede/tombstone、并发恢复）问题已可精确陈述，毕业为新子票据《裁决 Energy Fact 的 rebuild generation、supersede/tombstone 与并发恢复语义》，它是 W2–W6 全部波次的共同前置。
- 雾区第 2 条（MyEMS 能力如何毕业）由本票据解决，从雾区移除；各波次的进入条件以本文档为准，待其前置波次落地后再逐一升格为独立票据，避免提前切割。
- 雾区第 3 条（HA/集群与 Edge 触发条件）保持雾区不变。
