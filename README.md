# 泉来禾智慧能源平台

本仓库采用单仓库、多运行时结构。根目录负责编排、共享契约、跨领域验证与发布证据；产品源码按 `apps/`、`cmd/`、`modules/`、`services/` 和 `libs/` 分层。`cmd/` 表示可执行进程，`modules/` 表示逻辑领域能力，二者不得再与 `services/` 目录名混为同一概念。

## 系统结构

Phase 1 总体与部署架构以 `docs/architecture/deployment-architecture-v1.md` 与 `docs/architecture/deployment-topology-matrix-v1.md` 为准，工程基线见 `docs/architecture/phase1-overall-architecture.md`：

```text
User
  -> Nginx / Gateway
       -> React
       -> Central Platform
            -> Business / IoT / Telemetry / Control Services
                 -> PostgreSQL / ClickHouse / Redis / MQTT
                      -> Edge Gateway
                           -> OT Devices
```

旁路统一建设 Security、Monitoring、Logging、Tracing、Backup、Deployment 和 Audit。浏览器只访问 Nginx/Platform Gateway；服务间通过版本化 HTTP、事件和所有权契约协作，浏览器不直连模型提供方、数据库、ThingsBoard、MQTT 管理面或内部服务。PostgreSQL 保存平台权威业务/当前状态，ClickHouse 保存历史与分析数据，Redis 仅作为可重建缓存和实时传输层。

Phase 1 canonical deployment 是 **1 Linux Server + Docker Compose**。Application / IoT / Telemetry / Metric / Data / MQTT / Observability 在同一服务器内保持组件、网络与数据边界；现有 Kubernetes/Kustomize 与多服务器资产仅用于后期演进、局部认证和生产形态实验，不属于当前 Phase 1 运行或验收形态。机器可读基线见 `deploy/platform/phase1/architecture-baseline.v1.json` 和 `deploy/platform/phase1/alignment-matrix.v1.json`。

## 目录

```text
apps/hvac-web/        React + Vite Web，包含 Demo 与 Real 两种运行模式
cmd/                  Phase 1 canonical 长运行进程入口，只做启动与组合
modules/              逻辑领域 Owner 与领域数据资产，不代表独立部署
services/             尚待 RC-04 收敛的历史模块，以及明确独立的 workload
libs/                 窄接口跨领域共享库、授权库和基础设施库
contracts/            OpenAPI、事件、数据所有权和设备集成契约
infra/                PostgreSQL、ClickHouse、Centrifugo、MQTT 等基础设施配置
deploy/               Phase 1 Compose、镜像、历史认证资产、发布门禁和切换配置
benchmarks/           Operations Agent 确定性与安全基准
scripts/              构建、测试、审计、契约生成和发布认证入口
docs/                 ADR、领域设计、运维方案、安全与研究文档
semantic/cube/        分析语义层
tools/                模拟器和测试工具
```

## Web 运行模式

- Demo：使用演示数据和本地只读能力，入口为 `apps/hvac-web/src/demo/main.tsx`。
- Real：使用受保护 Shell、站点范围、Platform Gateway API 和实时遥测，入口为 `apps/hvac-web/src/real/main.tsx`。

```bash
npm run dev:demo
npm run dev:real
npm run build:demo
npm run build:real
```

## 源码拓扑

### Canonical executable：`cmd/`

- `energy-api/`：公共 API/BFF 与 Phase 1 内嵌业务 Owner 的默认运行入口。
- `iot-service/`：MQTT 上行、命令执行/验证和 Edge Fleet 的默认运行入口。
- `telemetry-worker/`：Telemetry ingest、Latest/History 与 Energy projection 的默认运行入口。
- `metric-worker/`：Metric 执行与结果投影入口。
- `scheduler/`：耐久任务调度协调入口。
- `maintenance-worker/`：证书、Dead Job、Tenant Retirement 等运维任务执行入口。

### Logical domain owners：`modules/`

- `audit/`：Audit 领域、追加式账本、事务 Inbox，以及显式 owner-split 的 `audit-owner`。
- `alarm/`：Alarm 领域、规则/生命周期、数据库 migrations/testdata，以及显式 owner-split 的 `alarm-owner`。
- `workorder/`：Work Order 领域、生命周期、数据库 migrations/testdata，以及显式 owner-split 的 `work-order-owner`。
- `registry/`：Tenant/Site、Space/Asset、Device/Product/Point 与 Registry read/write 边界，以及显式 owner-split 的 `registry-owner`。
- `iam/`：Principal/Capability/Tenant/Site 授权、Delegation Grant 与 Reconciliation 边界，以及显式 owner-split 的 `iam-owner`。
- `command/`：Cloud Command Intent、Governance、Approval、Dispatch/Verification Authority、PostgreSQL migrations，以及显式 owner-split 的 `command-owner`。
- `telemetry/`：Telemetry ingest、Current/Realtime/History、查询适配与生成合同；默认运行于 `cmd/telemetry-worker`，并提供 `telemetry-query-owner` 和 history projector。
- `metric/`：Metric Version/Binding/Calculation Run/Result/Publication 与 Scheduler Job 执行；默认运行于 `cmd/metric-worker`。
- `energy/`：Energy Processing、MeterBinding 解析、canonical Counter Delta 到 Energy Fact 的投影与 rebuild/correction；默认由 `cmd/telemetry-worker` 组合执行，并保留 `energy-projector` 作为显式构建入口。
- `scheduler/`：耐久任务的扫描、Claim 协调和调度统计；默认运行于 `cmd/scheduler`。
- `maintenance/`：证书到期扫描、Dead Job 处置和 Tenant Retirement 等运维作业；默认运行于 `cmd/maintenance-worker`。
- `iot/`：MQTT ingress、连接状态、Edge Fleet 同步/OTA 传输侧和 `iot-service` 的协议执行面；默认运行于 `cmd/iot-service`。

`modules/*/cmd/*-owner` 只用于显式 owner-split / 同版本故障域验证；默认 Phase 1 仍由 `energy-api` 内嵌这些 Owner。Domain Module 不等于独立 Deployable。

### Independently deployable workloads：`services/`

`services/` 只保留具有独立生命周期、故障域或部署需求的 workload，不再作为逻辑 Domain 的默认源码目录：

- 平台/身份/耐久性：`identity-service/`、`outbox-relay/`。
- Telemetry/Analytics：`settlement-service/`。
- Control/Rules/Delivery：`rule-runtime-service/`、`notification-service/`、`outbound-delivery-service/`。
- Intelligence：`forecast-service/`、`optimization-service/`、`fdd-service/`。
- 智能运维：`operations-agent-service/`，TypeScript 模块化单体，负责授权范围内的调查编排、确定性分析、受控模型调用、业务记录和 AG-UI 投影。

Operations Agent 通过 `energy-api`/Platform Gateway 边界暴露受保护调查接口；模型不能选择工具、扩大 Scope 或提交业务效果。`npm run operations-agent:safety-certification` 统一验证授权负向、精确重试、重启、并发和事件流恢复。

## 契约和所有权

- `contracts/http/`：公共及内部 OpenAPI 契约和生成锁。
- `contracts/events/`：Protobuf 与事件兼容锁。
- `contracts/ownership/`：路由、数据写入权威和切换阶段。
- `contracts/operations-agent/`：Operations Agent Tool Catalog 与可信 Runtime Context Schema。
- `contracts/telemetry/`：遥测兼容性和 IAM 授权规则。
- `contracts/thingsboard/`：设备控制映射。

常用检查：

```bash
npm run repo:check
npm run repo:governance:test
npm run architecture:phase1:check
npm run docs:phase1:consistency:check
npm run data:phase1:check
npm run deployment:phase1:check
npm run domain:matrix:test
npm run operations-agent:contracts:check
npm run contracts:check
npm run events:check
npm run ownership:check
npm run lint
npm run build
```

Operations Agent 的逻辑工具、Owner、Runtime READ 白名单、Tool Authorization 子集、Receipt owner 与可信控制字段以 `contracts/operations-agent/` 为来源。`npm run operations-agent:contracts:generate` 更新 Service、Benchmark 和 Web 生成常量；`operations-agent:contracts:check` 同时校验生成漂移以及公共/内部 OpenAPI 枚举。

领域任务矩阵统一维护 PR、本地和夜间回归使用的命令组合。默认只运行所选领域的单元层；先用 `domain:plan` 查看计划，再按需增加持久化或浏览器层：

```bash
npm run domain:plan -- --domain=operations-agent --layers=unit,integration
npm run domain:run -- --domain=telemetry --layers=contracts,unit
npm run domain:run -- --domain=command --layers=unit,integration
```

支持的领域包括 `web`、`platform`、`registry`、`telemetry`、`command`、`alarm`、`workorder`、`analytics`、`operations-agent` 和 `pocs`。命令及 Profile 的唯一配置源是 `scripts/domain-task-matrix.mjs`。全量回归通过命名集合执行：`all` 覆盖某个 Gate 的全部 Profile，`browser-linux` 和 `browser-windows` 保持浏览器检查的运行平台边界；新增 Profile 未加入这些集合时矩阵会立即失败。

较长但仍需保留公共名称的能力检查也由同一矩阵维护。阶段性的 Runtime/Gateway Snapshot Gate 已退出 active CI；当前保留的是有真实合同、持久化、传输或业务行为价值的能力入口。可在不执行真实测试的情况下查看展开顺序：

```bash
node scripts/run-capability-task.mjs --task=s2:telemetry-ingest --dry-run=true
node scripts/run-capability-task.mjs --task=s3:command-ux --dry-run=true
```

`repo:check` 检查 Git 已跟踪文件，防止日志、本地协调数据、生成产物以及仓库内 `.worktrees/`、`.clones/` checkout 被纳入版本库，并根据当前工作树中实际包含源码文件的 `cmd/`、`modules/`、`services/` 目录校验 README 源码清单一致。Git ignore 只是防止误提交的安全网：Git worktree、上游参考源码和临时 scratch 必须位于产品仓库根目录之外，避免污染 IDE、代码索引、搜索和 Agent 分析。历史 S0/S1 发布证据的活跃引用已迁到 `docs/evidence/go-data-ai-platform*`，`.scratch` 不再承担权威职责。该检查还通过 `scripts/package-script-long-chain-baseline.json` 对根脚本执行棘轮治理：普通旧长链继续冻结，新增长链必须迁入任务矩阵；明确的认证、发布和切换操作可作为 `explicit-operation` 保留显式入口，已迁移能力入口不得回退为 `&&` 命令链。

## 依赖所有权

- 根目录 `package.json`：Web、契约生成、仓库编排和跨层验证。
- `cmd/*/go.mod`：canonical executable 的薄启动模块；RC-04 期间允许临时依赖尚未迁移的实现 module。
- `modules/*/go.mod`：逻辑领域 Owner 的 Go module 与领域数据资产边界。
- `services/*/go.mod`：尚待迁移的历史模块或明确独立 workload 的依赖边界。
- `services/operations-agent-service/package.json`：Operations Agent 独立 Node 依赖。
- `libs/*/go.mod`：共享领域与安全能力的窄模块依赖。
- 根目录 `go.work`：仅负责本地 Go workspace 编排。

设计规则见 `DESIGN.md`，总体架构基线见 `docs/architecture/phase1-overall-architecture.md`，文档作用域与旧认证资产表述规则见 `docs/architecture/document-scope-policy.md`，架构决策见 `docs/adr/`，当前领域上下文见 `CONTEXT.md`。
