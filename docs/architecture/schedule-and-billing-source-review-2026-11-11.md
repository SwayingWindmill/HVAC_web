# 冷站周运行计划、例外排班与两部制电费账单核对源码参照记录（2026-11-11）

本文针对路线图 `#757` 中 `04 · 运行计划` 与 `05 · 电费账单` 两块功能，核对固定版本的外部参照源码、行业标准与本仓库现有实现，明确数据模型、核对口径与模块归属取舍（问题单 `#762`）。

| 参照对象 | 固定版本 / 提交 | 核对范围 |
|---|---|---|
| OpenEMS | `2026.9.0`（`14f0ded`） | `io.openems.edge.scheduler.daily`、`io.openems.edge.scheduler.jscalendar`、`io.openems.common.jscalendar` |
| MyEMS | `v6.9.0`（`b360f5b`） | `myems_system_db.sql`、`myems_billing_db.sql`、`myems_energy_plan_db.sql`、`myems_historical_db.sql`、`workingcalendar.py`、`offlinemeter.py`、`offline_meter_billing.py`、`tariff.py`、`tenantbill.py`、`spaceplan.py`、`spacecost.py`、`spaceenergycategory.py` |
| ANSI/ASHRAE Standard 135 & Guideline 36 | BACnet Clause 12.24 / 12.9；Guideline 36 | `Schedule`（`Weekly_Schedule` + `Exception_Schedule` + `Schedule_Default`）、`Calendar`、预冷提前开机与停机延时宽限窗 |
| IETF RFC 8984 | JSCalendar（Section 4.3.3 / 4.3.5） | `recurrenceRules`、`recurrenceOverrides`、本地时间与时区语义 |
| 本仓库 | `main`（`8f454404`） | `modules/scheduler`、`modules/maintenance`、`modules/alarm`、`services/fdd-service`、`modules/registry`、`modules/energy`、`services/settlement-service`、`services/report-service` |

---

## 读到的做法

### 一、周运行计划、例外排班与运行状态核对

1. **OpenEMS `scheduler.daily`（纯日内时刻表，无星期与节假日）**
   - `io.openems.edge.scheduler.daily/src/io/openems/edge/scheduler/daily/Config.java:24-41` 与 `SchedulerDailyImpl.java:35,71-105`：配置为按本地时刻排好序的 JSON 数组，内存里存成 `TreeMap<LocalTime, LinkedHashSet<String>>`。运行时调 `controllerSchedule.lowerEntry(LocalTime.now(clock))` 取最近一个早于当前时刻的槽位；若当天零点后还没有槽位，则回绕取前一天的 `lastEntry()`（`SchedulerDailyImpl.java:94-99`）。
   - **局限**：第一，不分星期几，也没有任何日期例外；第二，`lowerEntry` 采用严格小于 `<`，导致在整点边界时刻（例如 `00:00`，见 `SchedulerDailyImplTest.java:52-55`）仍命中上一个槽位，不符合左闭右开 `[start, end)` 的时间片直觉。

2. **OpenEMS `scheduler.jscalendar`（RFC 8984 子集：支持跨午夜持续时长，但未实现日期例外）**
   - `io.openems.edge.scheduler.jscalendar/src/io/openems/edge/scheduler/jscalendar/SchedulerJSCalendarImpl.java:43,70-97` 与 `io.openems.common/src/io/openems/common/jscalendar/JSCalendar.java:65-71,222-275,385-405,530-569,700-763,916-943,995-1314`：
   - **周循环与跨午夜表达**：`Task<PAYLOAD>`（`JSCalendar.java:700-701`）保存 `(uid, updated, start, duration, recurrenceRules, payload)`，时间片按 `[start, end)` 左闭右开判定（`START` 含边界见 `:256`，`END` 不含边界见 `:266`）。只配日内时刻时，`start` 挂在哨兵日期 `1970-01-01` 上（`:751-753,801-803`），配合 `WEEKLY` + `byDay`（`mo`..`su`，`:1033-1040`）表达周排班。查 `[from, to)` 内生效实例时，`getOccurrencesBetween` 先把查询起点前推一个 `duration`（`from.minus(this.duration)`，`:919-921`），因此周五 `22:00` 开始、持续 `PT8H` 的跨午夜任务在周六 `00:00–06:00` 会自然命中，不用手拆两条规则。
   - **未实现真正的例外覆盖**：虽然注释引用了 RFC 8984，但源码里**没有实现** RFC 8984 第 4.3.5 节的 `recurrenceOverrides` 或排除日期规则（`:700-763`）。多个任务重叠时，`_getOneTasksBetween`（`:385-405`）按任务在数组中的先后顺序调用 `addToOccurrencesBetween`（`:530-569`），把后序任务被前序任务占用的时间段切碎挖空（见 `SchedulerJSCalendarImplTest.java:41-61,108-127`）。此外载荷只有 `Payload(String[] controllerIds)`（`Utils.java:20`），缺少「某天整天停机覆盖周计划」及「临时加班申请—审批」语义。

3. **MyEMS 工作日历（仅做整天非工作日报表分桶，无日内排班与偏离提醒）**
   - `database/install/myems_system_db.sql:2849-2868` 与 `myems-api/core/workingcalendar.py:114-126,667-686,977-1004`：只建了 `tbl_working_calendars (id, name, description)` 和 `tbl_working_calendars_non_working_days (id, working_calendar_id, date_local, description)`，通过关联表绑到空间、车间、商铺、租户（`myems_system_db.sql:2003,2272,2474,2614`）。另有一张 `tbl_control_modes_times`（`myems_system_db.sql:193-216`）仅存 `(start_time_of_day, end_time_of_day, power_value)` 供储能/微网功率给定使用，既不分星期也不连工作日历。
   - **局限**：
     1. **只有单向「非工作日」日期，没有日内开机时段与加班覆盖**：表里只有 `date_local DATE`，记不了「工作日 `08:00–22:00` 供冷」。管理端习惯先把全年周末批量导入 `non_working_days`（`myems-admin/views/settings/workingcalendar/batch-weekend-nonworkingday.model.html`），一旦遇到周六临时加班供冷，只能把那天的非工作日记录直接删掉，留不下谁批准了加班、加班到几点的审计记录。
     2. **只用于事后能耗报表分桶，不做运行偏离检测**：`myems-api/reports/spaceenergycategory.py:434-507,512-590` 只在汇总空间能耗时用 `if current_datetime in reporting['non_working_days']` 把日电量归入「非工作日小计 / 工作日小计」；当 `period_type == 'hourly'` 时，`current_datetime` 格式化为 `'YYYY-MM-DD HH:MM:SS'`（`:569`），而 `non_working_days` 存的是 `'YYYY-MM-DD'`（`:527`），字符串相等判断直接失效（`:586`）。

4. **BACnet `Schedule` / `Calendar` 与 ASHRAE Guideline 36 工程实践**
   - ANSI/ASHRAE Standard 135 第 12.24 节 `Schedule` 对象将建筑设备排班分为三层：`Effective_Period`（排班生效起止日期）、`Weekly_Schedule`（周一到周日 7 天各自的有序时间—状态表）、`Exception_Schedule`（按单日、日期区间或第 12.9 节 `Calendar` 日期列表触发的例外事件，带优先级 `1..16`，整段优先于 `Weekly_Schedule`）以及默认兜底值 `Schedule_Default`（未命中任何时段时回落到默认状态，如 `OFF`）。需要注意 BACnet 原生按日表在午夜 `00:00` 会回落到 `Schedule_Default`，跨午夜运行必须在次日 `00:00` 补写续段。
   - ASHRAE Guideline 36 在商业冷站启停序列中明确了两类正常物理过渡：**预冷提前开机（Optimum Start）** 会在计划供冷开始前提前启动冷机与水泵把管网水温拉到设定值；**停机延时（Coast-down / Pump Delay）** 会在冷机停机后让冷冻水泵、冷却水泵和冷却塔风机继续运转 15–30 分钟带走余热。同时，停机期间主机曲轴箱电加热器与控制柜仍有待机功耗，过渡季供冷时段内建筑无冷负荷时冷机也会正常待机停机。因此核对偏离计划运行必须带启停宽限窗并读取已证实的运行状态，不能按「计划外功率大于 0」或「计划内某分钟未开机」简单报警。

### 二、月度电费预算与两部制电费账单核对

1. **MyEMS 成本中心、电价与计费库（仅有电度分时估算，无两部制结算账单表）**
   - `database/install/myems_system_db.sql:223-241,2284-2314` 与 `myems-aggregation/tariff.py:32-162`：`tbl_cost_centers` 关联 `tbl_tariffs` 与 `tbl_tariffs_timeofuses`（按 `toppeak`、`onpeak`、`midpeak`、`offpeak`、`deep` 配置每度电单价 `price`）。全库**没有**变压器容量基本电费（元/kVA·月）、最大需量基本电费（元/kW·月）、功率因数调整电费（力调电费）或政府性基金及附加字段。
   - `database/install/myems_billing_db.sql:15-607`、`myems-api/reports/spacecost.py:344-350`、`myems-api/reports/tenantbill.py:273-398,456-473`：`myems_billing_db` 里**没有任何电费账单头表或票面明细表**，全部是 24 张 `tbl_*_hourly (id, *_id, start_datetime_utc, actual_value)` 小时级时间序列表，存的是后台用「小时电量 × 分时电度单价」算出来的估算值；`tenantbill.py` 只是把租户名下小时估算费用按区间求和导出收费单，无法录入或核对供电局开出的 `结算电费单`。

2. **MyEMS 能源计划库与文件导入（小时电量计划表 + 开源版缺失后台解析器）**
   - `database/install/myems_energy_plan_db.sql:15-608` 与 `myems-api/reports/spaceplan.py:558-653`：`myems_energy_plan_db` 把同样的 24 张 `tbl_*_hourly` 表复制了一遍，用来存逐小时计划能耗（kWh），在 `spaceplan.py` 里逐小时算 `values_plan - values_actual`，并不是商业业主财务考核用的「月度电费预算金额（元）」。
   - 更关键的是，`myems-api/core/costfile.py:158-167` 与 `energyplanfile.py:167-176` 把用户上传的 Excel 二进制写进 `myems_historical_db.tbl_cost_files`（`myems_historical_db.sql:46-53`）和 `tbl_energy_plan_files`（`myems_historical_db.sql:183-190`）并标为 `status = 'new'`，但开源仓库的 `myems-normalization/` 目录下只有 `offlinemeter.py`，没有任何消费这两张文件表的解析进程。

3. **MyEMS `offlinemeter` 手工表读数平摊（应明确拒绝的反模式）**
   - `myems-normalization/offlinemeter.py:296-330` 与 `myems-aggregation/offline_meter_billing.py:256-273`：针对没有远传的 `tbl_offline_meters`（`myems_system_db.sql:1469-1485`），Excel 按天填入电量后，后台直接按 24 小时均匀平摊写入 `tbl_offline_meter_hourly`（`daily_value / 24`，见 `offlinemeter.py:301-302`），再乘以分时电价算费（`offline_meter_billing.py:271`）。商业冷站用电集中在白天峰平段，把人工账单均匀摊进夜间谷段会直接算错峰谷电费并污染小时历史读数。

---

## 本仓库现状

1. **`modules/scheduler` 只管后台计算作业，不管现场设备排班**
   - `infra/registry/postgres/init/009l-scheduler-job-v1.sql:15-85` 与 `019-fdd-scheduled-evaluation.sql:10-25`：`core_registry.schedule_definitions`、`job_instances`、`job_attempts` 存的是平台内部定时任务（5 段 cron 或固定秒数间隔，触发 `FDD_EVALUATION`、`REPORT_GENERATE`、`OPTIMIZATION_RUN` 等），和冷站现场几点开机供冷是完全不同的概念，不能混用（已在 `GLOSSARY.md` 区分 `调度定义` 与 `运行计划`）。

2. **`modules/maintenance` 只管平台自身巡检与租户退役，没有现场维保或排班表**
   - `infra/registry/postgres/init/009j-operations-support-domains-v2.sql:135-190`、`013-s23-maintenance-retirement.sql:29-88` 与 `modules/maintenance/pkg/maintenance/domain.go:10-46`：`maintenance_events` 和 `tenant_retirement_runs` 只处理证书过期扫描（`CERTIFICATE_EXPIRY_SCAN`）、死信清理（`DEAD_WORK_DISPOSITION`）和租户数据退役（`TENANT_RETIREMENT`），没有任何冷站排班或设备停机窗口表。`modules/alarm`（`005_s13_alarm_orthogonal.sql`）也只有单条告警上的临时静默字段 `suppression jsonb`，没有站点级日历。

3. **`services/fdd-service` 已有分钟级「冷站是否在运行」标准判定（`工况门控`）**
   - `infra/registry/postgres/init/033-fdd-operating-gates.sql`、`services/fdd-service/internal/fdd/gates.go:40-67,193-250` 与 `docs/specs/fdd-rule-definition.md`：已在分钟网格上实现了可重放的运行状态判定——读取 `core_registry.assets` 上的冷机额定功率 `rated_power` 与绑定设备，在保持窗口（`gateHoldMinutes: 10`）内判断 `chillerRunning`（有额定功率时 `power_total >= 0.1 * rated_power`，否则回退判断 `run_state == 'RUNNING'`）与 `chilledWaterPumpRunning`（`run_state == 'RUNNING'`）。
   - 对照松雅轩现场配置（`deploy/sites/songyaxuan.site.json:237,811`）：网关在冷机电表总有功功率 > 5 kW 时报 `chiller.run_state = RUNNING`，在冷冻水总管流量 > 10 m³/h 时报 `chwp.run_state = RUNNING`（电表每 5 分钟采样一帧，温度与流量每 1 分钟采样一帧）。这套口径可以直接复用来判断冷站是否真正在供冷运行。

4. **分时 `电价表` 与 Cube `估算电费` 已实现并支持任意日期窗口查询**
   - `modules/registry/internal/core/tariff.go`、`libs/tariffmodel/windows.go`、`028-retire-own-tariff-pricing.sql` 与 `docs/specs/estimated-energy-cost.md`：`core_registry.tariffs` / `tariff_versions` / `tariff_periods` / `tariff_assignments` 由嵌入 `energy-api` 的 `platform-core-service`（`cmd/energy-api/embedded_energy.go:39,104-111`）读写，时间片已采用站点本地日内分钟数 `[start_minute, end_minute)`（`0..1440`）；`telemetry-worker` 将其展开到 ClickHouse `analytics.tariff_price_windows`；Cube `site_energy_cost` 按 15 分钟电量桶关联电度单价算出冷站 `估算电费`，天然支持按自然月或按任意跨月抄表起止日期聚合。

5. **`services/settlement-service` 仅有命令行底座，未接入网关且模型面向自底向上重算**
   - `infra/registry/postgres/init/009b-settlement-foundation-v2.sql:25-510`、`012-s19-settlement-projections.sql:20-75` 与 `services/settlement-service/internal/settlement/engine.go:63-195`：虽然 `core_registry` 里已有 `settlement_boundaries`、`settlement_periods`、`settlement_snapshots`、`settlement_change_candidates`（其中 `reason_code` 在 `009b:451` 预留了 `'UTILITY_BILL'` 和 `'MANUAL_ENTRY'`），但：
     1. `settlement-service` 目前只是读写标准输入输出的 CLI 程序（`services/settlement-service/cmd/settlement-service/main.go`），未接入 `energy-api` 路由，也未部署进 `deploy/platform/phase1/compose.yaml`（见 `docs/HANDOFF.md:26`）；
     2. `Engine.RunPeriod` 的逻辑是读 ClickHouse `Metric` 明细乘以平台电价表自底向上算快照，**没有**保存供电局外部 `结算电费单` 票面明细（电度电费、容量/需量基本电费、力调电费、整栋总表 vs 冷站分表口径）的表，也没有 `月度电费预算` 表；
     3. 已登记的 SQL 迁移文件均已冻结，新增结构必须追加新迁移文件。

6. **`services/report-service` 的月度 `报告快照` 不可变，且生成早于供电局出账**
   - `infra/registry/postgres/init/022-report-snapshots.sql` 与 `docs/specs/report-snapshots.md`：`report_snapshots` 在每月初（如次月 1 日 03:00）自动固化上一自然月数据，其中 `energyCostCNY` 明确只含冷站分时 `估算电费`。而供电局两部制电费单往往在次月中下旬才出账，且抄表周期经常跨自然月（例如上月 25 日至本月 24 日）。

---

## 建议采纳与取舍

### 一、冷站运行计划与运行例外（对应路线图 `04`）

1. **采用「站点周运行计划 + 整日优先的运行例外」双层模型（借鉴 BACnet，对齐本仓库 `tariff_periods` 分钟区间写法）**
   - **周运行计划（`core_registry.operating_schedules` + `operating_schedule_slots`）**：
     - 每个站点同一时间最多一套生效中的周运行计划，保存版本号、名称、站点 IANA 时区快照（如 `Asia/Shanghai`，随站点时区校验）与发布时间；未命中任何供冷时段的时间默认均为停机状态 `OFF`（对应 BACnet `Schedule_Default`）。
     - 常驻时段按 `day_of_week`（周一 `1` 至周日 `7`）+ 日内分钟半开区间 `[start_minute, end_minute)`（`0 <= start_minute < end_minute <= 1440`）存 `COOLING` 时段及可选参考供水温度设定值（`target_chws_temp_c`，第一版仅作值班提示，不下发边缘控制指令），在数据库层用 PostgreSQL `int4range` 排他约束禁止同日重叠。
     - **跨午夜时段处理**：前端表单允许值班主管直接录跨午夜时段（例如周五 `22:00` 至次日 `06:00`），后端保存时校验并拆成周五 `[1320, 1440)` 与周六 `[0, 360)` 两条记录（共用同一个 `span_group_id` 供前端合并回显）。这样既避免人工漏配次日零点续段，又让按任意本地日期展开计划只需查当天记录，不必像 OpenEMS `JSCalendar` 那样每次回溯前一天。
   - **运行例外与审批（`core_registry.operating_exceptions`）**：
     - 按站点本地起止日期 `[start_date, end_date]`（首尾都算，例如国庆停机 `2026-10-01` 至 `2026-10-07` 共 7 整天）登记，带例外类型（`HOLIDAY_SHUTDOWN` 节假日停机、`OVERTIME_COOLING` 临时加班供冷、`MAINTENANCE_WINDOW` 检修停机）与替代时段列表（整天停机即替代时段为空；周六加班即填入当天如 `[540, 1080)` 对应 `09:00–18:00`）。
     - **整日覆盖规则**：只要某个本地日期命中状态为 `APPROVED` 的 `运行例外`，当天 00:00–24:00 完全按该例外的替代时段生效，不再叠加当天原本的周运行计划，彻底避免 OpenEMS 数组顺序切分留下的半截周时段。同一站点内 `APPROVED` 例外的日期区间通过排他约束禁止重叠。
     - **审批状态机**：`PENDING_APPROVAL` → `APPROVED` / `REJECTED` / `CANCELLED`，完整记录申请人、提交原因、审批人、审批时间与驳回说明，淘汰 MyEMS 靠删改 `non_working_days` 凑加班的做法。

2. **偏离计划运行核对：复用 FDD `工况门控` 运行口径 + 对称启停宽限窗 + 持续时长防抖**
   - **运行状态口径**：直接复用 `fdd-service` 的分钟级运行判定（至少一台冷机满足 `power_total >= 10%` 额定功率，无额定功率时 `run_state = 'RUNNING'`，或冷冻水泵 `run_state = 'RUNNING'`），排除停机待机电加热功耗干扰。
   - **过渡宽限窗**：计划从 `OFF` 切入 `COOLING` 前留 **预冷提前开机宽限窗**（默认 `45` 分钟，站点可配 `0..120` 分钟）；计划从 `COOLING` 切入 `OFF` 后留 **停机延时宽限窗**（默认 `30` 分钟，站点可配 `0..60` 分钟）。宽限窗内提前开机或延后停泵均视为正常执行计划。
   - **两类偏离分开定规则，严防误报**：
     - **计划停机期仍在运行（`UNSCHEDULED_RUNNING`）**：核心节能抓手（抓夜间或节假日忘关机）。在扣除预冷与停机延时宽限窗后的 `OFF` 时段内，冷站连续运行满 `15` 分钟（跨越至少 3 个 5 分钟电表周期）才触发一条「偏离计划运行提醒」，并在冷站停机或进入合法供冷时段后自动恢复。
     - **计划供冷期未运行（`SCHEDULED_IDLE`）**：过渡季或凉爽时段建筑无冷负荷时冷机停机属正常现象。第一版建议默认只开启「计划停机期仍在运行」提醒；若需开启「计划供冷期未运行」提醒，必须同时满足室外温度高于门槛（例如室外干球温度 >= 22 °C）且连续 `30` 分钟无运行，不能无条件报「应开未开」。

3. **模块与权限归属**
   - 表建在 `core_registry`，读写与审批接口归入已嵌入 `energy-api` 的 `platform-core-service`（单机部署不增加新容器）。
   - 权限边界：运行人员可查看排班并提交 `运行例外` 申请；排班发布与例外审批需要更高管理权限（在能力目录中由站点主管 / 租户管理员角色持有，不在代码里写死角色名）。

### 二、月度电费预算与两部制电费账单核对（对应路线图 `05`）

1. **拒绝的做法**
   - **不平摊回灌时序库**：拒绝 MyEMS `offlinemeter` 把人工账单电量除以 24 塞进小时表的做法，不污染 ClickHouse 遥测与 Cube `site_energy_cost`。
   - **不强塞进 `settlement-service` 的 `Metric` 重算引擎，也不新起独立容器**：单台服务器内存受限（`docs/HANDOFF.md:47`），预算与账单本质是低频财务核对表单，直接放在 `core_registry` 由嵌入 `energy-api` 的 `platform-core-service` 管理（与 `tariffs` 同库同进程）；未上线的 CLI 版 `services/settlement-service` 保持现状不混改。
   - **不回写修改已生成的月度 `报告快照`**：`report_snapshots` 保持不可变。

2. **月度电费预算表（`core_registry.monthly_electricity_budgets`）**
   - 按 `(tenant_id, site_id, billing_month)` 唯一（`billing_month` 为自然月 `YYYY-MM`），保存冷站当月电费预算金额 `budget_amount_cny numeric(14,2)`、备注与更新人。
   - L1 驾驶舱「本月累计电费」卡片直接拿 Cube `site_energy_cost` 算出的当月 1 日零点至今冷站 `估算电费` 对比该预算金额（例如显示「本月估算电费 42,800 元 / 月度预算 60,000 元，已使用 71.3%」），口径一致且实时更新。

3. **两部制结算电费单表（`core_registry.utility_bills`）**
   - 将供电局账单作为一等业务单据原样入库，按中国商业建筑两部制电费单真实票面建模：
     - **账期与实际抄表窗口分离**：既记归属账期 `billing_month`（`YYYY-MM`，用于按月归档检索），也必填站点本地**抄表起止日期** `reading_start_date` 与 `reading_end_date`（首尾都算，例如 `2026-09-25` 至 `2026-10-24` 共 30 整天，折算查询窗口为站点时区 `2026-09-25 00:00` 至 `2026-10-25 00:00`），原生支持供电局跨自然月抄表例日。
     - **表计覆盖口径（`meter_scope`）**：显式区分 `'CHILLER_PLANT'`（供电局专变电表直供冷站）与 `'WHOLE_BUILDING'`（供电局按整栋楼变压器总表出账，冷站仅装内部总表/分表——如松雅轩这类商业建筑的常态）。
     - **票面电量与四段式费用明细**：
       1. 抄表总有功电量 `billed_energy_kwh`（及可选的尖/峰/平/谷分项抄表电量）；
       2. **电度电费** `energy_charge_cny`（>= 0）；
       3. **基本/需量电费**：计收方式 `basic_charge_mode IN ('NONE', 'TRANSFORMER_CAPACITY_KVA', 'MAX_DEMAND_KW')`、计费容量（kVA）或核定最大需量（kW）数值、金额 `basic_charge_cny`（>= 0）；
       4. **力调电费（功率因数调整电费）**：考核标准功率因数（如 `0.90`）、实际功率因数 `actual_power_factor`、金额 `power_factor_adjustment_cny`（**允许正负**：功率因数不达标为正数加收，高于标准值为负数扣减奖励）；
       5. **其他费用** `other_charges_cny`（政府性基金及附加、退补电费等，允许正负）与 **应收总电费** `total_payable_cny`（数据库约束校验等于四项费用之和）。
     - **归档状态**：`DRAFT`（草稿可编辑）→ `CONFIRMED`（确认归档后锁定，后续若供电局退补更正，通过新增修订版本保留审计链）；第一版不强依赖对象存储上传 PDF 发票原件，保留票面单号 `utility_account_no` / `invoice_no` 与备注即可闭环。

4. **两类表计口径下的核对算法（厘清整栋总表 vs 冷站分表边界）**
   - 核对时，后端按账单的**实际抄表日期区间** `[reading_start_date, reading_end_date + 1 day)` 查询 Cube `site_energy` 与 `site_energy_cost`，取出**同时间窗口**内的「平台冷站总表电量（kWh）」与「平台冷站分时估算电费（元）」：
   - **当 `meter_scope = 'CHILLER_PLANT'`（专变直供冷站）时**：直接同口径对账——
     - **电量核对差额** = `账单抄表电量 − 平台同窗口冷站电量`（用于发现 CT 倍率配错、网关断线漏计或抄表起止日填错）；
     - **电度电费核对差额** = `账单电度电费 − 平台同窗口冷站估算电费`（用于发现平台 `电价表` 峰谷单价或时段配置滞后）；
     - **非电度电费占比** = `(基本电费 + 力调电费 + 其他费用) ÷ 应收总电费`（量化需量申报优化与无功补偿改造空间）。
   - **当 `meter_scope = 'WHOLE_BUILDING'`（整栋总表出账、冷站为内部子计量）时**：整栋账单包含照明、电梯、办公插座与末端风机盘管，**禁止**拿整栋电度电费直接减冷站估算电费报「计量误差」。核对视图改为透明展示：
     - **同周期冷站电量占比** = `平台同窗口冷站电量 ÷ 账单整栋抄表总电量`（例如冷站 `85,000 kWh` ÷ 整栋账单 `200,000 kWh` = `42.5%`）；
     - **冷站电度估算电费**（来自平台冷站总表 × 平台 `电价表`）与 **整栋综合平均电度单价**（`应收总电费 ÷ 账单总电量`，含基本电费与力调电费摊入每度电后的真实单价）；
     - **冷站综合电费参考值**：默认按「同周期冷站电量占比 × 整栋应收总电费」折算（同时允许财务可选填入「冷站固定分摊比例」覆盖），界面明确标出计算公式与分子分母。

5. **跨自然月抄表周期与月度 `报告快照` 的衔接**
   - 月度报告（`report_snapshots`）严格维持**自然月（1 日 00:00 至次月 1 日 00:00）**的冷站运行与 `估算电费` 口径，到点自动生成且永不篡改。
   - 账单管理页（L3）按 `billing_month` 归档 `结算电费单`，核对计算严格使用账单自身的 `reading_start_date..reading_end_date` 窗口；月度报告详情页只需按 `month == billing_month` 旁路读取已 `CONFIRMED` 的账单摘要卡片（并醒目注明「供电局抄表周期：9 月 25 日–10 月 24 日」），两套时间轴各司其职、互不覆盖。
