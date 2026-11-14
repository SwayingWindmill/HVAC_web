# pstack 工程工作流调研与全量扩展记录（2026-11-13 ~ 2026-11-14）

## 看了什么

团队人手和 Agent 变多后，仅靠在 `AGENTS.md` 与 `docs/HANDOFF.md` 里追加「注意不要……」「记得同步修改……」很容易失效，且手工双写清单已经发生漏同步。为此完整比对并审阅了：

- `michael-denyer/pstack-claude` `0.9.138`（提交 `2044f1810afc98c3aa80100587dd990c83cf6ea8`，MIT 协议）及其 `tools/upstream.json` 固定的三大来源（`cursor/plugins` `pstack`、`cursor-team-kit`、`thermos`）；
- 上游最新版本中全部可用技能、23 套 `poteto-mode` 剧本，以及零依赖运行版 `statechart/scripts/`、`check-plan`、`resume`、`worktree-audit`、`log.sh` 与 Antigravity 工具映射。

## 决定

**全面淘汰 `mattpocock/skills` 后，在 `.agents/skills/` 与 `.claude/skills/` 双目录逐字节 1:1 镜像安装全套 `pstack` 可用技能与剧本：43 个 `pstack` 技能（含全量 23 套剧本）+ 2 个项目验证技能（`verify`、`command-safety`）+ 3 个 UI 技能，共 48 个技能；门禁检查与安全钩子继续由仓库 `scripts/` TypeScript 承接，并严格守住 `.agents/` 零 `.js`/`.mjs` 与零 `.audit/` 上限。**

- **采纳的**：
  - **`poteto-mode` 总控与全量 23 套剧本**：将 24 条核心原则内联进 `poteto-mode/SKILL.md`（控制在截断阈值 `24,000` 字节内），配置全套 23 套剧本（核心开发 5 套、`prototype`、`visual-parity`、`perf-issue`、`hillclimb`、`runtime-forensics`、`trace-forensics`、`multi-phase-plan`、`autonomous-run`、`orchestrate`、`autopilot-full`、`autopilot-stack`、`babysit`、`shipping`、`session-pickup`、`pause-safely`、`worktree-cleanup`、`authoring-a-skill`、`eval`）及 4 份参考文档（`agy-tools`、`bugbot-triage`、`merge-safety`、`resume-storage`）。
  - **补齐全部 17 个可用拓展技能**：在首批 26 个基础上补齐 `automate-me`、`benchmark-checklist`、`bro`、`ketchup`、`ketchup-video`、`poteto-help`、`recall`、`reflect`、`setup-pstack`、`sharpen`、`show-me-your-work`、`statechart`、`teach`、`technical-writing`、`thermo-nuclear-review`、`thermos`、`what-did-i-get-done`。
  - **零 `.js`/`.mjs`、零第三方运行依赖辅助脚本**：
    - `statechart/scripts/` 直接选用 Node 24+ 原生可运行的零依赖 TypeScript 版本（去除 `test/` 中的 `.mjs` 与 `fast-check` 测试依赖）；本机 WSL 仓库外已通过登录壳 `bash -lc` 预装 `mise`（`~/.local/bin/mise`）、`java@temurin-25`、`http:tla2tools@1.7.4` 与 `pele@1.9.0`（`/home/haozhang/.local/share/pele`），`ketchup-video` 已安装静态 `ffmpeg`/`ffprobe`（`~/.local/bin/`）并复用 Playwright Chromium（`~/.cache/ms-playwright/`）。
    - `check-plan.ts`、`resume.ts`、`worktree-audit.ts` 由上游 `.mjs` 改写为纯标准库 TypeScript（通过 WSL 登录壳 `wsl -e bash -lc "node --disable-warning=ExperimentalWarning ..."` 直接执行），其中 `worktree-audit.ts` 扫描 Antigravity（`antigravity`/`antigravity-cli`/`antigravity-ide` 的 `brain` 目录，兼容 `file:///<drive>%3A/` 编码轨迹 URI）与 Claude Code 会话历史，并将 Windows worktree 路径映射为 `/mnt/<drive>/...` 且禁止在 WSL 误判为 `prunable`；`show-me-your-work/scripts/log.sh` 保留纯 POSIX bash。
- **改造的**：
  - **严格兼容 Antigravity 与仓库静态门禁**：把所有上游 `.audit/` 路径统一迁至 `.scratch/`；所有 `.cursor/` / `/loop` / `AskUserQuestion` / `create-skill` / `Origin` 引用适配为 Antigravity `transcript.jsonl`、`schedule`、`ask_question`、`agy-customizations` 与原生 `gh`/`git`；同步完成 Windows + WSL 双侧运行时适配（`~/.gemini/config/GEMINI.md`、`~/.gemini/config/skills/`、`~/.gemini/config/mcp_config.json` 与 `mcp_<server>_<tool>` / `call_mcp_tool`、`Workspace: "share"` 隔离 Windows worktree 与 `.scratch/` 不遗传规则、`shadcn` 经 `npm run ui:shadcn` 执行与 `:root[data-theme="light"]`/`.dark` 约定、`opening-a-pr.md` 用 `read_file` 触发技能而非斜杠命令，以及 WSL 禁止对含 Windows worktree 的仓库执行 `git worktree prune`）。
  - **模型覆盖与 PR 合入边界清晰化**：默认仍遵守 `AGENTS.md`（未配 `pstack-models.md` 时 Antigravity 一律传 `Model: "inherit"`；`setup-pstack` 仅按用户明确配置写入 `~/.gemini/config/pstack-models.md` 或 `.claude/pstack-models.md`）；单 PR 交付继续按 `opening-a-pr.md` 自动跟进 CI 并 squash 合并，而 `babysit`/`autopilot-stack` 等堆叠场景停止在 merge-ready，明确交由 `shipping` 或人工确认后合并。
  - **领域边界提示**：控制指令与 MQTT 关联状态机继续以 [`command-safety`](../../.agents/skills/command-safety/SKILL.md)（`npm run command:formal`）为权威形式化验证，`statechart` 用于临时/通用状态流 TLA+ 建模与可视化。
- **拒绝的（唯一不可用技能及运行时不可移植项）**：
  - **`make-bot-ui`**：强依赖 Cursor 云端 Webhook 与 `update_state`/`secret-request` 等专有云接口，无法在 Antigravity / Claude Code 本地环境运行。
  - **23 个独立 `principle-*` 外链目录**：原则条文已完整内联在 `poteto-mode/SKILL.md`，不再拆出 23 个单规则目录。
  - **上游 `SessionStart` 启动钩子（`session-start.sh`）与 Bun 版 `watch-pr`/`ship-pr`**：前者与 `AGENTS.md` / Windows 冲突；后者强依赖 `bun` + `commander`，改以 `gh pr view`/`gh pr checks`/`gh pr merge --match-head-commit` 原生命令在 `babysit`/`shipping`/`merge-safety` 中直接实现。
