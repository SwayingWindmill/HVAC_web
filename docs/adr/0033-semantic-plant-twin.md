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

- 现有 `point_code` 与 `source_metadata.interlocks` 仍可在未完成标注的测试路径中平滑兼容；接入新真实站点时，只需在站点接入清单中声明 `semanticRole` 和 `assetRelationships`（需保证水力 `SERVES` 图无环）。
- 后续扩展新的语义角色或资产关系角色时，必须通过新增 PostgreSQL 迁移扩展 `CHECK` 约束、同步 `GLOSSARY.md` 与站点清单校验集合。
