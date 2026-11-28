# 0034 全栈架构收敛：单一权威实现、深模块边界与棘轮防回归门禁

状态：已实现（2026-11-28）。

## 背景

随功能快速迭代（站点接入、遥测管线、指令治理、告警通知、AI 运维助手与数据分析），代码库一度积累出多处平行实现与隐性熵增：Go 工作区分裂出 10 个子模块，各领域各自实现 UUIDv7 生成、`ClickHouse` HTTP 查询与服务启动壳；AI 助手会话契约跨前端、Node 助手与 Go 网关三端手工同步；网页端出现超 600 行巨型文件与跨层反向依赖；构建/集成测试脚本中 `.mjs` 与 `.ts` 混用并各自复制 TCP 端口探测与 `go.work` 解析；部分 Playwright 端到端用例在默认测试运行中无条件向主仓库写截图，既污染并发工作树，又在大并发下拖慢关键交互断言。

按照「假设今天是产品诞生的第一天，在已知全部真实需求与约束下向理想形态安全收敛」的原则，平台分 9 个波次完成全栈架构收敛，并把每一条已确立的架构规范固化为机器可执行的单向/双向棘轮门禁。

## 决定

1. **后端单一工作区与深模块底座（Single Source of Truth & Deep Modules）**
   - **`go.work` 唯一双模块边界**：工作区收拢为 `.`（根 Go 模块，统一承载服务、领域模块、公共库与模拟器）与 `./cmd/wecom-alert-adapter`（独立无依赖出口适配器），由 `scripts/check-repository-governance.ts` 禁止新增并行子模块。
   - **公共基础设施深模块**：将跨领域重复逻辑收拢进 `libs/` 权威实现，包括 `libs/uuidv7`（全仓唯一 UUIDv7 生成入口，并在 `scripts/check-ownership-boundaries.ts` 通过 `localUUIDv7WrapperPattern` 禁止各包自建 `newUUIDv7` 包装函数）、`libs/servicehost`（统一服务宿主启动、健康探针与优雅退出）、`libs/clickhousehttp`、`libs/schemagate`、`libs/grant`、`libs/registryauth`、`libs/edgetunnel` 与 `libs/edgecontrol`。
   - **单写者数据库边界与 1-Source-3-End 契约**：保留跨领域只读视图（`*_v1`）与受保护行级安全（`FORCE RLS`，含 `agent_operations` 会话库）；平台公共 API、S2 遥测与 AI 运维助手会话契约均通过单份源契约生成三端类型与服务端绑定（`contracts:generate`、`telemetry:contracts:generate`、`operations-agent:contracts:generate`、`semantic:catalog:generate`）。

2. **前端严格 4 层零环有向无环图与类型收口**
   - **单向分层 DAG**：`apps/hvac-web/src` 严格执行四层单向依赖（基础层 `lib`/`domain`/`api`/`platform` → 原语与区块层 `components/(ui|data-table|assistant-ui)`、`blocks` → 业务域层 `features` → 路由层 `routes`，并禁止跨层反向或越界引入 `app/`），由 `scripts/check-design-system.ts` 使用 Tarjan 强连通分量检测零循环与零越层导入。
   - **唯一网关请求入口与规模上限**：所有网页端接口调用统一收敛到 `@/api/gateway-client`（`gatewayRequest`）与 `@/api/platform-client`（`platformGatewayClient`），清理 `features/**` 下全部非 `const` 类型断言 `as`，并通过 `scripts/web-file-lines-baseline.json` 守住单文件不超过 `600` 行（存量豁免清空为 `legacyFileCaps: {}`）与 `scripts/web-as-cast-baseline.json`（`maxNonConstAsCasts: 21`）双向棘轮。
   - **E2E 验证截图按需触发且工作树隔离**：`apps/hvac-web/e2e/test.ts` 提供 `captureVerifyScreenshot`，仅当显式设置 `HVAC_E2E_SCREENSHOTS=1` 时向当前工作树 `.scratch/verify/...` 写图；删除各规格文件内的硬编码主仓路径与重复截图辅助函数。

3. **仓库工程脚本全面 TypeScript 化与零松弛棘轮**
   - **严格 TypeScript 工具链**：除服务于 Phase 1 部署/恢复/站点导入/Nginx Dockerfile 运行时检查，以及本地环境启动、工作负载签发、中心站模拟与验收台、站点重算、FDD 规则发布与本地浏览器辅助的 `24` 个受限 `.mjs` 文件外，其余工程校验、代码生成、Git 钩子与容器集成测试运行器全部迁移至 `.ts`（以 `node --experimental-strip-types` 运行，并通过 `tsc -p tsconfig.scripts.json --noEmit` 强校验）。
   - **消除脚本重复实现**：`scripts/run-go.ts` 复用 `scripts/lib/go-workspace.ts` 的 `readGoWorkspace`；所有容器集成运行器与 Phase 1 夹具（`scripts/lib/phase1-postgres-fixture.ts`）统一复用 `scripts/lib/postgres-compose-harness.ts` 的 `findAvailablePort`，删除各脚本私有的端口探活拷贝与失效 `go-workspace.d.mts` 声明。
   - **双向零松弛基线**：`scripts/javascript-tooling-baseline.json` 将脚本遗留 JS 文件数与字节上限从 `47` 个 / `747,562` 字节收紧并锁定至 `24` 个 / `355,944` 字节（同时拒绝增长与未同步基线的滞后松弛）。

4. **机器可执行防回归门禁总览（Enforceable Architecture）**
   - `npm run repo:check`：校验 `go1.27.4` 的 `gofmt`、`go.work` 双模块清单、`.agents/skills` ↔ `.claude/skills` 逐字节镜像、Compose 回环端口绑定、`docs/HANDOFF.md` 字节预算、JavaScript 遗留棘轮、`go-file-lines-baseline.json`（新增文件 `<= 800` 行）、`go-test-sleep-baseline.json`（测试休眠 `<= 55` 处）及 `changelog.d` 规范。
   - `npm run ownership:check`：校验数据库单写者归属、Go 纵向分层 DAG、高风险服务禁用裸 `map[string]any` 以及全仓禁用局部 `newUUIDv7` 包装。
   - `npm run design:check`：校验 Web 4 层零环 DAG、shadcn/组件原语规范、`shadcn-cli.ts` 样式版本一致、单文件 `<= 600` 行及 `as` 断言棘轮。
   - 专项静态与形式化门禁：`npm run typecheck:scripts`、`npm run clickhouse:sql:check`、`npm run command:formal`、`npm run operations-agent-service:check`（内含 `boundaries` 边界检查）、`npm run go:lint`、`npm run security:go-vuln`。

## 不选的做法

- **强行一刀切迁移部署期与现场/本地重型 `.mjs` 脚本或强改兼容契约**：`deploy/platform/phase1/cd/*`、`scripts/phase1-wsl-compose.mjs`、`scripts/check-phase1-deployment.mjs`、`scripts/verify-phase1-recovery-drill.mjs`、`scripts/site-onboarding.mjs`、`scripts/thingsboard-history-import.mjs`、`deploy/platform/phase1/nginx/Dockerfile` 内使用的 `scripts/check-web-js-runtime.mjs`，以及与之耦合的本地环境/身份签发/中心站模拟与历史重放等合计 `24` 个 `.mjs` 脚本直接关联宿主机、容器运行时与运维手册；同时网关对外错误信封及小程序本地项目配置需与发布/双端演进协同窗口联动。上述明确登记的保留边界不在无窗口时硬改，而是通过棘轮基线锁定范围、后续安全渐进收敛。
- **靠注释豁免或放宽基线绕过门禁**：所有 `scripts/*-baseline.json` 基线只降不升，任何测试禁用或棘轮松弛均视为架构退化。

## 后果

- 新增业务代码与脚本默认落在单一权威位置，跨层耦合与重复实现在静态校验与 PR 门禁阶段即被阻断；多 Agent 可在隔离 worktree 中并行推进而不互相覆盖 `.scratch/verify` 或破坏全局契约。
- 后续若需清理上述保留边界（例如部署期与现场 `.mjs` 脚本或网关兼容双错误信封），在确认对应运维手册、容器镜像与小程序端同步就绪后按同一模式收敛并同步下调 `scripts/javascript-tooling-baseline.json`。
