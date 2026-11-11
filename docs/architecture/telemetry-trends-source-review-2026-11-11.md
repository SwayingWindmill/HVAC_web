# 多点位历史趋势的降采样、开关量分栏与事件叠加参照核对（2026-11-11）

运维人员排查冷站异常（例如冷冻水出水温度偏高、主机频繁加减载或水泵变频振荡）时，需要把同一时间段内的主机功率、进出水温度、水泵频率、温度设定值和启停运行状态拉到同一条时间轴上比对，并看到这段时间里触发过哪些告警事件、下发过哪些控制指令。本文核对四个外部开源实现的一手源码与本仓库现有代码，供历史趋势视图（`/operations/trends`，对应 `#757`、`#758`、`#759`）定稿接口契约和页面规则时直接取用。

| 项目 | 核对版本 | 固定提交 / 位置 | 许可证与借鉴边界 |
| --- | --- | --- | --- |
| ThingsBoard | `v4.4` | `6d46786579c8b29caf5102f95ddb133674bed68b` | 服务端主干 Apache-2.0；前端时序图含 BUSL-1.1 头，只核对交互与参数边界，不搬代码 |
| OpenEMS | `2026.9.0` | `14f0dedf5fe279845281bafaf0e48ea0ab51333a` | EPL-2.0，只做分桶档位与分栏对照，不搬代码 |
| MyEMS | `v6.9.0` | `b360f5bb4c2be4fd15854057963531b2e8d1bc0a` | MIT |
| Grafana | `v13.3.1` | `5db6ba887630c7fa12e5bfd063f2fa854025468c` | AGPL-3.0，只做缺数断线、状态泳道与事件标记规则参照，不搬代码 |
| Recharts（仓库已装依赖） | `3.8.0` | `apps/hvac-web/package.json:47`、`node_modules/recharts/package.json:3` | MIT，项目现用图表库 |

---

## 读到的做法

### 1. 时间窗口、自动聚合桶宽与单次点数上限

- **ThingsBoard：前后端双层卡死桶数上限，前端按窗口跨度除以 200–500 自动选档。**
  - 历史读数接口 `GET /api/plugins/telemetry/{entityType}/{entityId}/values/timeseries/history`（`application/src/main/java/org/thingsboard/server/controller/TelemetryController.java:293-359`）按单个实体接收 `keys`、`startTs`、`endTs`、`intervalType`（`common/data/src/main/java/org/thingsboard/server/common/data/kv/IntervalType.java:5-8`：`MILLISECONDS | WEEK | WEEK_ISO | MONTH | QUARTER`）、毫秒步长 `interval`、聚合方式 `agg`（`common/data/src/main/java/org/thingsboard/server/common/data/kv/Aggregation.java:8-12`：`MIN | MAX | AVG | SUM | COUNT | NONE`）和原始条数上限 `limit`（仅 `agg=NONE` 时生效，默认 `100`）。
  - 服务端在 `dao/src/main/java/org/thingsboard/server/dao/timeseries/BaseTimeseriesService.java:68-69,315-327` 强制校验聚合桶数：`intervalCounts = (endTs - startTs) / Math.max(interval, 1000)`，一旦超过 `database.ts_max_intervals`（`application/src/main/resources/thingsboard.yml:306` 默认 `700`）直接报错拒绝，防止前端传入长窗口配细粒度把数据库打满。
  - 前端时间服务 `ui-ngx/src/app/core/services/time.service.ts:26,98-128` 设 `MAX_DATAPOINTS_LIMIT = 500`，规定允许的最细步长 `minIntervalLimit = timewindowMs / 500`、默认推荐步长 `avgInterval = timewindow / 200`，再贴合到预置档位表 `defaultTimeIntervals`（`ui-ngx/src/app/shared/models/time/time.models.ts:1209-1335`：`1s/5s/10s/15s/30s/1m/2m/5m/10m/15m/30m/1h/...`）。例如 `1h` 默认落到 `15s–1m`（60–240 桶），`6h` 落到 `1m–2m`（180–360 桶），`1d` 落到 `5m–10m`（144–288 桶），`7d` 落到 `30m–1h`（168–336 桶）。此外在 `time.models.ts:26,176` 与 `ui-ngx/src/app/modules/home/components/widget/lib/chart/time-series-chart.ts:183,253-255` 支持 `timeForComparison`（上一周期或昨日同刻双时间轴对比）。
- **OpenEMS：请求可省略粒度，服务端按起止天数把单条曲线稳定压在 168–288 个点。**
  - `io.openems.common/src/io/openems/common/jsonrpc/request/QueryHistoricTimeseriesDataRequest.java:68-86,102-105` 允许不传 `resolution`；`io.openems.common/src/io/openems/common/timedata/CommonTimedataService.java:24-49,61-66` 的 `calculateResolution(fromDate, toDate)` 按自然日跨度自动定档：`days <= 1` 用 `5m`（一天 288 点），`days == 2` 用 `10m`（288 点），`days == 3` 用 `15m`（288 点），`days <= 6` 用 `30m`，`days <= 12`（含 `7d`）用 `1h`（7 天共 168 点）。
  - `io.openems.backend.timedata.aggregatedinflux/src/io/openems/backend/timedata/aggregatedinflux/AggregatedInflux.java:105-108,125-139,170-195` 把瞬时功率/温度的均值表（`retentionPolicyAvg`）与电表累计读数的日末极值表（`retentionPolicyMax` 配合 `DbDataUtils.calculateLastMinusFirst` 算时段用电量）彻底分开查询。
- **Grafana：用图表像素宽度定 `maxDataPoints`，再用采集最小周期托底。**
  - `public/app/features/query/state/PanelQueryRunner.ts:283,335-346` 把面板像素宽度作为 `maxDataPoints` 传给 `calculateInterval(timeRange, maxDataPoints, lowerIntervalLimit)`；`packages/grafana-data/src/datetime/rangeutil.ts:592-606,665-757` 先算 `(to - from) / resolution`，再经 `roundInterval` 规整到 `1s/5s/10s/15s/30s/1m/2m/5m/10m/15m/30m/1h/...` 标准档位，并用数据源采集周期 `lowLimitInterval` 托底（例如设备每 1 分钟采一次，看 15 分钟窗口也不会算出无意义的 5 秒桶）。
- **MyEMS：历史趋势直接裸查原始表，无服务端降采样也无行数上限。**
  - `myems-api/reports/metertrend.py:286-291` 调用 `myems-api/core/utilities.py:1064-1152` 的 `query_point_values_batch`，直接对 `tbl_energy_value`、`tbl_analog_value`、`tbl_digital_value` 执行 `SELECT point_id, utc_date_time, actual_value ... WHERE utc_date_time BETWEEN %s AND %s ORDER BY point_id, utc_date_time`，原样返回区间内全部原始行。点数随采集频率和天数线性膨胀，7 天多点位高频采集下响应体积不可控。

### 2. 连续模拟量与离散状态分栏、缺数断线

- **Grafana：模拟量曲线与状态色带拆成独立面板并同步十字准星，按采样间隔阈值主动插 `null` 断线。**
  - 模拟量走 `TimeSeriesPanel`（`public/app/plugins/panel/timeseries/TimeSeriesPanel.tsx:75,153-187`），开关/运行状态走 `StateTimelinePanel`（`public/app/plugins/panel/state-timeline/StateTimelinePanel.tsx:72-75,97-131`，`TimelineMode.Changes` 水平状态泳道），两者通过 `EventBusPlugin` + `cursorSync` 共享十字准星和缩放范围，避免把 `0/1` 开关量和 `kW`、`°C` 塞进同一张多 Y 轴图。即使布尔字段被放进数值图，`public/app/plugins/panel/timeseries/utils.ts:225-255` 也会强制把值域锁在 `[0, 1]` 并把连线方式改成 `LineInterpolation.StepAfter`（源码注释直接写明布尔量画平滑线或斜线没有物理意义）。
  - 缺数断线不靠运气，而是在绘图前显式插空：`utils.ts:170-176` 调用 `applyNullInsertThreshold`（`packages/grafana-data/src/transformations/transformers/nulls/nullInsertThreshold.ts:28-120,137-180`），同时检查「窗口起点到首条读数」「相邻两条读数间距 `curValue - prevValue > threshold`」「末条读数到窗口终点」三处间隔；只要超过阈值（取 `insertNulls` 或采样周期 `interval`，见 `nullInsertThreshold.ts:51`），就自动插入 `null` 点把线断开，且 `SpanNullsEditor.tsx:9-25` 默认 `spanNulls = false`（绝不跨 `null` 拉直线）。在状态泳道里还专门提供 `INSERT_MODES.plusone`（`prev + 1ms`，`nullInsertThreshold.ts:8-9`），防止设备断联前最后一个「运行」状态在离线几小时内被一直画成在线运行。
- **ThingsBoard 与 OpenEMS：离散状态统一走阶梯线配枚举刻度，多图联动游标。**
  - ThingsBoard 在 `ui-ngx/src/app/modules/home/components/widget/lib/chart/time-series-chart.ts:98-104` 为 `state` 图表固定开启 `step = true`、`stepType = LineSeriesStepType.end`（保持当前状态直到下一采样点改变），并由 `time-series-chart-state.models.ts:17-91` 把 `'true'/'false'` 或字符串枚举转成离散刻度标签。
  - OpenEMS 在 `ui/src/app/shared/components/chart/abstracthistorychart.ts:171-173,436-438,851-868` 把空值保留为 `null`（不填 `0`），对继电器/状态通道开启 `stepped` 折线并锁死 `[0, 1]`、`stepSize: 1` 的 `ON/OFF` 轴，多个分栏图通过 `ChartConstants.Plugins.SYNC_CHARTS()`（`ui/src/app/shared/components/chart/timeline-chart/timeline-chart.ts:19,98-100`）联动悬停游标。

### 3. 告警事件与控制指令叠加

- **Grafana：区间事件铺极淡底色、瞬时事件画细虚线，标记收进坐标轴轨道并支持密集折叠。**
  - `public/app/plugins/panel/timeseries/plugins/AnnotationsPlugin.tsx:161-213,274-352` 在同一条时间轴上区分两类事件：
    1. **带起止时间的区间事件**（`isRegion && timeEnd`，对应告警从触发到恢复的持续时段）：起止边界画 `[5, 5]` 虚线竖线（`175-176,189-196` 行），中间铺默认透明度仅 `0.1`（10%）的半透明色块（`colorManipulator.alpha(color, regionOpacity ?? 0.1)`，第 `201` 行），既标出异常时段又不盖住主曲线。
    2. **单时间点的瞬时事件**（`!isRegion`，对应下发一次控制指令）：只画一条贯穿绘图区的虚线竖线加 X 轴边缘小标记（`189-190,304-309` 行），不在曲线中间弹常驻文字框。
  - 事件密集时有两层防遮挡保护：一是 `useAnnotationClustering`（`97-110,362-367` 行）按绘图区像素宽度把挤在一起的标记合并；二是 `options.annotations?.multiLane` 分轨模式（`166,280` 行）把事件收进 X 轴外侧高为 `ANNOTATION_LANE_SIZE` 的独立窄轨，不再往主图里画满屏竖线。
- **其余三个参照实现均无完整的趋势事件叠加层。**
  - ThingsBoard `time-series-chart.models.ts:475-554` 只有水平阈值线（`TimeSeriesChartThreshold`）；OpenEMS `abstracthistorychart.ts:8,41` 只用 `chartjs-plugin-annotation` 画静态限额线；MyEMS `myems-api/reports/fddfault.py:41-44` 是返回 `{}` 的空桩。因此事件叠加规则直接以 Grafana 为参照基准。

---

## 本仓库现状

### 1. ClickHouse 历史表与读数聚合实现

- **原始表与物化汇总表都已就位**：`infra/telemetry/clickhouse/init/001-telemetry-history.sql:3-53` 的 `telemetry_history.observations` 按 `(tenant_id, site_id, point_id, sensor_id, device_id, telemetry_key, sampled_at, observation_id)` 排序，记录 `point_type`、`value_type`（`NUMBER | STRING | BOOLEAN | JSON`）、`value_number`、`value_string`、`value_boolean`、毫秒级 UTC `sampled_at`、读数质量 `quality` 与落库时间 `projected_at`。同目录下的 `001-telemetry-history.sql:55-135`、`003-telemetry-rollups.sql:4-129` 和 `004-counter-semantics.sql:277-329` 还建有 `numeric_1min`、`numeric_15min`、`numeric_hourly` 和 `counter_1min`、`counter_15min`、`counter_hourly` 物化表。
- **查询服务当前直接扫 `observations` 做快照一致性聚合**：`modules/telemetry/internal/history/aggregate.go:308-513` 带 `projected_at < snapshot_cutoff` 和读数质量过滤（`VALID_ONLY` 只取 `GOOD`；`USABLE` 取 `GOOD | PARTIAL | ESTIMATED | MANUAL`）直接按主键前缀聚合 `observations`。对冷站单设备 ≤8 个点位、7 天窗口的数据规模，ClickHouse 主键前缀扫描本身就是毫秒级。

### 2. 现有网关与遥测历史接口的权限链路及 4 个具体缺口

- **已跑通的公开接口与权限链路**：`contracts/http/s2-telemetry-public.openapi.json:176-233,572-789` 暴露了两个按单台设备查询的 POST 接口，由 `cmd/energy-api/internal/gateway/device_history.go:20-114` 转发到 `modules/telemetry/internal/query/server.go:24-25,273-367`：
  - `POST /api/v1/telemetry/device-series:query`（原始读数序列，单设备 `1..8` 个 `keys`，`pageSize <= 500`）
  - `POST /api/v1/telemetry/device-series:aggregate`（分桶聚合，单设备 `1..8` 个 `keys`，返回数值桶 `average/minimum/maximum`、电表累计量差值桶 `delta`、状态计数桶 `stateCounts`）
  - 两个接口都在网关侧按 `(deviceId, keys)` 校验 `telemetry.history.read` 权限（`device_history.go:38,82`、`contracts/iam/capability-catalog.v1.json:28`），再签发绑定了请求范围 SHA-256 摘要 `ScopeDigest` 的短期委托令牌交由 `telemetry-query-service` 复核（`server.go:296-304,344-352`）。
- **对照 `/operations/trends` 排障需求，现有历史接口存在 4 个具体缺口**：
  1. **缺少 15 分钟以下的聚合粒度**：`AggregateGranularity`（`libs/telemetryhistorymodel/aggregation.go:20-28`、`contracts/http/s2-telemetry-public.openapi.json:673`）目前只支持 `QUARTER_HOUR | HOUR | DAY | WEEK | MONTH`。看 `1h` 窗口时 `QUARTER_HOUR` 只有 4 个点，看 `6h` 只有 24 个点，无法看清主机加减载与水温波动过程。
  2. **`MaximumAggregateBuckets = 1000` 卡的是「单设备所有请求点位合计桶数」**：`modules/telemetry/internal/history/aggregate.go:72` 检查时间槽数 `len(periods) <= 1000`，但 `aggregate.go:117`、`libs/telemetryhistorymodel/aggregation.go:232` 和 `s2-telemetry-public.openapi.json:770` 同时限制返回数组总长度 `len(buckets) <= 1000`。一旦引入 `1m` 粒度，单台冷机选 4 个点位看 `6h` 就会产生 `4 × 360 = 1440` 个桶，直接触发 `bucket count exceeds maximum` 失败。
  3. **多点位原始查询 `device-series:query` 存在分页饿死与限流扣减问题**：`modules/telemetry/internal/history/client.go:329-330` 写的是 `ORDER BY telemetry_key, history_source.sampled_at LIMIT PageSize+1`（`MaximumHistoryPageSize = 500`，`libs/telemetryhistorymodel/model.go:19`）——同一台设备一次查 4 个点位时，若排在字母序前面的第 1 个点位在窗口内有 500 条采样，第 1 页返回的全是第 1 个点位，后 3 个点位完全拿不到数据。此外 `QueryDeviceHistory`（`device_history.go:30`）每次调用都会扣减 `limitpolicy.DimensionExpensiveQuery` 配额，而聚合接口 `QueryDeviceHistoryAggregate`（`device_history.go:72`）不扣减该配额。
  4. **设定值点位与字符串型运行状态点位在 `aggregate.go` 中被漏掉**：
     - `libs/telemetryhistorymodel/aggregation.go:278` 与 `aggregate.go:323` 只允许 `point_type IN ('TELEMETRY', 'COUNTER', 'STATE')`，所有 `point_type = 'SETTING'` 的设定值点位（如冷冻水出水温度设定、频率设定）调聚合接口一律返回空。
     - 真实站点配置 `deploy/sites/songyaxuan.site.json:232-237` 与模拟冷站契约 `contracts/registry/central-plant-device-points.v2.json:9,26,38,50` 中，`chiller.run_state`、`chwp.run_state`、`cwp.run_state`、`cooling_tower.run_state` 均登记为 `"pointType": "TELEMETRY", "valueType": "STRING"`；但 `aggregate.go:366-367` 的 `gaugeAggregateQuery` 只查 `point_type = 'TELEMETRY' AND value_type = 'NUMBER'`，而 `stateAggregateQuery`（`aggregate.go:471`）只查 `point_type = 'STATE'`。两边条件都不命中，导致冷站运行状态点位调 `device-series:aggregate` 时静默返回 0 个桶。

### 3. 告警事件与控制指令只读接口现状

- **告警事件（`modules/alarm`）**：公开接口 `GET /api/v1/alarms?siteId=...&limit=1..200&cursor=...&condition=...`（`cmd/energy-api/internal/gateway/alarm.go:478-481,654-702`、`modules/alarm/pkg/alarmservice/postgres.go:54-120`）要求 `alarm:read` 权限，按 `first_occurred_at DESC, alarm_id DESC` 游标分页。返回结构（`apps/hvac-web/src/features/alarms/alarm-api.ts:24-42`）已包含 `deviceId`、`firstOccurredAt`、`clearedAt`、`condition`（`ACTIVE | CLEARED`）、`currentSeverity`、`peakSeverity` 和 `title`。需要注意：前端现有的 `alarmHistoryQuery`（`alarm-api.ts:170-187`）在翻页遇到 `firstOccurredAt < since` 时立即停止；如果一条告警在趋势窗口开始前就已经触发、直到窗口内才恢复或至今仍处于 `ACTIVE`，仅靠 `firstOccurredAt` 截断会漏掉它。
- **控制指令（`modules/command`）**：公开接口 `GET /api/v1/commands?siteId=...&limit=1..100`（`cmd/energy-api/internal/gateway/command.go:350-388`、`modules/command/pkg/commandservice/postgres.go:357-395`）校验站点可见性后按 `created_at DESC, command_id DESC` 返回最近 100 条指令，含 `commandId`、`deviceId`、`pointId`、`capability`、`parameters`、`status`、`createdAt` 及状态流转明细 `transitions`（`apps/hvac-web/src/features/control/command-api.ts:23-38`）。当前人工下发指令在 7 天窗口内远少于 100 条，可直接复用。

### 4. 网页端图表封装与现有缺数连线缺陷

- **已有基础能力**：`apps/hvac-web/src/features/operations/realtime/use-plant-history.ts:24-49` 已跑通用 TanStack Query `useQueries` 按设备分组并发调用 `POST /api/v1/telemetry/device-series:aggregate`；`apps/hvac-web/src/features/operations/systems-devices/components/DeviceInspector.tsx:191-230` 已用 Recharts `<ComposedChart>` 画出 `[minimum, maximum]` 区间包络带加 `average` 均值线。`recharts@3.8.0` 原生支持多图游标联动 `syncId` + `syncMethod="value"`（`node_modules/recharts/types/synchronisation/types.d.ts:37`）、数值时间轴 `<XAxis type="number" scale="time" domain={[fromMs, toMs]} />`（`cartesian/XAxis.d.ts:19,56-81`）、`connectNulls={false}`（`cartesian/Line.d.ts:62-66`）、`type="stepAfter"`（`shape/Curve.d.ts:11`）以及 `<ReferenceArea>` / `<ReferenceLine>`（`cartesian/ReferenceArea.d.ts:21-57`、`cartesian/ReferenceLine.d.ts:45-53`）。
- **现存缺数失真问题**：`apps/hvac-web/src/features/operations/realtime/components/RealtimeWorkspace.tsx:150-160` 和 `DeviceInspector.tsx:191-195,224` 把后端返回的非空 `buckets` 直接塞进默认分类横轴（`type="category"`），既没有补齐空桶，也没有插入 `null`。由于 `aggregate.go:369` 只返回 `included_count > 0` 的非空桶，如果网关在 10:00 到 12:00 断联两小时，图上会把 09:45 和 12:00 的两个点紧挨着连成一条斜线，既看不出两小时缺数，也把横轴时间比例尺挤歪了。

---

## 建议采纳与取舍

### 1. 时间窗口与聚合桶宽规则（改造后采用 ThingsBoard + OpenEMS + Grafana，不采用 MyEMS 全量裸查）

- **不采用** MyEMS 不设上限拉全量原始点的做法；默认一律走服务端分桶聚合，单条序列单次查询最多返回 **720 个桶**（与 ThingsBoard `ts_max_intervals = 700` 同量级）。
- **最多同屏对比 8 个点位**：足够同时放下「主机功率 + 冷冻水供回水温度 + 出水温度设定 + 冷却水进出水温度 + 水泵频率 + 主机运行状态」完整排障组合；同一台设备最多 8 个点位也恰好对齐现有 `keys` 长度上限，跨 2–4 台设备时前端按 `deviceId` 分组并发 2–4 个聚合请求。
- **四个标准窗口的默认桶宽与可选档位**：
  - `1h`（过去 60 分钟）：默认 `1m`（`MINUTE`，60 桶，含均值与最大/最小包络带）；仅在 `1h` 窗口允许手动切到「原始值」看逐次采样点，`6h / 今日 / 7d` 禁用「原始值」。
  - `6h`（过去 6 小时）：默认 `5m`（`FIVE_MINUTE`，72 桶），允许手动切 `1m`（360 桶）或 `15m`（24 桶）。
  - `今日`（按站点时区从当日 `00:00` 到当前时刻，例如今日 14:30 打开即查 `00:00–14:30`）：已过时长 `<= 2h` 自动用 `1m`，`2h–8h` 用 `5m`，`> 8h` 用 `15m`（`QUARTER_HOUR`，全天最多 96 桶，夏令时切换日最多 100 桶）。
  - `7d`（按站点时区最近 7 天，首尾共 168 小时）：默认 `1h`（`HOUR`，168 桶，与 OpenEMS `days <= 12 -> 1h` 一致），允许手动切细到 `15m`（672 桶）。
- **支持昨日同时段虚线对比（改造后采用 ThingsBoard `timeForComparison`）**：在 `1h / 6h / 今日` 窗口提供「对比昨日同时段」开关（`7d` 对应「对比上周同时段」），开启后用相同聚合粒度多查一组按站点本地时刻平移 `-1d`（或 `-7d`）的序列，以同色半透明虚线叠在对应分栏内。

### 2. 连续模拟量、设定值与离散状态的分栏布局（采纳 Grafana 分栏联动，不采用单图多于双 Y 轴混画）

- **按量纲与值形态拆成共享数值时间轴、同步十字准星的上下分栏（`<ComposedChart syncId="ops-trends" syncMethod="value">`）**：
  - **连续模拟量分栏**（如温度栏 `°C`、功率栏 `kW`、频率与开度栏 `Hz / %`）：同量纲点位自动归入同一分栏共用同一把 Y 轴比例尺（例如冷冻水供水温度、回水温度和出水温度设定值都在 `°C` 栏，一眼就能看出供回水温差和实际出水偏离设定值多少）；单张分栏最多允许左右 2 个 Y 轴，超出自动另起一栏。聚合模式下模拟量画均值实线 + `12%` 透明度 `[minimum, maximum]` 包络带；**设定值点位统一用 `type="stepAfter"` 阶梯虚线**，不用斜线插值伪造渐变过程。
  - **底部离散状态泳道栏**（运行状态、启停布尔量、故障码状态）：单独放在底部的紧凑窄栏（每条点位高 `28–36px`），与上方模拟量图完全对齐同一 `<XAxis type="number" domain={[fromMs, toMs]} />` 和十字准星，绝不与 `kW`、`°C` 共用数值 Y 轴。聚合桶内取 `stateCounts` 中占比最高的状态着色（桶内发生过状态切换或出现过异常状态时用斜纹/边框标出，悬停提示框列出该桶完整计数，如 `运行 4 次 / 停机 1 次`）；原始值模式下直接用 `stepAfter` 阶梯线渲染。

### 3. 缺数断线与离线边界规则（采纳 Grafana 显式补空断线规则）

- **横轴一律使用真实毫秒时间戳数值轴（`<XAxis type="number" domain={[fromMs, toMs]} />`）**，不再使用会把空档挤没的字符串分类轴。
- **分桶聚合模式**：前端按窗口起止时间和桶宽生成完整时间槽，后端未返回读数的空槽一律填 `null`（绝不填 `0`），所有 `<Line>` 与 `<Area>` 保持 `connectNulls={false}`，缺 1 个桶即断开连线。
- **原始值模式（仅 `1h`）**：扫描相邻采样点间隔与窗口首尾边界，设断线阈值 `gapThreshold = max(3 * 相邻采样间隔中位数, 5 分钟)`；一旦相邻两点间隔超过 `gapThreshold`，或末条读数距窗口右边界超过 `gapThreshold`（网关已离线），就在前一点采样时间 `+ 1ms` 处插入一个 `null` 点——这样模拟量不会跨停机断联期拉出假斜线，开关量 `stepAfter` 也不会把断联前的「运行」状态一直画到当前时刻。

### 4. 告警区间与控制指令叠加（采纳 Grafana 低透明度区间 + 细虚线 + 事件角标窄轨）

- **独立显隐开关与权限降级**：工具栏提供「告警事件」「控制指令」两个开关；默认只展示与当前已选点位所属 `deviceId` 相关的事件（可切「全站事件」），从源头过滤掉无关设备干扰。会话缺少 `alarm:read` 权限时自动禁用告警开关，不阻塞趋势曲线加载。
- **告警画底色区间，指令画瞬时虚线，文字收进轨道角标**：
  - **告警事件**：按 `[max(firstOccurredAt, fromMs), min(clearedAt ?? nowMs, toMs)]` 在各分栏底层画 `<ReferenceArea>`，填充不透明度控制在 `0.08–0.10`，不在绘图区内部叠文字。
  - **控制指令**：按 `createdAt` 画贯穿分栏的细虚线 `<ReferenceLine strokeDasharray="4 4" />`。
  - **密集折叠**：在共享时间轴紧上方设一条 `24px` 高的事件角标窄轨；像素间距小于 `16px` 的多条告警或指令自动聚合成单个 `+N` 角标，悬停或点击后再展开事件清单（告警标题、峰值级别、起止与恢复时间；指令动作、目标参数、最终执行状态）。

### 5. 最小接口与实现收口建议（供 `#759` 规格定稿直接拍板）

- **不新建跨模块聚合网关接口**：点位历史读数归 `modules/telemetry`，告警列表归 `modules/alarm`（`GET /api/v1/alarms`），控制指令归 `modules/command`（`GET /api/v1/commands`），前端按各自权限独立读取并在同一时间轴对齐，守住模块数据归属边界。
- **对 `POST /api/v1/telemetry/device-series:aggregate` 做 3 处向后兼容的最小扩展**：
  1. `AggregateGranularity` 新增 `MINUTE`（`1m`）和 `FIVE_MINUTE`（`5m`）两档（涉及 `libs/telemetryhistorymodel/aggregation.go`、`modules/telemetry/internal/history/aggregate.go`、`contracts/http/s2-telemetry-public.openapi.json`）。
  2. 把桶数校验拆成「单点位时间槽数 `len(periods) <= 720`」与「单设备单次响应总桶数 `len(buckets) <= 5760`（`8 个点位 × 720 桶`）」，消除单设备多点位查 `6h` 分钟桶时的 `1000` 桶误伤。
  3. 修正 `aggregate.go` 的点位分流条件：
     - `point_type = 'COUNTER'` 继续走累计读数差值桶（`delta`）；
     - `point_type IN ('TELEMETRY', 'SETTING') AND value_type = 'NUMBER'` 走数值聚合桶（`average / minimum / maximum`），让温度与频率设定值可查；
     - `point_type = 'STATE' OR (point_type IN ('TELEMETRY', 'SETTING') AND value_type IN ('STRING', 'BOOLEAN'))` 走状态计数桶（`stateCounts`，布尔值统一记为 `'true' / 'false'`），修复 `songyaxuan.site.json` 与模拟器里 `*.run_state`（`TELEMETRY + STRING`）聚合为空的问题。
- **留给 `#759` 拍板的两个具体边界项**：
  1. **`1h` 窗口是否保留「原始值」档位**：若 `1h` 直接以 `1m` 聚合（含 `min/max/avg`）作为最细档，前端完全不碰 `device-series:query` 的 `DimensionExpensiveQuery` 限流与多点位分页截断问题；若确需保留 `1h` 原始值档，则需在 `modules/telemetry/internal/history/client.go:329-330` 用窗口函数 `ROW_NUMBER() OVER (PARTITION BY telemetry_key ORDER BY sampled_at)` 按每个点位均衡限制返回条数（如每点位 ≤360 条），避免排在前面的 `telemetry_key` 把 `PageSize = 500` 占满。
  2. **跨窗口长告警补齐方式**：默认可由前端同时请求 `condition=ACTIVE` 与按 `firstOccurredAt` 倒序的最近分页并在本地按区间相交合并；若需严格覆盖「窗口开始前很久触发、且已在窗口内恢复」的历史长告警，可在 `GET /api/v1/alarms` 上新增可选的区间交集查询参数 `activeFrom` / `activeTo`（SQL 条件 `first_occurred_at <= $to AND (cleared_at IS NULL OR cleared_at >= $from)`）。
