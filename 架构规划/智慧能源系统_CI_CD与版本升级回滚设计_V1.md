# 智慧能源系统 CI/CD 与版本升级回滚设计

> 版本：V1.0  
> 技术基线：Go + React + PostgreSQL + ClickHouse + Redis + MQTT + Edge Gateway  
> 适用对象：研发、测试、DevOps、SRE、Edge研发、实施、发布管理  
> 核心目标：建立“可重复构建、自动验证、渐进发布、数据库兼容、可快速回滚、全程可审计”的交付体系

---

# 1. 设计目标

智慧能源系统不是单一 Web 应用，而是由多类组件共同组成：

```text
React Frontend
Go API
Go IoT Service
Go Telemetry Worker
Edge Gateway
PostgreSQL
ClickHouse
Redis
MQTT
Config Center
Device Template
OTA Package
```

任何一次版本发布都可能影响：

```text
设备采集
数据质量
历史写入
告警
控制
页面
数据库
Edge现场
```

因此 CI/CD 的目标不是“把代码部署上去”，而是：

```text
Build
→ Test
→ Package
→ Validate
→ Deploy
→ Observe
→ Promote
→ Rollback
```

---

# 2. 发布原则

必须遵守：

```text
Artifact Immutable
Environment Promotion
Backward Compatible First
Canary Before Full
Database Expand-Migrate-Contract
No Manual Hotfix Without Audit
Rollback Must Be Designed Before Release
```

---

# 3. 环境模型

推荐：

```text
Development
Testing
Staging
Production
```

---

# 4. Development

用途：

```text
本地研发
单元测试
接口联调
Simulator
```

允许：

```text
快速迭代
自动Migration
测试数据重置
```

---

# 5. Testing

用途：

```text
CI Integration
Contract Test
E2E
Migration Test
```

特点：

```text
每次Merge自动部署
使用模拟设备
不接真实生产设备
```

---

# 6. Staging

用途：

```text
Release Candidate
性能
故障注入
真实Meter
生产前演练
```

尽量接近 Production：

```text
相同镜像
相同配置结构
相同Migration
相同ClickHouse DDL
```

---

# 7. Production

原则：

```text
Only Promoted Artifact
```

禁止：

```text
重新编译
现场改代码
手工替换二进制
```

---

# 8. Monorepo CI

推荐仓库：

```text
smart-energy/
├── backend/
├── frontend/
├── edge/
├── simulator/
├── contracts/
├── deploy/
├── docs/
└── tests/
```

CI 根据变更路径执行不同 Pipeline。

---

# 9. 变更影响识别

例如：

```text
backend/**
→ Go Pipeline

frontend/**
→ React Pipeline

edge/**
→ Edge Pipeline

contracts/**
→ Contract Validation

postgres/**
→ PostgreSQL Migration Pipeline

clickhouse/**
→ ClickHouse DDL Pipeline
```

---

# 10. 主分支策略

建议：

```text
main
```

始终保持：

```text
可发布
```

功能开发：

```text
feature/*
```

修复：

```text
fix/*
```

紧急：

```text
hotfix/*
```

---

# 11. Pull Request Gate

PR 必须通过：

```text
Format
Lint
Unit Test
Contract Validate
Build
Security Scan
Migration Static Check
```

关键模块额外：

```text
Integration
E2E
Safety Negative Test
```

---

# 12. Go CI

执行：

```text
go fmt
go vet
static analysis
unit test
race test for selected modules
integration test
build
```

产出：

```text
energy-api
iot-service
telemetry-worker
edge-gateway
```

---

# 13. Go Build

必须：

```text
CGO policy explicit
version injection
commit SHA
build time
```

二进制应支持：

```text
--version
```

输出：

```text
version
git_commit
build_time
schema_compatibility
```

---

# 14. React CI

执行：

```text
install locked dependencies
lint
typecheck
unit test
build
bundle check
```

产物：

```text
immutable static artifact
```

---

# 15. Frontend Version

前端展示：

```text
Frontend Version
Backend Version
API Version
```

便于故障定位。

---

# 16. Edge CI

Edge 除普通 Go 测试外，还需要：

```text
protocol unit test
golden decode test
template compatibility test
store-forward test
command safety test
cross compile
```

目标平台：

```text
linux/amd64
linux/arm64
```

---

# 17. Edge Build Artifact

产物：

```text
edge-gateway
manifest.json
checksum
signature
release-notes
```

---

# 18. Device Template CI

每次 Template 变更：

```text
Schema Validate
Point Standard Validate
Unit Validate
Duplicate Validate
Golden Test
Compatibility Test
```

失败：

```text
禁止合并
```

---

# 19. OpenAPI CI

检查：

```text
YAML Syntax
OpenAPI Schema
Breaking Change
Response Model
Operation ID
```

---

# 20. MQTT Schema CI

检查：

```text
JSON Schema
Example Validation
Backward Compatibility
Required Field Changes
```

---

# 21. 数据库 Migration CI

PostgreSQL：

```text
fresh install
upgrade from previous
rollback where supported
schema diff
integration test
```

---

# 22. PostgreSQL Migration 原则

Migration：

```text
append-only
```

已发布 Migration 禁止修改。

例如：

```text
000023_add_control_policy.sql
```

上线后不能改原文件。

修复：

```text
000024_fix_control_policy.sql
```

---

# 23. Expand-Migrate-Contract

所有破坏性数据库变更必须三阶段。

## Expand

先增加兼容结构：

```text
new column
new table
new index
```

旧代码仍能运行。

## Migrate

新旧字段并存：

```text
dual read / dual write
backfill
```

## Contract

确认所有应用切换后：

```text
remove old field
remove old index
```

---

# 24. 禁止直接 Rename

例如：

```text
device.name
→ device.display_name
```

禁止一次 Migration 直接 rename 后同时发新服务。

推荐：

```text
add display_name
backfill
dual compatibility
switch
drop name later
```

---

# 25. 大表 Migration

禁止在生产高峰：

```text
长时间锁表
```

需要：

```text
online strategy
batch backfill
monitor lock
```

---

# 26. PostgreSQL Rollback

数据库 Rollback 分为：

```text
Application Rollback
Schema Forward Fix
Data Restore
```

优先：

```text
Application Rollback
+
Backward-compatible Schema
```

不依赖逆向 DDL。

---

# 27. 为什么不依赖 DOWN Migration

生产中：

```text
DROP COLUMN
DELETE DATA
```

往往不可逆。

因此：

```text
DOWN
```

主要用于：

```text
Development / CI
```

生产优先：

```text
Forward Fix
```

---

# 28. ClickHouse DDL Pipeline

检查：

```text
single-node DDL
cluster DDL
syntax
table existence
materialized view
distributed table
TTL
storage policy reference
```

---

# 29. ClickHouse Schema Evolution

优先：

```text
ADD COLUMN
CREATE NEW TABLE
CREATE NEW VIEW
```

谨慎：

```text
mutation
mass update
mass delete
```

---

# 30. ClickHouse ORDER BY 变更

ORDER BY 是重大结构变化。

不能：

```text
ALTER简单完成
```

通常应：

```text
create new table
backfill
dual write
switch query
retire old
```

---

# 31. ClickHouse 表版本化

复杂演进可以：

```text
telemetry_numeric_v1
telemetry_numeric_v2
```

通过 Repository / View 隔离底层变化。

---

# 32. ClickHouse MV 变更

Materialized View 修改：

```text
create new target
create new MV
validate
switch query
remove old
```

避免直接破坏正在写入的聚合链。

---

# 33. Contract First

顺序：

```text
Contract
→ Backend
→ Edge / Frontend
```

例如新增 MQTT 字段：

```text
先让Cloud兼容
再升级Edge
最后启用新功能
```

---

# 34. API Backward Compatibility

至少支持：

```text
N
N-1
```

客户端版本。

破坏性 API：

```text
/api/v2
```

---

# 35. MQTT Backward Compatibility

Edge 数量大，升级慢。

Cloud 必须支持：

```text
MQTT Schema N
MQTT Schema N-1
```

一段迁移期。

---

# 36. Edge 升级不能与 Cloud 强绑定

禁止：

```text
Cloud发布
→ 所有Edge必须立即升级
```

必须允许：

```text
Mixed Fleet Version
```

---

# 37. Release Artifact

每次 Release 生成：

```text
release_id
version
git_commit
build_manifest
images
binaries
schemas
migration versions
release notes
checksums
SBOM
```

---

# 38. Artifact Registry

保存：

```text
Docker Image
Frontend Bundle
Edge Binary
OTA Package
```

不使用：

```text
latest
```

作为生产唯一标签。

推荐：

```text
v1.8.3
git-abc123
```

---

# 39. Image Digest

生产部署应记录：

```text
image digest
```

而不是只记：

```text
tag
```

---

# 40. Release Manifest

示例：

```yaml
release: "2026.08.09.1"

backend:
  api: "1.8.0"
  iot: "1.8.0"
  telemetry_worker: "1.8.0"

frontend:
  version: "1.7.4"

edge:
  supported: ">=1.5.0"

postgres_schema:
  migration: 24

clickhouse_schema:
  version: 7

mqtt_schema:
  version: "1.1"

device_template_schema:
  version: "1.0"
```

---

# 41. Release Candidate

Staging 通过后产生：

```text
RC
```

例如：

```text
v1.8.0-rc.2
```

Production 不重新 Build。

将同一 Artifact：

```text
Promote
```

进入 Production。

---

# 42. 部署顺序

一般推荐：

```text
Backward-compatible DB Expand
↓
Backend
↓
Telemetry Worker
↓
Frontend
↓
Edge Canary
↓
Config Enable
↓
DB Contract Later
```

---

# 43. 后端 Canary

可按：

```text
instance percentage
tenant
site
```

灰度。

---

# 44. Blue/Green

适用于：

```text
API
Frontend
Stateless Worker
```

方案：

```text
Blue = Current
Green = New
```

验证后切流量。

---

# 45. Rolling Deployment

对于普通 Go Service：

```text
逐实例替换
```

要求：

```text
readiness
graceful shutdown
connection drain
```

---

# 46. Telemetry Worker 发布特殊性

Telemetry Worker 是数据热路径。

发布时必须监控：

```text
queue depth
processing lag
duplicate
ClickHouse write
Redis update
```

---

# 47. Worker Rolling Upgrade

必须避免：

```text
旧Worker和新Worker重复消费同消息
```

需明确：

```text
consumer group
partition ownership
MQTT subscription strategy
```

---

# 48. MQTT Consumer 切换

如果使用共享订阅：

```text
$share/group/...
```

需保证升级期间消息分配明确。

---

# 49. Frontend 发布

Frontend 可：

```text
Blue/Green
or
immutable static deployment
```

---

# 50. 前后端兼容

发布期间可能：

```text
New Frontend
+ Old Backend
```

或反之。

必须定义：

```text
compatibility window
```

---

# 51. Edge OTA

Edge 发布流程：

```text
Build
↓
Sign
↓
Upload
↓
Canary
↓
Download
↓
Verify
↓
Stage
↓
Switch
↓
Health
↓
Commit
```

失败：

```text
Rollback
```

---

# 52. Edge OTA 分批

推荐：

```text
1 gateway
↓
1%
↓
5%
↓
20%
↓
50%
↓
100%
```

---

# 53. Edge Canary 选择

优先：

```text
实验Site
非关键设备
典型硬件型号
典型网络
```

不要只选择：

```text
最稳定环境
```

---

# 54. Edge OTA Health Gate

升级后检查：

```text
process alive
mqtt connected
config loaded
device online
poll success
queue normal
cpu/memory
```

---

# 55. Edge 自动回滚

触发：

```text
cannot start
crash loop
health timeout
config incompatible
```

---

# 56. Edge 版本回滚

必须保留：

```text
current
previous
```

至少一个稳定版本。

---

# 57. Edge Firmware Compatibility

发布前检查：

```text
Gateway Hardware
OS
Architecture
Template Schema
MQTT Schema
Device Firmware
```

---

# 58. Config Center 与 CI/CD

代码发布：

```text
CI/CD
```

配置发布：

```text
Config Center
```

两者必须关联：

```text
software compatibility
schema compatibility
release manifest
```

---

# 59. Feature Enablement

推荐：

```text
Deploy Code First
↓
Feature Flag Off
↓
Verify
↓
Canary Enable
↓
Full Enable
```

---

# 60. Kill Switch

高风险功能必须有：

```text
disable_remote_control
disable_auto_optimization
disable_new_rule_engine
```

等紧急关闭能力。

---

# 61. Release Window

生产发布必须支持：

```text
Maintenance Window
Blackout Window
```

---

# 62. Blackout Window

例如：

```text
用电高峰
月度结算
大型活动
关键生产班次
```

禁止非紧急变更。

---

# 63. Change Freeze

特殊时期：

```text
重大节假日
客户保供
关键生产期
```

设置：

```text
Change Freeze
```

---

# 64. 发布审批

生产发布至少：

```text
Developer
Tester
Release Approver
```

高风险控制功能增加：

```text
Safety Reviewer
```

---

# 65. Release Checklist

发布前：

```text
CI PASS
Security PASS
Migration PASS
E2E PASS
Performance Smoke PASS
Backup Confirmed
Rollback Plan Ready
Monitoring Ready
On-call Ready
```

---

# 66. Backup Before Change

涉及：

```text
Database
Critical Config
Control
```

的高风险变更前确认：

```text
recent valid backup
```

---

# 67. Smoke Test

发布后立即：

```text
Login
Site API
Device API
Latest Telemetry
History
MQTT Ingestion
ClickHouse Write
Redis Latest
```

---

# 68. Energy Smoke

检查：

```text
Current Load
Today Energy
```

不出现明显异常。

---

# 69. Control Smoke

生产控制 Smoke 不应随意对真实高风险设备操作。

可使用：

```text
Simulator
Test Device
No-op / Preview
```

---

# 70. Release Observability

发布过程中单独 Dashboard：

```text
API error
latency
CPU
memory
MQTT
Telemetry lag
ClickHouse write
Redis errors
alarm count
command failure
```

---

# 71. Deployment Marker

发布时向 Observability 写：

```text
deployment event
```

Dashboard 可以标记：

```text
v1.8.0 deployed
```

便于关联异常。

---

# 72. Automatic Promotion

只有满足：

```text
error rate normal
latency normal
telemetry lag normal
device online normal
```

才自动进入下一批。

---

# 73. Automatic Pause

如：

```text
5xx ×3
telemetry lag > threshold
device offline increase
command failure increase
```

自动暂停。

---

# 74. Rollback Trigger

分：

```text
Automatic
Manual
```

---

# 75. 自动 Rollback 适用

适合：

```text
Stateless API
Frontend
Edge软件启动失败
```

---

# 76. 不适合自动 Rollback

例如：

```text
Safety Policy tightening
Irreversible data migration
Settlement data correction
```

必须人工判断。

---

# 77. 应用回滚

原则：

```text
New Schema
must support Old App
```

这样应用可以快速：

```text
v1.8
→ v1.7
```

---

# 78. 数据回滚

数据变更不能简单：

```text
rollback code
```

必须根据：

```text
backup
audit
revision
forward fix
```

恢复。

---

# 79. PostgreSQL 数据恢复

可能：

```text
Point-in-time Recovery
Logical Correction
Backup Restore
```

由灾备方案定义。

---

# 80. ClickHouse 数据回滚

通常：

```text
new table
new revision
restore partition
backup restore
```

而不是对 PB 数据做大规模 UPDATE。

---

# 81. Metric 回滚

Metric Formula 出错：

```text
disable new version
restore previous active version
recalculate affected periods
```

---

# 82. Alarm Rule 回滚

```text
new rule inactive
previous active
```

并保留新规则在错误期间产生的 Audit。

---

# 83. Device Template 回滚

```text
Desired Template Version
→ Previous Released Version
```

Edge 应支持：

```text
apply previous
```

---

# 84. 数据语义变更 Rollback

例如：

```text
power sign
multiplier
unit
```

属于危险变更。

需同时处理：

```text
Template
Historical Data
Metric
Alarm
```

不能只回滚 Template。

---

# 85. Release Notes

每个 Release：

```text
Added
Changed
Fixed
Deprecated
Security
Migration
Compatibility
Known Issues
Rollback Notes
```

---

# 86. Breaking Change

必须显式标记：

```text
BREAKING
```

并给出：

```text
migration path
compatibility period
rollback boundary
```

---

# 87. SBOM

建议生成：

```text
Software Bill of Materials
```

记录：

```text
Go modules
npm packages
OS base image
```

---

# 88. Dependency Scan

检查：

```text
known vulnerabilities
license
outdated critical package
```

---

# 89. Secret Scan

CI 必须阻止：

```text
private key
password
token
certificate secret
```

进入 Git。

---

# 90. Container Security

检查：

```text
non-root
minimal base image
read-only where possible
no unnecessary package
```

---

# 91. Artifact Signing

关键制品：

```text
Container Image
Edge Binary
OTA Package
```

建议签名。

---

# 92. Provenance

Release 应能追溯：

```text
source commit
builder
time
pipeline
artifact digest
```

---

# 93. Production Access

生产部署权限：

```text
最小权限
```

研发人员不应默认拥有：

```text
直接SSH改生产
```

---

# 94. Break-glass

紧急访问：

```text
Break-glass
```

需要：

```text
MFA
reason
expiry
audit
```

---

# 95. 手工变更检测

生产配置与 Git/Config Center 不一致：

```text
Drift
```

必须检测。

---

# 96. Infrastructure as Code

基础设施尽量：

```text
declarative
```

例如：

```text
Compose
Terraform
Kubernetes YAML
```

避免口头配置。

---

# 97. Dev Compose

第一阶段：

```text
Docker Compose
```

统一本地环境。

---

# 98. Kubernetes 引入条件

只有出现：

```text
服务数量增长
高可用
自动扩缩
多环境复杂
```

再引入。

不作为第一阶段强制。

---

# 99. Redis 发布

Redis Key Schema 变化应：

```text
versioned
```

不要直接破坏旧 Key。

例如：

```text
point:last:v2:{point_id}
```

过渡后删除旧版本。

---

# 100. MQTT Broker 变更

必须测试：

```text
connection
ACL
TLS
retained
QoS
shared subscription
reconnect storm
```

---

# 101. Broker Upgrade

先：

```text
Staging
```

再：

```text
Canary cluster/node
```

验证 Gateway 自动重连。

---

# 102. ClickHouse Upgrade

升级前：

```text
backup
compatibility
release notes
staging
replica test
```

生产集群逐节点进行时必须保证：

```text
replica compatibility
```

---

# 103. Keeper Upgrade

Keeper 属于协调关键组件。

要求：

```text
quorum remains available
one node at a time
```

---

# 104. PostgreSQL Upgrade

Major Upgrade：

```text
单独项目
```

需要：

```text
compatibility test
backup
restore drill
cutover plan
rollback boundary
```

---

# 105. Release Calendar

维护：

```text
Release Train
```

例如：

```text
Weekly Normal Release
Monthly Platform Release
Emergency Hotfix Anytime
```

---

# 106. Hotfix

流程仍必须：

```text
branch
test
build
artifact
approve
deploy
audit
```

不能：

```text
SSH直接改
```

---

# 107. Hotfix 后处理

Hotfix 必须：

```text
merge back main
update release notes
post review
```

---

# 108. Release Ownership

每次发布明确：

```text
Release Owner
Test Owner
Rollback Owner
On-call Owner
```

---

# 109. On-call

重大生产发布期间必须：

```text
有人可响应
```

并知道：

```text
怎么停
怎么回滚
怎么查日志
怎么恢复
```

---

# 110. Release Runbook

标准流程：

```text
Precheck
Backup
Deploy
Smoke
Observe
Promote
Complete
```

失败：

```text
Pause
Rollback
Verify
Incident
```

---

# 111. CI Pipeline 推荐阶段

```text
Checkout
↓
Dependency Restore
↓
Lint
↓
Unit
↓
Contract Validate
↓
Build
↓
Security Scan
↓
Integration
↓
Package
↓
Artifact Sign
↓
Publish
```

---

# 112. CD Pipeline 推荐阶段

```text
Select Artifact
↓
Compatibility Check
↓
Migration Expand
↓
Deploy Canary
↓
Smoke
↓
Observe
↓
Promote Batch
↓
Full Rollout
↓
Post Check
↓
Release Complete
```

---

# 113. CI 缓存

允许：

```text
Go module cache
npm cache
Docker layer cache
```

但不能牺牲：

```text
reproducibility
```

---

# 114. Lock File

Frontend：

```text
必须提交lock file
```

保证依赖一致。

---

# 115. Go Dependency

```text
go.mod
go.sum
```

必须受版本控制。

---

# 116. Reproducible Build

同一：

```text
commit
```

应能生成相同逻辑版本的制品。

---

# 117. Test Matrix

Go：

```text
unit
integration
race selected
```

Edge：

```text
amd64
arm64
```

Frontend：

```text
build
browser smoke
```

---

# 118. E2E Pipeline

至少：

```text
Simulator
→ MQTT
→ IoT
→ Worker
→ Redis
→ ClickHouse
→ API
```

---

# 119. Control E2E

只在：

```text
Simulator / Test Device
```

执行：

```text
Command
→ Edge
→ Readback
→ Verified
```

---

# 120. Migration E2E

测试：

```text
Previous Release DB
↓
Apply New Migration
↓
Run Old App
↓
Run New App
```

验证兼容窗口。

---

# 121. Performance Gate

每个大版本：

```text
5k
10k
20k values/s
```

至少做 Smoke。

重大数据路径改动做：

```text
24h endurance
```

---

# 122. Performance Regression

如果：

```text
Telemetry Lag
CPU
Memory
Query P95
```

明显回退：

```text
block release
```

---

# 123. Release SLO

发布过程：

```text
No critical data loss
No command audit loss
No uncontrolled downtime
```

---

# 124. P0/P1 Gate

Production 发布禁止存在：

```text
P0 open
P1 open
Critical Security open
Critical Safety open
```

---

# 125. 发布完成条件

不是：

```text
Deployment succeeded
```

而是：

```text
Deployment succeeded
+
Smoke passed
+
SLO stable
+
Telemetry healthy
+
No critical alarm increase
+
Rollback window observed
```

---

# 126. Rollback Window

发布后至少观察：

```text
15min
30min
1h
```

具体由风险级别决定。

---

# 127. Release Risk Level

定义：

```text
R1 Low
R2 Medium
R3 High
R4 Critical
```

---

# 128. R1

例如：

```text
UI文案
```

可快速全量。

---

# 129. R2

例如：

```text
普通API功能
```

需 Canary。

---

# 130. R3

例如：

```text
Telemetry Pipeline
DB Migration
Alarm Engine
```

需要：

```text
Staging
Canary
Observation
Rollback Plan
```

---

# 131. R4

例如：

```text
Control
Safety
Edge OTA大量设备
```

需要：

```text
双审批
明确窗口
现场/On-call
逐批发布
```

---

# 132. 生产 Gate 自动化

Pipeline 自动检查：

```text
artifact approved
migration status
backup status
health
blackout window
change freeze
approval
```

---

# 133. Release Audit

记录：

```text
release_id
artifact
commit
who
approver
when
targets
migration
result
rollback
incident
```

---

# 134. Metrics

CI：

```text
build duration
test pass rate
flake rate
```

CD：

```text
deployment frequency
lead time
change failure rate
rollback rate
MTTR
```

---

# 135. 智慧能源特有交付指标

增加：

```text
Telemetry Regression Rate
Gateway Upgrade Success Rate
Config Drift Rate
Command Failure Rate after Release
Data Quality Regression
```

---

# 136. Release Dashboard

展示：

```text
Current Production Version
Previous Version
Deployment Progress
Target Health
Telemetry Lag
Device Online
Error Rate
Rollback Status
```

---

# 137. 第一阶段实现范围

P0：

```text
Go CI
React CI
Edge CI
OpenAPI/JSON Schema Validate
PostgreSQL Migration CI
ClickHouse DDL CI
Docker Image Build
Artifact Registry
Staging Deploy
Production Manual Approval
Basic Canary
Rollback
Release Manifest
```

---

# 138. P1

```text
Automated Promotion
Artifact Signing
SBOM
Full Security Scan
Edge OTA Fleet Rollout
Feature Flag Integration
Advanced Release Dashboard
```

---

# 139. 第一阶段不做

```text
复杂Multi-region CD
GitOps全自动生产
多云同步
Service Mesh
```

---

# 140. Milestone D1

## CI Ready

```text
Build
Unit
Lint
Contract
Artifact
```

---

# 141. Milestone D2

## Integration Ready

```text
Migration
ClickHouse
MQTT
Simulator
E2E
```

---

# 142. Milestone D3

## Staging Ready

```text
Same Artifact Promotion
Smoke
Performance
Failure Test
```

---

# 143. Milestone D4

## Production Release Ready

```text
Approval
Canary
Observe
Rollback
Audit
```

---

# 144. Milestone D5

## Edge OTA Ready

```text
Sign
Canary
Batch
Health
Rollback
Fleet Status
```

---

# 145. Production Gate

正式生产发布体系至少达到：

```text
main始终可发布

Artifact不可变

Production不重新Build

Migration自动验证

ClickHouse DDL自动验证

OpenAPI / MQTT Schema自动验证

Staging E2E通过

Canary可执行

Rollback实际演练通过

Edge OTA可回滚

Release全程可审计
```

---

# 146. 与配置中心的边界

```text
CI/CD
负责软件与数据库交付

Config Center
负责运行配置发布
```

两者通过：

```text
Release Manifest
Compatibility Matrix
```

连接。

---

# 147. 与灾备的边界

CI/CD 负责：

```text
变更前确认Backup有效
```

灾备系统负责：

```text
真正Backup / Restore / PITR
```

---

# 148. 与Observability的边界

CI/CD 负责：

```text
Deployment Marker
Health Gate
Automatic Pause
```

Observability 提供：

```text
Metrics
Logs
Traces
SLO
```

---

# 149. 最终发布闭环

```text
Developer
  ↓
Pull Request
  ↓
CI
  ↓
Artifact
  ↓
Testing
  ↓
Staging
  ↓
Release Candidate
  ↓
Production Approval
  ↓
Canary
  ↓
Observe
  ↓
Batch Rollout
  ↓
Full Production
  ↓
Post Check
  ↓
Release Complete
```

异常：

```text
Health Regression
   ↓
Pause
   ↓
Rollback
   ↓
Verify
   ↓
Incident / Review
```

---

# 150. 冻结原则

智慧能源系统 CI/CD 必须坚持：

```text
Build Once
Promote Same Artifact
```

```text
Schema Change
Backward Compatible First
```

```text
Production
No Manual Mutation
```

```text
High Risk
Canary First
```

```text
Edge
Gradual OTA
```

```text
Database
Expand-Migrate-Contract
```

```text
Every Release
Has Rollback Plan
```

```text
Every Production Change
Is Auditable
```

最终目标不是：

```text
“发布速度快”
```

而是：

```text
任何一个版本
从哪段代码构建、
经过哪些测试、
用了哪些Schema、
影响哪些设备、
谁批准、
何时上线、
上线后是否健康、
出了问题如何恢复，
全部清晰可追溯。
```

这才是智慧能源系统可持续研发与生产交付的标准。
