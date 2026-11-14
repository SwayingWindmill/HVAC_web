# 告警发给谁

这一页是平台自己的告警（服务挂了、磁盘满、备份过期，Prometheus → Alertmanager）。设备告警（网关上报中断、温度越限等）按站点发给值班人员，另走一条路，见 `alarm-delivery.md`；它和这里共用同一份 SMTP 账号和群机器人，另外多一个企业微信自建应用（`wecom-app.env`、`wecom-app-secret`）。

Prometheus 把告警交给 Alertmanager。没有配置接收方时，告警分组之后留在 Alertmanager 里，不发出去。这是默认行为：运行目录里没有下面的文件，启动脚本就用仓库里的空接收方。

密钥放在 `${PHASE1_RUNTIME_DIR}/alert-receiver/`。正式机器上是：

```text
/opt/hvac-platform/deploy/platform/phase1/runtime/server/alert-receiver/
```

这个目录在 Git 忽略的 `runtime/` 下面。样例在 `deploy/platform/phase1/observability/alert-receiver.example/`，文件名多一个 `.example`。复制时去掉 `.example`。目录 `755`，文件 `644`，否则容器里的用户读不到。

## 企业微信群

1. 在群里添加群机器人，得到一条 `https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...`。
2. 把这一行写进 `wecom-webhook-url`，不要引号，不要第二行。
3. Alertmanager 只把告警 POST 到内网的 `wecom-alert-adapter:9095`。适配器再转成一条文本发到企业微信。生成出来的 Alertmanager 配置里没有 key。

## 邮件

`smtp.env` 里写主机、发件人、账号、收件人。QQ 邮箱是 `smtp.qq.com:465`，`smtp-password` 写授权码（QQ 邮箱「设置 → 账号 → 开启 SMTP 服务」生成），不是 QQ 密码。`SMTP_TO` 只给 Alertmanager 用；设备告警的收件人在 `alarm-delivery/recipients.json`。`smtp-password` 里只有密码。465 端口一连接就是 TLS，样例里 `SMTP_REQUIRE_TLS=false` 就是这种连法。密码不会写进生成的配置，配置只用 `smtp_auth_password_file` 指向这个文件。

两个都放了，同一条告警会发到群里和邮箱。只放一个就只走那一个。

## 生效

```bash
cd /opt/hvac-platform
sudo -n /opt/hvac-platform-tools/deploy.sh
```

`page` 和 `critical` 每小时再提醒一次，其他严重程度每 4 小时一次。恢复时也会发一条。

看 Alertmanager 自己的页面（经 SSH 隧道进 `alertmanager:9093`）里，路由的接收方应是 `notify`。仍是 `none` 时，说明目录空、文件名不对，或者 `smtp.env` 有字段没通过校验（容器日志会写缺了哪一项，不会写密码）。

每条告警的 `runbook` 指向 `docs/operations/alerts.md` 里的一节。群消息里会带上这个路径。
