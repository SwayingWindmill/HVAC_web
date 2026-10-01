# 34 集成管理 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `34 集成管理`  
> **Route intent：** `/settings/integrations`、`/settings/integrations/:integrationId`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；`OPC UA`、`BACnet/SC`、`MQTT`、`OAuth/OIDC`、`TLS`、`Webhook`、`API` 等标准术语保留为专业辅助。  
> **设计输入声明：** 本文件不参考当前项目旧 Integration、Registry、Gateway、Connector、ThingsBoard 或凭证配置页面。现有实现只能在实施阶段作为真实 Connector / Endpoint / Credential / Mapping / Capability / Sync / Health / Audit contract 的候选证据来源。

---

# 1. Primary Job

本 Surface 的核心任务是：

> **治理外部数据与控制连接的生命周期，让用户明确知道“连到了谁、以什么身份、被授权做什么、正在同步什么、映射到什么业务对象、运行得是否健康、哪里失败、变更会影响什么”。**

34 是 **Integration Lifecycle + Connector Operations Workspace**，不是：

- “API Key + URL + Test” 表单；
- Secret Vault；
- 设备资产中心；
- 数据质量中心；
- 点位语义模型编辑器；
- 实时控制台；
- 通用 ETL IDE；
- 在浏览器直接连接 BACnet / OPC UA / MQTT / Modbus 的工具；
- 一个绿色 `Connected` 就代表数据、设备和业务全部正常的黑盒页面。

用户离开本页前应该能回答：

1. 当前 Integration 连接哪个 Source / External System；
2. Connector 类型和部署位置是什么；
3. Endpoint / Protocol / Security Profile 是什么；
4. 当前 Authentication 是否成功；
5. 当前 Authorization / Scope 到底允许什么；
6. 支持哪些 Capability：read、history、subscribe、write、command、metadata 等；
7. 当前哪些 source objects 被映射到 32 的 canonical entities；
8. Live sync / historical sync / outbound write 是否健康；
9. 当前 lag、backlog、last success、error 是什么；
10. Connector、Source、Device、Data Quality 哪一层出了问题；
11. Credential / Certificate 是否即将过期或需要轮换；
12. 当前使用哪个 Connector / Mapping / Credential Revision；
13. 变更、cutover、rollback 会影响哪些数据和控制能力；
14. 是否需要 31 Data Quality / 32 Semantic Model / 25–28 Control 继续处理。

---

# 2. 主要用户

## Primary

- **Integration Administrator：**建立、测试、启用、暂停、升级和退役 Integration；
- **OT / BAS Engineer：**维护 BACnet、OPC UA、MQTT、BMS / EMS / Gateway 集成；
- **Data Engineer：**维护历史同步、cursor、mapping、schema 与 backfill；
- **Platform Operator：**观察 Connector runtime、lag、error、backlog、rate limit；
- **Security Administrator：**管理 credential reference、certificate trust、rotation 与授权边界。

## Secondary

- Data Quality Owner：从 31 深链到 source / connector issue；
- Semantic Model Owner：从 32 处理 source binding / mapping；
- Control Engineer：确认集成是否真正支持安全控制能力；
- Auditor：检查 credential rotation、mapping revision、control capability、change history；
- Site Administrator：消费站点级 Integration 状态，但不能默认获得 Secret / Control 权限。

---

# 3. 外部最佳实践依据

## 3.1 NIST SP 800-82 Rev.3 — Building Automation 属于 OT

NIST SP 800-82 Rev.3 明确把 Building Automation Systems、physical monitoring / measurement systems 纳入 OT，并强调 OT security 必须兼顾 performance、reliability 与 safety。

来源：

- https://csrc.nist.gov/pubs/sp/800/82/r3/final

本页采用：

- External / OT Integration 不是普通 SaaS webhook；
- Read 与 Write / Control capability 必须分离；
- Connector / network / credential 变更需要明确影响和审计；
- 浏览器不直接持有 OT credential 或执行 OT protocol；
- integration degraded 时不能通过危险 fallback 获得控制能力。

## 3.2 OPC UA Security Model — Authentication 与 Authorization 分离

OPC UA 明确分别定义：

- Application Authentication；
- User Authentication；
- Authorization；
- SecureChannel / Certificate Trust。

官方说明中，Authorization 可以细化到具体用户、资源与操作；认证成功不等于允许 read / write / execute。

来源：

- https://reference.opcfoundation.org/specs/OPC-10000-2/4
- https://reference.opcfoundation.org/specs/OPC-10000-2/5.2.2
- https://reference.opcfoundation.org/specs/OPC-10000-2/5.2.4

本页采用：

```text
Authenticated ≠ Authorized
Trusted Certificate ≠ Authorized Action
Read Authorized ≠ Write Authorized
Write Authorized ≠ Safe Control Authority
```

## 3.3 MQTT 5.0 — Connection / Session / Delivery 是不同事实

MQTT 5.0 明确区分 Network Connection、Keep Alive、Session State 与不同 QoS delivery flow。Keep Alive 可以帮助判断 network/server availability，但不能证明上游设备或业务数据正常。

来源：

- https://docs.oasis-open.org/mqtt/mqtt/v5.0/mqtt-v5.0.html

本页采用：

```text
Connected ≠ Session Healthy automatically
Session Healthy ≠ Message Flow Healthy
Message Flow Healthy ≠ Source Data Fresh
QoS ≠ End-to-end business exactly-once
```

## 3.4 BACnet / BACnet Secure Connect

BACnet 是 Building Automation 的核心互操作协议；安全连接、证书与网络可达性只解决通信层事实，不能替代业务对象、控制权限和数据质量判断。

来源：

- https://bacnet.org/

本页采用：Protocol Reachability、Security / Trust、Object Capability、Runtime Data Quality 分层呈现。

## 3.5 DOE / FEMP EMIS

DOE/FEMP EMIS 实践强调从 BAS、metering、utility、weather、enterprise data 等多源系统获取可信数据，并持续维护数据质量和 analytics 可用性。

来源：

- https://www.energy.gov/cmei/femp/energy-management-information-systems
- https://www.energy.gov/cmei/femp/operations-support-energy-management-information-systems

本页采用：Integration success 必须以业务数据持续可用、映射正确和可追溯为目标，而不是以 socket connected 为终点。

---

# 4. 产品语言契约

主界面中文优先：

```text
集成管理
连接
数据源
目标系统
连接器
端点
认证
授权范围
能力
映射
实时同步
历史同步
同步延迟
积压
最近成功
凭证
证书
到期时间
连接测试
映射验证
暂停
恢复
切换
回滚
错误
重试
审计
```

Engineering Detail 可以保留：

```text
OPC UA
BACnet/SC
MQTT
REST API
Webhook
OAuth 2.0 / OIDC
mTLS
X.509
QoS
Cursor
Checkpoint
Rate Limit
Dead-letter
```

---

# 5. Mandatory Semantic Separation

```text
Integration ≠ Connector Runtime
Connector Definition ≠ Connector Instance
Endpoint ≠ Source System
Endpoint Reachable ≠ Authenticated
Authenticated ≠ Authorized
Authorized ≠ Capability Available
Capability Available ≠ Safe to Use
Read Capability ≠ Write Capability
Write Capability ≠ Control Authority

Connector Health ≠ Source Health
Source Health ≠ Device Health
Device Health ≠ Data Quality
Transport Healthy ≠ Data Flow Healthy
Data Flow Healthy ≠ Mapping Valid
Mapping Valid ≠ Business Data Valid

Connected ≠ Synchronized
Sync Running ≠ Sync Caught Up
Sync Lag ≠ Data Freshness automatically
Backlog ≠ Data Loss
Last Success ≠ Currently Healthy

Connection Test Passed ≠ Data Flow Healthy
Authentication Test Passed ≠ Authorization Complete
Sample Read Passed ≠ Full Scope Read Healthy
Test Write Passed ≠ Safe Production Control

Credential Reference ≠ Secret
Credential Valid ≠ Authorized Scope
Credential Rotated ≠ Integration Reconfigured
Certificate Trusted ≠ Certificate Unexpired

Source Object ≠ Canonical Entity
Source ID ≠ Canonical ID
Mapping ≠ Identity
Mapping Changed ≠ Historical Mapping Rewritten
Mapping Published ≠ Historical Data Reprocessed

Live Sync ≠ Backfill
Backfill ≠ Correction
Replay ≠ Live Ingest
Retry ≠ Replay

Configuration Published ≠ Runtime Activated
Activated ≠ Healthy
Cutover Requested ≠ Cutover Completed
Rollback Requested ≠ Previous Version Restored

Provider ACK ≠ Business Success
Protocol QoS ≠ End-to-end exactly-once
```

---

# 6. Core Domain Objects

34 至少治理：

```text
Integration
Integration Revision
Connector Definition
Connector Runtime Instance
Endpoint
Authentication Profile Reference
Credential / Certificate Reference
Authorization Scope
Capability Declaration
Source Namespace
Source Object
Mapping Set
Mapping Revision
Sync Job / Stream
Checkpoint / Cursor
Backfill Job
Retry Policy
Rate / Quota Policy
Health Observation
Integration Incident
Connection Test Run
Mapping Validation Run
Cutover Plan
Rollback Target
Audit Record
```

Secret material 由专门 secret / credential owner 持有；34 只引用，不回显。

---

# 7. Integration Identity Contract

每个 Integration 至少有：

```text
Integration ID
Display Name
Integration Type
Source System
Target Domain
Site / Portfolio Scope
Owner
Current Revision
Lifecycle State
Runtime State
Created At
```

Display Name 不是 Identity。

重命名不创建新 Integration。

---

# 8. Integration Lifecycle Contract

Governance State：

```text
Draft
In Review
Approved
Published
Superseded
Retired
```

Runtime State：

```text
Inactive
Starting
Active
Paused
Degraded
Failed
Stopping
Unavailable
Unknown
```

明确：

```text
Published ≠ Active
Active ≠ Healthy
Paused ≠ Failed
Retired ≠ Source Deleted
```

---

# 9. Connector Definition Contract

Connector Definition 至少声明：

```text
Connector Type
Protocol
Supported Security Profiles
Supported Authentication Methods
Supported Capabilities
Configuration Schema
Runtime Requirements
Version
Vendor / Implementation
```

Connector Definition 是“这个连接器能做什么”，Integration Revision 是“本次怎么配置它”。

---

# 10. Connector Definition ≠ Runtime Instance

同一个 Connector Definition 可以运行多个实例：

```text
OPC UA Connector v4
→ Instance SG-PLANT-01
→ Instance SG-PLANT-02
```

Runtime health 必须按实例观察。

不能因为定义本身有效就认为每个 instance healthy。

---

# 11. Endpoint Contract

Endpoint 至少记录：

```text
Endpoint ID
Protocol
Host / URL / Broker / Server URI
Port
Security Profile
Network Zone
Expected Server Identity
Site Scope
Owner
Effective Period
```

UI 不默认展示敏感内部网络细节给无权限用户。

---

# 12. Endpoint Reachability Contract

Reachability 可分层：

```text
DNS / Name resolution
Network route
TCP / transport
TLS / SecureChannel
Protocol handshake
```

因此：

```text
TCP Reachable
≠ Protocol Healthy

TLS Established
≠ Application Authenticated
```

---

# 13. Authentication Contract

支持的 authentication 取决于 Connector / Protocol：

```text
Application certificate
Client certificate / mTLS
Username / Password
OAuth / OIDC token
API credential
Signed request
Protocol-specific credential
```

34 只保存：

```text
Credential Reference
Credential Type
Credential Owner
Last Rotated At
Expires At
Validation State
```

不保存 / 回显 plaintext secret。

---

# 14. Authentication ≠ Authorization

Authentication 只回答：

> 对方是否确认“你是谁”？

Authorization 回答：

> 这个身份允许做什么？

因此 OPC UA / API 集成必须能表达：

```text
Authenticated: Yes
Read History: Allowed
Subscribe: Allowed
Write: Denied
Method Call: Denied
```

不能把登录成功显示成“权限正常”。

---

# 15. Authorization Scope Contract

Authorization Scope 至少可表达：

```text
Allowed namespace / resource
Allowed Site / Device / Object scope
Read
Subscribe
History Read
Metadata Read
Write
Command / Method Execute
Admin capability if explicitly supported
```

必须遵循 least privilege。

---

# 16. Capability Contract

Connector capability 必须由 owner 明确声明 / 探测并持久化，例如：

```text
Realtime Read
Subscription / Stream
Historical Read
Metadata Discovery
Alarm / Event Read
Write
Command / Method
File / Artifact Read
Webhook Receive
Webhook Send
```

前端不能：

```text
protocol = OPC UA
→ assume historical read supported
```

---

# 17. Read Capability ≠ Write / Control Capability

特别是 OT 集成：

```text
Realtime Read
≠ Write

Write
≠ Command Execution

Command Execution
≠ Safe Control Authority
```

25–28 Control / Strategy / Execution 仍是高风险控制事实 owner。

34 只治理“integration 是否提供这种 capability”。

---

# 18. Control-capable Integration Boundary

任何支持 write / command 的 Integration 必须额外显示：

```text
Capability Owner
Authorization Scope
Allowed Target Scope
Protocol Security
Command Gateway / Execution Owner
Audit Enabled
Control Integration Status
```

不能存在通用：

```text
[Test Write]
```

按钮去现场写任意点。

---

# 19. Security Profile Contract

不同协议使用自身标准安全模型，例如：

```text
OPC UA SecurityPolicy / MessageSecurityMode
BACnet/SC certificate / trust
HTTPS TLS
MQTT TLS + authentication
OAuth / OIDC scopes
```

Security Profile 必须由 backend / connector owner 验证。

浏览器不自行判断证书可信链。

---

# 20. Certificate / Trust Contract

对于 certificate-based integrations 至少记录：

```text
Certificate Reference
Subject / Application identity
Issuer
Fingerprint
Valid From
Expires At
Trust State
Revocation State if owner provides
Last Validation At
```

并区分：

```text
Trusted ≠ Unexpired
Unexpired ≠ Authorized
```

---

# 21. Credential Rotation Contract

Credential rotation 建议流程：

```text
Create / obtain new credential
↓
Validate reference
↓
Test authentication
↓
Test required authorization scope
↓
Controlled cutover
↓
Observe integration
↓
Revoke old credential
```

不能：

```text
new secret saved
→ old secret immediately deleted
```

如果协议/owner 不能支持 overlap，则必须有明确 maintenance / cutover plan。

---

# 22. Credential Expiry Contract

必须区分：

```text
Expires Soon
Expired
Invalid
Revoked
Unknown
```

Expiry 提醒不是 Connector Failure。

已过期是否导致 runtime failure 必须由实际 runtime evidence 表达。

---

# 23. Source System Contract

Source System 是外部事实 owner，例如：

```text
BAS
Metering Platform
Utility API
Weather Service
CMMS
ERP
BMS Gateway
OPC UA Server
MQTT Broker / Edge Gateway
```

Source System identity ≠ Endpoint。

一个 Source System 可以有多个 endpoint / environment。

---

# 24. Source Object Contract

Source Object 至少有：

```text
Source Object ID
Source Namespace
Source Type
Source Path / Address
Source Metadata
Observed Schema
Source Revision / Version if available
```

Source Object ID 不自动成为 32 Canonical ID。

---

# 25. Mapping Contract

Mapping 必须显式连接：

```text
Source Object
→ Canonical Entity / Point / Meter / Relationship
```

并记录：

```text
Mapping ID
Mapping Revision
Source binding
Canonical target
Transform / conversion
Unit handling
Data nature
Effective From / To
Owner
Validation state
```

---

# 26. Mapping ≠ Identity

映射只是 source-to-canonical relationship。

```text
Source Object ID
≠ Canonical ID
```

同一个 canonical point 在 connector migration 时可以绑定新的 source object，而业务 identity 保持不变。

---

# 27. Semantic Model Handoff

32 是以下事实 owner：

```text
Canonical Identity
Entity Class
Point / Meter semantics
Quantity
Unit
Relationship
Effective model
```

34 不重新定义 ontology。

Mapping 到错误对象时，在 34 变更 binding；若 canonical model 本身错误，进入 32。

---

# 28. Mapping Validation Contract

Publish mapping 前至少验证：

```text
Source object exists
Canonical target exists
Quantity compatible
Unit compatible / explicit conversion
Data type compatible
Required relationship valid
No prohibited duplicate binding
Effective period valid
```

不能只验证“名称相似”。

---

# 29. Schema Drift Contract

外部 Source 可能发生：

```text
Field added / removed
Type changed
Path changed
Unit changed
Enum changed
Namespace changed
API version changed
```

Schema drift 必须形成明确状态：

```text
Compatible
Needs Review
Breaking
Unknown
```

不能因为 parser 还能返回 JSON 就视为 integration healthy。

---

# 30. Unit / Scale Transformation Contract

Transformation 必须显式版本化，例如：

```text
°F → °C
kW raw integer × 0.1
Wh → kWh
counter rollover handling
```

禁止：

```text
unit missing
→ guess by magnitude
```

Mapping revision 必须保留 conversion definition。

---

# 31. Event Time / Ingest Time Contract

Integration 必须保留：

```text
Source Event Time
Source Timezone / Offset
Observed At if available
Received At
Ingested At
```

延迟同步不能把 Ingest Time 冒充 Event Time。

31 Data Quality 负责同步/时间质量影响。

---

# 32. Live Sync Contract

Live Sync 至少显示：

```text
Runtime State
Last Message / Poll At
Last Successful Ingest
Current Lag
Throughput
Backlog
Error Rate
Checkpoint / Cursor if applicable
```

不能只有：

```text
Connected
```

---

# 33. Historical Sync Contract

Historical Sync 是独立 capability / job：

```text
Requested Range
Source Range
Cursor / Pagination
Fetched Count
Accepted Count
Rejected Count
Duplicate Count
Last Checkpoint
Completion State
```

Live health 不证明 history health。

---

# 34. Sync State Contract

建议：

```text
Idle
Starting
Running
Caught Up
Lagging
Backlogged
Paused
Failed
Unavailable
Unknown
```

```text
Running ≠ Caught Up
Lagging ≠ Failed
Backlog ≠ Data Loss
```

---

# 35. Sync Lag Contract

Lag 必须定义语义，例如：

```text
Now - latest successfully ingested source event time
```

或 source owner 明确的其它定义。

必须同时展示：

```text
Definition
Measured At
Expected SLA / Objective if owner-defined
```

---

# 36. Sync Lag ≠ Data Freshness Automatically

例如 source 本来每 24 小时发布一次账单：

```text
Sync lag = 12 h
```

可能仍然正常。

而 5 秒 cadence telemetry：

```text
lag = 2 min
```

可能严重 stale。

Freshness 由 31 结合 expected cadence 判断。

---

# 37. Checkpoint / Cursor Contract

Pull / stream connector 必须记录 owner-defined progress：

```text
Cursor
Offset
Sequence
Timestamp watermark
Page token
Subscription checkpoint
```

Checkpoint 是 connector progress，不等于业务数据 completeness。

---

# 38. Duplicate / Idempotency Contract

Integration 必须能够识别 protocol/source 允许范围内的 duplicate delivery。

建议持有：

```text
Source Event ID
Message ID
Sequence
Idempotency Key
Source + Timestamp + Owner-defined key
```

禁止仅使用 payload hash 作为所有场景的 universal identity。

---

# 39. Protocol Delivery ≠ Business Exactly-once

即使 protocol 提供 QoS / delivery guarantee，也不能自动宣称：

```text
Business Side Effect Exactly Once
```

消息可能在：

```text
Broker
Connector
Transformation
Persistence
Downstream Projection
```

发生不同 retry / dedup 行为。

因此业务 exactly-once 必须由端到端 owner contract 明确。

---

# 40. Retry Contract

数据同步 retry 可以采用 bounded policy：

```text
Retryable Error
→ Backoff
→ Retry within policy
→ Failed / Paused / Dead-letter when exhausted
```

必须记录：

```text
Attempt
Reason
First Failure
Last Failure
Next Retry
Policy
```

禁止无限重试。

---

# 41. Control Retry Boundary

对于 write / command：

```text
Integration-level network retry
≠ Safe command retry
```

高风险控制命令必须遵循 25 / 28 的 Execution / Idempotency / Reconciliation contract。

34 不允许通用 connector 自动把控制命令“失败就重发”。

---

# 42. Rate Limit / Quota Contract

对于 API / SaaS integration 至少可显示：

```text
Quota
Rate Limit
Remaining if owner exposes
Reset At
Throttled Requests
Backoff State
```

Rate limited ≠ Authentication Failure。

---

# 43. Backpressure Contract

当 downstream 处理速度不足时必须可见：

```text
Queue Depth
Oldest Pending Age
Consumer Lag
Dropped / Rejected count if any
```

不能通过 silent drop 保持“Green”。

---

# 44. Dead-letter / Rejected Data Contract

无法处理的数据必须保留可处置事实：

```text
Source record / message reference
Reason
Mapping Revision
Connector Revision
First Seen
Last Seen
Count
Owner
Disposition
```

敏感 payload 可受权限限制。

---

# 45. Backfill Contract

Backfill 是明确的 historical ingestion operation：

```text
Requested Window
Reason
Source Snapshot / Revision if available
Mapping Revision
Transform Revision
Started / Completed At
Counts
Errors
```

```text
Backfill ≠ Live Sync
```

---

# 46. Replay Contract

Replay 使用已存在的 source / integration records 重新经过指定 pipeline / mapping，必须明确：

```text
Input Dataset
Input Revision
Mapping Revision
Transform Revision
Output Target
Side-effect policy
```

Replay 默认不能重复触发真实 external side effects。

---

# 47. Backfill / Replay ≠ Correction

Correction 表示原 source fact / mapping / transform 被认定需要修正。

Backfill / replay 只是重新获取 / 重新处理。

```text
Replay Completed
≠ Source Corrected
```

31 负责 Data Quality Issue / Correction / downstream impact。

---

# 48. Connector Health Contract

Connector Health 至少分解：

```text
Process / Runtime
Network / Transport
Authentication
Authorization
Source Protocol
Sync Runtime
Mapping Runtime
Outbound Delivery if applicable
```

不提供单一黑盒 `Integration Health = 92`。

---

# 49. Connector Health ≠ Source Health

例如：

```text
Connector Runtime: Healthy
Authentication: Healthy
Source API: 503
```

这时：

```text
Connector ≠ Failed process
Source Service = Unavailable
```

二者必须分开。

---

# 50. Source Health ≠ Device Health

如果 BAS Gateway / OPC UA server 不可达：

```text
Source / Endpoint Unavailable
```

不能推出：

```text
AHU Offline
Chiller Stopped
Meter Failed
```

设备物理状态必须来自其权威 owner。

---

# 51. Data Flow Health Contract

Data Flow Health 至少关注：

```text
Expected messages / polls
Actual received
Lag
Backlog
Rejected records
Mapping errors
Persistence errors
Late / out-of-order
```

它与 network connected 分离。

---

# 52. Data Quality Handoff

31 是：

```text
Coverage
Freshness
Completeness
Validity
Plausibility
Synchronization
Business Impact
```

owner。

34 可以产生 Integration Evidence：

```text
Source Unavailable
Mapping Failed
Lagging
Schema Drift
Rejected Records
```

这些可以成为 31 Data Quality Issue 的 evidence / source。

---

# 53. Health State Contract

建议 Integration Runtime Health：

```text
Healthy
Degraded
Failed
Paused
Unavailable
Unknown
```

必须同时显示 component reason。

不能：

```text
one healthy subcomponent
→ overall healthy
```

---

# 54. Last Success Contract

`Last Successful Sync` 必须带：

```text
Operation type
Completed At
Checkpoint / Range
Records
Revision
```

```text
Last Success yesterday
≠ Currently Healthy
```

---

# 55. Error Contract

Error 至少有：

```text
Error ID
Category
Stage
First Seen
Last Seen
Count
Current / Historical
Retryable
Affected Scope
Owner
Evidence
```

Category 可以包括：

```text
Network
TLS / Certificate
Authentication
Authorization
Protocol
Rate Limit
Schema
Mapping
Transformation
Persistence
Timeout
Source Error
Unknown
```

---

# 56. Error ≠ Root Cause Automatically

例如：

```text
TLS handshake failed
```

是 failure observation，不自动证明 root cause 是 certificate expiry。

如果 certificate validation evidence 明确过期，才可以确认。

10 Diagnosis 的根因语义继续适用。

---

# 57. Test Connection Contract

`Test Connection` 必须是分层测试，不是一个绿色 toast。

至少可以显示：

```text
Endpoint reachability
TLS / SecureChannel
Application authentication
User authentication
Required authorization scope
Capability discovery
Optional safe sample read
```

测试结果必须带 timestamp / revision。

---

# 58. Connection Test Passed ≠ Data Flow Healthy

一次测试成功只能证明：

> 在该测试时刻，该测试范围通过。

它不能证明：

```text
continuous sync healthy
history complete
mapping correct
source data valid
control safe
```

---

# 59. Safe Test Boundary

默认 Test 不产生现场副作用。

允许：

```text
Reachability test
Authentication test
Authorization discovery
Metadata read
Read-only sample
```

Write / command 测试必须进入 Control owner 的明确测试流程。

---

# 60. Webhook Inbound Contract

Inbound webhook 至少治理：

```text
Endpoint identity
Authentication / Signature method
Replay protection if supported
Allowed source
Schema version
Rate limit
Idempotency
Received / rejected counts
```

Webhook endpoint secret 不进入浏览器。

---

# 61. Webhook Outbound Contract

Outbound webhook 至少治理：

```text
Target reference
Auth reference
Payload / schema revision
Timeout
Retry policy
Idempotency key
Delivery state
Dead-letter handling
```

33 Notification delivery 如果使用 webhook，34 持有 connector / endpoint health；33 持有 notification lifecycle。

---

# 62. Mapping Revision / Effective Date Contract

任何 mapping 实质变更都必须形成新 revision：

```text
Source binding
Canonical target
Unit conversion
Transformation
Data nature
Effective From / To
```

历史数据 / event 必须能解析当时实际 mapping revision。

---

# 63. Current Mapping ≠ Historical Mapping

例如：

```text
2026-01 → 2026-08
Source A17 → M-02

2026-09 →
Source B03 → M-02
```

查询 7 月历史时仍应使用当时的 binding/provenance。

不能通过当前 mapping 重解释所有历史。

---

# 64. Mapping Changed ≠ Historical Data Rewritten

修改 mapping 后：

```text
New Mapping Published
```

不等于：

```text
Historical Data Reprocessed
Historical KPI Recomputed
Historical Report Corrected
```

需要进入 31 impact assessment / recomputation workflow。

---

# 65. Integration Revision Contract

Integration Revision 至少绑定：

```text
Connector Definition Version
Endpoint Revision
Security Profile
Credential Reference
Authorization Scope
Capability Config
Mapping Set Revision
Sync Config
Retry / Rate policy
Runtime placement
Effective Date
```

已 Published Revision 不原地修改。

---

# 66. Change Review Contract

高影响变更包括：

```text
Endpoint change
Credential / certificate change
Auth scope expansion
Enable write / command
Mapping target change
Unit / scale change
Protocol / API version change
Backfill / replay into production
Runtime placement change
```

必须有：

```text
Change reason
Impact
Test evidence
Approver if required
Cutover plan
Rollback target
Audit
```

---

# 67. Cutover Contract

Cutover 可以支持：

```text
Pilot
Shadow read
Dual-run comparison
Controlled stop/start
Scheduled window
```

不能默认 old / new connector 同时写生产数据导致 double ingest。

---

# 68. Dual-run / Shadow Contract

Dual-run 如果启用，必须明确：

```text
Authoritative source
Shadow source
Comparison window
Dedup policy
No-production-write rule if applicable
Exit criteria
```

Shadow data 不能自动成为 authoritative business fact。

---

# 69. Rollback Contract

必须明确 Rollback Target：

```text
Integration Revision
Mapping Revision
Credential reference compatibility
Runtime version
```

禁止：

```text
rollback = previous
```

而不指定版本。

---

# 70. Rollback Requested ≠ Restored

正式链路：

```text
Rollback Requested
↓
Connector Reconfigured
↓
Runtime Activated
↓
Auth / Capability Verified
↓
Sync Resumed
↓
Health Observed
↓
Rollback Completed
```

不能点击以后立即显示恢复成功。

---

# 71. Environment Contract

至少区分：

```text
Development
Test
Staging
Production
```

不同环境的 endpoint / credential / authorization 不混用。

测试 credential 不能 silent fallback 到 production。

---

# 72. Runtime Placement Contract

Connector 可以部署在：

```text
Cloud
Site Gateway
Edge Node
DMZ / Integration Zone
Other owner-defined runtime
```

必须知道 runtime location 与 network/security boundary。

但 UI 不默认暴露具体敏感 IP / topology 给无权限用户。

---

# 73. Integration Ownership Contract

每个 Integration 至少有：

```text
Business Owner
Technical Owner
Security / Credential Owner
Source System Owner
Mapping Owner
On-call / Escalation owner if applicable
```

一个“管理员”字段不足以支撑真实治理。

---

# 74. Permission Contract

典型权限：

```text
Read Integrations
Read Health
Read Sensitive Metadata
Create Draft Integration
Edit Draft Integration
Test Connection
Publish Integration
Activate / Pause Integration
Manage Mapping
Run Backfill
Run Replay
Rotate Credential Reference
Approve Control Capability
View Delivery / Error Records
Read Audit
```

Secret read 不自动包含在 Integration 管理权限中。

---

# 75. Security Boundary Contract

浏览器不得：

- 直接持有外部 system secret；
- 直接建立 OT protocol session；
- 直接使用 certificate private key；
- 直接调用 unrestricted write / command endpoint；
- 通过 CORS / browser storage 规避 connector backend。

所有外部 integration action 必须经过受控 backend / connector owner。

---

# 76. Secret Handling Contract

页面最多显示：

```text
Credential Reference
Type
Owner
Last Rotated
Expires At
Validation State
```

禁止显示：

```text
raw password
API token
private key
client secret
refresh token
```

日志 / error 也必须避免 secret leakage。

---

# 77. Audit Contract

至少审计：

```text
Integration create / edit
Revision create
Endpoint change
Security profile change
Credential reference change
Authorization scope change
Capability change
Mapping revision
Unit / transform change
Publish
Activate / Pause
Connection test
Backfill / replay
Cutover / rollback
Control capability enablement
Certificate trust change
```

Audit 保留 who / when / before / after / reason / revision / evidence。

---

# 78. AI Assistance Boundary

AI 可以：

- 总结 recurring integration errors；
- 建议 mapping candidate；
- 解释 schema drift；
- 草拟 cutover checklist；
- 总结 failed records；
- 建议 investigation path。

AI 不能：

- 自动把 source object 映射成 canonical entity 并直接 publish；
- 自动扩大 authorization scope；
- 自动开启 write / control capability；
- 自动接受未知 certificate；
- 自动回显 / 导出 secret；
- 自动把 failed connector 切到另一个 source 作为 authoritative；
- 自动执行 production backfill / replay / rollback。

AI suggestion 必须保持 Candidate / Draft。

---

# 79. No Defensive Programming / No Compatibility Design

明确禁止：

```text
integration API error
→ []

endpoint unavailable
→ source down automatically

source down
→ device offline

authenticated
→ authorized

authorized read
→ write allowed

write capability
→ safe control authority

connection test passed
→ integration healthy

TCP reachable
→ protocol healthy

TLS established
→ authenticated

keepalive healthy
→ source data fresh

connected
→ synchronized

sync running
→ caught up

last success exists
→ currently healthy

sync lag high
→ data stale without cadence contract

backlog
→ data lost

credential exists
→ credential valid

certificate trusted
→ unexpired

credential rotated
→ integration reconfigured

source object name matches
→ canonical mapping

unit missing
→ guess by magnitude

mapping missing
→ infer from name

mapping changed
→ rewrite historical mapping

mapping published
→ historical data reprocessed

backfill completed
→ correction complete

replay completed
→ source corrected

QoS 2
→ business exactly-once

retryable failure
→ retry forever

network timeout on control write
→ resend automatically

new integration revision
→ mutate old revision

cutover requested
→ cutover completed

rollback requested
→ restored

source connector failed
→ silently use alternate source

multiple integration APIs
→ first success wins

legacy connector fallback
legacy registry compatibility adapter
browser direct OT connection
```

正式原则：

> **连接、认证、授权、能力、同步、映射、数据质量、设备状态和控制权限是不同事实。Connector Health 不等于 Source Health，Source Health 不等于 Device Health；Connection Test 不等于持续数据流健康；Mapping 变更不改写历史；Secret 不进入浏览器；Unknown 保持 Unknown。**

---

# 80. Information Architecture

```text
Context Header
↓
Integration Portfolio Summary
↓
Integration Ledger
                         → Integration Inspector
↓
Integration Detail
  Overview
  Endpoint / Security
  Authentication / Authorization
  Capabilities
  Mapping
  Live Sync
  Historical Sync
  Health / Errors
  Credential / Certificate
  Tests
  Versions / Change
  Audit
↓
31 Data Quality / 32 Semantic Model / 33 Rules / 25–28 Control
```

默认是 Ledger-first，不做 connector 卡片墙。

---

# 81. Route / URL State Contract

```text
/settings/integrations
/settings/integrations/:integrationId
```

Search Params 可包括：

```text
siteId
integrationType
protocol
state
health
authState
capability
mappingState
syncState
owner
environment
q
revision
```

Integration identity 进入 Path；筛选进入 Search Params。

---

# 82. Capability Gating

34 可见条件：

```text
Integration Management capability exists
AND principal may discover it
```

具体 action 还需要：

```text
action permission
AND integration supports capability
AND environment permits action
AND security policy permits action
```

无 write capability 时不显示假的 Write / Control toggle。

---

# 83. Integration Ledger Contract

默认列：

```text
集成
来源系统
协议 / 类型
Scope
运行状态
认证 / 授权
能力
实时同步
历史同步
映射
最近成功
Owner
```

异常优先可显示：

```text
Degraded
Credential Expiring
Authorization Changed
Schema Drift
Lagging
Backlogged
Mapping Invalid
```

---

# 84. Integration Inspector Contract

快速 Inspector 显示：

```text
Identity
Source / Endpoint summary
Runtime State
Authentication
Authorization summary
Capabilities
Sync Lag / Backlog
Mapping Revision / State
Credential expiry
Last Success
Current Errors
Owner
```

不在 Inspector 回显 Secret。

---

# 85. Integration Detail Layout

推荐：

```text
概览
连接与安全
认证与授权
能力
映射
同步
历史数据
健康与错误
测试
版本与变更
审计
```

Progressive disclosure 到 Advanced protocol detail。

---

# 86. Health Visualization Contract

不做：

```text
Integration Health 87
```

而显示分层事实，例如：

```text
Connector Runtime      正常
Endpoint Reachability  正常
Authentication         正常
Authorization          只读
Realtime Sync          延迟 9 min
Historical Sync        正常
Mapping                2 项需复核
Data Quality           进入 31 查看
```

---

# 87. Test Workspace Contract

Test Connection 页面显示每一步：

```text
1 Endpoint
2 TLS / SecureChannel
3 Application Auth
4 User Auth
5 Authorization Scope
6 Capability Discovery
7 Safe Sample Read
```

每一步有独立 Result / Evidence / Timestamp。

不会只显示：

```text
连接成功
```

---

# 88. Error / Incident Workspace Contract

当前异常聚合：

```text
Integration
Stage
Error Category
Affected Capability
Affected Scope
Started At
Last Seen
Retry State
Business / Data Impact
Owner
```

可以 deep-link：

```text
31 Data Quality
32 Semantic Mapping
10 Diagnosis
25 / 28 Control Execution
```

---

# 89. Empty / Partial / Unknown / Error States

## Empty

没有 Integration 是合法 Empty。

## Partial

例如 Connector health 可读，但 source authorization endpoint unavailable。

## Unknown

例如 source 不提供 device physical health。

## Error

Integration service 请求失败明确显示 Request Failed。

禁止：

```text
service error → no integrations
unknown source state → healthy
unknown device state → offline
```

---

# 90. Accessibility / Responsive Contract

- Health 不只靠颜色；
- Authentication / Authorization / Capability 有文字状态；
- Error stage 和 impact 可由 screen reader 理解；
- Mapping validation result 可键盘访问；
- Test stepper 有 textual alternative；
- 768px 下仍能完成 integration lookup、查看 health、error、credential expiry、mapping state 和 test result；
- Secret 永不因窄屏折叠策略被意外展示。

---

# 91. Wireframe Intent

```text
┌────────────────────────────────────────────────────────────────────────────┐
│ 集成管理                                             生产环境              │
├────────────────────────────────────────────────────────────────────────────┤
│ 异常 3   凭证即将到期 1   映射待复核 2   同步延迟 1                     │
├────────────────────────────────────────────────────────────────────────────┤
│ 集成                  协议      认证    授权     同步      映射           │
│ 中央冷站 OPC UA        OPC UA    正常    只读     延迟9m    正常           │
│ EG8200 遥测            MQTT      正常    订阅     正常      2项复核        │
│ Utility Billing API    REST      正常    账单读   正常      正常           │
│ CMMS                    REST      正常    工单读写 限流中    正常           │
├───────────────────────────────────────────────┬────────────────────────────┤
│ 中央冷站 OPC UA                              │ 快速检查                   │
│ Source: Plant OPC UA Server                  │ Revision rev7              │
│ Endpoint: opc.tcp / SecurityPolicy...        │ Connector: Healthy         │
│ Authentication: Application + User OK        │ Source: Reachable          │
│ Authorization: Read / Subscribe              │ Device Health: Unknown     │
│ Write / Method: Denied                       │ Data Quality: [查看31]     │
│ Realtime: Lag 9 min · Backlog 2,418          │ Credential expires 23 d    │
│ Mapping: MAP-17 rev9 · Valid                 │ [运行连接测试]             │
├────────────────────────────────────────────────────────────────────────────┤
│ 当前问题                                                                    │
│ Realtime subscription lagging since 14:32                                  │
│ Connector process healthy · Auth healthy · Source reachable                │
│ Impact: 128 telemetry points freshness risk → 查看数据质量                 │
└────────────────────────────────────────────────────────────────────────────┘
```

---

# 92. Browser Acceptance Criteria

## Identity / Security

- Integration / Connector Definition / Runtime Instance 分开；
- Endpoint / Source System 分开；
- Authentication / Authorization 分开；
- Authorization scope 可见；
- Secret 不进入浏览器；
- Certificate trust / expiry 分开。

## Capability

- read / history / subscribe / write / command 明确分开；
- read auth 不冒充 write auth；
- write capability 不冒充 Control Authority；
- unsupported capability 不显示可执行按钮。

## Health

- Connector / Source / Device / Data Quality 分开；
- Network / Auth / Sync / Mapping health 分层；
- Connected 不冒充 Synchronized；
- Last Success 不冒充 Current Health；
- Unknown 不显示 Healthy / Offline。

## Sync

- Live / Historical 分开；
- Running / Caught Up 分开；
- Lag / Freshness 分开；
- Backlog / Data Loss 分开；
- retry 有边界；
- control write 不使用 generic auto-retry。

## Mapping

- Source ID / Canonical ID 分开；
- mapping 有 revision / effective period；
- unit / transform 显式；
- mapping change 不重写历史；
- mapping publish 不自动声明 downstream recomputed。

## Change / Audit

- Integration Revision immutable；
- credential / auth scope / mapping / protocol change 可审计；
- cutover / rollback 有明确 target；
- rollback requested 不显示 restored；
- high-risk control capability change 有额外 review。

## Implementation Integrity

- 浏览器不直连 OT system；
- 无 plaintext secret；
- 无 alternate-source silent fallback；
- 无 legacy connector compatibility adapter；
- review scenario 无 runtime / network error。

---

# 93. Explicit Non-goals

34 不是：

- Secret Vault；
- Asset Registry；
- Semantic Ontology Editor；
- Data Quality issue owner；
- Generic ETL builder；
- Full API management platform；
- SIEM；
- Direct BACnet / OPC UA explorer for operators；
- Control Command Console；
- Network topology management system；
- Browser protocol debugger。

---

# 94. READY FOR WIREFRAME Decision

本 Surface 达到 READY FOR WIREFRAME 的条件：

- Integration / Connector Definition / Runtime Instance 分离；
- Endpoint / Source System 分离；
- Authentication / Authorization 分离；
- Read / Write / Command capability 分离；
- Connector / Source / Device / Data Quality health 分离；
- Connection Test / Continuous Health 分离；
- Live Sync / Historical Sync / Backfill / Replay 分离；
- Sync Lag / Freshness 分离；
- Mapping / Identity 分离；
- Source ID / Canonical ID 分离；
- Mapping Revision + Effective Date 明确；
- Current Mapping / Historical Mapping 分离；
- Credential Reference / Secret 分离；
- Certificate Trust / Expiry / Authorization 分离；
- bounded retry / backpressure / dead-letter 明确；
- Control retry boundary 明确；
- Cutover / Rollback lifecycle 明确；
- 31 / 32 / 33 / 25–28 / 36 责任边界明确；
- Security Boundary 明确；
- No Defensive Programming 明确；
- 中文产品语言明确；
- Browser Acceptance Criteria 明确。

**当前决定：SELECTED / READY FOR WIREFRAME。**
