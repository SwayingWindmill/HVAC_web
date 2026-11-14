# 第三方与项目 skill

本目录下包含以下来源的 skill（全部 **48 个技能**在 `.agents/skills/` 与 `.claude/skills/` 保持逐字节 1:1 镜像，由 `npm run repo:check` 自动校对）：

1. [michael-denyer/pstack-claude](https://github.com/michael-denyer/pstack-claude) 与 [cursor/plugins `pstack`](https://github.com/cursor/plugins/tree/main/pstack)（MIT 协议，基于 Lauren Tan 的 `pstack` 与 `cursor-team-kit`）：版本 `0.9.138`，提交固定在 `2044f1810afc98c3aa80100587dd990c83cf6ea8`，授权文件见 `.agents/skills/PSTACK-LICENSE.txt`，筛选与本地化说明见 `docs/architecture/pstack-source-review-2026-11-13.md`。除依赖 Cursor 云端 Webhook 的 `make-bot-ui` 与已内联进 `poteto-mode` 的 23 个独立 `principle-*` 目录外，已安装全部 **43 个 `pstack` 技能**：
   - **总控与规划（5 个）**：`poteto-mode`（`SKILL.md` 控制在截断上限 `24,000` 字节内，配 `references/{agy-tools,bugbot-triage,merge-safety,resume-storage}.md`、全量 **23 套剧本**与零第三方依赖 `.ts` 脚本 `scripts/{check-plan,resume,worktree-audit}.ts`）、`figure-it-out`、`architect`、`blast-radius`、`setup-pstack`
   - **实现、性能与状态建模（7 个）**：`tdd`、`swarm`、`arena`、`typescript-best-practices`、`correct`、`benchmark-checklist`、`statechart`（零 npm 运行依赖 `scripts/statechart`，需在 WSL 登录壳 `bash -lc` 下运行；本机 WSL 已在仓库外预装 `mise`、`java@temurin-25`、`http:tla2tools@1.7.4` 与 `pele@1.9.0`）
   - **理解、提问、汇总与写作（13 个）**：`how`、`why`、`teach`、`in-your-own-words`、`sharpen`、`recall`、`what-did-i-get-done`、`ketchup`、`ketchup-video`（本机 WSL 已安装静态 `ffmpeg`/`ffprobe` 并复用 Playwright Chromium）、`bro`、`deslop`、`unslop`、`technical-writing`
   - **对抗审查、决策审计与自进化（8 个）**：`interrogate`、`thermo-nuclear-review`、`thermo-nuclear-code-quality-review`、`thermos`、`no-comments`（含 `references/comment-sicko-prompt.md`）、`show-me-your-work`（含 `scripts/log.sh` 与 TSV 模板，日志写入 `.scratch/`）、`reflect`、`automate-me`
   - **验证工程化维护（4 个）**：`create-verification-skill`、`maintain-verification-skill`、`create-formal-verification-skill`、`maintain-formal-verification-skill`
   - **GitHub PR 协作与导航（6 个）**：`make-pr-easy-to-review`、`babysit`、`fix-ci`、`fix-merge-conflicts`、`get-pr-comments`、`poteto-help`
2. 项目专属验证与形式化验证技能（2 个）：按 `create-verification-skill` / `create-formal-verification-skill` 为 `HVAC_web` 生成的 `verify`（1440px Playwright 录制网关与实库验证）与 `command-safety`（`modules/command/pkg/commandformal` 状态机穷举检验）。
3. 项目前端与界面规范技能（3 个）：`frontend-design`、`shadcn`、`web-design-guidelines`。
