# AI 助手服务（operations-agent-service）

平台网关后面的 AI 运维助手。值班人员就一个站点提问，服务用 pi（`@earendil-works/pi-agent-core` / `pi-ai`，ADR 0014）跑一个只读数据的助手，把问答记成一个助手会话。

```text
平台网关（登录会话、CSRF、站点可见性、限流）
  -> 双向 TLS -> transport-http   会话的接口、签好的提问人授权
  -> application                  会话的生命周期：新建、回答、取消、提交补充信息
  -> runtime-pi                   每次回答一个 pi 助手：提示词、模型、工具、预算
  -> tools                        只读工具（见下）
  -> persistence                  agent_operations：会话、回答、消息、工具调用、产出
```

每次回答开始时，服务先按提问人的权限读一次站点名称和时区，提示里写明站点和站点本地的当前时间，模型不用为此调工具。

工具：资产清单 `assets_list`、设备清单 `equipment_list`、当前读数 `telemetry_get_current`、历史读数 `telemetry_query_history`、能耗序列 `energy_query_series`、两段时间对比 `energy_compare_periods`、按设备拆分的用电 `energy_by_equipment`、语义查询 `energy_semantic_query`（不分组时可以同时要站点能耗和估算电费，工具分两个视图查、按时间段合成一张表）、告警列表 `alarm_list`、故障诊断结果 `fdd_findings`、未完成的工单 `work_order_list`。

每次读数据之前，服务调网关只接受双向 TLS 的工具授权接口（`/internal/v1/operations/tool-authorization`），用会话里的服务授权换一张这次读取专用的授权，所以工具永远读不到提问人看不到的东西。工具只读各数据负责方的接口；边界检查拒绝直连 ClickHouse、Cube、ThingsBoard 和指令接口。没有任何工具能操作设备。

一次回答以 `investigation_complete`（结论卡片和依据）或 `investigation_request_input`（向值班人员提一个问题）结束。每次回答受 `AgentRunBudget` 限制（模型调用次数、工具调用次数、时间、查询范围、结果大小、令牌数），正式环境的上限在 `src/bootstrap/main.ts`。pi 的内部状态不保存：每次新回答都从已保存的消息和产出重建对话；回答到一半中断的，记为失败。

会话的事件（`hvac.agent.event/v1`）通过 SSE 推送：快照、逐字输出的文字（`assistant.delta`）、工具开始和结束、产出、补充信息的提问、回答的结果。网页用的类型由 `scripts/generate-operations-agent-session-contracts.mjs` 生成。

评测题库和最近的结果在 `evals/`（`evals/README.md`、`evals/BASELINE.md`）。

## 模型

模型由环境变量决定，目前只接了 DeepSeek：

```text
AGENT_MODEL_PROVIDER=deepseek
AGENT_MODEL_ID=<确切的模型 id>
AGENT_MODEL_ALLOWLIST=<逗号分隔的确切 id>
AGENT_MODEL_THINKING_LEVEL=off|minimal|low|medium|high
AGENT_MODEL_TIMEOUT_MS=<100..120000，默认 30000>
AGENT_MODEL_MAX_OUTPUT_TOKENS=<默认 2048>
DEEPSEEK_API_KEY=<服务器上的密钥>
```

## 检查

在仓库根目录：

```bash
npm --prefix services/operations-agent-service run check     # 接口定义、类型检查、模块边界、测试（用假的模型）
npm run operations-agent-service:postgres                     # 会话存储，对着一个临时的 PostgreSQL
OPERATIONS_AGENT_OPERATIONS_MIGRATOR_DATABASE_URL=... \
  npm --prefix services/operations-agent-service run migrate:operations
```

模块之间只通过各自的 `index.ts` 引用；`agent` 和 `application` 不引用任何适配器或外部包（`scripts/check-operations-agent-service-boundaries.mjs` 检查）。
