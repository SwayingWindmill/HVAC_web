# Copilot Runtime

Node.js CopilotKit Runtime module，位于 HVAC Web 与 EnergyAgent LangGraph 之间。

职责：

- 注册统一的 `default` Agent；
- 将 CopilotKit/AG-UI 请求转发到 EnergyAgent graph；
- 启用 A2UI middleware；
- 提供 Runtime 与上游 Agent 健康状态。

从仓库根目录运行：

```bash
npm run dev:ai-runtime
npm run verify:ai-runtime
```

主要环境变量见 `docs/energyagent-integration.md`。
