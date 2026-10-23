# energy-api（平台网关）

网页唯一的后端入口。它的 `internal/gateway` 负责面向浏览器的 HTTP 接口、登录会话、在入口处检查权限并换取授权，再把请求转给负责那类数据的模块。业务数据和业务规则在 `modules/*` 或单独的服务里，energy-api 本身不是一个业务领域。默认还把告警、工单、登记、权限、操作记录等模块嵌在进程里运行。

## 公开接口

以 `contracts/http/platform-gateway.openapi.yaml` 为准。按用途大致分为：

- 运行状态：`GET /api/v1/health`（`includeBuild=true` 带构建信息）、`GET /api/v1/version`、`GET /api/v1/platform/status`。
- 登录和会话：`POST /api/v1/auth/login`（开始带 PKCE 的 OIDC 授权码流程）、`GET /api/v1/auth/callback`（完成回调、建登录会话）、`GET /api/v1/principal`（当前登录的人，经 IAM）、`POST /api/v1/auth/logout`（带 Origin 和 CSRF 检查吊销当前会话，并在 `Location` 里返回身份服务的退出地址）、`POST /api/v1/auth/sessions/{sessionId}/revoke`（管理员吊销会话）、`POST /api/v1/me/password`（改自己的密码）。
- 业务数据：站点、设备、遥测、能耗、告警、工单、控制指令、故障诊断、预测、优化、节能验证、报告、通知、集成、用户与角色（`/api/v1/iam/...`）、AI 助手（`/api/v1/sites/{siteId}/operations/agent-sessions` 等）。

成功的响应直接是资源本身，没有统一的外层包装。失败用 `application/problem+json`，带固定的 `code`、`traceId`、`retryable` 和安全的说明。

网关接受合法的 `X-Request-ID`，延续合法的 W3C `traceparent`，每个响应都带这两个头。请求日志只记接口模板、方法、状态、耗时、请求编号和追踪编号；不记 cookie、授权头、身份头、查询参数、授权码、授权或请求体。

## 登录边界

OIDC 令牌在服务端交换并加密保存。浏览器只拿到一个不透明的 `__Host-hvac_session` cookie（`Secure`、`HttpOnly`、`Path=/`、`SameSite=Lax`）。

网关实现标准的 OIDC 授权码流程（S256 PKCE、发现接口、换码、取 JWKS、校验签名），不绑定某个身份提供方；第一阶段用平台自己的 `identity-service`。

身份提供方不负责登录会话，也不负责权限。网关自己保管一次性的 state（有时限，绑定本地的 `returnTo`）、生成并用恒定时间校验 nonce、严格检查 JWT 和令牌类型、检查 `nbf`、限制身份提供方响应的大小、加密保存登录会话、在事务里写操作记录和待发事件，并以平台自己的租户授权为准。身份提供方的角色或自定义字段从不作为授权依据。

网关通过 TLS 1.3 双向认证调 IAM，每次签一张短时授权，绑定网关的 SPIFFE 身份、IAM 对象、一个动作、一个会话范围、当前租户、策略版本和到期时间。客户端自己带的身份头或授权头一律拒绝。

更详细的身份和威胁边界见 `docs/security/login-and-current-user.md`。

## 登录会话和操作记录

开启登录时，正式配置要求 `GATEWAY_DATABASE_URL` 和 `AUDIT_URL`。只有明确打开测试开关，才允许把会话放在内存里或不连操作记录服务。

登录、退出和管理员吊销会话，都在一个 PostgreSQL 事务里同时写会话状态、操作记录意图和 Protobuf 待发事件。成功后响应带固定的 `X-Audit-Message-ID`；事务提交之后才下发 cookie。网关查操作记录时，另签一张只限 `audit:read` 和当前租户的双向 TLS 授权。

完整的事务、投递、收件表、行级隔离和哈希链设计见 `docs/security/session-and-audit.md`。

## 接口归属

网关把 `contracts/ownership/route-ownership.v1.json` 作为一份校验过的、不可变的快照加载。每个公开请求在处理前，都按方法和路径确定唯一一个负责的模块；没有按比例分流、影子模块、旧系统或请求级的回退。生效的版本号在响应头 `X-Route-Policy-Revision` 里。

更新这份归属表时，归属表和路由的版本号都必须递增。网关先追加写一条路由策略的记录，再原子地替换快照：进行中的请求按原来的决定处理，之后的请求才用新的。

旧系统的路由适配器已经不在网关里；历史上的迁移定义会被现在的解析器拒绝。

数据归属和回滚的细节见 `docs/security/route-ownership.md`。

## 测试

```bash
npm run contracts:generate
npm run contracts:check
npm run events:check
npm run ownership:check
npm run test:gateway
npm run test:identity
npm run test:durable-unit
npm run test:durable-postgres
npm run build:energy-api
npm run build:iam-owner
npm run build:oidc-fixture
npm run lint
npm run build
```

## 构建信息

发布构建可以不改代码注入构建信息：

```bash
go build -ldflags "-X main.version=0.3.0 -X main.commit=$(git rev-parse HEAD) -X main.builtAt=$(date -u +%Y-%m-%dT%H:%M:%SZ)" ./cmd/energy-api
```

本地默认是 `dev`、`unknown`、`unknown`。
