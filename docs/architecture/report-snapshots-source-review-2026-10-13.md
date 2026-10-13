# 报告快照：参考平台源码比对（2026-10-13）

规格见 `docs/specs/report-snapshots.md`。问题是：已结束月份的报告怎样在记录变化后仍保持不变。

固定版本沿用 `centralized-energy-source-review-2026-09-07.md`：MyEMS v6.7.0（be6e6ce8ddeac57afb04bddb9621501fb555cab0），在固定提交上用 raw 源码读取。

## 读到的做法

- **MyEMS** `myems-api/reports/spaceenergycategory.py` 的 `on_get`：报表在请求时现算，从能耗库的逐时汇总表 `tbl_space_input_category_hourly` 读数，再用 `utilities.aggregate_hourly_data_by_period` 汇总成日、月。
- **MyEMS** `myems-aggregation/space_energy_input_category.py`：后台进程按计量表逐时汇总写入上述逐时表；数据补齐后会重算覆盖，因此历史报表随之改变，不存在固定的报告版本。
- **REJECT（ThingsBoard、OpenEMS）**：ThingsBoard 社区版没有报表；OpenEMS 是边缘侧能量管理，不出运营报表。

## 决定

- **ADOPT（MyEMS）**：报告数字来自按小时汇总的能耗，再按站点时区汇总成日、月。
- **ADAPT**：已结束月份的报告另存为带版本号、生成后不再修改的快照；重算产生新版本而不是覆盖。MyEMS 的报表随汇总表重算而变，不能回答「当时报出去的是什么数」。
