# CodeGraph

CodeGraph 在本地给代码建一张关系图，可以按结构搜索、查调用路径、看改一个文件会影响哪些地方。

## 安装

```bash
npm ci
npm run codegraph:init
```

装好后重启支持 MCP 的 agent。项目级的 MCP 配置会启动固定版本的本地包，并关闭匿名统计。

## 命令

```bash
npm run codegraph:status
npm run codegraph:sync
npx --no-install codegraph explore "how does command submission reach PostgreSQL?"
npx --no-install codegraph affected path/to/changed-file.ts
```

索引存在 `.codegraph/`（已在 `.gitignore` 里）。`codegraph.json` 排除了 worktree、临时数据、生成的产物、原型和参考资料，所以结果只描述现在在用的平台代码。
