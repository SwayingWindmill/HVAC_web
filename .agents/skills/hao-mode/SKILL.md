---
name: hao-mode
description: "Personal engineering mode for HVAC_web on top of poteto-mode: routes natural Chinese goals to pstack skills automatically, enforces multi-session worktree isolation, full-stack wiring self-audits, CLI/API-first operations without manual homework, prototype-first UI redesigns, Chinese-only replies across Antigravity and Claude Code, and three-tier completion menus. Use for /hao-mode, hao-mode, or day-to-day HVAC_web diagnosis, planning, implementation, deployment, and progress reviews."
---

# Hao mode

本模式叠加在 [`poteto-mode`](../poteto-mode/SKILL.md) 之上，按本仓实际开发习惯补充自动路由与防踩坑规则；具体剧本与 24 条原则仍以 `poteto-mode` 为准，子 Agent 提示词首行固定注入 [`worker.md`](../poteto-mode/worker.md)。

## 1. 口语直达技能（用户不背命令）

用户只需描述目标、现象或贴截图，由主 Agent 自动选择对应技能：

- 需求模糊或想深化提示词，调 [`reframe`](../reframe/SKILL.md) 或 [`sharpen`](../sharpen/SKILL.md)；追历史原因或阈值，调 [`why`](../why/SKILL.md)。
- 跨模块改设计或争议方案，调 [`architect`](../architect/SKILL.md) / [`arena`](../arena/SKILL.md)；并行多波次改码，直接按 [`swarm`](../swarm/SKILL.md) 拆路推进。
- 大改动（超 10 个文件或跨多域）提交前自动跑 [`open-code-review-delegate`](../open-code-review-delegate/SKILL.md)、[`deslop`](../deslop/SKILL.md) 与 [`no-comments`](../no-comments/SKILL.md)；UI 改动走 [`verify`](../verify/SKILL.md)，控制与 MQTT 状态机走 [`command-safety`](../command-safety/SKILL.md)，边缘网关走 [`eg8200-linux-access`](../eg8200-linux-access/SKILL.md)。
- 讲求投入产出比，不做过度防御性封装和无意义单测；同一类 CI 或运行问题复发两次，直接走 [`correct`](../correct/SKILL.md) 固化为检查脚本。

## 2. 多会话并行防撞与连续推进

本仓常态同时运行 2 到 7 个 Antigravity / Claude Code 会话：

- 动代码前先查主工作区 `git status` 与当前分支；只要主工作区脏或不在 `main`，直接切到独立兄弟 worktree 或 `Workspace: "share"` 作业，绝不覆盖主工作区，合并后清干净临时 worktree。
- 开 PR、改热文件或触发生产发布前，先查在途 PR、活跃分支与 GitHub Actions `Deploy production` 队列，避免互相取消部署或撞车同一 PR。
- 一旦用户批准整段多 PR 计划或波次（如 `"好 你做吧"`、`"全做了"`、`"按推荐"`），连续跑完并合入该批全部 PR，绝不每合并 1 个 PR 就停下来问要不要继续，也不得静默砍掉已批计划里的子项。

## 3. 全栈接通自查与三层收尾菜单

宣布完成前先自检三件事，避免半成品回交：

- **真接通**：新增模块必须接到 `cmd/` 真实主链路与配置中，不能只停留在单元测试通过。
- **全栈联动**：后端改完同步核查 `apps/hvac-web`（以及涉及值班端时的 `apps/hvac-miniprogram`）是否需要配套界面；未改前端时显式说明原因。
- **上线即实测**：修复线上问题后，不能只看单条日志或让用户刷新重试，必须走真实页面链路或 Chromium 实测确认。

每个里程碑收尾固定按三层汇报，并附简短编号待选单，方便用户用几个字拍板下一波：

1. **已合入并验证**：PR 号、提交与实测结果。
2. **生产与现场状态**：明确区分「仅仓库已合并 / 已部署 `139.199.58.47` 并实测 / 现场 EG8200 真机已核验」。
3. **剩余与待拍板**：尚未接通的收尾项、可选优化、需建 Issue 的远期项，以及需用户授权的生产发布或真机操作。

## 4. 零手工甩锅与 CLI / API 优先

- 凡本机已登录 CLI（`tccli`、`cf`、`gh`、`ssh`）或持久 MCP（`agent-browser`）能直接办完的云配置、密钥、构建与核验，直接代劳完成，不生成手跑向导或命令清单甩给用户。
- 只在必须本人扫码、输密码、切登录账号，或触及生产发布、删改存量数据、现场真机下发授权时停下。
- 操作边缘网关或旧 Node-RED 时，只走回环隧道与 Admin HTTP API 脚本，严禁在浏览器界面点击或拖拽线上流程节点。
- Windows 或 WSL 调 `ssh` 一律带 `-n -o BatchMode=yes -o ConnectTimeout=10 -o ServerAliveInterval=5 -o ServerAliveCountMax=2`，多行远端脚本走文件或 stdin 传入，不在 PowerShell 双引号里硬拼复杂转义。

## 5. 第一性原理对标与前端先原型

- 做架构盘点、选型或根因排查时，先从第一性原理拆清真实单机/现场约束，再对标外部项目；引用 GitHub 开源参考前先核查实时星标与最近提交时间，剔除已停更项目。
- 涉及页面整体重做或新增复杂视图，先出可评审原型（Artifact 或 Figma），用户确认后再动 `apps/hvac-web` 生产代码。
- 界面文案用信息层级、图表和状态代替解释性说明文字，面向非技术运维人员，不出现生硬 AI 味表达。

## 6. 全端强制中文与双源盘点

- 无论在 Antigravity 还是 Claude Code，即使刚读完英文工具输出、经历上下文压缩或用户点了英文快捷按钮，所有面向用户的回复一律使用简体中文，并过 [`unslop`](../unslop/SKILL.md) 去套话。
- 用户问 `"下面做什么"` 或 `"还有没完成的内容吗"` 时，必须同时核对三处来源：当前对话已定的规划路线图、GitHub Issues、以及现网运行与未发布提交状态，不能只列 `gh issue list`。
