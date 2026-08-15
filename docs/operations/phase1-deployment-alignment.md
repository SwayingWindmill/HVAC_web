# Phase 1 Deployment Alignment

## Authority

Phase 1 总体和部署架构以 `SE-ARCH-DEPLOY-001 V1.0 CURRENT`《智慧能源系统总体部署架构设计 V1（单服务器基线）》为准。实现采用以下处理规则：

```text
文档要求且缺失       -> MISSING，补齐
文档要求且已有       -> KEEP
实现明显超过当前阶段 -> SIMPLIFY 或 DEFER
与文档冲突           -> 调整实现
仅内部实现更可靠     -> KEEP，但不得上升为一级架构
```

完整矩阵位于 `deploy/platform/phase1/alignment-matrix.v1.json`。文档作用域规则位于 `docs/architecture/document-scope-policy.md`；S0/S3 历史认证与 Local Fixture 文档必须显式声明 `CERTIFICATION_REFERENCE` 或 `LOCAL_FIXTURE`，不得覆盖本文件和总体架构基线。

## Phase 1 canonical deployment

第一阶段正式部署模型固定为 1 Linux Server + Docker Compose。Application / IoT / Telemetry / Metric / Data / MQTT / Observability 在同一服务器内保持独立组件、网络和数据边界；多服务器与 Kubernetes 仅属于未来拆分/演进，不得成为当前 Phase 1 运行、Staging 或 Production Acceptance 的部署形态。

目标目录：

```text
deploy/platform/phase1/
├─ architecture-baseline.v1.json
├─ alignment-matrix.v1.json
├─ compose.yaml
├─ nginx/
├─ environments/
├─ observability/
└─ backup/
```

## Delivery rule

Phase 1 发布流程遵循：

```text
Git
 ↓
CI
 ↓
Test
 ↓
Build image
 ↓
Registry
 ↓
Staging Compose
 ↓
Acceptance
 ↓
Manual Approval
 ↓
Production Compose
```

现有 SBOM、provenance、Cosign、immutable digest 等供应链安全能力继续保留。这些能力不会因为取消 Kubernetes 作为 Phase 1 前置而降低。

## Recovery targets and production evidence

Phase 1 架构/部署矩阵当前不再保留 `MISSING`。`SE-OPS-009 V1.0 CURRENT CANDIDATE` 已正式给出分层恢复目标，并由 `deploy/platform/phase1/recovery/recovery-targets.v1.json` 机器化：PostgreSQL `RPO≤5min/RTO≤2h`、Control `RPO≤5min/RTO≤1h`、Telemetry Cloud `RTO≤4h`、Metric `RPO≤30min/RTO≤4h`、Whole Server Replacement `RTO≤4h`（带 Cold Standby / External Backup 等前提）。

Nginx + Real static image、`/realtime/ → energy-api` 公共 Realtime 边界、四环境 contract、Host metrics / 80%+90% disk alert、容器 CPU/Memory 边界、Docker log rotation、可配置宿主机数据目录、PostgreSQL WAL/base backup、Application Scheduler durable Job queue、RPO/RTO recovery contract 和 production-safe S0-S5 migration runner 已进入 canonical Phase 1 deployment。fixture/testdata/local bootstrap 不进入生产执行链。

RPO/RTO **目标定义完成不等于生产达标证明完成**。每个生产部署仍必须使用真实 external backup 和实际恢复主机执行 timestamped Restore Drill；RTO 从“确认影响服务”开始，到关键业务验证完成结束，不能以 Container Running 作为结束点。Drill 必须记录 Actual RPO/RTO、Control reconciliation、Scheduler/Outbox backlog、Edge Replay（若声明 Telemetry effective RPO）及问题整改。未通过真实演练时，不得对现场声明目标已经达成。

Scheduler HTTP 管理 URI 仍是 DESIGN_PROPOSED，不固化为正式 API；基础设施备份继续由 systemd timer / infrastructure scheduler 独立触发。Optimization 保持 DEFER，不作为基础部署缺口。
