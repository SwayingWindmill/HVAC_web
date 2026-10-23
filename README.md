# 泉来禾智慧能源平台

商用暖通空调的智能节能平台，第一个场景是冷站：值班人员在网页上看冷站的实时运行、告警、工单、能耗和电费，AI 助手帮他们解释数据。浏览器端是一个 React 单页应用，后端是 Go；第一阶段部署在一台 Linux 服务器上，用 Docker Compose 运行全部服务。

接手工作先读 `docs/HANDOFF.md`（做到哪了、下一步做什么），规则见 `AGENTS.md`。

## 系统怎么连起来

```text
浏览器
  -> Nginx / 平台网关（energy-api）
       -> 网页（React）
       -> 各业务模块：设备与点位、遥测、告警、工单、控制指令、能耗、智能服务
            -> PostgreSQL / ClickHouse / Redis / MQTT
                 -> 站点网关（例如松雅轩的 Node-RED）
                      -> 冷站设备（电表、温度模块、机组）
```

- 浏览器只访问 Nginx 和平台网关，不直接连数据库、MQTT、内部服务或大模型。
- PostgreSQL 存业务数据和当前状态，ClickHouse 存历史读数和统计，Redis 只做可以重建的缓存和实时推送。
- 服务之间通过有版本的 HTTP 接口和事件协作，接口定义在 `contracts/`。
- 总体架构见 `docs/architecture/phase1-overall-architecture.md`，部署的机器可读描述在 `deploy/platform/phase1/`。

## 目录

```text
apps/hvac-web/   网页，唯一的浏览器入口
cmd/             长期运行的进程入口，只负责启动和组装
modules/         各业务领域的代码和数据库表，默认嵌在 cmd/ 的进程里运行
services/        需要单独部署的服务（身份、智能服务、AI 助手等）
libs/            跨领域共享的小库（授权、数据模型、TLS 等）
contracts/       HTTP 接口、事件、数据归属和设备接入的定义
infra/           数据库初始化脚本、观测配置等基础设施
deploy/          Compose 部署、镜像、站点配置
semantic/cube/   能耗统计的语义层（Cube）
scripts/         构建、测试、检查和代码生成脚本
tools/           冷站模拟器和测试工具
docs/            交接、ADR、规格、架构记录和运维手册
```

### 进程入口：`cmd/`

- `energy-api/`：平台网关。网页的接口、登录会话、权限检查，以及默认嵌在里面的业务模块。
- `connectivity/`：接收站点网关的 MQTT 数据，下发并核对控制指令。
- `telemetry-worker/`：遥测的接收、最新值、历史和实时推送，以及把电表读数算成能耗。
- `metric-worker/`：按计划计算指标（例如每 15 分钟的负荷）。
- `scheduler/`：定时任务的调度。
- `maintenance-worker/`：证书到期检查、失败任务处理等运维作业。

### 业务领域：`modules/`

- `registry/`：租户、站点、空间、设备、点位。
- `iam/`：谁能看、能改哪些站点（角色指派）。
- `audit/`：操作记录，只追加不修改。
- `telemetry/`：设备读数的接收、最新值和历史查询。
- `connectivity/`：站点网关的接入和证书。
- `command/`：控制指令的审批、下发和结果核对。
- `alarm/`：告警规则和告警的发生、确认、恢复。
- `workorder/`：工单。
- `metric/`：指标的定义、计算和结果。
- `energy/`：把电表的累计读数算成每段时间的能耗。
- `scheduler/`：定时任务的扫描和领取。
- `maintenance/`：运维作业的逻辑。

这些模块默认嵌在 `energy-api` 等进程里运行；`modules/*/cmd/*-owner` 只在需要把某个模块单独拆出来运行时使用。

### 单独部署的服务：`services/`

- 身份：`identity-service/`（账号和密码）。
- 结算：`settlement-service/`（命令行工具，账单以它为准）。
- 通知：`notification-service/`（把设备告警和每日运行结论发到值班人员的微信上）。
- 智能服务：`forecast-service/`（负荷预测）、`optimization-service/`（优化建议）、`fdd-service/`（故障诊断）、`mv-service/`（节能验证）、`report-service/`（月度报告）。
- AI 助手：`operations-agent-service/`，基于 pi（`@earendil-works/pi-agent-core`），只读数据、不能下发控制。它只通过平台网关对外，每次读数据前网关按提问人的权限换一张只能读这个站点的授权。

## 本地运行

主目录是 `E:\Code\HVAC_web`，所有命令在 WSL 的 `/mnt/e/Code/HVAC_web` 里执行。

本地环境是 Compose 项目 `hvac-local`，地址 https://localhost:8443：

```bash
npm run local:up
```

它从当前代码构建，按顺序做完数据库迁移、身份初始化、模拟器的种子数据和启动，可以重复执行。管理员口令在 `deploy/platform/phase1/runtime/local/local-admin.credentials`。`npm run local:down` 停止，`npm run local:reset` 连数据一起删掉重来。

只改网页时：

```bash
npm run dev
npm run lint
npm run build
npm run web:e2e
```

浏览器测试用录制好的接口数据，不需要后端。清理本地截图和临时产物用 `npm run repo:clean:local`。

## 检查

```bash
npm run repo:check
npm run design:check
npm run contracts:check
npm run operations-agent:contracts:check
npm run deployment:phase1:check
npm run domain:plan -- --domain=operations-agent --layers=unit,integration
```

CI 只运行 `scripts/domain-task-matrix.mjs` 里登记的命令，按改动的领域挑着跑。`domain:plan` 先看会跑哪些，`domain:run` 实际执行；支持的领域有 `web`、`platform`、`registry`、`telemetry`、`command`、`alarm`、`workorder`、`analytics` 和 `operations-agent`。

`repo:check` 防止日志、生成产物和临时检出被提交，并核对本文的目录清单和 `cmd/`、`modules/`、`services/` 里实际的目录一致。

## 依赖归属

- 根目录 `package.json`：网页、接口代码生成、仓库检查。
- `cmd/*/go.mod`、`modules/*/go.mod`、`services/*/go.mod`、`libs/*/go.mod`：各自的 Go 依赖；根目录 `go.work` 只用于本地把它们组成一个工作区。
- `services/operations-agent-service/package.json`：AI 助手的 Node 依赖。

设计规则见 `DESIGN.md`，产品背景见 `PRODUCT.md`，业务名词见 `GLOSSARY.md`，架构决定见 `docs/adr/`，功能规格见 `docs/specs/`。
