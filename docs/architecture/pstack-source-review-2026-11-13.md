# pstack 工程工作流调研（2026-11-13）

## 看了什么

团队人手和 Agent 变多后，仅靠在 `AGENTS.md` 与 `docs/HANDOFF.md` 里追加「注意不要……」「记得同步修改……」很容易失效：单份交接文档膨胀到接近工具单次读取上限（48,434 字节对比 46,080 字节阈值），而 `capability-labels.ts` 等手工双写清单也已经发生实际漏同步。为此调研了：

- `michael-denyer/pstack-claude` `0.9.138`（提交 `2044f1810afc98c3aa80100587dd990c83cf6ea8`，MIT 协议），及其 `tools/upstream.json` 固定的三个上游来源：`cursor/plugins` 的 `pstack`（`c742dd61706fbacc1b95f1a20b7b988cf096ce6c`）、`cursor-team-kit`（`e46364b8be46000b7df0f260550cd712afbb8d36`）和 `thermos`（`118e73b0e196e4d49d5aade7ce6dd98985a621f68`）；
- 重点审阅了 `poteto-mode`（含 `references/agy-tools.md`、`playbooks/bug-fix.md`、`playbooks/opening-a-pr.md`）、`architect`、`blast-radius`、`typescript-best-practices`、`correct`、`deslop`、`unslop`、`how`、`why`、`create-verification-skill`、`create-formal-verification-skill`，以及 Antigravity 插件包装（`.antigravity-plugin/plugin.json`、`hooks/hooks.json`、`hooks/session-start.sh`、`hooks/session-start.ps1`、`tests/agy.test.mjs`）。

## 决定

**只把经过筛选且无文件/规则冲突的纯 Markdown 技能与做法收进仓库，门禁检查与安全钩子用 TypeScript 承接；不安装或整包拷贝全量 65 技能插件。**

- **采纳的**：
  - **`correct` 逐层固化机制**：重复出现两次以上的人工纠错，依次用「单一事实源或结构消除 → 编译期类型约束 → 报错直接写明改法的门禁检查 → 行为测试 → 最后才写文档规则」处理，并在加新检查时先在真实历史错误上验证它会失败，然后在 `AGENTS.md` 维护规则与门禁检查的对照表。
  - **`architect`、`blast-radius`、`typescript-best-practices`**：动复杂多模块前先用 `architect` 并行出两套以上结构草图与接口边界，再按 `blast-radius` 跑真实代码验证关键假设与影响面，写 `.ts`/`.tsx` 时遵循 `typescript-best-practices`（以 `zod` 和生成 OpenAPI 类型为准，不用手写 `as` 代替边界校验）。
  - **`deslop` 与 `unslop`**：提交前对照分支差异删掉复述代码的注释、信任边界内的多余兜底与空泛套话；文档与 PR 正文坚持用真实事实和数字说话。
  - **`how` 与 `why`**：改动陌生或带防御逻辑的代码前，按 `how` 做并行只读代码走读，按 `why` 查 `git blame`、PR 讨论、ADR 与事故记录，严格区分直接证据、推断与未知。
  - **`create-verification-skill` 与 `create-formal-verification-skill`**：按前者生成并实跑验证了项目专属的 `verify` 技能（`.agents/skills/verify/` 与 `.claude/skills/verify/`），把 Playwright 录制网关（5190/5194 端口）与本地 `hvac-local` 平台（`https://localhost:8443`、1440×900 截图）的启动、体检、驱动、留证和清理固化成可执行手册；按后者落地项目专属的 `command-safety` 形式化验证技能（`modules/command/pkg/commandformal` 与 `npm run command:formal`），对 `commandservice`（共享规则文件 `transitions.go`）与 `mqttconnector` 恢复逻辑做显式状态机穷举检验并校验与 PostgreSQL SQL 守卫语句对齐。
  - **跨平台 `PreToolUse` 安全钩子与 1:1 技能镜像门禁**：用 `scripts/hooks/pre-tool-guard.ts` 统一拦截破坏性 Git 操作、直推 `main`/`master`、读取或打印运行时密钥，以及对生产部署/清库命令发出确认提示，同时接入 `.agents/hooks.json`（Antigravity）和 `.claude/settings.json`（Claude Code）；在 `scripts/check-repository-governance.ts` 强制校对 `.agents/skills/` 与 `.claude/skills/` 30 个技能逐字节 1:1 镜像，并将遗留 JS 上限收紧至 `102`（同时禁止 `.agents/` 下保留 JS）。
- **改造的**：
  - **精选 10 个上游技能并裁撤 24 个过时/重名技能，双目录逐字节同步**：全量 `pstack-claude` 含 65 个技能、600 余个文件及多份 `.mjs` 脚本；整包拷入既会突破 `scripts/javascript-tooling-baseline.json` 的遗留 JS 文件数上限，也会把大量无关或与已有工作流重名的技能描述塞进上下文。裁撤 24 个过时/非本技术栈技能、Codex `openai.yaml` 及 `why` 中不使用的 6 份 SaaS 外部手册后，连同项目专属的 `verify` 与 `command-safety` 技能，仓库统一保留 30 个技能，由 `npm run repo:check` 自动守住双目录一致性。
  - **清理跨插件死链与外部模型配置引用**：引入 `architect`、`blast-radius`、`typescript-best-practices`、`how`、`why`、`create-verification-skill`、`create-formal-verification-skill` 时，去掉指向未引入的 `../poteto-mode/references/*`、`arena`、`interrogate` 与 `pstack-models.md` 模型覆写表的断链，统一改写为当前运行环境的并行子代理与双目录路径。
  - **PR 交付与自动合入仍按本仓库规则执行**：上游 `opening-a-pr.md` 默认「开 PR 后不盯 CI、不自动合并」，与 `AGENTS.md` 冲突；不引入 `poteto-mode` 总控技能，而是把 `architect`/`blast-radius`/`how`/`why`/`verify`/`deslop` 串进 `docs/agents/issue-delivery.md`，继续保持本地检查与审查完成后开 PR、盯完 GitHub Actions、全绿自动 squash 合并的闭环。
- **拒绝的**：
  - **`SessionStart` 启动钩子（`hooks.json` + `session-start.sh`）**：上游插件在 Antigravity 清单里写死执行 `./hooks/session-start.sh agy`，在 Windows 下会通过 `cmd /c` 直接失败，且其 `session-start.ps1` 只支持 `codex|muse|copilot`、不支持 `agy`；每轮会话注入大段英文总控提示也会与 `AGENTS.md` 重叠。
  - **外部服务、热修复与重型脚本技能**：`setup-thermos` / `thermos` / `grainulator`（无 Thermos 环境）、`create-pr` / `babysit-pr` / `bug-court`（已有 `pr`、`diagnosing-bugs` 与自动合入流程）、`statechart` 与 `tla-plus` 语料及 `.mjs` 脚本（具体并发状态机如 `command-safety` 直接按 `create-formal-verification-skill` 用 Go 单独落盘，不搬运上游通用语料）。
