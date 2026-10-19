# 分表计量：参照项目的源码对比（2026-10-12）

Issue #481 第二阶段，规格： `docs/specs/metered-breakdown.md`。问题只有一个：设备加了分表后，站点总量怎样不把分表重复计入、又能按设备拆分。

在下面的固定提交上读原始源码：

| 参考 | Release | Commit |
| --- | --- | --- |
| ThingsBoard CE | v4.3.1.1 | c2a52e46c44e308ddee430e7266b8e10eddde9c4 |
| OpenEMS | 2026.7.0 | 2e2792d59fc5ba3b99ce3cf98d15081c0a74895e |
| MyEMS | v6.7.0 | be6e6ce8ddeac57afb04bddb9621501fb555cab0 |

## 读到的做法

- **OpenEMS** `io.openems.common/src/io/openems/common/types/MeterType.java`：电表按类型区分。`CONSUMPTION_METERED` 的注释写明它「不加入」计算出的用电，因为电网表已经量过；`CONSUMPTION_NOT_METERED` 则加入。站点总量由 `io.openems.edge.core/src/io/openems/edge/core/sum/SumImpl.java` 从电网表、发电表和储能算出。
- **MyEMS** `database/install/myems_system_db.sql` 的 `tbl_meters` 有 `is_counted` 和 `master_meter_id`；`myems-aggregation/space_energy_input_category.py`、`equipment_energy_input_category.py` 汇总空间、设备用能时只取 `m.is_counted = 1` 的表。即「是否计入总量」是每块表上的一个手工标志，层级另由主表引用表达。
- **ThingsBoard** `common/data/src/main/java/org/thingsboard/server/common/data/cf/CalculatedFieldType.java`：汇总靠计算字段（`RELATED_ENTITIES_AGGREGATION`、`ENTITY_AGGREGATION`），按实体关系把子实体遥测加起来；没有「此表已含在上级表中」的概念，避免重复计数要使用者自己建模。

## 决定

- **改造后采用（OpenEMS、MyEMS）**：区分「计入站点总量的表」与「已含在总量里的分表」，总量只加前者，分表单独看。这正是 `MeterType.CONSUMPTION_METERED` 与 `is_counted` 解决的问题。
- **改造点**：不在电表上另设标志，而是从能源拓扑推出计入范围——绑定所在的边从非电网节点进入关联了设备的负载节点即为分项（`BRANCH`），其余为站点总量（`SITE`）。计量绑定本就挂在拓扑的边上，且已发布的拓扑版本不可变，再加一个可与拓扑不一致的标志只会多一处漂移。MyEMS 的 `master_meter_id` 也由「冷站 → 设备」的边表达。
- **不采用（ThingsBoard 的关系聚合）**：按关系把子实体相加会把分表与总表重复计数，且需要每个站点手工配置；本平台的总量与分项都由能耗查询固定的算法给出。
- 「未分项 = 总表 − 分表之和」与 MyEMS、OpenEMS 都不同：两者不展示差额。这里按「只显示真实数据」的约定如实展示差额，可为负。
