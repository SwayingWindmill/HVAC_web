# alarm-delivery.example

服务器上 `${PHASE1_RUNTIME_DIR}/alarm-delivery/` 的样例（设备告警送到人手上）。整个运行目录都不进 Git，这个目录也一样。步骤见 `docs/operations/alarm-delivery.md`。

| 服务器上的文件 | 样例 | 谁读 |
| --- | --- | --- |
| `recipients.json` | `recipients.json.example` | energy-api：收件人编号 → 邮箱和企业微信账号 |
| `alarm-send-token` | 没有样例，用 `openssl rand -hex 32 > alarm-send-token` 生成 | energy-api 和 wecom-alert-adapter（`POST /send` 的令牌） |

收件人编号就是 `deploy/sites/*.alarm-routing.json` 里写的那些。两个容器都以 uid 65532 运行。部署脚本每次把运行目录里的文件改成 `644`、目录改成 `755`，运行目录本身是 `700`，主机上只有 root 能进去。
