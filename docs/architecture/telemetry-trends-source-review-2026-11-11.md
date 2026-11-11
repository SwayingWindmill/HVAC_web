# 多点位历史趋势的降采样、开关量分栏与事件叠加参照核对（2026-11-11）

运维人员排查冷站异常（例如冷冻水出水温度偏高、主机频繁加减载或水泵变频振荡）时，需要把同一时间段内的主机功率、进出水温度、水泵频率、温度设定值和启停运行状态拉到同一条时间轴上比对，并看到这段时间里触发过哪些告警事件、下发过哪些控制指令。本文核对四个外部开源实现的一手源码与本仓库实现（对应 `#766`、PR `#772`，见 `docs/specs/telemetry-trends.md`），记录各项规则的采纳、改造与拒绝决定。

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

## 本仓库现状（已实现）

### 1. ClickHouse 历史表与读数聚合实现

- **原始表与物化汇总表已就位**：`infra/telemetry/clickhouse/init/001-telemetry-history.sql:3-53` 的 `telemetry_history.observations` 按 `(tenant_id, site_id, point_id, sensor_id, device_id, telemetry_key, sampled_at, observation_id)` 排序，记录 `point_type`、`value_type`（`NUMBER | STRING | BOOLEAN | JSON`）、`value_number`、`value_string`、`value_boolean`、毫秒级 UTC `sampled_at`、读数质量 `quality` 与落库时间 `projected_at`。同目录下还建有 `numeric_1min`、`numeric_15min`、`numeric_hourly` 和 `counter_1min`、`counter_15min`、`counter_hourly` 物化表。
- **查询服务直接扫 `observations` 做快照一致性聚合**：`modules/telemetry/internal/history/aggregate.go` 带 `projected_at < snapshot_cutoff` 和读数质量过滤（`VALID_ONLY` 只取 `GOOD`；`USABLE` 取 `GOOD | PARTIAL | ESTIMATED | MANUAL`）按主键前缀聚合 `observations`：
  - `AggregateGranularity`（`libs/telemetryhistorymodel/aggregation.go`、`contracts/http/s2-telemetry-public.openapi.json`）支持 `MINUTE | FIVE_MINUTE | QUARTER_HOUR | HOUR | DAY | WEEK | MONTH` 共 7 档。
  - `MINUTE` 与 `FIVE_MINUTE` 单点位最多允许 `720` 个时间槽，`QUARTER_HOUR` 及以上单点位最多允许 `1000` 个时间槽（兼容用电负荷与月报 10 天 `960` 个 15 分钟桶的分块拉取），且单次请求总桶数受 `时间槽数 × keys 数量 <= 5760` 参数校验与响应上限双层约束。
  - 数值型 `TELEMETRY` 与 `SETTING` 点位统一输出 `gauge` 数值桶（`average / minimum / maximum / first / last / sampleCount`）；`STATE` 点位以及登记为字符串或布尔型的 `TELEMETRY` / `SETTING` 点位（含松雅轩与模拟冷站的 `*.run_state`）统一输出离散 `state` 桶，并返回按频次降序、字典序升序确定性排列的 `stateCounts`。

### 2. 公开接口与权限链路

- **遥测聚合接口**：`POST /api/v1/telemetry/device-series:aggregate` 由 `cmd/energy-api/internal/gateway/device_history.go` 校验 `(deviceId, keys)` 的 `telemetry.history.read` 权限后，签发绑定了请求范围摘要 `ScopeDigest` 的委托令牌交由 `telemetry-query-service` 复核。
- **告警区间交集查询**：`GET /api/v1/alarms`（`cmd/energy-api/internal/gateway/alarm.go`、`modules/alarm/pkg/alarmservice/postgres.go`）要求 `alarm.list`（网关侧映射 `alarm:read`），支持可选参数 `activeFrom` / `activeTo`，按 `first_occurred_at <= activeTo AND (cleared_at IS NULL OR cleared_at >= activeFrom)` 一次覆盖窗口前触发且持续到窗口内的长告警与窗口内新触发告警。
- **控制指令查询**：`GET /api/v1/commands?siteId=...`（`cmd/energy-api/internal/gateway/command.go`）校验 `site.read` 站点可见性后返回最近 100 条控制指令及状态流转时间。

### 3. 网页端分栏曲线、缺数留白与排障联动

- **统一毫秒时间槽补空**：`apps/hvac-web/src/features/operations/trends/timeline.ts` 按起止时间、站点时区和聚合粒度生成连续时间槽，缺读数的槽位显式填 `null`（绝不填 `0`）。历史趋势（`TrendsChartStack.tsx`）、实时运行（`RealtimeWorkspace.tsx`）和设备详情抽屉（`DeviceInspector.tsx`）均使用 `<XAxis type="number" scale="time" domain={[fromMs, toMs]} />` 搭配 `connectNulls={false}`，断联时段直接留白断线。
- **上下分栏像素级对齐与双向游标同步**：`apps/hvac-web/src/features/operations/trends/components/` 将事件时间轴窄轨（`TrendsEventRail.tsx`）、按物理单位拆分的模拟量曲线栈（`TrendsChartStack.tsx`）和底部设备启停状态条（`TrendsStateLane.tsx`）统一收敛在同一水平绘图区内边距（左 `48px` 纵轴区、右 `12px` 留白）中，悬停任一曲线或状态条都会双向联动十字游标与时刻读数。

---

## 采纳、改造与拒绝决定

### 1. 时间窗口、聚合档位与原始值取舍

- **已拒绝（MyEMS 全量裸查原始表）**：历史趋势不拉取无上限原始行，一律走服务端分桶聚合。
- **已拒绝（`1 小时` 窗口开放 `device-series:query` 秒级原始值模式）**：`1 小时` 窗口直接以 `MINUTE`（`1 分钟`，60 桶，含均值实线与最小/最大包络带）作为最细档位，既能看清主机加减载与水温短时波动，又不触发 `device-series:query` 的 `DimensionExpensiveQuery` 限流扣减与多点位分页截断。
- **已改造采纳（ThingsBoard + OpenEMS + Grafana 分桶规则）**：
  - 同屏最多对比 `8` 个点位，前端按 `deviceId` 分组并发调用 `device-series:aggregate`。
  - 标准窗口默认档位：`1 小时 → 1 分钟`（60 桶）、`6 小时 → 5 分钟`（72 桶）、`今日`（站点本地零点至今，`<= 2h` 取 `1 分钟`、`2h–8h` 取 `5 分钟`、`> 8h` 取 `15 分钟`）、`7 天 → 1 小时`（168 桶，可切 `15 分钟` 672 桶），同时支持 `自定义` 起止时段选择。
  - 支持勾选「对比昨日同时段」（跨度超过 1 天时自动切换为「对比上周同时段」），按站点本地时刻平移拉取对照序列并以半透明虚线叠画。

### 2. 连续模拟量分栏、设定值阶梯线与离散状态条

- **已采纳（Grafana 模拟量分栏 + 独立状态条）**：
  - 连续模拟量按物理单位（`kW`、`°C`、`m³/h`、`Hz` 等）拆成共享时间轴的上下独立分栏，每栏单一纵轴，分类色严格按每个分栏内 `chart-1..chart-4` 顺序分配，不混用状态语义色。
  - 模拟量画均值实线 + `12%` 透明度 `[minimum, maximum]` 包络带；设定值（`SETTING`）画 `type="stepAfter"` 阶梯虚线。
  - 离散运行状态收进底部紧凑的「设备启停状态」条，优先按桶内 `stateCounts` 样本数最多的状态着色（样本数相同时按 `故障 > 运行 > 停止` 优先），并按 `RUNNING` 样本占比计算运行比例。

### 3. 告警区间、控制指令竖线与密集事件折叠

- **已改造采纳（Grafana 低透明度区间 + 瞬时竖线 + 独立窄轨折叠）**：
  - 工具栏提供「叠加告警」（需 `alarm.list` 权限）与「叠加控制指令」（需 `site.read` 权限）两个独立开关，缺少对应权限时仅禁用该开关，不阻塞趋势图渲染。
  - 告警按真实起止时刻夹紧到 `[fromMs, toMs]` 画低透明度 `<ReferenceArea>`；控制指令按真实执行/创建时间画细虚线 `<ReferenceLine>`。
  - 上方事件时间轴窄轨按 `2.5%` 宽度阈值把密集邻近事件自动聚合成 `+N 条事件` 胶囊（告警胶囊带级别几何符号与软底色），点开后可跳去告警详情、运行调节，或在有 `work-order.create` 权限时直接带入趋势时段与排查点位创建工单。
