# EG8200 模拟冷站

在没有真实网关和设备的时候，用来验证整个平台的确定性模拟器：模拟一个小型商用冷站，像真的网关一样把设备读数发给平台、接收控制指令、按物理模型做出反应，再通过后续的读数报告结果。规格和规则见 `docs/specs/virtual-central-plant.md`。

本地环境（`npm run local:up`）跑两个实例，A、B 两个模拟站点各一个（`deploy/acceptance/phase1-simulator.compose.yaml`，场景分别是 `configs/scenario-daily-a.json` 和 `scenario-daily-b.json`），程序是 `cmd/eg8200-mqtt-publisher`。

## 模拟的设备

- `CHILLER-01`：水冷冷水机组
- `CHWP-01`：冷冻水泵
- `CWP-01`：冷却水泵
- `CT-01`：冷却塔
- `METER-HVAC-TOTAL`：空调总电表
- `BTU-METER-01`：冷冻水冷量计

模型包含：机组 COP、制冷量、辅机功率、累计电量和累计冷量、水泵的相似定律、冷却塔的逼近温度和基本的联锁。冷冻水侧始终保持一个能量平衡（`Q ≈ 1.163 × 流量(m³/h) × 温差(°C)`）。水泵频率、冷却塔风机转速、机组负荷和水温都随时间变化：指令成功只是设定了目标，设备的实际读数要在之后的几个周期里慢慢变过去。

## 能接收的指令

| 设备 | 指令 | 参数 | 允许范围 |
| --- | --- | --- | --- |
| 机组 | `start`、`stop`、`resetFault` | 无 | — |
| 机组 | `setChilledWaterTemperatureSetpoint` | `setpointC` | 5–12 °C |
| 机组 | `setLoadLimit` | `loadLimitPct` | 20–100% |
| 水泵 | `start`、`stop`、`resetFault` | 无 | — |
| 水泵 | `setFrequency` | `frequencyHz` | 20–50 Hz |
| 冷却塔 | `start`、`stop`、`resetFault` | 无 | — |
| 冷却塔 | `setFanSpeed` | `fanSpeedPct` | 20–100% |

每条指令的结果带 `success`、固定的结果代码 `code`，必要时带实际设定的值。无效的指令不改变设备状态。设备状态版本号由平台的遥测给出，不由模拟器给。

## 经 MQTT 接入平台

用 MQTT 配置格式 6（`configs/central-plant.mqtt.local.example.json`）。

1. 在网页「集成管理」里登记一台网关，生成一次性接入码。
2. 只在第一次启动时设置 `EG8200_ENROLLMENT_CODE`。`enrollmentUrl` 必须是 HTTPS 地址；`enrollmentCaFile` 用来信任入口，`enrollmentServerName` 是入口的名字；`caFile` 另外用来信任 MQTT 服务器；`certFile` 和 `keyFile` 指向一个可写的证书文件位置。
3. 模拟器自己生成私钥和证书请求，用接入码换一张 90 天的证书和 MQTT 地址，并原子地写好自己的证书文件。不需要手工签证书，也不用写凭据的 SQL；私钥一直留在模拟器（网关）这边。

之后启动不用接入码，直接用证书文件。它每小时检查一次，在最后 14 天内用当前证书续期；旧证书到原来的到期时间才失效。证书过期或被永久吊销后，要重新登记一台网关，并明确地把设备转过去。接入服务按网关隔离数据和指令；某张证书到期时，已经建立的 MQTT 连接不保证立刻断开。

一个模拟器进程就是一台 EG8200 网关。MQTT 配置里用它在登记里的设备 id（`gatewayId`）指定这台网关，这也是证书的 CN 和 MQTT 客户端 id。它把读数发到 `hvac/v1/{gatewayId}/up/telemetry`，每台设备用模拟器里的设备名作为网关里的设备名，接入服务再从登记里找到租户、站点、设备和点位。指令从 `hvac/v1/{gatewayId}/down/command` 收，回复发到 `hvac/v1/{gatewayId}/up/reply`。

## 历史导入

历史导入是手工或开发时用的，不属于实时的 MQTT 链路，也不在正式部署里。它用同一套冷站模型和场景、点位信息，以及「模拟器设备 → 平台设备 id」的对照（`configs/central-plant.device-ids.local.json`），把读数一条条提交给遥测的「只写历史」接口。本地平时用 `scripts/local-history-replay.mjs` 调它。

`TELEMETRY_ALLOWED_HISTORICAL_REPLAY_SPIFFE` 指定专门的导入程序身份（默认 `spiffe://hvac.local/historical-replay-runner`），不要用 MQTT 接入的身份。遥测按这台设备、这个点位最近一次实时收到的点位来对应导入的读数，所以要等实时数据到过之后再导入这台设备。每次导入还要一个固定的 UUIDv7 批次号、历史的开始时刻和一段有限的时长：

```bash
go run ./tools/eg8200-simulator/cmd/eg8200-history-replay \
  -plant-config ./tools/eg8200-simulator/configs/central-plant.local.json \
  -device-ids ./tools/eg8200-simulator/configs/central-plant.device-ids.local.json \
  -telemetry-url https://127.0.0.1:18446 \
  -dataset-id 01900000-0000-7000-8000-000000000002 \
  -from 2026-07-01T00:00:00Z \
  -duration 24h \
  -tls-cert /path/to/replay/tls.crt \
  -tls-key /path/to/replay/tls.key \
  -server-ca /path/to/ca.crt
```

导入来源（`HISTORY_REPLAY`）、收到时间、来源分区和事件编号、点位对应、校验、去重和写历史都由遥测负责。导入的读数可以成为正式的历史，但不会改最新读数、在线状态、设备快照或状态版本号。同一批次重导会得到同样的顺序号，所以中途重试或重启不会重复写入。数据库和历史的集成测试：`node scripts/run-historical-replay-integration.mjs`。

## ATV630 变频器

- `cmd/eg8200-virtual-atv630`：ATV630 的 Modbus/TCP 模拟从站。
- `cmd/eg8200-atv630-edge`：用平台的 ATV630 驱动（`libs/edgecontrol`）通过真实 TCP 连接去读写它。

协议验收：`node scripts/run-atv630-protocol-acceptance.mjs`。

## 旧的 ThingsBoard 模式

`cmd/eg8200-simulator` 经 ThingsBoard 的设备 HTTP 接口发读数、轮询服务端 RPC 指令。用法：在 ThingsBoard 里按 `configs/central-plant.local.json` 的 id 建六台设备，用环境变量传各自的访问令牌（`TB_TOKEN_CHILLER_01` 等，不要写进配置文件），然后：

```bash
go run ./tools/eg8200-simulator/cmd/eg8200-simulator \
  -config ./tools/eg8200-simulator/configs/central-plant.local.json
```

默认目标是 `http://localhost:8080`；只有本机地址可以用 HTTP，别的地址必须 HTTPS。

## 运行状态

诊断端口默认 `19092`：`GET /health/live`、`GET /health/ready`。所有设备在一个周期里都发送成功之后，才算就绪。

## 镜像

在仓库根目录构建非 root 的镜像：

```bash
npm run build:eg8200-simulator-image
```

运行镜像时把配置只读挂进去，令牌用环境变量传；镜像里没有凭据，也没有正式环境的默认配置。

## 读数之间的关系

- 水泵流量和转速成正比；
- 水泵和冷却塔风机的功率和转速的三次方成正比；
- 机组功率 = 制冷量 ÷ COP；
- COP 受冷冻水设定温度、冷却水温度和低负荷运行的影响；
- 冷却水流量、冷冻水流量或冷却塔不够时，机组不制冷；
- 空调总功率 = 机组 + 两台水泵 + 冷却塔风机；
- 冷量计的制冷量和机组用同一个能量平衡。

## 测试

```bash
go test ./tools/eg8200-simulator/...
```

单元测试覆盖：配置和场景的严格解析、场景按步推进、能量平衡、水泵相似定律、指令校验、联锁、读数内容，以及模拟器不越权写业务数据。
