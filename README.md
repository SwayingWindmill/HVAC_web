# 泉来禾智慧能源平台

本仓库采用单仓库、多运行时结构。根目录只负责编排、共享文档和跨层验证，不承载具体应用源码。

## 目录结构

```text
apps/
  hvac-web/                  生产 Web adapter（Vite + React）

services/
  platform-gateway/          S0 公共 HTTP 入口（Go）
  iam-service/               S0 私有身份服务（Go，mTLS）
  audit-ledger-service/      S0 Transactional Inbox 与 append-only Audit（Go）
  outbox-relay/              S0 PostgreSQL Outbox → Kafka API Relay（Go）
  oidc-test-provider/        S0 确定性 OIDC 测试提供者（Go）

libs/
  identitycontext/           受约束委托与 actor chain 类型
  observability/             W3C Trace、非阻塞 OTLP、低基数指标与日志脱敏
  ownershipregistry/         版本化路由所有权、稳定 cohort 与决策审计
  sessionevent/              版本化 Session Audit Protobuf
  sessionstore/              Session、Audit Intent 与 Outbox 事务层
  oidctest/                  OIDC 测试 fixture
  testpki/                   临时测试 CA 与 Workload 证书

contracts/
  http/                      OpenAPI 权威契约与生成工具锁
  events/                    Protobuf 事件契约与兼容锁
  ownership/                 Route/Data Ownership Registry 与 revision 锁

infra/
  s0-durable/                PostgreSQL、Redpanda、OTel Collector 与 Prometheus fixture

scripts/                     本地编排、验证和浏览器审计
docs/                        产品、架构和集成文档
```

## 当前 AI 运行方式

```text
apps/hvac-web
  -> local read-only HvacMockAgent
```

旧 Python EnergyAgent、独立 Copilot Runtime 和 Next.js 参考 Canvas 已删除。未来 TypeScript Operations Agent 必须通过 Platform Gateway 接入；架构见 ADR 0009、ADR 0010 和 `docs/operations-agent/framework-architecture.md`。

## 常用命令

```bash
npm run dev                 # 仅启动 HVAC Web
npm run dev:s0-gateway      # 启动 HVAC Web + Go Platform Gateway
npm run dev:s0-auth         # 启动 HTTPS Web、OIDC、Gateway 与 mTLS IAM
npm run dev:s0-durable      # 启动 PostgreSQL、Redpanda、Collector、OIDC、IAM、Relay、Audit、Legacy、Gateway 与 Web
npm run delivery:local      # 校验本地交付契约后启动同一完整 S0 拓扑
npm run delivery:validate   # 校验 local/staging 显式配置与生产出口隔离
npm run delivery:check      # 校验镜像、探针、身份、NetworkPolicy、迁移与供应链资产
npm run delivery:render -- --bindings=<private.json> --output=out/s0-staging
npm run contracts:check     # 校验 OpenAPI 生成产物无漂移
npm run events:check        # 校验 Protobuf 字段号和类型兼容锁
npm run ownership:check     # 校验 Route/Data Ownership Registry 与 revision 锁
npm run test:gateway        # Gateway Go 黑盒测试
npm run test:identity       # OIDC、委托、IAM、撤销与 Gateway 身份测试
npm run test:durable-unit   # Session 事件、存储、Relay、Inbox 与 Audit 单元测试
npm run test:observability  # Trace、OTLP、指标标签、日志脱敏与跨服务集成测试
npm run test:ownership      # Route Registry、稳定 cohort、Legacy 代理与公共故障测试
npm run test:legacy-compatibility # Go Legacy 兼容夹具 mTLS/委托边界测试
npm run test:durable-postgres # 真实 PostgreSQL 事务、RLS、所有权审计和重启测试
npm run build:gateway       # 独立构建 Gateway 二进制
npm run build:legacy-compatibility # 构建 Go Legacy 兼容夹具
npm run build:iam           # 独立构建 IAM 二进制
npm run build:audit-ledger
npm run build:outbox-relay
npm run audit:platform-gateway
npm run audit:auth-principal
npm run audit:durable-session
npm run audit:route-ownership
npm run audit:observability # Trace 连续性、Collector 故障隔离与秘密缺失黑盒审计
npm run audit:s0-rollout    # 验证 readiness 门控滚动升级与兼容版本回滚模型
npm run audit:delivery      # 汇总交付配置、静态资产、回滚模型与 PostgreSQL 兼容门禁
npm run lint
npm run build
```

S0 服务默认通过独立 loopback 诊断端口暴露 `/health/startup`、`/health/live`、`/health/ready`、`/metrics` 和 `/diagnostics`。Gateway、Relay、Audit Ledger、IAM 与 OIDC fixture 的默认端口依次为 `19080`、`19081`、`19082`、`19083`、`19084`。Observability 说明见 `docs/operations/s0-observability.md`，可复现交付、签名镜像、staging 渲染和回滚说明见 `docs/operations/s0-delivery.md`。

## 依赖所有权

- 根目录 `package.json`：HVAC Web、契约生成与仓库编排所需 Node 依赖。
- `services/platform-gateway/go.mod`：Gateway 独立 Go module。
- `services/iam-service/go.mod`：私有 IAM 独立 Go module。
- `services/audit-ledger-service/go.mod`：Audit Consumer、Transactional Inbox 与查询服务依赖。
- `services/outbox-relay/go.mod`：独立 Outbox Relay 与 Kafka API 客户端依赖。
- `services/oidc-test-provider/go.mod`：本地/测试 OIDC fixture 独立 Go module。
- `libs/*/go.mod`：委托、Observability、Route Ownership、Session 事件、事务存储、OIDC fixture 与测试 PKI 的窄接口模块；根目录 `go.work` 只负责编排。
- `contracts/http/tooling.lock.json`：OpenAPI 生成器、模板、Go、Node 与运行时校验版本锁。
- `contracts/events/session-audit.v1.lock.json`：Protobuf v1 字段名、字段号和字段类型兼容锁。
- `contracts/ownership/ownership.v1.lock.json`：公共路由 owner、数据 writer 与单调 revision 兼容锁。
- `hvac-backend/package.json`：Legacy Frozen NestJS 依赖；S0 私有模式仅加载既有 health controller/service。
未来 Agent adapter 的依赖必须由其所属模块独立管理，并通过 Platform Gateway 和版本化合同保证跨进程兼容性。
