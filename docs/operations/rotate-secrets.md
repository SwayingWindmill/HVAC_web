# 轮换密码和密钥

轮换时只改运行目录或 GitHub 密钥里的那一份，不要把新值写进 Git，也不要在 shell 历史里 `echo` 出来。改完用「还能登录、数据库还接受连接」来确认，不要把密码打印出来核对。

## 数据库角色

应用用的是各自的数据库角色，密码在 Git 忽略的 `runtime/server/db-role-credentials/roles.sql`（由 `role-credentials.sql.example` 复制来）。超级用户密码是环境文件里的 `POSTGRES_ADMIN_PASSWORD`。

1. 在库里 `ALTER ROLE <角色> PASSWORD '...'`。
2. 把同一句写回那份 `roles.sql`，文件权限保持只有部署用户能读。
3. 环境文件里如果有这个角色的连接串，一起换成新密码。
4. 重启用到这条连接串的容器。身份服务、平台网关、遥测、调度至少要看一眼是否还在重启。

超级用户密码还要写回 `POSTGRES_ADMIN_PASSWORD`，并让 `postgres-exporter` 用到的同一变量一起生效（导出器不另存一份密码）。

## 登录和后台入口

操作员自己的平台密码在网页「系统管理」里改。那不是服务器上的文件。

Grafana 管理员密码是环境文件里的 `GRAFANA_ADMIN_PASSWORD`。改完重建 `grafana` 容器。

AI 助手的 `DEEPSEEK_API_KEY` 在 `server.runtime.env`。没配时助手显示「未接入」。更换后重启 `operations-agent-service`。

## 部署用的 SSH

`DEPLOY_SSH_KEY` 或 `DEPLOY_SSH_PASSWORD` 在 GitHub 仓库密钥和 `production` 环境密钥里。环境里有同名密钥时，部署用环境里的那一份。

1. 在服务器上给 `ubuntu` 换密钥或密码（`sudo` 仍须对 `/opt/hvac-platform-tools/deploy.sh` 免密）。
2. 更新 GitHub 上的密钥。
3. 用 Actions 里的「Deploy production」跑一次空部署之前，先确认新密钥能 `ssh ubuntu@139.199.58.47`。不要把私钥内容贴进日志。

## 告警接收方

企业微信机器人的 key 和 SMTP 密码在 `runtime/server/alert-receiver/`。换 key 就换掉 `wecom-webhook-url` 那一行；换邮箱密码就换掉 `smtp-password`。文件保持目录 `755`、文件 `644`，让容器用户读得了。然后重建 `alertmanager` 和 `wecom-alert-adapter`。细节在 `docs/operations/alert-receivers.md`。

## 不要顺手换的

身份服务的签名密钥（`signing-key.pem`）换了之后，已经发出的登录会话会全部失效。除非密钥泄漏，否则不要为了「定期轮换」换它。网关 CA 的私钥只在接入服务的目录里，覆盖它会使已签发的网关证书都对不上。
