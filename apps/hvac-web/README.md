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

shadcn CLI 不装进依赖，`scripts/shadcn-cli.mjs` 用 `npx` 运行固定版本（`SHADCN_CLI_VERSION`）。shadcn 的基础样式是这个版本 `dist/tailwind.css` 的副本，放在 `src/styles/shadcn-tailwind.css`；升级 CLI 时把这个文件换成新版本的，`npm run design:check` 会检查两者版本一致。

前端工具链只能用 Linux/WSL 的 Node 运行。workspace 的脚本用当前的 Node 直接启动 Vite、TanStack Router CLI 和 TypeScript，免得 Windows 和 WSL 共用一份代码时，旧的 `.bin` 脚本把构建切到另一个系统。

网页通过受保护的应用框架和平台网关拿身份、站点范围、设备登记、遥测、能耗、指令、告警和 AI 助手的数据，只有一个入口、一种构建，没有演示版或假数据模式。浏览器不直接连内部服务、大模型、数据库或设备。


## 约定

- 技术栈：React 19、Vite、TypeScript、Tailwind CSS v4、shadcn/ui（`radix-nova`，组件用 Radix 的 `asChild` 组合）、TanStack Router / Query / Table、React Hook Form + Zod、date-fns、Lucide 图标；图表用 shadcn Chart + Recharts，数字孪生的 3D 用 React Three Fiber。
- 状态各归其位：URL 状态归 Router，服务端数据归 Query，表单归 React Hook Form，组件自己的状态用 `useState` / `useReducer`；多个组件真正共享的纯前端状态才用 Zustand。同一个事实只放在一套状态里。
- 站点范围的页面放在 `routes/_app._site.*`，站点由 `?site=` 指定（用 `useWorkspaceScope()` 读）；对象详情在页面内的侧栏里打开，由 `?inspect=<id>` 指定，没有单独的详情路由。
- 路由文件只解析 URL、做权限跳转、组合加载和出错状态；业务逻辑、查询、表单 schema 和领域组件放在 `features/<领域>`。这就是全部的分层：没有 `pages/`、仓储层、通用 Store 或全局事件总线。
- 页面调接口统一走 OpenAPI 生成的客户端加 feature 里的 query / mutation。实时页面先取快照再接推送，推送由实时状态层（`src/platform/telemetry-live/`）负责，业务组件不自己开 WebSocket。
- 按钮、弹窗、表格等组件都来自 shadcn/ui 和 `src/blocks` 里的组合（`Main`、`PageHeader`、`Panel`、`StateCard`、`DataTableBlock` 等，见 `DESIGN.md`），不另做一套；普通列表用 `DataTableBlock`（`npm run design:check` 会检查）。
- 页面只显示真实来源的数据；还没接上的，明确显示「未接入」。示例数据只出现在 `HVAC_WEB_FRONTEND_REVIEW=true` 的评审构建里。
- 面向值班人员的界面只显示业务事实：状态、影响、时间、原因、谁负责、能做什么；用 `GLOSSARY.md` 里的词。内部值（UUID、trace ID、枚举、版本号、合同或 schema 名）翻成日常说法，或者不显示；业务编号用真的业务编号，不拿 UUID 截一段代替。
- 只做桌面端。界面改动要在 1440px 宽度下用真实数据实际看过（本地 8443 或服务器），构建通过不算看过；发现的问题一次改完，再看一遍确认。
