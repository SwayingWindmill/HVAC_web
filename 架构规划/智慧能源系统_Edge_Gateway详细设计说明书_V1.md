# 智慧能源系统 Edge Gateway 详细设计说明书

> 版本：V1.0  
> 技术基线：Go + SQLite/Local Store + MQTT + Protocol Adapter  
> 云端基线：PostgreSQL + ClickHouse + Redis + MQTT  
> 第一阶段目标：Meter + Modbus 跑通完整现场数据链路

---

# 1. 文档目标

Edge Gateway 是现场设备与云平台之间的标准边界。

数据链路：

```text
Field Device
  ↓
Protocol Adapter
  ↓
Poll Scheduler
  ↓
Decode / Normalize
  ↓
Quality Engine
  ↓
Local Latest
  ↓
Store & Forward
  ↓
MQTT
  ↓
Cloud
```

控制链路：

```text
Cloud Command
  ↓
MQTT
  ↓
Edge Command Handler
  ↓
Local Safety Validation
  ↓
Protocol Write
  ↓
Device ACK
  ↓
Readback
  ↓
Verification
  ↓
Command Reply
```

Edge 的核心职责不是“读取寄存器”，而是把不同厂家、不同协议的现场设备统一转换为平台标准语义，并保证数据可靠、可补传、可诊断、可安全控制。

---

# 2. Edge 职责边界

Edge 必须负责：

```text
设备连接
协议适配
轮询调度
数据解码
点位标准化
时间戳
数据质量
本地缓存
断网补传
MQTT会话
设备状态
本地安全校验
控制回读
配置版本
OTA
健康监控
现场诊断
```

Edge 不负责：

```text
复杂能源指标
费用结算
跨站点分析
长期历史查询
AI预测
优化调度
用户权限
租户业务逻辑
```

这些属于 Cloud。

---

# 3. 核心设计原则

1. **Vendor Protocol 到 Edge 为止。** Cloud 不知道 Modbus Register、BACnet Object、OPC UA Node。
2. **Standard Point 是唯一业务语义。** Vendor Point 必须经过 Device Template 映射。
3. **采集优先于分析。** Edge 只做必要转换、质量、缓存和安全控制。
4. **断网不丢数据。** Cloud 不可用时继续采集并本地持久化。
5. **实时优先于补传。** Replay 必须限速，不得拖慢实时数据。
6. **控制默认拒绝。** Cloud 校验通过后，Edge 仍需再次做本地安全校验。
7. **配置必须版本化。** Edge 运行配置不可依赖“latest”。
8. **Edge 本地状态优先。** LOCAL / MAINTENANCE / LOCKED 必须优先于远程控制。

---

# 4. 推荐技术实现

Edge Gateway 推荐使用 Go：

```text
单二进制
跨平台
低内存
适合Linux ARM/x86
并发模型清晰
协议库丰富
适合长期运行服务
```

第一阶段本地持久化使用 SQLite。

后期高吞吐场景可再评估 Pebble / Badger / RocksDB，但第一阶段不引入。

---

# 5. 总体模块架构

```text
edge-gateway
│
├── Runtime
├── Device Manager
├── Connection Manager
├── Protocol Adapters
├── Poll Scheduler
├── Device Template Engine
├── Decode Engine
├── Point Normalizer
├── Quality Engine
├── Latest Cache
├── Local Store
├── Store & Forward
├── MQTT Client
├── Command Handler
├── Safety Validator
├── Readback Verifier
├── Config Manager
├── OTA Manager
├── Health Monitor
├── Metrics
└── Diagnostics
```

推荐工程目录：

```text
edge/
├── cmd/gateway/
├── internal/
│   ├── runtime/
│   ├── config/
│   ├── device/
│   ├── template/
│   ├── connection/
│   ├── protocol/
│   │   ├── modbus/
│   │   ├── bacnet/
│   │   └── opcua/
│   ├── polling/
│   ├── decode/
│   ├── normalize/
│   ├── quality/
│   ├── storage/
│   ├── replay/
│   ├── mqtt/
│   ├── command/
│   ├── safety/
│   ├── ota/
│   └── observability/
├── configs/
├── migrations/
└── tests/
```

---

# 6. Device Manager

维护当前网关管理的设备运行实例。

运行对象建议包含：

```text
device_id
device_code
device_type
product_id
gateway_id
protocol
connection_profile
control_mode
enabled
template_version
runtime_state
```

运行状态：

```text
INIT
CONNECTING
ONLINE
DEGRADED
OFFLINE
FAULT
DISABLED
```

---

# 7. Connection Manager

连接必须共享，禁止每个 Point 建立独立连接。

典型：

```text
Modbus TCP:
一个 TCP Connection
→ 多 Device / Unit ID

Modbus RTU:
一个 Serial Port
→ 多 Slave
```

职责：

```text
连接创建
重连
超时
连接健康
连接共享
错误分类
并发限制
```

---

# 8. Protocol Adapter

所有协议统一抽象为：

```text
Open()
Close()
Health()
Read(request)
BatchRead(requests)
Write(request)
```

Adapter 只处理 Vendor Protocol，不处理 Standard Point、业务单位、Alarm 或 Cloud 逻辑。

协议优先级：

```text
P0 Modbus TCP
P0 Modbus RTU
P1 BACnet/IP
P1 OPC UA
P2 DL/T645
P2 IEC104
P2 IEC61850
```

第一阶段只要求 Meter + Modbus TCP 正式跑通。

---

# 9. Modbus Adapter

至少支持：

```text
FC01 Read Coils
FC02 Read Discrete Inputs
FC03 Read Holding Registers
FC04 Read Input Registers
FC05 Write Single Coil
FC06 Write Single Register
FC15 Write Multiple Coils
FC16 Write Multiple Registers
```

控制写入只允许 Device Template 明确声明的地址和 Function Code。

示例连接配置：

```yaml
protocol: MODBUS_TCP
host: 192.168.1.20
port: 502
unit_id: 1
timeout_ms: 1000
retry_count: 2
byte_order: BIG_ENDIAN
word_order: NORMAL
```

---

# 10. Poll Scheduler

Poll Scheduler 是 Edge 核心组件。

禁止：

```text
一个Point一个goroutine
```

推荐：

```text
Poll Group
→ Batch Read Plan
→ Shared Connection
```

Poll Task：

```text
device_id
poll_group
interval
priority
deadline
connection_key
read_plan
```

优先级：

```text
CRITICAL
HIGH
NORMAL
LOW
```

控制命令优先于普通采集。

---

# 11. Poll Group

Meter 第一阶段：

```text
realtime: 5s
active_power
reactive_power
voltage_a/b/c
current_a/b/c
frequency
power_factor

counter: 60s
energy_import
energy_export
```

不同设备可以定义自己的 Poll Group。

---

# 12. Read Plan Compiler

Device Template 加载后，应预编译连续寄存器读取计划：

```text
Point Mapping
→ Register Range
→ Batch Read Plan
```

例如连续地址：

```text
40001
40002
40003
40004
```

应一次读取，而不是四次独立请求。

编译时考虑：

```text
Function Code
最大寄存器长度
厂家限制
数据类型
禁止跨区读取
地址间隙
```

---

# 13. 调度抖动与 Overrun

大量设备不能同一时刻轮询。

建议给 5 秒任务加入约 ±500ms Jitter。

如果一次读取耗时超过周期：

```text
Skip / Coalesce
```

不能无限堆积 Poll Task。

监控：

```text
poll_overrun_total
poll_lag_seconds
```

---

# 14. Timeout / Retry

推荐初始：

```text
Timeout 500~2000ms
Retry 1~2
```

现场可配置。

禁止无限重试，否则一个故障设备可能拖垮整个总线。

---

# 15. Device Template Engine

模板包含：

```text
Metadata
Connection
Poll Groups
Points
Enum Mapping
Bit Mapping
Commands
Readback
```

生命周期：

```text
Load
→ Validate
→ Compile
→ Activate
```

每个运行设备必须绑定具体版本：

```text
METER_X1@1.0.0
PCS_X1@1.2.0
```

---

# 16. Point Mapping 与 Decode

标准流程：

```text
Raw Bytes
→ Endian
→ Word Order
→ Data Type
→ Raw Number
→ Multiplier
→ Offset
→ Unit Conversion
→ Range Validation
→ Standard Value
```

至少支持：

```text
INT16 / UINT16
INT32 / UINT32
INT64 / UINT64
FLOAT32 / FLOAT64
BOOL
BITSET
ENUM
STRING
```

字节序必须支持 ABCD / BADC / CDAB / DCBA 等组合。

---

# 17. 单位转换

标准公式：

```text
standard_value
=
(raw_value × multiplier + offset)
× unit_factor
```

例如：

```text
12640 × 0.01 = 126.4 kW
```

Cloud 看到：

```text
active_power = 126.4 kW
```

而不是：

```text
40001 = 12640
```

---

# 18. Physical Range

Device Template 的 min/max 表示物理有效范围，不是告警阈值。

例如：

```text
voltage_a: 0~500V
```

超出后：

```text
quality = OUT_OF_RANGE
```

不自动截断。

---

# 19. Enum / Bitset

Vendor：

```text
0 = stopped
1 = running
2 = fault
```

转换为：

```text
STOPPED
RUNNING
FAULT
```

状态字 Bitset 也应拆为标准 Point：

```text
fault_status
remote_mode
alarm_status
```

---

# 20. Dimensions

高数量通道使用：

```text
channel
rack
pack
cell
string
mppt
```

作为 Dimension。

禁止制造：

```text
cell_voltage_001
cell_voltage_002
...
```

---

# 21. Quality Engine

统一质量码：

```text
0  GOOD
10 BAD
20 TIMEOUT
30 OFFLINE
40 MISSING
50 STALE
60 OUT_OF_RANGE
70 PARSE_ERROR
80 ESTIMATED
90 MANUAL
```

判断顺序：

```text
Connection
→ Read Result
→ Decode
→ Range
→ Freshness
→ Final Quality
```

---

# 22. Stale

如果最新成功采集时间超过：

```text
2~3 × sampling_interval
```

则标记：

```text
STALE
```

不能继续把旧值显示成 GOOD。

---

# 23. 时间戳

区分：

```text
device_time
gateway_time
cloud_time
```

如果设备 RTC 可信，则 event_time 优先使用 device_time；否则使用 gateway_time。

Gateway 必须有 NTP，持续监控 clock_offset。

---

# 24. Local Latest Cache

本地维护：

```text
point_id
value
quality
event_time
```

用途：

```text
本地诊断
控制回读
变化判断
状态判断
```

不作为长期历史数据库。

---

# 25. Local Store

第一阶段使用 SQLite。

建议表：

```text
telemetry_queue
event_queue
command_log
config_version
device_runtime
```

`telemetry_queue`：

```text
id
message_id
topic
payload
event_time
created_at
priority
state
retry_count
next_retry_at
```

状态：

```text
PENDING
SENDING
ACKED
FAILED
```

---

# 26. 为什么按 Batch 存 Queue

MQTT 本身就是批量消息，因此本地 Store & Forward 优先保存标准化 Batch，而不是每个 Point 单独一行。

优点：

```text
降低SQLite行数
降低IO
Replay无需重新解析协议
```

---

# 27. 本地缓存能力

第一阶段：

```text
>= 24h
```

正式生产目标：

```text
3~7 days
```

容量根据：

```text
values/s
payload size
disk capacity
compression
```

实际计算。

---

# 28. Disk Guard

监控：

```text
disk_used_percent
queue_bytes
queue_age
```

80%：告警。

90%+：进入保护策略：

```text
优先保留关键Telemetry
保留Control Audit
限制低价值Replay
限制低优先级日志
```

不能让磁盘填满导致 Edge 崩溃。

---

# 29. Store & Forward

Cloud/MQTT 不可用：

```text
Telemetry
→ Local Store
```

恢复：

```text
Realtime Stream
+
Replay Worker
```

优先级：

```text
P0 Realtime
P0 Critical Event
P0 Command Reply
P1 State
P3 Replay
```

Replay 必须限速。

---

# 30. Replay

Replay 消息明确：

```json
{
  "replay": true
}
```

同一 Device 按 event_time 补传；不同设备可并行。

Cloud 必须识别 Replay，且旧 Replay 不得覆盖 Redis Latest 的较新实时值。

---

# 31. MQTT Client

正常：

```text
1 Gateway
→ 1 MQTT Session
```

不是：

```text
1 Device
→ 1 MQTT Session
```

Topic：

```text
energy/v1/{tenant}/{site}/{gateway}/telemetry
energy/v1/{tenant}/{site}/{gateway}/state
energy/v1/{tenant}/{site}/{gateway}/heartbeat
energy/v1/{tenant}/{site}/{gateway}/event
energy/v1/{tenant}/{site}/{gateway}/command
energy/v1/{tenant}/{site}/{gateway}/command/reply
```

第一阶段 QoS 建议全部使用 1。

---

# 32. MQTT Reconnect

使用：

```text
Exponential Backoff
+
Jitter
```

避免大量 Gateway 同时重连形成 Reconnect Storm。

---

# 33. Heartbeat

建议 30 秒。

字段：

```text
uptime
cpu
memory
disk
temperature
software_version
config_version
connected_devices
offline_devices
pending_messages
```

Cloud 根据 heartbeat timeout 判断 ONLINE / DEGRADED / OFFLINE。

---

# 34. Command Handler

处理链：

```text
MQTT Command
→ Decode
→ Validate
→ Dedup
→ Expiry
→ Target Resolve
→ Safety
→ Write
→ Readback
→ Reply
```

---

# 35. Command Idempotency

以 command_id 为唯一键。

Edge 保存最近已处理 Command。

重复 Command：

```text
不重复写设备
```

直接返回已有状态/结果。

---

# 36. Command Expiry

如果：

```text
now > expireAt
```

直接返回：

```text
EXPIRED
```

绝不执行过期命令。

---

# 37. Edge Safety Validation

Cloud 已经校验一次，Edge 仍必须再次检查：

```text
Device Online
Control Mode
Writable
Capability
Static Range
Dynamic Range
SOC
Temperature
Interlock
Rate Limit
Maintenance
Local Override
```

原则：

```text
Default Deny
```

---

# 38. Control Mode

标准：

```text
LOCAL
REMOTE_MANUAL
REMOTE_AUTO
MAINTENANCE
LOCKED
```

例如算法自动控制只有 REMOTE_AUTO 才允许。

现场切到 LOCAL 后，Cloud Remote Command 必须被 Edge 拒绝。

---

# 39. Kill Switch

软件层支持：

```text
control_disabled
```

作用范围：

```text
gateway
device
asset
```

但软件 Kill Switch 不替代硬件急停和保护回路。

---

# 40. Write / Readback

完整控制：

```text
Command
→ Validate
→ Encode
→ Protocol Write
→ Device ACK
→ Readback
→ Verify
```

例如：

```text
set_power = 300kW
```

写后读取 target_power，验证：

```text
abs(actual - target) <= tolerance
```

Protocol Write ACK 不等于 VERIFIED。

---

# 41. Command Reply 状态

统一：

```text
RECEIVED
VALIDATING
REJECTED
WRITING
DEVICE_ACK
EXECUTED
VERIFIED
FAILED
TIMEOUT
EXPIRED
```

Edge 本地保存 command audit，Cloud 保存主审计记录。

---

# 42. Config Manager

Edge 配置必须版本化，包括：

```text
Gateway Config
Device List
Connection Profile
Device Template
Point Mapping
Poll Group
Control Policy
MQTT
Logging
```

每次发布：

```text
config_version
checksum
released_at
```

Cloud 维护：

```text
desired_config_version
```

Edge 上报：

```text
reported_config_version
```

---

# 43. Config 发布流程

```text
Cloud Build Config
→ Validate
→ Release
→ Notify Gateway
→ Gateway Download
→ Checksum
→ Local Validate
→ Stage
→ Activate
→ Report Version
```

目录建议：

```text
current/
candidate/
previous/
```

配置激活失败时自动恢复 previous。

---

# 44. Config Dry Run

激活前验证：

```text
Schema
Reference
Template
Connection
Poll Plan
Command Mapping
```

Dry Run 不执行真实控制。

---

# 45. OTA Manager

Gateway OTA：

```text
Check Version
→ Download
→ Checksum
→ Signature Verify
→ Stage
→ Switch
→ Start
→ Health Check
→ Commit
```

失败自动 Rollback。

批量发布：

```text
Canary
→ 1%
→ 10%
→ 30%
→ 100%
```

升级包必须签名，Edge 内置可信公钥。

---

# 46. Local Diagnostics

建议提供仅管理网可访问的轻量本地诊断页面：

```text
Gateway Overview
Device List
Point Latest
Protocol Test
Poll Result
MQTT State
Queue State
Network
NTP
Logs
Config Version
```

生产写操作默认关闭。

---

# 47. Health Model

Gateway：

```text
HEALTHY
DEGRADED
UNHEALTHY
```

检查：

```text
CPU
Memory
Disk
NTP
MQTT
Local DB
Device Connectivity
Queue Age
```

Device Health 不能只看 TCP 是否连通，还要综合：

```text
latest telemetry
quality
poll success rate
protocol errors
```

---

# 48. Edge Metrics

基础：

```text
gateway_uptime_seconds
cpu_usage_percent
memory_usage_percent
disk_usage_percent
```

协议：

```text
protocol_requests_total
protocol_errors_total
protocol_latency_seconds
poll_lag_seconds
poll_overrun_total
```

Telemetry：

```text
telemetry_values_total
telemetry_batches_total
telemetry_queue_depth
telemetry_queue_oldest_seconds
replay_values_total
```

MQTT：

```text
mqtt_connected
mqtt_publish_total
mqtt_publish_errors_total
mqtt_reconnect_total
```

Command：

```text
command_received_total
command_success_total
command_failed_total
command_verify_failed_total
```

Metrics Label 禁止使用 point_id 等高基数标签。

---

# 49. Structured Log

统一字段：

```text
timestamp
level
service
gateway_id
device_id
protocol
event
error_code
message_id
command_id
trace_id
```

禁止每个正常 Point 打 INFO 日志。

---

# 50. Security

网络边界：

```text
Field Network
→ Edge Gateway
→ Cloud Network
```

Cloud 不直接连接 Modbus 设备。

每台 Gateway 必须拥有唯一身份和证书，推荐 MQTT mTLS。

证书生命周期：

```text
Provision
Rotate
Expire
Revoke
```

MQTT ACL：

```text
只能发布自己的Topic
只能订阅自己的Command Topic
```

---

# 51. 本地 Secret 与权限

Private Key / MQTT Credential / API Token：

```text
不能硬编码
不能提交Git
必须限制文件权限
优先加密存储
```

Gateway Service 尽量 non-root；串口通过 group permission 授权。

---

# 52. 双网口建议

关键生产网关建议：

```text
eth0 → OT Device Network
eth1 → IT / Cloud Network
```

降低现场网络横向风险。

---

# 53. Graceful Shutdown

关闭顺序：

```text
Stop New Poll
→ Finish Critical Command
→ Flush Local State
→ Close MQTT
→ Close Connections
→ Close DB
```

重启恢复：

```text
Load Config
→ Open Store
→ Recover Queue
→ Recover Command State
→ Connect MQTT
→ Start Poll
→ Start Replay
```

---

# 54. Resource Guard

限制：

```text
goroutine
queue
connection
batch
log size
disk
```

CPU 高时优先降级：

```text
低优先级Poll
Replay
```

不能影响：

```text
Critical Poll
Command
Heartbeat
```

---

# 55. Simulator 与测试

第一阶段必须提供 Modbus TCP Meter Simulator。

测试：

```text
正常采集
Timeout
Offline
Illegal Function
Illegal Address
Connection Reset
Slow Device
Counter Reset
Duplicate Message
Delayed Message
Out-of-order
Bad Quality
```

---

# 56. Golden Template Test

每个正式 Device Product Template 必须有：

```text
Raw Data
→ Expected Standard Point
```

用于自动验证：

```text
Endian
Data Type
Multiplier
Unit Conversion
Enum
Bitset
Quality
```

---

# 57. Store & Forward Test

至少验证：

```text
Cloud Offline 1h
Edge继续采集
Queue持续增长
Cloud恢复
Realtime优先
Replay补齐
```

正式生产 Gate 再扩展到 24h+。

---

# 58. Config / OTA Test

必须覆盖：

```text
错误Template
错误Connection
Activation Failure
Config Rollback
OTA Startup Failure
OTA Rollback
```

---

# 59. Command Safety Test

至少：

```text
Offline Device
LOCAL Mode
Out-of-range
Expired
Duplicate
Interlock
Timeout
Readback mismatch
```

均必须安全失败。

---

# 60. 性能测试

单 Gateway 分档：

```text
100 Devices
500 Devices
1000 Devices
```

以及：

```text
1k values/s
5k values/s
10k values/s
```

具体能力以硬件等级和协议复杂度压测决定。

---

# 61. Gateway Hardware 分档

示例：

```text
Small
2 Core / 2GB / 16GB

Standard
4 Core / 4GB / 32~64GB

High
8 Core / 8GB / 128GB+
```

实际依据：

```text
Device Count
values/s
协议复杂度
缓存天数
```

---

# 62. Cloud / Edge 配置关系

PostgreSQL 是配置 Source of Truth。

Edge 是本地副本。

配置发布：

```text
Create Device
→ Create Point
→ Release Config Version
→ Gateway Sync
→ Activate
→ Telemetry Allowed
```

未知 Device / Point 不允许自动创建。

---

# 63. Gateway Provisioning

```text
Factory ID
→ Registration
→ Tenant/Site Binding
→ Certificate Provision
→ Initial Config
→ Activated
```

Gateway 损坏更换后，历史查询仍以 device_id / point_id 为核心，不依赖旧 Gateway 才能读取历史。

---

# 64. Commissioning

正式投运：

```text
Connection Test
→ Template Test
→ Point Read
→ Unit Verify
→ Quality Verify
→ Cloud Verify
→ History Verify
→ Accept
```

Meter 第一阶段重点核对：

```text
active_power
voltage_a/b/c
current_a/b/c
frequency
power_factor
energy_import
```

与现场表计显示逐项比对。

---

# 65. CT/PT Ratio

如果表计读数未包含 CT/PT 倍率，必须由模板或资产配置明确：

```text
ct_ratio
pt_ratio
```

不能让现场人员凭经验临时乘倍数。

---

# 66. 数据一致性检查

Commissioning 时：

```text
Edge Standard Value
Cloud Redis Latest
ClickHouse Raw
React UI
```

四处必须一致。

---

# 67. 故障定位顺序

页面无数据：

```text
Device
→ Protocol
→ Poll
→ Decode
→ Normalize
→ Local Queue
→ MQTT
→ Cloud
```

每层必须有独立诊断信息。

---

# 68. 第一阶段交付范围

P0：

```text
Go Edge Runtime
Modbus TCP
Modbus RTU
Device Template
Poll Scheduler
Read Plan
Decode
Normalize
Quality
Latest
SQLite Queue
Store & Forward
MQTT
Heartbeat
Config Version
Diagnostics
```

P1：

```text
Command Handler
Readback
Local Safety
OTA
```

第一阶段不要求：

```text
BACnet
OPC UA
IEC104
IEC61850
Active-Standby Edge
Complex Local Rule
Device Firmware OTA
```

---

# 69. 里程碑

```text
E1 Edge Runtime Ready
Config / Storage / MQTT / Health

E2 Meter Connected
Modbus / Template / Poll / Normalize / Quality

E3 Offline Capable
Local Store / Replay / Realtime Priority

E4 Manageable
Config Version / Diagnostics / Metrics / Logs

E5 Control Ready
Command / Safety / Write / Readback / Verify
```

---

# 70. Device Product DoD

一个 Device Product 接入完成必须具备：

```text
Template
Golden Data
Protocol Test
Point Mapping Test
Commissioning Record
Performance Result
Failure Test
Documentation
```

---

# 71. Edge Production Gate

正式上线至少满足：

```text
72h稳定运行
无Crash Loop

设备采集成功率达标
断网24h不丢数据
恢复后Replay完成
Realtime不被Replay拖慢
Disk Guard通过
MQTT重连通过
配置回滚通过
NTP正常
所有Standard Point与现场核对
```

控制设备额外：

```text
LOCAL Mode Reject
Expired Reject
Range Reject
Interlock Reject
Duplicate Command Test
Readback Verify
```

---

# 72. 最终 Edge 架构

```text
                     Cloud
            MQTT / Config / OTA
                       ↑
                   TLS/mTLS
                       ↑
┌──────────────────── Edge Gateway ────────────────────┐
│                                                     │
│ Config → Template → Device Manager                   │
│                      ↓                              │
│                Poll Scheduler                       │
│                      ↓                              │
│                Protocol Adapter                     │
│                      ↓                              │
│              Decode / Normalize                     │
│                      ↓                              │
│                Quality Engine                       │
│                      ↓                              │
│         ┌────────────┴────────────┐                 │
│         │                         │                 │
│    Latest Cache              Local Store            │
│                                   ↓                 │
│                              Replay Worker           │
│                                   ↓                 │
│                             MQTT Publisher           │
│                                                     │
│ Command → Safety → Write → Readback → Verify        │
│                                                     │
│ Metrics / Logs / Health / Diagnostics / OTA         │
└──────────────────────┬──────────────────────────────┘
                       ↓
             Modbus / BACnet / OPC UA
                       ↓
                 Field Devices
```

---

# 73. 冻结结论

Edge Gateway V1 正式采用：

```text
Go
+
Device Template
+
Protocol Adapter
+
Poll Scheduler
+
Standard Point
+
Quality Engine
+
SQLite Store & Forward
+
MQTT
+
Config Version
+
Local Safety / Readback
```

第一阶段必须先通过：

```text
Meter + Modbus
```

完整链路。

之后按顺序扩展：

```text
PV
→ PCS/BMS Monitoring
→ Alarm
→ ESS Manual Control
→ Advanced Protocol
→ Limited Local Automation
```

Edge 的核心价值最终应体现为：

```text
任何厂家设备
→ 统一标准语义
→ 稳定数据
→ 可诊断
→ 可补传
→ 可安全控制
```
