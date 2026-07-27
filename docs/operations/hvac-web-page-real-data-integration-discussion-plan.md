# HVAC Web 页面真实数据对接讨论计划

## Status

本文件定义 Real Mode Shell 之后的页面真实数据对接讨论方式。它不推进 S4–S7，不把尚未确认的 UI 演示功能自动转化为后端建设任务。

## 原则

页面需要单独讨论真实数据对接，但讨论边界按共享数据 Owner、Read Model 和业务口径划分，而不是按每个页面组件或卡片重复讨论。

同一权威数据被多个页面消费时，只定义一次契约：

- Dashboard 与 BigScreen 共享 Site 运营摘要 Read Model；
- Energy 与 Cost 共享计量、时间聚合和费用口径；
- Assets 与 Commands 共享 Registry Device/Site 身份，但各自保留领域 Owner；
- System 只消费平台状态、Principal、Route 和 Registry 诊断，不拥有业务数据。

## 讨论顺序

### G1 — Assets 与 Commands

优先级最高，因为 S1 Registry、S2 Telemetry 和 S3 Command 已有真实链路。

需要确认：

- Assets 列表与详情的正式字段；
- Equipment 与 Device 的展示关系；
- Presence、Telemetry Readiness、Last Known Value 的页面语义；
- 历史曲线的时间范围、点位选择、采样与降采样；
- Commands 的可控 Device 来源；
- Capability 目录、参数边界、风险和审批展示；
- Command 当前状态、实时状态、恢复和 OUTCOME_UNKNOWN；
- 哪些现有静态设备元数据必须删除或迁入 Registry。

输出：一份 Assets/Commands 数据契约、页面状态矩阵和纵向 Tickets。

### G2 — Dashboard 与 BigScreen

两者必须使用同一 Site 运营摘要和相同 KPI 口径，BigScreen 不拥有独立数据源。

需要确认：

- 用户进入 Dashboard 后必须回答的运营问题；
- 第一阶段保留的 KPI 与删除的演示 KPI；
- 在线、离线、过期、不可用的设备汇总口径；
- 当前功率、设定值偏差、设备运行矩阵的来源；
- 缺失 Alarm、FDD、Optimize 时区块如何呈现；
- Site Operations Summary BFF/Read Model 的字段、时效和覆盖率；
- Dashboard 与 BigScreen 的刷新、实时和降级规则。

输出：共享 Site Operations Summary 契约及两个展示 Adapter 的验收标准。

### G3 — Energy 与 Cost

两者共享暖通电能事实，但费用必须由后端版本化计价口径拥有。

需要确认：

- 真实计量点、设备类别与计量边界；
- 年/月/周/日分析的业务问题和钻取路径；
- Site IANA 时区、业务日、峰平谷区间；
- 原始采样、小时/日/月聚合和修订语义；
- 数据覆盖率、缺口、重算和迟到数据；
- 基线、同比、环比、节能率是否一期建设；
- 电价合同、币种、税费、生效时间和费用重算；
- 水、燃气、冷量的“待接入”状态。

输出：Energy Read Model、Cost Read Model、公共时间语义及页面 Tickets。

### G4 — System 平台状态

第一阶段只做真实只读诊断和平台状态，不建设 Mock 用户、规则和管理 CRUD。

需要确认：

- Principal、Session、Policy Revision 的展示；
- Gateway Health、Build Identity、Route Ownership 的展示；
- Registry Site 信息和 Revision；
- S2/S3 服务状态是否需要专用聚合端点；
- 错误 Trace ID、重试和诊断信息；
- 用户、角色、规则、审计查询 Tab 的隐藏或 Not Integrated 策略。

输出：System Status 页面契约和只读验收清单。

## 暂不讨论后端契约的页面

以下模块已决定不进入当前真实化建设：

- Alarm；
- Work Order；
- FDD；
- Optimize；
- AI Investigation；
- System 用户、角色、规则和完整审计管理。

它们当前只需要统一 Not Integrated 页面或部署隐藏策略，不应为现有 Mock UI 临时设计数据库或 API。

## 每组讨论必须回答的标准问题

1. 页面帮助用户做出什么判断或动作？
2. 每个显示事实的权威 Owner 是谁？
3. 数据来自现有领域接口、简单前端派生、Gateway BFF，还是持久化 Read Model？
4. 公共 DTO 包含哪些字段、单位、枚举和 Revision？
5. Organization、Site、Device 和时间范围如何显式表达？
6. `asOf`、watermark、覆盖率、Freshness、Quality 和部分数据如何呈现？
7. 有效 Capability 如何控制页面、字段和动作？
8. Snapshot、Delta、轮询和手动刷新分别适用于什么数据？
9. 未接入、无权限、无数据、过期、部分覆盖、不可用和契约错误如何区分？
10. 写操作是否存在；若存在，如何处理 CSRF、幂等、Revision、冲突和审计？
11. 当前 Mock/静态常量如何删除、隔离或迁移？
12. 哪些浏览器行为和后端契约证据证明页面已经真实打通？

## 讨论与实施关系

- 页面数据契约可以在 RMS-01/RMS-02 实施期间并行讨论。
- 页面代码迁移必须等待所需 Shell Ticket 完成，特别是 Capability、Site-scoped 路由和 Scope Purge。
- 每组讨论完成后先形成 Spec 和纵向 Ticket，再进入实现。
- 不要求所有页面讨论完成后才开始实现；按 G1 → G2 → G3 → G4 工作 frontier 推进。

## 推荐下一步

先讨论 G1：Assets 与 Commands。它们已有最多真实后端能力，可以最快明确“现有链路直接复用”和“仍需补契约”的边界，并为后续 Dashboard、Energy 和 Cost 提供真实 Site/Device 基础。