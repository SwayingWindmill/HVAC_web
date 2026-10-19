# 第一阶段的总体架构

第一阶段的云端是**一台 Linux 服务器 + Docker Compose**。部署的机器可读描述在 `deploy/platform/phase1/`（进程清单 `runtime-inventory.v1.json`、架构基线 `architecture-baseline.v1.json`、恢复目标 `recovery/recovery-targets.v1.json`），`npm run architecture:phase1:check` 检查它们和 Compose 一致。现场控制层的设计见 ADR 0012 和 `contracts/architecture/edge-control-plane.v1.json`。

## 1. 整体

```text
                    值班人员（浏览器）
                          │ HTTPS / WSS
                          ▼
                 nginx  →  平台网关（energy-api）
                          │
        ┌─────────────────┼──────────────────┐
        ▼                 ▼                  ▼
   业务模块           设备数据             智能服务
 （登记、告警、     （设备接入、遥测、    （预测、优化、诊断、
  工单、指令…）       指标计算）          节能验证、报表、AI 助手）
        └─────────────────┼──────────────────┘
                          ▼
         PostgreSQL · ClickHouse · Redis · MQTT · Cube
                          │ MQTT（TLS）
                          ▼
                    现场网关（Node-RED）
                          │ Modbus / BACnet / …
                          ▼
                         设备
```

安全、监控、日志、追踪、备份和审计贯穿各层。浏览器只通过 nginx 和平台网关访问业务接口，不直接连数据库、MQTT 或内部服务。

## 2. 进程

逻辑上的业务模块和实际运行的进程是分开的：第一阶段尽量不把模块拆成单独的进程。

| 进程 | 做什么 | 默认运行 |
| --- | --- | --- |
| `nginx` | 唯一的公开入口 | 是 |
| `energy-api` | 平台网关；进程里还跑着权限（IAM）、设备登记、告警、通知、工单、指令、操作记录和查询服务（`telemetry-query-service`），每个模块有自己的双向 TLS 端口和身份，Compose 里用旧的服务名作为它的网络别名 | 是 |
| `telemetry-worker` | 接收设备读数、最新值、实时推送、写历史、把电表读数算成用电量 | 是 |
| `metric-worker` | 领取并执行指标计算任务 | 是 |
| `scheduler` | 扫描定时计划、生成任务、处理错过和重试、回收过期的租约；不做业务计算，也不控制设备 | 是 |
| `maintenance` | 证书到期扫描、处理一直失败的任务、停用租户（`cmd/maintenance-worker`） | 是 |
| `identity-service` | 平台自己的登录服务（OIDC） | 是 |
| `centrifugo` | 实时推送的内部传输；浏览器只看到网关的 `/realtime/` | 是 |
| `postgres`、`clickhouse`、`redis`、`cube` | 数据和语义层 | 本机部署时 |
| `connectivity`、`mqtt-broker` | 设备接入和 MQTT 服务器（ADR 0015） | 接现场或模拟器时 |
| `forecast-service`、`optimization-service`、`fdd-service`、`mv-service`、`report-service`、`operations-agent-service` | 智能服务 | 按需（`intelligence`） |
| 监控一组（`otel-collector`、`prometheus`、`alertmanager`、`loki`、`tempo`、`grafana` 等） | 指标、日志、追踪和告警 | 按档位选 |

数据库迁移、启动前的表结构检查、身份初始化、备份都是一次性的运维操作，不算常驻进程。第一阶段不需要 Kubernetes、服务网格或 Kafka。

本地环境还会跑冷站模拟器（`tools/eg8200-simulator/`），代替真实的现场网关。

## 3. 数据放在哪

- **PostgreSQL**：业务主数据、配置、设备登记、指令和告警状态等，以它为准。
- **ClickHouse**：遥测历史、用电记录、指标结果等统计数据。
- **Redis**：最新值和实时传输用的缓存，可以重建，不保存唯一的业务数据。
- **对象存储**：归档、备份、数据集等大文件；第一阶段只定了和厂商无关的外部依赖约定（`object-storage.external.v1.json`），具体产品由部署环境选。
- **MQTT**：现场和云端之间唯一的通道。
- **现场网关**：断网时在本地存着待发的读数，恢复后再发。

## 4. 云端和现场的分工

云端不直接访问 PLC、楼控系统或其他现场设备。云端出故障时，现场必须能继续采集、缓存、执行本地控制和安全策略。

云端负责：租户和权限、设备登记、指令的权限审批和记录、遥测和统计、告警、工单、预测和优化。现场负责：读写设备、本地控制和安全联锁、断网缓存。

ADR 0012 定了现场控制层的目标设计（参照 OpenEMS）：

- 现场是**软实时**的控制层，默认 1 秒一个周期；设备保护、急停、消防这类硬实时功能必须留在专用硬件和固件里。
- 每个周期先读、再算、再写：所有控制器看同一份不变的数据，协议读写不能卡住控制器。
- 点位（Point）是云端的身份和历史；通道（Channel）是现场的运行值。现场的通道地址不能变成平台的主键。
- 云端的指令是「意图」：云端负责权限、审批、防重复和审计；现场在应用安全、联锁和设备限制之后决定实际写下去的值。远程意图有到期时间，云端断开或过期后，控制权回到现场的本地策略，不会一直停在最后一次远程设定值。
- 模拟器和真实设备只在驱动、协议和物理模型上不同，控制和指令的路径共用。

现在实际做到哪一步、还缺什么，见 ADR 0012 和 `libs/edgecontrol`；真实的现场网关目前跑的是 Node-RED。

## 5. 部署边界

第一阶段的正式云端只接受单台服务器。业务、设备数据、指标、数据库、MQTT 和监控仍保持逻辑和进程上的边界，以后要拆到多台服务器时按这些边界拆。

现场网关是独立的现场边界，不算在「一台服务器」里。开发和测试时可以把模拟器放在同一台机器上，正式环境不能因此把云端进程部署进 PLC 或楼控网络。

下面这些都不是第一阶段的前提：Kubernetes、服务网格、自动扩缩、PostgreSQL 主备、ClickHouse 集群、MQTT 集群、把 Kafka/Redpanda 当成必需的消息骨干、多地域、GitOps。

## 6. 环境

正式上线前，开发、测试、预发、正式四套环境的数据库、Redis、MQTT、存储、密钥、域名和配置都要分开，开发和测试不能连正式的数据库或 MQTT。现在有本机的 `hvac-local` 一套和现场服务器一套。

## 7. 恢复目标

以 `deploy/platform/phase1/recovery/recovery-targets.v1.json` 为准：PostgreSQL 最多丢 5 分钟数据、2 小时内恢复；控制相关最多丢 5 分钟、1 小时内恢复；云端遥测 4 小时内恢复；指标最多丢 30 分钟、4 小时内恢复；整台服务器更换 4 小时内恢复。

整机 4 小时恢复的前提是：有冷备或能及时更换的硬件、服务器以外的备份、有版本的配置和写好的恢复步骤。单台服务器不承诺不停机、自动切换、数据零丢失或 99.99% 的可用性。

定了目标不等于做到了。恢复时间从确认故障影响服务开始算，到业务恢复并通过关键业务验证为止；容器跑起来不算。是否达标要靠带时间记录的恢复演练（见 `deploy/platform/phase1/recovery/README.md`）。
