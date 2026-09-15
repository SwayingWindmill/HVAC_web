# Node-RED 智慧能源 Edge Gateway 源码审查与架构裁决

日期：2026-09-13
状态：`EDGE OUTBOX 1.0.2 LIVE-VERIFIED`；协议采集、北向上报、顺序发送和耐久 Outbox 已在现场 EG8200 验证，独立控制内核仍未实施。

## 1. 审查问题与边界

本记录回答一个有限问题：如果现场网关采用 Node-RED 组装协议流程，ThingsBoard IoT Gateway/Gateway MQTT API、OpenEMS Edge 和 MyEMS 中哪些机制应该成为 HVAC Edge 的架构约束？

本次不选择 Node-RED 版本或第三方 Modbus/MQTT 节点包，也不把流程图当作控制算法证明。本项目已接受的 [`ADR 0012`](../adr/0012-openems-informed-edge-control-plane.md) 仍是 Edge 控制面权威；本记录只裁决 Node-RED 在该权威中的位置。真正引入 Node-RED 或 npm 节点前，仍需另行固定其官方发布和插件提交，审查存储、部署、重启、背压及安全边界。

## 2. 固定上游基线

| 参考实现 | 官方发布 | 固定提交 | 本次用途 |
| --- | --- | --- | --- |
| ThingsBoard IoT Gateway | [`3.8.4`](https://github.com/thingsboard/thingsboard-gateway/releases/tag/3.8.4) | [`a735a2d654a218c007b5db7759ceed44794253f9`](https://github.com/thingsboard/thingsboard-gateway/tree/a735a2d654a218c007b5db7759ceed44794253f9) | Connector/Converter、本地事件存储、MQTT 批量上行与确认 |
| OpenEMS | [`2026.7.0`](https://github.com/OpenEMS/openems/releases/tag/2026.7.0) | [`2e2792d59fc5ba3b99ce3cf98d15081c0a74895e`](https://github.com/OpenEMS/openems/tree/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e) | Edge IPO/Process Image、Modbus 时序、Timedata/重发、无效值语义 |
| MyEMS | [`v6.7.0`](https://github.com/MyEMS/myems/releases/tag/v6.7.0) | [`be6e6ce8ddeac57afb04bddb9621501fb555cab0`](https://github.com/MyEMS/myems/tree/be6e6ce8ddeac57afb04bddb9621501fb555cab0) | Modbus 采集映射、raw/latest 分层、计量归一化 |
| Node-RED | [`4.0.0`](https://github.com/node-red/node-red/releases/tag/4.0.0) | [`cb0c48457952bc6209c4e1729038b12b9100630e`](https://github.com/node-red/node-red/tree/cb0c48457952bc6209c4e1729038b12b9100630e) | revision 部署、Inject 相位和共享 MQTT broker 行为 |

四个提交均通过官方 Git tag 引用复核。ThingsBoard 网关文档是滚动文档，用于解释公开协议；精确运行机制以上述固定源码和测试为准。OpenEMS 与 MyEMS 的更广结论分别已记录在 [`openems-source-review.md`](./openems-source-review.md) 和 [`myems-source-review.md`](./myems-source-review.md)；本文不重新裁决其他产品域。

### 2.1 现场 Node-RED 运行时裁决

现场管理 API 确认实际运行 Node-RED `4.0.0`，核心 MQTT 节点来自 `node-red/mqtt`。本次读取了固定提交中的 [`editor-api/lib/admin/flows.js`](https://github.com/node-red/node-red/blob/cb0c48457952bc6209c4e1729038b12b9100630e/packages/node_modules/%40node-red/editor-api/lib/admin/flows.js)、[`runtime/lib/api/flows.js`](https://github.com/node-red/node-red/blob/cb0c48457952bc6209c4e1729038b12b9100630e/packages/node_modules/%40node-red/runtime/lib/api/flows.js)、[`20-inject.js`](https://github.com/node-red/node-red/blob/cb0c48457952bc6209c4e1729038b12b9100630e/packages/node_modules/%40node-red/nodes/core/common/20-inject.js) 和 [`10-mqtt.js`](https://github.com/node-red/node-red/blob/cb0c48457952bc6209c4e1729038b12b9100630e/packages/node_modules/%40node-red/nodes/core/network/10-mqtt.js)，并核对对应的 [`flows_spec.js`](https://github.com/node-red/node-red/blob/cb0c48457952bc6209c4e1729038b12b9100630e/test/unit/%40node-red/editor-api/lib/admin/flows_spec.js)、[`20-inject_spec.js`](https://github.com/node-red/node-red/blob/cb0c48457952bc6209c4e1729038b12b9100630e/test/nodes/core/common/20-inject_spec.js) 和 [`21-mqtt_spec.js`](https://github.com/node-red/node-red/blob/cb0c48457952bc6209c4e1729038b12b9100630e/test/nodes/core/network/21-mqtt_spec.js)。

- `ADOPT`：使用 `Node-RED-API-Version: v2` 的 `{rev, flows}` 合同；官方 runtime 在 revision 不匹配时返回 409，并用 mutex 串行化部署。
- `ADOPT`：Inject 的 `onceDelay` 决定第一次触发相位，之后才启动固定 repeat interval；因此七个轮询的相位在每次重启后保持错峰。
- `ADOPT`：多个 MQTT output 节点引用同一个 broker config；核心节点通过该共享 broker connection 发布，并按节点/消息 QoS 调用 publish。
- `ADAPT`：全量 flow POST 只用于这次已明确授权的旧路径切除；部署工具必须先保存原 revision 快照，并携带读取到的 revision，不能盲写。

现场 Modbus 节点来自厂商包 `@iotrouter/history` `2.0.0`。公开 npm/GitHub 检索未找到该固定版本的官方运行时源码或测试，因此仍标记为 `UNVERIFIED`，不把它的私有 type 编码、错误恢复或调度语义提升为架构合同。当前继续使用它的理由仅是 EG8200 的 `/dev/ttyAS3` 厂商串口集成这一已存在硬件边界，并且七个读取在 ThingsBoard 中完成了两个实际采集周期验证；它不得承载控制、缓存或重发责任。后续替换该节点前必须选择有固定源码和测试的 Modbus 实现，并在真实 EG8200 串口上证明相同点表行为。

## 3. ThingsBoard：北向协议与离线上行边界

### 3.1 实际读取的官方证据

| 官方证据 | 观察到的行为 |
| --- | --- |
| [IoT Gateway architecture](https://thingsboard.io/docs/iot-gateway/what-is-thingsboard-iot-gateway/) | Connector 负责轮询/订阅和写回，Converter 负责协议数据与 ThingsBoard 数据模型之间的双向转换，Event Storage 在上云前缓冲，ThingsBoard Client 专门批量排空存储。内存存储不抗进程重启，文件存储可以。 |
| [Gateway API overview](https://thingsboard.io/docs/paas/eu/reference/gateway-api/overview/) | 一个网关身份用一条 MQTT 连接代理多个下游 Device；`v1/gateway/connect|disconnect|telemetry|attributes|rpc` 为显式上下行主题，载荷按下游 device name 路由。`connect` 需等 PUBACK 后再发数据。 |
| [Gateway telemetry API](https://thingsboard.io/docs/paas/eu/reference/gateway-api/telemetry/) 和 [Gateway RPC API](https://thingsboard.io/docs/paas/eu/reference/gateway-api/rpc/) | 历史采样可携带源时间；RPC 是网关与指定下游设备间的请求/响应传输面，并不定义 HVAC 控制优先级、租约或物理回读成功。 |
| [`tb_gateway_service.py`](https://github.com/thingsboard/thingsboard-gateway/blob/a735a2d654a218c007b5db7759ceed44794253f9/thingsboard_gateway/gateway/tb_gateway_service.py) | 连接器产生的转换数据先进 Event Storage；发送线程按 Device 聚合 telemetry/attributes，只在 MQTT publish future 成功后调用 `event_pack_processing_done()` 删除本地批次。 |
| [`sqlite_event_storage.py`](https://github.com/thingsboard/thingsboard-gateway/blob/a735a2d654a218c007b5db7759ceed44794253f9/thingsboard_gateway/storage/sqlite/sqlite_event_storage.py) | SQLite 存储将“取批次”和“批次处理完成”分开；固定版本在删除已确认记录之后才允许预取新批次。 |
| [`test_sqlite_duplicate_race_condition.py`](https://github.com/thingsboard/thingsboard-gateway/blob/a735a2d654a218c007b5db7759ceed44794253f9/tests/unit/service/test_sqlite_duplicate_race_condition.py) 和 [`test_storage.py`](https://github.com/thingsboard/thingsboard-gateway/blob/a735a2d654a218c007b5db7759ceed44794253f9/tests/unit/service/test_storage.py) | 回归测试专门保护 SQLite 预取/删除竞态不应重复返回已处理消息，并验证 memory/file/SQLite 的分批顺序；相关修正历史可追溯到官方 [PR #1943](https://github.com/thingsboard/thingsboard-gateway/pull/1943) 和 [PR #2014](https://github.com/thingsboard/thingsboard-gateway/pull/2014)。这些证据不证明跨 Edge、MQTT broker 和 Cloud ingest 的 exactly-once。 |

### 3.2 裁决

- `ADOPT`：将现场协议 Connector、数据 Converter、耐久 Event/Timedata 存储、北向 Client 分成独立责任；一条网关 MQTT TLS 会话复用多个下游设备。
- `ADOPT`：先耐久化、后发送，只在可验证的 publish 确认后推进本地游标；重连后从未确认批次继续。
- `ADAPT`：ThingsBoard 外部载荷按 device name 路由，但 Node-RED 内部与 HVAC Cloud ingest 必须携带 Registry 可验证的 Gateway/Device/Point 稳定身份、mapping revision、source sequence 和 observed-at；不由自由文本名称创建业务身份。
- `ADAPT`：上行按 at-least-once 设计。Telemetry Runtime 继续依据稳定 source position/观测身份幂等去重和排序；MQTT QoS/PUBACK 不被误宣称为端到端 exactly-once。
- `REJECT`：生产上行使用纯内存队列，或在未确认发送成功前删除本地事件。
- `REJECT`：将 Gateway RPC 直接连到任意 Modbus 写节点；RPC 必须转成带身份、租约、期望状态和审计证据的 Edge Command Intent。

## 4. OpenEMS：控制周期、Modbus 调度、Timedata 与质量

### 4.1 实际读取的官方证据

| 官方证据 | 观察到的行为 |
| --- | --- |
| [`edge/architecture.adoc`](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/doc/modules/ROOT/pages/edge/architecture.adoc) | Edge 是软实时 IPO 循环；异步通信只更新 `nextValue`，周期边界才切换为本轮 Controller 共享的不变 Process Image。Scheduler 按顺序执行 Controller，后执行者不能覆盖先前更高优先级约束。文档明确说明火灾/灾害保护等硬实时职责不属于该软件循环。 |
| [`ModbusWorker.java`](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/io.openems.edge.bridge.modbus/src/io/openems/edge/bridge/modbus/api/worker/ModbusWorker.java) | Modbus worker 尽早在 `EXECUTE_WRITE` 之后执行写，尽晚在 `BEFORE_PROCESS_IMAGE` 之前执行读；读任务错误会使相关元素无效并标记组件通信失败，而不是保留无警告的旧值。 |
| [`CycleTasksManagerTest.java`](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/io.openems.edge.bridge.modbus/test/io/openems/edge/bridge/modbus/api/worker/internal/CycleTasksManagerTest.java) | 测试保护写任务在 write event 后立即可用、读任务与较短周期下的状态转移；这是具体的协议/控制交接合同。 |
| [`Value.java`](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/io.openems.edge.common/src/io/openems/edge/common/channel/value/Value.java) | Channel Value 随时可为 `UNDEFINED`，包括启动和设备通信丢失时；值携带本地创建时间。固定源码没有提供等价于 HVAC `GOOD/SUSPECT/BAD + reason` 的完整质量模型。 |
| [`Timedata.java`](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/io.openems.edge.timedata.api/src/io/openems/edge/timedata/api/Timedata.java) | Timedata 是可替换的 latest/history 提供者，对未发送时间段、最后重发游标和指定 Channel 的重发查询提供显式 API。 |
| [`ResendHistoricDataWorker.java`](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/io.openems.edge.controller.api.backend/src/io/openems/edge/controller/api/backend/ResendHistoricDataWorker.java) | 历史重发需显式触发，只选达到配置 persistence priority 的非 write-only Channel，使用成功时间点和前各 5 分钟 buffer，每批最大 5 分钟；失败不推进游标。 |

### 4.2 裁决

- `ADOPT`：所有参与控制的测点在固定周期边界生成不变 Process Image；控制器不直接读取仍在变化的 Node-RED message/context。
- `ADOPT`：Modbus 通信 worker 与 Controller/Scheduler 分离，读/写在周期中有明确交接点，错误使值无效并生成通信质量证据。
- `ADOPT`：耐久 Timedata 维护 latest/history、未发送区间和成功游标；断网恢复用有界分块重发，而不是对当前值无限重试。
- `ADAPT`：OpenEMS `UNDEFINED` 和 `ModbusCommunicationFailed` 映射为 HVAC 的 Presence/Freshness/Quality 正交事实，保留 observed-at/received-at、quality reason 和 mapping revision；不将通信失败压缩成一个布尔“offline”。
- `ADAPT`：Node-RED 可以托管 Connector、Converter、运维观测和非关键编排，但生产 Controller/Scheduler/Arbiter/Process Image 必须是可测试的独立 Edge runtime module，而不是隐含在无序消息流中。
- `REJECT`：用 Node-RED 事件到达顺序代替显式 Scheduler 优先级，或让 Cloud/SCADA 指令绕过本地保护 Controller。
- `REJECT`：用 Node-RED/通用 Edge 软件承担 PLC、变频器、机组本体的硬实时安全联锁。

## 5. MyEMS：Modbus 采集映射与能源归一化边界

### 5.1 实际读取的官方证据

| 官方证据 | 观察到的行为 |
| --- | --- |
| [`myems-modbus-tcp/main.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-modbus-tcp/main.py) | Gateway 监测和各 data-source acquisition 使用独立进程；现场协议采集不与 normalization 或报表进程混合。 |
| [`acquisition.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-modbus-tcp/acquisition.py) | Point 映射显式配置 slave id、function code、offset、register count、format 和 byte swap；读值按 ANALOG/DIGITAL/ENERGY 分类，应用 ratio/offset，分别写入 history 和 latest 表，并更新 data-source last-seen。 |
| [`myems-modbus-tcp/test.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-modbus-tcp/test.py) | 该文件是需要真实 host/port 的手工连通和寄存器读取工具，只打印结果，没有断言。它不能证明轮询时序、断线恢复、写安全或数据不丢。 |
| [`myems-normalization/main.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-normalization/main.py) | 物理 Meter、Offline Meter、Virtual Meter、Virtual Point 和 Data Repair 是独立处理责任。 |
| [`meter.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-normalization/meter.py) | 物理表归一化在清洗缓冲窗口之后运行，从 `is_bad=0` 的累计能源读数按时段计算增量，应用 hourly low/high limit，将规范化结果批量写入 `tbl_meter_hourly`。已处理时间点来自目标表最大时间，与采集 latest 不是同一事实。 |

### 5.2 裁决

- `ADOPT`：协议采集、原始/工程观测、质量清洗、表计增量归一化、汇总/报表是不同的责任和事实，不在一个 Node-RED function node 中连续改写同一 payload。
- `ADOPT`：Modbus Point Mapping 必须是受审查的版本化配置，至少包含 unit/slave/function/register/width/data type/byte-word order/scale/offset/poll policy。
- `ADAPT`：Node-RED Converter 同时保留 protocol raw evidence 和带 mapping revision 的 engineering value；MyEMS 在采集阶段直接应用 ratio/offset 的行为不得导致原始证据不可重演。
- `ADAPT`：累计表归一化必须感知 Counter reset/rollover、Point/Mapping revision、单位和质量，结果写入可重算的 Energy owner；Node-RED 只发布观测，不成为 Cloud 结算/报表权威。
- `ADAPT`：data-source last-seen 是连接/轮询证据，不等于每个 Point 的 Freshness 或 Quality；后两者仍按点、样本时间和接受结果独立计算。
- `REJECT`：复制 MyEMS 的直接数据库写入拓扑到 Node-RED；Edge 不跨 WAN 连接 Cloud 业务表，而是通过受版本化的 MQTT ingest contract 发布。
- `REJECT`：把 MyEMS 手工 `test.py` 当作生产 Modbus 验收门禁，或把没有观测样本的时段默认造成可用的零能耗。

## 6. Node-RED Edge 目标分层

```text
OT Device / PLC / Meter
        |
        v
Node-RED protocol flows
  connection + poll/subscription + decode/encode
        |
        v
versioned Point Mapping / Converter
  raw evidence + engineering value + source time/sequence
        |
        +-----------------------+
        |                       |
        v                       v
independent Edge control core   durable Edge Timedata / Outbox
  Channel nextValue               priority + resend cursor
  Process Image / Cycle                    |
  Scheduler / Arbiter                      v
  leased Intent + readback        ThingsBoard Gateway MQTT API
        |                                  |
        v                                  v
 governed protocol write          Cloud Telemetry Runtime
                                   canonical identity/order/
                                   quality/current/history
```

这个分层的关键不是进程数，而是权威与失败边界：

1. Node-RED flow 可以是协议连接和运维编排的部署容器，但 flow/context 不是控制 Process Image、Command 审计或 Telemetry Current 的权威。
2. 控制内核和协议流程通过有类型的 Channel/Intent/Readback 合同交接；不共享可任意修改的全局 `msg`/context。
3. Timedata/Outbox 先落盘再上行，其压力策略按 safety/control/alarm/audit 证据优先于诊断和普通 telemetry，不依赖 Node-RED 进程内队列存活。
4. Cloud 仍是 Registry、Telemetry acceptance/current/history、Alarm/FDD/Energy 与 Command governance 的权威；Edge 保留断网采集、快速本地控制和安全约束。

## 7. 最小可证明合同

本记录不新增永久 CI 门禁。实施时仅需最小的直接行为证据：

- 同一控制周期的所有 Controller 读取同一 Process Image，周期中途到达的 Modbus 值只在下一轮可见。
- Modbus timeout 将受影响 Channel 标为不可用/质量降级，不以无警告的旧值继续控制。
- 未确认的上行批次在重启/重连后可重发，成功游标不越过失败批次；Cloud 对重复投递幂等。
- Cloud RPC 只能创建有租约的 Intent，高优先级本地约束可限制/拒绝它，最终结果由后续物理回读证明。
- 一个已固定 mapping revision 能确定地解码 byte/word order、scale 和 offset；修改 mapping 不覆写原始观测。
- 累计表增量遇到缺样、坏质量、reset/rollover 或 mapping/unit revision 边界时产生显式不完整/不可用结果，不伪造零能耗。

## 8. 总结裁决

- `ADOPT`：ThingsBoard 的 Connector → Converter → durable storage → batched MQTT client 责任分层和 Gateway MQTT 多设备传输面。
- `ADOPT`：OpenEMS 的 Process Image、确定性 Scheduler、Modbus 周期交接、错误无效化与有游标 Timedata 重发。
- `ADOPT`：MyEMS 将协议采集与质量清洗、物理/虚拟表归一化、修复和汇总分离的责任边界。
- `ADAPT`：Node-RED 是可替换的协议/编排外壳；它不拥有稳定业务身份、控制优先级、耐久命令、计量结算或 Cloud Current。
- `ADAPT`：质量使用 HVAC 已接受的 Presence + Freshness + Quality + reason 模型，吸收 OpenEMS 无效化/通信失败和 MyEMS `is_bad` 过滤证据，不退化为任一上游的单维状态。
- `REJECT`：通用消息流直接闭环控制、内存队列冒充耐久时序库、Gateway RPC 绕过 Edge Arbiter、Edge 直写 Cloud 业务库，以及将缺测时段当成零能耗。

因此，Node-RED 方案只有在“协议流程外壳 + 独立 Edge 控制内核 + 耐久 Timedata/Outbox + Cloud 权威”这条路径上才与当前 HVAC 架构一致。把整个 Edge 控制面收缩为一张 Node-RED flow，与三个固定参考实现的强证据均冲突。

## 9. 现场第一层改造记录（2026-09-14）

本轮只实施最小可工作的协议采集与北向上报层，不宣称已经实现独立控制内核或耐久 Timedata/Outbox：

- 将 Node-RED 从 225 个对象收敛为 27 个对象，只保留 `Edge·采集与上报` 和 `ET1010·状态` 两个流程。
- 删除旧 OIDCS HTTP/MQTT 上报、重复温度/电表/水流轮询、空白流程、45 个 Debug 节点和 32 个 ET1010 直接线圈写入节点。
- 一个 ThingsBoard broker 配置承载两个 MQTT output 节点；所有 telemetry 使用 `v1/gateway/telemetry`、QoS 1，不再在每次采样前重复发送 gateway connect。
- 温度 UID20、水流 UID10、四块电表 UID5/2/3/4 和 ET1010 UID31 使用 1/8/15/22/29/36/45 秒相位错峰；现场只保留七个定时轮询所有者。
- 温度 t1-t20 统一应用 `/10`；四块电表分别发布为 `ammeter-unit1` 至 `ammeter-unit4`；ET1010 只发布十六路物理状态。
- 部署 revision 从 `6bbd5edc906cd0fbb92ea7ff9d22dab9bc4f81b28740dcdc9ce7af97fd9771d4` 变为 `9341275171a472563276dce8fae7a47c37bb29af7458288d45ff1d37fc0e7c59`。

部署后从 ThingsBoard `ts_kv_latest` 只读复核：温度、水流和 ET1010 的时间戳连续跨越两个 60 秒周期；四块电表均完成第二个 300 秒周期的错峰更新。温度值已从寄存器整数恢复为工程值，例如 t1 为 13.9、t2 为 14.5。水流、ET1010 各状态和部分电表功率的零值是现场设备本次实际返回，不在 Edge 中改写或伪造。

尚未实施的下一层能力仍是本记录第 6、7 节定义的独立 Process Image/控制内核、完整 Timedata、TLS 和 Cloud command governance；当前 Node-RED 流程不得被描述为这些合同已经完成。耐久 MQTT Outbox 已在下一节单独完成。

## 10. 单机耐久 Outbox 实施与验收（2026-09-15）

### 10.1 现场约束与依赖裁决

现场管理 API 和诊断节点确认 EG8200 只有一台设备，运行 Linux `arm` 32 位、Node.js `20.12.2`、Node-RED `4.0.0`，物理内存约 512 MB；系统后台不可登录，部署入口只有 Node-RED 管理 API。因此实现不能依赖增加主机、Docker、外部数据库或网关 shell。

候选依赖固定为 [`node-red-node-sqlite@2.0.1`](https://www.npmjs.com/package/node-red-node-sqlite/v/2.0.1) 及其原生依赖 [`sqlite3@6.0.1`](https://github.com/TryGhost/node-sqlite3/tree/a7badce)。前者官方安装说明明确指出 v2 可能缺少目标平台预编译产物并需要本机构建；后者 v6.0.1 的官方发布只提供 Linux `arm64`/`x64`，不提供 32 位 `arm`，README 还将预编译支持基线列为 Node.js 20.17.0 及以上。现场既不具备编译链，也不能稳定下载构建依赖，所以裁决如下：

- `REJECT`：在这台 EG8200 上安装 `node-red-node-sqlite@2.0.1`。它不是坏方案，但与已验证硬件/运行时约束不兼容。
- `REJECT`：第三方文件队列包和 Node-RED 核心 File/File In 流程。候选第三方包缺少持续维护证据；核心 File In 在现场厂商运行时探针中报告完成却没有输出记录，不能作为恢复合同。
- `ADAPT`：保留 ThingsBoard Gateway 的“先耐久化、成功确认后删除”机制，但用 Node.js 核心 `fs` 实现本机 append-only journal，不引入原生依赖或后台服务。

### 10.2 已实施合同

本仓库新增 `@hvac/edge-outbox` `1.0.2`，通过 Node-RED 官方节点安装 API上传到 `/root/.node-red/node_modules/@hvac/edge-outbox`。现场正常重启后，诊断 API 已确认运行版本为 `1.0.2` 且没有 pending version；随后通过带 revision 的 flow deploy 启用与该版本匹配的串行化流程。

实现只承担一个明确责任：在 MQTT 发布前将完整 ThingsBoard Gateway telemetry 事件追加到 `RED.settings.userDir/hvac-edge-outbox/outbox.jsonl` 并 `fsync`，收到 QoS 1 PUBACK 后追加 ACK 并 `fsync`。启动和 MQTT 重连时从最早未确认事件恢复；同一时刻只允许一条事件在途，当前事件 ACK 后才发送下一条，因此成功位置不会越过失败事件。累计 256 个 ACK 后用同目录临时文件、文件同步和原子 rename 压缩日志。队列达到 128 MB 时显式失败，不静默丢弃旧 telemetry。启动发现断电残留的末行时先压缩为完整未确认事件再接受新写入；Node-RED 热部署等待当前日志操作完成；入队和 ACK 都实时输出 pending 状态。

Node-RED `4.0.0` 固定源码中的 MQTT output 只在 publish callback 成功后调用 `done()`，Complete 节点只在无错误完成时发出消息。因此当前流程将 `Complete(MQTT)` 作为 PUBACK 后的 ACK 入口，不用 Delay、连接状态或“已调用 publish”冒充确认。七个采集 formatter 统一进入一个 Outbox，再串行进入唯一 MQTT output；ET1010 通过 Link 节点跨流程进入同一所有者。所有指向 `tb.oidcs.com`/`emqx.oidcs.com` 的配置和路径均已移除。

### 10.3 最小行为证据

本地 `node:test` 保护五个当前合同：未确认事件跨 store 重建保留、ACK 事件重启后不再重放、断电造成的末行残缺不遮蔽此前完整事件或后续写入、压缩后只保留未确认事件、后项只有在前项 ACK 后才发布。测试不覆盖 Node.js `fs` 自身或无业务意义的 getter。

现场运行版本 1.0.2 的受控验收临时将同一个 MQTT broker 配置从 1883 改为不可达的 1884，并使用 `try/finally` 保证恢复：

1. MQTT 状态转为 yellow/ring `connecting`，Outbox `pending` 从 0 变为 1，证明事件在 broker 不可达时已先落盘。
2. 将端口恢复为 1883 并全量重载流程，Outbox 从 journal 启动恢复；`pending` 回到 0，`lastReplayAt` 和 `lastAckAt` 均推进，MQTT 恢复 green/dot `connected`。
3. ThingsBoard PostgreSQL 在本次 1.0.2 断网测试窗口内保留了 `temperature|1789438513125`、`temperature|1789438519133` 和 `waterflow|1789438526124` 三个源时间样本；其中待发温度事件在恢复后落库，七类设备最新 `ts_kv` 随后继续推进。

现场最终已运行 flow revision 为 `d53701266c143242187989ccca5571c1640ec102c8aa5b78a192d080c1650271`，对象数 39、定时/启动 Inject 8 个、Modbus read 7 个、MQTT output 1 个。部署脚本会先核对运行模块版本，版本不符时明确拒绝部署，避免把新流程接到旧节点实现。该证据证明的是进程重启、流程重载和 MQTT 断连下的 at-least-once 耐久补发，不把它夸大为磁盘损坏容错、整机断电认证或端到端 exactly-once。

本轮没有伪造尚不存在的 Registry Gateway/Device/Point ID、mapping revision 或 source sequence，也没有把 formatter 后的工程值宣称为可重演的协议 raw evidence。当前自由文本 ThingsBoard device name 和 `device:timestamp` 只属于已运行北向协议的临时定位键；稳定身份、原始观测、Cloud 幂等和完整 Timedata 仍是进入控制/结算层之前必须完成的边界。
