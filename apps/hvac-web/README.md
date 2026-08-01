# HVAC Web

泉来禾智慧能源平台的浏览器端产品应用。

- Runtime：Vite + React 19 + TypeScript。
- Demo 入口：`src/demo/main.tsx`。
- Real 入口：`src/real/main.tsx`。
- 通用产品路由：`src/App.tsx` 和 `src/pages/`。
- 真实平台适配：`src/real/`、`src/api/` 和 `src/platform/telemetry-live/`。
- AI 与 Operations Workspace：`src/ai/` 和 `src/real/OperationsInvestigation.tsx`。

从仓库根目录运行：

```bash
npm run dev:demo
npm run dev:real
npm run lint
npm run build:demo
npm run build:real
```

Demo 模式使用演示数据和本地只读能力。Real 模式通过受保护 Shell 和 Platform Gateway 获取身份、站点范围、Registry、遥测、能源、命令、告警及 Operations Agent 数据。浏览器不得直连内部服务、模型提供方、数据库或 ThingsBoard。

页面不得直接发起无边界请求。数据访问应依次经过生成或版本化 API 契约、领域适配器、React Query/实时状态层，再投影到 UI。
