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

该目录只负责浏览器端产品界面。当前 AI 功能使用本地只读 `HvacMockAgent`；未来 Operations Agent 通过 Platform Gateway 接入，不允许浏览器直连 Agent Runtime。
