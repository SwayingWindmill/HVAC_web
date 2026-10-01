# HVAC Web

泉来禾智慧能源平台的浏览器端权威产品应用。

- Runtime：Vite + React 19 + TypeScript。
- 产品入口：`src/app/main.tsx`。
- 应用骨架与 Shell：`src/app/`。
- 领域页面：`src/features/`。
- 平台 API 适配：`src/api/`。
- 实时遥测：`src/platform/telemetry-live/`。
- Operations Workspace：`src/features/operations/OperationsInvestigation.tsx`。

`apps/hvac-web` 是独立 npm workspace，package name 为 `@hvac/web`。它自己的 `package.json`、`components.json`、TypeScript/Vite 配置和应用运行依赖都放在本目录；仓库根 package 负责统一编排和跨项目治理，并暂时保留仓库级 browser review fixtures 直接使用的 React/Vite/TanStack 等工具依赖。

从仓库根目录运行：

```bash
npm run dev
npm run typecheck:web
npm run build
npm run preview
```

也可以显式调用 workspace：

```bash
npm -w @hvac/web run dev
npm -w @hvac/web run typecheck
npm -w @hvac/web run build
```

shadcn/tablecn/ReUI registry 配置唯一位于 `apps/hvac-web/components.json`。从仓库根目录统一执行：

```bash
npm run ui:shadcn -- info --json
npm run ui:shadcn -- add <registry-component>
```

前端 JavaScript 工具链只允许由 Linux/WSL Node 执行。workspace 脚本显式由当前 Node 启动 Vite、TanStack Router CLI 和 TypeScript，避免 Windows/WSL 共用 checkout 时历史 `.bin` shim 把构建切到另一侧操作系统。

Web 应用通过受保护 Shell 和 Platform Gateway 获取身份、站点范围、Registry、遥测、能源、命令、告警及 Operations Agent 数据。仓库不再维护独立 Demo 应用、Mock/Real 双入口或双构建目标。浏览器不得直连内部服务、模型提供方、数据库或设备 Provider。

页面不得直接发起无边界请求。数据访问应依次经过生成或版本化 API 契约、领域适配器、React Query/实时状态层，再投影到 UI。
