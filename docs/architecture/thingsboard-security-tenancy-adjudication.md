# ThingsBoard CE 租户、身份、权限与审计裁决

状态：`D01_ADJUDICATION_COMPLETE`

审查票：[审查租户、身份、权限与审计](https://github.com/SwayingWindmill/HVAC_web/issues/238)

本文只裁决 ThingsBoard CE v4.3.1.1 的租户、客户、用户、认证、授权、API 凭据、资源不可见性、审计与使用限制。它不把 ThingsBoard 或 HVAC Web 预设为正确实现，也不授权在本审查票内直接改变产品行为。

## 1. 结论

HVAC Web 不应恢复 ThingsBoard 运行时依赖，也不应复制其固定三角色、浏览器 URL 令牌、明文 API Key、宽权限 Personal Access Token、宽松 JIT Tenant Admin 或 best-effort 审计实现。

当前 HVAC Web 在以下基础边界上更适合 HVAC 控制场景，应保留：

- OIDC Authorization Code + PKCE 的 BFF Session，浏览器不持有 Access Token 或 Refresh Token；
- `issuer + subject` 不可变外部身份与平台 IAM Principal 分离；
- Tenant Membership、精确 Site Scope、Domain Action、显式 Deny 和有效期共同参与授权；
- mTLS/SPIFFE 工作负载身份和短时、单 Audience、单 Action、精确 Scope 的 Delegation Grant；
- 公开 Registry 读取将不可见资源和无权读取统一为 404；
- Session 状态、Audit Intent 和 Outbox 在同一 PostgreSQL 事务内提交，Audit Ledger 以幂等方式消费。

但这些底层基础不能证明当前 D01 已经完整。源码反向审查确认存在以下实质缺口：

1. Phase 1 自建 Identity Provider 只有最小密码登录，没有 MFA、Passkey、用户激活、用户自助恢复、Step-up Authentication 或完整安全事件审计。
2. `identity-service` 的登录、锁定、管理员建号和密码重置没有进入耐久 Audit Ledger；当前耐久审计从 Gateway Session 创建后才开始。
3. 当前 OIDC Session 通过 Token Claim 或 `OIDC_DEFAULT_TENANT_ID` 固定一个 Tenant，没有已授权 Tenant 的选择、切换、Session Context 轮换和切换审计，不能满足完整多租户 UX。
4. 只有离线 CLI 建号/重置密码和离线 IAM Reconciliation，没有产品级 Tenant、Principal、Membership、Role、Site Binding、Explicit Deny 管理 API 与 UI。
5. 没有外部自动化所需的最小权限、可过期、可撤销、哈希存储 API Credential。内部机器身份的 mTLS/SPIFFE 不能直接替代外部 API Credential 产品能力。
6. 没有统一 Tenant Policy、Quota、Usage 和超限状态模型；现有限制散落在各 Domain 配置和契约中。
7. 没有 Tenant 退役/删除的耐久状态机、影响清单、Tombstone、清理进度和重试证据。
8. Audit Ledger 查询只覆盖窄入口或按 Message ID 读取，缺少跨 Domain 的统一搜索投影、分类、筛选和保留策略。
9. 公开契约规定 Roles 仅是展示/审计上下文，但 Session 撤销与 Audit 查询仍硬编码 `platform-admin` / `audit-reader`，与本项目自己的 Capability 设计冲突。
10. `identity-service` 只有密码哈希和签名单元测试，没有 Store、登录锁定、Authorization Code、并发消费和 HTTP 安全流程的直接测试证据。
11. 当前 Tenant 化迁移没有完成测试闭环：IAM 与 Audit Ledger 测试仍构造已从生产类型移除的 `OrganizationID` / `ActingOrganizationID`，目标包无法通过测试编译。

因此 D01 的客观裁决是：**保留本地安全内核，替换角色硬编码和自建通用 IdP 扩张方向，吸收 ThingsBoard 的管理面、配额面和安全生命周期覆盖，但重做其密钥、OAuth、审计和删除语义。**

## 2. 固定证据基线

| 证据 | 固定值 |
| --- | --- |
| 官方仓库 | `thingsboard/thingsboard` |
| 版本 | `v4.3.1.1` |
| 提交 | `c2a52e46c44e308ddee430e7266b8e10eddde9c4` |
| 许可证 | Apache-2.0 |
| 本地只读源码 | `C:\Users\HaoZhang\AppData\Local\Temp\thingsboard-v4.3.1.1-src` |
| 全功能目录 | `contracts/architecture/thingsboard-ce-capability-inventory.v1.json` |

上游行为以固定提交的源码、测试、DDL 和配置为准。当前官方文档仅用于发现能力和解释产品入口，不能覆盖固定源码的实际行为。

主要上游源码证据：

- `Authority`, `Resource`, `Operation`, `DefaultAccessControlService`, `SysAdminPermissions`, `TenantAdminPermissions`, `CustomerUserPermissions`；
- `ThingsboardSecurityConfiguration`, `RestAuthenticationProvider`, `JwtTokenFactory`, `DefaultTokenOutdatingService`, `DefaultSystemSecurityService`；
- `ApiKeyController`, `ApiKeyAuthenticationProvider`, `ApiKeyServiceImpl`, `ApiKeyEntity`；
- `TwoFactorAuthController`, `DefaultTwoFactorAuthService`, `DefaultTwoFaConfigManager` 和各 MFA Provider；
- `AbstractOAuth2ClientMapper`, `Oauth2AuthenticationSuccessHandler`；
- `AuditLogServiceImpl`, `AuditLogController`；
- `TenantServiceImpl`, `CustomerServiceImpl`, `CleanUpService`, `DefaultTenantProfileConfiguration`；
- `AuthControllerTest`, `TokenOutdatingTest`, `DefaultSystemSecurityServiceTest`, `TwoFactorAuthTest` 和 API Key Controller/Auth 测试。

固定提交的关键可复核入口：

- [Authority](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/security/Authority.java)、[CustomerUserPermissions](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/security/permission/CustomerUserPermissions.java)；
- [JwtTokenFactory](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/security/model/token/JwtTokenFactory.java)、[DefaultSystemSecurityService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/security/system/DefaultSystemSecurityService.java)；
- [ApiKeyServiceImpl](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/pat/ApiKeyServiceImpl.java)、[ApiKeyEntity](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/model/sql/ApiKeyEntity.java)；
- [Oauth2AuthenticationSuccessHandler](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/security/auth/oauth2/Oauth2AuthenticationSuccessHandler.java)、[AbstractOAuth2ClientMapper](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/security/auth/oauth2/AbstractOAuth2ClientMapper.java)；
- [TotpTwoFaProvider](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/security/auth/mfa/provider/impl/TotpTwoFaProvider.java)、[BackupCodeTwoFaProvider](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/security/auth/mfa/provider/impl/BackupCodeTwoFaProvider.java)；
- [AuditLogServiceImpl](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/audit/AuditLogServiceImpl.java)、[CleanUpService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/housekeeper/CleanUpService.java)、[DefaultTenantProfileConfiguration](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/tenant/profile/DefaultTenantProfileConfiguration.java)；
- 官方说明入口：[Roles](https://thingsboard.io/docs/user-guide/roles/)、[Security](https://thingsboard.io/docs/user-guide/security/overview/)、[Two-factor authentication](https://thingsboard.io/docs/user-guide/security/two-factor-authentication/)、[Audit log](https://thingsboard.io/docs/user-guide/security/audit-log/)、[Tenant profiles](https://thingsboard.io/docs/user-guide/tenant-profiles/)、[Customers](https://thingsboard.io/docs/user-guide/customers/)。

主要本地证据：

- `cmd/energy-api/internal/gateway/identity.go`, `login_state_store.go`, `audit.go`, `registry.go`；
- `services/identity-service/internal/identity/server.go`, `store.go`, `password.go`；
- `modules/iam/internal/iam/authorization.go`, `postgres_authorization.go`, `reconciliation.go`；
- `libs/sessionstore/store.go`, `postgres.go`；
- `modules/audit/internal/audit/server.go`；
- `infra/identity/postgres/init/001-identity-baseline.sql`；
- `infra/registry/postgres/init/001-s1-registry-baseline.sql`, `001a-tenant-foundation.sql`, `003-iam-runtime-identity-resolution.sql`, `004-iam-reconciliation.sql`；
- `docs/operations/hvac-web-real-mode-shell-spec.md`、两份用户提供的前端/UX 规范。

## 3. 它解决的问题与 Domain 模型

### 3.1 ThingsBoard

ThingsBoard 将平台管理、租户所有权、客户委派和最终用户访问压在一个统一实体模型中：

```text
System scope
  └─ SYS_ADMIN
      └─ Tenant + TenantProfile
          └─ TENANT_ADMIN
              ├─ Customer
              │   └─ CUSTOMER_USER
              └─ Tenant-owned entities
                  └─ optional Customer assignment / public assignment
```

它解决的核心问题是：

- 系统管理员如何创建 Tenant，并用 Tenant Profile 约束资源和 API 使用；
- Tenant 如何拥有实体、用户和设置；
- Customer User 如何只看到被分配给 Customer 的 Dashboard、Device、Asset 等资源；
- 登录、JWT、Refresh Token、2FA、OAuth、API Key 和审计如何共用一个用户目录；
- 删除 Tenant 时如何清理大批从属实体。

该模型适合通用 IoT 平台，但 `Customer` 同时承担商业客户、资源委派容器和公开访问载体，语义较重；固定三 Authority 又把身份类别、管理级别和业务动作揉在一起。

### 3.2 HVAC Web 当前模型

```text
IdentityUser
  └─ immutable OIDC issuer + subject
      └─ Principal
          ├─ TenantMembership [time-bounded]
          ├─ RoleBinding [tenant, actions, allow/deny, time-bounded]
          ├─ SiteBinding [tenant, exact site, actions, allow/deny]
          └─ ExplicitDeny [tenant, optional site, exact action]

Tenant
  └─ Site
      └─ Space / Asset / Device / Point

BFFSession
  ├─ encrypted provider tokens + encrypted CSRF secret
  └─ atomic AuditIntent + Outbox
      └─ append-only Audit Ledger
```

本地模型把 Authentication、Authorization 和业务 Registry 分开，能表达一个 Principal 对多个 Tenant/Site 的不同权限，也能将读、告警确认、控制创建、控制审批等 Action 分开。这比 ThingsBoard 固定 Authority 更符合 HVAC 高风险控制。

必须保持的语义边界：

- `Site` 是物理/运营范围，不是 ThingsBoard `Customer` 的同义词；
- `Tenant` 是数据、安全、策略和计费边界，不应由 OIDC Domain 自动创建；
- `Role` 是 Action 集合的管理便利，不应成为运行时硬编码分支；
- `Principal` 由不可变 `issuer + subject` 解析，Email 和 Display Name 不是身份键；
- 内部 Workload Credential 与外部自动化 API Credential 是两种不同 Domain 对象。

## 4. 核心流程、关键代码和异常边界

### 4.1 Authentication 与 Session

ThingsBoard 流程：用户名密码或 OAuth 登录 → 生成 Access/Refresh JWT Pair → 客户端持有 Token → Refresh 时重读用户状态 → Token Outdating 按用户或 Session 使旧 Token 失效。密码认证还包含禁用、失败次数、锁定、过期、历史和 2FA 分支。

HVAC Web 流程：浏览器发起同源登录 → Gateway 保存一次性 State/Nonce/PKCE Verifier → OIDC 回调消费 State → 服务端交换并校验 ID Token → IAM 重新解析当前 Principal 和 Tenant 权限 → PostgreSQL 原子写 BFF Session、Audit Intent 和 Outbox → 浏览器只得到不透明 `__Host-hvac_session` Cookie。

本地异常边界更严格：

- Redis State 缺失、过期或重复使用返回 `OIDC_STATE_INVALID`；
- OIDC Discovery、Code Exchange、JWKS 或 IAM 不可用时失败关闭；
- Session Store 不可用返回 503，不回退为无状态 Token；
- Session 过期、撤销或空闲超时返回统一 `SESSION_INVALID`；
- 状态变更同时校验精确 Origin 和 Session-bound CSRF；
- Session 机密无法解密时返回 503，不继续使用损坏状态。

裁决：`KEEP` 本地 BFF Session；`ADAPT` ThingsBoard 的密码安全策略、Token/Session 全局撤销和 MFA 生命周期；`REJECT` SPA 持有 JWT Pair。

### 4.2 Authorization 与资源不可见性

ThingsBoard 用 `Authority -> Resource -> Operation -> PermissionChecker` 集中判断。优点是资源和动作词汇统一，控制器普遍先按 Tenant 查询实体再检查权限。缺点是 CE 角色固定，且 `CUSTOMER_USER` 在固定源码中对被分配 Device/Asset 被授予 `WRITE`、`WRITE_ATTRIBUTES`、`WRITE_TELEMETRY`、`RPC_CALL` 和 `CLAIM_DEVICES`，角色名称本身不能可靠表达风险。

HVAC Web 用不可变 Principal、有效 Tenant Membership、Domain Action、精确 Site Binding 和 Explicit Deny 做判定；数据库再以 Tenant/Site RLS 兜底。IAM 查询使用只读、Repeatable Read 事务，要求数据库连接角色严格为 `s1_iam_runtime`，并设置 `app.principal_id` 与 `app.tenant_id`。

公开 Registry 路由把 IAM 403/404 统一映射为：

```text
404 RESOURCE_NOT_FOUND
```

这符合 UX 规范中的 BOLA 边界。对“资源已在可见 Scope 内但缺少动作权限”的控制、审批等操作仍应返回 403 和可行动原因。

裁决：`KEEP` 本地细粒度模型、Explicit Deny 和 RLS；`ADAPT` ThingsBoard 的集中资源/动作目录，但用生成式 Capability Catalog 保持各 Bounded Context 拥有自己的 Action 类型；`REJECT` 固定三 Authority。

### 4.3 API Credential 与机器身份

ThingsBoard API Key 创建后只在响应中显示一次，可禁用、过期、删除，但固定源码把完整 Secret 保存在 `api_key.value` 并按原值查询；认证后获得该用户的完整 Authority，不支持独立 Scope。

HVAC Web 内部调用已经使用 mTLS/SPIFFE 和短时 Delegation Grant，比用户等价 API Key 更适合服务间调用。但当前没有外部集成凭据产品。

裁决：

- `KEEP` 内部 mTLS/SPIFFE；
- `ADAPT` “只显示一次、可过期、可撤销、记录最后使用时间”的生命周期；
- `REPLACE` 为独立 `ApiCredential` / `ServiceAccount`，只保存带 Pepper 的 Hash，绑定 Tenant、允许 Action、允许 Site、到期时间、创建者和用途；
- `REJECT` 明文存储和继承用户全部权限。

### 4.4 MFA、Step-up 与 SSO

ThingsBoard 提供 TOTP、SMS、Email、Backup Code，以及强制配置、失败计数和锁定。值得吸收的是 Provider 生命周期和“配置 Token / 预验证 Token”分阶段流程。

不应复制的行为：

- TOTP Secret 被放在持久化的 `otpauth://...secret=...` 中读取；
- Backup Code 集合按原值持久化、匹配后删除；
- OAuth 成功处理器把 Access Token 和 Refresh Token 放入重定向 URL；
- OAuth Mapper 可根据映射结果 JIT 创建 Tenant 和 TENANT_ADMIN，并只用进程内锁协调创建。

HVAC UX 规范明确要求 R3/R4 控制使用 Step-up Authentication。当前本地 Identity Service 没有 MFA 或认证强度/最近认证时间，属于真实缺口。

裁决：`ADAPT` MFA Provider 与分阶段流程；在选定的生产 OIDC Provider 中实现 MFA/Passkey，并将 `acr`/`amr`、最近认证时间和 Step-up Evidence 绑定到 BFF Session 与高风险 Delegation；`REJECT` 明文 MFA Secret、URL Token 和自动授予 Tenant Admin。

### 4.5 Audit

ThingsBoard 的优势是 Action Type 广、查询入口统一，能按 Tenant、Customer、User、Entity、时间和 Action 筛选。它适合作为操作历史投影。

其固定源码不是合规证据账本：

- Audit 保存通过异步 Executor 执行；失败被捕获并只写日志，不阻止业务动作；
- Action Data 可包含 Attributes、Timeseries、RPC 参数和 Device Credentials；
- 失败详情可包含堆栈，存在敏感信息和高基数风险。

HVAC Web 的 Session 变更把状态、Audit Intent 和 Outbox 原子提交；Audit Ledger 校验生产者 SPIFFE、Idempotency Key、Event ID 和 Actor SPIFFE，并对重复内容冲突返回 409。这一模式更适合控制系统。

本地仍有三项缺口：

- Authentication 失败、锁定、建号和密码重置没有耐久事件；
- Session/Audit 查询仍以角色字符串授权；
- 缺少统一、可搜索、脱敏的跨 Domain Audit Projection。

裁决：`KEEP` 本地事务 Audit Intent、Outbox、幂等 Ledger；`ADAPT` ThingsBoard 的 Action Taxonomy 与查询维度；`REJECT` 敏感 Payload 和 best-effort-only 安全证据。

### 4.6 Tenant Profile、Usage 与 Lifecycle

ThingsBoard `DefaultTenantProfileConfiguration` 汇总实体数、Transport/API/REST/WS 限流、消息/数据点/脚本执行、资源大小、TTL、Session/Subscription、Rule Engine 和队列限制。它解决了“一个 Tenant 能消耗多少共享平台资源”的问题。

值得吸收的是：Tenant 明确绑定一个版本化 Profile，运行时能够计算 Effective Limit 和 Usage State。不可照搬的是把安全、存储、队列、脚本、传输和产品功能放进一个巨大配置对象。

目标应拆为：

```text
TenantPolicySet
  ├─ IdentitySecurityPolicy
  ├─ ApiRatePolicy
  ├─ TelemetryRetentionPolicy
  ├─ RealtimeSubscriptionPolicy
  ├─ AutomationExecutionPolicy
  ├─ ResourceStoragePolicy
  └─ NotificationDeliveryPolicy

TenantUsageSnapshot
  └─ policyVersion + measuredAt + counters + limit state
```

ThingsBoard Tenant 删除会先删除部分核心记录，再把大量实体清理提交给可选 Housekeeper；Housekeeper 不存在时任务不会提交，事件清理错误也只记录日志。HVAC Web 当前甚至没有完整 Tenant 退役流程。

裁决：`ADAPT` Tenant Profile 的显式限额和 Effective Usage；`REPLACE` 大配置聚合与可选清理，使用 `ACTIVE -> SUSPENDED -> RETIRING -> RETIRED` 的耐久 Saga、Tombstone、影响清单、可重试任务和审计证据。

## 5. 源码级本地反向审查

| 本地模块 | 已证实行为 | 客观裁决 |
| --- | --- | --- |
| Gateway OIDC/BFF | PKCE、State、Nonce、Issuer/Audience/Signature 校验；服务端加密 Token；HttpOnly/Secure/SameSite Cookie | `KEEP` |
| Redis Login State | `SET NX + TTL`，`GETDEL` 一次性消费 | `KEEP` |
| PostgreSQL Session Store | Serializable 事务同时写 Session、Audit Intent、Outbox；失败注入点覆盖回滚边界 | `KEEP` |
| IAM Principal Resolution | 不可变 `issuer + subject`；未映射 Principal 失败关闭 | `KEEP` |
| IAM Authorization | Membership + Role/Site Binding + Explicit Deny + 时间窗 + Policy Revision | `KEEP` |
| IAM PostgreSQL Boundary | 固定只读 DB Role、RLS Context、Repeatable Read、Timeout | `KEEP` |
| Public Registry Read | 未授权或不存在统一 404；下游响应再次校验 Tenant/Entity Identity | `KEEP` |
| SPIFFE Delegation | mTLS Peer + 签名 Grant + Audience/Action/Scope/TTL 校验 | `KEEP` |
| Audit Ledger Ingest | Producer SPIFFE、Body Limit、幂等与内容冲突检测 | `KEEP` |
| Identity Password Store | Argon2id；不存在用户也执行 Dummy Hash；5 次失败锁定 15 分钟，但安全生命周期不完整 | `KEEP` 当前安全原语，不扩张为通用 IdP |
| Identity OIDC Server | 单 Client、最小 Code Flow；没有 MFA/恢复/激活/联合身份；HTTP/Store 缺直接测试 | `REPLACE` 生产通用 IdP；仅可保留为明确受限的测试/单租户实现 |
| Tenant Context | ID Token Claim 或 Deployment Default 固定 Tenant；无产品级 Tenant Switch | `REPLACE` |
| Session Revocation Auth | Gateway 硬编码 `platform-admin` Role | `REPLACE` 为 `session:revoke` Capability |
| Audit Query Auth | Gateway 和 Audit Ledger 双重硬编码 `audit-reader/platform-admin` | `REPLACE` 为 IAM 签发的 `audit:read` Capability |
| API Credential | 仅内部 Workload Credential，无外部最小权限凭据 | `ADAPT` ThingsBoard 生命周期并重做安全模型 |
| Tenant Policy/Usage | 只有 Domain 局部限制，无统一版本化 Effective Policy/Usage | `ADAPT` Tenant Profile 思路并拆分聚合 |
| Tenant Lifecycle | 无退役/删除 Saga | `REPLACE` ThingsBoard Housekeeper 语义 |
| Product Admin Plane | 仅 CLI + Reconciliation，无 Tenant/User/Role 管理 API/UI | `ADAPT` ThingsBoard 管理覆盖到本地 Domain 模型 |
| Audit Search | 多个耐久写入边界存在，但没有统一跨 Domain 搜索投影 | `ADAPT` ThingsBoard 查询维度，不集中写 Authority |

## 6. 最终能力裁决矩阵

| ThingsBoard 参考能力 | 裁决 | 映射到 HVAC Web |
| --- | --- | --- |
| Tenant 拥有实体和用户 | `ADAPT` | 保持 Tenant 数据边界；用户通过 Membership 关联，不限制为单 Tenant |
| Customer 委派与 Public Customer | `DEFER` | Site 不能替代 Customer；只有出现外部客户/承包商委派需求时增加独立 Delegated Access Group |
| 固定 SYS_ADMIN/TENANT_ADMIN/CUSTOMER_USER | `REJECT` | 保持 Capability + Scope + Explicit Deny；Platform Admin 使用独立 Break-glass Policy |
| Resource + Operation 集中目录 | `ADAPT` | 建立版本化 Capability Catalog，Domain Action 仍由各 Bounded Context 拥有 |
| Tenant-scoped DAO 查询 | `KEEP` | IAM + Service Scope + PostgreSQL RLS 多层隔离 |
| 不可见资源隐藏 | `KEEP` | 读取不可见对象统一 404；可见对象动作不足返回 403 |
| Access/Refresh JWT 给 SPA | `REJECT` | 保持 BFF Opaque Session Cookie |
| Session 定向/全局撤销 | `ADAPT` | 增加按 Principal、Tenant、Credential 和 Security Event 撤销；保留当前同步拒绝目标 |
| 密码策略、锁定、过期、历史 | `ADAPT` | 交给选定生产 IdP；不继续扩张自制密码平台，除非复用评估证明必要 |
| TOTP/SMS/Email/Backup Code | `ADAPT` | 优先 Passkey/TOTP；Secret 加密、Recovery Code 只存 Hash；Step-up 绑定高风险 Action |
| OAuth Domain/Client Mapper | `ADAPT` | 显式预注册 Issuer/Client/Claim Mapper；无自动 Tenant/Admin 创建 |
| OAuth URL Token | `REJECT` | 回调只接收 Code，随后清理 URL；浏览器不见 Token |
| 用户 API Key | `REPLACE` | Scoped ApiCredential/ServiceAccount；Hash-at-rest、一次显示、过期、轮换、撤销、最后使用记录 |
| 管理员 Impersonation Token | `REJECT` | 如未来确需支持，只允许双人批准、短时、明显 Banner、禁止高风险控制、全量审计的 Support Session |
| Audit Action Taxonomy 与筛选 | `ADAPT` | 建统一脱敏搜索投影，不改变各 Domain 的 Audit Authority |
| 异步 best-effort Audit | `REJECT` | 安全/控制状态必须与 Audit Intent 原子提交；投影可异步 |
| Tenant Profile 和 API Usage | `ADAPT` | 拆分 Policy Aggregate，提供统一 Effective Policy/Usage View |
| 可选 Housekeeper Tenant 删除 | `REPLACE` | 耐久 Retirement Saga + Tombstone + 可重试清理 + 进度/失败证据 |
| 系统/租户 Mail、SMS、UI、QR 设置 | `DEFER` | 按 Notification、Deployment 和 UX 审查域处理，不放入 IAM 核心 |

## 7. 不适合 HVAC Web 的上游部分

以下差异有明确安全、Domain 或复杂度理由，不需要按 ThingsBoard 行为重构：

- 固定 Authority 无法表达 `control:create`、`control:approve`、`alarm:ack` 等风险不同的 Action。
- Customer 是通用 IoT 委派容器，不能替代物理 Site、运营 Tenant 或外部服务商组织。
- 浏览器 Bearer Token 会扩大 XSS、日志、历史记录和前端存储泄漏面；BFF 更符合 Real Mode 规范。
- 明文 API Key、明文 MFA Recovery Material 和 OAuth URL Token 不满足最小秘密暴露原则。
- best-effort Audit 不能作为控制、审批、Session 撤销或身份生命周期的证据边界。
- JIT 创建 Tenant/Tenant Admin 会把外部 Claim Mapping 错误直接升级为平台所有权错误。
- 大而全 Tenant Profile 会跨越身份、遥测、自动化、存储和通知 Bounded Context。
- 可选 Housekeeper 无法证明 Tenant 数据已经完整退役。
- 管理员直接获取下级用户 Token 绕过了用户在场、Step-up 和可归责性。

## 8. 与项目规范的对齐

| 项目要求 | 当前证据 | 裁决 |
| --- | --- | --- |
| 不可见资源 404，不猜测其他 Tenant/Site | Registry 已统一 404 | 已满足，保留 |
| 可见 Scope 内缺动作权限返回 403 | Domain 写操作有独立 Action；需持续按路由验证 | 部分满足 |
| 前端 Route/Component/Action 仅用于 UX，后端权威授权 | IAM 是权威；Session/Audit 仍硬编码 Role | 存在冲突，必须重构 |
| 当前 Tenant 和 Site 持续可见并可明确切换 | Site Context 已进入 Real Shell；Tenant Switch 未实现 | 未满足完整多租户 |
| R3/R4 Step-up Authentication | 本地无 MFA/Step-up Evidence | 未满足，P0 |
| 高风险操作 Audit 由后端生成 | 控制和 Session 有耐久意图；身份生命周期缺失 | 部分满足 |
| SPA 不保存 Long-lived Secret | BFF Cookie、CSRF 内存、服务端加密 Token | 已满足，保留 |
| Real 配置失败关闭且不回退 Demo | Phase 1 Real 部署显式配置 OIDC/IAM/DB/Audit；测试 Fixture 通过显式开关隔离 | 已满足部署方向，继续验证 |

## 9. 实施优先级与验收门槛

本节只冻结后续实施顺序，不在本审查票内修改运行时。

### P0：身份与高风险安全闭环

1. 完成生产 IdP 复用评估：优先选择维护良好的 OIDC/OAuth2 实现；自建 `identity-service` 不得在缺少证据时继续扩张为通用 IdP。
2. 实现 MFA/Passkey 和 R3/R4 Step-up；高风险 Delegation 必须携带可验证 Authentication Assurance 与 Freshness。
3. 把登录成功/失败、锁定、解锁、建号、禁用、密码重置、MFA 变更和 Step-up 结果写入耐久 Audit Intent/Outbox。
4. 将 Session 撤销和 Audit 读取从 Role 字符串改为 IAM Capability；Roles 恢复为纯展示/管理分组。
5. 为 Identity Store/HTTP Flow 增加并发、过期、单次消费、锁定、枚举防护、Key Rotation 和故障注入测试。
6. 完成 Tenant 术语/字段迁移的测试闭环，删除旧 Organization Fixture 和断言，恢复 IAM 与 Audit Ledger 包测试可编译、可执行。

验收门槛：无 MFA/Step-up 的 Principal 不能创建或审批 R3/R4 Command；Audit 写入失败时对应身份/安全变更不能被宣称成功；不存在硬编码 Role 授权分支。

### P1：多租户和管理控制面

1. 实现已授权 Tenant 列表和显式 Tenant Context Switch；切换时轮换 Session Context/CSRF、清理 Site Cache/Realtime，并写审计。
2. 建立 Tenant、Principal、Membership、Role Template、Site Binding、Explicit Deny 的管理 API 与 UI；所有变更带 Revision、审批边界和审计。
3. 建立版本化 Capability Catalog，生成/校验 IAM、Gateway、OpenAPI 和前端枚举，消除字符串漂移。
4. 实现外部 Scoped ApiCredential/ServiceAccount；默认无权限、Secret 只显示一次、数据库只存 Hash。
5. 建立跨 Domain Audit Search Projection，支持 Tenant/Actor/Action/Resource/Outcome/Time 查询与敏感字段分级脱敏。

验收门槛：一个多 Tenant Principal 可显式选择 Context，不能通过 Header/URL 越权切换；权限变更即时反映到新决策并使旧高风险 Grant 失效；Audit 查询不依赖角色字符串。

### P2：租户治理

1. 建立拆分的 Tenant Policy Set 和统一 Effective Policy/Usage View。
2. 实现限流、配额预警、硬限制、Usage Event 和管理员可解释状态。
3. 实现 Tenant Retirement Saga、Tombstone、清理进度、失败重试和法定保留例外。
4. 只有在产品证据出现后，才增加 Customer/Contractor/Delegated Access Group；不得把 Site 复用成客户组织。

验收门槛：任一超限决策可追溯到 Policy Version 和 Usage Snapshot；Tenant 退役可证明每个 Owner Service 的状态，不能因可选 Worker 缺失而静默完成。

## 10. 文档与源码冲突

当前 ThingsBoard 官方 Roles 文档将 CE Customer User 描述为只读，但固定 v4.3.1.1 `CustomerUserPermissions` 源码明确允许被分配 Device/Asset 的写入、Telemetry/Attribute 写入、RPC 和 Claim。本文以固定源码为准，并把该冲突视为“不应从角色名称推断权限”的证据。

本地也存在文档漂移：`docs/security/s0-authenticated-principal.md` 仍描述早期内存 Session，而当前生产接线已支持 PostgreSQL Durable Session、Audit Intent 和 Outbox。该文档不能作为当前持久化边界的反证，后续文档治理应将其标记为历史阶段或更新为当前状态。

## 11. 本轮最终裁决

- HVAC Web 的 Tenant/Site Scope、Capability、Explicit Deny、RLS、BFF Session、SPIFFE Delegation 和事务 Audit 内核获得 `KEEP`。
- ThingsBoard 的管理面覆盖、Session 撤销、MFA 生命周期、Audit 查询维度和 Tenant Usage/Profile 思路获得 `ADAPT`。
- ThingsBoard 的固定 Authority、URL Token、明文/全权 API Key、JIT Tenant Admin、管理员直接取用户 Token、best-effort Audit 和可选删除清理获得 `REJECT` 或 `REPLACE`。
- 当前本地 Identity Provider、Role 硬编码、多 Tenant Context、身份安全审计、API Credential、统一 Usage Policy、Tenant Lifecycle 和 Audit Search 仍是待实施差距，不能宣称 D01 产品能力完整。

该裁决完成 D01 对比，不完成整个 ThingsBoard 全功能审查。其余九个审查域及最终跨域反向审查仍按总路线图继续。

## 12. 本轮验证结果

执行日期：2026-08-17。

| 验证 | 结果 |
| --- | --- |
| `git diff --check -- docs/architecture/thingsboard-security-tenancy-adjudication.md` | 通过 |
| `go test ./services/identity-service/internal/identity` | 通过 |
| `go test ./libs/sessionstore` | 通过 |
| `go test ./modules/iam/internal/iam` | 失败：测试仍引用旧 `OrganizationID`、`ActingOrganizationID`、`OrganizationMembership` |
| `go test ./modules/audit/internal/audit` | 失败：测试仍引用旧 `OrganizationID`、`ActingOrganizationID` |

失败发生在测试编译阶段，证明当前 Tenant 迁移尚未完成；它不是 ThingsBoard 参考实现的缺陷，也不是本轮新增文档造成的回归。在修复前，不得宣称 IAM/Audit 的当前 Tenant 化实现已通过回归验证。
