# 设备登记（Registry）

登记服务（`platform-core-service`）负责平台的 `core_registry` 表：租户下的站点、空间、资产、设备、传感器、点位、计量绑定和电价。它只给内部的服务用，不面向浏览器；默认嵌在 `energy-api` 里运行，`modules/registry/cmd/registry-owner` 是单独运行的入口。

## 内部接口

都在 `/internal/v1/registry` 下：

- 读：`GET /sites`、`/sites/{siteId}`、`/sites/{siteId}/assets`、`/assets/{assetId}`、`/sites/{siteId}/devices`、`/devices/{deviceId}`、`/sites/{siteId}/spaces/...`、`/sites/{siteId}/asset-model`（资产模型：设备、机组和在用点位）、`/sites/{siteId}/tariff`（当前电价）、`/sites/{siteId}/import-tariffs`（一段时间内的进线电价版本，给电价窗口同步用）、`/sites/{siteId}/meter-bindings/resolve?deviceId=...&pointId=...&sampledAt=...`（查一个点位在某个时刻的计量绑定）。
- 写（系统管理 → 注册中心用）：新建和修改站点、空间、资产、设备、传感器、点位，以及绑定。

调用方必须出示验证过的客户端证书，证书里唯一的 SPIFFE 身份要在配置的允许名单里（平台网关、AI 助手、能耗计算等）。每个请求还要带 `X-Delegation-Grant`：由 IAM 直接签给这个出示者、只针对一个具体动作、对象是 `platform-core-service` 的授权。授权不能转授，签给网关的授权不能被 AI 助手拿去用。

查库之前，登记服务用自己的服务身份调 IAM 的 `/internal/v1/registry-read/grant-status`（只接受双向 TLS），IAM 只返回当前的登记策略版本，以及这张授权的 `jti` 有没有被撤销。然后拒绝：签名不对、签发方 / 出示者 / 对象 / 动作不对、过期、有效期过长、转授、已撤销、策略过期、范围自相矛盾的授权。

## 数据库边界

`CORE_DATABASE_URL` 必须以 `s1_core_service` 登录，这个登录只能切换到 `s1_core_runtime` 组角色。每次读取用一个可重复读的只读事务：

1. `SET LOCAL ROLE s1_core_runtime`。
2. 按验证过的授权，在事务里设置 `app.tenant_id` 和 `app.authorized_site_ids`。
3. 查强制行级隔离的表。
4. 提交。

租户是最外层的隔离，授权里的站点范围在租户内再收窄。不存在和无权查看都返回 `RESOURCE_NOT_FOUND`，不透露东西存不存在。这个身份读不到 IAM 的数据。

列表用 `(display_name COLLATE "C", id)` 翻页，每页条数有上限。翻页游标用 HMAC-SHA256 保护，绑定到这个接口的资源、上级、具体动作、策略版本和生效的租户 / 站点范围；每一页都重新检查权限。

## 环境变量

必填：

- `CORE_DATABASE_URL`
- `CORE_TLS_CERT`
- `CORE_TLS_KEY`
- `CORE_CLIENT_CA`
- `CORE_IAM_CA`
- `CORE_IAM_GRANT_CERT`
- `CORE_IAM_ENDPOINT`
- `CORE_CURSOR_HMAC_KEY`：URL 安全的 base64，至少 32 字节

可选：

- `CORE_SERVICE_ADDR`（默认 `127.0.0.1:18445`）
- `CORE_DIAGNOSTICS_ADDR`（默认 `127.0.0.1:19084`）
- `CORE_GRANT_ISSUER`（默认 IAM 的 SPIFFE 身份）
- `CORE_ALLOWED_WORKLOAD_SPIFFE`（默认网关的 SPIFFE 身份）
- `CORE_OPERATIONS_AGENT_SPIFFE`（默认 AI 助手的 SPIFFE 身份）
- `CORE_ANALYTICS_PROJECTOR_SPIFFE`（默认能耗计算程序的 SPIFFE 身份）
- `CORE_AUDIENCE`（默认 `platform-core-service`）
- `OTEL_EXPORTER_OTLP_ENDPOINT`

日志和指标只记接口模板、状态、耗时和追踪编号，不记原始授权、cookie、凭据或用户资料。

## 测试

```bash
npm run test:identity
npm run build:registry-owner
npm run registry:integration
npm run domain:run -- --domain=registry --layers=contracts,unit,integration
```
