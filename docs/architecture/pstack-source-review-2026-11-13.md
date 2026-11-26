# pstack 工程工作流调研与全量扩展记录（2026-11-13 ~ 2026-11-26）

## 看了什么

团队人手和 Agent 变多后，仅靠在 `AGENTS.md` 与 `docs/HANDOFF.md` 里追加「注意不要……」「记得同步修改……」很容易失效，且手工双写清单已经发生漏同步。为此完整比对并审阅了：

- `cursor/plugins/pstack` `0.15.48`（提交 `9e278fe4`，仓库快照 `b1fbf4b60ac6813b4583ccc356850dafcf492665`，MIT 协议）以及前期引入的 `michael-denyer/pstack-claude` `0.9.138`（提交 `2044f1810afc98c3aa80100587dd990c83cf6ea8`）；
- 上游最新版本中全部可用技能、22 套 `poteto-mode` 剧本、24 条 `principle-*` 细则文档、`worker.md` 子 Agent 底线规则、Claude Code 适配说明，以及零依赖运行版 `statechart/scripts/`、`resume`、`worktree-audit`、`log.sh` 与 Antigravity 工具映射。

## 决定

**在 `.agents/skills/` 与 `.claude/skills/` 双目录逐字节 1:1 镜像维护全套 `pstack` 可用技能与项目技能（共 55 个技能：44 个 `pstack` 技能含 `reframe` 与 22 套剧本 + 5 个 `mattpocock/skills` + 3 个项目验证/运维技能 + 2 个前端规范技能 + 1 个代码审查脚手架）；门禁检查与安全钩子继续由仓库 `scripts/` TypeScript 承接，并严格守住 `.agents/` 零 `.js`/`.mjs` 与零 `.audit/` 上限。**

- **采纳的**：
  - **`poteto-mode` 总控、22 套剧本与 `worker.md`**：将 24 条核心原则内联进 [`poteto-mode/SKILL.md`](../../.agents/skills/poteto-mode/SKILL.md)（控制在截断阈值 `24,000` 字节内），把上游 24 个 `principle-*` 细则收入 `poteto-mode/references/principles/*.md` 按需读取，新增 [`poteto-mode/worker.md`](../../.agents/skills/poteto-mode/worker.md) 作为所有子 Agent 提示词第一行读入的共享行为底线，并配置全部 22 套剧本及 4 份参考文档（`agy-tools`、`bugbot-triage`、`merge-safety`、`resume-storage`）。
  - **补齐 `reframe` 并同步 `#1236`/`#1249` 精简与修复**：新增 `reframe`（用专业术语和相邻领域做法重写模糊需求），将 `sharpen` 更新为可实测分叉优先出原型再给推荐；按上游 `#1236`/`#1249` 将 `why`、`how`、`reflect`、`architect` 精简为单文件（删除各自的 `references/` 多代理模板）、精简 `unslop`、`technical-writing` 及 `opening-a-pr`/`babysit`/`multi-phase-plan` 剧本，移除冗余的 `orchestrate.md` 与 `check-plan.ts`，取消对 `how` 的强制自动前置路由。
  - **零 `.js`/`.mjs`、零第三方运行依赖辅助脚本与 Windows 兼容**：
    - `statechart/scripts/` 选用 Node 24+ 原生可运行的零依赖 TypeScript 版本（去除 `test/` 中的 `.mjs` 与 `fast-check` 测试依赖），同步上游 SANY 行列号报错修复、Mermaid 父状态边框箭头修复，以及 `#1292` 原生 Windows 兼容（新增 `janitor.ts`/`kill.ts`、兼容 TLC `\r\n` 换行与 `windowsHide`）；本机 WSL 仓库外已通过登录壳 `bash -lc` 预装 `mise`（`~/.local/bin/mise`）、`java@temurin-25`、`http:tla2tools@1.7.4` 与 `pele@1.9.0`（`/home/haozhang/.local/share/pele`），`ketchup-video` 已安装静态 `ffmpeg`/`ffprobe`（`~/.local/bin/`）并复用 Playwright Chromium（`~/.cache/ms-playwright/`）。
    - `resume.ts`、`worktree-audit.ts` 由上游 `.mjs` 改写为纯标准库 TypeScript（通过 WSL 登录壳 `wsl -e bash -lc "node --disable-warning=ExperimentalWarning ..."` 直接执行），其中 `worktree-audit.ts` 扫描 Antigravity（`antigravity`/`antigravity-cli`/`antigravity-ide` 的 `brain` 目录，兼容 `file:///<drive>%3A/` 编码轨迹 URI）与 Claude Code 会话历史，并将 Windows worktree 路径映射为 `/mnt/<drive>/...` 且禁止在 WSL 误判为 `prunable`；`show-me-your-work/scripts/log.sh` 保留纯 POSIX bash。
- **改造的**：
  - **严格兼容 Antigravity、Claude Code 与仓库静态门禁**：把所有上游 `.audit/` 路径统一迁至 `.scratch/`；所有 `.cursor/` / `/loop` / `AskUserQuestion` / `create-skill` / `Origin` 引用适配为 Antigravity `transcript.jsonl`、`schedule`、`ask_question`、`agy-customizations` 与原生 `gh`/`git`；[`agy-tools.md`](../../.agents/skills/poteto-mode/references/agy-tools.md) 同时补齐 Claude Code 适配对照（`Agent`、`Explore`、`isolation: "worktree"` 与 `~/.claude/rules/pstack-models.md`）；同步保留 Windows + WSL 双侧运行时约定（`Workspace: "share"` 隔离 Windows worktree 与 `.scratch/` 不遗传规则、WSL 禁止对含 Windows worktree 的仓库执行 `git worktree prune`）。
  - **模型覆盖与 PR 合入边界清晰化**：默认仍遵守 `AGENTS.md`（未配 `pstack-models.md` 时 Antigravity 一律传 `Model: "inherit"`；`setup-pstack` 仅按用户明确配置写入 `~/.gemini/config/pstack-models.md` 或 Claude Code 自动加载的 `~/.claude/rules/pstack-models.md` / `.claude/rules/pstack-models.md`，已清理旧 `how`/`why`/`reflect`/`architect` 角色行并支持 `large/medium/small` 三档预算）；单 PR 交付继续按 `opening-a-pr.md` 自动跟进 CI 并 squash 合并，而 `babysit`/`autopilot-stack` 等堆叠场景停止在 merge-ready，明确交由 `shipping` 或人工确认后合并。
  - **领域边界提示**：控制指令与 MQTT 关联状态机继续以 [`command-safety`](../../.agents/skills/command-safety/SKILL.md)（`npm run command:formal`）为权威形式化验证，`statechart` 用于临时/通用状态流 TLA+ 建模与可视化。
- **拒绝的（不可用或强绑定 Cursor 项）**：
  - **`make-bot-ui` 与 `automations/benny/`**：强依赖 Cursor 云端 Automations、Webhook 与 `update_state`/`secret-request` 等专有云接口，无法在 Antigravity / Claude Code 本地环境运行。
  - **24 个顶层 `principle-*` 技能目录**：为避免污染顶层斜杠技能菜单，全部收口到 `poteto-mode/references/principles/*.md` 并由 `poteto-mode/SKILL.md` 索引链接，不单列为顶层技能目录。
  - **上游 `SessionStart` 启动钩子（`session-start.sh`）与 Bun 版 `watch-pr` / `worktree-audit.sh`**：前者与 `AGENTS.md` / Windows 冲突；后者强依赖 `bun` + `commander`，改以 `gh pr view`/`gh pr checks`/`gh pr merge --match-head-commit` 原生命令及 `worktree-audit.ts` 直接实现。
