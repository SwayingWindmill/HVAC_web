# Phase 1 Deployment Alignment

## Authority

Phase 1 总体和部署架构以 `架构规划/智慧能源系统部署与运维架构设计.md` 为准。实现采用以下处理规则：

```text
文档要求且缺失       -> MISSING，补齐
文档要求且已有       -> KEEP
实现明显超过当前阶段 -> SIMPLIFY 或 DEFER
与文档冲突           -> 调整实现
仅内部实现更可靠     -> KEEP，但不得上升为一级架构
```

完整矩阵位于 `deploy/platform/phase1/alignment-matrix.v1.json`。文档作用域规则位于 `docs/architecture/document-scope-policy.md`；S0/S3 历史认证与 Local Fixture 文档必须显式声明 `CERTIFICATION_REFERENCE` 或 `LOCAL_FIXTURE`，不得覆盖本文件和总体架构基线。

## Phase 1 canonical deployment

第一阶段正式部署模型固定为 Linux Server + Docker Compose。Kubernetes manifests 可以继续用于历史认证和未来阶段实验，但不得成为 Phase 1 运行、Staging 或 Production Acceptance 的必需依赖。

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

## Known gaps that must remain visible

以下能力当前仍是显式缺口，不能因为架构/部署 Gate 通过而宣称已完成：

- Formal RPO/RTO drill
- Optimization Service runtime

Nginx + Real static image、四环境 contract、单实例 Grafana/Loki/Tempo、OTel 聚合、PostgreSQL WAL/base backup 和 production-safe S0-S5 migration runner 已进入 canonical Phase 1 deployment。迁移器使用精确 allowlist、执行 SQL hash/drift tracking 和运行时 role credential file；fixture/testdata/local bootstrap 不进入生产执行链。RPO/RTO 必须以真实故障时间戳演练证明；Optimization 也必须有真实业务 runtime，不能用占位容器伪装完成。

上传的部署文档对 RPO/RTO 给出的是概念和示例（例如 RPO ≤ 15 min、RTO ≤ 1 hour），没有指定本项目必须采用的最终目标值，因此不能把示例静默固化成生产 SLO；该项需要明确的业务/运维目标后再做带时间戳的正式演练。文档同样只规定 Optimization Service 应独立部署，并未提供足够的优化 API、算法或输入输出契约，所以不能通过一个空 Python 容器把它伪装成已完成能力。

只有剩余 `MISSING` 项获得相应设计输入并完成实现，再通过 Simulator/Formal/Hardware 各自适用的验收后，才能形成完整 Phase 1 Production Deployment Acceptance。
