# Wayfinder #311：Energy Fact 的 rebuild generation、supersede/tombstone 与并发恢复语义

状态：DECIDED / SOURCE-BACKED  
审查日期：2026-08-25  
范围：裁决能源区间事实的历史修正语义——触发分类、generation 身份、旧版本表示、可见性、并发约束和失败恢复；给出可直接进入实施规格的边界。不实现迁移或代码，不改变 W0 首切片的 fail-fast 行为，不批准多写入者部署。
决策授权：本轮四个分支由驱动开发者显式委托裁决（grilling 会话记录），每项决定均附源码依据。

## 1. 决策结论

1. **两级触发，一套机制**：晚到数据的微修正是自动的；定义变更的窗口级 rebuild 是显式的 run。两者都表现为"带新 revision 的新事实行"，没有第三种修正路径。
2. **事实行不可变，多版本并存，读侧 latest-revision-wins**：完全对齐 `analytics.metric_result_facts` 的既有先例（ORDER BY 尾部带 revision、writer 持 `ALTER DELETE` 做保留期清理），不引入 ReplacingMergeTree、可见性布尔列或原地 UPDATE。
3. **Run ledger 是 ClickHouse 追加事件表**，归 Energy Processing 所有；不在 `core_registry` 复制 Metric 的跨域直写冲突（#304 §4.4 已标记该模式为待收敛）。
4. **单 active writer 维持为唯一的并发正确性来源**；互斥不依赖数据库约束，多写入者协调仍留在雾区的 HA 触发条件之后。
5. **失败恢复按月分区 chunk 推进**：cursor 记录完成块；重放前清除本 run 残留；`PERSISTED` 仅在全部 chunk 完成后成立。

## 2. 证据基线

### 2.1 HVAC 生产源码（本轮逐文件核对）

| 证据 | 观察 | 对语义的约束 |
| --- | --- | --- |
| [`004-counter-semantics.sql`](../../infra/s2-telemetry/clickhouse/init/004-counter-semantics.sql) | `counter_deltas` 是 `CREATE VIEW`，实时重算配对。 | 晚到观察 B 插入 A、C 之间后，配对自动变为 (A,B)+(B,C)；已写 Fact(C)（prev=A）的 delta 从此错误。**晚到数据本身就是修正触发器**，不是只有配置变更才需要修正。 |
| [`002-analytics-energy-interval.sql`](../../infra/s2-telemetry/clickhouse/init/002-analytics-energy-interval.sql) | 普通 `MergeTree` + `non_replicated_deduplication_window=100000`。 | 同主键重复插入被**丢弃而非替换**："改一条事实"不能靠重插同键行；修正行的排序键必须含 revision。 |
| [`005-metric-result-revisions.sql`](../../infra/s2-telemetry/clickhouse/init/005-metric-result-revisions.sql) | `metric_result_facts`：MergeTree，ORDER BY 尾部 `(revision, result_id)`；writer 有 `GRANT ALTER DELETE`；reader 含 settlement/cube。 | 仓库内已有的同型事实修正机制：新版本=新行，读侧取最新 revision，旧版本保留供 pinning、到期物理清理。Energy 对齐此先例。 |
| [`009c-metric-model-v2.sql`](../../infra/s1-registry/postgres/init/009c-metric-model-v2.sql) | `metric_calculation_runs` 状态机 `PENDING/RUNNING/PERSISTING/PERSISTED/FAILED` + 单活跃 run 部分唯一索引；runs 由 metric-engine 直写 `core_registry`。 | 状态机语义可复制；但"服务直写 core_registry"已被 #304 §4.4 标记为 ownership 待收敛项，Energy 不得再复制该冲突。 |
| [`projector.go`](../../services/analytics-read-model-projector/internal/energy/projector.go)、[`client.go`](../../services/analytics-read-model-projector/internal/clickhouse/client.go) | anti-join 仅按 current observation 是否已存在判定跳过。 | 微修正要求把候选判定升级为"无 Fact **或** 最新版 Fact 的 `source_previous_observation_id` ≠ canonical view 的 prev"。 |

### 2.2 上游证据

- MyEMS v6.7.0 `be6e6ce8…`：normalization 的 data repair 以独立处理记录修正历史能耗，不覆写原始 telemetry（见 [#303 研究记录](../research/wayfinder-energy-reference-source-review-2026-08.md) §4）。ADOPT 为"修正=留痕的新处理输出"。
- ThingsBoard / OpenEMS：本分支无新增证据；其 calculated field 重算与 Edge resend 机制不进入 Backend 能源事实语义。

## 3. 触发分类

| 触发 | 类型 | Scope | 发起方 |
| --- | --- | --- | --- |
| 晚到观察改变既有配对 | 自动微修正 | 受影响 transition 的 current observation 集合 | Projector 候选判定自动发现 |
| MeterBinding 发布/撤销/有效期变更 | 显式窗口 run | binding × 新旧有效区间的并集 | Binding release 事件的后续动作 |
| Point revision/unit/Counter 语义变化 | 显式窗口 run | 覆盖该 Point 的 binding × boundary 之后 | Registry 内容发布流程 |
| 质量映射或算法版本变更 | 显式窗口 run | 策略声明的 binding 集合 × 窗口 | 配置变更流程 |
| 人工修正请求 | 显式窗口 run | 显式声明的 scope | Administration 工作空间（审批制） |

微修正与窗口 run 共用同一 run ledger、同一"新 revision 行"表示和同一恢复协议，仅 scope 不同。**禁止**绕过 run 直接手改事实行。

## 4. Generation 身份与数据表示

### 4.1 Run ledger：`analytics.energy_rebuild_runs`（ClickHouse 追加事件表）

```text
event_id UUID          -- 事件行身份
run_id UUID            -- 一次 rebuild 的身份（UUIDv7）
tenant_id / site_id UUID
scope_type             -- TRANSITION_CORRECTIONS | BINDING_WINDOW
meter_binding_id       -- 窗口 run 必填；微修正可空
window_start/window_end DateTime64
reason_code            -- LATE_ARRIVAL | BINDING_CHANGE | SEMANTICS_CHANGE | POLICY_CHANGE | MANUAL
trigger_ref            -- 触发来源（binding version、policy id、请求 id）
event_type             -- RUN_STARTED | RUN_PROGRESS | RUN_PERSISTED_CHUNK | RUN_COMPLETED | RUN_FAILED
chunk_cursor           -- 月分区游标（如 2026-08）
detail                 -- JSON：计数、失败原因等
recorded_at
```

- Owner 是 Energy Processing（`analytics-read-model-projector`）；`cube_analytics_reader` 增加 SELECT 供 Query/UI 展示重建状态。
- 最新状态由事件序列派生（每个 run 取最后一条事件）；不做 UPDATE。
- 互斥说明：追加模型无法表达唯一约束。v1 的互斥来源是**单 active writer 部署门禁**（进程内串行：run 与增量批处理交错执行，任一时刻至多一个 run 处于活动状态）；未来多写入者必须先引入 PostgreSQL 侧协调，属雾区 HA 议题。

### 4.2 事实行扩展

沿用 #310 Step 1 的同一迁移，增加：

- `fact_revision UInt64` —— 逻辑键内的单调版本；
- `rebuild_run_id UUID` —— 产出本行的 run；W0 增量基线行该列为空（generation 0 语义），**不得**解释为"未修正=完美"；
- ORDER BY 扩展为 `(..., period_end, fact_revision, source_current_observation_id)`，与 #310 要求的 meter/meter_binding 前置组合；修正行因此永不触碰 dedup window。

逻辑键维持 `(tenant_id, site_id, meter_binding_id, source_current_observation_id)`；一个逻辑键的多行 = 该事实的版本历史。

### 4.3 可见性与 tombstone

- **没有 tombstone 列、没有删除标记、没有原地修改**。"supersede" = 同逻辑键出现更高 revision 的行。
- 所有消费 Energy Fact 的固定读路径（Cube members、Query Service、报表 read model）必须在聚合前按逻辑键取最高 revision——这一要求落在固定查询适配层实现，UI/浏览器永远不见多版本。
- 报表 pinning（W6）：绑定"截至 revision N / 截至 run X"即可确定性地重放当时结果集，因为旧版本行在被保留期清理前始终在场。
- 清理即 tombstone 的物理形态：writer 按保留策略对"非最新且超过保留窗"的旧版本执行 `ALTER DELETE`（对齐 metric writer 授权）；清理只影响审计留存，不影响任何默认查询。

### 4.4 datasetRevision 契约升级

`<QUERY_DATASET_REVISION>:<max fact revision>` 中的 revision 语义从 source offset 升级为 max **visible** `fact_revision`；存在活动 run 时响应附 `rebuilding: {run_id, scope}` 元数据。UI 按 #308 的真实状态语言显示 REBUILDING/partial，不得把重建中的范围渲染成最终值。

## 5. 并发约束

1. v1 保持**单 active writer**；这是持久化正确性的唯一来源，继续作为发布门禁检查项（沿 #310 §8.3）。
2. 单进程内执行顺序：增量批与 run 串行交错；任一时刻至多一个活动 run。
3. 活动 run 覆盖的 binding×窗口内，增量路径跳过命中该范围的候选；这些观察由 run 收尾后的下一轮增量补上（canonical view 按 event-time 重算，不丢）。
4. 不引入 lease 服务、分布式锁或心跳协议——它们属于多写入者议题，而多写入者仍被雾区的 HA 触发条件挡住。

## 6. 失败恢复

- Run 按 `toYYYYMM(period_end)` 月分区切成 chunk；每完成一个 chunk 追加 `RUN_PERSISTED_CHUNK`（cursor）。
- Chunk 写入 = 单条 INSERT 批次；崩溃残留行带 `rebuild_run_id`。恢复时先对该 run 未完成 chunk 的残留行做 `ALTER DELETE`，再重放该 chunk——保证同一 chunk 至多一份输出。
- `RUN_COMPLETED` 仅在全部 chunk 完成、且收尾校验（受影响逻辑键的最新 revision 全部来自本 run 或其后）通过后追加；校验失败 → `RUN_FAILED`，旧版本因从未被删除仍是完整一致的结果集。
- 任意时刻崩溃，默认查询看到的都是某个前缀一致的版本集合（latest-revision-wins 的性质），不需要额外的 cut-over 协议。

## 7. 对现有契约的影响

| 契约 | 影响 |
| --- | --- |
| #310 实施规格 | Step 1 迁移同时落地 `fact_revision`/`rebuild_run_id`/ORDER BY 扩展；Step 3 候选判定升级为 prev 比较；验收门禁追加"晚到观察使旧配对 Fact 产生更高 revision 新行，且旧行不再参与聚合"。 |
| Energy Series 公共契约 | `datasetRevision` 语义升级 + `rebuilding` 元数据；字段名不变，客户端无需感知内部结构（沿 #307 原则）。 |
| data-ownership.v1.json | `analytics.energy_rebuild_runs` 归 `analytics-read-model-projector` 写入；`cube_analytics_reader` 只读。 |
| #304 ownership | 不新增跨域直写；run ledger 留在 Energy Processing 自己的数据集内。 |

## 8. 明确不做

- 多写入者/HA 部署及其协调机制（雾区不变）；
- ReplacingMergeTree、可见性列、轻量删除作为常规路径、跨月分区事务；
- 自动触发全站回填（一切大范围重算必须经审批的显式 run）;
- 为未到来的第二能源类型预演 rebuild 泛化。

## 9. 后续边界

W2–W6 各波次的决策票据直接引用本文档的触发分类与表示语义；实施规格（迁移 DDL、candidate 升级的精确算法、Cube member 改造清单）在 W0 首切片通过 #310 门禁后，随首个需要修正能力的波次单独开票，不在地图阶段展开。
