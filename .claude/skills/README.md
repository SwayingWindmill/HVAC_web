# 第三方与项目 skill

本目录下包含以下来源的 skill：

1. [mattpocock/skills](https://github.com/mattpocock/skills)（MIT 协议）：版本固定在提交 `49dd158d1076134a641b33efb035946536778336`（插件 mattpocock-skills 1.3.1），授权文件见 `.agents/skills/MATTPOCOCK-LICENSE.txt`，内容原样复制并去掉了 Codex 专用的 `agents/openai.yaml`。其中 `wizard/template.sh` 和 `diagnosing-bugs/scripts/hitl-loop.template.sh` 是给 agent 复制改写的模板，不会自动运行。
2. [michael-denyer/pstack-claude](https://github.com/michael-denyer/pstack-claude)（MIT 协议，基于 Lauren Tan 的 `pstack` 与 `cursor-team-kit`）：版本 `0.9.138`，提交固定在 `2044f1810afc98c3aa80100587dd990c83cf6ea8`，授权文件见 `.agents/skills/PSTACK-LICENSE.txt`，筛选与改造说明见 `docs/architecture/pstack-source-review-2026-11-13.md`。引入且双目录同步的 7 个技能为：`correct`、`deslop`、`unslop`、`how`、`why`、`create-verification-skill`、`create-formal-verification-skill`（均已清理跨插件断链与平台模型覆写表引用）。
3. 项目自建 `verify` 技能：按 `create-verification-skill` 为 `HVAC_web` 编写并实跑验证的浏览器取证技能，覆盖录制网关与本地 `hvac-local` 平台，目录与 `.agents/skills/verify/` 保持一致。
