# CLAUDE.md

商用 HVAC 智能节能平台，第一个场景是冷站。React SPA + Go 后端；第一阶段部署形态是单台 Linux 服务器 + Docker Compose。

`docs/` 里有大量历史文档，以代码为准，文档只作背景。AGENTS.md 是更详细的规则来源，本文件是它的精简版。

## 仓库结构

- `apps/hvac-web/`：唯一的 Web 入口（React 19、Vite、TanStack Router/Query、shadcn/ui）
- `cmd/`：可执行进程。`energy-api` 是平台网关，`telemetry-worker` 负责遥测运行时、实时推送和历史投影
- `modules/`：各领域的数据与逻辑 owner；`services/`：独立 workload；`libs/`：共享库
- `contracts/`：OpenAPI 与所有权契约；`infra/`：数据库迁移；`deploy/platform/phase1/`：Compose、迁移清单、镜像
- `tools/eg8200-simulator/`：冷站模拟器，本地实时数据的来源

## 命令

项目命令都在 WSL 里执行（`/mnt/e/Code/HVAC_web`），不用 Windows 的 node/npm/go。

```bash
npm -w @hvac/web run typecheck
npm -w @hvac/web run build
npm run web:e2e                                          # 前端浏览器测试：Playwright + 录制的网关数据，不需要后端
(cd modules/telemetry && go vet ./... && go test ./...)   # go.work 多模块，按模块执行
npm run deployment:phase1:check                          # 改了 compose 或迁移清单后
node scripts/run-s2-realtime-postgres-tests.mjs          # 遥测 Postgres 集成测试，使用独立临时库
```

- `routeTree.gen.ts` 由 Vite 插件生成；新增或移动 `routes/` 文件后，跑一次 dev 或 build 来更新它。
- 浏览器测试在 `apps/hvac-web/e2e/`，接口数据来自 `e2e/fixtures/`（从本地环境录制、已脱敏），写操作由 `e2e/gateway.ts` 按 owner 的规则模拟。页面改了接口调用后，要同步更新测试桩。
- 日志里的错误内容会被统一替换成 `[REDACTED]`；排查时直接调用对应的 Go 函数看真实错误。

## 本地环境

- 运行中的环境：Compose 项目 `hvac-phase1-local`，地址 https://localhost:9443，使用私有 CA。另有一套 `hvac-phase1-dev`，地址 https://localhost:8443。
- 前端开发服务器可以代理到 9443，需要设置三个环境变量：
  - `PLATFORM_GATEWAY_PROXY_TARGET=https://localhost:9443`
  - `NODE_EXTRA_CA_CERTS` 指向 `internal-pki/ca.crt`
  - `VITE_TLS_CERT` / `VITE_TLS_KEY` 指向 `tls/public.*`

  这几个文件都在 `deploy/platform/phase1/runtime/live-chain-20261001/` 下。WebSocket 推送只允许 9443 同源，所以开发服务器下只能看到快照。
- `deploy/platform/phase1/runtime/**` 里是密钥和凭据：不打印、不提交、不修改权限。
- git push 用 Windows 侧的 git（WSL 访问 GitHub 有 TLS 问题）；`gh` 命令用 WSL 里已登录的那份。

## 工程原则

- **不做向后兼容**：直接删除废弃路径，不加兼容层或回退。
- **从最简单的可用版本做起**：一层层往上加，每一步之后产品都还能用。
- **防御只针对真实存在的失败**：失败要明确暴露，不要静默回退。
- **测试只保护真实契约或出现过的回归**：契约变了就修改或删除旧测试。
- **不叠加检查**：用已有的最小检查覆盖一个不变量，不重复验证同一件事。
- **优先用已有依赖和成熟库**：参照外部实现时固定版本，并在 `docs/architecture/` 里简短记录采纳、改造或拒绝的理由。

## 数据库迁移

- 迁移文件在 `infra/*/postgres/init/` 和各模块的 `migrations/` 下。新增迁移要同时登记到 `deploy/platform/phase1/migrations/` 里的三个文件：`manifest.v1.json`、`migration-list.tsv`、`Dockerfile`。
- 正式上线前：迁移文件可以直接修改，开发库出现迁移漂移就重建（数据由模拟器重新产生）。迁移器按 sha256 校验，已有库遇到被改过的迁移会中止。
- 正式上线时冻结迁移：之后已执行的迁移一律不改，改表结构只能新增迁移，届时再加一条禁止修改已执行迁移的检查。

## 前端

- 页面只显示真实 owner 的数据；还没接入的，就明确显示"未接入"。示例数据只允许出现在 `HVAC_WEB_FRONTEND_REVIEW=true` 的评审构建里。
- 站点范围的页面放在 `routes/_app._site.*`，站点由 `?site=` 指定（用 `useWorkspaceScope()` 读取）；对象详情用页面内的侧栏打开，由 `?inspect=<id>` 指定，不另开详情路由。
- 组件不直接 fetch，统一走 OpenAPI 生成的客户端加 feature 内的 query。URL 状态归 Router 管，服务端状态归 Query 管。
- 面向运维人员的界面只显示业务事实，不显示 UUID、trace ID 或内部枚举。
- UI 改动要在 1440px 宽度下用真实数据实际看过，不能拿构建通过代替。

## Git

- `main` 是唯一主干，受保护，必须走 PR。每项改动开一个分支，用 Conventional Commits，提交说明里写清楚原因。
- 文本文件统一 LF（见 `.gitattributes`）；不提交构建产物和二进制。
