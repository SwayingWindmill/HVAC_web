# 第三方与项目 skill

本目录下包含以下来源的 skill（全部 **29 个技能**在 `.agents/skills/` 与 `.claude/skills/` 保持逐字节 1:1 镜像，由 `npm run repo:check` 自动校对）：

1. [mattpocock/skills](https://github.com/mattpocock/skills)（MIT 协议）：版本固定在提交 `49dd158d1076134a641b33efb035946536778336`（插件 `mattpocock-skills` 1.3.1），授权文件见 `.agents/skills/MATTPOCOCK-LICENSE.txt`，仅保留适用于本仓库的 15 个技能（`code-review`、`codebase-design`、`diagnosing-bugs`、`domain-modeling`、`grilling`、`pr`、`prototype`、`research`、`tdd`、`to-spec`、`to-tickets`、`triage`、`wayfinder`、`wizard`、`writing-for-agents`），并已去掉 Codex 专用的 `agents/openai.yaml` 及指向已裁撤安装脚本的过时引文。
2. [michael-denyer/pstack-claude](https://github.com/michael-denyer/pstack-claude)（MIT 协议，基于 Lauren Tan 的 `pstack` 与 `cursor-team-kit`）：版本 `0.9.138`，提交固定在 `2044f1810afc98c3aa80100587dd990c83cf6ea8`，授权文件见 `.agents/skills/PSTACK-LICENSE.txt`，筛选与改造说明见 `docs/architecture/pstack-source-review-2026-11-13.md`。引入并双目录同步的 10 个技能为：`architect`、`blast-radius`、`correct`、`create-formal-verification-skill`、`create-verification-skill`、`deslop`、`how`、`typescript-best-practices`、`unslop`、`why`（均已清理跨插件断链、未使用 SaaS 清单及外部模型覆写表引用）。
3. 项目自建 `verify` 技能：按 `create-verification-skill` 为 `HVAC_web` 编写并实跑验证的浏览器取证技能，覆盖录制网关与本地 `hvac-local` 平台。
4. 项目前端与界面规范技能（3 个）：`frontend-design`、`shadcn`、`web-design-guidelines`。
