# 泉来禾智慧能源平台

本仓库采用单仓库、多运行时结构。根目录负责编排、共享契约、跨服务验证与发布证据；具体产品代码位于 `apps/`、`services/` 和 `libs/`。

## 系统结构

```text
apps/hvac-web
  -> platform-gateway
       -> IAM / Registry / Telemetry / Analytics / Command / Alarm / Work Order
       -> operations-agent-service
```

浏览器只访问 Platform Gateway。服务间通过版本化 HTTP、事件和所有权契约协作；浏览器不直连模型提供方、数据库、ThingsBoard 或内部服务。

## 目录

```text
apps/hvac-web/        React + Vite Web，包含 Demo 与 Real 两种运行模式
services/             独立部署的 Go 服务与 TypeScript Operations Agent
libs/                 窄接口 Go 领域库、授权库和基础设施库
contracts/            OpenAPI、事件、数据所有权和设备集成契约
infra/                PostgreSQL、ClickHouse、Centrifugo、ThingsBoard 等本地拓扑
deploy/               镜像、Kubernetes、发布门禁和切换配置
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

## 服务目录

### 平台入口与身份

- `platform-gateway/`：浏览器公共入口、BFF 安全边界和内部路由编排。
- `iam-service/`：身份、授权、委托和站点访问决策。
- `platform-core-service/`：站点、设备和 Registry 权威数据。
- `audit-ledger-service/`：追加式审计账本和事务 Inbox。
- `outbox-relay/`：事务 Outbox 事件转发。
- `oidc-test-provider/`：确定性本地 OIDC 测试提供方。
- `legacy-migration-service/`：旧 Registry 数据迁移边界。

### 遥测与分析

- `telemetry-runtime-service/`：当前遥测、摄取和历史投影运行时。
- `telemetry-query-service/`：历史遥测与能源分析产品查询。
- `analytics-read-model-projector/`：分析读取模型投影。
- `telemetry-shadow-comparator/`：迁移与切换期间的遥测影子比较。
- `thingsboard-telemetry-adapter/`：ThingsBoard 遥测适配。

### 命令、告警与工单

- `command-service/`：命令意图和治理状态权威服务。
- `command-dispatcher/`：命令派发与结果验证。
- `thingsboard-connector-control/`：ThingsBoard 控制连接器。
- `alarm-service/`：告警读取模型与生命周期。
- `work-order-service/`：工单领域和持久化运行时。

### 智能运维

- `operations-agent-service/`：TypeScript 模块化单体，负责授权范围内的调查编排、确定性分析、LangGraph 只读运行时、业务记录和 AG-UI 投影。

Operations Agent 通过 Platform Gateway 暴露受保护的调查接口。现有实现包含 Registry/Energy 权威读取、PostgreSQL 持久化、只读运行时、首个 Web Operations Workspace，以及受控 Finding 合成接口与 Fake Provider。调用来源、配置摘要、输入输出摘要、延迟和有界计量会与 Finding 原子持久化，但不会进入公共投影。真实外部模型、断线游标恢复和调度器仍是后续工作；模型当前不能选择工具、扩大 Scope 或提交业务效果。

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

支持的领域包括 `web`、`platform`、`registry`、`telemetry`、`command`、`analytics`、`operations-agent` 和 `pocs`。命令及 Profile 的唯一配置源是 `scripts/domain-task-matrix.mjs`。全量回归通过命名集合执行：`all` 覆盖某个 Gate 的全部 Profile，`browser-linux` 和 `browser-windows` 保持浏览器检查的运行平台边界；新增 Profile 未加入这些集合时矩阵会立即失败。

较长但仍需保留公共名称的能力检查也由同一矩阵维护。目前已迁移 12 个 S2/S3 能力入口，包括 Telemetry Baseline、IAM、Runtime、History、Ingest、Gateway、Realtime，以及 Command Safety、Authority、API、ThingsBoard 和 UX。公共 npm 名称保持不变；可在不执行真实测试的情况下查看展开顺序：

```bash
node scripts/run-capability-task.mjs --task=s2:gateway-snapshot --dry-run=true
node scripts/run-capability-task.mjs --task=s3:command-ux --dry-run=true
```

`repo:check` 只检查 Git 已跟踪文件，防止日志、本地协调数据和生成产物进入版本库，并校验服务目录与 README 服务清单一致。它还通过 `scripts/package-script-long-chain-baseline.json` 对根脚本执行棘轮治理：超过四个内联命令的旧链条只能原样保留，新增或修改长链必须迁入任务矩阵；已迁移能力入口不得回退为 `&&` 命令链。基线更新命令 `npm run repo:long-chains:update` 仅用于显式审查后的例外调整。`.worktrees/` 当前仅保持 Git 忽略，不纳入该检查的失败规则。

## 依赖所有权

- 根目录 `package.json`：Web、契约生成、仓库编排和跨层验证。
- `services/operations-agent-service/package.json`：Operations Agent 独立 Node 依赖。
- `services/*/go.mod`：各 Go 服务独立依赖。
- `libs/*/go.mod`：共享领域与安全能力的窄模块依赖。
- 根目录 `go.work`：仅负责本地 Go workspace 编排。

设计规则见 `DESIGN.md`，架构决策见 `docs/adr/`，当前领域上下文见 `CONTEXT.md`。
