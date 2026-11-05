# 设备告警送到人手上：路由、升级、确认和恢复

2026-11-01 北京时间 14:26 前后，松雅轩网关断线，「网关上报中断」响了，但没有人收到、也没有人确认。Alertmanager 的接收方是空的，而设备告警只靠小程序的一次性订阅提醒（用完就悄悄发不出去，ADR 0026）。这里规定设备告警怎样按站点和等级发到具体的人（邮件、企业微信应用消息、企业微信群机器人），没人确认时怎样升级，确认和恢复时怎样告诉已经收到的人。

问题单：#695（设备告警改发企业微信）、#690（Alertmanager 发邮件）、#749（第 3 点：规则长时间判断不了也要报）。决定见 ADR 0031。

## 链路

```text
告警模块（同一个事务）            通知模块（energy-api 里）                       wecom-alert-adapter
alarm_notification_outbox  ──►  alarm_delivery（每条告警一行：现在的状态）  ──►  POST /send  ──►  QQ 邮箱 SMTP
CREATED / SEVERITY_CHANGED       alarm_delivery_attempt（每条消息一行）          （持令牌）       企业微信应用消息
ACKNOWLEDGED / CLEARED           按路由文件决定发给谁、何时发、是否升级                            企业微信群机器人
```

- 告警模块在改变告警的同一个事务里写待发事件：新告警、等级变化，以及（本次新增）第一次确认 `ACKNOWLEDGED` 和恢复 `CLEARED`。事件不会丢，也不会先于告警的改动被看到。
- 通知模块收下事件时，在 `notification_runtime.alarm_delivery` 里记下这条告警现在的样子（类型、等级、标题、开始、确认、恢复）。版本号新的事件覆盖旧的；确认和恢复只会从空变成有，晚到的旧事件不会把它们抹掉；开始时间取最早的。确认先于创建到达（乱序）也照样停止升级。
- 发送器（`AlarmDeliveryDispatcher`）每次领一条到时间的告警（`FOR UPDATE SKIP LOCKED`，多副本不会重复领），按站点的路由文件决定这一刻该发哪些消息，写进 `alarm_delivery_attempt`。每条消息有唯一的去重键 `告警:类型:级:渠道:收件人`，同一条消息只会写一次。然后一条条交给适配器发送。
- 通知模块不出公网。它只把「渠道 + 收件地址 + 标题 + 正文」交给适配器；SMTP 密码、企业微信应用的 Secret 和群机器人地址只有适配器能读。

## 路由文件

每个站点一份，进 Git：`deploy/sites/<站点>.alarm-routing.json`。只写收件人编号，不写邮箱和企业微信账号。编号到联系方式的对照在服务器上的 `recipients.json`，不进 Git（样例 `deploy/platform/phase1/alarm-delivery.example/`）。

| 字段 | 含义 |
| --- | --- |
| `siteId`、`siteName`、`timezone` | 站点。免打扰时段和消息里的时间都按站点时区 |
| `holdMinutes` | 告警开始后先等这么久再发第一条（0–60）。这段时间里恢复了，或者它的根因告警也到了，就谁都不打扰 |
| `maxAgeHours` | 第一次看到时告警已经开始超过这么久（发送关着、通知模块停过），不再补发，记 `TOO_OLD`。默认 12 |
| `quietHours` | 每天的免打扰时段，可以跨午夜。低于 `bypassSeverity` 的告警等时段结束再发；确认和恢复的通知也等 |
| `rateLimitPerHour` | 每个收件人每小时最多收多少条首发和升级消息。超出的记 `SUPPRESSED / RATE_LIMITED`，不再补发。`CRITICAL` 不限 |
| `rootCauseAlarmType`、`suppressWhenRootCause` | 根因归并：站点上这个类型的告警亮着时，类型以这些前缀开头的告警谁都不告诉（记 `ROOT_CAUSE`，每分钟再看一次）。根因恢复后它们照常走 |
| `routes` | 从上往下第一条匹配的路由决定。`minSeverity` 是最低等级，`alarmTypePrefixes` 可选 |
| `routes[].steps` | 升级的各级。第一级 `afterMinutes` 必须是 0（等待结束就发），后面每级在第一条发出后这么多分钟、仍没人确认、告警还亮着时发。最多 5 级 |
| `notifyAck`、`notifyRecovery` | 确认、恢复时，告诉这条告警已经发到过的每个人（只发给已经收到过的人，没收到过的不打扰） |

渠道：`smtp`（收件人的 `email`）、`wecom_app`（收件人的 `wecomUserId`，企业微信自建应用发给个人）、`wecom_bot`（群机器人，收件人固定写成 `group`，同一级写一次）。

启动时检查：字段不认识、等级写错、级数或时间不对、路由文件里的收件人在 `recipients.json` 里没有、或者没有这个渠道需要的联系方式，energy-api 就不启动（发送开着时）。仓库里的路由文件由 `cmd/energy-api` 的测试用样例收件人加载一遍，写错了 CI 就失败。

### 松雅轩

`deploy/sites/songyaxuan.alarm-routing.json`：

- 等 3 分钟；22:00–07:00（北京时间）免打扰，重要及以上不受限；每人每小时最多 6 条。
- 「网关上报中断」是根因：它亮着时，读数长时间没有更新、读数一动不动、数据质量、规则长时间判断不了这四类不发。11-01 那次断线，值班人员只会收到一条「网关上报中断」，而不是二十多条。
- 重要及以上：第一级发给 `songyaxuan-duty`（企业微信应用 + 邮件）；15 分钟没人确认，发给 `songyaxuan-lead`（企业微信应用 + 邮件 + 群机器人）。确认和恢复都通知已经收到的人。
- 一般、警告：只发邮件给 `songyaxuan-duty`，不升级，确认和恢复不另外通知。
- 收件人编号是占位，真实的人和联系方式由张浩在服务器的 `recipients.json` 里定。

## 一条告警的过程（松雅轩、网关断线）

| 北京时间 | 发生了什么 | 消息 |
| --- | --- | --- |
| 14:26 | 「网关上报中断」（重要）开始 | 无（等 3 分钟） |
| 14:29 | 等待结束、没人确认 | 值班：企业微信 + 邮件「【告警·重要】松雅轩：网关上报中断」 |
| 14:44 | 15 分钟没人确认 | 负责人：企业微信 + 邮件 + 群「【告警升级·重要】…（无人确认）」 |
| 14:46 | 读数长时间没有更新（症状）开始 | 无（根因亮着，`ROOT_CAUSE`） |
| 14:50 | 有人在网页或小程序上确认 | 值班、负责人和群：「【已确认】…，确认人」；之后不再升级 |
| 15:10 | 网关恢复，告警恢复 | 值班、负责人和群：「【已恢复】…，持续 44 分钟」 |

确认就是现有的确认：网页告警页、小程序告警详情、网关的告警确认接口，都写同一条告警，告警模块随即写 `ACKNOWLEDGED`。消息里的链接是 `<控制台>/operations/alarms?site=<站点>&view=all&inspect=<告警>`，点开就是这条告警，可以直接确认。

## 发送状态

`alarm_delivery_attempt.state`：

| 状态 | 含义 |
| --- | --- |
| `PENDING` | 等着发（`next_attempt_at` 到了就发） |
| `SENDING` | 正在发，租约 2 分钟。进程在这时死掉，租约过了会再发一次（可能重复一条，宁可重复也不漏） |
| `SENT` | 适配器说发出去了 |
| `DRY_RUN` | 演练模式：适配器检查了配置和收件地址，没有发 |
| `SUPPRESSED` | 被限流（`RATE_LIMITED`），不发 |
| `FAILED` | `NO_CONTACT`（收件人没有这个渠道的联系方式）、`REJECTED`（适配器 400/422/501：渠道没配、地址被拒、企业微信不认识这个人、服务器 IP 不在可信 IP 里）、`GAVE_UP`（连不上或上游失败，按 1、4、9 分钟重试，第 4 次仍失败） |

只记去重键、渠道、收件人编号和状态，不记邮箱、企业微信账号和正文。日志也一样。

## 模式

energy-api 的 `ALARM_DELIVERY_MODE`：

- `off`（默认）：不启动发送器。告警事件照样记进 `alarm_delivery`，打开时 12 小时以内还亮着的告警会补发第一条。
- `dry-run`：和 `live` 一样决定、记录、升级，只是让适配器只检查、不发。上线前先跑这个，对着 `alarm_delivery_attempt` 看「该发给谁、什么时候发」对不对。
- `live`：真发。

## 适配器 `POST /send`

`wecom-alert-adapter` 原来只把 Alertmanager 的告警转到企业微信群。现在多一个 `POST /send`：

- 要带 `Authorization: Bearer <alarm-send-token>`。没有令牌文件时一律 503，令牌不对 401。
- 请求：`{"channel","to","subject","text","dryRun","key"}`，不认识的字段 400。标题里有换行（邮件头注入）400。
- `smtp`：用 Alertmanager 同一份 `smtp.env` 和 `smtp-password`。465 端口直接 TLS（QQ 邮箱），其他端口必须 STARTTLS，不支持就拒绝。UTF-8，标题按 RFC 2047 编码。收件人被拒（5xx）422，临时失败 502。
- `wecom_app`：`wecom-app.env`（`WECOM_CORP_ID`、`WECOM_AGENT_ID`）和 `wecom-app-secret`。access_token 缓存到过期前 5 分钟，被告知过期（40014、42001、40001）就重取一次。不认识的人（`invaliduser`、81013）422；服务器 IP 不是应用的可信 IP（60020）501。
- `wecom_bot`：原来的群机器人地址文件。
- `dryRun: true`：只检查这个渠道配好了、地址像样，不连网络，204。
- 返回：204 成功；400、422、501 是不会自己好的问题，发送器不再重试；其他（502、503）会重试。

## 不做的

- 短信和电话：现在没有通道。路由里的渠道是可扩展的，以后加一个渠道只需要适配器支持、路由文件写上。
- 按值班表轮换：路由文件里写的是固定的人，换人改 `recipients.json`（同一个编号换联系方式），不用改 Git。
- 小程序订阅提醒仍照旧（ADR 0026：企业微信跑稳一周后再删）。
