# 交接

现在做到哪、下一步做什么、服务器和本地环境有哪些特殊情况。改了这里说的事，就在同一个 PR 里更新本文。怎么构建、测试和运行见 `AGENTS.md`；用户能看到的变化见 `CHANGELOG.md`；每张单的细节在 GitHub 问题单里。

最后更新：2026-10-30。

## 产品现在有什么

下面的页面都读真实的数据来源（本地 `hvac-local`，https://localhost:8443；服务器 https://hvac.swayingwindmill.online）：

| 分组 | 页面 | 数据从哪来 |
| --- | --- | --- |
| 总览 | `/overview` | 能耗、冷站实时运行、告警、故障诊断、工单、优化建议、电价 |
| 监测 | 实时运行、设备 | 遥测的快照和推送、设备登记、设备历史 |
| 运行 | 数字孪生（`/operations/digital-twin`） | 登记里的冷站设备 + 实时运行；末端楼宇按站点写在 `twin-building.ts`（目前只有松雅轩），房间和末端设备登记之前都显示「未接入」（规格 `docs/specs/digital-twin.md`） |
| 能源 | 能耗分析（含「用电去向」：总表和各机组分表）、用电负荷（含负荷预测） | 能耗序列（Cube）、设备按小时/15 分钟的统计、站点电价、负荷预测 |
| 能源 | 优化建议 | 预测服务每小时 :07 跑，优化服务每小时 :20 跑，也可以手动触发（规格 `docs/specs/scheduled-optimization.md`） |
| 能源 | 节能项目（`/energy-analysis/savings`） | mv-service：节能措施、固定的基线、已验证日（ADR 0021） |
| 能源 | 能源报告（`/reports`） | 已结束的月份读 report-service 的快照（每月 1 日 03:00 生成），本月和没存快照的月份现算；有年度视图，可打印 A4 |
| 能源 | 站点对标（`/benchmark`） | 每个有权限的站点的能耗序列；没有能耗权限的站点列出来但不参加比较 |
| 运维 | 告警、故障诊断、工单、控制指令 | 告警、故障诊断服务 FDD（每 15 分钟跑一次）、工单、控制指令审批 |
| 平台 | 通知（站点的告警本身：正在发生的和最近恢复的，和小程序一样）、集成管理、系统管理（含用户与角色、修改密码、AI 助手用量） | 告警、网关证书、权限服务 IAM 的角色指派（ADR 0022）、登记、操作记录 |
| 手机 | 微信小程序（`apps/hvac-miniprogram`，#664 的功能都已上线，0.2.0 在微信审核中）：账号密码登录和微信一键登录、总览（本月节省、今天用能和昨天同时段比、冷站现在、正在发生的告警、每日运行结论）、运行（实时运行和设备）、助手（问答、查询步骤、结论卡片、历史对话）、通知（正在发生和最近恢复的告警、告警详情、确认告警、告警微信提醒）、我的（切换站点、绑定或解除微信、退出） | 和网页同一个网关；登录走 `/api/v1/auth/app/sessions`（微信一键登录是 `/sessions/wechat`），会话放在 `Authorization` 头里；微信绑定由身份服务换取和保存微信编号，只有它能访问微信 |
| 全站 | AI 助手（页头按钮，右侧 420px 面板；能耗分析、告警、设备详情上的「解释」） | Operations Agent 的会话，经平台网关；服务没配 `DEEPSEEK_API_KEY` 时面板显示「未接入」 |

电费都是按站点分时电价算的估算值，页面上标明「估算」；账单以结算（`services/settlement-service`，命令行工具，没有网页接口）为准。

## 设计

- 设计系统是 `DESIGN.md`（1.0），视觉基准是 Claude Design 画布 https://claude.ai/artifact/LVwBRsavimfmaBHnRYJbdm 的「定稿」页；令牌写在 `apps/hvac-web/src/global.css`。2026-10 的重设计七个阶段都已完成。
- 字号：`text-sm` 是正文 13/20，`text-xs` 是说明 12/18；面板标题用 `text-title`（14/20），单位和徽标用 `text-micro`（11/16）。
- 每个页面都用这些组件，不另做新的数字条或状态胶囊：
  - `Main`：页面边距和节奏。`PageHeader` + `PageMeta`。
  - `Panel` / `PanelHeader` / `PanelLink`（`blocks/panel`）：唯一的白色面板，标题栏高 48px；数字放在面板里作为一条分隔的数字条。
  - `StateCard`（`blocks/state`）：空、失败、无权限、未接入，以及路由级的状态页和 404。
  - `SeverityMark`（用形状区分严重程度）、`InfoTip`（解释放在 ⓘ 后面，不用灰色小字）。
  - `MetricTabsChartCard`（`chartType="bar"` 时正在进行的时段画斜线）、`components/ui/table` 的 `Table`。
  - `Status` / `StatusBadge`：带静止圆点的浅色标签。
- `FactTile` 的 `className` 作用在数值上，不在格子上；要跨列，外面包一层 `<div className="col-span-…">`。
- 没保存的表单归它所在的页面管：系统管理用 `features/system/unsaved-drafts.tsx`（路由拦截）。站点页面以后有非弹窗的表单，也自己加拦截。
- 系统管理的「用户与角色」改 IAM 目录时，`lib/capability-labels.ts` 和 `lib/role-labels.ts` 要一起改。`scripts/test-real-design-tokens.mjs` 检查 `features/system/design-system/design-tokens.ts` 里的每对颜色达到 WCAG 要求。
- 看效果：开一个 Vite 开发服务器在 5190 端口（`npm -w @hvac/web run dev -- --host localhost --port 5190`），e2e 配置会复用它，`npm run web:e2e -- e2e/<spec>` 几秒就能跑完，也可以写个临时用例截图。界面改动最后要在 1440px 下用真实数据看过。

## 服务器（139.199.58.47）

- 跑的是 main。Compose 项目 `hvac-platform`，目录 `/opt/hvac-platform`，`single-lite` 档位，带智能服务（预测、优化、故障诊断 FDD、节能验证 M&V、报告）和 AI 助手；两个模拟器按用户要求停着；对外经主机的 nginx 转到 127.0.0.1:18443。
- 部署方式：推到 `main`，或在 Actions 里手动跑 `.github/workflows/deploy-production.yml`（手动跑时可勾 `rebuild_base`，先把各服务的 `deploy-base` 重建成验证过的一层，见 `docs/operations/disk-full.md#重建部署基础镜像`）。流水线在 GitHub 的机器上按镜像参数编译 Go 服务（工具链 `1.27.3`，`CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w"`），并构建网页和 AI 助手。然后用 SSH 连上这台机器：从这次提交做 `git archive` 解到 `/opt/hvac-platform` 并写 `REVISION`（不删运行目录里没进 Git 的密钥和数据）；已有镜像先打 `before-<时间>` 标签，再只换 `/service` 和 `/healthcheck`（网页换 dist 和 nginx 配置，迁移镜像换迁移文件和 `migration-list.tsv`，AI 助手换编译结果，依赖在服务器上按 lockfile 执行 `npm ci --omit=dev` 装进镜像）。服务器内存紧，镜像保持本机的，这一步不 `docker pull`。解包和写 `REVISION` 用 SSH 登录的用户，目录仍归 `ubuntu`。换完重启走仓库里的 `deploy/platform/phase1/cd/deploy.sh`（随源码解到 `/opt/hvac-platform`，传入这次的提交号；不存在或 `sudo` 不允许免密跑它时退回服务器上老的 `/opt/hvac-platform-tools/deploy.sh`，见 `docs/operations/rollback.md`）：这个脚本要改运行目录里容器用户（`65532`、Postgres、ClickHouse）拥有的文件权限，所以登录用户不是 root 时用免密 `sudo` 跑它。流水线自己不启动模拟器。传到服务器的是一份压缩包，里面没有 AI 助手的 node_modules，也没有 sourcemap 和 `.d.ts`。两端 zstd 都至少 1.4 时用 `zstd -19 --long=27`（17 个 Go 程序里重复的运行时压在一起），否则退回 `zstd -3`，再不行就 gzip。2026-10-29 失败的那份是 83,063,775 字节（zstd -3，含 node_modules）。本地用同一套 Go 二进制和源码包重打：含 node_modules、zstd -3 是 81,831,100 字节（少掉的约 1.2 MB 是网页 dist 和迁移文件副本）；去掉 node_modules 和 sourcemap、改用长窗口之后是 23,865,253 字节。算上网页，压缩包大约 25 MB。上传时限按每分钟 0.5 MiB，再加 25% 和 3 分钟，这个大小一次大约 60 分钟；这条链路测过大约 1 MB/分钟，大约 25 分钟传完。构建 AI 助手镜像时，服务器要能访问 npm registry，装不上依赖就不会换上这版镜像。没传完就留下已传到的字节，下次从断点接着传，不从头再来。三次都失败才删掉 `/tmp` 里的发布目录，并且不执行 apply。部署任务限时 360 分钟（GitHub 托管运行器的上限），SSH 这一步限时 330 分钟。部署任务挂在 GitHub Environment `production` 上（需要审核人点头）。重启之后先在服务器上查本机 `/healthz`、`/sign-in`、`/login`、身份发现和容器健康，再从 GitHub 的机器访问公网做同样的检查；哪一步失败都把镜像标回这次的 `before-<时间>` 再重启。成功后只留最近两套 `before-*` 镜像，并清掉悬空镜像和超过一天的构建缓存。需要手工退回时，在服务器上跑 `deploy/platform/phase1/cd/rollback.sh`（不带参数就是最近一次）。这台机器上 root 不能密码登录，操作者是 `ubuntu`。SSH 凭据放在仓库的 Actions secrets：必须有 `DEPLOY_SSH_KEY`（优先）或 `DEPLOY_SSH_PASSWORD`；`DEPLOY_SSH_USER` 不设就是 `ubuntu`，`DEPLOY_SSH_HOST` 不设就是 `139.199.58.47`，`DEPLOY_SSH_PORT` 不设就是 `22`。没配凭据时流水线直接失败，并写明要设哪几个 secret。本机内存紧、不开 Docker Desktop 时，仍可在 WSL 里按同样的参数编译，再在服务器上换 `/service`。只重启某个服务时，用 `node scripts/phase1-wsl-compose.mjs --integration [--intelligence] up -d --no-deps <服务>`，带上服务器的环境变量（AI 助手要 `--intelligence`）。
- `DEEPSEEK_API_KEY` 在 `server.runtime.env` 里，是用户自己放的。
- 服务器上的 Go 服务还是用 Go 1.25.12 构建的。仓库从 #707 起用 Go 1.27.3、`golang.org/x/text` v0.41.0。上面的部署流水线第一次跑成功之后，服务器上的 `/service` 才换成 1.27.3，Modbus TLS 连接上的 4 个标准库漏洞和 `x/text` 处理特殊输入会崩溃的问题才算修好。
- 夜间备份由 `hvac-backup.timer` 做，备份放在同一块盘的运行目录里，还没有拷到服务器以外。
- 告警要发出去，在运行目录的 `alert-receiver/` 放企业微信机器人地址和/或 SMTP（样例在 `observability/alert-receiver.example/`，真文件不进 Git）。什么都不放，Alertmanager 仍是空接收方。Prometheus 用仓库里的 `observability/prometheus.yaml`（15 天且不超过 2GB），不再读运行目录里手改的那份。Centrifugo 的指标从容器网络里的明文 9000 抓，不再去碰只开 TLS 的 8000，所以 `Phase1ProcessDown` 不会再因为这次抓取失败而误报。主机备份新鲜度和容器重启由 `hvac-host-metrics.timer` 写给 node-exporter。Tempo 仍只在 `observability-full`，这台机器的 `single-lite` 不开它。步骤见 `docs/operations/alerts.md`。
- 两个演示站点（central-plant、local-energy-site-b）的 28 个定时任务都停用了：模拟器停着，指标任务每小时都会失败。重新开模拟器时一起启用。
- 运行时数据清理（#553）每小时删已送达的历史事件、删 7 天前的原始读数。第一次删原始读数从 2026-10-21 开始，之后跑一次 `VACUUM FULL telemetry_runtime.source_observations`，能腾出约 3.5 GB；`VACUUM FULL` 要先有和剩下的数据一样大的空闲空间，磁盘腾出来之前别跑。
- **磁盘**（2026-10-20 11:20 UTC：59 GB 用了 46 GB，剩 12 GB）。占地方的是：`source_observations` 5.4 GB（见上一条）、同一块盘上的夜间备份 6.5 GB、ClickHouse 的 `system.query_log` 0.8 GB。2026-10-19 19:52 磁盘满过一次，Postgres 重启了 10 次，ClickHouse 拒绝写入，写历史读数的程序停了 15 个小时（#649 已修）。元凶是告警判断记录 `hvac_s4` 的 `alarm_runtime.alarm_evaluation_event`：以前每收到一批读数，站点的每条规则都写一行，每行带整个站点 81 个读数的快照（约 6 KB），结果没变也写；10-18 起一天约 100 万行，两天 10 GB，10-20 补收积压读数时又是 7 分钟 3.5 万行。10-20 经用户同意把这张表清空（`TRUNCATE`，只是判断的流水，告警、处理记录和当前判断状态都不受影响）。#651 起只在判断结果变了时写一行（状态、判断不了的原因、发报或恢复、规则版本变了），超过 30 天的每分钟删掉；那 3.5 万次判断按新规则只写 14 行，估计一天几百行、两三 MB。#651 已在 2026-10-20 11:30 UTC 部署到服务器（main `e64062fd`，迁移 011 已执行）：之后 11 分钟判断了约 9.7 万次，一行都没写。部署前旧代码在网关恢复上报后又写了 12 万行（715 MB，最后一行 11:29:58 UTC），2026-11-19 起会被自动删掉。

### 松雅轩（第一个真实站点）

- 规格：`docs/specs/site-gateway-onboarding.md`。它的 Node-RED 网关通过双向 TLS 连 8884 端口（主机 nginx 转到 Mosquitto）发数据，网关证书 2027-01-12 到期。ThingsBoard 已停（数据库备份在 `/opt/backups/thingsboard/`），网关上已经没有 ThingsBoard 的输出。
- ThingsBoard 的历史（导入批次 `01a14000-7000-7000-8000-000000000001`，截至 2026-10-14T10:26:30Z）由容器 `tb-history-import` 从导出的 CSV 导入；导完后按设备核对条数，记录里还没有核对结果。
- 网关的耐久发件箱 2026-10-14 起发往平台：断线时读数存在网关上，恢复后补发（10-20 断线后补收过积压）；平台的 MQTT 接收程序是保留 24 小时的会话，Mosquitto 落盘，所以平台重启、部署时读数也不会丢。网关上还要做：ET1010 是用来控制的，需要平台的指令能下发到 Node-RED（#696）。改一个 Modbus 读取节点会卡住共用串口上的另外六个，改完要重启所有流程（规格「已知问题」）。
- 能耗：网关把四块电表加成「空调总电表（四台机组合计）」，能耗拓扑把它当总表；电价是临时的深圳分时电价；4 号电表的电量 = 寄存器 60 减去基数 76938 kWh。
- 设备读数按现场 2026-10-15 手绘的通道表对应；有些通道的读数和它在表上的位置对不上（蒸发器进出可能接反了），机组的冷冻水出水温度和整个冷站对不上（C、D 机组运行时 28–32 ℃，总管供水 6 ℃），要现场核对（#614）。
- 告警规则在 `deploy/sites/songyaxuan.alarm-rules.json`，用 `modules/alarm/cmd/alarm-rules` 发布。文件就是站点规则的全部：内容没变的规则不会出新版本，文件里没有的规则（删掉、改名、设备没有启用点位）会被撤销，它亮着的告警自动恢复，处理记录里写「规则已撤销」（原因 `RULE_RETIRED`，#693）。告警页的「正在生效的规则」页签列出站点现在的规则，发布后到那里核对。运行时先列出这次新增、更新、撤销哪些规则；`-print` 只列这份清单、不写库，它也要连告警库，所以和发布一样在服务器上跑。在服务器上用告警模块的数据库身份跑一个一次性容器：在 `modules/alarm` 里 `CGO_ENABLED=0 go build -o out/alarm-rules/alarm-rules ./cmd/alarm-rules`，把它和两个站点文件放到一个目录，再执行 `docker run --rm --network container:hvac-platform-energy-api-1 --volumes-from hvac-platform-energy-api-1 -e ALARM_DATABASE_URL_FILE=<energy-api 的值> -v <目录>:/alarm-rules:ro --entrypoint /alarm-rules/alarm-rules <energy-api 镜像> /alarm-rules/songyaxuan.alarm-rules.json`。
  - 「读数长时间没有更新」是每台设备一条（不是每个点位一条）：它亮着时，同一台设备另一个点位也停了不会再报，所有点位都恢复才清除。
  - 2026-10-22 部署迁移 014 后对松雅轩发布过一次（用户确认后）：撤销了「C 机组冷凝出水温度一动不动」（#614 改了通道）和「微信提醒测试（可忽略）」，其余 33 条没变。`ALARM_DATABASE_URL_FILE` 的值用 `docker inspect` 从 energy-api 的环境变量里读（镜像里没有 `printenv`），是 `/run/hvac/secrets/alarm.dsn`。
  - 告警服务（单机部署时在 energy-api 里）每分钟把到时间的判断再算一遍（#615，`RunDueEvaluations`），用每条规则上一次看到的读数，所以网关整个断线时也会报；站点断线期间新发布的规则，也用站点最后收到的读数算一次。「网关上报中断」：松雅轩 67 个启用的点位 15 分钟都没有读数时报（重要），收到任何一个新读数就恢复；断线超过 30 分钟时，各台设备的「读数长时间没有更新」也会跟着报。
  - 「一动不动」（#616）：设备自己读出来的每个温度读数一条（温度采集模块 18 个通道、冷量计 2 个，共 20 条），连续 6 小时完全没变就报「提示」，读数一变就恢复；机组电表上复制来的温度不另外判断。告警自己记每个读数「从什么时候起是现在这个值」（`telemetry_evaluation_input.unchanged_since`），规则条件是 `UNCHANGED` / `CHANGED`（对照 ThingsBoard 见 `docs/architecture/alarm-unchanged-reading-source-review-2026-10-20.md`）。部署后要等每台设备的下一批读数才开始记，最早 6 小时后才可能报。
  - 告警收到一台设备的一批读数时，只算用到这台设备读数的规则（#659，对照 ThingsBoard 见 `docs/architecture/alarm-evaluation-relevance-source-review-2026-10-20.md`）。松雅轩一批温度采集模块的读数算 20 条，冷量计 5 条，机组电表 3 条，其他设备 2 条；补收积压时每分钟能处理约 950 批。只跟时间有关的变化（读数过期、多久没数据）和设备不上报时新发布的规则版本，由每分钟的定时复查处理。
  - 告警判断时用的「现在」是站点时钟（#661，对照 ThingsBoard 见 `docs/architecture/alarm-backlog-clock-source-review-2026-10-20.md`）：读数还在不断送到时，是站点最新一批读数的时间加上告警收到它以来过了多久；2 分钟没有新读数送到，就是当前时间。补收积压时旧读数按它自己的时间判断，不会报了又马上恢复；补收时，每分钟的定时复查会领到还没到期的判断再放回去，到期了才算。告警上记的发生和恢复时间仍是平台做出决定的时间。
- 数据修补留下的备份表：4 号电表 2026-10-14 之前的历史按功率积分重算过，被替换的行在 ClickHouse `telemetry_history.d4fix_backup_observations` 和 `analytics.d4fix_backup_energy_facts`；冷量计的历史重放后被替换的行在 `telemetry_history.coolfix_backup_observations`。能耗页面再看几天没问题就删掉这三张表。
- 2026-10-20 的服务器状况：松雅轩网关从 10-20 03:57（北京时间）起没有再上报，「网关上报中断」和 10 条「读数长时间没有更新」都亮着，网关恢复后会自动恢复。磁盘见上面「磁盘」一条。
- 松雅轩的负荷预测链路（15 分钟负荷指标、基线模型、每小时预测任务）是 2026-10-16 在服务器上按接入 SQL 补建的。
- 松雅轩还没有优化建议（2026-10-30 只读核对）：没有 `OPTIMIZATION_RUN` 调度定义，也没有优化模型部署绑定；租户唯一一份优化策略（站点共用）的室内温度键是 `temperature`，松雅轩没有这个键（它的是 `room_temperature`），按现在的策略会报「读不到可用的实时室内温度」。另外 7 天里供水温度只有约 27% 的时间在策略的 6–10 °C 内（白天开机时约 5–6 °C，夜里 11–13 °C），就算键对了也常会报「当前供水温度或室内温度已在策略范围之外」。站点的安全、舒适范围和响应系数要用户定；优化策略现在只按租户存，给松雅轩单独一份要先决定是改共用的那份还是让策略按站点存。

## AI 助手

- 规格 `docs/specs/operations-assistant-v1.md`、`docs/specs/agent-semantic-query.md`；运行时只有 pi（`@earendil-works/pi-agent-core` 1.3.0，ADR 0014）；面板基于 assistant-ui（`docs/architecture/assistant-ui-source-review-2026-10-16.md`）。
- 只读工具：资产清单、设备清单、当前读数、历史读数、能耗序列和同比、按设备拆分的能耗、语义查询（Cube 视图上自选最多四个数，不分组时用电和电费可以一起查）、告警列表、故障诊断结果、未完成的工单。站点名和站点本地的当前时间在每次回答开始时由服务读一次写进提示，读不到时这次回答以 `SITE_CONTEXT_UNAVAILABLE` 失败（#593）。每次读数据前网关按提问人的权限签一张只对这次请求有效的授权，助手拿着它去找数据负责方。每次回答最多 12 次模型调用、24 次工具调用。
- 一个会话可以接着问（#617）；建议的问题显示在最后一个回答的卡片下面（#638）。会话只属于发起的人，面板的「历史对话」列出我在当前站点最近 50 个（#639）。模型只看到前面问答的文字，看不到前面的读数和结论卡片，所以追问会重新读数据；每次都把整段历史发给模型，对话太长会超出单次回答的输入上限，这时开新对话。
- 回答的文字里，查数据前那句过程话不保存，和正文重复的卡片提醒会去掉（#607）。
- 回答第一行是一整句加粗的关键结论，单独占一行，正文里只加粗这一句（#663）。结尾的 `**` 后面直接接文字时，面板按 CommonMark 不会加粗、只显示星号，所以规则要求加粗句后面换行；模型偶尔忘了换行时（评测 72 次里 1 次），面板自己把后面的文字挪到下一段（`apps/hvac-web/src/features/assistant/answer-markdown.ts`）。
- 结论卡片可以带一张小图（#640）：助手只说画哪一次读到的数据（`chartFrom`），由那次读数据的工具画图（`tools/internal/read-tool-charts.ts`），图里的数都是读到的数。读数历史的结果带点位名称。
- 系统管理里的「AI 助手」标签显示每天的提问次数、失败、耗时、用量和每个步骤，以及每个步骤没成功的原因（#597、#624）。原因来自工具调用记录里的 `failure_code`：收尾被拒时记哪一部分错了、怎么错的（比如 `FINDING_KEY_FIGURES_TOO_LONG`、`FINDING_EVIDENCE_NOT_READ`），不存模型写的原文；页面把它换成「关键数字太长」「引用了没读到的数据」这样的话。只有能看操作记录的人（目前只有管理员）看得到；评测的会话也算在里面。
- 评测：`services/operations-agent-service/evals/`，24 道松雅轩的真实问题，在登录后的网页里跑（README）。最近一次（2026-10-20，#663 之后）：72 次里 67 次全部检查通过，3 次全过的题 21/24，71 次第一行是单独一行的加粗结论，每次回答平均 4.1 秒、读 1.7 次数据。记录在 `evals/BASELINE.md`。
- 已知问题：
  - 约 3% 的回答没有正文、只有卡片（评测 72 次里 2 次）。
  - 问「看一下另一个站点」时，助手有时不说看不了，直接答当前站点（评测 3 次里 2 次）。
  - 设备名里带「UID」（例如「B 机组电表（2 号，UID 2）」）时，助手照抄就不是纯中文；这是站点的命名。
  - 提示里用 `UNABLE_TO_CONCLUDE` 说明结论类型，模型偶尔把这个词写进正文（10-19 抽查 4 次里 1 次）。
  - 「10 月 14 日能效为什么最低」这类数据不全的问题，读数据次数常超过上限，有时还会下结论。

## 能耗和 Cube

- 所有能耗读取都经过 Cube（ADR 0023）。对外只有三个视图：`site_energy`（站点总量：用电、供冷、能效）、`equipment_energy`（按设备的分表）和 `site_energy_cost`（估算电费），每个字段都有中文标题和说明（`semantic/cube/README.md`；为什么这样分，见 `docs/architecture/cube-views-source-review-2026-10-17.md`）。
- 电费也由 Cube 算：遥测把每个站点的电价排成按 UTC 的价格窗口（`analytics.tariff_price_windows`），`site_energy_cost` 视图按每段电量结束时所在的窗口计价（规格 `docs/specs/estimated-energy-cost.md`）。网页、报告、优化、M&V 和 AI 助手都读这个电费，自己不再算。
- report-service、优化服务和 M&V 以各自的后台服务身份（ADR 0017）读能耗，IAM 给它们签 30 秒的授权；它们没有 ClickHouse 账号。证书由 `scripts/issue-workload-identities.mjs` 签发，`local:up` 和服务器的 `deploy.sh` 都会跑。
- 网页仍然自己用供冷除以用电算能效。

## 下一步

按顺序：

1. **#664 微信小程序**：规格 `docs/specs/miniprogram-wechat.md` 里的四件事都已合并并部署：绑定微信（#671）、告警提醒（#674）、每日运行结论（#675）、微信一键登录（#676）。绑定、告警提醒、一键登录都在真机上试过；每日运行结论还等真机收到第一条（要先在总览点「多收一天」），收到后和网页的数对一遍再关单。界面按 #677 逐页整理过（#678–#683：总览、通知、运行和设备、助手、登录，加上 lucide 图标和会转的泉来禾风车助手图标），#677 等用户在手机上看过再关。
   - **发布**：2026-10-21 用开发者工具命令行上传了 0.2.0（main `089c84f0`），用户已提交审核。审核通过、点了「发布」后，把服务器 `IDENTITY_WECHAT_MINIPROGRAM_STATE` 改成 `formal` 并重启 identity-service，消息点开才进正式版。上传命令：`cli.bat upload --project <apps/hvac-miniprogram 的完整路径> -v <版本> -d <说明>`（开发者工具要登录、开服务端口）。
   - **以后的界面改动不靠截图**：用户 2026-10-21 说直接改、不用模拟器截图（开发者工具的自动化截图很慢、常超时），整理逻辑由小程序测试守住，样式由用户在手机上看。
   - **其他约定**：身份服务和通知模块的 Postgres 测试由 `npm run test:identity-postgres`、`npm run test:notification-postgres` 在临时库上运行。小程序读冷站用的点位名称表和助手的对话整理规则从网页复制（`npm -w @hvac/miniprogram run sync`，测试会检查两份是否一致）。通知页直接读告警（网页的通知页也是）。订阅消息是一次性的：用户每点一次「允许」，服务器才能发一条。设计稿是画布「值班小程序界面」，规范是设计系统「泉来禾 HVAC 设计系统」的「小程序」一节（链接在问题单里）。
2. **#614**（要现场人员）：核对松雅轩的通道表和机组温度。2026-10-21 用三天的逐小时数据对过（结果在 #614 的评论里）：t1、t2、t3、t4、t10 在冷冻水一侧，t5、t6、t7、t9 在冷却水一侧，t8 没有探头，t14 从 2026-10-29 起也没有探头（原始值一直是 0），和站点文件的「蒸发出 t3/t5/t7/t9、冷凝出 t4/t6/t8/t10」对不上。所以小程序和助手把 C 机组冷冻水出水报成 29–31 ℃（其实是冷却水 t7）。用 15 分钟数据进一步判定出 C、D 的四路，按数据改了（#685）；A、B 的出水温度和四台的冷却水进水先停用，等现场核对 t3、t4、t5、t9、t10。
3. **#641**：用评测前后对比决定 AI 助手要不要用 pi 的 codemode（模型写一小段脚本连续读数据）。
4. **对标复查后补的差距**：2026-10 把 2026-08 的 ThingsBoard / OpenEMS / MyEMS 对标结论按现在的代码重新对了一遍（`docs/research/thingsboard-gap-review-2026-10.md`，91 项：已做 46、不做 29、还差 16）。决定不做和推迟的在 ADR 0026，保留自建身份服务在 ADR 0025，真实站点的指令由网关执行、平台检查在 ADR 0027。迁移已经冻结（#688，规则见 `deploy/platform/phase1/README.md`「数据库和迁移」）。备份传到 COS 并演练恢复（#689）用户说先不做。设备当前值在 Redis 里没有或 Redis 挂了时，改读 PostgreSQL 里的设备当前快照（#692）。其余的按问题单的先后做：Alertmanager 发邮件 → 删掉通用规则、Kafka、统一外发投递和通知收件箱（#691：Kafka 已删，操作记录只走 PostgreSQL 待发事件；统一外发投递、通知收件箱、通知规则和模板已删，网页通知页改读告警，通知模块只发微信提醒，告警也只把新告警交给通知模块；通用规则（「自动化规则」页、网关的规则接口、`rule-runtime-service`、`rule.manage`）已删；服务器上旧的 `outbound_delivery_*`、`rule_runtime_*` 角色由迁移 031、032 停用但没删，因为服务器的 role-credentials 文件还在给它们设密码，把那两行去掉后可以删；在新的空库上（新服务器、恢复备份）用这份旧文件跑迁移会因为角色不存在而中止，也要先去掉那两行，本机的 `npm run local:up` 会自己去掉）→ 设备告警改发企业微信 → 机组和水泵启停指令（#542，等现场确认 ET1010 每一路通道对应哪台设备、每台泵服务哪台机组）。
   - **换云和换域名还只是调研**（`docs/research/cloud-migration-options-2026-10.md`）：现在的 139 是腾讯云轻量应用服务器，迁移后不再用轻量服务器。建议用两台正式的云服务器（腾讯云 CVM 或阿里云 ECS）跑 Docker Compose（应用一台、数据一台，数据盘单独挂载），不用容器服务。不换域名、一个月内要迁，就留在腾讯云广州（约 766 元/月），因为换一家要重新接入备案；迁移时一起换新域名的话，备案在哪家都要重做，阿里云同样配置约 624 元/月。`swayingwindmill.online` 是个人备案，以后会换成带 quanlaihe 的企业域名；换域名时，网关改成按域名连（Mosquitto 证书要加上新域名），微信的 IP 白名单和小程序的合法域名也要一起改。
5. **宣传片**（`tools/promo-video`，2026-10-22 起）：60 秒「冷站的一天」，三维是产品孪生的场景代码按片子时间逐帧渲染，产品界面只取单个组件、按设计系统用代码重画，负荷曲线、能流图和全年节电日历墙也是代码画的，小程序画面在片子里先画出来，配乐和音效用代码合成（`music/compose.ts`）。用法和渲染（WSL 只有软件 WebGL，渲染在 Windows 侧用显卡）见它的 README。片子里先于产品出现的小程序功能记在 `tools/promo-video/film/FEATURE-GAPS.md`，用户要求片子做完后把小程序补齐。

还没做完的大功能：

- **节能验证（#484）**：方法 B（单台机组分表）和方法 C（总表）都已上线并验收过（规格 `docs/specs/savings-verification.md`）。还没做：非常规调整、需量节省、多个措施之间分摊节省量。
- **站点对标（#485）**：可以按能效、单位面积用电、单位面积每度日用电排名（规格 `docs/specs/site-profile-benchmark.md`）。度日现在由页面按每天的平均温度现算，报告或 M&V 也要用时再改成每天的指标。还没有可信的行业基准来源，页面显示「未接入」。

小的后续：

- 改 FDD 低温差的门槛或时间窗口时，发布新版本（不要改旧版本）：`node scripts/publish-fdd-rule-revision.mjs --tenant <uuid> --minimum-delta-tc <K> --window-minutes <n> [--effective-from <时刻>]`。
- 指标计算里的电量差值不会在点位改版或换单位时断开，能耗计算（`counter_deltas_from`）会断开。第一次原地改点位配置之前要补上。
- 能耗序列算电费时，每读一条用电记录约占 0.5 KB ClickHouse 内存。实时电表每块每天约 24,000 条，一个站点一个月约 150 MiB，一年会超过 1.5 GiB 的上限。实时数据上要查长时间范围之前，天和月的粒度要改成读按小时汇总的表。
- 每次能耗差值查询都会把相关点位的整个累计读数历史扫一遍（每个窗口约 700 万行，约 0.5 秒，随历史增长）；`has(points, point_id)` 用不上 `observations` 的排序键（它把 `point_id` 包在 `ifNull` 里）。
- 设备详情侧栏和工单侧栏还要按新设计打磨。

## 本地环境的偏差

当前本地数据库和种子数据不一致的地方。`npm run local:reset` 再 `local:up` 会把它们全部清掉。

- 本地库里还有 `energy_cost` 指标的绑定和定时任务（#575 已经不用这个指标了）：停用对应的 `schedule_definitions` 行，或者重置。
- `infra/telemetry/clickhouse/init/014-tariff-price-windows.sql` 只在空数据卷上执行：已有的 ClickHouse 要手动 `docker exec -i <clickhouse> clickhouse-client --multiquery < …`，再跑一次基础身份初始化，让 `energy-projection` 角色拿到 `tariff.read`。
- 两个模拟站点都有重放的历史：电表（含分表）2026-08-25 到 10-06，气象站的室外温度 2026-08-25 到 10-06（站点时间）。实时历史从 2026-10-12 22:06 开始。A 站点 09-29 09:00 把供水温度设定提高到 8 ℃；B 站点冷冻水泵 09-08 09:00 起从 50 Hz 降到 45 Hz。
- 08-25 到 09-14 这段只重算了 B 站点的 `hourly_energy`、`hourly_cooling` 和 CHWP-01 的 `device_hourly_energy`，A 站点这段的小时和日指标没有。
- 节能项目里有 B 站点的方法 B 验收措施（CHWP-01，09-08 到 09-15）和 A 站点的方法 C 措施（8 ℃ 设定，09-29 09:00 到 10-06）；验收结果也存成了 `e2e/fixtures/savings-measure-*.json`。
- 2026 年 9 月的报告快照是手工排队生成的，只覆盖 30 天里的 16 天（来自重放的历史）。
- 2026-10-13 删掉了两段重放交界处重复写入的 12 条电表读数（导入批次 `01a03480-0400-7000-8000-00000000000{1,2}`）；9-15 00:00（站点时间）那个小时的小时指标是在删除之前算的，还包含这两条读数。
- 2026-10-12 修复分页问题之前算出的用电量缺了一些时段。要补：停 `telemetry-worker`，清空 `analytics.energy_projection_checkpoints`，再启动（运行中清空没用，正在跑的那批会把进度写回去）；已有事实的时段会跳过。
- 几处是手工改的，重置后由初始化脚本恢复：`analytics.energy_interval_facts_visible`（按 `009-energy-fact-rebuild-v1.sql`）、`telemetry_history.counter_deltas_from` 和 `counter_deltas`（按 `004-counter-semantics.sql`）；ClickHouse 用户 `report_service_reader` 已经不用了。
- 没有模拟器也能造几周的历史：`node scripts/local-history-replay.mjs --from … --to … [--commands <站点代码>=<文件>] [--metrics-only | --weather-only]`，经遥测的历史导入写入电表和气象站读数，再重算小时和日指标。三周约 10 分钟一个站点；只重放实时历史开始之前的时间段（累计读数会从头开始）。

## 已知的坑

- **主机待机会停掉 Docker Desktop。** Windows 现代待机之后（系统日志 Kernel-Power 506/507），WSL 里可能找不到 `docker`。启动 `D:\Program Files\Docker\Docker\Docker Desktop.exe`，`hvac-local` 的容器会自己回来。
- **npm 镜像慢。** npmmirror 上有些包要四分钟以上，网页镜像的 `npm ci` 设了 15 分钟超时。外网不通时，`PHASE1_BUILD_PROXY=http://host.docker.internal:7897` 把主机代理传给镜像构建。
- **后台服务的证书在扩展项变了时会重签。** `ensureWorkloadIdentity` 只在 `extensions.cnf` 一致时保留证书，并用一个 root 容器把目录交给 65532 组（目录留在操作者自己的组里，智能服务会因为 `permission denied` 不停重启）。
- **并行构建镜像会耗尽 Windows 的提交内存。** 冷启动 `local:up` 时开着别的程序，可能触发 Resource-Exhaustion 2004，WSL 重启、Docker Desktop 卡死（`docker desktop restart` 能恢复）。一次只构建一个镜像（`COMPOSE_PARALLEL_LIMIT=1`，每个服务单独 `phase1-wsl-compose.mjs ... build <服务>`），再用 `COMPOSE_BAKE=false` 跑 `local:up`。
- **在 worktree 里跑 local:up。** Windows git 建的 worktree 在 `.git` 里存的是 Windows 路径，要设 `GIT_DIR`/`GIT_WORK_TREE`，并把 `PHASE1_RUNTIME_DIR`、`PHASE1_ENV_FILE` 指向主目录的运行时文件和环境文件。
- **Go 代理不稳。** `goproxy.cn` 有时下载失败，用 `PHASE1_GO_PROXY="https://goproxy.cn|https://goproxy.io|https://proxy.golang.org,direct"` 跑 `local:up`，网络出错就重跑。
- **WSL 里没有 C 编译器，跑不了 `go test -race`。** CI 的单元门禁对所有 Go 测试加了 `-race`（#707）。本地要先验证，就在 `golang:1.27.3-bookworm` 容器里跑：`docker run --rm -v /mnt/e/Code/HVAC_web:/src -w /src/<模块> -e GOPROXY=https://goproxy.cn,direct -e GOFLAGS=-race golang:1.27.3-bookworm go test ./...`。静态检查和漏洞扫描（`npm run go:lint`、`npm run security:go-vuln`）在 WSL 里直接跑，第一次会用 `go.work` 里的 Go 版本编译工具。取舍见 `docs/architecture/go-static-checks-source-review-2026-10-26.md`。
- **本地栈开着时跑 e2e。** e2e 构建本身就可能超过 Playwright 240 秒的启动超时，并行的 worker 也会在加载不完的页面上超时。先构建一次 `dist-e2e`，自己开 `vite preview --outDir dist-e2e --port 5190`（配置会复用），再 `npm run web:e2e -- --workers=1`。
- **几个 worktree 同时跑 e2e 会互相借服务器。** 不设 `CI` 时配置会复用 5190 端口上已经开着的服务器，哪怕它是另一个 worktree 构建的，测到的就是别人的代码。并行跑的时候先 `ss -ltnp | grep 5190` 看一眼，或者设 `CI=1` 让它自己构建（端口被占时会直接报错，而不是悄悄复用）。
- **新的网关接口要登记负责方。** 平台网关只放行 `contracts/ownership/route-ownership.v1.json` 里登记过的接口，别的一律回 404「has no applied owner」；网关的接口测试不加载这份登记，所以测试全过、服务器上却是 404（小程序登录和 `/api/v1/me/wechat` 都这样出过事）。现在 `npm run ownership:check` 会检查 `platform-gateway.openapi.yaml` 里的每个接口都登记了；不在这份契约里的接口（例如小程序的）还要自己记得登记。
- **集成测试只在改到对应模块时才跑。** 所以一个迁移弄坏了别的模块的种子数据，要等以后改到那个模块才会发现（例如 #504 修的 Registry 种子写 FDD 结论）。

## 运维手册

出了事按 `docs/operations/` 里写好的步骤做：`disk-full.md`（磁盘写满）、`postgres-crash.md`（PostgreSQL 崩溃）、`restore-from-backup.md`（从夜间备份恢复）、`rollback.md`（把一次部署退回去）、`certificate-renewal.md`（Let's Encrypt 续期）、`rotate-secrets.md`（换密码和密钥）、`alert-receivers.md`（告警发到企业微信或邮箱）。告警规则里的链接仍指向 `docs/operations/alerts.md` 的对应小节。

覆盖率在 `.github/workflows/quality.yml`：Go 语句覆盖率和网页 `src` 的行覆盖率低于已提交的基线（允许 0.1 个百分点的误差）就失败。同一个工作流用现有的 Playwright 录制网关跑登录、告警、实时运行；不在 CI 里把整套 Compose 拉起来，因为那需要运行时密钥和这台小机器装不下的内存。浏览器装不上时这一个任务跳过并留下警告，测试跑了但失败则算失败。

## 流程

- 每项改动一个分支，PR 用 `mattpocock-skills:pr` 技能的模板。CI 绿了再合并。
- 界面要在真实数据上看过（本地 8443 或服务器）；截图脚本用 `scripts/lib/local-administrator-browser.mjs`。
- 用户能注意到的变化写进 `CHANGELOG.md`；下一步或环境偏差变了就更新本文。
