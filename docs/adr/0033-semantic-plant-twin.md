# 0033 语义设备群孪生：显式水力关系、点位语义角色与只读孪生视图

状态：已采纳（2026-11-16）。补充并取代 ADR 0028「几个站点的点位代码必须一致」的约束。

## 背景

此前冷站智能服务（`optimization-service`、`fdd-service`）与控制服务在读取站点拓扑和点位时，存在三处脆弱依赖：

1. **跨站点点位代码硬编码**：ADR 0028 要求同一租户下的站点必须使用相同的 `point_code` 才能共享优化策略与诊断规则，一旦现场设备沿用真实电表或模块点位代码就会对不上。
2. **水力关联隐式反推**：辅机与冷机之间的 `SERVES` 关系只藏在控制点 `source_metadata.interlocks` 中，缺乏显式的资产间数据库模型，没有配置联锁或总管资产的站点无法表达清晰的水路层次。
3. **跨模块直接读基表与拼装逻辑重复**：不同服务各自写 SQL 查询 `core_registry` 基表、各算一遍 `nameplate.unconfirmed` 过滤和 `site_control_states` 默认值，容易随基表演进而漂移。

## 决定

1. **新增显式资产关系表 `core_registry.asset_relationships`**
   - 记录同一租户、同一站点下 `from_asset_id -> to_asset_id` 的有向水力服务边（`relationship_role IN ('SERVES')`），禁止自环并强制唯一活动边索引，复用 `ACTIVE`/`RETIRED` 与 `valid_from`/`valid_to` 生命周期，不授予写角色 `DELETE` 权限。
   - `modules/registry/internal/core` 的 `GetSiteAssetModel` 将该表的 `ASSET -> ASSET` 边一并输出到 `SiteAssetModel.Relationships`。
2. **在 `core_registry.point_subject_bindings` 增加 `semantic_role`**
   - 通过受限 `CHECK` 约束固定 11 个物理语义角色枚举：`ACTIVE_POWER`、`RUN_STATE`、`CHW_LEAVING_TEMP`、`CHW_ENTERING_TEMP`、`CW_LEAVING_TEMP`、`CW_ENTERING_TEMP`、`SUPPLY_WATER_TEMP`、`RETURN_WATER_TEMP`、`COOLING_LOAD`、`WATER_FLOW`、`ZONE_TEMP`。
   - 接入清单与孪生消费者通过 `(测量对象, semantic_role)` 解析物理信号，取消不同站点 `point_code` 必须一致的前置限制（取代 ADR 0028 的该项后果）。
3. **按 ADR 0016 暴露 10 张 `core_registry.plant_twin_*_v1` 安全屏障只读视图**
   - 统一由 `core_registry_read_port` 持有，涵盖站点控制模式（无记录时默认 `REMOTE_AUTO`）、单元资产与铭牌（过滤 `nameplate.unconfirmed`）、资产锁、设备及 `source_key`、设备绑定、点位语义角色、水力有向边、周运行计划、时段与已批准例外；每张视图显式绑定 `current_tenant_id()` 与 `is_authorized_site(...)`。
4. **站点接入清单与模拟冷站同步声明显式关系与语义角色**
   - `scripts/site-onboarding.mjs` 与 `deploy/sites/songyaxuan.site.json` 校验并幂等同步 `assetRelationships`（16 条有向无环 `SERVES` 边）和 `semanticRole`；`scripts/phase1-central-plant-simulator.mjs` 同步为本地模拟冷站写入对应关系与语义角色。

## 不选的做法

- **仅在 `source_metadata` JSON 中补充水力边和角色标签**：缺少数据库外键、枚举约束和部分唯一索引，无法靠 schema 拦住错配或跨站引用。
- **为每个智能服务单独复制另一份策略配置**：不解决设备点位代码语义识别问题，反而把一份租户策略拆散。

## 后果

- 现有 `point_code` 与 `source_metadata.interlocks` 仍可在未完成标注的测试路径中平滑兼容；接入新真实站点时，只需在站点接入清单中声明 `semanticRole` 和 `assetRelationships`（需保证水力 `SERVES` 与 `FEEDS_HEADER` 图无环）。
- 后续扩展新的语义角色或资产关系角色时，必须通过新增 PostgreSQL 迁移扩展 `CHECK` 约束、同步 `GLOSSARY.md` 与站点清单校验集合。

## 增补：暖通水力环路与并联支路拓扑（2026-11-18）

通过不可变增量迁移 `infra/registry/postgres/init/hydraulic-loop-topology.sql` 扩展冷站水力环路与并联支路模型：

1. **环路与支路归属列**：在 `core_registry.assets` 新增可空字段 `water_loop`（`CHILLED_WATER`、`CONDENSER_WATER` 或 `COOLING_TOWER_LOOP`）与 `branch_code`（并联支路编码），并在 `core_registry.plant_twin_units_v1` 末尾追加这两列；未配置的旧站点返回 `NULL`，HTTP `GET /api/v1/sites/{siteId}/asset-model` 省略或返回 `null`，保持向后兼容。
2. **扩展拓扑关系与语义角色**：`core_registry.asset_relationships.relationship_role` 扩展支持 `SERVES`、`ISOLATES`（隔离阀切断并联支路或设备）和 `FEEDS_HEADER`（支路或设备汇入水系统总管）；`core_registry.point_subject_bindings.semantic_role` 扩展至 17 个枚举，新增 `VALVE_FEEDBACK`、`VALVE_COMMAND`、`VFD_FREQUENCY`、`VFD_FREQUENCY_COMMAND`、`HEADER_DIFF_PRESSURE` 和 `BRANCH_FLOW`。
3. **跨模块水力关系视图**：新增安全屏障视图 `core_registry.plant_twin_asset_relationships_v1`，在一条查询中同时暴露起点与终点资产编码、类型及各自的 `water_loop` 与 `branch_code`。
4. **运行视图与孪生查询**：`libs/planttwin` 提供 `UnitsByLoop`、`ParallelBranches` 以及 `HydraulicGraph.Isolates` / `IsolatedBy` / `FeedsHeader` / `HeaderFeeders`；Web「实时运行」与微信小程序「运行」按冷冻水/冷却水环路及 `branch_code` 聚合展示并联支路，未配置支路编码的站点退回设备分类分组。
