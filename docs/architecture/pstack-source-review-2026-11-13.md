# pstack 工程工作流调研（2026-11-13，2026-11-14 全量切换）

## 看了什么

团队人手和 Agent 变多后，仅靠在 `AGENTS.md` 与 `docs/HANDOFF.md` 里追加「注意不要……」「记得同步修改……」很容易失效：单份交接文档膨胀到接近工具单次读取上限（48,434 字节对比 46,080 字节阈值），而 `capability-labels.ts` 等手工双写清单也已经发生实际漏同步。为此调研了：

- `michael-denyer/pstack-claude` `0.9.138`（提交 `2044f1810afc98c3aa80100587dd990c83cf6ea8`，MIT 协议），及其 `tools/upstream.json` 固定的三个上游来源：`cursor/plugins` 的 `pstack`（`c742dd61706fbacc1b95f1a20b7b988cf096ce6c`）、`cursor-team-kit`（`e46364b8be46000b7df0f260550cd712afbb8d36`）和 `thermos`（`118e73b0e196e4d49d5aade7ce6dd98985a621f68`）；
- 完整审阅了 `poteto-mode`（含 `references/agy-tools.md`、`references/bugbot-triage.md` 及核心剧本 `feature`、`bug-fix`、`refactoring`、`investigation`、`opening-a-pr`）、`figure-it-out`、`architect`、`blast-radius`、`swarm`、`arena`、`tdd`、`interrogate`、`in-your-own-words`、`thermo-nuclear-code-quality-review`、`no-comments`（及 `plugins/pstack/agents/comment-sicko.md`）、`typescript-best-practices`、`correct`、`deslop`、`unslop`、`how`、`why`、`create-verification-skill`、`maintain-verification-skill`、`create-formal-verification-skill`、`maintain-formal-verification-skill`、`make-pr-easy-to-review`、`babysit`、`fix-ci`、`fix-merge-conflicts`、`get-pr-comments`，以及 Antigravity 插件包装与钩子脚本。

## 决定

**全面淘汰 15 个 `mattpocock/skills`（清理重名与方法论分歧），采用裁剪本地化后的 26 个纯 Markdown `pstack` 技能 + 2 个项目验证技能（`verify`、`command-safety`）+ 3 个 UI 技能（`frontend-design`、`shadcn`、`web-design-guidelines`），共 31 个技能在双目录 1:1 镜像；门禁检查与安全钩子继续由 TypeScript 承接，不安装全量 65 技能包及 `.mjs` 脚本。**

- **采纳的**：
  - **精简版 `poteto-mode` 总控 + 5 套核心剧本**：原版 `poteto-mode/SKILL.md` 为 `29,027` 字节，会超过 Antigravity 原生技能加载器 `24,000` 字节截断阈值。本次去掉 10 余套无关平台映射表与重型长跑/云端剧本（`orchestrate`、`autopilot-*`、`runtime-forensics` 等）和 23 个零散 `principle-*` 外链目录，把 24 条核心原则内联进小于 10 KB 的 `poteto-mode/SKILL.md`，只保留 `feature`、`bug-fix`、`refactoring`、`investigation`、`opening-a-pr` 5 套剧本及 `references/agy-tools.md`、`references/bugbot-triage.md`。
  - **规划、协作、去噪与 PR 叶子技能组**：补齐 `figure-it-out`、`tdd`（`pstack` 轻量回归先红后绿版）、`swarm`、`arena`、`interrogate`、`in-your-own-words`、`thermo-nuclear-code-quality-review`、`no-comments`（将上游 `agents/comment-sicko.md` 收进 `references/comment-sicko-prompt.md`）、`make-pr-easy-to-review`、`babysit`、`fix-ci`、`fix-merge-conflicts`、`get-pr-comments`，以及两项验证维护循环技能 `maintain-verification-skill`、`maintain-formal-verification-skill`，与早先落地的 `architect`、`blast-radius`、`typescript-best-practices`、`correct`、`deslop`、`unslop`、`how`、`why`、`create-verification-skill`、`create-formal-verification-skill` 构成完整闭环。
  - **验证与工程安全底座**：沿用已落地的 `verify`（Playwright 1440×900 录制网关与实库验证）、`command-safety`（`npm run command:formal` 状态机穷举检验）、跨平台 `PreToolUse` 安全钩子（`scripts/hooks/pre-tool-guard.ts`），以及 `scripts/check-repository-governance.ts` 强制校对 `.agents/skills/` 与 `.claude/skills/` 31 个技能逐字节 1:1 镜像和 `<= 102` 遗留 JS 上限。
- **改造的**：
  - **全量替换 `mattpocock/skills`**：移除原 14 个 `mattpocock` 技能目录、原地替换 `tdd`（共淘汰 15 个 `mattpocock` 技能）并删除 `MATTPOCOCK-LICENSE.txt`（`skills-lock.json` 仅保留 `shadcn`），消除双套 `tdd`、`pr`/`make-pr-easy-to-review`、`diagnosing-bugs`/`bug-fix`、`code-review`/`interrogate` 重名与规则冲突；同步把 `architect` 里的 `grilling` 改为 `interrogate`，把术语表与 ADR 格式摘要直接沉淀进 `docs/agents/domain.md`，重写 `docs/agents/issue-delivery.md`、`docs/agents/issue-tracker.md`、`docs/agents/triage-labels.md` 与 `AGENTS.md` 引用。
  - **清理跨工具链死链与不兼容规则**：去掉 Codex/Copilot/Muse/Pi 映射、`Origin` CLI 分支、`/setup-pstack`/`pstack-models.md` 外部模型覆盖表（统一 `invoke_subagent` 默认 `Model: "inherit"`，除非用户显式指定）、上游 `poteto-mode/scripts/*`（避免 `.agents/` 新增 JS 与 `.audit/` 违禁文件，本地临时检查单改为 `.scratch/todo.md`）以及全部不存在的 `principle-*` 技能链接。
  - **PR 交付与自动合入以 `AGENTS.md` 为准**：重写 `poteto-mode/playbooks/opening-a-pr.md` 与 `babysit/SKILL.md`，保留上游结构化 PR 说明，取消「开 PR 后不盯 CI、不自动合并」限制，改为推分支（Windows `git push`）与开 PR（WSL `gh`）后立即用 `gh pr checks --watch` 跟进直到 CI 全绿并自动执行 `gh pr merge --squash --delete-branch`；仅生产部署、数据销毁与外部消息须先请示。
- **拒绝的**：
  - **`SessionStart` 启动钩子（`hooks.json` + `session-start.sh`）**：上游在 Antigravity 清单写死执行 `./hooks/session-start.sh agy`，Windows 下会通过 `cmd /c` 失败，且每轮注入大段英文总控提示会与 `AGENTS.md` 重叠。
  - **无关平台工具、云端工作流与通用语料脚本**：23 个独立 `principle-*` 技能目录、9 份非本工具链平台映射表、`pstack-claude` 自带 `.mjs` 脚本（`watch-pr`、`worktree-audit`、`check-playbooks`）、`setup-pstack` / `grainulator` / `orchestrate` / `autopilot-*`、`why` 中的 6 份 SaaS 外部手册，以及 `tla-plus`/`statechart` 重语料（具体并发不变量直接由 `command-safety` 用 Go 校验）。
