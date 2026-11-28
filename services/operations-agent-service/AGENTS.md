# services/operations-agent-service/AGENTS.md

基于 pi（`@earendil-works/pi-agent-core`）的只读 AI 运维助手服务。本包**不在**根 npm `workspaces` 中，所有 npm 命令须在根目录带 `--prefix services/operations-agent-service` 执行。完整架构与评测基线见 `services/operations-agent-service/README.md`、`docs/specs/operations-assistant-v1.md` 与 `docs/adr/0014-pi-agent-runtime-architecture.md`。

## 快速验证命令（WSL 根目录执行）

```bash
npm --prefix services/operations-agent-service run check                    # 契约校验 + 双 tsconfig 类型检查 + 边界检查 + 全套单测
npm --prefix services/operations-agent-service run build && node --experimental-strip-types --test services/operations-agent-service/test/<名称>.test.ts
npm run operations-agent-service:postgres                                 # 临时 PostgreSQL 会话存储与租户 RLS 集成测试
npm run operations-agent:eval -- --only=<题号>                            # Tier-2 CLI 评测（凭据只走环境变量，不进命令行）
```

## 架构不变量与红线

- **只读工具与 mTLS 授权**：严禁新增任何写设备或控制类工具；每次读数必须通过平台网关双向 TLS 接口 `/internal/v1/operations/tool-authorization` 按提问人权限换票，边界检查硬性拦截直连 ClickHouse、Cube、ThingsBoard 或控制指令路径（`scripts/check-operations-agent-service-boundaries.mjs`）。
- **模块隔离与租户 RLS**：内部模块（`agent`、`application`、`runtime-pi`、`tools`、`persistence`、`transport-http`、`bootstrap`）仅通过各自 `index.ts` 互相引用；`agent` 与 `application` 严禁引入外部包或适配器；所有持久化读写必须经过 `persistence/internal/postgres-tenant-transaction.ts` 保持租户 RLS 隔离。
- **不可手改的生成物**：
  - `src/agent/internal/session-contract.gen.ts` → 修改 `contracts/http/operations-agent-sessions-public.openapi.json` 后跑 `npm run operations-agent:contracts:generate`；
  - `src/tools/internal/semantic-catalog.gen.ts` → 修改 `semantic/cube/model/` 后跑 `npm run semantic:catalog:generate`。
- **无状态与运行预算**：不持久化 pi 内部状态，每次新回答从已存消息与产出重建上下文，并严格受 `AgentRunBudget`（`src/bootstrap/main.ts`）约束；正式模型由 `AGENT_MODEL_*` 环境变量指定。
