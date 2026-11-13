# 问题单：GitHub

本仓库的需求、PRD 和任务都记在 GitHub Issues 里，一律用 `gh` 命令行操作（在 WSL 里已登录的那份）。

## 常用操作

- **新建**：`gh issue create --title "..." --body "..."`。多行正文用 heredoc。
- **查看**：`gh issue view <编号> --comments`，需要时用 `jq` 筛评论，并把标签一起取出来。
- **列表**：`gh issue list --state open --json number,title,body,labels,comments --jq '[.[] | {number, title, body, labels: [.labels[].name], comments: [.comments[].body]}]'`，按需加 `--label`、`--state`。
- **评论**：`gh issue comment <编号> --body "..."`
- **加 / 去标签**：`gh issue edit <编号> --add-label "..."` / `--remove-label "..."`
- **关闭**：`gh issue close <编号> --comment "..."`

仓库从 `git remote -v` 推断，在仓库目录里运行 `gh` 时会自动识别。

## 外部 PR 分诊

**PRs as a request surface: no.** 本仓库默认不把外部 PR 当作需求入口来分诊；确需查看或处理 PR 时，用对应的 `gh pr` 命令：

- **查看 PR**：`gh pr view <编号> --comments`，改动用 `gh pr diff <编号>`。
- **列出开放 PR**：`gh pr list --state open --json number,title,body,labels,author,authorAssociation,comments`，按需筛选外部贡献者（`CONTRIBUTOR`、`FIRST_TIME_CONTRIBUTOR`、`NONE`）。
- **评论 / 标签 / 关闭**：`gh pr comment`、`gh pr edit --add-label` / `--remove-label`、`gh pr close`。

GitHub 的问题单和 PR 共用一套编号，单写 `#42` 可能是任何一种：先 `gh pr view 42`，不是再 `gh issue view 42`。

## 拆分大型任务与阻塞关系

- **拆分子单**：大型需求拆成父单和子单时，可用 GitHub sub-issue，或在父单任务列表里列出各子单并在子单正文第一行注明 `Part of #<父单编号>`，认领时用 `gh issue edit <n> --add-assignee @me`。
- **阻塞关系**：用 GitHub **原生的问题依赖** 记录阻塞关系：`gh api --method POST repos/<owner>/<repo>/issues/<子单>/dependencies/blocked_by -F issue_id=<阻塞单的数据库 id>`（传入数字数据库 id：`gh api repos/<owner>/<repo>/issues/<n> --jq .id`），或在子单正文第一行写 `Blocked by: #<n>`。所有阻塞单关闭后再推进后续任务。
- **结束**：`gh issue comment <n> --body "<结论>"`，再 `gh issue close <n>`，并在父单补充结果链接。
