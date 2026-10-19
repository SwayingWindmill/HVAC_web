# 第一阶段的部署怎么定

## 依据

第一阶段的总体和部署架构以《智慧能源系统总体部署架构设计 V1（单服务器基线）》（`SE-ARCH-DEPLOY-001 V1.0`）为准。实现和它对照时按这几条处理：

```text
文档要求、实现没有         -> MISSING，补上
文档要求、实现已有         -> KEEP
实现明显超出当前阶段       -> SIMPLIFY 或 DEFER
和文档冲突                 -> 改实现
只是内部实现更可靠         -> KEEP，但不提升为一级架构
```

逐项的对照表在 `deploy/platform/phase1/alignment-matrix.v1.json`（`npm run architecture:phase1:check` 检查）。

## 部署形态

第一阶段固定为一台 Linux 服务器 + Docker Compose。应用、设备接入、遥测、指标、数据、MQTT 和观测在同一台服务器上，但各自是独立的组件，有自己的网络和数据边界。多台服务器和 Kubernetes 只属于以后的拆分，不作为现在的运行、预发或正式验收的部署方式。

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

## 档位

只有一份 Compose，通过 profile 选资源规模。`deploy/platform/phase1/deployment-tiers.v1.json` 定义了 `demo`、`single-lite`、`single-full` 三档（本地和服务器用 `single-lite`）。观测三选一：`observability-core`（Prometheus + node-exporter + Grafana）、`observability-logs`（再加 OTel Collector + Loki）、`observability-full`（再加 Tempo），选了哪个记在环境变量 `PHASE1_OBSERVABILITY_PROFILE` 里。预测、优化、故障诊断等智能服务通过 `intelligence` 可选启用。

## 可用性

第一阶段的可用性等级固定为「单机、可恢复」（`SINGLE_NODE_RECOVERABLE`，`deploy/platform/phase1/availability-tier.v1.json`）。这份文件列出每个单点故障和升级它的代价，并记了 ThingsBoard v4.3.1.1 从单机到多实例的参考做法；这不是承诺现在就做。没有实测数据，不能从单机的恢复目标推出一个可用性百分比。

## 发布流程

```text
Git → CI → 测试 → 构建镜像 → 镜像仓库 → 预发 Compose → 验收 → 人工批准 → 正式 Compose
```

SBOM、来源证明、Cosign 签名、不可变的镜像摘要这些供应链安全措施继续保留，不因为不用 Kubernetes 而降低。

（服务器 139.199.58.47 现在的实际做法更简单：本地从 main 构建镜像、`docker save | ssh docker load` 传过去，见 `docs/HANDOFF.md`。）

## 恢复目标

《SE-OPS-009 V1.0》给出了分层的恢复目标，写在 `deploy/platform/phase1/recovery/recovery-targets.v1.json`：

| 对象 | 最多丢多少数据（RPO） | 多久恢复（RTO） |
| --- | --- | --- |
| PostgreSQL 业务数据 | 5 分钟 | 2 小时 |
| 控制指令和指令记录 | 5 分钟 | 1 小时 |
| 遥测的云端服务 | — | 4 小时 |
| 指标 | 30 分钟 | 4 小时 |
| 整台服务器更换 | — | 4 小时（前提是有冷备机和外部备份） |

**定下目标不等于达到目标。** 每个正式部署都必须用真实的外部备份、在真实的恢复主机上做一次带时间记录的恢复演练：RTO 从「确认服务受影响」开始算，到「关键业务验证完成」结束，不能以容器跑起来为终点。演练要记下实际的 RPO/RTO、控制指令的核对、定时任务和待发事件的积压、网关补发（如果声明了遥测的实际 RPO）和整改的问题。没通过真实演练，不能对现场说目标已经达到。恢复步骤见 `deploy/platform/phase1/recovery/README.md`。

定时任务的 HTTP 管理接口还只是设计，没有作为正式接口；基础设施的备份仍由 systemd timer 单独触发。
