# 第三方与项目 skill

本目录下包含以下来源的 skill（全部 **31 个技能**在 `.agents/skills/` 与 `.claude/skills/` 保持逐字节 1:1 镜像，由 `npm run repo:check` 自动校对）：

1. [michael-denyer/pstack-claude](https://github.com/michael-denyer/pstack-claude)（MIT 协议，基于 Lauren Tan 的 `pstack` 与 `cursor-team-kit`）：版本 `0.9.138`，提交固定在 `2044f1810afc98c3aa80100587dd990c83cf6ea8`，授权文件见 `.agents/skills/PSTACK-LICENSE.txt`，筛选、裁剪与本地化说明见 `docs/architecture/pstack-source-review-2026-11-13.md`。仓库已全部采用 `pstack` 工作流并清理 `mattpocock/skills`（`skills-lock.json` 仅保留 `shadcn`），双目录同步 **26 个 pstack 技能**：
   - **总控与规划（4 个）**：`poteto-mode`（`SKILL.md` 精简至 10 KB 内，配 `references/agy-tools.md`、`references/bugbot-triage.md` 及 `feature`/`bug-fix`/`refactoring`/`investigation`/`opening-a-pr` 5 套剧本）、`figure-it-out`、`architect`、`blast-radius`
   - **实现与多代理协作（5 个）**：`tdd`、`swarm`、`arena`、`typescript-best-practices`、`correct`
   - **理解、去噪与审查（8 个）**：`how`、`why`、`in-your-own-words`、`interrogate`、`thermo-nuclear-code-quality-review`、`no-comments`（含 `references/comment-sicko-prompt.md`）、`deslop`、`unslop`
   - **验证工程化维护（4 个）**：`create-verification-skill`、`maintain-verification-skill`、`create-formal-verification-skill`、`maintain-formal-verification-skill`
   - **GitHub PR 协作（5 个）**：`make-pr-easy-to-review`、`babysit`、`fix-ci`、`fix-merge-conflicts`、`get-pr-comments`
2. 项目专属验证与形式化验证技能（2 个）：按 `create-verification-skill` / `create-formal-verification-skill` 为 `HVAC_web` 生成的 `verify`（1440px Playwright 录制网关与实库验证）与 `command-safety`（`modules/command/pkg/commandformal` 状态机穷举检验）。
3. 项目前端与界面规范技能（3 个）：`frontend-design`、`shadcn`、`web-design-guidelines`。
