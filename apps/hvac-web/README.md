# HVAC Web

泉来禾智慧能源平台的网页，浏览器端唯一的产品入口。

- 运行环境：Vite + React 19 + TypeScript。
- 入口：`src/app/main.tsx`；应用框架：`src/app/`。
- 业务页面：`src/features/`；路由：`src/routes/`（`routeTree.gen.ts` 由 Vite 插件生成）。
- 接口客户端：`src/api/`（由 OpenAPI 生成）；实时遥测：`src/platform/telemetry-live/`。
- 页面共用的组合：`src/blocks/`（见那里的 README）；设计规则：根目录 `DESIGN.md`。

`apps/hvac-web` 是一个单独的 npm workspace，包名 `@hvac/web`。它的 `package.json`、`components.json`、TypeScript 和 Vite 配置、运行依赖都在本目录；仓库根目录的 package 负责编排和跨项目的检查。

在仓库根目录（WSL 里）运行：

```bash
npm run dev
npm run typecheck:web
npm run build
npm run preview
npm run web:e2e
```

也可以直接调 workspace：

```bash
npm -w @hvac/web run dev
npm -w @hvac/web run typecheck
npm -w @hvac/web run build
```

shadcn、tablecn、ReUI 等组件注册表只在 `apps/hvac-web/components.json` 里配置，从根目录执行：

```bash
npm run ui:shadcn -- info --json
npm run ui:shadcn -- add <注册表里的组件>
```

前端工具链只能用 Linux/WSL 的 Node 运行。workspace 的脚本用当前的 Node 直接启动 Vite、TanStack Router CLI 和 TypeScript，免得 Windows 和 WSL 共用一份代码时，旧的 `.bin` 脚本把构建切到另一个系统。

网页通过受保护的应用框架和平台网关拿身份、站点范围、设备登记、遥测、能耗、指令、告警和 AI 助手的数据，只有一个入口、一种构建，没有演示版或假数据模式。浏览器不直接连内部服务、大模型、数据库或设备。

组件不直接发请求：数据依次经过生成的接口客户端、feature 里的查询（TanStack Query）或实时状态层，再显示到界面上。
