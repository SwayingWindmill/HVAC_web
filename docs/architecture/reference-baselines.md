# 三参考项目源码基线与裁决规则

状态：PROPOSED / SOURCE-ALIGNED  
适用范围：Backend、数据处理、集成、UI；不改变当前 Phase 1 单服务器部署基线，也不把 Edge 网关纳入本阶段交付。

## 1. 这份文档解决什么问题

本项目不能把现有实现当成架构正确性的证明。当前代码、已有架构文档和历史 ADR 都必须重新和参考项目的源码、官方测试及官方文档比较。

本文件规定三份参考项目的固定审查基线、证据优先级和结果表达方式。后续 Backend/UI 设计文档只能引用这些基线或明确标注尚未审查的内容。

## 2. 固定审查基线

| 项目 | 固定版本 | 固定提交 | 本次审查范围 | HVAC 采用边界 |
| --- | --- | --- | --- | --- |
| ThingsBoard CE | v4.3.1.1 | c2a52e46c44e308ddee430e7266b8e10eddde9c4 | Tenant/Customer、实体关系、Transport/Integration、Rule Engine、Telemetry、Dashboard/Widget、Edge 同步 | Backend 平台化能力、事件自动化、实时视图；不引入 ThingsBoard 运行时依赖 |
| OpenEMS | 2026.7.0 | 2e2792d | Edge Channel、Process Image、Cycle、Controller、Scheduler、Timedata、Backend 连接和 UI 自描述 | 后续 Edge Control Plane 的控制语义；当前 Backend/UI 仅冻结 seam |
| MyEMS | v6.7.0 | be6e6ce8ddeac57afb04bddb9621501fb555cab0 | API、cleaning、normalization、aggregation、database、admin/web UI、Modbus acquisition | Backend 能源内容模型、数据处理链、报表与管理 UI |

MyEMS v6.7.0 已通过官方 tag 解析到完整提交 `be6e6ce8ddeac57afb04bddb9621501fb555cab0`，本次逐文件研究记录见 `docs/research/wayfinder-energy-reference-source-review-2026-08.md`。基线仍保持 SOURCE-ALIGNED；未覆盖的行为继续标记为 VERIFY，不因固定提交而自动成为 VERIFIED。

## 3. 证据优先级

从高到低：

1. 固定提交中的生产源码；
2. 固定提交中的官方测试、数据库 schema 和迁移；
3. 参考项目官方文档；
4. 官方 README 和 release notes；
5. 本项目现有实现；
6. 架构讨论、截图、二手文章和个人推断。

高优先级证据与本项目现状冲突时，现状默认被标记为 UNVERIFIED，不因为已经存在就获得保留资格。

## 4. 结果标签

- ADOPT：参考机制的职责和不变量直接进入目标架构。
- ADAPT：保留机制，但按 HVAC 的领域对象、部署范围或技术栈改写。
- REJECT：明确不采用，并写出原因。
- VERIFY：证据不足，禁止在目标架构中当作事实使用。
- LOCAL-CHANGE：参考证据表明本项目需要修改，而不是让参考项目适配本项目。

每个 LOCAL-CHANGE 必须回答三件事：

1. 当前本项目的假设是什么；
2. 参考源码证明了什么；
3. 需要删除、重命名、拆分或新增什么。

## 5. 已有本地文档的处理方式

以下文档继续作为已有范围内的约束，但不作为三参考项目的替代证据：

- docs/architecture/thingsboard-source-review.md
- docs/architecture/thingsboard-target-domain-model.md
- docs/architecture/openems-source-review.md
- docs/architecture/data-architecture-v2-conformance.md
- docs/architecture/phase1-overall-architecture.md

其中 data-architecture-v2-conformance.md 和 phase1-overall-architecture.md 是当前项目的部署/数据基线；它们不等于参考项目的结论。如果新审查发现本项目的模型、模块边界或处理链不合理，应在本轮架构变更清单中直接提出修改，而不是用“当前已经这样实现”结束讨论。

ThingsBoard 在本项目中仍然是隔离的架构参考和测试 fixture 来源，不恢复为当前生产运行时依赖。OpenEMS Edge 仍然是未来现场控制面的参考，不因为本轮聚焦 Backend/UI 就把 Edge 伪装成已经完成。

## 6. 研究记录索引

| 主题 | 记录 |
| --- | --- |
| 三项目事实对比 | docs/architecture/thingsboard-openems-benchmark.md |
| ThingsBoard 源码审查 | docs/architecture/thingsboard-source-review.md |
| ThingsBoard 目标领域模型 | docs/architecture/thingsboard-target-domain-model.md |
| OpenEMS 源码审查 | docs/architecture/openems-source-review.md |
| MyEMS 源码审查 | docs/architecture/myems-source-review.md |
| 需要修改本项目的事项 | docs/architecture/architecture-change-list.md |
| 部署总体架构 | docs/architecture/deployment-architecture-v1.md |
| 部署拓扑矩阵 | docs/architecture/deployment-topology-matrix-v1.md |
| 部署发布与恢复 | docs/architecture/deployment-release-and-recovery-v1.md |
| 部署参考项目审查 | docs/architecture/deployment-reference-review.md |
| Wayfinder #303 三方能源源码证据 | docs/research/wayfinder-energy-reference-source-review-2026-08.md |
| Wayfinder #302 能源领域词汇与首个纵向切片 | docs/architecture/energy-domain-and-slice-v1.md |
| Wayfinder #304 能源模块所有权与接口 seams | docs/architecture/energy-module-ownership-v1.md |
| Wayfinder #305 能源数据链与事实生命周期 | docs/architecture/energy-data-lifecycle-v1.md |
| Wayfinder #306 Energy Fact、MeterBinding、质量与溯源契约 | docs/architecture/energy-fact-meter-binding-contract-v1.md |
| Wayfinder #307 Backend Energy Content 查询契约 | docs/architecture/energy-content-query-contract-v1.md |
| Wayfinder #308 三个工作空间的 UI 信息架构 | docs/architecture/ui-workspace-information-architecture-v1.md |
| Wayfinder #310 Energy Slice v1 实施规格与验收门禁 | docs/architecture/energy-slice-implementation-spec-v1.md |
