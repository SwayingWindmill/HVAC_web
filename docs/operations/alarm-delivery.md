# 设备告警发到值班人员：配置、上线和退回

设计见 `docs/specs/alarm-delivery.md`。这里是服务器上要做的事。平台自己的告警（Prometheus → Alertmanager）怎么发，见 `alert-receivers.md`；两条路共用同一份 SMTP 账号。

默认是关着的（`ALARM_DELIVERY_MODE` 没设就是 `off`）：合并、部署这次改动，谁都不会收到消息。

## 要放的文件

都在运行目录里，不进 Git。正式机器上是 `/opt/hvac-platform/deploy/platform/phase1/runtime/server/`。部署脚本每次把运行目录里的文件改成 `644`、目录 `755`，运行目录本身 `700`。

### `alarm-delivery/`（energy-api 和 wecom-alert-adapter 都挂载）

1. `recipients.json`：从 `deploy/platform/phase1/alarm-delivery.example/recipients.json.example` 复制。每个收件人编号（路由文件里写的那些）写上姓名、`email`、`wecomUserId`。企业微信账号是通讯录里成员的「账号」，不是姓名和手机号。某个渠道用不上可以不写那一项；但路由文件让这个人收这个渠道时，必须写。
2. `alarm-send-token`：`openssl rand -hex 32 > alarm-send-token`。energy-api 用它调用适配器的 `POST /send`，适配器用它核对。不要贴到聊天和问题单里。换令牌：写新文件，重启 `energy-api` 和 `wecom-alert-adapter`。

### `alert-receiver/`（Alertmanager 和 wecom-alert-adapter 挂载）

- 邮件：`smtp.env`、`smtp-password`，和 `alert-receivers.md` 里的是同一份。QQ 邮箱：`SMTP_SMARTHOST=smtp.qq.com:465`，`SMTP_AUTH_USERNAME` 是邮箱地址，`smtp-password` 是 QQ 邮箱「设置 → 账号 → 开启 SMTP 服务」生成的授权码，不是 QQ 密码。设备告警的收件人来自 `recipients.json`，不看 `SMTP_TO`。
- 企业微信应用：`wecom-app.env`（`WECOM_CORP_ID`、`WECOM_AGENT_ID`）、`wecom-app-secret`（只一行 Secret）。在企业微信管理后台：
  1. 「应用管理 → 自建 → 创建应用」，可见范围包含要收告警的人。
  2. 记下 AgentId 和 Secret（Secret 在企业微信里查看，会发到管理员的企业微信）。企业 ID 在「我的企业 → 企业信息」。
  3. 应用详情页「企业可信IP」加上服务器的公网 IP（139.199.58.47）。不加的话企业微信回 60020，消息记成 `FAILED / REJECTED`，适配器日志 `alarm_send_not_configured`。
- 企业微信群机器人：`wecom-webhook-url`，和 Alertmanager 用的是同一个群。路由里写了 `wecom_bot` 才用。

### `environments/server.runtime.env`

```text
ALARM_DELIVERY_MODE=dry-run
```

`ALARM_DELIVERY_CONSOLE_URL` 不用设，默认是部署脚本给的 `PLATFORM_PUBLIC_ORIGIN`（消息里的链接）。

## 上线顺序

1. 这条改动叠在 #752 → #751 → #750 上面，按顺序合并、一次部署。迁移由部署时的 migrator 执行：告警的 015（规则长时间判断不了）和通知的 005（发送记录）。
2. 部署后先保持 `off`。看 energy-api 日志有 `alarm_delivery_off`。
3. 放好上面的文件，`ALARM_DELIVERY_MODE=dry-run`，只重启 energy-api 和适配器：
   `node scripts/phase1-wsl-compose.mjs --integration up -d --no-deps energy-api wecom-alert-adapter`（带上服务器的环境变量）。
   - 配置有错（路由文件引用了 `recipients.json` 里没有的人、少了令牌文件等），energy-api 起不来，日志会写哪一项，不写联系方式。改好再起，或者改回 `off`。
4. 演练至少一天，最好碰到一次真告警。每天看一眼：

   ```sql
   -- 用超级用户连 hvac_s1（docker exec <postgres 容器> psql -U postgres -d hvac_s1）
   SELECT a.created_at AT TIME ZONE 'Asia/Shanghai' AS 北京时间, d.title, a.kind, a.step, a.channel, a.recipient, a.state, a.reason
   FROM notification_runtime.alarm_delivery_attempt a
   JOIN notification_runtime.alarm_delivery d USING (tenant_id, alarm_id)
   ORDER BY a.created_at DESC LIMIT 50;
   ```

   对一对：该发的人对不对、等了 3 分钟、免打扰时段的一般告警是不是等到 07:00、断线时症状告警有没有被压住、确认之后有没有停止升级、恢复时是不是只告诉了收到过的人。`state` 应是 `DRY_RUN`。出现 `FAILED / NO_CONTACT` 是 `recipients.json` 少了联系方式；`FAILED / REJECTED` 看适配器日志（`alarm_send_*`）。
5. 发一条真的试一下：把自己写成 `songyaxuan-duty`，`live`，在网页上对一条测试告警确认，看手机和邮箱。然后换成真的值班人员。
6. `ALARM_DELIVERY_MODE=live`，重启 energy-api。小程序的订阅提醒照旧发，企业微信稳定一周后再按 ADR 0026 删掉。

## 退回

- **马上不发**：`ALARM_DELIVERY_MODE=off`，重启 energy-api。还没发的消息留在表里，下次打开时，超过 12 小时的告警不会补发。
- **只停某个渠道**：把 `alert-receiver/` 里那个渠道的文件挪走，重启适配器。发往它的消息记成 `FAILED / REJECTED`，其他渠道照发。
- **只停某个站点**：删掉 `deploy/sites/<站点>.alarm-routing.json`，提交、部署。没有路由文件的站点不发（`NO_ROUTE`）。`routes` 不能写空，写空 energy-api 起不来。
- **退回代码**：照 `rollback.md` 退回上一版镜像即可。旧版通知模块认得 `ACKNOWLEDGED`、`CLEARED` 事件，只是不处理；新表留着不碍事。要删表：先 `off`，再执行 `services/notification-service/migrations/rollback/005_alarm_delivery.sql`；告警的 015 有 `modules/alarm/migrations/rollback/015_s32_rules_unevaluable.sql`。

## 已知的限制

- 发送中途进程被杀，租约（2 分钟）过后会再发一次，同一条消息可能收到两次。宁可重复也不漏。
- 「规则长时间判断不了」亮着期间，新判断不了的规则不会更新到已经发出的消息里；网页上的告警是准的。
- 重要及以上不受免打扰限制，半夜也会发。
- 适配器只在 `observability-*` 档位启动。正式机器的 `single-lite` 带这个档位。
