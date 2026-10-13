# 分时电价计费：参考平台源码比对（2026-10-13）

规格见 `docs/specs/tou-energy-cost.md`。问题是：一天的电度电费怎样按分时电价计算。

固定版本沿用 `centralized-energy-source-review-2026-09-07.md`：MyEMS v6.7.0（be6e6ce8ddeac57afb04bddb9621501fb555cab0），在固定提交上用 raw 源码读取。

## 读到的做法

- **MyEMS** `myems-aggregation/meter_billing.py`：按计量表逐时段汇总电量后，逐时段取电价相乘，`actual_value = current_energy * current_tariff`，写入计费库；电价或电量缺失的时段直接跳过。
- **MyEMS** `myems-aggregation/tariff.py` 的 `get_energy_category_tariffs`：电价有 `valid_from_datetime_utc`/`valid_through_datetime_utc`，分时价目在 `tbl_tariffs_timeofuses`（`start_time_of_day`、`end_time_of_day`、`price`），按配置的固定分钟时区偏移把每个时段展开成逐时段单价。
- OpenEMS 的电价只用于边缘侧调度（日前价格），不做历史计费；ThingsBoard 没有电价模型。两者不适用。

## 决定

- **ADOPT（MyEMS 的计算方式）**：逐小时用电 × 该小时单价再求和，电价按有效期选版本。
- **ADAPT**：
  - 时区用电价版本的 IANA 时区和日类型（工作日/周末），不用固定分钟偏移，夏令时地区也正确；
  - 电价和用电量缺一小时就整天不出结果，而不是像 MyEMS 那样跳过该小时，避免把偏低的电费当成真实电费；
  - 计费结果是一条带修订号的指标结果（可追溯所用电价版本），不另建计费库。
