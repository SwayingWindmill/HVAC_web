# 集成与处理架构规划

状态：PROPOSED / SOURCE-ALIGNED  
范围：Backend ingestion、事件处理、能源 processing、未来 Edge seam

## 1. 集成分层

~~~text
Protocol Adapter
    ↓
Transport Envelope
    ↓
Identity / Mapping
    ↓
Acceptance + Quality
    ↓
Raw Telemetry
    ↓
Energy Processing / Automation
    ↓
Query Read Model / UI
~~~

协议 adapter 不负责能源计算；规则处理不负责替代 Edge 控制；UI 不直接订阅设备协议。

## 2. 当前 Backend 接入

当前阶段使用 MQTT 和 simulator fixture 完成纵向切片：

- MQTT 只作为传输；
- envelope 携带 tenant/site/device/point、event time、ingest time、message type、event id 和 schema version；
- ingestion 负责 mapping、acceptance、quality 和幂等；
- telemetry module 负责 raw/current/history；
- energy processing module 负责 cleaning、normalization、aggregation；
- query module 负责给 UI 的 current、series、comparison、quality、report。

ThingsBoard 的 Transport/Integration/Converter 证明了“协议接入和统一内部消息”应分开；MyEMS 的 Modbus TCP 模块证明了现场 acquisition 可以独立于中心能源处理；当前不因这一结论立即部署真实 Modbus 服务。

## 3. 事件可靠性

事件处理至少需要：

- event_id：来源事件的唯一标识；
- consumer_name：消费者身份；
- business_revision：业务事实版本；
- event time 与 ingest time；
- source mapping revision；
- processing run / output revision；
- retryable failure 与 terminal failure 的区分。

跨 module 的重要状态变更使用 PostgreSQL Outbox；消费者按 event_id + consumer_name 幂等。ClickHouse 写入成功但 projection 未推进时，必须通过 reconcile 重新确认，而不是重复创建业务事实。

## 4. 处理链的触发方式

| 处理 | 触发 | 失败恢复 | 结果 |
| --- | --- | --- | --- |
| Current projection | ingestion event | 可按 raw/event 重建 | current revision |
| Cleaning | ingestion watermark 或人工重跑 | run lease + retry | cleaning decision |
| Normalization | cleaning watermark、binding/tariff revision | 输入窗口重算 | normalized fact |
| Aggregation | normalized watermark | 固定窗口重算 | aggregate fact |
| Billing/Carbon | period close 或 revision change | 追加 revision，不覆盖锁定结果 | settlement/report snapshot |
| Alarm/FDD | realtime event 或 aggregate completion | 独立状态机和 dedup | finding/alarm/work order |

Energy processing 允许异步和批量，但必须有 freshness、watermark、quality summary 和 run status；不能用一个“消息已消费”代替能源结果已生成。

## 5. 未来 Edge seam

当前不开发生产 Edge Host，但 Backend 现在就冻结以下消息类别：

~~~text
telemetry
attribute/config
alarm/event
command_intent
reported_state
readback/evidence
manifest/sync
~~~

未来 command 链：

~~~text
UI / Rule / Optimization
    ↓
Command Governance
    ↓
Approval / IAM / Audit / Lease
    ↓
Edge Intent
    ↓
Local Safety / Controller / Scheduler / Arbiter
    ↓
Effective Value + Readback Evidence
    ↓
Cloud Verification
~~~

OpenEMS 的 Channel/Process Image/Cycle 只约束未来 Edge 的本地控制语义；不能被实现成 Backend 的同步请求链。ThingsBoard 的 Edge 本地存储和可选择同步可作为未来 store-and-forward 参考；不能被简化为 MQTT 重试队列。

## 6. 对当前项目的明确修改

- 将 mqtt-telemetry-adapter 定位为 adapter/ingress，不再让它承担能源语义；
- 将 telemetry-runtime-service 与 energy processing 的 ownership 分开；
- 将 simulator 明确标为 fixture，并让它只替换 protocol/physical seam；
- 为 raw → normalized → aggregate 增加 watermark/run/revision；
- 为 command 增加 accepted/delivered/applied/constrained/readback/expired/unknown 状态；
- 删除任何 UI 直接依赖 MQTT topic 或内部数据库的路径；
- Edge 相关实现延期，但保留 manifest、intent、reported-state、evidence 的 schema；
- 不引入 ThingsBoard runtime、Kafka/Redpanda 或真实协议平台作为本阶段前置。

## 7. 验收条件

- 重复消息不会生成重复 normalized 或 aggregate 事实；
- 乱序事件进入历史，但不回退 current；
- 一次处理失败可以从 watermark 重试；
- UI 可以显示结果的 freshness、quality 和 revision；
- simulator 替换为真实 adapter 时，能源处理和 UI 查询不需要重写；
- Cloud 无法伪造“设备已经执行”的状态。

