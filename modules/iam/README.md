# 权限（IAM）

IAM 决定一个人或一个后台服务能在哪些站点做什么，并为每次允许的请求签一张短时授权。账号和密码由身份服务（`identity-service`）管；IAM 只保存「哪个登录身份对应平台里的哪个人」，不碰密码，也不面向浏览器。默认嵌在 `energy-api` 里运行，`modules/iam/cmd/iam-owner` 是单独运行的入口。

## 网络边界

IAM 只接受 TLS 1.3，并且必须出示客户端证书。知道地址不够：调用方的证书身份（SPIFFE）必须正好等于配置里允许的那个，还要带一张由那张证书签的、范围受限的授权。

主要接口（都是内部接口）：

| 接口 | 谁调 | 做什么 |
| --- | --- | --- |
| `POST /internal/v1/principal/current` | 平台网关 | 当前登录的人是谁、能看到哪些租户和站点 |
| `POST /internal/v1/registry/decision`、`telemetry/decision`、`command/decision`、`analytics/decision`、`alarm/decision`、`work-order/decision` | 平台网关 | 判定这个人能不能做这件事，允许时签授权 |
| `POST /internal/v1/registry/workload-decision`、`analytics/workload-decision` | 后台服务（报告、优化、节能验证、能耗计算等） | 判定后台服务自己能不能读（ADR 0017） |
| `POST /internal/v1/telemetry/grants:consume`、`telemetry/revocations:poll` | 遥测 | 用掉一张遥测授权、拉取撤销 |
| `POST /internal/v1/registry-read/grant-status` | 登记服务 | 查一张登记授权当前是否还有效 |
| `POST /internal/v1/admin/members`、`admin/mutations` | 平台网关 | 用户与角色管理 |
| `POST /internal/v1/admin/api-credentials/*` | 没有调用方：网关不再转发 | 发、换、收回 API 凭据 |

## 当前的人

`POST /internal/v1/principal/current` 要求授权的对象是 `iam-service`，动作只有一个 `principal:read`，范围只有一个 `session:{id}`，带上登录身份（签发方和主体）、会话、策略和授权的编号，不能转授。返回的是导航用的上下文；里面的租户信息本身不算授权。

## 怎么判定

以登记服务的判定为例（其他几类同理）。请求体只有：

```json
{
  "tenantId": "018f1e00-0000-7000-8000-000000000001",
  "action": "site.read"
}
```

IAM 先把配置的签发方加上不变的主体编号对应到平台里的一个人，确认他在这个租户里是有效成员，再看他在这个租户里的角色指派（范围是整个租户或某一个站点，ADR 0022），最后去掉明确禁止的。光是成员不给任何权限；别的租户的角色指派不算。租户编号必须是 UUIDv7，格式不对在查库之前就拒绝。

返回结果写明允许还是拒绝、是谁、哪个租户、允许和不允许的站点、具体动作、策略版本、原因和判定时间。拒绝也是一次正常的判定，所以返回 HTTP 200，只是不签授权。

允许时，IAM 给登记服务签一张最长 30 秒有效的授权，绑定：IAM 和出示授权的服务的身份；一个对象和一个具体动作；发起的人和他的登录身份；租户和允许 / 不允许的站点；策略版本和允许的原因；上一级网关授权、会话、签发和到期时间、唯一编号；不能转授。

登记服务这边用 `libs/registryauth` 验证：要求策略版本是最新的，并在线查是否已撤销；格式不对、太大、对象不对、出示者不对、过期、已撤销、策略过期或范围自相矛盾，一律拒绝。

网关可以替明确允许的下游服务（例如 AI 助手）申请授权，IAM 直接签给那个服务；其他的转授一律拒绝。

## 查授权是否有效

`POST /internal/v1/registry-read/grant-status` 只接受配置里的登记服务身份（双向 TLS）。请求只带租户编号和授权的 `jti`，不带授权本身。IAM 返回当前有效的登记策略版本，以及这个编号有没有未过期的撤销记录。数据库或策略出错时，登记服务的授权判定就不可用（不会放行）。

## 数据库

设置 `IAM_DATABASE_URL` 使用 PostgreSQL 的 `iam` 表。连接必须以 `s1_iam_runtime` 身份登录；用 postgres、迁移或登记服务的身份连接，会在建连接池时被拒绝。每次判定用一个可重复读的只读事务：

1. `iam.resolve_principal_identity` 只按签发方加主体编号精确查人，不返回可变的资料字段。
2. 在事务里设置当前的人和租户，数据库据此做行级隔离。
3. 成员关系、角色指派、明确禁止和当前的策略版本从同一个快照里读。
4. 遇到不认识的动作、没有生效的策略，或者数据库出错，判定为不可用，而不是放宽权限。

这个身份读不到登记服务的表，也不能改 IAM 的数据。`IAM_DATABASE_URL` 和 `IAM_S1_AUTHORIZATION_FIXTURE` 只能设一个；两个都没设时，IAM 拒绝一切，不会退回到测试数据。

## 用户与角色管理

`POST /internal/v1/admin/members`（网关调用，需要 `iam:admin` 授权和「用户与角色管理」权限 `iam.admin`）返回可以指派的内置角色，以及本租户所有成员和他们的角色指派。修改接口包括 `member.create`：在一个事务里把身份目录里的账号加为成员，并给他第一批角色指派。每次修改之后，操作的人必须仍然是有效成员、并且在整个租户上持有 `iam.admin`，否则拒绝（`IAM_ADMIN_SELF_LOCKOUT`），防止管理员把自己锁在外面。带租户级权限的角色（例如管理员）只能指派到整个租户；内置角色不能修改。

## 从清单同步身份

`PostgresReconciliationStore` 是单独的管理通道，只接受 `s1_iam_reconciler` 数据库身份。它应用一个人完整的、已批准的授权清单，并按（来源系统、来源键、来源版本）记下输入的 SHA-256：

- 版本和哈希都一样：记「没有变化」，不重写授权数据。
- 版本更旧、同版本哈希不同、来源换了人、登录身份换了人、人员编号换了：记「隔离」，不改现有数据。
- 邮箱和显示名可以更新；邮箱相同也不会把两个不同的登录身份合并。
- 应用清单时，在一个可串行化的事务里整体替换这个人的成员关系、角色指派和明确禁止。角色按 key 指定已有的角色，不认识的 key 让这次同步失败。
- 记录里保存来源、版本、哈希、安全的编号、结果和原因、数据条数；不保存登录令牌、浏览器 cookie、客户端凭据或原始授权。

这个身份受行级隔离约束，读不到 `core_registry`，不能改策略，是唯一能写同步记录和隔离表的身份；正常判定用的 `s1_iam_runtime` 读不到这些表。

`modules/iam/cmd/identity-reconciler` 是没有 HTTP 的同步入口：从标准输入读一条 JSON 命令，通过只读的 `identity_directory_reader` 只取选中用户的编号、显示名、邮箱和状态，再加上明确批准的租户、角色指派和禁止，拼成完整的同步请求。身份提供方的角色或组织信息不会变成授权。作为受保护的管理任务运行，需要：

- `IDENTITY_DIRECTORY_DATABASE_URL`
- `IAM_RECONCILER_DATABASE_URL`
- `IDENTITY_ISSUER`

身份目录的连接是只读的，读不到密码。

## 测试数据

默认拒绝一切。只有同时明确设置下面两项，才启用固定的测试授权：

```text
IAM_S1_AUTHORIZATION_FIXTURE=true
IAM_EXTERNAL_SUBJECT_ISSUER=https://configured-identity-issuer.example
```

测试数据覆盖：直接的租户成员、整租户和单站点的角色指派、明确禁止、是成员但没有这个动作、成员关系已撤销、没有对应的人。只用于确定性的集成测试，不能当作正式环境的兜底。

## 请求头和登录信息

IAM 拒绝客户端自己带的人员、角色、租户、站点、管理员或范围请求头（包括加前缀的变体）。邮箱、显示名和身份提供方的信息都不能选择或扩大授权；判定只用验证过的签发方和主体，加上平台自己保存的成员、角色指派和策略。

判定的日志和指标只有安全的编号、动作、原因、策略版本、范围数量和追踪编号；没有登录令牌、cookie、原始授权、邮箱或显示名。

## 环境变量

主要的几个：`IAM_SERVICE_ADDR`、`IAM_TLS_CERT`、`IAM_TLS_KEY`、`IAM_CLIENT_CA`、`IAM_ALLOWED_WORKLOAD_SPIFFE`（网关的身份）、`IAM_CORE_WORKLOAD_SPIFFE`（登记服务的身份）、`IAM_OPERATIONS_AGENT_SPIFFE`、`IAM_REPORT_SERVICE_SPIFFE`、`IAM_OPTIMIZATION_SERVICE_SPIFFE`、`IAM_MV_SERVICE_SPIFFE`、`IAM_TELEMETRY_RUNTIME_SPIFFE`、`IAM_AUDIENCE`、`IAM_REGISTRY_GRANT_AUDIENCE`、`IAM_TELEMETRY_GRANT_AUDIENCE`、`IAM_COMMAND_GRANT_AUDIENCE`、`IAM_POLICY_REVISION`、`IAM_DATABASE_URL`、`IAM_ADMIN_DATABASE_URL`、`IAM_TELEMETRY_GRANT_DATABASE_URL`、`IAM_API_CREDENTIAL_PEPPER_FILE`。嵌在 `energy-api` 里时由 `cmd/energy-api/embedded_energy.go` 读取。

登记授权的签发方取自 IAM 自己 TLS 证书里的 SPIFFE 身份，不是可以随便改的环境变量。

## 测试

```bash
npm run test:identity
npm run registry:integration
npm run build:iam-owner
```
