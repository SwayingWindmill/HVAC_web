# AGENTS.md

商用 HVAC 智能节能平台（React SPA + 微信小程序 + Go 后端 + Node AI 助手 + Python 求解器），Phase 1 单机 Docker Compose 部署。规则与代码冲突时以代码为准；涉及本项目业务名词、架构、设计或运维约定时优先检索本地文档而非依赖预训练习惯。

## 本地文档索引

| 路径 | 何时先读 |
| --- | --- |
| `GLOSSARY.md`、`docs/adr/` | 改动业务领域代码前，对齐业务名词与架构决策 |
| `docs/specs/`、`PRODUCT.md` | 核对功能边界、验收标准与产品安全红线 |
| `DESIGN.md`、`apps/hvac-web/AGENTS.md` | 改动网页 UI、路由、状态或跑浏览器/1440px 验收前 |
| `apps/hvac-miniprogram/AGENTS.md` | 改动微信小程序、审核隔离页或共享域模型前 |
| `services/operations-agent-service/AGENTS.md` | 改动 AI 助手工具、会话、租户 RLS 或 `evals/` 前 |
| `deploy/platform/phase1/README.md`、`docs/agents/local-environment.md` | 改动 Compose、PostgreSQL/ClickHouse 迁移或本地 `hvac-local` 环境前 |
| `docs/HANDOFF.md` | 仅查生产服务器（`139.199.58.47`）、松雅轩现场点位与本地环境偏差（不作任务队列） |
| `docs/agents/issue-tracker.md`、`docs/agents/issue-delivery.md`、`.agents/skills/poteto-mode/SKILL.md`、`.agents/skills/hao-mode/SKILL.md` | 认领 GitHub Issues、断点续跑与标准交付流 |

## 双平台分工与快速验证命令

- **Windows 侧**：代码定位先用 `codegraph_explore`（见 `docs/agents/codegraph.md`）；用 `read_file` 并行读切片（`150–250` 行）；查历史与全文直接跑单条 `git` 命令（不拼管道/`&&`/`||`）；推送用 Windows `git push`（WSL 访问 GitHub 有 TLS 问题）。
- **WSL 侧（`/mnt/e/Code/HVAC_web`）**：执行 Node/npm、Go、Docker、Playwright 与 `gh`；`apps/hvac-web` 拒绝 Windows Node；多条命令先写入临时脚本再跑单条 `wsl -e bash -lc "bash <脚本路径>"`（遇 `WSAETIMEDOUT` 重试）。

```bash
# 秒级单文件 / 局部验证（WSL 执行）
go test -run <TestName> ./modules/<域>/...                                 # 根 Go 模块（含 cmd/modules/services/libs/tools）
node scripts/run-go.mjs test -run <TestName> ./cmd/wecom-alert-adapter/...  # go.work 唯一独立子模块
node --experimental-strip-types --test scripts/test-<名称>.ts                # Web 模型/壳层/治理脚本单测（也支持 .mjs）
npm run web:e2e -- e2e/<名称>.spec.ts                                       # 录制网关单 Spec（并行/worktree 加 CI=1 PLAYWRIGHT_PORT=5194）
npm -w @hvac/miniprogram run test                                           # 小程序双类型检查 + 共享同步校验 + 全套单测
npm --prefix services/operations-agent-service run check                    # AI 助手契约 + 类型 + 模块边界 + 全套单测
PYTHONPATH=services/ai-solver-service python3 -m unittest services/ai-solver-service/tests/test_solver.py  # Python 求解器单文件测试

# 提交前门禁与实库验收（WSL 执行）
npm run check                                                               # 静态治理 + 脚本类型 + 设计 AST/DAG + Web 构建 + 小程序检查
npm run preflight -- --skip-e2e                                             # 按改动文件自动跑对应 contracts + unit（支持 --dry-run）
npm run domain:run -- --domain=<域>                                         # 按 scripts/domain-task-matrix.ts 跑指定域
npm run command:formal                                                      # 改控制指令/MQTT/设备围栏必跑形式化检验
npm run go:lint && npm run security:go-vuln                                 # 改 Go 代码或依赖必跑
npm run verify:doctor                                                       # 检查 WSL Node、Chromium 与 5190/5194/8443 端口
npm run verify:live -- --slug=<标识> --route=<路径>                         # Tier-2 实库 1440px 巡检与截图（无 8443 返回退出码 2）
npm run operations-agent:eval -- --only=<题号>                              # Tier-2 AI 助手 CLI 评测（凭据只走环境变量）
```

- PR 门禁会对 Go 单测启用 `-race`；WSL 缺少 `gcc` 时，用 `golang:1.27.4-bookworm` 容器复现（见 `docs/HANDOFF.md`）。

## 架构不变量与生成物红线

- **Go 模块与分层**：`go.work` 仅保留 `.` 与 `cmd/wecom-alert-adapter`（禁新建并行子模块）；`repo:check` 校验 `go1.27.4` `gofmt`；`ownership:check` 守住单写者表归属（`contracts/ownership/data-ownership.v1.json`）、Go 纵向分层（`libs` 不上引、`modules`/`services` 不引 `cmd`/`tools`、`cmd/edge-gateway` 非测试与 `tools/*` 不引 `modules`/`services`）及规范 `libs/uuidv7`。
- **网关路由白名单**：网关仅放行 `contracts/ownership/route-ownership.v1.json` 登记的路由；未进 OpenAPI 契约的新增路由必须手工补登，集成测试不会替你拦截。
- **迁移双向冻结与 ClickHouse**：PostgreSQL（`migration-list.tsv`）与 ClickHouse（`clickhouse-migration-list.tsv`）已登记 SQL 一字节不可改，改表结构只能新增业务主题命名迁移（PG 登记进 `manifest.v1.json` 后跑 `npm run migrations:sync`）；ClickHouse 查询须匹配最小权限角色并遵守 `clickhouse:sql:check`。
- **不可手改的生成物与同步镜像（改源头后执行对应命令）**：
  - `npm run contracts:generate` → `cmd/energy-api/internal/platformapi/api.gen.go`、`apps/hvac-web/src/api/generated/platformGateway.gen.ts`
  - `npm run telemetry:contracts:generate` → `modules/telemetry/pkg/telemetryapi/api.gen.go`、`apps/hvac-web/src/api/generated/s2Telemetry.gen.ts`
  - `npm run operations-agent:contracts:generate` → `apps/hvac-web/src/api/generated/operationsAgentSessions.gen.ts`、`services/operations-agent-service/src/agent/internal/session-contract.gen.ts`、`cmd/energy-api/internal/gateway/operations_agent_contract_gen.go`
  - `npm run semantic:catalog:generate` → `libs/analyticsmodel/semantic_catalog.json`、`services/operations-agent-service/src/tools/internal/semantic-catalog.gen.ts`
  - `npm run migrations:sync` → `deploy/platform/phase1/migrations/{migration-list.tsv,Dockerfile}`、`infra/registry/compose.yaml`
  - `npm -w @hvac/miniprogram run sync` → `apps/hvac-miniprogram/miniprogram/shared/*`
  - `npm run eg8200:config:generate` → `tools/eg8200-simulator/configs/central-plant.local.json`
  - 自动/镜像维护：`apps/hvac-web/src/routeTree.gen.ts`（Vite 插件生成）；`.agents/skills/**` ↔ `.claude/skills/**`（逐字节 1:1 镜像，`repo:check` 强校验）。

## 编码、防过度设计与核实准则

- **最小外科手术改动**：只做目标需要的变更，功能替换时直接删掉旧路径，不留兼容层、双写或废弃开关；优先复用仓库既有依赖与工具（借鉴外部实现时先读固定版本源码并简记于 `docs/architecture/`）；不确定需求边界或核心方案分叉时先沟通，不静默假设或过度抽象。
- **精准防御与测试**：校验、重试与兜底仅放在信任边界、外部 I/O、并发、数据损坏与控制安全处，失败直接报错；测试只守契约、权限边界、状态机与历史回归，约定变更同步改/删旧测试。
- **全栈接通自查**：新增后端能力须接到 `cmd/` 真实主链路与配置；改接口或共享域模型时同步核对 `apps/hvac-web` 与 `apps/hvac-miniprogram` 是否需联动，并更新 `apps/hvac-web/e2e/fixtures/` 与 `e2e/gateway.ts`。
- **日志、固化与文档**：运行日志 `error` 字段固定脱敏为 `[REDACTED]`，排查优先写局部复现测试（临时 `[DEBUG-*]` 提交前清零）；同一错误或纠错出现两次走 `/correct` 固化为架构/类型/门禁；文档用中文大白话 + `GLOSSARY.md`，用户可见变更写 `changelog.d/YYYY-MM-DD-<slug>.md`（汇总用 `npm run changelog:release`）。

## 三层操作边界

### Always（直接闭环执行）
- 只读侦察、本地构建/单测/形式化检验、运行代码生成与同步脚本、修复 CI 失败。
- 特性分支验证通过后直接 Windows `git push` + WSL `gh pr create` + `gh pr checks --watch`，CI 全绿即自动 `gh pr merge --squash --delete-branch`（用户显式要求保留或堆叠 PR 除外）。

### Ask first（必须先征得确认）
- 触发生产发布（`deploy-production.yml`、服务器 `139.199.58.47` 或松雅轩现场 EG8200 实操）。
- 重置或销毁数据（`npm run local:reset`、`docker compose down -v`、删库删表）。
- 对外发消息（企业微信/微信/邮件/公开渠道），或改变产品核心方向与现场点位基线。

### Never（绝对禁止）
- 读取、打印、提交或改权限受保护密钥：`deploy/platform/phase1/runtime/**`、`deploy/platform/phase1/environments/*.runtime.env`、`deploy/platform/phase1/observability/{wecom-webhook-url,smtp.env,smtp-password}`。
- 手改上述生成物、同步镜像或已冻结的 PostgreSQL/ClickHouse 迁移 SQL。
- 直推 `main`、`git push --force`、`git reset --hard`、`git clean -f`、`git checkout .`；在业务 PR 直改 `CHANGELOG.md` `## 未发布` 正文。
- 靠删测试、加忽略注释或调松棘轮基线（`scripts/*-baseline.json`）换取绿灯；在 `.agents/` 新增 JS 或破坏 `.agents/skills` ↔ `.claude/skills` 镜像。

## 多会话协作与交付流程

- **多会话防撞**：改代码前先查 `git status` 与当前分支；主工作区脏或不在 `main` 时切独立 worktree 作业，合并后清理临时 worktree。
- **流程与子 Agent**：任务按 `pstack`（`.agents/skills/poteto-mode/SKILL.md`）与 `hao-mode` 推进；派发任何子 Agent 提示词首行必须写 `Read <abs-path-to-poteto-mode>/worker.md before anything else.`。
- **文本与产物**：文本文件统一 LF（见 `.gitattributes`），构建产物与二进制留在仓库外。
