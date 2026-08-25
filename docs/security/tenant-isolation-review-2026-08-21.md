# 多租户隔离现状核查报告

> 日期：2026-08-21
> 范围：PostgreSQL RLS 覆盖、capability 鉴权与租户过滤、审计账本闭环
> 方法：静态代码核查（未做运行时渗透测试）
> 结论先行：**代码层面隔离设计严谨、fail-closed，未发现明显越权漏洞**；但存在若干"静态审计看不见、需动态验证"的边界，见文末风险点。

---

## 1. RLS 覆盖核查

逐库统计 `ENABLE/FORCE ROW LEVEL SECURITY`、`CREATE POLICY` 与建表文件的对应关系（仅当前分支，排除 worktrees 与 clickhouse init）：

| 库 | 建表文件 | 开 RLS 文件 | 结论 |
|---|---|---|---|
| s1-registry | 26 | 26 | 全覆盖 |
| s2-telemetry（PG） | 6 | 6 | 全覆盖 |
| alarm-service | 6 | 6 | 全覆盖 |
| command-service | 2 | 2 | 全覆盖 |
| work-order-service | 2 | 2 | 全覆盖 |
| notification / outbound-delivery / rule-runtime | 1+1+2 | 1+1+2 | 全覆盖 |
| connectivity | 2 | 2 | 全覆盖 |
| s0-durable | 1 | 1 | 全覆盖 |
| **identity** | 2 | **0** | **合理例外（见下）** |

**identity 库未开 RLS 属合理设计，非漏洞**：`identity.users` / `authorization_requests` / `authorization_codes` / `user_mfa` 等表**不含 `tenant_id` 字段**（用户是平台全局身份，通过 capability 访问 site），隔离依赖 `REVOKE ALL ... FROM PUBLIC` + 精确 role 授权（`identity_runtime` / `identity_admin` / `identity_directory_reader`），且仅 identity 服务经 mTLS 访问。

**RLS policy 语义抽查**（`services/alarm-service/migrations/001_s4_alarm_runtime.sql`）：标准模式为
```sql
CREATE POLICY ... USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
```
即按会话变量 `app.tenant_id` 过滤，未设置时返回 NULL → 恒不匹配 → **fail-closed**。

---

## 2. capability 鉴权与租户过滤核查

### 2.1 伪造身份 header 防护（网关边界）
`services/platform-gateway/internal/gateway/server.go` 在路由入口显式拒绝调用方自带的 `X-Principal` / `X-Tenant-ID` / `X-Site-ID` / `X-Admin` / `X-Delegation-Grant` / 各域 `X-*-Context` / `X-*-Grant` 等 20+ 个身份 header，命中即返回 `FORGED_IDENTITY_HEADER`。**外部调用方无法通过伪造 header 冒充租户或提权。**

### 2.2 租户/站点从授权结果派生，不信任请求参数
- 每域独立授权函数：`authorizeTelemetry`、`authorizeAnalytics`、`authorizeAnalyticsForPresenter` 等，返回 `authorizedTarget.TenantID / SiteID`。
- 请求里的 deviceID 被映射到授权范围：`device_history.go` 中 `selection.Complete(authorizedTarget.TenantID, authorizedTarget.SiteID)`。
- 双重校验：`telemetry.go:509` 校验授权目标（`authorized.DeviceID == target.DeviceID` 且 tenant/site 为合法 UUIDv7）；`telemetry.go:682` 再校验读回快照的 tenant/site 与授权一致。

### 2.3 RLS 会话变量来源可信
各服务在事务内 `SET LOCAL ROLE <受限 role>` + `SELECT set_config('app.tenant_id', $1, true)`，`$1` 来源为 **claims.TenantID（身份声明）而非请求参数**（`platform-core-service/internal/core/postgres_write_common.go`、`libs/domainoutbox/store.go`、`services/settlement-service/internal/settlement/postgres.go` 等）。

### 2.4 越权回归测试已存在
`platform-gateway/internal/gateway/analytics_test.go: TestGatewayEnergySeriesRejectsUnauthorizedSite`、`auth_integration_test.go`（IAM capability 精确传输）、`principal_capability_contract_test.go`（capability 版本契约一致）等。

---

## 3. 审计账本闭环核查

- **哈希链防篡改**：`audit-ledger-service/internal/audit/store.go` 每条记录存 `PreviousRecordHash`，`recordHash = hashRecord(previousHash, payload)`，逐条链式，篡改任一条会破坏后续所有 hash。
- **审计先于执行（fail-closed）**：`server.go: applyRouteOwnership` 中每个请求写 `ROUTE_DECIDED` 审计（含 TenantID / Subject / Issuer / SPIFFE / TraceID），**`routeAudit.Record` 失败直接返回 503，请求不执行**。
- 审计覆盖：路由决策、session、command、alarm 等关键操作，`audit.go` 额外记录 forbidden/unauthorized 事件。

---

## 4. 风险点（静态审计的盲区，需动态验证）

以下不是"已发现的漏洞"，而是"静态看代码看不到、需要运行时/渗透验证"的边界，建议按序补查：

1. **operations-agent / AI 副驾授权通道**：`X-Operations-Registry-Site-Grant` 等工具授权 header 有防护，但其授权决策链路（工具级授权 vs site 级 capability）未在本轮逐项展开，需单独审计。
2. **centrifugo 实时订阅的服务端授权**：前端 telemetry-live 有"连接 capability"配额，但**服务端 centrifugo 订阅某个 site 遥测流的授权校验**是否严格，本轮未核查（重点：跨 site 订阅是否被拒）。
3. **telemetry-workload 特殊通道**：`server.go:199` 对"已验真的 telemetry workload"放行 `X-Tenant-ID`（设备上报通道），需确认该 workload 的 mTLS 身份验证足够严格，且不能冒充其他租户上报。
4. **静态审计的固有局限**：本轮只做了代码核查，未做实际攻击模拟。建议补若干针对性越权测试（伪造 tenant、跨 site 查询、未授权订阅）作为 CI 门禁。

---

## 5. 结论与建议

**结论**：该项目的多租户隔离不是"有洞"，而是**三层纵深、fail-closed 的严谨设计**——RLS 全覆盖 + capability 鉴权（伪造 header 防护 + tenant/site 从授权派生 + 双重校验）+ 审计先于执行（hash 链防篡改）。这套体系在同类 Go 微服务里属于少见的高标准，**它就是你要的 SaaS 护城河的底座，已经建好了**。

**建议（按序）**：
1. 补 3 个针对性越权测试（伪造 tenant / 跨 site 查询 / 未授权 centrifugo 订阅）纳入 CI，把"静态严谨"变成"可回归验证"。
2. 专项审计 operations-agent 工具授权 + centrifugo 服务端订阅授权两条边链路。
3. 隔离层**不需要重做**，把火力转向下一优先级：租户配额/限流（D10 P0 未完成项）。
