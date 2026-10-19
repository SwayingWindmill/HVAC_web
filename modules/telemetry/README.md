# 遥测（telemetry）

平台的设备读数模块：接收读数、维护每台设备的当前状态并实时推送、写历史，以及给能耗和设备历史提供查询。它分成几个进程跑，彼此的资源预算互不影响：

- `cmd/telemetry-worker`：接收读数、当前状态、实时推送、写历史，以及能源模块的用电量计算。
- `modules/telemetry/cmd/telemetry-query-owner`：查询服务（`telemetry-query-service`），下面主要讲它。
- `modules/telemetry/cmd/telemetry-history-projector`：把历史写进 ClickHouse 的单独入口。

## 数据怎么流

```text
设备读数
    -> 写历史（telemetry_history.observations）
        -> 算成用电量 / 冷量（modules/energy，analytics.energy_interval_facts）
            -> Cube（site_energy、equipment_energy、site_energy_cost 视图）
                -> 查询服务
                    -> 平台网关（网页）
                    -> AI 助手
                    -> 报告、优化、节能验证服务（以各自的后台服务身份）
```

查询服务只提供固定的几种查询：能耗走 Cube，设备历史用一个权限最小的 ClickHouse 只读账号查 `telemetry_history.observations`。它不接受任意 SQL、库名、表名或维度。

## 查询服务的接口

| 接口 | 做什么 |
| --- | --- |
| `POST /internal/v1/analytics/energy-series` | 能耗序列：一个站点一段时间的用电或供冷，按小时 / 天 / 月；用电的每个点带估算电费；可以按设备拆分（`breakdown: device`）。网页的入口是 `POST /api/v1/analytics/energy-series` |
| `POST /internal/v1/analytics/semantic-query` | AI 助手的语义查询（见 `docs/specs/agent-semantic-query.md`） |
| `POST /internal/v1/telemetry/device-history` | 设备历史：原始读数 |
| `POST /internal/v1/telemetry/device-history:aggregate` | 设备历史：按刻钟 / 小时 / 天 / 周 / 月的平均、最小、最大值 |

实时推送的订阅授权（`POST /internal/v1/telemetry/centrifugo/subscribe`）不在查询服务里，在 `telemetry-worker` 的遥测部分（`pkg/telemetry/server.go`）。

能耗序列：

- 只接受固定的查询格式，拒绝任意的 Cube 字段、SQL、维度和度量；
- 校验租户和站点（UUIDv7）、时区（IANA）、粒度、质量规则，时间范围最长 366 天；
- 换一张 30 秒有效、带租户和站点范围的 Cube 令牌，把请求翻译成固定的 Cube 查询；
- 返回请求的和实际的粒度、数据截至时间、数据版本、是否不完整，以及各质量等级的数量。

它不接收读数、不判断在线和新鲜度、不写 ClickHouse、不计算用电量、不下发指令，也不让浏览器直接访问 Cube。

设备历史：

- 原始读数：最多 8 个点位，每页最多 500 行，时间范围最长 24 小时，按点位和采样时间排序，每个点位各自限量；只返回已接收的有限数值，带质量、质量原因、数据截至时间、数据版本、是否不完整和被截断的点位。
- 按时间段统计：刻钟、小时、天、周、月，时间范围最长 366 天、最多 1000 个时间段；累计量给增量。
- 账号 `telemetry_query_history_reader` 只能查 `telemetry_history.observations`，不能写历史，也读不到能耗数据。

## 数据截至时间和版本

能耗序列在同一个 16 秒预算里执行两个固定的 Cube 查询：

- 按请求的时间范围查数据；
- 用同样的租户、站点和能源类型条件，从请求的起点往后、不设终点，查元数据。所以它的开销和查询窗口一样，又能看到终点之后的数据。

接口约定起点包含、终点不含；Cube 的日期范围两头都包含，所以发给 Cube 的终点是 `to - 1ms`（和 ClickHouse `DateTime64(3)` 的精度一致），不到 1 毫秒的请求直接拒绝。

返回的元数据这样得出：

- `dataWatermark`（数据截至时间）：元数据查询里最大的截至时间；起点之后站点没有任何数据时为空；
- `aggregateWatermark`：现在等于 `dataWatermark`；
- `datasetRevision`：`<QUERY_DATASET_REVISION>:<最大的记录版本>`；起点之后没有数据时后缀是 `:empty`；
- `partial`（不完整）：截至时间早于请求的终点，或者请求范围里有任何一个本地的小时 / 天 / 月没有数据，就是不完整；截至时间够了也不会掩盖中间的缺口。

Cube 的缓存刷新时间不会被当作数据截至时间。

## 安全边界

浏览器从不直接调查询服务或 Cube：

```text
浏览器 -> 平台网关 -> 查询服务 -> Cube
              \-> AI 助手 -> 查询服务 -> Cube
```

查询服务检查：

1. 调用方的双向 TLS 证书在允许名单里（平台网关、AI 助手、故障诊断服务，以及报告、优化、节能验证服务）；
2. 网关签的短时授权（后台服务则是 IAM 签的授权，ADR 0017）；
3. 授权的签发方是网关，`executingService` 等于实际出示证书的服务；
4. 动作是固定的那一个；
5. 授权绑定了整个规范化查询的 SHA-256 摘要。

调用方自己带的身份或范围请求头一律拒绝。Cube 收到的是另一张短时令牌，并按租户和站点做行级隔离。

## 环境变量

必填：

| 变量 | 用途 |
|---|---|
| `QUERY_TLS_CERT` / `QUERY_TLS_KEY` | 服务的 TLS 证书和私钥 |
| `QUERY_CLIENT_CA` | 验证调用方证书的 CA |
| `QUERY_GATEWAY_DELEGATION_CERT` | 验证网关授权的公钥证书 |
| `QUERY_CUBE_ENDPOINT` | Cube 地址，例如 `http://cube:4000` |
| `QUERY_CUBE_API_SECRET` | 签 Cube 短时令牌的 HS256 密钥，至少 32 字节 |
| `QUERY_DATASET_REVISION` | 能耗数据版本的前缀，例如 `energy-interval:v1` |
| `QUERY_HISTORY_CLICKHOUSE_ENDPOINT` | 设备历史用的 ClickHouse 地址 |

可选，括号里是默认值：

| 变量 | 默认值 |
|---|---|
| `QUERY_SERVICE_ADDR` | `127.0.0.1:18447` |
| `QUERY_DIAGNOSTICS_ADDR` | `127.0.0.1:19088` |
| `QUERY_DELEGATION_ISSUER_SPIFFE` | `spiffe://hvac.local/platform-gateway` |
| `QUERY_ALLOWED_WORKLOAD_SPIFFE` | `spiffe://hvac.local/platform-gateway` |
| `QUERY_OPERATIONS_AGENT_SPIFFE` | `spiffe://hvac.local/operations-agent-service` |
| `QUERY_FDD_SERVICE_SPIFFE` | 故障诊断服务的身份 |
| `QUERY_REPORT_SERVICE_SPIFFE`、`QUERY_OPTIMIZATION_SERVICE_SPIFFE`、`QUERY_MV_SERVICE_SPIFFE` | 报告、优化、节能验证服务的身份（嵌在 `energy-api` 里时由 `cmd/energy-api/embedded_energy.go` 读取） |
| `QUERY_AUDIENCE` | `telemetry-query-service` |
| `QUERY_CUBE_CA` | 不设时用系统信任的证书 |
| `QUERY_HISTORY_CLICKHOUSE_CA` | 不设时用系统信任的证书 |
| `QUERY_HISTORY_CLICKHOUSE_DATABASE` | `telemetry_history` |
| `QUERY_HISTORY_CLICKHOUSE_TABLE` | `observations` |
| `QUERY_HISTORY_CLICKHOUSE_USERNAME` | `telemetry_query_history_reader` |
| `QUERY_HISTORY_CLICKHOUSE_PASSWORD` | 空（本地测试账号没有密码） |

## 测试

```bash
npm run test:analytics
npm run test:analytics-gateway
npm run build:telemetry-query-owner
```

测试覆盖：固定字段翻译成 Cube 查询、租户条件、按质量区分的度量、数据截至时间和版本、是否不完整的判断、返回大小的预算、双向 TLS 身份、授权绑定查询摘要，以及拒绝伪造的请求头。

## 还没有

- 导出任务和查冷数据；
- 基线和对比模型；
- 水、燃气的能耗。
