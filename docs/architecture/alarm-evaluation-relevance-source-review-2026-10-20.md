# 一批读数只算用得到的告警规则：对照 ThingsBoard（2026-10-20）

问题单 #659。以前告警每收到一台设备的一批读数，就把站点全部规则（松雅轩 34 条）挨个算一遍，每条规则一个事务。网关断线恢复后，告警追积压很慢。

## ThingsBoard v4.4 怎么做

版本：tag `v4.4`，commit `6d46786579c8b29caf5102f95ddb133674bed68b`（见 `references/thingsboard-v4.4.md`）。v4.4 的告警规则是 `ALARM` 类型的「计算字段」（calculated field）。路径相对 `application/src/main/java/org/thingsboard/server/`。

- **按设备找规则**：读数到达时，只取挂在这台设备（或它的设备类型）上的计算字段（`actors/calculatedField/CalculatedFieldManagerMessageProcessor.java` 的 `onTelemetryMsg`，`getCalculatedFieldsByEntityId`）。
- **按读数名再筛一遍**：规则用到的读数登记在 `CalculatedFieldCtx` 里，分成本设备的（`mainEntityArguments`）和别的设备的（`linkedEntityArguments`）。只有这批读数里有规则用到的读数名，才算这条规则（`service/cf/ctx/state/CalculatedFieldCtx.java` 的 `matches`、`linkMatches`）。
- **别的设备的读数也能触发**：规则用到别的设备的读数时，那台设备的读数会通过「链接」送给这条规则（`filterCalculatedFieldLinks`）。
- **只跟时间有关的变化单独复查**：规则里有「持续多久」「多久没数据」这类条件时，由定时复查任务按间隔重新算（`scheduleCfsReevaluation`；`CalculatedFieldCtx.requiresScheduledReevaluation`），不靠读数触发。

## 我们怎么做

- **采纳**：读数到达时只算用到这台设备读数的规则；只跟时间有关的变化（读数过期、多久没数据、持续时间到点、「一动不动」到时间）交给每分钟一次的定时复查（#615）。每次判断都记下下次该复查的时间，所以不会漏。代码在 `modules/alarm/pkg/alarmservice/telemetry_evaluation.go` 的 `readsDevice`。
- **改造**：ThingsBoard 预先给每台设备建好规则索引。我们站点上只有几十条规则，每批读数到达时现读一次规则、看它的条件里有没有这台设备的读数（站点快照里读数的名字是「设备编号/读数名」），不另外建索引。
- **不按读数名再筛**：一台设备的一批读数一般都会带上它的全部读数，按设备筛已经够用。
- **结果**：「网关上报中断」用到所有设备的读数，任何一台设备的读数到了都会算它，所以能马上恢复。松雅轩一批温度采集模块的读数要算 20 条规则，冷量计 5 条，机组电表 3 条，其他设备 2 条；以前每批都是 34 条。
