---
date: 2026-11-20
category: 改进
---
- 更新记录改为 `changelog.d/` 独立碎片文件模式：每个 PR 新增独立碎片文件并由 `npm run repo:check` 与 `npm run changelog:check` 校验格式及防直改规则，汇总发布时通过 `npm run changelog:release` 自动归并进 `CHANGELOG.md`，消除并行 PR 的高频合并冲突。
