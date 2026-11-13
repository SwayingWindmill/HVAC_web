# CodeGraph

CodeGraph 在本地给代码建一张关系图，可以按结构搜索、查调用路径、看改一个文件会影响哪些地方。

## 安装

```bash
npm ci
npm run codegraph:init
```

装好后重启支持 MCP 的 agent。`package.json` 中的 `codegraph:*` 脚本和项目 `.mcp.json` 都直接通过 `node ./node_modules/@colbymchenry/codegraph/npm-shim.js` 调用仓库固定版本（并关闭匿名统计），可在 WSL 和 Windows 上直接运行，不依赖全局 `codegraph` 或 `.cmd` 包装脚本。

如果在 Windows 上使用 Antigravity 桌面版并配置用户级 `~/.gemini/config/mcp_config.json`，同样配置 `"command": "node"` 与 `"args": ["E:/Code/HVAC_web/node_modules/@colbymchenry/codegraph/npm-shim.js", "serve", "--mcp"]`，避免桌面版启动时遇到 `codegraph ENOENT` 或旧版全局安装版本冲突。

## 命令

支持 MCP 的 agent 优先直接调用 `codegraph_explore`、`codegraph_context`、`codegraph_search` 和 `codegraph_impact`；CLI 命令如下：

```bash
npm run codegraph:status
npm run codegraph:sync
node ./node_modules/@colbymchenry/codegraph/npm-shim.js explore "how does command submission reach PostgreSQL?"
node ./node_modules/@colbymchenry/codegraph/npm-shim.js affected path/to/changed-file.ts
```

索引存在 `.codegraph/`（已在 `.gitignore` 里）。排除的目录列在 `codegraph.json` 里（临时数据、生成的产物、原型和参考资料），所以结果只描述现在在用的平台代码。
