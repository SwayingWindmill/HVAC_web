# pstack 工程工作流调研（2026-11-13）

## 看了什么

团队人手和 Agent 变多后，仅靠在 `AGENTS.md` 与 `docs/HANDOFF.md` 里追加「注意不要……」「记得同步修改……」很容易失效：单份交接文档膨胀到接近工具单次读取上限（48,434 字节对比 46,080 字节阈值），而 `capability-labels.ts` 等手工双写清单也已经发生实际漏同步。为此调研了：

- `michael-denyer/pstack-claude` `0.9.138`（提交 `2044f1810afc98c3aa80100587dd990c83cf6ea8`，MIT 协议），及其 `tools/upstream.json` 固定的三个上游来源：`cursor/plugins` 的 `pstack`（`c742dd61706fbacc1b95f1a20b7b988cf096ce6c`）、`cursor-team-kit`（`e46364b8be46000b7df0f260550cd712afbb8d36`）和 `thermos`（`118e73b0e196e4d49d5aade7ce6dd8985a621f68`）；
- 重点审阅了 `poteto-mode`（含 `references/agy-tools.md`、`playbooks/bug-fix.md`、`playbooks/opening-a-pr.md`）、`correct`、`deslop`、`unslop`、`how`、`why`、`create-verification-skill`、`create-formal-verification-skill`，以及 Antigravity 插件包装（`.antigravity-plugin/plugin.json`、`hooks/hooks.json`、`hooks/session-start.sh`、`hooks/session-start.ps1`、`tests/agy.test.mjs`）。

## 决定

**只把经过筛选且无文件/规则冲突的纯 Markdown 技能与做法收进仓库，门禁检查用项目已有脚本承接；不安装或整包拷贝全量 65 技能插件。**

- **采纳的**：
  - **`correct` 逐层固化机制**：重复出现两次以上的人工纠错，依次用「单一事实源或结构消除 → 编译期类型约束 → 报错直接写明改法的门禁检查 → 行为测试 → 最后才写文档规则」处理，并在加新检查时先在真实历史错误上验证它会失败，然后在 `AGENTS.md` 维护规则与门禁检查的对照表。本次直接用它消掉了三处积弊：
    1. `FactTile` 把 `className` 改为 `valueClassName`，并在 `scripts/check-design-system.mjs` 拦截 `<FactTile className="...">`；
    2. `apps/hvac-web/src/lib/capability-labels.ts` 与 `role-labels.ts` 改为直接受 `platformGateway.gen.ts` 类型与 `scripts/test-rms-principal-capability-contract.mjs` 契约测试双重约束（当场补齐漏掉的 `session.revoke`「吊销会话」与 `template.manage`「模板版本」，清掉 6 个已废弃权限键）；
    3. `scripts/check-repository-governance.ts` 新增 `docs/HANDOFF.md` `28,672` 字节单次读取上限检查与 `.audit/` 禁止跟踪检查。
  - **`deslop` 与 `unslop`**：提交前对照分支差异删掉复述代码的注释、信任边界内的多余兜底与空泛套话；文档与 PR 正文坚持用真实事实和数字说话。
  - **`how` 与 `why`**：改动陌生或带防御逻辑的代码前，按 `how` 做并行只读代码走读，按 `why` 查 `git blame`、PR 讨论、ADR 与事故记录，严格区分直接证据、推断与未知。
  - **`create-verification-skill` 与 `create-formal-verification-skill`**：按前者生成并实跑验证了项目专属的 `verify` 技能（`.agents/skills/verify/` 与 `.claude/skills/verify/`），把 Playwright 录制网关（5190/5194 端口）与本地 `hvac-local` 平台（`https://localhost:8443`、1440×900 截图）的启动、体检、驱动、留证和清理固化成可执行手册。
- **改造的**：
  - **精选 7 个上游技能并双目录同步，不整包引入 65 个技能**：全量 `pstack-claude` 含 65 个技能、600 余个文件及多份 `.mjs` 脚本；整包拷入既会突破 `scripts/javascript-tooling-baseline.json` 的遗留 JS 文件数上限（`111 / 111`），也会在每轮会话把大量无关技能描述塞进上下文，并与已有的 `mattpocock/skills`（`tdd`、`pr`、`code-review`、`diagnosing-bugs`、`prototype`、`research`、`teach`）重名。只引入互补的 `correct`、`deslop`、`unslop`、`how`、`why`、`create-verification-skill`、`create-formal-verification-skill` 加项目自建 `verify`，同步放在 `.agents/skills/`（Antigravity 读取）与 `.claude/skills/`（Claude Code 读取），授权文本留在 `.agents/skills/PSTACK-LICENSE.txt`（并在 `.claude/skills/README.md` 登记出处与版本）。
  - **清理跨插件死链与外部模型配置引用**：上游 `how`、`why`、`create-verification-skill`、`create-formal-verification-skill` 里引用了未引入的 `../poteto-mode/references/*`、`../statechart/SKILL.md` 与 `pstack-models.md` 模型覆写表；引入时统一改写为直接使用当前运行环境的只读/通用子代理与双目录镜像路径。
  - **PR 交付与自动合入仍按本仓库规则执行**：上游 `opening-a-pr.md` 默认「开 PR 后不盯 CI、不自动合并」，与 `AGENTS.md` 冲突；不引入 `poteto-mode` 总控技能，而是把 `how`/`why`、`verify` 与 `deslop` 串进 `docs/agents/issue-delivery.md`，继续保持本地检查与审查完成后开 PR、盯完 GitHub Actions、全绿自动 squash 合并的闭环。
- **拒绝的**：
  - **`SessionStart` 启动钩子（`hooks.json` + `session-start.sh`）**：上游插件在 Antigravity 清单里写死执行 `./hooks/session-start.sh agy`，在 Windows 下会通过 `cmd /c` 直接失败，且其 `session-start.ps1` 只支持 `codex|muse|copilot`、不支持 `agy`；每轮会话注入大段英文总控提示也会与 `AGENTS.md` 重叠。
  - **外部服务、热修复与重型脚本技能**：`setup-thermos` / `thermos` / `grainulator`（无 Thermos 环境）、`create-pr` / `babysit-pr` / `bug-court`（已有 `pr`、`diagnosing-bugs` 与自动合入流程）、`statechart` 与 `tla-plus` 语料及 `.mjs` 脚本（受 JS 棘轮限制，后续对具体并发状态机做形式化验证时再按 `create-formal-verification-skill` 单独落盘）。
