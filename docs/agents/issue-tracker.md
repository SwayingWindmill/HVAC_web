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

## 外部 PR 算不算需求

**PRs as a request surface: no.**（`/triage` 读这一行。本仓库不把外部 PR 当作需求来分诊；以后要这样做，把 `no` 改成 `yes`。）

改成 `yes` 时，PR 和问题单用同样的标签和状态，换成对应的 `gh pr` 命令：

- **查看 PR**：`gh pr view <编号> --comments`，改动用 `gh pr diff <编号>`。
- **列出要分诊的外部 PR**：`gh pr list --state open --json number,title,body,labels,author,authorAssociation,comments`，只留 `authorAssociation` 是 `CONTRIBUTOR`、`FIRST_TIME_CONTRIBUTOR` 或 `NONE` 的（去掉 `OWNER`/`MEMBER`/`COLLABORATOR`）。
- **评论 / 标签 / 关闭**：`gh pr comment`、`gh pr edit --add-label` / `--remove-label`、`gh pr close`。

GitHub 的问题单和 PR 共用一套编号，单写 `#42` 可能是任何一种：先 `gh pr view 42`，不是再 `gh issue view 42`。

## 技能说「发布到问题单」时

新建一张 GitHub 问题单。

## 技能说「取相关的单子」时

运行 `gh issue view <编号> --comments`。

## Wayfinder 的操作

`/wayfinder` 用。**地图**是一张问题单，它的**子单**是一张张任务。

- **地图**：一张带 `wayfinder:map` 标签的问题单，正文写笔记、已定的决定和还没弄清的部分。`gh issue create --label wayfinder:map`。
- **子单**：以 GitHub 子问题（sub-issue，用 `gh api` 调子问题接口）挂在地图下。没开子问题功能时，把子单加到地图正文的任务列表里，并在子单正文第一行写 `Part of #<地图编号>`。标签用 `wayfinder:<类型>`（`research`/`prototype`/`grilling`/`task`）。有人认领后，指派给他。
- **阻塞关系**：用 GitHub **原生的问题依赖**，页面上看得到。加一条：`gh api --method POST repos/<owner>/<repo>/issues/<子单>/dependencies/blocked_by -F issue_id=<阻塞单的数据库 id>`，这里要的是数字形式的**数据库 id**（`gh api repos/<owner>/<repo>/issues/<n> --jq .id`），**不是** `#编号` 或 `node_id`。GitHub 在 `issue_dependencies_summary.blocked_by` 里只统计还开着的阻塞单。没有依赖功能时，在子单正文第一行写 `Blocked by: #<n>, #<n>`。所有阻塞单都关了，这张子单就解除阻塞。
- **找下一张能做的**：列出地图下还开着的子单（`gh issue list --state open`，限定在地图的子问题或任务列表里），去掉有开着的阻塞单（`issue_dependencies_summary.blocked_by > 0`，或 `Blocked by` 行里有开着的单）或已经有人认领的；按地图里的顺序取第一张。
- **认领**：`gh issue edit <n> --add-assignee @me`，这是这次会话的第一个写操作。
- **结束**：`gh issue comment <n> --body "<结论>"`，再 `gh issue close <n>`，然后在地图的「已定的决定」里补一条摘要和链接。
