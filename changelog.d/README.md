# changelog.d 更新记录碎片目录

为避免多个并行 PR 同时修改 [`CHANGELOG.md`](../CHANGELOG.md) `## 未发布` 顶部同一位置导致反复合并冲突，所有面向用户的变更改为在本目录新增**独立碎片文件**，不再在业务/修复 PR 中直接编辑 `CHANGELOG.md`。

## 文件命名

```text
changelog.d/YYYY-MM-DD-<slug>.md
```

- `YYYY-MM-DD`：有效公历日期，需与 frontmatter 中的 `date` 一致。
- `<slug>`：仅允许小写字母 `a-z`、数字 `0-9` 和连字符 `-`（首字符为字母或数字），建议使用语义短名，例如 `2026-11-20-fix-tariff-display.md`。
- 换行符统一为 LF。

## 文件格式

仅允许 `date`、`category` 和可选 `pr` 三个 frontmatter 字段；`category` 仅允许 `新增`、`改进`、`变更`、`修复`：

```markdown
---
date: 2026-11-20
category: 改进
pr: 925
---
- 用大白话写清楚用户或运维人员看到的变化、影响了哪个页面/接口或运行行为。
```

- 正文至少包含一条以 `- ` 开头的列表项（可含缩进子项），禁止出现 Markdown 标题（`#`）或 Git 冲突标记。

## 检查与汇总发布命令

- 校验本目录全部碎片并检查 `CHANGELOG.md` 未被违规直改：

  ```bash
  npm run changelog:check
  ```

- 将本目录所有碎片按日期降序与固定分类顺序（`新增` → `改进` → `变更` → `修复`）归并进 `CHANGELOG.md` 的 `## 未发布` 下，并删除已汇总碎片（可先加 `--dry-run` 预览）：

  ```bash
  npm run changelog:release -- --dry-run
  npm run changelog:release
  ```
