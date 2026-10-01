# 集中能源架构：本轮源码审查记录

日期：2026-09-07。状态：REVIEWED FOR PROPOSAL；未实施，不能标记本地已符合目标。适用方案：[集中能源架构审查](./centralized-energy-architecture-review-2026-09-07.md)。

本记录区分本次真实读取的官方材料、原有审查记录和待补材料。没有将README、既有本地实现、函数名中的Production或测试文件存在当作完整行为证明。未复制上游实现代码。

## 1. 固定版本

| 参考 | Release | Commit | 本次核实方式 |
| --- | --- | --- | --- |
| ThingsBoard CE | v4.3.1.1 | c2a52e46c44e308ddee430e7266b8e10eddde9c4 | 官方git ls-remote tag；固定commit raw源码 |
| OpenEMS | 2026.7.0 | 2e2792d59fc5ba3b99ce3cf98d15081c0a74895e | E:/Code/openems本地官方引用git rev-parse/git show，子审查完成 |
| MyEMS | v6.7.0 | be6e6ce8ddeac57afb04bddb9621501fb555cab0 | 官方git ls-remote；临时目录depth1/no-checkout引用；git show |

这些是已有项目固定参考的复核，不是最新版或升级推荐。MyEMS参考位于OS临时目录，不把独立checkout加入产品仓库。官方GitHub API触发匿名速率限制后使用公开git/raw读取，没有因此推断测试不存在。

## 2. ThingsBoard：组合运行角色与领域调用

固定源码根：[ThingsBoard固定提交](https://github.com/thingsboard/thingsboard/tree/c2a52e46c44e308ddee430e7266b8e10eddde9c4)。本次读取：

- `common/queue/src/main/java/org/thingsboard/server/queue/discovery/DefaultTbServiceInfoProvider.java`：init中monolith展开全部ServiceType，其他运行模式选择配置角色；运行角色不等于每个领域独立进程。
- `application/src/main/java/org/thingsboard/server/controller/AssetController.java`：controller调用注入的asset业务对象，使用tenant与permission检查；没有要求每个领域方法经内部HTTP。
- `application/src/test/java/org/thingsboard/server/controller/AssetControllerTest.java`：审阅保存后的Tenant归属、跨Tenant Customer assignment拒绝等相关片段；不将这些测试等同于HVAC的Site/RLS/即时撤销验证。
- `common/queue/src/main/java/org/thingsboard/server/queue/memory/InMemoryTbQueueConsumer.java`：poll取存储消息，commit为空。
- `common/queue/src/test/java/org/thingsboard/server/queue/memory/DefaultInMemoryStorageTest.java`：批量取出和lag减少测试；测试没有证明持久投递。
- 官方文档：[Monolithic architecture](https://thingsboard.io/docs/reference/architecture/monolithic/)。文档为读取时版本；精确机制以固定代码为准。

**ADOPT：**同一运行进程组合多个职责，领域调用与网络传输分离；保持服务端授权。

**ADAPT：**HVAC的同进程调用候选保留Tenant/Site、RLS、撤销、精确Command授权、审计，真实跨进程保持mTLS。相关本地证据为energy-api embedded_energy、Gateway Registry和Registry server。首先改只读链；本轮未定义完整类型Interface或更改威胁模型。

**REJECT：**将内存queue/空commit视为耐久事务Outbox替代品；复制Java actor/Kafka集群拓扑作为数百站点的无条件前置；移植Customer权限替代HVAC Site模型。

固定参考未给本轮候选的本地transport移除、权限撤销和全部command行为提供直接证明，需本地窄行为测试与压测。当前多张SPIFFE凭据在同进程内不构成进程攻陷隔离，是本地部署事实的推论，不是上游性能结论。

## 3. OpenEMS：生产Edge与周期/协议交接

固定源码根：[OpenEMS固定提交](https://github.com/OpenEMS/openems/tree/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e)。本次子审查使用git show读取：

- `io.openems.edge.core/src/io/openems/edge/core/cycle/CycleWorker.java`：过程映像切换、周期与Controller顺序、write阶段、耗时测量。
- `io.openems.edge.bridge.modbus/src/io/openems/edge/bridge/modbus/api/worker/ModbusWorker.java`：协议worker、读写同步、故障设备处理。
- `io.openems.edge.bridge.modbus/test/io/openems/edge/bridge/modbus/api/worker/internal/CycleTasksManagerTest.java`：读取前145行，write事件、读任务/过程映像和短周期相关用例；不声称全部测试已执行。
- `io.openems.edge.scheduler.fixedorder/test/io/openems/edge/scheduler/fixedorder/SchedulerFixedOrderImplTest.java`：固定配置次序。
- `doc/modules/ROOT/pages/edge/architecture.adoc`：异步设备通信、控制过程映像和硬实时责任。

**ADOPT：**通信与控制调度分开、显式过程映像与顺序、总线故障隔离。

**ADAPT：**生产Go Edge组合复用libs/edgecontrol；真实和模拟Adapter使用相同Seam；保留已记录的Critical Controller fail-closed和云端租约，设备/PLC硬件保护不能被云端取代。

**REJECT：**将模拟器包当生产Edge的长期Owner；用fake driver类型标签证明真实协议；为了参考机制整体引入Java/OSGi。

本地对应：tools/eg8200-simulator/internal/simulator/edge_runtime.go、publisher/main.go、libs/edgecontrol/driver.go和timedata.go。未做真实Modbus设备验收，不能宣布周期稳定性和离线恢复已达到生产目标。

## 4. MyEMS：能源处理职责与测试证据强弱

固定源码根：[MyEMS固定提交](https://github.com/MyEMS/myems/tree/be6e6ce8ddeac57afb04bddb9621501fb555cab0)。本次读取：

- `myems-normalization/main.py`：分别启动Meter、OfflineMeter、VirtualMeter、VirtualPoint、DataRepair处理。
- `myems-aggregation/main.py`：按计量/空间等对象和energy/billing/carbon职责启动处理；读到的并行进程结构不等同于可靠容量或任务恢复保证。
- `myems-aggregation/meter_billing.py`：读取前160行，所有meter配置读取、增量处理的职责说明、数据库组合与重试。未把注释描述全部当作后续算法已查实。
- `myems-aggregation/test_tariff.py`：实际是连接费率逻辑后打印结果的脚本，没有断言；不能说经过该文件证明了计费正确性。
- `myems-normalization/README.md`、`myems-aggregation/README.md`：读取官方职责说明和部署入口。
- 固定git tree搜索test文件，看到test_tariff、API数据库/MQTT/workflow、Modbus test等；本轮未读/执行所有测试。

**ADOPT：**采集、规范化/修复、能源/账单/碳排汇总具有不同职责，能源模型不等于通用遥测模型。

**ADAPT：**映射到HVAC已有Energy、Metric、Settlement等Owner和Go worker；保持版本/质量/时态绑定，不复制物理数据库数量。元数据批量与增量处理是本地明确调用/扫描风险导出的候选，不虚称上游已提供相同实现。

**REJECT：**把每个细分处理函数直接部署为长期进程；照搬全量枚举和固定轮询作为数百站点性能方案；把打印式测试当作产品行为门禁。

## 5. ClickHouse：方向证据已读，精确机制未裁决

本地canonical镜像：`clickhouse/clickhouse-server:26.3.12.3`，见deploy/platform/phase1/compose.yaml。本次未完成该版本源码commit与相关测试的固定读取。

已读官方文档：[Selecting an insert strategy](https://clickhouse.com/docs/concepts/best-practices/selecting-an-insert-strategy)。其支持客户端批量减少同步INSERT固定开销、保持重试批次内容与顺序的方向。文档为滚动版本，不能替代本地MergeTree/物化视图的精确去重合同。

本地已读：history_clickhouse.go、history.go、history_postgres.go相关claim片段、001-telemetry-history.sql、canonical telemetry-worker循环。单行token与有限窗口是事实；缺少长期重放证明不能通过“换一个批token”掩盖。

**ADAPT候选，实施前待审：**有界微批、稳定持久批次、确认/恢复与物化汇总一致。需要补读固定版本的MergeTree去重实现、窗口/重试测试及物化视图重复输入测试，随后裁决具体方法。

**REJECT：**即刻启用fire-and-forget确认；宣称租约或ReplacingMergeTree单独提供全链路exactly-once；在没有上述证明时修改生产DDL。

## 6. Pi、Scheduler与依赖选择范围

本轮Pi建议落实已接受ADR0014及既有固定Pi 0.84.4审查，不重新选择框架。本次直接核对本地bootstrap/persistence与Compose；没有重新读取Pi全部上游，亦没有新增Pi机制。

Scheduler cron解析、Identity提供方、Go module收敛只列有界审查候选。未完成River/cron/Identity替代品的固定源码/测试审查，不引入新依赖，不作库版本选择。继续使用现有项目锁定依赖与已证明合同。

## 7. 对现有架构决策的影响

- ADR0013：保留领域单Owner和数据职责；同进程Interface符合原则，但现有listener部署条款需要修订。
- ADR0012：保留云边控制分工；提取生产Edge组合兑现既有决策。
- ADR0014：删除旧checkpoint依赖和Graph路径兑现既有决策；保留业务记录。
- ADR0003/0008：不删除Current权威或历史查询职责；常态增量和微批实施时更新相关过时路径与实现说明。
- 当前Phase 1单机基线：本报告不自动切换部署。双API/外置数据/HA是有触发条件的后续实施，必须有相应真实证据。
