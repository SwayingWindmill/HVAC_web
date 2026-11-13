# 问题单的交付流程

每张问题单按这五步交付（复杂或跨模块工作可配合 `poteto-mode` 总控剧本，非标准大型迁移用 `figure-it-out` 先拆步骤），每一步做到「完成」那一句才进下一步。

1. **读清楚要做什么。** 用 `in-your-own-words` 先复述用户目标、要解决的问题和最小预期改动；读问题单、上级规格、评论、依赖的单子、`GLOSSARY.md` 和相关的 ADR，记下从哪个提交开始做。改动不熟悉的模块前先用 `how` 走读运行路径；涉及防御逻辑、阈值或历史取舍时先用 `why` 查 `git blame`、PR 讨论与 ADR；跨模块多状态设计或共享契约改动先用 `architect` 出结构草图（备选多形状时用 `arena` 比选），并用 `blast-radius` 跑真实代码验证关键假设。多切片并行时按 `swarm` 拆独立工作树或目录。
   完成：能用 `GLOSSARY.md` 的词说出要交付的每个行为和它的验收方式。
2. **参照外部项目时先读源码。** 借鉴外部项目时，看固定版本的官方源码、测试和文档，按 `AGENTS.md` 在 `docs/architecture/` 里记下采纳、改造或拒绝了什么。产品文档和设计文档比旧的实现更有说服力。
   完成：每个借来的设计都有固定版本的出处和取舍理由。
3. **一个行为一个行为地做。** 明确数据形状与不变量，修复存在廉价本地复现路径的缺陷时先按 `tdd` 写会因预期原因失败的回归测试再改代码；每一步都打通真实的产品路径。编辑 `.ts`/`.tsx` 时遵循 `typescript-best-practices`；同类错误或人工纠错重复出现两次，顺手按 `correct` 技能用结构、类型或门禁消掉。
   完成：规格里的每个行为都有可复现的验证或先失败后通过的测试，并且在产品里能走通。
4. **在真实环境里按 `verify` 取证验收。** 在 WSL 里构建、测试、运行；改了网页 UI 时按 `verify` 技能在 1440px 宽度下运行网页并把修改前后的截图与命令输出留在 `.scratch/verify/`，必要时用 `maintain-verification-skill` 同步更新 `verify/features/`。改动控制指令协议、设备围栏或 MQTT 回执关联状态时加跑 `npm run command:formal`（`command-safety`），并按 `maintain-formal-verification-skill` 校对同步提交号。
   完成：每条验收标准都有可复查的运行输出或 1440px 截图证据（录制网关与实库 `https://localhost:8443` 分开记录）。
5. **清理、验证后自动 PR 并合入。** 按 `deslop` 与 `no-comments` 清理多余注释与临时兜底（大型或高复杂度改动加用 `thermo-nuclear-code-quality-review`），文本和 PR 正文用 `unslop`；存在争议设计或高风险边界时，先用 `interrogate` 跑对抗实证并核对 `AGENTS.md` / 规格基线。按 `make-pr-easy-to-review` 与 `poteto-mode/playbooks/opening-a-pr.md` 整理提交和 PR 说明，Windows 侧推分支、WSL `gh` 开 PR，并用 `gh pr checks --watch`（故障、冲突或评审意见时用 `babysit`、`fix-ci`、`fix-merge-conflicts`、`get-pr-comments`）跟进 CI：全绿即自动 `gh pr merge --squash --delete-branch` 合入 `main` 并清理分支。
   完成：PR 已在 CI 全绿下合入 `main`，可操作问题已解决或记清；验收全部完成后才关问题单。

对外发消息、重置数据和触发生产部署仍要当前会话里用户的授权。
