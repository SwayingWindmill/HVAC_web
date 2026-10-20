# 补收积压读数时告警按什么时间判断：对照 ThingsBoard（2026-10-20）

问题单 #661。告警补收积压读数时，拿收到读数的这一刻去判断旧读数。补到落后 30 分钟左右时，29 分 59 秒前的读数算「有」、30 分 01 秒前的算「没有」，「读数长时间没有更新」就报了又马上恢复。2026-10-20 12:47 有 5 条这样的。

## ThingsBoard v4.4 怎么做

版本：tag `v4.4`，commit `6d46786579c8b29caf5102f95ddb133674bed68b`（见 `references/thingsboard-v4.4.md`）。路径相对 `application/src/main/java/org/thingsboard/server/service/cf/ctx/state/alarm/`。

- **计划时段按读数的时间**：读数触发判断时，「现在在不在规则的生效时段里」用的是这批读数里最新的时间（`AlarmRuleState.eval`：`newEvent ? state.getLatestTimestamp() : System.currentTimeMillis()`）。
- **「持续多久」从读数的时间算起**：持续条件第一次成立时，起点记读数的时间，但持续了多久仍然用当前时间去减（`AlarmRuleState.evalDuration`）。
- **新告警的开始时间记读数的时间**，读数时间比当前时间还晚时改用当前时间（`AlarmCalculatedFieldState` 新建告警时的 `startTs = getLatestTimestamp()`）；更新已有告警时记当前时间（`setEndTs(System.currentTimeMillis())`）。
- **「多久没数据」用当前时间**：`evalNoDataPredicate` 是 `System.currentTimeMillis() - argument.getTs()`。所以晚到的读数在 ThingsBoard 里一样会被判成「没数据」。

## 我们怎么做

- **自己做：站点时钟**。判断时用的「现在」，是站点最新一批读数的时间，加上告警收到它以来过了多久，不超过当前时间（`modules/alarm/pkg/alarmservice/evaluator_postgres.go` 的 `siteClock`）。
  - 读数准时送到时，站点时钟就是现在。
  - 补收积压时，读数一批接一批地到，站点时钟跟着读数的时间走，40 分钟前的读数按 40 分钟前判断，不会被当成「太旧」。
  - 读数停了，站点时钟从最后一批读数开始照常往前走，30 分钟后定时复查照样报「读数长时间没有更新」，15 分钟后照样报「网关上报中断」。
  - 读数到达时的判断和每分钟的定时复查用的是同一个站点时钟，不会一个说「有」一个说「没有」。
- **改造**：ThingsBoard 只把计划时段和持续起点挪到读数的时间，「多久没数据」仍然用当前时间，解决不了这个问题。我们把所有和时间有关的判断（读数是否太旧、多久没数据、持续多久、计划时段、「一动不动」）都放到站点时钟上。
- **拒绝**：告警的发生和恢复时间记读数的时间。告警记录不允许时间倒退：比如告警已经在 10:49 报出，补收进来一批 10:30 的读数要恢复它，恢复时间不能早于 10:49。人对告警的确认也是按现在记的。所以告警上记的仍然是平台做出决定的时间（`recordAt`），读数自己的时间在告警的证据里。
