# OpenEMS 2026.9.0 源码参考评审

- 版本：OpenEMS tag `2026.9.0`，commit `14f0ded`（"Merge branch 'release/2026.9.0'"），本地浅克隆，只读。
- 依据：只看源码和仓库内 asciidoc（`doc/modules/ROOT/pages/`），没有用二手资料。路径相对 OpenEMS 仓库根，`:N` 是行号。为了省篇幅，bundle 内的 `src/io/openems/<...>/` 前缀写成 `…/`，例如 `io.openems.edge.core/…/cycle/CycleWorker.java` 指的是 `io.openems.edge.core/src/io/openems/edge/core/cycle/CycleWorker.java`。
- 目的：给本平台（Go + TypeScript，冷站优先，现场边缘网关做 Modbus/BACnet 采集和控制，第一阶段单机 Compose）的架构评审提供一手对照，重点是边缘控制运行时。
- 说明：标为"推断"的是从代码结构推出来的结论，没有运行验证。

规模：仓库根目录有 200 个 `io.openems.edge.*` bundle、19 个 `io.openems.backend.*` bundle；技术栈是 Java 21 + OSGi/bnd + Angular/Ionic（`AGENTS.md:5-11`）。Edge 的目标硬件是"1gb ram, 2 cores, 4gb disk"（`AGENTS.md:7`）。

---

## 1. Edge 运行时：Component / Channel / Nature / Cycle

### 1.1 OSGi 组件模型

- 每个设备、控制器、服务都是一个 OSGi Declarative Services 组件，实现 `OpenemsComponent`，用 Component-ID（`ess0`、`meter0`）寻址（`doc/modules/ROOT/pages/coreconcepts.adoc:56-75`）。OSGi 允许运行中启用、修改、停用组件，依赖关系由框架重新连线（同上 `:62-63`）。
- 依赖靠 `@Reference` + LDAP target 过滤器按 ID 注入，target 由配置项拼出来，例如 `target = "(&(id=${config.ess_id})(enabled=true))"`（`io.openems.edge.controller.ess.balancing/…/controller/ess/balancing/ControllerEssBalancingImpl.java:42-48`）。配置变更时，`OpenemsComponent.updateReferenceFilter` 直接改 ConfigAdmin 里的 target 属性（`io.openems.edge.common/…/component/OpenemsComponent.java:375-377`）。
- 运行时是 Apache Felix 7.0.5（`io.openems.edge.application/EdgeApp.bndrun:1`），Edge 打包了 54 个第三方 bundle（`grep -c "version='"` 统计 `EdgeApp.bndrun`），再加上 OpenEMS 自己的 bundle。

### 1.2 Channel：value 与 nextValue

- Channel 是组件的数据点，有类型、单位、读写模式，地址是 `Component-ID/Channel-ID`（`coreconcepts.adoc:77-126`）。
- 每个 Channel 有两个字段：`nextValue`（采集线程随时写入的最新值）和 `activeValue`（本周期控制器读到的值）（`io.openems.edge.common/…/channel/internal/AbstractReadChannel.java:46,50`）。
- `nextProcessImage()` 把 `nextValue` 复制到 `activeValue`，触发 `onUpdate`；值有变化时再触发 `onChange`；最后把新值追加进 `pastValues`（同文件 `:116-141`）。`pastValues` 只保留 5 分 10 秒（`io.openems.edge.common/…/channel/Channel.java:56`），后面 rrd4j 和 5 分钟聚合上送都从这里取数据。
- 累计量单位（电量这类 cumulated unit）如果遇到 `null` 会被直接忽略，保证单调不减（`AbstractReadChannel.java:183-193`）。
- 读值 API 强制调用方处理"未定义"：`getOrError()` / `orElse()`（`coreconcepts.adoc:92-97`）。

### 1.3 Cycle 与 Process Image

`CycleWorker.forever()` 是整个 Edge 的主循环（`io.openems.edge.core/…/cycle/CycleWorker.java:36-125`），默认周期 1000 ms（`io.openems.edge.common/…/cycle/Cycle.java:17`）：

1. 喂 systemd watchdog（`:43`）
2. `BEFORE_PROCESS_IMAGE` 事件（`:49`）
3. 对所有启用组件的所有 Channel 调 `nextProcessImage()`（`:54-59`），然后单独更新 `Sum` 组件（`:64-68`）
4. `AFTER_PROCESS_IMAGE` → `BEFORE_CONTROLLERS`（`:73,78`）
5. 按 Scheduler 顺序执行控制器（`:84-89`）
6. `AFTER_CONTROLLERS` → `BEFORE_WRITE` → `EXECUTE_WRITE` → `AFTER_WRITE`（`:94-109`）
7. 记录实际周期耗时（`:120-121`）

所有事件都是同步分发（`io.openems.edge.common/…/event/EdgeEventConstants.java:18-81`；`EdgeApp.bndrun:12` 设了 `org.apache.felix.eventadmin.Timeout=0`）。Process Image 的思路取自 PLC：采集是异步的，但在一个周期里控制器看到的值不变（`doc/modules/ROOT/pages/edge/architecture.adoc:110-123`）。官方文档也写明了边界：Java 加 1 秒周期属于软实时，不适合虚拟惯量、消防这类硬实时任务（同文件 `:29-31`）。

`AbstractWorker` 按"周期时间减去本轮耗时"休眠（`io.openems.common/…/worker/AbstractWorker.java:159-168`），所以周期是"尽力 1 秒"，超时不补偿。

### 1.4 Scheduler 决定控制器顺序

- `Scheduler.getControllers()` 返回有序的控制器 ID 列表。实现有：`AllAlphabetically`（先放配置里排好的 ID，其余按字母序补齐，`io.openems.edge.scheduler.allalphabetically/…/SchedulerAllAlphabeticallyImpl.java:58-70`）、`FixedOrder`、`Daily`、`JSCalendar`（四个目录见 `io.openems.edge.scheduler.*`）。
- 存在多个 Scheduler 时，CycleImpl 用 `TreeSet` 按 **Scheduler ID 排序**；源码注释还写着"按 cycleTime 排序"，已经和代码不一致（`io.openems.edge.core/…/cycle/CycleImpl.java:55-58`）。
- 文档里的约定是"后执行的控制器不允许覆盖先前的结果"（`edge/architecture.adoc:78-82`），所以**排在前面的优先级更高**。

### 1.5 写冲突怎么解决：没有通用机制

- 通用的 `WriteChannel.setNextWriteValue()` 只是把值存进 `nextWriteValueOpt`，**后写的覆盖先写的**（`io.openems.edge.common/…/channel/IntegerWriteChannel.java:25-27`；`WriteChannel.java:40-55`）。Channel 层没有优先级，也不记录是谁写的。
- 文档说的"后执行者不能覆盖"，实际只在 ESS 功率上实现了：`ManagedSymmetricEss` 的 `SetActivePowerEquals/LessOrEquals/GreaterOrEquals` 等写通道挂了 `onChannelSetNextWrite(new PowerConstraint(...))`（`io.openems.edge.ess.api/…/ess/api/ManagedSymmetricEss.java:85-164`），每次写入都变成一条线性约束，交给 `EssPower` 求解器；`addConstraintAndValidate` 发现约束不可解就抛异常（`io.openems.edge.ess.core/…/power/v1/PowerDistributionHandlerV1.java:84-98`）。于是先执行的控制器先占住可行域，后执行的写入如果冲突，这个控制器就会失败。求解器在 `AFTER_PROCESS_IMAGE` / `BEFORE_WRITE` / `AFTER_WRITE` 三个事件上工作（`io.openems.edge.ess.core/…/power/EssPowerImpl.java:59-61,193-200`）。
- 对数字输出、热泵 SG-Ready 这类非 ESS 资源，冲突只能靠"每个物理输出只配一个控制器"的人工约定来避免（推断：在 `io.openems.edge.io.api`、`io.openems.edge.heat.api` 里没有找到类似 Power 的仲裁层）。
- 外部 API 写入（Backend/REST/Modbus-TCP）也走控制器：`ApiWorker` 在 API 控制器的 `run()` 里**每个周期重新写一次**，超过 `DEFAULT_TIMEOUT_SECONDS = 10` 秒没有刷新就停止写入（`io.openems.edge.controller.api.common/…/ApiWorker.java:34-41,155-175,198-212`；`io.openems.edge.controller.api.backend/…/ControllerApiBackendImpl.java:194-196`）。这样远程设定值也落在 Scheduler 的优先级里，会被排在前面的安全控制器限制住（`edge/architecture.adoc:68-73`）。顺带一提：`setTimeoutSeconds` 的注释写"默认 60"，常量却是 10（`ApiWorker.java:41,180-182`），又一处注释和代码不一致。

### 1.6 控制器异常

- `executeSchedulersWithOptionalMeasure` 对每个控制器单独 try/catch，失败了就记日志、设 `RUN_FAILED`（Level.FAULT 的状态通道），然后接着执行下一个控制器（`CycleWorker.java:184-200`；`io.openems.edge.controller.api/…/controller/api/Controller.java:27,67-68`）。
- **但在这个版本里**，`controller::run` 被包在 `executeCycleStep` 里，而 `executeCycleStep` 自己把 `Exception` 吞掉只打 warn（`CycleWorker.java:135-146`）。结果外层 catch 永远收不到异常，紧接着又调用 `_setRunFailed(false)`（`:185-187`），控制器抛异常时 `RUN_FAILED` 反而会被清掉（推断，依据是控制流；`DebugUtils.measure` 会把异常往上抛，`io.openems.common/…/utils/DebugUtils.java:32-37`）。这说明：错误怎样暴露成状态，需要用测试锁住，不能只靠 review。
- 整个周期最外层还有一个 `catch (Throwable)`（`CycleWorker.java:111-117`），任何错误都不会让主循环停下来。被停用的控制器会被跳过，并设置 `IgnoreDisabledController`（`:179-182,88`）。

---

## 2. 设备驱动与 Modbus Bridge

### 2.1 Bridge 的调度

- Bridge 组件（`BridgeModbusTcpImpl` / `BridgeModbusSerialImpl`）只挂两个 Cycle 事件：`BEFORE_PROCESS_IMAGE → worker.onBeforeProcessImage()`，`EXECUTE_WRITE → worker.onExecuteWrite()`（`io.openems.edge.bridge.modbus/…/api/AbstractModbusBridge.java:147-151`）。
- `ModbusWorker` 是一个独立线程，不断取下一个 Task 去执行。成功就把该组件从"故障"名单移出，并清掉 `ModbusCommunicationFailed`；失败就加入故障名单，并对这个 Task 里的元素调用 invalidate（`…/api/worker/ModbusWorker.java:72-118`）。
- 每个周期的 Task 状态机是 `INITIAL_WAIT → WRITE → WAIT_BEFORE_READ → READ → FINISHED`（`…/api/worker/internal/CycleTasksManager.java:46-52`）。`onBeforeProcessImage` 时如果上一轮还没到 FINISHED，就置 `CycleTimeIsTooShort`（`:73`）。`onExecuteWrite` 把状态直接切到 WRITE，让写任务排到最前（`:20,114-116`）。
- 读任务优先级：每个周期执行**全部 HIGH 读任务 + 全部写任务 + 一个 LOW 读任务**（LOW 任务轮流排队）（`…/internal/TasksSupplierImpl.java:92-106,145-163`）。同一 unit 的请求保持顺序，HIGH 排在最后，好让数据尽量新鲜（`:129-130`）。
- **自适应等待**：`WaitDelayHandler` 学习所有任务的执行时长，把 READ 尽量推迟到下一次 `BEFORE_PROCESS_IMAGE` 之前，留 20 ms 余量（`…/internal/WaitDelayHandler.java:15,51-58,78-82`）。架构文档有对应的示意图（`edge/architecture.adoc:125-132`）。
- **故障设备退避**：失败次数 × 2 秒，最多等 5 分钟（`…/internal/DefectiveComponents.java:17-18,48`）。没到重试时间的故障组件，它的任务整轮丢弃（`TasksSupplierImpl.java:109-121`），一台离线设备不会拖慢整条总线。可以用 `retryModbusCommunication(sourceId)` 立即重试（`ModbusWorker.java:149-151`）。

### 2.2 寄存器到 Channel 的映射

驱动继承 `AbstractOpenemsModbusComponent`，在 `defineModbusProtocol()` 里声明 Task 和映射。以 Janitza UMG96RM-E 为例（`io.openems.edge.meter.janitza/…/umg96rme/MeterJanitzaUmg96rmeImpl.java:91-137`）：

```java
new FC3ReadRegistersTask(19000, Priority.HIGH,
    m(ElectricityMeter.ChannelId.VOLTAGE_L1, new FloatDoublewordElement(19000), SCALE_FACTOR_3), ...)
new FC3ReadRegistersTask(19068, Priority.LOW,
    m(ElectricityMeter.ChannelId.ACTIVE_CONSUMPTION_ENERGY, new FloatDoublewordElement(19068)), ...)
```

- 一个 Task 对应一段连续寄存器的一次功能码请求（FC1/2/3/4/5/6/16，见 `…/api/task/`）。元素类型覆盖有/无符号字、双字、四字、浮点、位字、字符串、Dummy 占位（`…/api/element/`），字序由 `WordOrder` 控制。
- 换算用 `ElementToChannelConverter`（`SCALE_FACTOR_n`、取反、按配置反向等）。SunSpec 设备用 `ElementToChannelScaleFactorConverter` 从另一个寄存器读比例因子（`…/api/ElementToChannelScaleFactorConverter.java`）。
- 驱动直接把设备点映射到 **Nature 的 ChannelId**（如 `ElectricityMeter.ChannelId.VOLTAGE_L1`），所以控制器只依赖 Nature，不依赖具体厂家。
- 同一个寄存器可以按配置映射到不同 Channel，例如电表安装方向反了时把消费和发电对调（同文件 `:128-137`）。

### 2.3 错误与过期数据

- 读失败时 `AbstractModbusElement.invalidate()` 给计数器加 1，达到 `invalidateElementsAfterReadErrors`（默认 1）就把值置为 `null`（`…/api/element/AbstractModbusElement.java:208-213`；`io.openems.edge.bridge.modbus/…/ConfigTcp.java:33-34`）。
- 失败不会沿用旧值，而是变成"未定义"，下游控制器必须用 `orElse`/`getOrError` 处理（见 1.2）。读值本身不带"最后成功时间"之类的质量戳；过期只表现为 null，再加上组件级的 `ModbusCommunicationFailed` 状态。
- 删除协议（组件停用）时会 invalidate 全部元素（`TasksSupplierImpl.java:71-88`）。

---

## 3. Nature：能力抽象

- Nature 就是"带 Channel 的 Java 接口"，放在 `*.api` bundle 里（`edge/architecture.adoc:143-176`）。设备类 Nature 包括 `ess.api`、`meter.api`、`evse.api`（`evcs.api` 已标为 deprecated）、`io.api`、`heat.api`、`thermometer.api` 等（同文件 `:153-165`）。
- 实现类必须把子 Nature 的所有父接口都显式写出来，否则检测不到（`AGENTS.md:22-24`）。Janitza 驱动就写了 `implements MeterJanitzaUmg96rme, ElectricityMeter, ModbusComponent, OpenemsComponent, ModbusSlave`（`MeterJanitzaUmg96rmeImpl.java:46-47`）。
- 控制器面向 Nature 编程：`ControllerEssBalancingImpl` 只引用 `ManagedSymmetricEss` 和 `ElectricityMeter`（`ControllerEssBalancingImpl.java:42-48`）；测试里用 `DummyManagedSymmetricEss` 替换即可（见第 9 节）。
- HVAC 相关的 Nature 很薄：`Thermometer` 只有一个 `TEMPERATURE` 通道，单位 0.1 °C（`io.openems.edge.thermometer.api/…/thermometer/api/Thermometer.java:25-26`）；`Heat` 只有 `STATUS` 和 `TEMPERATURE`（`io.openems.edge.heat.api/…/heat/api/Heat.java:28,40`）；`ManagedHeatElement` 是一组以功率为目标的写通道（`TARGET_ACTIVE_POWER` 等，`ManagedHeatElement.java:16-25`）；热泵控制器 `heatpump.sgready` 驱动的是继电器（`io.openems.edge.controller.io.heatpump.sgready/…/*Impl.java:78-90`）。**没有冷机、水泵、冷却塔、阀门、流量计、冷量表之类的 Nature**。冷站要用的语义（供回水温度、冷量、COP、启停联锁、最小运行/停机时间）都得自己建模。
- 告警模型：每个组件都有一个 `STATE` 汇总通道（StateCollector），取所有 `Level`（INFO/WARNING/FAULT）状态通道里的最高级别，持久化优先级为 VERY_HIGH（`io.openems.edge.common/…/component/OpenemsComponent.java:222-226`）。Sum 再把整站汇总成一个状态。这是"状态位"，不是带生命周期的告警事件。

---

## 4. Edge 持久化与断网补传

### 4.1 PersistencePriority：一个字段决定存什么、传什么

`PersistencePriority` 分 VERY_LOW/LOW/MEDIUM/HIGH/VERY_HIGH（`io.openems.common/…/channel/PersistencePriority.java:45-84`）。默认值是：状态通道 HIGH，其他 LOW；VERY_HIGH 留给 `Core.Sum`（同文件 `:26-31,80-84`）。

| 消费方 | 默认阈值 | 行为 | 证据 |
|---|---|---|---|
| Backend 实时 | `persistencePriority = HIGH` | 每周期只发变化值，每 5 分钟发一次全量（`TimestampedDataNotification`） | `io.openems.edge.controller.api.backend/…/Config.java:43-44`；`PersistencePriority.java:70-77`；`SendChannelValuesWorker.java:360-415` |
| Backend 聚合 | `aggregationPriority = LOW` | 每 5 分钟发平均值/最大值（`AggregatedDataNotification`），带随机延迟错峰 | `Config.java:46-47`；`SendChannelValuesWorker.java:59,71-74,125-135,171-218` |
| 本地 rrd4j | `persistencePriority = HIGH` | 每 300 s 一个步长（5 分钟平均） | `io.openems.edge.timedata.rrd4j/…/Config.java:23`；`Rrd4jConstants.java:9-10` |
| 断网补传 | `resendPriority = HIGH` | 应与 timedata 的阈值保持一致 | `Config.java:49-50` |

### 4.2 rrd4j

- 在 `AFTER_PROCESS_IMAGE` 时从 Channel 的 `pastValues` 里收集数据，异步写入（`io.openems.edge.timedata.rrd4j/…/TimedataRrd4jImpl.java:39,154`；`RecordWorker.java:107-134`）。累计量和普通量用不同的合并函数（`RecordWorker.java:173`）。
- 存档设计（Version1）：5 分钟分辨率保留 31 天，1 小时分辨率保留 334 天（`…/rrd4j/version/Version1.java:45-46`）。目前有 Version1-3 三个版本的 schema（`…/rrd4j/version/`），版本通过数据源名称编码（`Rrd4jConstants.java:12-18`）。
- 本地**没有秒级原始数据**；秒级只在 Channel 内存里保留 5 分钟。

### 4.3 InfluxDB（Edge 侧，可选）

`io.openems.edge.timedata.influxdb` 每 `noOfCycles`（默认 1）个周期写一次，队列上限 5000，可以设为只读（`…/timedata/influxdb/Config.java:42-48`）。这是给有本地 Influx 的大型站点准备的，默认部署用 rrd4j。

### 4.4 补传

- 发送失败时，Edge 置位 `UNABLE_TO_SEND`（Level.WARNING，VERY_HIGH，**一定会写进 rrd4j**），相当于在本地时序库里给"没发出去的时间段"做了标记；`LAST_SUCCESSFUL_RESEND` 记录补传进度（`io.openems.edge.controller.api.backend/…/api/ControllerApiBackend.java:28-33`；`SendChannelValuesWorker.java:444`）。
- `ResendHistoricDataWorker` 在重连后延迟 5 分钟启动（`ResendHistoricDataWorker.java:73`）。它先用 `getResendTimeranges(UNABLE_TO_SEND, lastResend)` 查出需要补的时间段，再按每段最多 5 分钟用 `queryResendData` 从 rrd4j 读出数据，以 `ResendDataNotification` 发送。每段成功后推进 `LAST_SUCCESSFUL_RESEND`，失败就停下，等下次触发（`ResendHistoricDataWorker.java:76,117-180`）。
- 所以补传的是 **5 分钟聚合值**，不是原始值（`doc/modules/ROOT/pages/backend/timedata.adoc:28-30`）。这种方案用"一个持久化状态通道 + 一个进度游标"实现，不需要单独的发送队列文件，思路很省事。代价是补回来的数据分辨率低于在线时的数据。

---

## 5. Edge 与 Backend 的协议

- **传输**：Edge 的 `Controller.Api.Backend` 用 WebSocket 客户端连接（`…/controller/api/backend/WebsocketClient.java:36-38`），断线由 `ClientReconnectorWorker` 负责重连。消息全部是 JSON-RPC 2.0（`io.openems.common/…/jsonrpc/base/JsonrpcRequest.java:18-26`）。
- **认证**：只靠 HTTP 头里的 `apikey`。Backend.Edge.App 用 Metadata 下发的 `apikey → edgeId` 映射查表（`io.openems.backend.edge.application/…/edge/server/WebsocketServer.java:63-65`；`…/edge/application/Cache.java:36-40`）。文档要求 apikey 必须足够随机，因为它隐含了授权（`doc/modules/ROOT/pages/backend/metadata.adoc` 的 Edges 一节）。没有 mTLS，也没有设备证书或密钥轮换机制（推断：源码里没找到）。
- **Edge → Backend 通知**（`io.openems.common/…/jsonrpc/notification/`）：`EdgeConfigNotification`（连接建立时和配置变化时发送，`OnOpen.java:26-30`；`ControllerApiBackendImpl.java:220-227`）、`TimestampedDataNotification`、`AggregatedDataNotification`、`ResendDataNotification`、`SystemLogNotification`、`LogMessageNotification`。Backend 的 `Edge.Manager` 按 method 分发，数据写 TimedataManager，配置写 Metadata（`io.openems.backend.edge.manager/…/OnNotification.java:147-157,171,202-210`）。
- **Backend → Edge 请求**：`EdgeManagerImpl.send(edgeId, user, role, request)` 把请求包成 `AuthenticatedRpcRequest(user, role, payload)`，再套一层 `EdgeRpcRequest(edgeId, ...)`（`…/edge/manager/EdgeManagerImpl.java:153-162`）。Edge 解开后把 `User`（包含角色）放进上下文，交给各组件注册的 JsonApi 路由（`io.openems.edge.controller.api.backend/…/handler/AuthenticatedRequestHandler.java:44-65`）。可用的操作包括：
  - 组件配置的增删改查 `Create/Update/DeleteComponentConfig`，需要 ADMIN（`io.openems.edge.controller.api.common/…/handler/ComponentConfigRequestHandler.java:35-79`）
  - `SetChannelValue`，需要 ADMIN，经 ApiWorker 每周期重写，10 秒超时（见 1.5）
  - `ExecuteSystemCommand`，需要 ADMIN，在 Edge 上执行任意 shell 命令，可以通过 `echo <password> | sudo -S` 提权（`io.openems.edge.core/…/core/host/HostImpl.java:193-196,284-285`；`…/host/Bash.java:102,118`）
  - 系统更新和重启，需要 OWNER（`HostImpl.java:170-176,255-270`）
- **路由与规模**：较新的结构把面向 Edge 的 WebSocket 拆成独立的 `Backend.Edge.App`（可以部署多台），它再通过 WebSocket 中继到中心的 `Edge.Manager`（`doc/modules/ROOT/pages/backend/architecture.adoc:16-47`）。中心侧找某个 edge 的连接用的是**遍历所有 Edge.App 连接**、逐个 `containsEdgeId`（`EdgeManagerImpl.java:207-215`）。线程池默认 10（`io.openems.backend.edge.manager/…/Config.java:15`；`io.openems.backend.edge.application/…/Config.java:20,28`）。源码里**没有写明单台 Backend 能承载多少 Edge**。拆出 Edge.App 本身就说明，单进程直连海量 Edge 曾经是瓶颈（`backend/architecture.adoc:17`）。

---

## 6. Backend

- **组成**：Metadata、Timedata、Ui.Websocket、Edge.Manager、B2B，都是同一个 OSGi 进程里的服务（`backend/architecture.adoc:10-15`），第三方 bundle 51 个（`io.openems.backend.application/BackendApp.bndrun`）。
- **Metadata**：
  - `metadata.odoo`：生产实现，依赖 Odoo ERP 及其 PostgreSQL。Edge 每次上报配置时，和库里的旧配置做 `EdgeConfigDiff`，差异写入 `EdgeConfigUpdate` 表，形成配置变更历史（`io.openems.backend.metadata.odoo/…/MetadataOdoo.java:497-525`；`…/odoo/Field.java:179-201`）。
  - `metadata.file`：开发用，**所有用户对所有 Edge 都是 ADMIN**（`io.openems.backend.metadata.file/…/MetadataFile.java:90,357-358`）。
  - `metadata.dummy`：测试用。
- **Timedata**：`TimedataManager` 把数据写给**所有** Timedata 实现，读取时取**第一个**能返回结果的（`doc/modules/ROOT/pages/backend/timedata.adoc:10-14`）。仓库里有 `backend.timedata.influx`、`backend.timedata.aggregatedinflux`（专存 5 分钟聚合，给 UI 做快速查询）和 `dummy`。**2026.9.0 里没有 TimescaleDB 实现**（`grep -ril timescale` 在 Java 源码里没有结果）。聚合在 Edge 上算好再上送，目的是降低数据库 CPU（`backend/timedata.adoc:34-35`）。
- **Ui.Websocket**：UI 通过 `EdgeRpcRequest` 把请求经 Backend 转发给 Edge，用 `SubscribeChannelsRequest` 订阅实时通道（`io.openems.backend.uiwebsocket/…/impl/OnRequest.java:139-140,330`），默认限流 `requestLimit = 20`（`…/uiwebsocket/impl/Config.java:18`）。UI 和 Edge 共用一套 JSON-RPC 方法，同一个 Angular UI 既能直连 Edge，也能经 Backend 访问。
- **B2B**：`b2bwebsocket` 用 HTTP Basic 认证，专有方法只有 `SubscribeEdgesChannels`，其余请求转给通用处理器（`io.openems.backend.b2bwebsocket/…/OnRequest.java:25-35`；`OnOpen.java:49`）；`b2brest` 是 JSON-RPC over REST（`io.openems.backend.b2brest/…/RestHandler.java`）。
- **权限**：只有四个角色：`ADMIN(0) < INSTALLER(1) < OWNER(2) < GUEST(3)`，数值越小权限越高（`io.openems.common/…/session/Role.java:9-24`）。用户有一个全局角色，每个 (user, edge) 还有一个角色，由 Metadata 提供，Odoo 实现在内存里缓存（`io.openems.backend.common/…/metadata/Metadata.java:432-465`；`MetadataOdoo.java:778-795`）。**没有租户、组织、站点组这一层**，也没有细粒度权限（某个组件/某个通道/只读控制）。多租户只能交给 Odoo 的业务模型去做。
- **告警**：`backend.alerting` 只有两个处理器：Edge 离线（`OfflineEdgeHandler`）和整站 Sum State 达到 WARNING 及以上（`SumStateHandler`），通过邮件发出（`io.openems.backend.alerting/…/Alerting.java:96-101`；`…/handler/SumStateHandler.java:121-122`）。没有告警确认、工单、点位级规则或趋势分析。

---

## 7. 配置管理

- Edge 配置就是 OSGi ConfigAdmin 的配置，Felix 把它存成纯文本文件，目录由 `felix.cm.dir` 指定（`io.openems.edge.application/EdgeApp.bndrun:10`；`doc/modules/ROOT/pages/edge/configuration.adoc:117`）。原生的管理入口是 Felix Web Console 的 8080 端口（`edge/configuration.adoc:36-41`）。
- 远程修改走 `UpdateComponentConfigRequest` → `ComponentManagerImpl.handleUpdateComponentConfigRequest`：读出当前属性，合并修改，写上 `_lastChangeBy` / `_lastChangeAt`，再调用 `config.update()`（`io.openems.edge.core/…/componentmanager/ComponentManagerImpl.java:498-520,614-617`）。OSGi 随后重启这个组件。
- **Edge 本地没有版本历史**，只有"最后修改人/时间"两个属性。历史只在 Backend：每次 `EdgeConfigNotification` 到达后算 diff 存进 Odoo（见第 6 节）。这是"事后记录"，没有期望配置/实际配置对账，也没有回滚。
- 配置生效时，组件会被 OSGi 停用再激活，Channel 也重建（推断，依据 DS 生命周期和 `AbstractReadChannel.java:90-92` 的清理回调），所以改一个参数可能让设备通信中断一个周期以上。
- 上层还有 AppManager（`io.openems.edge.core/…/core/appmanager/`），把多个组件配置打包成"App"安装，属于商业化的配置模板层。

---

## 8. 能源调度与优化

- `io.openems.edge.energy` 用遗传算法库 **Jenetics** 做优化：Mode 对应 Gene，Period（15 分钟）对应 Genotype 里的一个下标，一个 Schedule 对应一个 Genotype；全局上下文每 15 分钟重建一次（`io.openems.edge.energy/readme.adoc:1-40`）。
- 参与优化的控制器实现 `EnergySchedulable`，通过 `EnergyScheduleHandler` 提供 `modes()`、`simulate()`、`evaluate()`、`applySchedule()`（`io.openems.edge.energy.api/…/handler/EnergyScheduleHandler.java:22-207`）。也就是说：**控制器自己定义可选模式和仿真模型，优化器只搜索模式组合**。
- `EnergySchedulerImpl` 注入 `PredictorManager`（负荷/光伏预测）和 `TimeOfUseTariff`（电价）（`io.openems.edge.energy/…/EnergySchedulerImpl.java:77-82`）。优化在线程里持续运行，可以被取消；出错后 30 秒重启（`…/optimizer/Optimizer.java:40-55,205-268`）。
- **执行**：调度结果不会直接写设备。`TimeOfUseTariffControllerImpl.run()` 每个周期从 handler 取 `getCurrentPeriod()`，结合实时 Sum/ESS 数据算出 `ApplyMode`（BALANCING/DELAY_DISCHARGE/CHARGE_GRID…），再通过普通的 ESS 写通道下发（`io.openems.edge.controller.ess.timeofusetariff/…/TimeOfUseTariffControllerImpl.java:205-267`；`StateMachine.java:5-20`）。计划只是控制器的输入，安全约束和优先级照常生效。
- 预测器有 `persistencemodel`、`similardaymodel`、`profileclusteringmodel`、`production.linearmodel`、`lstm` 五种（`io.openems.edge.predictor.*` 目录）；电价源有十几个，按国家/供应商各写一个 bundle（`io.openems.edge.timeofusetariff.*`）。
- 同时存在 V1（只管 ESS）和 V2（通用 EnergySchedulable）两套实现，控制器里按版本分支（`TimeOfUseTariffControllerImpl.java:216-262`）。

---

## 9. 仿真与测试

- **控制器单测不需要 OSGi**：`ComponentTest` / `ControllerTest` 用 `addReference("ess", new DummyManagedSymmetricEss("ess0"))` 注入假依赖，`activate(config)`，然后用 `next(new TestCase().input(...).output(...))` 跑一个完整周期（`io.openems.edge.controller.ess.balancing/test/…/BalancingImplTest.java:21-41`）。
- `AbstractComponentTest.next()` 在测试里按真实顺序重放 Cycle：`BEFORE_PROCESS_IMAGE` → `nextProcessImage` → 写入 inputs → … → `EXECUTE_WRITE` → `AFTER_WRITE` → 校验 outputs（`io.openems.edge.common/…/common/test/AbstractComponentTest.java:979-1010`）。`timeleap(TimeLeapClock, ...)` 用来控制时间（`:406`），所以"最小运行时间""延时"这类逻辑可以不 sleep 就测。
- Dummy 套件很全：`DummyComponentManager`、`DummyCycle`、`DummyEventAdmin`、`DummyMeta` 等（`io.openems.edge.common/…/common/test/`），每个 `*.api` bundle 都附带自己的 Dummy（如 `DummyThermometer`、`DummyManagedHeatElement`，见第 3 节路径）。
- Modbus 驱动测试有 `ModbusSlaveSimulator` 和 `DummyModbusComponent`（`io.openems.edge.bridge.modbus/test/io/openems/edge/bridge/modbus/`）。
- `io.openems.edge.simulator` 提供 CSV 数据源、反应式 ESS/电表/温度计仿真、`SimulatorModbus`。`SimulatorApp` 能在一次 JSON-RPC 调用里删除现有配置、换成 `TimeLeapClock`、以 `DO_NOT_WAIT` 跑满速周期，完成"加速一天"的整站仿真（`io.openems.edge.simulator/…/simulator/app/SimulatorAppImpl.java:166-210,290,319,367`）。

---

## 10. 源码里能看到的弱点和代价

1. **OSGi 复杂度**：依赖注入写在注解和 LDAP 字符串里（`target = "(&(id=${config.ess_id})(enabled=true))"`），出错要到运行时才暴露；还得手动列出所有父接口，否则 Nature 检测失效（`AGENTS.md:23`）。配置变更等于组件重启（第 7 节）。
2. **注释和代码不一致、错误被吞**：Scheduler 排序的注释（`CycleImpl.java:55-58`）、ApiWorker 超时的注释（`ApiWorker.java:41,180`）、控制器异常把 `RUN_FAILED` 清掉（第 1.6 节）。"失败不中断循环"的设计让问题长期只停留在 warn 日志里。
3. **写冲突没有通用仲裁**：只有 ESS 功率有约束求解（第 1.5 节）。其他执行器默认后写覆盖，要靠配置纪律保证。
4. **控制周期全局只有一个**：所有控制器共用 1 秒（`Cycle.java:17`），HVAC 里慢变量（冷机加减机、按分钟甚至十分钟算）和快变量（压差 PID）只能在控制器内部自己分频。
5. **JSON-RPC 弱类型、两套并存**：旧式手写 `*Request` 类 44 个（`io.openems.common/…/jsonrpc/request/`），新式 `EndpointRequestType` + `JsonSerializer` 路径 DSL（`…/jsonrpc/serialization/`）并存，载荷是 Gson `JsonObject`。没有 IDL 或生成的客户端，前后端靠手工对齐。
6. **UI/Backend/Edge 耦合**：UI 直接调用 Edge 的方法和 Channel 地址（`ess0/Soc` 这类字符串），Backend 基本只是转发和存时序数据。Channel ID 一改，前端、历史数据和 B2B 集成都会断。
7. **云端业务很薄**：没有租户层、资产台账、工单、告警生命周期，也没有能耗分析模型。告警只有"离线 + 整站状态"两种邮件（第 6 节），用户与业务元数据依赖 Odoo ERP。
8. **安全面**：Edge 只靠 apikey 认证；ADMIN 可以远程执行任意 shell 并带 sudo 密码（`Bash.java:102`）；Felix Web Console 端口 8080（`EdgeApp.bndrun:9`）。
9. **数据保真度**：本地只存 5 分钟 RRD，补传也是 5 分钟聚合；秒级数据只在内存里保留 5 分钟（第 4 节）。对冷站 COP 诊断、故障回溯来说偏粗。
10. **代码体积**：200 个 Edge bundle 里大部分是厂家驱动和电价源，每个都是独立 OSGi bundle、独立 Config 注解类。对一个小团队来说，维护成本主要花在这层上。

---

## 值得吸收的（尤其是边缘控制）

1. **Process Image（value/nextValue）**：采集线程只写 `nextValue`，周期开始时统一切换，控制器在一个周期内看到一致的快照。Go 里用"每周期复制一份只读快照"就能实现。证据：`AbstractReadChannel.java:46-50,116-141`；`edge/architecture.adoc:110-123`。
2. **固定阶段的控制周期**：读 → 控制器 → 写，阶段事件同步执行，写操作集中在 `EXECUTE_WRITE`。这样采集、控制、下发的时序可以推理，也可以测试。证据：`CycleWorker.java:36-125`；`EdgeEventConstants.java:18-81`。
3. **控制器按优先级串行，外部指令也是控制器**：远程设定值经 ApiWorker 进入同一条优先级链，会被安全控制器约束；写入要每周期刷新，超时即失效（相当于看门狗）。证据：`edge/architecture.adoc:68-82`；`ApiWorker.java:41,155-212`。
4. **约束式仲裁取代"最后写入获胜"**：ESS 把写入变成约束，先到的高优先级约束收窄可行域，冲突时显式失败。冷站可以照这个思路，对冷机/水泵的启停台数、频率范围、温度设定做约束聚合。证据：`ManagedSymmetricEss.java:85-164`；`PowerDistributionHandlerV1.java:84-98`。
5. **Modbus 调度细节**：HIGH 读任务每周期全读、LOW 轮询、写任务插到最前、根据学到的耗时把读推迟到周期边界、故障设备线性退避（上限 5 分钟）、读失败直接置空而不是沿用旧值。证据：`TasksSupplierImpl.java:92-163`；`CycleTasksManager.java:46-116`；`WaitDelayHandler.java`；`DefectiveComponents.java:17-48`；`AbstractModbusElement.java:208-213`。
6. **声明式点表映射**：驱动只声明"功能码 + 起始地址 + 元素类型 + 目标能力通道 + 换算"，控制器依赖能力接口而不是厂家。我们的点表/设备模板应当映射到冷站能力模型（冷机、泵、塔、冷量表）。证据：`MeterJanitzaUmg96rmeImpl.java:91-137`；`edge/architecture.adoc:143-165`。
7. **用一个字段决定存储和上送策略**：每个点声明一次优先级，本地存储、实时上送、聚合上送、补传都按它过滤。证据：`PersistencePriority.java:6-84`；`io.openems.edge.controller.api.backend/…/Config.java:43-50`。
8. **"发送失败"也是一条时序数据**：断网期间把 `UNABLE_TO_SEND` 写进本地时序库，重连后查出这些时间段，按 5 分钟分段补传，再推进游标。不需要额外的队列存储，补传进度可以观察。证据：`ControllerApiBackend.java:28-33`；`ResendHistoricDataWorker.java:117-180`。
9. **实时流只发变化值，定期发全量**：每周期只发变化值，每 5 分钟发一次全量快照，带宽小，接收端也能自愈。证据：`SendChannelValuesWorker.java:360-415`；`backend/timedata.adoc:19-22`。
10. **优化结果只是控制器的输入**：Jenetics 搜索出"每 15 分钟用哪个模式"，执行时控制器每周期按实时数据重新计算下发值，安全约束依然有效。冷站的群控/寻优也应当输出模式和设定值区间，而不是直接写设备。证据：`io.openems.edge.energy/readme.adoc`；`TimeOfUseTariffControllerImpl.java:205-267`。
11. **不依赖容器的周期级测试框架**：Dummy 依赖 + `input/output` + `timeleap`，按真实阶段重放一个周期，控制器逻辑可以在 CI 里不接硬件就测。Go 里可以用同样的表驱动结构。证据：`AbstractComponentTest.java:979-1010`；`io.openems.edge.controller.ess.balancing/test/…/BalancingImplTest.java:21-41`。
12. **面向 Edge 的连接层可以单独扩容**：接入网关（认证 + 中继）和中心业务分开部署，单机部署时也可以合在一起。证据：`backend/architecture.adoc:16-47`。

## 不应照搬的

1. **OSGi/Felix 与注解 + LDAP 依赖注入**：Go 的 Edge 用显式构造和编译期接口就够了；配置变更应该做成在线重载，而不是重启组件。证据：`EdgeApp.bndrun:1-12`；`ControllerEssBalancingImpl.java:42-48`；`AGENTS.md:23`。
2. **吞掉异常、只打 warn**：控制器失败、通信失败必须变成显式状态并上报，而且要有测试保证。不要照抄 `executeCycleStep` 那种 catch-all。证据：`CycleWorker.java:135-146,184-200`。
3. **Channel 层后写覆盖、只有 ESS 有仲裁**：我们应该给所有执行器（冷机启停、频率、阀位、设定值）统一定义写入方、优先级和约束聚合，不能只在一种设备上做。证据：`IntegerWriteChannel.java:25-27`。
4. **弱类型、两套并存的 JSON-RPC**：Edge↔云和云↔前端应以 OpenAPI/Protobuf 等单一契约生成代码；控制面（下发命令）也不应复用 UI 的通用通道写接口。证据：`io.openems.common/…/jsonrpc/request/`（44 个类）与 `…/jsonrpc/serialization/` 并存。
5. **UI 直接按 `组件ID/通道ID` 字符串访问设备**：前端应通过云端领域模型（站点/设备/指标）取数，让设备地址的变化只影响 owner 一处。证据：`coreconcepts.adoc:122-126`；`uiwebsocket/…/OnRequest.java:139-140,330`。
6. **远程任意 shell + sudo 密码、只靠 apikey 认证 Edge**：我们的边缘网关应只开放有限、有审计的运维动作，设备身份用证书或可轮换的密钥。证据：`HostImpl.java:193-196,284-285`；`Bash.java:102`；`edge/server/WebsocketServer.java:63-65`。
7. **全局四档角色、无租户层、靠 ERP 做多租户**：本平台从第一天就需要组织/站点范围和动作级权限，不要把 Odoo 式元数据当权威。证据：`Role.java:9-24`；`MetadataFile.java:357-358`；`MetadataOdoo.java:778-795`。
8. **本地只存 5 分钟 RRD，补传只补聚合值**：冷站诊断需要分钟级甚至更细的原始数据。边缘缓冲应保存原始值（有界、按时间淘汰），聚合交给云端。证据：`Rrd4jConstants.java:9`；`Version1.java:45-46`；`backend/timedata.adoc:28-30`。
9. **配置历史只在云端事后 diff**：应该用"期望配置版本 → 下发 → Edge 回报已生效版本"的对账模型，并支持回滚。证据：`MetadataOdoo.java:497-525`；`ComponentManagerImpl.java:614-617`。
10. **一个驱动/电价源一个 bundle，所有控制器共用一个周期**：冷站设备种类有限，应该用数据驱动的点表模板加少量协议驱动；控制回路按时间尺度分层（秒级 PID、分钟级群控、15 分钟寻优），不要全塞进 1 秒周期。证据：`io.openems.edge.meter.*` / `io.openems.edge.timeofusetariff.*` 目录；`Cycle.java:17`。
11. **云端告警只有"离线 + 整站状态"邮件**：OpenEMS 的云端定位是监控和转发，告警生命周期、工单、能效分析这些本平台的核心域要自己设计，不能指望从 OpenEMS 继承。证据：`io.openems.backend.alerting/…/Alerting.java:96-101`。
