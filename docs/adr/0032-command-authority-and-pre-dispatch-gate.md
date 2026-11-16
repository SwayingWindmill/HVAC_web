# 0032 由控制服务统一掌管控制策略、两段闸门与急停旁路留痕

状态：已实现（2026-11-16）。

## 背景

此前控制域的核心规则分散在三个地方：

1. **平台网关**：`cmd/energy-api/internal/gateway/{command.go, control_exit.go}` 自己组装资产拓扑、查站点控制模式与检修窗口、查同伴设备联锁、算风险与 TOTP 校验，再发给 IAM 签发一次性指令授权；更严重的是 `degradedShutdownCommandState()` 在 `EMERGENCY_DO_RELEASE` 断采时伪造了一条 `GOOD` 质量、带占位修订号的运行状态证据，让审计账目分不清「现场读数确实满足前提」还是「断采时紧急强放」。
2. **派发器与接入层**：`modules/command/pkg/commanddispatcher/dynamic_safety.go` 与 `modules/connectivity/pkg/connectivity/command_route.go` 又各查一遍 `telemetry` 与 `core_registry.site_control_states_v1` / `device_control_locks_v1` 表来重算拦截，和网关里的规则各自演化，锁和检修窗口顺序也靠人肉保持一致。
3. **跨层顺序隐患**：`POST /api/v1/sites/{siteId}/control-shutdown` 和高风险 `/api/v1/commands` 若在检查权限/锁/联锁前先做两步验证或签发一次性 IAM `Grant`，会在肯定被拒的请求上误消费授权或先返回 `428`。

## 决定

1. **控制业务语义全部收拢到 `command-service` 内的 `ControlAuthority`（ADR 0032）**
   - `ControlAuthority` 通过三个显式端口与上游交互：`PlantTwinPort`（调 Core Registry 资产/模型/控制态及新内部端点 `GET /internal/v1/registry/commands/dispatch-context`）、`TelemetryLivePort`（读 Telemetry Runtime 最新在线读数 `GET /internal/v1/commands/reported-state`）、`IAMAuthorizerPort`（以 `spiffe://hvac.local/command-service` 身份申请只作用域在 `decision/registry` 与 `decision/command` 的单次授权）。
   - `POST /api/v1/commands` 家族与 `/api/v1/sites/{siteId}/control-{state,mode,shutdown}`、`/api/v1/assets/{assetId}/control-lock`（路由所有权第 49 版起全部划归 `command-service`）由平台网关在验完会话、CSRF、限流、边缘两步验证与严格 JSON 后，签发短时 `X-Command-Control-Context` 转给 `OperatorHTTPHandler`（`/internal/v1/operator/...`）。
2. **纯函数 `EvaluateControlGate` 统一提交前与派发前两道闸门，严守拒止与花销顺序**
   - `libs/commandmodel.EvaluateControlGate` 在提交阶段（`GatePhaseSubmit`）和出队列派发前阶段（`GatePhasePreDispatch`）复用同一套判定：禁止直接调用 `EMERGENCY_DO_RELEASE` 点位 → 站点控制模式 → 资产控制锁 → 站点检修窗口 → 目标设备遥测健康度 → 全部同伴设备联锁 → 高风险 `STEP_UP_REQUIRED`（`428`）。
   - 任何硬拦截（`403` / `409` / `503`）必须在 `428 STEP_UP_REQUIRED` 之前返回；`428` 必须在向 IAM 申请或消费一次性 `Command Grant` 之前返回。
   - `ControlAuthority.ClaimDispatch` 在事务外调用 Twin 与 Live 端口运行 `EvaluateControlGate(GatePhasePreDispatch)`；被终局安全条件拦下的指令按 `ConnectorPreSendRejected` 落账并向后扫最多 `3` 条同租户排队指令，瞬态依赖失败只回队列一次不做队头重试消耗。
3. **显式 `PreconditionMode` 取代假遥测伪造**
   - `modules/command/migrations/008_command_precondition_mode.sql` 为 `command_intents` 增加 `precondition_mode`（`REQUIRE_CURRENT_STATE` | `EMERGENCY_BYPASS`）和 `bypass_reason`，纳入规范化负载哈希与事件记录。
   - 删除 `degradedShutdownCommandState()`；仅在 `EMERGENCY_DO_RELEASE` 急停断采或读数不健康时，经显式确认后以 `EMERGENCY_BYPASS` 落库，不再写入虚假 `GOOD` 读数。
4. **清理重复实现**
   - 删除 `modules/command/pkg/commanddispatcher/dynamic_safety.go` 及对应测试；`DurableDispatcher.RunOnce` 信任 `ClaimDispatch` 已通过两段闸门。
   - 精简 `modules/connectivity/pkg/connectivity/command_route.go`，只保留网关绑定、点位通道元数据与网关证书检查，移除直连 Core Registry 锁表与联锁 SQL 分支。

## 不选的做法

- **继续在网关保留控制策略并仅补修 `degradedShutdownCommandState`**：网关本应只是认证、限流与边缘校验壳层，继续让网关持有站点控制拓扑和双闸门分叉逻辑，会导致前端/Bff 与派发器永远存在两套口径。
- **在派发器内直连 PostgreSQL `core_registry`**：派发器属 `connectivity` 进程，跨库读控制锁表违背单写方与Owner读取视图边界（ADR 0013 / ADR 0016）。

## 后果

- `command-service`（嵌入式随 `energy-api` 或独立容器 `command-owner`）运行期需要 `IAM_URL`、`CORE_URL`、`TELEMETRY_RUNTIME_URL` 与对应 SPIFFE 信任配置（`spiffe://hvac.local/command-service` 仅在 IAM 获准 `RegistryDecisionPath` 和 `CommandDecisionPath`）。
- 删除原有独立网关联锁测试文件 `control_interlock_test.go`，联锁与顺序不变量改由 `libs/commandmodel/control_gate_test.go`、`modules/command/pkg/commandservice/control_authority_test.go` 及端到端 `cmd/energy-api/internal/gateway/{command_test,control_exit_test}.go` 共同守住。
- 面向外部浏览器与小程序的公共 HTTP 契约（OpenAPI 字段、状态码、`STEP_UP_REQUIRED` 流程）保持 100% 兼容。
