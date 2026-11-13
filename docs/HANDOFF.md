# 交接

现在做到哪、下一步做什么、服务器和本地环境有哪些特殊情况。改了这里说的事，就在同一个 PR 里更新本文（单次读取预算 `<= 28 KiB`，`npm run repo:check` 会检查；详细运维步骤放 `docs/operations/`）。怎么构建、测试和运行见 `AGENTS.md`；用户能看到的变化见 `CHANGELOG.md`；每张单的细节在 GitHub 问题单里。

最后更新：2026-11-13。

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
| 平台 | 通知、集成管理（`/settings/integrations`：现场网关、电表数据核对、传感器校准）、系统管理（用户与角色、改密、AI 助手用量） | 告警（与小程序同源）、网关证书、传感器/电表核对与探头零点建议（`docs/specs/integrations-workspace.md`）、IAM 角色指派（ADR 0022）、操作记录 |
| 手机 | 微信小程序（`apps/hvac-miniprogram`，#664 已上线，0.2.0 审核中） | 经同一网关 `/api/v1/auth/app/sessions`（微信一键登录 `/sessions/wechat`，`Authorization` 头）；总览、运行、通知、我的已开放，助手页为过审暂藏（恢复见小程序 README） |
| 全站 | AI 助手（页头 420px 面板；能耗/告警/设备详情「解释」） | `operations-agent-service` 经平台网关；未配 `DEEPSEEK_API_KEY` 时显示「未接入」 |

日常用电曲线上的电费为按站点分时电价计算的估算值；正式电费以「电费账单」页归档的供电局结算电费单（或 `services/settlement-service`）为准。

## 设计

- 设计系统见 `DESIGN.md`（1.0），视觉基准是 Claude Design 画布 https://claude.ai/artifact/LVwBRsavimfmaBHnRYJbdm 的「定稿」页，令牌在 `apps/hvac-web/src/global.css`；`text-sm` 正文 13/20、`text-xs` 说明 12/18、`text-title` 面板标题 14/20、`text-micro` 单位与徽标 11/16。
- 复用现有组合块：`Main`、`PageHeader` / `PageMeta`、`Panel` / `PanelHeader` / `PanelLink`、`StateCard`、`SeverityMark`、`InfoTip`、`MetricTabsChartCard`、`FactTile`（值样式传 `valueClassName`）、`components/ui/table`、`Status` / `StatusBadge`。
- 路由组合、`routeTree.gen.ts` 新鲜度、`DESIGN.md` §1 文案、Web AST（禁 `as unknown as` / 裸 `fetch` / `WebSocket` / `<Button variant="secondary">` / `"use client"`（`components/ui/` 外）/ `@ts-ignore` / `eslint-disable` / `console.log` / `JSON.parse(JSON.stringify)` 与非 `const` `as` 棘轮 `43`）、浅/深主题 WCAG 对比度与 IAM 权限/角色展示同步由 `npm run typecheck:scripts`、`npm run design:check`、`scripts/test-real-design-tokens.ts` 与 `npm run web:principal:contract` 自动检查；非弹窗未保存表单归所在页面拦截（如系统管理 `features/system/unsaved-drafts.tsx`）。
- 视觉验收：用 `verify` 技能（`features/README.md` 索引全站 23 份功能验收地图、覆盖 24 个 `e2e/*.spec.ts`；Tier 1 走 Vite 5190/5194 录制网关，Tier 2 走 `npm run verify:live` 对 `https://localhost:8443` 跑 1440×900 实库巡检并落盘截图）。

## 服务器（139.199.58.47）

- 运行分支为 `main`，Compose 项目 `hvac-platform`，目录 `/opt/hvac-platform`，`single-lite` 档位（含预测/优化/FDD/M&V/报告与 AI 助手，两个模拟器按要求停用），主机 nginx 转发至 `127.0.0.1:18443`；root 禁密码登录，操作账户 `ubuntu`（需要 root 改运行时目录权限时走免密 `sudo`）。
- **部署流水线**（`.github/workflows/deploy-production.yml`，挂在需要人工审核点头的 GitHub Environment `production` 上；手动触发可勾 `rebuild_base` 重建基础镜像，详见 `docs/operations/disk-full.md#重建部署基础镜像`）：在 GitHub 运行器编译 Go 服务（`1.27.4`，`CGO_ENABLED=0 -trimpath -buildvcs=false -ldflags="-s -w"`）、网页与 AI 助手，按 `git archive` + zstd 增量包通过 SSH 传至 `/opt/hvac-platform`（保留运行目录密钥和数据），给旧镜像打 `before-<时间>` 后只替换 `/service`、`/healthcheck` 与网页 dist，然后执行 `deploy/platform/phase1/cd/deploy.sh`（失败回退老的 `/opt/hvac-platform-tools/deploy.sh` 或运行 `cd/rollback.sh`）。部署前先验证本机及公网 `/healthz`、`/sign-in`、`/login`、身份发现与容器健康，成功后只保留最近两套 `before-*` 镜像并清悬空镜像。所需 Actions secrets：`DEPLOY_SSH_KEY`（优先）或 `DEPLOY_SSH_PASSWORD`（可选 `DEPLOY_SSH_USER`、`DEPLOY_SSH_HOST`、`DEPLOY_SSH_PORT`）。只重启单服务用 `node scripts/phase1-wsl-compose.mjs --integration [--intelligence] up -d --no-deps <服务>`。
- `DEEPSEEK_API_KEY` 在 `server.runtime.env` 中由用户维护。
- 服务器运行着的 Go 服务仍为旧的 Go 1.25.12 构建（仓库从 #707 起用 Go 1.27.4、`golang.org/x/text` v0.41.0），须等生产部署流水线跑通一次才会换上 1.27.4。
- 夜间备份由 `hvac-backup.timer` 写在本机同一块盘上，尚未拷到异地；两个演示站点（`central-plant`、`local-energy-site-b`）的 28 个调度定义已停用。
- 告警发送：在运行目录 `alert-receiver/` 放企业微信机器人地址与/或 SMTP（样例见 `observability/alert-receiver.example/`，步骤见 `docs/operations/alerts.md`）；Prometheus 使用仓库内 `observability/prometheus.yaml`（15 天 / 2 GB 上限），Centrifugo 指标从容器内网明文 9000 抓取。
- 磁盘与清理：#553 每小时清理已送达历史事件和 7 天前原始读数；#651 仅在告警判断结果或规则版本变化时写 `alarm_runtime.alarm_evaluation_event` 并清理 30 天前记录。磁盘清理与 `VACUUM FULL telemetry_runtime.source_observations` 见 `docs/operations/disk-full.md`（空闲空间不足数据体量前勿跑 `VACUUM FULL`）。

### 松雅轩（第一个真实站点）

- 规格：`docs/specs/site-gateway-onboarding.md`。Node-RED 网关通过双向 TLS 连 8884 端口（nginx 转 Mosquitto），证书 2027-01-12 到期；ThingsBoard 已停（备份在 `/opt/backups/thingsboard/`），历史导入批次 `01a14000-7000-7000-8000-000000000001`（截至 `2026-10-14T10:26:30Z`）的按设备条数尚未核对。
- 网关支持耐久发件箱（24 小时 MQTT 会话落盘）；ET1010 控制指令下发待接入（#696）；修改单个 Modbus 读取节点会卡住同串口其余六个，改后须重启所有流程。
- **能耗与电表**：站点启用合成总表（`"totalMeter": {"kind": "sum-of-sub-meters"}`，#798），将四台冷机分表相加（有表缺数时显示已有之和并标「不完整」；网关计算的总表保留为普通电表对照）。四块电表名与从站已按 2026-11-12 核实统一：`A 机组电表`（UID 3）、`B 机组电表`（UID 4，电量 = 寄存器 60 - 基数 `76938` kWh）、`C 机组电表`（UID 5）、`D 机组电表`（UID 2，RS485-1 `/dev/ttyAS3` 仍无应答待现场查线）。超过 1 小时断点后的恢复差值记为跨缺口电量（`is_cross_gap = 1`），计入周期总量与账单但不进逐时图。电价暂用深圳分时电价。ClickHouse 迁移 `015`、`016` 及 `site-onboarding.mjs apply` 已在服务器生效。
- **温度与在线**：温度通道按 2026-11-08 手绘点位表配置（A–D 蒸发出 t1–t4、冷凝出 t6–t9、冷却总管进 t5、A 支管进 t10、冷却塔出/进 t12/t13、室外 t15、总回 t16–t17、总供 t18–t19；t14/t20 无探头；房间暂无探头，房间温度停用）。带 `from` 的复制点位不写在线信号，无自有启用点位的设备（冷却塔、室外环境、房间）设为 `NOT_APPLICABLE`；`telemetry-worker` 每分钟执行 `SweepDuePresence` 置离线（#797，已在服务器生效）。
- **告警规则**（`deploy/sites/songyaxuan.alarm-rules.json`）：用 `modules/alarm/cmd/alarm-rules` 发布（未在文件里的规则按 `RULE_RETIRED` 自动撤销并恢复）。2026-11-13 已在服务器套用最新站点文件并发布 112 条规则：含 13 个关键点位的「读数中断」（15 分钟，#796）、其余设备自有启用点位的「读数长时间没有更新」（30 分钟）、网关整站 78 点位 15 分钟无读数中断，以及超过 24 小时未确认读数中断自动升一级并推每日 08:00 微信汇总（#800）。数据质量告警（#750/#751/#752，规格 `docs/specs/telemetry-data-quality.md`）须待服务器上按顺序跑完迁移 `011-point-quality.sql`、部署服务、`site-onboarding.mjs apply` 与规则发布后才替换旧的 6 小时「一动不动」提示。在服务器查看/发布命令：`docker run --rm --network container:hvac-platform-energy-api-1 --volumes-from hvac-platform-energy-api-1 -e ALARM_DATABASE_URL_FILE=/run/hvac/secrets/alarm.dsn -v <目录>:/alarm-rules:ro --entrypoint /alarm-rules/alarm-rules <energy-api 镜像> /alarm-rules/songyaxuan.alarm-rules.json`（加 `-print` 只检查不写库）。
- ClickHouse 数据修补遗留三张备份表（`telemetry_history.d4fix_backup_observations`、`analytics.d4fix_backup_energy_facts`、`telemetry_history.coolfix_backup_observations`），确认页面数据无误后删除。
- 接入核对与优化限制：`node scripts/site-onboarding.mjs validate deploy/sites/songyaxuan.site.json` 尚余 7 项人工决定（`alarm-rules -print` 核对、低温差诊断 3.5 K 与实际约 1.65 ℃ 阈值、优化室温点位编码冲突、四个响应系数与是否启用优化调度）。因无房间温度探头，松雅轩当前不登记优化建议；若将来加探头，须重新确定参数后再启用。

## AI 助手

- 规格 `docs/specs/operations-assistant-v1.md`、`docs/specs/agent-semantic-query.md`；运行时使用 pi（`@earendil-works/pi-agent-core` 1.3.0，ADR 0014），面板基于 assistant-ui。
- 只读工具覆盖资产、设备、实时与历史读数、能耗序列与拆分、语义查询、告警、诊断与未完工单；每次回答开始写入站点与本地时间，失败报 `SITE_CONTEXT_UNAVAILABLE`，每次最多 12 次模型调用、24 次工具调用。支持多轮续问（模型只看到历史文字不看卡片，追问重读数据）、结论卡片绑定工具原生 `chartFrom` 图表，并在系统管理「AI 助手」页按 `failure_code` 展示失败原因（限有操作记录权限者查看）。
- 评测基准（`services/operations-agent-service/evals/BASELINE.md`，2026-10-20，#663 后 24 题 × 3 轮）：67/72 全部检查通过，平均 4.1 秒 / 读 1.7 次数据；已知问题包括约 3% 无正文只出卡片、跨站点提问偶有直接答当前站、设备名含 `UID` 非纯中文、正文偶漏 `UNABLE_TO_CONCLUDE`，以及数据不全题易超调用上限；现支持 `npm run operations-agent:eval` 命令行 Bearer 会话认证执行与自动注销清理。

## 能耗和 Cube

- 所有能耗读取经过 Cube（ADR 0023）三个视图：`site_energy`、`equipment_energy`、`site_energy_cost`（按 UTC 分时电价表 `analytics.tariff_price_windows` 计价，`docs/specs/estimated-energy-cost.md`）；后台服务以 ADR 0017 身份领取 30 秒授权访问（证书由 `scripts/issue-workload-identities.mjs` 签发）。网页仍自己用供冷除以用电算能效。

## 下一步

按顺序推进：

1. **点位质量与不确定度上线（#750/#751/#752）**：代码已进 `main`（网页「集成管理」已支持电表核对与探头零点建议），须在服务器按顺序执行数据库迁移、部署新服务、`site-onboarding.mjs apply` 与 `alarm-rules` 发布。
2. **#664 微信小程序**：绑定、告警提醒、一键登录与界面改版已上线；0.2.0 已提交微信审核，待过审发布后把服务器 `IDENTITY_WECHAT_MINIPROGRAM_STATE` 改为 `formal` 并重启 `identity-service`；另待真机收到第一条每日运行结论（点「多收一天」）对数后关 #664 与 #677。以后小程序界面改动不靠开发者工具慢速截图，直接按单元测试守住整理逻辑、真机验收。`identity` / `notification` 库测试为 `npm run test:identity-postgres`、`npm run test:notification-postgres`；点位词典同步用 `npm -w @hvac/miniprogram run sync`。
3. **#614 现场探头补齐**：等松雅轩现场加装房间温度探头后接回点位并评估是否恢复优化调度。
4. **#641 AI 助手**：用评测前后对比决定是否启用 pi codemode。
5. **对标复查（91 项余 16 项，`docs/research/thingsboard-gap-review-2026-10.md`）**：按顺序做 Alertmanager 发邮件 → 清理服务器残留旧数据库角色配置（`outbound_delivery_*`、`rule_runtime_*` 须从服务器 `role-credentials` 删除那两行后再彻底删角色，以免新空库迁移中止；本机 `local:up` 会自动处理）→ 企业微信设备告警 → 机组/水泵启停控制（#542，待现场确认 ET1010 通道）；备份上云（#689）暂不做；换云/换企业域名调研见 `docs/research/cloud-migration-options-2026-10.md`。
6. **宣传片与小程序对齐**：`tools/promo-video`（Windows 显卡渲染）里先画出的小程序功能见 `tools/promo-video/film/FEATURE-GAPS.md`，片后补齐。
7. **功能与技术收尾**：节能验证（#484：非常规调整、需量与多措施分摊）、站点对标（#485：行业基准来源接入）、COP 影子模型积累后决定是否切换发布求解器、FDD 低温差新版本阈值（`node scripts/publish-fdd-rule-revision.mjs`，水泵 15/22 kW 铭牌与运行状态绑定待现场核实）、长范围分时电费查询内存优化、服务器部署能耗小时汇总（按 `docs/operations/energy-hourly-rollup.md`）、设备详情与工单侧栏新设计打磨。

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

- 故障与变更手册见 `docs/operations/`：`disk-full.md`、`postgres-crash.md`、`restore-from-backup.md`、`rollback.md`、`certificate-renewal.md`、`rotate-secrets.md`、`alert-receivers.md`、`alerts.md`、`energy-hourly-rollup.md`。
- `.github/workflows/quality.yml` 检查 Go 语句与网页 `src` 行覆盖率不低于基线（允许 0.1 个百分点误差），并用录制网关跑登录、告警与实时运行 e2e。
