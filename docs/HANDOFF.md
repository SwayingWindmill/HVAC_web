# 服务器、现场与环境基线

记录线上服务器、松雅轩现场和本地环境的当前事实与已知偏差（单次读取预算 `<= 28 KiB`，`npm run repo:check` 会检查；详细运维步骤放 `docs/operations/`）。**本文件不作为开发计划或「下一步」任务清单**；需求排期与进度认领见 GitHub Issues（`docs/agents/issue-tracker.md`），跨会话断点续跑、暂停留痕与交付流程一律按 `pstack`（`.agents/skills/poteto-mode/SKILL.md`）执行。怎么构建、测试和运行见 `AGENTS.md`；用户能看到的变化见 `CHANGELOG.md`。

最后更新：2026-11-23。

## 产品现在有什么

下面的页面都读真实的数据来源（本地 `hvac-local`，https://localhost:8443；服务器 https://hvac.swayingwindmill.online）：

| 分组 | 页面 | 数据从哪来 |
| --- | --- | --- |
| 总览 | `/overview` | 能耗、冷站实时运行、告警、故障诊断、工单、优化建议、电价、关键点位数据缺口提示条（`GET /api/v1/sites/{siteId}/data-gaps`，与能耗分析、实时运行、能源报告共用，有中断时直达电表数据核对，#799） |
| 运行 | 实时运行、历史趋势（`/operations/trends`）、运行计划（`/operations/schedules`）、设备、数字孪生（`/operations/digital-twin`） | 遥测快照与推送、设备登记与历史（1 分钟 / 5 分钟 / 15 分钟 / 按小时 / 按日汇总）、告警重叠窗口、控制指令；运行计划由登记模块保存每周供冷窗口与全天例外，核查停机窗口内的计划外开机并创建现场核查工单（目标供水温度仅作交接参考，`docs/specs/operating-schedule.md`）；数字孪生末端楼宇按站点写在 `twin-building.ts`（目前只有松雅轩，规格 `docs/specs/digital-twin.md`） |
| 能源 | 能耗分析（含「用电去向」总表和分表）、用电负荷（含负荷预测）、电费账单（`/energy-analysis/billing`） | 能耗序列（Cube）、设备统计、站点电价、负荷预测；电费账单由登记模块保存月度预算与两部制结算电费单（专变核对或整栋总表分摊，确认归档后在能源报告旁路展示，`docs/specs/electricity-billing.md`） |
| 能源 | 优化建议（`/energy-analysis/optimization`） | 预测每小时 `:07` 跑，优化每小时 `:20` 跑（可手动触发）；「当前建议」按有无可写供水温度设定点走控制审批或创建人工调温工单，「模型与负荷分析」只读查询冷机 COP 影子评估与 24 小时负荷预测（`docs/specs/optimization-workspace.md`、`docs/specs/chiller-cop-model.md`） |
| 能源 | 节能项目（`/energy-analysis/savings`） | `mv-service`：节能措施、固定基线、已验证日（ADR 0021） |
| 能源 | 能源报告（`/reports`） | 已结束月份读 `report-service` 快照（每月 1 日 03:00 生成），本月和无快照月份现算；有年度视图，可打印 A4 |
| 能源 | 站点对标（`/benchmark`） | 有权限站点的能耗序列；无能耗权限站点列出但不参加比较 |
| 运维 | 告警、故障诊断、工单、控制指令 | 告警、FDD（每 15 分钟跑一次）、工单、控制指令审批 |
| 平台 | 通知、集成管理（`/settings/integrations`：现场网关、无线集中器、电表数据核对、传感器校准、OTA 升级）、系统管理（用户与角色、改密、AI 助手用量） | 告警（与小程序同源）、网关证书、无线集中器参数与 EID 远程配置、边缘程序与固件 OTA 升级（`#920`）、传感器/电表核对与探头零点建议（`docs/specs/integrations-workspace.md`）、IAM 角色指派（ADR 0022）、操作记录 |
| 手机 | 微信小程序（`apps/hvac-miniprogram`，#664 已上线，0.2.0 审核中） | 经同一网关 `/api/v1/auth/app/sessions`（微信一键登录 `/sessions/wechat`，`Authorization` 头）；总览、运行、通知、我的已开放，助手页为过审暂藏（恢复见小程序 README） |
| 全站 | AI 助手（页头 420px 面板；能耗/告警/设备详情「分析原因」） | `operations-agent-service` 经平台网关；未配 `DEEPSEEK_API_KEY` 时显示「未接入」 |

日常用电曲线上的电费为按站点分时电价计算的估算值；正式电费以「电费账单」页归档的供电局结算电费单（或 `services/settlement-service`）为准。

## 设计

- 设计系统见 `DESIGN.md`（1.0），视觉基准是 Claude Design 画布 https://claude.ai/artifact/LVwBRsavimfmaBHnRYJbdm 的「定稿」页，令牌在 `apps/hvac-web/src/global.css`；`text-sm` 正文 13/20、`text-xs` 说明 12/18、`text-title` 面板标题 14/20、`text-micro` 单位与徽标 11/16。
- 复用现有组合块：`Main`、`PageHeader` / `PageMeta`、`Panel` / `PanelHeader` / `PanelLink`、`StateCard`、`SeverityMark`、`InfoTip`、`MetricTabsChartCard`、`FactTile`（值样式传 `valueClassName`）、`components/ui/table`、`Status` / `StatusBadge`。
- 路由组合、`routeTree.gen.ts` 新鲜度、`DESIGN.md` §1 文案、Web AST（禁 `as unknown as` / 裸 `fetch` / `WebSocket` / `<Button variant="secondary">` / `"use client"`（`components/ui/` 外）/ `@ts-ignore` / `eslint-disable` / `console.log` / `JSON.parse(JSON.stringify)` 与非 `const` `as` 棘轮 `43`）、浅/深主题 WCAG 对比度与 IAM 权限/角色展示同步由 `npm run typecheck:scripts`、`npm run design:check`、`scripts/test-real-design-tokens.ts` 与 `npm run web:principal:contract` 自动检查；非弹窗未保存表单归所在页面拦截（如系统管理 `features/system/unsaved-drafts.tsx`）。
- 视觉验收：用 `verify` 技能（`features/README.md` 索引全站 23 份功能验收地图、覆盖 31 个 `e2e/*.spec.ts`；Tier 1 走 Vite 5190/5194 录制网关，Tier 2 走 `npm run verify:live` 对 `https://localhost:8443` 跑 1440×900 实库巡检并落盘截图）。

## 服务器（139.199.58.47）

- 告警发送：平台告警在运行目录 `alert-receiver/` 放企业微信机器人地址与/或 SMTP（样例见 `observability/alert-receiver.example/`，步骤见 `docs/operations/alerts.md`）；设备告警不走 Alertmanager，由 `energy-api` 按 `deploy/sites/*.alarm-routing.json` 经 `wecom-alert-adapter` 发送（默认 `ALARM_DELIVERY_MODE=off`，收件人与令牌放运行目录 `alarm-delivery/`，企业微信自建应用密钥放 `alert-receiver/`，见 `docs/operations/alarm-delivery.md`）。Prometheus 使用仓库内 `observability/prometheus.yaml`（15 天 / 2 GB 上限），Centrifugo 指标从容器内网明文 9000 抓取。
- 磁盘与清理：#553 每小时清理已送达历史事件和 7 天前原始读数；#651 仅在告警判断结果或规则版本变化时写 `alarm_runtime.alarm_evaluation_event` 并清理 30 天前记录。磁盘清理与 `VACUUM FULL telemetry_runtime.source_observations` 见 `docs/operations/disk-full.md`（空闲空间不足数据体量前勿跑 `VACUUM FULL`）。
- 当前线上基线（2026-11-23 平台服务器已全量上线 `44469e77`，松雅轩 EG8200 现场已升级至 Go `hvac-edge-gateway` `0.1.5` [`slots/slot-b` 激活，`previous -> slots/slot-a` 保留 `0.1.4`]，涵盖 `#921` 端侧模型与超参热更新、`#922` 1Hz 波形抓拍与远程诊断、`#953` 跨站告警规则模板库与偏离重置、`#997`–`#1009` 性能优化与 `CHANGELOG.md` 41 条碎片归并及 `gateway-waveform-and-diagnostics.sql`、`rule-baseline-templates.sql`、MQTT ACL `up/waveform`/`up/diagnostics`/`up/metrics`/`down/diagnostics` 生效；已验证 `/healthz` `ok`、全容器健康、Prometheus `up_down=0` 与 `hvac_mqtt_online_gateways=1`、`hvac_s1`/`hvac_s4` 新表及 `energy-hourly-rollup --verify` 6 个站月零偏差核验）：已签发 `alarm` 内部工作负载证书（`spiffe://hvac.local/alarm-service`）并执行 `alarm-edge-event-projection.sql` 等迁移；宿主机 `nginx` 终止 `:443/gateway/v1/*` 网关双向 TLS 并以内部工作负载身份 `host-edge-proxy` 向容器 `nginx` 透传已校验的 `X-Gateway-Client-Cert`，已启用 `upstream hvac_edge`（`keepalive 32`）并对 `/assets/` / `/` 开启缓冲与连接复用，同时保持 `/realtime/`、`/api/v1/assistant/` 与 `/gateway/v1/tunnel` 非缓冲；宿主机 `hvac-gateway-nodered-loopback.socket` 仅监听 `127.0.0.1:18800` 提供现场 Node-RED 常驻回环访问（`docs/operations/gateway-nodered-tunnel.md`），SSH 运维通道经 `CONNECTIVITY_TUNNEL_SSH_LOOPBACK_ADDR=127.0.0.1:18822` 按需开放；遗留 PostgreSQL 角色已清理，迁移 `011-point-quality.sql`、`006_alarm_delivery.sql`、`006-gateway-concentrator-audit.sql`、`027-energy-query-acceleration.sql`、`alarm_runtime.site_rule_bundle_revision`、`connectivity` `edge_rule_bundle`、`site-onboarding.mjs apply` 与 `songyaxuan.alarm-rules.json`（116 条规则）已上线生效。
- 待人工触发的服务器上线操作：① 企业微信 `alarm-delivery`（联动 `#695`）在运行目录放齐 `recipients.json`、`alarm-send-token` 与自建应用密钥及可信 IP 后，按 `docs/operations/alarm-delivery.md` 先跑至少 1 天 `dry-run` 演练、替换松雅轩占位收件人再切换 `live`（稳定 1 周后按 ADR 0026 移除小程序订阅提醒）；② 微信小程序 0.2.0 过审发布后把服务器 `IDENTITY_WECHAT_MINIPROGRAM_STATE` 改为 `formal` 并重启 `identity-service`（#664）。

### 松雅轩（第一个真实站点）

- 规格：`docs/specs/site-gateway-onboarding.md`。现场 EG8200（网关 `01a14000-4000-7000-8000-000000000100`，证书 `2027-01-12` 到期）已实测打通真实 `root` SSH 回环隧道（`127.0.0.1:18822 -> 22`，见 `.agents/skills/eg8200-linux-access/SKILL.md`）：`Ubuntu 20.04.5 LTS` / `Linux 5.4.61 armv7l`（双核 Cortex-A7）、485 MiB RAM、宿主机 `/etc` 与 `/etc/systemd/system` 可读写。2026-11-23 已完成向独立 Go 边缘守护进程 `hvac-edge-gateway` `0.1.4` 切流并滚动升级至 `0.1.5`（`slots/slot-b` 生效，`previous -> slots/slot-a` 保留 `0.1.4`，常驻内存约 `11–12 MiB`，已验证实时上报 `connectivity.gateway_host_metrics`）：Go 进程直接接管 `/dev/ttyAS3`（`9600 8N1 45ms` RS-485 优先级总线锁）、`:8884` 双向 TLS MQTT 上行（`temperature`、`temperature-status`/`voltage`/`rssi`、`waterflow`、`chwp-main`、`ammeter-unit1..4`、`ammeter-total`、`btu-calc` 与 `et1010` 全部 31 台设备连续多周期实测入库）、ET1010 顺序安全执行器（含 `power === null` 禁盲停保护与 `START_UNIT_SEQUENCE` 逆序回滚，替代 Node-RED）、`UID 20` 无线集中器 MQTT RPC、14 天自动证书续签及 `:443/gateway/v1/tunnel` SSH 回环（`127.0.0.1:18822 -> 22`，审计表 `connectivity.gateway_tunnel_audit` 已验）。原 `node-red.service` 已停止并 `systemctl mask`，`/home/pi/edge8000/app/usr_service_auto_startup.sh` 中 `ZHCSharpMid` 守护段已注释禁用（保留 `.bak-pre-cutover` 备份）；`ThingsBoard` 已停（备份在 `/opt/backups/thingsboard/`），历史导入批次 `01a14000-7000-7000-8000-000000000001`（截至 `2026-10-14T10:26:30Z`）的按设备条数尚未核对。
- 网关支持耐久发件箱（24 小时 MQTT 会话落盘）；ET1010 20 通道控制（12 路保持型水泵/塔 `LEVEL`、8 路主机制动开机/关机脉冲 `PULSE` `u14..u44`/`u51..u54` 与一键冷机组启停 `UNIT_SEQUENCE`）、Registry 路由视图 `command_point_routes_v1`（迁移 `038`）、双闸门水路联锁与 Edge 执行器均已进代码并配置 `songyaxuan.site.json`；修改单个 Modbus 读取节点会卡住同串口其余六个，网关流更新须在人工审定后重启全部流程。
- **能耗与电表**：站点启用合成总表（`"totalMeter": {"kind": "sum-of-sub-meters"}`，#798），将四台冷机分表相加（有表缺数时显示已有之和并标「不完整」；网关计算的总表保留为普通电表对照）。四块电表名与从站已按 2026-11-15 结合运行数据重新核对确认：`A 机组电表`（UID 4，`ammeter-unit4`，电量 = 寄存器 60 - 基数 `76938` kWh）、`B 机组电表`（UID 3，`ammeter-unit3`）、`C 机组电表`（UID 5，`ammeter-unit1`）、`D 机组电表`（UID 2，`ammeter-unit2`，随 C、D 各自同时计量对应机组、水泵与冷却塔；B/C/D 机箱改造后四表均在线）。超过 1 小时断点后的恢复差值记为跨缺口电量（`is_cross_gap = 1`），计入周期总量与账单但不进逐时图。电价暂用深圳分时电价。ClickHouse 迁移 `015`、`016` 及 `site-onboarding.mjs apply` 已在服务器生效。
- **温度与在线**：站点清单已同步 2026-11-14 现场温度通道：A–D 蒸发出 t1–t4、冷却总管进 t5、A–D 冷凝出 t6–t9（其中 t8 槽位绑定物理 EID 50）、A 支管进 t10、冷却塔内侧总管 t11（槽位 11 绑定物理探头 EID 51；空槽位 t51 标 `INACTIVE`）、外侧总管 t12（与 t55 同点位共存，冷却塔出水优先取在线的 t55）、冷却水塔水 t13（暂作进塔）与 t14（已启用）、室外 t15、总回 t16–t17、总供 t18–t19、大厅室内 t53（`room/room_temperature`）、宴会厅室内 t54（`room/banquet_room_temperature`）；仅保留 t20 与 t51 为 `INACTIVE`（网关 `SPARE` 屏蔽 t20、t51 与离线停用的 t52）。带 `from` 的复制点位不写在线信号，无自有启用点位的设备（冷却塔、室外环境、房间）设为 `NOT_APPLICABLE`；`telemetry-worker` 每分钟执行 `SweepDuePresence` 置离线（#797，已在服务器生效）。现场 EG8200 已由 `hvac-edge-gateway` `0.1.5` 全量接管 `1–19` 与 `53–55` 通道轮询及诊断上报（屏蔽备用/空槽位 `20、51` 与停用槽位 `52`），实测 `18` 路在线、`4` 路探头当前物理离线并实时上送 `OFFLINE`。
- **告警规则**（`deploy/sites/songyaxuan.alarm-rules.json`）：用 `modules/alarm/cmd/alarm-rules` 发布（未在文件里的规则按 `RULE_RETIRED` 自动撤销并恢复）。站点清单现含温度模块电池电压/信号强度/通道状态三组诊断设备及 14/53/54/55 通道与大厅/宴会厅室内温度（共 159 个启用观测点位、116 条生效规则，告警快照上限 `MaxEvaluationSnapshotInputs = 512`），含 13 个关键点位的「读数中断」（15 分钟，#796）、其余设备自有启用点位的「读数长时间没有更新」（30 分钟）、网关整站 15 分钟无读数中断，以及超过 24 小时未确认读数中断自动升一级并推每日 08:00 微信汇总（#800）。数据质量（`011-point-quality.sql`、`site-onboarding.mjs apply`）与 `songyaxuan.alarm-rules.json`（116 条规则）已在服务器生效；在服务器查看/发布时先静态编译 `modules/alarm/cmd/alarm-rules` 再运行：`docker run --rm --user 0 --network container:hvac-platform-energy-api-1 --volumes-from hvac-platform-energy-api-1 -e ALARM_DATABASE_URL_FILE=/run/hvac/secrets/alarm.dsn -v <二进制>:/usr/local/bin/alarm-rules:ro -v /opt/hvac-platform/deploy/sites:/sites:ro --entrypoint /usr/local/bin/alarm-rules postgres:16.4-bookworm /sites/songyaxuan.alarm-rules.json`（加 `-print` 只检查不写库）。
- ClickHouse 数据修补遗留三张备份表（`telemetry_history.d4fix_backup_observations`、`analytics.d4fix_backup_energy_facts`、`telemetry_history.coolfix_backup_observations`），确认页面数据无误后删除。
- 接入核对与优化限制：`node scripts/site-onboarding.mjs validate deploy/sites/songyaxuan.site.json` 尚余 7 项人工决定（`alarm-rules -print` 核对、低温差诊断 3.5 K 与实际约 1.65 ℃ 阈值、优化室温点位编码冲突、四个响应系数与是否启用优化调度）。大厅（t53）与宴会厅（t54）室内温度已写入站点清单，但因优化响应系数未标定且待与租户共用策略确认，当前仍不登记优化建议。

## AI 助手

- 规格 `docs/specs/operations-assistant-v1.md`、`docs/specs/agent-semantic-query.md`；运行时使用 pi（`@earendil-works/pi-agent-core` 1.3.0，ADR 0014），面板基于 assistant-ui。
- 只读工具覆盖资产、设备、实时与历史读数、能耗序列与拆分、语义查询、告警、诊断与未完工单；每次回答开始写入站点与本地时间，失败报 `SITE_CONTEXT_UNAVAILABLE`，每次最多 12 次模型调用、24 次工具调用。支持多轮续问（模型只看到历史文字不看卡片，追问重读数据）、结论卡片绑定工具原生 `chartFrom` 图表，并在系统管理「AI 助手」页按 `failure_code` 展示失败原因（限有操作记录权限者查看）。
- 评测基准（`services/operations-agent-service/evals/BASELINE.md`，2026-10-20，#663 后 24 题 × 3 轮）：67/72 全部检查通过，平均 4.1 秒 / 读 1.7 次数据；已知问题包括约 3% 无正文只出卡片、跨站点提问偶有直接答当前站、设备名含 `UID` 非纯中文、正文偶漏 `UNABLE_TO_CONCLUDE`，以及数据不全题易超调用上限；现支持 `npm run operations-agent:eval` 命令行 Bearer 会话认证执行与自动注销清理。

## 能耗和 Cube

- 所有能耗读取经过 Cube（ADR 0023）三个视图：`site_energy`、`equipment_energy`、`site_energy_cost`（按 UTC 分时电价表 `analytics.tariff_price_windows` 计价，`docs/specs/estimated-energy-cost.md`）；后台服务以 ADR 0017 身份领取 30 秒授权访问（证书由 `scripts/issue-workload-identities.mjs` 签发）。网页仍自己用供冷除以用电算能效。

## 本地环境的偏差

当前本地库与种子数据可能存在历史偏差；运行 `npm run local:reset` 再 `local:up` 可全部清掉重来：

- 本地库若仍保留已废弃的 `energy_cost` 调度定义，可停用或重置；老数据卷若缺少 ClickHouse `014`、`015`，按 `docs/operations/energy-hourly-rollup.md` 手工应用并回填，或直接重置（手工修改的 `energy_interval_facts_visible`、`counter_deltas_from`、`counter_deltas` 重置后由初始化脚本恢复；`report_service_reader` 已不用）。
- 两个模拟站点保留 2026-08-25 至 10-06 重放历史与 10-12 22:06 起实时历史；A 站缺 08-25 至 09-14 汇总指标；节能验收与 9 月报告快照（含 `e2e/fixtures/savings-measure-*.json`）来自该重放区间；10-12 分页修复前若缺电量子时段，须停 `telemetry-worker` 清 `analytics.energy_projection_checkpoints` 再启动补算。
- 无模拟器造几周历史：`node scripts/local-history-replay.mjs --from … --to … [--commands <站点代码>=<文件>] [--metrics-only | --weather-only]`（三周约 10 分钟/站点，仅重放实时历史之前时段）。

## 已知的坑

- **主机待机会停掉 Docker Desktop**：睡眠恢复后若 WSL 找不到 `docker`，启动 `D:\Program Files\Docker\Docker\Docker Desktop.exe`。
- **npm 与 Go 代理**：外网慢时镜像构建传 `PHASE1_BUILD_PROXY=http://host.docker.internal:7897`；Go 代理不稳时设 `PHASE1_GO_PROXY="https://goproxy.cn|https://goproxy.io|https://proxy.golang.org,direct"`。
- **并行构建耗尽内存 / 证书权限**：冷启动防 OOM 可用 `COMPOSE_PARALLEL_LIMIT=1` 分个 build 再配 `COMPOSE_BAKE=false` 拉起 `local:up`；`ensureWorkloadIdentity` 须保留目录属组 `65532` 防智能服务 `permission denied`。
- **worktree 里跑 `local:up` 或 e2e**：Windows worktree 的 `.git` 为 Windows 路径，跑 `local:up` 须传 `GIT_DIR`/`GIT_WORK_TREE`、`PHASE1_RUNTIME_DIR`、`PHASE1_ENV_FILE` 指向主目录；跑 e2e 时不设 `CI` 会复用 5190 端口上的任意服务，并行请加 `CI=1 PLAYWRIGHT_PORT=5194`（见 `verify` 技能）。
- **WSL 缺少 C 编译器跑 `go test -race`**：PR 门禁会对 Go 单测加 `-race`，本地复现用容器：`docker run --rm -v /mnt/e/Code/HVAC_web:/src -w /src/<模块> -e GOPROXY=https://goproxy.cn,direct -e GOFLAGS=-race golang:1.27.4-bookworm go test ./...`。
- **新增网关接口要登记**：网关仅放行 `contracts/ownership/route-ownership.v1.json` 登记的接口；`platform-gateway.openapi.yaml` 里的接口已有 `npm run ownership:check` 守住，未进 OpenAPI 契约的网关接口须同步手工登记，集成测试不会替你拦截。
- **集成测试按模块触发**：新增迁移若影响其他模块种子数据，当时可能不被该模块集成测试跑到。

## 运维手册与覆盖率

- 故障与变更手册见 `docs/operations/`：`disk-full.md`（磁盘写满）、`postgres-crash.md`（Postgres 崩溃）、`restore-from-backup.md`（从备份恢复）、`rollback.md`（回滚）、`certificate-renewal.md`（证书续期）、`rotate-secrets.md`（密钥轮换）、`alert-receivers.md`（平台告警接收）、`alarm-delivery.md`（设备告警按站点发送与升级）、`alerts.md`、`energy-hourly-rollup.md`。
- `.github/workflows/quality.yml` 检查 Go 语句与网页 `src` 行覆盖率不低于基线（允许 0.1 个百分点误差），并用录制网关跑登录、告警与实时运行 e2e；控制指令与 MQTT 关联不变量另由 `npm run command:formal`（`command-safety`）在静态/单测门禁中穷举校验。
- Agent 技能树已完成 `pstack` 全量落地与 Antigravity + Windows/WSL 适配（共 `48` 个技能：`43` 个 `pstack` 技能 + `verify` + `command-safety` + 3 个 UI 技能，配置全部 `23` 套剧本及 `.ts`/`.sh` 辅助脚本；仅 Cursor 云端 `make-bot-ui` 因无宿主 API 未装）：统一了 `~/.gemini/config/GEMINI.md`、`~/.gemini/config/skills/`、`call_mcp_tool` / `mcp_<server>_<tool>`、`npm run ui:shadcn`、WSL 登录壳（`wsl -e bash -lc`）、Windows worktree 与 WSL `git`/`gh` 隔离、`worktree-audit.ts` URL 编码轨迹匹配与防误删保护，并在 WSL 仓库外预装 `mise`、`java@temurin-25`、`tla2tools@1.7.4`、`ffmpeg`/`ffprobe` 及 `/home/haozhang/.local/share/pele`（`pele@1.9.0`）；`.agents/skills/` 与 `.claude/skills/` 逐字节 1:1 镜像由 `npm run repo:check` 自动校对。
