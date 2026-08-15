# 架构与部署文档作用域规则

本规则用于避免局部认证、历史实现或未来演进资产反向定义 Phase 1 总体架构。

## 权威顺序

1. `SE-ARCH-DEPLOY-001 V1.0 CURRENT`《智慧能源系统总体部署架构设计 V1（单服务器基线）》：Phase 1 当前总体部署架构来源。
2. `docs/architecture/phase1-overall-architecture.md`：仓库内 L1/L2 总体架构基线。
3. `deploy/platform/phase1/architecture-baseline.v1.json` 与 `deploy/platform/phase1/alignment-matrix.v1.json`：机器可读部署基线与对齐状态。
4. 领域 ADR、operations/security 文档：只约束其声明的领域或认证范围，不得覆盖以上总体基线。

## 文档作用域

### CANONICAL_ARCHITECTURE

描述当前 Phase 1 一级/二级总体架构。不得把 S0–S5、Adapter、Projector、Kubernetes Namespace 等实现细节提升为一级架构。

### CANONICAL_PHASE1_DEPLOYMENT

描述当前 Phase 1 正式部署。基线是 1 Linux Server + Docker Compose；Application/Data/MQTT 等职责必须在同一服务器内保持组件、网络与数据边界。多服务器只属于 FUTURE_STAGE，不是当前 Phase 1 允许形态。

### CERTIFICATION_REFERENCE

用于证明某个领域、安全边界、滚动升级、NetworkPolicy 或目标等价环境。Kubernetes、Kustomize、cert-manager、CSI、Kind 等可以出现在这类文档中，但它们不是 Phase 1 正式部署前置条件。

### LOCAL_FIXTURE

仅用于本地开发、模拟、ThingsBoard/设备 fixture 或确定性集成测试。Local OIDC、虚拟设备、Redpanda 等测试依赖不得被描述成 Production 服务选型。

### FUTURE_STAGE

用于多实例、高可用、集群化、自动扩缩容或多 Region 演进。包括 Kubernetes 作为主编排、MQTT Cluster、PostgreSQL Replica、ClickHouse Cluster、Kafka/Redpanda 平台 backbone 等。

## Kafka / Redpanda 表述规则

S0 历史 durable-session/audit 实现和认证测试可以继续描述 Kafka-compatible backbone 或 Redpanda，因为这些是已存在的工程证据。

但相关文档必须明确：

- 该链路属于 S0 compatibility/certification implementation；
- Redpanda 是本地/认证依赖，不是 Phase 1 Production 服务选型；
- `deploy/platform/phase1/` 默认部署不依赖 Kafka/Redpanda；
- 后续只有在吞吐、Replay 或跨服务事件规模证明有必要时，才重新评估平台消息总线。

## Kubernetes 表述规则

保留的 Kubernetes 文档必须明确其作用域是 certification/reference/local fixture/future stage 中的一种，不得使用“Phase 1 正式部署必须”“Production 必须 Kubernetes”等表述。

`deploy/s0/staging/`、`deploy/s3/target/`、Kind-based S3 local profiles 可以继续存在，但不得成为 `deployment:phase1:check` 的运行前置。

## S0–S5 表述规则

S0–S5 是工程实施、认证和数据库边界编号，不是一级业务架构层级。

总体架构对外使用职责名：Identity/Registry/Telemetry/Energy/Alarm/Command/Work Order/Optimization。只有 L3 实现、migration、测试或认证文档可以使用 S0–S5 作为主要组织方式。

## 一致性验收

运行：

```bash
npm run docs:phase1:consistency:check
```

该 Gate 检查：

- canonical 总体架构和 Phase 1 部署入口存在；
- 保留的 Kubernetes/Redpanda 重点文档声明正确作用域；
- S3 Kubernetes Target 不声称自己是 Phase 1 canonical deployment；
- S0 Redpanda 文档不声称 Redpanda 是 Phase 1 Production 选型；
- README 指向 canonical Phase 1 总体与部署基线。
