# ThingsBoard CE Alarm、Notification 与处置状态裁决

状态：`D06_ADJUDICATION_COMPLETE`

审查票：[审查 Alarm、Notification 与处置状态](https://github.com/SwayingWindmill/HVAC_web/issues/239)

本文只裁决 ThingsBoard CE v4.3.1.1 的 Alarm 聚合、规则条件、处置协作、Notification 管理与投递语义。Rule Engine 的通用执行、Queue/Ack/Transaction 属于 D05/#243；Edge 同步属于 D08/#241；通用外部投递适配器属于 D09/#236。本文记录跨域接口，但不在本票替这些域作实现裁决，也不把审查结论写成“能力已经交付”。

裁决词汇：

- `ADOPT`：行为和边界可直接进入目标设计；
- `ADAPT`：吸收模式，但按 HVAC Domain、安全和本地权威重做；
- `KEEP`：本地实现有明确场景或安全证据，应保留；
- `REPLACE`：现有本地或上游行为存在实质冲突，应被目标行为替换；
- `REJECT`：明确不进入本项目；
- `DEFER`：有潜在价值，但当前没有产品、设备或运维证据支持实施。

## 1. 执行结论

固定源码与本地反向审查后的结论不是“复制 ThingsBoard”，也不是“保住现有 Alarm”。两边都存在需要保留和替换的部分：

1. **采用 ThingsBoard 的正交 Alarm 事实，替换本地单一 `status`。** `ACTIVE/CLEARED` 与 `ACK/UNACK` 是不同事实；Assignment、Suppression 也不应挤进同一枚举。本地虽然在 UI 中通过扫描 Transition 把 ACK 重新推导出来，但数据库和 API 仍声明 `ACKNOWLEDGED` 是一个 Status，模型内部已经自相矛盾。
2. **保留本地更强的治理内核。** Tenant/Site Scope、FORCE RLS、Gateway/IAM 精确能力、幂等摘要、乐观并发、Actor/Policy/Correlation、Evidence Timeline 和“Telemetry 不能在浏览器中补造 Alarm”都比 ThingsBoard 的通用 CRUD 边界更适合 HVAC。
3. **替换当前 Alarm 规则最小实现。** 本地只有单点 `SIMPLE_THRESHOLD`，没有 Clear Rule、持续时间、重复计数、计划、迟滞、耐久计时状态或重启恢复；并会在新匹配到来时自动重开已关闭 Alarm。它不足以支撑真实 HVAC 故障和恢复语义。ThingsBoard 的状态化条件机值得吸收，但任意脚本、永不降级 Severity 和通用 Relation 传播不应照搬。
4. **Notification 当前没有可运行产品能力。** Registry SQL 已有 Policy、Template、Message、Delivery、User State 骨架，并正确规定 Notification Read/ACK 不等于 Alarm ACK；但没有发现生产 Notification API、Rule Processor、Scheduler、Delivery Worker、Inbox 或通道调用链。`backend-architecture.v2.json` 的 `ALIGNED` 只能解释为逻辑/DDL 规划，不能解释为功能已实现。
5. **吸收 ThingsBoard 的 Target/Template/Rule/Request/Inbox 分层和 Alarm Escalation 产品模式。** 动态受众、模板预览、用户偏好、站内信未读状态、分阶段升级和状态变化取消后续升级都有直接价值。
6. **拒绝 ThingsBoard 的投递完成语义。** 固定源码在收件人或通道失败时仍把 Request 写成 `SENT`，没有耐久 Attempt/Receipt/Retry/Lease；去重又先于投递生效，失败可能压掉后续通知。目标必须使用 D09 已裁决的 `DeliveryIntent -> DeliveryAttempt -> DeliveryReceipt`，并显式表达部分成功、未知、失败、取消和死信。
7. **取消“人工关闭等于故障恢复”的默认行为。** Alarm Clear/Recovery 只能由恢复条件或有权限、有理由、有证据的人工流程产生；普通 `CLOSE` 不能掩盖仍然 Active 的物理条件。Work Order 负责维护工作是否完成，不替 Alarm 声称设备已恢复。

因此，当前 Alarm 不是“全部推倒”：安全治理、证据、幂等、Scope 和读路径应保留；聚合状态、规则状态和发布身份必须重构。Notification 则是“保留 DDL 中有价值的边界、重新实现运行时”，不能在现有表上继续叠加完成度声明。

## 2. 固定证据基线

| 证据 | 固定值 |
| --- | --- |
| 官方仓库 | `thingsboard/thingsboard` |
| 版本 | `v4.3.1.1` |
| 提交 | `c2a52e46c44e308ddee430e7266b8e10eddde9c4` |
| 许可证 | Apache-2.0 |
| 本地只读源码 | `C:\Users\HaoZhang\AppData\Local\Temp\thingsboard-v4.3.1.1-src` |
| 全功能目录 | `contracts/architecture/thingsboard-ce-capability-inventory.v1.json` 的 D06 |

上游行为以固定提交的源码、测试、DDL 和配置为准；官方文档用于确认公开入口与用户语义，不覆盖源码事实。

主要 ThingsBoard Alarm 证据：

- [Alarm](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/alarm/Alarm.java)、[AlarmStatus](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/alarm/AlarmStatus.java) 与 [AlarmComment](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/alarm/AlarmComment.java)；
- [BaseAlarmService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/alarm/BaseAlarmService.java)、[DefaultTbAlarmService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/entitiy/alarm/DefaultTbAlarmService.java) 与 [DefaultAlarmSubscriptionService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/telemetry/DefaultAlarmSubscriptionService.java)；
- [schema-functions.sql](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/resources/sql/schema-functions.sql) 与 [schema-entities-idx.sql](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/resources/sql/schema-entities-idx.sql)；
- [AlarmCalculatedFieldConfiguration](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/cf/configuration/AlarmCalculatedFieldConfiguration.java)、[AlarmCalculatedFieldState](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/cf/ctx/state/alarm/AlarmCalculatedFieldState.java) 与 [AlarmRuleState](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/cf/ctx/state/alarm/AlarmRuleState.java)；
- [TbCreateAlarmNode](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/rule-engine/rule-engine-components/src/main/java/org/thingsboard/rule/engine/action/TbCreateAlarmNode.java) 与 [TbClearAlarmNode](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/rule-engine/rule-engine-components/src/main/java/org/thingsboard/rule/engine/action/TbClearAlarmNode.java)；
- 官方说明：[Alarm rules](https://thingsboard.io/docs/user-guide/alarm-rules/)、[Alarms](https://thingsboard.io/docs/paas/eu/user-guide/alarms/) 与 [Alarm Query API](https://thingsboard.io/docs/reference/alarm-query-api/)。

主要 ThingsBoard Notification 证据：

- [NotificationTarget](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/notification/targets/NotificationTarget.java)、[NotificationTemplate](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/notification/template/NotificationTemplate.java)、[NotificationRule](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/notification/rule/NotificationRule.java) 与 [NotificationRequest](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/notification/NotificationRequest.java)；
- [DefaultNotificationCenter](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/notification/DefaultNotificationCenter.java)、[DefaultNotificationRuleProcessor](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/notification/rule/DefaultNotificationRuleProcessor.java) 与 [DefaultNotificationSchedulerService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/notification/DefaultNotificationSchedulerService.java)；
- [DefaultNotificationDeduplicationService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/queue/src/main/java/org/thingsboard/server/queue/notification/DefaultNotificationDeduplicationService.java)；
- [NotificationRuleApiTest](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/test/java/org/thingsboard/server/service/notification/NotificationRuleApiTest.java) 与 [NotificationApiTest](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/test/java/org/thingsboard/server/service/notification/NotificationApiTest.java)；
- 官方说明：[Notifications](https://thingsboard.io/docs/user-guide/notifications/) 与 [Notification rule triggers](https://thingsboard.io/docs/reference/notification-system/rule-triggers/)。

主要本地证据：

- `libs/alarmmodel/model.go`, `rule.go` 及其测试；
- `modules/alarm/pkg/alarmservice/{http,store,postgres}.go` 与 migrations 001–005；
- `cmd/energy-api/internal/gateway/alarm.go`、IAM Alarm 授权与 `contracts/http/s4-alarm-public.openapi.json`；
- `apps/hvac-web/src/api/alarms.ts`, `RealAlarms.tsx`, `LocalAlarmLifecycle.tsx`, `real-alarms-projection.ts`；
- `infra/registry/postgres/init/009j-operations-support-domains-v2.sql`；
- `docs/operations/real-product-roadmap.md` 与 `docs/operations/s4-alarm-read-promotion.md`；
- 用户提供的《智慧能源系统_前端交互与能源控制UX规范_V1.1》和《智慧能源系统_前端工程架构与实现设计_V1》；
- D09 的 `docs/architecture/thingsboard-ai-analytics-integrations-adjudication.md`。

## 3. 参考项目功能与它解决的问题

### 3.1 Alarm 聚合与处置

ThingsBoard 把相同 Originator + Alarm Type 的未清除事件聚合为一条 Active Alarm。新匹配更新 Severity、时间和 Details；Clear 后保留历史，下一次匹配创建新 Alarm。ACK、Clear、Assignment 分别记录时间，用户操作追加 System Comment 和 Audit Action。

它解决的问题是：重复遥测异常不应制造无穷告警行；“故障是否仍存在”“是否有人看到”“由谁负责”必须能分别查询；处置历史不能只存在浏览器状态。

### 3.2 Alarm 规则状态

v4.3.1.1 的主要 Alarm Rule 路径位于 Calculated Field。每个 Severity 有 Create Rule，可选 Clear Rule；条件支持 Simple、Duration、Repeating，支持 Any Time、Specific Time、Custom Schedule，并持久/恢复条件状态与定时重新评估。No-data 和表达式结果也参与条件判定。

它解决的问题是：工业告警不是一次 `value > threshold`；持续超限、连续次数、班次计划、数据中断和恢复条件都需要跨消息状态。

### 3.3 Alarm 查询、协作与传播

ThingsBoard 支持 Status、Severity、Type、Assignee、时间与文本查询，提供 Alarm Comment、System Comment，并把 Alarm 投影到父 Entity、Owner 或 Tenant，供不同资源视角查询。

它解决的问题是：操作员需要按责任和严重度定位异常；同一异常可能需要在设备、区域、客户和租户视图中可见；协作信息必须围绕同一 Alarm 聚合。

### 3.4 Notification 管理面

ThingsBoard 把 Notification 分成 Target、Template、Rule、Request 和用户 Inbox。Target 可解析明确用户、Customer 用户、Tenant Admin、所有用户、Originator Owner、Affected User 等；Template 按 WEB、EMAIL、SMS、SLACK、MICROSOFT_TEAMS、MOBILE_APP 提供不同内容；Rule 负责 Trigger、Filter、Recipient Stage 和 Template。

它解决的问题是：谁收、发什么、为什么发、何时发、用户是否已读不能混进 Alarm 或通道代码。

### 3.5 调度、升级与取消

Alarm Notification Rule 可配置多级延迟表。每级生成一个 Request；Clear Rule 匹配 Alarm 状态后删除仍为 `SCHEDULED` 的 Request。Scheduler 在分区接管时重新加载定时请求，因此单进程重启后仍可恢复尚未执行的计划。

它解决的问题是：Critical Alarm 未被处理时逐级通知，恢复或确认后停止无意义升级。

## 4. ThingsBoard Domain 模型

### 4.1 Alarm

```text
Alarm
  identity: tenant/customer/id + originator + type
  condition: acknowledged(bool) + cleared(bool)
  severity: CRITICAL | MAJOR | MINOR | WARNING | INDETERMINATE
  responsibility: assigneeId + assignTs
  time: startTs + endTs + ackTs + clearTs
  details
  propagation flags + relation types

derived AlarmStatus
  ACTIVE_UNACK | ACTIVE_ACK | CLEARED_UNACK | CLEARED_ACK

AlarmComment
  SYSTEM | OTHER

EntityAlarm
  propagated entity -> alarm projection
```

这里最重要的 Pattern 是 Status 由 `cleared` 和 `acknowledged` 派生，而不是把 ACK 当成故障生命周期状态。

### 4.2 Alarm Rule

```text
AlarmCalculatedFieldConfiguration
  arguments
  createRules: Severity -> AlarmRule
  clearRule?: AlarmRule
  propagation configuration

AlarmRule
  condition
  details
  dashboard

AlarmCalculatedFieldState
  per-rule evaluation state
  duration/repeat counters
  schedule state
  timers and persisted snapshot
```

### 4.3 Notification

```text
NotificationTarget
  PLATFORM_USERS | SLACK | MICROSOFT_TEAMS

NotificationTemplate
  notificationType
  deliveryMethod -> channel template

NotificationRule
  trigger config
  recipients config / escalation table
  templateId

NotificationRequest
  targetIds + template/inline template + info + delay + originator + ruleId
  PROCESSING | SCHEDULED | SENT
  aggregate stats/errors

Notification (Inbox item)
  recipient + rendered content + info
  SENT | READ
```

该分层适合产品配置，但 Request 没有逐收件人 Attempt/Receipt，`SENT` 同时承担“处理结束”和“投递成功”两种含义。

## 5. ThingsBoard 核心流程

### 5.1 Create/Update/Clear Alarm

```text
Rule/REST request
  -> validate tenant/originator/type
  -> create_or_update_active_alarm(originator, type)
  -> insert or update Alarm
  -> save entity lifecycle event
  -> create/remove EntityAlarm propagation projections
  -> WebSocket Alarm updates
  -> Notification Rule Processor
```

数据库函数先查询最新未 Clear 的 `(originator_id, type)` 并 `FOR UPDATE`，不存在则 Insert，存在则 Update。问题是查询结果不存在时没有可锁记录，且活动索引不是 Unique；两个并发首次创建仍可能形成两个 Active Alarm。这是不能照搬的并发边界。

ACK/Clear/Assign/Unassign 的 SQL 函数对 Alarm 行加锁，并返回 `modified`，底层是自然幂等的；但用户服务层把重复 ACK/Clear 转为 `BAD_REQUEST`。本地 ACK 返回当前投影、不制造第二次 Transition 的行为更符合 UX 规范，应该保留。

### 5.2 Stateful Alarm evaluation

```text
accepted input snapshot
  -> resolve arguments and quality/no-data state
  -> evaluate schedule
  -> evaluate Simple / Duration / Repeating condition
  -> update persisted rule state and timer
  -> select matched severity
  -> create/update active Alarm
  -> evaluate clear rule and clear when matched
```

固定源码按 Severity 枚举顺序选择，不允许从更高 Severity 降到更低 Severity。该策略便于“只升级”，但会让当前 Severity 长期高于当前物理风险；HVAC 目标应同时保存 `currentSeverity` 和 `peakSeverity`，允许当前 Severity 按规则证据变化，而不是复制永不降级行为。

### 5.3 Notification

```text
Trigger
  -> enabled Rule/filter/dedup/rate limit
  -> one Request per escalation stage
  -> resolve current Target and Template
  -> SCHEDULED or PROCESSING
  -> target -> recipient -> channel send
  -> aggregate stats/errors
  -> Request status SENT
  -> WEB Inbox persistence and WebSocket update
```

Clear Rule 通过 Rule ID + Alarm Originator 查找并删除 `SCHEDULED` Request。该取消流程在上游测试中有覆盖；多延迟级别的实际 Web 通知时间也有端到端测试。

## 6. ThingsBoard 关键代码结构

| 层 | 关键结构 | 客观评价 |
| --- | --- | --- |
| Alarm data | `Alarm`, `AlarmInfo`, `EntityAlarm`, `AlarmComment` | ACK/Clear 正交，协作模型完整；Severity 中的 INDETERMINATE 混入风险等级不适合本项目 |
| Alarm DAO | `BaseAlarmService`, SQL functions | 单行锁与 create/update 返回值清楚；并发首次创建缺唯一约束 |
| Alarm application | `DefaultTbAlarmService`, `DefaultAlarmSubscriptionService` | System Comment、Audit、WS、Notification 串联清楚；重复操作错误语义和异步副作用原子性较弱 |
| Alarm rules | `AlarmCalculatedFieldState`, `AlarmRuleState` | Duration/Repeating/Schedule/State Restore 值得吸收；任意表达式和单调 Severity 需改造 |
| Rule nodes | `TbCreateAlarmNode`, `TbClearAlarmNode` | Created/Updated/Cleared/False 分支清晰；通用 Rule Engine 所有权留给 D05 |
| Notification data | Target/Template/Rule/Request/Notification | 管理面分层优秀；Request/Inbox 状态不足 |
| Notification processing | `DefaultNotificationRuleProcessor`, `DefaultNotificationCenter` | 动态受众、偏好、模板和升级清晰；同步直调通道、错误后 `SENT` 不可靠 |
| Notification scheduling | `DefaultNotificationSchedulerService` | 能从数据库恢复 Scheduled；单线程本地 Timer、无 Lease/Attempt/Retry |
| Notification dedup | `DefaultNotificationDeduplicationService` | Trigger/Rule Key 简洁；Soft Cache/可选外部 Cache 不是业务权威，且先去重后投递 |

## 7. 异常与边界处理

### 7.1 ThingsBoard 做得好的部分

- Alarm Clear 不删除历史；同一 Originator + Type 的 Active Alarm 会更新而不是无限新增。
- ACK、Clear、Assignment 都有独立时间和系统评论。
- Alarm Rule 的 Duration、Repeating、Schedule 会保存状态并安排重新评估。
- Notification 手工请求会校验目标和通道是否匹配；用户可关闭特定类型/通道。
- Scheduled Request 会在分区接管时重新加载；Rule 删除和 Alarm Clear Rule 会取消后续计划。
- 目标重叠时，在一次 Request 内避免向同一 Recipient/Channel 重复发送。
- Notification 记录最多一定数量的收件人错误，便于管理面查看失败样本。

### 7.2 ThingsBoard 需要替换的部分

- `create_or_update_active_alarm` 没有数据库唯一活动键，首次并发创建有重复风险。
- Entity Alarm 传播捕获异常后继续，主 Alarm 成功但传播投影可能部分缺失。
- ACK/Clear API 不要求 Reason；重复操作被应用层返回错误，和自然幂等底层不一致。
- Alarm Comment 允许普通更新/删除；对 HVAC 审计，系统事件必须不可变，用户更正不能抹掉原记录。
- Rule 状态使用脚本/动态表达式会扩大配置、沙箱和可解释性风险。
- Severity 只升不降，不能区分当前风险和历史峰值。
- Rule/System Notification 遇到通道未配置时静默跳过该通道；操作员可能看不到配置错误。
- Target 级异常会提前结束后续 Target；Recipient 级异常虽继续处理，Request 最终仍写 `SENT`。
- Scheduled 失败也被写成 `SENT`；没有 `FAILED/PARTIAL/UNKNOWN/CANCELLED/EXPIRED` Request 状态。
- 没有耐久 Delivery Attempt、Provider Receipt、重试退避、最大尝试、Dead Letter、人工 Replay 或 Worker Lease。
- 去重时间戳在投递前写入；如果之后投递失败，后续 Trigger 可能被当成已处理。
- 延迟 Request 只保存 Target/Template ID，执行时读取当前配置；计划期间的配置编辑会改变已批准通知的内容或收件人。
- Teams Webhook 等配置直接位于 JSON；Secret 必须按 D09 改为 `CredentialRef`。
- 普通用户偏好可关闭通道，但安全强制通知不能完全由用户 Opt-out 覆盖。

## 8. 值得吸收的 Pattern

1. Alarm 条件事实、ACK 事实和 Assignment 分离。
2. 同一业务指纹只允许一条 Active Alarm，Clear 后下一次触发新 Incident。
3. Create Rule 与 Clear Rule 分离；Duration、Repeating、Schedule 和耐久状态是一等模型。
4. Alarm Timeline 同时容纳 System Event 与用户协作，但 System Event 不可编辑。
5. Target、Template、Rule、Request、Inbox 分层；动态 Audience Definition 不侵入 Channel Adapter。
6. Alarm Escalation 用多个明确 Stage 表达，并在状态条件满足时取消未执行 Stage。
7. Notification Read/ACK 与 Alarm ACK 严格分离。本地 DDL 已明确这一点，应保留。
8. 用户 Channel Preference、模板预览、未读计数和 WebSocket Inbox 更新。
9. 规则删除、目标/模板删除前检查引用，避免悬挂配置。
10. Trigger 专用 Info 提供模板字段，避免模板直接读取任意领域对象。

## 9. 不适合本项目的部分

1. 把 `INDETERMINATE` 当 Severity；它是 Evaluation/Quality 状态，不是风险等级。
2. 通用 Entity Relation 递归传播；本项目已有 Tenant/Site/Asset/Equipment 权威层级，应生成明确投影和 Audience，而不是遍历任意 Relation。
3. Alarm Severity 永不降级；目标保存当前值和历史峰值。
4. 任意 TBEL/JavaScript Alarm 表达式进入 Phase 1；先实现版本化 Typed Predicate、窗口、迟滞和组合条件。
5. 普通用户删除 Alarm 或系统 Timeline 事实。
6. 让手工 Close 把 Active 物理条件伪装成已恢复。
7. Notification 通道同步直调并把 API Return 当作最终交付。
8. 用本地 Soft Cache/普通 Cache 作为去重权威。
9. 失败后仍统一 `SENT`，或只有 Request 汇总错误而没有逐收件人 Attempt。
10. 一次性实现全部 Slack/Teams/SMS/Mobile Provider；通道必须由真实运维场景和 SLO 驱动。
11. 把 Edge Notification 同步塞进 Cloud Alarm 聚合；D08 需单独处理离线权威、冲突和同步。
12. 为兼容现有 `OPEN/ACKNOWLEDGED/SUPPRESSED/CLOSED` 增加双写或映射层。项目规则明确不保留过时路径，应一次性切换新 Contract 和数据模型。

## 10. 本地源码级反向审查

### 10.1 已实现且应保留

| 本地能力 | 源码事实 | 裁决 |
| --- | --- | --- |
| Scope 与授权 | Gateway 从 Session 派生 Tenant/Site，IAM 对 list/read/动作作精确判定，下游校验签名 Context | `KEEP` |
| 数据隔离 | Alarm 表 FORCE RLS，事务设置 `app.tenant_id`，跨 Scope 失败关闭 | `KEEP` |
| 单条变更治理 | 非 ACK 操作要求 Expected Version；Mutation 有稳定 Idempotency Key + Request Digest + Response Snapshot | `KEEP` |
| ACK 自然幂等 | Transition 已有 ACK 时返回当前 Aggregate，不增加 Version/Transition | `KEEP`，优于上游应用层重复错误 |
| 处置证据 | Transition 保存 Actor、Policy Revision、Correlation、Reason、Version、Time | `KEEP` |
| 规则输入质量 | 非 `GOOD` 输入产生 `INDETERMINATE`，不能 Build Publication | `KEEP/ADAPT` |
| 事件发布原子性 | Provenance Event 与 Alarm Current 在一个 Tenant 事务提交；Event Digest 防同 ID 不同内容 | `KEEP` |
| Real 诚实性 | 浏览器不从 Telemetry/Presence 补造 Alarm；公共写仍为 0%，读路径无 fallback | `KEEP` |
| 查询基础 | Site-scoped Cursor List/Detail、Status/Severity Filter、受保护 Cache 清理 | `KEEP/ADAPT` |

### 10.2 必须替换或补齐

| 发现 | 证据 | 风险 | 裁决 |
| --- | --- | --- | --- |
| ACK/Status 模型矛盾 | `StatusAcknowledged` 是合法状态，但 ACK 实际保持 `OPEN`；UI 再扫描 Transition 推导“已确认” | API、DB、筛选和 UI 对同一事实可能给出不同答案 | `REPLACE` 为正交字段 |
| Suppress 占用 lifecycle | `SUPPRESSED` 覆盖原 Status，再依靠 Transition 恢复 | 抑制通知被误解为故障状态变化 | `REPLACE` 为独立 Suppression |
| Close 可关闭任意未关闭 Alarm | 无 Recovery 条件校验；新 Rule Match 会自动 `REOPEN` | 人工动作可隐藏仍 Active 条件，且重开丢失新 Incident 边界 | `REPLACE` |
| 规则能力过小 | 仅 `SIMPLE_THRESHOLD` + 一个 Telemetry Key | 无持续、重复、计划、迟滞、组合、Clear 和重启恢复 | `REPLACE` |
| Alarm ID 由调用者传入 | Publication 通过 `alarmID` 查当前行，数据库只对 ID 唯一 | 两个调用者可为同一活动条件创建不同 Alarm | `REPLACE` 为业务指纹 Partial Unique |
| 发布更新并发结果暴露为 Version Conflict | 单行 `FOR UPDATE` 后仍按 Version Update；首次 Insert 只按外部 Alarm ID 冲突 | 重试/并发需要围绕业务键收敛 | `ADAPT` |
| Severity 不完整 | 本地为 INFO/WARNING/MAJOR/CRITICAL，缺 UX 规范中的 MINOR | 展示和规则 Contract 不一致 | `REPLACE` Contract |
| Comment 只有 ACK Comment | Comment 被写成 ACK Transition Reason，无独立协作 Comment API | 不能讨论、修正或区分系统事实/用户备注 | `ADAPT` ThingsBoard Pattern |
| 传播/相关性缺失 | 无 Registry 层级 Alarm Projection、Storm/Correlation Group | 父设备/Site 视图和风暴治理不足 | `ADAPT`，不复制通用 Relation |
| 查询过窄 | 仅单 Status、单 Severity、Cursor | 缺 Type、Assignee、Ack/Clear、时间、文本、设备和 Related Entity Filter | `ADAPT` |
| 抑制定时执行缺失 | 只有 `suppressed_until`，Read Promotion 文档明确排除自动到期 | 到期后不会由权威 Worker 自动解除 | `REPLACE/ADD` |
| Work Order 只有 UI 跳转 | Detail 拼接 `?sourceAlarm=`；正式多对多 Link 尚未交付 | 链接不能作为领域关联证据 | `DEFER` 到 Work Order Link Slice |

### 10.3 Notification 当前状态

`009j-operations-support-domains-v2.sql` 已定义：

- `notification_policies`；
- `notification_templates`；
- `notification_messages`；
- `notification_deliveries`；
- `notification_user_states`。

其中值得保留的设计包括 Tenant/Site Scope、Released Template 概念、每消息/收件人/通道唯一、`PENDING/SENT/DELIVERED/FAILED/SUPPRESSED`、Read 与 Notification ACK，以及明确“Notification ACK 不修改 Alarm”。

但源码图与全文检索没有找到这些表的生产写入者、HTTP API、规则处理器、调度器、Delivery Worker 或 Inbox UI；运行角色只有 SELECT Grant。Forecast/Metric 中的 `audit-notification` 只是 Outbox Consumer 名称，没有 Notification Consumer 实现。当前应标记为：

```text
Notification logical schema: PARTIAL / UNVERIFIED
Notification runtime: NOT_IMPLEMENTED
Notification Real UI: NOT_IMPLEMENTED
```

DDL 本身也不足以成为目标实现：没有 Target/Audience、Rule、Request/Intent、Schedule、Escalation Stage、独立 Attempt/Receipt、Lease、RetryAt、MaxAttempts、Dead Letter、Provider Message ID、Template/Recipient Snapshot 或 CredentialRef；`attempt` 只是 Delivery 行上的整数，不能保留逐次证据。

## 11. 与项目规范对齐

### 11.1 UX 规范

《智慧能源系统_前端交互与能源控制UX规范_V1.1》明确冻结：

- Severity 为 `CRITICAL/MAJOR/MINOR/WARNING/INFO`；
- Severity 与 Status 不能混为一个颜色；
- Detail 必须展示 Trigger Condition、Current Value、Rule Version、Timeline、Acknowledgement、Recovery、Notifications 和 Related Device；
- `Alarm ACK != Alarm RESOLVED`；ACK 只表示“有人知道了”；
- 重复 ACK 不得制造第二次处置的错觉；首次 ACK 的 Actor、Time、Comment 必须可追溯；
- Resolve 必须来自 Recovery Condition 或明确人工流程；
- Suppress 必须有 Reason、Duration、Scope；
- Storm Suppression 仍需显示被合并数量；
- 404/403 保持非泄露 Scope 语义。

正交 Alarm 模型、自然幂等 ACK、恢复条件、显式 Suppression 和 Storm Count 都是文档直接要求，不是为了追随 ThingsBoard 新增的偏好。

### 11.2 前端工程规范

《智慧能源系统_前端工程架构与实现设计_V1》要求大量 Alarm 使用 Server-side Pagination/Cursor，关键状态不能只靠颜色，并要求 AlarmAckDialog 的 Component Test、OpenAPI→Generated Client→Typecheck→Critical API Mock→Build 的 Contract Gate。当前 Cursor、受保护缓存与错误映射可保留；新 Contract 必须同步生成客户端，不能继续由前端扫描 Transition 修补领域模型。

### 11.3 本地架构文档冲突

1. `backend-architecture.v2.json` 把 Notification 标为 `ALIGNED`，但实现指针只有 Registry SQL；应在后续架构同步中降为 `PARTIAL/NOT_IMPLEMENTED`，直到运行时、API、Worker 和 UI 证据齐全。
2. `real-product-roadmap.md` 正确说明 Alarm P1–P4 已合并但公共写仍为 0%，并明确 Notification、Suppression Expiry、Correlation 和 Work Order Link 是后续独立 Slice。本文不改变该发布事实。
3. `s4-alarm-read-promotion.md` 只证明读 Canary Gate，不证明 Lifecycle、Rule Evaluation 或 Notification 已上线。
4. D09 已裁决通用外部效果使用 Delivery Intent/Attempt/Receipt；Notification 不能再发明第二套弱化投递状态机。

## 12. 完整能力裁决矩阵

| D06 能力 | ThingsBoard 参考 | 本地现状 | 裁决 |
| --- | --- | --- | --- |
| Alarm identity/aggregate | Originator + Type 聚合 Active | 外部 Alarm ID + Source Reference | `ADAPT`：业务指纹 + DB Unique Active |
| Active/Cleared | 一等 Boolean | OPEN/CLOSED 混合人工处置 | `ADOPT/REPLACE` |
| Ack/Unack | 与 Clear 正交 | Transition 隐含 ACK，Status 声明冲突 | `ADOPT/REPLACE` |
| Severity | 5 级含 INDETERMINATE | 4 级缺 MINOR | `ADAPT` UX 五级；INDETERMINATE 移至 Evaluation |
| Assignment | 独立 Assignee/Time | 独立字段 +审计 Transition | `KEEP/ADAPT` |
| Suppression | 主要在规则/通知侧 | 占用 Alarm Status | `REPLACE` 为独立有 Scope/Reason/Duration 的 Policy |
| User/System comments | 独立 Comment CRUD +系统评论 | 只有 Timeline/ACK Comment | `ADAPT`；系统不可变、用户更正留痕 |
| Propagation | 通用 Relation/Owner/Tenant | 无 | `ADAPT` 为 Registry 层级投影和 Audience |
| Query/filter/count | 状态、Severity、Type、Assignee、时间、文本等 | 单 Status/Severity + Cursor | `ADAPT` |
| Create/update active | SQL Find-or-create | 按外部 Alarm ID Publish | `REPLACE` 为原子业务键 Upsert |
| Clear/recovery | Clear Rule 或 API | 普通 Close；无 Clear Rule | `REPLACE` |
| Duplicate/replay | Active 合并；用户重复 ACK 报错 | Event Digest + ACK 自然幂等 | `KEEP` 本地幂等，补 Active Unique |
| Duration condition | 有 | 无 | `ADAPT` |
| Repeating condition | 有 | 无 | `ADAPT` |
| Schedule/timezone | 有 | 无 | `ADAPT`，按 Site Timezone/DST 测试 |
| Persisted rule state/timer | 有 | 无 | `ADOPT/ADAPT` |
| No-data/quality | 有 No-data；表达式灵活 | 非 GOOD -> INDETERMINATE | `KEEP/ADAPT`，不得因坏质量 Clear |
| Script expressions | TBEL/Simple | Typed Threshold | `REJECT` Phase 1 任意脚本；扩展 Typed Predicate |
| Notification target/audience | 动态 Platform Users + Slack/Teams | 无运行时 | `ADAPT` |
| Template/preview | 多通道模板和 Preview | DDL Template，无 API/Preview | `ADAPT` |
| Rule/trigger | 多类 Trigger | 无 | `ADAPT` Alarm-first；通用 Rule 生命周期归 D05 |
| Request/intent | PROCESSING/SCHEDULED/SENT | DDL Message/Delivery | `REPLACE` 为 durable Intent 状态机 |
| User inbox/read | WEB Notification SENT/READ | DDL User State，无运行时/UI | `ADAPT` |
| User preferences | 类型×通道 Opt-out | 无 | `ADAPT`；Mandatory Safety 例外 |
| Email | 同步 Channel | 无 | `ADAPT` 为 Worker Adapter，P0/P1 |
| SMS | 同步 Channel | 无 | `DEFER` 到 Critical Alarm 场景/SLO 与 Provider 明确 |
| Slack | Token Channel | 无 | `DEFER` |
| Microsoft Teams | Webhook Channel | 无 | `DEFER`，Secret 用 CredentialRef |
| Mobile/Firebase | Firebase Channel | 无移动产品 | `DEFER` |
| Webhook | 非 D06 主要 Channel | DDL 枚举，无 Worker | `ADAPT` D09 Egress Port，不直接发送 |
| Alarm trigger/escalation | 延迟表 + Clear Rule 取消 | 无 | `ADAPT`，条件和交付证据更严格 |
| API/Usage/Rate/Resource triggers | 覆盖广 | 无统一 Notification | `DEFER` 到管理面需求 |
| Device activity trigger | 有 | Presence 有事实，无 Notification | `DEFER/ADAPT` |
| Edge trigger/sync | 有 Edge 事件 | 审查域 D08 | `DEFER` 到 #241 |
| Entity/Rule/Task triggers | 有 | Work Order/Agent 有各自事实 | `DEFER`，不得跨域直写 |
| Scheduling restart recovery | 重载 SCHEDULED +本地 Timer | 无 | `REPLACE` 为 DB Lease/Claim Worker |
| Dedup | Trigger/Rule Cache | Delivery 唯一 DDL，无 Worker | `REPLACE` 为 Durable Intent Business Key |
| Retry/backoff | 无通知级耐久重试 | 只有 `attempt` 计数列 | `REPLACE` |
| Partial/failure/unknown | Request 仍 SENT + Error Stats | DDL FAILED 但无逐次证据 | `REPLACE` |
| Dead letter/replay | 无 | 无 | `ADOPT` D09 Pattern |
| Notification Edge sync | 有边界能力 | 无 | `DEFER` 到 D08 |

## 13. 映射到本项目目标设计

### 13.1 Alarm 目标模型

```text
AlarmIncident
  id
  tenantId + siteId
  fingerprint [source kind + source id + alarm type + rule scope]
  conditionState: ACTIVE | CLEARED
  currentSeverity: CRITICAL | MAJOR | MINOR | WARNING | INFO
  peakSeverity
  acknowledgedAt/by/comment?
  assignee
  first/last occurrence + occurrenceCount
  ruleRevision + source evidence
  version

AlarmEvaluationState
  ruleRevision
  MATCHED | NOT_MATCHED | INDETERMINATE
  duration/repeat/window/schedule state
  lastInputRevision + quality blocker + nextEvaluationAt

AlarmSuppression
  scope + reason + startsAt + expiresAt + actor/policy

AlarmTimelineEntry
  immutable SYSTEM_EVENT | USER_NOTE | USER_NOTE_CORRECTION

AlarmCorrelationGroup
  stable group + visible merged count + member incidents
```

不变量：

- `conditionState` 只由 Clear Rule/Recovery Evidence 或受治理人工 Recovery 改变；
- ACK 不改变 `conditionState`，Notification Read/ACK 也不改变 Alarm ACK；
- Suppression 不改变 `conditionState` 或删除事实；到期由耐久 Worker 执行；
- 同一 Tenant/Site/Fingerprint 最多一条 Active Incident，由数据库 Partial Unique 保证；
- Clear 后复发创建新 Incident，并通过 Correlation Group 关联；不自动 Reopen 旧 Closed 行；
- 质量不可信时 Evaluation 为 INDETERMINATE，不能据此 Clear Active Alarm；
- `currentSeverity` 可按当前规则证据变化，`peakSeverity` 保留历史峰值；
- Work Order 完成不直接 Clear Alarm，Alarm Clear 也不删除 Work Order。

### 13.2 Alarm 核心流程

```text
Accepted Telemetry/Presence/FDD Evidence
  -> versioned Alarm Rule evaluator
  -> quality and staleness gate
  -> persisted Duration/Repeat/Schedule state
  -> Create or Clear decision
  -> transaction:
       append source event
       upsert active incident by fingerprint
       append immutable timeline
       enqueue domain outbox
  -> read projections / subscriptions
  -> Notification policy evaluation
```

Typed Rule 首批至少支持：比较、范围、迟滞、持续时间、重复次数、No-data/Stale、AND/OR 组合、Clear Predicate 和 Site Timezone Schedule。任意脚本只有在沙箱、资源上限、调试、版本化和运营证据完整后才重新评估。

### 13.3 Notification 目标模型

```text
NotificationAudienceDefinition [versioned]
NotificationTemplateRevision [immutable once released]
NotificationRuleRevision
  trigger + filter + mandatory/advisory + escalation stages

DeliveryIntent
  source event/alarm + rule revision + stage
  template revision + recipient snapshot
  stable business key
  PENDING | SCHEDULED | PROCESSING | PARTIALLY_DELIVERED |
  DELIVERED | FAILED | CANCELLED | EXPIRED | DEAD_LETTER

DeliveryAttempt
  recipient + channel + attemptNo + lease/fence
  startedAt + outcome + retryAt + failure class

DeliveryReceipt
  provider message id + provider accepted/delivered/bounced evidence

InboxItem
  user + rendered snapshot + SENT/READ/ARCHIVED
```

### 13.4 Notification 核心流程

```text
Alarm Domain Outbox
  -> match exact Notification Rule Revision
  -> create one durable Intent per escalation stage (idempotent)
  -> snapshot released template and resolved recipients
  -> worker claims due Intent with lease/fence
  -> create Attempt before external effect
  -> Channel Adapter sends using CredentialRef
  -> record Provider Receipt or OUTCOME_UNKNOWN
  -> retry/backoff/dead-letter by failure class
  -> project Inbox/read count

Alarm ACK/Clear/Policy change
  -> atomically/idempotently cancel eligible future stages
  -> never erase completed attempts or inbox history
```

升级条件必须明确引用 Alarm 事实，例如 `conditionState=ACTIVE && acknowledgedAt IS NULL`；是否已经成功触达某一值班组也可成为下一阶段条件。取消与 Worker Claim 竞争时，以数据库状态、Lease 和 Fence 收敛，不能只取消进程内 Timer。

## 14. 实施顺序

### P0-A：替换 Alarm 聚合 Contract

1. 新建正交 Alarm Schema，加入 `MINOR`、Ack 字段、Condition State、Peak Severity、Suppression 和 Fingerprint。
2. 删除旧 `OPEN/ACKNOWLEDGED/SUPPRESSED/CLOSED` Contract 和前端 Transition 扫描补丁，不做兼容双写。
3. 建立活动 Fingerprint Partial Unique、并发 Upsert 和新 Incident 复发语义。
4. 保留 RLS、Gateway/IAM、Idempotency、Expected Version、Evidence/Audit；重新生成 OpenAPI Client。
5. 将手工 `CLOSE/REOPEN` 替换为受治理 Recovery/Clear；Public Mutation 仍按独立发布门推进。

### P0-B：实现耐久 Alarm Rule State

1. Versioned Rule + Typed Predicate + explicit Clear Predicate。
2. Duration、Repeating、Hysteresis、No-data/Stale 和 Site Schedule。
3. 持久状态、`nextEvaluationAt`、Timer Claim/Lease、重启恢复和 Rule Revision 切换策略。
4. 规则输入 Evidence Snapshot 与 Quality Blocker 进入 Detail。

### P0-C：实现 Notification 最小闭环

1. 重构现有 DDL 为 Audience/TemplateRevision/RuleRevision/Intent/Attempt/Receipt/Inbox；不在旧表上叠兼容层。
2. 首批通道只做 IN_APP，并用同一 Delivery Port 接 EMAIL；External Adapter 复用 D09 的安全和 Receipt Contract。
3. Alarm Created/Severity Changed/ACK/Cleared Trigger，分阶段 Escalation 和持久取消。
4. Mandatory Safety 与 Advisory Preference 分离；用户不能 Opt-out 必须送达的 Critical Policy。
5. Real Inbox、未读数、详情 Notification Timeline 与管理 Preview。

### P1：处置协作与检索

- Immutable System Timeline、User Note/Correction、Assignment 历史；
- Type/Ack/Clear/Assignee/Device/Time/Text 多条件 Cursor 查询；
- Registry 层级投影、Alarm Correlation/Storm Group 与可见合并数；
- 正式 Alarm↔Work Order 多对多 Link；
- Delivery 管理面、Dead Letter、Replay Approval、SLO 和配置审计。

### P2/DEFER

- SMS 仅在 Critical 告警值班流程、Provider、成本与 SLO 明确后进入；
- Teams/Slack 在客户协作系统确认后进入；
- Mobile/Firebase 在真实移动端和 Token 生命周期存在后进入；
- Device Activity、API Usage、Rate Limit、Resource、Rule Component、Task Trigger 按对应域需求逐个接入；
- Edge Alarm/Notification 离线同步由 D08 裁决。

## 15. 实施门槛与测试

### Alarm Gate

- 并发 100 次同 Fingerprint 首次发布只能产生一条 Active Incident；
- 同 Event ID 同内容重放收敛，不同内容冲突；
- ACK 不 Clear、Clear 不隐式 ACK、重复 ACK 不新增 Timeline；
- Suppression 不改变 Active/Cleared，且到期 Worker 在重启后仍执行；
- Close/Resolve 不能在无 Recovery Evidence 或授权人工流程时隐藏 Active Condition；
- Duration/Repeating/Hysteresis/Schedule 在重启、乱序、重复输入、DST 和 Rule Revision 切换下确定；
- STALE/INVALID/Missing 输入不能把 Active Alarm 清除；
- 当前 Severity 可变化，Peak Severity 单调保留；
- System Timeline 不可更新/删除，用户更正保留原始记录；
- Tenant/Site RLS、403/404 非泄露、Cursor 与多过滤器测试通过。

### Notification Gate

- 外发前 Intent 和 Attempt 已耐久提交；Worker 崩溃和 Lease 过期可收敛；
- 同 Source Event/Rule/Stage 重放只产生一个 Intent；
- 单个 Recipient/Channel 失败不阻断其他目标，Request 正确表达 Partial；
- Provider Accepted、Delivered、Bounced、Rejected、Timeout 和 Outcome Unknown 分开；
- Retry 有错误分类、退避、上限、Dead Letter 和有审计的 Replay；
- ACK/Clear 与 Stage Worker 并发时，取消和发送结果可由数据库事实解释；
- Delayed Intent 使用不可变 Template/Recipient Snapshot，不受后续配置编辑影响；
- Notification Read/ACK 不修改 Alarm；Mandatory Policy 不被普通 Opt-out 关闭；
- Secret 不进入 Template/Target JSON、日志、错误、URL 或前端；
- Inbox、Attempt 和 Receipt 均受 Tenant/User/Site Scope 与 RLS 保护。

## 16. 本轮最终裁决

- **KEEP**：Alarm 的 Tenant/Site/IAM/Gateway/RLS 边界、Evidence/Audit Timeline、幂等摘要、乐观并发、Provenance Event 原子提交、非 GOOD Quality 阻断、Real 无 Telemetry 推断和 ACK 自然幂等。
- **ADOPT/ADAPT**：ThingsBoard 的 Active/Cleared 与 Ack 正交模型、Create/Clear Rule、Duration/Repeating/Schedule/State Restore、Assignment、System/User Timeline、Target/Template/Rule/Request/Inbox 分层、动态 Audience、Preview、用户偏好、Alarm Escalation 与取消。
- **REPLACE**：本地单一 `OPEN/ACKNOWLEDGED/SUPPRESSED/CLOSED`、任意手工 Close、自动 Reopen 旧 Alarm、调用者提供 Alarm Identity、只有 Simple Threshold 的规则、Schema-only Notification 完成度；以及 ThingsBoard 的非唯一 Active Create、单调 Severity、Partial Propagation、重复 ACK Error、同步 Channel、错误后 `SENT`、投递前 Cache Dedup 和无 Attempt/Receipt/Retry。
- **REJECT**：INDETERMINATE Severity、通用 Relation 传播、Phase 1 任意脚本、删除系统处置事实、Notification/Work Order 状态反向伪造 Alarm Recovery、Secret-in-JSON 和一次性复制全部 Provider。
- **DEFER**：Slack、Teams、Mobile、无明确 Provider/SLO 的 SMS、管理类全量 Trigger 与 Edge Sync。

本轮关闭的是“源码级裁决是否完成”，不是 Alarm/Notification 运行时是否完成。当前 Alarm 读 Canary 和本地治理基线是真实能力；正交状态、完整规则和 Notification 全链仍是明确待实现差距。

## 17. 验证记录

| 命令 | 结果 | 解释 |
| --- | --- | --- |
| `npm run s4:alarm:check` | `PASS` | 当前 Alarm Contract/Route 基线检查通过：3 条 Public Route、10 条 Runtime Contract-only Route |
| Alarm Model/Service `go test ./...` | `PASS` | `libs/alarmmodel` 与 `modules/alarm` 单元/HTTP/Store 测试通过 |
| Alarm Model/Service `go vet ./...` | `PASS` | 两个模块静态检查通过 |
| `npm run ownership:check` | `PASS` | 当前 Ownership Registry 自身一致：53 Routes、72 Resources |
| `npm run rms:real:graph` | `PASS` | 88 个 Real 可达模块通过，无 Demo 依赖回流 |
| `npm run lint` | `PASS` | HVAC Web TypeScript `--noEmit` 通过 |
| `npm run s4:alarm:promotion:test` | `FAIL` | Promotion Runner 仍要求旧路径 `GET /api/v1/sites/{siteId}/alarms`，当前 Registry 使用 `/api/v1/alarms`，3 项测试均在前置路由检查失败 |
| `agent-reach check-update` | `PARTIAL` | 本地 v1.5.0；网络重试 3 次后无法检查远端更新，并报告字符检测可选依赖警告 |

聚合 `npm run s4:alarm-service` 因同一个 Promotion Route 漂移在 Preflight 处停止，尚未进入其后 Go Test/Vet；这些步骤已单独执行并通过。该漂移属于当前脏工作树中的路由/认证脚本不一致，不是本文档改动造成，也不能被记录成绿色发布门。

即使当前模型测试通过，也只证明现有实现按自身 Contract 运行，不会推翻已经确认的 Domain 冲突：测试本身明确断言 ACK 后 Status 仍为 `OPEN`，而 API Contract 同时保留 `ACKNOWLEDGED` Status，正是本次裁决要求替换的模型。
