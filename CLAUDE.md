# CLAUDE.md

商用 HVAC 智能节能平台，第一个场景是冷站。React SPA + Go 后端；第一阶段部署形态是单台 Linux 服务器 + Docker Compose。

`docs/` 里带日期或名为 `*-source-review*` 的文档是当时的调研记录，只作背景；当前约定看 `CONTEXT.md`、`docs/adr/` 和 AGENTS.md 列出的权威文档，与代码冲突时以代码为准。AGENTS.md 是更详细的规则来源，本文件是它的精简版。

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
- 门禁只运行 `scripts/domain-task-matrix.mjs` 里登记的命令，集成脚本只跑它 `-run` 点名的 Go 测试。新的检查或测试要登记进去才会运行；没有门禁运行的检查会悄悄过时，删掉它。
- 日志里的 `error` 字段统一显示为 `[REDACTED]`。要看真实错误，写一个直接调用该 Go 函数的测试，或临时把错误记在带 `[DEBUG-xxxx]` 前缀的字段里，查完删掉。
- 从 Windows 调 WSL 时，多行或带嵌套引号的命令先写成脚本文件，再用 `wsl -e bash -lc "bash <脚本路径>"` 执行；`wsl` 偶发 `WSAETIMEDOUT` 时重试即可。

## 本地环境

- 本地环境只有一套：Compose 项目 `hvac-local`，地址 https://localhost:8443，使用私有 CA，包含设备接入、模拟器和智能层（`single-lite` 档位）。
  - `npm run local:up`：从当前代码构建，并按顺序完成迁移、身份初始化、种子数据和启动；可以重复执行。
  - `npm run local:down` 停止，`npm run local:ps` 查看状态；`npm run local:reset` 删除容器、数据卷和模拟器（网关）状态，之后 `local:up` 从零重建。
  - 运行时文件（证书、密钥、凭据、配置）在 `deploy/platform/phase1/runtime/local/`，管理员口令在其中的 `local-admin.credentials`。服务证书在 `internal-pki/<身份>/tls.{crt,key}`，CA 是 `internal-pki/ca.crt`；`data/eg8200*` 是两个模拟器的命令台账和发件箱。
- 一个服务的配置按这个顺序找：运行时环境文件 → `scripts/phase1-wsl-compose.mjs`（注入数据库连接串等）→ `deploy/platform/phase1/compose.yaml` 和 `wsl.override.yaml` → 服务代码里 `envOr` 的默认值。
- 要用某个服务的身份做临时探测：`docker run --rm --network hvac-local_application --volumes-from <服务容器> ...`，证书路径与该服务内一致。
- 前端开发服务器可以代理到 8443，需要设置三个环境变量：
  - `PLATFORM_GATEWAY_PROXY_TARGET=https://localhost:8443`
  - `NODE_EXTRA_CA_CERTS` 指向 `runtime/local/internal-pki/ca.crt`
  - `VITE_TLS_CERT` / `VITE_TLS_KEY` 指向 `runtime/local/tls/public.*`

  WebSocket 推送只允许 8443 同源，所以开发服务器下只能看到快照。
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

- `main` 是唯一主干，只通过 PR 合并，不直接推送。每项改动开一个分支，用 Conventional Commits，提交说明里写清楚原因。
- 文本文件统一 LF（见 `.gitattributes`）；不提交构建产物和二进制。

## Agent skills

### Issue tracker

需求、PRD 和任务都记在 GitHub Issues，用 `gh` 操作。见 `docs/agents/issue-tracker.md`。

### Triage labels

使用五个默认分诊标签（`needs-triage`、`needs-info`、`ready-for-agent`、`ready-for-human`、`wontfix`）。见 `docs/agents/triage-labels.md`。

### Domain docs

单一上下文：根目录 `CONTEXT.md` 加 `docs/adr/`。见 `docs/agents/domain.md`。
