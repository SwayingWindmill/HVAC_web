# Open Code Review 调研与引入记录（2026-11-16）

## 看了什么

跨模块或改动文件较多时，仅靠模型手读 diff 容易跳过末尾文件或配置变化。为此比对并实装验证了：

- `alibaba/open-code-review` `v1.13.10`（提交 `7e5ce06c88de5ce81df5dc4f24e2e485b517fae8`，Apache-2.0 协议），包括 `skills/open-code-review-delegate/SKILL.md`、`internal/config/rules/system_rules.go`、`internal/config/rules/rule_docs/{go,ts_js_tsx_jsx}.md`、`internal/config/allowlist/default_exclude_patterns.json` 与 `internal/diff/git.go`。

## 决定

**只引入本地委托模式（`open-code-review-delegate`）与精简版 `.opencodereview/rule.json`，作为大改动分支的补充清单工具，不替代 `thermos` / `interrogate`，也不增加额外门禁脚本或阻塞性 CI。**

- **采纳的**：
  - **`open-code-review-delegate` Skill**：用 `ocr delegate preview --format json` 与 `ocr delegate rule --format json` 做确定性选文件和规则分组，再由当前宿主 Agent 按 `(path, status)` 清单逐文件审查并汇报 `coverage_rate`，OCR 侧不配置也不调用外部大模型。
  - **精简版 `.opencodereview/rule.json`**：仅在 `exclude` 补上上游默认列表遗漏的 `**/*.gen.ts`（`apps/hvac-web/src/api/generated/*.gen.ts`、`apps/hvac-web/src/routeTree.gen.ts`、`semantic-catalog.gen.ts`），并在 `merge_system_rule: true` 下补充指令状态机、冻结迁移、合约归属与前端真实性 4 条项目规则。
  - **PR 分类器识别 `.opencodereview/`**：在 `scripts/classify-pr-gates.ts` 元数据白名单放行 `.opencodereview/`，避免调整评审规则时触发全仓门禁。
- **改造的**：
  - 按 `AGENTS.md` 与 `repo:check` 要求将 Skill 逐字节 1:1 镜像到 `.agents/skills/` 与 `.claude/skills/`；把大 diff 临时目录从 `/tmp` 改为 `.scratch/ocr-diff/`；移除自动改码步骤，明确只输出发现，实际修改继续走 `poteto-mode` 与 `scripts/domain-task-matrix.ts` 验证。
- **拒绝的**：
  - **`open-code-review` 独立 LLM 审查模式与阻塞 CI**：避免额外维护大模型密钥、CI 超时卡合入或绕开本地工具边界。
  - **额外防御脚本与测试**：`ocr` 已按 `.gitignore` 自动过滤工作区与分支里的运行时密钥目录，Skill 镜像也已有 `npm run repo:check` 守住，不再叠写冗余测试或守卫脚本。
