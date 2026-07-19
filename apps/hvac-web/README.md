# HVAC Web

泉来禾智慧能源平台的生产 Web adapter。

- Runtime：Vite + React 18
- 入口：`src/main.tsx`
- AI 接入：`src/ai`
- 配置：`vite.config.ts`、`tsconfig.json`

从仓库根目录运行：

```bash
npm run dev:web
npm run lint
npm run build
```

该目录只负责浏览器端产品界面。CopilotKit Runtime 和 EnergyAgent 分别位于 `runtimes/copilot-runtime` 与 `agents/energy-agent`。
