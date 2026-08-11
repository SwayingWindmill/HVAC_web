# 智慧能源系统标准测试用例与 Golden Dataset

> 文档编号：SE-TEST-004  
> 版本：V1.0  
> 状态：CURRENT  
> 上位设计：测试与验收体系、第一阶段工程实施设计、Edge Gateway、设备接入规范、能源指标体系、计量结算与对账、能源拓扑详细设计  
> 技术基线：Go + React + PostgreSQL + ClickHouse + Redis + MQTT + Edge Gateway  
> 核心目标：将系统关键规则转化为可重复、可自动化、可回归的确定性测试数据和期望结果

---

# 1. 文档定位

已有《测试与验收体系设计》解决：

```text
测试分几层
什么阶段测什么
上线需要哪些Gate
```

本设计解决：

```text
具体输入是什么？
期望输出是什么？
错误场景怎么造？
自动化测试拿什么数据？
不同服务如何共用同一套测试事实？
```

因此 Golden Dataset 是：

```text
设计规范
→ 可执行事实
```

之间的桥梁。

---

# 2. Golden Dataset 原则

Golden Dataset 必须：

```text
确定
小而完整
跨服务复用
版本化
人工可核算
自动可校验
```

禁止：

```text
随机生成后没有固定seed
期望值靠运行结果反推
测试环境每次数据不同
```

---

# 3. Dataset 版本

统一：

```text
golden_dataset_version = 1.0.0
```

与：

```text
Point Standard
Metric Standard
MQTT Schema
Device Template Schema
Settlement Policy
```

分别记录兼容版本。

---

# 4. 测试域

第一版 Golden Dataset 覆盖：

```text
G01 Modbus Decode
G02 Point Normalize / Quality
G03 MQTT Telemetry
G04 Telemetry Worker
G05 Redis Latest
G06 ClickHouse Raw
G07 1min Rollup
G08 Counter / Energy
G09 Demand
G10 Tariff / Settlement
G11 Topology / Virtual Meter
G12 Alarm
G13 Command / Safety
G14 Replay / Duplicate / Out-of-order
G15 Config / Version
G16 Failure / Recovery
```

---

# 5. 标准测试 Site

统一测试对象：

```text
Tenant: T001
Site:   S001
Timezone: Asia/Shanghai
```

---

# 6. 标准设备

```text
Gateway:
GW001

Meter:
MTR001

PV:
PV001

PCS:
PCS001

BMS:
BMS001
```

第一阶段自动化基线主要使用：

```text
GW001 + MTR001
```

---

# 7. Meter 标准 Point

```text
active_power
reactive_power
power_factor
frequency
voltage_a
voltage_b
voltage_c
current_a
current_b
current_c
energy_import
energy_export
```

---

# 8. G01 Modbus Decode Golden

Golden CSV：

```text
golden/modbus_meter_decode.csv
```

覆盖：

```text
UINT16
INT16
UINT32
INT32
FLOAT32
ABCD
CDAB
Multiplier
Negative Power
Counter
```

每一行定义：

```text
registers
data type
word order
multiplier
expected value
```

---

# 9. Decode Case M001

输入：

```text
registers = 0x04D2
type = UINT16
multiplier = 0.1
```

输出：

```text
123.4
```

---

# 10. Decode Case M002

输入：

```text
INT16 = -250
multiplier = 0.1
```

输出：

```text
-25.0
```

用于验证：

```text
ESS / Grid双向功率
```

---

# 11. Decode Case M003

32位 Counter：

```text
registers = [0x0001, 0x86A0]
```

输出：

```text
100000
```

---

# 12. Point Normalize

统一链：

```text
Raw
→ Decode
→ Multiplier
→ Offset
→ Unit
→ Sign
→ Physical Range
→ Standard Point
```

---

# 13. Quality Golden

至少测试：

```text
GOOD
TIMEOUT
OFFLINE
MISSING
STALE
OUT_OF_RANGE
PARSE_ERROR
ESTIMATED
MANUAL
```

---

# 14. Physical Range

Case：

```text
voltage_a = 9999V
physical range = 0~500V
```

Expected：

```text
value = 9999
quality = OUT_OF_RANGE
```

不能：

```text
clamp to 500
```

---

# 15. Parse Error

错误长度：

```text
FLOAT32
只有1个register
```

Expected：

```text
quality = PARSE_ERROR
```

不能产生伪造数值。

---

# 16. MQTT Telemetry Golden

文件：

```text
golden/mqtt_telemetry_valid.json
```

必须通过：

```text
JSON Schema
Envelope Validation
Gateway Validation
Point Validation
```

---

# 17. MQTT Envelope

标准字段：

```text
schemaVersion
messageId
gatewayId
timestamp
sequence
traceId
payload
```

---

# 18. Telemetry Worker Golden

文件：

```text
golden/telemetry_input.jsonl
golden/telemetry_expected.jsonl
```

验证：

```text
Point Mapping
Quality
event_time
ingest_time
Redis Latest
ClickHouse Row
```

---

# 19. Unknown Point

输入：

```text
pointCode = vendor_power_x
```

且不存在 Point Definition。

Expected：

```text
Reject / Quarantine
```

不得：

```text
自动创建Point
```

---

# 20. Duplicate Message

同：

```text
gatewayId + messageId
```

重复到达。

Expected：

```text
仅处理一次
```

---

# 21. Out-of-order

先收到：

```text
10:00:05
```

后收到：

```text
10:00:03
```

Expected：

```text
ClickHouse两条都可保存
Redis Latest仍保持10:00:05
```

---

# 22. Replay

历史数据：

```text
replay = true
```

Expected：

```text
写入ClickHouse
但不能覆盖更新的Redis Latest
```

---

# 23. Redis Latest Golden

更新条件：

```text
incoming.event_time > current.event_time
```

同时间重复：

```text
不得无条件覆盖
```

具体 Tie Breaker 必须固定为：

```text
event_time
→ ingest_time / sequence
```

---

# 24. ClickHouse Raw Golden

每个 Telemetry Expected Row 至少验证：

```text
tenant_id
site_id
gateway_id
device_id
point_id
point_code
event_time
value
quality
replay
```

---

# 25. 1min Rollup Golden

文件：

```text
golden/rollup_1m_cases.json
```

验证：

```text
MIN
MAX
AVG
FIRST
LAST
COUNT
GOOD_COUNT
```

---

# 26. Rollup Case R001

输入：

```text
10, 20, 30, 40
```

Expected：

```text
min   = 10
max   = 40
avg   = 25
first = 10
last  = 40
count = 4
```

---

# 27. Quality Rollup

输入：

```text
GOOD
GOOD
TIMEOUT
GOOD
```

Expected：

```text
count = 4
good_count = 3
good_rate = 75%
```

---

# 28. Counter Golden

文件：

```text
golden/settlement_cases.json
```

至少：

```text
Normal
Reset
Rollover
Replacement
Boundary Estimate
```

---

# 29. Counter Normal

```text
Start = 1000
End = 1200
```

Expected：

```text
Energy = 200 kWh
```

---

# 30. Counter Reset

```text
Start = 1000
Before Reset = 1200
After Reset = 0
End = 50
```

Expected：

```text
Energy = 250 kWh
```

---

# 31. Counter Rollover

Golden 定义：

```text
rollover_modulus = 10000
```

输入：

```text
Start = 9900
End = 100
```

Expected：

```text
Energy = 200 kWh
```

注意：

```text
这里按计数空间[0,10000)定义，
因此计算为(10000-9900)+100。
```

---

# 32. Meter Replacement

```text
Old Start = 1000
Old Final = 1200

New Initial = 10
New End = 110
```

Expected：

```text
Energy = 300 kWh
```

---

# 33. Demand Golden

文件：

```text
golden/demand_cases.json
```

固定：

```text
15min Fixed Window
Site Timezone = Asia/Shanghai
```

---

# 34. Demand Case D001

15min 电量：

```text
25 kWh
```

Demand：

```text
25 / 0.25
=
100 kW
```

---

# 35. max_demand

多个窗口：

```text
80
100
120
90 kW
```

Expected：

```text
max_demand = 120 kW
```

---

# 36. Tariff Golden

文件：

```text
golden/tariff_cases.json
```

基础电价：

```text
PEAK   = 1.00 CNY/kWh
FLAT   = 0.60 CNY/kWh
VALLEY = 0.30 CNY/kWh
```

---

# 37. Tariff Case T001

```text
Peak   = 100 kWh
Flat   = 200 kWh
Valley = 300 kWh
```

Expected：

```text
Energy Charge
=
100×1.00
+
200×0.60
+
300×0.30
=
310 CNY
```

---

# 38. Tariff Boundary

区间：

```text
17:50~18:10
```

18:00：

```text
FLAT → PEAK
```

必须拆成：

```text
10min Flat
10min Peak
```

---

# 39. Tariff Version

Case：

```text
V1 effective before 2026-08-16
V2 effective from 2026-08-16
```

Expected：

```text
同一个月分别按V1/V2计算
```

---

# 40. Settlement Lock Golden

Case：

```text
Period LOCKED
Late Data Arrives
```

Expected：

```text
Original Snapshot unchanged
Change Candidate created
```

不得：

```text
自动覆盖Locked账单
```

---

# 41. Revision Golden

审批后：

```text
Revision 1
→ Revision 2
```

必须保留：

```text
Old Value
New Value
Reason
Operator
Approver
```

---

# 42. Topology Golden

文件：

```text
golden/topology_cases.json
```

覆盖：

```text
Parent/Child
Virtual Meter
Balance
Duplicate Primary
Virtual Cycle
```

---

# 43. Duplicate Counting

输入：

```text
Main = 1000
A = 400
B = 500
```

Expected：

```text
Site Total = 1000
```

不能：

```text
1900
```

---

# 44. Virtual Meter SUM

```text
A = 100
B = 200
C = 300
```

Expected：

```text
600
```

---

# 45. Virtual Meter DIFFERENCE

```text
Main = 1000
Known Children = 900
```

Expected：

```text
Unmetered = 100
```

---

# 46. Balance Golden

输入：

```text
Grid Import   = 700
PV Generation = 300
ESS Discharge = 100

Load          = 1000
ESS Charge    = 50
Grid Export   = 20
```

Expected：

```text
Balance Error
=
1100 - 1070
=
30 kWh
```

---

# 47. Duplicate Primary

同一个 Settlement Boundary：

```text
PRIMARY A
PRIMARY B
```

有效期重叠。

Expected：

```text
Topology Validation Failed
```

---

# 48. Virtual Cycle

```text
VM_A depends VM_B
VM_B depends VM_A
```

Expected：

```text
Release Blocked
```

---

# 49. Alarm Golden

文件：

```text
golden/alarm_cases.json
```

覆盖：

```text
Threshold
Duration
Hysteresis
Recovery
Missing
Storm Dedup
Replay
```

---

# 50. Threshold

规则：

```text
voltage_a > 250V
duration = 10s
```

短暂 5s：

```text
不触发
```

持续 12s：

```text
触发
```

---

# 51. Hysteresis

Trigger：

```text
> 250
```

Recover：

```text
< 245
```

246：

```text
仍保持Alarm
```

---

# 52. Replay Alarm

历史补传：

```text
replay = true
```

默认：

```text
不产生实时告警风暴
```

---

# 53. Command Golden

文件：

```text
golden/command_cases.json
```

覆盖：

```text
Valid
Expired
Duplicate
Out of Range
LOCAL Mode
Offline
Readback Match
Readback Mismatch
```

---

# 54. Valid Command

```text
set_power = 300 kW
allowed range = [-500, 500]
mode = REMOTE_MANUAL
device online
```

Expected：

```text
VALIDATING
→ WRITING
→ DEVICE_ACK
→ EXECUTED
→ VERIFIED
```

---

# 55. Expired Command

```text
expiresAt < now
```

Expected：

```text
EXPIRED
```

不得写物理设备。

---

# 56. Duplicate Command

相同：

```text
command_id
```

再次到达。

Expected：

```text
返回已有结果
不得重复写设备
```

---

# 57. LOCAL Mode

```text
mode = LOCAL
```

远程命令：

```text
REJECTED
```

---

# 58. Out of Range

```text
set_power = 800
allowed = [-500,500]
```

Expected：

```text
REJECTED
```

---

# 59. Readback Mismatch

```text
requested = 300
readback = 100
tolerance = 5
```

Expected：

```text
FAILED / UNVERIFIED
```

不能：

```text
SUCCESS
```

---

# 60. Config Golden

文件：

```text
golden/config_cases.json
```

覆盖：

```text
Checksum
Compatibility
Desired/Reported
Rollback
Duplicate Primary
Breaking Point Change
```

---

# 61. Checksum

Candidate Config 内容变化：

```text
checksum必须变化
```

Checksum 不匹配：

```text
Apply Blocked
```

---

# 62. Desired / Reported

```text
desired = v2
reported = v1
```

Expected：

```text
OUT_OF_SYNC
```

应用成功：

```text
reported = v2
IN_SYNC
```

---

# 63. Config Rollback

Candidate Apply 失败：

```text
current = v1
candidate = v2
```

Expected：

```text
restore v1
reported = v1
status = ROLLED_BACK / FAILED
```

---

# 64. Failure Golden

文件：

```text
golden/failure_cases.json
```

覆盖：

```text
MQTT Down
ClickHouse Down
Redis Down
Edge Offline
Replay Storm
```

---

# 65. MQTT Down

Expected：

```text
Edge Local Queue grows
No data loss within local retention
```

恢复：

```text
Realtime first
Replay second
```

---

# 66. Redis Down

Expected：

```text
Latest degraded
ClickHouse write may continue
```

恢复：

```text
Latest rebuild
```

---

# 67. ClickHouse Down

Expected：

```text
Cloud ingestion bounded retry
Edge eventually buffers
No unbounded memory queue
```

---

# 68. Edge Offline 24h

Expected：

```text
Local Queue retains data
```

恢复：

```text
Realtime resumes
Replay completes
Latest not overwritten by old replay
```

---

# 69. Replay Storm

输入：

```text
Realtime = 10k values/s
Replay   = 20k values/s
```

Expected：

```text
Realtime SLO优先
Replay限流
```

---

# 70. E2E Golden Path

标准：

```text
Modbus Register
↓
Edge Decode
↓
Standard Point
↓
MQTT
↓
Telemetry Worker
↓
Redis Latest
↓
ClickHouse Raw
↓
1min Rollup
↓
API
↓
React
```

使用：

```text
active_power
```

作为第一条 Vertical Slice。

---

# 71. E2E Case E001

输入：

```text
Meter active_power = 123.4 kW
event_time = 2026-08-09T04:00:00Z
```

验证：

```text
Edge Standard Value = 123.4
MQTT Value = 123.4
Redis Latest = 123.4
CH Raw = 123.4
API Latest = 123.4
UI = 123.4 kW
```

---

# 72. Time Golden

必须同时测试：

```text
device_time
gateway_time
cloud_time
```

业务时间：

```text
event_time
```

统一 UTC 存储。

---

# 73. Site Timezone

结算测试：

```text
Asia/Shanghai
```

保证：

```text
UTC 16:00
=
Local 00:00 next day
```

---

# 74. Golden Dataset 文件结构

```text
golden/
├── README.md
├── manifest.json
├── modbus_meter_decode.csv
├── mqtt_telemetry_valid.json
├── telemetry_input.jsonl
├── telemetry_expected.jsonl
├── rollup_1m_cases.json
├── settlement_cases.json
├── demand_cases.json
├── tariff_cases.json
├── topology_cases.json
├── alarm_cases.json
├── command_cases.json
├── config_cases.json
└── failure_cases.json
```

---

# 75. Manifest

记录：

```text
dataset_version
created_at
timezone
point_standard
metric_standard
mqtt_schema
files
checksums
```

---

# 76. CI 使用方式

建议每个仓库测试直接引用：

```text
tests/golden/
```

而不是复制后各自维护。

---

# 77. Go Unit Test

例如：

```text
Decode Modbus
```

读取：

```text
modbus_meter_decode.csv
```

逐行执行：

```text
input → decode → compare expected
```

---

# 78. Edge Contract Test

读取：

```text
mqtt_telemetry_valid.json
```

执行：

```text
Edge output
→ JSON Schema
→ exact semantic assertion
```

---

# 79. Worker Integration Test

读取：

```text
telemetry_input.jsonl
```

写入 Worker。

比较：

```text
Redis
ClickHouse
```

和：

```text
telemetry_expected.jsonl
```

---

# 80. Metric Test

读取：

```text
rollup_1m_cases.json
settlement_cases.json
demand_cases.json
```

验证确定性结果。

---

# 81. Topology Test

读取：

```text
topology_cases.json
```

验证：

```text
Graph Compiler
Virtual Meter
Balance
Validation
```

---

# 82. Control Safety Test

读取：

```text
command_cases.json
```

必须包含大量：

```text
Negative Tests
```

因为控制系统：

```text
Reject正确
```

和：

```text
Execute正确
```

同样重要。

---

# 83. Snapshot Testing

对于：

```text
OpenAPI Response
MQTT Payload
Config Package
Settlement Snapshot
```

可以做结构 Snapshot。

但不能因为实现变了就无脑：

```text
Update Snapshot
```

必须先人工Review语义。

---

# 84. Floating Point

比较：

```text
Float
```

必须定义：

```text
absolute tolerance
relative tolerance
```

例如：

```text
1e-6
```

财务金额应使用：

```text
Decimal / fixed precision
```

不使用普通 Float 直接判最终账单。

---

# 85. Timestamp

Golden Dataset 时间全部固定。

禁止：

```text
now()
```

作为不可预测输入。

测试时 Clock 必须：

```text
Injectable
```

---

# 86. Random

Simulator Random 模式：

```text
固定seed
```

例如：

```text
seed = 20260809
```

---

# 87. Golden Dataset 变更流程

```text
Change Requirement
↓
Update Spec
↓
Update Dataset
↓
Independent Review
↓
Run Old + New Implementation
↓
Approve
↓
New Dataset Version
```

---

# 88. 不允许实现反推Golden

禁止：

```text
先让程序跑
再把程序输出保存成Expected
```

Golden Expected 必须：

```text
来自规范
+
人工核算
```

---

# 89. Breaking Dataset Change

例如：

```text
ESS功率方向改变
Counter Rollover定义改变
Demand Window改变
```

必须：

```text
MAJOR Version
```

---

# 90. Dataset Owner

建议：

```text
QA
+
Domain Owner
```

共同维护。

---

# 91. Production Bug Regression

每一个：

```text
SEV-1
SEV-2
严重数据Bug
控制Bug
```

修复后必须增加：

```text
Regression Golden Case
```

---

# 92. Bug Case ID

建议：

```text
REG-2026-001
REG-2026-002
```

并关联：

```text
Incident ID
```

---

# 93. Test Case ID

统一：

```text
MOD-
TEL-
ROL-
CNT-
DEM-
TRF-
TOP-
ALM-
CMD-
CFG-
FAIL-
E2E-
```

---

# 94. 第一阶段必跑集

PR：

```text
MOD
TEL
CMD Safety
Schema
```

Merge：

```text
+ Worker
+ Rollup
+ Topology
```

Staging：

```text
+ Settlement
+ Alarm
+ Failure
+ E2E
```

Production Release：

```text
Golden Full Suite
```

---

# 95. 第一阶段 Gate

至少：

```text
所有Golden PASS

Unknown Point REJECT

Duplicate无污染

Replay不覆盖Latest

Counter Reset PASS

Meter Replacement PASS

Demand PASS

Tariff PASS

Topology无重复计量

Command Duplicate不重复执行

LOCAL Mode拒绝Remote

Readback Mismatch不成功

Config Rollback PASS

Edge Offline Replay PASS
```

---

# 96. 与 Simulator 的关系

Simulator 用于：

```text
生成动态流量
```

Golden Dataset 用于：

```text
固定正确答案
```

两者不能替代。

---

# 97. 与性能测试的关系

Golden：

```text
Correctness
```

Performance：

```text
Throughput / Latency / Stability
```

性能压测也必须抽样用 Golden 验证：

```text
高吞吐时没有算错
```

---

# 98. 与 UAT 的关系

UAT 验证：

```text
业务可接受
```

Golden 验证：

```text
系统计算符合标准
```

---

# 99. 最终冻结原则

第一：

```text
测试数据必须版本化
```

第二：

```text
Expected Result必须来自规范
```

第三：

```text
所有核心数据链必须有Golden Case
```

第四：

```text
控制必须有Negative Golden Case
```

第五：

```text
时间、随机、浮点必须可确定
```

第六：

```text
生产严重Bug必须沉淀Regression Case
```

第七：

```text
Golden Dataset必须跨Edge、Cloud、Metric、Settlement共享
```

最终目标是做到：

```text
同一组输入，

Edge知道自己应该输出什么，
Telemetry Worker知道应该写什么，
Redis知道Latest应该是什么，
ClickHouse知道Raw和Rollup是什么，
Metric知道结果是什么，
Settlement知道账单是什么，
Control知道哪些命令必须拒绝。

任何一次代码升级，
都可以用同一套数据证明：
“新版本没有改变已经冻结的业务语义。”
```
