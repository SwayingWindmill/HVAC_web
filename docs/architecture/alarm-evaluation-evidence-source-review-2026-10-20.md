# 告警判断记录写多少：对照 ThingsBoard（2026-10-20）

问题单 #651。告警模块每次判断后更新 `alarm_runtime.alarm_evaluation_state`（每条规则一行，只存最新的判断），并往 `alarm_runtime.alarm_evaluation_event` 追加一行判断记录。以前每收到一批读数就追加一行，带整个站点的读数快照，松雅轩 14 条规则一天约 100 万行、6 GB。

## ThingsBoard v4.4 怎么做

版本：tag `v4.4`，commit `6d46786579c8b29caf5102f95ddb133674bed68b`（见 `references/thingsboard-v4.4.md`）。路径相对 `application/src/main/java/org/thingsboard/server/`。

- 告警规则是 `ALARM` 类型的计算字段，状态按「计算字段 + 实体」一个键存：`service/cf/ctx/state/RocksDBCalculatedFieldStateService.java` 的 `doPersist` 是 `cfRocksDb.put(stateId.toKey(), ...)`，新状态覆盖旧状态，不留历史。
- 每次计算后都存状态（`actors/calculatedField/CalculatedFieldEntityMessageProcessor.java` 的 `finally` 里调 `persistState`），但每次计算的记录只在开了调试时写（同一处的 `persistCalculatedFieldDebugEvent`，受 `DebugModeUtil.isDebugAllAvailable` 控制），调试事件另有保留期（`thingsboard.yml` 的 events TTL）。
- 长期留下来的是告警本身和它的变化：建告警、升级、清除。

## 我们怎么做

- **采纳**：当前判断只存一份、覆盖写（`alarm_evaluation_state` 本来就是这样）。
- **改造**：判断记录不做成调试开关，而是只在结果变了时写一行：状态、判断不了的原因、发报或恢复、规则版本或规则指派变了。这样平时不写，出问题时仍能看到判断结果是什么时候、因为哪些读数变的。代码在 `modules/alarm/pkg/alarmservice/evaluator_postgres.go` 的 `evaluationOutcomeChanged`。
- **采纳**：判断记录有保留期，30 天（迁移 `011_s28_evaluation_event_retention.sql`）。30 天内的行仍然不能改、不能删。
- **拒绝**：每次计算写一行。一条站点规则看得到站点的每个读数，结果没变的判断已经由 `alarm_evaluation_state` 记着。
