# apps/hvac-web/AGENTS.md

泉来禾智慧能源平台网页端（`@hvac/web`），浏览器唯一入口，必须使用 WSL Linux Node 运行工具链（拒绝 Windows Node）。完整约定见 `apps/hvac-web/README.md` 与根目录 `DESIGN.md`。

## 快速验证命令（WSL 根目录执行）

```bash
npm -w @hvac/web run typecheck                                        # 前端 + Vite/Node 双 tsconfig 类型检查
npm run design:check                                                  # 设计系统、AST、Tarjan 无环 DAG 与行数门禁
node --experimental-strip-types --test scripts/test-real-<模块>.ts     # 单文件模型测试（如 test-real-gateway-client.ts）
npm run web:e2e -- e2e/<名称>.spec.ts                                 # 录制网关 Playwright 单 Spec（并行加 CI=1 PLAYWRIGHT_PORT=5194）
npm run verify:live -- --slug=<标识> --route=<路径>                   # 1440px 实库巡检截图（需 hvac-local 8443）
```

## 架构不变量与常见暗坑

- **状态单一归属**：URL `?site=` 走 `useWorkspaceScope()`；对象详情在页内侧栏通过 `?inspect=<id>` 打开，不设独立详情路由；服务端数据归 TanStack Query，表单归 React Hook Form + Zod，跨组件纯前端状态才用 Zustand（`nuqs` 与 `@dnd-kit/*` 已清理，勿重新引入）。
- **四层单向依赖**：`src/routes/` 仅负责路由解析、权限跳转与状态编排；业务逻辑、查询与领域组件放入 `src/features/<域>/`；请求统一走 `@/api/platform-client` 与 `gatewayRequest`，实时推送统一走 `src/platform/telemetry-live/`，保持模块无环 DAG（`design:check` 拦截）。
- **面向值班人员的真实数据**：无演示/假数据模式，未接入字段统一显示「未接入」；禁止暴露 UUID、技术枚举或 UCUM 原始单位码（如 `Cel`、`m3/h`），禁止单字模板拼接中文文案。

## 生成物与跨端联动

- **禁手改生成物**：`src/routeTree.gen.ts`（Vite 插件维护）与 `src/api/generated/*.gen.ts`（由根目录 `contracts:generate` / `telemetry:contracts:generate` / `operations-agent:contracts:generate` 生成）。
- **UI 组件新增**：统一执行 `npm run ui:shadcn -- add <组件>`；`src/styles/shadcn-tailwind.css` 须与 `SHADCN_CLI_VERSION` 一致。
- **共享域联动小程序**：修改 `src/domain/plantTopology.ts`、`src/domain/centralPlantTelemetry.ts`、`src/features/assistant/thread-model.ts`、`src/features/assistant/tool-labels.ts` 或 `src/lib/answer-markdown.ts` 后，必跑 `npm -w @hvac/miniprogram run sync`。
- **测试桩与验收地图**：改动接口请求/响应时同步更新 `e2e/fixtures/` 与 `e2e/gateway.ts`；新增 `e2e/*.spec.ts` 须在 `.agents/skills/verify/features/` 登记并更新索引；UI 改动须在 1440px 宽度下实看验收。
