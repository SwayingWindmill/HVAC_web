# 登录和当前用户

浏览器怎么登录、平台网关怎么确认「当前是谁」，以及中间的信任边界。会话和操作记录怎么持久保存见 `session-and-audit.md`。

## 信任边界

```text
浏览器
  HTTPS + 不透明的 Secure、HttpOnly、SameSite=Lax cookie
      |
      v
网页 / 平台网关（energy-api）的公开入口
  OIDC 授权码 + PKCE
  身份服务的令牌在服务端加密保存
  签发短时授权
      |
      | TLS 1.3 双向认证
      v
内部的 IAM
```

浏览器只能访问网关公开的 `/api/v1` 接口，拿不到 IAM 的地址、客户端证书、访问令牌、刷新令牌、ID 令牌、回调完成后的授权码，也拿不到任何授权。

## 登录流程

1. `POST /api/v1/auth/login` 在服务端建一个一次性的登录状态：PKCE 校验码、nonce、站内的 `returnTo` 和创建时间。
2. 网关跳到配置的 OIDC 授权地址（平台自己的身份服务），用授权码流程、PKCE S256、state 和 nonce。
3. `GET /api/v1/auth/callback` 只消费一次这个状态，并在服务端用授权码换令牌。
4. 网关校验：发现接口的签发方、JWKS 签名、算法、密钥编号、签发方、对象、令牌用途、令牌类型、nonce、过期和生效时间。
5. 身份服务的令牌用 AES-GCM 加密，只存在服务端的会话记录里。
6. 浏览器拿到一个不透明的 `__Host-hvac_session` cookie（`Secure`、`HttpOnly`、`Path=/`、`SameSite=Lax`）。
7. 网关跳回站内的原页面，并从地址里去掉回调的 code 和 state。

会话存在 PostgreSQL 里，登录、退出和吊销都在一个事务里同时写操作记录（见 `session-and-audit.md`）。

## 当前用户

`GET /api/v1/principal` 要求有效的登录会话。网关用自己双向 TLS 证书的私钥签一张授权，调 IAM 的 `POST /internal/v1/principal/current`。

这张授权最多 60 秒有效，绑定：发起的人和他的签发方；执行的网关的 SPIFFE 身份；当前租户；对象 IAM；只有一个动作 `principal:read`；只有一个会话范围；策略版本；会话编号、授权编号、签发和到期时间。

IAM 同时验证 TLS 客户端证书链，以及用对方证书公钥验证授权签名。改对象、扩大动作或范围、换一个服务转发、签名不对、过期、有效期过长，一律拒绝。

返回结果把这些分开写：发起的用户、执行的服务、当前租户、对象、策略版本、授权到期时间，以及登录会话的编号、到期时间、CSRF 令牌和吊销的生效目标。

## 公开入口的保护

网关拒绝客户端自己带的 `X-Principal`、角色、租户、站点、管理员和授权请求头。用 cookie 认证的修改操作必须同时满足：

- `Origin` 和公开地址完全一致；
- 用恒定时间比较会话绑定的 `X-CSRF-Token`。

退出和管理员吊销立即生效，目标是 1 秒内；黑盒测试验证下一个请求就被拒绝。

## 固定的错误代码

公开的 Problem Details 代码包括：`AUTHENTICATION_REQUIRED`、`SESSION_INVALID`、`FORGED_IDENTITY_HEADER`、`OIDC_STATE_INVALID`、`OIDC_PKCE_VALIDATION_FAILED`、`OIDC_ISSUER_INVALID`、`OIDC_AUDIENCE_INVALID`、`OIDC_TOKEN_TYPE_INVALID`、`OIDC_SIGNATURE_INVALID`、`OIDC_NONCE_INVALID`、`OIDC_TOKEN_EXPIRED`、`OIDC_TOKEN_NOT_ACTIVE`、`ORIGIN_NOT_ALLOWED`、`CSRF_TOKEN_INVALID`、`IAM_UNAVAILABLE`、`IAM_IDENTITY_REJECTED`、`SESSION_REVOCATION_FORBIDDEN`。

公开的错误里从不包含令牌、授权码、cookie、授权、身份服务的密钥或 IAM 的原始响应。

## 测试

```bash
npm run contracts:check
npm run test:identity
npm run build:energy-api
npm run build:iam-owner
npm run build:oidc-fixture
npm run lint
npm run build
```

接口测试走真实的跳转和回调，覆盖 PKCE、nonce、签发方、对象、签名、过期、生效时间、令牌类型、双向 TLS、授权的限制、CSRF、Origin、退出和管理员吊销。测试用的 OIDC 服务（`build:oidc-fixture`）支持成功、管理员登录、JWKS 轮换，以及只在自动化测试里用的、按 `login_hint` 触发的拒绝情况。
