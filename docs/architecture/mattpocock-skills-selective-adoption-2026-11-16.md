# `mattpocock/skills` 选择性补齐与防冲突适配记录（2026-11-16）

## 看了什么

以 `mattpocock/skills`（`v1.3.2` 后提交 `5863806`，MIT 协议）全部 39 个技能为基准，逐项比对仓库现有 `pstack` 48 个技能体系（见 [pstack 调研记录](pstack-source-review-2026-11-13.md)）与本机已加载技能，筛出真正补缺且不与 `poteto-mode` 主流程、静态门禁（`npm run repo:check`）冲突的部分。

## 决定

**在仓库 `.agents/skills/` 与 `.claude/skills/` 双目录新增 4 个独立技能（仓库技能总数由 48 增至 52），并将 3 项上下文工程与排查辅助内容并入 `poteto-mode` / `poteto-help`；其余易与 `pstack` 重名或产生双套开发流的技能拒绝进仓（个人向 3 项迁入用户全局 `~/.gemini/config/skills/`）。**

- **完整引入仓库的 4 个技能**：
  - `wizard`（含 `template.sh`）：补充人工点击第三方后台、配凭证与 CI Secrets 的交互向导生成能力；限定临时脚本写进 `.scratch/`，严禁读取或覆盖 [pre-tool-guard.ts](../../scripts/hooks/pre-tool-guard.ts) 保护的运行密钥路径。
  - `to-questionnaire`（手动触发）：面向现场暖通工程师、客户运维等外部干系人生成异步/会议确认问卷，默认写入 `.scratch/`。
  - `grilling`：保留设计树前沿（frontier）逐轮拷问与 `research` 后台核查机制，并在描述中限定仅在显式请求拷问或由 `improve-codebase-architecture` 调用时触发，不打断 `poteto-mode` 的 `Never Block on the Human` 常规执行；同步关联根目录 `GLOSSARY.md`、`docs/agents/domain.md` 与 `docs/adr/`。
  - `improve-codebase-architecture`（手动触发，含 `HTML-REPORT.md` 与自包含 `references/deep-module-lens.md`）：按近期 `git log` 热点和 `deletion test` 扫描浅模块，生成 Tailwind/Mermaid/SVG `Before/After` 对比报告至 `.scratch/architecture-review-<timestamp>.html`；选定候选后经 `grilling` 收敛，再交棒 `architect`（`Design it twice`）与 `poteto-mode/playbooks/refactoring.md` 执行。

- **并入现有 `pstack` 文件的 3 项能力**：
  - 将 `writing-for-agents`（Two Loads、渐进披露、完成条件防提前收尾、Leading Words 胜于否定禁令、环境即缓存剪枝与 No-op 测试）并入 [authoring-a-skill.md](../../.agents/skills/poteto-mode/playbooks/authoring-a-skill.md)。
  - 将 `ask-matt/PHASE-BOUNDARIES.md`（`Continue` -> `/clear` -> `pause-safely` / `handoff` -> `Subagent` -> `/compact` 五选一决策树）并入 [poteto-help/SKILL.md](../../.agents/skills/poteto-help/SKILL.md)，并同步更新技能列表至 52 个。
  - 将 `diagnosing-bugs/scripts/hitl-loop.template.sh` 迁入 `.agents/skills/poteto-mode/scripts/hitl-loop.template.sh`，作为无头浏览器与 CLI 均无法触达现场硬件时的最后兜底工具挂在 [bug-fix.md](../../.agents/skills/poteto-mode/playbooks/bug-fix.md) 第一步。

- **放进个人全局、不进仓库的 3 项**：
  - `loop-me`、`handoff`（跨工具/跨目录便携交接，仓内断点仍走 `pause-safely` + `resume.ts`）与重命名为 `interactive-tutor` 的教学工作区技能（原名 `teach` 会与仓库 `pstack` 代码讲解员 `teach` 冲突），装于 `~/.gemini/config/skills/`。

- **拒绝引入仓库的**：
  - `tdd`、`implement`、`implement-spec`、`codebase-design`、`code-review`、`pr`、`retro`、`prototype`、`ask-matt`、`chief-of-staff`、`claude-handoff`、`software-factory`、`setup-ts-deep-modules`（`.cjs` 违反仓库 JS 门禁）、`setup-matt-pocock-skills`、`research`、`wait-what` 及冻结的 `misc/*`。
