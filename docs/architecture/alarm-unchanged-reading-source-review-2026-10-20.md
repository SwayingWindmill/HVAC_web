# 告警条件「读数一动不动」：对照 ThingsBoard（2026-10-20）

问题单 #616：一个读数连续 6 小时完全没变就报「提示」（10/16 室外温度一整天卡在 21.5 ℃，没人发现）。

## ThingsBoard v4.4 有什么

版本：tag `v4.4`，commit `6d46786579c8b29caf5102f95ddb133674bed68b`（见 `references/thingsboard-v4.4.md`）。路径相对 `common/data/src/main/java/org/thingsboard/server/common/data/alarm/rule/condition/`。

- 告警条件里对单个读数的判断只有四种：字符串、数值、布尔和「无数据」（`expression/predicate/FilterPredicateType.java`：`STRING`、`NUMERIC`、`BOOLEAN`、`NO_DATA`），可以组合（`ComplexFilterPredicate`）。
- 再就是写 TBEL 脚本（`expression/TbelAlarmConditionExpression.java`）。没有「一段时间内值没变」这种现成条件。

## 我们怎么做

- **拒绝**：用脚本表达。规则文件只描述条件，不放代码。
- **自己做**：告警记下每个读数「从什么时候起是现在这个值」（`alarm_runtime.telemetry_evaluation_input.unchanged_since`，和上一批读数比较，值相同就保留原来的时间）。规则用两个条件读它：`UNCHANGED`（读数还在正常上报，而且已经这么久没变）和 `CHANGED`（这段时间里变过，或者根本没有读数）。代码在 `modules/alarm/pkg/alarmservice/evaluator.go` 和 `telemetry_evaluation.go`。
- 只判断值完全相同，不支持「变化不超过多少」。#609 定的规则就是「完全不变」；会带 ±0.1 抖动的卡死探头，等真遇到再加。
- 规则只看设备自己读出来的点位：机组电表上的温度是从温度采集模块的通道复制过来的（站点文件的 `from`），不再单独判断，一个卡住的探头只报一条。
