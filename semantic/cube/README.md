# Cube 语义层

能耗统计的语义层：用电、供冷、能效、电费这些数怎么算、怎么按时间汇总、谁能看，都定义在这里。Cube 只给内部的查询服务（`telemetry-query-service`）调，不对外开放。

## 在数据链里的位置

```text
telemetry_history.observations
    -> 把累计读数算成用电量（modules/energy）
        -> analytics.energy_interval_facts_visible（每段用电记录，只露出最新版本）
            -> Cube
                -> 查询服务
```

Cube 从不直接读原始读数。表计清零的处理、换算成每段的电量、统一单位、质量、数据版本和数据截至时间，都在上游算好。

## 视图和内部 cube

对外只有视图（ADR 0023）。查询服务查它们，AI 助手的语义查询也在它们的字段里选；每个字段都有中文标题和说明。

| 视图 | 内部 cube | 数据 | 字段 |
|---|---|---|---|
| `site_energy` | `site_energy_facts` | 站点总表（`SITE`）的记录：站点总量 | 共有字段（见下）加 `electricity_valid_kwh`、`cooling_valid_kwh`、`cop`（同一时间段的供冷 ÷ 用电） |
| `equipment_energy` | `equipment_energy_facts` | 设备分表（`BRANCH`）的记录：已经包含在总量里 | 共有字段加 `equipment_id`（装分表的设备） |
| `site_energy_cost` | `site_energy_cost_facts` | 站点总表的用电，按电价窗口计价（`docs/specs/estimated-energy-cost.md`） | `cost_valid_cny`、`cost_valid_and_suspect_cny`、`priced_valid_kwh`、`priced_valid_and_suspect_kwh`、`unpriced_valid_kwh`、`unpriced_valid_and_suspect_kwh`、`site_id`、`tariff_period`、`period_end`（没有共有字段） |

前两个 cube 都继承 `energy_facts`（`model/cubes/energy_facts.yml`，基于 `analytics.energy_interval_facts_visible`），各自在 SQL 里固定计入范围，所以一个视图不会把总量和它的分表混在一起。共有字段：`energy_valid_kwh`、`energy_valid_and_suspect_kwh`（两者都要加 `energy_type` 条件）、三个质量计数、`max_data_watermark`、`max_dataset_revision`、`site_id`、`energy_type`、`quality`、`period_end`。租户、电表、点位和传感器的编号不是视图字段。

查询服务按 `period_end` 以小时、天、月汇总：一整段记录算进它结束时刻所在的时间段。

没有开启 Cube 的预聚合。ClickHouse 里的记录是最终依据，「只露出最新版本」的视图先去掉旧版本，Cube 再负责字段定义、按时区汇总和权限过滤。

## 权限

查询服务每次生成一张 30 秒有效的 HS256 JWT，里面有：`tenantId`、`siteId` 和 `siteIds`、`principalId`、`policyRevision`、用户组 `analytics_reader`。

`cube.py` 把 JWT 里的信息对应到 Cube 的用户组。基础 cube `energy_facts` 带行级规则，由 `site_energy_facts` 和 `equipment_energy_facts` 继承（`site_energy_cost_facts` 不继承它，有自己同样的行级规则）：只返回 `securityContext.tenantId` 和 `securityContext.siteId` 的数据。查视图时，Cube 会把 cube 的行规则和视图的合在一起，所以租户不必是视图字段。视图（`model/views/energy.yml`）只把字段开放给 `analytics_reader`（默认拒绝）。cube 设成 `public: false`，直接查 cube 字段的请求会因为字段不可见被拒。

Cube 用的 ClickHouse 账号是 `cube_analytics_reader`，只能查 `analytics.energy_interval_facts_visible`（以及电价窗口的可见视图），不能写。

## 本地单独跑

先启动带好原始历史和能耗表结构的 ClickHouse：

```bash
docker compose -f infra/telemetry/compose.yaml up -d clickhouse
```

把 `local.env.example` 复制成一个不提交的本地环境文件，换掉 Cube 的 API 密钥，然后：

```bash
docker compose --env-file <本地环境文件> -f semantic/cube/compose.yaml up
```

示例里 Cube 容器通过 `host.docker.internal` 连本机发布的 ClickHouse HTTP 端口；Cube 的 4000 端口只绑本机。

本地的 ClickHouse 读取账号没有密码，只用于本机。本地单实例的 Cube 用内存队列和缓存，没有预聚合。正式环境要提供带认证的只读 ClickHouse 账号、TLS，以及用于分布式队列和缓存的 Cube Store。

## 测试

```bash
npm run analytics:history:integration
docker compose --env-file semantic/cube/local.env.example -f semantic/cube/compose.yaml config
```
