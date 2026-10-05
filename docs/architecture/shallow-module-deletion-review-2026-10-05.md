# Shallow module / deletion test review

日期：2026-10-05。方法：Matt Pocock `codebase-design` 的 deletion test——删掉模块后，复杂度消失，还是转移到调用方？行数少、未部署、只有一个实现，都不是单独成立的删除理由。

初始审查重点覆盖 Go 组合入口、公共 façade、共享库，以及 Operations Agent 的模块入口；未核验仓库外调用方或实际部署，不代表整个前端或数据库已完成审计。用户随后批准实施，第 1–5 项已完成，执行结果见下文。之前的 skills 安装和 glossary 修改保持原状。

## 1. 优先删除：三个无调用的公开转发 helper

- `modules/energy/pkg/analyticsprojector/projector.go:38` 的 `DefaultHTTPClient()` 仅返回空配置 `http.Client`。源码中没有调用。真实入口 `cmd/telemetry-worker/main.go:355` 自行构造投影依赖及 HTTP/TLS 配置。
- `modules/iam/pkg/iamserver/server.go:36` 的 `NewS1FixtureAuthorizationStore()`。
- 同文件 `:40` 的 `NewDenyAllAuthorizationStore()`。

后两个仅转发到内部 IAM 实现，没有发现外部调用。内部函数仍在 `modules/iam/cmd/iam-owner/main.go:59,95` 和 `modules/iam/internal/iam/server.go:141` 使用。

**Deletion test：通过。** 删除这些公开 helper 不会把逻辑搬到调用方。删除范围只包括三个公开声明及由此不再需要的 import；保留内部 deny-all 行为和有效内部调用。实施后用对应 Go 包/调用入口编译确认，无需新增镜像式测试。

## 2. 优先收敛：Agent SSE 的双份实现

两个文件逐字节相同（SHA-256 均为 `03b73ac0db53903d681e4f85dc94f66845af336aa32563040d6ce0b7484ace1f`）：

- `services/operations-agent-service/src/transport-http/internal/agent-session-events.ts:21`
- `services/operations-agent-service/src/transport-events/internal/agent-session-events.ts:21`

但两个副本都在用：`bootstrap/internal/agent-session-runtime.ts:31` 从 transport-http 导入；`bootstrap/index.ts:23` 从 transport-events 导入。现有 `test/agent-session-event-stream.test.ts:5` 测试的是 transport-http 出口。该测试不能同时保证另一份副本永不漂移。

**Deletion test：通过，但需要改调用方导入。** 保留 transport-events 中唯一的事件编码/流实现，将 production runtime 和现有 SSE 测试导向这个入口，删除 transport-http 的副本及其 re-export。bootstrap 负责给 HTTP handler 注入事件流构造函数，已有这种装配方式；不要让两个 adapter 互相导入，也不要留下兼容转发出口。SSE 业务行为仍保留，消失的是第二份维护责任。

实施时复用现有 SSE 行为测试及 Agent 模块边界检查，重点确认订阅、终态结束、取消订阅和错误传播，没有必要新增永久 gate。

## 3. 删除候选：无消费者的 `libs/domainoutbox`

审查时 `libs/domainoutbox/store.go` 实现 claim、lease、retry、inbox/version 等 PostgreSQL 行为，**其实现并非浅模块**。但扫描 Go 源码和模块引用，未发现任何消费者；机器使用的登记包括 `go.work:17` 和 `contracts/architecture/backend-architecture.v2.json:46` 的 Audit ALIGNED 实现。历史源码评审文档也提到该库，不能将那些引用当作运行时消费者。

**Deletion test：在仓库内通过。** 删除整库及 workspace 注册不会向现有调用方转移逻辑；同时需纠正架构契约对实际实现的描述。它是孤立实现的删除候选，不是因为函数数量少。

**不能扩大为删除 domain outbox 表。** 事件表仍有真实生产写入：

- Registry：`modules/registry/internal/core/postgres_write_common.go:180`。
- Metric：`modules/metric/pkg/metric/postgres.go:379`。
- Forecast：`services/forecast-service/internal/forecast/postgres.go:175`。
- Optimization：`services/optimization-service/internal/optimization/postgres.go:163`。

库无调用不证明其对应数据契约无用，也不证明这些事件已具备端到端消费链路。实施前确认上述架构契约的实际所有者；删除后检查 workspace、架构检查和实际引用包，不添加新的替代基础设施。

## 4. 删除候选：只为旧测试服务的 Dispatcher 路径

`modules/command/pkg/commanddispatcher/dispatcher.go:20` 的旧 `Dispatcher` 只包装 PrepareDispatch → Connector.Execute → ResolveDispatch。`New()` 的调用仅见同包 `dispatcher_test.go:19,50,70,74`。唯一外部包导入位于 `cmd/connectivity/command_runtime.go`，生产入口 `:100` 构造的是 `NewDurable()`。

当前 durable 路径在 `durable.go:40` 保留真实 claim、动态安全验证、结果回写及 finalization 责任。

**Deletion test：旧实现通过；整个文件不能直接删除。** `dispatcher.go:15` 的 `Connector` 接口也被 DurableDispatcher 使用，应保留在当前实现附近。旧 `Dispatcher`、构造函数、仅供其使用的 `CommandStore` 可作为退休对象。

旧测试保护的三项行为仍有价值：provider ACK 不等于已验证成功、发送后超时为 outcome unknown、发送前失败允许重试且 fence 前进。先确认或补齐当前 durable/Command Service 接口上的最小行为测试，再删除旧测试和独占 fixture。不能用“只有测试调用”作为删除业务不变量的理由，也不能删掉整个 commanddispatcher 包。

## 5. 可删除的元数据：Agent 运行时模块描述图

`services/operations-agent-service/src/bootstrap/index.ts:107` 的 `bootstrapModule` 和 `:124` 的 `operationsAgentServiceModules` 汇集各模块的 `name/layer/dependencies` 常量。源码搜索未见运行时逻辑读取这个汇总；root index 只是再导出它。唯一测试引用 `scripts/test-operations-agent-service-boundaries.mjs:126` 是故意构造非法自包 import 的临时源文件，不是验证描述图的业务消费者。

真正执行边界检查的是 `scripts/check-operations-agent-service-boundaries.mjs:9,30` 的模块集合、允许依赖规则及实际 import 解析，未读取运行时描述图。例如检查器包含 runtime-pi，而汇总数组未包含它；两套列表并非同一事实来源。

**Deletion test：通过。** 可删除没有消费者的描述对象、专属类型和专为描述对象存在的 import，保留有真实行为的模块、公开业务出口及现有 import 边界检查。非法 self-import 测试应改为引用现存公开符号，继续保护原有边界。不要因为删除这些描述对象就合并所有目录或移除架构约束。

## 6. 真正的浅 façade：需先决定接口形状

以下公共包以内部类型 alias 和构造函数转发为主：

- `modules/energy/pkg/analyticsprojector/projector.go`
- `modules/iam/pkg/iamserver/server.go`
- `modules/registry/pkg/coreservice/server.go`
- `modules/audit/pkg/auditserver/server.go`
- `modules/telemetry/pkg/queryservice/server.go`

其中 energy 最明确：公开 ReaderConfig、WriterConfig、ProjectorConfig、BindingResolverConfig 和各构造函数，调用方 `cmd/telemetry-worker/main.go:355–415` 仍需了解并装配所有依赖。对调用者而言，这层没有隐藏装配复杂度。

**Deletion test：转发层本身几乎不承载复杂度；内部业务实现承载大量复杂度。** 这些 façade 也承担 Go `internal` 可见性桥接，外部 cmd 不能简单改为直接导入内部包。因此不是立即删目录的候选，更不能据此删 owner、安全校验或领域逻辑。

优先考虑 energy：让真正需要被外部使用的实现拥有一个直接公共接口，或把稳定的投影装配责任放进 energy 所有者，收缩外部暴露的内部 config。目标是调用方少学一套内部装配细节；若只是把转发函数搬到新目录，收益为零。不要为删 façade 再增加兼容 façade。

这是接口深化提议，尚未选定最终重构方案。涉及产品、授权、遥测、能源等实质架构行为的改变，实施前按仓库规则阅读已固定版本的 ThingsBoard/OpenEMS/MyEMS 相关源码、测试及文档，并记录 ADOPT/ADAPT/REJECT。本轮静态候选审查不替代该 source review。

## 暂不直接删除

`services/outbox-relay` 有真实的租约、broker acknowledgement、重试责任。当前 `cmd/energy-api/embedded_energy.go:233,242,262` 使用嵌入式 PostgreSQL audit 投递，但旧 Kafka relay 仍被以下入口使用：`scripts/s0-durable-topology.mjs:311`、`scripts/run-durable-session-browser-audit.mjs:332`、`scripts/run-s0-security-failure-gates.mjs:27`、`deploy/s0/staging/workloads/outbox-relay.yaml`。它是**旧拓扑整体退休候选**，不是无调用浅模块。需一起审查旧部署、脚本、契约和耐久行为后再决定；当前 sessionstore/sessionevent 及审计幂等能力要保留。

短但仍有价值的模块：

- `libs/schemagate/schemagate.go:25` 的 schema 检查保护真实数据库启动边界；最多讨论移入唯一所有者，不能消除检查行为。
- `modules/telemetry/internal/analytics/engine.go` 的接口连接 query 与 Cube adapter；让类型直接属于 query 会造成 adapter/query 依赖环风险，不能仅因文件很短删除。
- command transport state 和 maintenance retirement 检查保护持久关联及退休前置条件；删除会把复杂度或安全责任搬到调用方。

## 推荐执行顺序

先删三个无调用公开 helper；再收敛重复 SSE 和移除无消费模块描述对象；随后单独处理孤立 domainoutbox 和旧 Dispatcher。公共 façade 深化及 Kafka 旧拓扑退休各自形成独立、经过源码比对的改动，避免把低风险删除与 owner/安全/持久化重构混在一起。

## 用户批准后的实施结果

- 删除三个无调用公开 helper；IAM 内部 fixture/deny-all 实现及真实调用保留。
- SSE 唯一实现归 transport-events；production runtime 和现有 SSE 测试改用该入口；删除 transport-http 副本与旧出口。
- 删除 13 个模块的无消费描述对象、相关专属类型、import 及 root 汇总出口。保留所有业务模块和实际 import 边界检查规则。self-import 负向测试改用 `createAgentSessionService` 作为非法导入符号，仍验证同一违规行为。
- 删除 domainoutbox 的两个源文件及 workspace 登记；Audit 架构契约只登记真实的 modules/audit。数据库表、生产者、当前审计投递实现未改动。历史 source-review 中该库的代码引用属于删除前证据，可通过 Git 历史查阅，不再代表当前运行实现。
- 删除旧 Dispatcher 和独占 CommandStore。Connector 接口迁到 durable.go。原三个行为测试迁到 durable_test.go，以 test-only adapter 接入真实 Command Service，并通过 DurableDispatcher.RunOnce 执行；ACK、发送后超时、重试/fence 契约保留。

验证通过：Go helper 包编译；commanddispatcher、commandservice、cmd/connectivity、cmd/energy-api、cmd/telemetry-worker 的 `go test`；Agent TypeScript typecheck/build、四项现有 SSE/HTTP 测试、八项边界测试及现有模块边界检查；Backend Architecture V2 基线检查。无新 gate、无新依赖。

公共 façade 和 Kafka 旧拓扑未改动。没有执行数据库集成或实际部署验证；本次删除不改变数据库或外部传输协议。
