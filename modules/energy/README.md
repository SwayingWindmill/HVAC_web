# 能源模块（energy）

把电表、冷量计的累计读数算成每段时间的用电量（或冷量），并同步电价窗口。第一阶段它在 `cmd/telemetry-worker` 里运行；`modules/energy/cmd/energy-projector` 是单独构建和集成测试用的入口，默认不部署。

## 把累计读数算成用电量

```text
telemetry_history.counter_arrivals        （检查点之后、按历史批次号顺序新到的累计读数）
    -> telemetry_history.counter_deltas_from（按读数所在的小时：这些点位、这一小时的差值）
    -> 登记服务的计量绑定查询
    -> analytics.energy_interval_facts       （每段用电记录）
    -> analytics.energy_projection_checkpoints（处理到哪了）
```

- 读数之间的差值、表计清零、走满回到 0、中断后恢复，这些规则都写在 ClickHouse 的视图里，Go 代码不重算。
- 一个累计读数点位必须有唯一一条已发布、启用中的主计量绑定（任何能源类型都可以）；没有的话，程序明确报错并停下。
- 每个差值写成一段记录，电量正好等于这个差值。读数质量 `GOOD` 记为有效（`VALID`）；`PARTIAL`/`ESTIMATED`/`MANUAL` 记为可疑（`SUSPECT`）；`STALE`/`INVALID`/未知记为无效（`INVALID`）。空的差值和不合理的减少由视图排除，不写成 0。
- 每段记录按当前读数的编号识别，用（租户、站点、计量绑定、当前读数编号）去重。第一次写入版本号为 0；读数晚到需要更正时，版本号加一，记下这次重算的编号，并写下重算开始、写入一批、完成或失败的记录。
- 每次最多读一批（默认 256 条）检查点之后新到的累计读数，按 `history_sequence` 排序。对每条读数算它自己的差值；如果它是晚到的，再更正它后面那条读数的差值。读取范围只限于这批读数涉及的点位和最早的采样时间，不扫全部历史（#441）。记录写好之后才前移检查点；前移丢了，最多重放一批，已经写过、前一条读数也相同的差值会跳过。
- 累计读数的视图用 ClickHouse 的 `SQL SECURITY DEFINER`，读取账号只能查这些视图，碰不到原始读数表。

计量绑定的查询是登记服务的内部接口（双向 TLS），要一张带 `meter-binding.resolve` 权限的短时授权。这个程序以自己的后台服务身份（ADR 0017）向 IAM 每个租户申请一张，快到期前续签。

## 同步电价窗口

把每个站点的进线电价展开成按 UTC 的价格窗口，写进 `analytics.tariff_price_windows`，Cube 用它给每段用电计价（`internal/energy/price_window_projector.go`，规则见 `docs/specs/estimated-energy-cost.md`）。

## 谁能读写哪些数据

| 数据 | 权限 |
|---|---|
| `telemetry_history.counter_arrivals`、`counter_deltas_from` | 只读 |
| `analytics.energy_interval_facts` | 写入，以及去重时读 |
| `analytics.energy_projection_checkpoints` | 读和前移 |
| `analytics.tariff_price_windows` | 写入 |
| 登记服务的 `meter_bindings` | 由登记服务的接口读 |

## 环境变量

必填：

| 变量 | 用途 |
|---|---|
| `ANALYTICS_CLICKHOUSE_HTTP_URL` | ClickHouse 的 HTTP 地址 |
| `ANALYTICS_CORE_REGISTRY_URL` | 登记服务的 HTTP 地址 |
| `ANALYTICS_IAM_URL` | 签发登记授权的 IAM 地址 |
| `ANALYTICS_INTERNAL_CA` | 登记服务和 IAM 的内部 CA |
| `ANALYTICS_INTERNAL_TLS_CERT` | 这个程序的双向 TLS 证书（它的后台服务身份） |
| `ANALYTICS_INTERNAL_TLS_KEY` | 对应的私钥 |

可选，括号里是默认值：

| 变量 | 默认值 |
|---|---|
| `ANALYTICS_SOURCE_DATABASE` | `telemetry_history` |
| `ANALYTICS_DATABASE` | `analytics` |
| `ANALYTICS_ENERGY_TABLE` | `energy_interval_facts` |
| `ANALYTICS_CLICKHOUSE_READER_USERNAME` / `_PASSWORD` | 空 |
| `ANALYTICS_CLICKHOUSE_WRITER_USERNAME` / `_PASSWORD` | 空 |
| `ANALYTICS_CLICKHOUSE_CA` | 系统信任的证书 |
| `ANALYTICS_PROJECTOR_BATCH_SIZE` | `256` |
| `ANALYTICS_PROJECTOR_POLL_INTERVAL` | `500ms` |
| `ANALYTICS_PROJECTOR_DIAGNOSTICS_ADDR` | `127.0.0.1:19089` |

## 测试

```bash
npm run test:analytics
npm run analytics:history:check
npm run analytics:history:integration
npm run build:analytics-read-model-projector
```

集成测试写入正常的和乱序到达的累计读数，用一条测试绑定，检查读数增加和清零时算出的结果，并确认再算一遍不会重复写入。

## 现在的限制

- 读数晚到时的更正和重算记录已经有了；但运维人员还不能任选一段历史让它重算。
- 每一批仍要把相关点位在最早到达时间之前的历史扫一遍（取最后一条读数和每个版本的最大值）。内存不会涨，但耗时随历史增长（#441）。
