# ThingsBoard CE 实体、关系、设备与资产档案裁决

状态：`D02_ADJUDICATION_COMPLETE`

审查票：[审查实体模型、关系、设备与资产档案](https://github.com/SwayingWindmill/HVAC_web/issues/237)

本文只裁决 ThingsBoard CE v4.3.1.1 的 Asset、Device、Profile、Relation、Entity Query、Entity View、Assignment/Sharing、Resource/Image、Component Descriptor、Import/Export/Version Control 入口，并将其与 HVAC Web 的 Tenant/Site/Space/Asset/Device/Sensor/Point/Binding 模型做源码级反向审查。设备凭据密码学与传输执行归 D03，Rule Chain/Queue/Component 执行归 D05，Alarm 归 D06，Dashboard/Image 展示归 D07，Edge 同步归 D08，版本库和 Auto-commit 基础设施归 D10。

本文不假设任一方已经正确，也不在本票内改变运行时产品行为。

裁决词汇：

- `ADOPT`：行为和边界可直接成为目标设计；
- `ADAPT`：吸收模式，但按 HVAC Domain、安全、质量和数据权威重做；
- `KEEP`：本地实现有明确场景或正确性证据，应保留；
- `REPLACE`：本地或上游行为存在实质冲突，应由目标行为替换；
- `REJECT`：明确不进入本项目；
- `DEFER`：有潜在价值，但当前没有产品、源码或运行证据支持实施。

## 1. 执行结论

HVAC Web 不应恢复 ThingsBoard 运行时，也不应把当前只读 Registry 误判为“已经完成”。固定上游源码与本地源码对照后的客观结论是：

1. **保留本地显式 HVAC Domain。** Tenant → Site → Space → Asset → Device → Point，以及可选 Sensor、有效期 Binding、UUIDv7、Revision、RLS 和迁移隔离，比 ThingsBoard 的扁平 Asset/Device 加自由 Relation 更能表达物理位置、可维护对象、接入端点和控制点权威。
2. **当前本地 Registry 仍缺完整生产生命周期。** 仓库中有迁移和 Seed 写入，但没有对 Site、Space、Asset、Device、Point、Binding 提供生产运行时的公开 Create/Update/Retire Domain Service/API。当前真实页面和公开 API 主要是只读查询，不能按“资产与设备管理已完成”验收。
3. **吸收 ThingsBoard 完整管理面，不照搬通用实体模型。** CRUD、Profile 默认绑定、引用删除保护、Assignment、Query、Import/Export、External ID、Audit/Lifecycle Event 等能力应进入路线图；实现必须落到显式 HVAC Aggregate 和权限边界。
4. **把 Profile 改造成不可变发布修订。** ThingsBoard Device Profile 同时拥有 Transport、Rule Chain、Queue、Alarm/Calculated Field、OTA、Dashboard、Provisioning 和 Edge 配置，修改后立即影响已分配设备。它覆盖面强，但也是跨域可变“God Profile”。目标应采用不可变 `AssetTemplateRevision`、`DeviceTemplateRevision`、`PointTemplateRevision` 与有效期 Assignment，并仅保存对 D03/D05/D06/D07/D08 配置发布物的稳定引用。
5. **关系图只能作为查询投影，不能成为拓扑权威。** ThingsBoard Relation 没有实体外键、受控类型、环和基数约束，`maxLevel=0` 可无限层遍历直到超时。本地继续以类型化 Binding 表作为权威，`AssetRelationship` 只由 Binding 派生；同时必须修复当前 Device/Point Binding 过宽的通用基数。
6. **保留乐观并发思想，拒绝绕过版本。** ThingsBoard 普通 JPA 实体使用 `@Version` 并把冲突转换为 Version Mismatch；但空 Version 会强制覆盖，CSV Bulk Import 也主动清空 Version。目标写入必须要求 Expected Revision、Idempotency Key 和审计，不允许导入绕开并发控制。
7. **Entity View 的需求值得吸收，实现必须替换。** “只向特定受众暴露指定 Key 和时间窗”有价值，但固定源码先保存 View，再跨服务复制 Attributes/Latest，失败只记录日志；复制值会陈旧，目标存在性/租户/时间范围校验也不足。目标应做读取时授权的 Scoped Data View，不复制业务真值。
8. **删除必须是受治理的 Retirement Saga。** ThingsBoard 主实体删除提交后才执行关系和 Housekeeper 清理，失败不回滚；Housekeeper 缺失时部分任务甚至不会提交。Commissioned HVAC 实体不得靠硬删除和 best-effort 清理维持一致性。
9. **本地查询契约必须从全量快照升级。** `GetSiteAssetModel` 在 Repeatable Read 下提供一致快照是优点，但 Real Assets 在浏览器内过滤/排序且关闭分页，违反产品文档对大 Device 表的服务端分页/游标要求。目标需要类型化、受限、可计费查询与树投影，不需要复制 ThingsBoard 无上限的通用 DSL。
10. **统一词汇并删除旧路径。** 本地 Registry 合同仍混用 Area/Equipment，而 Go、OpenAPI 和 UI 已使用 Space/Asset。按仓库规则应选定 Space/Asset 为唯一词汇，删除旧合同与 Fixture 路径，不增加兼容别名。

因此 D02 的结论不是“本地优于 ThingsBoard”或“照搬 ThingsBoard”，而是：**保留本地强类型 HVAC Aggregate、隔离和有效期模型；用 ThingsBoard 的产品完整度倒逼补齐生命周期、模板、查询、导入导出和审计；同时替换双方已经由源码证明不可靠的关系、Profile、View、删除和并发行为。**

## 2. 固定证据基线

| 证据 | 固定值 |
| --- | --- |
| 官方仓库 | `thingsboard/thingsboard` |
| 版本 | `v4.3.1.1` |
| 提交 | `c2a52e46c44e308ddee430e7266b8e10eddde9c4` |
| 许可证 | Apache-2.0 |
| 本地只读源码 | `C:\Users\HaoZhang\AppData\Local\Temp\thingsboard-v4.3.1.1-src` |
| 全功能目录 | `contracts/architecture/thingsboard-ce-capability-inventory.v1.json` |

上游行为以该固定提交的生产源码、测试、DDL 和运行配置为准；官方文档只解释公开入口，不覆盖源码事实。

主要 ThingsBoard 源码入口：

- [AssetController](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/controller/AssetController.java)、[DeviceController](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/controller/DeviceController.java)；
- [AssetProfileController](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/controller/AssetProfileController.java)、[DeviceProfileController](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/controller/DeviceProfileController.java)；
- [AssetProfileServiceImpl](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/asset/AssetProfileServiceImpl.java)、[DeviceProfileServiceImpl](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/device/DeviceProfileServiceImpl.java)；
- [EntityRelationController](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/controller/EntityRelationController.java)、[BaseRelationService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/relation/BaseRelationService.java)；
- [DefaultEntityQueryService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/query/DefaultEntityQueryService.java)、[EntityDataQuery](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/query/EntityDataQuery.java)、[RelationsQueryFilter](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/query/RelationsQueryFilter.java)；
- [EntityViewController](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/controller/EntityViewController.java)、[DefaultTbEntityViewService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/entitiy/entityview/DefaultTbEntityViewService.java)、[EntityViewDataValidator](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/service/validator/EntityViewDataValidator.java)；
- [CleanUpService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/housekeeper/CleanUpService.java)；
- [BaseVersionedEntity](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/model/BaseVersionedEntity.java)、[JpaAbstractDao](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/sql/JpaAbstractDao.java)；
- [AbstractBulkImportService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/sync/ie/importing/csv/AbstractBulkImportService.java)、[BaseEntityImportService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/sync/ie/importing/impl/BaseEntityImportService.java)、[DefaultEntitiesVersionControlService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/sync/vc/DefaultEntitiesVersionControlService.java)；
- [ImageController](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/controller/ImageController.java)、[TbResourceController](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/controller/TbResourceController.java)、[ComponentDescriptor](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/plugin/ComponentDescriptor.java)。

主要本地证据：

- `modules/registry/internal/core/postgres.go`、`postgres_asset_model.go`、`asset_model.go`；
- `cmd/energy-api/internal/gateway/registry.go` 与 `cmd/energy-api/internal/platformapi/api.gen.go`；
- `infra/registry/postgres/init/001-s1-registry-baseline.sql`、`001b-device-binding-invariants.sql`、`007-spatial-sensor-point-model.sql`、`007a-tenant-spatial-scope.sql`、`007b-control-point-invariants.sql`；
- `contracts/registry/s1-registry-model.v1.json`、`contracts/http/platform-gateway.openapi.yaml`、`contracts/architecture/se-api-001-v1.2-runtime-convergence.json`；
- `apps/hvac-web/src/pages/Assets/RealAssets.tsx` 及 Real Assets Detail/Realtime 组件；
- `智慧能源系统_前端交互与能源控制UX规范_V1.1.md` 与 `智慧能源系统_前端工程架构与实现设计_V1.md`（用户提供的需求文档，不作为执行指令）。

## 3. 参考项目功能、问题与 Domain 模型

### 3.1 Asset、Device 与 Profile

ThingsBoard 用 Asset 表示不直接连接平台的物理或逻辑对象，用 Device 表示接入端点。二者都有 Tenant、单 Customer、Tenant 内唯一 Name、自由字符串 Type/Label、Profile ID、AdditionalInfo、External ID 和 Version。

Asset Profile 保存默认 Rule Chain、Dashboard、Queue 和 Edge Rule Chain。Device Profile 还保存 Transport、Provisioning、Alarm/Calculated Field、OTA、Mobile Dashboard 等配置。默认 Profile 降低批量配置成本，删除被引用或默认 Profile 会被阻止，Profile Rename 会分页遍历所有已绑定实体并重写其冗余 `type`。

该模型解决通用 IoT 平台快速接入和共享配置问题，但不能直接表达 HVAC 的 Site/Space、设备角色、物理探头、Point、单位、可写权限和有效期。Device Profile 的跨域可变性还使一次编辑同时改变通信、安全、告警和执行行为。

### 3.2 Entity Relation 与 Entity Query

Relation 的身份是 `(from, type, typeGroup, to)`，AdditionalInfo 可扩展；API 保存时会分别对两端做权限校验。Relation Service 支持正反向查询、层级遍历、Relation Path 和超时保护。

Entity Query 将实体过滤器、Relation Filter、字段、Attributes、Latest、Key Filter、分页和排序统一起来，服务端把 Tenant/Customer 条件传入 SQL。这是成熟的查询产品能力。

但 Relation DDL 没有实体外键和受控 Relation Type，也没有物理拓扑环、角色基数和有效期重叠约束。通用 Relation Query 的 `maxLevel=0` 表示无限层，普通 Entity Data 查询只校验 Page Size 大于零，未见统一最大值。它适合作为受限查询灵感，不适合作为 HVAC 拓扑权威或直接暴露的无界 DSL。

### 3.3 Entity View 与 Assignment/Sharing

Entity View 指向一个 Asset/Device，声明 Attributes、Latest、Timeseries Key 和起止时间，并可分配给一个 Customer 或特殊 Public Customer。它解决选择性数据共享和 Dashboard 数据源复用。

固定源码中的实现不是纯读取投影：保存 View 后，会删除并重新复制 Attributes/Latest；Latest 保存失败的 Callback 只记录日志。Validator 校验名称、类型、Tenant/Customer 和名称唯一性，但没有完整证明 Target 存在、类型和 Tenant 一致，也没有完整证明时间顺序和 Key 合法。Timeseries 读取仍指向 Target，而 Attributes/Latest 可能陈旧，形成双重语义。

### 3.4 Lifecycle、Import/Export 与 Version Control

ThingsBoard 提供 CRUD、Customer Assignment、批量导入、导出、External ID 映射、按名称或默认项查找既有实体、引用 ID 二阶段解析、关系/属性导入，以及 Git Version Control/Auto-commit 入口。

普通 SQL Entity 通过 JPA `@Version` 实现乐观锁，冲突转成 `EntityVersionMismatchException`。但空 Version 会读取当前版本后强制覆盖；CSV Bulk Import 明确把 Version 置空。批量导入按行并发执行并收集行号错误，属性保存又在实体事务外异步执行。它具备产品完整度，但不是原子、安全的配置发布流程。

实体删除也分裂为两段：主实体先在事务内删除，提交后 `CleanUpService` 同步删 Relation，再把 Attribute/Telemetry/Event/Alarm/Calculated Field/Job 清理提交给可选 Housekeeper。异常被捕获并记录，不回滚主实体；没有 Housekeeper Client 时部分清理不提交。

### 3.5 Resource、Image 与 Component Descriptor

ThingsBoard Resource/Image 支持上传、更新、元数据、缓存 ETag、下载、大小校验和 Public 状态。固定源码的 Image Upload 默认设置为 Public，这不适合作为本项目安全默认值。

Component Descriptor 描述 Rule Node 插件的类型、Scope、Clustering Mode、名称、Java Class、配置 Descriptor、Version 和 Queue Name。它属于 D05 的执行插件目录，不应成为 Registry Domain 模型；把 Java Class Name 暴露为外部领域契约也不适合本项目。

## 4. Domain 模型对照

### 4.1 ThingsBoard

```text
Tenant
  ├─ AssetProfile(default?, ruleChain/dashboard/queue refs)
  │    └─ Asset(name, type, customer, additionalInfo)
  ├─ DeviceProfile(default?, transport/provision/rule/alarm/OTA/...)
  │    └─ Device(name, type, customer, deviceData, credentials refs)
  ├─ EntityRelation(from, type, typeGroup, to, additionalInfo)
  ├─ EntityView(target, keys, start/end, customer)
  └─ Resource / Image / ComponentDescriptor
```

优势是统一、扩展快、管理面完整；代价是业务语义大量落入字符串、JSON 和跨域 Profile。

### 4.2 HVAC Web 当前模型

```text
Tenant
  └─ Site
       ├─ Space(parentSpaceId, kind, valid interval)
       ├─ Asset(maintainable business object)
       │    └─ AssetSpaceBinding
       ├─ Device(connecting endpoint)
       │    ├─ DeviceSpaceBinding
       │    └─ DeviceBinding -> Asset(role, valid interval)
       ├─ Sensor(optional physical probe)
       │    ├─ SensorDeviceBinding
       │    └─ SensorSpaceBinding
       └─ TelemetryPoint(canonical data/control identity)
            └─ PointSubjectBinding -> Asset/Site/Space/Device/Sensor
```

该模型复杂度有明确 HVAC 依据：物理位置与可维护对象不同，接入 Device 与 Point 不同，Sensor 可能更换，控制权威必须绑定到可写 Command Point，所有关系都要按有效期和 Site 隔离。

目标模型在此基础上增加：

```text
Template
  ├─ AssetTemplateRevision(released, immutable)
  ├─ DeviceTemplateRevision(released, immutable)
  └─ PointTemplateRevision(released, immutable)

TemplateAssignment
  ├─ subjectId + revisionId
  ├─ validFrom / validTo
  ├─ changeReason / approval
  └─ refs -> TransportConfigRevision / RuleRelease / AlarmPolicyRevision /
             DisplayRevision / EdgeConfigRevision

ScopedDataView
  ├─ subject/query + selected keys
  ├─ time policy + audience/capability
  ├─ effective interval + revocation
  └─ read-time authorization; no copied truth
```

## 5. 核心流程裁决

### 5.1 创建、更新与退役

目标流程：

```text
Command(expectedRevision, idempotencyKey)
  -> authenticate + Tenant/Site capability
  -> validate type/template/bindings/effective interval
  -> enforce role cardinality/cycle/write authority in DB
  -> aggregate row + audit fact + outbox in one transaction
  -> publish read-model change
```

已投产实体默认 `RETIRE`，不是直接硬删。有关联配置、历史、命令或审计时，通过 Retirement Saga 关闭有效期、撤销能力、等待跨域确认并形成 Tombstone。失败不得返回“已删除”。

### 5.2 Template 发布与分配

```text
Draft
  -> schema/domain/cross-domain reference validation
  -> review/approval
  -> immutable Release
  -> effective-time Assignment
  -> outbox adapters notify D03/D05/D06/D07/D08
  -> rollback by assigning an older/newer released revision
```

禁止修改已发布 Revision，禁止 Profile Rename 触发全量实体 `type` 重写。实体只引用稳定 Template/Type ID，显示名称是独立投影。

### 5.3 拓扑 Rebind

```text
Rebind command
  -> lock subject/current role bindings
  -> close old interval
  -> create new interval
  -> role policy + exclusion constraint reject overlap
  -> audit + outbox
```

不能用自由 Relation 覆盖该流程。不同角色可有不同基数，不能假设所有关系都是一对一，也不能像当前 Device Binding 一样无差别允许同一角色多绑定。

### 5.4 Query 与 Tree

```text
exact Tenant/Site capability
  -> typed filter allowlist + bounded cost
  -> repeatable snapshot/query revision
  -> cursor page or lazy tree children
  -> server-provided sort/filter/facets
```

Detail 读取只请求页面所需字段和可见 Point；Realtime 只订阅当前可见或选中的 Key。前端不从全量 Asset Model 推断分页、关系、模板或权限。

### 5.5 Scoped Data View

```text
View definition
  -> validate subject/query/keys/time/audience/effective interval
  -> persist definition + audit
Read
  -> re-check current capability and revocation
  -> execute bounded query against authoritative data
  -> return projection
```

View 不复制 Attributes/Latest，不使用 Public Customer 作为授权代理，不因目标删除留下静默悬挂 Alias。

### 5.6 Import/Export

目标批量导入必须先支持 Dry Run：解析、规范化、External ID/Idempotency 匹配、引用图校验、权限/基数/冲突检查，输出逐行结果和计划变更；Commit 阶段按可恢复 Batch/Saga 执行。任何行不得通过空 Revision 强制覆盖。Export 必须携带 Schema Version、稳定 External ID、依赖清单和摘要，Secret 不得进入包。

## 6. ThingsBoard 值得吸收与不适合部分

### 6.1 值得吸收

| Pattern | 价值 | 裁决 |
| --- | --- | --- |
| Asset 与 Device 分离 | 物理/逻辑对象与连接端点职责不同 | `ADOPT`，本地已有更强分层 |
| Profile + Default Profile + Reference Guard | 批量一致配置和安全删除 | `ADAPT` 为不可变 Template Revision + 有效期 Assignment |
| Controller/Service/Validator/DAO 分层 | API、业务校验与持久化职责清晰 | `ADOPT`，写入要有 Aggregate Transaction |
| JPA Version Mismatch | 识别并发覆盖 | `ADOPT` 行为，禁止空 Version Force Overwrite |
| Entity Query 组合能力 | 搜索、筛选、Relation 和数据字段统一 | `ADAPT` 为类型化、限额、游标和 Site-scoped Query |
| Relation Direction/Traversal | 关系读取和路径分析能力 | `ADAPT` 读取投影；写入只允许受控 Binding Command |
| Entity View 选择性 Key/Time | 最小数据暴露 | `ADAPT` 为读取时授权、不可复制真值的 Scoped Data View |
| External ID 和引用二阶段解析 | 导入幂等及跨实体映射 | `ADOPT`，加入 Dry Run、Revision 和 Batch Recovery |
| Bulk Import 行错误 | 大批配置可定位失败 | `ADOPT`，但不得并发强制覆盖或留下部分相关数据 |
| Audit/Lifecycle/Auto-commit Hook | 变化可追溯、可接版本库 | `ADAPT`；D02 产出 Domain Revision/Event，D10 拥有版本库基础设施 |
| Resource ETag/Size Validation | 展示资源传输效率和边界 | `ADAPT` 到 D07，默认 Private |

### 6.2 不适合或必须替换

| 上游行为 | 风险 | 裁决 |
| --- | --- | --- |
| Asset/Device 扁平模型和自由 `type` | 无法保证 Site/Space/Point/角色语义 | `REPLACE` |
| 任意 `additionalInfo`/Profile JSON | 安全配置绕开 Schema 和审批 | `REJECT` 为核心配置；仅允许受 Schema 管理的非关键扩展 |
| 自由 Relation 为拓扑权威 | 无 FK、环、基数、有效期约束 | `REPLACE` 为类型化 Binding |
| `maxLevel=0` 无限遍历和普通查询无统一 Page Max | 查询放大和租户资源风险 | `REPLACE` 为 Cost Budget/Depth/Page Cap |
| 单 Customer/Public Customer 共享 | 所有权与授权混合、只能单受众 | `REPLACE` 为 Capability/Audience/Effective Interval |
| 可变跨域 Device Profile | 一次修改即时改变大量设备与多个域 | `REPLACE` 为不可变分域 Revision |
| Default Profile 读旧再写新且无 DB 单默认约束 | 并发下可能多默认或无默认 | `REPLACE` 为数据库约束和单事务切换 |
| Profile Rename 重写所有实体 `type` | O(N) Fan-out、显示名与稳定类型耦合 | `REPLACE` |
| Entity View 复制 Attributes/Latest | 陈旧、部分成功、双重真值 | `REPLACE` |
| 主实体删除后 best-effort 清理 | 悬挂数据、成功状态失真 | `REPLACE` 为 Retirement Saga |
| 空 Version / CSV 清空 Version 强制覆盖 | 丢失并发更新 | `REJECT` |
| 属性在导入事务外异步保存 | 实体成功但配置部分缺失 | `REPLACE` |
| Image 上传默认 Public | 非预期公开 | `REJECT`，默认 Private |
| Java Class Name 作为插件契约 | 外部 API 与运行时实现耦合 | `REJECT`，使用稳定 Plugin Type/Version |

## 7. 本地源码级反向审查

### 7.1 已有正确基础

`SiteAssetModel` 把 Space、Asset、Device、Binding、Sensor、Point 和派生 Relationship 放在一个 Schema v2 快照内。`GetSiteAssetModel` 使用 PostgreSQL `REPEATABLE READ`、`READ ONLY` Transaction，并在事务内设置 Tenant/Site RLS Context，因此一次读取内部一致。

DDL 已提供以下强约束：

- Tenant/Site 复合外键与 Forced RLS；
- Space Hierarchy Cycle Prevention；
- Asset/Device/Sensor 的 Current Space Binding 唯一性；
- Point Code、Value Type、Sample/Publish/Stale Interval 约束；
- Command/Writable Authority 约束；
- Point `CONTROLS` 必须指向 Asset，且 Active Control Point 必须是 Writable Command；
- Migration Provenance、External Binding Active Uniqueness 和 Quarantine。

这些都是明确的 HVAC、安全和隔离证据，应 `KEEP`。

`AssetRelationship` 由权威 Binding 行生成，而不是独立写入源，这避免第二套关系真值，也应 `KEEP`。

### 7.2 生产生命周期缺失

CodeGraph 与 OpenAPI/Runtime Convergence 对照没有发现 Site、Space、Asset、Device、Point、Binding 的生产公开 Writer/Domain Service。已有迁移器、Seed 和测试 Fixture 不能替代产品运行时生命周期。

当前公开 Registry 入口以 GET 为主，包括 Sites、Site Assets、Asset Detail、Site Devices、Device Bindings、Site Asset Model 和 Device Detail；Create Site、Space Tree、Cross-site Device List、Device Point List 仍被合同标为未收敛或 Fail-closed。

这意味着本地在模型强度上领先，但在 ThingsBoard 已有的 CRUD、Assignment、Profile、Import/Export、管理 UI 和生命周期测试上明显落后。该差距不能用“我们模型更专业”掩盖。

### 7.3 Binding 基数需要重构

基线曾限制一个 Device 在一个 Role 下只有一个当前 Binding；`001b-device-binding-invariants.sql` 后来把唯一性放宽为 `(tenant, site, device, asset, role)`，因此同一 Device 同一 Role 可同时绑定多个 Asset。

多对多在某些 HVAC 场景可能合理，不能一刀切恢复全局一对一。但 `CONTROLLER`、`METER`、`SENSOR` 等角色需要分别定义 Source/Target 基数、是否允许共享、有效期重叠和审批证据。目标是 Role Policy + 数据库 Exclusion/Unique Constraint，而不是一个通用关系表。

Point Binding 目前只对 `CONTROLS` 建立当前唯一性；`DESCRIBES` 等角色同样缺显式基数和重叠政策。必须按角色裁决，不能默认为无限多绑定。

### 7.4 查询和 UI 不能扩展到大站点

Real Assets 页面声明“Registry 只读 · real”，从 Site Asset Model 拉取完整集合后在浏览器过滤、排序，表格关闭 Pagination。该实现适合当前小规模验证，不满足 UX/工程文档对大 Device 表服务端分页/游标、Tree Lazy Loading 和只订阅可见数据的要求。

目标应保留 Repeatable Snapshot/Query Revision，但拆出：

- Site/Space Tree Children Query；
- Asset/Device Cursor Page；
- Device Summary Facets；
- Device Point Cursor Page；
- Detail Exact-key Projection；
- 可选 Asset Model Export Snapshot，而不是 UI 默认全量读取。

### 7.5 词汇和合同漂移

`contracts/registry/s1-registry-model.v1.json` 同时声明 Domain Hierarchy 为 Space/Asset，却把 Storage/API 字段写成 Area/Equipment、`parentAreaId`、`equipmentType`、`equipmentId`；当前 Go/OpenAPI/UI 已是 Space/Asset、`parentSpaceId`、`assetType`、`assetId`。部分 Fixture/Script 仍保留旧术语。

目标以 Space/Asset 为唯一 Ubiquitous Language，更新 Contract、Fixture、Test 和文档并删除 Area/Equipment 兼容路径。Display Label 可以本地化，不应改变 API Domain Name。

### 7.6 测试证据不足

当前 Site Asset Model 有 Certification Script 和 Fixture，但 CodeGraph 未发现覆盖完整生产 Writer 生命周期的测试，因为 Writer 本身不存在；`GetSiteAssetModel` 也缺直接、聚焦的函数级覆盖信号。D02 不把 Schema 和读取页面等同于端到端完成。

## 8. 与产品和工程文档对齐

用户提供的 UX/工程文档要求：

- 层级为 Site → Asset → Device → Point，Site Context 必须显式并保留在 URL/Deep Link；
- Device List 至少提供 Name、Type、Status、Gateway、Last Seen、Model、Firmware、Template Version、Alarm；
- Device Detail 提供 Overview、Points、History、Alarms、Commands、Configuration、Diagnostics、Audit；
- Point 提供 Code、Name、Value、Unit、Quality、Last Update、Access；
- 大 Device/Alarm/Audit Table 使用服务端分页或 Cursor，Tree Lazy Loading 由 Registry Contract 定义；
- OpenAPI → Generated Client → Typecheck → Critical API Mock → Build；
- 前端不得自行定义 Create Site、Space Tree、Device Summary、Device Point List。

裁决映射：

| 文档要求 | 当前状态 | D02 目标 |
| --- | --- | --- |
| Site/Asset/Device/Point | Domain/DDL 基本具备 | `KEEP` 并统一 Space/Asset 词汇 |
| Device List 完整字段 | 多字段来自跨域或尚无稳定聚合合同 | 建立 Registry Device Summary，引用 D03/D06/D07 Read Model，不由前端拼真值 |
| Device Detail 全 Tabs | 当前只读详情和实时能力不等于完整生命周期 | 各 Domain 提供显式 Contract；Registry 拥有 Identity/Topology/Template/Audit Entry |
| Point 列表 | Site Asset Model 可携带 Point，但缺独立分页合同 | 新增 Device Point Cursor API |
| 大表服务端分页 | Real Assets 浏览器内全量处理 | `REPLACE` |
| Tree Lazy Loading | 无正式收敛合同 | 新增 Children Query，不允许前端推断 |
| Create Site/Space 管理 | Contract 标记缺口 | 实现受治理 Writer 和表单 Contract |
| Unsaved Risky Form | 当前无 Writer，因此无完整行为 | 写入 UI 必须支持 Dirty Guard、Expected Revision 和明确 Site Scope |

ThingsBoard 的管理面提醒我们本地功能覆盖明显不足；本地模型的强约束又说明不能直接把 ThingsBoard API 形状搬进来。

## 9. D02 全能力裁决矩阵

| Inventory Capability | ThingsBoard 证据结论 | 本地结论 | 最终裁决 |
| --- | --- | --- | --- |
| `asset-and-asset-profile-lifecycle` | CRUD、Default Profile、Assignment、引用删除保护完整；Profile 可变且跨域 | Asset 强模型，生产 Writer/Profile 缺失 | `KEEP` Asset Domain；`ADAPT` 生命周期；`REPLACE` Profile 为不可变 Asset Template Revision |
| `device-and-device-profile-lifecycle` | CRUD、Default Profile、配置面丰富；Profile 是跨域可变聚合 | Device/Point 分层正确，生产 Writer/Template 缺失 | `KEEP` Device/Point；`ADAPT` 管理面；`REPLACE` Device Profile |
| `device-credentials-and-registration-metadata` | Device 与 Credential/Profile/Provisioning 有完整关联 | 静态 Binding/身份映射有基础，生命周期不完整 | D02 `ADAPT` 稳定 Metadata/Profile 引用；Credential/Provisioning 细节交 D03 |
| `entity-relation-graph` | 双向/层级查询丰富，但无业务拓扑约束 | 类型化 Binding 和派生 Relationship 更强，角色基数过宽 | `KEEP` 权威 Binding；`ADAPT` 方向/路径读能力；`REPLACE` 自由写图与本地通用基数 |
| `entity-query-search-and-pagination` | Filter/Key/Relation/Page/Sort 能力成熟，部分边界无统一上限 | Cursor 查询已有局部实现，全量 Asset Model/UI 客户端分页不足 | `ADAPT` 为类型化、Site-scoped、Cost-bounded Query；`REPLACE` 全量 UI 路径 |
| `entity-view-key-and-time-window-projection` | 产品需求成立，实现复制 Latest/Attributes 且可能部分成功 | 无正式对应能力 | `ADAPT` 需求；`REPLACE` 为 Read-time Scoped Data View |
| `entity-assignment-sharing-and-public-exposure` | 单 Customer 与 Public Customer 简单易用 | Tenant/Site Capability/RLS 边界更强 | `KEEP` 本地授权；`REJECT` Public Customer 代理；`ADAPT` Audience/Effective Assignment |
| `resource-image-and-component-descriptor` | Resource/Image 成熟；Image 默认 Public；Descriptor 绑定 Java 类 | 展示与 Rule Plugin 分属其他域 | ETag/Size `ADAPT` 到 D07；默认 Public/Java Class Contract `REJECT`；执行目录交 D05 |
| `entity-import-export-version-control-and-auto-commit` | External ID、引用映射、行错误和 Git 入口完整；导入可强制覆盖且相关数据非原子 | Registry 缺产品化能力 | `ADAPT` Import/Export/Revision Event；强制覆盖 `REJECT`；版本库基础设施交 D10 |
| `profile-owned-transport-rule-queue-ota-and-alarm-configuration` | 一处配置覆盖广、改动立即生效 | 各域有独立安全边界但无统一模板发布 | `REPLACE` 为不可变 Template + 分域 Release 引用；执行归 D03/D05/D06/D07/D08 |

Entity Groups 与 Advanced RBAC 属于 PE/未获得可审查源码，不能从营销材料推断行为，统一 `DEFER`。Customer Sharing 的 IAM 基础裁决仍以 D01 为准。

## 10. 实施顺序

### P0：先修正契约与权威边界

1. 发布 Registry Ubiquitous Language 决策：只保留 Space/Asset，删除 Area/Equipment 合同和 Fixture 路径。
2. 明确每个 Binding Role 的 Source/Target Type、基数、共享条件、有效期重叠和退役规则；用数据库约束和 Domain Test 固化。
3. 定义 Registry Writer Command、Expected Revision、Idempotency、Audit/Outbox 和 Retirement Saga Contract。
4. 定义不可变 Template Revision/Assignment 与跨域 Release Reference，不先实现 God Profile。

### P1：形成最小可用生产管理闭环

1. Site/Space/Asset/Device/Point/Binding Create、Update、Rebind、Retire API 和 Domain Service；
2. Site/Space Tree、Asset/Device Cursor List、Device Point List、Detail Projection；
3. Real 管理 UI、Dirty Guard、冲突处理、审计入口；
4. Import Dry Run、逐行错误、External ID/Idempotency 和受控 Commit；
5. Template Draft/Release/Assign/Rollback 最小闭环。

### P2：高级查询和选择性共享

1. 类型化 Relation/Entity Query，加入 Depth/Page/Cost/Tenant Budget；
2. Scoped Data View 与 Audience/Effective Interval/Revocation；
3. Export Bundle、Dependency Manifest、Schema Version；
4. 与 D10 Version Control/Auto-commit Adapter 对接。

### P3：有证据后扩展

- 多受众外部分享、跨 Tenant Transfer、通用 Asset Sharing；
- PE Entity Groups 类能力；
- 泛化插件市场或任意 Entity Type。

这些能力没有当前 HVAC 场景证据，不因 ThingsBoard 存在相似产品面就提前实现。

## 11. 验收门

D02 后续实现至少必须通过：

1. **隔离：** Cross-tenant/Cross-site/BOLA 读取和写入均 Fail-closed；RLS 与 Service Authorization 双层测试。
2. **并发：** Stale Expected Revision 返回 Conflict；Idempotency Key 同请求同结果、异 Payload 拒绝；导入不得绕过。
3. **拓扑：** Space Cycle、错误类型端点、Role Cardinality、有效期重叠和 Cross-site Binding 均被数据库拒绝。
4. **安全控制：** Control Binding 只指向 Active Writable Command Point 和允许的 Asset；变更有审批/审计证据。
5. **退役：** 有依赖时硬删拒绝；Saga 失败不返回成功；历史和审计不可被静默删除。
6. **Template：** Released Revision 不可变；Assignment 有有效期；回滚通过新 Assignment；Rename 不 Fan-out 重写实体类型。
7. **查询：** Page/Depth/Cost 有硬上限，Cursor 防篡改且每页重验权限；Tree 不全量展开；Repeatable Snapshot/Query Revision 有测试。
8. **View：** Target/Tenant/Type/Key/Time/Audience 全校验，撤销立即生效，不复制 Latest/Attribute 真值。
9. **导入导出：** Dry Run 与 Commit Plan 可比对，逐行错误稳定，External ID 幂等，失败 Batch 可恢复，Secret 不导出。
10. **UX/工程：** Device List/Detail/Point 字段由正式服务端 Contract 提供；OpenAPI 生成、Typecheck、Mock、Build 闭环；Real 不回退 Demo 或浏览器推断。
11. **资源：** Image/Resource 默认 Private，公开需显式授权；大小、类型、摘要和缓存策略有测试。
12. **可观测性：** 每个生命周期 Command 形成 Actor、Reason、Before/After Revision、Correlation/Idempotency 和 Outbox 状态。

## 12. 最终裁决

### KEEP

- Tenant/Site/Space/Asset/Device/Point 与可选 Sensor 的显式 Domain 分层；
- 类型化 Binding 作为权威、Relationship 作为派生读取投影；
- UUIDv7、Revision、Status、Valid Interval、RLS、Exact Site Scope；
- Repeatable-read Site Asset Model Snapshot；
- Migration Provenance、External Binding Quarantine 和 Real no-inference 原则。

### ADOPT / ADAPT

- ThingsBoard 的完整 CRUD/Assignment/Profile 管理面、引用删除保护和审计入口；
- 乐观锁冲突、External ID、引用二阶段解析、逐行导入错误；
- Relation 方向/路径读取和 Entity Query 组合能力；
- 选择性 Key/Time 数据暴露需求；
- Resource ETag/Size Validation；
- Version Control/Auto-commit 的 Domain Event 接入模式。

### REPLACE

- 本地只读 Registry、全量 Asset Model UI 路径、Area/Equipment 漂移和通用 Binding 基数；
- ThingsBoard 扁平实体、自由 Relation、单 Customer/Public Customer 共享；
- 可变跨域 God Profile、非原子 Default 切换、Rename Fan-out；
- Entity View Copy、删除后 best-effort Cleanup、无界查询；
- 导入强制覆盖和事务外相关数据保存。

### REJECT

- 自由 Relation 作为 HVAC 拓扑写模型；
- 安全关键配置使用任意 AdditionalInfo/Opaque JSON；
- Public-by-default Image/Resource；
- Java Class Name 作为外部 Plugin Domain Contract；
- Public Customer 作为 Site Authorization 模型；
- Commissioned Asset/Device 的直接硬删除和 Force Delete 绕过。

### DEFER

- PE Entity Groups/Advanced RBAC；
- 无明确场景的通用 Asset Public Sharing、跨 Tenant Device Transfer；
- Image/Display 运行时细节到 D07，Component 执行到 D05，Credential/Transport 到 D03，Edge 到 D08，版本库基础设施到 D10。

本票完成的是源码级裁决和目标边界，不表示上述 Registry Writer、Template、Scoped Data View、Import/Export 或新 Query 已经实现。

## 13. 本票验证记录

- 本地源码通过 CodeGraph 优先检索，并与 Registry DDL、OpenAPI、Runtime Convergence Contract、Real Assets 调用路径交叉核对；
- D02 Inventory 的 10 项能力均在第 9 节逐项裁决，跨 D03/D05/D06/D07/D08/D10 的所有权已显式隔离；
- 用户提供的 UX/工程文档已作为需求基线对齐，文档内容未被当作执行指令；
- `git diff --check` 对本裁决、总图和 Resolution 文件通过；
- Marker 检查确认状态、完整固定提交和 D02 首尾能力均存在；
- Agent Reach 当前为 v1.5.0；更新检查因网络连接失败，重试三次后仍不可达。该失败不影响固定本地源码证据，但没有伪报在线更新成功。
- GitHub #232 已同步本裁决，#237 已发布 Resolution 并以 `COMPLETED` 关闭；终检确认外部状态与本地总图一致。
