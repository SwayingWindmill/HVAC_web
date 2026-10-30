# 能耗小时汇总上线（015）

`infra/telemetry/clickhouse/init/015-energy-hourly-rollup.sql` 做两件事：

1. 换掉 `telemetry_history.counter_deltas_from`。新定义只改了按点位筛读数那一句，让查询用得上 `observations` 的排序键，结果和原来一样。
2. 新建能耗小时汇总：`analytics.energy_hourly_states`（AggregatingMergeTree）、把新能耗记录和更正写进去的两个物化视图、给 Cube 读的视图 `analytics.energy_hourly`，并授权 `cube_analytics_reader` 读它。

`init/` 下的文件只在空数据卷上执行。已有的 ClickHouse（生产、本地）要按下面的顺序手工做一次。

## 先后顺序：先做完这里，再部署

这次合并之后，Cube 的站点用电和设备用电改为整小时读 `analytics.energy_hourly`。部署前生产上没有这个视图，能耗页面、报告和 AI 助手的用电查询都会报错；有视图但没回填，历史月份的整小时会是空的。所以：

1. 先在生产按下面的步骤应用 015、回填、核对。
2. 核对通过后再合并、部署。

015 只新建对象和换一个结果不变的视图，旧版本的程序不读这些新对象，先应用不影响正在跑的版本。

## 步骤

在生产服务器上，`hvac-platform-clickhouse-1` 是 ClickHouse 容器。

1. **确认有可用的备份。** 夜间备份见 `docs/operations/restore-from-backup.md`。最近一份不是今天的，先手动跑一次备份。
2. **停能耗投影。** 能耗投影跑在 `telemetry-worker` 里：

   ```bash
   docker stop hvac-platform-telemetry-worker-1
   ```

   回填期间如果有新的能耗记录写进来，物化视图和回填会各算一次，那个月就会重复。停着的这段时间里新读数留在消息队列里，重新启动后会补上。

3. **应用 015：**

   ```bash
   docker exec -i hvac-platform-clickhouse-1 clickhouse-client --multiquery < infra/telemetry/clickhouse/init/015-energy-hourly-rollup.sql
   ```

   每一句都可以重复执行。

4. **回填历史：**

   ```bash
   infra/telemetry/clickhouse/ops/backfill-energy-hourly-rollup.sh
   ```

   每个（租户、站点、月份）一次插入，从最早的月份开始，每批打印一行 `filled …`。汇总表里已经有数据时脚本会拒绝开始。中途断了用 `--resume` 接着跑：它跳过已经有汇总的月份。断在某一批中间时，那一批可能已经部分写入，`--resume` 会把它当成做完了，所以接着跑完之后一定要做第 5 步。

   脚本默认用 `docker exec -i hvac-platform-clickhouse-1 clickhouse-client`；别的环境用 `CLICKHOUSE_CLIENT` 换成那里的客户端命令（按空格拆开执行）。

5. **核对：**

   ```bash
   infra/telemetry/clickhouse/ops/backfill-energy-hourly-rollup.sh --verify
   ```

   按（租户、站点、月份）比较可见能耗记录和汇总的区间数与电量（电量差 1e-6 kWh 以内算相同）。全部一致时打印 `energy hourly rollup matches …` 并返回 0；有差异时列出每个对不上的月份并返回 1。对不上时不要启动 `telemetry-worker`，先查原因。

6. **启动能耗投影：**

   ```bash
   docker start hvac-platform-telemetry-worker-1
   ```

7. 等停机期间积压的读数投影完（几分钟到十几分钟，看停了多久），**再跑一次 `--verify`**。这一次检查的是物化视图有没有接住新的记录和更正。
8. 合并、部署。

## 汇总怎么保持正确

- 新能耗记录插入时，物化视图 `energy_facts_to_hourly` 把它加进所在小时。
- 一条记录被更正（同一个 `fact_id` 系列出现 `fact_revision + 1`）时，`energy_fact_corrections_to_hourly` 把旧版本从它的小时里减掉，新版本由上一个物化视图加上。
- `energy_hourly` 只返回区间数不为 0 的组，所以一个小时的记录全被更正成别的质量时，旧质量那一行会消失。

物化视图在源表写入之后执行。物化视图失败（例如 ClickHouse 在两步之间重启）时，源表已经有这条记录而汇总没有，`--verify` 会报出那个月。出现这种情况时，在停掉 `telemetry-worker` 的情况下删掉那个月的汇总行再用 `--resume` 补（删数据需要人确认后手工做）。

## 已知的差别

- **按质量的数据版本。** `max_dataset_revision` 按质量分组时，被更正走的那个版本的最大值仍留在汇总里。整个站点的数据新鲜度（产品现在用的）不受影响。
- **半小时时区。** 整小时按 UTC 的小时切。时差不是整小时的时区（例如 UTC+5:30）里，每个本地小时都会被当成跨两个 UTC 小时、读单条记录，结果仍然正确，只是用不上汇总。
- **电费。** `site_energy_cost_facts` 仍读单条记录：电价时段可以在任意分钟切换，按小时汇总会把一个小时里的两种电价混在一起。

## 回退

- 只退程序（Cube 模型和指标计算）：按 `docs/operations/rollback.md` 退部署。旧的 Cube 模型读单条记录，不需要动 ClickHouse。
- 015 里的对象可以留着：物化视图只多一点写入开销。
- `counter_deltas_from` 的新定义和旧定义结果相同，不需要退。确实要退时，把 `004-counter-semantics.sql` 里这个视图的定义用 `CREATE OR REPLACE VIEW` 再执行一次。
- 要彻底去掉汇总时，先删两个物化视图，再删 `energy_hourly`、`energy_interval_fact_revisions` 和 `energy_hourly_states`。这是删数据，需要人确认后手工做。
