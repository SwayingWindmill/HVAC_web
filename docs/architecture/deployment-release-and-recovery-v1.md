# 部署发布、升级与恢复 V1

状态：PROPOSED / SOURCE-ALIGNED  
范围：单节点当前生产、未来集群、Backend/UI 优先阶段

## 1. Product Release Manifest

一次部署不能只由 Compose 文件和 env 文件定义。每个可部署版本需要一个不可变 Product Release Manifest，至少绑定：

| 字段 | 内容 |
| --- | --- |
| productVersion | 产品版本 |
| releaseRevision | 发布提交或发布修订 |
| topologyId | backend-dev、single-node、cluster-application 等 |
| imageDigests | 每个 Module 的 immutable image digest |
| migrationManifestDigest | 允许执行的 migration 集合与摘要 |
| configRevision | 非 Secret 配置版本 |
| secretReferences | SecretRef 名称和版本，不保存 Secret 内容 |
| deploymentTier | single-lite、single-full 或未来 cluster tier |
| observabilityTier | core、logs、full 或 externalized |
| dataContractVersions | PostgreSQL、ClickHouse、MQTT、Realtime 合同版本 |
| backupPolicyRevision | 备份和保留策略版本 |
| rollbackEvidence | 回滚或恢复前提 |

Manifest 是协调 Module 的发布版本，不成为任何领域事实的 owner。

## 2. 单节点发布流程

~~~text
Source Commit
  ↓
CI / Test / Security Scan
  ↓
Build Image
  ↓
Sign + Push Immutable Digest
  ↓
Create Product Release Manifest
  ↓
Staging Single-node Deploy
  ↓
Schema / Migration Preflight
  ↓
Business Smoke + Recovery Checks
  ↓
Manual Approval
  ↓
Production Single-node Deploy
  ↓
Post-deploy Validation
~~~

生产主机只拉取批准的 digest，不在主机上重新 build。部署前必须确认：

- topologyId 与环境允许；
- image digest 完整；
- migration manifest 与当前 schema 匹配；
- config revision 已审查；
- SecretRef 可解析；
- 备份 readiness 通过；
- 资源 tier 与主机容量匹配；
- observability profile 与拓扑一致。

## 3. 升级原则

本项目不使用运行时 fallback、dual-write 或兼容层掩盖旧架构。升级采取版本化替换：

1. 备份并记录恢复点；
2. 校验目标 release manifest；
3. 执行明确的 migration/preflight；
4. 替换应用 Module；
5. 执行健康、数据、Realtime 和业务 smoke；
6. 记录实际版本和证据。

如果数据库变更不可逆，不能简单把旧 image 换回去作为 rollback。此时 rollback 必须是：

- 恢复数据库和配置快照；
- 恢复旧 Product Release Manifest；
- 重新启动旧版本；
- 重新执行业务验证。

## 4. 集群发布流程

集群形态额外需要：

~~~text
Release Manifest
  ↓
Compatibility / Capacity Gate
  ↓
Canary node
  ↓
Readiness + live journey
  ↓
Rolling replacement
  ↓
Worker lease / outbox / command dedup validation
  ↓
Data-plane failover validation
  ↓
Promotion
~~~

禁止只升级应用节点而不验证 PostgreSQL、ClickHouse、Redis、MQTT 和 Realtime 的实际组合。

## 5. 数据恢复权威

| 数据角色 | 当前 authority | 单节点恢复 | 集群演进 |
| --- | --- | --- | --- |
| Registry/IAM/Command/Scheduler | PostgreSQL | WAL + base backup + PITR | primary/standby + fencing |
| Raw/History/Analytics | ClickHouse accepted fact | restore or rebuild with watermark | replicated tables or rebuild proof |
| Current projection | PostgreSQL head；Redis rebuildable | PostgreSQL rebuild Redis | remove Redis authority dependency before HA |
| Realtime | Telemetry owner + Redis recovery contract | snapshot/history fallback | multi-node transport + recovery drill |
| MQTT transport | Broker session, not business SoT | restart/reconnect; Edge replay if available | stable endpoint + failover proof |
| Report/export/artifact | Object Storage + manifest | restore by verified manifest | replicated/managed object storage |
| Edge local data | Edge Timedata | Edge local recovery | separate Edge fleet recovery |

## 6. 当前恢复目标的使用方式

现有 recovery targets 继续作为 Phase 1 目标定义，但必须区分：

- 目标定义；
- backup readiness；
- 真实 restore drill；
- production attainment。

container running 不能作为 RTO 完成。至少要验证：

- PostgreSQL integrity；
- ClickHouse write/rebuild；
- Redis current rebuild；
- MQTT reconnect；
- telemetry lag；
- metric/energy processing；
- alarm reevaluation；
- command reconciliation；
- scheduler/outbox backlog；
- disk capacity 和 clock sync。

Edge Store and Forward 只有在生产 Edge Host 已部署、保留周期已证明且 replay 已演练时，才可以计入 telemetry effective RPO。

## 7. 集群准入与退出条件

进入 Cluster 前：

- 单节点容量、恢复和故障证据已形成；
- 至少一个具体业务目标不能由单节点满足；
- 已决定哪些 Module 需要多实例；
- PostgreSQL owner/lease/fence 已演练；
- Redis 不再承载不可替代的业务 authority；
- MQTT reconnect/failover 合同已冻结；
- ClickHouse replica 或 rebuild 方案已验证；
- 外部 Observability 和 release control path 可用。

集群不能满足这些条件时，继续优化单节点的 locality、可观测性和恢复效率。

## 8. 不属于当前阶段的发布前置

- Kubernetes；
- Kafka/Redpanda；
- Service Mesh；
- 多 Region；
- 全量 Edge fleet；
- 全量 Forecast/Optimization/FDD 常驻部署；
- 以 HA 取代恢复演练。

