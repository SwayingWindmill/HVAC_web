# 第一阶段部署

`deploy/platform/phase1` 是第一阶段唯一的部署入口，依据《智慧能源系统总体部署架构设计 V1（单服务器基线）》（`SE-ARCH-DEPLOY-001 V1.0`）：一台 Linux 服务器 + Docker Compose。仓库里没有 Kubernetes 清单，第一阶段也不需要。

本地开发平时只用 `npm run local:up` / `local:down` / `local:reset`（见 `docs/agents/local-environment.md`），它按顺序做完下面的迁移、身份初始化、种子数据和启动。服务器的部署方式见 `docs/HANDOFF.md`。本文讲这些步骤各自是什么。

## 对外的边界

### 网关证书

接入服务（Connectivity）持有网关 CA，并在内部 8448 端口提供 HTTPS 的证书服务。每个部署用 Connectivity 的 `--initialize-gateway-ca` 命令初始化一次 `GATEWAY_CA_DIR`（用 `CONNECTIVITY_CA` 提供服务 CA）：它只新建文件，从不覆盖已有的密钥，并给 Connectivity 另签一张服务端证书。平时运行时这个目录只读挂载。只有公开的信任证书给 nginx、Mosquitto 和网页后端，CA 私钥只留在 Connectivity。`npm run local:up` 会自动做第一次初始化。

nginx 通过 HTTPS 对外提供 `/gateway/v1/enroll` 和 `/gateway/v1/renew`，并以平台网关的服务身份向 Connectivity 认证；续期时只转发验证过的客户端证书。运维人员在网页「集成管理」登记网关、生成 24 小时有效的一次性接入码；模拟器用同样的网页接口自动申请证书，不再手工签发或写种子。吊销对一台网关是永久的。证书的签发、续期、吊销记录写在 `connectivity.gateway_credential_audit` 里，还没有在操作记录页面显示。

默认的单机部署只对外开 HTTPS / WSS。启用 `integration` 时才加 MQTT：

```text
443   HTTPS / WSS -> Nginx -> 网页 + energy-api（/api、/realtime）+ identity-service（/identity）
8883  MQTT TLS    -> Mosquitto -> 现场网关    [只在 integration 时]
```

（服务器上对外的 MQTT 是 8884，由主机的 nginx 转到 Mosquitto，见 `docs/specs/site-gateway-onboarding.md`。）

本机的 PostgreSQL、ClickHouse、Redis、Grafana、Loki、Tempo、Prometheus 和所有 Go 服务都不对主机开端口。身份服务是基础设施，不是业务服务；nginx 只在已有的 HTTPS 入口下暴露它的 `/identity`。应用网络只在内部；数据网络在数据库都在本机时也只在内部，选了外部数据库时才允许出站。访问监控和数据服务要走 VPN / SSH 隧道这类受控的管理通道，不另开公网端口。

## 服务清单

`runtime-inventory.v1.json` 给 Compose 里的每个服务分了类：

- 默认的业务服务：`energy-api`、`telemetry-worker`、`metric-worker`；
- 默认的支撑服务：`scheduler`、`maintenance`（运行 `cmd/maintenance-worker`）；身份服务 `identity-service` 是基础设施；
- 可选：`connectivity` + `mqtt-broker`（`integration`），预测、优化、故障诊断、节能验证、报告（`intelligence`）；
- 一次性的运维动作：迁移、表结构预检、身份初始化 / 管理 / 同步、备份。

Compose 里增删服务而不更新这份清单，`npm run deployment:phase1:check` 会失败。

## 环境变量文件

仓库里有四份示例：

```text
environments/development.runtime.env.example
environments/testing.runtime.env.example
environments/staging.runtime.env.example
environments/production.runtime.env.example
```

复制需要的一份成不提交的 `*.runtime.env`，替换所有 `[REDACTED_SECRET]`，再用 `PHASE1_ENV_FILE` 指向它。真实的环境文件和 `deploy/platform/phase1/runtime/` 都被 Git 忽略。

开发、测试、预发环境默认不允许正式的出站访问。正式环境的示例里绝不能有演示、测试数据或测试身份服务的地址。

### 数据库放在哪

PostgreSQL、ClickHouse、Redis 各自可以放在本机或外部，互不影响，也不改应用的角色：

```text
PHASE1_POSTGRES_MODE=local       -> 启动 local-postgres
PHASE1_POSTGRES_MODE=external    -> 不起 PostgreSQL 容器；迁移和预检用 PHASE1_POSTGRES_HOST/PORT/SSLMODE

PHASE1_CLICKHOUSE_MODE=local     -> 启动 local-clickhouse
PHASE1_CLICKHOUSE_MODE=external  -> 不起 ClickHouse 容器；客户端用 PHASE1_CLICKHOUSE_HTTP_URL

PHASE1_REDIS_MODE=local          -> 启动 local-redis
PHASE1_REDIS_MODE=external       -> 不起 Redis 容器；各服务用各自配置的外部地址
```

- 外部 PostgreSQL：把 `PHASE1_POSTGRES_HOST` 指向外部地址，并更新环境文件里每个应用的连接串。表结构预检（`phase1-schema-preflight`）照样必须先通过。
- 外部 ClickHouse：`PHASE1_CLICKHOUSE_HTTP_URL` 设成受保护的外部 HTTP(S) 地址，Compose 会把它传给能耗查询、历史和能耗计算、指标、预测和优化。上线前在外部建好 `telemetry_history`、`analytics` 表结构和同样最小权限的账号。仓库里本机 ClickHouse 的初始化 SQL 用的是无密码的内部账号，不能原样用在网络可达的外部部署上，外部账号的密码放在部署密钥里。
- 外部 Redis：保持一个 Redis，但保留原来的逻辑分隔：DB 0 / 前缀给实时推送和可重建的最新值，DB 1 给短时的 OIDC 登录状态，DB 2 给共享的限流计数。配置 `CENTRIFUGO_ENGINE_REDIS_ADDRESS`、`TELEMETRY_LATEST_CACHE_REDIS_URL`、`METRIC_REDIS_URL`、`OIDC_STATE_REDIS_URL`、`LIMIT_POLICY_REDIS_URL`。这一步不引入 Redis 哨兵或集群，也不算高可用。

任何一个数据服务放到外部时，启动程序会给这个部署的 `data` 网络开出站。三个都在本机时，`data` 只在内部。放到外部只是换了位置和故障范围，不是高可用。

## 静态检查

```bash
npm run architecture:phase1:check
npm run deployment:phase1:check
```

不启动服务也能检查 Compose 能不能解析。要用启动程序，因为它根据 `PHASE1_DEPLOYMENT_TIER` 套上这一档的 CPU / 内存和 ClickHouse 内部内存上限，并选对应的观测档位：

```bash
PHASE1_ENV_FILE=deploy/platform/phase1/environments/development.runtime.env \
  node --experimental-strip-types scripts/phase1-compose.ts config --quiet
```

本地 WSL 部署用仓库里的 `wsl.override.yaml` 和下面的启动程序。它读 Git 忽略的 `runtime/db-role-credentials/roles.sql`，在内存里拼出最小权限的身份服务和 IAM 连接串，只传给 Compose 进程，不把数据库角色的密码写进环境文件，也不打印：

```bash
npm run deployment:phase1:wsl -- config --quiet

# 从当前代码构建网页和自己的服务镜像，再启动
npm run deployment:phase1:wsl -- --source-deploy up -d

# 需要 MQTT 和接入服务时
npm run deployment:phase1:wsl -- --source-deploy --integration up -d
```

本地部署用 `--source-deploy`，保证网页和服务来自同一个 Git 版本（构建前把网页的构建号设成当前版本）。`PHASE1_GO_BUILD_IMAGE`、`PHASE1_GO_RUNTIME_IMAGE`、`PHASE1_GO_PROXY`、`PHASE1_WEB_BUILD_IMAGE`、`PHASE1_NGINX_RUNTIME_IMAGE`、`PHASE1_NPM_REGISTRY` 可以按本地网络换构建来源，不改仓库里正式环境的默认值；`PHASE1_ENV_FILE` 和 `PHASE1_DB_ROLE_CREDENTIALS_SQL` 可以换本地环境文件和角色密码 SQL 的位置。`--simulator-acceptance` 会自动带上 `--integration`，因为模拟器要走本地的 MQTT 和接入服务。

## 启动前要准备的

- `PUBLIC_TLS_DIR` 下的公网 HTTPS 证书和私钥；
- 启用 `integration` 时，`MQTT_PKI_DIR` 下的 MQTT CA、服务器和客户端证书；
- `INTERNAL_PKI_DIR` 下的内部服务证书；
- 一份真实的、不提交的环境文件；
- 数据库在本机时，独立的 PostgreSQL WAL / 基础备份位置和 ClickHouse 备份位置；外部数据库按它自己的备份方式；
- 预发和正式环境：审核过的应用镜像版本和摘要。

仓库里的示例只是格式说明，不是正式的密钥。

预发和正式环境要求每个自己的应用镜像都固定到不可变的 `@sha256:` 摘要。替换好摘要、做完迁移之后，正式环境直接用批准的镜像，不在主机上重新构建：

```bash
PHASE1_ENV_FILE=deploy/platform/phase1/environments/production.runtime.env \
  node --experimental-strip-types scripts/phase1-compose.ts pull
PHASE1_ENV_FILE=deploy/platform/phase1/environments/production.runtime.env \
  node --experimental-strip-types scripts/phase1-compose.ts up -d --no-build
```

## 数据库和迁移

PostgreSQL（本机或外部）上有身份库 `hvac_identity`，以及各业务库 `hvac_s0` 到 `hvac_s5`（名字沿用了早期的阶段编号）。密码哈希和身份服务的授权请求、授权码在 `hvac_identity`；网关的 OIDC 登录关联状态是 Redis 里一条 10 分钟的一次性记录，所以网关重启或多实例时登录不受影响。浏览器看到的签发方是公网 HTTPS 地址，网关和身份服务之间的发现、换令牌、取 JWKS 走内部网络上的 `OIDC_BACKCHANNEL_BASE_URL`。IAM 的授权数据在 `hvac_s1`。登录会话默认最长 8 小时（`SESSION_ABSOLUTE_TTL=8h`），60 分钟没有操作就过期（`SESSION_IDLE_TTL=60m`）；只有用户在浏览器里的操作会刷新空闲时间，后台的遥测请求不会让无人值守的会话一直有效。

迁移程序在 `migrations/`：

- 只执行 `manifest.v1.json` 里精确列出的、审核过的迁移，按原样执行 SQL，不在运行时改写；
- 测试数据和凭据不在正式的迁移里；
- 在每个库里记下迁移状态和源文件哈希，发现被改过或恢复不完整就停下；
- 新增迁移文件（在 `infra/*/postgres/init/` 或各模块的 `migrations/` 下）要同时登记到 `manifest.v1.json`、`migration-list.tsv` 和 `Dockerfile`。

迁移已经冻结（2026-10-22，#688）：服务器上已有真实站点的数据，`migration-list.tsv` 里登记过的迁移都执行过，内容一个字节都不能改，改表结构只能新增迁移（已有库遇到改过的迁移，迁移器会按哈希中止）。不要的迁移可以从清单里去掉，但它建的每张表都要由清单里还在的某条迁移删掉（`DROP TABLE`，或 `DROP SCHEMA ... CASCADE` 删掉整个 schema），例如删 `services/rule-runtime-service` 时，同一个 PR 里新增了 `032-drop-generic-rules.sql`，用 `DROP SCHEMA rule_runtime CASCADE` 删掉它的表。`npm run migrations:freeze:check` 拿当前分支和它从 main 分出来的那个提交比，守住这两条，门禁的 contracts 一步会跑它。

服务正常启动前先跑一次性的产品 / 表结构预检，要求产品版本和迁移清单的摘要完全一致；正式环境没有跳过的开关。

`limit-policy.v1.json` 是有版本的高风险限流策略。第一个执行的是 AI 助手的请求次数：网关在 Redis 里原子地按会话预留时间窗口，Redis 不可用时拒绝请求。策略只读挂载，被 `product-release.v1.json` 引用。

数据库角色不用仓库里的正式密码创建。把 `migrations/role-credentials.sql.example` 复制到 Git 忽略的运行时目录，换上部署提供的密码，再在应用上线前执行迁移：

```bash
PHASE1_ENV_FILE=deploy/platform/phase1/environments/production.runtime.env \
  node --experimental-strip-types scripts/phase1-compose.ts \
  --profile migration run --rm phase1-migrator
```

不管数据库在本机还是外部，都用同一个启动程序；它根据 `PHASE1_POSTGRES_MODE` 决定要不要起 `local-postgres` 和开数据网络的出站。

测试数据、种子数据、旧系统迁移和只用于本机的身份初始化都不在正式的迁移清单里。`npm run deployment:phase1:migration:test` 在一个隔离的新 PostgreSQL 上把迁移跑两遍（证明能重复执行），校验迁移哈希、强制行级隔离和数据库连接权限，并确认指令下发程序没有数据库角色。

## 身份初始化

身份初始化是迁移之后运维人员明确执行的一步，不是服务启动时顺便做的，也从不把租户、站点、角色这些授权写进身份服务。

1. 用 `identity-bootstrap` profile 跑一次 `identity-keygen`，在 `IDENTITY_RUNTIME_DIR` 下生成不提交的 PKCS#8 RSA 签名密钥；已有的不覆盖。
2. 用 `identity-admin`（`IDENTITY_ADMIN_DATABASE_URL` 绑定专门的 `identity_admin` 角色）`create` 一个带密码的账号。开发和测试环境找回密码用 `npm run deployment:phase1:identity-reset`：它执行 `reset-password-random`，原子地改写唯一的、不提交的 `runtime/local-admin.credentials`，不打印新密码。不要手工维护第二份本地凭据文件。
3. 全新的本地 WSL 部署，跑一次 `npm run deployment:phase1:local-foundation`：可重复执行，只建审核过的本地租户和站点，以及 IAM 判定需要的登记、遥测、告警、工单基础策略；不建模拟设备。
4. 用审核过的输入跑 `identity-reconciler`，把这个账号的「签发方 + 主体编号」连同租户成员关系和角色指派写进 IAM（ADR 0022）。`seed.roleBindings` 的每一项用 `roleKey` 指定角色（内置的如 `admin`，或租户自己的），可以带 `siteId`；不带就是整个租户。第一个管理员是 `{"roleKey": "admin"}`，之后的人在「系统管理 → 用户与角色」里加。改输入要提高 `sourceVersion`。
5. 启动或重启 `identity-service`；签名密钥文件不存在或无效时它拒绝启动。

6. 要用微信小程序的微信绑定、一键登录和订阅提醒时：在微信公众平台「开发管理 → 开发设置」生成 AppSecret，管理员自己把它写进 `IDENTITY_RUNTIME_DIR` 下的 `wechat-miniprogram-appsecret`（只有这一行），然后重启 `identity-service`。同一页的「IP白名单」开着时，要把服务器的公网 IP 加进去，否则微信拒绝（日志 `wechat_refused`，`wechat_errcode` 40164），小程序提示「服务器还没有开通微信功能」。没有这个文件时身份服务照常启动，小程序里绑定微信和一键登录会提示「服务器还没有开通微信功能」；文件在但是空的，身份服务拒绝启动。微信消息打开小程序的哪个版本由 `IDENTITY_WECHAT_MINIPROGRAM_STATE` 决定，Compose 默认 `developer`（小程序还没发布），发布后改成 `formal`。告警提醒和每日结论用的模板 ID 默认写在代码里，换模板时设 `NOTIFICATION_WECHAT_ALARM_TEMPLATE_ID`、`NOTIFICATION_WECHAT_DAILY_TEMPLATE_ID`；提醒里的站点名称、时区和每日结论的能耗由网关的后台服务身份读，它在租户上的角色（`site.read`、`analytics.energy-series.read`）由 `phase1-bootstrap-local-foundation` 登记。身份服务只认网关签名的凭证里写的账号，所以它也读网关的证书（`INTERNAL_PKI_DIR` 下的 `gateway/tls.crt`，只挂这一个文件），缺了就拒绝启动。AppID 默认是本平台小程序的 `wx4b7b82bf0351c6f3`，不是秘密。只有 `identity-service` 加入 `wechat-egress` 网络、能访问 `api.weixin.qq.com`；别的服务要发微信消息都经过它（`docs/specs/miniprogram-wechat.md`）。

`identity_runtime` 不能新增用户。单独的 `identity_admin` 角色让账号管理不扩大长期运行的身份服务的权限。签名私钥、初始化凭据和带部署授权的同步输入都不进 Git。

## 本地模拟冷站

WSL 开发时，服务启动不会顺便写模拟设备。平台起来之后，跑 `npm run deployment:phase1:simulator`：它可重复地初始化本地冷站的登记数据和接入服务的状态，再用 `simulator-acceptance` profile 启动单独的 `deploy/acceptance/phase1-simulator.compose.yaml`。模拟器不在正式的 Compose 里。接入服务的种子只存模拟器证书的指纹和密钥引用，从不把私钥写进 PostgreSQL。这个只用于验收的初始化用部署的 PostgreSQL 管理员写登记和接入服务的测试数据（因为强制行级隔离仍然开着，应用和迁移角色都没有绕过权限）；正式部署不跑这个命令。

## 备份

本机 PostgreSQL 支持持续的 WAL 归档，并有 `backup` profile 做基础备份。WAL 归档默认关，`POSTGRES_ARCHIVE_MODE=on` 才开：开发和测试默认关（除非真的准备了归档目录），预发和正式的示例里是开的。外部 PostgreSQL 的备份由那边负责；只有外部数据库允许用配置的账号做 `pg_basebackup` 时，才能用仓库里的备份容器。本机 ClickHouse 用自带的备份 profile；外部 ClickHouse 的备份和时间点恢复由那边负责。细节见 `backup/README.md`。

```bash
docker compose -f deploy/platform/phase1/compose.yaml --profile local-postgres --profile backup run --rm postgres-backup
docker compose -f deploy/platform/phase1/compose.yaml --profile local-clickhouse --profile backup run --rm clickhouse-backup
```

恢复目标由《SE-OPS-009 V1.0》给出，`recovery/` 里有机器可读的目标、整台服务器的恢复步骤和演练记录格式。配好备份不等于达到目标：每个正式部署都要用真实的备份做一次带时间记录的灾难恢复演练。

## 定时任务

《SE-PLATFORM-006 V1.0》实现为一个单独的 `scheduler` 进程，状态存在 PostgreSQL 里。链条是 `schedule_definitions -> job_instances -> job_attempts`：调度器负责时间、去重、错过和重试的协调、过期租约的恢复；各业务服务负责实际执行。

`metric-worker` 不再自己扫指标绑定来决定什么时候算，而是用 `FOR UPDATE SKIP LOCKED` 从 PostgreSQL 领 `METRIC_WINDOW_CALC`、`METRIC_RECALC`、`METRIC_BACKFILL` 任务，记录每次尝试、续租约，用现有的指标引擎计算和保存。单机上补算一次只跑一个任务。

调度器的 HTTP 管理接口还只是设计。进入接口定义之前，运维人员可以用 `scheduler/schedule-definition.sql.example` 建审核过的定时任务；这不代表直接改数据库是公开的接口。PostgreSQL / ClickHouse 的备份不归这个调度器，仍由基础设施的备份机制触发。

## 档位和观测

只有一份 Compose，用明确的 profile，不是一个什么都开的默认配置。机器可读的定义在 `deployment-tiers.v1.json`：

```text
observability-core  = Prometheus + node-exporter + Grafana
observability-logs  = core + OTel Collector + Loki
observability-full  = logs + Tempo
local-postgres      = 本机 PostgreSQL
local-clickhouse    = 本机 ClickHouse
local-redis         = 本机 Redis
integration         = connectivity + mqtt-broker
intelligence        = forecast-service + optimization-service + fdd-service + mv-service + report-service
```

档位在环境文件里选，不单独传观测 profile：

```bash
PHASE1_ENV_FILE=deploy/platform/phase1/environments/production.runtime.env \
  node --experimental-strip-types scripts/phase1-compose.ts up -d --no-build
```

`PHASE1_DEPLOYMENT_TIER` 说了算。`PHASE1_OBSERVABILITY_PROFILE` 必须和档位一致，并决定 OTel Collector 的管道（`observability/otel-collector/{core,logs,full}.yaml`）。两个启动程序都会校验并注入选中的资源和 profile；档位还把 ClickHouse 的内部内存上限设得比容器内存上限低。

| 档位 | 观测 | 说明 |
|---|---|---|
| `single-lite` | `observability-logs` | 目标 8 核 16G；集中的指标和日志，没有集中的追踪 |
| `single-full` | `observability-full` | 目标 16 核 32G；加单实例 Tempo |

`observability-core` 是最小的演示形态。Tempo 和智能服务默认不启动，要用 profile 打开。

```text
observability-core
  指标 -> Prometheus -> Grafana
  主机 -> node-exporter -> Prometheus

observability-logs
  + Docker JSON 日志 -> OTel Collector -> Loki -> Grafana

observability-full
  + OTLP 追踪 -> OTel Collector -> Tempo -> Grafana
```

所有应用指标由 Prometheus 直接从各 Go 服务的诊断端口抓取。OTel Collector 只在集中日志和追踪时需要，所以 `observability-core` 去掉它也不影响告警和看板。第一阶段没有多节点的观测集群。告警怎么处理见 `docs/operations/alerts.md`。

## 单机的存储和资源

PostgreSQL、ClickHouse、Redis 和 MQTT 的数据放在可配置的主机目录（`POSTGRES_DATA_DIR`、`CLICKHOUSE_DATA_DIR`、`REDIS_DATA_DIR`、`MQTT_DATA_DIR`），不用看不见的 Docker 卷。正式环境把这些目录放在规划好的数据盘上，PostgreSQL 的 WAL / 基础备份和 ClickHouse 备份放在和主数据目录分开的存储上。

所有长期运行的容器都有 CPU / 内存上限和 Docker JSON 日志轮转。`node-exporter` 给 Prometheus 提供主机的 CPU、内存、磁盘、负载和网络指标；磁盘用到 80% 预警、90% 严重告警。

第一阶段的 Compose 里没有 Kafka / Redpanda。

## 可用性和恢复的证据

第一阶段是「单机、可恢复」（`SINGLE_NODE_RECOVERABLE`），不是高可用。`availability-tier.v1.json`：

- 列出 PostgreSQL、ClickHouse、Redis、MQTT、Centrifugo 和观测的单点故障；
- 记下每个组件的升级路径和触发条件；
- 定义从单机到多实例的路径，参照 ThingsBoard 按 `service.type` 切换拓扑的做法，同时保留 PostgreSQL 的待发事件和任务租约作为可靠的主干。

RPO/RTO 只是恢复目标。`recovery/attainment.v1.json` 默认是「目标已定义」（`TARGET_DEFINED`），`productionClaimAllowed=false`。只有真实的、带时间记录的恢复演练验证了外部备份、恢复硬件和路径、PostgreSQL 实际恢复到的时间点、各组件的恢复时间、控制指令的核对、定时任务和待发事件的恢复以及最后的业务验证，正式部署才能声明达到《SE-OPS-009》的目标。只在本地容器里试一下不够；任何可用性百分比都要引用可用性等级和实测数据。

核实一份真实的整机演练记录后，明确地生成并校验有时限的达标声明：

```bash
npm run deployment:phase1:recovery:verify -- --file=/evidence/drill-record.json --attainment-output=/evidence/attainment.json
npm run deployment:phase1:recovery:claim:check -- --file=/evidence/attainment.json --drill-record=/evidence/drill-record.json
```

预发环境的进程故障演练和灾难恢复是两回事：它逐个杀掉并重启关键容器，检查恢复时间，再跑一遍关键的冒烟测试，但永远记 `productionClaimAllowed=false`。

### 把业务模块拆成单独进程

参照 ThingsBoard 明确选择拓扑角色的做法（不采用它的内存队列或 Kafka / actor 运行方式），可以用同版本的各模块入口把它们拆成单独进程，只留通知嵌在 `energy-api` 里。需要 `single-full` 档位：

```bash
PHASE1_ENV_FILE=deploy/platform/phase1/environments/staging.runtime.env \
  PHASE1_DEPLOYMENT_TIER=single-full \
  node --experimental-strip-types scripts/phase1-compose.ts --owner-split config --quiet

PHASE1_ENV_FILE=deploy/platform/phase1/environments/staging.runtime.env \
  npm run deployment:phase1:owner-split:drill -- \
  --confirm-staging-owner-split --output=/evidence/owner-split.json
```

配置通过不等于认证：在预发环境的真实接口演练通过之前，它一直是「已实现、需要演练」。运维人员要把 `owner-split-release.v1.json.example` 和 `owner-split-live-journeys.v1.json.example` 复制到 Git 忽略的运行时目录，替换所有占位符，把 `energy-api` 和各模块入口的镜像摘要绑定到同一个产品版本，设置批准的清单 SHA-256，并提供覆盖每个拆出模块的、带认证的真实接口操作。演练记录里不保存授权头和响应内容。
